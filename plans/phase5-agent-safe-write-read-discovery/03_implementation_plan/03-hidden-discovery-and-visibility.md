# Stage 3: Hidden Discovery, Backup Discovery, And Visibility Diagnostics

## Goal

Let agents intentionally include hidden files, rediscover dot-sidecar backups, and explain missing/ignored results without broad shell searches.

## Depends On

- Stage 1 path/redaction/error-code contracts.
- Stage 2 backup rediscovery hints.

## Touched Areas

- `filetoolsserver/handler/file_scan.go`
- `filetoolsserver/handler/list_dir.go`
- `filetoolsserver/handler/glob_file_search.go`
- `filetoolsserver/handler/grep_tool.go`
- `filetoolsserver/handler/inspect_path.go`
- `filetoolsserver/handler/workspace_inventory.go`
- `filetoolsserver/handler/tool_types.go`
- tests in `agent_tools_test.go`, `handler_test.go`

## Steps

1. Extend file-walk stats:
   - `HiddenEntriesIncluded`
   - `VCSMetadataSkipped`
   - `VCSMetadataIncluded` for non-content discovery only
   - keep existing `DotEntriesSkipped`.
2. Add `include_hidden` input to:
   - `list_dir`;
   - `glob_file_search`;
   - `workspace_inventory`;
   - `grep`.
3. Add `include_vcs_metadata` input to:
   - `list_dir`;
   - `glob_file_search`;
   - `workspace_inventory`.
4. Do not add `include_vcs_metadata` to `grep` schema. Because Go JSON unmarshalling can ignore unknown fields, `grep` must explicitly detect a raw `include_vcs_metadata` argument and reject it with `error_code=vcs_content_traversal_unsupported`; broad VCS content traversal remains out of scope.
5. For `include_hidden=false`, preserve current hidden skip behavior exactly.
6. Truth table for non-content discovery tools:
   - `include_hidden=false`, `include_vcs_metadata=false`: skip dotfiles/dotdirs and VCS metadata.
   - `include_hidden=true`, `include_vcs_metadata=false`: include dotfiles/dotdirs except VCS metadata.
   - `include_hidden=false`, `include_vcs_metadata=true`: show VCS metadata only when it is a direct child/candidate of the requested directory or exact requested path; keep other dotfiles hidden.
   - `include_hidden=true`, `include_vcs_metadata=true`: include dotfiles/dotdirs and VCS metadata, but still treat VCS traversal as metadata-only.
7. Per-tool VCS traversal semantics:
   - `list_dir`: may list `.git`/VCS entries only as direct children when `include_vcs_metadata=true`; it never recurses.
   - `glob_file_search`: may match VCS metadata paths only when `include_vcs_metadata=true`, but must not recursively walk inside `.git/objects`, `.git/logs`, or equivalent high-volume internals; skipped internals increment `vcs_entries_skipped`.
   - `workspace_inventory`: may include VCS directories as directory nodes only when `include_vcs_metadata=true`, capped by normal `limit`/`max_depth`; it must not inspect file contents and must count skipped high-volume VCS internals.
   - `inspect_path`: exact VCS paths can be inspected as metadata with `include_vcs_metadata` in `discovery_context`; it does not read content beyond existing cheap metadata.
   - `grep`: never accepts VCS metadata traversal.
   - No non-content tool reads file contents because of `include_vcs_metadata`; all VCS support is names, kinds, counts and cheap metadata only.
8. Ensure exact hidden file path behavior remains allowed for current tools.
9. Add output echo fields:
   - `include_hidden`;
   - `include_vcs_metadata` where accepted;
   - `hidden_entries_included`;
   - `vcs_entries_skipped`.
10. Update `list_dir`:
   - direct dot-backup files appear with `include_hidden=true`;
   - `.git` appears only with `include_vcs_metadata=true`;
   - count reflects returned entries only.
11. Update `glob_file_search`:
    - with `include_hidden=true`, canonical sidecar backup pattern can find `.*.bak`;
    - hidden and VCS counts are clear;
    - sort/continuation from Stage 5 uses hidden policy in replay input.
12. Update `workspace_inventory`:
    - hidden dirs/files counted or included according to policy;
    - hidden summary appears in `summary`.
13. Update `grep`:
    - broad hidden traversal requires `include_hidden=true`;
    - when broad hidden traversal is active, `redaction_mode=auto` redacts secret-like text;
    - when broad directory traversal touches visible config/log-like files, `redaction_mode=auto` also redacts secret-like text;
    - exact hidden file path without `include_hidden` keeps current behavior, but redaction can be requested.
14. Extend `inspect_path` with optional `discovery_context`:
    - `target_directory`;
    - `glob_pattern` for file-discovery glob context;
    - `grep_glob` for optional grep content-filter context if needed;
    - `type`;
    - `ignore_globs`;
    - `include_hidden`;
    - `include_vcs_metadata`.
15. Add `visibility` output:
    - `target_path`;
    - `exists`;
    - `would_list_dir_show`;
    - `would_glob_match`;
    - `would_grep_traverse`;
    - `reasons[]` with codes.
16. Visibility diagnostics must not read file content, except cheap binary metadata already used by `inspect_path`.
17. Cwd mode:
    - `visibility.target_path` is cwd-relative;
    - outside-cwd paths return `outside_cwd` without absolute leak beyond allowed `cwd`.

## Acceptance

- Hidden files remain hidden by default.
- `include_hidden=true` shows `.env.example`, `.github`, `.codex`, and dot-backups where applicable.
- VCS metadata is not included by `include_hidden` alone.
- VCS truth table is tested for list, glob and inventory.
- Recursive VCS internals are capped/skipped for glob and inventory; no tool reads VCS file contents as part of VCS metadata discovery.
- `grep(include_hidden=true)` redacts secret-like text in broad hidden/config/log matches.
- Broad visible `grep` over config/log-like files redacts secret-like text in `auto`.
- Backup paths from write outputs can be rediscovered with `glob_file_search(include_hidden=true, glob_pattern=".*.bak")`.
- `glob_pattern=".*.bak"` is the canonical public glob for dot-sidecar backup rediscovery in Phase 5 and must match the fixed sidecar shape `.<base>.<timestamp>.<hash>.<attempt>.bak`; tests must prove this against the actual glob matcher.
- `inspect_path` can explain absent hidden, ignored, glob mismatch, binary, unreadable, outside-cwd, symlink-outside-cwd, VCS and missing cases.

## Checks

- Fixture tree with visible files, dotfiles, `.github`, `.codex`, `.git`, dot-backups, ignored dirs, binary file, symlink outside cwd.
- Tests for default hidden skip on list/glob/inventory/grep.
- Tests for hidden inclusion without VCS.
- Tests for VCS inclusion only on non-content tools, including direct `.git` listing, recursive skip/counter behavior and no-content guarantees.
- Tests that `grep(include_vcs_metadata=...)` is rejected explicitly with `vcs_content_traversal_unsupported`, not silently ignored.
- Tests for grep broad hidden redaction.
- Tests for grep broad visible config/log-like redaction and exact visible file compatibility.
- Cwd no-leak tests for diagnostics and hints.

## Handoff / Next Stage

After Stage 3, discovery tools can support read continuation and workspace summaries without hidden ambiguity.

## Stop And Ask If

- A useful backup discovery path requires broad VCS traversal.
- Visibility diagnostics would need raw content to explain a result.
- Hidden inclusion would change default outputs.
