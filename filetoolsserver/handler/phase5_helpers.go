package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	redactionOff    = "off"
	redactionAuto   = "auto"
	redactionStrict = "strict"

	backupRediscoveryGlob = ".*.bak"
)

var (
	secretKeyValuePattern  = regexp.MustCompile(`(?i)\b([A-Z0-9_.-]*(?:secret|token|password|passwd|pwd|api[_-]?key|access[_-]?key|private[_-]?key|client[_-]?secret)[A-Z0-9_.-]*)\s*([:=])\s*("[^"\r\n]*"|'[^'\r\n]*'|[^\s#,\]\}]+)`)
	bearerTokenPattern     = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[A-Za-z0-9._~+/\-=]{12,}`)
	privateKeyBeginPattern = regexp.MustCompile(`(?i)-----BEGIN [A-Z ]*PRIVATE KEY-----`)
)

func normalizeRedactionMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", redactionOff:
		return redactionOff, nil
	case redactionAuto, redactionStrict:
		return redactionStrict, nil
	default:
		return "", fmt.Errorf("invalid_redaction_mode: use off, strict, or auto")
	}
}

func stricterRedactionMode(parent, child string) (string, error) {
	parentMode, err := normalizeRedactionMode(parent)
	if err != nil {
		return "", err
	}
	childMode, err := normalizeRedactionMode(child)
	if err != nil {
		return "", err
	}
	if parentMode == redactionStrict || childMode == redactionStrict {
		return redactionStrict, nil
	}
	return redactionOff, nil
}

func isRiskyContentPath(path string) bool {
	if hasHiddenPathSegment(path) {
		return true
	}
	name := strings.ToLower(filepath.Base(path))
	ext := strings.ToLower(filepath.Ext(name))
	if name == ".env" || strings.HasPrefix(name, ".env.") || strings.Contains(name, "kubeconfig") {
		return true
	}
	switch ext {
	case ".env", ".log", ".pem", ".key", ".crt", ".p12", ".pfx":
		return true
	default:
		return false
	}
}

func hasHiddenPathSegment(path string) bool {
	for _, segment := range normalizedPathSegments(path) {
		if strings.HasPrefix(segment, ".") && segment != "." && segment != ".." {
			return true
		}
	}
	return false
}

func hasVCSPathSegment(path string) bool {
	for _, segment := range normalizedPathSegments(path) {
		if isVCSDirectoryName(segment) {
			return true
		}
	}
	return false
}

func normalizedPathSegments(path string) []string {
	normalized := strings.ReplaceAll(path, "\\", "/")
	parts := strings.Split(normalized, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.HasSuffix(part, ":") {
			continue
		}
		out = append(out, part)
	}
	return out
}

func shouldRedactContent(mode string, path string, broad bool, includeHidden bool) bool {
	normalized, err := normalizeRedactionMode(mode)
	if err != nil {
		return false
	}
	if normalized == redactionStrict {
		return true
	}
	return false
}

func redactString(value string, mode string, risky bool) (string, bool) {
	normalized, err := normalizeRedactionMode(mode)
	if err != nil {
		return value, false
	}
	if normalized == redactionOff {
		return value, false
	}
	if normalized != redactionStrict && !risky && !containsSecretLike(value) {
		return value, false
	}
	redacted := redactSecretKeyValues(value)
	redacted = bearerTokenPattern.ReplaceAllString(redacted, `${1} [REDACTED]`)
	redacted = privateKeyBeginPattern.ReplaceAllString(redacted, "-----BEGIN [REDACTED]-----")
	return redacted, redacted != value
}

func redactSecretKeyValues(value string) string {
	matches := secretKeyValuePattern.FindAllStringSubmatchIndex(value, -1)
	if len(matches) == 0 {
		return value
	}
	var b strings.Builder
	last := 0
	for _, match := range matches {
		if len(match) < 8 || match[0] < last {
			continue
		}
		key := value[match[2]:match[3]]
		sep := value[match[4]:match[5]]
		rawValue := value[match[6]:match[7]]
		b.WriteString(value[last:match[0]])
		if shouldRedactKeyValue(key, rawValue) {
			b.WriteString(key)
			b.WriteString(sep)
			b.WriteString("[REDACTED]")
		} else {
			b.WriteString(value[match[0]:match[1]])
		}
		last = match[1]
	}
	b.WriteString(value[last:])
	return b.String()
}

func shouldRedactKeyValue(key, rawValue string) bool {
	cleanValue := strings.Trim(strings.TrimSpace(rawValue), "\"'`")
	if cleanValue == "" {
		return false
	}
	if isBenignTokenCounterKey(key) && isPlainNumber(cleanValue) {
		return false
	}
	if isPlainNumber(cleanValue) || cleanValue == "true" || cleanValue == "false" {
		return false
	}
	return len(cleanValue) >= 8 || likelyHighEntropy(cleanValue)
}

func isBenignTokenCounterKey(key string) bool {
	lower := strings.ToLower(key)
	return strings.Contains(lower, "output_tokens") ||
		strings.Contains(lower, "input_tokens") ||
		strings.Contains(lower, "max_tokens") ||
		strings.Contains(lower, "max_output_tokens") ||
		strings.Contains(lower, "total_tokens")
}

func isPlainNumber(value string) bool {
	if value == "" {
		return false
	}
	dotSeen := false
	for i, r := range value {
		if r == '-' && i == 0 {
			continue
		}
		if r == '.' && !dotSeen {
			dotSeen = true
			continue
		}
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func containsSecretLike(value string) bool {
	return secretKeyValuePattern.MatchString(value) ||
		bearerTokenPattern.MatchString(value) ||
		privateKeyBeginPattern.MatchString(value)
}

func likelyHighEntropy(value string) bool {
	if len(value) < 32 {
		return false
	}
	classes := 0
	if regexp.MustCompile(`[a-z]`).MatchString(value) {
		classes++
	}
	if regexp.MustCompile(`[A-Z]`).MatchString(value) {
		classes++
	}
	if regexp.MustCompile(`[0-9]`).MatchString(value) {
		classes++
	}
	if regexp.MustCompile(`[+/=_-]`).MatchString(value) {
		classes++
	}
	return classes >= 2
}

func canonicalHash(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		data = []byte(fmt.Sprintf("%#v", v))
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func withPrimaryHintList(hints []ActionHint) (*ActionHint, []ActionHint) {
	if len(hints) == 0 {
		return nil, nil
	}
	first := hints[0]
	return &first, hints
}

func sortedStringSet(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func backupDiscoveryForResults(pathCtx PathContext, results []BackupResult) *BackupDiscoveryHint {
	type groupKey struct {
		role string
		dir  string
	}
	groups := map[groupKey][]string{}
	backupPaths := []string{}
	for _, result := range results {
		if !result.Created || result.BackupPath == "" {
			continue
		}
		projectedBackup := slashPath(result.BackupPath)
		backupPaths = append(backupPaths, projectedBackup)
		dir := slashPath(filepath.Dir(result.BackupPath))
		key := groupKey{role: result.Role, dir: dir}
		groups[key] = append(groups[key], projectedBackup)
	}
	if len(backupPaths) == 0 {
		return nil
	}
	sort.Strings(backupPaths)
	keys := make([]groupKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].dir == keys[j].dir {
			return keys[i].role < keys[j].role
		}
		return keys[i].dir < keys[j].dir
	})
	discoveryGroups := make([]BackupDiscoveryGroup, 0, len(keys))
	hints := make([]ActionHint, 0, len(keys))
	for _, key := range keys {
		paths := append([]string(nil), groups[key]...)
		sort.Strings(paths)
		input := map[string]any{
			"target_directory": key.dir,
			"glob_pattern":     backupRediscoveryGlob,
			"include_hidden":   true,
		}
		addCwdIDToRecommendedInput(pathCtx, "glob_file_search", input)
		hint := ActionHint{
			SafeToRetry:                true,
			RecommendedNextTool:        "glob_file_search",
			RecommendedNextInput:       input,
			RecommendedNextInputPolicy: "rediscover_sidecar_backups",
			Reason:                     "Rediscover hidden sidecar backups created by this write.",
		}
		discoveryGroups = append(discoveryGroups, BackupDiscoveryGroup{
			Role:                key.role,
			Directory:           key.dir,
			GlobPattern:         backupRediscoveryGlob,
			IncludeHidden:       true,
			BackupPaths:         paths,
			NextRecommendedCall: hint,
		})
		hints = append(hints, hint)
	}
	primary, all := withPrimaryHintList(hints)
	return &BackupDiscoveryHint{
		BackupPaths:          backupPaths,
		DiscoveryGroups:      discoveryGroups,
		NextRecommendedCall:  primary,
		NextRecommendedCalls: all,
		Reason:               "Sidecar backups are hidden dot-files; use include_hidden=true to rediscover them.",
	}
}
