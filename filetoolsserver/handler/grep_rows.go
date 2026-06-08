package handler

import (
	"sort"
	"strings"
)

const (
	grepReadRangeExpansion = 2
	grepReadRangeMergeGap  = 3
	grepReadRangeMaxCount  = 3
)

type grepRowCollector struct {
	output       GrepOutput
	legacy       []string
	limit        int
	lineWindow   *SourceLineRange
	stats        GrepSearchStats
	groupOrder   []string
	groups       map[string]*grepFileGroupBuilder
	limitStopped bool
	fileCapSeen  bool
}

type grepFileGroupBuilder struct {
	path       string
	matchCount int
	rowCount   int
	firstLine  int
	lastLine   int
	matchLines map[int]struct{}
	matchRows  map[int]struct{}
	lines      map[int]struct{}
	capped     bool
	hasContext bool
}

func newGrepRowCollector(input GrepToolInput, displayPath string, contextBefore, contextAfter, limit int, dotEntriesSkipped bool) *grepRowCollector {
	patternMode := normalizedGrepPatternMode(input.PatternMode)
	return &grepRowCollector{
		limit:      limit,
		lineWindow: cloneSourceLineRange(input.LineWindow),
		groups:     map[string]*grepFileGroupBuilder{},
		stats: GrepSearchStats{
			Completed:         true,
			CountsAreComplete: true,
		},
		output: GrepOutput{
			Pattern:           input.Pattern,
			PatternMode:       patternMode,
			Path:              displayPath,
			OutputMode:        grepOutputMode(input),
			ContextBefore:     contextBefore,
			ContextAfter:      contextAfter,
			CaseInsensitive:   input.CaseInsensitive,
			Multiline:         input.Multiline,
			LineWindow:        cloneSourceLineRange(input.LineWindow),
			Limit:             limit,
			DotEntriesSkipped: dotEntriesSkipped,
			Matches:           []GrepMatch{},
			Files:             []string{},
			Counts:            []GrepCount{},
			FileGroups:        []GrepFileGroup{},
		},
	}
}

func (c *grepRowCollector) Add(row textRow) (bool, error) {
	if c.limit > 0 && c.output.RowCount >= c.limit {
		c.markLimitStopped()
		return false, nil
	}
	c.legacy = append(c.legacy, renderTextRow(row))
	switch row.Kind {
	case "match":
		matchDelta := 1
		if row.UseMatchDelta {
			matchDelta = row.MatchCountDelta
		}
		c.output.MatchCount += matchDelta
		c.output.RowCount++
		c.output.Matches = append(c.output.Matches, GrepMatch{
			Path: row.Path,
			Line: row.Line,
			Kind: row.Kind,
			Text: row.Text,
		})
		c.recordContentRow(row, matchDelta, false)
	case "context":
		c.output.RowCount++
		c.output.Matches = append(c.output.Matches, GrepMatch{
			Path: row.Path,
			Line: row.Line,
			Kind: row.Kind,
			Text: row.Text,
		})
		c.recordContentRow(row, 0, true)
	case "file":
		c.output.MatchCount++
		c.output.RowCount++
		c.output.Files = append(c.output.Files, row.Path)
	case "count":
		c.output.MatchCount += row.Count
		c.output.RowCount++
		c.output.Counts = append(c.output.Counts, GrepCount{
			Path:  row.Path,
			Count: row.Count,
		})
	}
	return true, nil
}

func (c *grepRowCollector) noteWalkStats(stats fileWalkStats) {
	c.output.DotEntriesSkipped = c.output.DotEntriesSkipped || stats.DotEntriesSkipped
	c.stats.FilesSeen += stats.FilesSeen
	c.stats.SkippedHidden += stats.SkippedHidden
	c.stats.SkippedIgnored += stats.SkippedIgnored
	c.stats.SkippedVCS += stats.SkippedVCS
	c.stats.SkippedUnreadable += stats.SkippedUnreadable
}

func (c *grepRowCollector) noteFileSeen() {
	c.stats.FilesSeen++
}

func (c *grepRowCollector) noteFileSearched() {
	c.stats.FilesSearched++
}

func (c *grepRowCollector) noteSkippedTypeOrGlob() {
	c.stats.SkippedTypeOrGlob++
}

func (c *grepRowCollector) noteSkippedBinary() {
	c.stats.SkippedBinary++
}

func (c *grepRowCollector) noteSkippedUnreadable() {
	c.stats.SkippedUnreadable++
}

func (c *grepRowCollector) noteFileResult(path string, result grepFileSearchResult) {
	if result.Searched {
		c.noteFileSearched()
	}
	if result.SkippedBinary {
		c.noteSkippedBinary()
	}
	if result.SkippedUnreadable {
		c.noteSkippedUnreadable()
	}
	if result.Capped {
		c.markFileCapped(path)
	}
}

func (c *grepRowCollector) markFileCapped(path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	c.fileCapSeen = true
	group := c.groupForPath(path)
	if !group.capped {
		group.capped = true
	}
}

func (c *grepRowCollector) markLimitStopped() {
	c.limitStopped = true
	c.output.Truncated = true
}

func (c *grepRowCollector) recordContentRow(row textRow, matchDelta int, isContext bool) {
	if row.Path == "" || row.Line < 1 {
		return
	}
	group := c.groupForPath(row.Path)
	group.rowCount++
	if matchDelta > 0 {
		group.matchCount += matchDelta
		group.matchLines[row.Line] = struct{}{}
	}
	if !isContext {
		group.matchRows[row.Line] = struct{}{}
	}
	if isContext {
		group.hasContext = true
	}
	if group.firstLine == 0 || row.Line < group.firstLine {
		group.firstLine = row.Line
	}
	if row.Line > group.lastLine {
		group.lastLine = row.Line
	}
	group.lines[row.Line] = struct{}{}
}

func (c *grepRowCollector) groupForPath(path string) *grepFileGroupBuilder {
	group := c.groups[path]
	if group == nil {
		group = &grepFileGroupBuilder{
			path:       path,
			matchLines: map[int]struct{}{},
			matchRows:  map[int]struct{}{},
			lines:      map[int]struct{}{},
		}
		c.groups[path] = group
		c.groupOrder = append(c.groupOrder, path)
	}
	return group
}

func (c *grepRowCollector) Finish() (GrepOutput, bool, error) {
	if len(c.legacy) > 0 {
		c.output.Text = strings.Join(c.legacy, "\n") + "\n"
	}
	c.output.FileGroups = c.finishFileGroups()
	c.finishStats()
	return c.output, c.output.MatchCount > 0, nil
}

func (c *grepRowCollector) finishFileGroups() []GrepFileGroup {
	if grepOutputModeFromOutput(c.output) != "content" {
		return []GrepFileGroup{}
	}
	groups := make([]GrepFileGroup, 0, len(c.groupOrder))
	for _, path := range c.groupOrder {
		group := c.groups[path]
		if group == nil || group.rowCount == 0 || group.matchCount == 0 {
			continue
		}
		groups = append(groups, GrepFileGroup{
			Path:       group.path,
			MatchCount: group.matchCount,
			RowCount:   group.rowCount,
			FirstLine:  group.firstLine,
			LastLine:   group.lastLine,
			ReadRanges: buildGrepGroupReadRanges(group, c.lineWindow),
			Capped:     group.capped,
		})
	}
	return groups
}

func (c *grepRowCollector) finishStats() {
	switch grepOutputModeFromOutput(c.output) {
	case "content":
		for _, group := range c.output.FileGroups {
			if group.MatchCount > 0 {
				c.stats.FilesWithMatches++
			}
			if group.Capped {
				c.stats.FilesCapped++
			}
		}
	case "files_with_matches":
		c.stats.FilesWithMatches = len(c.output.Files)
	case "count":
		c.stats.FilesWithMatches = len(c.output.Counts)
	}

	stopReason := ""
	switch {
	case c.limitStopped:
		stopReason = "limit"
	case c.fileCapSeen:
		stopReason = "file_cap"
	case c.stats.SkippedUnreadable > 0:
		stopReason = "unreadable"
	}
	c.stats.StopReason = stopReason
	c.stats.Completed = stopReason == ""
	c.stats.CountsAreComplete = c.stats.Completed
	if stopReason != "" {
		c.output.Truncated = true
	}
	c.output.SearchStats = &c.stats
}

func grepOutputModeFromOutput(output GrepOutput) string {
	if output.OutputMode == "" {
		return "content"
	}
	return output.OutputMode
}

func renderTextRow(row textRow) string {
	return row.Prefix + row.Body
}

func buildGrepReadRanges(lines map[int]struct{}, lineWindow *SourceLineRange) []SourceLineRange {
	if len(lines) == 0 {
		return []SourceLineRange{}
	}
	values := make([]int, 0, len(lines))
	for line := range lines {
		values = append(values, line)
	}
	sort.Ints(values)
	ranges := make([]SourceLineRange, 0, len(values))
	for _, line := range values {
		start := maxInt(1, line-grepReadRangeExpansion)
		end := line + grepReadRangeExpansion
		if lineWindow != nil {
			start = maxInt(start, lineWindow.StartLine)
			end = minInt(end, lineWindow.EndLine)
			if end < start {
				continue
			}
		}
		if len(ranges) == 0 {
			ranges = append(ranges, SourceLineRange{StartLine: start, EndLine: end})
			continue
		}
		last := &ranges[len(ranges)-1]
		if start <= last.EndLine+grepReadRangeMergeGap {
			if end > last.EndLine {
				last.EndLine = end
			}
			continue
		}
		ranges = append(ranges, SourceLineRange{StartLine: start, EndLine: end})
	}
	if len(ranges) > grepReadRangeMaxCount {
		ranges = ranges[:grepReadRangeMaxCount]
	}
	return ranges
}

func buildGrepGroupReadRanges(group *grepFileGroupBuilder, lineWindow *SourceLineRange) []SourceLineRange {
	if group == nil {
		return []SourceLineRange{}
	}
	if group.hasContext {
		return buildGrepReturnedReadRanges(group.lines, lineWindow)
	}
	return buildGrepMatchReadRanges(group.matchLines, group.matchRows, lineWindow)
}

func buildGrepMatchReadRanges(logicalMatchLines, returnedMatchRows map[int]struct{}, lineWindow *SourceLineRange) []SourceLineRange {
	if len(returnedMatchRows) > 0 {
		return buildGrepReadRanges(returnedMatchRows, lineWindow)
	}
	return buildGrepReadRanges(logicalMatchLines, lineWindow)
}

func buildGrepReturnedReadRanges(lines map[int]struct{}, lineWindow *SourceLineRange) []SourceLineRange {
	if len(lines) == 0 {
		return []SourceLineRange{}
	}
	values := make([]int, 0, len(lines))
	for line := range lines {
		values = append(values, line)
	}
	sort.Ints(values)
	ranges := make([]SourceLineRange, 0, len(values))
	for _, line := range values {
		start := line
		end := line
		if lineWindow != nil {
			start = maxInt(start, lineWindow.StartLine)
			end = minInt(end, lineWindow.EndLine)
			if end < start {
				continue
			}
		}
		if len(ranges) == 0 {
			ranges = append(ranges, SourceLineRange{StartLine: start, EndLine: end})
			continue
		}
		last := &ranges[len(ranges)-1]
		if start <= last.EndLine+grepReadRangeMergeGap {
			last.EndLine = end
			continue
		}
		ranges = append(ranges, SourceLineRange{StartLine: start, EndLine: end})
	}
	if len(ranges) > grepReadRangeMaxCount {
		ranges = ranges[:grepReadRangeMaxCount]
	}
	return ranges
}

func cloneSourceLineRange(value *SourceLineRange) *SourceLineRange {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
