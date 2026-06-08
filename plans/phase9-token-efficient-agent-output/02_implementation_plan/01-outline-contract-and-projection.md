# Stage 1: Outline Contract And Projection

Goal:
Make `outline_file(agent)` compact by default while preserving rich metadata through explicit profiles and keeping resolver/write workflows exact.

Depends on:
- Clean concept in `../01_concept.md`.
- Existing centralized item enrichment in `filetoolsserver/handler/outline_common.go`.
- Existing resolver in `filetoolsserver/handler/resolve_symbol_range.go`.
- Existing recursive output schema in `filetoolsserver/handler/outline_schema.go`.

Touched areas:
- `filetoolsserver/handler/refactor_types.go`
- `filetoolsserver/handler/outline_file.go`
- `filetoolsserver/handler/outline_common.go`
- `filetoolsserver/handler/outline_schema.go`
- `filetoolsserver/handler/resolve_symbol_range.go`
- `filetoolsserver/handler/agent_tools_test.go`

Steps:
1. Add `IncludeWriteMetadata bool json:"include_write_metadata,omitempty"` to `OutlineFileInput`.
2. Extend `outlineOptions` with `includeWriteMetadata bool`.
3. Set `includeWriteMetadata` during `HandleOutlineFile`:
   - true when `outputProfile == outlineProfileFull`;
   - true when `input.IncludeWriteMetadata`;
   - false for default `agent` and `outline` alias.
4. Keep parser construction rich:
   - do not remove `exactOutlineItemWithSelector`;
   - do not alter parser-specific item construction to save fields;
   - do not alter `outlineForSymbolResolution` internals to rely on compact output.
5. Add a response-boundary projection function, for example `projectOutlineOutput(output OutlineFileOutput, options outlineOptions) OutlineFileOutput`.
6. Replace the vague projection return shape with a precise public DTO contract:
   - keep rich `OutlineFileOutput` / `OutlineItem` as internal parser and resolver data unless implementation proves a cleaner rename is needed;
   - introduce public projected output/item DTOs, for example `OutlineFilePublicOutput` and `OutlinePublicItem`;
   - change `HandleOutlineFile` so its returned `output` is the public projected output type received by `server.go` and assigned to MCP `StructuredContent`;
   - keep parser functions returning rich internal outline data;
   - convert rich output to public output inside `HandleOutlineFile` before return;
   - update error/base output helpers for outline so errors and `fingerprint_only` also return the public output shape.
7. Public DTO fields use `omitempty` where compact profile can omit data:
   - do not rely on setting `OutlineItem.ID`, `Confidence`, or `RangeIsEstimated` to zero values, because current rich JSON tags/schema make them noisy or required;
   - the public schema must be generated or maintained from the same public DTO shape, not from stale rich item assumptions.
8. Validate the actual public structured JSON shape in tests:
   - either invoke the registered tool/server wrapper and inspect `CallToolResult.StructuredContent`;
   - or serialize exactly the public output value returned by `HandleOutlineFile`;
   - do not assert only on internal rich structs.
9. Apply projection after parser output, filtering, stats, truncation, and hints are complete, but before returning `structuredResultOnly`.
10. Implement three explicit recursive item modes:
   - `agent`: compact navigation only;
   - `agent + include_write_metadata`: compact navigation plus write/range proof metadata only;
   - `full`: old detailed item shape.
11. Implement default `agent` item projection:
   - always keep `kind`, `name`, `range`, `symbol_ref`, useful path context, depth, and children;
   - hide `selector`, per-item `range_fingerprint`, `byte_range`, `whole_line_range`, `write_safe`, `refusal_reason`, and heavy `metadata` in default agent;
   - hide `id` in default agent because `symbol_ref` is the action handoff;
   - hide default exact trust fields when `confidence == "exact"` and `range_is_estimated == false`;
   - show `confidence` and/or `range_is_estimated=true` only when the item is estimated or lower confidence and that changes agent caution.
12. Implement `agent + include_write_metadata` item projection:
   - keep compact navigation fields;
   - add `selector`, `range_fingerprint`, `byte_range`, `whole_line_range`, `write_safe`, and `refusal_reason` when present;
   - do not add heavy `metadata`, local parser noise, JSON/YAML leaf expansion, or `id` merely because write metadata was requested;
   - include non-default `confidence` / `range_is_estimated=true` caution fields.
13. Preserve `full` profile as old detailed behavior:
   - include `id`, full trust fields, selectors, byte ranges, write-safety fields, and metadata as before.
14. Add optional `outline_stats.omitted_field_counts` only if it helps agents understand projection without adding more noise than it saves. If added, keep it compact and profile-specific.
15. Update `OutlineFileOutputSchema`:
   - build it from the public projected output contract, not from internal rich-only expectations;
   - make compact-hidden item fields optional;
   - remove `id`, `confidence`, and `range_is_estimated` from required output fields unless the schema can model profile-specific requirements without lying;
   - ensure recursive `children` schema still works;
   - document optionality through schema descriptions if the schema helper supports it without large metadata bloat.
16. Update input schema tests so `include_write_metadata` is accepted.
17. Update same-outline hints:
   - truncation continuation hints preserve `include_write_metadata=true`;
   - invalid-profile retry hints do not invent write metadata, but preserve it if the user explicitly asked for it and the retry remains same-outline;
   - compact/full output profile continuation remains as today.
18. Update resolver preflight:
   - if `selector.range` is present and `selector.range_fingerprint` is omitted, validate the range against current `source_fingerprint` after the existing source-file fingerprint check;
   - keep existing stale check when `selector.range_fingerprint` is present;
   - preserve old full selector behavior.
19. Ensure resolver accepts compact forms:
   - `selector.symbol_ref` with `source_fingerprint`;
   - `selector.range` with `source_fingerprint`;
   - full selector with per-item fingerprint.
20. Add compact profile tests:
   - default `agent` item has `symbol_ref` and `range`;
   - default `agent` item omits selector/write/range proof metadata;
   - default `agent` item omits `id` and exact/default trust fields in actual marshaled JSON;
   - estimated/lower-confidence item includes caution fields;
   - `include_write_metadata=true` restores write/range proof metadata;
   - `include_write_metadata=true` does not restore heavy metadata or become `full`;
   - `full` restores old detailed metadata.
21. Add public JSON tests:
   - default `agent` public JSON omits `id`, `selector`, `range_fingerprint`, `byte_range`, `whole_line_range`, `write_safe`, `refusal_reason`, heavy `metadata`, and exact/default trust fields;
   - `include_write_metadata=true` public JSON includes write/range proof metadata but still omits heavy metadata and `id`;
   - `full` public JSON includes the detailed compatibility fields;
   - assertions inspect public structured content, not internal rich structs.
22. Add hint-preservation tests:
   - truncated `outline_file(agent, include_write_metadata=true)` returns a continuation hint preserving `include_write_metadata=true`;
   - following that hinted call still yields selector/range proof metadata.
23. Add resolver roundtrip tests:
   - compact item `symbol_ref + source_fingerprint` resolves to the exact range;
   - compact item `range + source_fingerprint` resolves without per-item `range_fingerprint`;
   - stale `source_fingerprint` still fails before resolution;
   - old full selector still resolves.

Checks:
- Focused tests for default compact profile and full/write metadata profile.
- Focused tests for resolver compact symbol_ref and range fallback.
- Focused tests for `include_write_metadata` hint preservation.
- Schema tests for optional profile-projected fields.

Handoff / next stage:
After compact/full/resolver contracts pass focused tests, Stage 2 can measure real normalized response and workflow byte savings.

Stop and ask if:
- Compact output cannot preserve enough context for common navigation without keeping a hidden field.
- Backward compatibility needs a staged rollout instead of immediate compact default.
