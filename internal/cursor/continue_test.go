package cursor

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/config"
	serverruntime "github.com/Dirard/mcp-file-tools/internal/runtime"
)

type testWaiter struct {
	cancelled chan struct{}
	delivered chan api.Result
	closed    chan struct{}
	closeOnce sync.Once
}

func newTestWaiter() *testWaiter {
	return &testWaiter{
		cancelled: make(chan struct{}),
		delivered: make(chan api.Result, 1),
		closed:    make(chan struct{}),
	}
}

func (waiter *testWaiter) Deliver(result api.Result)  { waiter.delivered <- result }
func (waiter *testWaiter) Cancelled() <-chan struct{} { return waiter.cancelled }
func (waiter *testWaiter) CloseWithoutResponse() {
	waiter.closeOnce.Do(func() { close(waiter.closed) })
}

func awaitResult(t *testing.T, waiter *testWaiter) api.Result {
	t.Helper()
	select {
	case result := <-waiter.delivered:
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for continuation result")
		return api.Result{}
	}
}

func assertWorkReturnedEventually(t *testing.T, coordinator *serverruntime.Coordinator, id string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		reservation, outcome := coordinator.Admit(context.Background(), []byte(id))
		if outcome == serverruntime.AdmitRun {
			lease, start := reservation.Start()
			if start.Kind != serverruntime.StartRun || lease == nil {
				t.Fatalf("eventual Start = (%p, %d)", lease, start.Kind)
			}
			lease.WorkerReturned()
			return
		}
		select {
		case <-deadline:
			t.Fatalf("work slot %q was not returned", id)
		case <-time.After(time.Millisecond):
		}
	}
}

func publishContinuationToken(t *testing.T, registry *Registry, state State, summaryBytes uint64, id string) Token {
	t.Helper()
	_, work := newWorkLease(t, id)
	commit, err := registry.CommitDynamicInitial(DynamicInitial{
		State:       state,
		SummaryPlan: ReservationUse{Kind: BroadSummaryFinal, Slots: 1, Bytes: summaryBytes},
	}, work)
	if err != nil {
		t.Fatalf("CommitDynamicInitial: %v", err)
	}
	if err := commit.Publication.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := commit.Publication.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return commit.Token
}

func TestContinueRunsOneComputationForConcurrentWaiters(t *testing.T) {
	cfg := config.DefaultRuntime()
	cfg.CursorMaxEntries = 16
	registry, clock := newTestRegistry(t, cfg)
	clock.now = time.Now()
	parent := newTestState(api.ToolProject, 21, "immutable-parent")
	token := publishContinuationToken(t, registry, parent, 64, "singleflight-initial")
	firstCoordinator, firstWork := newWorkLease(t, "singleflight-first")
	secondCoordinator, secondWork := newWorkLease(t, "singleflight-second")
	firstWaiter := newTestWaiter()
	secondWaiter := newTestWaiter()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	compute := func(_ context.Context, working State, _ Resources) Outcome {
		calls.Add(1)
		working.(*testState).payload[0] = 'X'
		close(started)
		<-release
		return Outcome{Result: api.Navigation("done\n", false)}
	}

	registry.Continue(context.Background(), token, api.ToolProject, 21, firstWaiter, compute, firstWork)
	<-started
	registry.Continue(context.Background(), token, api.ToolProject, 21, secondWaiter, compute, secondWork)
	close(release)
	first := awaitResult(t, firstWaiter)
	second := awaitResult(t, secondWaiter)
	firstText, _ := first.Text()
	secondText, _ := second.Text()
	if firstText != "done\n" || secondText != firstText || calls.Load() != 1 {
		t.Fatalf("results/calls = %q %q / %d", firstText, secondText, calls.Load())
	}
	if string(parent.payload) != "immutable-parent" {
		t.Fatalf("committed parent mutated: %q", parent.payload)
	}
	assertWorkReturnedEventually(t, firstCoordinator, "singleflight-first-next")
	assertWorkReturnedEventually(t, secondCoordinator, "singleflight-second-next")
}

func TestContinueMemoizesPartialResultForReplay(t *testing.T) {
	cfg := config.DefaultRuntime()
	cfg.CursorMaxEntries = 16
	registry, clock := newTestRegistry(t, cfg)
	clock.now = time.Now()
	parent := newTestState(api.ToolProject, 22, "parent")
	successor := newTestState(api.ToolProject, 22, "successor")
	placeholder := api.Navigation("next "+(Token{}).String()+"\n", false)
	memoBytes, err := resultFootprint(placeholder)
	if err != nil {
		t.Fatalf("resultFootprint: %v", err)
	}
	transitionBytes := successor.Footprint() + memoBytes
	token := publishContinuationToken(t, registry, parent, 64, "replay-initial")
	firstCoordinator, firstWork := newWorkLease(t, "replay-first")
	firstWaiter := newTestWaiter()
	childTokens := make(chan Token, 1)
	var calls atomic.Int32
	compute := func(_ context.Context, _ State, _ Resources) Outcome {
		calls.Add(1)
		return Outcome{
			Successor:   successor,
			Reservation: ReservationUse{Kind: BroadSummaryFinal, Bytes: transitionBytes},
			Progress:    ProgressProof{Kind: ProgressOutputKey, BeforeValue: 1, AfterValue: 2},
			Finalize: func(child Token) (api.Result, error) {
				childTokens <- child
				return api.Navigation("next "+child.String()+"\n", false), nil
			},
		}
	}
	registry.Continue(context.Background(), token, api.ToolProject, 22, firstWaiter, compute, firstWork)
	first := awaitResult(t, firstWaiter)
	child := <-childTokens
	firstText, _ := first.Text()
	if firstText != "next "+child.String()+"\n" {
		t.Fatalf("first result = %q", firstText)
	}

	secondCoordinator, secondWork := newWorkLease(t, "replay-second")
	secondWaiter := newTestWaiter()
	registry.Continue(context.Background(), token, api.ToolProject, 22, secondWaiter, func(context.Context, State, Resources) Outcome {
		calls.Add(1)
		return Outcome{Result: api.Navigation("unexpected\n", false)}
	}, secondWork)
	second := awaitResult(t, secondWaiter)
	secondText, _ := second.Text()
	if secondText != firstText || calls.Load() != 1 {
		t.Fatalf("replay result/calls = %q / %d", secondText, calls.Load())
	}
	if _, code := registry.Lookup(child); code != "" {
		t.Fatalf("child Lookup code = %q", code)
	}
	assertWorkReturnedEventually(t, firstCoordinator, "replay-first-next")
	assertWorkReturnedEventually(t, secondCoordinator, "replay-second-next")
}

func TestContinueEnforcesDynamicPageLimit(t *testing.T) {
	for index, tool := range []api.ToolName{api.ToolProject, api.ToolSearch} {
		t.Run(string(tool), func(t *testing.T) {
			cfg := config.DefaultRuntime()
			cfg.CursorMaxEntries = 16
			cfg.CursorMaxPages = 2
			registry, clock := newTestRegistry(t, cfg)
			clock.now = time.Now()
			cwdID := uint64(30 + index)
			parent := newTestState(tool, cwdID, "parent")
			successor := newTestState(tool, cwdID, "successor")
			placeholder := api.Navigation("next "+(Token{}).String()+"\n", false)
			transitionBytes, err := SuccessorBytes(successor, placeholder)
			if err != nil {
				t.Fatalf("SuccessorBytes: %v", err)
			}
			token := publishContinuationToken(t, registry, parent, 64, "page-limit-initial")
			coordinator, work := newWorkLease(t, "page-limit-continue")
			waiter := newTestWaiter()
			children := make(chan Token, 1)
			registry.Continue(context.Background(), token, tool, cwdID, waiter, func(context.Context, State, Resources) Outcome {
				return Outcome{
					Successor:   successor,
					Reservation: ReservationUse{Kind: BroadSummaryFinal, Bytes: transitionBytes},
					Progress:    ProgressProof{Kind: ProgressOutputKey, BeforeValue: 1, AfterValue: 2},
					Finalize: func(child Token) (api.Result, error) {
						children <- child
						return api.Navigation("next "+child.String()+"\n", false), nil
					},
				}
			}, work)
			result := awaitResult(t, waiter)
			text, _ := result.Text()
			if !result.IsError() || text != "ERROR\tbudget_exceeded\n" {
				t.Fatalf("page-limit result = (%t, %q)", result.IsError(), text)
			}
			child := <-children
			if _, code := registry.Lookup(child); code != api.ErrorCursorExpired {
				t.Fatalf("unpublished child lookup code = %q", code)
			}
			assertWorkReturnedEventually(t, coordinator, "page-limit-next")
		})
	}
}

func TestCancellingOneWaiterDoesNotCancelSharedComputation(t *testing.T) {
	cfg := config.DefaultRuntime()
	cfg.CursorMaxEntries = 16
	registry, clock := newTestRegistry(t, cfg)
	clock.now = time.Now()
	token := publishContinuationToken(t, registry, newTestState(api.ToolSearch, 23, "parent"), 64, "cancel-one-initial")
	firstCoordinator, firstWork := newWorkLease(t, "cancel-one-first")
	secondCoordinator, secondWork := newWorkLease(t, "cancel-one-second")
	blockerCoordinator, blockerWork := newWorkLease(t, "cancel-one-blocker")
	limiter := serverruntime.NewSubLimiter(1)
	blockerLease, blockerOutcome := limiter.Acquire(context.Background(), time.Now().Add(2*time.Second), blockerWork)
	if blockerOutcome != serverruntime.SubAcquired {
		t.Fatalf("blocker Acquire outcome = %d", blockerOutcome)
	}
	firstWaiter := newTestWaiter()
	secondWaiter := newTestWaiter()
	started := make(chan struct{})
	var calls atomic.Int32
	compute := func(ctx context.Context, _ State, _ Resources) Outcome {
		calls.Add(1)
		close(started)
		lease, outcome := limiter.Acquire(ctx, time.Now().Add(2*time.Second), firstWork)
		if outcome != serverruntime.SubAcquired {
			return Outcome{Result: api.Navigation("sub-work cancelled\n", false)}
		}
		lease.WorkerReturned()
		return Outcome{Result: api.Navigation("survived\n", false)}
	}
	registry.Continue(context.Background(), token, api.ToolSearch, 23, firstWaiter, compute, firstWork)
	<-started
	registry.Continue(context.Background(), token, api.ToolSearch, 23, secondWaiter, compute, secondWork)
	firstWork.MarkNoCommit()
	select {
	case <-firstWaiter.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled waiter was not closed")
	}
	blockerLease.WorkerReturned()
	result := awaitResult(t, secondWaiter)
	text, _ := result.Text()
	if text != "survived\n" || calls.Load() != 1 {
		t.Fatalf("remaining result/calls = %q / %d", text, calls.Load())
	}
	select {
	case result := <-firstWaiter.delivered:
		t.Fatalf("cancelled waiter received %#v", result)
	default:
	}
	assertWorkReturnedEventually(t, firstCoordinator, "cancel-one-first-next")
	assertWorkReturnedEventually(t, secondCoordinator, "cancel-one-second-next")
	blockerWork.WorkerReturned()
	assertWorkReturnedEventually(t, blockerCoordinator, "cancel-one-blocker-next")
}

func TestLastWaiterCancellationRetainsAccountingUntilWorkerReturns(t *testing.T) {
	cfg := config.DefaultRuntime()
	cfg.CursorMaxEntries = 16
	registry, clock := newTestRegistry(t, cfg)
	clock.now = time.Now()
	token := publishContinuationToken(t, registry, newTestState(api.ToolSearch, 24, "parent"), 64, "cancel-last-initial")
	coordinator, work := newWorkLease(t, "cancel-last")
	waiter := newTestWaiter()
	started := make(chan struct{})
	release := make(chan struct{})
	registry.Continue(context.Background(), token, api.ToolSearch, 24, waiter, func(_ context.Context, _ State, _ Resources) Outcome {
		close(started)
		<-release
		return Outcome{Result: api.Navigation("late\n", false)}
	}, work)
	<-started
	close(waiter.cancelled)
	select {
	case <-waiter.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("last waiter was not closed")
	}
	if _, code := registry.Lookup(token); code != api.ErrorCursorExpired {
		t.Fatalf("Lookup after last cancellation code = %q", code)
	}
	registry.mu.Lock()
	retained := registry.totalBytes > registry.baseBytes
	registry.mu.Unlock()
	if !retained {
		t.Fatal("cursor accounting was released before worker return")
	}
	close(release)
	deadline := time.After(2 * time.Second)
	for {
		registry.mu.Lock()
		clean := registry.totalBytes == registry.baseBytes && registry.usedSlots == 0
		registry.mu.Unlock()
		if clean {
			break
		}
		select {
		case <-deadline:
			t.Fatal("worker return did not clean tombstoned lineage")
		case <-time.After(time.Millisecond):
		}
	}
	assertWorkReturnedEventually(t, coordinator, "cancel-last-next")
}

func TestCommitDeadlineRespondsBeforeUncooperativeWorkerCleanup(t *testing.T) {
	cfg := config.DefaultRuntime()
	cfg.CursorMaxEntries = 16
	registry, clock := newTestRegistry(t, cfg)
	clock.now = time.Now()
	token := publishContinuationToken(t, registry, newTestState(api.ToolProject, 25, "deadline-parent"), 64, "deadline-initial")
	registry.mu.Lock()
	entry, code := registry.lookupScopedLocked(token, api.ToolProject, 25, registry.clockNowLocked())
	if code != "" {
		registry.mu.Unlock()
		t.Fatalf("lookup before deadline code = %q", code)
	}
	lineage := registry.entries.lineage[entry.index]
	registry.lineages.commitDeadline[lineage] = time.Now().Add(20 * time.Millisecond).UnixNano()
	registry.mu.Unlock()

	coordinator, work := newWorkLease(t, "deadline-work")
	waiter := newTestWaiter()
	started := make(chan struct{})
	release := make(chan struct{})
	registry.Continue(context.Background(), token, api.ToolProject, 25, waiter, func(_ context.Context, _ State, _ Resources) Outcome {
		close(started)
		<-release
		return Outcome{Result: api.Navigation("too late\n", false)}
	}, work)
	<-started
	result := awaitResult(t, waiter)
	text, _ := result.Text()
	if text != "ERROR\tcursor_expired\n" {
		t.Fatalf("deadline result = %q", text)
	}
	registry.mu.Lock()
	retained := registry.totalBytes > registry.baseBytes && registry.lineages.pins[lineage] == 1
	registry.mu.Unlock()
	if !retained {
		t.Fatal("deadline released cursor accounting before worker return")
	}
	close(release)
	deadline := time.After(2 * time.Second)
	for {
		registry.mu.Lock()
		clean := registry.totalBytes == registry.baseBytes && registry.usedSlots == 0
		registry.mu.Unlock()
		if clean {
			break
		}
		select {
		case <-deadline:
			t.Fatal("late worker return did not clean deadline tombstone")
		case <-time.After(time.Millisecond):
		}
	}
	assertWorkReturnedEventually(t, coordinator, "deadline-next")
}
