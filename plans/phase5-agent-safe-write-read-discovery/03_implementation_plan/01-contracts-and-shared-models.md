# Stage 1: Contracts And Shared Models

## Goal

Define shared DTOs, schemas, redaction, continuation, path projection and error codes before tool-specific implementation starts.

## Depends On

- Clean Phase 5 concept.
- Current Phase 3 cwd path contract.
- Current Phase 4 grep contract.

## Touched Areas

- `filetoolsserver/handler/tool_types.go`
- `filetoolsserver/handler/refactor_types.go`
- `filetoolsserver/handler/schema_constraints.go`
- `filetoolsserver/handler/cwd_path.go`
- `filetoolsserver/handler/response.go`
- `filetoolsserver/handler/errors.go`
- `filetoolsserver/server.go`
- `README.md`
- `TOOLS.md`
- `server.json`

## Shared DTOs

### Diff Preview

Add:

```go
type DiffPreview struct {
    Role         string            `json:"role"`
    Format       string            `json:"format"`
    Text         string            `json:"text,omitempty"`
    Truncated    bool              `json:"truncated"`
    Stats        DiffPreviewStats  `json:"stats"`
    Redacted     bool              `json:"redacted"`
    RedactionMode string           `json:"redaction_mode,omitempty"`
    PathMode     string            `json:"path_mode,omitempty"`
}

type DiffPreviewStats struct {
    FilesChanged int `json:"files_changed"`
    LinesAdded   int `json:"lines_added"`
    LinesRemoved int `json:"lines_removed"`
    HunksReturned int `json:"hunks_returned"`
    HunksOmitted  int `json:"hunks_omitted,omitempty"`
}
```

Rules:

- Range write outputs expose `diff_previews []DiffPreview`, not one ambiguous singular field.
- `role` values include `target`, `source_removal`, and `source_rewrite`; for `move_ranges_batch`, source rewrite previews use `source_diff_previews[]`.
- `format` is `unified`.
- `text` is bounded by `MCP_DIFF_PREVIEW_MAX_BYTES`, default `32768`.
- `truncated=true` when text/hunks were omitted.
- Diff paths use the same projected output mode as the tool.
- Redaction applies before truncation statistics are finalized.

### Joiner And Boundary

Add:

```go
type JoinerEffect struct {
    Requested string `json:"requested"`
    Normalized string `json:"normalized"`
    InsertedNewlinesBetweenBlocks int `json:"inserted_newlines_between_blocks"`
    LeftEndedWithNewline bool `json:"left_ended_with_newline"`
    RightStartedWithNewline bool `json:"right_started_with_newline"`
}

type BoundaryPreview struct {
    TargetFile string `json:"target_file,omitempty"`
    Placement string `json:"placement"`
    Before string `json:"before,omitempty"`
    Between string `json:"between,omitempty"`
    After string `json:"after,omitempty"`
    Redacted bool `json:"redacted"`
    RedactionMode string `json:"redaction_mode,omitempty"`
    Truncated bool `json:"truncated"`
}
```

Rules:

- `blank_line` means exactly one visually blank line between adjacent non-empty blocks.
- `single_newline` means text blocks are separated by one line break when both sides are non-empty.
- `none` means no inserted separator beyond source/target existing bytes.
- Boundary preview is bounded to a few lines/chars around the join boundary.

### Validation

Add:

```go
type WriteValidation struct {
    Status string `json:"status"`
    TargetReadBack []ReadBackWindow `json:"target_read_back"`
    SourceReadBack []ReadBackWindow `json:"source_read_back,omitempty"`
    RedactionMode string `json:"redaction_mode,omitempty"`
    NextRecommendedCall *ActionHint `json:"next_recommended_call,omitempty"`
    NextRecommendedCalls []ActionHint `json:"next_recommended_calls,omitempty"`
    ErrorCode string `json:"error_code,omitempty"`
    Error string `json:"error,omitempty"`
}

type ReadBackWindow struct {
    File string `json:"file"`
    Range LineRange `json:"range"`
    Text string `json:"text,omitempty"`
    Truncated bool `json:"truncated"`
    Redacted bool `json:"redacted"`
    RedactionMode string `json:"redaction_mode,omitempty"`
}
```

Status values:

- `not_requested`
- `planned_only`
- `applied_and_verified`
- `applied_validation_truncated`
- `applied_validation_failed`
- `partial_state`

Rules:

- If validation read-back is truncated, skipped by size limits or otherwise insufficient to inspect the affected range, set `status=applied_validation_truncated`.
- For `applied_validation_truncated`, include a cwd-safe `next_recommended_call` for `read_file` that targets the affected file/window with explicit `start_line` and `chunk_lines` or range parameters.
- If several read-back windows need follow-up, use the global hint-list invariant: `next_recommended_call` is primary, and `next_recommended_calls[]` is the full ordered list with element 0 identical to the primary hint.
- These hints are inspection-only and never suggest another write.

### Backup Result Recovery

Extend existing `BackupResult`:

```go
ErrorCode string `json:"error_code,omitempty"`
```

Rules:

- `backup_results[]` remains the detailed source for backup creation success/failure in single and batch outputs.
- If a requested backup cannot be created and the write is refused before mutation, set the relevant `backup_results[].error_code=backup_creation_failed` and use top-level `error_code=backup_creation_failed`.
- If a backup failure is non-fatal in a future path, still set `backup_results[].error_code` and keep the top-level tool status honest; Phase 5 should not silently downgrade backup failure to an unstructured string.

### Write Output Placement

Add the safety fields in predictable locations:

```go
// Single copy_ranges / move_ranges outputs.
DiffPreviews []DiffPreview `json:"diff_previews"`
JoinerEffect JoinerEffect `json:"joiner_effect"`
BoundaryPreview BoundaryPreview `json:"boundary_preview"`
Validation WriteValidation `json:"validation"`

// Batch target result.
DiffPreviews []DiffPreview `json:"diff_previews"`
JoinerEffect JoinerEffect `json:"joiner_effect"`
BoundaryPreview BoundaryPreview `json:"boundary_preview"`
Validation WriteValidation `json:"validation"`

// Move batch top-level source rewrite/removal.
SourceDiffPreviews []DiffPreview `json:"source_diff_previews,omitempty"`
SourceValidation WriteValidation `json:"source_validation,omitempty"`
BackupDiscovery *BackupDiscoveryHint `json:"backup_discovery,omitempty"`
```

Rules:

- Batch per-target fields describe only that target's planned/applied write.
- Move batch source rewrite/removal uses top-level `source_diff_previews` and `source_validation`, never a fake target result.
- Top-level batch aggregate fields may summarize truncation/status counts but must not replace per-target safety evidence.

### Backup Discovery

Add:

```go
type BackupDiscoveryHint struct {
    BackupPaths []string `json:"backup_paths"`
    DiscoveryGroups []BackupDiscoveryGroup `json:"discovery_groups"`
    NextRecommendedCall *ActionHint `json:"next_recommended_call,omitempty"`
    NextRecommendedCalls []ActionHint `json:"next_recommended_calls,omitempty"`
    Reason string `json:"reason,omitempty"`
}

type BackupDiscoveryGroup struct {
    Role string `json:"role,omitempty"`
    Directory string `json:"directory"`
    GlobPattern string `json:"glob_pattern"`
    IncludeHidden bool `json:"include_hidden"`
    BackupPaths []string `json:"backup_paths"`
    NextRecommendedCall ActionHint `json:"next_recommended_call"`
}
```

Placement:

- Single write outputs expose `backup_discovery` when sidecar backups are created or when backup paths exist.
- Batch write outputs expose top-level `backup_discovery` aggregating created backup paths and grouping rediscovery hints by directory and role.
- Per-target `backup_results[]` remain the detailed source of backup creation success/failure.

Rules:

- `backup_discovery.backup_paths` uses projected path mode.
- `backup_discovery.discovery_groups[]` uses projected directory and backup path mode.
- Each group `next_recommended_call` uses `glob_file_search` with `include_hidden=true`, current public `glob_pattern`, that group's backup directory, same cwd mode, and no cleanup/delete action.
- `next_recommended_call` is the primary single-group convenience; batch consumers use the full ordered `next_recommended_calls[]` list or `discovery_groups[]`.
- Under `cwd_id`, every path in backup discovery output and recommended input is cwd-relative except absolute `cwd`.

### Coverage And Continuation

Add:

```go
type ReadCoverage struct {
    RequestedRangeComplete bool `json:"requested_range_complete"`
    CompleteFileRead bool `json:"complete_file_read"`
    FileTotalLinesKnown bool `json:"file_total_lines_known"`
    NextRange *SourceLineRange `json:"next_range,omitempty"`
    Proof *ReadCoverageProof `json:"proof,omitempty"`
}

type ReadCoverageProof struct {
    SizeBytes int64 `json:"size_bytes"`
    ModifiedUnixNano int64 `json:"modified_unix_nano"`
    SHA256 string `json:"sha256,omitempty"`
    ProofStrength string `json:"proof_strength"`
    Range SourceLineRange `json:"range"`
}
```

Add generic:

```go
type ContinuationHint struct {
    Complete bool `json:"complete"`
    Consistency string `json:"consistency,omitempty"`
    CanonicalQueryHash string `json:"canonical_query_hash,omitempty"`
    LastSortKey *DiscoverySortKey `json:"last_sort_key,omitempty"`
    StaleIfFileChanges bool `json:"stale_if_file_changes,omitempty"`
    NextRecommendedCall *ActionHint `json:"next_recommended_call,omitempty"`
    NextRecommendedCalls []ActionHint `json:"next_recommended_calls,omitempty"`
    Reason string `json:"reason,omitempty"`
}

type OutlineContinuationHint struct {
    Complete bool `json:"complete"`
    Consistency string `json:"consistency,omitempty"`
    CanonicalQueryHash string `json:"canonical_query_hash,omitempty"`
    LastIncludedLine int `json:"last_included_line,omitempty"`
    NextOmittedLine int `json:"next_omitted_line,omitempty"`
    NextOmittedItemKey string `json:"next_omitted_item_key,omitempty"`
    SourceFingerprint FileFingerprint `json:"source_fingerprint"`
    StaleIfFileChanges bool `json:"stale_if_file_changes"`
    NextRecommendedCall *ActionHint `json:"next_recommended_call,omitempty"`
    NextRecommendedCalls []ActionHint `json:"next_recommended_calls,omitempty"`
    Reason string `json:"reason,omitempty"`
}

type DiscoveryContinuationAfter struct {
    CanonicalQueryHash string `json:"canonical_query_hash"`
    LastSortKey DiscoverySortKey `json:"last_sort_key"`
}

type DiscoverySortKey struct {
    Path string `json:"path"`
    ModifiedUnixNano *int64 `json:"modified_unix_nano,omitempty"`
    SizeBytes *int64 `json:"size_bytes,omitempty"`
}

type WorkspaceDirectoryPageEntry struct {
    Path string `json:"path"`
    ParentPath string `json:"parent_path,omitempty"`
    Depth int `json:"depth"`
    DirectFileCount int `json:"direct_file_count"`
    DirectDirCount int `json:"direct_dir_count"`
    ReadError string `json:"read_error,omitempty"`
}
```

Rules:

- No hidden server state.
- Recommended inputs replay the whole query.
- If a continuation depends on file version, include `proof_strength`.
- For `ReadCoverageProof`, `sha256`, `size_bytes`, and `modified_unix_nano` describe the full source file. `range` only describes the returned line coverage. Exact continuation proof never uses a returned-range hash.
- `proof_strength=exact` MUST include non-empty `sha256`, `size_bytes`, and `modified_unix_nano`. If the tool cannot compute a non-empty SHA256 within configured thresholds, it MUST emit `proof_strength=stat_only` instead of `exact`.
- `proof_strength=exact` is the only proof that can reject same-size/same-mtime file changes.
- `proof_strength=stat_only` may support best-effort hints, but must not claim exact stale detection.
- `canonical_query_hash` is the hash of normalized tool name, path mode, target path, filters, hidden/VCS policy, sort mode, ignore globs, limits and relevant options.
- `last_sort_key` carries the exact per-tool key needed to continue.
- Required sort-key fields:
  - `modified_*`: `path` and `modified_unix_nano` as JSON number;
  - `size_*`: `path` and `size_bytes` as JSON number;
  - `path_*`, `directory_path_asc`, and workspace inventory: `path` only.
- Discovery tools with paged continuation accept a structured continuation input containing both `canonical_query_hash` and `last_sort_key`; a sort key without the hash is insufficient for mismatch detection.
- `consistency` values are `unchanged`, `changed_tree`, `changed_file`, or `unknown`.
- Tools must not set `complete=true` after a stale/changed/unknown continuation unless the current call independently reached the end of the current query.
- Tools must not report `changed_tree` unless they have a proof-backed reason; otherwise use `unknown` and warn that duplicate/skip is possible.
- `outline_file.continuation` uses `OutlineContinuationHint`, not discovery `last_sort_key`.
- `outline_file.continuation.canonical_query_hash` is computed from normalized tool name, path mode, target file, language, output profile, include flags, filters (`line_window`, `name_contains`, `kinds`, `max_items`, `max_depth`) and relevant limits.
- `outline_file.continuation.source_fingerprint` is the file fingerprint used for the current outline.
- `outline_file.continuation.last_included_line` is informational only and must not be used as the resume cursor for nested outlines.
- When `outline_file` omits items due to `max_items`, depth, line-window or output truncation, it records the first omitted flattened outline item as `next_omitted_line` and, when needed for deterministic disambiguation, `next_omitted_item_key`.
- `outline_file.continuation.next_recommended_call.recommended_next_input` replays `outline_file` with the same query plus a bounded `line_window` that starts at `next_omitted_line`, not after a parent item's end line; it includes `cwd_id` in cwd mode.
- If the flattened item ordering cannot prove no skip/duplicate for the next window, `continuation.consistency="unknown"` and `reason` must warn that duplicate or skipped outline items are possible.
- `outline_file.continuation.complete=true` only when the parser/filter/output stage reached the end of all items for the current query. If output truncates by `max_items` or line window, `complete=false`.
- If a replayed outline continuation sees a different file fingerprint, return `continuation.consistency="changed_file"` and do not claim complete coverage for the old query.
- Existing Go and Markdown exact outlines keep their current parser behavior; Phase 5 only aligns continuation metadata. Generic text outlines may continue to produce generic chunks, but their continuation uses the same fingerprint/query-hash/cwd projection contract.
- `workspace_inventory.directories_page[]` is the canonical stateless continuation surface for directory coverage. It is a flat deterministic path-ordered page with `path`, `parent_path`, `depth`, direct counts and optional `read_error`.
- `workspace_inventory.root` remains a bounded overview tree for the current call. Continuation consumers must not rely on `root` shape to merge pages.
- In an unchanged tree, concatenating `directories_page[]` from page 1..N using `continuation_after` must cover each returned directory exactly once in deterministic path order.
- On changed/unknown trees, `directories_page[]` remains best-effort and must carry the same consistency warning contract as `ContinuationHint`.

### Redaction

Add helper contract:

- input field `redaction_mode` where tools produce new content-bearing risky previews;
- values `auto` and `strict`;
- default `auto`;
- no `off` in Phase 5 for hidden/config/log broad outputs.

Redaction matrix:

| Surface | Existing raw behavior | Phase 5 `auto` behavior |
| --- | --- | --- |
| `read_file.text` | Preserved raw exact read | unchanged unless a later explicit redaction option is added |
| `read_files.items[].text` | New surface | redacts hidden/config/log-like paths and secret-like values |
| exact visible file `grep.matches[].text` | Preserved | unchanged unless caller sets `redaction_mode=strict` |
| broad visible `grep` over config/log-like paths | Existing broad surface | redacts config/log-like paths and secret-like values |
| broad `grep(include_hidden=true).matches[].text` | New broadened surface | redacts hidden/config/log-like paths and secret-like values |
| `diff_previews[].text` | New surface | redacts when either file is hidden/config/log-like or secret-like values are detected |
| `validation.*.text` | New surface | same as diff/read-back redaction |
| visibility diagnostics | New surface | do not quote raw file content |

Shared risky-content predicate:

- path is dot-hidden;
- path basename or extension is config/log/env-like: `.env`, `.env.*`, `*.log`, `*.pem`, `*.key`, `*.crt`, `*.p12`, `*.pfx`, `*kubeconfig*`;
- content line contains a secret-like key/value, bearer/basic token, private-key block, or long high-entropy string.

Redaction targets:

- key/value assignments;
- env-like `NAME=value`;
- JSON/YAML/TOML-ish secret keys;
- bearer/basic tokens;
- private-key blocks;
- long high-entropy strings.

Output must preserve:

- file path;
- line number;
- key/name when safe;
- replacement marker such as `[REDACTED]`;
- `redacted=true`.

Diff preview pipeline:

1. Compute raw before/after bytes and raw structural diff stats on the unredacted diff.
2. Redact every returned content-bearing diff, boundary-preview and read-back string according to the effective redaction mode.
3. Apply byte/hunk truncation to the redacted returned surface.
4. Set `truncated`, `hunks_omitted`, and related returned-surface counters from the redacted truncated output while keeping structural add/remove stats from step 1.

### Action Hint Lists

Use one invariant for every Phase 5/Phase 6 hint list:

- `next_recommended_call` is the primary next action.
- `next_recommended_calls[]`, when present, is the full ordered list of useful next actions.
- `next_recommended_calls[0]` must be identical to `next_recommended_call`.
- Do not use `next_recommended_calls[]` as an alternates-only list that omits the primary hint.
- Backup discovery follows the same rule: single writes can expose the same first group in `next_recommended_call`, while `next_recommended_calls[]` contains all group hints in priority order and repeats the primary as element 0.

Public redaction echo fields:

- `grep.matches[].redacted`;
- `grep.matches[].redaction_mode`;
- `grep.counts[]` and `grep.files[]` do not need redaction echo because they do not return content snippets;
- `read_files.items[].redacted`;
- `read_files.items[].redaction_mode`.

Grep exact vs broad predicate:

- `exact file grep` means `path` resolves to one existing regular file before search, no directory walk occurs, and no recursive `glob` expansion is used to choose files.
- `broad grep` means `path` resolves to a directory, or the search enumerates multiple candidate files through recursion, `glob`, type filtering, or hidden traversal.
- Exact visible file grep preserves current raw snippets in `redaction_mode=auto`; callers can still choose `strict`.
- Exact hidden/config/log-like file grep is allowed for compatibility, but if `redaction_mode=strict` is set it redacts.
- Broad visible grep over config/log-like candidate paths redacts secret-like snippets in `auto`.
- Broad hidden grep with `include_hidden=true` redacts hidden/config/log-like snippets in `auto`.
- Tests must cover exact visible raw compatibility, exact strict redaction, broad visible config/log redaction, broad hidden redaction and no raw secret-like values in failed broad diagnostics.

### Error Codes

Define stable constants and use them consistently:

- `invalid_joiner`
- `invalid_placement`
- `invalid_backup_mode`
- `backup_creation_failed`
- `invalid_redaction_mode`
- `invalid_read_range`
- `invalid_continuation_proof`
- `too_many_items`
- `continuation_query_mismatch`
- `source_fingerprint_mismatch`
- `target_fingerprint_mismatch`
- `range_out_of_bounds`
- `hidden_excluded`
- `vcs_excluded`
- `ignored_by_glob`
- `glob_mismatch`
- `type_mismatch`
- `binary_excluded`
- `unreadable_path`
- `outside_cwd`
- `symlink_target_outside_cwd`
- `continuation_stale`
- `post_write_validation_failed`
- `vcs_content_traversal_unsupported`

Error-code field placement:

- `ReadFileOutput.ErrorCode` for failed single reads and invalid range recovery.
- `ReadFilesItem.ErrorCode` for per-item read failures; top-level `ReadFilesOutput.ErrorCode` only for failed whole-call validation.
- `ListDirOutput.ErrorCode`, `GlobFileSearchOutput.ErrorCode`, `GrepOutput.ErrorCode`, `InspectPathOutput.ErrorCode`, and `WorkspaceInventoryOutput.ErrorCode` for failed tool results and visibility/search recovery cases.
- Write tool top-level `error_code` remains only for failed tool results.
- `backup_results[].error_code` for requested backup creation failures; when backup failure prevents mutation, top-level `error_code=backup_creation_failed` mirrors the failed result.
- Applied writes with post-write validation problems return success plus `validation.error_code=post_write_validation_failed`; they do not set top-level `error_code`.

## Path/Cwd Projection Inventory

Add every new path-bearing output field to projection/schema tests:

- `diff_previews[]` file headers inside `text`
- `target_results[].diff_previews[]` file headers inside `text`
- `target_results[].boundary_preview.target_file`
- `target_results[].validation.target_read_back[].file`
- `source_diff_previews[]` file headers inside `text`
- `source_validation.source_read_back[].file`
- `validation.target_read_back[].file`
- `validation.source_read_back[].file`
- `validation.next_recommended_call.recommended_next_input.target_file`
- `validation.next_recommended_calls[].recommended_next_input.target_file`
- `target_results[].validation.next_recommended_call.recommended_next_input.target_file`
- `target_results[].validation.next_recommended_calls[].recommended_next_input.target_file`
- `source_validation.next_recommended_call.recommended_next_input.target_file`
- `source_validation.next_recommended_calls[].recommended_next_input.target_file`
- `boundary_preview.target_file`
- `backup_discovery.backup_paths[]`
- `backup_discovery.discovery_groups[].directory`
- `backup_discovery.discovery_groups[].backup_paths[]`
- `backup_discovery.discovery_groups[].next_recommended_call.recommended_next_input.target_directory`
- `backup_discovery.next_recommended_call.recommended_next_input.target_directory`
- `backup_discovery.next_recommended_calls[].recommended_next_input.target_directory`
- `continuation.next_recommended_call.recommended_next_input`
- `glob_file_search.continuation.last_sort_key.path`
- `glob_file_search.continuation.next_recommended_call.recommended_next_input.continuation_after.last_sort_key.path`
- `glob_file_search.continuation.next_recommended_calls[].recommended_next_input.continuation_after.last_sort_key.path`
- `workspace_inventory.continuation.last_sort_key.path`
- `workspace_inventory.continuation.next_recommended_call.recommended_next_input.continuation_after.last_sort_key.path`
- `workspace_inventory.continuation.next_recommended_calls[].recommended_next_input.continuation_after.last_sort_key.path`
- `outline_file.continuation.next_recommended_call.recommended_next_input.target_file`
- `outline_file.continuation.next_recommended_calls[].recommended_next_input.target_file`
- `read_files.items[].file`
- `read_files.continuation.next_recommended_call.recommended_next_input.items[].target_file`
- `read_files.continuation.next_recommended_calls[].recommended_next_input.items[].target_file`
- `workspace_inventory.directories_page[].path`
- `workspace_inventory.directories_page[].parent_path`
- `summary.package_hints[]`
- `summary.source_dir_hints[]`
- `summary.test_dir_hints[]`
- `summary.largest_directories[].path`
- `summary.backup_candidate_directories[].path`
- `summary.backup_discovery_hints[].recommended_next_input.target_directory`
- any extended `summary.*.path` fields
- `glob.groups[].directory`
- `inspect_path.visibility.target_path`

For unified diff `text`, path header rendering must use already-projected paths. Tests must prove no absolute path leak under `cwd_id`.

## Redaction Echo Inventory

Add every new non-path redaction echo field to schema/redaction tests:

- `diff_previews[].redaction_mode`
- `diff_previews[].redacted`
- `boundary_preview.redaction_mode`
- `boundary_preview.redacted`
- `validation.redaction_mode`
- `validation.target_read_back[].redaction_mode`
- `validation.target_read_back[].redacted`
- `validation.source_read_back[].redaction_mode`
- `validation.source_read_back[].redacted`
- `target_results[].diff_previews[].redaction_mode`
- `target_results[].diff_previews[].redacted`
- `target_results[].boundary_preview.redaction_mode`
- `target_results[].boundary_preview.redacted`
- `target_results[].validation.redaction_mode`
- `target_results[].validation.target_read_back[].redaction_mode`
- `target_results[].validation.target_read_back[].redacted`
- `target_results[].validation.source_read_back[].redaction_mode`
- `target_results[].validation.source_read_back[].redacted`
- `source_diff_previews[].redaction_mode`
- `source_diff_previews[].redacted`
- `source_validation.redaction_mode`
- `source_validation.target_read_back[].redaction_mode`
- `source_validation.target_read_back[].redacted`
- `source_validation.source_read_back[].redaction_mode`
- `source_validation.source_read_back[].redacted`
- `grep.matches[].redacted`
- `grep.matches[].redaction_mode`
- `read_files.items[].redacted`
- `read_files.items[].redaction_mode`

## Schema Constraints

Steps:

1. Add new DTO structs before handlers use them.
2. Extend input structs:
   - `include_hidden` for list/search/inventory/grep where applicable.
   - `include_vcs_metadata` for list/search/inventory only, default false.
   - `redaction_mode` for grep, `read_files`, and write preview/read-back surfaces.
   - `count_total_lines`, `chunk_lines` for `read_file`.
   - `sort`, `continuation_after` for `glob_file_search`.
   - `include_summary`, `summary_profile`, `continuation_after` for `workspace_inventory`.
   - `discovery_context` for `inspect_path`, with `glob_pattern` for file discovery and `grep_glob` only for grep-style content filter diagnostics.
3. Add `ReadFilesInput` / `ReadFilesOutput`.
4. Do not add `CleanupArtifactsInput` / `CleanupArtifactsOutput` in Phase 5.
5. Update schema enum constraints.
6. Update path field schema traversal for new path names.
7. Update cwd plumbing for every new or extended tool:
   - `read_files` implements `CwdAwareInput` and is accepted by server cwd decoding;
   - `ReadFilesOutput` and extended discovery/inventory/write outputs are handled by `AttachCwdOutputMeta`;
   - nested `ActionHint.recommended_next_input` maps include `cwd_id` when the original request used cwd mode;
   - path sanitizer/projection covers new output structs and nested hints without absolute leaks.
8. Do not add `include_vcs_metadata` to `grep`; add explicit runtime detection/rejection for that raw argument with `error_code=vcs_content_traversal_unsupported` so JSON unknown-field behavior cannot silently ignore a dangerous request.

## Checks

- Schema tests prove new fields are present and path fields reject empty strings where relevant.
- Structured error tests prove new arrays default to `[]`, not `null`.
- Schema tests prove discovery continuation input carries both `canonical_query_hash` and `last_sort_key`.
- Schema tests prove per-sort `last_sort_key` required fields and JSON numeric types.
- Schema/unit tests prove `proof_strength=exact` cannot be emitted without non-empty `sha256`, size and mtime; missing SHA downgrades to `stat_only`.
- Error-code tests prove every affected failed output and `read_files.items[]` can carry stable `error_code`.
- Backup error tests prove requested backup creation failures set `backup_results[].error_code=backup_creation_failed` and, when mutation is refused before apply, top-level `error_code=backup_creation_failed`.
- Validation tests prove applied validation failures use `validation.error_code`, not top-level `error_code`.
- Validation truncation tests prove `applied_validation_truncated` returns cwd-safe `read_file` hints through `validation.next_recommended_call` / `validation.next_recommended_calls[]`.
- Cwd tests prove new path-bearing fields use relative paths under `cwd_id`.
- Cwd tests prove `read_files`, nested recommended inputs, workspace inventory page entries and backup discovery hints receive `cwd_id` metadata and leak no absolute paths except `cwd`.
- Redaction helper unit tests cover common secret-like patterns without leaking raw values in failures.
- Redaction matrix tests prove raw secret-like values do not appear in broad hidden/config/log grep, `read_files`, diff previews, validation read-back, warnings or recovery hints, and prove redaction echo fields are set.
- Grep compatibility tests prove exact visible file `auto` output remains raw while broad config/log and broad hidden searches redact.

## Handoff / Next Stage

After Stage 1, write tools can consume shared DTOs without inventing field names.

## Stop And Ask If

- A new field needs a behavior incompatible with existing output semantics.
- Redaction cannot be implemented without either leaking raw values or hiding all useful evidence.
- Path projection for diff `text` cannot be tested reliably.
