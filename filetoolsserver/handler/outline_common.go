package handler

import (
	"bufio"
	"context"
	"io"
	"os"
	"strings"
)

func readDecodedFileLines(ctx context.Context, path string, encResult encodingResult) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := bufio.NewReader(decodedReader(file, encResult))
	var lines []string
	sawAny := false
	endedWithLineBreak := false
	for {
		if err := contextError(ctx); err != nil {
			return nil, err
		}
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			sawAny = true
			endedWithLineBreak = strings.HasSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")
			lines = append(lines, line)
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
	}
	if sawAny && endedWithLineBreak {
		lines = append(lines, "")
	}
	return lines, nil
}

func readDecodedFileBytes(ctx context.Context, path string, encResult encodingResult) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := decodedReader(file, encResult)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	return data, nil
}

func lineByteRangeForSource(source []byte, r SourceLineRange) SourceByteRange {
	if r.StartLine < 1 {
		r.StartLine = 1
	}
	if r.EndLine < r.StartLine {
		r.EndLine = r.StartLine
	}
	start := lineStartByte(source, r.StartLine)
	end := lineStartByte(source, r.EndLine+1)
	if end == len(source) && r.EndLine >= 1 && len(source) > 0 && source[len(source)-1] == '\n' {
		return SourceByteRange{StartByte: start, EndByteExclusive: len(source)}
	}
	return SourceByteRange{StartByte: start, EndByteExclusive: end}
}

func lineStartByte(source []byte, oneBasedLine int) int {
	if oneBasedLine <= 1 {
		return 0
	}
	line := 1
	for i, b := range source {
		if b == '\n' {
			line++
			if line == oneBasedLine {
				return i + 1
			}
		}
	}
	return len(source)
}

func finalizeOutlineItems(items []OutlineItem, options outlineOptions) ([]OutlineItem, OutlineStats, bool) {
	filtered := filterOutlineItems(items, options)
	flat := flattenOutlineItems(filtered)
	total := countOutlineItems(filtered)
	limited, returned := limitOutlineItems(filtered, options.maxItems)
	omitted := total - returned
	stats := OutlineStats{
		ItemsReturned:     returned,
		ItemsOmittedKnown: true,
		ItemsOmitted:      omitted,
	}
	if returned > 0 {
		stats.LastIncludedLine = lastOutlineLine(limited)
	}
	if omitted > 0 {
		stats.TruncationReason = "max_items"
		if returned < len(flat) {
			next := flat[returned]
			stats.NextOmittedLine = next.Range.StartLine
			stats.NextOmittedKind = next.Kind
			stats.NextOmittedName = next.Name
		}
	}
	return limited, stats, omitted > 0
}

func finalizeOutlineCategories(imports, symbols, sections []OutlineItem, options outlineOptions) ([]OutlineItem, []OutlineItem, []OutlineItem, OutlineStats, bool) {
	filteredImports := filterOutlineItems(imports, options)
	filteredSymbols := filterOutlineItems(symbols, options)
	filteredSections := filterOutlineItems(sections, options)
	remaining := options.maxItems
	unbounded := options.maxItems < 1
	outputImports := []OutlineItem{}
	outputSymbols := []OutlineItem{}
	outputSections := []OutlineItem{}
	if options.includeImports {
		var returned int
		outputImports, returned = limitOutlineCategoryItems(filteredImports, remaining, unbounded)
		remaining -= returned
	}
	if options.includeSymbols {
		var returned int
		outputSymbols, returned = limitOutlineCategoryItems(filteredSymbols, remaining, unbounded)
		remaining -= returned
	}
	if options.includeSections {
		var returned int
		outputSections, returned = limitOutlineCategoryItems(filteredSections, remaining, unbounded)
		remaining -= returned
	}
	returned := countOutlineItems(outputImports) + countOutlineItems(outputSymbols) + countOutlineItems(outputSections)
	total := 0
	flat := []OutlineItem{}
	if options.includeImports {
		total += countOutlineItems(filteredImports)
		flat = append(flat, flattenOutlineItems(filteredImports)...)
	}
	if options.includeSymbols {
		total += countOutlineItems(filteredSymbols)
		flat = append(flat, flattenOutlineItems(filteredSymbols)...)
	}
	if options.includeSections {
		total += countOutlineItems(filteredSections)
		flat = append(flat, flattenOutlineItems(filteredSections)...)
	}
	omitted := total - returned
	stats := OutlineStats{
		ItemsReturned:     returned,
		ItemsOmitted:      omitted,
		ItemsOmittedKnown: true,
	}
	if returned > 0 {
		stats.LastIncludedLine = maxInt(lastOutlineLine(outputImports), maxInt(lastOutlineLine(outputSymbols), lastOutlineLine(outputSections)))
	}
	if omitted > 0 {
		stats.TruncationReason = "max_items"
		if returned < len(flat) {
			next := flat[returned]
			stats.NextOmittedLine = next.Range.StartLine
			stats.NextOmittedKind = next.Kind
			stats.NextOmittedName = next.Name
		}
	}
	return outputImports, outputSymbols, outputSections, stats, omitted > 0
}

func limitOutlineCategoryItems(items []OutlineItem, remaining int, unbounded bool) ([]OutlineItem, int) {
	if unbounded {
		return limitOutlineItems(items, 0)
	}
	if remaining <= 0 {
		return []OutlineItem{}, 0
	}
	return limitOutlineItemsBounded(items, remaining)
}

func exactOutlineItem(info fileTextInfo, item OutlineItem) OutlineItem {
	item.Confidence = "exact"
	item.RangeIsEstimated = false
	item.RangeFingerprint = &info.fingerprint
	return item
}

func exactOutlineItemWithSelector(info fileTextInfo, language string, item OutlineItem, byteRange SourceByteRange, wholeLineRange, writeSafe bool, refusalReason string) OutlineItem {
	if len(item.Path) == 0 {
		item.Path = []string{item.Name}
	}
	symbolRef := outlineSymbolRef(info.fingerprint, language, item.Kind, item.Name, item.Path, item.Range, byteRange)
	item.ByteRange = &byteRange
	item.SymbolRef = symbolRef
	item.WholeLineRange = outlineBoolPtr(wholeLineRange)
	item.WriteSafe = outlineBoolPtr(writeSafe)
	item.RefusalReason = refusalReason
	item.Selector = &OutlineSelector{
		Language:         language,
		Kind:             item.Kind,
		Name:             item.Name,
		SymbolPath:       append([]string(nil), item.Path...),
		Range:            item.Range,
		ByteRange:        byteRange,
		WholeLineRange:   wholeLineRange,
		WriteSafe:        writeSafe,
		RangeFingerprint: info.fingerprint,
		SymbolRef:        symbolRef,
		Disambiguator:    symbolDisambiguator(symbolRef),
	}
	return exactOutlineItem(info, item)
}

func outlineSymbolRef(fingerprint FileFingerprint, language, kind, name string, symbolPath []string, r SourceLineRange, byteRange SourceByteRange) string {
	hash := canonicalHash(map[string]any{
		"fingerprint":  fingerprint,
		"language":     language,
		"kind":         kind,
		"name":         name,
		"symbol_path":  symbolPath,
		"range":        r,
		"byte_range":   byteRange,
		"selector_api": "phase6",
	})[:24]
	return language + ":" + kind + ":" + hash
}

func symbolDisambiguator(symbolRef string) string {
	if len(symbolRef) <= 12 {
		return symbolRef
	}
	return symbolRef[len(symbolRef)-12:]
}

func filterOutlineItems(items []OutlineItem, options outlineOptions) []OutlineItem {
	filtered := make([]OutlineItem, 0, len(items))
	for _, item := range items {
		item.Children = filterOutlineItems(item.Children, options)
		matches := outlineItemMatches(item, options)
		if matches || len(item.Children) > 0 {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func outlineItemMatches(item OutlineItem, options outlineOptions) bool {
	if options.maxDepth > 0 && item.Depth > options.maxDepth {
		return false
	}
	if options.lineWindow != nil && !rangesIntersect(item.Range, *options.lineWindow) {
		return false
	}
	if strings.TrimSpace(options.nameContains) != "" {
		needle := strings.ToLower(strings.TrimSpace(options.nameContains))
		if !strings.Contains(strings.ToLower(item.Name), needle) && !strings.Contains(strings.ToLower(item.Detail), needle) {
			return false
		}
	}
	if len(options.kinds) > 0 {
		found := false
		for _, kind := range options.kinds {
			if strings.EqualFold(strings.TrimSpace(kind), item.Kind) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func rangesIntersect(a, b SourceLineRange) bool {
	return a.StartLine <= b.EndLine && b.StartLine <= a.EndLine
}

func enclosingOutlineItems(items []OutlineItem, line int) []OutlineItem {
	if line < 1 {
		return []OutlineItem{}
	}
	chain := bestEnclosingOutlineChain(items, line)
	if len(chain) == 0 {
		return []OutlineItem{}
	}
	out := make([]OutlineItem, 0, len(chain))
	for i := len(chain) - 1; i >= 0; i-- {
		item := chain[i]
		item.Children = nil
		out = append(out, item)
	}
	return out
}

func bestEnclosingOutlineChain(items []OutlineItem, line int) []OutlineItem {
	var best []OutlineItem
	for _, item := range items {
		if line < item.Range.StartLine || line > item.Range.EndLine {
			continue
		}
		childChain := bestEnclosingOutlineChain(item.Children, line)
		var candidate []OutlineItem
		if len(childChain) > 0 {
			candidate = append([]OutlineItem{item}, childChain...)
		} else {
			candidate = []OutlineItem{item}
		}
		if betterEnclosingOutlineChain(candidate, best) {
			best = candidate
		}
	}
	return best
}

func betterEnclosingOutlineChain(candidate, current []OutlineItem) bool {
	if len(candidate) == 0 {
		return false
	}
	if len(current) == 0 {
		return true
	}
	candidateInner := candidate[len(candidate)-1]
	currentInner := current[len(current)-1]
	candidateSpan := rangeLineSpan(candidateInner.Range)
	currentSpan := rangeLineSpan(currentInner.Range)
	if candidateSpan != currentSpan {
		return candidateSpan < currentSpan
	}
	return len(candidate) > len(current)
}

func countOutlineItems(items []OutlineItem) int {
	count := 0
	for _, item := range items {
		count++
		count += countOutlineItems(item.Children)
	}
	return count
}

func limitOutlineItems(items []OutlineItem, maxItems int) ([]OutlineItem, int) {
	if maxItems < 1 {
		return items, countOutlineItems(items)
	}
	return limitOutlineItemsBounded(items, maxItems)
}

func limitOutlineItemsBounded(items []OutlineItem, maxItems int) ([]OutlineItem, int) {
	if maxItems <= 0 {
		return []OutlineItem{}, 0
	}
	limited := make([]OutlineItem, 0, len(items))
	returned := 0
	for _, item := range items {
		if returned >= maxItems {
			break
		}
		itemCopy := item
		returned++
		remaining := maxItems - returned
		itemCopy.Children, _ = limitOutlineItemsBounded(item.Children, remaining)
		returned += countOutlineItems(itemCopy.Children)
		limited = append(limited, itemCopy)
	}
	return limited, returned
}

func lastOutlineLine(items []OutlineItem) int {
	last := 0
	for _, item := range items {
		if item.Range.EndLine > last {
			last = item.Range.EndLine
		}
		if childLast := lastOutlineLine(item.Children); childLast > last {
			last = childLast
		}
	}
	return last
}

func flattenOutlineItems(items []OutlineItem) []OutlineItem {
	flat := make([]OutlineItem, 0, countOutlineItems(items))
	for _, item := range items {
		itemCopy := item
		children := itemCopy.Children
		itemCopy.Children = nil
		flat = append(flat, itemCopy)
		flat = append(flat, flattenOutlineItems(children)...)
	}
	return flat
}
