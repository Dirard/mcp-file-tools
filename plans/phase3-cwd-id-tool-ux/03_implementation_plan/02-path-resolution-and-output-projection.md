# Stage 2: Path Resolution And Output Projection

## Goal

Create one path mode system that all tools use, so no-cwd and cwd-aware behavior are consistent across inputs, outputs, schemas, errors, recovery hints, and path-map display aliases.

## Files To Add Or Change

Likely additions:

- `filetoolsserver/handler/cwd_path.go`
- `filetoolsserver/handler/cwd_path_contract_test.go`

Likely changes:

- `filetoolsserver/handler/validation.go`
- `filetoolsserver/handler/schema_constraints.go`
- `filetoolsserver/handler/errors.go`
- `filetoolsserver/handler/refactor_errors.go`
- all handler files that currently call `isAbsoluteToolPath`, `ResolvePath`, `displayPath`, `displayResolvedPath`, or `displaySearchPath`

## Path Mode Model

Introduce a request path context:

```go
type PathMode int

const (
    PathModeAbsolute PathMode = iota
    PathModeCwdRelative
)

type PathContext struct {
    Mode   PathMode
    CwdID  CwdIDInput
    ID     int64
    CwdAbs string
    CwdOut string
}
```

Core helper behavior:

- `BuildPathContext(cwdID CwdIDInput) (PathContext, error)`
- `ResolveInputPath(ctx PathContext, raw string) (PathResolutionResult, error)`
- `ProjectOutputPath(ctx PathContext, abs string) (string, error)`
- `ProjectOutputText(ctx PathContext, text string) string` only for controlled templates, not arbitrary raw logs

The implementation may choose exact names, but there must be a single shared route for path mode decisions.
`CwdIDInput` refers to the shared Stage 1 presence-aware input representation that distinguishes absent, `null`, invalid, and positive values before path mode is selected.
Each tool call must build one request-scoped `PathContext` before resolving any path input. For cwd mode, that single step validates and resolves `cwd_id` once, captures `ID`, `CwdAbs`, and `CwdOut`, and all input resolution and output projection in the call reuse that snapshot. Multi-path tools must not perform a fresh cwd lookup per path, so expiry cannot make `source_file` and `target_file` disagree inside one request.

## Input Rules

No `cwd_id`:

- empty paths rejected
- relative paths rejected
- absolute paths accepted through existing normalization and path maps
- outputs are absolute/display paths, slash-normalized

With `cwd_id`:

- `cwd_id` must resolve to a live entry before path resolution
- empty paths rejected
- absolute-looking paths are rejected by a cwd-mode helper that is not limited to the current `GOOS`
- reject POSIX absolute `/...`, Windows drive absolute `C:/...` and `C:\...`, Windows rooted paths without drive such as `\Windows` and `\foo`, Windows drive-relative `C:foo`, UNC paths, and extended UNC/device forms such as `\\server\share`, `//server/share`, `\\?\...`, and `\\.\...`
- `.` is valid and resolves to cwd
- `sub/file.txt` is valid
- `a/../b` is valid if the cleaned path remains under cwd
- `../outside` and any cleaned outside-cwd path are rejected
- no fallback to absolute mode if id lookup fails

Symlink containment for cwd-aware path resolution:

- Cwd mode enforces both lexical containment and realpath containment for existing filesystem objects.
- For read/list/glob/grep/workspace/outline/inspect inputs, after lexical resolution under cwd, evaluate symlinks for the existing target when possible. If the real target resolves outside the registered cwd, return `path_outside_cwd` or the tool-specific outside-cwd shape without leaking the absolute target.
- For write/copy/move tools, cwd containment is additional to the existing refactor safety policy; it must not loosen current symlink rejection for source paths, final target paths, or parent components.
- If existing refactor safety rejects a symlink source, target, or parent component in no-cwd mode, the same path shape must also be rejected in cwd mode even when the symlink resolves inside cwd.
- For write/copy/move targets that may not exist yet, evaluate every existing parent component for cwd containment before mutation. If any existing component resolves outside cwd, reject the request. The final new path may be absent, but its resolved parent must remain under cwd and still pass existing refactor symlink safety.
- For source files in write/copy/move tools, evaluate the existing source path for cwd containment and reject outside-cwd real targets, while preserving existing source-symlink rejection semantics.
- Broken symlinks keep existing missing/broken-symlink semantics for the specific tool, but generated errors must not include absolute targets and must not allow traversal outside cwd.
- Traversal tools must not follow symlinked directories outside cwd. They may omit outside-cwd symlink targets or return generic warnings/errors, but they must not enumerate outside-cwd contents.

Examples that must work when the id is live:

```json
{ "target_path": "internal/test.go", "cwd_id": 1 }
```

```json
{ "target_file": "README.md", "cwd_id": 1 }
```

```json
{
  "source_file": "plans/source.md",
  "targets": [
    { "target_file": "docs/part-1.md" }
  ],
  "cwd_id": 1
}
```

The cwd mode is ergonomic, not an authorization boundary. It must not weaken existing file safety checks.

## Output Projection Rules

Global path formatting:

- Use `/` in every output path on every OS.
- Do not output doubled slashes for Windows drive paths; use `D:/repo/file`.
- Normalize configured path-map display aliases to `/` even when config uses `\`.

No `cwd_id` output:

- Filesystem path fields return absolute/display paths.
- Path-map display roots are allowed, but slash-normalized.

Cwd-aware output:

- Include `"cwd": "D:/ai-apps/mcp-file-tools"` on successful responses and on structured errors only when the supplied `cwd_id` resolved before the error.
- The `cwd` field is the only absolute filesystem path allowed in cwd-aware success or resolved-error filesystem path fields, replay input maps, and generated diagnostic/recovery/hint text. Content, query, pattern, and content-derived outline fields are not part of this no-leak rule.
- All other filesystem path fields are cwd-relative, slash-normalized, and have no leading `./`.
- Use `"."` only for the cwd itself.
- If an absolute path field cannot be represented under cwd, return structured `path_outside_cwd` or omit optional metadata according to the tool contract.
- Do not hide or rewrite structural non-filesystem fields such as `OutlineItem.path`.

## Input And Output Surface Inventory

Filesystem input fields:

- `read_file.target_file`
- `outline_file.target_file`
- `list_dir.target_directory`
- `glob_file_search.target_directory`
- `grep.path`
- `inspect_path.target_path`
- `workspace_inventory.target_directory`
- `copy_ranges.source_file`
- `copy_ranges.target_file`
- `move_ranges.source_file`
- `move_ranges.target_file`
- `copy_ranges_batch.source_file`
- `copy_ranges_batch.targets[].target_file`
- `move_ranges_batch.source_file`
- `move_ranges_batch.targets[].target_file`
- `set_cwd.directory` is absolute-only and never cwd-aware

Actual filesystem output path fields:

- `read_file.file`
- `outline_file.file`
- `outline_file.warnings[].file`
- `list_dir.directory`
- `glob_file_search.target_directory`
- `glob_file_search.files[].path`
- `grep.path`
- `grep.matches[].path`
- `grep.files[]`
- `grep.counts[].path`
- `inspect_path.path`
- `inspect_path.resolved_path`
- `inspect_path.symlink_target`
- `workspace_inventory.root.path`
- `workspace_inventory.root.directories[].path` recursively
- `copy_ranges.source_file`
- `copy_ranges.target_file`
- `copy_ranges.backup_paths[]`
- `copy_ranges.backup_results[].file`
- `copy_ranges.backup_results[].backup_path`
- `copy_ranges.boundary_warnings[].target_file`
- `copy_ranges.warnings[].file`
- `copy_ranges.partial_state.source_file`
- `copy_ranges.partial_state.target_file`
- `copy_ranges.partial_state.files_maybe_modified[]`
- `copy_ranges.partial_state.backup_paths[]`
- `move_ranges` same single-tool fields as `copy_ranges`
- `copy_ranges_batch.source_file`
- `copy_ranges_batch.targets_written[]`
- `copy_ranges_batch.backup_paths[]`
- `copy_ranges_batch.backup_results[].file`
- `copy_ranges_batch.backup_results[].backup_path`
- `copy_ranges_batch.batch_warnings[].file`
- `copy_ranges_batch.warnings[].file`
- `copy_ranges_batch.target_results[].target_file`
- `copy_ranges_batch.target_results[].backup_paths[]`
- `copy_ranges_batch.target_results[].boundary_warnings[].target_file`
- `copy_ranges_batch.target_results[].warnings[].file`
- `copy_ranges_batch.partial_state.source_file`
- `copy_ranges_batch.partial_state.target_results[]` same path fields as `copy_ranges_batch.target_results[]`
- `copy_ranges_batch.partial_state.backup_paths[]`
- `copy_ranges_batch.partial_state.backup_results[].file`
- `copy_ranges_batch.partial_state.backup_results[].backup_path`
- `move_ranges_batch` same batch output path fields as `copy_ranges_batch`; source backup artifacts are represented through `backup_paths[]` and `backup_results[].*` above

Replay input maps with nested path keys:

Replay projection is keyed by the recommended tool's input shape, not by the tool that produced the hint. Only these path keys are projected; arbitrary map keys and the map as a whole must not be treated as filesystem paths.

- `read_file`: `target_file`
- `outline_file`: `target_file`
- `list_dir`: `target_directory`
- `glob_file_search`: `target_directory`
- `grep`: `path`
- `inspect_path`: `target_path`
- `workspace_inventory`: `target_directory`
- `copy_ranges`: `source_file`, `target_file`
- `move_ranges`: `source_file`, `target_file`
- `copy_ranges_batch`: `source_file`, `targets[].target_file`
- `move_ranges_batch`: `source_file`, `targets[].target_file`

Surfaces that must apply this inventory:

- `outline_file.next_recommended_call.recommended_next_input`
- `copy_ranges.partial_state.recommended_next_input`
- `copy_ranges.action_hint.recommended_next_input`
- `move_ranges.partial_state.recommended_next_input`
- `move_ranges.action_hint.recommended_next_input`
- `copy_ranges_batch.partial_state.recommended_next_input`
- `copy_ranges_batch.action_hint.recommended_next_input`
- `move_ranges_batch.partial_state.recommended_next_input`
- `move_ranges_batch.action_hint.recommended_next_input`
- any nested `recommended_next_input` inside batch target results or partial-state recovery hints
- Every cwd-aware replay input map for a path tool must include the current `cwd_id`.

Fields that are not filesystem paths:

- `OutlineItem.path` and nested outline ancestry arrays
- grep match line text
- `read_file.text`
- content-derived outline labels, names, and details
- fingerprint hashes and line counts
- `inspect_path.symlink_target_outside_cwd` boolean metadata
- glob pattern strings
- grep pattern/query metadata
- JSON schema property names
- generated diagnostic, warning, error, reason, recovery, and hint text listed below

## Recommended Next Input Replay Rules

When `cwd_id` is supplied and a response includes a `recommended_next_input` or `next_recommended_call` for another path tool:

- include the same current `cwd_id` in that recommended input
- project every filesystem path value in that recommended input as cwd-relative using the replay path-key inventory above
- do not project unknown fields or whole maps as paths
- do not include `cwd` in the recommended input; `cwd` remains response metadata only
- ensure the recommended input can be replayed as-is without triggering `relative_path_requires_cwd`
- exception: hints whose purpose is to recover from unknown, expired, or unavailable cwd ids may set `recommended_next_tool: "set_cwd"` and a generic reason such as "register this directory again", but must not include `recommended_next_input.directory` or any embedded absolute path

## Text Surfaces That Must Not Leak Absolute Paths

When `cwd_id` is supplied, these fields must be produced from cwd-projected values or from generic templates without raw paths:

- `error`
- `message`
- `reason`
- `recovery_hint`
- `backup_error`
- `warnings[].message`
- `batch_warnings[].message`
- `boundary_warnings[].message`
- `boundary_warnings[].recommended_action`
- `backup_results[].error`
- `target_results[].error`
- `target_results[].backup_error`
- `action_hint.reason`
- `partial_state.error`
- `partial_state.recovery_hint`
- `copy_ranges_batch.partial_state.backup_results[].error`
- `move_ranges_batch.partial_state.backup_results[].error`
- `workspace_inventory.root.read_error`
- `workspace_inventory.root.directories[].read_error` recursively
- `workspace_inventory.truncation_reason`
- any future structured error wrapper text

No-leak checks are field-aware. They apply separately to filesystem path fields, replay input maps, and generated text such as errors, warnings, recovery hints, and action-hint reasons. They must not sanitize or fail on user/file content fields that can legitimately contain absolute-looking strings, such as `read_file.text`, `grep.matches[].text`, glob/grep pattern strings, and content-derived outline labels.

If an incoming cwd-aware request itself contains an absolute path, the error must not echo that absolute string. Use a message such as "absolute paths are not allowed when cwd_id is set".

## Symlink Output Contract

`inspect_path.symlink_target` is optional metadata and must not be an absolute leak in cwd mode.

- If the symlink target resolves under cwd, output it as cwd-relative.
- If the symlink target resolves outside cwd in cwd mode, omit `symlink_target`, set `symlink_target_outside_cwd: true`, and do not include the target path in any error/message field.
- If the symlink target is broken and outside-cwd cannot be resolved safely, omit `symlink_target`; keep `broken_symlink` semantics and do not emit an absolute target.
- `symlink_target_outside_cwd` is absent or false when no `cwd_id` is supplied or when the target is representable under cwd.
- In no-cwd mode, keep the existing symlink target semantics but slash-normalize the displayed path.

## Acceptance

- Every handler uses the shared path mode instead of ad hoc early absolute checks.
- No-cwd outputs never contain `\`.
- Cwd-aware filesystem path fields, replay input maps, and generated diagnostic/recovery/hint text never contain the cwd absolute path outside the `cwd` field.
- Content/query/content-derived fields remain literal and are not sanitized merely because they contain absolute-looking text.
- Cwd-aware relative output never starts with `./`.
- `"."` is used for cwd itself.
- Tests cover path-map aliases configured with backslashes.

## Stop And Ask If

- A path-bearing string cannot be safely rebuilt from projected values.
- A required output field points outside cwd and product must choose between omission, structured outside-cwd metadata, or an error.
