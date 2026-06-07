package handler

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const defaultReadFilesMaxTotalLines = 1000

func (h *Handler) HandleReadFiles(ctx context.Context, req *mcp.CallToolRequest, input ReadFilesInput) (*mcp.CallToolResult, ReadFilesOutput, error) {
	pathCtx, cwdErr := h.BuildPathContext(input.CwdID)
	if cwdErr != nil {
		return toolCwdError[ReadFilesOutput](cwdErr)
	}
	mode, err := normalizeRedactionMode(input.RedactionMode)
	if err != nil {
		return errorResult(err.Error()), ReadFilesOutput{Error: err.Error(), ErrorCode: "invalid_redaction_mode", Items: []ReadFilesItemOutput{}}, nil
	}
	if len(input.Items) > h.config.ReadFilesMaxItems {
		message := fmt.Sprintf("too_many_items: read_files accepts at most %d items", h.config.ReadFilesMaxItems)
		return errorResult(message), ReadFilesOutput{Error: message, ErrorCode: "too_many_items", Items: []ReadFilesItemOutput{}}, nil
	}
	maxLines := defaultReadFilesMaxTotalLines
	if input.MaxTotalLines != nil && *input.MaxTotalLines > 0 && *input.MaxTotalLines < maxLines {
		maxLines = *input.MaxTotalLines
	}
	maxBytes := h.config.ReadFilesMaxTotalBytes
	if input.MaxTotalBytes != nil && *input.MaxTotalBytes > 0 && *input.MaxTotalBytes < maxBytes {
		maxBytes = *input.MaxTotalBytes
	}
	output := ReadFilesOutput{
		Items:         []ReadFilesItemOutput{},
		MaxTotalLines: maxLines,
		MaxTotalBytes: maxBytes,
		RedactionMode: mode,
	}
	linesUsed := 0
	bytesUsed := 0
	for index, item := range input.Items {
		if linesUsed >= maxLines || bytesUsed >= maxBytes {
			output.Truncated = true
			output.Continuation = readFilesContinuation(pathCtx, input, index, item, maxLines, maxBytes, nil, nil)
			break
		}
		readInput := ReadFileInput{
			CwdAwareInput:   CwdAwareInput{CwdID: input.CwdID},
			TargetFile:      item.TargetFile,
			StartLine:       item.StartLine,
			EndLine:         item.EndLine,
			CountTotalLines: input.CountTotalLines,
			ChunkLines:      item.ChunkLines,
			ExpectedVersion: item.ExpectedVersion,
		}
		if readInput.EndLine == nil && readInput.ChunkLines == nil {
			remaining := maxLines - linesUsed
			if remaining > 0 {
				readInput.ChunkLines = intPtr(remaining)
				readInput.CountTotalLines = true
			}
		}
		_, readOutput, _ := h.HandleReadFile(ctx, req, readInput)
		itemOutput := readFilesItemFromReadOutput(readOutput)
		if readOutput.Error != "" {
			itemOutput.Status = "error"
			output.Items = append(output.Items, itemOutput)
			continue
		}
		itemOutput.Status = "ok"
		itemLimit := h.config.ReadFilesMaxItemBytes
		remainingBytes := maxBytes - bytesUsed
		if remainingBytes < itemLimit {
			itemLimit = remainingBytes
		}
		text, keptLines, keptBytes, lastEmittedLine, truncated := trimLineNumberedText(itemOutput.Text, maxLines-linesUsed, itemLimit)
		if truncated && itemOutput.Range != nil && lastEmittedLine > 0 && lastEmittedLine < itemOutput.Range.End {
			itemOutput.Range.End = lastEmittedLine
		}
		risky := shouldRedactContent(mode, itemOutput.File, true, hasHiddenPathSegment(itemOutput.File))
		redacted, changed := redactString(text, mode, risky)
		if changed {
			text = redacted
			itemOutput.Redacted = changed
		}
		if len([]byte(text)) > itemLimit {
			var redactedLastLine int
			text, keptLines, keptBytes, redactedLastLine, truncated = trimLineNumberedText(text, maxLines-linesUsed, itemLimit)
			if redactedLastLine > 0 {
				lastEmittedLine = redactedLastLine
				if itemOutput.Range != nil {
					itemOutput.Range.End = redactedLastLine
				}
			}
		} else {
			keptBytes = len([]byte(text))
		}
		itemOutput.RedactionMode = mode
		itemOutput.Text = text
		if truncated {
			itemOutput.Truncated = true
		}
		if truncated && keptLines == 0 {
			itemOutput.Range = nil
			itemOutput.Coverage = nil
		}
		adjustReadFilesItemCoverage(&itemOutput)
		linesUsed += keptLines
		bytesUsed += keptBytes
		if truncated {
			output.Truncated = true
			itemOutput.Continuation = nil
			nextLine := nextReadFilesStartLine(item, itemOutput, lastEmittedLine)
			output.Items = append(output.Items, itemOutput)
			nextItem := item
			nextItem.StartLine = intPtr(nextLine)
			nextItem.EndLine = nil
			if keptLines == 0 {
				output.Continuation = readFilesOversizedLineContinuation(pathCtx, input, index, nextItem, readOutput.Fingerprint, readOutput.Coverage)
			} else {
				output.Continuation = readFilesContinuation(pathCtx, input, index, nextItem, maxLines, maxBytes, readOutput.Fingerprint, readOutput.Coverage)
			}
			break
		}
		if readInput.ChunkLines != nil && readFilesReadOutputHasMore(readOutput) {
			output.Truncated = true
			itemOutput.Continuation = nil
			nextLine := nextReadFilesStartLine(item, itemOutput, lastEmittedLine)
			output.Items = append(output.Items, itemOutput)
			nextItem := item
			nextItem.StartLine = intPtr(nextLine)
			nextItem.EndLine = nil
			output.Continuation = readFilesContinuation(pathCtx, input, index, nextItem, maxLines, maxBytes, readOutput.Fingerprint, itemOutput.Coverage)
			break
		}
		output.Items = append(output.Items, itemOutput)
	}
	output.Count = len(output.Items)
	if output.Continuation == nil {
		output.Continuation = &ContinuationHint{Complete: true, Consistency: "unknown"}
	}
	return structuredResultOnly(), output, nil
}

func readFilesReadOutputHasMore(output ReadFileOutput) bool {
	if output.Continuation == nil || output.Continuation.Complete || output.Range == nil {
		return false
	}
	if output.TotalLinesKnown && output.TotalLines != nil && output.Range.End >= *output.TotalLines {
		return false
	}
	return true
}

func readFilesItemFromReadOutput(output ReadFileOutput) ReadFilesItemOutput {
	return ReadFilesItemOutput{
		File:            output.File,
		Text:            output.Text,
		Range:           output.Range,
		RequestedRange:  output.RequestedRange,
		TotalLines:      output.TotalLines,
		TotalLinesKnown: output.TotalLinesKnown,
		Coverage:        output.Coverage,
		Continuation:    output.Continuation,
		Fingerprint:     output.Fingerprint,
		Error:           output.Error,
		ErrorCode:       output.ErrorCode,
	}
}

func trimLineNumberedText(text string, maxLines, maxBytes int) (string, int, int, int, bool) {
	if maxLines <= 0 || maxBytes <= 0 {
		return "", 0, 0, 0, strings.TrimSpace(text) != ""
	}
	lines := strings.SplitAfter(text, "\n")
	var b strings.Builder
	keptLines := 0
	lastLineNumber := 0
	for _, line := range lines {
		if line == "" {
			continue
		}
		if keptLines >= maxLines || b.Len()+len(line) > maxBytes {
			return b.String(), keptLines, b.Len(), lastLineNumber, true
		}
		b.WriteString(line)
		keptLines++
		if n := lineNumberedTextPrefix(line); n > 0 {
			lastLineNumber = n
		}
	}
	return b.String(), keptLines, b.Len(), lastLineNumber, false
}

func lineNumberedTextPrefix(line string) int {
	sep := strings.IndexByte(line, '|')
	if sep <= 0 {
		return 0
	}
	n := 0
	for _, ch := range line[:sep] {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = n*10 + int(ch-'0')
	}
	return n
}

func nextReadFilesStartLine(item ReadFileInputItem, output ReadFilesItemOutput, lastEmittedLine int) int {
	if lastEmittedLine > 0 {
		return lastEmittedLine + 1
	}
	if output.Range != nil {
		return output.Range.End + 1
	}
	if item.StartLine != nil {
		return *item.StartLine
	}
	return 1
}

func adjustReadFilesItemCoverage(output *ReadFilesItemOutput) {
	if output == nil || output.Coverage == nil || output.Range == nil {
		return
	}
	coverage := *output.Coverage
	coverage.RequestedRangeComplete = !output.Truncated && coverage.RequestedRangeComplete
	coverage.CompleteFileRead = false
	if output.TotalLinesKnown && output.TotalLines != nil {
		coverage.CompleteFileRead = output.Range.Start == 1 && output.Range.End >= *output.TotalLines
		if output.Range.End < *output.TotalLines {
			coverage.NextRange = &SourceLineRange{StartLine: output.Range.End + 1, EndLine: *output.TotalLines}
		} else {
			coverage.NextRange = nil
		}
	} else {
		coverage.NextRange = &SourceLineRange{
			StartLine: output.Range.End + 1,
			EndLine:   output.Range.End + maxInt(1, output.Range.End-output.Range.Start+1),
		}
	}
	if coverage.Proof != nil {
		proof := *coverage.Proof
		proof.Range = SourceLineRange{StartLine: output.Range.Start, EndLine: output.Range.End}
		coverage.Proof = &proof
	}
	output.Coverage = &coverage
	if output.Continuation != nil && output.Continuation.Complete {
		output.Continuation.Consistency = readCoverageConsistency(output.Coverage)
	}
	if output.Coverage != nil && output.TotalLinesKnown && output.TotalLines != nil && output.Range != nil && output.Range.End >= *output.TotalLines {
		output.Continuation = &ContinuationHint{Complete: true, Consistency: readCoverageConsistency(output.Coverage)}
	}
}

func readFilesContinuation(pathCtx PathContext, input ReadFilesInput, index int, current ReadFileInputItem, maxLines, maxBytes int, fingerprint *FileFingerprint, coverage *ReadCoverage) *ContinuationHint {
	if index < 0 || index >= len(input.Items) {
		return &ContinuationHint{Complete: true, Consistency: "unknown"}
	}
	items := make([]map[string]any, 0, len(input.Items)-index)
	first := readFileItemInputMap(current, fingerprint, coverage)
	items = append(items, first)
	for _, item := range input.Items[index+1:] {
		items = append(items, readFileItemInputMap(item, nil, nil))
	}
	nextInput := map[string]any{
		"items":           items,
		"max_total_lines": maxLines,
		"max_total_bytes": maxBytes,
		"redaction_mode":  normalizedOrAuto(input.RedactionMode),
	}
	if input.CountTotalLines {
		nextInput["count_total_lines"] = true
	}
	addCwdIDToRecommendedInput(pathCtx, "read_files", nextInput)
	action := ActionHint{
		SafeToRetry:                true,
		RecommendedNextTool:        "read_files",
		RecommendedNextInput:       nextInput,
		RecommendedNextInputPolicy: "continue_read_files",
		Reason:                     "Continue batch reading from the next complete line boundary.",
	}
	return &ContinuationHint{
		Complete:             false,
		Consistency:          readCoverageConsistency(coverage),
		StaleIfFileChanges:   true,
		NextRecommendedCall:  &action,
		NextRecommendedCalls: []ActionHint{action},
		Reason:               "read_files output stopped at a configured line or byte limit.",
	}
}

func readFilesOversizedLineContinuation(pathCtx PathContext, input ReadFilesInput, index int, current ReadFileInputItem, fingerprint *FileFingerprint, coverage *ReadCoverage) *ContinuationHint {
	startLine := 1
	if current.StartLine != nil && *current.StartLine > 0 {
		startLine = *current.StartLine
	}
	nextInput := map[string]any{
		"target_file": current.TargetFile,
		"start_line":  startLine,
		"end_line":    startLine,
	}
	if fingerprint != nil {
		nextInput["expected_version"] = readCoverageProofInput(ReadCoverageProof{
			SizeBytes:        fingerprint.SizeBytes,
			ModifiedUnixNano: fingerprint.ModifiedUnixNano,
			SHA256:           fingerprint.SHA256,
			ProofStrength:    "exact",
			Range:            SourceLineRange{StartLine: startLine, EndLine: startLine},
		})
	}
	addCwdIDToRecommendedInput(pathCtx, "read_file", nextInput)
	action := ActionHint{
		SafeToRetry:                true,
		RecommendedNextTool:        "read_file",
		RecommendedNextInput:       nextInput,
		RecommendedNextInputPolicy: "read_oversized_line",
		Reason:                     "Read the oversized line directly; read_files stopped before a complete line and did not advance.",
	}
	return &ContinuationHint{
		Complete:             false,
		Consistency:          readCoverageConsistency(coverage),
		StaleIfFileChanges:   true,
		NextRecommendedCall:  &action,
		NextRecommendedCalls: []ActionHint{action},
		Reason:               "read_files stopped before the next complete line because the line exceeds the byte budget; no line was skipped.",
	}
}

func readFileItemInputMap(item ReadFileInputItem, fingerprint *FileFingerprint, coverage *ReadCoverage) map[string]any {
	out := map[string]any{"target_file": item.TargetFile}
	if item.StartLine != nil {
		out["start_line"] = *item.StartLine
	}
	if item.EndLine != nil {
		out["end_line"] = *item.EndLine
	}
	if item.ChunkLines != nil {
		out["chunk_lines"] = *item.ChunkLines
	}
	if item.ExpectedVersion != nil {
		out["expected_version"] = readCoverageProofInput(*item.ExpectedVersion)
	} else if fingerprint != nil {
		out["expected_version"] = readCoverageProofInput(ReadCoverageProof{
			SizeBytes:        fingerprint.SizeBytes,
			ModifiedUnixNano: fingerprint.ModifiedUnixNano,
			SHA256:           fingerprint.SHA256,
			ProofStrength:    "exact",
		})
	} else if coverage != nil && coverage.Proof != nil {
		out["expected_version"] = readCoverageProofInput(*coverage.Proof)
	}
	return out
}

func readCoverageProofInput(proof ReadCoverageProof) map[string]any {
	out := map[string]any{
		"size_bytes":         proof.SizeBytes,
		"modified_unix_nano": proof.ModifiedUnixNano,
		"proof_strength":     proof.ProofStrength,
	}
	if proof.SHA256 != "" {
		out["sha256"] = proof.SHA256
	}
	if proof.Range.StartLine > 0 || proof.Range.EndLine > 0 {
		out["range"] = map[string]any{
			"start_line": proof.Range.StartLine,
			"end_line":   proof.Range.EndLine,
		}
	}
	return out
}

func normalizedOrAuto(mode string) string {
	normalized, err := normalizeRedactionMode(mode)
	if err != nil {
		return redactionAuto
	}
	return normalized
}
