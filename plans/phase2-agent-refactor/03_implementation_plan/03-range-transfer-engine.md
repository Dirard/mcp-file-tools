# Stage 3: Range Transfer Engine

## Goal

Build the safe, tested internals for exact byte-span extraction, placement, source deletion, fingerprint checking, file identity checks, symlink rejection, path locks, and streaming writes.

This stage should be mostly handler-private helpers with unit tests before public mutating tools are exposed.

## Depends On

- Stage 1 contracts.
- `outline_file` fingerprints and line ranges from Stage 2.
- Existing path validation and encoding helpers.

## Touched Areas

- new `filetoolsserver/handler/range_transfer.go`
- new `filetoolsserver/handler/range_index.go`
- new `filetoolsserver/handler/refactor_safety.go`
- new `filetoolsserver/handler/path_locks.go`
- new `filetoolsserver/handler/atomic_write.go`
- tests in `filetoolsserver/handler/range_transfer_test.go`

## Steps

1. Implement text write eligibility:
   - Accept UTF-8, ASCII, and UTF-8 BOM when current encoding support treats the file as text.
   - Reject binary files.
   - Reject UTF-16/UTF-32 writes in MVP.
   - Reject files whose line boundaries cannot map safely to raw byte spans.

2. Implement raw line index:
   - Scan file bytes in streaming chunks.
   - Provide dense and sparse modes:
     - dense mode may record byte offset for every line start only while the line count is within a fixed memory budget;
     - sparse mode records only requested range boundary offsets plus line count and fingerprint metadata.
   - Write tools must use sparse/range-aware scanning when source or target line count exceeds the dense budget; a file within `WriteThreshold` must not fail merely because it has many very short lines.
   - Track whether EOF has final newline.
   - Count line count through the same shared Phase 2 line-index helper used by `outline_file` fingerprints.
   - The helper must match the whole-file `read_file` display-line contract for addressable ranges: empty file -> 0; non-empty file -> decoded LF/CRLF line terminator count plus 1, so a file ending with LF/CRLF has an addressable final empty line.
   - Do not use `inspect_path.line_count` as the range source of truth unless `inspect_path` is separately migrated to this shared helper with compatibility notes.
   - Explicitly test and document line counts for:
     - empty file -> 0 lines;
     - non-empty file without newline -> 1 line;
     - file ending with a final newline -> shared helper and `read_file` expose the final empty line as addressable;
     - EOF without newline.
   - Compute sha256 during scan when possible.
   - Keep memory bounded to O(min(line_count, dense_budget) + requested_range_count), not O(file bytes) or unbounded O(line_count).

3. Implement line range to byte span:
   - `start_line` is 1-based inclusive.
   - Span starts at first byte of `start_line`.
   - Span ends after `end_line` terminator if present.
   - EOF without final newline ends at EOF.
   - UTF-8 BOM belongs to line 1.
   - Reject out-of-bounds ranges with current line count and recommended `outline_file`.

4. Implement source extraction streaming:
   - For each range, copy raw bytes from source to a writer.
   - Insert joiner bytes only between ranges.
   - Preserve CRLF/LF and all selected bytes exactly.
   - Compute selected bytes/line counts for output.

5. Implement target placement streaming:
   - `create_new`: output consists of selected source ranges plus joiners.
   - `append`: stream existing target then selected source ranges.
   - `prepend`: stream selected source ranges then existing target.
   - `insert_before_line`: stream target before line, inserted payload, then target from line onward.
   - `replace_range`: stream target before replacement span, inserted payload, then target after replacement span.
   - Do not auto-format or normalize line endings.

6. Implement source deletion streaming for move tools:
   - Normalize moved ranges against original source snapshot.
   - Reject overlaps for move.
   - Delete bottom-to-top logically, but implement streaming as copy all source bytes except moved spans.
   - Return removed line ranges and counts.

7. Implement boundary warnings:
   - Detect when inserted bytes meet non-empty target bytes without newline at append/prepend/insert/replace boundaries.
   - Return structured `boundary_warnings`.
   - Dry-run returns the same warnings without mutation.

8. Implement file identity and symlink safety:
   - Use `os.Lstat` to reject final-path symlinks for every read or write file path.
   - For every mutated path, reject symlink components in parent chain after path-map resolution.
   - Reject source/target same filesystem object using `os.SameFile` when possible plus cleaned/case-insensitive path equality on Windows.
   - Treat hardlinks with same identity as same-file operations.
   - Apply target checks to every batch target.

9. Implement path locks:
   - Add lock manager to `Handler`.
   - Acquire all source and target resolved paths in stable sorted order.
   - Release locks with defer.
   - Ensure dry-run either uses read locks if available or same exclusive path lock for simplicity; document choice in code comments.

10. Implement write primitives:
   - Existing file replacement: temp file in same directory, close, then rename where supported.
   - Source replacement uses same primitive.
   - Create-new target: use no-overwrite path. Prefer exclusive create or atomic link strategy; never use a plain overwrite rename for `must_not_exist`.
   - On write failure, return partial state with exact phase and fingerprints known so far.
   - Recompute or capture final fingerprint after successful write.

11. Implement sidecar backup primitive:
   - Shared by single and batch write tools; handlers should not hand-roll backup creation.
   - Only backs up existing regular files, never `create_new` targets.
   - Reject backup if the file or parent directory chain fails the same symlink safety checks as mutation.
   - Generate collision-resistant sidecar names in the same directory using timestamp plus short content/path hash plus attempt suffix.
   - Create backup with exclusive no-overwrite semantics (`O_CREATE|O_EXCL` or platform-equivalent) and bounded retry on name collision.
   - Copy exact source bytes to the backup; do not normalize encoding or line endings.
   - Close backup successfully before mutating the original; sync backup file and parent directory where practical, and document platform limits where directory sync is unavailable.
   - After backup and immediately before mutation, recheck the file fingerprint/identity; if it changed, abort without mutation and return the backup path plus current fingerprint.
   - Structured `backup_failed` errors include file role, original file, attempted backup path when known, any prior backup paths, and partial-state phase.

12. Implement precondition rechecks:
   - Validate all preconditions before reading.
   - Recheck source fingerprint immediately before every target mutation.
   - Recheck target precondition immediately before target mutation.
   - For move tools, recheck source again after target writes and immediately before source replace.

13. Implement threshold behavior:
   - Reject source/target files above `WriteThreshold` with agent-actionable error.
   - Acceptance test should prove a representative file far larger than token-friendly size still works.
   - Do not use threshold as an excuse to load whole files into memory.

## Checks

- Unit tests for byte spans:
  - LF, CRLF, EOF without newline;
  - BOM on line 1;
  - blank lines and adjacent ranges;
  - range out of bounds.

- Unit tests for placement:
  - create, append, prepend, insert before line, replace range.

- Safety tests:
  - relative path rejected before mutation;
  - same-file identity rejected;
  - hardlink identity rejected where OS supports it;
  - final symlink rejected;
  - parent symlink rejected;
  - binary and UTF-16 rejected.

- Backup primitive tests:
  - exact byte backup with LF/CRLF/BOM content;
  - collision retry uses no-overwrite creation;
  - backup path parent symlink rejected;
  - backup write failure aborts before original mutation;
  - stale original after backup aborts before mutation and returns backup path/current fingerprint.

- Performance tests:
  - representative large source with many lines;
  - high-line-count file near `WriteThreshold` uses sparse range-aware scanning and does not allocate one unbounded offset entry per line;
  - streaming output path does not allocate full file-sized buffers.

## Handoff / Next Stage

Move to `04-single-write-tools.md` once engine tests can prove exact byte preservation and safety without public tool handlers.

## Stop And Ask If

- A safe create-new strategy cannot be implemented without either overwrite risk or unacceptable partial-file risk.
- Platform-specific file identity behavior prevents a reliable same-file rejection strategy.
