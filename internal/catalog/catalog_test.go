package catalog

import (
	"reflect"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

func TestOrderedCatalog(t *testing.T) {
	want := []Definition{
		{
			Name: api.ToolSetCWD, Title: "Set CWD",
			Description: "Register an absolute local directory and return cwd_id.",
			ReadOnly:    false, Idempotent: true, Destructive: false, OpenWorld: false,
		},
		{
			Name: api.ToolProject, Title: "Project Map",
			Description: "List a bounded project tree under cwd_id with resumable pagination.",
			ReadOnly:    true, Idempotent: true, Destructive: false, OpenWorld: false,
		},
		{
			Name: api.ToolSearch, Title: "Search",
			Description: "Find files, text lines, or symbols under cwd_id with resumable pagination.",
			ReadOnly:    true, Idempotent: true, Destructive: false, OpenWorld: false,
		},
		{
			Name: api.ToolRead, Title: "Read",
			Description: "Read source ranges or parser outlines for up to 24 files under cwd_id.",
			ReadOnly:    true, Idempotent: true, Destructive: false, OpenWorld: false,
		},
	}

	got := Ordered()
	if len(got) != len(want) {
		t.Fatalf("Ordered() length = %d, want %d", len(got), len(want))
	}
	toolNames := api.OrderedToolNames()
	for i := range want {
		if got[i].Name != toolNames[i] {
			t.Errorf("Ordered()[%d].Name = %q, want api order %q", i, got[i].Name, toolNames[i])
		}
		if len(got[i].InputSchema) == 0 {
			t.Errorf("Ordered()[%d].InputSchema is empty", i)
		}
		if (i == 0) != (len(got[i].OutputSchema) != 0) {
			t.Errorf("Ordered()[%d].OutputSchema presence is wrong", i)
		}
		gotWithoutSchema := got[i]
		gotWithoutSchema.InputSchema = nil
		gotWithoutSchema.OutputSchema = nil
		if !reflect.DeepEqual(gotWithoutSchema, want[i]) {
			t.Errorf("Ordered()[%d] = %#v, want %#v", i, gotWithoutSchema, want[i])
		}

		lookup, ok := Lookup(want[i].Name)
		if !ok {
			t.Errorf("Lookup(%q) not found", want[i].Name)
			continue
		}
		lookup.InputSchema = nil
		lookup.OutputSchema = nil
		if !reflect.DeepEqual(lookup, want[i]) {
			t.Errorf("Lookup(%q) = %#v, want %#v", want[i].Name, lookup, want[i])
		}
	}

	if got, ok := Lookup(api.ToolName("unknown")); ok || !reflect.DeepEqual(got, Definition{}) {
		t.Fatalf("Lookup(unknown) = %#v, %t; want zero, false", got, ok)
	}
}

func TestInstructions(t *testing.T) {
	const want = "Code mode: max_output_tokens=10000; emit content[0].text; set_cwd also mirrors cwd_id in structuredContent; never stringify CallToolResult."
	if Instructions != want {
		t.Fatalf("Instructions = %q, want %q", Instructions, want)
	}
	if len(Instructions) != len(want) {
		t.Fatalf("Instructions length = %d, want %d ASCII bytes", len(Instructions), len(want))
	}
}

func TestCatalogDefinitionIsolation(t *testing.T) {
	baseline := Ordered()
	mutated := Ordered()
	if len(mutated) != 4 {
		t.Fatalf("Ordered() length = %d, want 4", len(mutated))
	}

	for i := range mutated {
		mutated[i].Name = api.ToolName("mutated")
		mutated[i].Title = "mutated"
		mutated[i].Description = "mutated"
		mutated[i].ReadOnly = !mutated[i].ReadOnly
		mutated[i].Idempotent = !mutated[i].Idempotent
		mutated[i].Destructive = !mutated[i].Destructive
		mutated[i].OpenWorld = !mutated[i].OpenWorld
		for j := range mutated[i].InputSchema {
			mutated[i].InputSchema[j] ^= 0xff
		}
		for j := range mutated[i].OutputSchema {
			mutated[i].OutputSchema[j] ^= 0xff
		}
	}

	fresh := Ordered()
	if !reflect.DeepEqual(fresh, baseline) {
		t.Fatalf("fresh Ordered() changed after mutation:\n got %#v\nwant %#v", fresh, baseline)
	}
	for i := range baseline {
		if len(baseline[i].InputSchema) == 0 || len(fresh[i].InputSchema) == 0 {
			t.Fatalf("Ordered()[%d].InputSchema is empty", i)
		}
		if &baseline[i].InputSchema[0] == &fresh[i].InputSchema[0] {
			t.Errorf("Ordered()[%d].InputSchema aliases a later call", i)
		}
	}

	for _, name := range api.OrderedToolNames() {
		first, ok := Lookup(name)
		if !ok {
			t.Fatalf("Lookup(%q) not found", name)
		}
		second, ok := Lookup(name)
		if !ok {
			t.Fatalf("second Lookup(%q) not found", name)
		}
		for i := range first.InputSchema {
			first.InputSchema[i] ^= 0xff
		}
		for i := range first.OutputSchema {
			first.OutputSchema[i] ^= 0xff
		}
		first.Title = "mutated"

		third, ok := Lookup(name)
		if !ok {
			t.Fatalf("third Lookup(%q) not found", name)
		}
		if !reflect.DeepEqual(third, second) {
			t.Fatalf("fresh Lookup(%q) changed after mutation", name)
		}
		if len(second.InputSchema) == 0 || len(third.InputSchema) == 0 {
			t.Fatalf("Lookup(%q).InputSchema is empty", name)
		}
		if &second.InputSchema[0] == &third.InputSchema[0] {
			t.Errorf("Lookup(%q).InputSchema aliases a later call", name)
		}
	}

	if got := api.OrderedToolNames(); got != [4]api.ToolName{api.ToolSetCWD, api.ToolProject, api.ToolSearch, api.ToolRead} {
		t.Fatalf("api tool order changed after catalog mutation: %v", got)
	}
}
