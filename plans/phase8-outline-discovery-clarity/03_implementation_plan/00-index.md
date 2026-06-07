# Phase 8 Implementation Plan

Goal:
Deliver practical 10/10 Agent Experience improvements for MCP file tools by reducing discovery noise, improving outline quality for common languages, separating inventory completeness concepts, and making joiner dry-run outcomes obvious before mutation.

Source concept:
- `../01_human_concept.md`
- `../02_technical_concept.md`

User-visible result:
- Agents can identify the right file tool from compact metadata and can lazy-load full schemas when needed.
- `outline_file` gives high-signal, parser-backed navigation for JS, TS, TSX, Python, Java, JSON and YAML, with selectors that round-trip through `resolve_symbol_range`.
- `workspace_inventory` exposes canonical page/summary/tree completeness fields so `summary.complete=false` cannot be confused with page continuation status.
- Range tool dry-runs explain `joiner` newline style and visual blank-line effects, especially for `blank_line` plus existing empty edge lines.

Scope:
- Discovery/tool metadata in `filetoolsserver/server.go`, `server.json`, `README.md`, `TOOLS.md`, and related tests.
- Outline routing, tree-sitter extraction, profile filtering/noise handling, selector resolution, and fixtures/tests for JS/TS/TSX/Python/Java/JSON/YAML.
- Workspace inventory output structs, schema, handler semantics, docs, and tests.
- Range transfer joiner diagnostics for single and batch range tools, docs, schema, and tests.
- Verification probes that check agent-facing behavior, not only internal helpers.

Out of scope:
- IDE-grade semantic engine, type checking, rename symbol, import organizer, AST rewrite, UI, external service, broad cwd_id redesign.
- Returning safety-first redaction defaults. Default redaction stays `off`; `strict` is explicit, and `auto` remains a compatibility alias for `strict`.
- Removing the range-tool fingerprint, dry-run, explicit apply, diff preview, and read-back validation model.

Must preserve:
- Go and Markdown outline reliability.
- Existing cwd-aware behavior: with `cwd_id`, inputs are relative and path outputs are cwd-relative.
- `resolve_symbol_range` never mutates files; write recommendations remain dry-run-only.
- Existing public fields remain unless proven internal-only; inventory/joiner clarity is additive.
- Windows build and normal Go test workflow.
- No git branch creation.

Plan file map:
- `01-discovery-metadata.md` - compact two-layer tool metadata and discovery probes.
- `02-outline-quality.md` - language routing, extraction, profiles, selector round-trip, and fixtures.
- `03-workspace-inventory.md` - canonical completeness semantics and schema/docs/tests.
- `04-joiner-diagnostics.md` - joiner DTO, boundary analysis, and dry-run warnings.
- `05-docs-tests-verification.md` - docs/schema consistency, full verification sequence, restart/smoke.

Global decisions:
- Discovery baseline: with `max_lines=150`, a generic agent sees all 14 tool names and short summaries in `tool_search` metadata, but callable tools and full schemas are still lazy-loaded. Phase 8 should make both pre-search summaries and post-search full descriptions better; it does not need to eliminate lazy loading.
- `import_block` contains real import declarations only. Source-bearing re-exports such as `export ... from "pkg"` use kind `re_export`. Exported declarations keep their declaration kind (`function`, `class`, `component`, `variable`, `interface`, `type`) and expose export status via detail/metadata, not by changing into import-like items.
- Inventory adds canonical fields and keeps old `summary.complete` / `continuation.complete` as compatibility aliases.
- Java must be parser-backed. If the ordinary Windows Go build cannot expose Java grammar, stop at an explicit dependency/build gate instead of silently falling back to generic text.
- Joiner dry-run is the source of truth; apply must not infer or repair whitespace heuristically.

Global acceptance:
- The four agent-facing probes in `../01_human_concept.md` pass with representative fixtures.
- Every selector emitted by `outline_file` in `agent` or `full` profile resolves through `resolve_symbol_range`.
- Default `agent` outline profile is compact/action-oriented; `full` remains the opt-in complete navigation view.
- Docs, server metadata, server.json, Go structs, output schemas, and tests describe the same public fields.

Global checks:
- Focused tests for discovery metadata, outline languages, selector round-trip, inventory semantics, joiner diagnostics.
- `go test -count=1 ./filetoolsserver/handler`
- `go test -count=1 ./...`
- Targeted race tests for outline/search/write/inventory areas if practical in local runtime.
- `go build` for the normal Windows target.
- MCP restart/watchdog smoke after implementation review is clean.

Stop and ask if:
- A public schema field name needs to change after implementation starts and compatibility impact is unclear.
- Java parser-backed outline requires a new dependency or build tag that is not ordinary Windows-compatible.
- Any step would require destructive git, secret reads, publishing, live systems, migrations, or network-dependent verification.
