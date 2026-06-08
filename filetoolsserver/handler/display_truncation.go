package handler

import "unicode/utf8"

func truncateDisplayPrefix(text string, maxBytes int, marker string) (string, bool) {
	if maxBytes <= 0 || len([]byte(text)) <= maxBytes {
		return text, false
	}
	limit := maxBytes
	if markerBytes := len([]byte(marker)); markerBytes > 0 && markerBytes < maxBytes {
		limit = maxBytes - markerBytes
	}
	prefix := validUTF8PrefixByBytes(text, limit)
	prefix = trimTrailingBrokenCluster(prefix)
	if len([]byte(prefix))+len([]byte(marker)) <= maxBytes {
		return prefix + marker, true
	}
	return validUTF8PrefixByBytes(prefix, maxBytes), true
}

func truncateDisplaySuffix(text string, maxBytes int, marker string) (string, bool) {
	if maxBytes <= 0 || len([]byte(text)) <= maxBytes {
		return text, false
	}
	limit := maxBytes
	if markerBytes := len([]byte(marker)); markerBytes > 0 && markerBytes < maxBytes {
		limit = maxBytes - markerBytes
	}
	suffix := validUTF8SuffixByBytes(text, limit)
	suffix = trimLeadingBrokenCluster(suffix)
	if len([]byte(marker))+len([]byte(suffix)) <= maxBytes {
		return marker + suffix, true
	}
	return validUTF8SuffixByBytes(suffix, maxBytes), true
}

func validUTF8PrefixByBytes(text string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len([]byte(text)) <= maxBytes {
		return text
	}
	if maxBytes > len(text) {
		maxBytes = len(text)
	}
	for maxBytes > 0 && !utf8.ValidString(text[:maxBytes]) {
		maxBytes--
	}
	if maxBytes <= 0 {
		return ""
	}
	return text[:maxBytes]
}

func validUTF8SuffixByBytes(text string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len([]byte(text)) <= maxBytes {
		return text
	}
	start := len(text) - maxBytes
	for start < len(text) && !utf8.ValidString(text[start:]) {
		start++
	}
	if start >= len(text) {
		return ""
	}
	return text[start:]
}

func trimTrailingBrokenCluster(text string) string {
	for text != "" {
		r, size := utf8.DecodeLastRuneInString(text)
		if r != '\u200d' && !isVariationSelector(r) && !isEmojiModifier(r) {
			return text
		}
		text = text[:len(text)-size]
	}
	return text
}

func trimLeadingBrokenCluster(text string) string {
	for text != "" {
		r, size := utf8.DecodeRuneInString(text)
		if r != '\u200d' && !isVariationSelector(r) && !isEmojiModifier(r) && !isCombiningMark(r) {
			return text
		}
		text = text[size:]
	}
	return text
}

func isVariationSelector(r rune) bool {
	return (r >= '\ufe00' && r <= '\ufe0f') || (r >= '\U000e0100' && r <= '\U000e01ef')
}

func isEmojiModifier(r rune) bool {
	return r >= '\U0001f3fb' && r <= '\U0001f3ff'
}

func isCombiningMark(r rune) bool {
	return (r >= '\u0300' && r <= '\u036f') ||
		(r >= '\u1ab0' && r <= '\u1aff') ||
		(r >= '\u1dc0' && r <= '\u1dff') ||
		(r >= '\u20d0' && r <= '\u20ff') ||
		(r >= '\ufe20' && r <= '\ufe2f')
}
