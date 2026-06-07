package handler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/config"
)

func TestCopyRangesBatchCreatesMultipleTargets(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "concept.md")
	targetA := filepath.Join(tempDir, "part-a.md")
	targetB := filepath.Join(tempDir, "part-b.md")
	if err := os.WriteFile(source, []byte("# A\nalpha\n# B\nbeta\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)

	result, output, err := h.HandleCopyRangesBatch(context.Background(), nil, CopyRangesBatchInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Targets: []BatchRangeTarget{
			{
				TargetFile:         targetA,
				TargetPrecondition: TargetPrecondition{MustNotExist: true},
				Placement:          TargetPlacement{Mode: placementCreateNew},
				Ranges:             []SourceLineRange{{StartLine: 1, EndLine: 2}},
			},
			{
				TargetFile:         targetB,
				TargetPrecondition: TargetPrecondition{MustNotExist: true},
				Placement:          TargetPlacement{Mode: placementCreateNew},
				Ranges:             []SourceLineRange{{StartLine: 3, EndLine: 4}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !output.Applied || len(output.TargetResults) != 2 || len(output.TargetsWritten) != 2 {
		t.Fatalf("unexpected batch copy output: result=%#v output=%#v", result, output)
	}
	if output.Operation != "copy_batch" {
		t.Fatalf("copy_ranges_batch operation = %q, want copy_batch", output.Operation)
	}
	totalTargetBytes := int64(len("# A\nalpha\n") + len("# B\nbeta\n"))
	if output.BytesWrittenTargetBytes != totalTargetBytes || output.BytesRewrittenSourceBytes != 0 || output.BytesWrittenTotalBytes != totalTargetBytes || output.BytesWritten != totalTargetBytes {
		t.Fatalf("copy batch byte metrics should be target-only with total alias: %#v", output)
	}
	for _, targetResult := range output.TargetResults {
		if targetResult.BytesWritten == 0 || targetResult.TargetFingerprintForNextWrite == nil || targetResult.TargetFingerprintAfter == nil {
			t.Fatalf("batch target result should expose per-target write deltas and next fingerprint: %#v", targetResult)
		}
		if targetResult.Validation.Status != "applied_and_verified" || len(targetResult.Validation.TargetReadBack) == 0 {
			t.Fatalf("batch target result should include applied read-back validation: %#v", targetResult.Validation)
		}
	}
	assertFileContent(t, targetA, "# A\nalpha\n")
	assertFileContent(t, targetB, "# B\nbeta\n")
	assertFileContent(t, source, "# A\nalpha\n# B\nbeta\n")
}

func TestCopyRangesBatchPreservesSourceJoinerDiagnostics(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	target := filepath.Join(tempDir, "target.txt")
	if err := os.WriteFile(source, []byte("one\n\ntwo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)

	result, output, err := h.HandleCopyRangesBatch(context.Background(), nil, CopyRangesBatchInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Targets: []BatchRangeTarget{{
			TargetFile:         target,
			TargetPrecondition: TargetPrecondition{MustNotExist: true},
			Placement:          TargetPlacement{Mode: placementCreateNew},
			Ranges: []SourceLineRange{
				{StartLine: 1, EndLine: 1},
				{StartLine: 3, EndLine: 3},
			},
			Joiner: "blank_line",
		}},
		DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(output.TargetResults) != 1 {
		t.Fatalf("copy_ranges_batch should plan one target: result=%#v output=%#v", result, output)
	}
	targetResult := output.TargetResults[0]
	if len(targetResult.JoinerEffect.SourceBoundaries) != 1 {
		t.Fatalf("batch target should preserve source joiner diagnostics: %#v", targetResult.JoinerEffect)
	}
	boundary := targetResult.JoinerEffect.SourceBoundaries[0]
	if boundary.VisualBlankLinesBetween != 2 || !containsTestString(boundary.WarningCodes, "blank_line_joiner_extra_visual_blank_lines") {
		t.Fatalf("batch source boundary should explain extra visual blank lines: %#v", boundary)
	}
	if !containsBoundaryWarningCode(targetResult.BoundaryWarnings, "blank_line_joiner_extra_visual_blank_lines") {
		t.Fatalf("batch target should expose joiner warning through boundary_warnings: %#v", targetResult.BoundaryWarnings)
	}
}

func TestMoveRangesBatchWritesTargetsAndRemovesSourceOnce(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "concept.md")
	targetA := filepath.Join(tempDir, "part-a.md")
	targetB := filepath.Join(tempDir, "part-b.md")
	if err := os.WriteFile(source, []byte("# A\nalpha\n# B\nbeta\n# C\ngamma\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)

	result, output, err := h.HandleMoveRangesBatch(context.Background(), nil, MoveRangesBatchInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Targets: []BatchRangeTarget{
			{
				TargetFile:         targetA,
				TargetPrecondition: TargetPrecondition{MustNotExist: true},
				Placement:          TargetPlacement{Mode: placementCreateNew},
				Ranges:             []SourceLineRange{{StartLine: 1, EndLine: 2}},
			},
			{
				TargetFile:         targetB,
				TargetPrecondition: TargetPrecondition{MustNotExist: true},
				Placement:          TargetPlacement{Mode: placementCreateNew},
				Ranges:             []SourceLineRange{{StartLine: 3, EndLine: 4}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !output.Applied || output.RemovedSourceLines != 4 {
		t.Fatalf("unexpected batch move output: result=%#v output=%#v", result, output)
	}
	if output.Operation != "move_batch" {
		t.Fatalf("move_ranges_batch operation = %q, want move_batch", output.Operation)
	}
	for _, targetResult := range output.TargetResults {
		if targetResult.Validation.Status != "applied_and_verified" || len(targetResult.Validation.TargetReadBack) == 0 {
			t.Fatalf("batch move target should include applied read-back validation: %#v", targetResult.Validation)
		}
	}
	if output.SourceValidation == nil || output.SourceValidation.Status != "applied_and_verified" || len(output.SourceValidation.SourceReadBack) == 0 {
		t.Fatalf("batch move source should include applied read-back validation: %#v", output.SourceValidation)
	}
	targetBytes := int64(len("# A\nalpha\n") + len("# B\nbeta\n"))
	sourceRewriteBytes := int64(len("# C\ngamma\n"))
	if output.BytesWrittenTargetBytes != targetBytes || output.BytesRewrittenSourceBytes != sourceRewriteBytes || output.BytesWrittenTotalBytes != targetBytes+sourceRewriteBytes || output.BytesWritten != targetBytes+sourceRewriteBytes {
		t.Fatalf("move batch byte metrics should split target and source rewrite bytes: %#v", output)
	}
	assertFileContent(t, targetA, "# A\nalpha\n")
	assertFileContent(t, targetB, "# B\nbeta\n")
	assertFileContent(t, source, "# C\ngamma\n")
}

func TestMoveRangesBatchPlansSourceAfterRewriteBytes(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "concept.md")
	target := filepath.Join(tempDir, "part.md")
	if err := os.WriteFile(source, []byte("# A\nalpha\n# B\nbeta\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Load()
	cfg.BatchMaxPlannedBytes = 100
	h := NewHandler(WithConfig(cfg))
	sourceFP := outlineFingerprintForTest(t, h, source)

	result, output, err := h.HandleMoveRangesBatch(context.Background(), nil, MoveRangesBatchInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Targets: []BatchRangeTarget{
			{
				TargetFile:         target,
				TargetPrecondition: TargetPrecondition{MustNotExist: true},
				Placement:          TargetPlacement{Mode: placementCreateNew},
				Ranges:             []SourceLineRange{{StartLine: 1, EndLine: 2}},
			},
		},
		DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	targetBytes := int64(len("# A\nalpha\n"))
	sourceRewriteBytes := int64(len("# B\nbeta\n"))
	if result.IsError || output.Applied {
		t.Fatalf("move batch dry-run should plan without applying: result=%#v output=%#v", result, output)
	}
	if output.WouldWriteTargetBytes != targetBytes || output.WouldRewriteSourceBytes != sourceRewriteBytes || output.WouldWriteTotalBytes != targetBytes+sourceRewriteBytes || output.WouldWriteBytes != output.WouldWriteTotalBytes {
		t.Fatalf("move batch planned byte metrics should expose target/source/total aliases: %#v", output)
	}
	assertFileContent(t, source, "# A\nalpha\n# B\nbeta\n")
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create target, stat err=%v", err)
	}
}

func TestPlanBatchTargetDoesNotMaterializePayload(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	target := filepath.Join(tempDir, "target.txt")
	if err := os.WriteFile(source, []byte("one\ntwo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)
	sourceInfo, err := h.prepareBatchTransferSource(context.Background(), source, source, sourceFP, &BatchRangeTransferOutput{})
	if err != nil {
		t.Fatal(err)
	}
	sourceScan, err := scanLineSpans(context.Background(), source, []SourceLineRange{{StartLine: 1, EndLine: 2}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	targetPlan, err := h.planBatchTarget(context.Background(), source, sourceFP, source, source, sourceInfo, resolvedBatchTarget{
		target: BatchRangeTarget{
			TargetFile:         target,
			TargetPrecondition: TargetPrecondition{MustNotExist: true},
			Placement:          TargetPlacement{Mode: placementCreateNew},
			Ranges:             []SourceLineRange{{StartLine: 1, EndLine: 2}},
		},
		resolved: target,
		display:  target,
	}, sourceScan, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(targetPlan.plan.payload) != 0 || targetPlan.plan.payloadSize != int64(len("one\ntwo\n")) || targetPlan.output.WouldWriteBytes != int64(len("one\ntwo\n")) {
		t.Fatalf("batch planning should keep only payload metadata before aggregate limit: %#v", targetPlan.plan)
	}
}

func TestWriteBatchTargetPlanRechecksSourceAfterMaterializeBeforeWrite(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	target := filepath.Join(tempDir, "target.txt")
	if err := os.WriteFile(source, []byte("one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)
	batchOutput := BatchRangeTransferOutput{
		Operation:      operationOutputName(operationCopy, true),
		SourceFile:     source,
		TargetResults:  []BatchTargetResult{},
		TargetsWritten: []string{},
		BackupPaths:    []string{},
		BackupResults:  []BackupResult{},
	}
	sourceInfo, err := h.prepareBatchTransferSource(context.Background(), source, source, sourceFP, &batchOutput)
	if err != nil {
		t.Fatal(err)
	}
	sourceScan, err := scanLineSpans(context.Background(), source, []SourceLineRange{{StartLine: 1, EndLine: 1}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	targetPlan, err := h.planBatchTarget(context.Background(), source, sourceFP, source, source, sourceInfo, resolvedBatchTarget{
		target: BatchRangeTarget{
			TargetFile:         target,
			TargetPrecondition: TargetPrecondition{MustNotExist: true},
			Placement:          TargetPlacement{Mode: placementCreateNew},
			Ranges:             []SourceLineRange{{StartLine: 1, EndLine: 1}},
		},
		resolved: target,
		display:  target,
	}, sourceScan, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("two\n"), 0644); err != nil {
		t.Fatal(err)
	}

	singleOutput, err := h.writeBatchTargetPlan(context.Background(), PathContext{}, targetPlan, source, sourceFP, &batchOutput)
	if err == nil || errorCodeFromMessage(err.Error()) != "source_fingerprint_mismatch" {
		t.Fatalf("expected final source recheck mismatch before target write, err=%v output=%#v batch=%#v", err, singleOutput, batchOutput)
	}
	if batchOutput.CurrentSourceFingerprint == nil || batchOutput.SourceFingerprintForNextWrite != nil {
		t.Fatalf("batch output should expose current source fingerprint and omit next source fingerprint: %#v", batchOutput)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("target should not be created after final source mismatch, stat err=%v", statErr)
	}
}

func TestApplyBatchSourceMoveRejectsOverThresholdBeforeFingerprint(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	if err := os.WriteFile(source, []byte("small\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Load()
	cfg.WriteThreshold = 6
	h := NewHandler(WithConfig(cfg))
	sourceFP := outlineFingerprintForTest(t, h, source)
	if err := os.WriteFile(source, []byte("too-large\n"), 0644); err != nil {
		t.Fatal(err)
	}
	output := BatchRangeTransferOutput{
		Operation:      operationOutputName(operationMove, true),
		SourceFile:     source,
		TargetResults:  []BatchTargetResult{},
		TargetsWritten: []string{"target.txt"},
		BackupPaths:    []string{},
		BackupResults:  []BackupResult{},
	}

	err := h.applyBatchSourceMove(context.Background(), PathContext{}, source, sourceFP, source, BackupSpec{}, []SourceLineRange{{StartLine: 1, EndLine: 1}}, nil, &output)
	if err == nil || !strings.Contains(err.Error(), "MCP_WRITE_THRESHOLD") {
		t.Fatalf("expected threshold rejection before batch source move fingerprint work, err=%v output=%#v", err, output)
	}
	if output.CurrentSourceFingerprint != nil {
		t.Fatalf("threshold rejection should not fingerprint oversized source before source move: %#v", output.CurrentSourceFingerprint)
	}
}

func TestCopyRangesBatchLimitExceededBeforeMutation(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	targetA := filepath.Join(tempDir, "a.txt")
	targetB := filepath.Join(tempDir, "b.txt")
	if err := os.WriteFile(source, []byte("one\ntwo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Load()
	cfg.BatchMaxTargets = 1
	h := NewHandler(WithConfig(cfg))
	sourceFP := outlineFingerprintForTest(t, h, source)

	result, output, err := h.HandleCopyRangesBatch(context.Background(), nil, CopyRangesBatchInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Targets: []BatchRangeTarget{
			{TargetFile: targetA, TargetPrecondition: TargetPrecondition{MustNotExist: true}, Placement: TargetPlacement{Mode: placementCreateNew}, Ranges: []SourceLineRange{{StartLine: 1, EndLine: 1}}},
			{TargetFile: targetB, TargetPrecondition: TargetPrecondition{MustNotExist: true}, Placement: TargetPlacement{Mode: placementCreateNew}, Ranges: []SourceLineRange{{StartLine: 2, EndLine: 2}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || output.ErrorCode != "batch_limit_exceeded" {
		t.Fatalf("expected batch limit structured error, got result=%#v output=%#v", result, output)
	}
	if _, err := os.Stat(targetA); !os.IsNotExist(err) {
		t.Fatalf("limit failure should not create first target, stat err=%v", err)
	}
	if _, err := os.Stat(targetB); !os.IsNotExist(err) {
		t.Fatalf("limit failure should not create second target, stat err=%v", err)
	}
}

func TestCopyRangesBatchEarlyErrorHasActionHint(t *testing.T) {
	h := NewHandler()

	result, output, err := h.HandleCopyRangesBatch(context.Background(), nil, CopyRangesBatchInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || output.ActionHint == nil || output.ActionHint.Reason == "" {
		t.Fatalf("batch early errors should expose fallback action_hint: result=%#v output=%#v", result, output)
	}
}

func TestCopyRangesBatchAllowsOverlapWithWarning(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	target := filepath.Join(tempDir, "target.txt")
	if err := os.WriteFile(source, []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)

	result, output, err := h.HandleCopyRangesBatch(context.Background(), nil, CopyRangesBatchInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Targets: []BatchRangeTarget{
			{
				TargetFile:         target,
				TargetPrecondition: TargetPrecondition{MustNotExist: true},
				Placement:          TargetPlacement{Mode: placementCreateNew},
				Ranges: []SourceLineRange{
					{StartLine: 1, EndLine: 2},
					{StartLine: 2, EndLine: 3},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !output.Applied || len(output.BatchWarnings) == 0 || output.WarningSummary == nil {
		t.Fatalf("expected copy overlap warning and successful output: result=%#v output=%#v", result, output)
	}
	assertFileContent(t, target, "one\ntwo\ntwo\nthree\n")
}

func TestCopyRangesBatchWarningsAreTruncated(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	target := filepath.Join(tempDir, "target.txt")
	if err := os.WriteFile(source, []byte("one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ranges := make([]SourceLineRange, 0, 60)
	for i := 0; i < 60; i++ {
		ranges = append(ranges, SourceLineRange{StartLine: 1, EndLine: 1})
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)

	result, output, err := h.HandleCopyRangesBatch(context.Background(), nil, CopyRangesBatchInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Targets: []BatchRangeTarget{
			{
				TargetFile:         target,
				TargetPrecondition: TargetPrecondition{MustNotExist: true},
				Placement:          TargetPlacement{Mode: placementCreateNew},
				Ranges:             ranges,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !output.Applied || !output.WarningsTruncated {
		t.Fatalf("expected successful truncated batch warnings: result=%#v output=%#v", result, output)
	}
	if len(output.BatchWarnings) != batchWarningLimit+1 {
		t.Fatalf("expected capped warnings plus summary, got %d warnings=%#v", len(output.BatchWarnings), output.BatchWarnings)
	}
	if output.BatchWarnings[len(output.BatchWarnings)-1].Code != "batch_warnings_truncated" {
		t.Fatalf("last warning should summarize truncation: %#v", output.BatchWarnings)
	}
	if output.WarningSummary == nil || output.WarningSummary.TotalWarnings != 59 || output.WarningSummary.ByCode["copy_batch_duplicate_source_range"] != 59 {
		t.Fatalf("warning_summary should include omitted warnings by code: %#v", output.WarningSummary)
	}
}

func TestWarningSummaryPreservesOmittedCountsAfterRebuild(t *testing.T) {
	output := BatchRangeTransferOutput{
		BatchWarnings: []ToolWarning{
			{Code: "copy_batch_duplicate_source_range"},
			{Code: "batch_warnings_truncated"},
		},
		Warnings: []ToolWarning{
			{Code: "source_changed_after_target_writes"},
		},
		OmittedWarningCounts: map[string]int{
			"copy_batch_duplicate_source_range": 9,
		},
	}
	output.WarningSummary = warningSummary(output.BatchWarnings, output.Warnings)
	addOmittedWarningCounts(output.WarningSummary, output.OmittedWarningCounts)
	if output.WarningSummary.TotalWarnings != 11 || output.WarningSummary.ByCode["copy_batch_duplicate_source_range"] != 10 || output.WarningSummary.ByCode["source_changed_after_target_writes"] != 1 {
		t.Fatalf("rebuilt warning summary should preserve omitted counts: %#v", output.WarningSummary)
	}
}

func TestCopyRangesBatchPreflightFailureIncludesTargetDiagnostics(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	target := filepath.Join(tempDir, "target.txt")
	if err := os.WriteFile(source, []byte("one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)
	targetFP := outlineFingerprintForTest(t, h, target)
	if err := os.WriteFile(target, []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}

	result, output, err := h.HandleCopyRangesBatch(context.Background(), nil, CopyRangesBatchInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Targets: []BatchRangeTarget{
			{
				TargetFile:         target,
				TargetPrecondition: TargetPrecondition{Fingerprint: &targetFP},
				Placement:          TargetPlacement{Mode: placementAppend},
				Ranges:             []SourceLineRange{{StartLine: 1, EndLine: 1}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || len(output.TargetResults) != 1 {
		t.Fatalf("expected batch preflight error with one target result: result=%#v output=%#v", result, output)
	}
	targetResult := output.TargetResults[0]
	if targetResult.Status != "failed" || targetResult.FailedAt != "validation" || !targetResult.Failed || targetResult.CurrentTargetFingerprint == nil || targetResult.ExpectedTargetFingerprint == nil || len(targetResult.RequestedRanges) != 1 {
		t.Fatalf("target diagnostics missing from preflight failure: %#v", targetResult)
	}
	assertFileContent(t, target, "changed\n")
}

func TestMoveRangesBatchStaleSourceRecoveryDoesNotMarkSourceModifiedByTool(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	if err := os.WriteFile(source, []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)
	if err := os.WriteFile(source, []byte("external\nchange\n"), 0644); err != nil {
		t.Fatal(err)
	}
	output := BatchRangeTransferOutput{
		Operation:               operationOutputName(operationMove, true),
		SourceFile:              source,
		SourceFingerprintBefore: &sourceFP,
		TargetResults:           []BatchTargetResult{},
		TargetsWritten:          []string{filepath.Join(tempDir, "target.txt")},
		WouldRemoveSourceRanges: []SourceLineRange{{StartLine: 1, EndLine: 1}},
		WouldRemoveSourceLines:  1,
		BackupPaths:             []string{},
		BackupResults:           []BackupResult{},
	}

	err := h.applyBatchSourceMove(context.Background(), PathContext{}, source, sourceFP, source, BackupSpec{}, output.WouldRemoveSourceRanges, nil, &output)
	if err == nil || !strings.Contains(err.Error(), "source_fingerprint_mismatch") {
		t.Fatalf("expected source fingerprint mismatch, err=%v output=%#v", err, output)
	}
	partial := batchPartialState(output, "write_source", err)
	if partial.SourceModifiedByTool || partial.SourceFingerprintAfter != nil || partial.CurrentSourceFingerprint == nil {
		t.Fatalf("stale external source should be current fingerprint, not tool after fingerprint: %#v", partial)
	}
	assertFileContent(t, source, "external\nchange\n")
}

func TestMoveRangesBatchStaleSourceWithBackupUsesCurrentFingerprint(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	if err := os.WriteFile(source, []byte("one\ntwo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)
	if err := os.WriteFile(source, []byte("external\n"), 0644); err != nil {
		t.Fatal(err)
	}
	output := BatchRangeTransferOutput{
		Operation:               operationOutputName(operationMove, true),
		SourceFile:              source,
		SourceFingerprintBefore: &sourceFP,
		TargetResults:           []BatchTargetResult{},
		WouldRemoveSourceRanges: []SourceLineRange{{StartLine: 1, EndLine: 1}},
		BackupPaths:             []string{},
		BackupResults:           []BackupResult{},
	}

	err := h.applyBatchSourceMove(context.Background(), PathContext{}, source, sourceFP, source, BackupSpec{Mode: backupModeSidecar}, output.WouldRemoveSourceRanges, nil, &output)
	if err == nil || !strings.Contains(err.Error(), "source_fingerprint_mismatch") {
		t.Fatalf("expected source fingerprint mismatch, err=%v output=%#v", err, output)
	}
	if output.SourceFingerprintAfter != nil || output.CurrentSourceFingerprint == nil {
		t.Fatalf("stale source with backup should report current fingerprint only: %#v", output)
	}
	assertFileContent(t, source, "external\n")
}

func TestBatchTargetFailureCarriesCurrentSourceFingerprint(t *testing.T) {
	before := FileFingerprint{SHA256: "before"}
	current := FileFingerprint{SHA256: "current"}
	output := BatchRangeTransferOutput{
		Operation:                     operationOutputName(operationMove, true),
		SourceFile:                    "source.txt",
		SourceFingerprintBefore:       &before,
		SourceFingerprintForNextWrite: &before,
		TargetResults:                 []BatchTargetResult{},
		TargetsWritten:                []string{"target-a.txt"},
		BackupPaths:                   []string{},
		BackupResults:                 []BackupResult{},
		CurrentSourceFingerprint:      nil,
	}
	singleOutput := RangeTransferOutput{
		SourceFingerprintBefore:         &before,
		SourceFingerprintCheckedAtWrite: &current,
		CurrentSourceFingerprint:        &current,
		SourceFingerprintForNextWrite:   &current,
	}
	err := sourceFingerprintMismatchError("source changed before target write")

	applySingleSourceDiagnosticsToBatch(&output, singleOutput)
	partial := batchPartialState(output, "write_targets", err)
	if partial.CurrentSourceFingerprint == nil || partial.CurrentSourceFingerprint.SHA256 != "current" || partial.ErrorCode != "source_fingerprint_mismatch" {
		t.Fatalf("batch partial_state should carry current source fingerprint: %#v", partial)
	}
	if partial.RecommendedNextInput == nil || partial.RecommendedNextInput["target_file"] != output.SourceFile || partial.RecommendedNextInput["output_profile"] != outlineProfileOutline {
		t.Fatalf("batch source mismatch should recommend refreshing source outline: %#v", partial.RecommendedNextInput)
	}
	if output.SourceFingerprintForNextWrite != nil {
		t.Fatalf("batch source mismatch must clear stale source_fingerprint_for_next_write: %#v", output.SourceFingerprintForNextWrite)
	}
}

func TestBatchSourceMismatchMarksCurrentTargetFailed(t *testing.T) {
	current := FileFingerprint{SHA256: "current"}
	output := BatchRangeTransferOutput{
		Operation:                     operationOutputName(operationCopy, true),
		SourceFile:                    "source.txt",
		TargetResults:                 []BatchTargetResult{{TargetFile: "written.txt", Status: "written", Written: true}},
		TargetsWritten:                []string{"written.txt"},
		BackupPaths:                   []string{},
		BackupResults:                 []BackupResult{},
		OmittedWarningCounts:          map[string]int{},
		CurrentSourceFingerprint:      &current,
		SourceFingerprintForNextWrite: nil,
	}
	targetPlan := batchTargetPlan{output: RangeTransferOutput{
		TargetFile:       "current-target.txt",
		RequestedRanges:  []SourceLineRange{{StartLine: 1, EndLine: 1}},
		Ranges:           []TransferRangeResult{},
		BoundaryWarnings: []BoundaryWarning{},
		Warnings:         []ToolWarning{},
		BackupPaths:      []string{},
		BackupResults:    []BackupResult{},
	}}
	err := sourceFingerprintMismatchError("source changed before target write")

	failed := batchTargetResultFromSingle(targetPlan.output, "failed", false, false, true, err)
	failed.FailedAt = "source_recheck_before_target"
	output.TargetResults = append(output.TargetResults, failed)
	output.TargetResults = append(output.TargetResults, skippedBatchTargetResult("later.txt"))
	partial := batchPartialState(output, "source_recheck_before_target", err)
	if len(partial.TargetResults) != 3 || !partial.TargetResults[1].Failed || partial.TargetResults[1].TargetFile != "current-target.txt" || partial.TargetResults[1].ErrorCode != "source_fingerprint_mismatch" || !partial.TargetResults[2].Skipped {
		t.Fatalf("source mismatch should fail current target and skip later targets: %#v", partial.TargetResults)
	}
}

func TestBatchPartialStateIncludesFailedTargetBackups(t *testing.T) {
	backup := BackupResult{
		File:       "target.txt",
		Role:       "target",
		Requested:  true,
		Created:    true,
		BackupPath: "target.txt.bak",
	}
	output := BatchRangeTransferOutput{
		Operation:      operationOutputName(operationCopy, true),
		SourceFile:     "source.txt",
		TargetResults:  []BatchTargetResult{},
		TargetsWritten: []string{},
		BackupPaths:    []string{},
		BackupResults:  []BackupResult{},
	}
	singleOutput := RangeTransferOutput{
		BackupPaths:   []string{backup.BackupPath},
		BackupResults: []BackupResult{backup},
	}
	err := fmt.Errorf("target_fingerprint_mismatch: target fingerprint changed after backup")

	carryBatchTargetSideEffects(&output, singleOutput)
	partial := batchPartialState(output, "write_targets", err)
	if len(partial.BackupPaths) != 1 || partial.BackupPaths[0] != backup.BackupPath || len(partial.BackupResults) != 1 || !partial.BackupResults[0].Created {
		t.Fatalf("batch partial_state should include backups created before target failure: %#v", partial)
	}
}

func TestBatchTargetMismatchRecoveryRecommendsFailedTarget(t *testing.T) {
	output := BatchRangeTransferOutput{
		Operation:      operationOutputName(operationCopy, true),
		SourceFile:     "source.txt",
		TargetResults:  []BatchTargetResult{{TargetFile: "target.txt", Status: "failed", Failed: true, ErrorCode: "target_fingerprint_mismatch"}},
		TargetsWritten: []string{},
		BackupPaths:    []string{},
		BackupResults:  []BackupResult{},
	}
	err := targetFingerprintMismatchError("target changed before target write")

	partial := batchPartialState(output, "write_targets", err)
	if partial.RecommendedNextInput == nil || partial.RecommendedNextInput["target_file"] != "target.txt" || partial.RecommendedNextInput["output_profile"] != outlineProfileFingerprintOnly {
		t.Fatalf("batch target mismatch should recommend failed target fingerprint refresh: %#v", partial.RecommendedNextInput)
	}
}

func TestBatchTargetMissingRecoveryUsesInspectPath(t *testing.T) {
	output := BatchRangeTransferOutput{
		Operation:      operationOutputName(operationCopy, true),
		SourceFile:     "source.txt",
		TargetResults:  []BatchTargetResult{{TargetFile: "missing.txt", Status: "failed", Failed: true, ErrorCode: "target_missing"}},
		TargetsWritten: []string{},
		BackupPaths:    []string{},
		BackupResults:  []BackupResult{},
	}
	err := fmt.Errorf("target_missing: placement append requires an existing target")

	partial := batchPartialState(output, "write_targets", err)
	if partial.RecommendedNextTool != "inspect_path" || partial.RecommendedNextInput == nil || partial.RecommendedNextInput["target_path"] != "missing.txt" {
		t.Fatalf("batch target_missing should inspect missing target path: tool=%q input=%#v", partial.RecommendedNextTool, partial.RecommendedNextInput)
	}
}

func TestBatchTargetPostWriteInspectFailureMarksWrittenTarget(t *testing.T) {
	output := BatchRangeTransferOutput{
		Operation:      operationOutputName(operationMove, true),
		SourceFile:     "source.txt",
		TargetResults:  []BatchTargetResult{},
		TargetsWritten: []string{},
		BackupPaths:    []string{},
		BackupResults:  []BackupResult{},
	}
	singleOutput := RangeTransferOutput{
		TargetFile:       "target.txt",
		BytesWritten:     12,
		BackupPaths:      []string{},
		BackupResults:    []BackupResult{},
		BoundaryWarnings: []BoundaryWarning{},
		Warnings:         []ToolWarning{},
	}
	err := targetPostWriteInspectError(fmt.Errorf("stat failed"))

	targetWritten := singleOutput.BytesWritten > 0 || errorCodeFromMessage(err.Error()) == "target_post_write_inspect_failed"
	output.BytesWritten += singleOutput.BytesWritten
	output.TargetResults = append(output.TargetResults, batchTargetResultFromSingle(singleOutput, "failed", targetWritten, false, true, err))
	partial := batchPartialState(output, "write_targets", err)

	if output.BytesWritten != 12 || len(partial.TargetResults) != 1 || !partial.TargetResults[0].Written || !partial.TargetResults[0].Failed {
		t.Fatalf("post-write inspect failure should expose modified target bytes/result: output=%#v partial=%#v", output, partial)
	}
	if partial.RecommendedNextInput == nil || partial.RecommendedNextInput["target_file"] != "target.txt" || partial.RecommendedNextInput["output_profile"] != outlineProfileFingerprintOnly {
		t.Fatalf("post-write target inspect failure should recommend target fingerprint refresh: %#v", partial.RecommendedNextInput)
	}
}

func TestBatchSourcePostWriteInspectFailureMarksSourceModified(t *testing.T) {
	output := BatchRangeTransferOutput{
		Operation:               operationOutputName(operationMove, true),
		SourceFile:              "source.txt",
		TargetResults:           []BatchTargetResult{},
		TargetsWritten:          []string{"target.txt"},
		BackupPaths:             []string{},
		BackupResults:           []BackupResult{},
		RemovedSourceLines:      3,
		RemovedSourceRanges:     []SourceLineRange{{StartLine: 1, EndLine: 3}},
		WouldRemoveSourceLines:  3,
		WouldRemoveSourceRanges: []SourceLineRange{{StartLine: 1, EndLine: 3}},
	}
	err := sourcePostWriteInspectError(fmt.Errorf("stat failed"))

	partial := batchPartialState(output, "write_source", err)
	if !partial.SourceModifiedByTool {
		t.Fatalf("source post-write inspect failure should mark source modified: %#v", partial)
	}
	if partial.RecommendedNextInput == nil || partial.RecommendedNextInput["target_file"] != "source.txt" || partial.RecommendedNextInput["output_profile"] != outlineProfileFingerprintOnly {
		t.Fatalf("source post-write inspect failure should recommend source fingerprint refresh: %#v", partial.RecommendedNextInput)
	}
}

func TestBatchSourceRangeOutOfBoundsHasSourceActionHint(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	target := filepath.Join(tempDir, "target.txt")
	if err := os.WriteFile(source, []byte("one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)

	result, output, err := h.HandleCopyRangesBatch(context.Background(), nil, CopyRangesBatchInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Targets: []BatchRangeTarget{
			{
				TargetFile:         target,
				TargetPrecondition: TargetPrecondition{MustNotExist: true},
				Placement:          TargetPlacement{Mode: placementCreateNew},
				Ranges:             []SourceLineRange{{StartLine: 3, EndLine: 3}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || output.ErrorCode != "range_out_of_bounds" || output.ActionHint == nil {
		t.Fatalf("expected source range out of bounds action hint: result=%#v output=%#v", result, output)
	}
	if output.ActionHint.RecommendedNextTool != "outline_file" || output.ActionHint.RecommendedNextInput["target_file"] != output.SourceFile {
		t.Fatalf("batch source range error should recommend source outline: %#v", output.ActionHint)
	}
}

func TestBatchTargetPlacementOutOfBoundsHasTargetActionHint(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	target := filepath.Join(tempDir, "target.txt")
	if err := os.WriteFile(source, []byte("one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)
	targetFP := outlineFingerprintForTest(t, h, target)

	result, output, err := h.HandleCopyRangesBatch(context.Background(), nil, CopyRangesBatchInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Targets: []BatchRangeTarget{
			{
				TargetFile:         target,
				TargetPrecondition: TargetPrecondition{Fingerprint: &targetFP},
				Placement:          TargetPlacement{Mode: placementInsertBeforeLine, Line: 99},
				Ranges:             []SourceLineRange{{StartLine: 1, EndLine: 1}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || output.ErrorCode != "range_out_of_bounds" || output.ActionHint == nil {
		t.Fatalf("expected target placement out of bounds action hint: result=%#v output=%#v", result, output)
	}
	if output.ActionHint.RecommendedNextTool != "outline_file" || output.ActionHint.RecommendedNextInput["target_file"] != filepath.ToSlash(target) {
		t.Fatalf("batch target placement error should recommend target outline: %#v", output.ActionHint)
	}
	if len(output.TargetResults) != 1 || output.TargetResults[0].ExpectedTargetFingerprint == nil || output.TargetResults[0].CurrentTargetFingerprint == nil {
		t.Fatalf("failed target placement result should include target fingerprint diagnostics: %#v", output.TargetResults)
	}
}

func TestPathLockKeyNormalizesWindowsCase(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows lock-key case folding is platform-specific")
	}
	a := pathLockKey(`D:\Repo\File.txt`)
	b := pathLockKey(`d:\repo\file.txt`)
	if a == "" || a != b {
		t.Fatalf("Windows path lock key should be case-insensitive: %q vs %q", a, b)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s content = %q, want %q", path, string(data), want)
	}
}
