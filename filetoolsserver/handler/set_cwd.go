package handler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (h *Handler) HandleSetCwd(ctx context.Context, req *mcp.CallToolRequest, input SetCwdInput) (*mcp.CallToolResult, SetCwdOutput, error) {
	if input.invalid != "" {
		return setCwdToolError("invalid_input", input.invalid)
	}
	if strings.TrimSpace(input.Directory) == "" {
		return setCwdToolError("invalid_directory", "directory is required")
	}
	if !isAbsoluteToolPath(input.Directory) {
		return setCwdToolError("invalid_directory", "directory must be an absolute path for this server OS")
	}
	v := h.ResolvePath(input.Directory)
	if !v.Ok() {
		return setCwdToolError("invalid_directory", "directory cannot be resolved")
	}
	info, err := os.Stat(v.Path)
	if err != nil {
		return setCwdToolError("invalid_directory", "directory cannot be accessed")
	}
	if !info.IsDir() {
		return setCwdToolError("invalid_directory", "directory must point to an existing directory")
	}
	resolved := v.Path
	display := h.displayResolvedPath(input.Directory, v.Path)
	if evaluated, err := filepath.EvalSymlinks(v.Path); err == nil {
		resolved = filepath.Clean(evaluated)
	}
	id, cwdErr := h.cwdRegistry.register(ctx, resolved, display)
	if cwdErr != nil {
		return setCwdError(cwdErr)
	}
	return structuredResultOnly(), SetCwdOutput{CwdID: id}, nil
}

func setCwdToolError(code, message string) (*mcp.CallToolResult, SetCwdOutput, error) {
	return setCwdError(&CwdError{
		Code:    code,
		Message: message,
		Hint: &ActionHint{
			SafeToRetry: false,
			Reason:      message,
		},
	})
}

func setCwdError(err *CwdError) (*mcp.CallToolResult, SetCwdOutput, error) {
	if err == nil {
		err = &CwdError{Code: "cwd_error", Message: "cwd error"}
	}
	output := SetCwdOutput{
		Error:      err.Message,
		ErrorCode:  err.Code,
		ActionHint: err.Hint,
	}
	if output.ActionHint == nil {
		output.ActionHint = &ActionHint{SafeToRetry: false, Reason: fmt.Sprintf("cwd error %s", err.Code)}
	}
	return errorResult(err.Message), output, nil
}
