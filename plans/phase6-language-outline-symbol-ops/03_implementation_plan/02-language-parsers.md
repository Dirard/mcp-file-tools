# Stage 2: Language Parsers

## Goal

Implement language-aware outline extraction with honest ranges and parser statuses.

## Depends On

- Stage 1 parser adapter and dependency proof.

## Touched Areas

- existing `outline_go.go`, `outline_markdown.go`, `outline_generic.go`
- new language parser files:
  - `outline_treesitter.go`
  - `outline_jsts.go`
  - `outline_svelte.go`
  - `outline_python.go`
  - `outline_json.go`
  - `outline_yaml.go`
- outline tests and fixtures

## Shared Extraction Rules

1. Convert parser byte/range points to 1-based inclusive line ranges.
2. Preserve parser byte ranges in selector diagnostics when available.
3. Include comments/decorators/import docs in the symbol range when parser nodes support it and behavior is deterministic.
4. Set `confidence="exact"` and `range_is_estimated=false` only for parser-backed exact node ranges.
5. Set selector only for exact items.
6. Set `selector.write_safe=true` only when the exact parser node expands to whole lines without including unrelated sibling tokens.
7. If a parser can identify a symbol but not reliable end range, emit item with `confidence="estimated"`, `range_is_estimated=true`, no selector.
8. If a parser reports syntax errors but still returns partial tree, set `parser_status="partial"` and warning with parse-error location.
9. If a parser fails completely, return `parser_status="parse_error"`, fingerprint, warnings/error, no fake symbols.
10. Apply filters after parent/child relationships are built so enclosing context can be retained.
11. Binary or undecodable inputs must not enter language parsers. They return the existing honest binary/undecodable refusal or generic fallback behavior without fake structure and without panic.

## Byte-To-Line And Write-Safety Rules

Current range-transfer tools mutate whole lines. Parser extraction therefore has two levels of exactness:

- `range_is_estimated=false`: parser-backed exact range is suitable for navigation/read.
- `selector.write_safe=true`: parser-backed exact range is also safe for line-based copy/move.

Byte-to-line conversion algorithm:

1. Build a byte-offset line index from the original file bytes before UTF-8 decoding transformations. Treat `\r\n` as one line break and do not split the CR and LF into separate line boundaries.
2. `StartByte` maps to the 1-based line that contains that byte. If `StartByte` is exactly the file length for an empty trailing range, reject write-safety and use the nearest readable diagnostic line only.
3. `EndByteExclusive` maps to the line containing the previous byte (`EndByteExclusive - 1`).
4. If `EndByteExclusive` is exactly at the start byte of a later line, the resulting inclusive line range ends at the previous line.
5. If `EndByteExclusive` points just after `\r` in a CRLF pair, normalize to the full CRLF boundary before applying rule 3, or reject write-safety if the parser range bisects the newline pair.
6. A node ending at column 0 of the next line does not include that next line in `SourceLineRange`.
7. A node ending at final trailing newline includes the previous content line only unless parser bytes include owned trailing decoration that is documented for that language.
8. Parser ranges that cannot be converted without ambiguity return exact read/navigation diagnostics but `write_safe=false`.

Write-safe line expansion is allowed only when:

- the node starts at the first non-whitespace token on `start_line`, or preceding bytes on that line are indentation/allowed decoration owned by the node;
- the node ends before the line break on `end_line`, with only whitespace/trailing delimiter owned by the node after it;
- expanding to `start_line..end_line` does not include sibling declarations, sibling object properties, adjacent array elements or unrelated statements.
- for delimiter-separated languages and node kinds, deleting or moving the selected line range does not require modifying a comma, semicolon, colon, indentation marker, block delimiter or other syntax token outside the selected range.

Same-line fragments are exact but not write-safe unless they occupy the whole line. Required fixtures:

- JSON one-line object with multiple properties;
- YAML one-line mapping/sequence when parser supports it;
- JavaScript/TypeScript one-line file with multiple declarations/statements.
- JSON/YAML/JavaScript/TypeScript first/middle/last property or declaration cases with trailing commas/delimiters.
- CRLF fixtures, trailing-newline fixtures, node-ending-at-column-0 fixtures and same-line sibling fixtures for byte-to-line conversion.

These fixtures must prove the item can be read/resolved exactly, but `resolve_symbol_range` refuses mutation with `symbol_range_not_write_safe` unless a future byte-range write tool exists.

## TypeScript / JavaScript / TSX / JSX / React

Parser scope:

- `treesitter_javascript_typescript_declarations`

Extract item kinds:

- `import_block`
- `import`
- `export`
- `function`
- `class`
- `method`
- `interface`
- `type`
- `variable`
- `component`

Rules:

- Imports group into import block when adjacent at top-level.
- Export metadata is attached to exported declaration.
- React component is conservative:
  - PascalCase function or class with JSX in body;
  - exported PascalCase declaration;
  - const/arrow function with JSX return when parser tree confirms JSX.
- If JSX detection is not exact, use `function` or `variable` with metadata `component_candidate=true`.
- Type-only declarations are symbols but not components.

## Svelte

Parser scope:

- `treesitter_svelte_blocks`

Extract item kinds:

- `module_script`
- `script`
- `style`
- `markup`
- nested JS/TS imports/functions/variables when injection parser support is proven.

Rules:

- Top-level Svelte blocks are exact if parser returns ranges.
- Nested script symbols use JS/TS extraction with ranges offset to source file lines.
- If nested injection is not reliable, return exact block ranges only and parser status `partial`.

## Python

Parser scope:

- `treesitter_python_declarations`

Extract item kinds:

- `import`
- `function`
- `class`
- `method`
- decorators as metadata.

Rules:

- Class children include methods.
- Decorator lines are included in function/class ranges when parser exposes them.
- Nested functions are included with path nesting.

## JSON

Parser scope:

- `treesitter_json_paths`

Extract item kinds:

- `object`
- `array`
- `property`
- `value`

Rules:

- Names are JSON path strings, e.g. `$.scripts.test` or `$.dependencies[0]`.
- Top-level object/array is a section.
- Nested properties are symbols/sections depending on depth and value kind.
- Parse errors return line/column warning.

## YAML

Parser scope:

- `treesitter_yaml_paths`

Extract item kinds:

- `document`
- `mapping`
- `sequence`
- `key`
- `value`

Rules:

- Names are YAML path strings, e.g. `services.api.image`.
- Multi-document files get document items with document index.
- Anchors/aliases/tags appear in metadata when parser exposes them.
- If parser lacks exact end ranges for a value, mark estimated and omit selector.

## Go And Markdown Regression

Steps:

1. Adapt Go parser to registry without changing output.
2. Adapt Markdown parser to registry without changing output.
3. Keep generic fallback behavior unchanged.
4. Ensure Go/Markdown selectors are added only for exact items and do not change old required fields.

## Acceptance

- Each language returns useful items with expected kinds.
- Malformed file tests produce honest parser status.
- Exact items have selectors; estimated items do not.
- Selector `write_safe` is true only for whole-line ranges.
- One-line JSON/JS/YAML fragments are exact read/navigation items but not mutation-safe when whole-line expansion would include siblings.
- `line_window`, `name_contains`, `kinds`, `max_items`, `max_depth` still work.
- Go/Markdown snapshots do not regress except additive selector metadata.
- Binary/undecodable files do not get fake structure and do not crash parser dispatch.

## Checks

- Fixtures for each language with line-number assertions.
- Byte offset to line range assertions for at least one item per parser-backed language.
- One-line JSON/JS/YAML non-write-safe fixtures.
- Parser status tests: ok, partial, parse_error, generic fallback.
- Binary and undecodable fixtures for parser dispatch refusal/fallback.
- Item count/truncation tests.
- Filter tests for name/kind/window/depth.
- Cwd tests for file paths in warnings/recommended calls.

## Handoff / Next Stage

After Stage 2, the outline output can feed enclosing symbol and selector resolution.

## Stop And Ask If

- A language parser can only provide regex-like guesses.
- A parser dependency gives byte ranges but no line mapping sufficient for inclusive ranges.
- Parser extraction makes outputs too large or slow under existing limits.
