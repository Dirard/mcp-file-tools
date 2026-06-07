package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (h *Handler) HandleMoveRanges(ctx context.Context, req *mcp.CallToolRequest, input MoveRangesInput) (*mcp.CallToolResult, MoveRangesOutput, error) {
	pathCtx, cwdErr := h.BuildPathContext(input.CwdID)
	if cwdErr != nil {
		return toolCwdError[MoveRangesOutput](cwdErr)
	}
	output, err := h.executeSingleRangeTransfer(ctx, pathCtx, CopyRangesInput(input), operationMove)
	output.BackupDiscovery = backupDiscoveryForResults(pathCtx, output.BackupResults)
	if err != nil {
		output.Error = err.Error()
		output.ErrorCode = errorCodeFromMessage(err.Error())
		if output.ActionHint == nil {
			output.ActionHint = actionHintForRangeTransferOutput(output)
		}
		return errorResult(err.Error()), MoveRangesOutput(output), nil
	}
	return structuredResultOnly(), MoveRangesOutput(output), nil
}
