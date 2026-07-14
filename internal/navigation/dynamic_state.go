package navigation

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"strings"
	"unsafe"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/cursor"
	"github.com/Dirard/mcp-file-tools/internal/present"
	"github.com/Dirard/mcp-file-tools/internal/scanner"
)

type dynamicMode uint8

const (
	dynamicProject dynamicMode = iota + 1
	dynamicFileSearch
	dynamicTextSearch
	dynamicSymbolSearch
)

type dynamicState struct {
	scan        *scanner.State
	mode        dynamicMode
	tool        api.ToolName
	cwdID       uint64
	limit       uint16
	projectPath string
	pattern     searchPattern
	summary     []present.Warning
}

var _ cursor.State = (*dynamicState)(nil)

func newTraversalState(mode dynamicMode, scan *scanner.State, limit uint16, projectPath string, pattern searchPattern) *dynamicState {
	if scan == nil || limit == 0 || limit > 1000 {
		return nil
	}
	state := &dynamicState{
		scan:        scan,
		mode:        mode,
		tool:        scan.Tool(),
		cwdID:       scan.CWDID(),
		limit:       limit,
		projectPath: strings.Clone(projectPath),
		pattern:     pattern,
	}
	if !state.valid() {
		return nil
	}
	return state
}

func newSummaryState(parent *dynamicState, warnings []present.Warning) *dynamicState {
	if parent == nil || len(warnings) == 0 {
		return nil
	}
	state := &dynamicState{
		mode:        parent.mode,
		tool:        parent.tool,
		cwdID:       parent.cwdID,
		limit:       parent.limit,
		projectPath: strings.Clone(parent.projectPath),
		pattern:     parent.pattern,
		summary:     cloneWarnings(warnings),
	}
	if !state.valid() {
		return nil
	}
	return state
}

func newInitialSummaryState(mode dynamicMode, tool api.ToolName, cwdID uint64, limit uint16, projectPath string, pattern searchPattern, warnings []present.Warning) *dynamicState {
	parent := &dynamicState{mode: mode, tool: tool, cwdID: cwdID, limit: limit, projectPath: strings.Clone(projectPath), pattern: pattern}
	return newSummaryState(parent, warnings)
}

func (state *dynamicState) Tool() api.ToolName {
	if state == nil {
		return ""
	}
	return state.tool
}

func (state *dynamicState) CWDID() uint64 {
	if state == nil {
		return 0
	}
	return state.cwdID
}

func (state *dynamicState) SharedDigest() ([32]byte, bool) {
	return [32]byte{}, false
}

func (state *dynamicState) Footprint() uint64 {
	if state == nil {
		return 0
	}
	bytes := uint64(unsafe.Sizeof(dynamicState{})) + uint64(len(state.projectPath)+len(state.pattern.query))
	if state.scan != nil {
		bytes += state.scan.Footprint()
	}
	bytes += uint64(cap(state.summary)) * uint64(unsafe.Sizeof(present.Warning{}))
	for _, warning := range state.summary {
		bytes += uint64(len(warning.Path))
	}
	return bytes
}

func (state *dynamicState) Digest() [32]byte {
	if state == nil {
		return sha256.Sum256(nil)
	}
	digest := sha256.New()
	writeDynamicUint64(digest, uint64(state.mode))
	writeDynamicString(digest, string(state.tool))
	writeDynamicUint64(digest, state.cwdID)
	writeDynamicUint64(digest, uint64(state.limit))
	writeDynamicString(digest, state.projectPath)
	writeDynamicString(digest, state.pattern.query)
	writeDynamicUint64(digest, uint64(state.pattern.context))
	if state.pattern.regex {
		digest.Write([]byte{1})
	} else {
		digest.Write([]byte{0})
	}
	if state.pattern.ignoreCase {
		digest.Write([]byte{1})
	} else {
		digest.Write([]byte{0})
	}
	if state.scan == nil {
		digest.Write([]byte{0})
	} else {
		digest.Write([]byte{1})
		scanDigest := state.scan.Digest()
		digest.Write(scanDigest[:])
	}
	writeDynamicUint64(digest, uint64(len(state.summary)))
	for _, warning := range state.summary {
		writeDynamicString(digest, string(warning.Code))
		writeDynamicUint64(digest, warning.Count)
		writeDynamicString(digest, warning.Path)
	}
	var result [32]byte
	copy(result[:], digest.Sum(nil))
	return result
}

func (state *dynamicState) CloneForCompute() cursor.State {
	if state == nil {
		return nil
	}
	clone := *state
	clone.projectPath = strings.Clone(state.projectPath)
	clone.pattern.query = strings.Clone(state.pattern.query)
	clone.summary = cloneWarnings(state.summary)
	if state.scan != nil {
		clone.scan = state.scan.Clone()
	}
	if !clone.valid() {
		return nil
	}
	return &clone
}

func (state *dynamicState) valid() bool {
	if state == nil || state.cwdID == 0 || state.limit == 0 || state.limit > 1000 || !state.tool.Valid() {
		return false
	}
	switch state.mode {
	case dynamicProject:
		if state.tool != api.ToolProject || state.projectPath == "" || !state.pattern.valid(state.mode) {
			return false
		}
	case dynamicFileSearch:
		if state.tool != api.ToolSearch || state.projectPath != "" || !state.pattern.valid(state.mode) {
			return false
		}
	case dynamicTextSearch, dynamicSymbolSearch:
		if state.tool != api.ToolSearch || state.projectPath != "" || !state.pattern.valid(state.mode) {
			return false
		}
	default:
		return false
	}
	if (state.scan == nil) == (len(state.summary) == 0) {
		return false
	}
	if state.scan != nil {
		if state.scan.Tool() != state.tool || state.scan.CWDID() != state.cwdID {
			return false
		}
		expectedMode := scanner.Project
		switch state.mode {
		case dynamicFileSearch:
			expectedMode = scanner.FileSearch
		case dynamicTextSearch:
			expectedMode = scanner.TextSearch
		case dynamicSymbolSearch:
			expectedMode = scanner.SymbolSearch
		}
		return state.scan.Mode() == expectedMode
	}
	for _, warning := range state.summary {
		if !warning.Code.Valid() || warning.Count == 0 {
			return false
		}
	}
	return true
}

func cloneWarnings(warnings []present.Warning) []present.Warning {
	if warnings == nil {
		return nil
	}
	cloned := make([]present.Warning, len(warnings))
	for index, warning := range warnings {
		warning.Path = strings.Clone(warning.Path)
		cloned[index] = warning
	}
	return cloned
}

func writeDynamicString(digest hash.Hash, value string) {
	writeDynamicUint64(digest, uint64(len(value)))
	digest.Write([]byte(value))
}

func writeDynamicUint64(digest hash.Hash, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	digest.Write(encoded[:])
}
