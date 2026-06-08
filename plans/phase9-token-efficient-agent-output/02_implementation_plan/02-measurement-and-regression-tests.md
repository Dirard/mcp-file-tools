# Stage 2: Measurement And Regression Tests

Goal:
Prove token efficiency through stable normalized response and workflow metrics, not through cosmetic field deletion.

Depends on:
- Stage 1 compact/full contract.
- Existing handler tests in `filetoolsserver/handler/agent_tools_test.go`.

Touched areas:
- `filetoolsserver/handler/agent_tools_test.go`
- Optional helper file under `filetoolsserver/handler/` if the measurement helpers would make the test file harder to read.

Steps:
1. Add a deterministic normalization helper for test outputs:
   - marshal to canonical compact JSON;
   - stable key ordering through Go JSON struct/map behavior or explicit recursive normalization where needed;
   - normalize temp directory paths;
   - normalize timestamps such as `modified_unix_nano`;
   - normalize hashes if the fixture content is not intended to be part of the assertion;
   - normalize `cwd_id` if present.
2. Define metrics in test helper structs:
   - `single_response_normalized_bytes`;
   - `workflow_total_normalized_bytes`;
   - `tool_calls`;
   - `items_returned`;
   - `items_omitted`;
   - `hint_count`;
   - `hint_input_bytes`;
   - optional report-only `estimated_tokens`.
3. Build baseline/current comparison inside tests without external files where possible:
   - compare `agent` to `full` only for the single-response projection budget;
   - compare compact workflow output bytes to an explicit current/pre-projection default agent baseline;
   - create the pre-projection baseline only through test-only code in `_test.go` or test-only helpers;
   - do not add a production legacy mode, config flag, env flag, public API option, or hidden compatibility branch for the baseline;
   - capture the rich current default shape before compact projection and convert it to a legacy public-output JSON fixture in test code;
   - measure the same public structured JSON form that MCP clients see after `server.go` assigns `StructuredContent`;
   - use `full` as workflow baseline only if the test proves the fixture item set and emitted metadata are equivalent to the old default agent shape;
   - keep numbers explainable and deterministic.
4. Public-output measurement rule:
   - all byte metrics serialize public projected output values or `CallToolResult.StructuredContent`;
   - metrics must not serialize internal rich structs unless explicitly measuring the legacy pre-projection baseline in test-only code;
   - any legacy baseline serialization must be labeled as baseline-only and impossible to invoke from production tool inputs.
5. Add fixtures:
   - noise-heavy TSX with imports, exported component, local symbols, JSX;
   - JSON leaf-heavy config;
   - YAML multi-document or nested config;
   - Python function/class for selector roundtrip;
   - one grep fixture with grouped read ranges.
6. Add outline single-response budget tests:
   - TSX/JSON/YAML `agent` normalized bytes are at least 25% smaller than `full`;
   - simple fixtures do not grow more than 10% or 256 bytes, whichever is stricter for the harness.
7. Add workflow budget tests:
   - `outline(agent) -> resolve_symbol_range(symbol_ref) -> read_file(range)`;
   - `outline(agent) -> resolve_symbol_range(symbol_ref, target_intent dry-run)`;
   - `grep -> grouped read_ranges/read_file`;
   - one mandatory discovery hint path using `glob` or `workspace_inventory`, even if no cleanup is implemented.
8. For each canonical workflow:
   - assert total normalized bytes are at least 15% below baseline where this phase changes the workflow;
   - assert `tool_calls <= baseline`;
   - fail if compact output forces an extra full-file read.
9. Add discovery/hint metrics:
   - measure bytes in `next_recommended_call` and one-element `next_recommended_calls` duplication;
   - report whether duplicate hint cleanup is worth implementing;
   - fail only on acceptance budget/tool-call regression, not on cosmetic duplication alone.
10. Add negative false-economy test:
   - compact output cannot pass just because one response is smaller if total calls increase.
11. Add write-metadata noise guard tests:
   - `include_write_metadata=true` is smaller than `full` on JSON/YAML/TSX fixtures when heavy metadata/full semantic expansion would otherwise dominate;
   - it still includes selector/range proof fields needed for resolver/write prep.
12. Keep budget assertions readable:
   - include actual byte counts in failure messages;
   - include which response or workflow exceeded budget;
   - avoid golden blobs that are hard to maintain.
13. Preserve functional tests:
   - existing parser/language expectations remain about symbols/ranges;
   - tests that previously expected metadata in default agent move to `full` or `include_write_metadata`.

Checks:
- Focused measurement tests.
- Existing outline and resolver tests.
- Mandatory discovery/hint workflow measurement test.
- Confirm failure messages explain the budget and actual values.

Handoff / next stage:
After metrics prove savings and no extra calls, Stage 3 can safely tighten hints/docs and run full verification.

Stop and ask if:
- The measured budget cannot be met without hiding data required for navigation or resolver precision.
- Stable normalization requires a large testing framework that would add maintenance cost beyond the value.
