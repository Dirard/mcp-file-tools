package scanner

import (
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/pathspec"
)

func TestStateClonePreservesCursorAccounting(t *testing.T) {
	root, code := pathspec.ParseRelative(pathspec.POSIX, ".", true)
	if code != "" {
		t.Fatal(code)
	}
	state, code := newState(Request{
		Tool:  api.ToolProject,
		CWDID: 1,
		Mode:  Project,
		Root:  root,
		Depth: 1,
	}, generousLimits())
	if code != "" {
		t.Fatal(code)
	}
	state.pending = make([]Row, 1, 8)
	state.pending[0] = Row{Kind: RowDirectory, Path: "."}

	clone := state.Clone()
	if clone == nil || clone.Digest() != state.Digest() || clone.Footprint() != state.Footprint() {
		t.Fatalf("clone accounting changed: original=%d clone=%d", state.Footprint(), clone.Footprint())
	}
}
