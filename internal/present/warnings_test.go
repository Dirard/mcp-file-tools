package present

import (
	"strings"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/navmodel"
)

func TestRenderBroadWarnings(t *testing.T) {
	page, err := RenderSearch(SearchPage{
		Mode:   SearchFile,
		Status: Complete,
		Warnings: []Warning{
			{Code: api.WarningParserSkipped, Count: 2, Path: "b.go"},
			{Code: api.WarningBinarySkipped, Count: 1},
			{Code: api.WarningMountSkipped, Count: 3, Path: strings.Repeat("x", 129)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "@@search\tfile\tcomplete\trows=0\n" +
		"!\tbinary_skipped\tcount=1\tomitted=1\n" +
		"!\tmount_skipped\tcount=3\tomitted=3\n" +
		"!\tparser_skipped\tcount=2\tomitted=1\tpath=\"b.go\"\n"
	text, _ := page.Result.Text()
	if text != want {
		t.Fatalf("unexpected warning output:\n%s\nwant:\n%s", text, want)
	}

	invalid := []SearchPage{
		{Mode: SearchFile, Status: Partial, Cursor: testCursor, Warnings: []Warning{{Code: api.WarningBinarySkipped, Count: 1}}},
		{Mode: SearchFile, Status: Complete, Warnings: []Warning{{Code: api.WarningBinarySkipped}}},
		{Mode: SearchFile, Status: Complete, Warnings: []Warning{{Code: "bad", Count: 1}}},
		{Mode: SearchFile, Status: Complete, Warnings: []Warning{{Code: api.WarningBinarySkipped, Count: 1}, {Code: api.WarningBinarySkipped, Count: 2}}},
	}
	for index, input := range invalid {
		if _, err := RenderSearch(input); err == nil {
			t.Fatalf("invalid warning set %d was accepted", index)
		}
	}
}

func TestWarningsFromAccumulator(t *testing.T) {
	var accumulator navmodel.Accumulator
	if err := accumulator.AddCandidate("broken.go", api.WarningParserPartial, api.WarningParserPartial); err != nil {
		t.Fatal(err)
	}
	warnings, err := WarningsFromAccumulator(accumulator)
	if err != nil || len(warnings) != 1 || warnings[0].Code != api.WarningParserPartial || warnings[0].Count != 1 || warnings[0].Path != "broken.go" {
		t.Fatalf("unexpected conversion: warnings=%+v err=%v", warnings, err)
	}
}
