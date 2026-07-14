package textio

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/config"
	"github.com/Dirard/mcp-file-tools/internal/rootfs"
)

type rawReader struct {
	ctx      context.Context
	file     *rootfs.File
	budget   Budget
	chunk    [config.RangeReadChunkMaxBytes]byte
	position int
	length   int
	rawRead  uint64
	eof      bool
}

func newRawReader(ctx context.Context, file *rootfs.File, budget Budget) (*rawReader, api.ErrorCode) {
	if ctx == nil || file == nil {
		return nil, api.ErrorInvalidInput
	}
	reader := &rawReader{ctx: ctx, file: file, budget: budget}
	if code := reader.checkLifecycle(); code != "" {
		return nil, code
	}
	return reader, ""
}

func (reader *rawReader) next() (byte, bool, api.ErrorCode) {
	return reader.nextWithReadLimit(len(reader.chunk))
}

func (reader *rawReader) nextWithReadLimit(readLimit int) (byte, bool, api.ErrorCode) {
	if reader.position == reader.length {
		if code := reader.fill(readLimit); code != "" {
			return 0, false, code
		}
		if reader.eof {
			return 0, false, ""
		}
	}
	value := reader.chunk[reader.position]
	reader.position++
	return value, true, ""
}

func (reader *rawReader) fill(readLimit int) api.ErrorCode {
	if reader.eof {
		return ""
	}
	if code := reader.checkLifecycle(); code != "" {
		return code
	}
	if readLimit < 1 || readLimit > len(reader.chunk) {
		return api.ErrorIOError
	}
	readSize := readLimit
	if reader.budget.MaxRawBytes != 0 {
		if reader.rawRead >= reader.budget.MaxRawBytes {
			readSize = 1
		} else if remaining := reader.budget.MaxRawBytes - reader.rawRead; remaining < uint64(readSize) {
			readSize = int(remaining)
		}
	}
	n, err := reader.file.ReadContext(reader.ctx, reader.chunk[:readSize])
	reader.position = 0
	reader.length = 0
	if n > 0 {
		reader.rawRead += uint64(n)
		if reader.budget.Charge != nil && reader.budget.Charge(uint64(n)) != nil {
			return api.ErrorBudgetExceeded
		}
		if reader.budget.MaxRawBytes != 0 && reader.rawRead > reader.budget.MaxRawBytes {
			return api.ErrorBudgetExceeded
		}
		reader.length = n
	}
	if err != nil {
		if errors.Is(err, io.EOF) && n == 0 {
			reader.eof = true
			return ""
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return api.ErrorBudgetExceeded
		}
		return api.ErrorIOError
	}
	if n == 0 {
		reader.eof = true
	}
	return ""
}

func (reader *rawReader) checkLifecycle() api.ErrorCode {
	if reader.ctx.Err() != nil {
		return api.ErrorBudgetExceeded
	}
	if !reader.budget.Deadline.IsZero() && !time.Now().Before(reader.budget.Deadline) {
		return api.ErrorBudgetExceeded
	}
	return ""
}
