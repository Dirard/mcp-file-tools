# Phase 5 Implementation Plan: Agent Safe Write, Read, And Discovery

plan_version_label: phase5-agent-safe-write-read-discovery-srs-v1
status: clean_reviewed_ready_for_implementation
concept_source:
- plans/phase5-agent-safe-write-read-discovery/01_human_concept.md
- plans/phase5-agent-safe-write-read-discovery/02_technical_concept.md

## Goal

Upgrade existing file-tools so an agent can safely complete this workflow without raw shell:

1. Discover visible or hidden files intentionally.
2. Explain why a file/result is absent.
3. Read bounded or full context with coverage proof.
4. Preview write changes as bounded unified diffs.
5. Understand `joiner` and newline boundaries before mutation.
6. Apply range writes with existing fingerprint safety.
7. Receive bounded post-write validation/read-back evidence.
8. Rediscover backups and recover from partial-state outputs.

## Scope

Affected tools:

- `read_file`
- new `read_files` batch read helper
- `copy_ranges`
- `move_ranges`
- `copy_ranges_batch`
- `move_ranges_batch`
- `list_dir`
- `glob_file_search`
- `grep`
- `inspect_path`
- `workspace_inventory`
- `outline_file` existing continuation replay verification and shared hint alignment only

Affected shared areas:

- DTOs and JSON schemas
- path/cwd projection
- file traversal
- range transfer planning
- redaction helpers
- docs and server metadata
- tests

## Out Of Scope

- No language-aware outline changes; Phase 6 owns that.
- No symbol selectors or symbol-aware writes; Phase 6 owns that.
- No LSP, semantic index, embeddings, type checking, cross-file rename, or AST rewrite.
- No automatic backup restore.
- No hidden traversal by default.
- No broad `.git`/VCS traversal by default.
- No stateful server cursor or chat/session-bound continuation.
- No raw secret display in broad hidden/config/log discovery flows.
- No full PDF/image/document extraction.
- No cleanup/delete helper in Phase 5. Backup cleanup is future-gated until tool-created provenance can be verified by more than filename shape.

## Must Preserve

- Existing tool names and current required inputs keep working.
- Existing output fields keep their old meaning.
- `set_cwd` remains the only way to obtain `cwd_id`.
- Without `cwd_id`, path inputs remain absolute-only and output paths are slash-normalized absolute paths.
- With `cwd_id`, path inputs remain cwd-relative and all path outputs are cwd-relative except absolute `cwd`.
- `dry_run=true` remains non-mutating and creates no backups.
- Write tools remain explicit-source/target, fingerprint-gated, limit-bounded, symlink-safe, and partial-state aware.
- Phase 4 `grep` fields remain intact.
- Phase 4 visible broad `grep` raw-text behavior is preserved except for a narrow Phase 5 safety exception: `redaction_mode=auto` redacts secret-like values in broad config/log-like content matches.
- Structured errors remain JSON-only; plain MCP text content remains empty.
- Large-file thresholds and concurrency limiters remain respected.

## Concept Transferred Into SRS

User-visible result:

- Agents see exact planned changes before writing.
- Agents can intentionally include hidden files and rediscover dot-sidecar backups.
- Agents can prove read coverage and continue large reads/results.
- Agents get clearer recovery-oriented errors.

Behavior / contracts:

- Additive optional fields and helper tools.
- Stateless continuation only.
- Hidden traversal explicit; VCS metadata separately protected.
- Secret redaction applies to newly broadened risky content surfaces.
- Diff preview and read-back are bounded, truncation-marked, and advisory evidence; fingerprints remain the mutation contract.

Acceptance:

- The full safe write/read/discovery workflow works in tests.
- Cwd projection is proven for every new path-bearing field.
- Old default behavior is unchanged.

## Plan File Map

- `00-index.md`: global goal, scope, decisions, acceptance, and checks.
- `01-contracts-and-shared-models.md`: DTOs, schemas, path/cwd, redaction, continuation, error-code cross-cutting contracts.
- `02-write-diff-boundary-validation.md`: range write preview, joiner model, validation/read-back, backup hints.
- `03-hidden-discovery-and-visibility.md`: `include_hidden`, backup discovery, explain missing/ignored, VCS guardrails.
- `04-read-continuation-and-batch.md`: `read_file` coverage, chunk continuation, `read_files`.
- `05-inventory-glob-and-cleanup.md`: workspace summary, glob sort/continuation, backup rediscovery and cleanup future gate.
- `06-docs-tests-review.md`: tests, docs, schemas, MCP metadata, review handoff.

## Global Decisions

1. New public fields are additive unless an existing bug is explicitly fixed.
2. `include_hidden` is the primary hidden opt-in field, default `false`.
3. `include_vcs_metadata` is separate, default `false`; `grep` rejects broad VCS traversal even if this field is present.
4. Dot-sidecar backups keep the current naming pattern `.<base>.<timestamp>.<hash>.<attempt>.bak`.
5. `redaction_mode` values are `auto` and `strict`; no raw opt-out is added for risky new broad outputs in Phase 5.
   - Safety exception: broad visible config/log-like `grep` content is treated as risky in `auto`; exact visible file grep remains raw unless `strict` is requested.
6. `dry_run=true` write outputs include bounded diff preview by default.
7. Applied write outputs include bounded validation/read-back by default, with truncation and failure status.
8. `blank_line` means one visually blank line between adjacent non-empty blocks.
9. Continuation is stateless and replayable via explicit next input, not `nextCursor` server state.
10. `read_files` is added as a batch read helper because parallel calls are possible but token/noise cost is still high for common agent workflows.
11. `inspect_path` receives optional discovery diagnostics instead of adding a separate visibility tool.
12. `cleanup_artifacts` is deferred from Phase 5. Filename shape alone cannot prove a backup is tool-created, so Phase 5 implements backup rediscovery but no deletion helper.
13. Binary preview stays metadata-only, with better MIME/extension hints where cheap.
14. Stateless continuation is exact only when the selected filesystem result set is unchanged between calls; changed trees are best-effort and must be labeled with stale/change warnings, not overclaimed as complete.
15. Phase 5 verifies existing `outline_file` truncation hints and upgrades only their shared stateless continuation metadata where needed: replay input shape, stale/change honesty, cwd-safe recommended input and schema docs. It does not add language-aware outline continuation; Phase 6 owns language behavior.
16. Windows ergonomics are concrete acceptance requirements: slash-normalized drive paths, spaces/Cyrillic, CRLF, drive roots, symlink/junction and outside-cwd cases must be tested or explicitly platform-skipped.

## Global Acceptance

Implementation is accepted only when:

- Existing tests pass.
- Existing default outputs do not lose fields or change path mode.
- Every write tool returns `diff_previews`, `joiner_effect`, `boundary_preview`, and `validation` in the defined cases.
- Diff preview is present in dry-run for create, append, prepend, insert, replace, move source removal, copy batch, and move batch source rewrite.
- Batch target write safety fields live in `target_results[]`; move batch source rewrite/removal lives only in `source_diff_previews[]` and `source_validation`.
- `blank_line` produces one visually blank line between adjacent non-empty blocks.
- Backups created by write tools expose `backup_discovery` and can be rediscovered through hidden-aware `list_dir` and `glob_file_search` with `glob_pattern`.
- Hidden discovery remains off by default.
- `.git`/VCS metadata remains excluded from broad content search.
- `inspect_path` can explain hidden, ignored, glob mismatch, binary, unreadable, missing, outside-cwd, symlink-outside-cwd and VCS reasons where applicable.
- `read_file` can return coverage proof and opt-in total line count for bounded ranges.
- `read_files` handles mixed success/errors without hiding successful reads.
- `glob_file_search` supports stable sort modes and stateless continuation; consistency remains `unknown` unless a future tree proof is added.
- `workspace_inventory` returns bounded summary metadata plus an explicit flat `directories_page[]` continuation surface; `root` remains an overview tree, not the canonical merge contract for later pages.
- `outline_file` existing continuation hints are tested for stateless replay, stale/change honesty and cwd-safe recommended inputs; Phase 5 does not add language-aware outline semantics.
- Redaction prevents raw secret-like values in broad hidden/config/log search, diff, read-back and diagnostic snippets.
- Specific `error_code` values replace generic errors for common recovery cases.
- No Phase 5 tool can delete rediscovered backup files; backup cleanup remains a future feature requiring verifiable provenance/manifest.
- Windows paths with drive letters, spaces/Cyrillic names, CRLF files, drive roots, symlinks/junctions and outside-cwd diagnostics are covered by tests or explicit platform skip notes.
- All new path-bearing fields are cwd-safe.

## Global Checks

Expected verification:

- PowerShell: `$env:GOPROXY='off'; go test -count=1 ./filetoolsserver/handler ./filetoolsserver -run "Read|Write|Copy|Move|Hidden|Glob|Inventory|Schema|Cwd|Redact|Backup"`
- PowerShell: `$env:GOPROXY='off'; go test -count=1 ./...`
- PowerShell: `$env:GOPROXY='off'; go test -race -count=1 ./filetoolsserver/handler -run "Read|Write|Copy|Move|Hidden|Glob|Inventory|Cwd"`
- Manual schema/docs check for `README.md`, `TOOLS.md`, `server.json`, and server tool descriptions.
- Manual MCP smoke check after rebuild for at least `read_file`, `copy_ranges` dry-run, hidden `glob_file_search`, and `workspace_inventory`.
- Windows-focused tests/smoke checks for slash-normalized `D:/...` output, path spaces/Cyrillic, CRLF preservation, drive-root handling, and symlink/junction diagnostics.

## Stop And Ask If

- A required feature would expose raw secret-like values by default.
- Diff preview or read-back requires returning content above configured thresholds.
- Hidden traversal would need to include VCS metadata for broad content search.
- Continuation cannot be made stateless and replayable.
- Product scope changes to require backup deletion/cleanup in Phase 5.
- Any new path-bearing field cannot be projected safely under `cwd_id`.
