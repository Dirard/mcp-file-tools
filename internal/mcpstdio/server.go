package mcpstdio

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/Dirard/mcp-file-tools/internal/api"
	workruntime "github.com/Dirard/mcp-file-tools/internal/runtime"
)

// CallExecutor is the only boundary between transport-owned admission and tool work.
type CallExecutor interface {
	Call(ctx context.Context, call api.Call, work *workruntime.WorkLease) workruntime.Execution
	Close()
}

// GateFactory creates one isolated call executor for one stdio connection.
type GateFactory interface {
	NewConnection() (CallExecutor, error)
}

// Server serves strict newline-delimited MCP connections through an executor factory.
type Server struct {
	Limits  workruntime.Limits
	Factory GateFactory
	Version string
}

// NewServer constructs a strict MCP stdio server.
func NewServer(limits workruntime.Limits, factory GateFactory) *Server {
	return &Server{Limits: limits, Factory: factory, Version: serverImplementationDev}
}

// Serve owns one connection until clean EOF or a terminal transport error.
func (server *Server) Serve(ctx context.Context, input io.Reader, output io.Writer) error {
	if server == nil || server.Factory == nil {
		return errors.New("mcpstdio: missing executor factory")
	}
	if ctx == nil || input == nil || output == nil {
		return errors.New("mcpstdio: invalid connection arguments")
	}
	if server.Limits.MaxConcurrent == 0 || server.Limits.QueueMax != 0 && server.Limits.QueueTimeout <= 0 {
		return errors.New("mcpstdio: invalid admission limits")
	}
	toolOutputs, err := newToolOutputLimiter(server.Limits)
	if err != nil {
		return err
	}

	executor, err := server.Factory.NewConnection()
	if err != nil {
		return fmt.Errorf("mcpstdio: create connection executor: %w", err)
	}
	if executor == nil {
		return errors.New("mcpstdio: executor factory returned nil")
	}
	fatal := workruntime.NewFatalSignal()
	connection := stdioConnection{
		executor:     executor,
		coordinator:  workruntime.NewCoordinatorWithFatal(server.Limits, fatal),
		fatal:        fatal,
		frames:       newFrameReader(input),
		lifecycle:    newLifecycleWithVersion(server.Version),
		usedIDs:      newUsedIDRegistry(),
		output:       output,
		protocolBusy: newProtocolBusyQueue(),
		toolOutputs:  toolOutputs,
		toolRequests: make(map[SemanticIDKey]*toolRequest),
	}
	if closer, ok := input.(io.Closer); ok {
		connection.inputCloser = closer
	}
	return connection.serve(ctx)
}
