package catalog

import (
	"bytes"
	"encoding/json"
	"testing"
	"unicode/utf8"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

const (
	rawCatalogMaxBytes        = 12_000
	codexCatalogMaxCharacters = 10_000
)

type budgetAnnotations struct {
	Title           string `json:"title"`
	ReadOnlyHint    bool   `json:"readOnlyHint"`
	DestructiveHint bool   `json:"destructiveHint"`
	IdempotentHint  bool   `json:"idempotentHint"`
	OpenWorldHint   bool   `json:"openWorldHint"`
}

type budgetTool struct {
	Name         api.ToolName      `json:"name"`
	Description  string            `json:"description"`
	InputSchema  json.RawMessage   `json:"inputSchema"`
	OutputSchema json.RawMessage   `json:"outputSchema,omitempty"`
	Annotations  budgetAnnotations `json:"annotations"`
}

func TestCatalogBudgets(t *testing.T) {
	tools := budgetTools(t)
	raw := rawToolsListJSON(t, tools)
	if len(raw) > rawCatalogMaxBytes {
		t.Fatalf("raw tools/list = %d bytes, limit %d", len(raw), rawCatalogMaxBytes)
	}
	if bytes.Count(raw, []byte(`"outputSchema"`)) != 1 {
		t.Fatal("raw tools/list must contain exactly one outputSchema")
	}

	codexFacing := codexFacingDefinitions(t, tools)
	characters := utf8.RuneCount(codexFacing)
	if characters > codexCatalogMaxCharacters {
		t.Fatalf("Codex-facing definitions = %d characters, limit %d", characters, codexCatalogMaxCharacters)
	}

	t.Logf("catalog budgets: raw=%d/%d bytes codex=%d/%d characters", len(raw), rawCatalogMaxBytes, characters, codexCatalogMaxCharacters)
}

func budgetTools(t *testing.T) []budgetTool {
	t.Helper()
	definitions := Ordered()
	tools := make([]budgetTool, len(definitions))
	for i, definition := range definitions {
		if !json.Valid(definition.InputSchema) {
			t.Fatalf("Ordered()[%d].InputSchema is invalid JSON", i)
		}
		if len(definition.OutputSchema) != 0 && !json.Valid(definition.OutputSchema) {
			t.Fatalf("Ordered()[%d].OutputSchema is invalid JSON", i)
		}
		tools[i] = budgetTool{
			Name:         definition.Name,
			Description:  definition.Description,
			InputSchema:  append(json.RawMessage(nil), definition.InputSchema...),
			OutputSchema: append(json.RawMessage(nil), definition.OutputSchema...),
			Annotations: budgetAnnotations{
				Title:           definition.Title,
				ReadOnlyHint:    definition.ReadOnly,
				DestructiveHint: definition.Destructive,
				IdempotentHint:  definition.Idempotent,
				OpenWorldHint:   definition.OpenWorld,
			},
		}
	}
	return tools
}

func rawToolsListJSON(t *testing.T, tools []budgetTool) []byte {
	t.Helper()
	message := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  struct {
			Tools []budgetTool `json:"tools"`
		} `json:"result"`
	}{
		JSONRPC: "2.0",
		ID:      2,
	}
	message.Result.Tools = tools
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("marshal tools/list: %v", err)
	}
	return append(raw, '\n')
}

func codexFacingDefinitions(t *testing.T, tools []budgetTool) []byte {
	t.Helper()
	var combined bytes.Buffer
	for i, tool := range tools {
		raw, err := json.Marshal(tool)
		if err != nil {
			t.Fatalf("marshal Codex-facing definition %d: %v", i, err)
		}
		combined.WriteString(Instructions)
		combined.WriteByte('\n')
		combined.Write(raw)
		combined.WriteByte('\n')
	}
	return combined.Bytes()
}
