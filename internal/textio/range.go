package textio

import (
	"errors"
	"unsafe"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

// RangeLine owns one literal selected source line.
type RangeLine struct {
	Number uint32
	Text   string
}

// RangeSink copies only lines in an already validated inclusive range.
type RangeSink struct {
	start      uint64
	end        uint64
	lines      []RangeLine
	textBytes  uint64
	maxBytes   uint64
	finished   bool
	finishCode api.ErrorCode
}

func NewRangeSink(start, end uint32, maxBytes uint64) (*RangeSink, api.ErrorCode) {
	if start == 0 || end < start {
		return nil, api.ErrorInvalidInput
	}
	return &RangeSink{start: uint64(start), end: uint64(end), maxBytes: maxBytes}, ""
}

func (sink *RangeSink) Consume(line Line) error {
	if sink == nil || sink.finished {
		return sinkCodeError{code: api.ErrorInvalidInput}
	}
	if line.Number < sink.start || line.Number > sink.end {
		return nil
	}
	if line.TooLong {
		return sinkCodeError{code: api.ErrorLineTooLong}
	}
	if !sink.appendLine(uint32(line.Number), line.Bytes) {
		return sinkCodeError{code: api.ErrorBudgetExceeded}
	}
	return nil
}

func (sink *RangeSink) appendLine(number uint32, text []byte) bool {
	addedText := uint64(len(text))
	required := len(sink.lines) + 1
	capacity := cap(sink.lines)
	if required > capacity {
		capacity = 1
		if cap(sink.lines) != 0 {
			capacity = cap(sink.lines) * 2
			if capacity < required {
				capacity = required
			}
		}
		if !sink.fits(capacity, addedText) {
			capacity = required
		}
		if !sink.fits(capacity, addedText) {
			return false
		}
		grown := make([]RangeLine, len(sink.lines), capacity)
		copy(grown, sink.lines)
		sink.lines = grown
	} else if !sink.fits(capacity, addedText) {
		return false
	}
	sink.lines = append(sink.lines, RangeLine{Number: number, Text: string(text)})
	sink.textBytes += addedText
	return true
}

func (sink *RangeSink) fits(capacity int, addedText uint64) bool {
	headerBytes := uint64(capacity) * uint64(unsafe.Sizeof(RangeLine{}))
	if headerBytes > sink.maxBytes || sink.textBytes > sink.maxBytes-headerBytes {
		return false
	}
	return addedText <= sink.maxBytes-headerBytes-sink.textBytes
}

// Finish validates EOF clamping. Empty files always produce an empty range.
func (sink *RangeSink) Finish(summary Summary) api.ErrorCode {
	if sink == nil {
		return api.ErrorInvalidInput
	}
	if sink.finished {
		return sink.finishCode
	}
	sink.finished = true
	if summary.LineCount != 0 && sink.start > summary.LineCount {
		sink.finishCode = api.ErrorInvalidInput
	}
	return sink.finishCode
}

// TakeLines transfers the selected literal rows after a successful Finish.
func (sink *RangeSink) TakeLines() []RangeLine {
	if sink == nil {
		return nil
	}
	if !sink.finished || sink.finishCode != "" {
		sink.lines = nil
		sink.textBytes = 0
		return nil
	}
	lines := sink.lines
	sink.lines = nil
	sink.textBytes = 0
	return lines
}

type sinkCodeError struct {
	code api.ErrorCode
}

func (err sinkCodeError) Error() string { return string(err.code) }

func sinkErrorCode(err error) api.ErrorCode {
	var coded sinkCodeError
	if errors.As(err, &coded) && coded.code.Valid() {
		return coded.code
	}
	return api.ErrorIOError
}
