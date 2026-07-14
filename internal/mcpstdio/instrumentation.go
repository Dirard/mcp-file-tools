package mcpstdio

import (
	"sync"
	"sync/atomic"

	workruntime "github.com/Dirard/mcp-file-tools/internal/runtime"
)

type connectionInstrumentation struct {
	admissionAttempts  atomic.Uint64
	workerTasks        atomic.Uint64
	workLeases         atomic.Uint64
	leasesMu           sync.Mutex
	startedLeases      []*workruntime.WorkLease
	afterResponseClaim func(timeout bool)
}

func (instrumentation *connectionInstrumentation) recordResponseClaim(timeout bool) {
	if instrumentation == nil || instrumentation.afterResponseClaim == nil {
		return
	}
	instrumentation.afterResponseClaim(timeout)
}

func (instrumentation *connectionInstrumentation) recordWorkLease(work *workruntime.WorkLease) {
	instrumentation.workLeases.Add(1)
	instrumentation.leasesMu.Lock()
	instrumentation.startedLeases = append(instrumentation.startedLeases, work)
	instrumentation.leasesMu.Unlock()
}

func (instrumentation *connectionInstrumentation) leases() []*workruntime.WorkLease {
	instrumentation.leasesMu.Lock()
	defer instrumentation.leasesMu.Unlock()
	return append([]*workruntime.WorkLease(nil), instrumentation.startedLeases...)
}
