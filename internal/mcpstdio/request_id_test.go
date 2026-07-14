package mcpstdio

import (
	"strings"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/config"
)

func TestClassifyInboundPreservesExactValidRequestIDs(t *testing.T) {
	largeInteger := strings.Repeat("9", 120)
	maxString := `"` + strings.Repeat("x", int(config.RequestIDMaxRawBytes)-2) + `"`
	maxNumber := strings.Repeat("8", int(config.RequestIDMaxRawBytes))
	for _, rawID := range []string{
		`"a"`,
		`"\u0061"`,
		`""`,
		`0`,
		`-0`,
		`1e3`,
		`1000.0`,
		`1E+03`,
		largeInteger,
		maxString,
		maxNumber,
	} {
		raw := []byte(`{"jsonrpc":"2.0","id":` + rawID + `,"method":"ping"}`)
		message, err := classifyInbound(raw)
		if err != nil {
			t.Fatalf("classifyInbound(id=%s) error = %v", rawID, err)
		}
		if message.kind != inboundRequest || message.output != "" {
			t.Fatalf("classification for id=%s = (%d, %q), want request", rawID, message.kind, message.output)
		}
		if message.requestID.RawJSON() != rawID {
			t.Fatalf("raw ID = %q, want %q", message.requestID.RawJSON(), rawID)
		}
		if len(message.requestID.SemanticKey().encoded) == 0 || uint64(len(message.requestID.SemanticKey().encoded)) > config.UsedIDKeyMaxBytes {
			t.Fatalf("semantic key length = %d", len(message.requestID.SemanticKey().encoded))
		}
		for index := range raw {
			raw[index] = 'x'
		}
		if message.requestID.RawJSON() != rawID {
			t.Fatalf("caller mutation changed raw ID to %q", message.requestID.RawJSON())
		}
	}
}

func TestRequestIDSemanticEqualityUsesDecodedStringsAndMathematicalIntegers(t *testing.T) {
	for _, group := range [][]string{
		{`"a"`, `"\u0061"`},
		{`0`, `-0`, `0.0`, `0e-999`},
		{`1e3`, `1000.0`, `1E+03`, `100000e-2`},
	} {
		var want SemanticIDKey
		for index, rawID := range group {
			message, err := classifyInbound([]byte(`{"jsonrpc":"2.0","id":` + rawID + `,"method":"ping"}`))
			if err != nil || message.kind != inboundRequest {
				t.Fatalf("classifyInbound(id=%s) = (%d, %v)", rawID, message.kind, err)
			}
			if index == 0 {
				want = message.requestID.SemanticKey()
			} else if message.requestID.SemanticKey() != want {
				t.Fatalf("semantic key for %s differs inside group", rawID)
			}
		}
	}

	stringID, err := classifyInbound([]byte(`{"jsonrpc":"2.0","id":"1000","method":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	numberID, err := classifyInbound([]byte(`{"jsonrpc":"2.0","id":1000,"method":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	if stringID.requestID.SemanticKey() == numberID.requestID.SemanticKey() {
		t.Fatal("string and number request IDs share one semantic key")
	}
}

func TestClassifyInboundRejectsInvalidRequestIDsWithNullResponseID(t *testing.T) {
	tooLongString := `"` + strings.Repeat("x", int(config.RequestIDMaxRawBytes)-1) + `"`
	tooLongNumber := strings.Repeat("9", int(config.RequestIDMaxRawBytes)+1)
	for _, rawID := range []string{
		`1.5`,
		`1.50`,
		`15e-1`,
		`1e-1`,
		`null`,
		`true`,
		`false`,
		`{}`,
		`[]`,
		tooLongString,
		tooLongNumber,
	} {
		message, err := classifyInbound([]byte(`{"jsonrpc":"2.0","id":` + rawID + `,"method":"ping"}`))
		if err != nil {
			t.Fatalf("classifyInbound(id length %d) error = %v", len(rawID), err)
		}
		if message.kind != inboundInvalidRequest || message.output != wantInvalidRequestResponse {
			t.Fatalf("classification for id=%s = (%d, %q), want invalid request", rawID, message.kind, message.output)
		}
		if message.requestID.RawJSON() != "" || message.requestID.SemanticKey() != (SemanticIDKey{}) {
			t.Fatalf("invalid ID retained state: %#v", message.requestID)
		}
	}
}

func TestMissingRequestIDIsNotificationButExplicitNullIsInvalid(t *testing.T) {
	notification, err := classifyInbound([]byte(`{"jsonrpc":"2.0","method":"ping"}`))
	if err != nil || notification.kind != inboundNotification {
		t.Fatalf("missing ID classification = (%d, %v), want notification", notification.kind, err)
	}
	explicitNull, err := classifyInbound([]byte(`{"jsonrpc":"2.0","id":null,"method":"ping"}`))
	if err != nil || explicitNull.kind != inboundInvalidRequest || explicitNull.output != wantInvalidRequestResponse {
		t.Fatalf("null ID classification = (%d, %q, %v), want invalid request", explicitNull.kind, explicitNull.output, err)
	}
}
