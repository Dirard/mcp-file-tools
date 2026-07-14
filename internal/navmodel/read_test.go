package navmodel

import (
	"strings"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

func TestReadViewAndLine(t *testing.T) {
	for _, view := range []ReadView{ReadSource, ReadOutline} {
		if !view.Valid() {
			t.Fatalf("expected view %d to be valid", view)
		}
	}
	for _, view := range []ReadView{0, 3} {
		if view.Valid() {
			t.Fatalf("expected view %d to be invalid", view)
		}
	}

	line, err := NewReadLine(2147483647, strings.Repeat("x", 4096))
	if err != nil || line.Number() != 2147483647 || len(line.Text()) != 4096 || line.Validate() != nil {
		t.Fatalf("valid boundary line rejected: line=%+v err=%v", line, err)
	}

	invalid := []struct {
		number uint32
		text   string
	}{
		{0, "x"},
		{2147483648, "x"},
		{1, "bad\nline"},
		{1, "bad\rline"},
		{1, strings.Repeat("x", 4097)},
		{1, string([]byte{0xff})},
	}
	for _, test := range invalid {
		if _, err := NewReadLine(test.number, test.text); err == nil {
			t.Fatalf("accepted invalid line number=%d len=%d", test.number, len(test.text))
		}
	}
	if (ReadLine{}).Validate() == nil {
		t.Fatal("zero read line must be invalid")
	}
}

func TestReadItemConstructors(t *testing.T) {
	warnings := []api.WarningCode{api.WarningParserPartial, api.WarningBinarySkipped}
	lines := []ReadLine{mustReadLine(t, 7, "first|value"), mustReadLine(t, 8, "second")}
	source, err := NewReadSourceItem(0, "src/main.go", lines, warnings)
	if err != nil {
		t.Fatal(err)
	}
	lines[0] = mustReadLine(t, 99, "mutated")
	warnings[0] = api.WarningMountSkipped
	if source.Kind() != ReadItemSourceRows || source.View() != ReadSource || source.Index() != 0 || !source.Success() {
		t.Fatalf("unexpected source shape: kind=%d view=%d index=%d", source.Kind(), source.View(), source.Index())
	}
	if path, ok := source.Path(); !ok || path != "src/main.go" {
		t.Fatalf("unexpected source path: %q %v", path, ok)
	}
	gotLines, ok := source.Lines()
	if !ok || gotLines[0].Number() != 7 || gotLines[0].Text() != "first|value" {
		t.Fatalf("source did not retain an owned line copy: %+v", gotLines)
	}
	gotLines[0] = mustReadLine(t, 1, "changed")
	if again, _ := source.Lines(); again[0].Number() != 7 {
		t.Fatal("mutating Lines result changed the item")
	}
	gotWarnings := source.Warnings()
	if len(gotWarnings) != 2 || gotWarnings[0] != api.WarningBinarySkipped || gotWarnings[1] != api.WarningParserPartial {
		t.Fatalf("warnings not copied and ASCII sorted: %v", gotWarnings)
	}
	gotWarnings[0] = api.WarningMountSkipped
	if source.Warnings()[0] != api.WarningBinarySkipped {
		t.Fatal("mutating Warnings result changed the item")
	}
	if source.Validate() != nil || source.Footprint() == 0 {
		t.Fatalf("valid source rejected: %v", source.Validate())
	}

	records := []Record{
		{Type: Import, Range: Range{Start: 1, End: 2}, Name: "fmt"},
		{Type: Symbol, Range: Range{Start: 4, End: 8}, Kind: api.KindFunction, Name: "main"},
	}
	outline, err := NewReadOutlineItem(1, "src/main.go", api.LanguageGo, records, nil)
	if err != nil {
		t.Fatal(err)
	}
	records[0].Name = "mutated"
	gotRecords, ok := outline.Records()
	if !ok || gotRecords[0].Name != "fmt" {
		t.Fatalf("outline did not retain an owned record copy: %+v", gotRecords)
	}
	gotRecords[0].Name = "changed"
	if again, _ := outline.Records(); again[0].Name != "fmt" {
		t.Fatal("mutating Records result changed the item")
	}
	if language, ok := outline.Language(); !ok || language != api.LanguageGo {
		t.Fatalf("unexpected outline language: %q %v", language, ok)
	}

	if empty, err := NewReadSourceEmptyItem(2, "empty.txt", nil); err != nil || empty.Kind() != ReadItemEmpty || !empty.Success() {
		t.Fatalf("source empty rejected: item=%+v err=%v", empty, err)
	}
	if empty, err := NewReadOutlineEmptyItem(3, "empty.go", api.LanguageGo, nil); err != nil || empty.Kind() != ReadItemEmpty || !empty.Success() {
		t.Fatalf("outline empty rejected: item=%+v err=%v", empty, err)
	}

	allowed := map[api.ErrorCode]bool{
		api.ErrorInvalidInput: true, api.ErrorNotFound: true, api.ErrorBinary: true,
		api.ErrorUnsupportedEncoding: true, api.ErrorUnsupportedLanguage: true,
		api.ErrorLineTooLong: true, api.ErrorBudgetExceeded: true,
		api.ErrorPermissionDenied: true, api.ErrorIOError: true, api.ErrorParserFailed: true,
	}
	for _, code := range api.OrderedErrorCodes() {
		item, err := NewReadErrorItem(ReadSource, 0, code, nil)
		if allowed[code] {
			if err != nil || item.Success() || item.Kind() != ReadItemFailure {
				t.Fatalf("allowed item error %q rejected: item=%+v err=%v", code, item, err)
			}
			if _, ok := item.Path(); ok {
				t.Fatalf("error item %q retained a path", code)
			}
			if got, ok := item.ErrorCode(); !ok || got != code {
				t.Fatalf("error item lost code %q: %q %v", code, got, ok)
			}
		} else if err == nil {
			t.Fatalf("non-item error %q accepted", code)
		}
	}

	invalidConstructors := []func() error{
		func() error {
			_, err := NewReadSourceItem(24, "x", []ReadLine{mustReadLine(t, 1, "x")}, nil)
			return err
		},
		func() error { _, err := NewReadSourceItem(0, "", []ReadLine{mustReadLine(t, 1, "x")}, nil); return err },
		func() error {
			_, err := NewReadSourceItem(0, "x", []ReadLine{mustReadLine(t, 1, "x"), mustReadLine(t, 3, "y")}, nil)
			return err
		},
		func() error { _, err := NewReadOutlineItem(0, "x", "unknown", records, nil); return err },
		func() error {
			_, err := NewReadOutlineItem(0, "x", api.LanguageGo, []Record{records[1], records[0]}, nil)
			return err
		},
		func() error {
			_, err := NewReadOutlineItem(0, "x", api.LanguageGo, []Record{records[0], records[0]}, nil)
			return err
		},
		func() error {
			_, err := NewReadSourceEmptyItem(0, "x", []api.WarningCode{api.WarningBinarySkipped, api.WarningBinarySkipped})
			return err
		},
	}
	for index, construct := range invalidConstructors {
		if err := construct(); err == nil {
			t.Fatalf("invalid constructor case %d was accepted", index)
		}
	}
	if (ReadItem{}).Validate() == nil {
		t.Fatal("zero read item must be invalid")
	}
}

func TestReadSnapshotImmutabilityAndOutcomes(t *testing.T) {
	warnings := []api.WarningCode{api.WarningParserPartial}
	source, err := NewReadSourceItem(0, "a.go", []ReadLine{mustReadLine(t, 1, "package a")}, warnings)
	if err != nil {
		t.Fatal(err)
	}
	failure, err := NewReadErrorItem(ReadSource, 1, api.ErrorBudgetExceeded, warnings)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := NewReadSourceEmptyItem(2, "empty.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	items := []ReadItem{source, failure, empty}
	snapshot, err := NewReadSnapshot(ReadSource, items)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshot.Footprint()
	items[0] = ReadItem{}
	warnings[0] = api.WarningMountSkipped
	returned := snapshot.Items()
	returned[0] = ReadItem{}
	if snapshot.View() != ReadSource || snapshot.Success() != 2 || snapshot.Failed() != 1 || snapshot.Footprint() != before || snapshot.Validate() != nil {
		t.Fatalf("snapshot changed through caller-owned state: success=%d failed=%d footprint=%d err=%v", snapshot.Success(), snapshot.Failed(), snapshot.Footprint(), snapshot.Validate())
	}
	stored := snapshot.Items()
	if code, ok := stored[1].ErrorCode(); !ok || code != api.ErrorBudgetExceeded {
		t.Fatalf("snapshot lost per-file budget error: %q %v", code, ok)
	}
	if _, ok := stored[1].Path(); ok {
		t.Fatal("snapshot error item exposed a path")
	}

	for count := 1; count <= 25; count++ {
		batch := make([]ReadItem, count)
		for index := range batch {
			batch[index], err = NewReadSourceEmptyItem(uint32(index), "empty.txt", nil)
			if err != nil {
				break
			}
		}
		_, gotErr := NewReadSnapshot(ReadSource, batch)
		if (count <= 24) == (gotErr != nil) {
			t.Fatalf("unexpected snapshot result for %d items: %v", count, gotErr)
		}
	}

	wrongIndex, _ := NewReadSourceEmptyItem(1, "x", nil)
	if _, err := NewReadSnapshot(ReadSource, []ReadItem{wrongIndex}); err == nil {
		t.Fatal("snapshot accepted an out-of-order index")
	}
	wrongView, _ := NewReadOutlineEmptyItem(0, "x.go", api.LanguageGo, nil)
	if _, err := NewReadSnapshot(ReadSource, []ReadItem{wrongView}); err == nil {
		t.Fatal("snapshot accepted a view mismatch")
	}
	if (ReadSnapshot{}).Validate() == nil {
		t.Fatal("zero snapshot must be invalid")
	}
}

func mustReadLine(t *testing.T, number uint32, text string) ReadLine {
	t.Helper()
	line, err := NewReadLine(number, text)
	if err != nil {
		t.Fatal(err)
	}
	return line
}
