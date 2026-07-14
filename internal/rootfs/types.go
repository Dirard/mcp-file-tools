package rootfs

import (
	"context"
	"errors"
	"sync"

	"github.com/Dirard/mcp-file-tools/internal/pathspec"
)

var (
	ErrInvalidTarget    = errors.New("rootfs: invalid target OS")
	ErrNotFound         = errors.New("rootfs: not found")
	ErrNotDirectory     = errors.New("rootfs: not a directory")
	ErrNotRegular       = errors.New("rootfs: not a regular file")
	ErrPermissionDenied = errors.New("rootfs: permission denied")
	ErrIO               = errors.New("rootfs: I/O failure")
	ErrClosed           = errors.New("rootfs: handle closed")
	ErrBorrowExpired    = errors.New("rootfs: borrow expired")
	ErrWrongTargetKind  = errors.New("rootfs: wrong target kind")
	ErrTargetConsumed   = errors.New("rootfs: target consumed")
	ErrInvalidReadSize  = errors.New("rootfs: invalid read buffer size")
	ErrSymlink          = errors.New("rootfs: symbolic link boundary")
	ErrMountBoundary    = errors.New("rootfs: mount boundary")
	ErrSpecial          = errors.New("rootfs: special node")
	ErrSourceChanged    = errors.New("rootfs: source changed")
	ErrInvalidCallback  = errors.New("rootfs: invalid borrow callback")
)

// Identity is stable handle-derived evidence for one filesystem object.
type Identity struct {
	Platform pathspec.TargetOS
	Mount    [16]byte
	File     [16]byte
}

// EntryKind classifies one physical directory entry boundary.
type EntryKind uint8

const (
	EntryFile EntryKind = iota + 1
	EntryDir
	EntrySymlink
	EntrySpecial
	EntryBoundary
)

// Entry is an addressable directory candidate with actual enumerated spelling.
type Entry struct {
	Path          pathspec.Relative
	Kind          EntryKind
	Identity      Identity
	IdentityKnown bool
}

// EnumerationDisposition is the closed result of validating one physical entry.
type EnumerationDisposition uint8

const (
	EnumerationCandidate EnumerationDisposition = iota + 1
	EnumerationPathEncodingUnsupported
	EnumerationUnaddressable
	EnumerationSourceChanged
	EnumerationUnreadable
)

// EnumerationOutcome never carries path or identity data for skipped boundaries.
type EnumerationOutcome struct {
	disposition  EnumerationDisposition
	boundaryKind EntryKind
	entry        Entry
}

func (outcome EnumerationOutcome) Disposition() EnumerationDisposition {
	return outcome.disposition
}

func (outcome EnumerationOutcome) BoundaryKind() EntryKind {
	return outcome.boundaryKind
}

func (outcome EnumerationOutcome) Candidate() (Entry, bool) {
	if outcome.disposition != EnumerationCandidate {
		return Entry{}, false
	}
	return outcome.entry, true
}

// Root owns the registry-level root handle and immutable handle-derived evidence.
type Root struct {
	mu        sync.Mutex
	handle    platformRoot
	canonical string
	identity  Identity
	closed    bool
}

type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}

// Lease owns one duplicated root handle for a call/cursor lineage.
type Lease struct {
	_                 noCopy
	mu                sync.Mutex
	handle            platformRoot
	identity          Identity
	activeBorrows     uint64
	nextGeneration    uint64
	activeGenerations map[uint64]struct{}
	closeRequested    bool
	closed            bool
}

// Borrowed exposes only relative opens for the lifetime of one callback.
type Borrowed interface {
	OpenDir(pathspec.Relative) (*Dir, error)
	OpenRegular(pathspec.Relative) (*File, error)
}

// Dir owns one verified directory handle.
type Dir struct {
	mu       sync.Mutex
	handle   platformDir
	identity Identity
	resolved pathspec.Relative
	closed   bool
}

// File owns one verified regular-file handle.
type File struct {
	mu       sync.Mutex
	handle   platformFile
	identity Identity
	resolved pathspec.Relative
	closed   bool
}

// SearchTargetKind is the closed set of initial-search handle kinds.
type SearchTargetKind uint8

const (
	SearchTargetDirectory SearchTargetKind = iota + 1
	SearchTargetRegular
)

// SearchTarget owns exactly one verified Dir or File until it is transferred.
type SearchTarget struct {
	_         noCopy
	mu        sync.Mutex
	kind      SearchTargetKind
	directory *Dir
	file      *File
	consumed  bool
}

type borrowedView struct {
	mu         sync.Mutex
	condition  *sync.Cond
	lease      *Lease
	generation uint64
	active     bool
	operations uint64
}

var _ Borrowed = (*borrowedView)(nil)

// Compile-time API assertions keep the read seam mode-free.
var (
	_ interface {
		OpenRegular(pathspec.Relative) (*File, error)
		OpenSearchTarget(pathspec.Relative) (*SearchTarget, error)
	} = (*Lease)(nil)
	_ interface {
		ReadContext(context.Context, []byte) (int, error)
	} = (*File)(nil)
)
