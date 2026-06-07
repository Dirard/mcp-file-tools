package handler

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// HandleWorkspaceInventory builds a shallow project map made only of directories.
func (h *Handler) HandleWorkspaceInventory(ctx context.Context, req *mcp.CallToolRequest, input WorkspaceInventoryInput) (*mcp.CallToolResult, WorkspaceInventoryOutput, error) {
	pathCtx, cwdErr := h.BuildPathContext(input.CwdID)
	if cwdErr != nil {
		return toolCwdError[WorkspaceInventoryOutput](cwdErr)
	}
	maxDepth, err := effectiveOptionalNonNegative(input.MaxDepth, defaultWorkspaceInventoryDepth, "max_depth")
	if err != nil {
		return toolError[WorkspaceInventoryOutput](err.Error())
	}
	limit, err := effectiveOptionalLimit(input.Limit, defaultWorkspaceInventoryLimit)
	if err != nil {
		return toolError[WorkspaceInventoryOutput](err.Error())
	}
	summaryProfile, err := normalizeWorkspaceSummaryProfile(input.SummaryProfile)
	if err != nil {
		return errorResult(err.Error()), WorkspaceInventoryOutput{
			Error:                 err.Error(),
			ErrorCode:             "invalid_summary_profile",
			DirectoriesPage:       []WorkspaceDirectoryPageEntry{},
			MaxDepth:              maxDepth,
			Limit:                 limit,
			IncludeHidden:         input.IncludeHidden,
			IncludeVCSMetadata:    input.IncludeVCSMetadata,
			IgnoredDirectoryCount: 0,
		}, nil
	}
	input.SummaryProfile = summaryProfile
	resolvedDirectory, _, err := h.resolveToolPath(pathCtx, input.TargetDirectory, "target_directory")
	if err != nil {
		return toolError[WorkspaceInventoryOutput](fmt.Sprintf("Cannot inspect target_directory: %v", err))
	}
	stat, err := os.Stat(resolvedDirectory)
	if err != nil {
		return toolError[WorkspaceInventoryOutput](fmt.Sprintf("Cannot access target_directory: %v", pathCtx.sanitizeErrorText(err.Error())))
	}
	if !stat.IsDir() {
		return toolError[WorkspaceInventoryOutput]("target_directory is a file, not a directory.\n\nUse inspect_path for one path or choose a directory for workspace_inventory.")
	}
	releaseTool, err := h.acquireToolCall(ctx)
	if err != nil {
		return toolError[WorkspaceInventoryOutput](limiterWaitError("tool call", err))
	}
	defer releaseTool()

	queryHash := workspaceInventoryQueryHash(pathCtx, input, maxDepth, limit)
	if input.ContinuationAfter != nil && input.ContinuationAfter.CanonicalQueryHash != queryHash {
		message := "continuation_query_mismatch: continuation_after does not match the current workspace_inventory query"
		return errorResult(message), WorkspaceInventoryOutput{
			Error:                 message,
			ErrorCode:             "continuation_query_mismatch",
			DirectoriesPage:       []WorkspaceDirectoryPageEntry{},
			MaxDepth:              maxDepth,
			Limit:                 limit,
			IncludeHidden:         input.IncludeHidden,
			IncludeVCSMetadata:    input.IncludeVCSMetadata,
			IgnoredDirectoryCount: 0,
		}, nil
	}
	builder := workspaceInventoryBuilder{
		ctx:               ctx,
		handler:           h,
		pathCtx:           pathCtx,
		root:              resolvedDirectory,
		requestedRoot:     input.TargetDirectory,
		maxDepth:          maxDepth,
		pageLimit:         limit,
		scanLimit:         workspaceInventoryScanLimit(limit),
		continuationAfter: input.ContinuationAfter,
		ignoreMatcher:     newCompiledIgnoreMatcher(input.IgnoreGlobs),
		includeHidden:     input.IncludeHidden,
		includeVCS:        input.IncludeVCSMetadata,
		fileTypeCounts:    map[string]int{},
		packageHints:      map[string]bool{},
		sourceDirHints:    map[string]bool{},
		testDirHints:      map[string]bool{},
		backupDirs:        map[string]int{},
	}
	root := builder.build(resolvedDirectory, 0, "")
	directoriesPage := builder.directoriesPage
	output := WorkspaceInventoryOutput{
		Root:                  &root,
		DirectoriesPage:       directoriesPage,
		MaxDepth:              maxDepth,
		Limit:                 limit,
		DirectoryCount:        builder.directoryCount,
		IgnoredDirectoryCount: builder.ignoredDirectoryCount,
		IncludeHidden:         input.IncludeHidden,
		IncludeVCSMetadata:    input.IncludeVCSMetadata,
		DotEntriesSkipped:     builder.dotEntriesSkipped,
		HiddenEntriesIncluded: builder.hiddenEntriesIncluded,
		VCSEntriesSkipped:     builder.vcsEntriesSkipped,
		VCSEntriesIncluded:    builder.vcsEntriesIncluded,
		Truncated:             builder.truncated,
		TruncationReason:      builder.truncationReason,
		MaxDepthReached:       builder.maxDepthReached,
	}
	if (input.IncludeSummary == nil || *input.IncludeSummary) && summaryProfile != "none" {
		output.Summary = builder.summary(pathCtx, summaryProfile, directoriesPage)
	}
	output.Continuation = workspaceInventoryContinuation(pathCtx, input, queryHash, limit, directoriesPage, builder.truncated, builder.truncationReason)
	output.NextRecommendedCalls = workspaceInventoryNextRecommendedCalls(pathCtx, directoriesPage, builder.truncated)
	if len(output.NextRecommendedCalls) > 0 {
		output.NextRecommendedCall = &output.NextRecommendedCalls[0]
	}
	return structuredResultOnly(), output, nil
}

type workspaceInventoryBuilder struct {
	ctx                   context.Context
	handler               *Handler
	pathCtx               PathContext
	root                  string
	requestedRoot         string
	maxDepth              int
	pageLimit             int
	scanLimit             int
	continuationAfter     *DiscoveryContinuationAfter
	ignoreMatcher         compiledIgnoreMatcher
	includeHidden         bool
	includeVCS            bool
	directoryCount        int
	ignoredDirectoryCount int
	ignoredEntriesSkipped int
	dotEntriesSkipped     bool
	hiddenEntriesIncluded int
	hiddenEntriesSkipped  int
	vcsEntriesSkipped     int
	vcsEntriesIncluded    int
	truncated             bool
	truncationReason      string
	maxDepthReached       bool
	fileTypeCounts        map[string]int
	packageHints          map[string]bool
	sourceDirHints        map[string]bool
	testDirHints          map[string]bool
	backupDirs            map[string]int
	directoriesPage       []WorkspaceDirectoryPageEntry
	pageFull              bool
	directoriesScanned    int
}

func (b *workspaceInventoryBuilder) build(path string, depth int, parentPath string) WorkspaceDirectoryNode {
	node := WorkspaceDirectoryNode{
		Name:        filepath.Base(path),
		Path:        b.handler.projectSearchPath(b.pathCtx, path, b.requestedRoot, b.root),
		Depth:       depth,
		Directories: []WorkspaceDirectoryNode{},
	}
	if depth == 0 {
		node.Path = b.handler.projectOutputPath(b.pathCtx, path)
	}
	if b.shouldChargeDirectoryScan(node) {
		b.directoriesScanned++
		if b.scanLimit > 0 && b.directoriesScanned > b.scanLimit {
			node.Truncated = true
			b.truncated = true
			b.truncationReason = fmt.Sprintf("directory scan limit %d reached before page was complete", b.scanLimit)
			return node
		}
	}
	summarizeNode := b.shouldSummarizeNode(node)
	if summarizeNode {
		b.directoryCount++
		if depth > 0 {
			b.noteDirectory(path, filepath.Base(path))
		}
	}
	if err := contextError(b.ctx); err != nil {
		node.Truncated = true
		node.ReadError = b.pathCtx.sanitizeErrorText(err.Error())
		b.truncated = true
		b.truncationReason = b.pathCtx.sanitizeErrorText(err.Error())
		return node
	}

	dir, err := os.Open(path)
	if err != nil {
		node.ReadError = b.pathCtx.sanitizeErrorText(err.Error())
		return node
	}
	defer dir.Close()

	childDirs := make([]string, 0, 16)
	for {
		if err := contextError(b.ctx); err != nil {
			b.markContextError(&node, err)
			return node
		}
		entries, readErr := dir.ReadDir(128)
		for _, entry := range entries {
			if err := contextError(b.ctx); err != nil {
				b.markContextError(&node, err)
				return node
			}
			name := entry.Name()
			childPath := filepath.Join(path, name)
			if entry.IsDir() {
				skip, hidden, vcs := shouldSkipInventoryDir(b.root, childPath, name, b.ignoreMatcher, b.includeHidden, b.includeVCS)
				ignoredByGlob := b.ignoreMatcher.shouldSkipPath(b.root, childPath, true)
				if hidden {
					b.dotEntriesSkipped = true
				}
				if vcs && skip {
					b.vcsEntriesSkipped++
				}
				if skip {
					if hidden {
						if summarizeNode {
							b.hiddenEntriesSkipped++
						}
					}
					if summarizeNode && ignoredByGlob && !hidden && !vcs {
						b.ignoredEntriesSkipped++
					}
					b.ignoredDirectoryCount++
					continue
				}
				if hidden {
					b.hiddenEntriesIncluded++
				}
				if vcs {
					b.vcsEntriesIncluded++
				}
				node.DirectDirCount++
				if depth >= b.maxDepth {
					b.maxDepthReached = true
					continue
				}
				childDirs = append(childDirs, childPath)
				continue
			}
			skip, hidden, vcs := shouldSkipInventoryFile(b.root, childPath, name, b.ignoreMatcher, b.includeHidden, b.includeVCS)
			ignoredByGlob := b.ignoreMatcher.shouldSkipPath(b.root, childPath, false)
			if hidden {
				b.dotEntriesSkipped = true
			}
			if summarizeNode && hidden && skip && isBackupCandidateName(name) {
				b.noteBackupCandidateDirectory(path)
			}
			if vcs && skip {
				b.vcsEntriesSkipped++
			}
			if skip {
				if hidden {
					if summarizeNode {
						b.hiddenEntriesSkipped++
					}
				}
				if summarizeNode && ignoredByGlob && !hidden && !vcs {
					b.ignoredEntriesSkipped++
				}
				continue
			}
			if hidden {
				b.hiddenEntriesIncluded++
			}
			if vcs {
				b.vcsEntriesIncluded++
			}
			node.DirectFileCount++
			if summarizeNode {
				b.noteFile(path, childPath, name)
			}
		}
		if readErr == nil {
			continue
		}
		if readErr != io.EOF {
			node.ReadError = b.pathCtx.sanitizeErrorText(readErr.Error())
		}
		break
	}

	if depth >= b.maxDepth {
		b.addPageEntry(node, parentPath)
		return node
	}

	b.addPageEntry(node, parentPath)
	if b.pageFull {
		node.Truncated = true
		return node
	}
	sort.Strings(childDirs)
	for _, childPath := range childDirs {
		if b.pageFull {
			node.Truncated = true
			return node
		}
		node.Directories = append(node.Directories, b.build(childPath, depth+1, node.Path))
	}
	return node
}

func (b *workspaceInventoryBuilder) shouldChargeDirectoryScan(node WorkspaceDirectoryNode) bool {
	if b.continuationAfter == nil {
		return true
	}
	entry := WorkspaceDirectoryPageEntry{Path: node.Path}
	key := workspaceInventorySortKey(entry)
	return globSortCompare(key, b.continuationAfter.LastSortKey, "path_asc") > 0
}

func (b *workspaceInventoryBuilder) shouldSummarizeNode(node WorkspaceDirectoryNode) bool {
	if b.pageFull {
		return false
	}
	if b.pageLimit > 0 && len(b.directoriesPage) >= b.pageLimit {
		return false
	}
	if b.continuationAfter == nil {
		return true
	}
	entry := WorkspaceDirectoryPageEntry{Path: node.Path}
	key := workspaceInventorySortKey(entry)
	return globSortCompare(key, b.continuationAfter.LastSortKey, "path_asc") > 0
}

func (b *workspaceInventoryBuilder) markTruncated(node *WorkspaceDirectoryNode) {
	node.Truncated = true
	b.truncated = true
	if b.truncationReason == "" {
		b.truncationReason = fmt.Sprintf("directory page limit %d reached", b.pageLimit)
	}
}

func (b *workspaceInventoryBuilder) addPageEntry(node WorkspaceDirectoryNode, parentPath string) {
	if b.pageFull {
		return
	}
	entry := WorkspaceDirectoryPageEntry{
		Path:            node.Path,
		ParentPath:      parentPath,
		Depth:           node.Depth,
		DirectFileCount: node.DirectFileCount,
		DirectDirCount:  node.DirectDirCount,
		ReadError:       node.ReadError,
	}
	key := workspaceInventorySortKey(entry)
	if b.continuationAfter != nil && globSortCompare(key, b.continuationAfter.LastSortKey, "path_asc") <= 0 {
		return
	}
	if b.pageLimit > 0 && len(b.directoriesPage) >= b.pageLimit {
		b.pageFull = true
		b.truncated = true
		if b.truncationReason == "" {
			b.truncationReason = fmt.Sprintf("directory page limit %d reached", b.pageLimit)
		}
		return
	}
	b.directoriesPage = append(b.directoriesPage, entry)
}

func (b *workspaceInventoryBuilder) markContextError(node *WorkspaceDirectoryNode, err error) {
	node.Truncated = true
	node.ReadError = b.pathCtx.sanitizeErrorText(err.Error())
	b.truncated = true
	if b.truncationReason == "" {
		b.truncationReason = b.pathCtx.sanitizeErrorText(err.Error())
	}
}

func shouldSkipInventoryDir(root, path, name string, ignoreMatcher compiledIgnoreMatcher, includeHidden, includeVCS bool) (bool, bool, bool) {
	hidden := strings.HasPrefix(name, ".")
	vcs := isVCSDirectoryName(name) || pathInsideVCSMetadata(root, path)
	if vcs && (!includeVCS || isHighVolumeVCSInternalName(name)) {
		return true, hidden, true
	}
	if hidden && !includeHidden && !(vcs && includeVCS) {
		return true, true, vcs
	}
	return ignoreMatcher.shouldSkipPath(root, path, true), hidden, vcs
}

func shouldSkipInventoryFile(root, path, name string, ignoreMatcher compiledIgnoreMatcher, includeHidden, includeVCS bool) (bool, bool, bool) {
	hidden := strings.HasPrefix(name, ".")
	vcs := pathInsideVCSMetadata(root, path)
	if vcs && !includeVCS {
		return true, hidden, true
	}
	if hidden && !includeHidden && !(vcs && includeVCS) {
		return true, true, vcs
	}
	return ignoreMatcher.shouldSkipPath(root, path, false), hidden, vcs
}

func (b *workspaceInventoryBuilder) noteDirectory(path, name string) {
	display := b.handler.projectSearchPath(b.pathCtx, path, b.requestedRoot, b.root)
	lower := strings.ToLower(name)
	switch lower {
	case "src", "source", "app", "apps", "cmd", "internal", "pkg", "lib":
		b.sourceDirHints[display] = true
	case "test", "tests", "__tests__", "spec", "specs":
		b.testDirHints[display] = true
	}
	if strings.Contains(lower, "test") {
		b.testDirHints[display] = true
	}
}

func (b *workspaceInventoryBuilder) noteFile(parent, path, name string) {
	ext := strings.ToLower(filepath.Ext(name))
	if ext == "" {
		ext = "[no_ext]"
	}
	b.fileTypeCounts[ext]++
	lower := strings.ToLower(name)
	switch lower {
	case "go.mod", "package.json", "pnpm-lock.yaml", "yarn.lock", "package-lock.json", "pyproject.toml", "requirements.txt", "cargo.toml", "pom.xml", "build.gradle", "deno.json", "bun.lockb":
		b.packageHints[b.handler.projectSearchPath(b.pathCtx, path, b.requestedRoot, b.root)] = true
	}
	if strings.HasSuffix(lower, ".bak") || strings.Contains(lower, ".bak.") {
		b.noteBackupCandidateDirectory(parent)
	}
}

func isBackupCandidateName(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".bak") || strings.Contains(lower, ".bak.")
}

func (b *workspaceInventoryBuilder) noteBackupCandidateDirectory(parent string) {
	dir := b.handler.projectSearchPath(b.pathCtx, parent, b.requestedRoot, b.root)
	b.backupDirs[dir]++
}

func (b *workspaceInventoryBuilder) summary(pathCtx PathContext, profile string, directories []WorkspaceDirectoryPageEntry) *WorkspaceSummary {
	largest := append([]WorkspaceDirectoryPageEntry(nil), directories...)
	sort.SliceStable(largest, func(i, j int) bool {
		if largest[i].DirectFileCount == largest[j].DirectFileCount {
			return largest[i].Path < largest[j].Path
		}
		return largest[i].DirectFileCount > largest[j].DirectFileCount
	})
	if len(largest) > 10 {
		largest = largest[:10]
	}
	backupCandidates := make([]BackupCandidateDirectory, 0, len(b.backupDirs))
	backupHints := []ActionHint{}
	for dir, count := range b.backupDirs {
		backupCandidates = append(backupCandidates, BackupCandidateDirectory{Path: dir, CandidateFileCount: count})
		input := map[string]any{
			"target_directory": dir,
			"glob_pattern":     backupRediscoveryGlob,
			"include_hidden":   true,
		}
		addCwdIDToRecommendedInput(pathCtx, "glob_file_search", input)
		backupHints = append(backupHints, ActionHint{
			SafeToRetry:                true,
			RecommendedNextTool:        "glob_file_search",
			RecommendedNextInput:       input,
			RecommendedNextInputPolicy: "rediscover_sidecar_backups",
			Reason:                     "Rediscover sidecar backups in this directory.",
		})
	}
	sort.Slice(backupCandidates, func(i, j int) bool { return backupCandidates[i].Path < backupCandidates[j].Path })
	sort.Slice(backupHints, func(i, j int) bool {
		return fmt.Sprint(backupHints[i].RecommendedNextInput["target_directory"]) < fmt.Sprint(backupHints[j].RecommendedNextInput["target_directory"])
	})
	coverageComplete, incompleteReason, scanScope := b.summaryCoverage()
	return &WorkspaceSummary{
		Complete:                   coverageComplete,
		SummaryCoverageComplete:    coverageComplete,
		TreeScanComplete:           coverageComplete,
		SummaryIncompleteReason:    incompleteReason,
		ScanScope:                  scanScope,
		Profile:                    profile,
		FileTypeCounts:             b.fileTypeCounts,
		PackageHints:               sortedStringSet(b.packageHints),
		SourceDirHints:             sortedStringSet(b.sourceDirHints),
		TestDirHints:               sortedStringSet(b.testDirHints),
		LargestDirectories:         largest,
		BackupCandidateDirectories: backupCandidates,
		BackupDiscoveryHints:       backupHints,
		HiddenEntriesSkipped:       b.hiddenEntriesSkipped,
		IgnoredEntriesSkipped:      b.ignoredEntriesSkipped,
	}
}

func (b *workspaceInventoryBuilder) summaryCoverage() (complete bool, incompleteReason, scanScope string) {
	switch {
	case b.truncated && strings.Contains(b.truncationReason, "directory scan limit"):
		return false, "scan_limit_reached", "scan_limited"
	case b.truncated && strings.TrimSpace(b.truncationReason) != "":
		return false, b.truncationReason, "page_limited"
	case b.truncated:
		return false, "directory page limit reached", "page_limited"
	case b.maxDepthReached:
		return false, "max_depth_reached", "max_depth_limited"
	case b.continuationAfter != nil:
		return false, "continuation_page", "continuation_page"
	default:
		return true, "", "requested_depth"
	}
}

func normalizeWorkspaceSummaryProfile(profile string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "", "compact":
		return "compact", nil
	case "none":
		return "none", nil
	case "extended":
		return "extended", nil
	default:
		return "", fmt.Errorf("invalid_summary_profile: use compact, none, or extended")
	}
}

func flattenWorkspaceDirectories(root WorkspaceDirectoryNode, parent string, limit int) []WorkspaceDirectoryPageEntry {
	out := []WorkspaceDirectoryPageEntry{}
	var walk func(WorkspaceDirectoryNode, string)
	walk = func(node WorkspaceDirectoryNode, parentPath string) {
		if limit > 0 && len(out) >= limit {
			return
		}
		out = append(out, WorkspaceDirectoryPageEntry{
			Path:            node.Path,
			ParentPath:      parentPath,
			Depth:           node.Depth,
			DirectFileCount: node.DirectFileCount,
			DirectDirCount:  node.DirectDirCount,
			ReadError:       node.ReadError,
		})
		for _, child := range node.Directories {
			walk(child, node.Path)
		}
	}
	walk(root, parent)
	return out
}

func workspaceInventoryContinuation(pathCtx PathContext, input WorkspaceInventoryInput, queryHash string, limit int, page []WorkspaceDirectoryPageEntry, truncated bool, reason string) *ContinuationHint {
	pageComplete := !truncated
	if !truncated {
		return &ContinuationHint{
			Complete:           true,
			PageComplete:       &pageComplete,
			Consistency:        "unknown",
			CanonicalQueryHash: queryHash,
			Reason:             "The page is complete, but directory tree stability is not proven between calls.",
		}
	}
	if len(page) == 0 {
		return &ContinuationHint{Complete: false, PageComplete: &pageComplete, Consistency: "unknown", CanonicalQueryHash: queryHash, Reason: reason}
	}
	lastKey := workspaceInventorySortKey(page[len(page)-1])
	nextInput := map[string]any{
		"target_directory": input.TargetDirectory,
		"limit":            limit,
		"continuation_after": map[string]any{
			"canonical_query_hash": queryHash,
			"last_sort_key":        sortKeyInputMap(lastKey),
		},
	}
	if input.MaxDepth != nil {
		nextInput["max_depth"] = *input.MaxDepth
	}
	if len(input.IgnoreGlobs) > 0 {
		nextInput["ignore_globs"] = input.IgnoreGlobs
	}
	if input.IncludeHidden {
		nextInput["include_hidden"] = true
	}
	if input.IncludeVCSMetadata {
		nextInput["include_vcs_metadata"] = true
	}
	if input.IncludeSummary != nil {
		nextInput["include_summary"] = *input.IncludeSummary
	}
	if strings.TrimSpace(input.SummaryProfile) != "" {
		nextInput["summary_profile"] = input.SummaryProfile
	}
	addCwdIDToRecommendedInput(pathCtx, "workspace_inventory", nextInput)
	hint := ActionHint{
		SafeToRetry:                true,
		RecommendedNextTool:        "workspace_inventory",
		RecommendedNextInput:       nextInput,
		RecommendedNextInputPolicy: "continue_workspace_inventory_page",
		Reason:                     "Continue this stateless workspace_inventory page using the query hash and last sort key.",
	}
	return &ContinuationHint{
		Complete:             false,
		PageComplete:         &pageComplete,
		Consistency:          "unknown",
		CanonicalQueryHash:   queryHash,
		LastSortKey:          &lastKey,
		StaleIfFileChanges:   true,
		NextRecommendedCall:  &hint,
		NextRecommendedCalls: []ActionHint{hint},
		Reason:               reason,
	}
}

func workspaceInventoryNextRecommendedCalls(pathCtx PathContext, page []WorkspaceDirectoryPageEntry, truncated bool) []ActionHint {
	if truncated {
		return nil
	}
	best := WorkspaceDirectoryPageEntry{}
	for _, entry := range page {
		if entry.DirectFileCount <= 0 {
			continue
		}
		if best.Path == "" || entry.DirectFileCount > best.DirectFileCount || entry.DirectFileCount == best.DirectFileCount && entry.Path < best.Path {
			best = entry
		}
	}
	if best.Path == "" {
		return nil
	}
	input := map[string]any{
		"target_directory": best.Path,
		"glob_pattern":     "*",
		"limit":            20,
		"sort":             "directory_path_asc",
	}
	addCwdIDToRecommendedInput(pathCtx, "glob_file_search", input)
	return []ActionHint{{
		SafeToRetry:                true,
		RecommendedNextTool:        "glob_file_search",
		RecommendedNextInput:       input,
		RecommendedNextInputPolicy: "discover_files_in_directory",
		Reason:                     "Inventory is directory-level; use a bounded glob_file_search to inspect files in this directory.",
	}}
}

func workspaceInventoryScanLimit(pageLimit int) int {
	return maxInt(defaultWorkspaceInventoryLimit, pageLimit*20)
}

func workspaceInventorySortKey(entry WorkspaceDirectoryPageEntry) DiscoverySortKey {
	return DiscoverySortKey{Path: entry.Path}
}

func workspaceInventoryQueryHash(pathCtx PathContext, input WorkspaceInventoryInput, maxDepth, limit int) string {
	ignore := append([]string(nil), input.IgnoreGlobs...)
	sort.Strings(ignore)
	includeSummary := true
	if input.IncludeSummary != nil {
		includeSummary = *input.IncludeSummary
	}
	return canonicalHash(map[string]any{
		"tool":                 "workspace_inventory",
		"cwd":                  pathCtx.HasCwd,
		"cwd_id":               pathCtx.CwdID,
		"target_directory":     slashPath(input.TargetDirectory),
		"max_depth":            maxDepth,
		"limit":                limit,
		"ignore_globs":         ignore,
		"include_hidden":       input.IncludeHidden,
		"include_vcs_metadata": input.IncludeVCSMetadata,
		"include_summary":      includeSummary,
		"summary_profile":      input.SummaryProfile,
	})
}
