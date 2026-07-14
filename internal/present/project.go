package present

import (
	"strings"
	"unicode/utf8"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/config"
)

type ProjectEntryKind uint8

const (
	ProjectDirectory ProjectEntryKind = iota + 1
	ProjectFile
)

type ProjectEntry struct {
	Kind ProjectEntryKind
	Path string
}

type ProjectPage struct {
	Path     string
	Status   Status
	Cursor   Cursor
	Entries  []ProjectEntry
	Warnings []Warning
}

func RenderProject(input ProjectPage) (Page, error) {
	return renderProjectPage(input, config.OutputMaxBytes)
}

func renderProjectPage(input ProjectPage, maxBytes uint64) (Page, error) {
	text, err := renderProjectLimit(input, maxBytes)
	if err != nil {
		return Page{}, errInvalidPresentation
	}
	result := api.Navigation(string(text), false)
	if result.Validate() != nil {
		return Page{}, errInvalidPresentation
	}
	return Page{
		Result:   result,
		Rows:     uint64(len(input.Entries)),
		Complete: input.Status == Complete,
	}, nil
}

func renderProject(input ProjectPage) ([]byte, error) {
	return renderProjectLimit(input, config.OutputMaxBytes)
}

func renderProjectLimit(input ProjectPage, maxBytes uint64) ([]byte, error) {
	if !validPresentPath(input.Path) || !validStatusCursor(input.Status, input.Cursor) || !validProjectEntries(input.Entries) {
		return nil, errInvalidPresentation
	}
	warnings, err := normalizeWarnings(input.Status, input.Warnings)
	if err != nil {
		return nil, err
	}

	buffer := newOutputBuffer(maxBytes)
	buffer.appendString("@@project\t")
	if err := buffer.quote(input.Path); err != nil {
		return nil, err
	}
	buffer.appendByte('\t')
	buffer.appendString(statusName(input.Status))
	buffer.appendString("\trows=")
	buffer.appendUint(uint64(len(input.Entries)))
	buffer.appendString(cursorField(input.Status, input.Cursor))
	buffer.appendByte('\n')

	for _, entry := range input.Entries {
		if entry.Kind == ProjectDirectory {
			buffer.appendString("D\t")
		} else {
			buffer.appendString("F\t")
		}
		if err := buffer.quote(entry.Path); err != nil {
			return nil, err
		}
		buffer.appendByte('\n')
	}
	if err := appendBroadWarningsBuffer(buffer, warnings); err != nil {
		return nil, err
	}
	return buffer.finish()
}

func validProjectEntries(entries []ProjectEntry) bool {
	for index, entry := range entries {
		if (entry.Kind != ProjectDirectory && entry.Kind != ProjectFile) || !validPresentPath(entry.Path) {
			return false
		}
		if index > 0 && compareProjectEntries(entries[index-1], entry) >= 0 {
			return false
		}
	}
	return true
}

func compareProjectEntries(left, right ProjectEntry) int {
	leftRoot := left.Path == "."
	rightRoot := right.Path == "."
	if leftRoot != rightRoot {
		if leftRoot {
			return -1
		}
		return 1
	}
	if pathOrder := strings.Compare(left.Path, right.Path); pathOrder != 0 {
		return pathOrder
	}
	if left.Kind < right.Kind {
		return -1
	}
	if left.Kind > right.Kind {
		return 1
	}
	return 0
}

func validPresentPath(path string) bool {
	return path != "" && len(path) <= api.InputStringMaxBytes && utf8.ValidString(path)
}

func appendStatus(dst []byte, status Status) []byte {
	return append(dst, statusName(status)...)
}

func appendHeaderCursor(dst []byte, status Status, cursor Cursor) []byte {
	return append(dst, cursorField(status, cursor)...)
}

func statusName(status Status) string {
	if status == Complete {
		return "complete"
	}
	return "partial"
}

func cursorField(status Status, cursor Cursor) string {
	if status == Partial {
		return "\tcursor=" + string(cursor)
	}
	return ""
}
