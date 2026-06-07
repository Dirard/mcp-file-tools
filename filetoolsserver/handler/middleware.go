package handler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// WithRecovery wraps a tool handler with panic recovery.
// If a panic occurs, it returns an error result instead of crashing the server.
func WithRecovery[In, Out any](handler mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, args In) (result *mcp.CallToolResult, output Out, err error) {
		defer func() {
			if r := recover(); r != nil {
				message := "internal error: panic in tool handler"
				slog.Error("panic recovered in tool handler", "panic_type", fmt.Sprintf("%T", r))
				result = errorResult(message)
				setStructuredErrorOutput(&output, message)
			}
		}()
		return handler(ctx, req, args)
	}
}

func setStructuredErrorOutput[Out any](output *Out, message string) {
	switch value := any(output).(type) {
	case *ReadFileOutput:
		*value = ReadFileOutput{Error: message}
	case *ReadFilesOutput:
		*value = ReadFilesOutput{Error: message, Items: []ReadFilesItemOutput{}}
	case *SetCwdOutput:
		*value = SetCwdOutput{Error: message}
	case *ListDirOutput:
		*value = ListDirOutput{Error: message, Entries: []ListDirEntry{}}
	case *GlobFileSearchOutput:
		*value = GlobFileSearchOutput{Error: message, Files: []GlobFileMatch{}}
	case *GrepOutput:
		*value = GrepOutput{Error: message, Matches: []GrepMatch{}, Files: []string{}, Counts: []GrepCount{}, FileGroups: []GrepFileGroup{}}
	case *InspectPathOutput:
		*value = InspectPathOutput{Error: message}
	case *WorkspaceInventoryOutput:
		*value = WorkspaceInventoryOutput{Error: message}
	case *OutlineFileOutput:
		*value = OutlineFileOutput{
			Error:          message,
			Imports:        []OutlineItem{},
			Symbols:        []OutlineItem{},
			Sections:       []OutlineItem{},
			EnclosingItems: []OutlineItem{},
			Warnings:       []ToolWarning{},
		}
	case *ResolveSymbolRangeOutput:
		*value = ResolveSymbolRangeOutput{
			Error:            message,
			Matches:          []ResolvedSymbolMatch{},
			ResolvedRanges:   []ResolvedRange{},
			ResolutionStatus: resolveStatusFailed,
		}
	case *CopyRangesOutput:
		*value = CopyRangesOutput{
			Error:            message,
			Ranges:           []TransferRangeResult{},
			BoundaryWarnings: []BoundaryWarning{},
			Warnings:         []ToolWarning{},
			BackupPaths:      []string{},
			BackupResults:    []BackupResult{},
		}
	case *MoveRangesOutput:
		*value = MoveRangesOutput{
			Error:            message,
			Ranges:           []TransferRangeResult{},
			BoundaryWarnings: []BoundaryWarning{},
			Warnings:         []ToolWarning{},
			BackupPaths:      []string{},
			BackupResults:    []BackupResult{},
		}
	case *CopyRangesBatchOutput:
		*value = CopyRangesBatchOutput{
			Error:          message,
			TargetResults:  []BatchTargetResult{},
			TargetsWritten: []string{},
			BatchWarnings:  []ToolWarning{},
			Warnings:       []ToolWarning{},
			BackupPaths:    []string{},
			BackupResults:  []BackupResult{},
		}
	case *MoveRangesBatchOutput:
		*value = MoveRangesBatchOutput{
			Error:          message,
			TargetResults:  []BatchTargetResult{},
			TargetsWritten: []string{},
			BatchWarnings:  []ToolWarning{},
			Warnings:       []ToolWarning{},
			BackupPaths:    []string{},
			BackupResults:  []BackupResult{},
		}
	}
}

func setStructuredCwdErrorOutput[Out any](output *Out, err *CwdError) {
	message := cwdErrorMessage(err)
	setStructuredErrorOutput(output, message)
	switch value := any(output).(type) {
	case *SetCwdOutput:
		value.Error = message
		value.ErrorCode = cwdErrorCode(err)
		value.ActionHint = cwdErrorHint(err)
	case *ReadFileOutput:
		applyCwdMeta(&value.CwdOutputMeta, err)
		value.ErrorCode = cwdErrorCode(err)
		value.ActionHint = cwdErrorHint(err)
	case *ReadFilesOutput:
		applyCwdMeta(&value.CwdOutputMeta, err)
		value.ErrorCode = cwdErrorCode(err)
		value.ActionHint = cwdErrorHint(err)
	case *ListDirOutput:
		applyCwdMeta(&value.CwdOutputMeta, err)
		value.ErrorCode = cwdErrorCode(err)
		value.ActionHint = cwdErrorHint(err)
	case *GlobFileSearchOutput:
		applyCwdMeta(&value.CwdOutputMeta, err)
		value.ErrorCode = cwdErrorCode(err)
		value.ActionHint = cwdErrorHint(err)
	case *GrepOutput:
		applyCwdMeta(&value.CwdOutputMeta, err)
		value.ErrorCode = cwdErrorCode(err)
		value.ActionHint = cwdErrorHint(err)
	case *InspectPathOutput:
		applyCwdMeta(&value.CwdOutputMeta, err)
		value.ErrorCode = cwdErrorCode(err)
		value.ActionHint = cwdErrorHint(err)
	case *WorkspaceInventoryOutput:
		applyCwdMeta(&value.CwdOutputMeta, err)
		value.ErrorCode = cwdErrorCode(err)
		value.ActionHint = cwdErrorHint(err)
	case *OutlineFileOutput:
		applyCwdMeta(&value.CwdOutputMeta, err)
		value.ErrorCode = cwdErrorCode(err)
		value.ActionHint = cwdErrorHint(err)
	case *ResolveSymbolRangeOutput:
		applyCwdMeta(&value.CwdOutputMeta, err)
		value.ErrorCode = cwdErrorCode(err)
		value.ActionHint = cwdErrorHint(err)
	case *CopyRangesOutput:
		applyCwdMeta(&value.CwdOutputMeta, err)
		value.ErrorCode = cwdErrorCode(err)
		value.ActionHint = cwdErrorHint(err)
	case *MoveRangesOutput:
		applyCwdMeta(&value.CwdOutputMeta, err)
		value.ErrorCode = cwdErrorCode(err)
		value.ActionHint = cwdErrorHint(err)
	case *CopyRangesBatchOutput:
		applyCwdMeta(&value.CwdOutputMeta, err)
		value.ErrorCode = cwdErrorCode(err)
		value.ActionHint = cwdErrorHint(err)
	case *MoveRangesBatchOutput:
		applyCwdMeta(&value.CwdOutputMeta, err)
		value.ErrorCode = cwdErrorCode(err)
		value.ActionHint = cwdErrorHint(err)
	}
}

func applyCwdMeta(meta *CwdOutputMeta, err *CwdError) {
	if meta == nil {
		return
	}
	meta.ErrorCode = cwdErrorCode(err)
	meta.ActionHint = cwdErrorHint(err)
	if err == nil {
		return
	}
	if err.CwdID != nil {
		meta.CwdID = err.CwdID
	}
	if err.Cwd != "" {
		meta.Cwd = slashPath(err.Cwd)
	}
}

func cwdErrorCode(err *CwdError) string {
	if err == nil || err.Code == "" {
		return "cwd_error"
	}
	return err.Code
}

func cwdErrorHint(err *CwdError) *ActionHint {
	if err != nil && err.Hint != nil {
		return err.Hint
	}
	return &ActionHint{SafeToRetry: false, Reason: "cwd error"}
}

// WithLogging wraps a tool handler with request/response logging.
// Logs tool name, duration, and any errors.
func WithLogging[In, Out any](logger *slog.Logger, toolName string, handler mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	if logger == nil {
		return handler
	}
	return func(ctx context.Context, req *mcp.CallToolRequest, args In) (*mcp.CallToolResult, Out, error) {
		start := time.Now()
		logger.Debug("tool_call_start", "tool", toolName)

		result, output, err := handler(ctx, req, args)
		duration := time.Since(start)

		if err != nil {
			logger.Error("tool_call_error", "tool", toolName, "duration", duration, "error_type", fmt.Sprintf("%T", err))
		} else if result != nil && result.IsError {
			logger.Warn("tool_call_failed", "tool", toolName, "duration", duration)
		} else {
			logger.Info("tool_call_success", "tool", toolName, "duration", duration)
		}

		return result, output, err
	}
}

// Wrap applies recovery and optional logging to a tool handler.
// This is the main entry point for wrapping handlers.
func Wrap[In, Out any](logger *slog.Logger, toolName string, handler mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	// Apply recovery first (outermost), then logging
	wrapped := WithRecovery(handler)
	if logger != nil {
		wrapped = WithLogging(logger, toolName, wrapped)
	}
	return wrapped
}
