package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Dirard/mcp-file-tools/internal/config"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestReadFileLongLineReturnsFullLine(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "long.txt")
	longLine := strings.Repeat("x", 12*1024)
	if err := os.WriteFile(file, []byte(longLine), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("read_file returned error: %#v", result.Content)
	}
	if len(result.Content) != 0 {
		t.Fatalf("read_file should suppress MCP text duplication, got %d content items", len(result.Content))
	}
	if !strings.Contains(output.Text, "1|") {
		t.Fatalf("read_file output did not include line number:\n%s", output.Text)
	}
	if !strings.Contains(output.Text, longLine) {
		t.Fatalf("read_file output did not include the complete long line")
	}
}

func TestReadFileLineRangeCanJumpToDeepLines(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "deep.txt")
	var b strings.Builder
	for i := 1; i <= 2300; i++ {
		b.WriteString("line ")
		b.WriteString(strconv.Itoa(i))
		b.WriteByte('\n')
	}
	if err := os.WriteFile(file, []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	startLine := 2057
	endLine := 2057
	result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{
		TargetFile: file,
		StartLine:  &startLine,
		EndLine:    &endLine,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("read_file deep range returned error: %#v output=%#v", result, output)
	}
	if !strings.Contains(output.Text, "2057|line 2057") {
		t.Fatalf("read_file deep range did not include expected line:\n%s", output.Text)
	}
}

func TestOutlineFileMarkdownReturnsHierarchyAndFingerprint(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "concept.md")
	content := "---\ntitle: Demo\n---\n# Intro\ntext\n## Details\n```go\n# not heading\n```\n# Next\n"
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("outline_file returned error: %#v", output)
	}
	if output.Fingerprint == nil || output.Fingerprint.LineCount != 11 || output.Fingerprint.SizeBytes == 0 || output.Fingerprint.SHA256 == "" {
		t.Fatalf("unexpected fingerprint: %#v", output.Fingerprint)
	}
	if output.Language != "markdown" || output.ParserStatus != "ok" || output.ParserScope != "markdown_atx_headings" {
		t.Fatalf("unexpected parser metadata: %#v", output)
	}
	if len(output.Sections) != 3 {
		t.Fatalf("expected frontmatter and two top-level sections, got %#v", output.Sections)
	}
	if output.Sections[1].Name != "Intro" || len(output.Sections[1].Children) != 1 || output.Sections[1].Children[0].Name != "Details" {
		t.Fatalf("unexpected markdown hierarchy: %#v", output.Sections)
	}
	for _, item := range flattenOutlineItems(output.Sections) {
		if item.Confidence != "exact" || item.RangeIsEstimated || item.RangeFingerprint == nil {
			t.Fatalf("markdown outline item should carry exact trust metadata: %#v", item)
		}
	}
}

func TestOutlineFileMarkdownFenceLengthPreventsFakeHeadings(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "concept.md")
	content := "# Real\n````\n```go\n# not heading\n```\n````\n# Next\n"
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("outline_file returned error: %#v", output)
	}
	if len(output.Sections) != 2 || output.Sections[0].Name != "Real" || output.Sections[1].Name != "Next" {
		t.Fatalf("fenced heading leaked into outline: %#v", output.Sections)
	}
}

func TestOutlineFileMarkdownFenceCloseRejectsTrailingText(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "concept.md")
	content := "# Real\n````\n````not-a-close\n# not heading\n````\n# Next\n"
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("outline_file returned error: %#v", output)
	}
	if len(output.Sections) != 2 || output.Sections[0].Name != "Real" || output.Sections[1].Name != "Next" {
		t.Fatalf("fenced heading leaked after invalid closing fence: %#v", output.Sections)
	}
}

func TestOutlineFileMarkdownFrontmatterRangeIsExact(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "concept.md")
	content := "---\ntitle: Demo\n---\nintro prose before heading\n# Real\n"
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(output.Sections) == 0 {
		t.Fatalf("outline_file returned error: result=%#v output=%#v", result, output)
	}
	frontmatter := output.Sections[0]
	if frontmatter.Kind != "frontmatter" || frontmatter.Range.StartLine != 1 || frontmatter.Range.EndLine != 3 {
		t.Fatalf("frontmatter range should stop at closing delimiter: %#v", frontmatter)
	}
}

func TestOutlineFileTruncationReturnsNextRecommendedLineWindow(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "concept.md")
	content := "# One\ntext\n# Two\ntext\n# Three\ntext\n"
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	maxItems := 1

	h := NewHandler()
	result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{
		TargetFile: file,
		MaxItems:   &maxItems,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !output.Truncated || output.NextRecommendedCall == nil {
		t.Fatalf("expected truncated outline with next call: result=%#v output=%#v", result, output)
	}
	nextInput := output.NextRecommendedCall.RecommendedNextInput
	lineWindow, ok := nextInput["line_window"].(map[string]any)
	if !ok || lineWindow["start_line"] == nil || lineWindow["end_line"] == nil {
		t.Fatalf("next recommended call should include bounded line_window: %#v", output.NextRecommendedCall)
	}
	if _, ok := nextInput["name_contains"]; ok {
		t.Fatalf("next recommended call should not narrow continuation with name_contains: %#v", output.NextRecommendedCall)
	}
	if _, ok := nextInput["kinds"]; ok {
		t.Fatalf("next recommended call should not narrow continuation with kinds: %#v", output.NextRecommendedCall)
	}
	got, ok := lineWindow["start_line"].(int)
	if !ok || got != output.OutlineStats.NextOmittedLine || got <= output.Sections[0].Range.StartLine {
		t.Fatalf("next recommended call should move to next omitted item, line_window=%#v stats=%#v sections=%#v", lineWindow, output.OutlineStats, output.Sections)
	}
	nextWindow := SourceLineRange{StartLine: got, EndLine: output.Fingerprint.LineCount}
	nextResult, nextOutput, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{
		TargetFile: file,
		MaxItems:   &maxItems,
		LineWindow: &nextWindow,
	})
	if err != nil {
		t.Fatal(err)
	}
	if nextResult.IsError || len(nextOutput.Sections) == 0 || nextOutput.Sections[0].Name != "Two" {
		t.Fatalf("next recommended line_window should make forward progress: result=%#v output=%#v", nextResult, nextOutput)
	}
}

func TestOutlineFileTruncationPreservesFullOutputProfile(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "config.json")
	content := "{\n  \"alpha\": true,\n  \"beta\": true,\n  \"gamma\": true\n}\n"
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	maxItems := 1

	h := NewHandler()
	result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{
		TargetFile:    file,
		OutputProfile: outlineProfileFull,
		MaxItems:      &maxItems,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !output.Truncated || output.NextRecommendedCall == nil {
		t.Fatalf("full profile truncated outline should expose continuation: result=%#v output=%#v", result, output)
	}
	if output.NextRecommendedCall.RecommendedNextInput["output_profile"] != outlineProfileFull {
		t.Fatalf("full profile continuation must preserve full profile: %#v", output.NextRecommendedCall)
	}
}

func TestOutlineFileMarkdownMaxItemsBoundsStoredHeadings(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "concept.md")
	var content strings.Builder
	for i := 1; i <= 120; i++ {
		content.WriteString("# Heading ")
		content.WriteString(strconv.Itoa(i))
		content.WriteString("\ntext\n")
	}
	if err := os.WriteFile(file, []byte(content.String()), 0644); err != nil {
		t.Fatal(err)
	}
	maxItems := 5

	h := NewHandler()
	result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{
		TargetFile: file,
		MaxItems:   &maxItems,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !output.Truncated || output.NextRecommendedCall == nil {
		t.Fatalf("expected bounded truncated outline: result=%#v output=%#v", result, output)
	}
	if got := len(flattenOutlineItems(output.Sections)); got != maxItems {
		t.Fatalf("outline should store/return only max_items headings, got %d sections=%#v", got, output.Sections)
	}
	if !output.OutlineStats.ItemsOmittedKnown || output.OutlineStats.ItemsOmitted != 115 || output.OutlineStats.NextOmittedLine != 11 {
		t.Fatalf("truncated markdown stats should point at first omitted heading: %#v", output.OutlineStats)
	}
	if output.Sections[len(output.Sections)-1].Range.EndLine != 10 {
		t.Fatalf("last returned heading should have exact closed range despite bounded storage: %#v", output.Sections[len(output.Sections)-1])
	}
}

func TestOutlineFileMarkdownContinuationWithMaxDepthStaysBounded(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "concept.md")
	var content strings.Builder
	for i := 1; i <= 80; i++ {
		content.WriteString("### Heading ")
		content.WriteString(strconv.Itoa(i))
		content.WriteString("\ntext\n")
	}
	if err := os.WriteFile(file, []byte(content.String()), 0644); err != nil {
		t.Fatal(err)
	}
	maxItems := 5
	maxDepth := 3
	window := SourceLineRange{StartLine: 41, EndLine: 160}

	h := NewHandler()
	result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{
		TargetFile: file,
		MaxItems:   &maxItems,
		MaxDepth:   &maxDepth,
		LineWindow: &window,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !output.Truncated || output.NextRecommendedCall == nil {
		t.Fatalf("expected bounded continuation outline: result=%#v output=%#v", result, output)
	}
	if got := len(flattenOutlineItems(output.Sections)); got != maxItems {
		t.Fatalf("line_window + max_depth should still return only max_items headings, got %d sections=%#v", got, output.Sections)
	}
	if output.Sections[0].Range.StartLine != 41 || output.OutlineStats.NextOmittedLine != 51 {
		t.Fatalf("bounded continuation should start at window and point to first omitted heading: stats=%#v sections=%#v", output.OutlineStats, output.Sections)
	}
}

func TestOutlineFileMarkdownLineWindowIncludesEnclosingSection(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "concept.md")
	var content strings.Builder
	content.WriteString("# Parent\n")
	for i := 2; i <= 80; i++ {
		content.WriteString("body\n")
	}
	content.WriteString("# Next\n")
	if err := os.WriteFile(file, []byte(content.String()), 0644); err != nil {
		t.Fatal(err)
	}
	window := SourceLineRange{StartLine: 50, EndLine: 50}

	h := NewHandler()
	result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{
		TargetFile: file,
		LineWindow: &window,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(output.Sections) != 1 {
		t.Fatalf("line_window should include enclosing parent: result=%#v output=%#v", result, output)
	}
	if output.Sections[0].Name != "Parent" || output.Sections[0].Range.StartLine != 1 || output.Sections[0].Range.EndLine != 80 {
		t.Fatalf("unexpected enclosing section: %#v", output.Sections[0])
	}
}

func TestOutlineFileMarkdownWarningsAreBounded(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "concept.md")
	var content strings.Builder
	for i := 1; i <= 80; i++ {
		content.WriteString("Heading ")
		content.WriteString(strconv.Itoa(i))
		content.WriteString("\n---\n\n")
	}
	if err := os.WriteFile(file, []byte(content.String()), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("outline_file returned error: %#v", output)
	}
	if len(output.Warnings) != markdownWarningLimit+1 {
		t.Fatalf("warnings should be capped with one summary warning, got %d warnings=%#v", len(output.Warnings), output.Warnings)
	}
	last := output.Warnings[len(output.Warnings)-1]
	if last.Code != "markdown_warnings_truncated" || !strings.Contains(last.Message, "30 additional") {
		t.Fatalf("expected markdown warning truncation summary, got %#v", last)
	}
}

func TestOutlineFileExplicitUnsupportedLanguageReturnsStructuredError(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(file, []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{
		TargetFile: file,
		Language:   "brainfuck",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || output.ParserStatus != "unsupported_language" || output.ErrorCode != "unsupported_language" || output.Fingerprint == nil {
		t.Fatalf("explicit unsupported language should return structured unsupported_language error with fingerprint: result=%#v output=%#v", result, output)
	}
}

func TestOutlineFileTreeSitterLanguagesReturnSelectorMetadata(t *testing.T) {
	tempDir := t.TempDir()
	tests := []struct {
		name         string
		fileName     string
		source       string
		language     string
		parserStatus string
		symbol       string
		kind         string
		writeSafe    bool
	}{
		{name: "javascript", fileName: "sample.js", source: "function loadConfig() {\n  return true;\n}\n", language: "javascript", parserStatus: "ok", symbol: "loadConfig", kind: "function", writeSafe: true},
		{name: "typescript", fileName: "sample.ts", source: "class Loader {\n  loadConfig(): boolean { return true }\n}\n", language: "typescript", parserStatus: "ok", symbol: "Loader", kind: "class", writeSafe: true},
		{name: "tsx", fileName: "sample.tsx", source: "function Widget() {\n  return <section />;\n}\n", language: "tsx", parserStatus: "ok", symbol: "Widget", kind: "component", writeSafe: true},
		{name: "jsx", fileName: "sample.jsx", source: "function Widget() {\n  return <section />;\n}\n", language: "javascript", parserStatus: "ok", symbol: "Widget", kind: "component", writeSafe: true},
		{name: "python", fileName: "sample.py", source: "class Loader:\n    def load_config(self):\n        return True\n", language: "python", parserStatus: "ok", symbol: "Loader", kind: "class", writeSafe: true},
		{name: "java", fileName: "sample.java", source: "package com.example;\n\nimport java.util.List;\n\npublic class Loader {\n  public boolean loadConfig() { return true; }\n}\n", language: "java", parserStatus: "ok", symbol: "Loader", kind: "class", writeSafe: true},
		{name: "json", fileName: "sample.json", source: "{\n  \"service\": {\"enabled\": true}\n}\n", language: "json", parserStatus: "ok", symbol: "document.service", kind: "property", writeSafe: false},
		{name: "yaml", fileName: "sample.yaml", source: "service:\n  enabled: true\n", language: "yaml", parserStatus: "ok", symbol: "document.service", kind: "key", writeSafe: false},
		{name: "svelte", fileName: "sample.svelte", source: "<script>\n  export let loadConfig = true;\n</script>\n<section>{loadConfig}</section>\n", language: "svelte", parserStatus: "partial", symbol: "script", kind: "script", writeSafe: false},
	}

	h := NewHandler()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := filepath.Join(tempDir, tt.fileName)
			if err := os.WriteFile(file, []byte(tt.source), 0644); err != nil {
				t.Fatal(err)
			}
			result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError || output.Language != tt.language || output.ParserStatus != tt.parserStatus {
				t.Fatalf("outline_file should parse %s through tree-sitter: result=%#v output=%#v", tt.name, result, output)
			}
			item := findOutlineItemByName(output.Symbols, tt.symbol)
			if item == nil {
				item = findOutlineItemByDisplayNameSuffix(output.Symbols, tt.symbol)
			}
			if item == nil || item.Selector == nil || item.SymbolRef == "" || item.ByteRange == nil || item.RangeFingerprint == nil {
				t.Fatalf("%s symbol %q should expose selector metadata, symbols=%#v", tt.name, tt.symbol, output.Symbols)
			}
			if item.Selector.Language != tt.language || item.Selector.SymbolRef != item.SymbolRef || len(item.Selector.SymbolPath) == 0 {
				t.Fatalf("%s selector should be stable and language-local: %#v item=%#v", tt.name, item.Selector, item)
			}
			if item.ByteRange.StartByte >= item.ByteRange.EndByteExclusive || item.Range.StartLine < 1 || item.Range.EndLine < item.Range.StartLine {
				t.Fatalf("%s selector ranges should be valid: %#v", tt.name, item)
			}
			if item.Kind != tt.kind {
				t.Fatalf("%s symbol kind = %q, want %q: %#v", tt.name, item.Kind, tt.kind, item)
			}
			itemWriteSafe := boolValue(item.WriteSafe)
			if item.WriteSafe == nil && item.Selector != nil {
				itemWriteSafe = item.Selector.WriteSafe
			}
			if itemWriteSafe != tt.writeSafe {
				t.Fatalf("%s write_safe = %#v, want %v: %#v", tt.name, item.WriteSafe, tt.writeSafe, item)
			}
		})
	}
}

func findOutlineItemByName(items []OutlineItem, name string) *OutlineItem {
	for i := range items {
		if items[i].Name == name {
			return &items[i]
		}
		if found := findOutlineItemByName(items[i].Children, name); found != nil {
			return found
		}
	}
	return nil
}

func findOutlineItemByKind(items []OutlineItem, kind string) *OutlineItem {
	for i := range items {
		if items[i].Kind == kind {
			return &items[i]
		}
		if found := findOutlineItemByKind(items[i].Children, kind); found != nil {
			return found
		}
	}
	return nil
}

func findOutlineItemByDisplayNameSuffix(items []OutlineItem, suffix string) *OutlineItem {
	for i := range items {
		if strings.HasSuffix(items[i].Name, "."+suffix) || strings.HasSuffix(items[i].Name, suffix) {
			return &items[i]
		}
		if found := findOutlineItemByDisplayNameSuffix(items[i].Children, suffix); found != nil {
			return found
		}
	}
	return nil
}

func findOutlineItemAcrossCategories(output OutlineFileOutput, name string) *OutlineItem {
	if found := findOutlineItemByName(output.Imports, name); found != nil {
		return found
	}
	if found := findOutlineItemByName(output.Symbols, name); found != nil {
		return found
	}
	return findOutlineItemByName(output.Sections, name)
}

func findOutlineItemByKindAndName(items []OutlineItem, kind, name string) *OutlineItem {
	for i := range items {
		if items[i].Kind == kind && items[i].Name == name {
			return &items[i]
		}
		if found := findOutlineItemByKindAndName(items[i].Children, kind, name); found != nil {
			return found
		}
	}
	return nil
}

func countTopLevelOutlineItemsByKind(items []OutlineItem, kind string) int {
	count := 0
	for i := range items {
		if items[i].Kind == kind {
			count++
		}
	}
	return count
}

func TestOutlineFileTreeSitterImportsAndEnclosingLine(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.ts")
	source := "import util from \"pkg\";\nimport other from \"other\";\nclass Loader {\n  loadConfig() {\n    return util;\n  }\n}\n"
	if err := os.WriteFile(file, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	line := 4
	h := NewHandler()
	result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{
		TargetFile:     file,
		IncludeImports: true,
		EnclosingLine:  &line,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.ParserStatus != "ok" {
		t.Fatalf("outline_file should parse TS imports and enclosing symbols: result=%#v output=%#v", result, output)
	}
	if len(output.Imports) != 1 || output.Imports[0].Kind != "import_block" || output.Imports[0].Selector == nil || len(output.Imports[0].Children) != 2 || output.Imports[0].Range.StartLine != 1 || output.Imports[0].Range.EndLine != 2 {
		t.Fatalf("imports-only tree-sitter outline should expose import_block selector metadata and child imports: %#v", output)
	}
	if len(output.Symbols) != 0 {
		t.Fatalf("include_imports-only output should not return symbols: %#v", output.Symbols)
	}
	if len(output.EnclosingItems) < 2 || output.EnclosingItems[0].Name != "loadConfig" || output.EnclosingItems[1].Name != "Loader" {
		t.Fatalf("enclosing_line should return innermost TS method then class: %#v", output.EnclosingItems)
	}
}

func TestOutlineFileJSLikeImportBlocksAcrossAliases(t *testing.T) {
	tempDir := t.TempDir()
	tests := []struct {
		name     string
		fileName string
		source   string
	}{
		{name: "js", fileName: "sample.js", source: "import a from \"a\";\nexport { b } from \"b\";\nfunction Load() { return a; }\n"},
		{name: "ts", fileName: "sample.ts", source: "import a from \"a\";\nexport { b } from \"b\";\nfunction load(): string { return a; }\n"},
		{name: "tsx", fileName: "sample.tsx", source: "import a from \"a\";\nexport { b } from \"b\";\nfunction Widget() { return <section />; }\n"},
		{name: "jsx", fileName: "sample.jsx", source: "import a from \"a\";\nexport { b } from \"b\";\nfunction Widget() { return <section />; }\n"},
	}
	h := NewHandler()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := filepath.Join(tempDir, tt.fileName)
			if err := os.WriteFile(file, []byte(tt.source), 0644); err != nil {
				t.Fatal(err)
			}
			result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{
				TargetFile:     file,
				IncludeImports: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError || len(output.Imports) != 2 {
				t.Fatalf("%s should return an import block plus source-bearing re-export: result=%#v output=%#v", tt.name, result, output)
			}
			block := output.Imports[0]
			if block.Kind != "import_block" || block.Selector == nil || block.SymbolRef == "" || block.Range.StartLine != 1 || block.Range.EndLine != 1 || len(block.Children) != 1 || block.Children[0].Kind != "import" {
				t.Fatalf("%s import block should group only real import statements: %#v", tt.name, block)
			}
			reExport := output.Imports[1]
			if reExport.Kind != "re_export" || reExport.Selector == nil || reExport.SymbolRef == "" || reExport.Range.StartLine != 2 || reExport.Range.EndLine != 2 {
				t.Fatalf("%s source-bearing export should remain visible as separate re_export: %#v", tt.name, reExport)
			}
		})
	}
}

func TestOutlineFileJSLikeExportDeclarationContainingFromTextIsNotReExport(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "from-text.tsx")
	source := "export { helper } from \"./helper\";\n\nexport const Card = () => <span>{\" from \"}</span>;\n"
	if err := os.WriteFile(file, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{
		TargetFile:     file,
		IncludeImports: true,
		IncludeSymbols: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("TSX export declaration containing from text should parse: result=%#v output=%#v", result, output)
	}
	if reExport := findOutlineItemByKind(output.Imports, "re_export"); reExport == nil || reExport.Range.StartLine != 1 || reExport.Range.EndLine != 1 {
		t.Fatalf("source-bearing export should remain visible as re_export: %#v", output.Imports)
	}
	card := findOutlineItemByKindAndName(output.Symbols, "component", "Card")
	if card == nil || card.Selector == nil || card.Range.StartLine != 3 || card.Range.EndLine != 3 || !boolValue(card.WriteSafe) || !card.Selector.WriteSafe {
		t.Fatalf("exported component containing literal from text should remain a write-safe symbol: %#v", output)
	}
	for _, item := range output.Imports {
		if item.Kind == "re_export" && item.Range.StartLine == 3 {
			t.Fatalf("exported component containing literal from text must not be classified as re_export: %#v", output.Imports)
		}
	}
}

func TestOutlineFileJavaReturnsBaselineSymbolsAndResolves(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "Loader.java")
	source := `package com.example.config;

import java.util.List;

public @interface Managed {}

interface Loadable {}

enum Mode { FAST }

record Pair(String key, String value) {}

public class Loader implements Loadable {
  private final List<String> names;

  public Loader(List<String> names) {
    this.names = names;
  }

  public boolean loadConfig() {
    return !names.isEmpty();
  }
}
`
	if err := os.WriteFile(file, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.Language != "java" || output.ParserStatus != "ok" {
		t.Fatalf("java should parse through tree-sitter: result=%#v output=%#v", result, output)
	}
	for _, kind := range []string{"package", "import"} {
		if findOutlineItemByKind(output.Imports, kind) == nil {
			t.Fatalf("java should expose %s item in imports category: %#v", kind, output.Imports)
		}
	}
	for _, tt := range []struct {
		kind string
		name string
	}{
		{kind: "annotation", name: "Managed"},
		{kind: "interface", name: "Loadable"},
		{kind: "enum", name: "Mode"},
		{kind: "record", name: "Pair"},
		{kind: "class", name: "Loader"},
		{kind: "field", name: "names"},
		{kind: "constructor", name: "Loader"},
		{kind: "method", name: "loadConfig"},
	} {
		if findOutlineItemByKindAndName(output.Symbols, tt.kind, tt.name) == nil {
			t.Fatalf("java should expose %s %q, symbols=%#v", tt.kind, tt.name, output.Symbols)
		}
	}
	method := findOutlineItemByKindAndName(output.Symbols, "method", "loadConfig")
	if method == nil || method.Selector == nil || method.SymbolRef == "" || method.ByteRange == nil || method.RangeFingerprint == nil {
		t.Fatalf("java method should expose selector metadata: %#v", method)
	}
	result, resolved, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        file,
		SourceFingerprint: *output.Fingerprint,
		Selector: SymbolSelectorQuery{
			Name: "loadConfig",
			Kind: "method",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || resolved.ResolutionStatus != resolveStatusResolved || resolved.Ambiguous || len(resolved.Matches) != 1 || resolved.Matches[0].Name != "loadConfig" || resolved.Matches[0].Kind != "method" {
		t.Fatalf("java method should resolve by selector: result=%#v output=%#v", result, resolved)
	}
}

func TestResolveSymbolRangeRefusesJavaMultiDeclaratorFieldWrite(t *testing.T) {
	tempDir := t.TempDir()
	sourceFile := filepath.Join(tempDir, "Fields.java")
	targetFile := filepath.Join(tempDir, "field.txt")
	source := "class Fields {\n  private int a, b;\n}\n"
	if err := os.WriteFile(sourceFile, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	_, outline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: sourceFile})
	if err != nil {
		t.Fatal(err)
	}
	field := findOutlineItemByKindAndName(outline.Symbols, "field", "a")
	if field == nil || field.WriteSafe == nil || *field.WriteSafe || field.RefusalReason != "symbol_range_not_write_safe" {
		t.Fatalf("multi-declarator Java field should be exact for navigation but not write-safe: %#v", field)
	}
	result, resolved, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        sourceFile,
		SourceFingerprint: *outline.Fingerprint,
		Selector:          SymbolSelectorQuery{SymbolRef: field.SymbolRef},
		TargetIntent: &WriteTargetIntent{
			Operation:          operationCopy,
			TargetFile:         targetFile,
			TargetPrecondition: TargetPrecondition{MustNotExist: true},
			Placement:          TargetPlacement{Mode: placementCreateNew},
			DryRun:             true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || resolved.WriteRefusalCode != "symbol_range_not_write_safe" || resolved.RecommendedWriteCall != nil || len(resolved.Matches) != 1 || resolved.Matches[0].WriteSafe {
		t.Fatalf("multi-declarator Java field should resolve read-only and refuse write recommendation: result=%#v output=%#v", result, resolved)
	}
}

func TestOutlineFileJSLikeExportDeclarationsRemainSymbols(t *testing.T) {
	tempDir := t.TempDir()
	tests := []struct {
		name     string
		fileName string
		source   string
		symbols  []string
	}{
		{name: "export function", fileName: "function.ts", source: "export function loadConfig() { return true; }\n", symbols: []string{"loadConfig"}},
		{name: "export class", fileName: "class.ts", source: "export class Loader {}\n", symbols: []string{"Loader"}},
		{name: "export const", fileName: "const.ts", source: "export const loadConfig = () => true;\n", symbols: []string{"loadConfig"}},
	}
	h := NewHandler()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := filepath.Join(tempDir, tt.fileName)
			if err := os.WriteFile(file, []byte(tt.source), 0644); err != nil {
				t.Fatal(err)
			}
			result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{
				TargetFile:     file,
				IncludeSymbols: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError || len(output.Imports) != 0 {
				t.Fatalf("%s symbols-only outline should not require import output: result=%#v output=%#v", tt.name, result, output)
			}
			for _, symbol := range tt.symbols {
				if findOutlineItemByName(output.Symbols, symbol) == nil {
					t.Fatalf("%s exported declaration %q should remain in symbols: %#v", tt.name, symbol, output.Symbols)
				}
			}
			result, resolved, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
				SourceFile:        file,
				SourceFingerprint: *output.Fingerprint,
				Selector: SymbolSelectorQuery{
					Name: tt.symbols[0],
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError || resolved.ResolutionStatus != resolveStatusResolved || resolved.Ambiguous || len(resolved.Matches) != 1 || resolved.Matches[0].Name != tt.symbols[0] {
				t.Fatalf("%s exported declaration should resolve by name to one candidate: result=%#v output=%#v", tt.name, result, resolved)
			}
		})
	}
}

func TestOutlineFileNestedChildrenRespectMaxItems(t *testing.T) {
	tempDir := t.TempDir()
	jsFile := filepath.Join(tempDir, "imports.ts")
	goFile := filepath.Join(tempDir, "imports.go")
	if err := os.WriteFile(jsFile, []byte("import a from \"a\";\nimport b from \"b\";\nimport c from \"c\";\nfunction load() { return a; }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goFile, []byte("package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\nfunc main() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	for _, tt := range []struct {
		name       string
		file       string
		wantKind   string
		wantCount  int
		wantChildN int
		maxItems   int
	}{
		{name: "js parent only", file: jsFile, wantKind: "import_block", wantCount: 1, wantChildN: 0, maxItems: 1},
		{name: "js one child", file: jsFile, wantKind: "import_block", wantCount: 2, wantChildN: 1, maxItems: 2},
		{name: "go parent only", file: goFile, wantKind: "import_block", wantCount: 1, wantChildN: 0, maxItems: 1},
		{name: "go one child", file: goFile, wantKind: "import_block", wantCount: 2, wantChildN: 1, maxItems: 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{
				TargetFile:     tt.file,
				IncludeImports: true,
				MaxItems:       &tt.maxItems,
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError || len(output.Imports) != 1 {
				t.Fatalf("outline should return bounded import block: result=%#v output=%#v", result, output)
			}
			block := output.Imports[0]
			if block.Kind != tt.wantKind || countOutlineItems(output.Imports) != tt.wantCount || len(block.Children) != tt.wantChildN || !output.Truncated {
				t.Fatalf("nested children should respect max_items budget: output=%#v block=%#v want count=%d children=%d", output, block, tt.wantCount, tt.wantChildN)
			}
		})
	}
}

func TestOutlineFileCategoryBudgetsRespectMaxItems(t *testing.T) {
	tempDir := t.TempDir()
	jsFile := filepath.Join(tempDir, "all.ts")
	goFile := filepath.Join(tempDir, "all.go")
	if err := os.WriteFile(jsFile, []byte("import a from \"a\";\nimport b from \"b\";\nfunction load() { return a; }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goFile, []byte("package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\nfunc main() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	for _, tt := range []struct {
		name       string
		file       string
		maxItems   int
		wantTotal  int
		wantSymbol bool
	}{
		{name: "js import only", file: jsFile, maxItems: 1, wantTotal: 1, wantSymbol: false},
		{name: "js import plus one child", file: jsFile, maxItems: 2, wantTotal: 2, wantSymbol: false},
		{name: "go import only", file: goFile, maxItems: 1, wantTotal: 1, wantSymbol: false},
		{name: "go import plus one child", file: goFile, maxItems: 2, wantTotal: 2, wantSymbol: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{
				TargetFile: tt.file,
				MaxItems:   &tt.maxItems,
			})
			if err != nil {
				t.Fatal(err)
			}
			total := countOutlineItems(output.Imports) + countOutlineItems(output.Symbols) + countOutlineItems(output.Sections)
			if result.IsError || total != tt.wantTotal || (len(output.Symbols) > 0) != tt.wantSymbol || output.OutlineStats.ItemsReturned != tt.wantTotal || !output.Truncated {
				t.Fatalf("include-all category budgeting should respect max_items: result=%#v output=%#v total=%d", result, output, total)
			}
		})
	}
}

func TestOutlineFileJSONAndYAMLReturnContainerAndPathNodes(t *testing.T) {
	tempDir := t.TempDir()
	jsonFile := filepath.Join(tempDir, "config.json")
	yamlFile := filepath.Join(tempDir, "compose.yaml")
	if err := os.WriteFile(jsonFile, []byte("{\n  \"services\": [{\"name\": \"api\"}]\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(yamlFile, []byte("---\nservices:\n  - name: api\n---\nservices:\n  - name: worker\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	jsonResult, jsonOutline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: jsonFile})
	if err != nil {
		t.Fatal(err)
	}
	if jsonResult.IsError || findOutlineItemByKind(jsonOutline.Sections, "document") == nil || findOutlineItemByKind(jsonOutline.Sections, "array") != nil || findOutlineItemByName(jsonOutline.Symbols, "document.services") == nil || findOutlineItemByName(jsonOutline.Symbols, "document.services[0].name") == nil || findOutlineItemByKind(jsonOutline.Symbols, "value") != nil || jsonOutline.OutlineStats.OmittedLeafItems == 0 || countOutlineItems(jsonOutline.Sections) != 1 {
		t.Fatalf("JSON compact outline should expose key paths without section-tree/value noise: result=%#v output=%#v", jsonResult, jsonOutline)
	}
	if jsonOutline.NextRecommendedCall == nil || jsonOutline.NextRecommendedCall.RecommendedNextInputPolicy != "expand_config_leaf_items" || jsonOutline.NextRecommendedCall.RecommendedNextInput["output_profile"] != outlineProfileFull {
		t.Fatalf("JSON agent profile should recommend full profile when leaves are omitted: %#v", jsonOutline.NextRecommendedCall)
	}
	yamlResult, yamlOutline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: yamlFile})
	if err != nil {
		t.Fatal(err)
	}
	if yamlResult.IsError || findOutlineItemByKind(yamlOutline.Sections, "stream") == nil || findOutlineItemByKind(yamlOutline.Sections, "sequence") != nil || findOutlineItemByKindAndName(yamlOutline.Symbols, "key", "document[0].services") == nil || findOutlineItemByKindAndName(yamlOutline.Symbols, "key", "document[0].services[0].name") == nil || findOutlineItemByKind(yamlOutline.Symbols, "value") != nil || yamlOutline.OutlineStats.OmittedLeafItems == 0 || countOutlineItems(yamlOutline.Sections) != 1 {
		t.Fatalf("YAML compact outline should expose key paths without section-tree/value noise: result=%#v output=%#v", yamlResult, yamlOutline)
	}
	yamlFullResult, yamlFullOutline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: yamlFile, OutputProfile: outlineProfileFull})
	if err != nil {
		t.Fatal(err)
	}
	if yamlFullResult.IsError || findOutlineItemByKindAndName(yamlFullOutline.Symbols, "value", "document[0].services[0].name") == nil || yamlFullOutline.OutlineStats.OmittedLeafItems != 0 {
		t.Fatalf("YAML full outline should expose leaf value nodes: result=%#v output=%#v", yamlFullResult, yamlFullOutline)
	}
}

func TestOutlineFileConfigBracketLikeKeysKeepLiteralIdentity(t *testing.T) {
	tempDir := t.TempDir()
	jsonFile := filepath.Join(tempDir, "brackets.json")
	yamlFile := filepath.Join(tempDir, "brackets.yaml")
	jsonContent := "{\n  \"[]\": true,\n  \"[0]\": true,\n  \"[api]\": {\"child\": true},\n  \"object\": true,\n  \"document.api\": true,\n  \"$.api\": true,\n  \" api \": true\n}\n"
	yamlContent := "\"[]\": true\n\"[0]\": true\n\"[api]\":\n  child: true\nobject: true\n\"document.api\": true\n\"$.api\": true\n\" api \": true\n"
	if err := os.WriteFile(jsonFile, []byte(jsonContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(yamlFile, []byte(yamlContent), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	jsonResult, jsonOutline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: jsonFile})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`document["[]"]`, `document["[0]"]`, `document["[api]"]`, "document.object", `document["document.api"]`, `document["$.api"]`, `document[" api "]`} {
		if jsonResult.IsError || findOutlineItemByKindAndName(jsonOutline.Symbols, "property", expected) == nil {
			t.Fatalf("JSON outline should preserve literal key path %q: result=%#v output=%#v", expected, jsonResult, jsonOutline)
		}
	}

	yamlResult, yamlOutline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: yamlFile})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`document["[]"]`, `document["[0]"]`, `document["[api]"]`, "document.object", `document["document.api"]`, `document["$.api"]`, `document[" api "]`} {
		if yamlResult.IsError || findOutlineItemByKindAndName(yamlOutline.Symbols, "key", expected) == nil {
			t.Fatalf("YAML outline should preserve literal key path %q: result=%#v output=%#v", expected, yamlResult, yamlOutline)
		}
	}
}

func TestOutlineFileYAMLSequenceIndexesDoNotResetPerItem(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "list.yaml")
	content := "items:\n  - name: api\n  - name: worker\nscalar_items:\n  - alpha\n  - beta\n"
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	result, outline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file, OutputProfile: outlineProfileFull})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"document.items[0].name", "document.items[1].name", "document.scalar_items[0]", "document.scalar_items[1]"} {
		if result.IsError || findOutlineItemByName(outline.Symbols, expected) == nil {
			t.Fatalf("YAML sequence path should preserve index %q: result=%#v output=%#v", expected, result, outline)
		}
	}
}

func TestOutlineFileInvalidOutputProfileReturnsRepairHint(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "config.json")
	if err := os.WriteFile(file, []byte("{\"service\": true}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{
		TargetFile:    file,
		OutputProfile: "verbose",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || output.ErrorCode != "invalid_output_profile" || output.NextRecommendedCall == nil {
		t.Fatalf("invalid output_profile should return structured repair hint: result=%#v output=%#v", result, output)
	}
	if output.NextRecommendedCall.RecommendedNextTool != "outline_file" || output.NextRecommendedCall.RecommendedNextInput["output_profile"] != outlineProfileAgent {
		t.Fatalf("invalid output_profile repair hint should retry agent profile: %#v", output.NextRecommendedCall)
	}
}

func TestOutlineFileConfigUnicodeKeysAndValuesPreserveUTF8(t *testing.T) {
	tempDir := t.TempDir()
	jsonFile := filepath.Join(tempDir, "unicode.json")
	longKey := strings.Repeat("ключ", 30) + "🙂"
	longValue := strings.Repeat("значение🙂", 20)
	content := fmt.Sprintf("{\n  %q: %q\n}\n", longKey, longValue)
	if err := os.WriteFile(jsonFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	agentResult, agentOutline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: jsonFile})
	if err != nil {
		t.Fatal(err)
	}
	expectedPath := fmt.Sprintf("document[%q]", longKey)
	if agentResult.IsError || findOutlineItemByName(agentOutline.Symbols, expectedPath) == nil {
		t.Fatalf("agent JSON outline should preserve full Unicode key path: result=%#v expected=%q output=%#v", agentResult, expectedPath, agentOutline)
	}
	encodedAgent, err := json.Marshal(agentOutline)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.Valid(encodedAgent) || strings.Contains(string(encodedAgent), "\uFFFD") || strings.Contains(string(encodedAgent), "ï¿½") {
		t.Fatalf("agent JSON outline should not contain replacement characters: %s", encodedAgent)
	}

	fullResult, fullOutline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: jsonFile, OutputProfile: outlineProfileFull})
	if err != nil {
		t.Fatal(err)
	}
	valueItem := findOutlineItemByKindAndName(fullOutline.Symbols, "value", expectedPath)
	if fullResult.IsError || valueItem == nil || !utf8.ValidString(valueItem.Name) || strings.Contains(valueItem.Name, "\uFFFD") {
		t.Fatalf("full JSON outline should preserve UTF-8 in value display names: result=%#v value=%#v output=%#v", fullResult, valueItem, fullOutline)
	}
}

func TestOutlineFilePythonNestedFunctionAndReactNegativeComponentCases(t *testing.T) {
	tempDir := t.TempDir()
	pythonFile := filepath.Join(tempDir, "nested.py")
	tsxFile := filepath.Join(tempDir, "widget.tsx")
	if err := os.WriteFile(pythonFile, []byte("def outer():\n    def helper():\n        return True\n    return helper()\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tsxFile, []byte("class WidgetHelper {\n  value() { return 1 }\n}\nfunction Widget() {\n  return <section />\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	_, pythonOutline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: pythonFile})
	if err != nil {
		t.Fatal(err)
	}
	helper := findOutlineItemByName(pythonOutline.Symbols, "helper")
	if helper == nil || helper.Kind != "function" {
		t.Fatalf("nested Python helper should stay function, not method: %#v", pythonOutline.Symbols)
	}
	_, tsxOutline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: tsxFile})
	if err != nil {
		t.Fatal(err)
	}
	classItem := findOutlineItemByName(tsxOutline.Symbols, "WidgetHelper")
	component := findOutlineItemByName(tsxOutline.Symbols, "Widget")
	if classItem == nil || classItem.Kind != "class" || component == nil || component.Kind != "component" {
		t.Fatalf("React detection should be conservative: class stays class, JSX function is component: %#v", tsxOutline.Symbols)
	}
}

func TestOutlineFileTSXAgentProfileRemovesDuplicateAndLocalVariableNoise(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "widget.tsx")
	source := `import React, { useMemo } from "react";
import type { User } from "./types";
export { helper } from "./helper";

const localFormat = (value: string) => value.trim();
const Widget = ({ user }: { user: User }) => {
  const title = useMemo(() => localFormat(user.name), [user.name]);
  const onClick = () => console.log(title);
  const Inner = () => <strong>{title}</strong>;
  return <section onClick={onClick}><Inner /></section>;
};

export const Card = () => {
  const label = "Ada";
  return <Widget user={{ name: label }} />;
};
`
	if err := os.WriteFile(file, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	result, outline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("TSX compact outline should parse: result=%#v output=%#v", result, outline)
	}
	for _, noisyName := range []string{
		"const localFormat = (value: string) => value.trim();",
		"const Widget = ({ user }: { user: User }) => {",
		"const title = useMemo(() => localFormat(user.name), [user.name]);",
		"title",
		"onClick",
		"Inner",
		"label",
	} {
		if findOutlineItemByName(outline.Symbols, noisyName) != nil {
			t.Fatalf("TSX compact outline should hide duplicate/local variable noise %q: %#v", noisyName, outline.Symbols)
		}
	}
	widget := findOutlineItemByKindAndName(outline.Symbols, "component", "Widget")
	card := findOutlineItemByKindAndName(outline.Symbols, "component", "Card")
	localFormat := findOutlineItemByKindAndName(outline.Symbols, "variable", "localFormat")
	if widget == nil || card == nil || localFormat == nil {
		t.Fatalf("TSX compact outline should keep top-level useful declarations: %#v", outline.Symbols)
	}
	if card.Range.StartLine != 13 || card.Range.EndLine != 16 || card.Selector == nil || card.Selector.Name != "Card" || !boolValue(card.WriteSafe) || !card.Selector.WriteSafe {
		t.Fatalf("exported const component should have clean declaration-level selector: %#v", card)
	}

	fullResult, fullOutline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file, OutputProfile: outlineProfileFull})
	if err != nil {
		t.Fatal(err)
	}
	if fullResult.IsError || findOutlineItemByName(fullOutline.Symbols, "title") == nil || findOutlineItemByName(fullOutline.Symbols, "Inner") == nil {
		t.Fatalf("TSX full outline should expose local variables hidden by compact profile: result=%#v output=%#v", fullResult, fullOutline)
	}
	line := 7
	enclosingResult, enclosingOutline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file, EnclosingLine: &line})
	if err != nil {
		t.Fatal(err)
	}
	if enclosingResult.IsError || len(enclosingOutline.EnclosingItems) == 0 || enclosingOutline.EnclosingItems[0].Name != "title" {
		t.Fatalf("TSX enclosing_line should use full outline and expose hidden local variable: result=%#v enclosing=%#v", enclosingResult, enclosingOutline.EnclosingItems)
	}
}

func TestOutlineFileJSLikeMultiDeclaratorComponentsAreNotWriteSafe(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "multi.tsx")
	source := `import React from "react";

const A = () => <span>A</span>,
  B = () => <span>B</span>;

export const C = () => <span>C</span>, D = () => <span>D</span>;
`
	if err := os.WriteFile(file, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	result, outline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("TSX multi-declarator outline should parse: result=%#v output=%#v", result, outline)
	}
	for _, name := range []string{"B", "D"} {
		item := findOutlineItemByKindAndName(outline.Symbols, "component", name)
		if item == nil || item.Selector == nil {
			t.Fatalf("multi-declarator component %s should be visible for navigation: %#v", name, outline.Symbols)
		}
		if item.WriteSafe == nil || *item.WriteSafe || item.Selector.WriteSafe {
			t.Fatalf("multi-declarator component %s should be exact but not write-safe: %#v", name, item)
		}
	}
}

func TestOutlineFileConfigCompactKeepsKeysAndEscapeHatches(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "config.json")
	source := "{\n  \"services\": [{\"name\": \"api\", \"ports\": [8080]}],\n  \"featureFlags\": {\"newCheckout\": true}\n}\n"
	if err := os.WriteFile(file, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	result, outline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	key := findOutlineItemByKindAndName(outline.Symbols, "property", "document.services[0].name")
	if result.IsError || key == nil || findOutlineItemByKind(outline.Symbols, "value") != nil || findOutlineItemByName(outline.Sections, `document.services["[]"]`) != nil {
		t.Fatalf("compact JSON outline should keep key paths and omit value/synthetic wrapper noise: result=%#v output=%#v", result, outline)
	}
	if key.WriteSafe != nil || key.RefusalReason != "" || key.Selector == nil || key.Selector.WriteSafe {
		t.Fatalf("compact JSON item should reduce item-level write-safe noise while selector remains truthful: %#v", key)
	}
	for _, selector := range []SymbolSelectorQuery{
		{SymbolRef: key.SymbolRef},
		{SymbolPath: key.Path},
	} {
		resolvedResult, resolved, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
			SourceFile:        file,
			SourceFingerprint: *outline.Fingerprint,
			Selector:          selector,
		})
		if err != nil {
			t.Fatal(err)
		}
		if resolvedResult.IsError || resolved.ResolutionStatus != resolveStatusResolved || len(resolved.Matches) != 1 || resolved.Matches[0].Name != key.Name {
			t.Fatalf("compact JSON selector should resolve through full internal outline: selector=%#v result=%#v output=%#v", selector, resolvedResult, resolved)
		}
	}

	window := SourceLineRange{StartLine: 2, EndLine: 2}
	windowResult, windowOutline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file, LineWindow: &window})
	if err != nil {
		t.Fatal(err)
	}
	if windowResult.IsError || findOutlineItemByKind(windowOutline.Symbols, "value") == nil {
		t.Fatalf("line_window should surface config values hidden by compact default: result=%#v output=%#v", windowResult, windowOutline)
	}
	line := 2
	enclosingResult, enclosingOutline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file, EnclosingLine: &line})
	if err != nil {
		t.Fatal(err)
	}
	if enclosingResult.IsError || len(enclosingOutline.EnclosingItems) == 0 || findOutlineItemByKind(enclosingOutline.EnclosingItems, "value") == nil {
		t.Fatalf("enclosing_line should use full config outline and expose hidden value node: result=%#v enclosing=%#v", enclosingResult, enclosingOutline.EnclosingItems)
	}
}

func TestOutlineFilePythonDecoratorsExpandWriteSafeSymbolRange(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "decorated.py")
	source := "@route('/config')\ndef load_config():\n    return True\n\n@entity\nclass Loader:\n    def run(self):\n        return True\n"
	if err := os.WriteFile(file, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	_, outline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	fn := findOutlineItemByName(outline.Symbols, "load_config")
	cls := findOutlineItemByName(outline.Symbols, "Loader")
	if fn == nil || cls == nil {
		t.Fatalf("decorated Python function/class should be outlined as symbols: %#v", outline.Symbols)
	}
	if fn.Range.StartLine != 1 || fn.Range.EndLine != 3 || !*fn.WriteSafe || fn.Metadata["decorated"] != "true" {
		t.Fatalf("decorated function range should include decorator and stay write-safe: %#v", fn)
	}
	if cls.Range.StartLine != 5 || cls.Range.EndLine != 8 || !*cls.WriteSafe || cls.Metadata["decorated"] != "true" {
		t.Fatalf("decorated class range should include decorator and stay write-safe: %#v", cls)
	}
}

func TestOutlineFileTreeSitterParseWarningIncludesLine(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "broken.json")
	if err := os.WriteFile(file, []byte("{\n  \"service\": \n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	result, outline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || outline.ParserStatus != "partial" || len(outline.Warnings) == 0 || outline.Warnings[0].Code != "parse_error" || outline.Warnings[0].Line < 1 {
		t.Fatalf("partial tree-sitter outline should include actionable parse warning line: result=%#v output=%#v", result, outline)
	}
}

func TestResolveSymbolRangeBySymbolRefAndEnclosingLine(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.py")
	source := "class Loader:\n    def load_config(self):\n        return True\n"
	if err := os.WriteFile(file, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	outlineResult, outline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	if outlineResult.IsError || outline.Fingerprint == nil {
		t.Fatalf("outline_file should produce selectors for resolver: result=%#v output=%#v", outlineResult, outline)
	}
	method := findOutlineItemByName(outline.Symbols, "load_config")
	if method == nil || method.SymbolRef == "" {
		t.Fatalf("python method should expose symbol_ref: %#v", outline.Symbols)
	}
	if method.Kind != "method" {
		t.Fatalf("python class child should be classified as method: %#v", method)
	}

	result, output, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        file,
		SourceFingerprint: *outline.Fingerprint,
		Selector:          SymbolSelectorQuery{SymbolRef: method.SymbolRef},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.ResolutionStatus != resolveStatusResolved || len(output.ResolvedRanges) != 1 {
		t.Fatalf("resolve_symbol_range should resolve by symbol_ref: result=%#v output=%#v", result, output)
	}
	if output.ResolvedRanges[0].Range != method.Range || output.NextRecommendedCall == nil || output.NextRecommendedCall.RecommendedNextTool != "read_file" {
		t.Fatalf("resolved range should match outline selector and recommend read_file: %#v method=%#v", output, method)
	}

	line := 2
	enclosingResult, enclosing, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        file,
		SourceFingerprint: *outline.Fingerprint,
		Selector:          SymbolSelectorQuery{EnclosingLine: &line},
	})
	if err != nil {
		t.Fatal(err)
	}
	if enclosingResult.IsError || len(enclosing.Matches) < 2 || enclosing.Matches[0].Name != "load_config" || enclosing.Matches[1].Name != "Loader" {
		t.Fatalf("enclosing_line should return innermost symbol first: result=%#v output=%#v", enclosingResult, enclosing)
	}
}

func TestResolveSymbolRangeByExactRangeRequiresFingerprint(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.py")
	source := "class Loader:\n    def load_config(self):\n        return True\n"
	if err := os.WriteFile(file, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	_, outline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	method := findOutlineItemByName(outline.Symbols, "load_config")
	if method == nil || outline.Fingerprint == nil {
		t.Fatalf("python method should be available for range selector: outline=%#v", outline)
	}

	missingFingerprintResult, missingFingerprintOutput, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        file,
		SourceFingerprint: *outline.Fingerprint,
		Selector:          SymbolSelectorQuery{Range: &method.Range},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !missingFingerprintResult.IsError || missingFingerprintOutput.ErrorCode != "selector_range_fingerprint_required" {
		t.Fatalf("range selector without range_fingerprint should be refused: result=%#v output=%#v", missingFingerprintResult, missingFingerprintOutput)
	}

	result, output, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        file,
		SourceFingerprint: *outline.Fingerprint,
		Selector: SymbolSelectorQuery{
			Range:            &method.Range,
			RangeFingerprint: outline.Fingerprint,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.ResolutionStatus != resolveStatusResolved || output.ParserStatus != "range_selector" || len(output.Matches) != 1 || output.Matches[0].Name != "selected_range" || !output.Matches[0].WriteSafe || len(output.ResolvedRanges) != 1 || output.ResolvedRanges[0].Range != method.Range {
		t.Fatalf("range selector with matching fingerprint should resolve exactly: result=%#v output=%#v method=%#v", result, output, method)
	}
}

func TestResolveSymbolRangeExactRangePreparesDryRunWriteRecommendation(t *testing.T) {
	tempDir := t.TempDir()
	sourceFile := filepath.Join(tempDir, "sample.py")
	targetFile := filepath.Join(tempDir, "selected.txt")
	source := "class Loader:\n    def load_config(self):\n        return True\n"
	if err := os.WriteFile(sourceFile, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	_, outline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: sourceFile})
	if err != nil {
		t.Fatal(err)
	}
	if outline.Fingerprint == nil {
		t.Fatalf("outline should expose fingerprint: %#v", outline)
	}
	selected := SourceLineRange{StartLine: 2, EndLine: 2}
	result, output, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        sourceFile,
		SourceFingerprint: *outline.Fingerprint,
		Selector: SymbolSelectorQuery{
			Range:            &selected,
			RangeFingerprint: outline.Fingerprint,
		},
		TargetIntent: &WriteTargetIntent{
			Operation:  operationCopy,
			TargetFile: targetFile,
			Placement:  TargetPlacement{Mode: placementCreateNew},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.ResolutionStatus != resolveStatusResolved || output.ParserStatus != "range_selector" || output.WriteRecommendationStatus != writeRecommendationReady || output.RecommendedWriteCall == nil {
		t.Fatalf("current exact range selector should prepare dry-run write recommendation: result=%#v output=%#v", result, output)
	}
	nextInput := output.RecommendedWriteCall.RecommendedNextInput
	ranges, ok := nextInput["ranges"].([]SourceLineRange)
	if !ok || len(ranges) != 1 || ranges[0] != selected || nextInput["dry_run"] != true {
		t.Fatalf("exact range recommendation should feed copy_ranges dry-run with selected range: %#v", nextInput)
	}
	targetPrecondition, ok := nextInput["target_precondition"].(TargetPrecondition)
	if !ok || !targetPrecondition.MustNotExist {
		t.Fatalf("exact range recommendation should prepare missing target precondition: %#v", nextInput["target_precondition"])
	}
}

func TestResolveSymbolRangeExactRangeBypassesParserThreshold(t *testing.T) {
	tempDir := t.TempDir()
	sourceFile := filepath.Join(tempDir, "large.go")
	targetFile := filepath.Join(tempDir, "selected.go")
	if err := os.WriteFile(sourceFile, []byte("package sample\n\nfunc A() {}\nfunc B() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Load()
	cfg.WriteThreshold = 1
	h := NewHandler(WithConfig(cfg))
	thresholdResult, thresholdOutline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: sourceFile})
	if err != nil {
		t.Fatal(err)
	}
	if !thresholdResult.IsError || thresholdOutline.Fingerprint == nil || thresholdOutline.ParserStatus != "outline_parse_threshold_exceeded" {
		t.Fatalf("test setup should hit outline threshold but keep fingerprint: result=%#v output=%#v", thresholdResult, thresholdOutline)
	}
	selected := SourceLineRange{StartLine: 3, EndLine: 3}
	result, output, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        sourceFile,
		SourceFingerprint: *thresholdOutline.Fingerprint,
		Selector: SymbolSelectorQuery{
			Range:            &selected,
			RangeFingerprint: thresholdOutline.Fingerprint,
		},
		TargetIntent: &WriteTargetIntent{
			Operation:  operationCopy,
			TargetFile: targetFile,
			Placement:  TargetPlacement{Mode: placementCreateNew},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.ParserStatus != "range_selector" || output.ResolutionStatus != resolveStatusResolved || output.WriteRecommendationStatus != writeRecommendationReady || output.RecommendedWriteCall == nil {
		t.Fatalf("exact range should bypass parser threshold and prepare dry-run recommendation: result=%#v output=%#v", result, output)
	}
	if output.ResolvedRanges[0].Range != selected {
		t.Fatalf("threshold-bypassed exact range should preserve selected range: %#v", output.ResolvedRanges)
	}
}

func TestResolveSymbolRangeRejectsInvalidSelectorRangeAndEnclosingLine(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.py")
	if err := os.WriteFile(file, []byte("def load_config():\n    return True\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	_, outline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	if outline.Fingerprint == nil {
		t.Fatalf("outline should expose fingerprint: %#v", outline)
	}
	badRange := SourceLineRange{StartLine: 2, EndLine: 1}
	rangeResult, rangeOutput, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        file,
		SourceFingerprint: *outline.Fingerprint,
		Selector: SymbolSelectorQuery{
			Range:            &badRange,
			RangeFingerprint: outline.Fingerprint,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rangeResult.IsError || rangeOutput.ErrorCode != "invalid_selector_range" || rangeOutput.ResolutionStatus == resolveStatusResolved {
		t.Fatalf("invalid selector range should be refused before synthetic fallback: result=%#v output=%#v", rangeResult, rangeOutput)
	}
	line := 99
	enclosingResult, enclosingOutput, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        file,
		SourceFingerprint: *outline.Fingerprint,
		Selector:          SymbolSelectorQuery{EnclosingLine: &line},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !enclosingResult.IsError || enclosingOutput.ErrorCode != "invalid_enclosing_line" || enclosingOutput.ResolutionStatus == resolveStatusResolved {
		t.Fatalf("invalid enclosing line should be refused before outline resolution: result=%#v output=%#v", enclosingResult, enclosingOutput)
	}
}

func TestResolveSymbolRangeRejectsEmptyFileSelectorLines(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "empty.txt")
	if err := os.WriteFile(file, nil, 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	_, outline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	if outline.Fingerprint == nil || outline.Fingerprint.LineCount != 0 {
		t.Fatalf("empty file outline should expose line_count=0 fingerprint: %#v", outline)
	}
	r := SourceLineRange{StartLine: 1, EndLine: 1}
	rangeResult, rangeOutput, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        file,
		SourceFingerprint: *outline.Fingerprint,
		Selector: SymbolSelectorQuery{
			Range:            &r,
			RangeFingerprint: outline.Fingerprint,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rangeResult.IsError || rangeOutput.ErrorCode != "invalid_selector_range" || rangeOutput.ResolutionStatus == resolveStatusResolved {
		t.Fatalf("empty-file range selector should be invalid: result=%#v output=%#v", rangeResult, rangeOutput)
	}
	line := 1
	enclosingResult, enclosingOutput, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        file,
		SourceFingerprint: *outline.Fingerprint,
		Selector:          SymbolSelectorQuery{EnclosingLine: &line},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !enclosingResult.IsError || enclosingOutput.ErrorCode != "invalid_enclosing_line" || enclosingOutput.ResolutionStatus == resolveStatusResolved {
		t.Fatalf("empty-file enclosing_line should be invalid: result=%#v output=%#v", enclosingResult, enclosingOutput)
	}
}

func TestResolveSymbolRangeBySymbolRefSearchesBeyondPublicOutlineLimit(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "many.js")
	var source strings.Builder
	const targetIndex = 620
	for i := 0; i < 650; i++ {
		fmt.Fprintf(&source, "function f%03d() {\n  return %d;\n}\n", i, i)
	}
	if err := os.WriteFile(file, []byte(source.String()), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	targetWindow := SourceLineRange{StartLine: targetIndex*3 + 1, EndLine: targetIndex*3 + 3}
	_, windowedOutline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{
		TargetFile:      file,
		LineWindow:      &targetWindow,
		IncludeSymbols:  true,
		IncludeImports:  false,
		IncludeSections: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	item := findOutlineItemByName(windowedOutline.Symbols, "f620")
	if item == nil || item.SymbolRef == "" || windowedOutline.Fingerprint == nil {
		t.Fatalf("windowed outline should expose late symbol_ref: %#v", windowedOutline)
	}
	result, output, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        file,
		SourceFingerprint: *windowedOutline.Fingerprint,
		Selector:          SymbolSelectorQuery{SymbolRef: item.SymbolRef},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.ResolutionStatus != resolveStatusResolved || len(output.Matches) != 1 || output.Matches[0].Name != "f620" || output.ResolvedRanges[0].Range != item.Range {
		t.Fatalf("symbol_ref beyond public outline max_items should resolve through internal unbounded candidates: result=%#v output=%#v item=%#v", result, output, item)
	}
}

func TestResolveSymbolRangeGenericTextEnclosingLine(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "notes.txt")
	if err := os.WriteFile(file, []byte("one\n\ntwo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	_, outline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	line := 3
	result, output, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        file,
		SourceFingerprint: *outline.Fingerprint,
		Selector:          SymbolSelectorQuery{EnclosingLine: &line},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.ResolutionStatus != resolveStatusResolved || output.ParserStatus != "generic_text" || len(output.Matches) != 1 || output.Matches[0].Kind != "text_block" || output.ResolvedRanges[0].Range.StartLine != 3 {
		t.Fatalf("resolve_symbol_range should resolve generic text enclosing_line through internal unbounded outline: result=%#v output=%#v", result, output)
	}
}

func TestResolveSymbolRangeAmbiguousDuplicateNames(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.js")
	source := "function same() {\n  return 1;\n}\nfunction same() {\n  return 2;\n}\n"
	if err := os.WriteFile(file, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	_, outline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	result, output, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        file,
		SourceFingerprint: *outline.Fingerprint,
		Selector:          SymbolSelectorQuery{Name: "same"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.ResolutionStatus != resolveStatusAmbiguous || !output.Ambiguous || len(output.Matches) != 2 || output.NextRecommendedCall == nil || len(output.NextRecommendedCalls) != 2 {
		t.Fatalf("duplicate name selector should return ambiguous candidates with read hints: result=%#v output=%#v", result, output)
	}
}

func TestResolveSymbolRangeTargetIntentRecommendsDryRunCreateNew(t *testing.T) {
	tempDir := t.TempDir()
	sourceFile := filepath.Join(tempDir, "source.py")
	targetFile := filepath.Join(tempDir, "copied.py")
	if err := os.WriteFile(sourceFile, []byte("def load_config():\n    return True\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	_, outline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: sourceFile})
	if err != nil {
		t.Fatal(err)
	}
	item := findOutlineItemByName(outline.Symbols, "load_config")
	if item == nil || outline.Fingerprint == nil {
		t.Fatalf("source outline should expose write-safe symbol: %#v", outline)
	}
	result, output, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        sourceFile,
		SourceFingerprint: *outline.Fingerprint,
		Selector: SymbolSelectorQuery{
			Range:            &item.Range,
			RangeFingerprint: outline.Fingerprint,
		},
		TargetIntent: &WriteTargetIntent{
			Operation:  operationCopy,
			TargetFile: targetFile,
			Placement:  TargetPlacement{Mode: placementCreateNew},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.WriteRecommendationStatus != writeRecommendationReady || output.TargetSyntaxProof != targetSyntaxProofCreateNew || output.RecommendedWriteCall == nil {
		t.Fatalf("create_new target intent should return ready dry-run write recommendation: result=%#v output=%#v", result, output)
	}
	if output.NextRecommendedCall == nil || output.NextRecommendedCall.RecommendedNextTool != "read_file" {
		t.Fatalf("write recommendation must not replace read inspection hint: %#v", output.NextRecommendedCall)
	}
	nextInput := output.RecommendedWriteCall.RecommendedNextInput
	ranges, ok := nextInput["ranges"].([]SourceLineRange)
	if !ok || len(ranges) != 1 || ranges[0] != item.Range || nextInput["dry_run"] != true || nextInput["source_file"] != sourceFile || nextInput["target_file"] != targetFile {
		t.Fatalf("recommended write input should contain concrete dry-run range-tool fields: %#v item=%#v", nextInput, item)
	}
	targetPrecondition, ok := nextInput["target_precondition"].(TargetPrecondition)
	if !ok || !targetPrecondition.MustNotExist || targetPrecondition.Fingerprint != nil {
		t.Fatalf("create_new recommendation should prepare must_not_exist precondition: %#v", nextInput["target_precondition"])
	}
	copyResult, copyOutput, err := h.HandleCopyRanges(context.Background(), nil, CopyRangesInput{
		SourceFile:         sourceFile,
		SourceFingerprint:  *outline.Fingerprint,
		Ranges:             ranges,
		TargetFile:         targetFile,
		TargetPrecondition: TargetPrecondition{MustNotExist: true},
		Placement:          TargetPlacement{Mode: placementCreateNew},
		DryRun:             true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if copyResult.IsError || len(copyOutput.DiffPreviews) == 0 || copyOutput.Validation.Status == "" {
		t.Fatalf("recommended dry-run copy_ranges call should produce Phase 5 preview/validation output: result=%#v output=%#v", copyResult, copyOutput)
	}
}

func TestResolveSymbolRangeRefusesMoveForNestedPythonSymbol(t *testing.T) {
	tempDir := t.TempDir()
	sourceFile := filepath.Join(tempDir, "source.py")
	targetFile := filepath.Join(tempDir, "copied.py")
	if err := os.WriteFile(sourceFile, []byte("class Loader:\n    def load_config(self):\n        return True\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	_, outline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: sourceFile})
	if err != nil {
		t.Fatal(err)
	}
	item := findOutlineItemByName(outline.Symbols, "load_config")
	if item == nil || outline.Fingerprint == nil || item.Metadata["nested"] != "true" {
		t.Fatalf("nested Python method should expose nested metadata: %#v", item)
	}
	result, output, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        sourceFile,
		SourceFingerprint: *outline.Fingerprint,
		Selector:          SymbolSelectorQuery{SymbolRef: item.SymbolRef},
		TargetIntent: &WriteTargetIntent{
			Operation:          operationMove,
			TargetFile:         targetFile,
			TargetPrecondition: TargetPrecondition{MustNotExist: true},
			Placement:          TargetPlacement{Mode: placementCreateNew},
			DryRun:             true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.WriteRefusalCode != "symbol_source_deletion_not_proven" || output.RecommendedWriteCall != nil {
		t.Fatalf("move recommendation should refuse nested Python source deletion: result=%#v output=%#v", result, output)
	}
}

func TestResolveSymbolRangeTargetIntentWorksForGoAndMarkdownExactItems(t *testing.T) {
	tempDir := t.TempDir()
	goFile := filepath.Join(tempDir, "source.go")
	mdFile := filepath.Join(tempDir, "notes.md")
	if err := os.WriteFile(goFile, []byte("package main\n\nfunc Load() int {\n\treturn 1\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mdFile, []byte("# Alpha\n\nKeep line.\n\n## Beta\n\nMove me.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()

	_, goOutline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: goFile})
	if err != nil {
		t.Fatal(err)
	}
	goItem := findOutlineItemByName(goOutline.Symbols, "Load")
	if goItem == nil || goItem.Selector == nil || goItem.WriteSafe == nil || !*goItem.WriteSafe {
		t.Fatalf("Go function should expose write-safe selector metadata: %#v", goOutline.Symbols)
	}
	goResult, goOutput, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        goFile,
		SourceFingerprint: *goOutline.Fingerprint,
		Selector:          SymbolSelectorQuery{SymbolRef: goItem.SymbolRef},
		TargetIntent: &WriteTargetIntent{
			Operation:          operationCopy,
			TargetFile:         filepath.Join(tempDir, "copy.go"),
			TargetPrecondition: TargetPrecondition{MustNotExist: true},
			Placement:          TargetPlacement{Mode: placementCreateNew},
			DryRun:             true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if goResult.IsError || goOutput.WriteRecommendationStatus != writeRecommendationReady || goOutput.RecommendedWriteCall == nil {
		t.Fatalf("Go selector should produce ready dry-run write recommendation: result=%#v output=%#v", goResult, goOutput)
	}

	_, mdOutline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: mdFile})
	if err != nil {
		t.Fatal(err)
	}
	mdItem := findOutlineItemByName(mdOutline.Sections, "Beta")
	if mdItem == nil || mdItem.Selector == nil || mdItem.WriteSafe == nil || !*mdItem.WriteSafe {
		t.Fatalf("Markdown section should expose write-safe selector metadata: %#v", mdOutline.Sections)
	}
	mdResult, mdOutput, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        mdFile,
		SourceFingerprint: *mdOutline.Fingerprint,
		Selector:          SymbolSelectorQuery{SymbolRef: mdItem.SymbolRef},
		TargetIntent: &WriteTargetIntent{
			Operation:          operationCopy,
			TargetFile:         filepath.Join(tempDir, "section.md"),
			TargetPrecondition: TargetPrecondition{MustNotExist: true},
			Placement:          TargetPlacement{Mode: placementCreateNew},
			DryRun:             true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if mdResult.IsError || mdOutput.WriteRecommendationStatus != writeRecommendationReady || mdOutput.RecommendedWriteCall == nil {
		t.Fatalf("Markdown selector should produce ready dry-run write recommendation: result=%#v output=%#v", mdResult, mdOutput)
	}
}

func TestResolveSymbolRangeTargetIntentAlwaysReturnsDryRunPreviewHint(t *testing.T) {
	tempDir := t.TempDir()
	sourceFile := filepath.Join(tempDir, "source.py")
	targetFile := filepath.Join(tempDir, "copied.py")
	if err := os.WriteFile(sourceFile, []byte("def load_config():\n    return True\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	_, outline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: sourceFile})
	if err != nil {
		t.Fatal(err)
	}
	item := findOutlineItemByName(outline.Symbols, "load_config")
	result, output, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        sourceFile,
		SourceFingerprint: *outline.Fingerprint,
		Selector:          SymbolSelectorQuery{SymbolRef: item.SymbolRef},
		TargetIntent: &WriteTargetIntent{
			Operation:          operationCopy,
			TargetFile:         targetFile,
			TargetPrecondition: TargetPrecondition{MustNotExist: true},
			Placement:          TargetPlacement{Mode: placementCreateNew},
			DryRun:             false,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.WriteRecommendationStatus != writeRecommendationReady || output.RecommendedWriteCall == nil || output.PreviewWriteCall != nil {
		t.Fatalf("target intent should always return a dry-run recommendation regardless of dry_run input: result=%#v output=%#v", result, output)
	}
	if output.RecommendedWriteCall.RecommendedNextInput["dry_run"] != true {
		t.Fatalf("recommended write call must force dry_run=true: %#v", output.RecommendedWriteCall)
	}
}

func TestResolveSymbolRangeTargetIntentAllowsExistingMarkdownTarget(t *testing.T) {
	tempDir := t.TempDir()
	sourceFile := filepath.Join(tempDir, "source.py")
	targetFile := filepath.Join(tempDir, "notes.md")
	if err := os.WriteFile(sourceFile, []byte("def load_config():\n    return True\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetFile, []byte("# Notes\n\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	_, sourceOutline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: sourceFile})
	if err != nil {
		t.Fatal(err)
	}
	item := findOutlineItemByName(sourceOutline.Symbols, "load_config")
	result, output, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        sourceFile,
		SourceFingerprint: *sourceOutline.Fingerprint,
		Selector:          SymbolSelectorQuery{SymbolRef: item.SymbolRef},
		TargetIntent: &WriteTargetIntent{
			Operation:  operationCopy,
			TargetFile: targetFile,
			Placement:  TargetPlacement{Mode: placementAppend},
			Joiner:     "blank_line",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.WriteRecommendationStatus != writeRecommendationReady || output.TargetSyntaxProof != targetSyntaxProofMarkdownOK || output.RecommendedWriteCall == nil {
		t.Fatalf("existing Markdown target should allow dry-run symbol recommendation: result=%#v output=%#v", result, output)
	}
	targetPrecondition, ok := output.RecommendedWriteCall.RecommendedNextInput["target_precondition"].(TargetPrecondition)
	if !ok || targetPrecondition.Fingerprint == nil || targetPrecondition.MustNotExist {
		t.Fatalf("existing target recommendation should prepare fingerprint precondition: %#v", output.RecommendedWriteCall.RecommendedNextInput)
	}
}

func TestResolveSymbolRangeFullConfigLeafSymbolRefRoundTripsReadOnly(t *testing.T) {
	tempDir := t.TempDir()
	sourceFile := filepath.Join(tempDir, "config.json")
	targetFile := filepath.Join(tempDir, "copied.txt")
	if err := os.WriteFile(sourceFile, []byte("{\n  \"service\": {\n    \"enabled\": true\n  }\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	fullResult, fullOutline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{
		TargetFile:    sourceFile,
		OutputProfile: outlineProfileFull,
	})
	if err != nil {
		t.Fatal(err)
	}
	valueItem := findOutlineItemByKindAndName(fullOutline.Symbols, "value", "document.service.enabled")
	if fullResult.IsError || valueItem == nil || valueItem.SymbolRef == "" || fullOutline.Fingerprint == nil {
		t.Fatalf("full JSON outline should expose value leaf symbol_ref: result=%#v output=%#v", fullResult, fullOutline)
	}
	result, output, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        sourceFile,
		SourceFingerprint: *fullOutline.Fingerprint,
		Selector:          SymbolSelectorQuery{SymbolRef: valueItem.SymbolRef},
		TargetIntent: &WriteTargetIntent{
			Operation:  operationCopy,
			TargetFile: targetFile,
			Placement:  TargetPlacement{Mode: placementCreateNew},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.ResolutionStatus != resolveStatusResolved || len(output.ResolvedRanges) != 1 || output.ResolvedRanges[0].Range != valueItem.Range {
		t.Fatalf("resolver should round-trip full-profile JSON value symbol_ref for exact read range: result=%#v output=%#v value=%#v", result, output, valueItem)
	}
	if output.WriteRefusalCode != "symbol_range_not_write_safe" || output.RecommendedWriteCall != nil || len(output.Matches) != 1 || output.Matches[0].WriteSafe {
		t.Fatalf("full-profile JSON value should remain read-only/write-unsafe: result=%#v output=%#v value=%#v", result, output, valueItem)
	}
}

func TestResolveSymbolRangeTargetIntentStaleTargetFingerprintReturnsRefreshHint(t *testing.T) {
	tempDir := t.TempDir()
	sourceFile := filepath.Join(tempDir, "source.py")
	targetFile := filepath.Join(tempDir, "notes.md")
	if err := os.WriteFile(sourceFile, []byte("def load_config():\n    return True\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetFile, []byte("# Notes\n\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	_, sourceOutline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: sourceFile})
	if err != nil {
		t.Fatal(err)
	}
	_, targetOutline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: targetFile})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetFile, []byte("# Notes\n\nChanged.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	item := findOutlineItemByName(sourceOutline.Symbols, "load_config")
	result, output, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        sourceFile,
		SourceFingerprint: *sourceOutline.Fingerprint,
		Selector:          SymbolSelectorQuery{SymbolRef: item.SymbolRef},
		TargetIntent: &WriteTargetIntent{
			Operation:          operationCopy,
			TargetFile:         targetFile,
			TargetPrecondition: TargetPrecondition{Fingerprint: targetOutline.Fingerprint},
			Placement:          TargetPlacement{Mode: placementAppend},
			DryRun:             true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.WriteRefusalCode != "target_fingerprint_mismatch" || output.ActionHint == nil {
		t.Fatalf("stale target fingerprint should return refresh hint without tool error: result=%#v output=%#v", result, output)
	}
	if output.ActionHint.RecommendedNextTool != "inspect_path" || output.ActionHint.RecommendedNextInput["target_path"] != targetFile {
		t.Fatalf("stale target fingerprint should recommend exact target inspection: %#v", output.ActionHint)
	}
}

func TestResolveSymbolRangeTargetIntentRefusesNonWholeLineExactRange(t *testing.T) {
	tempDir := t.TempDir()
	sourceFile := filepath.Join(tempDir, "config.json")
	targetFile := filepath.Join(tempDir, "copied.txt")
	if err := os.WriteFile(sourceFile, []byte("{\"service\": true, \"debug\": false}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	_, outline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: sourceFile})
	if err != nil {
		t.Fatal(err)
	}
	item := findOutlineItemByDisplayNameSuffix(outline.Symbols, "document.service")
	if item == nil || item.Selector == nil || item.Selector.WriteSafe {
		t.Fatalf("same-line JSON property should be exact but not write-safe: %#v", outline.Symbols)
	}
	result, output, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        sourceFile,
		SourceFingerprint: *outline.Fingerprint,
		Selector:          SymbolSelectorQuery{SymbolRef: item.SymbolRef},
		TargetIntent: &WriteTargetIntent{
			Operation:          operationCopy,
			TargetFile:         targetFile,
			TargetPrecondition: TargetPrecondition{MustNotExist: true},
			Placement:          TargetPlacement{Mode: placementCreateNew},
			DryRun:             true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.WriteRefusalCode != "symbol_range_not_write_safe" || output.RecommendedWriteCall != nil {
		t.Fatalf("non-whole-line exact range should resolve for read but refuse write recommendation: result=%#v output=%#v", result, output)
	}
}

func TestResolveSymbolRangeTargetIntentRefusesJsonDelimiterSensitiveWholeLineRange(t *testing.T) {
	tempDir := t.TempDir()
	sourceFile := filepath.Join(tempDir, "config.json")
	targetFile := filepath.Join(tempDir, "copied.txt")
	if err := os.WriteFile(sourceFile, []byte("{\n  \"first\": true,\n  \"last\": false\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	_, outline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: sourceFile})
	if err != nil {
		t.Fatal(err)
	}
	item := findOutlineItemByDisplayNameSuffix(outline.Symbols, "document.last")
	if item == nil || item.Selector == nil || !item.Selector.WholeLineRange || item.Selector.WriteSafe {
		t.Fatalf("multi-line JSON property should be whole-line exact but not delimiter write-safe: %#v", item)
	}
	result, output, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        sourceFile,
		SourceFingerprint: *outline.Fingerprint,
		Selector:          SymbolSelectorQuery{SymbolRef: item.SymbolRef},
		TargetIntent: &WriteTargetIntent{
			Operation:          operationMove,
			TargetFile:         targetFile,
			TargetPrecondition: TargetPrecondition{MustNotExist: true},
			Placement:          TargetPlacement{Mode: placementCreateNew},
			DryRun:             true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.WriteRefusalCode != "symbol_range_not_write_safe" || output.RecommendedWriteCall != nil {
		t.Fatalf("delimiter-sensitive JSON property should resolve for read but refuse move recommendation: result=%#v output=%#v", result, output)
	}
}

func TestResolveSymbolRangeTargetIntentRefusesSameFileAndStructuredTarget(t *testing.T) {
	tempDir := t.TempDir()
	sourceFile := filepath.Join(tempDir, "source.py")
	targetJSON := filepath.Join(tempDir, "config.json")
	if err := os.WriteFile(sourceFile, []byte("def load_config():\n    return True\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetJSON, []byte("{\n  \"existing\": true\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	_, outline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: sourceFile})
	if err != nil {
		t.Fatal(err)
	}
	item := findOutlineItemByName(outline.Symbols, "load_config")
	sameFileResult, sameFileOutput, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        sourceFile,
		SourceFingerprint: *outline.Fingerprint,
		Selector:          SymbolSelectorQuery{SymbolRef: item.SymbolRef},
		TargetIntent: &WriteTargetIntent{
			Operation:          operationMove,
			TargetFile:         sourceFile,
			TargetPrecondition: TargetPrecondition{MustNotExist: true},
			Placement:          TargetPlacement{Mode: placementCreateNew},
			DryRun:             true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sameFileResult.IsError || sameFileOutput.WriteRefusalCode != "target_same_file_unsupported" || sameFileOutput.RecommendedWriteCall != nil {
		t.Fatalf("same-file symbol write recommendation should be refused: result=%#v output=%#v", sameFileResult, sameFileOutput)
	}

	_, targetOutline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: targetJSON})
	if err != nil {
		t.Fatal(err)
	}
	structuredResult, structuredOutput, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        sourceFile,
		SourceFingerprint: *outline.Fingerprint,
		Selector:          SymbolSelectorQuery{SymbolRef: item.SymbolRef},
		TargetIntent: &WriteTargetIntent{
			Operation:          operationCopy,
			TargetFile:         targetJSON,
			TargetPrecondition: TargetPrecondition{Fingerprint: targetOutline.Fingerprint},
			Placement:          TargetPlacement{Mode: placementAppend},
			TargetSyntaxMode:   "plain_text",
			DryRun:             true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if structuredResult.IsError || structuredOutput.WriteRefusalCode != "target_syntax_not_proven" || structuredOutput.TargetSyntaxStatus != targetSyntaxUnknown || structuredOutput.RecommendedWriteCall != nil {
		t.Fatalf("structured target must refuse unproven symbol write recommendation: result=%#v output=%#v", structuredResult, structuredOutput)
	}
}

func TestResolveSymbolRangeTargetIntentRejectsSymlinkRecommendations(t *testing.T) {
	tempDir := t.TempDir()
	sourceFile := filepath.Join(tempDir, "source.py")
	if err := os.WriteFile(sourceFile, []byte("def load_config():\n    return True\n"), 0644); err != nil {
		t.Fatal(err)
	}
	sourceLink := filepath.Join(tempDir, "source-link.py")
	if err := os.Symlink("source.py", sourceLink); err != nil {
		t.Skipf("symlink creation unavailable in this environment: %v", err)
	}
	targetFile := filepath.Join(tempDir, "target.py")
	targetLink := filepath.Join(tempDir, "target-link.py")
	if err := os.WriteFile(targetFile, []byte("target\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.py", targetLink); err != nil {
		t.Skipf("target symlink creation unavailable in this environment: %v", err)
	}

	h := NewHandler()
	_, sourceLinkOutline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: sourceLink})
	if err != nil {
		t.Fatal(err)
	}
	linkItem := findOutlineItemByName(sourceLinkOutline.Symbols, "load_config")
	if linkItem == nil || sourceLinkOutline.Fingerprint == nil {
		t.Fatalf("source symlink outline should expose symbol for recommendation refusal: %#v", sourceLinkOutline)
	}
	sourceResult, sourceOutput, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        sourceLink,
		SourceFingerprint: *sourceLinkOutline.Fingerprint,
		Selector:          SymbolSelectorQuery{SymbolRef: linkItem.SymbolRef},
		TargetIntent: &WriteTargetIntent{
			Operation:          operationCopy,
			TargetFile:         filepath.Join(tempDir, "created.py"),
			TargetPrecondition: TargetPrecondition{MustNotExist: true},
			Placement:          TargetPlacement{Mode: placementCreateNew},
			DryRun:             true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sourceResult.IsError || sourceOutput.WriteRefusalCode != "source_symlink_unsupported" || sourceOutput.RecommendedWriteCall != nil {
		t.Fatalf("source symlink recommendation should be refused before ready status: result=%#v output=%#v", sourceResult, sourceOutput)
	}

	_, outline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: sourceFile})
	if err != nil {
		t.Fatal(err)
	}
	item := findOutlineItemByName(outline.Symbols, "load_config")
	if item == nil || outline.Fingerprint == nil {
		t.Fatalf("source outline should expose symbol: %#v", outline)
	}
	targetResult, targetOutput, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        sourceFile,
		SourceFingerprint: *outline.Fingerprint,
		Selector:          SymbolSelectorQuery{SymbolRef: item.SymbolRef},
		TargetIntent: &WriteTargetIntent{
			Operation:          operationCopy,
			TargetFile:         targetLink,
			TargetPrecondition: TargetPrecondition{MustNotExist: true},
			Placement:          TargetPlacement{Mode: placementCreateNew},
			DryRun:             true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !targetResult.IsError || targetOutput.WriteRefusalCode != "target_symlink_unsupported" || targetOutput.RecommendedWriteCall != nil {
		t.Fatalf("target symlink recommendation should be refused before ready status: result=%#v output=%#v", targetResult, targetOutput)
	}
}

func TestResolveSymbolRangeCwdProjection(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "sample.py"), []byte("def load_config():\n    return True\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(WithConfig(cwdRegistryTestConfig(t)))
	cwdID := setCwdWithHandlerForTest(t, h, tempDir)
	cwdInput := CwdAwareInput{CwdID: CwdIDInput{Present: true, Value: cwdID}}
	_, outline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{
		CwdAwareInput: cwdInput,
		TargetFile:    "sample.py",
	})
	if err != nil {
		t.Fatal(err)
	}
	item := findOutlineItemAcrossCategories(outline, "load_config")
	if item == nil || outline.Fingerprint == nil {
		t.Fatalf("cwd outline should expose relative selector item: %#v", outline)
	}
	result, output, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		CwdAwareInput:     cwdInput,
		SourceFile:        "sample.py",
		SourceFingerprint: *outline.Fingerprint,
		Selector:          SymbolSelectorQuery{SymbolRef: item.SymbolRef},
	})
	if err != nil {
		t.Fatal(err)
	}
	pathCtx, cwdErr := h.BuildPathContext(cwdInput.CwdID)
	if cwdErr != nil {
		t.Fatal(cwdErr)
	}
	AttachCwdOutputMeta(&output, pathCtx)
	if result.IsError || output.CwdID == nil || *output.CwdID != cwdID || output.Cwd == "" || output.File != "sample.py" {
		t.Fatalf("cwd resolver output should stay relative and carry cwd meta: result=%#v output=%#v", result, output)
	}
	if output.NextRecommendedCall == nil || output.NextRecommendedCall.RecommendedNextInput["target_file"] != "sample.py" || output.NextRecommendedCall.RecommendedNextInput["cwd_id"] != cwdID {
		t.Fatalf("cwd resolver read hint should use relative target_file and cwd_id: %#v", output.NextRecommendedCall)
	}

	result, output, err = h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		CwdAwareInput:     cwdInput,
		SourceFile:        "sample.py",
		SourceFingerprint: *outline.Fingerprint,
		Selector:          SymbolSelectorQuery{SymbolRef: item.SymbolRef},
		TargetIntent: &WriteTargetIntent{
			Operation:          operationCopy,
			TargetFile:         "out.py",
			TargetPrecondition: TargetPrecondition{MustNotExist: true},
			Placement:          TargetPlacement{Mode: placementCreateNew},
			DryRun:             true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	AttachCwdOutputMeta(&output, pathCtx)
	if result.IsError || output.RecommendedWriteCall == nil {
		t.Fatalf("cwd resolver should produce write recommendation: result=%#v output=%#v", result, output)
	}
	writeInput := output.RecommendedWriteCall.RecommendedNextInput
	if writeInput["source_file"] != "sample.py" || writeInput["target_file"] != "out.py" || writeInput["cwd_id"] != cwdID {
		t.Fatalf("cwd write recommendation should stay relative and carry cwd_id: %#v", writeInput)
	}
}

func TestResolveSymbolRangeCwdSanitizesRefusalReasons(t *testing.T) {
	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "sample.py")
	if err := os.WriteFile(sourcePath, []byte("def load_config():\n    return True\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(WithConfig(cwdRegistryTestConfig(t)))
	cwdID := setCwdWithHandlerForTest(t, h, tempDir)
	cwdInput := CwdAwareInput{CwdID: CwdIDInput{Present: true, Value: cwdID}}
	_, outline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{
		CwdAwareInput: cwdInput,
		TargetFile:    "sample.py",
	})
	if err != nil {
		t.Fatal(err)
	}
	item := findOutlineItemAcrossCategories(outline, "load_config")
	if item == nil || outline.Fingerprint == nil {
		t.Fatalf("cwd outline should expose selector item: %#v", outline)
	}
	pathCtx, cwdErr := h.BuildPathContext(cwdInput.CwdID)
	if cwdErr != nil {
		t.Fatal(cwdErr)
	}
	absNeedle := filepath.ToSlash(tempDir)
	result, output, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		CwdAwareInput:     cwdInput,
		SourceFile:        "sample.py",
		SourceFingerprint: *outline.Fingerprint,
		Selector:          SymbolSelectorQuery{SymbolRef: item.SymbolRef},
		TargetIntent: &WriteTargetIntent{
			Operation:          operationCopy,
			TargetFile:         "missing.md",
			TargetPrecondition: TargetPrecondition{Fingerprint: outline.Fingerprint},
			Placement:          TargetPlacement{Mode: placementAppend},
			TargetSyntaxMode:   "markdown",
			DryRun:             true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	AttachCwdOutputMeta(&output, pathCtx)
	if result.IsError || output.WriteRefusalCode != "target_syntax_not_proven" {
		t.Fatalf("missing target should produce syntax refusal without top-level tool error: result=%#v output=%#v", result, output)
	}
	if strings.Contains(filepath.ToSlash(output.WriteRefusalReason), absNeedle) || strings.Contains(filepath.ToSlash(output.TargetSyntaxProofReason), absNeedle) {
		t.Fatalf("cwd refusal fields should not leak absolute cwd paths: output=%#v abs=%q", output, absNeedle)
	}

	linkPath := filepath.Join(tempDir, "source-link.py")
	if err := os.Symlink("sample.py", linkPath); err == nil {
		_, linkOutline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{
			CwdAwareInput: cwdInput,
			TargetFile:    "source-link.py",
		})
		if err != nil {
			t.Fatal(err)
		}
		linkItem := findOutlineItemAcrossCategories(linkOutline, "load_config")
		if linkItem == nil || linkOutline.Fingerprint == nil {
			t.Fatalf("cwd symlink outline should expose selector item: %#v", linkOutline)
		}
		linkResult, linkOutput, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
			CwdAwareInput:     cwdInput,
			SourceFile:        "source-link.py",
			SourceFingerprint: *linkOutline.Fingerprint,
			Selector:          SymbolSelectorQuery{SymbolRef: linkItem.SymbolRef},
			TargetIntent: &WriteTargetIntent{
				Operation:          operationCopy,
				TargetFile:         "out.py",
				TargetPrecondition: TargetPrecondition{MustNotExist: true},
				Placement:          TargetPlacement{Mode: placementCreateNew},
				DryRun:             true,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		AttachCwdOutputMeta(&linkOutput, pathCtx)
		if !linkResult.IsError || linkOutput.WriteRefusalCode != "source_symlink_unsupported" || strings.Contains(filepath.ToSlash(linkOutput.WriteRefusalReason), absNeedle) {
			t.Fatalf("cwd symlink refusal should be sanitized: result=%#v output=%#v abs=%q", linkResult, linkOutput, absNeedle)
		}
	} else {
		t.Logf("symlink creation unavailable in this environment: %v", err)
	}
}

func TestResolveSymbolRangeRejectsStaleFingerprintAndLanguageConflict(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.ts")
	if err := os.WriteFile(file, []byte("class Loader {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler()
	_, outline, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	if outline.Fingerprint == nil {
		t.Fatalf("outline should include fingerprint: %#v", outline)
	}
	stale := *outline.Fingerprint
	stale.SHA256 = "stale"
	staleResult, staleOutput, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        file,
		SourceFingerprint: stale,
		Selector:          SymbolSelectorQuery{Name: "Loader"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !staleResult.IsError || staleOutput.ErrorCode != "symbol_fingerprint_mismatch" || staleOutput.ActionHint == nil {
		t.Fatalf("stale fingerprint should be rejected with outline refresh hint: result=%#v output=%#v", staleResult, staleOutput)
	}

	conflictResult, conflictOutput, err := h.HandleResolveSymbolRange(context.Background(), nil, ResolveSymbolRangeInput{
		SourceFile:        file,
		SourceFingerprint: *outline.Fingerprint,
		Language:          "typescript",
		Selector:          SymbolSelectorQuery{Language: "python", Name: "Loader"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !conflictResult.IsError || conflictOutput.ErrorCode != "selector_language_conflict" {
		t.Fatalf("language conflict should be a whole-call error: result=%#v output=%#v", conflictResult, conflictOutput)
	}
}

func TestOutlineFileGenericTextLineWindowChunksOnlyWindow(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(file, []byte("outside one\noutside two\ninside    one\tvalue\ninside two\noutside three\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{
		TargetFile: file,
		LineWindow: &SourceLineRange{
			StartLine: 3,
			EndLine:   4,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("generic text line_window should return structured outline output: %#v", output)
	}
	if output.ParserStatus != "generic_text" || len(output.Sections) != 1 {
		t.Fatalf("generic text line_window should return one in-window block: %#v", output)
	}
	block := output.Sections[0]
	if block.Range.StartLine != 3 || block.Range.EndLine != 4 || block.Name != "inside one value" {
		t.Fatalf("generic text block should stay inside requested line_window: %#v", block)
	}
}

func TestOutlineFileGenericTextChunkBoundaries(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "chunks.txt")
	nearLimit := strings.Repeat("a", genericTextTargetBytes-2)
	longLine := strings.Repeat("b", genericTextTargetBytes+200)
	var content strings.Builder
	content.WriteString(nearLimit)
	content.WriteByte('\n')
	content.WriteString(longLine)
	content.WriteByte('\n')
	for i := 0; i < genericTextMaxBlockLines+1; i++ {
		content.WriteString("c\n")
	}
	if err := os.WriteFile(file, []byte(content.String()), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("generic text chunk outline should succeed: %#v", output)
	}
	if len(output.Sections) < 4 {
		t.Fatalf("expected multiple generic text chunks, got %#v", output.Sections)
	}
	if output.Sections[0].Range.StartLine != 1 || output.Sections[0].Range.EndLine != 1 {
		t.Fatalf("near-limit first line should not absorb next line: %#v", output.Sections[0])
	}
	if output.Sections[1].Range.StartLine != 2 || output.Sections[1].Range.EndLine != 2 {
		t.Fatalf("single over-limit line should be its own chunk: %#v", output.Sections[1])
	}
	if output.Sections[2].Range.StartLine != 3 || output.Sections[2].Range.EndLine != 42 {
		t.Fatalf("generic chunks should split at max block lines: %#v", output.Sections[2])
	}
	if output.Sections[3].Range.StartLine != 43 || output.Sections[3].Range.EndLine != 43 {
		t.Fatalf("line after max block split should be preserved: %#v", output.Sections[3])
	}
}

func TestOutlineFileGenericTextTruncationHintUsesNextChunk(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "truncated.txt")
	if err := os.WriteFile(file, []byte("one\n\ntwo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	maxItems := 1

	h := NewHandler()
	result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{
		TargetFile: file,
		MaxItems:   &maxItems,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("generic text truncation should return structured output: %#v", output)
	}
	if !output.Truncated || output.NextRecommendedCall == nil || output.OutlineStats.NextOmittedLine != 3 {
		t.Fatalf("generic text truncation should point at next chunk: %#v", output)
	}
	lineWindow, ok := output.NextRecommendedCall.RecommendedNextInput["line_window"].(map[string]any)
	if !ok || lineWindow["start_line"] == nil {
		t.Fatalf("generic text truncation hint should include line_window: %#v", output.NextRecommendedCall)
	}
	nextBytes, err := json.Marshal(output.NextRecommendedCall.RecommendedNextInput)
	if err != nil {
		t.Fatal(err)
	}
	var nextInput OutlineFileInput
	if err := json.Unmarshal(nextBytes, &nextInput); err != nil {
		t.Fatal(err)
	}
	nextResult, nextOutput, err := h.HandleOutlineFile(context.Background(), nil, nextInput)
	if err != nil {
		t.Fatal(err)
	}
	if nextResult.IsError || nextOutput.Language != outlineLanguageText || len(nextOutput.Sections) != 1 || nextOutput.Sections[0].Range.StartLine != 3 {
		t.Fatalf("generic text continuation hint should replay successfully: result=%#v output=%#v input=%#v", nextResult, nextOutput, nextInput)
	}
}

func TestOutlineFileRejectsInvalidLineWindow(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "concept.md")
	if err := os.WriteFile(file, []byte("# One\n"), 0644); err != nil {
		t.Fatal(err)
	}
	window := SourceLineRange{StartLine: 2, EndLine: 1}

	h := NewHandler()
	result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{
		TargetFile: file,
		LineWindow: &window,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(output.Error, "line_window must") {
		t.Fatalf("expected invalid line_window structured error: result=%#v output=%#v", result, output)
	}
}

func TestOutlineFileGoReturnsImportsAndSymbols(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.go")
	content := "package sample\n\nimport (\n\t\"fmt\"\n\t\"io\"\n)\n\ntype (\n\tService struct{}\n\tRunner interface{ Run() }\n)\n\nfunc New() *Service { return &Service{} }\n\nfunc (s *Service) Run() { fmt.Println(\"ok\", io.EOF) }\n"
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("outline_file go returned error: %#v", output)
	}
	if output.Language != "go" || output.ParserStatus != "ok" || len(output.Imports) != 1 {
		t.Fatalf("unexpected go outline metadata: %#v", output)
	}
	if output.Imports[0].Kind != "import_block" || output.Imports[0].Range.StartLine != 3 || output.Imports[0].Range.EndLine != 6 || len(output.Imports[0].Children) != 2 {
		t.Fatalf("go imports should expose exact block range with child imports: %#v", output.Imports)
	}
	if len(output.Symbols) == 0 || output.Symbols[0].Kind != "type_block" || output.Symbols[0].Range.StartLine != 8 || output.Symbols[0].Range.EndLine != 11 || len(output.Symbols[0].Children) != 2 {
		t.Fatalf("go type declaration should expose exact block range with child specs: %#v", output.Symbols)
	}
	var sawType, sawFunction, sawMethod bool
	for _, item := range flattenOutlineItems(output.Symbols) {
		switch item.Kind + ":" + item.Name {
		case "type:Service":
			sawType = true
		case "function:New":
			sawFunction = true
		case "method:Service.Run":
			sawMethod = true
		}
	}
	if !sawType || !sawFunction || !sawMethod {
		t.Fatalf("go outline missing expected symbols: %#v", output.Symbols)
	}
	for _, item := range append(flattenOutlineItems(output.Imports), flattenOutlineItems(output.Symbols)...) {
		if item.Confidence != "exact" || item.RangeIsEstimated || item.RangeFingerprint == nil {
			t.Fatalf("go outline item should carry exact trust metadata: %#v", item)
		}
	}
}

func TestOutlineFileGoGroupedSpecDocRangesDoNotUseBlockDoc(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.go")
	content := "package sample\n\n// block doc\ntype (\n\t// service doc\n\tService struct{}\n\tRunner interface{ Run() }\n)\n"
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(output.Symbols) == 0 || len(output.Symbols[0].Children) != 2 {
		t.Fatalf("unexpected go outline: result=%#v output=%#v", result, output)
	}
	if output.Symbols[0].Range.StartLine != 3 {
		t.Fatalf("block range should include block doc: %#v", output.Symbols[0])
	}
	service := output.Symbols[0].Children[0]
	runner := output.Symbols[0].Children[1]
	if service.Range.StartLine != 5 || runner.Range.StartLine != 7 {
		t.Fatalf("child spec ranges should not reuse block doc: service=%#v runner=%#v", service, runner)
	}
}

func TestOutlineFileLineWindowIncludesEnclosingItem(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.go")
	content := "package sample\n\nfunc Long() {\n\tprintln(\"a\")\n\tprintln(\"b\")\n\tprintln(\"c\")\n}\n"
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	window := SourceLineRange{StartLine: 5, EndLine: 5}

	h := NewHandler()
	result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{
		TargetFile: file,
		LineWindow: &window,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(output.Symbols) != 1 || output.Symbols[0].Name != "Long" {
		t.Fatalf("line_window should include enclosing function item: result=%#v output=%#v", result, output)
	}
}

func TestOutlineFileGoThresholdReturnsFingerprintOnlyError(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "large.go")
	if err := os.WriteFile(file, []byte("package sample\nfunc X() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Load()
	cfg.WriteThreshold = 1
	h := NewHandler(WithConfig(cfg))

	result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("expected threshold to be a structured tool error: %#v", output)
	}
	if output.Fingerprint == nil || output.ParserStatus != "outline_parse_threshold_exceeded" || output.NextRecommendedCall == nil {
		t.Fatalf("threshold output should keep fingerprint and action hint: %#v", output)
	}
	if strings.Contains(output.NextRecommendedCall.Reason, "line_window") && !strings.Contains(output.NextRecommendedCall.Reason, "cannot") {
		t.Fatalf("threshold output should not recommend line_window retry: %#v", output.NextRecommendedCall)
	}
}

func TestOutlineFileCwdRecommendationsCarryCwdID(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "many.go"), []byte("package sample\n\nfunc A() {}\nfunc B() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := cwdRegistryTestConfig(t)
	h := NewHandler(WithConfig(cfg))
	cwdID := setCwdWithHandlerForTest(t, h, tempDir)
	maxItems := 1
	result, output, err := h.HandleOutlineFile(context.Background(), nil, OutlineFileInput{
		CwdAwareInput: CwdAwareInput{CwdID: CwdIDInput{Present: true, Value: cwdID}},
		TargetFile:    "many.go",
		MaxItems:      &maxItems,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !output.Truncated || output.NextRecommendedCall == nil {
		t.Fatalf("cwd truncated outline should return continuation hint: result=%#v output=%#v", result, output)
	}
	if got := output.NextRecommendedCall.RecommendedNextInput["cwd_id"]; got != cwdID {
		t.Fatalf("outline continuation recommendation should carry cwd_id, got %#v in %#v", got, output.NextRecommendedCall)
	}
	if got := output.NextRecommendedCall.RecommendedNextInput["target_file"]; got != "many.go" {
		t.Fatalf("outline continuation recommendation should keep relative target_file, got %#v", got)
	}

	thresholdCfg := cwdRegistryTestConfig(t)
	thresholdCfg.WriteThreshold = 1
	thresholdHandler := NewHandler(WithConfig(thresholdCfg))
	thresholdCwdID := setCwdWithHandlerForTest(t, thresholdHandler, tempDir)
	thresholdResult, thresholdOutput, err := thresholdHandler.HandleOutlineFile(context.Background(), nil, OutlineFileInput{
		CwdAwareInput: CwdAwareInput{CwdID: CwdIDInput{Present: true, Value: thresholdCwdID}},
		TargetFile:    "many.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !thresholdResult.IsError || thresholdOutput.NextRecommendedCall == nil {
		t.Fatalf("cwd threshold outline should return fingerprint_only hint: result=%#v output=%#v", thresholdResult, thresholdOutput)
	}
	if got := thresholdOutput.NextRecommendedCall.RecommendedNextInput["cwd_id"]; got != thresholdCwdID {
		t.Fatalf("outline threshold recommendation should carry cwd_id, got %#v in %#v", got, thresholdOutput.NextRecommendedCall)
	}
}

func TestGrepInputJSONDeserializesJSONNativeFlagsAndDashAliases(t *testing.T) {
	var input GrepToolInput
	err := json.Unmarshal([]byte(`{
		"pattern": "needle",
		"before": 2,
		"after": 3,
		"context": 4,
		"case_insensitive": true
	}`), &input)
	if err != nil {
		t.Fatal(err)
	}
	if input.Before != 2 || input.After != 3 || input.Context != 4 || !input.CaseInsensitive {
		t.Fatalf("json-native flags were not deserialized: %#v", input)
	}

	err = json.Unmarshal([]byte(`{"pattern":"needle","-B":1,"-A":2,"-C":3,"-i":true}`), &input)
	if err != nil {
		t.Fatal(err)
	}
	if input.Before != 1 || input.After != 2 || input.Context != 3 || !input.CaseInsensitive {
		t.Fatalf("dash compatibility aliases were not deserialized: %#v", input)
	}
}

func TestGrepInputSchemaUsesJSONNativeFlags(t *testing.T) {
	schema, err := jsonschema.For[GrepToolInput](nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pattern_mode", "before", "after", "context", "case_insensitive", "ignore_globs", "line_window", "max_matches_per_file", "limit"} {
		if _, ok := schema.Properties[name]; !ok {
			t.Fatalf("grep schema is missing public property %q; properties: %#v", name, schema.Properties)
		}
	}
	for _, name := range []string{"-B", "-A", "-C", "-i", "B", "A", "C", "I"} {
		if _, ok := schema.Properties[name]; ok {
			t.Fatalf("grep schema exposes legacy alias property %q; properties: %#v", name, schema.Properties)
		}
	}
}

func TestGrepRequiresExplicitPath(t *testing.T) {
	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Pattern: "needle",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStructuredToolError(t, result, output, "path is required")
	if !strings.Contains(output.Error, "path is required") || !strings.Contains(output.Error, "absolute path") {
		t.Fatalf("grep missing-path error should explain explicit path requirement: %q", output.Error)
	}
}

func TestGrepRejectsRelativePath(t *testing.T) {
	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    ".",
		Pattern: "needle",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStructuredToolError(t, result, output, "absolute")
	if !strings.Contains(output.Error, "relative paths require cwd_id") {
		t.Fatalf("grep relative-path error should explain cwd_id relative mode: %q", output.Error)
	}
}

func TestGrepRejectsDriveRelativeWindowsPathSyntax(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("filepath.IsAbs covers Windows syntax on Windows; this test guards non-Windows path-map syntax")
	}
	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    "C:repo",
		Pattern: "needle",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStructuredToolError(t, result, output, "absolute")
}

func TestGrepRejectsWindowsAbsolutePathSyntaxOnNonWindowsServer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows absolute paths are valid on Windows servers")
	}
	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    "D:/repo",
		Pattern: "needle",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStructuredToolError(t, result, output, "absolute")
}

func TestReadFileRejectsRelativeTargetFile(t *testing.T) {
	h := NewHandler()
	result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{
		TargetFile: "src/main.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStructuredToolError(t, result, output, "absolute")
	if !strings.Contains(output.Error, "relative paths require cwd_id") {
		t.Fatalf("read_file relative-path error should explain cwd_id relative mode: %q", output.Error)
	}
}

func TestReadFileInputSchemaExposesLineRange(t *testing.T) {
	schema, err := jsonschema.For[ReadFileInput](nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"target_file", "start_line", "end_line"} {
		if _, ok := schema.Properties[name]; !ok {
			t.Fatalf("read_file schema is missing property %q; properties: %#v", name, schema.Properties)
		}
	}
	assertSchemaOmits(t, schema, "cursor")
}

func TestReadFileChecksExpectedVersionBeforeEmptyFileSuccess(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(file, []byte("before\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	initialResult, initial, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{
		TargetFile:      file,
		CountTotalLines: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if initialResult.IsError || initial.Coverage == nil || initial.Coverage.Proof == nil || initial.Coverage.Proof.ProofStrength != "exact" {
		t.Fatalf("initial exact read proof missing: result=%#v output=%#v", initialResult, initial)
	}
	if err := os.WriteFile(file, nil, 0644); err != nil {
		t.Fatal(err)
	}

	result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{
		TargetFile:      file,
		ExpectedVersion: initial.Coverage.Proof,
		CountTotalLines: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || output.ErrorCode != "continuation_stale" {
		t.Fatalf("stale expected_version must be rejected before empty-file success: result=%#v output=%#v", result, output)
	}
}

func TestReadFileAllowsExactExpectedVersionForEmptyFile(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "empty.txt")
	if err := os.WriteFile(file, nil, 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	initialResult, initial, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{
		TargetFile:      file,
		CountTotalLines: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if initialResult.IsError || initial.Fingerprint == nil || initial.Fingerprint.SizeBytes != 0 {
		t.Fatalf("empty file should expose a fingerprint when total lines are requested: result=%#v output=%#v", initialResult, initial)
	}
	proof := ReadCoverageProof{
		SizeBytes:        initial.Fingerprint.SizeBytes,
		ModifiedUnixNano: initial.Fingerprint.ModifiedUnixNano,
		SHA256:           initial.Fingerprint.SHA256,
		ProofStrength:    "exact",
	}
	result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{
		TargetFile:      file,
		ExpectedVersion: &proof,
		CountTotalLines: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.TotalLines == nil || *output.TotalLines != 0 {
		t.Fatalf("current empty exact proof should be accepted: result=%#v output=%#v", result, output)
	}

	stale := proof
	stale.SHA256 = strings.Repeat("0", 64)
	staleResult, staleOutput, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{
		TargetFile:      file,
		ExpectedVersion: &stale,
		CountTotalLines: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !staleResult.IsError || staleOutput.ErrorCode != "continuation_stale" {
		t.Fatalf("stale empty exact proof should use continuation_stale semantics: result=%#v output=%#v", staleResult, staleOutput)
	}
}

func TestReadFilesContinuationStartsAfterLastEmittedLine(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(file, []byte("first\nsecond\nthird\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleReadFiles(context.Background(), nil, ReadFilesInput{
		Items: []ReadFileInputItem{
			{TargetFile: file},
		},
		MaxTotalBytes: intPtr(len("1|first\n") + 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !output.Truncated || len(output.Items) != 1 {
		t.Fatalf("read_files should truncate one item cleanly: result=%#v output=%#v", result, output)
	}
	if output.Items[0].Range == nil || output.Items[0].Range.End != 1 || strings.Contains(output.Items[0].Text, "second") {
		t.Fatalf("truncated item should expose only the emitted line range/text: %#v", output.Items[0])
	}
	if output.Items[0].Coverage == nil || output.Items[0].Coverage.Proof == nil || output.Items[0].Coverage.Proof.Range.EndLine != 1 || output.Items[0].Coverage.NextRange == nil || output.Items[0].Coverage.NextRange.StartLine != 2 {
		t.Fatalf("truncated item coverage should match emitted lines: %#v", output.Items[0].Coverage)
	}
	if output.Items[0].Continuation != nil {
		t.Fatalf("truncated read_files item should not keep stale read_file continuation: %#v", output.Items[0].Continuation)
	}
	hint := output.Continuation.NextRecommendedCall
	if hint == nil || hint.RecommendedNextTool != "read_files" {
		t.Fatalf("read_files truncation should recommend continuation: %#v", output.Continuation)
	}
	items, ok := hint.RecommendedNextInput["items"].([]map[string]any)
	if !ok || len(items) != 1 {
		t.Fatalf("read_files continuation should preserve item list shape: %#v", hint.RecommendedNextInput)
	}
	if items[0]["start_line"] != 2 {
		t.Fatalf("read_files continuation should resume after last emitted line, got %#v", items[0])
	}
	if _, ok := items[0]["expected_version"]; !ok {
		t.Fatalf("read_files continuation should carry an expected_version proof: %#v", items[0])
	}
}

func TestReadFilesPromotesInnerChunkContinuation(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(file, []byte("first\nsecond\nthird\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleReadFiles(context.Background(), nil, ReadFilesInput{
		Items: []ReadFileInputItem{
			{TargetFile: file},
		},
		MaxTotalLines: intPtr(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !output.Truncated || output.Continuation == nil || output.Continuation.NextRecommendedCall == nil {
		t.Fatalf("read_files should promote inner read_file chunk continuation: result=%#v output=%#v", result, output)
	}
	if len(output.Items) != 1 || output.Items[0].Continuation != nil {
		t.Fatalf("read_files should not leave stale item-level continuation when top-level continuation is present: %#v", output.Items)
	}
	hint := output.Continuation.NextRecommendedCall
	items, ok := hint.RecommendedNextInput["items"].([]map[string]any)
	if !ok || len(items) != 1 || items[0]["start_line"] != 3 {
		t.Fatalf("promoted continuation should resume the current item at line 3: %#v", hint.RecommendedNextInput)
	}
	proof := readCoverageProofFromRecommendedInputForTest(t, items[0])
	if proof.ProofStrength != "exact" || proof.SHA256 == "" {
		t.Fatalf("promoted continuation should carry exact expected_version proof: %#v", items[0])
	}

	startLine := items[0]["start_line"].(int)
	replayResult, replay, err := h.HandleReadFiles(context.Background(), nil, ReadFilesInput{
		Items: []ReadFileInputItem{
			{TargetFile: file, StartLine: &startLine, ExpectedVersion: &proof},
		},
		MaxTotalLines: intPtr(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if replayResult.IsError || replay.Truncated || len(replay.Items) != 1 || !strings.Contains(replay.Items[0].Text, "3|third") {
		t.Fatalf("promoted continuation replay should read the tail exactly once: result=%#v output=%#v", replayResult, replay)
	}
	if replay.Items[0].Coverage == nil || replay.Items[0].Coverage.CompleteFileRead {
		t.Fatalf("tail replay must not claim whole-file coverage: %#v", replay.Items[0].Coverage)
	}
	if replay.Items[0].Continuation == nil || !replay.Items[0].Continuation.Complete || replay.Items[0].Continuation.NextRecommendedCall != nil {
		t.Fatalf("read_files replay tail item should be terminal-complete with no stale next call: %#v", replay.Items[0].Continuation)
	}
}

func TestReadFilesOversizedFirstLineDoesNotAdvanceContinuation(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "long.txt")
	if err := os.WriteFile(file, []byte(strings.Repeat("x", 120)+"\nsecond\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Load()
	cfg.ReadFilesMaxItemBytes = 40
	cfg.ReadFilesMaxTotalBytes = 40
	h := NewHandler(WithConfig(cfg))

	result, output, err := h.HandleReadFiles(context.Background(), nil, ReadFilesInput{
		Items: []ReadFileInputItem{
			{TargetFile: file},
		},
		MaxTotalBytes: intPtr(40),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !output.Truncated || len(output.Items) != 1 {
		t.Fatalf("read_files should stop on an oversized first line without error: result=%#v output=%#v", result, output)
	}
	item := output.Items[0]
	if item.Range != nil || item.Coverage != nil || strings.TrimSpace(item.Text) != "" {
		t.Fatalf("oversized first line should not claim emitted coverage/text: %#v", item)
	}
	hint := output.Continuation.NextRecommendedCall
	if hint == nil || hint.RecommendedNextTool != "read_file" || hint.RecommendedNextInputPolicy != "read_oversized_line" {
		t.Fatalf("oversized line should recommend direct read_file recovery: %#v", output.Continuation)
	}
	if hint.RecommendedNextInput["start_line"] != 1 || hint.RecommendedNextInput["end_line"] != 1 {
		t.Fatalf("oversized line continuation must not advance past line 1: %#v", hint.RecommendedNextInput)
	}
	expectedVersion, ok := hint.RecommendedNextInput["expected_version"].(map[string]any)
	if !ok || expectedVersion["proof_strength"] != "exact" || expectedVersion["sha256"] == "" {
		t.Fatalf("oversized line continuation should carry an exact public expected_version proof: %#v", hint.RecommendedNextInput)
	}

	startLine := hint.RecommendedNextInput["start_line"].(int)
	endLine := hint.RecommendedNextInput["end_line"].(int)
	proof := ReadCoverageProof{
		SizeBytes:        output.Items[0].Fingerprint.SizeBytes,
		ModifiedUnixNano: output.Items[0].Fingerprint.ModifiedUnixNano,
		SHA256:           output.Items[0].Fingerprint.SHA256,
		ProofStrength:    "exact",
		Range:            SourceLineRange{StartLine: startLine, EndLine: endLine},
	}
	replayResult, replay, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{
		TargetFile:      file,
		StartLine:       &startLine,
		EndLine:         &endLine,
		ExpectedVersion: &proof,
		CountTotalLines: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if replayResult.IsError || !strings.Contains(replay.Text, strings.Repeat("x", 120)) {
		t.Fatalf("oversized read_file replay should succeed with exact proof: result=%#v output=%#v", replayResult, replay)
	}
}

func TestReadFileFinalTailChunkIsTerminalComplete(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(file, []byte("first\nsecond\nthird\n"), 0644); err != nil {
		t.Fatal(err)
	}

	startLine := 3
	h := NewHandler()
	result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{
		TargetFile:      file,
		StartLine:       &startLine,
		CountTotalLines: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.Coverage == nil || output.Coverage.CompleteFileRead {
		t.Fatalf("tail read should not claim whole-file coverage: result=%#v output=%#v", result, output)
	}
	if output.Continuation == nil || !output.Continuation.Complete || output.Continuation.NextRecommendedCall != nil {
		t.Fatalf("tail read should return terminal continuation without EOF+1 next call: %#v", output.Continuation)
	}
}

func readCoverageProofFromRecommendedInputForTest(t *testing.T, item map[string]any) ReadCoverageProof {
	t.Helper()
	value, ok := item["expected_version"].(map[string]any)
	if !ok {
		t.Fatalf("recommended input should contain expected_version map: %#v", item)
	}
	proof := ReadCoverageProof{
		SizeBytes:        int64FromAnyForTest(t, value["size_bytes"]),
		ModifiedUnixNano: int64FromAnyForTest(t, value["modified_unix_nano"]),
		ProofStrength:    stringFromAnyForTest(t, value["proof_strength"]),
	}
	if sha, ok := value["sha256"].(string); ok {
		proof.SHA256 = sha
	}
	if rawRange, ok := value["range"].(map[string]any); ok {
		proof.Range = SourceLineRange{
			StartLine: intFromAnyForTest(t, rawRange["start_line"]),
			EndLine:   intFromAnyForTest(t, rawRange["end_line"]),
		}
	}
	return proof
}

func intFromAnyForTest(t *testing.T, value any) int {
	t.Helper()
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		t.Fatalf("expected integer-like value, got %#v", value)
		return 0
	}
}

func int64FromAnyForTest(t *testing.T, value any) int64 {
	t.Helper()
	switch v := value.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	default:
		t.Fatalf("expected int64-like value, got %#v", value)
		return 0
	}
}

func stringFromAnyForTest(t *testing.T, value any) string {
	t.Helper()
	if text, ok := value.(string); ok {
		return text
	}
	t.Fatalf("expected string value, got %#v", value)
	return ""
}

func TestReadFilesDefaultKeepsVisibleSecretLikeContentLiteral(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "settings.txt")
	if err := os.WriteFile(file, []byte("PASSWORD=visible-secret\nplain=ok\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleReadFiles(context.Background(), nil, ReadFilesInput{
		Items: []ReadFileInputItem{
			{TargetFile: file},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(output.Items) != 1 {
		t.Fatalf("read_files visible secret fixture should succeed: result=%#v output=%#v", result, output)
	}
	item := output.Items[0]
	if item.Redacted || output.RedactionMode != redactionOff || item.RedactionMode != redactionOff || !strings.Contains(item.Text, "PASSWORD=visible-secret") {
		t.Fatalf("read_files default should keep literal content: output=%#v item=%#v", output, item)
	}
}

func TestReadFilesStrictRedactsNestedHiddenPathContent(t *testing.T) {
	tempDir := t.TempDir()
	workflowDir := filepath.Join(tempDir, ".github", "workflows")
	if err := os.MkdirAll(workflowDir, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(workflowDir, "deploy.yml")
	if err := os.WriteFile(file, []byte("deploy_token=secret-value\nsafe=ok\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleReadFiles(context.Background(), nil, ReadFilesInput{
		RedactionMode: redactionStrict,
		Items: []ReadFileInputItem{
			{TargetFile: file},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(output.Items) != 1 {
		t.Fatalf("read_files hidden path should succeed with redaction: result=%#v output=%#v", result, output)
	}
	item := output.Items[0]
	if !item.Redacted || strings.Contains(item.Text, "secret-value") || !strings.Contains(item.Text, "deploy_token=[REDACTED]") {
		t.Fatalf("nested hidden path content should be auto-redacted: %#v", item)
	}
}

func TestReadFilesStrictDoesNotRedactBenignTokenCounters(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "config.json")
	content := "module=github.com/acme/project\nmax_output_tokens=4096\n"
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleReadFiles(context.Background(), nil, ReadFilesInput{
		RedactionMode: redactionStrict,
		Items: []ReadFileInputItem{
			{TargetFile: file},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(output.Items) != 1 {
		t.Fatalf("read_files benign config fixture should succeed: result=%#v output=%#v", result, output)
	}
	item := output.Items[0]
	if item.Redacted || !strings.Contains(item.Text, "github.com/acme/project") || !strings.Contains(item.Text, "max_output_tokens=4096") {
		t.Fatalf("strict redaction should preserve benign module paths and token counters: %#v", item)
	}
}

func TestStrictRedactionDoesNotRedactBenignLongIdentifiersOrKeys(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "config.txt")
	identifier := "BuildArtifactIdentifierAlphaBetaGammaDelta1234567890"
	keyName := "feature_toggle_key_name_that_is_long_but_not_secret_1234567890"
	content := identifier + "\n" + keyName + "=enabled\n"
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	readResult, readOutput, err := h.HandleReadFiles(context.Background(), nil, ReadFilesInput{
		RedactionMode: redactionStrict,
		Items:         []ReadFileInputItem{{TargetFile: file}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if readResult.IsError || len(readOutput.Items) != 1 {
		t.Fatalf("read_files benign identifier fixture should succeed: result=%#v output=%#v", readResult, readOutput)
	}
	if readOutput.Items[0].Redacted || !strings.Contains(readOutput.Items[0].Text, identifier) || !strings.Contains(readOutput.Items[0].Text, keyName) {
		t.Fatalf("strict read_files should preserve benign identifiers and key names: %#v", readOutput.Items[0])
	}

	grepResult, grepOutput, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:          file,
		Pattern:       identifier,
		PatternMode:   "literal",
		RedactionMode: redactionStrict,
	})
	if err != nil {
		t.Fatal(err)
	}
	if grepResult.IsError || len(grepOutput.Matches) == 0 || grepOutput.Matches[0].Redacted || !strings.Contains(grepOutput.Matches[0].Text, identifier) {
		t.Fatalf("strict grep should preserve benign identifiers: result=%#v output=%#v", grepResult, grepOutput)
	}
}

func TestToolSchemasDoNotExposeCursorPagination(t *testing.T) {
	inputSchemas := []struct {
		name   string
		schema *jsonschema.Schema
	}{
		{"read_file", schemaForTest[ReadFileInput](t)},
		{"read_files", schemaForTest[ReadFilesInput](t)},
		{"list_dir", schemaForTest[ListDirInput](t)},
		{"glob_file_search", schemaForTest[GlobFileSearchInput](t)},
		{"grep", schemaForTest[GrepToolInput](t)},
		{"inspect_path", schemaForTest[InspectPathInput](t)},
		{"workspace_inventory", schemaForTest[WorkspaceInventoryInput](t)},
		{"outline_file", schemaForTest[OutlineFileInput](t)},
		{"resolve_symbol_range", schemaForTest[ResolveSymbolRangeInput](t)},
		{"copy_ranges", schemaForTest[CopyRangesInput](t)},
		{"move_ranges", schemaForTest[MoveRangesInput](t)},
		{"copy_ranges_batch", schemaForTest[CopyRangesBatchInput](t)},
		{"move_ranges_batch", schemaForTest[MoveRangesBatchInput](t)},
	}
	for _, tt := range inputSchemas {
		t.Run(tt.name, func(t *testing.T) {
			assertSchemaOmits(t, tt.schema, "cursor")
		})
	}

	outputSchemas := []struct {
		name   string
		schema *jsonschema.Schema
	}{
		{"read_file", schemaForTest[ReadFileOutput](t)},
		{"read_files", schemaForTest[ReadFilesOutput](t)},
		{"list_dir", schemaForTest[ListDirOutput](t)},
		{"glob_file_search", schemaForTest[GlobFileSearchOutput](t)},
		{"grep", schemaForTest[GrepOutput](t)},
		{"inspect_path", schemaForTest[InspectPathOutput](t)},
		{"workspace_inventory", WorkspaceInventoryOutputSchema()},
		{"outline_file", OutlineFileOutputSchema()},
		{"resolve_symbol_range", schemaForTest[ResolveSymbolRangeOutput](t)},
		{"copy_ranges", schemaForTest[CopyRangesOutput](t)},
		{"move_ranges", schemaForTest[MoveRangesOutput](t)},
		{"copy_ranges_batch", schemaForTest[CopyRangesBatchOutput](t)},
		{"move_ranges_batch", schemaForTest[MoveRangesBatchOutput](t)},
	}
	for _, tt := range outputSchemas {
		t.Run(tt.name+"_output", func(t *testing.T) {
			assertSchemaOmits(t, tt.schema, "nextCursor")
			if tt.name != "read_file" && tt.name != "read_files" {
				assertSchemaOmits(t, tt.schema, "text")
			}
		})
	}
}

func TestToolOutputSchemasExposeToolSpecificFields(t *testing.T) {
	readSchema := schemaForTest[ReadFileOutput](t)
	for _, name := range []string{"text", "file", "total_lines", "total_lines_known", "requested_range", "range"} {
		assertSchemaHas(t, readSchema, name)
	}
	assertSchemaOmits(t, readSchema, "lines")

	readFilesSchema := schemaForTest[ReadFilesOutput](t)
	for _, name := range []string{"items", "max_total_lines", "max_total_bytes", "count", "truncated", "continuation", "redaction_mode"} {
		assertSchemaHas(t, readFilesSchema, name)
	}

	listSchema := schemaForTest[ListDirOutput](t)
	for _, name := range []string{"directory", "count", "dot_entries_skipped", "entries", "message"} {
		assertSchemaHas(t, listSchema, name)
	}

	globSchema := schemaForTest[GlobFileSearchOutput](t)
	for _, name := range []string{"pattern", "target_directory", "limit", "count", "total_match_count", "truncated", "dot_entries_skipped", "files", "next_recommended_call", "next_recommended_calls", "message"} {
		assertSchemaHas(t, globSchema, name)
	}

	grepSchema := schemaForTest[GrepOutput](t)
	for _, name := range []string{"pattern", "pattern_mode", "path", "output_mode", "context_before", "context_after", "case_insensitive", "line_window", "limit", "truncated", "dot_entries_skipped", "matches", "files", "counts", "search_stats", "file_groups", "next_recommended_call", "next_recommended_calls", "message"} {
		assertSchemaHas(t, grepSchema, name)
	}
	ApplyPathOutputSchemaConstraints(grepSchema)
	assertSchemaRejectsEmptyPath(t, grepSchema, "path")
	assertSchemaRejectsEmptyPathItem(t, grepSchema, "files")
	fileGroupPathSchema := findFirstSchemaProperty(grepSchema.Properties["file_groups"], "path")
	if fileGroupPathSchema == nil || fileGroupPathSchema.MinLength == nil {
		t.Fatalf("grep file_groups[].path should be path-constrained: %#v", grepSchema.Properties["file_groups"])
	}

	inspectSchema := schemaForTest[InspectPathOutput](t)
	for _, name := range []string{"path", "resolved_path", "exists", "kind", "size_bytes", "line_count", "modified_at", "mode", "is_readable", "is_binary", "symlink_target", "direct_file_count", "direct_dir_count"} {
		assertSchemaHas(t, inspectSchema, name)
	}
	ApplyPathOutputSchemaConstraints(inspectSchema)
	assertSchemaRejectsEmptyPath(t, inspectSchema, "symlink_target")

	inventorySchema := WorkspaceInventoryOutputSchema()
	for _, name := range []string{"root", "directories_page", "summary", "continuation", "max_depth", "limit", "directory_count", "ignored_directory_count", "include_hidden", "include_vcs_metadata", "dot_entries_skipped", "hidden_entries_included", "vcs_entries_skipped", "vcs_entries_included", "truncated", "truncation_reason", "max_depth_reached", "next_recommended_call", "next_recommended_calls"} {
		assertSchemaHas(t, inventorySchema, name)
	}
	rootSchema := inventorySchema.Properties["root"]
	if rootSchema == nil || rootSchema.Ref == "" {
		t.Fatalf("workspace_inventory root schema should reference directory node definition: %#v", rootSchema)
	}
	nodeSchema := inventorySchema.Defs["workspace_directory_node"]
	if nodeSchema == nil || nodeSchema.Properties["directories"] == nil || nodeSchema.Properties["directories"].Items == nil || nodeSchema.Properties["directories"].Items.Ref == "" {
		t.Fatalf("workspace_inventory directories schema should recursively reference directory node: %#v", nodeSchema)
	}
	if inventorySchema.Defs["workspace_directory_page_entry"] == nil || inventorySchema.Defs["workspace_summary"] == nil || inventorySchema.Defs["continuation_hint"] == nil {
		t.Fatalf("workspace_inventory schema should expose Phase 5 page/summary/continuation defs: %#v", inventorySchema.Defs)
	}
	summarySchema := inventorySchema.Defs["workspace_summary"]
	for _, name := range []string{"summary_coverage_complete", "tree_scan_complete", "summary_incomplete_reason", "scan_scope"} {
		if summarySchema.Properties[name] == nil {
			t.Fatalf("workspace summary schema should expose Phase 8 field %q: %#v", name, summarySchema.Properties)
		}
	}
	continuationSchema := inventorySchema.Defs["continuation_hint"]
	if continuationSchema.Properties["page_complete"] == nil {
		t.Fatalf("workspace continuation schema should expose page_complete: %#v", continuationSchema.Properties)
	}

	copyOutputSchema := schemaForTest[CopyRangesOutput](t)
	for _, name := range []string{"newline_bytes", "source_boundaries", "target_boundary", "target_boundaries", "visual_blank_lines_between", "warning_codes"} {
		if findFirstSchemaProperty(copyOutputSchema, name) == nil {
			t.Fatalf("copy_ranges output schema should expose joiner diagnostic field %q: %#v", name, copyOutputSchema)
		}
	}

	outlineSchema := OutlineFileOutputSchema()
	for _, name := range []string{"file", "language", "parser_status", "fingerprint", "imports", "symbols", "sections", "enclosing_items", "outline_stats", "truncated", "warnings", "next_recommended_call"} {
		assertSchemaHas(t, outlineSchema, name)
	}
	if outlineSchema.Defs["outline_stats"] == nil || outlineSchema.Defs["outline_stats"].Properties["omitted_leaf_items"] == nil {
		t.Fatalf("outline_stats schema should expose omitted_leaf_items: %#v", outlineSchema.Defs["outline_stats"])
	}
	ApplyPathOutputSchemaConstraints(outlineSchema)
	assertSchemaRejectsEmptyPath(t, outlineSchema, "file")
	outlineItemSchema := outlineSchema.Defs["outline_item"]
	if outlineItemSchema == nil || outlineItemSchema.Properties["path"] == nil || outlineItemSchema.Properties["path"].Items == nil {
		t.Fatalf("outline item path schema is missing: %#v", outlineItemSchema)
	}
	assertSchemaDoesNotConstrainAsFilesystemPath(t, outlineItemSchema.Properties["path"].Items, "outline_item.path[]")
	for _, name := range []string{"enclosing_path", "byte_range", "selector", "symbol_ref", "whole_line_range", "write_safe", "refusal_reason"} {
		if outlineItemSchema.Properties[name] == nil {
			t.Fatalf("outline item schema should include Phase 6 field %q: %#v", name, outlineItemSchema.Properties)
		}
	}
	byteRangeSchema := outlineSchema.Defs["source_byte_range"]
	selectorSchema := outlineSchema.Defs["outline_selector"]
	if byteRangeSchema == nil || selectorSchema == nil {
		t.Fatalf("outline schema should expose Phase 6 selector defs: %#v", outlineSchema.Defs)
	}
	for _, name := range []string{"start_byte", "end_byte_exclusive"} {
		if byteRangeSchema.Properties[name] == nil {
			t.Fatalf("source_byte_range schema should include %q: %#v", name, byteRangeSchema.Properties)
		}
	}
	for _, name := range []string{"whole_line_range", "write_safe", "range_fingerprint"} {
		if selectorSchema.Properties[name] == nil {
			t.Fatalf("outline_selector schema should include %q: %#v", name, selectorSchema.Properties)
		}
	}

	resolveSchema := schemaForTest[ResolveSymbolRangeOutput](t)
	for _, name := range []string{"file", "language", "parser_status", "fingerprint", "matches", "resolved_ranges", "ambiguous", "resolution_status", "next_recommended_call", "next_recommended_calls", "write_recommendation_status", "write_refusal_code", "target_syntax_status", "target_syntax_proof", "recommended_write_call", "preview_write_call"} {
		assertSchemaHas(t, resolveSchema, name)
	}
	ApplyPathOutputSchemaConstraints(resolveSchema)
	assertSchemaRejectsEmptyPath(t, resolveSchema, "file")
	resolveRecommendedInputSchema := findFirstSchemaProperty(resolveSchema, "recommended_next_input")
	if resolveRecommendedInputSchema == nil || resolveRecommendedInputSchema.Properties["target_file"] == nil || resolveRecommendedInputSchema.Properties["source_file"] == nil {
		t.Fatalf("resolve_symbol_range write hints should expose constrained recommended_next_input path fields: %#v", resolveRecommendedInputSchema)
	}
	assertSchemaRejectsEmptyPath(t, resolveRecommendedInputSchema, "source_file")
	assertSchemaRejectsEmptyPath(t, resolveRecommendedInputSchema, "target_file")

	copySchema := schemaForTest[CopyRangesOutput](t)
	for _, name := range []string{"operation", "dry_run", "applied", "source_file", "target_file", "ranges", "target_placement", "source_fingerprint_for_next_write", "target_fingerprint_for_next_write", "boundary_warnings", "warnings", "backup_paths", "backup_results", "partial_state"} {
		assertSchemaHas(t, copySchema, name)
	}
	ApplyPathOutputSchemaConstraints(copySchema)
	assertSchemaRejectsEmptyPath(t, copySchema, "source_file")
	assertSchemaRejectsEmptyPath(t, copySchema, "target_file")
	assertSchemaRejectsEmptyPathItem(t, copySchema, "backup_paths")
	recommendedInputSchema := findFirstSchemaProperty(copySchema, "recommended_next_input")
	if recommendedInputSchema == nil || recommendedInputSchema.Properties["target_file"] == nil {
		t.Fatalf("recommended_next_input should expose constrained recovery path fields: %#v", recommendedInputSchema)
	}
	assertSchemaRejectsEmptyPath(t, recommendedInputSchema, "target_file")
	assertSchemaOmits(t, copySchema, "sections")

	batchSchema := schemaForTest[CopyRangesBatchOutput](t)
	for _, name := range []string{"operation", "dry_run", "applied", "source_file", "target_results", "targets_written", "batch_warnings", "warnings", "warnings_truncated", "warning_summary", "backup_paths", "backup_results", "partial_state"} {
		assertSchemaHas(t, batchSchema, name)
	}
	ApplyPathOutputSchemaConstraints(batchSchema)
	assertSchemaRejectsEmptyPath(t, batchSchema, "source_file")
	assertSchemaRejectsEmptyPathItem(t, batchSchema, "targets_written")
	assertSchemaRejectsEmptyPathItem(t, batchSchema, "backup_paths")
	batchRecommendedInputSchema := findFirstSchemaProperty(batchSchema, "recommended_next_input")
	if batchRecommendedInputSchema == nil {
		t.Fatalf("batch output should expose constrained recommended_next_input schema: %#v", batchSchema)
	}
	assertNestedArrayObjectRejectsEmptyPath(t, batchRecommendedInputSchema, "targets", "target_file")
	assertSchemaOmits(t, batchSchema, "target_file")
}

func TestSanitizeRecommendedInputProjectsNestedBatchTargets(t *testing.T) {
	root := filepath.Clean(t.TempDir())
	pathCtx := PathContext{
		HasCwd: true,
		CwdID:  1,
		CwdAbs: root,
		CwdOut: filepath.ToSlash(root),
	}
	target := filepath.Join(root, "nested", "target.txt")
	input := map[string]any{
		"targets": []any{
			map[string]any{
				"target_file": target,
			},
		},
	}

	sanitizeRecommendedInput(pathCtx, input)

	targets, ok := input["targets"].([]any)
	if !ok || len(targets) != 1 {
		t.Fatalf("recommended input targets changed shape: %#v", input)
	}
	firstTarget, ok := targets[0].(map[string]any)
	if !ok {
		t.Fatalf("recommended input target changed type: %#v", targets[0])
	}
	if firstTarget["target_file"] != "nested/target.txt" {
		t.Fatalf("nested target_file should be cwd-relative, got %#v", firstTarget["target_file"])
	}
}

func TestCwdReplayHelperAcceptsResolveSymbolRange(t *testing.T) {
	if !toolAcceptsCwdID("resolve_symbol_range") {
		t.Fatal("resolve_symbol_range should accept cwd_id in replay hints")
	}
}

func TestToolOutputsMarshalEmptyCollections(t *testing.T) {
	cases := []struct {
		name   string
		output any
		fields []string
	}{
		{
			name:   "list_dir_success",
			output: ListDirOutput{Entries: []ListDirEntry{}},
			fields: []string{"entries"},
		},
		{
			name:   "glob_file_search_success",
			output: GlobFileSearchOutput{Files: []GlobFileMatch{}},
			fields: []string{"files"},
		},
		{
			name:   "grep_success",
			output: GrepOutput{Matches: []GrepMatch{}, Files: []string{}, Counts: []GrepCount{}, FileGroups: []GrepFileGroup{}},
			fields: []string{"matches", "files", "counts", "file_groups"},
		},
		{
			name:   "list_dir_error",
			output: StructuredErrorOutput[ListDirOutput]("boom"),
			fields: []string{"entries"},
		},
		{
			name:   "glob_file_search_error",
			output: StructuredErrorOutput[GlobFileSearchOutput]("boom"),
			fields: []string{"files"},
		},
		{
			name:   "grep_error",
			output: StructuredErrorOutput[GrepOutput]("boom"),
			fields: []string{"matches", "files", "counts", "file_groups"},
		},
		{
			name:   "outline_file_error",
			output: StructuredErrorOutput[OutlineFileOutput]("boom"),
			fields: []string{"imports", "symbols", "sections", "warnings"},
		},
		{
			name:   "resolve_symbol_range_error",
			output: StructuredErrorOutput[ResolveSymbolRangeOutput]("boom"),
			fields: []string{"matches", "resolved_ranges"},
		},
		{
			name:   "copy_ranges_error",
			output: StructuredErrorOutput[CopyRangesOutput]("boom"),
			fields: []string{"ranges", "boundary_warnings", "warnings", "backup_paths", "backup_results"},
		},
		{
			name:   "move_ranges_error",
			output: StructuredErrorOutput[MoveRangesOutput]("boom"),
			fields: []string{"ranges", "boundary_warnings", "warnings", "backup_paths", "backup_results"},
		},
		{
			name:   "copy_ranges_batch_error",
			output: StructuredErrorOutput[CopyRangesBatchOutput]("boom"),
			fields: []string{"target_results", "targets_written", "batch_warnings", "warnings", "backup_paths", "backup_results"},
		},
		{
			name:   "move_ranges_batch_error",
			output: StructuredErrorOutput[MoveRangesBatchOutput]("boom"),
			fields: []string{"target_results", "targets_written", "batch_warnings", "warnings", "backup_paths", "backup_results"},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			for _, field := range tt.fields {
				assertJSONEmptyArrayField(t, tt.output, field)
			}
		})
	}
}

func TestPathInputSchemasMarkRequiredFields(t *testing.T) {
	readSchema := schemaForTest[ReadFileInput](t)
	assertSchemaRequires(t, readSchema, "target_file")
	ApplyPathInputSchemaConstraints(readSchema)
	assertSchemaRejectsEmptyPath(t, readSchema, "target_file")

	listSchema := schemaForTest[ListDirInput](t)
	assertSchemaRequires(t, listSchema, "target_directory")
	ApplyPathInputSchemaConstraints(listSchema)
	assertSchemaRejectsEmptyPath(t, listSchema, "target_directory")

	globSchema := schemaForTest[GlobFileSearchInput](t)
	assertSchemaRequires(t, globSchema, "glob_pattern")
	assertSchemaRequires(t, globSchema, "target_directory")
	ApplyPathInputSchemaConstraints(globSchema)
	assertSchemaRejectsEmptyPath(t, globSchema, "target_directory")

	grepSchema := schemaForTest[GrepToolInput](t)
	assertSchemaRequires(t, grepSchema, "pattern")
	assertSchemaRequires(t, grepSchema, "path")
	ApplyToolInputSchemaConstraints(grepSchema, "grep")
	assertSchemaRejectsEmptyPath(t, grepSchema, "path")
	if got := grepSchema.Properties["pattern_mode"].Enum; len(got) != 2 || got[0] != "regex" || got[1] != "literal" {
		t.Fatalf("grep pattern_mode schema should expose regex/literal enum: %#v", grepSchema.Properties["pattern_mode"])
	}
	assertSchemaEnum(t, grepSchema.Properties["output_mode"], "content", "files_with_matches", "count")
	if minimum := grepSchema.Properties["max_matches_per_file"].Minimum; minimum == nil || *minimum != 1 {
		t.Fatalf("grep max_matches_per_file schema should require positive values: %#v", grepSchema.Properties["max_matches_per_file"])
	}
	lineWindow := grepSchema.Properties["line_window"]
	if lineWindow == nil || lineWindow.Properties["start_line"] == nil || lineWindow.Properties["end_line"] == nil {
		t.Fatalf("grep line_window schema should expose start_line/end_line: %#v", lineWindow)
	}
	if minimum := lineWindow.Properties["start_line"].Minimum; minimum == nil || *minimum != 1 {
		t.Fatalf("grep line_window.start_line schema should require positive values: %#v", lineWindow.Properties["start_line"])
	}
	if minimum := lineWindow.Properties["end_line"].Minimum; minimum == nil || *minimum != 1 {
		t.Fatalf("grep line_window.end_line schema should require positive values: %#v", lineWindow.Properties["end_line"])
	}

	inspectSchema := schemaForTest[InspectPathInput](t)
	assertSchemaRequires(t, inspectSchema, "target_path")
	ApplyPathInputSchemaConstraints(inspectSchema)
	assertSchemaRejectsEmptyPath(t, inspectSchema, "target_path")

	inventorySchema := schemaForTest[WorkspaceInventoryInput](t)
	assertSchemaRequires(t, inventorySchema, "target_directory")
	ApplyPathInputSchemaConstraints(inventorySchema)
	assertSchemaRejectsEmptyPath(t, inventorySchema, "target_directory")
	assertSchemaEnum(t, inventorySchema.Properties["summary_profile"], "compact", "none", "extended")

	outlineSchema := schemaForTest[OutlineFileInput](t)
	assertSchemaRequires(t, outlineSchema, "target_file")
	ApplyPathInputSchemaConstraints(outlineSchema)
	assertSchemaRejectsEmptyPath(t, outlineSchema, "target_file")

	resolveSchema := schemaForTest[ResolveSymbolRangeInput](t)
	assertSchemaRequires(t, resolveSchema, "source_file")
	assertSchemaRequires(t, resolveSchema, "selector")
	assertSchemaRequires(t, resolveSchema, "source_fingerprint")
	ApplyToolInputSchemaConstraints(resolveSchema, "resolve_symbol_range")
	assertSchemaRejectsEmptyPath(t, resolveSchema, "source_file")
	targetIntentFileSchema := findFirstSchemaProperty(resolveSchema.Properties["target_intent"], "target_file")
	if targetIntentFileSchema == nil || targetIntentFileSchema.MinLength == nil {
		t.Fatalf("resolve_symbol_range target_intent.target_file should be path-constrained: %#v", resolveSchema.Properties["target_intent"])
	}

	copySchema := schemaForTest[CopyRangesInput](t)
	assertSchemaRequires(t, copySchema, "source_file")
	assertSchemaRequires(t, copySchema, "target_file")
	ApplyToolInputSchemaConstraints(copySchema, "copy_ranges")
	assertSchemaRejectsEmptyPath(t, copySchema, "source_file")
	assertSchemaRejectsEmptyPath(t, copySchema, "target_file")
	assertSchemaEnum(t, copySchema.Properties["joiner"], "none", "single_newline", "blank_line")
	assertSchemaEnum(t, copySchema.Properties["redaction_mode"], "off", "strict", "auto")
	assertSchemaEnum(t, findFirstSchemaProperty(copySchema.Properties["placement"], "mode"), "create_new", "append", "prepend", "insert_before_line", "replace_range")
	assertSchemaEnum(t, findFirstSchemaProperty(copySchema.Properties["backup"], "mode"), "none", "sidecar")

	batchSchema := schemaForTest[CopyRangesBatchInput](t)
	assertSchemaRequires(t, batchSchema, "source_file")
	assertSchemaRequires(t, batchSchema, "targets")
	ApplyToolInputSchemaConstraints(batchSchema, "copy_ranges_batch")
	assertSchemaRejectsEmptyPath(t, batchSchema, "source_file")
	batchTargetSchema := batchSchema.Properties["targets"].Items
	assertSchemaEnum(t, batchSchema.Properties["redaction_mode"], "off", "strict", "auto")
	assertSchemaEnum(t, batchTargetSchema.Properties["joiner"], "none", "single_newline", "blank_line")
	assertSchemaEnum(t, batchTargetSchema.Properties["redaction_mode"], "off", "strict", "auto")
	assertSchemaEnum(t, batchTargetSchema.Properties["placement"].Properties["mode"], "create_new", "append", "prepend", "insert_before_line", "replace_range")
	assertSchemaEnum(t, batchTargetSchema.Properties["backup"].Properties["mode"], "none", "sidecar")

	readFilesSchema := schemaForTest[ReadFilesInput](t)
	assertSchemaRequires(t, readFilesSchema, "items")
	ApplyToolInputSchemaConstraints(readFilesSchema, "read_files")
	assertSchemaEnum(t, readFilesSchema.Properties["redaction_mode"], "off", "strict", "auto")

	outlineSchema = schemaForTest[OutlineFileInput](t)
	ApplyToolInputSchemaConstraints(outlineSchema, "outline_file")
	assertSchemaEnum(t, outlineSchema.Properties["output_profile"], "agent", "full", "fingerprint_only", "outline")

	globSchema = schemaForTest[GlobFileSearchInput](t)
	ApplyToolInputSchemaConstraints(globSchema, "glob_file_search")
	assertSchemaEnum(t, globSchema.Properties["sort"], "modified_desc", "modified_asc", "path_asc", "path_desc", "size_desc", "size_asc", "directory_path_asc")
}

func schemaForTest[T any](t *testing.T) *jsonschema.Schema {
	t.Helper()
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func assertSchemaRequires(t *testing.T, schema *jsonschema.Schema, name string) {
	t.Helper()
	for _, required := range schema.Required {
		if required == name {
			return
		}
	}
	t.Fatalf("schema does not mark %q as required; required=%v", name, schema.Required)
}

func assertSchemaOmits(t *testing.T, schema *jsonschema.Schema, name string) {
	t.Helper()
	if _, ok := schema.Properties[name]; ok {
		t.Fatalf("schema should not expose %q after pagination removal; properties: %#v", name, schema.Properties)
	}
}

func assertSchemaEnum(t *testing.T, schema *jsonschema.Schema, values ...string) {
	t.Helper()
	if schema == nil {
		t.Fatalf("schema enum target is missing; want %v", values)
	}
	if len(schema.Enum) != len(values) {
		t.Fatalf("schema enum = %#v, want %v", schema.Enum, values)
	}
	for i, value := range values {
		if schema.Enum[i] != value {
			t.Fatalf("schema enum = %#v, want %v", schema.Enum, values)
		}
	}
}

func assertSchemaRejectsEmptyPath(t *testing.T, schema *jsonschema.Schema, name string) {
	t.Helper()
	property := schema.Properties[name]
	if property == nil {
		t.Fatalf("schema property %q is missing", name)
	}
	if property.MinLength == nil || *property.MinLength != 1 {
		t.Fatalf("schema property %q must reject empty path strings: %#v", name, property)
	}
	if property.Description == "" {
		t.Fatalf("schema property %q must document path mode: %#v", name, property)
	}
}

func assertSchemaRejectsEmptyPathItem(t *testing.T, schema *jsonschema.Schema, name string) {
	t.Helper()
	property := schema.Properties[name]
	if property == nil || property.Items == nil {
		t.Fatalf("schema array property %q is missing items schema: %#v", name, property)
	}
	if property.Items.MinLength == nil || *property.Items.MinLength != 1 {
		t.Fatalf("schema array property %q items must reject empty path strings: %#v", name, property.Items)
	}
	if property.Items.Description == "" {
		t.Fatalf("schema array property %q items must document path mode: %#v", name, property.Items)
	}
}

func assertNestedArrayObjectRejectsEmptyPath(t *testing.T, schema *jsonschema.Schema, arrayName, propertyName string) {
	t.Helper()
	arrayProperty := schema.Properties[arrayName]
	if arrayProperty == nil || arrayProperty.Items == nil {
		t.Fatalf("schema array property %q is missing items schema: %#v", arrayName, arrayProperty)
	}
	property := arrayProperty.Items.Properties[propertyName]
	if property == nil {
		t.Fatalf("schema property %q[].%s is missing: %#v", arrayName, propertyName, arrayProperty.Items.Properties)
	}
	if property.MinLength == nil || *property.MinLength != 1 {
		t.Fatalf("schema property %q[].%s must reject empty path strings: %#v", arrayName, propertyName, property)
	}
	if property.Description == "" {
		t.Fatalf("schema property %q[].%s must document path mode: %#v", arrayName, propertyName, property)
	}
}

func assertSchemaDoesNotConstrainAsFilesystemPath(t *testing.T, schema *jsonschema.Schema, name string) {
	t.Helper()
	if schema.MinLength != nil {
		t.Fatalf("schema property %q should not reject non-empty breadcrumb strings as filesystem paths: %#v", name, schema)
	}
	if schema.Pattern != "" {
		t.Fatalf("schema property %q should not document filesystem path shape: %#v", name, schema)
	}
}

func findFirstSchemaProperty(schema *jsonschema.Schema, name string) *jsonschema.Schema {
	return findFirstSchemaPropertySeen(schema, name, map[*jsonschema.Schema]bool{})
}

func findFirstSchemaPropertySeen(schema *jsonschema.Schema, name string, seen map[*jsonschema.Schema]bool) *jsonschema.Schema {
	if schema == nil || seen[schema] {
		return nil
	}
	seen[schema] = true
	if property := schema.Properties[name]; property != nil {
		return property
	}
	for _, property := range schema.Properties {
		if found := findFirstSchemaPropertySeen(property, name, seen); found != nil {
			return found
		}
	}
	for _, definition := range schema.Defs {
		if found := findFirstSchemaPropertySeen(definition, name, seen); found != nil {
			return found
		}
	}
	for _, definition := range schema.Definitions {
		if found := findFirstSchemaPropertySeen(definition, name, seen); found != nil {
			return found
		}
	}
	if found := findFirstSchemaPropertySeen(schema.Items, name, seen); found != nil {
		return found
	}
	if found := findFirstSchemaPropertySeen(schema.AdditionalProperties, name, seen); found != nil {
		return found
	}
	return nil
}

func assertPathSchemaMatchesServerOS(t *testing.T, name, patternText string) {
	t.Helper()
	pattern, err := regexp.Compile(patternText)
	if err != nil {
		t.Fatalf("schema property %q has invalid pattern %q: %v", name, patternText, err)
	}
	if runtime.GOOS == "windows" {
		if !pattern.MatchString(`D:\repo`) || pattern.MatchString(`/repo`) {
			t.Fatalf("windows path schema for %q must accept Windows absolute paths and reject POSIX roots: %q", name, patternText)
		}
		return
	}
	if !pattern.MatchString(`/repo`) || pattern.MatchString(`D:\repo`) {
		t.Fatalf("posix path schema for %q must accept POSIX absolute paths and reject Windows drive paths: %q", name, patternText)
	}
}

func assertSchemaHas(t *testing.T, schema *jsonschema.Schema, name string) {
	t.Helper()
	if _, ok := schema.Properties[name]; !ok {
		t.Fatalf("schema should expose %q; properties: %#v", name, schema.Properties)
	}
}

func assertJSONEmptyArrayField(t *testing.T, value any, field string) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	item, ok := raw[field]
	if !ok {
		t.Fatalf("marshaled JSON should expose %q: %s", field, data)
	}
	if string(item) != "[]" {
		t.Fatalf("marshaled JSON field %q should be [], got %s in %s", field, item, data)
	}
}

func findInventoryDirectory(nodes []WorkspaceDirectoryNode, name string) *WorkspaceDirectoryNode {
	for i := range nodes {
		if nodes[i].Name == name {
			return &nodes[i]
		}
	}
	return nil
}

func TestListDirRequiresTargetDirectory(t *testing.T) {
	h := NewHandler()
	result, output, err := h.HandleListDir(context.Background(), nil, ListDirInput{})
	if err != nil {
		t.Fatal(err)
	}
	assertStructuredToolError(t, result, output, "target_directory is required")
}

func TestListDirRejectsRelativeTargetDirectory(t *testing.T) {
	h := NewHandler()
	result, output, err := h.HandleListDir(context.Background(), nil, ListDirInput{
		TargetDirectory: ".",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStructuredToolError(t, result, output, "absolute")
	if !strings.Contains(output.Error, "relative paths require cwd_id") {
		t.Fatalf("list_dir relative-path error should explain cwd_id relative mode: %q", output.Error)
	}
}

func TestInspectPathRequiresTargetPath(t *testing.T) {
	h := NewHandler()
	result, output, err := h.HandleInspectPath(context.Background(), nil, InspectPathInput{})
	if err != nil {
		t.Fatal(err)
	}
	assertStructuredToolError(t, result, output, "target_path is required")
}

func TestInspectPathRejectsRelativeTargetPath(t *testing.T) {
	h := NewHandler()
	result, output, err := h.HandleInspectPath(context.Background(), nil, InspectPathInput{
		TargetPath: ".",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStructuredToolError(t, result, output, "absolute")
}

func TestInspectPathDiscoveryContextResolvesCwdRelativeTargetDirectoryForGrepGlob(t *testing.T) {
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "app.ts"), []byte("export const value = 1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(WithConfig(cwdRegistryTestConfig(t)))
	cwdID := setCwdWithHandlerForTest(t, h, tempDir)
	result, output, err := h.HandleInspectPath(context.Background(), nil, InspectPathInput{
		CwdAwareInput: CwdAwareInput{CwdID: CwdIDInput{Present: true, Value: cwdID}},
		TargetPath:    "src/app.ts",
		DiscoveryContext: &InspectPathDiscoveryContext{
			TargetDirectory: ".",
			GrepGlob:        "src/*.ts",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.Visibility == nil {
		t.Fatalf("inspect_path should return visibility for cwd-relative discovery context: result=%#v output=%#v", result, output)
	}
	if output.Path != "src/app.ts" || output.ResolvedPath != "src/app.ts" {
		t.Fatalf("cwd inspect output paths should stay relative: %#v", output)
	}
	if hasVisibilityReason(output.Visibility.Reasons, "grep_glob_mismatch") || !output.Visibility.WouldGrepTraverse {
		t.Fatalf("cwd-relative target_directory should make grep_glob match: %#v", output.Visibility)
	}
}

func TestInspectPathFlagsNestedHiddenSegments(t *testing.T) {
	tempDir := t.TempDir()
	workflowDir := filepath.Join(tempDir, ".github", "workflows")
	if err := os.MkdirAll(workflowDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workflowDir, "deploy.yml"), []byte("name: deploy\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(WithConfig(cwdRegistryTestConfig(t)))
	cwdID := setCwdWithHandlerForTest(t, h, tempDir)
	result, output, err := h.HandleInspectPath(context.Background(), nil, InspectPathInput{
		CwdAwareInput: CwdAwareInput{CwdID: CwdIDInput{Present: true, Value: cwdID}},
		TargetPath:    ".github/workflows/deploy.yml",
		DiscoveryContext: &InspectPathDiscoveryContext{
			TargetDirectory: ".",
			GrepGlob:        ".github/**/*.yml",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.Visibility == nil {
		t.Fatalf("inspect_path should return hidden visibility details: result=%#v output=%#v", result, output)
	}
	if !output.IsHidden || !hasVisibilityReason(output.Visibility.Reasons, "hidden_excluded") || output.Visibility.WouldGrepTraverse {
		t.Fatalf("nested hidden path should be marked hidden and excluded by default: %#v", output)
	}
}

func TestInspectPathVisibilityMissingAndBinaryDoNotOverclaimDiscovery(t *testing.T) {
	tempDir := t.TempDir()
	h := NewHandler()
	result, missing, err := h.HandleInspectPath(context.Background(), nil, InspectPathInput{
		TargetPath: filepath.Join(tempDir, "missing.txt"),
		DiscoveryContext: &InspectPathDiscoveryContext{
			TargetDirectory: tempDir,
			GrepGlob:        "*.txt",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || missing.Visibility == nil {
		t.Fatalf("missing inspect should still return visibility diagnostics: result=%#v output=%#v", result, missing)
	}
	if missing.Visibility.WouldListDirShow || missing.Visibility.WouldGlobMatch || missing.Visibility.WouldGrepTraverse {
		t.Fatalf("missing path should not claim discovery visibility: %#v", missing.Visibility)
	}

	binaryFile := filepath.Join(tempDir, "image.bin")
	if err := os.WriteFile(binaryFile, []byte{0x00, 0x01, 0x02, 0x03}, 0644); err != nil {
		t.Fatal(err)
	}
	result, binary, err := h.HandleInspectPath(context.Background(), nil, InspectPathInput{
		TargetPath: binaryFile,
		DiscoveryContext: &InspectPathDiscoveryContext{
			TargetDirectory: tempDir,
			GrepGlob:        "*.bin",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || binary.Visibility == nil {
		t.Fatalf("binary inspect should return visibility diagnostics: result=%#v output=%#v", result, binary)
	}
	if !binary.Visibility.WouldListDirShow || !binary.Visibility.WouldGlobMatch || binary.Visibility.WouldGrepTraverse || !hasVisibilityReason(binary.Visibility.Reasons, "binary_excluded") {
		t.Fatalf("binary path should list/glob but not grep traverse: %#v", binary.Visibility)
	}
}

func TestWorkspaceInventoryRequiresTargetDirectory(t *testing.T) {
	h := NewHandler()
	result, output, err := h.HandleWorkspaceInventory(context.Background(), nil, WorkspaceInventoryInput{})
	if err != nil {
		t.Fatal(err)
	}
	assertStructuredToolError(t, result, output, "target_directory is required")
}

func TestWorkspaceInventoryRejectsRelativeTargetDirectory(t *testing.T) {
	h := NewHandler()
	result, output, err := h.HandleWorkspaceInventory(context.Background(), nil, WorkspaceInventoryInput{
		TargetDirectory: ".",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStructuredToolError(t, result, output, "absolute")
}

func TestGlobFileSearchLimitTruncatesResults(t *testing.T) {
	tempDir := t.TempDir()
	for _, name := range []string{"a.go", "b.go", "c.go"} {
		if err := os.WriteFile(filepath.Join(tempDir, name), []byte("package main\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	h := NewHandler()
	result, output, err := h.HandleGlobFileSearch(context.Background(), nil, GlobFileSearchInput{
		TargetDirectory: tempDir,
		GlobPattern:     "*.go",
		Limit:           intPtr(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("glob_file_search returned error: %#v", output)
	}
	if output.Limit != 2 || output.Count != 2 || output.TotalMatchCount != 3 || !output.Truncated || len(output.Files) != 2 {
		t.Fatalf("unexpected limited glob output: %#v", output)
	}
	if output.Continuation == nil || output.Continuation.Consistency != "unknown" || output.Continuation.LastSortKey == nil {
		t.Fatalf("truncated glob continuation should be stateless and honest about tree stability: %#v", output.Continuation)
	}
	if output.NextRecommendedCall != nil || len(output.NextRecommendedCalls) != 0 {
		t.Fatalf("truncated glob should not emit noisy read/outline recommendations: %#v", output.NextRecommendedCalls)
	}
}

func TestGlobFileSearchCompleteContinuationDoesNotClaimUnchangedTree(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "a.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGlobFileSearch(context.Background(), nil, GlobFileSearchInput{
		TargetDirectory: tempDir,
		GlobPattern:     "*.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.Truncated || output.Continuation == nil {
		t.Fatalf("complete glob search should succeed with continuation metadata: result=%#v output=%#v", result, output)
	}
	if output.Continuation.Consistency != "unknown" || !strings.Contains(output.Continuation.Reason, "not proven") {
		t.Fatalf("complete glob continuation must not overclaim unchanged tree stability: %#v", output.Continuation)
	}
	if output.NextRecommendedCall == nil || output.NextRecommendedCall.RecommendedNextTool != "outline_file" || output.NextRecommendedCall.RecommendedNextInputPolicy != "inspect_single_glob_match_structure" {
		t.Fatalf("single source-like glob match should recommend outline_file first: %#v", output.NextRecommendedCall)
	}
	if len(output.NextRecommendedCalls) < 2 || output.NextRecommendedCalls[1].RecommendedNextTool != "read_files" {
		t.Fatalf("single text-like glob match should also expose bounded read_files recommendation: %#v", output.NextRecommendedCalls)
	}
}

func TestGlobFileSearchNoMatchMessageIsCwdAware(t *testing.T) {
	tempDir := t.TempDir()
	h := NewHandler(WithConfig(cwdRegistryTestConfig(t)))
	cwdID := setCwdWithHandlerForTest(t, h, tempDir)

	result, output, err := h.HandleGlobFileSearch(context.Background(), nil, GlobFileSearchInput{
		CwdAwareInput:   CwdAwareInput{CwdID: CwdIDInput{Present: true, Value: cwdID}},
		TargetDirectory: ".",
		GlobPattern:     "*.missing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.Message == "" {
		t.Fatalf("cwd no-match glob should return a friendly structured message: result=%#v output=%#v", result, output)
	}
	if strings.Contains(output.Message, "absolute target_directory") || strings.Contains(output.Text, "absolute target_directory") {
		t.Fatalf("cwd no-match message should not recommend absolute-only input: message=%q text=%q", output.Message, output.Text)
	}
}

func TestGrepLimitTruncatesStreamingOutput(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(file, []byte("needle\nneedle\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    tempDir,
		Pattern: "needle",
		Limit:   intPtr(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", output)
	}
	if output.Limit != 1 || output.RowCount != 1 || len(output.Matches) != 1 || !output.Truncated {
		t.Fatalf("unexpected limited grep output: %#v", output)
	}
}

func TestGrepEmptyFileDoesNotProduceFalseLineEvidence(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "empty.txt")
	if err := os.WriteFile(file, []byte{}, 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		outputMode string
		multiline  bool
	}{
		{name: "content"},
		{name: "files", outputMode: "files_with_matches"},
		{name: "count", outputMode: "count"},
		{name: "multiline_content", multiline: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHandler()
			result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
				Path:       file,
				Pattern:    "^$",
				OutputMode: tt.outputMode,
				Multiline:  tt.multiline,
				Limit:      intPtr(10),
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError {
				t.Fatalf("grep returned error: %#v", output)
			}
			if output.MatchCount != 0 || output.RowCount != 0 || len(output.Matches) != 0 || len(output.Files) != 0 || len(output.Counts) != 0 || len(output.FileGroups) != 0 {
				t.Fatalf("empty file should not create false line evidence: %#v", output)
			}
			if output.SearchStats == nil || !output.SearchStats.Completed || !output.SearchStats.CountsAreComplete || output.SearchStats.StopReason != "" || output.SearchStats.FilesSearched != 1 {
				t.Fatalf("empty file should be a complete searched no-match: %#v", output.SearchStats)
			}
		})
	}
}

func TestGrepExactLimitContentIsComplete(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(file, []byte("needle\nneedle\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    tempDir,
		Pattern: "needle",
		Limit:   intPtr(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", output)
	}
	if output.RowCount != 2 || output.MatchCount != 2 || output.Truncated {
		t.Fatalf("exact content limit should return complete rows without truncation: %#v", output)
	}
	if output.SearchStats == nil || !output.SearchStats.Completed || !output.SearchStats.CountsAreComplete || output.SearchStats.StopReason != "" {
		t.Fatalf("exact content limit should keep complete stats: %#v", output.SearchStats)
	}
}

func TestGrepExactLimitFilesWithMatchesIsComplete(t *testing.T) {
	tempDir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(tempDir, name), []byte("needle\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:       tempDir,
		Pattern:    "needle",
		OutputMode: "files_with_matches",
		Limit:      intPtr(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", output)
	}
	if output.RowCount != 2 || output.MatchCount != 2 || len(output.Files) != 2 || output.Truncated {
		t.Fatalf("exact files limit should return complete rows without truncation: %#v", output)
	}
	if output.SearchStats == nil || !output.SearchStats.Completed || !output.SearchStats.CountsAreComplete || output.SearchStats.StopReason != "" {
		t.Fatalf("exact files limit should keep complete stats: %#v", output.SearchStats)
	}
}

func TestGrepExactLimitCountIsComplete(t *testing.T) {
	tempDir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(tempDir, name), []byte("needle\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:       tempDir,
		Pattern:    "needle",
		OutputMode: "count",
		Limit:      intPtr(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", output)
	}
	if output.RowCount != 2 || output.MatchCount != 2 || len(output.Counts) != 2 || output.Truncated {
		t.Fatalf("exact count limit should return complete rows without truncation: %#v", output)
	}
	if output.SearchStats == nil || !output.SearchStats.Completed || !output.SearchStats.CountsAreComplete || output.SearchStats.StopReason != "" {
		t.Fatalf("exact count limit should keep complete stats: %#v", output.SearchStats)
	}
}

func TestGrepOutputPathUsesResolvedServerPath(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(file, []byte("needle\n"), 0644); err != nil {
		t.Fatal(err)
	}

	slashPath := filepath.ToSlash(tempDir)
	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    slashPath,
		Pattern: "needle",
		Limit:   intPtr(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", output)
	}
	expected := filepath.ToSlash(filepath.Clean(slashPath))
	if output.Path != expected {
		t.Fatalf("grep output path should use resolved server path, got %q want %q", output.Path, expected)
	}
}

func TestGrepContextDoesNotDuplicateNeighborRows(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.txt")
	content := strings.Join([]string{"needle one", "between", "needle two"}, "\n")
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    file,
		Pattern: "needle",
		Context: 1,
		Limit:   intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", output)
	}
	seen := make(map[int]int)
	for _, match := range output.Matches {
		seen[match.Line]++
	}
	for line, count := range seen {
		if count != 1 {
			t.Fatalf("grep duplicated line %d in context output: %#v", line, output.Matches)
		}
	}
	if output.MatchCount != 2 || output.RowCount != 3 {
		t.Fatalf("unexpected grep context counts: %#v", output)
	}
}

func TestGrepMultilineContextDoesNotDuplicateRows(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "multi.txt")
	content := strings.Join([]string{"before", "start", "end", "middle", "start", "end", "after"}, "\n")
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:      file,
		Pattern:   "start\nend",
		Context:   1,
		Multiline: true,
		Limit:     intPtr(20),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", output)
	}
	seen := make(map[int]int)
	for _, match := range output.Matches {
		seen[match.Line]++
	}
	for line, count := range seen {
		if count != 1 {
			t.Fatalf("multiline grep duplicated line %d in context output: %#v", line, output.Matches)
		}
	}
	if output.MatchCount != 2 || output.RowCount != 7 {
		t.Fatalf("multiline grep should count logical matches and unique rows: %#v", output)
	}
}

func TestInspectPathReturnsFileMetadata(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(file, []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleInspectPath(context.Background(), nil, InspectPathInput{TargetPath: file})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("inspect_path returned error: %#v", output)
	}
	if !output.Exists || output.Kind != "file" || output.Name != "sample.txt" || output.SizeBytes == nil || *output.SizeBytes != 6 || output.IsBinary == nil || *output.IsBinary || output.LineCount == nil || *output.LineCount != 2 {
		t.Fatalf("unexpected inspect_path file output: %#v", output)
	}
}

func TestInspectPathCountsTextLines(t *testing.T) {
	tempDir := t.TempDir()
	cases := []struct {
		name string
		text string
		want int
	}{
		{name: "empty.txt", text: "", want: 0},
		{name: "no-final-newline.txt", text: "a\nb", want: 2},
		{name: "final-newline.txt", text: "a\nb\n", want: 3},
	}

	h := NewHandler()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			file := filepath.Join(tempDir, tt.name)
			if err := os.WriteFile(file, []byte(tt.text), 0644); err != nil {
				t.Fatal(err)
			}
			result, output, err := h.HandleInspectPath(context.Background(), nil, InspectPathInput{TargetPath: file})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError {
				t.Fatalf("inspect_path returned error: %#v", output)
			}
			if output.LineCount == nil || *output.LineCount != tt.want {
				t.Fatalf("line_count mismatch for %q: got %#v want %d", tt.name, output.LineCount, tt.want)
			}
		})
	}
}

func TestInspectPathOmitsLineCountForBinaryAndDirectory(t *testing.T) {
	tempDir := t.TempDir()
	binaryFile := filepath.Join(tempDir, "asset.bin")
	if err := os.WriteFile(binaryFile, []byte{0, 1, 2, '\n'}, 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleInspectPath(context.Background(), nil, InspectPathInput{TargetPath: binaryFile})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("inspect_path returned error: %#v", output)
	}
	if output.IsBinary == nil || !*output.IsBinary || output.LineCount != nil {
		t.Fatalf("binary inspect_path should omit line_count: %#v", output)
	}

	result, output, err = h.HandleInspectPath(context.Background(), nil, InspectPathInput{TargetPath: tempDir})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("inspect_path returned error: %#v", output)
	}
	if output.LineCount != nil {
		t.Fatalf("directory inspect_path should omit line_count: %#v", output)
	}
}

func TestInspectPathCountsUnicodeBOMTextLines(t *testing.T) {
	tempDir := t.TempDir()
	utf16File := filepath.Join(tempDir, "utf16.txt")
	utf16Content := []byte{0xFF, 0xFE}
	for _, r := range "first\nsecond\n" {
		utf16Content = append(utf16Content, byte(r), 0x00)
	}
	if err := os.WriteFile(utf16File, utf16Content, 0644); err != nil {
		t.Fatal(err)
	}
	utf32File := filepath.Join(tempDir, "utf32be.txt")
	if err := os.WriteFile(utf32File, utf32Bytes("first\nsecond", false), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	for _, tt := range []struct {
		name string
		file string
		want int
	}{
		{name: "utf16le", file: utf16File, want: 3},
		{name: "utf32be", file: utf32File, want: 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, output, err := h.HandleInspectPath(context.Background(), nil, InspectPathInput{TargetPath: tt.file})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError {
				t.Fatalf("inspect_path returned error: %#v", output)
			}
			if output.IsBinary == nil || *output.IsBinary || output.LineCount == nil || *output.LineCount != tt.want {
				t.Fatalf("inspect_path should count BOM text lines: %#v", output)
			}
		})
	}
}

func TestInspectPathReturnsDirectoryDirectCounts(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "visible.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, ".hidden"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(tempDir, "child"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(tempDir, ".hidden-dir"), 0755); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleInspectPath(context.Background(), nil, InspectPathInput{TargetPath: tempDir})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("inspect_path returned error: %#v", output)
	}
	if output.Kind != "directory" || output.DirectFileCount == nil || *output.DirectFileCount != 1 || output.DirectDirCount == nil || *output.DirectDirCount != 1 {
		t.Fatalf("inspect_path directory counts should be direct and skip dot entries: %#v", output)
	}
}

func TestInspectPathReturnsAbsoluteSymlinkTarget(t *testing.T) {
	tempDir := t.TempDir()
	targetName := " target.txt"
	target := filepath.Join(tempDir, targetName)
	if err := os.WriteFile(target, []byte("target"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tempDir, "link.txt")
	if err := os.Symlink(targetName, link); err != nil {
		t.Skipf("symlink creation unavailable in this environment: %v", err)
	}

	h := NewHandler()
	result, output, err := h.HandleInspectPath(context.Background(), nil, InspectPathInput{TargetPath: link})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("inspect_path returned error: %#v", output)
	}
	if output.Kind != "symlink" || output.SymlinkTarget != filepath.ToSlash(target) || output.SymlinkTargetKind != "file" {
		t.Fatalf("inspect_path should expose absolute symlink target, got %#v want target %q", output, target)
	}
}

func TestWorkspaceInventoryReturnsNestedDirectoryCounts(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "root.txt"), []byte("root"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(tempDir, "folder1"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(tempDir, "folder2"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "folder1", "inside.txt"), []byte("inside"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(tempDir, ".hidden"), 0755); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleWorkspaceInventory(context.Background(), nil, WorkspaceInventoryInput{
		TargetDirectory: tempDir,
		MaxDepth:        intPtr(2),
		Limit:           intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("workspace_inventory returned error: %#v", output)
	}
	if output.Root == nil {
		t.Fatalf("workspace_inventory success must include root: %#v", output)
	}
	root := output.Root
	if root.DirectFileCount != 1 || root.DirectDirCount != 2 || len(root.Directories) != 2 || !output.DotEntriesSkipped {
		t.Fatalf("unexpected root inventory counts: %#v", root)
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "relative_path") {
		t.Fatalf("workspace_inventory must not expose relative paths: %s", encoded)
	}
	folder1 := findInventoryDirectory(root.Directories, "folder1")
	if folder1 == nil || folder1.DirectFileCount != 1 || folder1.DirectDirCount != 0 {
		t.Fatalf("unexpected folder1 inventory: %#v", root.Directories)
	}
	if output.NextRecommendedCall == nil || output.NextRecommendedCall.RecommendedNextTool != "glob_file_search" || output.NextRecommendedCall.RecommendedNextInputPolicy != "discover_files_in_directory" {
		t.Fatalf("workspace inventory should recommend only directory-level glob discovery: %#v", output.NextRecommendedCall)
	}
	if output.NextRecommendedCall.RecommendedNextInput["target_directory"] == "" || output.NextRecommendedCall.RecommendedNextInput["glob_pattern"] != "*" {
		t.Fatalf("workspace glob recommendation should stay directory-level and bounded: %#v", output.NextRecommendedCall.RecommendedNextInput)
	}
}

func TestWorkspaceInventoryReportsHiddenBackupCandidateDirectory(t *testing.T) {
	tempDir := t.TempDir()
	backupName := ".target.txt.20260605T120000Z.ab12cd34.1.bak"
	if err := os.WriteFile(filepath.Join(tempDir, backupName), []byte("backup\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleWorkspaceInventory(context.Background(), nil, WorkspaceInventoryInput{
		TargetDirectory: tempDir,
		MaxDepth:        intPtr(1),
		Limit:           intPtr(5),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.Summary == nil {
		t.Fatalf("workspace_inventory should return summary with backup candidates: result=%#v output=%#v", result, output)
	}
	if len(output.Summary.BackupCandidateDirectories) != 1 {
		t.Fatalf("hidden sidecar backup directory should be reported as a candidate: %#v", output.Summary.BackupCandidateDirectories)
	}
	dir := filepath.ToSlash(tempDir)
	candidate := output.Summary.BackupCandidateDirectories[0]
	if candidate.Path != dir || candidate.CandidateFileCount != 1 {
		t.Fatalf("backup candidate should identify only the directory and count: %#v", candidate)
	}
	if len(output.Summary.BackupDiscoveryHints) != 1 {
		t.Fatalf("backup candidate should include rediscovery hint: %#v", output.Summary.BackupDiscoveryHints)
	}
	input := output.Summary.BackupDiscoveryHints[0].RecommendedNextInput
	if input["target_directory"] != dir || input["glob_pattern"] != ".*.bak" || input["include_hidden"] != true {
		t.Fatalf("backup rediscovery hint should be ready for hidden glob search: %#v", input)
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), backupName) {
		t.Fatalf("workspace_inventory should not reveal hidden backup filenames in summary: %s", encoded)
	}
}

func TestWorkspaceInventorySummaryProfileContract(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "package.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	defaultResult, defaultOutput, err := h.HandleWorkspaceInventory(context.Background(), nil, WorkspaceInventoryInput{
		TargetDirectory: tempDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if defaultResult.IsError || defaultOutput.Summary == nil || defaultOutput.Summary.Profile != "compact" {
		t.Fatalf("workspace_inventory should default summary_profile to compact: result=%#v output=%#v", defaultResult, defaultOutput)
	}

	noneResult, noneOutput, err := h.HandleWorkspaceInventory(context.Background(), nil, WorkspaceInventoryInput{
		TargetDirectory: tempDir,
		SummaryProfile:  "none",
	})
	if err != nil {
		t.Fatal(err)
	}
	if noneResult.IsError || noneOutput.Summary != nil {
		t.Fatalf("summary_profile=none should omit summary: result=%#v output=%#v", noneResult, noneOutput)
	}

	extendedResult, extendedOutput, err := h.HandleWorkspaceInventory(context.Background(), nil, WorkspaceInventoryInput{
		TargetDirectory: tempDir,
		SummaryProfile:  "extended",
	})
	if err != nil {
		t.Fatal(err)
	}
	if extendedResult.IsError || extendedOutput.Summary == nil || extendedOutput.Summary.Profile != "extended" {
		t.Fatalf("summary_profile=extended should be accepted and reflected: result=%#v output=%#v", extendedResult, extendedOutput)
	}

	invalidResult, invalidOutput, err := h.HandleWorkspaceInventory(context.Background(), nil, WorkspaceInventoryInput{
		TargetDirectory: tempDir,
		SummaryProfile:  "surprise",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !invalidResult.IsError || invalidOutput.ErrorCode != "invalid_summary_profile" {
		t.Fatalf("invalid summary_profile should return a structured error: result=%#v output=%#v", invalidResult, invalidOutput)
	}
}

func TestWorkspaceInventorySummaryCountsIgnoredEntriesSeparatelyFromHidden(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "ignored.tmp"), []byte("tmp"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(tempDir, ".hidden"), 0755); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleWorkspaceInventory(context.Background(), nil, WorkspaceInventoryInput{
		TargetDirectory: tempDir,
		IgnoreGlobs:     []string{"*.tmp"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.Summary == nil {
		t.Fatalf("workspace_inventory should return summary counts: result=%#v output=%#v", result, output)
	}
	if output.Summary.HiddenEntriesSkipped != 1 || output.Summary.IgnoredEntriesSkipped != 1 {
		t.Fatalf("summary should count ignored-glob entries separately from hidden skips: %#v", output.Summary)
	}
}

func TestWorkspaceInventorySummaryIncompleteWhenMaxDepthReached(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tempDir, "level1", "level2"), 0755); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleWorkspaceInventory(context.Background(), nil, WorkspaceInventoryInput{
		TargetDirectory: tempDir,
		MaxDepth:        intPtr(0),
		Limit:           intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.Summary == nil || !output.MaxDepthReached {
		t.Fatalf("workspace_inventory should report max_depth coverage loss: result=%#v output=%#v", result, output)
	}
	if output.Summary.Complete {
		t.Fatalf("summary.complete should be false when max_depth hides deeper directories: %#v", output.Summary)
	}
	if output.Continuation == nil || output.Continuation.PageComplete == nil || !*output.Continuation.PageComplete || !output.Continuation.Complete {
		t.Fatalf("continuation completeness should describe the returned page, not tree coverage: %#v", output.Continuation)
	}
	if output.Summary.SummaryCoverageComplete || output.Summary.TreeScanComplete || output.Summary.SummaryIncompleteReason != "max_depth_reached" || output.Summary.ScanScope != "max_depth_limited" {
		t.Fatalf("summary should expose explicit max_depth coverage fields: %#v", output.Summary)
	}
}

func TestWorkspaceInventoryScanCapDoesNotCountUnscannedDirectory(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(tempDir, "child"), 0755); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	builder := workspaceInventoryBuilder{
		ctx:            context.Background(),
		handler:        h,
		pathCtx:        PathContext{},
		root:           tempDir,
		requestedRoot:  tempDir,
		maxDepth:       1,
		pageLimit:      100,
		scanLimit:      1,
		ignoreMatcher:  newCompiledIgnoreMatcher(nil),
		fileTypeCounts: map[string]int{},
		packageHints:   map[string]bool{},
		sourceDirHints: map[string]bool{},
		testDirHints:   map[string]bool{},
		backupDirs:     map[string]int{},
	}
	_ = builder.build(tempDir, 0, "")
	if !builder.truncated || !strings.Contains(builder.truncationReason, "directory scan limit") {
		t.Fatalf("test fixture should hit the scan cap, got truncated=%v reason=%q", builder.truncated, builder.truncationReason)
	}
	if builder.directoryCount != 1 {
		t.Fatalf("directory_count should count only scanned page directories, got %d", builder.directoryCount)
	}
}

func TestWorkspaceInventoryContinuationDoesNotSpendScanCapBeforeLastSortKey(t *testing.T) {
	tempDir := t.TempDir()
	for i := 0; i < 260; i++ {
		name := fmt.Sprintf("d%03d", i)
		if err := os.Mkdir(filepath.Join(tempDir, name), 0755); err != nil {
			t.Fatal(err)
		}
	}

	h := NewHandler()
	firstResult, first, err := h.HandleWorkspaceInventory(context.Background(), nil, WorkspaceInventoryInput{
		TargetDirectory: tempDir,
		MaxDepth:        intPtr(1),
		Limit:           intPtr(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if firstResult.IsError || first.Continuation == nil || first.Continuation.CanonicalQueryHash == "" {
		t.Fatalf("first inventory page should expose continuation hash: result=%#v output=%#v", firstResult, first)
	}

	lastReturned := filepath.ToSlash(filepath.Join(tempDir, "d220"))
	nextResult, next, err := h.HandleWorkspaceInventory(context.Background(), nil, WorkspaceInventoryInput{
		TargetDirectory: tempDir,
		MaxDepth:        intPtr(1),
		Limit:           intPtr(1),
		ContinuationAfter: &DiscoveryContinuationAfter{
			CanonicalQueryHash: first.Continuation.CanonicalQueryHash,
			LastSortKey:        DiscoverySortKey{Path: lastReturned},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if nextResult.IsError || len(next.DirectoriesPage) != 1 {
		t.Fatalf("continuation after a late sort key should not dead-end on pre-continuation scan cap: result=%#v output=%#v", nextResult, next)
	}
	if !strings.HasSuffix(next.DirectoriesPage[0].Path, "d221") {
		t.Fatalf("continuation should resume after d220, got page=%#v", next.DirectoriesPage)
	}
	if next.Continuation == nil || next.Continuation.NextRecommendedCall == nil {
		t.Fatalf("truncated continuation page should remain actionable: %#v", next.Continuation)
	}
}

func TestWorkspaceInventoryContinuationSummaryIsPageLocal(t *testing.T) {
	tempDir := t.TempDir()
	for _, name := range []string{"a", "b"} {
		if err := os.Mkdir(filepath.Join(tempDir, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(tempDir, "a", "package.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "b", "go.mod"), []byte("module example.test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	firstResult, first, err := h.HandleWorkspaceInventory(context.Background(), nil, WorkspaceInventoryInput{
		TargetDirectory: tempDir,
		MaxDepth:        intPtr(1),
		Limit:           intPtr(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if firstResult.IsError || first.Continuation == nil || first.Continuation.LastSortKey == nil {
		t.Fatalf("first page should expose continuation metadata: result=%#v output=%#v", firstResult, first)
	}

	secondResult, second, err := h.HandleWorkspaceInventory(context.Background(), nil, WorkspaceInventoryInput{
		TargetDirectory: tempDir,
		MaxDepth:        intPtr(1),
		Limit:           intPtr(2),
		ContinuationAfter: &DiscoveryContinuationAfter{
			CanonicalQueryHash: first.Continuation.CanonicalQueryHash,
			LastSortKey:        *first.Continuation.LastSortKey,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if secondResult.IsError || second.Summary == nil || len(second.DirectoriesPage) == 0 {
		t.Fatalf("second page should return a page-local summary: result=%#v output=%#v", secondResult, second)
	}
	if second.Summary.Complete {
		t.Fatalf("continuation page summary should not claim whole-workspace completeness: %#v", second.Summary)
	}
	if second.Summary.SummaryCoverageComplete || second.Summary.TreeScanComplete || second.Summary.SummaryIncompleteReason != "continuation_page" || second.Summary.ScanScope != "continuation_page" {
		t.Fatalf("continuation page summary should explicitly mark page-local coverage: %#v", second.Summary)
	}
	if second.Continuation == nil || second.Continuation.PageComplete == nil || !*second.Continuation.PageComplete {
		t.Fatalf("final continuation page should mark page_complete=true: %#v", second.Continuation)
	}
	joinedHints := strings.Join(second.Summary.PackageHints, "\n")
	if strings.Contains(joinedHints, "/a/package.json") || !strings.Contains(joinedHints, "/b/go.mod") {
		t.Fatalf("continuation summary should include only current-page package hints, got %#v", second.Summary.PackageHints)
	}
}

func TestWorkspaceInventoryContinuationPagesDirectoriesWithoutDuplicates(t *testing.T) {
	tempDir := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		if err := os.Mkdir(filepath.Join(tempDir, name), 0755); err != nil {
			t.Fatal(err)
		}
	}

	h := NewHandler()
	firstResult, first, err := h.HandleWorkspaceInventory(context.Background(), nil, WorkspaceInventoryInput{
		TargetDirectory: tempDir,
		MaxDepth:        intPtr(1),
		Limit:           intPtr(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if firstResult.IsError || !first.Truncated || len(first.DirectoriesPage) != 2 || first.Continuation == nil || first.Continuation.NextRecommendedCall == nil {
		t.Fatalf("first inventory page should be truncated with continuation: result=%#v output=%#v", firstResult, first)
	}
	if first.Continuation.CanonicalQueryHash == "" || first.Continuation.LastSortKey == nil {
		t.Fatalf("workspace continuation should expose query hash and last sort key: %#v", first.Continuation)
	}
	if first.Continuation.PageComplete == nil || *first.Continuation.PageComplete {
		t.Fatalf("truncated workspace page should expose page_complete=false: %#v", first.Continuation)
	}

	nextInput := WorkspaceInventoryInput{
		TargetDirectory:   tempDir,
		MaxDepth:          intPtr(1),
		Limit:             intPtr(2),
		ContinuationAfter: &DiscoveryContinuationAfter{CanonicalQueryHash: first.Continuation.CanonicalQueryHash, LastSortKey: *first.Continuation.LastSortKey},
	}
	secondResult, second, err := h.HandleWorkspaceInventory(context.Background(), nil, nextInput)
	if err != nil {
		t.Fatal(err)
	}
	if secondResult.IsError || len(second.DirectoriesPage) == 0 {
		t.Fatalf("second inventory page should continue successfully: result=%#v output=%#v", secondResult, second)
	}
	seen := map[string]bool{}
	for _, entry := range first.DirectoriesPage {
		seen[entry.Path] = true
	}
	for _, entry := range second.DirectoriesPage {
		if seen[entry.Path] {
			t.Fatalf("workspace_inventory continuation duplicated directory %q; first=%#v second=%#v", entry.Path, first.DirectoriesPage, second.DirectoriesPage)
		}
	}
	if second.Continuation == nil || second.Continuation.Consistency != "unknown" {
		t.Fatalf("workspace_inventory final page should not claim unchanged tree stability: %#v", second.Continuation)
	}
	if second.Continuation.PageComplete == nil || !*second.Continuation.PageComplete {
		t.Fatalf("workspace_inventory final page should expose page_complete=true: %#v", second.Continuation)
	}
}

func TestWorkspaceInventoryValidationErrorsOmitEmptyRootPath(t *testing.T) {
	h := NewHandler()
	for _, input := range []WorkspaceInventoryInput{
		{},
		{TargetDirectory: "."},
	} {
		result, output, err := h.HandleWorkspaceInventory(context.Background(), nil, input)
		if err != nil {
			t.Fatal(err)
		}
		if !result.IsError {
			t.Fatalf("workspace_inventory validation should return error for %#v", input)
		}
		if output.Root != nil {
			t.Fatalf("workspace_inventory validation error should not include root: %#v", output)
		}
		encoded, err := json.Marshal(output)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), `"path":""`) || strings.Contains(string(encoded), `"root"`) {
			t.Fatalf("workspace_inventory validation error must not expose empty path fields: %s", encoded)
		}
	}
}

func TestGlobFileSearchRequiresExplicitTargetDirectory(t *testing.T) {
	h := NewHandler()
	result, output, err := h.HandleGlobFileSearch(context.Background(), nil, GlobFileSearchInput{
		GlobPattern: "*.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStructuredToolError(t, result, output, "target_directory is required")
	if !strings.Contains(output.Error, "target_directory is required") || !strings.Contains(output.Error, "absolute path") {
		t.Fatalf("glob missing-target error should explain explicit path requirement: %q", output.Error)
	}
}

func TestGlobFileSearchRejectsRelativeTargetDirectory(t *testing.T) {
	h := NewHandler()
	result, output, err := h.HandleGlobFileSearch(context.Background(), nil, GlobFileSearchInput{
		TargetDirectory: ".",
		GlobPattern:     "*.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStructuredToolError(t, result, output, "absolute")
	if !strings.Contains(output.Error, "relative paths require cwd_id") {
		t.Fatalf("glob relative-target error should explain cwd_id relative mode: %q", output.Error)
	}
}

func TestGlobFileSearchSimplePatternSearchesRecursively(t *testing.T) {
	tempDir := t.TempDir()
	nestedDir := filepath.Join(tempDir, "src", "internal")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatal(err)
	}
	nestedFile := filepath.Join(nestedDir, "handler.go")
	if err := os.WriteFile(nestedFile, []byte("package internal"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "readme.md"), []byte("# readme"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGlobFileSearch(context.Background(), nil, GlobFileSearchInput{
		TargetDirectory: tempDir,
		GlobPattern:     "*.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("glob_file_search returned error: %#v", result.Content)
	}
	if !strings.Contains(output.Text, "handler.go") {
		t.Fatalf("glob_file_search did not find nested *.go file:\n%s", output.Text)
	}
}

func TestGlobFileSearchRejectsFileTargetDirectory(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "app.go")
	if err := os.WriteFile(file, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGlobFileSearch(context.Background(), nil, GlobFileSearchInput{
		TargetDirectory: file,
		GlobPattern:     "*.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStructuredToolError(t, result, output, "not a directory")
}

func TestGlobFileSearchWithTargetDirectoryAndDoubleStar(t *testing.T) {
	tempDir := t.TempDir()
	subDir := filepath.Join(tempDir, "subdir")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	nestedFile := filepath.Join(subDir, "nested.go")
	if err := os.WriteFile(nestedFile, []byte("package subdir"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGlobFileSearch(context.Background(), nil, GlobFileSearchInput{
		TargetDirectory: tempDir,
		GlobPattern:     "**/*.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("glob_file_search returned error: %#v", result.Content)
	}
	if !strings.Contains(output.Text, "nested.go") {
		t.Fatalf("glob_file_search did not find nested file with target_directory:\n%s", output.Text)
	}
}

func TestGlobFileSearchDisplaysMappedAliasPath(t *testing.T) {
	tempDir := t.TempDir()
	sourceDir := filepath.Join(t.TempDir(), "alias")
	file := filepath.Join(tempDir, "app.go")
	if err := os.WriteFile(file, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Load()
	cfg.PathMaps = []config.PathMap{
		{Source: sourceDir, Target: tempDir},
	}
	h := NewHandler(WithConfig(cfg))
	result, output, err := h.HandleGlobFileSearch(context.Background(), nil, GlobFileSearchInput{
		TargetDirectory: sourceDir,
		GlobPattern:     "*.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("glob_file_search returned error: %#v", result.Content)
	}
	if !strings.Contains(output.Text, filepath.ToSlash(filepath.Join(sourceDir, "app.go"))) {
		t.Fatalf("glob_file_search did not display mapped alias path:\n%s", output.Text)
	}
}

func TestGlobFileSearchSlashPatternIsRelativeToTargetRoot(t *testing.T) {
	tempDir := t.TempDir()
	nestedDir := filepath.Join(tempDir, "filetoolsserver", "handler")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "app.go"), []byte("package handler"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGlobFileSearch(context.Background(), nil, GlobFileSearchInput{
		TargetDirectory: tempDir,
		GlobPattern:     "handler/*.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("glob_file_search returned error: %#v", result.Content)
	}
	if strings.Contains(output.Text, "app.go") {
		t.Fatalf("slash glob pattern should not match nested suffix outside target root:\n%s", output.Text)
	}

	result, output, err = h.HandleGlobFileSearch(context.Background(), nil, GlobFileSearchInput{
		TargetDirectory: tempDir,
		GlobPattern:     "**/handler/*.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("glob_file_search returned error: %#v", result.Content)
	}
	if !strings.Contains(output.Text, "app.go") {
		t.Fatalf("recursive glob pattern should match nested handler file:\n%s", output.Text)
	}
}

func TestFriendlyValidationErrorsNamePublicParameters(t *testing.T) {
	h := NewHandler()

	result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{})
	if err != nil {
		t.Fatal(err)
	}
	assertStructuredToolError(t, result, output, "target_file")

	globResult, globOutput, err := h.HandleGlobFileSearch(context.Background(), nil, GlobFileSearchInput{})
	if err != nil {
		t.Fatal(err)
	}
	assertStructuredToolError(t, globResult, globOutput, "glob_pattern")
}

func assertStructuredToolError(t *testing.T, result *mcp.CallToolResult, output any, want string) {
	t.Helper()
	if result == nil || !result.IsError {
		t.Fatalf("expected tool error, got %#v", result)
	}
	if len(result.Content) != 0 {
		t.Fatalf("tool error should not duplicate plain text content, got %#v", result.Content)
	}
	if text := structuredOutputText(output); text != "" {
		t.Fatalf("tool error text should be empty, got %q", text)
	}
	errorText := structuredOutputError(output)
	if !strings.Contains(errorText, want) {
		t.Fatalf("tool error should mention %q, got %q", want, errorText)
	}
}

func structuredOutputText(output any) string {
	switch value := output.(type) {
	case ReadFileOutput:
		return value.Text
	case ListDirOutput:
		return value.Text
	case GlobFileSearchOutput:
		return value.Text
	case GrepOutput:
		return value.Text
	case InspectPathOutput:
		return value.Text
	case WorkspaceInventoryOutput:
		return value.Text
	default:
		return ""
	}
}

func structuredOutputError(output any) string {
	switch value := output.(type) {
	case ReadFileOutput:
		return value.Error
	case ListDirOutput:
		return value.Error
	case GlobFileSearchOutput:
		return value.Error
	case GrepOutput:
		return value.Error
	case InspectPathOutput:
		return value.Error
	case WorkspaceInventoryOutput:
		return value.Error
	default:
		return ""
	}
}

func TestToolErrorsReturnFullLongUserInput(t *testing.T) {
	tempDir := t.TempDir()
	longPattern := strings.Repeat("(", 12*1024)
	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    tempDir,
		Pattern: longPattern,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStructuredToolError(t, result, output, "Invalid regex")
	if !strings.Contains(output.Error, longPattern) {
		t.Fatalf("long tool error should preserve the full user input")
	}

	globResult, globOutput, err := h.HandleGlobFileSearch(context.Background(), nil, GlobFileSearchInput{
		GlobPattern: strings.Repeat("[", 12*1024),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStructuredToolError(t, globResult, globOutput, "target_directory is required")
}

func TestNoMatchResponsesReturnFullLongUserInput(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "sample.txt"), []byte("needle\n"), 0644); err != nil {
		t.Fatal(err)
	}
	longPattern := strings.Repeat("a", 12*1024)

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    tempDir,
		Pattern: longPattern,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep no-match should not be a tool error: %#v", result.Content)
	}
	if !strings.Contains(output.Text, "No matches found") || !strings.Contains(output.Text, longPattern) {
		t.Fatalf("long grep no-match response should be helpful and complete, got %q", output.Text)
	}

	globResult, globOutput, err := h.HandleGlobFileSearch(context.Background(), nil, GlobFileSearchInput{
		TargetDirectory: tempDir,
		GlobPattern:     longPattern,
	})
	if err != nil {
		t.Fatal(err)
	}
	if globResult.IsError {
		t.Fatalf("glob no-match should not be a tool error: %#v", globResult.Content)
	}
	if !strings.Contains(globOutput.Text, "No files matched") || !strings.Contains(globOutput.Text, longPattern) {
		t.Fatalf("long glob no-match response should be helpful and complete, got %q", globOutput.Text)
	}
}

func TestReadFileLineNumbersForCRLFEmptyLines(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "crlf.txt")
	if err := os.WriteFile(file, []byte("alpha\r\n\r\nomega\r\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("read_file returned error: %#v", result.Content)
	}
	if strings.Contains(output.Text, "\r") {
		t.Fatalf("read_file output leaked CR characters:\n%q", output.Text)
	}
	if !strings.Contains(output.Text, "2|\n") {
		t.Fatalf("read_file output did not preserve empty line number:\n%s", output.Text)
	}
}

func TestGrepLongLineReturnsFullMatchWithLocationPrefix(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "long.txt")
	line := "needle" + strings.Repeat("x", 12*1024)
	if err := os.WriteFile(file, []byte(line), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    tempDir,
		Pattern: "needle",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", output)
	}
	if len(result.Content) != 0 {
		t.Fatalf("grep should suppress MCP text duplication, got %d content items", len(result.Content))
	}
	prefix := ":1:"
	if !strings.Contains(output.Text, prefix) {
		t.Fatalf("grep output did not include location prefix:\n%s", output.Text)
	}
	if !strings.Contains(output.Text, line) {
		t.Fatalf("grep output did not include complete matching line")
	}
}

func TestGrepStreamsLargeFilesInLineMode(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "large.log")
	content := strings.Repeat("noise\n", 200) + "needle here\n" + strings.Repeat("noise\n", 200)
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(WithConfig(&config.Config{MemoryThreshold: 128}))
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    tempDir,
		Pattern: "needle",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", output)
	}
	if !strings.Contains(output.Text, "large.log:201:needle here") {
		t.Fatalf("grep did not stream large file in line mode:\n%s", output.Text)
	}
}

func TestGrepLargeFileLineModeSeesTrailingEmptyLine(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "large-empty.log")
	content := strings.Repeat("noise\n", 200)
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(WithConfig(&config.Config{MemoryThreshold: 128}))
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    tempDir,
		Pattern: "^$",
		Limit:   intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", output)
	}
	if output.MatchCount != 1 || !strings.Contains(output.Text, "large-empty.log:201:") {
		t.Fatalf("large-file grep should expose final empty display line: %#v text=%q", output, output.Text)
	}
}

func TestGrepLargeFileMultilineLineWindowSeesTrailingEmptyLine(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "large-empty.log")
	content := strings.Repeat("noise\n", 200)
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	window := SourceLineRange{StartLine: 201, EndLine: 201}
	h := NewHandler(WithConfig(&config.Config{MemoryThreshold: 128}))
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:       file,
		Pattern:    "^$",
		Multiline:  true,
		LineWindow: &window,
		Limit:      intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", output)
	}
	if output.MatchCount != 1 || !strings.Contains(output.Text, "large-empty.log:201:") {
		t.Fatalf("large-file multiline line_window should expose final empty display line: %#v text=%q", output, output.Text)
	}
}

func TestGrepIgnoreGlobsSkipsDirectories(t *testing.T) {
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "src")
	vendorDir := filepath.Join(tempDir, "vendor")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(vendorDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "app.go"), []byte("package main\n// needle\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vendorDir, "dep.go"), []byte("package dep\n// needle\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:        tempDir,
		Pattern:     "needle",
		IgnoreGlobs: []string{"vendor/**"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", result.Content)
	}
	if !strings.Contains(output.Text, "app.go") {
		t.Fatalf("grep did not include non-ignored match:\n%s", output.Text)
	}
	if strings.Contains(output.Text, "dep.go") || strings.Contains(output.Text, "vendor") {
		t.Fatalf("grep did not honor ignore_globs:\n%s", output.Text)
	}
}

func TestGrepDisplaysMappedAliasPath(t *testing.T) {
	tempDir := t.TempDir()
	sourceDir := filepath.Join(t.TempDir(), "alias")
	file := filepath.Join(tempDir, "app.go")
	if err := os.WriteFile(file, []byte("package main\n// needle\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Load()
	cfg.PathMaps = []config.PathMap{
		{Source: sourceDir, Target: tempDir},
	}
	h := NewHandler(WithConfig(cfg))
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    sourceDir,
		Pattern: "needle",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", result.Content)
	}
	if !strings.Contains(output.Text, filepath.ToSlash(filepath.Join(sourceDir, "app.go"))+":2:// needle") {
		t.Fatalf("grep did not display mapped alias path:\n%s", output.Text)
	}
}

func TestGrepGlobSlashPatternIsRelativeToSearchRoot(t *testing.T) {
	tempDir := t.TempDir()
	nestedDir := filepath.Join(tempDir, "filetoolsserver", "handler")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "app.go"), []byte("package handler\n// needle\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    tempDir,
		Pattern: "needle",
		Glob:    "handler/*.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", result.Content)
	}
	if strings.Contains(output.Text, "app.go") {
		t.Fatalf("grep slash glob pattern should not match nested suffix outside search root:\n%s", output.Text)
	}

	result, output, err = h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    tempDir,
		Pattern: "needle",
		Glob:    "**/handler/*.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", result.Content)
	}
	if !strings.Contains(output.Text, "app.go:2:// needle") {
		t.Fatalf("grep recursive glob should match nested handler file:\n%s", output.Text)
	}
}

func TestGrepScansFullResultWithoutPageStop(t *testing.T) {
	tempDir := t.TempDir()
	var first strings.Builder
	for i := 0; i < 2000; i++ {
		first.WriteString("needle ")
		first.WriteString(strings.Repeat("x", 80))
		first.WriteString("\n")
	}
	if err := os.WriteFile(filepath.Join(tempDir, "a-many.log"), []byte(first.String()), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "z-later.log"), []byte("needle later\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(WithConfig(&config.Config{MemoryThreshold: 128}))
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    tempDir,
		Pattern: "needle",
		Limit:   intPtr(2500),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", result.Content)
	}
	if !strings.Contains(output.Text, "z-later.log") {
		t.Fatalf("grep did not include later matches after many earlier rows:\n%s", output.Text)
	}
}

func TestGrepCountModeEmitsOneRowPerFile(t *testing.T) {
	var emitted []textRow
	keepGoing, _, err := grepLineRowsForFile(
		context.Background(),
		"sample.log",
		[]string{"needle one", "noise", "needle two"},
		0,
		regexp.MustCompile("needle"),
		grepSearchOptions{Mode: "count"},
		func(row textRow) (bool, error) {
			emitted = append(emitted, row)
			return true, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !keepGoing {
		t.Fatal("count mode should keep scanning after emitting a file count row")
	}
	if len(emitted) != 1 || emitted[0].Body != "sample.log:2" {
		t.Fatalf("count mode emitted %#v, want one sample.log:2 row", emitted)
	}
}

func TestGrepLiteralPatternModeSearchesExactText(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(file, []byte("fooXbar\nfoo.bar\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:        file,
		Pattern:     "foo.bar",
		PatternMode: "literal",
		Limit:       intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("literal grep returned error: %#v", output)
	}
	if output.PatternMode != "literal" || output.MatchCount != 1 || len(output.Matches) != 1 || output.Matches[0].Line != 2 {
		t.Fatalf("literal grep should match only exact dotted text: %#v", output)
	}
	if output.SearchStats == nil || !output.SearchStats.Completed || !output.SearchStats.CountsAreComplete {
		t.Fatalf("literal grep should return complete stats: %#v", output.SearchStats)
	}
}

func TestGrepLineWindowPreservesOriginalLineNumbers(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(file, []byte("needle outside\nnoise\nneedle inside\nneedle outside\n"), 0644); err != nil {
		t.Fatal(err)
	}

	window := SourceLineRange{StartLine: 3, EndLine: 3}
	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:       file,
		Pattern:    "needle",
		LineWindow: &window,
		Limit:      intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("line_window grep returned error: %#v", output)
	}
	if output.LineWindow == nil || *output.LineWindow != window {
		t.Fatalf("line_window should be echoed on success: %#v", output.LineWindow)
	}
	if output.MatchCount != 1 || len(output.Matches) != 1 || output.Matches[0].Line != 3 {
		t.Fatalf("line_window should search only line 3 with original numbering: %#v", output.Matches)
	}
	if output.SearchStats == nil || output.SearchStats.FilesSeen != 1 || output.SearchStats.FilesSearched != 1 || !output.SearchStats.Completed {
		t.Fatalf("line_window stats should cover one searched file: %#v", output.SearchStats)
	}
}

func TestGrepMultilineLineWindowPastEOFIsNoMatch(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(file, []byte("one\ntwo\n"), 0644); err != nil {
		t.Fatal(err)
	}

	window := SourceLineRange{StartLine: 20, EndLine: 25}
	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:       file,
		Pattern:    "^$",
		Multiline:  true,
		LineWindow: &window,
		Limit:      intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("line_window past EOF grep returned error: %#v", output)
	}
	if output.MatchCount != 0 || len(output.Matches) != 0 || output.SearchStats == nil || output.SearchStats.FilesSearched != 1 || !output.SearchStats.Completed {
		t.Fatalf("line_window past EOF should be a complete no-match: %#v", output)
	}
}

func TestGrepMaxMatchesPerFileDistinguishesExactCapFromActualCap(t *testing.T) {
	tempDir := t.TempDir()
	exact := filepath.Join(tempDir, "exact.txt")
	capped := filepath.Join(tempDir, "capped.txt")
	if err := os.WriteFile(exact, []byte("needle one\nneedle two\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(capped, []byte("needle one\nneedle two\nneedle three\n"), 0644); err != nil {
		t.Fatal(err)
	}
	capTwo := 2

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:              exact,
		Pattern:           "needle",
		MaxMatchesPerFile: &capTwo,
		Limit:             intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.Truncated || output.SearchStats == nil || !output.SearchStats.Completed || output.SearchStats.FilesCapped != 0 {
		t.Fatalf("exact cap should remain complete and uncapped: result=%#v output=%#v", result, output)
	}

	result, output, err = h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:              capped,
		Pattern:           "needle",
		MaxMatchesPerFile: &capTwo,
		Limit:             intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("capped grep returned error: %#v", output)
	}
	if !output.Truncated || output.SearchStats == nil || output.SearchStats.StopReason != "file_cap" || output.SearchStats.FilesCapped != 1 || output.SearchStats.Completed {
		t.Fatalf("actual cap should mark incomplete file_cap stats: %#v", output)
	}
	if len(output.FileGroups) != 1 || !output.FileGroups[0].Capped || output.FileGroups[0].MatchCount != 2 {
		t.Fatalf("file group should show retained match count and capped=true: %#v", output.FileGroups)
	}
}

func TestGrepBinarySkipsDoNotMakeStatsIncomplete(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "text.txt"), []byte("plain text\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "binary.bin"), []byte{0, 1, 2, 3}, 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    tempDir,
		Pattern: "needle",
		Limit:   intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", output)
	}
	if output.SearchStats == nil || output.SearchStats.SkippedBinary != 1 || !output.SearchStats.Completed || !output.SearchStats.CountsAreComplete || output.SearchStats.StopReason != "" || output.Truncated {
		t.Fatalf("binary skip alone should not make grep incomplete: %#v", output.SearchStats)
	}
}

func TestGrepNoMatchRecommendsLiteralForRegexLookingPattern(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(file, []byte("a[bc]\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    file,
		Pattern: "a[bc]",
		Limit:   intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", output)
	}
	if output.MatchCount != 0 || output.NextRecommendedCall == nil {
		t.Fatalf("no-match regex-looking pattern should return a retry hint: %#v", output)
	}
	if output.NextRecommendedCall.RecommendedNextTool != "grep" || output.NextRecommendedCall.RecommendedNextInput["pattern_mode"] != "literal" {
		t.Fatalf("expected literal retry recommendation, got %#v", output.NextRecommendedCall)
	}
	if output.NextRecommendedCall.RecommendedNextInputPolicy != "retry_literal_pattern" {
		t.Fatalf("literal retry policy drifted: %#v", output.NextRecommendedCall)
	}
}

func TestGrepContentRecommendsReadFileForFirstRange(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(file, []byte("one\nneedle\ntwo\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    file,
		Pattern: "needle",
		Limit:   intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", output)
	}
	hint := output.NextRecommendedCall
	if hint == nil || hint.RecommendedNextTool != "read_file" || hint.RecommendedNextInputPolicy != "open_matched_range" {
		t.Fatalf("content grep should recommend read_file first range: %#v", hint)
	}
	if hint.RecommendedNextInput["target_file"] != output.FileGroups[0].Path || hint.RecommendedNextInput["start_line"] != 1 || hint.RecommendedNextInput["end_line"] != 4 {
		t.Fatalf("read_file recommendation should target first grouped range: %#v groups=%#v", hint.RecommendedNextInput, output.FileGroups)
	}
}

func TestGrepContentRecommendsReadFilesForBoundedGroupedRanges(t *testing.T) {
	tempDir := t.TempDir()
	aFile := filepath.Join(tempDir, "a.go")
	bFile := filepath.Join(tempDir, "b.go")
	if err := os.WriteFile(aFile, []byte("package main\n\nfunc Alpha() {\n\tprintln(\"needle\")\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bFile, []byte("package main\n\nfunc Beta() {\n\tprintln(\"needle\")\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    tempDir,
		Pattern: "needle",
		Limit:   intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", output)
	}
	if output.NextRecommendedCall == nil || output.NextRecommendedCall.RecommendedNextTool != "read_files" || output.NextRecommendedCall.RecommendedNextInputPolicy != "open_grouped_match_ranges" {
		t.Fatalf("bounded grouped grep should recommend read_files: %#v", output.NextRecommendedCall)
	}
	items, ok := output.NextRecommendedCall.RecommendedNextInput["items"].([]map[string]any)
	if !ok || len(items) != 2 {
		t.Fatalf("read_files recommendation should include two bounded items: %#v", output.NextRecommendedCall.RecommendedNextInput)
	}
	if len(output.NextRecommendedCalls) != 1 {
		t.Fatalf("multi-file grep should avoid noisy outline hints and keep read_files primary: %#v", output.NextRecommendedCalls)
	}
}

func TestGrepCwdContentRecommendationIsRelativeAndCarriesCwdIDAfterProjection(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(file, []byte("needle\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(WithConfig(cwdRegistryTestConfig(t)))
	cwdID := setCwdWithHandlerForTest(t, h, tempDir)
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		CwdAwareInput: CwdAwareInput{CwdID: CwdIDInput{Present: true, Value: cwdID}},
		Path:          "sample.txt",
		Pattern:       "needle",
		Limit:         intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("cwd grep returned error: %#v", output)
	}
	pathCtx, cwdErr := h.BuildPathContext(CwdIDInput{Present: true, Value: cwdID})
	if cwdErr != nil {
		t.Fatalf("build cwd context: %#v", cwdErr)
	}
	AttachCwdOutputMeta(&output, pathCtx)
	hint := output.NextRecommendedCall
	if hint == nil || hint.RecommendedNextTool != "read_file" {
		t.Fatalf("cwd grep should recommend read_file: %#v", hint)
	}
	if hint.RecommendedNextInput["target_file"] != "sample.txt" || hint.RecommendedNextInput["cwd_id"] != cwdID {
		t.Fatalf("cwd read_file recommendation should stay relative and include cwd_id: %#v", hint.RecommendedNextInput)
	}
}

func TestGrepCwdRetryRecommendationCarriesCwdID(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "sample.txt"), []byte("a[bc]\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(WithConfig(cwdRegistryTestConfig(t)))
	cwdID := setCwdWithHandlerForTest(t, h, tempDir)
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		CwdAwareInput: CwdAwareInput{CwdID: CwdIDInput{Present: true, Value: cwdID}},
		Path:          "sample.txt",
		Pattern:       "a[bc]",
		Limit:         intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || output.NextRecommendedCall == nil || output.NextRecommendedCall.RecommendedNextTool != "grep" {
		t.Fatalf("cwd no-match grep should recommend retry grep call: result=%#v output=%#v", result, output)
	}
	nextInput := output.NextRecommendedCall.RecommendedNextInput
	if got := nextInput["cwd_id"]; got != cwdID {
		t.Fatalf("cwd grep retry recommendation should carry cwd_id, got %#v in %#v", got, nextInput)
	}
	if got := nextInput["path"]; got != "sample.txt" {
		t.Fatalf("cwd grep retry recommendation should keep relative path, got %#v in %#v", got, nextInput)
	}
}

func TestGrepFileCapBroadScopeRecommendsFilesWithMatchesMapping(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "a.txt"), []byte("needle\nneedle\nneedle\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "b.txt"), []byte("needle\n"), 0644); err != nil {
		t.Fatal(err)
	}
	capOne := 1

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:              tempDir,
		Pattern:           "needle",
		MaxMatchesPerFile: &capOne,
		Limit:             intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", output)
	}
	hint := output.NextRecommendedCall
	if hint == nil || hint.RecommendedNextTool != "grep" || hint.RecommendedNextInputPolicy != "map_capped_grep_files" {
		t.Fatalf("file_cap should recommend mapping grep: %#v", hint)
	}
	if hint.RecommendedNextInput["output_mode"] != "files_with_matches" {
		t.Fatalf("file_cap mapping should switch to files_with_matches: %#v", hint.RecommendedNextInput)
	}
	if _, ok := hint.RecommendedNextInput["max_matches_per_file"]; ok {
		t.Fatalf("file_cap mapping should not preserve max_matches_per_file: %#v", hint.RecommendedNextInput)
	}
}

func TestGrepLimitDominatedByFirstFileRecommendsPerFileCap(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "a.txt"), []byte(strings.Repeat("needle\n", 9)), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "b.txt"), []byte(strings.Repeat("needle\n", 2)), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    tempDir,
		Pattern: "needle",
		Limit:   intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", output)
	}
	hint := output.NextRecommendedCall
	if hint == nil || hint.RecommendedNextTool != "grep" || hint.RecommendedNextInputPolicy != "narrow_truncated_grep" {
		t.Fatalf("limit should recommend narrow_truncated_grep: %#v", hint)
	}
	if _, ok := hint.RecommendedNextInput["max_matches_per_file"]; !ok {
		t.Fatalf("dominated limit should add max_matches_per_file: %#v", hint.RecommendedNextInput)
	}
	if _, ok := hint.RecommendedNextInput["output_mode"]; ok {
		t.Fatalf("dominated limit should stay in content mode, got %#v", hint.RecommendedNextInput)
	}
	if !strings.Contains(hint.Reason, "per-file cap") {
		t.Fatalf("dominated limit reason should explain the per-file cap: %q", hint.Reason)
	}
}

func TestGrepLimitWithExistingPerFileCapRecommendsFilesWithMatchesMapping(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "a.txt"), []byte(strings.Repeat("needle\n", 20)), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "b.txt"), []byte(strings.Repeat("needle\n", 2)), 0644); err != nil {
		t.Fatal(err)
	}
	capTwenty := 20

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:              tempDir,
		Pattern:           "needle",
		MaxMatchesPerFile: &capTwenty,
		Limit:             intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", output)
	}
	hint := output.NextRecommendedCall
	if hint == nil || hint.RecommendedNextTool != "grep" || hint.RecommendedNextInputPolicy != "narrow_truncated_grep" {
		t.Fatalf("limit should recommend narrow_truncated_grep: %#v", hint)
	}
	if hint.RecommendedNextInput["output_mode"] != "files_with_matches" {
		t.Fatalf("existing per-file cap should switch to files_with_matches mapping: %#v", hint.RecommendedNextInput)
	}
	if _, ok := hint.RecommendedNextInput["max_matches_per_file"]; ok {
		t.Fatalf("mapping recommendation should omit existing max_matches_per_file: %#v", hint.RecommendedNextInput)
	}
}

func TestGrepLimitNotDominatedRecommendsFilesWithMatchesMapping(t *testing.T) {
	tempDir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(tempDir, name), []byte(strings.Repeat("needle\n", 3)), 0644); err != nil {
			t.Fatal(err)
		}
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    tempDir,
		Pattern: "needle",
		Limit:   intPtr(5),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", output)
	}
	hint := output.NextRecommendedCall
	if hint == nil || hint.RecommendedNextTool != "grep" || hint.RecommendedNextInputPolicy != "narrow_truncated_grep" {
		t.Fatalf("limit should recommend narrow_truncated_grep: %#v", hint)
	}
	if hint.RecommendedNextInput["output_mode"] != "files_with_matches" {
		t.Fatalf("non-dominated limit should map files_with_matches: %#v", hint.RecommendedNextInput)
	}
	if _, ok := hint.RecommendedNextInput["max_matches_per_file"]; ok {
		t.Fatalf("non-dominated limit mapping should not add max_matches_per_file: %#v", hint.RecommendedNextInput)
	}
	if !strings.Contains(hint.Reason, "files_with_matches") {
		t.Fatalf("non-dominated limit reason should match the mapping recommendation: %q", hint.Reason)
	}
}

func TestGrepUnsafeCwdIgnoreGlobsOmitRecommendation(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(file, []byte("a[bc]\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(WithConfig(cwdRegistryTestConfig(t)))
	cwdID := setCwdWithHandlerForTest(t, h, tempDir)
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		CwdAwareInput: CwdAwareInput{CwdID: CwdIDInput{Present: true, Value: cwdID}},
		Path:          "sample.txt",
		Pattern:       "a[bc]",
		IgnoreGlobs:   []string{"D:/outside/**"},
		Limit:         intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("cwd grep returned error: %#v", output)
	}
	if output.NextRecommendedCall != nil {
		t.Fatalf("unsafe cwd ignore_globs should omit recommendation instead of broadening scope: %#v", output.NextRecommendedCall)
	}
}

func TestGrepIncompleteNoMatchDoesNotRecommendPatternRetry(t *testing.T) {
	h := NewHandler()
	output := GrepOutput{
		Pattern:     "a[bc]",
		PatternMode: "regex",
		Path:        "/repo",
		OutputMode:  "content",
		SearchStats: &GrepSearchStats{
			Completed:         false,
			CountsAreComplete: false,
			StopReason:        "unreadable",
		},
	}
	hint := h.grepNextRecommendedCall(PathContext{}, output, GrepToolInput{Pattern: "a[bc]"}, grepSearchOptions{Mode: "content", ResolvedRoot: "/repo"}, false)
	if hint != nil {
		t.Fatalf("incomplete no-match should not recommend literal/case retry: %#v", hint)
	}
}

func TestGrepDenseContentRecommendsOutlineWithStablePolicy(t *testing.T) {
	h := NewHandler()
	output := GrepOutput{
		Pattern:    "needle",
		Path:       "/repo",
		OutputMode: "content",
		FileGroups: []GrepFileGroup{
			{Path: "/repo/a.go", MatchCount: 8, RowCount: 8, FirstLine: 10, LastLine: 20},
		},
	}
	hint := h.grepNextRecommendedCall(PathContext{}, output, GrepToolInput{Pattern: "needle"}, grepSearchOptions{Mode: "content", ResolvedRoot: "/repo"}, true)
	if hint == nil || hint.RecommendedNextTool != "outline_file" || hint.RecommendedNextInputPolicy != "inspect_file_outline" {
		t.Fatalf("dense grep result should recommend inspect_file_outline: %#v", hint)
	}
}

func TestGrepDenseContentCwdRecommendationCarriesRelativePathAndCwdID(t *testing.T) {
	h := NewHandler()
	pathCtx := PathContext{
		HasCwd: true,
		CwdID:  42,
		CwdAbs: filepath.Clean("C:/repo"),
		CwdOut: "C:/repo",
	}
	output := GrepOutput{
		Pattern:    "needle",
		Path:       ".",
		OutputMode: "content",
		FileGroups: []GrepFileGroup{
			{Path: "src/a.go", MatchCount: 8, RowCount: 8, FirstLine: 10, LastLine: 20},
		},
	}
	hint := h.grepNextRecommendedCall(pathCtx, output, GrepToolInput{Pattern: "needle"}, grepSearchOptions{Mode: "content", ResolvedRoot: filepath.Clean("C:/repo")}, true)
	if hint == nil || hint.RecommendedNextTool != "outline_file" {
		t.Fatalf("dense cwd grep result should recommend outline_file: %#v", hint)
	}
	if got := hint.RecommendedNextInput["cwd_id"]; got != int64(42) {
		t.Fatalf("cwd recommendation should carry cwd_id, got %#v in %#v", got, hint.RecommendedNextInput)
	}
	if got := hint.RecommendedNextInput["target_file"]; got != "src/a.go" {
		t.Fatalf("cwd recommendation should keep relative target_file, got %#v in %#v", got, hint.RecommendedNextInput)
	}
}

func TestGrepExactlyWideContentRecommendsOutlineWithInclusiveSpan(t *testing.T) {
	h := NewHandler()
	output := GrepOutput{
		Pattern:    "needle",
		Path:       "/repo",
		OutputMode: "content",
		FileGroups: []GrepFileGroup{
			{Path: "/repo/a.go", MatchCount: 1, RowCount: 1, FirstLine: 10, LastLine: 129, ReadRanges: []SourceLineRange{{StartLine: 10, EndLine: 129}}},
		},
	}
	hint := h.grepNextRecommendedCall(PathContext{}, output, GrepToolInput{Pattern: "needle"}, grepSearchOptions{Mode: "content", ResolvedRoot: "/repo"}, true)
	if hint == nil || hint.RecommendedNextTool != "outline_file" || hint.RecommendedNextInputPolicy != "inspect_file_outline" {
		t.Fatalf("exact 120-line span should recommend inspect_file_outline: %#v", hint)
	}
}

func TestGrepContextOnlyLimitDoesNotBuildMatchNavigation(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(file, []byte("context\nneedle\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    file,
		Pattern: "needle",
		Before:  1,
		Limit:   intPtr(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", output)
	}
	if output.MatchCount != 0 || len(output.FileGroups) != 0 {
		t.Fatalf("context-only limited output should not expose match groups: match_count=%d groups=%#v", output.MatchCount, output.FileGroups)
	}
	if output.NextRecommendedCall != nil {
		t.Fatalf("context-only limited output should not recommend match navigation: %#v", output.NextRecommendedCall)
	}
	if strings.Contains(output.Message, "No matches found") || !strings.Contains(output.Message, "stop_reason=\"limit\"") {
		t.Fatalf("incomplete context-only output should not claim complete no-match: %q", output.Message)
	}
}

func TestGrepCaseInsensitiveRetrySkipsPathLikePattern(t *testing.T) {
	h := NewHandler()
	output := GrepOutput{
		Pattern:    "/tmp/path",
		Path:       "/repo",
		OutputMode: "content",
		SearchStats: &GrepSearchStats{
			Completed:         true,
			CountsAreComplete: true,
		},
	}
	hint := h.grepNextRecommendedCall(PathContext{}, output, GrepToolInput{Pattern: "/tmp/path"}, grepSearchOptions{Mode: "content", ResolvedRoot: "/repo"}, false)
	if hint != nil {
		t.Fatalf("path-like no-match pattern should not recommend case-insensitive retry: %#v", hint)
	}
}

func TestGrepReadRangesClampToLineWindow(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(file, []byte("one\ntwo\nneedle\nfour\nfive\n"), 0644); err != nil {
		t.Fatal(err)
	}

	window := SourceLineRange{StartLine: 3, EndLine: 3}
	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:       file,
		Pattern:    "needle",
		LineWindow: &window,
		Limit:      intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", output)
	}
	if len(output.FileGroups) != 1 || len(output.FileGroups[0].ReadRanges) != 1 {
		t.Fatalf("expected one grouped read range: %#v", output.FileGroups)
	}
	if got := output.FileGroups[0].ReadRanges[0]; got != window {
		t.Fatalf("read range should clamp to line_window, got %#v want %#v", got, window)
	}
}

func TestGrepReturnedContextRowsFeedReadRanges(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "sample.txt")
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "plain"
	}
	lines[9] = "needle"
	if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    file,
		Pattern: "needle",
		Context: 5,
		Limit:   intPtr(50),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", output)
	}
	if len(output.FileGroups) != 1 || len(output.FileGroups[0].ReadRanges) != 1 {
		t.Fatalf("expected one grouped read range: %#v", output.FileGroups)
	}
	want := SourceLineRange{StartLine: 5, EndLine: 15}
	if got := output.FileGroups[0].ReadRanges[0]; got != want {
		t.Fatalf("read range should include returned context rows, got %#v want %#v", got, want)
	}
	hint := output.NextRecommendedCall
	if hint == nil || hint.RecommendedNextTool != "read_file" || hint.RecommendedNextInput["start_line"] != want.StartLine || hint.RecommendedNextInput["end_line"] != want.EndLine {
		t.Fatalf("read_file recommendation should use context-fed range: %#v", hint)
	}
}

func TestGrepStatsFilesSeenExcludeHiddenIgnoredAndVCSFiles(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "visible.txt"), []byte("visible\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, ".hidden.txt"), []byte("hidden\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "ignored.tmp"), []byte("ignored\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(tempDir, ".git")
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte("git\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:        tempDir,
		Pattern:     "nomatch",
		IgnoreGlobs: []string{"*.tmp"},
		Limit:       intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", output)
	}
	stats := output.SearchStats
	if stats == nil {
		t.Fatal("grep should return search_stats")
	}
	if stats.FilesSeen != 1 || stats.SkippedHidden != 1 || stats.SkippedIgnored != 1 || stats.SkippedVCS != 1 {
		t.Fatalf("files_seen should count only traversal-visible candidates: %#v", stats)
	}
}

func TestGrepLargeFileStreamSetupFailureCountsUnreadable(t *testing.T) {
	h := NewHandler(WithConfig(&config.Config{MemoryThreshold: 128}))
	missing := filepath.Join(t.TempDir(), "missing.log")

	keepGoing, result, err := h.grepLargeFileRows(context.Background(), missing, missing, regexp.MustCompile("needle"), grepSearchOptions{Mode: "content"}, func(row textRow) (bool, error) {
		t.Fatalf("did not expect row emission for unreadable stream setup: %#v", row)
		return false, nil
	})
	if err != nil {
		t.Fatalf("stream setup failure should be reported as skipped_unreadable, got err=%v", err)
	}
	if !keepGoing || !result.SkippedUnreadable || result.Searched {
		t.Fatalf("unexpected large-file setup result: keepGoing=%v result=%#v", keepGoing, result)
	}
}

func TestGrepLargeSingleLineReturnsGuardError(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "huge-single-line.log")
	if err := os.WriteFile(file, []byte("needle"+strings.Repeat("x", 4096)), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(WithConfig(&config.Config{MemoryThreshold: 128}))
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    tempDir,
		Pattern: "needle",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStructuredToolError(t, result, output, "line exceeds MCP_MEMORY_THRESHOLD")
	msg := output.Error
	if !strings.Contains(msg, "line exceeds MCP_MEMORY_THRESHOLD") || !strings.Contains(msg, "read_file with start_line/end_line") {
		t.Fatalf("large single-line guard error is not actionable: %s", msg)
	}
}

func TestGrepMultilineLargeFileReturnsGuardError(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "large.txt")
	if err := os.WriteFile(file, []byte(strings.Repeat("line\n", 200)), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(WithConfig(&config.Config{MemoryThreshold: 128}))
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:      tempDir,
		Pattern:   "line\\nline",
		Multiline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStructuredToolError(t, result, output, "multiline grep requires loading")
	msg := output.Error
	if !strings.Contains(msg, "multiline grep requires loading") || !strings.Contains(msg, "MCP_MEMORY_THRESHOLD") {
		t.Fatalf("multiline guard error is not actionable: %s", msg)
	}
}

func TestGrepMultilineSkipsLargeBinaryBeforeGuard(t *testing.T) {
	tempDir := t.TempDir()
	binaryFile := filepath.Join(tempDir, "asset.bin")
	binaryContent := append([]byte{0, 1, 2, 3}, []byte(strings.Repeat("binary", 200))...)
	if err := os.WriteFile(binaryFile, binaryContent, 0644); err != nil {
		t.Fatal(err)
	}
	textFile := filepath.Join(tempDir, "small.txt")
	if err := os.WriteFile(textFile, []byte("start\nmiddle\nend\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(WithConfig(&config.Config{MemoryThreshold: 128}))
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:      tempDir,
		Pattern:   "start\\nmiddle",
		Multiline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep should skip large binary before multiline guard: %#v", result.Content)
	}
	if !strings.Contains(output.Text, "small.txt:1:start") || !strings.Contains(output.Text, "small.txt:2:middle") {
		t.Fatalf("grep did not return multiline text match while skipping large binary:\n%s", output.Text)
	}
}

func TestReadFileEmptyFileReportsZeroTotalLines(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "empty.txt")
	if err := os.WriteFile(file, nil, 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("read_file returned error: %#v", result.Content)
	}
	if output.Text != "" || output.TotalLines == nil || *output.TotalLines != 0 {
		t.Fatalf("read_file did not report zero total lines for empty file:\n%s", output.Text)
	}
}

func TestReadFileEmptyFileValidatesInvalidRanges(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "empty-ranges.txt")
	if err := os.WriteFile(file, nil, 0644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		startLine *int
		endLine   *int
		want      string
	}{
		{name: "bad_start", startLine: intPtr(0), want: "Invalid start_line"},
		{name: "bad_end", endLine: intPtr(0), want: "Invalid end_line"},
		{name: "bad_explicit_end", startLine: intPtr(1), endLine: intPtr(0), want: "Invalid end_line"},
		{name: "reversed", startLine: intPtr(2), endLine: intPtr(1), want: "cannot be greater"},
	}
	h := NewHandler()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{
				TargetFile: file,
				StartLine:  tt.startLine,
				EndLine:    tt.endLine,
			})
			if err != nil {
				t.Fatal(err)
			}
			assertStructuredToolError(t, result, output, tt.want)
		})
	}
}

func TestReadFileDisplaysTrailingEmptyLine(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "trailing-newline.txt")
	if err := os.WriteFile(file, []byte("alpha\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("read_file returned error: %#v", result.Content)
	}
	if !strings.Contains(output.Text, "2|\n") {
		t.Fatalf("read_file did not display trailing empty line:\n%s", output.Text)
	}
}

func TestReadFileLineRangeCanReadTrailingEmptyLine(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "trailing-empty-range.txt")
	if err := os.WriteFile(file, []byte("alpha\n"), 0644); err != nil {
		t.Fatal(err)
	}

	startLine := 2
	endLine := 2
	h := NewHandler()
	result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{
		TargetFile: file,
		StartLine:  &startLine,
		EndLine:    &endLine,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("read_file returned error: %#v", result.Content)
	}
	if output.Range == nil || output.Range.Start != 2 || output.Range.End != 2 || !strings.Contains(output.Text, "2|\n") {
		t.Fatalf("read_file range did not render trailing empty line:\n%s", output.Text)
	}
}

func TestReadFileStreamsLargeFileRange(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "large-range.txt")
	var b strings.Builder
	for i := 1; i <= 600; i++ {
		b.WriteString("line\n")
	}
	b.WriteString("target\n")
	for i := 0; i < 600; i++ {
		b.WriteString("line\n")
	}
	if err := os.WriteFile(file, []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}

	startLine := 601
	endLine := 601
	h := NewHandler(WithConfig(&config.Config{MemoryThreshold: 128}))
	result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{
		TargetFile: file,
		StartLine:  &startLine,
		EndLine:    &endLine,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("read_file returned error: %#v", result.Content)
	}
	if output.TotalLinesKnown ||
		output.RequestedRange == nil || output.RequestedRange.Start != 601 || output.RequestedRange.End != 601 ||
		output.Range == nil || output.Range.Start != 601 || output.Range.End != 601 ||
		!strings.Contains(output.Text, "601|target") {
		t.Fatalf("read_file did not stream requested large-file range:\n%s", output.Text)
	}
}

func TestReadFileReturnsHugeSingleLineWithSmallThreshold(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "huge-single-line.txt")
	line := strings.Repeat("x", 256*1024)
	if err := os.WriteFile(file, []byte(line), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(WithConfig(&config.Config{MemoryThreshold: 128}))
	result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("read_file returned error: %#v", result.Content)
	}
	if output.TotalLines == nil || *output.TotalLines != 1 || !strings.Contains(output.Text, "1|") {
		t.Fatalf("read_file huge single-line output missing expected line metadata:\n%s", output.Text)
	}
	if !strings.Contains(output.Text, line) {
		t.Fatalf("read_file huge single-line output did not include the complete line")
	}
}

func TestReadFileDoesNotSplitExactChunkLine(t *testing.T) {
	for _, size := range []int{2048, 4096} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			tempDir := t.TempDir()
			file := filepath.Join(tempDir, "exact-chunk.txt")
			line := strings.Repeat("x", size)
			if err := os.WriteFile(file, []byte(line), 0644); err != nil {
				t.Fatal(err)
			}

			h := NewHandler()
			result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{TargetFile: file})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError {
				t.Fatalf("read_file returned error: %#v", result.Content)
			}
			if got := strings.Count(output.Text, "1|"); got != 1 {
				t.Fatalf("read_file emitted %d line rows, want 1:\n%s", got, output.Text)
			}
			if !strings.Contains(output.Text, line) {
				t.Fatalf("read_file did not include complete exact chunk line")
			}
		})
	}
}

func TestReadFileLineRangeDoesNotSplitExactChunkLine(t *testing.T) {
	for _, size := range []int{2048, 4096} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			tempDir := t.TempDir()
			file := filepath.Join(tempDir, "exact-range-chunk.txt")
			line := strings.Repeat("x", size)
			if err := os.WriteFile(file, []byte(line), 0644); err != nil {
				t.Fatal(err)
			}

			startLine := 1
			endLine := 1
			h := NewHandler()
			result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{
				TargetFile: file,
				StartLine:  &startLine,
				EndLine:    &endLine,
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError {
				t.Fatalf("read_file returned error: %#v", result.Content)
			}
			if got := strings.Count(output.Text, "1|"); got != 1 {
				t.Fatalf("read_file range emitted %d line rows, want 1:\n%s", got, output.Text)
			}
			if !strings.Contains(output.Text, line) {
				t.Fatalf("read_file range did not include complete exact chunk line")
			}
		})
	}
}

func TestReadFileLineRangeUsesOriginalLineNumbers(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "range.txt")
	content := strings.Join([]string{"one", "two", "three", "four", "five"}, "\n")
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	startLine := 3
	endLine := 4
	h := NewHandler()
	result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{
		TargetFile: file,
		StartLine:  &startLine,
		EndLine:    &endLine,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("read_file returned error: %#v", result.Content)
	}
	if output.TotalLinesKnown ||
		output.RequestedRange == nil || output.RequestedRange.Start != 3 || output.RequestedRange.End != 4 ||
		output.Range == nil || output.Range.Start != 3 || output.Range.End != 4 ||
		!strings.Contains(output.Text, "3|three") ||
		!strings.Contains(output.Text, "4|four") {
		t.Fatalf("read_file did not render requested original line range:\\n%s", output.Text)
	}
	if strings.Contains(output.Text, "two") || strings.Contains(output.Text, "five") {
		t.Fatalf("read_file range leaked lines outside requested range:\\n%s", output.Text)
	}
}

func TestReadFileLineRangeLongLineKeepsOriginalLineNumbers(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "range-long-line.txt")
	longLine := "needle" + strings.Repeat("x", 12*1024)
	content := "first\n" + longLine + "\nthird"
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	startLine := 2
	endLine := 2
	h := NewHandler()
	result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{
		TargetFile: file,
		StartLine:  &startLine,
		EndLine:    &endLine,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("read_file returned error: %#v", result.Content)
	}
	if output.Range == nil || output.Range.Start != 2 || output.Range.End != 2 || !strings.Contains(output.Text, "2|needle") {
		t.Fatalf("read_file range used wrong line numbers:\\n%s", output.Text)
	}
	if !strings.Contains(output.Text, longLine) {
		t.Fatalf("read_file range did not include complete long line")
	}
}

func TestReadFileRangeReturnsCompleteLargeRange(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "many-lines.txt")
	var b strings.Builder
	for i := 1; i <= 300; i++ {
		b.WriteString("line ")
		b.WriteString(strconv.Itoa(i))
		b.WriteString(" ")
		b.WriteString(strings.Repeat("x", 80))
		b.WriteByte('\n')
	}
	if err := os.WriteFile(file, []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}

	startLine := 1
	endLine := 300
	h := NewHandler()
	result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{
		TargetFile: file,
		StartLine:  &startLine,
		EndLine:    &endLine,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("read_file returned error: %#v", result.Content)
	}
	if output.Range == nil || output.Range.Start != 1 || output.Range.End != 300 ||
		!strings.Contains(output.Text, "1|line 1 ") ||
		!strings.Contains(output.Text, "300|line 300 ") {
		t.Fatalf("read_file range did not include the complete requested range:\n%s", output.Text)
	}
}

func TestReadFileInvalidLineRangeReturnsFriendlyError(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "range-invalid.txt")
	if err := os.WriteFile(file, []byte("one\ntwo\nthree"), 0644); err != nil {
		t.Fatal(err)
	}

	startLine := 3
	endLine := 2
	h := NewHandler()
	result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{
		TargetFile: file,
		StartLine:  &startLine,
		EndLine:    &endLine,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStructuredToolError(t, result, output, "start_line")
	msg := output.Error
	if !strings.Contains(msg, "start_line") || !strings.Contains(msg, "end_line") {
		t.Fatalf("range error should mention start_line and end_line: %s", msg)
	}
}

func TestReadFileEndLineBeyondEOFClampsToFileEnd(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "short.txt")
	if err := os.WriteFile(file, []byte("one\ntwo\nthree"), 0644); err != nil {
		t.Fatal(err)
	}

	startLine := 1
	endLine := 80
	h := NewHandler()
	result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{
		TargetFile: file,
		StartLine:  &startLine,
		EndLine:    &endLine,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("read_file should clamp end_line beyond EOF instead of erroring: %#v output=%#v", result, output)
	}
	if output.Error != "" {
		t.Fatalf("read_file should not set structured error when clamping, got %q", output.Error)
	}
	if output.TotalLines == nil || *output.TotalLines != 3 ||
		output.Range == nil || output.Range.Start != 1 || output.Range.End != 3 ||
		!strings.Contains(output.Text, "3|three") {
		t.Fatalf("read_file did not clamp end_line to EOF:\n%s", output.Text)
	}
}

func TestReadFileStartLineBeyondEOFReturnsStructuredError(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "short-start.txt")
	if err := os.WriteFile(file, []byte("one\ntwo\nthree"), 0644); err != nil {
		t.Fatal(err)
	}

	startLine := 80
	h := NewHandler()
	result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{
		TargetFile: file,
		StartLine:  &startLine,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStructuredToolError(t, result, output, "beyond EOF")
	if !strings.Contains(output.Error, "total_lines=3") {
		t.Fatalf("read_file EOF error should include total lines, got %q", output.Error)
	}
	if output.File == "" || output.TotalLines == nil || *output.TotalLines != 3 || !output.TotalLinesKnown {
		t.Fatalf("read_file EOF error should include structured file and total lines, got %#v", output)
	}
}

func TestReadFileExplicitRangeBeyondEOFReturnsStructuredError(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "short-explicit.txt")
	if err := os.WriteFile(file, []byte("one\ntwo\nthree"), 0644); err != nil {
		t.Fatal(err)
	}

	startLine := 80
	endLine := 100
	h := NewHandler()
	result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{
		TargetFile: file,
		StartLine:  &startLine,
		EndLine:    &endLine,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStructuredToolError(t, result, output, "beyond EOF")
	if !strings.Contains(output.Error, "total_lines=3") {
		t.Fatalf("read_file explicit EOF error should include total lines, got %q", output.Error)
	}
	if output.TotalLines == nil || *output.TotalLines != 3 || !output.TotalLinesKnown || output.RequestedRange == nil || output.RequestedRange.Start != 80 || output.RequestedRange.End != 100 {
		t.Fatalf("read_file explicit EOF error should include structured total lines and requested range, got %#v", output)
	}
}

func TestReadFileEmptyFileRangeBeyondEOFReturnsStructuredError(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "empty.txt")
	if err := os.WriteFile(file, nil, 0644); err != nil {
		t.Fatal(err)
	}

	startLine := 80
	h := NewHandler()
	result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{
		TargetFile: file,
		StartLine:  &startLine,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStructuredToolError(t, result, output, "beyond EOF")
	if !strings.Contains(output.Error, "total_lines=0") {
		t.Fatalf("read_file empty EOF error should include zero total lines, got %q", output.Error)
	}
	if output.TotalLines == nil || *output.TotalLines != 0 || !output.TotalLinesKnown {
		t.Fatalf("read_file empty EOF error should include structured zero total lines, got %#v", output)
	}
}

func TestReadFileDisplaysMappedAliasPath(t *testing.T) {
	tempDir := t.TempDir()
	sourceDir := filepath.Join(t.TempDir(), "alias")
	file := filepath.Join(tempDir, "mapped.txt")
	if err := os.WriteFile(file, []byte("mapped display"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(WithConfig(&config.Config{
		PathMaps: []config.PathMap{
			{Source: sourceDir, Target: tempDir},
		},
	}))

	result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{TargetFile: filepath.Join(sourceDir, "mapped.txt")})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("read_file returned error: %#v output=%#v", result, output)
	}
	if output.File != filepath.ToSlash(filepath.Join(sourceDir, "mapped.txt")) {
		t.Fatalf("read_file did not display mapped alias path: %#v", output)
	}
}

func TestReadFileDoesNotRequireConfiguredRoot(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "outside-old-root.txt")
	if err := os.WriteFile(file, []byte("open access"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{TargetFile: file})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("read_file should not require a configured root: %#v", result.Content)
	}
	if !strings.Contains(output.Text, "open access") {
		t.Fatalf("read_file did not read absolute path without a configured root:\n%s", output.Text)
	}
}

func TestGlobFileSearchSupportsMultipleDoubleStarSegments(t *testing.T) {
	tempDir := t.TempDir()
	targetDir := filepath.Join(tempDir, "src", "fixtures", "unit")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatal(err)
	}
	targetFile := filepath.Join(targetDir, "case.go")
	if err := os.WriteFile(targetFile, []byte("package unit"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGlobFileSearch(context.Background(), nil, GlobFileSearchInput{
		TargetDirectory: tempDir,
		GlobPattern:     "**/fixtures/**/*.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("glob_file_search returned error: %#v", result.Content)
	}
	if !strings.Contains(output.Text, "case.go") {
		t.Fatalf("glob_file_search did not handle multiple ** segments:\n%s", output.Text)
	}
}

func TestGlobFileSearchIgnoreGlobsPrunesDirectories(t *testing.T) {
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "src")
	nodeModulesDir := filepath.Join(tempDir, "node_modules", "pkg")
	vendorDir := filepath.Join(tempDir, "vendor", "pkg")
	for _, dir := range []string{srcDir, nodeModulesDir, vendorDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(srcDir, "keep.go"), []byte("package src"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodeModulesDir, "noise.go"), []byte("package noise"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vendorDir, "noise.go"), []byte("package noise"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGlobFileSearch(context.Background(), nil, GlobFileSearchInput{
		TargetDirectory: tempDir,
		GlobPattern:     "*.go",
		IgnoreGlobs:     []string{"node_modules/**", "vendor/**"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("glob_file_search returned error: %#v", result.Content)
	}
	if !strings.Contains(output.Text, "keep.go") || strings.Contains(output.Text, "noise.go") {
		t.Fatalf("glob_file_search ignore_globs did not prune noisy directories:\n%s", output.Text)
	}
}

func TestGlobFileSearchInputSchemaExposesIgnoreGlobs(t *testing.T) {
	schema, err := jsonschema.For[GlobFileSearchInput](nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := schema.Properties["ignore_globs"]; !ok {
		t.Fatalf("glob_file_search schema is missing ignore_globs; properties: %#v", schema.Properties)
	}
}

func TestListDirIgnoreGlobsSkipDirectorySelfForDoubleStar(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tempDir, "node_modules"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "keep.txt"), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleListDir(context.Background(), nil, ListDirInput{
		TargetDirectory: tempDir,
		IgnoreGlobs:     []string{"node_modules/**"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("list_dir returned error: %#v", result.Content)
	}
	if strings.Contains(output.Text, "node_modules") || !strings.Contains(output.Text, "keep.txt") {
		t.Fatalf("list_dir ignore_globs did not filter directory self correctly:\n%s", output.Text)
	}
}

func TestListDirIgnoreGlobsDoNotUseSubstringFallback(t *testing.T) {
	tempDir := t.TempDir()
	for _, dir := range []string{"vendor", "vendor_backup", "myvendor"} {
		if err := os.MkdirAll(filepath.Join(tempDir, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}

	h := NewHandler()
	result, output, err := h.HandleListDir(context.Background(), nil, ListDirInput{
		TargetDirectory: tempDir,
		IgnoreGlobs:     []string{"vendor/**"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("list_dir returned error: %#v", result.Content)
	}
	if strings.Contains(output.Text, "\n  - vendor/...") {
		t.Fatalf("list_dir did not ignore exact vendor directory:\n%s", output.Text)
	}
	if !strings.Contains(output.Text, "vendor_backup/...") || !strings.Contains(output.Text, "myvendor/...") {
		t.Fatalf("list_dir ignored non-matching substring directories:\n%s", output.Text)
	}
}

func TestListDirEmptyOrFullyFilteredReturnsFriendlyMessage(t *testing.T) {
	tempDir := t.TempDir()
	h := NewHandler()

	result, output, err := h.HandleListDir(context.Background(), nil, ListDirInput{TargetDirectory: tempDir})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("list_dir empty directory returned error: %#v", result.Content)
	}
	if !strings.Contains(output.Text, "empty or all entries were filtered") {
		t.Fatalf("list_dir empty directory did not return friendly message:\n%s", output.Text)
	}

	if err := os.WriteFile(filepath.Join(tempDir, "skip.tmp"), []byte("skip"), 0644); err != nil {
		t.Fatal(err)
	}
	result, output, err = h.HandleListDir(context.Background(), nil, ListDirInput{
		TargetDirectory: tempDir,
		IgnoreGlobs:     []string{"*.tmp"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("list_dir fully filtered directory returned error: %#v", result.Content)
	}
	if !strings.Contains(output.Text, "empty or all entries were filtered") {
		t.Fatalf("list_dir fully filtered directory did not return friendly message:\n%s", output.Text)
	}
}

func TestListDirDisplaysMappedAliasPath(t *testing.T) {
	tempDir := t.TempDir()
	sourceDir := filepath.Join(t.TempDir(), "alias")
	if err := os.WriteFile(filepath.Join(tempDir, "child.txt"), []byte("child"), 0644); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(WithConfig(&config.Config{
		PathMaps: []config.PathMap{
			{Source: sourceDir, Target: tempDir},
		},
	}))

	result, output, err := h.HandleListDir(context.Background(), nil, ListDirInput{TargetDirectory: sourceDir})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("list_dir returned error: %#v output=%#v", result, output)
	}
	if !strings.HasPrefix(output.Text, listDirHeaderLine(filepath.ToSlash(sourceDir))) {
		t.Fatalf("list_dir did not display mapped alias path header:\n%s", output.Text)
	}
	if !strings.Contains(output.Text, "  - child.txt") {
		t.Fatalf("list_dir missing child entry:\n%s", output.Text)
	}
}

func TestListDirHeaderPreservesPOSIXRoot(t *testing.T) {
	if got := listDirHeaderLine("/"); got != "/\n" {
		t.Fatalf("list_dir header for POSIX root = %q, want /\\n", got)
	}
	if got := listDirHeaderLine("."); got != "./\n" {
		t.Fatalf("list_dir header for current directory = %q, want ./\\n", got)
	}
	if got := listDirHeaderLine(`D:\repo`); got != "D:/repo/\n" {
		t.Fatalf("list_dir header for Windows path = %q, want D:/repo/\\n", got)
	}
}

func TestGrepMultilineIncludesContextRows(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "multi.txt")
	content := "before\nstart\nmiddle\nend\nafter\n"
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:      tempDir,
		Pattern:   "start\nmiddle\nend",
		Context:   1,
		Multiline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", result.Content)
	}
	if !strings.Contains(output.Text, ":1-before") ||
		!strings.Contains(output.Text, ":2:start") ||
		!strings.Contains(output.Text, ":3:middle") ||
		!strings.Contains(output.Text, ":4:end") ||
		!strings.Contains(output.Text, ":5-after") {
		t.Fatalf("grep multiline output did not include expected context rows:\n%s", output.Text)
	}
	if output.MatchCount != 1 || output.RowCount != 5 {
		t.Fatalf("grep multiline should count logical regex matches, not match rows: %#v", output)
	}
}

func TestGrepMultilineWithoutContextKeepsDefaultReadRangeExpansion(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "multi.txt")
	content := strings.Join([]string{"one", "two", "start", "middle-a", "middle-b", "middle-c", "end", "eight", "nine", "ten"}, "\n") + "\n"
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:      tempDir,
		Pattern:   "start\nmiddle-a\nmiddle-b\nmiddle-c\nend",
		Multiline: true,
		Limit:     intPtr(20),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", result.Content)
	}
	if len(output.FileGroups) != 1 || len(output.FileGroups[0].ReadRanges) != 1 {
		t.Fatalf("expected one grouped read range: %#v", output.FileGroups)
	}
	want := SourceLineRange{StartLine: 1, EndLine: 9}
	if got := output.FileGroups[0].ReadRanges[0]; got != want {
		t.Fatalf("multiline match rows without context should include returned match rows plus default expansion, got %#v want %#v", got, want)
	}
}

func TestGrepMultilineFullFileSeesTrailingEmptyLineAnchor(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "trailing-empty.txt")
	if err := os.WriteFile(file, []byte("noise\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:      file,
		Pattern:   "(?m)^$",
		Multiline: true,
		Limit:     intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", output)
	}
	if output.MatchCount != 1 || output.RowCount != 1 || len(output.Matches) != 1 {
		t.Fatalf("trailing empty display line should produce exactly one match row: %#v", output)
	}
	if got := output.Matches[0].Line; got != 2 {
		t.Fatalf("trailing empty display line should be anchored to line 2, got line %d", got)
	}
	if len(output.FileGroups) != 1 || len(output.FileGroups[0].ReadRanges) != 1 {
		t.Fatalf("expected one grouped read range: %#v", output.FileGroups)
	}
	wantRange := SourceLineRange{StartLine: 1, EndLine: 4}
	if got := output.FileGroups[0].ReadRanges[0]; got != wantRange {
		t.Fatalf("trailing empty display line read range = %#v, want %#v", got, wantRange)
	}
}

func TestGrepMultilineAnchorsUseFullStringSemantics(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "anchors.txt")
	if err := os.WriteFile(file, []byte("alpha\nbeta\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:      file,
		Pattern:   "(?m)^",
		Multiline: true,
		Limit:     intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", output)
	}
	if output.MatchCount != 3 || output.RowCount != 3 || len(output.Matches) != 3 {
		t.Fatalf("multiline start anchors should match only display line starts: %#v", output)
	}
	for i, match := range output.Matches {
		if wantLine := i + 1; match.Line != wantLine {
			t.Fatalf("match %d anchored to line %d, want %d: %#v", i, match.Line, wantLine, output.Matches)
		}
	}
}

func TestGrepMultilineAnchorCountUsesFullStringSemantics(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "anchors.txt")
	if err := os.WriteFile(file, []byte("alpha\nbeta\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:       file,
		Pattern:    "(?m)^",
		OutputMode: "count",
		Multiline:  true,
		Limit:      intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", output)
	}
	if output.MatchCount != 3 || output.RowCount != 1 || len(output.Counts) != 1 || output.Counts[0].Count != 3 {
		t.Fatalf("multiline anchor count should use full-string regexp semantics: %#v", output)
	}
}

func TestGrepMultilineCountEmitsOneRowPerFile(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "multi-count.txt")
	content := strings.Repeat("start\nend\n", 50)
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:       tempDir,
		Pattern:    "start\nend",
		OutputMode: "count",
		Multiline:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", result.Content)
	}
	if !strings.Contains(output.Text, "multi-count.txt:50") {
		t.Fatalf("grep multiline count did not emit one file count row:\n%s", output.Text)
	}
	if strings.Count(output.Text, "multi-count.txt:") != 1 {
		t.Fatalf("grep multiline count should not emit per-match rows:\n%s", output.Text)
	}
}

func TestGrepDirectoryTraversalSkipsHiddenFilesByDefault(t *testing.T) {
	tempDir := t.TempDir()
	hiddenFile := filepath.Join(tempDir, ".env.example")
	if err := os.WriteFile(hiddenFile, []byte("HIDDEN_NEEDLE=true"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    tempDir,
		Pattern: "HIDDEN_NEEDLE",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", result.Content)
	}
	if strings.Contains(output.Text, ".env.example") || strings.Contains(output.Text, "HIDDEN_NEEDLE=true") {
		t.Fatalf("grep should skip hidden files during directory traversal:\n%s", output.Text)
	}
}

func TestGrepExplicitHiddenFilePathIsAllowed(t *testing.T) {
	tempDir := t.TempDir()
	hiddenFile := filepath.Join(tempDir, ".env.example")
	if err := os.WriteFile(hiddenFile, []byte("HIDDEN_NEEDLE=true"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    hiddenFile,
		Pattern: "HIDDEN_NEEDLE",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", result.Content)
	}
	if !strings.Contains(output.Text, ".env.example") || !strings.Contains(output.Text, "HIDDEN_NEEDLE=true") {
		t.Fatalf("grep should search an explicitly requested hidden file:\n%s", output.Text)
	}
}

func TestGrepDirectoryTraversalPrunesGitMetadata(t *testing.T) {
	tempDir := t.TempDir()
	gitDir := filepath.Join(tempDir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte("SECRET_NEEDLE=from-git\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, ".env.example"), []byte("SECRET_NEEDLE=from-hidden-file\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "visible.txt"), []byte("SECRET_NEEDLE=from-visible-file\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    tempDir,
		Pattern: "SECRET_NEEDLE",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", result.Content)
	}
	if strings.Contains(output.Text, ".git") || strings.Contains(output.Text, "from-git") {
		t.Fatalf("grep should prune .git metadata by default:\n%s", output.Text)
	}
	if strings.Contains(output.Text, ".env.example") || strings.Contains(output.Text, "from-hidden-file") {
		t.Fatalf("grep should skip hidden working-tree files during directory traversal:\n%s", output.Text)
	}
	if !strings.Contains(output.Text, "visible.txt") || !strings.Contains(output.Text, "from-visible-file") {
		t.Fatalf("grep should still include visible working-tree files:\n%s", output.Text)
	}
}

func TestGrepFindsUTF16LETextWithBOM(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "utf16.txt")
	content := []byte{0xFF, 0xFE}
	for _, r := range "needle utf16\n" {
		content = append(content, byte(r), 0x00)
	}
	if err := os.WriteFile(file, content, 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    tempDir,
		Pattern: "needle",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", result.Content)
	}
	if !strings.Contains(output.Text, "utf16.txt:1:") || !strings.Contains(output.Text, "needle utf16") {
		t.Fatalf("grep did not find UTF-16 LE text with BOM:\n%s", output.Text)
	}
}

func TestGrepFindsUTF32LETextWithBOM(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "utf32le.txt")
	if err := os.WriteFile(file, utf32Bytes("needle utf32le\n", true), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    tempDir,
		Pattern: "needle",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", result.Content)
	}
	if !strings.Contains(output.Text, "utf32le.txt:1:") || !strings.Contains(output.Text, "needle utf32le") {
		t.Fatalf("grep did not find UTF-32 LE text with BOM:\n%s", output.Text)
	}
}

func TestGrepFindsUTF32BETextWithBOM(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "utf32be.txt")
	if err := os.WriteFile(file, utf32Bytes("needle utf32be\n", false), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGrepTool(context.Background(), nil, GrepToolInput{
		Path:    tempDir,
		Pattern: "needle",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("grep returned error: %#v", result.Content)
	}
	if !strings.Contains(output.Text, "utf32be.txt:1:") || !strings.Contains(output.Text, "needle utf32be") {
		t.Fatalf("grep did not find UTF-32 BE text with BOM:\n%s", output.Text)
	}
}

func TestReadFileReadsUTF32LETextWithBOMAndRange(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "utf32le.txt")
	if err := os.WriteFile(file, utf32Bytes("first\nneedle utf32le\nthird\n", true), 0644); err != nil {
		t.Fatal(err)
	}

	startLine := 2
	endLine := 2
	h := NewHandler()
	result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{
		TargetFile: file,
		StartLine:  &startLine,
		EndLine:    &endLine,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("read_file returned error: %#v", result.Content)
	}
	if !strings.Contains(output.Text, "2|needle utf32le") {
		t.Fatalf("read_file did not decode UTF-32 LE range with line numbers:\n%s", output.Text)
	}
	if strings.Contains(output.Text, "first") || strings.Contains(output.Text, "third") {
		t.Fatalf("read_file UTF-32 LE range leaked lines outside requested range:\n%s", output.Text)
	}
}

func TestReadFileReadsUTF32BETextWithBOMAndRange(t *testing.T) {
	tempDir := t.TempDir()
	file := filepath.Join(tempDir, "utf32be.txt")
	if err := os.WriteFile(file, utf32Bytes("first\nneedle utf32be\nthird\n", false), 0644); err != nil {
		t.Fatal(err)
	}

	startLine := 2
	endLine := 2
	h := NewHandler()
	result, output, err := h.HandleReadFile(context.Background(), nil, ReadFileInput{
		TargetFile: file,
		StartLine:  &startLine,
		EndLine:    &endLine,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("read_file returned error: %#v", result.Content)
	}
	if !strings.Contains(output.Text, "2|needle utf32be") {
		t.Fatalf("read_file did not decode UTF-32 BE range with line numbers:\n%s", output.Text)
	}
	if strings.Contains(output.Text, "first") || strings.Contains(output.Text, "third") {
		t.Fatalf("read_file UTF-32 BE range leaked lines outside requested range:\n%s", output.Text)
	}
}

func utf32Bytes(text string, littleEndian bool) []byte {
	var content []byte
	if littleEndian {
		content = []byte{0xFF, 0xFE, 0x00, 0x00}
	} else {
		content = []byte{0x00, 0x00, 0xFE, 0xFF}
	}
	for _, r := range text {
		codePoint := uint32(r)
		if littleEndian {
			content = append(content, byte(codePoint), byte(codePoint>>8), byte(codePoint>>16), byte(codePoint>>24))
		} else {
			content = append(content, byte(codePoint>>24), byte(codePoint>>16), byte(codePoint>>8), byte(codePoint))
		}
	}
	return content
}

func TestGlobFileSearchSkipsHiddenFiles(t *testing.T) {
	tempDir := t.TempDir()
	hiddenFile := filepath.Join(tempDir, ".hidden.go")
	if err := os.WriteFile(hiddenFile, []byte("package hidden"), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewHandler()
	result, output, err := h.HandleGlobFileSearch(context.Background(), nil, GlobFileSearchInput{
		TargetDirectory: tempDir,
		GlobPattern:     "*.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("glob_file_search returned error: %#v", result.Content)
	}
	if strings.Contains(output.Text, ".hidden.go") {
		t.Fatalf("glob_file_search should skip hidden files by default:\n%s", output.Text)
	}
}
