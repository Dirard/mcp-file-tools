package runtime

import (
	"errors"
	"sync"
	"sync/atomic"
)

// ErrInternalFatal is the detail-free terminal signal for a recovered runtime invariant.
var ErrInternalFatal = errors.New("runtime: internal fatal")

// FatalSignal converges all owned goroutine failures for one connection.
type FatalSignal struct {
	once       sync.Once
	transition sync.Mutex
	triggering atomic.Bool
	done       chan struct{}
}

// NewFatalSignal constructs one connection-local fatal signal.
func NewFatalSignal() *FatalSignal {
	return &FatalSignal{done: make(chan struct{})}
}

// Done closes exactly once when any owned entry recovers a panic.
func (signal *FatalSignal) Done() <-chan struct{} {
	if signal == nil {
		panic("runtime: fatal signal is nil")
	}
	return signal.done
}

// Triggered reports whether the signal has reached its terminal state.
func (signal *FatalSignal) Triggered() bool {
	select {
	case <-signal.Done():
		return true
	default:
		return false
	}
}

// RunBeforeFatal linearizes one complete operation before the fatal transition.
// It returns false without running operation once the fatal transition starts.
func (signal *FatalSignal) RunBeforeFatal(operation func()) bool {
	if signal == nil {
		panic("runtime: fatal signal is nil")
	}
	if operation == nil {
		panic("runtime: fatal operation is nil")
	}
	signal.transition.Lock()
	defer signal.transition.Unlock()
	if signal.triggering.Load() || signal.Triggered() {
		return false
	}
	operation()
	return true
}

// Run guards one owned goroutine entry. Panic values are deliberately discarded.
// Cleanup runs once for this failed entry and cleanup panics cannot escape.
func (signal *FatalSignal) Run(operation func(), cleanup func()) (panicked bool) {
	if signal == nil {
		panic("runtime: fatal signal is nil")
	}
	defer func() {
		if recover() == nil {
			return
		}
		signal.once.Do(func() {
			signal.triggering.Store(true)
			signal.transition.Lock()
			close(signal.done)
			signal.transition.Unlock()
		})
		runFatalCleanup(cleanup)
		panicked = true
	}()
	operation()
	return false
}

func runFatalCleanup(cleanup func()) {
	if cleanup == nil {
		return
	}
	defer func() { _ = recover() }()
	cleanup()
}
