package mcpstdio

import (
	"context"
	"io"

	workruntime "github.com/Dirard/mcp-file-tools/internal/runtime"
)

var (
	_ CallExecutor = (*fakeCallExecutor)(nil)
	_ GateFactory  = (*fakeExecutorFactory)(nil)
	_ interface {
		Serve(context.Context, io.Reader, io.Writer) error
	} = (*Server)(nil)
	_ func(workruntime.Limits, GateFactory) *Server = NewServer
)
