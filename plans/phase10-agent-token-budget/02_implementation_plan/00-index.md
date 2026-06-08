# Phase 10 Implementation Plan Index

Goal:
Deliver measurable token efficiency for mcp-file-tools after Phase 9 without making agents less capable. The phase starts by measuring the full workflow token ledger, then implements only proven high-value compaction with schema/runtime parity and usefulness guardrails.

Scope:
- Add a token-budget measurement harness for canonical agent workflows.
- Freeze the post-Phase-9 baseline before runtime compaction.
- Rank cost centers across tool metadata, request JSON, response metadata/content, next-call hints, error guidance, retries, and detail/full fallback calls.
- Implement targeted compact/default projection only for measured high-cost surfaces.
- Keep detailed/debug data available through explicit detail/full paths where compact defaults omit it.
- Update output schemas, tool descriptions, README, TOOLS, and tests together.

Out of scope:
- Removing tools.
- Reintroducing safety-first redaction defaults.
- Removing exact ranges, fingerprints/proofs, `expected_version`, continuation, validation, read ranges, boundary/joiner diagnostics, or write verification.
- Broad breaking schema rewrite without a compatibility path.
- Optimizing `inspect_path` unless the ledger proves it materially contributes to workflow cost.

Must preserve:
- Phase 9 `outline_file` compact/full/include_write_metadata contracts.
- Same or fewer calls for every canonical workflow.
- Discovery quality for successful, no-result, ambiguous, and truncated cases.
- Stale-aware continuation and refresh hints.
- Range dry-run validation and observable escape-sensitive preview caveat.
- Runtime public JSON and advertised output schemas stay aligned.

Concept transferred into plan:
- User-visible result: agents spend fewer tokens in real repository workflows while still knowing the next safe/useful call.
- Behavior / contracts: default output is action-compact; detail/full paths preserve richer diagnostics; correctness fields are not noise.
- Acceptance: aggregate canonical workflow cost drops at least 15% versus post-Phase-9 baseline; at least two selected non-outline workflow families drop at least 10%; no workflow grows more than 3%; tool calls do not increase; no happy path requires detail/full fallback.

Plan file map:
- `01-ledger-and-baselines.md` -> measurement harness, canonical fixtures, baseline freezing, component ledger.
- `02-target-selection-and-contracts.md` -> choose implementation targets from measured data and lock compact/detail contracts.
- `03-runtime-projection-and-schema.md` -> implement targeted projections, schema parity, and compatibility behavior.
- `04-docs-tool-metadata-and-discovery.md` -> update tool descriptions/docs and verify discovery-friendly metadata.
- `05-verification-and-rollout.md` -> focused tests, full checks, MCP rebuild/restart, smoke, and review handoff.
- `06-canonical-workflow-fixtures.md` -> exact workflow fixture intents, derived-call fields, and minimum useful assertions.

Global decisions:
- Stage 1 is a real gate: do not change runtime defaults before the post-Phase-9 baseline is captured.
- Before the immutable baseline is captured, no production/runtime/registration/schema/docs changes are allowed. Only test harness code that observes current behavior may be added.
- Baseline must be stored as an immutable measured ledger fixture with exact per-workflow bytes, component buckets, and tool-call counts, not recomputed from the optimized runtime.
- Primary `tool_metadata` accounting counts the full registered tool list metadata once per canonical workflow. Per-tool metadata bytes are report-only for target selection unless a separate per-call metadata scenario is explicitly added.
- Optimize the largest measured contributors first. If response JSON is not dominant, optimize metadata/schema/request surfaces first.
- Projection is tool-family aware. Shared helper names are fine; a global omit-defaults serializer is not.
- Compact defaults may omit duplicate/non-action fields only when required schema and next-action/correctness contracts remain clear.
- If there is exactly one next-call hint, compact output may keep only `next_recommended_call`; if multiple distinct hints exist, compact output must preserve a list or equivalent unambiguous structure.
- Detail/full paths are required for fields removed from compact defaults unless a field is explicitly proven obsolete.

Global risks:
- False savings if tests compare optimized output to itself.
- Schema drift if `MarshalJSON` or projection changes without output schema updates.
- More reasoning or fallback calls if compact output hides discovery/correctness signals.
- Weak stale handling if fingerprints/proofs/expected versions are treated as noise.
- Write workflow risk if preview caveats or read-back guidance are compressed away.

Global checks:
- New token ledger tests with baseline constants/fixtures.
- Schema/runtime parity tests for every projected tool family.
- Workflow budget tests for happy, stale, no-result, truncated, ordinary failure, and write dry-run/read-back paths.
- Canonical workflow tests derive follow-up calls from actual returned compact/default fields; hardcoded follow-up inputs are allowed only for the first call of a workflow.
- Existing focused outline tests including Phase 9 projection behavior.
- Runtime tests prove omitted `redaction_mode` still defaults to `off` for affected read/search/write-preview paths.
- `go test -count=1 -parallel=1 ./filetoolsserver/handler -run "TestToken|TestBudget|TestProjection|TestOutlineFile|TestResolveSymbol|TestSchema"`
- `go test -count=1 -parallel=1 ./...`
- `go vet ./...`
- `go build -o .\mcp-file-tools.exe .\cmd\mcp-file-tools`
- Restart/watchdog MCP and smoke `workspace_inventory`, `glob_file_search`, `grep`, `read_file/read_files`, `outline_file`, `resolve_symbol_range`, `copy_ranges dry_run`.

Stop and ask if:
- Meeting the 15% aggregate threshold requires hiding next-action/correctness fields.
- A measured optimization requires breaking public schema without a compatibility path.
- The immutable baseline cannot be captured without changing production tool registration or schemas.
- Tool metadata/schema dominates token cost and fixing it requires changing public tool names or removing callable tools.
- Any step would require destructive git, publishing, network release work, live-system mutation, or raw secret inspection.
