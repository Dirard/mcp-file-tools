package scanner

import (
	"strings"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

func TestGlobClosedGrammarAndPathScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pattern    string
		path       string
		ignoreCase bool
		want       bool
	}{
		{pattern: "*.go", path: "internal/scanner/walker.go", want: true},
		{pattern: "*.go", path: "internal/scanner/walker.rs", want: false},
		{pattern: "**/x.go", path: "x.go", want: true},
		{pattern: "**/x.go", path: "a/b/x.go", want: true},
		{pattern: "a/**/x.go", path: "a/x.go", want: true},
		{pattern: "a/**/x.go", path: "a/b/c/x.go", want: true},
		{pattern: "src/{go,rs,py}/[a-c]?.txt", path: "src/rs/b7.txt", want: true},
		{pattern: `literal\*.go`, path: "deep/literal*.go", want: true},
		{pattern: `[\-a]`, path: "-", want: true},
		{pattern: `[\]]`, path: "]", want: true},
		{pattern: `[!a-c]`, path: "z", want: true},
		{pattern: `[!a-c]`, path: "b", want: false},
		{pattern: `[a-z]`, path: "K", ignoreCase: true, want: true},
		{pattern: "k.txt", path: "deep/K.txt", ignoreCase: true, want: true},
	}

	for _, test := range tests {
		test := test
		t.Run(test.pattern+"/"+test.path, func(t *testing.T) {
			glob, code := CompileGlob(test.pattern, test.ignoreCase)
			if code != "" {
				t.Fatalf("CompileGlob returned %q", code)
			}
			if got := glob.Match(test.path); got != test.want {
				t.Fatalf("Match(%q) = %v, want %v", test.path, got, test.want)
			}
		})
	}
}

func TestGlobRejectsMalformedOrUnboundedPatterns(t *testing.T) {
	t.Parallel()

	invalid := []string{
		"", "a**b", "***", "[]", "[!]", "[z-a]", "[-a]", "[a-]", "[a/b]",
		`\a`, `a\`, "{a}", "{a,}", "{a,b}{c,d}", "{a,{b,c}}",
	}
	for _, pattern := range invalid {
		if _, code := CompileGlob(pattern, false); code != api.ErrorInvalidInput {
			t.Fatalf("CompileGlob(%q) code = %q, want %q", pattern, code, api.ErrorInvalidInput)
		}
	}

	tooMany := "{" + strings.Join(make([]string, 33), ",x") + "}"
	if _, code := CompileGlob(tooMany, false); code != api.ErrorInvalidInput {
		t.Fatalf("33 alternatives code = %q, want invalid_input", code)
	}
	largeAlternative := strings.Repeat("x", 2_100)
	parts := make([]string, 32)
	for index := range parts {
		parts[index] = largeAlternative
	}
	if _, code := CompileGlob("{"+strings.Join(parts, ",")+"}", false); code != api.ErrorInvalidInput {
		t.Fatalf("expanded variant byte overflow code = %q, want invalid_input", code)
	}
}
