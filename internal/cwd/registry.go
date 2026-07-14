package cwd

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/config"
	"github.com/Dirard/mcp-file-tools/internal/rootfs"
)

const maxSafeID ID = 9_007_199_254_740_991

// ID is a process-local cwd identifier that is exactly representable by JSON clients.
type ID uint64

type identityKey struct {
	canonical string
	identity  rootfs.Identity
}

type entry struct {
	root      *rootfs.Root
	key       identityKey
	expiresAt time.Time
}

// Registry owns opened roots and hands callers independently owned leases.
type Registry struct {
	mu         sync.Mutex
	next       ID
	byID       map[ID]*entry
	byIdentity map[identityKey]ID
	closed     bool
	ttl        time.Duration
	maxEntries uint64
	clock      Clock
}

// Stats is a compact test/debug view that does not expose roots or identities.
type Stats struct {
	ActiveEntries uint64
	NextID        ID
	Closed        bool
}

// New creates an empty process-local registry. It does not touch the filesystem.
func New(cfg config.Runtime, clock Clock) *Registry {
	if clock == nil {
		clock = systemClock{}
	}
	return &Registry{
		next:       1,
		byID:       make(map[ID]*entry),
		byIdentity: make(map[identityKey]ID),
		ttl:        cfg.CWDTTL,
		maxEntries: cfg.CWDMaxEntries,
		clock:      clock,
	}
}

// Register consumes root on every outcome.
func (registry *Registry) Register(root *rootfs.Root) (ID, bool, api.ErrorCode) {
	if root == nil {
		return 0, false, api.ErrorInvalidInput
	}
	key := identityKey{
		canonical: root.CanonicalPath(),
		identity:  root.Identity(),
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	now := registry.clock.Now()
	if registry.closed {
		_ = root.Close()
		return 0, false, api.ErrorIOError
	}
	registry.sweepExpiredLocked(now)
	if id, found := registry.byIdentity[key]; found {
		_ = root.Close()
		return id, false, ""
	}
	if uint64(len(registry.byID)) >= registry.maxEntries || registry.next == 0 || registry.next > maxSafeID {
		_ = root.Close()
		return 0, false, api.ErrorBudgetExceeded
	}

	id := registry.next
	registry.next++
	registry.byID[id] = &entry{
		root:      root,
		key:       key,
		expiresAt: now.Add(registry.ttl),
	}
	registry.byIdentity[key] = id
	return id, true, ""
}

// Lookup duplicates a root while holding the registry lock through expiry.
func (registry *Registry) Lookup(id ID) (*rootfs.Lease, api.ErrorCode) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	now := registry.clock.Now()
	if registry.closed || id == 0 || id > maxSafeID {
		return nil, api.ErrorCWDUnknown
	}
	registry.sweepExpiredLocked(now)
	current, found := registry.byID[id]
	if !found {
		return nil, api.ErrorCWDUnknown
	}
	lease, err := current.root.Duplicate()
	if err == nil {
		return lease, ""
	}
	if errors.Is(err, rootfs.ErrClosed) {
		return nil, api.ErrorCWDUnknown
	}
	return nil, api.ErrorIOError
}

// Close releases every registry-owned root exactly once. Existing leases remain owned by callers.
func (registry *Registry) Close() error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.closed {
		return nil
	}
	registry.closed = true

	ids := make([]ID, 0, len(registry.byID))
	for id := range registry.byID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
	var firstErr error
	for _, id := range ids {
		if err := registry.byID[id].root.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	registry.byID = nil
	registry.byIdentity = nil
	return firstErr
}

// StatsForTest returns accounting state without exposing owned handles.
func (registry *Registry) StatsForTest() Stats {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return Stats{
		ActiveEntries: uint64(len(registry.byID)),
		NextID:        registry.next,
		Closed:        registry.closed,
	}
}

func (registry *Registry) sweepExpiredLocked(now time.Time) {
	if len(registry.byID) == 0 {
		return
	}
	ids := make([]ID, 0)
	for id, current := range registry.byID {
		if !now.Before(current.expiresAt) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
	for _, id := range ids {
		current := registry.byID[id]
		delete(registry.byID, id)
		if mapped, found := registry.byIdentity[current.key]; found && mapped == id {
			delete(registry.byIdentity, current.key)
		}
		_ = current.root.Close()
	}
}
