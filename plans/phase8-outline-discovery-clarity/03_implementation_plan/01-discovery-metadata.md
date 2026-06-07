# Stage 1: Discovery Metadata

Goal:
Make tool discovery useful at both lazy-discovery layers: short pre-search summaries should expose all 14 tools clearly, and post-search full descriptions should be compact enough to avoid noise while still carrying the key contracts.

Depends on:
- Accepted concept.
- Generic-agent diagnostic: all 14 tool names are visible in `tool_search` metadata at `max_lines=150`, but full schemas/callables require lazy discovery.

Touched areas:
- `filetoolsserver/server.go`
- `server.json`
- `README.md`
- `TOOLS.md`
- `filetoolsserver/server_test.go`
- any tests that assert description/server instruction text

Public contract:
- Pre-search summaries answer "which tool should I lazy-load?"
- Full tool descriptions answer "how do I call this safely and what next call should I expect?"
- Detailed examples belong in `TOOLS.md`, not in every MCP tool description.
- Descriptions must keep default redaction `off` for `read_files`, `grep`, and previews.

Discovery probe set:
- Query: "find files by name" -> `glob_file_search`
- Query: "symbols in file" -> `outline_file`
- Query: "batch read context" -> `read_files`
- Query: "selector to ranges" -> `resolve_symbol_range`
- Query: "copy range with dry-run preview" -> `copy_ranges` or `move_ranges`
- Query: "repo directory map completeness" -> `workspace_inventory`
- Query: "content search with read ranges" -> `grep`
- Query: "path metadata exists hidden binary" -> `inspect_path`

Steps:
1. Inventory current descriptions in `server.go` and `server.json`.
2. Define a two-layer style guide in code comments or tests:
   - server instructions: one concise line per tool with strongest use case;
   - tool description: purpose, path mode, key inputs, output contract, key pitfall, next-call behavior.
3. Shorten `serverInstructions` while preserving all 14 names and the cwd_id path-mode rule.
4. Rewrite each tool description in `server.go`:
   - `set_cwd`: register cwd_id; no filesystem mutation except allocator state.
   - `read_file`: exact single-file/range read, line-numbered output.
   - `read_files`: batch known files/ranges, coverage/continuation, redaction off by default.
   - `outline_file`: symbols/sections/selectors/fingerprint, supported languages including Java after Stage 2.
   - `resolve_symbol_range`: selector/enclosing line to ranges and dry-run write recommendation.
   - range tools: exact ranges, fingerprints, dry-run, diff/read-back, joiner diagnostics after Stage 4.
   - `list_dir`: direct children only.
   - `glob_file_search`: filename/path discovery.
   - `grep`: content search with structured matches/read ranges.
   - `inspect_path`: one-path metadata and visibility context.
   - `workspace_inventory`: directory map plus canonical completeness fields after Stage 3.
5. Update `server.json` summaries to match the pre-search layer, not the long descriptions.
6. Update README tool list and Agent Workflow with the lazy-discovery baseline:
   - agents may need `tool_search` to make tools callable;
   - metadata should still make the correct tool obvious before loading.
7. Update `TOOLS.md` only where public semantics changed; do not duplicate entire MCP schemas.
8. Add or update tests that assert:
   - all 14 tools are present in server instructions and `server.json`;
   - first-line summaries are distinct for the discovery probe set;
   - descriptions do not regress into overly long mini-docs;
   - redaction default text remains `off`.

Checks:
- Focused server metadata tests.
- Manual compare of `server.go`, `server.json`, README and TOOLS wording for contradictions.
- Later full `go test -count=1 ./...`.

Handoff / next stage:
Stage 2 can update metadata language names after Java support is wired. If Stage 2 changes outline public semantics, return here to align descriptions.

Stop and ask if:
- Eliminating lazy discovery itself becomes a requirement; current accepted scope is making lazy discovery obvious and useful, not changing Codex runtime loading.
