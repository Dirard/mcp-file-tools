# Stage 6: Docs, Tests, Review, And Rollout

## Goal

Document the Phase 5 agent workflow, update schemas/metadata, verify old and new behavior, and prepare clean review handoff.

## Depends On

- Stages 1-5 implemented or explicitly deferred with plan-approved reason.

## Touched Areas

- `README.md`
- `TOOLS.md`
- `server.json`
- `filetoolsserver/server.go`
- `filetoolsserver/server_test.go`
- `filetoolsserver/handler/*_test.go`

## Documentation Steps

1. Update tool list if `read_files` is added.
2. Add short "Agent Safe Write Loop" section:
   - discover;
   - read with coverage;
   - dry-run diff;
   - verify boundary;
   - apply;
   - read-back;
   - rediscover backup.
3. Update `copy_ranges*` / `move_ranges*` docs with:
   - diff preview fields;
   - joiner semantics;
   - validation;
   - structured `backup_discovery` hints.
4. Update `list_dir`, `glob_file_search`, `grep`, `workspace_inventory` docs with hidden policy.
5. Update `inspect_path` docs with visibility diagnostics.
6. Update `read_file` docs with coverage and continuation.
7. Add `read_files` docs if implemented.
8. Update `outline_file` docs only where Phase 5 verifies or improves existing stateless continuation and cwd-safe next calls: no language behavior change, only replay input shape, stale/change honesty and projected recommended input.
9. Document that Phase 5 supports backup rediscovery but does not expose backup cleanup/deletion.
10. Update environment variables.
11. Update `server.json` descriptions.
12. Update server tool descriptions in `server.go`.

## Test Matrix

Required test groups:

- Existing regression:
  - old inputs still work;
  - old path modes still work;
  - old grep Phase 4 behavior still works.
- Write safety:
  - diff preview all placements;
  - move source diff;
  - batch diffs;
  - diff truncation;
  - joiner boundary;
  - post-write validation;
  - `applied_validation_truncated` returns cwd-safe `read_file` follow-up hints;
  - requested backup creation failure returns stable `backup_results[].error_code`.
- Discovery:
  - hidden default off;
  - hidden on;
  - VCS protected;
  - structured backup rediscovery hints;
  - explain visibility.
- Read:
  - coverage fields;
  - opt-in total lines;
  - chunk continuation;
  - stale continuation;
  - exact proof requires non-empty full-file sha plus full-file size/mtime; stat-only proof does not overclaim stale detection;
  - batch read mixed results;
  - `read_files` auto redaction for hidden/config/log-like paths while exact `read_file` remains raw;
  - `outline_file` continuation replay with projected paths, stale/change honesty and no cwd leaks;
  - nested `outline_file` continuation uses `next_omitted_line` / flattened item ordering, not parent `last_included_line`, and proves no skipped child symbols or dishonest duplicates.
- Inventory/glob:
  - sort modes;
  - continuation;
  - structured `continuation_after` with query hash/last sort key;
  - typed per-sort `last_sort_key` schema;
  - continuation query mismatch error;
  - changed-tree or unknown continuation warnings for added/removed/renamed/modified entries;
  - summary;
  - summary path projection for package/source/test/largest-directory/backup-candidate hints;
  - workspace inventory backup summary and `glob_file_search(include_hidden=true)` hints;
  - workspace inventory `directories_page[]` continuation merge contract;
  - cwd no leaks.
- Redaction:
  - grep broad hidden;
  - grep broad visible config/log-like;
  - exact visible file grep compatibility remains raw in `auto`;
  - grep/read_files `redacted` and `redaction_mode` echo fields;
  - diff preview;
  - boundary preview, read-back and validation snippets with echoed effective redaction mode;
  - batch read;
  - diagnostics.
- Backup discovery:
  - `backup_discovery` output placement for single and batch writes;
  - multi-directory batch `backup_discovery.discovery_groups[]` and `next_recommended_calls[]`;
  - sidecar backup rediscovery through hidden-aware list/glob;
  - workspace inventory backup candidate summary and hints;
  - no `cleanup_artifacts` schema/server registration in Phase 5;
  - docs do not instruct agents to delete backups through file-tools.
- Windows/cwd:
  - slash-normalized `D:/...` output;
  - paths with spaces and Cyrillic segments;
  - CRLF files keep correct line counts/ranges;
  - drive root boundaries;
  - symlink/junction metadata and outside-cwd target protection.
- Schema/cwd plumbing:
  - new `read_files` tool accepts `cwd_id`;
  - new and extended outputs receive `cwd` metadata;
  - nested `ActionHint.recommended_next_input` maps include `cwd_id` in cwd mode;
  - no absolute paths leak in new path-bearing fields except `cwd`.

## Verification Commands

Run after implementation:

- PowerShell: `$env:GOPROXY='off'; go test -count=1 ./filetoolsserver/handler ./filetoolsserver -run "Read|Write|Copy|Move|Hidden|Glob|Inventory|Schema|Cwd|Redact|Backup"`
- PowerShell: `$env:GOPROXY='off'; go test -count=1 ./...`
- PowerShell: `$env:GOPROXY='off'; go test -race -count=1 ./filetoolsserver/handler -run "Read|Write|Copy|Move|Hidden|Glob|Inventory|Cwd"`

If dependency changes occur:

- PowerShell: `$env:GOPROXY='off'; go list -m all`
- build native Windows binary:
  - `go build -trimpath -buildvcs=false -o .\mcp-file-tools.exe .\cmd\mcp-file-tools`

## Review Handoff

Product owner review should check:

- safe write workflow is truly agent-useful;
- hidden/backup discovery preserves safety;
- cleanup/deletion is not accidentally exposed in Phase 5;
- read continuation proves coverage;
- continuation docs do not overclaim completeness on changed files/trees;
- Windows ergonomics are concrete enough for agents to use relative paths confidently;
- docs do not overclaim.

Engineering reviewer should check:

- DTO consistency;
- path/cwd projection inventory;
- redaction safety;
- diff/validation correctness;
- continuation determinism;
- no accidental cleanup tool/schema registration;
- exact refusal/error codes for invalid joiners, placements, read ranges, item limits and redaction modes;
- test coverage and maintainability.

## Stop And Ask If

- Docs would need to tell agents to use raw shell for a core Phase 5 workflow.
- A required test cannot be written without weakening safety.
- Review finds a concept-level change, such as raw secret output or backup cleanup/deletion in Phase 5.
