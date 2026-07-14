package textio

import (
	"encoding/binary"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

type byteSource struct {
	raw      *rawReader
	prefix   []byte
	position int
}

func detectEncoding(raw *rawReader) (Encoding, *byteSource, api.ErrorCode) {
	prefix := make([]byte, 0, 4)
	present, code := appendPrefixByte(raw, &prefix)
	if code != "" {
		return "", nil, code
	}
	if !present {
		return EncodingUTF8, &byteSource{raw: raw}, ""
	}
	switch prefix[0] {
	case 0xef:
		if present, code = appendPrefixByte(raw, &prefix); code != "" {
			return "", nil, code
		} else if !present || prefix[1] != 0xbb {
			return EncodingUTF8, &byteSource{raw: raw, prefix: prefix}, ""
		}
		if present, code = appendPrefixByte(raw, &prefix); code != "" {
			return "", nil, code
		} else if present && prefix[2] == 0xbf {
			return EncodingUTF8, &byteSource{raw: raw}, ""
		}
		return EncodingUTF8, &byteSource{raw: raw, prefix: prefix}, ""
	case 0xfe:
		if present, code = appendPrefixByte(raw, &prefix); code != "" {
			return "", nil, code
		} else if present && prefix[1] == 0xff {
			return EncodingUTF16BE, &byteSource{raw: raw}, ""
		}
		return EncodingUTF8, &byteSource{raw: raw, prefix: prefix}, ""
	case 0xff:
		if present, code = appendPrefixByte(raw, &prefix); code != "" {
			return "", nil, code
		} else if !present || prefix[1] != 0xfe {
			return EncodingUTF8, &byteSource{raw: raw, prefix: prefix}, ""
		}
		if present, code = appendPrefixByte(raw, &prefix); code != "" {
			return "", nil, code
		} else if !present || prefix[2] != 0x00 {
			return EncodingUTF16LE, &byteSource{raw: raw, prefix: prefix[2:]}, ""
		}
		if present, code = appendPrefixByte(raw, &prefix); code != "" {
			return "", nil, code
		} else if present && prefix[3] == 0x00 {
			return EncodingUTF32LE, &byteSource{raw: raw}, ""
		}
		return EncodingUTF16LE, &byteSource{raw: raw, prefix: prefix[2:]}, ""
	case 0x00:
		if present, code = appendPrefixByte(raw, &prefix); code != "" {
			return "", nil, code
		} else if !present || prefix[1] != 0x00 {
			return EncodingUTF8, &byteSource{raw: raw, prefix: prefix}, ""
		}
		if present, code = appendPrefixByte(raw, &prefix); code != "" {
			return "", nil, code
		} else if !present || prefix[2] != 0xfe {
			return EncodingUTF8, &byteSource{raw: raw, prefix: prefix}, ""
		}
		if present, code = appendPrefixByte(raw, &prefix); code != "" {
			return "", nil, code
		} else if present && prefix[3] == 0xff {
			return EncodingUTF32BE, &byteSource{raw: raw}, ""
		}
		return EncodingUTF8, &byteSource{raw: raw, prefix: prefix}, ""
	default:
		return EncodingUTF8, &byteSource{raw: raw, prefix: prefix}, ""
	}
}

func appendPrefixByte(raw *rawReader, prefix *[]byte) (bool, api.ErrorCode) {
	value, present, code := raw.next()
	if code != "" || !present {
		return present, code
	}
	*prefix = append(*prefix, value)
	return true, ""
}

func (source *byteSource) next() (byte, bool, api.ErrorCode) {
	if source.position < len(source.prefix) {
		value := source.prefix[source.position]
		source.position++
		return value, true, ""
	}
	return source.raw.next()
}

func (source *byteSource) nextWithReadLimit(readLimit int) (byte, bool, api.ErrorCode) {
	if source.position < len(source.prefix) {
		value := source.prefix[source.position]
		source.position++
		return value, true, ""
	}
	return source.raw.nextWithReadLimit(readLimit)
}

func (source *byteSource) consumeIfExact(pattern []byte) api.ErrorCode {
	for index, expected := range pattern {
		value, ok, code := source.nextWithReadLimit(len(pattern) - index)
		if code != "" {
			return code
		}
		if !ok || value != expected {
			return ""
		}
	}
	return ""
}

type scalarDecoder struct {
	encoding Encoding
	source   *byteSource
}

func (decoder *scalarDecoder) nextRune() (rune, bool, api.ErrorCode) {
	switch decoder.encoding {
	case EncodingUTF8:
		return decoder.nextUTF8()
	case EncodingUTF16BE:
		return decoder.nextUTF16(binary.BigEndian)
	case EncodingUTF16LE:
		return decoder.nextUTF16(binary.LittleEndian)
	case EncodingUTF32BE:
		return decoder.nextUTF32(binary.BigEndian)
	case EncodingUTF32LE:
		return decoder.nextUTF32(binary.LittleEndian)
	default:
		return 0, false, api.ErrorUnsupportedEncoding
	}
}

func (decoder *scalarDecoder) nextUTF8() (rune, bool, api.ErrorCode) {
	first, ok, code := decoder.source.next()
	if code != "" || !ok {
		return 0, ok, code
	}
	if first == 0 {
		return 0, false, api.ErrorBinary
	}
	if first < utf8.RuneSelf {
		return rune(first), true, ""
	}
	width := 0
	switch {
	case first >= 0xc2 && first <= 0xdf:
		width = 2
	case first >= 0xe0 && first <= 0xef:
		width = 3
	case first >= 0xf0 && first <= 0xf4:
		width = 4
	default:
		return 0, false, api.ErrorUnsupportedEncoding
	}
	var encoded [utf8.UTFMax]byte
	encoded[0] = first
	for index := 1; index < width; index++ {
		value, present, readCode := decoder.source.next()
		if readCode != "" {
			return 0, false, readCode
		}
		if !present {
			return 0, false, api.ErrorUnsupportedEncoding
		}
		encoded[index] = value
	}
	if !utf8.Valid(encoded[:width]) {
		return 0, false, api.ErrorUnsupportedEncoding
	}
	character, size := utf8.DecodeRune(encoded[:width])
	if size != width {
		return 0, false, api.ErrorUnsupportedEncoding
	}
	return character, true, ""
}

func (decoder *scalarDecoder) nextUTF16(order binary.ByteOrder) (rune, bool, api.ErrorCode) {
	first, present, code := decoder.readUnit16(order)
	if code != "" || !present {
		return 0, present, code
	}
	if first == 0 {
		return 0, false, api.ErrorBinary
	}
	if first >= 0xdc00 && first <= 0xdfff {
		return 0, false, api.ErrorUnsupportedEncoding
	}
	if first < 0xd800 || first > 0xdbff {
		return rune(first), true, ""
	}
	second, present, code := decoder.readUnit16(order)
	if code != "" {
		return 0, false, code
	}
	if !present || second < 0xdc00 || second > 0xdfff {
		return 0, false, api.ErrorUnsupportedEncoding
	}
	return utf16.DecodeRune(rune(first), rune(second)), true, ""
}

func (decoder *scalarDecoder) readUnit16(order binary.ByteOrder) (uint16, bool, api.ErrorCode) {
	first, present, code := decoder.source.next()
	if code != "" || !present {
		return 0, present, code
	}
	second, present, code := decoder.source.next()
	if code != "" {
		return 0, false, code
	}
	if !present {
		return 0, false, api.ErrorUnsupportedEncoding
	}
	var encoded [2]byte
	encoded[0], encoded[1] = first, second
	return order.Uint16(encoded[:]), true, ""
}

func (decoder *scalarDecoder) nextUTF32(order binary.ByteOrder) (rune, bool, api.ErrorCode) {
	var encoded [4]byte
	for index := range encoded {
		value, present, code := decoder.source.next()
		if code != "" {
			return 0, false, code
		}
		if !present {
			if index == 0 {
				return 0, false, ""
			}
			return 0, false, api.ErrorUnsupportedEncoding
		}
		encoded[index] = value
	}
	value := order.Uint32(encoded[:])
	if value == 0 {
		return 0, false, api.ErrorBinary
	}
	if value > utf8.MaxRune || value >= 0xd800 && value <= 0xdfff {
		return 0, false, api.ErrorUnsupportedEncoding
	}
	return rune(value), true, ""
}

func (decoder *scalarDecoder) consumeFollowingLF() api.ErrorCode {
	var pattern []byte
	switch decoder.encoding {
	case EncodingUTF8:
		pattern = []byte{0x0a}
	case EncodingUTF16BE:
		pattern = []byte{0x00, 0x0a}
	case EncodingUTF16LE:
		pattern = []byte{0x0a, 0x00}
	case EncodingUTF32BE:
		pattern = []byte{0x00, 0x00, 0x00, 0x0a}
	case EncodingUTF32LE:
		pattern = []byte{0x0a, 0x00, 0x00, 0x00}
	}
	return decoder.source.consumeIfExact(pattern)
}
