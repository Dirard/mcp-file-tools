package cursor

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"math/bits"
	"sync"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/config"
	"github.com/Dirard/mcp-file-tools/internal/rootfs"
	serverruntime "github.com/Dirard/mcp-file-tools/internal/runtime"
)

const (
	entryKindResident uint8 = iota + 1
	entryKindTombstone
)

const lineagePhaseMask uint16 = 0x0003

const (
	lineagePhaseProvisional uint16 = iota + 1
	lineagePhasePublishing
	lineagePhasePublished
)

const lineageRollbackPending uint16 = 1 << 2

var errRegistryInvariant = errors.New("cursor: registry invariant failed")

type registryError struct{ code api.ErrorCode }

func (err *registryError) Error() string { return "cursor: " + string(err.code) }

// CodeOf returns a stable public error code for registry admission failures.
func CodeOf(err error) api.ErrorCode {
	var coded *registryError
	if errors.As(err, &coded) {
		return coded.code
	}
	return api.ErrorIOError
}

func cursorError(code api.ErrorCode) error { return &registryError{code: code} }

// Registry owns all cursor state for exactly one MCP connection.
type Registry struct {
	mu            sync.Mutex
	cfg           config.Runtime
	secret        [32]byte
	entropy       io.Reader
	clock         Clock
	tokenIndex    tokenIndex
	lineageIndex  lineageIndex
	entries       entryArena
	lineages      lineageArena
	layout        Layout
	baseBytes     uint64
	usedSlots     uint64
	totalBytes    uint64
	closed        bool
	nextLineageID uint64
}

// New constructs one fixed-capacity connection-local registry.
func New(cfg config.Runtime) (*Registry, error) {
	registry := &Registry{cfg: cfg, entropy: rand.Reader, clock: wallClock{}}
	if _, err := io.ReadFull(registry.entropy, registry.secret[:]); err != nil {
		return nil, cursorError(api.ErrorIOError)
	}
	if err := registry.initialize(); err != nil {
		registry.secret = [32]byte{}
		return nil, err
	}
	return registry, nil
}

func (registry *Registry) initialize() error {
	if registry == nil || registry.entropy == nil || registry.clock == nil {
		return cursorError(api.ErrorInvalidInput)
	}
	if registry.cfg.CursorMaxEntries == 0 || registry.cfg.CursorTTL <= 0 || registry.cfg.CursorMaxEntryBytes == 0 || registry.cfg.CursorMaxTotalBytes == 0 || registry.cfg.CursorMaxPages == 0 || registry.cfg.CursorMaxEntryBytes > registry.cfg.CursorMaxTotalBytes {
		return cursorError(api.ErrorInvalidInput)
	}
	layout, err := ContainerLayout(registry.cfg.CursorMaxEntries)
	if err != nil {
		return cursorError(api.ErrorInvalidInput)
	}
	if layout.Total > registry.cfg.CursorMaxTotalBytes {
		return cursorError(api.ErrorBudgetExceeded)
	}
	tokens, err := newTokenIndex(layout.IndexSlots)
	if err != nil {
		return cursorError(api.ErrorInvalidInput)
	}
	lineageKeys, err := newLineageIndex(layout.IndexSlots)
	if err != nil {
		return cursorError(api.ErrorInvalidInput)
	}
	entries, err := newEntryArena(layout.MaxEntries)
	if err != nil {
		return cursorError(api.ErrorInvalidInput)
	}
	lineages, err := newLineageArena(layout.MaxEntries)
	if err != nil {
		return cursorError(api.ErrorInvalidInput)
	}

	registry.cfg.IgnoreDirsAdd = nil
	registry.tokenIndex = tokens
	registry.lineageIndex = lineageKeys
	registry.entries = entries
	registry.lineages = lineages
	registry.layout = layout
	registry.baseBytes = layout.Total
	registry.totalBytes = layout.Total
	return nil
}

// CommitDynamicInitial installs a provisional project/search lineage.
func (registry *Registry) CommitDynamicInitial(request DynamicInitial, work *serverruntime.WorkLease) (commit InitialCommit, err error) {
	rootOwned := request.Root != nil
	defer func() {
		if err != nil && rootOwned {
			_ = request.Root.Close()
		}
	}()

	if request.State == nil || work == nil {
		return InitialCommit{}, cursorError(api.ErrorInvalidInput)
	}
	cfg, ok := registry.configSnapshot()
	if !ok {
		return InitialCommit{}, cursorError(api.ErrorCursorExpired)
	}
	if cfg.CursorMaxPages < 2 {
		return InitialCommit{}, cursorError(api.ErrorBudgetExceeded)
	}
	if _, shared := request.State.SharedDigest(); shared {
		return InitialCommit{}, cursorError(api.ErrorInvalidInput)
	}
	if request.SummaryPlan.Kind != BroadSummaryFinal || request.SummaryPlan.Slots != 1 || request.SummaryPlan.MustTerminalize {
		return InitialCommit{}, cursorError(api.ErrorInvalidInput)
	}

	commit, installed, commitErr := registry.commitInitial(initialRequest{
		state:       request.State,
		root:        request.Root,
		reservation: request.SummaryPlan,
	}, work, cfg)
	if installed {
		rootOwned = false
	}
	return commit, commitErr
}

// CommitReadInitial installs a provisional immutable read plan.
func (registry *Registry) CommitReadInitial(request ReadInitial, work *serverruntime.WorkLease) (InitialCommit, error) {
	if request.State == nil || request.Shared == nil || work == nil {
		return InitialCommit{}, cursorError(api.ErrorInvalidInput)
	}
	cfg, ok := registry.configSnapshot()
	if !ok {
		return InitialCommit{}, cursorError(api.ErrorCursorExpired)
	}
	sharedDigest, shared := request.State.SharedDigest()
	if !shared || sharedDigest != request.Shared.Digest() {
		return InitialCommit{}, cursorError(api.ErrorInvalidInput)
	}
	if request.PagePlan.Pages < 2 || uint64(request.PagePlan.Pages) > cfg.CursorMaxPages || request.PagePlan.Slots != uint64(request.PagePlan.Pages-1) {
		return InitialCommit{}, cursorError(api.ErrorInvalidInput)
	}
	returnFirst, _, err := registry.commitInitial(initialRequest{
		state:  request.State,
		shared: request.Shared,
		reservation: ReservationUse{
			Kind:  ReadPagePlan,
			Slots: request.PagePlan.Slots,
			Bytes: request.PagePlan.Bytes,
		},
	}, work, cfg)
	return returnFirst, err
}

type initialRequest struct {
	state       State
	root        *rootfs.Lease
	shared      SharedAllocation
	reservation ReservationUse
}

func (registry *Registry) commitInitial(request initialRequest, work *serverruntime.WorkLease, cfg config.Runtime) (InitialCommit, bool, error) {
	stateBytes := request.state.Footprint()
	if !request.state.Tool().Valid() || request.state.CWDID() == 0 || request.state.CWDID() > 1<<53-1 || stateBytes > cfg.CursorMaxEntryBytes {
		return InitialCommit{}, false, cursorError(api.ErrorInvalidInput)
	}
	reservationLimit, err := checkedMul(request.reservation.Slots, cfg.CursorMaxEntryBytes)
	if err == nil {
		reservationLimit, err = checkedMul(reservationLimit, 2)
	}
	if err != nil || request.reservation.Bytes > reservationLimit {
		return InitialCommit{}, false, cursorError(api.ErrorBudgetExceeded)
	}
	sharedBytes := uint64(0)
	if request.shared != nil {
		sharedBytes = request.shared.Footprint()
	}
	rootBytes := uint64(0)
	if request.root != nil {
		rootBytes = uint64(bits.UintSize / 8)
	}
	dynamicBytes, err := LineageBytes(LineageAccounting{
		EntryBytes:    stateBytes,
		SharedBytes:   sharedBytes,
		RootBytes:     rootBytes,
		ReservedBytes: request.reservation.Bytes,
	})
	if err != nil {
		return InitialCommit{}, false, cursorError(api.ErrorBudgetExceeded)
	}
	deltaSlots, err := checkedAdd(1, request.reservation.Slots)
	if err != nil {
		return InitialCommit{}, false, cursorError(api.ErrorBudgetExceeded)
	}

	publication := &InitialPublication{registry: registry, work: work, armed: true}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.closed {
		return InitialCommit{}, false, cursorError(api.ErrorCursorExpired)
	}
	now := registry.clock.Now().UnixNano()
	if !registry.canAdmitWithEvictionLocked(deltaSlots, dynamicBytes, now, noRef) {
		return InitialCommit{}, false, cursorError(api.ErrorBudgetExceeded)
	}
	var token Token
	if _, err := io.ReadFull(registry.entropy, token[:]); err != nil {
		return InitialCommit{}, false, cursorError(api.ErrorIOError)
	}
	if _, exists := registry.tokenIndex.lookup(token, registry.tokenHome(token)); exists {
		return InitialCommit{}, false, errRegistryInvariant
	}
	if registry.nextLineageID == math.MaxUint64 {
		return InitialCommit{}, false, errRegistryInvariant
	}
	lineageID := registry.nextLineageID + 1
	if _, exists := registry.lineageIndex.lookup(lineageID, registry.lineageHome(lineageID)); exists {
		return InitialCommit{}, false, errRegistryInvariant
	}
	for !registry.fitsLocked(deltaSlots, dynamicBytes) {
		victim := registry.eligibleLRUTailLocked(now, noRef)
		if victim == noRef || !registry.removeLineageLocked(lineageRef{index: victim, generation: registry.lineages.generation[victim]}) {
			return InitialCommit{}, false, errRegistryInvariant
		}
	}

	lineage, ok := registry.lineages.claim()
	if !ok {
		return InitialCommit{}, false, errRegistryInvariant
	}
	entry, ok := registry.entries.claim()
	if !ok {
		_ = registry.lineages.release(lineage)
		return InitialCommit{}, false, errRegistryInvariant
	}
	registry.nextLineageID = lineageID
	registry.installInitialLocked(lineage, entry, lineageID, token, request, stateBytes, sharedBytes, dynamicBytes, now)
	publication.lineage = lineage
	if err := work.Transfer(publication); err != nil {
		registry.removeLineageLocked(lineage)
		return InitialCommit{}, true, err
	}
	return InitialCommit{Token: token, Publication: publication}, true, nil
}

func (registry *Registry) configSnapshot() (config.Runtime, bool) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.closed {
		return config.Runtime{}, false
	}
	return registry.cfg, true
}

func (registry *Registry) installInitialLocked(lineage lineageRef, entry EntryRef, lineageID uint64, token Token, request initialRequest, stateBytes, sharedBytes, dynamicBytes uint64, now int64) {
	lineageIndex := lineage.index
	entryIndex := entry.index
	registry.lineages.flags[lineageIndex] = lineagePhaseProvisional
	registry.lineages.id[lineageIndex] = lineageID
	registry.lineages.entryHead[lineageIndex] = entryIndex
	registry.lineages.resident[lineageIndex] = 1
	registry.lineages.reservedSlots[lineageIndex] = request.reservation.Slots
	registry.lineages.reservedBytes[lineageIndex] = request.reservation.Bytes
	registry.lineages.dynamicBytes[lineageIndex] = dynamicBytes
	registry.lineages.root[lineageIndex] = request.root
	registry.lineages.shared[lineageIndex] = request.shared
	registry.lineages.sharedBytes[lineageIndex] = sharedBytes
	registry.lineages.createdAt[lineageIndex] = now
	registry.lineages.workExpiresAt[lineageIndex] = addDuration(now, registry.cfg.CursorTTL)
	registry.lineages.commitDeadline[lineageIndex] = addDuration(registry.lineages.workExpiresAt[lineageIndex], config.CursorHandoffGrace)

	registry.entries.kind[entryIndex] = entryKindResident
	registry.entries.lineage[entryIndex] = lineageIndex
	registry.entries.parent[entryIndex] = noRef
	registry.entries.successor[entryIndex] = noRef
	registry.entries.state[entryIndex] = request.state
	registry.entries.stateBytes[entryIndex] = stateBytes

	if err := registry.tokenIndex.insert(token, entryIndex, registry.tokenHome(token)); err != nil {
		panic("cursor: preflighted token insert failed")
	}
	if err := registry.lineageIndex.insert(lineageID, lineageIndex, registry.lineageHome(lineageID)); err != nil {
		panic("cursor: preflighted lineage insert failed")
	}
	registry.lruAttachFrontLocked(lineageIndex)
	registry.usedSlots += 1 + request.reservation.Slots
	registry.totalBytes += dynamicBytes
}

// Lookup resolves one connection-local token and refreshes whole-lineage recency.
func (registry *Registry) Lookup(token Token) (EntryRef, api.ErrorCode) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return registry.lookupLocked(token, registry.clockNowLocked())
}

func (registry *Registry) lookupLocked(token Token, now int64) (EntryRef, api.ErrorCode) {
	ref, lineageIndex, code := registry.resolveLocked(token, now)
	if code != "" {
		return EntryRef{}, code
	}
	registry.lruTouchLocked(lineageIndex)
	return ref, ""
}

func (registry *Registry) lookupScopedLocked(token Token, tool api.ToolName, cwdID uint64, now int64) (EntryRef, api.ErrorCode) {
	ref, lineageIndex, code := registry.resolveLocked(token, now)
	if code != "" {
		return EntryRef{}, code
	}
	state := registry.entries.state[ref.index]
	if state == nil || state.Tool() != tool {
		return EntryRef{}, api.ErrorCursorWrongTool
	}
	if state.CWDID() != cwdID {
		return EntryRef{}, api.ErrorCursorWrongCWD
	}
	registry.lruTouchLocked(lineageIndex)
	return ref, ""
}

func (registry *Registry) resolveLocked(token Token, now int64) (EntryRef, arenaRef, api.ErrorCode) {
	if registry.closed {
		return EntryRef{}, noRef, api.ErrorCursorExpired
	}
	entryIndex, ok := registry.tokenIndex.lookup(token, registry.tokenHome(token))
	if !ok || int(entryIndex) >= len(registry.entries.occupied) || registry.entries.occupied[entryIndex] == 0 {
		return EntryRef{}, noRef, api.ErrorCursorExpired
	}
	lineageIndex := registry.entries.lineage[entryIndex]
	if int(lineageIndex) >= len(registry.lineages.occupied) || registry.lineages.occupied[lineageIndex] == 0 {
		return EntryRef{}, noRef, api.ErrorCursorExpired
	}
	phase := registry.lineages.flags[lineageIndex] & lineagePhaseMask
	if phase != lineagePhasePublishing && phase != lineagePhasePublished {
		return EntryRef{}, noRef, api.ErrorCursorExpired
	}
	if registry.expiredLocked(lineageIndex, now) {
		registry.removeLineageLocked(lineageRef{index: lineageIndex, generation: registry.lineages.generation[lineageIndex]})
		return EntryRef{}, noRef, api.ErrorCursorExpired
	}
	return EntryRef{index: entryIndex, generation: registry.entries.generation[entryIndex]}, lineageIndex, ""
}

func (registry *Registry) expiredLocked(lineage arenaRef, now int64) bool {
	if now >= registry.lineages.commitDeadline[lineage] {
		return true
	}
	return now >= registry.lineages.workExpiresAt[lineage] && now >= registry.lineages.protectedUntil[lineage]
}

func (registry *Registry) canAdmitWithEvictionLocked(deltaSlots, deltaBytes uint64, now int64, exclude arenaRef) bool {
	usedSlots := registry.usedSlots
	totalBytes := registry.totalBytes
	tokenLive := uint64(registry.tokenIndex.live)
	lineageLive := uint64(registry.lineageIndex.live)
	if registry.fitsValues(usedSlots, totalBytes, tokenLive, lineageLive, deltaSlots, deltaBytes) {
		return true
	}
	current := registry.lineages.lruTail
	for visited := uint64(0); current != noRef && visited < registry.layout.MaxEntries; visited++ {
		previous := registry.lineages.lruPrev[current]
		if current != exclude && registry.evictableLocked(current, now) {
			reclaimedSlots := uint64(registry.lineages.resident[current]) + uint64(registry.lineages.tombstones[current]) + registry.lineages.reservedSlots[current]
			if reclaimedSlots > usedSlots || registry.lineages.dynamicBytes[current] > totalBytes || lineageLive == 0 {
				return false
			}
			usedSlots -= reclaimedSlots
			totalBytes -= registry.lineages.dynamicBytes[current]
			lineageLive--
			reclaimedEntries := uint64(registry.lineages.resident[current]) + uint64(registry.lineages.tombstones[current])
			if reclaimedEntries > tokenLive {
				return false
			}
			tokenLive -= reclaimedEntries
			if registry.fitsValues(usedSlots, totalBytes, tokenLive, lineageLive, deltaSlots, deltaBytes) {
				return true
			}
		}
		current = previous
	}
	return false
}

func (registry *Registry) fitsLocked(deltaSlots, deltaBytes uint64) bool {
	return registry.fitsValues(registry.usedSlots, registry.totalBytes, uint64(registry.tokenIndex.live), uint64(registry.lineageIndex.live), deltaSlots, deltaBytes)
}

func (registry *Registry) fitsValues(usedSlots, totalBytes, tokenLive, lineageLive, deltaSlots, deltaBytes uint64) bool {
	maxEntries := registry.layout.MaxEntries
	if deltaSlots > maxEntries || usedSlots > maxEntries-deltaSlots {
		return false
	}
	if deltaBytes > registry.cfg.CursorMaxTotalBytes || totalBytes > registry.cfg.CursorMaxTotalBytes-deltaBytes {
		return false
	}
	return tokenLive < maxEntries && lineageLive < maxEntries
}

func (registry *Registry) evictableLocked(lineage arenaRef, now int64) bool {
	return int(lineage) < len(registry.lineages.occupied) && registry.lineages.occupied[lineage] != 0 && registry.lineages.flags[lineage]&lineagePhaseMask == lineagePhasePublished && registry.lineages.pins[lineage] == 0 && now >= registry.lineages.protectedUntil[lineage]
}

func (registry *Registry) eligibleLRUTailLocked(now int64, exclude arenaRef) arenaRef {
	current := registry.lineages.lruTail
	for visited := uint64(0); current != noRef && visited < registry.layout.MaxEntries; visited++ {
		if current != exclude && registry.evictableLocked(current, now) {
			return current
		}
		current = registry.lineages.lruPrev[current]
	}
	return noRef
}

func (registry *Registry) removeLineageLocked(lineage lineageRef) bool {
	if !registry.lineages.valid(lineage) {
		return false
	}
	index := lineage.index
	if registry.lineages.pins[index] != 0 {
		registry.lineages.flags[index] = lineageRollbackPending
		registry.cancelLineageWorkLocked(index)
		return true
	}
	releasedSlots := uint64(registry.lineages.resident[index]) + uint64(registry.lineages.tombstones[index]) + registry.lineages.reservedSlots[index]
	if releasedSlots > registry.usedSlots || registry.totalBytes < registry.baseBytes || registry.lineages.dynamicBytes[index] > registry.totalBytes-registry.baseBytes {
		return false
	}
	registry.lruDetachLocked(index)
	entry := registry.lineages.entryHead[index]
	for visited := uint64(0); entry != noRef && visited < registry.layout.MaxEntries; visited++ {
		next := registry.entries.successor[entry]
		if token, home, ok := registry.tokenForEntryLocked(entry); ok {
			registry.tokenIndex.delete(token, home)
		}
		ref := EntryRef{index: entry, generation: registry.entries.generation[entry]}
		if !registry.entries.release(ref) {
			return false
		}
		entry = next
	}
	lineageID := registry.lineages.id[index]
	registry.lineageIndex.delete(lineageID, registry.lineageHome(lineageID))
	registry.usedSlots -= releasedSlots
	registry.totalBytes -= registry.lineages.dynamicBytes[index]
	root := registry.lineages.root[index]
	registry.lineages.root[index] = nil
	if !registry.lineages.release(lineage) {
		return false
	}
	if root != nil {
		_ = root.Close()
	}
	if registry.closed && registry.lineageIndex.live == 0 {
		registry.releaseFixedLocked()
	}
	return true
}

func (registry *Registry) tokenForEntryLocked(entry arenaRef) (Token, uint32, bool) {
	for slot := range registry.tokenIndex.state {
		if registry.tokenIndex.state[slot] != 0 && registry.tokenIndex.entry[slot] == entry {
			return registry.tokenIndex.key[slot], registry.tokenIndex.home[slot], true
		}
	}
	return Token{}, 0, false
}

func (registry *Registry) cancelLineageWorkLocked(lineage arenaRef) {
	entry := registry.lineages.entryHead[lineage]
	for visited := uint64(0); entry != noRef && visited < registry.layout.MaxEntries; visited++ {
		if active, ok := registry.entries.runtime[entry].(*computation); ok && active != nil && !active.finished {
			active.noCommit = true
			active.cancel()
		}
		entry = registry.entries.successor[entry]
	}
}

func (registry *Registry) lruAttachFrontLocked(lineage arenaRef) {
	registry.lineages.lruPrev[lineage] = noRef
	registry.lineages.lruNext[lineage] = registry.lineages.lruHead
	if registry.lineages.lruHead != noRef {
		registry.lineages.lruPrev[registry.lineages.lruHead] = lineage
	} else {
		registry.lineages.lruTail = lineage
	}
	registry.lineages.lruHead = lineage
}

func (registry *Registry) lruDetachLocked(lineage arenaRef) {
	previous := registry.lineages.lruPrev[lineage]
	next := registry.lineages.lruNext[lineage]
	if previous != noRef {
		registry.lineages.lruNext[previous] = next
	} else {
		registry.lineages.lruHead = next
	}
	if next != noRef {
		registry.lineages.lruPrev[next] = previous
	} else {
		registry.lineages.lruTail = previous
	}
	registry.lineages.lruPrev[lineage] = noRef
	registry.lineages.lruNext[lineage] = noRef
}

func (registry *Registry) lruTouchLocked(lineage arenaRef) {
	if registry.lineages.lruHead == lineage {
		return
	}
	registry.lruDetachLocked(lineage)
	registry.lruAttachFrontLocked(lineage)
}

func (registry *Registry) tokenHome(token Token) uint32 {
	return registry.keyedHome([]byte("cursor-token-home\x00"), token[:])
}

func (registry *Registry) lineageHome(lineageID uint64) uint32 {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], lineageID)
	return registry.keyedHome([]byte("cursor-lineage-home\x00"), encoded[:])
}

func (registry *Registry) keyedHome(domain, value []byte) uint32 {
	mac := hmac.New(sha256.New, registry.secret[:])
	_, _ = mac.Write(domain)
	_, _ = mac.Write(value)
	sum := mac.Sum(nil)
	return uint32(binary.BigEndian.Uint64(sum[:8]) & uint64(len(registry.tokenIndex.state)-1))
}

func (registry *Registry) clockNowLocked() int64 {
	if registry.clock == nil {
		return 0
	}
	return registry.clock.Now().UnixNano()
}

func addDuration(nanos int64, duration time.Duration) int64 {
	if duration > 0 && nanos > math.MaxInt64-int64(duration) {
		return math.MaxInt64
	}
	return nanos + int64(duration)
}

// Close invalidates every token and releases fixed storage after the last pin.
func (registry *Registry) Close() error {
	if registry == nil {
		return nil
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.closed {
		return nil
	}
	registry.closed = true
	for registry.lineageIndex.live != 0 {
		removed := false
		for index := range registry.lineages.occupied {
			if registry.lineages.occupied[index] == 0 {
				continue
			}
			if registry.lineages.pins[index] != 0 {
				registry.lineages.flags[index] = lineageRollbackPending
				registry.cancelLineageWorkLocked(arenaRef(index))
				continue
			}
			ref := lineageRef{index: arenaRef(index), generation: registry.lineages.generation[index]}
			registry.removeLineageLocked(ref)
			removed = true
			break
		}
		if !removed {
			break
		}
	}
	if registry.lineageIndex.live == 0 {
		registry.releaseFixedLocked()
	}
	return nil
}

func (registry *Registry) releaseFixedLocked() {
	registry.tokenIndex = tokenIndex{}
	registry.lineageIndex = lineageIndex{}
	registry.entries = entryArena{freeHead: noRef}
	registry.lineages = lineageArena{freeHead: noRef, lruHead: noRef, lruTail: noRef}
	registry.layout = Layout{}
	registry.baseBytes = 0
	registry.usedSlots = 0
	registry.totalBytes = 0
	registry.secret = [32]byte{}
	registry.entropy = nil
	registry.clock = nil
	registry.cfg = config.Runtime{}
	registry.nextLineageID = 0
}
