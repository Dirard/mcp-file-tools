# Stage 4: Symbol-Aware Range Tool Integration

## Goal

Connect exact selector resolution to existing range-transfer tools without bypassing line/fingerprint safety.

## Depends On

- Phase 5 write preview/read-back/validation implemented and tested.
- Stage 3 `resolve_symbol_range`.

## Touched Areas

- `filetoolsserver/handler/range_transfer.go`
- `filetoolsserver/handler/resolve_symbol_range.go`
- `filetoolsserver/handler/tool_types.go`
- `filetoolsserver/handler/copy_ranges.go`
- `filetoolsserver/handler/move_ranges.go`
- `filetoolsserver/handler/refactor_types.go`
- tests

## API Direction

Phase 6 core implementation:

- Keep `copy_ranges*` / `move_ranges*` primary input as concrete `ranges`.
- Use this workflow:
  1. `outline_file`;
  2. `resolve_symbol_range` with Stage 4 optional `target_intent`;
  3. `copy_ranges` / `move_ranges` using `recommended_write_call.recommended_next_input` from the resolver.

Direct selector write input is a future gate, not Phase 6 core. It may be planned later only if it stays simpler and safer than the resolver-mediated workflow.

Stage 4 implementation gate:

- If Phase 5 diff previews, post-write/read-back validation and write redaction are not implemented and tested, stop Phase 6 implementation after Stage 3.
- Before implementing Stage 4, run and record the Phase 5 write-safety test group in the same working tree. The group must cover diff previews, redaction/truncation order, boundary preview/joiner behavior, backup discovery hints, post-write read-back/validation, batch source/target placement, and cwd no-leak for write outputs.
- A review note alone is not sufficient for this gate.
- In that case, Phase 6 status is partial/blocked for full product acceptance. Docs may describe symbol navigation and read-only `resolve_symbol_range`, but must not claim Phase 6 or symbol-aware copy/move is complete.

## `resolve_symbol_range` Next Calls

Extend resolver input:

```go
type ResolveSymbolRangeInput struct {
    CwdAwareInput
    SourceFile string `json:"source_file"`
    Language string `json:"language,omitempty"`
    Selector SymbolSelectorQuery `json:"selector"`
    SourceFingerprint FileFingerprint `json:"source_fingerprint"`
    TargetIntent *WriteTargetIntent `json:"target_intent,omitempty"`
}

type WriteTargetIntent struct {
    Operation string `json:"operation"` // copy or move
    TargetFile string `json:"target_file"`
    TargetPrecondition TargetPrecondition `json:"target_precondition"`
    Placement TargetPlacement `json:"placement"`
    TargetSyntaxMode string `json:"target_syntax_mode,omitempty"` // auto, markdown, plain_text
    Joiner string `json:"joiner,omitempty"`
    Backup *BackupSpec `json:"backup,omitempty"`
    DryRun bool `json:"dry_run"`
}
```

Extend resolver output:

- `next_recommended_call` for `read_file` for inspection;
- `write_recommendation_status`;
- `write_refusal_code`;
- `write_refusal_reason`;
- `target_syntax_status` values: `not_checked`, `safe`, `unsafe`, `unknown`;
- `target_syntax_proof` values: `none`, `create_new`, `markdown_parser_ok`, `plain_text_asserted`, `structured_allowlist`;
- `target_syntax_proof_reason` short human-readable reason;
- `recommended_write_call *ActionHint`, only when selector is unambiguous, exact, delimiter-safe, whole-line write-safe, parser status is `ok`, and caller included complete `target_intent` with `dry_run=true`.
- `preview_write_call *ActionHint`, only for refusal responses that can still offer an equivalent safe dry-run preview such as `target_intent.dry_run=false`.

`recommended_write_call` uses the existing public `ActionHint` shape:

- `recommended_next_tool` is `copy_ranges` or `move_ranges`;
- `recommended_next_input` is the public JSON input shape for that range tool, including explicit `cwd_id` when resolver request used cwd mode;
- `recommended_next_input` always has `dry_run=true`;
- `recommended_next_input.source_file` is copied from resolver `source_file`;
- `recommended_next_input` includes `source_fingerprint`, concrete `ranges`, and target intent fields copied into the public `copy_ranges` or `move_ranges` input.

Rules:

- `target_intent` never mutates by itself.
- `operation` is only `copy` or `move` in Phase 6.
- `target_syntax_mode` values are omitted/`auto`, `markdown`, and `plain_text`. `plain_text` is an explicit caller assertion that the target is non-structured text; parser `generic_fallback` alone never implies plain-text safety.
- Caller-forced `target_syntax_mode` values cannot override supported structured/config target detection for existing files. If the target path or detected language is JSON, YAML, JavaScript, TypeScript, TSX/JSX, Svelte, Python, Go or another structured language, treat it as structured and refuse unless the structured allowlist explicitly permits it. This applies to `plain_text` and `markdown`.
- Invalid `operation` returns `error_code=invalid_target_operation`. Invalid `target_syntax_mode` returns `error_code=invalid_target_syntax_mode`.
- `target_intent.dry_run=true` is required for ready recommendations.
- `write_recommendation_status` values: `not_requested`, `ready`, `refused`, `not_applicable`.
- `write_refusal_code` carries refusal reasons such as `target_intent_requires_dry_run`, `symbol_range_not_write_safe`, `symbol_parser_not_write_safe`, `target_same_file_unsupported`, or `target_syntax_not_proven`.
- A successful read/navigation resolution can return `write_recommendation_status="refused"` without top-level `error_code`.
- Do not set `recommended_write_call` unless the input includes explicit target information, the recommended call is dry-run preview, and `write_recommendation_status="ready"`.
- If `target_intent.dry_run=false`, do not set top-level `error_code`; set `write_recommendation_status="refused"` and `write_refusal_code=target_intent_requires_dry_run`. Omit `recommended_write_call`; when all other write-safety checks pass, return an equivalent preview-safe range-tool `ActionHint` with `dry_run=true` in `preview_write_call`.
- `next_recommended_call` / `next_recommended_calls[]` remain read/inspection hints only in Stage 4. Do not place write-preview hints there.
- Do not produce a mutating recommendation when target intent is incomplete, ambiguous, stale, estimated, parser-partial, not whole-line write-safe or not delimiter-safe.
- Resolve `source_file` and `target_intent.target_file` through the same path/cwd/filesystem identity check as range-transfer tools. Use `os.SameFile`-style identity where available after statting both paths, so hardlinks, symlinks/junctions, case/drive variants and cwd-relative aliases identify the same physical file. If they identify the same file, refuse the Stage 4 recommendation with `write_refusal_code=target_same_file_unsupported`; same-file symbol copy/move is future-gated until range-transfer supports it safely.
- For structured/delimiter-sensitive target languages or config formats, `recommended_write_call` may be `ready` only when target placement is both allowlisted below and proven delimiter-safe for insertion without modifying syntax outside the target range.
- `target_syntax_status="safe"` is allowed only for these explicit proof modes:
  - `create_new`, where the target file does not exist and the full target content is the copied symbol/range payload; return `target_syntax_proof="create_new"`;
  - Markdown targets with parser status `ok`, where Markdown is auto-detected or extension-supported for the target path, insertion is line-structural and no language delimiter repair is needed; return `target_syntax_proof="markdown_parser_ok"`;
  - explicit `target_syntax_mode="plain_text"` targets that are not detected as structured/config targets; return `target_syntax_proof="plain_text_asserted"`;
  - parser-backed existing target only for an explicitly allowlisted placement/language pair in the matrix below, where `target_precondition.fingerprint` matches, target parse status is `ok`, source/target languages are compatible, placement is at a parser-recognized boundary, and insertion does not require comma/indent/bracket/token repair outside the inserted range.
- Existing structured target proof requires reading/parsing the target after target precondition resolution and before returning `recommended_write_call`. `partial`, `parse_error`, `generic_fallback`, stale fingerprint, or unsupported target language cannot produce `target_syntax_status="safe"`.
- If target syntax cannot be proven from the explicit safe modes, set `target_syntax_status="unknown"`, `write_recommendation_status="refused"`, and `write_refusal_code=target_syntax_not_proven`.
- `ready` means the source range and target placement are safe enough for a Phase 5 dry-run range-tool preview; it does not imply project tests pass or semantic refactor correctness.
- With `parser_status="partial"`, resolution can return read/navigation matches, but `recommended_write_call` is omitted with `write_recommendation_status="refused"` and `write_refusal_code=symbol_parser_not_write_safe`.

Target placement proof matrix:

| Placement | Phase 6 ready condition | Structured-target default |
| --- | --- | --- |
| `create_new` | Ready when target does not exist and target precondition allows creation. | Safe because no existing target syntax must be repaired. |
| `append` | Ready for Markdown targets with parser status `ok`, or explicit `target_syntax_mode="plain_text"` targets that are documented as non-structured text. Parser-backed structured targets are future-gated unless added to the allowlist below. | Refuse with `target_syntax_not_proven` unless allowlisted proof exists. |
| `prepend` | Ready for Markdown targets with parser status `ok`, or explicit `target_syntax_mode="plain_text"` targets that are documented as non-structured text. Parser-backed structured targets are future-gated unless added to the allowlist below. | Refuse with `target_syntax_not_proven` unless allowlisted proof exists. |
| `insert_before_line` | Ready for Markdown targets with parser status `ok`, or explicit `target_syntax_mode="plain_text"` targets that are documented as non-structured text. Parser-backed structured targets are future-gated unless added to the allowlist below. | Refuse with `target_syntax_not_proven` unless allowlisted proof exists. |
| `replace_range` | Ready for Markdown targets with parser status `ok`, or explicit `target_syntax_mode="plain_text"` targets only. Structured-language replacement is future-gated because it can require AST/formatter repair. | Refuse with `target_syntax_not_proven`. |

Phase 6 initial structured-target allowlist:

- none beyond `create_new`.

Any parser-backed existing structured target insertion added later must update this matrix and tests before returning `target_syntax_status="safe"`.

## Future Gate: Direct Selector Inputs

Do not implement this in Phase 6 core. If a later phase accepts it, add to range tools:

```go
SourceSelectors []SymbolSelectorQuery `json:"source_selectors,omitempty"`
```

Rules:

- `ranges` and `source_selectors` cannot both be empty.
- If both are present, resolved selector ranges append after explicit ranges only if no overlap.
- Source fingerprint is still required.
- All selectors resolve before any target precondition or write.
- Ambiguous/stale/estimated selectors fail before mutation.
- Output echoes `resolved_selectors[]` and final concrete `ranges`.

## Batch Tools

Phase 6 core does not add batch selector input.

- Batch symbol recommendations are future-gated and not Phase 6 acceptance.
- Agents may still manually compose existing batch tools from concrete resolver outputs, but Phase 6 does not claim that as a 10/10 no-manual-line-number batch workflow.
- A later phase can add a bounded `batch_target_intent` or direct batch selector contract after review.

## Acceptance

- Symbol-based copy/move workflow does not require manual line-number copying.
- Mutation still goes through existing range transfer engine.
- `recommended_write_call` is dry-run-only in Phase 6 core.
- Diff preview/read-back from Phase 5 appears for symbol-derived writes.
- Estimated/ambiguous/stale selectors never mutate.
- Non-whole-line exact selectors never mutate through line-based tools.
- Delimiter-sensitive exact selectors never produce write recommendations when deletion/move would require syntax repair outside the selected range.
- Same-file source/target symbol writes are refused in Phase 6 core rather than returning a false-ready hint.
- Structured target placements that require unproven delimiter/syntax repair are refused instead of marked ready.
- Final outputs always show concrete ranges.
- If Phase 5 write safety implementation is missing, Stage 4 is explicitly blocked and Phase 6 is not fully accepted.

## Checks

- `resolve_symbol_range` -> `copy_ranges` create_new function move in temp fixture.
- `resolve_symbol_range` -> `move_ranges` with source removal diff/read-back.
- `resolve_symbol_range(target_intent)` returns ready recommended input without manual range transcription.
- `target_intent.dry_run=false` refuses non-preview recommendation, omits `recommended_write_call`, keeps `next_recommended_call` read/inspection-only, and returns the equivalent dry-run write preview in `preview_write_call`.
- Same-file target refusal with `write_refusal_code=target_same_file_unsupported`.
- Same-file target refusal tests cover normalized same path, cwd-relative aliases, case/drive normalization where applicable, hardlinks where available, and symlink/junction targets with platform skip notes when link creation is unavailable.
- Target syntax safe-mode tests cover `create_new` and Markdown/plain text line insertion.
- Placement matrix tests cover `create_new`, `append`, `prepend`, `insert_before_line`, and `replace_range`, proving unsupported structured placements refuse with `target_syntax_not_proven`.
- Target-side delimiter/syntax unknown refusal with `write_refusal_code=target_syntax_not_proven`.
- Overlap/duplicate selector range tests.
- Fingerprint mismatch before mutation.
- Non-whole-line exact range refusal.
- JSON/YAML/JS/TS delimiter edge cases: first/middle/last property or declaration, trailing comma, same-line siblings, and refusal when formatter/structural repair would be needed.
- Cwd projection tests for selector and final ranges.

## Handoff / Next Stage

After Stage 4, Phase 6 core behavior is complete. Structural edit remains future gated work.

## Stop And Ask If

- Direct selector write input makes write schemas confusing or unsafe.
- Symbol writes need parser behavior unavailable in `resolve_symbol_range`.
- Any mutation path bypasses Phase 5 diff/validation.
- Phase 5 write preview/read-back/validation is not actually implemented yet.
- A batch symbol workflow becomes required for Phase 6 core.
