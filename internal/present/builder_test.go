package present

import (
	"strings"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/navmodel"
)

func TestBuilderTryCommitFinalize(t *testing.T) {
	first, err := NewProjectUnit(ProjectDirectory, ".")
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewProjectUnit(ProjectFile, "a.go")
	if err != nil {
		t.Fatal(err)
	}
	single, err := renderProject(ProjectPage{Path: ".", Status: Partial, Cursor: readCursorPlaceholder, Entries: []ProjectEntry{{Kind: ProjectDirectory, Path: "."}}})
	if err != nil {
		t.Fatal(err)
	}
	secondAlone, err := renderProject(ProjectPage{Path: ".", Status: Partial, Cursor: readCursorPlaceholder, Entries: []ProjectEntry{{Kind: ProjectFile, Path: "a.go"}}})
	if err != nil {
		t.Fatal(err)
	}
	capBytes := len(single)
	if len(secondAlone) > capBytes {
		capBytes = len(secondAlone)
	}
	builder, err := newProjectBuilder(".", uint64(capBytes))
	if err != nil {
		t.Fatal(err)
	}
	if fit := builder.Try(first); fit != Fits {
		t.Fatalf("first unit fit=%d", fit)
	}
	builder.Commit(first)
	if fit := builder.Try(second); fit != NextPage {
		t.Fatalf("second unit fit=%d, want NextPage", fit)
	}
	result, err := builder.Finalize(Partial, testCursor, nil)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := result.Text()
	if text != "@@project\t\".\"\tpartial\trows=1\tcursor="+string(testCursor)+"\nD\t\".\"\n" {
		t.Fatalf("unexpected finalized page: %q", text)
	}

	intrinsicBuilder, _ := newProjectBuilder(".", 100)
	intrinsic, _ := NewProjectUnit(ProjectFile, strings.Repeat("\x01", 100))
	if fit := intrinsicBuilder.Try(intrinsic); fit != IntrinsicOverflow {
		t.Fatalf("intrinsic unit fit=%d", fit)
	}
}

func TestSearchBuilderAndSummaryPreflight(t *testing.T) {
	match, err := NewSearchTextUnit(SearchMatchRow, "huge.txt", 4294967296, "found\tvalue")
	if err != nil {
		t.Fatal(err)
	}
	builder, err := NewSearchBuilder(SearchText)
	if err != nil {
		t.Fatal(err)
	}
	if fit := builder.Try(match); fit != Fits {
		t.Fatalf("text unit fit=%d", fit)
	}
	builder.Commit(match)
	result, err := builder.Finalize(Complete, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := result.Text()
	want := "@@search\ttext\tcomplete\trows=1\tmatches=1\n@\t\"huge.txt\"\nM\t4294967296\tfound\tvalue\n"
	if text != want {
		t.Fatalf("unexpected search builder result: %q", text)
	}

	rowOnly, _, _ := renderSearch(SearchPage{Mode: SearchText, Status: Complete, Rows: []SearchRow{{Kind: SearchMatchRow, Path: "huge.txt", Line: 4294967296, Text: "found\tvalue"}}})
	rowEnvelope, _, _ := renderSearch(SearchPage{Mode: SearchText, Status: Partial, Cursor: readCursorPlaceholder, Rows: []SearchRow{{Kind: SearchMatchRow, Path: "huge.txt", Line: 4294967296, Text: "found\tvalue"}}})
	withSummary, _, _ := renderSearch(SearchPage{
		Mode: SearchText, Status: Complete,
		Rows:     []SearchRow{{Kind: SearchMatchRow, Path: "huge.txt", Line: 4294967296, Text: "found\tvalue"}},
		Warnings: []Warning{{Code: api.WarningParserSkipped, Count: 1, Path: "broken.go"}},
	})
	if len(withSummary) <= len(rowOnly) {
		t.Fatal("invalid summary fixture")
	}
	small, _ := newSearchBuilder(SearchText, uint64(len(rowEnvelope)))
	if small.Try(match) != Fits {
		t.Fatal("row must fit the small builder")
	}
	small.Commit(match)
	warnings := []Warning{{Code: api.WarningParserSkipped, Count: 1, Path: "broken.go"}}
	if fit := small.TrySummary(warnings); fit != NextPage {
		t.Fatalf("summary fit=%d, want NextPage", fit)
	}
	empty, _ := newSearchBuilder(SearchText, uint64(len(rowEnvelope)))
	if fit := empty.TrySummary(warnings); fit != Fits {
		t.Fatalf("aggregate-only summary fit=%d", fit)
	}
}

func TestUnitConstructors(t *testing.T) {
	if _, err := NewSearchFileUnit(""); err == nil {
		t.Fatal("empty file path accepted")
	}
	if _, err := NewSearchTextUnit(SearchContextRow, "a", 0, "x"); err == nil {
		t.Fatal("zero text line accepted")
	}
	record := navmodel.Record{Type: navmodel.Symbol, Range: navmodel.Range{Start: 1, End: 2}, Kind: api.KindFunction, Name: "run"}
	if _, err := NewSearchSymbolUnit("a.go", record); err != nil {
		t.Fatal(err)
	}
	record.Type = navmodel.Import
	if _, err := NewSearchSymbolUnit("a.go", record); err == nil {
		t.Fatal("non-symbol record accepted")
	}
}

func TestOutputBufferExactCap(t *testing.T) {
	for _, test := range []struct {
		value string
		ok    bool
	}{
		{value: "1234", ok: true},
		{value: "12345", ok: true},
		{value: "123456", ok: false},
	} {
		buffer := newOutputBuffer(5)
		buffer.appendString(test.value)
		got, err := buffer.finish()
		if (err == nil) != test.ok {
			t.Fatalf("value %q: err=%v", test.value, err)
		}
		if test.ok && string(got) != test.value {
			t.Fatalf("value %q changed to %q", test.value, got)
		}
		if !test.ok && len(got) != 6 {
			t.Fatalf("overflow scratch retained %d bytes, want cap+1", len(got))
		}
	}
}
