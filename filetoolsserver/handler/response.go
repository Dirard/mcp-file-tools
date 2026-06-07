package handler

import "github.com/modelcontextprotocol/go-sdk/mcp"

// Response helpers for handler operations.
// These provide consistent error and success response formatting across all handlers.

// errorResult marks the MCP call as failed without duplicating the error as
// plain-text content. Tool-specific handlers put the actionable message into
// structured output so agents can read output.error consistently.
func errorResult(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{},
		IsError: true,
	}
}

func toolError[Out any](message string) (*mcp.CallToolResult, Out, error) {
	return errorResult(message), StructuredErrorOutput[Out](message), nil
}

func toolCwdError[Out any](err *CwdError) (*mcp.CallToolResult, Out, error) {
	return errorResult(cwdErrorMessage(err)), StructuredCwdErrorOutput[Out](err), nil
}

func StructuredErrorOutput[Out any](message string) Out {
	var output Out
	setStructuredErrorOutput(&output, message)
	return output
}

func StructuredCwdErrorOutput[Out any](err *CwdError) Out {
	var output Out
	setStructuredCwdErrorOutput(&output, err)
	return output
}

func cwdErrorMessage(err *CwdError) string {
	if err == nil || err.Message == "" {
		return "cwd error"
	}
	return err.Message
}

func structuredResultOnly() *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{}}
}
