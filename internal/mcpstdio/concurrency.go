package mcpstdio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	workruntime "github.com/Dirard/mcp-file-tools/internal/runtime"
)

var errRecoveredWorkerPanic = errors.New("mcpstdio: recovered worker panic")

const protocolBusyQueueCapacity = 1

type protocolSlot struct {
	mu    sync.Mutex
	inUse bool
}

type protocolBusyQueue struct {
	responses chan []byte
	startOnce sync.Once
	closeOnce sync.Once
}

func newProtocolBusyQueue() *protocolBusyQueue {
	return &protocolBusyQueue{responses: make(chan []byte, protocolBusyQueueCapacity)}
}

func (slot *protocolSlot) tryAcquire() bool {
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.inUse {
		return false
	}
	slot.inUse = true
	return true
}

func (slot *protocolSlot) release() {
	slot.mu.Lock()
	if !slot.inUse {
		slot.mu.Unlock()
		panic("mcpstdio: protocol slot released while idle")
	}
	slot.inUse = false
	slot.mu.Unlock()
}

func (connection *stdioConnection) dispatchProtocol(decision lifecycleDecision) error {
	if !connection.protocolSlot.tryAcquire() {
		response := encodeProtocolError(decision.requestID, protocolServerBusy)
		return connection.enqueueProtocolBusy(response)
	}
	connection.protocolWorkers.Add(1)
	connection.launch(func() error {
		defer connection.protocolWorkers.Done()
		defer connection.protocolSlot.release()
		response, err := encodeLifecycleDecision(decision)
		if err != nil {
			return err
		}
		return connection.write(response)
	}, nil)
	return nil
}

func (connection *stdioConnection) enqueueProtocolBusy(response []byte) error {
	queue := connection.protocolBusy
	if queue == nil {
		panic("mcpstdio: protocol busy queue is nil")
	}
	queue.startOnce.Do(func() {
		connection.launchProtocolQueue(func() error {
			for {
				select {
				case pending, open := <-queue.responses:
					if !open {
						return nil
					}
					if err := connection.write(pending); err != nil {
						return err
					}
				case <-connection.terminalSignal():
					return nil
				case <-connection.fatal.Done():
					return workruntime.ErrInternalFatal
				}
			}
		})
	})

	select {
	case queue.responses <- response:
		return nil
	case <-connection.terminalSignal():
		return errConnectionStopped
	case <-connection.fatal.Done():
		return workruntime.ErrInternalFatal
	}
}

func (connection *stdioConnection) closeProtocolBusy() {
	if connection.protocolBusy == nil {
		return
	}
	connection.protocolBusy.closeOnce.Do(func() {
		close(connection.protocolBusy.responses)
	})
}

type toolOutputLimiter struct {
	slots chan struct{}
}

func newToolOutputLimiter(limits workruntime.Limits) (*toolOutputLimiter, error) {
	if limits.MaxConcurrent == 0 || limits.QueueMax > ^uint64(0)-limits.MaxConcurrent {
		return nil, errors.New("mcpstdio: invalid tool output capacity")
	}
	capacity := limits.MaxConcurrent + limits.QueueMax
	maxInt := int(^uint(0) >> 1)
	if capacity > uint64(maxInt) {
		return nil, errors.New("mcpstdio: tool output capacity exceeds platform range")
	}
	return &toolOutputLimiter{slots: make(chan struct{}, int(capacity))}, nil
}

func mustNewToolOutputLimiter(limits workruntime.Limits) *toolOutputLimiter {
	limiter, err := newToolOutputLimiter(limits)
	if err != nil {
		panic(err)
	}
	return limiter
}

func (limiter *toolOutputLimiter) acquire(ctx context.Context, terminal, fatal <-chan struct{}) bool {
	if limiter == nil {
		panic("mcpstdio: tool output limiter is nil")
	}
	select {
	case limiter.slots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	case <-terminal:
		return false
	case <-fatal:
		return false
	}
}

func (limiter *toolOutputLimiter) release() {
	if limiter == nil {
		panic("mcpstdio: tool output limiter is nil")
	}
	select {
	case <-limiter.slots:
	default:
		panic("mcpstdio: tool output limiter released while idle")
	}
}

func (limiter *toolOutputLimiter) active() int {
	if limiter == nil {
		return 0
	}
	return len(limiter.slots)
}

type toolResponseCompletion struct {
	response    []byte
	publication workruntime.Publication
}

func (connection *stdioConnection) writeToolCompletion(completion toolResponseCompletion) (resultErr error) {
	var transaction responseTransaction
	if err := connection.beginResponseTransaction(&transaction); err != nil {
		resultErr = err
		if completion.publication != nil {
			resultErr = combinePublicationErrors(resultErr, abortPublication(completion.publication))
		}
		return resultErr
	}
	defer transaction.end()
	return connection.writeToolCompletionInResponseTransaction(&transaction, completion)
}

func (connection *stdioConnection) writeToolCompletionInResponseTransaction(transaction *responseTransaction, completion toolResponseCompletion) (resultErr error) {
	connection.requireResponseTransaction(transaction)

	var inputCloser io.Closer
	connection.outputMu.Lock()
	defer func() {
		connection.outputMu.Unlock()
		closeConnectionInput(inputCloser)
	}()
	executed, resultErr, inputCloser := connection.runResponseTransactionLocked(func() error {
		if completion.publication == nil {
			return connection.writeLocked(completion.response)
		}
		return connection.writePublishedToolResponseLocked(completion.response, completion.publication)
	})
	if !executed && completion.publication != nil {
		resultErr = combinePublicationErrors(resultErr, abortPublication(completion.publication))
	}
	return resultErr
}

func (connection *stdioConnection) writePublishedToolResponseLocked(response []byte, publication workruntime.Publication) (resultErr error) {
	terminal := false
	defer func() {
		if recovered := recover(); recovered != nil {
			if !terminal {
				_ = abortPublication(publication)
			}
			panic(recovered)
		}
		if resultErr != nil && !terminal {
			resultErr = combinePublicationErrors(resultErr, abortPublication(publication))
		}
	}()

	if err := connection.writeAllowed(); err != nil {
		return err
	}
	if err := publication.Prepare(); err != nil {
		return fmt.Errorf("mcpstdio: prepare publication: %w", err)
	}
	if err := connection.writeLocked(response); err != nil {
		return err
	}
	if err := publication.Commit(); err != nil {
		terminal = true
		return fmt.Errorf("mcpstdio: commit publication: %w", err)
	}
	terminal = true
	return nil
}

func abortPublication(publication workruntime.Publication) (abortErr error) {
	if publication == nil {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			abortErr = errRecoveredWorkerPanic
		}
	}()
	publication.Abort()
	return nil
}

func combinePublicationErrors(errs ...error) error {
	var combined error
	for _, err := range errs {
		if err == nil {
			continue
		}
		combined = errors.Join(combined, err)
	}
	return combined
}
