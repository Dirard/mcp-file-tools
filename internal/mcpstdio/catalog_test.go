package mcpstdio

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/catalog"
)

func TestToolsListCatalogMatchesExactRawWireGolden(t *testing.T) {
	result, err := toolsListResultJSON()
	if err != nil {
		t.Fatalf("toolsListResultJSON() error = %v", err)
	}
	got := []byte(`{"jsonrpc":"2.0","id":2,"result":` + result + `}` + "\n")
	want, err := os.ReadFile("testdata/tools-list.golden")
	if err != nil {
		t.Fatalf("read tools-list golden: %v\nactual bytes:\n%s", err, got)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("tools/list raw wire differs from golden\n got: %s\nwant: %s", got, want)
	}
	if len(got) > 12_000 {
		t.Fatalf("tools/list raw wire = %d bytes, limit 12000", len(got))
	}
	for _, forbidden := range [][]byte{
		[]byte(`"outputSchema"`),
		[]byte(`"nextCursor"`),
		[]byte(`"execution"`),
		[]byte(`"icons"`),
	} {
		if bytes.Contains(got, forbidden) {
			t.Fatalf("tools/list contains forbidden field %s", forbidden)
		}
	}
}

func TestToolsListCatalogUsesExactOrderAndAnnotations(t *testing.T) {
	result, err := toolsListResultJSON()
	if err != nil {
		t.Fatalf("toolsListResultJSON() error = %v", err)
	}
	var decoded struct {
		Tools []struct {
			Name        api.ToolName    `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
			Annotations struct {
				Title           string `json:"title"`
				ReadOnlyHint    bool   `json:"readOnlyHint"`
				DestructiveHint bool   `json:"destructiveHint"`
				IdempotentHint  bool   `json:"idempotentHint"`
				OpenWorldHint   bool   `json:"openWorldHint"`
			} `json:"annotations"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(result), &decoded); err != nil {
		t.Fatalf("decode cached tools/list result: %v", err)
	}
	definitions := catalog.Ordered()
	if len(decoded.Tools) != len(definitions) {
		t.Fatalf("tool count = %d, want %d", len(decoded.Tools), len(definitions))
	}
	for index, definition := range definitions {
		tool := decoded.Tools[index]
		if tool.Name != definition.Name || tool.Description != definition.Description || !bytes.Equal(tool.InputSchema, definition.InputSchema) {
			t.Fatalf("tool %d catalog fields differ: %#v", index, tool)
		}
		if tool.Annotations.Title != definition.Title ||
			tool.Annotations.ReadOnlyHint != definition.ReadOnly ||
			tool.Annotations.DestructiveHint != definition.Destructive ||
			tool.Annotations.IdempotentHint != definition.Idempotent ||
			tool.Annotations.OpenWorldHint != definition.OpenWorld {
			t.Fatalf("tool %d annotations differ: %#v", index, tool.Annotations)
		}
	}
	if got := [4]api.ToolName{decoded.Tools[0].Name, decoded.Tools[1].Name, decoded.Tools[2].Name, decoded.Tools[3].Name}; got != api.OrderedToolNames() {
		t.Fatalf("tool order = %v", got)
	}
}

func TestToolsListCatalogCacheIsImmutableAndIDIndependent(t *testing.T) {
	first, err := toolsListResultJSON()
	if err != nil {
		t.Fatalf("first toolsListResultJSON() error = %v", err)
	}
	mutated := catalog.Ordered()
	for index := range mutated {
		mutated[index].Name = "mutated"
		mutated[index].Description = "mutated"
		for byteIndex := range mutated[index].InputSchema {
			mutated[index].InputSchema[byteIndex] ^= 0xff
		}
	}
	second, err := toolsListResultJSON()
	if err != nil {
		t.Fatalf("second toolsListResultJSON() error = %v", err)
	}
	if first != second {
		t.Fatal("cached tools/list result changed after caller-owned catalog mutation")
	}
	if bytes.Contains([]byte(first), []byte(`"jsonrpc"`)) || bytes.Contains([]byte(first), []byte(`"id"`)) {
		t.Fatalf("cached result contains a response envelope: %s", first)
	}
}
