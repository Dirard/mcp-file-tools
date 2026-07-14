package navigation

import (
	"encoding/json"
	"runtime"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/cursor"
	"github.com/Dirard/mcp-file-tools/internal/jsonwire"
	"github.com/Dirard/mcp-file-tools/internal/navmodel"
	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"github.com/Dirard/mcp-file-tools/internal/scanner"
)

const maxSafeCWDID = uint64(9_007_199_254_740_991)

var toolArgumentLimits = jsonwire.Limits{
	MaxDepth:          4,
	MaxObjectMembers:  16,
	MaxContainerItems: 128,
	MaxKeyBytes:       64,
	MaxStringBytes:    api.InputStringMaxBytes,
	MaxNumberRawBytes: 64,
}

type argumentKind uint8

const (
	initialArguments argumentKind = iota + 1
	continuationArguments
)

type ProjectInitial struct {
	CWDID          uint64
	Path           pathspec.Relative
	Depth          uint8
	Limit          uint16
	IncludeIgnored bool
}

type Continuation struct {
	CWDID  uint64
	Cursor cursor.Token
}

type SearchInitial struct {
	CWDID          uint64
	Mode           dynamicMode
	Query          string
	Path           pathspec.Relative
	Glob           *scanner.Glob
	Regex          bool
	IgnoreCase     bool
	Context        uint8
	IncludeIgnored bool
	Limit          uint16
}

type ReadFile struct {
	Path       pathspec.Relative
	Start, End uint32
}

type ReadInitial struct {
	CWDID    uint64
	Files    []ReadFile
	Mode     navmodel.ReadView
	MaxBytes uint64
}

type projectArguments struct {
	kind         argumentKind
	project      ProjectInitial
	continuation Continuation
}

type searchArguments struct {
	kind         argumentKind
	search       SearchInitial
	continuation Continuation
}

type readArguments struct {
	kind         argumentKind
	read         ReadInitial
	continuation Continuation
}

func decodeProjectArguments(raw []byte) (projectArguments, api.ErrorCode) {
	object, err := jsonwire.ScanObject(raw, toolArgumentLimits, jsonwire.ToolArguments)
	if err != nil {
		return projectArguments{}, api.ErrorInvalidInput
	}
	if _, continuing := object.Member("cursor"); continuing {
		continuation, code := decodeContinuation(object)
		if code != "" {
			return projectArguments{}, code
		}
		return projectArguments{kind: continuationArguments, continuation: continuation}, ""
	}
	if !validProjectInitialShape(object) {
		return projectArguments{}, api.ErrorInvalidInput
	}
	cwdID, ok := decodeUintMember(object, "cwd_id", 1, maxSafeCWDID, true)
	if !ok {
		return projectArguments{}, api.ErrorInvalidInput
	}
	pathValue := "."
	if _, present := object.Member("path"); present {
		pathValue, ok = decodeStringMember(object, "path", true)
		if !ok {
			return projectArguments{}, api.ErrorInvalidInput
		}
	}
	depth, ok := decodeUintMember(object, "depth", 0, 8, false)
	if !ok {
		return projectArguments{}, api.ErrorInvalidInput
	}
	if _, present := object.Member("depth"); !present {
		depth = 2
	}
	limit, ok := decodeUintMember(object, "limit", 1, 1000, false)
	if !ok {
		return projectArguments{}, api.ErrorInvalidInput
	}
	if _, present := object.Member("limit"); !present {
		limit = 200
	}
	includeIgnored, ok := decodeBoolMember(object, "include_ignored", false)
	if !ok {
		return projectArguments{}, api.ErrorInvalidInput
	}
	path, code := pathspec.ParseRelative(hostTarget(), pathValue, true)
	if code != "" {
		return projectArguments{}, code
	}
	return projectArguments{
		kind: initialArguments,
		project: ProjectInitial{
			CWDID:          cwdID,
			Path:           path,
			Depth:          uint8(depth),
			Limit:          uint16(limit),
			IncludeIgnored: includeIgnored,
		},
	}, ""
}

func decodeSearchArguments(raw []byte, detail *inputErrorDetail) (searchArguments, api.ErrorCode) {
	object, err := jsonwire.ScanObject(raw, toolArgumentLimits, jsonwire.ToolArguments)
	if err != nil {
		return searchArguments{}, api.ErrorInvalidInput
	}
	if _, continuing := object.Member("cursor"); continuing {
		continuation, code := decodeContinuation(object)
		if code != "" {
			return searchArguments{}, code
		}
		return searchArguments{kind: continuationArguments, continuation: continuation}, ""
	}
	if !validSearchInitialShape(object) {
		return searchArguments{}, api.ErrorInvalidInput
	}
	cwdID, ok := decodeUintMember(object, "cwd_id", 1, maxSafeCWDID, true)
	if !ok {
		return searchArguments{}, api.ErrorInvalidInput
	}
	query, ok := decodeStringMember(object, "query", true)
	if !ok {
		return searchArguments{}, api.ErrorInvalidInput
	}
	modeName := "text"
	if _, present := object.Member("mode"); present {
		modeName, ok = decodeStringMember(object, "mode", true)
		if !ok {
			return searchArguments{}, api.ErrorInvalidInput
		}
	}
	mode := dynamicMode(0)
	switch modeName {
	case "file":
		mode = dynamicFileSearch
	case "text":
		mode = dynamicTextSearch
	case "symbol":
		mode = dynamicSymbolSearch
	default:
		return searchArguments{}, api.ErrorInvalidInput
	}
	if mode == dynamicFileSearch && hasAnyMember(object, "glob", "regex", "context") {
		return searchArguments{}, api.ErrorInvalidInput
	}
	if mode == dynamicSymbolSearch && hasAnyMember(object, "context") {
		return searchArguments{}, api.ErrorInvalidInput
	}
	ignoreCase, ok := decodeBoolMember(object, "ignore_case", false)
	if !ok {
		return searchArguments{}, api.ErrorInvalidInput
	}
	regex, ok := decodeBoolMember(object, "regex", false)
	if !ok {
		return searchArguments{}, api.ErrorInvalidInput
	}
	contextLines, ok := decodeUintMember(object, "context", 0, 20, false)
	if !ok {
		return searchArguments{}, api.ErrorInvalidInput
	}
	var compiledGlob *scanner.Glob
	if mode == dynamicFileSearch {
		compiled, code := scanner.CompileGlob(query, ignoreCase)
		if code != "" {
			return searchArguments{}, code
		}
		compiledGlob = &compiled
	} else {
		if _, err := compileSearchMatcher(query, regex, ignoreCase); err != nil {
			if regex {
				detail.set("query", "invalid_re2_expression")
			}
			return searchArguments{}, api.ErrorInvalidInput
		}
		if _, present := object.Member("glob"); present {
			globPattern, valid := decodeStringMember(object, "glob", true)
			if !valid {
				return searchArguments{}, api.ErrorInvalidInput
			}
			compiled, code := scanner.CompileGlob(globPattern, ignoreCase)
			if code != "" {
				return searchArguments{}, code
			}
			compiledGlob = &compiled
		}
	}
	pathValue := "."
	if _, present := object.Member("path"); present {
		pathValue, ok = decodeStringMember(object, "path", true)
		if !ok {
			return searchArguments{}, api.ErrorInvalidInput
		}
	}
	includeIgnored, ok := decodeBoolMember(object, "include_ignored", false)
	if !ok {
		return searchArguments{}, api.ErrorInvalidInput
	}
	limit, ok := decodeUintMember(object, "limit", 1, 1000, false)
	if !ok {
		return searchArguments{}, api.ErrorInvalidInput
	}
	if _, present := object.Member("limit"); !present {
		limit = 50
	}
	path, code := pathspec.ParseRelative(hostTarget(), pathValue, true)
	if code != "" {
		return searchArguments{}, code
	}
	return searchArguments{
		kind: initialArguments,
		search: SearchInitial{
			CWDID:          cwdID,
			Mode:           mode,
			Query:          query,
			Path:           path,
			Glob:           compiledGlob,
			Regex:          regex,
			IgnoreCase:     ignoreCase,
			Context:        uint8(contextLines),
			IncludeIgnored: includeIgnored,
			Limit:          uint16(limit),
		},
	}, ""
}

func decodeReadArguments(raw []byte, detail *inputErrorDetail) (readArguments, api.ErrorCode) {
	object, err := jsonwire.ScanObject(raw, toolArgumentLimits, jsonwire.ToolArguments)
	if err != nil {
		return readArguments{}, api.ErrorInvalidInput
	}
	if _, continuing := object.Member("cursor"); continuing {
		continuation, code := decodeContinuation(object)
		if code != "" {
			return readArguments{}, code
		}
		return readArguments{kind: continuationArguments, continuation: continuation}, ""
	}
	if !validReadInitialShape(object) {
		return readArguments{}, api.ErrorInvalidInput
	}

	cwdID, ok := decodeUintMember(object, "cwd_id", 1, maxSafeCWDID, true)
	if !ok {
		return readArguments{}, api.ErrorInvalidInput
	}
	viewName := "source"
	if _, present := object.Member("view"); present {
		viewName, ok = decodeStringMember(object, "view", true)
		if !ok {
			return readArguments{}, api.ErrorInvalidInput
		}
	}
	view := navmodel.ReadView(0)
	switch viewName {
	case "source":
		view = navmodel.ReadSource
	case "outline":
		view = navmodel.ReadOutline
	default:
		return readArguments{}, api.ErrorInvalidInput
	}
	maxBytes, ok := decodeUintMember(object, "max_bytes", 4096, 32768, false)
	if !ok {
		if value, present := object.Value("max_bytes"); present && value.Kind() == jsonwire.Number {
			decimal, err := jsonwire.ParseDecimal(value.Bytes())
			if err == nil && decimal.IsInteger() {
				switch {
				case decimal.CompareUint64(4096) < 0:
					detail.set("max_bytes", "minimum_is_4096")
				case decimal.CompareUint64(32768) > 0:
					detail.set("max_bytes", "maximum_is_32768")
				}
			}
		}
		return readArguments{}, api.ErrorInvalidInput
	}
	if _, present := object.Member("max_bytes"); !present {
		maxBytes = 32768
	}

	filesValue, present := object.Value("files")
	if !present || filesValue.Kind() != jsonwire.Array {
		return readArguments{}, api.ErrorInvalidInput
	}
	filesArray, err := jsonwire.ScanArray(filesValue.Bytes(), toolArgumentLimits, jsonwire.ToolArguments)
	if err != nil {
		return readArguments{}, api.ErrorInvalidInput
	}
	values := filesArray.Values()
	if len(values) == 0 || len(values) > 24 {
		return readArguments{}, api.ErrorInvalidInput
	}

	type rawReadFile struct {
		path       string
		start, end uint32
	}
	rawFiles := make([]rawReadFile, len(values))
	for index, value := range values {
		if value.Kind() != jsonwire.Object {
			return readArguments{}, api.ErrorInvalidInput
		}
		item, scanErr := jsonwire.ScanObject(value.Bytes(), toolArgumentLimits, jsonwire.ToolArguments)
		if scanErr != nil || !validReadFileShape(item, view) {
			return readArguments{}, api.ErrorInvalidInput
		}
		pathValue, valid := decodeStringMember(item, "path", true)
		if !valid {
			return readArguments{}, api.ErrorInvalidInput
		}
		current := rawReadFile{path: pathValue}
		if view == navmodel.ReadSource {
			start, valid := decodeUintMember(item, "start", 1, 1<<31-1, false)
			if !valid {
				return readArguments{}, api.ErrorInvalidInput
			}
			if _, supplied := item.Member("start"); !supplied {
				start = 1
			}
			end, valid := decodeUintMember(item, "end", 1, 1<<31-1, true)
			if !valid || start > end {
				return readArguments{}, api.ErrorInvalidInput
			}
			current.start = uint32(start)
			current.end = uint32(end)
		}
		rawFiles[index] = current
	}

	files := make([]ReadFile, len(rawFiles))
	for index, rawFile := range rawFiles {
		path, code := pathspec.ParseRelative(hostTarget(), rawFile.path, true)
		if code != "" {
			return readArguments{}, code
		}
		files[index] = ReadFile{Path: path, Start: rawFile.start, End: rawFile.end}
	}
	return readArguments{
		kind: initialArguments,
		read: ReadInitial{
			CWDID:    cwdID,
			Files:    files,
			Mode:     view,
			MaxBytes: maxBytes,
		},
	}, ""
}

func decodeContinuation(object jsonwire.ObjectView) (Continuation, api.ErrorCode) {
	if len(object.Members()) != 2 || !hasAllMembers(object, "cwd_id", "cursor") {
		return Continuation{}, api.ErrorInvalidInput
	}
	cwdMember, _ := object.Member("cwd_id")
	cursorMember, _ := object.Member("cursor")
	if cwdMember.Kind != jsonwire.Number || cursorMember.Kind != jsonwire.String {
		return Continuation{}, api.ErrorInvalidInput
	}
	cwdID, ok := decodeUintMember(object, "cwd_id", 1, maxSafeCWDID, true)
	if !ok {
		return Continuation{}, api.ErrorInvalidInput
	}
	rawCursor, ok := decodeStringMember(object, "cursor", true)
	if !ok {
		return Continuation{}, api.ErrorInvalidInput
	}
	token, code := cursor.ParseToken(rawCursor)
	if code != "" {
		return Continuation{}, code
	}
	return Continuation{CWDID: cwdID, Cursor: token}, ""
}

func validProjectInitialShape(object jsonwire.ObjectView) bool {
	allowed := map[string]jsonwire.ValueKind{
		"cwd_id":          jsonwire.Number,
		"path":            jsonwire.String,
		"depth":           jsonwire.Number,
		"limit":           jsonwire.Number,
		"include_ignored": 0,
	}
	if _, required := object.Member("cwd_id"); !required {
		return false
	}
	for _, member := range object.Members() {
		expected, known := allowed[member.Key]
		if !known || !matchesMemberKind(member.Kind, expected) {
			return false
		}
	}
	return true
}

func validSearchInitialShape(object jsonwire.ObjectView) bool {
	allowed := map[string]jsonwire.ValueKind{
		"cwd_id":          jsonwire.Number,
		"query":           jsonwire.String,
		"mode":            jsonwire.String,
		"path":            jsonwire.String,
		"glob":            jsonwire.String,
		"regex":           0,
		"ignore_case":     0,
		"context":         jsonwire.Number,
		"include_ignored": 0,
		"limit":           jsonwire.Number,
	}
	if !hasAllMembers(object, "cwd_id", "query") {
		return false
	}
	for _, member := range object.Members() {
		expected, known := allowed[member.Key]
		if !known || !matchesMemberKind(member.Kind, expected) {
			return false
		}
	}
	return true
}

func validReadInitialShape(object jsonwire.ObjectView) bool {
	allowed := map[string]jsonwire.ValueKind{
		"cwd_id":    jsonwire.Number,
		"files":     jsonwire.Array,
		"view":      jsonwire.String,
		"max_bytes": jsonwire.Number,
	}
	if !hasAllMembers(object, "cwd_id", "files") {
		return false
	}
	for _, member := range object.Members() {
		expected, known := allowed[member.Key]
		if !known || member.Kind != expected {
			return false
		}
	}
	return true
}

func validReadFileShape(object jsonwire.ObjectView, view navmodel.ReadView) bool {
	allowed := map[string]jsonwire.ValueKind{"path": jsonwire.String}
	if view == navmodel.ReadSource {
		allowed["start"] = jsonwire.Number
		allowed["end"] = jsonwire.Number
		if !hasAllMembers(object, "path", "end") {
			return false
		}
	} else if view != navmodel.ReadOutline || !hasAllMembers(object, "path") {
		return false
	}
	for _, member := range object.Members() {
		expected, known := allowed[member.Key]
		if !known || member.Kind != expected {
			return false
		}
	}
	return true
}

func matchesMemberKind(actual, expected jsonwire.ValueKind) bool {
	if expected == 0 {
		return actual == jsonwire.True || actual == jsonwire.False
	}
	return actual == expected
}

func decodeUintMember(object jsonwire.ObjectView, name string, minimum, maximum uint64, required bool) (uint64, bool) {
	value, present := object.Value(name)
	if !present {
		return 0, !required
	}
	if value.Kind() != jsonwire.Number {
		return 0, false
	}
	decimal, err := jsonwire.ParseDecimal(value.Bytes())
	if err != nil || !decimal.IsInteger() || decimal.CompareUint64(minimum) < 0 || decimal.CompareUint64(maximum) > 0 {
		return 0, false
	}
	decoded, ok := decimal.Uint64()
	return decoded, ok
}

func decodeStringMember(object jsonwire.ObjectView, name string, required bool) (string, bool) {
	value, present := object.Value(name)
	if !present {
		return "", !required
	}
	if value.Kind() != jsonwire.String {
		return "", false
	}
	var decoded string
	if err := json.Unmarshal(value.Bytes(), &decoded); err != nil || decoded == "" || len(decoded) > api.InputStringMaxBytes {
		return "", false
	}
	return decoded, true
}

func decodeBoolMember(object jsonwire.ObjectView, name string, defaultValue bool) (bool, bool) {
	value, present := object.Value(name)
	if !present {
		return defaultValue, true
	}
	switch value.Kind() {
	case jsonwire.True:
		return true, true
	case jsonwire.False:
		return false, true
	default:
		return false, false
	}
}

func hasAllMembers(object jsonwire.ObjectView, names ...string) bool {
	for _, name := range names {
		if _, present := object.Member(name); !present {
			return false
		}
	}
	return true
}

func hasAnyMember(object jsonwire.ObjectView, names ...string) bool {
	for _, name := range names {
		if _, present := object.Member(name); present {
			return true
		}
	}
	return false
}

func hostTarget() pathspec.TargetOS {
	if runtime.GOOS == "windows" {
		return pathspec.Windows
	}
	return pathspec.POSIX
}
