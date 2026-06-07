# Stage 2: Unicode-Safe Preview Truncation

## Goal

Ensure every preview/snippet truncation returns valid display text and never introduces U+FFFD (`\uFFFD` / `�`) because of truncation. It also must not emit mojibake sequence `ï¿½`.

## Depends On

- Stage 1 redaction defaults, because preview tests should inspect literal output.

## Touched Areas

- `filetoolsserver/handler/write_preview.go`
- `filetoolsserver/handler/read_files.go`
- `filetoolsserver/handler/grep_tool.go`
- `filetoolsserver/handler/phase5_helpers.go`
- any helper currently slicing strings/bytes by budget
- tests in `write_tools_test.go`, `agent_tools_test.go`

## Contract

- Existing configured budgets remain byte budgets.
- Shared helper truncates on grapheme cluster boundary where feasible.
- Helper always returns valid UTF-8.
- Marker counts inside budget when room exists.
- If no marker fits, return the largest valid prefix or empty string; never partial rune.
- Helper reports `truncated=true` exactly when visible output was shortened.

## Steps

1. Inventory current truncation functions:
   - `boundedPreviewString`
   - `boundedPreviewStringSuffix`
   - `trimLineNumberedText`
   - diff preview builder budget checks
   - grep snippet/context trimming if any.
2. Add a shared helper, for example `truncateDisplayUTF8(value string, maxBytes int, marker string, mode prefix/suffix)`.
3. Prefer grapheme-aware behavior using a small dependency only if it is worth it and allowed by dependency policy; otherwise implement practical cluster protection for combining marks/ZWJ/variation modifiers and document exact scope.
4. Replace direct byte slicing at content boundaries.
5. Preserve suffix truncation for boundary `before` snippets.
6. Ensure diff preview hunk selection still honors byte budget while clipping lines safely.
7. Add tests for:
   - `diff_previews[].text`;
   - `boundary_preview`;
   - read-back snippets;
   - `grep` snippets/context;
   - error/warning snippets that quote content;
   - Cyrillic boundary preview truncation;
   - Cyrillic diff preview truncation;
   - combining mark sequence;
   - emoji skin-tone modifier;
   - ZWJ emoji sequence;
   - very small budget.
8. Assert no output contains U+FFFD because of truncation.

## Checks

- Targeted Unicode preview tests pass.
- Existing diff truncation stats remain meaningful.
- No test expects exact byte-clipped invalid output.

## Handoff / Next Stage

After Stage 2, preview evidence is trustworthy for Unicode content. Stage 3 can focus on JSON/YAML output shape.

## Stop And Ask If

- Grapheme support requires a dependency with unacceptable build impact.
- Budget compatibility would require preserving invalid UTF-8.
