# Phase 2 Agent Refactor Tools Implementation Plan

plan_version_label: phase2-agent-refactor-plan-v1
status: reviewed, ready for implementation
concept_source:
- `plans/phase2-agent-refactor/01_human_concept.md`
- `plans/phase2-agent-refactor/02_technical_concept.md`

## Goal

Implement five agent-friendly MCP tools for exact range-based refactoring of large local files:

- `outline_file`
- `copy_ranges`
- `move_ranges`
- `copy_ranges_batch`
- `move_ranges_batch`

The result must let an agent inspect file structure, select exact line ranges, dry-run risky writes, apply single-target or explicit multi-target transfers, and recover from common failures without guessing the next tool call.

## Scope

In scope:

- Markdown ATX and Go exact `outline_file`.
- Unsupported-language fingerprint output without fake exact ranges.
- UTF-8/ASCII write support for exact byte-range transfer.
- Single-source/single-target copy and move tools.
- Single-source/multi-target batch copy and move tools.
- `dry_run`, boundary warnings, next fingerprints, actionable errors, truncation recovery.
- Documentation, schema, unit tests, and smoke coverage for all new tools.

Out of scope:

- LSP, workspace index, watcher/cache as correctness dependency.
- Semantic refactor, formatter, auto-imports, reference updates, symbol rename.
- `split_markdown_sections` or automatic file naming.
- Binary editing, UTF-16/UTF-32 writes.
- Same-file copy/reorder/move.
- Following symlink paths for mutated files.
- Tree-sitter TS/JS/Python/Java implementation in Stage 1.

## Must Preserve

- Existing six tools keep behavior and schemas unless explicitly documented as additive docs updates.
- All path inputs remain full absolute paths for the MCP server OS; empty and relative paths are rejected.
- Successful tool results stay tool-specific structured JSON, with no cursor/nextCursor pagination.
- Error MCP content remains empty; actionable error data lives in structured output.
- Path-map behavior and server-OS path display remain first-class.
- Existing `read_file` compact `text` output is not replaced by per-line JSON objects.

## Global Decisions

- Implement Stage 1 entirely in Go stdlib plus existing dependencies. Do not add Tree-sitter in this plan.
- Keep public MCP handlers in `filetoolsserver/handler`; split implementation into focused files rather than one large handler.
- Add pure helper files under `filetoolsserver/handler` first. Extract to `internal/refactor` only if implementation becomes tangled; do not start with a new package unless import boundaries demand it.
- Use `go/parser` / `go/ast` for Go outline.
- Use a hand-written Markdown ATX scanner for MVP.
- Choose streaming/hybrid write construction: do not build full target-after content in memory for normal write paths.
- Add `MCP_WRITE_THRESHOLD` with default `67108864` bytes (64 MiB), matching the current memory threshold default. Threshold is an allow/deny cap, not a reason to load whole files into memory.
- Use per-handler path locks for all mutated paths, acquired in stable resolved-path order.
- Use temp-file plus atomic rename for existing target/source replacement where supported. For create-new target, use an overwrite-safe create path; never silently overwrite an existing target.
- Treat residual external-editor races after final recheck as documented OS-level risk; do not claim cross-process transaction atomicity.

## Plan File Map

- `01-contracts-foundation.md` - shared types, schemas, config, tool registration, error contracts.
- `02-outline-file.md` - `outline_file`, fingerprints, Markdown/Go parsers, truncation recovery.
- `03-range-transfer-engine.md` - byte-span engine, encoding, symlink/file identity safety, locks, atomic writes.
- `04-single-write-tools.md` - `copy_ranges` and `move_ranges` behavior, dry-run, warnings, next fingerprints.
- `05-batch-tools-recovery.md` - `copy_ranges_batch`, `move_ranges_batch`, batch partial recovery.
- `06-docs-smoke-review.md` - docs, smoke, tests, review cycle, rollout notes.

## Implementation Route

1. Build contracts and shared structs first so schemas and handlers have a stable target.
2. Implement `outline_file` before write tools because write tools depend on its fingerprint/range workflow.
3. Build the raw range-transfer engine behind tests before exposing mutating handlers.
4. Add single-target tools on top of the engine.
5. Add batch tools after single tools are correct, reusing the same precondition and streaming write primitives.
6. Update docs/server instructions and run smoke only after all tool schemas are registered.
7. Run plan/implementation review cycle before treating the phase as done.

## Global Acceptance

The plan is complete when implementation can prove:

- Agents can get source and target fingerprints without full-file reads.
- Agents can inspect Markdown/Go structure and choose exact line ranges.
- Agents can dry-run every write tool.
- Agents can use structured errors to know the next safe tool call.
- Agents can split a large Markdown file into many explicit targets in one batch call.
- Crash/partial failures return enough state for manual or agent reconciliation.
- Existing read-only tools still pass existing tests and smoke.

## Global Checks

Required after implementation:

- `go test ./...`
- `go run ./test_server.go`
- Manual MCP smoke for all existing tools plus five new tools.
- Windows path behavior tests for absolute/relative/path-map/symlink-sensitive cases where OS supports them.
- Large-file representative tests for outline and write threshold behavior.
- Documentation review against `README.md`, `TOOLS.md`, and server tool descriptions.

## Stop And Ask If

- A new dependency is needed for Stage 1.
- A platform-safe create-new no-overwrite strategy cannot be implemented without weakening data integrity.
- UTF-16/UTF-32 support becomes necessary for real fixtures.
- Same-file move/copy appears required.
- Product behavior would choose target names, headings, imports, formatting, or semantic movement for the agent.
