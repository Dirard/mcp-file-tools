package jsonwire

import "unicode/utf8"

type utf8Iterator struct {
	raw      []byte
	position int
}

func newUTF8Iterator(raw []byte) utf8Iterator {
	return utf8Iterator{raw: raw}
}

func (iterator *utf8Iterator) next() (rune, Span, bool, error) {
	if iterator.position == len(iterator.raw) {
		return 0, Span{}, false, nil
	}
	start := iterator.position
	value, width := utf8.DecodeRune(iterator.raw[start:])
	if value == utf8.RuneError && width == 1 {
		return 0, Span{}, false, newValidationError(KindUnicode, start)
	}
	iterator.position += width
	return value, Span{Start: start, End: iterator.position}, true, nil
}

func decodeJSONString(raw []byte, start int) (string, Span, error) {
	return scanJSONString(raw, start, ^uint64(0), true)
}

func scanJSONString(raw []byte, start int, maxDecodedBytes uint64, capture bool) (string, Span, error) {
	return scanJSONStringSemantics(raw, start, maxDecodedBytes, capture, true)
}

func scanJSONStringSemantics(raw []byte, start int, maxDecodedBytes uint64, capture, validateSurrogates bool) (string, Span, error) {
	if start < 0 || start >= len(raw) || raw[start] != '"' {
		return "", Span{}, newValidationError(KindSyntax, boundedPosition(start, len(raw)))
	}
	position := start + 1
	var decoded []byte
	if capture {
		capacity := len(raw) - position
		if capacity > 256 {
			capacity = 256
		}
		decoded = make([]byte, 0, capacity)
	}
	var decodedBytes uint64

	for position < len(raw) {
		current := raw[position]
		switch {
		case current == '"':
			return string(decoded), Span{Start: start, End: position + 1}, nil
		case current == '\\':
			value, next, err := decodeJSONEscape(raw, position, validateSurrogates)
			if err != nil {
				return "", Span{}, err
			}
			width := utf8.RuneLen(value)
			if decodedLimitExceeded(decodedBytes, width, maxDecodedBytes) {
				return "", Span{}, newValidationError(KindResource, position)
			}
			decodedBytes += uint64(width)
			if capture {
				decoded = utf8.AppendRune(decoded, value)
			}
			position = next
		case current < 0x20:
			return "", Span{}, newValidationError(KindSyntax, position)
		case current < utf8.RuneSelf:
			if decodedLimitExceeded(decodedBytes, 1, maxDecodedBytes) {
				return "", Span{}, newValidationError(KindResource, position)
			}
			decodedBytes++
			if capture {
				decoded = append(decoded, current)
			}
			position++
		default:
			iterator := utf8Iterator{raw: raw, position: position}
			_, scalarSpan, ok, err := iterator.next()
			if err != nil {
				return "", Span{}, err
			}
			if !ok {
				return "", Span{}, newValidationError(KindSyntax, position)
			}
			width := scalarSpan.End - scalarSpan.Start
			if decodedLimitExceeded(decodedBytes, width, maxDecodedBytes) {
				return "", Span{}, newValidationError(KindResource, position)
			}
			decodedBytes += uint64(width)
			if capture {
				decoded = append(decoded, raw[scalarSpan.Start:scalarSpan.End]...)
			}
			position = scalarSpan.End
		}
	}
	return "", Span{}, newValidationError(KindSyntax, len(raw))
}

func decodedLimitExceeded(current uint64, added int, limit uint64) bool {
	return current > limit || uint64(added) > limit-current
}

func decodeJSONEscape(raw []byte, slash int, validateSurrogates bool) (rune, int, error) {
	if slash+1 >= len(raw) {
		return 0, slash, newValidationError(KindSyntax, len(raw))
	}
	escape := raw[slash+1]
	switch escape {
	case '"', '\\', '/':
		return rune(escape), slash + 2, nil
	case 'b':
		return '\b', slash + 2, nil
	case 'f':
		return '\f', slash + 2, nil
	case 'n':
		return '\n', slash + 2, nil
	case 'r':
		return '\r', slash + 2, nil
	case 't':
		return '\t', slash + 2, nil
	case 'u':
		return decodeJSONUnicodeEscape(raw, slash, validateSurrogates)
	default:
		return 0, slash, newValidationError(KindSyntax, slash+1)
	}
}

func decodeJSONUnicodeEscape(raw []byte, slash int, validateSurrogates bool) (rune, int, error) {
	value, end, err := decodeJSONHex4(raw, slash+2)
	if err != nil {
		return 0, slash, err
	}
	if value >= 0xd800 && value <= 0xdbff {
		if end+2 <= len(raw) && raw[end] == '\\' && raw[end+1] == 'u' {
			low, next, err := decodeJSONHex4(raw, end+2)
			if err != nil {
				return 0, slash, err
			}
			if low >= 0xdc00 && low <= 0xdfff {
				value = 0x10000 + (value-0xd800)*0x400 + (low - 0xdc00)
				return rune(value), next, nil
			}
		}
		if validateSurrogates {
			return 0, slash, newValidationError(KindUnicode, slash)
		}
		return utf8.RuneError, end, nil
	} else if value >= 0xdc00 && value <= 0xdfff {
		if validateSurrogates {
			return 0, slash, newValidationError(KindUnicode, slash)
		}
		return utf8.RuneError, end, nil
	}
	return rune(value), end, nil
}

func decodeJSONHex4(raw []byte, start int) (uint32, int, error) {
	if start < 0 || start+4 > len(raw) {
		return 0, start, newValidationError(KindSyntax, len(raw))
	}
	var value uint32
	for position := start; position < start+4; position++ {
		nibble, ok := hexNibble(raw[position])
		if !ok {
			return 0, position, newValidationError(KindSyntax, position)
		}
		value = value<<4 | uint32(nibble)
	}
	return value, start + 4, nil
}

func hexNibble(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}

func boundedPosition(position, length int) int {
	if position < 0 {
		return 0
	}
	if position > length {
		return length
	}
	return position
}
