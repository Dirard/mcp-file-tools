package mcpstdio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	workruntime "github.com/Dirard/mcp-file-tools/internal/runtime"
)

var errConnectionStopped = errors.New("mcpstdio: connection stopped")

type frameReadResult struct {
	frame     []byte
	available bool
	err       error
}

type framePump struct {
	results  <-chan frameReadResult
	consumed chan<- struct{}
}

type responseTransactionGate struct {
	mu      sync.Mutex
	active  sync.WaitGroup
	stopped bool
}

type responseTransaction struct {
	gate *responseTransactionGate
}

func (gate *responseTransactionGate) begin(transaction *responseTransaction) bool {
	if transaction == nil || transaction.gate != nil {
		panic("mcpstdio: invalid response transaction begin")
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.stopped {
		return false
	}
	gate.active.Add(1)
	transaction.gate = gate
	return true
}

func (transaction *responseTransaction) end() {
	if transaction == nil || transaction.gate == nil {
		panic("mcpstdio: response transaction ended more than once")
	}
	gate := transaction.gate
	transaction.gate = nil
	gate.active.Done()
}

func (gate *responseTransactionGate) stopAccepting() {
	gate.mu.Lock()
	gate.stopped = true
	gate.mu.Unlock()
}

func (gate *responseTransactionGate) stopAndWait() {
	gate.stopAccepting()
	gate.active.Wait()
}

type stdioConnection struct {
	executor    CallExecutor
	coordinator *workruntime.Coordinator
	fatal       *workruntime.FatalSignal
	frames      *frameReader
	lifecycle   *connectionLifecycle
	usedIDs     *usedIDRegistry
	output      io.Writer
	outputMu    sync.Mutex
	inputCloser io.Closer
	responses   responseTransactionGate

	terminalMu      sync.Mutex
	terminalDone    chan struct{}
	terminalStopped bool

	protocolSlot      protocolSlot
	protocolBusy      *protocolBusyQueue
	toolOutputs       *toolOutputLimiter
	toolRequestsMu    sync.Mutex
	toolRequests      map[SemanticIDKey]*toolRequest
	instrumentation   *connectionInstrumentation
	workers           sync.WaitGroup
	protocolWorkers   sync.WaitGroup
	closeExecutorOnce sync.Once
	asyncErrorMu      sync.Mutex
	asyncError        error
}

func (connection *stdioConnection) serve(ctx context.Context) (serveError error) {
	pump := connection.startFramePump()
	defer func() {
		connection.responses.stopAndWait()
		connection.stop()
		connection.closeExecutor()
		if fatalErr := connection.fatalError(); fatalErr != nil {
			serveError = fatalErr
			return
		}
		if serveError == nil {
			serveError = connection.loadAsyncError()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-connection.fatal.Done():
			return workruntime.ErrInternalFatal
		case <-connection.terminalSignal():
			return connection.loadAsyncError()
		case result, open := <-pump.results:
			if !open {
				return connection.finishEOF()
			}
			if result.err != nil {
				if fatalErr := connection.fatalError(); fatalErr != nil {
					return fatalErr
				}
				return result.err
			}
			if !result.available {
				return connection.finishEOF()
			}
			closeConnection, err := connection.handleFrame(ctx, result.frame)
			result.frame = nil
			if err != nil {
				return err
			}
			if closeConnection {
				return nil
			}
			if err := connection.acknowledgeFrame(ctx, pump.consumed); err != nil {
				return err
			}
		}
	}
}

func (connection *stdioConnection) finishEOF() error {
	connection.closeProtocolBusy()
	connection.protocolWorkers.Wait()
	if fatalErr := connection.fatalError(); fatalErr != nil {
		return fatalErr
	}
	return connection.loadAsyncError()
}

func (connection *stdioConnection) startFramePump() framePump {
	results := make(chan frameReadResult)
	consumed := make(chan struct{})
	go func() {
		defer close(results)
		panicked := connection.fatal.Run(func() {
			for {
				frame, available, err := connection.frames.next()
				result := frameReadResult{frame: frame, available: available, err: err}
				select {
				case results <- result:
				case <-connection.terminalSignal():
					return
				}
				if err != nil || !available {
					return
				}
				select {
				case <-consumed:
					result.frame = nil
					frame = nil
				case <-connection.fatal.Done():
					return
				case <-connection.terminalSignal():
					return
				}
			}
		}, connection.failAfterWorkerPanic)
		if panicked {
			connection.recordAsyncError(workruntime.ErrInternalFatal)
		}
	}()
	return framePump{results: results, consumed: consumed}
}

func (connection *stdioConnection) acknowledgeFrame(ctx context.Context, consumed chan<- struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := connection.fatalError(); err != nil {
		return err
	}
	select {
	case consumed <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-connection.fatal.Done():
		return workruntime.ErrInternalFatal
	case <-connection.terminalSignal():
		if err := connection.loadAsyncError(); err != nil {
			return err
		}
		return errConnectionStopped
	}
}

func (connection *stdioConnection) handleFrame(ctx context.Context, frame []byte) (bool, error) {
	if err := connection.fatalError(); err != nil {
		return false, err
	}
	message, err := classifyInbound(frame)
	if err != nil {
		return false, fmt.Errorf("mcpstdio: classify frame: %w", err)
	}

	switch message.kind {
	case inboundParseError, inboundInvalidRequest:
		return false, connection.write([]byte(message.output))
	case inboundResponse:
		return false, nil
	}

	admission := connection.usedIDs.admit(message)
	switch admission.kind {
	case requestIDDuplicate, requestIDExhausted:
		if err := connection.write([]byte(admission.output)); err != nil {
			return false, err
		}
		return admission.closeConnection, nil
	case requestIDIgnored, requestIDAccepted:
	}

	decision := connection.lifecycle.handle(validateInboundSchema(message))
	switch decision.action {
	case lifecycleDrop:
		return false, nil
	case lifecycleCancel:
		connection.cancelToolRequest(decision.cancellationID)
		return false, nil
	case lifecycleToolsCall:
		return false, connection.admitToolCall(ctx, decision)
	case lifecycleInitialize:
		response, encodeErr := encodeLifecycleDecision(decision)
		if encodeErr != nil {
			return false, encodeErr
		}
		return false, connection.write(response)
	default:
		return false, connection.dispatchProtocol(decision)
	}
}

func (connection *stdioConnection) write(response []byte) (resultErr error) {
	var transaction responseTransaction
	if err := connection.beginResponseTransaction(&transaction); err != nil {
		return err
	}
	defer transaction.end()
	return connection.writeInResponseTransaction(&transaction, response)
}

func (connection *stdioConnection) beginResponseTransaction(transaction *responseTransaction) error {
	if connection.responses.begin(transaction) {
		return nil
	}
	if fatalErr := connection.fatalError(); fatalErr != nil {
		return fatalErr
	}
	return errConnectionStopped
}

func (connection *stdioConnection) writeInResponseTransaction(transaction *responseTransaction, response []byte) (resultErr error) {
	connection.requireResponseTransaction(transaction)

	var inputCloser io.Closer
	connection.outputMu.Lock()
	defer func() {
		connection.outputMu.Unlock()
		closeConnectionInput(inputCloser)
	}()
	_, resultErr, inputCloser = connection.runResponseTransactionLocked(func() error {
		return connection.writeLocked(response)
	})
	return resultErr
}

func (connection *stdioConnection) requireResponseTransaction(transaction *responseTransaction) {
	if transaction == nil || transaction.gate != &connection.responses {
		panic("mcpstdio: invalid response transaction")
	}
}

func (connection *stdioConnection) runResponseTransactionLocked(operation func() error) (executed bool, resultErr error, inputCloser io.Closer) {
	if connection.fatal == nil {
		resultErr = operation()
	} else if !connection.fatal.RunBeforeFatal(func() {
		resultErr = operation()
	}) {
		connection.storeAsyncError(workruntime.ErrInternalFatal)
		return false, workruntime.ErrInternalFatal, connection.markStopped()
	}
	if resultErr != nil {
		connection.storeAsyncError(resultErr)
		inputCloser = connection.markStopped()
	}
	return true, resultErr, inputCloser
}

func (connection *stdioConnection) writeLocked(response []byte) error {
	if err := connection.writeAllowed(); err != nil {
		return err
	}

	for len(response) != 0 {
		if err := connection.writeAllowed(); err != nil {
			return err
		}
		written, err := connection.output.Write(response)
		if written < 0 || written > len(response) {
			return fmt.Errorf("mcpstdio: invalid write count %d for %d bytes", written, len(response))
		}
		response = response[written:]
		if err != nil {
			return fmt.Errorf("mcpstdio: write response: %w", err)
		}
		if written == 0 {
			return fmt.Errorf("mcpstdio: write response: %w", io.ErrShortWrite)
		}
	}
	if err := connection.writeAllowed(); err != nil {
		return err
	}
	if flusher, ok := connection.output.(interface{ Flush() error }); ok {
		if err := flusher.Flush(); err != nil {
			return fmt.Errorf("mcpstdio: flush response: %w", err)
		}
	}
	return nil
}

func (connection *stdioConnection) writeAllowed() error {
	if err := connection.fatalError(); err != nil {
		return err
	}
	connection.terminalMu.Lock()
	stopped := connection.terminalStopped
	connection.terminalMu.Unlock()
	if stopped {
		return errConnectionStopped
	}
	return nil
}

func (connection *stdioConnection) launch(operation func() error, cleanup func()) {
	connection.workers.Add(1)
	go func() {
		defer connection.workers.Done()
		connection.runWorker(operation, cleanup)
	}()
}

func (connection *stdioConnection) launchProtocolQueue(operation func() error) {
	connection.protocolWorkers.Add(1)
	go func() {
		defer connection.protocolWorkers.Done()
		connection.runWorker(operation, nil)
	}()
}

func (connection *stdioConnection) runWorker(operation func() error, cleanup func()) {
	if connection.fatal == nil {
		panic("mcpstdio: connection fatal signal is nil")
	}
	panicked := connection.fatal.Run(func() {
		if err := operation(); err != nil {
			if errors.Is(err, errRecoveredWorkerPanic) {
				panic(errRecoveredWorkerPanic)
			}
			if errors.Is(err, errConnectionStopped) {
				return
			}
			connection.recordAsyncError(err)
		}
	}, func() {
		connection.failAfterWorkerPanic()
		if cleanup != nil {
			cleanup()
		}
	})
	if panicked {
		connection.recordAsyncError(workruntime.ErrInternalFatal)
	}
}

func (connection *stdioConnection) fatalError() error {
	if connection.fatal == nil {
		return nil
	}
	if connection.fatal.Triggered() {
		return workruntime.ErrInternalFatal
	}
	return nil
}

func (connection *stdioConnection) failAfterWorkerPanic() {
	connection.cancelAllToolRequests()
}

func (connection *stdioConnection) recordAsyncError(err error) {
	if err == nil || errors.Is(err, errConnectionStopped) {
		return
	}
	connection.storeAsyncError(err)
	connection.stop()
}

func (connection *stdioConnection) storeAsyncError(err error) {
	if err == nil || errors.Is(err, errConnectionStopped) {
		return
	}
	connection.asyncErrorMu.Lock()
	if errors.Is(err, workruntime.ErrInternalFatal) {
		connection.asyncError = workruntime.ErrInternalFatal
	} else if connection.asyncError == nil {
		connection.asyncError = err
	}
	connection.asyncErrorMu.Unlock()
}

func (connection *stdioConnection) loadAsyncError() error {
	connection.asyncErrorMu.Lock()
	defer connection.asyncErrorMu.Unlock()
	return connection.asyncError
}

func (connection *stdioConnection) closeExecutor() {
	connection.closeExecutorOnce.Do(func() {
		connection.cancelAllToolRequests()
		connection.executor.Close()
	})
}

func (connection *stdioConnection) terminalSignal() <-chan struct{} {
	connection.terminalMu.Lock()
	defer connection.terminalMu.Unlock()
	if connection.terminalDone == nil {
		connection.terminalDone = make(chan struct{})
		if connection.terminalStopped {
			close(connection.terminalDone)
		}
	}
	return connection.terminalDone
}

func (connection *stdioConnection) stop() {
	closeConnectionInput(connection.markStopped())
}

func (connection *stdioConnection) markStopped() io.Closer {
	connection.responses.stopAccepting()
	connection.terminalMu.Lock()
	if connection.terminalStopped {
		connection.terminalMu.Unlock()
		return nil
	}
	connection.terminalStopped = true
	if connection.terminalDone == nil {
		connection.terminalDone = make(chan struct{})
	}
	close(connection.terminalDone)
	closer := connection.inputCloser
	connection.inputCloser = nil
	connection.terminalMu.Unlock()
	return closer
}

func closeConnectionInput(closer io.Closer) {
	if closer != nil {
		_ = closer.Close()
	}
}
