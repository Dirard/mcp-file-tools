package mcpstdio

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/api"
	workruntime "github.com/Dirard/mcp-file-tools/internal/runtime"
)

func TestServerKeepsOneProtocolSlotAndCancellationResponsiveUnderToolSaturation(t *testing.T) {
	executor := newSaturationExecutor()
	writer := newProtocolBlockingWriter(`"id":10`)
	reader, input := io.Pipe()
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- NewServer(workruntime.Limits{
			MaxConcurrent: 1,
			QueueMax:      1,
			QueueTimeout:  time.Hour,
		}, &fakeExecutorFactory{executor: executor}).Serve(context.Background(), reader, writer)
	}()

	writeFrame(t, input, initializeRequest(`"init"`, supportedProtocolVersion, `{}`))
	writeFrame(t, input, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	awaitOutputContains(t, writer, `"id":"init"`)

	writeFrame(t, input, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read","arguments":{"path":"active"}}}`)
	awaitMCPStdIOSignal(t, executor.activeStarted, "active tool call")
	writeFrame(t, input, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"read","arguments":{"path":"queued"}}}`)
	writeFrame(t, input, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"read","arguments":{"path":"overflow"}}}`)
	awaitOutputContains(t, writer, `{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"ERROR\tbudget_exceeded\n"}],"isError":true}}`)

	writeFrame(t, input, `{"jsonrpc":"2.0","id":10,"method":"ping"}`)
	awaitMCPStdIOSignal(t, writer.blocked, "blocked protocol response")
	writeFrame(t, input, `{"jsonrpc":"2.0","id":11,"method":"tools/list"}`)
	writeFrame(t, input, `{"jsonrpc":"2.0","id":12,"method":"unknown/protocol"}`)
	writeFrame(t, input, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":2,"reason":"stop queued"}}`)
	writeFrame(t, input, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"read","arguments":{"path":"replacement"}}}`)

	close(writer.unblock)
	awaitOutputContains(t, writer, `{"jsonrpc":"2.0","id":10,"result":{}}`)
	awaitOutputContains(t, writer, `{"jsonrpc":"2.0","id":11,"error":{"code":-32000,"message":"server busy"}}`)
	awaitOutputContains(t, writer, `{"jsonrpc":"2.0","id":12,"error":{"code":-32000,"message":"server busy"}}`)

	executor.releaseCalls()
	awaitMCPStdIOSignal(t, executor.replacementStarted, "replacement tool call")
	awaitOutputContains(t, writer, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"DATA\tok\n"}]}}`)
	awaitOutputContains(t, writer, `{"jsonrpc":"2.0","id":4,"result":{"content":[{"type":"text","text":"DATA\tok\n"}]}}`)
	if err := input.Close(); err != nil {
		t.Fatalf("close input: %v", err)
	}
	select {
	case err := <-serveResult:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve() did not finish after tool workers returned")
	}

	if writer.contains(`"id":2`) {
		t.Fatal("queued cancellation emitted a response")
	}
	if executor.callCount() != 2 || executor.closeCount() != 1 {
		t.Fatalf("executor calls/closes = %d/%d, want 2/1", executor.callCount(), executor.closeCount())
	}
}

type saturationExecutor struct {
	mu                 sync.Mutex
	calls              int
	closed             int
	activeStarted      chan struct{}
	replacementStarted chan struct{}
	release            chan struct{}
	releaseOnce        sync.Once
}

func newSaturationExecutor() *saturationExecutor {
	return &saturationExecutor{
		activeStarted:      make(chan struct{}),
		replacementStarted: make(chan struct{}),
		release:            make(chan struct{}),
	}
}

func (executor *saturationExecutor) Call(_ context.Context, call api.Call, work *workruntime.WorkLease) workruntime.Execution {
	defer work.WorkerReturned()
	executor.mu.Lock()
	executor.calls++
	callNumber := executor.calls
	executor.mu.Unlock()
	switch callNumber {
	case 1:
		if !bytes.Contains(call.Arguments(), []byte(`"active"`)) {
			panic("first executor call is not active request")
		}
		close(executor.activeStarted)
	case 2:
		if !bytes.Contains(call.Arguments(), []byte(`"replacement"`)) {
			panic("cancelled queued request reached executor")
		}
		close(executor.replacementStarted)
	default:
		panic("unexpected tool call")
	}
	<-executor.release
	return workruntime.Execution{Kind: workruntime.ExecutionOrdinary, Result: api.Navigation("DATA\tok\n", false)}
}

func (executor *saturationExecutor) Close() {
	executor.mu.Lock()
	executor.closed++
	executor.mu.Unlock()
	executor.releaseCalls()
}

func (executor *saturationExecutor) releaseCalls() {
	executor.releaseOnce.Do(func() { close(executor.release) })
}

func (executor *saturationExecutor) callCount() int {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return executor.calls
}

func (executor *saturationExecutor) closeCount() int {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return executor.closed
}

type protocolBlockingWriter struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	needle   []byte
	blocked  chan struct{}
	unblock  chan struct{}
	blockOne sync.Once
}

func newProtocolBlockingWriter(needle string) *protocolBlockingWriter {
	return &protocolBlockingWriter{
		needle:  []byte(needle),
		blocked: make(chan struct{}),
		unblock: make(chan struct{}),
	}
}

func (writer *protocolBlockingWriter) Write(data []byte) (int, error) {
	if bytes.Contains(data, writer.needle) {
		writer.blockOne.Do(func() {
			close(writer.blocked)
			<-writer.unblock
		})
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.buffer.Write(data)
}

func (writer *protocolBlockingWriter) contains(needle string) bool {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return bytes.Contains(writer.buffer.Bytes(), []byte(needle))
}

func writeFrame(t *testing.T, writer *io.PipeWriter, frame string) {
	t.Helper()
	written := make(chan error, 1)
	go func() {
		_, err := io.WriteString(writer, frame+"\n")
		written <- err
	}()
	select {
	case err := <-written:
		if err != nil {
			t.Fatalf("write frame: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out writing frame")
	}
}

func awaitOutputContains(t *testing.T, writer *protocolBlockingWriter, needle string) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if writer.contains(needle) {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for output %q", needle)
		case <-ticker.C:
		}
	}
}

func awaitMCPStdIOSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}
