package textio

import (
	"context"
	"math"
	"unsafe"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/rootfs"
)

const MaxCanonicalBufferBytes uint64 = 33_554_432

type CanonicalBuffer struct {
	Summary Summary
	Bytes   []byte
	Lines   []LineOffset
}

type LineOffset struct {
	Number uint32
	Start  uint32
	End    uint32
}

func BufferCanonical(ctx context.Context, file *rootfs.File, domain Domain, budget Budget, maxCanonicalBytes uint64) (CanonicalBuffer, api.ErrorCode) {
	if maxCanonicalBytes > MaxCanonicalBufferBytes || maxCanonicalBytes >= math.MaxUint32 {
		return CanonicalBuffer{}, api.ErrorInvalidInput
	}
	sink := &bufferSink{maxBytes: maxCanonicalBytes}
	summary, code := streamCanonicalWithLimits(ctx, file, domain, budget, sink, maxCanonicalBytes+1, maxCanonicalBytes)
	if code != "" {
		return CanonicalBuffer{Summary: summary}, code
	}
	if !summary.FinalLF && len(sink.lines) != 0 {
		if len(sink.bytes) == 0 || sink.bytes[len(sink.bytes)-1] != '\n' {
			return CanonicalBuffer{}, api.ErrorIOError
		}
		sink.bytes = sink.bytes[:len(sink.bytes)-1]
	}
	if uint64(len(sink.bytes)) > maxCanonicalBytes {
		return CanonicalBuffer{}, api.ErrorBudgetExceeded
	}
	canonical := make([]byte, len(sink.bytes))
	copy(canonical, sink.bytes)
	offsets := make([]LineOffset, len(sink.lines))
	copy(offsets, sink.lines)
	return CanonicalBuffer{Summary: summary, Bytes: canonical, Lines: offsets}, ""
}

func (buffer CanonicalBuffer) Line(number uint32) ([]byte, bool) {
	if number == 0 || uint64(number) > uint64(len(buffer.Lines)) {
		return nil, false
	}
	offset := buffer.Lines[number-1]
	if offset.Number != number || offset.Start > offset.End || uint64(offset.End) > uint64(len(buffer.Bytes)) {
		return nil, false
	}
	return buffer.Bytes[offset.Start:offset.End], true
}

// Footprint counts the retained backing arrays exactly once.
func (buffer CanonicalBuffer) Footprint() uint64 {
	return uint64(cap(buffer.Bytes)) + uint64(cap(buffer.Lines))*uint64(unsafe.Sizeof(LineOffset{}))
}

type bufferSink struct {
	maxBytes uint64
	bytes    []byte
	lines    []LineOffset
}

func (sink *bufferSink) Consume(line Line) error {
	if line.TooLong || line.ByteLen != uint64(len(line.Bytes)) {
		return sinkCodeError{code: api.ErrorBudgetExceeded}
	}
	if line.Number != uint64(len(sink.lines))+1 || line.Number > math.MaxUint32 {
		return sinkCodeError{code: api.ErrorIOError}
	}
	additional := uint64(len(line.Bytes)) + 1
	if uint64(len(sink.bytes)) > math.MaxUint64-additional || uint64(len(sink.bytes))+additional > sink.maxBytes+1 {
		return sinkCodeError{code: api.ErrorBudgetExceeded}
	}
	start := uint32(len(sink.bytes))
	sink.bytes = append(sink.bytes, line.Bytes...)
	end := uint32(len(sink.bytes))
	sink.lines = append(sink.lines, LineOffset{Number: uint32(line.Number), Start: start, End: end})
	sink.bytes = append(sink.bytes, '\n')
	return nil
}
