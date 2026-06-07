# Phase 3 Implementation Plan: cwd_id and Tool UX

plan_version_label: phase3-cwd-id-tool-ux-srs-v1
status: draft_for_plan_review
concept_source:
- plans/phase3-cwd-id-tool-ux/01_human_concept.md
- plans/phase3-cwd-id-tool-ux/02_technical_concept.md

## Goal

Add a `set_cwd(directory)` tool and a small integer `cwd_id` mode so agents can keep using short relative paths across file-tools calls without introducing chat/session isolation, hidden process cwd, or path output ambiguity.

The final product must make the following true:

- `set_cwd` takes exactly one user-facing input, `directory`, and returns only `{ "cwd_id": <int> }`.
- `cwd_id` is server-wide and usable by parallel subagents and future chats against the same running file-tools server.
- `cwd_id` is a lookup key, not a security token and not a session-scoped attribute.
- Without `cwd_id`, tools keep absolute-only input behavior, but all path outputs become slash-normalized display paths such as `D:/ai-apps/mcp-file-tools/README.md`.
- With `cwd_id`, every path input parameter in every existing path tool is relative-only, and all filesystem path outputs are relative to the registered cwd, slash-normalized, with no leading `./`; the only absolute path in cwd-aware success or resolved-error output is the `cwd` metadata field.
- No cwd-aware filesystem path fields, replay input maps, generated errors, warnings, recovery text, or action hints leak raw absolute paths; content/query/content-derived fields remain literal.
- `outline_file` gives useful generic structure for ordinary non-Go/non-Markdown text instead of only a fingerprint.
- `inspect_path`, `read_file`, `outline_file`, and fingerprint line counts use one final-empty-line model.
- `move_ranges_batch` byte metrics are explicit enough that target-write bytes and source-rewrite bytes cannot be confused.

## Scope

Tools affected:

- New: `set_cwd`
- Existing path tools with optional `cwd_id`: `read_file`, `outline_file`, `copy_ranges`, `move_ranges`, `copy_ranges_batch`, `move_ranges_batch`, `list_dir`, `glob_file_search`, `grep`, `inspect_path`, `workspace_inventory`

Canonical cwd-aware input examples:

```json
{ "target_path": "internal/test.go", "cwd_id": 1 }
```

```json
{ "target_file": "README.md", "cwd_id": 1 }
```

Without `cwd_id`, these same relative inputs remain invalid.

Code and metadata surfaces affected:

- handler inputs, outputs, path validation, path display, error/recovery helpers, and tests
- server registration, schema constraints, manual schemas, server instructions, `server.json`
- `README.md`, `TOOLS.md`, and manual smoke examples where they describe absolute path behavior, tool count, line counts, or batch bytes
- container/runtime config surfaces: `Dockerfile`, `docker-compose.yml`, `smithery.yaml`, and package/runtime config files if present, so allocator state is either persistent and explicit or `set_cwd` returns `cwd_state_unavailable`

## Out Of Scope

- No `os.Chdir` and no process-wide current directory.
- No hidden default cwd chosen from chat, thread, environment, or last call.
- No session/chat/subagent isolation for `cwd_id`.
- No durable recovery of active cwd entries across file-tools process restart. After restart, old ids may be unavailable, but must never silently map to a different cwd.
- No security model change: `cwd_id` is for token-efficient path ergonomics, not for filesystem authorization.
- No broad refactor of unrelated handler logic.

## Global Decisions

1. `set_cwd` input schema has one field: `directory`.
2. `set_cwd` output schema has one field: `cwd_id`.
3. Other path tools get optional `cwd_id`; agents pass it as a normal tool input.
4. Active cwd entries live in the server handler, shared by all clients connected to that server process.
5. A SQLite allocator stores only id allocation state and prevents active ids from being reused for a different cwd after restart. Active cwd entries themselves are memory-resident.
6. `cwd_id` values are small sequential positive integers, allocated from the persistent high-water mark.
7. TTL target is 7 days from the most recent successful `set_cwd` registration for that active id; ordinary path-tool lookups do not refresh TTL.
8. If `cwd_id` is missing, all path inputs must be absolute as before.
9. If `cwd_id` is present, all path inputs must be relative; absolute, drive-relative, empty, and outside-cwd paths are rejected.
10. Output paths use `/` on every OS and in every path-map display alias.
11. With `cwd_id`, output field `cwd` is absolute and slash-normalized; every other filesystem path output is cwd-relative or `"."`.
12. No cwd-aware filesystem path fields, replay input maps, or generated diagnostic/recovery/hint text may embed raw absolute paths outside the `cwd` metadata field; content/query/content-derived fields remain literal and are not sanitized just because they contain path-looking text.
13. `OutlineItem.path` is structural outline ancestry, not a filesystem path.
14. Top-level `would_write_bytes` in batch outputs is retained only as a documented compatibility alias, not as the primary metric.

## Plan File Map

- `00-index.md`: product goal, global decisions, route, and acceptance.
- `01-contracts-and-state.md`: `set_cwd`, registry, TTL, SQLite allocator, config, and failure modes.
- `02-path-resolution-and-output-projection.md`: path modes, slash normalization, output projection, no-leak inventory, and error behavior.
- `03-schema-and-tool-surface.md`: JSON schema, MCP registration, docs, server metadata, and public descriptions.
- `04-tool-handler-migration.md`: per-tool migration of read, search, inspect, workspace, write, and batch handlers.
- `05-outline-linecount-batch-metrics.md`: generic outline fallback, unified line-count model, and byte metric contract.
- `06-tests-docs-review.md`: test matrix, verification commands, review loop, and handoff rules.

## Implementation Route

1. Add cwd registry, allocator, config, and `set_cwd` contract.
2. Add shared path resolution/projection primitives and slash-normalized display behavior.
3. Update schemas and tool registration to expose the new conditional path contract.
4. Migrate read/search/inspect/workspace handlers.
5. Migrate outline behavior, line counts, and generic text fallback.
6. Migrate copy/move/batch handlers and recovery/action-hint surfaces.
7. Update docs, `server.json`, tests, and smoke examples.
8. Run verification and then the implementation review cycle required by `codex-flow-v2`.

## Global Acceptance

The plan is ready for implementation only when plan review confirms:

- Product transfer is clean: every user correction is represented, especially no session isolation and cwd-aware relative outputs.
- Engineering readiness is clean: implementation steps identify all path-bearing fields, nested recovery fields, schemas, docs, and tests.
- No open product question remains that would force an implementation agent to invent behavior.

The implementation is accepted only when:

- `set_cwd` returns only a small `cwd_id` integer and does not change process cwd.
- Live `cwd_id` values work across independent MCP clients connected to the same server.
- Expired, unknown, or post-restart stale ids fail with structured errors and do not resolve to another cwd.
- No-cwd mode remains absolute-input-only and returns slash-normalized absolute/display paths.
- Cwd mode rejects absolute inputs and returns cwd-relative paths without `./`.
- Cwd mode accepts relative path input parameters for all 11 existing path tools, including nested batch target paths.
- Cwd-aware filesystem path fields, replay input maps, nested warnings, recovery hints, backup results, and action hints do not leak absolute paths; `read_file.text`, `grep.matches[].text`, query/pattern fields, and content-derived outline labels are excluded from no-leak assertions.
- Tool discovery and `server.json` expose 12 tools.
- Generic outline fallback, line count consistency, and batch byte metric clarity are covered by tests and docs.

## Global Checks

Expected verification after implementation:

- `go test ./...`
- focused registry concurrency tests under `go test -race` or equivalent race check
- SQLite dependency vendoring is consistent (`go.mod`, `go.sum`, `vendor/`, `vendor/modules.txt`)
- focused handler tests for cwd registry, path projection, outline fallback, line counts, and batch metrics
- manifest/schema checks for 12 tools, cwd-aware path schema behavior, and explicit path descriptions when conditional JSON Schema is not practical
- runtime config checks cover stable `MCP_CWD_STATE_PATH` behavior in local, Docker Compose, and Smithery/container surfaces
- docs checks or targeted review of `README.md`, `TOOLS.md`, `server.json`, and server instructions

## Stop And Ask If

- Adding pure-Go SQLite or file-lock dependencies is unacceptable for this repository.
- The chosen default SQLite state path cannot be made stable and writeable in supported runtime environments.
- A needed output field cannot be projected relative to cwd without either leaking an absolute path or changing the public response shape.
- A schema generator limitation prevents representing the dual absolute/relative contract without misleading tool discovery.
- Product behavior needs to choose between incompatible options not settled in the concept or this SRS.

## Review Requirement

Before implementation, run an independent plan review cycle:

- `product_owner`: checks that this SRS preserves the agreed concept and user value.
- `reviewer`: checks engineering completeness, sequencing, missing surfaces, and testability.

After both reviews are clean and the user says OK, implementation may begin.
