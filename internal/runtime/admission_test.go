package runtime

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestCoordinatorEnforcesActiveLimits(t *testing.T) {
	for _, maximum := range []uint64{1, 8, 64} {
		t.Run(fmt.Sprintf("max-%d", maximum), func(t *testing.T) {
			coordinator := NewCoordinator(Limits{
				MaxConcurrent: maximum,
				QueueMax:      0,
				QueueTimeout:  time.Second,
			})
			reservations := make([]Reservation, 0, maximum)
			for index := uint64(0); index < maximum; index++ {
				reservation, outcome := coordinator.Admit(context.Background(), []byte(fmt.Sprintf("id-%d", index)))
				if outcome != AdmitRun || reservation == nil {
					t.Fatalf("admission %d = (%T, %d), want runnable reservation", index, reservation, outcome)
				}
				reservations = append(reservations, reservation)
			}
			assertCoordinatorCounts(t, coordinator, maximum, 0)

			overflow, outcome := coordinator.Admit(context.Background(), []byte("overflow"))
			if outcome != AdmitImmediateBudgetExceeded || overflow != nil {
				t.Fatalf("overflow admission = (%T, %d), want immediate budget exceeded", overflow, outcome)
			}
			assertCoordinatorCounts(t, coordinator, maximum, 0)

			for index, reservation := range reservations {
				lease, start := reservation.Start()
				if start.Kind != StartRun || start.ResponseRight != nil || lease == nil {
					t.Fatalf("start %d = (%p, %#v), want work lease", index, lease, start)
				}
				lease.WorkerReturned()
			}
			assertCoordinatorCounts(t, coordinator, 0, 0)
		})
	}
}

func TestCoordinatorEnforcesQueueLimitsWithoutCreatingWork(t *testing.T) {
	for _, queueMaximum := range []uint64{0, 1, 1_024} {
		t.Run(fmt.Sprintf("queue-%d", queueMaximum), func(t *testing.T) {
			coordinator := NewCoordinator(Limits{
				MaxConcurrent: 1,
				QueueMax:      queueMaximum,
				QueueTimeout:  time.Hour,
			})
			active, outcome := coordinator.Admit(context.Background(), []byte("active"))
			if outcome != AdmitRun || active == nil {
				t.Fatalf("active admission = (%T, %d)", active, outcome)
			}

			queued := make([]Reservation, 0, queueMaximum)
			for index := uint64(0); index < queueMaximum; index++ {
				reservation, queuedOutcome := coordinator.Admit(context.Background(), []byte(fmt.Sprintf("queued-%d", index)))
				if queuedOutcome != AdmitQueued || reservation == nil {
					t.Fatalf("queued admission %d = (%T, %d)", index, reservation, queuedOutcome)
				}
				concrete := reservation.(*reservationState)
				if concrete.request.lease != nil || concrete.request.state != Queued {
					t.Fatalf("queued request %d owns work: %#v", index, concrete.request)
				}
				queued = append(queued, reservation)
			}
			assertCoordinatorCounts(t, coordinator, 1, queueMaximum)

			overflow, overflowOutcome := coordinator.Admit(context.Background(), []byte("overflow"))
			if overflowOutcome != AdmitImmediateBudgetExceeded || overflow != nil {
				t.Fatalf("queue overflow = (%T, %d)", overflow, overflowOutcome)
			}
			assertCoordinatorCounts(t, coordinator, 1, queueMaximum)

			lease, start := active.Start()
			if start.Kind != StartRun || start.ResponseRight != nil || lease == nil {
				t.Fatalf("active start = (%p, %#v)", lease, start)
			}
			lease.WorkerReturned()
			for _, reservation := range queued {
				queuedLease, queuedStart := reservation.Start()
				if queuedStart.Kind != StartRun || queuedStart.ResponseRight != nil || queuedLease == nil {
					t.Fatalf("queued start = (%p, %#v)", queuedLease, queuedStart)
				}
				queuedLease.WorkerReturned()
			}
			assertCoordinatorCounts(t, coordinator, 0, 0)
		})
	}
}

type startedReservation struct {
	index int
	lease *WorkLease
}

func TestCoordinatorPromotesStrictFIFOWithoutLateBypass(t *testing.T) {
	coordinator := NewCoordinator(Limits{
		MaxConcurrent: 1,
		QueueMax:      3,
		QueueTimeout:  time.Hour,
	})
	active, outcome := coordinator.Admit(context.Background(), []byte("active"))
	if outcome != AdmitRun {
		t.Fatalf("active outcome = %d", outcome)
	}
	activeLease, start := active.Start()
	if start.Kind != StartRun || start.ResponseRight != nil || activeLease == nil {
		t.Fatalf("active start = (%p, %#v)", activeLease, start)
	}

	reservations := make([]Reservation, 0, 4)
	for index := 1; index <= 3; index++ {
		reservation, queued := coordinator.Admit(context.Background(), []byte(fmt.Sprintf("queued-%d", index)))
		if queued != AdmitQueued {
			t.Fatalf("queued outcome %d = %d", index, queued)
		}
		reservations = append(reservations, reservation)
	}
	assertCoordinatorCounts(t, coordinator, 1, 3)

	started := make(chan startedReservation, 4)
	startAttempted := make(chan struct{}, 4)
	startAsync := func(index int, reservation Reservation) {
		go func() {
			startAttempted <- struct{}{}
			lease, result := reservation.Start()
			if result.Kind != StartRun || result.ResponseRight != nil {
				started <- startedReservation{index: -index}
				return
			}
			started <- startedReservation{index: index, lease: lease}
		}()
	}
	for index, reservation := range reservations {
		startAsync(index+1, reservation)
	}
	for range reservations {
		<-startAttempted
	}
	select {
	case unexpected := <-started:
		t.Fatalf("queued reservation started before a real return: %#v", unexpected)
	default:
	}

	activeLease.WorkerReturned()
	first := awaitStartedReservation(t, started)
	if first.index != 1 || first.lease == nil {
		t.Fatalf("first promoted reservation = %#v", first)
	}
	assertCoordinatorCounts(t, coordinator, 1, 2)

	late, lateOutcome := coordinator.Admit(context.Background(), []byte("late"))
	if lateOutcome != AdmitQueued || late == nil {
		t.Fatalf("late admission bypassed queue: (%T, %d)", late, lateOutcome)
	}
	startAsync(4, late)
	<-startAttempted
	assertCoordinatorCounts(t, coordinator, 1, 3)

	current := first
	for want := 2; want <= 4; want++ {
		current.lease.WorkerReturned()
		current = awaitStartedReservation(t, started)
		if current.index != want || current.lease == nil {
			t.Fatalf("promotion %d = %#v", want, current)
		}
	}
	current.lease.WorkerReturned()
	assertCoordinatorCounts(t, coordinator, 0, 0)
}

func TestReservationOwnsIDBytesAndParentContext(t *testing.T) {
	type contextKey struct{}
	key := contextKey{}
	parent := context.WithValue(context.Background(), key, "value")
	id := []byte("request-id")
	coordinator := NewCoordinator(Limits{MaxConcurrent: 1, QueueTimeout: time.Second})
	reservation, outcome := coordinator.Admit(parent, id)
	if outcome != AdmitRun {
		t.Fatalf("admission outcome = %d", outcome)
	}
	id[0] = 'X'
	first := reservation.IDKey()
	if string(first) != "request-id" {
		t.Fatalf("owned id = %q", first)
	}
	first[0] = 'Y'
	if second := reservation.IDKey(); string(second) != "request-id" {
		t.Fatalf("IDKey exposed internal storage: %q", second)
	}
	if reservation.Context().Value(key) != "value" || reservation.Context().Err() != nil {
		t.Fatalf("reservation context lost parent state: %v", reservation.Context().Err())
	}
	lease, start := reservation.Start()
	if start.Kind != StartRun || start.ResponseRight != nil || lease == nil {
		t.Fatalf("start = (%p, %#v)", lease, start)
	}
	lease.WorkerReturned()
}

func TestCoordinatorSerializesQueuedCancellationAndTimeout(t *testing.T) {
	for _, testCase := range []struct {
		name              string
		terminalize       func(Reservation, *manualTimer)
		wantKind          StartOutcomeKind
		wantState         RequestState
		wantResponseRight bool
		wantTimerStops    int
	}{
		{
			name: "cancellation-first",
			terminalize: func(reservation Reservation, timer *manualTimer) {
				reservation.Cancel()
				timer.fireCallback()
			},
			wantKind:       StartCancelled,
			wantState:      Cancelled,
			wantTimerStops: 1,
		},
		{
			name: "timeout-first",
			terminalize: func(reservation Reservation, timer *manualTimer) {
				timer.fireCallback()
				reservation.Cancel()
			},
			wantKind:          StartQueueTimeoutBudgetExceeded,
			wantState:         TimeoutResponseCommitted,
			wantResponseRight: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			clock := &manualClock{}
			coordinator := newCoordinatorWithClock(Limits{
				MaxConcurrent: 1,
				QueueMax:      1,
				QueueTimeout:  time.Second,
			}, clock)

			active, activeOutcome := coordinator.Admit(context.Background(), []byte("active"))
			if activeOutcome != AdmitRun {
				t.Fatalf("active admission = %d", activeOutcome)
			}
			activeLease, activeStart := active.Start()
			if activeStart.Kind != StartRun || activeLease == nil {
				t.Fatalf("active start = (%p, %#v)", activeLease, activeStart)
			}

			queued, queuedOutcome := coordinator.Admit(context.Background(), []byte("queued"))
			if queuedOutcome != AdmitQueued {
				t.Fatalf("queued admission = %d", queuedOutcome)
			}
			timer := clock.singleTimer(t)
			started := make(chan reservationStart, 1)
			go func() {
				lease, outcome := queued.Start()
				started <- reservationStart{lease: lease, outcome: outcome}
			}()

			testCase.terminalize(queued, timer)
			result := awaitReservationStart(t, started)
			if result.lease != nil || result.outcome.Kind != testCase.wantKind {
				t.Fatalf("queued start = (%p, %#v), want nil lease and kind %d", result.lease, result.outcome, testCase.wantKind)
			}
			if (result.outcome.ResponseRight != nil) != testCase.wantResponseRight {
				t.Fatalf("response right = %v, want present %v", result.outcome.ResponseRight != nil, testCase.wantResponseRight)
			}
			if result.outcome.ResponseRight != nil && !result.outcome.ResponseRight.queueTimeout {
				t.Fatal("response right is not the queue-timeout capability")
			}
			if timer.stopCount() != testCase.wantTimerStops {
				t.Fatalf("timer stops = %d, want %d", timer.stopCount(), testCase.wantTimerStops)
			}
			assertCoordinatorCounts(t, coordinator, 1, 0)
			assertCoordinatorRequestCount(t, coordinator, 1)
			concrete := queued.(*reservationState)
			coordinator.mu.Lock()
			terminalState := concrete.request.state
			retainedResponseRight := concrete.request.response
			coordinator.mu.Unlock()
			if terminalState != testCase.wantState {
				t.Fatalf("terminal state = %d, want %d", terminalState, testCase.wantState)
			}
			if retainedResponseRight != nil {
				t.Fatal("coordinator retained a response right after transport received it")
			}

			next, nextOutcome := coordinator.Admit(context.Background(), []byte("next"))
			if nextOutcome != AdmitQueued || next == nil {
				t.Fatalf("next admission = (%T, %d), want queued", next, nextOutcome)
			}
			next.Cancel()
			activeLease.WorkerReturned()
			assertCoordinatorCounts(t, coordinator, 0, 0)
			assertCoordinatorRequestCount(t, coordinator, 0)
		})
	}
}

type reservationStart struct {
	lease   *WorkLease
	outcome StartOutcome
}

func awaitReservationStart(t *testing.T, started <-chan reservationStart) reservationStart {
	t.Helper()
	select {
	case result := <-started:
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reservation start outcome")
		return reservationStart{}
	}
}

type manualClock struct {
	mu     sync.Mutex
	timers []*manualTimer
}

func (clock *manualClock) AfterFunc(delay time.Duration, callback func()) coordinatorTimer {
	timer := &manualTimer{delay: delay, callback: callback}
	clock.mu.Lock()
	clock.timers = append(clock.timers, timer)
	clock.mu.Unlock()
	return timer
}

func (clock *manualClock) singleTimer(t *testing.T) *manualTimer {
	t.Helper()
	clock.mu.Lock()
	defer clock.mu.Unlock()
	if len(clock.timers) != 1 {
		t.Fatalf("timer count = %d, want 1", len(clock.timers))
	}
	if clock.timers[0].delay != time.Second {
		t.Fatalf("timer delay = %s, want 1s", clock.timers[0].delay)
	}
	return clock.timers[0]
}

type manualTimer struct {
	mu       sync.Mutex
	delay    time.Duration
	callback func()
	stops    int
}

func (timer *manualTimer) Stop() bool {
	timer.mu.Lock()
	defer timer.mu.Unlock()
	timer.stops++
	return timer.stops == 1
}

func (timer *manualTimer) fireCallback() {
	timer.callback()
}

func (timer *manualTimer) stopCount() int {
	timer.mu.Lock()
	defer timer.mu.Unlock()
	return timer.stops
}

func assertCoordinatorCounts(t *testing.T, coordinator *Coordinator, active, queued uint64) {
	t.Helper()
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.active != active || uint64(coordinator.queue.Len()) != queued {
		t.Fatalf("coordinator counts = active %d queued %d, want %d/%d", coordinator.active, coordinator.queue.Len(), active, queued)
	}
}

func assertCoordinatorRequestCount(t *testing.T, coordinator *Coordinator, want int) {
	t.Helper()
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if got := len(coordinator.requests); got != want {
		t.Fatalf("coordinator request count = %d, want %d", got, want)
	}
}

func awaitStartedReservation(t *testing.T, started <-chan startedReservation) startedReservation {
	t.Helper()
	select {
	case result := <-started:
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for promoted reservation")
		return startedReservation{}
	}
}
