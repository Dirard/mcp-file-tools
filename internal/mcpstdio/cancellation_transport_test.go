package mcpstdio

import (
	"bytes"
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

func TestCancellationUsesSemanticToolIDsAndLeavesLingeringWorkCharged(t *testing.T) {
	tests := []struct {
		name     string
		activeID string
		cancelID string
	}{
		{name: "numeric", activeID: `1e3`, cancelID: `1000.0`},
		{name: "decoded string", activeID: `"a\u0062"`, cancelID: `"ab"`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := newLingeringCancellationExecutor()
			instrumentation := &connectionInstrumentation{}
			var output bytes.Buffer
			connection := newAdmissionTestConnection(workruntime.Limits{
				MaxConcurrent: 1,
				QueueMax:      0,
				QueueTimeout:  time.Second,
			}, executor, instrumentation, &output)
			connection.lifecycle = newLifecycle()

			sendConnectionFrame(t, connection, initializeRequest(`"init"`, supportedProtocolVersion, `{}`))
			sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
			sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":2,"method":"ping"}`)
			awaitProtocolIdle(t, connection)
			sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)
			awaitProtocolIdle(t, connection)

			sendConnectionFrame(t, connection, fmt.Sprintf(
				`{"jsonrpc":"2.0","id":%s,"method":"tools/call","params":{"name":"read","arguments":{"path":"linger"}}}`,
				test.activeID,
			))
			awaitMCPStdIOSignal(t, executor.started, "active executor")
			if got := connectionRequestCount(connection); got != 1 {
				t.Fatalf("active request count = %d, want 1", got)
			}

			for _, frame := range []string{
				`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"init"}}`,
				`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":2}}`,
				`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":3}}`,
				`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"unknown"}}`,
				`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"reason":"missing"}}`,
				`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":null}}`,
			} {
				sendConnectionFrame(t, connection, frame)
			}
			if got := executor.owner.noCommit.Load(); got != 0 {
				t.Fatalf("non-tool cancellation marked work no-commit %d times", got)
			}

			sendConnectionFrame(t, connection, fmt.Sprintf(
				`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":%s,"reason":"stop"}}`,
				test.cancelID,
			))
			awaitMCPStdIOSignal(t, executor.contextCancelled, "reservation context cancellation")
			awaitMCPStdIOSignal(t, executor.owner.noCommitSignal, "lease no-commit notification")
			if got := connectionRequestCount(connection); got != 0 {
				t.Fatalf("transport request count after cancellation = %d, want 0", got)
			}
			sendConnectionFrame(t, connection, fmt.Sprintf(
				`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":%s,"reason":"repeat"}}`,
				test.cancelID,
			))
			if got := executor.owner.noCommit.Load(); got != 1 {
				t.Fatalf("lease no-commit calls = %d, want 1", got)
			}

			sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":40,"method":"tools/call","params":{"name":"read","arguments":{"path":"overflow"}}}`)
			if current := connectionOutput(connection, &output); !strings.Contains(current, `{"jsonrpc":"2.0","id":40,"result":{"content":[{"type":"text","text":"ERROR\tbudget_exceeded\n"}],"isError":true}}`) {
				t.Fatalf("lingering work did not retain its slot: %s", current)
			}

			executor.releaseFirst()
			connection.workers.Wait()
			if got := executor.owner.released.Load(); got != 1 {
				t.Fatalf("owner releases = %d, want 1", got)
			}
			sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":50,"method":"tools/call","params":{"name":"read","arguments":{"path":"reused"}}}`)
			connection.workers.Wait()

			current := connectionOutput(connection, &output)
			if strings.Contains(current, `"id":`+test.activeID+`,"result"`) {
				t.Fatalf("cancelled active request emitted a response: %s", current)
			}
			if !strings.Contains(current, `{"jsonrpc":"2.0","id":50,"result":{"content":[{"type":"text","text":"DATA\tok\n"}]}}`) {
				t.Fatalf("slot was not reusable after actual worker return: %s", current)
			}
			if calls := executor.calls.Load(); calls != 2 {
				t.Fatalf("executor calls = %d, want 2", calls)
			}
			if got := instrumentation.workLeases.Load(); got != 2 {
				t.Fatalf("work leases = %d, want 2", got)
			}
			sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":50,"reason":"completed"}}`)
			if calls := executor.calls.Load(); calls != 2 {
				t.Fatalf("completed cancellation changed executor calls to %d", calls)
			}
			if err := executor.transferError(); err != nil {
				t.Fatalf("work lease transfer: %v", err)
			}
			connection.closeExecutor()
			if got := executor.closed.Load(); got != 1 {
				t.Fatalf("executor closes = %d, want 1", got)
			}
		})
	}
}

func TestQueueTimeoutResponseRightSurvivesLateCancellationLookup(t *testing.T) {
	reservation := &timeoutWonReservation{ctx: context.Background()}
	request := &toolRequest{reservation: reservation}
	if !request.cancel() {
		t.Fatal("late cancellation lookup was not recorded")
	}
	if reservation.cancels.Load() != 1 {
		t.Fatalf("reservation cancellation calls = %d, want 1", reservation.cancels.Load())
	}
	if !request.claimTimeoutResponse() {
		t.Fatal("coordinator-issued timeout response right was suppressed by late cancellation")
	}
	if request.claimResponse() {
		t.Fatal("ordinary response right remained open after timeout response claim")
	}
	if request.cancel() {
		t.Fatal("cancellation won after timeout response claim")
	}
}

type timeoutWonReservation struct {
	ctx     context.Context
	cancels atomic.Uint64
}

func (*timeoutWonReservation) IDKey() []byte {
	return []byte("timeout")
}

func (reservation *timeoutWonReservation) Context() context.Context {
	return reservation.ctx
}

func (*timeoutWonReservation) Start() (*workruntime.WorkLease, workruntime.StartOutcome) {
	return nil, workruntime.StartOutcome{
		Kind:          workruntime.StartQueueTimeoutBudgetExceeded,
		ResponseRight: &workruntime.ResponseRight{},
	}
}

func (reservation *timeoutWonReservation) Cancel() {
	reservation.cancels.Add(1)
}

type cancellationLeaseOwner struct {
	noCommit       atomic.Uint64
	released       atomic.Uint64
	noCommitOnce   sync.Once
	noCommitSignal chan struct{}
}

func newCancellationLeaseOwner() *cancellationLeaseOwner {
	return &cancellationLeaseOwner{noCommitSignal: make(chan struct{})}
}

func (owner *cancellationLeaseOwner) MarkNoCommit() {
	owner.noCommit.Add(1)
	owner.noCommitOnce.Do(func() { close(owner.noCommitSignal) })
}

func (owner *cancellationLeaseOwner) Release() {
	owner.released.Add(1)
}

type lingeringCancellationExecutor struct {
	calls            atomic.Uint64
	closed           atomic.Uint64
	owner            *cancellationLeaseOwner
	started          chan struct{}
	contextCancelled chan struct{}
	firstRelease     chan struct{}
	startedOnce      sync.Once
	cancelledOnce    sync.Once
	releaseOnce      sync.Once
	transferMu       sync.Mutex
	transferErr      error
}

func newLingeringCancellationExecutor() *lingeringCancellationExecutor {
	return &lingeringCancellationExecutor{
		owner:            newCancellationLeaseOwner(),
		started:          make(chan struct{}),
		contextCancelled: make(chan struct{}),
		firstRelease:     make(chan struct{}),
	}
}

func (executor *lingeringCancellationExecutor) Call(ctx context.Context, _ api.Call, work *workruntime.WorkLease) workruntime.Execution {
	callNumber := executor.calls.Add(1)
	if callNumber == 1 {
		if err := work.Transfer(executor.owner); err != nil {
			executor.transferMu.Lock()
			executor.transferErr = err
			executor.transferMu.Unlock()
		}
		executor.startedOnce.Do(func() { close(executor.started) })
		<-ctx.Done()
		executor.cancelledOnce.Do(func() { close(executor.contextCancelled) })
		<-executor.firstRelease
	}
	work.WorkerReturned()
	return workruntime.Execution{Kind: workruntime.ExecutionOrdinary, Result: api.Navigation("DATA\tok\n", false)}
}

func (executor *lingeringCancellationExecutor) Close() {
	executor.closed.Add(1)
	executor.releaseFirst()
}

func (executor *lingeringCancellationExecutor) releaseFirst() {
	executor.releaseOnce.Do(func() { close(executor.firstRelease) })
}

func (executor *lingeringCancellationExecutor) transferError() error {
	executor.transferMu.Lock()
	defer executor.transferMu.Unlock()
	return executor.transferErr
}
