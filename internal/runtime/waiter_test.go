package runtime

import (
	"sync"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

func TestWaiterDeliversIDFreeResultOrClosesWithoutResponse(t *testing.T) {
	cancelled := make(chan struct{})
	delivered := NewWaiter(cancelled)
	if delivered.Cancelled() != cancelled {
		t.Fatal("waiter changed its cancellation channel")
	}
	select {
	case <-delivered.Done():
		t.Fatal("new waiter is already terminal")
	default:
	}
	want := api.Navigation("DATA\tshared\n", false)
	delivered.Deliver(want)
	got, ok := delivered.Await()
	if !ok || got != want {
		t.Fatalf("delivered waiter = %#v/%v, want %#v/true", got, ok, want)
	}
	gotAgain, okAgain := delivered.Await()
	if !okAgain || gotAgain != want {
		t.Fatalf("repeated Await = %#v/%v, want stable delivery", gotAgain, okAgain)
	}
	delivered.CloseWithoutResponse()
	if gotAfterClose, okAfterClose := delivered.Await(); !okAfterClose || gotAfterClose != want {
		t.Fatalf("late close changed delivery to %#v/%v", gotAfterClose, okAfterClose)
	}

	closed := NewWaiter(nil)
	if closed.Cancelled() != nil {
		t.Fatal("nil cancellation channel gained a synthetic signal")
	}
	closed.CloseWithoutResponse()
	if got, ok := closed.Await(); ok || got != (api.Result{}) {
		t.Fatalf("closed waiter = %#v/%v, want zero/false", got, ok)
	}
	closed.Deliver(want)
	if got, ok := closed.Await(); ok || got != (api.Result{}) {
		t.Fatalf("late delivery changed closed waiter to %#v/%v", got, ok)
	}
}

func TestWaiterDeliveryAndCloseRaceHasOneStableWinner(t *testing.T) {
	want := api.Navigation("DATA\trace\n", false)
	for iteration := 0; iteration < 1_000; iteration++ {
		waiter := NewWaiter(nil)
		start := make(chan struct{})
		var racers sync.WaitGroup
		racers.Add(2)
		go func() {
			defer racers.Done()
			<-start
			waiter.Deliver(want)
		}()
		go func() {
			defer racers.Done()
			<-start
			waiter.CloseWithoutResponse()
		}()
		close(start)
		racers.Wait()

		got, delivered := waiter.Await()
		if delivered && got != want {
			t.Fatalf("iteration %d delivered %#v, want %#v", iteration, got, want)
		}
		if !delivered && got != (api.Result{}) {
			t.Fatalf("iteration %d closed with non-zero result %#v", iteration, got)
		}
		select {
		case <-waiter.Done():
		default:
			t.Fatalf("iteration %d has no terminal Done signal", iteration)
		}
		gotAgain, deliveredAgain := waiter.Await()
		if gotAgain != got || deliveredAgain != delivered {
			t.Fatalf("iteration %d changed outcome from %#v/%v to %#v/%v", iteration, got, delivered, gotAgain, deliveredAgain)
		}
	}
}
