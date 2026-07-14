//go:build linux

package rootfs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"unsafe"

	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"golang.org/x/sys/unix"
)

func TestLinuxReadEntriesPreservesPhysicalOrderAndChargesRawRecordsFirst(t *testing.T) {
	rootPath := t.TempDir()
	for _, name := range []string{"c", "a", "b"} {
		if err := os.WriteFile(filepath.Join(rootPath, name), []byte(name), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(rootPath, ".git"), 0o700); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, ".git", "descendant"), []byte("nested"), 0o600); err != nil {
		t.Fatalf("write descendant: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, ".svn"), []byte("metadata"), 0o600); err != nil {
		t.Fatalf("write .svn: %v", err)
	}
	if err := os.Symlink(".git", filepath.Join(rootPath, "node_modules")); err != nil {
		t.Fatalf("node_modules symlink: %v", err)
	}
	if err := unix.Mkfifo(filepath.Join(rootPath, ".hg"), 0o600); err != nil {
		t.Fatalf("mkfifo .hg: %v", err)
	}
	if err := os.Symlink("c", filepath.Join(rootPath, "link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := unix.Mkfifo(filepath.Join(rootPath, "fifo"), 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	invalidName := string([]byte{'b', 'a', 'd', '-', 0xff})
	if err := os.WriteFile(filepath.Join(rootPath, invalidName), []byte("invalid"), 0o600); err != nil {
		t.Fatalf("write invalid UTF-8 name: %v", err)
	}

	rawRecords := readLinuxRawDirectory(t, rootPath)
	expectedEvents := make([]string, 0, len(rawRecords)*2)
	for _, record := range rawRecords {
		expectedEvents = append(expectedEvents, fmt.Sprintf("charge:%d", record.size))
		if bytes.Equal(record.name, []byte(".")) || bytes.Equal(record.name, []byte("..")) {
			continue
		}
		if bytes.Equal(record.name, []byte(invalidName)) {
			expectedEvents = append(expectedEvents, "outcome:encoding")
		} else {
			expectedEvents = append(expectedEvents, "outcome:"+string(record.name))
		}
	}

	root, lease := openRootAndLease(t, rootPath)
	defer root.Close()
	defer lease.Close()
	directory, err := lease.OpenDir(mustRelative(t, pathspec.POSIX, ".", true))
	if err != nil {
		t.Fatalf("OpenDir(.) error = %v", err)
	}
	defer directory.Close()
	events := make([]string, 0, len(expectedEvents))
	kinds := make(map[string]EntryKind)
	err = directory.ReadEntries(context.Background(), func(rawBytes uint64) error {
		events = append(events, fmt.Sprintf("charge:%d", rawBytes))
		return nil
	}, func(outcome EnumerationOutcome) error {
		if outcome.Disposition() == EnumerationPathEncodingUnsupported {
			if outcome.BoundaryKind() != EntryFile {
				t.Fatalf("invalid-name boundary kind = %v, want %v", outcome.BoundaryKind(), EntryFile)
			}
			assertPathlessOutcome(t, outcome)
			events = append(events, "outcome:encoding")
			return nil
		}
		entry, ok := outcome.Candidate()
		if !ok {
			t.Fatalf("unexpected non-candidate disposition %v", outcome.Disposition())
		}
		if !entry.IdentityKnown || entry.Identity.Platform != pathspec.POSIX || entry.Identity == (Identity{Platform: pathspec.POSIX}) {
			t.Fatalf("candidate %q identity = %#v, known=%v", entry.Path.String(), entry.Identity, entry.IdentityKnown)
		}
		kinds[entry.Path.String()] = entry.Kind
		events = append(events, "outcome:"+entry.Path.String())
		return nil
	})
	if err != nil {
		t.Fatalf("ReadEntries() error = %v", err)
	}
	if !reflect.DeepEqual(events, expectedEvents) {
		t.Fatalf("stream events differ\n got: %#v\nwant: %#v", events, expectedEvents)
	}
	wantKinds := map[string]EntryKind{
		"c": EntryFile, "a": EntryFile, "b": EntryFile,
		".git": EntryDir, ".svn": EntryFile, "node_modules": EntrySymlink, ".hg": EntrySpecial,
		"link": EntrySymlink, "fifo": EntrySpecial,
	}
	for path, want := range wantKinds {
		if got := kinds[path]; got != want {
			t.Fatalf("kind %q = %v, want %v", path, got, want)
		}
	}
	if _, found := kinds[".git/descendant"]; found {
		t.Fatal("ReadEntries enumerated a directory descendant")
	}
}

func TestLinuxReadEntriesStopsBeforeOutcomeOnChargeFailure(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "file"), []byte("data"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	root, lease := openRootAndLease(t, rootPath)
	defer root.Close()
	defer lease.Close()
	directory, err := lease.OpenDir(mustRelative(t, pathspec.POSIX, ".", true))
	if err != nil {
		t.Fatalf("OpenDir(.) error = %v", err)
	}
	defer directory.Close()
	want := errors.New("charge stopped")
	consumed := 0
	err = directory.ReadEntries(context.Background(), func(uint64) error { return want }, func(EnumerationOutcome) error {
		consumed++
		return nil
	})
	if !errors.Is(err, want) || consumed != 0 {
		t.Fatalf("ReadEntries() = error %v, consumed %d; want charge error and zero outcomes", err, consumed)
	}
}

func TestLinuxReadEntriesValidatesCallbacksBeforeIOAndPropagatesConsumeFailure(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "file"), []byte("data"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	root, lease := openRootAndLease(t, rootPath)
	defer root.Close()
	defer lease.Close()
	directory, err := lease.OpenDir(mustRelative(t, pathspec.POSIX, ".", true))
	if err != nil {
		t.Fatalf("OpenDir(.) error = %v", err)
	}
	defer directory.Close()
	if err := directory.ReadEntries(context.Background(), nil, func(EnumerationOutcome) error { return nil }); !errors.Is(err, ErrInvalidCallback) {
		t.Fatalf("nil charge error = %v, want %v", err, ErrInvalidCallback)
	}
	if err := directory.ReadEntries(context.Background(), func(uint64) error { return nil }, nil); !errors.Is(err, ErrInvalidCallback) {
		t.Fatalf("nil consume error = %v, want %v", err, ErrInvalidCallback)
	}
	if err := directory.ReadEntries(nil, func(uint64) error { return nil }, func(EnumerationOutcome) error { return nil }); !errors.Is(err, ErrIO) {
		t.Fatalf("nil context error = %v, want %v", err, ErrIO)
	}
	want := errors.New("consume stopped")
	consumed := 0
	err = directory.ReadEntries(context.Background(), func(uint64) error { return nil }, func(EnumerationOutcome) error {
		consumed++
		return want
	})
	if !errors.Is(err, want) || consumed != 1 {
		t.Fatalf("consume failure = error %v, consumed %d; want sentinel after one outcome", err, consumed)
	}
}

func TestLinuxReadEntriesCancellationCloseAndReadAfterClose(t *testing.T) {
	root, lease := openRootAndLease(t, t.TempDir())
	defer root.Close()
	defer lease.Close()
	directory, err := lease.OpenDir(mustRelative(t, pathspec.POSIX, ".", true))
	if err != nil {
		t.Fatalf("OpenDir(.) error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	charges := 0
	consumes := 0
	err = directory.ReadEntries(ctx, func(uint64) error { charges++; return nil }, func(EnumerationOutcome) error {
		consumes++
		return nil
	})
	if !errors.Is(err, context.Canceled) || charges != 0 || consumes != 0 {
		t.Fatalf("canceled ReadEntries = error %v, charges %d, consumes %d", err, charges, consumes)
	}
	fd := directory.handle.fd
	if err := directory.Close(); err != nil {
		t.Fatalf("directory Close() error = %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("closed directory fd check = %v, want EBADF", err)
	}
	reused := reuseClosedFD(t, fd)
	defer unix.Close(reused)
	if err := directory.Close(); err != nil {
		t.Fatalf("second directory Close() error = %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(reused), unix.F_GETFD, 0); err != nil {
		t.Fatalf("second directory Close() closed reused fd: %v", err)
	}
	if err := directory.ReadEntries(context.Background(), func(uint64) error { return nil }, func(EnumerationOutcome) error { return nil }); !errors.Is(err, ErrClosed) {
		t.Fatalf("ReadEntries after close error = %v, want %v", err, ErrClosed)
	}
}

type linuxRawDirectoryRecord struct {
	size uint64
	name []byte
}

func readLinuxRawDirectory(t *testing.T, directory string) []linuxRawDirectoryRecord {
	t.Helper()
	fd, err := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open raw directory: %v", err)
	}
	defer unix.Close(fd)
	nameOffset := int(unsafe.Offsetof(unix.Dirent{}.Name))
	buffer := make([]byte, 32*1024)
	var records []linuxRawDirectoryRecord
	for {
		read, err := unix.Getdents(fd, buffer)
		if err != nil {
			t.Fatalf("getdents: %v", err)
		}
		if read == 0 {
			return records
		}
		for offset := 0; offset < read; {
			if read-offset < nameOffset {
				t.Fatalf("short dirent at offset %d", offset)
			}
			entry := (*unix.Dirent)(unsafe.Pointer(&buffer[offset]))
			recordLength := int(entry.Reclen)
			if recordLength < nameOffset || recordLength > read-offset {
				t.Fatalf("invalid reclen %d at offset %d", recordLength, offset)
			}
			name := buffer[offset+nameOffset : offset+recordLength]
			if terminator := bytes.IndexByte(name, 0); terminator >= 0 {
				name = name[:terminator]
			}
			records = append(records, linuxRawDirectoryRecord{size: uint64(recordLength), name: append([]byte(nil), name...)})
			offset += recordLength
		}
	}
}
