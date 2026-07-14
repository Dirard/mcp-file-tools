package runtime

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCoordinatorStressReleasesEveryBoundedRequestTimerAndQueueNode(t *testing.T) {
	const (
		activeMax = uint64(8)
		queueMax  = uint64(16)
		cycles    = 200
	)
	random := rand.New(rand.NewSource(0x5eed))

	for cycle := 0; cycle < cycles; cycle++ {
		clock := &stressCoordinatorClock{}
		coordinator := newCoordinatorWithClock(Limits{
			MaxConcurrent: activeMax,
			QueueMax:      queueMax,
			QueueTimeout:  time.Second,
		}, clock)

		activeReservations := make([]Reservation, 0, activeMax)
		activeLeases := make([]*WorkLease, 0, activeMax)
		for index := uint64(0); index < activeMax; index++ {
			reservation, outcome := coordinator.Admit(context.Background(), []byte(fmt.Sprintf("%d-active-%d", cycle, index)))
			if outcome != AdmitRun {
				t.Fatalf("cycle %d active admission %d = %d", cycle, index, outcome)
			}
			lease, start := reservation.Start()
			if start.Kind != StartRun || lease == nil {
				t.Fatalf("cycle %d active start %d = %p/%#v", cycle, index, lease, start)
			}
			activeReservations = append(activeReservations, reservation)
			activeLeases = append(activeLeases, lease)
		}

		queued := make([]Reservation, 0, queueMax)
		for index := uint64(0); index < queueMax; index++ {
			reservation, outcome := coordinator.Admit(context.Background(), []byte(fmt.Sprintf("%d-queued-%d", cycle, index)))
			if outcome != AdmitQueued {
				t.Fatalf("cycle %d queued admission %d = %d", cycle, index, outcome)
			}
			queued = append(queued, reservation)
		}
		assertCoordinatorBounded(t, coordinator, activeMax, queueMax)
		if reservation, outcome := coordinator.Admit(context.Background(), []byte(fmt.Sprintf("%d-overflow", cycle))); reservation != nil || outcome != AdmitImmediateBudgetExceeded {
			t.Fatalf("cycle %d overflow = %T/%d", cycle, reservation, outcome)
		}
		assertCoordinatorBounded(t, coordinator, activeMax, queueMax)

		for _, reservation := range queued {
			concrete := reservation.(*reservationState)
			timer := concrete.request.timer.(*stressCoordinatorTimer)
			cancelFirst := random.Intn(2) == 0
			if cancelFirst {
				reservation.Cancel()
				timer.fire()
			} else {
				timer.fire()
				reservation.Cancel()
			}
			lease, outcome := reservation.Start()
			if lease != nil || outcome.Kind != StartCancelled && outcome.Kind != StartQueueTimeoutBudgetExceeded {
				t.Fatalf("cycle %d queued terminal = %p/%#v", cycle, lease, outcome)
			}
			if cancelFirst && outcome.Kind != StartCancelled {
				t.Fatalf("cycle %d cancellation-first outcome = %d", cycle, outcome.Kind)
			}
			if !cancelFirst && (outcome.Kind != StartQueueTimeoutBudgetExceeded || outcome.ResponseRight == nil) {
				t.Fatalf("cycle %d timeout-first outcome = %#v", cycle, outcome)
			}
			coordinator.mu.Lock()
			queueNode, retainedTimer := concrete.request.queueNode, concrete.request.timer
			coordinator.mu.Unlock()
			if queueNode != nil || retainedTimer != nil {
				t.Fatalf("cycle %d retained queue node/timer = %v/%T", cycle, queueNode, retainedTimer)
			}
			assertCoordinatorBounded(t, coordinator, activeMax, queueMax)
		}

		order := random.Perm(len(activeReservations))
		for _, index := range order {
			if random.Intn(2) == 0 {
				activeReservations[index].Cancel()
			}
			activeLeases[index].WorkerReturned()
			assertCoordinatorBounded(t, coordinator, activeMax, queueMax)
		}
		assertCoordinatorCounts(t, coordinator, 0, 0)
		assertCoordinatorRequestCount(t, coordinator, 0)
		if got := clock.live.Load(); got != 0 {
			t.Fatalf("cycle %d live timers = %d, want 0", cycle, got)
		}
		if got := clock.created.Load(); got != queueMax {
			t.Fatalf("cycle %d created timers = %d, want %d", cycle, got, queueMax)
		}
	}
}

func TestCoordinatorRandomCancelTimeoutPromotionRaceLeavesNoState(t *testing.T) {
	const iterations = 1_000
	for iteration := 0; iteration < iterations; iteration++ {
		clock := &stressCoordinatorClock{}
		coordinator := newCoordinatorWithClock(Limits{
			MaxConcurrent: 1,
			QueueMax:      1,
			QueueTimeout:  time.Second,
		}, clock)
		active, activeOutcome := coordinator.Admit(context.Background(), []byte(fmt.Sprintf("%d-active", iteration)))
		if activeOutcome != AdmitRun {
			t.Fatalf("iteration %d active admission = %d", iteration, activeOutcome)
		}
		activeLease, activeStart := active.Start()
		if activeStart.Kind != StartRun || activeLease == nil {
			t.Fatalf("iteration %d active start = %p/%#v", iteration, activeLease, activeStart)
		}
		queued, queuedOutcome := coordinator.Admit(context.Background(), []byte(fmt.Sprintf("%d-queued", iteration)))
		if queuedOutcome != AdmitQueued {
			t.Fatalf("iteration %d queued admission = %d", iteration, queuedOutcome)
		}
		timer := queued.(*reservationState).request.timer.(*stressCoordinatorTimer)
		started := make(chan reservationStart, 1)
		go func() {
			lease, outcome := queued.Start()
			started <- reservationStart{lease: lease, outcome: outcome}
		}()

		start := make(chan struct{})
		var racers sync.WaitGroup
		racers.Add(3)
		go func() {
			defer racers.Done()
			<-start
			queued.Cancel()
		}()
		go func() {
			defer racers.Done()
			<-start
			timer.fire()
		}()
		go func() {
			defer racers.Done()
			<-start
			activeLease.WorkerReturned()
		}()
		close(start)
		racers.Wait()
		result := awaitReservationStart(t, started)
		switch result.outcome.Kind {
		case StartRun:
			if result.lease == nil {
				t.Fatalf("iteration %d run outcome has no lease", iteration)
			}
			result.lease.WorkerReturned()
		case StartCancelled, StartQueueTimeoutBudgetExceeded:
			if result.lease != nil {
				t.Fatalf("iteration %d terminal outcome retained lease %p", iteration, result.lease)
			}
		default:
			t.Fatalf("iteration %d unexpected start outcome %#v", iteration, result.outcome)
		}
		assertCoordinatorCounts(t, coordinator, 0, 0)
		assertCoordinatorRequestCount(t, coordinator, 0)
		if got := clock.live.Load(); got != 0 {
			t.Fatalf("iteration %d live timers = %d, want 0", iteration, got)
		}
	}
}

func assertCoordinatorBounded(t *testing.T, coordinator *Coordinator, activeMax, queueMax uint64) {
	t.Helper()
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	active, queued, requests := coordinator.active, uint64(coordinator.queue.Len()), uint64(len(coordinator.requests))
	if active > activeMax || queued > queueMax || requests > activeMax+queueMax {
		t.Fatalf("coordinator exceeded bounds: active=%d/%d queued=%d/%d requests=%d/%d", active, activeMax, queued, queueMax, requests, activeMax+queueMax)
	}
}

type stressCoordinatorClock struct {
	created atomic.Uint64
	live    atomic.Int64
}

func (clock *stressCoordinatorClock) AfterFunc(_ time.Duration, callback func()) coordinatorTimer {
	clock.created.Add(1)
	clock.live.Add(1)
	return &stressCoordinatorTimer{clock: clock, callback: callback}
}

type stressCoordinatorTimer struct {
	clock    *stressCoordinatorClock
	callback func()
	done     atomic.Bool
}

func (timer *stressCoordinatorTimer) Stop() bool {
	if !timer.done.CompareAndSwap(false, true) {
		return false
	}
	timer.clock.live.Add(-1)
	return true
}

func (timer *stressCoordinatorTimer) fire() bool {
	if !timer.done.CompareAndSwap(false, true) {
		return false
	}
	timer.clock.live.Add(-1)
	timer.callback()
	return true
}
