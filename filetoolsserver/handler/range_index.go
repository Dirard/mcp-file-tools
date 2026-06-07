package handler

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
)

type byteSpan struct {
	Start int64
	End   int64
}

type rangeSpan struct {
	Range SourceLineRange
	Span  byteSpan
}

type lineScanResult struct {
	LineCount       int
	SizeBytes       int64
	FinalNewline    bool
	RangeSpans      []rangeSpan
	LineStartOffset map[int]int64
}

func validateSourceRanges(ranges []SourceLineRange, allowOverlap bool) error {
	if len(ranges) == 0 {
		return fmt.Errorf("ranges must contain at least one range")
	}
	for i, r := range ranges {
		if r.StartLine < 1 || r.EndLine < r.StartLine {
			return fmt.Errorf("invalid range at index %d: start_line and end_line must be 1-based and end_line >= start_line", i)
		}
	}
	if !allowOverlap {
		sorted := append([]SourceLineRange(nil), ranges...)
		sort.Slice(sorted, func(i, j int) bool {
			if sorted[i].StartLine == sorted[j].StartLine {
				return sorted[i].EndLine < sorted[j].EndLine
			}
			return sorted[i].StartLine < sorted[j].StartLine
		})
		for i := 1; i < len(sorted); i++ {
			if sorted[i].StartLine <= sorted[i-1].EndLine {
				return fmt.Errorf("ranges must be non-overlapping")
			}
		}
	}
	return nil
}

func scanLineSpans(ctx context.Context, path string, ranges []SourceLineRange, extraLineStarts []int) (lineScanResult, error) {
	file, err := os.Open(path)
	if err != nil {
		return lineScanResult{}, err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return lineScanResult{}, err
	}
	result := lineScanResult{
		SizeBytes:       stat.Size(),
		RangeSpans:      make([]rangeSpan, len(ranges)),
		LineStartOffset: map[int]int64{},
	}
	for _, line := range extraLineStarts {
		if line >= 1 {
			result.LineStartOffset[line] = -1
		}
	}
	for i, r := range ranges {
		result.RangeSpans[i].Range = r
		result.LineStartOffset[r.StartLine] = -1
	}
	rangeStartIndexes, rangeEndIndexes := indexRangeEndpoints(ranges)
	if stat.Size() == 0 {
		result.LineCount = 0
		if len(ranges) > 0 {
			return result, fmt.Errorf("range %d-%d is out of bounds; file has 0 lines", ranges[0].StartLine, ranges[0].EndLine)
		}
		return result, nil
	}

	buf := make([]byte, 64*1024)
	offset := int64(0)
	currentLine := 1
	currentLineStart := int64(0)
	lastByte := byte(0)
	markLineStart(&result, currentLine, currentLineStart)
	markRangeStarts(&result, rangeStartIndexes[currentLine], currentLineStart)
	for {
		if err := contextError(ctx); err != nil {
			return lineScanResult{}, err
		}
		n, readErr := file.Read(buf)
		for i := 0; i < n; i++ {
			b := buf[i]
			absolute := offset + int64(i)
			lastByte = b
			if b != '\n' {
				continue
			}
			lineEnd := absolute + 1
			markRangeEnds(&result, rangeEndIndexes[currentLine], lineEnd)
			currentLine++
			currentLineStart = lineEnd
			markLineStart(&result, currentLine, currentLineStart)
			markRangeStarts(&result, rangeStartIndexes[currentLine], currentLineStart)
		}
		offset += int64(n)
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return lineScanResult{}, readErr
		}
		if n == 0 {
			break
		}
	}
	result.FinalNewline = lastByte == '\n'
	result.LineCount = currentLine
	if !result.FinalNewline {
		markRangeEnds(&result, rangeEndIndexes[currentLine], stat.Size())
	} else {
		markRangeEnds(&result, rangeEndIndexes[currentLine], stat.Size())
	}
	for i, span := range result.RangeSpans {
		if span.Range.EndLine > result.LineCount {
			return result, fmt.Errorf("range %d-%d is out of bounds; file has %d lines", span.Range.StartLine, span.Range.EndLine, result.LineCount)
		}
		if span.Span.Start < 0 || span.Span.End < span.Span.Start {
			return result, fmt.Errorf("could not map range %d-%d to byte span", span.Range.StartLine, span.Range.EndLine)
		}
		if span.Span.End == span.Span.Start {
			return result, fmt.Errorf("zero_byte_range: range %d-%d maps to no bytes; select a concrete source line before the final trailing newline", span.Range.StartLine, span.Range.EndLine)
		}
		result.RangeSpans[i] = span
	}
	for line, offset := range result.LineStartOffset {
		if line == result.LineCount+1 {
			result.LineStartOffset[line] = stat.Size()
			continue
		}
		if offset < 0 && line > result.LineCount {
			return result, fmt.Errorf("line %d is out of bounds; file has %d lines", line, result.LineCount)
		}
	}
	return result, nil
}

func indexRangeEndpoints(ranges []SourceLineRange) (map[int][]int, map[int][]int) {
	starts := make(map[int][]int, len(ranges))
	ends := make(map[int][]int, len(ranges))
	for i, r := range ranges {
		starts[r.StartLine] = append(starts[r.StartLine], i)
		ends[r.EndLine] = append(ends[r.EndLine], i)
	}
	return starts, ends
}

func markLineStart(result *lineScanResult, line int, offset int64) {
	if _, ok := result.LineStartOffset[line]; ok {
		result.LineStartOffset[line] = offset
	}
}

func markRangeStarts(result *lineScanResult, indexes []int, offset int64) {
	for _, i := range indexes {
		result.RangeSpans[i].Span.Start = offset
	}
}

func markRangeEnds(result *lineScanResult, indexes []int, offset int64) {
	for _, i := range indexes {
		result.RangeSpans[i].Span.End = offset
	}
}
