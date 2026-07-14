package present

import (
	"errors"
	"unicode/utf8"
)

var errInvalidPresentation = errors.New("invalid presentation input")

const lowerHex = "0123456789abcdef"

func QuoteScalar(dst []byte, value string) ([]byte, error) {
	if err := encodeQuotedScalar(value, func(fragment string) {
		dst = append(dst, fragment...)
	}); err != nil {
		return dst, errInvalidPresentation
	}
	return dst, nil
}

func encodeQuotedScalar(value string, emit func(string)) error {
	if !utf8.ValidString(value) {
		return errInvalidPresentation
	}
	emit("\"")
	for index, scalar := range value {
		switch scalar {
		case '"':
			emit("\\\"")
		case '\\':
			emit("\\\\")
		case '\b':
			emit("\\b")
		case '\t':
			emit("\\t")
		case '\n':
			emit("\\n")
		case '\f':
			emit("\\f")
		case '\r':
			emit("\\r")
		default:
			if scalar < 0x20 {
				escaped := [6]byte{'\\', 'u', '0', '0', lowerHex[byte(scalar)>>4], lowerHex[byte(scalar)&0x0f]}
				emit(string(escaped[:]))
			} else {
				size := utf8.RuneLen(scalar)
				emit(value[index : index+size])
			}
		}
	}
	emit("\"")
	return nil
}
