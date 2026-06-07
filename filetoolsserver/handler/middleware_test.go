package handler

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type testInput struct {
	Value string `json:"value"`
}

type testOutput struct {
	Result string `json:"result"`
}

func TestWithRecovery_NoPanic(t *testing.T) {
	handler := func(ctx context.Context, req *mcp.CallToolRequest, input testInput) (*mcp.CallToolResult, testOutput, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "success"}},
		}, testOutput{Result: "ok"}, nil
	}

	wrapped := WithRecovery(handler)
	result, output, err := wrapped(context.Background(), &mcp.CallToolRequest{}, testInput{Value: "test"})

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if result.IsError {
		t.Error("expected non-error result")
	}
	if output.Result != "ok" {
		t.Errorf("expected output 'ok', got %q", output.Result)
	}
}

func TestWithRecovery_Panic(t *testing.T) {
	const marker = "panic marker should not leak"
	handler := func(ctx context.Context, req *mcp.CallToolRequest, input testInput) (*mcp.CallToolResult, testOutput, error) {
		panic(marker)
	}

	wrapped := WithRecovery(handler)
	result, _, err := wrapped(context.Background(), &mcp.CallToolRequest{}, testInput{Value: "test"})

	if err != nil {
		t.Errorf("expected no error (panic handled via result), got %v", err)
	}
	if result == nil || !result.IsError {
		t.Error("expected error result")
	}
	if len(result.Content) != 0 {
		t.Fatalf("panic result should not duplicate plain text content, got %#v", result.Content)
	}
}

func TestWithRecovery_PanicWithNilValue(t *testing.T) {
	handler := func(ctx context.Context, req *mcp.CallToolRequest, input testInput) (*mcp.CallToolResult, testOutput, error) {
		panic(nil)
	}

	wrapped := WithRecovery(handler)
	result, _, err := wrapped(context.Background(), &mcp.CallToolRequest{}, testInput{Value: "test"})

	if err != nil {
		t.Errorf("expected no error (panic handled via result), got %v", err)
	}
	if result == nil || !result.IsError {
		t.Error("expected error result")
	}
}

func TestWithRecovery_PanicSetsStructuredReadFileError(t *testing.T) {
	const marker = "structured panic marker should not leak"
	handler := func(ctx context.Context, req *mcp.CallToolRequest, input testInput) (*mcp.CallToolResult, ReadFileOutput, error) {
		panic(marker)
	}

	wrapped := WithRecovery(handler)
	result, output, err := wrapped(context.Background(), &mcp.CallToolRequest{}, testInput{Value: "test"})

	if err != nil {
		t.Errorf("expected no error (panic handled via result), got %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected error result")
	}
	if len(result.Content) != 0 {
		t.Fatalf("panic error should not duplicate plain text content, got %#v", result.Content)
	}
	if output.Text != "" || output.Error != "internal error: panic in tool handler" || strings.Contains(output.Error, marker) {
		t.Fatalf("expected structured panic error, got %#v", output)
	}
}

func TestWithRecovery_PanicSetsStructuredPhase2Errors(t *testing.T) {
	cases := []struct {
		name  string
		check func(t *testing.T)
	}{
		{
			name: "outline_file",
			check: func(t *testing.T) {
				handler := func(ctx context.Context, req *mcp.CallToolRequest, input testInput) (*mcp.CallToolResult, OutlineFileOutput, error) {
					panic("phase2 panic marker should not leak")
				}
				result, output, err := WithRecovery(handler)(context.Background(), &mcp.CallToolRequest{}, testInput{})
				assertPhase2PanicError(t, result, err, output.Error)
				if output.Imports == nil || output.Symbols == nil || output.Sections == nil || output.Warnings == nil {
					t.Fatalf("outline_file panic output should keep arrays non-nil: %#v", output)
				}
			},
		},
		{
			name: "copy_ranges",
			check: func(t *testing.T) {
				handler := func(ctx context.Context, req *mcp.CallToolRequest, input testInput) (*mcp.CallToolResult, CopyRangesOutput, error) {
					panic("phase2 panic marker should not leak")
				}
				result, output, err := WithRecovery(handler)(context.Background(), &mcp.CallToolRequest{}, testInput{})
				assertPhase2PanicError(t, result, err, output.Error)
				if output.Ranges == nil || output.BoundaryWarnings == nil || output.Warnings == nil || output.BackupPaths == nil || output.BackupResults == nil {
					t.Fatalf("copy_ranges panic output should keep arrays non-nil: %#v", output)
				}
			},
		},
		{
			name: "move_ranges",
			check: func(t *testing.T) {
				handler := func(ctx context.Context, req *mcp.CallToolRequest, input testInput) (*mcp.CallToolResult, MoveRangesOutput, error) {
					panic("phase2 panic marker should not leak")
				}
				result, output, err := WithRecovery(handler)(context.Background(), &mcp.CallToolRequest{}, testInput{})
				assertPhase2PanicError(t, result, err, output.Error)
				if output.Ranges == nil || output.BoundaryWarnings == nil || output.Warnings == nil || output.BackupPaths == nil || output.BackupResults == nil {
					t.Fatalf("move_ranges panic output should keep arrays non-nil: %#v", output)
				}
			},
		},
		{
			name: "copy_ranges_batch",
			check: func(t *testing.T) {
				handler := func(ctx context.Context, req *mcp.CallToolRequest, input testInput) (*mcp.CallToolResult, CopyRangesBatchOutput, error) {
					panic("phase2 panic marker should not leak")
				}
				result, output, err := WithRecovery(handler)(context.Background(), &mcp.CallToolRequest{}, testInput{})
				assertPhase2PanicError(t, result, err, output.Error)
				if output.TargetResults == nil || output.TargetsWritten == nil || output.BatchWarnings == nil || output.Warnings == nil || output.BackupPaths == nil || output.BackupResults == nil {
					t.Fatalf("copy_ranges_batch panic output should keep arrays non-nil: %#v", output)
				}
			},
		},
		{
			name: "move_ranges_batch",
			check: func(t *testing.T) {
				handler := func(ctx context.Context, req *mcp.CallToolRequest, input testInput) (*mcp.CallToolResult, MoveRangesBatchOutput, error) {
					panic("phase2 panic marker should not leak")
				}
				result, output, err := WithRecovery(handler)(context.Background(), &mcp.CallToolRequest{}, testInput{})
				assertPhase2PanicError(t, result, err, output.Error)
				if output.TargetResults == nil || output.TargetsWritten == nil || output.BatchWarnings == nil || output.Warnings == nil || output.BackupPaths == nil || output.BackupResults == nil {
					t.Fatalf("move_ranges_batch panic output should keep arrays non-nil: %#v", output)
				}
			},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, tt.check)
	}
}

func assertPhase2PanicError(t *testing.T, result *mcp.CallToolResult, err error, message string) {
	t.Helper()
	if err != nil {
		t.Fatalf("expected no error (panic handled via result), got %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected error result")
	}
	if len(result.Content) != 0 {
		t.Fatalf("panic result should not duplicate plain text content, got %#v", result.Content)
	}
	if message != "internal error: panic in tool handler" || strings.Contains(message, "phase2 panic marker") {
		t.Fatalf("expected structured sanitized panic error, got %q", message)
	}
}

func TestWithRecovery_PanicDoesNotLogRawPanicValue(t *testing.T) {
	const marker = "panic marker should not be logged"
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError})))
	defer slog.SetDefault(previous)

	handler := func(ctx context.Context, req *mcp.CallToolRequest, input testInput) (*mcp.CallToolResult, ReadFileOutput, error) {
		panic(marker)
	}

	wrapped := WithRecovery(handler)
	result, output, err := wrapped(context.Background(), &mcp.CallToolRequest{}, testInput{})
	if err != nil {
		t.Errorf("expected no error (panic handled via result), got %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected error result")
	}
	if strings.Contains(output.Error, marker) {
		t.Fatalf("panic output leaked marker: %#v", output)
	}
	if strings.Contains(buf.String(), marker) {
		t.Fatalf("panic log leaked marker:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "panic_type") {
		t.Fatalf("panic log did not include panic_type:\n%s", buf.String())
	}
}

func TestWithLogging_Success(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := func(ctx context.Context, req *mcp.CallToolRequest, input testInput) (*mcp.CallToolResult, testOutput, error) {
		return &mcp.CallToolResult{}, testOutput{Result: "ok"}, nil
	}

	wrapped := WithLogging(logger, "test_tool", handler)
	_, _, _ = wrapped(context.Background(), &mcp.CallToolRequest{}, testInput{})

	logOutput := buf.String()
	if !strings.Contains(logOutput, "tool_call_start") {
		t.Error("expected tool_call_start log")
	}
	if !strings.Contains(logOutput, "tool_call_success") {
		t.Error("expected tool_call_success log")
	}
	if !strings.Contains(logOutput, "test_tool") {
		t.Error("expected tool name in log")
	}
}

func TestWithLogging_InfoLevelLogsSuccess(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	handler := func(ctx context.Context, req *mcp.CallToolRequest, input testInput) (*mcp.CallToolResult, testOutput, error) {
		return &mcp.CallToolResult{}, testOutput{Result: "ok"}, nil
	}

	wrapped := WithLogging(logger, "test_tool", handler)
	_, _, _ = wrapped(context.Background(), &mcp.CallToolRequest{}, testInput{})

	logOutput := buf.String()
	if strings.Contains(logOutput, "tool_call_start") {
		t.Error("did not expect debug start log at info level")
	}
	if !strings.Contains(logOutput, "tool_call_success") {
		t.Error("expected tool_call_success log at info level")
	}
	if !strings.Contains(logOutput, "duration") {
		t.Error("expected duration in success log")
	}
}

func TestWithLogging_ToolError(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := func(ctx context.Context, req *mcp.CallToolRequest, input testInput) (*mcp.CallToolResult, testOutput, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "something went wrong"}},
			IsError: true,
		}, testOutput{}, nil
	}

	wrapped := WithLogging(logger, "test_tool", handler)
	_, _, _ = wrapped(context.Background(), &mcp.CallToolRequest{}, testInput{})

	logOutput := buf.String()
	if !strings.Contains(logOutput, "tool_call_failed") {
		t.Error("expected tool_call_failed log")
	}
	if strings.Contains(logOutput, "something went wrong") {
		t.Error("did not expect tool error message in log")
	}
}

func TestWithLogging_StructuredToolError(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := func(ctx context.Context, req *mcp.CallToolRequest, input testInput) (*mcp.CallToolResult, ReadFileOutput, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{},
			IsError: true,
		}, ReadFileOutput{Text: "", Error: "structured failure"}, nil
	}

	wrapped := WithLogging(logger, "test_tool", handler)
	_, _, _ = wrapped(context.Background(), &mcp.CallToolRequest{}, testInput{})

	logOutput := buf.String()
	if !strings.Contains(logOutput, "tool_call_failed") {
		t.Error("expected tool_call_failed log")
	}
	if strings.Contains(logOutput, "structured failure") {
		t.Error("did not expect structured error message in log")
	}
}

func TestWithLogging_NilLogger(t *testing.T) {
	handler := func(ctx context.Context, req *mcp.CallToolRequest, input testInput) (*mcp.CallToolResult, testOutput, error) {
		return &mcp.CallToolResult{}, testOutput{Result: "ok"}, nil
	}

	// Should not panic with nil logger
	wrapped := WithLogging(nil, "test_tool", handler)
	result, output, err := wrapped(context.Background(), &mcp.CallToolRequest{}, testInput{})

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if output.Result != "ok" {
		t.Errorf("expected output 'ok', got %q", output.Result)
	}
	if result == nil {
		t.Error("expected non-nil result")
	}
}

func TestWrap_CombinesMiddleware(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := func(ctx context.Context, req *mcp.CallToolRequest, input testInput) (*mcp.CallToolResult, testOutput, error) {
		panic("test panic in wrapped handler")
	}

	wrapped := Wrap(logger, "test_tool", handler)
	result, _, err := wrapped(context.Background(), &mcp.CallToolRequest{}, testInput{})

	// Should recover from panic
	if err != nil {
		t.Errorf("expected no error (panic handled via result), got %v", err)
	}
	if result == nil || !result.IsError {
		t.Error("expected error result")
	}

	// Logging middleware sees IsError result, logs as warning
	logOutput := buf.String()
	if !strings.Contains(logOutput, "tool_call_start") {
		t.Error("expected tool_call_start log")
	}
	if !strings.Contains(logOutput, "tool_call_failed") {
		t.Error("expected tool_call_failed log")
	}
}
