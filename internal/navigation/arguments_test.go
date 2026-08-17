package navigation

import (
	"strings"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/navmodel"
)

func TestDecodeProjectArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		raw        string
		wantKind   argumentKind
		wantPath   string
		wantDepth  uint8
		wantLimit  uint16
		wantCursor bool
		wantOK     bool
	}{
		{name: "defaults", raw: `{"cwd_id":7}`, wantKind: initialArguments, wantPath: ".", wantDepth: 2, wantLimit: 200, wantOK: true},
		{name: "mathematical integers", raw: `{"cwd_id":7e0,"path":"src","depth":8.0,"limit":1e3,"include_ignored":true}`, wantKind: initialArguments, wantPath: "src", wantDepth: 8, wantLimit: 1000, wantOK: true},
		{name: "continuation", raw: `{"cwd_id":7,"cursor":"AAAAAAAAAAAAAAAAAAAAAA"}`, wantKind: continuationArguments, wantCursor: true, wantOK: true},
		{name: "unknown before lexical", raw: `{"cwd_id":7,"path":"../escape","unknown":true}`},
		{name: "duplicate", raw: `{"cwd_id":7,"cwd_id":8}`},
		{name: "wrong type", raw: `{"cwd_id":"7"}`},
		{name: "fractional", raw: `{"cwd_id":7,"depth":2.5}`},
		{name: "range", raw: `{"cwd_id":7,"depth":9}`},
		{name: "mixed continuation", raw: `{"cwd_id":7,"cursor":"AAAAAAAAAAAAAAAAAAAAAA","limit":1}`},
		{name: "bad path", raw: `{"cwd_id":7,"path":"../escape"}`},
		{name: "bad cursor", raw: `{"cwd_id":7,"cursor":"not-a-cursor"}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			arguments, code := decodeProjectArguments([]byte(test.raw), nil)
			if (code == "") != test.wantOK {
				t.Fatalf("code = %q", code)
			}
			if !test.wantOK {
				return
			}
			if arguments.kind != test.wantKind {
				t.Fatalf("kind = %d", arguments.kind)
			}
			if test.wantKind == initialArguments {
				if arguments.project.Path.String() != test.wantPath || arguments.project.Depth != test.wantDepth || arguments.project.Limit != test.wantLimit {
					t.Fatalf("project = %+v", arguments.project)
				}
			}
			if test.wantCursor && arguments.continuation.Cursor.String() != "AAAAAAAAAAAAAAAAAAAAAA" {
				t.Fatalf("cursor = %q", arguments.continuation.Cursor.String())
			}
		})
	}
}

func TestDecodeSearchArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		raw         string
		wantMode    dynamicMode
		wantPath    string
		wantLimit   uint16
		wantContext uint8
		matchPath   string
		wantOK      bool
	}{
		{name: "text defaults", raw: `{"cwd_id":9,"query":"needle"}`, wantMode: dynamicTextSearch, wantPath: ".", wantLimit: 50, wantOK: true},
		{name: "text options", raw: `{"cwd_id":9,"query":"^Need(le)?$","mode":"text","path":"src","glob":"**/*.GO","regex":true,"ignore_case":true,"context":20,"include_ignored":true,"limit":1000}`, wantMode: dynamicTextSearch, wantPath: "src", wantLimit: 1000, wantContext: 20, matchPath: "src/pkg/main.go", wantOK: true},
		{name: "symbol", raw: `{"cwd_id":9,"query":"^Serve","mode":"symbol","glob":"*.go","regex":true}`, wantMode: dynamicSymbolSearch, wantPath: ".", wantLimit: 50, matchPath: "main.go", wantOK: true},
		{name: "file", raw: `{"cwd_id":9,"query":"src/**/TEST?.GO","mode":"file","path":"src","ignore_case":true,"include_ignored":true,"limit":1000}`, wantMode: dynamicFileSearch, wantPath: "src", wantLimit: 1000, matchPath: "src/pkg/test1.go", wantOK: true},
		{name: "continuation", raw: `{"cwd_id":9,"cursor":"AAAAAAAAAAAAAAAAAAAAAA"}`, wantOK: true},
		{name: "file glob forbidden", raw: `{"cwd_id":9,"query":"*.go","mode":"file","glob":"**/*.go"}`},
		{name: "file regex forbidden even false", raw: `{"cwd_id":9,"query":"*.go","mode":"file","regex":false}`},
		{name: "file context forbidden even zero", raw: `{"cwd_id":9,"query":"*.go","mode":"file","context":0}`},
		{name: "symbol context forbidden even zero", raw: `{"cwd_id":9,"query":"Serve","mode":"symbol","context":0}`},
		{name: "empty query", raw: `{"cwd_id":9,"query":""}`},
		{name: "malformed query glob", raw: `{"cwd_id":9,"query":"[abc","mode":"file"}`},
		{name: "malformed filter glob", raw: `{"cwd_id":9,"query":"needle","glob":"[abc"}`},
		{name: "malformed regex", raw: `{"cwd_id":9,"query":"(","regex":true}`},
		{name: "context range", raw: `{"cwd_id":9,"query":"needle","context":21}`},
		{name: "unknown mode", raw: `{"cwd_id":9,"query":"needle","mode":"semantic"}`},
		{name: "unknown", raw: `{"cwd_id":9,"query":"*.go","mode":"file","wat":1}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			arguments, code := decodeSearchArguments([]byte(test.raw), nil)
			if (code == "") != test.wantOK {
				t.Fatalf("code = %q", code)
			}
			if !test.wantOK || arguments.kind == continuationArguments {
				return
			}
			if arguments.search.Mode != test.wantMode || arguments.search.Path.String() != test.wantPath || arguments.search.Limit != test.wantLimit || arguments.search.Context != test.wantContext {
				t.Fatalf("search = %+v", arguments.search)
			}
			if test.matchPath != "" && (arguments.search.Glob == nil || !arguments.search.Glob.Match(test.matchPath)) {
				t.Fatalf("compiled glob does not match %q", test.matchPath)
			}
		})
	}
}

func TestDecodeReadArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		raw        string
		wantKind   argumentKind
		wantView   navmodel.ReadView
		wantFiles  int
		wantStart  uint32
		wantEnd    uint32
		wantBytes  uint64
		wantCursor bool
		wantOK     bool
	}{
		{name: "source defaults", raw: `{"cwd_id":11,"files":[{"path":"main.go","end":7}]}`, wantKind: initialArguments, wantView: navmodel.ReadSource, wantFiles: 1, wantStart: 1, wantEnd: 7, wantBytes: 1048576, wantOK: true},
		{name: "source bounds and duplicates", raw: `{"cwd_id":11e0,"files":[{"path":"main.go","start":2.0,"end":2147483647},{"path":"main.go","end":3}],"view":"source","max_bytes":4096}`, wantKind: initialArguments, wantView: navmodel.ReadSource, wantFiles: 2, wantStart: 2, wantEnd: 2147483647, wantBytes: 4096, wantOK: true},
		{name: "max bytes upper bound", raw: `{"cwd_id":11,"files":[{"path":"main.go","end":1}],"max_bytes":1048576}`, wantKind: initialArguments, wantView: navmodel.ReadSource, wantFiles: 1, wantStart: 1, wantEnd: 1, wantBytes: 1048576, wantOK: true},
		{name: "outline", raw: `{"cwd_id":11,"files":[{"path":"main.go"}],"view":"outline"}`, wantKind: initialArguments, wantView: navmodel.ReadOutline, wantFiles: 1, wantBytes: 1048576, wantOK: true},
		{name: "continuation", raw: `{"cwd_id":11,"cursor":"AAAAAAAAAAAAAAAAAAAAAA"}`, wantKind: continuationArguments, wantCursor: true, wantOK: true},
		{name: "twenty four files", raw: `{"cwd_id":11,"files":[` + strings.TrimSuffix(strings.Repeat(`{"path":"main.go","end":1},`, 24), ",") + `]}`, wantKind: initialArguments, wantView: navmodel.ReadSource, wantFiles: 24, wantStart: 1, wantEnd: 1, wantBytes: 1048576, wantOK: true},
		{name: "empty files", raw: `{"cwd_id":11,"files":[]}`},
		{name: "twenty five files", raw: `{"cwd_id":11,"files":[` + strings.TrimSuffix(strings.Repeat(`{"path":"main.go","end":1},`, 25), ",") + `]}`},
		{name: "missing source end", raw: `{"cwd_id":11,"files":[{"path":"main.go"}]}`},
		{name: "source start after end", raw: `{"cwd_id":11,"files":[{"path":"main.go","start":3,"end":2}]}`},
		{name: "source field in outline", raw: `{"cwd_id":11,"files":[{"path":"main.go","start":1}],"view":"outline"}`},
		{name: "unknown item field", raw: `{"cwd_id":11,"files":[{"path":"main.go","end":1,"extra":true}]}`},
		{name: "duplicate item field", raw: `{"cwd_id":11,"files":[{"path":"main.go","path":"other.go","end":1}]}`},
		{name: "null item field", raw: `{"cwd_id":11,"files":[{"path":null,"end":1}]}`},
		{name: "bad path after valid item", raw: `{"cwd_id":11,"files":[{"path":"main.go","end":1},{"path":"../escape","end":1}]}`},
		{name: "max bytes low", raw: `{"cwd_id":11,"files":[{"path":"main.go","end":1}],"max_bytes":4095}`},
		{name: "max bytes high", raw: `{"cwd_id":11,"files":[{"path":"main.go","end":1}],"max_bytes":1048577}`},
		{name: "mixed continuation", raw: `{"cwd_id":11,"cursor":"AAAAAAAAAAAAAAAAAAAAAA","files":[{"path":"main.go","end":1}]}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			arguments, code := decodeReadArguments([]byte(test.raw), nil)
			if (code == "") != test.wantOK {
				t.Fatalf("code = %q", code)
			}
			if !test.wantOK {
				return
			}
			if arguments.kind != test.wantKind {
				t.Fatalf("kind = %d", arguments.kind)
			}
			if test.wantKind == continuationArguments {
				if test.wantCursor && arguments.continuation.Cursor.String() != "AAAAAAAAAAAAAAAAAAAAAA" {
					t.Fatalf("cursor = %q", arguments.continuation.Cursor.String())
				}
				return
			}
			if arguments.read.Mode != test.wantView || len(arguments.read.Files) != test.wantFiles || arguments.read.MaxBytes != test.wantBytes {
				t.Fatalf("read = %+v", arguments.read)
			}
			if test.wantFiles != 0 && (arguments.read.Files[0].Start != test.wantStart || arguments.read.Files[0].End != test.wantEnd) {
				t.Fatalf("first file = %+v", arguments.read.Files[0])
			}
		})
	}
}
