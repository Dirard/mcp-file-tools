package filetoolsserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimitar-grigorov/mcp-file-tools/filetoolsserver/handler"
	"github.com/dimitar-grigorov/mcp-file-tools/internal/config"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestServerInstructionsDescribeAgentFriendlyGrep(t *testing.T) {
	for _, phrase := range []string{"search_stats", "file_groups", "read_ranges", "next_recommended_call", "literal"} {
		if !strings.Contains(serverInstructions, phrase) {
			t.Fatalf("server instructions should mention grep %q capability", phrase)
		}
	}
}

func TestServerInstructionsDescribePhase6SymbolTools(t *testing.T) {
	for _, phrase := range []string{"fourteen tools", "resolve_symbol_range", "JavaScript/JSX", "TypeScript/TSX", "enclosing_items", "recommended_write_call", "dry-run-only"} {
		if !strings.Contains(serverInstructions, phrase) {
			t.Fatalf("server instructions should mention Phase 6 capability %q", phrase)
		}
	}
}

func TestServerManifestExposesPublicTools(t *testing.T) {
	data, err := os.ReadFile("../server.json")
	if err != nil {
		t.Fatal(err)
	}

	var manifest struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}

	got := make([]string, 0, len(manifest.Tools))
	for _, tool := range manifest.Tools {
		got = append(got, tool.Name)
	}
	want := []string{
		"set_cwd",
		"read_file",
		"read_files",
		"outline_file",
		"resolve_symbol_range",
		"copy_ranges",
		"move_ranges",
		"copy_ranges_batch",
		"move_ranges_batch",
		"list_dir",
		"glob_file_search",
		"grep",
		"inspect_path",
		"workspace_inventory",
	}
	if len(got) != len(want) {
		t.Fatalf("manifest tool count = %d (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("manifest tools = %v, want %v", got, want)
		}
	}
}

func TestMCPToolCallReturnsToolSpecificStructuredOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	server := NewServer(nil, nil)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- server.Run(ctx, serverTransport)
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = session.Close()
		cancel()
		<-serverDone
	}()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if !strings.Contains(serverInstructions, tool.Name) {
			t.Fatalf("server instructions should list tool %q for discovery", tool.Name)
		}
		if len(tool.Description) > 520 {
			t.Fatalf("tool description for %s is too long for discovery (%d bytes): %q", tool.Name, len(tool.Description), tool.Description)
		}
	}
	setCwdTool := findTool(t, tools.Tools, "set_cwd")
	setCwdOutputSchema := decodeToolInputSchema(t, setCwdTool.OutputSchema)
	requireSetCwdOutputSchema(t, setCwdOutputSchema)

	readFileTool := findTool(t, tools.Tools, "read_file")
	readFileSchema := decodeToolInputSchema(t, readFileTool.InputSchema)
	requireSchemaField(t, readFileSchema, "target_file")
	requireAbsolutePathSchema(t, readFileSchema, "target_file")
	requireSchemaProperty(t, readFileSchema, "cwd_id")
	readFileOutputSchema := decodeToolInputSchema(t, readFileTool.OutputSchema)
	requireSchemaProperty(t, readFileOutputSchema, "file")
	requireAbsolutePathSchema(t, readFileOutputSchema, "file")
	requireSchemaProperty(t, readFileOutputSchema, "text")
	forbidSchemaProperty(t, readFileOutputSchema, "lines")

	outlineTool := findTool(t, tools.Tools, "outline_file")
	outlineSchema := decodeToolInputSchema(t, outlineTool.InputSchema)
	requireSchemaField(t, outlineSchema, "target_file")
	requireAbsolutePathSchema(t, outlineSchema, "target_file")
	outlineOutputSchema := decodeToolInputSchema(t, outlineTool.OutputSchema)
	requireAbsolutePathSchema(t, outlineOutputSchema, "file")
	requireSchemaProperty(t, outlineOutputSchema, "cwd_id")
	requireSchemaProperty(t, outlineOutputSchema, "cwd")
	requireSchemaProperty(t, outlineOutputSchema, "error_code")
	requireSchemaProperty(t, outlineOutputSchema, "action_hint")
	requireSchemaProperty(t, outlineOutputSchema, "sections")
	requireSchemaProperty(t, outlineOutputSchema, "fingerprint")

	copyTool := findTool(t, tools.Tools, "copy_ranges")
	copySchema := decodeToolInputSchema(t, copyTool.InputSchema)
	requireSchemaField(t, copySchema, "source_file")
	requireSchemaField(t, copySchema, "target_file")
	requireAbsolutePathSchema(t, copySchema, "source_file")
	requireAbsolutePathSchema(t, copySchema, "target_file")
	copyOutputSchema := decodeToolInputSchema(t, copyTool.OutputSchema)
	requireAbsolutePathSchema(t, copyOutputSchema, "source_file")
	requireAbsolutePathSchema(t, copyOutputSchema, "target_file")
	requireAbsolutePathArrayItemSchema(t, copyOutputSchema, "backup_paths")
	requireSchemaProperty(t, copyOutputSchema, "action_hint")

	moveTool := findTool(t, tools.Tools, "move_ranges")
	moveSchema := decodeToolInputSchema(t, moveTool.InputSchema)
	requireSchemaField(t, moveSchema, "source_file")
	requireSchemaField(t, moveSchema, "target_file")
	requireAbsolutePathSchema(t, moveSchema, "source_file")
	requireAbsolutePathSchema(t, moveSchema, "target_file")

	copyBatchTool := findTool(t, tools.Tools, "copy_ranges_batch")
	copyBatchSchema := decodeToolInputSchema(t, copyBatchTool.InputSchema)
	requireSchemaField(t, copyBatchSchema, "source_file")
	requireAbsolutePathSchema(t, copyBatchSchema, "source_file")
	requireNestedArrayObjectAbsolutePathSchema(t, copyBatchSchema, "targets", "target_file")
	copyBatchOutputSchema := decodeToolInputSchema(t, copyBatchTool.OutputSchema)
	requireAbsolutePathSchema(t, copyBatchOutputSchema, "source_file")
	requireAbsolutePathArrayItemSchema(t, copyBatchOutputSchema, "targets_written")
	requireNestedArrayObjectAbsolutePathSchema(t, copyBatchOutputSchema, "target_results", "target_file")
	requireSchemaProperty(t, copyBatchOutputSchema, "target_results")

	moveBatchTool := findTool(t, tools.Tools, "move_ranges_batch")
	moveBatchSchema := decodeToolInputSchema(t, moveBatchTool.InputSchema)
	requireSchemaField(t, moveBatchSchema, "source_file")
	requireAbsolutePathSchema(t, moveBatchSchema, "source_file")
	requireNestedArrayObjectAbsolutePathSchema(t, moveBatchSchema, "targets", "target_file")
	moveBatchOutputSchema := decodeToolInputSchema(t, moveBatchTool.OutputSchema)
	requireAbsolutePathSchema(t, moveBatchOutputSchema, "source_file")
	requireAbsolutePathArrayItemSchema(t, moveBatchOutputSchema, "targets_written")
	requireNestedArrayObjectAbsolutePathSchema(t, moveBatchOutputSchema, "target_results", "target_file")

	grepTool := findTool(t, tools.Tools, "grep")
	grepSchema := decodeToolInputSchema(t, grepTool.InputSchema)
	requireSchemaProperty(t, grepSchema, "pattern_mode")
	requireSchemaProperty(t, grepSchema, "before")
	requireSchemaProperty(t, grepSchema, "after")
	requireSchemaProperty(t, grepSchema, "context")
	requireSchemaProperty(t, grepSchema, "case_insensitive")
	requireSchemaProperty(t, grepSchema, "line_window")
	requireSchemaProperty(t, grepSchema, "max_matches_per_file")
	requireSchemaProperty(t, grepSchema, "limit")
	forbidSchemaProperty(t, grepSchema, "-B")
	forbidSchemaProperty(t, grepSchema, "-A")
	forbidSchemaProperty(t, grepSchema, "-C")
	forbidSchemaProperty(t, grepSchema, "-i")
	grepOutputSchema := decodeToolInputSchema(t, grepTool.OutputSchema)
	requireAbsolutePathArrayItemSchema(t, grepOutputSchema, "files")
	requireSchemaProperty(t, grepOutputSchema, "search_stats")
	requireSchemaProperty(t, grepOutputSchema, "file_groups")
	requireSchemaProperty(t, grepOutputSchema, "next_recommended_call")
	inspectTool := findTool(t, tools.Tools, "inspect_path")
	inspectSchema := decodeToolInputSchema(t, inspectTool.InputSchema)
	requireSchemaField(t, inspectSchema, "target_path")
	requireAbsolutePathSchema(t, inspectSchema, "target_path")
	inspectOutputSchema := decodeToolInputSchema(t, inspectTool.OutputSchema)
	requireAbsolutePathSchema(t, inspectOutputSchema, "symlink_target")
	workspaceTool := findTool(t, tools.Tools, "workspace_inventory")
	workspaceSchema := decodeToolInputSchema(t, workspaceTool.InputSchema)
	requireSchemaField(t, workspaceSchema, "target_directory")
	requireAbsolutePathSchema(t, workspaceSchema, "target_directory")
	workspaceOutputSchema := decodeToolInputSchema(t, workspaceTool.OutputSchema)
	requireSchemaProperty(t, workspaceOutputSchema, "cwd_id")
	requireSchemaProperty(t, workspaceOutputSchema, "cwd")
	requireSchemaProperty(t, workspaceOutputSchema, "error_code")
	requireSchemaProperty(t, workspaceOutputSchema, "action_hint")

	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(file, []byte("first\nsecond\n"), 0644); err != nil {
		t.Fatal(err)
	}

	success, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "read_file",
		Arguments: map[string]any{"target_file": file},
	})
	if err != nil {
		t.Fatal(err)
	}
	if success.IsError {
		t.Fatalf("read_file returned error: %#v", success)
	}
	if len(success.Content) != 0 {
		t.Fatalf("read_file should not duplicate structured output into content, got %d content items", len(success.Content))
	}
	successOutput := decodeStructuredOutput[handler.ReadFileOutput](t, success.StructuredContent)
	if successOutput.Error != "" || successOutput.File != filepath.ToSlash(file) || !strings.Contains(successOutput.Text, "2|second") {
		t.Fatalf("unexpected read_file structured output: %#v", successOutput)
	}

	toolError, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_dir",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !toolError.IsError {
		t.Fatalf("list_dir missing target_directory should be a tool error: %#v", toolError)
	}
	if len(toolError.Content) != 0 {
		t.Fatalf("list_dir tool error should not duplicate structured output into content, got %d content items", len(toolError.Content))
	}
	errorOutput := decodeStructuredOutput[handler.ListDirOutput](t, toolError.StructuredContent)
	if errorOutput.Text != "" || !strings.Contains(errorOutput.Error, "target_directory is required") {
		t.Fatalf("unexpected list_dir structured error output: %#v", errorOutput)
	}
}

func TestMCPSetCwdEnablesRelativePathTools(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "internal", "test.go")
	if err := os.WriteFile(file, []byte("package main\n\nfunc Needle() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Load()
	cfg.CwdStatePath = filepath.Join(t.TempDir(), "cwd-state.sqlite")
	cfg.CwdStateConfigError = ""

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	server := NewServer(nil, cfg)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- server.Run(ctx, serverTransport)
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = session.Close()
		cancel()
		<-serverDone
	}()

	setResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "set_cwd",
		Arguments: map[string]any{"directory": root},
	})
	if err != nil {
		t.Fatal(err)
	}
	if setResult.IsError {
		t.Fatalf("set_cwd returned error: %#v", setResult)
	}
	setRaw, err := json.Marshal(setResult.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var setMap map[string]any
	if err := json.Unmarshal(setRaw, &setMap); err != nil {
		t.Fatal(err)
	}
	if len(setMap) != 1 || setMap["cwd_id"] == nil {
		t.Fatalf("set_cwd success output should contain only cwd_id: %s", setRaw)
	}
	setOutput := decodeStructuredOutput[handler.SetCwdOutput](t, setResult.StructuredContent)
	if setOutput.CwdID != 1 {
		t.Fatalf("first cwd_id should be small and deterministic, got %#v", setOutput)
	}

	readResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "read_file",
		Arguments: map[string]any{
			"cwd_id":      setOutput.CwdID,
			"target_file": "internal/test.go",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	readOutput := decodeStructuredOutput[handler.ReadFileOutput](t, readResult.StructuredContent)
	requireCwdMeta(t, readOutput.CwdID, readOutput.Cwd, setOutput.CwdID, root)
	if readOutput.File != "internal/test.go" || !strings.Contains(readOutput.Text, "1|package main") {
		t.Fatalf("read_file should use cwd-relative input/output: %#v", readOutput)
	}

	listResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "list_dir",
		Arguments: map[string]any{
			"cwd_id":           setOutput.CwdID,
			"target_directory": ".",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	listOutput := decodeStructuredOutput[handler.ListDirOutput](t, listResult.StructuredContent)
	requireCwdMeta(t, listOutput.CwdID, listOutput.Cwd, setOutput.CwdID, root)
	if listOutput.Directory != "." || len(listOutput.Entries) != 1 || listOutput.Entries[0].Name != "internal" {
		t.Fatalf("list_dir should return cwd-relative root: %#v", listOutput)
	}

	globResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "glob_file_search",
		Arguments: map[string]any{
			"cwd_id":           setOutput.CwdID,
			"target_directory": ".",
			"glob_pattern":     "*.go",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	globOutput := decodeStructuredOutput[handler.GlobFileSearchOutput](t, globResult.StructuredContent)
	requireCwdMeta(t, globOutput.CwdID, globOutput.Cwd, setOutput.CwdID, root)
	if len(globOutput.Files) != 1 || globOutput.Files[0].Path != "internal/test.go" {
		t.Fatalf("glob_file_search should return cwd-relative files: %#v", globOutput)
	}

	grepResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "grep",
		Arguments: map[string]any{
			"cwd_id":  setOutput.CwdID,
			"path":    ".",
			"pattern": "Needle",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	grepOutput := decodeStructuredOutput[handler.GrepOutput](t, grepResult.StructuredContent)
	requireCwdMeta(t, grepOutput.CwdID, grepOutput.Cwd, setOutput.CwdID, root)
	if len(grepOutput.Matches) != 1 || grepOutput.Matches[0].Path != "internal/test.go" {
		t.Fatalf("grep should return cwd-relative matches: %#v", grepOutput)
	}

	inspectResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "inspect_path",
		Arguments: map[string]any{
			"cwd_id":      setOutput.CwdID,
			"target_path": "internal/test.go",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	inspectOutput := decodeStructuredOutput[handler.InspectPathOutput](t, inspectResult.StructuredContent)
	requireCwdMeta(t, inspectOutput.CwdID, inspectOutput.Cwd, setOutput.CwdID, root)
	if inspectOutput.Path != "internal/test.go" || inspectOutput.ResolvedPath != "internal/test.go" {
		t.Fatalf("inspect_path should return cwd-relative paths: %#v", inspectOutput)
	}

	workspaceResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "workspace_inventory",
		Arguments: map[string]any{
			"cwd_id":           setOutput.CwdID,
			"target_directory": ".",
			"max_depth":        1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	workspaceOutput := decodeStructuredOutput[handler.WorkspaceInventoryOutput](t, workspaceResult.StructuredContent)
	requireCwdMeta(t, workspaceOutput.CwdID, workspaceOutput.Cwd, setOutput.CwdID, root)
	if workspaceOutput.Root == nil || workspaceOutput.Root.Path != "." {
		t.Fatalf("workspace_inventory should return cwd-relative root: %#v", workspaceOutput)
	}

	outlineResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "outline_file",
		Arguments: map[string]any{
			"cwd_id":      setOutput.CwdID,
			"target_file": "internal/test.go",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	outlineOutput := decodeStructuredOutput[handler.OutlineFileOutput](t, outlineResult.StructuredContent)
	requireCwdMeta(t, outlineOutput.CwdID, outlineOutput.Cwd, setOutput.CwdID, root)
	if outlineOutput.File != "internal/test.go" || outlineOutput.Fingerprint == nil {
		t.Fatalf("outline_file should return cwd-relative file and fingerprint: %#v", outlineOutput)
	}
}

func TestMCPCwdErrorsAndReplayHints(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.txt")
	if err := os.WriteFile(source, []byte("one\n\ntwo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside\n"), 0644); err != nil {
		t.Fatal(err)
	}
	symlinkAvailable := os.Symlink(outside, filepath.Join(root, "link-out")) == nil
	chainedSymlinkAvailable := symlinkAvailable && os.Symlink(filepath.Join(root, "link-out"), filepath.Join(root, "link-mid")) == nil

	cfg := config.Load()
	cfg.CwdStatePath = filepath.Join(t.TempDir(), "cwd-state.sqlite")
	cfg.CwdStateConfigError = ""

	withMCPTestSession(t, cfg, func(ctx context.Context, session *mcp.ClientSession) {
		cwdID := callSetCwdForTest(t, ctx, session, root)

		invalidResult, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "read_file",
			Arguments: map[string]any{"cwd_id": 0, "target_file": "source.txt"},
		})
		if err != nil {
			t.Fatal(err)
		}
		invalidOutput := decodeStructuredOutput[handler.ReadFileOutput](t, invalidResult.StructuredContent)
		if !invalidResult.IsError || invalidOutput.ErrorCode != "invalid_cwd_id" || invalidOutput.CwdID != nil {
			t.Fatalf("invalid cwd_id should not echo raw id: result=%#v output=%#v", invalidResult, invalidOutput)
		}

		unknownResult, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "read_file",
			Arguments: map[string]any{"cwd_id": cwdID + 1000, "target_file": "source.txt"},
		})
		if err != nil {
			t.Fatal(err)
		}
		unknownOutput := decodeStructuredOutput[handler.ReadFileOutput](t, unknownResult.StructuredContent)
		if !unknownResult.IsError || unknownOutput.ErrorCode != "cwd_id_unknown" || unknownOutput.CwdID == nil || *unknownOutput.CwdID != cwdID+1000 {
			t.Fatalf("unknown positive cwd_id should be actionable and echoed: %#v", unknownOutput)
		}
		if unknownOutput.ActionHint == nil || unknownOutput.ActionHint.RecommendedNextTool != "set_cwd" || unknownOutput.ActionHint.RecommendedNextInput != nil {
			t.Fatalf("unknown cwd_id should recommend set_cwd without embedded directory: %#v", unknownOutput.ActionHint)
		}

		invalidArgumentResult, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "read_file",
			Arguments: map[string]any{"cwd_id": cwdID, "target_file": 123},
		})
		if err != nil {
			t.Fatal(err)
		}
		invalidArgumentOutput := decodeStructuredOutput[handler.ReadFileOutput](t, invalidArgumentResult.StructuredContent)
		requireCwdMeta(t, invalidArgumentOutput.CwdID, invalidArgumentOutput.Cwd, cwdID, root)
		if !invalidArgumentResult.IsError || invalidArgumentOutput.ErrorCode == "" || invalidArgumentOutput.ActionHint == nil {
			t.Fatalf("cwd-aware invalid JSON arguments should preserve cwd recovery metadata: result=%#v output=%#v", invalidArgumentResult, invalidArgumentOutput)
		}
		if !strings.Contains(invalidArgumentOutput.Error, "Invalid JSON arguments") {
			t.Fatalf("cwd-aware invalid JSON arguments should preserve the decode error: %#v", invalidArgumentOutput)
		}

		absoluteResult, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name: "read_file",
			Arguments: map[string]any{
				"cwd_id":      cwdID,
				"target_file": source,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		absoluteOutput := decodeStructuredOutput[handler.ReadFileOutput](t, absoluteResult.StructuredContent)
		requireCwdMeta(t, absoluteOutput.CwdID, absoluteOutput.Cwd, cwdID, root)
		if !absoluteResult.IsError || absoluteOutput.ErrorCode != "absolute_path_not_allowed_with_cwd" {
			t.Fatalf("absolute path with cwd_id should be rejected with cwd metadata: %#v", absoluteOutput)
		}
		if strings.Contains(absoluteOutput.Error, filepath.ToSlash(root)) {
			t.Fatalf("cwd-aware path error should not echo absolute input: %#v", absoluteOutput)
		}

		escapeResult, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name: "read_file",
			Arguments: map[string]any{
				"cwd_id":      cwdID,
				"target_file": "../outside.txt",
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		escapeOutput := decodeStructuredOutput[handler.ReadFileOutput](t, escapeResult.StructuredContent)
		requireCwdMeta(t, escapeOutput.CwdID, escapeOutput.Cwd, cwdID, root)
		if !escapeResult.IsError || escapeOutput.ErrorCode != "path_outside_cwd" {
			t.Fatalf("cwd escape should be rejected as path_outside_cwd: %#v", escapeOutput)
		}

		if symlinkAvailable {
			symlinkResult, err := session.CallTool(ctx, &mcp.CallToolParams{
				Name: "inspect_path",
				Arguments: map[string]any{
					"cwd_id":      cwdID,
					"target_path": "link-out",
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			symlinkOutput := decodeStructuredOutput[handler.InspectPathOutput](t, symlinkResult.StructuredContent)
			requireCwdMeta(t, symlinkOutput.CwdID, symlinkOutput.Cwd, cwdID, root)
			if symlinkOutput.Kind != "symlink" || !symlinkOutput.SymlinkTargetOutsideCwd || symlinkOutput.SymlinkTarget != "" {
				t.Fatalf("outside-cwd symlink target should be flagged without path leak: %#v", symlinkOutput)
			}

			grepResult, err := session.CallTool(ctx, &mcp.CallToolParams{
				Name: "grep",
				Arguments: map[string]any{
					"cwd_id":  cwdID,
					"path":    ".",
					"pattern": "outside",
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			grepOutput := decodeStructuredOutput[handler.GrepOutput](t, grepResult.StructuredContent)
			requireCwdMeta(t, grepOutput.CwdID, grepOutput.Cwd, cwdID, root)
			if grepResult.IsError || len(grepOutput.Matches) != 0 || len(grepOutput.Files) != 0 || strings.Contains(grepOutput.Text, "link-out") {
				t.Fatalf("grep should not read outside-cwd symlink target: result=%#v output=%#v", grepResult, grepOutput)
			}

			multilineGrepResult, err := session.CallTool(ctx, &mcp.CallToolParams{
				Name: "grep",
				Arguments: map[string]any{
					"cwd_id":    cwdID,
					"path":      ".",
					"pattern":   "outside",
					"multiline": true,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			multilineGrepOutput := decodeStructuredOutput[handler.GrepOutput](t, multilineGrepResult.StructuredContent)
			requireCwdMeta(t, multilineGrepOutput.CwdID, multilineGrepOutput.Cwd, cwdID, root)
			if multilineGrepResult.IsError || len(multilineGrepOutput.Matches) != 0 || len(multilineGrepOutput.Files) != 0 || strings.Contains(multilineGrepOutput.Text, "link-out") {
				t.Fatalf("multiline grep should not read outside-cwd symlink target: result=%#v output=%#v", multilineGrepResult, multilineGrepOutput)
			}

			globResult, err := session.CallTool(ctx, &mcp.CallToolParams{
				Name: "glob_file_search",
				Arguments: map[string]any{
					"cwd_id":           cwdID,
					"target_directory": ".",
					"glob_pattern":     "link-out",
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			globOutput := decodeStructuredOutput[handler.GlobFileSearchOutput](t, globResult.StructuredContent)
			requireCwdMeta(t, globOutput.CwdID, globOutput.Cwd, cwdID, root)
			if globResult.IsError || len(globOutput.Files) != 0 || globOutput.TotalMatchCount != 0 {
				t.Fatalf("glob_file_search should not expose outside-cwd symlink file: result=%#v output=%#v", globResult, globOutput)
			}
		}
		if chainedSymlinkAvailable {
			chainedSymlinkResult, err := session.CallTool(ctx, &mcp.CallToolParams{
				Name: "inspect_path",
				Arguments: map[string]any{
					"cwd_id":      cwdID,
					"target_path": "link-mid",
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			chainedSymlinkOutput := decodeStructuredOutput[handler.InspectPathOutput](t, chainedSymlinkResult.StructuredContent)
			requireCwdMeta(t, chainedSymlinkOutput.CwdID, chainedSymlinkOutput.Cwd, cwdID, root)
			if chainedSymlinkOutput.Kind != "symlink" || !chainedSymlinkOutput.SymlinkTargetOutsideCwd || chainedSymlinkOutput.SymlinkTarget != "" {
				t.Fatalf("chained outside-cwd symlink target should be flagged without path leak: %#v", chainedSymlinkOutput)
			}
		}

		outlineResult, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name: "outline_file",
			Arguments: map[string]any{
				"cwd_id":      cwdID,
				"target_file": "source.txt",
				"max_items":   1,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		outlineOutput := decodeStructuredOutput[handler.OutlineFileOutput](t, outlineResult.StructuredContent)
		requireCwdMeta(t, outlineOutput.CwdID, outlineOutput.Cwd, cwdID, root)
		if outlineOutput.ParserStatus != "generic_text" || outlineOutput.Fingerprint == nil || outlineOutput.NextRecommendedCall == nil {
			t.Fatalf("generic text outline should truncate with replay hint: %#v", outlineOutput)
		}
		if got := cwdIDFromRecommendedInput(t, outlineOutput.NextRecommendedCall.RecommendedNextInput); got != cwdID {
			t.Fatalf("outline replay cwd_id = %d, want %d; hint=%#v", got, cwdID, outlineOutput.NextRecommendedCall)
		}
		if outlineOutput.NextRecommendedCall.RecommendedNextInput["target_file"] != "source.txt" {
			t.Fatalf("outline replay target should be cwd-relative: %#v", outlineOutput.NextRecommendedCall)
		}

		for _, toolName := range []string{"copy_ranges", "move_ranges", "copy_ranges_batch", "move_ranges_batch"} {
			assertRefactorCwdPathError(t, ctx, session, toolName, cwdID, source, outlineOutput.Fingerprint, "absolute_path_not_allowed_with_cwd")
			assertRefactorCwdPathError(t, ctx, session, toolName, cwdID, "../source.txt", outlineOutput.Fingerprint, "path_outside_cwd")
		}

		if symlinkAvailable {
			symlinkWriteResult, err := session.CallTool(ctx, &mcp.CallToolParams{
				Name: "copy_ranges",
				Arguments: map[string]any{
					"cwd_id":             cwdID,
					"source_file":        "source.txt",
					"source_fingerprint": outlineOutput.Fingerprint,
					"ranges":             []map[string]any{{"start_line": 1, "end_line": 1}},
					"target_file":        "link-out",
					"target_precondition": map[string]any{
						"must_not_exist": true,
					},
					"placement": map[string]any{
						"mode": "create_new",
					},
					"dry_run": true,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			symlinkWriteOutput := decodeStructuredOutput[handler.CopyRangesOutput](t, symlinkWriteResult.StructuredContent)
			requireCwdMeta(t, symlinkWriteOutput.CwdID, symlinkWriteOutput.Cwd, cwdID, root)
			if !symlinkWriteResult.IsError || symlinkWriteOutput.Error == "" {
				t.Fatalf("copy_ranges symlink target should fail with structured error: %#v", symlinkWriteOutput)
			}
			if strings.Contains(symlinkWriteOutput.Error, filepath.ToSlash(root)) || strings.Contains(symlinkWriteOutput.Error, root) {
				t.Fatalf("cwd-aware refactor error should not leak absolute cwd path: %#v", symlinkWriteOutput)
			}
		}

		copyResult, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name: "copy_ranges",
			Arguments: map[string]any{
				"cwd_id":             cwdID,
				"source_file":        "source.txt",
				"source_fingerprint": outlineOutput.Fingerprint,
				"ranges":             []map[string]any{{"start_line": 99, "end_line": 99}},
				"target_file":        "target.txt",
				"target_precondition": map[string]any{
					"must_not_exist": true,
				},
				"placement": map[string]any{
					"mode": "create_new",
				},
				"dry_run": true,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		copyOutput := decodeStructuredOutput[handler.CopyRangesOutput](t, copyResult.StructuredContent)
		if !copyResult.IsError || copyOutput.ActionHint == nil || copyOutput.ActionHint.RecommendedNextTool != "outline_file" {
			t.Fatalf("copy_ranges out-of-bounds should expose outline action hint: %#v", copyOutput)
		}
		if got := cwdIDFromRecommendedInput(t, copyOutput.ActionHint.RecommendedNextInput); got != cwdID {
			t.Fatalf("copy_ranges action hint cwd_id = %d, want %d; hint=%#v", got, cwdID, copyOutput.ActionHint)
		}
		if copyOutput.ActionHint.RecommendedNextInput["target_file"] != "source.txt" {
			t.Fatalf("copy_ranges action hint target should be cwd-relative source: %#v", copyOutput.ActionHint)
		}
	})
}

func assertRefactorCwdPathError(t *testing.T, ctx context.Context, session *mcp.ClientSession, toolName string, cwdID int64, sourceFile string, fingerprint *handler.FileFingerprint, expectedCode string) {
	t.Helper()
	args := refactorCwdPathErrorArgs(toolName, cwdID, sourceFile, fingerprint)
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: toolName, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	output := decodeStructuredMap(t, result.StructuredContent)
	if !result.IsError || output["error_code"] != expectedCode {
		t.Fatalf("%s should return %s for cwd path contract violation: result=%#v output=%#v", toolName, expectedCode, result, output)
	}
	hint, ok := output["action_hint"].(map[string]any)
	if !ok || hint["safe_to_retry"] != false || hint["reason"] == "" {
		t.Fatalf("%s cwd path error should include targeted action_hint: %#v", toolName, output)
	}
}

func refactorCwdPathErrorArgs(toolName string, cwdID int64, sourceFile string, fingerprint *handler.FileFingerprint) map[string]any {
	ranges := []map[string]any{{"start_line": 1, "end_line": 1}}
	targetPrecondition := map[string]any{"must_not_exist": true}
	placement := map[string]any{"mode": "create_new"}
	if strings.Contains(toolName, "batch") {
		return map[string]any{
			"cwd_id":             cwdID,
			"source_file":        sourceFile,
			"source_fingerprint": fingerprint,
			"targets": []map[string]any{{
				"target_file":         "target-batch.txt",
				"target_precondition": targetPrecondition,
				"placement":           placement,
				"ranges":              ranges,
			}},
			"dry_run": true,
		}
	}
	return map[string]any{
		"cwd_id":              cwdID,
		"source_file":         sourceFile,
		"source_fingerprint":  fingerprint,
		"ranges":              ranges,
		"target_file":         "target-single.txt",
		"target_precondition": targetPrecondition,
		"placement":           placement,
		"dry_run":             true,
	}
}

func findTool(t *testing.T, tools []*mcp.Tool, name string) *mcp.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func withMCPTestSession(t *testing.T, cfg *config.Config, fn func(context.Context, *mcp.ClientSession)) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	server := NewServer(nil, cfg)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- server.Run(ctx, serverTransport)
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = session.Close()
		cancel()
		<-serverDone
	}()

	fn(ctx, session)
}

func callSetCwdForTest(t *testing.T, ctx context.Context, session *mcp.ClientSession, directory string) int64 {
	t.Helper()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "set_cwd",
		Arguments: map[string]any{"directory": directory},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("set_cwd returned error: %#v", result)
	}
	output := decodeStructuredOutput[handler.SetCwdOutput](t, result.StructuredContent)
	if output.CwdID < 1 {
		t.Fatalf("set_cwd returned invalid cwd_id: %#v", output)
	}
	return output.CwdID
}

func cwdIDFromRecommendedInput(t *testing.T, input map[string]any) int64 {
	t.Helper()
	value, ok := input["cwd_id"]
	if !ok {
		t.Fatalf("recommended_next_input missing cwd_id: %#v", input)
	}
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	default:
		t.Fatalf("recommended_next_input cwd_id has unexpected type %T: %#v", value, input)
	}
	return 0
}

func decodeToolInputSchema(t *testing.T, value any) *jsonschema.Schema {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	return &schema
}

func requireSchemaField(t *testing.T, schema *jsonschema.Schema, name string) {
	t.Helper()
	for _, required := range schema.Required {
		if required == name {
			return
		}
	}
	t.Fatalf("schema does not mark %q as required; required=%v", name, schema.Required)
}

func requireSchemaProperty(t *testing.T, schema *jsonschema.Schema, name string) {
	t.Helper()
	if _, ok := schema.Properties[name]; !ok {
		t.Fatalf("schema is missing property %q; properties=%#v", name, schema.Properties)
	}
}

func requireSetCwdOutputSchema(t *testing.T, schema *jsonschema.Schema) {
	t.Helper()
	if len(schema.OneOf) != 2 {
		t.Fatalf("set_cwd output schema should use success/error oneOf, got %#v", schema.OneOf)
	}
	success := schema.OneOf[0]
	requireSchemaProperty(t, success, "cwd_id")
	requireSchemaField(t, success, "cwd_id")
	forbidSchemaProperty(t, success, "error")
	forbidSchemaProperty(t, success, "error_code")
	forbidSchemaProperty(t, success, "action_hint")
}

func requireAbsolutePathSchema(t *testing.T, schema *jsonschema.Schema, name string) {
	t.Helper()
	property, ok := schema.Properties[name]
	if !ok {
		t.Fatalf("schema is missing property %q; properties=%#v", name, schema.Properties)
	}
	if property.MinLength == nil || *property.MinLength != 1 {
		t.Fatalf("schema property %q should reject empty paths: %#v", name, property)
	}
	if property.Description == "" {
		t.Fatalf("schema property %q should document path mode: %#v", name, property)
	}
}

func requireAbsolutePathArrayItemSchema(t *testing.T, schema *jsonschema.Schema, name string) {
	t.Helper()
	property, ok := schema.Properties[name]
	if !ok || property.Items == nil {
		t.Fatalf("schema is missing array property %q with items; properties=%#v", name, schema.Properties)
	}
	if property.Items.MinLength == nil || *property.Items.MinLength != 1 {
		t.Fatalf("schema property %q items should reject empty paths: %#v", name, property.Items)
	}
	if property.Items.Description == "" {
		t.Fatalf("schema property %q items should document path mode: %#v", name, property.Items)
	}
}

func requireNestedArrayObjectAbsolutePathSchema(t *testing.T, schema *jsonschema.Schema, arrayName, propertyName string) {
	t.Helper()
	arrayProperty, ok := schema.Properties[arrayName]
	if !ok || arrayProperty.Items == nil {
		t.Fatalf("schema is missing array property %q with items; properties=%#v", arrayName, schema.Properties)
	}
	property, ok := arrayProperty.Items.Properties[propertyName]
	if !ok {
		t.Fatalf("schema property %q[] is missing nested property %q; properties=%#v", arrayName, propertyName, arrayProperty.Items.Properties)
	}
	if property.MinLength == nil || *property.MinLength != 1 {
		t.Fatalf("schema property %q[].%s should reject empty paths: %#v", arrayName, propertyName, property)
	}
	if property.Description == "" {
		t.Fatalf("schema property %q[].%s should document path mode: %#v", arrayName, propertyName, property)
	}
}

func forbidSchemaProperty(t *testing.T, schema *jsonschema.Schema, name string) {
	t.Helper()
	if _, ok := schema.Properties[name]; ok {
		t.Fatalf("schema should not expose property %q; properties=%#v", name, schema.Properties)
	}
}

func decodeStructuredOutput[Out any](t *testing.T, value any) Out {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var output Out
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatal(err)
	}
	return output
}

func decodeStructuredMap(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatal(err)
	}
	return output
}

func requireCwdMeta(t *testing.T, gotID *int64, gotCwd string, wantID int64, wantCwd string) {
	t.Helper()
	if gotID == nil || *gotID != wantID {
		t.Fatalf("cwd_id metadata = %#v, want %d", gotID, wantID)
	}
	if gotCwd != filepath.ToSlash(wantCwd) {
		t.Fatalf("cwd metadata = %q, want %q", gotCwd, filepath.ToSlash(wantCwd))
	}
}
