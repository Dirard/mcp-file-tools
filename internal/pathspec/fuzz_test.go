package pathspec

import (
	"strings"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

func FuzzParseRelative(f *testing.F) {
	for _, seed := range []string{
		"", ".", "a", "a/b", `a\b`, "..", "/absolute", `C:\absolute`, " leading ", "cafe\u0301", "CON.txt", string([]byte{0xff}),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		for _, target := range [...]TargetOS{POSIX, Windows} {
			for _, allowRoot := range [...]bool{false, true} {
				path, code := ParseRelative(target, raw, allowRoot)
				if code != "" {
					if code != api.ErrorInvalidInput && code != api.ErrorPathOutsideCWD {
						t.Fatalf("unexpected error code %q", code)
					}
					if path.Target() != 0 || path.String() != "" || len(path.Components()) != 0 {
						t.Fatal("rejected input returned a partial path")
					}
					continue
				}
				if path.Target() != target || path.ByteLen() < 1 || path.ByteLen() > maxPathBytes {
					t.Fatalf("accepted path has invalid target/length: target=%d length=%d", path.Target(), path.ByteLen())
				}
				if target == POSIX && path.String() != raw {
					t.Fatalf("POSIX input was repaired: raw=%q normalized=%q", raw, path.String())
				}
				if target == Windows && path.String() != strings.ReplaceAll(raw, `\`, "/") {
					t.Fatalf("Windows input was repaired beyond separator normalization: raw=%q normalized=%q", raw, path.String())
				}
				reparsed, reparseCode := ParseRelative(target, path.String(), allowRoot)
				if reparseCode != "" || reparsed.String() != path.String() {
					t.Fatalf("accepted output did not reparse identically: code=%q output=%q reparsed=%q", reparseCode, path.String(), reparsed.String())
				}
				assertComponents(t, reparsed.Components(), path.Components())
			}
		}
	})
}

func FuzzAppendDiscovered(f *testing.F) {
	for _, seed := range []string{"", ".", "..", "child", "a/b", `a\b`, "CON", " leading ", string([]byte{0xff})} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, component string) {
		for _, target := range [...]TargetOS{POSIX, Windows} {
			parent, code := ParseRelative(target, "parent", false)
			if code != "" {
				t.Fatalf("parse fixed parent: %q", code)
			}
			path, ok := AppendDiscovered(parent, component)
			if !ok {
				if path.Target() != 0 || path.String() != "" || len(path.Components()) != 0 {
					t.Fatal("rejected discovered component returned a partial path")
				}
				continue
			}
			if path.Target() != target || path.ByteLen() > maxPathBytes {
				t.Fatalf("accepted discovered path has invalid target/length: target=%d length=%d", path.Target(), path.ByteLen())
			}
			reparsed, reparseCode := ParseRelative(target, path.String(), false)
			if reparseCode != "" || reparsed.String() != path.String() {
				t.Fatalf("discovered output did not reparse: code=%q path=%q", reparseCode, path.String())
			}
		}
	})
}
