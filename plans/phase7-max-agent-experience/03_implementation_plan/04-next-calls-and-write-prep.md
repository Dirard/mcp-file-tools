# Stage 4: Next Calls And Write Preparation

## Goal

Reduce manual JSON assembly by returning schema-valid next calls for common agent workflows.

## Depends On

- Stage 1 literal defaults.
- Stage 3 outline profile contract.

## Touched Areas

- `filetoolsserver/handler/grep_tool.go`
- `filetoolsserver/handler/glob_file_search.go`
- `filetoolsserver/handler/workspace_inventory.go`
- `filetoolsserver/handler/resolve_symbol_range.go`
- `filetoolsserver/handler/action hint` structs/helpers
- schema tests and tool tests

## Contract

Search-to-read:

- `grep` content output should provide ready `read_file` and/or `read_files` inputs for matched ranges.
- `grep` should provide `outline_file` input for source-like/language-relevant files where structure is useful.
- Recommended inputs must be cwd-aware and range-aware.
- `grep` read recommendation thresholds:
  - `truncated=false`;
  - at most 6 files in grouped matches;
  - at most 12 total read ranges;
  - at most 3 ranges per file;
  - otherwise recommend narrowing instead of `read_files`.
- `grep` outline recommendation threshold:
  - `truncated=false`;
  - exactly one matched file is source/config-like by the Phase 7 extension/type classification below;
  - otherwise recommend narrowing or read ranges, not outline.

Extension/type classification:

- `source_like_extensions`: `.go`, `.js`, `.jsx`, `.ts`, `.tsx`, `.py`, `.svelte`.
- `config_like_extensions`: `.json`, `.jsonc`, `.yaml`, `.yml`, `.toml`, `.ini`, `.cfg`, `.conf`.
- `text_like_extensions`: source-like plus config-like plus `.md`, `.markdown`, `.txt`, `.rst`, `.csv`, `.tsv`.
- Positive classification is extension-only and case-insensitive.
- Files without one of these extensions are not recommended for automatic `read_files` / `outline_file` handoff by `glob_file_search`; return narrowing or plain file rows instead.
- Tests must include representative positives and negatives.

Glob-to-read/outline:

- `glob_file_search` may recommend `read_files` only when `truncated=false`, result count is 1-6, and all entries are text-like by the Phase 7 extension/type classification.
- It recommends `outline_file` when `truncated=false` and exactly one result is source/config-like by the Phase 7 extension/type classification.
- For broad/truncated results, recommend narrowing, not noisy batch reads.

Workspace inventory:

- Stays directory-level.
- May recommend `glob_file_search`, continued `workspace_inventory`, or directory narrowing.
- Must not guess direct file paths for `read_files` or `outline_file`.

Write preparation:

- `resolve_symbol_range(target_intent)` returns dry-run-ready `copy_ranges` / `move_ranges` input when safe.
- Source selector/range must be explicit.
- Target intent must include explicit target file and operation.
- `target_intent.target_precondition` is optional/nullable for preparation. If omitted, `resolve_symbol_range` attempts to inspect the explicit target path and prepare one.
- Target paths are never inferred.
- Recommended write inputs include:
  - `source_file`;
  - `source_fingerprint`;
  - concrete `ranges`;
  - `target_file`;
  - target precondition when obtainable;
  - placement;
  - joiner;
  - `dry_run=true`;
  - `redaction_mode` only when explicitly requested.
- Write recommendation gates:
  - single exact range or explicit non-overlapping range set;
  - every range is whole-line and `write_safe=true`;
  - current reparse has `parser_status="ok"` for parser-backed symbols;
  - no estimated ranges;
  - no same-file source/target after normalized path comparison;
  - target syntax/proven placement is safe;
  - JSON/YAML move/delete unsafe nodes do not receive write recommendations;
  - recommended inputs never set `dry_run=false`.

Target precondition outcomes:

- existing target with readable fingerprint -> include `target_precondition.fingerprint`;
- missing target for create-new intent -> include `target_precondition.must_not_exist=true`;
- missing target for append/prepend/insert/replace -> refuse recommendation and return `inspect_path`/create-new hint;
- target changed/mismatch against provided precondition -> refuse recommendation and return target refresh hint;
- unreadable/unsupported target -> refuse recommendation and return exact inspection hint.

## Steps

1. Inventory current `next_recommended_call` and `next_recommended_calls` surfaces.
2. Add schema-validation tests for recommended input maps.
3. Update `grep`:
   - group ranges into compact `read_files` recommendations when useful;
   - keep `read_file` single-file recommendation for focused results;
   - avoid over-batching when output is broad/truncated.
4. Update `glob_file_search`:
   - recommend `read_files` only when `truncated=false`, result count is 1-6, and all entries are text-like by the Phase 7 extension/type classification;
   - recommend `outline_file` only when `truncated=false` and exactly one result is source/config-like by the Phase 7 extension/type classification;
   - otherwise recommend narrowed glob/search.
5. Update `workspace_inventory`:
   - ensure recommendations are directory-level only;
   - reject guessed file-specific recommendations in tests.
6. Update `resolve_symbol_range` target intent:
   - include target precondition if target exists and fingerprint can be read;
   - include `must_not_exist` when create-new target is missing;
   - return exact refresh action if target precondition cannot be prepared;
   - ensure recommendations never set `dry_run=false`.
7. Add workflow tests:
   - grep -> read_file/read_files;
   - grep -> outline_file;
   - glob -> read_files bounded;
   - glob -> outline_file for exactly one source/config-like result by the Phase 7 extension/type classification;
   - glob broad/truncated -> narrowing recommendation, not batch read/outline;
   - workspace -> glob narrow;
   - resolve -> dry-run write input for existing target;
   - resolve -> dry-run write input for missing target.
   - resolve omitted precondition existing target;
   - resolve missing target create_new;
   - resolve same-file refusal;
   - resolve unsafe structured JSON/YAML target refusal;
   - resolve non-whole-line/estimated range refusal.

## Checks

- Recommended next inputs validate against generated schemas.
- Cwd projection is proven for recommended inputs.
- No direct workspace_inventory guessed read/outline calls.

## Handoff / Next Stage

After Stage 4, common happy paths are easier. Stage 5 improves unhappy-path ergonomics and output compactness.

## Stop And Ask If

- A recommendation would require guessing target paths.
- Schema-valid next input would be too noisy to help the agent.
