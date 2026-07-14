package rootfs

import (
	"errors"
	"unicode/utf8"

	"github.com/Dirard/mcp-file-tools/internal/pathspec"
)

type posixEntryEvidence func(string) (EntryKind, Identity, bool, error)

func posixEnumerationOutcome(parent pathspec.Relative, rawName []byte, kind EntryKind, identity Identity, identityKnown bool) EnumerationOutcome {
	if !utf8.Valid(rawName) {
		return EnumerationOutcome{
			disposition:  EnumerationPathEncodingUnsupported,
			boundaryKind: kind,
		}
	}
	return enumerationCandidate(parent, string(rawName), kind, identity, identityKnown)
}

func posixEnumerationOutcomeWithEvidence(parent pathspec.Relative, rawName []byte, kindHint EntryKind, evidence posixEntryEvidence) (EnumerationOutcome, error) {
	outcome := posixEnumerationOutcome(parent, rawName, kindHint, Identity{}, false)
	if outcome.Disposition() != EnumerationCandidate {
		return outcome, nil
	}

	kind, identity, identityKnown, err := evidence(string(rawName))
	if err != nil {
		if errors.Is(err, ErrClosed) {
			return EnumerationOutcome{}, ErrClosed
		}
		disposition := EnumerationUnreadable
		if errors.Is(err, ErrSourceChanged) || errors.Is(err, ErrNotFound) {
			disposition = EnumerationSourceChanged
		}
		return EnumerationOutcome{
			disposition:  disposition,
			boundaryKind: kindHint,
		}, nil
	}

	outcome.boundaryKind = kind
	outcome.entry.Kind = kind
	outcome.entry.Identity = identity
	outcome.entry.IdentityKnown = identityKnown
	return outcome, nil
}
