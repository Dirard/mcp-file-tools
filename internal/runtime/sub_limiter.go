package runtime

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"time"
)

var (
	errSubLeaseNoCommit = errors.New("runtime: sub-lease cannot transfer after no-commit")
	errSubLeaseReturned = errors.New("runtime: sub-lease already returned")
	errSubLeaseNilOwner = errors.New("runtime: sub-lease owner is nil")
)

// SubAcquireOutcome is the transport-free terminal result of local sub-work admission.
type SubAcquireOutcome uint8

const (
	SubAcquired SubAcquireOutcome = iota + 1
	SubCancelled
	SubDeadlineExceeded
)

// SubLimiter bounds one process-wide class of scan or parse workers.
type SubLimiter struct {
	mu      sync.Mutex
	max     uint64
	active  uint64
	waiters list.List
	// parents makes the waiter bound structural: one coordinator-issued call
	// may own at most one active or queued unit in this limiter.
	parents map[*WorkLease]struct{}
}

// SubLease keeps one sub-work slot and its resource owner charged until the
// corresponding scan or parse worker actually returns. The parent call lease
// remains independently charged until the whole call worker returns.
type SubLease struct {
	mu       sync.Mutex
	limiter  *SubLimiter
	parent   *WorkLease
	owner    Owner
	noCommit bool
	returned bool
}

type subWaiter struct {
	ctx      context.Context
	deadline time.Time
	parent   *WorkLease
	ready    chan struct{}
	node     *list.Element
	outcome  SubAcquireOutcome
}

// NewSubLimiter constructs a closed, positive-capacity sub-work limiter.
func NewSubLimiter(max uint64) *SubLimiter {
	if max == 0 {
		panic("runtime: sub-limiter capacity must be positive")
	}
	return &SubLimiter{
		max:     max,
		parents: make(map[*WorkLease]struct{}),
	}
}

// Acquire reserves one FIFO sub-work slot under an already admitted call and
// the call's fixed deadline.
func (limiter *SubLimiter) Acquire(ctx context.Context, deadline time.Time, parent *WorkLease) (*SubLease, SubAcquireOutcome) {
	if limiter == nil {
		panic("runtime: sub-limiter is nil")
	}
	if ctx == nil {
		panic("runtime: sub-limiter context is nil")
	}
	if deadline.IsZero() {
		panic("runtime: sub-limiter deadline is zero")
	}
	if parent == nil {
		panic("runtime: sub-limiter parent lease is nil")
	}
	if !parent.subAcquireAllowed() {
		return nil, SubCancelled
	}
	if outcome, terminal := subAcquireTerminal(ctx, deadline, time.Now()); terminal {
		return nil, outcome
	}

	waiter := &subWaiter{
		ctx:      ctx,
		deadline: deadline,
		parent:   parent,
		ready:    make(chan struct{}),
	}
	limiter.mu.Lock()
	if limiter.max == 0 || limiter.parents == nil {
		limiter.mu.Unlock()
		panic("runtime: sub-limiter was not constructed")
	}
	if _, exists := limiter.parents[parent]; exists {
		limiter.mu.Unlock()
		panic("runtime: parent already owns sub-work in this limiter")
	}
	if !parent.subAcquireAllowed() {
		limiter.mu.Unlock()
		return nil, SubCancelled
	}
	limiter.parents[parent] = struct{}{}
	if limiter.active < limiter.max && limiter.waiters.Len() == 0 {
		limiter.active++
		limiter.mu.Unlock()
		return limiter.finishReserved(ctx, deadline, parent)
	}
	waiter.node = limiter.waiters.PushBack(waiter)
	limiter.mu.Unlock()

	timer := time.NewTimer(time.Until(deadline))
	select {
	case <-waiter.ready:
	case <-ctx.Done():
		outcome, _ := subAcquireTerminal(ctx, deadline, time.Now())
		limiter.terminalizeWaiter(waiter, outcome)
	case <-timer.C:
		limiter.terminalizeWaiter(waiter, SubDeadlineExceeded)
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	<-waiter.ready
	if waiter.outcome != SubAcquired {
		return nil, waiter.outcome
	}
	return limiter.finishReserved(ctx, deadline, parent)
}

func (limiter *SubLimiter) finishReserved(ctx context.Context, deadline time.Time, parent *WorkLease) (*SubLease, SubAcquireOutcome) {
	lease := &SubLease{limiter: limiter, parent: parent}
	if outcome, terminal := subAcquireTerminal(ctx, deadline, time.Now()); terminal {
		lease.WorkerReturned()
		return nil, outcome
	}
	if !parent.subAcquireAllowed() {
		lease.WorkerReturned()
		return nil, SubCancelled
	}
	return lease, SubAcquired
}

func (limiter *SubLimiter) terminalizeWaiter(waiter *subWaiter, outcome SubAcquireOutcome) {
	limiter.mu.Lock()
	if waiter.outcome == 0 {
		limiter.waiters.Remove(waiter.node)
		waiter.node = nil
		delete(limiter.parents, waiter.parent)
		waiter.outcome = outcome
		close(waiter.ready)
	}
	limiter.mu.Unlock()
}

func (limiter *SubLimiter) release(parent *WorkLease) {
	limiter.mu.Lock()
	if limiter.active == 0 {
		limiter.mu.Unlock()
		panic("runtime: sub-limiter released while idle")
	}
	if _, exists := limiter.parents[parent]; !exists {
		limiter.mu.Unlock()
		panic("runtime: sub-limiter parent released without ownership")
	}
	delete(limiter.parents, parent)
	limiter.active--
	limiter.promoteHeadLocked(time.Now())
	limiter.mu.Unlock()
}

func (limiter *SubLimiter) promoteHeadLocked(now time.Time) {
	for limiter.active < limiter.max {
		head := limiter.waiters.Front()
		if head == nil {
			return
		}
		waiter := head.Value.(*subWaiter)
		limiter.waiters.Remove(head)
		waiter.node = nil
		if outcome, terminal := subAcquireTerminal(waiter.ctx, waiter.deadline, now); terminal {
			delete(limiter.parents, waiter.parent)
			waiter.outcome = outcome
			close(waiter.ready)
			continue
		}
		limiter.active++
		waiter.outcome = SubAcquired
		close(waiter.ready)
		return
	}
}

func subAcquireTerminal(ctx context.Context, deadline, now time.Time) (SubAcquireOutcome, bool) {
	if !now.Before(deadline) {
		return SubDeadlineExceeded, true
	}
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return SubDeadlineExceeded, true
		}
		return SubCancelled, true
	}
	return 0, false
}

// Transfer moves sub-work resource cleanup to a new exactly-once owner.
func (lease *SubLease) Transfer(owner Owner) error {
	if owner == nil {
		return errSubLeaseNilOwner
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.returned {
		return errSubLeaseReturned
	}
	if lease.noCommit {
		return errSubLeaseNoCommit
	}
	lease.owner = owner
	return nil
}

// MarkNoCommit prevents later sub-work publication and closes the same parent
// call publication without releasing either work slot.
func (lease *SubLease) MarkNoCommit() {
	lease.mu.Lock()
	if lease.noCommit || lease.returned {
		lease.mu.Unlock()
		return
	}
	lease.noCommit = true
	parent := lease.parent
	owner, notify := lease.owner.(NoCommitOwner)
	lease.mu.Unlock()

	if parent != nil {
		parent.MarkNoCommit()
	}
	if notify {
		owner.MarkNoCommit()
	}
}

// WorkerReturned releases the sub-work owner and slot exactly once. It never
// releases the parent call slot; the final call worker owns that transition.
func (lease *SubLease) WorkerReturned() {
	lease.mu.Lock()
	if lease.returned {
		lease.mu.Unlock()
		return
	}
	lease.returned = true
	owner := lease.owner
	lease.owner = nil
	limiter := lease.limiter
	parent := lease.parent
	lease.limiter = nil
	lease.parent = nil
	lease.mu.Unlock()

	if limiter != nil {
		defer limiter.release(parent)
	}
	if owner != nil {
		owner.Release()
	}
}
