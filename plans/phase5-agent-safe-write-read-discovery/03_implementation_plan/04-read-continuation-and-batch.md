# Stage 4: Read Coverage, Chunk Continuation, And Batch Read

## Goal

Make reading line-addressable files more proof-oriented: agents can know whether a requested range is complete, request total line counts when needed, continue large reads statelessly, and read multiple known files compactly.

## Depends On

- Stage 1 coverage/continuation DTOs.
- Stage 3 visibility rules for hidden exact paths.

## Touched Areas

- `filetoolsserver/handler/read_file.go`
- `filetoolsserver/handler/read_output.go`
- `filetoolsserver/handler/tool_types.go`
- `filetoolsserver/server.go`
- `filetoolsserver/handler/schema_constraints.go`
- tests in `agent_tools_test.go`, `handler_test.go`

## `read_file` Input Additions

Add:

```go
CountTotalLines bool `json:"count_total_lines,omitempty"`
ChunkLines *int `json:"chunk_lines,omitempty"`
ExpectedVersion *ReadCoverageProof `json:"expected_version,omitempty"`
```

Rules:

- `count_total_lines=true` forces a full line count even for bounded ranges.
- `chunk_lines` is valid only with optional `start_line` and without `end_line`.
- `end_line + chunk_lines` is rejected with `error_code=invalid_read_range` because precedence would be ambiguous.
- `chunk_lines` selects at most that many lines from `start_line` or line 1.
- `expected_version` is optional; if provided with `proof_strength=exact`, compare full-file `size_bytes`, `modified_unix_nano` and full-file `sha256`; mismatch returns `error_code=continuation_stale`.
- `expected_version.proof_strength=exact` with empty/missing `sha256`, size or mtime is rejected with `error_code=invalid_continuation_proof`.
- If only `proof_strength=stat_only` is provided, compare size/mtime and set continuation consistency to `unknown` on success because same-size/same-mtime edits cannot be ruled out.
- Existing bounded fast path remains default.

## `read_file` Output Additions

Add:

- `coverage`;
- `continuation`;
- `fingerprint` only when already computed or `count_total_lines=true` / full read makes it cheap enough; otherwise use `coverage.proof` with full-file size/mtime and returned range.

Rules:

- `requested_range_complete=true` means returned text covers the requested range after EOF clipping.
- `complete_file_read=true` means the whole file was returned.
- `file_total_lines_known` mirrors `total_lines_known`.
- `coverage.next_range` is informational. It may include an `end_line` to describe the next bounded span, but agents must not copy it verbatim into chunked continuation input because `chunk_lines + end_line` is invalid.
- `continuation.next_recommended_call` recommends `read_file` with:
  - same `cwd_id`;
  - same `target_file`;
  - next `start_line`;
  - same `chunk_lines`;
  - same `count_total_lines` when caller set `count_total_lines=true`;
  - no `end_line` when `chunk_lines` is present;
  - `expected_version` with exact file proof when the tool can compute it within thresholds.

## New Tool: `read_files`

Add a batch helper for known files/ranges.

Input:

```go
type ReadFilesInput struct {
    CwdAwareInput
    Items []ReadFileInputItem `json:"items"`
    MaxTotalLines *int `json:"max_total_lines,omitempty"`
    MaxTotalBytes *int `json:"max_total_bytes,omitempty"`
    CountTotalLines bool `json:"count_total_lines,omitempty"`
    RedactionMode string `json:"redaction_mode,omitempty"`
}
```

Each item:

- `target_file`;
- optional `start_line`;
- optional `end_line`;
- optional `chunk_lines`;
- optional `expected_version`.

Output:

- `items[]` in input order;
- per-item `status`: `ok` or `error`;
- per-item flattened read output fields, not nested `output`:
  - `file`;
  - `text`;
  - `range`, `requested_range`, `total_lines`, `total_lines_known`, `truncated`;
  - `coverage`, `continuation`, `fingerprint` where applicable;
  - `error`, `error_code`, `redacted`, `redaction_mode` where applicable;
- top-level `truncated` when `max_total_lines` stops further reads;
- top-level `continuation` for skipped remaining items.

Rules:

- Default `MaxTotalLines` uses a safe bounded value, e.g. `1000`.
- Add `MCP_READ_FILES_MAX_ITEMS`, default `24`; reject larger `items[]` with `error_code=too_many_items`.
- Add `MCP_READ_FILES_MAX_TOTAL_BYTES`, default `262144`, and `MCP_READ_FILES_MAX_ITEM_BYTES`, default `65536`.
- `MaxTotalBytes` can lower the configured total byte cap but cannot exceed it.
- Per-item text is emitted only as complete lines. If a line would exceed the item byte cap, do not emit a partial line; set that item `truncated=true`, stop the item at the last complete emitted line, and return a read/inspect hint rather than a line-based continuation that would resume mid-line.
- Top-level output stops before exceeding total bytes/lines and must not cut a line in the middle when it also returns line-based continuation.
- Per-item errors do not cancel other items unless global limit is reached.
- Path projection applies per item.
- No file content appears in top-level errors.
- `redaction_mode=auto` redacts hidden/config/log-like paths and secret-like values in this new batch content surface; exact single `read_file` remains raw by design.
- Top-level continuation for `read_files` is stateless:
  - if the current item is partially read, next input contains the same current item with updated `start_line` and `expected_version`;
  - remaining input suffix is copied after that item;
  - already completed items are omitted;
  - `next_recommended_call.recommended_next_input` replays all effective public top-level batch options that affect output semantics, including `redaction_mode`, `count_total_lines`, `max_total_lines`, `max_total_bytes` and cwd mode;
  - `max_item_bytes` is server-config only in Phase 5 and is not emitted in recommended public input;
  - continuation is emitted only at line boundaries; if byte caps prevent returning the next complete line, do not produce a line-based continuation for that partial line and instead mark the item truncated with an inspection hint;
  - `next_recommended_call.recommended_next_input.items` is capped by `MCP_READ_FILES_MAX_ITEMS` and may include a warning if the original request must be split again;
  - no server cursor/state is used.

## Steps

1. Add DTOs and schemas.
2. Refactor current read path so single and batch use the same read-range engine.
3. Add cheap file version proof extraction from `os.Stat`.
4. Add optional full line counting for bounded ranges.
5. Add chunking behavior and next-call hints.
6. Add stale continuation check.
7. Add batch read handler and server registration.
8. Add schema constraints for `items[].target_file`.
9. Update docs and examples.

## Acceptance

- Existing `read_file` behavior remains unchanged when new fields are absent.
- Bounded range still uses fast path unless `count_total_lines=true`.
- `count_total_lines=true` returns known total lines.
- `chunk_lines` returns deterministic chunks and next range.
- Continuation errors on changed size/mtime when expected version is provided.
- `read_files` returns mixed success/errors in input order.
- `read_files` respects max total line limit and provides continuation.
- `read_files` respects item count, item byte and total byte caps.
- `read_files(redaction_mode=auto)` does not leak raw secret-like values for hidden/config/log-like files.
- Cwd projection works for every item.

## Checks

- Read full file, bounded range, start-only, end-only, start past EOF.
- Final empty line consistency with `inspect_path`.
- Chunk continuation across entire file with proof-of-complete.
- Stale continuation by modifying temp file between calls.
- Batch read visible and exact hidden files.
- Batch read with one missing file and one successful file.
- Batch read global line limit.
- Batch read item count, per-item byte and total byte limits.
- Batch read continuation for partial current item plus remaining suffix.
- Batch read continuation with non-default `redaction_mode`, `count_total_lines`, `max_total_lines` and `max_total_bytes` proves recommended input replays the effective public top-level options.
- Single `read_file` chunk continuation with `count_total_lines=true` proves the recommended input preserves total-line counting.
- Long-line byte-cap test proves `read_files` never emits a continuation that resumes in the middle of a line.
- Per-item truncation test proves `read_files.items[].truncated=true` is set for item byte cap / long-line refusal and top-level `truncated` does not replace item-level status.
- Exact continuation stale test where same-size/same-mtime content changes but sha differs, where feasible.
- Invalid exact continuation proof test for missing/empty sha.
- Next-call shape test proves chunk continuation uses `start_line + chunk_lines` and does not include `end_line`.
- `invalid_read_range` and `too_many_items` error-code tests.
- Cwd no-leak tests for flattened `read_files.items[].file` and continuation `items[].target_file`.
- Cwd tests.

## Handoff / Next Stage

After Stage 4, discovery continuation can reuse `ContinuationHint` patterns and batch read can support docs/test workflows.

## Stop And Ask If

- Full line counting for bounded ranges would require unacceptable memory for large files.
- A useful continuation design requires server state.
- Batch output becomes too large to bound predictably.
