# Phase 1 Agent Navigation Technical Concept

concept_version_label: phase1-agent-navigation-v1
status: draft, pending user acceptance

## Technical Direction

Фаза 1 добавляет agent-navigation foundation поверх текущих read-only file tools.

Ключевой принцип: additive, stateless, bounded, backward-compatible.

Текущие default outputs остаются совместимыми. Новое structured поведение включается только явно.

## C-001: Preserve Small Read-Only Surface

Текущие инструменты остаются:

- `read_file`
- `list_dir`
- `glob_file_search`
- `grep`

Добавляется только один новый tool:

- `inspect_path`

No mutating tools. No `read_multiple`.

Сервер не становится индексатором и не хранит долговременное состояние workspace.

## C-002: `inspect_path`

### Purpose

Дешево проверить один путь перед чтением/поиском.

### Input

```json
{
  "path": "/path/to/file-or-dir"
}
```

Optional future-safe fields are allowed only after separate concept/plan decision. Phase 1 input should stay minimal.

### Output Shape

The tool follows the existing MCP response envelope:

```json
{
  "text": "{\"path\":\"/path/to/file-or-dir\",\"display_path\":\"/path/to/file-or-dir\",\"exists\":true,\"kind\":\"file\",\"size_bytes\":12345,\"modified_at\":\"2026-06-04T12:00:00Z\",\"readable\":null,\"readability_basis\":\"unknown\",\"symlink\":false}",
  "nextCursor": null
}
```

The `text` field is a complete valid compact JSON object. `inspect_path` should normally fit in one page and should not need `nextCursor`.

Minimum semantic fields inside `text`:

```json
{
  "path": "/path/to/file-or-dir",
  "display_path": "/path/to/file-or-dir",
  "exists": true,
  "kind": "file",
  "size_bytes": 12345,
  "modified_at": "2026-06-04T12:00:00Z",
  "readable": null,
  "readability_basis": "unknown",
  "symlink": false,
  "target_kind": null
}
```

`path` is the canonical call path: agents may pass it verbatim to `read_file`, `grep`, `glob_file_search`, or another `inspect_path` call. `display_path` is human-facing and must not be required for follow-up calls.

### Cheapness Contract

Allowed:

- one path resolution;
- `stat` / `lstat`;
- permission/readability probe only if cheap, non-recursive, and basis-aware;
- path mapping/display normalization already used by existing tools.

Forbidden:

- directory traversal;
- child count;
- recursive size;
- content preview;
- hash;
- MIME sniffing that reads file content;
- encoding detection that reads the file;
- JSON/language parsing.

### Error Semantics

Missing path and invalid path must be agent-actionable.

Use structured errors for invalid input/path failures:

```json
{
  "text": "",
  "error": {
    "code": "invalid_path",
    "message": "..."
  }
}
```

If a missing path is treated as successful inspection, it must be consistent and explicit:

```json
{
  "exists": false,
  "kind": "missing"
}
```

Concept preference:

- missing path is a non-error inspection result: `exists: false`, `kind: "missing"`;
- unreadable existing path is also an inspection result where possible: `readable: false` or `null`;
- invalid path syntax or path mapping failures are structured errors.

`readable` is tri-state:

- `true`: a cheap check strongly indicates readability;
- `false`: a cheap check strongly indicates not readable;
- `null`: unknown.

`readability_basis` explains the signal: `stat`, `open_probe`, or `unknown`.

For symlinks/junctions, `kind` reports the immediate filesystem object. `target_kind` may report the resolved target only if resolving it is cheap and does not require traversal beyond normal path resolution.

## C-003: `read_file` Remains The Range Reader

No `read_ranges` in phase 1.

No schema change is required for `read_file`.

Must preserve:

- `target_file`;
- `start_line`;
- `end_line`;
- `cursor`;
- 1-based inclusive line ranges;
- soft clipping when `end_line` is beyond EOF;
- bounded range fast path when both bounds are set;
- line-numbered output;
- CRLF normalization;
- empty line preservation;
- char-level continuation for long lines.

Agent flow after structured grep:

```text
match.line = 119
agent calls read_file(target_file=match.path, start_line=99, end_line=139)
```

This is intentionally explicit. It avoids fuzzy `read_around` and avoids hidden batch reads.

## C-004: Structured `grep`

### Default Compatibility

Existing `grep` output modes remain unchanged:

- `content`
- `files_with_matches`
- `count`

Add:

- `structured`

Only `output_mode: "structured"` returns structured match objects. Plain text `content` remains default.

### Structured Match Shape

`output_mode: "structured"` uses the existing MCP tool response envelope:

```json
{
  "text": "{\"matches\":[...],\"truncated\":false}",
  "nextCursor": "g:12:0"
}
```

The `text` field is a complete valid compact JSON object on every page. `nextCursor` remains the normal top-level continuation field. Do not put partial JSON in `text`.

Minimal JSON object inside `text`:

```json
{
  "matches": [
    {
      "path": "/path/to/file.go",
      "line": 119,
      "text": "func example() {",
      "source_ref": {
        "path": "/path/to/file.go",
        "line": 119
      }
    }
  ],
  "truncated": false
}
```

No-match result in structured mode is also valid JSON, not a friendly plain-text message:

```json
{
  "matches": [],
  "truncated": false
}
```

No-match in structured mode is not an error and has no `nextCursor`.

Minimal match fields:

```json
{
  "path": "/path/to/file.go",
  "line": 119,
  "text": "func example() {",
  "source_ref": {
    "path": "/path/to/file.go",
    "line": 119
  }
}
```

`path` and `source_ref.path` are canonical call paths accepted verbatim by `read_file` and `grep`. `display_path`, if included, is human-facing only.

Optional match fields if cheap/reliable:

```json
{
  "column": 7,
  "end_column": 14,
  "match_text": "example"
}
```

### Pagination Problem To Solve

Structured output must stay valid on every page. Do not split raw JSON in the middle of an object.

For long match lines, use a fragment model instead of invalid JSON slicing:

```json
{
  "matches": [
    {
      "path": "/path/to/file.go",
      "line": 119,
      "text_fragment": "first part...",
      "text_continues": true,
      "fragment_start_byte": 0,
      "source_ref": {
        "path": "/path/to/file.go",
        "line": 119
      }
    }
  ],
  "truncated": true
}
```

Continuation cursor then resumes at the next fragment boundary. Each page remains valid structured output.

Fragments are measured by UTF-8 byte offsets in the original decoded line and must only split on valid UTF-8 boundaries. The serialized JSON page must stay under the internal response budget after JSON escaping.

### Unsupported Combinations

Phase 1 structured mode is intentionally narrow.

Return a structured error when `output_mode: "structured"` is combined with:

- `output_mode: "structured"` with multiline matches;
- `structured` plus `-A`, `-B`, or `-C` context lines;
- `structured` plus `files_with_matches` or `count` semantics.

The error must be explicit and actionable:

```json
{
  "text": "",
  "error": {
    "code": "unsupported_structured_grep_combination",
    "message": "structured grep in phase 1 supports line matches only; use output_mode=content for context, count, files_with_matches, or multiline"
  }
}
```

## C-005: Shared Cursor, Budget, Error Contract

### Budget

All tools must target serialized MCP output under the current Codex budget, approximately 10 KiB.

Implementation should use an internal lower limit to leave framing margin. Budget is measured after JSON/text serialization and escaping of the tool payload, before it is returned to the MCP client.

### Cursor

Cursor remains:

- compact;
- opaque to agents;
- copied exactly from `nextCursor`;
- tool/request scoped;
- invalid if used with incompatible parameters;
- independent across concurrent agents.

Cursor should encode enough to resume without server-side session state:

- cursor version;
- tool/output kind;
- logical position;
- char offset when continuing an existing text-tool long line;
- UTF-8 byte offset when continuing a structured `grep` fragment;
- parameter hash if needed to reject wrong-request reuse.

### Result Classes

Keep these distinct:

1. Successful page with complete result.
2. Successful partial page with `nextCursor`.
3. Friendly no-result message, not an error.
4. Structured tool error with empty `text`.

Do not collapse no-result into error.

Do not duplicate error text in plain-text content.

For `output_mode: "structured"`, no-result is the structured JSON object `{"matches":[],"truncated":false}` inside `text`; friendly plain-text no-result messages are for non-structured modes only.

## C-006: Source Anchors

Use a single conceptual `source_ref` shape wherever a structured result points to source text:

```json
{
  "path": "/path/to/file.go",
  "line": 119
}
```

Optional cheap extension:

```json
{
  "path": "/path/to/file.go",
  "line": 119,
  "column": 7,
  "end_line": 119,
  "end_column": 14
}
```

`source_ref` is an action anchor, not a permanent immutable snapshot. If files change between calls, the server may continue best-effort or return an actionable cursor/path error.

Canonical path rule:

- `source_ref.path` is the value agents should pass to follow-up calls;
- `path` in structured matches is the same canonical call path;
- `display_path`, if present, is human-facing only;
- host/container path mapping must be applied before returning `source_ref.path`, so the value is directly accepted by the same MCP server in later calls.

## C-007: `glob_file_search` Phase 1 Scope

Default behavior must remain unchanged:

- recursive simple patterns;
- `**` semantics;
- brace expansion;
- dotfile traversal rules;
- `ignore_globs` directory pruning;
- newest-first output;
- cursor pagination.

No new `glob_file_search` parameters are implemented in phase 1.

Deferred ideas, not part of accepted phase 1 scope:

- `sort`;
- `modified_since`;
- `max_entries`;
- `include_metadata`;
- separate `recent_files`.

Reason: the current glob tool already supports the basic navigation need. Adding sorting/filtering now would broaden phase 1 beyond the core `inspect_path` + structured `grep` + shared contract work.

## C-008: Cross-Platform Path Semantics

Must work on:

- Windows native paths;
- Linux/macOS POSIX paths;
- Docker-mounted paths with configured mapping.

Rules:

- input paths follow server OS and existing path mapping behavior;
- output paths must be immediately usable as input to follow-up MCP calls;
- do not force agents to translate `/mnt/d/...` vs `D:\...` manually when path maps are configured;
- handle drive letters, mixed slashes, symlink/junction reporting, hidden explicit paths, and permission errors consistently.

## C-009: Speed And Concurrency

All phase 1 features must stay bounded.

No new long-lived global mutable state.

No project-wide cache required for correctness.

Concurrent tool calls from multiple agents must not share cursor state or leak errors/results between calls.

Verification must include mixed parallel calls:

```text
inspect_path + grep structured + read_file(start_line,end_line) + glob_file_search
```

## C-010: Hard Non-Goals

Do not implement in phase 1:

- `list_tree`;
- `workspace_inventory`;
- `outline_file`;
- JSON tools;
- JSONL tools;
- language parsers;
- semantic symbol search;
- file/project index;
- watcher;
- mutating tools;
- `read_multiple`;
- `read_ranges`;
- `read_around`.

## Review And Verification Expectations

Before implementation is accepted, independent review must check:

- current four default tools did not regress;
- `inspect_path` is genuinely cheap;
- structured `grep` pages are always valid structured output;
- long-line continuation works in structured and text outputs;
- no-result/error/partial-page semantics remain distinct;
- Windows/Linux/Docker path examples work;
- concurrent calls do not share cursor state;
- `glob_file_search` additions, if implemented, do not change old glob behavior.

Minimum test areas:

- long Unicode line at page boundary;
- CRLF and empty lines;
- hidden explicit file vs hidden traversal;
- binary file near text files;
- malformed cursor and wrong-tool cursor;
- `end_line` beyond EOF remains soft-clipped;
- no-match grep remains non-error;
- Docker/Windows path mapping parity;
- parallel mixed tool calls.

## Acceptance Record Draft

This concept is not accepted yet.

```text
accepted_concept_record:
  concept_version_label: phase1-agent-navigation-v1
  status: accepted
  accepted_by: user
  accepted_scope:
    - C-001
    - C-002
    - C-003
    - C-004
    - C-005
    - C-006
    - C-007
    - C-008
    - C-009
    - C-010
```
