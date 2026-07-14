package scanner

import (
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/rootfs"
)

func TestIgnorePolicyKeepsInfrastructureClosed(t *testing.T) {
	t.Parallel()

	policy, ok := newIgnorePolicy([]string{"generated"})
	if !ok {
		t.Fatal("newIgnorePolicy rejected a valid operator basename")
	}
	for _, name := range []string{".git", ".hg", ".svn"} {
		for _, kind := range []rootfs.EntryKind{
			rootfs.EntryFile,
			rootfs.EntryDir,
			rootfs.EntrySymlink,
			rootfs.EntrySpecial,
			rootfs.EntryBoundary,
		} {
			if !policy.skip(name, kind, true) {
				t.Fatalf("infrastructure %q kind %d was admitted with include_ignored", name, kind)
			}
		}
	}
	if policy.skip(".codegraph", rootfs.EntryDir, false) {
		t.Fatal(".codegraph must remain ordinary project content")
	}
}

func TestIgnorePolicyOnlySkipsOrdinaryDirectoriesWhenEnabled(t *testing.T) {
	t.Parallel()

	policy, ok := newIgnorePolicy([]string{"generated"})
	if !ok {
		t.Fatal("newIgnorePolicy rejected a valid operator basename")
	}
	for _, name := range []string{
		"node_modules", "vendor", ".venv", "venv", "target", "dist", "build", "out",
		".cache", "__pycache__", "coverage", ".coverage", ".next", ".nuxt", "generated",
	} {
		if !policy.skip(name, rootfs.EntryDir, false) {
			t.Fatalf("ordinary ignored directory %q was admitted", name)
		}
		if policy.skip(name, rootfs.EntryDir, true) {
			t.Fatalf("ordinary ignored directory %q remained excluded with include_ignored", name)
		}
		if policy.skip(name, rootfs.EntryFile, false) {
			t.Fatalf("ordinary ignore basename %q incorrectly excluded a regular file", name)
		}
	}
	if policy.skip("Node_Modules", rootfs.EntryDir, false) {
		t.Fatal("ignore basenames must be bytewise case-sensitive")
	}
}
