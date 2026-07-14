package scanner

import (
	"container/heap"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"github.com/Dirard/mcp-file-tools/internal/rootfs"
)

func TestFrontierUsesCanonicalPathOrder(t *testing.T) {
	t.Parallel()

	var queue frontier
	for _, item := range []struct {
		path string
		kind rootfs.EntryKind
	}{
		{path: "c", kind: rootfs.EntryFile},
		{path: "a/z", kind: rootfs.EntryFile},
		{path: "b", kind: rootfs.EntryDir},
		{path: "a", kind: rootfs.EntryDir},
	} {
		path, code := pathspec.ParseRelative(pathspec.POSIX, item.path, false)
		if code != "" {
			t.Fatalf("ParseRelative(%q) = %q", item.path, code)
		}
		heap.Push(&queue, scanUnit{path: path, kind: item.kind})
	}

	want := []string{"a", "a/z", "b", "c"}
	for index, path := range want {
		unit := heap.Pop(&queue).(scanUnit)
		if unit.path.String() != path {
			t.Fatalf("pop %d = %q, want %q", index, unit.path.String(), path)
		}
	}
}
