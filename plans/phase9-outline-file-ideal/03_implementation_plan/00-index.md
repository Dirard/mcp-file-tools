# Phase 9 Implementation Plan

## Stage 1: JS / TS / TSX Symbol Quality

1. Update JS-like symbol extraction so declarations are represented by canonical declaration symbols, not overlapping lexical and declarator symbols.
2. Do this in extraction/refinement, not only in final filtering, so public selectors point at the declaration range agents actually want.
3. Widen single-declarator variable/component ranges to the enclosing declaration, and to the wrapping non-source export statement when applicable.
4. Mark widened single-declarator declaration symbols write-safe only when parser-clean, whole-line safe, and delimiter-safe.
5. Keep multi-declarator variable declarations exact for navigation but not write-safe.
6. Add compact-profile filtering for local variable noise.
7. Compute `enclosing_items` from full/unfiltered items, or otherwise prove hidden local variables still appear for `enclosing_line`.
8. Ensure resolver uses full JS-like outline internally so full/filtered symbols still resolve.
9. Add focused TSX tests, including local variable hidden in default symbols but visible through `enclosing_line` and explicit filters.

## Stage 2: JSON / YAML Compact Agent Profile

1. Add config-profile filtering that distinguishes scalar leaf paths from container/navigation paths.
2. Keep scalar leaf key/property paths in default output; omit literal `value` nodes rather than omitting keys.
3. Collapse/promote useful children of omitted synthetic wrappers; do not lose structural navigation while hiding `.object`, `.mapping`, `.sequence`, or `["[]"]` wrapper names.
4. Reduce repeated item-level write-safety/refusal fields in compact config output while preserving selector facts.
5. Ensure compact emitted selectors resolve by both `symbol_ref` and `symbol_path` against the full/internal resolver.
6. Compute `enclosing_items` from full/unfiltered items, or otherwise prove hidden scalar/value nodes still appear for `enclosing_line`; `line_window` must also be able to surface details hidden by the default profile.
7. Ensure resolver uses full config outline internally.
8. Add focused JSON/YAML tests for compact key retention, value omission, wrapper omission/promotion, full profile, `line_window`, `enclosing_line`, and selector roundtrip.

## Stage 3: Preview Guidance

1. Update docs/tool descriptions so escape-sensitive edits require final read-back or `read_file` verification.
2. Add focused test coverage if the guidance is exposed through structured hints or validation output.

## Stage 4: Verification And Runtime

1. Run focused outline/resolve tests.
2. Run `go test -count=1 ./...`.
3. Build `mcp-file-tools.exe`.
4. Restart the MCP/watchdog and smoke-test the server.
5. Run product and engineering implementation review; repair and recheck if needed.
