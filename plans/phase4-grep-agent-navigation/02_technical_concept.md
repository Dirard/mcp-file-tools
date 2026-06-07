# Phase 4 Grep Agent Navigation Technical Concept

concept_version_label: phase4-grep-agent-navigation-v1
status: clean_product_owner_approved

## Technical Direction

Phase 4 upgrades the existing `grep` tool from structured ripgrep-like output into an agent navigation tool.

The implementation should preserve the current search engine shape:

- one request, one structured JSON response;
- no stateful pagination;
- no index;
- no AST parsing inside `grep`;
- no write behavior;
- existing path/cwd contract from Phase 3.

The new technical layer should add:

- literal pattern support;
- search completeness telemetry;
- file-level grouping;
- read ranges for follow-up;
- one recommended next call.

## Current Baseline

Current `grep` already supports:

- required `pattern` and `path`;
- `output_mode`: `content`, `files_with_matches`, `count`;
- `before`, `after`, `context`;
- `case_insensitive`;
- `type`, `glob`, `ignore_globs`;
- `multiline`;
- `limit`;
- cwd-aware path projection;
- dot-prefix traversal skip and exact dot-prefix file search;
- large-file streaming in line mode;
- multiline memory guard.

Relevant current areas:

- `filetoolsserver/handler/grep_tool.go`;
- `filetoolsserver/handler/grep_rows.go`;
- `filetoolsserver/handler/tool_types.go`;
- `filetoolsserver/handler/cwd_path.go`;
- `filetoolsserver/server.go`;
- `TOOLS.md`, `README.md`, `server.json`;
- existing grep tests in `filetoolsserver/handler/agent_tools_test.go`.

## Input Contract Additions

### `pattern_mode`

Add:

```go
PatternMode string `json:"pattern_mode,omitempty"`
```

Supported values:

- empty or `regex`: preserve current behavior;
- `literal`: compile by escaping the pattern before regexp matching.

Rules:

- default remains regex;
- invalid values return structured tool error;
- `case_insensitive` still applies to both regex and literal;
- `multiline` still applies to both regex and literal, but literal multiline means the literal string may include line breaks if the caller provides them.

### `line_window`

Add optional line window for narrowing a search inside one exact file:

```json
{
  "line_window": {
    "start_line": 100,
    "end_line": 180
  }
}
```

Rules:

- valid only when resolved `path` is a file;
- invalid for directory traversal because it would be ambiguous across files;
- 1-based inclusive lines;
- errors are structured and explain whether the caller should use a file path or remove `line_window`;
- works with `content`, `files_with_matches`, and `count`;
- for multiline search, matching should be constrained to the selected display-line slice.

### `max_matches_per_file`

Add:

```go
MaxMatchesPerFile *int `json:"max_matches_per_file,omitempty"`
```

Rules:

- only meaningful for `content`;
- default means no per-file cap beyond global `limit`;
- when set, the collector should stop emitting additional content rows for a file after its cap, but continue scanning later files when possible;
- stats must report that at least one file was capped.

This prevents one noisy file from consuming the global result window.

## Output Contract Additions

### `search_stats`

Add `SearchStats` to `GrepOutput`.

Conceptual fields:

```go
type GrepSearchStats struct {
    FilesSeen          int    `json:"files_seen"`
    FilesSearched      int    `json:"files_searched"`
    FilesWithMatches   int    `json:"files_with_matches"`
    SkippedHidden      int    `json:"skipped_hidden"`
    SkippedIgnored     int    `json:"skipped_ignored"`
    SkippedBinary      int    `json:"skipped_binary"`
    SkippedTypeOrGlob  int    `json:"skipped_type_or_glob"`
    FilesCapped        int    `json:"files_capped,omitempty"`
    Completed          bool   `json:"completed"`
    StopReason         string `json:"stop_reason,omitempty"`
    CountsAreComplete  bool   `json:"counts_are_complete"`
}
```

SRS may refine exact fields based on what traversal can track cleanly, but these semantics are required:

- `completed=true` only when all selected files were searched without output-limit stop;
- `stop_reason` explains incomplete results, for example `limit`, `context_cancelled`, `memory_threshold`, `file_cap`;
- `counts_are_complete=false` when `match_count`, `row_count`, `file_groups[].match_count`, or `counts[]` are only for returned/scanned subset;
- no field should imply a full-repo count when early-stop happened.

### `file_groups`

Add:

```go
type GrepFileGroup struct {
    Path       string            `json:"path"`
    MatchCount int              `json:"match_count"`
    RowCount   int              `json:"row_count"`
    FirstLine  int              `json:"first_line,omitempty"`
    LastLine   int              `json:"last_line,omitempty"`
    ReadRanges []SourceLineRange `json:"read_ranges"`
    Capped     bool             `json:"capped,omitempty"`
}
```

Rules:

- groups are built from returned evidence rows and counted matches;
- groups must not duplicate full text;
- `read_ranges` are 1-based inclusive ranges suitable for `read_file`;
- adjacent/overlapping ranges should be merged;
- read ranges should include enough context to be useful, based on existing context inputs and a small default expansion when context is zero;
- cwd-aware paths must be cwd-relative.

### `next_recommended_call`

Add:

```go
NextRecommendedCall *ActionHint `json:"next_recommended_call,omitempty"`
```

Rules:

- one recommendation only;
- include `cwd_id` when input used `cwd_id`;
- recommended path inputs follow current output path mode;
- sanitize through cwd output projection;
- never embed hidden absolute paths except `cwd` metadata already allowed by Phase 3;
- do not recommend mutating tools.

Recommended next tool choices:

- `read_file` for the best returned `read_range`;
- `grep` when result is truncated and a narrower retry can be formed;
- `outline_file` when structure is likely more useful than more text, especially many matches in one file;
- no recommendation when there is no clear safe next step.

## Collector And Counting Direction

The current `grepRowCollector` truncates by global `limit` and stores legacy text plus mode-specific arrays.

Phase 4 should extend the collector rather than replacing it:

- keep existing `matches`, `files`, `counts`;
- maintain per-file group state;
- maintain returned match and row counts;
- track stop reason when `limit` stops output;
- support per-file caps without stopping the whole traversal where possible;
- preserve streaming behavior for large files.

Important distinction:

- `match_count` remains the count represented by returned/scanned output, not necessarily whole repo truth;
- `search_stats.counts_are_complete` tells the caller whether it can treat counts as complete.

## Traversal Stats Direction

Current file walk tracks `dot_entries_skipped` as a bool. Phase 4 needs richer stats for `grep`.

Preferred direction:

- extend file walk stats or add grep-specific traversal stats without breaking `glob_file_search`;
- count hidden skipped entries when possible;
- count ignored path skips when possible;
- count binary skips in grep filtering;
- count type/glob filtered files in grep filtering;
- count files seen/searched/with matches.

Do not overclaim exact counts where the current traversal cannot know them cheaply. If a stat is not exact enough, either omit it or document it as selected-scope count in SRS.

## Pattern Compilation

Current compilation prepends `(?i)` and `(?s)`.

Phase 4 direction:

- for `regex`, keep current behavior;
- for `literal`, apply regexp quoting before adding flags;
- preserve invalid-regex errors for regex mode;
- literal mode should not fail because of regex metacharacters.

## Line Window Search

For small files, line-window can reuse existing decoded content line splitting.

For large files:

- line mode should stream and skip until `start_line`, then stop after `end_line`;
- multiline mode may keep current memory guard and apply the line slice after decoding;
- if implementation cannot keep multiline line-window efficient in the first pass, SRS must define a bounded safe path, not silently scan outside the window.

## Read Range Construction

Read ranges should help the next `read_file` call.

Rules:

- for each group, build ranges from match rows and context rows;
- merge ranges when the gap is small;
- keep ranges bounded so `file_groups` does not become huge;
- `read_ranges` should not exceed a small per-file maximum chosen in SRS;
- when `line_window` was used, suggested ranges stay within that window unless the input context explicitly included surrounding lines.

## Recommendation Policy

Recommended call policy should be deterministic and testable.

Suggested priority:

1. If there is at least one file group with a read range and result is not dominated by truncation, recommend `read_file` for the first useful range.
2. If global limit truncated before enough file diversity, recommend a narrower `grep` with `files_with_matches`, `type`, `glob`, `max_matches_per_file`, or smaller path when possible.
3. If one file has many returned matches, recommend `outline_file` for that file or a `read_file` range around the densest group.
4. If no matches and pattern looks regex-like, recommend `pattern_mode=literal` only when safe and not already literal.
5. If no matches and case-sensitive mode was used, recommend `case_insensitive=true` only when useful.

Do not generate many alternatives. One top-level hint is enough.

## CWD And Path Projection

All new path-bearing fields must follow Phase 3 rules:

- without `cwd_id`, slash-normalized absolute/display paths;
- with `cwd_id`, `cwd` absolute metadata plus cwd-relative path fields;
- no leading `./` except `.` for cwd itself;
- no absolute leaks in `file_groups`, `read_ranges` recommended inputs, or `next_recommended_call`.

`sanitizeGrepOutput` must be extended to cover:

- `file_groups[].path`;
- `next_recommended_call.recommended_next_input.path`;
- `next_recommended_call.recommended_next_input.target_file`;
- nested fields added by SRS.

## Schema And Docs

Update:

- `GrepToolInput` schema for `pattern_mode`, `line_window`, `max_matches_per_file`;
- `GrepOutput` schema for `search_stats`, `file_groups`, `next_recommended_call`;
- server tool description;
- `TOOLS.md`;
- `README.md` summary if needed;
- `server.json` metadata if it includes grep description.

Tool description should say:

```text
grep is not just rg-like text output. It returns line evidence, file groups, search completeness stats, and a recommended next call for agent navigation.
```

## Test Direction

Required test categories:

- existing grep tests still pass;
- literal mode finds regex-looking text without escaping;
- regex mode remains default and still rejects invalid regex;
- file groups are produced for content matches;
- read ranges are merged and bounded;
- next recommended `read_file` includes cwd-aware relative paths;
- no absolute path leaks in cwd-aware new fields;
- truncation sets `completed=false`, `stop_reason=limit`, and incomplete counts;
- no-match output has search stats and a useful recommendation when applicable;
- `max_matches_per_file` prevents one file from consuming all returned rows;
- line-window works for single file and errors on directory path;
- multiline grouping and ranges are line-correct;
- dotfile/VCS behavior is unchanged;
- large-file streaming behavior is preserved;
- schema exposes new fields and still omits cursor/nextCursor.

## Risks

- Overcomplicated schema could make `grep` harder rather than easier.
- Stats can mislead if they imply complete counts after early stop.
- Recommendations can become noisy if there is more than one.
- File grouping can bloat output if read ranges are not bounded.
- Literal mode must not change default regex behavior.
- Per-file cap must not silently hide that a file was capped.
- Cwd-aware recommended inputs can leak absolute paths if sanitization is missed.

## Key Technical Decisions

- No new output mode is required for `file_groups`; it is additive.
- No hidden reranking by default.
- No `.gitignore` support in Phase 4.
- No broad dotfile traversal change in Phase 4.
- No semantic search or AST parsing inside `grep`.
- No cursor/stateful pagination.
- Current `limit` remains the global returned row/file/count cap.
- New stats explain whether the result can be treated as complete.

## Open Questions

None blocking.

Exact field names, per-file range limits, and recommendation priority details are SRS-level decisions as long as they preserve this concept.
