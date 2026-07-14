package codeparse

import (
	"strings"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/navmodel"
)

func TestTreeSitterParsersCoverEverySupportedLanguageFamily(t *testing.T) {
	tests := []struct {
		name     string
		language api.Language
		source   string
		kind     api.Kind
		symbol   string
	}{
		{"javascript", api.LanguageJavaScript, "import x from 'pkg';\nfunction Run() {}", api.KindFunction, "Run"},
		{"jsx", api.LanguageJSX, "function App() { return <div />; }", api.KindComponent, "App"},
		{"typescript", api.LanguageTypeScript, "interface Store { value: string }", api.KindInterface, "Store"},
		{"tsx", api.LanguageTSX, "const Card = () => <div />;", api.KindComponent, "Card"},
		{"python", api.LanguagePython, "class Thing:\n    def run(self):\n        pass\n", api.KindClass, "Thing"},
		{"java", api.LanguageJava, "package demo; class Thing {}", api.KindClass, "Thing"},
		{"rust", api.LanguageRust, "struct Thing { value: i32 }", api.KindStruct, "Thing"},
		{"c", api.LanguageC, "int run(void) { return 0; }", api.KindFunction, "run"},
		{"cpp", api.LanguageCPP, "namespace demo { class Thing {}; }", api.KindNamespace, "demo"},
		{"csharp", api.LanguageCSharp, "class Thing { public int Value { get; set; } }", api.KindClass, "Thing"},
		{"ruby", api.LanguageRuby, "class Thing\n  def run; end\nend\n", api.KindClass, "Thing"},
		{"kotlin", api.LanguageKotlin, "package demo\n\nclass Thing {\n  fun run(): Boolean = true\n}\n", api.KindClass, "Thing"},
		{"swift", api.LanguageSwift, "struct Thing { func run() {} }", api.KindStruct, "Thing"},
		{"bash", api.LanguageBash, "run() { echo ok; }", api.KindFunction, "run"},
		{"json", api.LanguageJSON, `{"thing":{"enabled":true}}`, api.KindProperty, "thing"},
		{"yaml", api.LanguageYAML, "thing:\n  enabled: true\n", api.KindProperty, "thing"},
		{"svelte", api.LanguageSvelte, "<script>let value = 1;</script><h1>Hello</h1><style>h1{}</style>", api.KindSection, "script"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed := parseTreeSitter(test.language, []byte(test.source))
			if parsed.fatal {
				t.Fatalf("fatal parse: %#v", parsed)
			}
			records := parsed.records
			if len(parsed.errorRanges) != 0 {
				records = filterUnsafeRecords(records, parsed.errorRanges)
			}
			projected, ok := projectRecords(records)
			if !ok {
				t.Fatalf("projection rejected: %#v", records)
			}
			if !hasSymbolRecord(projected, test.kind, test.symbol) {
				t.Fatalf("missing %q/%q in %#v (errors %#v)", test.kind, test.symbol, projected, parsed.errorRanges)
			}
			for _, record := range projected {
				if record.Name == "true" || record.Name == "1" {
					t.Fatalf("source literal escaped projection: %#v", record)
				}
			}
		})
	}
}

func hasSymbolRecord(records []navmodel.Record, kind api.Kind, name string) bool {
	for _, record := range records {
		if record.Type == navmodel.Symbol && record.Kind == kind && record.Name == name {
			return true
		}
	}
	return false
}

func TestTreeSitterNamesAreNotDisplayClipped(t *testing.T) {
	name := "ContainerNameThatIsIntentionallyLongerThanFortyBytesForNavigation"
	parsed := parseTreeSitter(api.LanguageJavaScript, []byte("function "+name+"() {}"))
	projected, ok := projectRecords(parsed.records)
	if parsed.fatal || !ok || !hasProjectedRecord(projected, navmodel.Symbol, api.KindFunction, name, 1) {
		t.Fatalf("long name lost or clipped: %#v, errors=%#v", projected, parsed.errorRanges)
	}
}

func TestTreeSitterRecoverableErrorsRetainOnlySafeRecords(t *testing.T) {
	parsed := parseTreeSitter(api.LanguageJavaScript, []byte("function Safe() {}\nfunction Broken("))
	if parsed.fatal || len(parsed.errorRanges) == 0 {
		t.Fatalf("malformed source did not produce recoverable positions: %#v", parsed)
	}
	projected, ok := projectRecords(filterUnsafeRecords(parsed.records, parsed.errorRanges))
	if !ok || !hasProjectedRecord(projected, navmodel.Symbol, api.KindFunction, "Safe", 1) {
		t.Fatalf("safe function lost: projected=%#v ok=%t raw=%#v errors=%#v", projected, ok, parsed.records, parsed.errorRanges)
	}
	for _, record := range projected {
		if strings.Contains(record.Name, "Broken") {
			t.Fatalf("unsafe function survived: %#v", record)
		}
	}
}
