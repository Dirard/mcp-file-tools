package present

import (
	"strconv"
	"strings"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/config"
	"github.com/Dirard/mcp-file-tools/internal/navmodel"
)

type Fit uint8

const (
	Fits Fit = iota + 1
	NextPage
	IntrinsicOverflow
)

type unitFamily uint8

const (
	unitProject unitFamily = iota + 1
	unitSearchFile
	unitSearchText
	unitSearchSymbol
)

type Unit struct {
	family  unitFamily
	project ProjectEntry
	search  SearchRow
}

func NewProjectUnit(kind ProjectEntryKind, path string) (Unit, error) {
	entry := ProjectEntry{Kind: kind, Path: strings.Clone(path)}
	if !validProjectEntries([]ProjectEntry{entry}) {
		return Unit{}, errInvalidPresentation
	}
	return Unit{family: unitProject, project: entry}, nil
}

func NewSearchFileUnit(path string) (Unit, error) {
	row := SearchRow{Kind: SearchFileRow, Path: strings.Clone(path)}
	if !validSearchRows(SearchFile, []SearchRow{row}) {
		return Unit{}, errInvalidPresentation
	}
	return Unit{family: unitSearchFile, search: row}, nil
}

func NewSearchTextUnit(kind SearchRowKind, path string, line uint64, text string) (Unit, error) {
	row := SearchRow{Kind: kind, Path: strings.Clone(path), Line: line, Text: strings.Clone(text)}
	if !validSearchRows(SearchText, []SearchRow{row}) {
		return Unit{}, errInvalidPresentation
	}
	return Unit{family: unitSearchText, search: row}, nil
}

func NewSearchSymbolUnit(path string, record navmodel.Record) (Unit, error) {
	owned, ok := navmodel.NewRecord(record)
	if !ok || owned.Type != navmodel.Symbol {
		return Unit{}, errInvalidPresentation
	}
	row := SearchRow{
		Kind:       SearchSymbolRow,
		Path:       strings.Clone(path),
		Range:      owned.Range,
		SymbolKind: owned.Kind,
		Name:       owned.Name,
	}
	if !validSearchRows(SearchSymbol, []SearchRow{row}) {
		return Unit{}, errInvalidPresentation
	}
	return Unit{family: unitSearchSymbol, search: row}, nil
}

type Builder struct {
	family      unitFamily
	projectPath string
	searchMode  SearchMode
	maxBytes    uint64
	units       []Unit
	pending     Unit
	pendingFit  Fit
	pendingSet  bool
	invalid     bool
}

func NewProjectBuilder(path string) (*Builder, error) {
	return newProjectBuilder(path, config.OutputMaxBytes)
}

func newProjectBuilder(path string, maxBytes uint64) (*Builder, error) {
	if !validPresentPath(path) || maxBytes == 0 || maxBytes > config.OutputMaxBytes {
		return nil, errInvalidPresentation
	}
	return &Builder{family: unitProject, projectPath: strings.Clone(path), maxBytes: maxBytes}, nil
}

func NewSearchBuilder(mode SearchMode) (*Builder, error) {
	return newSearchBuilder(mode, config.OutputMaxBytes)
}

func newSearchBuilder(mode SearchMode, maxBytes uint64) (*Builder, error) {
	if !validSearchMode(mode) || maxBytes == 0 || maxBytes > config.OutputMaxBytes {
		return nil, errInvalidPresentation
	}
	return &Builder{family: familyForSearchMode(mode), searchMode: mode, maxBytes: maxBytes}, nil
}

func (builder *Builder) Try(unit Unit) Fit {
	if builder == nil || builder.invalid || !builder.accepts(unit) {
		if builder != nil {
			builder.invalid = true
		}
		return IntrinsicOverflow
	}

	fit := IntrinsicOverflow
	if builder.fits(appendUnit(builder.units, unit), Partial, readCursorPlaceholder, nil) {
		fit = Fits
	} else if builder.fits([]Unit{unit}, Partial, readCursorPlaceholder, nil) {
		fit = NextPage
	}
	builder.pending = unit
	builder.pendingFit = fit
	builder.pendingSet = true
	return fit
}

func (builder *Builder) Commit(unit Unit) {
	if builder == nil || builder.invalid || !builder.pendingSet || builder.pendingFit != Fits || builder.pending != unit {
		if builder != nil {
			builder.invalid = true
		}
		return
	}
	builder.units = append(builder.units, unit)
	builder.pending = Unit{}
	builder.pendingFit = 0
	builder.pendingSet = false
}

func (builder *Builder) TrySummary(summary []Warning) Fit {
	if builder == nil || builder.invalid {
		return IntrinsicOverflow
	}
	if builder.fits(builder.units, Complete, "", summary) {
		return Fits
	}
	if builder.fits(nil, Complete, "", summary) {
		return NextPage
	}
	return IntrinsicOverflow
}

func (builder *Builder) Finalize(status Status, cursor Cursor, summary []Warning) (api.Result, error) {
	if builder == nil || builder.invalid || (builder.pendingSet && builder.pendingFit == Fits) {
		return api.Result{}, errInvalidPresentation
	}
	page, err := builder.render(builder.units, status, cursor, summary)
	if err != nil {
		return api.Result{}, err
	}
	text, ok := page.Result.Text()
	if !ok || uint64(len(text)) > builder.maxBytes {
		return api.Result{}, errInvalidPresentation
	}
	return page.Result, nil
}

func (builder *Builder) accepts(unit Unit) bool {
	return unit.family != 0 && unit.family == builder.family
}

func (builder *Builder) fits(units []Unit, status Status, cursor Cursor, summary []Warning) bool {
	page, err := builder.render(units, status, cursor, summary)
	if err != nil {
		return false
	}
	text, ok := page.Result.Text()
	return ok && uint64(len(text)) <= builder.maxBytes
}

func (builder *Builder) render(units []Unit, status Status, cursor Cursor, summary []Warning) (Page, error) {
	switch builder.family {
	case unitProject:
		entries := make([]ProjectEntry, len(units))
		for index, unit := range units {
			if unit.family != unitProject {
				return Page{}, errInvalidPresentation
			}
			entries[index] = unit.project
		}
		return renderProjectPage(ProjectPage{
			Path:     builder.projectPath,
			Status:   status,
			Cursor:   cursor,
			Entries:  entries,
			Warnings: summary,
		}, builder.maxBytes)
	case unitSearchFile, unitSearchText, unitSearchSymbol:
		rows := make([]SearchRow, len(units))
		for index, unit := range units {
			if unit.family != builder.family {
				return Page{}, errInvalidPresentation
			}
			rows[index] = unit.search
		}
		return renderSearchPage(SearchPage{
			Mode:     builder.searchMode,
			Status:   status,
			Cursor:   cursor,
			Rows:     rows,
			Warnings: summary,
		}, builder.maxBytes)
	default:
		return Page{}, errInvalidPresentation
	}
}

func familyForSearchMode(mode SearchMode) unitFamily {
	switch mode {
	case SearchFile:
		return unitSearchFile
	case SearchText:
		return unitSearchText
	case SearchSymbol:
		return unitSearchSymbol
	default:
		return 0
	}
}

func appendUnit(units []Unit, unit Unit) []Unit {
	result := make([]Unit, len(units)+1)
	copy(result, units)
	result[len(units)] = unit
	return result
}

type outputBuffer struct {
	data     []byte
	limit    uint64
	overflow bool
}

func newOutputBuffer(limit uint64) *outputBuffer {
	return &outputBuffer{data: make([]byte, 0, int(limit+1)), limit: limit}
}

func (buffer *outputBuffer) appendString(value string) {
	maximum := int(buffer.limit + 1)
	if len(buffer.data) >= maximum {
		if value != "" {
			buffer.overflow = true
		}
		return
	}
	remaining := maximum - len(buffer.data)
	if len(value) > remaining {
		buffer.data = append(buffer.data, value[:remaining]...)
		buffer.overflow = true
		return
	}
	buffer.data = append(buffer.data, value...)
	if uint64(len(buffer.data)) > buffer.limit {
		buffer.overflow = true
	}
}

func (buffer *outputBuffer) appendByte(value byte) {
	maximum := int(buffer.limit + 1)
	if len(buffer.data) >= maximum {
		buffer.overflow = true
		return
	}
	buffer.data = append(buffer.data, value)
	if uint64(len(buffer.data)) > buffer.limit {
		buffer.overflow = true
	}
}

func (buffer *outputBuffer) appendUint(value uint64) {
	var encoded [20]byte
	bytes := strconv.AppendUint(encoded[:0], value, 10)
	buffer.appendString(string(bytes))
}

func (buffer *outputBuffer) quote(value string) error {
	return encodeQuotedScalar(value, buffer.appendString)
}

func (buffer *outputBuffer) finish() ([]byte, error) {
	if buffer.overflow {
		return buffer.data, errPresentationOverflow
	}
	return buffer.data, nil
}
