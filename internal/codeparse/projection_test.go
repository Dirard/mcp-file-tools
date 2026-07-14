package codeparse

import (
	"reflect"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/navmodel"
)

func TestProjectRecordsMapsSuppressesDeduplicatesAndSorts(t *testing.T) {
	raw := []rawRecord{
		{kind: "function", lineRange: navmodel.Range{Start: 8, End: 10}, depth: 1, name: "run"},
		{kind: "import_block", lineRange: navmodel.Range{Start: 2, End: 4}},
		{kind: "import", lineRange: navmodel.Range{Start: 3, End: 3}, name: "example/pkg"},
		{kind: "function", lineRange: navmodel.Range{Start: 8, End: 10}, depth: 1, name: "run"},
		{kind: "record", lineRange: navmodel.Range{Start: 5, End: 7}, name: "Item"},
		{kind: "value", lineRange: navmodel.Range{Start: 12, End: 12}, name: "secret literal"},
	}

	got, ok := projectRecords(raw)
	if !ok {
		t.Fatal("projection rejected valid records")
	}
	want := []navmodel.Record{
		{Type: navmodel.Import, Range: navmodel.Range{Start: 3, End: 3}, Name: "example/pkg"},
		{Type: navmodel.Symbol, Range: navmodel.Range{Start: 5, End: 7}, Kind: api.KindStruct, Name: "Item"},
		{Type: navmodel.Symbol, Range: navmodel.Range{Start: 8, End: 10}, Depth: 1, Kind: api.KindFunction, Name: "run"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("projection = %#v, want %#v", got, want)
	}
}

func TestProjectRecordsCoversClosedInternalMapping(t *testing.T) {
	tests := map[string]struct {
		recordType navmodel.RecordKind
		kind       api.Kind
	}{
		"import": {recordType: navmodel.Import}, "re_export": {recordType: navmodel.Import},
		"section": {recordType: navmodel.Heading, kind: api.KindSection}, "frontmatter": {recordType: navmodel.Heading, kind: api.KindSection},
		"package": {kind: api.KindPackage}, "module": {kind: api.KindModule}, "namespace": {kind: api.KindNamespace},
		"class": {kind: api.KindClass}, "interface": {kind: api.KindInterface}, "annotation": {kind: api.KindInterface}, "protocol": {kind: api.KindInterface},
		"struct": {kind: api.KindStruct}, "record": {kind: api.KindStruct}, "enum": {kind: api.KindEnum}, "trait": {kind: api.KindTrait},
		"type": {kind: api.KindType}, "union": {kind: api.KindType}, "typealias": {kind: api.KindType},
		"constant": {kind: api.KindConstant}, "const": {kind: api.KindConstant}, "enum_case": {kind: api.KindConstant},
		"variable": {kind: api.KindVariable}, "var": {kind: api.KindVariable}, "static": {kind: api.KindVariable},
		"field": {kind: api.KindField}, "property": {kind: api.KindProperty}, "key": {kind: api.KindProperty},
		"function": {kind: api.KindFunction}, "method": {kind: api.KindMethod}, "constructor": {kind: api.KindConstructor},
		"object": {kind: api.KindObject}, "companion_object": {kind: api.KindObject}, "mapping": {kind: api.KindObject}, "array": {kind: api.KindObject}, "sequence": {kind: api.KindObject}, "element": {kind: api.KindObject},
		"component": {kind: api.KindComponent}, "module_script": {kind: api.KindSection}, "script": {kind: api.KindSection}, "style": {kind: api.KindSection}, "markup": {kind: api.KindSection},
		"macro": {kind: api.KindOther}, "impl": {kind: api.KindOther}, "unknown_leaf": {kind: api.KindOther},
	}

	for internal, want := range tests {
		t.Run(internal, func(t *testing.T) {
			got, ok := projectRecords([]rawRecord{{kind: internal, lineRange: navmodel.Range{Start: 1, End: 1}, name: "n"}})
			if !ok || len(got) != 1 {
				t.Fatalf("projectRecords(%q) = %#v,%t", internal, got, ok)
			}
			wantType := want.recordType
			if wantType == 0 {
				wantType = navmodel.Symbol
			}
			if got[0].Type != wantType || got[0].Kind != want.kind {
				t.Fatalf("mapping %q = (%d,%q), want (%d,%q)", internal, got[0].Type, got[0].Kind, wantType, want.kind)
			}
		})
	}
}

func TestProjectRecordsRejectsCollidingModeSeekKeys(t *testing.T) {
	raw := []rawRecord{
		{kind: "function", lineRange: navmodel.Range{Start: 1, End: 2}, name: "same"},
		{kind: "method", lineRange: navmodel.Range{Start: 1, End: 2}, name: "same"},
	}
	if got, ok := projectRecords(raw); ok || got != nil {
		t.Fatalf("colliding outline key accepted: %#v,%t", got, ok)
	}
}
