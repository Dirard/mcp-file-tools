//go:build ignore

// Manual smoke test for the public MCP server operations.
// Run with: go run test_server.go

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Dirard/mcp-file-tools/filetoolsserver"
	"github.com/Dirard/mcp-file-tools/filetoolsserver/handler"
)

var failed = 0

func check(name string, ok bool) {
	fmt.Printf("%-40s ", name)
	if ok {
		fmt.Println("OK")
	} else {
		fmt.Println("FAIL")
		failed++
	}
}

func main() {
	tempDir, _ := os.MkdirTemp("", "mcp-test-*")
	defer os.RemoveAll(tempDir)

	ctx := context.Background()
	h := handler.NewHandler()

	fmt.Printf("Server version: %s\n\n", filetoolsserver.Version)

	testFile := filepath.Join(tempDir, "test.txt")
	_ = os.WriteFile(testFile, []byte("first\nneedle\nlast"), 0644)
	_ = os.Mkdir(filepath.Join(tempDir, "subdir"), 0755)
	_ = os.WriteFile(filepath.Join(tempDir, "subdir", "nested.go"), []byte("package main\n// needle\n"), 0644)
	_ = os.Mkdir(filepath.Join(tempDir, "second"), 0755)
	_ = os.WriteFile(filepath.Join(tempDir, "second", "another.go"), []byte("package main\n// needle\n"), 0644)

	r1, o1, _ := h.HandleReadFile(ctx, nil, handler.ReadFileInput{TargetFile: testFile})
	check("read_file", !r1.IsError && strings.Contains(o1.Text, "2|needle"))

	rOutline, oOutline, _ := h.HandleOutlineFile(ctx, nil, handler.OutlineFileInput{TargetFile: testFile})
	check("outline_file", !rOutline.IsError && oOutline.Fingerprint != nil && oOutline.Fingerprint.LineCount == 3)

	longFile := filepath.Join(tempDir, "long.txt")
	_ = os.WriteFile(longFile, []byte(strings.Repeat("x", 12*1024)), 0644)
	r2, o2, _ := h.HandleReadFile(ctx, nil, handler.ReadFileInput{TargetFile: longFile})
	check("read_file long line full output", !r2.IsError && strings.Contains(o2.Text, "1|"+strings.Repeat("x", 12*1024)))

	r4, o4, _ := h.HandleListDir(ctx, nil, handler.ListDirInput{TargetDirectory: tempDir})
	check("list_dir", !r4.IsError && listDirHasEntry(o4, "test.txt", "file"))

	r5, o5, _ := h.HandleGlobFileSearch(ctx, nil, handler.GlobFileSearchInput{
		TargetDirectory: tempDir,
		GlobPattern:     "**/*.go",
		Limit:           intPtr(1),
	})
	check("glob_file_search", !r5.IsError && o5.Limit == 1 && o5.Count == 1 && o5.TotalMatchCount == 2 && o5.Truncated)

	r6, o6, _ := h.HandleGrepTool(ctx, nil, handler.GrepToolInput{
		Path:    tempDir,
		Pattern: "needle",
		Limit:   intPtr(1),
	})
	check("grep", !r6.IsError && o6.Limit == 1 && o6.RowCount == 1 && o6.Truncated && len(o6.Matches) == 1)

	r8, o8, _ := h.HandleInspectPath(ctx, nil, handler.InspectPathInput{TargetPath: testFile})
	check("inspect_path", !r8.IsError && o8.Exists && o8.Kind == "file" && o8.SizeBytes != nil && *o8.SizeBytes > 0 && o8.LineCount != nil && *o8.LineCount == 3)

	r9, o9, _ := h.HandleWorkspaceInventory(ctx, nil, handler.WorkspaceInventoryInput{
		TargetDirectory: tempDir,
		MaxDepth:        intPtr(2),
		Limit:           intPtr(10),
	})
	check("workspace_inventory", !r9.IsError && o9.Root != nil && o9.Root.DirectFileCount >= 2 && o9.Root.DirectDirCount == 2 && len(o9.Root.Directories) == 2)

	refactorSource := filepath.Join(tempDir, "refactor.md")
	copyTarget := filepath.Join(tempDir, "copied.md")
	moveTarget := filepath.Join(tempDir, "moved.md")
	_ = os.WriteFile(refactorSource, []byte("# Intro\nkeep\n# Extract\nmove me\n# Tail\nkeep tail\n"), 0644)
	refactorFingerprint := fingerprintForSmoke(ctx, h, refactorSource)

	rCopy, oCopy, _ := h.HandleCopyRanges(ctx, nil, handler.CopyRangesInput{
		SourceFile:        refactorSource,
		SourceFingerprint: refactorFingerprint,
		Ranges:            []handler.SourceLineRange{{StartLine: 3, EndLine: 4}},
		TargetFile:        copyTarget,
		TargetPrecondition: handler.TargetPrecondition{
			MustNotExist: true,
		},
		Placement: handler.TargetPlacement{Mode: "create_new"},
		DryRun:    true,
	})
	check("copy_ranges dry_run", !rCopy.IsError && !oCopy.Applied && oCopy.WouldWriteBytes > 0)

	rMove, oMove, _ := h.HandleMoveRanges(ctx, nil, handler.MoveRangesInput{
		SourceFile:        refactorSource,
		SourceFingerprint: refactorFingerprint,
		Ranges:            []handler.SourceLineRange{{StartLine: 3, EndLine: 4}},
		TargetFile:        moveTarget,
		TargetPrecondition: handler.TargetPrecondition{
			MustNotExist: true,
		},
		Placement: handler.TargetPlacement{Mode: "create_new"},
	})
	check("move_ranges", !rMove.IsError && oMove.Applied && oMove.RemovedSourceLines == 2 && fileContains(moveTarget, "# Extract\nmove me\n") && fileContains(refactorSource, "# Intro\nkeep\n# Tail\nkeep tail\n"))

	batchSource := filepath.Join(tempDir, "batch.md")
	batchTargetA := filepath.Join(tempDir, "batch-a.md")
	batchTargetB := filepath.Join(tempDir, "batch-b.md")
	_ = os.WriteFile(batchSource, []byte("# A\nalpha\n# B\nbeta\n# C\ngamma\n"), 0644)
	batchFingerprint := fingerprintForSmoke(ctx, h, batchSource)
	rCopyBatch, oCopyBatch, _ := h.HandleCopyRangesBatch(ctx, nil, handler.CopyRangesBatchInput{
		SourceFile:        batchSource,
		SourceFingerprint: batchFingerprint,
		Targets: []handler.BatchRangeTarget{
			{
				TargetFile:         batchTargetA,
				TargetPrecondition: handler.TargetPrecondition{MustNotExist: true},
				Placement:          handler.TargetPlacement{Mode: "create_new"},
				Ranges:             []handler.SourceLineRange{{StartLine: 1, EndLine: 2}},
			},
			{
				TargetFile:         batchTargetB,
				TargetPrecondition: handler.TargetPrecondition{MustNotExist: true},
				Placement:          handler.TargetPlacement{Mode: "create_new"},
				Ranges:             []handler.SourceLineRange{{StartLine: 3, EndLine: 4}},
			},
		},
	})
	check("copy_ranges_batch", !rCopyBatch.IsError && oCopyBatch.Applied && len(oCopyBatch.TargetsWritten) == 2 && fileContains(batchTargetA, "# A\nalpha\n") && fileContains(batchTargetB, "# B\nbeta\n"))

	moveBatchSource := filepath.Join(tempDir, "move-batch.md")
	moveBatchTargetA := filepath.Join(tempDir, "move-batch-a.md")
	moveBatchTargetB := filepath.Join(tempDir, "move-batch-b.md")
	_ = os.WriteFile(moveBatchSource, []byte("# A\nalpha\n# B\nbeta\n# C\ngamma\n"), 0644)
	moveBatchFingerprint := fingerprintForSmoke(ctx, h, moveBatchSource)
	rMoveBatch, oMoveBatch, _ := h.HandleMoveRangesBatch(ctx, nil, handler.MoveRangesBatchInput{
		SourceFile:        moveBatchSource,
		SourceFingerprint: moveBatchFingerprint,
		Targets: []handler.BatchRangeTarget{
			{
				TargetFile:         moveBatchTargetA,
				TargetPrecondition: handler.TargetPrecondition{MustNotExist: true},
				Placement:          handler.TargetPlacement{Mode: "create_new"},
				Ranges:             []handler.SourceLineRange{{StartLine: 1, EndLine: 2}},
			},
			{
				TargetFile:         moveBatchTargetB,
				TargetPrecondition: handler.TargetPrecondition{MustNotExist: true},
				Placement:          handler.TargetPlacement{Mode: "create_new"},
				Ranges:             []handler.SourceLineRange{{StartLine: 3, EndLine: 4}},
			},
		},
	})
	check("move_ranges_batch", !rMoveBatch.IsError && oMoveBatch.Applied && oMoveBatch.RemovedSourceLines == 4 && fileContains(moveBatchSource, "# C\ngamma\n"))

	r7, o7, _ := h.HandleListDir(ctx, nil, handler.ListDirInput{})
	check("list_dir requires target_directory", r7.IsError && o7.Text == "" && strings.Contains(o7.Error, "target_directory is required"))

	fmt.Println()
	if failed > 0 {
		fmt.Printf("FAILED: %d test(s)\n", failed)
		os.Exit(1)
	}
	fmt.Println("All public smoke tests passed!")
}

func listDirHasEntry(output handler.ListDirOutput, name, kind string) bool {
	for _, entry := range output.Entries {
		if entry.Name == name && entry.Kind == kind {
			return true
		}
	}
	return false
}

func globHasFile(output handler.GlobFileSearchOutput, suffix string) bool {
	for _, file := range output.Files {
		if strings.HasSuffix(file.Path, suffix) {
			return true
		}
	}
	return false
}

func grepHasMatch(output handler.GrepOutput, suffix string, line int, text string) bool {
	for _, match := range output.Matches {
		if strings.HasSuffix(match.Path, suffix) && match.Line == line && match.Text == text {
			return true
		}
	}
	return false
}

func fingerprintForSmoke(ctx context.Context, h *handler.Handler, path string) handler.FileFingerprint {
	_, output, _ := h.HandleOutlineFile(ctx, nil, handler.OutlineFileInput{
		TargetFile:    path,
		OutputProfile: "fingerprint_only",
	})
	if output.Fingerprint == nil {
		return handler.FileFingerprint{}
	}
	return *output.Fingerprint
}

func fileContains(path, want string) bool {
	data, err := os.ReadFile(path)
	return err == nil && string(data) == want
}

func intPtr(value int) *int {
	return &value
}
