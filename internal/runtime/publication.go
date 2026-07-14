package runtime

import (
	"errors"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

// Publication guards a provisional result until its response is durably written.
type Publication interface {
	Prepare() error
	Commit() error
	Abort()
}

// ExecutionKind classifies whether a result creates an initial cursor lineage.
type ExecutionKind uint8

const (
	ExecutionOrdinary ExecutionKind = iota + 1
	ExecutionInitialCursor
)

// Execution is the ID-free result of one admitted tool call.
type Execution struct {
	Kind        ExecutionKind
	Result      api.Result
	Publication Publication
}

// ValidatePublication enforces the initial-cursor publication invariant before
// transport encoding or output begins.
func (execution Execution) ValidatePublication() error {
	switch execution.Kind {
	case ExecutionOrdinary:
		if execution.Publication != nil {
			return errors.New("runtime: ordinary execution has a publication")
		}
	case ExecutionInitialCursor:
		if execution.Publication == nil {
			return errors.New("runtime: initial cursor execution lacks a publication")
		}
	default:
		return errors.New("runtime: execution kind is invalid")
	}
	return nil
}
