package mcpstdio

import (
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/jsonwire"
)

const (
	wantParseErrorResponse     = `{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"parse error"}}` + "\n"
	wantInvalidRequestResponse = `{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"invalid request"}}` + "\n"
)

func TestClassifyInboundReturnsExactParseError(t *testing.T) {
	invalidUTF8 := append([]byte(`{"jsonrpc":"2.0","method":"`), 0xff)
	invalidUTF8 = append(invalidUTF8, []byte(`"}`)...)
	for _, raw := range [][]byte{
		invalidUTF8,
		[]byte(`{"jsonrpc":"2.0","method":`),
		[]byte(`{"jsonrpc":"2.0","method":"ping"} trailing`),
	} {
		message, err := classifyInbound(raw)
		if err != nil {
			t.Fatalf("classifyInbound() error = %v", err)
		}
		if message.kind != inboundParseError || message.output != wantParseErrorResponse {
			t.Fatalf("classification = (%d, %q), want parse error", message.kind, message.output)
		}
	}
}

func TestClassifyInboundReturnsExactInvalidRequest(t *testing.T) {
	for _, raw := range []string{
		`null`,
		`[]`,
		`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`,
		`{}`,
		`{"method":"ping"}`,
		`{"jsonrpc":"1.0","method":"ping"}`,
		`{"jsonrpc":"2.0","method":1}`,
		`{"jsonrpc":"2.0","method":"ping","x":1,"x":2}`,
		`{"jsonrpc":"2.0","method":null,"result":{}}`,
	} {
		message, err := classifyInbound([]byte(raw))
		if err != nil {
			t.Fatalf("classifyInbound(%s) error = %v", raw, err)
		}
		if message.kind != inboundInvalidRequest || message.output != wantInvalidRequestResponse {
			t.Fatalf("classification for %s = (%d, %q), want invalid request", raw, message.kind, message.output)
		}
	}
}

func TestClassifyInboundDropsResponseShapesBeforeSemanticValidation(t *testing.T) {
	for _, raw := range []string{
		`{"jsonrpc":"2.0","id":1,"result":{}}`,
		`{"jsonrpc":"2.0","id":1,"error":{"code":-1}}`,
		`{"jsonrpc":1,"id":{},"result":null,"error":false}`,
		`{"x":1,"x":2,"\u0072esult":{}}`,
	} {
		message, err := classifyInbound([]byte(raw))
		if err != nil {
			t.Fatalf("classifyInbound(%s) error = %v", raw, err)
		}
		if message.kind != inboundResponse || message.output != "" {
			t.Fatalf("classification for %s = (%d, %q), want silent response drop", raw, message.kind, message.output)
		}
		if len(message.protocol.Root().Members()) != 0 || message.validationError != nil {
			t.Fatalf("response shape retained dispatch state: %#v", message)
		}
	}
}

func TestClassifyInboundSeparatesRequestsAndNotifications(t *testing.T) {
	for _, test := range []struct {
		name           string
		raw            string
		wantKind       inboundKind
		wantParamError bool
	}{
		{
			name:     "request",
			raw:      `{"jsonrpc":"2.0","id":1,"method":"ping"}`,
			wantKind: inboundRequest,
		},
		{
			name:     "notification",
			raw:      `{"jsonrpc":"2.0","method":"notifications/initialized"}`,
			wantKind: inboundNotification,
		},
		{
			name:     "escaped protocol strings",
			raw:      `{"jsonrpc":"\u0032.0","id":"a","method":"p\u0069ng"}`,
			wantKind: inboundRequest,
		},
		{
			name:           "request with deferred params error",
			raw:            `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"x":1,"x":2}}`,
			wantKind:       inboundRequest,
			wantParamError: true,
		},
		{
			name:           "notification with deferred params error",
			raw:            `{"jsonrpc":"2.0","method":"ping","params":{"x":1,"x":2}}`,
			wantKind:       inboundNotification,
			wantParamError: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			message, err := classifyInbound([]byte(test.raw))
			if err != nil {
				t.Fatalf("classifyInbound() error = %v", err)
			}
			if message.kind != test.wantKind || message.output != "" {
				t.Fatalf("classification = (%d, %q), want (%d, empty)", message.kind, message.output, test.wantKind)
			}
			if _, ok := message.protocol.Root().Member("method"); !ok {
				t.Fatal("dispatchable message lost its immutable protocol view")
			}
			if test.wantParamError {
				if message.validationError == nil || message.validationError.Scope() != jsonwire.ScopeProtocolParams {
					t.Fatalf("validation error = %v, want params scope", message.validationError)
				}
			} else if message.validationError != nil {
				t.Fatalf("unexpected validation error: %v", message.validationError)
			}
		})
	}
}
