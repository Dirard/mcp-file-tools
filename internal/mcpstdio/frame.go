package mcpstdio

import (
	"bufio"
	"errors"
	"fmt"
	"io"

	"github.com/Dirard/mcp-file-tools/internal/config"
)

const frameReadBufferBytes = 4 << 10

var (
	errFrameTooLarge     = errors.New("mcpstdio: frame too large")
	errUnterminatedFrame = errors.New("mcpstdio: unterminated frame")
)

type frameReader struct {
	input *bufio.Reader
	done  bool
}

func newFrameReader(input io.Reader) *frameReader {
	return &frameReader{
		input: bufio.NewReaderSize(input, frameReadBufferBytes),
	}
}

func (reader *frameReader) next() ([]byte, bool, error) {
	if reader.done {
		return nil, false, nil
	}

	limit := int(config.StdioFrameMaxBytes)
	var frame []byte
	for {
		fragment, err := reader.input.ReadSlice('\n')
		delimited := len(fragment) != 0 && fragment[len(fragment)-1] == '\n'
		if delimited {
			fragment = fragment[:len(fragment)-1]
		}
		if len(fragment) > limit-len(frame) {
			reader.done = true
			return nil, false, errFrameTooLarge
		}
		frame = appendFrameBytes(frame, fragment, limit)

		if delimited {
			return frame, true, nil
		}
		switch err {
		case nil:
			continue
		case bufio.ErrBufferFull:
			continue
		case io.EOF:
			reader.done = true
			if len(frame) == 0 {
				return nil, false, nil
			}
			return nil, false, errUnterminatedFrame
		default:
			reader.done = true
			return nil, false, fmt.Errorf("mcpstdio: read frame: %w", err)
		}
	}
}

func appendFrameBytes(frame, fragment []byte, limit int) []byte {
	required := len(frame) + len(fragment)
	if required <= cap(frame) {
		return append(frame, fragment...)
	}

	capacity := cap(frame) * 2
	if capacity < required {
		capacity = required
	}
	if capacity < 64 {
		capacity = 64
	}
	if capacity > limit {
		capacity = limit
	}
	next := make([]byte, len(frame), capacity)
	copy(next, frame)
	return append(next, fragment...)
}
