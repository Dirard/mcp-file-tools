package mcpstdio

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/api"
	workruntime "github.com/Dirard/mcp-file-tools/internal/runtime"
)

func TestQueueTimeoutKeepsResponseRightWhileOutboundCapacityIsFull(t *testing.T) {
	limits := workruntime.Limits{
		MaxConcurrent: 1,
		QueueMax:      1,
		QueueTimeout:  100 * time.Millisecond,
	}
	executor := newOutboundTimeoutExecutor()
	writer := newOutboundGateWriter()
	connection := newOutboundTestConnection(limits, executor, writer)
	t.Cleanup(func() {
		writer.release()
		executor.releaseSecond()
		connection.closeExecutor()
	})

	sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read","arguments":{"path":"first"}}}`)
	awaitMCPStdIOSignal(t, writer.entered, "first blocked response")
	sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"read","arguments":{"path":"lingering"}}}`)
	awaitMCPStdIOSignal(t, executor.secondStarted, "second active executor")

	thirdDone := make(chan error, 1)
	go func() {
		closeConnection, err := connection.handleFrame(context.Background(), []byte(
			`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"read","arguments":{"path":"timeout"}}}`,
		))
		if err == nil && closeConnection {
			err = fmt.Errorf("connection closed while admitting timeout request")
		}
		thirdDone <- err
	}()

	time.Sleep(2 * limits.QueueTimeout)
	if got := executor.calls.Load(); got != 2 {
		t.Fatalf("executor calls before output release = %d, want 2", got)
	}

	writer.release()
	if err := awaitServeResult(t, thirdDone); err != nil {
		t.Fatalf("timeout request admission after output release: %v", err)
	}
	awaitOutboundContains(t, writer, `"id":3,"result"`)
	wire := writer.String()
	if !strings.Contains(wire, `ERROR\tbudget_exceeded\n`) {
		t.Fatalf("timeout response is not canonical budget_exceeded: %q", wire)
	}
	if got := strings.Count(wire, `"id":3,`); got != 1 {
		t.Fatalf("timeout response count = %d, want 1: %q", got, wire)
	}
	if got := executor.calls.Load(); got != 2 {
		t.Fatalf("timed-out request reached executor; calls = %d", got)
	}

	executor.releaseSecond()
	connection.workers.Wait()
	if got := strings.Count(writer.String(), `"id":3,`); got != 1 {
		t.Fatalf("timeout response count after worker drain = %d, want 1", got)
	}
}

func TestCancelledPreStartOutputWaitDoesNotRetainAdmissionState(t *testing.T) {
	limits := workruntime.Limits{
		MaxConcurrent: 1,
		QueueMax:      1,
		QueueTimeout:  time.Minute,
	}
	executor := newOutboundTimeoutExecutor()
	writer := newOutboundGateWriter()
	connection := newOutboundTestConnection(limits, executor, writer)
	t.Cleanup(func() {
		writer.release()
		executor.releaseSecond()
		connection.closeExecutor()
	})

	sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read","arguments":{"path":"first"}}}`)
	awaitMCPStdIOSignal(t, writer.entered, "first blocked response")
	sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"read","arguments":{"path":"lingering"}}}`)
	awaitMCPStdIOSignal(t, executor.secondStarted, "second active executor")

	for id := 3; id <= 102; id++ {
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		closeConnection, err := connection.handleFrame(cancelled, []byte(fmt.Sprintf(
			`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"read","arguments":{"path":"cancelled"}}}`,
			id,
		)))
		if err != nil || closeConnection {
			t.Fatalf("cancelled pre-start frame %d = close %v error %v", id, closeConnection, err)
		}
		if got := connectionRequestCount(connection); got != 1 {
			t.Fatalf("request state after cancelled pre-start frame %d = %d, want active executor only", id, got)
		}
		if got := connection.toolOutputs.active(); got != 2 {
			t.Fatalf("output permits after cancelled pre-start frame %d = %d, want 2", id, got)
		}
	}
	if got := executor.calls.Load(); got != 2 {
		t.Fatalf("cancelled pre-start requests reached executor; calls = %d", got)
	}

	writer.release()
	executor.releaseSecond()
	connection.workers.Wait()
	if got := connectionRequestCount(connection); got != 0 {
		t.Fatalf("request state after drain = %d, want 0", got)
	}
	if got := connection.toolOutputs.active(); got != 0 {
		t.Fatalf("output permits after drain = %d, want 0", got)
	}
}

func TestBlockedToolOutputBackpressuresBeforeExecutorCountCanGrow(t *testing.T) {
	const (
		admittedBound = 4
		frameCount    = 100
	)
	executor := &outboundBoundExecutor{calls: make(chan uint64, frameCount)}
	writer := newOutboundGateWriter()
	connection := newPublicationTestConnection(executor, writer)
	t.Cleanup(func() {
		writer.release()
		connection.closeExecutor()
	})

	sendDone := make(chan error, 1)
	go func() {
		for id := 1; id <= frameCount; id++ {
			closeConnection, err := connection.handleFrame(context.Background(), []byte(fmt.Sprintf(
				`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"read","arguments":{"path":"page"}}}`,
				id,
			)))
			if err != nil {
				sendDone <- err
				return
			}
			if closeConnection {
				sendDone <- fmt.Errorf("connection closed while flooding tool output")
				return
			}
		}
		sendDone <- nil
	}()

	for call := 0; call < admittedBound; call++ {
		select {
		case <-executor.calls:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for executor call %d", call+1)
		}
	}
	awaitMCPStdIOSignal(t, writer.entered, "blocked tool output")
	select {
	case call := <-executor.calls:
		t.Errorf("executor escaped outbound bound with call %d", call)
	case <-time.After(30 * time.Millisecond):
	}
	sendCompleted := false
	var sendErr error
	select {
	case err := <-sendDone:
		sendCompleted = true
		sendErr = err
		t.Errorf("tool flood did not receive output backpressure: %v", err)
	default:
	}

	writer.release()
	if !sendCompleted {
		sendErr = awaitServeResult(t, sendDone)
	}
	if sendErr != nil {
		t.Fatalf("tool flood error after output release: %v", sendErr)
	}
	connection.workers.Wait()
	if got := executor.count.Load(); got > frameCount || got < admittedBound {
		t.Fatalf("executor calls after release = %d, want %d..%d", got, admittedBound, frameCount)
	}
}

func TestBusyProtocolOutputBackpressuresReaderInsteadOfLaunchingWorkers(t *testing.T) {
	writer := newOutboundGateWriter()
	connection := newPublicationTestConnection(&fakeCallExecutor{}, writer)
	t.Cleanup(func() {
		writer.release()
		connection.closeExecutor()
	})

	closeConnection, err := connection.handleFrame(context.Background(), []byte(
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
	))
	if err != nil || closeConnection {
		t.Fatalf("first ping = close %v, error %v", closeConnection, err)
	}
	awaitMCPStdIOSignal(t, writer.entered, "blocked protocol output")

	floodDone := make(chan error, 1)
	go func() {
		for id := 2; id <= 100; id++ {
			closeConnection, frameErr := connection.handleFrame(context.Background(), []byte(fmt.Sprintf(
				`{"jsonrpc":"2.0","id":%d,"method":"ping"}`,
				id,
			)))
			if frameErr != nil {
				floodDone <- frameErr
				return
			}
			if closeConnection {
				floodDone <- fmt.Errorf("connection closed during protocol flood")
				return
			}
		}
		floodDone <- nil
	}()

	floodCompleted := false
	var floodErr error
	select {
	case err := <-floodDone:
		floodCompleted = true
		floodErr = err
		t.Errorf("busy protocol flood launched workers instead of applying backpressure: %v", err)
	case <-time.After(30 * time.Millisecond):
	}

	writer.release()
	if !floodCompleted {
		floodErr = awaitServeResult(t, floodDone)
	}
	if floodErr != nil {
		t.Fatalf("protocol flood error after output release: %v", floodErr)
	}
	connection.closeProtocolBusy()
	connection.workers.Wait()
	connection.protocolWorkers.Wait()
}

type outboundBoundExecutor struct {
	count atomic.Uint64
	calls chan uint64
}

func (executor *outboundBoundExecutor) Call(_ context.Context, _ api.Call, work *workruntime.WorkLease) workruntime.Execution {
	call := executor.count.Add(1)
	executor.calls <- call
	work.WorkerReturned()
	return workruntime.Execution{
		Kind:   workruntime.ExecutionOrdinary,
		Result: api.Navigation("DATA\tok\n", false),
	}
}

func (*outboundBoundExecutor) Close() {}

type outboundTimeoutExecutor struct {
	calls         atomic.Uint64
	secondStarted chan struct{}
	secondOnce    sync.Once
	secondRelease chan struct{}
	releaseOnce   sync.Once
}

func newOutboundTimeoutExecutor() *outboundTimeoutExecutor {
	return &outboundTimeoutExecutor{
		secondStarted: make(chan struct{}),
		secondRelease: make(chan struct{}),
	}
}

func (executor *outboundTimeoutExecutor) Call(_ context.Context, _ api.Call, work *workruntime.WorkLease) workruntime.Execution {
	call := executor.calls.Add(1)
	if call == 2 {
		executor.secondOnce.Do(func() { close(executor.secondStarted) })
		<-executor.secondRelease
	}
	work.WorkerReturned()
	return workruntime.Execution{
		Kind:   workruntime.ExecutionOrdinary,
		Result: api.Navigation("DATA\tok\n", false),
	}
}

func (*outboundTimeoutExecutor) Close() {}

func (executor *outboundTimeoutExecutor) releaseSecond() {
	executor.releaseOnce.Do(func() { close(executor.secondRelease) })
}

type outboundGateWriter struct {
	entered     chan struct{}
	enteredOnce sync.Once
	releaseGate chan struct{}
	releaseOnce sync.Once
	mu          sync.Mutex
	data        []byte
}

func newOutboundGateWriter() *outboundGateWriter {
	return &outboundGateWriter{
		entered:     make(chan struct{}),
		releaseGate: make(chan struct{}),
	}
}

func (writer *outboundGateWriter) Write(data []byte) (int, error) {
	writer.enteredOnce.Do(func() { close(writer.entered) })
	<-writer.releaseGate
	writer.mu.Lock()
	writer.data = append(writer.data, data...)
	writer.mu.Unlock()
	return len(data), nil
}

func (writer *outboundGateWriter) release() {
	writer.releaseOnce.Do(func() { close(writer.releaseGate) })
}

func (writer *outboundGateWriter) String() string {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return string(writer.data)
}

func newOutboundTestConnection(limits workruntime.Limits, executor CallExecutor, output *outboundGateWriter) *stdioConnection {
	fatal := workruntime.NewFatalSignal()
	return &stdioConnection{
		executor:     executor,
		coordinator:  workruntime.NewCoordinatorWithFatal(limits, fatal),
		fatal:        fatal,
		lifecycle:    &connectionLifecycle{state: lifecycleReady},
		usedIDs:      newUsedIDRegistry(),
		output:       output,
		protocolBusy: newProtocolBusyQueue(),
		toolOutputs:  mustNewToolOutputLimiter(limits),
		toolRequests: make(map[SemanticIDKey]*toolRequest),
	}
}

func awaitOutboundContains(t *testing.T, writer *outboundGateWriter, fragment string) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if strings.Contains(writer.String(), fragment) {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for outbound fragment %q in %q", fragment, writer.String())
		case <-ticker.C:
		}
	}
}
