# Phase 2 Agent Refactor Tools Technical Concept

concept_version_label: phase2-agent-refactor-v1
status: accepted for implementation planning
acceptance_record: user accepted this concept in the current planning thread

## Technical Direction

Фаза 2 добавляет пять MCP tools:

- `outline_file` - read-only per-file outline and fingerprint.
- `copy_ranges` - write exact source line ranges into one target without changing source.
- `move_ranges` - write exact source line ranges into one target and remove them from source after target write succeeds.
- `copy_ranges_batch` - copy exact ranges from one source snapshot into multiple explicit targets.
- `move_ranges_batch` - write exact ranges from one source snapshot into multiple explicit targets and then remove all moved source ranges once.

Core implementation may share internal range-transfer engine, but public tools stay separate because `move_ranges` has destructive source-edit semantics.

All tools remain stateless, per-call, bounded, JSON-native, and path-explicit.

Agent ergonomics is part of the contract. A successful implementation must minimize guesswork: outputs should include next fingerprints, dry-run diagnostics, boundary warnings, and structured recovery hints where they materially reduce follow-up tool calls.

## Shared Types

### Absolute Path Inputs

Every path input must be:

- present;
- non-empty after trim;
- absolute for the server OS;
- resolved through the existing path-map rules;
- rejected if relative or ambiguous.

Write tools reject symlink paths by default in MVP. A future explicit `follow_symlinks` option would need a separate safety decision.

### File Identity And Symlink Safety

For write tools, identity checks are part of validation:

- reject if `source_file` resolves to the same filesystem object as any target file;
- reject same-file operations for all write tools in MVP;
- compare identity with the strongest cheap signal available on the OS: canonical cleaned path plus file ID/inode when available; on Windows also account for case-insensitive path equality;
- reject final-path symlinks for any read or write file path;
- for every file that may be mutated, reject symlink components anywhere in the parent directory chain after path-map resolution;
- apply parent-chain symlink rejection to target writes in `create_new`, `append`, `prepend`, `insert_before_line`, and `replace_range`;
- apply parent-chain symlink rejection to source writes in `move_ranges` and `move_ranges_batch`;
- `create_new` must use exclusive create or equivalent final no-exist check so an external create race is not silently overwritten;
- hardlinks that resolve to the same file identity are treated as same-file operations and rejected.

These rules are intentionally conservative. A future explicit follow-symlink mode would need a separate product and safety decision.

### Line Ranges

Line ranges use the existing `read_file` convention:

```json
{
  "start_line": 10,
  "end_line": 40
}
```

Rules:

- 1-based inclusive;
- `start_line >= 1`;
- `end_line >= start_line`;
- source ranges are interpreted against the original source snapshot;
- overlapping ranges are rejected;
- adjacent ranges are allowed but reported as normalized/adjacent;
- extraction order follows request order;
- move tools delete source ranges internally from bottom to top.

### Line-To-Byte Extraction Semantics

Write tools move raw byte spans derived from line ranges.

For source extraction:

- selected bytes start at the first byte of `start_line`;
- selected bytes end after the line terminator of `end_line`, if that terminator exists;
- if `end_line` is the last line and EOF has no final newline, selected bytes end at EOF;
- CRLF, LF, indentation, comments, blank lines, and all selected bytes are preserved exactly;
- a UTF-8 BOM belongs to line 1 and is selected only if the range starts at line 1;
- ranges never normalize line endings.

For target replacement:

- `replace_range` removes bytes from the first byte of target `start_line` through the line terminator of target `end_line`, if present;
- if replacement ends at EOF without newline, it ends at EOF;
- inserted source bytes and explicit `joiner` bytes are then placed at that span.

`joiner` bytes are inserted only between multiple extracted ranges. They are never inserted before the first range or after the last range.

### File Fingerprint

Fingerprint shape:

```json
{
  "sha256": "hex",
  "size_bytes": 12345,
  "line_count": 900,
  "modified_unix_nano": 1780600000000000000
}
```

`sha256` is the write gate. Size, line count, and mtime are diagnostics and useful error context.

Write tools reject if the current file hash differs from the supplied fingerprint.

### Agent Action Hint

Errors and high-risk warnings use a compact, structured next-step hint:

```json
{
  "safe_to_retry": false,
  "recommended_next_tool": "outline_file",
  "recommended_next_input": {
    "target_file": "/abs/source.go",
    "output_profile": "outline"
  },
  "reason": "source changed; ranges must be selected against a fresh fingerprint"
}
```

`recommended_next_input` must never contain relative paths. It may echo validated absolute paths and simple parameters, but it must not invent product decisions such as target file names.

### Next Fingerprints

Successful write outputs include fingerprints useful for the next agent step:

```json
{
  "source_fingerprint_for_next_write": {"sha256": "hex"},
  "target_fingerprint_for_next_write": {"sha256": "hex"}
}
```

For `copy_ranges`, source is unchanged by the tool, but the field still reports the final source fingerprint that allowed the target write. For `move_ranges`, source fingerprint is the post-delete fingerprint. Batch outputs report per-target next fingerprints and one source fingerprint.

### Text Encoding Scope

MVP write tools support UTF-8-compatible text files only:

- UTF-8;
- ASCII;
- UTF-8 with BOM if existing encoding support treats it as text.

Reject in MVP:

- binary files;
- UTF-16/UTF-32 writes;
- mixed source/target encodings;
- files whose line boundaries cannot be mapped safely to raw byte spans.

Reason: write tools must preserve exact selected bytes. Expanding write support to UTF-16/UTF-32 is possible later, but should be explicit and tested separately.

## `outline_file`

### Purpose

Return a compact structure map and full-file fingerprint for one file.

It must not return block bodies. It returns names, kinds, signatures/labels, ranges, and fingerprints.

### Input

```json
{
  "target_file": "/abs/path/file.go",
  "language": "auto",
  "output_profile": "outline",
  "include_imports": true,
  "include_symbols": true,
  "include_sections": true,
  "line_window": null,
  "name_contains": null,
  "kinds": null,
  "max_items": 500,
  "max_depth": 8
}
```

Defaults:

- `language`: `auto`;
- `output_profile`: `outline`;
- `include_imports`: `true`;
- `include_symbols`: `true`;
- `include_sections`: `true` for Markdown, ignored for code;
- `line_window`: `null`;
- `name_contains`: `null`;
- `kinds`: `null`;
- `max_items`: `500`;
- `max_depth`: `8`.

Invalid:

- empty/relative `target_file`;
- unknown `output_profile`;
- invalid `line_window` bounds;
- `max_items < 1`;
- `max_depth < 0`;
- directory target;
- binary file.

`output_profile` values:

- `outline`: return compact outline items plus fingerprint;
- `fingerprint_only`: return file/fingerprint/parser metadata and no outline arrays unless warnings are needed.

`fingerprint_only` output example:

```json
{
  "file": "/abs/target.go",
  "language": "go",
  "parser_status": "fingerprint_only",
  "fingerprint": {
    "sha256": "hex",
    "size_bytes": 777,
    "line_count": 55,
    "modified_unix_nano": 1780600000000000000
  },
  "warnings": []
}
```

Canonical target fingerprint workflow for existing targets: call `outline_file` with `output_profile: "fingerprint_only"` and use the returned `fingerprint` as `target_precondition.fingerprint`. This works for supported and unsupported text files and avoids full-file reads.

`line_window`, `name_contains`, and `kinds` are explicit narrowing filters, not cursor pagination. They allow an agent to retry a truncated outline with a smaller result set while still receiving the full-file fingerprint for the current snapshot.

For `line_window`, include outline items that intersect the window, not only items whose start line is inside the window. Returned ranges stay full original-file ranges so they remain safe inputs to write tools.

### Output

```json
{
  "file": "/abs/path/file.go",
  "language": "go",
  "parser": "go/ast",
  "parser_status": "exact",
  "fingerprint": {
    "sha256": "hex",
    "size_bytes": 12345,
    "line_count": 900,
    "modified_unix_nano": 1780600000000000000
  },
  "imports": [
    {
      "id": "go:import_block:imports:3-12",
      "path": ["imports"],
      "kind": "import_block",
      "name": "imports",
      "range": {"start_line": 3, "end_line": 12},
      "fingerprint": "sha256:range-hash",
      "confidence": "exact"
    }
  ],
  "symbols": [
    {
      "id": "go:function:HandleReadFile:16-94",
      "path": ["HandleReadFile"],
      "kind": "function",
      "name": "HandleReadFile",
      "signature": "func (h *Handler) HandleReadFile(...)",
      "range": {"start_line": 16, "end_line": 94},
      "children": [],
      "fingerprint": "sha256:range-hash",
      "confidence": "exact",
      "range_is_estimated": false
    }
  ],
  "sections": [],
  "outline_stats": {
    "items_returned": 2,
    "items_omitted": 0,
    "max_items": 500,
    "max_depth": 8,
    "last_included_line": 94,
    "truncation_reason": null,
    "next_recommended_call": null
  },
  "truncated": false,
  "warnings": []
}
```

Markdown output uses `sections`:

```json
{
  "file": "/abs/path/concept.md",
  "language": "markdown",
  "parser": "markdown_heading_scanner",
  "parser_status": "exact",
  "parser_scope": "markdown_atx_headings",
  "sections": [
    {
      "id": "md:h1:goal:1-120",
      "path": ["Goal"],
      "kind": "markdown_section",
      "level": 1,
      "title": "Goal",
      "range": {"start_line": 1, "end_line": 120},
      "heading_line": 1,
      "children": [
        {
          "id": "md:h2:scope:20-80",
          "path": ["Goal", "Scope"],
          "kind": "markdown_section",
          "level": 2,
          "title": "Scope",
          "range": {"start_line": 20, "end_line": 80},
          "heading_line": 20,
          "children": [],
          "fingerprint": "sha256:range-hash",
          "confidence": "exact",
          "range_is_estimated": false
        }
      ],
      "fingerprint": "sha256:range-hash",
      "confidence": "exact",
      "range_is_estimated": false
    }
  ]
}
```

Outline item `id` is stable only within the returned file fingerprint. It is for agent reasoning, logging, and follow-up prompts; write tools still use explicit line ranges plus full-file fingerprints as the safety gate.

### Markdown Parser Contract

Markdown scanner:

- detects ATX headings `#` through `######` with 0-3 leading spaces;
- requires the opening `#` sequence to be followed by whitespace or end-of-line, so `#tag` is not a heading;
- supports optional closing `#` sequences in ATX headings;
- treats 4+ leading spaces as indented code, not a heading;
- ignores escaped heading markers and indented code-block lines;
- ignores headings inside fenced code blocks using backtick or tilde fences;
- treats an unclosed fence as fencing until EOF and returns a warning;
- section range starts at heading line;
- section range ends at the line before the next heading of same or higher level, or EOF;
- nested children are included under parent;
- frontmatter may be reported as `kind: "frontmatter"` range if present, but not moved automatically.
- Setext headings are not part of MVP exactness. If detected cheaply, return a warning such as `setext_headings_unsupported`; do not include them as exact sections unless support is explicitly implemented.
- `parser_status: "exact"` for Markdown means exact within `parser_scope`, not full CommonMark semantic coverage.

### Code Parser Contract

Normalized symbol kinds:

- `package`;
- `import_block`;
- `const_block`;
- `var_block`;
- `type`;
- `struct`;
- `interface`;
- `enum`;
- `class`;
- `function`;
- `method`;
- `constructor`;
- `decorator_block`;
- `unknown_block`.

Go MVP:

- parser: `go/ast`;
- exact imports, const/var/type blocks, funcs, methods;
- declaration ranges include immediately attached Go doc comments;
- build tags and package comments before the package declaration may be reported as `file_preamble`, not silently attached to the first symbol;
- trailing comments and blank lines after a declaration are excluded unless they are inside the AST node range;
- no semantic dependency graph;
- no reference search.

Tree-sitter language pack:

- parser backend isolated behind an interface;
- per-file parsing only;
- no daemon, no index, no workspace cache required for correctness;
- language support is enabled only when packaging/build cost is accepted in plan.

Unsupported language:

```json
{
  "file": "/abs/path/file.ext",
  "language": "unknown",
  "parser_status": "unsupported",
  "fingerprint": {
    "sha256": "hex",
    "size_bytes": 12345,
    "line_count": 900,
    "modified_unix_nano": 1780600000000000000
  },
  "imports": [],
  "symbols": [],
  "sections": [],
  "outline_stats": {
    "items_returned": 0,
    "items_omitted": 0,
    "max_items": 500,
    "max_depth": 8,
    "last_included_line": null,
    "truncation_reason": null,
    "next_recommended_call": null
  },
  "warnings": ["language is not supported for exact outline; fingerprint is still available"]
}
```

No default regex pretending to be exact AST. If best-effort fallback is added later, every item must have:

```json
{
  "confidence": "best_effort",
  "range_is_estimated": true
}
```

### Limits

`outline_file` must be bounded:

- `max_items` stops after N outline items and sets `truncated=true`;
- `max_depth` limits nested output;
- no cursor pagination in this phase;
- output should stay compact and omit block bodies.

When `truncated=true`, output must include recovery metadata:

```json
{
  "truncated": true,
  "outline_stats": {
    "items_returned": 500,
    "items_omitted": null,
    "items_omitted_known": false,
    "last_included_line": 1840,
    "truncation_reason": "max_items",
    "next_recommended_call": {
      "tool": "outline_file",
      "input": {
        "target_file": "/abs/path/file.go",
        "line_window": {"start_line": 1841, "end_line": 2600},
        "max_items": 500
      }
    }
  }
}
```

`next_recommended_call` must be a bounded explicit query, never hidden cursor state. If no safe recommendation can be made, return `null` and explain via warning.

## `copy_ranges`

### Purpose

Copy exact source line ranges into one target file.

### Input

```json
{
  "source_file": "/abs/source.go",
  "source_fingerprint": {
    "sha256": "hex",
    "size_bytes": 12345,
    "line_count": 900,
    "modified_unix_nano": 1780600000000000000
  },
  "ranges": [
    {"start_line": 10, "end_line": 40},
    {"start_line": 80, "end_line": 120}
  ],
  "target_file": "/abs/target.go",
  "target_precondition": {
    "must_not_exist": true
  },
  "placement": {
    "mode": "create_new"
  },
  "joiner": "none",
  "backup": {
    "mode": "none"
  },
  "dry_run": false
}
```

### Normative Input Rules

`copy_ranges` input must satisfy all of these:

- `source_file` and `target_file` are required absolute paths;
- `source_fingerprint` is required and must include `sha256`;
- `ranges` is required and non-empty;
- every range is 1-based inclusive and in bounds for the source snapshot;
- source ranges must not overlap;
- `target_precondition` has exactly one variant:
  - `{"must_not_exist": true}`;
  - `{"fingerprint": {...}}`;
- existing target writes require target fingerprint;
- new target writes require `must_not_exist`;
- target parent directory must already exist and pass symlink safety checks;
- `placement.mode` has exactly one supported shape;
- `backup.mode` is either `none` or `sidecar`;
- `dry_run` defaults to `false`;
- source and target must not resolve to the same filesystem object in MVP.

Existing target example:

```json
{
  "target_precondition": {
    "fingerprint": {
      "sha256": "hex",
      "size_bytes": 777,
      "line_count": 55,
      "modified_unix_nano": 1780600000000000000
    }
  },
  "placement": {
    "mode": "append"
  }
}
```

### Placement Modes

Allowed:

- `create_new`: target must not exist; no line field allowed.
- `append`: target must exist and fingerprint must match; no line field allowed.
- `prepend`: target must exist and fingerprint must match; no line field allowed.
- `insert_before_line`: target must exist and fingerprint must match; requires `line`, where line must be `1..target_total_lines+1`. Canonical append sentinel is `target_total_lines+1`; for a target ending with LF/CRLF, inserting before the addressable final empty line (`target_total_lines`) also maps to EOF, but tools should recommend `append` or `target_total_lines+1` for clarity.
- `replace_range`: target must exist and fingerprint must match; requires explicit target `range`, where `start_line >= 1`, `end_line >= start_line`, and `end_line <= target_total_lines`.

Avoid `insert_after_line` in public schema. Agents can compute `insert_before_line = after_line + 1`.

`replace_range` shape:

```json
{
  "mode": "replace_range",
  "range": {"start_line": 20, "end_line": 35}
}
```

### Joiner

`joiner` controls text inserted between multiple extracted ranges:

- `none`: preserve exact extracted bytes next to each other;
- `single_newline`: insert one newline between ranges;
- `blank_line`: insert two newlines between ranges.

Default: `none`.

For existing targets, newline joiners use the target's dominant newline style. For `create_new`, newline joiners use the source file's dominant newline style. If newline style cannot be determined, use `\n` and return a warning.

Joiners affect only separators between extracted ranges. Boundary newlines between existing target content and inserted content are caller-managed by selecting ranges/placement/joiner deliberately; the tool does not invent leading or trailing separators.

No auto-formatting. No newline normalization for selected range content.

Write tools must detect suspicious boundaries and return structured warnings, especially when inserted bytes and existing target bytes meet without a newline and both sides are non-empty text. `dry_run=true` returns the same warnings without mutation.

Boundary warning example:

```json
{
  "code": "boundary_may_need_newline",
  "boundary": "target_end_to_insert_start",
  "recommended_action": "rerun with joiner or adjust selected range/placement if this was not intentional"
}
```

### Dry Run

With `dry_run=true`, all write tools validate paths, fingerprints, ranges, target placement, symlink safety, encoding, line-to-byte mapping, and boundary warnings, then return the planned effect without changing any file.

Dry-run output uses the same tool-specific output schema plus tool-specific planned deltas:

```json
{
  "dry_run": true,
  "applied": false,
  "would_write_bytes": 2048,
  "would_remove_source_lines": 31,
  "preconditions_ok": true
}
```

For copy tools, omit source-removal fields or set them to `0`. For move tools, include `would_remove_source_lines` and `would_remove_source_ranges`. Batch dry-run reports planned deltas per target plus aggregate source-removal deltas for move batch.

### Write Protocol

`copy_ranges` write order:

- validate source fingerprint and target precondition before reading;
- read source/target and compute target-after content;
- immediately before any target mutation, re-check source fingerprint against the requested `source_fingerprint`;
- if the final source recheck mismatches, abort with `source_fingerprint_mismatch` and do not write target;
- immediately before target mutation, re-check target precondition with lstat/hash or exclusive create;
- if the final target recheck mismatches, abort with `target_fingerprint_mismatch`, `target_exists`, or `target_missing` and do not write target;
- write target with temp file in target directory and atomic rename where possible.

The successful output must report the exact source fingerprint observed by the final source recheck that allowed the target write.

### Output

```json
{
  "source_file": "/abs/source.go",
  "target_file": "/abs/target.go",
  "operation": "copy",
  "dry_run": false,
  "applied": true,
  "ranges": [
    {"start_line": 10, "end_line": 40, "line_count": 31}
  ],
  "target_placement": {"mode": "create_new"},
  "bytes_written": 2048,
  "lines_written": 31,
  "source_fingerprint_before": {"sha256": "hex"},
  "source_fingerprint_checked_at_write": {"sha256": "same-hex"},
  "target_fingerprint_before": null,
  "target_fingerprint_after": {"sha256": "hex"},
  "source_fingerprint_for_next_write": {"sha256": "same-hex"},
  "target_fingerprint_for_next_write": {"sha256": "hex"},
  "backup_paths": [],
  "boundary_warnings": [],
  "warnings": []
}
```

## `move_ranges`

### Purpose

Move exact source line ranges into one target file, then remove those ranges from source.

### Input

Same schema as `copy_ranges`, but tool name is `move_ranges`.

### Additional Validation

Reject before writing if:

- source and target resolve to the same path;
- source and target resolve to the same filesystem identity, including hardlinks/case-insensitive aliases;
- any source ranges overlap;
- target placement overlaps source in any same-file future mode;
- source fingerprint mismatches;
- target precondition mismatches.

Same-file moves are out of MVP.

### Write Order And Failure Semantics

`move_ranges` cannot honestly guarantee full cross-file transaction atomicity.

Contract:

- validate all preconditions first;
- compute target content and source-after content before writing;
- immediately before any target mutation, re-check source fingerprint against the requested `source_fingerprint`;
- if the final source recheck mismatches before target write, abort with `source_fingerprint_mismatch` and do not write target;
- immediately before replacing/creating target, re-check target precondition with lstat/hash or exclusive create;
- write target with temp file in target directory and atomic rename where possible;
- immediately before replacing source, re-check source fingerprint;
- if this second source recheck mismatches after target write, return `source_fingerprint_mismatch` with `partial_state.phase = "target_written_source_not_updated"`;
- after target write succeeds, write source-after with temp file in source directory and atomic rename where possible;
- crash between target and source writes can leave duplicated text, not lost text;
- output and errors must expose enough state for manual reconciliation when possible;
- per-process path locks serialize writes from this MCP server;
- external editor races are reduced by final fingerprint checks, but not fully eliminated without OS-level conditional replace or external filesystem locks.

### Output

```json
{
  "source_file": "/abs/source.go",
  "target_file": "/abs/target.go",
  "operation": "move",
  "dry_run": false,
  "applied": true,
  "ranges": [
    {"start_line": 10, "end_line": 40, "line_count": 31}
  ],
  "target_placement": {"mode": "append"},
  "bytes_written": 2048,
  "source_lines_removed": 31,
  "source_fingerprint_before": {"sha256": "hex"},
  "source_fingerprint_after": {"sha256": "hex"},
  "target_fingerprint_before": {"sha256": "hex"},
  "target_fingerprint_after": {"sha256": "hex"},
  "source_fingerprint_for_next_write": {"sha256": "hex"},
  "target_fingerprint_for_next_write": {"sha256": "hex"},
  "backup_paths": [],
  "boundary_warnings": [],
  "warnings": []
}
```

## `copy_ranges_batch`

### Purpose

Copy exact source line ranges from one source snapshot into multiple explicit target files without changing source.

This is the ergonomic path for non-destructive Markdown decomposition and other one-source/multi-target splits. The agent still chooses target paths, ranges, placement, joiners, and backup behavior.

### Input

```json
{
  "source_file": "/abs/source.md",
  "source_fingerprint": {"sha256": "hex"},
  "targets": [
    {
      "target_file": "/abs/part_01.md",
      "target_precondition": {"must_not_exist": true},
      "placement": {"mode": "create_new"},
      "ranges": [{"start_line": 1, "end_line": 120}],
      "joiner": "none",
      "backup": {"mode": "none"}
    },
    {
      "target_file": "/abs/part_02.md",
      "target_precondition": {"must_not_exist": true},
      "placement": {"mode": "create_new"},
      "ranges": [{"start_line": 121, "end_line": 260}],
      "joiner": "none",
      "backup": {"mode": "none"}
    }
  ],
  "dry_run": false
}
```

Rules:

- one source file per call;
- `targets` is required and non-empty;
- every target entry follows `copy_ranges` target/range/placement rules;
- target files must be unique by filesystem identity and path;
- source and every target must not resolve to the same filesystem object;
- all source ranges across all targets are interpreted against the same original source snapshot;
- overlapping source ranges are allowed for copy, but reported in `batch_warnings` because duplicated output is easy to miss;
- all preconditions are validated before any target write;
- final source recheck happens immediately before every target mutation;
- final target precondition recheck happens immediately before each target mutation.

### Output

```json
{
  "source_file": "/abs/source.md",
  "operation": "copy_batch",
  "dry_run": false,
  "applied": true,
  "target_results": [
    {
      "target_file": "/abs/part_01.md",
      "ranges": [{"start_line": 1, "end_line": 120, "line_count": 120}],
      "bytes_written": 4096,
      "target_fingerprint_after": {"sha256": "hex"},
      "target_fingerprint_for_next_write": {"sha256": "hex"},
      "boundary_warnings": []
    }
  ],
  "source_fingerprint_checked_at_write": {"sha256": "hex"},
  "source_fingerprint_for_next_write": {"sha256": "hex"},
  "batch_warnings": [],
  "backup_paths": [],
  "warnings": []
}
```

## `move_ranges_batch`

### Purpose

Move exact source line ranges from one source snapshot into multiple explicit targets, then remove all moved source ranges from source once.

This is the ergonomic path for destructive Markdown decomposition. It avoids the `move_ranges -> re-outline -> move_ranges` loop and keeps all removals aligned to the original source snapshot.

### Input

Same shape as `copy_ranges_batch`, but tool name is `move_ranges_batch`.

Additional rules:

- overlapping source ranges across all targets are rejected;
- duplicate source ranges across targets are rejected;
- all target writes must succeed before source is modified;
- after all target writes, source fingerprint is rechecked immediately before source replace;
- source deletion removes the union of all moved ranges bottom-to-top from the original source snapshot;
- if any target write fails before source replace, source must not be modified by the tool;
- if source replace fails or late source fingerprint mismatch happens after target writes, return partial state that lists written targets and says source was not modified by the tool when true.

### Output

```json
{
  "source_file": "/abs/source.md",
  "operation": "move_batch",
  "dry_run": false,
  "applied": true,
  "target_results": [
    {
      "target_file": "/abs/part_01.md",
      "ranges": [{"start_line": 1, "end_line": 120, "line_count": 120}],
      "bytes_written": 4096,
      "target_fingerprint_after": {"sha256": "hex"},
      "target_fingerprint_for_next_write": {"sha256": "hex"},
      "boundary_warnings": []
    }
  ],
  "source_ranges_removed": [
    {"start_line": 1, "end_line": 120, "line_count": 120}
  ],
  "source_lines_removed": 120,
  "source_fingerprint_before": {"sha256": "hex"},
  "source_fingerprint_after": {"sha256": "hex"},
  "source_fingerprint_for_next_write": {"sha256": "hex"},
  "batch_warnings": [],
  "backup_paths": [],
  "warnings": []
}
```

## Backup Policy

Default:

```json
{"mode": "none"}
```

Optional:

```json
{"mode": "sidecar"}
```

Sidecar backup behavior:

- create backup next to each modified file;
- include timestamp or short hash in file name;
- return `backup_paths`;
- do not create backups silently.

Backups are not a substitute for fingerprint preconditions.

## Error Semantics

All validation failures return structured tool error with empty plain text content.

Base error shape:

```json
{
  "error": "source_fingerprint_mismatch",
  "message": "source fingerprint differs from the supplied precondition",
  "file": "/abs/source.go",
  "safe_to_retry": false,
  "recommended_next_tool": "outline_file",
  "recommended_next_input": {
    "target_file": "/abs/source.go",
    "output_profile": "outline"
  }
}
```

Every common error must include the fields an agent needs for the next step:

- mismatch errors include `expected_fingerprint`, `actual_fingerprint`, and current `line_count` when available;
- range errors include `requested_range`, `current_line_count`, and whether fresh outline is required;
- target existence errors include current existence state and current fingerprint when available;
- path errors include the rejected path and normalized reason, but never suggest a relative path;
- partial write errors include `partial_state`.

Important error cases:

- `invalid_path`;
- `relative_path_rejected`;
- `source_fingerprint_mismatch`;
- `target_fingerprint_mismatch`;
- `target_exists`;
- `target_missing`;
- `overlapping_ranges`;
- `range_out_of_bounds`;
- `binary_file_rejected`;
- `unsupported_encoding`;
- `symlink_rejected`;
- `parent_directory_missing`;
- `same_file_operation_unsupported`;
- `write_failed`.

Examples:

```json
{
  "error": "source_fingerprint_mismatch",
  "file": "/abs/source.go",
  "expected_fingerprint": {"sha256": "old"},
  "actual_fingerprint": {"sha256": "new", "line_count": 940},
  "safe_to_retry": false,
  "recommended_next_tool": "outline_file",
  "recommended_next_input": {
    "target_file": "/abs/source.go",
    "output_profile": "outline"
  },
  "reason": "source changed; select ranges against the new outline"
}
```

```json
{
  "error": "target_fingerprint_mismatch",
  "file": "/abs/target.go",
  "expected_fingerprint": {"sha256": "old"},
  "actual_fingerprint": {"sha256": "new", "line_count": 70},
  "safe_to_retry": false,
  "recommended_next_tool": "outline_file",
  "recommended_next_input": {
    "target_file": "/abs/target.go",
    "output_profile": "fingerprint_only"
  },
  "reason": "target changed; refresh target precondition before writing"
}
```

```json
{
  "error": "range_out_of_bounds",
  "file": "/abs/source.go",
  "requested_range": {"start_line": 900, "end_line": 960},
  "current_line_count": 920,
  "fresh_outline_required": true,
  "safe_to_retry": false,
  "recommended_next_tool": "outline_file",
  "recommended_next_input": {
    "target_file": "/abs/source.go",
    "output_profile": "outline"
  }
}
```

No partial write should happen after validation errors.

If a write failure or late source fingerprint mismatch happens after at least one file may have changed, the structured error must include a partial-state object:

```json
{
  "error": "source_fingerprint_mismatch",
  "partial_state": {
    "operation": "move",
    "phase": "target_written_source_not_updated",
    "files_maybe_modified": ["/abs/target.go"],
    "targets_written": ["/abs/target.go"],
    "source_modified_by_tool": false,
    "original_source_text_definitely_present": "unknown",
    "target_fingerprint_after": {"sha256": "hex"},
    "source_fingerprint_after": {"sha256": "hex-or-unknown"},
    "recovery_hint": "target may contain duplicated moved text; source was not modified by this tool after target write, but an external editor may have changed it"
  }
}
```

`error` is the concrete structured error code, either `write_failed` or `source_fingerprint_mismatch`. `source_modified_by_tool=false` is required for the target-written/source-not-updated phase. `original_source_text_definitely_present` may be `true`, `false`, or `"unknown"` depending on whether the server can verify the current source still contains the original selected spans. If the server cannot determine a safe recovery state, it must say so explicitly.

Batch partial failures must use a batch-specific partial state so an agent can continue without manually inspecting every target:

```json
{
  "error": "write_failed",
  "partial_state": {
    "operation": "move_batch",
    "phase": "target_writes_partially_completed_source_not_updated",
    "source_file": "/abs/source.md",
    "source_modified_by_tool": false,
    "source_fingerprint_before": {"sha256": "hex"},
    "source_fingerprint_after": {"sha256": "hex-or-unknown"},
    "target_results": [
      {
        "target_file": "/abs/part_01.md",
        "status": "written",
        "written": true,
        "skipped": false,
        "failed": false,
        "target_fingerprint_before": null,
        "target_fingerprint_after": {"sha256": "hex"},
        "target_fingerprint_for_next_write": {"sha256": "hex"},
        "ranges": [{"start_line": 1, "end_line": 120}]
      },
      {
        "target_file": "/abs/part_02.md",
        "status": "failed",
        "written": false,
        "skipped": false,
        "failed": true,
        "failed_at": "target_write",
        "error": "write_failed",
        "target_fingerprint_before": null,
        "target_fingerprint_after": null,
        "ranges": [{"start_line": 121, "end_line": 260}]
      },
      {
        "target_file": "/abs/part_03.md",
        "status": "skipped",
        "written": false,
        "skipped": true,
        "failed": false,
        "failed_at": null,
        "target_fingerprint_before": null,
        "target_fingerprint_after": null,
        "ranges": [{"start_line": 261, "end_line": 400}]
      }
    ],
    "recommended_next_tool": "move_ranges_batch",
    "recommended_next_input_policy": "retry only failed/skipped targets after re-outline or explicit human/agent reconciliation; do not delete source until target set is complete",
    "recovery_hint": "some targets may already contain copied text; source was not modified by this tool"
  }
}
```

Allowed batch target `status` values:

- `planned`: validated but not reached yet;
- `written`: target write completed;
- `skipped`: not attempted because an earlier target failed;
- `failed`: attempted and failed;
- `rolled_back`: future optional status only if implementation safely removes a newly created target during recovery.

For `copy_ranges_batch`, `source_modified_by_tool` is always `false`. For `move_ranges_batch`, if failure occurs after source replace starts, `phase` must say so explicitly and `source_modified_by_tool` must reflect what the server can prove.

## Performance And Concurrency

`outline_file`:

- one file only;
- no recursive traversal;
- parser is per-file and bounded by `max_items` / `max_depth`;
- computes full-file hash while scanning/parsing;
- no long-lived cache required.

`copy_ranges` / `move_ranges` / batch variants:

- single-target variants use one source and one target per call;
- batch variants use one source and multiple explicit targets per call;
- read source and target after fingerprint validation;
- perform a final source fingerprint recheck immediately before any target replace/create;
- perform a final target precondition recheck immediately before target replace/create;
- for move tools, perform a second source fingerprint recheck after target writes and immediately before source replace;
- preserve selected raw bytes;
- use temp files and atomic rename where supported;
- acquire per-process locks for source and target paths in stable path order to avoid deadlocks;
- implementation plan must explicitly choose target-after construction strategy: whole-file in memory with a tested cap, streaming temp output, or a staged hybrid;
- reject files above a configurable write threshold only after the threshold is explicitly chosen in implementation plan and tested against representative large-file scenarios.

## Implementation Stages

### Stage 1: Exact Foundation

- `outline_file` for Markdown and Go.
- `copy_ranges`.
- `move_ranges`.
- `copy_ranges_batch`.
- `move_ranges_batch`.
- `dry_run` for all write tools.
- Agent-actionable errors and truncation recovery metadata.
- UTF-8/ASCII text only for writes.
- No symlink writes.
- No same-file copy/move.
- No `split_markdown_sections`.

### Stage 2: Language Pack

- Add parser interface implementations for TS/JS/Python/Java via Tree-sitter-style backend if dependency and build packaging are accepted.
- Add tests for exact ranges and confidence labels.

### Stage 3: Convenience Layer, If Still Needed

Consider `split_markdown_sections` only after explicit product decision for naming/overwrite/frontmatter/index behavior.

## Verification Expectations

Concept implementation should be reviewed against:

- current read-only tools do not regress;
- empty/relative paths rejected for all new path inputs;
- Markdown headings inside fenced code blocks ignored;
- Markdown nested section ranges correct;
- Go funcs/methods/types/imports have correct ranges;
- unsupported language does not claim exact ranges;
- unsupported language still returns fingerprint for target preconditions;
- `outline_file` `fingerprint_only` returns enough data for existing target precondition without full-file read;
- `outline_file` truncated output includes `outline_stats` and a bounded `next_recommended_call`;
- `copy_ranges` creates a new file from multiple ranges;
- `copy_ranges` appends to existing target only with target fingerprint;
- `copy_ranges` dry-run validates and reports planned effect without mutation;
- `copy_ranges` rejects same-file source/target identity;
- `move_ranges` removes source ranges and preserves target inserted bytes;
- `move_ranges` dry-run validates source deletion plan without mutation;
- `copy_ranges_batch` creates multiple explicit targets from one source snapshot;
- `move_ranges_batch` writes multiple targets then removes source ranges once;
- multi-call move requires re-outline after each move unless all ranges go to one target in one call;
- destructive multi-target Markdown split uses `move_ranges_batch` without re-outline per target;
- common errors include expected/actual context and recommended next tool;
- batch partial failures include per-target status and recovery hints;
- boundary newline warnings are returned in dry-run and write outputs;
- successful writes return next fingerprints for safe follow-up calls;
- final precondition recheck protects against external edits as far as the OS allows;
- line-to-byte semantics preserve CRLF, EOF-without-newline, and selected bytes exactly;
- overlapping ranges rejected;
- stale source fingerprint rejected;
- stale target fingerprint rejected;
- binary and non-UTF-8 writes rejected in MVP;
- symlink write rejected;
- crash/failure semantics documented and tested as far as practical;
- Windows path behavior and line endings covered.
