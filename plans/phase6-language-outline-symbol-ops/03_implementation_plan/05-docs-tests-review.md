# Stage 5: Docs, Tests, Review, And Rollout

## Goal

Document language support and symbol workflows, verify parser honesty, and prepare plan/implementation review.

## Depends On

- Stages 1-4.

## Touched Areas

- `README.md`
- `TOOLS.md`
- `server.json`
- `filetoolsserver/server.go`
- `filetoolsserver/handler/*_test.go`
- parser fixtures under handler test fixtures

## Documentation Steps

1. Update `outline_file` docs:
   - supported languages;
   - parser statuses;
   - exact vs estimated confidence;
   - selector metadata;
   - enclosing line.
2. Add `resolve_symbol_range` docs:
   - input selector forms;
   - Stage 3 read/navigation output, `resolution_status`, `next_recommended_call`, and `next_recommended_calls[]`;
   - fingerprint requirement;
   - ambiguity handling;
   - estimated-range refusal;
   - exact vs estimated ranges and no selector on estimated ranges.
3. Document symbol-aware workflow:
   - `grep`;
   - `outline_file`;
   - Stage 3 `resolve_symbol_range` for read/navigation;
   - Stage 4 `resolve_symbol_range(target_intent)`;
   - `copy_ranges`/`move_ranges` dry-run from `recommended_write_call.recommended_next_input` where `recommended_write_call` is `ActionHint`-shaped;
   - apply and validate.
4. Document non-goals:
   - not LSP;
   - not semantic rename;
   - not formatter;
   - not AST rewrite;
   - not a batch symbol workflow in Phase 6 core.
5. Update `server.json` tool list if `resolve_symbol_range` is added.
6. Update MCP tool descriptions in `server.go`.
7. Document parser dependency and build expectations.

## Fixture Matrix

Create minimal fixtures:

- Go function/method/type/import.
- Markdown nested headings.
- TypeScript imports, interface, type alias, function, class, method, exported const component candidate.
- TSX/JSX React function component and arrow component.
- Svelte module script, instance script, style, markup, nested JS symbol if supported.
- Python imports, decorator, class, method, function, nested function.
- JSON nested object/array/property.
- JSON one-line object with multiple properties for non-write-safe exact selector behavior.
- YAML mapping, sequence, multi-document, anchor/alias if supported.
- YAML one-line mapping/sequence when parser supports it for non-write-safe exact selector behavior.
- JavaScript/TypeScript one-line multiple declaration/statement fixture for non-write-safe exact selector behavior.
- Malformed TS/Python/JSON/YAML/Svelte cases.
- Binary and undecodable text fixtures that must not produce fake language structure or crash.

## Test Matrix

Required tests:

- Language auto-detect and aliases.
- Parser status ok/partial/parse_error/generic_fallback.
- Expected symbol kinds by language.
- Exact range line assertions.
- Parser byte/range extraction assertions for dependency proof.
- Svelte nested script offset assertions if nested script symbols are supported.
- Selector presence only on exact ranges.
- No selector on estimated ranges.
- `resolve_symbol_range` outputs `confidence` and `range_is_estimated` so exact-not-write-safe ranges cannot be confused with estimated ranges.
- `write_safe=true` only on whole-line ranges.
- `line_window`, `enclosing_line`, `name_contains`, `kinds`, `max_items`, `max_depth`.
- `resolve_symbol_range` success.
- `resolve_symbol_range` top-level language vs selector language conflict.
- `resolve_symbol_range` successful read/navigation resolution with write refusal and no top-level `error_code`.
- `resolve_symbol_range` ambiguity.
- `resolve_symbol_range` stale fingerprint.
- `resolve_symbol_range` estimated refusal.
- `resolve_symbol_range` non-whole-line refusal.
- `resolve_symbol_range` parser-partial write refusal.
- Stage 4 symbol workflow into copy/move using `target_intent` and ready `ActionHint` recommended input, with no manual line-number transcription.
- Stage 4 `resolve_symbol_range(target_intent)` never recommends `dry_run=false`.
- Stage 4 `target_intent.dry_run=false` refusal with absent `recommended_write_call`, read/inspection-only `next_recommended_call`, and preview-safe `preview_write_call`.
- Stage 4 `target_intent.dry_run=false` preserves read/inspection hints in `next_recommended_call` / `next_recommended_calls[]` while putting the dry-run write preview only in `preview_write_call`.
- Stage 4 same-file source/target refusal with `target_same_file_unsupported`.
- Stage 4 same-file refusal covers normalized same path, cwd-relative aliases, case/drive normalization where applicable, hardlinks where available, and symlink/junction same-file target with platform skip notes.
- Stage 4 structured target syntax unknown/unsafe refusal with `target_syntax_not_proven`.
- Stage 4 target syntax safe-mode tests cover `create_new` and Markdown/plain text line insertion. Existing structured target insertions are refused in Phase 6 unless a later SRS expands the allowlist.
- Stage 4 target syntax safe-mode tests assert `target_syntax_proof` and `target_syntax_proof_reason`, distinguishing parser-proven Markdown from caller-asserted plain text.
- `target_syntax_mode="plain_text"` or `target_syntax_mode="markdown"` on structured targets such as `.json`, `.yaml`, `.ts` or `.js` refuses with `target_syntax_not_proven`.
- Invalid `target_intent.operation` and invalid `target_syntax_mode` return stable schema/error codes.
- Cwd mode proves `recommended_write_call.recommended_next_input.cwd_id` is present when the resolver request used cwd mode.
- Cwd mode proves preview-safe `preview_write_call.recommended_next_input.cwd_id` is present when returned.
- Stage 4 gate requires a passed Phase 5 write-safety test group in the same working tree before symbol write workflow ships; a review note alone is not sufficient.
- Dependency gate review note recording version, license, build time, binary delta and whether budgets were met.
- Vendor/offline proof uses concrete PowerShell commands when `vendor/` is committed:
  - `$env:GOFLAGS='-mod=vendor'; $env:GOPROXY='off'; $env:CGO_ENABLED='0'; go test -count=1 ./filetoolsserver/handler -run "OutlineParserDependency|Outline|Symbol|Schema|Cwd"`;
  - `$env:GOFLAGS='-mod=vendor'; $env:GOPROXY='off'; $env:CGO_ENABLED='0'; go build -trimpath -buildvcs=false -o .\mcp-file-tools.exe .\cmd\mcp-file-tools`.
- Batch symbol workflow is documented as future-gated, not Phase 6 complete behavior.
- Cwd no-leak for file, warnings, selector outputs, recommended inputs, and language-local `OutlineItem.path` / `enclosing_path` / `symbol_path` not being treated as filesystem paths.
- Delimiter-sensitive write-safety tests for JSON/YAML/JS/TS first/middle/last properties or declarations, trailing commas and same-line siblings.
- Byte-to-line conversion tests for CRLF, trailing newline, node ending at column 0, end-exclusive newline boundaries and same-line siblings.
- Existing Go/Markdown/generic regressions.

## Verification Commands

After implementation:

- PowerShell: `$env:GOPROXY='off'; go test -count=1 ./filetoolsserver/handler ./filetoolsserver -run "Outline|Symbol|Copy|Move|Schema|Cwd"`
- PowerShell: `$env:GOPROXY='off'; go test -count=1 ./...`
- PowerShell: `$env:GOPROXY='off'; go test -race -count=1 ./filetoolsserver/handler -run "Outline|Symbol|Cwd"`
- Windows build:
  - `$env:CGO_ENABLED='0'; go build -trimpath -buildvcs=false -o .\mcp-file-tools.exe .\cmd\mcp-file-tools`

## Review Handoff

Product owner review should check:

- language support helps real agent workflows;
- no fake semantic claims;
- structural edit remains out of core scope;
- Stage 4 symbol workflow removes manual line-number copying through `target_intent`;
- Phase 6 does not claim symbol mutation if Phase 5 write safety is absent;
- Phase 6 does not claim batch symbol workflow as core acceptance.

Engineering reviewer should check:

- parser dependency/build risk;
- parser query/range extraction proof, not only compile/parse smoke;
- dependency budget decision gate;
- parser adapter maintainability;
- exact/estimated honesty;
- byte-to-line and whole-line write-safety rules;
- selector ambiguity and fingerprint safety;
- concrete resolver DTOs and dry-run-only recommended write calls;
- resolver status/refusal fields vs top-level tool errors;
- cwd projection;
- test depth.

## Implementation Notes

Current implementation snapshot:

- Parser dependency: `github.com/odvcencio/gotreesitter v0.20.2`, vendored under `vendor/`, MIT license in `vendor/github.com/odvcencio/gotreesitter/LICENSE`.
- Parser proof covers JavaScript, TypeScript, TSX, Python, JSON, YAML and Svelte parse/range smoke fixtures with byte-to-line assertions.
- Pure-Go/offline proof passed with `GOFLAGS=-mod=vendor`, `GOPROXY=off`, `CGO_ENABLED=0`.
- Built binary: `mcp-file-tools.exe`, 49,356,288 bytes after Phase 6 repair.
- Phase 5 write-safety gate passed in the same working tree before Stage 4 target-intent recommendation code was enabled.
- `resolve_symbol_range(target_intent)` returns dry-run-only `recommended_write_call`; it never mutates and never recommends `dry_run=false`.
- Existing structured target insertion remains future-gated except `create_new`; existing Markdown and explicit non-structured plain text are the initial safe target modes.
- JSON/YAML source nodes are exact/readable but not write-safe by default because line-based tools cannot repair commas/indent/delimiters outside the selected range.
- JSON/YAML display names are path-like for agent navigation, including nested array/sequence keys, while direct source mutation remains conservative; public config kinds use the normalized plan contract (`document`, `object`, `array`, `property`, `value`, `stream`, `mapping`, `sequence`, `key`) and nested config sections stay hierarchical instead of duplicated as top-level sections.
- Repair coverage includes parser-backed `range + range_fingerprint` write recommendations, unbounded internal resolver candidates for late `symbol_ref` matches, decorated Python symbol ranges that include decorator lines, conservative JSX component detection, and line-aware parse warnings.
- Final repair coverage also includes JS/TS/TSX/JSX `import_block` grouping with import/export children, YAML scalar `value` nodes, replayable generic text continuation hints, generic text `enclosing_line` resolution, resolver tool-limiter coverage, and symlink refusal before write recommendations become ready.
- Engineering hardening coverage includes bounded nested `max_items` behavior, selector range/enclosing-line validation before synthetic fallback, and cwd sanitization for resolver refusal/proof reasons.
- Final engineering repair closes category-level `max_items` budgeting across imports/symbols/sections and rejects selector lines on empty files before any synthetic range fallback.
- Source-deletion safety hardening refuses `move` recommendations for nested Python symbols until enclosing-suite validity can be proven; JS/TS exported declarations remain discoverable as single resolver candidates even when import output is disabled.
- Svelte returns exact block/markup ranges with `parser_status="partial"` until nested script symbol extraction is implemented.
- Batch symbol workflows and direct selector inputs on range tools remain future-gated.

## Stop And Ask If

- Parser dependency proof is weak or skipped.
- Docs would overclaim exactness.
- Tests cannot cover malformed/ambiguous cases.
- Symbol write workflow still requires manual range copying because `resolve_symbol_range` output is not actionable.
