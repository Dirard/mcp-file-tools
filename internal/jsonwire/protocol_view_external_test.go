package jsonwire_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/jsonwire"
)

func TestProtocolViewExportsDeferredArgumentsWithoutChangingAdmission(t *testing.T) {
	limits := externalProtocolLimits()
	for _, test := range []struct {
		name          string
		arguments     string
		wantErrorKind jsonwire.ErrorKind
	}{
		{
			name:          "duplicate key",
			arguments:     `{"x":1,"x":2}`,
			wantErrorKind: jsonwire.KindDuplicate,
		},
		{
			name:          "isolated surrogate",
			arguments:     `{"value":"\uD800"}`,
			wantErrorKind: jsonwire.KindUnicode,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			wantFrame := `{"jsonrpc":"2.0","params":{"name":"read_file","arguments":` + test.arguments + `,"_meta":{"progressToken":"p"},"task":{"ttl":60000}},"method":"tools/call"}`
			input := []byte(wantFrame)
			view, err := jsonwire.ScanProtocolObject(input, limits)
			if err != nil {
				t.Fatalf("ScanProtocolObject() error = %v", err)
			}
			for index := range input {
				input[index] = 'x'
			}

			method, ok := view.Root().Value("method")
			if !ok || string(method.Bytes()) != `"tools/call"` {
				t.Fatalf("root method = (%q, %t)", method.Bytes(), ok)
			}

			params, ok := view.Params()
			if !ok {
				t.Fatal("Params() did not expose the protocol params object")
			}
			for _, name := range []string{"name", "arguments", "_meta", "task"} {
				if _, ok := params.Member(name); !ok {
					t.Fatalf("params member %q is missing", name)
				}
			}
			name, ok := params.Value("name")
			if !ok || string(name.Bytes()) != `"read_file"` {
				t.Fatalf("params name = (%q, %t)", name.Bytes(), ok)
			}

			arguments, ok := view.Arguments()
			if !ok {
				t.Fatal("Arguments() did not expose tools/call arguments")
			}
			if got := string(arguments.Bytes()); got != test.arguments {
				t.Fatalf("arguments bytes = %q, want %q", got, test.arguments)
			}
			paramsArguments, ok := params.Value("arguments")
			if !ok || paramsArguments.Span() != arguments.Span() {
				t.Fatalf("params arguments span = (%#v, %t), protocol span = %#v", paramsArguments.Span(), ok, arguments.Span())
			}

			err = arguments.Validate(limits, jsonwire.ToolArguments)
			var validationError *jsonwire.ValidationError
			if !errors.As(err, &validationError) || validationError.Kind() != test.wantErrorKind {
				t.Fatalf("ToolArguments validation error = %v, want %q", err, test.wantErrorKind)
			}
		})
	}
}

func TestProtocolViewRecoversParamsScopedErrorsBeforeRequestHeaders(t *testing.T) {
	limits := externalProtocolLimits()
	for _, test := range []struct {
		name      string
		params    string
		wantKind  jsonwire.ErrorKind
		configure func(*jsonwire.Limits)
	}{
		{
			name:     "duplicate",
			params:   `{"name":"read","arguments":{},"_meta":{"x":1,"x":2}}`,
			wantKind: jsonwire.KindDuplicate,
		},
		{
			name:     "escaped surrogate",
			params:   `{"name":"read","arguments":{},"_meta":{"value":"\uD800"}}`,
			wantKind: jsonwire.KindUnicode,
		},
		{
			name:     "depth cap",
			params:   `{"nested":{"deeper":{}}}`,
			wantKind: jsonwire.KindResource,
			configure: func(limits *jsonwire.Limits) {
				limits.MaxDepth = 3
			},
		},
		{
			name:     "object member cap",
			params:   `{"a":0,"b":0,"c":0,"d":0,"e":0}`,
			wantKind: jsonwire.KindResource,
			configure: func(limits *jsonwire.Limits) {
				limits.MaxObjectMembers = 4
			},
		},
		{
			name:     "container item cap",
			params:   `{"a":0,"b":0,"c":0}`,
			wantKind: jsonwire.KindResource,
			configure: func(limits *jsonwire.Limits) {
				// Four envelope members fit; the third params member makes
				// the complete frame exceed the order-independent total cap.
				limits.MaxContainerItems = 6
			},
		},
		{
			name:     "key byte cap",
			params:   `{"abcdefgh":0}`,
			wantKind: jsonwire.KindResource,
			configure: func(limits *jsonwire.Limits) {
				limits.MaxKeyBytes = 7
			},
		},
		{
			name:     "string byte cap",
			params:   `{"value":"12345678901"}`,
			wantKind: jsonwire.KindResource,
			configure: func(limits *jsonwire.Limits) {
				limits.MaxStringBytes = 10
			},
		},
		{
			name:     "number byte cap",
			params:   `{"value":1234}`,
			wantKind: jsonwire.KindResource,
			configure: func(limits *jsonwire.Limits) {
				limits.MaxNumberRawBytes = 3
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			caseLimits := limits
			if test.configure != nil {
				test.configure(&caseLimits)
			}
			frame := `{"params":` + test.params + `,"id":7,"method":"tools/call","jsonrpc":"2.0"}`
			input := []byte(frame)
			view, err := jsonwire.ScanProtocolObject(input, caseLimits)
			var validationError *jsonwire.ValidationError
			if !errors.As(err, &validationError) {
				t.Fatalf("ScanProtocolObject() error = %v, want *ValidationError", err)
			}
			if validationError.Kind() != test.wantKind || validationError.Scope() != jsonwire.ScopeProtocolParams {
				t.Fatalf("validation error = (%q, %q), want (%q, %q)", validationError.Kind(), validationError.Scope(), test.wantKind, jsonwire.ScopeProtocolParams)
			}
			for index := range input {
				input[index] = 'x'
			}

			id, ok := view.Root().Value("id")
			if !ok || string(id.Bytes()) != `7` {
				t.Fatalf("recovered id = (%q, %t)", id.Bytes(), ok)
			}
			method, ok := view.Root().Value("method")
			if !ok || string(method.Bytes()) != `"tools/call"` {
				t.Fatalf("recovered method = (%q, %t)", method.Bytes(), ok)
			}
			params, ok := view.ParamsValue()
			if !ok || string(params.Bytes()) != test.params {
				t.Fatalf("recovered params = (%q, %t), want %q", params.Bytes(), ok, test.params)
			}
		})
	}
}

func TestProtocolViewKeepsStructuralErrorsAtDocumentScope(t *testing.T) {
	raw := []byte(`{"params":{"x":[}},"id":7,"method":"tools/call","jsonrpc":"2.0"}`)
	view, err := jsonwire.ScanProtocolObject(raw, externalProtocolLimits())
	var validationError *jsonwire.ValidationError
	if !errors.As(err, &validationError) || validationError.Scope() != jsonwire.ScopeDocument {
		t.Fatalf("ScanProtocolObject() = (%#v, %v), want document-scoped error", view, err)
	}
	if _, ok := view.Root().Member("id"); ok {
		t.Fatal("structurally invalid frame exposed a recovered request id")
	}
}

func TestProtocolViewUsesDocumentEnvelopeParamsPriorityIndependentOfMemberOrder(t *testing.T) {
	for _, raw := range []string{
		`{"params":{"_meta":{"x":1,"x":2}},"id":7,"method":"\uD800","jsonrpc":"2.0"}`,
		`{"method":"\uD800","jsonrpc":"2.0","params":{"_meta":{"x":1,"x":2}},"id":7}`,
	} {
		_, err := jsonwire.ScanProtocolObject([]byte(raw), externalProtocolLimits())
		var validationError *jsonwire.ValidationError
		if !errors.As(err, &validationError) || validationError.Kind() != jsonwire.KindUnicode || validationError.Scope() != jsonwire.ScopeProtocolEnvelope {
			t.Fatalf("ScanProtocolObject(%s) error = %v, want envelope unicode", raw, err)
		}
	}

	view, err := jsonwire.ScanProtocolObject(
		[]byte(`{"x":1,"x":2,"params":[}}`),
		externalProtocolLimits(),
	)
	var validationError *jsonwire.ValidationError
	if !errors.As(err, &validationError) || validationError.Kind() != jsonwire.KindSyntax || validationError.Scope() != jsonwire.ScopeDocument {
		t.Fatalf("multi-defect document error = %v, want document syntax", err)
	}
	if len(view.Root().Members()) != 0 {
		t.Fatalf("document error exposed root members: %#v", view.Root().Members())
	}
}

func TestProtocolViewAppliesDepthCapToTopLevelNonObject(t *testing.T) {
	limits := externalProtocolLimits()
	limits.MaxDepth = 3
	_, err := jsonwire.ScanProtocolObject([]byte(`[[[[]]]]`), limits)
	var validationError *jsonwire.ValidationError
	if !errors.As(err, &validationError) || validationError.Kind() != jsonwire.KindResource || validationError.Scope() != jsonwire.ScopeProtocolEnvelope {
		t.Fatalf("top-level depth error = %v, want envelope resource", err)
	}
}

func TestProtocolViewDocumentErrorsOverrideEarlierRecoverableErrors(t *testing.T) {
	limits := externalProtocolLimits()
	limits.MaxDepth = 3
	inputs := [][]byte{
		[]byte(`{"params":[[[[1,}}]]],"id":7,"method":"tools/call","jsonrpc":"2.0"}`),
		append(
			append([]byte(`{"params":{"x":1,"x":2},"id":7,"method":"`), 0xff),
			[]byte(`","jsonrpc":"2.0"}`)...,
		),
	}
	for _, raw := range inputs {
		view, err := jsonwire.ScanProtocolObject(raw, limits)
		var validationError *jsonwire.ValidationError
		if !errors.As(err, &validationError) || validationError.Scope() != jsonwire.ScopeDocument {
			t.Fatalf("ScanProtocolObject(%q) error = %v, want document scope", raw, err)
		}
		if len(view.Root().Members()) != 0 {
			t.Fatalf("document error exposed root members: %#v", view.Root().Members())
		}
	}
}

func TestProtocolViewRecoversEscapedHeadersAfterEnvelopeError(t *testing.T) {
	raw := []byte(`{"x":1,"x":2,"\u0069d":7,"meth\u006fd":"tools/call","params":{}}`)
	view, err := jsonwire.ScanProtocolObject(raw, externalProtocolLimits())
	var validationError *jsonwire.ValidationError
	if !errors.As(err, &validationError) || validationError.Kind() != jsonwire.KindDuplicate || validationError.Scope() != jsonwire.ScopeProtocolEnvelope {
		t.Fatalf("ScanProtocolObject() error = %v, want envelope duplicate", err)
	}
	id, ok := view.Root().Value("id")
	if !ok || string(id.Bytes()) != "7" {
		t.Fatalf("recovered escaped id = (%q, %t)", id.Bytes(), ok)
	}
	method, ok := view.Root().Value("method")
	if !ok || string(method.Bytes()) != `"tools/call"` {
		t.Fatalf("recovered escaped method = (%q, %t)", method.Bytes(), ok)
	}
}

func TestProtocolViewRecoversResponseShapeAfterEnvelopeError(t *testing.T) {
	for _, key := range []string{"result", "error"} {
		for _, beforeError := range []bool{false, true} {
			t.Run(key+map[bool]string{false: " after error", true: " before error"}[beforeError], func(t *testing.T) {
				member := `"` + key + `":{"marker":1}`
				if !beforeError {
					member = `"\u` + map[string]string{"result": "0072", "error": "0065"}[key] + key[1:] + `":{"marker":1}`
				}
				parts := []string{`"x":1`, `"x":2`, member}
				if beforeError {
					parts = []string{member, `"x":1`, `"x":2`}
				}
				raw := []byte(`{` + strings.Join(parts, ",") + `}`)
				view, err := jsonwire.ScanProtocolObject(raw, externalProtocolLimits())
				var validationError *jsonwire.ValidationError
				if !errors.As(err, &validationError) || validationError.Kind() != jsonwire.KindDuplicate || validationError.Scope() != jsonwire.ScopeProtocolEnvelope {
					t.Fatalf("ScanProtocolObject() error = %v, want envelope duplicate", err)
				}
				value, ok := view.Root().Value(key)
				if !ok || string(value.Bytes()) != `{"marker":1}` {
					t.Fatalf("recovered %s = (%q, %t)", key, value.Bytes(), ok)
				}
			})
		}
	}
}

func TestProtocolViewRetainsEachRecoveryHeaderOnlyOnce(t *testing.T) {
	const repeats = 8_192
	var raw strings.Builder
	raw.Grow(repeats*7 + 64)
	raw.WriteString(`{"x":1,"x":2`)
	for range repeats {
		raw.WriteString(`,"id":0`)
	}
	raw.WriteString(`,"result":{}}`)

	view, err := jsonwire.ScanProtocolObject([]byte(raw.String()), externalProtocolLimits())
	var validationError *jsonwire.ValidationError
	if !errors.As(err, &validationError) || validationError.Kind() != jsonwire.KindDuplicate || validationError.Scope() != jsonwire.ScopeProtocolEnvelope {
		t.Fatalf("ScanProtocolObject() error = %v, want envelope duplicate", err)
	}
	members := view.Root().Members()
	if len(members) != 3 {
		t.Fatalf("recovered root member count = %d, want 3", len(members))
	}
	for _, name := range []string{"x", "id", "result"} {
		if _, ok := view.Root().Member(name); !ok {
			t.Fatalf("recovered root is missing %q", name)
		}
	}
}

func TestProtocolViewAssignsTotalItemOverflowIndependentOfMemberOrder(t *testing.T) {
	limits := externalProtocolLimits()
	limits.MaxContainerItems = 6
	for _, test := range []struct {
		raw       string
		wantScope jsonwire.ValidationScope
	}{
		{
			raw:       `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"x":0},"ext":[0]}`,
			wantScope: jsonwire.ScopeProtocolParams,
		},
		{
			raw:       `{"jsonrpc":"2.0","id":1,"method":"ping","ext":[0],"params":{"x":0}}`,
			wantScope: jsonwire.ScopeProtocolParams,
		},
		{
			raw:       `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"x":0},"ext":[0,1]}`,
			wantScope: jsonwire.ScopeProtocolEnvelope,
		},
		{
			raw:       `{"jsonrpc":"2.0","id":1,"method":"ping","ext":[0,1],"params":{"x":0}}`,
			wantScope: jsonwire.ScopeProtocolEnvelope,
		},
	} {
		view, err := jsonwire.ScanProtocolObject([]byte(test.raw), limits)
		var validationError *jsonwire.ValidationError
		if !errors.As(err, &validationError) || validationError.Kind() != jsonwire.KindResource || validationError.Scope() != test.wantScope {
			t.Fatalf("ScanProtocolObject(%s) error = %v, want %s resource", test.raw, err, test.wantScope)
		}
		id, ok := view.Root().Value("id")
		if !ok || string(id.Bytes()) != "1" {
			t.Fatalf("recovered id = (%q, %t)", id.Bytes(), ok)
		}
	}
}

func TestGenericProtocolModeKeepsValueScope(t *testing.T) {
	_, err := jsonwire.ScanObject(
		[]byte(`{"params":{"arguments":{"x":1,"x":2}},"method":"extension/run"}`),
		externalProtocolLimits(),
		jsonwire.ProtocolWithRawArguments,
	)
	var validationError *jsonwire.ValidationError
	if !errors.As(err, &validationError) || validationError.Kind() != jsonwire.KindDuplicate || validationError.Scope() != jsonwire.ScopeValue {
		t.Fatalf("ScanObject() error = %v, want value-scoped duplicate", err)
	}
}

func externalProtocolLimits() jsonwire.Limits {
	return jsonwire.Limits{
		MaxDepth:          64,
		MaxObjectMembers:  4_096,
		MaxContainerItems: 65_536,
		MaxKeyBytes:       4_096,
		MaxStringBytes:    262_144,
		MaxNumberRawBytes: 256,
	}
}
