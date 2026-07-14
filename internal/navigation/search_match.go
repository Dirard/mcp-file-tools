package navigation

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

type searchPattern struct {
	query      string
	regex      bool
	ignoreCase bool
	context    uint8
}

func patternFromInitial(initial SearchInitial) searchPattern {
	if initial.Mode != dynamicTextSearch && initial.Mode != dynamicSymbolSearch {
		return searchPattern{}
	}
	return searchPattern{
		query:      strings.Clone(initial.Query),
		regex:      initial.Regex,
		ignoreCase: initial.IgnoreCase,
		context:    initial.Context,
	}
}

func (pattern searchPattern) valid(mode dynamicMode) bool {
	if mode != dynamicTextSearch && mode != dynamicSymbolSearch {
		return pattern == (searchPattern{})
	}
	if pattern.query == "" || len(pattern.query) > api.InputStringMaxBytes || !utf8.ValidString(pattern.query) || pattern.context > 20 {
		return false
	}
	if mode == dynamicSymbolSearch && pattern.context != 0 {
		return false
	}
	_, err := compileSearchMatcher(pattern.query, pattern.regex, pattern.ignoreCase)
	return err == nil
}

func compileSearchMatcher(query string, regex, ignoreCase bool) (*regexp.Regexp, error) {
	expression := query
	if !regex {
		expression = regexp.QuoteMeta(query)
	}
	if ignoreCase {
		expression = "(?i:" + expression + ")"
	}
	return regexp.Compile(expression)
}
