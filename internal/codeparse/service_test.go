package codeparse

import (
	"context"
	"crypto/sha256"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/config"
	"github.com/Dirard/mcp-file-tools/internal/navmodel"
	workruntime "github.com/Dirard/mcp-file-tools/internal/runtime"
)

func TestServiceRequiresProcessOwnedDependenciesAndValidatesInputBeforeParsing(t *testing.T) {
	cfg := config.DefaultRuntime()
	cache := NewCache(cfg.ParserCacheMaxEntries, cfg.ParserCacheMaxBytes)
	limiter := workruntime.NewSubLimiter(1)
	if NewService(cfg, cache, nil) != nil || NewService(cfg, nil, limiter) != nil {
		t.Fatal("constructor accepted a nil process-owned dependency")
	}
	service := NewService(cfg, cache, limiter)
	if service == nil {
		t.Fatal("valid service rejected")
	}
	parent := testWorkLease(t, context.Background(), "validation")
	input := cacheTestInput("main.go", "package main\nfunc Main() {}\n")

	badDigest := input
	badDigest.SHA256[0] ^= 0xff
	if result, code := service.Parse(context.Background(), time.Now().Add(time.Second), parent, badDigest); code != api.ErrorInvalidInput || result.State != 0 {
		t.Fatalf("digest mismatch = %#v,%q", result, code)
	}
	badLanguage := input
	badLanguage.Language = api.LanguageJavaScript
	if result, code := service.Parse(context.Background(), time.Now().Add(time.Second), parent, badLanguage); code != api.ErrorInvalidInput || result.State != 0 {
		t.Fatalf("language mismatch = %#v,%q", result, code)
	}
}

func TestServiceEnforcesByteBudgetAndClassifiesParserStates(t *testing.T) {
	input := cacheTestInput("main.go", "package main\nfunc Safe() {}\n")
	cfg := config.DefaultRuntime()
	cfg.ParseMaxBytes = uint64(len(input.Canonical))
	service := NewService(cfg, NewCache(0, 0), workruntime.NewSubLimiter(1))
	parent := testWorkLease(t, context.Background(), "states")

	clean, code := service.Parse(context.Background(), time.Now().Add(time.Second), parent, input)
	if code != "" || clean.State != Clean || !hasSymbolRecord(clean.Records, api.KindFunction, "Safe") {
		t.Fatalf("clean parse = %#v,%q", clean, code)
	}

	over := input
	over.Canonical = append(append([]byte(nil), input.Canonical...), ' ')
	over.SHA256 = sha256.Sum256(over.Canonical)
	if result, overCode := service.Parse(context.Background(), time.Now().Add(time.Second), parent, over); overCode != api.ErrorBudgetExceeded || result.State != 0 {
		t.Fatalf("over-budget parse = %#v,%q", result, overCode)
	}

	malformed := cacheTestInput("broken.go", "package main\nfunc Safe() {}\nfunc Broken( {\n")
	cfg.ParseMaxBytes = uint64(len(malformed.Canonical))
	recoverableService := NewService(cfg, NewCache(0, 0), workruntime.NewSubLimiter(1))
	recoverable, recoverableCode := recoverableService.Parse(context.Background(), time.Now().Add(time.Second), parent, malformed)
	if recoverableCode != "" || recoverable.State != Recoverable || !hasSymbolRecord(recoverable.Records, api.KindFunction, "Safe") {
		t.Fatalf("recoverable parse = %#v,%q", recoverable, recoverableCode)
	}

	fatal, fatalCode := classifyParse(api.LanguageJavaScript, parseOutput{fatal: true})
	if fatalCode != api.ErrorParserFailed || fatal.State != Fatal || len(fatal.Records) != 0 {
		t.Fatalf("fatal parse = %#v,%q", fatal, fatalCode)
	}
}

func TestServiceCacheHitDoesNotAcquireParseLimiter(t *testing.T) {
	cfg := config.DefaultRuntime()
	limiter := workruntime.NewSubLimiter(1)
	cache := NewCache(8, 1<<20)
	service := NewService(cfg, cache, limiter)
	input := cacheTestInput("main.go", "package main\nfunc Main() {}\n")
	firstParent := testWorkLease(t, context.Background(), "cache-fill")
	first, code := service.Parse(context.Background(), time.Now().Add(time.Second), firstParent, input)
	if code != "" || first.State != Clean {
		t.Fatalf("cache fill = %#v,%q", first, code)
	}

	holdParent := testWorkLease(t, context.Background(), "cache-hold")
	hold, outcome := limiter.Acquire(context.Background(), time.Now().Add(time.Second), holdParent)
	if outcome != workruntime.SubAcquired || hold == nil {
		t.Fatalf("cannot occupy parse limiter: %p,%d", hold, outcome)
	}
	defer hold.WorkerReturned()

	hitParent := testWorkLease(t, context.Background(), "cache-hit")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	hit, hitCode := service.Parse(ctx, time.Now().Add(time.Second), hitParent, input)
	if hitCode != "" || hit.State != Clean || !hasSymbolRecord(hit.Records, api.KindFunction, "Main") {
		t.Fatalf("cache hit waited for parser slot: %#v,%q", hit, hitCode)
	}

	missParent := testWorkLease(t, context.Background(), "cache-miss-wait")
	missInput := cacheTestInput("other.go", "package other\n")
	miss, missCode := service.Parse(context.Background(), time.Now().Add(20*time.Millisecond), missParent, missInput)
	if miss.State != CallAborted || missCode != api.ErrorBudgetExceeded || len(miss.Records) != 0 {
		t.Fatalf("cache miss limiter deadline = %#v,%q", miss, missCode)
	}
}

func TestServiceCancellationReturnsBeforeUncooperativeParserAndRetainsSlot(t *testing.T) {
	cfg := config.DefaultRuntime()
	limiter := workruntime.NewSubLimiter(1)
	service := NewService(cfg, NewCache(0, 0), limiter)
	started := make(chan struct{})
	release := make(chan struct{})
	service.parse = func(api.Language, []byte) parseOutput {
		close(started)
		<-release
		return parseOutput{records: []rawRecord{{kind: "function", lineRange: navmodel.Range{Start: 1, End: 1}, name: "Late"}}}
	}

	ctx, cancel := context.WithCancel(context.Background())
	parent := testWorkLease(t, ctx, "cancel-running")
	input := cacheTestInput("main.go", "package main")
	type response struct {
		result Result
		code   api.ErrorCode
	}
	responseReady := make(chan response, 1)
	go func() {
		result, code := service.Parse(ctx, time.Now().Add(time.Second), parent, input)
		responseReady <- response{result: result, code: code}
	}()
	<-started
	cancel()
	select {
	case got := <-responseReady:
		if got.result.State != CallAborted || got.code != "" || len(got.result.Records) != 0 {
			t.Fatalf("cancelled parse = %#v,%q", got.result, got.code)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled parse did not return")
	}

	secondParent := testWorkLease(t, context.Background(), "cancel-waiter")
	if lease, outcome := limiter.Acquire(context.Background(), time.Now().Add(20*time.Millisecond), secondParent); outcome != workruntime.SubDeadlineExceeded || lease != nil {
		t.Fatalf("uncooperative worker released slot early: %p,%d", lease, outcome)
	}
	close(release)
	lease, outcome := limiter.Acquire(context.Background(), time.Now().Add(time.Second), secondParent)
	if outcome != workruntime.SubAcquired || lease == nil {
		t.Fatalf("parser slot did not release on worker return: %p,%d", lease, outcome)
	}
	lease.WorkerReturned()
}

func TestServiceRecoversParserPanicAndNeverCachesFatalResult(t *testing.T) {
	cfg := config.DefaultRuntime()
	service := NewService(cfg, NewCache(8, 1<<20), workruntime.NewSubLimiter(1))
	var calls atomic.Uint64
	service.parse = func(api.Language, []byte) parseOutput {
		calls.Add(1)
		panic("parser failure")
	}
	parent := testWorkLease(t, context.Background(), "panic")
	input := cacheTestInput("main.go", "package main")
	for index := 0; index < 2; index++ {
		result, code := service.Parse(context.Background(), time.Now().Add(time.Second), parent, input)
		if code != api.ErrorParserFailed || result.State != Fatal || len(result.Records) != 0 {
			t.Fatalf("panic parse %d = %#v,%q", index, result, code)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("fatal result was cached; parser calls = %d", calls.Load())
	}
}

func testWorkLease(t *testing.T, ctx context.Context, id string) *workruntime.WorkLease {
	t.Helper()
	coordinator := workruntime.NewCoordinator(workruntime.Limits{MaxConcurrent: 1, QueueMax: 0, QueueTimeout: time.Second})
	reservation, outcome := coordinator.Admit(ctx, []byte(id))
	if outcome != workruntime.AdmitRun || reservation == nil {
		t.Fatalf("test admission = %T,%d", reservation, outcome)
	}
	lease, start := reservation.Start()
	if start.Kind != workruntime.StartRun || lease == nil {
		t.Fatalf("test start = %p,%#v", lease, start)
	}
	t.Cleanup(lease.WorkerReturned)
	return lease
}
