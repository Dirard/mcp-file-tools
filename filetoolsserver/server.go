package filetoolsserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/dimitar-grigorov/mcp-file-tools/filetoolsserver/handler"
	"github.com/dimitar-grigorov/mcp-file-tools/internal/config"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is set at build time via ldflags.
var Version = "dev"

// Server instructions for AI assistants.
const serverInstructions = `MCP filesystem server with fourteen tools: structured JSON output for each call.

Tools: set_cwd, read_file, read_files, outline_file, resolve_symbol_range, copy_ranges, move_ranges, copy_ranges_batch, move_ranges_batch, list_dir, glob_file_search, grep, inspect_path, workspace_inventory.

High-value flow: set_cwd once, discover with workspace_inventory/glob_file_search/grep, inspect with read_file/read_files/outline_file, resolve selectors with resolve_symbol_range, then use copy_ranges/move_ranges dry_run before mutation.

grep supports literal/regex search and returns search_stats plus file_groups with read_ranges and next_recommended_call hints.

Path tools accept absolute paths by default. With cwd_id, path inputs are cwd-relative and outputs include cwd metadata.

outline_file is parser-backed for Markdown, Go, JavaScript/JSX, TypeScript/TSX, Python, Java, JSON, YAML, and Svelte where supported. It returns fingerprint, imports/symbols/sections/enclosing_items and exact selectors. Default agent profile keeps TSX/JS/TS and JSON/YAML high-signal; full profile exposes local variables and config values.

workspace_inventory is directory-only: page completeness is continuation.page_complete, while summary/tree coverage is summary.summary_coverage_complete, summary.tree_scan_complete, summary.summary_incomplete_reason, and summary.scan_scope.

resolve_symbol_range can return recommended_write_call as dry-run-only copy/move input when selector and target safety are proven.

Range tools return diff_previews, boundary_preview, boundary_warnings, joiner_effect diagnostics, validation read-back, optional sidecar backups, and partial_state on recovery paths. Previews are bounded display text; verify escape-sensitive edits with validation/read_file. read_files, grep, and write previews default redaction_mode to off; strict is explicit opt-in.`

// Helper for bool pointers (OpenWorldHint needs a pointer).
func boolPtr(b bool) *bool {
	return &b
}

// NewServer creates a new MCP server with the public file tools registered.
// If logger is nil, logging middleware is disabled but recovery is still active.
// If cfg is nil, configuration is loaded from environment variables.
func NewServer(logger *slog.Logger, cfg *config.Config) *mcp.Server {
	var handlerOpts []handler.Option
	if cfg != nil {
		handlerOpts = append(handlerOpts, handler.WithConfig(cfg))
	}
	h := handler.NewHandler(handlerOpts...)

	impl := &mcp.Implementation{
		Name:    "mcp-file-tools",
		Version: Version,
	}

	serverOpts := &mcp.ServerOptions{
		Instructions: serverInstructions,
		Logger:       logger,
	}
	server := mcp.NewServer(impl, serverOpts)

	setCwdDescription := `Register one absolute directory and get a small cwd_id for cwd-relative calls. Success output is exactly cwd_id. Params: directory.`

	addStructuredToolWithOutputSchema(server, &mcp.Tool{
		Name:        "set_cwd",
		Description: setCwdDescription,
		Annotations: &mcp.ToolAnnotations{
			Title:          "Set CWD",
			ReadOnlyHint:   false,
			IdempotentHint: false,
			OpenWorldHint:  boolPtr(false),
		},
	}, logger, "set_cwd", h.HandleSetCwd, h, handler.SetCwdOutputSchema())

	readFileDescription := `Read one known file or 1-based line range. Returns compact line-numbered text plus file/range/total_lines/coverage/continuation metadata. Params: target_file, start_line, end_line, chunk_lines, count_total_lines, expected_version.`

	addStructuredTool(server, &mcp.Tool{
		Name:        "read_file",
		Description: readFileDescription,
		Annotations: &mcp.ToolAnnotations{
			Title:          "Read File",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
	}, logger, "read_file", h.HandleReadFile, h)

	readFilesDescription := `Batch-read known files/ranges with per-item status, complete-line budgets, coverage, continuation, and literal output by default. redaction_mode defaults off; strict is opt-in. Params: items, max_total_lines, max_total_bytes, count_total_lines, redaction_mode.`

	addStructuredTool(server, &mcp.Tool{
		Name:        "read_files",
		Description: readFilesDescription,
		Annotations: &mcp.ToolAnnotations{
			Title:          "Read Files",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
	}, logger, "read_files", h.HandleReadFiles, h)

	outlineFileDescription := `Inspect one file as a compact structure outline plus fingerprint. Parser-backed languages: Markdown, Go, JS/JSX, TS/TSX, Python, Java, JSON, YAML, Svelte. Default agent profile hides TSX local variable noise and JSON/YAML value/wrapper noise while keeping key paths; full/filters/enclosing_line expose details. Returns selectors, stats, and next calls.`

	addStructuredToolWithOutputSchema(server, &mcp.Tool{
		Name:        "outline_file",
		Description: outlineFileDescription,
		Annotations: &mcp.ToolAnnotations{
			Title:          "Outline File",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
	}, logger, "outline_file", h.HandleOutlineFile, h, handler.OutlineFileOutputSchema())

	resolveSymbolRangeDescription := `Resolve an outline selector, kind/name/path, exact range+fingerprint, or enclosing_line to concrete ranges. With target_intent, returns dry-run copy/move recommendations when write safety is proven; never mutates. Params: source_file, source_fingerprint, selector, language, target_intent.`

	addStructuredTool(server, &mcp.Tool{
		Name:        "resolve_symbol_range",
		Description: resolveSymbolRangeDescription,
		Annotations: &mcp.ToolAnnotations{
			Title:          "Resolve Symbol Range",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
	}, logger, "resolve_symbol_range", h.HandleResolveSymbolRange, h)

	copyRangesDescription := `Copy exact 1-based source ranges into one explicit target using fingerprints and placement. dry_run returns diff_previews, joiner_effect, boundary_preview/warnings, validation, and optional backup hints. Previews are bounded display text; verify escape-sensitive edits with read-back/read_file. joiner: none, single_newline, blank_line.`

	addStructuredTool(server, &mcp.Tool{
		Name:        "copy_ranges",
		Description: copyRangesDescription,
		Annotations: &mcp.ToolAnnotations{
			Title:          "Copy Ranges",
			ReadOnlyHint:   false,
			IdempotentHint: false,
			OpenWorldHint:  boolPtr(false),
		},
	}, logger, "copy_ranges", h.HandleCopyRanges, h)

	moveRangesDescription := `Move exact source ranges into one explicit target, then remove them from source after target write and source recheck. Same shape as copy_ranges; returns target/source previews, validation, backups, and partial_state on recovery paths. Verify escape-sensitive edits with read-back/read_file.`

	addStructuredTool(server, &mcp.Tool{
		Name:        "move_ranges",
		Description: moveRangesDescription,
		Annotations: &mcp.ToolAnnotations{
			Title:          "Move Ranges",
			ReadOnlyHint:   false,
			IdempotentHint: false,
			OpenWorldHint:  boolPtr(false),
		},
	}, logger, "move_ranges", h.HandleMoveRanges, h)

	copyRangesBatchDescription := `Copy exact ranges from one source snapshot into multiple explicit targets. Per target: placement, ranges, joiner, backup, diff_previews, joiner_effect, validation. dry_run plans without mutation. Params: source_file, source_fingerprint, targets, dry_run.`

	addStructuredTool(server, &mcp.Tool{
		Name:        "copy_ranges_batch",
		Description: copyRangesBatchDescription,
		Annotations: &mcp.ToolAnnotations{
			Title:          "Copy Ranges Batch",
			ReadOnlyHint:   false,
			IdempotentHint: false,
			OpenWorldHint:  boolPtr(false),
		},
	}, logger, "copy_ranges_batch", h.HandleCopyRangesBatch, h)

	moveRangesBatchDescription := `Move ranges from one source snapshot into multiple explicit targets, then remove the union from source once. Target writes happen before source rewrite; source_diff_previews/source_validation describe removal. Params: source_file, source_fingerprint, targets, source_backup, dry_run.`

	addStructuredTool(server, &mcp.Tool{
		Name:        "move_ranges_batch",
		Description: moveRangesBatchDescription,
		Annotations: &mcp.ToolAnnotations{
			Title:          "Move Ranges Batch",
			ReadOnlyHint:   false,
			IdempotentHint: false,
			OpenWorldHint:  boolPtr(false),
		},
	}, logger, "move_ranges_batch", h.HandleMoveRangesBatch, h)

	listDirDescription := `List direct children of one directory, non-recursive. Returns entries{name,kind}, counts, hidden/VCS skip counters, and ignore_globs behavior. Use glob_file_search for recursive file discovery. Params: target_directory, ignore_globs, include_hidden, include_vcs_metadata.`

	addStructuredTool(server, &mcp.Tool{
		Name:        "list_dir",
		Description: listDirDescription,
		Annotations: &mcp.ToolAnnotations{
			Title:          "List Directory",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
	}, logger, "list_dir", h.HandleListDir, h)

	globDescription := `Recursively find files by glob pattern under one directory. Supports **, simple brace patterns, sort, limit, continuation_after, hidden/VCS flags, ignore_globs, groups, and next read/outline calls for narrow complete results. Params: target_directory, glob_pattern, sort, limit, continuation_after, ignore_globs, include_hidden, include_vcs_metadata.`

	addStructuredTool(server, &mcp.Tool{
		Name:        "glob_file_search",
		Description: globDescription,
		Annotations: &mcp.ToolAnnotations{
			Title:          "Glob File Search",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
	}, logger, "glob_file_search", h.HandleGlobFileSearch, h)

	grepDescription := `Search file contents with literal or regex patterns and MCP-native output. Returns matches/files/counts, search_stats, file_groups with read_ranges, and schema-valid read/outline next calls for narrow complete results. redaction_mode defaults off; include_vcs_metadata is unsupported for content traversal. Params: pattern, path, pattern_mode, output_mode, context/before/after, case_insensitive, type, glob, ignore_globs, multiline, line_window, max_matches_per_file, limit.`

	addStructuredTool(server, &mcp.Tool{
		Name:        "grep",
		Description: grepDescription,
		Annotations: &mcp.ToolAnnotations{
			Title:          "Grep",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
	}, logger, "grep", h.HandleGrepTool, h)

	inspectPathDescription := `Inspect one filesystem path without reading full content. Returns exists/kind, size/timestamps/mode, text line_count, shallow directory counts, symlink metadata, binary/encoding hints, mime_hint, and discovery_context. Params: target_path.`

	addStructuredTool(server, &mcp.Tool{
		Name:        "inspect_path",
		Description: inspectPathDescription,
		Annotations: &mcp.ToolAnnotations{
			Title:          "Inspect Path",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
	}, logger, "inspect_path", h.HandleInspectPath, h)

	workspaceInventoryDescription := `Build a directories-only project inventory: root, directories_page, summary, continuation, counters, and glob hints. Does not list file names. page_complete is page status; summary_coverage_complete/tree_scan_complete/reason/scan_scope are coverage status. Params: target_directory, max_depth, limit, ignore_globs, include_hidden, include_vcs_metadata, include_summary, summary_profile, continuation_after.`

	addStructuredToolWithOutputSchema(server, &mcp.Tool{
		Name:        "workspace_inventory",
		Description: workspaceInventoryDescription,
		Annotations: &mcp.ToolAnnotations{
			Title:          "Workspace Inventory",
			ReadOnlyHint:   true,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
	}, logger, "workspace_inventory", h.HandleWorkspaceInventory, h, handler.WorkspaceInventoryOutputSchema())

	return server
}

func addStructuredTool[In, Out any](server *mcp.Server, tool *mcp.Tool, logger *slog.Logger, toolName string, toolHandler mcp.ToolHandlerFor[In, Out], owner *handler.Handler) {
	addStructuredToolWithOutputSchema(server, tool, logger, toolName, toolHandler, owner, nil)
}

func addStructuredToolWithOutputSchema[In, Out any](server *mcp.Server, tool *mcp.Tool, logger *slog.Logger, toolName string, toolHandler mcp.ToolHandlerFor[In, Out], owner *handler.Handler, outputSchema *jsonschema.Schema) {
	inputSchema, err := jsonschema.For[In](nil)
	if err != nil {
		panic(fmt.Errorf("input schema for %s: %w", toolName, err))
	}
	if outputSchema == nil {
		outputSchema, err = jsonschema.For[Out](nil)
		if err != nil {
			panic(fmt.Errorf("output schema for %s: %w", toolName, err))
		}
	}
	handler.ApplyToolInputSchemaConstraints(inputSchema, toolName)
	handler.ApplyPathOutputSchemaConstraints(outputSchema)
	tool.InputSchema = inputSchema
	tool.OutputSchema = outputSchema

	wrapped := handler.Wrap(logger, toolName, toolHandler)
	server.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var input In
		var decodedCwdID handler.CwdIDInput
		var requestPathCtx handler.PathContext
		hasRequestPathCtx := false
		setter, supportsCwdID := any(&input).(handler.CwdIDSetter)
		if req != nil && req.Params.Arguments != nil && len(req.Params.Arguments) > 0 {
			if supportsCwdID {
				cwdID, cwdErr := handler.DecodeCwdIDFromRaw(req.Params.Arguments)
				if cwdErr != nil {
					return &mcp.CallToolResult{
						Content:           []mcp.Content{},
						StructuredContent: handler.StructuredCwdErrorOutput[Out](cwdErr),
						IsError:           true,
					}, nil
				}
				decodedCwdID = cwdID
				if decodedCwdID.Present {
					pathCtx, cwdErr := owner.BuildPathContext(decodedCwdID)
					if cwdErr != nil {
						return &mcp.CallToolResult{
							Content:           []mcp.Content{},
							StructuredContent: handler.StructuredCwdErrorOutput[Out](cwdErr),
							IsError:           true,
						}, nil
					}
					requestPathCtx = pathCtx
					hasRequestPathCtx = true
					decodedCwdID.PathContext = &requestPathCtx
				}
			}
			if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
				output := handler.StructuredErrorOutput[Out]("Invalid JSON arguments: " + err.Error())
				if hasRequestPathCtx {
					handler.AttachCwdOutputMeta(&output, requestPathCtx)
				}
				return &mcp.CallToolResult{
					Content:           []mcp.Content{},
					StructuredContent: output,
					IsError:           true,
				}, nil
			}
			if supportsCwdID {
				setter.SetCwdID(decodedCwdID)
			}
		}

		result, output, err := wrapped(ctx, req, input)
		if err != nil {
			output := handler.StructuredErrorOutput[Out](err.Error())
			if hasRequestPathCtx {
				handler.AttachCwdOutputMeta(&output, requestPathCtx)
			}
			return &mcp.CallToolResult{
				Content:           []mcp.Content{},
				StructuredContent: output,
				IsError:           true,
			}, nil
		}
		if result == nil {
			result = &mcp.CallToolResult{}
		}
		if result.Content == nil {
			result.Content = []mcp.Content{}
		}
		if hasRequestPathCtx {
			handler.AttachCwdOutputMeta(&output, requestPathCtx)
		}
		result.StructuredContent = output
		return result, nil
	})
}
