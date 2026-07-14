package scanner

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Dirard/mcp-file-tools/internal/rootfs"
)

var infrastructureBasenames = [...]string{".git", ".hg", ".svn"}

var ordinaryIgnoredDirectories = [...]string{
	"node_modules", "vendor", ".venv", "venv", "target", "dist", "build", "out",
	".cache", "__pycache__", "coverage", ".coverage", ".next", ".nuxt",
}

type ignorePolicy struct {
	extra []string
}

func newIgnorePolicy(extra []string) (ignorePolicy, bool) {
	if len(extra) > 64 {
		return ignorePolicy{}, false
	}
	copyOfExtra := append([]string(nil), extra...)
	for _, name := range copyOfExtra {
		if name == "" || name == "." || name == ".." || len(name) > 255 ||
			!utf8.ValidString(name) || strings.ContainsAny(name, "/\\\x00") {
			return ignorePolicy{}, false
		}
	}
	sort.Strings(copyOfExtra)
	for index := 1; index < len(copyOfExtra); index++ {
		if copyOfExtra[index] == copyOfExtra[index-1] {
			return ignorePolicy{}, false
		}
	}
	return ignorePolicy{extra: copyOfExtra}, true
}

func (policy ignorePolicy) skip(name string, kind rootfs.EntryKind, includeIgnored bool) bool {
	for _, infrastructure := range infrastructureBasenames {
		if name == infrastructure {
			return true
		}
	}
	if includeIgnored || kind != rootfs.EntryDir {
		return false
	}
	for _, ignored := range ordinaryIgnoredDirectories {
		if name == ignored {
			return true
		}
	}
	index := sort.SearchStrings(policy.extra, name)
	return index < len(policy.extra) && policy.extra[index] == name
}
