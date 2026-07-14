//go:build linux

package rootfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"golang.org/x/sys/unix"
)

func TestLinuxRootSymlinkLoopIsTypedInvalidInput(t *testing.T) {
	parent := t.TempDir()
	loop := filepath.Join(parent, "loop")
	if err := os.Symlink("loop", loop); err != nil {
		t.Fatalf("create root symlink loop: %v", err)
	}
	root, err := OpenRoot(mustRootDirectory(t, loop))
	if root != nil {
		_ = root.Close()
		t.Fatal("OpenRoot(loop) returned a root")
	}
	if !errors.Is(err, ErrSymlink) {
		t.Fatalf("OpenRoot(loop) error = %v, want %v", err, ErrSymlink)
	}
}

func TestLinuxRootEnumerationStreamsAreIndependent(t *testing.T) {
	for _, forceFallback := range []bool{false, true} {
		backend := "openat2"
		if forceFallback {
			backend = "fallback"
		}
		for _, searchTarget := range []bool{false, true} {
			seam := "OpenDir"
			if searchTarget {
				seam = "OpenSearchTarget"
			}
			t.Run(backend+"/"+seam, func(t *testing.T) {
				testLinuxIndependentRootStreams(t, forceFallback, searchTarget)
			})
		}
	}
}

func testLinuxIndependentRootStreams(t *testing.T, forceFallback, searchTarget bool) {
	t.Helper()
	rootPath := t.TempDir()
	want := make([]string, 64)
	for index := range want {
		want[index] = "entry-" + twoDigits(index)
		if err := os.WriteFile(filepath.Join(rootPath, want[index]), []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", want[index], err)
		}
	}
	root, err := OpenRoot(mustRootDirectory(t, rootPath))
	if err != nil {
		t.Fatalf("OpenRoot() error = %v", err)
	}
	defer root.Close()
	firstLease, err := root.Duplicate()
	if err != nil {
		t.Fatalf("first Duplicate() error = %v", err)
	}
	defer firstLease.Close()
	secondLease, err := root.Duplicate()
	if err != nil {
		t.Fatalf("second Duplicate() error = %v", err)
	}
	defer secondLease.Close()
	firstLease.handle.forceFallback = forceFallback
	secondLease.handle.forceFallback = forceFallback
	openDirectory := func(lease *Lease) (*Dir, error) {
		path := mustRelative(t, pathspec.POSIX, ".", true)
		if !searchTarget {
			return lease.OpenDir(path)
		}
		target, openErr := lease.OpenSearchTarget(path)
		if openErr != nil {
			return nil, openErr
		}
		directory, takeErr := target.TakeDir()
		if takeErr != nil {
			_ = target.Close()
			return nil, takeErr
		}
		return directory, nil
	}

	first, err := openDirectory(firstLease)
	if err != nil {
		t.Fatalf("first root directory open error = %v", err)
	}
	second, err := openDirectory(secondLease)
	if err != nil {
		_ = first.Close()
		t.Fatalf("second root directory open error = %v", err)
	}
	firstNames, firstErr := collectEntryNames(first)
	secondNames, secondErr := collectEntryNames(second)
	_ = first.Close()
	_ = second.Close()
	if firstErr != nil || secondErr != nil {
		t.Fatalf("sequential enumeration errors = %v, %v", firstErr, secondErr)
	}
	assertEntryNames(t, firstNames, want)
	assertEntryNames(t, secondNames, want)

	third, err := openDirectory(firstLease)
	if err != nil {
		t.Fatalf("third root directory open error = %v", err)
	}
	fourth, err := openDirectory(secondLease)
	if err != nil {
		_ = third.Close()
		t.Fatalf("fourth root directory open error = %v", err)
	}
	type result struct {
		names []string
		err   error
	}
	results := make(chan result, 2)
	var workers sync.WaitGroup
	for _, directory := range []*Dir{third, fourth} {
		workers.Add(1)
		go func(directory *Dir) {
			defer workers.Done()
			names, readErr := collectEntryNames(directory)
			results <- result{names: names, err: readErr}
		}(directory)
	}
	workers.Wait()
	close(results)
	_ = third.Close()
	_ = fourth.Close()
	for got := range results {
		if got.err != nil {
			t.Fatalf("concurrent enumeration error = %v", got.err)
		}
		assertEntryNames(t, got.names, want)
	}
}

func TestLinuxFallbackRejectsParentMovedOutsideBeforeFinalOpen(t *testing.T) {
	parent := t.TempDir()
	rootPath := filepath.Join(parent, "root")
	outsidePath := filepath.Join(parent, "outside")
	if err := os.MkdirAll(filepath.Join(rootPath, "a", "b"), 0o700); err != nil {
		t.Fatalf("mkdir rooted tree: %v", err)
	}
	if err := os.Mkdir(outsidePath, 0o700); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	root, lease := openRootAndLease(t, rootPath)
	defer root.Close()
	defer lease.Close()
	lease.handle.forceFallback = true
	path := mustRelative(t, pathspec.POSIX, "a/b/file", false)
	components := path.Components()
	parentFD, finalName, err := openLinuxFallbackParent(lease.handle, components)
	if err != nil {
		t.Fatalf("open fallback parent: %v", err)
	}
	defer unix.Close(parentFD)
	moved := filepath.Join(outsidePath, "a")
	if err := os.Rename(filepath.Join(rootPath, "a"), moved); err != nil {
		t.Fatalf("move parent outside root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moved, "b", "file"), []byte("outside"), 0o600); err != nil {
		t.Fatalf("insert outside file: %v", err)
	}
	file, _, err := openLinuxFallbackRegularAt(lease.handle, parentFD, len(components)-1, finalName)
	if file.valid {
		_ = closePlatformFile(&file)
		t.Fatal("moved parent exposed a regular file")
	}
	if !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("moved-parent final open error = %v, want %v", err, ErrSourceChanged)
	}
}

func collectEntryNames(directory *Dir) ([]string, error) {
	names := make([]string, 0)
	err := directory.ReadEntries(context.Background(), func(uint64) error { return nil }, func(outcome EnumerationOutcome) error {
		entry, ok := outcome.Candidate()
		if ok {
			components := entry.Path.Components()
			names = append(names, components[len(components)-1])
		}
		return nil
	})
	sort.Strings(names)
	return names, err
}

func assertEntryNames(t *testing.T, got, want []string) {
	t.Helper()
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("entry count = %d, want %d (%v)", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("entry names = %v, want %v", got, want)
		}
	}
}

func twoDigits(value int) string {
	return string([]byte{'0' + byte(value/10), '0' + byte(value%10)})
}
