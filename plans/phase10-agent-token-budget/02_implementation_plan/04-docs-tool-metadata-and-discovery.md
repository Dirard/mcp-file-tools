# Stage 4: Docs, Tool Metadata, And Discovery

Goal:
Make the optimized behavior easy for agents to discover and use without spending the saved tokens on confusing descriptions.

Depends on:
- Stage 3 implemented contracts.

Touched areas:
- `filetoolsserver/server.go`
- `README.md`
- `TOOLS.md`
- Relevant schema/docs tests.

Steps:
1. Update `serverInstructions` only where behavior changed.
2. Keep tool descriptions compact and high-signal:
   - what the tool is for;
   - the main output shape;
   - the detail/full escape hatch if relevant;
   - the most important params.
3. Do not stuff full profile semantics into every tool description. Put longer explanations in `TOOLS.md`.
4. Update README workflows:
   - show measurement/compact behavior only where users need it;
   - keep `set_cwd -> discover -> inspect -> resolve -> dry_run -> verify` flow clear;
   - keep default redaction off documented where relevant.
5. Update `TOOLS.md` with exact compact/detail/full contracts for changed tools:
   - fields kept by default;
   - fields available in detail/full;
   - warning that previews are bounded display text and escape-sensitive edits require validation/read-back/read_file.
6. Add or update tests that protect tool metadata:
   - all 14 tools remain registered;
   - descriptions stay below agreed compact length ceilings;
   - high-value terms still appear for discovery, especially `glob_file_search`, `outline_file`, `grep`, `workspace_inventory`, and range tools.
7. Run one generic-agent/tool-list smoke if available in the current environment:
   - verify callable tools surface without extra lazy search when max_lines is high enough;
   - record result in final notes.

Checks:
- Docs match runtime/schema names exactly.
- Description length tests pass.
- Existing server tests pass.
- No docs imply safety-first redaction defaults.
- Runtime redaction default tests and docs agree: omitted `redaction_mode` is `off` for affected tools.

Handoff / next stage:
Stage 5 performs full verification and restart/smoke.

Stop and ask if:
- Discoverability smoke shows compact descriptions hide important callable tools.
- Docs need to describe a breaking behavior change not accepted in concept.
