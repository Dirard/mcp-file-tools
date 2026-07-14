package pathspec

import (
	"strings"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

func TestParseRelativePOSIXPreservesAcceptedPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		raw        string
		allowRoot  bool
		want       string
		components []string
	}{
		{name: "root", raw: ".", allowRoot: true, want: "."},
		{name: "single", raw: "file", want: "file", components: []string{"file"}},
		{name: "nested", raw: "dir/file", want: "dir/file", components: []string{"dir", "file"}},
		{name: "literal backslash and colon", raw: `a\b:c`, want: `a\b:c`, components: []string{`a\b:c`}},
		{name: "spaces", raw: " leading /trailing ", want: " leading /trailing ", components: []string{" leading ", "trailing "}},
		{name: "control", raw: "a/\x01b", want: "a/\x01b", components: []string{"a", "\x01b"}},
		{name: "combining unicode", raw: "cafe\u0301/Привет", want: "cafe\u0301/Привет", components: []string{"cafe\u0301", "Привет"}},
		{name: "maximum bytes", raw: strings.Repeat("a", 4096), want: strings.Repeat("a", 4096), components: []string{strings.Repeat("a", 4096)}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, code := ParseRelative(POSIX, test.raw, test.allowRoot)
			if code != "" {
				t.Fatalf("ParseRelative(%q) code = %q, want success", test.raw, code)
			}
			if got.Target() != POSIX {
				t.Fatalf("Target() = %d, want POSIX", got.Target())
			}
			if got.String() != test.want {
				t.Fatalf("String() = %q, want %q", got.String(), test.want)
			}
			if got.ByteLen() != len(test.want) {
				t.Fatalf("ByteLen() = %d, want %d", got.ByteLen(), len(test.want))
			}
			assertComponents(t, got.Components(), test.components)
		})
	}
}

func TestParseRelativePOSIXRejectsWithoutRepair(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		raw       string
		allowRoot bool
		want      api.ErrorCode
	}{
		{name: "empty", raw: "", want: api.ErrorInvalidInput},
		{name: "root disallowed", raw: ".", want: api.ErrorInvalidInput},
		{name: "absolute root", raw: "/", want: api.ErrorPathOutsideCWD},
		{name: "absolute", raw: "/a", want: api.ErrorPathOutsideCWD},
		{name: "absolute repeated separator", raw: "//a", want: api.ErrorPathOutsideCWD},
		{name: "trailing separator", raw: "a/", want: api.ErrorInvalidInput},
		{name: "repeated separator", raw: "a//b", want: api.ErrorInvalidInput},
		{name: "dot component", raw: "a/./b", want: api.ErrorInvalidInput},
		{name: "parent", raw: "..", want: api.ErrorPathOutsideCWD},
		{name: "parent component", raw: "a/../b", want: api.ErrorPathOutsideCWD},
		{name: "nul", raw: "a\x00b", want: api.ErrorInvalidInput},
		{name: "invalid utf8", raw: string([]byte{'a', 0xff}), want: api.ErrorInvalidInput},
		{name: "over maximum", raw: strings.Repeat("a", 4097), want: api.ErrorInvalidInput},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, code := ParseRelative(POSIX, test.raw, test.allowRoot)
			if code != test.want {
				t.Fatalf("ParseRelative(%q) code = %q, want %q", test.raw, code, test.want)
			}
			if got.Target() != 0 || got.String() != "" || len(got.Components()) != 0 {
				t.Fatalf("ParseRelative(%q) returned partial path", test.raw)
			}
		})
	}
}

func TestParseRootDirectoryPOSIX(t *testing.T) {
	t.Parallel()

	accepted := []struct {
		raw        string
		want       string
		components []string
	}{
		{raw: "/"},
		{raw: "/workspace", components: []string{"workspace"}},
		{raw: "/workspace/", want: "/workspace", components: []string{"workspace"}},
		{raw: "//workspace///./src/", want: "/workspace/src", components: []string{"workspace", "src"}},
		{raw: `/ leading /a\b:c/Привет`, components: []string{" leading ", `a\b:c`, "Привет"}},
		{raw: "/" + strings.Repeat("a", 4095), components: []string{strings.Repeat("a", 4095)}},
	}
	for _, test := range accepted {
		want := test.want
		if want == "" {
			want = test.raw
		}
		root, code := ParseRootDirectory(POSIX, test.raw)
		if code != "" {
			t.Fatalf("ParseRootDirectory(%q) code = %q, want success", test.raw, code)
		}
		if root.Target() != POSIX || root.String() != want || root.ByteLen() != len(want) {
			t.Fatalf("ParseRootDirectory(%q) = target %d, string %q, bytes %d", test.raw, root.Target(), root.String(), root.ByteLen())
		}
		assertComponents(t, root.Components(), test.components)
	}

	rejected := []string{
		"", ".", "relative", "/a/../b", "/a\x00b",
		string(append([]byte{'/'}, 0xff)),
		"/" + strings.Repeat("a", 4096),
	}
	for _, raw := range rejected {
		root, code := ParseRootDirectory(POSIX, raw)
		if code != api.ErrorInvalidInput {
			t.Fatalf("ParseRootDirectory(%q) code = %q, want %q", raw, code, api.ErrorInvalidInput)
		}
		if root.Target() != 0 || root.String() != "" || len(root.Components()) != 0 {
			t.Fatalf("ParseRootDirectory(%q) returned partial root", raw)
		}
	}
}

func TestParseRelativeWindowsPreservesAcceptedPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		raw        string
		allowRoot  bool
		want       string
		components []string
	}{
		{name: "root", raw: ".", allowRoot: true, want: "."},
		{name: "single", raw: "File.txt", want: "File.txt", components: []string{"File.txt"}},
		{name: "backslash", raw: `Dir\File.txt`, want: "Dir/File.txt", components: []string{"Dir", "File.txt"}},
		{name: "mixed separators", raw: `Dir\Sub/File.txt`, want: "Dir/Sub/File.txt", components: []string{"Dir", "Sub", "File.txt"}},
		{name: "leading space", raw: ` leading/name`, want: " leading/name", components: []string{" leading", "name"}},
		{name: "dotfile", raw: `.git/config`, want: ".git/config", components: []string{".git", "config"}},
		{name: "nonreserved devices", raw: `COM0/COM10/LPT0/LPT10/COM⁴`, want: "COM0/COM10/LPT0/LPT10/COM⁴", components: []string{"COM0", "COM10", "LPT0", "LPT10", "COM⁴"}},
		{name: "unicode casing", raw: `Каталог/Файл`, want: "Каталог/Файл", components: []string{"Каталог", "Файл"}},
		{name: "maximum bytes", raw: strings.Repeat("a", 4096), want: strings.Repeat("a", 4096), components: []string{strings.Repeat("a", 4096)}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, code := ParseRelative(Windows, test.raw, test.allowRoot)
			if code != "" {
				t.Fatalf("ParseRelative(%q) code = %q, want success", test.raw, code)
			}
			if got.Target() != Windows || got.String() != test.want || got.ByteLen() != len(test.want) {
				t.Fatalf("ParseRelative(%q) = target %d, string %q, bytes %d", test.raw, got.Target(), got.String(), got.ByteLen())
			}
			assertComponents(t, got.Components(), test.components)
		})
	}
}

func TestParseRelativeWindowsRejectsRootedAndTraversalPaths(t *testing.T) {
	t.Parallel()

	paths := []string{
		`\rooted`, `/rooted`, `C:\absolute`, `c:/absolute`, `C:drive-relative`,
		`\\server\share`, `\\.\pipe\name`, `\\?\C:\path`, `\??\C:\path`, `\\?\Volume{01234567-89ab-cdef-0123-456789abcdef}\path`,
		`..`, `a\..\b`, `a/../b`,
	}
	for _, raw := range paths {
		got, code := ParseRelative(Windows, raw, false)
		if code != api.ErrorPathOutsideCWD {
			t.Fatalf("ParseRelative(%q) code = %q, want %q", raw, code, api.ErrorPathOutsideCWD)
		}
		if got.Target() != 0 || got.String() != "" || len(got.Components()) != 0 {
			t.Fatalf("ParseRelative(%q) returned partial path", raw)
		}
	}
}

func TestParseRelativeWindowsRejectsLexicalDefects(t *testing.T) {
	t.Parallel()

	paths := []string{
		"", ".", `a\`, `a/`, `a\\b`, `a//b`, `a\/b`, `a\.\b`, `a/./b`,
		"a\x00b", "a\x01b", "a\x1fb", `a<b`, `a>b`, `name:stream`, `a"b`, `a|b`, `a?b`, `a*b`,
		`trailing.`, `trailing `, string([]byte{'a', 0xff}), strings.Repeat("a", 4097),
	}
	for _, raw := range paths {
		got, code := ParseRelative(Windows, raw, false)
		if code != api.ErrorInvalidInput {
			t.Fatalf("ParseRelative(%q) code = %q, want %q", raw, code, api.ErrorInvalidInput)
		}
		if got.Target() != 0 || got.String() != "" || len(got.Components()) != 0 {
			t.Fatalf("ParseRelative(%q) returned partial path", raw)
		}
	}
}

func TestParseRelativeWindowsRejectsReservedAliases(t *testing.T) {
	t.Parallel()

	reserved := []string{
		"CON", "con.txt", "PRN", "aux.log", "NUL", "clock$", "ConIn$", "CONOUT$.txt",
		"COM1", "com2.txt", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "lpt2.txt", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
		"COM¹", "com².txt", "COM³", "LPT¹", "lpt².txt", "LPT³",
	}
	for _, raw := range reserved {
		got, code := ParseRelative(Windows, raw, false)
		if code != api.ErrorInvalidInput {
			t.Fatalf("ParseRelative(%q) code = %q, want %q", raw, code, api.ErrorInvalidInput)
		}
		if got.Target() != 0 || got.String() != "" || len(got.Components()) != 0 {
			t.Fatalf("ParseRelative(%q) returned partial path", raw)
		}
	}
}

func TestParseRootDirectoryWindows(t *testing.T) {
	t.Parallel()

	accepted := []struct {
		raw        string
		want       string
		components []string
	}{
		{raw: `C:\`, want: "C:/"},
		{raw: `c:/`, want: "c:/"},
		{raw: `D:\Dir\Sub`, want: "D:/Dir/Sub", components: []string{"Dir", "Sub"}},
		{raw: `E:/Dir\Sub`, want: "E:/Dir/Sub", components: []string{"Dir", "Sub"}},
		{raw: `C:\Dir\\.\Sub\`, want: "C:/Dir/Sub", components: []string{"Dir", "Sub"}},
		{raw: `Z:\` + strings.Repeat("a", 4093), want: "Z:/" + strings.Repeat("a", 4093), components: []string{strings.Repeat("a", 4093)}},
	}
	for _, test := range accepted {
		root, code := ParseRootDirectory(Windows, test.raw)
		if code != "" {
			t.Fatalf("ParseRootDirectory(%q) code = %q, want success", test.raw, code)
		}
		if root.Target() != Windows || root.String() != test.want || root.ByteLen() != len(test.want) {
			t.Fatalf("ParseRootDirectory(%q) = target %d, string %q, bytes %d", test.raw, root.Target(), root.String(), root.ByteLen())
		}
		assertComponents(t, root.Components(), test.components)
	}

	rejected := []string{
		"", `C:`, `C:relative`, `\rooted`, `/rooted`, `\\server\share`, `\\.\device`, `\\?\C:\path`, `\??\C:\path`,
		`\\?\Volume{01234567-89ab-cdef-0123-456789abcdef}\`, `1:\path`, `CC:\path`,
		`C:\a\..\b`, `C:\CON`, `C:\name:stream`, `C:\trailing.`, `C:\trailing `,
		string(append([]byte(`C:\`), 0xff)), `C:\` + strings.Repeat("a", 4094),
	}
	for _, raw := range rejected {
		root, code := ParseRootDirectory(Windows, raw)
		if code != api.ErrorInvalidInput {
			t.Fatalf("ParseRootDirectory(%q) code = %q, want %q", raw, code, api.ErrorInvalidInput)
		}
		if root.Target() != 0 || root.String() != "" || len(root.Components()) != 0 {
			t.Fatalf("ParseRootDirectory(%q) returned partial root", raw)
		}
	}
}

func TestPathComponentsAreDefensiveCopies(t *testing.T) {
	t.Parallel()

	relative, code := ParseRelative(POSIX, "a/b", false)
	if code != "" {
		t.Fatalf("ParseRelative() code = %q", code)
	}
	components := relative.Components()
	components[0] = "changed"
	assertComponents(t, relative.Components(), []string{"a", "b"})

	root, code := ParseRootDirectory(POSIX, "/a/b")
	if code != "" {
		t.Fatalf("ParseRootDirectory() code = %q", code)
	}
	components = root.Components()
	components[0] = "changed"
	assertComponents(t, root.Components(), []string{"a", "b"})
}

func TestAppendDiscoveredPreservesActualSpelling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		target    TargetOS
		parentRaw string
		component string
		want      string
	}{
		{name: "POSIX root child", target: POSIX, parentRaw: ".", component: " leading ", want: " leading "},
		{name: "POSIX nested", target: POSIX, parentRaw: "a", component: `b\c:d`, want: `a/b\c:d`},
		{name: "Windows root child", target: Windows, parentRaw: ".", component: "ActualCase", want: "ActualCase"},
		{name: "Windows nested", target: Windows, parentRaw: `Dir\Sub`, component: "ActualCase.txt", want: "Dir/Sub/ActualCase.txt"},
		{name: "exact maximum", target: POSIX, parentRaw: strings.Repeat("a", 4094), component: "b", want: strings.Repeat("a", 4094) + "/b"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parent, code := ParseRelative(test.target, test.parentRaw, true)
			if code != "" {
				t.Fatalf("parse parent code = %q", code)
			}
			got, ok := AppendDiscovered(parent, test.component)
			if !ok {
				t.Fatalf("AppendDiscovered(%q, %q) rejected", test.parentRaw, test.component)
			}
			if got.Target() != test.target || got.String() != test.want || got.ByteLen() != len(test.want) {
				t.Fatalf("AppendDiscovered() = target %d, string %q, bytes %d", got.Target(), got.String(), got.ByteLen())
			}
		})
	}
}

func TestAppendDiscoveredRejectsUnaddressableNamesAtomically(t *testing.T) {
	t.Parallel()

	posixParent, code := ParseRelative(POSIX, "parent", false)
	if code != "" {
		t.Fatalf("parse POSIX parent code = %q", code)
	}
	windowsParent, code := ParseRelative(Windows, "parent", false)
	if code != "" {
		t.Fatalf("parse Windows parent code = %q", code)
	}
	tests := []struct {
		name      string
		parent    Relative
		component string
	}{
		{name: "zero parent", component: "child"},
		{name: "empty", parent: posixParent, component: ""},
		{name: "dot", parent: posixParent, component: "."},
		{name: "parent", parent: posixParent, component: ".."},
		{name: "POSIX separator", parent: posixParent, component: "a/b"},
		{name: "POSIX nul", parent: posixParent, component: "a\x00b"},
		{name: "POSIX invalid utf8", parent: posixParent, component: string([]byte{'a', 0xff})},
		{name: "Windows separator", parent: windowsParent, component: `a\b`},
		{name: "Windows forbidden", parent: windowsParent, component: "name:stream"},
		{name: "Windows reserved", parent: windowsParent, component: "CON.txt"},
		{name: "Windows trailing", parent: windowsParent, component: "name."},
		{name: "aggregate over maximum", parent: mustRelative(t, POSIX, strings.Repeat("a", 4094)), component: "bb"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := AppendDiscovered(test.parent, test.component)
			if ok {
				t.Fatalf("AppendDiscovered(%q, %q) accepted as %q", test.parent.String(), test.component, got.String())
			}
			if got.Target() != 0 || got.String() != "" || len(got.Components()) != 0 {
				t.Fatal("AppendDiscovered returned a partial path")
			}
		})
	}
}

func mustRelative(t *testing.T, target TargetOS, raw string) Relative {
	t.Helper()
	path, code := ParseRelative(target, raw, true)
	if code != "" {
		t.Fatalf("ParseRelative(%q) code = %q", raw, code)
	}
	return path
}

func assertComponents(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("Components() = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("Components() = %#v, want %#v", got, want)
		}
	}
}
