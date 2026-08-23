//go:build darwin

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

func TestDarwinRootEnumerationStreamsAreIndependent(t *testing.T) {
	for _, searchTarget := range []bool{false, true} {
		seam := "OpenDir"
		if searchTarget {
			seam = "OpenSearchTarget"
		}
		t.Run(seam, func(t *testing.T) {
			testDarwinIndependentRootStreams(t, searchTarget)
		})
	}
}

func testDarwinIndependentRootStreams(t *testing.T, searchTarget bool) {
	t.Helper()
	rootPath := t.TempDir()
	want := make([]string, 64)
	for index := range want {
		want[index] = "entry-" + darwinTwoDigits(index)
		if err := os.WriteFile(filepath.Join(rootPath, want[index]), []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", want[index], err)
		}
	}
	root, firstLease := openDarwinRootAndLease(t, rootPath)
	defer root.Close()
	defer firstLease.Close()
	secondLease, err := root.Duplicate()
	if err != nil {
		t.Fatalf("second Duplicate() error = %v", err)
	}
	defer secondLease.Close()
	openDirectory := func(lease *Lease) (*Dir, error) {
		path := mustDarwinRelative(t, ".", true)
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
	firstNames, firstErr := collectDarwinEntryNames(first)
	secondNames, secondErr := collectDarwinEntryNames(second)
	_ = first.Close()
	_ = second.Close()
	if firstErr != nil || secondErr != nil {
		t.Fatalf("sequential enumeration errors = %v, %v", firstErr, secondErr)
	}
	assertDarwinEntryNames(t, firstNames, want)
	assertDarwinEntryNames(t, secondNames, want)

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
			names, readErr := collectDarwinEntryNames(directory)
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
		assertDarwinEntryNames(t, got.names, want)
	}
}

func TestDarwinRejectsParentMovedOutsideBeforeFinalOpen(t *testing.T) {
	parent := t.TempDir()
	rootPath := filepath.Join(parent, "root")
	outsidePath := filepath.Join(parent, "outside")
	if err := os.MkdirAll(filepath.Join(rootPath, "a", "b"), 0o700); err != nil {
		t.Fatalf("mkdir rooted tree: %v", err)
	}
	if err := os.Mkdir(outsidePath, 0o700); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	root, lease := openDarwinRootAndLease(t, rootPath)
	defer root.Close()
	defer lease.Close()
	path := mustDarwinRelative(t, "a/b/file", false)
	components := path.Components()
	parentFD, finalName, throughSymlink, err := openDarwinParent(lease.handle, components)
	if err != nil {
		t.Fatalf("open parent: %v", err)
	}
	defer unix.Close(parentFD)
	moved := filepath.Join(outsidePath, "a")
	if err := os.Rename(filepath.Join(rootPath, "a"), moved); err != nil {
		t.Fatalf("move parent outside root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moved, "b", "file"), []byte("outside"), 0o600); err != nil {
		t.Fatalf("insert outside file: %v", err)
	}
	file, _, err := openDarwinRegularAt(lease.handle, parentFD, len(components)-1, finalName, throughSymlink)
	if file.valid {
		_ = closePlatformFile(&file)
		t.Fatal("moved parent exposed a regular file")
	}
	if !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("moved-parent final open error = %v, want %v", err, ErrSourceChanged)
	}
}

func openDarwinRootAndLease(t *testing.T, directory string) (*Root, *Lease) {
	t.Helper()
	parsed, code := pathspec.ParseRootDirectory(pathspec.POSIX, filepath.ToSlash(directory))
	if code != "" {
		t.Fatalf("ParseRootDirectory(%q) code = %q", directory, code)
	}
	root, err := OpenRoot(parsed)
	if err != nil {
		t.Fatalf("OpenRoot(%q) error = %v", directory, err)
	}
	lease, err := root.Duplicate()
	if err != nil {
		_ = root.Close()
		t.Fatalf("Duplicate() error = %v", err)
	}
	return root, lease
}

func mustDarwinRelative(t *testing.T, raw string, allowRoot bool) pathspec.Relative {
	t.Helper()
	path, code := pathspec.ParseRelative(pathspec.POSIX, raw, allowRoot)
	if code != "" {
		t.Fatalf("ParseRelative(%q) code = %q", raw, code)
	}
	return path
}

func collectDarwinEntryNames(directory *Dir) ([]string, error) {
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

func assertDarwinEntryNames(t *testing.T, got, want []string) {
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

func darwinTwoDigits(value int) string {
	return string([]byte{'0' + byte(value/10), '0' + byte(value%10)})
}
