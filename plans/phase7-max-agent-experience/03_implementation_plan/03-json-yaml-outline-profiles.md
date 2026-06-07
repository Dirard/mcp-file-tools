# Stage 3: JSON/YAML Outline Profiles

## Goal

Reduce JSON/YAML outline noise by default while preserving exact navigation and full-detail opt-in.

## Depends On

- Existing Phase 6 tree-sitter outline support.

## Touched Areas

- `filetoolsserver/handler/outline_file.go`
- `filetoolsserver/handler/outline_treesitter.go`
- `filetoolsserver/handler/outline_common.go`
- `filetoolsserver/handler/outline_schema.go`
- `filetoolsserver/handler/tool_types.go`
- docs/tests for `outline_file`

## Contract

Public `output_profile` values:

- `agent`: default compact structure profile.
- `full`: include leaf/value-heavy output.
- `fingerprint_only`: metadata-only existing behavior.
- `outline`: legacy alias for `agent`.

JSON/YAML leaf rules:

- `full` always includes leaf/value/property-detail nodes.
- `agent` omits low-value leaves by default.
- `agent` includes leaves when `kinds` explicitly includes leaf/value kinds.
- `agent` includes leaves when `line_window` intersects a leaf.
- `agent` includes leaves when `name_contains` directly matches a leaf path/name.

Stats:

- `outline_stats.omitted_leaf_items` or equivalent reports omitted leaf count.
- Existing total counts remain clear enough to understand truncation/filtering.

Canonical path grammar:

- JSON/YAML root is `document`.
- YAML multi-doc root uses `document[0]`, `document[1]`, etc.
- Dot notation only for simple identifier keys.
- Bracket notation for ambiguous keys: `document["foo:bar"]`.
- Sequence indexes use `[0]`, `[1]`.
- Keys are JSON string-escaped inside brackets and preserve Unicode/punctuation.

Write safety:

- JSON/YAML move/delete remains conservative by default.
- Output should distinguish read/navigation exactness from mutation safety.

## Steps

1. Update output profile parsing and aliases.
2. Add JSON/YAML profile-aware filtering before public output assembly.
3. Add leaf override handling for `kinds`, `line_window`, `name_contains`.
4. Replace JSON path display `$...` and YAML display with canonical `document...` grammar.
5. Preserve selector metadata compatibility where possible.
6. Add omitted leaf stats.
7. Update next recommended call to suggest `output_profile="full"` or narrow filters when leaves are omitted.
8. Add tests:
   - default agent profile is smaller than full;
   - full includes leaf/value nodes;
   - explicit `kinds` includes leaves in agent profile;
   - `line_window` and `name_contains` include directly matching leaves;
   - `:` / dot / space / Unicode keys stay distinct;
   - YAML multi-document root naming.

## Checks

- Existing Go/Markdown/generic outline tests still pass.
- JSON/YAML fixtures prove exact paths and lower noise.
- Schema docs list the final enum values.

## Handoff / Next Stage

After Stage 3, outline output is cleaner and exact. Stage 4 can connect search/discovery and write prep to this cleaner shape.

## Stop And Ask If

- Profile default change breaks an essential existing test in a way that cannot be updated without losing behavior.
- Canonical path grammar conflicts with selector resolution.
