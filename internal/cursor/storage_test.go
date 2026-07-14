package cursor

import "testing"

func TestTokenIndexBackwardShiftAcrossWraparound(t *testing.T) {
	index, err := newTokenIndex(8)
	if err != nil {
		t.Fatalf("newTokenIndex: %v", err)
	}
	keys := []Token{{1}, {2}, {3}, {4}}
	homes := []uint32{6, 6, 0, 6}
	for i := range keys {
		if err := index.insert(keys[i], arenaRef(i), homes[i]); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	if !index.delete(keys[0], homes[0]) {
		t.Fatal("delete returned false")
	}
	for i := 1; i < len(keys); i++ {
		got, ok := index.lookup(keys[i], homes[i])
		if !ok || got != arenaRef(i) {
			t.Fatalf("lookup %d = (%d, %v), want (%d, true)", i, got, ok, i)
		}
	}
	if _, ok := index.lookup(keys[0], homes[0]); ok {
		t.Fatal("deleted key is still present")
	}
}

func TestArenaReuseInvalidatesOldEntryRef(t *testing.T) {
	arena, err := newEntryArena(1)
	if err != nil {
		t.Fatalf("newEntryArena: %v", err)
	}
	first, ok := arena.claim()
	if !ok {
		t.Fatal("first claim failed")
	}
	if !arena.valid(first) {
		t.Fatal("first ref is not valid")
	}
	if !arena.release(first) {
		t.Fatal("release failed")
	}
	if arena.valid(first) {
		t.Fatal("released ref remained valid")
	}
	second, ok := arena.claim()
	if !ok {
		t.Fatal("second claim failed")
	}
	if second.index != first.index || second.generation == first.generation {
		t.Fatalf("reused ref = %#v, first = %#v", second, first)
	}
}

func TestFixedStorageColumnsHaveNoSpareCapacity(t *testing.T) {
	entries, err := newEntryArena(3)
	if err != nil {
		t.Fatalf("newEntryArena: %v", err)
	}
	lineages, err := newLineageArena(3)
	if err != nil {
		t.Fatalf("newLineageArena: %v", err)
	}
	assertFixed := func(name string, length, capacity int) {
		t.Helper()
		if length != 3 || capacity != 3 {
			t.Fatalf("%s len/cap = %d/%d, want 3/3", name, length, capacity)
		}
	}
	assertFixed("entry.state", len(entries.state), cap(entries.state))
	assertFixed("entry.runtime", len(entries.runtime), cap(entries.runtime))
	assertFixed("entry.successor", len(entries.successor), cap(entries.successor))
	assertFixed("lineage.root", len(lineages.root), cap(lineages.root))
	assertFixed("lineage.shared", len(lineages.shared), cap(lineages.shared))
	assertFixed("lineage.lruNext", len(lineages.lruNext), cap(lineages.lruNext))
}
