# Stage 2: Target Selection And Contracts

Goal:
Turn the measured ledger into a small implementation target list with explicit compact/detail/full contracts.

Depends on:
- Stage 1 baseline ledger and top-contributor report.

Touched areas:
- Plan report files under `plans/phase10-agent-token-budget/`.
- Candidate runtime files identified by the ledger, likely among:
  - `filetoolsserver/handler/tool_types.go`;
  - `filetoolsserver/handler/refactor_types.go`;
  - `filetoolsserver/handler/read_file.go`;
  - `filetoolsserver/handler/read_files.go`;
  - `filetoolsserver/handler/grep_tool.go`;
  - `filetoolsserver/handler/glob_file_search.go`;
  - `filetoolsserver/handler/workspace_inventory.go`;
  - `filetoolsserver/handler/range_transfer.go`;
  - `filetoolsserver/server.go`.

Steps:
1. Rank candidate surfaces by aggregate and per-workflow contribution.
2. Select only targets that can plausibly deliver:
   - at least 15% aggregate workflow reduction;
   - at least two non-outline family wins of at least 10%;
   - no workflow growth above 3%.
3. For each selected tool family, write a compact contract before coding:
   - fields always kept in compact default;
   - fields omitted in compact default;
   - fields moved to detail/full;
   - fields never treated as noise;
   - schema changes required;
   - docs/tool-description changes required;
   - workflow tests that prove usefulness.
4. Candidate contract examples:
   - Discovery/search: keep `files`/`matches`/`file_groups`/`read_ranges`/truncation/continuation; consider trimming duplicate primary/list hints only when one hint exists; keep no-result guidance if it prevents broad retry.
   - Read: keep line text, range, coverage/continuation facts needed for next chunk; consider omitting default false/empty fields only when schema and continuation semantics stay obvious.
   - Workspace inventory: keep page/summary completeness distinction; consider compacting repeated counters only if ledger shows value and docs stay clear.
   - Resolve/write dry-run: keep validation, fingerprints-for-next-write, boundary warnings, joiner correctness, partial state; consider bounding preview payloads and backup discovery in compact/default only with explicit detail access.
5. Decide profile naming only after target selection:
   - Prefer existing names where possible: `agent`, `full`, `summary_profile`.
   - If a new profile knob is needed, prefer a small shared vocabulary such as `output_profile=compact|detail|full`, but only where it does not conflict with existing tool semantics.
   - Avoid adding knobs that cost more schema/description tokens than they save.
6. Define compatibility path:
   - If compact removes fields from default public output, detail/full must expose them.
   - If a field is removed entirely, plan must state why it is obsolete and what test proves no agent workflow uses it.
7. Update the baseline report with selected targets and rejected candidates.

Checks:
- Every selected target maps to a measured cost contributor.
- Every omitted field has a detail/full path or an explicit obsolete-field decision.
- No correctness/stale/write-validation field is listed as removable.
- No selected change requires extra happy-path tool calls.

Handoff / next stage:
Stage 3 implements only the selected contracts. If an attractive optimization lacks a clear contract, it returns here instead of being invented during coding.

Stop and ask if:
- The selected contract requires a public breaking schema change without a compatibility path.
- The measured top cost center is outside the current product scope.

