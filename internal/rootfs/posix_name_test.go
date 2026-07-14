package rootfs

import (
	"strings"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/pathspec"
)

func TestPOSIXEnumerationOutcomeKeepsEncodingAndAddressabilityClosed(t *testing.T) {
	t.Parallel()

	root, code := pathspec.ParseRelative(pathspec.POSIX, ".", true)
	if code != "" {
		t.Fatalf("ParseRelative(root) code = %q", code)
	}
	identity := Identity{Platform: pathspec.POSIX, Mount: [16]byte{1}, File: [16]byte{2}}
	valid := posixEnumerationOutcome(root, []byte("literal\\name"), EntryFile, identity, true)
	entry, ok := valid.Candidate()
	if !ok || entry.Path.String() != `literal\name` || entry.Identity != identity || !entry.IdentityKnown {
		t.Fatalf("valid candidate = %#v, ok=%v", entry, ok)
	}

	invalidEncoding := posixEnumerationOutcome(root, []byte{'b', 'a', 'd', 0xff}, EntryDir, identity, true)
	if invalidEncoding.Disposition() != EnumerationPathEncodingUnsupported || invalidEncoding.BoundaryKind() != EntryDir {
		t.Fatalf("invalid encoding outcome = disposition %v, kind %v", invalidEncoding.Disposition(), invalidEncoding.BoundaryKind())
	}
	assertPathlessOutcome(t, invalidEncoding)

	longParent, code := pathspec.ParseRelative(pathspec.POSIX, strings.Repeat("p", 4091), false)
	if code != "" {
		t.Fatalf("ParseRelative(long parent) code = %q", code)
	}
	unaddressable := posixEnumerationOutcome(longParent, []byte("child"), EntryFile, identity, true)
	if unaddressable.Disposition() != EnumerationUnaddressable || unaddressable.BoundaryKind() != EntryFile {
		t.Fatalf("unaddressable outcome = disposition %v, kind %v", unaddressable.Disposition(), unaddressable.BoundaryKind())
	}
	assertPathlessOutcome(t, unaddressable)
}

func assertPathlessOutcome(t *testing.T, outcome EnumerationOutcome) {
	t.Helper()
	entry, ok := outcome.Candidate()
	if ok || entry.Path.String() != "" || entry.Kind != 0 || entry.Identity != (Identity{}) || entry.IdentityKnown {
		t.Fatalf("pathless outcome leaked candidate data: %#v, ok=%v", entry, ok)
	}
}
