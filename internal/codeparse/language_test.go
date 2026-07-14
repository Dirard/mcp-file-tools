package codeparse

import (
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

func TestLanguageForPathUsesClosedExtensionTable(t *testing.T) {
	tests := map[string]api.Language{
		"a.md": api.LanguageMarkdown, "a.markdown": api.LanguageMarkdown, "a.mdown": api.LanguageMarkdown, "a.mkd": api.LanguageMarkdown,
		"a.go": api.LanguageGo,
		"a.js": api.LanguageJavaScript, "a.mjs": api.LanguageJavaScript, "a.cjs": api.LanguageJavaScript,
		"a.jsx": api.LanguageJSX,
		"a.ts":  api.LanguageTypeScript, "a.mts": api.LanguageTypeScript, "a.cts": api.LanguageTypeScript,
		"a.tsx": api.LanguageTSX,
		"a.py":  api.LanguagePython, "a.pyi": api.LanguagePython,
		"a.java": api.LanguageJava,
		"a.rs":   api.LanguageRust,
		"a.c":    api.LanguageC, "a.h": api.LanguageC,
		"a.cc": api.LanguageCPP, "a.cpp": api.LanguageCPP, "a.cxx": api.LanguageCPP, "a.hh": api.LanguageCPP, "a.hpp": api.LanguageCPP, "a.hxx": api.LanguageCPP,
		"a.cs": api.LanguageCSharp,
		"a.rb": api.LanguageRuby,
		"a.kt": api.LanguageKotlin, "a.kts": api.LanguageKotlin,
		"a.swift": api.LanguageSwift,
		"a.sh":    api.LanguageBash, "a.bash": api.LanguageBash,
		"a.json": api.LanguageJSON,
		"a.yaml": api.LanguageYAML, "a.yml": api.LanguageYAML,
		"a.svelte": api.LanguageSvelte,
	}
	for path, want := range tests {
		got, ok := LanguageForPath("dir/" + path)
		if !ok || got != want {
			t.Fatalf("LanguageForPath(%q) = %q,%t, want %q,true", path, got, ok, want)
		}
	}
	if got, ok := LanguageForPath("DIR/FILE.TSX"); !ok || got != api.LanguageTSX {
		t.Fatalf("ASCII uppercase = %q,%t", got, ok)
	}
	for _, path := range []string{"README", "script.txt", "file.go.backup", "file.İTS", ""} {
		if got, ok := LanguageForPath(path); ok {
			t.Fatalf("unsupported %q mapped to %q", path, got)
		}
	}
}
