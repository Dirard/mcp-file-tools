package config

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

var (
	_ uint64        = OutputMaxBytes
	_ uint64        = PrimaryCodeModeMaxOutputTokens
	_ uint64        = StdioFrameMaxBytes
	_ uint64        = RequestIDMaxRawBytes
	_ uint64        = SessionMaxRequests
	_ uint64        = UsedIDKeyMaxBytes
	_ uint64        = UsedIDTableSlots
	_ uint64        = UsedIDArenaMaxBytes
	_ time.Duration = CursorHandoffGrace
	_ uint64        = WarningExamplesPerCode
	_ uint64        = WarningSummaryLineMaxBytes
	_ uint64        = WarningSummaryMaxBytes
	_ uint64        = SearchScanLineMaxBytes
	_ uint64        = SourceLineMaxBytes
	_ uint64        = RangeReadChunkMaxBytes
	_ uint64        = ReadMaxItems
	_ uint64        = Runtime{}.ScanMaxBytes
)

const (
	_ uint64 = UsedIDArenaMaxBytes - SessionMaxRequests*UsedIDKeyMaxBytes
	_ uint64 = SessionMaxRequests*UsedIDKeyMaxBytes - UsedIDArenaMaxBytes
	_ uint64 = WarningSummaryMaxBytes - 12*WarningSummaryLineMaxBytes
	_ uint64 = 12*WarningSummaryLineMaxBytes - WarningSummaryMaxBytes
)

func TestCompileTimeLimits(t *testing.T) {
	for _, test := range []struct {
		name string
		got  uint64
		want uint64
	}{
		{name: "output bytes", got: OutputMaxBytes, want: 32_768},
		{name: "primary code mode output tokens", got: PrimaryCodeModeMaxOutputTokens, want: 10_000},
		{name: "stdio frame bytes", got: StdioFrameMaxBytes, want: 1_048_576},
		{name: "request ID raw bytes", got: RequestIDMaxRawBytes, want: 256},
		{name: "session requests", got: SessionMaxRequests, want: 65_536},
		{name: "used ID key bytes", got: UsedIDKeyMaxBytes, want: 272},
		{name: "used ID table slots", got: UsedIDTableSlots, want: 131_072},
		{name: "used ID arena bytes", got: UsedIDArenaMaxBytes, want: 17_825_792},
		{name: "warning examples per code", got: WarningExamplesPerCode, want: 1},
		{name: "warning summary line bytes", got: WarningSummaryLineMaxBytes, want: 128},
		{name: "warning summary bytes", got: WarningSummaryMaxBytes, want: 1_536},
		{name: "search scan line bytes", got: SearchScanLineMaxBytes, want: 4_096},
		{name: "source line bytes", got: SourceLineMaxBytes, want: 4_096},
		{name: "range read chunk bytes", got: RangeReadChunkMaxBytes, want: 4_096},
		{name: "read items", got: ReadMaxItems, want: 24},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("got %d, want %d", test.got, test.want)
			}
		})
	}

	if CursorHandoffGrace != 60*time.Second {
		t.Fatalf("CursorHandoffGrace = %s, want 60s", CursorHandoffGrace)
	}
}

func TestLoadRuntimeNumericSettings(t *testing.T) {
	for _, setting := range runtimeSettingTests() {
		t.Run(setting.name, func(t *testing.T) {
			t.Run("unset", func(t *testing.T) {
				got, err := LoadRuntime(mapLookup(nil))
				if err != nil {
					t.Fatalf("LoadRuntime() error = %v", err)
				}
				if value := setting.value(got); value != setting.defaultValue {
					t.Fatalf("value = %d, want default %d", value, setting.defaultValue)
				}
			})

			for _, valid := range []struct {
				name  string
				value uint64
			}{
				{name: "minimum", value: setting.min},
				{name: "default spelling", value: setting.defaultValue},
				{name: "maximum", value: setting.max},
			} {
				t.Run(valid.name, func(t *testing.T) {
					env := make(map[string]string)
					if setting.prepare != nil {
						setting.prepare(env, valid.value)
					}
					env[setting.name] = strconv.FormatUint(valid.value, 10)

					got, err := LoadRuntime(mapLookup(env))
					if err != nil {
						t.Fatalf("LoadRuntime() error = %v", err)
					}
					if value := setting.value(got); value != valid.value {
						t.Fatalf("value = %d, want %d", value, valid.value)
					}
				})
			}

			base := strconv.FormatUint(setting.min, 10)
			belowMin := "-1"
			if setting.min > 0 {
				belowMin = strconv.FormatUint(setting.min-1, 10)
			}
			for _, invalid := range []struct {
				name string
				raw  string
			}{
				{name: "empty", raw: ""},
				{name: "plus sign", raw: "+" + base},
				{name: "minus sign", raw: "-" + base},
				{name: "leading zero", raw: "0" + base},
				{name: "leading whitespace", raw: " " + base},
				{name: "trailing whitespace", raw: base + " "},
				{name: "suffix", raw: base + "ms"},
				{name: "fraction", raw: base + ".0"},
				{name: "uint64 overflow", raw: "18446744073709551616"},
				{name: "below minimum", raw: belowMin},
				{name: "above maximum", raw: strconv.FormatUint(setting.max+1, 10)},
			} {
				t.Run(invalid.name, func(t *testing.T) {
					got, err := LoadRuntime(mapLookup(map[string]string{setting.name: invalid.raw}))
					if err == nil {
						t.Fatalf("LoadRuntime(%q) error = nil", invalid.raw)
					}
					var invalidRuntime *InvalidRuntimeError
					if !errors.As(err, &invalidRuntime) {
						t.Fatalf("error type = %T, want *InvalidRuntimeError", err)
					}
					if err.Error() != "invalid runtime configuration" {
						t.Fatalf("error text = %q, want non-echoing fixed text", err)
					}
					if !reflect.DeepEqual(got, Runtime{}) {
						t.Fatalf("LoadRuntime() returned partial configuration: %#v", got)
					}
				})
			}
		})
	}
}

func TestLoadRuntimeIgnoresUnknownEnvironment(t *testing.T) {
	env := map[string]string{
		"MCPGODEBUG":                   "malformed",
		"MCP_HTTP_ADDR":                "malformed",
		"MCP_LOG_FILE":                 "malformed",
		"MCP_PATH_MAPS":                "malformed",
		"MCP_SENSITIVE_PATHS_ADD":      "malformed",
		"V2_OUTPUT_MAX_BYTES":          "1",
		"V2_STDIO_FRAME_MAX_BYTES":     "1",
		"V2_SESSION_MAX_REQUESTS":      "1",
		"V2_WARNING_SUMMARY_MAX_BYTES": "1",
	}

	got, err := LoadRuntime(mapLookup(env))
	if err != nil {
		t.Fatalf("LoadRuntime() error = %v", err)
	}
	if want := DefaultRuntime(); !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadRuntime() = %#v, want defaults %#v", got, want)
	}
}

func TestLoadRuntimeRejectsCrossFieldViolations(t *testing.T) {
	for _, test := range []struct {
		name string
		env  map[string]string
	}{
		{
			name: "cursor entry exceeds total",
			env: map[string]string{
				"MCP_CURSOR_MAX_ENTRY_BYTES": "67108864",
				"MCP_CURSOR_MAX_TOTAL_BYTES": "8388608",
			},
		},
		{
			name: "scan calls exceed call concurrency",
			env: map[string]string{
				"MCP_CALL_MAX_CONCURRENT": "8",
				"MCP_SCAN_MAX_CALLS":      "9",
			},
		},
		{
			name: "parse calls exceed call concurrency",
			env: map[string]string{
				"MCP_CALL_MAX_CONCURRENT": "8",
				"MCP_PARSE_MAX_CALLS":     "9",
			},
		},
		{
			name: "scan frontier exceeds cursor entry",
			env: map[string]string{
				"MCP_CURSOR_MAX_ENTRY_BYTES":  "8388608",
				"MCP_SCAN_FRONTIER_MAX_BYTES": "16777216",
			},
		},
		{
			name: "scan frontier exceeds scan bytes",
			env: map[string]string{
				"MCP_CURSOR_MAX_ENTRY_BYTES":  "67108864",
				"MCP_CURSOR_MAX_TOTAL_BYTES":  "67108864",
				"MCP_SCAN_FRONTIER_MAX_BYTES": "67108864",
				"MCP_SCAN_MAX_BYTES":          "16777216",
			},
		},
		{
			name: "parse bytes exceed scan bytes",
			env: map[string]string{
				"MCP_PARSE_MAX_BYTES": "33554432",
				"MCP_SCAN_MAX_BYTES":  "16777216",
			},
		},
		{
			name: "queue timeout exceeds scan timeout",
			env: map[string]string{
				"MCP_CALL_QUEUE_TIMEOUT_MS": "60000",
				"MCP_SCAN_TIMEOUT_MS":       "30000",
			},
		},
		{
			name: "parser cache entries zero alone",
			env: map[string]string{
				"MCP_PARSER_CACHE_MAX_ENTRIES": "0",
			},
		},
		{
			name: "parser cache bytes zero alone",
			env: map[string]string{
				"MCP_PARSER_CACHE_MAX_BYTES": "0",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			requireInvalidRuntime(t, test.env)
		})
	}
}

func TestLoadRuntimeAcceptsPairedParserCacheModes(t *testing.T) {
	for _, test := range []struct {
		name    string
		entries string
		bytes   string
	}{
		{name: "disabled", entries: "0", bytes: "0"},
		{name: "enabled minimum positive", entries: "1", bytes: "1"},
		{name: "enabled defaults", entries: "1024", bytes: "67108864"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := LoadRuntime(mapLookup(map[string]string{
				"MCP_PARSER_CACHE_MAX_ENTRIES": test.entries,
				"MCP_PARSER_CACHE_MAX_BYTES":   test.bytes,
			}))
			if err != nil {
				t.Fatalf("LoadRuntime() error = %v", err)
			}
			wantEntries, _ := strconv.ParseUint(test.entries, 10, 64)
			wantBytes, _ := strconv.ParseUint(test.bytes, 10, 64)
			if got.ParserCacheMaxEntries != wantEntries || got.ParserCacheMaxBytes != wantBytes {
				t.Fatalf("parser cache = (%d, %d), want (%d, %d)", got.ParserCacheMaxEntries, got.ParserCacheMaxBytes, wantEntries, wantBytes)
			}
		})
	}
}

func TestLoadRuntimeRetainsScanCapBeyondUint32(t *testing.T) {
	const want = uint64(4_294_967_296)
	got, err := LoadRuntime(mapLookup(map[string]string{
		"MCP_SCAN_MAX_BYTES": "4294967296",
	}))
	if err != nil {
		t.Fatalf("LoadRuntime() error = %v", err)
	}
	if got.ScanMaxBytes != want {
		t.Fatalf("ScanMaxBytes = %d, want %d", got.ScanMaxBytes, want)
	}
	if got.ScanMaxBytes <= uint64(^uint32(0)) {
		t.Fatalf("ScanMaxBytes = %d, want a value beyond 32-bit uint", got.ScanMaxBytes)
	}
}

func TestLoadRuntimeIgnoreDirsAdd(t *testing.T) {
	sixtyFour := make([]string, 64)
	for i := range sixtyFour {
		sixtyFour[i] = "dir-" + strconv.Itoa(i)
	}

	for _, test := range []struct {
		name string
		raw  string
		want []string
	}{
		{name: "empty", raw: `[]`, want: []string{}},
		{name: "64 unique", raw: mustJSONStrings(t, sixtyFour), want: sixtyFour},
		{name: "bytewise case-sensitive", raw: `["A","a"]`, want: []string{"A", "a"}},
		{name: "without Unicode normalization", raw: `["é","e\u0301"]`, want: []string{"é", "é"}},
		{name: "escaped scalars", raw: `["\u0061","\uD83D\uDE00"]`, want: []string{"a", "😀"}},
		{name: "255 ASCII bytes", raw: mustJSONStrings(t, []string{strings.Repeat("a", 255)}), want: []string{strings.Repeat("a", 255)}},
		{name: "255 Unicode bytes", raw: mustJSONStrings(t, []string{strings.Repeat("é", 127) + "a"}), want: []string{strings.Repeat("é", 127) + "a"}},
		{name: "interior space", raw: `["a b"]`, want: []string{"a b"}},
		{name: "leading space preserved", raw: `[" name"]`, want: []string{" name"}},
		{name: "trailing space preserved", raw: `["name "]`, want: []string{"name "}},
		{name: "ordinary dotted basename", raw: `[".git"]`, want: []string{".git"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := LoadRuntime(mapLookup(map[string]string{
				"MCP_IGNORE_DIRS_ADD": test.raw,
			}))
			if err != nil {
				t.Fatalf("LoadRuntime() error = %v", err)
			}
			if !reflect.DeepEqual(got.IgnoreDirsAdd, test.want) {
				t.Fatalf("IgnoreDirsAdd = %#v, want %#v", got.IgnoreDirsAdd, test.want)
			}
		})
	}
}

func TestLoadRuntimeRejectsInvalidIgnoreDirsAdd(t *testing.T) {
	sixtyFive := make([]string, 65)
	for i := range sixtyFive {
		sixtyFive[i] = "dir-" + strconv.Itoa(i)
	}
	invalidUTF8 := string([]byte{'[', '"', 0xff, '"', ']'})

	for _, test := range []struct {
		name string
		raw  string
	}{
		{name: "65 entries", raw: mustJSONStrings(t, sixtyFive)},
		{name: "duplicate", raw: `["same","same"]`},
		{name: "duplicate after decoding", raw: `["a","\u0061"]`},
		{name: "null", raw: `null`},
		{name: "object", raw: `{}`},
		{name: "non-string element", raw: `[1]`},
		{name: "trailing bytes", raw: `[] false`},
		{name: "invalid UTF-8", raw: invalidUTF8},
		{name: "empty name", raw: `[""]`},
		{name: "256 ASCII bytes", raw: mustJSONStrings(t, []string{strings.Repeat("a", 256)})},
		{name: "256 Unicode bytes", raw: mustJSONStrings(t, []string{strings.Repeat("é", 128)})},
		{name: "slash", raw: mustJSONStrings(t, []string{"a/b"})},
		{name: "backslash", raw: mustJSONStrings(t, []string{`a\b`})},
		{name: "NUL", raw: mustJSONStrings(t, []string{"a\x00b"})},
		{name: "dot", raw: `["."]`},
		{name: "dotdot", raw: `[".."]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			requireInvalidRuntime(t, map[string]string{
				"MCP_IGNORE_DIRS_ADD": test.raw,
			})
		})
	}
}

func TestLoadRuntimeIgnoreDirsAddIsolation(t *testing.T) {
	env := map[string]string{"MCP_IGNORE_DIRS_ADD": `["node_modules"]`}
	first, err := LoadRuntime(mapLookup(env))
	if err != nil {
		t.Fatalf("first LoadRuntime() error = %v", err)
	}
	if !reflect.DeepEqual(first.IgnoreDirsAdd, []string{"node_modules"}) {
		t.Fatalf("first IgnoreDirsAdd = %#v, want decoded value", first.IgnoreDirsAdd)
	}
	first.IgnoreDirsAdd[0] = "mutated"

	second, err := LoadRuntime(mapLookup(env))
	if err != nil {
		t.Fatalf("second LoadRuntime() error = %v", err)
	}
	if !reflect.DeepEqual(second.IgnoreDirsAdd, []string{"node_modules"}) {
		t.Fatalf("second IgnoreDirsAdd = %#v, want fresh decoded value", second.IgnoreDirsAdd)
	}
}

type runtimeSettingTest struct {
	name         string
	min          uint64
	defaultValue uint64
	max          uint64
	value        func(Runtime) uint64
	prepare      func(map[string]string, uint64)
}

func runtimeSettingTests() []runtimeSettingTest {
	return []runtimeSettingTest{
		{name: "MCP_CWD_TTL_SECONDS", min: 3_600, defaultValue: 604_800, max: 2_592_000, value: func(r Runtime) uint64 { return uint64(r.CWDTTL / time.Second) }},
		{name: "MCP_CWD_MAX_ENTRIES", min: 1, defaultValue: 256, max: 4_096, value: func(r Runtime) uint64 { return r.CWDMaxEntries }},
		{name: "MCP_CURSOR_TTL_SECONDS", min: 60, defaultValue: 1_800, max: 86_400, value: func(r Runtime) uint64 { return uint64(r.CursorTTL / time.Second) }},
		{name: "MCP_CURSOR_MAX_ENTRIES", min: 16, defaultValue: 1_024, max: 4_096, value: func(r Runtime) uint64 { return r.CursorMaxEntries }},
		{
			name: "MCP_CURSOR_MAX_ENTRY_BYTES", min: 1_048_576, defaultValue: 8_388_608, max: 67_108_864,
			value: func(r Runtime) uint64 { return r.CursorMaxEntryBytes },
			prepare: func(env map[string]string, _ uint64) {
				env["MCP_SCAN_FRONTIER_MAX_BYTES"] = "1048576"
			},
		},
		{name: "MCP_CURSOR_MAX_TOTAL_BYTES", min: 8_388_608, defaultValue: 67_108_864, max: 536_870_912, value: func(r Runtime) uint64 { return r.CursorMaxTotalBytes }},
		{name: "MCP_CURSOR_MAX_PAGES", min: 1, defaultValue: 256, max: 1_024, value: func(r Runtime) uint64 { return r.CursorMaxPages }},
		{
			name: "MCP_CALL_MAX_CONCURRENT", min: 1, defaultValue: 8, max: 64,
			value: func(r Runtime) uint64 { return r.CallMaxConcurrent },
			prepare: func(env map[string]string, _ uint64) {
				env["MCP_SCAN_MAX_CALLS"] = "1"
				env["MCP_PARSE_MAX_CALLS"] = "1"
			},
		},
		{name: "MCP_CALL_QUEUE_MAX", min: 0, defaultValue: 64, max: 1_024, value: func(r Runtime) uint64 { return r.CallQueueMax }},
		{
			name: "MCP_CALL_QUEUE_TIMEOUT_MS", min: 100, defaultValue: 5_000, max: 60_000,
			value: func(r Runtime) uint64 { return uint64(r.CallQueueTimeout / time.Millisecond) },
			prepare: func(env map[string]string, _ uint64) {
				env["MCP_SCAN_TIMEOUT_MS"] = "300000"
			},
		},
		{name: "MCP_SCAN_MAX_FILES", min: 1_000, defaultValue: 100_000, max: 1_000_000, value: func(r Runtime) uint64 { return r.ScanMaxFiles }},
		{name: "MCP_SCAN_MAX_DIRS", min: 1_000, defaultValue: 100_000, max: 1_000_000, value: func(r Runtime) uint64 { return r.ScanMaxDirs }},
		{name: "MCP_SCAN_MAX_BYTES", min: 16_777_216, defaultValue: 536_870_912, max: 4_294_967_296, value: func(r Runtime) uint64 { return r.ScanMaxBytes }},
		{
			name: "MCP_SCAN_TIMEOUT_MS", min: 1_000, defaultValue: 30_000, max: 300_000,
			value: func(r Runtime) uint64 { return uint64(r.ScanTimeout / time.Millisecond) },
			prepare: func(env map[string]string, _ uint64) {
				env["MCP_CALL_QUEUE_TIMEOUT_MS"] = "100"
			},
		},
		{
			name: "MCP_SCAN_MAX_CALLS", min: 1, defaultValue: 4, max: 32,
			value: func(r Runtime) uint64 { return r.ScanMaxCalls },
			prepare: func(env map[string]string, _ uint64) {
				env["MCP_CALL_MAX_CONCURRENT"] = "64"
			},
		},
		{
			name: "MCP_SCAN_FRONTIER_MAX_BYTES", min: 1_048_576, defaultValue: 8_388_608, max: 67_108_864,
			value: func(r Runtime) uint64 { return r.ScanFrontierMaxBytes },
			prepare: func(env map[string]string, _ uint64) {
				env["MCP_CURSOR_MAX_ENTRY_BYTES"] = "67108864"
				env["MCP_CURSOR_MAX_TOTAL_BYTES"] = "67108864"
				env["MCP_SCAN_MAX_BYTES"] = "4294967296"
			},
		},
		{name: "MCP_PARSE_MAX_BYTES", min: 1_048_576, defaultValue: 8_388_608, max: 33_554_432, value: func(r Runtime) uint64 { return r.ParseMaxBytes }},
		{
			name: "MCP_PARSE_MAX_CALLS", min: 1, defaultValue: 2, max: 16,
			value: func(r Runtime) uint64 { return r.ParseMaxCalls },
			prepare: func(env map[string]string, _ uint64) {
				env["MCP_CALL_MAX_CONCURRENT"] = "64"
			},
		},
		{
			name: "MCP_PARSER_CACHE_MAX_ENTRIES", min: 0, defaultValue: 1_024, max: 16_384,
			value: func(r Runtime) uint64 { return r.ParserCacheMaxEntries },
			prepare: func(env map[string]string, value uint64) {
				if value == 0 {
					env["MCP_PARSER_CACHE_MAX_BYTES"] = "0"
				}
			},
		},
		{
			name: "MCP_PARSER_CACHE_MAX_BYTES", min: 0, defaultValue: 67_108_864, max: 1_073_741_824,
			value: func(r Runtime) uint64 { return r.ParserCacheMaxBytes },
			prepare: func(env map[string]string, value uint64) {
				if value == 0 {
					env["MCP_PARSER_CACHE_MAX_ENTRIES"] = "0"
				}
			},
		},
	}
}

func mapLookup(values map[string]string) LookupEnv {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

func requireInvalidRuntime(t *testing.T, env map[string]string) {
	t.Helper()
	got, err := LoadRuntime(mapLookup(env))
	if err == nil {
		t.Fatal("LoadRuntime() error = nil, want invalid configuration")
	}
	var invalidRuntime *InvalidRuntimeError
	if !errors.As(err, &invalidRuntime) {
		t.Fatalf("error type = %T, want *InvalidRuntimeError", err)
	}
	if err.Error() != "invalid runtime configuration" {
		t.Fatalf("error text = %q, want non-echoing fixed text", err)
	}
	if !reflect.DeepEqual(got, Runtime{}) {
		t.Fatalf("LoadRuntime() returned partial configuration: %#v", got)
	}
}

func mustJSONStrings(t *testing.T, values []string) string {
	t.Helper()
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return string(raw)
}
