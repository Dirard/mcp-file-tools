package cursor

import (
	"errors"
	"sync"

	"github.com/Dirard/mcp-file-tools/internal/config"
	serverruntime "github.com/Dirard/mcp-file-tools/internal/runtime"
)

var errPublicationState = errors.New("cursor: invalid publication state")

// InitialPublication keeps rollback armed until the complete response is written.
type InitialPublication struct {
	mu           sync.Mutex
	registry     *Registry
	lineage      lineageRef
	work         *serverruntime.WorkLease
	armed        bool
	prepared     bool
	terminal     bool
	workReturned bool
}

// Prepare makes a provisional lineage lookup-visible while retaining rollback.
func (publication *InitialPublication) Prepare() error {
	if publication == nil {
		return errPublicationState
	}
	publication.mu.Lock()
	defer publication.mu.Unlock()
	if publication.terminal || !publication.armed || publication.prepared || publication.registry == nil {
		return errPublicationState
	}
	registry := publication.registry
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.closed || !registry.lineages.valid(publication.lineage) || registry.lineages.flags[publication.lineage.index]&lineagePhaseMask != lineagePhaseProvisional {
		return errPublicationState
	}
	registry.lineages.flags[publication.lineage.index] = lineagePhasePublishing
	protectedUntil := addDuration(registry.clockNowLocked(), config.CursorHandoffGrace)
	if protectedUntil > registry.lineages.protectedUntil[publication.lineage.index] {
		registry.lineages.protectedUntil[publication.lineage.index] = protectedUntil
	}
	publication.prepared = true
	return nil
}

// Commit publishes the lineage and returns the transferred work slot.
func (publication *InitialPublication) Commit() error {
	if publication == nil {
		return errPublicationState
	}
	publication.mu.Lock()
	if publication.terminal || !publication.armed || !publication.prepared || publication.registry == nil {
		publication.mu.Unlock()
		return errPublicationState
	}
	registry := publication.registry
	registry.mu.Lock()
	valid := !registry.closed && registry.lineages.valid(publication.lineage) && registry.lineages.flags[publication.lineage.index]&lineagePhaseMask == lineagePhasePublishing
	if valid {
		registry.lineages.flags[publication.lineage.index] = lineagePhasePublished
	}
	registry.mu.Unlock()
	publication.armed = false
	publication.terminal = true
	work := publication.takeWorkLocked()
	publication.mu.Unlock()
	if work != nil {
		work.WorkerReturned()
	}
	if !valid {
		return errPublicationState
	}
	return nil
}

// Abort rolls back an armed lineage and returns the transferred work slot.
func (publication *InitialPublication) Abort() {
	publication.rollback(true, false)
}

// MarkNoCommit rolls back publication but leaves the running worker charged.
func (publication *InitialPublication) MarkNoCommit() {
	publication.rollback(false, false)
}

// Release is invoked by WorkLease.WorkerReturned and rolls back only while armed.
func (publication *InitialPublication) Release() {
	publication.rollback(false, true)
}

func (publication *InitialPublication) rollback(returnWork, calledByWork bool) {
	if publication == nil {
		return
	}
	publication.mu.Lock()
	if calledByWork {
		publication.workReturned = true
	}
	if !publication.terminal {
		publication.terminal = true
		publication.armed = false
		if registry := publication.registry; registry != nil {
			registry.mu.Lock()
			registry.removeLineageLocked(publication.lineage)
			registry.mu.Unlock()
		}
	}
	var work *serverruntime.WorkLease
	if returnWork {
		work = publication.takeWorkLocked()
	}
	publication.mu.Unlock()
	if work != nil {
		work.WorkerReturned()
	}
}

func (publication *InitialPublication) takeWorkLocked() *serverruntime.WorkLease {
	if publication.workReturned {
		return nil
	}
	publication.workReturned = true
	return publication.work
}
