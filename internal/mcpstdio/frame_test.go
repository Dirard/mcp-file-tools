package mcpstdio

import (
	"bytes"
	"errors"
	"io"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/config"
)

func TestFrameReaderReturnsCleanEOFWithoutAFrame(t *testing.T) {
	reader := newFrameReader(strings.NewReader(""))
	for attempt := 0; attempt < 2; attempt++ {
		frame, ok, err := reader.next()
		if err != nil || ok || frame != nil {
			t.Fatalf("next() = (%q, %t, %v), want (nil, false, nil)", frame, ok, err)
		}
	}
}

func TestFrameReaderPreservesEveryByteExceptDelimiterLF(t *testing.T) {
	input := "{}\n[1, 2]\n{\"x\":\t1}\r\n\n"
	want := []string{"{}", "[1, 2]", "{\"x\":\t1}\r", ""}
	reader := newFrameReader(strings.NewReader(input))
	for index, expected := range want {
		frame, ok, err := reader.next()
		if err != nil || !ok || string(frame) != expected {
			t.Fatalf("frame %d = (%q, %t, %v), want (%q, true, nil)", index, frame, ok, err, expected)
		}
	}
	frame, ok, err := reader.next()
	if err != nil || ok || frame != nil {
		t.Fatalf("final next() = (%q, %t, %v), want clean EOF", frame, ok, err)
	}
}

func TestFrameReaderAcceptsExactMaximumFrame(t *testing.T) {
	want := bytes.Repeat([]byte{'x'}, int(config.StdioFrameMaxBytes))
	input := append(append([]byte(nil), want...), '\n')
	reader := newFrameReader(bytes.NewReader(input))
	frame, ok, err := reader.next()
	if err != nil || !ok {
		t.Fatalf("next() = (%d bytes, %t, %v), want exact-cap frame", len(frame), ok, err)
	}
	if !bytes.Equal(frame, want) {
		t.Fatalf("frame differs at exact %d-byte cap", config.StdioFrameMaxBytes)
	}
}

func TestFrameReaderRejectsCapPlusOneBeforeDelimiter(t *testing.T) {
	source := &repeatedByteReader{
		remaining: int(config.StdioFrameMaxBytes) * 8,
		delimiter: true,
	}
	frame, ok, err := newFrameReader(source).next()
	if !errors.Is(err, errFrameTooLarge) || ok || frame != nil {
		t.Fatalf("next() = (%d bytes, %t, %v), want frame-too-large", len(frame), ok, err)
	}
	if source.bytesRead > int(config.StdioFrameMaxBytes)+(64<<10) {
		t.Fatalf("reader consumed %d bytes before rejecting cap+1", source.bytesRead)
	}
}

func TestFrameReaderDoesNotAllocateTheCompleteOversizedFrame(t *testing.T) {
	operation := func() {
		source := &repeatedByteReader{
			remaining: int(config.StdioFrameMaxBytes) * 8,
			delimiter: true,
		}
		frame, ok, err := newFrameReader(source).next()
		if !errors.Is(err, errFrameTooLarge) || ok || frame != nil {
			panic("unexpected oversized-frame result")
		}
	}
	allocated := frameAllocatedBytesPerRun(3, operation)
	ceiling := config.StdioFrameMaxBytes*3 + (128 << 10)
	t.Logf("allocated_bytes_per_read=%d ceiling=%d", allocated, ceiling)
	if allocated > ceiling {
		t.Fatalf("allocated bytes = %d, want <= %d", allocated, ceiling)
	}
}

func TestFrameReaderRejectsUnterminatedFinalBytes(t *testing.T) {
	for _, input := range []string{
		`{"jsonrpc":"2.0"}`,
		strings.Repeat("x", int(config.StdioFrameMaxBytes)),
	} {
		frame, ok, err := newFrameReader(strings.NewReader(input)).next()
		if !errors.Is(err, errUnterminatedFrame) || ok || frame != nil {
			t.Fatalf("next(%d bytes) = (%d bytes, %t, %v), want unterminated-frame", len(input), len(frame), ok, err)
		}
	}
}

func TestFrameReaderPrefersOversizedOverUnterminated(t *testing.T) {
	input := strings.Repeat("x", int(config.StdioFrameMaxBytes)+1)
	frame, ok, err := newFrameReader(strings.NewReader(input)).next()
	if !errors.Is(err, errFrameTooLarge) || ok || frame != nil {
		t.Fatalf("next() = (%d bytes, %t, %v), want frame-too-large", len(frame), ok, err)
	}
}

type repeatedByteReader struct {
	remaining int
	delimiter bool
	bytesRead int
}

func (reader *repeatedByteReader) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	if reader.remaining > 0 {
		count := min(len(buffer), reader.remaining)
		for index := range count {
			buffer[index] = 'x'
		}
		reader.remaining -= count
		reader.bytesRead += count
		return count, nil
	}
	if reader.delimiter {
		buffer[0] = '\n'
		reader.delimiter = false
		reader.bytesRead++
		return 1, nil
	}
	return 0, io.EOF
}

func frameAllocatedBytesPerRun(runs int, operation func()) uint64 {
	previousGCPercent := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(previousGCPercent)

	operation()
	minimum := ^uint64(0)
	for sample := 0; sample < 3; sample++ {
		runtime.GC()
		var before runtime.MemStats
		var after runtime.MemStats
		runtime.ReadMemStats(&before)
		for range runs {
			operation()
		}
		runtime.ReadMemStats(&after)
		bytesPerRun := (after.TotalAlloc - before.TotalAlloc) / uint64(runs)
		minimum = min(minimum, bytesPerRun)
	}
	return minimum
}
