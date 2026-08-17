package navigation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/codeparse"
	"github.com/Dirard/mcp-file-tools/internal/config"
	"github.com/Dirard/mcp-file-tools/internal/cursor"
	"github.com/Dirard/mcp-file-tools/internal/cwd"
	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"github.com/Dirard/mcp-file-tools/internal/rootfs"
	runtimepkg "github.com/Dirard/mcp-file-tools/internal/runtime"
	"github.com/Dirard/mcp-file-tools/internal/scanner"
	"github.com/Dirard/mcp-file-tools/internal/textio"
)

func TestProjectDepthZeroAndResumableTraversal(t *testing.T) {
	fixture := newNavigationFixture(t, map[string]string{
		"a.go":     "package a\n",
		"b.txt":    "b\n",
		"dir/c.go": "package c\n",
	})

	rootOnly := fixture.project(t, fmt.Sprintf(`{"cwd_id":%d,"depth":0}`, fixture.cwdID))
	if rootOnly.Kind != runtimepkg.ExecutionOrdinary || rootOnly.Result.IsError() {
		t.Fatalf("root-only execution = %+v", rootOnly)
	}
	if text := resultText(t, rootOnly); text != "@@project\t\".\"\tcomplete\trows=1\nD\t\".\"\n" {
		t.Fatalf("root-only project page:\n%s", text)
	}

	execution := fixture.project(t, fmt.Sprintf(`{"cwd_id":%d,"depth":1,"limit":1}`, fixture.cwdID))
	var rows []string
	for {
		text := resultText(t, execution)
		if execution.Result.IsError() {
			t.Fatalf("project page error:\n%s", text)
		}
		rows = append(rows, pageRows(text)...)
		cursorValue, partial := pageCursor(text)
		if !partial {
			if execution.Kind != runtimepkg.ExecutionOrdinary {
				t.Fatalf("terminal execution kind = %d", execution.Kind)
			}
			break
		}
		if execution.Kind == runtimepkg.ExecutionInitialCursor {
			publishInitial(t, execution)
		} else if execution.Kind != runtimepkg.ExecutionOrdinary || execution.Publication != nil {
			t.Fatalf("continuation execution = %+v", execution)
		}
		execution = fixture.project(t, fmt.Sprintf(`{"cwd_id":%d,"cursor":%q}`, fixture.cwdID, cursorValue))
	}

	want := []string{"D\t\".\"", "F\t\"a.go\"", "F\t\"b.txt\"", "D\t\"dir\""}
	if strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Fatalf("project rows = %q, want %q", rows, want)
	}
}

func TestFileSearchDirectoryAndExplicitFile(t *testing.T) {
	fixture := newNavigationFixture(t, map[string]string{
		"a.go":     "package a\n",
		"b.txt":    "b\n",
		"dir/c.go": "package c\n",
	})

	directory := fixture.search(t, fmt.Sprintf(`{"cwd_id":%d,"query":"*.go","mode":"file"}`, fixture.cwdID))
	if directory.Kind != runtimepkg.ExecutionOrdinary || directory.Result.IsError() {
		t.Fatalf("directory search execution = %+v", directory)
	}
	if rows := pageRows(resultText(t, directory)); strings.Join(rows, "\n") != "F\t\"a.go\"\nF\t\"dir/c.go\"" {
		t.Fatalf("directory search rows = %q", rows)
	}

	explicit := fixture.search(t, fmt.Sprintf(`{"cwd_id":%d,"query":"*.go","mode":"file","path":"a.go"}`, fixture.cwdID))
	if rows := pageRows(resultText(t, explicit)); len(rows) != 1 || rows[0] != "F\t\"a.go\"" {
		t.Fatalf("explicit-file rows = %q", rows)
	}

	miss := fixture.search(t, fmt.Sprintf(`{"cwd_id":%d,"query":"*.txt","mode":"file","path":"a.go"}`, fixture.cwdID))
	if rows := pageRows(resultText(t, miss)); len(rows) != 0 {
		t.Fatalf("explicit-file miss rows = %q", rows)
	}
}

func TestFileSearchTraversesBeyondUint8Depth(t *testing.T) {
	path := strings.Repeat("d/", 256) + "needle.txt"
	fixture := newNavigationFixture(t, map[string]string{path: "needle\n"})

	execution := fixture.search(t, fmt.Sprintf(`{"cwd_id":%d,"query":"needle.txt","mode":"file"}`, fixture.cwdID))
	rows := fixture.collectSearchRows(t, execution)
	want := "F\t" + fmt.Sprintf("%q", path)
	if len(rows) != 1 || rows[0] != want {
		t.Fatalf("deep search rows = %q, want %q", rows, want)
	}
}

func TestTextSearchCanonicalContextAndContinuation(t *testing.T) {
	fixture := newNavigationFixture(t, map[string]string{
		"a.txt": "before\r\nNeedle\r\nafter\r\nother\r\n",
		"b.txt": "needle second\n",
	})

	execution := fixture.search(t, fmt.Sprintf(`{"cwd_id":%d,"query":"needle","mode":"text","glob":"*.txt","ignore_case":true,"context":1,"limit":2}`, fixture.cwdID))
	rows := fixture.collectSearchRows(t, execution)
	want := []string{
		"C\t1\tbefore",
		"M\t2\tNeedle",
		"C\t3\tafter",
		"M\t1\tneedle second",
	}
	if strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Fatalf("text rows = %q, want %q", rows, want)
	}

	limited := &textSearchSink{
		path:             "a.txt",
		matcher:          regexp.MustCompile("needle"),
		maxRetainedBytes: 1,
	}
	if err := limited.Consume(textio.Line{Number: 1, Bytes: []byte("needle")}); err == nil || !limited.exceeded || len(limited.rows) != 0 {
		t.Fatalf("retained budget was not enforced before append: err=%v exceeded=%t rows=%d", err, limited.exceeded, len(limited.rows))
	}
}

func TestSymbolSearchAndExplicitUnsupportedLanguage(t *testing.T) {
	fixture := newNavigationFixture(t, map[string]string{
		"main.go":   "package main\n\nfunc Serve() {}\n",
		"notes.txt": "Serve\n",
	})

	directory := fixture.search(t, fmt.Sprintf(`{"cwd_id":%d,"query":"^Serve$","mode":"symbol","regex":true}`, fixture.cwdID))
	if rows := fixture.collectSearchRows(t, directory); len(rows) != 1 || rows[0] != "S\t3:3\tfunction\t\"Serve\"" {
		t.Fatalf("symbol rows = %q", rows)
	}

	unsupported := fixture.search(t, fmt.Sprintf(`{"cwd_id":%d,"query":"Serve","mode":"symbol","path":"notes.txt","glob":"*.go"}`, fixture.cwdID))
	if !unsupported.Result.IsError() || resultText(t, unsupported) != "ERROR\tunsupported_language\tmessage=outline_language_is_not_supported\thint=use_source_view_instead\n" {
		t.Fatalf("unsupported language = %+v, %q", unsupported, resultText(t, unsupported))
	}
}

func TestTextSearchBroadWarningsAndExplicitBinaryError(t *testing.T) {
	fixture := newNavigationFixture(t, map[string]string{
		"binary.txt": "needle\x00data",
		"long.txt":   strings.Repeat("x", 4097) + "needle\n",
	})

	broad := fixture.search(t, fmt.Sprintf(`{"cwd_id":%d,"query":"needle","mode":"text"}`, fixture.cwdID))
	broadText := resultText(t, broad)
	if broad.Result.IsError() || !strings.Contains(broadText, "!\tbinary_skipped\tcount=1") || !strings.Contains(broadText, "!\tline_too_long_skipped\tcount=1") {
		t.Fatalf("broad text result:\n%s", broadText)
	}

	explicit := fixture.search(t, fmt.Sprintf(`{"cwd_id":%d,"query":"needle","mode":"text","path":"binary.txt"}`, fixture.cwdID))
	if !explicit.Result.IsError() || resultText(t, explicit) != "ERROR\tbinary\tmessage=file_is_binary\thint=choose_a_text_file_or_use_another_tool\n" {
		t.Fatalf("explicit binary result:\n%s", resultText(t, explicit))
	}
}

func TestReadSourceSnapshotContinuationIsFilesystemFree(t *testing.T) {
	var original strings.Builder
	var want []string
	for line := 1; line <= 12; line++ {
		text := fmt.Sprintf("line-%02d-%s", line, strings.Repeat("x", 880))
		original.WriteString(text)
		original.WriteString("\r\n")
		want = append(want, fmt.Sprintf("%d|%s", line, text))
	}
	fixture := newNavigationFixture(t, map[string]string{"large.txt": original.String()})

	execution := fixture.read(t, fmt.Sprintf(`{"cwd_id":%d,"files":[{"path":"large.txt","end":12}],"max_bytes":4096}`, fixture.cwdID))
	if execution.Kind != runtimepkg.ExecutionInitialCursor || execution.Result.IsError() {
		t.Fatalf("initial read execution = %+v", execution)
	}
	publishInitial(t, execution)
	if err := os.Remove(filepath.Join(fixture.directory, "large.txt")); err != nil {
		t.Fatal(err)
	}

	var got []string
	for {
		text := resultText(t, execution)
		if execution.Result.IsError() {
			t.Fatalf("read page error:\n%s", text)
		}
		got = append(got, sourceRows(text)...)
		cursorValue, partial := pageCursor(text)
		if !partial {
			break
		}
		execution = fixture.read(t, fmt.Sprintf(`{"cwd_id":%d,"cursor":%q}`, fixture.cwdID, cursorValue))
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("source rows changed after deletion: got %d rows", len(got))
	}
}

func TestReadReturnsMoreThanLegacyOutputLimit(t *testing.T) {
	fixture := newNavigationFixture(t, map[string]string{
		"large.txt": strings.Repeat(strings.Repeat("x", 1024)+"\n", 64),
	})

	execution := fixture.read(t, fmt.Sprintf(`{"cwd_id":%d,"files":[{"path":"large.txt","end":64}],"max_bytes":1048576}`, fixture.cwdID))
	text := resultText(t, execution)
	if execution.Kind != runtimepkg.ExecutionOrdinary || execution.Result.IsError() || len(text) <= 32768 {
		t.Fatalf("read did not return one page above the legacy limit: kind=%d bytes=%d result=%q", execution.Kind, len(text), text)
	}
}

func TestReadMixedAndAllErrorResult(t *testing.T) {
	fixture := newNavigationFixture(t, map[string]string{"ok.txt": "first\r\nsecond\r\n"})

	mixed := fixture.read(t, fmt.Sprintf(`{"cwd_id":%d,"files":[{"path":"ok.txt","start":2,"end":9},{"path":"missing.txt","end":1}]}`, fixture.cwdID))
	mixedText := resultText(t, mixed)
	if mixed.Result.IsError() || !strings.Contains(mixedText, "2|second\n") || !strings.Contains(mixedText, "@\t\"<path-hidden>\"\titem=1\terror\tnot_found") {
		t.Fatalf("mixed read result:\n%s", mixedText)
	}

	allError := fixture.read(t, fmt.Sprintf(`{"cwd_id":%d,"files":[{"path":"missing-a.txt","end":1},{"path":"missing-b.txt","end":1}]}`, fixture.cwdID))
	allErrorText := resultText(t, allError)
	if !allError.Result.IsError() || strings.Count(allErrorText, "\terror\tnot_found") != 2 {
		t.Fatalf("all-error read result:\n%s", allErrorText)
	}
}

func TestReadOutlineAndUnsupportedLanguage(t *testing.T) {
	fixture := newNavigationFixture(t, map[string]string{
		"main.go":   "package main\n\nfunc Serve() {}\n",
		"notes.txt": "Serve\n",
	})

	execution := fixture.read(t, fmt.Sprintf(`{"cwd_id":%d,"files":[{"path":"main.go"},{"path":"notes.txt"}],"view":"outline"}`, fixture.cwdID))
	text := resultText(t, execution)
	if execution.Result.IsError() || !strings.Contains(text, "@@read\toutline\tcomplete") || !strings.Contains(text, "\tfunction\t\"Serve\"") || !strings.Contains(text, "@\t\"<path-hidden>\"\titem=1\terror\tunsupported_language") {
		t.Fatalf("outline read result:\n%s", text)
	}
}

func TestSetCWDRegistersOneStableRoot(t *testing.T) {
	fixture := newNavigationFixture(t, map[string]string{"main.go": "package main\n"})
	raw := fmt.Sprintf(`{"directory":%q}`, fixture.directory)

	first := fixture.setCWD(t, raw)
	firstID, ok := first.Result.CWDID()
	if first.Kind != runtimepkg.ExecutionOrdinary || first.Result.IsError() || !ok || firstID != fixture.cwdID {
		t.Fatalf("first set_cwd = %+v", first)
	}
	second := fixture.setCWD(t, raw)
	secondID, ok := second.Result.CWDID()
	if second.Result.IsError() || !ok || secondID != firstID {
		t.Fatalf("second set_cwd = %+v", second)
	}

	invalid := fixture.setCWD(t, fmt.Sprintf(`{"directory":%q,"extra":true}`, fixture.directory))
	if !invalid.Result.IsError() || resultText(t, invalid) != "ERROR\tinvalid_input\tfield=arguments\treason=does_not_match_tool_contract\tmessage=tool_arguments_are_invalid\thint=fix_the_named_field_and_retry\n" {
		t.Fatalf("invalid set_cwd = %+v", invalid)
	}
	missing := fixture.setCWD(t, fmt.Sprintf(`{"directory":%q}`, filepath.Join(fixture.directory, "missing")))
	if !missing.Result.IsError() || resultText(t, missing) != "ERROR\tnot_found\tmessage=path_was_not_found\thint=check_the_relative_path_and_registered_cwd\n" {
		t.Fatalf("missing set_cwd = %+v", missing)
	}
}

func TestInvalidInputExplainsKnownFields(t *testing.T) {
	fixture := newNavigationFixture(t, map[string]string{"main.go": "package main\n"})
	tests := []struct {
		name string
		run  func() runtimepkg.Execution
		want string
	}{
		{
			name: "project missing cwd_id",
			run:  func() runtimepkg.Execution { return fixture.project(t, `{}`) },
			want: "ERROR\tinvalid_input\tfield=cwd_id\treason=required\tmessage=tool_arguments_are_invalid\thint=fix_the_named_field_and_retry\n",
		},
		{
			name: "search missing cwd_id",
			run:  func() runtimepkg.Execution { return fixture.search(t, `{}`) },
			want: "ERROR\tinvalid_input\tfield=cwd_id\treason=required\tmessage=tool_arguments_are_invalid\thint=fix_the_named_field_and_retry\n",
		},
		{
			name: "search missing query",
			run: func() runtimepkg.Execution {
				return fixture.search(t, fmt.Sprintf(`{"cwd_id":%d}`, fixture.cwdID))
			},
			want: "ERROR\tinvalid_input\tfield=query\treason=required\tmessage=tool_arguments_are_invalid\thint=fix_the_named_field_and_retry\n",
		},
		{
			name: "search invalid mode",
			run: func() runtimepkg.Execution {
				return fixture.search(t, fmt.Sprintf(`{"cwd_id":%d,"query":"main","mode":"unknown"}`, fixture.cwdID))
			},
			want: "ERROR\tinvalid_input\tfield=mode\treason=must_be_file_text_or_symbol\tmessage=tool_arguments_are_invalid\thint=fix_the_named_field_and_retry\n",
		},
		{
			name: "read missing cwd_id",
			run:  func() runtimepkg.Execution { return fixture.read(t, `{}`) },
			want: "ERROR\tinvalid_input\tfield=cwd_id\treason=required\tmessage=tool_arguments_are_invalid\thint=fix_the_named_field_and_retry\n",
		},
		{
			name: "read missing files",
			run: func() runtimepkg.Execution {
				return fixture.read(t, fmt.Sprintf(`{"cwd_id":%d}`, fixture.cwdID))
			},
			want: "ERROR\tinvalid_input\tfield=files\treason=required\tmessage=tool_arguments_are_invalid\thint=fix_the_named_field_and_retry\n",
		},
		{
			name: "relative directory",
			run:  func() runtimepkg.Execution { return fixture.setCWD(t, `{"directory":"relative"}`) },
			want: "ERROR\tinvalid_input\tfield=directory\treason=absolute_path_required\tmessage=tool_arguments_are_invalid\thint=fix_the_named_field_and_retry\n",
		},
		{
			name: "invalid regex",
			run: func() runtimepkg.Execution {
				return fixture.search(t, fmt.Sprintf(`{"cwd_id":%d,"query":"(","regex":true}`, fixture.cwdID))
			},
			want: "ERROR\tinvalid_input\tfield=query\treason=invalid_re2_expression\tmessage=tool_arguments_are_invalid\thint=fix_the_named_field_and_retry\n",
		},
		{
			name: "max bytes below minimum",
			run: func() runtimepkg.Execution {
				return fixture.read(t, fmt.Sprintf(`{"cwd_id":%d,"files":[{"path":"main.go","end":1}],"max_bytes":2048}`, fixture.cwdID))
			},
			want: "ERROR\tinvalid_input\tfield=max_bytes\treason=minimum_is_4096\tmessage=tool_arguments_are_invalid\thint=fix_the_named_field_and_retry\n",
		},
		{
			name: "source read missing end",
			run: func() runtimepkg.Execution {
				return fixture.read(t, fmt.Sprintf(`{"cwd_id":%d,"files":[{"path":"main.go"}]}`, fixture.cwdID))
			},
			want: "ERROR\tinvalid_input\tfield=files[].end\treason=required_for_source_view\tmessage=tool_arguments_are_invalid\thint=fix_the_named_field_and_retry\n",
		},
		{
			name: "project depth above maximum",
			run: func() runtimepkg.Execution {
				return fixture.project(t, fmt.Sprintf(`{"cwd_id":%d,"depth":9}`, fixture.cwdID))
			},
			want: "ERROR\tinvalid_input\tfield=depth\treason=must_be_integer_0_to_8\tmessage=tool_arguments_are_invalid\thint=fix_the_named_field_and_retry\n",
		},
		{
			name: "outline read with source fields",
			run: func() runtimepkg.Execution {
				return fixture.read(t, fmt.Sprintf(`{"cwd_id":%d,"view":"outline","files":[{"path":"main.go","end":1}]}`, fixture.cwdID))
			},
			want: "ERROR\tinvalid_input\tfield=files[]\treason=outline_items_allow_only_path\tmessage=tool_arguments_are_invalid\thint=fix_the_named_field_and_retry\n",
		},
		{
			name: "source read start after end",
			run: func() runtimepkg.Execution {
				return fixture.read(t, fmt.Sprintf(`{"cwd_id":%d,"files":[{"path":"main.go","start":2,"end":1}]}`, fixture.cwdID))
			},
			want: "ERROR\tinvalid_input\tfield=files[].start\treason=must_not_exceed_end\tmessage=tool_arguments_are_invalid\thint=fix_the_named_field_and_retry\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			execution := test.run()
			if !execution.Result.IsError() || resultText(t, execution) != test.want {
				t.Fatalf("result = %q, want %q", resultText(t, execution), test.want)
			}
		})
	}
}

type navigationFixture struct {
	connection *Connection
	cwdID      uint64
	nextCall   uint64
	directory  string
}

func newNavigationFixture(t *testing.T, files map[string]string) *navigationFixture {
	t.Helper()
	directory := t.TempDir()
	for name, contents := range files {
		path := filepath.Join(directory, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	rootPath, code := pathspec.ParseRootDirectory(hostTarget(), directory)
	if code != "" {
		t.Fatalf("parse fixture root = %q", code)
	}
	root, err := rootfs.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultRuntime()
	cwdRegistry := cwd.New(cfg, nil)
	t.Cleanup(func() { _ = cwdRegistry.Close() })
	id, inserted, code := cwdRegistry.Register(root)
	if code != "" || !inserted {
		t.Fatalf("register fixture root = (%d, %v, %q)", id, inserted, code)
	}
	cursors, err := cursor.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cursors.Close() })
	scanLimiter := runtimepkg.NewSubLimiter(cfg.ScanMaxCalls)
	parseLimiter := runtimepkg.NewSubLimiter(cfg.ParseMaxCalls)
	parser := codeparse.NewService(
		cfg,
		codeparse.NewCache(cfg.ParserCacheMaxEntries, cfg.ParserCacheMaxBytes),
		parseLimiter,
	)
	if parser == nil {
		t.Fatal("parser service is nil")
	}
	service := &Service{
		Config:      cfg,
		CWD:         cwdRegistry,
		Parser:      parser,
		ScanLimiter: scanLimiter,
		Scanner:     scanner.NewService(scanLimiter),
	}
	return &navigationFixture{
		connection: &Connection{Service: service, Cursors: cursors},
		cwdID:      uint64(id),
		directory:  directory,
	}
}

func (fixture *navigationFixture) project(t *testing.T, raw string) runtimepkg.Execution {
	t.Helper()
	ctx, work := fixture.work(t)
	return fixture.connection.Project(ctx, []byte(raw), work)
}

func (fixture *navigationFixture) setCWD(t *testing.T, raw string) runtimepkg.Execution {
	t.Helper()
	ctx, work := fixture.work(t)
	return fixture.connection.SetCWD(ctx, []byte(raw), work)
}

func (fixture *navigationFixture) search(t *testing.T, raw string) runtimepkg.Execution {
	t.Helper()
	ctx, work := fixture.work(t)
	return fixture.connection.Search(ctx, []byte(raw), work)
}

func (fixture *navigationFixture) read(t *testing.T, raw string) runtimepkg.Execution {
	t.Helper()
	ctx, work := fixture.work(t)
	return fixture.connection.Read(ctx, []byte(raw), work)
}

func (fixture *navigationFixture) collectSearchRows(t *testing.T, execution runtimepkg.Execution) []string {
	t.Helper()
	var rows []string
	for {
		text := resultText(t, execution)
		if execution.Result.IsError() {
			t.Fatalf("search page error:\n%s", text)
		}
		rows = append(rows, pageRows(text)...)
		cursorValue, partial := pageCursor(text)
		if !partial {
			return rows
		}
		if execution.Kind == runtimepkg.ExecutionInitialCursor {
			publishInitial(t, execution)
		}
		execution = fixture.search(t, fmt.Sprintf(`{"cwd_id":%d,"cursor":%q}`, fixture.cwdID, cursorValue))
	}
}

func (fixture *navigationFixture) work(t *testing.T) (context.Context, *runtimepkg.WorkLease) {
	t.Helper()
	fixture.nextCall++
	coordinator := runtimepkg.NewCoordinator(runtimepkg.Limits{
		MaxConcurrent: 1,
		QueueMax:      1,
		QueueTimeout:  time.Second,
	})
	reservation, outcome := coordinator.Admit(context.Background(), []byte(fmt.Sprintf("navigation-%d", fixture.nextCall)))
	if outcome != runtimepkg.AdmitRun || reservation == nil {
		t.Fatalf("admit = %T, %d", reservation, outcome)
	}
	work, start := reservation.Start()
	if start.Kind != runtimepkg.StartRun || work == nil {
		t.Fatalf("start = %p, %#v", work, start)
	}
	return reservation.Context(), work
}

func publishInitial(t *testing.T, execution runtimepkg.Execution) {
	t.Helper()
	if execution.Kind != runtimepkg.ExecutionInitialCursor || execution.Publication == nil {
		t.Fatalf("partial execution lacks publication: %+v", execution)
	}
	if err := execution.ValidatePublication(); err != nil {
		t.Fatal(err)
	}
	if err := execution.Publication.Prepare(); err != nil {
		t.Fatal(err)
	}
	if err := execution.Publication.Commit(); err != nil {
		t.Fatal(err)
	}
}

func resultText(t *testing.T, execution runtimepkg.Execution) string {
	t.Helper()
	if err := execution.Result.Validate(); err != nil {
		t.Fatal(err)
	}
	text, ok := execution.Result.Text()
	if !ok {
		t.Fatalf("execution does not contain text: %+v", execution)
	}
	return text
}

func pageCursor(text string) (string, bool) {
	header, _, _ := strings.Cut(text, "\n")
	marker := "\tcursor="
	index := strings.Index(header, marker)
	if index < 0 {
		return "", false
	}
	return header[index+len(marker):], true
}

func pageRows(text string) []string {
	_, body, found := strings.Cut(text, "\n")
	if !found {
		return nil
	}
	var rows []string
	for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		if strings.HasPrefix(line, "D\t") || strings.HasPrefix(line, "F\t") || strings.HasPrefix(line, "M\t") || strings.HasPrefix(line, "C\t") || strings.HasPrefix(line, "S\t") {
			rows = append(rows, line)
		}
	}
	return rows
}

func sourceRows(text string) []string {
	_, body, found := strings.Cut(text, "\n")
	if !found {
		return nil
	}
	var rows []string
	for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		separator := strings.IndexByte(line, '|')
		if separator > 0 && line[0] >= '0' && line[0] <= '9' {
			rows = append(rows, line)
		}
	}
	return rows
}
