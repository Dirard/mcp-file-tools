package handler

import (
	"path/filepath"
	"strings"
)

func isSourceLikePath(path string) bool {
	switch lowerPathExtension(path) {
	case ".go", ".js", ".jsx", ".ts", ".tsx", ".py", ".svelte":
		return true
	default:
		return false
	}
}

func isConfigLikePath(path string) bool {
	switch lowerPathExtension(path) {
	case ".json", ".jsonc", ".yaml", ".yml", ".toml", ".ini", ".cfg", ".conf":
		return true
	default:
		return false
	}
}

func isTextLikePath(path string) bool {
	if isSourceLikePath(path) || isConfigLikePath(path) {
		return true
	}
	switch lowerPathExtension(path) {
	case ".md", ".markdown", ".txt", ".rst", ".csv", ".tsv":
		return true
	default:
		return false
	}
}

func lowerPathExtension(path string) string {
	return strings.ToLower(filepath.Ext(strings.TrimSpace(path)))
}
