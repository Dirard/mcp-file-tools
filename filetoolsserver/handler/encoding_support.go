package handler

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/Dirard/mcp-file-tools/internal/encoding"
	textEncoding "golang.org/x/text/encoding"
	"golang.org/x/text/transform"
)

type encodingResult struct {
	encoder            textEncoding.Encoding
	name               string
	detectedEncoding   string
	encodingConfidence int
	autoDetected       bool
}

// resolveEncoding returns an explicit encoding or auto-detects based on file size.
func (h *Handler) resolveEncoding(inputEncoding string, filePath string) (encodingResult, error) {
	detectionMode := "full"
	if loadToMemory, _ := h.shouldLoadEntireFile(filePath); !loadToMemory {
		detectionMode = "sample"
	}
	return h.resolveEncodingWithDetectionMode(inputEncoding, filePath, detectionMode)
}

func (h *Handler) resolveEncodingSample(inputEncoding string, filePath string) (encodingResult, error) {
	return h.resolveEncodingWithDetectionMode(inputEncoding, filePath, "sample")
}

func (h *Handler) resolveEncodingWithDetectionMode(inputEncoding string, filePath string, detectionMode string) (encodingResult, error) {
	result := encodingResult{}

	if inputEncoding != "" {
		result.name = strings.ToLower(inputEncoding)
		if isUTF32Encoding(result.name) {
			return result, nil
		}
		enc, ok := encoding.Get(result.name)
		if !ok {
			return result, fmt.Errorf("%w: %s", ErrEncodingUnsupported, result.name)
		}
		result.encoder = enc
		return result, nil
	}

	result.autoDetected = true
	detection, err := encoding.DetectFromFile(filePath, detectionMode)
	if err != nil {
		result.name = "utf-8"
		result.detectedEncoding = "detection failed, using utf-8"
		return result, nil
	}
	result.detectedEncoding = detection.Charset
	result.encodingConfidence = detection.Confidence

	if detection.Confidence >= encoding.MinConfidenceThreshold && detection.Charset != "" {
		result.name = detection.Charset
	} else {
		result.name = "utf-8"
		if detection.Charset != "" {
			result.detectedEncoding = detection.Charset + " (low confidence, using utf-8)"
		}
	}

	if isUTF32Encoding(result.name) {
		return result, nil
	}

	enc, ok := encoding.Get(result.name)
	if !ok {
		result.name = "utf-8"
		result.detectedEncoding += " (unsupported, using utf-8)"
	} else {
		result.encoder = enc
	}
	return result, nil
}

func decodedReader(r io.Reader, encResult encodingResult) io.Reader {
	if isUTF32Encoding(encResult.name) {
		return &utf32Reader{reader: r, littleEndian: strings.EqualFold(encResult.name, "utf-32-le")}
	}
	if encoding.IsUTF8(encResult.name) || encResult.encoder == nil {
		return r
	}
	return transform.NewReader(r, encResult.encoder.NewDecoder())
}

type utf32Reader struct {
	reader       io.Reader
	littleEndian bool
	checkedBOM   bool
	pending      []byte
}

func (r *utf32Reader) Read(p []byte) (int, error) {
	written := 0
	for written < len(p) {
		if len(r.pending) > 0 {
			n := copy(p[written:], r.pending)
			written += n
			r.pending = r.pending[n:]
			continue
		}

		next, err := r.nextRune()
		if err != nil {
			if err == io.EOF && written > 0 {
				return written, nil
			}
			return written, err
		}

		var encoded [utf8.UTFMax]byte
		n := utf8.EncodeRune(encoded[:], next)
		r.pending = encoded[:n]
	}
	return written, nil
}

func (r *utf32Reader) nextRune() (rune, error) {
	unit, err := r.readCodeUnit()
	if err != nil {
		if err == io.ErrUnexpectedEOF {
			return utf8.RuneError, nil
		}
		return 0, err
	}

	if !r.checkedBOM {
		r.checkedBOM = true
		if unit[0] == 0xFF && unit[1] == 0xFE && unit[2] == 0x00 && unit[3] == 0x00 {
			r.littleEndian = true
			return r.nextRune()
		}
		if unit[0] == 0x00 && unit[1] == 0x00 && unit[2] == 0xFE && unit[3] == 0xFF {
			r.littleEndian = false
			return r.nextRune()
		}
	}

	var codePoint uint32
	if r.littleEndian {
		codePoint = uint32(unit[0]) | uint32(unit[1])<<8 | uint32(unit[2])<<16 | uint32(unit[3])<<24
	} else {
		codePoint = uint32(unit[0])<<24 | uint32(unit[1])<<16 | uint32(unit[2])<<8 | uint32(unit[3])
	}
	next := rune(codePoint)
	if !utf8.ValidRune(next) {
		next = utf8.RuneError
	}
	return next, nil
}

func (r *utf32Reader) readCodeUnit() ([4]byte, error) {
	var unit [4]byte
	n, err := io.ReadFull(r.reader, unit[:])
	if err == nil {
		return unit, nil
	}
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		if n == 0 {
			return unit, io.EOF
		}
		return unit, io.ErrUnexpectedEOF
	}
	return unit, err
}

func decodeFileContent(data []byte, forcedEncoding string) (string, string) {
	var encodingName string
	if forcedEncoding != "" {
		encodingName = strings.ToLower(forcedEncoding)
	} else {
		detection, _ := encoding.DetectSample(data)
		if detection.Charset != "" {
			encodingName = detection.Charset
		} else {
			encodingName = "utf-8"
		}
	}
	if encoding.IsUTF8(encodingName) {
		return string(data), encodingName
	}
	if isUTF32Encoding(encodingName) {
		return decodeUTF32(data, encodingName), encodingName
	}
	enc, ok := encoding.Get(encodingName)
	if !ok {
		return string(data), "utf-8"
	}
	decoder := enc.NewDecoder()
	decoded, err := decoder.Bytes(data)
	if err != nil {
		return string(data), "utf-8"
	}
	return string(decoded), encodingName
}

func isUTF32Encoding(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return name == "utf-32-le" || name == "utf-32-be"
}

func decodeUTF32(data []byte, encodingName string) string {
	littleEndian := strings.EqualFold(encodingName, "utf-32-le")
	offset := 0
	if len(data) >= 4 {
		if data[0] == 0xFF && data[1] == 0xFE && data[2] == 0x00 && data[3] == 0x00 {
			littleEndian = true
			offset = 4
		} else if data[0] == 0x00 && data[1] == 0x00 && data[2] == 0xFE && data[3] == 0xFF {
			littleEndian = false
			offset = 4
		}
	}
	var b strings.Builder
	for i := offset; i+3 < len(data); i += 4 {
		var codePoint uint32
		if littleEndian {
			codePoint = uint32(data[i]) | uint32(data[i+1])<<8 | uint32(data[i+2])<<16 | uint32(data[i+3])<<24
		} else {
			codePoint = uint32(data[i])<<24 | uint32(data[i+1])<<16 | uint32(data[i+2])<<8 | uint32(data[i+3])
		}
		r := rune(codePoint)
		if !utf8.ValidRune(r) {
			r = utf8.RuneError
		}
		b.WriteRune(r)
	}
	return b.String()
}
