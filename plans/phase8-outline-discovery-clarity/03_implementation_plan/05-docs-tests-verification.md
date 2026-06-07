# Stage 5: Docs, Tests, Verification, And Smoke

Goal:
Prove Phase 8 improved practical Agent Experience and did not only add internal code paths.

Depends on:
- Stages 1 through 4.

Touched areas:
- `README.md`
- `TOOLS.md`
- `server.json`
- `filetoolsserver/server.go`
- schemas in `filetoolsserver/handler/*_schema.go`
- tests under `filetoolsserver` and `filetoolsserver/handler`
- watchdog/restart smoke scripts only as verification targets, not as edited scope unless they fail because of Phase 8

Docs consistency steps:
1. Update README public tool list:
   - mention Java in outline language list;
   - mention canonical inventory completeness fields;
   - mention joiner dry-run diagnostics;
   - keep default redaction `off`.
2. Update Agent Workflow:
   - acknowledge lazy loading: use `tool_search` when MCP file tools are not callable yet;
   - after discovery, use `outline_file`/`resolve_symbol_range`/range dry-run path.
3. Update TOOLS sections:
   - `outline_file` supported languages and `agent`/`full` profile contract;
   - `resolve_symbol_range` selector round-trip expectations;
   - range tools `joiner_effect` fields and warnings;
   - `workspace_inventory` canonical completeness fields and legacy aliases.
4. Update `server.json` summaries to remain short and aligned with Stage 1.
5. Ensure output schemas include every new public field and reject undocumented extra fields only where intended.

Agent-facing probe tests:
1. Discovery metadata probe:
   - assert all 14 names are present in compact metadata;
   - assert probe keywords map to the intended tool descriptions.
2. Outline probe:
   - fixture outputs are compact enough to identify next action without broad manual reads;
   - `full` profile returns extra details omitted by `agent`.
3. Inventory probe:
   - page and summary/tree completeness are visibly separate.
4. Joiner probe:
   - dry-run explains actual visual blank-line outcome.

Command verification sequence:
1. Run focused tests after each implementation slice:
   - outline tests;
   - resolver tests;
   - inventory tests;
   - range/joiner tests;
   - metadata/docs/schema tests.
2. Run `go test -count=1 ./filetoolsserver/handler`.
3. Run `go test -count=1 ./...`.
4. Run targeted race tests where practical:
   - outline/search/write/inventory packages or test names, bounded to avoid runaway runtime.
5. Run normal Windows build:
   - `go build -o .\mcp-file-tools.exe .\cmd\mcp-file-tools`
6. Restart MCP/watchdog only after tests/build are clean and only if it stays within local verification scope.
7. Run smoke:
   - server starts;
   - tools list exposes 14 tools;
   - `outline_file` Java fixture returns parser-backed output;
   - `workspace_inventory` returns canonical completeness fields;
   - range dry-run returns joiner diagnostics.

Review cycle:
1. After implementation, run `product_owner` review for faithfulness to 10/10 AX and no redaction/product drift.
2. Run independent `reviewer` review for engineering quality, regressions, maintainability, naming, and tests.
3. Repair findings in exact scope.
4. If repair is substantive, run fresh independent review according to flow.

Completion criteria:
- Product and engineering reviews are clean or remaining P3 items are explicitly deferred with reason.
- Required checks pass or unchecked items are named with concrete reason.
- No branch is created.
- No unrelated user/pre-existing changes are reverted.

Stop and ask if:
- Verification requires network, publishing, live service access, secret reads, destructive git, or external-system mutation.
- Restart/smoke would affect a live service beyond local MCP verification.
