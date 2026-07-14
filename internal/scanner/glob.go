package scanner

import (
	"strings"
	"unicode"
	"unicode/utf8"
	"unsafe"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

type globAtomKind uint8

const (
	globLiteral globAtomKind = iota + 1
	globAnyScalar
	globAnyScalars
	globClass
)

type globRange struct {
	first rune
	last  rune
}

type globAtom struct {
	kind    globAtomKind
	scalar  rune
	negated bool
	ranges  []globRange
}

type globSegment struct {
	globstar bool
	atoms    []globAtom
}

type globVariant struct {
	segments []globSegment
}

// Glob is an immutable compiled pattern. Its slices are never exposed.
type Glob struct {
	variants   []globVariant
	fullPath   bool
	ignoreCase bool
	footprint  uint64
}

// CompileGlob compiles the closed, platform-independent v2 grammar.
func CompileGlob(pattern string, ignoreCase bool) (Glob, api.ErrorCode) {
	if pattern == "" || len(pattern) > api.InputStringMaxBytes || !utf8.ValidString(pattern) {
		return Glob{}, api.ErrorInvalidInput
	}
	expanded, ok := expandGlobBrace(pattern)
	if !ok {
		return Glob{}, api.ErrorInvalidInput
	}
	variants := make([]globVariant, 0, len(expanded))
	for _, variantPattern := range expanded {
		variant, valid := compileGlobVariant(variantPattern)
		if !valid {
			return Glob{}, api.ErrorInvalidInput
		}
		variants = append(variants, variant)
	}
	glob := Glob{
		variants:   variants,
		fullPath:   hasUnescapedSlash(pattern),
		ignoreCase: ignoreCase,
	}
	glob.footprint = glob.retainedFootprint()
	return glob, ""
}

// Match applies basename fallback when the original pattern had no unescaped slash.
func (glob Glob) Match(path string) bool {
	if path == "" || !utf8.ValidString(path) {
		return false
	}
	target := path
	if !glob.fullPath {
		if slash := strings.LastIndexByte(path, '/'); slash >= 0 {
			target = path[slash+1:]
		}
	}
	parts := strings.Split(target, "/")
	for _, variant := range glob.variants {
		if matchGlobVariant(variant, parts, glob.ignoreCase) {
			return true
		}
	}
	return false
}

// Footprint reports all retained compiled storage.
func (glob Glob) Footprint() uint64 {
	return glob.footprint
}

func expandGlobBrace(pattern string) ([]string, bool) {
	open, close := -1, -1
	classDepth := 0
	escaped := false
	for index := 0; index < len(pattern); index++ {
		character := pattern[index]
		if escaped {
			escaped = false
			continue
		}
		if character == '\\' {
			escaped = true
			continue
		}
		if character == '[' {
			classDepth++
			continue
		}
		if character == ']' && classDepth > 0 {
			classDepth--
			continue
		}
		if classDepth != 0 {
			continue
		}
		switch character {
		case '{':
			if open >= 0 || close >= 0 {
				return nil, false
			}
			open = index
		case '}':
			if open < 0 || close >= 0 {
				return nil, false
			}
			close = index
		}
	}
	if escaped || (open >= 0) != (close >= 0) || (open >= 0 && close < open) {
		return nil, false
	}
	if open < 0 {
		return []string{pattern}, true
	}
	inside := pattern[open+1 : close]
	if strings.ContainsAny(inside, "{}") {
		return nil, false
	}
	alternatives := strings.Split(inside, ",")
	if len(alternatives) < 2 || len(alternatives) > 32 {
		return nil, false
	}
	variants := make([]string, 0, len(alternatives))
	totalBytes := 0
	for _, alternative := range alternatives {
		if alternative == "" {
			return nil, false
		}
		variant := pattern[:open] + alternative + pattern[close+1:]
		totalBytes += len(variant)
		if totalBytes > 65_536 {
			return nil, false
		}
		variants = append(variants, variant)
	}
	return variants, true
}

func hasUnescapedSlash(pattern string) bool {
	escaped := false
	for index := 0; index < len(pattern); index++ {
		if escaped {
			escaped = false
			continue
		}
		if pattern[index] == '\\' {
			escaped = true
			continue
		}
		if pattern[index] == '/' {
			return true
		}
	}
	return false
}

func compileGlobVariant(pattern string) (globVariant, bool) {
	parts := splitGlobSegments(pattern)
	if parts == nil {
		return globVariant{}, false
	}
	segments := make([]globSegment, 0, len(parts))
	for _, part := range parts {
		if part == "**" {
			segments = append(segments, globSegment{globstar: true})
			continue
		}
		atoms, ok := compileGlobSegment(part)
		if !ok {
			return globVariant{}, false
		}
		segments = append(segments, globSegment{atoms: atoms})
	}
	return globVariant{segments: segments}, true
}

func splitGlobSegments(pattern string) []string {
	parts := make([]string, 0, strings.Count(pattern, "/")+1)
	start := 0
	escaped := false
	for index := 0; index < len(pattern); index++ {
		if escaped {
			escaped = false
			continue
		}
		if pattern[index] == '\\' {
			escaped = true
			continue
		}
		if pattern[index] != '/' {
			continue
		}
		if index == start {
			return nil
		}
		parts = append(parts, pattern[start:index])
		start = index + 1
	}
	if escaped || start == len(pattern) {
		return nil
	}
	parts = append(parts, pattern[start:])
	return parts
}

func compileGlobSegment(segment string) ([]globAtom, bool) {
	atoms := make([]globAtom, 0, utf8.RuneCountInString(segment))
	for index := 0; index < len(segment); {
		character := segment[index]
		switch character {
		case '*':
			if index+1 < len(segment) && segment[index+1] == '*' {
				return nil, false
			}
			atoms = append(atoms, globAtom{kind: globAnyScalars})
			index++
		case '?':
			atoms = append(atoms, globAtom{kind: globAnyScalar})
			index++
		case '[':
			atom, next, ok := compileGlobClass(segment, index+1)
			if !ok {
				return nil, false
			}
			atoms = append(atoms, atom)
			index = next
		case ']', '{', '}':
			return nil, false
		case '\\':
			if index+1 >= len(segment) || !strings.ContainsRune("*?[]{}\\", rune(segment[index+1])) {
				return nil, false
			}
			atoms = append(atoms, globAtom{kind: globLiteral, scalar: rune(segment[index+1])})
			index += 2
		default:
			scalar, size := utf8.DecodeRuneInString(segment[index:])
			if scalar == utf8.RuneError && size == 1 {
				return nil, false
			}
			atoms = append(atoms, globAtom{kind: globLiteral, scalar: scalar})
			index += size
		}
	}
	return atoms, len(atoms) != 0
}

func compileGlobClass(segment string, index int) (globAtom, int, bool) {
	atom := globAtom{kind: globClass}
	if index < len(segment) && segment[index] == '!' {
		atom.negated = true
		index++
	}
	for {
		if index >= len(segment) || segment[index] == ']' {
			return globAtom{}, 0, false
		}
		first, next, ok := parseClassScalar(segment, index)
		if !ok {
			return globAtom{}, 0, false
		}
		index = next
		last := first
		if index < len(segment) && segment[index] == '-' {
			last, index, ok = parseClassScalar(segment, index+1)
			if !ok || first > last {
				return globAtom{}, 0, false
			}
		}
		atom.ranges = append(atom.ranges, globRange{first: first, last: last})
		if index < len(segment) && segment[index] == ']' {
			return atom, index + 1, true
		}
	}
}

func parseClassScalar(segment string, index int) (rune, int, bool) {
	if index >= len(segment) {
		return 0, 0, false
	}
	if segment[index] == '\\' {
		if index+1 >= len(segment) || !strings.ContainsRune("\\]-!", rune(segment[index+1])) {
			return 0, 0, false
		}
		return rune(segment[index+1]), index + 2, true
	}
	if strings.ContainsRune("/\\]-", rune(segment[index])) {
		return 0, 0, false
	}
	scalar, size := utf8.DecodeRuneInString(segment[index:])
	if scalar == utf8.RuneError && size == 1 {
		return 0, 0, false
	}
	return scalar, index + size, true
}

func matchGlobVariant(variant globVariant, path []string, ignoreCase bool) bool {
	patternIndex, pathIndex := 0, 0
	globstarIndex, globstarPath := -1, 0
	for pathIndex < len(path) {
		if patternIndex < len(variant.segments) && variant.segments[patternIndex].globstar {
			globstarIndex = patternIndex
			globstarPath = pathIndex
			patternIndex++
			continue
		}
		if patternIndex < len(variant.segments) && matchGlobSegment(variant.segments[patternIndex], path[pathIndex], ignoreCase) {
			patternIndex++
			pathIndex++
			continue
		}
		if globstarIndex >= 0 {
			globstarPath++
			pathIndex = globstarPath
			patternIndex = globstarIndex + 1
			continue
		}
		return false
	}
	for patternIndex < len(variant.segments) && variant.segments[patternIndex].globstar {
		patternIndex++
	}
	return patternIndex == len(variant.segments)
}

func matchGlobSegment(segment globSegment, value string, ignoreCase bool) bool {
	characters := []rune(value)
	patternIndex, valueIndex := 0, 0
	starIndex, starValue := -1, 0
	for valueIndex < len(characters) {
		if patternIndex < len(segment.atoms) && segment.atoms[patternIndex].kind == globAnyScalars {
			starIndex = patternIndex
			starValue = valueIndex
			patternIndex++
			continue
		}
		if patternIndex < len(segment.atoms) && matchGlobAtom(segment.atoms[patternIndex], characters[valueIndex], ignoreCase) {
			patternIndex++
			valueIndex++
			continue
		}
		if starIndex >= 0 {
			starValue++
			valueIndex = starValue
			patternIndex = starIndex + 1
			continue
		}
		return false
	}
	for patternIndex < len(segment.atoms) && segment.atoms[patternIndex].kind == globAnyScalars {
		patternIndex++
	}
	return patternIndex == len(segment.atoms)
}

func matchGlobAtom(atom globAtom, value rune, ignoreCase bool) bool {
	switch atom.kind {
	case globLiteral:
		return value == atom.scalar || (ignoreCase && sameSimpleFoldOrbit(value, atom.scalar))
	case globAnyScalar:
		return value != '/'
	case globClass:
		matched := classContains(atom.ranges, value)
		if ignoreCase && !matched {
			for folded := unicode.SimpleFold(value); folded != value; folded = unicode.SimpleFold(folded) {
				if classContains(atom.ranges, folded) {
					matched = true
					break
				}
			}
		}
		if atom.negated {
			return !matched
		}
		return matched
	default:
		return false
	}
}

func classContains(ranges []globRange, value rune) bool {
	for _, candidate := range ranges {
		if candidate.first <= value && value <= candidate.last {
			return true
		}
	}
	return false
}

func sameSimpleFoldOrbit(left, right rune) bool {
	if left == right {
		return true
	}
	for folded := unicode.SimpleFold(left); folded != left; folded = unicode.SimpleFold(folded) {
		if folded == right {
			return true
		}
	}
	return false
}

func (glob Glob) retainedFootprint() uint64 {
	bytes := uint64(unsafe.Sizeof(Glob{})) + uint64(cap(glob.variants))*uint64(unsafe.Sizeof(globVariant{}))
	for _, variant := range glob.variants {
		bytes += uint64(cap(variant.segments)) * uint64(unsafe.Sizeof(globSegment{}))
		for _, segment := range variant.segments {
			bytes += uint64(cap(segment.atoms)) * uint64(unsafe.Sizeof(globAtom{}))
			for _, atom := range segment.atoms {
				bytes += uint64(cap(atom.ranges)) * uint64(unsafe.Sizeof(globRange{}))
			}
		}
	}
	return bytes
}
