//go:build linux

package rootfs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"golang.org/x/sys/unix"
)

func TestOpenRootUsesOneCanonicalDirectoryHandle(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	root, err := OpenRoot(mustRootDirectory(t, directory))
	if err != nil {
		t.Fatalf("OpenRoot() error = %v", err)
	}
	expectedCanonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatalf("resolve test directory: %v", err)
	}
	if got := root.CanonicalPath(); got != expectedCanonical {
		t.Fatalf("CanonicalPath() = %q, want %q", got, expectedCanonical)
	}
	identity := root.Identity()
	if identity.Platform != pathspec.POSIX || identity == (Identity{Platform: pathspec.POSIX}) {
		t.Fatalf("Identity() = %#v, want nonzero POSIX identity", identity)
	}
	if err := root.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := root.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if root.CanonicalPath() != expectedCanonical || root.Identity() != identity {
		t.Fatal("root metadata changed after close")
	}
}

func TestOpenRootFollowsOnlyTheSelectedRootSymlink(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	link := filepath.Join(parent, "root-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink root: %v", err)
	}

	linkedRoot, err := OpenRoot(mustRootDirectory(t, link))
	if err != nil {
		t.Fatalf("OpenRoot(symlink) error = %v", err)
	}
	defer linkedRoot.Close()
	targetRoot, err := OpenRoot(mustRootDirectory(t, target))
	if err != nil {
		t.Fatalf("OpenRoot(target) error = %v", err)
	}
	defer targetRoot.Close()
	if linkedRoot.CanonicalPath() != targetRoot.CanonicalPath() {
		t.Fatalf("symlink canonical = %q, target canonical = %q", linkedRoot.CanonicalPath(), targetRoot.CanonicalPath())
	}
	if linkedRoot.Identity() != targetRoot.Identity() {
		t.Fatalf("symlink identity = %#v, target identity = %#v", linkedRoot.Identity(), targetRoot.Identity())
	}
}

func TestOpenRootCanonicalAndIdentityComeFromSameHandleDuringReplacement(t *testing.T) {
	parent := t.TempDir()
	targetA := filepath.Join(parent, "a")
	targetB := filepath.Join(parent, "b")
	if err := os.Mkdir(targetA, 0o700); err != nil {
		t.Fatalf("mkdir target A: %v", err)
	}
	if err := os.Mkdir(targetB, 0o700); err != nil {
		t.Fatalf("mkdir target B: %v", err)
	}
	rootA, err := OpenRoot(mustRootDirectory(t, targetA))
	if err != nil {
		t.Fatalf("open target A: %v", err)
	}
	defer rootA.Close()
	rootB, err := OpenRoot(mustRootDirectory(t, targetB))
	if err != nil {
		t.Fatalf("open target B: %v", err)
	}
	defer rootB.Close()
	want := map[string]Identity{
		rootA.CanonicalPath(): rootA.Identity(),
		rootB.CanonicalPath(): rootB.Identity(),
	}

	link := filepath.Join(parent, "root-link")
	if err := os.Symlink(targetA, link); err != nil {
		t.Fatalf("create initial root link: %v", err)
	}
	stop := make(chan struct{})
	var replacer sync.WaitGroup
	replacer.Add(1)
	go func() {
		defer replacer.Done()
		for index := 0; ; index++ {
			select {
			case <-stop:
				return
			default:
			}
			target := targetA
			if index%2 == 1 {
				target = targetB
			}
			temporary := filepath.Join(parent, "replacement-link")
			_ = os.Remove(temporary)
			if err := os.Symlink(target, temporary); err != nil {
				return
			}
			if err := os.Rename(temporary, link); err != nil {
				return
			}
		}
	}()
	defer func() {
		close(stop)
		replacer.Wait()
	}()

	for iteration := 0; iteration < 200; iteration++ {
		root, err := OpenRoot(mustRootDirectory(t, link))
		if err != nil {
			t.Fatalf("OpenRoot() during replacement error = %v", err)
		}
		identity, knownCanonical := want[root.CanonicalPath()]
		if !knownCanonical || identity != root.Identity() {
			_ = root.Close()
			t.Fatalf("mixed handle evidence: canonical=%q identity=%#v", root.CanonicalPath(), root.Identity())
		}
		if err := root.Close(); err != nil {
			t.Fatalf("Close() during replacement error = %v", err)
		}
	}
}

func TestOpenRootSurvivesRenameAfterOpen(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	original := filepath.Join(parent, "original")
	renamed := filepath.Join(parent, "renamed")
	if err := os.Mkdir(original, 0o700); err != nil {
		t.Fatalf("mkdir original: %v", err)
	}
	root, err := OpenRoot(mustRootDirectory(t, original))
	if err != nil {
		t.Fatalf("OpenRoot() error = %v", err)
	}
	defer root.Close()
	canonicalAtOpen := root.CanonicalPath()
	identityAtOpen := root.Identity()
	if err := os.Rename(original, renamed); err != nil {
		t.Fatalf("rename root: %v", err)
	}
	if root.CanonicalPath() != canonicalAtOpen || root.Identity() != identityAtOpen {
		t.Fatal("stored root evidence changed after rename")
	}
}

func TestOpenRootRejectsMissingFileAndSpecialRoots(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	missing := filepath.Join(parent, "missing")
	assertOpenRootError(t, missing, ErrNotFound)

	file := filepath.Join(parent, "file")
	if err := os.WriteFile(file, []byte("data"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	assertOpenRootError(t, file, ErrNotDirectory)

	fifo := filepath.Join(parent, "fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	assertOpenRootError(t, fifo, ErrNotDirectory)
}

func TestRootDuplicateSurvivesOriginalCloseAndFDReuse(t *testing.T) {
	root, err := OpenRoot(mustRootDirectory(t, t.TempDir()))
	if err != nil {
		t.Fatalf("OpenRoot() error = %v", err)
	}
	wantIdentity := root.Identity()
	originalFD := root.handle.fd
	lease, err := root.Duplicate()
	if err != nil {
		t.Fatalf("Duplicate() error = %v", err)
	}
	defer lease.Close()
	if lease.handle.fd == originalFD {
		t.Fatalf("duplicate reused original fd %d", originalFD)
	}
	flags, err := unix.FcntlInt(uintptr(lease.handle.fd), unix.F_GETFD, 0)
	if err != nil {
		t.Fatalf("F_GETFD duplicate: %v", err)
	}
	if flags&unix.FD_CLOEXEC == 0 {
		t.Fatal("duplicate root fd does not have close-on-exec")
	}
	if err := root.Close(); err != nil {
		t.Fatalf("root Close() error = %v", err)
	}
	if duplicate, err := root.Duplicate(); !errors.Is(err, ErrClosed) || duplicate != nil {
		t.Fatalf("Duplicate() after root close = lease %v, error %v", duplicate, err)
	}

	reusedFD, err := unix.Open("/dev/null", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open fd-reuse probe: %v", err)
	}
	defer unix.Close(reusedFD)
	if reusedFD != originalFD {
		t.Fatalf("fd reuse probe = %d, want closed root fd %d", reusedFD, originalFD)
	}

	directory, err := lease.OpenDir(mustRelative(t, pathspec.POSIX, ".", true))
	if err != nil {
		t.Fatalf("lease OpenDir(.) after root close/reuse error = %v", err)
	}
	defer directory.Close()
	if directory.Identity() != wantIdentity {
		t.Fatalf("duplicate identity = %#v, want %#v", directory.Identity(), wantIdentity)
	}
}

func TestLeaseCloseIsIdempotentAndRejectsNewOperations(t *testing.T) {
	root, err := OpenRoot(mustRootDirectory(t, t.TempDir()))
	if err != nil {
		t.Fatalf("OpenRoot() error = %v", err)
	}
	defer root.Close()
	lease, err := root.Duplicate()
	if err != nil {
		t.Fatalf("Duplicate() error = %v", err)
	}
	leaseFD := lease.handle.fd
	if err := lease.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(leaseFD), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("closed lease fd check = %v, want EBADF", err)
	}
	if directory, err := lease.OpenDir(mustRelative(t, pathspec.POSIX, ".", true)); !errors.Is(err, ErrClosed) || directory != nil {
		t.Fatalf("OpenDir() after close = directory %v, error %v", directory, err)
	}
}

func TestWithBorrowInvalidatesRetainedView(t *testing.T) {
	t.Parallel()

	root, err := OpenRoot(mustRootDirectory(t, t.TempDir()))
	if err != nil {
		t.Fatalf("OpenRoot() error = %v", err)
	}
	defer root.Close()
	lease, err := root.Duplicate()
	if err != nil {
		t.Fatalf("Duplicate() error = %v", err)
	}
	defer lease.Close()
	var retained Borrowed
	opened, err := WithBorrow(lease, func(borrowed Borrowed) bool {
		retained = borrowed
		directory, openErr := borrowed.OpenDir(mustRelative(t, pathspec.POSIX, ".", true))
		if openErr != nil {
			t.Errorf("borrowed OpenDir(.) error = %v", openErr)
			return false
		}
		defer directory.Close()
		return directory.Identity() == root.Identity()
	})
	if err != nil || !opened {
		t.Fatalf("WithBorrow() = %v, %v", opened, err)
	}
	if directory, err := retained.OpenDir(mustRelative(t, pathspec.POSIX, ".", true)); !errors.Is(err, ErrBorrowExpired) || directory != nil {
		t.Fatalf("retained OpenDir() = directory %v, error %v", directory, err)
	}
	if file, err := retained.OpenRegular(mustRelative(t, pathspec.POSIX, "file", false)); !errors.Is(err, ErrBorrowExpired) || file != nil {
		t.Fatalf("retained OpenRegular() = file %v, error %v", file, err)
	}
}

func TestLeaseCloseDuringConcurrentBorrowsDefersOnePlatformClose(t *testing.T) {
	root, err := OpenRoot(mustRootDirectory(t, t.TempDir()))
	if err != nil {
		t.Fatalf("OpenRoot() error = %v", err)
	}
	defer root.Close()
	lease, err := root.Duplicate()
	if err != nil {
		t.Fatalf("Duplicate() error = %v", err)
	}
	leaseFD := lease.handle.fd
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	results := make(chan error, 2)
	rootPath := mustRelative(t, pathspec.POSIX, ".", true)
	for worker := 0; worker < 2; worker++ {
		go func() {
			operationErr, borrowErr := WithBorrow(lease, func(borrowed Borrowed) error {
				entered <- struct{}{}
				<-release
				directory, openErr := borrowed.OpenDir(rootPath)
				if openErr == nil {
					openErr = directory.Close()
				}
				return openErr
			})
			if borrowErr != nil {
				operationErr = borrowErr
			}
			results <- operationErr
		}()
	}
	<-entered
	<-entered
	if err := lease.Close(); err != nil {
		t.Fatalf("Close() during borrows error = %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(leaseFD), unix.F_GETFD, 0); err != nil {
		t.Fatalf("lease fd closed before borrows returned: %v", err)
	}
	if directory, err := lease.OpenDir(rootPath); !errors.Is(err, ErrClosed) || directory != nil {
		t.Fatalf("direct OpenDir() after close request = directory %v, error %v", directory, err)
	}
	close(release)
	for worker := 0; worker < 2; worker++ {
		if err := <-results; err != nil {
			t.Fatalf("borrowed operation after close request error = %v", err)
		}
	}
	if _, err := unix.FcntlInt(uintptr(leaseFD), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("lease fd after final borrow = %v, want EBADF", err)
	}
}

func TestWithBorrowNestedAndPanicCleanup(t *testing.T) {
	t.Parallel()

	root, err := OpenRoot(mustRootDirectory(t, t.TempDir()))
	if err != nil {
		t.Fatalf("OpenRoot() error = %v", err)
	}
	defer root.Close()
	lease, err := root.Duplicate()
	if err != nil {
		t.Fatalf("Duplicate() error = %v", err)
	}
	defer lease.Close()
	nested, err := WithBorrow(lease, func(Borrowed) bool {
		inner, innerErr := WithBorrow(lease, func(Borrowed) bool { return true })
		return innerErr == nil && inner
	})
	if err != nil || !nested {
		t.Fatalf("nested WithBorrow() = %v, %v", nested, err)
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("WithBorrow callback panic was swallowed")
			}
		}()
		_, _ = WithBorrow(lease, func(Borrowed) struct{} {
			panic("test panic")
		})
	}()
	lease.mu.Lock()
	activeBorrows := lease.activeBorrows
	lease.mu.Unlock()
	if activeBorrows != 0 {
		t.Fatalf("active borrows after panic = %d, want 0", activeBorrows)
	}
	if _, err := WithBorrow[struct{}](lease, nil); !errors.Is(err, ErrInvalidCallback) {
		t.Fatalf("WithBorrow(nil callback) error = %v", err)
	}
	directory, err := lease.OpenDir(mustRelative(t, pathspec.POSIX, ".", true))
	if err != nil {
		t.Fatalf("lease unusable after callback panic: %v", err)
	}
	if err := directory.Close(); err != nil {
		t.Fatalf("close directory after callback panic: %v", err)
	}
}

func TestFileReadContextRejectsOversizedBuffersBeforeRead(t *testing.T) {
	t.Parallel()

	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "file"), []byte("data"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	root, err := OpenRoot(mustRootDirectory(t, rootPath))
	if err != nil {
		t.Fatalf("OpenRoot() error = %v", err)
	}
	defer root.Close()
	lease, err := root.Duplicate()
	if err != nil {
		t.Fatalf("Duplicate() error = %v", err)
	}
	defer lease.Close()
	file, err := lease.OpenRegular(mustRelative(t, pathspec.POSIX, "file", false))
	if err != nil {
		t.Fatalf("OpenRegular() error = %v", err)
	}
	defer file.Close()
	if read, err := file.ReadContext(context.Background(), make([]byte, 4097)); !errors.Is(err, ErrInvalidReadSize) || read != 0 {
		t.Fatalf("ReadContext(4097) = %d, %v", read, err)
	}
	buffer := make([]byte, 4)
	read, err := file.ReadContext(context.Background(), buffer)
	if err != nil || read != 4 || string(buffer) != "data" {
		t.Fatalf("ReadContext(4) = %d, %v, %q", read, err, buffer)
	}
}

func TestLinuxOpenat2RejectsSymlinksAndWrongKinds(t *testing.T) {
	t.Parallel()

	rootPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootPath, "directory"), 0o700); err != nil {
		t.Fatalf("mkdir directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "file"), []byte("inside"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.Symlink("file", filepath.Join(rootPath, "relative-link")); err != nil {
		t.Fatalf("relative symlink: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(rootPath, "absolute-link")); err != nil {
		t.Fatalf("absolute symlink: %v", err)
	}
	if err := os.Symlink("loop", filepath.Join(rootPath, "loop")); err != nil {
		t.Fatalf("loop symlink: %v", err)
	}
	if err := os.Symlink("directory", filepath.Join(rootPath, "directory-link")); err != nil {
		t.Fatalf("directory symlink: %v", err)
	}

	root, lease := openRootAndLease(t, rootPath)
	defer root.Close()
	defer lease.Close()
	for _, raw := range []string{"relative-link", "absolute-link", "loop"} {
		file, err := lease.OpenRegular(mustRelative(t, pathspec.POSIX, raw, false))
		if file != nil {
			_ = file.Close()
			t.Fatalf("OpenRegular(%q) followed a symlink", raw)
		}
		if !errors.Is(err, ErrSymlink) {
			t.Fatalf("OpenRegular(%q) error = %v, want %v", raw, err, ErrSymlink)
		}
	}
	if directory, err := lease.OpenDir(mustRelative(t, pathspec.POSIX, "directory-link", false)); !errors.Is(err, ErrSymlink) || directory != nil {
		t.Fatalf("OpenDir(directory-link) = directory %v, error %v", directory, err)
	}
	if directory, err := lease.OpenDir(mustRelative(t, pathspec.POSIX, "file", false)); !errors.Is(err, ErrNotDirectory) || directory != nil {
		t.Fatalf("OpenDir(file) = directory %v, error %v", directory, err)
	}
	if file, err := lease.OpenRegular(mustRelative(t, pathspec.POSIX, "directory", false)); !errors.Is(err, ErrNotRegular) || file != nil {
		t.Fatalf("OpenRegular(directory) = file %v, error %v", file, err)
	}
}

func TestLinuxOpenat2RejectsProcMagicLinks(t *testing.T) {
	t.Parallel()

	probe, err := os.CreateTemp(t.TempDir(), "probe")
	if err != nil {
		t.Fatalf("create probe: %v", err)
	}
	defer probe.Close()
	root, lease := openRootAndLease(t, "/proc/self/fd")
	defer root.Close()
	defer lease.Close()
	path := mustRelative(t, pathspec.POSIX, strconv.Itoa(int(probe.Fd())), false)
	file, err := lease.OpenRegular(path)
	if file != nil {
		_ = file.Close()
		t.Fatal("OpenRegular followed a procfs magic link")
	}
	if !errors.Is(err, ErrSymlink) {
		t.Fatalf("OpenRegular(proc fd) error = %v, want %v", err, ErrSymlink)
	}
}

func TestLinuxOpenat2RejectsMountBoundary(t *testing.T) {
	root, lease := openRootAndLease(t, "/")
	defer root.Close()
	defer lease.Close()
	procRoot, err := OpenRoot(mustRootDirectory(t, "/proc"))
	if err != nil {
		t.Skipf("cannot open /proc for mount evidence: %v", err)
	}
	defer procRoot.Close()
	if procRoot.Identity().Mount == root.Identity().Mount {
		t.Skip("/proc is not a distinct mount in this environment")
	}
	directory, err := lease.OpenDir(mustRelative(t, pathspec.POSIX, "proc", false))
	if directory != nil {
		_ = directory.Close()
		t.Fatal("OpenDir(proc) crossed a mount boundary")
	}
	if !errors.Is(err, ErrMountBoundary) {
		t.Fatalf("OpenDir(proc) error = %v, want %v", err, ErrMountBoundary)
	}
}

func TestLinuxOpenat2AllowsHardLinksInsideRoot(t *testing.T) {
	t.Parallel()

	rootPath := t.TempDir()
	original := filepath.Join(rootPath, "original")
	linked := filepath.Join(rootPath, "linked")
	if err := os.WriteFile(original, []byte("content"), 0o600); err != nil {
		t.Fatalf("write original: %v", err)
	}
	if err := os.Link(original, linked); err != nil {
		t.Fatalf("hard link: %v", err)
	}
	root, lease := openRootAndLease(t, rootPath)
	defer root.Close()
	defer lease.Close()
	first, err := lease.OpenRegular(mustRelative(t, pathspec.POSIX, "original", false))
	if err != nil {
		t.Fatalf("OpenRegular(original) error = %v", err)
	}
	defer first.Close()
	second, err := lease.OpenRegular(mustRelative(t, pathspec.POSIX, "linked", false))
	if err != nil {
		t.Fatalf("OpenRegular(linked) error = %v", err)
	}
	defer second.Close()
	if first.Identity() != second.Identity() {
		t.Fatalf("hard-link identities differ: %#v != %#v", first.Identity(), second.Identity())
	}
	if got := readRootFSFile(t, second); got != "content" {
		t.Fatalf("hard-link content = %q, want content", got)
	}
}

func TestLinuxOpenat2ComponentReplacementNeverEscapes(t *testing.T) {
	testLinuxComponentReplacementNeverEscapes(t, false)
}

func TestLinuxForcedFallbackComponentReplacementNeverEscapes(t *testing.T) {
	testLinuxComponentReplacementNeverEscapes(t, true)
}

func testLinuxComponentReplacementNeverEscapes(t *testing.T, forceFallback bool) {
	t.Helper()
	rootPath := t.TempDir()
	insideDirectory := filepath.Join(rootPath, "slot")
	if err := os.Mkdir(insideDirectory, 0o700); err != nil {
		t.Fatalf("mkdir slot: %v", err)
	}
	if err := os.WriteFile(filepath.Join(insideDirectory, "file"), []byte("inside"), 0o600); err != nil {
		t.Fatalf("write inside file: %v", err)
	}
	outsideDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDirectory, "file"), []byte("outside"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	alternate := filepath.Join(rootPath, "alternate")
	if err := os.Symlink(outsideDirectory, alternate); err != nil {
		t.Fatalf("create alternate symlink: %v", err)
	}
	if err := unix.Renameat2(unix.AT_FDCWD, insideDirectory, unix.AT_FDCWD, alternate, unix.RENAME_EXCHANGE); err != nil {
		t.Skipf("RENAME_EXCHANGE unavailable: %v", err)
	}
	if err := unix.Renameat2(unix.AT_FDCWD, insideDirectory, unix.AT_FDCWD, alternate, unix.RENAME_EXCHANGE); err != nil {
		t.Fatalf("restore exchange probe: %v", err)
	}

	root, lease := openRootAndLease(t, rootPath)
	defer root.Close()
	defer lease.Close()
	lease.handle.forceFallback = forceFallback
	stop := make(chan struct{})
	exchangeErrors := make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				exchangeErrors <- nil
				return
			default:
			}
			if err := unix.Renameat2(unix.AT_FDCWD, insideDirectory, unix.AT_FDCWD, alternate, unix.RENAME_EXCHANGE); err != nil {
				exchangeErrors <- err
				return
			}
			runtime.Gosched()
		}
	}()
	path := mustRelative(t, pathspec.POSIX, "slot/file", false)
	successes := 0
	rejections := 0
	for iteration := 0; iteration < 500; iteration++ {
		file, err := lease.OpenRegular(path)
		if err != nil {
			if !errors.Is(err, ErrSymlink) && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrSourceChanged) {
				close(stop)
				<-exchangeErrors
				t.Fatalf("OpenRegular() during exchange error = %v", err)
			}
			rejections++
			continue
		}
		successes++
		content := readRootFSFile(t, file)
		if closeErr := file.Close(); closeErr != nil {
			close(stop)
			<-exchangeErrors
			t.Fatalf("file Close() error = %v", closeErr)
		}
		if content != "inside" {
			close(stop)
			<-exchangeErrors
			t.Fatalf("component replacement escaped root: %q", content)
		}
	}
	close(stop)
	if err := <-exchangeErrors; err != nil {
		t.Fatalf("exchange loop error = %v", err)
	}
	if successes == 0 || rejections == 0 {
		t.Logf("scheduler observed %d successes and %d rejections; deterministic barrier tests cover both states", successes, rejections)
	}
}

func TestLinuxForcedFallbackContainmentMatrix(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootPath, "directory"), 0o700); err != nil {
		t.Fatalf("mkdir directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "file"), []byte("inside"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.Link(filepath.Join(rootPath, "file"), filepath.Join(rootPath, "hard-link")); err != nil {
		t.Fatalf("hard link: %v", err)
	}
	if err := os.Symlink("file", filepath.Join(rootPath, "file-link")); err != nil {
		t.Fatalf("file symlink: %v", err)
	}
	if err := os.Symlink("directory", filepath.Join(rootPath, "directory-link")); err != nil {
		t.Fatalf("directory symlink: %v", err)
	}
	if err := os.Symlink("directory", filepath.Join(rootPath, "intermediate-link")); err != nil {
		t.Fatalf("intermediate symlink: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "directory", "nested"), []byte("nested"), 0o600); err != nil {
		t.Fatalf("write nested file: %v", err)
	}
	fifo := filepath.Join(rootPath, "fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	root, lease := openRootAndLease(t, rootPath)
	defer root.Close()
	defer lease.Close()
	lease.handle.forceFallback = true
	rootDirectory, err := lease.OpenDir(mustRelative(t, pathspec.POSIX, ".", true))
	if err != nil {
		t.Fatalf("fallback OpenDir(.) error = %v", err)
	}
	if rootDirectory.Identity() != root.Identity() {
		t.Fatalf("fallback root identity = %#v, want %#v", rootDirectory.Identity(), root.Identity())
	}
	_ = rootDirectory.Close()
	directory, err := lease.OpenDir(mustRelative(t, pathspec.POSIX, "directory", false))
	if err != nil {
		t.Fatalf("fallback OpenDir(directory) error = %v", err)
	}
	_ = directory.Close()
	file, err := lease.OpenRegular(mustRelative(t, pathspec.POSIX, "file", false))
	if err != nil {
		t.Fatalf("fallback OpenRegular(file) error = %v", err)
	}
	fileIdentity := file.Identity()
	if got := readRootFSFile(t, file); got != "inside" {
		t.Fatalf("fallback file content = %q", got)
	}
	_ = file.Close()
	hardLink, err := lease.OpenRegular(mustRelative(t, pathspec.POSIX, "hard-link", false))
	if err != nil {
		t.Fatalf("fallback OpenRegular(hard-link) error = %v", err)
	}
	if hardLink.Identity() != fileIdentity {
		t.Fatalf("fallback hard-link identity = %#v, want %#v", hardLink.Identity(), fileIdentity)
	}
	_ = hardLink.Close()
	for _, raw := range []string{"file-link", "intermediate-link/nested"} {
		opened, openErr := lease.OpenRegular(mustRelative(t, pathspec.POSIX, raw, false))
		if opened != nil {
			_ = opened.Close()
			t.Fatalf("fallback OpenRegular(%q) followed a symlink", raw)
		}
		if !errors.Is(openErr, ErrSymlink) {
			t.Fatalf("fallback OpenRegular(%q) error = %v, want %v", raw, openErr, ErrSymlink)
		}
	}
	if opened, openErr := lease.OpenDir(mustRelative(t, pathspec.POSIX, "directory-link", false)); !errors.Is(openErr, ErrSymlink) || opened != nil {
		t.Fatalf("fallback OpenDir(directory-link) = directory %v, error %v", opened, openErr)
	}
	if opened, openErr := lease.OpenRegular(mustRelative(t, pathspec.POSIX, "fifo", false)); !errors.Is(openErr, ErrSpecial) || opened != nil {
		t.Fatalf("fallback OpenRegular(fifo) = file %v, error %v", opened, openErr)
	}
}

func TestLinuxForcedFallbackRequiresMountProofAndRejectsMounts(t *testing.T) {
	root, lease := openRootAndLease(t, "/")
	defer root.Close()
	defer lease.Close()
	lease.handle.forceFallback = true
	procRoot, err := OpenRoot(mustRootDirectory(t, "/proc"))
	if err != nil {
		t.Skipf("cannot open /proc for mount evidence: %v", err)
	}
	defer procRoot.Close()
	if procRoot.Identity().Mount != root.Identity().Mount {
		if directory, openErr := lease.OpenDir(mustRelative(t, pathspec.POSIX, "proc", false)); !errors.Is(openErr, ErrMountBoundary) || directory != nil {
			t.Fatalf("fallback OpenDir(proc) = directory %v, error %v", directory, openErr)
		}
	}
	lease.handle.mountProof = false
	if directory, openErr := lease.OpenDir(mustRelative(t, pathspec.POSIX, ".", true)); !errors.Is(openErr, ErrIO) || directory != nil {
		t.Fatalf("fallback without mount proof = directory %v, error %v", directory, openErr)
	}
}

func TestLinuxContainedOpenErrorMapping(t *testing.T) {
	t.Parallel()

	if got := classifyContainedOpenError(unix.EAGAIN, false); !errors.Is(got, ErrSourceChanged) {
		t.Fatalf("EAGAIN mapping = %v, want %v", got, ErrSourceChanged)
	}
	if got := classifyContainedOpenError(unix.ESTALE, false); !errors.Is(got, ErrSourceChanged) {
		t.Fatalf("ESTALE mapping = %v, want %v", got, ErrSourceChanged)
	}
	wantPolicy := unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_XDEV
	if linuxResolvePolicy != wantPolicy {
		t.Fatalf("openat2 resolve policy = %#x, want %#x", linuxResolvePolicy, wantPolicy)
	}
	if linuxDirectoryFlags != unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC {
		t.Fatalf("directory open flags = %#x", linuxDirectoryFlags)
	}
	if linuxRegularFileFlags != unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK {
		t.Fatalf("regular-file open flags = %#x", linuxRegularFileFlags)
	}
	if linuxSearchTargetFlags != linuxRegularFileFlags {
		t.Fatalf("search-target open flags = %#x, regular-file flags = %#x", linuxSearchTargetFlags, linuxRegularFileFlags)
	}
}

func openRootAndLease(t *testing.T, rootPath string) (*Root, *Lease) {
	t.Helper()
	root, err := OpenRoot(mustRootDirectory(t, rootPath))
	if err != nil {
		t.Fatalf("OpenRoot(%q) error = %v", rootPath, err)
	}
	lease, err := root.Duplicate()
	if err != nil {
		_ = root.Close()
		t.Fatalf("Duplicate(%q) error = %v", rootPath, err)
	}
	return root, lease
}

func readRootFSFile(t *testing.T, file *File) string {
	t.Helper()
	var content strings.Builder
	buffer := make([]byte, 16)
	for {
		read, err := file.ReadContext(context.Background(), buffer)
		content.Write(buffer[:read])
		if errors.Is(err, io.EOF) {
			return content.String()
		}
		if err != nil {
			t.Fatalf("ReadContext() error = %v", err)
		}
	}
}

func assertOpenRootError(t *testing.T, path string, want error) {
	t.Helper()
	root, err := OpenRoot(mustRootDirectory(t, path))
	if root != nil {
		_ = root.Close()
		t.Fatalf("OpenRoot(%q) returned a root", path)
	}
	if !errors.Is(err, want) {
		t.Fatalf("OpenRoot(%q) error = %v, want %v", path, err, want)
	}
	if strings.Contains(err.Error(), path) {
		t.Fatalf("OpenRoot error disclosed the path: %q", err)
	}
}

func mustRootDirectory(t *testing.T, path string) pathspec.RootDirectory {
	t.Helper()
	directory, code := pathspec.ParseRootDirectory(pathspec.POSIX, path)
	if code != "" {
		t.Fatalf("ParseRootDirectory(%q) code = %q", path, code)
	}
	return directory
}

func mustRelative(t *testing.T, target pathspec.TargetOS, raw string, allowRoot bool) pathspec.Relative {
	t.Helper()
	path, code := pathspec.ParseRelative(target, raw, allowRoot)
	if code != "" {
		t.Fatalf("ParseRelative(%q) code = %q", raw, code)
	}
	return path
}
