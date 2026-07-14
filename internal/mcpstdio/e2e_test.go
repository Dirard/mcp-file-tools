package mcpstdio

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
	workruntime "github.com/Dirard/mcp-file-tools/internal/runtime"
)

type scriptedExecutor struct {
	calls  []recordedExecutorCall
	closed int
}

func (executor *scriptedExecutor) Call(_ context.Context, call api.Call, work *workruntime.WorkLease) workruntime.Execution {
	executor.calls = append(executor.calls, recordedExecutorCall{
		name:      call.Name(),
		arguments: call.Arguments(),
		work:      work,
	})
	switch call.Name() {
	case api.ToolSetCWD:
		work.WorkerReturned()
		return workruntime.Execution{Kind: workruntime.ExecutionOrdinary, Result: api.SetCWD(7)}
	case api.ToolProject:
		work.WorkerReturned()
		return workruntime.Execution{Kind: workruntime.ExecutionOrdinary, Result: api.Navigation("PROJECT\tstatus=ok\n", false)}
	case api.ToolSearch:
		result := api.Navigation("SEARCH\tstatus=partial\tcursor=next\nDATA\tone\n", false)
		publication := newFakePublication(work, publicationTestConfig{result: result})
		if err := work.Transfer(publication); err != nil {
			work.WorkerReturned()
			panic("scripted cursor publication transfer failed")
		}
		return workruntime.Execution{Kind: workruntime.ExecutionInitialCursor, Result: result, Publication: publication}
	case api.ToolRead:
		work.WorkerReturned()
		return workruntime.Execution{Kind: workruntime.ExecutionOrdinary, Result: api.Navigation("READ\tstatus=ok\titems=2\nITEM\t0\tok\nERROR\titem=1\tunreadable\n", false)}
	default:
		panic("transport dispatched an unknown tool")
	}
}

func (executor *scriptedExecutor) Close() {
	executor.closed++
}

func TestScriptedConnectionMatchesDeterministicRawWireGolden(t *testing.T) {
	executor := &scriptedExecutor{}
	output := &flushTrackingBuffer{}
	input := &flushSequencedReader{
		lines: [][]byte{
			[]byte(initializeRequest(`"init"`, "2025-11-25", `{}`) + "\n"),
			[]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"),
			[]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"),
			[]byte(`{"jsonrpc":"2.0","id":3,"method":"ping"}` + "\n"),
			[]byte(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"set_cwd","arguments":{"directory":"/tmp/project"}}}` + "\n"),
			[]byte(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"project","arguments":{"cwd_id":7}}}` + "\n"),
			[]byte(`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"search","arguments":{"cwd_id":7,"query":"x"}}}` + "\n"),
			[]byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"read","arguments":{"cwd_id":7,"files":["x"]}}}` + "\n"),
		},
		expectedFlushes: []int{0, 1, 1, 2, 3, 4, 5, 6, 7},
		output:          output,
	}
	if err := newTestServer(&fakeExecutorFactory{executor: executor}).Serve(context.Background(), input, output); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}

	template, err := os.ReadFile("testdata/connection.golden")
	if err != nil {
		t.Fatalf("read connection golden: %v", err)
	}
	toolsList, err := os.ReadFile("testdata/tools-list.golden")
	if err != nil {
		t.Fatalf("read tools/list golden: %v", err)
	}
	marker := []byte("{{TOOLS_LIST}}\n")
	if bytes.Count(template, marker) != 1 {
		t.Fatalf("connection golden marker count = %d, want 1", bytes.Count(template, marker))
	}
	want := bytes.Replace(template, marker, toolsList, 1)
	if !bytes.Equal(output.Bytes(), want) {
		t.Fatalf("scripted connection differs from golden\n got: %s\nwant: %s", output.Bytes(), want)
	}
	if len(executor.calls) != 4 || executor.closed != 1 {
		t.Fatalf("executor calls/closed = %d/%d, want 4/1", len(executor.calls), executor.closed)
	}
	for index, name := range api.OrderedToolNames() {
		if executor.calls[index].name != name {
			t.Fatalf("executor call %d = %#v", index, executor.calls[index])
		}
	}
}
