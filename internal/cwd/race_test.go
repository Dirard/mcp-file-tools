package cwd

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"github.com/Dirard/mcp-file-tools/internal/rootfs"
)

func TestRegistryLookupExpiryRaceKeepsSuccessfulLeasesStable(t *testing.T) {
	rootPath := makeDirectory(t, t.TempDir(), "root")
	clock := newFakeClock(time.Unix(700, 0))
	registry := newTestRegistry(clock, 4, time.Hour)
	t.Cleanup(func() { _ = registry.Close() })
	id, _, code := registry.Register(openRoot(t, rootPath))
	if code != "" {
		t.Fatal(code)
	}
	relative, pathCode := pathspec.ParseRelative(hostTarget(), ".", true)
	if pathCode != "" {
		t.Fatal(pathCode)
	}

	const lookups = 32
	start := make(chan struct{})
	leases := make(chan *rootfs.Lease, lookups)
	codes := make(chan api.ErrorCode, lookups)
	var wait sync.WaitGroup
	for range lookups {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			lease, code := registry.Lookup(id)
			if lease != nil {
				leases <- lease
			}
			codes <- code
		}()
	}
	close(start)
	clock.Advance(time.Hour)
	wait.Wait()
	close(leases)
	close(codes)

	for code := range codes {
		if code != "" && code != api.ErrorCWDUnknown {
			t.Fatalf("lookup race returned %q", code)
		}
	}
	for lease := range leases {
		dir, err := lease.OpenDir(relative)
		if err != nil {
			t.Fatalf("successful lease was invalidated by expiry: %v", err)
		}
		_ = dir.Close()
		_ = lease.Close()
	}
	if lease, code := registry.Lookup(id); lease != nil || code != api.ErrorCWDUnknown {
		t.Fatalf("post-expiry lookup = (%v, %q)", lease, code)
	}
}

func TestRegistryCloseRegisterLookupRaceEndsClosedAndEmpty(t *testing.T) {
	parent := t.TempDir()
	registry := newTestRegistry(newFakeClock(time.Unix(800, 0)), 64, time.Hour)
	seedID, _, code := registry.Register(openRoot(t, makeDirectory(t, parent, "seed")))
	if code != "" {
		t.Fatal(code)
	}

	const registrations = 16
	roots := make([]*rootfs.Root, registrations)
	for index := range roots {
		roots[index] = openRoot(t, makeDirectory(t, parent, "root-"+strconv.Itoa(index)))
	}
	start := make(chan struct{})
	var wait sync.WaitGroup
	for _, root := range roots {
		wait.Add(1)
		go func(candidate *rootfs.Root) {
			defer wait.Done()
			<-start
			_, _, _ = registry.Register(candidate)
		}(root)
	}
	for range registrations {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			lease, _ := registry.Lookup(seedID)
			if lease != nil {
				_ = lease.Close()
			}
		}()
	}
	wait.Add(1)
	go func() {
		defer wait.Done()
		<-start
		_ = registry.Close()
	}()
	close(start)
	wait.Wait()
	_ = registry.Close()

	stats := registry.StatsForTest()
	if !stats.Closed || stats.ActiveEntries != 0 {
		t.Fatalf("registry did not finish closed and empty: %#v", stats)
	}
}
