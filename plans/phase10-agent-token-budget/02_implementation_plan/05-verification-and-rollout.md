# Stage 5: Verification And Rollout

Goal:
Prove Phase 10 works end to end, rebuild/restart the local MCP server, and hand off a concise result.

Depends on:
- Stages 1-4 complete.
- Plan review clean.

Touched areas:
- Tests and docs touched by implementation.
- Local build output `mcp-file-tools.exe`.
- Local MCP/watchdog process.

Steps:
1. Run focused tests for new ledger/budget/projection/schema behavior.
2. Run existing focused tests around risky preserved behavior:
   - `outline_file` profiles and language outlines;
   - `resolve_symbol_range` compact roundtrip and target intent;
   - `read_file` and `read_files` continuation/stale paths;
   - `grep` file groups and read ranges;
   - `glob_file_search` continuation/no-result;
   - `workspace_inventory` page/summary completeness;
   - range dry-run validation, joiner diagnostics, boundary warnings, and escape-sensitive caveat.
   - workflow tests that derive follow-up calls from returned compact/default fields.
   - default redaction-off runtime regression tests.
3. Run full local checks:
   - `go test -count=1 -parallel=1 ./...`;
   - `go vet ./...`;
   - `go build -o .\mcp-file-tools.exe .\cmd\mcp-file-tools`.
4. Restart MCP/watchdog:
   - stop the old local `mcp-file-tools.exe` process if it is the workspace build;
   - let watchdog restart or start the expected server path;
   - verify `http://127.0.0.1:8787/healthz` returns `ok` if that endpoint is active.
5. Smoke real tool calls after restart:
   - `workspace_inventory` on repo root;
   - `glob_file_search` for Go files;
   - `grep` literal hit followed by read range;
   - `outline_file` compact and full on a representative file;
   - `resolve_symbol_range` from compact `symbol_ref`;
   - `copy_ranges dry_run` on a temp target.
6. Run required implementation review cycle:
   - `product_owner` checks product value and faithfulness to the token-efficiency concept;
   - `reviewer` checks engineering quality, bugs, regressions, maintainability, schema/docs drift, tests.
7. If review finds issues, repair and rerun focused checks plus targeted recheck. Run fresh review when repair is broad or changes contracts.
8. Final response:
   - summarize measured savings and selected targets;
   - state checks run;
   - state MCP rebuild/restart status;
   - mention no commit unless user requested one.

Checks:
- All required tests/build/vet pass.
- MCP is rebuilt and restarted.
- Smoke confirms compact defaults are usable and detail/full paths still work.
- Product/reviewer implementation review clean.

Stop and ask if:
- Full verification repeatedly fails in a way that requires changing concept thresholds or public contracts.
- Restart would affect a live/external system rather than the local development MCP process.
