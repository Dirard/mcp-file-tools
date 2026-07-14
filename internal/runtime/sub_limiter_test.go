package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubLimiterEnforcesConfiguredLimits(t *testing.T) {
	t.Parallel()

	for _, maximum := range []uint64{1, 8, 64} {
		maximum := maximum
		t.Run(fmt.Sprintf("max-%d", maximum), func(t *testing.T) {
			limiter := NewSubLimiter(maximum)
			deadline := time.Now().Add(time.Minute)
			leases := make([]*SubLease, 0, maximum)
			parentLeases := make([]*WorkLease, 0, maximum+1)
			parents := make([]*subParentProbe, 0, maximum+1)
			for index := uint64(0); index < maximum; index++ {
				parent, probe := newSubParentProbe()
				lease, outcome := limiter.Acquire(context.Background(), deadline, parent)
				if outcome != SubAcquired || lease == nil {
					t.Fatalf("acquire %d = (%p, %d), want acquired", index, lease, outcome)
				}
				leases = append(leases, lease)
				parentLeases = append(parentLeases, parent)
				parents = append(parents, probe)
			}
			assertSubLimiterCounts(t, limiter, maximum, 0)

			queuedParent, queuedProbe := newSubParentProbe()
			parentLeases = append(parentLeases, queuedParent)
			parents = append(parents, queuedProbe)
			queued := make(chan subAcquireResult, 1)
			go func() {
				lease, outcome := limiter.Acquire(context.Background(), deadline, queuedParent)
				queued <- subAcquireResult{lease: lease, outcome: outcome}
			}()
			awaitSubLimiterCounts(t, limiter, maximum, 1)

			leases[0].WorkerReturned()
			promoted := awaitSubAcquire(t, queued)
			if promoted.outcome != SubAcquired || promoted.lease == nil {
				t.Fatalf("promoted acquire = (%p, %d), want acquired", promoted.lease, promoted.outcome)
			}
			for _, lease := range leases[1:] {
				lease.WorkerReturned()
			}
			promoted.lease.WorkerReturned()
			assertSubLimiterCounts(t, limiter, 0, 0)
			for index, probe := range parents {
				if probe.releases.Load() != 0 {
					t.Fatalf("parent %d released with sub-slot: %d", index, probe.releases.Load())
				}
			}
			for _, parent := range parentLeases {
				parent.WorkerReturned()
			}
			for index, probe := range parents {
				if probe.releases.Load() != 1 {
					t.Fatalf("parent %d releases = %d, want 1", index, probe.releases.Load())
				}
			}
		})
	}
}

func TestSubLimiterPromotesFIFOWithoutLateBypass(t *testing.T) {
	t.Parallel()

	limiter := NewSubLimiter(1)
	deadline := time.Now().Add(time.Minute)
	activeParent, _ := newSubParentProbe()
	active, outcome := limiter.Acquire(context.Background(), deadline, activeParent)
	if outcome != SubAcquired || active == nil {
		t.Fatalf("active acquire = (%p, %d)", active, outcome)
	}

	olderParent, _ := newSubParentProbe()
	older := make(chan subAcquireResult, 1)
	go func() {
		lease, acquired := limiter.Acquire(context.Background(), deadline, olderParent)
		older <- subAcquireResult{lease: lease, outcome: acquired}
	}()
	awaitSubLimiterCounts(t, limiter, 1, 1)

	newerParent, _ := newSubParentProbe()
	newer := make(chan subAcquireResult, 1)
	go func() {
		lease, acquired := limiter.Acquire(context.Background(), deadline, newerParent)
		newer <- subAcquireResult{lease: lease, outcome: acquired}
	}()
	awaitSubLimiterCounts(t, limiter, 1, 2)

	active.WorkerReturned()
	activeParent.WorkerReturned()
	first := awaitSubAcquire(t, older)
	if first.outcome != SubAcquired || first.lease == nil {
		t.Fatalf("older acquire = (%p, %d)", first.lease, first.outcome)
	}
	select {
	case result := <-newer:
		if result.lease != nil {
			result.lease.WorkerReturned()
		}
		t.Fatalf("newer waiter bypassed older: outcome %d", result.outcome)
	case <-time.After(20 * time.Millisecond):
	}
	first.lease.WorkerReturned()
	olderParent.WorkerReturned()
	second := awaitSubAcquire(t, newer)
	if second.outcome != SubAcquired || second.lease == nil {
		t.Fatalf("newer acquire = (%p, %d)", second.lease, second.outcome)
	}
	second.lease.WorkerReturned()
	newerParent.WorkerReturned()
	assertSubLimiterCounts(t, limiter, 0, 0)
}

func TestSubLimiterRejectsCancellationAndDeadlineBeforeAcquire(t *testing.T) {
	t.Parallel()

	limiter := NewSubLimiter(1)
	parent, _ := newSubParentProbe()
	cancelledContext, cancel := context.WithCancel(context.Background())
	cancel()
	if lease, outcome := limiter.Acquire(cancelledContext, time.Now().Add(time.Minute), parent); lease != nil || outcome != SubCancelled {
		t.Fatalf("pre-cancelled acquire = (%p, %d), want cancelled", lease, outcome)
	}

	deadlineContext, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	parent, _ = newSubParentProbe()
	if lease, outcome := limiter.Acquire(deadlineContext, time.Now().Add(time.Minute), parent); lease != nil || outcome != SubDeadlineExceeded {
		t.Fatalf("context-deadline acquire = (%p, %d), want deadline", lease, outcome)
	}

	parent, _ = newSubParentProbe()
	if lease, outcome := limiter.Acquire(context.Background(), time.Now().Add(-time.Second), parent); lease != nil || outcome != SubDeadlineExceeded {
		t.Fatalf("fixed-deadline acquire = (%p, %d), want deadline", lease, outcome)
	}

	parent, _ = newSubParentProbe()
	parent.MarkNoCommit()
	if lease, outcome := limiter.Acquire(context.Background(), time.Now().Add(time.Minute), parent); lease != nil || outcome != SubCancelled {
		t.Fatalf("no-commit parent acquire = (%p, %d), want cancelled", lease, outcome)
	}
	assertSubLimiterCounts(t, limiter, 0, 0)
}

func TestSubLimiterLateReleaseAtDeadlineCannotAcquire(t *testing.T) {
	t.Parallel()

	limiter := NewSubLimiter(1)
	activeParent, _ := newSubParentProbe()
	active, outcome := limiter.Acquire(context.Background(), time.Now().Add(time.Minute), activeParent)
	if outcome != SubAcquired || active == nil {
		t.Fatalf("active acquire = (%p, %d)", active, outcome)
	}

	waitingParent, _ := newSubParentProbe()
	deadline := time.Now().Add(25 * time.Millisecond)
	waiting := make(chan subAcquireResult, 1)
	go func() {
		lease, acquired := limiter.Acquire(context.Background(), deadline, waitingParent)
		waiting <- subAcquireResult{lease: lease, outcome: acquired}
	}()
	awaitSubLimiterCounts(t, limiter, 1, 1)

	limiter.mu.Lock()
	time.Sleep(time.Until(deadline) + 5*time.Millisecond)
	released := make(chan struct{})
	go func() {
		active.WorkerReturned()
		close(released)
	}()
	limiter.mu.Unlock()

	result := awaitSubAcquire(t, waiting)
	if result.lease != nil || result.outcome != SubDeadlineExceeded {
		t.Fatalf("late waiter = (%p, %d), want deadline", result.lease, result.outcome)
	}
	awaitRuntimeSignal(t, released, "late active release")
	activeParent.WorkerReturned()
	waitingParent.WorkerReturned()
	assertSubLimiterCounts(t, limiter, 0, 0)
}

func TestSubLeaseKeepsOwnersAndBothSlotsChargedUntilWorkerReturn(t *testing.T) {
	t.Parallel()

	limiter := NewSubLimiter(1)
	parent, parentProbe := newSubParentProbe()
	lease, outcome := limiter.Acquire(context.Background(), time.Now().Add(time.Minute), parent)
	if outcome != SubAcquired || lease == nil {
		t.Fatalf("sub acquire = (%p, %d)", lease, outcome)
	}
	owner := &subOwnerProbe{}
	if err := lease.Transfer(owner); err != nil {
		t.Fatalf("transfer sub owner: %v", err)
	}
	lease.MarkNoCommit()
	if owner.noCommits.Load() != 1 {
		t.Fatalf("owner no-commit callbacks = %d, want 1", owner.noCommits.Load())
	}

	nextParent, _ := newSubParentProbe()
	next := make(chan subAcquireResult, 1)
	go func() {
		acquired, result := limiter.Acquire(context.Background(), time.Now().Add(time.Minute), nextParent)
		next <- subAcquireResult{lease: acquired, outcome: result}
	}()
	awaitSubLimiterCounts(t, limiter, 1, 1)
	select {
	case result := <-next:
		if result.lease != nil {
			result.lease.WorkerReturned()
		}
		t.Fatalf("lingering sub-slot was reused early: outcome %d", result.outcome)
	case <-time.After(20 * time.Millisecond):
	}
	if owner.releases.Load() != 0 || parentProbe.releases.Load() != 0 {
		t.Fatalf("resources released before worker return: owner=%d parent=%d", owner.releases.Load(), parentProbe.releases.Load())
	}

	lease.WorkerReturned()
	lease.WorkerReturned()
	promoted := awaitSubAcquire(t, next)
	if promoted.outcome != SubAcquired || promoted.lease == nil {
		t.Fatalf("promoted acquire = (%p, %d)", promoted.lease, promoted.outcome)
	}
	if owner.releases.Load() != 1 || owner.noCommits.Load() != 1 || parentProbe.releases.Load() != 0 {
		t.Fatalf("sub return callbacks owner=%d/%d parent=%d, want 1/1/0", owner.releases.Load(), owner.noCommits.Load(), parentProbe.releases.Load())
	}
	parent.WorkerReturned()
	if parentProbe.releases.Load() != 1 {
		t.Fatalf("controlled parent return releases = %d, want 1", parentProbe.releases.Load())
	}
	promoted.lease.WorkerReturned()
	nextParent.WorkerReturned()
	assertSubLimiterCounts(t, limiter, 0, 0)
}

func TestSubLeaseTerminalRacesChooseOneOwnerAndOneRelease(t *testing.T) {
	for iteration := 0; iteration < 300; iteration++ {
		limiter := NewSubLimiter(1)
		parent, parentProbe := newSubParentProbe()
		lease, outcome := limiter.Acquire(context.Background(), time.Now().Add(time.Minute), parent)
		if outcome != SubAcquired || lease == nil {
			t.Fatalf("iteration %d acquire = (%p, %d)", iteration, lease, outcome)
		}
		original := &subOwnerProbe{}
		replacement := &subOwnerProbe{}
		if err := lease.Transfer(original); err != nil {
			t.Fatalf("iteration %d initial transfer: %v", iteration, err)
		}

		start := make(chan struct{})
		transferResult := make(chan error, 1)
		var racers sync.WaitGroup
		racers.Add(4)
		go func() {
			defer racers.Done()
			<-start
			transferResult <- lease.Transfer(replacement)
		}()
		go func() {
			defer racers.Done()
			<-start
			parent.MarkNoCommit()
		}()
		go func() {
			defer racers.Done()
			<-start
			lease.MarkNoCommit()
		}()
		go func() {
			defer racers.Done()
			<-start
			lease.WorkerReturned()
		}()
		close(start)
		racers.Wait()
		transferErr := <-transferResult
		lease.WorkerReturned()
		parent.WorkerReturned()

		if transferErr != nil && !errors.Is(transferErr, errSubLeaseNoCommit) && !errors.Is(transferErr, errSubLeaseReturned) {
			t.Fatalf("iteration %d transfer error = %v", iteration, transferErr)
		}
		if total := original.releases.Load() + replacement.releases.Load(); total != 1 {
			t.Fatalf("iteration %d owner releases = %d/%d, want total 1", iteration, original.releases.Load(), replacement.releases.Load())
		}
		if total := original.noCommits.Load() + replacement.noCommits.Load(); total > 1 {
			t.Fatalf("iteration %d owner no-commits = %d/%d, want at most 1", iteration, original.noCommits.Load(), replacement.noCommits.Load())
		}
		if parentProbe.releases.Load() != 1 {
			t.Fatalf("iteration %d parent releases = %d, want 1", iteration, parentProbe.releases.Load())
		}
		assertSubLimiterCounts(t, limiter, 0, 0)
	}
}

func TestSubLimiterWaitersRemainBoundedByAdmittedCallers(t *testing.T) {
	t.Parallel()

	const waiters = 64
	coordinator := NewCoordinator(Limits{
		MaxConcurrent: waiters + 1,
		QueueMax:      0,
	})
	limiter := NewSubLimiter(1)
	activeParent := admitSubParent(t, coordinator, "active", context.Background())
	active, outcome := limiter.Acquire(context.Background(), time.Now().Add(time.Minute), activeParent)
	if outcome != SubAcquired || active == nil {
		t.Fatalf("active acquire = (%p, %d)", active, outcome)
	}

	results := make(chan subAcquireResult, waiters)
	cancels := make([]context.CancelFunc, 0, waiters)
	parents := make([]*WorkLease, 0, waiters)
	for index := range waiters {
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		parent := admitSubParent(t, coordinator, fmt.Sprintf("waiter-%d", index), ctx)
		parents = append(parents, parent)
		go func() {
			lease, acquired := limiter.Acquire(ctx, time.Now().Add(time.Minute), parent)
			results <- subAcquireResult{lease: lease, outcome: acquired}
		}()
	}
	awaitSubLimiterCounts(t, limiter, 1, waiters)
	if reservation, admitted := coordinator.Admit(context.Background(), []byte("overflow")); reservation != nil || admitted != AdmitImmediateBudgetExceeded {
		t.Fatalf("admission beyond sub-waiter bound = (%T, %d), want immediate overflow", reservation, admitted)
	}
	for _, cancel := range cancels {
		cancel()
	}
	for range waiters {
		result := awaitSubAcquire(t, results)
		if result.lease != nil || result.outcome != SubCancelled {
			t.Fatalf("cancelled waiter = (%p, %d)", result.lease, result.outcome)
		}
	}
	assertSubLimiterCounts(t, limiter, 1, 0)
	for _, parent := range parents {
		parent.WorkerReturned()
	}
	active.WorkerReturned()
	activeParent.WorkerReturned()
	assertSubLimiterCounts(t, limiter, 0, 0)
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.active != 0 || len(coordinator.requests) != 0 {
		t.Fatalf("coordinator retained active calls: active=%d requests=%d", coordinator.active, len(coordinator.requests))
	}
}

func TestSubLimiterRejectsUnissuedAndRepeatedParents(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	issued, _ := newSubParentProbe()
	forgedIdentity := &WorkLease{origin: issued.origin, release: func() {}}
	for _, test := range []struct {
		name   string
		parent *WorkLease
	}{
		{name: "zero", parent: &WorkLease{}},
		{name: "fabricated", parent: &WorkLease{release: func() {}}},
		{name: "copied identity", parent: forgedIdentity},
	} {
		t.Run(test.name, func(t *testing.T) {
			limiter := NewSubLimiter(1)
			if !subAcquirePanicked(limiter, context.Background(), deadline, test.parent) {
				t.Fatalf("%s parent was accepted", test.name)
			}
			assertSubLimiterCounts(t, limiter, 0, 0)
		})
	}
	issued.WorkerReturned()

	limiter := NewSubLimiter(1)
	parent, probe := newSubParentProbe()
	first, outcome := limiter.Acquire(context.Background(), deadline, parent)
	if outcome != SubAcquired || first == nil {
		t.Fatalf("first acquire = (%p, %d), want acquired", first, outcome)
	}
	repeatedContext, cancelRepeated := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancelRepeated()
	if !subAcquirePanicked(limiter, repeatedContext, deadline, parent) {
		t.Fatal("same parent acquired the same sub-limiter twice")
	}
	assertSubLimiterCounts(t, limiter, 1, 0)
	first.WorkerReturned()
	parent.WorkerReturned()
	if probe.releases.Load() != 1 {
		t.Fatalf("parent releases = %d, want 1", probe.releases.Load())
	}
	assertSubLimiterCounts(t, limiter, 0, 0)
}

func TestNewSubLimiterRejectsZeroCapacity(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("NewSubLimiter(0) did not reject disabled capacity")
		}
	}()
	_ = NewSubLimiter(0)
}

type subAcquireResult struct {
	lease   *SubLease
	outcome SubAcquireOutcome
}

type subParentProbe struct {
	releases atomic.Uint64
}

func (probe *subParentProbe) Release() {
	probe.releases.Add(1)
}

func newSubParentProbe() (*WorkLease, *subParentProbe) {
	coordinator := NewCoordinator(Limits{MaxConcurrent: 1})
	reservation, admitted := coordinator.Admit(context.Background(), []byte("sub-parent"))
	if admitted != AdmitRun || reservation == nil {
		panic("test sub-parent admission failed")
	}
	lease, started := reservation.Start()
	if started.Kind != StartRun || lease == nil {
		panic("test sub-parent start failed")
	}
	probe := &subParentProbe{}
	if err := lease.Transfer(probe); err != nil {
		panic("test sub-parent owner transfer failed")
	}
	return lease, probe
}

func admitSubParent(t *testing.T, coordinator *Coordinator, id string, ctx context.Context) *WorkLease {
	t.Helper()
	reservation, outcome := coordinator.Admit(ctx, []byte(id))
	if outcome != AdmitRun || reservation == nil {
		t.Fatalf("admit %q = (%T, %d), want run", id, reservation, outcome)
	}
	lease, started := reservation.Start()
	if started.Kind != StartRun || lease == nil {
		t.Fatalf("start %q = (%p, %d), want run", id, lease, started.Kind)
	}
	return lease
}

type subOwnerProbe struct {
	releases  atomic.Uint64
	noCommits atomic.Uint64
}

func (owner *subOwnerProbe) Release() {
	owner.releases.Add(1)
}

func (owner *subOwnerProbe) MarkNoCommit() {
	owner.noCommits.Add(1)
}

func awaitSubAcquire(t *testing.T, result <-chan subAcquireResult) subAcquireResult {
	t.Helper()
	select {
	case acquired := <-result:
		return acquired
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for sub-limiter acquire")
		return subAcquireResult{}
	}
}

func assertSubLimiterCounts(t *testing.T, limiter *SubLimiter, active uint64, waiters int) {
	t.Helper()
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.active != active || limiter.waiters.Len() != waiters {
		t.Fatalf("sub-limiter counts = active %d waiters %d, want %d/%d", limiter.active, limiter.waiters.Len(), active, waiters)
	}
	if got, want := len(limiter.parents), int(active)+waiters; got != want {
		t.Fatalf("sub-limiter parent claims = %d, want %d", got, want)
	}
}

func awaitSubLimiterCounts(t *testing.T, limiter *SubLimiter, active uint64, waiters int) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		limiter.mu.Lock()
		gotActive := limiter.active
		gotWaiters := limiter.waiters.Len()
		gotParents := len(limiter.parents)
		limiter.mu.Unlock()
		if gotActive == active && gotWaiters == waiters {
			if wantParents := int(active) + waiters; gotParents != wantParents {
				t.Fatalf("sub-limiter parent claims = %d, want %d", gotParents, wantParents)
			}
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for sub-limiter counts %d/%d; got %d/%d", active, waiters, gotActive, gotWaiters)
		case <-ticker.C:
		}
	}
}

func awaitRuntimeSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func subAcquirePanicked(limiter *SubLimiter, ctx context.Context, deadline time.Time, parent *WorkLease) (panicked bool) {
	defer func() {
		panicked = recover() != nil
	}()
	lease, _ := limiter.Acquire(ctx, deadline, parent)
	if lease != nil {
		lease.WorkerReturned()
	}
	return false
}
