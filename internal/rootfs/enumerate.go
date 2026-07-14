package rootfs

import (
	"context"

	"github.com/Dirard/mcp-file-tools/internal/pathspec"
)

// ReadEntries streams direct physical children in platform order. Each raw
// directory record is charged before its closed outcome is presented.
func (directory *Dir) ReadEntries(ctx context.Context, charge func(uint64) error, consume func(EnumerationOutcome) error) error {
	if ctx == nil {
		return ErrIO
	}
	if charge == nil || consume == nil {
		return ErrInvalidCallback
	}
	if directory == nil {
		return ErrClosed
	}
	directory.mu.Lock()
	defer directory.mu.Unlock()
	if directory.closed {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return enumeratePlatformDir(directory.handle, directory.resolved, directory.identity.Mount, ctx, charge, consume)
}

func enumerationCandidate(parent pathspec.Relative, component string, kind EntryKind, identity Identity, identityKnown bool) EnumerationOutcome {
	path, ok := pathspec.AppendDiscovered(parent, component)
	if !ok {
		return EnumerationOutcome{
			disposition:  EnumerationUnaddressable,
			boundaryKind: kind,
		}
	}
	return EnumerationOutcome{
		disposition:  EnumerationCandidate,
		boundaryKind: kind,
		entry: Entry{
			Path:          path,
			Kind:          kind,
			Identity:      identity,
			IdentityKnown: identityKnown,
		},
	}
}
