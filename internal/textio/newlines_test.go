package textio

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

func TestStreamCanonicalNormalizesLogicalNewlines(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		lines   uint64
		finalLF bool
	}{
		{name: "lf", raw: "a\nb\n", want: "a\nb\n", lines: 2, finalLF: true},
		{name: "crlf", raw: "a\r\nb\r\n", want: "a\nb\n", lines: 2, finalLF: true},
		{name: "lone_cr", raw: "a\rb\r", want: "a\nb\n", lines: 2, finalLF: true},
		{name: "mixed_without_final", raw: "a\r\nb\rc", want: "a\nb\nc", lines: 3},
		{name: "empty_lines", raw: "\n\n", want: "\n\n", lines: 2, finalLF: true},
		{name: "no_final", raw: "alpha", want: "alpha", lines: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := openTextFile(t, []byte(test.raw))
			defer file.Close()
			sink := &collectSink{}
			summary, code := StreamCanonical(context.Background(), file, Domain{}, unlimitedBudget(), sink)
			if code != "" {
				t.Fatalf("code = %q", code)
			}
			if got := sink.canonical(summary.FinalLF); got != test.want {
				t.Fatalf("canonical = %q, want %q", got, test.want)
			}
			if summary.LineCount != test.lines || summary.FinalLF != test.finalLF {
				t.Fatalf("summary = %#v, want lines=%d finalLF=%t", summary, test.lines, test.finalLF)
			}
			if summary.SHA256 != sha256.Sum256([]byte(test.want)) {
				t.Fatal("digest does not cover canonical bytes")
			}
		})
	}
}

func TestEquivalentEncodingsShareCanonicalDigest(t *testing.T) {
	rawValues := [][]byte{
		[]byte("alpha\r\nbeta\r"),
		append([]byte{0xef, 0xbb, 0xbf}, []byte("alpha\nbeta\n")...),
		encodeUTF16([]rune("alpha\nbeta\n"), binary.LittleEndian),
		encodeUTF32([]rune("alpha\rbeta\r\n"), binary.BigEndian),
	}
	var want [sha256.Size]byte
	for index, raw := range rawValues {
		file := openTextFile(t, raw)
		summary, code := StreamCanonical(context.Background(), file, Domain{}, unlimitedBudget(), &collectSink{})
		_ = file.Close()
		if code != "" {
			t.Fatalf("case %d code = %q", index, code)
		}
		if index == 0 {
			want = summary.SHA256
		} else if summary.SHA256 != want {
			t.Fatalf("case %d digest differs", index)
		}
	}
}

func TestLineCounterDoesNotNarrowOrWrap(t *testing.T) {
	counter := lineCounter{value: math.MaxUint32 - 2}
	for _, want := range []uint64{math.MaxUint32 - 1, math.MaxUint32, math.MaxUint32 + 1} {
		got, ok := counter.next()
		if !ok || got != want {
			t.Fatalf("next = (%d,%t), want (%d,true)", got, ok, want)
		}
	}
	counter.value = math.MaxUint64
	if _, ok := counter.next(); ok {
		t.Fatal("counter wrapped at MaxUint64")
	}
}

func TestBufferCanonicalReportsReadChargeOnCanonicalBudgetFailure(t *testing.T) {
	file := openTextFile(t, []byte("abcdef\n"))
	defer file.Close()

	buffer, code := BufferCanonical(context.Background(), file, Domain{}, Budget{MaxRawBytes: 64}, 3)
	if code != api.ErrorBudgetExceeded {
		t.Fatalf("code = %q", code)
	}
	if buffer.Summary.RawRead == 0 {
		t.Fatal("failed buffer lost its physical read charge")
	}
	if buffer.Bytes != nil || buffer.Lines != nil {
		t.Fatalf("failed buffer retained canonical content: %+v", buffer)
	}
}
