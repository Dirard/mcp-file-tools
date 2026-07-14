package jsonwire

import "strings"

const metaMaxRawBytes = 16_384

// ValidateMeta validates one MCP _meta object without exposing opaque extension
// values. Only top-level keys use META_KEY grammar; nested JSON remains generic.
func ValidateMeta(raw []byte) error {
	if len(raw) > metaMaxRawBytes {
		return newValidationError(KindResource, metaMaxRawBytes)
	}
	view, err := ScanObject(raw, metadataLimits(), ValidateAll)
	if err != nil {
		return err
	}
	for _, member := range view.members {
		if !validMetaKey(member.Key) {
			return newValidationError(KindMismatch, member.KeySpan.Start)
		}
		if member.Key == "progressToken" && member.Kind != String && member.Kind != Number {
			return newValidationError(KindMismatch, member.Value.Start)
		}
	}
	return nil
}

func metadataLimits() Limits {
	return Limits{
		MaxDepth:          16,
		MaxObjectMembers:  1_024,
		MaxContainerItems: 1_024,
		MaxKeyBytes:       256,
		MaxStringBytes:    4_096,
		MaxNumberRawBytes: 256,
	}
}

func validMetaKey(key string) bool {
	slash := strings.IndexByte(key, '/')
	if slash < 0 {
		return validMetaName(key)
	}
	if strings.IndexByte(key[slash+1:], '/') >= 0 {
		return false
	}
	return validMetaPrefix(key[:slash]) && validMetaName(key[slash+1:])
}

func validMetaPrefix(prefix string) bool {
	if prefix == "" {
		return false
	}
	for _, label := range strings.Split(prefix, ".") {
		if !validMetaLabel(label) {
			return false
		}
	}
	return true
}

func validMetaLabel(label string) bool {
	if len(label) == 0 || !isASCIIAlpha(label[0]) {
		return false
	}
	if len(label) == 1 {
		return true
	}
	if !isASCIIAlphanumeric(label[len(label)-1]) {
		return false
	}
	for index := 1; index < len(label)-1; index++ {
		value := label[index]
		if !isASCIIAlphanumeric(value) && value != '-' {
			return false
		}
	}
	return true
}

func validMetaName(name string) bool {
	if name == "" {
		return true
	}
	if !isASCIIAlphanumeric(name[0]) || !isASCIIAlphanumeric(name[len(name)-1]) {
		return false
	}
	for index := 1; index < len(name)-1; index++ {
		value := name[index]
		if !isASCIIAlphanumeric(value) && value != '-' && value != '_' && value != '.' {
			return false
		}
	}
	return true
}

func isASCIIAlpha(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func isASCIIAlphanumeric(value byte) bool {
	return isASCIIAlpha(value) || value >= '0' && value <= '9'
}
