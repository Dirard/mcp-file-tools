# Stage 3: Schema And Public Tool Surface

## Goal

Make tool discovery, manual schemas, docs, and manifest metadata describe the same dual path contract that runtime enforces.

## Files To Add Or Change

- `filetoolsserver/server.go`
- `filetoolsserver/server_test.go`
- `filetoolsserver/handler/schema_constraints.go`
- `filetoolsserver/handler/outline_schema.go`
- `filetoolsserver/handler/workspace_inventory_schema.go`
- `filetoolsserver/handler/tool_types.go`
- `filetoolsserver/handler/refactor_types.go`
- `server.json`
- `README.md`
- `TOOLS.md`
- `Dockerfile`
- `docker-compose.yml`
- `smithery.yaml`
- package/runtime config files if present
- `test_server.go` if its examples or expected tool count are stale

## Tool Registration

Add `set_cwd` to the server tool list.

Recommended MCP annotations:

- `ReadOnlyHint`: false, because it changes server registry state and may create or update server-local allocator state at `MCP_CWD_STATE_PATH`
- `DestructiveHint`: false
- `IdempotentHint`: false, because it may allocate a new id or refresh TTL
- `OpenWorldHint`: false

Public wording must say `set_cwd` does not mutate the registered workspace/target filesystem; it may mutate server-local allocator state.

Update server instructions from "eleven tools" to "twelve tools".

Update every path tool description:

- Without `cwd_id`, pass absolute paths.
- With `cwd_id`, pass relative paths under that cwd.
- Output paths are slash-normalized.
- In cwd mode, output paths are relative except `cwd`.

## Input Schema Contract

Add optional `cwd_id` to each existing path tool input schema.

Do not add `cwd_id` to `set_cwd`.

`set_cwd` must reject unknown input fields at runtime. The schema exposes only `directory`, but runtime validation must also fail extra fields, especially accidental `cwd_id`, instead of relying on default JSON unmarshalling behavior that ignores unknown fields.

For path fields in existing tools, schema should communicate:

- absolute path when `cwd_id` is absent
- relative path when `cwd_id` is present
- the relative rule applies to top-level path fields and nested path fields, including batch `targets[].target_file`
- runtime validation is authoritative

Schema shape:

- Add a top-level `cwd_id` property with integer type and minimum 1.
- Add maximum `9007199254740991` to every `cwd_id` schema property.
- Do not include `null` in the `cwd_id` schema; `cwd_id: null` is invalid, while an absent field means no-cwd mode.
- Replace unconditional absolute-only path descriptions with conditional wording.
- If feasible in the existing jsonschema library without misleading discovery output, use `anyOf` or `if/then` to express:
  - no `cwd_id`: path fields match server absolute path pattern
  - with `cwd_id`: path fields do not match server absolute path pattern
- If exact conditional JSON Schema is too brittle, use less restrictive string schemas plus explicit descriptions and rely on runtime validation/tests to enforce behavior. This fallback is accepted only if every top-level and nested path input description clearly says that relative paths require `cwd_id` and no-cwd mode still requires absolute paths.

Runtime decoding is authoritative and must use the shared presence-aware `cwd_id` input representation from Stage 1. Schema generation and input structs must be tested together so absent `cwd_id`, `cwd_id: null`, `cwd_id: 0`, malformed values, and positive ids cannot collapse into the same Go value.

The schema must not continue to say that relative paths are forbidden everywhere, and it must not describe relative paths as valid without `cwd_id`.

## Output Schema Contract

All existing path tool outputs:

- include optional `cwd_id`
- include optional `cwd`
- describe `cwd` as the absolute slash-normalized cwd for cwd-aware responses
- describe filesystem path fields as either slash-normalized absolute/display paths in no-cwd mode or cwd-relative paths in cwd mode
- `inspect_path` output schema includes optional boolean `symlink_target_outside_cwd`; in cwd mode, when this is true, `symlink_target` is omitted
- cwd-aware recovery/action hints for expired, unknown, or unavailable cwd ids must expose `recommended_next_tool: "set_cwd"` and must not include an absolute `directory` value

`set_cwd` success output:

- required `cwd_id`
- no `cwd`
- no `directory`
- no other metadata

`set_cwd` error output:

- allows `error`
- allows `error_code`
- allows optional `action_hint`
- does not include `cwd`, `directory`, or raw absolute paths
- is documented separately from the success payload so clients know successful `set_cwd` returns only `cwd_id`

All existing path tools must share one cwd-aware error envelope, including tools whose current outputs only have plain `error`:

- `error_code`
- `error`
- supplied `cwd_id` only when the request included a parseable positive integer id
- `cwd` when resolved
- `action_hint`

For `invalid_cwd_id` caused by `null`, malformed, string, fractional, zero, or negative values, the error schema allows `error_code` and `error` but omits `cwd_id` and must not echo the raw invalid value.
- `partial_state` where existing refactor tools already support it
- generated error, warning, backup, target, and partial-state error strings without raw absolute path leaks in cwd mode

For unknown, expired, or unavailable cwd ids on any of the 11 existing path tools, the envelope must include:

```json
{
  "error": "cwd id is expired",
  "error_code": "cwd_id_expired",
  "cwd_id": 1,
  "action_hint": {
    "safe_to_retry": false,
    "recommended_next_tool": "set_cwd"
  }
}
```

Do not include `action_hint.recommended_next_input.directory` for these stale-cwd recovery cases. The agent must use its remembered cwd context to call `set_cwd(directory)` again.

## Manual Schemas

`outline_schema.go`:

- Add top-level `cwd_id` and `cwd` output metadata.
- Keep `OutlineItem.path` as structural ancestry, not a filesystem path.
- Mark only top-level file fields and recommended next input path fields as filesystem paths.
- Document generic text fallback status and metadata.

`workspace_inventory_schema.go`:

- Add top-level `cwd_id` and `cwd` metadata.
- Mark recursive node `path` fields as filesystem paths that become cwd-relative in cwd mode.

`schema_constraints.go`:

- Include `directory` as a path input property for `set_cwd`, but apply absolute-only rules to it.
- Include new output metadata fields without incorrectly marking `cwd_id` as a path.
- Include `symlink_target_outside_cwd` as non-path boolean output metadata.
- Continue to skip non-filesystem fields even when named `path`.
- Update path output descriptions to mention slash normalization and cwd-relative projection.
- Update `ApplyPathOutputSchemaConstraints` so output path fields do not retain an unconditional absolute-only regex/pattern. Output path fields can be slash-normalized absolute/display paths in no-cwd mode or cwd-relative strings in cwd mode, so use a conditional schema if feasible or a relaxed string schema with precise descriptions. The relaxed fallback must be paired with schema-description tests, not left as an implicit public contract.

## server.json

Update the public manifest:

- tool count: 12
- add `set_cwd`
- update all path descriptions from absolute-only to conditional wording
- ensure examples use `D:/...` rather than escaped Windows backslashes
- describe `cwd_id` as a small server-wide integer id with TTL, not as a session token
- include `cwd_id`, `cwd`, `error_code`, and `action_hint` in output schemas for all existing path tools, including non-refactor tools

## Runtime Configuration Surfaces

Files:

- `Dockerfile`
- `docker-compose.yml`
- `smithery.yaml`
- package/runtime config files if present

Tasks:

- Expose `MCP_CWD_STATE_PATH` as an absolute server/container-local path.
- Expose `MCP_CWD_REQUIRE_EXPLICIT_STATE_PATH`; when true, the server must not allocate cwd ids from an implicit default state path.
- Set `MCP_CWD_REQUIRE_EXPLICIT_STATE_PATH=true` in Dockerfile/container packaging.
- The base container image must not allocate ids unless deployment config supplies a persistent `MCP_CWD_STATE_PATH`; without that path, `set_cwd` returns `cwd_state_unavailable`.
- For Docker Compose, add a named persistent writable state volume such as `/state` and set `MCP_CWD_STATE_PATH` inside that mount.
- For Smithery/container packaging, add a config/env route for a stable writable state path if the runtime supports it; if no stable writable path is available, document that `set_cwd` returns `cwd_state_unavailable`.
- Runtime docs must distinguish normal recreation with preserved state from explicit operator reset or unsupported ephemeral state where remembered `cwd_id` values must be discarded.
- Do not point cwd allocator state into the registered workspace unless explicitly configured by the operator; it is server-local allocator metadata, not project content.

## Documentation

`README.md`:

- Replace "11 tools" references with "12 tools".
- Add a short "Current working directory ids" section.
- Show:

```json
{ "directory": "D:/ai-apps/mcp-file-tools" }
```

and then:

```json
{ "target_file": "README.md", "cwd_id": 1 }
```

- Make clear that no-cwd mode still requires absolute inputs.
- Make clear that outputs use `/`.
- Add config docs for `MCP_CWD_STATE_PATH` and `MCP_CWD_TTL_SECONDS`.
- Add config docs for `MCP_CWD_REQUIRE_EXPLICIT_STATE_PATH`, including strict boolean parsing and fail-closed malformed values.
- Document the allocator state bundle: SQLite DB file, guard marker, lock file, and any SQLite sidecars (`-journal`, `-wal`, `-shm`) that must be handled together for reset/snapshot/restore.

`TOOLS.md`:

- Add full `set_cwd` entry.
- Update each tool input/output contract.
- Add cwd-aware output examples without leading `./`.
- Update line-count and batch-byte metric documentation.

## Acceptance

- Tool discovery exposes 12 tools.
- Schema discovery does not falsely reject relative paths when `cwd_id` is present; if conditional JSON Schema is not practical, relaxed string schemas are acceptable only with explicit descriptions.
- Schema discovery does not imply relative paths are accepted without `cwd_id`; this can be enforced by conditional schema or by tested descriptions plus runtime validation.
- Docs and manifest do not contain stale unconditional "relative paths are forbidden" wording.
- Docs and manifest do not show `D:\\...` examples except when explicitly explaining legacy escaped JSON input.

## Stop And Ask If

- The schema library cannot express conditional path validation and also cannot attach explicit, tested descriptions that make the runtime contract clear.
- Public docs must preserve exact old examples for compatibility reasons.
