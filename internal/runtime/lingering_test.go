package runtime

import (
	"context"
	"testing"
	"time"
)

func TestCancelledUncooperativeWorkerRemainsChargedUntilReturn(t *testing.T) {
	coordinator := NewCoordinator(Limits{
		MaxConcurrent: 1,
		QueueMax:      0,
		QueueTimeout:  time.Second,
	})
	reservation, admitted := coordinator.Admit(context.Background(), []byte("lingering"))
	if admitted != AdmitRun {
		t.Fatalf("admission = %d, want run", admitted)
	}
	lease, started := reservation.Start()
	if started.Kind != StartRun || lease == nil {
		t.Fatalf("start = (%p, %#v), want work lease", lease, started)
	}

	workerStarted := make(chan struct{})
	unblockWorker := make(chan struct{})
	workerReturned := make(chan struct{})
	go func() {
		close(workerStarted)
		<-unblockWorker
		lease.WorkerReturned()
		lease.WorkerReturned()
		close(workerReturned)
	}()
	awaitSignal(t, workerStarted, "worker start")

	reservation.Cancel()
	select {
	case <-reservation.Context().Done():
	default:
		t.Fatal("model-visible request remained open after cancellation")
	}
	assertCoordinatorCounts(t, coordinator, 1, 0)
	assertCoordinatorRequestCount(t, coordinator, 1)

	concrete := reservation.(*reservationState)
	coordinator.mu.Lock()
	state := concrete.request.state
	coordinator.mu.Unlock()
	if state != Lingering {
		t.Fatalf("request state = %d, want lingering", state)
	}
	lease.mu.Lock()
	noCommit, returned := lease.noCommit, lease.returned
	lease.mu.Unlock()
	if !noCommit || returned {
		t.Fatalf("lease state = noCommit %v returned %v, want true/false", noCommit, returned)
	}

	overflow, overflowOutcome := coordinator.Admit(context.Background(), []byte("while-lingering"))
	if overflowOutcome != AdmitImmediateBudgetExceeded || overflow != nil {
		t.Fatalf("lingering overflow = (%T, %d), want immediate budget exceeded", overflow, overflowOutcome)
	}

	close(unblockWorker)
	awaitSignal(t, workerReturned, "worker return")
	assertCoordinatorCounts(t, coordinator, 0, 0)
	assertCoordinatorRequestCount(t, coordinator, 0)

	next, nextOutcome := coordinator.Admit(context.Background(), []byte("after-return"))
	if nextOutcome != AdmitRun || next == nil {
		t.Fatalf("post-return admission = (%T, %d), want run", next, nextOutcome)
	}
	nextLease, nextStart := next.Start()
	if nextStart.Kind != StartRun || nextLease == nil {
		t.Fatalf("post-return start = (%p, %#v)", nextLease, nextStart)
	}
	nextLease.WorkerReturned()
}

func awaitSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}
