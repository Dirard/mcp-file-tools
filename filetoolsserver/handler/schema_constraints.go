package handler

import (
	"runtime"

	"github.com/google/jsonschema-go/jsonschema"
)

const (
	posixAbsolutePathSchemaPattern   = `^/.*$`
	windowsAbsolutePathSchemaPattern = `^(?:[A-Za-z]:[\\/].*|[\\/]{2}[^\\/]+[\\/][^\\/]+.*)$`
)

var pathInputSchemaFields = map[string]bool{
	"path":             true,
	"source_file":      true,
	"target_directory": true,
	"target_file":      true,
	"target_path":      true,
}

var pathOutputSchemaFields = map[string]bool{
	"backup_path":          true,
	"backup_paths":         true,
	"directory":            true,
	"file":                 true,
	"files_maybe_modified": true,
	"path":                 true,
	"resolved_path":        true,
	"source_file":          true,
	"symlink_target":       true,
	"target_directory":     true,
	"target_file":          true,
	"targets_written":      true,
}

// ApplyPathInputSchemaConstraints mirrors runtime path validation in tool schemas.
func ApplyPathInputSchemaConstraints(schema *jsonschema.Schema) {
	ApplyToolInputSchemaConstraints(schema, "")
}

// ApplyToolInputSchemaConstraints mirrors runtime path validation in tool schemas.
func ApplyToolInputSchemaConstraints(schema *jsonschema.Schema, toolName string) {
	applyPathSchemaConstraints(schema, pathInputSchemaFields, true)
	if toolName == "set_cwd" {
		markSetCwdDirectorySchema(schema)
	} else {
		addCwdIDInputSchema(schema)
	}
	if toolName == "grep" {
		markGrepInputSchema(schema)
	}
	markPhase5InputEnums(schema)
}

// ApplyPathOutputSchemaConstraints prevents path fields from being documented as empty strings.
func ApplyPathOutputSchemaConstraints(schema *jsonschema.Schema) {
	applyPathSchemaConstraints(schema, pathOutputSchemaFields, false)
}

func applyPathSchemaConstraints(schema *jsonschema.Schema, pathFields map[string]bool, input bool) {
	visited := map[*jsonschema.Schema]bool{}
	walkPathSchema(schema, pathFields, visited, input)
}

func walkPathSchema(schema *jsonschema.Schema, pathFields map[string]bool, visited map[*jsonschema.Schema]bool, input bool) {
	if schema == nil || visited[schema] {
		return
	}
	visited[schema] = true

	for name, property := range schema.Properties {
		if name == "recommended_next_input" {
			markRecommendedNextInputPathSchemas(property)
		}
		if name == "line_window" {
			markLineWindowSchema(property)
		}
		if pathFields[name] && !isNonFilesystemPathProperty(schema, name) {
			markPathSchema(property, input)
		}
		if name == "files" && property != nil && property.Items != nil && property.Items.Type == "string" {
			markPathSchema(property.Items, input)
		}
		walkPathSchema(property, pathFields, visited, input)
	}
	for _, definition := range schema.Defs {
		walkPathSchema(definition, pathFields, visited, input)
	}
	for _, definition := range schema.Definitions {
		walkPathSchema(definition, pathFields, visited, input)
	}
	walkPathSchema(schema.Items, pathFields, visited, input)
	for _, item := range schema.ItemsArray {
		walkPathSchema(item, pathFields, visited, input)
	}
	for _, item := range schema.PrefixItems {
		walkPathSchema(item, pathFields, visited, input)
	}
	walkPathSchema(schema.AdditionalItems, pathFields, visited, input)
	walkPathSchema(schema.Contains, pathFields, visited, input)
	for _, property := range schema.PatternProperties {
		walkPathSchema(property, pathFields, visited, input)
	}
	walkPathSchema(schema.AdditionalProperties, pathFields, visited, input)
	walkPathSchema(schema.PropertyNames, pathFields, visited, input)
	walkPathSchema(schema.UnevaluatedProperties, pathFields, visited, input)
	for _, item := range schema.AllOf {
		walkPathSchema(item, pathFields, visited, input)
	}
	for _, item := range schema.AnyOf {
		walkPathSchema(item, pathFields, visited, input)
	}
	for _, item := range schema.OneOf {
		walkPathSchema(item, pathFields, visited, input)
	}
	walkPathSchema(schema.Not, pathFields, visited, input)
	walkPathSchema(schema.If, pathFields, visited, input)
	walkPathSchema(schema.Then, pathFields, visited, input)
	walkPathSchema(schema.Else, pathFields, visited, input)
	for _, dependent := range schema.DependentSchemas {
		walkPathSchema(dependent, pathFields, visited, input)
	}
	walkPathSchema(schema.ContentSchema, pathFields, visited, input)
}

func isNonFilesystemPathProperty(parent *jsonschema.Schema, name string) bool {
	if name != "path" || parent == nil {
		return false
	}
	return parent.Properties["id"] != nil &&
		parent.Properties["kind"] != nil &&
		parent.Properties["range"] != nil &&
		parent.Properties["confidence"] != nil &&
		parent.Properties["range_is_estimated"] != nil
}

func markRecommendedNextInputPathSchemas(schema *jsonschema.Schema) {
	if schema == nil {
		return
	}
	if schema.Properties == nil {
		schema.Properties = map[string]*jsonschema.Schema{}
	}
	for _, name := range []string{"path", "source_file", "target_directory", "target_file", "target_path"} {
		property := schema.Properties[name]
		if property == nil {
			property = &jsonschema.Schema{Type: "string"}
			schema.Properties[name] = property
		}
		markPathSchema(property, false)
	}
	targets := schema.Properties["targets"]
	if targets == nil {
		targets = &jsonschema.Schema{Type: "array"}
		schema.Properties["targets"] = targets
	}
	if targets.Items == nil {
		targets.Items = &jsonschema.Schema{Type: "object"}
	}
	if targets.Items.Properties == nil {
		targets.Items.Properties = map[string]*jsonschema.Schema{}
	}
	targetFile := targets.Items.Properties["target_file"]
	if targetFile == nil {
		targetFile = &jsonschema.Schema{Type: "string"}
		targets.Items.Properties["target_file"] = targetFile
	}
	markPathSchema(targetFile, false)
}

func markPathSchema(schema *jsonschema.Schema, input bool) {
	if schema == nil {
		return
	}
	if schema.Items != nil {
		markPathSchema(schema.Items, input)
		return
	}
	minLength := 1
	schema.MinLength = &minLength
	if schema.Description == "" {
		if input {
			schema.Description = "Absolute path by default; when cwd_id is provided, use a cwd-relative path. Empty paths are rejected."
		} else {
			schema.Description = "Slash-normalized absolute path by default, or cwd-relative path when cwd_id was provided."
		}
	}
}

func addCwdIDInputSchema(schema *jsonschema.Schema) {
	if schema == nil {
		return
	}
	if schema.Properties == nil {
		schema.Properties = map[string]*jsonschema.Schema{}
	}
	minimum := float64(1)
	maximum := float64(maxCwdID)
	schema.Properties["cwd_id"] = &jsonschema.Schema{
		Type:        "integer",
		Minimum:     &minimum,
		Maximum:     &maximum,
		Description: "Optional small cwd id returned by set_cwd. When present, path inputs must be relative to that cwd.",
	}
	if !containsString(schema.PropertyOrder, "cwd_id") {
		schema.PropertyOrder = append(schema.PropertyOrder, "cwd_id")
	}
}

func markSetCwdDirectorySchema(schema *jsonschema.Schema) {
	if schema == nil {
		return
	}
	if schema.Properties == nil {
		schema.Properties = map[string]*jsonschema.Schema{}
	}
	property := schema.Properties["directory"]
	if property == nil {
		property = &jsonschema.Schema{Type: "string"}
		schema.Properties["directory"] = property
	}
	minLength := 1
	property.MinLength = &minLength
	property.Pattern = serverOSAbsolutePathSchemaPattern()
	property.Description = "Absolute directory path for the OS where this MCP server is running."
}

func markGrepInputSchema(schema *jsonschema.Schema) {
	if schema == nil {
		return
	}
	if schema.Properties == nil {
		schema.Properties = map[string]*jsonschema.Schema{}
	}
	if patternMode := schema.Properties["pattern_mode"]; patternMode != nil {
		patternMode.Enum = []any{"regex", "literal"}
		if patternMode.Description == "" {
			patternMode.Description = "Pattern interpretation mode. Omit for regex; use literal to search exact text without regexp escaping."
		}
	}
	if outputMode := schema.Properties["output_mode"]; outputMode != nil {
		outputMode.Enum = []any{"content", "files_with_matches", "count"}
	}
	if maxMatches := schema.Properties["max_matches_per_file"]; maxMatches != nil {
		minimum := float64(1)
		maxMatches.Minimum = &minimum
		if maxMatches.Description == "" {
			maxMatches.Description = "Positive per-file match cap for content output mode."
		}
	}
}

func markPhase5InputEnums(schema *jsonschema.Schema) {
	visited := map[*jsonschema.Schema]bool{}
	walkPhase5InputEnums(schema, visited)
}

func walkPhase5InputEnums(schema *jsonschema.Schema, visited map[*jsonschema.Schema]bool) {
	if schema == nil || visited[schema] {
		return
	}
	visited[schema] = true
	for name, property := range schema.Properties {
		switch name {
		case "redaction_mode":
			setStringEnum(property, "off", "strict", "auto")
		case "output_profile":
			setStringEnum(property, "agent", "full", "fingerprint_only", "outline")
		case "joiner":
			setStringEnum(property, "none", "single_newline", "blank_line")
		case "sort":
			setStringEnum(property, "modified_desc", "modified_asc", "path_asc", "path_desc", "size_desc", "size_asc", "directory_path_asc")
		case "summary_profile":
			setStringEnum(property, "compact", "none", "extended")
		case "backup", "source_backup":
			if property != nil && property.Properties != nil {
				setStringEnum(property.Properties["mode"], "none", "sidecar")
			}
		case "placement":
			if property != nil && property.Properties != nil {
				setStringEnum(property.Properties["mode"], "create_new", "append", "prepend", "insert_before_line", "replace_range")
			}
		}
		walkPhase5InputEnums(property, visited)
	}
	for _, definition := range schema.Defs {
		walkPhase5InputEnums(definition, visited)
	}
	for _, definition := range schema.Definitions {
		walkPhase5InputEnums(definition, visited)
	}
	walkPhase5InputEnums(schema.Items, visited)
	for _, item := range schema.ItemsArray {
		walkPhase5InputEnums(item, visited)
	}
	for _, item := range schema.PrefixItems {
		walkPhase5InputEnums(item, visited)
	}
	walkPhase5InputEnums(schema.AdditionalItems, visited)
	walkPhase5InputEnums(schema.Contains, visited)
	for _, property := range schema.PatternProperties {
		walkPhase5InputEnums(property, visited)
	}
	walkPhase5InputEnums(schema.AdditionalProperties, visited)
	for _, item := range schema.AllOf {
		walkPhase5InputEnums(item, visited)
	}
	for _, item := range schema.AnyOf {
		walkPhase5InputEnums(item, visited)
	}
	for _, item := range schema.OneOf {
		walkPhase5InputEnums(item, visited)
	}
}

func setStringEnum(schema *jsonschema.Schema, values ...string) {
	if schema == nil {
		return
	}
	schema.Enum = make([]any, 0, len(values))
	for _, value := range values {
		schema.Enum = append(schema.Enum, value)
	}
}

func markLineWindowSchema(schema *jsonschema.Schema) {
	if schema == nil || schema.Properties == nil {
		return
	}
	minimum := float64(1)
	for _, name := range []string{"start_line", "end_line"} {
		if property := schema.Properties[name]; property != nil {
			property.Minimum = &minimum
		}
	}
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func serverOSAbsolutePathSchemaPattern() string {
	if runtime.GOOS == "windows" {
		return windowsAbsolutePathSchemaPattern
	}
	return posixAbsolutePathSchemaPattern
}
