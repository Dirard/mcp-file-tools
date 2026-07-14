package mcpstdio

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/api"
	workruntime "github.com/Dirard/mcp-file-tools/internal/runtime"
)

var errShutdownTestWrite = errors.New("shutdown test write failed")

func TestServeEOFDoesNotWaitForLingeringExecutor(t *testing.T) {
	executor := newLingeringEOFExecutor()
	input := &eofAfterSignalReader{
		data: []byte(
			initializeRequest(`"init"`, "2025-11-25", `{}`) + "\n" +
				`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
				`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"read","arguments":{"path":"page"}}}` + "\n",
		),
		waitBeforeEOF: executor.started,
	}
	output := &flushTrackingBuffer{}
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- newTestServer(&fakeExecutorFactory{executor: executor}).Serve(context.Background(), input, output)
	}()

	awaitMCPStdIOSignal(t, executor.started, "lingering executor start")
	var serveErr error
	select {
	case serveErr = <-serveResult:
	case <-time.After(100 * time.Millisecond):
		t.Error("Serve waited for a lingering executor after EOF")
		executor.releaseCall()
		serveErr = awaitServeResult(t, serveResult)
	}
	if serveErr != nil {
		t.Fatalf("Serve() error = %v, want clean EOF", serveErr)
	}
	if executor.closed.Load() != 1 {
		t.Fatalf("executor closes = %d, want 1", executor.closed.Load())
	}

	executor.releaseCall()
	awaitMCPStdIOSignal(t, executor.returned, "lingering executor return")
	time.Sleep(20 * time.Millisecond)
	if strings.Contains(output.String(), `"id":7`) {
		t.Fatalf("lingering executor wrote after Serve returned: %q", output.String())
	}
}

func TestServeEOFWaitsForClaimedPublicationResponse(t *testing.T) {
	executor := newPublicationTestExecutor(publicationTestConfig{
		result: api.Navigation("DATA\tpage\n", false),
	})
	writer := newBlockingWriteNumberWriter(2)
	defer writer.releaseWrite()
	input := &eofAfterSignalReader{
		data: []byte(
			initializeRequest(`"init"`, "2025-11-25", `{}`) + "\n" +
				`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
				`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"read","arguments":{"path":"page"}}}` + "\n",
		),
		waitBeforeEOF: writer.entered,
	}
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- newTestServer(&fakeExecutorFactory{executor: executor}).Serve(context.Background(), input, writer)
	}()

	awaitMCPStdIOSignal(t, writer.entered, "claimed publication response write")
	select {
	case serveErr := <-serveResult:
		t.Fatalf("Serve returned while a claimed response write was active: %v", serveErr)
	case <-time.After(100 * time.Millisecond):
	}

	writer.releaseWrite()
	if serveErr := awaitServeResult(t, serveResult); serveErr != nil {
		t.Fatalf("Serve() error = %v, want clean EOF", serveErr)
	}
	assertPublicationCommitted(t, executor)
	if got := executor.closed.Load(); got != 1 {
		t.Fatalf("executor closes = %d, want 1", got)
	}
	outputAtReturn := writer.String()
	if !strings.Contains(outputAtReturn, `"id":7`) {
		t.Fatalf("claimed response missing at Serve return: %q", outputAtReturn)
	}
	time.Sleep(20 * time.Millisecond)
	if got := writer.String(); got != outputAtReturn {
		t.Fatalf("output changed after Serve returned: before=%q after=%q", outputAtReturn, got)
	}
}

func TestServeContextWaitsForClaimedPublicationResponse(t *testing.T) {
	executor := newPublicationTestExecutor(publicationTestConfig{
		result: api.Navigation("DATA\tpage\n", false),
	})
	writer := newBlockingWriteNumberWriter(2)
	defer writer.releaseWrite()
	input, inputWriter := io.Pipe()
	defer input.Close()
	defer inputWriter.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serveResult := make(chan error, 1)
	go func() {
		serveResult <- newTestServer(&fakeExecutorFactory{executor: executor}).Serve(ctx, input, writer)
	}()
	writeResult := make(chan error, 1)
	go func() {
		_, err := io.WriteString(inputWriter,
			initializeRequest(`"init"`, "2025-11-25", `{}`)+"\n"+
				`{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"+
				`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"read","arguments":{"path":"page"}}}`+"\n",
		)
		writeResult <- err
	}()

	awaitMCPStdIOSignal(t, writer.entered, "claimed publication response write")
	cancel()
	select {
	case serveErr := <-serveResult:
		t.Fatalf("Serve returned while a claimed response write was active: %v", serveErr)
	case <-time.After(100 * time.Millisecond):
	}

	writer.releaseWrite()
	if serveErr := awaitServeResult(t, serveResult); !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("Serve() error = %v, want context cancellation", serveErr)
	}
	assertPublicationCommitted(t, executor)
	if got := executor.closed.Load(); got != 1 {
		t.Fatalf("executor closes = %d, want 1", got)
	}
	if !strings.Contains(writer.String(), `"id":7`) {
		t.Fatalf("claimed response missing at Serve return: %q", writer.String())
	}
	_ = inputWriter.Close()
	select {
	case writeErr := <-writeResult:
		if writeErr != nil && !errors.Is(writeErr, io.ErrClosedPipe) {
			t.Fatalf("input write error = %v", writeErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for input writer")
	}
}

func TestServeEOFWaitsBetweenOrdinaryResponseClaimAndWrite(t *testing.T) {
	executor := newPublicationTestExecutor(publicationTestConfig{
		result: api.Navigation("DATA\tpage\n", false),
	})
	claimed := make(chan struct{})
	releaseClaim := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseClaim) }) }
	t.Cleanup(release)
	claimedTimeout := false
	instrumentation := &connectionInstrumentation{
		afterResponseClaim: func(timeout bool) {
			claimedTimeout = timeout
			close(claimed)
			<-releaseClaim
		},
	}
	input := &eofAfterSignalReader{
		data:          []byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"read","arguments":{"path":"page"}}}` + "\n"),
		waitBeforeEOF: claimed,
	}
	var output bytes.Buffer
	connection := newAdmissionTestConnection(testCallLimits(), executor, instrumentation, &output)
	connection.frames = newFrameReader(input)
	serveResult := make(chan error, 1)
	go func() { serveResult <- connection.serve(context.Background()) }()

	awaitMCPStdIOSignal(t, claimed, "ordinary response claim")
	if claimedTimeout {
		t.Fatal("ordinary response was recorded as a timeout response")
	}
	select {
	case serveErr := <-serveResult:
		t.Fatalf("Serve returned between ordinary response claim and write: %v", serveErr)
	case <-time.After(100 * time.Millisecond):
	}

	release()
	if serveErr := awaitServeResult(t, serveResult); serveErr != nil {
		t.Fatalf("Serve() error = %v, want clean EOF", serveErr)
	}
	assertPublicationCommitted(t, executor)
	if got := output.String(); !strings.Contains(got, `"id":7`) {
		t.Fatalf("claimed ordinary response missing at Serve return: %q", got)
	}
}

func TestServeEOFWaitsBetweenTimeoutResponseClaimAndWrite(t *testing.T) {
	limits := workruntime.Limits{MaxConcurrent: 1, QueueMax: 1, QueueTimeout: 20 * time.Millisecond}
	executor := newInstrumentedExecutor()
	claimed := make(chan struct{})
	releaseClaim := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseClaim) }) }
	t.Cleanup(release)
	claimedTimeout := false
	instrumentation := &connectionInstrumentation{
		afterResponseClaim: func(timeout bool) {
			claimedTimeout = timeout
			close(claimed)
			<-releaseClaim
		},
	}
	input := &eofAfterSignalReader{
		data: []byte(
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read","arguments":{"path":"hold"}}}` + "\n" +
				`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"read","arguments":{"path":"queued"}}}` + "\n",
		),
		waitBeforeEOF: claimed,
	}
	var output bytes.Buffer
	connection := newAdmissionTestConnection(limits, executor, instrumentation, &output)
	connection.frames = newFrameReader(input)
	serveResult := make(chan error, 1)
	go func() { serveResult <- connection.serve(context.Background()) }()

	awaitMCPStdIOSignal(t, executor.firstBlocked, "active executor call")
	awaitMCPStdIOSignal(t, claimed, "timeout response claim")
	if !claimedTimeout {
		t.Fatal("queue-timeout response was recorded as an ordinary response")
	}
	select {
	case serveErr := <-serveResult:
		t.Fatalf("Serve returned between timeout response claim and write: %v", serveErr)
	case <-time.After(100 * time.Millisecond):
	}

	release()
	if serveErr := awaitServeResult(t, serveResult); serveErr != nil {
		t.Fatalf("Serve() error = %v, want clean EOF", serveErr)
	}
	got := output.String()
	if !strings.Contains(got, `"id":2`) || !strings.Contains(got, "budget_exceeded") {
		t.Fatalf("claimed timeout response missing at Serve return: %q", got)
	}
	if strings.Contains(got, `"id":1`) {
		t.Fatalf("cancelled active call wrote during EOF shutdown: %q", got)
	}
}

func assertPublicationCommitted(t *testing.T, executor *publicationTestExecutor) {
	t.Helper()
	publication := executor.awaitPublication(t)
	snapshot := publication.snapshot()
	if got := strings.Join(snapshot.phases, ","); got != "prepare,commit" {
		t.Fatalf("publication phases = %q, want prepare,commit", got)
	}
	if snapshot.violation != "" {
		t.Fatalf("publication phase violation: %s", snapshot.violation)
	}
	if got := publication.released.Load(); got != 1 {
		t.Fatalf("publication owner releases = %d, want 1", got)
	}
}

func TestServeWorkerWriteErrorWakesOpenInput(t *testing.T) {
	executor := &fakeCallExecutor{}
	writer := &failWriteNumberWriter{failAt: 2}
	serveErr := runServerWithOpenInput(t, executor, writer)
	if !errors.Is(serveErr, errShutdownTestWrite) {
		t.Fatalf("Serve() error = %v, want writer failure", serveErr)
	}
	executor.mu.Lock()
	closed := executor.closed
	executor.mu.Unlock()
	if closed != 1 {
		t.Fatalf("executor closes = %d, want 1", closed)
	}
}

func TestServeWorkerFatalWakesOpenInput(t *testing.T) {
	executor := &panicCallExecutor{}
	serveErr := runServerWithOpenInput(t, executor, &bytes.Buffer{})
	if !errors.Is(serveErr, workruntime.ErrInternalFatal) {
		t.Fatalf("Serve() error = %v, want internal fatal", serveErr)
	}
	if executor.closed.Load() != 1 {
		t.Fatalf("executor closes = %d, want 1", executor.closed.Load())
	}
}

func runServerWithOpenInput(t *testing.T, executor CallExecutor, output io.Writer) error {
	t.Helper()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()

	serveResult := make(chan error, 1)
	go func() {
		serveResult <- newTestServer(&fakeExecutorFactory{executor: executor}).Serve(context.Background(), reader, output)
	}()
	writeResult := make(chan error, 1)
	go func() {
		_, err := io.WriteString(writer,
			initializeRequest(`"init"`, "2025-11-25", `{}`)+"\n"+
				`{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"+
				`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"read","arguments":{"path":"page"}}}`+"\n",
		)
		writeResult <- err
	}()

	select {
	case err := <-serveResult:
		return err
	case <-time.After(100 * time.Millisecond):
		t.Error("Serve remained blocked on open input after a worker terminal error")
		_ = writer.Close()
		return awaitServeResult(t, serveResult)
	}
}

func awaitServeResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Serve to return")
		return nil
	}
}

type eofAfterSignalReader struct {
	data          []byte
	offset        int
	waitBeforeEOF <-chan struct{}
	waited        bool
}

func (reader *eofAfterSignalReader) Read(destination []byte) (int, error) {
	if reader.offset < len(reader.data) {
		written := copy(destination, reader.data[reader.offset:])
		reader.offset += written
		return written, nil
	}
	if !reader.waited {
		<-reader.waitBeforeEOF
		reader.waited = true
	}
	return 0, io.EOF
}

type lingeringEOFExecutor struct {
	started     chan struct{}
	startedOnce sync.Once
	release     chan struct{}
	releaseOnce sync.Once
	returned    chan struct{}
	returnOnce  sync.Once
	closed      atomic.Uint64
}

func newLingeringEOFExecutor() *lingeringEOFExecutor {
	return &lingeringEOFExecutor{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		returned: make(chan struct{}),
	}
}

func (executor *lingeringEOFExecutor) Call(_ context.Context, _ api.Call, work *workruntime.WorkLease) workruntime.Execution {
	executor.startedOnce.Do(func() { close(executor.started) })
	<-executor.release
	work.WorkerReturned()
	executor.returnOnce.Do(func() { close(executor.returned) })
	return workruntime.Execution{
		Kind:   workruntime.ExecutionOrdinary,
		Result: api.Navigation("DATA\tlate\n", false),
	}
}

func (executor *lingeringEOFExecutor) Close() {
	executor.closed.Add(1)
}

func (executor *lingeringEOFExecutor) releaseCall() {
	executor.releaseOnce.Do(func() { close(executor.release) })
}

type failWriteNumberWriter struct {
	mu     sync.Mutex
	writes int
	failAt int
	buffer bytes.Buffer
}

type blockingWriteNumberWriter struct {
	mu          sync.Mutex
	writes      int
	blockAt     int
	entered     chan struct{}
	enteredOnce sync.Once
	release     chan struct{}
	releaseOnce sync.Once
	buffer      bytes.Buffer
}

func newBlockingWriteNumberWriter(blockAt int) *blockingWriteNumberWriter {
	return &blockingWriteNumberWriter{
		blockAt: blockAt,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (writer *blockingWriteNumberWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	writer.writes++
	block := writer.writes == writer.blockAt
	writer.mu.Unlock()
	if block {
		writer.enteredOnce.Do(func() { close(writer.entered) })
		<-writer.release
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.buffer.Write(data)
}

func (writer *blockingWriteNumberWriter) releaseWrite() {
	writer.releaseOnce.Do(func() { close(writer.release) })
}

func (writer *blockingWriteNumberWriter) String() string {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.buffer.String()
}

func (writer *failWriteNumberWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.writes++
	if writer.writes == writer.failAt {
		return 0, errShutdownTestWrite
	}
	return writer.buffer.Write(data)
}
