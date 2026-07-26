package config

import (
	"strconv"
	"strings"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/jsonwire"
)

const (
	OutputMaxBytes                 uint64        = 32_768
	ReadOutputMaxBytes             uint64        = 1_048_576
	PrimaryCodeModeMaxOutputTokens uint64        = 10_000
	StdioFrameMaxBytes             uint64        = 1_048_576
	RequestIDMaxRawBytes           uint64        = 256
	SessionMaxRequests             uint64        = 65_536
	UsedIDKeyMaxBytes              uint64        = 272
	UsedIDTableSlots               uint64        = 131_072
	UsedIDArenaMaxBytes            uint64        = 17_825_792
	CursorHandoffGrace             time.Duration = 60 * time.Second
	WarningExamplesPerCode         uint64        = 1
	WarningSummaryLineMaxBytes     uint64        = 128
	WarningSummaryMaxBytes         uint64        = 1_536
	SearchScanLineMaxBytes         uint64        = 4_096
	SourceLineMaxBytes             uint64        = 4_096
	RangeReadChunkMaxBytes         uint64        = 4_096
	ReadMaxItems                   uint64        = 24
)

const warningCodeCount uint64 = 12

// Paired unsigned subtractions make equality a compile-time requirement.
const (
	_ uint64 = UsedIDArenaMaxBytes - SessionMaxRequests*UsedIDKeyMaxBytes
	_ uint64 = SessionMaxRequests*UsedIDKeyMaxBytes - UsedIDArenaMaxBytes
	_ uint64 = WarningSummaryMaxBytes - warningCodeCount*WarningSummaryLineMaxBytes
	_ uint64 = warningCodeCount*WarningSummaryLineMaxBytes - WarningSummaryMaxBytes
)

// Runtime is the closed v2 startup configuration.
type Runtime struct {
	CWDTTL                time.Duration
	CWDMaxEntries         uint64
	CursorTTL             time.Duration
	CursorMaxEntries      uint64
	CursorMaxEntryBytes   uint64
	CursorMaxTotalBytes   uint64
	CursorMaxPages        uint64
	CallMaxConcurrent     uint64
	CallQueueMax          uint64
	CallQueueTimeout      time.Duration
	ScanMaxFiles          uint64
	ScanMaxDirs           uint64
	ScanMaxBytes          uint64
	ScanTimeout           time.Duration
	ScanMaxCalls          uint64
	ScanFrontierMaxBytes  uint64
	ParseMaxBytes         uint64
	ParseMaxCalls         uint64
	ParserCacheMaxEntries uint64
	ParserCacheMaxBytes   uint64
	IgnoreDirsAdd         []string
}

// LookupEnv looks up one allowlisted environment variable.
type LookupEnv func(string) (string, bool)

// InvalidRuntimeError reports invalid startup configuration without echoing it.
type InvalidRuntimeError struct{}

func (*InvalidRuntimeError) Error() string {
	return "invalid runtime configuration"
}

// DefaultRuntime returns the fixed v2 startup defaults.
func DefaultRuntime() Runtime {
	return Runtime{
		CWDTTL:                604_800 * time.Second,
		CWDMaxEntries:         256,
		CursorTTL:             1_800 * time.Second,
		CursorMaxEntries:      1_024,
		CursorMaxEntryBytes:   8_388_608,
		CursorMaxTotalBytes:   67_108_864,
		CursorMaxPages:        256,
		CallMaxConcurrent:     8,
		CallQueueMax:          64,
		CallQueueTimeout:      5_000 * time.Millisecond,
		ScanMaxFiles:          100_000,
		ScanMaxDirs:           100_000,
		ScanMaxBytes:          536_870_912,
		ScanTimeout:           30_000 * time.Millisecond,
		ScanMaxCalls:          4,
		ScanFrontierMaxBytes:  8_388_608,
		ParseMaxBytes:         8_388_608,
		ParseMaxCalls:         2,
		ParserCacheMaxEntries: 1_024,
		ParserCacheMaxBytes:   67_108_864,
		IgnoreDirsAdd:         []string{},
	}
}

// LoadRuntime reads only the closed v2 startup surface.
func LoadRuntime(lookup LookupEnv) (Runtime, error) {
	runtime := DefaultRuntime()
	for _, setting := range runtimeNumericSettings() {
		raw, present := lookup(setting.name)
		if !present {
			continue
		}
		value, ok := parseRuntimeUint(raw, setting.min, setting.max)
		if !ok || !setting.assign(&runtime, value) {
			return Runtime{}, &InvalidRuntimeError{}
		}
	}
	if raw, present := lookup("MCP_IGNORE_DIRS_ADD"); present {
		ignoreDirs, err := jsonwire.DecodeStringArray([]byte(raw))
		if err != nil || !validIgnoreDirsAdd(ignoreDirs) {
			return Runtime{}, &InvalidRuntimeError{}
		}
		runtime.IgnoreDirsAdd = ignoreDirs
	}
	if !validRuntime(runtime) {
		return Runtime{}, &InvalidRuntimeError{}
	}
	return runtime, nil
}

func validRuntime(runtime Runtime) bool {
	if runtime.CursorMaxEntryBytes > runtime.CursorMaxTotalBytes {
		return false
	}
	if runtime.ScanMaxCalls > runtime.CallMaxConcurrent || runtime.ParseMaxCalls > runtime.CallMaxConcurrent {
		return false
	}
	if runtime.ScanFrontierMaxBytes > runtime.CursorMaxEntryBytes || runtime.ScanFrontierMaxBytes > runtime.ScanMaxBytes {
		return false
	}
	if runtime.ParseMaxBytes > runtime.ScanMaxBytes {
		return false
	}
	if runtime.CallQueueTimeout > runtime.ScanTimeout {
		return false
	}
	if (runtime.ParserCacheMaxEntries == 0) != (runtime.ParserCacheMaxBytes == 0) {
		return false
	}
	return true
}

func validIgnoreDirsAdd(names []string) bool {
	if len(names) > 64 {
		return false
	}
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if len(name) == 0 || len(name) > 255 {
			return false
		}
		if name == "." || name == ".." {
			return false
		}
		if strings.ContainsAny(name, "/\\\x00") {
			return false
		}
		if _, duplicate := seen[name]; duplicate {
			return false
		}
		seen[name] = struct{}{}
	}
	return true
}

type runtimeNumericSetting struct {
	name   string
	min    uint64
	max    uint64
	assign func(*Runtime, uint64) bool
}

func runtimeNumericSettings() [20]runtimeNumericSetting {
	return [20]runtimeNumericSetting{
		{
			name: "MCP_CWD_TTL_SECONDS", min: 3_600, max: 2_592_000,
			assign: func(runtime *Runtime, value uint64) bool {
				duration, ok := durationFromUnits(value, time.Second)
				runtime.CWDTTL = duration
				return ok
			},
		},
		{
			name: "MCP_CWD_MAX_ENTRIES", min: 1, max: 4_096,
			assign: func(runtime *Runtime, value uint64) bool {
				runtime.CWDMaxEntries = value
				return true
			},
		},
		{
			name: "MCP_CURSOR_TTL_SECONDS", min: 60, max: 86_400,
			assign: func(runtime *Runtime, value uint64) bool {
				duration, ok := durationFromUnits(value, time.Second)
				runtime.CursorTTL = duration
				return ok
			},
		},
		{
			name: "MCP_CURSOR_MAX_ENTRIES", min: 16, max: 4_096,
			assign: func(runtime *Runtime, value uint64) bool {
				runtime.CursorMaxEntries = value
				return true
			},
		},
		{
			name: "MCP_CURSOR_MAX_ENTRY_BYTES", min: 1_048_576, max: 67_108_864,
			assign: func(runtime *Runtime, value uint64) bool {
				runtime.CursorMaxEntryBytes = value
				return true
			},
		},
		{
			name: "MCP_CURSOR_MAX_TOTAL_BYTES", min: 8_388_608, max: 536_870_912,
			assign: func(runtime *Runtime, value uint64) bool {
				runtime.CursorMaxTotalBytes = value
				return true
			},
		},
		{
			name: "MCP_CURSOR_MAX_PAGES", min: 1, max: 1_024,
			assign: func(runtime *Runtime, value uint64) bool {
				runtime.CursorMaxPages = value
				return true
			},
		},
		{
			name: "MCP_CALL_MAX_CONCURRENT", min: 1, max: 64,
			assign: func(runtime *Runtime, value uint64) bool {
				runtime.CallMaxConcurrent = value
				return true
			},
		},
		{
			name: "MCP_CALL_QUEUE_MAX", min: 0, max: 1_024,
			assign: func(runtime *Runtime, value uint64) bool {
				runtime.CallQueueMax = value
				return true
			},
		},
		{
			name: "MCP_CALL_QUEUE_TIMEOUT_MS", min: 100, max: 60_000,
			assign: func(runtime *Runtime, value uint64) bool {
				duration, ok := durationFromUnits(value, time.Millisecond)
				runtime.CallQueueTimeout = duration
				return ok
			},
		},
		{
			name: "MCP_SCAN_MAX_FILES", min: 1_000, max: 1_000_000,
			assign: func(runtime *Runtime, value uint64) bool {
				runtime.ScanMaxFiles = value
				return true
			},
		},
		{
			name: "MCP_SCAN_MAX_DIRS", min: 1_000, max: 1_000_000,
			assign: func(runtime *Runtime, value uint64) bool {
				runtime.ScanMaxDirs = value
				return true
			},
		},
		{
			name: "MCP_SCAN_MAX_BYTES", min: 16_777_216, max: 4_294_967_296,
			assign: func(runtime *Runtime, value uint64) bool {
				runtime.ScanMaxBytes = value
				return true
			},
		},
		{
			name: "MCP_SCAN_TIMEOUT_MS", min: 1_000, max: 300_000,
			assign: func(runtime *Runtime, value uint64) bool {
				duration, ok := durationFromUnits(value, time.Millisecond)
				runtime.ScanTimeout = duration
				return ok
			},
		},
		{
			name: "MCP_SCAN_MAX_CALLS", min: 1, max: 32,
			assign: func(runtime *Runtime, value uint64) bool {
				runtime.ScanMaxCalls = value
				return true
			},
		},
		{
			name: "MCP_SCAN_FRONTIER_MAX_BYTES", min: 1_048_576, max: 67_108_864,
			assign: func(runtime *Runtime, value uint64) bool {
				runtime.ScanFrontierMaxBytes = value
				return true
			},
		},
		{
			name: "MCP_PARSE_MAX_BYTES", min: 1_048_576, max: 33_554_432,
			assign: func(runtime *Runtime, value uint64) bool {
				runtime.ParseMaxBytes = value
				return true
			},
		},
		{
			name: "MCP_PARSE_MAX_CALLS", min: 1, max: 16,
			assign: func(runtime *Runtime, value uint64) bool {
				runtime.ParseMaxCalls = value
				return true
			},
		},
		{
			name: "MCP_PARSER_CACHE_MAX_ENTRIES", min: 0, max: 16_384,
			assign: func(runtime *Runtime, value uint64) bool {
				runtime.ParserCacheMaxEntries = value
				return true
			},
		},
		{
			name: "MCP_PARSER_CACHE_MAX_BYTES", min: 0, max: 1_073_741_824,
			assign: func(runtime *Runtime, value uint64) bool {
				runtime.ParserCacheMaxBytes = value
				return true
			},
		},
	}
}

func parseRuntimeUint(raw string, minValue, maxValue uint64) (uint64, bool) {
	if raw == "" {
		return 0, false
	}
	if raw[0] == '0' {
		if len(raw) != 1 {
			return 0, false
		}
	} else if raw[0] < '1' || raw[0] > '9' {
		return 0, false
	}
	for i := 1; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return 0, false
		}
	}

	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value < minValue || value > maxValue {
		return 0, false
	}
	return value, true
}

func durationFromUnits(value uint64, unit time.Duration) (time.Duration, bool) {
	const maxDuration = time.Duration(1<<63 - 1)
	if value > uint64(maxDuration/unit) {
		return 0, false
	}
	return time.Duration(value) * unit, true
}
