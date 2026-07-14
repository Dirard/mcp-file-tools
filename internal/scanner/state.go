package scanner

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"sort"
	"strings"
	"unicode/utf8"
	"unsafe"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/config"
	"github.com/Dirard/mcp-file-tools/internal/navmodel"
	"github.com/Dirard/mcp-file-tools/internal/rootfs"
)

// State is an immutable resumable traversal snapshot once returned from Service.
type State struct {
	request  Request
	limits   Limits
	ignore   ignorePolicy
	frontier frontier
	pending  []Row
	counters Counters
	warnings navmodel.Accumulator
	started  bool
}

func newState(request Request, limits Limits) (*State, api.ErrorCode) {
	if !validRequest(request) || !validLimits(limits) {
		return nil, api.ErrorInvalidInput
	}
	ignore, ok := newIgnorePolicy(limits.IgnoreDirsAdd)
	if !ok {
		return nil, api.ErrorInvalidInput
	}
	limits.IgnoreDirsAdd = nil
	if request.Glob != nil {
		compiled := *request.Glob
		request.Glob = &compiled
	}
	return &State{
		request: request,
		limits:  limits,
		ignore:  ignore,
		started: true,
	}, ""
}

func validRequest(request Request) bool {
	if request.CWDID == 0 || request.Root.Target() == 0 || request.Root.String() == "" {
		return false
	}
	switch request.Mode {
	case Project:
		return request.Tool == api.ToolProject && request.Glob == nil
	case FileSearch:
		return request.Tool == api.ToolSearch && request.Glob != nil
	case TextSearch, SymbolSearch:
		return request.Tool == api.ToolSearch
	default:
		return false
	}
}

func validLimits(limits Limits) bool {
	return limits.MaxFiles > 0 && limits.MaxDirs > 0 && limits.MaxBytes > 0 && limits.FrontierMaxBytes > 0
}

// Clone returns separately owned mutable working storage for one computation.
func (state *State) Clone() *State {
	if state == nil {
		return nil
	}
	clone := *state
	clone.ignore.extra = cloneStrings(state.ignore.extra)
	if state.frontier != nil {
		clone.frontier = make(frontier, len(state.frontier), cap(state.frontier))
		copy(clone.frontier, state.frontier)
	}
	clone.pending = cloneRows(state.pending)
	if state.request.Glob != nil {
		compiled := *state.request.Glob
		clone.request.Glob = &compiled
	}
	return &clone
}

// Footprint reports retained state storage, including slice capacities and strings.
func (state *State) Footprint() uint64 {
	if state == nil {
		return 0
	}
	bytes := uint64(unsafe.Sizeof(State{}))
	bytes += relativeRetainedBytes(state.request.Root)
	if state.request.Glob != nil {
		bytes += state.request.Glob.Footprint()
	}
	bytes += uint64(cap(state.ignore.extra)) * uint64(unsafe.Sizeof(""))
	for _, name := range state.ignore.extra {
		bytes += uint64(len(name))
	}
	bytes += state.frontier.retainedBytes()
	bytes += rowsRetainedBytes(state.pending)
	bytes += state.warnings.Footprint()
	return bytes
}

func (state *State) dynamicFootprint() uint64 {
	return state.frontier.retainedBytes() + rowsRetainedBytes(state.pending)
}

// Digest is a deterministic serialization hash used for successor tokens.
func (state *State) Digest() [32]byte {
	if state == nil {
		return sha256.Sum256(nil)
	}
	digest := sha256.New()
	writeDigestString(digest, string(state.request.Tool))
	writeDigestUint64(digest, state.request.CWDID)
	writeDigestUint64(digest, uint64(state.request.Mode))
	writeDigestString(digest, state.request.Root.String())
	writeDigestUint64(digest, uint64(state.request.Root.Target()))
	writeDigestUint64(digest, uint64(state.request.Depth))
	writeDigestBool(digest, state.request.IncludeIgnored)
	writeGlobDigest(digest, state.request.Glob)
	writeDigestUint64(digest, state.limits.MaxFiles)
	writeDigestUint64(digest, state.limits.MaxDirs)
	writeDigestUint64(digest, state.limits.MaxBytes)
	writeDigestUint64(digest, state.limits.MaxParserBytes)
	writeDigestUint64(digest, state.limits.FrontierMaxBytes)
	for _, name := range state.ignore.extra {
		writeDigestString(digest, name)
	}
	writeCountersDigest(digest, state.counters)

	ordered := append(frontier(nil), state.frontier...)
	sort.Slice(ordered, func(left, right int) bool { return compareScanUnits(ordered[left], ordered[right]) < 0 })
	for _, unit := range ordered {
		writeDigestString(digest, unit.path.String())
		writeDigestUint64(digest, uint64(unit.path.Target()))
		writeDigestUint64(digest, uint64(unit.kind))
		writeDigestUint64(digest, uint64(unit.depth))
		writeDigestBool(digest, unit.identityKnown)
		writeDigestUint64(digest, uint64(unit.identity.Platform))
		digest.Write(unit.identity.Mount[:])
		digest.Write(unit.identity.File[:])
	}
	for _, row := range state.pending {
		writeDigestBytes(digest, encodeRowKey(row))
	}
	for _, warning := range state.warnings.Summaries() {
		writeDigestString(digest, string(warning.Code()))
		writeDigestUint64(digest, warning.Count())
		writeDigestString(digest, warning.Example())
	}
	writeDigestBool(digest, state.started)
	var result [32]byte
	copy(result[:], digest.Sum(nil))
	return result
}

func (state *State) Tool() api.ToolName {
	if state == nil {
		return ""
	}
	return state.request.Tool
}

func (state *State) CWDID() uint64 {
	if state == nil {
		return 0
	}
	return state.request.CWDID
}

// Mode reports the immutable traversal consumer family.
func (state *State) Mode() Mode {
	if state == nil {
		return 0
	}
	return state.request.Mode
}

// MatchCandidateFile applies an optional cursor-owned file filter.
func (state *State) MatchCandidateFile(path string) bool {
	return state != nil && (state.request.Glob == nil || state.request.Glob.Match(path))
}

func (state *State) valid() bool {
	if state == nil || !state.started || !validRequest(state.request) || !validLimits(state.limits) {
		return false
	}
	if state.dynamicFootprint() > state.limits.FrontierMaxBytes || state.warnings.Validate() != nil {
		return false
	}
	for _, unit := range state.frontier {
		if unit.path.Target() == 0 || unit.path.String() == "" || (unit.kind != rootfs.EntryFile && unit.kind != rootfs.EntryDir) {
			return false
		}
	}
	for _, row := range state.pending {
		if !validRowForMode(row, state.request.Mode, row.Path) {
			return false
		}
	}
	return true
}

func cloneRows(rows []Row) []Row {
	if rows == nil {
		return nil
	}
	cloned := make([]Row, len(rows), cap(rows))
	for index, row := range rows {
		row.Path = strings.Clone(row.Path)
		row.Text = strings.Clone(row.Text)
		row.Name = strings.Clone(row.Name)
		cloned[index] = row
	}
	return cloned
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	cloned := make([]string, len(values), cap(values))
	for index, value := range values {
		cloned[index] = strings.Clone(value)
	}
	return cloned
}

func rowsRetainedBytes(rows []Row) uint64 {
	bytes := uint64(cap(rows)) * uint64(unsafe.Sizeof(Row{}))
	for _, row := range rows {
		bytes += uint64(len(row.Path) + len(row.Text) + len(row.Name))
	}
	return bytes
}

func validRowForMode(row Row, mode Mode, candidatePath string) bool {
	if row.Path != candidatePath || row.Path == "" || len(row.Path) > api.InputStringMaxBytes || !utf8.ValidString(row.Path) {
		return false
	}
	switch mode {
	case Project:
		return (row.Kind == RowDirectory || row.Kind == RowFile) && row.Line == 0 && row.Text == "" && !row.Range.Valid() && row.SymbolKind == "" && row.Name == ""
	case FileSearch:
		return row.Kind == RowFile && row.Line == 0 && row.Text == "" && !row.Range.Valid() && row.SymbolKind == "" && row.Name == ""
	case TextSearch:
		return (row.Kind == RowTextMatch || row.Kind == RowTextContext) && row.Line > 0 &&
			uint64(len(row.Text)) <= config.SearchScanLineMaxBytes && utf8.ValidString(row.Text) && !row.Range.Valid() && row.SymbolKind == "" && row.Name == ""
	case SymbolSearch:
		return row.Kind == RowSymbol && row.Line == 0 && row.Text == "" && row.Range.Valid() && row.SymbolKind.Valid() &&
			row.Name != "" && len(row.Name) <= api.InputStringMaxBytes && utf8.ValidString(row.Name)
	default:
		return false
	}
}

func writeGlobDigest(digest hash.Hash, glob *Glob) {
	if glob == nil {
		writeDigestBool(digest, false)
		return
	}
	writeDigestBool(digest, true)
	writeDigestBool(digest, glob.fullPath)
	writeDigestBool(digest, glob.ignoreCase)
	writeDigestUint64(digest, uint64(len(glob.variants)))
	for _, variant := range glob.variants {
		writeDigestUint64(digest, uint64(len(variant.segments)))
		for _, segment := range variant.segments {
			writeDigestBool(digest, segment.globstar)
			writeDigestUint64(digest, uint64(len(segment.atoms)))
			for _, atom := range segment.atoms {
				writeDigestUint64(digest, uint64(atom.kind))
				writeDigestUint64(digest, uint64(atom.scalar))
				writeDigestBool(digest, atom.negated)
				writeDigestUint64(digest, uint64(len(atom.ranges)))
				for _, candidate := range atom.ranges {
					writeDigestUint64(digest, uint64(candidate.first))
					writeDigestUint64(digest, uint64(candidate.last))
				}
			}
		}
	}
}

func writeCountersDigest(digest hash.Hash, counters Counters) {
	writeDigestUint64(digest, counters.Files)
	writeDigestUint64(digest, counters.Dirs)
	writeDigestUint64(digest, counters.DirectoryBytes)
	writeDigestUint64(digest, counters.ContentBytes)
	writeDigestUint64(digest, counters.ParserBytes)
}

func writeDigestString(digest hash.Hash, value string) {
	writeDigestBytes(digest, []byte(value))
}

func writeDigestBytes(digest hash.Hash, value []byte) {
	writeDigestUint64(digest, uint64(len(value)))
	digest.Write(value)
}

func writeDigestUint64(digest hash.Hash, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	digest.Write(encoded[:])
}

func writeDigestBool(digest hash.Hash, value bool) {
	if value {
		digest.Write([]byte{1})
	} else {
		digest.Write([]byte{0})
	}
}
