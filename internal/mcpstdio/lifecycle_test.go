package mcpstdio

import (
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/catalog"
)

func TestLifecycleNegotiatesOnlyMCP20251125(t *testing.T) {
	for _, requested := range []string{
		"2025-11-25",
		"2025-06-18",
		"2025-03-26",
		"2024-11-05",
		"2099-01-01",
		"unknown-future",
	} {
		t.Run(requested, func(t *testing.T) {
			lifecycle := newLifecycle()
			decision := lifecycle.handle(mustLifecycleSchema(t, initializeRequest(`1`, requested, `{}`)))
			if decision.action != lifecycleInitialize {
				t.Fatalf("initialize action = %d; decision = %#v", decision.action, decision)
			}
			want := initializeResult{
				protocolVersion: supportedProtocolVersion,
				serverName:      "mcp-file-tools",
				serverVersion:   "dev",
				instructions:    catalog.Instructions,
				tools:           true,
			}
			if decision.initialize != want {
				t.Fatalf("initialize result = %#v, want %#v", decision.initialize, want)
			}
			if decision.requestID.RawJSON() != `1` || lifecycle.state != lifecycleAwaitingInitialized {
				t.Fatalf("initialize state/id = (%d, %q)", lifecycle.state, decision.requestID.RawJSON())
			}
		})
	}
}

func TestLifecycleRejectsRepeatInitializeAndKnownToolsBeforeReady(t *testing.T) {
	tests := []struct {
		name  string
		state lifecycleState
		raw   string
		id    string
	}{
		{name: "list before initialize", state: lifecycleNew, raw: `{"jsonrpc":"2.0","id":"list-new","method":"tools/list"}`, id: `"list-new"`},
		{name: "call before initialize", state: lifecycleNew, raw: `{"jsonrpc":"2.0","id":"call-new","method":"tools/call","params":{"name":"read"}}`, id: `"call-new"`},
		{name: "list awaiting initialized", state: lifecycleAwaitingInitialized, raw: `{"jsonrpc":"2.0","id":"list-wait","method":"tools/list"}`, id: `"list-wait"`},
		{name: "call awaiting initialized", state: lifecycleAwaitingInitialized, raw: `{"jsonrpc":"2.0","id":"call-wait","method":"tools/call","params":{"name":"read"}}`, id: `"call-wait"`},
		{name: "repeat initialize awaiting", state: lifecycleAwaitingInitialized, raw: initializeRequest(`"repeat-wait"`, "2025-11-25", `{}`), id: `"repeat-wait"`},
		{name: "repeat initialize ready", state: lifecycleReady, raw: initializeRequest(`"repeat-ready"`, "2025-11-25", `{}`), id: `"repeat-ready"`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lifecycle := &connectionLifecycle{state: test.state}
			decision := lifecycle.handle(mustLifecycleSchema(t, test.raw))
			want := `{"jsonrpc":"2.0","id":` + test.id + `,"error":{"code":-32600,"message":"invalid request"}}` + "\n"
			if decision.action != lifecycleRejected || decision.output != want {
				t.Fatalf("decision = %#v, want output %q", decision, want)
			}
			if lifecycle.state != test.state {
				t.Fatalf("state changed from %d to %d", test.state, lifecycle.state)
			}
		})
	}
}

func TestLifecycleMalformedParamsPrecedeStateErrors(t *testing.T) {
	lifecycle := &connectionLifecycle{state: lifecycleReady}
	tests := []struct {
		raw string
		id  string
	}{
		{raw: `{"jsonrpc":"2.0","id":1,"method":"initialize","params":null}`, id: `1`},
		{raw: `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{"cursor":"never-issued"}}`, id: `2`},
		{raw: `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{}}`, id: `3`},
	}
	for _, test := range tests {
		decision := lifecycle.handle(mustLifecycleSchema(t, test.raw))
		want := `{"jsonrpc":"2.0","id":` + test.id + `,"error":{"code":-32602,"message":"invalid params"}}` + "\n"
		if decision.action != lifecycleRejected || decision.output != want {
			t.Fatalf("decision for %s = %#v, want output %q", test.raw, decision, want)
		}
		if lifecycle.state != lifecycleReady {
			t.Fatalf("malformed request changed state to %d", lifecycle.state)
		}
	}
}

func TestLifecycleInitializedNotificationTransitionsExactlyOnce(t *testing.T) {
	lifecycle := newLifecycle()

	for _, raw := range []string{
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized","params":null}`,
	} {
		decision := lifecycle.handle(mustLifecycleSchema(t, raw))
		if decision.action != lifecycleDrop || decision.output != "" || lifecycle.state != lifecycleNew {
			t.Fatalf("early initialized decision/state = (%#v, %d)", decision, lifecycle.state)
		}
	}

	initialize := lifecycle.handle(mustLifecycleSchema(t, initializeRequest(`1`, "2025-11-25", `{}`)))
	if initialize.action != lifecycleInitialize || lifecycle.state != lifecycleAwaitingInitialized {
		t.Fatalf("initialize decision/state = (%#v, %d)", initialize, lifecycle.state)
	}

	malformed := lifecycle.handle(mustLifecycleSchema(t, `{"jsonrpc":"2.0","method":"notifications/initialized","params":{"_meta":[]}}`))
	if malformed.action != lifecycleDrop || malformed.output != "" || lifecycle.state != lifecycleAwaitingInitialized {
		t.Fatalf("malformed initialized decision/state = (%#v, %d)", malformed, lifecycle.state)
	}

	valid := lifecycle.handle(mustLifecycleSchema(t, `{"jsonrpc":"2.0","method":"notifications/initialized","params":{"扩展":true}}`))
	if valid.action != lifecycleDrop || valid.output != "" || lifecycle.state != lifecycleReady {
		t.Fatalf("valid initialized decision/state = (%#v, %d)", valid, lifecycle.state)
	}

	duplicate := lifecycle.handle(mustLifecycleSchema(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	if duplicate.action != lifecycleDrop || duplicate.output != "" || lifecycle.state != lifecycleReady {
		t.Fatalf("duplicate initialized decision/state = (%#v, %d)", duplicate, lifecycle.state)
	}
}

func TestLifecycleAllowsPingInEveryStateAndToolsOnlyWhenReady(t *testing.T) {
	for _, state := range []lifecycleState{lifecycleNew, lifecycleAwaitingInitialized, lifecycleReady} {
		lifecycle := &connectionLifecycle{state: state}
		decision := lifecycle.handle(mustLifecycleSchema(t, `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"扩展":true}}`))
		if decision.action != lifecyclePing || decision.output != "" || lifecycle.state != state {
			t.Fatalf("ping in state %d = %#v; final state %d", state, decision, lifecycle.state)
		}
	}

	lifecycle := &connectionLifecycle{state: lifecycleReady}
	list := lifecycle.handle(mustLifecycleSchema(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	if list.action != lifecycleToolsList || list.requestID.RawJSON() != `2` {
		t.Fatalf("ready list decision = %#v", list)
	}
	call := lifecycle.handle(mustLifecycleSchema(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"read","arguments":{"path":"x"}}}`))
	if call.action != lifecycleToolsCall || call.requestID.RawJSON() != `3` || string(call.call.Arguments()) != `{"path":"x"}` {
		t.Fatalf("ready call decision = %#v, arguments = %s", call, call.call.Arguments())
	}
}

func TestLifecycleRoutesUnknownRequestsAndDropsUnknownNotifications(t *testing.T) {
	lifecycle := newLifecycle()
	request := lifecycle.handle(mustLifecycleSchema(t, `{"jsonrpc":"2.0","id":"u","method":"extension/unknown"}`))
	if request.action != lifecycleMethodNotFound || request.requestID.RawJSON() != `"u"` || request.output != "" {
		t.Fatalf("unknown request decision = %#v", request)
	}
	notification := lifecycle.handle(mustLifecycleSchema(t, `{"jsonrpc":"2.0","method":"extension/unknown","params":{"x":1}}`))
	if notification.action != lifecycleDrop || notification.output != "" {
		t.Fatalf("unknown notification decision = %#v", notification)
	}
}

func mustLifecycleSchema(t *testing.T, raw string) inboundSchemaResult {
	t.Helper()
	message, err := classifyInbound([]byte(raw))
	if err != nil {
		t.Fatalf("classifyInbound(%s) error = %v", raw, err)
	}
	if message.kind != inboundRequest && message.kind != inboundNotification {
		t.Fatalf("classifyInbound(%s) = kind %d output %q", raw, message.kind, message.output)
	}
	return validateInboundSchema(message)
}

func initializeRequest(id, version, capabilities string) string {
	return `{"jsonrpc":"2.0","id":` + id + `,"method":"initialize","params":{"protocolVersion":"` + version + `","capabilities":` + capabilities + `,"clientInfo":{"name":"test","version":"1"}}}`
}
