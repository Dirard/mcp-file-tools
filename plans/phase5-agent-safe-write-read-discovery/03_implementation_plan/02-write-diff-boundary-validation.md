# Stage 2: Write Diff, Boundary, Validation

## Goal

Make `copy_ranges*` and `move_ranges*` safe to operate without manual reconstruction: diff preview before mutation, explicit joiner/boundary model, and bounded read-back after mutation.

## Depends On

- Stage 1 DTOs and redaction helpers.

## Touched Areas

- `filetoolsserver/handler/range_transfer.go`
- `filetoolsserver/handler/batch_ranges.go`
- `filetoolsserver/handler/copy_ranges.go`
- `filetoolsserver/handler/move_ranges.go`
- `filetoolsserver/handler/refactor_safety.go`
- `filetoolsserver/handler/refactor_errors.go`
- `filetoolsserver/handler/read_output.go`
- `filetoolsserver/handler/write_tools_test.go`
- `filetoolsserver/handler/batch_tools_test.go`

## Steps

1. Add `RedactionMode string json:"redaction_mode,omitempty"` to `copy_ranges`, `move_ranges`, `copy_ranges_batch`, and `move_ranges_batch` top-level inputs.
2. For batch tools, allow optional per-target `redaction_mode` only to choose `auto` or `strict` for that target; top-level `strict` cannot be weakened by a per-target `auto`.
3. Echo effective redaction mode in `diff_previews[]`, `boundary_preview`, and `validation` surfaces where content is returned.
4. Add `DiffPreviews []DiffPreview`, `JoinerEffect`, `BoundaryPreview`, and `WriteValidation` fields to single and batch range outputs.
5. Keep old `boundary_warnings`, `warnings`, byte counters, fingerprints, and partial-state fields.
6. Build a reusable in-memory diff helper:
   - input old bytes, new bytes, projected old/new path labels, context lines, max bytes;
   - output unified diff text and stats;
   - no filesystem mutation;
   - compute structural diff stats on the unredacted diff;
   - redact returned text after stats are computed, preserving line breaks where possible so hunk shape stays readable;
   - apply byte/hunk truncation only after redaction, and set returned-surface `truncated` / `hunks_omitted` from the redacted truncated output;
   - mark `redacted=true` when text differs from raw preview.
7. For `create_new`, diff uses the non-path sentinel `<new file>` to target path. Do not render `/dev/null` because Phase 5 output is Windows/cwd-normalized and no-leak tests should not need a Unix path exception.
8. For `append`, `prepend`, `insert_before_line`, and `replace_range`, diff target before/after content.
9. For `move_ranges`, produce two `diff_previews[]` entries:
   - `role="target"` for target write;
   - `role="source_removal"` for source removal.
10. For `copy_ranges_batch`, produce per-target diff in each `target_results[]`.
11. For `move_ranges_batch`, produce:
   - per-target diffs;
   - top-level `source_diff_previews[]` entry with `role="source_rewrite"` for source rewrite/removal.
12. Add config:
   - `MCP_DIFF_PREVIEW_MAX_BYTES`, default `32768`;
   - `MCP_READ_BACK_MAX_LINES`, default `80`;
   - `MCP_BOUNDARY_PREVIEW_MAX_CHARS`, default `1000`.
13. Fix `blank_line` semantics:
    - adjacent non-empty blocks get exactly two newline characters between their last/first non-newline bytes;
    - if existing target/payload newlines already create one visual blank line, do not add extra blank lines;
    - empty side uses minimal sane separator.
14. Compute `JoinerEffect` for each write plan.
15. Compute `BoundaryPreview` from actual planned bytes, not from requested enum only.
16. Return `error_code=invalid_joiner` for unsupported joiner.
17. Return `error_code=invalid_placement` for invalid placement shape.
18. Return `error_code=invalid_redaction_mode` for unsupported redaction mode.
19. After apply, run bounded validation:
    - inspect target fingerprint;
    - compute affected target read-back windows;
    - for move, compute affected source read-back windows near removals;
    - read windows with existing line-number output format;
    - redact if needed;
    - set validation status;
    - if read-back is truncated or insufficient, set `validation.status=applied_validation_truncated` and include cwd-safe `read_file` hints in `validation.next_recommended_call` / `validation.next_recommended_calls[]`.
20. If validation fails after write, do not mark operation failed retroactively; set `validation.status=applied_validation_failed`, keep fingerprints/partial state and set `validation.error_code=post_write_validation_failed`. Do not set top-level `error_code` for an applied write.
21. Add structured backup failure codes:
    - requested backup creation failures set `backup_results[].error_code=backup_creation_failed`;
    - if backup failure prevents mutation, top-level `error_code=backup_creation_failed` mirrors the failed backup result;
    - tests cover single and batch backup failure surfaces.
22. Add backup rediscovery hint:
    - if backup created and backup path starts with dot filename, `backup_discovery.next_recommended_call` recommends `glob_file_search` with `include_hidden=true`, `glob_pattern=".*.bak"`, and same directory.
    - for batch writes, `backup_discovery.discovery_groups[]` and `next_recommended_calls[]` group rediscovery hints by backup directory and role, because target/source backups can be in different directories.
23. For batch outputs, aggregate:
    - total diff truncation count;
    - per-target validation statuses;
    - top-level source validation status for move batch.
24. Batch placement:
    - `target_results[].diff_previews` contains that target's target diff.
    - `target_results[].joiner_effect` contains that target's effective joiner.
    - `target_results[].boundary_preview` contains that target's planned boundary.
    - `target_results[].validation` contains that target's target read-back/validation.
    - `move_ranges_batch.source_diff_previews` and `move_ranges_batch.source_validation` describe the single source rewrite/removal after all target writes.
    - Batch top-level aggregate fields may summarize counts/status but do not replace per-target safety fields.

## Acceptance

- Dry-run for every placement mode returns a diff preview and does not mutate files.
- `blank_line` creates the visually expected empty line between `Keep line.` and `## Beta`.
- Diff preview under `cwd_id` uses cwd-relative file headers.
- `create_new` diff headers use `<new file>` as a non-path sentinel and do not leak `/dev/null` or absolute paths.
- Diff truncation is explicit and still returns stats.
- Redaction mode is schema-constrained, echoed, and consistent across single/batch write previews/read-backs.
- Applied write returns validation evidence or clear validation failure.
- Applied validation failure is represented inside `validation`, not as a failed top-level tool result.
- Existing fingerprint mismatch and range safety still fire before mutation.
- Backups are still created only when requested and never in dry-run.

## Checks

- Unit tests for diff helper on create/append/prepend/insert/replace/delete.
- Windows/cwd tests for `create_new` diff headers prove `<new file>` is accepted as a sentinel and no `/dev/null` or absolute path appears.
- Tests for CRLF preservation and diff readability.
- Tests for single copy/move dry-run diff.
- Tests for batch copy/move dry-run diffs plus per-target `joiner_effect`, `boundary_preview`, and `validation` placement.
- Tests for move batch top-level source diff/validation placement.
- Tests for `backup_discovery` on single and multi-directory batch writes, including cwd-safe `glob_file_search(include_hidden=true, glob_pattern=".*.bak")` recommended inputs.
- Tests for validation read-back windows.
- Tests for `applied_validation_truncated` and read-back-too-large cases with cwd-safe `read_file` recommended inputs.
- Tests for `backup_results[].error_code` on requested backup creation failure in single and batch paths.
- Tests for write redaction mode on single tools, batch top-level, per-target override and invalid enum.
- Tests for validation failure by making post-write inspect fail in a controlled helper or temp permission case when feasible, proving `validation.error_code` is used.
- Cwd no-leak tests for diff text and validation paths.
- Redaction tests for diff and read-back, including multi-line secret/private-key-like content where line-preserving redaction must not distort structural stats.
- Redaction/truncation order tests prove raw structural stats remain stable, secret-like content is redacted before returned truncation, and `truncated` / `hunks_omitted` describe the returned redacted surface.

## Handoff / Next Stage

After Stage 2, write tools have the safety surface needed for hidden backup discovery and later provenance-gated cleanup planning.

## Stop And Ask If

- Diff construction requires holding files above `MCP_WRITE_THRESHOLD`.
- Redaction makes diff unreadable enough that the agent cannot verify structural change.
- `blank_line` fix would break documented old behavior in a way that needs user acceptance.
