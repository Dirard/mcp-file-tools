# Stage 2: outline_file

## Goal

Implement `outline_file` as a read-only per-file structure and fingerprint tool that gives agents exact Markdown/Go ranges without block bodies and without full-file token dumps.

## Depends On

- Stage 1 contracts and schemas.
- Existing path validation, path-map display, limiter, encoding detection, and line-count helpers.

## Touched Areas

- new `filetoolsserver/handler/outline_file.go`
- new `filetoolsserver/handler/outline_markdown.go`
- new `filetoolsserver/handler/outline_go.go`
- new `filetoolsserver/handler/fingerprint.go`
- tests in `filetoolsserver/handler/outline_file_test.go`

## Behavior Contract

Input supports:

- `target_file`
- `language`: `auto`, `markdown`, `go`, or future values rejected/unsupported as appropriate.
- `output_profile`: `outline` or `fingerprint_only`.
- `include_imports`, `include_symbols`, `include_sections`.
- `line_window`, `name_contains`, `kinds`.
- `max_items`, default `500`.
- `max_depth`, default `8`.

Output always includes:

- `file`
- `language`
- `parser_status`
- full-file `fingerprint`
- arrays `imports`, `symbols`, `sections` unless `fingerprint_only`
- `outline_stats`
- `truncated`
- `warnings`

## Steps

1. Implement fingerprint helper:
   - Open regular text file after `ResolvePath`.
   - Reject directories and binary files.
   - Compute sha256 while using the shared Phase 2 line-index helper; do not count raw `\n` separately from write-range semantics.
   - `fingerprint.line_count` must match the line count that Phase 2 range validation uses and the whole-file `read_file` display-line contract: empty file -> 0; non-empty file -> decoded LF/CRLF line terminator count plus 1, so a file ending with LF/CRLF has an addressable final empty line.
   - Include `size_bytes`, `line_count`, `modified_unix_nano`.
   - Preserve path-map display in output `file`.
   - Use streaming reads; do not load whole file just to hash.

2. Implement `fingerprint_only` profile:
   - Return file, language detection, `parser_status: "fingerprint_only"`, fingerprint, warnings.
   - Do not run expensive parser work unless needed to identify warnings cheaply.
   - This is the canonical workflow for target preconditions.

3. Implement language detection:
   - `.md`, `.markdown` -> markdown.
   - `.go` -> go.
   - Unknown extension -> unsupported.
   - Explicit language overrides auto only when supported; unsupported explicit language returns structured unsupported output, not fake outline.

4. Implement Markdown ATX scanner:
   - Detect `#` through `######` headings with 0-3 leading spaces.
   - Require whitespace or EOL after opening hash sequence.
   - Support optional closing hashes.
   - Treat 4+ leading spaces as code.
   - Ignore escaped heading markers.
   - Ignore headings inside backtick or tilde fenced code blocks.
   - Treat unclosed fence as fenced until EOF and add warning.
   - Support frontmatter as `kind: "frontmatter"` range when present, but do not move it automatically.
   - Set `parser_scope: "markdown_atx_headings"`.
   - Warn on cheap Setext detection with `setext_headings_unsupported`.

5. Compute Markdown section ranges:
   - Section starts at heading line.
   - Section ends before the next heading of same or higher level, or EOF.
   - Nest children under parent up to `max_depth`.
   - Returned child ranges remain full original-file line ranges.

6. Implement Go outline:
   - Use `go/parser` with comments.
   - Guard parser cost before `go/parser`: if file size exceeds `MCP_WRITE_THRESHOLD` or a dedicated `MCP_OUTLINE_PARSE_THRESHOLD` introduced during implementation, return an agent-actionable `outline_parse_threshold_exceeded` structured output with fingerprint metadata only.
   - Do not recommend `line_window` as recovery for Go threshold failures unless a real window-capable Go parser is implemented in a later phase; Stage 1 Go outline is whole-file AST parse.
   - The threshold recovery recommendation may suggest `output_profile: "fingerprint_only"` for preconditions, a smaller source file, or an explicit threshold/config decision, but it must not imply exact Go ranges are available from a window retry.
   - Default parser threshold for Stage 1 is 64 MiB unless implementation introduces a separate config with the same default and tests.
   - The error/fallback must still return fingerprint metadata when it can be computed safely.
   - Return package/file preamble when useful.
   - Return import blocks, const/var/type blocks, funcs, methods.
   - Include immediately attached doc comments in declaration range.
   - Do not include trailing comments/blank lines unless inside AST node.
   - No reference search, no dependency graph, no formatting.

7. Add stable item identity:
   - Each item gets `id` stable only for the returned fingerprint.
   - Each item gets `path` array useful for agent reasoning.
   - IDs include language/kind/name/range enough to avoid collisions where practical.
   - Do not let write tools accept IDs as safety input; writes still use ranges and full-file fingerprint.

8. Implement filters and truncation:
   - `line_window` includes items whose full range intersects the window.
   - `name_contains` filters by visible name/title/signature label.
   - `kinds` filters normalized kinds.
   - Apply `max_items` and `max_depth` after filtering.
   - If truncated, include `outline_stats.items_returned`, `items_omitted` or `items_omitted_known=false`, `last_included_line`, `truncation_reason`, and bounded `next_recommended_call`.
   - Do not introduce cursor state.

9. Implement unsupported output:
   - Return fingerprint and empty arrays.
   - `parser_status: "unsupported"`.
   - Warning: exact outline unsupported but fingerprint usable for target precondition.

## Checks

- Markdown tests:
  - nested headings;
  - fenced code headings ignored;
  - tilde fences ignored;
  - unclosed fence warning;
  - escaped hash ignored;
  - 0-3 leading spaces accepted;
  - 4 spaces rejected;
  - `#tag` not heading;
  - Setext warning.

- Go tests:
  - functions, methods, type blocks, const/var blocks, import blocks;
  - doc comments included;
  - build tags/package comments not silently attached to first symbol;
  - parser errors become actionable errors.

- Agent ergonomics tests:
  - `fingerprint_only` avoids outline arrays;
  - truncated output includes `next_recommended_call`;
  - `line_window` includes intersecting item with full range.

- Go performance tests:
  - representative large Go fixture below threshold parses successfully;
  - file above parser threshold returns actionable threshold error/fingerprint output rather than exhausting memory;
  - threshold output does not recommend `line_window` retry unless a later implementation adds real window parsing;
  - `max_items` and `max_depth` limit output size, while parser threshold limits parse cost.

- Fingerprint tests:
  - same file returns stable sha/size/line_count;
  - line_count matches the shared Phase 2 line-index helper for empty, no-final-newline, final-newline, CRLF, and EOF-without-newline fixtures;
  - modified file changes sha or mtime;
  - binary file rejected.

## Handoff / Next Stage

Move to `03-range-transfer-engine.md` after `outline_file` can supply fingerprints/ranges for all later write tests.

## Stop And Ask If

- Go parser cannot produce exact ranges for a construct without a clear documented fallback.
- Markdown exactness would require broader CommonMark behavior than ATX scope.
