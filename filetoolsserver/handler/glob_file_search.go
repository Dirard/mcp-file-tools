package handler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fileWithMtime struct {
	Path        string
	DisplayPath string
	Mtime       time.Time
	Size        int64
}

// HandleGlobFileSearch finds files by glob pattern.
func (h *Handler) HandleGlobFileSearch(ctx context.Context, req *mcp.CallToolRequest, input GlobFileSearchInput) (*mcp.CallToolResult, GlobFileSearchOutput, error) {
	if input.GlobPattern == "" {
		return toolError[GlobFileSearchOutput]("glob_pattern is required.\n\nExample: glob_file_search(glob_pattern=\"*.go\")")
	}
	pathCtx, cwdErr := h.BuildPathContext(input.CwdID)
	if cwdErr != nil {
		return toolCwdError[GlobFileSearchOutput](cwdErr)
	}
	limit, err := effectiveOptionalLimit(input.Limit, defaultSearchLimit)
	if err != nil {
		return toolError[GlobFileSearchOutput](err.Error())
	}
	sortMode, err := normalizeGlobSort(input.Sort)
	if err != nil {
		return toolError[GlobFileSearchOutput](err.Error())
	}
	resolvedDirectory, displayDirectory, err := h.resolveToolPath(pathCtx, input.TargetDirectory, "target_directory")
	if err != nil {
		return toolError[GlobFileSearchOutput](fmt.Sprintf("Cannot search target_directory: %v", err))
	}
	roots := []string{resolvedDirectory}
	rootInfo, err := os.Stat(roots[0])
	if err != nil {
		return toolError[GlobFileSearchOutput](fmt.Sprintf("Cannot search target_directory %q: %v\n\nCheck that the directory exists and is readable.", displayDirectory, err))
	}
	if !rootInfo.IsDir() {
		return toolError[GlobFileSearchOutput](fmt.Sprintf("%q is a file, not a directory.\n\nUse grep for content search in a single file, or choose the parent directory for glob_file_search.", displayDirectory))
	}
	releaseTool, err := h.acquireToolCall(ctx)
	if err != nil {
		return toolError[GlobFileSearchOutput](limiterWaitError("tool call", err))
	}
	defer releaseTool()

	queryHash := globQueryHash(pathCtx, input, sortMode, limit)
	if input.ContinuationAfter != nil && input.ContinuationAfter.CanonicalQueryHash != queryHash {
		message := "continuation_query_mismatch: continuation_after does not match the current glob_file_search query"
		return errorResult(message), GlobFileSearchOutput{
			Error:              message,
			ErrorCode:          "continuation_query_mismatch",
			Pattern:            input.GlobPattern,
			TargetDirectory:    displayDirectory,
			Sort:               sortMode,
			IncludeHidden:      input.IncludeHidden,
			IncludeVCSMetadata: input.IncludeVCSMetadata,
			Limit:              limit,
			Files:              []GlobFileMatch{},
			Groups:             []GlobDirectoryGroup{},
		}, nil
	}
	filesWithMtime, totalMatches, walkStats, err := h.globFilesWithMtime(ctx, pathCtx, roots, input, sortMode)
	if err != nil {
		return toolError[GlobFileSearchOutput](fmt.Sprintf("Cannot collect files for glob search: %v", err))
	}
	filesWithMtime = applyGlobContinuation(filesWithMtime, sortMode, input.ContinuationAfter)
	page := filesWithMtime
	if limit > 0 && len(page) > limit {
		page = page[:limit]
	}
	if totalMatches == 0 {
		message := fmt.Sprintf("No files matched pattern %q. Try a broader pattern, choose a different target_directory, adjust ignore_globs, or use ** for nested paths.", input.GlobPattern)
		return structuredResultOnly(), GlobFileSearchOutput{
			Text:                  fmt.Sprintf("No files matched pattern %q.\n\nTry a broader pattern, choose a different target_directory, adjust ignore_globs, or use ** for nested paths.\n", input.GlobPattern),
			Pattern:               input.GlobPattern,
			TargetDirectory:       displayDirectory,
			Sort:                  sortMode,
			IncludeHidden:         input.IncludeHidden,
			IncludeVCSMetadata:    input.IncludeVCSMetadata,
			Limit:                 limit,
			DotEntriesSkipped:     walkStats.DotEntriesSkipped,
			HiddenEntriesIncluded: walkStats.HiddenEntriesIncluded,
			VCSEntriesSkipped:     walkStats.VCSEntriesSkipped,
			VCSEntriesIncluded:    walkStats.VCSEntriesIncluded,
			Files:                 []GlobFileMatch{},
			Groups:                []GlobDirectoryGroup{},
			SearchStats:           searchStatsFromWalkStats(walkStats, true, ""),
			Continuation:          completeContinuation(queryHash),
			Message:               message,
		}, nil
	}

	output := h.globFileSearchOutput(pathCtx, page, totalMatches, walkStats, input, sortMode, queryHash, displayDirectory, input.TargetDirectory, roots[0], limit, len(filesWithMtime) > len(page))
	return structuredResultOnly(), output, nil
}

func (h *Handler) globFilesWithMtime(ctx context.Context, pathCtx PathContext, roots []string, input GlobFileSearchInput, sortMode string) ([]fileWithMtime, int, fileWalkStats, error) {
	releaseScan, err := h.acquireScan(ctx)
	if err != nil {
		return nil, 0, fileWalkStats{}, fmt.Errorf("%s", limiterWaitError("scan", err))
	}
	defer releaseScan()

	matcher := newCompiledGlobMatcher([]string{normalizeToolPath(input.GlobPattern)})
	filesWithMtime := make([]fileWithMtime, 0)
	totalMatches := 0
	stats, err := h.walkFilesystemFilesWithPolicyStats(ctx, pathCtx, roots, input.IncludeHidden, input.IncludeVCSMetadata, input.IgnoreGlobs, func(file string) (bool, error) {
		if err := contextError(ctx); err != nil {
			return false, err
		}
		normalizedFile := normalizeToolPath(file)
		relativeFile := relativeGlobCandidate(file, roots[0])
		if !matcher.matches(relativeFile) && !matcher.matches(filepath.Base(normalizedFile)) {
			return true, nil
		}
		totalMatches++
		mtime := time.Unix(0, 0)
		size := int64(0)
		if info, err := os.Stat(file); err == nil {
			mtime = info.ModTime()
			size = info.Size()
		}
		filesWithMtime = append(filesWithMtime, fileWithMtime{
			Path:        file,
			DisplayPath: h.projectSearchPath(pathCtx, file, input.TargetDirectory, roots[0]),
			Mtime:       mtime,
			Size:        size,
		})
		return true, nil
	})
	if err != nil {
		return nil, 0, stats, err
	}
	sortGlobFiles(filesWithMtime, sortMode)
	return filesWithMtime, totalMatches, stats, nil
}

func matchFilesWithContext(ctx context.Context, files []string, globPattern, targetDirectory string) ([]string, error) {
	normalizedPattern := normalizeToolPath(globPattern)
	matcher := newCompiledGlobMatcher([]string{normalizedPattern})
	var matched []string
	for _, file := range files {
		if err := contextError(ctx); err != nil {
			return matched, err
		}
		normalizedFile := normalizeToolPath(file)
		relativeFile := relativeGlobCandidate(file, targetDirectory)
		if matcher.matches(relativeFile) || matcher.matches(filepath.Base(normalizedFile)) {
			matched = append(matched, file)
		}
	}
	return matched, nil
}

func relativeGlobCandidate(file, targetDirectory string) string {
	if strings.TrimSpace(targetDirectory) == "" {
		return normalizeToolPath(file)
	}
	rel, err := filepath.Rel(targetDirectory, file)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return normalizeToolPath(file)
	}
	return normalizeToolPath(rel)
}

func filesWithModTimeWithContext(ctx context.Context, files []string) ([]fileWithMtime, error) {
	result := make([]fileWithMtime, 0, len(files))
	for _, file := range files {
		if err := contextError(ctx); err != nil {
			return result, err
		}
		mtime := time.Unix(0, 0)
		if info, err := os.Stat(file); err == nil {
			mtime = info.ModTime()
		}
		result = append(result, fileWithMtime{Path: file, Mtime: mtime})
	}
	return result, nil
}

func sortFilesNewestFirst(files []fileWithMtime) {
	sortGlobFiles(files, "modified_desc")
}

func sortGlobFiles(files []fileWithMtime, sortMode string) {
	sort.SliceStable(files, func(i, j int) bool {
		return globSortCompare(globSortKey(files[i], sortMode), globSortKey(files[j], sortMode), sortMode) < 0
	})
}

func (h *Handler) formatGlobRows(pathCtx PathContext, files []fileWithMtime, requestedRoot, resolvedRoot string) []string {
	rows := make([]string, 0, len(files))
	for _, file := range files {
		mtime := "unknown"
		if file.Mtime.Unix() != 0 {
			mtime = file.Mtime.Format("2006-01-02 15:04:05")
		}
		rows = append(rows, fmt.Sprintf("%s (modified: %s)", h.projectSearchPath(pathCtx, file.Path, requestedRoot, resolvedRoot), mtime))
	}
	return rows
}

func (h *Handler) globFileSearchOutput(pathCtx PathContext, files []fileWithMtime, totalMatches int, walkStats fileWalkStats, input GlobFileSearchInput, sortMode, queryHash, displayDirectory, requestedRoot, resolvedRoot string, limit int, truncated bool) GlobFileSearchOutput {
	rows := h.formatGlobRows(pathCtx, files, requestedRoot, resolvedRoot)
	legacyText := fmt.Sprintf("Found %d files (sort: %s):\n\n", len(files), sortMode)
	if len(rows) > 0 {
		legacyText += strings.Join(rows, "\n") + "\n"
	}
	matches := make([]GlobFileMatch, 0, len(files))
	for _, file := range files {
		match := GlobFileMatch{Path: h.projectSearchPath(pathCtx, file.Path, requestedRoot, resolvedRoot)}
		if file.Mtime.Unix() != 0 {
			match.ModifiedAt = file.Mtime.Format(time.RFC3339Nano)
			match.ModifiedUnixNano = file.Mtime.UnixNano()
		}
		if sortMode == "size_desc" || sortMode == "size_asc" {
			size := file.Size
			match.SizeBytes = &size
		}
		matches = append(matches, match)
	}
	continuation := globContinuationHint(pathCtx, input, sortMode, queryHash, limit, files, truncated)
	nextCalls := globNextRecommendedCalls(pathCtx, matches, truncated)
	output := GlobFileSearchOutput{
		Text:                  legacyText,
		Pattern:               input.GlobPattern,
		TargetDirectory:       displayDirectory,
		Sort:                  sortMode,
		IncludeHidden:         input.IncludeHidden,
		IncludeVCSMetadata:    input.IncludeVCSMetadata,
		Limit:                 limit,
		Count:                 len(matches),
		TotalMatchCount:       totalMatches,
		Truncated:             truncated,
		DotEntriesSkipped:     walkStats.DotEntriesSkipped,
		HiddenEntriesIncluded: walkStats.HiddenEntriesIncluded,
		VCSEntriesSkipped:     walkStats.VCSEntriesSkipped,
		VCSEntriesIncluded:    walkStats.VCSEntriesIncluded,
		Files:                 matches,
		Groups:                globDirectoryGroups(matches, sortMode),
		SearchStats:           searchStatsFromWalkStats(walkStats, !truncated, stopReasonForTruncated(truncated)),
		Continuation:          continuation,
	}
	if len(nextCalls) > 0 {
		output.NextRecommendedCalls = nextCalls
		output.NextRecommendedCall = &output.NextRecommendedCalls[0]
	}
	return output
}

func normalizeGlobSort(sortMode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(sortMode)) {
	case "", "modified_desc":
		return "modified_desc", nil
	case "modified_asc", "path_asc", "path_desc", "size_desc", "size_asc", "directory_path_asc":
		return strings.ToLower(strings.TrimSpace(sortMode)), nil
	default:
		return "", fmt.Errorf("invalid sort %q; use modified_desc, modified_asc, path_asc, path_desc, size_desc, size_asc, or directory_path_asc", sortMode)
	}
}

func globSortKey(file fileWithMtime, sortMode string) DiscoverySortKey {
	pathValue := file.DisplayPath
	if pathValue == "" {
		pathValue = slashPath(file.Path)
	}
	key := DiscoverySortKey{Path: pathValue}
	switch sortMode {
	case "modified_desc", "modified_asc":
		v := file.Mtime.UnixNano()
		key.ModifiedUnixNano = &v
	case "size_desc", "size_asc":
		v := file.Size
		key.SizeBytes = &v
	}
	return key
}

func globSortCompare(left, right DiscoverySortKey, sortMode string) int {
	switch sortMode {
	case "modified_desc", "modified_asc":
		l := int64(0)
		r := int64(0)
		if left.ModifiedUnixNano != nil {
			l = *left.ModifiedUnixNano
		}
		if right.ModifiedUnixNano != nil {
			r = *right.ModifiedUnixNano
		}
		if l != r {
			if sortMode == "modified_desc" && l > r || sortMode == "modified_asc" && l < r {
				return -1
			}
			return 1
		}
	case "size_desc", "size_asc":
		l := int64(0)
		r := int64(0)
		if left.SizeBytes != nil {
			l = *left.SizeBytes
		}
		if right.SizeBytes != nil {
			r = *right.SizeBytes
		}
		if l != r {
			if sortMode == "size_desc" && l > r || sortMode == "size_asc" && l < r {
				return -1
			}
			return 1
		}
	}
	if left.Path == right.Path {
		return 0
	}
	if sortMode == "path_desc" {
		if left.Path > right.Path {
			return -1
		}
		return 1
	}
	if left.Path < right.Path {
		return -1
	}
	return 1
}

func applyGlobContinuation(files []fileWithMtime, sortMode string, continuation *DiscoveryContinuationAfter) []fileWithMtime {
	if continuation == nil {
		return files
	}
	idx := 0
	for idx < len(files) {
		key := globSortKey(files[idx], sortMode)
		if globSortCompare(key, continuation.LastSortKey, sortMode) > 0 {
			break
		}
		idx++
	}
	return files[idx:]
}

func globQueryHash(pathCtx PathContext, input GlobFileSearchInput, sortMode string, limit int) string {
	ignore := append([]string(nil), input.IgnoreGlobs...)
	sort.Strings(ignore)
	return canonicalHash(map[string]any{
		"tool":                 "glob_file_search",
		"cwd":                  pathCtx.HasCwd,
		"cwd_id":               pathCtx.CwdID,
		"target_directory":     slashPath(input.TargetDirectory),
		"glob_pattern":         normalizeToolPath(input.GlobPattern),
		"ignore_globs":         ignore,
		"include_hidden":       input.IncludeHidden,
		"include_vcs_metadata": input.IncludeVCSMetadata,
		"sort":                 sortMode,
		"limit":                limit,
	})
}

func globContinuationHint(pathCtx PathContext, input GlobFileSearchInput, sortMode, queryHash string, limit int, files []fileWithMtime, truncated bool) *ContinuationHint {
	hint := &ContinuationHint{
		Complete:           !truncated,
		Consistency:        "unknown",
		CanonicalQueryHash: queryHash,
	}
	if !truncated || len(files) == 0 {
		if !truncated {
			hint.Reason = "The page is complete, but directory tree stability is not proven between calls."
		}
		return hint
	}
	lastKey := globSortKey(files[len(files)-1], sortMode)
	hint.LastSortKey = &lastKey
	inputMap := map[string]any{
		"target_directory": input.TargetDirectory,
		"glob_pattern":     input.GlobPattern,
		"sort":             sortMode,
		"limit":            limit,
		"continuation_after": map[string]any{
			"canonical_query_hash": queryHash,
			"last_sort_key":        sortKeyInputMap(lastKey),
		},
	}
	if len(input.IgnoreGlobs) > 0 {
		inputMap["ignore_globs"] = append([]string(nil), input.IgnoreGlobs...)
	}
	if input.IncludeHidden {
		inputMap["include_hidden"] = true
	}
	if input.IncludeVCSMetadata {
		inputMap["include_vcs_metadata"] = true
	}
	addCwdIDToRecommendedInput(pathCtx, "glob_file_search", inputMap)
	action := ActionHint{
		SafeToRetry:                true,
		RecommendedNextTool:        "glob_file_search",
		RecommendedNextInput:       inputMap,
		RecommendedNextInputPolicy: "continue_glob_page",
		Reason:                     "Continue this stateless glob page using the query hash and last sort key.",
	}
	hint.NextRecommendedCall = &action
	hint.NextRecommendedCalls = []ActionHint{action}
	hint.Reason = "The result is truncated; continuation is stateless and assumes the tree has not changed."
	return hint
}

func sortKeyInputMap(key DiscoverySortKey) map[string]any {
	out := map[string]any{"path": key.Path}
	if key.ModifiedUnixNano != nil {
		out["modified_unix_nano"] = *key.ModifiedUnixNano
	}
	if key.SizeBytes != nil {
		out["size_bytes"] = *key.SizeBytes
	}
	return out
}

func completeContinuation(queryHash string) *ContinuationHint {
	return &ContinuationHint{
		Complete:           true,
		Consistency:        "unknown",
		CanonicalQueryHash: queryHash,
		Reason:             "The page is complete, but directory tree stability is not proven between calls.",
	}
}

func globNextRecommendedCalls(pathCtx PathContext, matches []GlobFileMatch, truncated bool) []ActionHint {
	if truncated || len(matches) == 0 {
		return nil
	}
	calls := []ActionHint{}
	if len(matches) == 1 && (isSourceLikePath(matches[0].Path) || isConfigLikePath(matches[0].Path)) {
		input := map[string]any{
			"target_file":    matches[0].Path,
			"output_profile": outlineProfileAgent,
		}
		addCwdIDToRecommendedInput(pathCtx, "outline_file", input)
		calls = append(calls, ActionHint{
			SafeToRetry:                true,
			RecommendedNextTool:        "outline_file",
			RecommendedNextInput:       input,
			RecommendedNextInputPolicy: "inspect_single_glob_match_structure",
			Reason:                     "Exactly one source/config-like file matched; outline it before choosing ranges or edits.",
		})
	}
	if len(matches) <= 6 && allGlobMatchesTextLike(matches) {
		items := make([]map[string]any, 0, len(matches))
		for _, match := range matches {
			items = append(items, map[string]any{"target_file": match.Path})
		}
		input := map[string]any{"items": items}
		addCwdIDToRecommendedInput(pathCtx, "read_files", input)
		calls = append(calls, ActionHint{
			SafeToRetry:                true,
			RecommendedNextTool:        "read_files",
			RecommendedNextInput:       input,
			RecommendedNextInputPolicy: "read_bounded_glob_matches",
			Reason:                     "Read this bounded text-like glob result in one compact call.",
		})
	}
	return calls
}

func allGlobMatchesTextLike(matches []GlobFileMatch) bool {
	for _, match := range matches {
		if !isTextLikePath(match.Path) {
			return false
		}
	}
	return true
}

func searchStatsFromWalkStats(stats fileWalkStats, completed bool, stopReason string) *GrepSearchStats {
	return &GrepSearchStats{
		FilesSeen:         stats.FilesSeen,
		SkippedHidden:     stats.SkippedHidden,
		SkippedIgnored:    stats.SkippedIgnored,
		SkippedVCS:        stats.SkippedVCS,
		SkippedUnreadable: stats.SkippedUnreadable,
		Completed:         completed,
		StopReason:        stopReason,
		CountsAreComplete: completed,
	}
}

func stopReasonForTruncated(truncated bool) string {
	if truncated {
		return "limit"
	}
	return ""
}

func globDirectoryGroups(files []GlobFileMatch, sortMode string) []GlobDirectoryGroup {
	if sortMode != "directory_path_asc" {
		return nil
	}
	byDir := map[string][]GlobFileMatch{}
	for _, file := range files {
		dir := slashPath(filepath.Dir(file.Path))
		if dir == "." {
			dir = ""
		}
		byDir[dir] = append(byDir[dir], file)
	}
	dirs := make([]string, 0, len(byDir))
	for dir := range byDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	groups := make([]GlobDirectoryGroup, 0, len(dirs))
	for _, dir := range dirs {
		groups = append(groups, GlobDirectoryGroup{Directory: dir, Count: len(byDir[dir]), Files: byDir[dir]})
	}
	return groups
}

func normalizeToolPath(path string) string {
	return filepath.ToSlash(strings.TrimSpace(path))
}

func appendBoundedNewest(files []fileWithMtime, candidate fileWithMtime, limit int) []fileWithMtime {
	if limit <= 0 {
		return files
	}
	if len(files) < limit {
		return append(files, candidate)
	}
	oldestIdx := 0
	for i := 1; i < len(files); i++ {
		if fileOlderThan(files[i], files[oldestIdx]) {
			oldestIdx = i
		}
	}
	if fileOlderThan(candidate, files[oldestIdx]) || candidate.Mtime.Equal(files[oldestIdx].Mtime) && candidate.Path >= files[oldestIdx].Path {
		return files
	}
	files[oldestIdx] = candidate
	return files
}

func fileOlderThan(left, right fileWithMtime) bool {
	if !left.Mtime.Equal(right.Mtime) {
		return left.Mtime.Before(right.Mtime)
	}
	return left.Path > right.Path
}
