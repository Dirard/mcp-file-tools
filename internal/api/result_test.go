package api

import (
	"slices"
	"strings"
	"testing"
)

var (
	_ [4]ToolName     = OrderedToolNames()
	_ [15]ErrorCode   = OrderedErrorCodes()
	_ [11]WarningCode = OrderedWarningCodes()
	_ [19]Language    = OrderedLanguages()
	_ [20]Kind        = OrderedKinds()
)

func TestClosedVocabularies(t *testing.T) {
	tools := OrderedToolNames()
	errors := OrderedErrorCodes()
	warnings := OrderedWarningCodes()
	languages := OrderedLanguages()
	kinds := OrderedKinds()

	assertClosedVocabulary(t, "tool names", tools[:], []string{
		"set_cwd", "project", "search", "read",
	}, ToolName.Valid)
	assertClosedVocabulary(t, "error codes", errors[:], []string{
		"invalid_input",
		"cwd_unknown",
		"path_outside_cwd",
		"not_found",
		"binary",
		"unsupported_encoding",
		"unsupported_language",
		"record_exceeds_budget",
		"cursor_expired",
		"cursor_wrong_tool",
		"cursor_wrong_cwd",
		"budget_exceeded",
		"permission_denied",
		"io_error",
		"parser_failed",
	}, ErrorCode.Valid)
	assertClosedVocabulary(t, "warning codes", warnings[:], []string{
		"binary_skipped",
		"parser_partial",
		"parser_skipped",
		"path_encoding_unsupported",
		"special_file_skipped",
		"unreadable_skipped",
		"unsupported_encoding_skipped",
		"source_changed_skipped",
		"symlink_skipped",
		"mount_skipped",
		"unaddressable_path_skipped",
	}, WarningCode.Valid)
	assertClosedVocabulary(t, "languages", languages[:], []string{
		"markdown",
		"go",
		"javascript",
		"jsx",
		"typescript",
		"tsx",
		"python",
		"java",
		"rust",
		"c",
		"cpp",
		"csharp",
		"ruby",
		"kotlin",
		"swift",
		"bash",
		"json",
		"yaml",
		"svelte",
	}, Language.Valid)
	assertClosedVocabulary(t, "kinds", kinds[:], []string{
		"package",
		"module",
		"namespace",
		"class",
		"interface",
		"struct",
		"enum",
		"trait",
		"type",
		"constant",
		"variable",
		"field",
		"property",
		"function",
		"method",
		"constructor",
		"object",
		"component",
		"section",
		"other",
	}, Kind.Valid)
}

func TestOrderedToolNamesIsolation(t *testing.T) {
	want := [4]ToolName{ToolSetCWD, ToolProject, ToolSearch, ToolRead}
	first := OrderedToolNames()
	second := OrderedToolNames()

	for i := range first {
		first[i] = ToolName("mutated")
	}

	if second != want {
		t.Fatalf("second call changed after local mutation: got %v, want %v", second, want)
	}
	if third := OrderedToolNames(); third != want {
		t.Fatalf("fresh call changed after local mutation: got %v, want %v", third, want)
	}
}

func TestNewCallOwnsArguments(t *testing.T) {
	raw := []byte(`{"path":"main.go"}`)
	want := slices.Clone(raw)
	call := NewCall(ToolRead, raw)

	raw[0] = '['
	if call.Name() != ToolRead {
		t.Fatalf("Name() = %q, want %q", call.Name(), ToolRead)
	}
	if got := call.Arguments(); !slices.Equal(got, want) {
		t.Fatalf("Arguments() after input mutation = %q, want %q", got, want)
	}

	first := call.Arguments()
	first[0] = '['
	if got := call.Arguments(); !slices.Equal(got, want) {
		t.Fatalf("Arguments() exposed internal storage: got %q, want %q", got, want)
	}
}

func TestNavigationResult(t *testing.T) {
	for _, test := range []struct {
		name    string
		text    string
		isError bool
	}{
		{name: "success", text: "FILE\tmain.go\n", isError: false},
		{name: "error", text: "ERROR\tinvalid_input\n", isError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := Navigation(test.text, test.isError)
			if result.Kind() != ResultText {
				t.Fatalf("Kind() = %d, want ResultText", result.Kind())
			}
			if got, ok := result.Text(); !ok || got != test.text {
				t.Fatalf("Text() = %q, %t; want %q, true", got, ok, test.text)
			}
			if got, ok := result.CWDID(); ok || got != 0 {
				t.Fatalf("CWDID() = %d, %t; want 0, false", got, ok)
			}
			if result.IsError() != test.isError {
				t.Fatalf("IsError() = %t, want %t", result.IsError(), test.isError)
			}
			if err := result.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestSetCWDResult(t *testing.T) {
	result := SetCWD(42)
	if result.Kind() != ResultCWD {
		t.Fatalf("Kind() = %d, want ResultCWD", result.Kind())
	}
	if got, ok := result.Text(); ok || got != "" {
		t.Fatalf("Text() = %q, %t; want empty, false", got, ok)
	}
	if got, ok := result.CWDID(); !ok || got != 42 {
		t.Fatalf("CWDID() = %d, %t; want 42, true", got, ok)
	}
	if result.IsError() {
		t.Fatal("IsError() = true, want false")
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestResultValidateRejectsInvalidStates(t *testing.T) {
	const maxSafeInteger = uint64(9007199254740991)
	invalidUTF8 := string([]byte{0xff, '\n'})

	for _, test := range []struct {
		name   string
		result Result
	}{
		{name: "empty text", result: Result{kind: ResultText}},
		{name: "non UTF-8 text", result: Result{kind: ResultText, text: invalidUTF8}},
		{name: "missing final LF", result: Result{kind: ResultText, text: "FILE\tmain.go"}},
		{name: "double final blank line", result: Result{kind: ResultText, text: "FILE\tmain.go\n\n"}},
		{name: "cwd zero", result: Result{kind: ResultCWD}},
		{name: "cwd above JS-safe maximum", result: Result{kind: ResultCWD, cwdID: maxSafeInteger + 1}},
		{name: "text plus cwd", result: Result{kind: ResultText, text: "FILE\tmain.go\n", cwdID: 1}},
		{name: "cwd plus text", result: Result{kind: ResultCWD, text: "FILE\tmain.go\n", cwdID: 1}},
		{name: "cwd error", result: Result{kind: ResultCWD, cwdID: 1, isError: true}},
		{name: "unknown kind", result: Result{kind: ResultKind(255)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.result.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want rejection")
			}
		})
	}
}

func TestResultKindValuesAreStable(t *testing.T) {
	if ResultText != 1 {
		t.Fatalf("ResultText = %d, want 1", ResultText)
	}
	if ResultCWD != 2 {
		t.Fatalf("ResultCWD = %d, want 2", ResultCWD)
	}
}

func assertClosedVocabulary[T ~string](
	t *testing.T,
	name string,
	got []T,
	want []string,
	valid func(T) bool,
) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length = %d, want %d", name, len(got), len(want))
	}

	seen := make(map[string]struct{}, len(got))
	for i, value := range got {
		raw := string(value)
		if raw != want[i] {
			t.Errorf("%s[%d] = %q, want %q", name, i, raw, want[i])
		}
		if raw == "" {
			t.Errorf("%s[%d] is empty", name, i)
		}
		if strings.TrimSpace(raw) != raw {
			t.Errorf("%s[%d] contains surrounding whitespace: %q", name, i, raw)
		}
		if strings.ToLower(raw) != raw {
			t.Errorf("%s[%d] is not lowercase-stable: %q", name, i, raw)
		}
		if _, duplicate := seen[raw]; duplicate {
			t.Errorf("%s contains duplicate %q", name, raw)
		}
		seen[raw] = struct{}{}
		if !valid(value) {
			t.Errorf("%s value %q is not valid", name, raw)
		}
	}

	for _, invalid := range []T{"", " ", "unknown", T(strings.ToUpper(want[0]))} {
		if valid(invalid) {
			t.Errorf("%s accepts invalid value %q", name, invalid)
		}
	}
}
