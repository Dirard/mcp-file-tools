package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

type tokenLedgerEntry struct {
	WorkflowName          string                    `json:"workflow_name"`
	ToolCalls             int                       `json:"tool_calls"`
	TotalNormalizedBytes  int                       `json:"total_normalized_bytes"`
	RawSerializedBytes    int                       `json:"raw_serialized_bytes"`
	EstimatedTokens       int                       `json:"estimated_tokens"`
	Components            tokenLedgerComponentBytes `json:"components"`
	PreviewCaveatObserved bool                      `json:"preview_caveat_observed,omitempty"`
}

type tokenLedgerComponentBytes struct {
	ToolMetadata       int `json:"tool_metadata"`
	RequestJSON        int `json:"request_json"`
	ResponseMetadata   int `json:"response_metadata"`
	ResponseContent    int `json:"response_content"`
	NextCallHints      int `json:"next_call_hints"`
	ErrorGuidance      int `json:"error_guidance"`
	RetriesOrFallbacks int `json:"retries_or_fallbacks"`
}

func phase10BaselineLedger() map[string]tokenLedgerEntry {
	return map[string]tokenLedgerEntry{
		"discovery_to_read":                  {WorkflowName: "discovery_to_read", ToolCalls: 5, TotalNormalizedBytes: 235118, RawSerializedBytes: 237853, EstimatedTokens: 58780, Components: tokenLedgerComponentBytes{ToolMetadata: 226611, RequestJSON: 423, ResponseMetadata: 8084, ResponseContent: 0, NextCallHints: 2908, ErrorGuidance: 1190, RetriesOrFallbacks: 0}},
		"grep_to_read_range":                 {WorkflowName: "grep_to_read_range", ToolCalls: 2, TotalNormalizedBytes: 229204, RawSerializedBytes: 230079, EstimatedTokens: 57301, Components: tokenLedgerComponentBytes{ToolMetadata: 226611, RequestJSON: 175, ResponseMetadata: 2418, ResponseContent: 67, NextCallHints: 1009, ErrorGuidance: 348, RetriesOrFallbacks: 0}},
		"chunked_read_continuation":          {WorkflowName: "chunked_read_continuation", ToolCalls: 2, TotalNormalizedBytes: 230197, RawSerializedBytes: 231485, EstimatedTokens: 57550, Components: tokenLedgerComponentBytes{ToolMetadata: 226611, RequestJSON: 318, ResponseMetadata: 3268, ResponseContent: 0, NextCallHints: 1922, ErrorGuidance: 530, RetriesOrFallbacks: 0}},
		"resolve_dry_run_to_read_validation": {WorkflowName: "resolve_dry_run_to_read_validation", ToolCalls: 4, TotalNormalizedBytes: 233866, RawSerializedBytes: 236269, EstimatedTokens: 58467, Components: tokenLedgerComponentBytes{ToolMetadata: 226611, RequestJSON: 775, ResponseMetadata: 6480, ResponseContent: 345, NextCallHints: 1759, ErrorGuidance: 507, RetriesOrFallbacks: 0}},
		"batch_dry_run":                      {WorkflowName: "batch_dry_run", ToolCalls: 1, TotalNormalizedBytes: 229996, RawSerializedBytes: 231112, EstimatedTokens: 57499, Components: tokenLedgerComponentBytes{ToolMetadata: 226611, RequestJSON: 504, ResponseMetadata: 2881, ResponseContent: 542, NextCallHints: 0, ErrorGuidance: 0, RetriesOrFallbacks: 0}},
		"read_files_continuation":            {WorkflowName: "read_files_continuation", ToolCalls: 2, TotalNormalizedBytes: 230634, RawSerializedBytes: 231970, EstimatedTokens: 57659, Components: tokenLedgerComponentBytes{ToolMetadata: 226611, RequestJSON: 346, ResponseMetadata: 3677, ResponseContent: 65, NextCallHints: 1954, ErrorGuidance: 580, RetriesOrFallbacks: 0}},
		"stale_continuation_refresh":         {WorkflowName: "stale_continuation_refresh", ToolCalls: 2, TotalNormalizedBytes: 227561, RawSerializedBytes: 228121, EstimatedTokens: 56891, Components: tokenLedgerComponentBytes{ToolMetadata: 226611, RequestJSON: 270, ResponseMetadata: 680, ResponseContent: 0, NextCallHints: 0, ErrorGuidance: 101, RetriesOrFallbacks: 0}},
		"stale_resolve_refresh":              {WorkflowName: "stale_resolve_refresh", ToolCalls: 2, TotalNormalizedBytes: 228044, RawSerializedBytes: 228692, EstimatedTokens: 57011, Components: tokenLedgerComponentBytes{ToolMetadata: 226611, RequestJSON: 226, ResponseMetadata: 1207, ResponseContent: 0, NextCallHints: 290, ErrorGuidance: 257, RetriesOrFallbacks: 0}},
		"ordinary_non_stale_failure":         {WorkflowName: "ordinary_non_stale_failure", ToolCalls: 1, TotalNormalizedBytes: 227433, RawSerializedBytes: 227740, EstimatedTokens: 56859, Components: tokenLedgerComponentBytes{ToolMetadata: 226611, RequestJSON: 70, ResponseMetadata: 752, ResponseContent: 0, NextCallHints: 301, ErrorGuidance: 235, RetriesOrFallbacks: 0}},
		"no_result_discovery":                {WorkflowName: "no_result_discovery", ToolCalls: 1, TotalNormalizedBytes: 227563, RawSerializedBytes: 227726, EstimatedTokens: 56891, Components: tokenLedgerComponentBytes{ToolMetadata: 226611, RequestJSON: 71, ResponseMetadata: 881, ResponseContent: 0, NextCallHints: 0, ErrorGuidance: 265, RetriesOrFallbacks: 0}},
		"truncated_discovery_continuation":   {WorkflowName: "truncated_discovery_continuation", ToolCalls: 2, TotalNormalizedBytes: 230726, RawSerializedBytes: 232195, EstimatedTokens: 57682, Components: tokenLedgerComponentBytes{ToolMetadata: 226611, RequestJSON: 335, ResponseMetadata: 3780, ResponseContent: 0, NextCallHints: 2042, ErrorGuidance: 686, RetriesOrFallbacks: 0}},
	}
}

func TestPhase10TokenLedgerBaseline(t *testing.T) {
	baseline := phase10BaselineLedger()
	if len(baseline) != 11 {
		t.Fatalf("unexpected baseline workflow count: got=%d want=11", len(baseline))
	}
	for name, entry := range baseline {
		if entry.TotalNormalizedBytes == 0 {
			t.Fatalf("baseline fixture for %s is not frozen yet: %#v", name, entry)
		}
	}
}

func TestPhase10TokenLedgerBudgetBeatsBaseline(t *testing.T) {
	entries := phase10TokenLedgerEntries(t)
	baselineLedger := phase10BaselineLedger()
	if len(entries) != len(baselineLedger) {
		t.Fatalf("unexpected workflow count: got=%d want=%d", len(entries), len(baselineLedger))
	}
	baselineTotal := 0
	currentTotal := 0
	nonOutlineWins := 0
	for _, entry := range entries {
		baseline, ok := baselineLedger[entry.WorkflowName]
		if !ok {
			t.Fatalf("unexpected workflow %q: %#v", entry.WorkflowName, entry)
		}
		baselineTotal += baseline.TotalNormalizedBytes
		currentTotal += entry.TotalNormalizedBytes
		if entry.ToolCalls > baseline.ToolCalls {
			t.Fatalf("%s increased tool calls: current=%d baseline=%d", entry.WorkflowName, entry.ToolCalls, baseline.ToolCalls)
		}
		if entry.TotalNormalizedBytes*100 > baseline.TotalNormalizedBytes*103 {
			t.Fatalf("%s grew beyond 3%% guardrail: current=%d baseline=%d", entry.WorkflowName, entry.TotalNormalizedBytes, baseline.TotalNormalizedBytes)
		}
		if entry.WorkflowName != "discovery_to_read" && entry.TotalNormalizedBytes*100 <= baseline.TotalNormalizedBytes*90 {
			nonOutlineWins++
		}
		if entry.WorkflowName == "resolve_dry_run_to_read_validation" && !entry.PreviewCaveatObserved {
			t.Fatalf("dry-run workflow must expose bounded-preview/read-back caveat in compact/default output: %#v", entry)
		}
	}
	if currentTotal*100 > baselineTotal*85 {
		t.Fatalf("aggregate workflow bytes should drop at least 15%%: current=%d baseline=%d", currentTotal, baselineTotal)
	}
	if nonOutlineWins < 2 {
		t.Fatalf("expected at least two non-outline workflow families to drop at least 10%%, got %d", nonOutlineWins)
	}
}

func TestPhase10TokenLedgerWorkflowTotals(t *testing.T) {
	entries := phase10TokenLedgerEntries(t)
	total := 0
	toolCalls := 0
	for _, entry := range entries {
		if entry.ToolCalls < 1 {
			t.Fatalf("%s should record at least one tool call: %#v", entry.WorkflowName, entry)
		}
		if entry.Components.ToolMetadata == 0 || entry.Components.RequestJSON == 0 || entry.TotalNormalizedBytes == 0 {
			t.Fatalf("%s should include metadata/request/total bytes: %#v", entry.WorkflowName, entry)
		}
		total += entry.TotalNormalizedBytes
		toolCalls += entry.ToolCalls
	}
	if total == 0 || toolCalls == 0 {
		t.Fatalf("ledger should not be empty: total=%d calls=%d", total, toolCalls)
	}
}

func TestPhase10DefaultRedactionStillOff(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "notes.txt")
	if err := os.WriteFile(file, []byte("alpha\nbeta\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	readFilesResult, readFilesOutput, err := h.HandleReadFiles(context.Background(), nil, ReadFilesInput{
		Items: []ReadFileInputItem{{TargetFile: file}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if readFilesResult.IsError || readFilesOutput.RedactionMode != redactionOff || len(readFilesOutput.Items) != 1 || readFilesOutput.Items[0].RedactionMode != redactionOff {
		t.Fatalf("read_files omitted redaction_mode should default to off: result=%#v output=%#v", readFilesResult, readFilesOutput)
	}
	grepResult, grepOutput, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:        tempDir,
		Pattern:     "alpha",
		PatternMode: "literal",
		OutputMode:  "content",
	})
	if err != nil {
		t.Fatal(err)
	}
	if grepResult.IsError || grepOutput.RedactionMode != redactionOff {
		t.Fatalf("grep omitted redaction_mode should default to off: result=%#v output=%#v", grepResult, grepOutput)
	}
	_, outline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file, OutputProfile: outlineProfileFingerprintOnly})
	if err != nil {
		t.Fatal(err)
	}
	if outline.Fingerprint == nil {
		t.Fatalf("fingerprint outline failed: %#v", outline)
	}
	target := filepath.Join(tempDir, "copy.txt")
	copyResult, copyOutput, err := h.HandleCopyRanges(context.Background(), nil, CopyRangesInput{
		SourceFile:         file,
		SourceFingerprint:  *outline.Fingerprint,
		Ranges:             []SourceLineRange{{StartLine: 1, EndLine: 1}},
		TargetFile:         target,
		TargetPrecondition: TargetPrecondition{MustNotExist: true},
		Placement:          TargetPlacement{Mode: placementCreateNew},
		DryRun:             true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if copyResult.IsError || copyOutput.Validation.RedactionMode != redactionOff {
		t.Fatalf("range dry-run omitted redaction_mode should default to off: result=%#v output=%#v", copyResult, copyOutput)
	}
}

func TestPhase10AgentOutputSchemasValidateRepresentativeRuntimeOutputs(t *testing.T) {
	tempDir := t.TempDir()
	textFile := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(textFile, []byte("alpha\nbeta\n"), 0644); err != nil {
		t.Fatal(err)
	}
	goFile := filepath.Join(tempDir, "app.go")
	if err := os.WriteFile(goFile, []byte("package main\n\nfunc loadConfig() string { return \"ok\" }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()

	_, readFileOutput, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{TargetFile: textFile, CountTotalLines: true})
	if err != nil {
		t.Fatal(err)
	}
	phase10AssertSchemaValid(t, "read_file", phase10CurrentOutputSchemaForTool(t, "read_file"), readFileOutput)

	_, readFilesOutput, err := h.HandleReadFiles(context.Background(), nil, ReadFilesInput{Items: []ReadFileInputItem{{TargetFile: textFile}}})
	if err != nil {
		t.Fatal(err)
	}
	phase10AssertSchemaValid(t, "read_files", phase10CurrentOutputSchemaForTool(t, "read_files"), readFilesOutput)

	_, listDirOutput, err := h.HandleListDir(context.Background(), nil, ListDirInput{TargetDirectory: tempDir})
	if err != nil {
		t.Fatal(err)
	}
	phase10AssertSchemaValid(t, "list_dir", phase10CurrentOutputSchemaForTool(t, "list_dir"), listDirOutput)

	_, globOutput, err := h.HandleGlobFileSearch(context.Background(), nil, GlobFileSearchInput{TargetDirectory: tempDir, GlobPattern: "*.go", Limit: intPtr(5)})
	if err != nil {
		t.Fatal(err)
	}
	phase10AssertSchemaValid(t, "glob_file_search", phase10CurrentOutputSchemaForTool(t, "glob_file_search"), globOutput)

	_, grepOutput, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{Path: tempDir, Pattern: "loadConfig", PatternMode: "literal", OutputMode: "content", Glob: "*.go"})
	if err != nil {
		t.Fatal(err)
	}
	phase10AssertSchemaValid(t, "grep", phase10CurrentOutputSchemaForTool(t, "grep"), grepOutput)

	_, inspectOutput, err := h.HandleInspectPath(context.Background(), nil, InspectPathInput{TargetPath: textFile})
	if err != nil {
		t.Fatal(err)
	}
	phase10AssertSchemaValid(t, "inspect_path", phase10CurrentOutputSchemaForTool(t, "inspect_path"), inspectOutput)

	_, inventoryOutput, err := h.HandleWorkspaceInventory(context.Background(), nil, WorkspaceInventoryInput{TargetDirectory: tempDir, MaxDepth: intPtr(1), Limit: intPtr(10), IncludeSummary: boolPtrForPhase10Test(true)})
	if err != nil {
		t.Fatal(err)
	}
	phase10AssertSchemaValid(t, "workspace_inventory", phase10CurrentOutputSchemaForTool(t, "workspace_inventory"), inventoryOutput)

	_, outlineOutput, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: goFile})
	if err != nil {
		t.Fatal(err)
	}
	phase10AssertSchemaValid(t, "outline_file", phase10CurrentOutputSchemaForTool(t, "outline_file"), outlineOutput)
	item := findOutlineItemByName(outlineOutput.Symbols, "loadConfig")
	if item == nil || outlineOutput.Fingerprint == nil {
		t.Fatalf("outline fixture should expose loadConfig and fingerprint: %#v", outlineOutput)
	}

	_, resolveOutput, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{SourceFile: goFile, SourceFingerprint: *outlineOutput.Fingerprint, Selector: SymbolSelectorQuery{SymbolRef: item.SymbolRef}})
	if err != nil {
		t.Fatal(err)
	}
	phase10AssertSchemaValid(t, "resolve_symbol_range", phase10CurrentOutputSchemaForTool(t, "resolve_symbol_range"), resolveOutput)

	copyTarget := filepath.Join(tempDir, "copy.txt")
	_, copyOutput, err := h.HandleCopyRanges(context.Background(), nil, CopyRangesInput{
		SourceFile:         goFile,
		SourceFingerprint:  *outlineOutput.Fingerprint,
		Ranges:             []SourceLineRange{{StartLine: 3, EndLine: 3}},
		TargetFile:         copyTarget,
		TargetPrecondition: TargetPrecondition{MustNotExist: true},
		Placement:          TargetPlacement{Mode: placementCreateNew},
		DryRun:             true,
	})
	if err != nil {
		t.Fatal(err)
	}
	phase10AssertSchemaValid(t, "copy_ranges", phase10CurrentOutputSchemaForTool(t, "copy_ranges"), copyOutput)

	_, moveOutput, err := h.HandleMoveRanges(context.Background(), nil, MoveRangesInput(CopyRangesInput{
		SourceFile:         goFile,
		SourceFingerprint:  *outlineOutput.Fingerprint,
		Ranges:             []SourceLineRange{{StartLine: 3, EndLine: 3}},
		TargetFile:         filepath.Join(tempDir, "move.txt"),
		TargetPrecondition: TargetPrecondition{MustNotExist: true},
		Placement:          TargetPlacement{Mode: placementCreateNew},
		DryRun:             true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	phase10AssertSchemaValid(t, "move_ranges", phase10CurrentOutputSchemaForTool(t, "move_ranges"), moveOutput)

	_, copyBatchOutput, err := h.HandleCopyRangesBatch(context.Background(), nil, CopyRangesBatchInput{
		SourceFile:        goFile,
		SourceFingerprint: *outlineOutput.Fingerprint,
		Targets: []BatchRangeTarget{{
			TargetFile:         filepath.Join(tempDir, "copy-batch.txt"),
			TargetPrecondition: TargetPrecondition{MustNotExist: true},
			Placement:          TargetPlacement{Mode: placementCreateNew},
			Ranges:             []SourceLineRange{{StartLine: 3, EndLine: 3}},
		}},
		DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	phase10AssertSchemaValid(t, "copy_ranges_batch", phase10CurrentOutputSchemaForTool(t, "copy_ranges_batch"), copyBatchOutput)

	_, moveBatchOutput, err := h.HandleMoveRangesBatch(context.Background(), nil, MoveRangesBatchInput{
		SourceFile:        goFile,
		SourceFingerprint: *outlineOutput.Fingerprint,
		Targets: []BatchRangeTarget{{
			TargetFile:         filepath.Join(tempDir, "move-batch.txt"),
			TargetPrecondition: TargetPrecondition{MustNotExist: true},
			Placement:          TargetPlacement{Mode: placementCreateNew},
			Ranges:             []SourceLineRange{{StartLine: 3, EndLine: 3}},
		}},
		DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	phase10AssertSchemaValid(t, "move_ranges_batch", phase10CurrentOutputSchemaForTool(t, "move_ranges_batch"), moveBatchOutput)
}

func phase10TokenLedgerEntries(t *testing.T) []tokenLedgerEntry {
	t.Helper()
	return []tokenLedgerEntry{
		phase10DiscoveryToReadWorkflow(t),
		phase10GrepToReadRangeWorkflow(t),
		phase10ChunkedReadContinuationWorkflow(t),
		phase10ResolveDryRunToReadValidationWorkflow(t),
		phase10BatchDryRunWorkflow(t),
		phase10ReadFilesContinuationWorkflow(t),
		phase10StaleContinuationRefreshWorkflow(t),
		phase10StaleResolveRefreshWorkflow(t),
		phase10OrdinaryNonStaleFailureWorkflow(t),
		phase10NoResultDiscoveryWorkflow(t),
		phase10TruncatedDiscoveryContinuationWorkflow(t),
	}
}

func phase10DiscoveryToReadWorkflow(t *testing.T) tokenLedgerEntry {
	t.Helper()
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(srcDir, "app.go")
	source := "package main\n\nfunc loadConfig() string {\n\treturn \"ok\"\n}\n"
	if err := os.WriteFile(file, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	rec := newPhase10WorkflowRecorder(t, "discovery_to_read")
	_, inventory, err := h.HandleWorkspaceInventory(context.Background(), nil, WorkspaceInventoryInput{TargetDirectory: tempDir, MaxDepth: intPtr(2), Limit: intPtr(20)})
	if err != nil {
		t.Fatal(err)
	}
	rec.addCall("workspace_inventory", WorkspaceInventoryInput{TargetDirectory: tempDir, MaxDepth: intPtr(2), Limit: intPtr(20)}, inventory)
	globDir := ""
	for _, entry := range inventory.DirectoriesPage {
		if strings.HasSuffix(normalizeToolPath(entry.Path), "/src") {
			globDir = entry.Path
			break
		}
	}
	if globDir == "" {
		t.Fatalf("inventory should expose src directory for derived glob: %#v", inventory.DirectoriesPage)
	}
	globInput := GlobFileSearchInput{TargetDirectory: globDir, GlobPattern: "*.go", Limit: intPtr(5)}
	_, globOutput, err := h.HandleGlobFileSearch(context.Background(), nil, globInput)
	if err != nil {
		t.Fatal(err)
	}
	rec.addCall("glob_file_search", globInput, globOutput)
	if len(globOutput.Files) == 0 {
		t.Fatalf("glob output should expose files: %#v", globOutput)
	}
	outlineInput := OutlineFileInput{TargetFile: globOutput.Files[0].Path}
	_, outline, err := h.HandleOutlineFile(context.Background(), nil, outlineInput)
	if err != nil {
		t.Fatal(err)
	}
	rec.addCall("outline_file", outlineInput, outline)
	item := findOutlineItemByName(outline.Symbols, "loadConfig")
	if item == nil || item.SymbolRef == "" || outline.Fingerprint == nil {
		t.Fatalf("outline should expose symbol_ref and fingerprint: %#v", outline)
	}
	resolveInput := ResolveSymbolRangeInput{SourceFile: outline.File, SourceFingerprint: *outline.Fingerprint, Selector: SymbolSelectorQuery{SymbolRef: item.SymbolRef}}
	_, resolved, err := h.HandleResolveSymbolRange(context.Background(), nil, resolveInput)
	if err != nil {
		t.Fatal(err)
	}
	rec.addCall("resolve_symbol_range", resolveInput, resolved)
	if len(resolved.ResolvedRanges) != 1 {
		t.Fatalf("resolve should expose one range: %#v", resolved)
	}
	r := resolved.ResolvedRanges[0].Range
	readInput := ReadFileInput{TargetFile: resolved.File, StartLine: &r.StartLine, EndLine: &r.EndLine}
	_, readOutput, err := h.HandleReadFile(context.Background(), nil, readInput)
	if err != nil {
		t.Fatal(err)
	}
	rec.addCall("read_file", readInput, readOutput)
	if !strings.Contains(readOutput.Text, "loadConfig") {
		t.Fatalf("derived read should include symbol text: %#v", readOutput)
	}
	return rec.entry()
}

func phase10GrepToReadRangeWorkflow(t *testing.T) tokenLedgerEntry {
	t.Helper()
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "app.go")
	if err := os.WriteFile(file, []byte("package main\n\nfunc loadConfig() string { return \"ok\" }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	rec := newPhase10WorkflowRecorder(t, "grep_to_read_range")
	grepInput := GrepToolInput{Path: tempDir, Pattern: "loadConfig", PatternMode: "literal", OutputMode: "content", Glob: "*.go"}
	_, grepOutput, err := h.HandleGrepTool(context.Background(), nil, grepInput)
	if err != nil {
		t.Fatal(err)
	}
	rec.addCall("grep", grepInput, grepOutput)
	if len(grepOutput.FileGroups) == 0 || len(grepOutput.FileGroups[0].ReadRanges) == 0 {
		t.Fatalf("grep should expose file_groups read_ranges: %#v", grepOutput)
	}
	readRange := grepOutput.FileGroups[0].ReadRanges[0]
	readInput := ReadFileInput{TargetFile: grepOutput.FileGroups[0].Path, StartLine: &readRange.StartLine, EndLine: &readRange.EndLine}
	_, readOutput, err := h.HandleReadFile(context.Background(), nil, readInput)
	if err != nil {
		t.Fatal(err)
	}
	rec.addCall("read_file", readInput, readOutput)
	if !strings.Contains(readOutput.Text, "loadConfig") {
		t.Fatalf("derived grep read should include match: %#v", readOutput)
	}
	return rec.entry()
}

func phase10ChunkedReadContinuationWorkflow(t *testing.T) tokenLedgerEntry {
	t.Helper()
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "chunk.txt")
	if err := os.WriteFile(file, []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	rec := newPhase10WorkflowRecorder(t, "chunked_read_continuation")
	firstInput := ReadFileInput{TargetFile: file, ChunkLines: intPtr(1), CountTotalLines: true}
	_, first, err := h.HandleReadFile(context.Background(), nil, firstInput)
	if err != nil {
		t.Fatal(err)
	}
	rec.addCall("read_file", firstInput, first)
	if first.Continuation == nil || first.Continuation.NextRecommendedCall == nil {
		t.Fatalf("chunked read should expose continuation: %#v", first)
	}
	nextInput := readFileInputFromRecommendedMap(t, first.Continuation.NextRecommendedCall.RecommendedNextInput)
	_, second, err := h.HandleReadFile(context.Background(), nil, nextInput)
	if err != nil {
		t.Fatal(err)
	}
	rec.addCall("read_file", nextInput, second)
	if !strings.Contains(second.Text, "two") {
		t.Fatalf("derived continuation should read second line: %#v", second)
	}
	return rec.entry()
}

func phase10ResolveDryRunToReadValidationWorkflow(t *testing.T) tokenLedgerEntry {
	t.Helper()
	tempDir := t.TempDir()
	sourceFile := filepath.Join(tempDir, "source.go")
	targetFile := filepath.Join(tempDir, "target.go")
	if err := os.WriteFile(sourceFile, []byte("package main\n\nfunc loadConfig() string {\n\treturn \"ok\"\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	rec := newPhase10WorkflowRecorder(t, "resolve_dry_run_to_read_validation")
	outlineInput := OutlineFileInput{TargetFile: sourceFile}
	_, outline, err := h.HandleOutlineFile(context.Background(), nil, outlineInput)
	if err != nil {
		t.Fatal(err)
	}
	rec.addCall("outline_file", outlineInput, outline)
	item := findOutlineItemByName(outline.Symbols, "loadConfig")
	if item == nil || outline.Fingerprint == nil || item.SymbolRef == "" {
		t.Fatalf("outline should expose write source symbol: %#v", outline)
	}
	resolveInput := ResolveSymbolRangeInput{
		SourceFile:        sourceFile,
		SourceFingerprint: *outline.Fingerprint,
		Selector:          SymbolSelectorQuery{SymbolRef: item.SymbolRef},
		TargetIntent: &WriteTargetIntent{
			Operation:  operationCopy,
			TargetFile: targetFile,
			Placement:  TargetPlacement{Mode: placementCreateNew},
			DryRun:     true,
		},
	}
	_, resolved, err := h.HandleResolveSymbolRange(context.Background(), nil, resolveInput)
	if err != nil {
		t.Fatal(err)
	}
	rec.addCall("resolve_symbol_range", resolveInput, resolved)
	if resolved.RecommendedWriteCall == nil {
		t.Fatalf("resolve should expose recommended write call: %#v", resolved)
	}
	copyInput := copyRangesInputFromRecommendedMap(t, resolved.RecommendedWriteCall.RecommendedNextInput)
	_, copyOutput, err := h.HandleCopyRanges(context.Background(), nil, copyInput)
	if err != nil {
		t.Fatal(err)
	}
	rec.addCall("copy_ranges", copyInput, copyOutput)
	if copyOutput.Validation.Status == "" || copyOutput.SourceFingerprintForNextWrite == nil {
		t.Fatalf("dry-run should expose validation and next write fingerprint: %#v", copyOutput)
	}
	assertDryRunValidationHintCarriesExactProof(t, copyOutput)
	rec.previewCaveatObserved = phase10OutputHasPreviewCaveat(resolved) || phase10OutputHasPreviewCaveat(copyOutput)
	validationInput := readFileInputFromDryRunOutput(t, RangeTransferOutput(copyOutput))
	_, validationRead, err := h.HandleReadFile(context.Background(), nil, validationInput)
	if err != nil {
		t.Fatal(err)
	}
	rec.addCall("read_file", validationInput, validationRead)
	if !strings.Contains(validationRead.Text, "loadConfig") {
		t.Fatalf("derived validation read should inspect source range: %#v", validationRead)
	}
	return rec.entry()
}

func assertDryRunValidationHintCarriesExactProof(t *testing.T, output CopyRangesOutput) {
	t.Helper()
	if output.Validation.NextRecommendedCall == nil {
		t.Fatalf("dry-run validation should expose read_file verification hint: %#v", output.Validation)
	}
	rawExpectedVersion, ok := output.Validation.NextRecommendedCall.RecommendedNextInput["expected_version"]
	if !ok {
		t.Fatalf("dry-run validation hint should carry expected_version: %#v", output.Validation.NextRecommendedCall)
	}
	encoded := phase10RawJSONBytes(t, rawExpectedVersion)
	var proof ReadCoverageProof
	if err := json.Unmarshal(encoded, &proof); err != nil {
		t.Fatalf("expected_version should decode as ReadCoverageProof: %v; raw=%s", err, encoded)
	}
	if proof.ProofStrength != "exact" || proof.SHA256 == "" || len(output.RequestedRanges) == 0 || proof.Range != output.RequestedRanges[0] {
		t.Fatalf("dry-run validation expected_version should be exact range proof: proof=%#v output=%#v", proof, output)
	}
}

func phase10BatchDryRunWorkflow(t *testing.T) tokenLedgerEntry {
	t.Helper()
	tempDir := t.TempDir()
	sourceFile := filepath.Join(tempDir, "source.txt")
	targetA := filepath.Join(tempDir, "a.txt")
	targetB := filepath.Join(tempDir, "b.txt")
	if err := os.WriteFile(sourceFile, []byte("alpha\nbeta\ngamma\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	rec := newPhase10WorkflowRecorder(t, "batch_dry_run")
	_, outline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: sourceFile, OutputProfile: outlineProfileFingerprintOnly})
	if err != nil {
		t.Fatal(err)
	}
	if outline.Fingerprint == nil {
		t.Fatalf("fingerprint outline failed: %#v", outline)
	}
	input := CopyRangesBatchInput{
		SourceFile:        sourceFile,
		SourceFingerprint: *outline.Fingerprint,
		Targets: []BatchRangeTarget{
			{TargetFile: targetA, TargetPrecondition: TargetPrecondition{MustNotExist: true}, Placement: TargetPlacement{Mode: placementCreateNew}, Ranges: []SourceLineRange{{StartLine: 1, EndLine: 1}}},
			{TargetFile: targetB, TargetPrecondition: TargetPrecondition{MustNotExist: true}, Placement: TargetPlacement{Mode: placementCreateNew}, Ranges: []SourceLineRange{{StartLine: 2, EndLine: 2}}},
		},
		DryRun: true,
	}
	_, output, err := h.HandleCopyRangesBatch(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	rec.addCall("copy_ranges_batch", input, output)
	if len(output.TargetResults) != 2 || output.TargetResults[0].Validation.Status == "" || output.TargetResults[0].JoinerEffect.Normalized == "" {
		t.Fatalf("batch dry-run should expose per-target validation and joiner diagnostics: %#v", output)
	}
	return rec.entry()
}

func phase10ReadFilesContinuationWorkflow(t *testing.T) tokenLedgerEntry {
	t.Helper()
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(file, []byte("first\nsecond\nthird\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	rec := newPhase10WorkflowRecorder(t, "read_files_continuation")
	input := ReadFilesInput{Items: []ReadFileInputItem{{TargetFile: file}}, MaxTotalBytes: intPtr(len("1|first\n") + 1)}
	_, output, err := h.HandleReadFiles(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	rec.addCall("read_files", input, output)
	if output.Continuation == nil || output.Continuation.NextRecommendedCall == nil {
		t.Fatalf("read_files should expose continuation: %#v", output)
	}
	nextInput := readFilesInputFromRecommendedMap(t, output.Continuation.NextRecommendedCall.RecommendedNextInput)
	_, nextOutput, err := h.HandleReadFiles(context.Background(), nil, nextInput)
	if err != nil {
		t.Fatal(err)
	}
	rec.addCall("read_files", nextInput, nextOutput)
	if len(nextOutput.Items) == 0 || !strings.Contains(nextOutput.Items[0].Text, "second") {
		t.Fatalf("derived read_files continuation should read next line: %#v", nextOutput)
	}
	return rec.entry()
}

func phase10StaleContinuationRefreshWorkflow(t *testing.T) tokenLedgerEntry {
	t.Helper()
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "stale.txt")
	if err := os.WriteFile(file, []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	rec := newPhase10WorkflowRecorder(t, "stale_continuation_refresh")
	initialInput := ReadFileInput{TargetFile: file, CountTotalLines: true}
	_, initial, err := h.HandleReadFile(context.Background(), nil, initialInput)
	if err != nil {
		t.Fatal(err)
	}
	rec.addCall("read_file", initialInput, initial)
	if initial.Coverage == nil || initial.Coverage.Proof == nil {
		t.Fatalf("initial read should expose proof: %#v", initial)
	}
	if err := os.WriteFile(file, []byte("new\n"), 0644); err != nil {
		t.Fatal(err)
	}
	staleInput := ReadFileInput{TargetFile: file, ExpectedVersion: initial.Coverage.Proof, CountTotalLines: true}
	staleResult, staleOutput, err := h.HandleReadFile(context.Background(), nil, staleInput)
	if err != nil {
		t.Fatal(err)
	}
	rec.addCall("read_file", staleInput, staleOutput)
	if !staleResult.IsError || staleOutput.ErrorCode != "continuation_stale" {
		t.Fatalf("stale read should be rejected explicitly: result=%#v output=%#v", staleResult, staleOutput)
	}
	return rec.entry()
}

func phase10StaleResolveRefreshWorkflow(t *testing.T) tokenLedgerEntry {
	t.Helper()
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "app.go")
	if err := os.WriteFile(file, []byte("package main\n\nfunc loadConfig() string { return \"old\" }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	rec := newPhase10WorkflowRecorder(t, "stale_resolve_refresh")
	outlineInput := OutlineFileInput{TargetFile: file}
	_, outline, err := h.HandleOutlineFile(context.Background(), nil, outlineInput)
	if err != nil {
		t.Fatal(err)
	}
	rec.addCall("outline_file", outlineInput, outline)
	item := findOutlineItemByName(outline.Symbols, "loadConfig")
	if item == nil || item.SymbolRef == "" || outline.Fingerprint == nil {
		t.Fatalf("outline should expose symbol_ref and fingerprint: %#v", outline)
	}
	if err := os.WriteFile(file, []byte("package main\n\nfunc loadConfig() string { return \"new\" }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	resolveInput := ResolveSymbolRangeInput{SourceFile: file, SourceFingerprint: *outline.Fingerprint, Selector: SymbolSelectorQuery{SymbolRef: item.SymbolRef}}
	result, output, err := h.HandleResolveSymbolRange(context.Background(), nil, resolveInput)
	if err != nil {
		t.Fatal(err)
	}
	rec.addCall("resolve_symbol_range", resolveInput, output)
	if !result.IsError || output.ErrorCode != "source_fingerprint_stale" && output.ErrorCode != "source_file_changed" && output.ErrorCode != "fingerprint_mismatch" && output.ErrorCode != "symbol_fingerprint_mismatch" {
		t.Fatalf("stale resolve should expose fingerprint error: result=%#v output=%#v", result, output)
	}
	return rec.entry()
}

func phase10OrdinaryNonStaleFailureWorkflow(t *testing.T) tokenLedgerEntry {
	t.Helper()
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "app.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	rec := newPhase10WorkflowRecorder(t, "ordinary_non_stale_failure")
	input := OutlineFileInput{TargetFile: file, OutputProfile: "verbose"}
	result, output, err := h.HandleOutlineFile(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	rec.addCall("outline_file", input, output)
	if !result.IsError || output.ErrorCode == "" || output.NextRecommendedCall == nil {
		t.Fatalf("ordinary non-stale failure should expose structured repair hint: result=%#v output=%#v", result, output)
	}
	return rec.entry()
}

func phase10NoResultDiscoveryWorkflow(t *testing.T) tokenLedgerEntry {
	t.Helper()
	tempDir := t.TempDir()
	h := NewHandler()
	rec := newPhase10WorkflowRecorder(t, "no_result_discovery")
	input := GlobFileSearchInput{TargetDirectory: tempDir, GlobPattern: "*.go", Limit: intPtr(5)}
	_, output, err := h.HandleGlobFileSearch(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	rec.addCall("glob_file_search", input, output)
	if output.Count != 0 || output.Message == "" {
		t.Fatalf("no-result discovery should keep actionable message: %#v", output)
	}
	return rec.entry()
}

func phase10TruncatedDiscoveryContinuationWorkflow(t *testing.T) tokenLedgerEntry {
	t.Helper()
	tempDir := t.TempDir()
	for _, name := range []string{"a.go", "b.go", "c.go"} {
		if err := os.WriteFile(filepath.Join(tempDir, name), []byte("package main\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	h := NewHandler()
	rec := newPhase10WorkflowRecorder(t, "truncated_discovery_continuation")
	input := GlobFileSearchInput{TargetDirectory: tempDir, GlobPattern: "*.go", Limit: intPtr(1), Sort: "path_asc"}
	_, output, err := h.HandleGlobFileSearch(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	rec.addCall("glob_file_search", input, output)
	if output.Continuation == nil || output.Continuation.NextRecommendedCall == nil {
		t.Fatalf("truncated glob should expose continuation: %#v", output)
	}
	nextInput := globInputFromRecommendedMap(t, output.Continuation.NextRecommendedCall.RecommendedNextInput)
	_, nextOutput, err := h.HandleGlobFileSearch(context.Background(), nil, nextInput)
	if err != nil {
		t.Fatal(err)
	}
	rec.addCall("glob_file_search", nextInput, nextOutput)
	if len(nextOutput.Files) == 0 || nextOutput.Files[0].Path == output.Files[0].Path {
		t.Fatalf("derived glob continuation should return next page: first=%#v next=%#v", output, nextOutput)
	}
	return rec.entry()
}

type phase10WorkflowRecorder struct {
	t                     *testing.T
	name                  string
	toolCalls             int
	components            tokenLedgerComponentBytes
	rawBytes              int
	previewCaveatObserved bool
}

func newPhase10WorkflowRecorder(t *testing.T, name string) *phase10WorkflowRecorder {
	t.Helper()
	return &phase10WorkflowRecorder{
		t:    t,
		name: name,
		components: tokenLedgerComponentBytes{
			ToolMetadata: phase10ToolMetadataBytes(t),
		},
	}
}

func (r *phase10WorkflowRecorder) addCall(tool string, input, output any) {
	r.t.Helper()
	r.toolCalls++
	req := phase10NormalizedJSONBytes(r.t, input)
	resp := phase10NormalizedJSONBytes(r.t, output)
	r.components.RequestJSON += len(req)
	r.components.ResponseMetadata += len(resp)
	r.components.ResponseContent += phase10JSONPathBytes(r.t, output, phase10ResponseContentPath)
	r.components.NextCallHints += phase10JSONPathBytes(r.t, output, phase10HintPath)
	r.components.ErrorGuidance += phase10JSONPathBytes(r.t, output, phase10ErrorGuidancePath)
	r.rawBytes += len(phase10RawJSONBytes(r.t, input)) + len(phase10RawJSONBytes(r.t, output))
	_ = tool
}

func (r *phase10WorkflowRecorder) entry() tokenLedgerEntry {
	total := r.components.ToolMetadata + r.components.RequestJSON + r.components.ResponseMetadata
	return tokenLedgerEntry{
		WorkflowName:          r.name,
		ToolCalls:             r.toolCalls,
		TotalNormalizedBytes:  total,
		RawSerializedBytes:    r.rawBytes + r.components.ToolMetadata,
		EstimatedTokens:       (total + 3) / 4,
		Components:            r.components,
		PreviewCaveatObserved: r.previewCaveatObserved,
	}
}

func phase10ToolMetadataBytes(t *testing.T) int {
	t.Helper()
	schemas := phase10RegisteredToolMetadataSchemas(t, true)
	return len(phase10NormalizedJSONBytes(t, schemas))
}

func phase10FullRegisteredToolMetadataBytes(t *testing.T) int {
	t.Helper()
	return len(phase10NormalizedJSONBytes(t, phase10RegisteredToolMetadataSchemas(t, false)))
}

type phase10ToolMetadataSchema struct {
	Name   string             `json:"name"`
	Input  *jsonschema.Schema `json:"input_schema"`
	Output *jsonschema.Schema `json:"output_schema"`
}

func phase10RegisteredToolMetadataSchemas(t *testing.T, compactOutput bool) []phase10ToolMetadataSchema {
	t.Helper()
	schemas := []phase10ToolMetadataSchema{
		phase10ToolMetadataSchemaFor[SetCwdInput](t, "set_cwd", SetCwdOutputSchema(), compactOutput),
		phase10ToolMetadataSchemaFor[ReadFileInput](t, "read_file", schemaForTest[ReadFileOutput](t), compactOutput),
		phase10ToolMetadataSchemaFor[ReadFilesInput](t, "read_files", schemaForTest[ReadFilesOutput](t), compactOutput),
		phase10ToolMetadataSchemaFor[OutlineFileInput](t, "outline_file", OutlineFileOutputSchema(), compactOutput),
		phase10ToolMetadataSchemaFor[ResolveSymbolRangeInput](t, "resolve_symbol_range", schemaForTest[ResolveSymbolRangeOutput](t), compactOutput),
		phase10ToolMetadataSchemaFor[CopyRangesInput](t, "copy_ranges", schemaForTest[CopyRangesOutput](t), compactOutput),
		phase10ToolMetadataSchemaFor[MoveRangesInput](t, "move_ranges", schemaForTest[MoveRangesOutput](t), compactOutput),
		phase10ToolMetadataSchemaFor[CopyRangesBatchInput](t, "copy_ranges_batch", schemaForTest[CopyRangesBatchOutput](t), compactOutput),
		phase10ToolMetadataSchemaFor[MoveRangesBatchInput](t, "move_ranges_batch", schemaForTest[MoveRangesBatchOutput](t), compactOutput),
		phase10ToolMetadataSchemaFor[ListDirInput](t, "list_dir", schemaForTest[ListDirOutput](t), compactOutput),
		phase10ToolMetadataSchemaFor[GlobFileSearchInput](t, "glob_file_search", schemaForTest[GlobFileSearchOutput](t), compactOutput),
		phase10ToolMetadataSchemaFor[GrepToolInput](t, "grep", schemaForTest[GrepOutput](t), compactOutput),
		phase10ToolMetadataSchemaFor[InspectPathInput](t, "inspect_path", schemaForTest[InspectPathOutput](t), compactOutput),
		phase10ToolMetadataSchemaFor[WorkspaceInventoryInput](t, "workspace_inventory", WorkspaceInventoryOutputSchema(), compactOutput),
	}
	if len(schemas) != 14 {
		t.Fatalf("registered metadata schema count = %d, want 14", len(schemas))
	}
	return schemas
}

func phase10ToolMetadataSchemaFor[In any](t *testing.T, name string, output *jsonschema.Schema, compactOutput bool) phase10ToolMetadataSchema {
	t.Helper()
	input := schemaForTest[In](t)
	ApplyToolInputSchemaConstraints(input, name)
	if compactOutput {
		output = ApplyToolOutputSchemaConstraints(output, name)
	} else {
		ApplyPathOutputSchemaConstraints(output)
	}
	return phase10ToolMetadataSchema{Name: name, Input: input, Output: output}
}

func phase10CurrentOutputSchemaForTool(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	for _, schema := range phase10RegisteredToolMetadataSchemas(t, true) {
		if schema.Name == name {
			return schema.Output
		}
	}
	t.Fatalf("missing output schema for %s", name)
	return nil
}

func phase10AssertSchemaValid(t *testing.T, toolName string, schema *jsonschema.Schema, output any) {
	t.Helper()
	schema = schema.CloneSchemas()
	schema.Schema = "https://json-schema.org/draft/2020-12/schema"
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("%s output schema should resolve: %v", toolName, err)
	}
	encoded := phase10RawJSONBytes(t, output)
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("%s output should decode as JSON: %v", toolName, err)
	}
	if err := resolved.Validate(decoded); err != nil {
		t.Fatalf("%s runtime output should validate against advertised compact schema: %v\nschema=%s\noutput=%s", toolName, err, string(phase10RawJSONBytes(t, schema)), string(encoded))
	}
}

func boolPtrForPhase10Test(v bool) *bool {
	return &v
}

func phase10NormalizedJSONBytes(t *testing.T, value any) []byte {
	t.Helper()
	encoded := phase10RawJSONBytes(t, value)
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	normalized := phase10NormalizeJSONValueForLedger(decoded, "")
	normalizedBytes, err := json.Marshal(normalized)
	if err != nil {
		t.Fatal(err)
	}
	return normalizedBytes
}

func phase10ComparableLedgerEntry(entry tokenLedgerEntry) tokenLedgerEntry {
	entry.RawSerializedBytes = 0
	return entry
}

var phase10DrivePathPattern = regexp.MustCompile(`[A-Za-z]:[/\\][^\s"']+`)

func phase10NormalizeJSONValueForLedger(value any, key string) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for childKey, childValue := range v {
			out[childKey] = phase10NormalizeJSONValueForLedger(childValue, childKey)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = phase10NormalizeJSONValueForLedger(item, key)
		}
		return out
	case string:
		switch {
		case key == "sha256":
			return "<sha256>"
		case key == "modified_at":
			return "<modified_at>"
		case key == "symbol_ref":
			return "<symbol_ref>"
		case key == "disambiguator":
			return "<id>"
		case strings.Contains(key, "file") || strings.Contains(key, "path") || strings.Contains(key, "directory") || key == "cwd":
			return "<path>"
		default:
			return phase10DrivePathPattern.ReplaceAllString(v, "<path>")
		}
	case float64:
		switch key {
		case "modified_unix_nano":
			return float64(1)
		case "cwd_id":
			return float64(1)
		default:
			return v
		}
	default:
		return v
	}
}

func phase10RawJSONBytes(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func phase10JSONPathBytes(t *testing.T, value any, keep func(string) bool) int {
	t.Helper()
	encoded := phase10NormalizedJSONBytes(t, value)
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	filtered := phase10FilterJSONPaths(decoded, "", keep)
	if filtered == nil {
		return 0
	}
	out, err := json.Marshal(filtered)
	if err != nil {
		t.Fatal(err)
	}
	return len(out)
}

func phase10FilterJSONPaths(value any, path string, keep func(string) bool) any {
	if keep(path) {
		return value
	}
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, child := range v {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			if filtered := phase10FilterJSONPaths(child, childPath, keep); filtered != nil {
				out[key] = filtered
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, child := range v {
			childPath := path + "[]"
			if filtered := phase10FilterJSONPaths(child, childPath, keep); filtered != nil {
				out = append(out, filtered)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}

func phase10ResponseContentPath(path string) bool {
	return strings.HasSuffix(path, ".text") ||
		strings.Contains(path, "matches[].text") ||
		strings.Contains(path, "items[].text") ||
		strings.Contains(path, "diff_previews[].text") ||
		strings.Contains(path, "read_back[].text") ||
		strings.Contains(path, "boundary_preview")
}

func phase10HintPath(path string) bool {
	return strings.Contains(path, "next_recommended_call") ||
		strings.Contains(path, "next_recommended_calls") ||
		strings.Contains(path, "recommended_write_call") ||
		strings.Contains(path, "preview_write_call") ||
		strings.Contains(path, "action_hint")
}

func phase10ErrorGuidancePath(path string) bool {
	return path == "error" || path == "error_code" || path == "message" || path == "reason" ||
		strings.HasSuffix(path, ".error") || strings.HasSuffix(path, ".error_code") ||
		strings.HasSuffix(path, ".message") || strings.HasSuffix(path, ".reason") ||
		strings.Contains(path, "refusal_reason")
}

func readFileInputFromRecommendedMap(t *testing.T, input map[string]any) ReadFileInput {
	t.Helper()
	encoded := phase10RawJSONBytes(t, input)
	var out ReadFileInput
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func readFilesInputFromRecommendedMap(t *testing.T, input map[string]any) ReadFilesInput {
	t.Helper()
	encoded := phase10RawJSONBytes(t, input)
	var out ReadFilesInput
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func globInputFromRecommendedMap(t *testing.T, input map[string]any) GlobFileSearchInput {
	t.Helper()
	encoded := phase10RawJSONBytes(t, input)
	var out GlobFileSearchInput
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func copyRangesInputFromRecommendedMap(t *testing.T, input map[string]any) CopyRangesInput {
	t.Helper()
	encoded := phase10RawJSONBytes(t, input)
	var out CopyRangesInput
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func readFileInputFromDryRunOutput(t *testing.T, output RangeTransferOutput) ReadFileInput {
	t.Helper()
	if output.Validation.NextRecommendedCall != nil {
		return readFileInputFromRecommendedMap(t, output.Validation.NextRecommendedCall.RecommendedNextInput)
	}
	if output.SourceFile == "" || len(output.RequestedRanges) == 0 {
		t.Fatalf("dry-run output lacks validation read fields: %#v", output)
	}
	r := output.RequestedRanges[0]
	return ReadFileInput{TargetFile: output.SourceFile, StartLine: &r.StartLine, EndLine: &r.EndLine}
}

func phase10OutputHasPreviewCaveat(value any) bool {
	encoded, err := json.Marshal(value)
	if err != nil {
		return false
	}
	text := strings.ToLower(string(encoded))
	return strings.Contains(text, "bounded") && (strings.Contains(text, "read-back") || strings.Contains(text, "read_file"))
}

func prettyJSONForTokenBudgetTest(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestPhase10PrintMeasuredLedgerForFixtureUpdate(t *testing.T) {
	if os.Getenv("MCP_FILE_TOOLS_PRINT_PHASE10_LEDGER") == "" {
		t.Skip("set MCP_FILE_TOOLS_PRINT_PHASE10_LEDGER=1 to print measured ledger")
	}
	fmt.Printf("full_registered_tool_metadata: %d\n", phase10FullRegisteredToolMetadataBytes(t))
	fmt.Printf("compact_registered_tool_metadata: %d\n", phase10ToolMetadataBytes(t))
	for _, entry := range phase10TokenLedgerEntries(t) {
		fmt.Printf("%q: %#v,\n", entry.WorkflowName, entry)
	}
}
