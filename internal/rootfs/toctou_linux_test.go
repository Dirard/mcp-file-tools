//go:build linux

package rootfs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"golang.org/x/sys/unix"
)

func TestEnumeratedIdentityRejectsEveryComponentReplacementBeforeContent(t *testing.T) {
	for _, forceFallback := range []bool{false, true} {
		backend := "openat2"
		if forceFallback {
			backend = "fallback"
		}
		for component := 0; component < 3; component++ {
			for _, replacementKind := range []string{"same-kind", "symlink"} {
				name := backend + "/component-" + strconv.Itoa(component) + "/" + replacementKind
				t.Run(name, func(t *testing.T) {
					testEnumeratedIdentityReplacement(t, forceFallback, component, replacementKind)
				})
			}
		}
	}
}

func testEnumeratedIdentityReplacement(t *testing.T, forceFallback bool, component int, replacementKind string) {
	t.Helper()
	rootPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rootPath, "a", "b"), 0o700); err != nil {
		t.Fatalf("mkdir original tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "a", "b", "file"), []byte("original"), 0o600); err != nil {
		t.Fatalf("write original file: %v", err)
	}
	root, lease := openRootAndLease(t, rootPath)
	defer root.Close()
	defer lease.Close()
	lease.handle.forceFallback = forceFallback
	enumerated := enumerateEntryAt(t, lease, "a/b", "file")
	outsideRoot := t.TempDir()

	replace := func() error {
		slot, parked, replacement, outside := replacementPaths(rootPath, outsideRoot, component)
		if replacementKind == "same-kind" {
			if err := createReplacementTree(replacement, component); err != nil {
				return err
			}
		} else if err := createOutsideTree(outside, component); err != nil {
			return err
		}
		if err := os.Rename(slot, parked); err != nil {
			return err
		}
		if replacementKind == "same-kind" {
			return os.Rename(replacement, slot)
		}
		return os.Symlink(outside, slot)
	}

	enumerationComplete := make(chan struct{})
	replacementComplete := make(chan error, 1)
	go func() {
		<-enumerationComplete
		replacementComplete <- replace()
	}()
	close(enumerationComplete)
	if err := <-replacementComplete; err != nil {
		t.Fatalf("replace component %d: %v", component, err)
	}

	path := mustRelative(t, pathspec.POSIX, "a/b/file", false)
	target, err := lease.OpenSearchTarget(path)
	if replacementKind == "symlink" {
		if target != nil {
			_ = target.Close()
			t.Fatal("symlink replacement exposed a search target")
		}
		if !errors.Is(err, ErrSymlink) {
			t.Fatalf("symlink replacement error = %v, want %v", err, ErrSymlink)
		}
		return
	}
	if err != nil {
		t.Fatalf("same-kind replacement OpenSearchTarget() error = %v", err)
	}
	file, err := target.TakeFile()
	if err != nil {
		_ = target.Close()
		t.Fatalf("same-kind replacement TakeFile() error = %v", err)
	}
	defer file.Close()
	if file.Identity() == enumerated.Identity {
		t.Fatal("component replacement retained the enumerated final identity")
	}
	var callerOutcome error
	if enumerated.IdentityKnown && file.Identity() != enumerated.Identity {
		callerOutcome = ErrSourceChanged
	}
	if !errors.Is(callerOutcome, ErrSourceChanged) {
		t.Fatalf("identity comparison outcome = %v, want %v", callerOutcome, ErrSourceChanged)
	}
	if offset, seekErr := unix.Seek(file.handle.fd, 0, io.SeekCurrent); seekErr != nil || offset != 0 {
		t.Fatalf("source_changed was detected after content read: offset=%d error=%v", offset, seekErr)
	}
}

func TestRepeatedOpenBranchesDoNotLeakPlatformHandles(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "file"), []byte("content"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.Mkdir(filepath.Join(rootPath, "directory"), 0o700); err != nil {
		t.Fatalf("mkdir directory: %v", err)
	}
	if err := unix.Mkfifo(filepath.Join(rootPath, "fifo"), 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	if err := os.Symlink("file", filepath.Join(rootPath, "symlink")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	root, lease := openRootAndLease(t, rootPath)
	defer root.Close()
	defer lease.Close()
	paths := map[string]pathspec.Relative{
		"file":      mustRelative(t, pathspec.POSIX, "file", false),
		"directory": mustRelative(t, pathspec.POSIX, "directory", false),
		"fifo":      mustRelative(t, pathspec.POSIX, "fifo", false),
		"symlink":   mustRelative(t, pathspec.POSIX, "symlink", false),
		"missing":   mustRelative(t, pathspec.POSIX, "missing", false),
	}
	for _, forceFallback := range []bool{false, true} {
		lease.handle.forceFallback = forceFallback
		before := openDescriptorCount(t)
		for iteration := 0; iteration < 200; iteration++ {
			fileTarget, err := lease.OpenSearchTarget(paths["file"])
			if err != nil {
				t.Fatalf("OpenSearchTarget(file) error = %v", err)
			}
			file, err := fileTarget.TakeFile()
			if err != nil {
				t.Fatalf("TakeFile() error = %v", err)
			}
			if err := fileTarget.Close(); err != nil {
				t.Fatalf("file target Close() after transfer error = %v", err)
			}
			if err := file.Close(); err != nil {
				t.Fatalf("file Close() error = %v", err)
			}

			directoryTarget, err := lease.OpenSearchTarget(paths["directory"])
			if err != nil {
				t.Fatalf("OpenSearchTarget(directory) error = %v", err)
			}
			if _, err := directoryTarget.TakeFile(); !errors.Is(err, ErrWrongTargetKind) {
				t.Fatalf("wrong TakeFile() error = %v", err)
			}
			if err := directoryTarget.Close(); err != nil {
				t.Fatalf("directory target Close() error = %v", err)
			}

			for name, want := range map[string]error{"fifo": ErrSpecial, "symlink": ErrSymlink, "missing": ErrNotFound} {
				target, openErr := lease.OpenSearchTarget(paths[name])
				if target != nil {
					_ = target.Close()
					t.Fatalf("OpenSearchTarget(%s) exposed a target", name)
				}
				if !errors.Is(openErr, want) {
					t.Fatalf("OpenSearchTarget(%s) error = %v, want %v", name, openErr, want)
				}
			}
		}
		after := openDescriptorCount(t)
		if after != before {
			t.Fatalf("open descriptor count after repeated branches = %d, want %d", after, before)
		}
	}
}

func enumerateEntryAt(t *testing.T, lease *Lease, directoryPath, name string) Entry {
	t.Helper()
	directory, err := lease.OpenDir(mustRelative(t, pathspec.POSIX, directoryPath, false))
	if err != nil {
		t.Fatalf("OpenDir(%s) error = %v", directoryPath, err)
	}
	defer directory.Close()
	var found Entry
	err = directory.ReadEntries(context.Background(), func(uint64) error { return nil }, func(outcome EnumerationOutcome) error {
		entry, ok := outcome.Candidate()
		if ok && entry.Path.String() == directoryPath+"/"+name {
			found = entry
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ReadEntries(%s) error = %v", directoryPath, err)
	}
	if !found.IdentityKnown {
		t.Fatalf("entry %s/%s has no identity", directoryPath, name)
	}
	return found
}

func replacementPaths(rootPath, outsideRoot string, component int) (slot, parked, replacement, outside string) {
	switch component {
	case 0:
		return filepath.Join(rootPath, "a"), filepath.Join(rootPath, "parked-a"), filepath.Join(rootPath, "replacement-a"), filepath.Join(outsideRoot, "outside-a")
	case 1:
		return filepath.Join(rootPath, "a", "b"), filepath.Join(rootPath, "a", "parked-b"), filepath.Join(rootPath, "a", "replacement-b"), filepath.Join(outsideRoot, "outside-b")
	default:
		return filepath.Join(rootPath, "a", "b", "file"), filepath.Join(rootPath, "a", "b", "parked-file"), filepath.Join(rootPath, "a", "b", "replacement-file"), filepath.Join(outsideRoot, "outside-file")
	}
}

func createReplacementTree(path string, component int) error {
	switch component {
	case 0:
		if err := os.MkdirAll(filepath.Join(path, "b"), 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(path, "b", "file"), []byte("replacement"), 0o600)
	case 1:
		if err := os.Mkdir(path, 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(path, "file"), []byte("replacement"), 0o600)
	default:
		return os.WriteFile(path, []byte("replacement"), 0o600)
	}
}

func createOutsideTree(path string, component int) error {
	switch component {
	case 0:
		if err := os.MkdirAll(filepath.Join(path, "b"), 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(path, "b", "file"), []byte("outside"), 0o600)
	case 1:
		if err := os.Mkdir(path, 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(path, "file"), []byte("outside"), 0o600)
	default:
		return os.WriteFile(path, []byte("outside"), 0o600)
	}
}

func openDescriptorCount(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatalf("read /proc/self/fd: %v", err)
	}
	return len(entries)
}
