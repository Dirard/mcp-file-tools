// Package config provides configuration management for MCP file tools server.
package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	// Environment variable names
	EnvMemoryThreshold         = "MCP_MEMORY_THRESHOLD"
	EnvWriteThreshold          = "MCP_WRITE_THRESHOLD"
	EnvBatchMaxTargets         = "MCP_BATCH_MAX_TARGETS"
	EnvBatchMaxRangesPerTarget = "MCP_BATCH_MAX_RANGES_PER_TARGET"
	EnvBatchMaxRangesPerCall   = "MCP_BATCH_MAX_RANGES_PER_CALL"
	EnvBatchMaxPlannedBytes    = "MCP_BATCH_MAX_PLANNED_BYTES"
	EnvDiffPreviewMaxBytes     = "MCP_DIFF_PREVIEW_MAX_BYTES"
	EnvReadBackMaxLines        = "MCP_READ_BACK_MAX_LINES"
	EnvBoundaryPreviewMaxChars = "MCP_BOUNDARY_PREVIEW_MAX_CHARS"
	EnvReadFilesMaxItems       = "MCP_READ_FILES_MAX_ITEMS"
	EnvReadFilesMaxTotalBytes  = "MCP_READ_FILES_MAX_TOTAL_BYTES"
	EnvReadFilesMaxItemBytes   = "MCP_READ_FILES_MAX_ITEM_BYTES"
	EnvMaxToolCalls            = "MCP_MAX_TOOL_CALLS"
	EnvMaxScanCalls            = "MCP_MAX_SCAN_CALLS"
	EnvMaxLargeReadCalls       = "MCP_MAX_LARGE_READ_CALLS"
	EnvPathMaps                = "MCP_PATH_MAPS"
	EnvCwdStatePath            = "MCP_CWD_STATE_PATH"
	EnvCwdRequireExplicitState = "MCP_CWD_REQUIRE_EXPLICIT_STATE_PATH"
	EnvCwdTTLSeconds           = "MCP_CWD_TTL_SECONDS"

	// Default values
	DefaultMaxSize                 = int64(64 * 1024 * 1024) // 64MB - files smaller than this are loaded into memory
	DefaultBatchMaxTargets         = 100
	DefaultBatchMaxRangesPerTarget = 100
	DefaultBatchMaxRangesPerCall   = 500
	DefaultDiffPreviewMaxBytes     = 32768
	DefaultReadBackMaxLines        = 80
	DefaultReadFilesMaxItems       = 24
	DefaultReadFilesMaxTotalBytes  = 256 * 1024
	DefaultReadFilesMaxItemBytes   = 64 * 1024
	DefaultBoundaryPreviewMaxChars = 1000
	DefaultMaxScanCalls            = 2
	DefaultMaxLargeReadCalls       = 2
	DefaultCwdTTLSeconds           = int64(7 * 24 * 60 * 60)
)

// Config holds server configuration loaded from environment variables.
type Config struct {
	// MemoryThreshold is the threshold for loading files into memory vs streaming.
	// Files smaller than this are loaded entirely into memory for better performance.
	// Files larger use streaming I/O to reduce memory usage.
	// Also used as threshold for encoding detection mode (full vs sample).
	// Set via MCP_MEMORY_THRESHOLD environment variable.
	// Default: 67108864 (64MB)
	MemoryThreshold int64

	// WriteThreshold is the per-file safety cap for Phase 2 write/refactor tools.
	// Set via MCP_WRITE_THRESHOLD. Default: 67108864 (64MB)
	WriteThreshold int64

	// BatchMaxTargets limits targets in one batch refactor call.
	// Set via MCP_BATCH_MAX_TARGETS. Default: 100.
	BatchMaxTargets int

	// BatchMaxRangesPerTarget limits ranges in one target entry.
	// Set via MCP_BATCH_MAX_RANGES_PER_TARGET. Default: 100.
	BatchMaxRangesPerTarget int

	// BatchMaxRangesPerCall limits total ranges across one batch call.
	// Set via MCP_BATCH_MAX_RANGES_PER_CALL. Default: 500.
	BatchMaxRangesPerCall int

	// BatchMaxPlannedBytes limits aggregate bytes one batch call may plan/write.
	// Set via MCP_BATCH_MAX_PLANNED_BYTES. Default: WriteThreshold.
	BatchMaxPlannedBytes int64

	// DiffPreviewMaxBytes bounds each returned unified diff preview.
	// Set via MCP_DIFF_PREVIEW_MAX_BYTES. Default: 32768.
	DiffPreviewMaxBytes int

	// ReadBackMaxLines bounds validation read-back windows after write tools apply.
	// Set via MCP_READ_BACK_MAX_LINES. Default: 80.
	ReadBackMaxLines int

	// BoundaryPreviewMaxChars bounds write boundary preview snippets.
	// Set via MCP_BOUNDARY_PREVIEW_MAX_CHARS. Default: 1000.
	BoundaryPreviewMaxChars int

	// ReadFilesMaxItems limits the number of items in one read_files call.
	// Set via MCP_READ_FILES_MAX_ITEMS. Default: 24.
	ReadFilesMaxItems int

	// ReadFilesMaxTotalBytes caps returned batch read text across all items.
	// Set via MCP_READ_FILES_MAX_TOTAL_BYTES. Default: 262144.
	ReadFilesMaxTotalBytes int

	// ReadFilesMaxItemBytes caps returned text for one read_files item.
	// Set via MCP_READ_FILES_MAX_ITEM_BYTES. Default: 65536.
	ReadFilesMaxItemBytes int

	// MaxToolCalls limits concurrently executing tool calls.
	// Set via MCP_MAX_TOOL_CALLS. Default: min(8, max(4, runtime.NumCPU())).
	MaxToolCalls int

	// MaxScanCalls limits scan-heavy operations such as recursive glob and grep.
	// Set via MCP_MAX_SCAN_CALLS. Default: 2.
	MaxScanCalls int

	// MaxLargeReadCalls limits concurrent large read_file operations.
	// Set via MCP_MAX_LARGE_READ_CALLS. Default: 2.
	MaxLargeReadCalls int

	// PathMaps rewrites absolute paths for the same OS before normalization.
	// Cross-OS host/container rewrites are intentionally ignored so tool inputs
	// stay absolute for the OS where this server is running.
	// Set via MCP_PATH_MAPS as semicolon-separated source=target pairs.
	PathMaps []PathMap

	// CwdStatePath stores the durable cwd_id high-water allocator state.
	// Set via MCP_CWD_STATE_PATH. When unset in local runs, defaults under
	// os.UserConfigDir()/mcp-file-tools/cwd-state.sqlite.
	CwdStatePath string

	// CwdRequireExplicitStatePath requires MCP_CWD_STATE_PATH to be set.
	// Packaged/container runtimes should enable this and mount a persistent path.
	CwdRequireExplicitStatePath bool

	// CwdStateConfigError is non-empty when cwd allocator config is fail-closed.
	CwdStateConfigError string

	// CwdTTLSeconds controls how long registered cwd ids remain active in memory.
	CwdTTLSeconds int64
}

// PathMap rewrites paths beginning with Source to the corresponding Target.
// Source and Target must both be absolute paths for the server OS.
type PathMap struct {
	Source string
	Target string
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	cfg := &Config{
		MemoryThreshold:         DefaultMaxSize,
		WriteThreshold:          DefaultMaxSize,
		BatchMaxTargets:         DefaultBatchMaxTargets,
		BatchMaxRangesPerTarget: DefaultBatchMaxRangesPerTarget,
		BatchMaxRangesPerCall:   DefaultBatchMaxRangesPerCall,
		DiffPreviewMaxBytes:     DefaultDiffPreviewMaxBytes,
		ReadBackMaxLines:        DefaultReadBackMaxLines,
		BoundaryPreviewMaxChars: DefaultBoundaryPreviewMaxChars,
		ReadFilesMaxItems:       DefaultReadFilesMaxItems,
		ReadFilesMaxTotalBytes:  DefaultReadFilesMaxTotalBytes,
		ReadFilesMaxItemBytes:   DefaultReadFilesMaxItemBytes,
		MaxToolCalls:            DefaultMaxToolCalls(),
		MaxScanCalls:            DefaultMaxScanCalls,
		MaxLargeReadCalls:       DefaultMaxLargeReadCalls,
		CwdTTLSeconds:           DefaultCwdTTLSeconds,
	}
	cfg.BatchMaxPlannedBytes = cfg.WriteThreshold

	cfg.MemoryThreshold = loadPositiveInt64Env(EnvMemoryThreshold, cfg.MemoryThreshold)
	cfg.WriteThreshold = loadPositiveInt64Env(EnvWriteThreshold, cfg.WriteThreshold)
	cfg.BatchMaxPlannedBytes = cfg.WriteThreshold
	cfg.BatchMaxTargets = loadPositiveIntEnv(EnvBatchMaxTargets, cfg.BatchMaxTargets)
	cfg.BatchMaxRangesPerTarget = loadPositiveIntEnv(EnvBatchMaxRangesPerTarget, cfg.BatchMaxRangesPerTarget)
	cfg.BatchMaxRangesPerCall = loadPositiveIntEnv(EnvBatchMaxRangesPerCall, cfg.BatchMaxRangesPerCall)
	cfg.BatchMaxPlannedBytes = loadPositiveInt64Env(EnvBatchMaxPlannedBytes, cfg.BatchMaxPlannedBytes)
	cfg.DiffPreviewMaxBytes = loadPositiveIntEnv(EnvDiffPreviewMaxBytes, cfg.DiffPreviewMaxBytes)
	cfg.ReadBackMaxLines = loadPositiveIntEnv(EnvReadBackMaxLines, cfg.ReadBackMaxLines)
	cfg.BoundaryPreviewMaxChars = loadPositiveIntEnv(EnvBoundaryPreviewMaxChars, cfg.BoundaryPreviewMaxChars)
	cfg.ReadFilesMaxItems = loadPositiveIntEnv(EnvReadFilesMaxItems, cfg.ReadFilesMaxItems)
	cfg.ReadFilesMaxTotalBytes = loadPositiveIntEnv(EnvReadFilesMaxTotalBytes, cfg.ReadFilesMaxTotalBytes)
	cfg.ReadFilesMaxItemBytes = loadPositiveIntEnv(EnvReadFilesMaxItemBytes, cfg.ReadFilesMaxItemBytes)
	cfg.MaxToolCalls = loadPositiveIntEnv(EnvMaxToolCalls, cfg.MaxToolCalls)
	cfg.MaxScanCalls = loadPositiveIntEnv(EnvMaxScanCalls, cfg.MaxScanCalls)
	cfg.MaxLargeReadCalls = loadPositiveIntEnv(EnvMaxLargeReadCalls, cfg.MaxLargeReadCalls)
	cfg.PathMaps = parsePathMaps(os.Getenv(EnvPathMaps))
	cfg.CwdTTLSeconds = loadPositiveInt64Env(EnvCwdTTLSeconds, cfg.CwdTTLSeconds)
	cfg.CwdRequireExplicitStatePath, cfg.CwdStateConfigError = loadStrictBoolEnv(EnvCwdRequireExplicitState)
	cfg.CwdStatePath = loadCwdStatePath(os.Getenv(EnvCwdStatePath), cfg.CwdRequireExplicitStatePath, &cfg.CwdStateConfigError)

	return cfg
}

func DefaultMaxToolCalls() int {
	cpus := runtime.NumCPU()
	if cpus < 4 {
		return 4
	}
	if cpus > 8 {
		return 8
	}
	return cpus
}

func MaxToolCallsOrDefault(value int) int {
	if value > 0 {
		return value
	}
	return DefaultMaxToolCalls()
}

func MaxScanCallsOrDefault(value int) int {
	if value > 0 {
		return value
	}
	return DefaultMaxScanCalls
}

func MaxLargeReadCallsOrDefault(value int) int {
	if value > 0 {
		return value
	}
	return DefaultMaxLargeReadCalls
}

func loadPositiveInt64Env(name string, fallback int64) int64 {
	value, err := strconv.ParseInt(os.Getenv(name), 10, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func loadPositiveIntEnv(name string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func loadStrictBoolEnv(name string) (bool, string) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return false, ""
	}
	switch strings.ToLower(raw) {
	case "true", "1", "yes", "on":
		return true, ""
	case "false", "0", "no", "off":
		return false, ""
	default:
		return false, name + " must be one of true, false, 1, 0, yes, no, on, off"
	}
}

func loadCwdStatePath(raw string, requireExplicit bool, configError *string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		if requireExplicit {
			appendConfigError(configError, EnvCwdStatePath+" is required when "+EnvCwdRequireExplicitState+" is true")
			return ""
		}
		dir, err := os.UserConfigDir()
		if err != nil || strings.TrimSpace(dir) == "" {
			appendConfigError(configError, "cannot determine user config directory for cwd state")
			return ""
		}
		return filepath.Join(dir, "mcp-file-tools", "cwd-state.sqlite")
	}
	cleaned := filepath.Clean(value)
	if !filepath.IsAbs(cleaned) {
		appendConfigError(configError, EnvCwdStatePath+" must be an absolute path for this server OS")
		return ""
	}
	return cleaned
}

func appendConfigError(target *string, message string) {
	if target == nil || message == "" {
		return
	}
	if *target == "" {
		*target = message
		return
	}
	*target += "; " + message
}

func parsePathMaps(value string) []PathMap {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	pairs := strings.Split(value, ";")
	maps := make([]PathMap, 0, len(pairs))
	for _, pair := range pairs {
		source, target, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		source = strings.TrimSpace(source)
		target = strings.TrimSpace(target)
		if source == "" || target == "" {
			continue
		}
		maps = append(maps, PathMap{
			Source: source,
			Target: target,
		})
	}
	return maps
}
