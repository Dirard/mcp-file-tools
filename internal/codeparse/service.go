package codeparse

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/config"
	workruntime "github.com/Dirard/mcp-file-tools/internal/runtime"
)

type parserBoundary func(api.Language, []byte) parseOutput

// Service validates fresh canonical snapshots, applies process-wide parser
// admission, and publishes compact immutable projections.
type Service struct {
	config  config.Runtime
	cache   *Cache
	limiter *workruntime.SubLimiter
	parse   parserBoundary
}

func NewService(cfg config.Runtime, cache *Cache, limiter *workruntime.SubLimiter) *Service {
	if cache == nil || limiter == nil || cfg.ParseMaxBytes == 0 || cfg.ParseMaxCalls == 0 {
		return nil
	}
	return &Service{config: cfg, cache: cache, limiter: limiter, parse: parseCanonical}
}

func (service *Service) Parse(ctx context.Context, deadline time.Time, parent *workruntime.WorkLease, input Input) (Result, api.ErrorCode) {
	if service == nil || service.cache == nil || service.limiter == nil || service.parse == nil || ctx == nil || deadline.IsZero() || parent == nil {
		return Result{}, api.ErrorInvalidInput
	}
	if aborted, code := callAborted(ctx, deadline); aborted {
		parent.MarkNoCommit()
		return Result{Language: input.Language, State: CallAborted}, code
	}
	if input.Path == "" || !input.Language.Valid() {
		return Result{}, api.ErrorInvalidInput
	}
	pathLanguage, supported := LanguageForPath(input.Path)
	if !supported || pathLanguage != input.Language {
		return Result{}, api.ErrorInvalidInput
	}
	if uint64(len(input.Canonical)) > service.config.ParseMaxBytes {
		return Result{}, api.ErrorBudgetExceeded
	}
	if sha256.Sum256(input.Canonical) != input.SHA256 {
		return Result{}, api.ErrorInvalidInput
	}

	key := cacheKeyFor(input)
	if cached, hit := service.cache.get(key); hit {
		if aborted, code := callAborted(ctx, deadline); aborted {
			parent.MarkNoCommit()
			return Result{Language: input.Language, State: CallAborted}, code
		}
		return cached, ""
	}

	lease, outcome := service.limiter.Acquire(ctx, deadline, parent)
	if outcome != workruntime.SubAcquired {
		parent.MarkNoCommit()
		code := api.ErrorCode("")
		if outcome == workruntime.SubDeadlineExceeded || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = api.ErrorBudgetExceeded
		}
		return Result{Language: input.Language, State: CallAborted}, code
	}

	source := bytes.Clone(input.Canonical)
	parser := service.parse
	finished := make(chan parseOutput, 1)
	go func() {
		parsed := callParserSafely(parser, input.Language, source)
		lease.WorkerReturned()
		finished <- parsed
	}()

	timer := time.NewTimer(time.Until(deadline))
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()

	select {
	case parsed := <-finished:
		if aborted, code := callAborted(ctx, deadline); aborted {
			parent.MarkNoCommit()
			return Result{Language: input.Language, State: CallAborted}, code
		}
		result, code := classifyParse(input.Language, parsed)
		if code != "" {
			return result, code
		}
		if aborted, abortedCode := callAborted(ctx, deadline); aborted {
			parent.MarkNoCommit()
			return Result{Language: input.Language, State: CallAborted}, abortedCode
		}
		service.cache.put(key, result)
		return result, ""
	case <-ctx.Done():
		lease.MarkNoCommit()
		parent.MarkNoCommit()
		_, code := callAborted(ctx, deadline)
		return Result{Language: input.Language, State: CallAborted}, code
	case <-timer.C:
		lease.MarkNoCommit()
		parent.MarkNoCommit()
		return Result{Language: input.Language, State: CallAborted}, api.ErrorBudgetExceeded
	}
}

func callParserSafely(parser parserBoundary, language api.Language, source []byte) (output parseOutput) {
	defer func() {
		if recover() != nil {
			output = parseOutput{fatal: true}
		}
	}()
	return parser(language, source)
}

func parseCanonical(language api.Language, source []byte) parseOutput {
	switch language {
	case api.LanguageGo:
		return parseGo(source)
	case api.LanguageMarkdown:
		return parseMarkdown(source)
	default:
		return parseTreeSitter(language, source)
	}
}

func classifyParse(language api.Language, parsed parseOutput) (Result, api.ErrorCode) {
	if parsed.fatal {
		return Result{Language: language, State: Fatal}, api.ErrorParserFailed
	}
	state := Clean
	records := parsed.records
	if len(parsed.errorRanges) != 0 {
		for _, errorRange := range parsed.errorRanges {
			if !errorRange.Valid() {
				return Result{Language: language, State: Fatal}, api.ErrorParserFailed
			}
		}
		state = Recoverable
		records = filterUnsafeRecords(records, parsed.errorRanges)
	}
	projected, ok := projectRecords(records)
	if !ok {
		return Result{Language: language, State: Fatal}, api.ErrorParserFailed
	}
	result, ok := cloneResult(Result{Language: language, State: state, Records: projected})
	if !ok {
		return Result{Language: language, State: Fatal}, api.ErrorParserFailed
	}
	return result, ""
}

func callAborted(ctx context.Context, deadline time.Time) (bool, api.ErrorCode) {
	if !time.Now().Before(deadline) {
		return true, api.ErrorBudgetExceeded
	}
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return true, api.ErrorBudgetExceeded
		}
		return true, ""
	}
	return false, ""
}
