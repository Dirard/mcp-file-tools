# Stage 4: Single-Target Write Tools

## Goal

Expose `copy_ranges` and `move_ranges` on top of the transfer engine, with agent-friendly dry-run, errors, warnings, and next fingerprints.

## Depends On

- Stage 1 contracts.
- Stage 3 transfer engine.
- Tool registration hook from Stage 1.

## Touched Areas

- new `filetoolsserver/handler/copy_ranges.go`
- new `filetoolsserver/handler/move_ranges.go`
- `filetoolsserver/server.go`
- tests in `filetoolsserver/handler/write_tools_test.go`

## copy_ranges Behavior

Input:

- `source_file`
- `source_fingerprint`
- non-empty `ranges`
- `target_file`
- `target_precondition`
- `placement`
- `joiner`
- `backup`
- `dry_run`

Rules:

- Source and target are absolute paths after trim/path-map.
- Source fingerprint is required and `sha256` is the write gate.
- Existing target writes require `target_precondition.fingerprint`.
- New target writes require `target_precondition.must_not_exist`.
- Source ranges must be in bounds and non-overlapping for single-target copy.
- Source/target same identity is rejected.
- Target parent directory must exist and pass symlink safety checks.

Output includes:

- `operation: "copy"`
- `dry_run`
- `applied`
- `ranges` with line counts
- `target_placement`
- bytes/lines written or would-write deltas
- source fingerprint before and checked-at-write
- target fingerprint before/after when applicable
- `source_fingerprint_for_next_write`
- `target_fingerprint_for_next_write`
- `boundary_warnings`
- `warnings`
- `backup_paths`

## move_ranges Behavior

Same input shape as `copy_ranges`, but:

- `operation: "move"`
- overlapping source ranges rejected.
- target is written first.
- source is modified only after target write succeeds and second source recheck passes.
- output includes source lines/ranges removed or would-remove deltas.
- partial failure after target write returns `target_written_source_not_updated`.

## Steps

1. Add handler methods:
   - `HandleCopyRanges`
   - `HandleMoveRanges`

2. Add validation wrapper:
   - Required paths present and absolute.
   - Fingerprint shape validation.
   - Placement one-of validation.
   - Joiner enum validation.
   - Backup enum validation.
   - Dry-run defaulting.

3. Wire dry-run:
   - Validate everything and compute planned deltas.
   - Return `applied=false`.
   - Do not create backups.
   - Do not create temp files unless implementation needs a throwaway temp under controlled cleanup; prefer no file creation.

4. Wire copy execution:
   - Acquire path locks.
   - Validate and build plan.
   - Final source recheck.
   - Final target precondition recheck.
   - Write target.
   - Return next fingerprints.

5. Wire move execution:
   - Acquire path locks.
   - Validate and build target/source-after plans.
   - Final source recheck before target.
   - Final target precondition recheck.
   - Write target.
   - Second source recheck.
   - Write source-after.
   - Return next fingerprints.

6. Implement action-oriented error outputs:
   - `source_fingerprint_mismatch` recommends `outline_file` with `output_profile: "outline"`.
   - `target_fingerprint_mismatch` recommends `outline_file` with `output_profile: "fingerprint_only"`.
   - `range_out_of_bounds` includes current line count and recommends `outline_file`.
   - `target_exists` and `target_missing` include current state and next tool.
   - Path and symlink errors never suggest relative paths.

7. Add sidecar backups:
   - Only when explicitly requested.
   - Use the shared backup primitive from Stage 3; do not duplicate backup naming/copy/retry logic in `copy_ranges` or `move_ranges`.
   - For `copy_ranges`, backup an existing target before replacement/append/prepend/insert; no backup is created for `create_new` because there is no previous target content.
   - For `move_ranges`, backup target and source if both may be modified; no source backup is created during dry-run.
   - Create backups after all precondition validation and before the first mutation of that file.
   - If backup creation fails, abort before mutating that file and return structured `backup_failed`; include any earlier successful backups in `backup_paths`.
   - If backup creation fails after another file was already mutated, return partial state with backup paths and modified files.
   - If the backed-up file changes between backup creation and final mutation recheck, abort without mutating that file and return `fingerprint_changed_after_backup` with backup path and current fingerprint.
   - Return paths in `backup_paths`.
   - Backups do not replace fingerprint checks.

## Checks

- `copy_ranges`:
  - create new from multiple ranges;
  - append/prepend/insert/replace existing target with fingerprint;
  - dry-run no mutation;
  - boundary warning returned;
  - source unchanged and source next fingerprint returned.

- `move_ranges`:
  - target written and source ranges removed;
  - dry-run no mutation;
  - source ranges deleted against original snapshot;
  - stale source before target write aborts with no target change;
  - stale source after target write returns partial state;
  - stale target aborts before target change.

- Error tests assert structured fields, not only message substrings.
- Backup tests cover collision retry, exact-byte backup, backup failure before mutation, and stale-file-after-backup abort.

## Handoff / Next Stage

Move to `05-batch-tools-recovery.md` once single-target tools are correct and reusable engine pieces are stable.

## Stop And Ask If

- A dry-run cannot compute useful boundary warnings without doing mutation.
- Sidecar backup behavior would require hidden naming or overwrite policy beyond timestamp/hash suffix.
