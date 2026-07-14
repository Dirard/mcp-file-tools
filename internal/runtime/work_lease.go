package runtime

import (
	"errors"
	"sync"
)

var (
	errLeaseNoCommit = errors.New("runtime: work lease cannot transfer after no-commit")
	errLeaseReturned = errors.New("runtime: work lease already returned")
	errNilOwner      = errors.New("runtime: work lease owner is nil")
)

// WorkLease keeps one active work slot charged until the actual worker returns.
type WorkLease struct {
	mu              sync.Mutex
	origin          *workLeaseOrigin
	owner           Owner
	ownerGeneration uint64
	noCommit        bool
	returned        bool
	release         func()
}

type workLeaseOrigin struct {
	lease *WorkLease
}

func newWorkLease(release func()) *WorkLease {
	if release == nil {
		panic("runtime: work lease release is nil")
	}
	lease := &WorkLease{release: release}
	lease.origin = &workLeaseOrigin{lease: lease}
	return lease
}

func (lease *WorkLease) subAcquireAllowed() bool {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.origin == nil || lease.origin.lease != lease {
		panic("runtime: sub-limiter parent was not issued by a coordinator")
	}
	return !lease.noCommit && !lease.returned
}

// Transfer moves resource cleanup to a new exactly-once owner.
func (lease *WorkLease) Transfer(owner Owner) error {
	if owner == nil {
		return errNilOwner
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.returned {
		return errLeaseReturned
	}
	if lease.noCommit {
		return errLeaseNoCommit
	}
	lease.owner = owner
	lease.ownerGeneration++
	return nil
}

// MarkNoCommit prevents later publication while retaining the work slot.
func (lease *WorkLease) MarkNoCommit() {
	for {
		lease.mu.Lock()
		if lease.noCommit || lease.returned {
			lease.mu.Unlock()
			return
		}
		owner := lease.owner
		generation := lease.ownerGeneration
		shared, sharedOwner := owner.(SharedNoCommitOwner)
		if !sharedOwner {
			lease.noCommit = true
			notify, shouldNotify := owner.(NoCommitOwner)
			lease.mu.Unlock()
			if shouldNotify {
				notify.MarkNoCommit()
			}
			return
		}
		lease.mu.Unlock()

		retain := shared.HandleSharedNoCommit()
		lease.mu.Lock()
		if lease.noCommit || lease.returned {
			lease.mu.Unlock()
			return
		}
		if lease.owner != owner || lease.ownerGeneration != generation {
			lease.mu.Unlock()
			continue
		}
		if retain {
			lease.mu.Unlock()
			return
		}
		lease.noCommit = true
		lease.mu.Unlock()
		return
	}
}

// WorkerReturned releases the charged work slot exactly once.
func (lease *WorkLease) WorkerReturned() {
	lease.mu.Lock()
	if lease.returned {
		lease.mu.Unlock()
		return
	}
	lease.returned = true
	owner := lease.owner
	lease.owner = nil
	release := lease.release
	lease.release = nil
	lease.mu.Unlock()
	if owner != nil {
		owner.Release()
	}
	if release != nil {
		release()
	}
}
