package scanner

import (
	"container/heap"
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"github.com/Dirard/mcp-file-tools/internal/rootfs"
	runtimepkg "github.com/Dirard/mcp-file-tools/internal/runtime"
)

var (
	errInvalidSeed       = errors.New("scanner: invalid initial seed")
	errConsumedSeed      = errors.New("scanner: initial seed already consumed")
	errEnumerationBudget = errors.New("scanner: enumeration budget exceeded")
)

type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}

// InitialMode closes the two directory-start transitions.
type InitialMode uint8

const (
	InitialEnumerate InitialMode = iota + 1
	InitialRootOnly
)

// InitialSeed privately owns one verified directory until exactly one initial call.
type InitialSeed struct {
	_         noCopy
	mu        sync.Mutex
	requested pathspec.Relative
	resolved  pathspec.Relative
	identity  rootfs.Identity
	mode      InitialMode
	directory *rootfs.Dir
	consumed  bool
}

// InitialFileSeed privately owns one verified regular file until exactly one initial call.
type InitialFileSeed struct {
	_         noCopy
	mu        sync.Mutex
	requested pathspec.Relative
	resolved  pathspec.Relative
	identity  rootfs.Identity
	file      *rootfs.File
	consumed  bool
}

type directorySeedValue struct {
	requested pathspec.Relative
	resolved  pathspec.Relative
	identity  rootfs.Identity
	mode      InitialMode
	directory *rootfs.Dir
}

type fileSeedValue struct {
	requested pathspec.Relative
	resolved  pathspec.Relative
	identity  rootfs.Identity
	file      *rootfs.File
}

// Service owns the process-wide scan sub-limiter.
type Service struct {
	scanLimiter *runtimepkg.SubLimiter
}

func NewService(scanLimiter *runtimepkg.SubLimiter) *Service {
	if scanLimiter == nil {
		panic("scanner: scan limiter is nil")
	}
	return &Service{scanLimiter: scanLimiter}
}

// NewInitialSeed takes ownership of directory on entry, including failures.
func NewInitialSeed(requested pathspec.Relative, directory *rootfs.Dir, mode InitialMode) (*InitialSeed, error) {
	if directory == nil {
		return nil, errInvalidSeed
	}
	resolved := directory.ResolvedPath()
	if (mode != InitialEnumerate && mode != InitialRootOnly) || !validSeedPaths(requested, resolved) {
		_ = directory.Close()
		return nil, errInvalidSeed
	}
	return &InitialSeed{
		requested: requested,
		resolved:  resolved,
		identity:  directory.Identity(),
		mode:      mode,
		directory: directory,
	}, nil
}

// NewInitialFileSeed takes ownership of file on entry, including failures.
func NewInitialFileSeed(requested pathspec.Relative, file *rootfs.File) (*InitialFileSeed, error) {
	if file == nil {
		return nil, errInvalidSeed
	}
	resolved := file.ResolvedPath()
	if !validSeedPaths(requested, resolved) {
		_ = file.Close()
		return nil, errInvalidSeed
	}
	return &InitialFileSeed{
		requested: requested,
		resolved:  resolved,
		identity:  file.Identity(),
		file:      file,
	}, nil
}

func validSeedPaths(requested, resolved pathspec.Relative) bool {
	return requested.Target() != 0 && requested.String() != "" && resolved.Target() == requested.Target() && resolved.String() != ""
}

func (seed *InitialSeed) take() (directorySeedValue, error) {
	if seed == nil {
		return directorySeedValue{}, errInvalidSeed
	}
	seed.mu.Lock()
	defer seed.mu.Unlock()
	if seed.consumed || seed.directory == nil {
		return directorySeedValue{}, errConsumedSeed
	}
	value := directorySeedValue{
		requested: seed.requested,
		resolved:  seed.resolved,
		identity:  seed.identity,
		mode:      seed.mode,
		directory: seed.directory,
	}
	seed.directory = nil
	seed.consumed = true
	return value, nil
}

func (seed *InitialFileSeed) take() (fileSeedValue, error) {
	if seed == nil {
		return fileSeedValue{}, errInvalidSeed
	}
	seed.mu.Lock()
	defer seed.mu.Unlock()
	if seed.consumed || seed.file == nil {
		return fileSeedValue{}, errConsumedSeed
	}
	value := fileSeedValue{
		requested: seed.requested,
		resolved:  seed.resolved,
		identity:  seed.identity,
		file:      seed.file,
	}
	seed.file = nil
	seed.consumed = true
	return value, nil
}

// AdvanceInitial consumes and closes the requested directory before returning.
func (service *Service) AdvanceInitial(
	ctx context.Context,
	deadline time.Time,
	parent *runtimepkg.WorkLease,
	root rootfs.Borrowed,
	seed *InitialSeed,
	request Request,
	limits Limits,
	rows uint64,
	consumer Consumer,
) (batch Batch, next *State, code api.ErrorCode) {
	return service.advanceInitial(ctx, deadline, parent, root, seed, request, limits, rows, consumer, nil)
}

// AdvanceInitialPage is AdvanceInitial with exact caller-owned row fitting.
func (service *Service) AdvanceInitialPage(
	ctx context.Context,
	deadline time.Time,
	parent *runtimepkg.WorkLease,
	root rootfs.Borrowed,
	seed *InitialSeed,
	request Request,
	limits Limits,
	rows uint64,
	consumer Consumer,
	page RowPage,
) (batch Batch, next *State, code api.ErrorCode) {
	if page == nil {
		return Batch{}, nil, api.ErrorInvalidInput
	}
	return service.advanceInitial(ctx, deadline, parent, root, seed, request, limits, rows, consumer, page)
}

func (service *Service) advanceInitial(
	ctx context.Context,
	deadline time.Time,
	parent *runtimepkg.WorkLease,
	root rootfs.Borrowed,
	seed *InitialSeed,
	request Request,
	limits Limits,
	rows uint64,
	consumer Consumer,
	page RowPage,
) (batch Batch, next *State, code api.ErrorCode) {
	value, seedErr := seed.take()
	if seedErr != nil {
		return Batch{}, nil, api.ErrorInvalidInput
	}
	defer func() {
		if closeErr := value.directory.Close(); closeErr != nil && code == "" {
			batch, next, code = Batch{}, nil, api.ErrorIOError
		}
	}()
	if !validAdvanceInputs(service, ctx, deadline, parent, rows, consumer) || !validDirectorySeedRequest(value, request) {
		return Batch{}, nil, api.ErrorInvalidInput
	}
	working, stateCode := newState(request, limits)
	if stateCode != "" {
		return Batch{}, nil, stateCode
	}
	working.request.Root = value.resolved
	lease, acquireCode := service.acquire(ctx, deadline, parent)
	if acquireCode != "" {
		return Batch{}, nil, acquireCode
	}
	defer lease.WorkerReturned()
	if callTerminal(ctx, deadline) {
		return Batch{}, nil, api.ErrorBudgetExceeded
	}
	if !working.incrementDir() {
		return Batch{}, nil, api.ErrorBudgetExceeded
	}
	rootCandidate := working.candidate(value.resolved, rootfs.EntryDir, value.identity, 0, deadline)
	if resultCode := working.applyConsumeResult(consumer.Consume(ctx, rootCandidate, nil), rootCandidate); resultCode != "" {
		return Batch{}, nil, resultCode
	}
	if value.mode == InitialRootOnly {
		return service.advanceWorking(ctx, deadline, nil, working, rows, consumer, true, page)
	}
	if enumerationCode := working.enumerateDirectory(ctx, deadline, value.directory, value.resolved.String(), 0, consumer); enumerationCode != "" {
		return Batch{}, nil, enumerationCode
	}
	return service.advanceWorking(ctx, deadline, root, working, rows, consumer, true, page)
}

// AdvanceInitialFile consumes and closes one already-open verified regular file.
func (service *Service) AdvanceInitialFile(
	ctx context.Context,
	deadline time.Time,
	parent *runtimepkg.WorkLease,
	seed *InitialFileSeed,
	request Request,
	limits Limits,
	rows uint64,
	consumer Consumer,
) (batch Batch, next *State, code api.ErrorCode) {
	return service.advanceInitialFile(ctx, deadline, parent, seed, request, limits, rows, consumer, nil)
}

// AdvanceInitialFilePage is AdvanceInitialFile with exact caller-owned row fitting.
func (service *Service) AdvanceInitialFilePage(
	ctx context.Context,
	deadline time.Time,
	parent *runtimepkg.WorkLease,
	seed *InitialFileSeed,
	request Request,
	limits Limits,
	rows uint64,
	consumer Consumer,
	page RowPage,
) (batch Batch, next *State, code api.ErrorCode) {
	if page == nil {
		return Batch{}, nil, api.ErrorInvalidInput
	}
	return service.advanceInitialFile(ctx, deadline, parent, seed, request, limits, rows, consumer, page)
}

func (service *Service) advanceInitialFile(
	ctx context.Context,
	deadline time.Time,
	parent *runtimepkg.WorkLease,
	seed *InitialFileSeed,
	request Request,
	limits Limits,
	rows uint64,
	consumer Consumer,
	page RowPage,
) (batch Batch, next *State, code api.ErrorCode) {
	value, seedErr := seed.take()
	if seedErr != nil {
		return Batch{}, nil, api.ErrorInvalidInput
	}
	defer func() {
		if closeErr := value.file.Close(); closeErr != nil && code == "" {
			batch, next, code = Batch{}, nil, api.ErrorIOError
		}
	}()
	if !validAdvanceInputs(service, ctx, deadline, parent, rows, consumer) || !validFileSeedRequest(value, request) {
		return Batch{}, nil, api.ErrorInvalidInput
	}
	working, stateCode := newState(request, limits)
	if stateCode != "" {
		return Batch{}, nil, stateCode
	}
	working.request.Root = value.resolved
	lease, acquireCode := service.acquire(ctx, deadline, parent)
	if acquireCode != "" {
		return Batch{}, nil, acquireCode
	}
	defer lease.WorkerReturned()
	if callTerminal(ctx, deadline) {
		return Batch{}, nil, api.ErrorBudgetExceeded
	}
	if !working.incrementFile() {
		return Batch{}, nil, api.ErrorBudgetExceeded
	}
	candidate := working.candidate(value.resolved, rootfs.EntryFile, value.identity, 0, deadline)
	if resultCode := working.applyConsumeResult(consumer.Consume(ctx, candidate, value.file), candidate); resultCode != "" {
		return Batch{}, nil, resultCode
	}
	return service.advanceWorking(ctx, deadline, nil, working, rows, consumer, true, page)
}

// Advance computes one functional continuation without mutating current.
func (service *Service) Advance(
	ctx context.Context,
	deadline time.Time,
	parent *runtimepkg.WorkLease,
	root rootfs.Borrowed,
	current *State,
	rows uint64,
	consumer Consumer,
) (Batch, *State, api.ErrorCode) {
	return service.advance(ctx, deadline, parent, root, current, rows, consumer, nil)
}

// AdvancePage is Advance with exact caller-owned row fitting.
func (service *Service) AdvancePage(
	ctx context.Context,
	deadline time.Time,
	parent *runtimepkg.WorkLease,
	root rootfs.Borrowed,
	current *State,
	rows uint64,
	consumer Consumer,
	page RowPage,
) (Batch, *State, api.ErrorCode) {
	if page == nil {
		return Batch{}, nil, api.ErrorInvalidInput
	}
	return service.advance(ctx, deadline, parent, root, current, rows, consumer, page)
}

func (service *Service) advance(
	ctx context.Context,
	deadline time.Time,
	parent *runtimepkg.WorkLease,
	root rootfs.Borrowed,
	current *State,
	rows uint64,
	consumer Consumer,
	page RowPage,
) (Batch, *State, api.ErrorCode) {
	if !validAdvanceInputs(service, ctx, deadline, parent, rows, consumer) || current == nil || !current.valid() {
		return Batch{}, nil, api.ErrorInvalidInput
	}
	working := current.Clone()
	lease, acquireCode := service.acquire(ctx, deadline, parent)
	if acquireCode != "" {
		return Batch{}, nil, acquireCode
	}
	defer lease.WorkerReturned()
	return service.advanceWorking(ctx, deadline, root, working, rows, consumer, false, page)
}

func validAdvanceInputs(service *Service, ctx context.Context, deadline time.Time, parent *runtimepkg.WorkLease, rows uint64, consumer Consumer) bool {
	maxInt := uint64(^uint(0) >> 1)
	return service != nil && service.scanLimiter != nil && ctx != nil && !deadline.IsZero() && parent != nil && consumer != nil && rows > 0 && rows <= maxInt
}

func validDirectorySeedRequest(seed directorySeedValue, request Request) bool {
	if request.Root.Target() != seed.requested.Target() || request.Root.String() != seed.requested.String() {
		return false
	}
	switch seed.mode {
	case InitialRootOnly:
		return request.Mode == Project && request.Tool == api.ToolProject && request.Depth == 0
	case InitialEnumerate:
		return !(request.Mode == Project && request.Depth == 0)
	default:
		return false
	}
}

func validFileSeedRequest(seed fileSeedValue, request Request) bool {
	return request.Mode != Project && request.Tool == api.ToolSearch && request.Root.Target() == seed.requested.Target() && request.Root.String() == seed.requested.String()
}

func (service *Service) acquire(ctx context.Context, deadline time.Time, parent *runtimepkg.WorkLease) (*runtimepkg.SubLease, api.ErrorCode) {
	lease, outcome := service.scanLimiter.Acquire(ctx, deadline, parent)
	if outcome != runtimepkg.SubAcquired {
		return nil, api.ErrorBudgetExceeded
	}
	return lease, ""
}

func (service *Service) advanceWorking(
	ctx context.Context,
	deadline time.Time,
	root rootfs.Borrowed,
	working *State,
	rowLimit uint64,
	consumer Consumer,
	progressed bool,
	page RowPage,
) (Batch, *State, api.ErrorCode) {
	output := make([]Row, 0, int(rowLimit))
	for {
		for len(working.pending) != 0 && uint64(len(output)) < rowLimit {
			row := working.pending[0]
			if page != nil {
				switch page.Try(row) {
				case RowFits:
					page.Commit(row)
				case RowNextPage:
					if len(output) == 0 {
						return Batch{}, nil, api.ErrorIOError
					}
					working.pending = cloneRows(working.pending)
					return working.batch(output, false), working, ""
				case RowIntrinsicOverflow:
					if len(output) == 0 {
						return Batch{}, nil, api.ErrorRecordExceedsBudget
					}
					working.pending = cloneRows(working.pending)
					return working.batch(output, false), working, ""
				default:
					return Batch{}, nil, api.ErrorIOError
				}
			}
			output = append(output, row)
			working.pending[0] = Row{}
			working.pending = working.pending[1:]
			progressed = true
		}
		if len(working.pending) != 0 {
			working.pending = cloneRows(working.pending)
			return working.batch(output, false), working, ""
		}
		if len(working.frontier) == 0 {
			return working.batch(output, true), nil, ""
		}
		if callTerminal(ctx, deadline) {
			if !progressed {
				return Batch{}, nil, api.ErrorBudgetExceeded
			}
			working.pending = cloneRows(working.pending)
			return working.batch(output, false), working, ""
		}
		unitProgress, processCode := working.processNext(ctx, deadline, root, consumer)
		progressed = progressed || unitProgress
		if processCode != "" {
			return Batch{}, nil, processCode
		}
	}
}

func callTerminal(ctx context.Context, deadline time.Time) bool {
	return ctx.Err() != nil || !time.Now().Before(deadline)
}

func (state *State) batch(rows []Row, complete bool) Batch {
	batch := Batch{
		Rows:     rows,
		Counters: state.counters,
		Complete: complete,
	}
	if complete {
		batch.Warnings = state.warnings.Summaries()
	}
	return batch
}

func (state *State) processNext(ctx context.Context, deadline time.Time, root rootfs.Borrowed, consumer Consumer) (bool, api.ErrorCode) {
	if root == nil {
		return false, api.ErrorIOError
	}
	unit := heap.Pop(&state.frontier).(scanUnit)
	switch unit.kind {
	case rootfs.EntryDir:
		directory, err := root.OpenDir(unit.path)
		if err != nil {
			return state.recordOpenWarning(unit.path.String(), err)
		}
		return state.processDirectory(ctx, deadline, directory, unit, consumer)
	case rootfs.EntryFile:
		if selector, ok := consumer.(CandidateSelector); ok && !selector.SelectCandidate(unit.path, rootfs.EntryFile) {
			return true, ""
		}
		file, err := root.OpenRegular(unit.path)
		if err != nil {
			return state.recordOpenWarning(unit.path.String(), err)
		}
		return state.processFile(ctx, deadline, file, unit, consumer)
	default:
		return true, api.ErrorIOError
	}
}

func (state *State) processDirectory(ctx context.Context, deadline time.Time, directory *rootfs.Dir, unit scanUnit, consumer Consumer) (progress bool, code api.ErrorCode) {
	defer func() {
		if closeErr := directory.Close(); closeErr != nil && code == "" {
			code = api.ErrorIOError
		}
	}()
	if unit.identityKnown && directory.Identity() != unit.identity {
		return state.addWarning(unit.path.String(), api.WarningSourceChangedSkipped)
	}
	if !state.incrementDir() {
		return true, api.ErrorBudgetExceeded
	}
	candidate := state.candidate(directory.ResolvedPath(), rootfs.EntryDir, directory.Identity(), unit.depth, deadline)
	if candidate.Path.Target() != unit.path.Target() {
		return true, api.ErrorCursorExpired
	}
	if resultCode := state.applyConsumeResult(consumer.Consume(ctx, candidate, nil), candidate); resultCode != "" {
		return true, resultCode
	}
	if unit.depth < state.request.Depth {
		if enumerationCode := state.enumerateDirectory(ctx, deadline, directory, candidate.Path.String(), unit.depth, consumer); enumerationCode != "" {
			return true, enumerationCode
		}
	}
	return true, ""
}

func (state *State) processFile(ctx context.Context, deadline time.Time, file *rootfs.File, unit scanUnit, consumer Consumer) (progress bool, code api.ErrorCode) {
	defer func() {
		if closeErr := file.Close(); closeErr != nil && code == "" {
			code = api.ErrorIOError
		}
	}()
	if unit.identityKnown && file.Identity() != unit.identity {
		return state.addWarning(unit.path.String(), api.WarningSourceChangedSkipped)
	}
	if !state.incrementFile() {
		return true, api.ErrorBudgetExceeded
	}
	candidate := state.candidate(file.ResolvedPath(), rootfs.EntryFile, file.Identity(), unit.depth, deadline)
	if candidate.Path.Target() != unit.path.Target() {
		return true, api.ErrorCursorExpired
	}
	if resultCode := state.applyConsumeResult(consumer.Consume(ctx, candidate, file), candidate); resultCode != "" {
		return true, resultCode
	}
	return true, ""
}

func (state *State) candidate(path pathspec.Relative, kind rootfs.EntryKind, identity rootfs.Identity, depth uint16, deadline time.Time) Candidate {
	contentRemaining := uint64(0)
	usedContent := state.counters.DirectoryBytes + state.counters.ContentBytes
	if usedContent >= state.counters.DirectoryBytes && usedContent < state.limits.MaxBytes {
		contentRemaining = state.limits.MaxBytes - usedContent
	}
	parserRemaining := uint64(0)
	if state.counters.ParserBytes < state.limits.MaxParserBytes {
		parserRemaining = state.limits.MaxParserBytes - state.counters.ParserBytes
	}
	retainedRemaining := uint64(0)
	retained := state.dynamicFootprint()
	if retained < state.limits.FrontierMaxBytes {
		retainedRemaining = state.limits.FrontierMaxBytes - retained
	}
	return Candidate{
		Path:                   path,
		Kind:                   kind,
		Identity:               identity,
		Depth:                  depth,
		ContentBytesRemaining:  contentRemaining,
		ParserBytesRemaining:   parserRemaining,
		RetainedBytesRemaining: retainedRemaining,
		Deadline:               deadline,
	}
}

func (state *State) applyConsumeResult(result ConsumeResult, candidate Candidate) api.ErrorCode {
	if !state.chargeContent(result.ContentBytes) || !state.chargeParser(result.ParserBytes) {
		return api.ErrorBudgetExceeded
	}
	if result.Code != "" {
		if !result.Code.Valid() {
			return api.ErrorIOError
		}
		return result.Code
	}
	if result.Warning != "" {
		if !result.Warning.Valid() {
			return api.ErrorIOError
		}
		if _, warningCode := state.addWarning(candidate.Path.String(), result.Warning); warningCode != "" {
			return warningCode
		}
	}
	if len(result.Rows) == 0 {
		return ""
	}
	if len(state.pending) != 0 {
		return api.ErrorIOError
	}
	retained := state.dynamicFootprint()
	rowsBytes := rowsRetainedBytes(result.Rows)
	if retained > state.limits.FrontierMaxBytes || rowsBytes > state.limits.FrontierMaxBytes-retained {
		return api.ErrorRecordExceedsBudget
	}
	rows := cloneRows(result.Rows)
	for _, row := range rows {
		if !validRowForMode(row, state.request.Mode, candidate.Path.String()) {
			return api.ErrorIOError
		}
	}
	sort.Slice(rows, func(left, right int) bool { return compareRows(rows[left], rows[right]) < 0 })
	write := 0
	for _, row := range rows {
		if write != 0 && compareRows(rows[write-1], row) == 0 {
			continue
		}
		rows[write] = row
		write++
	}
	if write != len(rows) {
		compact := make([]Row, write)
		copy(compact, rows[:write])
		rows = compact
	} else {
		rows = rows[:write:write]
	}
	if state.frontier.retainedBytes()+rowsRetainedBytes(rows) > state.limits.FrontierMaxBytes {
		return api.ErrorRecordExceedsBudget
	}
	state.pending = rows
	return ""
}

func (state *State) enumerateDirectory(ctx context.Context, deadline time.Time, directory *rootfs.Dir, directoryPath string, parentDepth uint16, consumer Consumer) api.ErrorCode {
	temporary := make(frontier, 0)
	temporaryPathBytes := uint64(0)
	baseDynamicBytes := state.dynamicFootprint()
	callbackCode := api.ErrorCode("")
	err := directory.ReadEntries(ctx, func(bytes uint64) error {
		if !state.chargeDirectory(bytes) {
			callbackCode = api.ErrorBudgetExceeded
			return errEnumerationBudget
		}
		return nil
	}, func(outcome rootfs.EnumerationOutcome) error {
		unit, keep, outcomeCode := state.unitFromOutcome(outcome, parentDepth)
		if outcomeCode != "" {
			callbackCode = outcomeCode
			return errEnumerationBudget
		}
		if !keep {
			return nil
		}
		if selector, ok := consumer.(CandidateSelector); ok && !selector.SelectCandidate(unit.path, unit.kind) {
			return nil
		}
		pathBytes := relativeRetainedBytes(unit.path)
		prospectiveCapacity := cap(temporary)
		if len(temporary) == cap(temporary) {
			if prospectiveCapacity == 0 {
				prospectiveCapacity = 1
			} else if prospectiveCapacity <= math.MaxInt/2 {
				prospectiveCapacity *= 2
			} else {
				callbackCode = api.ErrorBudgetExceeded
				return errEnumerationBudget
			}
		}
		prospective := uint64(prospectiveCapacity)*uint64(unsafe.Sizeof(scanUnit{})) + temporaryPathBytes + pathBytes
		if baseDynamicBytes+prospective > state.limits.FrontierMaxBytes {
			callbackCode = api.ErrorBudgetExceeded
			return errEnumerationBudget
		}
		if len(temporary) == cap(temporary) {
			grown := make(frontier, len(temporary), prospectiveCapacity)
			copy(grown, temporary)
			temporary = grown
		}
		temporary = append(temporary, unit)
		temporaryPathBytes += pathBytes
		return nil
	})
	if err != nil {
		if callbackCode != "" {
			return callbackCode
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || callTerminal(ctx, deadline) {
			return api.ErrorBudgetExceeded
		}
		_, warningCode := state.addWarning(directoryPath, api.WarningUnreadableSkipped)
		return warningCode
	}
	combined := make(frontier, len(state.frontier)+len(temporary))
	copy(combined, state.frontier)
	copy(combined[len(state.frontier):], temporary)
	if combined.retainedBytes()+rowsRetainedBytes(state.pending) > state.limits.FrontierMaxBytes {
		return api.ErrorBudgetExceeded
	}
	state.frontier = combined
	heap.Init(&state.frontier)
	return ""
}

func (state *State) unitFromOutcome(outcome rootfs.EnumerationOutcome, parentDepth uint16) (scanUnit, bool, api.ErrorCode) {
	switch outcome.Disposition() {
	case rootfs.EnumerationCandidate:
		entry, ok := outcome.Candidate()
		if !ok {
			return scanUnit{}, false, api.ErrorIOError
		}
		path := entry.Path.String()
		basename := path
		if slash := strings.LastIndexByte(path, '/'); slash >= 0 {
			basename = path[slash+1:]
		}
		if state.ignore.skip(basename, entry.Kind, state.request.IncludeIgnored) {
			return scanUnit{}, false, ""
		}
		switch entry.Kind {
		case rootfs.EntryFile, rootfs.EntryDir:
			return scanUnit{
				path:          entry.Path,
				kind:          entry.Kind,
				identity:      entry.Identity,
				identityKnown: entry.IdentityKnown,
				depth:         parentDepth + 1,
			}, true, ""
		case rootfs.EntrySymlink:
			_, code := state.addWarning(path, api.WarningSymlinkSkipped)
			return scanUnit{}, false, code
		case rootfs.EntrySpecial:
			_, code := state.addWarning(path, api.WarningSpecialFileSkipped)
			return scanUnit{}, false, code
		case rootfs.EntryBoundary:
			_, code := state.addWarning(path, api.WarningMountSkipped)
			return scanUnit{}, false, code
		default:
			return scanUnit{}, false, api.ErrorIOError
		}
	case rootfs.EnumerationPathEncodingUnsupported:
		_, code := state.addWarning("", api.WarningPathEncodingUnsupported)
		return scanUnit{}, false, code
	case rootfs.EnumerationUnaddressable:
		_, code := state.addWarning("", api.WarningUnaddressablePathSkipped)
		return scanUnit{}, false, code
	case rootfs.EnumerationSourceChanged:
		_, code := state.addWarning("", api.WarningSourceChangedSkipped)
		return scanUnit{}, false, code
	case rootfs.EnumerationUnreadable:
		_, code := state.addWarning("", api.WarningUnreadableSkipped)
		return scanUnit{}, false, code
	default:
		return scanUnit{}, false, api.ErrorIOError
	}
}

func (state *State) recordOpenWarning(path string, err error) (bool, api.ErrorCode) {
	code := api.WarningUnreadableSkipped
	switch {
	case errors.Is(err, rootfs.ErrSourceChanged), errors.Is(err, rootfs.ErrNotFound), errors.Is(err, rootfs.ErrWrongTargetKind):
		code = api.WarningSourceChangedSkipped
	case errors.Is(err, rootfs.ErrSymlink):
		code = api.WarningSymlinkSkipped
	case errors.Is(err, rootfs.ErrMountBoundary):
		code = api.WarningMountSkipped
	case errors.Is(err, rootfs.ErrSpecial), errors.Is(err, rootfs.ErrNotDirectory), errors.Is(err, rootfs.ErrNotRegular):
		code = api.WarningSpecialFileSkipped
	}
	return state.addWarning(path, code)
}

func (state *State) addWarning(path string, code api.WarningCode) (bool, api.ErrorCode) {
	if err := state.warnings.AddCandidate(path, code); err != nil {
		return true, api.ErrorBudgetExceeded
	}
	return true, ""
}

func (state *State) incrementFile() bool {
	if state.counters.Files >= state.limits.MaxFiles {
		return false
	}
	state.counters.Files++
	return true
}

func (state *State) incrementDir() bool {
	if state.counters.Dirs >= state.limits.MaxDirs {
		return false
	}
	state.counters.Dirs++
	return true
}

func (state *State) chargeDirectory(bytes uint64) bool {
	if state.counters.DirectoryBytes > math.MaxUint64-bytes {
		return false
	}
	total := state.counters.DirectoryBytes + state.counters.ContentBytes
	if total > state.limits.MaxBytes || bytes > state.limits.MaxBytes-total {
		return false
	}
	state.counters.DirectoryBytes += bytes
	return true
}

func (state *State) chargeContent(bytes uint64) bool {
	if state.counters.ContentBytes > math.MaxUint64-bytes {
		return false
	}
	total := state.counters.DirectoryBytes + state.counters.ContentBytes
	if total < state.counters.DirectoryBytes || total > state.limits.MaxBytes || bytes > state.limits.MaxBytes-total {
		return false
	}
	state.counters.ContentBytes += bytes
	return true
}

func (state *State) chargeParser(bytes uint64) bool {
	if state.counters.ParserBytes > state.limits.MaxParserBytes || bytes > state.limits.MaxParserBytes-state.counters.ParserBytes {
		return false
	}
	state.counters.ParserBytes += bytes
	return true
}
