package handler

import "github.com/google/jsonschema-go/jsonschema"

// OutlineFileOutputSchema returns a recursive schema for outline_file.
// jsonschema.For cannot infer OutlineItem.Children recursively.
func OutlineFileOutputSchema() *jsonschema.Schema {
	falseSchema := &jsonschema.Schema{Not: &jsonschema.Schema{}}
	stringSchema := func() *jsonschema.Schema { return &jsonschema.Schema{Type: "string"} }
	intSchema := func() *jsonschema.Schema { return &jsonschema.Schema{Type: "integer"} }
	int64Schema := func() *jsonschema.Schema { return &jsonschema.Schema{Type: "integer"} }
	boolSchema := func() *jsonschema.Schema { return &jsonschema.Schema{Type: "boolean"} }
	objectSchema := func() *jsonschema.Schema { return &jsonschema.Schema{Type: "object"} }
	stringArraySchema := func() *jsonschema.Schema {
		return &jsonschema.Schema{Type: "array", Items: stringSchema()}
	}

	rangeRef := &jsonschema.Schema{Ref: "#/$defs/source_line_range"}
	byteRangeRef := &jsonschema.Schema{Ref: "#/$defs/source_byte_range"}
	fingerprintRef := &jsonschema.Schema{Ref: "#/$defs/file_fingerprint"}
	warningRef := &jsonschema.Schema{Ref: "#/$defs/tool_warning"}
	selectorRef := &jsonschema.Schema{Ref: "#/$defs/outline_selector"}
	outlineItemRef := &jsonschema.Schema{Ref: "#/$defs/outline_item"}
	statsRef := &jsonschema.Schema{Ref: "#/$defs/outline_stats"}
	actionRef := &jsonschema.Schema{Ref: "#/$defs/action_hint"}

	schema := &jsonschema.Schema{
		Type: "object",
		Defs: map[string]*jsonschema.Schema{
			"source_line_range": &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"start_line": intSchema(),
					"end_line":   intSchema(),
				},
				Required:             []string{"start_line", "end_line"},
				AdditionalProperties: falseSchema,
			},
			"file_fingerprint": &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"sha256":             stringSchema(),
					"size_bytes":         int64Schema(),
					"line_count":         intSchema(),
					"modified_unix_nano": int64Schema(),
				},
				Required:             []string{"sha256", "size_bytes", "line_count", "modified_unix_nano"},
				AdditionalProperties: falseSchema,
			},
			"tool_warning": &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"code":    stringSchema(),
					"message": stringSchema(),
					"file":    stringSchema(),
					"line":    intSchema(),
				},
				Required:             []string{"code", "message"},
				AdditionalProperties: falseSchema,
			},
			"outline_stats": &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"items_returned":      intSchema(),
					"items_omitted":       intSchema(),
					"items_omitted_known": boolSchema(),
					"omitted_leaf_items":  intSchema(),
					"last_included_line":  intSchema(),
					"next_omitted_line":   intSchema(),
					"next_omitted_kind":   stringSchema(),
					"next_omitted_name":   stringSchema(),
					"truncation_reason":   stringSchema(),
				},
				Required:             []string{"items_returned", "items_omitted_known"},
				AdditionalProperties: falseSchema,
			},
			"action_hint": &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"safe_to_retry":                 boolSchema(),
					"recommended_next_tool":         stringSchema(),
					"recommended_next_input":        objectSchema(),
					"recommended_next_input_policy": stringSchema(),
					"reason":                        stringSchema(),
				},
				Required:             []string{"safe_to_retry"},
				AdditionalProperties: falseSchema,
			},
		},
		Properties: map[string]*jsonschema.Schema{
			"error":                 stringSchema(),
			"cwd_id":                int64Schema(),
			"cwd":                   stringSchema(),
			"error_code":            stringSchema(),
			"action_hint":           actionRef,
			"file":                  stringSchema(),
			"language":              stringSchema(),
			"parser_status":         stringSchema(),
			"parser_scope":          stringSchema(),
			"fingerprint":           fingerprintRef,
			"imports":               &jsonschema.Schema{Type: "array", Items: outlineItemRef},
			"symbols":               &jsonschema.Schema{Type: "array", Items: outlineItemRef},
			"sections":              &jsonschema.Schema{Type: "array", Items: outlineItemRef},
			"enclosing_items":       &jsonschema.Schema{Type: "array", Items: outlineItemRef},
			"outline_stats":         statsRef,
			"truncated":             boolSchema(),
			"warnings":              &jsonschema.Schema{Type: "array", Items: warningRef},
			"next_recommended_call": actionRef,
		},
		Required: []string{
			"imports",
			"symbols",
			"sections",
			"outline_stats",
			"truncated",
			"warnings",
		},
		AdditionalProperties: falseSchema,
	}

	schema.Defs["outline_item"] = &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"id":                 stringSchema(),
			"kind":               stringSchema(),
			"name":               stringSchema(),
			"detail":             stringSchema(),
			"path":               stringArraySchema(),
			"enclosing_path":     stringArraySchema(),
			"range":              rangeRef,
			"byte_range":         byteRangeRef,
			"depth":              intSchema(),
			"confidence":         stringSchema(),
			"range_is_estimated": boolSchema(),
			"range_fingerprint":  fingerprintRef,
			"selector":           selectorRef,
			"symbol_ref":         stringSchema(),
			"whole_line_range":   boolSchema(),
			"write_safe":         boolSchema(),
			"refusal_reason":     stringSchema(),
			"children":           &jsonschema.Schema{Type: "array", Items: outlineItemRef},
			"metadata":           objectSchema(),
		},
		Required:             []string{"kind", "name", "range"},
		AdditionalProperties: falseSchema,
	}
	schema.Defs["source_byte_range"] = &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"start_byte":         intSchema(),
			"end_byte_exclusive": intSchema(),
		},
		Required:             []string{"start_byte", "end_byte_exclusive"},
		AdditionalProperties: falseSchema,
	}
	schema.Defs["outline_selector"] = &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"language":          stringSchema(),
			"kind":              stringSchema(),
			"name":              stringSchema(),
			"symbol_path":       stringArraySchema(),
			"range":             rangeRef,
			"byte_range":        byteRangeRef,
			"whole_line_range":  boolSchema(),
			"write_safe":        boolSchema(),
			"range_fingerprint": fingerprintRef,
			"symbol_ref":        stringSchema(),
			"disambiguator":     stringSchema(),
		},
		Required:             []string{"language", "kind", "name", "symbol_path", "range", "byte_range", "whole_line_range", "write_safe", "range_fingerprint", "symbol_ref"},
		AdditionalProperties: falseSchema,
	}

	ApplyPathOutputSchemaConstraints(schema)
	return schema
}
