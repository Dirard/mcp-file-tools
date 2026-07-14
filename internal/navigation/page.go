package navigation

import (
	"errors"

	"github.com/Dirard/mcp-file-tools/internal/navmodel"
	"github.com/Dirard/mcp-file-tools/internal/present"
	"github.com/Dirard/mcp-file-tools/internal/scanner"
)

var errNavigationPresentation = errors.New("navigation: presentation failure")

type rowPage struct {
	builder *present.Builder
	mode    dynamicMode
	pending scanner.Row
	unit    present.Unit
	set     bool
	err     error
}

func (page *rowPage) Try(row scanner.Row) scanner.RowFit {
	if page == nil || page.builder == nil || page.err != nil || page.set {
		if page != nil && page.err == nil {
			page.err = errNavigationPresentation
		}
		return scanner.RowIntrinsicOverflow
	}
	unit, err := presentUnit(page.mode, row)
	if err != nil {
		page.err = errNavigationPresentation
		return scanner.RowIntrinsicOverflow
	}
	fit := page.builder.Try(unit)
	page.pending = row
	page.unit = unit
	page.set = true
	switch fit {
	case present.Fits:
		return scanner.RowFits
	case present.NextPage:
		return scanner.RowNextPage
	case present.IntrinsicOverflow:
		return scanner.RowIntrinsicOverflow
	default:
		page.err = errNavigationPresentation
		return scanner.RowIntrinsicOverflow
	}
}

func (page *rowPage) Commit(row scanner.Row) {
	if page == nil || page.err != nil || !page.set || page.pending != row {
		if page != nil && page.err == nil {
			page.err = errNavigationPresentation
		}
		return
	}
	page.builder.Commit(page.unit)
	page.pending = scanner.Row{}
	page.unit = present.Unit{}
	page.set = false
}

func presentUnit(mode dynamicMode, row scanner.Row) (present.Unit, error) {
	switch mode {
	case dynamicProject:
		switch row.Kind {
		case scanner.RowDirectory:
			return present.NewProjectUnit(present.ProjectDirectory, row.Path)
		case scanner.RowFile:
			return present.NewProjectUnit(present.ProjectFile, row.Path)
		}
	case dynamicFileSearch:
		if row.Kind == scanner.RowFile {
			return present.NewSearchFileUnit(row.Path)
		}
	case dynamicTextSearch:
		switch row.Kind {
		case scanner.RowTextMatch:
			return present.NewSearchTextUnit(present.SearchMatchRow, row.Path, row.Line, row.Text)
		case scanner.RowTextContext:
			return present.NewSearchTextUnit(present.SearchContextRow, row.Path, row.Line, row.Text)
		}
	case dynamicSymbolSearch:
		if row.Kind == scanner.RowSymbol {
			return present.NewSearchSymbolUnit(row.Path, navmodel.Record{
				Type:  navmodel.Symbol,
				Range: row.Range,
				Kind:  row.SymbolKind,
				Name:  row.Name,
			})
		}
	}
	return present.Unit{}, errNavigationPresentation
}

func warningsFromBatch(batch scanner.Batch) []present.Warning {
	if len(batch.Warnings) == 0 {
		return nil
	}
	warnings := make([]present.Warning, len(batch.Warnings))
	for index, summary := range batch.Warnings {
		warnings[index] = present.Warning{
			Code:  summary.Code(),
			Count: summary.Count(),
			Path:  summary.Example(),
		}
	}
	return warnings
}
