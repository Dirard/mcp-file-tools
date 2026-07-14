package textio

import (
	"context"
	"crypto/sha256"
	"reflect"
	"testing"
	"unicode/utf8"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

func FuzzStreamCanonical(f *testing.F) {
	f.Add([]byte("alpha\r\nbeta\r"), uint8(0))
	f.Add([]byte{0xef, 0xbb, 0xbf, 'a', '\n', 0xff}, uint8(1))
	f.Add([]byte{0xff, 0xfe, 'a', 0, '\r', 0, '\n', 0}, uint8(1))
	f.Add([]byte{0x00, 0x00, 0xfe, 0xff, 0, 0, 0, '\n'}, uint8(1))
	f.Add([]byte{0xc0, 0x80, 0}, uint8(0))

	f.Fuzz(func(t *testing.T, raw []byte, through uint8) {
		if len(raw) > 64*1024 {
			t.Skip()
		}
		domain := Domain{ThroughLine: uint32(through)}
		firstSummary, firstCode, firstLines := fuzzStream(t, raw, domain)
		secondSummary, secondCode, secondLines := fuzzStream(t, raw, domain)
		if firstCode != secondCode || firstSummary != secondSummary || !reflect.DeepEqual(firstLines, secondLines) {
			t.Fatalf("non-deterministic result: first=(%#v,%q,%#v) second=(%#v,%q,%#v)", firstSummary, firstCode, firstLines, secondSummary, secondCode, secondLines)
		}

		file := openTextFile(t, raw)
		document, bufferedCode := BufferCanonical(context.Background(), file, domain, unlimitedBudget(), 64*1024)
		_ = file.Close()
		if bufferedCode != firstCode {
			t.Fatalf("stream code %q differs from buffer code %q", firstCode, bufferedCode)
		}
		if bufferedCode != "" {
			return
		}
		if document.Summary != firstSummary {
			t.Fatalf("stream summary %#v differs from buffer summary %#v", firstSummary, document.Summary)
		}
		if !utf8.Valid(document.Bytes) {
			t.Fatal("successful canonical buffer is not UTF-8")
		}
		if document.Summary.SHA256 != sha256.Sum256(document.Bytes) {
			t.Fatal("digest does not match canonical bytes")
		}
		if document.Summary.LineCount != uint64(len(document.Lines)) {
			t.Fatalf("line count = %d, offsets = %d", document.Summary.LineCount, len(document.Lines))
		}
	})
}

type fuzzLine struct {
	Number  uint64
	Bytes   string
	ByteLen uint64
	TooLong bool
}

type fuzzSink struct {
	lines []fuzzLine
}

func (sink *fuzzSink) Consume(line Line) error {
	sink.lines = append(sink.lines, fuzzLine{
		Number:  line.Number,
		Bytes:   string(line.Bytes),
		ByteLen: line.ByteLen,
		TooLong: line.TooLong,
	})
	return nil
}

func fuzzStream(t *testing.T, raw []byte, domain Domain) (Summary, api.ErrorCode, []fuzzLine) {
	t.Helper()
	file := openTextFile(t, raw)
	defer file.Close()
	sink := &fuzzSink{}
	summary, code := StreamCanonical(context.Background(), file, domain, unlimitedBudget(), sink)
	return summary, code, sink.lines
}
