package handler

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	outlineLanguageText      = "text"
	genericTextMaxBlockLines = 40
	genericTextTargetBytes   = 4096
)

func (h *Handler) outlineGenericText(ctx context.Context, info fileTextInfo, options outlineOptions) (OutlineFileOutput, error) {
	base := outlineBaseOutput(info, "generic_text", outlineLanguageText)
	if options.includeSections {
		sections, stats, truncated, err := h.genericTextBlocks(ctx, info, options)
		if err != nil {
			return base, err
		}
		base.Sections = sections
		if options.enclosingLine != nil {
			base.EnclosingItems = enclosingOutlineItems(sections, *options.enclosingLine)
		}
		base.OutlineStats = stats
		base.Truncated = truncated
	} else {
		base.OutlineStats = OutlineStats{ItemsOmittedKnown: true}
	}
	return base, nil
}

func (h *Handler) genericTextBlocks(ctx context.Context, info fileTextInfo, options outlineOptions) ([]OutlineItem, OutlineStats, bool, error) {
	infoStat := info.stat
	if infoStat.Size() == 0 {
		return []OutlineItem{}, OutlineStats{ItemsOmittedKnown: true}, false, nil
	}
	stream, closeFile, err := newDisplayRuneStream(info.resolvedPath, info.encoding)
	if err != nil {
		return nil, OutlineStats{}, false, err
	}
	defer closeFile.Close()

	startLine := 1
	endLine := info.fingerprint.LineCount
	if options.lineWindow != nil {
		startLine = options.lineWindow.StartLine
		endLine = options.lineWindow.EndLine
		if startLine > endLine {
			return []OutlineItem{}, OutlineStats{ItemsOmittedKnown: true}, false, nil
		}
		for line := 1; line < startLine; line++ {
			_, eof, err := readDisplayLine(ctx, stream)
			if err != nil {
				return nil, OutlineStats{}, false, err
			}
			if eof {
				return []OutlineItem{}, OutlineStats{ItemsOmittedKnown: true}, false, nil
			}
		}
	}

	items := []OutlineItem{}
	returned := 0
	lineNumber := startLine
	var pendingLine *genericTextPendingLine
	for lineNumber <= endLine {
		line, eof, err := readGenericTextLine(ctx, stream, &pendingLine)
		if err != nil {
			return nil, OutlineStats{}, false, err
		}
		if strings.TrimSpace(line) == "" {
			lineNumber++
			if eof {
				break
			}
			continue
		}
		item, nextLine, eof, err := readGenericTextBlock(ctx, stream, info, line, lineNumber, endLine, &pendingLine)
		if err != nil {
			return nil, OutlineStats{}, false, err
		}
		if outlineItemMatches(item, options) {
			if options.maxItems > 0 && returned >= options.maxItems {
				stats := OutlineStats{
					ItemsReturned:     returned,
					ItemsOmitted:      1,
					ItemsOmittedKnown: false,
					LastIncludedLine:  lastOutlineLine(items),
					NextOmittedLine:   item.Range.StartLine,
					NextOmittedKind:   item.Kind,
					NextOmittedName:   item.Name,
					TruncationReason:  "max_items",
				}
				return items, stats, true, nil
			}
			items = append(items, item)
			returned++
		}
		lineNumber = nextLine
		if eof {
			break
		}
	}
	stats := OutlineStats{
		ItemsReturned:     returned,
		ItemsOmittedKnown: true,
		LastIncludedLine:  lastOutlineLine(items),
	}
	return items, stats, false, nil
}

type genericTextPendingLine struct {
	line string
	eof  bool
}

func readGenericTextLine(ctx context.Context, stream *displayRuneStream, pending **genericTextPendingLine) (string, bool, error) {
	if pending != nil && *pending != nil {
		value := **pending
		*pending = nil
		return value.line, value.eof, nil
	}
	return readDisplayLine(ctx, stream)
}

func readGenericTextBlock(ctx context.Context, stream *displayRuneStream, info fileTextInfo, firstLine string, startLine, maxLine int, pending **genericTextPendingLine) (OutlineItem, int, bool, error) {
	byteCount := len(firstLine) + 1
	endLine := startLine
	nextLine := startLine + 1
	eof := false
	for lineCount := 1; lineCount < genericTextMaxBlockLines && byteCount < genericTextTargetBytes && nextLine <= maxLine; {
		next, nextEOF, err := readGenericTextLine(ctx, stream, pending)
		if err != nil {
			return OutlineItem{}, 0, false, err
		}
		if strings.TrimSpace(next) == "" {
			eof = nextEOF
			nextLine++
			break
		}
		nextBytes := len(next) + 1
		if byteCount+nextBytes > genericTextTargetBytes {
			if pending != nil {
				*pending = &genericTextPendingLine{line: next, eof: nextEOF}
			}
			break
		}
		byteCount += nextBytes
		lineCount++
		endLine = nextLine
		nextLine++
		eof = nextEOF
		if nextEOF {
			break
		}
	}
	item := OutlineItem{
		ID:               fmt.Sprintf("text_block:%d", startLine),
		Kind:             "text_block",
		Name:             genericTextBlockName(firstLine),
		Range:            SourceLineRange{StartLine: startLine, EndLine: endLine},
		Confidence:       "synthetic",
		RangeIsEstimated: false,
		RangeFingerprint: &info.fingerprint,
		Metadata: map[string]string{
			"parser_tier": "generic_text",
		},
	}
	return item, nextLine, eof, nil
}

func genericTextBlockName(line string) string {
	name := strings.Join(strings.Fields(line), " ")
	if name == "" {
		return "text block"
	}
	if utf8.RuneCountInString(name) <= 80 {
		return name
	}
	var b strings.Builder
	count := 0
	for _, r := range name {
		if count >= 80 {
			break
		}
		b.WriteRune(r)
		count++
	}
	return b.String()
}
