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

var (
	errTestPublicationPrepare = errors.New("prepare failed")
	errTestPublicationCommit  = errors.New("commit failed")
	errTestPublicationWrite   = errors.New("write failed")
)

func TestPublicationPhasesFollowResponseOutcome(t *testing.T) {
	tests := []struct {
		name          string
		config        publicationTestConfig
		writerMode    publicationWriterMode
		wantPhases    string
		wantAsyncErr  bool
		wantFatal     bool
		wantOutput    bool
		wantVisibleAt bool
	}{
		{
			name:          "full prepare write commit",
			config:        publicationTestConfig{result: api.Navigation("DATA\tpage\n", false)},
			wantPhases:    "prepare,commit",
			wantOutput:    true,
			wantVisibleAt: true,
		},
		{
			name:         "encoder failure before prepare",
			config:       publicationTestConfig{result: api.Result{}},
			wantPhases:   "abort",
			wantAsyncErr: true,
		},
		{
			name:         "abort panic",
			config:       publicationTestConfig{result: api.Result{}, panicAbort: true},
			wantPhases:   "abort",
			wantAsyncErr: true,
			wantFatal:    true,
		},
		{
			name:         "prepare error",
			config:       publicationTestConfig{result: api.Navigation("DATA\tpage\n", false), prepareErr: errTestPublicationPrepare},
			wantPhases:   "prepare,abort",
			wantAsyncErr: true,
		},
		{
			name:         "prepare panic",
			config:       publicationTestConfig{result: api.Navigation("DATA\tpage\n", false), panicPrepare: true},
			wantPhases:   "prepare,abort",
			wantAsyncErr: true,
			wantFatal:    true,
		},
		{
			name:          "short writer after prepare",
			config:        publicationTestConfig{result: api.Navigation("DATA\tpage\n", false)},
			writerMode:    publicationWriterShort,
			wantPhases:    "prepare,abort",
			wantAsyncErr:  true,
			wantVisibleAt: true,
		},
		{
			name:          "writer error after prepare",
			config:        publicationTestConfig{result: api.Navigation("DATA\tpage\n", false)},
			writerMode:    publicationWriterError,
			wantPhases:    "prepare,abort",
			wantAsyncErr:  true,
			wantVisibleAt: true,
		},
		{
			name:          "commit error after complete line",
			config:        publicationTestConfig{result: api.Navigation("DATA\tpage\n", false), commitErr: errTestPublicationCommit},
			wantPhases:    "prepare,commit",
			wantAsyncErr:  true,
			wantOutput:    true,
			wantVisibleAt: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := newPublicationTestExecutor(test.config)
			writer := &publicationObservingWriter{executor: executor, mode: test.writerMode}
			connection := newPublicationTestConnection(executor, writer)

			sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read","arguments":{"path":"page"}}}`)
			connection.workers.Wait()
			publication := executor.awaitPublication(t)
			snapshot := publication.snapshot()

			if got := strings.Join(snapshot.phases, ","); got != test.wantPhases {
				t.Fatalf("publication phases = %q, want %q", got, test.wantPhases)
			}
			if snapshot.violation != "" {
				t.Fatalf("publication phase violation: %s", snapshot.violation)
			}
			if got := publication.released.Load(); got != 1 {
				t.Fatalf("publication owner releases = %d, want 1", got)
			}
			if got := connection.loadAsyncError() != nil; got != test.wantAsyncErr {
				t.Fatalf("async error present = %v, want %v: %v", got, test.wantAsyncErr, connection.loadAsyncError())
			}
			if got := connection.fatal.Triggered(); got != test.wantFatal {
				t.Fatalf("fatal signal = %v, want %v", got, test.wantFatal)
			}
			if got := writer.Len() != 0; got != test.wantOutput {
				t.Fatalf("wire output present = %v, want %v: %q", got, test.wantOutput, writer.String())
			}
			if got := writer.visibleAtWrite.Load(); got != test.wantVisibleAt {
				t.Fatalf("publication visible at first stdout byte = %v, want %v", got, test.wantVisibleAt)
			}
			if got := connectionRequestCount(connection); got != 0 {
				t.Fatalf("request count = %d, want 0", got)
			}
			if err := executor.transferError(); err != nil {
				t.Fatalf("publication lease transfer: %v", err)
			}
			connection.closeExecutor()
		})
	}
}

func TestPublicationCancellationBeforeExecutorReturnAbortsWithoutResponse(t *testing.T) {
	executor := newPublicationTestExecutor(publicationTestConfig{
		result:      api.Navigation("DATA\tpage\n", false),
		blockReturn: true,
	})
	writer := &publicationObservingWriter{executor: executor}
	connection := newPublicationTestConnection(executor, writer)

	sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"read","arguments":{"path":"page"}}}`)
	publication := executor.awaitPublication(t)
	sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":7}}`)
	awaitMCPStdIOSignal(t, publication.noCommitSignal, "publication no-commit")
	if got := connectionRequestCount(connection); got != 0 {
		t.Fatalf("request count after cancellation = %d, want 0", got)
	}
	executor.releaseReturn()
	connection.workers.Wait()

	snapshot := publication.snapshot()
	if got := strings.Join(snapshot.phases, ","); got != "abort" {
		t.Fatalf("publication phases = %q, want abort", got)
	}
	if snapshot.noCommit != 1 || publication.released.Load() != 1 {
		t.Fatalf("publication no-commit/releases = %d/%d, want 1/1", snapshot.noCommit, publication.released.Load())
	}
	if writer.Len() != 0 {
		t.Fatalf("cancelled publication wrote a response: %q", writer.String())
	}
	if err := connection.loadAsyncError(); err != nil {
		t.Fatalf("cancelled publication returned async error: %v", err)
	}
	connection.closeExecutor()
}

func TestPublicationResponseClaimMakesLaterCancellationANoop(t *testing.T) {
	executor := newPublicationTestExecutor(publicationTestConfig{
		result:       api.Navigation("DATA\tpage\n", false),
		blockPrepare: true,
	})
	writer := &publicationObservingWriter{executor: executor}
	connection := newPublicationTestConnection(executor, writer)

	sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"read","arguments":{"path":"page"}}}`)
	publication := executor.awaitPublication(t)
	awaitMCPStdIOSignal(t, publication.prepareEntered, "publication prepare")
	if publication.visible.Load() {
		t.Fatal("publication became lookup-visible before Prepare completed")
	}
	if got := connectionRequestCount(connection); got != 0 {
		t.Fatalf("request count after response claim = %d, want 0", got)
	}
	sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":9}}`)
	publication.releasePrepare()
	connection.workers.Wait()

	snapshot := publication.snapshot()
	if got := strings.Join(snapshot.phases, ","); got != "prepare,commit" {
		t.Fatalf("publication phases = %q, want prepare,commit", got)
	}
	if snapshot.noCommit != 0 || publication.released.Load() != 1 {
		t.Fatalf("publication no-commit/releases = %d/%d, want 0/1", snapshot.noCommit, publication.released.Load())
	}
	if writer.Len() == 0 || !writer.visibleAtWrite.Load() {
		t.Fatalf("claimed publication was not visible before its complete response: %q", writer.String())
	}
	connection.closeExecutor()
}

func TestEarlierWriterFailureAbortsPendingPublication(t *testing.T) {
	executor := newPendingPublicationExecutor()
	writer := newBlockingPublicationErrorWriter()
	connection := newPublicationTestConnection(executor, writer)

	sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read","arguments":{"path":"first"}}}`)
	awaitMCPStdIOSignal(t, writer.entered, "first response write")
	sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"read","arguments":{"path":"published"}}}`)
	publication := executor.awaitPublication(t)
	awaitConnectionRequestCount(t, connection, 0)
	writer.release()
	connection.workers.Wait()

	snapshot := publication.snapshot()
	if got := strings.Join(snapshot.phases, ","); got != "abort" && got != "prepare,abort" {
		t.Fatalf("pending publication phases = %q, want abort with optional raced prepare", got)
	}
	if snapshot.violation != "" || publication.released.Load() != 1 || publication.visible.Load() {
		t.Fatalf("pending publication violation/releases/visible = %q/%d/%v", snapshot.violation, publication.released.Load(), publication.visible.Load())
	}
	if err := connection.loadAsyncError(); !errors.Is(err, errTestPublicationWrite) {
		t.Fatalf("async error = %v, want writer error", err)
	}
	connection.closeExecutor()
}

func TestExecutionPublicationMismatchIsFatalBeforeWrite(t *testing.T) {
	tests := []struct {
		name                  string
		kind                  workruntime.ExecutionKind
		withPublication       bool
		wantPublicationPhases string
	}{
		{
			name:                  "ordinary result with publication",
			kind:                  workruntime.ExecutionOrdinary,
			withPublication:       true,
			wantPublicationPhases: "abort",
		},
		{
			name: "initial cursor without publication",
			kind: workruntime.ExecutionInitialCursor,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := newPublicationMismatchExecutor(test.kind, test.withPublication)
			var output bytes.Buffer
			connection := newPublicationTestConnection(executor, &output)

			sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read","arguments":{"path":"page"}}}`)
			connection.workers.Wait()

			if !connection.fatal.Triggered() {
				t.Fatal("publication mismatch emitted no fatal signal")
			}
			if !errors.Is(connection.loadAsyncError(), workruntime.ErrInternalFatal) {
				t.Fatalf("async error = %v, want internal fatal", connection.loadAsyncError())
			}
			if output.Len() != 0 {
				t.Fatalf("publication mismatch wrote output: %q", output.String())
			}
			if publication := executor.publicationSnapshot(); publication != nil {
				if got := strings.Join(publication.phases, ","); got != test.wantPublicationPhases {
					t.Fatalf("publication phases = %q, want %q", got, test.wantPublicationPhases)
				}
			}
			connection.closeExecutor()
		})
	}
}

type publicationTestConfig struct {
	result       api.Result
	prepareErr   error
	commitErr    error
	panicPrepare bool
	panicAbort   bool
	blockReturn  bool
	blockPrepare bool
}

type publicationTestExecutor struct {
	config      publicationTestConfig
	created     chan struct{}
	createdOnce sync.Once
	returnGate  chan struct{}
	returnOnce  sync.Once
	mu          sync.Mutex
	publication *fakePublication
	transferErr error
	closed      atomic.Uint64
}

type pendingPublicationExecutor struct {
	calls       atomic.Uint64
	created     chan struct{}
	createdOnce sync.Once
	mu          sync.Mutex
	publication *fakePublication
}

type publicationMismatchExecutor struct {
	kind            workruntime.ExecutionKind
	withPublication bool
	mu              sync.Mutex
	publication     *fakePublication
}

func newPublicationMismatchExecutor(kind workruntime.ExecutionKind, withPublication bool) *publicationMismatchExecutor {
	return &publicationMismatchExecutor{kind: kind, withPublication: withPublication}
}

func (executor *publicationMismatchExecutor) Call(_ context.Context, _ api.Call, work *workruntime.WorkLease) workruntime.Execution {
	execution := workruntime.Execution{
		Kind:   executor.kind,
		Result: api.Navigation("DATA\tpage\n", false),
	}
	if !executor.withPublication {
		work.WorkerReturned()
		return execution
	}
	publication := newFakePublication(work, publicationTestConfig{result: execution.Result})
	if err := work.Transfer(publication); err != nil {
		work.WorkerReturned()
		panic("mismatch publication lease transfer failed")
	}
	executor.mu.Lock()
	executor.publication = publication
	executor.mu.Unlock()
	execution.Publication = publication
	return execution
}

func (*publicationMismatchExecutor) Close() {}

func (executor *publicationMismatchExecutor) publicationSnapshot() *fakePublicationSnapshot {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if executor.publication == nil {
		return nil
	}
	snapshot := executor.publication.snapshot()
	return &snapshot
}

func newPendingPublicationExecutor() *pendingPublicationExecutor {
	return &pendingPublicationExecutor{created: make(chan struct{})}
}

func (executor *pendingPublicationExecutor) Call(_ context.Context, _ api.Call, work *workruntime.WorkLease) workruntime.Execution {
	if executor.calls.Add(1) == 1 {
		work.WorkerReturned()
		return workruntime.Execution{Kind: workruntime.ExecutionOrdinary, Result: api.Navigation("DATA\tfirst\n", false)}
	}
	publication := newFakePublication(work, publicationTestConfig{result: api.Navigation("DATA\tpublished\n", false)})
	if err := work.Transfer(publication); err != nil {
		work.WorkerReturned()
		panic("pending publication lease transfer failed")
	}
	executor.mu.Lock()
	executor.publication = publication
	executor.mu.Unlock()
	executor.createdOnce.Do(func() { close(executor.created) })
	return workruntime.Execution{Kind: workruntime.ExecutionInitialCursor, Result: api.Navigation("DATA\tpublished\n", false), Publication: publication}
}

func (*pendingPublicationExecutor) Close() {}

func (executor *pendingPublicationExecutor) awaitPublication(t *testing.T) *fakePublication {
	t.Helper()
	awaitMCPStdIOSignal(t, executor.created, "pending publication creation")
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return executor.publication
}

func newPublicationTestExecutor(config publicationTestConfig) *publicationTestExecutor {
	return &publicationTestExecutor{
		config:     config,
		created:    make(chan struct{}),
		returnGate: make(chan struct{}),
	}
}

func (executor *publicationTestExecutor) Call(_ context.Context, _ api.Call, work *workruntime.WorkLease) workruntime.Execution {
	publication := newFakePublication(work, executor.config)
	transferErr := work.Transfer(publication)
	executor.mu.Lock()
	executor.publication = publication
	executor.transferErr = transferErr
	executor.mu.Unlock()
	executor.createdOnce.Do(func() { close(executor.created) })
	if transferErr != nil {
		work.WorkerReturned()
		return workruntime.Execution{Kind: workruntime.ExecutionOrdinary, Result: executor.config.result}
	}
	if executor.config.blockReturn {
		<-executor.returnGate
	}
	return workruntime.Execution{Kind: workruntime.ExecutionInitialCursor, Result: executor.config.result, Publication: publication}
}

func (executor *publicationTestExecutor) Close() {
	executor.closed.Add(1)
	executor.releaseReturn()
}

func (executor *publicationTestExecutor) awaitPublication(t *testing.T) *fakePublication {
	t.Helper()
	awaitMCPStdIOSignal(t, executor.created, "publication creation")
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return executor.publication
}

func (executor *publicationTestExecutor) currentPublication() *fakePublication {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return executor.publication
}

func (executor *publicationTestExecutor) transferError() error {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return executor.transferErr
}

func (executor *publicationTestExecutor) releaseReturn() {
	executor.returnOnce.Do(func() { close(executor.returnGate) })
}

type fakePublication struct {
	work            *workruntime.WorkLease
	config          publicationTestConfig
	visible         atomic.Bool
	released        atomic.Uint64
	prepareEntered  chan struct{}
	prepareOnce     sync.Once
	prepareContinue chan struct{}
	prepareRelease  sync.Once
	noCommitSignal  chan struct{}
	noCommitOnce    sync.Once
	mu              sync.Mutex
	phases          []string
	prepared        bool
	terminal        bool
	noCommit        uint64
	violation       string
}

type fakePublicationSnapshot struct {
	phases    []string
	noCommit  uint64
	violation string
}

func newFakePublication(work *workruntime.WorkLease, config publicationTestConfig) *fakePublication {
	return &fakePublication{
		work:            work,
		config:          config,
		prepareEntered:  make(chan struct{}),
		prepareContinue: make(chan struct{}),
		noCommitSignal:  make(chan struct{}),
	}
}

func (publication *fakePublication) Prepare() error {
	publication.mu.Lock()
	publication.phases = append(publication.phases, "prepare")
	if publication.prepared || publication.terminal {
		publication.violation = "duplicate or terminal prepare"
	}
	publication.mu.Unlock()
	publication.prepareOnce.Do(func() { close(publication.prepareEntered) })
	if publication.config.panicPrepare {
		panic("publication prepare panic")
	}
	if publication.config.prepareErr != nil {
		return publication.config.prepareErr
	}
	if publication.config.blockPrepare {
		<-publication.prepareContinue
	}
	publication.mu.Lock()
	publication.prepared = true
	publication.mu.Unlock()
	publication.visible.Store(true)
	return nil
}

func (publication *fakePublication) Commit() error {
	publication.mu.Lock()
	publication.phases = append(publication.phases, "commit")
	if !publication.prepared || publication.terminal {
		publication.violation = "commit outside prepared state"
	}
	publication.terminal = true
	publication.mu.Unlock()
	publication.work.WorkerReturned()
	return publication.config.commitErr
}

func (publication *fakePublication) Abort() {
	publication.mu.Lock()
	publication.phases = append(publication.phases, "abort")
	if publication.terminal {
		publication.violation = "abort after terminal phase"
	}
	publication.terminal = true
	publication.mu.Unlock()
	publication.visible.Store(false)
	publication.work.WorkerReturned()
	if publication.config.panicAbort {
		panic("publication abort panic")
	}
}

func (publication *fakePublication) MarkNoCommit() {
	publication.mu.Lock()
	publication.noCommit++
	publication.mu.Unlock()
	publication.noCommitOnce.Do(func() { close(publication.noCommitSignal) })
}

func (publication *fakePublication) Release() {
	publication.released.Add(1)
}

func (publication *fakePublication) releasePrepare() {
	publication.prepareRelease.Do(func() { close(publication.prepareContinue) })
}

func (publication *fakePublication) snapshot() fakePublicationSnapshot {
	publication.mu.Lock()
	defer publication.mu.Unlock()
	return fakePublicationSnapshot{
		phases:    append([]string(nil), publication.phases...),
		noCommit:  publication.noCommit,
		violation: publication.violation,
	}
}

type publicationWriterMode uint8

const (
	publicationWriterOK publicationWriterMode = iota
	publicationWriterShort
	publicationWriterError
)

type publicationObservingWriter struct {
	executor       *publicationTestExecutor
	mode           publicationWriterMode
	visibleAtWrite atomic.Bool
	mu             sync.Mutex
	buffer         bytes.Buffer
}

type blockingPublicationErrorWriter struct {
	entered     chan struct{}
	releaseGate chan struct{}
	enteredOnce sync.Once
	releaseOnce sync.Once
}

func newBlockingPublicationErrorWriter() *blockingPublicationErrorWriter {
	return &blockingPublicationErrorWriter{
		entered:     make(chan struct{}),
		releaseGate: make(chan struct{}),
	}
}

func (writer *blockingPublicationErrorWriter) Write([]byte) (int, error) {
	writer.enteredOnce.Do(func() { close(writer.entered) })
	<-writer.releaseGate
	return 0, errTestPublicationWrite
}

func (writer *blockingPublicationErrorWriter) release() {
	writer.releaseOnce.Do(func() { close(writer.releaseGate) })
}

func (writer *publicationObservingWriter) Write(data []byte) (int, error) {
	if publication := writer.executor.currentPublication(); publication != nil && publication.visible.Load() {
		writer.visibleAtWrite.Store(true)
	}
	switch writer.mode {
	case publicationWriterShort:
		return 0, nil
	case publicationWriterError:
		return 0, errTestPublicationWrite
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.buffer.Write(data)
}

func (writer *publicationObservingWriter) Len() int {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.buffer.Len()
}

func (writer *publicationObservingWriter) String() string {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.buffer.String()
}

func newPublicationTestConnection(executor CallExecutor, output io.Writer) *stdioConnection {
	fatal := workruntime.NewFatalSignal()
	limits := workruntime.Limits{MaxConcurrent: 2, QueueMax: 2, QueueTimeout: time.Second}
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
		instrumentation: &connectionInstrumentation{},
	}
}
