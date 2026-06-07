# Phase 8 Technical Concept: Outline, Discovery, Inventory, Joiner AX

## Technical Goal

Improve agent usefulness without broadening the mutation model. Phase 8 should reduce noisy output, make next actions more reliable, improve parser-backed outlines for common languages, clarify inventory completeness semantics, and make range joiners explain their visual effect before apply.

## Current Baseline

- `outline_file` supports Markdown, Go, JavaScript/JSX, TypeScript/TSX, Python, JSON, YAML, Svelte and generic text.
- Java is present in the vendored `gotreesitter` grammar files, but it is not routed as an `outlineLanguage`.
- JS/TS/TSX/Python are parser-backed but less curated than Go/Markdown.
- JSON/YAML have `agent/full/fingerprint_only` profiles and exact config path work from Phase 7.
- Tool descriptions are long and may be unfriendly to lazy discovery/truncation.
- `workspace_inventory` has separate `summary.complete` and `continuation.complete` semantics that are easy to misread.
- Range tools expose `joiner_effect`, `boundary_preview`, `diff_previews`, and validation, but joiner diagnostics do not explicitly account for existing blank lines at range boundaries.

## Technical Scope

### 1. Discovery-Friendly Tool Metadata

Audit descriptions in:

- `filetoolsserver/server.go`
- `server.json`
- `TOOLS.md`
- `README.md`
- tests that assert server instructions/descriptions

Implement a shorter two-layer description style:

- top-level server instructions: one compact line per tool with strongest use case;
- individual tool descriptions: concise purpose, input mode, output contract, key next-call behavior, and the few non-obvious pitfalls.

Descriptions should include high-signal aliases only when useful:

- `outline_file`: symbols, sections, AST-ish outline, selectors, structure
- `glob_file_search`: find files, path discovery, filename search
- `grep`: content search, structured rg replacement
- `read_files`: batch read, compact context
- `resolve_symbol_range`: selector to line ranges, dry-run write prep
- range tools: diff preview, joiner, validation, backups

Avoid stuffing full docs into descriptions. Detailed examples remain in `TOOLS.md`.

### 2. Outline Quality By Language

#### Shared Outline Requirements

For JS, TS, TSX, Python, Java, JSON and YAML:

- stable symbol classification;
- compact default `agent` profile;
- exact line/byte ranges where parser supports it;
- selectors should round-trip through `resolve_symbol_range`;
- non-actionable `write_safe=false` diagnostics should be reduced or summarized in default profile;
- `full` profile may expose detailed diagnostics/leaf nodes.

Selector contract:

- every selector emitted by `outline_file` in `agent` or `full` profile must resolve through `resolve_symbol_range`;
- if default `agent` profile omits nested/noisy items, resolver must still use full-enough extraction internally so `full` selectors remain valid;
- compact profile changes must not create fake selectors or selector paths that cannot be resolved after a fresh outline.

Default profile noise contract:

- `agent` profile is for next action, not complete AST dumping;
- repeated `write_safe=false` should become a summary/count/reason when the reason is non-actionable at default depth;
- `full` profile remains the opt-in place for leaf values, detailed write safety diagnostics and all parser-visible navigation items.

#### JS/TS/TSX

Improve `treeSitterExtractor` classification:

- distinguish imports from exports;
- do not group non-import exported declarations into `import_block`;
- `import_block` should contain real import declarations only; re-exports should be separate `re_export`/`export` items unless tests prove source-bearing re-export grouping improves AX without hiding code declarations;
- support functions, arrow functions, classes, methods, interfaces/types where grammar has nodes;
- TSX components should be components only when component-like evidence exists;
- `export default function/class`, `export const Component = ...`, and re-exports should have predictable kind/detail.

Implementation likely touches:

- `treeSitterExtractor.symbolSpec`
- JS/TS symbol spec helpers
- export child extraction/grouping logic
- tests in `agent_tools_test.go` or split outline tests

#### Python

Improve classification compactness:

- functions/classes/methods/decorated definitions;
- imports/import_from;
- nested symbols remain marked but should not overwhelm default output;
- decorators should not create misleading ranges.

#### Java

Add Java as a first-class outline language:

- `outlineLanguageJava`
- `.java` extension routing
- `grammars.JavaLanguage()` if exported by vendored dependency/build tags; otherwise add/configure dependency with tests.
- Java symbol extraction must cover package, imports, classes, interfaces, enums, records, annotations, methods, constructors and fields as the minimum useful baseline.
- `resolve_symbol_range` should include Java in parser-backed languages and structured target detection.

Java write safety can be conservative initially, but read/navigation must be parser-backed and useful. If the ordinary Windows Go build cannot expose the Java grammar, the plan must stop at an explicit build/dependency gate instead of silently falling back to generic text.

#### JSON/YAML

Keep Phase 7 exact path identity. Improve output compactness:

- default `agent` profile should show containers and key/property paths, not noisy value leaves;
- summarize omitted leaves and write-safety reasons without repeating `write_safe=false` everywhere;
- `full` profile preserves all leaf details and selectors;
- sequence indexes and literal keys remain exact.

### 3. Workspace Inventory Completeness Semantics

Current ambiguity:

- `continuation.complete=true` may mean the returned page is complete.
- `summary.complete=false` may mean summary coverage is limited by depth/limit/continuation.

Change output semantics to be self-explanatory:

- keep backwards-compatible fields if needed, but add clearer names;
- canonical additive fields:
  - `page_complete`
  - `page_incomplete_reason`
  - `summary_coverage_complete`
  - `summary_incomplete_reason`
  - `tree_scan_complete`
  - `scan_scope`
  - `continuation.page_complete`
- messages/reasons should say whether the next action is page continuation, deeper scoped inventory, or glob/grep.
- existing `summary.complete` and `continuation.complete` stay as compatibility aliases, but docs/descriptions should steer agents to the canonical fields.

Tests must cover:

- page complete but summary incomplete;
- page incomplete with continuation hint;
- max_depth-limited summary;
- limit-limited page.

### 4. Joiner UX In Range Tools

Improve joiner diagnostics before apply:

- show requested/normalized joiner;
- show configured newline bytes;
- show added newlines between source ranges;
- show existing boundary newlines for:
  - source range joins;
  - target insertion boundary before/after;
  - replace boundary where relevant;
- explicitly flag when `blank_line` will create more than one visual empty line due to already-empty edge lines.

Possible structured fields:

- `joiner_effect.visual_blank_lines_between_ranges`
- `joiner_effect.existing_trailing_blank_lines`
- `joiner_effect.existing_leading_blank_lines`
- `joiner_effect.boundary_warnings`
- or a new `joiner_boundary_preview` if cleaner.

Preferred DTO shape should be settled in the plan before implementation:

- newline style / newline bytes used for normalization;
- per source range join boundary: trailing blank lines on left, leading blank lines on right, inserted newline count and resulting visual blank lines;
- target placement boundary: blank/newline state before and after inserted payload where applicable;
- warning codes for unexpected visual whitespace, especially `blank_line` plus already-empty edge lines.

Keep dry-run output the source of truth. Do not make apply implicit or heuristic.

## Compatibility

- Do not remove existing public fields unless tests/docs prove they are internal-only.
- Prefer additive schema fields for inventory/joiner clarity.
- Keep old `summary.complete` if needed, but add clearer replacements and update docs/tool descriptions to steer agents to the clear fields.
- Java language addition should not change auto detection for existing languages.

## Verification Expectations

Minimum checks:

- black-box agent-facing probes for discovery, outline noise, inventory clarity and joiner dry-run clarity;
- focused outline tests for JS, TS, TSX, Python, Java, JSON, YAML;
- resolver round-trip tests for new Java and repaired JS/TS/TSX selectors;
- workspace inventory semantics tests;
- joiner dry-run diagnostics tests;
- tool description/server instruction tests;
- schema/docs consistency checks for added inventory/joiner fields and shortened metadata;
- `go test -count=1 ./filetoolsserver/handler`;
- `go test -count=1 ./...`;
- targeted `-race` for outline/search/write/inventory areas;
- `go build`;
- MCP restart/watchdog smoke after clean review.

## Risks

- Tree-sitter grammar node names differ by language and may need empirical tests.
- Java grammar may require build-tag/dependency adjustment.
- Over-pruning outline output could hide useful selectors. Default `agent` should be compact, but `full` must remain complete.
- Inventory field renaming can confuse existing users if not additive.
- Joiner diagnostics can become noisy; default output should focus on actionable boundary risks.
