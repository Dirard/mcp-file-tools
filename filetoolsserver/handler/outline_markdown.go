package handler

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

var markdownHeadingPattern = regexp.MustCompile(`^( {0,3})(#{1,6})(?:[ \t]+|$)(.*)$`)

const markdownWarningLimit = 50

type markdownHeading struct {
	level    int
	line     int
	end      int
	title    string
	kind     string
	storable bool
}

type markdownHeadingScan struct {
	headings             []markdownHeading
	warnings             []ToolWarning
	warningsOmitted      int
	truncated            bool
	storableHeadingCount int
	firstOmittedLine     int
	firstOmittedKind     string
	firstOmittedName     string
}

func (h *Handler) outlineMarkdown(ctx context.Context, info fileTextInfo, options outlineOptions) (OutlineFileOutput, error) {
	scan, err := scanMarkdownHeadingsFromFile(ctx, info, options)
	if err != nil {
		return OutlineFileOutput{}, err
	}
	source, err := readDecodedFileBytes(ctx, info.resolvedPath, info.encoding)
	if err != nil {
		return OutlineFileOutput{}, err
	}
	items := markdownHeadingsToTree(info, scan.headings, info.fingerprint.LineCount, source)
	sections, stats, truncated := finalizeOutlineItems(items, options)
	if scan.truncated {
		truncated = true
		stats.ItemsOmittedKnown = true
		stats.ItemsOmitted = scan.storableHeadingCount - stats.ItemsReturned
		if stats.ItemsOmitted < 1 {
			stats.ItemsOmitted = 1
			stats.ItemsOmittedKnown = false
		}
		stats.TruncationReason = "max_items"
		stats.NextOmittedLine = scan.firstOmittedLine
		stats.NextOmittedKind = scan.firstOmittedKind
		stats.NextOmittedName = scan.firstOmittedName
	}

	output := outlineBaseOutput(info, "ok", outlineLanguageMarkdown)
	output.ParserScope = "markdown_atx_headings"
	output.OutlineStats = stats
	output.Truncated = truncated
	output.Warnings = append(output.Warnings, scan.warnings...)
	if options.enclosingLine != nil {
		output.EnclosingItems = enclosingOutlineItems(items, *options.enclosingLine)
	}
	if options.includeSections {
		output.Sections = sections
	}
	return output, nil
}

func scanMarkdownHeadingsFromFile(ctx context.Context, info fileTextInfo, options outlineOptions) (markdownHeadingScan, error) {
	file, err := os.Open(info.resolvedPath)
	if err != nil {
		return markdownHeadingScan{}, err
	}
	defer file.Close()

	scan := markdownHeadingScan{
		headings: make([]markdownHeading, 0),
		warnings: make([]ToolWarning, 0),
	}
	storeLimit := markdownHeadingStoreLimit(options)
	reader := bufio.NewReader(decodedReader(file, info.encoding))
	inFence := false
	fenceMarker := byte(0)
	fenceLength := 0
	inFrontmatter := false
	frontmatterStart := 0
	lineNo := 0
	previousLine := ""
	for {
		if err := contextError(ctx); err != nil {
			return markdownHeadingScan{}, err
		}
		line, readErr := reader.ReadString('\n')
		if len(line) > 0 {
			lineNo++
			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")
			if lineNo == 1 && strings.TrimSpace(line) == "---" {
				inFrontmatter = true
				frontmatterStart = 1
				previousLine = line
				if readErr == nil {
					continue
				}
			}
			if inFrontmatter {
				if lineNo > frontmatterStart && strings.TrimSpace(line) == "---" {
					scan.record(markdownHeading{
						level: 0,
						line:  frontmatterStart,
						end:   lineNo,
						title: "frontmatter",
						kind:  "frontmatter",
					}, storeLimit, options)
					inFrontmatter = false
				}
				previousLine = line
				if readErr == nil {
					continue
				}
				if readErr == io.EOF {
					break
				}
			}
			newHeadings, newWarnings := scanMarkdownLine(line, previousLine, lineNo, &inFence, &fenceMarker, &fenceLength, info)
			for _, heading := range newHeadings {
				scan.record(heading, storeLimit, options)
			}
			scan.addWarnings(newWarnings...)
			previousLine = line
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return markdownHeadingScan{}, readErr
		}
	}
	if inFence {
		scan.addWarnings(ToolWarning{
			Code:    "unclosed_fence",
			Message: "Markdown fenced code block is not closed before EOF; headings after the fence are ignored.",
			File:    info.displayPath,
		})
	}
	scan.finalizeWarnings(info)
	return scan, nil
}

func (scan *markdownHeadingScan) addWarnings(warnings ...ToolWarning) {
	for _, warning := range warnings {
		if len(scan.warnings) < markdownWarningLimit {
			scan.warnings = append(scan.warnings, warning)
			continue
		}
		scan.warningsOmitted++
	}
}

func (scan *markdownHeadingScan) finalizeWarnings(info fileTextInfo) {
	if scan.warningsOmitted == 0 {
		return
	}
	scan.warnings = append(scan.warnings, ToolWarning{
		Code:    "markdown_warnings_truncated",
		Message: fmt.Sprintf("%d additional Markdown warnings were omitted to keep outline_file output bounded.", scan.warningsOmitted),
		File:    info.displayPath,
	})
}

func (scan *markdownHeadingScan) record(heading markdownHeading, storeLimit int, options outlineOptions) {
	closeMarkdownHeadingsAt(scan.headings, heading)
	scan.pruneBeforeWindow(options)
	heading.storable = markdownHeadingIsStorable(heading, options)
	if !heading.storable {
		if markdownHeadingCanEncloseFutureWindow(heading, options) {
			scan.headings = append(scan.headings, heading)
		}
		return
	}
	scan.storableHeadingCount++
	if storeLimit < 1 || countStoredMarkdownHeadings(scan.headings) < storeLimit {
		scan.headings = append(scan.headings, heading)
		return
	}
	scan.truncated = true
	if scan.firstOmittedLine == 0 {
		scan.firstOmittedLine = heading.line
		scan.firstOmittedKind = heading.kind
		scan.firstOmittedName = heading.title
	}
}

func (scan *markdownHeadingScan) pruneBeforeWindow(options outlineOptions) {
	if options.lineWindow == nil || len(scan.headings) == 0 {
		return
	}
	kept := scan.headings[:0]
	for _, heading := range scan.headings {
		if heading.end > 0 && heading.end < options.lineWindow.StartLine {
			if heading.storable && scan.storableHeadingCount > 0 {
				scan.storableHeadingCount--
			}
			continue
		}
		kept = append(kept, heading)
	}
	scan.headings = kept
}

func countStoredMarkdownHeadings(headings []markdownHeading) int {
	count := 0
	for _, heading := range headings {
		if heading.storable {
			count++
		}
	}
	return count
}

func closeMarkdownHeadingsAt(headings []markdownHeading, next markdownHeading) {
	for i := len(headings) - 1; i >= 0; i-- {
		if headings[i].end > 0 {
			continue
		}
		if headings[i].level == 0 || next.level <= headings[i].level {
			headings[i].end = next.line - 1
		}
	}
}

func markdownHeadingStoreLimit(options outlineOptions) int {
	if options.maxItems < 1 {
		return 0
	}
	return options.maxItems
}

func markdownHeadingIsStorable(heading markdownHeading, options outlineOptions) bool {
	if options.maxDepth > 0 && heading.level > options.maxDepth {
		return false
	}
	if options.lineWindow != nil {
		switch {
		case heading.line > options.lineWindow.EndLine:
			return false
		case heading.line < options.lineWindow.StartLine && !markdownHeadingCanEncloseFutureWindow(heading, options):
			return false
		}
	}
	if strings.TrimSpace(options.nameContains) != "" {
		needle := strings.ToLower(strings.TrimSpace(options.nameContains))
		if !strings.Contains(strings.ToLower(heading.title), needle) {
			return false
		}
	}
	if len(options.kinds) > 0 {
		for _, kind := range options.kinds {
			if strings.EqualFold(strings.TrimSpace(kind), heading.kind) {
				return true
			}
		}
		return false
	}
	return true
}

func markdownHeadingCanEncloseFutureWindow(heading markdownHeading, options outlineOptions) bool {
	if options.lineWindow == nil {
		return false
	}
	if heading.level <= 0 || heading.line >= options.lineWindow.StartLine {
		return false
	}
	if options.maxDepth > 0 && heading.level > options.maxDepth {
		return false
	}
	if strings.TrimSpace(options.nameContains) != "" || len(options.kinds) > 0 {
		return false
	}
	return true
}

func scanMarkdownLine(line, previousLine string, lineNo int, inFence *bool, fenceMarker *byte, fenceLength *int, info fileTextInfo) ([]markdownHeading, []ToolWarning) {
	headings := []markdownHeading{}
	warnings := []ToolWarning{}
	trimmedLeft := strings.TrimLeft(line, " ")
	leadingSpaces := len(line) - len(trimmedLeft)
	if leadingSpaces <= 3 {
		if marker, length, ok := markdownFenceMarker(trimmedLeft); ok {
			if !*inFence {
				*inFence = true
				*fenceMarker = marker
				*fenceLength = length
			} else if marker == *fenceMarker && length >= *fenceLength && markdownFenceCloserHasOnlyWhitespace(trimmedLeft[length:]) {
				*inFence = false
				*fenceMarker = 0
				*fenceLength = 0
			}
			return headings, warnings
		}
	}
	if *inFence {
		return headings, warnings
	}
	match := markdownHeadingPattern.FindStringSubmatch(line)
	if match == nil {
		if lineNo > 1 && strings.TrimSpace(line) != "" && markdownLooksLikeSetextLine(previousLine, line) {
			warnings = append(warnings, ToolWarning{
				Code:    "setext_headings_unsupported",
				Message: "Setext-style Markdown headings are not included in the exact ATX outline.",
				File:    info.displayPath,
				Line:    lineNo,
			})
		}
		return headings, warnings
	}
	title := cleanMarkdownHeadingTitle(match[3])
	if title == "" {
		title = strings.Repeat("#", len(match[2]))
	}
	headings = append(headings, markdownHeading{
		level: len(match[2]),
		line:  lineNo,
		title: title,
		kind:  "section",
	})
	return headings, warnings
}

func markdownFenceMarker(trimmedLeft string) (byte, int, bool) {
	if len(trimmedLeft) < 3 {
		return 0, 0, false
	}
	marker := trimmedLeft[0]
	if marker != '`' && marker != '~' {
		return 0, 0, false
	}
	length := 0
	for length < len(trimmedLeft) && trimmedLeft[length] == marker {
		length++
	}
	if length < 3 {
		return 0, 0, false
	}
	return marker, length, true
}

func markdownFenceCloserHasOnlyWhitespace(rest string) bool {
	for _, r := range rest {
		if r != ' ' && r != '\t' {
			return false
		}
	}
	return true
}

func markdownLooksLikeSetextLine(previousLine, line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	for _, r := range line {
		if r != '=' && r != '-' {
			return false
		}
	}
	prev := strings.TrimSpace(previousLine)
	return prev != "" && !strings.HasPrefix(prev, "#")
}

func cleanMarkdownHeadingTitle(raw string) string {
	title := strings.TrimSpace(raw)
	title = strings.TrimRight(title, " \t")
	if strings.HasSuffix(title, "#") {
		trimmed := strings.TrimRight(title, "#")
		if strings.HasSuffix(trimmed, " ") || strings.HasSuffix(trimmed, "\t") || strings.TrimSpace(trimmed) == "" {
			title = strings.TrimSpace(trimmed)
		}
	}
	return title
}

func markdownHeadingsToTree(info fileTextInfo, headings []markdownHeading, totalLines int, source []byte) []OutlineItem {
	if len(headings) == 0 {
		return []OutlineItem{}
	}
	items := make([]OutlineItem, len(headings))
	for i, heading := range headings {
		endLine := totalLines
		if heading.end > 0 {
			endLine = heading.end
		} else {
			for j := i + 1; j < len(headings); j++ {
				if headings[j].level <= heading.level && heading.level > 0 {
					endLine = headings[j].line - 1
					break
				}
				if heading.level == 0 {
					endLine = headings[j].line - 1
					break
				}
			}
		}
		items[i] = exactOutlineItem(info, OutlineItem{
			ID:    fmt.Sprintf("markdown:%s:%d:%d:%s", heading.kind, heading.line, endLine, sanitizeOutlineIDPart(heading.title)),
			Kind:  heading.kind,
			Name:  heading.title,
			Range: SourceLineRange{StartLine: heading.line, EndLine: endLine},
			Depth: heading.level,
		})
	}

	roots := make([]OutlineItem, 0)
	stack := []int{}
	paths := make([][]string, len(items))
	for i := range items {
		level := headings[i].level
		if level == 0 {
			stack = stack[:0]
			paths[i] = []string{items[i].Name}
			continue
		}
		for len(stack) > 0 && headings[stack[len(stack)-1]].level >= level {
			stack = stack[:len(stack)-1]
		}
		path := make([]string, 0, len(stack)+1)
		for _, parent := range stack {
			path = append(path, items[parent].Name)
		}
		path = append(path, items[i].Name)
		paths[i] = path
		stack = append(stack, i)
	}
	for i := range items {
		items[i].Path = paths[i]
		byteRange := lineByteRangeForSource(source, items[i].Range)
		items[i] = exactOutlineItemWithSelector(info, outlineLanguageMarkdown, items[i], byteRange, true, true, "")
	}

	var attach func(index int) OutlineItem
	attach = func(index int) OutlineItem {
		item := items[index]
		for child := index + 1; child < len(items); child++ {
			if len(paths[child]) == len(paths[index])+1 && hasPathPrefix(paths[child], paths[index]) {
				item.Children = append(item.Children, attach(child))
			}
			if headings[child].level <= headings[index].level && headings[index].level > 0 {
				break
			}
		}
		return item
	}
	for i := range items {
		if len(paths[i]) == 1 {
			roots = append(roots, attach(i))
		}
	}
	return roots
}

func hasPathPrefix(path, prefix []string) bool {
	if len(prefix) > len(path) {
		return false
	}
	for i := range prefix {
		if path[i] != prefix[i] {
			return false
		}
	}
	return true
}

func sanitizeOutlineIDPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_':
			return r
		default:
			return '-'
		}
	}, value)
	value = strings.Trim(value, "-")
	if value == "" {
		return "unnamed"
	}
	return value
}
