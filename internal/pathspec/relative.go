package pathspec

import (
	"strings"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

// ParseRelative validates raw without trimming, cleaning, Unicode normalization,
// alias resolution, or filesystem access.
func ParseRelative(target TargetOS, raw string, allowRoot bool) (Relative, api.ErrorCode) {
	switch target {
	case POSIX:
		return parsePOSIXRelative(raw, allowRoot)
	case Windows:
		return parseWindowsRelative(raw, allowRoot)
	default:
		return Relative{}, api.ErrorInvalidInput
	}
}

// ParseRootDirectory validates one absolute root directory without contacting the filesystem.
func ParseRootDirectory(target TargetOS, raw string) (RootDirectory, api.ErrorCode) {
	switch target {
	case POSIX:
		return parsePOSIXRootDirectory(raw)
	case Windows:
		return parseWindowsRootDirectory(raw)
	default:
		return RootDirectory{}, api.ErrorInvalidInput
	}
}

// AppendDiscovered validates one actual enumerated component and appends it
// atomically. A rejected name never produces a partial or truncated path.
func AppendDiscovered(parent Relative, component string) (Relative, bool) {
	if parent.normalized == "" || !validPathString(component) || component == "." || component == ".." {
		return Relative{}, false
	}
	switch parent.target {
	case POSIX:
		if strings.ContainsRune(component, '/') {
			return Relative{}, false
		}
	case Windows:
		if strings.ContainsAny(component, `/\`) || !validWindowsComponent(component) {
			return Relative{}, false
		}
	default:
		return Relative{}, false
	}

	normalizedLength := len(component)
	if parent.normalized != "." {
		normalizedLength += len(parent.normalized) + 1
	}
	if normalizedLength > maxPathBytes {
		return Relative{}, false
	}

	components := make([]string, len(parent.components)+1)
	copy(components, parent.components)
	components[len(parent.components)] = component
	normalized := component
	if parent.normalized != "." {
		normalized = parent.normalized + "/" + component
	}
	return Relative{target: parent.target, normalized: normalized, components: components}, true
}
