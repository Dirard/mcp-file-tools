package navigation

import (
	"fmt"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/catalog"
)

func TestDispatcherRoutesExactlyFourTools(t *testing.T) {
	fixture := newNavigationFixture(t, map[string]string{
		"main.go": "package main\n\nfunc Serve() {}\n",
	})
	dispatcher, err := NewDispatcher(fixture.connection)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dispatcher.Close)

	calls := []struct {
		name api.ToolName
		raw  string
	}{
		{name: api.ToolSetCWD, raw: fmt.Sprintf(`{"directory":%q}`, fixture.directory)},
		{name: api.ToolProject, raw: fmt.Sprintf(`{"cwd_id":%d,"depth":0}`, fixture.cwdID)},
		{name: api.ToolSearch, raw: fmt.Sprintf(`{"cwd_id":%d,"query":"*.go","mode":"file"}`, fixture.cwdID)},
		{name: api.ToolRead, raw: fmt.Sprintf(`{"cwd_id":%d,"files":[{"path":"main.go","end":1}]}`, fixture.cwdID)},
	}
	for _, call := range calls {
		ctx, work := fixture.work(t)
		execution := dispatcher.Call(ctx, api.NewCall(call.name, []byte(call.raw)), work)
		if err := execution.Result.Validate(); err != nil {
			t.Fatalf("%s result: %v", call.name, err)
		}
		if execution.Result.IsError() {
			t.Fatalf("%s execution = %+v", call.name, execution)
		}
	}

	ctx, work := fixture.work(t)
	unknown := dispatcher.Call(ctx, api.NewCall(api.ToolName("read_file"), []byte(`{}`)), work)
	if !unknown.Result.IsError() || resultText(t, unknown) != "ERROR\tinvalid_input\tfield=arguments\treason=does_not_match_tool_contract\tmessage=tool_arguments_are_invalid\thint=fix_the_named_field_and_retry\n" {
		t.Fatalf("unknown route = %+v", unknown)
	}
}

func TestDispatcherRejectsCatalogMismatch(t *testing.T) {
	fixture := newNavigationFixture(t, map[string]string{"main.go": "package main\n"})
	names := api.OrderedToolNames()
	definitions := catalog.Ordered()
	definitions[1], definitions[2] = definitions[2], definitions[1]
	if dispatcher, err := newDispatcher(fixture.connection, names, definitions); err == nil || dispatcher != nil {
		t.Fatalf("mismatched dispatcher = (%p, %v)", dispatcher, err)
	}
}
