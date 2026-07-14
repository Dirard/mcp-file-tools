package mcpstdio

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/jsonwire"
	workruntime "github.com/Dirard/mcp-file-tools/internal/runtime"
)

func TestToolAdmissionPrecedesArgumentSemanticsAtFullCapacity(t *testing.T) {
	executor := newInstrumentedExecutor()
	instrumentation := &connectionInstrumentation{}
	var output bytes.Buffer
	connection := newAdmissionTestConnection(workruntime.Limits{
		MaxConcurrent: 1,
		QueueMax:      0,
		QueueTimeout:  time.Second,
	}, executor, instrumentation, &output)

	sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":100,"method":"tools/call","params":{"name":"read","arguments":{"path":"hold"}}}`)
	awaitMCPStdIOSignal(t, executor.firstBlocked, "first executor call")
	if got := connectionRequestCount(connection); got != 1 {
		t.Fatalf("active request count = %d, want 1", got)
	}

	sendConnectionFrame(t, connection, `{"jsonrpc":"1.0","id":1,"method":"ping"}`)
	sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"unknown","arguments":{}}}`)
	awaitProtocolIdle(t, connection)
	sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"read","task":{"ttl":"bad"},"arguments":{}}}`)
	awaitProtocolIdle(t, connection)
	sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"read","arguments":[]}}`)
	awaitProtocolIdle(t, connection)
	sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"read","arguments":{"path":"x","path":"y"}}}`)
	sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"read","arguments":{"path":"\uD800"}}}`)
	sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"read","arguments":{"path":"valid"}}}`)

	if got := instrumentation.admissionAttempts.Load(); got != 4 {
		t.Fatalf("admission attempts = %d, want 4", got)
	}
	if got := instrumentation.workerTasks.Load(); got != 1 {
		t.Fatalf("worker tasks = %d, want 1", got)
	}
	if got := instrumentation.workLeases.Load(); got != 1 {
		t.Fatalf("work leases = %d, want 1", got)
	}
	if calls, validations := executor.counts(); calls != 1 || validations != 1 {
		t.Fatalf("executor calls / argument validations = %d / %d, want 1 / 1", calls, validations)
	}
	if got := connectionRequestCount(connection); got != 1 {
		t.Fatalf("request count after overflow vectors = %d, want 1", got)
	}

	currentOutput := connectionOutput(connection, &output)
	for _, expected := range []string{
		`{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"invalid request"}}`,
		`{"jsonrpc":"2.0","id":2,"error":{"code":-32602,"message":"invalid params"}}`,
		`{"jsonrpc":"2.0","id":3,"error":{"code":-32602,"message":"invalid params"}}`,
		`{"jsonrpc":"2.0","id":4,"error":{"code":-32602,"message":"invalid params"}}`,
	} {
		if !strings.Contains(currentOutput, expected) {
			t.Fatalf("missing pre-admission response %q in %s", expected, currentOutput)
		}
	}
	if got := strings.Count(currentOutput, `"text":"ERROR\tbudget_exceeded\n"`); got != 3 {
		t.Fatalf("budget response count = %d, want 3: %s", got, currentOutput)
	}
	if strings.Contains(currentOutput, `"text":"ERROR\tinvalid_input\n"`) {
		t.Fatalf("saturated arguments reached semantic validation: %s", currentOutput)
	}

	executor.releaseFirst()
	connection.workers.Wait()
	if got := connectionRequestCount(connection); got != 0 {
		t.Fatalf("request count after worker return = %d, want 0", got)
	}
	startedLeases := instrumentation.leases()
	executorCalls := executor.snapshotCalls()
	if len(startedLeases) != 1 || len(executorCalls) != 1 {
		t.Fatalf("started/executor lease counts = %d/%d, want 1/1", len(startedLeases), len(executorCalls))
	}
	if startedLeases[0] != executorCalls[0].work {
		t.Fatalf("started/executor lease identity differs: %p / %p", startedLeases[0], executorCalls[0].work)
	}

	sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"read","arguments":{"path":"reused"}}}`)
	connection.workers.Wait()
	if calls, validations := executor.counts(); calls != 2 || validations != 2 {
		t.Fatalf("post-return executor calls / validations = %d / %d, want 2 / 2", calls, validations)
	}
	if got := instrumentation.workLeases.Load(); got != 2 {
		t.Fatalf("post-return work leases = %d, want 2", got)
	}
	executor.Close()
}

func TestQueuedCancellationCreatesNoExecutorWorkOrLease(t *testing.T) {
	executor := newInstrumentedExecutor()
	instrumentation := &connectionInstrumentation{}
	var output bytes.Buffer
	connection := newAdmissionTestConnection(workruntime.Limits{
		MaxConcurrent: 1,
		QueueMax:      1,
		QueueTimeout:  time.Hour,
	}, executor, instrumentation, &output)

	sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"read","arguments":{"path":"hold"}}}`)
	awaitMCPStdIOSignal(t, executor.firstBlocked, "first executor call")
	sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"read","arguments":{"path":"x","path":"y"}}}`)
	awaitAtomicValue(t, &instrumentation.workerTasks, 2, "accepted tool tasks")
	if got := connectionRequestCount(connection); got != 2 {
		t.Fatalf("active+queued request count = %d, want 2", got)
	}

	sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":11,"reason":"drop queued"}}`)
	awaitConnectionRequestCount(t, connection, 1)
	if calls, validations := executor.counts(); calls != 1 || validations != 1 {
		t.Fatalf("queued cancel executor calls / validations = %d / %d, want 1 / 1", calls, validations)
	}
	if got := instrumentation.workLeases.Load(); got != 1 {
		t.Fatalf("queued cancel work leases = %d, want 1", got)
	}
	if strings.Contains(connectionOutput(connection, &output), `"id":11`) {
		t.Fatalf("queued cancellation emitted a response: %s", connectionOutput(connection, &output))
	}

	sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"read","arguments":{"path":"replacement"}}}`)
	awaitAtomicValue(t, &instrumentation.workerTasks, 3, "replacement queued task")
	executor.releaseFirst()
	connection.workers.Wait()
	if calls, validations := executor.counts(); calls != 2 || validations != 2 {
		t.Fatalf("final executor calls / validations = %d / %d, want 2 / 2", calls, validations)
	}
	if got := instrumentation.workLeases.Load(); got != 2 {
		t.Fatalf("final work leases = %d, want 2", got)
	}
	if got := connectionRequestCount(connection); got != 0 {
		t.Fatalf("final request count = %d, want 0", got)
	}
	executor.Close()
}

type executorCall struct {
	call api.Call
	work *workruntime.WorkLease
}

type instrumentedExecutor struct {
	mu                  sync.Mutex
	calls               []executorCall
	argumentValidations int
	closed              int
	firstBlocked        chan struct{}
	firstBlockOnce      sync.Once
	firstRelease        chan struct{}
	firstReleaseOnce    sync.Once
}

func newInstrumentedExecutor() *instrumentedExecutor {
	return &instrumentedExecutor{
		firstBlocked: make(chan struct{}),
		firstRelease: make(chan struct{}),
	}
}

func (executor *instrumentedExecutor) Call(_ context.Context, call api.Call, work *workruntime.WorkLease) workruntime.Execution {
	executor.mu.Lock()
	executor.calls = append(executor.calls, executorCall{call: call, work: work})
	executor.argumentValidations++
	executor.mu.Unlock()
	_, validationError := jsonwire.ScanObject(call.Arguments(), protocolJSONLimits(), jsonwire.ToolArguments)
	if bytes.Contains(call.Arguments(), []byte(`"hold"`)) {
		executor.firstBlockOnce.Do(func() { close(executor.firstBlocked) })
		<-executor.firstRelease
	}
	defer work.WorkerReturned()
	if validationError != nil {
		return workruntime.Execution{Kind: workruntime.ExecutionOrdinary, Result: api.Navigation("ERROR\tinvalid_input\n", true)}
	}
	return workruntime.Execution{Kind: workruntime.ExecutionOrdinary, Result: api.Navigation("DATA\tok\n", false)}
}

func (executor *instrumentedExecutor) Close() {
	executor.mu.Lock()
	executor.closed++
	executor.mu.Unlock()
	executor.releaseFirst()
}

func (executor *instrumentedExecutor) releaseFirst() {
	executor.firstReleaseOnce.Do(func() { close(executor.firstRelease) })
}

func (executor *instrumentedExecutor) counts() (int, int) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return len(executor.calls), executor.argumentValidations
}

func (executor *instrumentedExecutor) snapshotCalls() []executorCall {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return append([]executorCall(nil), executor.calls...)
}

func newAdmissionTestConnection(limits workruntime.Limits, executor CallExecutor, instrumentation *connectionInstrumentation, output *bytes.Buffer) *stdioConnection {
	fatal := workruntime.NewFatalSignal()
	return &stdioConnection{
		executor:        executor,
		coordinator:     workruntime.NewCoordinatorWithFatal(limits, fatal),
		fatal:           fatal,
		lifecycle:       &connectionLifecycle{state: lifecycleReady},
		usedIDs:         newUsedIDRegistry(),
		output:          output,
		protocolBusy:    newProtocolBusyQueue(),
		toolOutputs:     mustNewToolOutputLimiter(limits),
		toolRequests:    make(map[SemanticIDKey]*toolRequest),
		instrumentation: instrumentation,
	}
}

func sendConnectionFrame(t *testing.T, connection *stdioConnection, frame string) {
	t.Helper()
	closeConnection, err := connection.handleFrame(context.Background(), []byte(frame))
	if err != nil || closeConnection {
		t.Fatalf("handleFrame(%s) = close %v error %v", frame, closeConnection, err)
	}
}

func connectionOutput(connection *stdioConnection, output *bytes.Buffer) string {
	connection.outputMu.Lock()
	defer connection.outputMu.Unlock()
	return output.String()
}

func awaitProtocolIdle(t *testing.T, connection *stdioConnection) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		connection.protocolSlot.mu.Lock()
		idle := !connection.protocolSlot.inUse
		connection.protocolSlot.mu.Unlock()
		if idle {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("timed out waiting for protocol slot")
		case <-ticker.C:
		}
	}
}

func awaitConnectionRequestCount(t *testing.T, connection *stdioConnection, want int) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if connectionRequestCount(connection) == want {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for request count %d", want)
		case <-ticker.C:
		}
	}
}

func connectionRequestCount(connection *stdioConnection) int {
	connection.toolRequestsMu.Lock()
	defer connection.toolRequestsMu.Unlock()
	return len(connection.toolRequests)
}

func awaitAtomicValue(t *testing.T, value interface{ Load() uint64 }, want uint64, description string) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if value.Load() == want {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s = %d; got %d", description, want, value.Load())
		case <-ticker.C:
		}
	}
}
