# Stage 5: Inventory, Glob Sort/Continuation, And Backup Rediscovery

## Goal

Improve project discovery beyond directory-only maps and newest-first globs, keep backups rediscoverable, and explicitly defer deletion/cleanup until tool-created provenance can be verified.

## Depends On

- Stage 1 continuation DTOs.
- Stage 3 hidden/discovery policy.
- Stage 4 continuation conventions.

## Touched Areas

- `filetoolsserver/handler/glob_file_search.go`
- `filetoolsserver/handler/workspace_inventory.go`
- `filetoolsserver/handler/inspect_path.go`
- `filetoolsserver/handler/atomic_write.go`
- `filetoolsserver/server.go`
- tests

## `glob_file_search` Additions

Input fields:

- `sort`: `modified_desc`, `modified_asc`, `path_asc`, `path_desc`, `size_desc`, `size_asc`, `directory_path_asc`; default `modified_desc`.
- `continuation_after`: structured object with `canonical_query_hash` and `last_sort_key`.
- `include_hidden`, `include_vcs_metadata` from Stage 3.

Output fields:

- `sort`;
- `continuation`;
- `search_stats` with hidden/VCS/ignored counters;
- optional `groups[]` for `directory_path_asc`.

Rules:

- Existing newest-first behavior remains default.
- Tie-break by slash-normalized path.
- For `modified_*`, `last_sort_key` includes `modified_unix_nano` and `path`.
- For `path_*`, `last_sort_key` includes `path`.
- For `size_*`, files include `size_bytes`; `last_sort_key` includes `size_bytes` and `path`.
- Continuation replays all query inputs.
- Continuation is exact only for an unchanged result set. The continuation token carries a cheap query fingerprint and last sort key, not server state.
- `continuation.canonical_query_hash` is computed from normalized target, cwd/path mode, `glob_pattern`, ignore globs, hidden/VCS policy, sort and limit.
- `continuation.last_sort_key` is the exact sort key for the last returned file.
- `next_recommended_call.recommended_next_input.continuation_after` contains both fields.
- Under `cwd_id`, `continuation.last_sort_key.path`, `continuation_after.last_sort_key.path`, and those same nested fields inside `next_recommended_call.recommended_next_input` / `next_recommended_calls[].recommended_next_input` are cwd-relative and leak no absolute paths except `cwd`.
- If `continuation_after.canonical_query_hash` does not match the current normalized query hash, return `error_code=continuation_query_mismatch` instead of continuing silently.
- If files are added, removed, renamed or modified between pages, Phase 5 does not prove tree stability; output remains best-effort with `continuation.consistency="unknown"` and wording that duplicates or skips are possible.
- If change cannot be proven or ruled out, use `continuation.consistency="unknown"` and the same duplicate/skip warning.
- The tool must never claim `complete=true` unless it reached the end of the walk under the current query.

## `workspace_inventory` Additions

Input fields:

- `include_hidden`;
- `include_vcs_metadata`;
- `include_summary` default `true`;
- `summary_profile`: `compact` default, `none`, `extended`;
- `continuation_after`: structured object with `canonical_query_hash` and `last_sort_key.path`.

Output fields:

- `summary`;
- `continuation`;
- `directories_page[]` as the canonical flat page for continuation/merge;
- hidden/VCS counters;
- retained `root` directory tree.

Compact summary includes:

- `file_type_counts`;
- `package_hints`;
- `source_dir_hints`;
- `test_dir_hints`;
- `largest_directories` by direct file count and optionally cheap size;
- `backup_candidate_directories[]` with directory paths and cheap counts for sidecar/dot backup filename patterns when hidden entries are included or skipped counts indicate likely hidden backups;
- `backup_discovery_hints[]` as `ActionHint` values recommending `glob_file_search(include_hidden=true, glob_pattern=".*.bak")` for candidate directories;
- `hidden_entries_skipped`;
- `ignored_entries_skipped`.

Rules:

- Inventory remains bounded and not an index.
- Directory walk order is deterministic slash path order.
- `directories_page[]` contains the directories returned for the current page in deterministic path order. Each row includes `path`, `parent_path`, `depth`, direct counts and optional `read_error`.
- Page 1 returns both `root` and `directories_page[]`. Continuation calls may return a partial `root` for preview, but only `directories_page[]` is the exact merge/coverage contract.
- For unchanged trees, collecting `directories_page[]` across continuation calls must cover each returned directory exactly once. Repeated ancestors in `root` do not count as duplicates because `root` is not the merge surface.
- Summary respects the same hidden/ignore policies as the tree.
- Summary paths are projected.
- Backup summary is discovery-only: it reports candidate directories/counts and glob hints, never delete actions and never file contents.
- If summary is incomplete because of limit/depth, `summary.complete=false`.
- `continuation_after` is stateless. It is exact only while the directory tree relevant to the query is unchanged.
- `continuation.canonical_query_hash` is computed from normalized target, cwd/path mode, ignore globs, hidden/VCS policy, summary profile, max depth and limit.
- `continuation.last_sort_key` is `path`.
- `next_recommended_call.recommended_next_input.continuation_after` contains both fields.
- Under `cwd_id`, `directories_page[].path`, `directories_page[].parent_path`, `continuation.last_sort_key.path`, `continuation_after.last_sort_key.path`, and those same nested fields inside recommended continuation inputs are cwd-relative and leak no absolute paths except `cwd`.
- If `continuation_after.canonical_query_hash` does not match the current normalized query hash, return `error_code=continuation_query_mismatch`.
- If a previously returned directory disappears or ordering inputs change before continuation, Phase 5 does not prove tree stability; return `continuation.consistency="unknown"` and explicit wording instead of overclaiming full coverage.
- If the tool cannot prove unchanged vs changed, return `continuation.consistency="unknown"` and warn that duplicate/skip is possible.

## Binary Metadata

Extend `inspect_path` only with cheap metadata:

- `mime_hint` from extension and simple magic bytes;
- `binary_preview_available=false` for unsupported extraction;
- no thumbnails/text extraction in Phase 5.

## Backup Cleanup Deferred

Do not add `cleanup_artifacts` in Phase 5.

Reason:

- current sidecar backup filenames are useful for rediscovery, but filename shape alone does not prove tool-created provenance;
- a user can create a file matching the same shape;
- a delete-capable helper without stronger provenance can become a disguised `rm`.

Phase 5 delivers:

- hidden-aware `list_dir` / `glob_file_search` backup rediscovery;
- `inspect_path` visibility diagnostics for backups;
- write-tool outputs that keep explicit `backup_paths` and rediscovery hints.

Future gate for backup cleanup:

- backup manifest/provenance is created together with the backup;
- provenance survives enough for safe discovery and deletion;
- dry-run confirmation hash covers provenance, candidate metadata, cwd/root, target directory and hidden/VCS policy;
- delete still refuses directories, symlinks, VCS metadata and outside-cwd paths.

## Acceptance

- Glob default output remains newest-first.
- Sort modes work with stable tie-breaks.
- Glob continuation can retrieve all results across pages without state when the result set is unchanged, and honestly reports `unknown` when unchanged coverage cannot be proven.
- Workspace summary helps identify source/test/package structure and is honest when incomplete.
- Workspace summary helps rediscover hidden sidecar backups through projected `backup_candidate_directories[]` and `backup_discovery_hints[]` when candidate evidence is available.
- Inventory paths and continuation are cwd-safe.
- Binary metadata improves without content extraction.
- Backup files can be rediscovered intentionally, but no Phase 5 cleanup/delete helper exists.
- Docs and schemas do not advertise `cleanup_artifacts` as an available Phase 5 tool.

## Checks

- Glob sort tests for all sort modes.
- Glob continuation tests for modified/path/size sorts.
- Glob continuation tests for structured `continuation_after`, query hash replay/mismatch, last sort key, and `unknown` consistency when tree stability is not proven.
- Hidden + glob continuation tests.
- Workspace summary tests for Go repo fixture, hidden entries, ignored dirs, max depth, limit.
- Workspace inventory continuation tests for structured `continuation_after`, query hash replay/mismatch, and `unknown` consistency when tree stability is not proven.
- Workspace inventory continuation tests prove `directories_page[]` has projected `path`/`parent_path`, deterministic order, no duplicates across pages in an unchanged tree, and honest `unknown` consistency because changed-tree detection is not proven.
- Cwd no-leak tests for `glob_file_search` and `workspace_inventory` continuation sort keys, including nested `continuation_after.last_sort_key.path` inside recommended inputs.
- Workspace inventory backup summary tests prove hidden sidecar backup candidate directories and `glob_file_search(include_hidden=true)` hints are cwd-safe and do not expose cleanup/delete.
- Backup rediscovery tests for dot-sidecar backups using `include_hidden=true`.
- Schema/server metadata tests prove no `cleanup_artifacts` Phase 5 tool is registered.
- Cwd no-leak tests.

## Handoff / Next Stage

After Stage 5, all user feedback outside language/symbol support has an SRS path.

## Stop And Ask If

- Product scope changes to require deleting/cleaning backups in Phase 5.
- Workspace summary requires full indexing of huge workspaces.
- Glob continuation cannot stay deterministic under selected sort.
