package handler

import (
	"context"
	"fmt"

	"github.com/dimitar-grigorov/mcp-file-tools/internal/config"
)

type handlerLimiters struct {
	tools      *requestLimiter
	scans      *requestLimiter
	largeReads *requestLimiter
}

func newHandlerLimiters(cfg *config.Config) handlerLimiters {
	maxToolCalls := 0
	maxScanCalls := 0
	maxLargeReadCalls := 0
	if cfg != nil {
		maxToolCalls = cfg.MaxToolCalls
		maxScanCalls = cfg.MaxScanCalls
		maxLargeReadCalls = cfg.MaxLargeReadCalls
	}
	return handlerLimiters{
		tools:      newRequestLimiter(config.MaxToolCallsOrDefault(maxToolCalls)),
		scans:      newRequestLimiter(config.MaxScanCallsOrDefault(maxScanCalls)),
		largeReads: newRequestLimiter(config.MaxLargeReadCallsOrDefault(maxLargeReadCalls)),
	}
}

type requestLimiter struct {
	permits chan struct{}
}

func newRequestLimiter(maxConcurrent int) *requestLimiter {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &requestLimiter{permits: make(chan struct{}, maxConcurrent)}
}

func (l *requestLimiter) acquire(ctx context.Context) (func(), error) {
	if l == nil {
		return func() {}, nil
	}
	select {
	case l.permits <- struct{}{}:
		return func() { <-l.permits }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (h *Handler) acquireToolCall(ctx context.Context) (func(), error) {
	return h.limiters.tools.acquire(ctx)
}

func (h *Handler) acquireScan(ctx context.Context) (func(), error) {
	return h.limiters.scans.acquire(ctx)
}

func (h *Handler) acquireLargeRead(ctx context.Context) (func(), error) {
	return h.limiters.largeReads.acquire(ctx)
}

func limiterWaitError(scope string, err error) string {
	return fmt.Sprintf("tool call cancelled while waiting for %s capacity: %v", scope, err)
}

func contextError(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
