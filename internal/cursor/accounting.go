package cursor

import (
	"errors"
	"math"
	"math/bits"
)

var errAccountingOverflow = errors.New("cursor: accounting overflow")

// Layout is the complete fixed allocation retained by one registry.
type Layout struct {
	MaxEntries          uint64
	IndexSlots          uint64
	WordBytes           uint64
	TokenIndexBacking   uint64
	LineageIndexBacking uint64
	EntryArenaBacking   uint64
	LineageArenaBacking uint64
	SliceDescriptors    uint64
	ScalarBytes         uint64
	Total               uint64
}

// EntryAccounting describes cursor-owned backing retained by one token entry.
type EntryAccounting struct {
	StateBytes uint64
	MemoBytes  uint64
}

// LineageAccounting describes dynamic bytes and logical slots for one lineage.
type LineageAccounting struct {
	Resident      uint64
	Tombstones    uint64
	ReservedSlots uint64
	EntryBytes    uint64
	SharedBytes   uint64
	RootBytes     uint64
	ReservedBytes uint64
}

// EntryBytes returns the dynamic bytes retained by one entry.
func EntryBytes(accounting EntryAccounting) (uint64, error) {
	return checkedAdd(accounting.StateBytes, accounting.MemoBytes)
}

// LineageBytes returns dynamic bytes; fixed arena/index capacity is excluded.
func LineageBytes(accounting LineageAccounting) (uint64, error) {
	total, err := checkedAdd(accounting.EntryBytes, accounting.SharedBytes)
	if err != nil {
		return 0, err
	}
	total, err = checkedAdd(total, accounting.RootBytes)
	if err != nil {
		return 0, err
	}
	return checkedAdd(total, accounting.ReservedBytes)
}

// UsedSlots returns resident plus semantic tombstone plus reserved slots.
func UsedSlots(accounting LineageAccounting) (uint64, error) {
	total, err := checkedAdd(accounting.Resident, accounting.Tombstones)
	if err != nil {
		return 0, err
	}
	return checkedAdd(total, accounting.ReservedSlots)
}

// ContainerLayout computes fixed parallel-column backing without allocator guesses.
func ContainerLayout(maxEntries uint64) (Layout, error) {
	if maxEntries == 0 || maxEntries > math.MaxUint64/2 {
		return Layout{}, errAccountingOverflow
	}
	indexSlots, err := nextPowerOfTwo(maxEntries * 2)
	if err != nil || indexSlots > maxIntValue() {
		return Layout{}, errAccountingOverflow
	}
	if maxEntries > maxIntValue() {
		return Layout{}, errAccountingOverflow
	}

	wordBytes := uint64(bits.UintSize / 8)
	layout := Layout{
		MaxEntries: maxEntries,
		IndexSlots: indexSlots,
		WordBytes:  wordBytes,
	}
	if layout.TokenIndexBacking, err = checkedMul(indexSlots, 25); err != nil {
		return Layout{}, err
	}
	if layout.LineageIndexBacking, err = checkedMul(indexSlots, 17); err != nil {
		return Layout{}, err
	}
	if layout.EntryArenaBacking, err = checkedMul(maxEntries, 44+5*wordBytes); err != nil {
		return Layout{}, err
	}
	if layout.LineageArenaBacking, err = checkedMul(maxEntries, 107+3*wordBytes); err != nil {
		return Layout{}, err
	}

	// Four slices per index, fourteen per entry arena, and twenty-one per
	// lineage arena. A Go slice descriptor is three machine words.
	layout.SliceDescriptors = 43 * 3 * wordBytes

	// ScalarBytes covers the fixed index/arena heads plus Registry's mutex,
	// scalar Runtime copy, secret, interface descriptors, layout, counters,
	// phase fields, and monotonic lineage id. Variable cursor data is excluded.
	indexArenaScalars := roundUp(4, wordBytes)*3 + roundUp(12, wordBytes)
	registryScalars := uint64(8) + 20*8 + 3*wordBytes + 32 + 4*wordBytes + 10*8 + 3*8 + 1 + 8
	layout.ScalarBytes = indexArenaScalars + roundUp(registryScalars, wordBytes)

	total := uint64(0)
	for _, part := range [...]uint64{
		layout.TokenIndexBacking,
		layout.LineageIndexBacking,
		layout.EntryArenaBacking,
		layout.LineageArenaBacking,
		layout.SliceDescriptors,
		layout.ScalarBytes,
	} {
		total, err = checkedAdd(total, part)
		if err != nil {
			return Layout{}, err
		}
	}
	layout.Total = total
	return layout, nil
}

func nextPowerOfTwo(value uint64) (uint64, error) {
	if value == 0 {
		return 0, errAccountingOverflow
	}
	if value&(value-1) == 0 {
		return value, nil
	}
	shift := bits.Len64(value)
	if shift >= 64 {
		return 0, errAccountingOverflow
	}
	return uint64(1) << shift, nil
}

func checkedAdd(left, right uint64) (uint64, error) {
	if right > math.MaxUint64-left {
		return 0, errAccountingOverflow
	}
	return left + right, nil
}

func checkedMul(left, right uint64) (uint64, error) {
	if left != 0 && right > math.MaxUint64/left {
		return 0, errAccountingOverflow
	}
	return left * right, nil
}

func roundUp(value, alignment uint64) uint64 {
	return (value + alignment - 1) &^ (alignment - 1)
}

func maxIntValue() uint64 {
	return uint64(^uint(0) >> 1)
}
