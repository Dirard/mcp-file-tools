package jsonwire

import (
	"errors"
	"strings"
	"testing"
)

func TestScanDocumentRejectsDuplicateDecodedKeysAtEveryDepth(t *testing.T) {
	for _, raw := range []string{
		`{"a":1,"a":2}`,
		`{"a":1,"\u0061":2}`,
		`{"nested":{"x":1,"x":2}}`,
		`[{"x":1,"\u0078":2}]`,
	} {
		for _, mode := range []Mode{ValidateAll, ToolArguments} {
			requireScanErrorKindInMode(t, []byte(raw), protocolTestLimits(), mode, KindDuplicate)
		}
	}
}

func TestProtocolWithRawArgumentsRejectsDuplicatesOutsideArguments(t *testing.T) {
	for _, raw := range []string{
		`{"method":"tools/call","method":"tools/call","params":{"arguments":{}}}`,
		`{"method":"tools/call","params":{"arguments":{},"arguments":{}}}`,
		`{"method":"tools/call","params":{"arguments":{},"_meta":{"x":1,"x":2}}}`,
		`{"method":"tools/call","params":{"arguments":{},"extension":{"x":1,"x":2}}}`,
		`{"method":"tools/call","extension":{"x":1,"x":2},"params":{"arguments":{}}}`,
	} {
		requireScanErrorKindInMode(t, []byte(raw), protocolTestLimits(), ProtocolWithRawArguments, KindDuplicate)
	}
}

func TestProtocolWithRawArgumentsDefersOnlyTheLocatedArgumentsObject(t *testing.T) {
	for _, arguments := range []string{
		`{"x":1,"x":2}`,
		`{"x":1,"\u0078":2}`,
		`{"nested":{"x":1,"x":2}}`,
		`{"nested":[{"x":1,"x":2}]}`,
	} {
		raw := []byte(`{"jsonrpc":"2.0","params":{"name":"read","arguments":` + arguments + `},"method":"tools/call"}`)
		result, err := scanDocumentDetailed(raw, protocolTestLimits(), ProtocolWithRawArguments)
		if err != nil {
			t.Fatalf("protocol scan for %s: %v", arguments, err)
		}
		if !result.HasRawArguments {
			t.Fatalf("protocol scan for %s did not locate raw arguments", arguments)
		}
		if got := string(raw[result.RawArguments.Start:result.RawArguments.End]); got != arguments {
			t.Fatalf("raw arguments = %s, want %s", got, arguments)
		}
		requireScanErrorKindInMode(t, raw[result.RawArguments.Start:result.RawArguments.End], protocolTestLimits(), ToolArguments, KindDuplicate)
	}
}

func TestProtocolWithRawArgumentsDefersSurrogateSemanticsButNotStructure(t *testing.T) {
	raw := []byte(`{"method":"tools/call","params":{"arguments":{"value":"\uD800"}}}`)
	result, err := scanDocumentDetailed(raw, protocolTestLimits(), ProtocolWithRawArguments)
	if err != nil {
		t.Fatalf("protocol scan: %v", err)
	}
	requireScanErrorKindInMode(t, raw[result.RawArguments.Start:result.RawArguments.End], protocolTestLimits(), ToolArguments, KindUnicode)

	badOutside := []byte(`{"method":"tools/call","params":{"arguments":{},"_meta":{"value":"\uD800"}}}`)
	requireScanErrorKindInMode(t, badOutside, protocolTestLimits(), ProtocolWithRawArguments, KindUnicode)

	badStructure := []byte(`{"method":"tools/call","params":{"arguments":{"value":[}}}`)
	requireScanErrorKindInMode(t, badStructure, protocolTestLimits(), ProtocolWithRawArguments, KindSyntax)
}

func TestProtocolWithRawArgumentsRequiresObjectKind(t *testing.T) {
	raw := []byte(`{"params":{"arguments":null},"method":"tools/call"}`)
	requireScanErrorKindInMode(t, raw, protocolTestLimits(), ProtocolWithRawArguments, KindMismatch)
}

func TestProtocolWithRawArgumentsDoesNotDeferAnotherMethod(t *testing.T) {
	raw := []byte(`{"params":{"arguments":{"x":1,"x":2}},"method":"extension/run"}`)
	requireScanErrorKindInMode(t, raw, protocolTestLimits(), ProtocolWithRawArguments, KindDuplicate)
}

func TestProtocolWithRawArgumentsStillEnforcesResourceCapsInsideArguments(t *testing.T) {
	limits := protocolTestLimits()
	limits.MaxDepth = 3
	raw := []byte(`{"method":"tools/call","params":{"arguments":{"nested":{}}}}`)
	requireScanErrorKindInMode(t, raw, limits, ProtocolWithRawArguments, KindResource)
}

func TestProtocolWithRawArgumentsRecognizesDecodedProtocolKeys(t *testing.T) {
	raw := []byte(`{"par\u0061ms":{"arg\u0075ments":{"x":1,"x":2}},"meth\u006fd":"tools\/call"}`)
	result, err := scanDocumentDetailed(raw, protocolTestLimits(), ProtocolWithRawArguments)
	if err != nil {
		t.Fatalf("protocol scan: %v", err)
	}
	if !result.HasRawArguments || string(raw[result.RawArguments.Start:result.RawArguments.End]) != `{"x":1,"x":2}` {
		t.Fatalf("raw arguments = %#v", result.RawArguments)
	}
}

func requireScanErrorKindInMode(t *testing.T, raw []byte, limits Limits, mode Mode, want ErrorKind) {
	t.Helper()
	_, _, err := scanDocument(raw, limits, mode)
	if err == nil {
		t.Fatalf("scanDocument(%d bytes, mode %d) error = nil", len(raw), mode)
	}
	var validationError *ValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("error type = %T, want *ValidationError", err)
	}
	if validationError.Kind() != want {
		t.Fatalf("error kind = %q, want %q (input prefix %q)", validationError.Kind(), want, strings.TrimSpace(string(raw[:min(len(raw), 32)])))
	}
}
