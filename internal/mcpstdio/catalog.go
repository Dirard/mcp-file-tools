package mcpstdio

import (
	"encoding/json"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/catalog"
)

type toolsListAnnotationsJSON struct {
	Title           string `json:"title"`
	ReadOnlyHint    bool   `json:"readOnlyHint"`
	DestructiveHint bool   `json:"destructiveHint"`
	IdempotentHint  bool   `json:"idempotentHint"`
	OpenWorldHint   bool   `json:"openWorldHint"`
}

type toolsListToolJSON struct {
	Name        api.ToolName             `json:"name"`
	Description string                   `json:"description"`
	InputSchema json.RawMessage          `json:"inputSchema"`
	Annotations toolsListAnnotationsJSON `json:"annotations"`
}

type toolsListResultJSONDocument struct {
	Tools []toolsListToolJSON `json:"tools"`
}

type toolsListResultCache struct {
	raw string
	err error
}

var cachedToolsListResult = buildToolsListResult(catalog.Ordered())

func buildToolsListResult(definitions []catalog.Definition) toolsListResultCache {
	tools := make([]toolsListToolJSON, len(definitions))
	for index, definition := range definitions {
		tools[index] = toolsListToolJSON{
			Name:        definition.Name,
			Description: definition.Description,
			InputSchema: append(json.RawMessage(nil), definition.InputSchema...),
			Annotations: toolsListAnnotationsJSON{
				Title:           definition.Title,
				ReadOnlyHint:    definition.ReadOnly,
				DestructiveHint: definition.Destructive,
				IdempotentHint:  definition.Idempotent,
				OpenWorldHint:   definition.OpenWorld,
			},
		}
	}
	raw, err := json.Marshal(toolsListResultJSONDocument{Tools: tools})
	return toolsListResultCache{raw: string(raw), err: err}
}

func toolsListResultJSON() (string, error) {
	return cachedToolsListResult.raw, cachedToolsListResult.err
}
