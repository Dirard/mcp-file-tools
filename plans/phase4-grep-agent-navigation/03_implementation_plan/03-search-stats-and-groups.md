# Stage 3: Search Stats And File Groups

## Goal

Make grep results self-describing: agents can see what was searched, what was skipped, whether counts are complete, and which file ranges are worth opening next.

## Depends On

- Stage 1 DTO/schema.
- Stage 2 validation and search options.

## Touched Areas

- `filetoolsserver/handler/file_scan.go`
- `filetoolsserver/handler/grep_tool.go`
- `filetoolsserver/handler/grep_rows.go`
- `filetoolsserver/handler/tool_types.go`
- `filetoolsserver/handler/agent_tools_test.go`

## Search Stats Semantics

Implement `GrepSearchStats` with exact reached-scope counts:

- `files_seen`: regular file candidates delivered to grep after hidden/ignore traversal filtering and before grep-specific type/glob/binary filtering.
- `files_searched`: files actually searched for content.
- `files_with_matches`: distinct files that contributed returned match evidence in the selected output mode. In `content`, this means at least one returned `matches[]` row with `kind="match"` for that file. In `files_with_matches`, this means the file appears in `files[]`. In `count`, this means the file appears in `counts[]`. Discovered-but-not-returned matches at a global-limit or per-file-cap boundary are not counted.
- `skipped_hidden`: dot-prefix entries skipped during traversal; a skipped directory counts as one entry. This is the current dotfile behavior, not a new OS hidden-attribute check.
- `skipped_ignored`: entries skipped by user-provided `ignore_globs`; a skipped directory counts as one entry.
- `skipped_vcs`: entries skipped by built-in VCS pruning (`.git`, `.hg`, `.svn`, `.jj`); a skipped directory counts as one entry.
- `skipped_binary`: files skipped because binary.
- `skipped_unreadable`: selected files skipped because open/read/encoding-probe failed.
- `skipped_type_or_glob`: files rejected by `type` or `glob`.
- `files_capped`: files where `max_matches_per_file` actually suppressed additional content rows beyond the returned cap.
- `completed`: true only if returned evidence and mode-specific counts cover the full selected scope without global limit, per-file cap, or unreadable selected files.
- `stop_reason`: empty when complete; otherwise `limit`, `file_cap`, or `unreadable` for successful outputs.
- `counts_are_complete`: true only if counts and groups represent the full selected scope.

Binary skip semantics:

- Binary skips are reported through `skipped_binary`, but they do not by themselves make successful output incomplete.
- Binary files are outside grep's returned text evidence domain under the existing binary guard.
- If binary skips are the only skip/incompleteness source, `completed=true`, `counts_are_complete=true`, and `stop_reason=""`.

Hard guard failures remain current tool errors rather than successful grep outputs. Memory-threshold failures, cancellation, path resolution errors, bad regex, bad options, and validation errors omit `search_stats`.

Completeness scope rule:

- When `completed=true`, reached scope equals full selected scope.
- When `completed=false` because of global `limit`, counters are exact only for the reached prefix before the global output stream stopped.
- When `completed=false` because of `file_cap`, traversal may continue to later files; counters are exact for traversal/search work actually reached, while returned evidence for capped file(s) is incomplete.
- When `completed=false` because of `unreadable`, traversal may continue; counters are exact for traversal/search work actually reached, while selected unreadable file(s) were skipped.
- Do not run a second pass just to make counters full-scope after `limit`, `file_cap`, or `unreadable`.
- `truncated=true`, `completed=false`, and `counts_are_complete=false` are the machine-readable signal that agents must not treat counters, `match_count`, `row_count`, or `file_groups` as full-scope complete evidence. `search_stats.stop_reason` is authoritative for why evidence is incomplete. In incomplete outputs, every zero counter means zero only in the reached traversal/search scope, not necessarily zero in the full selected scope.
- Tests must include a global-limit truncated result whose counters show reached-prefix truth and whose `completed=false` / `counts_are_complete=false` prevents full-scope interpretation.

Traversal and read-error semantics:

- `filepath.WalkDir` entry errors keep current behavior of skipping the entry, but are counted as `skipped_unreadable`.
- File open/read/encoding probe failures that cause grep to skip a selected file are counted as `skipped_unreadable`.
- A file that cannot be read is not counted as `files_searched`.
- If `skipped_unreadable > 0`, successful output has `completed=false`, `counts_are_complete=false`, and `stop_reason="unreadable"` unless `limit` or `file_cap` also occurred.
- A whole-root access failure remains a structured tool error without `search_stats`.
- `stop_reason` precedence is `limit`, then `file_cap`, then `unreadable`.

Do not ship best-effort counters. A counter value of `0` must mean exactly zero entries/files in the reached scope. Only when `completed=true` does reached scope equal the full selected scope.

## Returned Evidence Boundary

Mode-specific returned evidence is the counting boundary for `match_count`, `row_count`, `search_stats.files_with_matches`, and `file_groups`:

- A row rejected by the global `limit` before it is appended to `matches[]`, `files[]`, or `counts[]` is not counted as returned evidence.
- A suppressed match after `max_matches_per_file` is not counted as returned match evidence, even if that line is emitted as `kind="context"` for a retained match.
- `files_seen` and `files_searched` still count traversal/search work actually reached, even when a later match in that file is not returned.
- Exact tests must assert this boundary for `content` global limit, `files_with_matches` global limit, `count` global limit, and per-file cap.

## Traversal Implementation Direction

Current file walk has `DotEntriesSkipped bool`.

Preferred implementation:

- extend `fileWalkStats` with exact integer counters while preserving existing bool behavior for other tools;
- check built-in VCS directories before generic dot-prefix checks and before user `ignore_globs`;
- increment hidden skipped count for skipped dot-prefix files and directories;
- increment ignored skipped count for user `ignore_globs` skipped files/directories;
- increment VCS skipped count for built-in `.git/.hg/.svn/.jj` pruning separately from user ignores;
- preserve legacy `DotEntriesSkipped=true` when a dot-prefix VCS directory such as `.git` is pruned, even though `skipped_vcs` rather than `skipped_hidden` receives the exact stat count;
- make grep-specific filter count type/glob skips;
- make grep-specific binary check count binary skips.
- count open/read probe failures through `skipped_unreadable`, not `skipped_binary`.

Keep `glob_file_search` behavior unchanged.

## Collector Groups

Extend `grepRowCollector` to collect per-file groups.

Group update rules:

- For content match rows:
  - increment group `match_count`;
  - increment `row_count`;
  - update `first_line` / `last_line`;
  - include line in read-range building.
- For content context rows:
  - increment `row_count`;
  - update `first_line` / `last_line`;
  - include line in read-range building.
- For `files_with_matches`:
  - do not populate `file_groups`; keep the existing `files[]` as the lightweight result.
- For `count`:
  - do not populate `file_groups`; keep the existing `counts[]` as the count-oriented result.

`file_groups` is still marshalled as `[]` in non-content successful outputs.

`file_groups[].match_count` uses the same logical match semantics as top-level `match_count`. In multiline mode, one logical regex occurrence increments the group by one even when it emits several display rows.

Group ordering is deterministic:

- groups are ordered by first returned evidence row in the output stream;
- if two groups would tie, order by slash-normalized path ascending;
- rows inside each group are processed in returned row order;
- `read_ranges` are sorted by `start_line`, then `end_line` after merge.

## Read Range Construction

Normative constants:

- default expansion when no context requested: 2 lines before and 2 lines after each match.
- max read ranges per file: 3.
- merge gap: 3 lines.

Rules:

- Build ranges from returned content match/context rows.
- If context rows are present, they can define exact ranges.
- If only match rows exist, expand by default context.
- Clamp to `line_window` when input used one.
- Always clamp `read_ranges[].start_line` to at least `1`; ranges must be valid 1-based replayable `read_file` inputs.
- Do not need full file line count just to clamp to EOF; `read_file` can softly trim end beyond EOF.
- Ranges are sorted and merged.
- If more than 3 merged ranges remain, keep the earliest 3 by `start_line`, then `end_line`; later ranges are omitted because `file_groups` is a compact navigation summary, not a pagination cursor.
- `read_ranges` is empty only when no line coordinates are available.

## Completeness And Limit Semantics

Current collector stops when `row_count >= limit`.

After Phase 4:

- global limit stop sets `output.Truncated=true` only when additional returned evidence would have been appended beyond the limit;
- `search_stats.completed=false`;
- `search_stats.stop_reason="limit"`;
- `search_stats.counts_are_complete=false`;
- file group counts are complete only for returned/scanned rows, not necessarily whole file/repo.

Exact-limit rule:

- Returning exactly `limit` rows/files/count rows is not truncation by itself.
- A global limit stop exists only after grep discovers at least one additional output evidence row/file/count row that would be returned but is suppressed by the global limit.
- If the scan completes with exactly `limit` returned evidence rows and no suppressed next evidence, output remains `truncated=false`, `completed=true`, `counts_are_complete=true`, and `stop_reason=""`.

For per-file cap without global limit:

- `output.Truncated=true`;
- `search_stats.completed=false`;
- `search_stats.stop_reason="file_cap"`;
- `search_stats.counts_are_complete=false`;
- `search_stats.files_capped` is the number of capped files;
- capped `file_groups[]` have `capped=true`;
- traversal continues to later files, so later files can still appear in returned evidence;
- `files_seen`, `files_searched`, and skip counters include traversal/search work actually reached after the capped file.

If both cap and global limit happen, `stop_reason="limit"` wins because the whole result stream stopped early.

Exact-cap rule:

- Reaching exactly `max_matches_per_file` returned matches is not a cap by itself.
- A cap exists only after grep discovers at least one later match in that same file whose evidence is suppressed.
- Implementation must scan far enough inside that file to distinguish an exact-cap complete file from an actually capped file. It must not stop at the returned cap and mark the file capped merely because the cap was reached.

If unreadable selected files occur without limit/cap:

- `output.Truncated=true`;
- `search_stats.completed=false`;
- `search_stats.stop_reason="unreadable"`;
- `search_stats.counts_are_complete=false`;
- matched readable files still return their evidence;
- traversal continues where possible, and counters include traversal/search work actually reached after the unreadable file.

For no-match after full scan:

- `completed=true`;
- `counts_are_complete=true`;
- `stop_reason=""`;
- `files_with_matches=0`.

For no-match inside `line_window`, the same tuple applies and output echoes `line_window`.

No-match output construction:

- no-match is still a successful grep output, not a tool error;
- no-match output must preserve computed traversal/search stats, normalized `pattern_mode`, echoed `line_window`, `file_groups=[]`, and any safe no-match `next_recommended_call`;
- implementation must not discard collector/traversal state by rebuilding no-match output from only input/display path/dot flag.

For `files_with_matches`:

- if limit stops after N files, counts are incomplete.
- if scan completes, `counts_are_complete=true` for file-list completeness; no `file_groups` are populated in this mode.

## Tests

- stats on full no-match scan.
- stats on full match scan.
- stats on global limit truncation.
- exact-limit tests for `content`, `files_with_matches`, and `count`: exactly `limit` returned evidence rows with no suppressed next evidence remains complete and not truncated.
- incomplete-output zero counters are zero only for reached scope.
- exact returned-evidence boundary for content global limit, files_with_matches global limit, count global limit, and per-file cap.
- no-match output preserves computed stats, normalized `pattern_mode`, echoed `line_window`, and `file_groups=[]`.
- stats on binary skip.
- binary skip alone keeps `completed=true`, `counts_are_complete=true`, and `stop_reason=""` for match and no-match text evidence cases.
- stats on unreadable skip distinct from binary skip.
- stats on type/glob skip.
- stats on hidden skip.
- stats on ignore_globs skip.
- stats on built-in VCS skip distinct from user ignore_globs.
- VCS precedence test: `.git` increments `skipped_vcs`, not `skipped_hidden` or `skipped_ignored`, while legacy `dot_entries_skipped=true` is preserved.
- file groups for content mode with merged ranges.
- deterministic `file_groups` ordering follows first returned evidence order, with path tie-break.
- multiline file group `match_count` counts logical occurrences, not emitted match rows.
- `file_groups=[]` for count mode.
- `file_groups=[]` for files_with_matches mode.
- read ranges are bounded and sorted.
- read range expansion for a match on line 1 clamps `start_line=1`.
- read range overflow keeps the earliest 3 merged ranges by `start_line`, then `end_line`.
- capped content output exact tuple: `completed=false`, `stop_reason="file_cap"`, `counts_are_complete=false`, `files_capped>0`, later files still searched.
- exact-cap content output has `completed=true`, `truncated=false`, `counts_are_complete=true`, `files_capped=0`, and no capped group.
- global-limit truncated result proves counters are exact reached-prefix counts, not full selected-scope totals.
- file-cap and unreadable tests prove traversal may continue and counters include later reached files while returned evidence remains incomplete.
- memory-threshold and cancellation guard tests continue to expect structured tool errors, not successful stats outputs.
- unreadable/probe-failure files increment `skipped_unreadable`, make stats incomplete, and are not counted as searched; root access failure remains tool error.

## Stop And Ask If

- Exact stats would require full second pass over every file.
- A stat cannot be defined without misleading the agent.
