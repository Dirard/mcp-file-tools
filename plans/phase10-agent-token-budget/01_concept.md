# Phase 10: Agent Token Budget Concept

Goal:
Make mcp-file-tools cheaper across common agent workflows after the Phase 9 `outline_file` compact-output win, while preserving the practical experience: agents still know what happened, what to call next, and how to verify writes without guessing. Phase 10 starts measurement-first: build a token ledger, rank the real cost centers, then optimize only the surfaces proven to matter.

User / maintainer:
The primary user is an LLM coding agent using these tools repeatedly inside a repository. The maintainer needs a response contract that is compact by default, predictable, measurable, and still debuggable through explicit richer modes.

Scope:
- Build a measurement harness before runtime compaction. The harness must separate tool descriptions/schemas, request payloads, response metadata, response content, next-call hints, error/no-result guidance, retries, and full/detail fallback calls.
- Measure and optimize high-token response surfaces beyond `outline_file`: `read_file` / `read_files` coverage and continuation, `grep` results and file groups, `glob_file_search`, `workspace_inventory`, `inspect_path`, `resolve_symbol_range`, and range-tool dry-run previews.
- Prioritize implementation from measured top contributors. `inspect_path` and any other low-cost surface stay candidate-only unless the ledger shows they materially affect workflow cost.
- Reduce duplicate next-call hints, repeated proof fields, empty/default fields, verbose counters, and compatibility text where they do not change the next useful agent action.
- Preserve structured JSON as the primary product surface; do not replace useful structured fields with prose.
- Add explicit compact/detail/full behavior where needed instead of deleting advanced information outright.
- Keep token-efficiency tests workflow-based, not only single-response snapshots.

Out of scope:
- Do not remove tools.
- Do not remove exact line ranges, top-level fingerprints needed for stale checks, continuation ability, read ranges, dry-run validation, or boundary/joiner diagnostics.
- Do not reintroduce safety-first redaction defaults; default redaction remains off.
- Do not optimize by forcing agents into extra calls, full-file reads, or manual reconstruction.
- Do not make a broad breaking schema rewrite without a compatibility path.

Must not break:
- Discovery tools must still help agents choose a next file/tool with minimal search.
- Read tools must still expose enough coverage/continuation facts for chunked reads and stale-aware follow-up.
- Write dry-runs must still provide bounded previews plus the caveat that escape-sensitive edits require read-back/read_file verification.
- `outline_file` Phase 9 compact/full/write-metadata contracts remain intact.
- Tool descriptions must stay discovery-friendly: compact, high-signal, and not so terse that lazy discovery gets worse.

Key product decision:
The default should be "action-compact": include fields that affect the next call, correctness, stale checks, discovery confidence, or write verification; omit fields that only restate complete/default/empty state; and make richer diagnostics explicit. Token savings are useful only when an agent can still complete the same task with the same or fewer calls and without extra broad reads, retries, or fallback `full` calls.

Phase shape:
- Stage 1 is measurement and prioritization. It produces a token ledger and freezes baseline ceilings for canonical workflows before changing more runtime defaults.
- Stage 2 implements targeted output/schema/tool-description optimizations only for measured high-cost surfaces.
- If Stage 1 shows the dominant cost is tool descriptions/schemas or request payloads rather than responses, Stage 2 must optimize that layer first instead of forcing response compaction.
- If a proposed optimization cannot beat its threshold without hiding next-action or correctness data, it is rejected or deferred.

Minimum savings threshold:
- Aggregate canonical workflow cost must drop by at least 15% versus the post-Phase-9 baseline.
- At least two non-outline workflow families must each drop by at least 10% when they are selected for implementation.
- No canonical workflow may grow by more than 3% unless the growth is explicitly tied to improved correctness and offset by a larger aggregate win.
- Tool call count must be less than or equal to baseline for every canonical workflow.
- Detail/full fallback calls must not become required for the default happy path.

Candidate optimization areas:
- Public JSON projection for repeated common structs such as `ActionHint`, `ContinuationHint`, coverage/proof blocks, and search stats, but only with matching output schemas.
- Optional detail profiles for discovery/search outputs: compact default plus detail/debug fields on request.
- Deduplication of `next_recommended_call` versus `next_recommended_calls` when both contain the same first call.
- Omit empty arrays or default booleans only where the schema and caller expectations remain clear.
- Compress verbose no-result/error guidance only when the same guidance is already represented as structured next-call hints.
- Range dry-run output profiles that keep validation and warnings first, with large previews and backup discovery available explicitly or bounded more tightly.

Projection and schema contract:
- Projection must be explicit and tool-family aware, not a global "omit defaults everywhere" pass.
- Runtime public JSON and advertised output schema must be updated together and tested together.
- Compact default may omit fields only when they are not required by the schema and are not next-action/correctness fields.
- Fields such as exact ranges, stale-check fingerprints/proofs, continuation inputs, `expected_version`, read ranges, validation status, boundary warnings, joiner correctness signals, partial state, and fingerprints-for-next-write are not diagnostic noise.
- For duplicate next-call hints, compact output may return only the primary hint when exactly one hint exists. When multiple distinct hints exist, compact output must preserve the list or an equivalent unambiguous structure.
- `detail`/`full` paths must remain available for fields removed from compact defaults, unless the field is proven obsolete and removed through an explicit compatibility decision.

Success:
- Canonical workflows after Phase 9 get a further measurable reduction that meets the Phase 10 thresholds without increasing tool calls.
- No canonical workflow loses the next-call data needed to continue without guessing.
- Compact defaults remain understandable to agents seeing the tool for the first time.
- Detailed/debug/write-safety data remains available through explicit input knobs or existing full/detail paths.
- The ledger identifies whether the best wins are in schemas/descriptions, requests, responses, retries, or fallback calls.

Acceptance budgets:
- Use normalized JSON bytes with stable path/hash/timestamp/cwd normalization and also record raw serialized bytes/tokens as report-only secondary data.
- Primary metric: workflow_total_normalized_bytes by component: tool_metadata, request_json, response_metadata, response_content, next_call_hints, error_guidance, retries_or_fallbacks.
- Guardrail metric: tool_calls must be less than or equal to baseline.
- Canonical workflows:
  - `workspace_inventory -> glob_file_search -> outline_file -> resolve_symbol_range -> read_file`;
  - `grep(content) -> read_file(read_range)`;
  - `read_file(chunk) -> continuation next range`;
  - `resolve_symbol_range(target_intent dry-run) -> copy_ranges dry_run -> read_file validation`;
  - `copy_ranges_batch dry_run` with multiple targets.
  - `read_files` bounded multi-file read with continuation and `expected_version`;
  - stale `read_file` or `read_files` continuation rejection with repair hint;
  - stale `resolve_symbol_range` or range-tool fingerprint mismatch with refresh hint;
  - ordinary non-stale validation/error path with a structured repair or retry hint;
  - no-result or ambiguous discovery path that still guides the agent to a narrower next call;
  - truncated `workspace_inventory` or `glob_file_search` continuation path.
- A smaller response fails acceptance if it causes hidden full-file reads, weaker stale checks, missing verification, or more calls.
- A smaller response also fails if it requires extra reasoning-only reconstruction that was previously carried by structured next-call data.
- Write-preview workflows must assert an observable escape-sensitive caveat: compact/default output must still tell the agent that previews are bounded display text and that validation/read-back or `read_file` is the trustworthy final check for escape-sensitive edits.

Unacceptable result:
- Token savings come from hiding the fields that tell the agent what to do next.
- Compact responses become ambiguous enough that agents need more broad `grep`, `read_file`, or `full` calls.
- Write workflows lose trustworthy verification or escape-sensitive caveats.
- Schema/docs/tool descriptions drift from runtime behavior.

Risks:
- Some existing defaults are noisy but intentionally reassuring; removing them can hurt agent confidence.
- Empty/default fields may be relied on by tests or clients even if agents do not need them.
- Per-tool optimization can produce inconsistent profiles; prefer shared policy where it does not blur tool-specific meaning.
- Too much schema compaction may worsen tool discovery or callable selection.

Open questions:
- None for concept start; the first review should challenge whether this phase should be broad, or split into a measurement-only phase plus implementation phase.
