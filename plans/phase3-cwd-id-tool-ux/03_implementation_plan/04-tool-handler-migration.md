# Stage 4: Tool Handler Migration

## Goal

Move all existing path tools onto the shared cwd-aware path contract without widening unrelated behavior.

## Common Handler Steps

For every affected handler:

1. Add `cwd_id` to the input type.
2. Build one request-scoped `PathContext` from `cwd_id` before resolving any path field.
3. Replace early `isAbsoluteToolPath` checks with shared path mode resolution.
4. Resolve all input paths through the shared resolver using the same `PathContext`, including the Stage 2 symlink containment policy.
5. Use absolute resolved paths internally.
6. Project every filesystem path output through the shared projector.
7. Attach `cwd_id` and `cwd` metadata on cwd-aware responses.
8. Rebuild error, warning, action hint, and recovery text from projected paths or generic templates.
9. Make every cwd-aware `recommended_next_input` replayable by including the current `cwd_id` with cwd-relative path fields, except recovery hints that intentionally point to `set_cwd` after an unknown or expired id.
10. Add focused tests for both no-cwd and cwd-aware behavior.

Every affected input type must use the shared presence-aware `cwd_id` decoder from Stage 1 rather than a plain `*int`. The normal generic JSON decoding route and custom routes such as `GrepToolInput.UnmarshalJSON` must all distinguish absent, `null`, malformed, non-positive, and positive values.

## Read File

Files:

- `filetoolsserver/handler/read_file.go`
- `filetoolsserver/handler/read_output.go`
- `filetoolsserver/handler/tool_types.go`

Tasks:

- Accept `target_file` as absolute without `cwd_id` and relative with `cwd_id`.
- Output `file` as absolute/display no-cwd or cwd-relative with `cwd_id`.
- Keep `text`, ranges, and bounded reads unchanged.
- Ensure EOF/range errors do not echo absolute paths in cwd mode.
- Use the shared display-line count model from Stage 5.

## List Dir

Files:

- `filetoolsserver/handler/list_dir.go`

Tasks:

- Resolve `target_directory` by path mode.
- Output `directory` through projection.
- Child `entries[].name` are names, not filesystem paths; leave unchanged.
- Empty-directory messages must not contain absolute paths in cwd mode.

## Glob File Search

Files:

- `filetoolsserver/handler/glob_file_search.go`
- `filetoolsserver/handler/glob_match.go`

Tasks:

- Resolve `target_directory` by path mode.
- Output `target_directory` and `files[].path` through projection.
- Keep glob patterns unchanged.
- Ensure ignored/truncated messages do not embed raw roots.
- Preserve sorting and dot-entry behavior.

## Grep

Files:

- `filetoolsserver/handler/grep_tool.go`
- `filetoolsserver/handler/grep_rows.go`
- `filetoolsserver/handler/tool_types.go`

Tasks:

- Resolve `path` by path mode.
- Update `GrepToolInput.UnmarshalJSON` so the custom decoder uses the shared presence-aware `cwd_id` handling while keeping the existing `-A`, `-B`, `-C`, and `-i` alias behavior.
- Output root `path`, `matches[].path`, `files[]`, and `counts[].path` through projection.
- Match text is content, not a path field; do not alter it.
- Error messages for binary, missing, invalid regex, and traversal failures must avoid absolute path leaks in cwd mode.

## Inspect Path

Files:

- `filetoolsserver/handler/inspect_path.go`

Tasks:

- Resolve `target_path` by path mode.
- Output `path` and `resolved_path` through projection.
- Output `symlink_target` only when it can be represented under the current mode.
- In cwd mode, if symlink target resolves outside cwd, omit `symlink_target`, set `symlink_target_outside_cwd: true`, and do not include the target path in generated text.
- In no-cwd mode, keep existing `symlink_target` semantics but slash-normalize the path.
- Use the shared display-line count model from Stage 5.
- Keep file, directory, missing, symlink, hidden, readable, binary, encoding, and timestamp semantics intact.

## Workspace Inventory

Files:

- `filetoolsserver/handler/workspace_inventory.go`
- `filetoolsserver/handler/workspace_inventory_schema.go`

Tasks:

- Resolve `target_directory` by path mode.
- Project every recursive node `path`.
- Sanitize every recursive node `read_error` so cwd mode never embeds raw absolute paths.
- Sanitize top-level `truncation_reason` so cwd mode never embeds raw absolute paths.
- Use `"."` for the root node when target directory equals cwd in cwd mode.
- Keep direct file/dir counts, max depth, limit, and dot-entry behavior unchanged.

## Outline File

Files:

- `filetoolsserver/handler/outline_file.go`
- `filetoolsserver/handler/outline_common.go`
- `filetoolsserver/handler/outline_schema.go`

Tasks:

- Resolve `target_file` by path mode.
- Output top-level `file` and any warning/recommended-input file path through projection.
- Do not treat `OutlineItem.path` as filesystem paths.
- Implement generic text fallback and line-count model in Stage 5.

## Single Copy/Move Tools

Files:

- `filetoolsserver/handler/copy_ranges.go`
- `filetoolsserver/handler/move_ranges.go`
- `filetoolsserver/handler/range_transfer.go`
- `filetoolsserver/handler/refactor_errors.go`
- `filetoolsserver/handler/refactor_safety.go`
- `filetoolsserver/handler/refactor_types.go`

Tasks:

- Add `cwd_id` to inputs.
- Resolve `source_file` and `target_file` by path mode.
- Preserve existing refactor symlink safety exactly; cwd containment checks are added on top and must not allow symlink source/target/parent shapes currently rejected in no-cwd mode.
- Keep source/target fingerprint and mutation order unchanged.
- Project actual path outputs:
  - `source_file`
  - `target_file`
  - `backup_paths[]`
  - `backup_results[].file`
  - `backup_results[].backup_path`
  - `boundary_warnings[].target_file`
  - `warnings[].file`
  - `partial_state.*` file path fields
- Sanitize generated text surfaces without treating them as path fields:
  - `backup_results[].error`
  - `boundary_warnings[].message`
  - `boundary_warnings[].recommended_action`
  - `warnings[].message`
  - `partial_state.error`
  - `partial_state.recovery_hint`
  - `action_hint.reason`
- Project replay input maps using the Stage 2 recommended-tool path-key inventory, including `target_file`, `target_directory`, `target_path`, `path`, `source_file`, and `targets[].target_file` as appropriate for the recommended tool:
  - `partial_state.recommended_next_input`
  - `action_hint.recommended_next_input`
- Add current `cwd_id` to cwd-aware `action_hint.recommended_next_input` and any nested replayable path-tool inputs.
- Rewrite action-hint and recovery reason text so cwd mode never mentions raw absolute paths.
- Preserve sidecar backup behavior and partial-state semantics.

## Batch Copy/Move Tools

Files:

- `filetoolsserver/handler/batch_ranges.go`
- `filetoolsserver/handler/refactor_types.go`
- `filetoolsserver/handler/refactor_errors.go`

Tasks:

- Add `cwd_id` to batch inputs.
- Resolve `source_file` and each `targets[].target_file` by path mode.
- Preserve existing refactor symlink safety exactly for source paths, target paths, and parent components; cwd containment checks are additional and must not loosen no-cwd behavior.
- Keep source fingerprint, target preconditions, dry-run behavior, mutation order, and partial failure semantics unchanged.
- Project all top-level and per-target path outputs from the Stage 2 actual path-field inventory.
- Project nested `recommended_next_input` maps for source and every target using the Stage 2 recommended-tool path-key inventory, including `target_file`, `target_directory`, `target_path`, `path`, `source_file`, and `targets[].target_file` as appropriate for the recommended tool.
- Project top-level `action_hint.recommended_next_input` for both `copy_ranges_batch` and `move_ranges_batch` without treating the whole map as a path field.
- Add current `cwd_id` to every cwd-aware batch `recommended_next_input` that replays a path tool, including top-level action hints and partial-state recovery inputs.
- Ensure `batch_warnings[].file`, `warnings[].file`, `warnings[].message`, top-level `backup_results[].error`, `target_results[].error`, `target_results[].backup_error`, `target_results[].boundary_warnings[].target_file`, `target_results[].boundary_warnings[].message`, `target_results[].boundary_warnings[].recommended_action`, `target_results[].warnings[].file`, `target_results[].warnings[].message`, and partial-state nested errors use projected paths or safe generic text.
- Ensure top-level `error` and partial-state `error` / `recovery_hint` fields never echo raw absolute paths in cwd mode.
- Implement batch metric contract in Stage 5.

## Middleware And Generic Errors

Files:

- `filetoolsserver/handler/middleware.go`
- `filetoolsserver/handler/response.go`
- `filetoolsserver/handler/errors.go`

Tasks:

- Introduce or update the shared structured error wrapper so every existing path tool can emit `error`, `error_code`, supplied `cwd_id`, resolved `cwd`, and `action_hint` even when the tool's normal output type only had plain `error`.
- For `cwd_id_unknown`, `cwd_id_expired`, and `cwd_state_unavailable` on any existing path tool, return `action_hint.safe_to_retry = false` and `action_hint.recommended_next_tool = "set_cwd"` without `recommended_next_input.directory`.
- Ensure structured error output can include `cwd_id`, `cwd`, and `error_code` where available.
- Panic and middleware errors must not leak request absolute paths.
- If middleware lacks path context, return generic error text without path echoing.
- Add an explicit `set_cwd` registration/error route instead of relying on a single generic `Out` schema if that would merge success and error shapes.
- `set_cwd` success schema is generated from a success type with only `cwd_id`.
- `set_cwd` error schema/envelope allows `error`, `error_code`, and optional `action_hint` without adding those fields to the success schema.
- Cover invalid JSON, unknown input fields, handler errors, and panic/recovery paths through this route; success remains `{ "cwd_id": n }`.

## Acceptance

- Every existing path handler has a cwd-aware happy-path test and a cwd-aware invalid absolute-input test.
- No-cwd compatibility tests still prove relative inputs are rejected.
- Write and batch partial-state tests prove no absolute leaks in nested recovery surfaces.
- Internal mutation paths continue to use absolute resolved paths and existing locks/limiters.

## Stop And Ask If

- A handler builds path text from arbitrary errors and cannot be made leak-free without changing error shape.
- Existing partial-state semantics conflict with relative recommended inputs.
