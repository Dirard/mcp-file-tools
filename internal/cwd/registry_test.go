package cwd

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/config"
	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"github.com/Dirard/mcp-file-tools/internal/rootfs"
)

func TestRegistryAllocatesMonotonicIDsWithoutFilesystemState(t *testing.T) {
	parent := t.TempDir()
	firstPath := makeDirectory(t, parent, "first")
	secondPath := makeDirectory(t, parent, "second")
	before := directoryNames(t, parent)

	registry := newTestRegistry(newFakeClock(time.Unix(100, 0)), 8, time.Hour)
	t.Cleanup(func() { _ = registry.Close() })

	firstID, inserted, code := registry.Register(openRoot(t, firstPath))
	if code != "" || !inserted || firstID != 1 {
		t.Fatalf("first registration = (%d, %v, %q), want (1, true, empty)", firstID, inserted, code)
	}
	secondID, inserted, code := registry.Register(openRoot(t, secondPath))
	if code != "" || !inserted || secondID != 2 {
		t.Fatalf("second registration = (%d, %v, %q), want (2, true, empty)", secondID, inserted, code)
	}
	if got := directoryNames(t, parent); !equalStrings(got, before) {
		t.Fatalf("registry created filesystem state: before=%v after=%v", before, got)
	}
}

func TestRegistryDeduplicatesExactPathAndIdentity(t *testing.T) {
	parent := t.TempDir()
	originalPath := makeDirectory(t, parent, "root")
	registry := newTestRegistry(newFakeClock(time.Unix(200, 0)), 8, time.Hour)
	t.Cleanup(func() { _ = registry.Close() })

	firstID, inserted, code := registry.Register(openRoot(t, originalPath))
	if code != "" || !inserted {
		t.Fatalf("first registration failed: inserted=%v code=%q", inserted, code)
	}

	redundant := openRoot(t, originalPath)
	dedupedID, inserted, code := registry.Register(redundant)
	if code != "" || inserted || dedupedID != firstID {
		t.Fatalf("dedupe = (%d, %v, %q), want (%d, false, empty)", dedupedID, inserted, code, firstID)
	}
	if _, err := redundant.Duplicate(); !errors.Is(err, rootfs.ErrClosed) {
		t.Fatalf("redundant root was not consumed and closed: %v", err)
	}

	renamedPath := filepath.Join(parent, "renamed")
	if err := os.Rename(originalPath, renamedPath); err != nil {
		t.Fatal(err)
	}
	renamedID, inserted, code := registry.Register(openRoot(t, renamedPath))
	if code != "" || !inserted || renamedID == firstID {
		t.Fatalf("same identity at a different canonical path reused id: (%d, %v, %q)", renamedID, inserted, code)
	}

	makeDirectory(t, parent, "root")
	replacementID, inserted, code := registry.Register(openRoot(t, originalPath))
	if code != "" || !inserted || replacementID == firstID || replacementID == renamedID {
		t.Fatalf("replacement registration = (%d, %v, %q)", replacementID, inserted, code)
	}

	lease, code := registry.Lookup(firstID)
	if code != "" {
		t.Fatalf("old handle lookup after rename: %q", code)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryTTLIsAbsoluteAndCapacityDoesNotEvict(t *testing.T) {
	parent := t.TempDir()
	firstPath := makeDirectory(t, parent, "first")
	secondPath := makeDirectory(t, parent, "second")
	clock := newFakeClock(time.Unix(300, 0))
	registry := newTestRegistry(clock, 1, time.Hour)
	t.Cleanup(func() { _ = registry.Close() })

	firstID, _, code := registry.Register(openRoot(t, firstPath))
	if code != "" {
		t.Fatal(code)
	}
	clock.Advance(30 * time.Minute)
	lease, code := registry.Lookup(firstID)
	if code != "" {
		t.Fatal(code)
	}
	_ = lease.Close()
	if id, inserted, code := registry.Register(openRoot(t, firstPath)); code != "" || inserted || id != firstID {
		t.Fatalf("dedupe before expiry = (%d, %v, %q)", id, inserted, code)
	}
	if _, inserted, code := registry.Register(openRoot(t, secondPath)); code != api.ErrorBudgetExceeded || inserted {
		t.Fatalf("capacity result = (inserted=%v, code=%q), want budget_exceeded", inserted, code)
	}

	clock.Advance(30 * time.Minute)
	if lease, code := registry.Lookup(firstID); code != api.ErrorCWDUnknown || lease != nil {
		t.Fatalf("exact TTL lookup = (%v, %q), want nil cwd_unknown", lease, code)
	}
	secondID, inserted, code := registry.Register(openRoot(t, secondPath))
	if code != "" || !inserted || secondID <= firstID {
		t.Fatalf("post-expiry registration = (%d, %v, %q)", secondID, inserted, code)
	}
	if lease, code := registry.Lookup(firstID); code != api.ErrorCWDUnknown || lease != nil {
		t.Fatalf("expired id was reused: (%v, %q)", lease, code)
	}
}

func TestRegistryStopsAtSafeJSONInteger(t *testing.T) {
	parent := t.TempDir()
	registry := newTestRegistry(newFakeClock(time.Unix(400, 0)), 4, time.Hour)
	registry.next = maxSafeID
	t.Cleanup(func() { _ = registry.Close() })

	id, inserted, code := registry.Register(openRoot(t, makeDirectory(t, parent, "last")))
	if code != "" || !inserted || id != maxSafeID {
		t.Fatalf("last safe id = (%d, %v, %q)", id, inserted, code)
	}
	root := openRoot(t, makeDirectory(t, parent, "overflow"))
	if id, inserted, code := registry.Register(root); id != 0 || inserted || code != api.ErrorBudgetExceeded {
		t.Fatalf("overflow registration = (%d, %v, %q)", id, inserted, code)
	}
	if _, err := root.Duplicate(); !errors.Is(err, rootfs.ErrClosed) {
		t.Fatalf("rejected root was not consumed: %v", err)
	}
}

func TestRegistryCloseKeepsOutstandingLeaseIndependent(t *testing.T) {
	rootPath := makeDirectory(t, t.TempDir(), "root")
	registry := newTestRegistry(newFakeClock(time.Unix(500, 0)), 4, time.Hour)
	id, _, code := registry.Register(openRoot(t, rootPath))
	if code != "" {
		t.Fatal(code)
	}
	lease, code := registry.Lookup(id)
	if code != "" {
		t.Fatal(code)
	}
	if err := registry.Close(); err != nil {
		t.Fatal(err)
	}
	if err := registry.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if got, code := registry.Lookup(id); got != nil || code != api.ErrorCWDUnknown {
		t.Fatalf("lookup after close = (%v, %q)", got, code)
	}

	relative, pathCode := pathspec.ParseRelative(hostTarget(), ".", true)
	if pathCode != "" {
		t.Fatal(pathCode)
	}
	dir, err := lease.OpenDir(relative)
	if err != nil {
		t.Fatalf("outstanding lease invalidated by registry close: %v", err)
	}
	_ = dir.Close()
	_ = lease.Close()

	rejected := openRoot(t, rootPath)
	if _, inserted, code := registry.Register(rejected); inserted || code != api.ErrorIOError {
		t.Fatalf("register after close = (inserted=%v, code=%q)", inserted, code)
	}
	if _, err := rejected.Duplicate(); !errors.Is(err, rootfs.ErrClosed) {
		t.Fatalf("root supplied after close was not consumed: %v", err)
	}
}

func TestRegistryConcurrentRegistrationAndLookup(t *testing.T) {
	parent := t.TempDir()
	rootPath := makeDirectory(t, parent, "shared")
	registry := newTestRegistry(newFakeClock(time.Unix(600, 0)), 128, time.Hour)
	t.Cleanup(func() { _ = registry.Close() })

	const workers = 32
	roots := make([]*rootfs.Root, workers)
	for i := range roots {
		roots[i] = openRoot(t, rootPath)
	}
	ids := make([]ID, workers)
	codes := make([]api.ErrorCode, workers)
	var wait sync.WaitGroup
	for i := range roots {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			ids[index], _, codes[index] = registry.Register(roots[index])
		}(i)
	}
	wait.Wait()
	for i := range ids {
		if codes[i] != "" || ids[i] != 1 {
			t.Fatalf("worker %d = (%d, %q), want (1, empty)", i, ids[i], codes[i])
		}
	}

	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			lease, code := registry.Lookup(1)
			if code == "" {
				_ = lease.Close()
			}
		}()
	}
	wait.Wait()
	stats := registry.StatsForTest()
	if stats.ActiveEntries != 1 || stats.NextID != 2 {
		t.Fatalf("unexpected stats: %#v", stats)
	}
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(now time.Time) *fakeClock {
	return &fakeClock{now: now}
}

func (clock *fakeClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *fakeClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(duration)
	clock.mu.Unlock()
}

func newTestRegistry(clock Clock, maxEntries uint64, ttl time.Duration) *Registry {
	cfg := config.DefaultRuntime()
	cfg.CWDMaxEntries = maxEntries
	cfg.CWDTTL = ttl
	return New(cfg, clock)
}

func makeDirectory(t *testing.T, parent, name string) string {
	t.Helper()
	path := filepath.Join(parent, name)
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func openRoot(t *testing.T, path string) *rootfs.Root {
	t.Helper()
	directory, code := pathspec.ParseRootDirectory(hostTarget(), filepath.ToSlash(path))
	if code != "" {
		t.Fatalf("parse root %q: %q", path, code)
	}
	root, err := rootfs.OpenRoot(directory)
	if err != nil {
		t.Fatalf("open root %q: %v", path, err)
	}
	return root
}

func hostTarget() pathspec.TargetOS {
	if runtime.GOOS == "windows" {
		return pathspec.Windows
	}
	return pathspec.POSIX
}

func directoryNames(t *testing.T, path string) []string {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
