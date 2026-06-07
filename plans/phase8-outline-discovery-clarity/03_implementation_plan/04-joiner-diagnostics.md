# Stage 4: Joiner Diagnostics

Goal:
Make `copy_ranges`, `move_ranges`, `copy_ranges_batch`, and `move_ranges_batch` dry-runs explain the visual whitespace effect of `joiner` before any file mutation.

Depends on:
- Existing range fingerprint/dry-run/read-back model.

Touched areas:
- `filetoolsserver/handler/refactor_types.go`
- `filetoolsserver/handler/range_transfer.go`
- `filetoolsserver/handler/batch_ranges.go`
- write/batch range tests
- `filetoolsserver/server.go`
- `TOOLS.md`
- `README.md`

Public contract:
- `joiner_effect` keeps existing fields and gains additive diagnostics.
- Dry-run output shows requested and normalized joiner, newline style/bytes, source range boundary blank lines, target placement boundary state, resulting visual blank lines, and warning codes.
- `blank_line` does not become heuristic apply behavior; the tool only reports what will happen.
- Batch tools expose the same per-target diagnostics through existing target result structures.

Preferred DTO:
- Add `newline_style` as a display string: `lf`, `crlf`, or `none`.
- Add `newline_bytes` as escaped display text: `\n`, `\r\n`, or empty.
- Add `source_boundaries`, each with:
  - `left_range`
  - `right_range`
  - `left_trailing_blank_lines`
  - `right_leading_blank_lines`
  - `inserted_newline_count`
  - `resulting_visual_blank_lines`
  - `warning_codes`
- Add `target_boundary`, with:
  - `placement`
  - `before_has_newline`
  - `after_has_newline`
  - `before_trailing_blank_lines`
  - `after_leading_blank_lines`
  - `payload_starts_with_newline`
  - `payload_ends_with_newline`
  - `warning_codes`
- Add top-level `visual_blank_lines_between_ranges` for compact scan.
- Reuse existing top-level `boundary_warnings` as the aggregate warning path for both newline-join risks and joiner visual-whitespace risks. Do not add a second aggregate warning field. Detailed joiner-specific `warning_codes` live inside `joiner_effect.source_boundaries[]` and `joiner_effect.target_boundary`.

Implementation steps:
1. Inspect existing `lineScanResult` and `rangeSpan` data to determine whether selected range edge lines can be analyzed without rereading large content.
2. Add helper functions to count blank lines at selected source range edges:
   - trailing blank lines at the end of left range;
   - leading blank lines at the start of right range.
3. Add helper to classify newline style from `joinerBytes`.
4. Replace or extend `joinerEffectForPayload` so it can accept source boundary facts, not only range count.
5. Compute `resulting_visual_blank_lines` per join boundary:
   - account for blank edge lines already included in selected ranges;
   - account for inserted newline count from normalized joiner;
   - keep the calculation documented in tests.
6. Add warning code for `blank_line` creating more visual whitespace than expected, for example `joiner_extra_visual_blank_lines`.
7. Add target placement boundary diagnostics:
   - append: target end to payload start;
   - prepend: payload end to target start;
   - insert_before_line: both sides;
   - replace_range: before replaced range to payload start and payload end to after replaced range.
8. Keep existing `boundary_warnings` for missing newline join risks and add joiner-specific aggregate warnings there, using stable codes such as `joiner_extra_visual_blank_lines`. This is the only top-level warning path agents need to scan.
9. Wire diagnostics into single range tools before dry-run return.
10. Wire diagnostics into batch target results through the shared transfer plan path.
11. Update docs examples so `blank_line` is no longer described as guaranteed one visual blank line without qualification; it is the requested inserted separator, and diagnostics show actual visual outcome.

Test cases:
1. Two ranges with no edge blank lines and `blank_line` -> expected one visual blank line.
2. Left range already ends with an empty line and `blank_line` -> warning and higher visual blank count.
3. Right range starts with an empty line and `blank_line` -> warning and higher visual blank count.
4. Both edges already blank -> warning with combined count.
5. `single_newline` with clean edges -> no extra blank warning.
6. `none` with adjacent content -> existing boundary warning behavior preserved where applicable.
7. CRLF target/source style -> newline bytes report `\r\n`.
8. Batch copy/move exposes per-target joiner diagnostics.
9. Target boundary diagnostics for `append`: target end to payload start reports newline/blank state and warning codes.
10. Target boundary diagnostics for `prepend`: payload end to target start reports newline/blank state and warning codes.
11. Target boundary diagnostics for `insert_before_line`: both before and after insertion boundaries are represented.
12. Target boundary diagnostics for `replace_range`: before replaced range to payload start and payload end to after replaced range are represented.
13. At least one target-boundary test includes existing blank/newline risk and asserts top-level `boundary_warnings`.
14. Schema assertions cover `joiner_effect.source_boundaries`, `joiner_effect.target_boundary`, and top-level `boundary_warnings` for single and batch outputs.

Checks:
- Focused write range tests.
- Focused batch range tests.
- Docs/schema consistency.
- Later full handler and repo tests.

Handoff / next stage:
Stage 5 verifies docs and schemas after DTO fields are finalized.

Stop and ask if:
- The exact visual blank-line formula would change existing write output rather than only diagnostics.
