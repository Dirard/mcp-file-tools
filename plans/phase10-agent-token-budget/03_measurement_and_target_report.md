# Phase 10 Measurement And Target Report

Baseline:
- Immutable post-Phase-9 baseline is stored in `filetoolsserver/handler/token_budget_test.go`.
- Primary metric is normalized JSON bytes across canonical workflows.
- Baseline `tool_metadata` was 226611 bytes per workflow for the full registered 14-tool schema surface, dominating every workflow.

Selected target:
- Compact registered output schemas for MCP tool metadata.
- Keep input schemas unchanged.
- Keep Phase 10 runtime structured JSON unchanged except for the dry-run validation hint below. The post-Phase-9 baseline already includes the accepted `outline_file` agent projection.
- Advertise top-level action/correctness fields and allow additional runtime properties for compatibility.
- Keep exact `set_cwd` output schema because it is small and has a precise success/error contract.

Current measured result:
- Registered schema `tool_metadata` is now 35148 bytes per workflow.
- Representative workflow totals:
  - `discovery_to_read`: 235118 -> 43655 bytes.
  - `grep_to_read_range`: 229204 -> 37741 bytes.
  - `read_files_continuation`: 230634 -> 39171 bytes.
  - `resolve_dry_run_to_read_validation`: 233866 -> 43210 bytes.
  - `batch_dry_run`: 229996 -> 38533 bytes.
- Tool calls stayed equal to baseline for all canonical workflows.
- Runtime outputs are validated against the advertised compact schemas in focused tests.
- Actual MCP `ListTools` metadata, including descriptions and annotations, is covered by a server-level compact budget test.

Dry-run caveat fix:
- `copy_ranges`/`move_ranges` dry-run validation now exposes a `read_file` recommendation for the selected source range.
- The reason states that previews are bounded display text and escape-sensitive edits should be verified with `read_file`/read-back before applying.

Rejected targets for this pass:
- Response compaction for read/search/range outputs: response bytes were not the dominant cost after Phase 9.
- Removing correctness fields, stale proofs, continuation inputs, read ranges, fingerprints, validation, joiner diagnostics, or warnings.
- Compacting `set_cwd` output schema.
