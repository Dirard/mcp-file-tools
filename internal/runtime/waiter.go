package runtime

import (
	"sync"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

// Waiter adapts one cancellation signal to one stable ID-free result outcome.
type Waiter struct {
	cancelled <-chan struct{}
	done      chan struct{}
	once      sync.Once
	mu        sync.Mutex
	result    api.Result
	delivered bool
}

// NewWaiter constructs an independently cancellable shared-result waiter.
func NewWaiter(cancelled <-chan struct{}) *Waiter {
	return &Waiter{
		cancelled: cancelled,
		done:      make(chan struct{}),
	}
}

// Deliver commits one immutable result to this waiter.
func (waiter *Waiter) Deliver(result api.Result) {
	waiter.complete(result, true)
}

// Cancelled exposes only the owning request's cancellation signal.
func (waiter *Waiter) Cancelled() <-chan struct{} {
	return waiter.cancelled
}

// CloseWithoutResponse terminalizes this waiter without a result.
func (waiter *Waiter) CloseWithoutResponse() {
	waiter.complete(api.Result{}, false)
}

// Done closes after either delivery or response-free closure wins.
func (waiter *Waiter) Done() <-chan struct{} {
	return waiter.done
}

// Await returns the stable terminal outcome and may be called repeatedly.
func (waiter *Waiter) Await() (api.Result, bool) {
	<-waiter.done
	waiter.mu.Lock()
	defer waiter.mu.Unlock()
	return waiter.result, waiter.delivered
}

func (waiter *Waiter) complete(result api.Result, delivered bool) {
	waiter.once.Do(func() {
		waiter.mu.Lock()
		waiter.result = result
		waiter.delivered = delivered
		waiter.mu.Unlock()
		close(waiter.done)
	})
}
