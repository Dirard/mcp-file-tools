package catalog

import "github.com/Dirard/mcp-file-tools/internal/api"

// Instructions is the complete model-facing server instruction string.
const Instructions = "Code mode: max_output_tokens=10000; emit content[0].text; set_cwd also mirrors cwd_id in structuredContent; never stringify CallToolResult."

// Definition describes one tool in the immutable v2 catalog.
type Definition struct {
	Name         api.ToolName
	Title        string
	Description  string
	InputSchema  []byte
	OutputSchema []byte
	ReadOnly     bool
	Idempotent   bool
	Destructive  bool
	OpenWorld    bool
}

// Ordered returns fresh definitions in canonical tool order.
func Ordered() []Definition {
	names := api.OrderedToolNames()
	return []Definition{
		{
			Name:         names[0],
			Title:        "Set CWD",
			Description:  "Register an absolute local directory and return cwd_id.",
			InputSchema:  []byte(setCWDInputSchema),
			OutputSchema: []byte(setCWDOutputSchema),
			ReadOnly:     false,
			Idempotent:   true,
			Destructive:  false,
			OpenWorld:    false,
		},
		{
			Name:        names[1],
			Title:       "Project Map",
			Description: "List a bounded project tree under cwd_id with resumable pagination.",
			InputSchema: []byte(projectInputSchema),
			ReadOnly:    true,
			Idempotent:  true,
			Destructive: false,
			OpenWorld:   false,
		},
		{
			Name:        names[2],
			Title:       "Search",
			Description: "Find files, text lines, or symbols under cwd_id with resumable pagination.",
			InputSchema: []byte(searchInputSchema),
			ReadOnly:    true,
			Idempotent:  true,
			Destructive: false,
			OpenWorld:   false,
		},
		{
			Name:        names[3],
			Title:       "Read",
			Description: "Read source ranges or parser outlines for up to 24 files under cwd_id.",
			InputSchema: []byte(readInputSchema),
			ReadOnly:    true,
			Idempotent:  true,
			Destructive: false,
			OpenWorld:   false,
		},
	}
}

// Lookup returns a fresh definition for name.
func Lookup(name api.ToolName) (Definition, bool) {
	for _, definition := range Ordered() {
		if definition.Name == name {
			return definition, true
		}
	}
	return Definition{}, false
}
