package pathspec

import (
	"strings"
	"unicode/utf8"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

func parsePOSIXRelative(raw string, allowRoot bool) (Relative, api.ErrorCode) {
	if !validPathString(raw) {
		return Relative{}, api.ErrorInvalidInput
	}
	if raw == "." {
		if !allowRoot {
			return Relative{}, api.ErrorInvalidInput
		}
		return Relative{target: POSIX, normalized: raw}, ""
	}
	if raw[0] == '/' {
		return Relative{}, api.ErrorPathOutsideCWD
	}

	components := strings.Split(raw, "/")
	for _, component := range components {
		switch component {
		case "":
			return Relative{}, api.ErrorInvalidInput
		case ".":
			return Relative{}, api.ErrorInvalidInput
		case "..":
			return Relative{}, api.ErrorPathOutsideCWD
		}
	}
	return Relative{target: POSIX, normalized: raw, components: components}, ""
}

func parsePOSIXRootDirectory(raw string) (RootDirectory, api.ErrorCode) {
	if !validPathString(raw) || raw[0] != '/' {
		return RootDirectory{}, api.ErrorInvalidInput
	}
	clean := make([]string, 0, strings.Count(raw, "/"))
	for _, component := range strings.Split(raw[1:], "/") {
		switch component {
		case "", ".":
			continue
		case "..":
			return RootDirectory{}, api.ErrorInvalidInput
		}
		clean = append(clean, component)
	}
	if len(clean) == 0 {
		return RootDirectory{target: POSIX, normalized: "/"}, ""
	}
	normalized := "/" + strings.Join(clean, "/")
	return RootDirectory{target: POSIX, normalized: normalized, components: strings.Split(normalized[1:], "/")}, ""
}

func validPathString(raw string) bool {
	return len(raw) >= 1 && len(raw) <= maxPathBytes && utf8.ValidString(raw) && !strings.ContainsRune(raw, 0)
}
