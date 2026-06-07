# Stage 5: Outline, Line Counts, And Batch Metrics

## Goal

Fix the three UX issues that sit beside `cwd_id`: useful generic outlines, consistent final-empty-line counts, and clear batch byte metrics.

## Generic Outline Fallback

Files:

- `filetoolsserver/handler/outline_file.go`
- `filetoolsserver/handler/outline_common.go`
- `filetoolsserver/handler/outline_schema.go`
- `filetoolsserver/handler/agent_tools_test.go`

Existing exact parsers:

- Go exact outline stays compatible.
- Markdown ATX exact outline stays compatible.
- Existing `parser_status: "ok"` for exact parsers stays valid.

New generic text fallback:

- Applies to ordinary decoded text files when no exact parser is available.
- Does not apply when `output_profile: "fingerprint_only"` is requested; fingerprint-only bypasses exact and generic outlines and returns only fingerprint/cheap metadata as before.
- Does not apply to binary files or files that cannot be decoded under existing text rules.
- Does not invent imports, symbols, classes, functions, or AST nodes.
- Produces compact `sections` entries with exact line ranges.
- Uses `kind: "text_block"` or equivalent stable generic kind.
- Uses a name derived from a short trimmed first non-empty line, or `lines N-M` when no useful label exists.
- Adds metadata that clearly identifies the parser tier as generic, for example `parser_status: "generic_text"` and `parser_scope: "generic_blank_line_blocks"`.
- Each generic outline item must expose item-level honesty metadata, using fields such as `confidence: "synthetic"`, `range_is_estimated: false` for exact line spans, and `metadata.parser_tier: "generic_text"` or a named equivalent.
- Keeps `OutlineItem.path` as structural ancestry, not filesystem path.
- Respects `max_items`, line windows, truncation, and `next_recommended_call`.
- `next_recommended_call` path values obey cwd projection and include current `cwd_id` when the original request was cwd-aware, so the call is replayable.

Fallback chunking:

- Work over the requested `line_window` first; generic outline never needs to inspect unrelated lines outside that window except for existing cheap fingerprint metadata.
- Stream or bounded-read decoded text lines; do not require loading the full file content into memory just to build generic sections.
- A generic block is a run of non-empty lines separated from the next block by one or more blank lines.
- Skip leading/trailing blank lines as standalone items.
- Split any block when it would exceed 40 display lines or roughly 4096 UTF-8 bytes of source text, whichever comes first, but never split a single display line.
- The 4096-byte cap is soft for one display line: if one line alone exceeds 4096 bytes, emit a one-line chunk whose range is exact. The label still uses the 80-code-point cap and the outline does not include the full line body.
- For long continuous text with no blank lines, emit consecutive chunks of at most 40 display lines and target 4096 bytes each; when adding the next line would exceed the byte target and the current chunk is non-empty, start a new chunk. A single long line forms its own chunk.
- Item label is the first non-empty line in the block/chunk after trimming and whitespace collapse; cap the label at 80 Unicode code points. If no useful label remains, use `lines N-M`.
- Item range is exact inclusive display-line range for the emitted block/chunk.
- Apply `max_items` after chunking. If more chunks remain, set truncation metadata and produce `next_recommended_call` for the next line after the last emitted item.
- Avoid outputting full content bodies; outline remains structural.

Unsupported/binary behavior:

- `output_profile: "fingerprint_only"` remains fingerprint-only for non-Go/non-MD text, Go, and Markdown.
- Binary files remain fingerprint-only or safe error according to existing contract.
- Truly unsupported non-text inputs do not fake structure.

## Unified Display-Line Count

Files:

- `filetoolsserver/handler/read_file.go`
- `filetoolsserver/handler/inspect_path.go`
- `filetoolsserver/handler/fingerprint.go`
- `filetoolsserver/handler/outline_common.go`
- `filetoolsserver/handler/agent_tools_test.go`

Adopt one display-line model:

- `"" -> 0`
- `"a" -> 1`
- `"a\n" -> 2`
- `"a\r\n" -> 2`
- `"a\nb" -> 2`
- `"a\nb\n" -> 3`

Rules:

- A final line terminator creates a final empty display line.
- CRLF is treated as one line terminator.
- Empty files have zero lines.
- `read_file` output must be able to address the final empty line by line number.
- `inspect_path.line_count`, `read_file.total_lines`, outline/fingerprint line counts, and refactor fingerprints all agree for the same decoded text.
- Bounded range reads may still avoid full-file counting when EOF is not reached; if they do report a total, it must use the shared model.

Implementation direction:

- Create one shared helper for decoded text line counting.
- Remove or adapt `inspect_path` fast counting helpers so they do not drop final empty lines.
- Keep binary detection and encoding behavior unchanged.

## Batch Byte Metric Contract

Files:

- `filetoolsserver/handler/batch_ranges.go`
- `filetoolsserver/handler/refactor_types.go`
- `filetoolsserver/handler/batch_tools_test.go`
- `TOOLS.md`
- `README.md`

Problem:

- Per-target `would_write_bytes` is understandable.
- Top-level `move_ranges_batch.would_write_bytes` is confusing because it can include source rewrite bytes.

New dry-run fields:

```json
{
  "would_write_target_bytes": 120,
  "would_rewrite_source_bytes": 80,
  "would_write_total_bytes": 200,
  "would_write_bytes": 200
}
```

Rules:

- `would_write_target_bytes`: sum of bytes planned for target writes.
- `would_rewrite_source_bytes`: bytes planned for source rewrite; zero for copy batch, non-zero for move batch when source changes.
- `would_write_total_bytes`: target write bytes plus source rewrite bytes.
- legacy top-level `would_write_bytes`: compatibility alias for `would_write_total_bytes`.
- Per-target `target_results[].would_write_bytes` remains target payload bytes.

New applied fields:

```json
{
  "bytes_written_target_bytes": 120,
  "bytes_rewritten_source_bytes": 80,
  "bytes_written_total_bytes": 200,
  "bytes_written": 200
}
```

Rules:

- `bytes_written_target_bytes`: actual bytes written to targets.
- `bytes_rewritten_source_bytes`: actual bytes written to rewritten source; zero for copy batch.
- `bytes_written_total_bytes`: target plus source actual bytes.
- legacy top-level `bytes_written`: compatibility alias for `bytes_written_total_bytes`.
- Single-target `copy_ranges` and `move_ranges` keep their existing `would_write_bytes` and `bytes_written` semantics unless tests reveal the same ambiguity there.

Limiter behavior:

- `MCP_BATCH_MAX_PLANNED_BYTES` continues to guard total planned bytes.
- Documentation must say total planned bytes includes target payload bytes and, for move batch, source rewrite bytes.

## Acceptance

- `.txt` or config-like text file outline returns generic sections, not only fingerprint.
- Generic outline output does not pretend to be an exact AST.
- Binary/undecodable files do not fake outline structure.
- Line count tests cover empty, no trailing newline, LF trailing newline, CRLF trailing newline, and multi-line trailing newline cases.
- Batch tests prove copy and move expose target, source, total, and legacy alias metrics.
- Docs explain legacy aliases so users do not misread top-level totals.

## Stop And Ask If

- Product wants generic outline to be labeled `parser_status: "ok"` instead of `generic_text`.
- Existing clients require top-level `would_write_bytes` to mean target-only bytes.
