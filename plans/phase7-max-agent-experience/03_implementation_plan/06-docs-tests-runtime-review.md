# Stage 6: Docs, Tests, Runtime, And Review

## Goal

Make Phase 7 externally usable, documented, tested and reviewed.

## Depends On

- Stages 1-5 implementation.

## Touched Areas

- `README.md`
- `TOOLS.md`
- `server.json`
- `filetoolsserver/server.go`
- generated/runtime schemas
- tests
- `mcp-file-tools.exe` rebuild and watchdog restart

## Docs Requirements

Docs should teach the simple high-value agent workflow:

1. `set_cwd`
2. `grep` / `glob_file_search`
3. `read_file` / `read_files` literal by default
4. `outline_file output_profile="agent"`
5. `resolve_symbol_range target_intent`
6. recommended dry-run write call
7. apply after preview

Docs must state:

- default redaction is `off`;
- `strict` is explicit opt-in;
- `auto` is deprecated compatibility alias;
- `read_file` is literal-only;
- `read_files` defaults to literal;
- JSON/YAML `agent` vs `full` profiles;
- Unicode previews are valid display text;
- `workspace_inventory` stays directory-level.

## Test Requirements

Required test groups:

- redaction defaults and strict false-positive protection;
- read_file/read_files default parity;
- Unicode truncation with Cyrillic, combining marks and emoji sequences;
- JSON/YAML profile filtering and path grammar;
- search-to-read/outline recommendations;
- write-prep recommendations;
- actionable failure hints;
- cwd projection;
- schema enum/docs consistency.
- registered public tool list remains exactly the existing 14 tools; no new public tool/schema name is added.

## Review Requirements

After implementation:

- product_owner review checks user intent: max AX, no default masking, fewer manual steps, no safety-first drift.
- reviewer review checks engineering quality: bugs, regressions, readability, clean contracts, schema consistency and tests.
- If review findings require substantive repair, repair and run fresh review as required by `$codex-flow-v2`.

## Runtime Requirements

After clean implementation review:

1. Run full offline tests.
2. Build `mcp-file-tools.exe`.
3. Restart watchdog/server from fresh binary.
4. Confirm process and log start.
5. Smoke test at least:
   - `read_files` default literal;
   - strict redaction preserves paths/keys;
   - Unicode preview no U+FFFD (`\uFFFD` / `�`) because of truncation and no mojibake sequence `ï¿½`;
   - JSON/YAML `agent` vs `full`;
   - one recommended next call validates and can run.

## Checks

- `go test -count=1 ./...` passes with `GOFLAGS=-mod=vendor`, `GOPROXY=off`, `CGO_ENABLED=0`.
- Targeted race test passes for changed handler surfaces.
- Build succeeds.
- MCP/watchdog restarted.

## Stop And Ask If

- Tests reveal a product-level choice not covered by concept/SRS.
- Runtime restart cannot be completed after build.
