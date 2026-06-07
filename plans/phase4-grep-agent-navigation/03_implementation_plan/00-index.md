# Phase 4 Implementation Plan: Grep Agent Navigation

plan_version_label: phase4-grep-agent-navigation-srs-v12
status: implemented_clean_reviewed
concept_source:
- plans/phase4-grep-agent-navigation/01_human_concept.md
- plans/phase4-grep-agent-navigation/02_technical_concept.md

## Goal

Upgrade `grep` from structured ripgrep-like output into an agent navigation tool that is more useful than raw `rg` for coding agents.

The final product must make the following true:

- Existing `grep` calls and output modes keep working.
- `pattern_mode="literal"` lets agents search exact text without regex escaping mistakes.
- `search_stats` tells agents whether the result is complete and why it stopped.
- `file_groups[]` gives compact per-file navigation with ready `read_ranges`.
- `next_recommended_call` gives one safe, replayable next step when a useful one is obvious.
- `max_matches_per_file` prevents one noisy file from consuming the whole content result.
- `line_window` lets agents rerun `grep` inside one known file range.
- Cwd-aware paths and recommended inputs obey Phase 3 path projection and never leak absolute paths except `cwd`.
- Hidden/dotfile, large-file streaming, multiline guardrails, and current `content` / `files_with_matches` / `count` semantics are preserved.

## Scope

Affected areas:

- `filetoolsserver/handler/tool_types.go`
- `filetoolsserver/handler/grep_tool.go`
- `filetoolsserver/handler/grep_rows.go`
- `filetoolsserver/handler/file_scan.go`
- `filetoolsserver/handler/cwd_path.go`
- `filetoolsserver/handler/middleware.go`
- `filetoolsserver/handler/schema_constraints.go`
- `filetoolsserver/server.go`
- `filetoolsserver/handler/agent_tools_test.go`
- `filetoolsserver/server_test.go` if MCP-level cwd replay tests are useful
- `README.md`
- `TOOLS.md`
- `server.json`

## Out Of Scope

- No full `rg` flag parity.
- No PCRE2 support.
- No `.gitignore` support.
- No default ignore changes for `node_modules`, `vendor`, `dist`, `build`.
- No semantic search, embeddings, LSP, AST parsing, or exact definition/usage classification inside `grep`.
- No project index/cache.
- No write/replace behavior.
- No broad dotfile traversal default change.
- No secret redaction.
- No cursor, `nextCursor`, stateful pagination, or multi-call server state.

## Global Decisions

1. `pattern_mode` values are `regex`, `literal`, and empty as alias of `regex`.
2. Regex remains default for compatibility.
3. Literal mode uses Go regexp quoting and still honors `case_insensitive` and `multiline`.
4. Successful outputs always echo normalized `pattern_mode`: `regex` or `literal`; omitted input is returned as `regex`.
5. `line_window` is valid only when `path` resolves to a file.
6. `max_matches_per_file` is valid only for `output_mode=content`; other modes reject it with a structured error.
7. `search_stats` is always present on successful grep outputs, including no-match outputs, and omitted on validation/path/regex/tool-error outputs.
8. `file_groups` is additive, always marshalled as an array for schema stability, and populated only for `output_mode=content`; `files_with_matches` and `count` stay lightweight and use their existing arrays.
9. Existing `matches`, `files`, and `counts` remain the primary mode-specific evidence fields.
10. `next_recommended_call` is optional and top-level; at most one recommendation is returned.
11. `grep` may recommend only read-only tools: `read_file`, `grep`, or `outline_file`.
12. `grep` does not parse AST; if structure is useful it recommends `outline_file`.
13. `limit` remains the global output row/file/count cap.
14. `match_count`, `row_count`, and `search_stats.files_with_matches` remain mode-specific counts of returned output evidence. They do not count discovered-but-not-returned evidence at a global limit or per-file cap boundary; successful-output `search_stats.counts_are_complete` explains whether returned evidence covers the full selected scope.
15. Per-file caps make stats incomplete only when additional match evidence is actually suppressed after `max_matches_per_file`; a file with exactly the cap and no suppressed later match remains complete. Suppressed evidence sets `output.truncated=true`, `search_stats.completed=false`, `search_stats.stop_reason="file_cap"`, `search_stats.counts_are_complete=false`, and `file_groups[].capped=true` for capped content groups unless a global `limit` stop happens first, in which case `stop_reason="limit"`.
16. Cwd-aware output projection applies to all new path-bearing fields and recommended inputs.
17. When `line_window` is used, successful and no-match outputs echo `line_window` so the result cannot be mistaken for a full-file search.
18. No new default ignore behavior: `.gitignore` is not applied, exact dot-prefix file paths remain searchable, and non-dot directories such as `node_modules`, `vendor`, `dist`, and `build` are searched unless explicit `ignore_globs` exclude them.
19. In successful Phase 4 grep outputs, `truncated` is a legacy coarse incompleteness flag. It is `true` when returned evidence is incomplete because of global `limit`, per-file cap suppression, or unreadable selected files; `search_stats.stop_reason` is the precise reason.

## Plan File Map

- `00-index.md`: global goal, scope, decisions, acceptance, and checks.
- `01-contracts-and-schemas.md`: input/output DTOs, schema fields, docs-facing contracts.
- `02-pattern-window-and-filtering.md`: literal mode, line window, max matches per file, validation.
- `03-search-stats-and-groups.md`: traversal stats, file groups, read range construction, counting semantics.
- `04-next-call-and-cwd-projection.md`: recommendation policy, cwd sanitization, no-leak behavior.
- `05-tests-docs-review.md`: test matrix, docs updates, verification commands, review handoff.

## Implementation Route

1. Add DTOs and schemas for Phase 4 fields.
2. Add validation for `pattern_mode`, `line_window`, and `max_matches_per_file`.
3. Implement literal pattern compilation without changing regex default.
4. Implement file-only line-window search.
5. Extend grep traversal and collector to produce honest `search_stats`.
6. Extend collector to build bounded `file_groups[]` and `read_ranges`.
7. Add `next_recommended_call` policy.
8. Extend cwd projection/sanitization for all new path and recommended-input fields.
9. Update structured error constructors for new stable array fields.
10. Update server descriptions, docs, and manifest metadata.
11. Run targeted/full tests and review cycle.

## Global Acceptance

Plan is ready for implementation only when plan review confirms:

- It preserves the clean-approved concept and does not drift into `rg` clone, semantic search, indexing, or dotfile/default-ignore changes.
- It names exact public fields and compatibility behavior.
- It explains completeness/counting semantics so implementation cannot overclaim.
- It identifies all path-bearing fields and cwd-aware no-leak surfaces.
- It defines meaningful tests for literal mode, stats, groups, recommended calls, line window, caps, cwd, and old behavior.

Implementation is accepted only when:

- Existing grep tests pass.
- `pattern_mode=literal` finds regex-looking text without escaping.
- Regex remains default and invalid regex still errors only in regex mode.
- `search_stats` is present and exact for match, no-match, truncated, capped, binary, unreadable, hidden, VCS, and user-ignored cases.
- `file_groups[]` groups returned content evidence by file and produces bounded `read_ranges` without bloating `files_with_matches` or `count`.
- `next_recommended_call` is replayable and cwd-safe.
- `max_matches_per_file` works without stopping later files.
- `line_window` works on a file and errors on directories.
- Hidden/dot/git skip behavior is unchanged.
- Large-file line streaming and multiline memory guardrails are preserved.
- Docs and schema describe the new agent navigation contract.

## Global Checks

Expected verification after implementation:

- `GOPROXY=off go test ./filetoolsserver/handler ./filetoolsserver -run "Grep|Schema|Cwd"`
- `GOPROXY=off go test ./...`
- Focused MCP/cwd tests for recommended inputs and path projection.
- Manual/static docs check for `README.md`, `TOOLS.md`, `server.json`, and server tool description.

## Stop And Ask If

- Implementing `line_window` for multiline would require scanning or returning content outside the window.
- A proposed stats field cannot be tracked honestly without changing traversal architecture broadly.
- A recommendation would require choosing semantic meaning that `grep` cannot know.
- Any implementation path would change default ignore/hidden behavior.
- Any path-bearing new field cannot be projected safely under `cwd_id`.
