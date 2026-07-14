package rootfs

import (
	"strings"

	"github.com/Dirard/mcp-file-tools/internal/pathspec"
)

func windowsEnumerationOutcome(parent pathspec.Relative, rawName []uint16, kind EntryKind, identity Identity, identityKnown bool) EnumerationOutcome {
	name, ok := decodeWindowsUTF16(rawName)
	if !ok {
		return EnumerationOutcome{
			disposition:  EnumerationPathEncodingUnsupported,
			boundaryKind: kind,
		}
	}
	return enumerationCandidate(parent, name, kind, identity, identityKnown)
}

func decodeWindowsUTF16(raw []uint16) (string, bool) {
	var decoded strings.Builder
	decoded.Grow(len(raw))
	for index := 0; index < len(raw); index++ {
		unit := raw[index]
		switch {
		case unit >= 0xd800 && unit <= 0xdbff:
			if index+1 >= len(raw) {
				return "", false
			}
			low := raw[index+1]
			if low < 0xdc00 || low > 0xdfff {
				return "", false
			}
			character := rune(0x10000 + (uint32(unit)-0xd800)<<10 + uint32(low) - 0xdc00)
			decoded.WriteRune(character)
			index++
		case unit >= 0xdc00 && unit <= 0xdfff:
			return "", false
		default:
			decoded.WriteRune(rune(unit))
		}
	}
	return decoded.String(), true
}
