package handler

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const binaryCheckSize = 8192

var errStopFileWalk = errors.New("stop file walk")

type fileWalkStats struct {
	DotEntriesSkipped     bool
	FilesSeen             int
	SkippedHidden         int
	SkippedIgnored        int
	SkippedVCS            int
	SkippedUnreadable     int
	HiddenEntriesIncluded int
	VCSEntriesIncluded    int
	VCSEntriesSkipped     int
}

func (h *Handler) searchRoots(path string) ([]string, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("path is required")
	}
	v := h.ResolvePath(path)
	if !v.Ok() {
		return nil, v.Err
	}
	return []string{v.Path}, nil
}

func (h *Handler) collectFilesystemFilesWithIgnore(ctx context.Context, roots []string, includeHidden bool, ignoreGlobs []string) ([]string, error) {
	var files []string
	_, err := h.walkFilesystemFilesWithIgnoreStats(ctx, roots, includeHidden, ignoreGlobs, func(path string) (bool, error) {
		files = append(files, path)
		return true, nil
	})
	return files, err
}

func (h *Handler) collectFilesystemFilesWithIgnoreAndFilter(ctx context.Context, roots []string, includeHidden bool, ignoreGlobs []string, accept func(string) bool) ([]string, error) {
	files, _, err := h.collectFilesystemFilesWithIgnoreAndFilterStats(ctx, roots, includeHidden, ignoreGlobs, accept)
	return files, err
}

func (h *Handler) collectFilesystemFilesWithIgnoreAndFilterStats(ctx context.Context, roots []string, includeHidden bool, ignoreGlobs []string, accept func(string) bool) ([]string, fileWalkStats, error) {
	var files []string
	stats, err := h.walkFilesystemFilesWithIgnoreStats(ctx, roots, includeHidden, ignoreGlobs, func(path string) (bool, error) {
		if accept == nil || accept(path) {
			files = append(files, path)
		}
		return true, nil
	})
	return files, stats, err
}

func (h *Handler) collectFilesystemFilesWithPathContextAndFilterStats(ctx context.Context, pathCtx PathContext, roots []string, includeHidden bool, ignoreGlobs []string, accept func(string) bool) ([]string, fileWalkStats, error) {
	var files []string
	stats, err := h.walkFilesystemFilesWithPathContextStats(ctx, pathCtx, roots, includeHidden, ignoreGlobs, func(path string) (bool, error) {
		if accept == nil || accept(path) {
			files = append(files, path)
		}
		return true, nil
	})
	return files, stats, err
}

func (h *Handler) walkFilesystemFilesWithIgnore(ctx context.Context, roots []string, includeHidden bool, ignoreGlobs []string, visit func(string) (bool, error)) error {
	_, err := h.walkFilesystemFilesWithIgnoreStats(ctx, roots, includeHidden, ignoreGlobs, visit)
	return err
}

func (h *Handler) walkFilesystemFilesWithIgnoreStats(ctx context.Context, roots []string, includeHidden bool, ignoreGlobs []string, visit func(string) (bool, error)) (fileWalkStats, error) {
	return h.walkFilesystemFilesWithPathContextStats(ctx, PathContext{}, roots, includeHidden, ignoreGlobs, visit)
}

func (h *Handler) walkFilesystemFilesWithPathContextStats(ctx context.Context, pathCtx PathContext, roots []string, includeHidden bool, ignoreGlobs []string, visit func(string) (bool, error)) (fileWalkStats, error) {
	return h.walkFilesystemFilesWithPolicyStats(ctx, pathCtx, roots, includeHidden, false, ignoreGlobs, visit)
}

func (h *Handler) walkFilesystemFilesWithPolicyStats(ctx context.Context, pathCtx PathContext, roots []string, includeHidden, includeVCSMetadata bool, ignoreGlobs []string, visit func(string) (bool, error)) (fileWalkStats, error) {
	stats := fileWalkStats{}
	seen := make(map[string]struct{})
	ignoreMatcher := newCompiledIgnoreMatcher(ignoreGlobs)
	for _, root := range roots {
		select {
		case <-ctx.Done():
			return stats, ctx.Err()
		default:
		}
		info, err := os.Stat(root)
		if err != nil {
			return stats, fmt.Errorf("failed to access path %s: %w", root, err)
		}
		if !info.IsDir() {
			if !pathCtx.fileCandidateAllowed(root) {
				continue
			}
			stats.FilesSeen++
			if _, ok := seen[root]; !ok {
				seen[root] = struct{}{}
				keepGoing, err := visit(root)
				if err != nil {
					return stats, err
				}
				if !keepGoing {
					return stats, nil
				}
			}
			continue
		}
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			if err != nil {
				stats.SkippedUnreadable++
				return nil
			}
			if d.IsDir() {
				if path != root && isVCSDirectoryName(d.Name()) {
					if !includeVCSMetadata {
						stats.DotEntriesSkipped = true
						stats.SkippedVCS++
						stats.VCSEntriesSkipped++
						return filepath.SkipDir
					}
					stats.VCSEntriesIncluded++
					return nil
				}
				if path != root && pathInsideVCSMetadata(root, path) && isHighVolumeVCSInternalName(d.Name()) {
					stats.SkippedVCS++
					stats.VCSEntriesSkipped++
					return filepath.SkipDir
				}
				if !includeHidden && path != root && strings.HasPrefix(d.Name(), ".") {
					stats.DotEntriesSkipped = true
					stats.SkippedHidden++
					return filepath.SkipDir
				}
				if includeHidden && path != root && strings.HasPrefix(d.Name(), ".") {
					stats.HiddenEntriesIncluded++
				}
				if path != root && ignoreMatcher.shouldSkipPath(root, path, true) {
					stats.SkippedIgnored++
					return filepath.SkipDir
				}
				return nil
			}
			if isVCSDirectoryName(d.Name()) {
				if !includeVCSMetadata {
					stats.DotEntriesSkipped = true
					stats.SkippedVCS++
					stats.VCSEntriesSkipped++
					return nil
				}
				stats.VCSEntriesIncluded++
			}
			if !includeHidden && strings.HasPrefix(d.Name(), ".") {
				stats.DotEntriesSkipped = true
				stats.SkippedHidden++
				return nil
			}
			if includeHidden && strings.HasPrefix(d.Name(), ".") {
				stats.HiddenEntriesIncluded++
			}
			if pathInsideVCSMetadata(root, path) {
				if !includeVCSMetadata {
					stats.SkippedVCS++
					stats.VCSEntriesSkipped++
					return nil
				}
				stats.VCSEntriesIncluded++
			}
			if ignoreMatcher.shouldSkipPath(root, path, false) {
				stats.SkippedIgnored++
				return nil
			}
			if !pathCtx.fileCandidateAllowed(path) {
				return nil
			}
			stats.FilesSeen++
			if _, ok := seen[path]; !ok {
				seen[path] = struct{}{}
				keepGoing, err := visit(path)
				if err != nil {
					return err
				}
				if !keepGoing {
					return errStopFileWalk
				}
			}
			return nil
		})
		if err != nil && !errors.Is(err, errStopFileWalk) {
			return stats, err
		}
		if errors.Is(err, errStopFileWalk) {
			return stats, nil
		}
	}
	return stats, nil
}

func pathInsideVCSMetadata(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	parts := strings.FieldsFunc(rel, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	for _, part := range parts {
		if isVCSDirectoryName(part) {
			return true
		}
	}
	return false
}

func isHighVolumeVCSInternalName(name string) bool {
	switch name {
	case "objects", "logs", "refs", "hooks", "modules", "worktrees":
		return true
	default:
		return false
	}
}

func isVCSDirectoryName(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", ".jj":
		return true
	default:
		return false
	}
}

func isBinaryFile(data []byte) bool {
	checkSize := binaryCheckSize
	if len(data) < checkSize {
		checkSize = len(data)
	}
	for i := 0; i < checkSize; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}
