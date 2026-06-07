# Stage 6: Docs, Smoke, And Review

## Goal

Make Phase 2 usable from MCP clients, prove no read-only regressions, and close the implementation with product and engineering review.

## Depends On

- All handlers and tests from Stages 1-5.

## Touched Areas

- `README.md`
- `TOOLS.md`
- `filetoolsserver/server.go`
- `test_server.go`
- optional smoke fixture files under test temp dirs only
- plan/concept acceptance notes if status changes

## Documentation Steps

1. Update README public surface:
   - Change "read-only" framing to "read-only navigation plus explicit refactor write tools".
   - List all 11 tools.
   - Document that write tools are exact mechanical transfer only.
   - Document `MCP_WRITE_THRESHOLD`.
   - Keep absolute path and no cursor rules prominent.

2. Update TOOLS.md:
   - Add `outline_file`.
   - Add `copy_ranges`.
   - Add `move_ranges`.
   - Add `copy_ranges_batch`.
   - Add `move_ranges_batch`.
   - Include copyable JSON examples for Windows/POSIX placeholders.
   - Include `dry_run`, `fingerprint_only`, next fingerprints, structured errors, and batch partial state examples.

3. Update server tool descriptions:
   - Keep descriptions concise enough for MCP tool metadata.
   - Make `outline_file` the recommended first step.
   - For existing target writes, say to use `outline_file(output_profile="fingerprint_only")`.
   - For destructive multi-target Markdown split, say to use `move_ranges_batch`.

4. Update `test_server.go` smoke:
   - Existing six tools still smoke.
   - Add `outline_file` on Markdown and Go fixture.
   - Add `outline_file fingerprint_only`.
   - Add `copy_ranges dry_run`.
   - Add `copy_ranges create_new`.
   - Add `move_ranges dry_run`.
   - Add `copy_ranges_batch` on temp Markdown fixture.
   - Add `move_ranges_batch dry_run` destructive split on temp Markdown fixture.
   - Add `move_ranges_batch` execution on temp Markdown fixture when smoke can safely mutate only temp files.
   - Add batch partial/recovery smoke if it can be made deterministic without OS-specific fault injection; otherwise cover it in unit tests and reference that in smoke output.
   - Add one structured error smoke for stale fingerprint or range out of bounds.

## Verification Steps

1. Run focused unit tests while developing:
   - handler schema tests;
   - outline tests;
   - range transfer tests;
   - single write tests;
   - batch recovery tests.

2. Run full repository tests:
   - `go test ./...`

3. Run smoke:
   - `go run ./test_server.go`

4. Build:
   - Windows native: `go build -o .\mcp-file-tools.exe .\cmd\mcp-file-tools`
   - Cross-platform build only if release is requested.

5. Restart app/watchdog only after implementation is done and user wants live testing:
   - Native Windows watchdog script is documented in README.
   - Do not restart or mutate running service as part of plan creation.
   - Restart/watchdog is outside implementation acceptance unless the user explicitly asks for live MCP deployment/testing.

## Review Cycle

After implementation:

1. Product review:
   - Confirms tools still follow the accepted concept.
   - Confirms no hidden Markdown naming/split policy was introduced.
   - Confirms 10/10 agent ergonomics acceptance is met.

2. Engineering review:
   - Checks correctness, race handling, symlink safety, schema clarity, test coverage, and maintainability.
   - Checks write-path code is not a monolith.
   - Checks large-file behavior is streaming/hybrid as planned.

3. Repair:
   - Engineering findings go back to implementation owner.
   - Product findings return to concept/plan only if scope or behavior changed.
   - Fresh review after substantive repair.

## Acceptance Checklist

- All new path inputs reject empty and relative paths.
- No new cursor pagination exists.
- `outline_file` exact Markdown and Go behavior matches plan.
- `fingerprint_only` target workflow works for supported and unsupported text files.
- `dry_run` works for every write tool and mutates nothing.
- Single write tools preserve selected bytes.
- Batch write tools support multi-target Markdown decomposition.
- Batch partial failure returns per-target recovery status.
- Boundary warnings appear in dry-run and write outputs.
- Success outputs include next fingerprints.
- Existing six tools pass their old tests.
- README, TOOLS, and server instructions agree.

## Stop And Ask If

- Smoke requires live service restart or watchdog manipulation before the user explicitly asks for implementation/live test.
- Documentation would need to promise behavior not covered by tests.
