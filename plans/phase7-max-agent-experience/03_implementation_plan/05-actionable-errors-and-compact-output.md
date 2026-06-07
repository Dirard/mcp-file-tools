# Stage 5: Actionable Errors And Compact Output

## Goal

Make common failures and noisy outputs more useful without hiding proof required for safe next actions.

## Depends On

- Stage 3 profile behavior.
- Stage 4 next-call infrastructure.

## Touched Areas

- shared error/action hint helpers
- `resolve_symbol_range.go`
- range transfer error outputs
- outline profile errors
- schema/docs/tests

## Actionable Failure Contract

For deterministic repair paths, output should include one small recommended next action.

Required cases:

- stale source fingerprint -> `outline_file` refresh input;
- target fingerprint mismatch -> target `outline_file` or `inspect_path` refresh input;
- out-of-range -> bounded `read_file` or `outline_file`;
- ambiguous selector -> narrowed selector examples using kind/name/path/range;
- invalid `redaction_mode` or `output_profile` -> valid enum values and corrected minimal input;
- JSON/YAML write-unsafe node -> exact read recommendation plus delimiter/indent reason;
- target precondition cannot be prepared -> exact target inspection call.

## Compact Output Contract

Compact output in Phase 7 is limited to JSON/YAML leaf-noise reduction and related omitted-count hints. Fingerprint compaction for repeated nested outline items is explicitly deferred.

In-scope changes:

- include omitted counts and recommended full/narrow call;
- keep full profile capable of returning detailed proof;
- keep write-prep proof in `resolve_symbol_range` recommendations.

Out of scope for Phase 7:

- moving or removing repeated `range_fingerprint` fields from existing non-JSON/YAML outline items;
- changing selector proof shape for existing languages;
- removing fields needed by existing write tools.

## Steps

1. Inventory current `ErrorCode`, `ActionHint`, `NextRecommendedCall` payloads.
2. Add or normalize helper for minimal repair hints.
3. Update invalid enum errors for `redaction_mode` and `output_profile`.
4. Update stale fingerprint and target mismatch outputs.
5. Update ambiguous selector output to provide concrete narrowing options.
6. Update JSON/YAML write-unsafe refusal reason to recommend exact read.
7. Add targeted tests for every required failure case.
8. Add tests that Phase 7 compactness comes from JSON/YAML leaf omission, not removal of required write-proof fields.

## Checks

- Failure outputs remain structured JSON and do not leak absolute paths under `cwd_id`.
- Recommended repair inputs validate against schemas where applicable.
- Compact profile does not break write preparation.

## Handoff / Next Stage

After Stage 5, Phase 7 behavior should be implemented. Stage 6 updates docs, runs full checks, rebuilds and restarts runtime.

## Stop And Ask If

- Compacting output would remove required data for existing clients without a replacement.
- A repair hint would require guessing a path or action.
