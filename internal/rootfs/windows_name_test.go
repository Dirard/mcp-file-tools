package rootfs

import (
	"strings"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/pathspec"
)

func TestWindowsEnumerationOutcomePreservesWellFormedRawUTF16(t *testing.T) {
	t.Parallel()

	parent, code := pathspec.ParseRelative(pathspec.Windows, "Parent", false)
	if code != "" {
		t.Fatalf("ParseRelative(parent) code = %q", code)
	}
	identity := Identity{Platform: pathspec.Windows, Mount: [16]byte{1}, File: [16]byte{2}}
	outcome := windowsEnumerationOutcome(parent, []uint16{'M', 'i', 'x', 'e', 'd', 0xd83d, 0xde00}, EntryFile, identity, true)
	if outcome.Disposition() != EnumerationCandidate {
		t.Fatalf("Disposition() = %v, want %v", outcome.Disposition(), EnumerationCandidate)
	}
	if outcome.BoundaryKind() != EntryFile {
		t.Fatalf("BoundaryKind() = %v, want %v", outcome.BoundaryKind(), EntryFile)
	}
	entry, ok := outcome.Candidate()
	if !ok {
		t.Fatal("Candidate() rejected a well-formed UTF-16 name")
	}
	if got := entry.Path.String(); got != "Parent/Mixed😀" {
		t.Fatalf("candidate path = %q, want %q", got, "Parent/Mixed😀")
	}
	if entry.Kind != EntryFile || entry.Identity != identity || !entry.IdentityKnown {
		t.Fatalf("candidate evidence = %#v", entry)
	}
}

func TestWindowsEnumerationOutcomeRejectsIllFormedRawUTF16WithoutReplacement(t *testing.T) {
	t.Parallel()

	parent, code := pathspec.ParseRelative(pathspec.Windows, ".", true)
	if code != "" {
		t.Fatalf("ParseRelative(root) code = %q", code)
	}
	for name, raw := range map[string][]uint16{
		"isolated high":        {0xd800},
		"high before ordinary": {0xd800, 'x'},
		"isolated low":         {0xdc00},
	} {
		t.Run(name, func(t *testing.T) {
			outcome := windowsEnumerationOutcome(parent, raw, EntryDir, Identity{Platform: pathspec.Windows}, true)
			if outcome.Disposition() != EnumerationPathEncodingUnsupported {
				t.Fatalf("Disposition() = %v, want %v", outcome.Disposition(), EnumerationPathEncodingUnsupported)
			}
			if outcome.BoundaryKind() != EntryDir {
				t.Fatalf("BoundaryKind() = %v, want %v", outcome.BoundaryKind(), EntryDir)
			}
			if entry, ok := outcome.Candidate(); ok || entry.Path.String() != "" || entry.Kind != 0 || entry.Identity != (Identity{}) || entry.IdentityKnown {
				t.Fatalf("ill-formed boundary leaked candidate data: %#v, ok=%v", entry, ok)
			}
		})
	}
}

func TestWindowsEnumerationOutcomeKeepsEncodingAndLexicalFailuresDistinct(t *testing.T) {
	t.Parallel()

	parent, code := pathspec.ParseRelative(pathspec.Windows, ".", true)
	if code != "" {
		t.Fatalf("ParseRelative(root) code = %q", code)
	}
	outcome := windowsEnumerationOutcome(parent, []uint16{'C', 'O', 'N'}, EntryFile, Identity{}, false)
	if outcome.Disposition() != EnumerationUnaddressable {
		t.Fatalf("Disposition() = %v, want %v", outcome.Disposition(), EnumerationUnaddressable)
	}
	if _, ok := outcome.Candidate(); ok {
		t.Fatal("lexically unaddressable boundary exposed a candidate")
	}
	longParent, code := pathspec.ParseRelative(pathspec.Windows, strings.Repeat("p", 4091), false)
	if code != "" {
		t.Fatalf("ParseRelative(long parent) code = %q", code)
	}
	overlong := windowsEnumerationOutcome(longParent, []uint16{'c', 'h', 'i', 'l', 'd'}, EntryDir, Identity{}, false)
	if overlong.Disposition() != EnumerationUnaddressable || overlong.BoundaryKind() != EntryDir {
		t.Fatalf("overlong outcome = disposition %v, kind %v", overlong.Disposition(), overlong.BoundaryKind())
	}
	assertPathlessOutcome(t, overlong)
}
