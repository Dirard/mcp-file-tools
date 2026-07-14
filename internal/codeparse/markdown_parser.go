package codeparse

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/Dirard/mcp-file-tools/internal/navmodel"
)

var markdownATXHeading = regexp.MustCompile(`^( {0,3})(#{1,6})(?:[ \t]+|$)(.*)$`)

type markdownSection struct {
	level uint16
	start uint32
	end   uint32
	name  string
	kind  string
}

func parseMarkdown(source []byte) parseOutput {
	lines := canonicalLines(source)
	if len(lines) == 0 {
		return parseOutput{records: []rawRecord{}}
	}

	sections := make([]markdownSection, 0)
	inFrontmatter := strings.TrimSpace(lines[0]) == "---"
	frontmatterStart := uint32(0)
	if inFrontmatter {
		frontmatterStart = 1
	}
	inFence := false
	var fenceMarker byte
	fenceLength := 0

	for index, line := range lines {
		lineNumber := uint32(index + 1)
		if lineNumber == 1 && inFrontmatter {
			continue
		}
		if inFrontmatter {
			if strings.TrimSpace(line) == "---" {
				sections = append(sections, markdownSection{start: frontmatterStart, end: lineNumber, name: "frontmatter", kind: "frontmatter"})
				inFrontmatter = false
			}
			continue
		}

		trimmedLeft := strings.TrimLeft(line, " ")
		if len(line)-len(trimmedLeft) <= 3 {
			if marker, length, ok := markdownFence(trimmedLeft); ok {
				if !inFence {
					inFence = true
					fenceMarker = marker
					fenceLength = length
				} else if marker == fenceMarker && length >= fenceLength && strings.Trim(trimmedLeft[length:], " \t") == "" {
					inFence = false
					fenceMarker = 0
					fenceLength = 0
				}
				continue
			}
		}
		if inFence {
			continue
		}

		match := markdownATXHeading.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		level := uint16(len(match[2]))
		name := cleanMarkdownTitle(match[3])
		if name == "" {
			name = strings.Repeat("#", int(level))
		}
		for previous := range sections {
			if sections[previous].end == 0 && sections[previous].level > 0 && level <= sections[previous].level {
				sections[previous].end = lineNumber - 1
			}
		}
		sections = append(sections, markdownSection{level: level, start: lineNumber, name: name, kind: "section"})
	}

	lastLine := uint32(len(lines))
	records := make([]rawRecord, 0, len(sections))
	for _, section := range sections {
		if section.end == 0 {
			section.end = lastLine
		}
		records = append(records, rawRecord{
			kind:      section.kind,
			lineRange: navmodel.Range{Start: section.start, End: section.end},
			depth:     section.level,
			name:      section.name,
		})
	}
	return parseOutput{records: records}
}

func canonicalLines(source []byte) []string {
	if len(source) == 0 {
		return nil
	}
	parts := bytes.Split(source, []byte{'\n'})
	if len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}
	lines := make([]string, len(parts))
	for index, part := range parts {
		lines[index] = string(part)
	}
	return lines
}

func markdownFence(line string) (byte, int, bool) {
	if len(line) < 3 || (line[0] != '`' && line[0] != '~') {
		return 0, 0, false
	}
	marker := line[0]
	length := 1
	for length < len(line) && line[length] == marker {
		length++
	}
	return marker, length, length >= 3
}

func cleanMarkdownTitle(raw string) string {
	title := strings.TrimSpace(raw)
	if strings.HasSuffix(title, "#") {
		withoutHashes := strings.TrimRight(title, "#")
		if strings.HasSuffix(withoutHashes, " ") || strings.HasSuffix(withoutHashes, "\t") || strings.TrimSpace(withoutHashes) == "" {
			title = strings.TrimSpace(withoutHashes)
		}
	}
	return title
}
