package runtime

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFatalSignalConvergesConcurrentPanicsWithoutEchoingPayload(t *testing.T) {
	t.Parallel()

	signal := NewFatalSignal()
	var cleanups atomic.Uint64
	start := make(chan struct{})
	finished := make(chan bool, 2)
	for _, payload := range []string{
		`/private/workspace/secret.txt`,
		`{"query":"do not echo this"}`,
	} {
		payload := payload
		go func() {
			<-start
			finished <- signal.Run(func() {
				panic(payload)
			}, func() {
				cleanups.Add(1)
			})
		}()
	}
	close(start)

	for range 2 {
		if panicked := <-finished; !panicked {
			t.Fatal("owned panic was not recovered")
		}
	}
	if cleanups.Load() != 2 {
		t.Fatalf("cleanup count = %d, want one per failed entry", cleanups.Load())
	}
	select {
	case <-signal.Done():
	default:
		t.Fatal("fatal signal was not emitted")
	}
	if !signal.Triggered() {
		t.Fatal("fatal signal does not report its terminal state")
	}
	for _, forbidden := range []string{"private", "secret.txt", "query", "do not echo"} {
		if strings.Contains(ErrInternalFatal.Error(), forbidden) {
			t.Fatalf("fatal sentinel echoes panic payload fragment %q", forbidden)
		}
	}
}

func TestFatalSignalLeavesNormalEntryAndCleanupUntouched(t *testing.T) {
	t.Parallel()

	signal := NewFatalSignal()
	var cleanups atomic.Uint64
	if panicked := signal.Run(func() {}, func() { cleanups.Add(1) }); panicked {
		t.Fatal("normal entry reported a panic")
	}
	if cleanups.Load() != 0 {
		t.Fatalf("normal entry cleanup count = %d, want 0", cleanups.Load())
	}
	select {
	case <-signal.Done():
		t.Fatal("normal entry emitted a fatal signal")
	default:
	}
}

func TestCoordinatorOwnedEntryUsesSharedFatalSignalAndCleanup(t *testing.T) {
	t.Parallel()

	signal := NewFatalSignal()
	coordinator := NewCoordinatorWithFatal(Limits{MaxConcurrent: 1}, signal)
	var cleanups atomic.Uint64
	if panicked := coordinator.runOwned(func() {
		panic(`/workspace/input-that-must-not-escape`)
	}, func() {
		cleanups.Add(1)
	}); !panicked {
		t.Fatal("coordinator entry panic was not recovered")
	}
	if cleanups.Load() != 1 {
		t.Fatalf("coordinator cleanup count = %d, want 1", cleanups.Load())
	}
	select {
	case <-signal.Done():
	default:
		t.Fatal("coordinator did not use the shared fatal signal")
	}
}

func TestCoordinatorTimerPanicCancelsWaiterWithoutNormalOutcome(t *testing.T) {
	t.Parallel()

	clock := &manualClock{}
	signal := NewFatalSignal()
	coordinator := newCoordinatorWithClockAndFatal(Limits{
		MaxConcurrent: 1,
		QueueMax:      1,
		QueueTimeout:  time.Second,
	}, clock, signal)

	active, admitted := coordinator.Admit(context.Background(), []byte("active"))
	if admitted != AdmitRun {
		t.Fatalf("active admission = %d, want run", admitted)
	}
	activeLease, outcome := active.Start()
	if outcome.Kind != StartRun || activeLease == nil {
		t.Fatalf("active start = (%p, %#v)", activeLease, outcome)
	}
	queued, admitted := coordinator.Admit(context.Background(), []byte("queued"))
	if admitted != AdmitQueued {
		t.Fatalf("queued admission = %d, want queued", admitted)
	}

	// Force an invariant panic inside the actual timer callback after it has
	// started mutating the waiter, proving the boundary owns terminal cleanup.
	concrete := queued.(*reservationState)
	coordinator.mu.Lock()
	concrete.request.cancel = nil
	coordinator.mu.Unlock()
	clock.singleTimer(t).fireCallback()

	select {
	case <-signal.Done():
	default:
		t.Fatal("timer panic emitted no fatal signal")
	}
	started := make(chan reservationStart, 1)
	go func() {
		lease, startOutcome := queued.Start()
		started <- reservationStart{lease: lease, outcome: startOutcome}
	}()
	result := awaitReservationStart(t, started)
	if result.lease != nil || result.outcome.Kind != StartCancelled {
		t.Fatalf("timer panic start outcome = (%p, %#v), want cancelled", result.lease, result.outcome)
	}
	assertCoordinatorCounts(t, coordinator, 1, 0)
	assertCoordinatorRequestCount(t, coordinator, 1)
	activeLease.WorkerReturned()
	assertCoordinatorCounts(t, coordinator, 0, 0)
	assertCoordinatorRequestCount(t, coordinator, 0)
}
