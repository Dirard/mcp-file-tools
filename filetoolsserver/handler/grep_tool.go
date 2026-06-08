package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxGrepContextLines = 1000

// HandleGrepTool searches file contents and formats results like ripgrep.
func (h *Handler) HandleGrepTool(ctx context.Context, req *mcp.CallToolRequest, input GrepToolInput) (*mcp.CallToolResult, GrepOutput, error) {
	if input.invalid != "" {
		return errorResult(input.invalid), GrepOutput{Error: input.invalid, ErrorCode: "vcs_content_traversal_unsupported", Matches: []GrepMatch{}, Files: []string{}, Counts: []GrepCount{}, FileGroups: []GrepFileGroup{}}, nil
	}
	if input.Pattern == "" {
		return toolError[GrepOutput]("pattern is required.\n\nExample: grep(pattern=\"func.*Handler\")")
	}
	pathCtx, cwdErr := h.BuildPathContext(input.CwdID)
	if cwdErr != nil {
		return toolCwdError[GrepOutput](cwdErr)
	}
	limit, err := effectiveOptionalLimit(input.Limit, defaultSearchLimit)
	if err != nil {
		return toolError[GrepOutput](err.Error())
	}
	redactionMode, err := normalizeRedactionMode(input.RedactionMode)
	if err != nil {
		return errorResult(err.Error()), GrepOutput{Error: err.Error(), ErrorCode: "invalid_redaction_mode", Matches: []GrepMatch{}, Files: []string{}, Counts: []GrepCount{}, FileGroups: []GrepFileGroup{}}, nil
	}
	mode := grepOutputMode(input)
	if !isSupportedGrepOutputMode(input.OutputMode) {
		return toolError[GrepOutput](fmt.Sprintf("Invalid output_mode %q.\n\nUse one of: content, files_with_matches, count.", input.OutputMode))
	}
	if input.Before < 0 || input.After < 0 || input.Context < 0 {
		return toolError[GrepOutput]("Context parameters before, after and context cannot be negative.")
	}
	if input.Before > maxGrepContextLines || input.After > maxGrepContextLines || input.Context > maxGrepContextLines {
		return toolError[GrepOutput](fmt.Sprintf("Context parameters before, after and context cannot exceed %d lines.", maxGrepContextLines))
	}
	patternMode, err := validateGrepPatternMode(input.PatternMode)
	if err != nil {
		return toolError[GrepOutput](err.Error())
	}
	maxMatchesPerFile := 0
	if input.MaxMatchesPerFile != nil {
		if mode != "content" {
			return toolError[GrepOutput]("max_matches_per_file is supported only when output_mode is content.")
		}
		if *input.MaxMatchesPerFile < 1 {
			return toolError[GrepOutput]("max_matches_per_file must be >= 1")
		}
		maxMatchesPerFile = *input.MaxMatchesPerFile
	}
	if input.LineWindow != nil && (input.LineWindow.StartLine < 1 || input.LineWindow.EndLine < input.LineWindow.StartLine) {
		return toolError[GrepOutput]("line_window must use 1-based start_line/end_line and end_line >= start_line")
	}
	re, err := compileGrepPattern(input.Pattern, patternMode, input.CaseInsensitive, input.Multiline)
	if err != nil {
		return toolError[GrepOutput](fmt.Sprintf("Invalid regex pattern %q: %v\n\nCheck escaping. Go regexp syntax is used.", input.Pattern, err))
	}
	resolvedPath, displayRoot, err := h.resolveToolPath(pathCtx, input.Path, "path")
	if err != nil {
		return toolError[GrepOutput](fmt.Sprintf("Cannot search path: %v", err))
	}
	if input.LineWindow != nil {
		info, statErr := os.Stat(resolvedPath)
		if statErr != nil {
			return toolError[GrepOutput](fmt.Sprintf("Cannot search path: %v", statErr))
		}
		if info.IsDir() {
			return toolError[GrepOutput]("line_window can be used only when path resolves to a file.")
		}
	}
	roots := []string{resolvedPath}
	releaseTool, err := h.acquireToolCall(ctx)
	if err != nil {
		return toolError[GrepOutput](limiterWaitError("tool call", err))
	}
	defer releaseTool()
	releaseScan, err := h.acquireScan(ctx)
	if err != nil {
		return toolError[GrepOutput](limiterWaitError("scan", err))
	}
	defer releaseScan()

	ignoreGlobs := grepTraversalIgnoreGlobs(input.IgnoreGlobs)
	options := grepSearchOptions{
		Mode:              mode,
		PatternMode:       patternMode,
		Multiline:         input.Multiline,
		ContextBefore:     grepContextBefore(input),
		ContextAfter:      grepContextAfter(input),
		LineWindow:        input.LineWindow,
		MaxMatchesPerFile: maxMatchesPerFile,
		ResolvedRoot:      roots[0],
		RequestedRoot:     input.Path,
	}

	output, hasMatches, err := h.grepOutputStreaming(ctx, pathCtx, roots, displayRoot, re, input, ignoreGlobs, limit, options)
	if err != nil {
		return toolError[GrepOutput](err.Error())
	}
	output.IncludeHidden = input.IncludeHidden
	output.RedactionMode = redactionMode
	h.redactGrepOutput(&output, input, options, redactionMode)
	h.finalizeGrepOutput(pathCtx, &output, input, options, hasMatches)
	if !hasMatches {
		return structuredResultOnly(), output, nil
	}
	return structuredResultOnly(), output, nil
}

type grepSearchOptions struct {
	Mode              string
	PatternMode       string
	Multiline         bool
	ContextBefore     int
	ContextAfter      int
	LineWindow        *SourceLineRange
	MaxMatchesPerFile int
	ResolvedRoot      string
	RequestedRoot     string
}

func validateGrepPatternMode(mode string) (string, error) {
	switch strings.TrimSpace(mode) {
	case "", "regex":
		return "regex", nil
	case "literal":
		return "literal", nil
	default:
		return "", fmt.Errorf("Invalid pattern_mode %q. Use regex or literal.", mode)
	}
}

func normalizedGrepPatternMode(mode string) string {
	normalized, err := validateGrepPatternMode(mode)
	if err != nil {
		return ""
	}
	return normalized
}

func compileGrepPattern(pattern, patternMode string, caseInsensitive, multiline bool) (*regexp.Regexp, error) {
	if patternMode == "literal" {
		pattern = regexp.QuoteMeta(pattern)
	}
	prefix := ""
	if caseInsensitive {
		prefix += "(?i)"
	}
	if multiline {
		prefix += "(?s)"
	}
	return regexp.Compile(prefix + pattern)
}

func isSupportedGrepOutputMode(mode string) bool {
	switch mode {
	case "", "content", "files_with_matches", "count":
		return true
	default:
		return false
	}
}

func grepOutputMode(input GrepToolInput) string {
	if input.OutputMode == "" {
		return "content"
	}
	return input.OutputMode
}

func grepContextBefore(input GrepToolInput) int {
	before, _ := grepContext(input)
	return before
}

func grepContextAfter(input GrepToolInput) int {
	_, after := grepContext(input)
	return after
}

func grepNoMatchesMessage(pattern string) string {
	return fmt.Sprintf("No matches found for pattern %q.\n\nTry changing pattern, using case_insensitive=true, or adjusting path/type/glob/ignore_globs filters. For broad trees, narrow the search with type=\"go\", glob=\"*.{go,ts}\", or ignore_globs=[\"node_modules/**\",\"vendor/**\"].\n", pattern)
}

func grepTraversalIgnoreGlobs(ignoreGlobs []string) []string {
	return append([]string(nil), ignoreGlobs...)
}

type grepFileFilter struct {
	useGlob  bool
	glob     compiledGlobMatcher
	fileType string
	root     string
}

func newGrepFileFilter(input GrepToolInput, root string) grepFileFilter {
	return grepFileFilter{
		useGlob:  strings.TrimSpace(input.Glob) != "",
		glob:     newCompiledGlobMatcher([]string{input.Glob}),
		fileType: input.Type,
		root:     root,
	}
}

func (f grepFileFilter) matches(file string) bool {
	relativeFile := relativeGlobCandidate(file, f.root)
	if f.useGlob && !f.glob.matches(relativeFile) {
		return false
	}
	if f.fileType != "" && !matchesFileType(file, f.fileType) {
		return false
	}
	return true
}

func (h *Handler) grepOutputStreaming(ctx context.Context, pathCtx PathContext, roots []string, displayRoot string, re *regexp.Regexp, input GrepToolInput, ignoreGlobs []string, limit int, options grepSearchOptions) (GrepOutput, bool, error) {
	filter := newGrepFileFilter(input, options.ResolvedRoot)
	collector := newGrepRowCollector(input, displayRoot, options.ContextBefore, options.ContextAfter, limit, false)
	emit := func(row textRow) (bool, error) {
		return collector.Add(row)
	}
	if options.LineWindow != nil {
		collector.noteFileSeen()
		if !filter.matches(options.ResolvedRoot) {
			collector.noteSkippedTypeOrGlob()
			return collector.Finish()
		}
		displayFile := h.projectSearchPath(pathCtx, options.ResolvedRoot, input.Path, options.ResolvedRoot)
		keepGoing, result, err := h.grepSearchFile(ctx, options.ResolvedRoot, displayFile, re, options, emit)
		if err != nil {
			return GrepOutput{}, false, err
		}
		collector.noteFileResult(displayFile, result)
		if !keepGoing {
			return collector.Finish()
		}
		return collector.Finish()
	}
	walkStats, err := h.walkFilesystemFilesWithPolicyStats(ctx, pathCtx, roots, input.IncludeHidden, false, ignoreGlobs, func(file string) (bool, error) {
		if !filter.matches(file) {
			collector.noteSkippedTypeOrGlob()
			return true, nil
		}
		displayFile := h.projectSearchPath(pathCtx, file, input.Path, options.ResolvedRoot)
		keepGoing, result, err := h.grepSearchFile(ctx, file, displayFile, re, options, emit)
		if err != nil {
			return false, err
		}
		collector.noteFileResult(displayFile, result)
		return keepGoing, nil
	})
	if err != nil {
		return GrepOutput{}, false, err
	}
	collector.noteWalkStats(walkStats)
	return collector.Finish()
}

func (h *Handler) redactGrepOutput(output *GrepOutput, input GrepToolInput, options grepSearchOptions, mode string) {
	if output == nil || output.OutputMode != "content" {
		return
	}
	broad := !h.grepSingleFileScope(options)
	changedAny := false
	for i := range output.Matches {
		match := &output.Matches[i]
		risky := shouldRedactContent(mode, match.Path, broad, input.IncludeHidden)
		if mode == redactionAuto && !risky {
			match.RedactionMode = mode
			continue
		}
		redacted, changed := redactString(match.Text, mode, risky)
		if changed {
			match.Text = redacted
			match.Redacted = true
			match.RedactionMode = mode
			changedAny = changedAny || changed
		} else if mode == redactionStrict || risky {
			match.RedactionMode = mode
		}
	}
	if changedAny {
		output.Text = renderGrepContentText(output.Matches)
	}
}

func renderGrepContentText(matches []GrepMatch) string {
	if len(matches) == 0 {
		return ""
	}
	var b strings.Builder
	for _, match := range matches {
		sep := ":"
		if match.Kind == "context" {
			sep = "-"
		}
		b.WriteString(fmt.Sprintf("%s:%d%s%s\n", match.Path, match.Line, sep, match.Text))
	}
	return b.String()
}

type grepRowEmitter func(textRow) (bool, error)

type grepFileSearchResult struct {
	Searched          bool
	SkippedBinary     bool
	SkippedUnreadable bool
	Capped            bool
}

type grepFileTextClass int

const (
	grepFileText grepFileTextClass = iota
	grepFileBinary
	grepFileUnreadable
)

func (h *Handler) grepSearchFile(ctx context.Context, file, displayFile string, re *regexp.Regexp, options grepSearchOptions, emit grepRowEmitter) (bool, grepFileSearchResult, error) {
	class := classifyGrepFileForSearch(file)
	switch class {
	case grepFileBinary:
		return true, grepFileSearchResult{SkippedBinary: true}, nil
	case grepFileUnreadable:
		return true, grepFileSearchResult{SkippedUnreadable: true}, nil
	}
	if options.Multiline && options.LineWindow == nil && isFileLargerThan(file, h.config.MemoryThreshold) {
		size := int64(0)
		if info, err := os.Stat(file); err == nil {
			size = info.Size()
		}
		return false, grepFileSearchResult{}, fmt.Errorf("multiline grep requires loading each searched file into memory, and %q is %d bytes (threshold %d).\n\nNarrow path/type/glob/ignore_globs, use multiline=false for line-by-line search, or increase MCP_MEMORY_THRESHOLD if this is intentional.", displayFile, size, h.config.MemoryThreshold)
	}
	if options.Multiline && options.LineWindow != nil && isFileLargerThan(file, h.config.MemoryThreshold) {
		keepGoing, result, err := h.grepLargeMultilineLineWindowRows(ctx, file, displayFile, re, options, emit)
		return keepGoing, result, err
	}
	if !options.Multiline && isFileLargerThan(file, h.config.MemoryThreshold) {
		keepGoing, result, err := h.grepLargeFileRows(ctx, file, displayFile, re, options, emit)
		return keepGoing, result, err
	}
	keepGoing, result, err := h.grepSmallFileRows(ctx, file, displayFile, re, options, emit)
	return keepGoing, result, err
}

func classifyGrepFileForSearch(file string) grepFileTextClass {
	f, err := os.Open(file)
	if err != nil {
		return grepFileUnreadable
	}
	defer f.Close()
	sample := make([]byte, binaryCheckSize)
	n, err := f.Read(sample)
	if err != nil && err != io.EOF {
		return grepFileUnreadable
	}
	if hasUnicodeTextBOM(sample[:n]) {
		return grepFileText
	}
	if isBinaryFile(sample[:n]) {
		return grepFileBinary
	}
	return grepFileText
}

func (h *Handler) grepLargeMultilineLineWindowRows(ctx context.Context, file, displayFile string, re *regexp.Regexp, options grepSearchOptions, emit grepRowEmitter) (bool, grepFileSearchResult, error) {
	content, lineOffset, selectedLines, unreadable, err := h.readGrepLineWindowContent(ctx, file, displayFile, options.LineWindow)
	if unreadable {
		return true, grepFileSearchResult{SkippedUnreadable: true}, nil
	}
	if err != nil {
		return false, grepFileSearchResult{}, err
	}
	if selectedLines == 0 {
		return true, grepFileSearchResult{Searched: true}, nil
	}
	keepGoing, capped, err := grepMultilineRowsForFile(ctx, displayFile, content, re, options, lineOffset, emit)
	return keepGoing, grepFileSearchResult{Searched: true, Capped: capped}, err
}

func (h *Handler) readGrepLineWindowContent(ctx context.Context, file, displayFile string, window *SourceLineRange) (string, int, int, bool, error) {
	if window == nil {
		return "", 0, 0, false, nil
	}
	encResult, err := h.resolveEncoding("", file)
	if err != nil {
		return "", 0, 0, true, nil
	}
	stream, closeFile, err := newDisplayRuneStream(file, encResult)
	if err != nil {
		return "", 0, 0, true, nil
	}
	defer closeFile.Close()
	reader := newGrepDisplayLineReader(stream)
	var b strings.Builder
	lineNumber := 0
	selected := 0
	for {
		if err := contextError(ctx); err != nil {
			return "", 0, 0, false, err
		}
		line, ok, err := reader.readBounded(ctx, h.config.MemoryThreshold)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return "", 0, 0, false, err
			}
			return "", 0, 0, false, fmt.Errorf("Cannot grep %q line_window because at least one line exceeds MCP_MEMORY_THRESHOLD (%d characters). Narrow line_window or increase MCP_MEMORY_THRESHOLD if this is intentional.", displayFile, h.config.MemoryThreshold)
		}
		if !ok {
			break
		}
		lineNumber++
		if lineNumber < window.StartLine {
			continue
		}
		if lineNumber > window.EndLine {
			break
		}
		if selected > 0 {
			b.WriteByte('\n')
		}
		if int64(b.Len()+len(line)) > h.config.MemoryThreshold {
			return "", 0, 0, false, fmt.Errorf("multiline grep line_window for %q exceeds MCP_MEMORY_THRESHOLD (%d characters). Narrow line_window or increase MCP_MEMORY_THRESHOLD if this is intentional.", displayFile, h.config.MemoryThreshold)
		}
		b.WriteString(line)
		selected++
	}
	return b.String(), window.StartLine - 1, selected, false, nil
}

func (h *Handler) grepSmallFileRows(ctx context.Context, file, displayFile string, re *regexp.Regexp, options grepSearchOptions, emit grepRowEmitter) (bool, grepFileSearchResult, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return true, grepFileSearchResult{SkippedUnreadable: true}, nil
	}
	if err := contextError(ctx); err != nil {
		return false, grepFileSearchResult{}, err
	}
	content, encodingName := decodeFileContent(data, "")
	if isBinaryFile(data) && !grepDecodedEncodingAllowsNUL(encodingName) {
		return true, grepFileSearchResult{SkippedBinary: true}, nil
	}
	if content == "" {
		return true, grepFileSearchResult{Searched: true}, nil
	}
	if options.Multiline {
		lineOffset := 0
		selectedLines := 1
		if options.LineWindow != nil {
			content, lineOffset, selectedLines = contentForGrepLineWindow(content, options.LineWindow)
			if selectedLines == 0 {
				return true, grepFileSearchResult{Searched: true}, nil
			}
			if int64(len(content)) > h.config.MemoryThreshold {
				return false, grepFileSearchResult{}, fmt.Errorf("multiline grep line_window for %q exceeds MCP_MEMORY_THRESHOLD (%d characters). Narrow line_window or increase MCP_MEMORY_THRESHOLD if this is intentional.", displayFile, h.config.MemoryThreshold)
			}
		}
		keepGoing, capped, err := grepMultilineRowsForFile(ctx, displayFile, content, re, options, lineOffset, emit)
		return keepGoing, grepFileSearchResult{Searched: true, Capped: capped}, err
	}
	lines := splitDisplayLines(content)
	lineOffset := 0
	if options.LineWindow != nil {
		lines, lineOffset = linesForGrepLineWindow(content, lines, options.LineWindow)
	}
	keepGoing, capped, err := grepLineRowsForFile(ctx, displayFile, lines, lineOffset, re, options, emit)
	return keepGoing, grepFileSearchResult{Searched: true, Capped: capped}, err
}

func grepDecodedEncodingAllowsNUL(encodingName string) bool {
	encodingName = strings.ToLower(strings.TrimSpace(encodingName))
	return strings.HasPrefix(encodingName, "utf-16") || strings.HasPrefix(encodingName, "utf-32")
}

func (h *Handler) grepLargeFileRows(ctx context.Context, file, displayFile string, re *regexp.Regexp, options grepSearchOptions, emit grepRowEmitter) (bool, grepFileSearchResult, error) {
	encResult, err := h.resolveEncoding("", file)
	if err != nil {
		return true, grepFileSearchResult{SkippedUnreadable: true}, nil
	}
	stream, closeFile, err := newDisplayRuneStream(file, encResult)
	if err != nil {
		return true, grepFileSearchResult{SkippedUnreadable: true}, nil
	}
	defer closeFile.Close()
	reader := newGrepDisplayLineReader(stream)

	before := make([]string, 0, options.ContextBefore)
	emittedLines := make(map[int]struct{})
	emitContent := func(lineNumber int, separator, line string) (bool, error) {
		if _, ok := emittedLines[lineNumber]; ok {
			return true, nil
		}
		emittedLines[lineNumber] = struct{}{}
		return emit(grepContentRow(displayFile, lineNumber, separator, line))
	}
	afterRemaining := 0
	matchCount := 0
	retainedMatches := 0
	capped := false
	lineNumber := 0
	for {
		if err := contextError(ctx); err != nil {
			return false, grepFileSearchResult{}, err
		}
		line, ok, err := reader.readBounded(ctx, h.config.MemoryThreshold)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return false, grepFileSearchResult{}, err
			}
			return false, grepFileSearchResult{}, fmt.Errorf("Cannot grep %q line-by-line because at least one line exceeds MCP_MEMORY_THRESHOLD (%d characters).\n\nNarrow path/type/glob/ignore_globs, use read_file with start_line/end_line for known locations, or increase MCP_MEMORY_THRESHOLD if this is intentional.", displayFile, h.config.MemoryThreshold)
		}
		if !ok {
			break
		}
		lineNumber++
		if options.LineWindow != nil {
			if lineNumber < options.LineWindow.StartLine {
				continue
			}
			if lineNumber > options.LineWindow.EndLine {
				break
			}
		}
		if re.MatchString(line) {
			matchCount++
			switch options.Mode {
			case "files_with_matches":
				keepGoing, err := emit(grepFileRow(displayFile))
				return keepGoing, grepFileSearchResult{Searched: true, Capped: capped}, err
			case "count":
				continue
			default:
				if options.MaxMatchesPerFile > 0 && retainedMatches >= options.MaxMatchesPerFile {
					capped = true
					if afterRemaining > 0 {
						if keepGoing, err := emitContent(lineNumber, "-", line); !keepGoing || err != nil {
							return keepGoing, grepFileSearchResult{Searched: true, Capped: capped}, err
						}
						afterRemaining--
					}
					continue
				}
				retainedMatches++
				for i, contextLine := range before {
					if keepGoing, err := emitContent(lineNumber-len(before)+i, "-", contextLine); !keepGoing || err != nil {
						return keepGoing, grepFileSearchResult{Searched: true, Capped: capped}, err
					}
				}
				if keepGoing, err := emitContent(lineNumber, ":", line); !keepGoing || err != nil {
					return keepGoing, grepFileSearchResult{Searched: true, Capped: capped}, err
				}
				afterRemaining = options.ContextAfter
			}
		} else if afterRemaining > 0 && options.Mode == "content" {
			if keepGoing, err := emitContent(lineNumber, "-", line); !keepGoing || err != nil {
				return keepGoing, grepFileSearchResult{Searched: true, Capped: capped}, err
			}
			afterRemaining--
		}

		if options.ContextBefore > 0 {
			if len(before) == options.ContextBefore {
				copy(before, before[1:])
				before[len(before)-1] = line
			} else {
				before = append(before, line)
			}
		}
	}

	if options.Mode == "count" && matchCount > 0 {
		keepGoing, err := emit(grepCountRow(displayFile, matchCount))
		return keepGoing, grepFileSearchResult{Searched: true, Capped: capped}, err
	}
	return true, grepFileSearchResult{Searched: true, Capped: capped}, nil
}

func grepLineRowsForFile(ctx context.Context, file string, lines []string, lineOffset int, re *regexp.Regexp, options grepSearchOptions, emit grepRowEmitter) (bool, bool, error) {
	matchCount := 0
	capped := false
	if options.Mode == "content" {
		matchLines := make(map[int]bool)
		retainedMatchLines := make(map[int]bool)
		retainedMatches := 0
		for lineIdx, line := range lines {
			if err := contextError(ctx); err != nil {
				return false, capped, err
			}
			if re.MatchString(line) {
				matchCount++
				matchLines[lineIdx] = true
				if options.MaxMatchesPerFile > 0 && retainedMatches >= options.MaxMatchesPerFile {
					capped = true
					continue
				}
				retainedMatches++
				retainedMatchLines[lineIdx] = true
			}
		}
		if retainedMatches == 0 {
			return true, capped, nil
		}
		keepGoing, err := emitMergedLineContentRows(ctx, file, lines, lineOffset, retainedMatchLines, options.ContextBefore, options.ContextAfter, emit)
		return keepGoing, capped, err
	}
	for _, line := range lines {
		if err := contextError(ctx); err != nil {
			return false, false, err
		}
		if !re.MatchString(line) {
			continue
		}
		matchCount++
		switch options.Mode {
		case "files_with_matches":
			keepGoing, err := emit(grepFileRow(file))
			return keepGoing, false, err
		case "count":
			continue
		}
	}
	if options.Mode == "count" && matchCount > 0 {
		keepGoing, err := emit(grepCountRow(file, matchCount))
		return keepGoing, false, err
	}
	return true, false, nil
}

func emitMergedLineContentRows(ctx context.Context, file string, lines []string, lineOffset int, matchLines map[int]bool, contextBefore, contextAfter int, emit grepRowEmitter) (bool, error) {
	emitted := make(map[int]struct{})
	for lineIdx := range lines {
		if !matchLines[lineIdx] {
			continue
		}
		start := maxInt(0, lineIdx-contextBefore)
		end := minInt(len(lines), lineIdx+contextAfter+1)
		for i := start; i < end; i++ {
			if err := contextError(ctx); err != nil {
				return false, err
			}
			if _, ok := emitted[i]; ok {
				continue
			}
			emitted[i] = struct{}{}
			separator := "-"
			if matchLines[i] {
				separator = ":"
			}
			if keepGoing, err := emit(grepContentRow(file, lineOffset+i+1, separator, lines[i])); !keepGoing || err != nil {
				return keepGoing, err
			}
		}
	}
	return true, nil
}

func grepMultilineRowsForFile(ctx context.Context, file, content string, re *regexp.Regexp, options grepSearchOptions, lineOffset int, emit grepRowEmitter) (bool, bool, error) {
	matches := regexpMatches(re, content)
	switch options.Mode {
	case "files_with_matches":
		if err := contextError(ctx); err != nil {
			return false, false, err
		}
		if len(matches) == 0 {
			return true, false, nil
		}
		keepGoing, err := emit(grepFileRow(file))
		return keepGoing, false, err
	case "count":
		count := 0
		for range matches {
			if err := contextError(ctx); err != nil {
				return false, false, err
			}
			count++
		}
		if count == 0 {
			return true, false, nil
		}
		keepGoing, err := emit(grepCountRow(file, count))
		return keepGoing, false, err
	}

	lines := splitDisplayLines(content)
	lineStarts := lineStartOffsets(content)
	rows := make(map[int]*multilineLineRow)
	retainedMatches := 0
	capped := false
	for _, match := range matches {
		if err := contextError(ctx); err != nil {
			return false, capped, err
		}
		if options.MaxMatchesPerFile > 0 && retainedMatches >= options.MaxMatchesPerFile {
			capped = true
			continue
		}
		retainedMatches++
		startLine := lineForByteIndex(lineStarts, match[0])
		endLine := lineForByteIndex(lineStarts, maxInt(match[1]-1, match[0]))
		contextStart := maxInt(1, startLine-options.ContextBefore)
		contextEnd := minInt(len(lines), endLine+options.ContextAfter)
		for line := contextStart; line <= contextEnd; line++ {
			row := multilineRow(rows, line)
			if line < startLine || line > endLine {
				if row.Kind == "" {
					row.Kind = "context"
					row.Text = lines[line-1]
				}
				continue
			}
			row.Kind = "match"
			if line == startLine {
				row.MatchCountDelta++
			}
			lineStart := lineStarts[line-1]
			lineEnd := lineContentEndOffset(content, lineStarts, line)
			segmentStart := maxInt(match[0], lineStart)
			segmentEnd := minInt(match[1], lineEnd)
			if segmentStart < segmentEnd {
				row.extendSegment(segmentStart, segmentEnd)
			}
		}
	}
	keepGoing, err := emitMultilineRows(ctx, file, content, lines, rows, lineOffset, emit)
	return keepGoing, capped, err
}

type multilineLineRow struct {
	Kind            string
	Text            string
	MatchCountDelta int
	SegmentStart    int
	SegmentEnd      int
	HasSegment      bool
}

func multilineRow(rows map[int]*multilineLineRow, line int) *multilineLineRow {
	row := rows[line]
	if row == nil {
		row = &multilineLineRow{}
		rows[line] = row
	}
	return row
}

func (r *multilineLineRow) extendSegment(start, end int) {
	if !r.HasSegment {
		r.SegmentStart = start
		r.SegmentEnd = end
		r.HasSegment = true
		return
	}
	r.SegmentStart = minInt(r.SegmentStart, start)
	r.SegmentEnd = maxInt(r.SegmentEnd, end)
}

func emitMultilineRows(ctx context.Context, file, content string, lines []string, rows map[int]*multilineLineRow, lineOffset int, emit grepRowEmitter) (bool, error) {
	for line := 1; line <= len(lines); line++ {
		rowInfo := rows[line]
		if rowInfo == nil {
			continue
		}
		if err := contextError(ctx); err != nil {
			return false, err
		}
		separator := "-"
		body := rowInfo.Text
		if rowInfo.Kind == "match" {
			separator = ":"
			body = ""
			if rowInfo.HasSegment {
				body = content[rowInfo.SegmentStart:rowInfo.SegmentEnd]
			}
			body = strings.TrimSuffix(body, "\r")
		}
		row := grepContentRow(file, lineOffset+line, separator, body)
		if rowInfo.Kind == "match" {
			row.UseMatchDelta = true
			row.MatchCountDelta = rowInfo.MatchCountDelta
		}
		if keepGoing, err := emit(row); !keepGoing || err != nil {
			return keepGoing, err
		}
	}
	return true, nil
}

func lineContentEndOffset(content string, lineStarts []int, line int) int {
	if line < len(lineStarts) {
		end := lineStarts[line] - 1
		if end > 0 && content[end-1] == '\r' {
			end--
		}
		return end
	}
	return len(content)
}

func regexpMatches(re *regexp.Regexp, content string) [][2]int {
	rawMatches := re.FindAllStringIndex(content, -1)
	if len(rawMatches) == 0 {
		return nil
	}
	matches := make([][2]int, 0, len(rawMatches))
	for _, match := range rawMatches {
		if len(match) == 2 {
			matches = append(matches, [2]int{match[0], match[1]})
		}
	}
	return matches
}

func grepContentRow(file string, line int, separator, body string) textRow {
	kind := "match"
	if separator == "-" {
		kind = "context"
	}
	text := strings.TrimSuffix(body, "\r")
	return textRow{
		Prefix: fmt.Sprintf("%s:%d%s", file, line, separator),
		Body:   text,
		Path:   file,
		Line:   line,
		Kind:   kind,
		Text:   text,
	}
}

func grepFileRow(file string) textRow {
	return textRow{
		Body: file,
		Path: file,
		Kind: "file",
	}
}

func grepCountRow(file string, count int) textRow {
	return textRow{
		Body:  fmt.Sprintf("%s:%d", file, count),
		Path:  file,
		Kind:  "count",
		Count: count,
	}
}

func readDisplayLineBounded(ctx context.Context, stream *displayRuneStream, maxChars int64) (string, bool, error) {
	var b strings.Builder
	count := int64(0)
	for {
		if err := contextError(ctx); err != nil {
			return "", false, err
		}
		r, lineBreak, eof, err := stream.nextDisplayRune()
		if err != nil {
			return "", false, err
		}
		if eof {
			if count == 0 {
				return "", false, nil
			}
			return b.String(), true, nil
		}
		if lineBreak {
			return b.String(), true, nil
		}
		count++
		if count > maxChars {
			return "", false, fmt.Errorf("line exceeds threshold")
		}
		b.WriteRune(r)
	}
}

type grepDisplayLineReader struct {
	stream                     *displayRuneStream
	previousEndedWithLineBreak bool
	finalEmptyReturned         bool
}

func newGrepDisplayLineReader(stream *displayRuneStream) *grepDisplayLineReader {
	return &grepDisplayLineReader{stream: stream}
}

func (r *grepDisplayLineReader) readBounded(ctx context.Context, maxChars int64) (string, bool, error) {
	var b strings.Builder
	count := int64(0)
	for {
		if err := contextError(ctx); err != nil {
			return "", false, err
		}
		next, lineBreak, eof, err := r.stream.nextDisplayRune()
		if err != nil {
			return "", false, err
		}
		if eof {
			if count == 0 {
				if r.previousEndedWithLineBreak && !r.finalEmptyReturned {
					r.previousEndedWithLineBreak = false
					r.finalEmptyReturned = true
					return "", true, nil
				}
				return "", false, nil
			}
			r.previousEndedWithLineBreak = false
			return b.String(), true, nil
		}
		if lineBreak {
			r.previousEndedWithLineBreak = true
			r.finalEmptyReturned = false
			return b.String(), true, nil
		}
		r.previousEndedWithLineBreak = false
		count++
		if count > maxChars {
			return "", false, fmt.Errorf("line exceeds threshold")
		}
		b.WriteRune(next)
	}
}

func splitDisplayLines(content string) []string {
	lines := strings.Split(content, "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}

func linesForGrepLineWindow(content string, lines []string, window *SourceLineRange) ([]string, int) {
	if window == nil {
		return lines, 0
	}
	offset := window.StartLine - 1
	if content == "" || window.StartLine > len(lines) {
		return []string{}, offset
	}
	end := minInt(window.EndLine, len(lines))
	return lines[offset:end], offset
}

func contentForGrepLineWindow(content string, window *SourceLineRange) (string, int, int) {
	lines := splitDisplayLines(content)
	selected, offset := linesForGrepLineWindow(content, lines, window)
	return strings.Join(selected, "\n"), offset, len(selected)
}

func isFileLargerThan(file string, threshold int64) bool {
	info, err := os.Stat(file)
	return err == nil && info.Size() > threshold
}

func hasUnicodeTextBOM(data []byte) bool {
	if len(data) >= 4 {
		if data[0] == 0xFF && data[1] == 0xFE && data[2] == 0x00 && data[3] == 0x00 {
			return true
		}
		if data[0] == 0x00 && data[1] == 0x00 && data[2] == 0xFE && data[3] == 0xFF {
			return true
		}
	}
	if len(data) >= 2 {
		if data[0] == 0xFF && data[1] == 0xFE {
			return true
		}
		if data[0] == 0xFE && data[1] == 0xFF {
			return true
		}
	}
	return false
}

func lineStartOffsets(content string) []int {
	starts := []int{0}
	for i, r := range content {
		if r == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

func lineForByteIndex(lineStarts []int, index int) int {
	line := sort.Search(len(lineStarts), func(i int) bool {
		return lineStarts[i] > index
	})
	return line
}

func grepContext(input GrepToolInput) (int, int) {
	if input.Context > 0 {
		return input.Context, input.Context
	}
	before := input.Before
	after := input.After
	if before < 0 {
		before = 0
	}
	if after < 0 {
		after = 0
	}
	return before, after
}

func (h *Handler) finalizeGrepOutput(pathCtx PathContext, output *GrepOutput, input GrepToolInput, options grepSearchOptions, hasMatches bool) {
	if output == nil {
		return
	}
	if !hasMatches {
		if completeGrepNoMatch(output.SearchStats) {
			output.Text = grepNoMatchesMessage(input.Pattern)
			output.Message = fmt.Sprintf("No matches found for pattern %q. Try changing pattern, using case_insensitive=true, or adjusting path/type/glob/ignore_globs filters. For broad trees, narrow the search with type=\"go\", glob=\"*.{go,ts}\", or ignore_globs=[\"node_modules/**\",\"vendor/**\"].", input.Pattern)
		} else {
			stopReason := "incomplete"
			if output.SearchStats != nil && output.SearchStats.StopReason != "" {
				stopReason = output.SearchStats.StopReason
			}
			message := fmt.Sprintf("No match rows were returned before grep stopped or skipped part of the search (stop_reason=%q). Inspect search_stats before treating this as no matches.", stopReason)
			if output.Text == "" {
				output.Text = message + "\n"
			}
			output.Message = message
		}
	}
	if output.NextRecommendedCall == nil && len(output.NextRecommendedCalls) == 0 && hasMatches && output.OutputMode == "content" {
		output.NextRecommendedCalls = h.grepContentNextRecommendedCalls(pathCtx, *output)
		if len(output.NextRecommendedCalls) > 0 {
			output.NextRecommendedCall = &output.NextRecommendedCalls[0]
			return
		}
	}
	if output.NextRecommendedCall == nil {
		output.NextRecommendedCall = h.grepNextRecommendedCall(pathCtx, *output, input, options, hasMatches)
	}
	if output.NextRecommendedCall != nil && len(output.NextRecommendedCalls) == 0 {
		output.NextRecommendedCalls = []ActionHint{*output.NextRecommendedCall}
	}
}

func (h *Handler) grepContentNextRecommendedCalls(pathCtx PathContext, output GrepOutput) []ActionHint {
	if output.Truncated || output.SearchStats == nil || output.SearchStats.StopReason != "" || !output.SearchStats.Completed {
		return nil
	}
	calls := []ActionHint{}
	if readHint := grepReadRangesRecommendation(pathCtx, output); readHint != nil {
		calls = append(calls, *readHint)
	}
	if outlineHint := grepOutlineRecommendation(pathCtx, output); outlineHint != nil {
		calls = append(calls, *outlineHint)
	}
	return calls
}

func (h *Handler) grepNextRecommendedCall(pathCtx PathContext, output GrepOutput, input GrepToolInput, options grepSearchOptions, hasMatches bool) *ActionHint {
	stats := output.SearchStats
	singleScope := h.grepSingleFileScope(options)
	if stats != nil {
		switch stats.StopReason {
		case "limit":
			if !singleScope && output.OutputMode == "content" {
				nextInput, safe := recommendedGrepInput(pathCtx, output, input)
				if !safe {
					return nil
				}
				reason := "The global limit stopped broad grep before all evidence was returned; retry with files_with_matches to map matching files first."
				if input.MaxMatchesPerFile == nil && grepDominatedByFirstFile(output) {
					nextInput["max_matches_per_file"] = recommendedMaxMatchesPerFile(output.Limit)
					reason = "The global limit was dominated by one file; retry with a per-file cap to surface matches from more files."
				} else {
					prepareGrepMappingRecommendation(nextInput)
				}
				return &ActionHint{
					SafeToRetry:                true,
					RecommendedNextTool:        "grep",
					RecommendedNextInput:       nextInput,
					RecommendedNextInputPolicy: "narrow_truncated_grep",
					Reason:                     reason,
				}
			}
		case "file_cap":
			if !singleScope && output.OutputMode == "content" {
				nextInput, safe := recommendedGrepInput(pathCtx, output, input)
				if !safe {
					return nil
				}
				prepareGrepMappingRecommendation(nextInput)
				nextInput["limit"] = output.Limit
				return &ActionHint{
					SafeToRetry:                true,
					RecommendedNextTool:        "grep",
					RecommendedNextInput:       nextInput,
					RecommendedNextInputPolicy: "map_capped_grep_files",
					Reason:                     "At least one file hit max_matches_per_file; inspect the mapped files or narrow the grep before reading large ranges.",
				}
			}
		}
	}
	if !hasMatches {
		if !completeGrepNoMatch(output.SearchStats) {
			return nil
		}
		if normalizedGrepPatternMode(input.PatternMode) == "regex" && regexLookingLiteralPattern(input.Pattern) {
			nextInput, safe := recommendedGrepInput(pathCtx, output, input)
			if !safe {
				return nil
			}
			nextInput["pattern_mode"] = "literal"
			return &ActionHint{
				SafeToRetry:                true,
				RecommendedNextTool:        "grep",
				RecommendedNextInput:       nextInput,
				RecommendedNextInputPolicy: "retry_literal_pattern",
				Reason:                     "The pattern contains unescaped regexp metacharacters; retry literal mode if exact text was intended.",
			}
		}
		if !input.CaseInsensitive && usefulCaseInsensitiveRetryPattern(input.Pattern) {
			nextInput, safe := recommendedGrepInput(pathCtx, output, input)
			if !safe {
				return nil
			}
			nextInput["case_insensitive"] = true
			return &ActionHint{
				SafeToRetry:                true,
				RecommendedNextTool:        "grep",
				RecommendedNextInput:       nextInput,
				RecommendedNextInputPolicy: "retry_case_insensitive",
				Reason:                     "No matches were found and the pattern has letters; retry case_insensitive=true if casing is uncertain.",
			}
		}
		return nil
	}
	if output.OutputMode == "content" {
		for _, group := range output.FileGroups {
			if group.MatchCount >= 8 || group.LastLine-group.FirstLine+1 >= 120 {
				lineWindow := SourceLineRange{StartLine: maxInt(1, group.FirstLine), EndLine: group.LastLine}
				if input.LineWindow != nil {
					lineWindow.StartLine = maxInt(lineWindow.StartLine, input.LineWindow.StartLine)
					lineWindow.EndLine = minInt(lineWindow.EndLine, input.LineWindow.EndLine)
				}
				if lineWindow.EndLine < lineWindow.StartLine {
					continue
				}
				nextInput := map[string]any{
					"target_file": group.Path,
					"line_window": map[string]any{
						"start_line": lineWindow.StartLine,
						"end_line":   lineWindow.EndLine,
					},
				}
				addCwdIDToRecommendedInput(pathCtx, "outline_file", nextInput)
				return &ActionHint{
					SafeToRetry:                true,
					RecommendedNextTool:        "outline_file",
					RecommendedNextInput:       nextInput,
					RecommendedNextInputPolicy: "inspect_file_outline",
					Reason:                     "This file has a dense or wide match region; outline the bounded region before reading large chunks.",
				}
			}
		}
		if hint := grepReadRangesRecommendation(pathCtx, output); hint != nil {
			return hint
		}
	}
	return nil
}

func (h *Handler) grepSingleFileScope(options grepSearchOptions) bool {
	if options.LineWindow != nil {
		return true
	}
	info, err := os.Stat(options.ResolvedRoot)
	return err == nil && !info.IsDir()
}

func recommendedGrepInput(pathCtx PathContext, output GrepOutput, input GrepToolInput) (map[string]any, bool) {
	nextInput := map[string]any{
		"pattern": input.Pattern,
		"path":    output.Path,
	}
	if mode := normalizedGrepPatternMode(input.PatternMode); mode != "" && mode != "regex" {
		nextInput["pattern_mode"] = mode
	}
	if output.OutputMode != "" && output.OutputMode != "content" {
		nextInput["output_mode"] = output.OutputMode
	}
	if input.Context > 0 {
		nextInput["context"] = input.Context
	} else {
		if input.Before > 0 {
			nextInput["before"] = input.Before
		}
		if input.After > 0 {
			nextInput["after"] = input.After
		}
	}
	if input.CaseInsensitive {
		nextInput["case_insensitive"] = true
	}
	if input.Type != "" {
		nextInput["type"] = input.Type
	}
	if input.Glob != "" {
		nextInput["glob"] = input.Glob
	}
	ignoreGlobs, safe := recommendedIgnoreGlobs(pathCtx, input.IgnoreGlobs)
	if !safe {
		return nil, false
	}
	if len(ignoreGlobs) > 0 {
		nextInput["ignore_globs"] = ignoreGlobs
	}
	if input.IncludeHidden {
		nextInput["include_hidden"] = true
	}
	if mode, err := normalizeRedactionMode(input.RedactionMode); err == nil && mode != redactionAuto {
		nextInput["redaction_mode"] = mode
	}
	if input.Multiline {
		nextInput["multiline"] = true
	}
	if input.LineWindow != nil {
		nextInput["line_window"] = map[string]any{
			"start_line": input.LineWindow.StartLine,
			"end_line":   input.LineWindow.EndLine,
		}
	}
	if input.MaxMatchesPerFile != nil {
		nextInput["max_matches_per_file"] = *input.MaxMatchesPerFile
	}
	if input.Limit != nil {
		nextInput["limit"] = *input.Limit
	}
	addCwdIDToRecommendedInput(pathCtx, "grep", nextInput)
	return nextInput, true
}

func recommendedIgnoreGlobs(pathCtx PathContext, ignoreGlobs []string) ([]string, bool) {
	if len(ignoreGlobs) == 0 {
		return nil, true
	}
	result := make([]string, 0, len(ignoreGlobs))
	for _, raw := range ignoreGlobs {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if pathCtx.HasCwd {
			normalized := strings.ReplaceAll(trimmed, "\\", "/")
			if looksAbsoluteForAnySupportedOS(trimmed) || normalized == ".." || strings.HasPrefix(normalized, "../") {
				return nil, false
			}
		}
		result = append(result, strings.ReplaceAll(trimmed, "\\", "/"))
	}
	return result, true
}

func prepareGrepMappingRecommendation(nextInput map[string]any) {
	nextInput["output_mode"] = "files_with_matches"
	delete(nextInput, "context")
	delete(nextInput, "before")
	delete(nextInput, "after")
	delete(nextInput, "line_window")
	delete(nextInput, "max_matches_per_file")
}

func grepDominatedByFirstFile(output GrepOutput) bool {
	if len(output.FileGroups) == 0 {
		return false
	}
	firstMatchCount := output.FileGroups[0].MatchCount
	if firstMatchCount < 5 {
		return false
	}
	totalMatchCount := 0
	for _, group := range output.FileGroups {
		totalMatchCount += group.MatchCount
	}
	if totalMatchCount <= 0 {
		return false
	}
	return firstMatchCount*100 >= totalMatchCount*80
}

func completeGrepNoMatch(stats *GrepSearchStats) bool {
	return stats != nil && stats.Completed && stats.CountsAreComplete && stats.StopReason == ""
}

func grepReadRangesRecommendation(pathCtx PathContext, output GrepOutput) *ActionHint {
	const (
		maxRecommendedFiles         = 6
		maxRecommendedRanges        = 12
		maxRecommendedRangesPerFile = 3
	)
	groupsWithRanges := []GrepFileGroup{}
	totalRanges := 0
	for _, group := range output.FileGroups {
		if len(group.ReadRanges) == 0 {
			continue
		}
		if len(group.ReadRanges) > maxRecommendedRangesPerFile {
			return nil
		}
		groupsWithRanges = append(groupsWithRanges, group)
		totalRanges += len(group.ReadRanges)
	}
	if len(groupsWithRanges) == 0 || len(groupsWithRanges) > maxRecommendedFiles || totalRanges > maxRecommendedRanges {
		return nil
	}
	if totalRanges == 1 {
		group := groupsWithRanges[0]
		readRange := group.ReadRanges[0]
		input := map[string]any{
			"target_file": group.Path,
			"start_line":  readRange.StartLine,
			"end_line":    readRange.EndLine,
		}
		addCwdIDToRecommendedInput(pathCtx, "read_file", input)
		return &ActionHint{
			SafeToRetry:                true,
			RecommendedNextTool:        "read_file",
			RecommendedNextInput:       input,
			RecommendedNextInputPolicy: "open_matched_range",
			Reason:                     "Open the matched grep range with read_file for line-addressable context.",
		}
	}
	items := make([]map[string]any, 0, totalRanges)
	for _, group := range groupsWithRanges {
		for _, readRange := range group.ReadRanges {
			items = append(items, map[string]any{
				"target_file": group.Path,
				"start_line":  readRange.StartLine,
				"end_line":    readRange.EndLine,
			})
		}
	}
	input := map[string]any{"items": items}
	if output.RedactionMode != "" && output.RedactionMode != redactionOff {
		input["redaction_mode"] = output.RedactionMode
	}
	addCwdIDToRecommendedInput(pathCtx, "read_files", input)
	return &ActionHint{
		SafeToRetry:                true,
		RecommendedNextTool:        "read_files",
		RecommendedNextInput:       input,
		RecommendedNextInputPolicy: "open_grouped_match_ranges",
		Reason:                     "Read all bounded grep ranges in one compact read_files call.",
	}
}

func grepOutlineRecommendation(pathCtx PathContext, output GrepOutput) *ActionHint {
	if len(output.FileGroups) != 1 {
		return nil
	}
	group := output.FileGroups[0]
	if !isSourceLikePath(group.Path) && !isConfigLikePath(group.Path) {
		return nil
	}
	startLine := group.FirstLine
	endLine := group.LastLine
	if startLine <= 0 || endLine < startLine {
		return nil
	}
	input := map[string]any{
		"target_file":    group.Path,
		"output_profile": outlineProfileAgent,
		"line_window": map[string]any{
			"start_line": startLine,
			"end_line":   endLine,
		},
	}
	addCwdIDToRecommendedInput(pathCtx, "outline_file", input)
	return &ActionHint{
		SafeToRetry:                true,
		RecommendedNextTool:        "outline_file",
		RecommendedNextInput:       input,
		RecommendedNextInputPolicy: "inspect_matched_file_structure",
		Reason:                     "The complete grep result is focused on one source/config-like file; outline the matched window before editing.",
	}
}

func recommendedMaxMatchesPerFile(limit int) int {
	if limit <= 2 {
		return maxInt(1, limit)
	}
	value := limit / 5
	if value < 3 {
		value = 3
	}
	if value > limit {
		value = limit
	}
	return value
}

func regexLookingLiteralPattern(pattern string) bool {
	metachars := ".^$*+?()[]{}|"
	for i := 0; i < len(pattern); i++ {
		if !strings.ContainsRune(metachars, rune(pattern[i])) {
			continue
		}
		backslashes := 0
		for j := i - 1; j >= 0 && pattern[j] == '\\'; j-- {
			backslashes++
		}
		if backslashes%2 == 0 {
			return true
		}
	}
	return false
}

func usefulCaseInsensitiveRetryPattern(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "/") || strings.Contains(value, "\\") {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] >= 'A' && value[i] <= 'Z' || value[i] >= 'a' && value[i] <= 'z' {
			return true
		}
	}
	return false
}

func matchesFileType(path, fileType string) bool {
	extensions, ok := toolTypeExtensions[fileType]
	if !ok {
		return false
	}
	ext := filepath.Ext(path)
	for _, candidate := range extensions {
		if ext == candidate {
			return true
		}
	}
	return false
}

var toolTypeExtensions = map[string][]string{
	"js":      {".js", ".jsx", ".mjs"},
	"ts":      {".ts", ".tsx"},
	"py":      {".py", ".pyi"},
	"go":      {".go"},
	"rust":    {".rs"},
	"rs":      {".rs"},
	"java":    {".java"},
	"cpp":     {".cpp", ".cc", ".cxx", ".hpp", ".hh", ".hxx", ".h"},
	"c++":     {".cpp", ".cc", ".cxx", ".hpp", ".hh", ".hxx", ".h"},
	"c":       {".c", ".h"},
	"csharp":  {".cs"},
	"cs":      {".cs"},
	"ruby":    {".rb"},
	"rb":      {".rb"},
	"php":     {".php"},
	"bash":    {".sh", ".bash"},
	"sh":      {".sh", ".bash"},
	"shell":   {".sh", ".bash"},
	"kotlin":  {".kt", ".kts"},
	"kt":      {".kt", ".kts"},
	"swift":   {".swift"},
	"scala":   {".scala"},
	"r":       {".r", ".R"},
	"lua":     {".lua"},
	"perl":    {".pl", ".pm"},
	"elixir":  {".ex", ".exs"},
	"erlang":  {".erl", ".hrl"},
	"haskell": {".hs", ".lhs"},
	"clojure": {".clj", ".cljs", ".cljc"},
	"md":      {".md", ".markdown"},
	"json":    {".json"},
	"yaml":    {".yaml", ".yml"},
	"html":    {".html", ".htm"},
	"css":     {".css", ".scss", ".sass", ".less"},
	"sql":     {".sql"},
	"xml":     {".xml"},
	"toml":    {".toml"},
	"ini":     {".ini", ".cfg", ".conf"},
	"csv":     {".csv", ".tsv"},
	"tex":     {".tex", ".latex"},
	"proto":   {".proto"},
	"graphql": {".graphql", ".gql"},
	"tf":      {".tf", ".tfvars"},
	"vim":     {".vim"},
}
