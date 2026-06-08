package handler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	operationCopy = "copy"
	operationMove = "move"

	placementCreateNew        = "create_new"
	placementAppend           = "append"
	placementPrepend          = "prepend"
	placementInsertBeforeLine = "insert_before_line"
	placementReplaceRange     = "replace_range"

	backupModeSidecar = "sidecar"
)

type singleTransferPlan struct {
	sourceResolved  string
	sourceDisplay   string
	targetResolved  string
	targetDisplay   string
	sourceInfo      fileTextInfo
	targetInfo      *fileTextInfo
	sourceScan      lineScanResult
	joiner          []byte
	joinerEffect    JoinerEffect
	payload         []byte
	payloadSize     int64
	ranges          []TransferRangeResult
	wouldWriteBytes int64
	boundaryPreview BoundaryPreview
}

type singleTransferOptions struct {
	acquireLock        bool
	allowSourceOverlap bool
}

func (h *Handler) executeSingleRangeTransfer(ctx context.Context, pathCtx PathContext, input CopyRangesInput, operation string) (RangeTransferOutput, error) {
	return h.executeSingleRangeTransferWithLock(ctx, pathCtx, input, operation, true)
}

func (h *Handler) executeSingleRangeTransferWithLock(ctx context.Context, pathCtx PathContext, input CopyRangesInput, operation string, acquireLock bool) (RangeTransferOutput, error) {
	return h.executeSingleRangeTransferWithOptions(ctx, pathCtx, input, operation, singleTransferOptions{acquireLock: acquireLock})
}

func (h *Handler) executeSingleRangeTransferWithOptions(ctx context.Context, pathCtx PathContext, input CopyRangesInput, operation string, options singleTransferOptions) (RangeTransferOutput, error) {
	output := RangeTransferOutput{
		Operation:        operationOutputName(operation, false),
		DryRun:           input.DryRun,
		RequestedRanges:  append([]SourceLineRange(nil), input.Ranges...),
		Ranges:           []TransferRangeResult{},
		BoundaryWarnings: []BoundaryWarning{},
		Warnings:         []ToolWarning{},
		BackupPaths:      []string{},
		BackupResults:    []BackupResult{},
		TargetPlacement:  input.Placement,
	}
	if err := validateSourceRanges(input.Ranges, options.allowSourceOverlap); err != nil {
		return output, err
	}
	if err := validateTargetPrecondition(input.TargetPrecondition); err != nil {
		return output, err
	}
	if err := validatePlacementShape(input.Placement); err != nil {
		return output, err
	}
	sourceResolved, sourceDisplay, err := h.resolveRefactorPath(pathCtx, input.SourceFile, "source_file")
	if err != nil {
		return output, err
	}
	targetResolved, targetDisplay, err := h.resolveRefactorPath(pathCtx, input.TargetFile, "target_file")
	if err != nil {
		return output, err
	}
	internalInput := input
	internalInput.SourceFile = sourceResolved
	internalInput.TargetFile = targetResolved
	output.SourceFile = sourceDisplay
	output.TargetFile = targetDisplay
	if err := rejectSymlinkPath(sourceResolved, true); err != nil {
		return output, err
	}
	if err := rejectSymlinkPath(targetResolved, true); err != nil {
		return output, err
	}
	same, err := sameFileOrPath(sourceResolved, targetResolved)
	if err != nil {
		return output, err
	}
	if same {
		return output, fmt.Errorf("source_file and target_file must not refer to the same file")
	}

	if options.acquireLock {
		release := h.pathLocks.acquire([]string{sourceResolved, targetResolved})
		defer release()
	}

	plan, err := h.buildSingleTransferPlan(ctx, internalInput, operation, sourceResolved, sourceDisplay, targetResolved, targetDisplay)
	if err != nil {
		if isTargetRangeOutOfBoundsError(err) {
			output.RangeErrorFileRole = "target"
		}
		h.enrichSingleTransferError(ctx, internalInput, &output, err)
		return output, err
	}
	output.Ranges = plan.ranges
	output.WouldWriteBytes = plan.wouldWriteBytes
	output.JoinerEffect = plan.joinerEffect
	output.BoundaryPreview = plan.boundaryPreview
	output.DiffPreviews = h.diffPreviewsForSinglePlan(ctx, plan, internalInput.Placement, operation, normalizeRedactionOrDefault(internalInput.RedactionMode))
	output.Validation = WriteValidation{Status: "planned_only", TargetReadBack: []ReadBackWindow{}, RedactionMode: normalizeRedactionOrDefault(internalInput.RedactionMode)}
	output.SourceFingerprintBefore = &plan.sourceInfo.fingerprint
	output.SourceFingerprintCheckedAtWrite = &plan.sourceInfo.fingerprint
	output.SourceFingerprintForNextWrite = &plan.sourceInfo.fingerprint
	output.Validation.NextRecommendedCall = dryRunValidationReadHint(output)
	if plan.targetInfo != nil {
		output.TargetFingerprintBefore = &plan.targetInfo.fingerprint
	}
	boundaryWarnings, err := boundaryWarningsForPlan(ctx, plan, internalInput.Placement)
	if err != nil {
		if errorCodeFromMessage(err.Error()) == "range_out_of_bounds" {
			err = targetRangeOutOfBoundsError(err)
			output.RangeErrorFileRole = "target"
		}
		h.enrichSingleTransferError(ctx, internalInput, &output, err)
		return output, err
	}
	output.BoundaryWarnings = boundaryWarnings
	if operation == operationMove {
		output.WouldRemoveSourceRanges = internalInput.Ranges
		for _, r := range internalInput.Ranges {
			output.WouldRemoveSourceLines += r.EndLine - r.StartLine + 1
		}
	}
	if internalInput.DryRun {
		return output, nil
	}

	if err := h.recheckSourceBeforeTarget(ctx, internalInput, &output); err != nil {
		return output, err
	}

	var backupPaths []string
	targetBackedUp := false
	if shouldBackup(internalInput.Backup) && plan.targetInfo != nil {
		backup, err := createSidecarBackup(targetResolved, "target")
		backup = h.displayBackupResult(pathCtx, backup)
		output.BackupResults = append(output.BackupResults, backup)
		if backup.BackupPath != "" {
			backupPaths = append(backupPaths, backup.BackupPath)
		}
		if err != nil {
			output.BackupPaths = backupPaths
			output.PartialState = singlePartialState(output, "backup_target", false, err)
			return output, err
		}
		targetBackedUp = backup.Created
	}
	output.BackupPaths = backupPaths

	if targetBackedUp {
		currentTarget, err := h.inspectTextFileForRefactorWriteEligible(ctx, internalInput.TargetFile)
		if err != nil {
			return output, err
		}
		if !fingerprintMatches(currentTarget.fingerprint, *internalInput.TargetPrecondition.Fingerprint) {
			err := targetFingerprintMismatchError("target fingerprint changed after backup")
			output.ExpectedTargetFingerprint = internalInput.TargetPrecondition.Fingerprint
			output.CurrentTargetFingerprint = &currentTarget.fingerprint
			output.PartialState = singlePartialState(output, "target_recheck_after_backup", false, err)
			output.PartialState.CurrentTargetFingerprint = &currentTarget.fingerprint
			return output, err
		}
	}

	if targetBackedUp {
		if err := h.recheckSourceBeforeTarget(ctx, internalInput, &output); err != nil {
			output.PartialState = singlePartialState(output, "source_recheck_before_target", false, err)
			if output.PartialState != nil {
				output.PartialState.CurrentSourceFingerprint = output.CurrentSourceFingerprint
			}
			return output, err
		}
	}
	if plan.targetInfo != nil {
		if err := h.recheckPlannedTarget(ctx, internalInput, &output, "target changed before target write"); err != nil {
			output.PartialState = singlePartialState(output, "target_recheck_before_write", false, err)
			if output.PartialState != nil {
				output.PartialState.CurrentTargetFingerprint = output.CurrentTargetFingerprint
			}
			return output, err
		}
	}
	if err := h.writeTargetForPlan(ctx, plan, internalInput.Placement); err != nil {
		if errorCodeFromMessage(err.Error()) == "range_out_of_bounds" {
			output.RangeErrorFileRole = "target"
		}
		output.PartialState = singlePartialState(output, "write_target", false, err)
		return output, err
	}
	output.BytesWritten = output.WouldWriteBytes
	targetAfter, err := h.inspectTextFileForRefactor(ctx, internalInput.TargetFile)
	if err != nil {
		err = targetPostWriteInspectError(err)
		output.PartialState = singlePartialState(output, "target_fingerprint_after_write", true, err)
		return output, err
	}
	output.TargetFingerprintAfter = &targetAfter.fingerprint
	output.TargetFingerprintForNextWrite = &targetAfter.fingerprint
	h.applyTargetValidation(ctx, &output, plan, internalInput.Placement, normalizeRedactionOrDefault(internalInput.RedactionMode))

	if operation == operationMove {
		sourceRecheck, err := h.inspectTextFileForRefactorWriteEligible(ctx, internalInput.SourceFile)
		if err != nil {
			output.PartialState = singlePartialState(output, "source_recheck_after_target", true, err)
			return output, err
		}
		if !fingerprintMatches(sourceRecheck.fingerprint, internalInput.SourceFingerprint) {
			err := sourceFingerprintMismatchError("source changed after target write")
			markRangeTransferSourceMismatch(&output, sourceRecheck.fingerprint)
			output.PartialState = singlePartialState(output, "target_written_source_not_updated", true, err)
			return output, err
		}
		if shouldBackup(internalInput.Backup) {
			backup, err := createSidecarBackup(sourceResolved, "source")
			backup = h.displayBackupResult(pathCtx, backup)
			output.BackupResults = append(output.BackupResults, backup)
			if backup.BackupPath != "" {
				output.BackupPaths = append(output.BackupPaths, backup.BackupPath)
			}
			if err != nil {
				output.PartialState = singlePartialState(output, "backup_source", true, err)
				return output, err
			}
		}
		sourceRecheckAfterBackup, err := h.inspectTextFileForRefactorWriteEligible(ctx, internalInput.SourceFile)
		if err != nil {
			output.PartialState = singlePartialState(output, "source_recheck_after_backup", true, err)
			return output, err
		}
		if !fingerprintMatches(sourceRecheckAfterBackup.fingerprint, internalInput.SourceFingerprint) {
			err := sourceFingerprintMismatchError("source changed after source backup")
			markRangeTransferSourceMismatch(&output, sourceRecheckAfterBackup.fingerprint)
			output.PartialState = singlePartialState(output, "source_recheck_after_backup", true, err)
			output.PartialState.CurrentSourceFingerprint = &sourceRecheckAfterBackup.fingerprint
			return output, err
		}
		if err := writeFileReplace(sourceResolved, func(w io.Writer) error {
			return copyFileExceptSpans(ctx, w, sourceResolved, spansFromRangeSpans(plan.sourceScan.RangeSpans))
		}); err != nil {
			output.PartialState = singlePartialState(output, "write_source", true, err)
			return output, err
		}
		output.RemovedSourceRanges = internalInput.Ranges
		output.RemovedSourceLines = output.WouldRemoveSourceLines
		sourceAfter, err := h.inspectTextFileForRefactor(ctx, internalInput.SourceFile)
		if err != nil {
			err = sourcePostWriteInspectError(err)
			output.PartialState = singlePartialState(output, "source_fingerprint_after_write", true, err)
			markSingleSourceModified(&output)
			return output, err
		}
		output.SourceFingerprintAfter = &sourceAfter.fingerprint
		output.SourceFingerprintForNextWrite = &sourceAfter.fingerprint
		h.applySourceValidation(ctx, &output, plan, normalizeRedactionOrDefault(internalInput.RedactionMode))
	}
	output.Applied = true
	output.BackupDiscovery = backupDiscoveryForResults(pathCtx, output.BackupResults)
	return output, nil
}

func dryRunValidationReadHint(output RangeTransferOutput) *ActionHint {
	if !output.DryRun || output.SourceFile == "" || len(output.RequestedRanges) == 0 {
		return nil
	}
	r := output.RequestedRanges[0]
	input := map[string]any{
		"target_file": output.SourceFile,
		"start_line":  r.StartLine,
		"end_line":    r.EndLine,
	}
	if output.SourceFingerprintForNextWrite != nil {
		input["expected_version"] = ReadCoverageProof{
			SizeBytes:        output.SourceFingerprintForNextWrite.SizeBytes,
			ModifiedUnixNano: output.SourceFingerprintForNextWrite.ModifiedUnixNano,
			SHA256:           output.SourceFingerprintForNextWrite.SHA256,
			ProofStrength:    "exact",
			Range:            r,
		}
	}
	return &ActionHint{
		SafeToRetry:                true,
		RecommendedNextTool:        "read_file",
		RecommendedNextInput:       input,
		RecommendedNextInputPolicy: "verify_escape_sensitive_preview",
		Reason:                     "Previews are bounded display text; for escape-sensitive edits, verify with read_file/read-back before applying.",
	}
}

func (h *Handler) buildSingleTransferPlan(ctx context.Context, input CopyRangesInput, operation, sourceResolved, sourceDisplay, targetResolved, targetDisplay string) (singleTransferPlan, error) {
	sourceInfo, err := h.prepareTransferSource(ctx, input.SourceFile, sourceResolved, input.SourceFingerprint)
	if err != nil {
		return singleTransferPlan{}, err
	}
	sourceScan, err := scanLineSpans(ctx, sourceResolved, input.Ranges, nil)
	if err != nil {
		return singleTransferPlan{}, err
	}
	return h.buildTransferPlanFromSource(ctx, input, operation, sourceResolved, sourceDisplay, targetResolved, targetDisplay, sourceInfo, sourceScan)
}

func (h *Handler) prepareTransferSource(ctx context.Context, sourceFile, sourceResolved string, sourceFingerprint FileFingerprint) (fileTextInfo, error) {
	if err := ensureWriteEligibleTextFile(ctx, sourceResolved, h.config.WriteThreshold); err != nil {
		return fileTextInfo{}, err
	}
	sourceInfo, err := h.inspectTextFileForRefactor(ctx, sourceFile)
	if err != nil {
		return fileTextInfo{}, err
	}
	if !fingerprintMatches(sourceInfo.fingerprint, sourceFingerprint) {
		return fileTextInfo{}, sourceFingerprintMismatchError("source file changed; call outline_file again")
	}
	return sourceInfo, nil
}

func (h *Handler) buildTransferPlanFromSource(ctx context.Context, input CopyRangesInput, operation, sourceResolved, sourceDisplay, targetResolved, targetDisplay string, sourceInfo fileTextInfo, sourceScan lineScanResult) (singleTransferPlan, error) {
	return h.buildTransferPlanFromSourceWithPayload(ctx, input, operation, sourceResolved, sourceDisplay, targetResolved, targetDisplay, sourceInfo, sourceScan, true)
}

func (h *Handler) buildTransferPlanFromSourceMetadata(ctx context.Context, input CopyRangesInput, operation, sourceResolved, sourceDisplay, targetResolved, targetDisplay string, sourceInfo fileTextInfo, sourceScan lineScanResult) (singleTransferPlan, error) {
	return h.buildTransferPlanFromSourceWithPayload(ctx, input, operation, sourceResolved, sourceDisplay, targetResolved, targetDisplay, sourceInfo, sourceScan, false)
}

func (h *Handler) buildTransferPlanFromSourceWithPayload(ctx context.Context, input CopyRangesInput, operation, sourceResolved, sourceDisplay, targetResolved, targetDisplay string, sourceInfo fileTextInfo, sourceScan lineScanResult, materializePayload bool) (singleTransferPlan, error) {
	plan := singleTransferPlan{
		sourceResolved: sourceResolved,
		sourceDisplay:  sourceDisplay,
		targetResolved: targetResolved,
		targetDisplay:  targetDisplay,
		sourceInfo:     sourceInfo,
		sourceScan:     sourceScan,
	}

	if err := validateBackupSpec(input.Backup, "backup.mode"); err != nil {
		return plan, err
	}
	redactionMode, err := normalizeRedactionMode(input.RedactionMode)
	if err != nil {
		return plan, err
	}
	targetExists := fileExists(targetResolved)
	joinerPath := sourceResolved
	if targetExists {
		joinerPath = targetResolved
	}
	joiner, err := joinerBytes(ctx, joinerPath, input.Joiner)
	if err != nil {
		return plan, err
	}
	plan.joiner = joiner
	plan.joinerEffect = joinerEffectForPayload(input.Joiner, joiner, len(sourceScan.RangeSpans))
	plan.payloadSize, plan.ranges = selectedPayloadStats(sourceScan.RangeSpans, joiner)
	if materializePayload {
		payload, ranges, err := selectedPayload(ctx, sourceResolved, sourceScan.RangeSpans, joiner)
		if err != nil {
			return plan, err
		}
		plan.payload = payload
		plan.ranges = ranges
		plan.joinerEffect.SourceBoundaries = sourceJoinerBoundaries(ctx, sourceResolved, sourceScan.RangeSpans, joiner, plan.joinerEffect.Normalized)
	}

	switch input.Placement.Mode {
	case placementCreateNew:
		if !input.TargetPrecondition.MustNotExist {
			return plan, fmt.Errorf("target_precondition.must_not_exist is required for create_new")
		}
		if targetExists {
			return plan, fmt.Errorf("target_exists: create_new target already exists")
		}
		if err := rejectSymlinkPath(targetResolved, false); err != nil {
			return plan, err
		}
		if err := ensureParentDirectory(targetResolved); err != nil {
			return plan, err
		}
		plan.wouldWriteBytes = plan.payloadSize
	default:
		if !targetExists {
			return plan, fmt.Errorf("target_missing: placement %q requires an existing target", input.Placement.Mode)
		}
		if input.TargetPrecondition.Fingerprint == nil {
			return plan, fmt.Errorf("target_precondition.fingerprint is required for existing target writes")
		}
		if err := ensureWriteEligibleTextFile(ctx, targetResolved, h.config.WriteThreshold); err != nil {
			return plan, err
		}
		targetInfo, err := h.inspectTextFileForRefactor(ctx, input.TargetFile)
		if err != nil {
			return plan, err
		}
		if !fingerprintMatches(targetInfo.fingerprint, *input.TargetPrecondition.Fingerprint) {
			return plan, fmt.Errorf("target_fingerprint_mismatch: target file changed; call outline_file with output_profile=fingerprint_only")
		}
		plan.targetInfo = &targetInfo
		if materializePayload {
			if err := applyPlacementJoiner(ctx, &plan, input.Placement, input.Joiner, h.config.BoundaryPreviewMaxChars, redactionMode); err != nil {
				return plan, err
			}
		}
		plan.wouldWriteBytes, err = plannedTargetSize(ctx, targetResolved, input.Placement, plan.payloadSize)
		if err != nil {
			return plan, targetRangeOutOfBoundsError(err)
		}
	}
	if plan.wouldWriteBytes > h.config.WriteThreshold {
		return plan, fmt.Errorf("planned write exceeds MCP_WRITE_THRESHOLD")
	}
	if input.Placement.Mode == placementCreateNew && materializePayload {
		plan.boundaryPreview = boundaryPreviewForPlan(ctx, plan, input.Placement, redactionMode, h.config.BoundaryPreviewMaxChars)
	}
	return plan, nil
}

func validateTargetPrecondition(precondition TargetPrecondition) error {
	if precondition.MustNotExist && precondition.Fingerprint != nil {
		return fmt.Errorf("target_precondition must use either must_not_exist or fingerprint, not both")
	}
	return nil
}

func selectedPayloadStats(spans []rangeSpan, joiner []byte) (int64, []TransferRangeResult) {
	results := make([]TransferRangeResult, 0, len(spans))
	var total int64
	for i, span := range spans {
		if i > 0 {
			total += int64(len(joiner))
		}
		byteCount := span.Span.End - span.Span.Start
		total += byteCount
		results = append(results, TransferRangeResult{
			Range:     span.Range,
			LineCount: span.Range.EndLine - span.Range.StartLine + 1,
			ByteCount: byteCount,
		})
	}
	return total, results
}

func joinerEffectForPayload(requested string, joiner []byte, rangeCount int) JoinerEffect {
	joinCount := maxInt(0, rangeCount-1)
	return JoinerEffect{
		Requested:                     requested,
		Normalized:                    normalizeJoinerOrNone(requested),
		NewlineBytes:                  escapedJoinerBytes(joiner),
		SourceRangeJoinCount:          joinCount,
		InsertedNewlinesBetweenRanges: countNewlines(joiner) * joinCount,
	}
}

func escapedJoinerBytes(joiner []byte) string {
	if len(joiner) == 0 {
		return ""
	}
	return strings.ReplaceAll(strings.ReplaceAll(string(joiner), "\r", "\\r"), "\n", "\\n")
}

func sourceJoinerBoundaries(ctx context.Context, path string, spans []rangeSpan, joiner []byte, normalized string) []JoinerBoundaryEffect {
	if len(spans) < 2 || len(joiner) == 0 {
		return nil
	}
	out := make([]JoinerBoundaryEffect, 0, len(spans)-1)
	for i := 0; i < len(spans)-1; i++ {
		left, err := readByteSpan(ctx, path, spans[i].Span)
		if err != nil {
			return out
		}
		right, err := readByteSpan(ctx, path, spans[i+1].Span)
		if err != nil {
			return out
		}
		out = append(out, joinerBoundaryEffect(fmt.Sprintf("source_range_%d_to_%d", i+1, i+2), left, right, countNewlines(joiner), normalized))
	}
	return out
}

func selectedPayload(ctx context.Context, path string, spans []rangeSpan, joiner []byte) ([]byte, []TransferRangeResult, error) {
	var buf bytes.Buffer
	results := make([]TransferRangeResult, 0, len(spans))
	for i, span := range spans {
		if i > 0 && len(joiner) > 0 {
			buf.Write(joiner)
		}
		data, err := readByteSpan(ctx, path, span.Span)
		if err != nil {
			return nil, nil, err
		}
		buf.Write(data)
		results = append(results, TransferRangeResult{
			Range:     span.Range,
			LineCount: span.Range.EndLine - span.Range.StartLine + 1,
			ByteCount: int64(len(data)),
		})
	}
	return buf.Bytes(), results, nil
}

func readByteSpan(ctx context.Context, path string, span byteSpan) ([]byte, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err := file.Seek(span.Start, io.SeekStart); err != nil {
		return nil, err
	}
	size := span.End - span.Start
	if size < 0 {
		return nil, fmt.Errorf("invalid byte span")
	}
	data := make([]byte, size)
	if _, err = io.ReadFull(file, data); err != nil {
		return nil, err
	}
	return data, nil
}

func joinerBytes(ctx context.Context, stylePath, joiner string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(joiner)) {
	case "", "none":
		return nil, nil
	case "single_newline":
		style, err := dominantNewlineStyle(ctx, stylePath)
		if err != nil {
			return nil, err
		}
		return []byte(style), nil
	case "blank_line":
		style, err := dominantNewlineStyle(ctx, stylePath)
		if err != nil {
			return nil, err
		}
		return []byte(style + style), nil
	default:
		return nil, fmt.Errorf("invalid_joiner: unsupported joiner %q; use none, single_newline, or blank_line", joiner)
	}
}

func dominantNewlineStyle(ctx context.Context, path string) (string, error) {
	if err := contextError(ctx); err != nil {
		return "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	buf := make([]byte, 64*1024)
	var crlf, lf int
	prevCR := false
	for {
		if err := contextError(ctx); err != nil {
			return "", err
		}
		n, readErr := file.Read(buf)
		for i := 0; i < n; i++ {
			b := buf[i]
			if b == '\n' {
				if prevCR {
					crlf++
				} else {
					lf++
				}
			}
			prevCR = b == '\r'
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return "", readErr
		}
		if n == 0 {
			break
		}
	}
	if crlf > lf {
		return "\r\n", nil
	}
	return "\n", nil
}

func plannedTargetSize(ctx context.Context, target string, placement TargetPlacement, payloadSize int64) (int64, error) {
	stat, err := os.Stat(target)
	if err != nil {
		return 0, err
	}
	switch placement.Mode {
	case placementAppend, placementPrepend, placementInsertBeforeLine:
		if placement.Mode == placementInsertBeforeLine {
			if _, err := targetInsertOffset(ctx, target, placement.Line); err != nil {
				return 0, err
			}
		}
		return stat.Size() + payloadSize, nil
	case placementReplaceRange:
		if placement.Range == nil {
			return 0, fmt.Errorf("invalid_placement: replace_range placement requires range")
		}
		scan, err := scanLineSpans(ctx, target, []SourceLineRange{*placement.Range}, nil)
		if err != nil {
			return 0, err
		}
		removed := scan.RangeSpans[0].Span.End - scan.RangeSpans[0].Span.Start
		return stat.Size() - removed + payloadSize, nil
	default:
		return 0, fmt.Errorf("unsupported placement mode %q", placement.Mode)
	}
}

func validatePlacementShape(placement TargetPlacement) error {
	switch placement.Mode {
	case placementCreateNew, placementAppend, placementPrepend:
		if placement.Line != 0 {
			return fmt.Errorf("invalid_placement: placement.line is only allowed for insert_before_line")
		}
		if placement.Range != nil {
			return fmt.Errorf("invalid_placement: placement.range is only allowed for replace_range")
		}
	case placementInsertBeforeLine:
		if placement.Line < 1 {
			return fmt.Errorf("invalid_placement: insert_before_line requires placement.line >= 1")
		}
		if placement.Range != nil {
			return fmt.Errorf("invalid_placement: placement.range is only allowed for replace_range")
		}
	case placementReplaceRange:
		if placement.Line != 0 {
			return fmt.Errorf("invalid_placement: placement.line is only allowed for insert_before_line")
		}
		if placement.Range == nil {
			return fmt.Errorf("invalid_placement: replace_range placement requires range")
		}
	default:
		return fmt.Errorf("invalid_placement: unsupported placement mode %q", placement.Mode)
	}
	return nil
}

func boundaryWarningsForPlan(ctx context.Context, plan singleTransferPlan, placement TargetPlacement) ([]BoundaryWarning, error) {
	joinerWarnings := joinerBoundaryWarnings(plan, placement)
	if plan.targetInfo == nil || len(plan.payload) == 0 {
		return joinerWarnings, nil
	}
	targetSize := plan.targetInfo.stat.Size()
	if targetSize == 0 {
		return joinerWarnings, nil
	}
	payloadFirst := plan.payload[0]
	payloadLast := plan.payload[len(plan.payload)-1]
	warnings := []BoundaryWarning{}
	addBefore := func(boundary string) {
		warnings = append(warnings, BoundaryWarning{
			Code:              "boundary_may_need_newline",
			Message:           "Inserted text may join with preceding target text without a newline.",
			TargetFile:        plan.targetDisplay,
			Placement:         placement.Mode,
			Boundary:          boundary,
			RecommendedAction: "Include the needed newline in the selected range or adjust placement/content if this was not intentional.",
		})
	}
	addAfter := func(boundary string) {
		warnings = append(warnings, BoundaryWarning{
			Code:              "boundary_may_need_newline",
			Message:           "Inserted text may join with following target text without a newline.",
			TargetFile:        plan.targetDisplay,
			Placement:         placement.Mode,
			Boundary:          boundary,
			RecommendedAction: "Include the needed newline in the selected range or adjust placement/content if this was not intentional.",
		})
	}
	checkBefore := func(offset int64, boundary string) error {
		if offset <= 0 || payloadFirst == '\n' {
			return nil
		}
		prev, ok, err := readByteAt(ctx, plan.targetResolved, offset-1)
		if err != nil || !ok {
			return err
		}
		if prev != '\n' {
			addBefore(boundary)
		}
		return nil
	}
	checkAfter := func(offset int64, boundary string) error {
		if offset >= targetSize || payloadLast == '\n' {
			return nil
		}
		next, ok, err := readByteAt(ctx, plan.targetResolved, offset)
		if err != nil || !ok {
			return err
		}
		if next != '\n' {
			addAfter(boundary)
		}
		return nil
	}
	switch placement.Mode {
	case placementAppend:
		if err := checkBefore(targetSize, "target_end_to_insert_start"); err != nil {
			return nil, err
		}
	case placementPrepend:
		if err := checkAfter(0, "insert_end_to_target_start"); err != nil {
			return nil, err
		}
	case placementInsertBeforeLine:
		offset, err := targetInsertOffset(ctx, plan.targetResolved, placement.Line)
		if err != nil {
			return nil, err
		}
		if err := checkBefore(offset, "target_before_insert_to_insert_start"); err != nil {
			return nil, err
		}
		if err := checkAfter(offset, "insert_end_to_target_after_insert"); err != nil {
			return nil, err
		}
	case placementReplaceRange:
		if placement.Range == nil {
			return nil, fmt.Errorf("invalid_placement: replace_range placement requires range")
		}
		scan, err := scanLineSpans(ctx, plan.targetResolved, []SourceLineRange{*placement.Range}, nil)
		if err != nil {
			return nil, err
		}
		span := scan.RangeSpans[0].Span
		if err := checkBefore(span.Start, "target_before_replaced_range_to_insert_start"); err != nil {
			return nil, err
		}
		if err := checkAfter(span.End, "insert_end_to_target_after_replaced_range"); err != nil {
			return nil, err
		}
	}
	warnings = append(warnings, joinerWarnings...)
	return warnings, nil
}

func joinerBoundaryWarnings(plan singleTransferPlan, placement TargetPlacement) []BoundaryWarning {
	warnings := []BoundaryWarning{}
	add := func(boundary JoinerBoundaryEffect, targetFile string, placementMode string) {
		for _, code := range boundary.WarningCodes {
			if code != "blank_line_joiner_extra_visual_blank_lines" {
				continue
			}
			warnings = append(warnings, BoundaryWarning{
				Code:              code,
				Message:           "blank_line joiner will leave more than one visual blank line at this boundary because existing boundary newlines are already present.",
				TargetFile:        targetFile,
				Placement:         placementMode,
				Boundary:          boundary.Boundary,
				RecommendedAction: "Use single_newline, none, or include tighter source ranges if the extra visual blank line is not intentional.",
			})
		}
	}
	for _, boundary := range plan.joinerEffect.SourceBoundaries {
		add(boundary, plan.targetDisplay, placement.Mode)
	}
	if len(plan.joinerEffect.TargetBoundaries) > 0 {
		for _, boundary := range plan.joinerEffect.TargetBoundaries {
			add(boundary, plan.targetDisplay, placement.Mode)
		}
	} else if plan.joinerEffect.TargetBoundary != nil {
		add(*plan.joinerEffect.TargetBoundary, plan.targetDisplay, placement.Mode)
	}
	return warnings
}

func readByteAt(ctx context.Context, path string, offset int64) (byte, bool, error) {
	if offset < 0 {
		return 0, false, nil
	}
	if err := contextError(ctx); err != nil {
		return 0, false, err
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, false, err
	}
	defer file.Close()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return 0, false, err
	}
	var buf [1]byte
	n, err := file.Read(buf[:])
	if err != nil {
		if err == io.EOF {
			return 0, false, nil
		}
		return 0, false, err
	}
	return buf[0], n == 1, nil
}

func (h *Handler) writeTargetForPlan(ctx context.Context, plan singleTransferPlan, placement TargetPlacement) error {
	switch placement.Mode {
	case placementCreateNew:
		err := writeFileCreateNew(plan.targetResolved, func(w io.Writer) error {
			_, err := w.Write(plan.payload)
			return err
		})
		if os.IsExist(err) {
			return fmt.Errorf("target_exists: create_new target already exists")
		}
		return err
	case placementAppend, placementPrepend, placementInsertBeforeLine, placementReplaceRange:
		err := writeFileReplace(plan.targetResolved, func(w io.Writer) error {
			return writeExistingTargetWithPlacement(ctx, w, plan.targetResolved, placement, plan.payload)
		})
		if err != nil && errorCodeFromMessage(err.Error()) == "range_out_of_bounds" {
			return targetRangeOutOfBoundsError(err)
		}
		return err
	default:
		return fmt.Errorf("unsupported placement mode %q", placement.Mode)
	}
}

func writeExistingTargetWithPlacement(ctx context.Context, w io.Writer, target string, placement TargetPlacement, payload []byte) error {
	switch placement.Mode {
	case placementAppend:
		if err := copyFileRange(ctx, w, target, 0, -1); err != nil {
			return err
		}
		_, err := w.Write(payload)
		return err
	case placementPrepend:
		if _, err := w.Write(payload); err != nil {
			return err
		}
		return copyFileRange(ctx, w, target, 0, -1)
	case placementInsertBeforeLine:
		offset, err := targetInsertOffset(ctx, target, placement.Line)
		if err != nil {
			return err
		}
		if err := copyFileRange(ctx, w, target, 0, offset); err != nil {
			return err
		}
		if _, err := w.Write(payload); err != nil {
			return err
		}
		return copyFileRange(ctx, w, target, offset, -1)
	case placementReplaceRange:
		if placement.Range == nil {
			return fmt.Errorf("invalid_placement: replace_range placement requires range")
		}
		scan, err := scanLineSpans(ctx, target, []SourceLineRange{*placement.Range}, nil)
		if err != nil {
			return err
		}
		span := scan.RangeSpans[0].Span
		if err := copyFileRange(ctx, w, target, 0, span.Start); err != nil {
			return err
		}
		if _, err := w.Write(payload); err != nil {
			return err
		}
		return copyFileRange(ctx, w, target, span.End, -1)
	default:
		return fmt.Errorf("unsupported placement mode %q", placement.Mode)
	}
}

func targetInsertOffset(ctx context.Context, target string, line int) (int64, error) {
	if line < 1 {
		return 0, fmt.Errorf("insert_before_line requires line >= 1")
	}
	scan, err := scanLineSpans(ctx, target, nil, []int{line})
	if err != nil {
		return 0, err
	}
	if line == scan.LineCount+1 {
		return scan.SizeBytes, nil
	}
	offset, ok := scan.LineStartOffset[line]
	if !ok || offset < 0 {
		return 0, fmt.Errorf("line %d is out of bounds; file has %d lines", line, scan.LineCount)
	}
	return offset, nil
}

func copyFileRange(ctx context.Context, w io.Writer, path string, start, end int64) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return err
	}
	var reader io.Reader = file
	if end >= 0 {
		reader = io.LimitReader(file, end-start)
	}
	_, err = io.Copy(w, reader)
	return err
}

func copyFileExceptSpans(ctx context.Context, w io.Writer, path string, spans []byteSpan) error {
	sort.Slice(spans, func(i, j int) bool { return spans[i].Start < spans[j].Start })
	offset := int64(0)
	for _, span := range spans {
		if span.Start > offset {
			if err := copyFileRange(ctx, w, path, offset, span.Start); err != nil {
				return err
			}
		}
		offset = span.End
	}
	return copyFileRange(ctx, w, path, offset, -1)
}

func spansFromRangeSpans(spans []rangeSpan) []byteSpan {
	out := make([]byteSpan, 0, len(spans))
	for _, span := range spans {
		out = append(out, span.Span)
	}
	return out
}

func (h *Handler) recheckSourceBeforeTarget(ctx context.Context, input CopyRangesInput, output *RangeTransferOutput) error {
	sourceInfo, err := h.inspectTextFileForRefactorWriteEligible(ctx, input.SourceFile)
	if err != nil {
		return err
	}
	output.SourceFingerprintCheckedAtWrite = &sourceInfo.fingerprint
	output.CurrentSourceFingerprint = &sourceInfo.fingerprint
	output.ExpectedSourceFingerprint = &input.SourceFingerprint
	if !fingerprintMatches(sourceInfo.fingerprint, input.SourceFingerprint) {
		output.SourceFingerprintForNextWrite = nil
		return fmt.Errorf("source_fingerprint_mismatch: source changed before target write")
	}
	output.SourceFingerprintForNextWrite = &sourceInfo.fingerprint
	return nil
}

func markRangeTransferSourceMismatch(output *RangeTransferOutput, current FileFingerprint) {
	if output == nil {
		return
	}
	output.CurrentSourceFingerprint = &current
	output.SourceFingerprintForNextWrite = nil
}

func (h *Handler) enrichSingleTransferError(ctx context.Context, input CopyRangesInput, output *RangeTransferOutput, cause error) {
	if output == nil || cause == nil {
		return
	}
	output.RequestedRanges = append([]SourceLineRange(nil), input.Ranges...)
	code := errorCodeFromMessage(cause.Error())
	switch code {
	case "source_fingerprint_mismatch", "zero_byte_range", "range_out_of_bounds":
		if code == "range_out_of_bounds" && output.RangeErrorFileRole == "target" {
			if input.TargetPrecondition.Fingerprint != nil {
				output.ExpectedTargetFingerprint = input.TargetPrecondition.Fingerprint
			}
			if info, err := h.inspectTextFileForRefactor(ctx, input.TargetFile); err == nil {
				output.CurrentTargetFingerprint = &info.fingerprint
				if output.TargetFingerprintBefore == nil {
					output.TargetFingerprintBefore = &info.fingerprint
				}
			}
			return
		}
		output.ExpectedSourceFingerprint = &input.SourceFingerprint
		if code == "source_fingerprint_mismatch" {
			output.SourceFingerprintForNextWrite = nil
		}
		if info, err := h.inspectTextFileForRefactor(ctx, input.SourceFile); err == nil {
			output.CurrentSourceFingerprint = &info.fingerprint
			if output.SourceFingerprintBefore == nil {
				output.SourceFingerprintBefore = &info.fingerprint
			}
		}
	case "target_fingerprint_mismatch", "target_missing", "target_exists":
		if input.TargetPrecondition.Fingerprint != nil {
			output.ExpectedTargetFingerprint = input.TargetPrecondition.Fingerprint
		}
		if info, err := h.inspectTextFileForRefactor(ctx, input.TargetFile); err == nil {
			output.CurrentTargetFingerprint = &info.fingerprint
			if output.TargetFingerprintBefore == nil {
				output.TargetFingerprintBefore = &info.fingerprint
			}
		}
	}
}

func actionHintForRangeTransferOutput(output RangeTransferOutput) *ActionHint {
	if output.ErrorCode == "range_out_of_bounds" && output.RangeErrorFileRole == "target" {
		return &ActionHint{
			SafeToRetry:         false,
			RecommendedNextTool: "outline_file",
			RecommendedNextInput: map[string]any{
				"target_file":    output.TargetFile,
				"output_profile": outlineProfileFingerprintOnly,
			},
			Reason: "Refresh the target outline/fingerprint and adjust placement line or range before retrying.",
		}
	}
	return actionHintForTransferError(output.ErrorCode, output.SourceFile, output.TargetFile)
}

func validateBackupSpec(spec BackupSpec, fieldName string) error {
	switch strings.ToLower(strings.TrimSpace(spec.Mode)) {
	case "", "none", backupModeSidecar:
		return nil
	default:
		return fmt.Errorf("unsupported %s %q; use none or sidecar", fieldName, spec.Mode)
	}
}

func (h *Handler) displayBackupResult(pathCtx PathContext, result BackupResult) BackupResult {
	if result.File != "" {
		result.File = h.projectOutputPath(pathCtx, result.File)
	}
	if result.BackupPath != "" {
		result.BackupPath = h.projectOutputPath(pathCtx, result.BackupPath)
	}
	return result
}

func shouldBackup(spec BackupSpec) bool {
	return strings.EqualFold(strings.TrimSpace(spec.Mode), backupModeSidecar)
}

func operationOutputName(operation string, batch bool) string {
	switch operation {
	case operationCopy:
		if batch {
			return "copy_batch"
		}
		return operationCopy
	case operationMove:
		if batch {
			return "move_batch"
		}
		return operationMove
	default:
		return operation
	}
}

func outputOperationIsMove(operation string) bool {
	return operation == operationMove || operation == "move_batch" || operation == "move_ranges" || operation == "move_ranges_batch"
}

func sourceFingerprintMismatchError(message string) error {
	return fmt.Errorf("source_fingerprint_mismatch: %s", message)
}

func targetFingerprintMismatchError(message string) error {
	return fmt.Errorf("target_fingerprint_mismatch: %s", message)
}

func targetRangeOutOfBoundsError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("target_range_out_of_bounds: %w", err)
}

func targetPostWriteInspectError(err error) error {
	return fmt.Errorf("target_post_write_inspect_failed: target was written but its fingerprint could not be confirmed: %w", err)
}

func sourcePostWriteInspectError(err error) error {
	return fmt.Errorf("source_post_write_inspect_failed: source was written but its fingerprint could not be confirmed: %w", err)
}

func isTargetRangeOutOfBoundsError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "target_range_out_of_bounds")
}

func ensureParentDirectory(path string) error {
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("parent_directory_missing: cannot access target parent directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("parent_directory_missing: target parent is not a directory")
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func singlePartialState(output RangeTransferOutput, phase string, targetWritten bool, err error) *PartialState {
	errorCode := errorCodeFromMessage(err.Error())
	return &PartialState{
		Operation:                  output.Operation,
		Phase:                      phase,
		SourceFile:                 output.SourceFile,
		TargetFile:                 output.TargetFile,
		SourceModifiedByTool:       false,
		TargetWritten:              targetWritten,
		FilesMaybeModified:         maybeModifiedFiles(output, targetWritten),
		BackupPaths:                output.BackupPaths,
		SourceFingerprintBefore:    output.SourceFingerprintBefore,
		TargetFingerprintBefore:    output.TargetFingerprintBefore,
		TargetFingerprintAfter:     output.TargetFingerprintAfter,
		CurrentSourceFingerprint:   output.CurrentSourceFingerprint,
		CurrentTargetFingerprint:   output.CurrentTargetFingerprint,
		ErrorCode:                  errorCode,
		Error:                      err.Error(),
		RecommendedNextTool:        partialRecommendedTool(errorCode, output.RangeErrorFileRole),
		RecommendedNextInput:       singlePartialRecommendedInput(output, targetWritten, errorCode),
		RecommendedNextInputPolicy: "inspect_modified_files_before_retry",
		RecoveryHint:               "Inspect fingerprints for any file reported as maybe modified before retrying.",
		Ranges:                     output.Ranges,
	}
}

func partialRecommendedTool(errorCode, fileRole string) string {
	if errorCode == "target_missing" {
		return "inspect_path"
	}
	return "outline_file"
}

func singlePartialRecommendedInput(output RangeTransferOutput, targetWritten bool, errorCode string) map[string]any {
	if output.SourceFile == "" {
		return nil
	}
	if errorCode == "source_post_write_inspect_failed" {
		return map[string]any{
			"target_file":    output.SourceFile,
			"output_profile": outlineProfileFingerprintOnly,
		}
	}
	if errorCode == "target_post_write_inspect_failed" {
		if output.TargetFile == "" {
			return nil
		}
		return map[string]any{
			"target_file":    output.TargetFile,
			"output_profile": outlineProfileFingerprintOnly,
		}
	}
	if errorCode == "range_out_of_bounds" && output.RangeErrorFileRole == "target" {
		if output.TargetFile == "" {
			return nil
		}
		return map[string]any{
			"target_file":    output.TargetFile,
			"output_profile": outlineProfileFingerprintOnly,
		}
	}
	if errorCode == "target_missing" {
		if output.TargetFile == "" {
			return nil
		}
		return map[string]any{
			"target_path": output.TargetFile,
		}
	}
	if errorCode == "target_fingerprint_mismatch" || errorCode == "target_exists" {
		if output.TargetFile == "" {
			return nil
		}
		return map[string]any{
			"target_file":    output.TargetFile,
			"output_profile": outlineProfileFingerprintOnly,
		}
	}
	if output.CurrentSourceFingerprint != nil || !targetWritten || output.TargetFile == "" {
		return map[string]any{
			"target_file":    output.SourceFile,
			"output_profile": outlineProfileOutline,
		}
	}
	return map[string]any{
		"target_file":    output.TargetFile,
		"output_profile": outlineProfileFingerprintOnly,
	}
}

func maybeModifiedFiles(output RangeTransferOutput, targetWritten bool) []string {
	if targetWritten {
		return []string{output.TargetFile}
	}
	return []string{}
}

func markSingleSourceModified(output *RangeTransferOutput) {
	if output == nil || output.PartialState == nil {
		return
	}
	output.PartialState.SourceModifiedByTool = true
	files := output.PartialState.FilesMaybeModified
	seen := map[string]bool{}
	for _, file := range files {
		seen[file] = true
	}
	if output.SourceFile != "" && !seen[output.SourceFile] {
		output.PartialState.FilesMaybeModified = append(output.PartialState.FilesMaybeModified, output.SourceFile)
	}
}
