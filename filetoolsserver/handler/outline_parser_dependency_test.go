package handler

import (
	"strings"
	"testing"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

func TestOutlineParserDependencySmoke(t *testing.T) {
	tests := []struct {
		name     string
		language *gotreesitter.Language
		source   string
		marker   string
	}{
		{
			name:     "javascript",
			language: grammars.JavascriptLanguage(),
			source:   "export function loadConfig() {\n  return {ok: true};\n}\n",
			marker:   "loadConfig",
		},
		{
			name:     "typescript",
			language: grammars.TypescriptLanguage(),
			source:   "type Config = { enabled: boolean }\nexport class Loader {\n  loadConfig(): Config { return { enabled: true } }\n}\n",
			marker:   "Loader",
		},
		{
			name:     "tsx",
			language: grammars.TsxLanguage(),
			source:   "export function Widget() {\n  return <section>{'ready'}</section>;\n}\n",
			marker:   "Widget",
		},
		{
			name:     "python",
			language: grammars.PythonLanguage(),
			source:   "class Loader:\n    def load_config(self):\n        return {'ok': True}\n",
			marker:   "load_config",
		},
		{
			name:     "json",
			language: grammars.JsonLanguage(),
			source:   "{\n  \"service\": {\n    \"enabled\": true\n  }\n}\n",
			marker:   "service",
		},
		{
			name:     "yaml",
			language: grammars.YamlLanguage(),
			source:   "service:\n  enabled: true\n",
			marker:   "service",
		},
		{
			name:     "svelte",
			language: grammars.SvelteLanguage(),
			source:   "<script>\n  export let loadConfig = true;\n</script>\n<section>{loadConfig}</section>\n",
			marker:   "loadConfig",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.language == nil {
				t.Fatalf("%s language loader returned nil", tt.name)
			}
			source := []byte(tt.source)
			parser := gotreesitter.NewParser(tt.language)
			tree, err := parser.Parse(source)
			if err != nil {
				t.Fatalf("parse %s: %v", tt.name, err)
			}
			root := tree.RootNode()
			if root == nil {
				t.Fatalf("%s parse returned nil root", tt.name)
			}
			if root.HasError() {
				t.Fatalf("%s root has parse error: %s", tt.name, root.SExpr(tt.language))
			}
			if root.StartByte() != 0 || root.EndByte() != uint32(len(source)) {
				t.Fatalf("%s root byte range = %d..%d, want 0..%d", tt.name, root.StartByte(), root.EndByte(), len(source))
			}

			node := smallestNamedNodeContaining(root, tt.language, source, tt.marker)
			if node == nil {
				t.Fatalf("%s parse did not expose a named node containing marker %q; root=%s", tt.name, tt.marker, root.SExpr(tt.language))
			}
			text := node.Text(source)
			if !strings.Contains(text, tt.marker) {
				t.Fatalf("%s selected node text %q does not contain marker %q", tt.name, text, tt.marker)
			}
			if node.StartByte() >= node.EndByte() || node.EndByte() > uint32(len(source)) {
				t.Fatalf("%s selected node has invalid byte range %d..%d for source length %d", tt.name, node.StartByte(), node.EndByte(), len(source))
			}
			if string(source[node.StartByte():node.EndByte()]) != text {
				t.Fatalf("%s node byte range does not round-trip to node text", tt.name)
			}
			startLine := zeroBasedLineForByte(source, node.StartByte())
			endLine := zeroBasedLineForByte(source, maxUint32(node.EndByte(), 1)-1)
			if node.StartPoint().Row != startLine || node.EndPoint().Row < endLine {
				t.Fatalf("%s node points %v..%v do not match byte-derived lines %d..%d", tt.name, node.StartPoint(), node.EndPoint(), startLine, endLine)
			}
		})
	}
}

func smallestNamedNodeContaining(node *gotreesitter.Node, lang *gotreesitter.Language, source []byte, marker string) *gotreesitter.Node {
	var best *gotreesitter.Node
	var walk func(*gotreesitter.Node)
	walk = func(current *gotreesitter.Node) {
		if current == nil {
			return
		}
		if current.IsNamed() && strings.Contains(current.Text(source), marker) {
			if best == nil || current.EndByte()-current.StartByte() < best.EndByte()-best.StartByte() {
				best = current
			}
		}
		for i := 0; i < current.ChildCount(); i++ {
			walk(current.Child(i))
		}
	}
	walk(node)
	return best
}

func zeroBasedLineForByte(source []byte, offset uint32) uint32 {
	if offset > uint32(len(source)) {
		offset = uint32(len(source))
	}
	var line uint32
	for i := uint32(0); i < offset; i++ {
		if source[i] == '\n' {
			line++
		}
	}
	return line
}

func maxUint32(value, floor uint32) uint32 {
	if value < floor {
		return floor
	}
	return value
}
