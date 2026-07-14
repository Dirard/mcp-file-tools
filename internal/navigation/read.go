package navigation

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/codeparse"
	"github.com/Dirard/mcp-file-tools/internal/cursor"
	"github.com/Dirard/mcp-file-tools/internal/navmodel"
	"github.com/Dirard/mcp-file-tools/internal/present"
	"github.com/Dirard/mcp-file-tools/internal/rootfs"
	runtimepkg "github.com/Dirard/mcp-file-tools/internal/runtime"
	"github.com/Dirard/mcp-file-tools/internal/textio"
)

var errReadRawBudget = errors.New("navigation: read raw byte budget exceeded")

type readRawBudget struct {
	maximum   uint64
	used      uint64
	exhausted bool
}

func (budget *readRawBudget) charge(bytes uint64) error {
	if budget == nil || bytes == 0 || budget.exhausted || budget.used > budget.maximum || bytes > budget.maximum-budget.used {
		if budget != nil {
			budget.exhausted = true
		}
		return errReadRawBudget
	}
	budget.used += bytes
	return nil
}

func (connection *Connection) Read(ctx context.Context, raw []byte, work *runtimepkg.WorkLease) runtimepkg.Execution {
	if !connection.valid() || work == nil {
		return errorExecution(work, api.ErrorIOError)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	arguments, code := decodeReadArguments(raw)
	if code != "" {
		return errorExecution(work, code)
	}
	if arguments.kind == continuationArguments {
		return connection.continueRead(ctx, arguments.continuation, work)
	}
	return connection.startRead(ctx, arguments.read, work)
}

func (connection *Connection) startRead(ctx context.Context, initial ReadInitial, work *runtimepkg.WorkLease) runtimepkg.Execution {
	if !initial.Mode.Valid() || len(initial.Files) == 0 || len(initial.Files) > 24 || initial.MaxBytes < 4096 || initial.MaxBytes > 32768 ||
		(initial.Mode == navmodel.ReadOutline && connection.Service.Parser == nil) {
		return errorExecution(work, api.ErrorInvalidInput)
	}
	root, code := connection.Service.CWD.Lookup(cwdID(initial.CWDID))
	if code != "" {
		return errorExecution(work, code)
	}
	deadline := connection.Service.scanDeadline(ctx)
	scanLease, outcome := connection.Service.ScanLimiter.Acquire(ctx, deadline, work)
	if outcome != runtimepkg.SubAcquired {
		_ = root.Close()
		if outcome == runtimepkg.SubCancelled {
			work.MarkNoCommit()
		}
		return errorExecution(work, api.ErrorBudgetExceeded)
	}

	rawBudget := &readRawBudget{maximum: connection.Service.Config.ScanMaxBytes}
	items, code := connection.buildReadItems(ctx, deadline, root, initial, rawBudget, work)
	rootCode := closeLease(root)
	scanLease.WorkerReturned()
	if code != "" {
		return errorExecution(work, code)
	}
	if rootCode != "" {
		return errorExecution(work, rootCode)
	}
	snapshot, err := navmodel.NewReadSnapshot(initial.Mode, items)
	if err != nil {
		return errorExecution(work, api.ErrorIOError)
	}
	plan, code := present.PlanRead(snapshot, initial.MaxBytes, connection.Service.Config.CursorMaxPages)
	if code != "" {
		return errorExecution(work, code)
	}
	if plan.PageCount() == 1 {
		page, renderErr := plan.Render(0, "")
		if renderErr != nil {
			return errorExecution(work, api.ErrorIOError)
		}
		return ordinary(work, page.Result)
	}

	shared, err := newReadShared(snapshot, plan)
	if err != nil {
		return errorExecution(work, api.ErrorIOError)
	}
	if shared.Footprint() > connection.Service.Config.CursorMaxTotalBytes {
		return errorExecution(work, api.ErrorBudgetExceeded)
	}
	state := newReadState(shared, initial.CWDID, 1)
	if state == nil {
		return errorExecution(work, api.ErrorIOError)
	}
	pagePlan, code := readPageReservation(shared, initial.CWDID)
	if code != "" {
		return errorExecution(work, code)
	}
	commit, err := connection.Cursors.CommitReadInitial(cursor.ReadInitial{
		State:    state,
		Shared:   shared,
		PagePlan: pagePlan,
	}, work)
	if err != nil {
		return errorExecution(work, cursor.CodeOf(err))
	}
	page, renderErr := plan.Render(0, present.Cursor(commit.Token.String()))
	if renderErr != nil {
		commit.Publication.Abort()
		return ordinaryOwnedElsewhere(present.Error(api.ErrorIOError))
	}
	return runtimepkg.Execution{
		Kind:        runtimepkg.ExecutionInitialCursor,
		Result:      page.Result,
		Publication: commit.Publication,
	}
}

func (connection *Connection) buildReadItems(
	ctx context.Context,
	deadline time.Time,
	root *rootfs.Lease,
	initial ReadInitial,
	rawBudget *readRawBudget,
	work *runtimepkg.WorkLease,
) ([]navmodel.ReadItem, api.ErrorCode) {
	items := make([]navmodel.ReadItem, len(initial.Files))
	var retained uint64
	for index, file := range initial.Files {
		if retained >= connection.Service.Config.CursorMaxTotalBytes {
			return nil, api.ErrorBudgetExceeded
		}
		remaining := connection.Service.Config.CursorMaxTotalBytes - retained
		var item navmodel.ReadItem
		var code api.ErrorCode
		if initial.Mode == navmodel.ReadSource {
			item, code = connection.buildSourceItem(ctx, deadline, root, uint32(index), file, rawBudget, remaining)
		} else {
			item, code = connection.buildOutlineItem(ctx, deadline, root, uint32(index), file, rawBudget, work)
		}
		if code != "" {
			return nil, code
		}
		var ok bool
		retained, ok = addReadBytes(retained, item.Footprint())
		if !ok || retained > connection.Service.Config.CursorMaxTotalBytes {
			return nil, api.ErrorBudgetExceeded
		}
		items[index] = item
	}
	return items, ""
}

func (connection *Connection) buildSourceItem(
	ctx context.Context,
	deadline time.Time,
	root *rootfs.Lease,
	index uint32,
	request ReadFile,
	rawBudget *readRawBudget,
	retainedBudget uint64,
) (navmodel.ReadItem, api.ErrorCode) {
	file, err := root.OpenRegular(request.Path)
	if err != nil {
		return readErrorItem(navmodel.ReadSource, index, rootfsErrorCode(err))
	}
	resolved := file.ResolvedPath().String()
	sink, code := textio.NewRangeSink(request.Start, request.End, retainedBudget)
	if code != "" {
		_ = file.Close()
		return navmodel.ReadItem{}, api.ErrorIOError
	}
	summary, code := textio.StreamCanonical(ctx, file, textio.Domain{ThroughLine: request.End}, textio.Budget{
		Deadline: deadline,
		Charge:   rawBudget.charge,
	}, sink)
	closeErr := file.Close()
	if code == api.ErrorBudgetExceeded {
		return navmodel.ReadItem{}, api.ErrorBudgetExceeded
	}
	if closeErr != nil {
		return readErrorItem(navmodel.ReadSource, index, api.ErrorIOError)
	}
	if code != "" {
		return readErrorItem(navmodel.ReadSource, index, code)
	}
	if code = sink.Finish(summary); code != "" {
		return readErrorItem(navmodel.ReadSource, index, code)
	}
	selected := sink.TakeLines()
	if len(selected) == 0 {
		item, itemErr := navmodel.NewReadSourceEmptyItem(index, resolved, nil)
		if itemErr != nil {
			return navmodel.ReadItem{}, api.ErrorIOError
		}
		return item, ""
	}
	var textBytes uint64
	for _, line := range selected {
		if uint64(len(line.Text)) > ^uint64(0)-textBytes {
			return navmodel.ReadItem{}, api.ErrorBudgetExceeded
		}
		textBytes += uint64(len(line.Text))
	}
	expectedFootprint, ok := navmodel.ReadSourceItemFootprint(resolved, len(selected), textBytes)
	if !ok || expectedFootprint > retainedBudget {
		return navmodel.ReadItem{}, api.ErrorBudgetExceeded
	}
	lines := make([]navmodel.ReadLine, len(selected))
	for lineIndex, line := range selected {
		converted, lineErr := navmodel.NewOwnedReadLine(line.Number, line.Text)
		if lineErr != nil {
			return navmodel.ReadItem{}, api.ErrorIOError
		}
		lines[lineIndex] = converted
	}
	item, itemErr := navmodel.NewOwnedReadSourceItem(index, resolved, lines, nil)
	if itemErr != nil {
		return navmodel.ReadItem{}, api.ErrorIOError
	}
	if item.Footprint() != expectedFootprint {
		return navmodel.ReadItem{}, api.ErrorIOError
	}
	return item, ""
}

func (connection *Connection) buildOutlineItem(
	ctx context.Context,
	deadline time.Time,
	root *rootfs.Lease,
	index uint32,
	request ReadFile,
	rawBudget *readRawBudget,
	work *runtimepkg.WorkLease,
) (navmodel.ReadItem, api.ErrorCode) {
	file, err := root.OpenRegular(request.Path)
	if err != nil {
		return readErrorItem(navmodel.ReadOutline, index, rootfsErrorCode(err))
	}
	resolved := file.ResolvedPath().String()
	language, supported := codeparse.LanguageForPath(resolved)
	if !supported {
		if file.Close() != nil {
			return readErrorItem(navmodel.ReadOutline, index, api.ErrorIOError)
		}
		return readErrorItem(navmodel.ReadOutline, index, api.ErrorUnsupportedLanguage)
	}
	buffer, code := textio.BufferCanonical(ctx, file, textio.Domain{}, textio.Budget{
		Deadline: deadline,
		Charge:   rawBudget.charge,
	}, connection.Service.Config.ParseMaxBytes)
	closeErr := file.Close()
	if code == api.ErrorBudgetExceeded && (rawBudget.exhausted || ctx.Err() != nil || !time.Now().Before(deadline)) {
		return navmodel.ReadItem{}, api.ErrorBudgetExceeded
	}
	if closeErr != nil {
		return readErrorItem(navmodel.ReadOutline, index, api.ErrorIOError)
	}
	if code != "" {
		return readErrorItem(navmodel.ReadOutline, index, code)
	}
	if len(buffer.Bytes) == 0 {
		item, itemErr := navmodel.NewReadOutlineEmptyItem(index, resolved, language, nil)
		if itemErr != nil {
			return navmodel.ReadItem{}, api.ErrorIOError
		}
		return item, ""
	}
	parsed, parseCode := connection.Service.Parser.Parse(ctx, deadline, work, codeparse.Input{
		Path:      resolved,
		Canonical: buffer.Bytes,
		SHA256:    buffer.Summary.SHA256,
		Language:  language,
	})
	if parsed.State == codeparse.CallAborted {
		if parseCode == "" {
			parseCode = api.ErrorBudgetExceeded
		}
		return navmodel.ReadItem{}, parseCode
	}
	if parseCode != "" {
		if parseCode == api.ErrorParserFailed {
			return readErrorItem(navmodel.ReadOutline, index, parseCode)
		}
		return navmodel.ReadItem{}, api.ErrorIOError
	}
	warnings := []api.WarningCode(nil)
	if parsed.State == codeparse.Recoverable {
		warnings = []api.WarningCode{api.WarningParserPartial}
	} else if parsed.State != codeparse.Clean {
		return readErrorItem(navmodel.ReadOutline, index, api.ErrorParserFailed)
	}
	if len(parsed.Records) == 0 {
		item, itemErr := navmodel.NewReadOutlineEmptyItem(index, resolved, language, warnings)
		if itemErr != nil {
			return navmodel.ReadItem{}, api.ErrorIOError
		}
		return item, ""
	}
	item, itemErr := navmodel.NewReadOutlineItem(index, resolved, language, parsed.Records, warnings)
	if itemErr != nil {
		return navmodel.ReadItem{}, api.ErrorIOError
	}
	return item, ""
}

func readErrorItem(view navmodel.ReadView, index uint32, code api.ErrorCode) (navmodel.ReadItem, api.ErrorCode) {
	item, err := navmodel.NewReadErrorItem(view, index, code, nil)
	if err != nil {
		return navmodel.ReadItem{}, api.ErrorIOError
	}
	return item, ""
}

func readPageReservation(shared *readShared, cwdID uint64) (cursor.ReadPageReservation, api.ErrorCode) {
	if shared == nil || cwdID == 0 || cwdID > maxSafeCWDID || shared.plan.PageCount() < 2 || shared.plan.PageCount() > math.MaxUint32 {
		return cursor.ReadPageReservation{}, api.ErrorInvalidInput
	}
	var reservedBytes uint64
	for pageIndex := uint64(1); pageIndex+1 < shared.plan.PageCount(); pageIndex++ {
		successor := newReadState(shared, cwdID, pageIndex+1)
		if successor == nil {
			return cursor.ReadPageReservation{}, api.ErrorIOError
		}
		page, err := shared.plan.Render(pageIndex, present.Cursor((cursor.Token{}).String()))
		if err != nil {
			return cursor.ReadPageReservation{}, api.ErrorIOError
		}
		bytes, err := cursor.SuccessorBytes(successor, page.Result)
		if err != nil {
			return cursor.ReadPageReservation{}, api.ErrorBudgetExceeded
		}
		var ok bool
		reservedBytes, ok = addReadBytes(reservedBytes, bytes)
		if !ok {
			return cursor.ReadPageReservation{}, api.ErrorBudgetExceeded
		}
	}
	pages := uint32(shared.plan.PageCount())
	return cursor.ReadPageReservation{
		Pages: pages,
		Slots: uint64(pages - 1),
		Bytes: reservedBytes,
	}, ""
}

func (connection *Connection) continueRead(ctx context.Context, continuation Continuation, work *runtimepkg.WorkLease) runtimepkg.Execution {
	waiter := runtimepkg.NewWaiter(ctx.Done())
	connection.Cursors.Continue(ctx, continuation.Cursor, api.ToolRead, continuation.CWDID, waiter, func(_ context.Context, working cursor.State, _ cursor.Resources) cursor.Outcome {
		return connection.computeRead(working)
	}, work)
	result, delivered := waiter.Await()
	if !delivered {
		return ordinaryOwnedElsewhere(present.Error(api.ErrorBudgetExceeded))
	}
	return ordinaryOwnedElsewhere(result)
}

func (connection *Connection) computeRead(working cursor.State) cursor.Outcome {
	state, ok := working.(*readState)
	if !ok || !state.valid() {
		return cursor.Outcome{Result: present.Error(api.ErrorIOError)}
	}
	pageIndex := state.page
	if pageIndex+1 == state.shared.plan.PageCount() {
		page, err := state.shared.plan.Render(pageIndex, "")
		if err != nil {
			return cursor.Outcome{Result: present.Error(api.ErrorIOError)}
		}
		return cursor.Outcome{Result: page.Result}
	}
	successor := newReadState(state.shared, state.cwdID, pageIndex+1)
	if successor == nil {
		return cursor.Outcome{Result: present.Error(api.ErrorIOError)}
	}
	placeholder, err := state.shared.plan.Render(pageIndex, present.Cursor((cursor.Token{}).String()))
	if err != nil {
		return cursor.Outcome{Result: present.Error(api.ErrorIOError)}
	}
	bytes, err := cursor.SuccessorBytes(successor, placeholder.Result)
	if err != nil {
		return cursor.Outcome{Result: present.Error(api.ErrorBudgetExceeded)}
	}
	return cursor.Outcome{
		Successor: successor,
		Reservation: cursor.ReservationUse{
			Kind:  cursor.ReadPagePlan,
			Slots: 1,
			Bytes: bytes,
		},
		Progress: cursor.ProgressProof{
			Kind:        cursor.ProgressReadItem,
			BeforeValue: pageIndex,
			AfterValue:  pageIndex + 1,
		},
		Finalize: func(child cursor.Token) (api.Result, error) {
			page, renderErr := state.shared.plan.Render(pageIndex, present.Cursor(child.String()))
			return page.Result, renderErr
		},
	}
}
