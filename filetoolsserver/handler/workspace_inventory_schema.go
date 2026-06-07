package handler

import "github.com/google/jsonschema-go/jsonschema"

// WorkspaceInventoryOutputSchema returns a recursive schema for workspace_inventory.
// jsonschema.For cannot infer recursive Go structs, so this schema keeps the
// MCP contract precise without weakening Directories to []any in runtime output.
func WorkspaceInventoryOutputSchema() *jsonschema.Schema {
	falseSchema := &jsonschema.Schema{Not: &jsonschema.Schema{}}
	stringSchema := func() *jsonschema.Schema { return &jsonschema.Schema{Type: "string"} }
	intSchema := &jsonschema.Schema{Type: "integer"}
	boolSchema := &jsonschema.Schema{Type: "boolean"}
	nodeRef := &jsonschema.Schema{Ref: "#/$defs/workspace_directory_node"}
	actionRef := &jsonschema.Schema{Ref: "#/$defs/action_hint"}
	pageEntryRef := &jsonschema.Schema{Ref: "#/$defs/workspace_directory_page_entry"}
	summaryRef := &jsonschema.Schema{Ref: "#/$defs/workspace_summary"}
	continuationRef := &jsonschema.Schema{Ref: "#/$defs/continuation_hint"}
	sortKeyRef := &jsonschema.Schema{Ref: "#/$defs/discovery_sort_key"}

	nodeSchema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"name":              stringSchema(),
			"path":              stringSchema(),
			"depth":             intSchema,
			"direct_file_count": intSchema,
			"direct_dir_count":  intSchema,
			"truncated":         boolSchema,
			"read_error":        stringSchema(),
			"directories": &jsonschema.Schema{
				Type:  "array",
				Items: nodeRef,
			},
		},
		Required: []string{
			"name",
			"path",
			"depth",
			"direct_file_count",
			"direct_dir_count",
			"truncated",
			"directories",
		},
		AdditionalProperties: falseSchema,
		PropertyOrder: []string{
			"name",
			"path",
			"depth",
			"direct_file_count",
			"direct_dir_count",
			"truncated",
			"read_error",
			"directories",
		},
	}

	schema := &jsonschema.Schema{
		Type: "object",
		Defs: map[string]*jsonschema.Schema{
			"workspace_directory_node": nodeSchema,
			"workspace_directory_page_entry": &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"path":              stringSchema(),
					"parent_path":       stringSchema(),
					"depth":             intSchema,
					"direct_file_count": intSchema,
					"direct_dir_count":  intSchema,
					"read_error":        stringSchema(),
				},
				Required:             []string{"path", "depth", "direct_file_count", "direct_dir_count"},
				AdditionalProperties: falseSchema,
			},
			"backup_candidate_directory": &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"path":                    stringSchema(),
					"candidate_file_count":    intSchema,
					"hidden_evidence_skipped": boolSchema,
				},
				Required:             []string{"path", "candidate_file_count"},
				AdditionalProperties: falseSchema,
			},
			"action_hint": &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"safe_to_retry":                 boolSchema,
					"recommended_next_tool":         stringSchema(),
					"recommended_next_input":        &jsonschema.Schema{Type: "object"},
					"recommended_next_input_policy": stringSchema(),
					"reason":                        stringSchema(),
				},
				Required:             []string{"safe_to_retry"},
				AdditionalProperties: falseSchema,
			},
			"discovery_sort_key": &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"path":               stringSchema(),
					"modified_unix_nano": intSchema,
					"size_bytes":         intSchema,
				},
				Required:             []string{"path"},
				AdditionalProperties: falseSchema,
			},
			"continuation_hint": &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"complete":               boolSchema,
					"page_complete":          boolSchema,
					"consistency":            stringSchema(),
					"canonical_query_hash":   stringSchema(),
					"last_sort_key":          sortKeyRef,
					"stale_if_file_changes":  boolSchema,
					"next_recommended_call":  actionRef,
					"next_recommended_calls": &jsonschema.Schema{Type: "array", Items: actionRef},
					"reason":                 stringSchema(),
				},
				Required:             []string{"complete"},
				AdditionalProperties: falseSchema,
			},
			"workspace_summary": &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"complete":                     boolSchema,
					"summary_coverage_complete":    boolSchema,
					"tree_scan_complete":           boolSchema,
					"summary_incomplete_reason":    stringSchema(),
					"scan_scope":                   stringSchema(),
					"profile":                      stringSchema(),
					"file_type_counts":             &jsonschema.Schema{Type: "object", AdditionalProperties: intSchema},
					"package_hints":                &jsonschema.Schema{Type: "array", Items: stringSchema()},
					"source_dir_hints":             &jsonschema.Schema{Type: "array", Items: stringSchema()},
					"test_dir_hints":               &jsonschema.Schema{Type: "array", Items: stringSchema()},
					"largest_directories":          &jsonschema.Schema{Type: "array", Items: pageEntryRef},
					"backup_candidate_directories": &jsonschema.Schema{Type: "array", Items: &jsonschema.Schema{Ref: "#/$defs/backup_candidate_directory"}},
					"backup_discovery_hints":       &jsonschema.Schema{Type: "array", Items: actionRef},
					"hidden_entries_skipped":       intSchema,
					"ignored_entries_skipped":      intSchema,
				},
				Required:             []string{"complete", "summary_coverage_complete", "tree_scan_complete", "scan_scope", "profile"},
				AdditionalProperties: falseSchema,
			},
		},
		Properties: map[string]*jsonschema.Schema{
			"error":                   stringSchema(),
			"cwd_id":                  intSchema,
			"cwd":                     stringSchema(),
			"error_code":              stringSchema(),
			"action_hint":             actionRef,
			"root":                    nodeRef,
			"directories_page":        &jsonschema.Schema{Type: "array", Items: pageEntryRef},
			"summary":                 summaryRef,
			"continuation":            continuationRef,
			"max_depth":               intSchema,
			"limit":                   intSchema,
			"directory_count":         intSchema,
			"ignored_directory_count": intSchema,
			"include_hidden":          boolSchema,
			"include_vcs_metadata":    boolSchema,
			"dot_entries_skipped":     boolSchema,
			"hidden_entries_included": intSchema,
			"vcs_entries_skipped":     intSchema,
			"vcs_entries_included":    intSchema,
			"truncated":               boolSchema,
			"truncation_reason":       stringSchema(),
			"max_depth_reached":       boolSchema,
			"next_recommended_call":   actionRef,
			"next_recommended_calls":  &jsonschema.Schema{Type: "array", Items: actionRef},
		},
		Required: []string{
			"directories_page",
			"max_depth",
			"limit",
			"directory_count",
			"ignored_directory_count",
			"include_hidden",
			"include_vcs_metadata",
			"dot_entries_skipped",
			"truncated",
			"max_depth_reached",
		},
		AdditionalProperties: falseSchema,
		PropertyOrder: []string{
			"error",
			"cwd_id",
			"cwd",
			"error_code",
			"action_hint",
			"root",
			"directories_page",
			"summary",
			"continuation",
			"max_depth",
			"limit",
			"directory_count",
			"ignored_directory_count",
			"include_hidden",
			"include_vcs_metadata",
			"dot_entries_skipped",
			"hidden_entries_included",
			"vcs_entries_skipped",
			"vcs_entries_included",
			"truncated",
			"truncation_reason",
			"max_depth_reached",
			"next_recommended_call",
			"next_recommended_calls",
		},
	}
	ApplyPathOutputSchemaConstraints(schema)
	return schema
}
