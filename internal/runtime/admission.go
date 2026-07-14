package runtime

import (
	"container/list"
	"context"
	"sync"
	"time"
)

// Limits bounds active workers and FIFO reservations for one connection.
type Limits struct {
	MaxConcurrent uint64
	QueueMax      uint64
	QueueTimeout  time.Duration
}

// Owner releases one resource bundle owned by a work lease.
type Owner interface {
	Release()
}

// NoCommitOwner can additionally prevent publication after cancellation.
type NoCommitOwner interface {
	Owner
	MarkNoCommit()
}

// SharedNoCommitOwner detaches a cancelled requester from shared work before
// deciding whether the backing work lease must stop publishing. A true return
// keeps the lease usable for the remaining requesters.
type SharedNoCommitOwner interface {
	Owner
	HandleSharedNoCommit() bool
}

// RequestState is the coordinator-owned lifecycle of one admitted request.
type RequestState uint8

const (
	Queued RequestState = iota + 1
	Running
	Cancelled
	TimeoutResponseCommitted
	ResponseCommitted
	Lingering
	Returned
)

// AdmitOutcome reports the synchronous capacity decision.
type AdmitOutcome uint8

const (
	AdmitRun AdmitOutcome = iota + 1
	AdmitQueued
	AdmitImmediateBudgetExceeded
)

// StartOutcomeKind reports whether an admitted worker may execute.
type StartOutcomeKind uint8

const (
	StartRun StartOutcomeKind = iota + 1
	StartQueueTimeoutBudgetExceeded
	StartCancelled
)

// ResponseRight is the coordinator-issued capability for one terminal response.
// A queue timeout owns exactly one such capability; cancellation owns none.
type ResponseRight struct {
	queueTimeout bool
}

// StartOutcome carries both the terminal kind and any response capability.
type StartOutcome struct {
	Kind          StartOutcomeKind
	ResponseRight *ResponseRight
}

// Reservation is the transport-owned handle for one admitted request.
type Reservation interface {
	IDKey() []byte
	Context() context.Context
	Start() (*WorkLease, StartOutcome)
	Cancel()
}

// Coordinator owns all active and queued request state for one connection.
type Coordinator struct {
	mu       sync.Mutex
	limits   Limits
	active   uint64
	queue    list.List
	requests map[string]*request
	clock    coordinatorClock
	fatal    *FatalSignal
}

type request struct {
	idKey       []byte
	state       RequestState
	context     context.Context
	cancel      context.CancelFunc
	ready       chan struct{}
	readyClosed bool
	queueNode   *list.Element
	lease       *WorkLease
	startCalled bool
	timer       coordinatorTimer
	response    *ResponseRight
}

type reservationState struct {
	coordinator *Coordinator
	request     *request
}

// NewCoordinator constructs a bounded connection-local coordinator.
func NewCoordinator(limits Limits) *Coordinator {
	return NewCoordinatorWithFatal(limits, NewFatalSignal())
}

// NewCoordinatorWithFatal constructs a coordinator that shares its fatal signal
// with every other owned goroutine for the same connection.
func NewCoordinatorWithFatal(limits Limits, fatal *FatalSignal) *Coordinator {
	return newCoordinatorWithClockAndFatal(limits, systemCoordinatorClock{}, fatal)
}

func newCoordinatorWithClock(limits Limits, clock coordinatorClock) *Coordinator {
	return newCoordinatorWithClockAndFatal(limits, clock, NewFatalSignal())
}

func newCoordinatorWithClockAndFatal(limits Limits, clock coordinatorClock, fatal *FatalSignal) *Coordinator {
	if limits.MaxConcurrent == 0 {
		panic("runtime: MaxConcurrent must be positive")
	}
	if limits.QueueMax != 0 && limits.QueueTimeout <= 0 {
		panic("runtime: queued admission requires a positive timeout")
	}
	if clock == nil {
		panic("runtime: coordinator clock is nil")
	}
	if fatal == nil {
		panic("runtime: coordinator fatal signal is nil")
	}
	return &Coordinator{
		limits:   limits,
		requests: make(map[string]*request),
		clock:    clock,
		fatal:    fatal,
	}
}

// Admit reserves active capacity, joins the bounded FIFO, or rejects immediately.
func (coordinator *Coordinator) Admit(parent context.Context, idKey []byte) (Reservation, AdmitOutcome) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()

	key := string(idKey)
	if key == "" {
		panic("runtime: request id key is empty")
	}
	if _, exists := coordinator.requests[key]; exists {
		panic("runtime: request id key is already admitted")
	}

	state := Queued
	outcome := AdmitQueued
	if coordinator.active < coordinator.limits.MaxConcurrent && coordinator.queue.Len() == 0 {
		state = Running
		outcome = AdmitRun
		coordinator.active++
	} else if uint64(coordinator.queue.Len()) >= coordinator.limits.QueueMax {
		return nil, AdmitImmediateBudgetExceeded
	}

	requestContext, cancel := context.WithCancel(parent)
	admitted := &request{
		idKey:   append([]byte(nil), idKey...),
		state:   state,
		context: requestContext,
		cancel:  cancel,
		ready:   make(chan struct{}),
	}
	if state == Queued {
		admitted.queueNode = coordinator.queue.PushBack(admitted)
	}
	coordinator.requests[key] = admitted
	if state == Queued {
		admitted.timer = coordinator.clock.AfterFunc(coordinator.limits.QueueTimeout, func() {
			coordinator.runOwned(func() {
				coordinator.queueTimedOut(admitted)
			}, func() {
				coordinator.failQueueTimerAfterPanic(admitted)
			})
		})
	}
	return &reservationState{coordinator: coordinator, request: admitted}, outcome
}

func (coordinator *Coordinator) runOwned(operation func(), cleanup func()) bool {
	return coordinator.fatal.Run(operation, cleanup)
}

func (reservation *reservationState) IDKey() []byte {
	return append([]byte(nil), reservation.request.idKey...)
}

func (reservation *reservationState) Context() context.Context {
	return reservation.request.context
}

func (reservation *reservationState) Start() (*WorkLease, StartOutcome) {
	coordinator := reservation.coordinator
	coordinator.mu.Lock()
	if reservation.request.startCalled {
		coordinator.mu.Unlock()
		panic("runtime: reservation started more than once")
	}
	reservation.request.startCalled = true

	for {
		switch reservation.request.state {
		case Running:
			lease := newWorkLease(func() {
				coordinator.workerReturned(reservation.request)
			})
			reservation.request.lease = lease
			coordinator.mu.Unlock()
			return lease, StartOutcome{Kind: StartRun}
		case Queued:
			ready := reservation.request.ready
			coordinator.mu.Unlock()
			<-ready
			coordinator.mu.Lock()
		case Cancelled:
			coordinator.mu.Unlock()
			return nil, StartOutcome{Kind: StartCancelled}
		case TimeoutResponseCommitted:
			responseRight := reservation.request.response
			if responseRight == nil {
				coordinator.mu.Unlock()
				panic("runtime: queue timeout has no response right")
			}
			reservation.request.response = nil
			coordinator.mu.Unlock()
			return nil, StartOutcome{
				Kind:          StartQueueTimeoutBudgetExceeded,
				ResponseRight: responseRight,
			}
		default:
			coordinator.mu.Unlock()
			panic("runtime: reservation has no valid start transition")
		}
	}
}

func (reservation *reservationState) Cancel() {
	reservation.coordinator.cancel(reservation.request)
}
