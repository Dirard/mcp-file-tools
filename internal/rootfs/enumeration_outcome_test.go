package rootfs

import (
	"errors"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/pathspec"
)

func TestEnumerationEvidenceFailuresAreClosedPerEntryOutcomes(t *testing.T) {
	parent := mustPortableRelative(t, "directory")
	tests := []struct {
		name string
		err  error
		want EnumerationDisposition
	}{
		{name: "source changed", err: ErrSourceChanged, want: EnumerationSourceChanged},
		{name: "permission", err: ErrPermissionDenied, want: EnumerationUnreadable},
		{name: "I/O", err: ErrIO, want: EnumerationUnreadable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			outcome, err := posixEnumerationOutcomeWithEvidence(parent, []byte("entry"), EntryFile, func(string) (EntryKind, Identity, bool, error) {
				return 0, Identity{}, false, test.err
			})
			if err != nil {
				t.Fatalf("outcome error = %v", err)
			}
			if outcome.Disposition() != test.want || outcome.BoundaryKind() != EntryFile {
				t.Fatalf("outcome = disposition %v, kind %v; want %v, %v", outcome.Disposition(), outcome.BoundaryKind(), test.want, EntryFile)
			}
			if entry, ok := outcome.Candidate(); ok || outcome.entry.Path.String() != "" || outcome.entry.Kind != 0 || outcome.entry.Identity != (Identity{}) || outcome.entry.IdentityKnown {
				t.Fatalf("failure outcome exposed candidate %#v, %v", entry, ok)
			}
		})
	}
}

func TestEnumerationClassifiesRawNameBeforeEvidenceAndContinues(t *testing.T) {
	parent := mustPortableRelative(t, "directory")
	evidenceCalls := 0
	invalid, err := posixEnumerationOutcomeWithEvidence(parent, []byte{0xff}, EntryDir, func(string) (EntryKind, Identity, bool, error) {
		evidenceCalls++
		return EntryDir, Identity{}, false, nil
	})
	if err != nil {
		t.Fatalf("invalid-name outcome error = %v", err)
	}
	if invalid.Disposition() != EnumerationPathEncodingUnsupported || invalid.BoundaryKind() != EntryDir || evidenceCalls != 0 {
		t.Fatalf("invalid-name outcome = disposition %v, kind %v, evidence calls %d", invalid.Disposition(), invalid.BoundaryKind(), evidenceCalls)
	}

	dispositions := []EnumerationDisposition{invalid.Disposition()}
	changed, err := posixEnumerationOutcomeWithEvidence(parent, []byte("changed"), EntryFile, func(string) (EntryKind, Identity, bool, error) {
		return 0, Identity{}, false, ErrSourceChanged
	})
	if err != nil {
		t.Fatalf("changed outcome error = %v", err)
	}
	dispositions = append(dispositions, changed.Disposition())
	identity := Identity{Platform: pathspec.POSIX, File: [16]byte{1}}
	later, err := posixEnumerationOutcomeWithEvidence(parent, []byte("later"), EntryFile, func(string) (EntryKind, Identity, bool, error) {
		return EntryFile, identity, true, nil
	})
	if err != nil {
		t.Fatalf("later outcome error = %v", err)
	}
	dispositions = append(dispositions, later.Disposition())
	entry, ok := later.Candidate()
	if !ok || entry.Identity != identity || entry.Path.String() != "directory/later" {
		t.Fatalf("later sibling candidate = %#v, %v", entry, ok)
	}
	want := []EnumerationDisposition{EnumerationPathEncodingUnsupported, EnumerationSourceChanged, EnumerationCandidate}
	for index := range want {
		if dispositions[index] != want[index] {
			t.Fatalf("dispositions = %v, want %v", dispositions, want)
		}
	}
}

func TestEnumerationClosedHandleFailureRemainsTerminal(t *testing.T) {
	parent := mustPortableRelative(t, "directory")
	outcome, err := posixEnumerationOutcomeWithEvidence(parent, []byte("entry"), EntryFile, func(string) (EntryKind, Identity, bool, error) {
		return 0, Identity{}, false, ErrClosed
	})
	if !errors.Is(err, ErrClosed) || outcome.Disposition() != 0 || outcome.BoundaryKind() != 0 {
		t.Fatalf("closed evidence = outcome %#v, error %v", outcome, err)
	}
}

func mustPortableRelative(t *testing.T, raw string) pathspec.Relative {
	t.Helper()
	path, code := pathspec.ParseRelative(pathspec.POSIX, raw, false)
	if code != "" {
		t.Fatalf("ParseRelative(%q) code = %q", raw, code)
	}
	return path
}
