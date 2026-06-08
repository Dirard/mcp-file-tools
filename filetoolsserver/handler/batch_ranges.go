package handler

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (h *Handler) HandleCopyRangesBatch(ctx context.Context, req *mcp.CallToolRequest, input CopyRangesBatchInput) (*mcp.CallToolResult, CopyRangesBatchOutput, error) {
	pathCtx, cwdErr := h.BuildPathContext(input.CwdID)
	if cwdErr != nil {
		return toolCwdError[CopyRangesBatchOutput](cwdErr)
	}
	output, err := h.executeBatchRanges(ctx, pathCtx, input.SourceFile, input.SourceFingerprint, input.Targets, BackupSpec{}, input.RedactionMode, input.DryRun, operationCopy)
	output.BackupDiscovery = backupDiscoveryForResults(pathCtx, output.BackupResults)
	if err != nil {
		output.Error = err.Error()
		output.ErrorCode = errorCodeFromMessage(err.Error())
		if output.ActionHint == nil {
			output.ActionHint = actionHintForTransferError(output.ErrorCode, output.SourceFile, "")
		}
		return errorResult(err.Error()), CopyRangesBatchOutput(output), nil
	}
	return structuredResultOnly(), CopyRangesBatchOutput(output), nil
}

func (h *Handler) HandleMoveRangesBatch(ctx context.Context, req *mcp.CallToolRequest, input MoveRangesBatchInput) (*mcp.CallToolResult, MoveRangesBatchOutput, error) {
	pathCtx, cwdErr := h.BuildPathContext(input.CwdID)
	if cwdErr != nil {
		return toolCwdError[MoveRangesBatchOutput](cwdErr)
	}
	output, err := h.executeBatchRanges(ctx, pathCtx, input.SourceFile, input.SourceFingerprint, input.Targets, input.SourceBackup, input.RedactionMode, input.DryRun, operationMove)
	output.BackupDiscovery = backupDiscoveryForResults(pathCtx, output.BackupResults)
	if err != nil {
		output.Error = err.Error()
		output.ErrorCode = errorCodeFromMessage(err.Error())
		if output.ActionHint == nil {
			output.ActionHint = actionHintForTransferError(output.ErrorCode, output.SourceFile, "")
		}
		return errorResult(err.Error()), MoveRangesBatchOutput(output), nil
	}
	return structuredResultOnly(), MoveRangesBatchOutput(output), nil
}

type resolvedBatchTarget struct {
	target     BatchRangeTarget
	resolved   string
	display    string
	rangeStart int
}

type batchTargetPlan struct {
	input        CopyRangesInput
	plan         singleTransferPlan
	output       RangeTransferOutput
	materialized bool
}

func (h *Handler) executeBatchRanges(ctx context.Context, pathCtx PathContext, sourceFile string, sourceFingerprint FileFingerprint, targets []BatchRangeTarget, sourceBackup BackupSpec, redactionMode string, dryRun bool, operation string) (BatchRangeTransferOutput, error) {
	output := BatchRangeTransferOutput{
		Operation:      operationOutputName(operation, true),
		DryRun:         dryRun,
		TargetResults:  []BatchTargetResult{},
		TargetsWritten: []string{},
		BatchWarnings:  []ToolWarning{},
		Warnings:       []ToolWarning{},
		BackupPaths:    []string{},
		BackupResults:  []BackupResult{},
	}
	if len(targets) == 0 {
		return output, fmt.Errorf("targets must contain at least one target")
	}
	normalizedRedactionMode, err := normalizeRedactionMode(redactionMode)
	if err != nil {
		return output, err
	}
	if operation == operationMove {
		if err := validateBackupSpec(sourceBackup, "source_backup.mode"); err != nil {
			return output, err
		}
	}
	if err := h.validateBatchBounds(targets); err != nil {
		output.ErrorCode = "batch_limit_exceeded"
		output.ActionHint = &ActionHint{
			SafeToRetry:                false,
			RecommendedNextInputPolicy: "split_batch_explicitly",
			Reason:                     err.Error(),
		}
		return output, err
	}
	sourceResolved, sourceDisplay, err := h.resolveRefactorPath(pathCtx, sourceFile, "source_file")
	if err != nil {
		return output, err
	}
	internalSourceFile := sourceResolved
	output.SourceFile = sourceDisplay
	if err := rejectSymlinkPath(sourceResolved, true); err != nil {
		return output, err
	}
	lockPaths := []string{sourceResolved}
	resolvedTargetPaths := []string{}
	resolvedTargets := make([]resolvedBatchTarget, 0, len(targets))
	allRanges := make([]SourceLineRange, 0)
	for _, target := range targets {
		if err := validateSourceRanges(target.Ranges, operation == operationCopy); err != nil {
			return output, err
		}
		if err := validateTargetPrecondition(target.TargetPrecondition); err != nil {
			return output, err
		}
		if err := validatePlacementShape(target.Placement); err != nil {
			return output, err
		}
		if err := validateBackupSpec(target.Backup, "backup.mode"); err != nil {
			return output, err
		}
		targetResolved, targetDisplay, err := h.resolveRefactorPath(pathCtx, target.TargetFile, "target_file")
		if err != nil {
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
		for _, prior := range resolvedTargetPaths {
			sameTarget, err := sameFileOrPath(prior, targetResolved)
			if err != nil {
				return output, err
			}
			if sameTarget {
				return output, fmt.Errorf("target files must be unique")
			}
		}
		targetRedactionMode, err := stricterRedactionMode(normalizedRedactionMode, target.RedactionMode)
		if err != nil {
			return output, err
		}
		internalTarget := target
		internalTarget.TargetFile = targetResolved
		internalTarget.RedactionMode = targetRedactionMode
		resolvedTargets = append(resolvedTargets, resolvedBatchTarget{
			target:     internalTarget,
			resolved:   targetResolved,
			display:    targetDisplay,
			rangeStart: len(allRanges),
		})
		allRanges = append(allRanges, target.Ranges...)
		resolvedTargetPaths = append(resolvedTargetPaths, targetResolved)
		lockPaths = append(lockPaths, targetResolved)
	}
	if operation == operationCopy {
		var omittedWarningCounts map[string]int
		output.BatchWarnings, output.WarningsTruncated, omittedWarningCounts = copyBatchRangeWarnings(sourceDisplay, targets)
		output.OmittedWarningCounts = omittedWarningCounts
		output.WarningSummary = warningSummary(output.BatchWarnings, output.Warnings)
		addOmittedWarningCounts(output.WarningSummary, output.OmittedWarningCounts)
	}
	if operation == operationMove {
		if err := validateSourceRanges(sortedRanges(allRanges), false); err != nil {
			return output, fmt.Errorf("move_ranges_batch ranges must be non-overlapping across targets: %w", err)
		}
	}
	release := h.pathLocks.acquire(lockPaths)
	defer release()

	sourceInfo, err := h.prepareBatchTransferSource(ctx, internalSourceFile, sourceResolved, sourceFingerprint, &output)
	if err != nil {
		output.ActionHint = actionHintForTransferError(errorCodeFromMessage(err.Error()), output.SourceFile, "")
		return output, err
	}
	sourceScan, err := scanLineSpans(ctx, sourceResolved, allRanges, nil)
	if err != nil {
		output.ActionHint = actionHintForTransferError(errorCodeFromMessage(err.Error()), output.SourceFile, "")
		return output, err
	}

	targetPlans := make([]batchTargetPlan, 0, len(resolvedTargets))
	var plannedTargetBytes int64
	var plannedSourceRewriteBytes int64
	if operation == operationMove {
		output.WouldRemoveSourceRanges = sortedRanges(allRanges)
		for _, r := range output.WouldRemoveSourceRanges {
			output.WouldRemoveSourceLines += r.EndLine - r.StartLine + 1
		}
		plannedSourceRewriteBytes = sourceInfo.stat.Size() - rangeSpanTotalBytes(sourceScan.RangeSpans)
		if plannedSourceRewriteBytes < 0 {
			plannedSourceRewriteBytes = 0
		}
		if plannedSourceRewriteBytes > h.config.BatchMaxPlannedBytes {
			output.WouldRewriteSourceBytes = plannedSourceRewriteBytes
			syncBatchByteAliases(&output)
			output.ErrorCode = "batch_limit_exceeded"
			output.ActionHint = batchLimitExceededHint()
			return output, fmt.Errorf("batch_limit_exceeded: planned bytes %d exceed limit %d", plannedSourceRewriteBytes, h.config.BatchMaxPlannedBytes)
		}
		output.SourceDiffPreviews = batchSourceDiffPreviews(ctx, sourceResolved, sourceDisplay, sourceScan.RangeSpans, h.config.DiffPreviewMaxBytes, normalizedRedactionMode)
		output.SourceValidation = &WriteValidation{Status: "planned_only", TargetReadBack: []ReadBackWindow{}, RedactionMode: normalizedRedactionMode}
	}
	for i, target := range resolvedTargets {
		rangeEnd := target.rangeStart + len(target.target.Ranges)
		targetSourceScan := sourceScanForBatchTarget(sourceScan, sourceScan.RangeSpans[target.rangeStart:rangeEnd])
		targetPlan, err := h.planBatchTarget(ctx, internalSourceFile, sourceFingerprint, sourceResolved, sourceDisplay, sourceInfo, target, targetSourceScan, dryRun)
		if err != nil {
			output.TargetResults = append(output.TargetResults, batchTargetResultFromSingle(targetPlan.output, "failed", false, false, true, err))
			output.TargetResults[len(output.TargetResults)-1].FailedAt = "validation"
			for j := i + 1; j < len(resolvedTargets); j++ {
				output.TargetResults = append(output.TargetResults, skippedBatchTargetResult(resolvedTargets[j].display))
			}
			targetPlan.output.ErrorCode = errorCodeFromMessage(err.Error())
			output.ActionHint = actionHintForRangeTransferOutput(targetPlan.output)
			return output, fmt.Errorf("target %d validation failed: %w", i, err)
		}
		if err := h.materializeBatchTargetPlan(ctx, &targetPlan); err != nil {
			output.TargetResults = append(output.TargetResults, batchTargetResultFromSingle(targetPlan.output, "failed", false, false, true, err))
			output.TargetResults[len(output.TargetResults)-1].FailedAt = "validation"
			for j := i + 1; j < len(resolvedTargets); j++ {
				output.TargetResults = append(output.TargetResults, skippedBatchTargetResult(resolvedTargets[j].display))
			}
			targetPlan.output.ErrorCode = errorCodeFromMessage(err.Error())
			output.ActionHint = actionHintForRangeTransferOutput(targetPlan.output)
			return output, fmt.Errorf("target %d validation failed: %w", i, err)
		}
		plannedTargetBytes += targetPlan.output.WouldWriteBytes
		targetPlans = append(targetPlans, targetPlan)
		output.TargetResults = append(output.TargetResults, batchTargetResultFromSingle(targetPlan.output, "planned", false, false, false, nil))
		if plannedTargetBytes+plannedSourceRewriteBytes > h.config.BatchMaxPlannedBytes {
			output.WouldWriteTargetBytes = plannedTargetBytes
			output.WouldRewriteSourceBytes = plannedSourceRewriteBytes
			syncBatchByteAliases(&output)
			output.ErrorCode = "batch_limit_exceeded"
			output.ActionHint = batchLimitExceededHint()
			return output, fmt.Errorf("batch_limit_exceeded: planned bytes %d exceed limit %d", plannedTargetBytes+plannedSourceRewriteBytes, h.config.BatchMaxPlannedBytes)
		}
	}
	output.WouldWriteTargetBytes = plannedTargetBytes
	output.WouldRewriteSourceBytes = plannedSourceRewriteBytes
	syncBatchByteAliases(&output)
	if dryRun {
		return output, nil
	}

	if err := h.recheckBatchSourceBeforeTargets(ctx, internalSourceFile, sourceFingerprint, &output); err != nil {
		output.TargetResults = []BatchTargetResult{}
		for _, target := range resolvedTargets {
			output.TargetResults = append(output.TargetResults, skippedBatchTargetResult(target.display))
		}
		output.PartialState = batchPartialState(output, "source_recheck_before_targets", err)
		return output, err
	}

	output.TargetResults = []BatchTargetResult{}
	for i, targetPlan := range targetPlans {
		if err := h.recheckBatchSourceBeforeTargets(ctx, internalSourceFile, sourceFingerprint, &output); err != nil {
			failed := batchTargetResultFromSingle(targetPlan.output, "failed", false, false, true, err)
			failed.FailedAt = "source_recheck_before_target"
			output.TargetResults = append(output.TargetResults, failed)
			for j := i + 1; j < len(targetPlans); j++ {
				output.TargetResults = append(output.TargetResults, skippedBatchTargetResult(targetPlans[j].output.TargetFile))
			}
			output.PartialState = batchPartialState(output, "source_recheck_before_target", err)
			return output, err
		}
		singleOutput, err := h.writeBatchTargetPlan(ctx, pathCtx, targetPlan, internalSourceFile, sourceFingerprint, &output)
		if err != nil {
			carryBatchTargetSideEffects(&output, singleOutput)
			targetWritten := singleOutput.BytesWritten > 0 || errorCodeFromMessage(err.Error()) == "target_post_write_inspect_failed"
			if singleOutput.BytesWritten > 0 {
				output.BytesWrittenTargetBytes += singleOutput.BytesWritten
				syncBatchByteAliases(&output)
			}
			output.TargetResults = append(output.TargetResults, batchTargetResultFromSingle(singleOutput, "failed", targetWritten, false, true, err))
			for j := i + 1; j < len(targetPlans); j++ {
				output.TargetResults = append(output.TargetResults, skippedBatchTargetResult(targetPlans[j].output.TargetFile))
			}
			output.PartialState = batchPartialState(output, "write_targets", err)
			return output, err
		}
		output.TargetResults = append(output.TargetResults, batchTargetResultFromSingle(singleOutput, "written", true, false, false, nil))
		output.TargetsWritten = append(output.TargetsWritten, singleOutput.TargetFile)
		output.BackupPaths = append(output.BackupPaths, singleOutput.BackupPaths...)
		output.BackupResults = append(output.BackupResults, singleOutput.BackupResults...)
		output.BytesWrittenTargetBytes += singleOutput.BytesWritten
		syncBatchByteAliases(&output)
	}
	if operation == operationMove {
		if err := h.applyBatchSourceMoveWithRedaction(ctx, pathCtx, internalSourceFile, sourceFingerprint, sourceResolved, sourceBackup, normalizedRedactionMode, output.WouldRemoveSourceRanges, sourceScan.RangeSpans, &output); err != nil {
			output.PartialState = batchPartialState(output, "write_source", err)
			return output, err
		}
	} else {
		h.refreshCopyBatchSourceFingerprint(ctx, internalSourceFile, sourceFingerprint, &output)
	}
	output.Applied = true
	output.BackupDiscovery = backupDiscoveryForResults(pathCtx, output.BackupResults)
	return output, nil
}

func (h *Handler) prepareBatchTransferSource(ctx context.Context, sourceFile, sourceResolved string, sourceFingerprint FileFingerprint, output *BatchRangeTransferOutput) (fileTextInfo, error) {
	if err := ensureWriteEligibleTextFile(ctx, sourceResolved, h.config.WriteThreshold); err != nil {
		return fileTextInfo{}, err
	}
	sourceInfo, err := h.inspectTextFileForRefactor(ctx, sourceFile)
	if err != nil {
		return fileTextInfo{}, err
	}
	output.SourceFingerprintBefore = &sourceInfo.fingerprint
	output.SourceFingerprintCheckedAtWrite = &sourceInfo.fingerprint
	output.SourceFingerprintForNextWrite = &sourceInfo.fingerprint
	if !fingerprintMatches(sourceInfo.fingerprint, sourceFingerprint) {
		markBatchSourceMismatch(output, sourceInfo.fingerprint)
		return fileTextInfo{}, sourceFingerprintMismatchError("source file changed; call outline_file again")
	}
	return sourceInfo, nil
}

func sourceScanForBatchTarget(sourceScan lineScanResult, spans []rangeSpan) lineScanResult {
	targetScan := sourceScan
	targetScan.RangeSpans = append([]rangeSpan(nil), spans...)
	return targetScan
}

func (h *Handler) planBatchTarget(ctx context.Context, sourceFile string, sourceFingerprint FileFingerprint, sourceResolved, sourceDisplay string, sourceInfo fileTextInfo, target resolvedBatchTarget, sourceScan lineScanResult, dryRun bool) (batchTargetPlan, error) {
	input := batchTargetToCopyInput(sourceFile, sourceFingerprint, target.target, dryRun)
	output := initializedBatchTargetOutput(input, sourceDisplay, target.display, sourceInfo)
	plan, err := h.buildTransferPlanFromSourceMetadata(ctx, input, operationCopy, sourceResolved, sourceDisplay, target.resolved, target.display, sourceInfo, sourceScan)
	targetPlan := batchTargetPlan{input: input, plan: plan, output: output}
	if err != nil {
		if isTargetRangeOutOfBoundsError(err) {
			targetPlan.output.RangeErrorFileRole = "target"
		}
		h.enrichSingleTransferError(ctx, input, &targetPlan.output, err)
		return targetPlan, err
	}
	targetPlan.output.Ranges = plan.ranges
	targetPlan.output.WouldWriteBytes = plan.wouldWriteBytes
	if plan.targetInfo != nil {
		targetPlan.output.TargetFingerprintBefore = &plan.targetInfo.fingerprint
	}
	return targetPlan, nil
}

func (h *Handler) materializeBatchTargetPlan(ctx context.Context, targetPlan *batchTargetPlan) error {
	if targetPlan == nil || targetPlan.materialized {
		return nil
	}
	payload, ranges, err := selectedPayload(ctx, targetPlan.plan.sourceResolved, targetPlan.plan.sourceScan.RangeSpans, targetPlan.plan.joiner)
	if err != nil {
		return err
	}
	targetPlan.plan.payload = payload
	targetPlan.plan.ranges = ranges
	targetPlan.plan.joinerEffect.SourceBoundaries = sourceJoinerBoundaries(ctx, targetPlan.plan.sourceResolved, targetPlan.plan.sourceScan.RangeSpans, targetPlan.plan.joiner, targetPlan.plan.joinerEffect.Normalized)
	redactionMode := normalizeRedactionOrDefault(targetPlan.input.RedactionMode)
	if targetPlan.plan.targetInfo != nil {
		if err := applyPlacementJoiner(ctx, &targetPlan.plan, targetPlan.input.Placement, targetPlan.input.Joiner, h.config.BoundaryPreviewMaxChars, redactionMode); err != nil {
			return err
		}
		targetPlan.plan.wouldWriteBytes, err = plannedTargetSize(ctx, targetPlan.plan.targetResolved, targetPlan.input.Placement, targetPlan.plan.payloadSize)
		if err != nil {
			return targetRangeOutOfBoundsError(err)
		}
	} else if targetPlan.input.Placement.Mode == placementCreateNew {
		targetPlan.plan.payloadSize = int64(len(targetPlan.plan.payload))
		targetPlan.plan.wouldWriteBytes = targetPlan.plan.payloadSize
		targetPlan.plan.boundaryPreview = boundaryPreviewForPlan(ctx, targetPlan.plan, targetPlan.input.Placement, redactionMode, h.config.BoundaryPreviewMaxChars)
	}
	if targetPlan.plan.wouldWriteBytes > h.config.WriteThreshold {
		return fmt.Errorf("planned write exceeds MCP_WRITE_THRESHOLD")
	}
	targetPlan.output.Ranges = ranges
	targetPlan.output.WouldWriteBytes = targetPlan.plan.wouldWriteBytes
	targetPlan.output.JoinerEffect = targetPlan.plan.joinerEffect
	targetPlan.output.BoundaryPreview = targetPlan.plan.boundaryPreview
	targetPlan.output.DiffPreviews = h.diffPreviewsForSinglePlan(ctx, targetPlan.plan, targetPlan.input.Placement, operationCopy, redactionMode)
	targetPlan.output.Validation = WriteValidation{Status: "planned_only", TargetReadBack: []ReadBackWindow{}, RedactionMode: redactionMode}
	boundaryWarnings, err := boundaryWarningsForPlan(ctx, targetPlan.plan, targetPlan.input.Placement)
	if err != nil {
		if errorCodeFromMessage(err.Error()) == "range_out_of_bounds" {
			err = targetRangeOutOfBoundsError(err)
			targetPlan.output.RangeErrorFileRole = "target"
		}
		h.enrichSingleTransferError(ctx, targetPlan.input, &targetPlan.output, err)
		return err
	}
	targetPlan.output.BoundaryWarnings = boundaryWarnings
	targetPlan.materialized = true
	return nil
}

func initializedBatchTargetOutput(input CopyRangesInput, sourceDisplay, targetDisplay string, sourceInfo fileTextInfo) RangeTransferOutput {
	return RangeTransferOutput{
		Operation:                       operationOutputName(operationCopy, false),
		DryRun:                          input.DryRun,
		SourceFile:                      sourceDisplay,
		TargetFile:                      targetDisplay,
		RequestedRanges:                 append([]SourceLineRange(nil), input.Ranges...),
		Ranges:                          []TransferRangeResult{},
		TargetPlacement:                 input.Placement,
		SourceFingerprintBefore:         &sourceInfo.fingerprint,
		SourceFingerprintCheckedAtWrite: &sourceInfo.fingerprint,
		SourceFingerprintForNextWrite:   &sourceInfo.fingerprint,
		BoundaryWarnings:                []BoundaryWarning{},
		Warnings:                        []ToolWarning{},
		BackupPaths:                     []string{},
		BackupResults:                   []BackupResult{},
	}
}

func (h *Handler) recheckBatchSourceBeforeTargets(ctx context.Context, sourceFile string, sourceFingerprint FileFingerprint, output *BatchRangeTransferOutput) error {
	sourceInfo, err := h.inspectTextFileForRefactorWriteEligible(ctx, sourceFile)
	if err != nil {
		return err
	}
	output.SourceFingerprintCheckedAtWrite = &sourceInfo.fingerprint
	output.CurrentSourceFingerprint = &sourceInfo.fingerprint
	if !fingerprintMatches(sourceInfo.fingerprint, sourceFingerprint) {
		markBatchSourceMismatch(output, sourceInfo.fingerprint)
		return sourceFingerprintMismatchError("source changed before target write")
	}
	output.SourceFingerprintForNextWrite = &sourceInfo.fingerprint
	return nil
}

func (h *Handler) writeBatchTargetPlan(ctx context.Context, pathCtx PathContext, targetPlan batchTargetPlan, sourceFile string, sourceFingerprint FileFingerprint, batchOutput *BatchRangeTransferOutput) (RangeTransferOutput, error) {
	input := targetPlan.input
	plan := targetPlan.plan
	output := targetPlan.output
	output.DryRun = false

	if plan.targetInfo != nil {
		if err := h.recheckPlannedTarget(ctx, input, &output, "target file changed; call outline_file with output_profile=fingerprint_only"); err != nil {
			return output, err
		}
	}

	var backupPaths []string
	if shouldBackup(input.Backup) && plan.targetInfo != nil {
		backup, err := createSidecarBackup(plan.targetResolved, "target")
		backup = h.displayBackupResult(pathCtx, backup)
		output.BackupResults = append(output.BackupResults, backup)
		if backup.BackupPath != "" {
			backupPaths = append(backupPaths, backup.BackupPath)
		}
		if err != nil {
			output.BackupPaths = backupPaths
			return output, err
		}
	}
	output.BackupPaths = backupPaths

	if plan.targetInfo != nil {
		if err := h.recheckPlannedTarget(ctx, input, &output, "target fingerprint changed after backup"); err != nil {
			return output, err
		}
	}
	if input.TargetPrecondition.MustNotExist && fileExists(plan.targetResolved) {
		return output, fmt.Errorf("target_exists: create_new target already exists")
	}
	targetPlan.output = output
	if err := h.materializeBatchTargetPlan(ctx, &targetPlan); err != nil {
		return targetPlan.output, err
	}
	plan = targetPlan.plan
	output = targetPlan.output
	if err := h.recheckBatchSourceBeforeTargets(ctx, sourceFile, sourceFingerprint, batchOutput); err != nil {
		return output, err
	}
	if plan.targetInfo != nil {
		if err := h.recheckPlannedTarget(ctx, input, &output, "target changed before target write"); err != nil {
			return output, err
		}
	}
	if input.TargetPrecondition.MustNotExist && fileExists(plan.targetResolved) {
		return output, fmt.Errorf("target_exists: create_new target already exists")
	}
	if err := h.writeTargetForPlan(ctx, plan, input.Placement); err != nil {
		if input.TargetPrecondition.MustNotExist && os.IsExist(err) {
			err = fmt.Errorf("target_exists: create_new target already exists")
		}
		return output, err
	}
	output.BytesWritten = output.WouldWriteBytes
	targetAfter, err := h.inspectTextFileForRefactor(ctx, input.TargetFile)
	if err != nil {
		return output, targetPostWriteInspectError(err)
	}
	output.TargetFingerprintAfter = &targetAfter.fingerprint
	output.TargetFingerprintForNextWrite = &targetAfter.fingerprint
	h.applyTargetValidation(ctx, &output, plan, input.Placement, normalizeRedactionOrDefault(input.RedactionMode))
	output.Applied = true
	return output, nil
}

func (h *Handler) recheckPlannedTarget(ctx context.Context, input CopyRangesInput, output *RangeTransferOutput, message string) error {
	currentTarget, err := h.inspectTextFileForRefactorWriteEligible(ctx, input.TargetFile)
	if err != nil {
		return err
	}
	if !fingerprintMatches(currentTarget.fingerprint, *input.TargetPrecondition.Fingerprint) {
		output.ExpectedTargetFingerprint = input.TargetPrecondition.Fingerprint
		output.CurrentTargetFingerprint = &currentTarget.fingerprint
		return targetFingerprintMismatchError(message)
	}
	return nil
}

func carryBatchTargetSideEffects(output *BatchRangeTransferOutput, singleOutput RangeTransferOutput) {
	if output == nil {
		return
	}
	output.BackupPaths = append(output.BackupPaths, singleOutput.BackupPaths...)
	output.BackupResults = append(output.BackupResults, singleOutput.BackupResults...)
}

func skippedBatchTargetResult(targetFile string) BatchTargetResult {
	return BatchTargetResult{
		TargetFile:       targetFile,
		Status:           "skipped",
		Skipped:          true,
		Ranges:           []TransferRangeResult{},
		BackupPaths:      []string{},
		BoundaryWarnings: []BoundaryWarning{},
		Warnings:         []ToolWarning{},
	}
}

func (h *Handler) refreshCopyBatchSourceFingerprint(ctx context.Context, sourceFile string, sourceFingerprint FileFingerprint, output *BatchRangeTransferOutput) {
	sourceInfo, err := h.inspectTextFileForRefactor(ctx, sourceFile)
	if err != nil {
		return
	}
	if !fingerprintMatches(sourceInfo.fingerprint, sourceFingerprint) {
		markBatchSourceMismatch(output, sourceInfo.fingerprint)
		output.Warnings = append(output.Warnings, ToolWarning{
			Code:    "source_changed_after_target_writes",
			Message: "Source file changed after batch target writes; source_fingerprint_for_next_write is omitted.",
			File:    output.SourceFile,
		})
		output.WarningSummary = warningSummary(output.BatchWarnings, output.Warnings)
		addOmittedWarningCounts(output.WarningSummary, output.OmittedWarningCounts)
		return
	}
	output.SourceFingerprintForNextWrite = &sourceInfo.fingerprint
}

func (h *Handler) validateBatchBounds(targets []BatchRangeTarget) error {
	if len(targets) > h.config.BatchMaxTargets {
		return fmt.Errorf("batch_limit_exceeded: targets actual %d limit %d", len(targets), h.config.BatchMaxTargets)
	}
	totalRanges := 0
	for i, target := range targets {
		if len(target.Ranges) > h.config.BatchMaxRangesPerTarget {
			return fmt.Errorf("batch_limit_exceeded: target %d ranges actual %d limit %d", i, len(target.Ranges), h.config.BatchMaxRangesPerTarget)
		}
		totalRanges += len(target.Ranges)
	}
	if totalRanges > h.config.BatchMaxRangesPerCall {
		return fmt.Errorf("batch_limit_exceeded: total ranges actual %d limit %d", totalRanges, h.config.BatchMaxRangesPerCall)
	}
	return nil
}

func batchLimitExceededHint() *ActionHint {
	return &ActionHint{
		SafeToRetry:                false,
		RecommendedNextInputPolicy: "split_batch_explicitly",
		Reason:                     "aggregate planned write bytes exceeds MCP_BATCH_MAX_PLANNED_BYTES",
	}
}

func syncBatchByteAliases(output *BatchRangeTransferOutput) {
	if output == nil {
		return
	}
	output.WouldWriteTotalBytes = output.WouldWriteTargetBytes + output.WouldRewriteSourceBytes
	output.WouldWriteBytes = output.WouldWriteTotalBytes
	output.BytesWrittenTotalBytes = output.BytesWrittenTargetBytes + output.BytesRewrittenSourceBytes
	output.BytesWritten = output.BytesWrittenTotalBytes
}

func rangeSpanTotalBytes(spans []rangeSpan) int64 {
	var total int64
	for _, span := range spans {
		total += span.Span.End - span.Span.Start
	}
	return total
}

func batchTargetToCopyInput(sourceFile string, sourceFingerprint FileFingerprint, target BatchRangeTarget, dryRun bool) CopyRangesInput {
	return CopyRangesInput{
		SourceFile:         sourceFile,
		SourceFingerprint:  sourceFingerprint,
		Ranges:             target.Ranges,
		TargetFile:         target.TargetFile,
		TargetPrecondition: target.TargetPrecondition,
		Placement:          target.Placement,
		Joiner:             target.Joiner,
		Backup:             target.Backup,
		RedactionMode:      target.RedactionMode,
		DryRun:             dryRun,
	}
}

func batchTargetResultFromSingle(output RangeTransferOutput, status string, written, skipped, failed bool, err error) BatchTargetResult {
	result := BatchTargetResult{
		TargetFile:                    output.TargetFile,
		Status:                        status,
		Written:                       written,
		Skipped:                       skipped,
		Failed:                        failed,
		RequestedRanges:               output.RequestedRanges,
		Ranges:                        output.Ranges,
		WouldWriteBytes:               output.WouldWriteBytes,
		BytesWritten:                  output.BytesWritten,
		TargetFingerprintBefore:       output.TargetFingerprintBefore,
		TargetFingerprintAfter:        output.TargetFingerprintAfter,
		TargetFingerprintForNextWrite: output.TargetFingerprintForNextWrite,
		ExpectedTargetFingerprint:     output.ExpectedTargetFingerprint,
		CurrentTargetFingerprint:      output.CurrentTargetFingerprint,
		BackupRequested:               len(output.BackupResults) > 0,
		BackupPaths:                   output.BackupPaths,
		BoundaryWarnings:              output.BoundaryWarnings,
		Warnings:                      output.Warnings,
		DiffPreviews:                  output.DiffPreviews,
		JoinerEffect:                  output.JoinerEffect,
		BoundaryPreview:               output.BoundaryPreview,
		Validation:                    output.Validation,
	}
	if err != nil {
		result.Error = err.Error()
		result.ErrorCode = errorCodeFromMessage(err.Error())
		result.FailedAt = status
	}
	return result
}

func applySingleSourceDiagnosticsToBatch(output *BatchRangeTransferOutput, singleOutput RangeTransferOutput) {
	if output == nil {
		return
	}
	if singleOutput.SourceFingerprintBefore != nil && output.SourceFingerprintBefore == nil {
		output.SourceFingerprintBefore = singleOutput.SourceFingerprintBefore
	}
	if singleOutput.SourceFingerprintCheckedAtWrite != nil {
		output.SourceFingerprintCheckedAtWrite = singleOutput.SourceFingerprintCheckedAtWrite
	}
	if singleOutput.CurrentSourceFingerprint != nil {
		output.CurrentSourceFingerprint = singleOutput.CurrentSourceFingerprint
		output.SourceFingerprintForNextWrite = nil
		return
	}
	if singleOutput.SourceFingerprintForNextWrite != nil {
		output.SourceFingerprintForNextWrite = singleOutput.SourceFingerprintForNextWrite
	}
}

func (h *Handler) applyBatchSourceMove(ctx context.Context, pathCtx PathContext, sourceFile string, sourceFingerprint FileFingerprint, sourceResolved string, sourceBackup BackupSpec, ranges []SourceLineRange, sourceSpans []rangeSpan, output *BatchRangeTransferOutput) error {
	return h.applyBatchSourceMoveWithRedaction(ctx, pathCtx, sourceFile, sourceFingerprint, sourceResolved, sourceBackup, redactionAuto, ranges, sourceSpans, output)
}

func (h *Handler) applyBatchSourceMoveWithRedaction(ctx context.Context, pathCtx PathContext, sourceFile string, sourceFingerprint FileFingerprint, sourceResolved string, sourceBackup BackupSpec, redactionMode string, ranges []SourceLineRange, sourceSpans []rangeSpan, output *BatchRangeTransferOutput) error {
	sourceInfo, err := h.inspectTextFileForRefactorWriteEligible(ctx, sourceFile)
	if err != nil {
		return err
	}
	if !fingerprintMatches(sourceInfo.fingerprint, sourceFingerprint) {
		markBatchSourceMismatch(output, sourceInfo.fingerprint)
		return fmt.Errorf("source_fingerprint_mismatch: source changed after target writes")
	}
	if shouldBackup(sourceBackup) {
		backup, err := createSidecarBackup(sourceResolved, "source")
		backup = h.displayBackupResult(pathCtx, backup)
		output.BackupResults = append(output.BackupResults, backup)
		if backup.BackupPath != "" {
			output.BackupPaths = append(output.BackupPaths, backup.BackupPath)
		}
		if err != nil {
			return err
		}
	}
	sourceRecheckAfterBackup, err := h.inspectTextFileForRefactorWriteEligible(ctx, sourceFile)
	if err != nil {
		return err
	}
	if !fingerprintMatches(sourceRecheckAfterBackup.fingerprint, sourceFingerprint) {
		markBatchSourceMismatch(output, sourceRecheckAfterBackup.fingerprint)
		return fmt.Errorf("source_fingerprint_mismatch: source changed after source backup")
	}
	rangeSpans := sourceSpans
	if len(rangeSpans) == 0 {
		sourceScan, err := scanLineSpans(ctx, sourceResolved, ranges, nil)
		if err != nil {
			return err
		}
		rangeSpans = sourceScan.RangeSpans
	}
	if err := writeFileReplace(sourceResolved, func(w io.Writer) error {
		return copyFileExceptSpans(ctx, w, sourceResolved, spansFromRangeSpans(rangeSpans))
	}); err != nil {
		return err
	}
	output.RemovedSourceRanges = ranges
	output.RemovedSourceLines = output.WouldRemoveSourceLines
	output.BytesRewrittenSourceBytes = output.WouldRewriteSourceBytes
	syncBatchByteAliases(output)
	sourceAfter, err := h.inspectTextFileForRefactor(ctx, sourceFile)
	if err != nil {
		return sourcePostWriteInspectError(err)
	}
	output.SourceFingerprintAfter = &sourceAfter.fingerprint
	output.SourceFingerprintForNextWrite = &sourceAfter.fingerprint
	h.applyBatchSourceValidation(ctx, output, sourceResolved, redactionMode, ranges)
	return nil
}

func markBatchSourceMismatch(output *BatchRangeTransferOutput, current FileFingerprint) {
	if output == nil {
		return
	}
	output.CurrentSourceFingerprint = &current
	output.SourceFingerprintForNextWrite = nil
}

func sortedRanges(ranges []SourceLineRange) []SourceLineRange {
	out := append([]SourceLineRange(nil), ranges...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartLine == out[j].StartLine {
			return out[i].EndLine < out[j].EndLine
		}
		return out[i].StartLine < out[j].StartLine
	})
	return out
}

func batchPartialState(output BatchRangeTransferOutput, phase string, err error) *BatchPartialState {
	errorCode := errorCodeFromMessage(err.Error())
	sourceModified := outputOperationIsMove(output.Operation) && (output.SourceFingerprintAfter != nil || output.RemovedSourceLines > 0)
	return &BatchPartialState{
		Operation:                  output.Operation,
		Phase:                      phase,
		SourceFile:                 output.SourceFile,
		SourceModifiedByTool:       sourceModified,
		SourceFingerprintBefore:    output.SourceFingerprintBefore,
		SourceFingerprintAfter:     output.SourceFingerprintAfter,
		CurrentSourceFingerprint:   output.CurrentSourceFingerprint,
		TargetResults:              output.TargetResults,
		BackupPaths:                output.BackupPaths,
		BackupResults:              output.BackupResults,
		RecommendedNextTool:        partialRecommendedTool(errorCode, ""),
		RecommendedNextInputPolicy: "inspect_modified_files_before_retry",
		RecommendedNextInput:       batchPartialRecommendedInput(output, errorCode),
		RecoveryHint:               "Inspect written target fingerprints and refresh source outline before retrying.",
		ErrorCode:                  errorCode,
		Error:                      err.Error(),
	}
}

func batchPartialRecommendedInput(output BatchRangeTransferOutput, errorCode string) map[string]any {
	if output.SourceFile == "" {
		return nil
	}
	if errorCode == "target_missing" {
		for i := len(output.TargetResults) - 1; i >= 0; i-- {
			result := output.TargetResults[i]
			if result.Failed && result.TargetFile != "" {
				return map[string]any{
					"target_path": result.TargetFile,
				}
			}
		}
	}
	if errorCode == "source_post_write_inspect_failed" {
		return map[string]any{
			"target_file":    output.SourceFile,
			"output_profile": outlineProfileFingerprintOnly,
		}
	}
	if errorCode == "target_fingerprint_mismatch" || errorCode == "target_exists" || errorCode == "target_post_write_inspect_failed" {
		for i := len(output.TargetResults) - 1; i >= 0; i-- {
			result := output.TargetResults[i]
			if result.Failed && result.TargetFile != "" {
				return map[string]any{
					"target_file":    result.TargetFile,
					"output_profile": outlineProfileFingerprintOnly,
				}
			}
		}
	}
	if output.CurrentSourceFingerprint != nil || len(output.TargetsWritten) == 0 {
		return map[string]any{
			"target_file":    output.SourceFile,
			"output_profile": outlineProfileOutline,
		}
	}
	return map[string]any{
		"target_file":    output.TargetsWritten[0],
		"output_profile": outlineProfileFingerprintOnly,
	}
}

const batchWarningLimit = 50

func copyBatchRangeWarnings(sourceFile string, targets []BatchRangeTarget) ([]ToolWarning, bool, map[string]int) {
	type seenRange struct {
		targetIndex int
		rangeIndex  int
		r           SourceLineRange
	}
	seen := []seenRange{}
	warnings := []ToolWarning{}
	omitted := 0
	omittedByCode := map[string]int{}
	for targetIndex, target := range targets {
		for rangeIndex, r := range target.Ranges {
			for _, prior := range seen {
				if rangesOverlap(r, prior.r) {
					code := "copy_batch_overlapping_source_ranges"
					if r.StartLine == prior.r.StartLine && r.EndLine == prior.r.EndLine {
						code = "copy_batch_duplicate_source_range"
					}
					warning := ToolWarning{
						Code: code,
						Message: fmt.Sprintf("source range %d-%d for target %d overlaps range %d-%d for target %d; copy batch allows this but output may duplicate text",
							r.StartLine, r.EndLine, targetIndex, prior.r.StartLine, prior.r.EndLine, prior.targetIndex),
						File: sourceFile,
						Line: r.StartLine,
					}
					if len(warnings) < batchWarningLimit {
						warnings = append(warnings, warning)
					} else {
						omitted++
						omittedByCode[code]++
					}
					break
				}
			}
			seen = append(seen, seenRange{targetIndex: targetIndex, rangeIndex: rangeIndex, r: r})
		}
	}
	if omitted > 0 {
		warnings = append(warnings, ToolWarning{
			Code:    "batch_warnings_truncated",
			Message: fmt.Sprintf("%d additional batch warnings were omitted to keep copy_ranges_batch output bounded.", omitted),
			File:    sourceFile,
		})
	}
	return warnings, omitted > 0, omittedByCode
}

func rangesOverlap(a, b SourceLineRange) bool {
	return a.StartLine <= b.EndLine && b.StartLine <= a.EndLine
}

func warningSummary(batchWarnings, warnings []ToolWarning) *WarningSummary {
	summary := &WarningSummary{
		ByCode: map[string]int{},
	}
	for _, warning := range batchWarnings {
		if !strings.HasSuffix(warning.Code, "_truncated") {
			summary.TotalWarnings++
			summary.ByCode[warning.Code]++
		}
	}
	for _, warning := range warnings {
		if !strings.HasSuffix(warning.Code, "_truncated") {
			summary.TotalWarnings++
			summary.ByCode[warning.Code]++
		}
	}
	if summary.TotalWarnings == 0 {
		return nil
	}
	return summary
}

func addOmittedWarningCounts(summary *WarningSummary, omittedByCode map[string]int) {
	if summary == nil {
		return
	}
	for code, count := range omittedByCode {
		if count <= 0 {
			continue
		}
		summary.TotalWarnings += count
		summary.ByCode[code] += count
	}
}
