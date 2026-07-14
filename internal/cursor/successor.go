package cursor

import (
	"math"
	"math/bits"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/config"
)

const entryFlagMustTerminalize uint16 = 1 << 0

func resultFootprint(result api.Result) (uint64, error) {
	if err := result.Validate(); err != nil {
		return 0, err
	}
	wordBytes := uint64(bits.UintSize / 8)
	bytes := 5 * wordBytes
	if text, ok := result.Text(); ok {
		return checkedAdd(bytes, uint64(len(text)))
	}
	return bytes, nil
}

// SuccessorBytes returns the exact cursor bytes retained by one successor
// state and the memoized result of its parent.
func SuccessorBytes(state State, result api.Result) (uint64, error) {
	if state == nil {
		return 0, cursorError(api.ErrorInvalidInput)
	}
	memoBytes, err := resultFootprint(result)
	if err != nil {
		return 0, err
	}
	return EntryBytes(EntryAccounting{StateBytes: state.Footprint(), MemoBytes: memoBytes})
}

// materializeSuccessor atomically memoizes the parent result and installs its child.
// A zero-slot dynamic use is newly admitted; a one-slot use consumes a reservation.
func (registry *Registry) materializeSuccessor(parent EntryRef, successor State, result api.Result, use ReservationUse) (Token, error) {
	if successor == nil {
		return Token{}, cursorError(api.ErrorInvalidInput)
	}
	memoBytes, err := resultFootprint(result)
	if err != nil {
		return Token{}, cursorError(api.ErrorInvalidInput)
	}
	stateBytes := successor.Footprint()
	actualBytes, err := EntryBytes(EntryAccounting{StateBytes: stateBytes, MemoBytes: memoBytes})
	if err != nil || actualBytes != use.Bytes {
		return Token{}, cursorError(api.ErrorBudgetExceeded)
	}
	memo := result

	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.closed || !registry.entries.valid(parent) {
		return Token{}, cursorError(api.ErrorCursorExpired)
	}
	parentIndex := parent.index
	lineageIndex := registry.entries.lineage[parentIndex]
	if int(lineageIndex) >= len(registry.lineages.occupied) || registry.lineages.occupied[lineageIndex] == 0 {
		return Token{}, cursorError(api.ErrorCursorExpired)
	}
	phase := registry.lineages.flags[lineageIndex] & lineagePhaseMask
	if phase != lineagePhasePublishing && phase != lineagePhasePublished {
		return Token{}, cursorError(api.ErrorCursorExpired)
	}
	now := registry.clockNowLocked()
	if registry.expiredLocked(lineageIndex, now) {
		registry.removeLineageLocked(lineageRef{index: lineageIndex, generation: registry.lineages.generation[lineageIndex]})
		return Token{}, cursorError(api.ErrorCursorExpired)
	}
	if registry.entries.memo[parentIndex] != nil || registry.entries.successor[parentIndex] != noRef || registry.entries.page[parentIndex] == math.MaxUint32 || registry.entries.flags[parentIndex]&entryFlagMustTerminalize != 0 {
		return Token{}, errRegistryInvariant
	}
	// Entry page zero resumes the second response. A child would make the
	// response after the current computation reachable.
	if uint64(registry.entries.page[parentIndex])+3 > registry.cfg.CursorMaxPages {
		return Token{}, cursorError(api.ErrorBudgetExceeded)
	}
	parentState := registry.entries.state[parentIndex]
	if parentState == nil || successor.Tool() != parentState.Tool() || successor.CWDID() != parentState.CWDID() || !successor.Tool().Valid() || successor.CWDID() == 0 {
		return Token{}, cursorError(api.ErrorInvalidInput)
	}
	if stateBytes > registry.cfg.CursorMaxEntryBytes || memoBytes > registry.cfg.CursorMaxEntryBytes || registry.entries.stateBytes[parentIndex] > registry.cfg.CursorMaxEntryBytes-memoBytes {
		return Token{}, cursorError(api.ErrorBudgetExceeded)
	}
	if now >= registry.lineages.workExpiresAt[lineageIndex] {
		return Token{}, cursorError(api.ErrorCursorExpired)
	}

	isRead := registry.lineages.shared[lineageIndex] != nil
	sharedDigest, hasShared := successor.SharedDigest()
	if isRead {
		if !hasShared || sharedDigest != registry.lineages.shared[lineageIndex].Digest() || use.Kind != ReadPagePlan || use.Slots != 1 {
			return Token{}, cursorError(api.ErrorInvalidInput)
		}
	} else if hasShared || use.Kind != BroadSummaryFinal || use.Slots > 1 {
		return Token{}, cursorError(api.ErrorInvalidInput)
	}
	reserved := use.Slots == 1
	if reserved {
		if registry.lineages.reservedSlots[lineageIndex] == 0 || use.Bytes > registry.lineages.reservedBytes[lineageIndex] {
			return Token{}, errRegistryInvariant
		}
		if use.Kind != BroadSummaryFinal && registry.lineages.reservedSlots[lineageIndex] == 1 && use.Bytes != registry.lineages.reservedBytes[lineageIndex] {
			return Token{}, errRegistryInvariant
		}
	} else {
		if isRead || use.MustTerminalize || !registry.canAdmitWithEvictionLocked(1, actualBytes, now, lineageIndex) {
			return Token{}, cursorError(api.ErrorBudgetExceeded)
		}
	}

	parentToken, _, ok := registry.tokenForEntryLocked(parentIndex)
	if !ok {
		return Token{}, errRegistryInvariant
	}
	childToken := deriveChildToken(registry.secret, parentToken, successor.Digest())
	if _, exists := registry.tokenIndex.lookup(childToken, registry.tokenHome(childToken)); exists {
		return Token{}, errRegistryInvariant
	}
	if registry.entries.freeHead == noRef {
		return Token{}, errRegistryInvariant
	}
	if !reserved {
		for !registry.fitsLocked(1, actualBytes) {
			victim := registry.eligibleLRUTailLocked(now, lineageIndex)
			if victim == noRef || !registry.removeLineageLocked(lineageRef{index: victim, generation: registry.lineages.generation[victim]}) {
				return Token{}, errRegistryInvariant
			}
		}
	}
	child, ok := registry.entries.claim()
	if !ok {
		return Token{}, errRegistryInvariant
	}
	childIndex := child.index
	registry.entries.kind[childIndex] = entryKindResident
	registry.entries.lineage[childIndex] = lineageIndex
	registry.entries.parent[childIndex] = parentIndex
	registry.entries.successor[childIndex] = noRef
	registry.entries.page[childIndex] = registry.entries.page[parentIndex] + 1
	registry.entries.state[childIndex] = successor
	registry.entries.stateBytes[childIndex] = stateBytes
	if use.MustTerminalize {
		registry.entries.flags[childIndex] |= entryFlagMustTerminalize
	}
	registry.entries.memo[parentIndex] = &memo
	registry.entries.memoBytes[parentIndex] = memoBytes
	registry.entries.successor[parentIndex] = childIndex
	if err := registry.tokenIndex.insert(childToken, childIndex, registry.tokenHome(childToken)); err != nil {
		panic("cursor: preflighted child token insert failed")
	}
	registry.lineages.resident[lineageIndex]++
	if reserved {
		reservedBefore := registry.lineages.reservedBytes[lineageIndex]
		registry.lineages.reservedSlots[lineageIndex]--
		if use.Kind == BroadSummaryFinal && registry.lineages.reservedSlots[lineageIndex] == 0 {
			slack := reservedBefore - actualBytes
			registry.lineages.reservedBytes[lineageIndex] = 0
			registry.totalBytes -= slack
			registry.lineages.dynamicBytes[lineageIndex] -= slack
		} else {
			registry.lineages.reservedBytes[lineageIndex] -= actualBytes
		}
	} else {
		registry.usedSlots++
		registry.totalBytes += actualBytes
		registry.lineages.dynamicBytes[lineageIndex] += actualBytes
	}
	protectedUntil := addDuration(now, config.CursorHandoffGrace)
	if protectedUntil > registry.lineages.commitDeadline[lineageIndex] {
		protectedUntil = registry.lineages.commitDeadline[lineageIndex]
	}
	if protectedUntil > registry.lineages.protectedUntil[lineageIndex] {
		registry.lineages.protectedUntil[lineageIndex] = protectedUntil
	}
	registry.lruTouchLocked(lineageIndex)
	return childToken, nil
}
