package navmodel

import (
	"reflect"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

func TestRecordSurfaceIsNavigationOnly(t *testing.T) {
	typeOfRecord := reflect.TypeOf(Record{})
	want := map[string]reflect.Kind{
		"Type": reflect.Uint8, "Range": reflect.Struct, "Depth": reflect.Uint16, "Kind": reflect.String, "Name": reflect.String,
	}
	if typeOfRecord.NumField() != len(want) {
		t.Fatalf("Record has %d fields, want %d", typeOfRecord.NumField(), len(want))
	}
	for name, kind := range want {
		field, ok := typeOfRecord.FieldByName(name)
		if !ok || field.Type.Kind() != kind {
			t.Fatalf("Record field %q = %#v, present %t", name, field, ok)
		}
	}
}

func TestRecordValidationAndSeekKeys(t *testing.T) {
	record, ok := NewRecord(Record{
		Type:  Symbol,
		Range: Range{Start: 3, End: 8},
		Depth: 2,
		Kind:  api.KindFunction,
		Name:  "Serve",
	})
	if !ok {
		t.Fatal("valid record rejected")
	}
	if !record.Range.Valid() || !record.Valid() {
		t.Fatal("record is not valid")
	}
	if got := record.OutlineSeekKey(); got != (OutlineSeekKey{Start: 3, End: 8, Type: Symbol, Depth: 2, Name: "Serve"}) {
		t.Fatalf("outline key = %#v", got)
	}
	if got := record.SymbolSeekKey("pkg/server.go"); got != (SymbolSeekKey{Path: "pkg/server.go", Start: 3, End: 8, Kind: api.KindFunction, Name: "Serve"}) {
		t.Fatalf("symbol key = %#v", got)
	}

	invalid := []Record{
		{Type: Symbol, Range: Range{Start: 0, End: 1}, Kind: api.KindFunction, Name: "x"},
		{Type: Symbol, Range: Range{Start: 2, End: 1}, Kind: api.KindFunction, Name: "x"},
		{Type: 99, Range: Range{Start: 1, End: 1}, Kind: api.KindFunction, Name: "x"},
		{Type: Symbol, Range: Range{Start: 1, End: 1}, Kind: api.Kind("unknown"), Name: "x"},
		{Type: Import, Range: Range{Start: 1, End: 1}, Kind: api.KindFunction, Name: "x"},
	}
	for index, candidate := range invalid {
		if _, ok := NewRecord(candidate); ok {
			t.Fatalf("invalid record %d accepted", index)
		}
	}
}

func TestCloneRecordsOwnsNamesAndBacking(t *testing.T) {
	nameBytes := []byte("Alpha")
	record, ok := NewRecord(Record{Type: Symbol, Range: Range{Start: 1, End: 1}, Kind: api.KindClass, Name: string(nameBytes)})
	if !ok {
		t.Fatal("record rejected")
	}
	nameBytes[0] = 'X'
	if record.Name != "Alpha" {
		t.Fatalf("record retained caller name: %q", record.Name)
	}

	source := []Record{record}
	clone, ok := CloneRecords(source)
	if !ok || len(clone) != 1 {
		t.Fatalf("CloneRecords() = %#v,%t", clone, ok)
	}
	source[0].Name = "Changed"
	if clone[0].Name != "Alpha" {
		t.Fatalf("clone changed with source: %q", clone[0].Name)
	}
	if cap(clone) != len(clone) || RecordsFootprint(clone) == 0 {
		t.Fatalf("clone capacity/footprint = %d/%d, %d", len(clone), cap(clone), RecordsFootprint(clone))
	}
}
