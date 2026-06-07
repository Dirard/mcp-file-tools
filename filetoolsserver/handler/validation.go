package handler

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// shouldLoadEntireFile returns true if the file is small enough to load into memory.
// Returns (shouldLoad, fileSize). On stat error, defaults to true (load into memory).
func (h *Handler) shouldLoadEntireFile(path string) (bool, int64) {
	info, err := os.Stat(path)
	if err != nil {
		return true, 0
	}
	return info.Size() <= h.config.MemoryThreshold, info.Size()
}

// PathResolutionResult holds the result of resolving a user-provided path.
type PathResolutionResult struct {
	Path   string
	Result *mcp.CallToolResult
	Err    error
}

// Ok returns true if path resolution succeeded.
func (r PathResolutionResult) Ok() bool {
	return r.Err == nil
}

// ResolvePath checks that a path is non-empty and normalizes it to an absolute path.
func (h *Handler) ResolvePath(path string) PathResolutionResult {
	if path == "" {
		return PathResolutionResult{
			Result: errorResult(ErrPathRequired.Error()),
			Err:    ErrPathRequired,
		}
	}

	validatedPath, err := h.normalizeInputPath(path)
	if err != nil {
		return PathResolutionResult{
			Result: errorResult(err.Error()),
			Err:    err,
		}
	}

	return PathResolutionResult{Path: validatedPath}
}

func (h *Handler) normalizeInputPath(path string) (string, error) {
	mapped := h.mapInputPath(path)
	cleaned := filepath.Clean(mapped)
	if filepath.IsAbs(cleaned) {
		return cleaned, nil
	}
	return "", fmt.Errorf("path %q is not an absolute path for this server OS", path)
}

func (h *Handler) mapInputPath(path string) string {
	inputPath := strings.TrimSpace(path)
	inputKey := normalizePathMapPath(inputPath)
	if inputKey == "" {
		return slashPath(path)
	}
	for _, pathMap := range h.config.PathMaps {
		if !pathMapUsableForInput(pathMap.Source, pathMap.Target) {
			continue
		}
		sourcePath := normalizePathMapPath(pathMap.Source)
		if sourcePath == "" || !pathMapMatches(inputKey, sourcePath, runtime.GOOS == "windows") {
			continue
		}
		inputSlashPath := strings.ReplaceAll(inputPath, "\\", "/")
		suffix := strings.TrimLeft(inputSlashPath[len(sourcePath):], "/")
		target := normalizePathMapTarget(pathMap.Target)
		if suffix == "" {
			return target
		}
		return joinNormalizedPathMapTarget(target, suffix)
	}
	return path
}

func (h *Handler) displayPath(path string) string {
	inputPath := strings.TrimSpace(path)
	inputKey := normalizePathMapPath(inputPath)
	if inputKey == "" {
		return path
	}
	for _, pathMap := range h.config.PathMaps {
		if !pathMapUsableForDisplay(pathMap.Source, pathMap.Target) {
			continue
		}
		targetPath := normalizePathMapPath(pathMap.Target)
		if targetPath == "" || !pathMapMatches(inputKey, targetPath, runtime.GOOS == "windows") {
			continue
		}
		inputSlashPath := strings.ReplaceAll(inputPath, "\\", "/")
		suffix := strings.TrimLeft(inputSlashPath[len(targetPath):], "/")
		sourceRoot := displayPathMapRoot(pathMap.Source)
		if suffix == "" {
			return slashPath(sourceRoot)
		}
		return slashPath(joinDisplayPathSuffix(sourceRoot, "/", suffix))
	}
	return slashPath(path)
}

func (h *Handler) displayResolvedPath(requestedPath, resolvedPath string) string {
	return slashPath(h.displayPath(resolvedPath))
}

func (h *Handler) displaySearchPath(file, requestedRoot, resolvedRoot string) string {
	display := h.displayPath(file)
	if display != file {
		return slashPath(display)
	}
	requestedRoot = strings.TrimSpace(requestedRoot)
	if requestedRoot == "" {
		requestedRoot = "."
	}
	if isAbsoluteToolPath(requestedRoot) {
		return slashPath(display)
	}
	rel, err := filepath.Rel(resolvedRoot, file)
	if err != nil {
		return display
	}
	return slashPath(joinRelativeDisplayPath(requestedRoot, rel))
}

func joinRelativeDisplayPath(requestedRoot, rel string) string {
	separator := "/"
	requestedRoot = strings.TrimRight(requestedRoot, `/\`)
	if requestedRoot == "" {
		requestedRoot = "."
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return slashPath(requestedRoot)
	}
	if requestedRoot == "." {
		return rel
	}
	return slashPath(requestedRoot + separator + rel)
}

func displayPathMapRoot(path string) string {
	root := strings.TrimSpace(path)
	trimmedRoot := strings.TrimRight(root, `/\`)
	if trimmedRoot == "" {
		if strings.HasPrefix(root, "/") {
			return "/"
		}
		return root
	}
	if strings.HasSuffix(trimmedRoot, ":") {
		return trimmedRoot + "/"
	}
	return slashPath(trimmedRoot)
}

func joinDisplayPathSuffix(root, separator, suffix string) string {
	if strings.HasSuffix(root, "/") || strings.HasSuffix(root, "\\") {
		return root + suffix
	}
	return root + separator + suffix
}

func isAbsoluteToolPath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	return filepath.IsAbs(path)
}

func normalizePathMapPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = strings.ReplaceAll(path, "\\", "/")
	path = strings.TrimRight(path, "/")
	if path == "" {
		return "/"
	}
	return path
}

func normalizePathMapTarget(path string) string {
	normalized := normalizePathMapPath(path)
	if strings.HasSuffix(normalized, ":") {
		return normalized + "/"
	}
	return normalized
}

func joinNormalizedPathMapTarget(target, suffix string) string {
	if strings.HasSuffix(target, "/") {
		return target + suffix
	}
	return target + "/" + suffix
}

func pathMapMatches(input, source string, caseInsensitive bool) bool {
	if caseInsensitive {
		input = strings.ToLower(input)
		source = strings.ToLower(source)
	}
	if input == source {
		return true
	}
	if source == "/" {
		return strings.HasPrefix(input, "/")
	}
	return strings.HasPrefix(input, source+"/")
}

func pathMapUsableForInput(source, target string) bool {
	source = filepath.Clean(strings.TrimSpace(source))
	target = filepath.Clean(strings.TrimSpace(target))
	return source != "" && target != "" && filepath.IsAbs(source) && filepath.IsAbs(target)
}

func pathMapUsableForDisplay(source, target string) bool {
	return pathMapUsableForInput(source, target)
}
