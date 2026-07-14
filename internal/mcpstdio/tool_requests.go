package mcpstdio

import (
	"context"
	"fmt"
	"sync"

	"github.com/Dirard/mcp-file-tools/internal/api"
	workruntime "github.com/Dirard/mcp-file-tools/internal/runtime"
)

type toolRequest struct {
	mu          sync.Mutex
	id          RequestID
	call        api.Call
	reservation workruntime.Reservation
	cancelled   bool
	responding  bool
}

func (request *toolRequest) cancel() bool {
	request.mu.Lock()
	defer request.mu.Unlock()
	if request.cancelled || request.responding {
		return false
	}
	request.cancelled = true
	request.reservation.Cancel()
	return true
}

func (request *toolRequest) claimResponse() bool {
	request.mu.Lock()
	defer request.mu.Unlock()
	if request.cancelled || request.responding {
		return false
	}
	request.responding = true
	return true
}

func (request *toolRequest) claimTimeoutResponse() bool {
	request.mu.Lock()
	defer request.mu.Unlock()
	if request.responding {
		return false
	}
	request.responding = true
	return true
}

func (request *toolRequest) cancelledBeforeResponse() bool {
	request.mu.Lock()
	defer request.mu.Unlock()
	return request.cancelled
}

func (connection *stdioConnection) admitToolCall(ctx context.Context, decision lifecycleDecision) error {
	if connection.instrumentation != nil {
		connection.instrumentation.admissionAttempts.Add(1)
	}
	reservation, outcome := connection.coordinator.Admit(
		ctx,
		[]byte(decision.requestID.SemanticKey().encoded),
	)
	if outcome == workruntime.AdmitImmediateBudgetExceeded {
		response, err := encodeToolResult(decision.requestID, budgetExceededResult())
		if err != nil {
			return err
		}
		return connection.write(response)
	}
	if reservation == nil {
		return fmt.Errorf("mcpstdio: admitted tool call has no reservation")
	}
	if !connection.toolOutputs.acquire(
		ctx,
		connection.terminalSignal(),
		connection.fatal.Done(),
	) {
		reservation.Cancel()
		return nil
	}

	request := &toolRequest{
		id:          decision.requestID,
		call:        decision.call,
		reservation: reservation,
	}
	connection.registerToolRequest(request)
	if connection.instrumentation != nil {
		connection.instrumentation.workerTasks.Add(1)
	}
	connection.launch(func() error { return connection.runToolRequest(request) }, func() {
		request.cancel()
		connection.removeToolRequest(request)
	})
	return nil
}

func (connection *stdioConnection) runToolRequest(request *toolRequest) error {
	defer connection.toolOutputs.release()

	work, outcome := request.reservation.Start()
	switch outcome.Kind {
	case workruntime.StartCancelled:
		connection.removeToolRequest(request)
		return nil
	case workruntime.StartQueueTimeoutBudgetExceeded:
		if outcome.ResponseRight == nil {
			connection.removeToolRequest(request)
			return fmt.Errorf("mcpstdio: queue timeout has no response right")
		}
		response, err := encodeToolResult(request.id, budgetExceededResult())
		if err != nil {
			connection.removeToolRequest(request)
			return err
		}
		var transaction responseTransaction
		if err := connection.beginResponseTransaction(&transaction); err != nil {
			connection.removeToolRequest(request)
			return err
		}
		defer transaction.end()
		if !request.claimTimeoutResponse() {
			connection.removeToolRequest(request)
			return nil
		}
		connection.instrumentation.recordResponseClaim(true)
		connection.removeToolRequest(request)
		return connection.writeInResponseTransaction(&transaction, response)
	case workruntime.StartRun:
		if work == nil {
			return fmt.Errorf("mcpstdio: runnable reservation has no work lease")
		}
	default:
		return fmt.Errorf("mcpstdio: unknown reservation start outcome %d", outcome.Kind)
	}

	if connection.instrumentation != nil {
		connection.instrumentation.recordWorkLease(work)
	}
	execution := connection.executor.Call(request.reservation.Context(), request.call, work)
	if request.cancelledBeforeResponse() {
		connection.removeToolRequest(request)
		abortErr := abortPublication(execution.Publication)
		return combinePublicationErrors(nil, abortErr)
	}
	if err := execution.ValidatePublication(); err != nil {
		_ = abortPublication(execution.Publication)
		panic("mcpstdio: invalid execution publication contract")
	}
	response, err := encodeToolResult(request.id, execution.Result)
	if err != nil {
		connection.removeToolRequest(request)
		abortErr := abortPublication(execution.Publication)
		return combinePublicationErrors(err, abortErr)
	}
	var transaction responseTransaction
	if err := connection.beginResponseTransaction(&transaction); err != nil {
		connection.removeToolRequest(request)
		abortErr := abortPublication(execution.Publication)
		return combinePublicationErrors(err, abortErr)
	}
	defer transaction.end()
	if !request.claimResponse() {
		connection.removeToolRequest(request)
		abortErr := abortPublication(execution.Publication)
		return combinePublicationErrors(nil, abortErr)
	}
	connection.instrumentation.recordResponseClaim(false)
	connection.removeToolRequest(request)
	return connection.writeToolCompletionInResponseTransaction(&transaction, toolResponseCompletion{
		response:    response,
		publication: execution.Publication,
	})
}

func budgetExceededResult() api.Result {
	return api.Navigation("ERROR\tbudget_exceeded\n", true)
}

func (connection *stdioConnection) registerToolRequest(request *toolRequest) {
	key := request.id.SemanticKey()
	connection.toolRequestsMu.Lock()
	if _, exists := connection.toolRequests[key]; exists {
		connection.toolRequestsMu.Unlock()
		panic("mcpstdio: duplicate admitted request key")
	}
	connection.toolRequests[key] = request
	connection.toolRequestsMu.Unlock()
}

func (connection *stdioConnection) removeToolRequest(request *toolRequest) {
	key := request.id.SemanticKey()
	connection.toolRequestsMu.Lock()
	if connection.toolRequests[key] == request {
		delete(connection.toolRequests, key)
	}
	connection.toolRequestsMu.Unlock()
}

func (connection *stdioConnection) cancelToolRequest(key SemanticIDKey) {
	connection.toolRequestsMu.Lock()
	request := connection.toolRequests[key]
	connection.toolRequestsMu.Unlock()
	if request != nil {
		if request.cancel() {
			connection.removeToolRequest(request)
		}
	}
}

func (connection *stdioConnection) cancelAllToolRequests() {
	connection.toolRequestsMu.Lock()
	requests := make([]*toolRequest, 0, len(connection.toolRequests))
	for _, request := range connection.toolRequests {
		requests = append(requests, request)
	}
	connection.toolRequestsMu.Unlock()
	for _, request := range requests {
		if request.cancel() {
			connection.removeToolRequest(request)
		}
	}
}
