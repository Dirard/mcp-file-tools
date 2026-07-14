package present

import (
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/navmodel"
)

func TestRenderSearch(t *testing.T) {
	t.Run("file", func(t *testing.T) {
		page, err := RenderSearch(SearchPage{
			Mode:   SearchFile,
			Status: Complete,
			Rows: []SearchRow{
				{Kind: SearchFileRow, Path: "a.go"},
				{Kind: SearchFileRow, Path: "b.go"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		assertSearchPage(t, page, "@@search\tfile\tcomplete\trows=2\nF\t\"a.go\"\nF\t\"b.go\"\n", 2, 0, true)
	})

	t.Run("text uint64 literal rows", func(t *testing.T) {
		page, err := RenderSearch(SearchPage{
			Mode:   SearchText,
			Status: Partial,
			Cursor: testCursor,
			Rows: []SearchRow{
				{Kind: SearchMatchRow, Path: "huge.txt", Line: 4294967295, Text: "found\t|value"},
				{Kind: SearchContextRow, Path: "huge.txt", Line: 4294967296, Text: ""},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		want := "@@search\ttext\tpartial\trows=2\tmatches=1\tcursor=" + string(testCursor) + "\n" +
			"@\t\"huge.txt\"\n" +
			"M\t4294967295\tfound\t|value\n" +
			"C\t4294967296\t\n"
		assertSearchPage(t, page, want, 2, 1, false)
	})

	t.Run("symbol grouping", func(t *testing.T) {
		page, err := RenderSearch(SearchPage{
			Mode:   SearchSymbol,
			Status: Complete,
			Rows: []SearchRow{
				{Kind: SearchSymbolRow, Path: "a.go", Range: navmodel.Range{Start: 2, End: 4}, SymbolKind: api.KindFunction, Name: "run"},
				{Kind: SearchSymbolRow, Path: "b.go", Range: navmodel.Range{Start: 8, End: 8}, SymbolKind: api.KindVariable, Name: "x\n"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		want := "@@search\tsymbol\tcomplete\trows=2\n" +
			"@\t\"a.go\"\nS\t2:4\tfunction\t\"run\"\n" +
			"@\t\"b.go\"\nS\t8:8\tvariable\t\"x\\n\"\n"
		assertSearchPage(t, page, want, 2, 0, true)
	})

	zero, err := RenderSearch(SearchPage{Mode: SearchText, Status: Complete})
	if err != nil {
		t.Fatal(err)
	}
	assertSearchPage(t, zero, "@@search\ttext\tcomplete\trows=0\tmatches=0\n", 0, 0, true)
}

func TestRenderSearchRejectsInvalidInput(t *testing.T) {
	invalid := []SearchPage{
		{Mode: 0, Status: Complete},
		{Mode: SearchFile, Status: Complete, Rows: []SearchRow{{Kind: SearchMatchRow, Path: "a", Line: 1, Text: "x"}}},
		{Mode: SearchText, Status: Complete, Rows: []SearchRow{{Kind: SearchMatchRow, Path: "a", Text: "x"}}},
		{Mode: SearchText, Status: Complete, Rows: []SearchRow{{Kind: SearchMatchRow, Path: "a", Line: 1, Text: "x\n"}}},
		{Mode: SearchSymbol, Status: Complete, Rows: []SearchRow{{Kind: SearchSymbolRow, Path: "a", Range: navmodel.Range{Start: 2, End: 1}, SymbolKind: api.KindFunction, Name: "x"}}},
		{Mode: SearchSymbol, Status: Complete, Rows: []SearchRow{{Kind: SearchSymbolRow, Path: "a", Range: navmodel.Range{Start: 1, End: 1}, SymbolKind: "unknown", Name: "x"}}},
		{Mode: SearchFile, Status: Complete, Rows: []SearchRow{{Kind: SearchFileRow, Path: "b"}, {Kind: SearchFileRow, Path: "a"}}},
		{Mode: SearchFile, Status: Partial, Cursor: "short"},
	}
	for index, input := range invalid {
		if _, err := RenderSearch(input); err == nil {
			t.Fatalf("invalid search page %d was accepted", index)
		}
	}
}

func assertSearchPage(t *testing.T, page Page, want string, rows, matches uint64, complete bool) {
	t.Helper()
	text, ok := page.Result.Text()
	if !ok || text != want || page.Rows != rows || page.Matches != matches || page.Items != 0 || page.Complete != complete || page.Result.IsError() || page.Result.Validate() != nil {
		t.Fatalf("unexpected search page: text=%q rows=%d matches=%d items=%d complete=%v resultErr=%v", text, page.Rows, page.Matches, page.Items, page.Complete, page.Result.Validate())
	}
}
