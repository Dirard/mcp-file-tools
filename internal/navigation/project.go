package navigation

import (
	"context"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/cwd"
	"github.com/Dirard/mcp-file-tools/internal/present"
	runtimepkg "github.com/Dirard/mcp-file-tools/internal/runtime"
	"github.com/Dirard/mcp-file-tools/internal/scanner"
)

func (connection *Connection) Project(ctx context.Context, raw []byte, work *runtimepkg.WorkLease) runtimepkg.Execution {
	if !connection.valid() || work == nil {
		return errorExecution(work, api.ErrorIOError)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	arguments, code := decodeProjectArguments(raw)
	if code != "" {
		return errorExecution(work, code)
	}
	if arguments.kind == continuationArguments {
		return connection.continueDynamic(ctx, arguments.continuation, api.ToolProject, work)
	}
	return connection.startProject(ctx, arguments.project, work)
}

func (connection *Connection) startProject(ctx context.Context, initial ProjectInitial, work *runtimepkg.WorkLease) runtimepkg.Execution {
	root, code := connection.Service.CWD.Lookup(cwdID(initial.CWDID))
	if code != "" {
		return errorExecution(work, code)
	}
	directory, err := root.OpenDir(initial.Path)
	if err != nil {
		_ = root.Close()
		return errorExecution(work, rootfsErrorCode(err))
	}
	resolved := directory.ResolvedPath().String()
	mode := scanner.InitialEnumerate
	if initial.Depth == 0 {
		mode = scanner.InitialRootOnly
	}
	seed, err := scanner.NewInitialSeed(initial.Path, directory, mode)
	if err != nil {
		_ = root.Close()
		return errorExecution(work, api.ErrorIOError)
	}
	builder, err := present.NewProjectBuilder(resolved)
	if err != nil {
		_ = root.Close()
		return errorExecution(work, api.ErrorIOError)
	}
	page := &rowPage{builder: builder, mode: dynamicProject}
	batch, next, code := connection.Service.Scanner.AdvanceInitialPage(
		ctx,
		connection.Service.scanDeadline(ctx),
		work,
		root,
		seed,
		scanner.Request{
			Tool:           api.ToolProject,
			CWDID:          initial.CWDID,
			Mode:           scanner.Project,
			Root:           initial.Path,
			Depth:          uint16(initial.Depth),
			IncludeIgnored: initial.IncludeIgnored,
		},
		connection.Service.scanLimits(),
		uint64(initial.Limit),
		scanner.ConsumerFunc(projectConsumer),
		page,
	)
	if page.err != nil {
		_ = root.Close()
		return errorExecution(work, api.ErrorIOError)
	}
	if code != "" {
		_ = root.Close()
		return errorExecution(work, code)
	}
	return connection.finishInitial(dynamicProject, api.ToolProject, initial.CWDID, initial.Limit, resolved, searchPattern{}, builder, page, batch, next, root, work)
}

func cwdID(value uint64) cwd.ID {
	return cwd.ID(value)
}
