package app

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

type mapEnvironment map[string]string

func (environment mapEnvironment) Lookup(name string) (string, bool) {
	value, found := environment[name]
	return value, found
}

func TestRunVersionAndRejectedArguments(t *testing.T) {
	previous := Version
	Version = "1.2.3-test"
	t.Cleanup(func() { Version = previous })

	var output bytes.Buffer
	var stderr bytes.Buffer
	if code := Run(context.Background(), []string{"-version"}, mapEnvironment{}, IO{In: strings.NewReader(""), Out: &output, Err: &stderr}); code != 0 {
		t.Fatalf("version exit = %d", code)
	}
	if output.String() != "1.2.3-test\n" || stderr.Len() != 0 {
		t.Fatalf("version output = (%q, %q)", output.String(), stderr.String())
	}

	output.Reset()
	stderr.Reset()
	if code := Run(context.Background(), []string{"--http", ":8080"}, mapEnvironment{}, IO{In: strings.NewReader(""), Out: &output, Err: &stderr}); code == 0 {
		t.Fatal("unsupported arguments succeeded")
	}
	if output.Len() != 0 || stderr.String() != "mcp-file-tools-v2: fatal\n" {
		t.Fatalf("unsupported output = (%q, %q)", output.String(), stderr.String())
	}
}

func TestRunServesTheFourToolStdioSurface(t *testing.T) {
	previous := Version
	Version = "stdio-test"
	t.Cleanup(func() { Version = previous })

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	var stderr bytes.Buffer
	if code := Run(context.Background(), nil, mapEnvironment{}, IO{In: strings.NewReader(input), Out: &output, Err: &stderr}); code != 0 {
		t.Fatalf("stdio exit = %d, stderr = %q", code, stderr.String())
	}
	text := output.String()
	for _, required := range []string{`"version":"stdio-test"`, `"name":"set_cwd"`, `"name":"project"`, `"name":"search"`, `"name":"read"`} {
		if !strings.Contains(text, required) {
			t.Fatalf("stdio output lacks %q:\n%s", required, text)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("ordinary stdio wrote stderr: %q", stderr.String())
	}
}

func TestRunRejectsInvalidConfigurationOnce(t *testing.T) {
	var output bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), nil, mapEnvironment{"MCP_CURSOR_MAX_ENTRIES": "0"}, IO{In: strings.NewReader(""), Out: &output, Err: &stderr})
	if code == 0 || output.Len() != 0 || stderr.String() != "mcp-file-tools-v2: fatal\n" {
		t.Fatalf("invalid config = (%d, %q, %q)", code, output.String(), stderr.String())
	}
}
