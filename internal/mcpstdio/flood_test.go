package mcpstdio

import (
	"context"
	"fmt"
	stdruntime "runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/api"
	workruntime "github.com/Dirard/mcp-file-tools/internal/runtime"
)

func TestMixedSaturationFloodRetainsOnlyBoundedTransportState(t *testing.T) {
	const (
		activeMax = 2
		queueMax  = 3
	)
	executor := newBoundedFloodExecutor(activeMax)
	output := &countingDiscardWriter{}
	fatal := workruntime.NewFatalSignal()
	limits := workruntime.Limits{
		MaxConcurrent: activeMax,
		QueueMax:      queueMax,
		QueueTimeout:  time.Hour,
	}
	connection := &stdioConnection{
		executor:     executor,
		coordinator:  workruntime.NewCoordinatorWithFatal(limits, fatal),
		fatal:        fatal,
		lifecycle:    &connectionLifecycle{state: lifecycleReady},
		usedIDs:      newUsedIDRegistry(),
		output:       output,
		protocolBusy: newProtocolBusyQueue(),
		toolOutputs:  mustNewToolOutputLimiter(limits),
		toolRequests: make(map[SemanticIDKey]*toolRequest),
	}
	initialArenaCapacity := cap(connection.usedIDs.arena)
	initialSlotCapacity := cap(connection.usedIDs.slots)
	stdruntime.GC()
	var before stdruntime.MemStats
	stdruntime.ReadMemStats(&before)

	maxRequests := 0
	sendFloodTool := func(id int, path string) {
		t.Helper()
		sendConnectionFrame(t, connection, fmt.Sprintf(
			`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"read","arguments":{"path":"%s"}}}`,
			id,
			path,
		))
		current := connectionRequestCount(connection)
		if current > maxRequests {
			maxRequests = current
		}
		if current > activeMax+queueMax+1 {
			t.Fatalf("transport request state = %d, limit %d", current, activeMax+queueMax+1)
		}
	}

	sendFloodTool(1, "active-a")
	sendFloodTool(2, "active-b")
	awaitMCPStdIOSignal(t, executor.initialStarted, "initial active flood workers")
	for id := 3; id <= 5; id++ {
		sendFloodTool(id, "queued")
	}
	if got := connectionRequestCount(connection); got != activeMax+queueMax {
		t.Fatalf("full active+queue state = %d, want %d", got, activeMax+queueMax)
	}
	for id := 6; id <= 1_005; id++ {
		sendFloodTool(id, "overflow")
	}
	if got := connectionRequestCount(connection); got != activeMax+queueMax {
		t.Fatalf("overflow grew request state to %d", got)
	}

	sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":2000,"method":"tools/call","params":{"name":"unknown","arguments":{}}}`)
	sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","id":2001,"method":"ping"}`)
	sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":"p","progress":1}}`)
	for id := 3; id <= 5; id++ {
		sendConnectionFrame(t, connection, fmt.Sprintf(
			`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":%d}}`,
			id,
		))
	}
	awaitConnectionRequestCount(t, connection, activeMax)
	executor.releaseInitial()
	connection.workers.Wait()
	if got := connectionRequestCount(connection); got != 0 {
		t.Fatalf("request state after saturated workers returned = %d", got)
	}

	const completedFrames = 5_000
	for index := 0; index < completedFrames; index++ {
		sendFloodTool(10_000+index, "complete")
		if index%16 == 0 {
			sendConnectionFrame(t, connection, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"ping"}`, 20_000+index))
			sendConnectionFrame(t, connection, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"read","arguments":[]}}`, 30_000+index))
			sendConnectionFrame(t, connection, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"unknown"}}`)
		}
		if index%32 == 31 {
			connection.workers.Wait()
			if got := connectionRequestCount(connection); got != 0 {
				t.Fatalf("completed batch %d retained %d requests", index/32, got)
			}
		}
	}
	connection.workers.Wait()
	sendFloodTool(60_000, "final-reuse")
	connection.workers.Wait()
	if got := connectionRequestCount(connection); got != 0 {
		t.Fatalf("final completed request state = %d", got)
	}
	if maxRequests < activeMax+queueMax || maxRequests > activeMax+queueMax+1 {
		t.Fatalf("maximum observed request state = %d, want saturated range %d..%d", maxRequests, activeMax+queueMax, activeMax+queueMax+1)
	}
	if cap(connection.usedIDs.arena) != initialArenaCapacity || cap(connection.usedIDs.slots) != initialSlotCapacity {
		t.Fatalf("used-ID backing grew: arena %d/%d slots %d/%d", cap(connection.usedIDs.arena), initialArenaCapacity, cap(connection.usedIDs.slots), initialSlotCapacity)
	}
	if activeOutputs := connection.toolOutputs.active(); activeOutputs != 0 {
		t.Fatalf("completed flood retained %d tool output slots", activeOutputs)
	}
	if executor.calls.Load() <= activeMax || output.writes.Load() == 0 || output.bytes.Load() == 0 {
		t.Fatalf("flood did not exercise executor/output: calls=%d writes=%d bytes=%d", executor.calls.Load(), output.writes.Load(), output.bytes.Load())
	}

	stdruntime.GC()
	stdruntime.GC()
	var after stdruntime.MemStats
	stdruntime.ReadMemStats(&after)
	if after.HeapAlloc > before.HeapAlloc+8<<20 {
		t.Fatalf("retained heap grew by %d bytes, limit %d", after.HeapAlloc-before.HeapAlloc, 8<<20)
	}
	connection.closeExecutor()
	if got := executor.closed.Load(); got != 1 {
		t.Fatalf("executor closes = %d, want 1", got)
	}
}

type boundedFloodExecutor struct {
	blockCalls      uint64
	calls           atomic.Uint64
	closed          atomic.Uint64
	started         atomic.Uint64
	initialStarted  chan struct{}
	initialStartOne sync.Once
	initialRelease  chan struct{}
	releaseOnce     sync.Once
}

func newBoundedFloodExecutor(blockCalls uint64) *boundedFloodExecutor {
	return &boundedFloodExecutor{
		blockCalls:     blockCalls,
		initialStarted: make(chan struct{}),
		initialRelease: make(chan struct{}),
	}
}

func (executor *boundedFloodExecutor) Call(_ context.Context, _ api.Call, work *workruntime.WorkLease) workruntime.Execution {
	callNumber := executor.calls.Add(1)
	if callNumber <= executor.blockCalls {
		if executor.started.Add(1) == executor.blockCalls {
			executor.initialStartOne.Do(func() { close(executor.initialStarted) })
		}
		<-executor.initialRelease
	}
	work.WorkerReturned()
	return workruntime.Execution{Kind: workruntime.ExecutionOrdinary, Result: api.Navigation("DATA\tok\n", false)}
}

func (executor *boundedFloodExecutor) Close() {
	executor.closed.Add(1)
	executor.releaseInitial()
}

func (executor *boundedFloodExecutor) releaseInitial() {
	executor.releaseOnce.Do(func() { close(executor.initialRelease) })
}

type countingDiscardWriter struct {
	writes atomic.Uint64
	bytes  atomic.Uint64
}

func (writer *countingDiscardWriter) Write(data []byte) (int, error) {
	writer.writes.Add(1)
	writer.bytes.Add(uint64(len(data)))
	return len(data), nil
}
