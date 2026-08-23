//go:build linux

package rootfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"golang.org/x/sys/unix"
)

func TestLinuxEnumerationFollowsExternalSymlinks(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "file"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	rootPath := t.TempDir()
	if err := os.Symlink(filepath.Join(outside, "file"), filepath.Join(rootPath, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(rootPath, "dir-link")); err != nil {
		t.Fatal(err)
	}

	root, lease := openRootAndLease(t, rootPath)
	defer root.Close()
	defer lease.Close()
	directory, err := lease.OpenDir(mustRelative(t, pathspec.POSIX, ".", true))
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	err = directory.ReadEntries(context.Background(), func(uint64) error { return nil }, func(outcome EnumerationOutcome) error {
		if entry, ok := outcome.Candidate(); ok && (entry.Path.String() == "link" || entry.Path.String() == "dir-link") {
			want := EntryFile
			if entry.Path.String() == "dir-link" {
				want = EntryDir
			}
			if entry.Kind != want {
				t.Fatalf("external symlink entry = %#v, root = %#v", entry, root.Identity())
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	file, openErr := lease.OpenRegular(mustRelative(t, pathspec.POSIX, "link", false))
	if openErr != nil {
		t.Fatalf("OpenRegular(link) = %v", openErr)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	external, dirErr := lease.OpenDir(mustRelative(t, pathspec.POSIX, "dir-link", false))
	if dirErr != nil {
		t.Fatalf("OpenDir(dir-link) = %v", dirErr)
	}
	if err := external.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxFallbackFollowsSymlinkAcrossMountBoundary(t *testing.T) {
	external, err := os.MkdirTemp("/dev/shm", "mcp-file-tools-")
	if err != nil {
		t.Skipf("/dev/shm unavailable: %v", err)
	}
	defer os.RemoveAll(external)
	externalFile := filepath.Join(external, "file")
	if err := os.WriteFile(externalFile, []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	rootPath := t.TempDir()
	rootMount, rootErr := mountIDAt(rootPath)
	externalMount, externalErr := mountIDAt(external)
	if rootErr != nil || externalErr != nil || rootMount == externalMount {
		t.Skipf("distinct mounts unavailable: %v %v", rootErr, externalErr)
	}
	if err := os.Symlink(externalFile, filepath.Join(rootPath, "file-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(rootPath, "dir-link")); err != nil {
		t.Fatal(err)
	}

	root, lease := openRootAndLease(t, rootPath)
	defer root.Close()
	defer lease.Close()
	lease.handle.forceFallback = true
	file, fileErr := lease.OpenRegular(mustRelative(t, pathspec.POSIX, "file-link", false))
	if fileErr != nil {
		t.Fatalf("fallback OpenRegular(file-link) = %v", fileErr)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	directory, dirErr := lease.OpenDir(mustRelative(t, pathspec.POSIX, "dir-link", false))
	if dirErr != nil {
		t.Fatalf("fallback OpenDir(dir-link) = %v", dirErr)
	}
	if err := directory.Close(); err != nil {
		t.Fatal(err)
	}
}

func mountIDAt(path string) (uint64, error) {
	var statx unix.Statx_t
	mask := uint32(unix.STATX_MNT_ID)
	if err := unix.Statx(unix.AT_FDCWD, path, unix.AT_STATX_SYNC_AS_STAT, int(mask), &statx); err != nil {
		return 0, err
	}
	if statx.Mask&mask != mask {
		return 0, errors.New("rootfs: STATX_MNT_ID unavailable")
	}
	return statx.Mnt_id, nil
}
