package handler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// HandleInspectPath returns cheap metadata for one file-system path.
func (h *Handler) HandleInspectPath(ctx context.Context, req *mcp.CallToolRequest, input InspectPathInput) (*mcp.CallToolResult, InspectPathOutput, error) {
	pathCtx, cwdErr := h.BuildPathContext(input.CwdID)
	if cwdErr != nil {
		return toolCwdError[InspectPathOutput](cwdErr)
	}
	resolvedPath, displayPath, err := h.resolveInspectPath(pathCtx, input.TargetPath, "target_path")
	if err != nil {
		return toolError[InspectPathOutput](fmt.Sprintf("Cannot inspect target_path: %v", err))
	}
	releaseTool, err := h.acquireToolCall(ctx)
	if err != nil {
		return toolError[InspectPathOutput](limiterWaitError("tool call", err))
	}
	defer releaseTool()

	if err := contextError(ctx); err != nil {
		return toolError[InspectPathOutput](err.Error())
	}
	output := h.inspectPathOutputWithContext(ctx, pathCtx, resolvedPath, displayPath, input.DiscoveryContext)
	return structuredResultOnly(), output, nil
}

func (h *Handler) inspectPathOutput(ctx context.Context, pathCtx PathContext, resolvedPath, displayPath string) InspectPathOutput {
	output := InspectPathOutput{
		Path:         displayPath,
		ResolvedPath: h.projectOutputPath(pathCtx, resolvedPath),
		Name:         filepath.Base(resolvedPath),
		Extension:    filepath.Ext(resolvedPath),
		IsHidden:     hasHiddenPathSegment(resolvedPath),
		MimeHint:     mimeHintForPath(resolvedPath),
	}

	info, err := os.Lstat(resolvedPath)
	if err != nil {
		if os.IsNotExist(err) {
			output.Exists = false
			output.Kind = "missing"
			return output
		}
		output.Exists = false
		output.Kind = "unknown"
		output.Error = fmt.Sprintf("Cannot inspect %q: %v", displayPath, err)
		return output
	}
	output.Exists = true
	output.Mode = info.Mode().String()
	output.Permissions = info.Mode().Perm().String()
	output.ModifiedAt = info.ModTime().Format(timeRFC3339Nano)
	output.ModifiedUnixNano = info.ModTime().UnixNano()

	if info.Mode()&os.ModeSymlink != 0 {
		output.Kind = "symlink"
		h.inspectSymlink(pathCtx, resolvedPath, &output)
		return output
	}
	if info.IsDir() {
		output.Kind = "directory"
		h.inspectDirectory(ctx, resolvedPath, &output)
		return output
	}
	if info.Mode().IsRegular() {
		output.Kind = "file"
		size := info.Size()
		output.SizeBytes = &size
		h.inspectFile(ctx, resolvedPath, &output)
		return output
	}
	output.Kind = "other"
	return output
}

func (h *Handler) inspectPathOutputWithContext(ctx context.Context, pathCtx PathContext, resolvedPath, displayPath string, discoveryContext *InspectPathDiscoveryContext) InspectPathOutput {
	output := h.inspectPathOutput(ctx, pathCtx, resolvedPath, displayPath)
	isBinary := output.IsBinary != nil && *output.IsBinary
	output.Visibility = h.pathVisibility(pathCtx, resolvedPath, displayPath, discoveryContext, isBinary)
	return output
}

const timeRFC3339Nano = "2006-01-02T15:04:05.999999999Z07:00"

func (h *Handler) inspectSymlink(pathCtx PathContext, path string, output *InspectPathOutput) {
	target, err := os.Readlink(path)
	if err == nil {
		targetAbs := absoluteSymlinkTarget(path, target)
		if !pathCtx.HasCwd {
			output.SymlinkTarget = h.projectOutputPath(pathCtx, targetAbs)
		} else {
			exposedTarget := targetAbs
			if finalTarget, evalErr := filepath.EvalSymlinks(path); evalErr == nil {
				exposedTarget = finalTarget
			}
			if !pathInsideOrEqual(pathCtx.CwdAbs, exposedTarget) {
				output.SymlinkTargetOutsideCwd = true
			} else {
				output.SymlinkTarget = h.projectOutputPath(pathCtx, exposedTarget)
			}
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		output.BrokenSymlink = true
		return
	}
	switch {
	case info.IsDir():
		output.SymlinkTargetKind = "directory"
	case info.Mode().IsRegular():
		output.SymlinkTargetKind = "file"
	default:
		output.SymlinkTargetKind = "other"
	}
}

func absoluteSymlinkTarget(linkPath, target string) string {
	if target == "" {
		return target
	}
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(linkPath), target))
}

func (h *Handler) inspectDirectory(ctx context.Context, path string, output *InspectPathOutput) {
	dir, err := os.Open(path)
	if err != nil {
		output.IsReadable = false
		return
	}
	defer dir.Close()
	output.IsReadable = true
	fileCount := 0
	dirCount := 0
	for {
		if err := contextError(ctx); err != nil {
			output.Error = err.Error()
			break
		}
		entries, readErr := dir.ReadDir(128)
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			if entry.IsDir() {
				dirCount++
			} else {
				fileCount++
			}
		}
		if readErr == nil {
			continue
		}
		if readErr != io.EOF {
			output.IsReadable = false
		}
		break
	}
	output.DirectFileCount = &fileCount
	output.DirectDirCount = &dirCount
}

func (h *Handler) inspectFile(ctx context.Context, path string, output *InspectPathOutput) {
	file, err := os.Open(path)
	if err != nil {
		output.IsReadable = false
		return
	}
	defer file.Close()
	output.IsReadable = true

	sample := make([]byte, binaryCheckSize)
	n, readErr := file.Read(sample)
	hasTextBOM := n > 0 && hasUnicodeTextBOM(sample[:n])
	if n > 0 && !hasTextBOM && isBinaryFile(sample[:n]) {
		isBinary := true
		output.IsBinary = &isBinary
		return
	}
	if readErr != nil && readErr != io.EOF {
		output.Error = fmt.Sprintf("Cannot inspect file bytes: %v", readErr)
		return
	}
	isBinary := false
	output.IsBinary = &isBinary
	if output.SizeBytes != nil && *output.SizeBytes > h.config.MemoryThreshold {
		releaseLargeRead, err := h.acquireLargeRead(ctx)
		if err != nil {
			output.Error = limiterWaitError("large read", err)
			return
		}
		defer releaseLargeRead()
	}
	if hasTextBOM {
		encResult, err := h.resolveEncodingSample("", path)
		if err != nil {
			return
		}
		lineCount, err := countDecodedTextLinesFast(ctx, path, encResult)
		if err != nil {
			output.Error = fmt.Sprintf("Cannot count lines: %v", err)
			return
		}
		output.LineCount = intPtr(lineCount)
		output.Encoding = encResult.name
		output.DetectedEncoding = encResult.detectedEncoding
		output.EncodingConfidence = encResult.encodingConfidence
		return
	}
	lineCount, err := countTextLinesFast(ctx, file, sample[:n], readErr)
	if err != nil {
		output.Error = fmt.Sprintf("Cannot count lines: %v", err)
		return
	}
	output.LineCount = intPtr(lineCount)
	encResult, err := h.resolveEncodingSample("", path)
	if err != nil {
		return
	}
	output.Encoding = encResult.name
	output.DetectedEncoding = encResult.detectedEncoding
	output.EncodingConfidence = encResult.encodingConfidence
}

func countTextLinesFast(ctx context.Context, file *os.File, firstChunk []byte, firstErr error) (int, error) {
	if firstErr != nil && firstErr != io.EOF {
		return 0, firstErr
	}
	return countLinesInReaderFast(ctx, io.MultiReader(bytes.NewReader(firstChunk), file))
}

func countDecodedTextLinesFast(ctx context.Context, path string, encResult encodingResult) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	return countLinesInReaderFast(ctx, decodedReader(file, encResult))
}

func countLinesInReaderFast(ctx context.Context, reader io.Reader) (int, error) {
	buf := make([]byte, 64*1024)
	lines := 0
	sawBytes := false
	for {
		if err := contextError(ctx); err != nil {
			return 0, err
		}
		n, readErr := reader.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			sawBytes = true
			lines += bytes.Count(chunk, []byte{'\n'})
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return 0, readErr
		}
		if n == 0 {
			break
		}
	}
	if sawBytes {
		lines++
	}
	return lines, nil
}

func mimeHintForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".pdf":
		return "application/pdf"
	case ".zip":
		return "application/zip"
	case ".json":
		return "application/json"
	case ".yaml", ".yml":
		return "application/yaml"
	case ".md", ".markdown":
		return "text/markdown"
	case ".txt", ".log":
		return "text/plain"
	default:
		return ""
	}
}

func (h *Handler) pathVisibility(pathCtx PathContext, resolvedPath, displayPath string, context *InspectPathDiscoveryContext, isBinary bool) *PathVisibility {
	reasons := []VisibilityReason{}
	exists := true
	info, err := os.Lstat(resolvedPath)
	if err != nil {
		exists = false
		if os.IsNotExist(err) {
			reasons = append(reasons, visibilityReason("missing", "The path does not exist."))
		} else {
			reasons = append(reasons, visibilityReason("unreadable_path", "The path metadata could not be read."))
		}
		return &PathVisibility{
			TargetPath:        displayPath,
			Exists:            exists,
			WouldListDirShow:  false,
			WouldGlobMatch:    false,
			WouldGrepTraverse: false,
			Reasons:           reasons,
		}
	}
	hidden := hasHiddenPathSegment(resolvedPath)
	vcs := isPathOrParentVCS(resolvedPath)
	includeHidden := context != nil && context.IncludeHidden
	includeVCS := context != nil && context.IncludeVCSMetadata
	if hidden && !includeHidden && !(vcs && includeVCS) {
		reasons = append(reasons, visibilityReason("hidden_excluded", "Hidden dot-paths are excluded unless include_hidden=true."))
	}
	if vcs && !includeVCS {
		reasons = append(reasons, visibilityReason("vcs_excluded", "VCS metadata is excluded unless include_vcs_metadata=true on discovery tools."))
	}
	if pathCtx.HasCwd && !pathInsideOrEqual(pathCtx.CwdAbs, resolvedPath) {
		reasons = append(reasons, visibilityReason("outside_cwd", "The path is outside the registered cwd."))
	}
	if context != nil {
		root := filepath.Dir(resolvedPath)
		if strings.TrimSpace(context.TargetDirectory) != "" {
			if resolvedRoot, _, err := h.resolveToolPath(pathCtx, context.TargetDirectory, "discovery_context.target_directory"); err == nil {
				root = resolvedRoot
			} else {
				reasons = append(reasons, visibilityReason("invalid_discovery_target_directory", "The discovery_context.target_directory could not be resolved."))
			}
		}
		if len(context.IgnoreGlobs) > 0 {
			matcher := newCompiledIgnoreMatcher(context.IgnoreGlobs)
			if matcher.shouldSkipPath(root, resolvedPath, info != nil && info.IsDir()) {
				reasons = append(reasons, visibilityReason("ignored_by_glob", "The path is excluded by ignore_globs."))
			}
		}
		if strings.TrimSpace(context.GlobPattern) != "" {
			matcher := newCompiledGlobMatcher([]string{normalizeToolPath(context.GlobPattern)})
			rel := relativeGlobCandidate(resolvedPath, root)
			if !matcher.matches(rel) && !matcher.matches(filepath.Base(resolvedPath)) {
				reasons = append(reasons, visibilityReason("glob_mismatch", "The path does not match the provided glob_pattern."))
			}
		}
		if strings.TrimSpace(context.GrepGlob) != "" {
			matcher := newCompiledGlobMatcher([]string{normalizeToolPath(context.GrepGlob)})
			rel := relativeGlobCandidate(resolvedPath, root)
			if !matcher.matches(rel) && !matcher.matches(filepath.Base(resolvedPath)) {
				reasons = append(reasons, visibilityReason("grep_glob_mismatch", "The path does not match the provided grep_glob."))
			}
		}
		if strings.TrimSpace(context.Type) != "" && !matchesFileType(resolvedPath, context.Type) {
			reasons = append(reasons, visibilityReason("type_mismatch", "The path does not match the provided type filter."))
		}
	}
	if isBinary {
		reasons = append(reasons, visibilityReason("binary_excluded", "grep skips binary files."))
	}
	if info != nil && info.Mode()&os.ModeSymlink != 0 && pathCtx.HasCwd {
		if evaluated, err := filepath.EvalSymlinks(resolvedPath); err == nil && !pathInsideOrEqual(pathCtx.CwdAbs, evaluated) {
			reasons = append(reasons, visibilityReason("symlink_target_outside_cwd", "The symlink target resolves outside cwd."))
		}
	}
	wouldShow := !hasAnyVisibilityReason(reasons, "hidden_excluded", "vcs_excluded", "outside_cwd", "ignored_by_glob", "invalid_discovery_target_directory", "symlink_target_outside_cwd")
	return &PathVisibility{
		TargetPath:        displayPath,
		Exists:            exists,
		WouldListDirShow:  wouldShow,
		WouldGlobMatch:    wouldShow && !(context != nil && context.GlobPattern != "" && hasVisibilityReason(reasons, "glob_mismatch")),
		WouldGrepTraverse: wouldShow && !vcs && !hasAnyVisibilityReason(reasons, "grep_glob_mismatch", "type_mismatch", "binary_excluded"),
		Reasons:           reasons,
	}
}

func visibilityReason(code, message string) VisibilityReason {
	return VisibilityReason{Code: code, Message: message}
}

func hasVisibilityReason(reasons []VisibilityReason, code string) bool {
	for _, reason := range reasons {
		if reason.Code == code {
			return true
		}
	}
	return false
}

func hasAnyVisibilityReason(reasons []VisibilityReason, codes ...string) bool {
	for _, code := range codes {
		if hasVisibilityReason(reasons, code) {
			return true
		}
	}
	return false
}

func isPathOrParentVCS(path string) bool {
	if hasVCSPathSegment(path) {
		return true
	}
	for {
		name := filepath.Base(path)
		if isVCSDirectoryName(name) {
			return true
		}
		parent := filepath.Dir(path)
		if parent == path || parent == "." {
			return false
		}
		path = parent
	}
}
