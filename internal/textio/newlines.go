package textio

import (
	"context"
	"crypto/sha256"
	"hash"
	"math"
	"unicode/utf8"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/config"
	"github.com/Dirard/mcp-file-tools/internal/rootfs"
)

func streamCanonical(ctx context.Context, file *rootfs.File, domain Domain, budget Budget, sink LineSink) (Summary, api.ErrorCode) {
	return streamCanonicalWithLimits(ctx, file, domain, budget, sink, config.SearchScanLineMaxBytes, math.MaxUint64)
}

func streamCanonicalWithLimits(ctx context.Context, file *rootfs.File, domain Domain, budget Budget, sink LineSink, lineLimit, canonicalLimit uint64) (Summary, api.ErrorCode) {
	if sink == nil {
		return Summary{}, api.ErrorInvalidInput
	}
	raw, code := newRawReader(ctx, file, budget)
	if code != "" {
		return Summary{}, code
	}
	encoding, source, code := detectEncoding(raw)
	if code != "" {
		return Summary{RawRead: raw.rawRead}, code
	}
	decoder := scalarDecoder{encoding: encoding, source: source}
	emitter := newLineEmitter(sink, lineLimit, canonicalLimit)
	skipLF := false
	for {
		character, present, decodeCode := decoder.nextRune()
		if decodeCode != "" {
			return emitter.summary(encoding, raw.rawRead), decodeCode
		}
		if !present {
			if finishCode := emitter.finish(); finishCode != "" {
				return emitter.summary(encoding, raw.rawRead), finishCode
			}
			return emitter.summary(encoding, raw.rawRead), ""
		}
		if skipLF {
			skipLF = false
			if character == '\n' {
				if lifecycleCode := raw.checkLifecycle(); lifecycleCode != "" {
					return emitter.summary(encoding, raw.rawRead), lifecycleCode
				}
				continue
			}
		}
		switch character {
		case '\r':
			if domain.ThroughLine != 0 && emitter.nextNumber() == uint64(domain.ThroughLine) {
				if delimiterCode := decoder.consumeFollowingLF(); delimiterCode != "" {
					return emitter.summary(encoding, raw.rawRead), delimiterCode
				}
				if lineCode := emitter.newline(); lineCode != "" {
					return emitter.summary(encoding, raw.rawRead), lineCode
				}
				return emitter.summary(encoding, raw.rawRead), ""
			}
			if lineCode := emitter.newline(); lineCode != "" {
				return emitter.summary(encoding, raw.rawRead), lineCode
			}
			skipLF = true
		case '\n':
			if lineCode := emitter.newline(); lineCode != "" {
				return emitter.summary(encoding, raw.rawRead), lineCode
			}
			if domain.ThroughLine != 0 && emitter.counter.value == uint64(domain.ThroughLine) {
				return emitter.summary(encoding, raw.rawRead), ""
			}
		default:
			if textCode := emitter.text(character); textCode != "" {
				return emitter.summary(encoding, raw.rawRead), textCode
			}
		}
		if lifecycleCode := raw.checkLifecycle(); lifecycleCode != "" {
			return emitter.summary(encoding, raw.rawRead), lifecycleCode
		}
	}
}

type lineEmitter struct {
	sink           LineSink
	digest         hash.Hash
	line           []byte
	byteLen        uint64
	counter        lineCounter
	hasText        bool
	finalLF        bool
	lineLimit      uint64
	canonicalLimit uint64
	canonicalBytes uint64
}

type lineCounter struct {
	value uint64
}

func (counter *lineCounter) next() (uint64, bool) {
	if counter.value == math.MaxUint64 {
		return 0, false
	}
	counter.value++
	return counter.value, true
}

func newLineEmitter(sink LineSink, lineLimit, canonicalLimit uint64) *lineEmitter {
	return &lineEmitter{
		sink:           sink,
		digest:         sha256.New(),
		lineLimit:      lineLimit,
		canonicalLimit: canonicalLimit,
	}
}

func (emitter *lineEmitter) nextNumber() uint64 {
	if emitter.counter.value == math.MaxUint64 {
		return 0
	}
	return emitter.counter.value + 1
}

func (emitter *lineEmitter) text(character rune) api.ErrorCode {
	var encoded [utf8.UTFMax]byte
	length := utf8.EncodeRune(encoded[:], character)
	if uint64(length) > emitter.canonicalLimit || emitter.canonicalBytes > emitter.canonicalLimit-uint64(length) {
		return api.ErrorBudgetExceeded
	}
	if emitter.byteLen > math.MaxUint64-uint64(length) {
		return api.ErrorIOError
	}
	emitter.canonicalBytes += uint64(length)
	emitter.byteLen += uint64(length)
	if emitter.byteLen <= emitter.lineLimit {
		emitter.line = append(emitter.line, encoded[:length]...)
	}
	_, _ = emitter.digest.Write(encoded[:length])
	emitter.hasText = true
	emitter.finalLF = false
	return ""
}

func (emitter *lineEmitter) newline() api.ErrorCode {
	if emitter.canonicalBytes == emitter.canonicalLimit {
		return api.ErrorBudgetExceeded
	}
	emitter.canonicalBytes++
	number, ok := emitter.counter.next()
	if !ok {
		return api.ErrorIOError
	}
	line := emitter.currentLine(number)
	if err := emitter.sink.Consume(line); err != nil {
		return sinkErrorCode(err)
	}
	_, _ = emitter.digest.Write([]byte{'\n'})
	emitter.line = emitter.line[:0]
	emitter.byteLen = 0
	emitter.hasText = false
	emitter.finalLF = true
	return ""
}

func (emitter *lineEmitter) finish() api.ErrorCode {
	if !emitter.hasText {
		return ""
	}
	number, ok := emitter.counter.next()
	if !ok {
		return api.ErrorIOError
	}
	line := emitter.currentLine(number)
	if err := emitter.sink.Consume(line); err != nil {
		return sinkErrorCode(err)
	}
	emitter.line = emitter.line[:0]
	emitter.byteLen = 0
	emitter.hasText = false
	emitter.finalLF = false
	return ""
}

func (emitter *lineEmitter) currentLine(number uint64) Line {
	if emitter.byteLen > emitter.lineLimit {
		return Line{Number: number, ByteLen: emitter.byteLen, TooLong: true}
	}
	return Line{Number: number, Bytes: emitter.line, ByteLen: emitter.byteLen}
}

func (emitter *lineEmitter) summary(encoding Encoding, rawRead uint64) Summary {
	var digest [sha256.Size]byte
	copy(digest[:], emitter.digest.Sum(nil))
	return Summary{
		Encoding:  encoding,
		FinalLF:   emitter.finalLF,
		SHA256:    digest,
		RawRead:   rawRead,
		LineCount: emitter.counter.value,
	}
}
