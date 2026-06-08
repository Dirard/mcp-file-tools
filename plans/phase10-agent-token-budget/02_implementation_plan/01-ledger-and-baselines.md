# Stage 1: Ledger And Baselines

Goal:
Build the measurement system and freeze the post-Phase-9 baseline before changing runtime defaults.

Depends on:
- Clean Phase 10 concept.
- Current Phase 9 code state, including compact `outline_file` projection.

Touched areas:
- `filetoolsserver/handler/agent_tools_test.go` or a new focused test file under `filetoolsserver/handler`.
- Optional test-only helpers for JSON normalization, component classification, and workflow accounting.
- Test-only server/tool metadata measurement helpers that do not change production registration.

Steps:
1. Create a test-only `TokenLedger` helper with at least:
   - `workflow_name`;
   - `tool_calls`;
   - `total_normalized_bytes`;
   - `raw_serialized_bytes` report-only;
   - `estimated_tokens` report-only if a deterministic local estimator is available;
   - component buckets: `tool_metadata`, `request_json`, `response_metadata`, `response_content`, `next_call_hints`, `error_guidance`, `retries_or_fallbacks`.
2. Define metadata accounting before measuring:
   - primary workflow metric counts the full registered tool list metadata once per canonical workflow;
   - per-tool metadata bytes are report-only for target selection;
   - if a real runtime path sends metadata on every call, add a separate explicitly named per-call metadata scenario instead of changing the primary metric silently.
3. Reuse and extend the existing normalized JSON helper from `agent_tools_test.go` instead of inventing ad hoc string stripping.
4. Add path/hash/time/cwd normalization for:
   - `sha256`;
   - `modified_at`;
   - `modified_unix_nano`;
   - `cwd_id`;
   - file/path/directory keys;
   - temp-directory dependent text embedded inside structured hints.
5. Add component classification by JSON path. Start conservative:
   - `next_recommended_call`, `next_recommended_calls`, `recommended_write_call`, `preview_write_call`, `action_hint` -> `next_call_hints`;
   - `error`, `error_code`, `message`, `reason`, `refusal_reason`, `write_refusal_reason` -> `error_guidance` unless inside a hint;
   - `text`, `matches[].text`, `items[].text`, `diff_previews[].text`, `boundary_preview.*`, `read_back[].text` -> `response_content`;
   - `fingerprint`, `coverage`, `continuation`, `search_stats`, counters, ranges, validation, warnings -> `response_metadata`;
   - request JSON for every call -> `request_json`;
   - tool descriptions/input/output schema bytes -> `tool_metadata`.
6. Add canonical workflow fixtures that create temporary files/directories and call handlers directly. Use `06-canonical-workflow-fixtures.md` as the exact fixture contract:
   - `workspace_inventory -> glob_file_search -> outline_file -> resolve_symbol_range -> read_file`;
   - `grep(content) -> read_file(read_range)`;
   - `read_file(chunk) -> continuation next range`;
   - `resolve_symbol_range(target_intent dry-run) -> copy_ranges dry_run -> read_file validation`;
   - `copy_ranges_batch dry_run` with multiple targets;
   - `read_files` bounded multi-file read with continuation and `expected_version`;
   - stale `read_file` or `read_files` continuation rejection with repair hint;
   - stale `resolve_symbol_range` or range-tool fingerprint mismatch with refresh hint;
   - ordinary non-stale validation/error path with structured repair or retry hint;
   - no-result or ambiguous discovery path with useful next narrowing hint;
   - truncated `workspace_inventory` or `glob_file_search` continuation path.
7. Every workflow must build follow-up calls from actual returned fields:
   - use returned `continuation.next_recommended_call`;
   - use returned `file_groups[].read_ranges`;
   - use returned `symbol_ref` and top-level `fingerprint`;
   - use returned validation/repair/refresh hints;
   - fail when the returned fields are missing, ambiguous, or require manual reconstruction.
8. Include an observable write-preview caveat assertion: compact/default dry-run output must still state that previews are bounded display text and validation/read-back or `read_file` is the trustworthy final check for escape-sensitive edits.
9. Measure tool metadata without production changes:
   - If SDK exposes registered tool metadata cleanly, measure the registered descriptions and schemas.
   - If not, stop and record the measurement blocker before changing production registration.
   - Any registration refactor to expose metadata is a post-baseline Stage 3 change and needs an invariance test proving the public tool list/descriptions/schemas are unchanged.
   - Do not make tool metadata optimization decisions until the measurement exists.
10. Run the measurement tests in baseline mode and store an immutable measured baseline fixture with exact per-workflow `total_normalized_bytes`, component buckets, `tool_calls`, and raw/report-only values.
11. Derive budget ceilings from that immutable fixture in test code. Do not store only ceilings.
12. Add a test that fails if budget checks recompute baseline from current optimized output instead of reading the immutable fixture.
13. Produce a short in-repo report artifact under this plan directory, for example `baseline-ledger-report.md`, with the top contributors and target candidates. The report is explanatory; tests use the immutable fixture.

Checks:
- Focused measurement tests pass before runtime compaction.
- Immutable baseline fixture is stable across repeated runs and is not regenerated during optimized budget tests.
- Ledger includes all canonical workflows and all component buckets.
- Baseline report identifies top contributors and explicitly says whether `inspect_path` is in or out for Stage 2.
- Follow-up call inputs in workflow tests come from returned fields, not from hardcoded privileged knowledge.

Handoff / next stage:
Stage 2 consumes the baseline report and selects exact targets. If no target can plausibly meet thresholds without harming usefulness, stop and report measured no-go instead of forcing compaction.

Stop and ask if:
- Tool metadata cannot be measured without a large public API rewrite.
- Any production registration/schema/docs change is needed before baseline capture.
- Baseline results contradict the concept so strongly that response optimization is not the right product move.
