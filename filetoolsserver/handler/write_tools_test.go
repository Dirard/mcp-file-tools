package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dimitar-grigorov/mcp-file-tools/internal/config"
)

func TestCopyRangesCreateNewAndDryRun(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	target := filepath.Join(tempDir, "target.txt")
	if err := os.WriteFile(source, []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)

	dryResult, dryOutput, err := h.HandleCopyRanges(context.Background(), nil, CopyRangesInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Ranges:            []SourceLineRange{{StartLine: 2, EndLine: 2}},
		TargetFile:        target,
		TargetPrecondition: TargetPrecondition{
			MustNotExist: true,
		},
		Placement: TargetPlacement{Mode: placementCreateNew},
		DryRun:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if dryResult.IsError || dryOutput.Applied || dryOutput.WouldWriteBytes == 0 {
		t.Fatalf("unexpected dry-run output: result=%#v output=%#v", dryResult, dryOutput)
	}
	if dryOutput.Operation != operationCopy {
		t.Fatalf("copy_ranges operation = %q, want %q", dryOutput.Operation, operationCopy)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create target, stat err=%v", err)
	}

	result, output, err := h.HandleCopyRanges(context.Background(), nil, CopyRangesInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Ranges:            []SourceLineRange{{StartLine: 2, EndLine: 2}},
		TargetFile:        target,
		TargetPrecondition: TargetPrecondition{
			MustNotExist: true,
		},
		Placement: TargetPlacement{Mode: placementCreateNew},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !output.Applied {
		t.Fatalf("copy_ranges returned error: result=%#v output=%#v", result, output)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "two\n" {
		t.Fatalf("target content mismatch: %q", string(data))
	}
	sourceData, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(sourceData) != "one\ntwo\nthree\n" {
		t.Fatalf("copy_ranges should not modify source: %q", string(sourceData))
	}
}

func TestMoveRangesCreateNewRemovesSourceRange(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	target := filepath.Join(tempDir, "moved.txt")
	if err := os.WriteFile(source, []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)

	result, output, err := h.HandleMoveRanges(context.Background(), nil, MoveRangesInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Ranges:            []SourceLineRange{{StartLine: 2, EndLine: 2}},
		TargetFile:        target,
		TargetPrecondition: TargetPrecondition{
			MustNotExist: true,
		},
		Placement: TargetPlacement{Mode: placementCreateNew},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !output.Applied || output.RemovedSourceLines != 1 {
		t.Fatalf("move_ranges returned unexpected output: result=%#v output=%#v", result, output)
	}
	if output.Operation != operationMove {
		t.Fatalf("move_ranges operation = %q, want %q", output.Operation, operationMove)
	}
	targetData, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(targetData) != "two\n" {
		t.Fatalf("target content mismatch: %q", string(targetData))
	}
	sourceData, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(sourceData) != "one\nthree\n" {
		t.Fatalf("source content mismatch after move: %q", string(sourceData))
	}
}

func TestMoveRangesRejectsFinalEmptyDisplayLine(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	target := filepath.Join(tempDir, "moved.txt")
	if err := os.WriteFile(source, []byte("one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)

	result, output, err := h.HandleMoveRanges(context.Background(), nil, MoveRangesInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Ranges:            []SourceLineRange{{StartLine: 2, EndLine: 2}},
		TargetFile:        target,
		TargetPrecondition: TargetPrecondition{
			MustNotExist: true,
		},
		Placement: TargetPlacement{Mode: placementCreateNew},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || output.ErrorCode != "zero_byte_range" || output.Applied || output.RemovedSourceLines != 0 {
		t.Fatalf("expected zero-byte range rejection, result=%#v output=%#v", result, output)
	}
	if output.ActionHint == nil || output.ActionHint.RecommendedNextTool != "outline_file" || output.ActionHint.RecommendedNextInput["target_file"] != filepath.ToSlash(source) {
		t.Fatalf("zero-byte range should recommend source outline refresh: %#v", output.ActionHint)
	}
	assertFileContent(t, source, "one\n")
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("zero-byte rejected move should not create target, stat err=%v", err)
	}
}

func TestCopyRangesAllowsUnorderedNonOverlappingRangesInRequestOrder(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	target := filepath.Join(tempDir, "target.txt")
	if err := os.WriteFile(source, []byte("one\ntwo\nthree\nfour\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)

	result, output, err := h.HandleCopyRanges(context.Background(), nil, CopyRangesInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Ranges: []SourceLineRange{
			{StartLine: 3, EndLine: 3},
			{StartLine: 1, EndLine: 1},
		},
		TargetFile: target,
		TargetPrecondition: TargetPrecondition{
			MustNotExist: true,
		},
		Placement: TargetPlacement{Mode: placementCreateNew},
		Joiner:    "single_newline",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !output.Applied {
		t.Fatalf("copy_ranges returned error for unordered non-overlapping ranges: result=%#v output=%#v", result, output)
	}
	if output.JoinerEffect.SourceRangeJoinCount != 1 || output.JoinerEffect.InsertedNewlinesBetweenRanges != 1 {
		t.Fatalf("joiner_effect should explain source range joins: %#v", output.JoinerEffect)
	}
	assertFileContent(t, target, "three\n\none\n")
}

func TestDiffPreviewShowsLateChangeWithinBudget(t *testing.T) {
	var oldText strings.Builder
	for i := 1; i <= 80; i++ {
		oldText.WriteString(fmt.Sprintf("line %02d\n", i))
	}
	newText := oldText.String() + "needle late append\n"

	preview := diffPreviewForBytes("target", "old.txt", "new.txt", []byte(oldText.String()), []byte(newText), 300, redactionAuto, false)
	if preview.Role != "target" || preview.Format != "unified" {
		t.Fatalf("diff preview should use public target role/unified format: %#v", preview)
	}
	if !strings.Contains(preview.Text, "+needle late append") {
		t.Fatalf("bounded diff should show the late changed line instead of only the file start:\n%s", preview.Text)
	}
	if strings.Contains(preview.Text, "-line 01") || preview.Stats.LinesAdded != 1 || preview.Stats.LinesRemoved != 0 || preview.Stats.HunksReturned != 1 {
		t.Fatalf("bounded diff stats/text should describe the real append, got stats=%#v text=\n%s", preview.Stats, preview.Text)
	}
}

func TestDiffPreviewRedactsBeforeBudgetTruncation(t *testing.T) {
	secret := "ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890abcdefghijklmnopqrstuvwxyz"
	preview := diffPreviewForBytes("target", "old.txt", "new.txt", []byte("safe\n"), []byte("api_key="+secret+"\n"), 50, redactionStrict, false)
	if !preview.Truncated || !preview.Redacted {
		t.Fatalf("diff preview should truncate only after redaction: %#v", preview)
	}
	if strings.Contains(preview.Text, secret[:20]) || strings.Contains(preview.Text, secret) {
		t.Fatalf("diff preview leaked a raw secret prefix across the budget boundary:\n%s", preview.Text)
	}
}

func TestBoundaryPreviewRedactsBeforeTruncation(t *testing.T) {
	secret := "ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890abcdefghijklmnopqrstuvwxyz"
	preview := boundaryPreviewForPlan(context.Background(), singleTransferPlan{
		sourceDisplay: "source.txt",
		targetDisplay: "target.txt",
		payload:       []byte("api_key=" + secret + "\n"),
	}, TargetPlacement{Mode: placementCreateNew}, redactionAuto, 12)
	if !preview.Truncated || !preview.Redacted {
		t.Fatalf("boundary preview should redact full text before truncation: %#v", preview)
	}
	encoded := preview.Before + "\n" + preview.Between + "\n" + preview.After
	if strings.Contains(encoded, secret[:8]) || strings.Contains(encoded, secret) {
		t.Fatalf("boundary preview leaked a raw secret prefix after truncation: %#v", preview)
	}
}

func TestDisplayTruncationPreservesUTF8(t *testing.T) {
	cases := []string{
		"Привет мир Привет мир",
		"cafe\u0301 cafe\u0301 cafe\u0301",
		"emoji 👍🏽 family 👨‍👩‍👧‍👦 end",
	}
	for _, text := range cases {
		prefix, prefixTruncated := truncateDisplayPrefix(text, 17, "...")
		if !prefixTruncated || !utf8.ValidString(prefix) || strings.Contains(prefix, "\ufffd") || strings.Contains(prefix, "ï¿½") {
			t.Fatalf("prefix truncation should produce clean UTF-8 for %q: %q", text, prefix)
		}
		suffix, suffixTruncated := truncateDisplaySuffix(text, 17, "...")
		if !suffixTruncated || !utf8.ValidString(suffix) || strings.Contains(suffix, "\ufffd") || strings.Contains(suffix, "ï¿½") {
			t.Fatalf("suffix truncation should produce clean UTF-8 for %q: %q", text, suffix)
		}
	}
}

func TestDiffAndBoundaryPreviewTruncateUnicodeWithoutReplacement(t *testing.T) {
	oldText := "строка один\n"
	newText := oldText + strings.Repeat("кириллица ", 20) + "\n"
	diff := diffPreviewForBytes("target", "old.md", "new.md", []byte(oldText), []byte(newText), 80, redactionOff, false)
	if !diff.Truncated || !utf8.ValidString(diff.Text) || strings.Contains(diff.Text, "\ufffd") || strings.Contains(diff.Text, "ï¿½") {
		t.Fatalf("diff preview should truncate Unicode cleanly: %#v text=%q", diff, diff.Text)
	}

	boundary := boundaryPreviewForPlan(context.Background(), singleTransferPlan{
		sourceDisplay: "source.md",
		targetDisplay: "target.md",
		payload:       []byte(strings.Repeat("👍🏽", 20)),
	}, TargetPlacement{Mode: placementCreateNew}, redactionOff, 24)
	combined := boundary.Before + boundary.Between + boundary.After
	if !boundary.Truncated || !utf8.ValidString(combined) || strings.Contains(combined, "\ufffd") || strings.Contains(combined, "ï¿½") {
		t.Fatalf("boundary preview should truncate Unicode cleanly: %#v combined=%q", boundary, combined)
	}
}

func TestCreateSidecarBackupFailureHasStableCode(t *testing.T) {
	result, err := createSidecarBackup(filepath.Join(t.TempDir(), "missing.txt"), "target")
	if err == nil || errorCodeFromMessage(err.Error()) != "backup_creation_failed" {
		t.Fatalf("backup failure should return stable error code, err=%v result=%#v", err, result)
	}
	if result.ErrorCode != "backup_creation_failed" || result.Error == "" {
		t.Fatalf("backup result should expose stable error code: %#v", result)
	}
}

func TestBackupDiscoveryUsesHiddenSidecarGlob(t *testing.T) {
	tempDir := t.TempDir()
	backupPath := filepath.Join(tempDir, ".target.txt.20260605T120000Z.ab12cd34.1.bak")
	discovery := backupDiscoveryForResults(PathContext{}, []BackupResult{
		{Role: "target", Created: true, BackupPath: backupPath},
	})
	if discovery == nil || len(discovery.DiscoveryGroups) != 1 || discovery.NextRecommendedCall == nil {
		t.Fatalf("created backup should produce rediscovery hints: %#v", discovery)
	}
	group := discovery.DiscoveryGroups[0]
	if group.GlobPattern != ".*.bak" || !group.IncludeHidden || group.Directory != filepath.ToSlash(tempDir) {
		t.Fatalf("backup discovery should use hidden sidecar glob in the backup directory: %#v", group)
	}
	nextInput := discovery.NextRecommendedCall.RecommendedNextInput
	if nextInput["glob_pattern"] != ".*.bak" || nextInput["include_hidden"] != true || nextInput["target_directory"] != filepath.ToSlash(tempDir) {
		t.Fatalf("backup discovery next call should be ready for hidden glob rediscovery: %#v", nextInput)
	}
}

func TestSourceValidationDoesNotOverwriteFailedTargetValidation(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	if err := os.WriteFile(source, []byte("one\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	output := RangeTransferOutput{
		Validation: WriteValidation{
			Status:    "applied_validation_failed",
			ErrorCode: "post_write_validation_failed",
			Error:     "post_write_validation_failed: target read-back failed",
		},
		SourceFingerprintAfter: &FileFingerprint{LineCount: 1},
	}
	plan := singleTransferPlan{
		sourceResolved: source,
		sourceDisplay:  filepath.ToSlash(source),
	}

	h.applySourceValidation(context.Background(), &output, plan, redactionAuto)
	if output.Validation.Status != "applied_validation_failed" || output.Validation.ErrorCode != "post_write_validation_failed" {
		t.Fatalf("source validation must not downgrade failed target validation: %#v", output.Validation)
	}
	if len(output.Validation.SourceReadBack) != 1 {
		t.Fatalf("source read-back should still be attached without downgrading status: %#v", output.Validation)
	}
}

func TestCopyRangesRejectsStaleSourceFingerprint(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	target := filepath.Join(tempDir, "target.txt")
	if err := os.WriteFile(source, []byte("one\ntwo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)
	if err := os.WriteFile(source, []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}

	result, output, err := h.HandleCopyRanges(context.Background(), nil, CopyRangesInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Ranges:            []SourceLineRange{{StartLine: 1, EndLine: 1}},
		TargetFile:        target,
		TargetPrecondition: TargetPrecondition{
			MustNotExist: true,
		},
		Placement: TargetPlacement{Mode: placementCreateNew},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || output.ErrorCode != "source_fingerprint_mismatch" || output.ActionHint == nil {
		t.Fatalf("expected structured stale-source error, got result=%#v output=%#v", result, output)
	}
}

func TestRecheckSourceBeforeTargetRefreshesNextFingerprint(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	target := filepath.Join(tempDir, "target.txt")
	if err := os.WriteFile(source, []byte("same\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("target\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)
	targetFP := outlineFingerprintForTest(t, h, target)
	output := RangeTransferOutput{
		SourceFile:                    source,
		TargetFile:                    target,
		SourceFingerprintForNextWrite: &sourceFP,
	}
	newModTime := time.Unix(10, sourceFP.ModifiedUnixNano+1_000_000_000)
	if err := os.Chtimes(source, newModTime, newModTime); err != nil {
		t.Fatal(err)
	}

	err := h.recheckSourceBeforeTarget(context.Background(), CopyRangesInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		TargetFile:        target,
		TargetPrecondition: TargetPrecondition{
			Fingerprint: &targetFP,
		},
	}, &output)
	if err != nil {
		t.Fatal(err)
	}
	if output.SourceFingerprintForNextWrite == nil || output.SourceFingerprintForNextWrite.ModifiedUnixNano == sourceFP.ModifiedUnixNano {
		t.Fatalf("successful source recheck should refresh next fingerprint metadata: before=%#v output=%#v", sourceFP, output)
	}
}

func TestPrepareTransferSourceRejectsOverThresholdBeforeFingerprint(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "large.txt")
	if err := os.WriteFile(source, []byte("0123456789\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Load()
	cfg.WriteThreshold = 4
	h := NewHandler(WithConfig(cfg))

	_, err := h.prepareTransferSource(context.Background(), source, source, FileFingerprint{SHA256: "stale"})
	if err == nil || !strings.Contains(err.Error(), "MCP_WRITE_THRESHOLD") {
		t.Fatalf("expected threshold rejection before fingerprint work, err=%v", err)
	}
}

func TestRecheckSourceBeforeTargetRejectsOverThresholdBeforeFingerprint(t *testing.T) {
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
	output := RangeTransferOutput{SourceFile: source}

	err := h.recheckSourceBeforeTarget(context.Background(), CopyRangesInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
	}, &output)
	if err == nil || !strings.Contains(err.Error(), "MCP_WRITE_THRESHOLD") {
		t.Fatalf("expected threshold rejection before final source fingerprint work, err=%v output=%#v", err, output)
	}
	if output.CurrentSourceFingerprint != nil {
		t.Fatalf("threshold rejection should not fingerprint oversized source during final recheck: %#v", output.CurrentSourceFingerprint)
	}
}

func TestRecheckPlannedTargetRejectsOverThresholdBeforeFingerprint(t *testing.T) {
	tempDir := t.TempDir()
	target := filepath.Join(tempDir, "target.txt")
	if err := os.WriteFile(target, []byte("small\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Load()
	cfg.WriteThreshold = 6
	h := NewHandler(WithConfig(cfg))
	targetFP := outlineFingerprintForTest(t, h, target)
	if err := os.WriteFile(target, []byte("too-large\n"), 0644); err != nil {
		t.Fatal(err)
	}
	output := RangeTransferOutput{TargetFile: target}

	err := h.recheckPlannedTarget(context.Background(), CopyRangesInput{
		TargetFile: target,
		TargetPrecondition: TargetPrecondition{
			Fingerprint: &targetFP,
		},
	}, &output, "target changed before write")
	if err == nil || !strings.Contains(err.Error(), "MCP_WRITE_THRESHOLD") {
		t.Fatalf("expected threshold rejection before final target fingerprint work, err=%v output=%#v", err, output)
	}
	if output.CurrentTargetFingerprint != nil {
		t.Fatalf("threshold rejection should not fingerprint oversized target during final recheck: %#v", output.CurrentTargetFingerprint)
	}
}

func TestValidateUTF8FileRespectsCancelledContext(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	if err := os.WriteFile(source, []byte("text\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := validateUTF8File(ctx, source)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation from UTF-8 validation, err=%v", err)
	}
}

func TestCopyRangesBoundaryWarningOnAppendDryRun(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	target := filepath.Join(tempDir, "target.txt")
	if err := os.WriteFile(source, []byte("suffix"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("prefix"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)
	targetFP := outlineFingerprintForTest(t, h, target)

	result, output, err := h.HandleCopyRanges(context.Background(), nil, CopyRangesInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Ranges:            []SourceLineRange{{StartLine: 1, EndLine: 1}},
		TargetFile:        target,
		TargetPrecondition: TargetPrecondition{
			Fingerprint: &targetFP,
		},
		Placement: TargetPlacement{Mode: placementAppend},
		DryRun:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(output.BoundaryWarnings) != 1 || output.BoundaryWarnings[0].Boundary != "target_end_to_insert_start" {
		t.Fatalf("expected append boundary warning, result=%#v output=%#v", result, output)
	}
}

func TestCopyRangesBackupResultsUseDisplayPaths(t *testing.T) {
	tempDir := t.TempDir()
	actualRoot := filepath.Join(tempDir, "actual")
	displayRoot := filepath.Join(tempDir, "display")
	if err := os.MkdirAll(actualRoot, 0755); err != nil {
		t.Fatal(err)
	}
	actualSource := filepath.Join(actualRoot, "source.txt")
	actualTarget := filepath.Join(actualRoot, "target.txt")
	displaySource := filepath.Join(displayRoot, "source.txt")
	displayTarget := filepath.Join(displayRoot, "target.txt")
	if err := os.WriteFile(actualSource, []byte("new\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(actualTarget, []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Load()
	cfg.PathMaps = []config.PathMap{{Source: displayRoot, Target: actualRoot}}
	h := NewHandler(WithConfig(cfg))
	sourceFP := outlineFingerprintForTest(t, h, displaySource)
	targetFP := outlineFingerprintForTest(t, h, displayTarget)

	result, output, err := h.HandleCopyRanges(context.Background(), nil, CopyRangesInput{
		SourceFile:        displaySource,
		SourceFingerprint: sourceFP,
		Ranges:            []SourceLineRange{{StartLine: 1, EndLine: 1}},
		TargetFile:        displayTarget,
		TargetPrecondition: TargetPrecondition{
			Fingerprint: &targetFP,
		},
		Placement: TargetPlacement{Mode: placementAppend},
		Backup:    BackupSpec{Mode: backupModeSidecar},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(output.BackupResults) != 1 || len(output.BackupPaths) != 1 {
		t.Fatalf("expected successful backup output, result=%#v output=%#v", result, output)
	}
	backup := output.BackupResults[0]
	displayRootSlash := filepath.ToSlash(displayRoot)
	displayTargetSlash := filepath.ToSlash(displayTarget)
	actualRootSlash := filepath.ToSlash(actualRoot)
	if backup.File != displayTargetSlash || !strings.HasPrefix(backup.BackupPath, displayRootSlash) || !strings.HasPrefix(output.BackupPaths[0], displayRootSlash) {
		t.Fatalf("backup paths should use display path map, backup=%#v backup_paths=%#v", backup, output.BackupPaths)
	}
	if strings.Contains(backup.BackupPath, actualRootSlash) || strings.Contains(output.BackupPaths[0], actualRootSlash) {
		t.Fatalf("backup output leaked resolved path, backup=%#v backup_paths=%#v", backup, output.BackupPaths)
	}
}

func TestCopyRangesJoinerEnumAndInvalidJoiner(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	target := filepath.Join(tempDir, "joined.txt")
	if err := os.WriteFile(source, []byte("one\ntwo\nthree"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)

	result, output, err := h.HandleCopyRanges(context.Background(), nil, CopyRangesInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Ranges: []SourceLineRange{
			{StartLine: 1, EndLine: 1},
			{StartLine: 3, EndLine: 3},
		},
		TargetFile: target,
		TargetPrecondition: TargetPrecondition{
			MustNotExist: true,
		},
		Placement: TargetPlacement{Mode: placementCreateNew},
		Joiner:    "single_newline",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !output.Applied || output.Operation != operationCopy {
		t.Fatalf("copy_ranges with joiner failed: result=%#v output=%#v", result, output)
	}
	assertFileContent(t, target, "one\n\nthree")

	badTarget := filepath.Join(tempDir, "bad.txt")
	badResult, badOutput, err := h.HandleCopyRanges(context.Background(), nil, CopyRangesInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Ranges:            []SourceLineRange{{StartLine: 1, EndLine: 1}},
		TargetFile:        badTarget,
		TargetPrecondition: TargetPrecondition{
			MustNotExist: true,
		},
		Placement: TargetPlacement{Mode: placementCreateNew},
		Joiner:    "newline",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !badResult.IsError || !strings.Contains(badOutput.Error, "unsupported joiner") {
		t.Fatalf("expected invalid joiner error, result=%#v output=%#v", badResult, badOutput)
	}
	if _, err := os.Stat(badTarget); !os.IsNotExist(err) {
		t.Fatalf("invalid joiner should not create target, stat err=%v", err)
	}
}

func TestCopyRangesJoinerEffectReportsSourceBoundaryBlankLineNoise(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	target := filepath.Join(tempDir, "target.txt")
	if err := os.WriteFile(source, []byte("one\n\ntwo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)

	result, output, err := h.HandleCopyRanges(context.Background(), nil, CopyRangesInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Ranges: []SourceLineRange{
			{StartLine: 1, EndLine: 1},
			{StartLine: 3, EndLine: 3},
		},
		TargetFile: target,
		TargetPrecondition: TargetPrecondition{
			MustNotExist: true,
		},
		Placement: TargetPlacement{Mode: placementCreateNew},
		Joiner:    "blank_line",
		DryRun:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.JoinerEffect.NewlineBytes != "\\n\\n" || len(output.JoinerEffect.SourceBoundaries) != 1 {
		t.Fatalf("copy_ranges should report source joiner diagnostics: result=%#v output=%#v", result, output)
	}
	boundary := output.JoinerEffect.SourceBoundaries[0]
	if boundary.ExistingLeftNewlines != 1 || boundary.InsertedNewlines != 2 || boundary.VisualBlankLinesBetween != 2 || !containsTestString(boundary.WarningCodes, "blank_line_joiner_extra_visual_blank_lines") {
		t.Fatalf("source boundary should explain extra visual blank lines: %#v", boundary)
	}
	if !containsBoundaryWarningCode(output.BoundaryWarnings, "blank_line_joiner_extra_visual_blank_lines") {
		t.Fatalf("source joiner warning should also appear in boundary_warnings: %#v", output.BoundaryWarnings)
	}
}

func TestCopyRangesJoinerEffectReportsTargetBoundaryAcrossPlacements(t *testing.T) {
	tempDir := t.TempDir()
	h := NewHandler()
	for _, tt := range []struct {
		name         string
		source       string
		target       string
		placement    TargetPlacement
		wantBoundary string
		wantWarnings int
	}{
		{name: "append", source: "insert\n", target: "head\n\n\n", placement: TargetPlacement{Mode: placementAppend}, wantBoundary: "target_end_to_insert_start", wantWarnings: 1},
		{name: "prepend", source: "insert\n\n\n", target: "tail\n", placement: TargetPlacement{Mode: placementPrepend}, wantBoundary: "insert_end_to_target_start", wantWarnings: 1},
		{name: "insert", source: "insert\n\n\n", target: "head\n\n\ntail\n", placement: TargetPlacement{Mode: placementInsertBeforeLine, Line: 4}, wantBoundary: "target_before_insert_to_insert_start", wantWarnings: 2},
		{name: "replace", source: "insert\n\n\n", target: "head\n\n\nold\nend\n", placement: TargetPlacement{Mode: placementReplaceRange, Range: &SourceLineRange{StartLine: 4, EndLine: 4}}, wantBoundary: "target_before_replaced_range_to_insert_start", wantWarnings: 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := filepath.Join(tempDir, tt.name+"-source.txt")
			target := filepath.Join(tempDir, tt.name+"-target.txt")
			if err := os.WriteFile(source, []byte(tt.source), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte(tt.target), 0644); err != nil {
				t.Fatal(err)
			}
			sourceFP := outlineFingerprintForTest(t, h, source)
			targetFP := outlineFingerprintForTest(t, h, target)
			result, output, err := h.HandleCopyRanges(context.Background(), nil, CopyRangesInput{
				SourceFile:        source,
				SourceFingerprint: sourceFP,
				Ranges:            []SourceLineRange{{StartLine: 1, EndLine: countLinesForTest(tt.source)}},
				TargetFile:        target,
				TargetPrecondition: TargetPrecondition{
					Fingerprint: &targetFP,
				},
				Placement: tt.placement,
				Joiner:    "blank_line",
				DryRun:    true,
			})
			if err != nil {
				t.Fatal(err)
			}
			boundary := output.JoinerEffect.TargetBoundary
			if result.IsError || boundary == nil || boundary.Boundary != tt.wantBoundary {
				t.Fatalf("%s should report target boundary diagnostics: result=%#v output=%#v", tt.name, result, output)
			}
			if boundary.VisualBlankLinesBetween != 2 || !containsTestString(boundary.WarningCodes, "blank_line_joiner_extra_visual_blank_lines") {
				t.Fatalf("%s target boundary should explain extra visual blank lines: %#v", tt.name, boundary)
			}
			if len(output.JoinerEffect.TargetBoundaries) != tt.wantWarnings {
				t.Fatalf("%s should expose all target boundary diagnostics: %#v", tt.name, output.JoinerEffect.TargetBoundaries)
			}
			if countBoundaryWarningCode(output.BoundaryWarnings, "blank_line_joiner_extra_visual_blank_lines") != tt.wantWarnings {
				t.Fatalf("%s target warning should also appear in boundary_warnings: %#v", tt.name, output.BoundaryWarnings)
			}
		})
	}
}

func containsTestString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsBoundaryWarningCode(warnings []BoundaryWarning, want string) bool {
	return countBoundaryWarningCode(warnings, want) > 0
}

func countBoundaryWarningCode(warnings []BoundaryWarning, want string) int {
	count := 0
	for _, warning := range warnings {
		if warning.Code == want {
			count++
		}
	}
	return count
}

func countLinesForTest(text string) int {
	count := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		count++
	}
	return maxInt(1, count)
}

func TestCopyRangesRejectsInvalidBackupMode(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	target := filepath.Join(tempDir, "target.txt")
	if err := os.WriteFile(source, []byte("one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)

	result, output, err := h.HandleCopyRanges(context.Background(), nil, CopyRangesInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Ranges:            []SourceLineRange{{StartLine: 1, EndLine: 1}},
		TargetFile:        target,
		TargetPrecondition: TargetPrecondition{
			MustNotExist: true,
		},
		Placement: TargetPlacement{Mode: placementCreateNew},
		Backup:    BackupSpec{Mode: "maybe"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(output.Error, "unsupported backup.mode") {
		t.Fatalf("expected invalid backup mode error, result=%#v output=%#v", result, output)
	}
}

func TestCopyRangesRejectsPlacementFieldsOutsideMode(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	target := filepath.Join(tempDir, "target.txt")
	if err := os.WriteFile(source, []byte("one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)

	result, output, err := h.HandleCopyRanges(context.Background(), nil, CopyRangesInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Ranges:            []SourceLineRange{{StartLine: 1, EndLine: 1}},
		TargetFile:        target,
		TargetPrecondition: TargetPrecondition{
			MustNotExist: true,
		},
		Placement: TargetPlacement{Mode: placementCreateNew, Line: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || output.ErrorCode != "invalid_placement" || !strings.Contains(output.Error, "placement.line is only allowed") {
		t.Fatalf("expected strict placement validation error, result=%#v output=%#v", result, output)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("invalid placement should not create target, stat err=%v", err)
	}
}

func TestCopyRangesRejectsReplaceRangeWithoutRangeAsInvalidPlacement(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	target := filepath.Join(tempDir, "target.txt")
	if err := os.WriteFile(source, []byte("one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)

	result, output, err := h.HandleCopyRanges(context.Background(), nil, CopyRangesInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Ranges:            []SourceLineRange{{StartLine: 1, EndLine: 1}},
		TargetFile:        target,
		TargetPrecondition: TargetPrecondition{
			MustNotExist: true,
		},
		Placement: TargetPlacement{Mode: placementReplaceRange},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || output.ErrorCode != "invalid_placement" || !strings.Contains(output.Error, "replace_range placement requires range") {
		t.Fatalf("replace_range without range should expose stable invalid_placement: result=%#v output=%#v", result, output)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("invalid placement should not create target, stat err=%v", err)
	}
}

func TestCopyRangesRejectsContradictoryTargetPrecondition(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	target := filepath.Join(tempDir, "target.txt")
	if err := os.WriteFile(source, []byte("one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("target\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)
	targetFP := outlineFingerprintForTest(t, h, target)

	result, output, err := h.HandleCopyRanges(context.Background(), nil, CopyRangesInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Ranges:            []SourceLineRange{{StartLine: 1, EndLine: 1}},
		TargetFile:        target,
		TargetPrecondition: TargetPrecondition{
			Fingerprint:  &targetFP,
			MustNotExist: true,
		},
		Placement: TargetPlacement{Mode: placementAppend},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(output.Error, "must use either must_not_exist or fingerprint") {
		t.Fatalf("expected contradictory target_precondition error, result=%#v output=%#v", result, output)
	}
	assertFileContent(t, target, "target\n")
}

func TestCopyRangesPreservesExistingTargetPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX mode preservation consistently")
	}
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	target := filepath.Join(tempDir, "script.sh")
	if err := os.WriteFile(source, []byte("echo copied\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0750); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)
	targetFP := outlineFingerprintForTest(t, h, target)

	result, output, err := h.HandleCopyRanges(context.Background(), nil, CopyRangesInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Ranges:            []SourceLineRange{{StartLine: 1, EndLine: 1}},
		TargetFile:        target,
		TargetPrecondition: TargetPrecondition{
			Fingerprint: &targetFP,
		},
		Placement: TargetPlacement{Mode: placementAppend},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !output.Applied {
		t.Fatalf("copy_ranges failed: result=%#v output=%#v", result, output)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0750 {
		t.Fatalf("target mode changed after atomic replace: got %v want %v", got, os.FileMode(0750))
	}
}

func TestWriteFileReplacePreservesZeroPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX mode preservation consistently")
	}
	tempDir := t.TempDir()
	target := filepath.Join(tempDir, "locked.txt")
	if err := os.WriteFile(target, []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0000); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(target, 0600) }()

	if err := writeFileReplace(target, func(w io.Writer) error {
		_, err := w.Write([]byte("new\n"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(target, 0600) }()
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0000 {
		t.Fatalf("target mode changed after atomic replace: got %v want %v", got, os.FileMode(0000))
	}
}

func TestReadByteSpanRejectsShortRead(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "source.txt")
	if err := os.WriteFile(file, []byte("abc"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := readByteSpan(context.Background(), file, byteSpan{Start: 0, End: 5})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected short read error, got %v", err)
	}
}

func TestMoveRangesLateSourceMismatchPartialStateUsesRecoveryCode(t *testing.T) {
	before := FileFingerprint{SHA256: "before"}
	current := FileFingerprint{SHA256: "current"}
	targetAfter := FileFingerprint{SHA256: "target-after"}
	output := RangeTransferOutput{
		Operation:                     operationOutputName(operationMove, false),
		SourceFile:                    "source.txt",
		TargetFile:                    "target.txt",
		SourceFingerprintBefore:       &before,
		TargetFingerprintAfter:        &targetAfter,
		SourceFingerprintForNextWrite: &before,
		BackupPaths:                   []string{},
		Ranges:                        []TransferRangeResult{},
	}
	err := sourceFingerprintMismatchError("source changed after target write")

	markRangeTransferSourceMismatch(&output, current)
	partial := singlePartialState(output, "target_written_source_not_updated", true, err)
	if partial.Phase != "target_written_source_not_updated" || partial.ErrorCode != "source_fingerprint_mismatch" || partial.CurrentSourceFingerprint == nil || partial.TargetFingerprintAfter == nil || !partial.TargetWritten {
		t.Fatalf("late source mismatch partial_state should be actionable: %#v", partial)
	}
	if partial.RecommendedNextInput == nil || partial.RecommendedNextInput["target_file"] != output.SourceFile || partial.RecommendedNextInput["output_profile"] != outlineProfileOutline {
		t.Fatalf("late source mismatch should recommend refreshing source outline: %#v", partial.RecommendedNextInput)
	}
	if output.SourceFingerprintForNextWrite != nil {
		t.Fatalf("late source mismatch must not expose stale source_fingerprint_for_next_write: %#v", output.SourceFingerprintForNextWrite)
	}
}

func TestSingleRangeTransferRechecksTargetBeforeWrite(t *testing.T) {
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
	sourceResolved, sourceDisplay, err := h.resolveRefactorPath(PathContext{}, source, "source_file")
	if err != nil {
		t.Fatal(err)
	}
	targetResolved, targetDisplay, err := h.resolveRefactorPath(PathContext{}, target, "target_file")
	if err != nil {
		t.Fatal(err)
	}
	input := CopyRangesInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Ranges:            []SourceLineRange{{StartLine: 1, EndLine: 1}},
		TargetFile:        target,
		TargetPrecondition: TargetPrecondition{
			Fingerprint: &targetFP,
		},
		Placement: TargetPlacement{Mode: placementAppend},
	}
	plan, err := h.buildSingleTransferPlan(context.Background(), input, operationCopy, sourceResolved, sourceDisplay, targetResolved, targetDisplay)
	if err != nil {
		t.Fatal(err)
	}
	output := RangeTransferOutput{
		SourceFile:      sourceDisplay,
		TargetFile:      targetDisplay,
		TargetPlacement: input.Placement,
	}
	if plan.targetInfo == nil {
		t.Fatalf("test setup should plan an existing target write: %#v", plan)
	}
	if err := os.WriteFile(target, []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}

	err = h.recheckPlannedTarget(context.Background(), input, &output, "target changed before target write")
	if err == nil || errorCodeFromMessage(err.Error()) != "target_fingerprint_mismatch" {
		t.Fatalf("expected late target fingerprint mismatch, err=%v output=%#v", err, output)
	}
	if output.CurrentTargetFingerprint == nil || output.ExpectedTargetFingerprint == nil {
		t.Fatalf("target recheck should expose expected/current fingerprints: %#v", output)
	}
	partial := singlePartialState(output, "target_recheck_before_write", false, err)
	if partial.RecommendedNextInput == nil || partial.RecommendedNextInput["target_file"] != output.TargetFile || partial.RecommendedNextInput["output_profile"] != outlineProfileFingerprintOnly {
		t.Fatalf("target mismatch should recommend refreshing target fingerprint: %#v", partial.RecommendedNextInput)
	}
	assertFileContent(t, target, "changed\n")
}

func TestSingleRangeTransferTargetMismatchAfterBackupUsesSpecificCode(t *testing.T) {
	output := RangeTransferOutput{
		SourceFile: "source.txt",
		TargetFile: "target.txt",
	}
	err := targetFingerprintMismatchError("target fingerprint changed after backup")

	partial := singlePartialState(output, "target_recheck_after_backup", false, err)
	if partial.ErrorCode != "target_fingerprint_mismatch" || partial.RecommendedNextInput == nil || partial.RecommendedNextInput["target_file"] != output.TargetFile {
		t.Fatalf("target mismatch after backup should stay target-specific: %#v", partial)
	}
}

func TestTargetMissingRecoveryUsesInspectPath(t *testing.T) {
	hint := actionHintForTransferError("target_missing", "source.txt", "missing.txt")
	if hint.RecommendedNextTool != "inspect_path" || hint.RecommendedNextInput == nil || hint.RecommendedNextInput["target_path"] != "missing.txt" {
		t.Fatalf("target_missing action hint should inspect missing path: %#v", hint)
	}

	output := RangeTransferOutput{SourceFile: "source.txt", TargetFile: "missing.txt"}
	partial := singlePartialState(output, "write_target", false, fmt.Errorf("target_missing: placement append requires an existing target"))
	if partial.RecommendedNextTool != "inspect_path" || partial.RecommendedNextInput == nil || partial.RecommendedNextInput["target_path"] != "missing.txt" {
		t.Fatalf("target_missing partial recovery should inspect missing target path: tool=%q input=%#v", partial.RecommendedNextTool, partial.RecommendedNextInput)
	}
}

func TestTargetPlacementOutOfBoundsRecoveryUsesTargetOutline(t *testing.T) {
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

	result, output, err := h.HandleCopyRanges(context.Background(), nil, CopyRangesInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Ranges:            []SourceLineRange{{StartLine: 1, EndLine: 1}},
		TargetFile:        target,
		TargetPrecondition: TargetPrecondition{
			Fingerprint: &targetFP,
		},
		Placement: TargetPlacement{Mode: placementInsertBeforeLine, Line: 99},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || output.ErrorCode != "range_out_of_bounds" || output.ActionHint == nil {
		t.Fatalf("expected target placement range error with action hint: result=%#v output=%#v", result, output)
	}
	if output.ActionHint.RecommendedNextTool != "outline_file" || output.ActionHint.RecommendedNextInput["target_file"] != output.TargetFile || output.ActionHint.RecommendedNextInput["output_profile"] != outlineProfileFingerprintOnly {
		t.Fatalf("target placement error should recommend target outline: %#v", output.ActionHint)
	}
}

func TestLateCreateNewFileExistsMapsToTargetExists(t *testing.T) {
	tempDir := t.TempDir()
	target := filepath.Join(tempDir, "target.txt")
	if err := os.WriteFile(target, []byte("already there\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	err := h.writeTargetForPlan(context.Background(), singleTransferPlan{
		targetResolved: target,
		payload:        []byte("new\n"),
	}, TargetPlacement{Mode: placementCreateNew})
	if err == nil || errorCodeFromMessage(err.Error()) != "target_exists" {
		t.Fatalf("late create_new file exists should map to target_exists, err=%v", err)
	}
	assertFileContent(t, target, "already there\n")
}

func TestRangeTransferErrorCodesAreSpecific(t *testing.T) {
	cases := []struct {
		message string
		want    string
	}{
		{message: "ranges must be non-overlapping", want: "overlapping_ranges"},
		{message: "source_file and target_file must not refer to the same file", want: "same_file_operation_unsupported"},
		{message: "binary files are not supported", want: "binary_file_rejected"},
		{message: "unsupported encoding: utf-16", want: "unsupported_encoding"},
		{message: "only UTF-8/ASCII text writes are supported", want: "unsupported_encoding"},
		{message: "parent_directory_missing: cannot access target parent directory", want: "parent_directory_missing"},
	}
	for _, tt := range cases {
		t.Run(tt.want, func(t *testing.T) {
			if got := errorCodeFromMessage(tt.message); got != tt.want {
				t.Fatalf("errorCodeFromMessage(%q) = %q, want %q", tt.message, got, tt.want)
			}
		})
	}
}

func TestCopyRangesMissingParentReturnsSpecificErrorCode(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	target := filepath.Join(tempDir, "missing", "target.txt")
	if err := os.WriteFile(source, []byte("one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)

	result, output, err := h.HandleCopyRanges(context.Background(), nil, CopyRangesInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Ranges:            []SourceLineRange{{StartLine: 1, EndLine: 1}},
		TargetFile:        target,
		TargetPrecondition: TargetPrecondition{
			MustNotExist: true,
		},
		Placement: TargetPlacement{Mode: placementCreateNew},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || output.ErrorCode != "parent_directory_missing" {
		t.Fatalf("expected parent_directory_missing error, result=%#v output=%#v", result, output)
	}
}

func TestCopyRangesInvalidUTF8TargetReturnsSpecificErrorCode(t *testing.T) {
	tempDir := t.TempDir()
	source := filepath.Join(tempDir, "source.txt")
	target := filepath.Join(tempDir, "target.txt")
	if err := os.WriteFile(source, []byte("one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte{0xff, 0xfe, 0xfd}, 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	sourceFP := outlineFingerprintForTest(t, h, source)
	targetFP := FileFingerprint{SHA256: "placeholder"}

	result, output, err := h.HandleCopyRanges(context.Background(), nil, CopyRangesInput{
		SourceFile:        source,
		SourceFingerprint: sourceFP,
		Ranges:            []SourceLineRange{{StartLine: 1, EndLine: 1}},
		TargetFile:        target,
		TargetPrecondition: TargetPrecondition{
			Fingerprint: &targetFP,
		},
		Placement: TargetPlacement{Mode: placementAppend},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || output.ErrorCode != "unsupported_encoding" {
		t.Fatalf("expected unsupported_encoding error, result=%#v output=%#v", result, output)
	}
}

func outlineFingerprintForTest(t *testing.T, h *Handler, path string) FileFingerprint {
	t.Helper()
	result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{
		TargetFile:    path,
		OutputProfile: outlineProfileFingerprintOnly,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.Fingerprint == nil {
		t.Fatalf("outline_file fingerprint failed: result=%#v output=%#v", result, output)
	}
	if strings.TrimSpace(output.Fingerprint.SHA256) == "" {
		t.Fatalf("empty fingerprint: %#v", output.Fingerprint)
	}
	return *output.Fingerprint
}
