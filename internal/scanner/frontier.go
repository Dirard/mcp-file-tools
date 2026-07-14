package scanner

import (
	"strings"
	"unsafe"

	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"github.com/Dirard/mcp-file-tools/internal/rootfs"
)

type scanUnit struct {
	path          pathspec.Relative
	kind          rootfs.EntryKind
	identity      rootfs.Identity
	identityKnown bool
	depth         uint16
}

type frontier []scanUnit

func (queue frontier) Len() int { return len(queue) }

func (queue frontier) Less(left, right int) bool {
	return compareScanUnits(queue[left], queue[right]) < 0
}

func (queue frontier) Swap(left, right int) {
	queue[left], queue[right] = queue[right], queue[left]
}

func (queue *frontier) Push(value any) {
	*queue = append(*queue, value.(scanUnit))
}

func (queue *frontier) Pop() any {
	old := *queue
	last := len(old) - 1
	value := old[last]
	old[last] = scanUnit{}
	*queue = old[:last]
	return value
}

func compareScanUnits(left, right scanUnit) int {
	if compared := strings.Compare(left.path.String(), right.path.String()); compared != 0 {
		return compared
	}
	if left.kind == right.kind {
		return 0
	}
	if left.kind == rootfs.EntryDir {
		return -1
	}
	if right.kind == rootfs.EntryDir {
		return 1
	}
	if left.kind < right.kind {
		return -1
	}
	return 1
}

func (queue frontier) retainedBytes() uint64 {
	bytes := uint64(cap(queue)) * uint64(unsafe.Sizeof(scanUnit{}))
	for _, unit := range queue {
		bytes += relativeRetainedBytes(unit.path)
	}
	return bytes
}

func unitRetainedBytes(unit scanUnit) uint64 {
	return uint64(unsafe.Sizeof(scanUnit{})) + relativeRetainedBytes(unit.path)
}

func relativeRetainedBytes(path pathspec.Relative) uint64 {
	return path.RetainedBytes()
}
