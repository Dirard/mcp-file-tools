# Stage 5: Batch Tools And Recovery

## Goal

Expose `copy_ranges_batch` and `move_ranges_batch` for one-source/multi-target explicit range plans, especially Markdown decomposition, with 10/10 agent recovery semantics.

## Depends On

- Single-target transfer engine and write handlers.
- Batch partial state contracts from Stage 1.

## Touched Areas

- new `filetoolsserver/handler/copy_ranges_batch.go`
- new `filetoolsserver/handler/move_ranges_batch.go`
- shared batch helpers in `range_transfer.go` or `batch_transfer.go`
- tests in `filetoolsserver/handler/batch_tools_test.go`
- `filetoolsserver/server.go`

## copy_ranges_batch Behavior

Input:

- `source_file`
- `source_fingerprint`
- `targets[]`
- `dry_run`

Each target has:

- `target_file`
- `target_precondition`
- `placement`
- `ranges`
- `joiner`
- `backup`

Rules:

- One source file per call.
- Targets non-empty.
- Targets per call must be `<= BatchMaxTargets` (default `100`).
- Ranges per target must be `<= BatchMaxRangesPerTarget` (default `100`).
- Total ranges across the batch must be `<= BatchMaxRangesPerCall` (default `500`).
- Aggregate planned write bytes must be `<= BatchMaxPlannedBytes` (default `WriteThreshold`). Count every target replacement/create byte that would be written, including duplicated copied ranges, joiners, existing target bytes rewritten by atomic replace, and the source replacement bytes for move batch.
- Warning/detail arrays must stay bounded by these limits; if warning details are capped, return `warnings_truncated=true` with `warning_summary` aggregate counts rather than emitting unbounded rows.
- Target files unique by path and filesystem identity.
- Source and targets not same identity.
- Ranges interpreted against same source snapshot.
- Overlapping source ranges allowed for copy but reported in `batch_warnings`.
- All preconditions validated before any write.
- Source rechecked before each target mutation.
- Target precondition rechecked before each target mutation.

## move_ranges_batch Behavior

Same shape as copy batch, but:

- Overlapping and duplicate source ranges across targets are rejected.
- `source_backup` is top-level and explicit; it controls the source sidecar backup before source replacement.
- All targets are written before source is modified.
- Source is rechecked after all target writes and before source replace.
- Source deletion removes union of all moved ranges from original source snapshot.
- Any failure before source replace must leave source unmodified by the tool.

## Steps

1. Implement batch validation:
   - Validate source once.
   - Validate every target entry with single-target rules.
   - Enforce max targets, ranges per target, total ranges, and aggregate planned write bytes before any backup or mutation.
   - Return structured `batch_limit_exceeded` with `limit_name`, `limit`, `actual`, `safe_to_retry=false`, and `recommended_next_input_policy: "split_batch_explicitly"`.
   - Validate target uniqueness.
   - Validate source/target identity for every target.
   - Validate batch range overlap rules by operation.
   - Collect all precondition data before write phase.

2. Implement batch dry-run:
   - Return per-target planned deltas.
   - Return aggregate move source-removal deltas for `move_ranges_batch`.
   - Return boundary warnings per target.
   - Return `batch_warnings` for duplicate/overlap copy patterns.
   - No mutation and no backup creation.

3. Implement `copy_ranges_batch` execution:
   - Acquire source and all target locks in stable order.
   - Validate all preconditions.
   - For each target:
     - final source recheck;
     - final target recheck;
     - create requested target backup after precondition rechecks and before mutating that target;
     - write target;
     - record per-target result.
   - On target backup failure, mark that target failed with `backup_failed`, do not mutate that target, skip later targets, and return batch partial state.
   - On source fingerprint mismatch before a later target after earlier targets were written, mark the current target failed with `source_fingerprint_mismatch`, mark remaining targets skipped, set `source_modified_by_tool=false`, include current source fingerprint when it can be computed safely, and recommend `outline_file` on the source plus fingerprint checks for written targets before retry.
   - On any target failure, return batch partial state with statuses for written/failed/skipped targets and all backup paths created so far.
   - Source remains unchanged.

4. Implement `move_ranges_batch` execution:
   - Acquire source and all target locks in stable order.
   - Validate all preconditions.
   - Write every target using same target loop as copy batch.
   - If a target fails, return partial state with source unmodified.
   - After all targets written, recheck source.
   - If `source_backup.mode` is `sidecar`, create source backup after final source recheck and before source replacement.
   - On source backup failure, return partial state with all targets written, source unmodified, `backup_failed`, and backup paths created so far.
   - Write source-after once.
   - If source write fails, return partial state with all target statuses and source phase.

5. Implement batch partial state:
   - `operation`
   - `phase`
   - `source_file`
   - `source_modified_by_tool`
   - `source_fingerprint_before`
   - `source_fingerprint_after` or unknown marker
   - `target_results[]`
   - per-target `status`: `planned`, `written`, `skipped`, `failed`, optional future `rolled_back`
   - per-target `written`, `skipped`, `failed`, `failed_at`, `error`
   - per-target fingerprints before/after where known
   - per-target `backup_requested`, `backup_paths`, `backup_error`
   - aggregate `backup_paths`
   - `backup_results[]` with `file`, `role` (`source` or `target`), `requested`, `created`, `backup_path`, `error`
   - `warnings_truncated`
   - `warning_summary` with aggregate counts by warning code and target role
   - ranges for each target
   - `recommended_next_tool`
   - `recommended_next_input_policy`
   - `recovery_hint`

6. Apply batch backup semantics:
   - Dry-run never creates backups.
   - Use the shared backup primitive from Stage 3 for target and source backups.
   - `create_new` targets do not create backups because there is no previous target content.
   - Existing target sidecar backups are per-target and created only when that target requests them.
   - `move_ranges_batch` source backup is controlled only by top-level `source_backup`, not by any target entry.
   - Backup paths are returned both in aggregate `backup_paths` and in the relevant target/source result.
   - Backup creation happens after all initial precondition validation and immediately before the first mutation of that file.
   - A backup failure never mutates the file whose backup failed.

7. Keep batch tools explicit:
   - Do not infer target file names.
   - Do not create directories unless the user/agent target parent already exists.
   - Do not decide frontmatter/index behavior.
   - Do not update imports or references.

## Checks

- `copy_ranges_batch`:
  - creates two or more new target files from one source outline;
  - updates existing target with fingerprint;
  - duplicate source range returns warning, not failure;
  - batch limit exceeded returns structured rejection before backup/mutation;
  - source stale after earlier target writes returns partial state with source unchanged, written/failed/skipped target statuses, current source fingerprint, and next safe action;
  - target failure produces per-target partial state.

- `move_ranges_batch`:
  - writes multiple targets and removes source once;
  - rejects overlapping/duplicate moved ranges;
  - source is not modified when a target fails;
  - source stale after targets produces batch partial state;
  - source backup failure after target writes leaves source unmodified and returns target/backup partial state;
  - aggregate planned bytes includes target writes plus source replacement and is rejected before mutation when over limit;
  - dry-run reports per-target and aggregate deltas.

- Recovery:
  - partial state is enough to know which target files need inspection or retry;
  - `recommended_next_tool` never suggests a relative path;
  - no case requires full-file read just to decide the next recovery action.
  - backup failure before any write and after earlier target writes is covered by tests.

## Handoff / Next Stage

Move to docs and smoke only when batch tests prove agent recovery without manual target discovery.

## Stop And Ask If

- Batch recovery would need hidden rollback/deletion policy.
- Directory creation becomes necessary for the Markdown decomposition user story.
