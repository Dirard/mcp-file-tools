# Canonical Workflow Fixtures

Goal:
Pin the exact fixture intent for Phase 10 measurement and budget tests so token savings cannot be proven with hardcoded privileged knowledge.

Global fixture rules:
- Only the first call in a workflow may use test-authored input.
- Every follow-up call must be built from fields returned by the previous tool output.
- A workflow fails if required returned fields are missing, ambiguous, or require manual reconstruction.
- Each workflow records `tool_calls`, normalized bytes, raw bytes, component buckets, and detail/full fallback calls.
- Each workflow has a stored immutable post-Phase-9 baseline entry.

Workflow 1: Discovery To Read
- Sequence: `workspace_inventory -> glob_file_search -> outline_file -> resolve_symbol_range -> read_file`.
- Follow-up fields:
  - use `workspace_inventory.next_recommended_call` or `directories_page` to choose a bounded discovery target;
  - use `glob_file_search.files` or a returned outline/read hint to choose the file;
  - use `outline_file.symbols[].symbol_ref` and top-level `fingerprint`;
  - use `resolve_symbol_range.resolved_ranges[0].range`;
  - use the resolved range for `read_file`.
- Minimum assertions:
  - no full-file read is needed before the final bounded read;
  - `symbol_ref` and fingerprint are present;
  - tool calls do not exceed baseline.

Workflow 2: Grep To Read Range
- Sequence: `grep(content) -> read_file`.
- Follow-up fields:
  - use `grep.file_groups[].read_ranges[0]`;
  - use the group `path` as `read_file.target_file`.
- Minimum assertions:
  - `file_groups` and `read_ranges` are present;
  - read output contains the matched symbol/text;
  - no broad read is required.

Workflow 3: Chunked Read Continuation
- Sequence: `read_file(chunk) -> read_file(next chunk)`.
- Follow-up fields:
  - use `read_file.continuation.next_recommended_call.recommended_next_input`;
  - preserve `expected_version` when provided by the continuation.
- Minimum assertions:
  - next range advances from the returned output;
  - stale-aware proof remains present when required;
  - no hardcoded next line is used by the test.

Workflow 4: Resolve Target Intent To Dry-Run Verification
- Sequence: `outline_file -> resolve_symbol_range(target_intent dry-run) -> copy_ranges dry_run -> read_file validation`.
- Follow-up fields:
  - use `outline_file.symbol_ref` and `fingerprint`;
  - use `resolve_symbol_range.recommended_write_call` or `preview_write_call`;
  - use the returned call input for `copy_ranges dry_run`.
  - build the final `read_file` validation call from returned dry-run fields such as `source_file`, `target_file`, `requested_ranges`, `target_placement`, `validation.next_recommended_call`, or read-back/verification hints added by the implementation;
  - do not hardcode validation paths or line ranges in the assertion after the first call.
- Minimum assertions:
  - write recommendation is dry-run-only;
  - validation and fingerprints-for-next-write remain available;
  - compact/default output exposes the escape-sensitive caveat about bounded previews and read-back/read_file verification.
  - the final validation/read call is actually exercised and is derived from dry-run output without manual reconstruction.

Workflow 5: Batch Dry-Run
- Sequence: `copy_ranges_batch dry_run`.
- Follow-up fields:
  - use source fingerprint obtained from prior outline/read fixture output, not a hardcoded recomputation inside the assertion.
- Minimum assertions:
  - per-target validation, warnings, joiner diagnostics, and aggregate warnings remain inspectable;
  - batch output does not hide partial-state recovery paths.

Workflow 6: Read Files Continuation
- Sequence: `read_files -> read_files continuation`.
- Follow-up fields:
  - use `read_files.continuation.next_recommended_call.recommended_next_input`;
  - preserve `expected_version` for the current item.
- Minimum assertions:
  - continuation carries enough information to resume without manual reconstruction;
  - `redaction_mode` omitted in input behaves as `off`.

Workflow 7: Stale Continuation / Refresh
- Sequence: `read_file` or `read_files` with `expected_version`, mutate only a temp fixture file, then retry with stale proof.
- Follow-up fields:
  - use returned error/repair hint or refresh recommendation.
- Minimum assertions:
  - stale rejection remains explicit;
  - hint tells the agent how to refresh safely;
  - no fallback full-file read is required just to understand the error.

Workflow 8: Stale Resolve Or Range Tool Mismatch
- Sequence: compact `outline_file -> resolve_symbol_range` or range dry-run with stale fingerprint.
- Follow-up fields:
  - use returned refresh hint, not a hardcoded helper path.
- Minimum assertions:
  - mismatch output identifies stale/fingerprint problem;
  - refresh input is schema-valid.

Workflow 9: Ordinary Non-Stale Failure
- Sequence: one validation/error path that is not stale and not no-result, for example invalid target placement, invalid output profile, or range out of bounds.
- Follow-up fields:
  - use returned structured repair/retry hint when provided.
- Minimum assertions:
  - error guidance remains compact but actionable;
  - the test checks a concrete tool/error shape, not a generic "any error" case.

Workflow 10: No-Result Or Ambiguous Discovery
- Sequence: `grep` or `glob_file_search` no-result/ambiguous discovery.
- Follow-up fields:
  - use returned narrowing hint or structured message fields.
- Minimum assertions:
  - agent can choose a narrower next call without broad guessing;
  - compacting prose does not remove structured guidance.

Workflow 11: Truncated Discovery Continuation
- Sequence: `workspace_inventory` or `glob_file_search` with a low limit, then continuation.
- Follow-up fields:
  - use `continuation.next_recommended_call.recommended_next_input`;
  - preserve `continuation_after` query hash/sort key.
- Minimum assertions:
  - continuation is stateless and schema-valid;
  - page/summary completeness distinction remains clear for workspace inventory.
