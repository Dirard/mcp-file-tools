//go:build windows

package rootfs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"golang.org/x/sys/windows"
)

func TestWindowsRootEnumerationStreamsAreIndependent(t *testing.T) {
	for _, searchTarget := range []bool{false, true} {
		seam := "OpenDir"
		if searchTarget {
			seam = "OpenSearchTarget"
		}
		t.Run(seam, func(t *testing.T) {
			testWindowsIndependentRootStreams(t, searchTarget)
		})
	}
}

func testWindowsIndependentRootStreams(t *testing.T, searchTarget bool) {
	t.Helper()
	rootPath := t.TempDir()
	want := make([]string, 64)
	for index := range want {
		want[index] = fmt.Sprintf("entry-%02d", index)
		if err := os.WriteFile(filepath.Join(rootPath, want[index]), []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", want[index], err)
		}
	}
	root, firstLease := openWindowsRootAndLease(t, rootPath)
	defer root.Close()
	defer firstLease.Close()
	secondLease, err := root.Duplicate()
	if err != nil {
		t.Fatalf("second Duplicate() error = %v", err)
	}
	defer secondLease.Close()
	openDirectory := func(lease *Lease) (*Dir, error) {
		path := mustWindowsRelative(t, ".", true)
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
	firstNames, firstErr := collectWindowsEntryNames(first)
	secondNames, secondErr := collectWindowsEntryNames(second)
	_ = first.Close()
	_ = second.Close()
	if firstErr != nil || secondErr != nil {
		t.Fatalf("sequential enumeration errors = %v, %v", firstErr, secondErr)
	}
	assertWindowsEntryNames(t, firstNames, want)
	assertWindowsEntryNames(t, secondNames, want)

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
			names, readErr := collectWindowsEntryNames(directory)
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
		assertWindowsEntryNames(t, got.names, want)
	}
}

func TestWindowsRejectsParentMovedOutsideBeforeFinalOpen(t *testing.T) {
	parent := t.TempDir()
	rootPath := filepath.Join(parent, "root")
	outsidePath := filepath.Join(parent, "outside")
	if err := os.MkdirAll(filepath.Join(rootPath, "a", "b"), 0o700); err != nil {
		t.Fatalf("mkdir rooted tree: %v", err)
	}
	if err := os.Mkdir(outsidePath, 0o700); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	root, lease := openWindowsRootAndLease(t, rootPath)
	defer root.Close()
	defer lease.Close()
	path := mustWindowsRelative(t, "a/b/file", false)
	components := path.Components()
	parentHandle, _, throughSymlink, proofs, err := openWindowsParent(lease.handle, components)
	if err != nil {
		t.Fatalf("open parent: %v", err)
	}
	defer windows.CloseHandle(parentHandle)
	defer closeWindowsSymlinkProofs(proofs)
	moved := filepath.Join(outsidePath, "a")
	if err := os.Rename(filepath.Join(rootPath, "a"), moved); err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			t.Log("open descendant handle prevented the parent from moving outside the root")
			return
		}
		t.Fatalf("move parent outside root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moved, "b", "file"), []byte("outside"), 0o600); err != nil {
		t.Fatalf("insert outside file: %v", err)
	}
	file, _, _, err := openWindowsRegularAt(lease.handle, parentHandle, components[len(components)-1], false, throughSymlink)
	if file.valid {
		_ = closePlatformFile(&file)
		t.Fatal("moved parent exposed a regular file")
	}
	if !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("moved-parent final open error = %v, want %v", err, ErrSourceChanged)
	}
}

func TestWindowsHandlesPreserveActualCasingAndShareDelete(t *testing.T) {
	rootPath := t.TempDir()
	actualDirectory := filepath.Join(rootPath, "ActualCase")
	if err := os.Mkdir(actualDirectory, 0o700); err != nil {
		t.Fatalf("mkdir actual directory: %v", err)
	}
	actualFile := filepath.Join(actualDirectory, "MixedCase.TXT")
	if err := os.WriteFile(actualFile, []byte("inside"), 0o600); err != nil {
		t.Fatalf("write actual file: %v", err)
	}

	root, lease := openWindowsRootAndLease(t, rootPath)
	defer root.Close()
	defer lease.Close()
	if root.Identity().Platform != pathspec.Windows || root.CanonicalPath() == "" {
		t.Fatalf("root evidence = canonical %q, identity %#v", root.CanonicalPath(), root.Identity())
	}
	directory, err := lease.OpenDir(mustWindowsRelative(t, "actualcase", false))
	if err != nil {
		t.Fatalf("OpenDir(actualcase) error = %v", err)
	}
	defer directory.Close()
	if got := directory.ResolvedPath().String(); got != "ActualCase" {
		t.Fatalf("directory ResolvedPath() = %q, want %q", got, "ActualCase")
	}
	file, err := lease.OpenRegular(mustWindowsRelative(t, "actualcase/mixedcase.txt", false))
	if err != nil {
		t.Fatalf("OpenRegular(mixed casing) error = %v", err)
	}
	defer file.Close()
	if got := file.ResolvedPath().String(); got != "ActualCase/MixedCase.TXT" {
		t.Fatalf("file ResolvedPath() = %q, want %q", got, "ActualCase/MixedCase.TXT")
	}

	renamedFile := filepath.Join(actualDirectory, "Renamed.TXT")
	if err := os.Rename(actualFile, renamedFile); err != nil {
		t.Fatalf("rename open file (share-delete proof): %v", err)
	}
	if got := readWindowsRootFSFile(t, file); got != "inside" {
		t.Fatalf("open file content after rename = %q, want inside", got)
	}
	renamedDirectory := filepath.Join(rootPath, "RenamedDirectory")
	if err := os.Rename(actualDirectory, renamedDirectory); err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			t.Log("Windows kept the opened directory anchored and prevented its rename")
			return
		}
		t.Fatalf("rename open directory (share-delete proof): %v", err)
	}
	reopened, err := lease.OpenRegular(mustWindowsRelative(t, "renameddirectory/renamed.txt", false))
	if err != nil {
		t.Fatalf("OpenRegular after directory rename error = %v", err)
	}
	defer reopened.Close()
	if got := reopened.ResolvedPath().String(); got != "RenamedDirectory/Renamed.TXT" {
		t.Fatalf("reopened ResolvedPath() = %q, want %q", got, "RenamedDirectory/Renamed.TXT")
	}
}

func TestWindowsOpenSearchTargetTransfersOneActualCaseHandle(t *testing.T) {
	rootPath := t.TempDir()
	actualDirectory := filepath.Join(rootPath, "ActualDirectory")
	if err := os.Mkdir(actualDirectory, 0o700); err != nil {
		t.Fatalf("mkdir actual directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "MixedCase.TXT"), []byte("content"), 0o600); err != nil {
		t.Fatalf("write actual file: %v", err)
	}
	root, lease := openWindowsRootAndLease(t, rootPath)
	defer root.Close()
	defer lease.Close()

	fileTarget, err := lease.OpenSearchTarget(mustWindowsRelative(t, "mixedcase.txt", false))
	if err != nil {
		t.Fatalf("OpenSearchTarget(file) error = %v", err)
	}
	if fileTarget.Kind() != SearchTargetRegular {
		t.Fatalf("file target kind = %v, want %v", fileTarget.Kind(), SearchTargetRegular)
	}
	if directory, takeErr := fileTarget.TakeDir(); directory != nil || !errors.Is(takeErr, ErrWrongTargetKind) {
		t.Fatalf("TakeDir(file target) = directory %v, error %v", directory, takeErr)
	}
	file, err := fileTarget.TakeFile()
	if err != nil {
		t.Fatalf("TakeFile() error = %v", err)
	}
	if got := file.ResolvedPath().String(); got != "MixedCase.TXT" {
		_ = file.Close()
		t.Fatalf("file ResolvedPath() = %q, want MixedCase.TXT", got)
	}
	if err := fileTarget.Close(); err != nil {
		_ = file.Close()
		t.Fatalf("Close() after transfer error = %v", err)
	}
	if got := readWindowsRootFSFile(t, file); got != "content" {
		_ = file.Close()
		t.Fatalf("transferred file content = %q, want content", got)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("transferred file Close() error = %v", err)
	}

	directoryTarget, err := lease.OpenSearchTarget(mustWindowsRelative(t, "actualdirectory", false))
	if err != nil {
		t.Fatalf("OpenSearchTarget(directory) error = %v", err)
	}
	if directoryTarget.Kind() != SearchTargetDirectory {
		t.Fatalf("directory target kind = %v, want %v", directoryTarget.Kind(), SearchTargetDirectory)
	}
	directory, err := directoryTarget.TakeDir()
	if err != nil {
		t.Fatalf("TakeDir() error = %v", err)
	}
	if got := directory.ResolvedPath().String(); got != "ActualDirectory" {
		_ = directory.Close()
		t.Fatalf("directory ResolvedPath() = %q, want ActualDirectory", got)
	}
	if err := directory.ReadEntries(context.Background(), func(uint64) error { return nil }, func(EnumerationOutcome) error { return nil }); err != nil {
		_ = directory.Close()
		t.Fatalf("transferred directory ReadEntries() error = %v", err)
	}
	if err := directory.Close(); err != nil {
		t.Fatalf("transferred directory Close() error = %v", err)
	}

	if windowsSearchAccess != windowsRegularAccess {
		t.Fatalf("search access = %#x, regular access = %#x", windowsSearchAccess, windowsRegularAccess)
	}
	wantSearchOptions := uint32(windows.FILE_OPEN_REPARSE_POINT | windows.FILE_SYNCHRONOUS_IO_NONALERT)
	if windowsSearchOptions != wantSearchOptions {
		t.Fatalf("search options = %#x, want %#x", windowsSearchOptions, wantSearchOptions)
	}
	if windowsSearchOptions&(windows.FILE_DIRECTORY_FILE|windows.FILE_NON_DIRECTORY_FILE) != 0 {
		t.Fatalf("search options force a target kind: %#x", windowsSearchOptions)
	}
	wantDirectoryOptions := uint32(windows.FILE_OPEN_REPARSE_POINT | windows.FILE_SYNCHRONOUS_IO_NONALERT)
	if windowsDirectoryOptions != wantDirectoryOptions {
		t.Fatalf("directory options = %#x, want %#x", windowsDirectoryOptions, wantDirectoryOptions)
	}
	if windowsDirectoryOptions&(windows.FILE_DIRECTORY_FILE|windows.FILE_NON_DIRECTORY_FILE) != 0 {
		t.Fatalf("directory options force a target kind: %#x", windowsDirectoryOptions)
	}
}

func TestWindowsDuplicateSurvivesOriginalCloseAndHandleReuse(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "file"), []byte("inside"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	root, lease := openWindowsRootAndLease(t, rootPath)
	defer lease.Close()
	originalHandle := root.handle.handle
	if lease.handle.handle == originalHandle {
		t.Fatalf("DuplicateHandle reused live original handle %v", originalHandle)
	}
	if err := root.Close(); err != nil {
		t.Fatalf("root Close() error = %v", err)
	}

	reused := windows.InvalidHandle
	for attempt := 0; attempt < 256; attempt++ {
		handle, err := openWindowsNull()
		if err != nil {
			t.Fatalf("open NUL reuse probe: %v", err)
		}
		if handle == originalHandle {
			reused = handle
			break
		}
		_ = windows.CloseHandle(handle)
	}
	if reused == windows.InvalidHandle {
		t.Log("Windows did not recycle the closed root handle during this run")
	} else {
		defer windows.CloseHandle(reused)
	}
	file, err := lease.OpenRegular(mustWindowsRelative(t, "file", false))
	if err != nil {
		t.Fatalf("lease OpenRegular after original close/reuse error = %v", err)
	}
	defer file.Close()
	if got := readWindowsRootFSFile(t, file); got != "inside" {
		t.Fatalf("duplicate content = %q, want inside", got)
	}
}

func TestWindowsFollowsSymlinkAndRejectsJunctionBoundaries(t *testing.T) {
	rootPath := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "file"), []byte("outside"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	root, lease := openWindowsRootAndLease(t, rootPath)
	defer root.Close()
	defer lease.Close()

	t.Run("file symlink", func(t *testing.T) {
		link := filepath.Join(rootPath, "file-link")
		if err := os.Symlink(filepath.Join(outside, "file"), link); err != nil {
			t.Skipf("file symlink unavailable: %v", err)
		}
		file, err := lease.OpenRegular(mustWindowsRelative(t, "file-link", false))
		if err != nil {
			t.Fatalf("OpenRegular(file-link) = %v", err)
		}
		defer file.Close()
		if got := readWindowsRootFSFile(t, file); got != "outside" {
			t.Fatalf("file symlink content = %q", got)
		}
	})

	t.Run("directory symlink", func(t *testing.T) {
		link := filepath.Join(rootPath, "directory-link")
		if err := os.Symlink(outside, link); err != nil {
			t.Skipf("directory symlink unavailable: %v", err)
		}
		file, err := lease.OpenRegular(mustWindowsRelative(t, "directory-link/file", false))
		if err != nil {
			t.Fatalf("OpenRegular(directory-link/file) = %v", err)
		}
		defer file.Close()
		if got := readWindowsRootFSFile(t, file); got != "outside" {
			t.Fatalf("directory symlink content = %q", got)
		}
	})

	t.Run("junction", func(t *testing.T) {
		junction := filepath.Join(rootPath, "junction")
		if err := createWindowsJunction(junction, outside); err != nil {
			t.Skipf("junction unavailable: %v", err)
		}
		if directory, err := lease.OpenDir(mustWindowsRelative(t, "junction", false)); !errors.Is(err, ErrMountBoundary) || directory != nil {
			t.Fatalf("OpenDir(junction) = directory %v, error %v", directory, err)
		}
		if file, err := lease.OpenRegular(mustWindowsRelative(t, "junction/file", false)); !errors.Is(err, ErrMountBoundary) || file != nil {
			t.Fatalf("OpenRegular(junction/file) = file %v, error %v", file, err)
		}
	})
}

func TestWindowsComponentReplacementNeverEscapes(t *testing.T) {
	rootPath := t.TempDir()
	slot := filepath.Join(rootPath, "slot")
	if err := os.Mkdir(slot, 0o700); err != nil {
		t.Fatalf("mkdir slot: %v", err)
	}
	if err := os.WriteFile(filepath.Join(slot, "file"), []byte("inside"), 0o600); err != nil {
		t.Fatalf("write inside file: %v", err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "file"), []byte("outside"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	probe := filepath.Join(rootPath, "symlink-probe")
	if err := os.Symlink(outside, probe); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}
	if err := os.Remove(probe); err != nil {
		t.Fatalf("remove symlink probe: %v", err)
	}

	root, lease := openWindowsRootAndLease(t, rootPath)
	defer root.Close()
	defer lease.Close()
	parked := filepath.Join(rootPath, "parked")
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
			if err := os.Rename(slot, parked); err != nil {
				if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
					done <- err
					return
				}
				done <- err
				return
			}
			if err := os.Symlink(outside, slot); err != nil {
				_ = os.Rename(parked, slot)
				done <- err
				return
			}
			runtime.Gosched()
			if err := os.Remove(slot); err != nil {
				done <- err
				return
			}
			if err := os.Rename(parked, slot); err != nil {
				done <- err
				return
			}
			runtime.Gosched()
		}
	}()

	path := mustWindowsRelative(t, "slot/file", false)
	successes := 0
	rejections := 0
	for attempt := 0; attempt < 500; attempt++ {
		file, err := lease.OpenRegular(path)
		if err != nil {
			if !errors.Is(err, ErrSymlink) && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrSourceChanged) {
				close(stop)
				<-done
				t.Fatalf("OpenRegular during replacement error = %v", err)
			}
			rejections++
			continue
		}
		successes++
		content := readWindowsRootFSFile(t, file)
		if err := file.Close(); err != nil {
			close(stop)
			<-done
			t.Fatalf("file Close() error = %v", err)
		}
		if content != "inside" {
			close(stop)
			<-done
			t.Fatalf("component replacement escaped root: %q", content)
		}
	}
	close(stop)
	if err := <-done; err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) && successes > 0 {
			t.Log("Windows kept an opened descendant anchored and prevented component replacement")
			return
		}
		t.Fatalf("replacement loop error = %v", err)
	}
	if successes == 0 || rejections == 0 {
		t.Fatalf("replacement coverage = %d successes, %d rejections; want both", successes, rejections)
	}
}

func TestWindowsReadEntriesUsesActualUTF16CasingAndRawRecordCharges(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "MixedCase.TXT"), []byte("file"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.Mkdir(filepath.Join(rootPath, "NestedDirectory"), 0o700); err != nil {
		t.Fatalf("mkdir nested directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "NestedDirectory", "descendant"), []byte("nested"), 0o600); err != nil {
		t.Fatalf("write descendant: %v", err)
	}
	root, lease := openWindowsRootAndLease(t, rootPath)
	defer root.Close()
	defer lease.Close()
	directory, err := lease.OpenDir(mustWindowsRelative(t, ".", true))
	if err != nil {
		t.Fatalf("OpenDir(.) error = %v", err)
	}
	charges := 0
	chargedBytes := uint64(0)
	entries := make(map[string]Entry)
	err = directory.ReadEntries(context.Background(), func(rawBytes uint64) error {
		if rawBytes == 0 {
			t.Fatal("zero-byte Windows directory record charge")
		}
		charges++
		chargedBytes += rawBytes
		return nil
	}, func(outcome EnumerationOutcome) error {
		entry, ok := outcome.Candidate()
		if !ok {
			t.Fatalf("unexpected Windows disposition %v", outcome.Disposition())
		}
		if !entry.IdentityKnown || entry.Identity.Platform != pathspec.Windows {
			t.Fatalf("entry %q identity = %#v, known=%v", entry.Path.String(), entry.Identity, entry.IdentityKnown)
		}
		entries[entry.Path.String()] = entry
		return nil
	})
	if err != nil {
		t.Fatalf("ReadEntries() error = %v", err)
	}
	if charges < len(entries) || chargedBytes == 0 {
		t.Fatalf("charges = %d records/%d bytes for %d outcomes", charges, chargedBytes, len(entries))
	}
	if entry := entries["MixedCase.TXT"]; entry.Kind != EntryFile {
		t.Fatalf("MixedCase.TXT = %#v", entry)
	}
	if entry := entries["NestedDirectory"]; entry.Kind != EntryDir {
		t.Fatalf("NestedDirectory = %#v", entry)
	}
	if _, found := entries["NestedDirectory/descendant"]; found {
		t.Fatal("ReadEntries enumerated a directory descendant")
	}
	if err := directory.Close(); err != nil {
		t.Fatalf("directory Close() error = %v", err)
	}
	if err := directory.ReadEntries(context.Background(), func(uint64) error { return nil }, func(EnumerationOutcome) error { return nil }); !errors.Is(err, ErrClosed) {
		t.Fatalf("ReadEntries after close error = %v, want %v", err, ErrClosed)
	}
}

func collectWindowsEntryNames(directory *Dir) ([]string, error) {
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

func assertWindowsEntryNames(t *testing.T, got, want []string) {
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

func openWindowsRootAndLease(t *testing.T, directory string) (*Root, *Lease) {
	t.Helper()
	root, err := OpenRoot(mustWindowsRootDirectory(t, directory))
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

func mustWindowsRootDirectory(t *testing.T, raw string) pathspec.RootDirectory {
	t.Helper()
	directory, code := pathspec.ParseRootDirectory(pathspec.Windows, filepath.ToSlash(raw))
	if code != "" {
		t.Fatalf("ParseRootDirectory(%q) code = %q", raw, code)
	}
	return directory
}

func mustWindowsRelative(t *testing.T, raw string, allowRoot bool) pathspec.Relative {
	t.Helper()
	path, code := pathspec.ParseRelative(pathspec.Windows, raw, allowRoot)
	if code != "" {
		t.Fatalf("ParseRelative(%q) code = %q", raw, code)
	}
	return path
}

func readWindowsRootFSFile(t *testing.T, file *File) string {
	t.Helper()
	buffer := make([]byte, 4096)
	read, err := file.ReadContext(context.Background(), buffer)
	if err != nil {
		t.Fatalf("ReadContext() error = %v", err)
	}
	return string(buffer[:read])
}

func openWindowsNull() (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString("NUL")
	if err != nil {
		return windows.InvalidHandle, err
	}
	return windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, 0, 0)
}

func createWindowsJunction(link, target string) error {
	command := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", link, target)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("mklink /J failed: %w (%s)", err, output)
	}
	return nil
}
