package textio

import (
	"context"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/rootfs"
)

// Encoding is one member of the closed v2 text encoding matrix.
type Encoding string

const (
	EncodingUTF8    Encoding = "utf-8"
	EncodingUTF16BE Encoding = "utf-16be"
	EncodingUTF16LE Encoding = "utf-16le"
	EncodingUTF32BE Encoding = "utf-32be"
	EncodingUTF32LE Encoding = "utf-32le"
)

// Domain selects a canonical prefix. Zero means the complete file.
type Domain struct {
	ThroughLine uint32
}

// Budget bounds physical reads. A zero deadline disables the time check.
type Budget struct {
	MaxRawBytes uint64
	Deadline    time.Time
	Charge      func(rawBytes uint64) error
}

// Summary describes the complete selected canonical domain.
type Summary struct {
	Encoding  Encoding
	FinalLF   bool
	SHA256    [32]byte
	RawRead   uint64
	LineCount uint64
}

// Line is valid only for the duration of Consume.
type Line struct {
	Number  uint64
	Bytes   []byte
	ByteLen uint64
	TooLong bool
}

type LineSink interface {
	Consume(Line) error
}

// StreamCanonical decodes one selected domain without reopening or closing file.
func StreamCanonical(ctx context.Context, file *rootfs.File, domain Domain, budget Budget, sink LineSink) (Summary, api.ErrorCode) {
	return streamCanonical(ctx, file, domain, budget, sink)
}
