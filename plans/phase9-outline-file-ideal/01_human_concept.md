# Phase 9: `outline_file` As A Primary Agent Navigation Tool

## Goal

Make `outline_file` useful by default, not merely filterable. An agent should be able to call it on a TSX component or JSON/YAML config and immediately see the important navigation targets without wading through duplicate declarations, local variables, scalar leaves, synthetic containers, or repeated write-safety refusals.

## User Pain

- TSX output is technically useful, but too noisy without filters.
- JSON/YAML output still feels too verbose and conservative even after leaf `value` items were hidden.
- Diff and boundary previews are helpful but not authoritative for escape-sensitive code; final `read_file` verification must remain part of the workflow.

## Product Direction

- Default `output_profile="agent"` becomes a high-signal navigation profile.
- `output_profile="full"` remains the detailed escape hatch.
- Existing narrowing controls (`kinds`, `name_contains`, `line_window`, `enclosing_line`) remain useful and can reveal details hidden by the default profile.
- `resolve_symbol_range` must keep resolving selectors from both default and full profiles.

## TSX / JS / TS Acceptance

- Do not emit overlapping `lexical_declaration` plus `variable_declarator` entries for one declaration.
- Top-level declarations use clean symbol names, not whole source lines.
- Exported const components appear as one clean `component` symbol.
- Local variables inside functions/components are hidden in default agent profile.
- Full profile or explicit filters can still expose local variables.
- Imports and source-bearing re-exports remain visible and grouped as before.
- Functions, classes, methods, interfaces, types, components, and useful top-level variables remain visible.

## JSON / YAML Acceptance

- Default agent profile keeps key/property navigation paths, including scalar leaf key/property names, and omits literal value spam.
- Synthetic container names such as `document.foo.object` and `document.list["[]"]` do not appear in default output.
- Array item containers such as `document.services[0]` may remain when they help navigation.
- Config write-safety stays honest: no JSON/YAML write recommendation is implied without delimiter/indent repair.
- Repeated item-level `write_safe=false` / `refusal_reason` noise should be reduced in default output; selectors may still carry exact write-safety facts.
- Full profile keeps detailed leaves and parser tree detail for agents that need it.

## Preview Caveat

Range-tool previews are bounded display previews. For escape-sensitive edits, agents must verify the result with post-write read-back or an explicit `read_file` call rather than trusting preview rendering alone.

## Non-Goals

- No LSP or framework-specific React semantic engine.
- No JSON/YAML comma, delimiter, or indentation repair engine.
- No redaction/safety policy change; default redaction remains off.
- No broad rewrite of range tools outside the preview/read-back guidance needed for this phase.
