package mcpstdio

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/config"
	workruntime "github.com/Dirard/mcp-file-tools/internal/runtime"
)

const (
	fuzzConnectionMaxInputBytes  = 64 << 10
	fuzzConnectionMaxFrames      = 256
	fuzzConnectionMaxRequests    = 64
	fuzzConnectionMaxOutputBytes = 2 << 20
)

type statelessFuzzExecutor struct{}

func (statelessFuzzExecutor) Call(_ context.Context, call api.Call, work *workruntime.WorkLease) workruntime.Execution {
	defer work.WorkerReturned()
	if call.Name() == api.ToolSetCWD {
		return workruntime.Execution{Kind: workruntime.ExecutionOrdinary, Result: api.SetCWD(1)}
	}
	return workruntime.Execution{Kind: workruntime.ExecutionOrdinary, Result: api.Navigation("DATA\tfuzz\n", false)}
}

func (statelessFuzzExecutor) Close() {}

func FuzzConnection(f *testing.F) {
	seeds := [][]byte{
		nil,
		[]byte("{\n"),
		[]byte("{}\n"),
		[]byte(`{"jsonrpc":"2.0","id":1,"result":{}}` + "\n"),
		[]byte(initializeRequest(`1`, "2025-11-25", `{}`) + "\n"),
		[]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"),
		[]byte(`{"jsonrpc":"2.0","id":2,"method":"ping"}` + "\n"),
		[]byte(`{"jsonrpc":"2.0","id":3,"method":"tools/list"}` + "\n"),
		[]byte(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"read","arguments":{"path":"x"}}}` + "\n"),
		[]byte(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"read"}}` + "\n"),
		[]byte(`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"unknown"}}` + "\n"),
		[]byte(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":4}}` + "\n"),
		[]byte("\xff\n"),
		[]byte("{}\r\n"),
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > fuzzConnectionMaxInputBytes || bytes.Count(input, []byte{'\n'}) > fuzzConnectionMaxFrames {
			return
		}

		firstOutput, firstErr, firstRegistry := runFuzzConnection(input)
		secondOutput, secondErr, secondRegistry := runFuzzConnection(input)
		if !bytes.Equal(firstOutput, secondOutput) || errorString(firstErr) != errorString(secondErr) {
			t.Fatalf("nondeterministic connection\n first output: %q\nsecond output: %q\nfirst error: %v\nsecond error: %v", firstOutput, secondOutput, firstErr, secondErr)
		}
		assertFuzzConnectionBounds(t, firstOutput, firstRegistry)
		assertFuzzConnectionBounds(t, secondOutput, secondRegistry)
		assertResponseLinesValid(t, firstOutput)

		replayedOutput, replayErr, replayRegistry := runFuzzConnection(firstOutput)
		if replayErr != nil {
			t.Fatalf("replaying response frames returned error: %v; responses: %q", replayErr, firstOutput)
		}
		if len(replayedOutput) != 0 {
			t.Fatalf("response frames generated another response: %q -> %q", firstOutput, replayedOutput)
		}
		assertFuzzConnectionBounds(t, replayedOutput, replayRegistry)
	})
}

func runFuzzConnection(input []byte) ([]byte, error, *usedIDRegistry) {
	registry := newUsedIDRegistryWithConfig(usedIDRegistryConfig{
		maxRequests:   fuzzConnectionMaxRequests,
		tableSlots:    fuzzConnectionMaxRequests * 2,
		arenaMaxBytes: fuzzConnectionMaxRequests * int(config.UsedIDKeyMaxBytes),
	}, defaultSemanticIDDigest)
	var output bytes.Buffer
	fatal := workruntime.NewFatalSignal()
	limits := testCallLimits()
	connection := stdioConnection{
		executor:     statelessFuzzExecutor{},
		coordinator:  workruntime.NewCoordinatorWithFatal(limits, fatal),
		fatal:        fatal,
		frames:       newFrameReader(bytes.NewReader(input)),
		lifecycle:    newLifecycle(),
		usedIDs:      registry,
		output:       &output,
		protocolBusy: newProtocolBusyQueue(),
		toolOutputs:  mustNewToolOutputLimiter(limits),
		toolRequests: make(map[SemanticIDKey]*toolRequest),
	}
	err := connection.serve(context.Background())
	return append([]byte(nil), output.Bytes()...), err, registry
}

func assertFuzzConnectionBounds(t *testing.T, output []byte, registry *usedIDRegistry) {
	t.Helper()
	if registry.count > fuzzConnectionMaxRequests || len(registry.slots) != fuzzConnectionMaxRequests*2 {
		t.Fatalf("request registry bounds changed: count=%d slots=%d", registry.count, len(registry.slots))
	}
	if len(registry.arena) > fuzzConnectionMaxRequests*int(config.UsedIDKeyMaxBytes) {
		t.Fatalf("request registry arena = %d bytes", len(registry.arena))
	}
	if len(output) > fuzzConnectionMaxOutputBytes {
		t.Fatalf("connection output = %d bytes, limit %d", len(output), fuzzConnectionMaxOutputBytes)
	}
}

func assertResponseLinesValid(t *testing.T, output []byte) {
	t.Helper()
	if len(output) == 0 {
		return
	}
	if output[len(output)-1] != '\n' {
		t.Fatalf("response output lacks final LF: %q", output)
	}
	for _, line := range bytes.Split(output[:len(output)-1], []byte{'\n'}) {
		if len(line) == 0 || !json.Valid(line) {
			t.Fatalf("invalid JSON response line: %q", line)
		}
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
