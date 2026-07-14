package codeparse

import (
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/navmodel"
)

func TestParseGoExtractsSemanticRecordsFromCanonicalBytes(t *testing.T) {
	source := []byte(`package sample

import (
	"fmt"
	alias "example.test/pkg"
)

const (
	Answer = 42
	Other  = 7
)
var Global int

type Item struct {
	Field string
}

type Service interface {
	Do() error
}

func (item *Item) Run() {}
func Build() Item { return Item{} }
`)

	parsed := parseGo(source)
	if parsed.fatal || len(parsed.errorRanges) != 0 {
		t.Fatalf("parseGo clean result = %#v", parsed)
	}
	projected, ok := projectRecords(parsed.records)
	if !ok {
		t.Fatal("Go projection rejected")
	}
	want := []struct {
		recordType navmodel.RecordKind
		kind       api.Kind
		name       string
		start      uint32
	}{
		{navmodel.Symbol, api.KindPackage, "sample", 1},
		{navmodel.Import, "", "fmt", 4},
		{navmodel.Import, "", "example.test/pkg", 5},
		{navmodel.Symbol, api.KindConstant, "Answer", 9},
		{navmodel.Symbol, api.KindConstant, "Other", 10},
		{navmodel.Symbol, api.KindVariable, "Global", 12},
		{navmodel.Symbol, api.KindStruct, "Item", 14},
		{navmodel.Symbol, api.KindField, "Field", 15},
		{navmodel.Symbol, api.KindInterface, "Service", 18},
		{navmodel.Symbol, api.KindMethod, "Do", 19},
		{navmodel.Symbol, api.KindMethod, "Item.Run", 22},
		{navmodel.Symbol, api.KindFunction, "Build", 23},
	}
	for _, expected := range want {
		if !hasProjectedRecord(projected, expected.recordType, expected.kind, expected.name, expected.start) {
			t.Errorf("missing record type=%d kind=%q name=%q start=%d in %#v", expected.recordType, expected.kind, expected.name, expected.start, projected)
		}
	}
	for _, record := range projected {
		if record.Name == "const" || record.Name == "var" || record.Name == "type" || record.Name == "import" {
			t.Fatalf("group wrapper escaped projection: %#v", record)
		}
	}
}

func TestParseGoKeepsExactRecoverableErrorPositions(t *testing.T) {
	source := []byte("package sample\n\nfunc Safe() {}\nfunc Broken( {\n")
	parsed := parseGo(source)
	if parsed.fatal || len(parsed.errorRanges) == 0 {
		t.Fatalf("malformed Go result = %#v", parsed)
	}
	if parsed.errorRanges[0].Start == 0 || parsed.errorRanges[0].End < parsed.errorRanges[0].Start {
		t.Fatalf("invalid error range: %#v", parsed.errorRanges)
	}
	projected, ok := projectRecords(filterUnsafeRecords(parsed.records, parsed.errorRanges))
	if !ok || !hasProjectedRecord(projected, navmodel.Symbol, api.KindFunction, "Safe", 3) {
		t.Fatalf("safe record lost: %#v,%t", projected, ok)
	}
	for _, record := range projected {
		if record.Name == "Broken" {
			t.Fatalf("unsafe record survived: %#v", record)
		}
	}
}

func hasProjectedRecord(records []navmodel.Record, recordType navmodel.RecordKind, kind api.Kind, name string, start uint32) bool {
	for _, record := range records {
		if record.Type == recordType && record.Kind == kind && record.Name == name && record.Range.Start == start {
			return true
		}
	}
	return false
}
