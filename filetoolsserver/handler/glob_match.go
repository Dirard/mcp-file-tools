package handler

import (
	"path/filepath"
	"regexp"
	"strings"
)

type compiledGlobMatcher struct {
	patterns []compiledGlobPattern
}

type compiledGlobPattern struct {
	regex    *regexp.Regexp
	hasSlash bool
}

func newCompiledGlobMatcher(patterns []string) compiledGlobMatcher {
	compiled := make([]compiledGlobPattern, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = filepath.ToSlash(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}
		for _, expanded := range expandBraces(pattern) {
			expanded = filepath.ToSlash(strings.TrimSpace(expanded))
			if expanded == "" {
				continue
			}
			if compiledPattern, ok := compileSingleGlobPattern(expanded); ok {
				compiled = append(compiled, compiledPattern)
			}
		}
	}
	return compiledGlobMatcher{patterns: compiled}
}

func compileSingleGlobPattern(pattern string) (compiledGlobPattern, bool) {
	re, err := regexp.Compile("^" + globPatternToRegexp(pattern) + "$")
	if err != nil {
		return compiledGlobPattern{}, false
	}
	return compiledGlobPattern{
		regex:    re,
		hasSlash: strings.Contains(pattern, "/"),
	}, true
}

func (m compiledGlobMatcher) matches(path string) bool {
	path = filepath.ToSlash(path)
	for _, pattern := range m.patterns {
		if pattern.matches(path) {
			return true
		}
	}
	return false
}

func (p compiledGlobPattern) matches(path string) bool {
	if p.regex.MatchString(path) {
		return true
	}
	if !p.hasSlash {
		if forEachPathSuffixCandidate(path, func(candidate string) bool {
			return p.regex.MatchString(candidate)
		}) {
			return true
		}
		if p.regex.MatchString(filepath.Base(path)) {
			return true
		}
	}
	return false
}

type compiledIgnoreMatcher struct {
	patterns []compiledIgnorePattern
}

type compiledIgnorePattern struct {
	matcher     compiledGlobMatcher
	dirPatterns []compiledIgnoreDirPattern
}

type compiledIgnoreDirPattern struct {
	value   string
	matcher compiledGlobMatcher
}

func newCompiledIgnoreMatcher(ignoreGlobs []string) compiledIgnoreMatcher {
	patterns := make([]compiledIgnorePattern, 0, len(ignoreGlobs))
	for _, raw := range ignoreGlobs {
		pattern := filepath.ToSlash(strings.TrimSpace(raw))
		if pattern == "" {
			continue
		}
		compiled := compiledIgnorePattern{
			matcher: newCompiledGlobMatcher([]string{pattern}),
		}
		for _, expanded := range expandBraces(pattern) {
			expanded = filepath.ToSlash(strings.TrimSpace(expanded))
			if expanded == "" {
				continue
			}
			if strings.HasSuffix(expanded, "/**") {
				dirPattern := strings.TrimSuffix(expanded, "/**")
				compiled.dirPatterns = append(compiled.dirPatterns, compiledIgnoreDirPattern{
					value:   dirPattern,
					matcher: newCompiledGlobMatcher([]string{dirPattern}),
				})
			}
		}
		patterns = append(patterns, compiled)
	}
	return compiledIgnoreMatcher{patterns: patterns}
}

func (m compiledIgnoreMatcher) empty() bool {
	return len(m.patterns) == 0
}

func (m compiledIgnoreMatcher) matchesListDirName(name string) bool {
	name = filepath.ToSlash(name)
	base := filepath.Base(name)
	for _, pattern := range m.patterns {
		if pattern.matcher.matches(name) || pattern.matcher.matches(base) {
			return true
		}
		for _, dirPattern := range pattern.dirPatterns {
			if dirPattern.matcher.matches(name) || name == dirPattern.value || base == dirPattern.value {
				return true
			}
		}
	}
	return false
}

func (m compiledIgnoreMatcher) shouldSkipPath(root, path string, isDir bool) bool {
	if m.empty() {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return false
	}
	rel = filepath.ToSlash(rel)
	base := filepath.Base(rel)
	for _, pattern := range m.patterns {
		if isDir {
			for _, dirPattern := range pattern.dirPatterns {
				if dirPattern.matcher.matches(rel) || rel == dirPattern.value || base == dirPattern.value {
					return true
				}
			}
		}
		if pattern.matcher.matches(rel) || pattern.matcher.matches(base) {
			return true
		}
	}
	return false
}

func globPatternToRegexp(pattern string) string {
	var b strings.Builder
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '[':
			end := strings.IndexByte(pattern[i+1:], ']')
			if end >= 0 {
				class := pattern[i : i+end+2]
				b.WriteString(class)
				i += end + 1
			} else {
				b.WriteString("\\[")
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	return b.String()
}

func forEachPathSuffixCandidate(path string, visit func(string) bool) bool {
	for start := 0; start < len(path); {
		slash := strings.IndexByte(path[start:], '/')
		if slash < 0 {
			return false
		}
		start += slash + 1
		if start >= len(path) {
			return false
		}
		if visit(path[start:]) {
			return true
		}
	}
	return false
}

func expandBraces(pattern string) []string {
	start := strings.Index(pattern, "{")
	end := strings.Index(pattern, "}")
	if start == -1 || end == -1 || end < start {
		return []string{pattern}
	}
	prefix := pattern[:start]
	suffix := pattern[end+1:]
	parts := strings.Split(pattern[start+1:end], ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		result = append(result, prefix+strings.TrimSpace(part)+suffix)
	}
	return result
}
