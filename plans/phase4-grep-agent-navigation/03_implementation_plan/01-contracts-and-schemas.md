# Stage 1: Contracts And Schemas

## Goal

Define the public input/output contract for Phase 4 grep before changing search behavior.

## Depends On

- Clean-approved concept docs:
  - `plans/phase4-grep-agent-navigation/01_human_concept.md`
  - `plans/phase4-grep-agent-navigation/02_technical_concept.md`

## Touched Areas

- `filetoolsserver/handler/tool_types.go`
- `filetoolsserver/handler/middleware.go`
- `filetoolsserver/handler/schema_constraints.go`
- `filetoolsserver/server.go`
- `filetoolsserver/handler/agent_tools_test.go`
- `TOOLS.md`
- `README.md`
- `server.json`

## Input DTO Changes

Add to `GrepToolInput`:

```go
PatternMode       string           `json:"pattern_mode,omitempty"`
LineWindow        *SourceLineRange `json:"line_window,omitempty"`
MaxMatchesPerFile *int             `json:"max_matches_per_file,omitempty"`
```

`SourceLineRange` already exists and uses:

```json
{ "start_line": 1, "end_line": 10 }
```

Update `GrepToolInput.UnmarshalJSON` to preserve:

- current JSON-native flags;
- legacy dash aliases `-A`, `-B`, `-C`, `-i`;
- cwd-aware custom decoding path managed by the server wrapper;
- no accidental exposure of dash aliases in schema.

## Output DTO Changes

Add:

```go
type GrepSearchStats struct {
    FilesSeen         int    `json:"files_seen"`
    FilesSearched     int    `json:"files_searched"`
    FilesWithMatches  int    `json:"files_with_matches"`
    SkippedHidden     int    `json:"skipped_hidden"`
    SkippedIgnored    int    `json:"skipped_ignored"`
    SkippedVCS        int    `json:"skipped_vcs"`
    SkippedBinary     int    `json:"skipped_binary"`
    SkippedUnreadable int    `json:"skipped_unreadable"`
    SkippedTypeOrGlob int    `json:"skipped_type_or_glob"`
    FilesCapped       int    `json:"files_capped"`
    Completed         bool   `json:"completed"`
    StopReason        string `json:"stop_reason,omitempty"`
    CountsAreComplete bool   `json:"counts_are_complete"`
}

type GrepFileGroup struct {
    Path       string          `json:"path"`
    MatchCount int             `json:"match_count"`
    RowCount   int             `json:"row_count"`
    FirstLine  int             `json:"first_line,omitempty"`
    LastLine   int             `json:"last_line,omitempty"`
    ReadRanges []SourceLineRange `json:"read_ranges"`
    Capped     bool            `json:"capped,omitempty"`
}
```

Add to `GrepOutput`:

```go
PatternMode         string           `json:"pattern_mode,omitempty"`
LineWindow          *SourceLineRange  `json:"line_window,omitempty"`
SearchStats         *GrepSearchStats  `json:"search_stats,omitempty"`
FileGroups          []GrepFileGroup   `json:"file_groups"`
NextRecommendedCall *ActionHint       `json:"next_recommended_call,omitempty"`
```

`GrepSearchStats` is present on successful grep outputs only, including no-match outputs. It is omitted on validation, path resolution, regex, cancellation, memory-threshold, and other tool-error outputs so zero values cannot be mistaken for exact successful-scope stats.

`GrepSearchStats` integer counters are exact for the reached search scope. When `completed=true`, reached scope equals the full selected scope. When `completed=false`, counters are exact only for files and traversal entries reached before the stop or skip condition, and must not be documented or treated as full-scope totals. There is no best-effort `0`: if implementation cannot track one of these counters exactly for the reached scope, root must return to the plan instead of shipping the field. In an incomplete result, a zero counter means exactly zero in the reached scope only, not proof of zero in the unreached part of the selected scope.

All `GrepSearchStats` integer counters are present in JSON, including `files_capped=0`. Do not use `omitempty` on these counters; absence would make exact-zero and not-reported indistinguishable for agents.

Exact counter definitions:

- `files_seen`: regular file candidates delivered to grep after hidden/ignore traversal filtering, before grep-specific `type`, `glob`, and binary filtering.
- `files_searched`: files whose content was searched after `type`, `glob`, and binary filtering.
- `files_with_matches`: distinct files that contributed returned match evidence in the selected output mode. In `content`, this means at least one returned `matches[]` row with `kind="match"` for that file. In `files_with_matches`, this means the file appears in `files[]`. In `count`, this means the file appears in `counts[]`. Discovered-but-not-returned matches at a global-limit or per-file-cap boundary are not counted.
- `skipped_hidden`: dot-prefix filesystem entries skipped by traversal; skipped directories count as one entry, not as all descendants. Do not add OS-specific hidden-attribute behavior in Phase 4.
- `skipped_ignored`: filesystem entries skipped because a user-provided `ignore_globs` pattern matched; skipped directories count as one entry, not as all descendants.
- `skipped_vcs`: filesystem entries skipped by built-in VCS pruning for `.git`, `.hg`, `.svn`, or `.jj`; skipped directories count as one entry, not as all descendants.
- `skipped_binary`: files skipped because binary detection rejected them.
- `skipped_unreadable`: files skipped because open/read/encoding-probe failed after traversal selected them.
- `skipped_type_or_glob`: files rejected by grep-specific `type` or `glob`.
- `files_capped`: files where `max_matches_per_file` actually suppressed additional content evidence after the returned cap.
- `completed`: true only when returned evidence and mode-specific counts cover the full selected scope without global limit stop, per-file cap, or unreadable selected files.
- `stop_reason`: empty when `completed=true`; otherwise `limit`, `file_cap`, or `unreadable` for successful outputs.
- `counts_are_complete`: false whenever global limit, per-file cap, or unreadable selected files made mode-specific counts incomplete.

Binary skip semantics:

- `skipped_binary > 0` does not by itself make `completed=false`.
- Binary files are outside grep's returned text evidence domain under the existing binary guard.
- If binary skips are the only skip/incompleteness source, successful output has `completed=true`, `counts_are_complete=true`, and `stop_reason=""`, while `skipped_binary` still tells the agent what was excluded.

If `skipped_unreadable > 0`, successful output uses:

- `completed=false`;
- `counts_are_complete=false`;
- `stop_reason="unreadable"`, unless `limit` or `file_cap` also happened; `limit` wins over `file_cap`, and both win over `unreadable`.

## Compatibility Rules

1. Existing fields remain.
2. Existing mode-specific arrays remain:
   - `content` -> `matches`;
   - `files_with_matches` -> `files`;
   - `count` -> `counts`.
3. `file_groups` is additive for navigation and does not replace mode arrays.
4. `file_groups` is populated only in `content` mode. In `files_with_matches` and `count`, it is `[]` to avoid duplicating `files[]` or `counts[]`.
5. `search_stats` is omitted on grep tool errors and present on all successful grep outputs.
6. `line_window`, when present in input, is echoed in successful and no-match output.
7. `pattern_mode` is echoed as `regex` or `literal` on successful outputs; omitted input becomes `regex`.
8. `truncated` remains present as a legacy coarse incompleteness flag: it is `true` for successful outputs whose returned evidence is incomplete because of global `limit`, per-file cap suppression, or unreadable selected files; `search_stats.stop_reason` is authoritative for the precise cause.
9. `Text` remains suppressed from MCP content and still exists only for internal legacy text formatting.
10. No `cursor` or `nextCursor`.

## Schema Rules

Update schema tests to verify:

- input schema exposes `pattern_mode`, `line_window`, `max_matches_per_file`;
- input schema constrains `pattern_mode` to enum values `regex` and `literal` while runtime still treats omitted input as `regex`;
- input schema constrains `max_matches_per_file` with `minimum=1`;
- input schema constrains `line_window.start_line` and `line_window.end_line` with `minimum=1`;
- output schema exposes `pattern_mode`, `line_window`, `search_stats`, `file_groups`, `next_recommended_call`;
- `file_groups[].path` has path output constraints;
- `file_groups[].read_ranges[]` has `start_line` and `end_line`;
- `next_recommended_call.recommended_next_input` path fields remain constrained by existing recommended-input schema logic;
- dash aliases remain absent from public schema;
- cursor fields remain absent.

## Documentation Contract

Docs must describe `grep` as agent navigation:

- line evidence: `matches`, `files`, `counts`;
- navigation summary: `file_groups`;
- completeness: `search_stats`;
- next action: `next_recommended_call`;
- literal search: `pattern_mode=literal`;
- compatibility: regex remains default.

## Checks

- Schema unit tests for new fields.
- Compile after DTO additions.
- Existing output marshal empty-collection tests update `GrepOutput` so `file_groups` is always an empty array on success, no-match output, and grep validation/tool-error outputs.
- Error-output tests assert `search_stats` is omitted from marshalled JSON.
- `setStructuredErrorOutput` in `middleware.go` initializes `GrepOutput.FileGroups` to `[]GrepFileGroup{}` for panic/tool-error consistency.
- Cwd/error wrapper tests continue through the existing structured error path and verify `file_groups=[]`.

## Stop And Ask If

- A schema generator limitation would make new fields misleading in tool discovery.
- A field name collides with current public behavior.
