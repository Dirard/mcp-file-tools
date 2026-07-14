package codeparse

import (
	"reflect"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/navmodel"
)

func TestParseMarkdownExtractsFrontmatterNestedSectionsAndIgnoresFences(t *testing.T) {
	source := []byte("---\ntitle: Demo\n---\n# Root\ntext\n```go\n# ignored\n```\n## Child ##\ntext\n# Next")
	parsed := parseMarkdown(source)
	if parsed.fatal || len(parsed.errorRanges) != 0 {
		t.Fatalf("Markdown parse was not clean: %#v", parsed)
	}
	got, ok := projectRecords(parsed.records)
	if !ok {
		t.Fatal("Markdown projection rejected")
	}
	want := []navmodel.Record{
		{Type: navmodel.Heading, Range: navmodel.Range{Start: 1, End: 3}, Kind: api.KindSection, Name: "frontmatter"},
		{Type: navmodel.Heading, Range: navmodel.Range{Start: 4, End: 10}, Depth: 1, Kind: api.KindSection, Name: "Root"},
		{Type: navmodel.Heading, Range: navmodel.Range{Start: 9, End: 10}, Depth: 2, Kind: api.KindSection, Name: "Child"},
		{Type: navmodel.Heading, Range: navmodel.Range{Start: 11, End: 11}, Depth: 1, Kind: api.KindSection, Name: "Next"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Markdown records = %#v, want %#v", got, want)
	}
}

func TestParseMarkdownHasNoRecoverableSyntaxState(t *testing.T) {
	for _, source := range [][]byte{
		nil,
		[]byte("---\nunclosed: true\n# still frontmatter"),
		[]byte("~~~\n# inside unclosed fence"),
		[]byte("####### not an ATX heading"),
	} {
		parsed := parseMarkdown(source)
		if parsed.fatal || len(parsed.errorRanges) != 0 {
			t.Fatalf("Markdown syntax created parser error state for %q: %#v", source, parsed)
		}
		if _, ok := projectRecords(parsed.records); !ok {
			t.Fatalf("Markdown projection failed for %q", source)
		}
	}
}
