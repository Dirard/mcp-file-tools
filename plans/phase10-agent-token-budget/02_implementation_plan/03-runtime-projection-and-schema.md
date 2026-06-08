# Stage 3: Runtime Projection And Schema

Goal:
Implement targeted token reductions while keeping runtime JSON, output schemas, and agent workflows aligned.

Depends on:
- Stage 2 selected contracts.
- Stage 1 baseline constants/fixtures.

Touched areas:
- Runtime handler files for selected targets.
- Output schema builders for tools with custom projection.
- `filetoolsserver/server.go` if tool metadata/descriptions are selected.
- Tests in `filetoolsserver/handler`.

Steps:
1. Add projection helpers next to selected tool-family construction, following the Phase 9 outline pattern but avoiding hidden schema drift.
2. Do not rely on `omitempty` alone for semantic projection when output schemas still advertise required fields.
3. For each projected family:
   - construct rich internal output as today;
   - apply public projection only at the response boundary;
   - keep direct Go structs usable for internal tests where needed;
   - add custom output schema or schema constraints when public JSON differs from raw struct.
   - validate projected compact/default, detail/full, stale/error/no-result outputs against the schema actually registered on the MCP tool.
4. Implement next-call hint dedupe carefully:
   - if exactly one hint exists, compact may emit primary only;
   - if multiple distinct hints exist, emit the list or equivalent unambiguous structure;
   - detail/full may emit both primary and list for compatibility.
5. Preserve stale and continuation behavior:
   - `expected_version` must remain in continuation inputs where currently needed;
   - fingerprints/proofs must remain available for stale checks;
   - stale mismatch outputs must keep refresh hints.
6. Preserve write dry-run trust:
   - validation status and read-back windows remain available;
   - boundary warnings and joiner diagnostics remain available;
   - fingerprints-for-next-write remain available;
   - escape-sensitive caveat remains observable in compact/default output.
7. If tool metadata/schema is selected:
   - centralize descriptions only if it reduces duplication without making discovery worse;
   - keep all 14 callable tools discoverable;
   - avoid long profile explanations in every description; put detail in README/TOOLS instead.
8. Update or add schema tests:
   - projected runtime JSON contains no fields that schema forbids;
   - schema does not mark compact-hidden fields as required;
   - detail/full schema paths expose richer fields where promised.
   - parity checks use the registered `Tool.OutputSchema`, not only standalone helper schemas.
9. Update budget tests:
   - compare optimized workflow bytes to stored post-Phase-9 baselines;
   - enforce aggregate >=15%;
   - enforce at least two selected non-outline workflow families each drop >=10%;
   - enforce no workflow >3% growth;
   - enforce tool calls <= baseline;
   - enforce no required full/detail fallback for happy paths.
   - enforce follow-up calls are derived from actual compact/default output fields.
10. Add runtime regression tests for default redaction:
   - omitted `redaction_mode` remains `off` for `read_files`;
   - omitted `redaction_mode` remains `off` for `grep`;
   - omitted `redaction_mode` remains `off` for range dry-run previews/validation where applicable;
   - docs/schema wording matches runtime behavior.
11. Keep rejected candidates untouched. Do not opportunistically compact unrelated outputs.

Checks:
- Focused projection/schema tests pass.
- Focused workflow budget tests pass.
- Existing Phase 9 outline tests pass unchanged or with only contract-aligned updates.
- No test relies on recomputing baseline from optimized output.
- Registered tool output schemas validate projected public JSON.
- Default redaction-off regression tests pass for affected tools.

Handoff / next stage:
Stage 4 updates docs/tool metadata to match exactly what Stage 3 shipped.

Stop and ask if:
- A threshold cannot be met without removing correctness data.
- Runtime/schema parity would require large SDK-level changes.
- Tool metadata compaction makes a tool less discoverable in local/generic-agent smoke.
