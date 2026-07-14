package mcpstdio

import (
	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/catalog"
)

const (
	supportedProtocolVersion = "2025-11-25"
	serverImplementationName = "mcp-file-tools"
	serverImplementationDev  = "dev"
)

type lifecycleState uint8

const (
	lifecycleNew lifecycleState = iota
	lifecycleAwaitingInitialized
	lifecycleReady
)

type lifecycleAction uint8

const (
	lifecycleDrop lifecycleAction = iota + 1
	lifecycleInitialize
	lifecyclePing
	lifecycleToolsList
	lifecycleToolsCall
	lifecycleCancel
	lifecycleMethodNotFound
	lifecycleRejected
)

type initializeResult struct {
	protocolVersion string
	serverName      string
	serverVersion   string
	instructions    string
	tools           bool
}

type lifecycleDecision struct {
	action             lifecycleAction
	requestID          RequestID
	initialize         initializeResult
	call               api.Call
	cancellationID     SemanticIDKey
	cancellationReason string
	output             string
}

type connectionLifecycle struct {
	state         lifecycleState
	serverVersion string
}

func newLifecycle() *connectionLifecycle {
	return newLifecycleWithVersion(serverImplementationDev)
}

func newLifecycleWithVersion(version string) *connectionLifecycle {
	if version == "" {
		version = serverImplementationDev
	}
	return &connectionLifecycle{state: lifecycleNew, serverVersion: version}
}

func (lifecycle *connectionLifecycle) handle(schema inboundSchemaResult) lifecycleDecision {
	if schema.kind == inboundSchemaInvalid {
		if schema.requestID.RawJSON() == "" {
			return lifecycleDecision{action: lifecycleDrop}
		}
		return lifecycleDecision{
			action:    lifecycleRejected,
			requestID: schema.requestID,
			output:    schema.output,
		}
	}
	if schema.kind == inboundSchemaUnknown {
		if schema.requestID.RawJSON() == "" {
			return lifecycleDecision{action: lifecycleDrop}
		}
		return lifecycleDecision{
			action:    lifecycleMethodNotFound,
			requestID: schema.requestID,
		}
	}

	switch schema.method {
	case methodInitialize:
		if lifecycle.state != lifecycleNew {
			return rejectLifecycleRequest(schema.requestID)
		}
		lifecycle.state = lifecycleAwaitingInitialized
		return lifecycleDecision{
			action:    lifecycleInitialize,
			requestID: schema.requestID,
			initialize: initializeResult{
				protocolVersion: supportedProtocolVersion,
				serverName:      serverImplementationName,
				serverVersion:   lifecycle.serverVersion,
				instructions:    catalog.Instructions,
				tools:           true,
			},
		}
	case methodNotificationReady:
		if lifecycle.state == lifecycleAwaitingInitialized {
			lifecycle.state = lifecycleReady
		}
		return lifecycleDecision{action: lifecycleDrop}
	case methodPing:
		return lifecycleDecision{
			action:    lifecyclePing,
			requestID: schema.requestID,
		}
	case methodToolsList:
		if lifecycle.state != lifecycleReady {
			return rejectLifecycleRequest(schema.requestID)
		}
		return lifecycleDecision{
			action:    lifecycleToolsList,
			requestID: schema.requestID,
		}
	case methodToolsCall:
		if lifecycle.state != lifecycleReady {
			return rejectLifecycleRequest(schema.requestID)
		}
		return lifecycleDecision{
			action:    lifecycleToolsCall,
			requestID: schema.requestID,
			call:      schema.call,
		}
	case methodNotificationCancelled:
		return lifecycleDecision{
			action:             lifecycleCancel,
			cancellationID:     schema.cancellationID,
			cancellationReason: schema.cancellationReason,
		}
	}
	return lifecycleDecision{action: lifecycleDrop}
}

func rejectLifecycleRequest(id RequestID) lifecycleDecision {
	return lifecycleDecision{
		action:    lifecycleRejected,
		requestID: id,
		output:    invalidRequestForID(id),
	}
}
