//go:build linux

package rootfs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"golang.org/x/sys/unix"
)

func TestOpenSearchTargetTransfersTheVerifiedHandleOnce(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "file"), []byte("content"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.Mkdir(filepath.Join(rootPath, "directory"), 0o700); err != nil {
		t.Fatalf("mkdir directory: %v", err)
	}
	root, lease := openRootAndLease(t, rootPath)
	defer root.Close()
	defer lease.Close()

	fileTarget, err := lease.OpenSearchTarget(mustRelative(t, pathspec.POSIX, "file", false))
	if err != nil {
		t.Fatalf("OpenSearchTarget(file) error = %v", err)
	}
	if got := fileTarget.Kind(); got != SearchTargetRegular {
		t.Fatalf("file target kind = %v, want %v", got, SearchTargetRegular)
	}
	if directory, takeErr := fileTarget.TakeDir(); !errors.Is(takeErr, ErrWrongTargetKind) || directory != nil {
		t.Fatalf("TakeDir(file target) = directory %v, error %v", directory, takeErr)
	}
	file, err := fileTarget.TakeFile()
	if err != nil {
		t.Fatalf("TakeFile() error = %v", err)
	}
	assertLinuxReadOnlyPolicy(t, file.handle.fd)
	if offset, seekErr := unix.Seek(file.handle.fd, 0, io.SeekCurrent); seekErr != nil || offset != 0 {
		t.Fatalf("search(file) classification offset = %d, %v; want zero reads", offset, seekErr)
	}
	if second, takeErr := fileTarget.TakeFile(); !errors.Is(takeErr, ErrTargetConsumed) || second != nil {
		t.Fatalf("second TakeFile() = file %v, error %v", second, takeErr)
	}
	if err := fileTarget.Close(); err != nil {
		t.Fatalf("Close() after transfer error = %v", err)
	}
	if got := readRootFSFile(t, file); got != "content" {
		t.Fatalf("transferred file content = %q, want content", got)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("transferred file Close() error = %v", err)
	}

	directoryTarget, err := lease.OpenSearchTarget(mustRelative(t, pathspec.POSIX, "directory", false))
	if err != nil {
		t.Fatalf("OpenSearchTarget(directory) error = %v", err)
	}
	if got := directoryTarget.Kind(); got != SearchTargetDirectory {
		t.Fatalf("directory target kind = %v, want %v", got, SearchTargetDirectory)
	}
	if takenFile, takeErr := directoryTarget.TakeFile(); !errors.Is(takeErr, ErrWrongTargetKind) || takenFile != nil {
		t.Fatalf("TakeFile(directory target) = file %v, error %v", takenFile, takeErr)
	}
	directory, err := directoryTarget.TakeDir()
	if err != nil {
		t.Fatalf("TakeDir() error = %v", err)
	}
	if err := directoryTarget.Close(); err != nil {
		t.Fatalf("Close() after directory transfer error = %v", err)
	}
	if err := directory.ReadEntries(context.Background(), func(uint64) error { return nil }, func(EnumerationOutcome) error { return nil }); err != nil {
		t.Fatalf("transferred directory ReadEntries() error = %v", err)
	}
	if err := directory.Close(); err != nil {
		t.Fatalf("transferred directory Close() error = %v", err)
	}

	untaken, err := lease.OpenSearchTarget(mustRelative(t, pathspec.POSIX, "file", false))
	if err != nil {
		t.Fatalf("OpenSearchTarget(untaken file) error = %v", err)
	}
	untakenFD := untaken.file.handle.fd
	if err := untaken.Close(); err != nil {
		t.Fatalf("untaken Close() error = %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(untakenFD), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("untaken fd after first Close() = %v, want EBADF", err)
	}
	reusedUntakenFD := reuseClosedFD(t, untakenFD)
	defer unix.Close(reusedUntakenFD)
	if err := untaken.Close(); err != nil {
		t.Fatalf("second untaken Close() error = %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(reusedUntakenFD), unix.F_GETFD, 0); err != nil {
		t.Fatalf("second untaken Close() closed reused fd: %v", err)
	}
	if taken, takeErr := untaken.TakeFile(); !errors.Is(takeErr, ErrTargetConsumed) || taken != nil {
		t.Fatalf("TakeFile() after Close() = file %v, error %v", taken, takeErr)
	}
}

func TestOpenRegularAndSearchTargetRejectSpecialNodesWithoutBlocking(t *testing.T) {
	rootPath := t.TempDir()
	fifoPath := filepath.Join(rootPath, "fifo")
	if err := unix.Mkfifo(fifoPath, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	socketPath := filepath.Join(rootPath, "socket")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	defer listener.Close()
	nodes := []string{"fifo", "socket"}
	devicePath := filepath.Join(rootPath, "device")
	if err := unix.Mknod(devicePath, unix.S_IFCHR|0o600, int(unix.Mkdev(1, 3))); err == nil {
		nodes = append(nodes, "device")
	} else {
		t.Logf("device-node coverage unavailable: %v", err)
	}

	root, lease := openRootAndLease(t, rootPath)
	defer root.Close()
	defer lease.Close()

	for _, forceFallback := range []bool{false, true} {
		name := "openat2"
		if forceFallback {
			name = "fallback"
		}
		t.Run(name, func(t *testing.T) {
			lease.handle.forceFallback = forceFallback
			for _, node := range nodes {
				node := node
				t.Run(node, func(t *testing.T) {
					path := mustRelative(t, pathspec.POSIX, node, false)
					assertPromptSpecialRejection(t, func() error {
						file, openErr := lease.OpenRegular(path)
						if file != nil {
							_ = file.Close()
							return errors.New("OpenRegular exposed a special node")
						}
						return openErr
					})
					assertPromptSpecialRejection(t, func() error {
						target, openErr := lease.OpenSearchTarget(path)
						if target != nil {
							_ = target.Close()
							return errors.New("OpenSearchTarget exposed a special node")
						}
						return openErr
					})
				})
			}
		})
	}
}

func TestOpenSearchTargetClassifiesFinalIdentityAfterEnumeration(t *testing.T) {
	t.Parallel()

	rootPath := t.TempDir()
	pathOnDisk := filepath.Join(rootPath, "candidate")
	if err := os.WriteFile(pathOnDisk, []byte("old"), 0o600); err != nil {
		t.Fatalf("write original: %v", err)
	}
	root, lease := openRootAndLease(t, rootPath)
	defer root.Close()
	defer lease.Close()
	path := mustRelative(t, pathspec.POSIX, "candidate", false)
	enumerated := enumerateNamedEntry(t, lease, "candidate")

	replacement := filepath.Join(rootPath, "replacement")
	if err := os.WriteFile(replacement, []byte("new"), 0o600); err != nil {
		t.Fatalf("write replacement: %v", err)
	}
	if err := os.Rename(replacement, pathOnDisk); err != nil {
		t.Fatalf("replace candidate: %v", err)
	}
	file, err := lease.OpenRegular(path)
	if err != nil {
		t.Fatalf("OpenRegular(replaced candidate) error = %v", err)
	}
	if enumerated.Identity == file.Identity() {
		_ = file.Close()
		t.Fatal("replacement retained the enumerated identity")
	}
	callerOutcome := error(nil)
	if enumerated.IdentityKnown && enumerated.Identity != file.Identity() {
		callerOutcome = ErrSourceChanged
	}
	if !errors.Is(callerOutcome, ErrSourceChanged) {
		_ = file.Close()
		t.Fatalf("identity mismatch outcome = %v, want %v", callerOutcome, ErrSourceChanged)
	}
	if got := readRootFSFile(t, file); got != "new" {
		_ = file.Close()
		t.Fatalf("final handle content = %q, want new", got)
	}
	_ = file.Close()

	if err := os.Remove(pathOnDisk); err != nil {
		t.Fatalf("remove regular candidate: %v", err)
	}
	if err := unix.Mkfifo(pathOnDisk, 0o600); err != nil {
		t.Fatalf("replace candidate with FIFO: %v", err)
	}
	assertPromptSpecialRejection(t, func() error {
		target, openErr := lease.OpenSearchTarget(path)
		if target != nil {
			_ = target.Close()
			return errors.New("regular-to-FIFO replacement exposed a target")
		}
		return openErr
	})
}

func TestVerifiedFileReadContractAndExactlyOnceClose(t *testing.T) {
	content := make([]byte, 4097)
	for index := range content {
		content[index] = byte(index % 251)
	}
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "file"), content, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	root, lease := openRootAndLease(t, rootPath)
	defer root.Close()
	defer lease.Close()
	file, err := lease.OpenRegular(mustRelative(t, pathspec.POSIX, "file", false))
	if err != nil {
		t.Fatalf("OpenRegular() error = %v", err)
	}
	wantIdentity := file.Identity()
	if wantIdentity == (Identity{Platform: pathspec.POSIX}) {
		t.Fatal("OpenRegular() returned an empty identity")
	}
	if got := file.ResolvedPath().String(); got != "file" {
		t.Fatalf("ResolvedPath() = %q, want file", got)
	}
	assertLinuxReadOnlyPolicy(t, file.handle.fd)

	if read, err := file.ReadContext(context.Background(), make([]byte, 4097)); read != 0 || !errors.Is(err, ErrInvalidReadSize) {
		t.Fatalf("ReadContext(4097) = %d, %v", read, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if read, err := file.ReadContext(canceled, make([]byte, 1)); read != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadContext(canceled) = %d, %v", read, err)
	}
	first := make([]byte, 1)
	if read, err := file.ReadContext(context.Background(), first); read != 1 || err != nil || first[0] != content[0] {
		t.Fatalf("ReadContext(1) = %d, %v, %v", read, err, first)
	}
	rest := make([]byte, 4096)
	if read, err := file.ReadContext(context.Background(), rest); read != 4096 || err != nil || !bytes.Equal(rest, content[1:]) {
		t.Fatalf("ReadContext(4096) = %d, %v", read, err)
	}
	if read, err := file.ReadContext(context.Background(), make([]byte, 1)); read != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("ReadContext(EOF) = %d, %v", read, err)
	}

	fileFD := file.handle.fd
	if err := file.Close(); err != nil {
		t.Fatalf("File.Close() error = %v", err)
	}
	if file.Identity() != wantIdentity || file.ResolvedPath().String() != "file" {
		t.Fatal("file evidence changed after close")
	}
	if read, err := file.ReadContext(context.Background(), make([]byte, 1)); read != 0 || !errors.Is(err, ErrClosed) {
		t.Fatalf("ReadContext() after close = %d, %v", read, err)
	}
	if _, err := unix.FcntlInt(uintptr(fileFD), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("file fd after first Close() = %v, want EBADF", err)
	}

	reused := reuseClosedFD(t, fileFD)
	defer unix.Close(reused)
	if err := file.Close(); err != nil {
		t.Fatalf("second File.Close() error = %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(reused), unix.F_GETFD, 0); err != nil {
		t.Fatalf("second File.Close() closed reused fd: %v", err)
	}
}

func TestOpenSearchTargetAllowsHardLinksAndSymlinksButRejectsMounts(t *testing.T) {
	t.Parallel()

	rootPath := t.TempDir()
	original := filepath.Join(rootPath, "original")
	if err := os.WriteFile(original, []byte("content"), 0o600); err != nil {
		t.Fatalf("write original: %v", err)
	}
	if err := os.Link(original, filepath.Join(rootPath, "linked")); err != nil {
		t.Fatalf("hard link: %v", err)
	}
	if err := os.Symlink("original", filepath.Join(rootPath, "symlink")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	root, lease := openRootAndLease(t, rootPath)
	defer root.Close()
	defer lease.Close()
	first, err := lease.OpenSearchTarget(mustRelative(t, pathspec.POSIX, "original", false))
	if err != nil {
		t.Fatalf("OpenSearchTarget(original) error = %v", err)
	}
	firstFile, err := first.TakeFile()
	if err != nil {
		t.Fatalf("TakeFile(original) error = %v", err)
	}
	defer firstFile.Close()
	linked, err := lease.OpenSearchTarget(mustRelative(t, pathspec.POSIX, "linked", false))
	if err != nil {
		t.Fatalf("OpenSearchTarget(linked) error = %v", err)
	}
	linkedFile, err := linked.TakeFile()
	if err != nil {
		t.Fatalf("TakeFile(linked) error = %v", err)
	}
	defer linkedFile.Close()
	if firstFile.Identity() != linkedFile.Identity() {
		t.Fatalf("hard-link identities differ: %#v != %#v", firstFile.Identity(), linkedFile.Identity())
	}
	target, err := lease.OpenSearchTarget(mustRelative(t, pathspec.POSIX, "symlink", false))
	if err != nil {
		t.Fatalf("OpenSearchTarget(symlink) error = %v", err)
	}
	targetFile, err := target.TakeFile()
	if err != nil {
		_ = target.Close()
		t.Fatalf("TakeFile(symlink) error = %v", err)
	}
	defer targetFile.Close()
	_ = target.Close()
	if targetFile.Identity() != firstFile.Identity() {
		t.Fatalf("symlink identity = %#v, want %#v", targetFile.Identity(), firstFile.Identity())
	}

	filesystemRoot, filesystemLease := openRootAndLease(t, "/")
	defer filesystemRoot.Close()
	defer filesystemLease.Close()
	procRoot, err := OpenRoot(mustRootDirectory(t, "/proc"))
	if err != nil {
		t.Skipf("cannot open /proc for mount evidence: %v", err)
	}
	defer procRoot.Close()
	if procRoot.Identity().Mount == filesystemRoot.Identity().Mount {
		t.Skip("/proc is not a distinct mount in this environment")
	}
	if target, err := filesystemLease.OpenSearchTarget(mustRelative(t, pathspec.POSIX, "proc", false)); target != nil || !errors.Is(err, ErrMountBoundary) {
		if target != nil {
			_ = target.Close()
		}
		t.Fatalf("OpenSearchTarget(proc) = target %v, error %v", target, err)
	}
}

func enumerateNamedEntry(t *testing.T, lease *Lease, name string) Entry {
	t.Helper()
	directory, err := lease.OpenDir(mustRelative(t, pathspec.POSIX, ".", true))
	if err != nil {
		t.Fatalf("OpenDir(.) error = %v", err)
	}
	defer directory.Close()
	var found Entry
	err = directory.ReadEntries(context.Background(), func(uint64) error { return nil }, func(outcome EnumerationOutcome) error {
		entry, ok := outcome.Candidate()
		if ok && entry.Path.String() == name {
			found = entry
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ReadEntries() error = %v", err)
	}
	if found.Path.String() != name || !found.IdentityKnown {
		t.Fatalf("enumerated entry = %#v, want identity-known %q", found, name)
	}
	return found
}

func assertPromptSpecialRejection(t *testing.T, open func() error) {
	t.Helper()
	result := make(chan error, 1)
	go func() { result <- open() }()
	select {
	case err := <-result:
		if !errors.Is(err, ErrSpecial) {
			t.Fatalf("special-node open error = %v, want %v", err, ErrSpecial)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("special-node open blocked")
	}
}

func assertLinuxReadOnlyPolicy(t *testing.T, fd int) {
	t.Helper()
	status, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		t.Fatalf("F_GETFL: %v", err)
	}
	if status&unix.O_ACCMODE != unix.O_RDONLY || status&unix.O_NONBLOCK == 0 {
		t.Fatalf("open status flags = %#x, want read-only nonblocking", status)
	}
	descriptor, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	if err != nil {
		t.Fatalf("F_GETFD: %v", err)
	}
	if descriptor&unix.FD_CLOEXEC == 0 {
		t.Fatalf("descriptor flags = %#x, want close-on-exec", descriptor)
	}
}

func reuseClosedFD(t *testing.T, closedFD int) int {
	t.Helper()
	probe, err := unix.Open("/dev/null", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open fd-reuse probe: %v", err)
	}
	if probe == closedFD {
		return probe
	}
	if err := unix.Dup3(probe, closedFD, unix.O_CLOEXEC); err != nil {
		_ = unix.Close(probe)
		t.Fatalf("dup fd-reuse probe: %v", err)
	}
	_ = unix.Close(probe)
	return closedFD
}
