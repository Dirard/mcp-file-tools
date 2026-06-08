# Stage 3: Hints, Docs, And Final Checks

Goal:
Finish the phase without letting secondary optimizations create noise or regressions.

Depends on:
- Stage 1 compact/full/resolver behavior.
- Stage 2 measurement harness and budget tests.

Touched areas:
- `filetoolsserver/handler/*` hint helpers only where directly covered by tests.
- `filetoolsserver/server.go`
- `README.md`
- `TOOLS.md`
- `plans/phase9-token-efficient-agent-output/*` only for final notes if needed.

Steps:
1. Inspect duplicate next-call behavior only after Stage 2 metrics exist.
2. If a single `next_recommended_call` and a one-element `next_recommended_calls` materially affect measured bytes, add a small bounded cleanup:
   - preserve the single primary hint;
   - preserve multiple hints when they genuinely offer alternatives;
   - do not remove useful `grep.file_groups.read_ranges` or continuation inputs.
3. If hint cleanup is not needed for acceptance budgets, leave it as follow-up rather than expanding scope.
4. Base the cleanup/deferral decision on the mandatory Stage 2 discovery/hint metric, not on subjective output preference.
5. Do not restructure `workspace_inventory` in this phase unless Stage 2 proves it is necessary to meet a canonical workflow budget.
6. Update tool descriptions and docs:
   - default outline is compact navigation;
   - use `output_profile="full"` for detailed metadata;
   - use `include_write_metadata=true` when agent wants write/range proof fields without unrelated full-profile detail;
   - compact write workflows should go through `resolve_symbol_range`.
7. Keep discovery descriptions compact but high-signal:
   - preserve routing keywords for all tools;
   - avoid long prose that crowds tool discovery;
   - do not hide core capabilities.
8. Run focused checks:
   - `go test -count=1 ./filetoolsserver/handler -run "TestOutlineFile|TestResolveSymbol|TestSchema|TestToken|TestResponseSize"`
9. Run full checks:
   - `go test -count=1 ./...`
   - `go vet ./...`
   - `go build -o .\mcp-file-tools.exe .\cmd\mcp-file-tools`
10. Restart/check local MCP/watchdog only after build succeeds:
   - confirm `/healthz` or equivalent watchdog health;
   - smoke `outline_file(agent)`, `outline_file(full)`, `outline_file(include_write_metadata=true)`, `resolve_symbol_range`, and one grep/read_ranges path.
11. Run implementation review cycle:
   - `product_owner` for product value and faithfulness to token-efficiency goal;
   - `reviewer` for engineering quality, compatibility, tests, and maintainability.
12. Repair findings in scope and rerun necessary checks.

Checks:
- Focused tests.
- Full tests, vet, build.
- MCP restart/smoke.
- Product and engineering review clean.

Handoff / next stage:
When checks and review are clean, summarize changed files, measured savings, compatibility behavior, and any intentionally deferred follow-up.

Stop and ask if:
- Any docs/update path requires publishing or release work.
- Any optimization would remove useful agent guidance rather than bounding duplication.
