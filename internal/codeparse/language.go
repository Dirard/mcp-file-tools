package codeparse

import (
	"strings"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

func LanguageForPath(path string) (api.Language, bool) {
	baseStart := strings.LastIndexAny(path, "/\\") + 1
	dot := strings.LastIndexByte(path[baseStart:], '.')
	if dot < 0 {
		return "", false
	}
	extension := asciiLower(path[baseStart+dot:])
	switch extension {
	case ".md", ".markdown", ".mdown", ".mkd":
		return api.LanguageMarkdown, true
	case ".go":
		return api.LanguageGo, true
	case ".js", ".mjs", ".cjs":
		return api.LanguageJavaScript, true
	case ".jsx":
		return api.LanguageJSX, true
	case ".ts", ".mts", ".cts":
		return api.LanguageTypeScript, true
	case ".tsx":
		return api.LanguageTSX, true
	case ".py", ".pyi":
		return api.LanguagePython, true
	case ".java":
		return api.LanguageJava, true
	case ".rs":
		return api.LanguageRust, true
	case ".c", ".h":
		return api.LanguageC, true
	case ".cc", ".cpp", ".cxx", ".hh", ".hpp", ".hxx":
		return api.LanguageCPP, true
	case ".cs":
		return api.LanguageCSharp, true
	case ".rb":
		return api.LanguageRuby, true
	case ".kt", ".kts":
		return api.LanguageKotlin, true
	case ".swift":
		return api.LanguageSwift, true
	case ".sh", ".bash":
		return api.LanguageBash, true
	case ".json":
		return api.LanguageJSON, true
	case ".yaml", ".yml":
		return api.LanguageYAML, true
	case ".svelte":
		return api.LanguageSvelte, true
	default:
		return "", false
	}
}

func asciiLower(value string) string {
	buffer := []byte(value)
	for index, character := range buffer {
		if character >= 'A' && character <= 'Z' {
			buffer[index] = character + ('a' - 'A')
		}
	}
	return string(buffer)
}
