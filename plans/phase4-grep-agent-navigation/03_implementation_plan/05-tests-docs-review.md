# Stage 5: Tests, Docs, Verification, And Review

## Goal

Prove Phase 4 makes grep more useful for agents without breaking current behavior.

## Depends On

- Stages 1-4 implemented.

## Touched Areas

- `filetoolsserver/handler/agent_tools_test.go`
- `filetoolsserver/server_test.go`
- `README.md`
- `TOOLS.md`
- `server.json`
- `filetoolsserver/server.go`

## Test Matrix

### Compatibility

- Existing grep tests pass unchanged or with additive expectations.
- Existing mode behavior remains:
  - `content` returns `matches`;
  - `files_with_matches` returns `files`;
  - `count` returns `counts`.
- Regex remains default.
- Successful output echoes `pattern_mode="regex"` when input omitted it.
- Dash aliases still deserialize but are not schema properties.
- Exact dot-prefix file path remains searchable without `cwd_id`.
- Exact dot-prefix file path remains searchable with `cwd_id`.
- Broad traversal still skips dot-prefix entries.
- `.gitignore` is not applied.
- Non-dot noisy directories such as `node_modules`, `vendor`, `dist`, and `build` are still searched unless explicit `ignore_globs` excludes them.

### Literal Mode

- Literal search for `interface{}` succeeds.
- Literal search for `functionCall(` succeeds.
- Literal search for `a.b*c` treats punctuation literally.
- Regex invalid pattern still errors in default regex mode.
- Invalid regex-looking literal does not error in literal mode.
- Case-insensitive literal succeeds.

### Search Stats

- Full no-match scan has `completed=true`, `counts_are_complete=true`.
- Full match scan has `completed=true`.
- Global limit truncation has `completed=false`, `stop_reason=limit`, `counts_are_complete=false`.
- Exact limit without suppressed next evidence remains `completed=true`, `truncated=false`, `counts_are_complete=true`, and `stop_reason=""` for `content`, `files_with_matches`, and `count`.
- `files_with_matches` full scan has correct `search_stats`, preserves `files[]`, and has `file_groups=[]`.
- `files_with_matches` limit truncation has `completed=false`, `stop_reason=limit`, `counts_are_complete=false`, preserves `files[]`, and has `file_groups=[]`.
- `count` full scan has correct `search_stats`, preserves `counts[]`, and has `file_groups=[]`.
- `count` limit truncation has `completed=false`, `stop_reason=limit`, `counts_are_complete=false`, preserves `counts[]`, and has `file_groups=[]`.
- `truncated=true` for global limit, per-file cap suppression, and unreadable selected files; `truncated=false` for complete match/no-match outputs.
- `files_capped` is present as `0` in successful stats when no file was capped.
- Binary skip increments binary skip stat.
- Binary skip alone keeps `completed=true`, `counts_are_complete=true`, and `stop_reason=""` for match and no-match text evidence cases.
- Unreadable/probe-failure skip increments `skipped_unreadable`, does not increment `skipped_binary`, and makes stats incomplete.
- Dot-prefix traversal skip preserves `dot_entries_skipped=true` and increments exact `skipped_hidden` stat.
- `ignore_globs` skip increments exact ignored stat.
- Built-in VCS skip increments `skipped_vcs`, not user `skipped_ignored`.
- `.git` VCS pruning increments `skipped_vcs`, not `skipped_hidden` or `skipped_ignored`, while preserving legacy `dot_entries_skipped=true`.
- `type`/`glob` filtering increments type/glob skip stat.
- Memory-threshold and cancellation guard paths remain structured tool errors, not successful stats outputs.
- Validation/path/regex/tool-error outputs omit `search_stats` in marshalled JSON.
- Marshalled grep tool-error JSON has `file_groups=[]`.
- Cwd/error wrapper path also returns `file_groups=[]` and omits `search_stats`.
- Unreadable/probe-failure files increment `skipped_unreadable`, are not counted as searched, and whole-root access failure remains tool error.
- Unreadable/probe-failure tests use a deterministic strategy: a directory passed where a file candidate is expected, a broken symlink where supported by the OS/test helper, or a handler-level seam/mock if filesystem permissions are unreliable on Windows.
- Global-limit truncated stats test proves counters are reached-prefix exact counts and not full selected-scope totals.
- In incomplete outputs, zero counters are interpreted as zero in reached scope only.
- Returned-evidence boundary tests assert exact `match_count`, `row_count`, `search_stats.files_with_matches`, and arrays for content global limit, files_with_matches global limit, count global limit, and per-file cap.

### File Groups And Ranges

- Content mode creates one group per matched file.
- File group order is deterministic by first returned evidence row, with path tie-break.
- Groups do not duplicate text.
- First/last line reflect returned evidence rows.
- Read ranges are sorted, merged, and bounded.
- Read range default expansion is exactly 2 lines before/after, max 3 ranges per file, merge gap 3.
- Read range expansion for a match on line 1 clamps `start_line=1`.
- Read range overflow keeps the earliest 3 merged ranges by `start_line`, then `end_line`.
- Default read range expansion works when no context was requested.
- Context rows feed read ranges.
- Multiline content group `match_count` counts logical regex occurrences, not emitted display rows.
- Count mode has `file_groups=[]` and uses `counts[]`.
- Files mode has `file_groups=[]` and uses `files[]`.

### Max Matches Per File

- Capped noisy file does not consume global result.
- Later files still appear.
- Exact cap without an additional suppressed match remains complete and not truncated.
- Capped group has `capped=true`.
- Stats show `completed=false`, `stop_reason=file_cap`, `counts_are_complete=false`, and `files_capped`.
- If global limit and cap both happen, `stop_reason=limit` wins while `files_capped` still reports earlier caps.
- Multiline cap counts logical regex occurrences and does not split one retained multiline match group.
- Invalid values error.
- Cap in unsupported output modes errors.

### Line Window

- Single-file line window searches only selected lines.
- Returned line numbers are original file line numbers.
- Successful and no-match output echo `line_window`.
- No-match inside line window has `completed=true`, `counts_are_complete=true`, and does not imply full-file search.
- Line window with `start_line > EOF`, `end_line > EOF`, and empty file returns successful no-match with echoed window.
- Directory path with line window errors.
- Context rows and `read_ranges` clamp to line-window boundaries.
- Multiline line window does not return outside-window matches.
- Multiline line window preserves original line numbers after selected-slice search.
- Large-file line-mode line window preserves streaming.

### Recommendations

- Useful content result recommends `read_file`.
- Complete dense single-file content can recommend `outline_file`.
- Dense outline threshold uses at least 8 logical matches or returned evidence spanning at least 120 lines.
- Dense outline recommendation uses `line_window={first_line,last_line}` from the chosen group, clamped to the input `line_window` when present.
- Complete useful content does not recommend a redundant `grep`.
- Truncated multi-file/directory result recommends narrower `grep`, not first `read_file`.
- Truncated single-file or single-file `line_window` result skips redundant mapping and recommends `outline_file`, `read_file`, or no recommendation according to returned evidence.
- Truncated `files_with_matches` and `count` omit `next_recommended_call` unless a same-or-narrower non-semantic filter is available.
- Generated `max_matches_per_file` formula handles low limits: `limit=1 -> 1`, `limit=2 -> 2`.
- Capped multi-file/directory result recommends mapping/narrowing `grep`, not first `read_file`.
- Capped single-file or single-file `line_window` result skips redundant mapping and recommends `outline_file`, `read_file`, or no recommendation according to returned evidence.
- Dominant file can recommend `outline_file` or `read_file`.
- No-match regex-looking pattern can recommend literal retry.
- No-match literal/case-insensitive retries are emitted only for complete no-match output, not for incomplete unreadable/capped/limited searches.
- No-match literal/case-insensitive retries preserve `max_matches_per_file` when retrying content mode.
- Case-insensitive retry requires the usefulness predicate; positive examples include `todo`, `HTTP`, `handler42`, and negative examples include `123`, `[]()`, `...`, `/tmp/path`.
- Regex-looking literal retry predicate follows the normative metacharacter algorithm from Stage 4.
- Regex-looking examples that trigger literal retry: `foo.bar`, `func(`, `a[bc]`, `value|other`.
- Regex-looking examples that do not trigger literal retry: `foo\\.bar`, `func\\(`, `a\\[bc\\]`, `plain text`, `C:\\temp`.
- No recommendation is emitted when there is no clear safe next step.
- Only one top-level recommendation is emitted.
- `next_recommended_call` includes exact `ActionHint` JSON fields and `safe_to_retry=true`.
- Recommended `grep` retries preserve selected scope, including `cwd_id`, `path`, `line_window`, filters, pattern settings, and defined limit behavior.
- Cwd recommended `grep` preserves safe relative `ignore_globs`.
- Cwd recommended `grep` omits recommendation when original `ignore_globs` contains an absolute-looking path.

### CWD

- Cwd-aware outputs include `cwd_id` and `cwd`.
- Cwd-aware new path fields are relative.
- Cwd-aware recommended inputs include `cwd_id`.
- No absolute path leak in generated JSON except `cwd`.
- No absolute path leak in `next_recommended_call.reason`.
- No absolute path leak in `next_recommended_call.recommended_next_input.ignore_globs[]` when present.
- No absolute path leak with `line_window` plus `next_recommended_call` under `cwd_id`.
- No-leak JSON tests exclude `matches[].text` and user-supplied `pattern`, or use fixtures where content/pattern cannot contain cwd.
- No-cwd outputs remain slash-normalized absolute/display paths.

### Docs And Schema

- Tool schema exposes new input/output fields.
- Tool schema omits cursor/nextCursor.
- Server tool description mentions agent navigation.
- `TOOLS.md` documents new fields and examples.
- `README.md` summary stays accurate.
- `server.json` grep metadata is updated if stale.

## Verification Commands

Run after implementation:

```powershell
$env:GOPROXY='off'; go test ./filetoolsserver/handler ./filetoolsserver -run "Grep|Schema|Cwd"
$env:GOPROXY='off'; go test ./...
```

Race tests are not mandatory for this read-only grep phase unless implementation touches shared mutable state. If shared mutable state is introduced, return to plan because concept says no stateful grep.

## Review Cycle

After implementation and checks:

- Run `product_owner` review for product fit unless user explicitly stops review at that point.
- Run independent `reviewer` for engineering quality.
- If product findings appear, return to concept or plan.
- If engineering findings appear, repair and run fresh reviewer pass after substantive repair.

## Handoff To Implementation

Minimum handoff must include:

- user idea: grep 10/10 for agents, better than rg by being action-ready;
- accepted concept docs;
- this SRS bundle;
- out of scope list;
- must preserve current grep behavior and Phase 3 cwd path rules;
- required checks.

## Stop And Ask If

- A test requires reading raw secret-like files.
- Verification would require network.
- A product behavior change is needed to pass tests.
