package handler

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// HandleReadFile reads a text file into compact line-numbered text.
func (h *Handler) HandleReadFile(ctx context.Context, req *mcp.CallToolRequest, input ReadFileInput) (*mcp.CallToolResult, ReadFileOutput, error) {
	pathCtx, cwdErr := h.BuildPathContext(input.CwdID)
	if cwdErr != nil {
		return toolCwdError[ReadFileOutput](cwdErr)
	}
	resolvedFile, displayFile, err := h.resolveToolPath(pathCtx, input.TargetFile, "target_file")
	if err != nil {
		return toolError[ReadFileOutput](fmt.Sprintf("Cannot read target_file: %v", err))
	}
	if err := normalizeReadFileInput(&input); err != nil {
		return readFileStructuredError(displayFile, err.Error(), errorCodeFromMessage(err.Error()), nil, nil)
	}
	releaseTool, err := h.acquireToolCall(ctx)
	if err != nil {
		return toolError[ReadFileOutput](limiterWaitError("tool call", err))
	}
	defer releaseTool()

	stat, err := os.Stat(resolvedFile)
	if err != nil {
		return toolError[ReadFileOutput](fmt.Sprintf("Cannot read %q: %v\n\nCheck that the file exists and is readable.", displayFile, err))
	}
	if stat.IsDir() {
		return toolError[ReadFileOutput](fmt.Sprintf("%q is a directory, not a file.\n\nUse list_dir for directories.", displayFile))
	}
	if stat.Size() > h.config.MemoryThreshold {
		releaseLargeRead, err := h.acquireLargeRead(ctx)
		if err != nil {
			return toolError[ReadFileOutput](limiterWaitError("large read", err))
		}
		defer releaseLargeRead()
	}
	encResult, err := h.resolveEncodingSample("", resolvedFile)
	if err != nil {
		return toolError[ReadFileOutput](fmt.Sprintf("Cannot detect encoding for %q: %v", displayFile, err))
	}
	if err := h.checkReadExpectedVersion(ctx, resolvedFile, stat, encResult, input.ExpectedVersion); err != nil {
		return readFileStructuredError(displayFile, err.Error(), errorCodeFromMessage(err.Error()), nil, nil)
	}
	if stat.Size() == 0 {
		result, output, err := readEmptyFile(ctx, displayFile, input)
		if err == nil {
			h.enrichReadFileOutput(ctx, pathCtx, &output, input, resolvedFile, stat, encResult)
		}
		return result, output, err
	}

	if input.StartLine != nil && input.EndLine != nil && !input.CountTotalLines {
		startLine, endLine, err := readFileExplicitLineRange(*input.StartLine, *input.EndLine)
		if err != nil {
			return readFileStructuredError(displayFile, "invalid_read_range: "+err.Error(), "invalid_read_range", lineRangePtr(*input.StartLine, *input.EndLine), nil)
		}
		result, lineNumberBase, totalLinesKnown, totalLines, err := readFileBoundedRangeFromDisk(ctx, resolvedFile, encResult, startLine, endLine)
		if err != nil {
			return toolError[ReadFileOutput](fmt.Sprintf("Cannot read %q: %v", displayFile, err))
		}
		if totalLinesKnown && len(result.lines) == 0 && startLine > totalLines {
			return readFileStartBeyondEOFError(displayFile, startLine, totalLines, lineRangePtr(startLine, endLine))
		}
		output := readFileRangeOutput(displayFile, totalLinesKnown, totalLines, startLine, endLine, result, lineNumberBase)
		h.enrichReadFileOutput(ctx, pathCtx, &output, input, resolvedFile, stat, encResult)
		return structuredResultOnly(), output, nil
	}

	totalLines, err := countDecodedLines(ctx, resolvedFile, encResult)
	if err != nil {
		return toolError[ReadFileOutput](fmt.Sprintf("Cannot read %q: %v", displayFile, err))
	}
	if input.StartLine != nil && *input.StartLine > totalLines {
		return readFileStartBeyondEOFError(displayFile, *input.StartLine, totalLines, nil)
	}
	startIdx, endIdx, err := readFileLineRange(totalLines, input.StartLine, input.EndLine)
	if err != nil {
		return readFileStructuredError(displayFile, "invalid_read_range: "+err.Error(), "invalid_read_range", nil, intPtr(totalLines))
	}
	if startIdx == endIdx {
		output := readFileOutput(displayFile, totalLines, readTextResult{}, startIdx)
		h.enrichReadFileOutput(ctx, pathCtx, &output, input, resolvedFile, stat, encResult)
		return structuredResultOnly(), output, nil
	}

	result, lineNumberBase, err := readFileRangeFromDisk(ctx, resolvedFile, encResult, startIdx, endIdx)
	if err != nil {
		return toolError[ReadFileOutput](fmt.Sprintf("Cannot read %q: %v", displayFile, err))
	}
	output := readFileOutput(displayFile, totalLines, result, lineNumberBase)
	if input.StartLine != nil || input.EndLine != nil {
		output.RequestedRange = lineRangePtr(startIdx+1, endIdx)
	}
	h.enrichReadFileOutput(ctx, pathCtx, &output, input, resolvedFile, stat, encResult)
	return structuredResultOnly(), output, nil
}

func normalizeReadFileInput(input *ReadFileInput) error {
	if input == nil {
		return nil
	}
	if input.ExpectedVersion != nil {
		if err := validateReadCoverageProof(*input.ExpectedVersion); err != nil {
			return err
		}
	}
	if input.ChunkLines != nil {
		if *input.ChunkLines < 1 {
			return fmt.Errorf("invalid_read_range: chunk_lines must be >= 1")
		}
		if input.EndLine != nil {
			return fmt.Errorf("invalid_read_range: end_line cannot be combined with chunk_lines")
		}
		start := 1
		if input.StartLine != nil {
			start = *input.StartLine
		}
		if start < 1 {
			return fmt.Errorf("invalid_read_range: start_line must be >= 1")
		}
		end := start + *input.ChunkLines - 1
		input.StartLine = intPtr(start)
		input.EndLine = intPtr(end)
		return nil
	}
	return nil
}

func validateReadCoverageProof(proof ReadCoverageProof) error {
	switch proof.ProofStrength {
	case "", "stat_only":
		return nil
	case "exact":
		if proof.SHA256 == "" || proof.SizeBytes < 0 || proof.ModifiedUnixNano == 0 {
			return fmt.Errorf("invalid_continuation_proof: exact proof requires sha256, size_bytes and modified_unix_nano")
		}
		return nil
	default:
		return fmt.Errorf("invalid_continuation_proof: proof_strength must be exact or stat_only")
	}
}

func (h *Handler) checkReadExpectedVersion(ctx context.Context, resolvedFile string, stat os.FileInfo, encResult encodingResult, expected *ReadCoverageProof) error {
	if expected == nil {
		return nil
	}
	if err := validateReadCoverageProof(*expected); err != nil {
		return err
	}
	if expected.ProofStrength == "exact" {
		fp, err := computeFileFingerprint(ctx, resolvedFile, stat, encResult)
		if err != nil {
			return err
		}
		if !strings.EqualFold(fp.SHA256, expected.SHA256) || fp.SizeBytes != expected.SizeBytes || fp.ModifiedUnixNano != expected.ModifiedUnixNano {
			return fmt.Errorf("continuation_stale: file changed since expected_version")
		}
		return nil
	}
	if expected.SizeBytes != 0 && stat.Size() != expected.SizeBytes || expected.ModifiedUnixNano != 0 && stat.ModTime().UnixNano() != expected.ModifiedUnixNano {
		return fmt.Errorf("continuation_stale: file changed since expected_version")
	}
	return nil
}

func readEmptyFile(ctx context.Context, displayFile string, input ReadFileInput) (*mcp.CallToolResult, ReadFileOutput, error) {
	if err := contextError(ctx); err != nil {
		return toolError[ReadFileOutput](err.Error())
	}
	if input.StartLine != nil && input.EndLine != nil {
		startLine, endLine, err := readFileExplicitLineRange(*input.StartLine, *input.EndLine)
		if err != nil {
			return readFileStructuredError(displayFile, "invalid_read_range: "+err.Error(), "invalid_read_range", lineRangePtr(*input.StartLine, *input.EndLine), intPtr(0))
		}
		if startLine > 1 {
			return readFileStartBeyondEOFError(displayFile, startLine, 0, lineRangePtr(startLine, endLine))
		}
	} else {
		if input.StartLine != nil {
			if *input.StartLine < 1 {
				return readFileStructuredError(displayFile, fmt.Sprintf("invalid_read_range: Invalid start_line %d. Lines are 1-based; use start_line >= 1.", *input.StartLine), "invalid_read_range", nil, intPtr(0))
			}
			if *input.StartLine > 1 {
				return readFileStartBeyondEOFError(displayFile, *input.StartLine, 0, nil)
			}
		}
		if input.EndLine != nil && *input.EndLine < 1 {
			return readFileStructuredError(displayFile, fmt.Sprintf("invalid_read_range: Invalid end_line %d. Lines are 1-based; use end_line >= 1.", *input.EndLine), "invalid_read_range", nil, intPtr(0))
		}
	}
	return structuredResultOnly(), ReadFileOutput{
		File:            displayFile,
		TotalLines:      intPtr(0),
		TotalLinesKnown: true,
	}, nil
}

func readFileStructuredError(path, message, code string, requestedRange *LineRange, totalLines *int) (*mcp.CallToolResult, ReadFileOutput, error) {
	return errorResult(message), ReadFileOutput{
		Error:           message,
		ErrorCode:       code,
		File:            path,
		TotalLines:      totalLines,
		TotalLinesKnown: totalLines != nil,
		RequestedRange:  requestedRange,
	}, nil
}

func (h *Handler) enrichReadFileOutput(ctx context.Context, pathCtx PathContext, output *ReadFileOutput, input ReadFileInput, resolvedFile string, stat os.FileInfo, encResult encodingResult) {
	if output == nil || output.Error != "" {
		return
	}
	var fingerprint *FileFingerprint
	if input.CountTotalLines || output.TotalLinesKnown && output.Range != nil && output.Range.Start == 1 && output.TotalLines != nil && output.Range.End >= *output.TotalLines || input.ExpectedVersion != nil {
		if fp, err := computeFileFingerprint(ctx, resolvedFile, stat, encResult); err == nil {
			fingerprint = &fp
			output.Fingerprint = &fp
			if !output.TotalLinesKnown {
				output.TotalLines = intPtr(fp.LineCount)
				output.TotalLinesKnown = true
			}
		}
	}
	if input.CountTotalLines && output.TotalLines == nil {
		if total, err := countDecodedLines(ctx, resolvedFile, encResult); err == nil {
			output.TotalLines = intPtr(total)
			output.TotalLinesKnown = true
		}
	}
	output.Coverage = readCoverageForOutput(output, stat, fingerprint)
	output.Continuation = readContinuationForOutput(pathCtx, input, output, fingerprint)
}

func readCoverageForOutput(output *ReadFileOutput, stat os.FileInfo, fingerprint *FileFingerprint) *ReadCoverage {
	coverage := &ReadCoverage{
		RequestedRangeComplete: true,
		FileTotalLinesKnown:    output.TotalLinesKnown,
	}
	if output.Range == nil {
		if output.TotalLines != nil && *output.TotalLines == 0 {
			coverage.CompleteFileRead = true
		}
		return coverage
	}
	if output.RequestedRange != nil {
		coverage.RequestedRangeComplete = output.Range.Start <= output.RequestedRange.Start && output.Range.End >= minInt(output.RequestedRange.End, output.Range.End)
	}
	if output.TotalLinesKnown && output.TotalLines != nil {
		coverage.CompleteFileRead = output.Range.Start == 1 && output.Range.End >= *output.TotalLines
		if output.Range.End < *output.TotalLines {
			coverage.NextRange = &SourceLineRange{StartLine: output.Range.End + 1, EndLine: *output.TotalLines}
		}
	} else {
		coverage.CompleteFileRead = false
		coverage.NextRange = &SourceLineRange{StartLine: output.Range.End + 1, EndLine: output.Range.End + maxInt(1, output.Range.End-output.Range.Start+1)}
	}
	if output.Range != nil {
		proof := &ReadCoverageProof{
			SizeBytes:        stat.Size(),
			ModifiedUnixNano: stat.ModTime().UnixNano(),
			ProofStrength:    "stat_only",
			Range:            SourceLineRange{StartLine: output.Range.Start, EndLine: output.Range.End},
		}
		if fingerprint != nil && fingerprint.SHA256 != "" && fingerprint.SizeBytes > 0 && fingerprint.ModifiedUnixNano != 0 {
			proof.SHA256 = fingerprint.SHA256
			proof.SizeBytes = fingerprint.SizeBytes
			proof.ModifiedUnixNano = fingerprint.ModifiedUnixNano
			proof.ProofStrength = "exact"
		}
		coverage.Proof = proof
	}
	return coverage
}

func readContinuationForOutput(pathCtx PathContext, input ReadFileInput, output *ReadFileOutput, fingerprint *FileFingerprint) *ContinuationHint {
	if output == nil || output.Coverage == nil || output.Range == nil {
		if output != nil && output.Coverage != nil {
			return &ContinuationHint{Complete: true, Consistency: readCoverageConsistency(output.Coverage)}
		}
		return nil
	}
	if output.Coverage.CompleteFileRead || output.TotalLinesKnown && output.TotalLines != nil && output.Range.End >= *output.TotalLines {
		return &ContinuationHint{Complete: true, Consistency: readCoverageConsistency(output.Coverage)}
	}
	nextStart := output.Range.End + 1
	nextInput := map[string]any{
		"target_file": output.File,
		"start_line":  nextStart,
	}
	if input.ChunkLines != nil {
		nextInput["chunk_lines"] = *input.ChunkLines
	} else if input.EndLine != nil {
		window := output.Range.End - output.Range.Start + 1
		nextInput["end_line"] = output.Range.End + maxInt(1, window)
	}
	if input.CountTotalLines {
		nextInput["count_total_lines"] = true
	}
	if fingerprint != nil && fingerprint.SHA256 != "" && fingerprint.SizeBytes > 0 && fingerprint.ModifiedUnixNano != 0 {
		nextInput["expected_version"] = map[string]any{
			"size_bytes":         fingerprint.SizeBytes,
			"modified_unix_nano": fingerprint.ModifiedUnixNano,
			"sha256":             fingerprint.SHA256,
			"proof_strength":     "exact",
			"range": map[string]any{
				"start_line": output.Range.Start,
				"end_line":   output.Range.End,
			},
		}
	} else if output.Coverage != nil && output.Coverage.Proof != nil {
		nextInput["expected_version"] = readCoverageProofInput(*output.Coverage.Proof)
	}
	addCwdIDToRecommendedInput(pathCtx, "read_file", nextInput)
	action := ActionHint{
		SafeToRetry:                true,
		RecommendedNextTool:        "read_file",
		RecommendedNextInput:       nextInput,
		RecommendedNextInputPolicy: "continue_read_chunk",
		Reason:                     "Continue reading at the next returned line boundary.",
	}
	return &ContinuationHint{
		Complete:             false,
		Consistency:          readCoverageConsistency(output.Coverage),
		StaleIfFileChanges:   true,
		NextRecommendedCall:  &action,
		NextRecommendedCalls: []ActionHint{action},
		Reason:               "The returned range does not cover the complete file.",
	}
}

func readCoverageConsistency(coverage *ReadCoverage) string {
	if coverage != nil && coverage.Proof != nil && coverage.Proof.ProofStrength == "exact" {
		return "unchanged"
	}
	return "unknown"
}

type displayRuneStream struct {
	reader *bufio.Reader
}

func newDisplayRuneStream(path string, encResult encodingResult) (*displayRuneStream, *os.File, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	return &displayRuneStream{reader: bufio.NewReader(decodedReader(file, encResult))}, file, nil
}

func (s *displayRuneStream) nextDisplayRune() (rune, bool, bool, error) {
	r, _, err := s.reader.ReadRune()
	if err == nil {
		switch r {
		case '\n':
			return 0, true, false, nil
		case '\r':
			next, _, nextErr := s.reader.ReadRune()
			if nextErr == nil {
				if next == '\n' {
					return 0, true, false, nil
				}
				if err := s.reader.UnreadRune(); err != nil {
					return 0, false, false, err
				}
				return r, false, false, nil
			}
			if nextErr == io.EOF {
				return 0, false, true, nil
			}
			return 0, false, false, nextErr
		default:
			return r, false, false, nil
		}
	}
	if err == io.EOF {
		return 0, false, true, nil
	}
	return 0, false, false, err
}

func countDecodedLines(ctx context.Context, path string, encResult encodingResult) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	reader := decodedReader(file, encResult)
	buf := make([]byte, 32*1024)
	newlines := 0
	for {
		if err := contextError(ctx); err != nil {
			return 0, err
		}
		n, err := reader.Read(buf)
		if n > 0 {
			newlines += bytes.Count(buf[:n], []byte{'\n'})
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return 0, err
		}
		if n == 0 {
			break
		}
	}
	return newlines + 1, nil
}

func readFileRangeFromDisk(ctx context.Context, path string, encResult encodingResult, startIdx, endIdx int) (readTextResult, int, error) {
	selectedLineCount := endIdx - startIdx
	stream, closeFile, err := newDisplayRuneStream(path, encResult)
	if err != nil {
		return readTextResult{}, 0, err
	}
	defer closeFile.Close()

	lineNumberBase := startIdx
	if err := skipDisplayLines(ctx, stream, startIdx); err != nil {
		return readTextResult{}, 0, err
	}

	result := readTextResult{}
	for relativeLine := 0; relativeLine < selectedLineCount; relativeLine++ {
		if err := contextError(ctx); err != nil {
			return readTextResult{}, 0, err
		}
		text, eof, err := readDisplayLine(ctx, stream)
		if err != nil {
			return readTextResult{}, 0, err
		}
		result = appendReadLine(result, ReadTextLine{
			Line: relativeLine + 1,
			Text: text,
		})
		if eof {
			return result, lineNumberBase, nil
		}
	}

	return result, lineNumberBase, nil
}

func readFileBoundedRangeFromDisk(ctx context.Context, path string, encResult encodingResult, startLine, endLine int) (readTextResult, int, bool, int, error) {
	selectedLineCount := endLine - startLine + 1
	stream, closeFile, err := newDisplayRuneStream(path, encResult)
	if err != nil {
		return readTextResult{}, 0, false, 0, err
	}
	defer closeFile.Close()

	totalLinesBeforeTarget, err := skipDisplayLinesCounting(ctx, stream, startLine-1)
	if err != nil {
		if err == io.EOF {
			return readTextResult{}, startLine - 1, true, totalLinesBeforeTarget, nil
		}
		return readTextResult{}, 0, false, 0, err
	}

	lineNumberBase := startLine - 1
	result := readTextResult{}
	for relativeLine := 0; relativeLine < selectedLineCount; relativeLine++ {
		if err := contextError(ctx); err != nil {
			return readTextResult{}, 0, false, 0, err
		}
		text, eof, err := readDisplayLine(ctx, stream)
		if err != nil {
			return readTextResult{}, 0, false, 0, err
		}
		result = appendReadLine(result, ReadTextLine{
			Line: relativeLine + 1,
			Text: text,
		})
		if eof {
			return result, lineNumberBase, true, startLine + relativeLine, nil
		}
	}

	return result, lineNumberBase, false, 0, nil
}

func skipDisplayLinesCounting(ctx context.Context, stream *displayRuneStream, lines int) (int, error) {
	skipped := 0
	for skipped < lines {
		if err := contextError(ctx); err != nil {
			return 0, err
		}
		_, lineBreak, eof, err := stream.nextDisplayRune()
		if err != nil {
			return 0, err
		}
		if eof {
			return skipped + 1, io.EOF
		}
		if lineBreak {
			skipped++
		}
	}
	return 0, nil
}

func skipDisplayLines(ctx context.Context, stream *displayRuneStream, lines int) error {
	for skipped := 0; skipped < lines; {
		if err := contextError(ctx); err != nil {
			return err
		}
		_, lineBreak, eof, err := stream.nextDisplayRune()
		if err != nil {
			return err
		}
		if eof {
			return fmt.Errorf("position beyond file")
		}
		if lineBreak {
			skipped++
		}
	}
	return nil
}

func readDisplayChunk(ctx context.Context, stream *displayRuneStream, maxRunes int) (string, bool, bool, error) {
	var b strings.Builder
	count := 0
	for count < maxRunes {
		if err := contextError(ctx); err != nil {
			return "", false, false, err
		}
		r, lineBreak, eof, err := stream.nextDisplayRune()
		if err != nil {
			return "", false, false, err
		}
		if eof || lineBreak {
			return b.String(), true, eof, nil
		}
		b.WriteRune(r)
		count++
	}
	return b.String(), false, false, nil
}

func readDisplayLine(ctx context.Context, stream *displayRuneStream) (string, bool, error) {
	var b strings.Builder
	for {
		text, ended, eof, err := readDisplayChunk(ctx, stream, 2048)
		if err != nil {
			return "", false, err
		}
		b.WriteString(text)
		if ended {
			return b.String(), eof, nil
		}
	}
}

func appendReadLine(result readTextResult, row ReadTextLine) readTextResult {
	if len(result.lines) == 0 {
		result.startLine = row.Line
	}
	result.endLine = row.Line
	result.lines = append(result.lines, row)
	return result
}

func readFileOutput(path string, totalLines int, result readTextResult, lineNumberBase int) ReadFileOutput {
	var actualRange *LineRange
	if result.startLine > 0 {
		actualRange = lineRangePtr(lineNumberBase+result.startLine, lineNumberBase+result.endLine)
	}
	return ReadFileOutput{
		Text:            readFileOutputText(result, lineNumberBase),
		File:            path,
		TotalLines:      intPtr(totalLines),
		TotalLinesKnown: true,
		Range:           actualRange,
	}
}

func readFileRangeOutput(path string, totalLinesKnown bool, totalLines, requestedStartLine, requestedEndLine int, result readTextResult, lineNumberBase int) ReadFileOutput {
	var totalLinesPtr *int
	if totalLinesKnown {
		totalLinesPtr = intPtr(totalLines)
	}
	var actualRange *LineRange
	if result.startLine > 0 {
		actualRange = lineRangePtr(lineNumberBase+result.startLine, lineNumberBase+result.endLine)
	}
	return ReadFileOutput{
		Text:            readFileOutputText(result, lineNumberBase),
		File:            path,
		TotalLines:      totalLinesPtr,
		TotalLinesKnown: totalLinesKnown,
		RequestedRange:  lineRangePtr(requestedStartLine, requestedEndLine),
		Range:           actualRange,
	}
}

func readFileOutputText(result readTextResult, lineNumberBase int) string {
	var b strings.Builder
	for _, line := range result.lines {
		b.WriteString(fmt.Sprintf("%d|%s\n", lineNumberBase+line.Line, line.Text))
	}
	return b.String()
}

func readFileStartBeyondEOFMessage(path string, startLine, totalLines int) string {
	return fmt.Sprintf("start_line %d is beyond EOF for %q (total_lines=%d).\n\nChoose start_line <= total_lines or inspect the file length first.", startLine, path, totalLines)
}

func readFileStartBeyondEOFError(path string, startLine, totalLines int, requestedRange *LineRange) (*mcp.CallToolResult, ReadFileOutput, error) {
	message := readFileStartBeyondEOFMessage(path, startLine, totalLines)
	return errorResult(message), ReadFileOutput{
		Error:           message,
		ErrorCode:       "invalid_read_range",
		File:            path,
		TotalLines:      intPtr(totalLines),
		TotalLinesKnown: true,
		RequestedRange:  requestedRange,
	}, nil
}

func readFileLineRange(totalLines int, startLine, endLine *int) (int, int, error) {
	start := 1
	end := totalLines
	if startLine != nil {
		if *startLine < 1 {
			return 0, 0, fmt.Errorf("Invalid start_line %d. Lines are 1-based; use start_line >= 1.", *startLine)
		}
		start = *startLine
	}
	if endLine != nil {
		if *endLine < 1 {
			return 0, 0, fmt.Errorf("Invalid end_line %d. Lines are 1-based; use end_line >= 1.", *endLine)
		}
		end = *endLine
	}
	if startLine != nil && endLine != nil && start > end {
		return 0, 0, fmt.Errorf("Invalid line range: start_line (%d) cannot be greater than end_line (%d).", start, end)
	}
	if end > totalLines {
		end = totalLines
	}
	if start > totalLines {
		return totalLines, totalLines, nil
	}
	return start - 1, end, nil
}

func readFileExplicitLineRange(startLine, endLine int) (int, int, error) {
	if startLine < 1 {
		return 0, 0, fmt.Errorf("Invalid start_line %d. Lines are 1-based; use start_line >= 1.", startLine)
	}
	if endLine < 1 {
		return 0, 0, fmt.Errorf("Invalid end_line %d. Lines are 1-based; use end_line >= 1.", endLine)
	}
	if startLine > endLine {
		return 0, 0, fmt.Errorf("Invalid line range: start_line (%d) cannot be greater than end_line (%d).", startLine, endLine)
	}
	return startLine, endLine, nil
}
