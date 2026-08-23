package present

import (
	"strings"
	"unicode/utf8"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/config"
	"github.com/Dirard/mcp-file-tools/internal/navmodel"
)

type SearchMode uint8

const (
	SearchFile SearchMode = iota + 1
	SearchText
	SearchSymbol
)

type SearchRowKind uint8

const (
	SearchFileRow SearchRowKind = iota + 1
	SearchMatchRow
	SearchContextRow
	SearchSymbolRow
)

type SearchRow struct {
	Kind       SearchRowKind
	Path       string
	Line       uint64
	Text       string
	Range      navmodel.Range
	SymbolKind api.Kind
	Name       string
}

type SearchPage struct {
	Mode     SearchMode
	Status   Status
	Cursor   Cursor
	Rows     []SearchRow
	Warnings []Warning
}

func RenderSearch(input SearchPage) (Page, error) {
	return renderSearchPage(input, config.OutputMaxBytes)
}

func renderSearchPage(input SearchPage, maxBytes uint64) (Page, error) {
	text, matches, err := renderSearchLimit(input, maxBytes)
	if err != nil {
		return Page{}, errInvalidPresentation
	}
	result := api.Navigation(string(text), false)
	if result.Validate() != nil {
		return Page{}, errInvalidPresentation
	}
	return Page{
		Result:   result,
		Rows:     uint64(len(input.Rows)),
		Matches:  matches,
		Complete: input.Status == Complete,
	}, nil
}

func renderSearch(input SearchPage) ([]byte, uint64, error) {
	return renderSearchLimit(input, config.OutputMaxBytes)
}

func renderSearchLimit(input SearchPage, maxBytes uint64) ([]byte, uint64, error) {
	if !validSearchMode(input.Mode) || !validStatusCursor(input.Status, input.Cursor) || !validSearchRows(input.Mode, input.Rows) {
		return nil, 0, errInvalidPresentation
	}
	warnings, err := normalizeWarnings(input.Status, input.Warnings)
	if err != nil {
		return nil, 0, err
	}
	var matches uint64
	for _, row := range input.Rows {
		if row.Kind == SearchMatchRow {
			matches++
		}
	}

	buffer := newOutputBuffer(maxBytes)
	buffer.appendString("@@search\t")
	buffer.appendString(searchModeName(input.Mode))
	buffer.appendByte('\t')
	buffer.appendString(statusName(input.Status))
	buffer.appendString("\trows=")
	buffer.appendUint(uint64(len(input.Rows)))
	if input.Mode == SearchText {
		buffer.appendString("\tmatches=")
		buffer.appendUint(matches)
	}
	buffer.appendString(cursorField(input.Status, input.Cursor))
	buffer.appendByte('\n')

	var groupedPath string
	for _, row := range input.Rows {
		if input.Mode != SearchFile && row.Path != groupedPath {
			buffer.appendString("@\t")
			if err := buffer.quote(row.Path); err != nil {
				return nil, 0, err
			}
			buffer.appendByte('\n')
			groupedPath = row.Path
		}

		switch row.Kind {
		case SearchFileRow:
			buffer.appendString("F\t")
			if err := buffer.quote(row.Path); err != nil {
				return nil, 0, err
			}
		case SearchMatchRow, SearchContextRow:
			if row.Kind == SearchMatchRow {
				buffer.appendString("M\t")
			} else {
				buffer.appendString("C\t")
			}
			buffer.appendUint(row.Line)
			buffer.appendByte('\t')
			buffer.appendString(row.Text)
		case SearchSymbolRow:
			buffer.appendString("S\t")
			buffer.appendUint(uint64(row.Range.Start))
			buffer.appendByte(':')
			buffer.appendUint(uint64(row.Range.End))
			buffer.appendByte('\t')
			buffer.appendString(string(row.SymbolKind))
			buffer.appendByte('\t')
			if err := buffer.quote(row.Name); err != nil {
				return nil, 0, err
			}
		}
		buffer.appendByte('\n')
	}
	if err := appendBroadWarningsBuffer(buffer, warnings); err != nil {
		return nil, 0, err
	}
	text, err := buffer.finish()
	if err != nil {
		return text, 0, err
	}
	return text, matches, nil
}

func validSearchMode(mode SearchMode) bool {
	return mode == SearchFile || mode == SearchText || mode == SearchSymbol
}

func searchModeName(mode SearchMode) string {
	switch mode {
	case SearchFile:
		return "file"
	case SearchText:
		return "text"
	case SearchSymbol:
		return "symbol"
	default:
		return ""
	}
}

func validSearchRows(mode SearchMode, rows []SearchRow) bool {
	for index, row := range rows {
		if !validPresentPath(row.Path) || !validSearchRow(mode, row) {
			return false
		}
		if index > 0 && compareSearchRows(mode, rows[index-1], row) >= 0 {
			return false
		}
	}
	return true
}

func validSearchRow(mode SearchMode, row SearchRow) bool {
	switch mode {
	case SearchFile:
		return row.Kind == SearchFileRow && row.Line == 0 && row.Text == "" && row.Range == (navmodel.Range{}) && row.SymbolKind == "" && row.Name == ""
	case SearchText:
		return (row.Kind == SearchMatchRow || row.Kind == SearchContextRow) && row.Line != 0 &&
			utf8.ValidString(row.Text) &&
			strings.IndexByte(row.Text, '\r') < 0 && strings.IndexByte(row.Text, '\n') < 0 &&
			row.Range == (navmodel.Range{}) && row.SymbolKind == "" && row.Name == ""
	case SearchSymbol:
		return row.Kind == SearchSymbolRow && row.Line == 0 && row.Text == "" &&
			row.Range.Start != 0 && row.Range.End >= row.Range.Start && row.SymbolKind.Valid() && utf8.ValidString(row.Name)
	default:
		return false
	}
}

func compareSearchRows(mode SearchMode, left, right SearchRow) int {
	if pathOrder := strings.Compare(left.Path, right.Path); pathOrder != 0 {
		return pathOrder
	}
	switch mode {
	case SearchFile:
		return 0
	case SearchText:
		if left.Line != right.Line {
			if left.Line < right.Line {
				return -1
			}
			return 1
		}
		if left.Kind < right.Kind {
			return -1
		}
		if left.Kind > right.Kind {
			return 1
		}
		return 0
	case SearchSymbol:
		if left.Range.Start != right.Range.Start {
			if left.Range.Start < right.Range.Start {
				return -1
			}
			return 1
		}
		if left.Range.End != right.Range.End {
			if left.Range.End < right.Range.End {
				return -1
			}
			return 1
		}
		if kindOrder := strings.Compare(string(left.SymbolKind), string(right.SymbolKind)); kindOrder != 0 {
			return kindOrder
		}
		return strings.Compare(left.Name, right.Name)
	default:
		return 0
	}
}
