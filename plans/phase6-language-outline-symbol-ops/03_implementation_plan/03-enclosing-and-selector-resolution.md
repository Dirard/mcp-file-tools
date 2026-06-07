# Stage 3: Enclosing Symbol And Selector Resolution

## Goal

Let agents resolve "the structure around this line" and convert outline selectors into exact line ranges without manually copying numbers.

## Depends On

- Stage 2 parser extraction and selectors.

## Touched Areas

- `filetoolsserver/handler/outline_file.go`
- new `resolve_symbol_range.go`
- `filetoolsserver/handler/tool_types.go`
- `filetoolsserver/handler/cwd_path.go`
- `filetoolsserver/handler/schema_constraints.go`
- `filetoolsserver/server.go`
- tests

## `outline_file` Input Additions

Add:

```go
EnclosingLine *int `json:"enclosing_line,omitempty"`
```

Rules:

- `enclosing_line` is 1-based.
- It can be used with or without `line_window`.
- When set, output includes the innermost exact item containing that line plus parent chain where available.
- If only estimated items contain the line, output warns and does not return a selector for mutation.
- No separate `include_enclosing` flag is added in Phase 6; `enclosing_items` appears when `enclosing_line` is set.

## `outline_file` Output Additions

Add:

```go
EnclosingItems []OutlineItem `json:"enclosing_items"`
```

Rules:

- `enclosing_items[0]` is innermost.
- Parent items follow outward.
- Empty array when no structure contains the line.
- Cwd path behavior unchanged.

## New Tool: `resolve_symbol_range`

Input:

```go
type ResolveSymbolRangeInput struct {
    CwdAwareInput
    SourceFile string `json:"source_file"`
    Language string `json:"language,omitempty"`
    Selector SymbolSelectorQuery `json:"selector"`
    SourceFingerprint FileFingerprint `json:"source_fingerprint"`
}
```

Stage 3 is read/navigation-only. It resolves selectors to concrete ranges and `write_safe` diagnostics, but it does not accept `target_intent`, does not return `recommended_write_call`, and does not produce dry-run range-tool inputs. Stage 4 adds that write-recommendation extension only after the Phase 5 write-safety gate passes.

Selector query fields:

- `symbol_ref` optional if outline returned one;
- `kind`;
- `name`;
- `symbol_path`;
- `disambiguator`;
- `enclosing_line`;
- `allow_estimated` default false.

Concrete selector query schema:

```go
type SymbolSelectorQuery struct {
    SymbolRef string `json:"symbol_ref,omitempty"`
    Language string `json:"language,omitempty"`
    Kind string `json:"kind,omitempty"`
    Name string `json:"name,omitempty"`
    SymbolPath []string `json:"symbol_path,omitempty"`
    Range *SourceLineRange `json:"range,omitempty"`
    RangeFingerprint *FileFingerprint `json:"range_fingerprint,omitempty"`
    Disambiguator string `json:"disambiguator,omitempty"`
    EnclosingLine *int `json:"enclosing_line,omitempty"`
    AllowEstimated bool `json:"allow_estimated,omitempty"`
}
```

Language precedence:

- If only top-level `language` is set, use it for parser selection and selector matching.
- If only `selector.language` is set, use it.
- If both are set to the same normalized language, accept.
- If both are set and normalize to different languages, reject the call with `error_code=selector_language_conflict`; do not guess.

Output:

- `file`;
- `language`;
- `parser_status`;
- `fingerprint`;
- `matches[]` as `ResolvedSymbolMatch`;
- `resolved_ranges[]` as `ResolvedRange`;
- `ambiguous`;
- `resolution_status`;
- `error_code` only for whole-call failures;
- `next_recommended_call` for read/inspection;
- `next_recommended_calls[]` for multiple read/inspection hints using the global hint-list invariant.

Resolved shapes:

```go
type ResolvedSymbolMatch struct {
    SymbolRef string `json:"symbol_ref,omitempty"`
    Kind string `json:"kind"`
    Name string `json:"name"`
    SymbolPath []string `json:"symbol_path,omitempty"`
    Range SourceLineRange `json:"range"`
    ByteRange *SourceByteRange `json:"byte_range,omitempty"`
    Confidence string `json:"confidence"`
    RangeIsEstimated bool `json:"range_is_estimated"`
    WholeLineRange bool `json:"whole_line_range"`
    WriteSafe bool `json:"write_safe"`
    Disambiguator string `json:"disambiguator,omitempty"`
    RefusalReason string `json:"refusal_reason,omitempty"`
}

type ResolvedRange struct {
    Range SourceLineRange `json:"range"`
    ByteRange *SourceByteRange `json:"byte_range,omitempty"`
    Confidence string `json:"confidence"`
    RangeIsEstimated bool `json:"range_is_estimated"`
    WholeLineRange bool `json:"whole_line_range"`
    WriteSafe bool `json:"write_safe"`
    RangeFingerprint FileFingerprint `json:"range_fingerprint"`
    Selector *SymbolSelector `json:"selector,omitempty"`
    RefusalReason string `json:"refusal_reason,omitempty"`
}
```

Rules:

- `resolution_status` values: `resolved`, `ambiguous`, `not_found`, `estimated_only`, `stale`, `failed`.
- Top-level `error_code` is reserved for whole-call failures such as invalid input, source read failure, unsupported language with no usable output, or source fingerprint mismatch.
- `confidence` mirrors outline confidence (`exact`, `estimated`, or parser-specific documented values); `range_is_estimated` is the authoritative boolean for exact-vs-estimated decisions.
- `ResolvedRange.selector` is present only for parser-backed exact ranges. Estimated read-only ranges omit `selector`; do not fake selector metadata for estimated matches.
- Exact but not write-safe ranges, such as same-line fragments, have `range_is_estimated=false`, may include a selector, and set `write_safe=false` with `refusal_reason`; estimated ranges have `range_is_estimated=true`, no selector, and `write_safe=false`.
- `next_recommended_call` and `next_recommended_calls[]` are read/inspection only in Stage 3.
- `next_recommended_calls[]`, when present, is the full ordered hint list. Element 0 must be identical to `next_recommended_call`.

Rules:

- Re-parse file.
- Verify `source_fingerprint` before returning write-eligible ranges.
- If fingerprint mismatch, set `resolution_status="stale"` and top-level `error_code=symbol_fingerprint_mismatch`.
- If multiple exact matches, set `resolution_status="ambiguous"` with candidates; no top-level failure unless caller supplied an invalid selector.
- If no matches, set `resolution_status="not_found"` and `next_recommended_call` for inspection where useful.
- If only estimated matches and `allow_estimated=false`, set `resolution_status="estimated_only"`.
- If `allow_estimated=true`, output ranges are read-only, `write_safe=false`, and `selector` is absent.
- Exact resolved ranges include concrete `SourceLineRange`.
- `resolve_symbol_range` must be added to server cwd acceptance, `AttachCwdOutputMeta`, path sanitizer/projection tests, and schema traversal. Language-local `OutlineItem.path`, `enclosing_path`, and `symbol_path` fields must not be treated as filesystem paths.

## Selector Matching Policy

Matching order:

1. Exact `symbol_ref`.
2. Exact `range` + matching `range_fingerprint`.
3. Exact `symbol_path` + kind + name.
4. Enclosing line + kind/name filter.
5. Name-only only if unique in file.
6. `disambiguator` may narrow candidates from the rules above. It may be a sole exact selector only when it is deterministic for the current file fingerprint/parser scope/version/language/kind/symbol_path/name/range and this is covered by tests.

Ambiguity:

- Never pick one silently.
- Return candidates with range, kind, name, symbol_path, disambiguator.
- If `range` is provided without `range_fingerprint`, return `error_code=selector_range_fingerprint_required`.
- If `range_fingerprint` mismatches the current file fingerprint, return `symbol_fingerprint_mismatch`.

## Acceptance

- Agents can call `outline_file(enclosing_line=N)` and get innermost structure.
- Agents can resolve a function/class/config key to exact range from selector metadata.
- Agents can inspect concrete `resolved_ranges[]` for read/navigation and future Stage 4 write recommendations.
- Stale fingerprints refuse resolution.
- Ambiguous names return candidates, not guessed range.
- Estimated, non-whole-line and parser-partial ranges are read-only by default and never produce mutation recommendations.
- Output paths are cwd-safe.

## Checks

- Enclosing tests for nested Python method, TS class method, JSON nested property, YAML nested key, Markdown heading, Go method.
- Resolve selector by symbol_ref.
- Resolve by kind/name/symbol_path.
- Resolve by exact range + fingerprint.
- `parser_status="partial"` returns read/navigation matches but no write recommendation.
- Range query without fingerprint refusal.
- Top-level `language` vs `selector.language` conflict returns `selector_language_conflict`.
- Ambiguous duplicate function names.
- Fingerprint mismatch.
- Estimated-only refusal.
- Non-whole-line exact range returns `range_is_estimated=false`, `write_safe=false` with `refusal_reason`, without a write recommendation.
- Estimated range returns `range_is_estimated=true`, no selector, `write_safe=false`, and cannot be confused with exact-not-write-safe.
- Cwd tests.

## Handoff / Next Stage

After Stage 3, symbol ranges are resolved for navigation/read. Stage 4 can build safe range-tool recommendations from those resolved ranges after the Phase 5 write-safety gate.

## Stop And Ask If

- Selector matching would require cross-file semantic knowledge.
- The only usable selector is unstable across re-parse of the same file.
- Estimated ranges are needed for mutation.
- A useful recommended write call would need to bypass dry-run preview.
