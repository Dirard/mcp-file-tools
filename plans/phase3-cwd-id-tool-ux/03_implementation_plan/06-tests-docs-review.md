# Stage 6: Tests, Docs, And Review

## Goal

Make the implementation provable, not just plausible: tests must cover the dual path contract, nested no-leak surfaces, docs/schema consistency, generic outline fallback, line counts, and byte metrics.

## Test Files

Add focused tests:

- `filetoolsserver/handler/cwd_registry_test.go`
- `filetoolsserver/handler/cwd_tools_test.go`
- `filetoolsserver/handler/cwd_path_contract_test.go`

Update existing tests:

- `filetoolsserver/handler/handler_test.go`
- `filetoolsserver/handler/agent_tools_test.go`
- `filetoolsserver/handler/write_tools_test.go`
- `filetoolsserver/handler/batch_tools_test.go`
- `filetoolsserver/handler/middleware_test.go`
- `filetoolsserver/server_test.go`
- `internal/config/config_test.go`

## Required Test Matrix

`set_cwd`:

- requires existing absolute directory
- accepts slash-normalized Windows absolute input only in Windows-specific tests or path-map-backed fixtures; POSIX CI uses native absolute paths for acceptance
- rejects empty, relative, missing, and file paths
- rejects unknown input fields, especially accidental `cwd_id`
- success output returns only `cwd_id`
- error output allows structured `error`, `error_code`, and optional `action_hint`, and never includes `cwd`, `directory`, or a raw absolute path
- does not change process cwd
- does not mutate the registered workspace/target filesystem, while allowing server-local allocator state writes at `MCP_CWD_STATE_PATH`

Registry:

- allocates distinct immutable ids for different directories
- returns same active id for same directory if still live
- expires ids by TTL
- refreshes TTL on repeated `set_cwd`, but not on ordinary path-tool lookup
- invalid, unparsable, and non-positive `MCP_CWD_TTL_SECONDS` fall back to the default 7-day TTL
- expired lookup through a normal path tool returns `cwd_id_expired`, so the agent must call `set_cwd` again
- `cwd_id: null` and `cwd_id: 0` return `invalid_cwd_id`; only an absent field selects no-cwd mode
- huge JSON integers and values above `9007199254740991` return `invalid_cwd_id` without echoing the raw invalid value
- every cwd-aware input type, including the generic decoding route and `GrepToolInput.UnmarshalJSON`, preserves absent versus `null` versus malformed versus positive `cwd_id`
- `invalid_cwd_id` errors for `null`, malformed, string, fractional, zero, and negative values omit `cwd_id` and do not echo the raw invalid value; positive unknown/expired ids include `cwd_id`
- in-process expired ids keep tombstones so lookup returns `cwd_id_expired`; after process restart the same stale id is `cwd_id_unknown`
- one MCP client/session can call `set_cwd` and another independent client/session connected to the same live server can use that `cwd_id`; closing/reconnecting a client does not invalidate a live registry entry
- repeated `set_cwd` for the same canonical directory refreshes TTL and returns the same id without changing the original `cwd` display anchor
- once allocator state is unhealthy, every `set_cwd` returns `cwd_state_unavailable`, including same-canonical requests that would otherwise refresh TTL
- same canonical directory through another path-map alias, case variant, or symlink spelling does not mutate an existing id's `cwd` metadata
- does not reuse ids after handler/server restart when SQLite state remains
- stale post-restart id is unavailable and does not map to new cwd
- cold restart with the guard marker present but SQLite state missing/recreated returns `cwd_state_unavailable` and does not allocate a colliding id
- live-process stale high-water test rolls SQLite `last_issued` back under the same `state_uuid`; next `set_cwd` returns `cwd_state_unavailable`, no active or expired id is reused, and the allocator remains unhealthy for later registrations
- allocator boundary tests prove `last_issued = 9007199254740990` can issue `cwd_id = 9007199254740991`, while `last_issued = 9007199254740991` returns `cwd_state_unavailable` before allocation
- cold-start rollback/snapshot restore of SQLite state and guard marker to an older matching `state_uuid` is documented/tested as operator restore/reset: stale remembered `cwd_id` values must be discarded before cwd use resumes
- concurrent set/lookup does not duplicate or corrupt ids
- focused registry concurrency tests run under `go test -race` or an equivalent race check and cover concurrent same-canonical `set_cwd`, concurrent lookups, expiry cleanup, and SQLite allocation recheck

No-cwd mode:

- every path tool still rejects relative input
- every path output is slash-normalized absolute/display
- path-map aliases configured with `\` output with `/`

Cwd mode:

- every path tool accepts relative input
- every path tool rejects absolute input
- `.` maps to cwd and outputs `"."` where relevant
- no output path has leading `./`
- `../outside`, drive-relative paths, and cleaned outside-cwd paths are rejected
- cwd-mode rejects absolute-looking paths independently of current `GOOS`: POSIX `/...`, Windows drive absolute `C:/...` and `C:\...`, Windows rooted paths without drive such as `\Windows` and `\foo`, drive-relative `C:foo`, UNC paths, and extended UNC/device forms
- cwd-mode rejects existing symlink targets outside cwd for read/list/glob/grep/workspace/outline/inspect and rejects write/copy/move paths whose source or existing parent components resolve outside cwd
- write/copy/move symlink safety is not weakened: final source symlinks, final target symlinks, and symlink parent components that existing refactor safety rejects in no-cwd mode are also rejected in cwd mode, including when the symlink resolves inside cwd
- traversal tools do not enumerate outside-cwd contents through symlinked directories
- cwd-aware success output includes `cwd_id` and `cwd` except `set_cwd`
- `inspect_path` on a symlink whose target resolves outside cwd omits `symlink_target`, sets `symlink_target_outside_cwd: true`, and emits no absolute target path in generated text
- multi-path tools build one request-scoped cwd context: with a very short TTL or injected clock, expiry after context creation but before resolving later fields does not make one call mix cwd metadata or return partial `cwd_id_expired`; the next tool call after expiry returns `cwd_id_expired`

No absolute leaks:

- use field-aware assertions for cwd-aware outputs: check filesystem path fields and generated error/warning/recovery/action-hint text, not arbitrary content strings
- cover read/list/glob/grep/inspect/workspace/outline path fields and generated messages
- cover `workspace_inventory.root.read_error`, recursive `directories[].read_error`, and top-level `truncation_reason` by inducing those fields under `cwd_id`
- cover copy/move/batch `action_hint`, `partial_state`, `recommended_next_input`, `backup_paths`, top-level `backup_results`, `backup_results[].error`, `boundary_warnings[].target_file`, `boundary_warnings[].message`, `boundary_warnings[].recommended_action`, `warnings[].file`, `warnings[].message`, `batch_warnings[].file`, `target_results[].error`, `target_results[].backup_error`, `target_results[].warnings[].file`, `target_results[].boundary_warnings[].target_file`, `target_results[].boundary_warnings[].recommended_action`, `recovery_hint`, top-level `error`, partial-state nested errors, and warning messages
- prove `read_file.text`, `grep.matches[].text`, glob/grep pattern strings, and content-derived outline labels are not sanitized or rejected merely because they contain absolute-looking text
- cover `grep` JSON decoding directly: `cwd_id` survives `GrepToolInput.UnmarshalJSON` alongside existing `-A`, `-B`, `-C`, and `-i` aliases

Recommended input replay:

- every cwd-aware `recommended_next_input` or `next_recommended_call` for a path tool includes current `cwd_id`
- replay projection covers the recommended tool's exact path keys: `read_file.target_file`, `outline_file.target_file`, `list_dir.target_directory`, `glob_file_search.target_directory`, `grep.path`, `inspect_path.target_path`, `workspace_inventory.target_directory`, single copy/move `source_file` and `target_file`, and batch `source_file` plus `targets[].target_file`
- replay tests cover both top-level `action_hint.recommended_next_input` and `partial_state.recommended_next_input` for `copy_ranges_batch` and `move_ranges_batch`, plus `outline_file.next_recommended_call.recommended_next_input`
- replaying such a recommended input succeeds without `relative_path_requires_cwd`
- expired/unknown/unavailable cwd recovery hints on every existing path tool include `action_hint.safe_to_retry = false` and `recommended_next_tool: "set_cwd"`, but must not include `recommended_next_input.directory` or any embedded absolute path
- no stale-cwd recovery hint leaks the old cwd absolute path; the agent must use its own remembered cwd context when calling `set_cwd`

Schemas and manifest:

- server exposes 12 tools
- `set_cwd` schema has only `directory` input, success output with only `cwd_id`, and separate structured error output fields
- runtime validation rejects extra `set_cwd` input fields, including `cwd_id`
- existing path tool inputs expose optional `cwd_id`
- schema/runtime tests prove absent `cwd_id`, `cwd_id: null`, `cwd_id: 0`, malformed values, and positive ids produce the expected distinct runtime behavior
- output schemas expose optional `cwd_id` and `cwd`
- every existing path tool output schema exposes cwd-aware `error_code` and `action_hint`, including read/list/glob/grep/inspect/workspace/outline non-refactor tools
- output path schemas no longer have unconditional absolute-only regex/patterns; schema tests cover top-level and nested fields such as `file`, `path`, `files[]`, `targets_written[]`, `backup_paths[]`, and replay path maps
- schema descriptions no longer claim relative paths are forbidden everywhere

Outline fallback:

- generic text fallback returns sections
- generic outline items expose item-level honesty metadata such as `confidence`, `range_is_estimated`, and `metadata.parser_tier`
- `output_profile: "fingerprint_only"` bypasses exact and generic outlines for non-Go/non-MD text, Go, and Markdown
- generic chunking tests cover blank-line blocks, long continuous text split at 40 display lines or target 4096 bytes without splitting display lines, 80-code-point labels, `line_window`, `max_items`, truncation, and `next_recommended_call`
- generic chunking tests include a single display line longer than 4096 bytes; expected result is one exact one-line chunk with a capped label and no full body output
- generic fallback respects max items and line windows
- generic fallback does not fake imports or symbols
- binary/undecodable files stay safe

Line counts:

- `"" -> 0`
- `"a" -> 1`
- `"a\n" -> 2`
- `"a\r\n" -> 2`
- `"a\nb\n" -> 3`
- `read_file`, `inspect_path`, `outline_file`, and fingerprints agree

Batch bytes:

- copy batch reports target bytes, zero source rewrite bytes, total bytes, and legacy alias
- move batch reports target bytes, source rewrite bytes, total bytes, and legacy alias
- per-target metrics remain target-only
- applied metrics mirror dry-run metric shape

Docs:

- `README.md`, `TOOLS.md`, `server.json`, and server instructions say 12 tools
- docs explain `set_cwd`, TTL, state path, `MCP_CWD_REQUIRE_EXPLICIT_STATE_PATH`, no-cwd absolute mode, cwd relative mode, slash-normalized outputs
- docs say `set_cwd` may create/update server-local allocator state while not mutating the registered workspace/target filesystem
- examples use `D:/...` and relative paths without `./`
- docs explain batch metric aliases

Dependencies and vendoring:

- adding `modernc.org/sqlite` and `github.com/gofrs/flock` updates `go.mod`, `go.sum`, `vendor/`, and `vendor/modules.txt`
- `go test ./...` must run with the repository's vendored dependency state without inconsistent-vendoring errors

Runtime config and packaging:

- `MCP_CWD_STATE_PATH` rejects empty or relative values and accepts an absolute writable temp path in tests
- `MCP_CWD_REQUIRE_EXPLICIT_STATE_PATH=true` rejects missing/empty/relative `MCP_CWD_STATE_PATH` before any id allocation
- malformed `MCP_CWD_REQUIRE_EXPLICIT_STATE_PATH` values are fail-closed and make `set_cwd` return `cwd_state_unavailable` before default-path allocation
- tests isolate SQLite state file and guard marker per case
- tests/docs treat the allocator state bundle atomically: SQLite DB, guard marker, lock file, and SQLite sidecars with `-journal`, `-wal`, or `-shm` suffixes
- tests isolate and exercise the sibling state lock file through `github.com/gofrs/flock`; concurrent first-run/allocation across two registries using the same state path either serializes safely or returns `cwd_state_unavailable`
- stale lock-file tests prove file existence alone does not block allocation after the advisory lock is released, while a live lock holder causes bounded retry and then `cwd_state_unavailable`
- interrupted initialization tests cover DB-without-guard, guard-without-DB, and temp-guard-only states; no case may allocate an id until SQLite singleton row and canonical guard marker agree
- packaged/container runtime sets `MCP_CWD_REQUIRE_EXPLICIT_STATE_PATH=true` and does not allocate cwd ids from an implicit ephemeral default state path
- Docker Compose config exposes a named persistent writable state volume and sets `MCP_CWD_STATE_PATH` inside it
- Smithery/container config exposes a stable writable state path when supported, or docs clearly state `set_cwd` is unavailable with `cwd_state_unavailable`
- runtime recreation test allocates an id before recreation, restarts with the configured persistent state surface, and proves the next id advances from stored state rather than restarting at `1`
- packaged/container config without a persistent state surface returns `cwd_state_unavailable` before first allocation
- if both SQLite state and guard marker are absent after supposed packaged-runtime recreation, docs/tests classify it as explicit operator reset or unsupported ephemeral state; stale remembered ids must be discarded before cwd use resumes

## Helper Tests

Recommended helpers:

- `cwdTestEnv`: temp workspace, isolated temp `MCP_CWD_STATE_PATH` or injected state path, sibling guard marker path, registered cwd, id, absolute root, and fixtures
- tests must not depend on the first allocated id being literal `1`, except allocator-specific fixtures with an isolated database and explicit high-water setup
- `assertSlashNormalizedAbsolutePath`: absolute/display path with `/` and no `\`
- `assertCwdRelativePath`: relative path with `/`, no `./`, and `"."` only for cwd
- `assertNoGeneratedAbsoluteLeak`: field-aware scan that checks filesystem path fields and generated diagnostic/recovery/hint strings while explicitly excluding content/query fields
- `assertContentNotSanitized`: verifies read/grep/outline content-derived fields preserve absolute-looking text from user/file content
- `assertRecommendedInputReplayable`: verifies cwd-aware recommended inputs include `cwd_id` and can be replayed
- `assertStaleCwdHintHasNoDirectory`: verifies expired/unknown cwd hints name `set_cwd` without embedding absolute `directory`
- `assertOutsideCwdSymlinkShape`: verifies `symlink_target` omission plus `symlink_target_outside_cwd: true`
- `callToolAndDecode`: reuse existing structured-output helpers where possible

## Verification Commands

Implementation verification should run:

- `go test ./...`
- dependency update verification: after adding SQLite and file-lock dependencies, run the repository-appropriate vendoring step such as `go mod vendor`, then verify `vendor/modules.txt` is consistent
- focused reruns for failing packages
- optional schema/doc focused tests if added as separate test names

Do not run network-dependent dependency downloads without normal sandbox/approval handling.

## Plan Review Cycle

Before implementation:

1. Send this plan bundle to `product_owner` for product/intent review.
2. Send this plan bundle to `reviewer` for engineering readiness review.
3. Repair findings in the plan files.
4. Run fresh review where `codex-flow-v2` requires it.
5. Stop and wait for user OK before implementation.

## Implementation Review Cycle

After implementation:

1. Run focused and full tests.
2. Send implementation result to `product_owner` for concept adherence.
3. Send implementation result to `reviewer` for engineering quality.
4. Repair accepted findings.
5. Run fresh review after substantive repair.
6. Return final result only after review is clean or clearly blocked.

## Acceptance

- Tests make every explicit concept requirement observable.
- Docs and schemas match runtime behavior.
- Review reports are clean, or findings are repaired and rechecked.
- Implementation does not start until the user approves the clean plan.

## Stop And Ask If

- Tests reveal that accepted concept requirements conflict with each other.
- A needed review role cannot be launched due to runtime constraints.
- Dependency or sandbox restrictions prevent verifying the planned implementation.
- SQLite or file-lock dependency download or vendoring cannot be completed in the target environment.
