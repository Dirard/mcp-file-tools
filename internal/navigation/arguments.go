package navigation

import (
	"encoding/json"
	"runtime"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/config"
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

func decodeProjectArguments(raw []byte, detail *inputErrorDetail) (projectArguments, api.ErrorCode) {
	object, err := jsonwire.ScanObject(raw, toolArgumentLimits, jsonwire.ToolArguments)
	if err != nil {
		detail.set("arguments", "must_be_object")
		return projectArguments{}, api.ErrorInvalidInput
	}
	if _, continuing := object.Member("cursor"); continuing {
		continuation, code := decodeContinuation(object, detail)
		if code != "" {
			return projectArguments{}, code
		}
		return projectArguments{kind: continuationArguments, continuation: continuation}, ""
	}
	if _, present := object.Member("cwd_id"); !present {
		detail.set("cwd_id", "required")
		return projectArguments{}, api.ErrorInvalidInput
	}
	cwdID, ok := decodeUintMember(object, "cwd_id", 1, maxSafeCWDID, true)
	if !ok {
		detail.set("cwd_id", "must_be_positive_integer")
		return projectArguments{}, api.ErrorInvalidInput
	}
	if !validProjectInitialShape(object) {
		detail.set("arguments", "project_requires_cwd_id_and_known_fields")
		return projectArguments{}, api.ErrorInvalidInput
	}
	pathValue := "."
	if _, present := object.Member("path"); present {
		pathValue, ok = decodeStringMember(object, "path", true)
		if !ok {
			detail.set("path", "must_be_non_empty_string")
			return projectArguments{}, api.ErrorInvalidInput
		}
	}
	depth, ok := decodeUintMember(object, "depth", 0, 8, false)
	if !ok {
		detail.set("depth", "must_be_integer_0_to_8")
		return projectArguments{}, api.ErrorInvalidInput
	}
	if _, present := object.Member("depth"); !present {
		depth = 2
	}
	limit, ok := decodeUintMember(object, "limit", 1, 1000, false)
	if !ok {
		detail.set("limit", "must_be_integer_1_to_1000")
		return projectArguments{}, api.ErrorInvalidInput
	}
	if _, present := object.Member("limit"); !present {
		limit = 200
	}
	includeIgnored, ok := decodeBoolMember(object, "include_ignored", false)
	if !ok {
		detail.set("include_ignored", "must_be_boolean")
		return projectArguments{}, api.ErrorInvalidInput
	}
	path, code := pathspec.ParseRelative(hostTarget(), pathValue, true)
	if code != "" {
		if code == api.ErrorInvalidInput {
			detail.set("path", "must_be_relative_to_registered_root")
		}
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
		detail.set("arguments", "must_be_object")
		return searchArguments{}, api.ErrorInvalidInput
	}
	if _, continuing := object.Member("cursor"); continuing {
		continuation, code := decodeContinuation(object, detail)
		if code != "" {
			return searchArguments{}, code
		}
		return searchArguments{kind: continuationArguments, continuation: continuation}, ""
	}
	if _, present := object.Member("cwd_id"); !present {
		detail.set("cwd_id", "required")
		return searchArguments{}, api.ErrorInvalidInput
	}
	cwdID, ok := decodeUintMember(object, "cwd_id", 1, maxSafeCWDID, true)
	if !ok {
		detail.set("cwd_id", "must_be_positive_integer")
		return searchArguments{}, api.ErrorInvalidInput
	}
	if _, present := object.Member("query"); !present {
		detail.set("query", "required")
		return searchArguments{}, api.ErrorInvalidInput
	}
	query, ok := decodeStringMember(object, "query", true)
	if !ok {
		detail.set("query", "must_be_non_empty_string")
		return searchArguments{}, api.ErrorInvalidInput
	}
	if !validSearchInitialShape(object) {
		detail.set("arguments", "search_requires_cwd_id_query_and_known_fields")
		return searchArguments{}, api.ErrorInvalidInput
	}
	modeName := "text"
	if _, present := object.Member("mode"); present {
		modeName, ok = decodeStringMember(object, "mode", true)
		if !ok {
			detail.set("mode", "must_be_file_text_or_symbol")
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
		detail.set("mode", "must_be_file_text_or_symbol")
		return searchArguments{}, api.ErrorInvalidInput
	}
	if mode == dynamicFileSearch && hasAnyMember(object, "glob", "regex", "context") {
		detail.set("arguments", "file_mode_disallows_glob_regex_context")
		return searchArguments{}, api.ErrorInvalidInput
	}
	if mode == dynamicSymbolSearch && hasAnyMember(object, "context") {
		detail.set("context", "not_allowed_in_symbol_mode")
		return searchArguments{}, api.ErrorInvalidInput
	}
	ignoreCase, ok := decodeBoolMember(object, "ignore_case", false)
	if !ok {
		detail.set("ignore_case", "must_be_boolean")
		return searchArguments{}, api.ErrorInvalidInput
	}
	regex, ok := decodeBoolMember(object, "regex", false)
	if !ok {
		detail.set("regex", "must_be_boolean")
		return searchArguments{}, api.ErrorInvalidInput
	}
	contextLines, ok := decodeUintMember(object, "context", 0, 20, false)
	if !ok {
		detail.set("context", "must_be_integer_0_to_20")
		return searchArguments{}, api.ErrorInvalidInput
	}
	var compiledGlob *scanner.Glob
	if mode == dynamicFileSearch {
		compiled, code := scanner.CompileGlob(query, ignoreCase)
		if code != "" {
			detail.set("query", "invalid_glob_expression")
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
				detail.set("glob", "must_be_non_empty_string")
				return searchArguments{}, api.ErrorInvalidInput
			}
			compiled, code := scanner.CompileGlob(globPattern, ignoreCase)
			if code != "" {
				detail.set("glob", "invalid_glob_expression")
				return searchArguments{}, code
			}
			compiledGlob = &compiled
		}
	}
	pathValue := "."
	if _, present := object.Member("path"); present {
		pathValue, ok = decodeStringMember(object, "path", true)
		if !ok {
			detail.set("path", "must_be_non_empty_string")
			return searchArguments{}, api.ErrorInvalidInput
		}
	}
	includeIgnored, ok := decodeBoolMember(object, "include_ignored", false)
	if !ok {
		detail.set("include_ignored", "must_be_boolean")
		return searchArguments{}, api.ErrorInvalidInput
	}
	limit, ok := decodeUintMember(object, "limit", 1, 1000, false)
	if !ok {
		detail.set("limit", "must_be_integer_1_to_1000")
		return searchArguments{}, api.ErrorInvalidInput
	}
	if _, present := object.Member("limit"); !present {
		limit = 50
	}
	path, code := pathspec.ParseRelative(hostTarget(), pathValue, true)
	if code != "" {
		if code == api.ErrorInvalidInput {
			detail.set("path", "must_be_relative_path")
		}
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
		detail.set("arguments", "must_be_object")
		return readArguments{}, api.ErrorInvalidInput
	}
	if _, continuing := object.Member("cursor"); continuing {
		continuation, code := decodeContinuation(object, detail)
		if code != "" {
			return readArguments{}, code
		}
		return readArguments{kind: continuationArguments, continuation: continuation}, ""
	}
	if _, present := object.Member("cwd_id"); !present {
		detail.set("cwd_id", "required")
		return readArguments{}, api.ErrorInvalidInput
	}
	cwdID, ok := decodeUintMember(object, "cwd_id", 1, maxSafeCWDID, true)
	if !ok {
		detail.set("cwd_id", "must_be_positive_integer")
		return readArguments{}, api.ErrorInvalidInput
	}
	if _, present := object.Member("files"); !present {
		detail.set("files", "required")
		return readArguments{}, api.ErrorInvalidInput
	}
	if !validReadInitialShape(object) {
		detail.set("arguments", "read_requires_cwd_id_files_and_known_fields")
		return readArguments{}, api.ErrorInvalidInput
	}
	viewName := "source"
	if _, present := object.Member("view"); present {
		viewName, ok = decodeStringMember(object, "view", true)
		if !ok {
			detail.set("view", "must_be_source_or_outline")
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
		detail.set("view", "must_be_source_or_outline")
		return readArguments{}, api.ErrorInvalidInput
	}
	maxBytes, ok := decodeUintMember(object, "max_bytes", 4096, config.ReadOutputMaxBytes, false)
	if !ok {
		if value, present := object.Value("max_bytes"); present && value.Kind() == jsonwire.Number {
			decimal, err := jsonwire.ParseDecimal(value.Bytes())
			if err == nil && decimal.IsInteger() {
				switch {
				case decimal.CompareUint64(4096) < 0:
					detail.set("max_bytes", "minimum_is_4096")
				case decimal.CompareUint64(config.ReadOutputMaxBytes) > 0:
					detail.set("max_bytes", "maximum_is_1048576")
				}
			}
		}
		if detail == nil || detail.field == "" {
			detail.set("max_bytes", "must_be_integer_4096_to_1048576")
		}
		return readArguments{}, api.ErrorInvalidInput
	}
	if _, present := object.Member("max_bytes"); !present {
		maxBytes = config.ReadOutputMaxBytes
	}

	filesValue, present := object.Value("files")
	if !present || filesValue.Kind() != jsonwire.Array {
		detail.set("files", "must_be_array")
		return readArguments{}, api.ErrorInvalidInput
	}
	filesArray, err := jsonwire.ScanArray(filesValue.Bytes(), toolArgumentLimits, jsonwire.ToolArguments)
	if err != nil {
		detail.set("files", "must_be_valid_array")
		return readArguments{}, api.ErrorInvalidInput
	}
	values := filesArray.Values()
	if len(values) == 0 || len(values) > 24 {
		detail.set("files", "must_contain_1_to_24_items")
		return readArguments{}, api.ErrorInvalidInput
	}

	type rawReadFile struct {
		path       string
		start, end uint32
	}
	rawFiles := make([]rawReadFile, len(values))
	for index, value := range values {
		if value.Kind() != jsonwire.Object {
			detail.set("files[]", "items_must_be_objects")
			return readArguments{}, api.ErrorInvalidInput
		}
		item, scanErr := jsonwire.ScanObject(value.Bytes(), toolArgumentLimits, jsonwire.ToolArguments)
		if scanErr != nil {
			detail.set("files[]", "items_must_be_objects")
			return readArguments{}, api.ErrorInvalidInput
		}
		if view == navmodel.ReadSource {
			if _, present := item.Member("end"); !present {
				detail.set("files[].end", "required_for_source_view")
			}
		}
		if !validReadFileShape(item, view) {
			if view == navmodel.ReadOutline {
				detail.set("files[]", "outline_items_allow_only_path")
			} else if _, present := item.Member("end"); present {
				detail.set("files[]", "source_items_allow_path_start_end")
			}
			return readArguments{}, api.ErrorInvalidInput
		}
		pathValue, valid := decodeStringMember(item, "path", true)
		if !valid {
			detail.set("files[].path", "must_be_non_empty_string")
			return readArguments{}, api.ErrorInvalidInput
		}
		current := rawReadFile{path: pathValue}
		if view == navmodel.ReadSource {
			start, valid := decodeUintMember(item, "start", 1, 1<<31-1, false)
			if !valid {
				detail.set("files[].start", "must_be_integer_1_to_2147483647")
				return readArguments{}, api.ErrorInvalidInput
			}
			if _, supplied := item.Member("start"); !supplied {
				start = 1
			}
			end, valid := decodeUintMember(item, "end", 1, 1<<31-1, true)
			if !valid {
				detail.set("files[].end", "must_be_integer_1_to_2147483647")
				return readArguments{}, api.ErrorInvalidInput
			}
			if start > end {
				detail.set("files[].start", "must_not_exceed_end")
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
			if code == api.ErrorInvalidInput {
				detail.set("files[].path", "must_be_relative_to_registered_root")
			}
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

func decodeContinuation(object jsonwire.ObjectView, detail *inputErrorDetail) (Continuation, api.ErrorCode) {
	if len(object.Members()) != 2 || !hasAllMembers(object, "cwd_id", "cursor") {
		detail.set("arguments", "continuation_requires_only_cwd_id_and_cursor")
		return Continuation{}, api.ErrorInvalidInput
	}
	cwdMember, _ := object.Member("cwd_id")
	cursorMember, _ := object.Member("cursor")
	if cwdMember.Kind != jsonwire.Number || cursorMember.Kind != jsonwire.String {
		detail.set("arguments", "cwd_id_must_be_integer_and_cursor_must_be_string")
		return Continuation{}, api.ErrorInvalidInput
	}
	cwdID, ok := decodeUintMember(object, "cwd_id", 1, maxSafeCWDID, true)
	if !ok {
		detail.set("cwd_id", "must_be_positive_integer")
		return Continuation{}, api.ErrorInvalidInput
	}
	rawCursor, ok := decodeStringMember(object, "cursor", true)
	if !ok {
		detail.set("cursor", "must_be_22_character_token")
		return Continuation{}, api.ErrorInvalidInput
	}
	token, code := cursor.ParseToken(rawCursor)
	if code != "" {
		detail.set("cursor", "must_be_22_character_token")
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
