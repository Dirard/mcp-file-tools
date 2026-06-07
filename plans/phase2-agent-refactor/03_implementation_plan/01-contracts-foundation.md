# Stage 1: Contracts And Foundation

## Goal

Add the shared contracts that all Phase 2 tools depend on: typed inputs/outputs, fingerprints, error/recovery structures, path schema constraints, config, tool registration, and test scaffolding.

## Depends On

- Accepted concept in `01_human_concept.md` and `02_technical_concept.md`.
- Current handler patterns in `filetoolsserver/handler/tool_types.go`, `response.go`, `validation.go`, and `server.go`.

## Touched Areas

- `filetoolsserver/handler/tool_types.go` or new `refactor_types.go`
- `filetoolsserver/handler/errors.go`
- `filetoolsserver/handler/response.go`
- `filetoolsserver/handler/schema_constraints.go`
- `filetoolsserver/server.go`
- `internal/config/config.go`
- `internal/config/config_test.go`
- tests under `filetoolsserver/handler`

## Steps

1. Add shared type aliases/structs without handlers:
   - `SourceLineRange` with JSON `start_line` / `end_line`.
   - `FileFingerprint` with `sha256`, `size_bytes`, `line_count`, `modified_unix_nano`.
   - `ActionHint` with `safe_to_retry`, `recommended_next_tool`, `recommended_next_input`, `reason`.
   - `BoundaryWarning`, `ToolWarning`, `WarningSummary`, `OutlineStats`, `PartialState`, `BatchPartialState`, `BatchTargetResult`, `BackupResult`.
   - Input structs for all five tools.
   - Output structs for all five tools.
   - `MoveRangesBatchInput` includes top-level `source_backup` with the same shape as `backup`; default is `{"mode":"none"}`. This makes source backup explicit instead of inferring it from per-target backup flags.

2. Keep existing `LineRange` for existing read-file output:
   - Do not change `ReadFileOutput.requested_range` or `ReadFileOutput.range`.
   - Use new range structs for Phase 2 to match concept JSON.

3. Add path schema constraints for new nested path fields:
   - Add `source_file` and `target_file` to `pathInputSchemaFields`.
   - Ensure nested `targets[].target_file` receives absolute-path schema constraints via existing recursive walker.
   - Add all Phase 2 output path fields to `pathOutputSchemaFields` or to manual output schemas:
     - `source_file`;
     - `target_file`;
     - `backup_paths`;
     - `files_maybe_modified`;
     - `targets_written`;
     - `backup_results[].file`;
     - `backup_results[].backup_path`;
     - `target_results[].target_file`;
     - `target_results[].backup_paths`;
     - `partial_state.source_file`;
     - `partial_state.target_results[].target_file`;
     - `partial_state.backup_results[].file`;
     - `partial_state.backup_results[].backup_path`;
     - `recommended_next_input.target_file`.
   - If the generic schema walker cannot constrain nested array/object fields precisely, create manual output schema helpers for Phase 2 tools, as `WorkspaceInventoryOutputSchema()` already does for recursive output.

4. Add config for write threshold:
   - Add `EnvWriteThreshold = "MCP_WRITE_THRESHOLD"`.
   - Add `WriteThreshold int64` to `config.Config`.
   - Default to `67108864` bytes (64 MiB), matching existing `DefaultMaxSize`.
   - Add tests for default, env override, and invalid env fallback.

5. Add bounded batch config:
   - Add `EnvBatchMaxTargets = "MCP_BATCH_MAX_TARGETS"` with default `100`.
   - Add `EnvBatchMaxRangesPerTarget = "MCP_BATCH_MAX_RANGES_PER_TARGET"` with default `100`.
   - Add `EnvBatchMaxRangesPerCall = "MCP_BATCH_MAX_RANGES_PER_CALL"` with default `500`.
   - Add `EnvBatchMaxPlannedBytes = "MCP_BATCH_MAX_PLANNED_BYTES"` with default equal to `WriteThreshold`.
   - Add these fields to `config.Config`, with tests for defaults, env overrides, invalid fallback, zero/negative rejection, and default planned-bytes tracking custom `MCP_WRITE_THRESHOLD`.
   - These are hard per-call limits for batch tools, not pagination controls.

6. Design structured error helpers:
   - Keep `errorResult` empty-content behavior.
   - Add helper constructors for Phase 2 errors that populate typed structured output fields, not only `Error string`.
   - Ensure `StructuredErrorOutput[T]`, `setStructuredErrorOutput`, wrapper-level invalid JSON handling, handler-returned `error`, and recovered panic paths set the typed `error` field for every Phase 2 output type.
   - Add tests with all five new output structs for invalid JSON and recovered panic paths so generic wrapper failures stay machine-readable.
   - Preserve existing tools' current error shape unless deliberately migrated in a separate compatibility step.

7. Prepare tool registration metadata, but do not expose not-implemented tools:
   - Tool names: `outline_file`, `copy_ranges`, `move_ranges`, `copy_ranges_batch`, `move_ranges_batch`.
   - `outline_file` annotations: read-only true, idempotent true, open-world false.
   - Write tools annotations: read-only false, idempotent false, open-world false.
   - Descriptions must include absolute path requirement, `dry_run`, fingerprints, and recovery hints.
   - Update `serverInstructions` to include 11 tools after Phase 2.
   - Registration happens in the stage that adds the real handler:
     - `outline_file` registers in Stage 2.
     - `copy_ranges` and `move_ranges` register in Stage 4.
     - `copy_ranges_batch` and `move_ranges_batch` register in Stage 5.
   - Do not register public stub tools that return "not implemented".
   - Stage 1 must stay compile-ready with types/schema helpers only.

8. Add schema tests:
   - New input schemas expose required agent-friendly fields.
   - No new schema exposes `cursor` or `nextCursor`.
   - All path fields have minimum length and absolute path pattern.
   - `dry_run`, `output_profile`, `line_window`, `targets`, `recommended_next_tool`, `warnings_truncated`, aggregate warning counts, and batch partial fields are present.

## Checks

- Unit tests for config and schema generation.
- Existing schema tests still pass.
- Stage 1 schema tests prove new contracts are valid without public registration.
- Config tests cover write threshold and all batch bounds.
- Structured-error tests cover handler-level errors plus wrapper-level invalid JSON and recovered panic for all five Phase 2 output structs.
- Final registration test in Stage 6 proves all five new tools are present and existing six remain present.

## Handoff / Next Stage

Move to `02-outline-file.md` only after structs and schema contracts compile, tests can instantiate the new inputs/outputs, and no not-implemented public tool is registered.

## Stop And Ask If

- MCP SDK annotations do not support the intended mutating-tool flags and a visible behavior decision is needed.
- Output schemas cannot express recursive/nested batch partial state without manual schema construction.
