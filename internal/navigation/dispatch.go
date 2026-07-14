package navigation

import (
	"context"
	"errors"
	"sync"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/catalog"
	runtimepkg "github.com/Dirard/mcp-file-tools/internal/runtime"
)

var errDispatcherInvariant = errors.New("navigation: catalog and dispatcher disagree")

type route struct {
	name api.ToolName
	call func(context.Context, api.Call, *runtimepkg.WorkLease) runtimepkg.Execution
}

type Dispatcher struct {
	routes     [4]route
	connection *Connection
	closeOnce  sync.Once
}

func NewDispatcher(connection *Connection) (*Dispatcher, error) {
	return newDispatcher(connection, api.OrderedToolNames(), catalog.Ordered())
}

func newDispatcher(connection *Connection, names [4]api.ToolName, definitions []catalog.Definition) (*Dispatcher, error) {
	expected := api.OrderedToolNames()
	if connection == nil || !connection.valid() || len(definitions) != len(expected) {
		return nil, errDispatcherInvariant
	}
	dispatcher := &Dispatcher{connection: connection}
	for index, name := range names {
		if name != expected[index] || definitions[index].Name != name {
			return nil, errDispatcherInvariant
		}
		routeCall, ok := bindRoute(connection, name)
		if !ok {
			return nil, errDispatcherInvariant
		}
		dispatcher.routes[index] = route{name: name, call: routeCall}
	}
	return dispatcher, nil
}

func bindRoute(connection *Connection, name api.ToolName) (func(context.Context, api.Call, *runtimepkg.WorkLease) runtimepkg.Execution, bool) {
	switch name {
	case api.ToolSetCWD:
		return func(ctx context.Context, call api.Call, work *runtimepkg.WorkLease) runtimepkg.Execution {
			return connection.SetCWD(ctx, call.Arguments(), work)
		}, true
	case api.ToolProject:
		return func(ctx context.Context, call api.Call, work *runtimepkg.WorkLease) runtimepkg.Execution {
			return connection.Project(ctx, call.Arguments(), work)
		}, true
	case api.ToolSearch:
		return func(ctx context.Context, call api.Call, work *runtimepkg.WorkLease) runtimepkg.Execution {
			return connection.Search(ctx, call.Arguments(), work)
		}, true
	case api.ToolRead:
		return func(ctx context.Context, call api.Call, work *runtimepkg.WorkLease) runtimepkg.Execution {
			return connection.Read(ctx, call.Arguments(), work)
		}, true
	default:
		return nil, false
	}
}

func (dispatcher *Dispatcher) Call(ctx context.Context, call api.Call, work *runtimepkg.WorkLease) runtimepkg.Execution {
	if dispatcher == nil || dispatcher.connection == nil || work == nil {
		return errorExecution(work, api.ErrorIOError)
	}
	for _, candidate := range dispatcher.routes {
		if candidate.name == call.Name() && candidate.call != nil {
			return candidate.call(ctx, call, work)
		}
	}
	return errorExecution(work, api.ErrorInvalidInput)
}

func (dispatcher *Dispatcher) Close() {
	if dispatcher == nil {
		return
	}
	dispatcher.closeOnce.Do(func() {
		if dispatcher.connection != nil && dispatcher.connection.Cursors != nil {
			_ = dispatcher.connection.Cursors.Close()
		}
	})
}
