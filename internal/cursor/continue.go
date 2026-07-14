package cursor

import (
	"context"
	"errors"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/rootfs"
	serverruntime "github.com/Dirard/mcp-file-tools/internal/runtime"
)

// Resources exposes only a continuation-scoped borrowed root.
type Resources struct {
	Root rootfs.Borrowed
}

// Compute advances one separately owned working state.
type Compute func(ctx context.Context, working State, resources Resources) Outcome

// ResultFinalizer renders a partial result after the registry derives its child token.
type ResultFinalizer func(child Token) (api.Result, error)

// Outcome is either terminal Result or a Successor plus token-aware Finalize callback.
type Outcome struct {
	Result      api.Result
	Successor   State
	Reservation ReservationUse
	Progress    ProgressProof
	Finalize    ResultFinalizer
}

// Waiter keeps request-specific response state outside cursor accounting.
type Waiter interface {
	Deliver(api.Result)
	Cancelled() <-chan struct{}
	CloseWithoutResponse()
}

// ProgressKind is the closed set of monotonic continuation transitions.
type ProgressKind uint8

const (
	ProgressOutputKey ProgressKind = iota + 1
	ProgressReadItem
	ProgressSummary
	ProgressTraversal
)

// ProgressProof prevents a partial cursor from reproducing the same state forever.
type ProgressProof struct {
	Kind        ProgressKind
	BeforeValue uint64
	AfterValue  uint64
	Before      [32]byte
	After       [32]byte
}

// Valid reports whether this proof strictly advances its selected dimension.
func (proof ProgressProof) Valid() bool {
	switch proof.Kind {
	case ProgressOutputKey, ProgressReadItem:
		return proof.AfterValue > proof.BeforeValue
	case ProgressSummary, ProgressTraversal:
		return proof.Before != proof.After || proof.AfterValue > proof.BeforeValue
	default:
		return false
	}
}

type continuationWaiter struct {
	registry    *Registry
	computation *computation
	waiter      Waiter
	lease       *serverruntime.WorkLease
	previous    *continuationWaiter
	next        *continuationWaiter
	done        chan struct{}
	attached    bool
	doneClosed  bool
}

type computation struct {
	registry     *Registry
	entry        EntryRef
	lineage      lineageRef
	ctx          context.Context
	cancel       context.CancelFunc
	deadlineCode api.ErrorCode
	compute      Compute
	working      State
	root         *rootfs.Lease
	work         *serverruntime.WorkLease
	starter      *continuationWaiter
	head         *continuationWaiter
	tail         *continuationWaiter
	waiterCount  uint32
	noCommit     bool
	finished     bool
	tombstoned   bool
}

// Continue joins replay/singleflight for one scoped cursor token.
func (registry *Registry) Continue(ctx context.Context, token Token, tool api.ToolName, cwdID uint64, waiter Waiter, compute Compute, work *serverruntime.WorkLease) {
	if work == nil {
		return
	}
	if waiter == nil {
		work.WorkerReturned()
		return
	}
	if waiterIsCancelled(waiter) {
		waiter.CloseWithoutResponse()
		work.WorkerReturned()
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	node := &continuationWaiter{registry: registry, waiter: waiter, done: make(chan struct{})}
	registry.mu.Lock()
	entry, code := registry.lookupScopedLocked(token, tool, cwdID, registry.clockNowLocked())
	if code != "" {
		registry.mu.Unlock()
		deliverImmediate(waiter, cursorResult(code), work)
		return
	}
	entryIndex := entry.index
	if memo := registry.entries.memo[entryIndex]; memo != nil {
		result := *memo
		registry.mu.Unlock()
		deliverImmediate(waiter, result, work)
		return
	}
	if running := registry.entries.runtime[entryIndex]; running != nil {
		active, ok := running.(*computation)
		if !ok || active.finished || active.noCommit {
			registry.mu.Unlock()
			deliverImmediate(waiter, cursorResult(api.ErrorCursorExpired), work)
			return
		}
		node.computation = active
		node.lease = work
		active.attachLocked(node)
		if err := work.Transfer(node); err != nil {
			active.detachLocked(node)
			registry.mu.Unlock()
			waiter.CloseWithoutResponse()
			work.WorkerReturned()
			return
		}
		registry.mu.Unlock()
		go node.watchCancellation()
		return
	}
	if compute == nil {
		registry.mu.Unlock()
		deliverImmediate(waiter, cursorResult(api.ErrorInvalidInput), work)
		return
	}
	parent := registry.entries.state[entryIndex]
	working := cloneState(parent)
	if working == nil {
		lineageIndex := registry.entries.lineage[entryIndex]
		registry.lineages.flags[lineageIndex] = lineageRollbackPending
		registry.removeLineageLocked(lineageRef{index: lineageIndex, generation: registry.lineages.generation[lineageIndex]})
		registry.mu.Unlock()
		deliverImmediate(waiter, cursorResult(api.ErrorIOError), work)
		return
	}
	lineageIndex := registry.entries.lineage[entryIndex]
	lineage := lineageRef{index: lineageIndex, generation: registry.lineages.generation[lineageIndex]}
	deadline, deadlineCode := continuationDeadline(ctx, registry.lineages.commitDeadline[lineageIndex])
	computeContext, cancel := context.WithDeadline(context.Background(), deadline)
	active := &computation{
		registry:     registry,
		entry:        entry,
		lineage:      lineage,
		ctx:          computeContext,
		cancel:       cancel,
		deadlineCode: deadlineCode,
		compute:      compute,
		working:      working,
		root:         registry.lineages.root[lineageIndex],
		work:         work,
	}
	node.computation = active
	active.starter = node
	active.attachLocked(node)
	registry.entries.runtime[entryIndex] = active
	registry.lineages.pins[lineageIndex]++
	if err := work.Transfer(active); err != nil {
		registry.entries.runtime[entryIndex] = nil
		registry.lineages.pins[lineageIndex]--
		active.detachLocked(node)
		active.finished = true
		cancel()
		registry.mu.Unlock()
		waiter.CloseWithoutResponse()
		work.WorkerReturned()
		return
	}
	registry.mu.Unlock()
	go node.watchCancellation()
	go active.watchDeadline()
	go active.run()
}

func cloneState(parent State) State {
	if parent == nil {
		return nil
	}
	working := parent.CloneForCompute()
	if working == nil || working.Tool() != parent.Tool() || working.CWDID() != parent.CWDID() || working.Digest() != parent.Digest() || working.Footprint() != parent.Footprint() {
		return nil
	}
	parentShared, parentHasShared := parent.SharedDigest()
	workingShared, workingHasShared := working.SharedDigest()
	if parentHasShared != workingHasShared || parentShared != workingShared {
		return nil
	}
	return working
}

func continuationDeadline(ctx context.Context, lineageDeadlineNanos int64) (time.Time, api.ErrorCode) {
	lineageDeadline := time.Unix(0, lineageDeadlineNanos)
	if callerDeadline, ok := ctx.Deadline(); ok && callerDeadline.Before(lineageDeadline) {
		return callerDeadline, api.ErrorBudgetExceeded
	}
	return lineageDeadline, api.ErrorCursorExpired
}

func (node *continuationWaiter) watchCancellation() {
	select {
	case <-node.waiter.Cancelled():
		node.registry.cancelWaiter(node)
	case <-node.done:
	}
}

func (node *continuationWaiter) Release() {
	if node != nil && node.registry != nil {
		node.registry.cancelWaiter(node)
	}
}

func (node *continuationWaiter) MarkNoCommit() { node.Release() }

func (active *computation) Release() {}

func (active *computation) HandleSharedNoCommit() bool {
	if active == nil || active.registry == nil || active.starter == nil {
		return false
	}
	return active.registry.cancelWaiter(active.starter)
}

func (active *computation) attachLocked(node *continuationWaiter) {
	node.attached = true
	node.previous = active.tail
	if active.tail != nil {
		active.tail.next = node
	} else {
		active.head = node
	}
	active.tail = node
	active.waiterCount++
}

func (active *computation) detachLocked(node *continuationWaiter) {
	if node == nil || !node.attached {
		return
	}
	if node.previous != nil {
		node.previous.next = node.next
	} else {
		active.head = node.next
	}
	if node.next != nil {
		node.next.previous = node.previous
	} else {
		active.tail = node.previous
	}
	node.previous = nil
	node.next = nil
	node.attached = false
	active.waiterCount--
	closeWaiterDone(node)
}

func (active *computation) detachAllLocked() *continuationWaiter {
	head := active.head
	for node := head; node != nil; node = node.next {
		node.attached = false
		node.previous = nil
		closeWaiterDone(node)
	}
	active.head = nil
	active.tail = nil
	active.waiterCount = 0
	return head
}

func closeWaiterDone(node *continuationWaiter) {
	if !node.doneClosed {
		node.doneClosed = true
		close(node.done)
	}
}

func (registry *Registry) cancelWaiter(node *continuationWaiter) bool {
	if node == nil {
		return false
	}
	registry.mu.Lock()
	active := node.computation
	if active == nil || active.finished {
		registry.mu.Unlock()
		return false
	}
	if !node.attached {
		retain := !active.noCommit && active.waiterCount != 0
		registry.mu.Unlock()
		return retain
	}
	active.detachLocked(node)
	retain := active.waiterCount != 0
	if !retain {
		active.noCommit = true
		registry.tombstoneComputationLocked(active)
		active.cancel()
	}
	lease := node.lease
	node.lease = nil
	registry.mu.Unlock()
	node.waiter.CloseWithoutResponse()
	if lease != nil {
		lease.WorkerReturned()
	}
	return retain
}

func (registry *Registry) tombstoneComputationLocked(active *computation) {
	if active == nil || active.tombstoned || !registry.entries.valid(active.entry) || !registry.lineages.valid(active.lineage) {
		return
	}
	entryIndex := active.entry.index
	lineageIndex := active.lineage.index
	if registry.entries.kind[entryIndex] == entryKindResident && registry.lineages.resident[lineageIndex] != 0 {
		registry.entries.kind[entryIndex] = entryKindTombstone
		registry.lineages.resident[lineageIndex]--
		registry.lineages.tombstones[lineageIndex]++
	}
	registry.lineages.flags[lineageIndex] = lineageRollbackPending
	active.tombstoned = true
}

func (active *computation) watchDeadline() {
	<-active.ctx.Done()
	if errors.Is(active.ctx.Err(), context.DeadlineExceeded) {
		active.registry.expireComputation(active, active.deadlineCode)
	}
}

func (registry *Registry) expireComputation(active *computation, code api.ErrorCode) {
	registry.mu.Lock()
	if active == nil || active.finished || active.noCommit || !registry.entries.valid(active.entry) || registry.entries.runtime[active.entry.index] != active {
		registry.mu.Unlock()
		return
	}
	active.noCommit = true
	registry.tombstoneComputationLocked(active)
	head := active.detachAllLocked()
	registry.mu.Unlock()
	deliverWaiters(head, cursorResult(code), true)
}

func (active *computation) run() {
	outcome, err := executeComputation(active)
	if err != nil {
		active.registry.handleOutcome(active, Outcome{Result: cursorResult(api.ErrorIOError)})
		return
	}
	active.registry.handleOutcome(active, outcome)
}

func executeComputation(active *computation) (outcome Outcome, err error) {
	defer func() {
		if recover() != nil {
			err = errRegistryInvariant
		}
	}()
	if active.root == nil {
		return active.compute(active.ctx, active.working, Resources{}), nil
	}
	return rootfs.WithBorrow(active.root, func(borrowed rootfs.Borrowed) Outcome {
		return active.compute(active.ctx, active.working, Resources{Root: borrowed})
	})
}

func (registry *Registry) handleOutcome(active *computation, outcome Outcome) {
	if code, expired, usable := registry.computationStatus(active); !usable {
		registry.finishDiscarded(active)
		return
	} else if expired {
		registry.expireComputation(active, code)
		registry.finishDiscarded(active)
		return
	}
	if outcome.Successor == nil {
		if outcome.Finalize != nil || outcome.Result.Validate() != nil {
			registry.finishTerminal(active, cursorResult(api.ErrorIOError), true)
			return
		}
		registry.finishTerminal(active, outcome.Result, true)
		return
	}
	if outcome.Finalize == nil || !outcome.Progress.Valid() {
		registry.finishTerminal(active, cursorResult(api.ErrorBudgetExceeded), true)
		return
	}
	child, err := registry.previewSuccessor(active, outcome.Successor)
	if err != nil {
		registry.finishTerminal(active, cursorResult(CodeOf(err)), true)
		return
	}
	result, err := outcome.Finalize(child)
	if err != nil || result.Validate() != nil {
		registry.finishTerminal(active, cursorResult(api.ErrorIOError), true)
		return
	}
	committedChild, err := registry.materializeSuccessor(active.entry, outcome.Successor, result, outcome.Reservation)
	if err != nil || committedChild != child {
		if err == nil {
			err = errRegistryInvariant
		}
		registry.finishTerminal(active, cursorResult(CodeOf(err)), true)
		return
	}
	registry.finishPartial(active, result)
}

func (registry *Registry) computationStatus(active *computation) (api.ErrorCode, bool, bool) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if active == nil || active.finished || active.noCommit || registry.closed || !registry.entries.valid(active.entry) || registry.entries.runtime[active.entry.index] != active || active.waiterCount == 0 {
		return "", false, false
	}
	now := registry.clockNowLocked()
	if now >= registry.lineages.commitDeadline[active.lineage.index] {
		return api.ErrorCursorExpired, true, true
	}
	if errors.Is(active.ctx.Err(), context.DeadlineExceeded) {
		return active.deadlineCode, true, true
	}
	return "", false, true
}

func (registry *Registry) previewSuccessor(active *computation, successor State) (Token, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if active == nil || active.noCommit || active.waiterCount == 0 || !registry.entries.valid(active.entry) || registry.entries.runtime[active.entry.index] != active || !registry.lineages.valid(active.lineage) {
		return Token{}, cursorError(api.ErrorCursorExpired)
	}
	lineageIndex := active.lineage.index
	if registry.closed || registry.lineages.flags[lineageIndex]&lineagePhaseMask == 0 || registry.clockNowLocked() >= registry.lineages.workExpiresAt[lineageIndex] || registry.entries.flags[active.entry.index]&entryFlagMustTerminalize != 0 {
		return Token{}, cursorError(api.ErrorCursorExpired)
	}
	parent := registry.entries.state[active.entry.index]
	if parent == nil || successor == nil || successor.Tool() != parent.Tool() || successor.CWDID() != parent.CWDID() {
		return Token{}, cursorError(api.ErrorInvalidInput)
	}
	parentToken, _, ok := registry.tokenForEntryLocked(active.entry.index)
	if !ok {
		return Token{}, errRegistryInvariant
	}
	child := deriveChildToken(registry.secret, parentToken, successor.Digest())
	if _, exists := registry.tokenIndex.lookup(child, registry.tokenHome(child)); exists {
		return Token{}, errRegistryInvariant
	}
	return child, nil
}

func (registry *Registry) finishPartial(active *computation, result api.Result) {
	registry.mu.Lock()
	if active == nil || active.finished {
		registry.mu.Unlock()
		return
	}
	valid := !registry.closed && !active.noCommit && active.waiterCount != 0 && registry.entries.valid(active.entry) && registry.entries.runtime[active.entry.index] == active && registry.lineages.valid(active.lineage) && registry.lineages.flags[active.lineage.index]&lineagePhaseMask != 0
	head := active.detachAllLocked()
	if registry.entries.valid(active.entry) && registry.entries.runtime[active.entry.index] == active {
		registry.entries.runtime[active.entry.index] = nil
	}
	if registry.lineages.valid(active.lineage) && registry.lineages.pins[active.lineage.index] != 0 {
		registry.lineages.pins[active.lineage.index]--
	}
	active.finished = true
	active.cancel()
	work := active.work
	active.work = nil
	if !valid && registry.lineages.valid(active.lineage) {
		registry.tombstoneComputationLocked(active)
		registry.removeLineageLocked(active.lineage)
	}
	registry.mu.Unlock()
	deliverWaiters(head, result, valid)
	if work != nil {
		work.WorkerReturned()
	}
}

func (registry *Registry) finishTerminal(active *computation, result api.Result, offerResult bool) {
	registry.mu.Lock()
	if active == nil || active.finished {
		registry.mu.Unlock()
		return
	}
	deliver := offerResult && !registry.closed && !active.noCommit && active.waiterCount != 0 && registry.entries.valid(active.entry) && registry.entries.runtime[active.entry.index] == active
	head := active.detachAllLocked()
	registry.tombstoneComputationLocked(active)
	if registry.entries.valid(active.entry) && registry.entries.runtime[active.entry.index] == active {
		registry.entries.runtime[active.entry.index] = nil
	}
	if registry.lineages.valid(active.lineage) && registry.lineages.pins[active.lineage.index] != 0 {
		registry.lineages.pins[active.lineage.index]--
	}
	active.finished = true
	active.cancel()
	work := active.work
	active.work = nil
	if registry.lineages.valid(active.lineage) {
		registry.removeLineageLocked(active.lineage)
	}
	registry.mu.Unlock()
	deliverWaiters(head, result, deliver)
	if work != nil {
		work.WorkerReturned()
	}
}

func (registry *Registry) finishDiscarded(active *computation) {
	registry.mu.Lock()
	if active == nil || active.finished {
		registry.mu.Unlock()
		return
	}
	head := active.detachAllLocked()
	if registry.entries.valid(active.entry) && registry.entries.runtime[active.entry.index] == active {
		registry.entries.runtime[active.entry.index] = nil
	}
	if registry.lineages.valid(active.lineage) && registry.lineages.pins[active.lineage.index] != 0 {
		registry.lineages.pins[active.lineage.index]--
	}
	active.finished = true
	active.cancel()
	work := active.work
	active.work = nil
	if registry.lineages.valid(active.lineage) {
		registry.tombstoneComputationLocked(active)
		registry.removeLineageLocked(active.lineage)
	}
	registry.mu.Unlock()
	deliverWaiters(head, api.Result{}, false)
	if work != nil {
		work.WorkerReturned()
	}
}

func deliverImmediate(waiter Waiter, result api.Result, work *serverruntime.WorkLease) {
	if waiterIsCancelled(waiter) {
		waiter.CloseWithoutResponse()
	} else {
		waiter.Deliver(result)
	}
	work.WorkerReturned()
}

func deliverWaiters(head *continuationWaiter, result api.Result, deliver bool) {
	for node := head; node != nil; {
		next := node.next
		if deliver && !waiterIsCancelled(node.waiter) {
			node.waiter.Deliver(result)
		} else {
			node.waiter.CloseWithoutResponse()
		}
		if node.lease != nil {
			lease := node.lease
			node.lease = nil
			lease.WorkerReturned()
		}
		node = next
	}
}

func waiterIsCancelled(waiter Waiter) bool {
	if waiter == nil {
		return true
	}
	cancelled := waiter.Cancelled()
	if cancelled == nil {
		return false
	}
	select {
	case <-cancelled:
		return true
	default:
		return false
	}
}

func cursorResult(code api.ErrorCode) api.Result {
	if !code.Valid() {
		code = api.ErrorIOError
	}
	return api.Navigation("ERROR\t"+string(code)+"\n", true)
}
