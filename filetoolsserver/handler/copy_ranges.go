package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (h *Handler) HandleCopyRanges(ctx context.Context, req *mcp.CallToolRequest, input CopyRangesInput) (*mcp.CallToolResult, CopyRangesOutput, error) {
	pathCtx, cwdErr := h.BuildPathContext(input.CwdID)
	if cwdErr != nil {
		return toolCwdError[CopyRangesOutput](cwdErr)
	}
	output, err := h.executeSingleRangeTransfer(ctx, pathCtx, input, operationCopy)
	output.BackupDiscovery = backupDiscoveryForResults(pathCtx, output.BackupResults)
	if err != nil {
		output.Error = err.Error()
		output.ErrorCode = errorCodeFromMessage(err.Error())
		if output.ActionHint == nil {
			output.ActionHint = actionHintForRangeTransferOutput(output)
		}
		return errorResult(err.Error()), CopyRangesOutput(output), nil
	}
	return structuredResultOnly(), CopyRangesOutput(output), nil
}
