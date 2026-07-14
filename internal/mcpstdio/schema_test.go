package mcpstdio

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

func TestValidateInboundSchemaAcceptsSixKnownOpenShapes(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		method  string
		request bool
		check   func(*testing.T, inboundSchemaResult)
	}{
		{
			name: "initialize",
			raw: `{"jsonrpc":"2.0","id":"init","method":"initialize","外部":{"值":true},"params":{` +
				`"protocolVersion":"2025-11-25",` +
				`"capabilities":{` +
				`"experimental":{"实验":{"enabled":true}},` +
				`"roots":{"listChanged":true,"扩展":{"值":1}},` +
				`"sampling":{"context":{},"tools":{},"扩展":[]},` +
				`"elicitation":{"form":{},"url":{}},` +
				`"tasks":{"list":{},"cancel":{},"requests":{"sampling":{"createMessage":{}},"elicitation":{"create":{}}}},` +
				`"能力扩展":{"深":[1,2,3]}},` +
				`"clientInfo":{` +
				`"name":"клиент","title":"Клиент","version":"1.0",` +
				`"description":"описание","websiteUrl":"https://example.test",` +
				`"icons":[{"src":"data:image/png;base64,AA==","mimeType":"image/png","sizes":["48x48","any"],"theme":"dark","扩展":{}}],` +
				`"客户端扩展":{"值":"✓"}},` +
				`"_meta":{"com.example/trace":{"ключ":"значение"}},` +
				`"параметр":{"值":true}}}`,
			method:  "initialize",
			request: true,
			check: func(t *testing.T, result inboundSchemaResult) {
				if result.protocolVersion != "2025-11-25" {
					t.Fatalf("protocol version = %q", result.protocolVersion)
				}
			},
		},
		{
			name:    "ping",
			raw:     `{"jsonrpc":"2.0","id":2,"method":"ping","外部":[]}`,
			method:  "ping",
			request: true,
		},
		{
			name:    "tools list",
			raw:     `{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{"_meta":{"trace":"✓"},"扩展":[{"值":1}]}}`,
			method:  "tools/list",
			request: true,
		},
		{
			name:    "tools call",
			raw:     `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"read","task":{"ttl":60000,"扩展":true},"_meta":{},"扩展":"✓"}}`,
			method:  "tools/call",
			request: true,
			check: func(t *testing.T, result inboundSchemaResult) {
				if result.call.Name() != api.ToolRead || string(result.call.Arguments()) != `{}` {
					t.Fatalf("call = (%q, %s)", result.call.Name(), result.call.Arguments())
				}
			},
		},
		{
			name:   "initialized notification",
			raw:    `{"jsonrpc":"2.0","method":"notifications/initialized","params":{"_meta":{},"扩展":{"值":1}}}`,
			method: "notifications/initialized",
		},
		{
			name:   "cancelled notification",
			raw:    `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"cance\u006cled","reason":"причина","_meta":{},"扩展":true}}`,
			method: "notifications/cancelled",
			check: func(t *testing.T, result inboundSchemaResult) {
				if result.cancellationID.encoded != "scancelled" || result.cancellationReason != "причина" {
					t.Fatalf("cancellation = (%q, %q)", result.cancellationID.encoded, result.cancellationReason)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := mustValidateInboundSchema(t, test.raw)
			if result.kind != inboundSchemaAccepted || result.method != test.method || result.output != "" {
				t.Fatalf("schema result = %#v", result)
			}
			if test.request && result.requestID.RawJSON() == "" {
				t.Fatal("accepted request lost its request ID")
			}
			if test.check != nil {
				test.check(t, result)
			}
		})
	}
}

func TestValidateInboundSchemaRejectsMalformedKnownRequestFields(t *testing.T) {
	tests := []struct {
		name   string
		method string
		params string
	}{
		{name: "initialize params absent", method: "initialize"},
		{name: "initialize params null", method: "initialize", params: `,"params":null`},
		{name: "initialize version absent", method: "initialize", params: `,"params":{"capabilities":{},"clientInfo":{"name":"c","version":"1"}}`},
		{name: "initialize version kind", method: "initialize", params: `,"params":{"protocolVersion":1,"capabilities":{},"clientInfo":{"name":"c","version":"1"}}`},
		{name: "initialize capabilities kind", method: "initialize", params: `,"params":{"protocolVersion":"2025-11-25","capabilities":[],"clientInfo":{"name":"c","version":"1"}}`},
		{name: "roots known field kind", method: "initialize", params: `,"params":{"protocolVersion":"2025-11-25","capabilities":{"roots":{"listChanged":"yes"}},"clientInfo":{"name":"c","version":"1"}}`},
		{name: "experimental value kind", method: "initialize", params: `,"params":{"protocolVersion":"2025-11-25","capabilities":{"experimental":{"bad":[]}},"clientInfo":{"name":"c","version":"1"}}`},
		{name: "nested tasks kind", method: "initialize", params: `,"params":{"protocolVersion":"2025-11-25","capabilities":{"tasks":{"requests":{"sampling":{"createMessage":false}}}},"clientInfo":{"name":"c","version":"1"}}`},
		{name: "client name absent", method: "initialize", params: `,"params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"version":"1"}}`},
		{name: "client version kind", method: "initialize", params: `,"params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"c","version":1}}`},
		{name: "client icons kind", method: "initialize", params: `,"params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"c","version":"1","icons":{}}}`},
		{name: "icon src absent", method: "initialize", params: `,"params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"c","version":"1","icons":[{"theme":"dark"}]}}`},
		{name: "icon sizes item kind", method: "initialize", params: `,"params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"c","version":"1","icons":[{"src":"x","sizes":[1]}]}}`},
		{name: "icon theme value", method: "initialize", params: `,"params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"c","version":"1","icons":[{"src":"x","theme":"sepia"}]}}`},
		{name: "ping params kind", method: "ping", params: `,"params":[]`},
		{name: "ping meta kind", method: "ping", params: `,"params":{"_meta":[]}`},
		{name: "ping meta key", method: "ping", params: `,"params":{"_meta":{"/bad":1}}`},
		{name: "ping meta duplicate", method: "ping", params: `,"params":{"_meta":{"trace":1,"trace":2}}`},
		{name: "ping meta surrogate", method: "ping", params: `,"params":{"_meta":{"trace":"\uD800"}}`},
		{name: "list cursor", method: "tools/list", params: `,"params":{"cursor":"unused"}`},
		{name: "call params absent", method: "tools/call"},
		{name: "call name absent", method: "tools/call", params: `,"params":{}`},
		{name: "call name kind", method: "tools/call", params: `,"params":{"name":1}`},
		{name: "call unknown name", method: "tools/call", params: `,"params":{"name":"unknown"}`},
		{name: "call arguments null", method: "tools/call", params: `,"params":{"name":"read","arguments":null}`},
		{name: "call arguments array", method: "tools/call", params: `,"params":{"name":"read","arguments":[]}`},
		{name: "call task kind", method: "tools/call", params: `,"params":{"name":"read","task":[]}`},
		{name: "call task ttl kind", method: "tools/call", params: `,"params":{"name":"read","task":{"ttl":"60000"}}`},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rawID := strconv.Itoa(index + 1)
			raw := `{"jsonrpc":"2.0","id":` + rawID + `,"method":"` + test.method + `"` + test.params + `}`
			result := mustValidateInboundSchema(t, raw)
			want := `{"jsonrpc":"2.0","id":` + rawID + `,"error":{"code":-32602,"message":"invalid params"}}` + "\n"
			if result.kind != inboundSchemaInvalid || result.output != want {
				t.Fatalf("schema result = %#v, want output %q", result, want)
			}
		})
	}
}

func TestValidateInboundSchemaSilentlyRejectsMalformedKnownNotifications(t *testing.T) {
	tests := []string{
		`{"jsonrpc":"2.0","method":"notifications/initialized","params":null}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized","params":{"_meta":{"progressToken":null}}}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled"}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1.5}}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1,"reason":false}}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1,"_meta":[]}}`,
	}

	for _, raw := range tests {
		result := mustValidateInboundSchema(t, raw)
		if result.kind != inboundSchemaInvalid || result.output != "" {
			t.Fatalf("schema result for %s = %#v", raw, result)
		}
	}
}

func TestValidateInboundSchemaDefersOnlyArgumentsInternals(t *testing.T) {
	tests := []struct {
		name      string
		arguments string
	}{
		{name: "duplicate key", arguments: `{"path":"a","path":"b"}`},
		{name: "isolated surrogate", arguments: `{"path":"\uD800"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read","arguments":` + test.arguments + `}}`
			result := mustValidateInboundSchema(t, raw)
			if result.kind != inboundSchemaAccepted || string(result.call.Arguments()) != test.arguments {
				t.Fatalf("schema result = %#v; arguments = %s", result, result.call.Arguments())
			}
		})
	}

	for _, task := range []string{`{"ttl":1,"ttl":2}`, `{"扩展":"\uD800"}`} {
		raw := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"read","task":` + task + `}}`
		result := mustValidateInboundSchema(t, raw)
		if result.kind != inboundSchemaInvalid {
			t.Fatalf("malformed task %s result = %#v", task, result)
		}
	}
}

func TestValidateInboundSchemaEnforcesProtocolStringCapsAtOpenExtensionLevels(t *testing.T) {
	atLimit := strings.Repeat("x", int(protocolJSONStringMaxBytes))
	overLimit := atLimit + "x"
	levels := []struct {
		name  string
		build func(string) string
	}{
		{
			name: "outer",
			build: func(value string) string {
				return `{"jsonrpc":"2.0","id":1,"method":"ping","扩展":"` + value + `"}`
			},
		},
		{
			name: "params",
			build: func(value string) string {
				return `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"扩展":"` + value + `"}}`
			},
		},
		{
			name: "capability",
			build: func(value string) string {
				return `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{"扩展":"` + value + `"},"clientInfo":{"name":"c","version":"1"}}}`
			},
		},
		{
			name: "client info",
			build: func(value string) string {
				return `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"c","version":"1","扩展":"` + value + `"}}}`
			},
		},
	}

	for _, level := range levels {
		t.Run(level.name+" exact", func(t *testing.T) {
			result := mustValidateInboundSchema(t, level.build(atLimit))
			if result.kind != inboundSchemaAccepted {
				t.Fatalf("exact cap result = %#v", result)
			}
		})
		t.Run(level.name+" over", func(t *testing.T) {
			message, err := classifyInbound([]byte(level.build(overLimit)))
			if err != nil {
				t.Fatalf("classify over-cap extension: %v", err)
			}
			if level.name == "outer" {
				if message.kind != inboundInvalidRequest || message.output != invalidRequestOutput {
					t.Fatalf("outer over-cap classification = %#v", message)
				}
				return
			}
			result := validateInboundSchema(message)
			if result.kind != inboundSchemaInvalid {
				t.Fatalf("params over-cap result = %#v", result)
			}
		})
	}
}

func TestValidateInboundSchemaEnforcesOtherOpenParamsCapBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		exact string
		over  string
	}{
		{
			name:  "key bytes",
			exact: `{"` + strings.Repeat("k", int(protocolJSONKeyMaxBytes)) + `":true}`,
			over:  `{"` + strings.Repeat("k", int(protocolJSONKeyMaxBytes)+1) + `":true}`,
		},
		{
			name:  "object members",
			exact: openExtensionObject(4_096),
			over:  openExtensionObject(4_097),
		},
		{
			name:  "depth",
			exact: `{"扩展":` + nestedSchemaArrays(62) + `}`,
			over:  `{"扩展":` + nestedSchemaArrays(63) + `}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name+" exact", func(t *testing.T) {
			raw := `{"jsonrpc":"2.0","id":1,"method":"ping","params":` + test.exact + `}`
			if result := mustValidateInboundSchema(t, raw); result.kind != inboundSchemaAccepted {
				t.Fatalf("exact cap result = %#v", result)
			}
		})
		t.Run(test.name+" over", func(t *testing.T) {
			raw := `{"jsonrpc":"2.0","id":1,"method":"ping","params":` + test.over + `}`
			if result := mustValidateInboundSchema(t, raw); result.kind != inboundSchemaInvalid {
				t.Fatalf("over-cap result = %#v", result)
			}
		})
	}
}

func TestValidateInboundSchemaEnforcesExactMetaRawBoundary(t *testing.T) {
	const metaRawLimit = 16_384
	exactMeta := sizedMetaObject(metaRawLimit)
	overMeta := sizedMetaObject(metaRawLimit + 1)
	for _, test := range []struct {
		name string
		meta string
		kind inboundSchemaKind
	}{
		{name: "exact", meta: exactMeta, kind: inboundSchemaAccepted},
		{name: "over", meta: overMeta, kind: inboundSchemaInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"_meta":` + test.meta + `}}`
			if result := mustValidateInboundSchema(t, raw); result.kind != test.kind {
				t.Fatalf("meta boundary result = %#v", result)
			}
		})
	}
}

func TestValidateInboundSchemaTreatsWrongMessageFormsAsUnknown(t *testing.T) {
	tests := []string{
		`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"read","arguments":{"path":"\uD800"}}}`,
		`{"jsonrpc":"2.0","id":1,"method":"notifications/cancelled","params":{"requestId":1}}`,
		`{"jsonrpc":"2.0","id":2,"method":"extension/unknown","params":{"扩展":true}}`,
	}
	for _, raw := range tests {
		result := mustValidateInboundSchema(t, raw)
		if result.kind != inboundSchemaUnknown || result.output != "" {
			t.Fatalf("schema result for %s = %#v", raw, result)
		}
	}
}

func mustValidateInboundSchema(t *testing.T, raw string) inboundSchemaResult {
	t.Helper()
	message, err := classifyInbound([]byte(raw))
	if err != nil {
		t.Fatalf("classifyInbound(%s) error = %v", raw, err)
	}
	if message.kind != inboundRequest && message.kind != inboundNotification {
		t.Fatalf("classifyInbound(%s) kind = %d, output = %q", raw, message.kind, message.output)
	}
	return validateInboundSchema(message)
}

func openExtensionObject(members int) string {
	var builder strings.Builder
	builder.WriteByte('{')
	for index := 0; index < members; index++ {
		if index != 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(`"x`)
		builder.WriteString(strconv.Itoa(index))
		builder.WriteString(`":null`)
	}
	builder.WriteByte('}')
	return builder.String()
}

func nestedSchemaArrays(depth int) string {
	return strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth)
}

func sizedMetaObject(size int) string {
	const fields = 4
	payload := size - (2 + fields*6 + fields - 1)
	var builder strings.Builder
	builder.Grow(size)
	builder.WriteByte('{')
	for index := 0; index < fields; index++ {
		if index != 0 {
			builder.WriteByte(',')
		}
		builder.WriteByte('"')
		builder.WriteByte(byte('a' + index))
		builder.WriteString(`":"`)
		length := payload / (fields - index)
		builder.WriteString(strings.Repeat("m", length))
		payload -= length
		builder.WriteByte('"')
	}
	builder.WriteByte('}')
	return builder.String()
}
