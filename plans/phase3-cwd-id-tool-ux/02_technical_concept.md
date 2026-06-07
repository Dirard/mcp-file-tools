# Phase 3 CWD ID And Tool UX Technical Concept

concept_version_label: phase3-cwd-id-tool-ux-v1
status: accepted technical concept source for SRS planning
acceptance_record: concept review passed; current active gate is clean SRS plan review plus explicit user OK before implementation

## Technical Direction

Phase 3 adds one new tool and updates the path contract of existing tools:

- `set_cwd` registers an absolute directory and returns a small `cwd_id`.
- Existing path tools accept optional `cwd_id`.
- Without `cwd_id`, existing absolute-only input behavior remains.
- With `cwd_id`, relative path inputs are resolved through a server-wide immutable cwd handle.

This is not a process cwd. Implementation must never call `os.Chdir`.

The phase also fixes three UX issues:

- `outline_file` should provide useful structure for ordinary text files beyond Go and Markdown.
- `inspect_path.line_count` must match the line-count model used by `read_file`, `outline_file`, and fingerprints.
- Batch range outputs must expose clear byte metrics.

## Existing Contract To Preserve

The current public surface intentionally rejects relative path inputs. This is documented in `README.md`, `TOOLS.md`, server instructions, and tool descriptions.

Phase 3 changes that contract only when the caller passes a valid `cwd_id`.

Important compatibility rule:

```text
no cwd_id -> absolute path inputs and slash-normalized absolute path outputs
cwd_id    -> relative path inputs and relative path outputs anchored by cwd
```

No hidden default cwd is introduced.

## `set_cwd`

### Purpose

Register one absolute directory as a short explicit path context for later calls.

### Input

```json
{
  "directory": "D:/ai-apps/mcp-file-tools"
}
```

Rules:

- `directory` is required;
- `directory` must be non-empty after trim;
- `directory` must be an absolute path in the filesystem visible to the file-tools server;
- in the current Windows scenario, the preferred display/input form is `D:/ai-apps/mcp-file-tools`, without JSON-escaped backslashes;
- path maps may apply using the same input path-map rules as other tools;
- resolved path must exist;
- resolved path must be a directory;
- tool does not mutate the registered workspace/target filesystem, but may create or update server-local allocator state at `MCP_CWD_STATE_PATH`;
- tool does not change process cwd.

### Output

```json
{
  "cwd_id": 1
}
```

`cwd_id` is a small integer from the server-wide cwd registry. It is not scoped by MCP session, chat, subagent or client-provided attributes.

The `set_cwd` response stays intentionally small. TTL and expiry details are internal behavior unless the user later asks to expose them.

## CWD Store

### Identity

The store key is `cwd_id` only.

There must be no hidden `cwd_scope_key`, `ServerSession.ID()`, chat id, subagent id or client-supplied scope in the public or internal lookup key. A valid `cwd_id` means the same thing after MCP session reconnect and in every tool call until it expires or becomes unavailable.

Conflict avoidance comes from allocation, not session isolation:

- allocate ids atomically from one server-wide registry;
- never reuse an active id for a different directory;
- never overwrite an existing id's directory;
- allow many ids to exist at once;
- if an old id cannot be resolved, return an explicit unavailable/expired error instead of resolving it to a different cwd.

`cwd_id` is not a security token. It is only a compact reference to an absolute path. The existing file-tool permission model and path validation remain the authority.

### Allocation Anti-Reuse

`cwd_id` allocation must be restart-safe against stale-context reuse.

The accepted simple direction is a persisted allocator high-water mark. SQLite is a good local storage option for this metadata because allocation must be atomic and restart-safe:

- persist the next/highest issued integer id in server-local state;
- do not persist active cwd path entries in this phase;
- on startup, continue allocation above the persisted high-water mark;
- if allocator state cannot be read or safely advanced, fail `set_cwd` with an explicit server error instead of issuing an id that may collide with stale context;
- do not reset the counter to `1` after process restart.

This keeps `cwd_id` as a small integer handle, while preventing an old `cwd_id` remembered by an agent from resolving to a different directory after restart.

SQLite may store either:

- allocator-only state, such as `next_cwd_id` or `last_issued_cwd_id`;
- an issued-id ledger that marks old ids as unavailable after restart;
- later, if the product scope changes, durable active cwd entries with TTL.

For this concept, durable active cwd entries are not required. The must-have is that a stale remembered integer never becomes a handle for a different cwd.

SRS may choose another no-wrong-directory allocation scheme, but it must preserve these properties:

- key lookup remains `cwd_id` only;
- no session/chat/subagent scope is introduced;
- active cwd path entries are not durable workspace state;
- stale pre-restart ids become unavailable/expired, not remapped to new cwd paths.

### Entry

Each stored entry contains:

```text
cwd_id
resolved_cwd
display_cwd
created_at
expires_at
last_used_at
```

Entries are immutable. A `cwd_id` never changes directory during its lifetime.

### TTL

Default target TTL: seven days.

Expired entries are invalid for path resolution. The server may lazily clean expired entries on lookup or insert.

Optional tombstone behavior:

- keep an expired entry's `cwd` for a short grace period only to produce a better error;
- do not allow operations through expired entries;
- do not rely on tombstones for correctness.

### Reconnect

MCP session reconnect or chat recreation must not invalidate a live `cwd_id` by itself. The cwd registry lifetime is based on TTL, not on session lifetime.

If the underlying registry is unavailable, expired or lost, tools must return an actionable error that tells the agent to call `set_cwd` again with the intended directory.

An old `cwd_id` must never resolve to a different directory after reconnect or process restart. Durable cwd entries across process restart are out of scope for this phase. Planning must guarantee that stale ids become unavailable instead of being reused incorrectly within the TTL window.

## Path Resolution With `cwd_id`

Path resolution should be centralized so handlers do not duplicate special cases.

Target internal API shape:

```text
ResolvePathWithCwd(ctx, cwd_id, path, field_name) -> internal resolved path + relative display path + cwd metadata
```

Rules:

- If `cwd_id` is absent, preserve the existing absolute input contract; path outputs remain absolute but must be slash-normalized for display.
- If `cwd_id` is present, lookup the server-wide cwd entry first.
- If lookup fails because unknown/expired, return `cwd_id_unknown` or `cwd_id_expired`.
- If path is relative, join it to `resolved_cwd`, clean it, validate the result, and produce relative output paths anchored by `cwd`.
- If path is absolute while `cwd_id` is present, reject it with a conditional schema/runtime error. In cwd-aware mode, path inputs should be relative to `cwd`.
- Empty path is always invalid.
- Drive-relative Windows paths like `C:foo` remain invalid.
- Lexical escape above cwd through `..` is rejected for relative paths.
- If caller needs a path outside cwd, they can omit `cwd_id` and use old absolute mode, or call `set_cwd` for a higher/different directory.

The cwd handle is ergonomic context, not a sandbox or authorization boundary.

All existing early absolute-path checks must be replaced or made conditional:

```text
no cwd_id + relative path -> reject with the existing absolute-path style error
cwd_id + relative path    -> resolve through cwd before absolute validation
```

No handler should reject a relative path before cwd resolution when a valid `cwd_id` is present.

## Output CWD Metadata

Every successful output for a call that used `cwd_id` must include:

```json
{
  "cwd_id": 1,
  "cwd": "D:/ai-apps/mcp-file-tools"
}
```

This echo is mandatory for every cwd-aware successful output. It is not an open planning question. It lets the agent recover after reconnect by calling `set_cwd` again with the absolute `cwd`.

`cwd` is the absolute anchor. It should be slash-normalized in outputs, including on Windows:

```json
{
  "cwd_id": 1,
  "cwd": "D:/ai-apps/mcp-file-tools"
}
```

All other user-facing path fields in a successful cwd-aware output must be relative to `cwd`:

- `file`;
- `path`;
- `resolved_path`;
- `directory`;
- `target_directory`;
- `source_file`;
- `target_file`;
- `backup_path`;
- `backup_paths`;
- `files_maybe_modified`;
- `targets_written`;
- `symlink_target` when it is representable under `cwd`;
- `target_file` inside boundary warnings;
- paths inside `files`, `matches`, `target_results`, warnings, backups, partial states, recovery hints, `recommended_next_input` maps and action hints.

This list is intentionally not exhaustive. SRS must enumerate every filesystem path-bearing output field per tool. Non-filesystem structural fields, for example Markdown/outline item `path` arrays, are not filesystem path outputs and should not be rewritten as cwd-relative paths.

Relative output paths use forward slashes and a compact cwd-relative form without a leading `./`:

```json
{
  "cwd_id": 1,
  "cwd": "D:/ai-apps/mcp-file-tools",
  "file": "internal/test.go"
}
```

If an output path needs to refer to `cwd` itself, use `"."`.

`cwd` prevents ambiguity: it is the absolute anchor for every relative path in the same output.

If a core resolved path cannot be represented under `cwd`, a cwd-aware call should return a structured `path_outside_cwd` style error instead of emitting an absolute path field. This preserves the rule: with `cwd_id`, path outputs are relative; without `cwd_id`, path outputs are absolute.

For metadata-only path fields that may naturally point outside the requested path, such as `inspect_path.symlink_target`, do not leak an absolute path. If the value is representable under `cwd`, return it as cwd-relative. If it is outside `cwd`, either omit the path field and include an explicit boolean/reason such as `symlink_target_outside_cwd: true`, or return a structured tool-specific outside-cwd error. SRS must choose the exact shape, but absolute path leakage is not acceptable.

For search/list tools that currently format paths relative to the requested root, cwd-aware calls must use `cwd` as the display base. They must not feed the raw relative input path back into display formatting in a way that returns ambiguous or non-cwd-relative output paths.

Path-map display aliases are part of the same display contract. Mapped paths must also be slash-normalized in outputs; a path-map source containing backslashes must not cause `D:\...` style output.

## Error Contract

Unknown or expired cwd errors are structured tool errors.

Example:

```json
{
  "error": "cwd_id 1 is expired or unavailable; call set_cwd again with the intended directory",
  "error_code": "cwd_id_expired",
  "cwd_id": 1,
  "action_hint": {
    "safe_to_retry": false,
    "recommended_next_tool": "set_cwd",
    "recommended_next_input_policy": "call set_cwd with the absolute directory remembered by the agent"
  }
}
```

Do not silently retry, guess cwd, or fallback to interpreting the relative path from the server process directory.

All cwd-aware path tool output schemas must allow the same structured error metadata. Existing generic `error` output is not enough for this phase.

Required cwd-aware error fields:

```json
{
  "error": "cwd_id 1 is expired or unavailable; call set_cwd again with the intended directory",
  "error_code": "cwd_id_expired",
  "cwd_id": 1,
  "action_hint": {
    "safe_to_retry": false,
    "recommended_next_tool": "set_cwd"
  }
}
```

The implementation may use a shared error envelope or extend each affected output schema, but every cwd-aware path tool must expose these fields consistently for unknown/expired cwd failures.

## Schema And Tool Descriptions

Current schema/path constraints assume every path field must be absolute. Phase 3 needs schema updates.

The input schema should allow:

- absolute paths for old calls;
- non-empty relative paths when `cwd_id` is present;
- reject absolute path inputs when `cwd_id` is present, so cwd-aware mode has one coordinate system.

Output schemas should distinguish:

- no `cwd_id`: slash-normalized absolute path outputs;
- `cwd_id`: absolute `cwd` plus cwd-relative path fields.

Runtime validation remains the source of truth because JSON Schema cannot easily express every OS-specific path rule.

Schema work is part of the concept, not optional cleanup:

- add `cwd_id` to every cwd-aware input schema;
- add `cwd_id` and `cwd` to every cwd-aware output schema;
- add `error_code`, `cwd_id` and `action_hint` to every cwd-aware error output schema, or introduce an equivalent shared error envelope;
- add `directory` as a path input field for `set_cwd`;
- keep `set_cwd` output schema intentionally small: `cwd_id` only;
- add `cwd` as an absolute slash-normalized path output field for schema constraints;
- mark cwd-aware path output fields as cwd-relative display paths, not absolute paths;
- update all manual output schemas with `AdditionalProperties=false`, including `outline_file` and `workspace_inventory`;
- update nested batch/partial-state schemas where cwd-aware outputs include path metadata;
- keep runtime validation as the final authority for conditional path rules.

Tool descriptions must be updated together with schemas. Any description that says "relative paths are forbidden" should become conditional:

```text
Without cwd_id, path inputs are absolute and path outputs are slash-normalized absolute paths. With cwd_id, path inputs are resolved from cwd and path outputs are relative to cwd.
```

Server instructions, `TOOLS.md`, and the MCP package manifest (`server.json`) must mention there are now 12 tools and must not keep stale absolute-only descriptions.

## Tool Coverage

The optional `cwd_id` should be added consistently to all public tools that accept path inputs:

- `read_file`;
- `outline_file`;
- `copy_ranges`;
- `move_ranges`;
- `copy_ranges_batch`;
- `move_ranges_batch`;
- `list_dir`;
- `glob_file_search`;
- `grep`;
- `inspect_path`;
- `workspace_inventory`.

`set_cwd` accepts `directory` and returns `cwd_id`.

For batch tools, one top-level `cwd_id` applies to:

- `source_file`;
- every `target_file`;
- path values in recommended next inputs, warnings, backup metadata, partial states and recovery hints follow the same output path contract: no `cwd_id` means slash-normalized absolute paths; `cwd_id` means cwd-relative paths.

No per-target cwd override in this phase.

## Write Tool Safety

Write tools must receive internal resolved paths before entering existing refactor/write logic.

All existing safety rules remain:

- source fingerprint precondition;
- target precondition;
- dry-run;
- path locks;
- symlink rejection;
- binary/encoding rejection;
- write thresholds;
- sidecar backup behavior;
- partial-state recovery;
- final source and target rechecks.

`cwd_id` only shortens how paths are supplied by the agent. It must not change write authorization or stale-write safety.

## `outline_file` For More Text Files

### Problem

Current `outline_file` exact structure is limited to:

- Markdown ATX headings;
- Go AST.

All other text files return fingerprint and an unsupported warning. This is not useful enough for agents working across TypeScript, JavaScript, Python, JSON, YAML, TOML, docs, config and other text files.

### Concept Direction

Introduce parser tiers:

```text
exact parser      -> real language/format structure, exact semantic meaning within parser_scope
generic text      -> synthetic or heuristic line ranges for ordinary text files
fingerprint only  -> only when structure cannot be produced safely
```

Go and Markdown remain exact.

For other non-binary text files, `outline_file` should return a generic text outline rather than only fingerprint.

### Generic Text Outline Contract

Generic text outline is useful but honest.

It may return items such as:

- `text_chunk`;
- `blank_line_delimited_block`;
- `heading_like_line`;
- `json_top_level_member` if implemented with a structured parser;
- `yaml_top_level_key` if implemented with a structured parser.

Every generic item must clearly indicate:

```json
{
  "kind": "text_chunk",
  "confidence": "synthetic",
  "range_is_estimated": false,
  "metadata": {
    "parser_tier": "generic_text"
  }
}
```

`range_is_estimated=false` is allowed only when the line span itself is exact. The semantic meaning can still be synthetic or best-effort via `confidence`.

Generic text output must not claim:

- exact AST;
- exact imports;
- exact function/class/method semantics;
- language-specific correctness unless implemented by a real parser.

### Parser Status Values

Planning should settle exact names, but conceptually statuses should distinguish:

- `exact`;
- `generic_text`;
- `fingerprint_only`;
- `unsupported`;
- `outline_parse_threshold_exceeded`.

`parser_scope` should explain what was parsed, for example:

```text
go_ast
markdown_atx_headings
generic_line_chunks
generic_blank_line_blocks
```

Compatibility rule: existing successful exact parsers may keep `parser_status: "ok"`. Phase 3 should add parser tier/scope/confidence fields or additive statuses without breaking old clients that already interpret `ok`, `unsupported`, `fingerprint_only`, and `outline_parse_threshold_exceeded`.

Planning may choose whether the generic text fallback reports `parser_status: "generic_text"` or keeps `parser_status: "ok"` with `parser_scope: "generic_line_chunks"` and item-level `confidence`. It must not rename existing statuses casually.

### Generic Text Limits

Generic text outline must stay compact:

- respect `max_items`;
- respect `line_window`;
- no block bodies;
- no full text dump;
- include `next_recommended_call` when truncated;
- use full-file fingerprint as range snapshot.

This makes `outline_file` useful as a range-selection helper even when no exact language parser exists.

## Consistent Line Count

### Required Model

All tools must use the same display-line model:

```text
empty file -> 0
"a"        -> 1
"a\n"      -> 2
"a\r\n"    -> 2
```

Reason: `read_file` exposes the final empty line as addressable display output, and fingerprints already use that model.

### Technical Direction

Unify line-count helpers so `inspect_path` does not have a separate EOF-counting model.

Preferred direction:

- share the same decoded line-count function used by fingerprint/read-file display logic;
- preserve binary detection behavior;
- support BOM/decoded text paths consistently;
- update tests for trailing newline and CRLF.

## Batch Byte Metrics

### Problem

`target_results[].would_write_bytes` is understandable per target.

Top-level `would_write_bytes` in batch outputs can be ambiguous, especially for `move_ranges_batch`, because target writes and source rewrite are different effects.

### Concept Direction

Keep backwards compatibility, but add clearer fields.

Recommended conceptual fields:

```json
{
  "would_write_target_bytes": 8192,
  "would_rewrite_source_bytes": 12000,
  "would_write_total_bytes": 20192,
  "would_write_bytes": 20192
}
```

For `copy_ranges_batch`:

- `would_write_target_bytes` is sum of planned target bytes;
- `would_rewrite_source_bytes` is 0;
- `would_write_total_bytes` equals target bytes.

For `move_ranges_batch`:

- `would_write_target_bytes` is sum of planned target bytes;
- `would_rewrite_source_bytes` is planned source-after file size;
- `would_write_total_bytes` is target bytes plus source rewrite bytes.

Planning may choose exact names, but the output must make clear what is a target payload, what is a source rewrite, and what is used for configured batch limit checks.

## Documentation Updates

Documentation must be updated in the same phase:

- `README.md` tool list and path contract;
- `TOOLS.md` global path section;
- every tool section with path inputs;
- server instructions in `filetoolsserver/server.go`;
- MCP package manifest metadata in `server.json`;
- generated/declared JSON schemas if applicable;
- tests that assert relative paths are always rejected.

Docs must avoid saying "relative paths are forbidden" without the `cwd_id` condition.

Manifest/tool registry metadata must also avoid stale wording, because agents may discover tools through package metadata before reading docs.

## Verification Expectations

Concept implementation should later be checked against:

- old absolute path calls still pass;
- old absolute path calls return slash-normalized absolute path outputs, for example `D:/ai-apps/mcp-file-tools/README.md`;
- relative path without `cwd_id` is rejected;
- `set_cwd` rejects relative, missing, file and empty directories;
- `set_cwd` returns small integer `cwd_id`;
- tools resolve relative paths through valid `cwd_id`;
- absolute path inputs with `cwd_id` are rejected;
- outputs include slash-normalized absolute `cwd` when `cwd_id` was used;
- path fields in successful cwd-aware outputs are relative to `cwd`, for example `internal/test.go`;
- `inspect_path.resolved_path` follows the same relative output rule under `cwd_id`;
- `inspect_path.symlink_target` is relative if under `cwd`, otherwise omitted or reported with structured outside-cwd metadata, never absolute;
- cwd-aware output path fields do not add a leading `./` except `"."` for cwd itself;
- cwd-aware output path fields use `/` separators even on Windows;
- path-map alias outputs are slash-normalized even if the configured source path uses backslashes;
- unknown/expired `cwd_id` returns structured actionable error;
- two chats/subagents that call `set_cwd` for different directories receive distinct active ids;
- two cwd handles do not overwrite each other;
- an old `cwd_id` never resolves to a different directory after reconnect/restart;
- relative `..` escape is rejected;
- paths outside cwd in cwd-aware mode return a structured error instead of absolute path fields;
- batch/recovery/warning/recommended-next-input path fields follow the same slash-normalized or cwd-relative output rules;
- error messages, reasons and recovery hints do not leak backslash paths or absolute paths in cwd-aware mode;
- write tools keep existing fingerprint/dry-run/lock/symlink behavior;
- `outline_file` returns generic text outline for ordinary text files;
- generic outline does not claim exact AST;
- unsupported/binary files do not fake structure;
- `inspect_path.line_count` matches read/fingerprint model for empty file, no trailing newline, LF and CRLF trailing newline;
- batch byte metrics are clear and documented;
- `server.json`/manifest metadata exposes the new 12-tool surface and conditional path contract;
- old clients that ignore new fields are not broken.

## Open Questions For Planning

None blocking for concept.

Planning must still decide:

- exact TTL config name and default exposure;
- exact field names for batch byte metrics;
- exact generic text chunking strategy;
- exact schema mechanics for conditional `cwd_id` path behavior.

## Acceptance Path

This technical concept is not an implementation plan. After clean concept review and user approval, the next artifact should be a detailed SRS-style plan bundle.
