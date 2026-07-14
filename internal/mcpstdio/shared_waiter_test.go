package mcpstdio

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/api"
	workruntime "github.com/Dirard/mcp-file-tools/internal/runtime"
)

func TestSharedWaitersKeepResultsIDFreeAndCancelIndependently(t *testing.T) {
	tests := []struct {
		name       string
		cancelIDs  []string
		wantFirst  bool
		wantSecond bool
	}{
		{name: "deliver both", wantFirst: true, wantSecond: true},
		{name: "cancel numeric waiter", cancelIDs: []string{`17`}, wantSecond: true},
		{name: "cancel string waiter", cancelIDs: []string{`"waiter-b"`}, wantFirst: true},
		{name: "cancel both waiters", cancelIDs: []string{`17`, `"waiter-b"`}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := newSharedWaiterExecutor()
			instrumentation := &connectionInstrumentation{}
			var output bytes.Buffer
			connection := newAdmissionTestConnection(workruntime.Limits{
				MaxConcurrent: 2,
				QueueMax:      0,
				QueueTimeout:  time.Second,
			}, executor, instrumentation, &output)

			sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":17,"method":"tools/call","params":{"name":"read","arguments":{"cursor":"same"}}}`)
			sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":"waiter-b","method":"tools/call","params":{"name":"read","arguments":{"cursor":"same"}}}`)
			awaitMCPStdIOSignal(t, executor.joined, "two shared waiters")

			for _, cancelID := range test.cancelIDs {
				sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":`+cancelID+`}}`)
			}
			wantLive := 2 - len(test.cancelIDs)
			awaitSharedWaiterCount(t, executor, wantLive)
			if wantLive == 0 {
				awaitMCPStdIOSignal(t, executor.computationCancelled, "last-waiter computation cancellation")
			} else {
				executor.finish()
			}
			connection.workers.Wait()

			wire := connectionOutput(connection, &output)
			firstPresent := strings.Contains(wire, `"id":17,"result"`)
			secondPresent := strings.Contains(wire, `"id":"waiter-b","result"`)
			if firstPresent != test.wantFirst || secondPresent != test.wantSecond {
				t.Fatalf("response presence first/second = %v/%v, want %v/%v: %s", firstPresent, secondPresent, test.wantFirst, test.wantSecond, wire)
			}
			if err := connection.loadAsyncError(); err != nil {
				t.Fatalf("shared waiter async error: %v", err)
			}
			if got := executor.computeCalls.Load(); got != 1 {
				t.Fatalf("shared compute calls = %d, want 1", got)
			}
			if got := instrumentation.workLeases.Load(); got != 2 {
				t.Fatalf("shared waiter work leases = %d, want 2", got)
			}
			if got := connectionRequestCount(connection); got != 0 {
				t.Fatalf("remaining transport requests = %d, want 0", got)
			}

			if test.wantFirst && test.wantSecond {
				assertSharedResultFragments(t, []byte(wire), executor.result)
			}
			connection.closeExecutor()
			executor.watchers.Wait()
		})
	}
}

func assertSharedResultFragments(t *testing.T, wire []byte, result api.Result) {
	t.Helper()
	payload, err := encodeToolResultPayload(result)
	if err != nil {
		t.Fatalf("encode ID-free result payload: %v", err)
	}
	trimmed := bytes.TrimSuffix(wire, []byte{'\n'})
	lines := bytes.Split(trimmed, []byte{'\n'})
	if len(lines) != 2 {
		t.Fatalf("shared response lines = %d, want 2: %q", len(lines), wire)
	}
	var numericLine, stringLine []byte
	for _, line := range lines {
		switch {
		case bytes.Contains(line, []byte(`"id":17,"result"`)):
			numericLine = line
		case bytes.Contains(line, []byte(`"id":"waiter-b","result"`)):
			stringLine = line
		}
	}
	if numericLine == nil || stringLine == nil {
		t.Fatalf("shared responses missing expected IDs: %q", wire)
	}
	wantEnvelopeDelta := len(`"waiter-b"`) - len(`17`)
	if got := len(stringLine) - len(numericLine); got != wantEnvelopeDelta {
		t.Fatalf("envelope length delta = %d, want raw-ID-only delta %d: %q", got, wantEnvelopeDelta, wire)
	}
	for index, line := range lines {
		marker := []byte(`,"result":`)
		resultStart := bytes.Index(line, marker)
		if resultStart < 0 || len(line) < resultStart+len(marker)+2 || line[len(line)-1] != '}' {
			t.Fatalf("response %d has no bounded result fragment: %q", index, line)
		}
		fragment := line[resultStart+len(marker) : len(line)-1]
		if !bytes.Equal(fragment, payload) {
			t.Fatalf("response %d result fragment differs\n got: %s\nwant: %s", index, fragment, payload)
		}
	}
}

type sharedWaiterExecutor struct {
	mu                    sync.Mutex
	waiters               map[*workruntime.Waiter]struct{}
	terminal              bool
	result                api.Result
	computeCalls          atomic.Uint64
	joined                chan struct{}
	joinedOnce            sync.Once
	computationCancelled  chan struct{}
	computationCancelOnce sync.Once
	watchers              sync.WaitGroup
}

func newSharedWaiterExecutor() *sharedWaiterExecutor {
	return &sharedWaiterExecutor{
		waiters:              make(map[*workruntime.Waiter]struct{}),
		result:               api.Navigation("ERROR\tshared\n", true),
		joined:               make(chan struct{}),
		computationCancelled: make(chan struct{}),
	}
}

func (executor *sharedWaiterExecutor) Call(ctx context.Context, _ api.Call, work *workruntime.WorkLease) workruntime.Execution {
	waiter := workruntime.NewWaiter(ctx.Done())
	executor.mu.Lock()
	if len(executor.waiters) == 0 && !executor.terminal {
		executor.computeCalls.Add(1)
	}
	executor.waiters[waiter] = struct{}{}
	if len(executor.waiters) == 2 {
		executor.joinedOnce.Do(func() { close(executor.joined) })
	}
	executor.mu.Unlock()

	executor.watchers.Add(1)
	go func() {
		defer executor.watchers.Done()
		select {
		case <-waiter.Cancelled():
			executor.cancel(waiter)
		case <-waiter.Done():
		}
	}()

	result, delivered := waiter.Await()
	work.WorkerReturned()
	if !delivered {
		return workruntime.Execution{}
	}
	return workruntime.Execution{Kind: workruntime.ExecutionOrdinary, Result: result}
}

func (executor *sharedWaiterExecutor) cancel(waiter *workruntime.Waiter) {
	executor.mu.Lock()
	if _, exists := executor.waiters[waiter]; !exists {
		executor.mu.Unlock()
		return
	}
	delete(executor.waiters, waiter)
	last := len(executor.waiters) == 0 && !executor.terminal
	if last {
		executor.terminal = true
	}
	executor.mu.Unlock()
	waiter.CloseWithoutResponse()
	if last {
		executor.computationCancelOnce.Do(func() { close(executor.computationCancelled) })
	}
}

func (executor *sharedWaiterExecutor) finish() {
	executor.mu.Lock()
	if executor.terminal {
		executor.mu.Unlock()
		return
	}
	executor.terminal = true
	waiters := make([]*workruntime.Waiter, 0, len(executor.waiters))
	for waiter := range executor.waiters {
		waiters = append(waiters, waiter)
	}
	clear(executor.waiters)
	executor.mu.Unlock()
	for _, waiter := range waiters {
		waiter.Deliver(executor.result)
	}
}

func (executor *sharedWaiterExecutor) Close() {
	executor.mu.Lock()
	waiters := make([]*workruntime.Waiter, 0, len(executor.waiters))
	for waiter := range executor.waiters {
		waiters = append(waiters, waiter)
	}
	clear(executor.waiters)
	executor.terminal = true
	executor.mu.Unlock()
	for _, waiter := range waiters {
		waiter.CloseWithoutResponse()
	}
}

func (executor *sharedWaiterExecutor) waiterCount() int {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return len(executor.waiters)
}

func awaitSharedWaiterCount(t *testing.T, executor *sharedWaiterExecutor, want int) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if executor.waiterCount() == want {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for %d shared waiters; got %d", want, executor.waiterCount())
		case <-ticker.C:
		}
	}
}
