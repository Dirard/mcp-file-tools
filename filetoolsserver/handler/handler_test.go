package handler

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dimitar-grigorov/mcp-file-tools/internal/config"
)

func TestNewHandler(t *testing.T) {
	h := NewHandler()

	if h == nil {
		t.Fatal("expected handler, got nil")
	}
	if h.config == nil {
		t.Fatal("expected default config")
	}
}

func TestWithConfig(t *testing.T) {
	cfg := &config.Config{
		MemoryThreshold: 1024,
	}

	h := NewHandler(WithConfig(cfg))

	if h.config != cfg {
		t.Error("expected config to be set via WithConfig option")
	}
}

func TestWithConfig_Nil(t *testing.T) {
	h := NewHandler(WithConfig(nil))

	if h.config == nil {
		t.Error("config should not be nil when WithConfig(nil) is passed")
	}
}

func TestMapInputPathIgnoresCrossOSPathMap(t *testing.T) {
	h := NewHandler(WithConfig(&config.Config{
		PathMaps: []config.PathMap{
			{Source: `D:\`, Target: "/mnt/d"},
		},
	}))

	got := h.mapInputPath(`D:\Ai-Apps\SomeRepo\Main.go`)
	want := `D:\Ai-Apps\SomeRepo\Main.go`
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestMapInputPathRewritesSameOSAbsolutePath(t *testing.T) {
	tempDir := t.TempDir()
	sourceRoot := filepath.Join(tempDir, "source")
	targetRoot := filepath.Join(tempDir, "target")
	h := NewHandler(WithConfig(&config.Config{
		PathMaps: []config.PathMap{
			{Source: sourceRoot, Target: targetRoot},
		},
	}))

	got := h.mapInputPath(filepath.Join(sourceRoot, "dir", "file.go"))
	want := filepath.ToSlash(filepath.Join(targetRoot, "dir", "file.go"))
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestMapInputPathIgnoresRelativePathMapTarget(t *testing.T) {
	tempDir := t.TempDir()
	sourceRoot := filepath.Join(tempDir, "source")
	h := NewHandler(WithConfig(&config.Config{
		PathMaps: []config.PathMap{
			{Source: sourceRoot, Target: "relative-target"},
		},
	}))

	input := filepath.Join(sourceRoot, "file.go")
	got := h.mapInputPath(input)
	if got != input {
		t.Fatalf("relative path-map target should be ignored; got %q, want %q", got, input)
	}
}

func TestNormalizeInputPathRejectsRelativePath(t *testing.T) {
	h := NewHandler(WithConfig(&config.Config{}))

	if got, err := h.normalizeInputPath("relative/file.go"); err == nil {
		t.Fatalf("expected relative path to be rejected, got %q", got)
	}
}

func TestDisplayPathRewritesSameOSAbsoluteAlias(t *testing.T) {
	tempDir := t.TempDir()
	sourceRoot := filepath.Join(tempDir, "source")
	targetRoot := filepath.Join(tempDir, "target")
	h := NewHandler(WithConfig(&config.Config{
		PathMaps: []config.PathMap{
			{Source: sourceRoot, Target: targetRoot},
		},
	}))

	got := h.displayPath(filepath.Join(targetRoot, "dir", "file.go"))
	want := filepath.ToSlash(filepath.Join(sourceRoot, "dir", "file.go"))
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestDisplayPathPreservesWindowsDriveRootPathMap(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows drive-root display is Windows-specific")
	}
	targetRoot := t.TempDir()
	h := NewHandler(WithConfig(&config.Config{
		PathMaps: []config.PathMap{
			{Source: `D:\`, Target: targetRoot},
		},
	}))

	if got := h.displayPath(targetRoot); got != `D:/` {
		t.Fatalf("display path-map root must stay absolute, got %q", got)
	}
	if got := h.displayPath(filepath.Join(targetRoot, "child.txt")); got != `D:/child.txt` {
		t.Fatalf("display path-map child must not double-separate root, got %q", got)
	}
}

func TestDisplayPathPreservesPOSIXRootPathMap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX root path-map display is POSIX-specific")
	}
	targetRoot := t.TempDir()
	h := NewHandler(WithConfig(&config.Config{
		PathMaps: []config.PathMap{
			{Source: "/", Target: targetRoot},
		},
	}))

	if got := h.displayPath(targetRoot); got != "/" {
		t.Fatalf("display path-map root must stay absolute, got %q", got)
	}
	if got := h.displayPath(filepath.Join(targetRoot, "child.txt")); got != "/child.txt" {
		t.Fatalf("display path-map child must not double-separate root, got %q", got)
	}
	if got := h.mapInputPath("/child.txt"); got != filepath.ToSlash(filepath.Join(targetRoot, "child.txt")) {
		t.Fatalf("POSIX root path-map output must round-trip as input, got %q", got)
	}
}

func TestPathMapMatchesPOSIXRootDescendants(t *testing.T) {
	if !pathMapMatches("/child.txt", "/", false) {
		t.Fatal("POSIX root path-map should match absolute descendants")
	}
	if !pathMapMatches("/", "/", false) {
		t.Fatal("POSIX root path-map should match root itself")
	}
	if pathMapMatches("relative.txt", "/", false) {
		t.Fatal("POSIX root path-map must not match relative paths")
	}
}

func TestMapInputPathPreservesPOSIXRootTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX root path-map target is POSIX-specific")
	}
	h := NewHandler(WithConfig(&config.Config{
		PathMaps: []config.PathMap{
			{Source: "/repo", Target: "/"},
		},
	}))

	if got := h.mapInputPath("/repo"); got != "/" {
		t.Fatalf("exact POSIX root target must stay absolute, got %q", got)
	}
	if got := h.mapInputPath("/repo/child.txt"); got != "/child.txt" {
		t.Fatalf("POSIX root target descendants must stay absolute, got %q", got)
	}
}

func TestMapInputPathPreservesWindowsDriveRootTarget(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows drive-root path-map target is Windows-specific")
	}
	h := NewHandler(WithConfig(&config.Config{
		PathMaps: []config.PathMap{
			{Source: `Z:\`, Target: `D:\`},
		},
	}))

	if got := h.mapInputPath(`Z:\`); got != "D:/" {
		t.Fatalf("exact Windows drive-root target must stay absolute, got %q", got)
	}
	if normalized, err := h.normalizeInputPath(`Z:\`); err != nil || normalized != `D:\` {
		t.Fatalf("exact Windows drive-root target must normalize, got %q err=%v", normalized, err)
	}
	if normalized, err := h.normalizeInputPath(`Z:\child.txt`); err != nil || normalized != `D:\child.txt` {
		t.Fatalf("Windows drive-root target descendants must normalize, got %q err=%v", normalized, err)
	}
}

func TestPathMapsKeepPOSIXCaseSensitive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows path matching is intentionally case-insensitive")
	}
	h := NewHandler(WithConfig(&config.Config{
		PathMaps: []config.PathMap{
			{Source: "/Repo", Target: "/runtime/Repo"},
		},
	}))

	if got := h.mapInputPath("/repo/file.go"); got != "/repo/file.go" {
		t.Fatalf("POSIX source map should be case-sensitive, got %q", got)
	}
	if got := h.mapInputPath("/Repo/file.go"); got != "/runtime/Repo/file.go" {
		t.Fatalf("POSIX source map did not rewrite exact-case path: %q", got)
	}
	if got := h.displayPath("/runtime/repo/file.go"); got != "/runtime/repo/file.go" {
		t.Fatalf("POSIX target map should be case-sensitive, got %q", got)
	}
	if got := h.displayPath("/runtime/Repo/file.go"); got != "/Repo/file.go" {
		t.Fatalf("POSIX target map did not rewrite exact-case path: %q", got)
	}
}

func TestDisplayResolvedPathUsesResolvedPathWhenUnmapped(t *testing.T) {
	h := NewHandler(WithConfig(&config.Config{}))

	resolved := filepath.Join("absolute", "relative", "file.go")
	got := h.displayResolvedPath("relative/file.go", resolved)
	if got != filepath.ToSlash(resolved) {
		t.Fatalf("expected slash-normalized resolved path to be used, got %q", got)
	}
}

func TestNormalizeInputPath_PreservesLiteralDollarWithoutPathMaps(t *testing.T) {
	t.Setenv("data", "expanded")
	h := NewHandler(WithConfig(&config.Config{}))
	path := filepath.Join(t.TempDir(), "$data.txt")

	got, err := h.normalizeInputPath(path)
	if err != nil {
		t.Fatalf("normalizeInputPath returned error: %v", err)
	}
	want := filepath.Clean(path)
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
