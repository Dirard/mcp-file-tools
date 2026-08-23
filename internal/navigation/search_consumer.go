package navigation

import (
	"context"
	"errors"
	"math"
	"regexp"
	"unsafe"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/codeparse"
	"github.com/Dirard/mcp-file-tools/internal/navmodel"
	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"github.com/Dirard/mcp-file-tools/internal/rootfs"
	runtimepkg "github.com/Dirard/mcp-file-tools/internal/runtime"
	"github.com/Dirard/mcp-file-tools/internal/scanner"
	"github.com/Dirard/mcp-file-tools/internal/textio"
)

type searchConsumer struct {
	mode      dynamicMode
	matchFile func(string) bool
	matcher   *regexp.Regexp
	context   uint8
	explicit  bool
	parser    *codeparse.Service
	work      *runtimepkg.WorkLease
}

func newSearchConsumer(
	mode dynamicMode,
	pattern searchPattern,
	matchFile func(string) bool,
	explicit bool,
	parser *codeparse.Service,
	work *runtimepkg.WorkLease,
) (*searchConsumer, error) {
	consumer := &searchConsumer{
		mode:      mode,
		matchFile: matchFile,
		explicit:  explicit,
		parser:    parser,
		work:      work,
	}
	if mode == dynamicTextSearch || mode == dynamicSymbolSearch {
		matcher, err := compileSearchMatcher(pattern.query, pattern.regex, pattern.ignoreCase)
		if err != nil {
			return nil, err
		}
		consumer.matcher = matcher
		consumer.context = pattern.context
	}
	return consumer, nil
}

func (consumer *searchConsumer) SelectCandidate(path pathspec.Relative, kind rootfs.EntryKind) bool {
	if consumer == nil || kind != rootfs.EntryFile {
		return true
	}
	if consumer.matchFile != nil && !consumer.matchFile(path.String()) {
		return false
	}
	if consumer.mode == dynamicSymbolSearch {
		_, supported := codeparse.LanguageForPath(path.String())
		return supported
	}
	return true
}

func (consumer *searchConsumer) Consume(ctx context.Context, candidate scanner.Candidate, file *rootfs.File) scanner.ConsumeResult {
	if consumer == nil || candidate.Kind != rootfs.EntryFile || file == nil {
		return scanner.ConsumeResult{}
	}
	path := candidate.Path.String()
	if consumer.mode == dynamicSymbolSearch && consumer.explicit {
		if _, supported := codeparse.LanguageForPath(path); !supported {
			return scanner.ConsumeResult{Code: api.ErrorUnsupportedLanguage}
		}
	}
	if consumer.matchFile != nil && !consumer.matchFile(path) {
		return scanner.ConsumeResult{}
	}
	switch consumer.mode {
	case dynamicFileSearch:
		return scanner.ConsumeResult{Rows: []scanner.Row{{Kind: scanner.RowFile, Path: path}}}
	case dynamicTextSearch:
		return consumer.consumeText(ctx, candidate, file)
	case dynamicSymbolSearch:
		return consumer.consumeSymbols(ctx, candidate, file)
	default:
		return scanner.ConsumeResult{Code: api.ErrorIOError}
	}
}

type retainedTextLine struct {
	number uint64
	text   string
}

var errTextSearchRetainedBudget = errors.New("text search retained budget exceeded")

type textSearchSink struct {
	path             string
	matcher          *regexp.Regexp
	context          uint64
	previous         []retainedTextLine
	afterUntil       uint64
	lastEmitted      uint64
	rows             []scanner.Row
	rowStrings       uint64
	maxRetainedBytes uint64
	exceeded         bool
}

func (sink *textSearchSink) Consume(line textio.Line) error {
	sink.prunePrevious(line.Number)
	matched := sink.matcher.Match(line.Bytes)
	needsText := matched || line.Number <= sink.afterUntil || sink.context != 0
	if !needsText {
		return nil
	}
	text := string(line.Bytes)
	if matched {
		lower := uint64(1)
		if line.Number > sink.context {
			lower = line.Number - sink.context
		}
		for _, previous := range sink.previous {
			if previous.number >= lower && previous.number > sink.lastEmitted {
				if !sink.append(scanner.RowTextContext, previous.number, previous.text) {
					return sink.exceed()
				}
			}
		}
		if !sink.append(scanner.RowTextMatch, line.Number, text) {
			return sink.exceed()
		}
		upper := line.Number + sink.context
		if upper < line.Number {
			upper = math.MaxUint64
		}
		if upper > sink.afterUntil {
			sink.afterUntil = upper
		}
	} else if line.Number <= sink.afterUntil {
		if !sink.append(scanner.RowTextContext, line.Number, text) {
			return sink.exceed()
		}
	}
	if sink.context != 0 {
		sink.previous = append(sink.previous, retainedTextLine{number: line.Number, text: text})
	}
	return nil
}

func (sink *textSearchSink) prunePrevious(line uint64) {
	if sink.context == 0 || len(sink.previous) == 0 {
		sink.previous = sink.previous[:0]
		return
	}
	lower := uint64(1)
	if line > sink.context {
		lower = line - sink.context
	}
	first := 0
	for first < len(sink.previous) && sink.previous[first].number < lower {
		first++
	}
	if first != 0 {
		copy(sink.previous, sink.previous[first:])
		for index := len(sink.previous) - first; index < len(sink.previous); index++ {
			sink.previous[index] = retainedTextLine{}
		}
		sink.previous = sink.previous[:len(sink.previous)-first]
	}
}

func (sink *textSearchSink) append(kind scanner.RowKind, line uint64, text string) bool {
	addedStrings := uint64(len(sink.path) + len(text))
	required := len(sink.rows) + 1
	capacity := cap(sink.rows)
	if required > capacity {
		capacity = 1
		if cap(sink.rows) != 0 {
			capacity = cap(sink.rows) * 2
			if capacity < required {
				capacity = required
			}
		}
		if !sink.fits(capacity, addedStrings) {
			capacity = required
		}
		if !sink.fits(capacity, addedStrings) {
			return false
		}
		grown := make([]scanner.Row, len(sink.rows), capacity)
		copy(grown, sink.rows)
		sink.rows = grown
	} else if !sink.fits(capacity, addedStrings) {
		return false
	}
	sink.rows = append(sink.rows, scanner.Row{Kind: kind, Path: sink.path, Line: line, Text: text})
	sink.rowStrings += addedStrings
	sink.lastEmitted = line
	return true
}

func (sink *textSearchSink) fits(capacity int, addedStrings uint64) bool {
	headerBytes := uint64(capacity) * uint64(unsafe.Sizeof(scanner.Row{}))
	if headerBytes > sink.maxRetainedBytes || sink.rowStrings > sink.maxRetainedBytes-headerBytes {
		return false
	}
	return addedStrings <= sink.maxRetainedBytes-headerBytes-sink.rowStrings
}

func (sink *textSearchSink) exceed() error {
	sink.exceeded = true
	return errTextSearchRetainedBudget
}

func (consumer *searchConsumer) consumeText(ctx context.Context, candidate scanner.Candidate, file *rootfs.File) scanner.ConsumeResult {
	if consumer.matcher == nil || candidate.ContentBytesRemaining == 0 {
		return scanner.ConsumeResult{Code: api.ErrorBudgetExceeded}
	}
	sink := &textSearchSink{
		path:             candidate.Path.String(),
		matcher:          consumer.matcher,
		context:          uint64(consumer.context),
		previous:         make([]retainedTextLine, 0, int(consumer.context)),
		maxRetainedBytes: candidate.RetainedBytesRemaining,
	}
	summary, code := textio.StreamCanonical(ctx, file, textio.Domain{}, textio.Budget{
		MaxRawBytes: candidate.ContentBytesRemaining,
		Deadline:    candidate.Deadline,
	}, sink)
	result := scanner.ConsumeResult{ContentBytes: summary.RawRead}
	if sink.exceeded {
		result.Code = api.ErrorRecordExceedsBudget
		return result
	}
	if code != "" {
		return consumer.mapTextReadFailure(result, code)
	}
	result.Rows = sink.rows
	return result
}

func (consumer *searchConsumer) mapTextReadFailure(result scanner.ConsumeResult, code api.ErrorCode) scanner.ConsumeResult {
	if consumer.explicit {
		result.Code = code
		return result
	}
	switch code {
	case api.ErrorBinary:
		result.Warning = api.WarningBinarySkipped
	case api.ErrorUnsupportedEncoding:
		result.Warning = api.WarningUnsupportedEncodingSkipped
	case api.ErrorIOError, api.ErrorPermissionDenied:
		result.Warning = api.WarningUnreadableSkipped
	default:
		result.Code = code
	}
	return result
}

func (consumer *searchConsumer) consumeSymbols(ctx context.Context, candidate scanner.Candidate, file *rootfs.File) scanner.ConsumeResult {
	path := candidate.Path.String()
	language, supported := codeparse.LanguageForPath(path)
	if !supported {
		if consumer.explicit {
			return scanner.ConsumeResult{Code: api.ErrorUnsupportedLanguage}
		}
		return scanner.ConsumeResult{}
	}
	if consumer.matcher == nil || consumer.parser == nil || consumer.work == nil {
		return scanner.ConsumeResult{Code: api.ErrorIOError}
	}
	if candidate.ContentBytesRemaining == 0 {
		return scanner.ConsumeResult{Code: api.ErrorBudgetExceeded}
	}
	if candidate.ParserBytesRemaining == 0 {
		if consumer.explicit {
			return scanner.ConsumeResult{Code: api.ErrorBudgetExceeded}
		}
		return scanner.ConsumeResult{Warning: api.WarningParserSkipped}
	}
	buffer, code := textio.BufferCanonical(ctx, file, textio.Domain{}, textio.Budget{
		MaxRawBytes: candidate.ContentBytesRemaining,
		Deadline:    candidate.Deadline,
	}, candidate.ParserBytesRemaining)
	result := scanner.ConsumeResult{ContentBytes: buffer.Summary.RawRead}
	if code != "" {
		if code == api.ErrorBudgetExceeded && !consumer.explicit && buffer.Summary.RawRead <= candidate.ContentBytesRemaining {
			result.Warning = api.WarningParserSkipped
			return result
		}
		return consumer.mapTextReadFailure(result, code)
	}
	result.ParserBytes = uint64(len(buffer.Bytes))
	parsed, parseCode := consumer.parser.Parse(ctx, candidate.Deadline, consumer.work, codeparse.Input{
		Path:      path,
		Canonical: buffer.Bytes,
		SHA256:    buffer.Summary.SHA256,
		Language:  language,
	})
	if parseCode != "" {
		if parseCode == api.ErrorParserFailed && !consumer.explicit {
			result.Warning = api.WarningParserSkipped
			return result
		}
		result.Code = parseCode
		return result
	}
	switch parsed.State {
	case codeparse.Clean:
	case codeparse.Recoverable:
		result.Warning = api.WarningParserPartial
	case codeparse.Fatal:
		if consumer.explicit {
			result.Code = api.ErrorParserFailed
		} else {
			result.Warning = api.WarningParserSkipped
		}
		return result
	case codeparse.CallAborted:
		result.Code = api.ErrorBudgetExceeded
		return result
	default:
		result.Code = api.ErrorIOError
		return result
	}
	for _, record := range parsed.Records {
		if (record.Type != navmodel.Symbol && record.Type != navmodel.Heading) || record.Name == "" || !consumer.matcher.MatchString(record.Name) {
			continue
		}
		result.Rows = append(result.Rows, scanner.Row{
			Kind:       scanner.RowSymbol,
			Path:       path,
			Range:      record.Range,
			SymbolKind: record.Kind,
			Name:       record.Name,
		})
	}
	return result
}

var _ scanner.Consumer = (*searchConsumer)(nil)
var _ scanner.CandidateSelector = (*searchConsumer)(nil)
