package navigation

import (
	"context"
	"math"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/present"
	"github.com/Dirard/mcp-file-tools/internal/rootfs"
	runtimepkg "github.com/Dirard/mcp-file-tools/internal/runtime"
	"github.com/Dirard/mcp-file-tools/internal/scanner"
)

func (connection *Connection) Search(ctx context.Context, raw []byte, work *runtimepkg.WorkLease) runtimepkg.Execution {
	if !connection.valid() || work == nil {
		return errorExecution(work, api.ErrorIOError)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var detail inputErrorDetail
	arguments, code := decodeSearchArguments(raw, &detail)
	if code != "" {
		return inputErrorExecution(work, code, detail)
	}
	if arguments.kind == continuationArguments {
		return connection.continueDynamic(ctx, arguments.continuation, api.ToolSearch, work)
	}
	return connection.startSearch(ctx, arguments.search, work)
}

func (connection *Connection) startSearch(ctx context.Context, initial SearchInitial, work *runtimepkg.WorkLease) runtimepkg.Execution {
	if initial.Mode < dynamicFileSearch || initial.Mode > dynamicSymbolSearch || (initial.Mode == dynamicFileSearch && initial.Glob == nil) {
		return errorExecution(work, api.ErrorInvalidInput)
	}
	if initial.Mode == dynamicSymbolSearch && connection.Service.Parser == nil {
		return errorExecution(work, api.ErrorIOError)
	}
	root, code := connection.Service.CWD.Lookup(cwdID(initial.CWDID))
	if code != "" {
		return errorExecution(work, code)
	}
	target, err := root.OpenSearchTarget(initial.Path)
	if err != nil {
		_ = root.Close()
		return errorExecution(work, rootfsErrorCode(err))
	}
	presentMode, scanMode, ok := searchModes(initial.Mode)
	if !ok {
		_ = target.Close()
		_ = root.Close()
		return errorExecution(work, api.ErrorInvalidInput)
	}
	builder, err := present.NewSearchBuilder(presentMode)
	if err != nil {
		_ = target.Close()
		_ = root.Close()
		return errorExecution(work, api.ErrorIOError)
	}
	page := &rowPage{builder: builder, mode: initial.Mode}
	request := scanner.Request{
		Tool:           api.ToolSearch,
		CWDID:          initial.CWDID,
		Mode:           scanMode,
		Root:           initial.Path,
		Depth:          math.MaxUint16,
		IncludeIgnored: initial.IncludeIgnored,
		Glob:           initial.Glob,
	}
	var matchFile func(string) bool
	if initial.Glob != nil {
		matchFile = initial.Glob.Match
	}
	pattern := patternFromInitial(initial)
	consumer, consumerErr := newSearchConsumer(
		initial.Mode,
		pattern,
		matchFile,
		target.Kind() == rootfs.SearchTargetRegular,
		connection.Service.Parser,
		work,
	)
	if consumerErr != nil {
		_ = target.Close()
		_ = root.Close()
		return errorExecution(work, api.ErrorInvalidInput)
	}
	var batch scanner.Batch
	var next *scanner.State
	switch target.Kind() {
	case rootfs.SearchTargetDirectory:
		directory, takeErr := target.TakeDir()
		if takeErr != nil {
			_ = target.Close()
			_ = root.Close()
			return errorExecution(work, rootfsErrorCode(takeErr))
		}
		seed, seedErr := scanner.NewInitialSeed(initial.Path, directory, scanner.InitialEnumerate)
		if seedErr != nil {
			_ = root.Close()
			return errorExecution(work, api.ErrorIOError)
		}
		batch, next, code = connection.Service.Scanner.AdvanceInitialPage(
			ctx,
			connection.Service.scanDeadline(ctx),
			work,
			root,
			seed,
			request,
			connection.Service.scanLimits(),
			uint64(initial.Limit),
			consumer,
			page,
		)
	case rootfs.SearchTargetRegular:
		file, takeErr := target.TakeFile()
		if takeErr != nil {
			_ = target.Close()
			_ = root.Close()
			return errorExecution(work, rootfsErrorCode(takeErr))
		}
		seed, seedErr := scanner.NewInitialFileSeed(initial.Path, file)
		if seedErr != nil {
			_ = root.Close()
			return errorExecution(work, api.ErrorIOError)
		}
		batch, next, code = connection.Service.Scanner.AdvanceInitialFilePage(
			ctx,
			connection.Service.scanDeadline(ctx),
			work,
			seed,
			request,
			connection.Service.scanLimits(),
			uint64(initial.Limit),
			consumer,
			page,
		)
	default:
		_ = target.Close()
		_ = root.Close()
		return errorExecution(work, api.ErrorIOError)
	}
	if page.err != nil {
		_ = root.Close()
		return errorExecution(work, api.ErrorIOError)
	}
	if code != "" {
		_ = root.Close()
		return errorExecution(work, code)
	}
	return connection.finishInitial(initial.Mode, api.ToolSearch, initial.CWDID, initial.Limit, "", pattern, builder, page, batch, next, root, work)
}

func searchModes(mode dynamicMode) (present.SearchMode, scanner.Mode, bool) {
	switch mode {
	case dynamicFileSearch:
		return present.SearchFile, scanner.FileSearch, true
	case dynamicTextSearch:
		return present.SearchText, scanner.TextSearch, true
	case dynamicSymbolSearch:
		return present.SearchSymbol, scanner.SymbolSearch, true
	default:
		return 0, 0, false
	}
}
