# Phase 9: Token-Efficient Agent Output Concept

Goal:
Make mcp-file-tools materially cheaper for agents to use while preserving the practical usefulness that makes the tools valuable: fast navigation, exact ranges, safe write preparation, continuation, and useful next-call guidance.

User / maintainer:
The primary user is an LLM coding agent working inside a real repository. The maintainer needs output contracts that stay understandable, testable, and compatible enough that agents can choose the right tool without reading whole files or guessing ranges.

Scope:
- Make `outline_file` default `output_profile="agent"` navigation-first and compact.
- Keep exact write metadata available through `output_profile="full"` and `include_write_metadata=true`.
- Ensure compact outline items still round-trip through `resolve_symbol_range` using `symbol_ref`, ranges, and the top-level source fingerprint.
- Add measurement tests for normalized response size and workflow-size budgets, not just functional correctness.
- Remove or reduce obvious duplicate metadata where it does not help the next agent action.
- Add bounded cleanup for duplicated single next-call hints only when it is directly measurable and does not remove useful guidance.
- Leave `workspace_inventory` structural dedupe as a follow-up unless the outline measurement harness shows it is needed for the current acceptance budgets.

Out of scope:
- Do not remove core tools.
- Do not remove exact line ranges, top-level fingerprints, continuation, `grep.file_groups.read_ranges`, or write dry-run validation.
- Do not reintroduce safety-first redaction defaults; default redaction remains off.
- Do not optimize by blindly lowering `max_items`, hiding useful symbols, or replacing visible ranges with opaque server-only handles.
- Do not make broad protocol-breaking API changes without an explicit compatibility path.

Must not break:
- `outline_file` remains useful as the first tool for understanding JS/TS/TSX, Python, Java, Go, Markdown, JSON, YAML, Rust, C, C++, C#, Ruby, Kotlin, Swift, Bash, Svelte, and generic text.
- Compact outline output must be enough for common agent navigation decisions.
- A compact outline item must be resolvable into exact source ranges without requiring a full-file read.
- `output_profile="full"` must expose the detailed metadata needed by advanced/debug/write workflows.
- Existing range-transfer dry-run/read-back semantics remain clear, especially for escape-sensitive code.
- Tool discovery descriptions remain high-signal enough for agents to find the right callable tools.

Compatibility contract:
- This phase intentionally changes the default `outline_file` `output_profile="agent"` item shape to a compact navigation contract. That is acceptable because the product goal is maximum agent usefulness per token, and detailed metadata remains available explicitly.
- `output_profile="full"` must preserve the old detailed outline item shape as the compatibility/debug/write-metadata path.
- `include_write_metadata=true` on `agent` adds write/range proof metadata without expanding unrelated parser noise or JSON/YAML leaf detail.
- The output schema must mark profile-projected fields as optional and must not describe hidden default fields as required.
- Agents that need to prepare a write from compact outline output should call `resolve_symbol_range` with the outline response top-level `fingerprint` as `source_fingerprint`.
- Direct copy/move/write from a compact item range without `resolve_symbol_range` is not a supported shortcut for write workflows.

Compact item contract:
- Default `agent` items include `kind`, `name`, `range`, `symbol_ref` when available, `path` and `enclosing_path` when useful, `depth` when non-zero, and `children` for hierarchy.
- Default `agent` items hide `selector`, per-item `range_fingerprint`, `byte_range`, `whole_line_range`, `write_safe`, `refusal_reason`, and heavy `metadata`.
- `symbol_ref` is a stateless visible selector derived from file/content facts. It is not a server-side handle, cache key, registry id, or hidden mutable state.
- `confidence` and `range_is_estimated` may remain in compact output only if they materially affect agent action; otherwise they belong with write/debug metadata.

Resolver contract:
- `resolve_symbol_range` must accept compact selector forms:
  - `symbol_ref + source_fingerprint`;
  - `range + source_fingerprint` without per-item `range_fingerprint`;
  - existing full selector input with `range_fingerprint`.
- If `range` is supplied without `range_fingerprint`, the resolver treats the top-level `source_fingerprint` as the proof snapshot.
- Compact round-trip acceptance means exact source ranges can be recovered without a full-file read.

Key decisions:
- The largest win is field-level projection, not deleting functionality.
- Default `agent` outline should include navigation fields: `kind`, `name`, path context, `range`, `depth`, `children`, and `symbol_ref`.
- Per-item `selector`, `range_fingerprint`, `byte_range`, `whole_line_range`, `write_safe`, `refusal_reason`, and heavy `metadata` should move out of default output unless requested.
- Top-level file fingerprint remains the snapshot authority for resolver/write workflows.
- `resolve_symbol_range` should accept compact handoff data and use `source_fingerprint` as proof when per-item fingerprints are omitted.

Open questions: none.

Success:
- Noise-heavy outline `agent` responses are at least 25% smaller than `full` by normalized response bytes while still leading to correct resolver and read-range calls.
- Canonical workflows use at least 15% fewer total normalized response bytes than the current baseline without increasing tool call count.
- Agents can still choose and chain tools with minimal guesswork.
- Full-profile users can still retrieve detailed metadata explicitly.

Acceptance budgets:
- Normalized response size is measured from canonical compact JSON with stable key ordering, no pretty-print whitespace, normalized temp paths, normalized timestamps, normalized hashes, and normalized cwd ids.
- Primary metric: `workflow_total_normalized_bytes`.
- Guardrail metric: `tool_calls`; for canonical workflows it must be `<= baseline`.
- Supporting metrics: `single_response_normalized_bytes`, `items_returned`, `items_omitted`, `hint_count`, `hint_input_bytes`, and `estimated_tokens` as report-only.
- Canonical workflows:
  - `outline(agent) -> resolve_symbol_range(symbol_ref) -> read_file(range)`;
  - `outline(agent) -> resolve_symbol_range(symbol_ref, target_intent dry-run)`;
  - `grep -> grouped read_ranges/read_file`;
  - `glob or workspace_inventory hint -> next useful discovery/read call`.
- A smaller single response fails acceptance if the workflow needs more calls, hidden full-file reads, or manual range reconstruction.

Unacceptable result:
- Token savings come from making agents read whole files more often.
- Token savings miss the acceptance budgets.
- Range/write workflows become guessy or require manual reconstruction.
- Helpful next-call guidance disappears instead of being bounded.
- Compact output hides enough structure that agents lose confidence about what to inspect next.
- `full` or `include_write_metadata=true` no longer exposes the detailed metadata path.

Technical direction:
- Add an output projection layer after outline construction so parsers can keep rich internal metadata while default responses are compact.
- Add input support for `include_write_metadata`, with `full` implying it.
- Update resolver tests to prove compact `symbol_ref + source_fingerprint` round-trips.
- Update resolver behavior to accept `range + source_fingerprint` without per-item `range_fingerprint`.
- Add normalized response-size fixtures and budget checks for outline, grep/glob hints, workspace inventory, and discovery metadata.

Risks:
- Changing default `agent` shape is user-visible. Mitigate by documenting `full` and the explicit write-metadata flag.
- Over-compressing hints can hurt agent success. Measure workflow bytes and call count, not single-response size only.
- Existing tests may assert old field presence in default profile. Update them to assert behavior and profile-specific contracts.

Checks to consider:
- Focused outline profile tests for TSX, JSON, YAML, Python, Go/Markdown, and the recently added languages.
- Resolve round-trip tests from compact outline items.
- Normalized response-size tests comparing compact agent output against full output.
- Workflow-size regression tests for outline-to-resolve-to-read and grep-to-read paths.
- `go test -count=1 ./...`, `go vet ./...`, build, MCP restart, smoke.
