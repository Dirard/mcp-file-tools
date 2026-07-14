package cursor

import (
	"time"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/rootfs"
)

// State is an immutable, cursor-owned continuation snapshot.
type State interface {
	Tool() api.ToolName
	CWDID() uint64
	Digest() [32]byte
	SharedDigest() ([32]byte, bool)
	Footprint() uint64
	CloneForCompute() State
}

// SharedAllocation is immutable backing shared by every entry in a read lineage.
type SharedAllocation interface {
	Digest() [32]byte
	Footprint() uint64
}

type entryRuntime interface{}

// ReservationKind identifies the only two pre-admitted continuation plans.
type ReservationKind uint8

const (
	ReadPagePlan ReservationKind = iota + 1
	BroadSummaryFinal
)

// ReservationUse describes one exact reservation or materialization transition.
type ReservationUse struct {
	Kind            ReservationKind
	Slots           uint64
	Bytes           uint64
	MustTerminalize bool
}

// ReadPageReservation reserves every page after the already-rendered first page.
type ReadPageReservation struct {
	Pages uint32
	Slots uint64
	Bytes uint64
}

// DynamicInitial creates one project/search lineage with a terminal-summary credit.
type DynamicInitial struct {
	State       State
	Root        *rootfs.Lease
	SummaryPlan ReservationUse
}

// ReadInitial creates one immutable read lineage and charges shared backing once.
type ReadInitial struct {
	State    State
	Shared   SharedAllocation
	PagePlan ReadPageReservation
}

// InitialCommit couples the first token with its transactional publication owner.
type InitialCommit struct {
	Token       Token
	Publication *InitialPublication
}

// Clock is the minimal registry clock contract. Production installs wallClock.
type Clock interface {
	Now() time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }
