package codeparse

import (
	"reflect"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/navmodel"
)

func TestParserRecordsExposeOnlyNavigationData(t *testing.T) {
	resultType := reflect.TypeOf(Result{})
	for _, forbidden := range []string{"Source", "Literal", "Fingerprint", "ByteOffset", "Selector", "Profile", "Node"} {
		if _, present := resultType.FieldByName(forbidden); present {
			t.Fatalf("Result exposes forbidden field %q", forbidden)
		}
	}

	result := Result{
		Language: api.LanguageGo,
		State:    Clean,
		Records:  []navmodel.Record{{Type: navmodel.Symbol, Range: navmodel.Range{Start: 1, End: 1}, Kind: api.KindFunction, Name: "main"}},
	}
	clone, ok := cloneResult(result)
	if !ok {
		t.Fatal("cloneResult rejected valid result")
	}
	result.Records[0].Name = "changed"
	if clone.Records[0].Name != "main" {
		t.Fatalf("clone retained caller-owned records: %#v", clone)
	}
}
