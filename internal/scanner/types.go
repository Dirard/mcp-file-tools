package scanner

import (
	"context"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/navmodel"
	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"github.com/Dirard/mcp-file-tools/internal/rootfs"
)

// Mode is the closed set of filesystem traversal consumers.
type Mode uint8

const (
	Project Mode = iota + 1
	FileSearch
	TextSearch
	SymbolSearch
)

// Request is immutable scanner input retained by a dynamic cursor.
type Request struct {
	Tool           api.ToolName
	CWDID          uint64
	Mode           Mode
	Root           pathspec.Relative
	Depth          uint16
	IncludeIgnored bool
	Glob           *Glob
}

// Limits are cumulative lineage bounds. IgnoreDirsAdd extends only the
// ordinary directory policy and is copied before it can enter cursor state.
type Limits struct {
	MaxFiles         uint64
	MaxDirs          uint64
	MaxBytes         uint64
	MaxParserBytes   uint64
	FrontierMaxBytes uint64
	IgnoreDirsAdd    []string
}

// Counters report physical and logical work consumed by the lineage.
type Counters struct {
	Files          uint64
	Dirs           uint64
	DirectoryBytes uint64
	ContentBytes   uint64
	ParserBytes    uint64
}

// RowKind is a closed, presentation-neutral navigation record vocabulary.
type RowKind uint8

const (
	RowDirectory RowKind = iota + 1
	RowFile
	RowTextMatch
	RowTextContext
	RowSymbol
)

// Row is a bounded descriptor. It never retains raw file content.
type Row struct {
	Kind       RowKind
	Path       string
	Line       uint64
	Text       string
	Range      navmodel.Range
	SymbolKind api.Kind
	Name       string
}

// Candidate is one verified project-content object. File is supplied
// separately to make ownership explicit: scanner always closes it.
type Candidate struct {
	Path                   pathspec.Relative
	Kind                   rootfs.EntryKind
	Identity               rootfs.Identity
	Depth                  uint16
	ContentBytesRemaining  uint64
	ParserBytesRemaining   uint64
	RetainedBytesRemaining uint64
	Deadline               time.Time
}

// ConsumeResult is the bounded output of processing one verified candidate.
// Code is terminal. Warning is a broad skip/partial outcome. A candidate may
// also return rows and byte charges without either.
type ConsumeResult struct {
	Rows         []Row
	Warning      api.WarningCode
	Code         api.ErrorCode
	ContentBytes uint64
	ParserBytes  uint64
}

// Consumer receives each verified candidate at most once. file is non-nil
// only for regular files and is valid only for the duration of Consume.
type Consumer interface {
	Consume(context.Context, Candidate, *rootfs.File) ConsumeResult
}

// CandidateSelector can reject a lexical traversal candidate before scanner
// opens it. Selection must depend only on path and kind.
type CandidateSelector interface {
	SelectCandidate(pathspec.Relative, rootfs.EntryKind) bool
}

// ConsumerFunc adapts a function into a Consumer.
type ConsumerFunc func(context.Context, Candidate, *rootfs.File) ConsumeResult

func (function ConsumerFunc) Consume(ctx context.Context, candidate Candidate, file *rootfs.File) ConsumeResult {
	return function(ctx, candidate, file)
}

// Batch is one ordered page plus cumulative traversal state.
type Batch struct {
	Rows     []Row
	Counters Counters
	Warnings []navmodel.WarningSummary
	Complete bool
}

// RowFit is the presentation decision for one already-consumed row.
type RowFit uint8

const (
	RowFits RowFit = iota + 1
	RowNextPage
	RowIntrinsicOverflow
)

// RowPage lets a caller apply its exact rendered-byte budget while scanner
// retains the first row that does not fit. Commit is called only after Try
// returns RowFits.
type RowPage interface {
	Try(Row) RowFit
	Commit(Row)
}
