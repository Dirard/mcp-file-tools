package pathspec

import (
	"strings"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

func parseWindowsRelative(raw string, allowRoot bool) (Relative, api.ErrorCode) {
	if !validPathString(raw) {
		return Relative{}, api.ErrorInvalidInput
	}
	if raw == "." {
		if !allowRoot {
			return Relative{}, api.ErrorInvalidInput
		}
		return Relative{target: Windows, normalized: raw}, ""
	}
	if windowsPathIsRooted(raw) {
		return Relative{}, api.ErrorPathOutsideCWD
	}

	components, normalized, code := parseWindowsComponents(raw, api.ErrorPathOutsideCWD)
	if code != "" {
		return Relative{}, code
	}
	return Relative{target: Windows, normalized: normalized, components: components}, ""
}

func parseWindowsRootDirectory(raw string) (RootDirectory, api.ErrorCode) {
	if !validPathString(raw) || len(raw) < 3 || !asciiLetter(raw[0]) || raw[1] != ':' || !windowsSeparator(raw[2]) {
		return RootDirectory{}, api.ErrorInvalidInput
	}

	prefix := raw[:2] + "/"
	clean := make([]string, 0, strings.Count(raw[3:], "/")+strings.Count(raw[3:], `\`)+1)
	for _, component := range strings.Split(strings.ReplaceAll(raw[3:], `\`, "/"), "/") {
		switch component {
		case "", ".":
			continue
		case "..":
			return RootDirectory{}, api.ErrorInvalidInput
		}
		if !validWindowsComponent(component) {
			return RootDirectory{}, api.ErrorInvalidInput
		}
		clean = append(clean, component)
	}
	if len(clean) == 0 {
		return RootDirectory{target: Windows, normalized: prefix}, ""
	}
	normalized := prefix + strings.Join(clean, "/")
	return RootDirectory{target: Windows, normalized: normalized, components: strings.Split(normalized[3:], "/")}, ""
}

func parseWindowsComponents(raw string, parentCode api.ErrorCode) ([]string, string, api.ErrorCode) {
	normalized := strings.ReplaceAll(raw, `\`, "/")
	components := strings.Split(normalized, "/")
	for _, component := range components {
		switch component {
		case "", ".":
			return nil, "", api.ErrorInvalidInput
		case "..":
			return nil, "", parentCode
		}
		if !validWindowsComponent(component) {
			return nil, "", api.ErrorInvalidInput
		}
	}
	return components, normalized, ""
}

func validWindowsComponent(component string) bool {
	for _, character := range component {
		if character <= 0x1f {
			return false
		}
		switch character {
		case '<', '>', ':', '"', '|', '?', '*':
			return false
		}
	}
	last := component[len(component)-1]
	if last == '.' || last == ' ' {
		return false
	}
	return !windowsReservedName(component)
}

func windowsReservedName(component string) bool {
	base := component
	if extension := strings.IndexByte(base, '.'); extension >= 0 {
		base = base[:extension]
	}
	for _, reserved := range [...]string{"CON", "PRN", "AUX", "NUL", "CLOCK$", "CONIN$", "CONOUT$"} {
		if equalASCII(base, reserved) {
			return true
		}
	}
	if len(base) <= 3 {
		return false
	}
	prefix := base[:3]
	if !equalASCII(prefix, "COM") && !equalASCII(prefix, "LPT") {
		return false
	}
	switch base[3:] {
	case "1", "2", "3", "4", "5", "6", "7", "8", "9", "¹", "²", "³":
		return true
	default:
		return false
	}
}

func windowsPathIsRooted(raw string) bool {
	return windowsSeparator(raw[0]) || len(raw) >= 2 && asciiLetter(raw[0]) && raw[1] == ':'
}

func windowsSeparator(character byte) bool {
	return character == '/' || character == '\\'
}

func asciiLetter(character byte) bool {
	return character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z'
}

func equalASCII(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		leftByte := left[index]
		if leftByte >= 'a' && leftByte <= 'z' {
			leftByte -= 'a' - 'A'
		}
		if leftByte != right[index] {
			return false
		}
	}
	return true
}
