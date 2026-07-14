package navigation

import (
	"context"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/cursor"
	"github.com/Dirard/mcp-file-tools/internal/present"
	"github.com/Dirard/mcp-file-tools/internal/rootfs"
	runtimepkg "github.com/Dirard/mcp-file-tools/internal/runtime"
	"github.com/Dirard/mcp-file-tools/internal/scanner"
)

func (connection *Connection) finishInitial(
	mode dynamicMode,
	tool api.ToolName,
	cwdID uint64,
	limit uint16,
	projectPath string,
	pattern searchPattern,
	builder *present.Builder,
	page *rowPage,
	batch scanner.Batch,
	next *scanner.State,
	root *rootfs.Lease,
	work *runtimepkg.WorkLease,
) runtimepkg.Execution {
	if page == nil || page.err != nil || builder == nil || batch.Complete == (next != nil) {
		_ = closeLease(root)
		return errorExecution(work, api.ErrorIOError)
	}
	if next != nil {
		state := newTraversalState(mode, next, limit, projectPath, pattern)
		if state == nil {
			_ = closeLease(root)
			return errorExecution(work, api.ErrorIOError)
		}
		return connection.commitInitialPartial(builder, state, root, work)
	}

	warnings := warningsFromBatch(batch)
	switch builder.TrySummary(warnings) {
	case present.Fits:
		if code := closeLease(root); code != "" {
			return errorExecution(work, code)
		}
		result, err := builder.Finalize(present.Complete, "", warnings)
		if err != nil {
			return errorExecution(work, api.ErrorIOError)
		}
		return ordinary(work, result)
	case present.NextPage:
		if code := closeLease(root); code != "" {
			return errorExecution(work, code)
		}
		state := newInitialSummaryState(mode, tool, cwdID, limit, projectPath, pattern, warnings)
		if state == nil {
			return errorExecution(work, api.ErrorIOError)
		}
		return connection.commitInitialPartial(builder, state, nil, work)
	default:
		_ = closeLease(root)
		return errorExecution(work, api.ErrorRecordExceedsBudget)
	}
}

func (connection *Connection) commitInitialPartial(builder *present.Builder, state *dynamicState, root *rootfs.Lease, work *runtimepkg.WorkLease) runtimepkg.Execution {
	commit, err := connection.Cursors.CommitDynamicInitial(cursor.DynamicInitial{
		State: state,
		Root:  root,
		SummaryPlan: cursor.ReservationUse{
			Kind:  cursor.BroadSummaryFinal,
			Slots: 1,
			Bytes: summaryReservationBytes,
		},
	}, work)
	if err != nil {
		return errorExecution(work, cursor.CodeOf(err))
	}
	result, renderErr := builder.Finalize(present.Partial, present.Cursor(commit.Token.String()), nil)
	if renderErr != nil {
		commit.Publication.Abort()
		return ordinaryOwnedElsewhere(present.Error(api.ErrorIOError))
	}
	return runtimepkg.Execution{
		Kind:        runtimepkg.ExecutionInitialCursor,
		Result:      result,
		Publication: commit.Publication,
	}
}

func (connection *Connection) continueDynamic(ctx context.Context, continuation Continuation, tool api.ToolName, work *runtimepkg.WorkLease) runtimepkg.Execution {
	if ctx == nil {
		ctx = context.Background()
	}
	waiter := runtimepkg.NewWaiter(ctx.Done())
	connection.Cursors.Continue(ctx, continuation.Cursor, tool, continuation.CWDID, waiter, func(computeContext context.Context, working cursor.State, resources cursor.Resources) cursor.Outcome {
		return connection.computeDynamic(computeContext, working, resources, work)
	}, work)
	result, delivered := waiter.Await()
	if !delivered {
		return ordinaryOwnedElsewhere(present.Error(api.ErrorBudgetExceeded))
	}
	return ordinaryOwnedElsewhere(result)
}

func (connection *Connection) computeDynamic(ctx context.Context, working cursor.State, resources cursor.Resources, work *runtimepkg.WorkLease) cursor.Outcome {
	state, ok := working.(*dynamicState)
	if !ok || !state.valid() || work == nil {
		return cursor.Outcome{Result: present.Error(api.ErrorIOError)}
	}
	builder, err := builderForState(state)
	if err != nil {
		return cursor.Outcome{Result: present.Error(api.ErrorIOError)}
	}
	if state.scan == nil {
		result, renderErr := builder.Finalize(present.Complete, "", state.summary)
		if renderErr != nil {
			return cursor.Outcome{Result: present.Error(api.ErrorIOError)}
		}
		return cursor.Outcome{Result: result}
	}

	page := &rowPage{builder: builder, mode: state.mode}
	var consumer scanner.Consumer = scanner.ConsumerFunc(projectConsumer)
	if state.mode != dynamicProject {
		search, consumerErr := newSearchConsumer(
			state.mode,
			state.pattern,
			state.scan.MatchCandidateFile,
			false,
			connection.Service.Parser,
			work,
		)
		if consumerErr != nil {
			return cursor.Outcome{Result: present.Error(api.ErrorIOError)}
		}
		consumer = search
	}
	batch, next, code := connection.Service.Scanner.AdvancePage(
		ctx,
		connection.Service.scanDeadline(ctx),
		work,
		resources.Root,
		state.scan,
		uint64(state.limit),
		consumer,
		page,
	)
	if page.err != nil {
		return cursor.Outcome{Result: present.Error(api.ErrorIOError)}
	}
	if code != "" {
		return cursor.Outcome{Result: present.Error(code)}
	}
	if batch.Complete == (next != nil) {
		return cursor.Outcome{Result: present.Error(api.ErrorIOError)}
	}
	if next != nil {
		successor := newTraversalState(state.mode, next, state.limit, state.projectPath, state.pattern)
		return continuationOutcome(state, successor, builder, false)
	}

	warnings := warningsFromBatch(batch)
	switch builder.TrySummary(warnings) {
	case present.Fits:
		result, renderErr := builder.Finalize(present.Complete, "", warnings)
		if renderErr != nil {
			return cursor.Outcome{Result: present.Error(api.ErrorIOError)}
		}
		return cursor.Outcome{Result: result}
	case present.NextPage:
		successor := newSummaryState(state, warnings)
		return continuationOutcome(state, successor, builder, true)
	default:
		return cursor.Outcome{Result: present.Error(api.ErrorRecordExceedsBudget)}
	}
}

func continuationOutcome(parent, successor *dynamicState, builder *present.Builder, reserved bool) cursor.Outcome {
	if parent == nil || successor == nil || builder == nil {
		return cursor.Outcome{Result: present.Error(api.ErrorIOError)}
	}
	placeholder, err := builder.Finalize(present.Partial, present.Cursor((cursor.Token{}).String()), nil)
	if err != nil {
		return cursor.Outcome{Result: present.Error(api.ErrorIOError)}
	}
	bytes, err := cursor.SuccessorBytes(successor, placeholder)
	if err != nil {
		return cursor.Outcome{Result: present.Error(api.ErrorBudgetExceeded)}
	}
	use := cursor.ReservationUse{Kind: cursor.BroadSummaryFinal, Bytes: bytes}
	if reserved {
		if bytes > summaryReservationBytes {
			return cursor.Outcome{Result: present.Error(api.ErrorBudgetExceeded)}
		}
		use.Slots = 1
		use.MustTerminalize = true
	}
	before := parent.Digest()
	after := successor.Digest()
	return cursor.Outcome{
		Successor:   successor,
		Reservation: use,
		Progress: cursor.ProgressProof{
			Kind:   cursor.ProgressTraversal,
			Before: before,
			After:  after,
		},
		Finalize: func(child cursor.Token) (api.Result, error) {
			return builder.Finalize(present.Partial, present.Cursor(child.String()), nil)
		},
	}
}

func builderForState(state *dynamicState) (*present.Builder, error) {
	switch state.mode {
	case dynamicProject:
		return present.NewProjectBuilder(state.projectPath)
	case dynamicFileSearch:
		return present.NewSearchBuilder(present.SearchFile)
	case dynamicTextSearch:
		return present.NewSearchBuilder(present.SearchText)
	case dynamicSymbolSearch:
		return present.NewSearchBuilder(present.SearchSymbol)
	default:
		return nil, errNavigationPresentation
	}
}
