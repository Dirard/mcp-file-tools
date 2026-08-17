package mcpstdio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/config"
	workruntime "github.com/Dirard/mcp-file-tools/internal/runtime"
)

type fakeExecutorFactory struct {
	executor CallExecutor
	newCalls int
}

func (factory *fakeExecutorFactory) NewConnection() (CallExecutor, error) {
	factory.newCalls++
	return factory.executor, nil
}

type recordedExecutorCall struct {
	name      api.ToolName
	arguments []byte
	context   any
	work      *workruntime.WorkLease
}

type fakeCallExecutor struct {
	mu         sync.Mutex
	calls      []recordedExecutorCall
	closed     int
	contextKey any
}

func (executor *fakeCallExecutor) Call(ctx context.Context, call api.Call, work *workruntime.WorkLease) workruntime.Execution {
	defer work.WorkerReturned()
	arguments := call.Arguments()
	var contextValue any
	if executor.contextKey != nil {
		contextValue = ctx.Value(executor.contextKey)
	}
	executor.mu.Lock()
	executor.calls = append(executor.calls, recordedExecutorCall{
		name:      call.Name(),
		arguments: arguments,
		context:   contextValue,
		work:      work,
	})
	executor.mu.Unlock()
	if bytes.Equal(arguments, []byte(`{}`)) {
		return workruntime.Execution{Kind: workruntime.ExecutionOrdinary, Result: api.Navigation("ERROR\tinvalid_input\n", true)}
	}
	return workruntime.Execution{Kind: workruntime.ExecutionOrdinary, Result: api.Navigation("DATA\tok\n", false)}
}

func (executor *fakeCallExecutor) Close() {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	executor.closed++
}

func testCallLimits() workruntime.Limits {
	return workruntime.Limits{MaxConcurrent: 8, QueueMax: 64, QueueTimeout: time.Second}
}

func newTestServer(factory GateFactory) *Server {
	return NewServer(testCallLimits(), factory)
}

func TestServerDoesNotReadAheadWhileCurrentFrameIsLive(t *testing.T) {
	executor := &fakeCallExecutor{}
	server := newTestServer(&fakeExecutorFactory{executor: executor})
	var sequence atomic.Uint64
	input := newFrameReadAheadProbe(&sequence,
		[]byte(initializeRequest(`"init"`, "2025-11-25", `{}`)+"\n"),
		[]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"),
	)
	output := newFrameOrderingWriter(&sequence)
	t.Cleanup(output.release)

	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.Serve(context.Background(), input, output)
	}()
	awaitMCPStdIOSignal(t, output.entered, "first frame response write")

	select {
	case <-input.secondRead:
	case <-time.After(100 * time.Millisecond):
	}
	output.release()
	if err := awaitServeResult(t, serveDone); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}

	if got, want := output.order.Load(), input.secondOrder.Load(); got == 0 || want == 0 || got >= want {
		t.Fatalf("frame release/read order = %d/%d, want release before next frame read", got, want)
	}
}

func TestServerDispatchesValidatedToolCallsThroughConnectionExecutor(t *testing.T) {
	type contextKey struct{}
	key := contextKey{}
	executor := &fakeCallExecutor{contextKey: key}
	factory := &fakeExecutorFactory{executor: executor}
	server := newTestServer(factory)
	ctx := context.WithValue(context.Background(), key, "same-context")

	output := &flushTrackingBuffer{}
	input := &flushSequencedReader{
		lines: [][]byte{
			[]byte(initializeRequest(`"init"`, "2025-11-25", `{}`) + "\n"),
			[]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"),
			[]byte(`{"jsonrpc":"2.0","id":"outer-a","method":"tools/call","params":{"name":"read","arguments":{"path":"x","escaped":"\u0061"}}}` + "\n"),
			[]byte(`{"jsonrpc":"2.0","id":1e3,"method":"tools/call","params":{"name":"read","arguments":{"path":"x","escaped":"\u0061"}}}` + "\n"),
			[]byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"project"}}` + "\n"),
			[]byte(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"unknown","arguments":{}}}` + "\n"),
		},
		expectedFlushes: []int{0, 1, 1, 2, 3, 4, 5},
		output:          output,
	}
	if err := server.Serve(ctx, input, output); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}

	wantOutput := `{"jsonrpc":"2.0","id":"init","result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"mcp-file-tools","version":"dev"},"instructions":"Code mode: max_output_tokens=10000; emit content[0].text; set_cwd also mirrors cwd_id in structuredContent; never stringify CallToolResult."}}` + "\n" +
		`{"jsonrpc":"2.0","id":"outer-a","result":{"content":[{"type":"text","text":"DATA\tok\n"}]}}` + "\n" +
		`{"jsonrpc":"2.0","id":1e3,"result":{"content":[{"type":"text","text":"DATA\tok\n"}]}}` + "\n" +
		`{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"ERROR\tinvalid_input\n"}],"isError":true}}` + "\n" +
		`{"jsonrpc":"2.0","id":4,"error":{"code":-32602,"message":"invalid params"}}` + "\n"
	if output.String() != wantOutput {
		t.Fatalf("Serve() output differs\n got: %s\nwant: %s", output.String(), wantOutput)
	}

	if factory.newCalls != 1 || executor.closed != 1 {
		t.Fatalf("factory calls / executor closes = %d / %d, want 1 / 1", factory.newCalls, executor.closed)
	}
	if len(executor.calls) != 3 {
		t.Fatalf("executor call count = %d, want 3: %#v", len(executor.calls), executor.calls)
	}
	wantArguments := `{"path":"x","escaped":"\u0061"}`
	for index := 0; index < 2; index++ {
		call := executor.calls[index]
		if call.name != api.ToolRead || string(call.arguments) != wantArguments || call.context != "same-context" {
			t.Fatalf("executor call %d = %#v", index, call)
		}
	}
	missingArguments := executor.calls[2]
	if missingArguments.name != api.ToolProject || string(missingArguments.arguments) != `{}` || missingArguments.context != "same-context" {
		t.Fatalf("omitted arguments did not reach executor as empty object: %#v", missingArguments)
	}
}

func TestServerRegistersRequestIDBeforeParamsValidationAndExecutorDispatch(t *testing.T) {
	executor := &fakeCallExecutor{}
	server := newTestServer(&fakeExecutorFactory{executor: executor})
	output := &flushTrackingBuffer{}
	input := &flushSequencedReader{
		lines: [][]byte{
			[]byte(initializeRequest(`1`, "2025-11-25", `{}`) + "\n"),
			[]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"),
			[]byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"unknown"}}` + "\n"),
			[]byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"read","arguments":{"path":"would-dispatch"}}}` + "\n"),
		},
		expectedFlushes: []int{0, 1, 1, 2, 3},
		output:          output,
	}
	if err := server.Serve(context.Background(), input, output); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if len(executor.calls) != 0 {
		t.Fatalf("executor received calls after downstream rejection and ID reuse: %#v", executor.calls)
	}

	wantSuffix := `{"jsonrpc":"2.0","id":7,"error":{"code":-32602,"message":"invalid params"}}` + "\n" +
		`{"jsonrpc":"2.0","id":7,"error":{"code":-32600,"message":"invalid request"}}` + "\n"
	if !bytes.HasSuffix(output.Bytes(), []byte(wantSuffix)) {
		t.Fatalf("ID precedence suffix differs\n got: %s\nwant suffix: %s", output.Bytes(), wantSuffix)
	}
}

type notificationFloodReader struct {
	notificationCount uint64
	nextLine          uint64
	lineOffset        int
	baselineHeap      uint64
	maxRetainedGrowth uint64
}

var notificationFloodLines = [...][]byte{
	[]byte(`{"jsonrpc":"2.0","method":"extension/unknown"}` + "\n"),
	[]byte(`{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":"p","progress":1}}` + "\n"),
	[]byte(`{"jsonrpc":"2.0","method":"notifications/roots/list_changed"}` + "\n"),
	[]byte(`{"jsonrpc":"2.0","method":"tools/list"}` + "\n"),
	[]byte(`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"read","arguments":{"path":"ignored"}}}` + "\n"),
}

func (reader *notificationFloodReader) Read(destination []byte) (int, error) {
	if reader.nextLine > reader.notificationCount {
		return 0, io.EOF
	}
	if reader.lineOffset == 0 && reader.nextLine%10_000 == 0 {
		reader.sampleRetainedHeap()
	}

	var line []byte
	if reader.nextLine == reader.notificationCount {
		line = []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n")
	} else {
		line = notificationFloodLines[reader.nextLine%uint64(len(notificationFloodLines))]
	}
	written := copy(destination, line[reader.lineOffset:])
	reader.lineOffset += written
	if reader.lineOffset == len(line) {
		reader.nextLine++
		reader.lineOffset = 0
	}
	return written, nil
}

func (reader *notificationFloodReader) sampleRetainedHeap() {
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	if reader.baselineHeap == 0 {
		reader.baselineHeap = stats.HeapAlloc
		return
	}
	if stats.HeapAlloc > reader.baselineHeap {
		growth := stats.HeapAlloc - reader.baselineHeap
		if growth > reader.maxRetainedGrowth {
			reader.maxRetainedGrowth = growth
		}
	}
}

func TestServerDropsNotificationFloodWithoutAccumulatingState(t *testing.T) {
	const notificationCount = 100_000
	reader := &notificationFloodReader{notificationCount: notificationCount}
	executor := &fakeCallExecutor{}
	var output bytes.Buffer
	if err := newTestServer(&fakeExecutorFactory{executor: executor}).Serve(context.Background(), reader, &output); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}

	want := `{"jsonrpc":"2.0","id":1,"result":{}}` + "\n"
	if output.String() != want {
		t.Fatalf("notification flood output = %q, want final ping only %q", output.String(), want)
	}
	if len(executor.calls) != 0 {
		t.Fatalf("notification flood reached executor: calls=%d", len(executor.calls))
	}
	if executor.closed != 1 {
		t.Fatalf("executor close count = %d, want 1", executor.closed)
	}
	if reader.nextLine != notificationCount+1 {
		t.Fatalf("consumed lines = %d, want %d", reader.nextLine, notificationCount+1)
	}
	if reader.maxRetainedGrowth > 8<<20 {
		t.Fatalf("retained heap grew by %d bytes across notification flood, limit %d", reader.maxRetainedGrowth, 8<<20)
	}
}

func TestServerSilentlyDropsCancellationWithoutMatchingToolRequest(t *testing.T) {
	executor := &fakeCallExecutor{}
	input := bytes.NewBufferString(
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1000.0,"reason":"st\u006fp"}}` + "\n" +
			`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1e3,"reason":"repeat"}}` + "\n" +
			`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"unknown"}}` + "\n" +
			`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"reason":"missing id"}}` + "\n" +
			`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":null}}` + "\n" +
			`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n",
	)
	var output bytes.Buffer
	if err := newTestServer(&fakeExecutorFactory{executor: executor}).Serve(context.Background(), input, &output); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}

	if output.String() != `{"jsonrpc":"2.0","id":1,"result":{}}`+"\n" {
		t.Fatalf("cancellation sequence output = %q", output.String())
	}
	if len(executor.calls) != 0 || executor.closed != 1 {
		t.Fatalf("executor calls/closed = %d/%d, want 0/1", len(executor.calls), executor.closed)
	}
}

type flushTrackingBuffer struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	flushes int
	changed chan struct{}
}

func (buffer *flushTrackingBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(data)
}

func (buffer *flushTrackingBuffer) Flush() error {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	buffer.flushes++
	if buffer.changed != nil {
		close(buffer.changed)
	}
	buffer.changed = make(chan struct{})
	return nil
}

func (buffer *flushTrackingBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

func (buffer *flushTrackingBuffer) Bytes() []byte {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return append([]byte(nil), buffer.buffer.Bytes()...)
}

func (buffer *flushTrackingBuffer) flushCount() int {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.flushes
}

func (buffer *flushTrackingBuffer) waitForFlushes(want int) error {
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	for {
		buffer.mu.Lock()
		if buffer.flushes >= want {
			buffer.mu.Unlock()
			return nil
		}
		if buffer.changed == nil {
			buffer.changed = make(chan struct{})
		}
		changed := buffer.changed
		buffer.mu.Unlock()
		select {
		case <-changed:
		case <-timeout.C:
			return fmt.Errorf("timed out waiting for %d response flushes", want)
		}
	}
}

type flushSequencedReader struct {
	lines           [][]byte
	nextLine        int
	lineOffset      int
	output          *flushTrackingBuffer
	expectedFlushes []int
}

type frameReadAheadProbe struct {
	sequence    *atomic.Uint64
	lines       [][]byte
	nextLine    int
	lineOffset  int
	secondRead  chan struct{}
	secondOnce  sync.Once
	secondOrder atomic.Uint64
}

func newFrameReadAheadProbe(sequence *atomic.Uint64, lines ...[]byte) *frameReadAheadProbe {
	return &frameReadAheadProbe{
		sequence:   sequence,
		lines:      lines,
		secondRead: make(chan struct{}),
	}
}

func (reader *frameReadAheadProbe) Read(destination []byte) (int, error) {
	if reader.nextLine == len(reader.lines) {
		return 0, io.EOF
	}
	if reader.nextLine == 1 && reader.lineOffset == 0 {
		reader.secondOnce.Do(func() {
			reader.secondOrder.Store(reader.sequence.Add(1))
			close(reader.secondRead)
		})
	}
	line := reader.lines[reader.nextLine]
	written := copy(destination, line[reader.lineOffset:])
	reader.lineOffset += written
	if reader.lineOffset == len(line) {
		reader.nextLine++
		reader.lineOffset = 0
	}
	return written, nil
}

type frameOrderingWriter struct {
	sequence *atomic.Uint64
	entered  chan struct{}
	releaseC chan struct{}
	once     sync.Once
	released sync.Once
	order    atomic.Uint64
}

func newFrameOrderingWriter(sequence *atomic.Uint64) *frameOrderingWriter {
	return &frameOrderingWriter{
		sequence: sequence,
		entered:  make(chan struct{}),
		releaseC: make(chan struct{}),
	}
}

func (writer *frameOrderingWriter) Write(response []byte) (int, error) {
	writer.once.Do(func() { close(writer.entered) })
	<-writer.releaseC
	writer.order.Store(writer.sequence.Add(1))
	return len(response), nil
}

func (writer *frameOrderingWriter) release() {
	writer.released.Do(func() { close(writer.releaseC) })
}

func (reader *flushSequencedReader) Read(destination []byte) (int, error) {
	if reader.lineOffset == 0 {
		expected := reader.nextLine
		if reader.nextLine < len(reader.expectedFlushes) {
			expected = reader.expectedFlushes[reader.nextLine]
		}
		if err := reader.output.waitForFlushes(expected); err != nil {
			return 0, fmt.Errorf("before input line %d: %w", reader.nextLine, err)
		}
	}
	if reader.nextLine == len(reader.lines) {
		return 0, io.EOF
	}
	line := reader.lines[reader.nextLine]
	written := copy(destination, line[reader.lineOffset:])
	reader.lineOffset += written
	if reader.lineOffset == len(line) {
		reader.nextLine++
		reader.lineOffset = 0
	}
	return written, nil
}

func TestServerContinuesAfterEveryRecoverableProtocolError(t *testing.T) {
	tests := []struct {
		name          string
		badFrame      string
		errorResponse string
	}{
		{
			name:          "parse error",
			badFrame:      `{"jsonrpc":"2.0","id":1,"method":"ping"`,
			errorResponse: `{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"parse error"}}` + "\n",
		},
		{
			name:          "invalid request",
			badFrame:      `{"jsonrpc":"1.0","id":1,"method":"ping"}`,
			errorResponse: `{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"invalid request"}}` + "\n",
		},
		{
			name:          "invalid params",
			badFrame:      `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"cursor":"not-supported"}}`,
			errorResponse: `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"invalid params"}}` + "\n",
		},
		{
			name:          "method not found",
			badFrame:      `{"jsonrpc":"2.0","id":1,"method":"extension/unknown"}`,
			errorResponse: `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found"}}` + "\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := &flushTrackingBuffer{}
			reader := &flushSequencedReader{
				lines: [][]byte{
					[]byte(test.badFrame + "\n"),
					[]byte(`{"jsonrpc":"2.0","id":2,"method":"ping"}` + "\n"),
				},
				output: output,
			}
			executor := &fakeCallExecutor{}
			if err := newTestServer(&fakeExecutorFactory{executor: executor}).Serve(context.Background(), reader, output); err != nil {
				t.Fatalf("Serve() error = %v", err)
			}

			want := test.errorResponse + `{"jsonrpc":"2.0","id":2,"result":{}}` + "\n"
			if output.String() != want {
				t.Fatalf("response sequence differs\n got: %s\nwant: %s", output.String(), want)
			}
			if output.flushCount() != 2 {
				t.Fatalf("flush count = %d, want 2", output.flushCount())
			}
			if len(executor.calls) != 0 || executor.closed != 1 {
				t.Fatalf("executor state calls/closed = %d/%d", len(executor.calls), executor.closed)
			}
		})
	}
}

func TestServerClosesOversizedFrameWithoutResponse(t *testing.T) {
	frame := bytes.Repeat([]byte{'x'}, int(config.StdioFrameMaxBytes)+1)
	frame = append(frame, '\n')
	frame = append(frame, []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n")...)
	executor := &fakeCallExecutor{}
	var output bytes.Buffer
	err := newTestServer(&fakeExecutorFactory{executor: executor}).Serve(context.Background(), bytes.NewReader(frame), &output)
	if !errors.Is(err, errFrameTooLarge) {
		t.Fatalf("Serve() error = %v, want errFrameTooLarge", err)
	}
	if output.Len() != 0 {
		t.Fatalf("oversized frame produced output: %q", output.Bytes())
	}
	if len(executor.calls) != 0 || executor.closed != 1 {
		t.Fatalf("executor state calls/closed = %d/%d", len(executor.calls), executor.closed)
	}
}
