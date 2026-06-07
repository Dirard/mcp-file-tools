# Phase 6 Implementation Plan: Language-Aware Outline And Symbol Operations

plan_version_label: phase6-language-outline-symbol-ops-srs-v1
status: clean_reviewed_ready_for_implementation
concept_source:
- plans/phase6-language-outline-symbol-ops/01_human_concept.md
- plans/phase6-language-outline-symbol-ops/02_technical_concept.md
depends_on:
- plans/phase5-agent-safe-write-read-discovery/03_implementation_plan
- Phase 5 write diff/read-back/validation implementation must be complete before Phase 6 Stage 4 symbol-write workflow is implemented.
- Phase 6 Stage 4 cannot start until the Phase 5 write-safety test group for diff preview, redaction, boundary preview, backup discovery, post-write read-back/validation and cwd no-leak passes in the same working tree.

## Goal

Upgrade `outline_file` from Go/Markdown/generic text structure into a language-aware structure and selector layer for coding agents.

The final product must let an agent:

1. Outline Go, Markdown, TypeScript/JavaScript/TSX/JSX/React, Svelte, Python, JSON and YAML.
2. See honest parser status, scope, confidence, ranges and fallback warnings.
3. Find enclosing symbols around known lines.
4. Resolve exact symbol selectors to concrete line ranges under the same file fingerprint.
5. Use resolved exact ranges with existing range-transfer tools for symbol-aware copy/move.
6. Refuse mutation from estimated, ambiguous, stale or parse-error ranges.

## Scope

Affected tools:

- `outline_file`
- new `resolve_symbol_range` helper
- `copy_ranges`
- `move_ranges`
- docs/server metadata/tests

Affected technical areas:

- parser adapter layer;
- language detection;
- normalized `OutlineItem` selector metadata;
- parser status/error codes;
- cwd path projection;
- symbol-to-range safety.

## Out Of Scope

- No full LSP.
- No project-wide semantic index.
- No type checking.
- No cross-file rename.
- No dependency graph/build graph.
- No semantic import repair or formatter.
- No general AST rewrite engine.
- No structural edit implementation as Phase 6 core.
- No batch symbol recommendation or direct batch selector input in Phase 6 core; batch symbol workflows are future-gated.
- No broad hidden/default path behavior changes.
- No mutation from estimated ranges.

## Must Preserve

- Existing Go outline remains exact.
- Existing Markdown outline remains exact.
- Existing generic fallback remains available and honest.
- Existing `outline_file` filters remain useful:
  - `line_window`
  - `name_contains`
  - `kinds`
  - `max_items`
  - `max_depth`
  - `output_profile`
- Existing range write tools remain line/fingerprint-safe.
- Phase 5 write preview/read-back/validation contracts apply to symbol-derived writes.
- Cwd-aware path projection applies to every new path-bearing field.

## Concept Transferred Into SRS

User-visible result:

- Agents stop manually copying line numbers for common symbols/config nodes.
- Agents can navigate frontend/backend/config files structurally after `grep`.
- Agents can trust exact parser ranges and see explicit refusal for ambiguous/estimated ranges.

Behavior / contracts:

- Parser support is honest by language and status.
- Selectors are file-scoped, fingerprint-scoped and not permanent global IDs.
- Symbol-aware operations resolve to concrete ranges before write through `resolve_symbol_range`.
- Direct `source_selectors[]` on write tools are not Phase 6 core; they remain a future gate unless a later reviewed plan explicitly accepts them.
- Structural edit is only documented as a future gate.

Acceptance:

- Each supported language has fixtures and parser-status tests.
- Symbol resolution works on exact ranges and refuses unsafe cases.
- Existing tools and old behavior remain compatible.

## Plan File Map

- `00-index.md`: global goal, decisions, dependencies, acceptance, checks.
- `01-parser-dependency-and-core-contracts.md`: dependency proof, parser adapter, language detection, item/selector schema.
- `02-language-parsers.md`: Go/Markdown regression plus TS/JS/React/Svelte/Python/JSON/YAML parser extraction rules.
- `03-enclosing-and-selector-resolution.md`: line-to-enclosing behavior and `resolve_symbol_range` helper.
- `04-symbol-aware-range-tools.md`: selector-derived copy/move integration through existing range transfer safety.
- `05-docs-tests-review.md`: fixtures, tests, docs, review handoff.

## Global Decisions

1. Phase 6 uses a parser adapter layer; parsers return existing `OutlineItem` plus optional selector metadata.
2. Parser dependencies must be pure-Go or otherwise preserve current cross-platform native build expectations; `CGO_ENABLED=0` build proof is required before implementation proceeds.
3. Ordered parser dependency candidate:
   - first prove `github.com/odvcencio/gotreesitter` can parse required grammars in pure Go and build cleanly;
   - use `gopkg.in/yaml.v3` only if it improves YAML range extraction without replacing tree-sitter proof;
   - do not use CGo tree-sitter bindings.
4. If parser dependency proof fails, Phase 6 stops and returns to planning/concept; it must not replace exact parsers with regex while claiming exact ranges.
5. `OutlineItem` gains selector metadata but keeps existing fields.
6. Selector metadata is file-scoped and fingerprint-scoped.
7. Exact symbol writes require exact ranges and matching fingerprint.
8. Estimated ranges are read/navigation only.
9. `resolve_symbol_range` is the Phase 6 core API for converting selectors to ranges. Stage 4, after the Phase 5 write-safety gate, extends it with explicit target intent and ready recommended `copy_ranges` / `move_ranges` inputs with concrete line ranges.
10. Existing range tools remain the mutation engine.
11. Structural edit is not implemented in Phase 6 core.
12. Parser-backed byte ranges are converted to line ranges for current write tools. A range is `write_safe=true` only when the parser node expands to a whole-line range without absorbing sibling tokens and without requiring comma/delimiter/token repair outside the selected range; same-line object/key/declaration fragments and delimiter-sensitive fragments can be read-safe but not write-safe.
13. Full Phase 6 acceptance requires Stage 4 symbol-aware copy/move after Phase 5 write diff/read-back/validation is implemented and tested. If Phase 5 is not ready, Phase 6 can only reach a partial blocked milestone after parser extraction, enclosing lookup and read-only `resolve_symbol_range`; it must not be marked complete.

## Global Acceptance

Implementation is accepted only when:

- Parser dependency proof passes with `CGO_ENABLED=0`.
- Dependency proof records version, license, vendored/offline status, build time, binary size delta and stays within budget or stops for root decision.
- Because the planned primary parser dependency is currently pre-v1, implementation must stop for root decision after proof unless root has already accepted that exact dependency/version.
- Go and Markdown regression tests remain green.
- TypeScript/JavaScript/TSX/JSX outline returns imports, exports, functions, classes, methods, interfaces/types, variables, and conservative component metadata where supported.
- Svelte outline returns module script, instance script, style, markup and nested script symbols where parser support allows exact extraction.
- Python outline returns imports, functions, classes, methods and decorator metadata.
- JSON outline returns object/array/property/value items with path names and exact ranges.
- YAML outline returns document/mapping/sequence/key/value items with path names and honest limitations.
- Parser failures return structured status/errors and do not fake exact ranges.
- `line_window`/enclosing lookup returns innermost symbol plus parent path where possible.
- `resolve_symbol_range` resolves unambiguous exact symbols and refuses ambiguous/stale/estimated symbols.
- Stage 4 symbol-aware copy/move flows use `target_intent` to produce ready dry-run `recommended_next_input` maps with concrete `ranges`, so agents do not manually copy line numbers from output.
- `resolve_symbol_range` never recommends `dry_run=false`; apply remains a separate explicit range-tool call after preview.
- Stage 4 `recommended_write_call` is allowed only when the current reparse returns `parser_status="ok"`; `partial`, `parse_error`, `generic_fallback`, `estimated_only` and unsupported statuses are read/navigation only.
- Symbol-derived writes only proceed for `write_safe=true` whole-line delimiter-safe ranges; one-line JSON/JS/YAML fragments and ranges that require adjacent delimiter repair are refused or returned read-only.
- Symbol-aware copy/move flows end in concrete `ranges` and Phase 5 diff/validation output.
- Stage 4 symbol write workflow is implemented only after the explicit Phase 5 write-safety test group passes in the same working tree.
- Batch symbol workflows are documented as future-gated and are not required for Phase 6 acceptance.
- Cwd mode has no absolute path leaks.
- Docs explain exact vs estimated behavior and non-LSP boundaries.

## Global Checks

Expected verification:

- Dependency proof:
  - stop before proof and get root/user approval for exact module/version candidates or bounded latest-candidate proof, network access, and expected `go.mod`/`go.sum`/`vendor` mutation;
  - run module/network/vendor commands only after that approval;
  - `go mod download`
  - parser query/range extraction smoke tests for JavaScript, TypeScript, TSX/JSX, Python, JSON, YAML and Svelte, including Svelte nested source offsets;
  - dependency budget note: version, license, vendored/offline status, build time, binary size before/after and whether root decision is needed;
  - if `vendor/` is committed, these parser tests and build must pass:
    - PowerShell: `$env:GOFLAGS='-mod=vendor'; $env:GOPROXY='off'; $env:CGO_ENABLED='0'; go test -count=1 ./filetoolsserver/handler -run "OutlineParserDependency|Outline|Symbol|Schema|Cwd"`
    - PowerShell: `$env:GOFLAGS='-mod=vendor'; $env:GOPROXY='off'; $env:CGO_ENABLED='0'; go build -trimpath -buildvcs=false -o .\mcp-file-tools.exe .\cmd\mcp-file-tools`
  - PowerShell: `$env:CGO_ENABLED='0'; go test -count=1 ./filetoolsserver/handler -run "Outline|Symbol|Schema|Cwd"`
  - PowerShell: `$env:CGO_ENABLED='0'; go build -trimpath -buildvcs=false -o .\mcp-file-tools.exe .\cmd\mcp-file-tools`
- Main checks after implementation:
  - PowerShell: `$env:GOPROXY='off'; go test -count=1 ./filetoolsserver/handler ./filetoolsserver -run "Outline|Symbol|Copy|Move|Schema|Cwd"`
  - PowerShell: `$env:GOPROXY='off'; go test -count=1 ./...`
  - PowerShell: `$env:GOPROXY='off'; go test -race -count=1 ./filetoolsserver/handler -run "Outline|Symbol|Cwd"`
  - PowerShell: `$env:CGO_ENABLED='0'; go build -trimpath -buildvcs=false -o .\mcp-file-tools.exe .\cmd\mcp-file-tools`
- Targeted symbol-write checks:
  - outline selector -> `resolve_symbol_range(target_intent)` -> dry-run copy/move recommended input with no manual line-number transcription;
  - `target_intent.dry_run=false` refusal;
  - `parser_status="partial"` refusal for write recommendation;
  - JSON/YAML/JS/TS delimiter fixtures prove first/middle/last entries, trailing commas and same-line siblings are not mutation-safe unless the selected range owns all needed delimiters.

## Stop And Ask If

- No pure-Go parser path can support the required languages with acceptable build size/cost.
- A language can only be represented by brittle regex while needing exact mutation ranges.
- Symbol writes would need estimated ranges.
- Parser output cannot be bounded by existing limits.
- New selector fields cannot be projected safely under `cwd_id`.
- Any implementation path tries to become LSP, semantic rename, formatter, or AST rewrite.
