package mcpstdio

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/api"
	workruntime "github.com/Dirard/mcp-file-tools/internal/runtime"
)

var errFatalTestWrite = errors.New("fatal test write failed")

func TestWorkerPanicSignalsFatalOnceAndSuppressesNormalResults(t *testing.T) {
	t.Parallel()

	fatal := workruntime.NewFatalSignal()
	executor := &panicCallExecutor{}
	var output bytes.Buffer
	connection := &stdioConnection{
		executor: executor,
		coordinator: workruntime.NewCoordinatorWithFatal(workruntime.Limits{
			MaxConcurrent: 1,
			QueueMax:      1,
			QueueTimeout:  time.Second,
		}, fatal),
		fatal:        fatal,
		lifecycle:    &connectionLifecycle{state: lifecycleReady},
		usedIDs:      newUsedIDRegistry(),
		output:       &output,
		protocolBusy: newProtocolBusyQueue(),
		toolOutputs: mustNewToolOutputLimiter(workruntime.Limits{
			MaxConcurrent: 1,
			QueueMax:      1,
			QueueTimeout:  time.Second,
		}),
		toolRequests: make(map[SemanticIDKey]*toolRequest),
	}

	closeConnection, err := connection.handleFrame(context.Background(), []byte(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read","arguments":{"path":"/secret/input"}}}`,
	))
	if err != nil || closeConnection {
		t.Fatalf("initial tools/call = close %v, error %v", closeConnection, err)
	}
	connection.workers.Wait()

	select {
	case <-fatal.Done():
	default:
		t.Fatal("worker panic emitted no fatal signal")
	}
	if !errors.Is(connection.loadAsyncError(), workruntime.ErrInternalFatal) {
		t.Fatalf("async error = %v, want internal fatal", connection.loadAsyncError())
	}
	if executor.calls.Load() != 1 {
		t.Fatalf("executor calls = %d, want 1", executor.calls.Load())
	}
	if executor.ownerReleases.Load() != 1 {
		t.Fatalf("transferred owner releases = %d, want 1", executor.ownerReleases.Load())
	}
	if output.Len() != 0 {
		t.Fatalf("panic emitted a response: %q", output.String())
	}

	closeConnection, err = connection.handleFrame(context.Background(), []byte(
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
	))
	if closeConnection || !errors.Is(err, workruntime.ErrInternalFatal) {
		t.Fatalf("frame after fatal = close %v, error %v", closeConnection, err)
	}
	if executor.calls.Load() != 1 {
		t.Fatalf("executor ran after fatal: %d calls", executor.calls.Load())
	}
	if output.Len() != 0 {
		t.Fatalf("normal result escaped after fatal: %q", output.String())
	}
	for _, forbidden := range []string{"secret", "input", "panicCallExecutor"} {
		if strings.Contains(workruntime.ErrInternalFatal.Error(), forbidden) {
			t.Fatalf("fatal error echoes sensitive fragment %q", forbidden)
		}
	}
}

func TestOwnedWorkerEntriesShareOneFatalSignalAndCleanIndependently(t *testing.T) {
	t.Parallel()

	fatal := workruntime.NewFatalSignal()
	connection := &stdioConnection{fatal: fatal, output: &bytes.Buffer{}}
	connection.recordAsyncError(errors.New("ordinary worker error"))
	var cleanups atomic.Uint64
	start := make(chan struct{})
	for range 2 {
		connection.launch(func() error {
			<-start
			panic("untrusted input")
		}, func() {
			cleanups.Add(1)
		})
	}
	close(start)
	connection.workers.Wait()

	select {
	case <-fatal.Done():
	default:
		t.Fatal("owned workers emitted no fatal signal")
	}
	if cleanups.Load() != 2 {
		t.Fatalf("worker cleanup count = %d, want 2", cleanups.Load())
	}
	if !errors.Is(connection.loadAsyncError(), workruntime.ErrInternalFatal) {
		t.Fatalf("worker error = %v, want internal fatal", connection.loadAsyncError())
	}
}

func TestFatalTransitionLinearizesAfterInFlightResponseTransaction(t *testing.T) {
	fatal := workruntime.NewFatalSignal()
	var sequence atomic.Uint64
	writer := newFatalOrderingWriter(&sequence)
	t.Cleanup(writer.release)
	connection := &stdioConnection{fatal: fatal, output: writer}

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- connection.write([]byte("normal response\n"))
	}()
	awaitMCPStdIOSignal(t, writer.entered, "blocked response write")

	panicUnwound := make(chan struct{})
	cleanupDone := make(chan struct{})
	var fatalOrder atomic.Uint64
	go connection.runWorker(func() error {
		defer close(panicUnwound)
		panic("connection invariant")
	}, func() {
		fatalOrder.Store(sequence.Add(1))
		close(cleanupDone)
	})
	awaitMCPStdIOSignal(t, panicUnwound, "worker panic unwind")

	select {
	case <-cleanupDone:
	case <-time.After(100 * time.Millisecond):
	}
	writer.release()
	if err := awaitServeResult(t, writeDone); err != nil {
		t.Fatalf("in-flight response write: %v", err)
	}
	awaitMCPStdIOSignal(t, cleanupDone, "fatal cleanup")

	if got, want := writer.order.Load(), fatalOrder.Load(); got == 0 || want == 0 || got >= want {
		t.Fatalf("response/fatal linearization order = %d/%d, want response before fatal", got, want)
	}
	if got := writer.String(); got != "normal response\n" {
		t.Fatalf("in-flight response bytes = %q", got)
	}
}

func TestFatalBeforePublicationTransactionAbortsWithoutOutput(t *testing.T) {
	fatal := workruntime.NewFatalSignal()
	if panicked := fatal.Run(func() { panic("connection invariant") }, nil); !panicked {
		t.Fatal("fatal trigger did not recover panic")
	}
	publication := &fatalAbortPublication{}
	var output bytes.Buffer
	connection := &stdioConnection{fatal: fatal, output: &output}

	err := connection.writeToolCompletion(toolResponseCompletion{
		response:    []byte("cursor response\n"),
		publication: publication,
	})
	if !errors.Is(err, workruntime.ErrInternalFatal) {
		t.Fatalf("publication after fatal error = %v, want internal fatal", err)
	}
	if got := publication.aborts.Load(); got != 1 {
		t.Fatalf("publication aborts = %d, want 1", got)
	}
	if output.Len() != 0 {
		t.Fatalf("publication after fatal wrote output: %q", output.String())
	}
}

func TestStoppedResponseGateAbortPanicTriggersFatal(t *testing.T) {
	fatal := workruntime.NewFatalSignal()
	publication := &panicAbortPublication{}
	var output bytes.Buffer
	connection := &stdioConnection{fatal: fatal, output: &output}
	connection.responses.stopAccepting()

	connection.runWorker(func() error {
		return connection.writeToolCompletion(toolResponseCompletion{
			response:    []byte("cursor response\n"),
			publication: publication,
		})
	}, nil)

	if !fatal.Triggered() {
		t.Fatal("publication abort panic after response shutdown did not trigger fatal")
	}
	if got := publication.aborts.Load(); got != 1 {
		t.Fatalf("publication aborts = %d, want 1", got)
	}
	if output.Len() != 0 {
		t.Fatalf("stopped publication wrote output: %q", output.String())
	}
}

func TestWriteFailureStopsLaterResponseBeforeWorkerReportsError(t *testing.T) {
	writer := newTerminalOrderingWriter()
	t.Cleanup(writer.releaseFirst)
	connection := &stdioConnection{
		fatal:  workruntime.NewFatalSignal(),
		output: writer,
	}

	writeReturned := make(chan struct{})
	allowWorkerError := make(chan struct{})
	workerDone := make(chan struct{})
	go func() {
		connection.runWorker(func() error {
			err := connection.write([]byte("first response\n"))
			close(writeReturned)
			<-allowWorkerError
			return err
		}, nil)
		close(workerDone)
	}()
	awaitMCPStdIOSignal(t, writer.firstEntered, "first failing write")

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- connection.write([]byte("second response\n"))
	}()
	writer.releaseFirst()
	awaitMCPStdIOSignal(t, writeReturned, "first write return")
	secondErr := awaitServeResult(t, secondDone)
	close(allowWorkerError)
	awaitMCPStdIOSignal(t, workerDone, "failing worker completion")

	if !errors.Is(secondErr, errConnectionStopped) {
		t.Fatalf("later response error = %v, want connection stopped", secondErr)
	}
	if got := writer.calls.Load(); got != 1 {
		t.Fatalf("writer calls = %d, want only the failing write", got)
	}
}

type panicCallExecutor struct {
	calls         atomic.Uint64
	ownerReleases atomic.Uint64
	closed        atomic.Uint64
}

func (executor *panicCallExecutor) Call(_ context.Context, _ api.Call, work *workruntime.WorkLease) workruntime.Execution {
	executor.calls.Add(1)
	owner := &countingPanicOwner{releases: &executor.ownerReleases}
	if err := work.Transfer(owner); err != nil {
		panic("panic executor could not transfer work")
	}
	defer work.WorkerReturned()
	panic(`/private/raw-request.json`)
}

func (executor *panicCallExecutor) Close() {
	executor.closed.Add(1)
}

type countingPanicOwner struct {
	releases *atomic.Uint64
}

func (owner *countingPanicOwner) Release() {
	owner.releases.Add(1)
}

type fatalOrderingWriter struct {
	sequence    *atomic.Uint64
	entered     chan struct{}
	releaseC    chan struct{}
	enteredOnce sync.Once
	releaseOnce sync.Once
	mu          sync.Mutex
	output      bytes.Buffer
	order       atomic.Uint64
}

type fatalAbortPublication struct {
	aborts atomic.Uint64
}

type panicAbortPublication struct {
	aborts atomic.Uint64
}

type terminalOrderingWriter struct {
	firstEntered chan struct{}
	firstRelease chan struct{}
	releaseOnce  sync.Once
	calls        atomic.Uint64
}

func newTerminalOrderingWriter() *terminalOrderingWriter {
	return &terminalOrderingWriter{
		firstEntered: make(chan struct{}),
		firstRelease: make(chan struct{}),
	}
}

func (writer *terminalOrderingWriter) Write(response []byte) (int, error) {
	if call := writer.calls.Add(1); call == 1 {
		close(writer.firstEntered)
		<-writer.firstRelease
		return 0, errFatalTestWrite
	}
	return len(response), nil
}

func (writer *terminalOrderingWriter) releaseFirst() {
	writer.releaseOnce.Do(func() { close(writer.firstRelease) })
}

func (*fatalAbortPublication) Prepare() error {
	panic("publication prepared after fatal")
}

func (*fatalAbortPublication) Commit() error {
	panic("publication committed after fatal")
}

func (publication *fatalAbortPublication) Abort() {
	publication.aborts.Add(1)
}

func (*panicAbortPublication) Prepare() error {
	panic("publication prepared after response shutdown")
}

func (*panicAbortPublication) Commit() error {
	panic("publication committed after response shutdown")
}

func (publication *panicAbortPublication) Abort() {
	publication.aborts.Add(1)
	panic("publication abort panic")
}

func newFatalOrderingWriter(sequence *atomic.Uint64) *fatalOrderingWriter {
	return &fatalOrderingWriter{
		sequence: sequence,
		entered:  make(chan struct{}),
		releaseC: make(chan struct{}),
	}
}

func (writer *fatalOrderingWriter) Write(response []byte) (int, error) {
	writer.enteredOnce.Do(func() { close(writer.entered) })
	<-writer.releaseC
	writer.mu.Lock()
	written, err := writer.output.Write(response)
	writer.mu.Unlock()
	writer.order.Store(writer.sequence.Add(1))
	return written, err
}

func (writer *fatalOrderingWriter) release() {
	writer.releaseOnce.Do(func() { close(writer.releaseC) })
}

func (writer *fatalOrderingWriter) String() string {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.output.String()
}
