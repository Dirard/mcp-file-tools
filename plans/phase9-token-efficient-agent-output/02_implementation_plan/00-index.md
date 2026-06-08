# Phase 9 Implementation Plan Index

Goal:
Implement token-efficient agent output with the first and strongest win in `outline_file`: compact navigation-first `agent` responses, explicit rich metadata on request, and measurable workflow savings without loss of resolver/write usefulness.

Scope:
- Add compact/default outline response projection.
- Add `include_write_metadata` input behavior.
- Preserve `output_profile="full"` as the detailed compatibility path.
- Update resolver compact handoff rules.
- Add normalized response-size and workflow-size tests.
- Bound small next-call duplicate cleanup when directly covered by tests.

Out of scope:
- Removing tools or core fields such as top-level fingerprints, exact line ranges, continuation, `grep.file_groups.read_ranges`, or dry-run validation.
- Reintroducing safety-first redaction defaults.
- Broad `workspace_inventory` restructuring unless required to pass the current workflow budgets.
- Opaque server handles, hidden cache state, or server-side selector registries.
- Blind `max_items` reductions as a token optimization.

Must preserve:
- Agents can use compact `outline_file(agent)` to navigate without reading whole files.
- Agents can call `resolve_symbol_range` from compact outline data and receive exact ranges.
- Agents can request full write/debug metadata explicitly.
- Full output remains detailed enough for existing advanced tests and debugging.
- JSON/YAML/TSX compact profile stays useful rather than merely small.

Concept transferred into plan:
- User-visible result: fewer tokens/noise in common agent workflows, especially outline-first workflows.
- Behavior / contracts: default `agent` item shape changes intentionally; `full` and `include_write_metadata=true` expose rich metadata.
- Acceptance: at least 25% smaller noise-heavy outline agent output versus full, at least 15% lower canonical workflow bytes versus current baseline, and no increase in tool calls.

Plan file map:
- `01-outline-contract-and-projection.md` -> API contract, schema, projection, resolver compatibility.
- `02-measurement-and-regression-tests.md` -> normalized size harness, workflow budgets, fixtures, acceptance tests.
- `03-hints-docs-and-final-checks.md` -> bounded hint cleanup, docs/tool descriptions, full verification and MCP restart/smoke.

Global decisions:
- Build rich outline data as today, then project at the response boundary.
- Do not mutate parser/internal outline structures for compact output.
- Split internal rich outline data from the public projected outline output. `HandleOutlineFile` must return the public projected output that `server.go` places into MCP `StructuredContent`.
- Use dedicated public projected response/item DTOs for `outline_file`; do not rely on zeroing required rich struct fields that still marshal as noisy JSON.
- `symbol_ref` remains visible and stateless.
- `full` implies write metadata.
- `include_write_metadata=true` adds write/range proof metadata without expanding unrelated semantic noise.
- Workflow baselines must compare against the current pre-projection default agent shape, not against a wider `full` profile unless equivalence is explicitly proven for that fixture.
- Baseline helpers are test-only and measure serialized public-output JSON; no production legacy mode, flag, or hidden compatibility branch is allowed.

Global risks:
- Tests currently expect selector/range fingerprint metadata in default agent output; update them to assert profile-specific behavior.
- Resolver currently rejects `range` selectors without per-item `range_fingerprint`; update this before relying on compact range fallback.
- Schema currently marks some compact-hidden fields required; update schema together with output behavior.
- Single-response savings can be fake if workflows need more calls; acceptance must measure end-to-end workflow cost.
- Continuation/retry hints can accidentally drop `include_write_metadata=true`; preserve the flag in any same-outline follow-up input.

Global checks:
- Focused handler tests for outline compact/full/write-metadata profiles.
- Compact outline -> resolve -> read and compact outline -> resolve -> dry-run write tests.
- Normalized response-size budget tests.
- Discovery/hint workflow budget test for at least one `glob` or `workspace_inventory` path.
- Existing outline language tests across major languages.
- `go test -count=1 ./filetoolsserver/handler -run "TestOutlineFile|TestResolveSymbol|TestToken|TestResponseSize|TestSchema"`
- `go test -count=1 ./...`
- `go vet ./...`
- `go build -o .\mcp-file-tools.exe .\cmd\mcp-file-tools`
- Restart/watchdog MCP and smoke `outline_file`, `resolve_symbol_range`, `grep/read_ranges`.

Stop and ask if:
- A stronger backward-compatibility rollout is required than `full/include_write_metadata` opt-in.
- Meeting budgets requires removing useful navigation data.
- Any change would require destructive git, publishing, network release work, or reading secrets.
