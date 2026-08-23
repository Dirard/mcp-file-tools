//go:build linux

package navigation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLongLinesAreReadableAndSearchable(t *testing.T) {
	line := "needle" + strings.Repeat("x", 10_000)
	fixture := newNavigationFixture(t, map[string]string{"long.txt": line + "\n"})

	search := fixture.search(t, fmt.Sprintf(`{"cwd_id":%d,"query":"needle","mode":"text","path":"long.txt"}`, fixture.cwdID))
	if rows := fixture.collectSearchRows(t, search); len(rows) != 1 || rows[0] != "M\t1\t"+line {
		t.Fatalf("long-line search rows = %d", len(rows))
	}

	read := fixture.read(t, fmt.Sprintf(`{"cwd_id":%d,"files":[{"path":"long.txt","end":1}],"max_bytes":1048576}`, fixture.cwdID))
	if rows := sourceRows(resultText(t, read)); len(rows) != 1 || rows[0] != "1|"+line {
		t.Fatalf("long-line read rows = %d", len(rows))
	}
}

func TestToolsFollowSymlinksOutsideCWDAndTerminateOnCycle(t *testing.T) {
	outside := t.TempDir()
	manifest := "needle through symlink\n"
	if err := os.WriteFile(filepath.Join(outside, "manifest.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture := newNavigationFixture(t, nil)
	if err := os.Symlink(filepath.Join(outside, "manifest.toml"), filepath.Join(fixture.directory, "linked.toml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(fixture.directory, "linked-dir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".", filepath.Join(fixture.directory, "loop")); err != nil {
		t.Fatal(err)
	}

	project := fixture.project(t, fmt.Sprintf(`{"cwd_id":%d,"depth":2}`, fixture.cwdID))
	projectText := resultText(t, project)
	for _, row := range []string{"F\t\"linked.toml\"", "D\t\"linked-dir\"", "F\t\"linked-dir/manifest.toml\""} {
		if !strings.Contains(projectText, row) {
			t.Fatalf("project lacks %q:\n%s", row, projectText)
		}
	}

	search := fixture.search(t, fmt.Sprintf(`{"cwd_id":%d,"query":"needle through","mode":"text","path":"linked.toml"}`, fixture.cwdID))
	if rows := fixture.collectSearchRows(t, search); len(rows) != 1 || rows[0] != "M\t1\tneedle through symlink" {
		t.Fatalf("symlink text-search rows = %q", rows)
	}

	read := fixture.read(t, fmt.Sprintf(`{"cwd_id":%d,"files":[{"path":"linked.toml","end":1}]}`, fixture.cwdID))
	if rows := sourceRows(resultText(t, read)); len(rows) != 1 || rows[0] != "1|"+strings.TrimSuffix(manifest, "\n") {
		t.Fatalf("symlink read rows = %q", rows)
	}

	cycle := fixture.project(t, fmt.Sprintf(`{"cwd_id":%d,"depth":8,"limit":1000}`, fixture.cwdID))
	if cycle.Result.IsError() {
		t.Fatalf("cycle project error:\n%s", resultText(t, cycle))
	}
	rows := pageRows(resultText(t, cycle))
	want := []string{"D\t\".\"", "D\t\"linked-dir\"", "F\t\"linked-dir/manifest.toml\"", "F\t\"linked.toml\"", "D\t\"loop\""}
	if strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Fatalf("cycle rows = %q, want %q", rows, want)
	}
}

func TestToolsFollowSymlinkTargetAcrossMountBoundary(t *testing.T) {
	external, err := os.MkdirTemp("/dev/shm", "mcp-file-tools-")
	if err != nil {
		t.Skipf("/dev/shm unavailable: %v", err)
	}
	defer os.RemoveAll(external)
	manifest := "needle across mount\n"
	if err := os.WriteFile(filepath.Join(external, "manifest.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	fixtureMount, err := mountID(fixtureMountPath(t))
	if err != nil {
		t.Skipf("fixture mount evidence unavailable: %v", err)
	}
	externalMount, err := mountID(external)
	if err != nil || externalMount == fixtureMount {
		t.Skipf("external directory is not on another mount: %v", err)
	}

	fixture := newNavigationFixture(t, nil)
	if err := os.Symlink(filepath.Join(external, "manifest.toml"), filepath.Join(fixture.directory, "linked.toml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(fixture.directory, "linked-dir")); err != nil {
		t.Fatal(err)
	}

	project := fixture.project(t, fmt.Sprintf(`{"cwd_id":%d,"depth":2}`, fixture.cwdID))
	projectText := resultText(t, project)
	for _, row := range []string{"F\t\"linked.toml\"", "D\t\"linked-dir\"", "F\t\"linked-dir/manifest.toml\""} {
		if !strings.Contains(projectText, row) {
			t.Fatalf("project lacks %q:\n%s", row, projectText)
		}
	}
	read := fixture.read(t, fmt.Sprintf(`{"cwd_id":%d,"files":[{"path":"linked.toml","end":1}]}`, fixture.cwdID))
	if rows := sourceRows(resultText(t, read)); len(rows) != 1 || rows[0] != "1|needle across mount" {
		t.Fatalf("cross-mount read rows = %q", rows)
	}
}

func fixtureMountPath(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	t.Cleanup(func() { _ = os.RemoveAll(path) })
	return path
}

func mountID(path string) (uint64, error) {
	var statx unix.Statx_t
	mask := uint32(unix.STATX_MNT_ID)
	if err := unix.Statx(unix.AT_FDCWD, path, unix.AT_STATX_SYNC_AS_STAT, int(mask), &statx); err != nil {
		return 0, err
	}
	if statx.Mask&mask != mask {
		return 0, errors.New("STATX_MNT_ID unavailable")
	}
	return statx.Mnt_id, nil
}
