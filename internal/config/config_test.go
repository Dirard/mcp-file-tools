package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	clearConfigEnv(t)

	cfg := Load()

	if cfg.MemoryThreshold != DefaultMaxSize {
		t.Errorf("expected default memory threshold %d, got %d", DefaultMaxSize, cfg.MemoryThreshold)
	}
	if cfg.WriteThreshold != DefaultMaxSize {
		t.Errorf("expected default write threshold %d, got %d", DefaultMaxSize, cfg.WriteThreshold)
	}
	if cfg.BatchMaxTargets != DefaultBatchMaxTargets {
		t.Errorf("expected default batch max targets %d, got %d", DefaultBatchMaxTargets, cfg.BatchMaxTargets)
	}
	if cfg.BatchMaxRangesPerTarget != DefaultBatchMaxRangesPerTarget {
		t.Errorf("expected default batch max ranges per target %d, got %d", DefaultBatchMaxRangesPerTarget, cfg.BatchMaxRangesPerTarget)
	}
	if cfg.BatchMaxRangesPerCall != DefaultBatchMaxRangesPerCall {
		t.Errorf("expected default batch max ranges per call %d, got %d", DefaultBatchMaxRangesPerCall, cfg.BatchMaxRangesPerCall)
	}
	if cfg.BatchMaxPlannedBytes != cfg.WriteThreshold {
		t.Errorf("expected default batch max planned bytes to match write threshold %d, got %d", cfg.WriteThreshold, cfg.BatchMaxPlannedBytes)
	}
	if cfg.MaxToolCalls != DefaultMaxToolCalls() {
		t.Errorf("expected default max tool calls %d, got %d", DefaultMaxToolCalls(), cfg.MaxToolCalls)
	}
	if cfg.MaxScanCalls != DefaultMaxScanCalls {
		t.Errorf("expected default max scan calls %d, got %d", DefaultMaxScanCalls, cfg.MaxScanCalls)
	}
	if cfg.MaxLargeReadCalls != DefaultMaxLargeReadCalls {
		t.Errorf("expected default max large read calls %d, got %d", DefaultMaxLargeReadCalls, cfg.MaxLargeReadCalls)
	}
	if len(cfg.PathMaps) != 0 {
		t.Errorf("expected no default path maps, got %v", cfg.PathMaps)
	}
	if cfg.CwdStatePath == "" {
		t.Error("expected default cwd state path")
	}
	if cfg.CwdRequireExplicitStatePath {
		t.Error("expected cwd explicit state path requirement to default false")
	}
	if cfg.CwdStateConfigError != "" {
		t.Errorf("expected no default cwd state config error, got %q", cfg.CwdStateConfigError)
	}
	if cfg.CwdTTLSeconds != DefaultCwdTTLSeconds {
		t.Errorf("expected default cwd ttl %d, got %d", DefaultCwdTTLSeconds, cfg.CwdTTLSeconds)
	}
}

func TestLoad_CustomLimits(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv(EnvMemoryThreshold, "134217728") // 128MB
	t.Setenv(EnvWriteThreshold, "33554432")   // 32MB
	t.Setenv(EnvBatchMaxTargets, "12")
	t.Setenv(EnvBatchMaxRangesPerTarget, "8")
	t.Setenv(EnvBatchMaxRangesPerCall, "33")
	t.Setenv(EnvBatchMaxPlannedBytes, "16777216")
	t.Setenv(EnvMaxToolCalls, "6")
	t.Setenv(EnvMaxScanCalls, "3")
	t.Setenv(EnvMaxLargeReadCalls, "4")
	t.Setenv(EnvCwdTTLSeconds, "12345")

	cfg := Load()

	if cfg.MemoryThreshold != 134217728 {
		t.Errorf("expected memory threshold 134217728, got %d", cfg.MemoryThreshold)
	}
	if cfg.WriteThreshold != 33554432 {
		t.Errorf("expected write threshold 33554432, got %d", cfg.WriteThreshold)
	}
	if cfg.BatchMaxTargets != 12 {
		t.Errorf("expected batch max targets 12, got %d", cfg.BatchMaxTargets)
	}
	if cfg.BatchMaxRangesPerTarget != 8 {
		t.Errorf("expected batch max ranges per target 8, got %d", cfg.BatchMaxRangesPerTarget)
	}
	if cfg.BatchMaxRangesPerCall != 33 {
		t.Errorf("expected batch max ranges per call 33, got %d", cfg.BatchMaxRangesPerCall)
	}
	if cfg.BatchMaxPlannedBytes != 16777216 {
		t.Errorf("expected batch max planned bytes 16777216, got %d", cfg.BatchMaxPlannedBytes)
	}
	if cfg.MaxToolCalls != 6 {
		t.Errorf("expected max tool calls 6, got %d", cfg.MaxToolCalls)
	}
	if cfg.MaxScanCalls != 3 {
		t.Errorf("expected max scan calls 3, got %d", cfg.MaxScanCalls)
	}
	if cfg.MaxLargeReadCalls != 4 {
		t.Errorf("expected max large read calls 4, got %d", cfg.MaxLargeReadCalls)
	}
	if cfg.CwdTTLSeconds != 12345 {
		t.Errorf("expected cwd ttl 12345, got %d", cfg.CwdTTLSeconds)
	}
}

func TestLoad_PathMaps(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv(EnvPathMaps, `/source=/target; /home/user=/runtime/home/user; invalid ; =missing-source ; missing-target=`)

	cfg := Load()

	if len(cfg.PathMaps) != 2 {
		t.Fatalf("expected 2 path maps, got %d: %v", len(cfg.PathMaps), cfg.PathMaps)
	}
	if cfg.PathMaps[0].Source != "/source" || cfg.PathMaps[0].Target != "/target" {
		t.Errorf("unexpected first path map: %+v", cfg.PathMaps[0])
	}
	if cfg.PathMaps[1].Source != "/home/user" || cfg.PathMaps[1].Target != "/runtime/home/user" {
		t.Errorf("unexpected second path map: %+v", cfg.PathMaps[1])
	}
}

func TestLoad_InvalidLimitsFallbackToDefaults(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv(EnvMemoryThreshold, "not-a-number")
	t.Setenv(EnvWriteThreshold, "not-a-number")
	t.Setenv(EnvBatchMaxTargets, "0")
	t.Setenv(EnvBatchMaxRangesPerTarget, "-1")
	t.Setenv(EnvBatchMaxRangesPerCall, "not-a-number")
	t.Setenv(EnvBatchMaxPlannedBytes, "-1")
	t.Setenv(EnvMaxToolCalls, "not-a-number")
	t.Setenv(EnvMaxScanCalls, "0")
	t.Setenv(EnvMaxLargeReadCalls, "-1")
	t.Setenv(EnvCwdTTLSeconds, "not-a-number")

	cfg := Load()

	if cfg.MemoryThreshold != DefaultMaxSize {
		t.Errorf("expected fallback to %d for invalid threshold, got %d", DefaultMaxSize, cfg.MemoryThreshold)
	}
	if cfg.WriteThreshold != DefaultMaxSize {
		t.Errorf("expected fallback to %d for invalid write threshold, got %d", DefaultMaxSize, cfg.WriteThreshold)
	}
	if cfg.BatchMaxTargets != DefaultBatchMaxTargets {
		t.Errorf("expected fallback to %d for invalid batch max targets, got %d", DefaultBatchMaxTargets, cfg.BatchMaxTargets)
	}
	if cfg.BatchMaxRangesPerTarget != DefaultBatchMaxRangesPerTarget {
		t.Errorf("expected fallback to %d for invalid batch max ranges per target, got %d", DefaultBatchMaxRangesPerTarget, cfg.BatchMaxRangesPerTarget)
	}
	if cfg.BatchMaxRangesPerCall != DefaultBatchMaxRangesPerCall {
		t.Errorf("expected fallback to %d for invalid batch max ranges per call, got %d", DefaultBatchMaxRangesPerCall, cfg.BatchMaxRangesPerCall)
	}
	if cfg.BatchMaxPlannedBytes != cfg.WriteThreshold {
		t.Errorf("expected fallback batch max planned bytes to match write threshold %d, got %d", cfg.WriteThreshold, cfg.BatchMaxPlannedBytes)
	}
	if cfg.MaxToolCalls != DefaultMaxToolCalls() {
		t.Errorf("expected fallback to %d for invalid max tool calls, got %d", DefaultMaxToolCalls(), cfg.MaxToolCalls)
	}
	if cfg.MaxScanCalls != DefaultMaxScanCalls {
		t.Errorf("expected fallback to %d for invalid max scan calls, got %d", DefaultMaxScanCalls, cfg.MaxScanCalls)
	}
	if cfg.MaxLargeReadCalls != DefaultMaxLargeReadCalls {
		t.Errorf("expected fallback to %d for invalid max large read calls, got %d", DefaultMaxLargeReadCalls, cfg.MaxLargeReadCalls)
	}
	if cfg.CwdTTLSeconds != DefaultCwdTTLSeconds {
		t.Errorf("expected fallback to %d for invalid cwd ttl, got %d", DefaultCwdTTLSeconds, cfg.CwdTTLSeconds)
	}
}

func TestLoad_BatchMaxPlannedBytesDefaultsToCustomWriteThreshold(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv(EnvWriteThreshold, "123456")

	cfg := Load()

	if cfg.WriteThreshold != 123456 {
		t.Errorf("expected write threshold 123456, got %d", cfg.WriteThreshold)
	}
	if cfg.BatchMaxPlannedBytes != 123456 {
		t.Errorf("expected batch max planned bytes to track write threshold 123456, got %d", cfg.BatchMaxPlannedBytes)
	}
}

func TestLoad_NegativeMemoryThreshold(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv(EnvMemoryThreshold, "-1000")

	cfg := Load()

	if cfg.MemoryThreshold != DefaultMaxSize {
		t.Errorf("expected fallback to %d for negative threshold, got %d", DefaultMaxSize, cfg.MemoryThreshold)
	}
}

func TestLoad_CwdStateConfigValidation(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv(EnvCwdStatePath, "relative-state.sqlite")
	cfg := Load()
	if cfg.CwdStatePath != "" || !strings.Contains(cfg.CwdStateConfigError, "must be an absolute path") {
		t.Fatalf("relative cwd state path should fail closed: %#v", cfg)
	}

	clearConfigEnv(t)
	t.Setenv(EnvCwdRequireExplicitState, "true")
	cfg = Load()
	if cfg.CwdStatePath != "" || !strings.Contains(cfg.CwdStateConfigError, "is required") {
		t.Fatalf("explicit cwd state path requirement should fail when missing: %#v", cfg)
	}

	clearConfigEnv(t)
	t.Setenv(EnvCwdRequireExplicitState, "maybe")
	cfg = Load()
	if !strings.Contains(cfg.CwdStateConfigError, "must be one of") {
		t.Fatalf("malformed cwd explicit bool should fail closed: %#v", cfg)
	}

	clearConfigEnv(t)
	statePath := filepath.Join(t.TempDir(), "cwd-state.sqlite")
	t.Setenv(EnvCwdRequireExplicitState, "yes")
	t.Setenv(EnvCwdStatePath, statePath)
	cfg = Load()
	if cfg.CwdStateConfigError != "" || cfg.CwdStatePath != statePath || !cfg.CwdRequireExplicitStatePath {
		t.Fatalf("absolute explicit cwd state path should be accepted: %#v", cfg)
	}
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv(EnvMemoryThreshold, "")
	t.Setenv(EnvWriteThreshold, "")
	t.Setenv(EnvBatchMaxTargets, "")
	t.Setenv(EnvBatchMaxRangesPerTarget, "")
	t.Setenv(EnvBatchMaxRangesPerCall, "")
	t.Setenv(EnvBatchMaxPlannedBytes, "")
	t.Setenv(EnvMaxToolCalls, "")
	t.Setenv(EnvMaxScanCalls, "")
	t.Setenv(EnvMaxLargeReadCalls, "")
	t.Setenv(EnvPathMaps, "")
	t.Setenv(EnvCwdStatePath, "")
	t.Setenv(EnvCwdRequireExplicitState, "")
	t.Setenv(EnvCwdTTLSeconds, "")
}
