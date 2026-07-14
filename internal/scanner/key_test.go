package scanner

import (
	"bytes"
	"sort"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/navmodel"
)

func TestRowOrderingAndEncodingPreserveUint64Lines(t *testing.T) {
	t.Parallel()

	rows := []Row{
		{Kind: RowTextContext, Path: "z.go", Line: 1, Text: "z"},
		{Kind: RowTextMatch, Path: "a.go", Line: 4_294_967_296, Text: "next"},
		{Kind: RowFile, Path: "b.go"},
		{Kind: RowDirectory, Path: "b.go"},
		{Kind: RowTextContext, Path: "a.go", Line: 4_294_967_295, Text: "edge"},
		{Kind: RowTextMatch, Path: "a.go", Line: 4_294_967_295, Text: "edge"},
		{Kind: RowSymbol, Path: "a.go", Range: navmodel.Range{Start: 1, End: 2}, SymbolKind: api.KindFunction, Name: "f"},
	}
	sort.Slice(rows, func(i, j int) bool { return compareRows(rows[i], rows[j]) < 0 })

	wantKinds := []RowKind{
		RowTextMatch,
		RowTextContext,
		RowTextMatch,
		RowSymbol,
		RowDirectory,
		RowFile,
		RowTextContext,
	}
	for index, want := range wantKinds {
		if rows[index].Kind != want {
			t.Fatalf("row %d kind = %d, want %d", index, rows[index].Kind, want)
		}
	}
	before := encodeRowKey(Row{Kind: RowTextMatch, Path: "a.go", Line: 4_294_967_295})
	after := encodeRowKey(Row{Kind: RowTextMatch, Path: "a.go", Line: 4_294_967_296})
	if bytes.Compare(before, after) >= 0 {
		t.Fatal("binary row keys wrapped at the uint32 boundary")
	}
}
