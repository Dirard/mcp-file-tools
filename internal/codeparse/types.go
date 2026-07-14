package codeparse

import (
	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/navmodel"
)

// State is the parser outcome before a navigation caller maps it to an item or
// whole-call response.
type State uint8

const (
	Clean State = iota + 1
	Recoverable
	Fatal
	CallAborted
)

func (state State) valid() bool {
	return state >= Clean && state <= CallAborted
}

// Result owns compact projected navigation records. It intentionally contains
// neither parser nodes nor source bytes.
type Result struct {
	Language api.Language
	State    State
	Records  []navmodel.Record
}

// Input is a freshly decoded canonical source snapshot. SHA256 must be the
// digest of Canonical; the service verifies it before consulting its cache.
type Input struct {
	Path      string
	Canonical []byte
	SHA256    [32]byte
	Language  api.Language
}

func cloneResult(result Result) (Result, bool) {
	if !result.Language.Valid() || !result.State.valid() {
		return Result{}, false
	}
	records, ok := navmodel.CloneRecords(result.Records)
	if !ok {
		return Result{}, false
	}
	if (result.State == Fatal || result.State == CallAborted) && len(records) != 0 {
		return Result{}, false
	}
	result.Records = records
	return result, true
}

type rawRecord struct {
	kind      string
	lineRange navmodel.Range
	depth     uint16
	name      string
}

type parseOutput struct {
	records     []rawRecord
	errorRanges []navmodel.Range
	fatal       bool
}
