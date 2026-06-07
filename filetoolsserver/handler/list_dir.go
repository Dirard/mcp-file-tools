package handler

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// HandleListDir lists direct children of a directory.
func (h *Handler) HandleListDir(ctx context.Context, req *mcp.CallToolRequest, input ListDirInput) (*mcp.CallToolResult, ListDirOutput, error) {
	pathCtx, cwdErr := h.BuildPathContext(input.CwdID)
	if cwdErr != nil {
		return toolCwdError[ListDirOutput](cwdErr)
	}
	resolvedDirectory, displayDirectory, err := h.resolveToolPath(pathCtx, input.TargetDirectory, "target_directory")
	if err != nil {
		return toolError[ListDirOutput](fmt.Sprintf("Cannot list target_directory: %v", err))
	}
	releaseTool, err := h.acquireToolCall(ctx)
	if err != nil {
		return toolError[ListDirOutput](limiterWaitError("tool call", err))
	}
	defer releaseTool()

	stat, err := os.Stat(resolvedDirectory)
	if err != nil {
		return toolError[ListDirOutput](fmt.Sprintf("Cannot access directory %q: %v\n\nCheck the path or try its parent directory.", displayDirectory, err))
	}
	if !stat.IsDir() {
		return toolError[ListDirOutput](fmt.Sprintf("%q is a file, not a directory.\n\nUse read_file for files.", displayDirectory))
	}

	entries, err := os.ReadDir(resolvedDirectory)
	if err != nil {
		return toolError[ListDirOutput](fmt.Sprintf("Cannot read directory %q: %v", displayDirectory, err))
	}
	ignoreMatcher := newCompiledIgnoreMatcher(input.IgnoreGlobs)
	outputEntries := make([]ListDirEntry, 0, len(entries))
	dotEntriesSkipped := false
	hiddenIncluded := 0
	vcsIncluded := 0
	vcsSkipped := 0
	for _, entry := range entries {
		name := entry.Name()
		skip, hidden, vcs := shouldSkipListDirItem(name, ignoreMatcher, input.IncludeHidden, input.IncludeVCSMetadata)
		if hidden {
			dotEntriesSkipped = true
		}
		if vcs && skip {
			vcsSkipped++
		}
		if skip {
			continue
		}
		if strings.HasPrefix(name, ".") {
			hiddenIncluded++
		}
		if vcs {
			vcsIncluded++
		}
		kind := "file"
		if entry.IsDir() {
			kind = "directory"
		}
		outputEntries = append(outputEntries, ListDirEntry{Name: name, Kind: kind})
	}
	if len(outputEntries) == 0 {
		message := "Directory is empty or all entries were filtered. list_dir shows only direct children; use glob_file_search for recursive search."
		return structuredResultOnly(), ListDirOutput{
			Text:                  fmt.Sprintf("Directory %q is empty or all entries were filtered.\n\nlist_dir shows only direct children; use glob_file_search for recursive search.\n", displayDirectory),
			Directory:             displayDirectory,
			IncludeHidden:         input.IncludeHidden,
			IncludeVCSMetadata:    input.IncludeVCSMetadata,
			DotEntriesSkipped:     dotEntriesSkipped,
			HiddenEntriesIncluded: hiddenIncluded,
			VCSEntriesSkipped:     vcsSkipped,
			VCSEntriesIncluded:    vcsIncluded,
			Entries:               []ListDirEntry{},
			Message:               message,
		}, nil
	}
	return structuredResultOnly(), listDirOutput(displayDirectory, outputEntries, input.IncludeHidden, input.IncludeVCSMetadata, dotEntriesSkipped, hiddenIncluded, vcsSkipped, vcsIncluded), nil
}

func listDirOutput(directory string, entries []ListDirEntry, includeHidden, includeVCSMetadata, dotEntriesSkipped bool, hiddenIncluded, vcsSkipped, vcsIncluded int) ListDirOutput {
	var b strings.Builder
	b.WriteString(listDirHeaderLine(directory))
	for _, entry := range entries {
		if entry.Kind == "directory" {
			b.WriteString(fmt.Sprintf("  - %s/...\n", entry.Name))
		} else {
			b.WriteString(fmt.Sprintf("  - %s\n", entry.Name))
		}
	}
	return ListDirOutput{
		Text:                  b.String(),
		Directory:             directory,
		Count:                 len(entries),
		IncludeHidden:         includeHidden,
		IncludeVCSMetadata:    includeVCSMetadata,
		DotEntriesSkipped:     dotEntriesSkipped,
		HiddenEntriesIncluded: hiddenIncluded,
		VCSEntriesSkipped:     vcsSkipped,
		VCSEntriesIncluded:    vcsIncluded,
		Entries:               entries,
	}
}

func listDirHeaderLine(targetDirectory string) string {
	if targetDirectory == "/" {
		return "/\n"
	}
	header := strings.TrimRight(targetDirectory, `/\`)
	header = slashPath(header)
	if header == "" || header == "." {
		header = "."
	}
	return header + "/\n"
}

func shouldSkipListDirItem(name string, ignoreMatcher compiledIgnoreMatcher, includeHidden, includeVCSMetadata bool) (bool, bool, bool) {
	vcs := isVCSDirectoryName(name)
	if vcs && !includeVCSMetadata {
		return true, true, true
	}
	if strings.HasPrefix(name, ".") {
		if !includeHidden && !(vcs && includeVCSMetadata) {
			return true, true, vcs
		}
	}
	return ignoreMatcher.matchesListDirName(name), false, vcs
}
