# Phase 9 Technical Concept

## Core Contract

`outline_file(output_profile="agent")` should be compact enough for first-pass navigation. `output_profile="full"` should preserve the detailed parser-backed tree. The resolver must use an unfiltered/full outline internally so compact defaults do not make previously emitted selectors unresolvable.

Escape hatches are part of the contract: `full`, `kinds`, `name_contains`, `line_window`, and `enclosing_line` must be able to surface details hidden by the default compact profile. `enclosing_items` should be computed from the unfiltered/full outline, or an equivalent path that preserves hidden locals/leaves in the enclosing chain.

## JS / TS / TSX

1. Stop treating `lexical_declaration` and `variable_declaration` as outline symbols in the default extraction path.
2. Treat `variable_declarator` as the canonical variable/component item.
3. For a single declarator, widen the reported range/byte range to the enclosing declaration, and to the wrapping non-source export statement when applicable.
4. Keep multi-declarator declarations exact for navigation but not write-safe.
5. Hide local `variable` items in `agent` profile unless the caller asks with `full`, `kinds`, `name_contains`, or `line_window`.
6. Preserve top-level variables because they often represent exported constants, configs, schemas, hooks, and helpers.
7. Preserve component classification using JSX evidence plus PascalCase naming.

## JSON / YAML

1. Apply a config-specific `agent` profile before category finalization.
2. Keep scalar leaf key/property path nodes in default agent output because they are first-pass navigation targets.
3. Omit `value` nodes in agent profile as before.
4. Omit synthetic object/array/mapping/sequence wrapper nodes whose display paths add only `.object`, `.mapping`, `.sequence`, or `["[]"]`.
5. Keep document roots and useful array item containers such as indexed objects/mappings.
6. Collapse/promote useful children from hidden wrapper nodes without changing public selector paths in a way that breaks resolution.
7. Strip repeated item-level write-safety fields from compact config items while keeping selector write-safety facts intact.
8. Keep `resolve_symbol_range` internally on full config outline so hidden detailed paths and compact promoted paths both resolve by `symbol_ref` and by `symbol_path`.

## Preview / Read-Back

Do not rely on diff or boundary previews as exact escape-sensitive representations. Documentation and action hints should prefer final `read_file` or existing post-write read-back validation when the exact escaped text matters.

## Verification

- Focused TSX tests for duplicate removal, local-variable hiding, exported const components, full/explicit filter escape hatches, and resolve roundtrip.
- Focused TSX `enclosing_line` tests for hidden local variables.
- Focused JSON/YAML tests for compact default, full profile detail, synthetic container omission, scalar leaf key/property retention, value omission, `line_window`/`enclosing_line` escape hatches, and resolve roundtrip by `symbol_ref` and `symbol_path`.
- Preview/read-back documentation or test coverage for escape-sensitive verification guidance.
- `go test -count=1 ./...`
- Build and restart MCP/watchdog after implementation.
