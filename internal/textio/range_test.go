package textio

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

func TestFiniteDomainIgnoresInvalidSuffix(t *testing.T) {
	raw := append([]byte("one\ntwo\n"), []byte{0xff, 0, 0xff}...)
	file := openTextFile(t, raw)
	defer file.Close()
	sink := &collectSink{}
	summary, code := StreamCanonical(context.Background(), file, Domain{ThroughLine: 2}, unlimitedBudget(), sink)
	if code != "" {
		t.Fatalf("finite code = %q", code)
	}
	if got := sink.canonical(summary.FinalLF); got != "one\ntwo\n" {
		t.Fatalf("canonical = %q", got)
	}
	if summary.SHA256 != sha256.Sum256([]byte("one\ntwo\n")) {
		t.Fatal("finite digest includes suffix")
	}
	file = openTextFile(t, raw)
	defer file.Close()
	if _, fullCode := StreamCanonical(context.Background(), file, Domain{}, unlimitedBudget(), &collectSink{}); fullCode == "" {
		t.Fatal("full-domain read accepted the invalid suffix")
	}
}

func TestFiniteDomainUsesOnlyNeededRawBudget(t *testing.T) {
	file := openTextFile(t, []byte("\ninvalid suffix"))
	defer file.Close()
	summary, code := StreamCanonical(context.Background(), file, Domain{ThroughLine: 1}, Budget{MaxRawBytes: 1}, &collectSink{})
	if code != "" {
		t.Fatalf("code = %q", code)
	}
	if summary.RawRead != 1 {
		t.Fatalf("raw read = %d, want 1", summary.RawRead)
	}
}

func TestFiniteDomainReadAheadNeverExceeds4095Bytes(t *testing.T) {
	const delimiterEnd = 4096
	raw := []byte(strings.Repeat("x", delimiterEnd-1) + "\r" + strings.Repeat("z", 5000))
	file := openTextFile(t, raw)
	defer file.Close()
	summary, code := StreamCanonical(context.Background(), file, Domain{ThroughLine: 1}, unlimitedBudget(), &collectSink{})
	if code != "" {
		t.Fatalf("code = %q", code)
	}
	if readAhead := summary.RawRead - delimiterEnd; readAhead > 4095 {
		t.Fatalf("read-ahead = %d, want <= 4095", readAhead)
	}
}

func TestRawReadBudgetAndChargingAreExact(t *testing.T) {
	tests := []struct {
		name string
		cap  uint64
		want api.ErrorCode
	}{
		{name: "exact", cap: 4},
		{name: "cap_plus_one", cap: 3, want: api.ErrorBudgetExceeded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := openTextFile(t, []byte("data"))
			defer file.Close()
			var charged uint64
			summary, code := StreamCanonical(context.Background(), file, Domain{}, Budget{
				MaxRawBytes: test.cap,
				Charge: func(value uint64) error {
					charged += value
					return nil
				},
			}, &collectSink{})
			if code != test.want {
				t.Fatalf("code = %q, want %q", code, test.want)
			}
			if charged != summary.RawRead {
				t.Fatalf("charged = %d, raw read = %d", charged, summary.RawRead)
			}
		})
	}
}

func TestLifecycleAndChargeFailuresAreBudgetExceeded(t *testing.T) {
	tests := []struct {
		name   string
		ctx    context.Context
		budget Budget
	}{
		{name: "cancelled", ctx: cancelledContext(), budget: unlimitedBudget()},
		{name: "deadline", ctx: context.Background(), budget: Budget{MaxRawBytes: 100, Deadline: time.Now().Add(-time.Second)}},
		{name: "charge", ctx: context.Background(), budget: Budget{MaxRawBytes: 100, Charge: func(uint64) error { return errors.New("full") }}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := openTextFile(t, []byte("data"))
			defer file.Close()
			_, code := StreamCanonical(test.ctx, file, Domain{}, test.budget, &collectSink{})
			if code != api.ErrorBudgetExceeded {
				t.Fatalf("code = %q", code)
			}
		})
	}
}

func TestStreamRetainsLongLines(t *testing.T) {
	raw := []byte(strings.Repeat("a", 4096) + "\n" + strings.Repeat("b", 4097) + "\n")
	file := openTextFile(t, raw)
	defer file.Close()
	sink := &lineCaptureSink{}
	_, code := StreamCanonical(context.Background(), file, Domain{}, unlimitedBudget(), sink)
	if code != "" {
		t.Fatalf("code = %q", code)
	}
	if len(sink.lines) != 2 {
		t.Fatalf("lines = %d", len(sink.lines))
	}
	if sink.lines[0].ByteLen != 4096 || len(sink.lines[0].Bytes) != 4096 {
		t.Fatalf("4096-byte line = %#v", sink.lines[0])
	}
	if sink.lines[1].ByteLen != 4097 || len(sink.lines[1].Bytes) != 4097 {
		t.Fatalf("4097-byte line = %#v", sink.lines[1])
	}
}

func TestRangeSinkSelectsLiteralRowsAndClampsEOF(t *testing.T) {
	raw := []byte(strings.Repeat("p", 4097) + "\n\t|marker\nlast")
	file := openTextFile(t, raw)
	defer file.Close()
	sink, code := NewRangeSink(2, 10, ^uint64(0))
	if code != "" {
		t.Fatalf("NewRangeSink() code = %q", code)
	}
	summary, code := StreamCanonical(context.Background(), file, Domain{ThroughLine: 10}, unlimitedBudget(), sink)
	if code != "" {
		t.Fatalf("StreamCanonical() code = %q", code)
	}
	if code := sink.Finish(summary); code != "" {
		t.Fatalf("Finish() code = %q", code)
	}
	lines := sink.TakeLines()
	if len(lines) != 2 || lines[0].Number != 2 || lines[0].Text != "\t|marker" || lines[1].Number != 3 || lines[1].Text != "last" {
		t.Fatalf("lines = %#v", lines)
	}
}

func TestRangeSinkValidatesRange(t *testing.T) {
	if _, code := NewRangeSink(0, 1, ^uint64(0)); code != api.ErrorInvalidInput {
		t.Fatalf("zero start code = %q", code)
	}
	if _, code := NewRangeSink(2, 1, ^uint64(0)); code != api.ErrorInvalidInput {
		t.Fatalf("reversed range code = %q", code)
	}

	file := openTextFile(t, []byte("one\n"))
	sink, _ := NewRangeSink(2, 2, ^uint64(0))
	summary, code := StreamCanonical(context.Background(), file, Domain{ThroughLine: 2}, unlimitedBudget(), sink)
	_ = file.Close()
	if code != "" || sink.Finish(summary) != api.ErrorInvalidInput {
		t.Fatalf("start after EOF: stream=%q finish=%q", code, sink.Finish(summary))
	}

	file = openTextFile(t, nil)
	sink, _ = NewRangeSink(1, 1, ^uint64(0))
	summary, code = StreamCanonical(context.Background(), file, Domain{ThroughLine: 1}, unlimitedBudget(), sink)
	_ = file.Close()
	if code != "" || sink.Finish(summary) != "" || len(sink.TakeLines()) != 0 {
		t.Fatalf("empty range: stream=%q finish=%q", code, sink.Finish(summary))
	}

	file = openTextFile(t, []byte("one\n"))
	sink, _ = NewRangeSink(1, 1, 1)
	_, code = StreamCanonical(context.Background(), file, Domain{ThroughLine: 1}, unlimitedBudget(), sink)
	_ = file.Close()
	if code != api.ErrorBudgetExceeded || len(sink.TakeLines()) != 0 {
		t.Fatalf("retained range budget: code=%q", code)
	}
}

func TestBufferCanonicalReturnsCompactCanonicalBytesAndOffsets(t *testing.T) {
	file := openTextFile(t, []byte("alpha\r\nβ"))
	defer file.Close()
	document, code := BufferCanonical(context.Background(), file, Domain{}, unlimitedBudget(), 100)
	if code != "" {
		t.Fatalf("code = %q", code)
	}
	if got := string(document.Bytes); got != "alpha\nβ" {
		t.Fatalf("bytes = %q", got)
	}
	if cap(document.Bytes) != len(document.Bytes) || cap(document.Lines) != len(document.Lines) {
		t.Fatalf("buffer is not compact: bytes=%d/%d lines=%d/%d", len(document.Bytes), cap(document.Bytes), len(document.Lines), cap(document.Lines))
	}
	if len(document.Lines) != 2 || document.Lines[0] != (LineOffset{Number: 1, Start: 0, End: 5}) || document.Lines[1] != (LineOffset{Number: 2, Start: 6, End: 8}) {
		t.Fatalf("offsets = %#v", document.Lines)
	}
	if line, ok := document.Line(2); !ok || string(line) != "β" {
		t.Fatalf("Line(2) = %q,%t", line, ok)
	}
	if _, ok := document.Line(0); ok {
		t.Fatal("Line(0) succeeded")
	}
	wantFootprint := uint64(cap(document.Bytes)) + uint64(cap(document.Lines))*uint64(unsafe.Sizeof(LineOffset{}))
	if document.Footprint() != wantFootprint {
		t.Fatalf("footprint = %d, want %d", document.Footprint(), wantFootprint)
	}
}

func TestBufferCanonicalEnforcesWholeDocumentCapWithoutLineCap(t *testing.T) {
	longLine := strings.Repeat("x", 5000)
	file := openTextFile(t, []byte(longLine))
	document, code := BufferCanonical(context.Background(), file, Domain{}, unlimitedBudget(), 5000)
	_ = file.Close()
	if code != "" || string(document.Bytes) != longLine {
		t.Fatalf("exact cap: code=%q bytes=%d", code, len(document.Bytes))
	}

	file = openTextFile(t, []byte(longLine))
	document, code = BufferCanonical(context.Background(), file, Domain{}, unlimitedBudget(), 4999)
	_ = file.Close()
	if code != api.ErrorBudgetExceeded || len(document.Bytes) != 0 || len(document.Lines) != 0 {
		t.Fatalf("cap exceeded: code=%q document=%#v", code, document)
	}

	file = openTextFile(t, nil)
	_, code = BufferCanonical(context.Background(), file, Domain{}, unlimitedBudget(), MaxCanonicalBufferBytes+1)
	_ = file.Close()
	if code != api.ErrorInvalidInput {
		t.Fatalf("oversized configured cap code = %q", code)
	}
}

func TestBufferCanonicalStopsAtCanonicalCap(t *testing.T) {
	file := openTextFile(t, []byte(strings.Repeat("x", 10_000)))
	defer file.Close()
	var charged uint64
	_, code := BufferCanonical(context.Background(), file, Domain{}, Budget{
		MaxRawBytes: 20_000,
		Charge: func(value uint64) error {
			charged += value
			return nil
		},
	}, 10)
	if code != api.ErrorBudgetExceeded {
		t.Fatalf("code = %q", code)
	}
	if charged > 4096 {
		t.Fatalf("raw read = %d, want at most one chunk", charged)
	}
}

type lineCaptureSink struct {
	lines []Line
}

func (sink *lineCaptureSink) Consume(line Line) error {
	copyLine := line
	copyLine.Bytes = append([]byte(nil), line.Bytes...)
	sink.lines = append(sink.lines, copyLine)
	return nil
}

func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
