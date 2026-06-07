# Stage 2: Pattern Mode, Line Window, And Per-File Caps

## Goal

Implement the new grep inputs without changing old defaults.

## Depends On

- Stage 1 DTO/schema fields.

## Touched Areas

- `filetoolsserver/handler/grep_tool.go`
- `filetoolsserver/handler/tool_types.go`
- `filetoolsserver/handler/agent_tools_test.go`

## Pattern Mode

Implement:

```text
pattern_mode empty -> regex
pattern_mode regex -> current behavior
pattern_mode literal -> regexp.QuoteMeta(pattern), then apply case/multiline flags
```

Validation:

- reject values other than empty, `regex`, `literal`;
- error message names accepted values;
- regex parse errors happen only in regex mode;
- literal mode accepts regex metacharacters.

Tests:

- default regex behavior still works.
- invalid regex still fails when `pattern_mode` omitted.
- `pattern_mode=literal` finds `interface{}` and `functionCall(` without escaping.
- `case_insensitive=true` works in literal mode.
- `multiline=true` works with literal text containing newline when input provides one.

## Line Window

Validation:

- if `line_window` is present, `start_line >= 1`;
- `end_line >= start_line`;
- resolved `path` must be a file;
- directory path with `line_window` returns structured tool error and no scan.

Behavior:

- line-mode small file: search only selected display lines.
- line-mode large file: stream through file, search and emit only selected display lines, stop after `end_line`.
- `files_with_matches`: returns file if a match exists in the selected line window.
- `count`: counts matches only inside selected line window.
- `content`: line numbers remain original file line numbers, not window-relative.
- successful and no-match output must echo `line_window`.
- no output or recommendation should imply the whole file was searched when only line window was searched.

Boundary rules:

- Match domain is exactly `start_line..end_line`.
- `before`, `after`, and `context` may emit only rows inside `start_line..end_line`; context is clamped to the window.
- `file_groups[].read_ranges` are built only from returned rows and are clamped to `line_window`.
- A match that would require text outside the window is not a match for this call.
- If `end_line` is beyond EOF, the searched domain is softly clipped to EOF.
- If `start_line` is beyond EOF, output is successful no-match with echoed `line_window` and zero matches. After Stage 3 search_stats exists, the same case has `completed=true`, `counts_are_complete=true`, and `files_searched=1`.
- Empty files with `line_window` follow the same beyond-EOF no-match rule.

Multiline behavior:

- For first implementation, multiline line-window may load the file under existing memory guard, but it must build the searched content from the selected line slice only.
- Original line numbers are preserved by offsetting emitted selected-slice line numbers back to file line numbers.
- Cross-boundary matches are intentionally not supported: a multiline pattern cannot match text before `start_line` or after `end_line`.
- It must not return outside-window matches or outside-window context rows.
- If the file is too large for multiline memory guard, existing guard error applies.

Tests:

- file line-window includes only target range.
- original line numbers are preserved.
- directory with line-window errors.
- line-window no-match returns successful no-match output and echoes `line_window`.
- stats-dependent no-match assertions (`completed=true`, `counts_are_complete=true`) are final acceptance after Stage 3 search_stats exists, not a Stage 2-only check.
- line-window `start_line > EOF`, `end_line > EOF`, and empty-file cases return deterministic successful no-match outputs.
- multiline line-window matches only inside the selected range.
- line-window with context clamps context rows and `read_ranges` to the selected range.

## Max Matches Per File

Validation:

- `max_matches_per_file` must be positive if present.
- It is valid only for `output_mode` empty/content.
- In `files_with_matches` and `count`, reject it with structured error because it would make mode semantics confusing.

Behavior:

- In normal line mode, the cap counts match rows, not context rows.
- In multiline mode, the cap counts logical regex occurrences. All returned rows that belong to a retained occurrence are emitted together; the cap must not split one multiline occurrence across rows.
- Context rows for retained match rows are still emitted and count toward `row_count`, global `limit`, and `read_ranges`.
- A file is capped only when an additional match beyond `max_matches_per_file` is discovered and its evidence is suppressed. A file with exactly `max_matches_per_file` matches and no later suppressed match remains complete.
- If a later suppressed match line is also inside the context window of a retained match, the row may still be emitted only as `kind="context"` for that retained match. It must not be emitted as `kind="match"`, must not increment top-level or group `match_count`, and must still make the file capped/incomplete.
- Once a file has suppressed evidence after the cap, no more content rows from later matches in that file are emitted. Implementation may stop scanning that capped file at the first suppressed match and continue with the next file.
- Search must continue to later files unless a global `limit` stop or hard tool error occurs.
- `file_groups[].capped=true` for capped file groups.
- `search_stats.files_capped` increments.
- If any cap suppresses additional evidence and no global limit stop happens first:
  - `output.truncated=true`;
  - `search_stats.completed=false`;
  - `search_stats.stop_reason="file_cap"`;
  - `search_stats.counts_are_complete=false`;
  - top-level `match_count` and `file_groups[].match_count` count only returned match evidence, not suppressed matches.
- If a global limit stop also happens, `stop_reason="limit"` wins, `completed=false`, and `files_capped` still reports any caps hit before the limit.

Tests:

- one noisy file is capped and later file still appears.
- exact cap without an additional suppressed match remains `completed=true`, `truncated=false`, `files_capped=0`, and has no capped group.
- capped file group marks `capped=true`.
- stats show `completed=false`, `stop_reason="file_cap"`, `counts_are_complete=false`, and capped count.
- invalid cap values error.
- cap is rejected in `count` and `files_with_matches`.
- multiline cap keeps whole retained logical occurrences and does not split a multiline match group.
- cap/context overlap test: a suppressed later match that falls inside retained context is returned at most as `kind="context"`, does not increment match counts, and marks the file capped.

## Checks

- Targeted grep tests for pattern/window/cap.
- Existing large-file tests still pass.

## Stop And Ask If

- Supporting line-window would require changing read/display line numbering.
- Per-file cap cannot continue scanning later files without broad collector rewrite.
