package navigation

import (
	"context"
	"path/filepath"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/jsonwire"
	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"github.com/Dirard/mcp-file-tools/internal/rootfs"
	runtimepkg "github.com/Dirard/mcp-file-tools/internal/runtime"
)

func (connection *Connection) SetCWD(ctx context.Context, raw []byte, work *runtimepkg.WorkLease) runtimepkg.Execution {
	if !connection.valid() || work == nil {
		return errorExecution(work, api.ErrorIOError)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		work.MarkNoCommit()
		return errorExecution(work, api.ErrorBudgetExceeded)
	}
	var detail inputErrorDetail
	directory, code := decodeSetCWDArguments(raw, &detail)
	if code != "" {
		return inputErrorExecution(work, code, detail)
	}
	root, err := rootfs.OpenRoot(directory)
	if err != nil {
		return errorExecution(work, rootfsErrorCode(err))
	}
	id, _, code := connection.Service.CWD.Register(root)
	if code != "" {
		return errorExecution(work, code)
	}
	return ordinary(work, api.SetCWD(uint64(id)))
}

func decodeSetCWDArguments(raw []byte, detail *inputErrorDetail) (pathspec.RootDirectory, api.ErrorCode) {
	object, err := jsonwire.ScanObject(raw, toolArgumentLimits, jsonwire.ToolArguments)
	if err != nil || len(object.Members()) != 1 {
		return pathspec.RootDirectory{}, api.ErrorInvalidInput
	}
	member, present := object.Member("directory")
	if !present || member.Kind != jsonwire.String {
		return pathspec.RootDirectory{}, api.ErrorInvalidInput
	}
	directory, ok := decodeStringMember(object, "directory", true)
	if !ok {
		return pathspec.RootDirectory{}, api.ErrorInvalidInput
	}
	parsed, code := pathspec.ParseRootDirectory(hostTarget(), directory)
	if code == api.ErrorInvalidInput && !filepath.IsAbs(directory) {
		detail.set("directory", "absolute_path_required")
	}
	return parsed, code
}
