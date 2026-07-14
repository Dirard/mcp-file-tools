package cursor

import (
	"math"
	"strconv"
	"testing"
)

func TestContainerLayoutUsesFixedColumnFormulas(t *testing.T) {
	const entries = uint64(17)
	layout, err := ContainerLayout(entries)
	if err != nil {
		t.Fatalf("ContainerLayout: %v", err)
	}
	if layout.IndexSlots != 64 {
		t.Fatalf("IndexSlots = %d, want 64", layout.IndexSlots)
	}
	wordBytes := uint64(strconv.IntSize / 8)
	if layout.WordBytes != wordBytes {
		t.Fatalf("WordBytes = %d, want %d", layout.WordBytes, wordBytes)
	}
	if want := layout.IndexSlots * 25; layout.TokenIndexBacking != want {
		t.Fatalf("TokenIndexBacking = %d, want %d", layout.TokenIndexBacking, want)
	}
	if want := layout.IndexSlots * 17; layout.LineageIndexBacking != want {
		t.Fatalf("LineageIndexBacking = %d, want %d", layout.LineageIndexBacking, want)
	}
	if want := entries * (44 + 5*wordBytes); layout.EntryArenaBacking != want {
		t.Fatalf("EntryArenaBacking = %d, want %d", layout.EntryArenaBacking, want)
	}
	if want := entries * (107 + 3*wordBytes); layout.LineageArenaBacking != want {
		t.Fatalf("LineageArenaBacking = %d, want %d", layout.LineageArenaBacking, want)
	}
	wantTotal := layout.TokenIndexBacking + layout.LineageIndexBacking + layout.EntryArenaBacking + layout.LineageArenaBacking + layout.SliceDescriptors + layout.ScalarBytes
	if layout.Total != wantTotal {
		t.Fatalf("Total = %d, want %d", layout.Total, wantTotal)
	}
}

func TestAccountingRejectsOverflow(t *testing.T) {
	if _, err := EntryBytes(EntryAccounting{StateBytes: math.MaxUint64, MemoBytes: 1}); err == nil {
		t.Fatal("EntryBytes accepted overflow")
	}
	if _, err := UsedSlots(LineageAccounting{Resident: math.MaxUint64, ReservedSlots: 1}); err == nil {
		t.Fatal("UsedSlots accepted overflow")
	}
	if _, err := LineageBytes(LineageAccounting{EntryBytes: math.MaxUint64, SharedBytes: 1}); err == nil {
		t.Fatal("LineageBytes accepted overflow")
	}
	if _, err := ContainerLayout(math.MaxUint64); err == nil {
		t.Fatal("ContainerLayout accepted impossible capacity")
	}
}

func TestLineageAccountingSeparatesSlotsAndBytes(t *testing.T) {
	accounting := LineageAccounting{
		Resident:      2,
		Tombstones:    1,
		ReservedSlots: 3,
		EntryBytes:    100,
		SharedBytes:   200,
		RootBytes:     8,
		ReservedBytes: 400,
	}
	if got, err := UsedSlots(accounting); err != nil || got != 6 {
		t.Fatalf("UsedSlots = (%d, %v), want (6, nil)", got, err)
	}
	if got, err := LineageBytes(accounting); err != nil || got != 708 {
		t.Fatalf("LineageBytes = (%d, %v), want (708, nil)", got, err)
	}
}
