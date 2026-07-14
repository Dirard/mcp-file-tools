package cursor

import (
	"errors"
	"math"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/rootfs"
)

var (
	errDuplicateKey = errors.New("cursor: duplicate index key")
	errIndexFull    = errors.New("cursor: fixed index is full")
	errInvalidHome  = errors.New("cursor: invalid index home")
)

type arenaRef uint32

const noRef arenaRef = ^arenaRef(0)

// EntryRef remains safe across arena reuse because every claim carries a generation.
type EntryRef struct {
	index      arenaRef
	generation uint32
}

type lineageRef struct {
	index      arenaRef
	generation uint32
}

type tokenIndex struct {
	state []uint8
	home  []uint32
	key   []Token
	entry []arenaRef
	live  uint32
}

func newTokenIndex(size uint64) (tokenIndex, error) {
	length, err := fixedLength(size)
	if err != nil || size&(size-1) != 0 {
		return tokenIndex{}, errAccountingOverflow
	}
	return tokenIndex{
		state: make([]uint8, length),
		home:  make([]uint32, length),
		key:   make([]Token, length),
		entry: make([]arenaRef, length),
	}, nil
}

func (index *tokenIndex) lookup(key Token, home uint32) (arenaRef, bool) {
	if index == nil || int(home) >= len(index.state) || len(index.state) == 0 {
		return noRef, false
	}
	mask := uint32(len(index.state) - 1)
	for probe := uint32(0); probe < uint32(len(index.state)); probe++ {
		slot := (home + probe) & mask
		if index.state[slot] == 0 {
			return noRef, false
		}
		if index.key[slot] == key {
			return index.entry[slot], true
		}
	}
	return noRef, false
}

func (index *tokenIndex) insert(key Token, entry arenaRef, home uint32) error {
	if index == nil || int(home) >= len(index.state) || len(index.state) == 0 {
		return errInvalidHome
	}
	mask := uint32(len(index.state) - 1)
	for probe := uint32(0); probe < uint32(len(index.state)); probe++ {
		slot := (home + probe) & mask
		if index.state[slot] == 0 {
			index.state[slot] = 1
			index.home[slot] = home
			index.key[slot] = key
			index.entry[slot] = entry
			index.live++
			return nil
		}
		if index.key[slot] == key {
			return errDuplicateKey
		}
	}
	return errIndexFull
}

func (index *tokenIndex) delete(key Token, home uint32) bool {
	if index == nil || int(home) >= len(index.state) || len(index.state) == 0 {
		return false
	}
	mask := uint32(len(index.state) - 1)
	var hole uint32
	found := false
	for probe := uint32(0); probe < uint32(len(index.state)); probe++ {
		slot := (home + probe) & mask
		if index.state[slot] == 0 {
			return false
		}
		if index.key[slot] == key {
			hole = slot
			found = true
			break
		}
	}
	if !found {
		return false
	}
	index.clear(hole)
	for scanned := uint32(1); scanned < uint32(len(index.state)); scanned++ {
		slot := (hole + scanned) & mask
		if index.state[slot] == 0 {
			break
		}
		if cyclicDistance(index.home[slot], hole, uint32(len(index.state))) < cyclicDistance(index.home[slot], slot, uint32(len(index.state))) {
			index.state[hole] = 1
			index.home[hole] = index.home[slot]
			index.key[hole] = index.key[slot]
			index.entry[hole] = index.entry[slot]
			index.clear(slot)
			hole = slot
			scanned = 0
		}
	}
	index.live--
	return true
}

func (index *tokenIndex) clear(slot uint32) {
	index.state[slot] = 0
	index.home[slot] = 0
	index.key[slot] = Token{}
	index.entry[slot] = noRef
}

type lineageIndex struct {
	state   []uint8
	home    []uint32
	key     []uint64
	lineage []arenaRef
	live    uint32
}

func newLineageIndex(size uint64) (lineageIndex, error) {
	length, err := fixedLength(size)
	if err != nil || size&(size-1) != 0 {
		return lineageIndex{}, errAccountingOverflow
	}
	return lineageIndex{
		state:   make([]uint8, length),
		home:    make([]uint32, length),
		key:     make([]uint64, length),
		lineage: make([]arenaRef, length),
	}, nil
}

func (index *lineageIndex) lookup(key uint64, home uint32) (arenaRef, bool) {
	if index == nil || int(home) >= len(index.state) || len(index.state) == 0 {
		return noRef, false
	}
	mask := uint32(len(index.state) - 1)
	for probe := uint32(0); probe < uint32(len(index.state)); probe++ {
		slot := (home + probe) & mask
		if index.state[slot] == 0 {
			return noRef, false
		}
		if index.key[slot] == key {
			return index.lineage[slot], true
		}
	}
	return noRef, false
}

func (index *lineageIndex) insert(key uint64, lineage arenaRef, home uint32) error {
	if index == nil || int(home) >= len(index.state) || len(index.state) == 0 {
		return errInvalidHome
	}
	mask := uint32(len(index.state) - 1)
	for probe := uint32(0); probe < uint32(len(index.state)); probe++ {
		slot := (home + probe) & mask
		if index.state[slot] == 0 {
			index.state[slot] = 1
			index.home[slot] = home
			index.key[slot] = key
			index.lineage[slot] = lineage
			index.live++
			return nil
		}
		if index.key[slot] == key {
			return errDuplicateKey
		}
	}
	return errIndexFull
}

func (index *lineageIndex) delete(key uint64, home uint32) bool {
	if index == nil || int(home) >= len(index.state) || len(index.state) == 0 {
		return false
	}
	mask := uint32(len(index.state) - 1)
	var hole uint32
	found := false
	for probe := uint32(0); probe < uint32(len(index.state)); probe++ {
		slot := (home + probe) & mask
		if index.state[slot] == 0 {
			return false
		}
		if index.key[slot] == key {
			hole = slot
			found = true
			break
		}
	}
	if !found {
		return false
	}
	index.clear(hole)
	for scanned := uint32(1); scanned < uint32(len(index.state)); scanned++ {
		slot := (hole + scanned) & mask
		if index.state[slot] == 0 {
			break
		}
		if cyclicDistance(index.home[slot], hole, uint32(len(index.state))) < cyclicDistance(index.home[slot], slot, uint32(len(index.state))) {
			index.state[hole] = 1
			index.home[hole] = index.home[slot]
			index.key[hole] = index.key[slot]
			index.lineage[hole] = index.lineage[slot]
			index.clear(slot)
			hole = slot
			scanned = 0
		}
	}
	index.live--
	return true
}

func (index *lineageIndex) clear(slot uint32) {
	index.state[slot] = 0
	index.home[slot] = 0
	index.key[slot] = 0
	index.lineage[slot] = noRef
}

func cyclicDistance(home, slot, size uint32) uint32 {
	return (slot - home) & (size - 1)
}

type entryArena struct {
	freeHead   arenaRef
	occupied   []uint8
	kind       []uint8
	flags      []uint16
	generation []uint32
	nextFree   []arenaRef
	lineage    []arenaRef
	parent     []arenaRef
	successor  []arenaRef
	page       []uint32
	state      []State
	memo       []*api.Result
	runtime    []entryRuntime
	stateBytes []uint64
	memoBytes  []uint64
}

func newEntryArena(size uint64) (entryArena, error) {
	length, err := fixedLength(size)
	if err != nil {
		return entryArena{}, err
	}
	arena := entryArena{
		freeHead:   0,
		occupied:   make([]uint8, length),
		kind:       make([]uint8, length),
		flags:      make([]uint16, length),
		generation: make([]uint32, length),
		nextFree:   make([]arenaRef, length),
		lineage:    make([]arenaRef, length),
		parent:     make([]arenaRef, length),
		successor:  make([]arenaRef, length),
		page:       make([]uint32, length),
		state:      make([]State, length),
		memo:       make([]*api.Result, length),
		runtime:    make([]entryRuntime, length),
		stateBytes: make([]uint64, length),
		memoBytes:  make([]uint64, length),
	}
	for i := 0; i < length; i++ {
		arena.generation[i] = 1
		arena.lineage[i] = noRef
		arena.parent[i] = noRef
		arena.successor[i] = noRef
		arena.nextFree[i] = noRef
		if i+1 < length {
			arena.nextFree[i] = arenaRef(i + 1)
		}
	}
	return arena, nil
}

func (arena *entryArena) claim() (EntryRef, bool) {
	if arena == nil || arena.freeHead == noRef {
		return EntryRef{}, false
	}
	index := arena.freeHead
	arena.freeHead = arena.nextFree[index]
	arena.nextFree[index] = noRef
	arena.occupied[index] = 1
	return EntryRef{index: index, generation: arena.generation[index]}, true
}

func (arena *entryArena) valid(ref EntryRef) bool {
	return arena != nil && int(ref.index) < len(arena.occupied) && arena.occupied[ref.index] != 0 && arena.generation[ref.index] == ref.generation
}

func (arena *entryArena) release(ref EntryRef) bool {
	if !arena.valid(ref) || arena.generation[ref.index] == math.MaxUint32 {
		return false
	}
	index := ref.index
	arena.occupied[index] = 0
	arena.kind[index] = 0
	arena.flags[index] = 0
	arena.generation[index]++
	arena.lineage[index] = noRef
	arena.parent[index] = noRef
	arena.successor[index] = noRef
	arena.page[index] = 0
	arena.state[index] = nil
	arena.memo[index] = nil
	arena.runtime[index] = nil
	arena.stateBytes[index] = 0
	arena.memoBytes[index] = 0
	arena.nextFree[index] = arena.freeHead
	arena.freeHead = index
	return true
}

type lineageArena struct {
	freeHead       arenaRef
	lruHead        arenaRef
	lruTail        arenaRef
	occupied       []uint8
	flags          []uint16
	generation     []uint32
	nextFree       []arenaRef
	lruPrev        []arenaRef
	lruNext        []arenaRef
	entryHead      []arenaRef
	pins           []uint32
	resident       []uint32
	tombstones     []uint32
	id             []uint64
	createdAt      []int64
	workExpiresAt  []int64
	commitDeadline []int64
	protectedUntil []int64
	reservedSlots  []uint64
	reservedBytes  []uint64
	dynamicBytes   []uint64
	root           []*rootfs.Lease
	shared         []SharedAllocation
	sharedBytes    []uint64
}

func newLineageArena(size uint64) (lineageArena, error) {
	length, err := fixedLength(size)
	if err != nil {
		return lineageArena{}, err
	}
	arena := lineageArena{
		freeHead:       0,
		lruHead:        noRef,
		lruTail:        noRef,
		occupied:       make([]uint8, length),
		flags:          make([]uint16, length),
		generation:     make([]uint32, length),
		nextFree:       make([]arenaRef, length),
		lruPrev:        make([]arenaRef, length),
		lruNext:        make([]arenaRef, length),
		entryHead:      make([]arenaRef, length),
		pins:           make([]uint32, length),
		resident:       make([]uint32, length),
		tombstones:     make([]uint32, length),
		id:             make([]uint64, length),
		createdAt:      make([]int64, length),
		workExpiresAt:  make([]int64, length),
		commitDeadline: make([]int64, length),
		protectedUntil: make([]int64, length),
		reservedSlots:  make([]uint64, length),
		reservedBytes:  make([]uint64, length),
		dynamicBytes:   make([]uint64, length),
		root:           make([]*rootfs.Lease, length),
		shared:         make([]SharedAllocation, length),
		sharedBytes:    make([]uint64, length),
	}
	for i := 0; i < length; i++ {
		arena.generation[i] = 1
		arena.nextFree[i] = noRef
		arena.lruPrev[i] = noRef
		arena.lruNext[i] = noRef
		arena.entryHead[i] = noRef
		if i+1 < length {
			arena.nextFree[i] = arenaRef(i + 1)
		}
	}
	return arena, nil
}

func (arena *lineageArena) claim() (lineageRef, bool) {
	if arena == nil || arena.freeHead == noRef {
		return lineageRef{}, false
	}
	index := arena.freeHead
	arena.freeHead = arena.nextFree[index]
	arena.nextFree[index] = noRef
	arena.occupied[index] = 1
	return lineageRef{index: index, generation: arena.generation[index]}, true
}

func (arena *lineageArena) valid(ref lineageRef) bool {
	return arena != nil && int(ref.index) < len(arena.occupied) && arena.occupied[ref.index] != 0 && arena.generation[ref.index] == ref.generation
}

func (arena *lineageArena) release(ref lineageRef) bool {
	if !arena.valid(ref) || arena.generation[ref.index] == math.MaxUint32 {
		return false
	}
	index := ref.index
	arena.occupied[index] = 0
	arena.flags[index] = 0
	arena.generation[index]++
	arena.lruPrev[index] = noRef
	arena.lruNext[index] = noRef
	arena.entryHead[index] = noRef
	arena.pins[index] = 0
	arena.resident[index] = 0
	arena.tombstones[index] = 0
	arena.id[index] = 0
	arena.createdAt[index] = 0
	arena.workExpiresAt[index] = 0
	arena.commitDeadline[index] = 0
	arena.protectedUntil[index] = 0
	arena.reservedSlots[index] = 0
	arena.reservedBytes[index] = 0
	arena.dynamicBytes[index] = 0
	arena.root[index] = nil
	arena.shared[index] = nil
	arena.sharedBytes[index] = 0
	arena.nextFree[index] = arena.freeHead
	arena.freeHead = index
	return true
}

func fixedLength(size uint64) (int, error) {
	if size == 0 || size > maxIntValue() || size > math.MaxUint32 {
		return 0, errAccountingOverflow
	}
	return int(size), nil
}
