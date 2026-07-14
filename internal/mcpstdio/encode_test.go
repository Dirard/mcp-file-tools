package mcpstdio

import (
	"bytes"
	"os"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/catalog"
)

func TestCanonicalResponsesMatchRawWireGolden(t *testing.T) {
	responses := make([][]byte, 0, 15)
	responses = append(responses,
		encodeProtocolErrorNull(protocolParseError),
		encodeProtocolErrorNull(protocolInvalidRequest),
		encodeProtocolError(mustResponseID(t, `1e3`), protocolInvalidRequest),
		encodeProtocolError(mustResponseID(t, `"\u003c&>"`), protocolMethodNotFound),
		encodeProtocolError(mustResponseID(t, `4`), protocolInvalidParams),
		encodeProtocolError(mustResponseID(t, `5`), protocolServerBusy),
		encodeProtocolError(mustResponseID(t, `6`), protocolSessionRequestLimit),
	)

	initialize, err := encodeLifecycleDecision(lifecycleDecision{
		action:    lifecycleInitialize,
		requestID: mustResponseID(t, `"init"`),
		initialize: initializeResult{
			protocolVersion: supportedProtocolVersion,
			serverName:      serverImplementationName,
			serverVersion:   serverImplementationDev,
			instructions:    catalog.Instructions,
			tools:           true,
		},
	})
	if err != nil {
		t.Fatalf("encode initialize: %v", err)
	}
	ping, err := encodeLifecycleDecision(lifecycleDecision{
		action:    lifecyclePing,
		requestID: mustResponseID(t, `8`),
	})
	if err != nil {
		t.Fatalf("encode ping: %v", err)
	}
	responses = append(responses, initialize, ping)

	toolResults := []struct {
		id     string
		result api.Result
	}{
		{id: `9`, result: api.SetCWD(7)},
		{id: `10`, result: api.Navigation("PROJECT\trows=1\nDATA\t<tag>&\"slash\\\t \n", false)},
		{id: `11`, result: api.Navigation("ERROR\tinvalid_input\n", true)},
		{id: `12`, result: api.Navigation("SEARCH\tstatus=partial\tcursor=abc\nDATA\tone\n", false)},
		{id: `13`, result: api.Navigation("READ\tstatus=ok\titems=2\nITEM\t0\tok\nERROR\titem=1\tunreadable\n", false)},
		{id: `14`, result: api.Navigation("READ\tstatus=ok\titems=2\nERROR\titem=0\tunreadable\nERROR\titem=1\tunreadable\n", true)},
	}
	for _, test := range toolResults {
		encoded, encodeErr := encodeToolResult(mustResponseID(t, test.id), test.result)
		if encodeErr != nil {
			t.Fatalf("encode tool result %s: %v", test.id, encodeErr)
		}
		responses = append(responses, encoded)
	}

	got := bytes.Join(responses, nil)
	want, err := os.ReadFile("testdata/responses.golden")
	if err != nil {
		t.Fatalf("read response golden: %v\nactual bytes:\n%s", err, got)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("canonical response bytes differ from golden\n got: %s\nwant: %s", got, want)
	}
}

func TestToolsListLifecycleResponseReusesCatalogGolden(t *testing.T) {
	got, err := encodeLifecycleDecision(lifecycleDecision{
		action:    lifecycleToolsList,
		requestID: mustResponseID(t, `2`),
	})
	if err != nil {
		t.Fatalf("encode tools/list: %v", err)
	}
	want, err := os.ReadFile("testdata/tools-list.golden")
	if err != nil {
		t.Fatalf("read tools/list golden: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("tools/list response differs from golden\n got: %s\nwant: %s", got, want)
	}
}

func TestToolResultHasOneExactPayloadChannel(t *testing.T) {
	navigation, err := encodeToolResult(mustResponseID(t, `1`), api.Navigation("DATA\tvalue\n", false))
	if err != nil {
		t.Fatal(err)
	}
	wantNavigation := []byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"DATA\tvalue\n"}]}}` + "\n")
	if !bytes.Equal(navigation, wantNavigation) {
		t.Fatalf("navigation response = %s, want %s", navigation, wantNavigation)
	}
	for _, forbidden := range [][]byte{
		[]byte(`"annotations"`),
		[]byte(`"_meta"`),
		[]byte(`"structuredContent"`),
	} {
		if bytes.Contains(navigation, forbidden) {
			t.Fatalf("navigation response contains %s: %s", forbidden, navigation)
		}
	}

	cwd, err := encodeToolResult(mustResponseID(t, `2`), api.SetCWD(3))
	if err != nil {
		t.Fatal(err)
	}
	wantCWD := []byte(`{"jsonrpc":"2.0","id":2,"result":{"content":[],"structuredContent":{"cwd_id":3}}}` + "\n")
	if !bytes.Equal(cwd, wantCWD) {
		t.Fatalf("cwd response = %s, want %s", cwd, wantCWD)
	}
	if bytes.Contains(cwd, []byte(`"text"`)) || bytes.Contains(cwd, []byte(`"isError"`)) {
		t.Fatalf("cwd response contains a second payload channel: %s", cwd)
	}
}

func TestEncoderPreservesRawIDAndNeverHTMLEscapes(t *testing.T) {
	id := mustResponseID(t, `"\u003craw&>"`)
	result := api.Navigation("DATA\t<tag>&value\n", false)

	t.Setenv("MCPGODEBUG", "jsonescaping=1")
	withSetting, err := encodeToolResult(id, result)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MCPGODEBUG", "malformed-value")
	withMalformed, err := encodeToolResult(id, result)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(withSetting, withMalformed) {
		t.Fatalf("MCPGODEBUG changed wire bytes:\n%s\n%s", withSetting, withMalformed)
	}
	if !bytes.Contains(withSetting, []byte(`"id":"\u003craw&>"`)) {
		t.Fatalf("raw request id spelling was not preserved: %s", withSetting)
	}
	if !bytes.Contains(withSetting, []byte(`<tag>&value`)) || bytes.Contains(withSetting, []byte(`\u003c`+"tag")) || bytes.Contains(withSetting, []byte(`\u0026value`)) {
		t.Fatalf("tool text was HTML-escaped: %s", withSetting)
	}
}

func TestEncoderRejectsImpossibleToolResults(t *testing.T) {
	tests := []api.Result{
		{},
		api.SetCWD(0),
		api.Navigation("missing final LF", false),
	}
	for _, result := range tests {
		got, err := encodeToolResult(mustResponseID(t, `1`), result)
		if err == nil {
			t.Fatalf("encodeToolResult(%#v) unexpectedly succeeded: %s", result, got)
		}
		if len(got) != 0 {
			t.Fatalf("invalid result produced partial wire bytes: %q", got)
		}
	}
}

func mustResponseID(t *testing.T, raw string) RequestID {
	t.Helper()
	message, err := classifyInbound([]byte(`{"jsonrpc":"2.0","id":` + raw + `,"method":"ping"}`))
	if err != nil {
		t.Fatalf("classify request id %s: %v", raw, err)
	}
	if message.kind != inboundRequest {
		t.Fatalf("request id %s classified as %d, output %q", raw, message.kind, message.output)
	}
	return message.requestID
}
