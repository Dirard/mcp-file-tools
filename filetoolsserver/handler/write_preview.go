package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

func normalizeRedactionOrDefault(mode string) string {
	normalized, err := normalizeRedactionMode(mode)
	if err != nil {
		return redactionOff
	}
	return normalized
}

func normalizeJoinerOrNone(joiner string) string {
	normalized, err := normalizeJoinerName(joiner)
	if err != nil {
		return "none"
	}
	return normalized
}

func normalizeJoinerName(joiner string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(joiner)) {
	case "", "none":
		return "none", nil
	case "single_newline":
		return "single_newline", nil
	case "blank_line":
		return "blank_line", nil
	default:
		return "", fmt.Errorf("invalid_joiner: unsupported joiner %q; use none, single_newline, or blank_line", joiner)
	}
}

func applyPlacementJoiner(ctx context.Context, plan *singleTransferPlan, placement TargetPlacement, requestedJoiner string, maxPreviewChars int, mode string) error {
	if plan == nil {
		return nil
	}
	normalized, err := normalizeJoinerName(requestedJoiner)
	if err != nil {
		return err
	}
	effect := plan.joinerEffect
	effect.Requested = requestedJoiner
	effect.Normalized = normalized
	if normalized == "none" || plan.targetInfo == nil || len(plan.payload) == 0 {
		plan.joinerEffect = effect
		plan.boundaryPreview = boundaryPreviewForPlan(ctx, *plan, placement, mode, maxPreviewChars)
		return nil
	}
	style, err := dominantNewlineStyle(ctx, plan.targetResolved)
	if err != nil {
		return err
	}
	targetBytes, err := os.ReadFile(plan.targetResolved)
	if err != nil {
		return err
	}
	before, after, err := targetBoundaryBytes(ctx, plan.targetResolved, targetBytes, placement)
	if err != nil {
		return err
	}
	prefix := joinerSeparator(before, plan.payload, normalized, style)
	suffix := joinerSeparator(plan.payload, after, normalized, style)
	targetBoundaries := []JoinerBoundaryEffect{}
	if len(prefix) > 0 {
		effect.LeftEndedWithNewline = endsWithNewline(before)
		effect.RightStartedWithNewline = startsWithNewline(plan.payload)
		effect.InsertedNewlinesBetweenBlocks += countNewlines(prefix)
	}
	if len(before) > 0 && len(plan.payload) > 0 {
		targetBoundaries = append(targetBoundaries, joinerBoundaryEffect(targetJoinerBoundaryName(placement, "before"), before, plan.payload, countNewlines(prefix), normalized))
	}
	if len(suffix) > 0 {
		effect.InsertedNewlinesBetweenBlocks += countNewlines(suffix)
	}
	if len(plan.payload) > 0 && len(after) > 0 {
		targetBoundaries = append(targetBoundaries, joinerBoundaryEffect(targetJoinerBoundaryName(placement, "after"), plan.payload, after, countNewlines(suffix), normalized))
	}
	effect.TargetBoundaries = targetBoundaries
	effect.TargetBoundary = primaryJoinerBoundary(targetBoundaries)
	if len(prefix) > 0 || len(suffix) > 0 {
		adjusted := make([]byte, 0, len(prefix)+len(plan.payload)+len(suffix))
		adjusted = append(adjusted, prefix...)
		adjusted = append(adjusted, plan.payload...)
		adjusted = append(adjusted, suffix...)
		plan.payload = adjusted
		plan.payloadSize = int64(len(adjusted))
	}
	plan.joinerEffect = effect
	plan.boundaryPreview = boundaryPreviewForPlan(ctx, *plan, placement, mode, maxPreviewChars)
	return nil
}

func joinerBoundaryEffect(boundary string, left, right []byte, insertedNewlines int, normalized string) JoinerBoundaryEffect {
	leftNewlines := trailingNewlineCount(left)
	rightNewlines := leadingNewlineCount(right)
	totalNewlines := leftNewlines + insertedNewlines + rightNewlines
	effect := JoinerBoundaryEffect{
		Boundary:                boundary,
		ExistingLeftNewlines:    leftNewlines,
		ExistingRightNewlines:   rightNewlines,
		InsertedNewlines:        insertedNewlines,
		VisualBlankLinesBetween: maxInt(0, totalNewlines-1),
		LeftEndedWithNewline:    leftNewlines > 0,
		RightStartedWithNewline: rightNewlines > 0,
	}
	if normalized == "blank_line" && effect.VisualBlankLinesBetween > 1 {
		effect.WarningCodes = append(effect.WarningCodes, "blank_line_joiner_extra_visual_blank_lines")
	}
	return effect
}

func primaryJoinerBoundary(boundaries []JoinerBoundaryEffect) *JoinerBoundaryEffect {
	if len(boundaries) == 0 {
		return nil
	}
	for i := range boundaries {
		if len(boundaries[i].WarningCodes) > 0 {
			return &boundaries[i]
		}
	}
	return &boundaries[0]
}

func targetJoinerBoundaryName(placement TargetPlacement, side string) string {
	switch placement.Mode {
	case placementAppend:
		return "target_end_to_insert_start"
	case placementPrepend:
		return "insert_end_to_target_start"
	case placementInsertBeforeLine:
		if side == "before" {
			return "target_before_insert_to_insert_start"
		}
		return "insert_end_to_target_after_insert"
	case placementReplaceRange:
		if side == "before" {
			return "target_before_replaced_range_to_insert_start"
		}
		return "insert_end_to_target_after_replaced_range"
	default:
		return "target_boundary"
	}
}

func targetBoundaryBytes(ctx context.Context, target string, targetBytes []byte, placement TargetPlacement) ([]byte, []byte, error) {
	switch placement.Mode {
	case placementAppend:
		return targetBytes, nil, nil
	case placementPrepend:
		return nil, targetBytes, nil
	case placementInsertBeforeLine:
		offset, err := targetInsertOffset(ctx, target, placement.Line)
		if err != nil {
			return nil, nil, err
		}
		return targetBytes[:offset], targetBytes[offset:], nil
	case placementReplaceRange:
		if placement.Range == nil {
			return nil, nil, fmt.Errorf("invalid_placement: replace_range placement requires range")
		}
		scan, err := scanLineSpans(ctx, target, []SourceLineRange{*placement.Range}, nil)
		if err != nil {
			return nil, nil, err
		}
		span := scan.RangeSpans[0].Span
		return targetBytes[:span.Start], targetBytes[span.End:], nil
	default:
		return nil, nil, fmt.Errorf("invalid_placement: unsupported placement mode %q", placement.Mode)
	}
}

func joinerSeparator(left, right []byte, normalized, style string) []byte {
	if len(left) == 0 || len(right) == 0 {
		return nil
	}
	desired := 0
	switch normalized {
	case "single_newline":
		desired = 1
	case "blank_line":
		desired = 2
	default:
		return nil
	}
	existing := trailingNewlineCount(left) + leadingNewlineCount(right)
	needed := desired - existing
	if needed <= 0 {
		return nil
	}
	var b strings.Builder
	for i := 0; i < needed; i++ {
		b.WriteString(style)
	}
	return []byte(b.String())
}

func trailingNewlineCount(data []byte) int {
	count := 0
	for i := len(data) - 1; i >= 0; i-- {
		if data[i] == '\n' {
			count++
			continue
		}
		if data[i] == '\r' {
			continue
		}
		break
	}
	return count
}

func leadingNewlineCount(data []byte) int {
	count := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			count++
			continue
		}
		if data[i] == '\r' {
			continue
		}
		break
	}
	return count
}

func endsWithNewline(data []byte) bool {
	return trailingNewlineCount(data) > 0
}

func startsWithNewline(data []byte) bool {
	return leadingNewlineCount(data) > 0
}

func countNewlines(data []byte) int {
	return bytes.Count(data, []byte{'\n'})
}

func buildTargetAfterBytes(ctx context.Context, plan singleTransferPlan, placement TargetPlacement) ([]byte, []byte, error) {
	if placement.Mode == placementCreateNew {
		return nil, append([]byte(nil), plan.payload...), nil
	}
	before, err := os.ReadFile(plan.targetResolved)
	if err != nil {
		return nil, nil, err
	}
	var after bytes.Buffer
	if err := writeExistingTargetWithPlacement(ctx, &after, plan.targetResolved, placement, plan.payload); err != nil {
		return nil, nil, err
	}
	return before, after.Bytes(), nil
}

func buildSourceRemovalAfterBytes(ctx context.Context, plan singleTransferPlan) ([]byte, []byte, error) {
	before, err := os.ReadFile(plan.sourceResolved)
	if err != nil {
		return nil, nil, err
	}
	var after bytes.Buffer
	if err := copyFileExceptSpans(ctx, &after, plan.sourceResolved, spansFromRangeSpans(plan.sourceScan.RangeSpans)); err != nil {
		return nil, nil, err
	}
	return before, after.Bytes(), nil
}

func diffPreviewForBytes(role, oldLabel, newLabel string, oldBytes, newBytes []byte, maxBytes int, mode string, risky bool) DiffPreview {
	text, stats, truncated, changed := unifiedDiffPreview(oldLabel, newLabel, oldBytes, newBytes, maxBytes, mode, risky)
	return DiffPreview{
		Role:          role,
		Format:        "unified",
		Text:          text,
		Truncated:     truncated,
		Stats:         stats,
		Redacted:      changed,
		RedactionMode: mode,
		PathMode:      "projected",
	}
}

func unifiedDiffPreview(oldLabel, newLabel string, oldBytes, newBytes []byte, maxBytes int, mode string, risky bool) (string, DiffPreviewStats, bool, bool) {
	oldLines := splitDiffLines(string(oldBytes))
	newLines := splitDiffLines(string(newBytes))
	ops := lineDiffOps(oldLines, newLines)
	stats := diffStatsFromOps(ops)
	var b strings.Builder
	redacted := false
	b.WriteString("--- ")
	b.WriteString(oldLabel)
	b.WriteString("\n+++ ")
	b.WriteString(newLabel)
	b.WriteByte('\n')
	if stats.FilesChanged == 0 {
		return b.String(), stats, false, redacted
	}
	hunks := diffHunks(ops, 3)
	truncated := false
	for i, hunk := range hunks {
		text := renderDiffHunk(hunk)
		if value, changed := redactString(text, mode, risky); changed {
			text = value
			redacted = true
		}
		if maxBytes > 0 && b.Len()+len([]byte(text)) > maxBytes {
			truncated = true
			stats.HunksOmitted += len(hunks) - i
			if stats.HunksReturned == 0 {
				remaining := maxBytes - b.Len()
				if remaining > 0 {
					clipped, _ := truncateDisplayPrefix(text, remaining, "... [TRUNCATED]\n")
					b.WriteString(clipped)
					if !strings.HasSuffix(b.String(), "\n") {
						b.WriteByte('\n')
					}
				}
				stats.HunksReturned = 1
			}
			break
		}
		b.WriteString(text)
		stats.HunksReturned++
	}
	return b.String(), stats, truncated, redacted
}

func splitDiffLines(text string) []string {
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}

type diffOp struct {
	Kind    byte
	OldLine int
	NewLine int
	Text    string
}

type diffHunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Ops      []diffOp
}

func lineDiffOps(oldLines, newLines []string) []diffOp {
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	oldEnd := len(oldLines)
	newEnd := len(newLines)
	for oldEnd > prefix && newEnd > prefix && oldLines[oldEnd-1] == newLines[newEnd-1] {
		oldEnd--
		newEnd--
	}
	ops := make([]diffOp, 0, len(oldLines)+len(newLines))
	for i := 0; i < prefix; i++ {
		ops = append(ops, diffOp{Kind: ' ', OldLine: i + 1, NewLine: i + 1, Text: oldLines[i]})
	}
	oldMid := oldLines[prefix:oldEnd]
	newMid := newLines[prefix:newEnd]
	if len(oldMid)*len(newMid) <= 200000 {
		ops = append(ops, lcsDiffOps(oldMid, newMid, prefix, prefix)...)
	} else {
		for i, line := range oldMid {
			ops = append(ops, diffOp{Kind: '-', OldLine: prefix + i + 1, Text: line})
		}
		for i, line := range newMid {
			ops = append(ops, diffOp{Kind: '+', NewLine: prefix + i + 1, Text: line})
		}
	}
	for oldIdx, newIdx := oldEnd, newEnd; oldIdx < len(oldLines) && newIdx < len(newLines); oldIdx, newIdx = oldIdx+1, newIdx+1 {
		ops = append(ops, diffOp{Kind: ' ', OldLine: oldIdx + 1, NewLine: newIdx + 1, Text: oldLines[oldIdx]})
	}
	return ops
}

func lcsDiffOps(oldLines, newLines []string, oldOffset, newOffset int) []diffOp {
	rows := len(oldLines) + 1
	cols := len(newLines) + 1
	dp := make([]int, rows*cols)
	at := func(i, j int) *int { return &dp[i*cols+j] }
	for i := len(oldLines) - 1; i >= 0; i-- {
		for j := len(newLines) - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				*at(i, j) = *at(i+1, j+1) + 1
			} else if *at(i+1, j) >= *at(i, j+1) {
				*at(i, j) = *at(i+1, j)
			} else {
				*at(i, j) = *at(i, j+1)
			}
		}
	}
	ops := make([]diffOp, 0, len(oldLines)+len(newLines))
	i, j := 0, 0
	for i < len(oldLines) && j < len(newLines) {
		if oldLines[i] == newLines[j] {
			ops = append(ops, diffOp{Kind: ' ', OldLine: oldOffset + i + 1, NewLine: newOffset + j + 1, Text: oldLines[i]})
			i++
			j++
		} else if *at(i+1, j) >= *at(i, j+1) {
			ops = append(ops, diffOp{Kind: '-', OldLine: oldOffset + i + 1, Text: oldLines[i]})
			i++
		} else {
			ops = append(ops, diffOp{Kind: '+', NewLine: newOffset + j + 1, Text: newLines[j]})
			j++
		}
	}
	for ; i < len(oldLines); i++ {
		ops = append(ops, diffOp{Kind: '-', OldLine: oldOffset + i + 1, Text: oldLines[i]})
	}
	for ; j < len(newLines); j++ {
		ops = append(ops, diffOp{Kind: '+', NewLine: newOffset + j + 1, Text: newLines[j]})
	}
	return ops
}

func diffStatsFromOps(ops []diffOp) DiffPreviewStats {
	stats := DiffPreviewStats{}
	for _, op := range ops {
		switch op.Kind {
		case '+':
			stats.LinesAdded++
		case '-':
			stats.LinesRemoved++
		}
	}
	if stats.LinesAdded > 0 || stats.LinesRemoved > 0 {
		stats.FilesChanged = 1
	}
	return stats
}

func diffHunks(ops []diffOp, contextLines int) []diffHunk {
	changeIndexes := []int{}
	for i, op := range ops {
		if op.Kind != ' ' {
			changeIndexes = append(changeIndexes, i)
		}
	}
	if len(changeIndexes) == 0 {
		return nil
	}
	hunks := []diffHunk{}
	start := maxInt(0, changeIndexes[0]-contextLines)
	end := minInt(len(ops), changeIndexes[0]+contextLines+1)
	for _, idx := range changeIndexes[1:] {
		nextStart := maxInt(0, idx-contextLines)
		nextEnd := minInt(len(ops), idx+contextLines+1)
		if nextStart <= end {
			end = maxInt(end, nextEnd)
			continue
		}
		hunks = append(hunks, makeDiffHunk(ops[start:end]))
		start = nextStart
		end = nextEnd
	}
	hunks = append(hunks, makeDiffHunk(ops[start:end]))
	return hunks
}

func makeDiffHunk(ops []diffOp) diffHunk {
	h := diffHunk{Ops: ops}
	for _, op := range ops {
		if op.Kind != '+' {
			if h.OldStart == 0 {
				h.OldStart = op.OldLine
			}
			h.OldCount++
		}
		if op.Kind != '-' {
			if h.NewStart == 0 {
				h.NewStart = op.NewLine
			}
			h.NewCount++
		}
	}
	if h.OldStart == 0 {
		h.OldStart = maxInt(1, firstNewLine(ops))
	}
	if h.NewStart == 0 {
		h.NewStart = maxInt(1, firstOldLine(ops))
	}
	return h
}

func firstNewLine(ops []diffOp) int {
	for _, op := range ops {
		if op.NewLine > 0 {
			return op.NewLine
		}
	}
	return 1
}

func firstOldLine(ops []diffOp) int {
	for _, op := range ops {
		if op.OldLine > 0 {
			return op.OldLine
		}
	}
	return 1
}

func renderDiffHunk(h diffHunk) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", h.OldStart, h.OldCount, h.NewStart, h.NewCount))
	for _, op := range h.Ops {
		b.WriteByte(op.Kind)
		b.WriteString(op.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

func boundaryPreviewForPlan(ctx context.Context, plan singleTransferPlan, placement TargetPlacement, mode string, maxChars int) BoundaryPreview {
	preview := BoundaryPreview{
		TargetFile:    plan.targetDisplay,
		Placement:     placement.Mode,
		RedactionMode: mode,
	}
	var before, after []byte
	if placement.Mode == placementCreateNew {
		before = nil
		after = nil
	} else {
		targetBytes, err := os.ReadFile(plan.targetResolved)
		if err != nil {
			return preview
		}
		before, after, err = targetBoundaryBytes(ctx, plan.targetResolved, targetBytes, placement)
		if err != nil {
			return preview
		}
	}
	rawBefore := string(before)
	rawBetween := string(plan.payload)
	rawAfter := string(after)
	combined := rawBefore + "\n---PAYLOAD---\n" + rawBetween + "\n---AFTER---\n" + rawAfter
	redacted, changed := redactString(combined, mode, isRiskyContentPath(plan.targetDisplay) || isRiskyContentPath(plan.sourceDisplay))
	if changed {
		preview.Redacted = true
		parts := strings.Split(redacted, "\n---PAYLOAD---\n")
		if len(parts) == 2 {
			rawBefore = parts[0]
			tail := strings.SplitN(parts[1], "\n---AFTER---\n", 2)
			if len(tail) == 2 {
				rawBetween = tail[0]
				rawAfter = tail[1]
			}
		}
	}
	partLimit := maxInt(1, maxChars/3)
	var beforeTruncated, betweenTruncated, afterTruncated bool
	preview.Before, beforeTruncated = boundedPreviewStringSuffix(rawBefore, partLimit)
	preview.Between, betweenTruncated = boundedPreviewString(rawBetween, partLimit)
	preview.After, afterTruncated = boundedPreviewString(rawAfter, partLimit)
	preview.Truncated = beforeTruncated || betweenTruncated || afterTruncated
	return preview
}

func boundedPreviewString(text string, maxChars int) (string, bool) {
	return truncateDisplayPrefix(text, maxChars, "\n... [TRUNCATED]")
}

func boundedPreviewStringSuffix(text string, maxChars int) (string, bool) {
	return truncateDisplaySuffix(text, maxChars, "[TRUNCATED] ...\n")
}

func readBackWindowForFile(ctx context.Context, h *Handler, file, displayFile string, r LineRange, mode string, risky bool) (ReadBackWindow, error) {
	input := ReadFileInput{TargetFile: file, StartLine: &r.Start, EndLine: &r.End}
	_, out, _ := h.HandleReadFile(ctx, nil, input)
	if out.Error != "" {
		return ReadBackWindow{}, errors.New(out.Error)
	}
	text := out.Text
	redacted, changed := redactString(text, mode, risky)
	return ReadBackWindow{
		File:          displayFile,
		Range:         r,
		Text:          redacted,
		Truncated:     false,
		Redacted:      changed,
		RedactionMode: mode,
	}, nil
}

func (h *Handler) diffPreviewsForSinglePlan(ctx context.Context, plan singleTransferPlan, placement TargetPlacement, operation, mode string) []DiffPreview {
	maxBytes := h.config.DiffPreviewMaxBytes
	riskyTarget := isRiskyContentPath(plan.targetDisplay) || isRiskyContentPath(plan.sourceDisplay)
	previews := []DiffPreview{}
	targetBefore, targetAfter, err := buildTargetAfterBytes(ctx, plan, placement)
	if err == nil {
		oldLabel := plan.targetDisplay
		if placement.Mode == placementCreateNew {
			oldLabel = "<new file>"
		}
		previews = append(previews, diffPreviewForBytes("target", oldLabel, plan.targetDisplay, targetBefore, targetAfter, maxBytes, mode, riskyTarget))
	}
	if operation == operationMove {
		sourceBefore, sourceAfter, err := buildSourceRemovalAfterBytes(ctx, plan)
		if err == nil {
			previews = append(previews, diffPreviewForBytes("source_removal", plan.sourceDisplay, plan.sourceDisplay, sourceBefore, sourceAfter, maxBytes, mode, isRiskyContentPath(plan.sourceDisplay)))
		}
	}
	return previews
}

func (h *Handler) applyTargetValidation(ctx context.Context, output *RangeTransferOutput, plan singleTransferPlan, placement TargetPlacement, mode string) {
	if output == nil {
		return
	}
	output.Validation.RedactionMode = mode
	output.Validation.TargetReadBack = []ReadBackWindow{}
	if output.TargetFingerprintAfter == nil || output.TargetFingerprintAfter.LineCount == 0 {
		mergeWriteValidationStatus(&output.Validation, "applied_and_verified")
		return
	}
	r := targetValidationRange(*output.TargetFingerprintAfter, plan, placement, h.config.ReadBackMaxLines)
	window, err := readBackWindowForFile(ctx, h, plan.targetResolved, plan.targetDisplay, r, mode, isRiskyContentPath(plan.targetDisplay))
	if err != nil {
		if mergeWriteValidationStatus(&output.Validation, "applied_validation_failed") {
			output.Validation.ErrorCode = "post_write_validation_failed"
			output.Validation.Error = "post_write_validation_failed: " + err.Error()
		}
		return
	}
	mergeWriteValidationStatus(&output.Validation, "applied_and_verified")
	output.Validation.TargetReadBack = []ReadBackWindow{window}
	if r.End-r.Start+1 >= h.config.ReadBackMaxLines && output.TargetFingerprintAfter.LineCount > h.config.ReadBackMaxLines {
		if mergeWriteValidationStatus(&output.Validation, "applied_validation_truncated") {
			addReadBackContinuation(&output.Validation, "read_file", plan.targetDisplay, r.End+1, h.config.ReadBackMaxLines)
		}
	}
}

func (h *Handler) applySourceValidation(ctx context.Context, output *RangeTransferOutput, plan singleTransferPlan, mode string) {
	if output == nil {
		return
	}
	output.Validation.RedactionMode = mode
	if output.Validation.TargetReadBack == nil {
		output.Validation.TargetReadBack = []ReadBackWindow{}
	}
	if output.SourceFingerprintAfter == nil || output.SourceFingerprintAfter.LineCount == 0 {
		mergeWriteValidationStatus(&output.Validation, "applied_and_verified")
		return
	}
	r := sourceValidationRange(*output.SourceFingerprintAfter, plan, h.config.ReadBackMaxLines)
	window, err := readBackWindowForFile(ctx, h, plan.sourceResolved, plan.sourceDisplay, r, mode, isRiskyContentPath(plan.sourceDisplay))
	if err != nil {
		if mergeWriteValidationStatus(&output.Validation, "applied_validation_failed") {
			output.Validation.ErrorCode = "post_write_validation_failed"
			output.Validation.Error = "post_write_validation_failed: " + err.Error()
		}
		return
	}
	output.Validation.SourceReadBack = []ReadBackWindow{window}
	mergeWriteValidationStatus(&output.Validation, "applied_and_verified")
	if r.End-r.Start+1 >= h.config.ReadBackMaxLines && output.SourceFingerprintAfter.LineCount > h.config.ReadBackMaxLines {
		if mergeWriteValidationStatus(&output.Validation, "applied_validation_truncated") {
			addReadBackContinuation(&output.Validation, "read_file", plan.sourceDisplay, r.End+1, h.config.ReadBackMaxLines)
		}
	}
}

func mergeWriteValidationStatus(validation *WriteValidation, status string) bool {
	if validation == nil {
		return false
	}
	if validation.Status == "" || writeValidationStatusRank(status) > writeValidationStatusRank(validation.Status) {
		validation.Status = status
		return true
	}
	return false
}

func writeValidationStatusRank(status string) int {
	switch status {
	case "applied_validation_failed":
		return 3
	case "applied_validation_truncated":
		return 2
	case "applied_and_verified":
		return 1
	default:
		return 0
	}
}

func batchSourceDiffPreviews(ctx context.Context, sourceResolved, sourceDisplay string, spans []rangeSpan, maxBytes int, mode string) []DiffPreview {
	before, err := os.ReadFile(sourceResolved)
	if err != nil {
		return nil
	}
	var after bytes.Buffer
	if err := copyFileExceptSpans(ctx, &after, sourceResolved, spansFromRangeSpans(spans)); err != nil {
		return nil
	}
	return []DiffPreview{
		diffPreviewForBytes("source_rewrite", sourceDisplay, sourceDisplay, before, after.Bytes(), maxBytes, mode, isRiskyContentPath(sourceDisplay)),
	}
}

func (h *Handler) applyBatchSourceValidation(ctx context.Context, output *BatchRangeTransferOutput, sourceResolved string, mode string, ranges []SourceLineRange) {
	if output == nil {
		return
	}
	if output.SourceValidation == nil {
		output.SourceValidation = &WriteValidation{TargetReadBack: []ReadBackWindow{}}
	}
	output.SourceValidation.RedactionMode = mode
	if output.SourceFingerprintAfter == nil || output.SourceFingerprintAfter.LineCount == 0 {
		output.SourceValidation.Status = "applied_and_verified"
		return
	}
	r := batchSourceValidationRange(*output.SourceFingerprintAfter, ranges, h.config.ReadBackMaxLines)
	window, err := readBackWindowForFile(ctx, h, sourceResolved, output.SourceFile, r, mode, isRiskyContentPath(output.SourceFile))
	if err != nil {
		output.SourceValidation.Status = "applied_validation_failed"
		output.SourceValidation.ErrorCode = "post_write_validation_failed"
		output.SourceValidation.Error = "post_write_validation_failed: " + err.Error()
		return
	}
	output.SourceValidation.Status = "applied_and_verified"
	output.SourceValidation.SourceReadBack = []ReadBackWindow{window}
	if r.End-r.Start+1 >= h.config.ReadBackMaxLines && output.SourceFingerprintAfter.LineCount > h.config.ReadBackMaxLines {
		addReadBackContinuation(output.SourceValidation, "read_file", output.SourceFile, r.End+1, h.config.ReadBackMaxLines)
		output.SourceValidation.Status = "applied_validation_truncated"
	}
}

func targetValidationRange(fp FileFingerprint, plan singleTransferPlan, placement TargetPlacement, maxLines int) LineRange {
	total := maxInt(1, fp.LineCount)
	payloadLines := payloadDisplayLineCount(plan.payload)
	start := 1
	switch placement.Mode {
	case placementAppend:
		start = maxInt(1, total-payloadLines-2)
	case placementPrepend, placementCreateNew:
		start = 1
	case placementInsertBeforeLine:
		start = maxInt(1, placement.Line-2)
	case placementReplaceRange:
		if placement.Range != nil {
			start = maxInt(1, placement.Range.StartLine-2)
		}
	}
	end := minInt(total, start+maxInt(1, maxLines)-1)
	return LineRange{Start: start, End: end}
}

func sourceValidationRange(fp FileFingerprint, plan singleTransferPlan, maxLines int) LineRange {
	total := maxInt(1, fp.LineCount)
	start := 1
	if len(plan.sourceScan.RangeSpans) > 0 {
		start = maxInt(1, minInt(total, plan.sourceScan.RangeSpans[0].Range.StartLine)-2)
	}
	end := minInt(total, start+maxInt(1, maxLines)-1)
	return LineRange{Start: start, End: end}
}

func batchSourceValidationRange(fp FileFingerprint, ranges []SourceLineRange, maxLines int) LineRange {
	total := maxInt(1, fp.LineCount)
	start := 1
	if len(ranges) > 0 {
		start = maxInt(1, minInt(total, sortedRanges(ranges)[0].StartLine)-2)
	}
	end := minInt(total, start+maxInt(1, maxLines)-1)
	return LineRange{Start: start, End: end}
}

func payloadDisplayLineCount(payload []byte) int {
	if len(payload) == 0 {
		return 0
	}
	count := bytes.Count(payload, []byte{'\n'})
	if !bytes.HasSuffix(payload, []byte{'\n'}) {
		count++
	}
	return maxInt(1, count)
}

func addReadBackContinuation(validation *WriteValidation, tool, file string, nextStart, chunkLines int) {
	if validation == nil || nextStart < 1 {
		return
	}
	input := map[string]any{
		"target_file": file,
		"start_line":  nextStart,
		"chunk_lines": maxInt(1, chunkLines),
	}
	hint := ActionHint{
		SafeToRetry:                true,
		RecommendedNextTool:        tool,
		RecommendedNextInput:       input,
		RecommendedNextInputPolicy: "continue_read_back_window",
		Reason:                     "Continue the post-write read-back window if more context is needed.",
	}
	validation.NextRecommendedCall, validation.NextRecommendedCalls = withPrimaryHintList([]ActionHint{hint})
}
