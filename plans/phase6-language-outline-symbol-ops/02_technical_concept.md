# Phase 6 Language-Aware Outline And Symbol Operations Technical Concept

concept_version_label: phase6-language-outline-symbol-ops-v1
status: clean_srs_reviewed_ready_for_implementation

## Technical Direction

Phase 6 extends `outline_file` into a language-aware structure service and connects exact outline ranges to safe file operations.

The architecture should avoid turning `mcp-file-tools` into a language server:

- one request, one structured response;
- no project-wide index;
- no background parser cache required;
- no type checking;
- no cross-file semantic edits;
- no broad AST rewrite/format engine in the core phase;
- parser output must be honest about exactness.

The likely implementation center is a parser adapter layer that normalizes language-specific structures into existing `OutlineItem`.

## Current Baseline

Current `outline_file` has:

- `language=auto`;
- exact Markdown ATX sections;
- exact Go AST declarations;
- generic text outline for other text files;
- fingerprint output;
- `imports`, `symbols`, `sections`;
- `line_window`, `name_contains`, `kinds`, `max_items`, `max_depth`;
- truncation hints;
- cwd-aware path projection.

Current write tools are line-based:

- `copy_ranges`;
- `move_ranges`;
- `copy_ranges_batch`;
- `move_ranges_batch`.

Phase 6 should reuse their range/fingerprint safety rather than bypass it. Core Phase 6 symbol-write UX targets single `copy_ranges` / `move_ranges` recommendations; batch symbol workflows remain future-gated unless a later reviewed SRS accepts a bounded batch selector contract.

## Parser Adapter Model

Add a language parser interface conceptually shaped like:

```go
type OutlineParser interface {
    Language() string
    Parse(ctx context.Context, info fileTextInfo, options outlineParseOptions) (OutlineParseResult, error)
}
```

The parser returns normalized extracted items/status only. The central `outline_file` handler keeps ownership of output assembly, filtering, truncation, cwd projection and next-call hints.

SRS may choose exact interfaces differently. Requirements:

- language detection is centralized;
- each parser declares `parser_scope`;
- each parser can return warnings and parser status;
- each parser normalizes output into `OutlineItem`;
- each parser can refuse large files or fall back honestly;
- parser output remains bounded by `max_items` and `max_depth`.

## Language Detection

`language=auto` should recognize:

- `.go` -> Go;
- `.md`, `.markdown` -> Markdown;
- `.ts`, `.tsx`, `.js`, `.jsx`, possibly `.mjs`, `.cjs` -> TypeScript/JavaScript family;
- `.svelte` -> Svelte;
- `.py` -> Python;
- `.json`, `.jsonc` if supported -> JSON family;
- `.yaml`, `.yml` -> YAML.

Requested language values should have stable aliases:

- `typescript`, `ts`, `tsx`;
- `javascript`, `js`, `jsx`;
- `svelte`;
- `python`, `py`;
- `json`;
- `yaml`, `yml`;
- existing `go`, `markdown`, `md`, `auto`.

Unsupported requested language should return a structured error with a precise `error_code`.

## Parser Dependency Strategy

Phase 6 should prefer proven parsers over ad hoc regex for exact claims.

Possible directions for SRS:

- tree-sitter-based parsers for TS/JS/TSX/Python/Svelte where Go bindings and build constraints are acceptable;
- native Go packages for JSON/YAML AST/range extraction where practical;
- lightweight parser libraries only when they expose reliable byte/line ranges;
- generic outline fallback when exact parsing is not available.

Important product rule:

- regex scanning may create hints with `confidence="estimated"`;
- regex scanning must not produce `range_is_estimated=false`;
- symbol-based writes must require exact ranges unless SRS explicitly defines a safe estimated-range mode.

## Normalized Item Contract

Existing `OutlineItem` can remain the core schema.

Phase 6 may add:

```go
SymbolRef string `json:"symbol_ref,omitempty"`
```

or a nested selector:

```json
{
  "selector": {
    "language": "python",
    "kind": "function",
    "name": "load_config",
    "path": ["module", "load_config"],
    "range_fingerprint": { "...": "..." }
  }
}
```

Required properties:

- selector is scoped to one file;
- selector is not a stable cross-version global ID;
- selector includes or references the fingerprint used to compute range;
- duplicate/ambiguous names are distinguishable by path/range;
- cwd paths are not embedded as absolute paths under `cwd_id`.

## Language Output Requirements

### TypeScript / JavaScript / React

Expected item kinds:

- `import_block`;
- `import`;
- `export`;
- `function`;
- `class`;
- `method`;
- `interface`;
- `type`;
- `variable`;
- `component` when confidently recognized.

React component detection can be conservative:

- exported function returning JSX;
- PascalCase function/class component;
- const assigned to arrow/function with JSX body when parser can detect it.

If JSX detection is uncertain, use `function` or `variable` with metadata, not fake `component`.

### Svelte

Expected item kinds:

- `module_script`;
- `script`;
- `style`;
- `markup`;
- nested `import`, `function`, `variable` symbols from script blocks when parser support exists.

Svelte can start with exact block ranges even if nested TS/JS parsing is phased later.

### Python

Expected item kinds:

- `import`;
- `function`;
- `class`;
- `method`;
- maybe `decorator` metadata.

Class children should contain methods when range nesting is exact.

### JSON

Expected item kinds:

- `object`;
- `array`;
- `property`;
- `value`.

Names should be JSON paths or key names. Ranges should identify complete values where parser can provide byte ranges.

### YAML

Expected item kinds:

- `document`;
- `mapping`;
- `sequence`;
- `key`;
- `value`;
- anchors/aliases as metadata if parser exposes them.

Parser should handle multi-document YAML or explicitly report scope limitations.

## Enclosing Symbol Lookup

Phase 6 should support agent navigation from a known line to an enclosing structure.

Possible shapes:

- `outline_file` with `line_window` returns enclosing symbols/sections around the window;
- new input `enclosing_line`;
- helper `resolve_symbol_range`.

Requirements:

- if multiple enclosing symbols exist, return innermost and path to parents;
- do not drop parent context when `max_depth` allows it;
- output must identify whether range is exact or estimated.

## Symbol-To-Range Operations

Phase 6 should let agents use exact parser ranges without manually copying line numbers.

Possible implementation choices:

1. Add selector fields to write tools:

```json
{
  "source_selectors": [
    { "symbol_ref": "python:function:load_config:42" }
  ]
}
```

2. Add a helper that resolves selectors to line ranges:

```json
{
  "target_file": "internal/config.py",
  "selectors": [
    { "kind": "function", "name": "load_config" }
  ]
}
```

Then existing write tools consume concrete `ranges`.

Root preference for concept: selector resolution helper is safer if direct write-tool schema would become too complex. SRS should choose based on implementation risk.

Required behavior:

- source fingerprint must match the outline snapshot;
- ambiguous selector returns a structured error;
- estimated ranges are refused for writes by default;
- final output echoes resolved concrete ranges;
- diff preview and validation from Phase 5 apply.

## Structural Edit Gate

Structural edit is not a default Phase 6 implementation target. The core target is parser-backed outline plus exact selector-to-range resolution and symbol-aware copy/move over the existing range-transfer engine.

Phase 6 may leave a design-ready contract for structural edit, but actual mutation operations should be a later gated stage unless root/user explicitly promotes a narrow subset after parser acceptance.

Possible later operations:

- `replace_symbol_range`;
- `insert_before_symbol`;
- `insert_after_symbol`;
- `insert_import`;
- `replace_config_value` for JSON/YAML path.

Required safeguards for any later promoted subset:

- exact parser support for the language/operation;
- explicit target file;
- fingerprint precondition;
- dry-run diff preview;
- post-write validation;
- backup support;
- refusal on ambiguity, parse failure or estimated ranges.

Structural edit must not:

- resolve cross-file references;
- rename usages;
- infer types;
- format whole projects;
- modify package manager/build files unless explicitly targeted.

SRS should treat structural edit as out of core scope unless a reviewed Phase 6 plan explicitly says otherwise.

## Parser Status And Errors

Phase 6 should make parser outcomes machine-readable.

Candidate statuses:

- `ok`;
- `partial`;
- `parse_error`;
- `unsupported_language`;
- `outline_parse_threshold_exceeded`;
- `generic_fallback`;
- `estimated_only`.

Candidate error codes:

- `unsupported_language`;
- `parser_dependency_unavailable`;
- `parse_error`;
- `ambiguous_symbol`;
- `symbol_not_found`;
- `symbol_range_estimated`;
- `symbol_fingerprint_mismatch`;
- `structural_edit_unsupported`;
- `structural_edit_validation_failed`.

Exact names belong in SRS.

## Cwd And Path Projection

New path-bearing fields must be tested:

- selector source file fields;
- recommended next inputs;
- symbol operation outputs;
- parser warnings;
- structural edit diffs and validation output.

With `cwd_id`, all file paths except `cwd` metadata remain cwd-relative.

## Testing Direction

SRS should include fixtures for:

- TS imports/functions/classes/interfaces/types/components;
- TSX/JSX React components;
- Svelte module script, instance script, style, markup;
- Python imports/classes/functions/methods/decorators;
- JSON nested object/array values and parse errors;
- YAML mappings/sequences/multi-doc/anchors if supported;
- existing Go and Markdown regression;
- generic fallback regression;
- `line_window`/enclosing symbol;
- `name_contains`, `kinds`, `max_items`, `max_depth`;
- ambiguous selector refusal;
- symbol fingerprint mismatch;
- symbol-based copy/move happy path;
- structural edit contract checks only if included as a gated later subset;
- cwd projection;
- malformed files.

## Documentation Direction

Docs must explain:

- which languages are exact and which are fallback;
- what `confidence` means;
- how to use outline ranges with write tools;
- how symbol selectors relate to fingerprints;
- that Phase 6 is not LSP, semantic rename, or a general AST rewrite engine;
- examples for each supported language family.

## Stop And Ask If

- Exact parser support requires a dependency with unacceptable build/runtime cost.
- A language can only be represented through brittle regex while claiming exact ranges.
- Structural edit would require project-wide semantic knowledge.
- Symbol operation would mutate without Phase 5-style diff preview/validation.
- New fields cannot preserve cwd path projection.
- Supporting all requested languages at once would make tests shallow or parser honesty weak.
