package handler

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/dimitar-grigorov/mcp-file-tools/internal/config"
)

func TestNewHandlerConfiguresLimitersFromConfig(t *testing.T) {
	cfg := &config.Config{
		MemoryThreshold:   1024,
		MaxToolCalls:      1,
		MaxScanCalls:      2,
		MaxLargeReadCalls: 3,
	}
	h := NewHandler(WithConfig(cfg))

	if cap(h.limiters.tools.permits) != 1 {
		t.Fatalf("tool limiter capacity = %d, want 1", cap(h.limiters.tools.permits))
	}
	if cap(h.limiters.scans.permits) != 2 {
		t.Fatalf("scan limiter capacity = %d, want 2", cap(h.limiters.scans.permits))
	}
	if cap(h.limiters.largeReads.permits) != 3 {
		t.Fatalf("large read limiter capacity = %d, want 3", cap(h.limiters.largeReads.permits))
	}
}

func TestRequestLimiterWaitsForRelease(t *testing.T) {
	limiter := newRequestLimiter(1)
	releaseFirst, err := limiter.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	releasedFirst := false
	defer func() {
		if !releasedFirst {
			releaseFirst()
		}
	}()

	acquired := make(chan func(), 1)
	errs := make(chan error, 1)
	go func() {
		releaseSecond, err := limiter.acquire(context.Background())
		if err != nil {
			errs <- err
			return
		}
		acquired <- releaseSecond
	}()

	select {
	case releaseSecond := <-acquired:
		releaseSecond()
		t.Fatal("limiter acquired a second permit before the first was released")
	case err := <-errs:
		t.Fatalf("unexpected acquire error: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	releaseFirst()
	releasedFirst = true
	select {
	case releaseSecond := <-acquired:
		releaseSecond()
	case err := <-errs:
		t.Fatalf("unexpected acquire error after release: %v", err)
	case <-time.After(time.Second):
		t.Fatal("limiter did not acquire after release")
	}
}

func TestRequestLimiterStopsWaitingOnContextCancel(t *testing.T) {
	limiter := newRequestLimiter(1)
	release, err := limiter.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() {
		_, err := limiter.acquire(ctx)
		errs <- err
	}()

	select {
	case err := <-errs:
		t.Fatalf("limiter returned before cancellation: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-errs:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("limiter error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("limiter did not stop waiting after context cancellation")
	}
}

func TestResolveSymbolRangeUsesToolLimiter(t *testing.T) {
	cfg := &config.Config{
		MemoryThreshold:   1024,
		MaxToolCalls:      1,
		WriteThreshold:    1024,
		MaxScanCalls:      1,
		MaxLargeReadCalls: 1,
	}
	h := NewHandler(WithConfig(cfg))
	release, err := h.acquireToolCall(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result, output, err := h.HandleResolveSymbolRange(ctx, nil, ResolveSymbolRangeInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(output.Error, "tool call cancelled while waiting for tool call capacity") {
		t.Fatalf("resolve_symbol_range should wait on the tool limiter before validation: result=%#v output=%#v", result, output)
	}
}

func TestGrepLineRowsStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	keepGoing, _, err := grepLineRowsForFile(
		ctx,
		"sample.log",
		[]string{"needle"},
		0,
		regexp.MustCompile("needle"),
		grepSearchOptions{Mode: "content"},
		func(row textRow) (bool, error) {
			t.Fatalf("did not expect row emission after cancellation: %#v", row)
			return false, nil
		},
	)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("grep line cancellation error = %v, want context.Canceled", err)
	}
	if keepGoing {
		t.Fatal("grep line cancellation should stop scanning")
	}
}

func TestReadDisplayLineBoundedStopsInsideLongLineOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader := &cancelAfterFirstByteReader{
		data:   strings.Repeat("x", 4096),
		cancel: cancel,
	}
	stream := &displayRuneStream{reader: bufio.NewReader(reader)}

	line, ok, err := readDisplayLineBounded(ctx, stream, 4096)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("readDisplayLineBounded cancellation error = %v, want context.Canceled", err)
	}
	if line != "" || ok {
		t.Fatalf("readDisplayLineBounded returned line=%q ok=%v after cancellation", line, ok)
	}
	if reader.reads > 1 {
		t.Fatalf("readDisplayLineBounded kept reading after cancellation; reads=%d", reader.reads)
	}
}

func TestGrepLargeFileRowsPropagatesContextCancellation(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "large.log")
	if err := os.WriteFile(file, []byte("needle\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	h := NewHandler(WithConfig(&config.Config{MemoryThreshold: 128}))
	keepGoing, _, err := h.grepLargeFileRows(ctx, file, file, regexp.MustCompile("needle"), grepSearchOptions{Mode: "content"}, func(row textRow) (bool, error) {
		t.Fatalf("did not expect row emission after cancellation: %#v", row)
		return false, nil
	})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("grep large-file cancellation error = %v, want context.Canceled", err)
	}
	if keepGoing {
		t.Fatal("grep large-file cancellation should stop scanning")
	}
}

type cancelAfterFirstByteReader struct {
	data   string
	cancel context.CancelFunc
	reads  int
}

func (r *cancelAfterFirstByteReader) Read(p []byte) (int, error) {
	if r.data == "" {
		return 0, io.EOF
	}
	p[0] = r.data[0]
	r.data = r.data[1:]
	r.reads++
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	return 1, nil
}
