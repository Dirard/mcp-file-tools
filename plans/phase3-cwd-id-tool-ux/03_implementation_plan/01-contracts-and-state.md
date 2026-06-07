# Stage 1: Contracts And CWD State

## Goal

Introduce `set_cwd` and a server-wide cwd registry that gives agents short integer ids without changing process cwd, creating session state, or reusing stale ids for different directories.

## Files To Add Or Change

Likely additions:

- `filetoolsserver/handler/cwd_registry.go`
- `filetoolsserver/handler/cwd_registry_test.go`
- `filetoolsserver/handler/cwd_tools_test.go`

Likely changes:

- `filetoolsserver/handler/handler.go`
- `filetoolsserver/handler/tool_types.go`
- `filetoolsserver/handler/middleware.go`
- `internal/config/config.go`
- `internal/config/config_test.go`
- `filetoolsserver/server.go`
- `filetoolsserver/server_test.go`
- `go.mod` / `go.sum`
- `vendor/`
- `vendor/modules.txt`

## Public Tool Contract

`set_cwd` input:

```json
{
  "directory": "D:/ai-apps/mcp-file-tools"
}
```

`set_cwd` success output:

```json
{
  "cwd_id": 1
}
```

No `cwd`, `directory`, `path`, `message`, `ttl`, `expires_at`, or session metadata appears in successful `set_cwd` output.

`set_cwd` error output is a separate structured error payload, not a success payload:

```json
{
  "error": "cwd state is unavailable",
  "error_code": "cwd_state_unavailable"
}
```

Rules:

- Success output has exactly one field: `cwd_id`.
- Error output may include `error`, `error_code`, and optional `action_hint`.
- `set_cwd` error output never includes `cwd`, `directory`, or a raw absolute path.
- Output schema must allow this success/error split without suggesting successful `set_cwd` responses contain error metadata.

`directory` rules:

- required, non-empty string after trim
- absolute path in the file-tools server filesystem namespace
- slash-normalized Windows input such as `D:/repo` is valid
- existing path-map display aliases remain valid if they already resolve as absolute server-visible paths
- resolved target must exist and be a directory
- no `cwd_id` parameter is accepted by `set_cwd`
- unknown input fields are rejected at runtime; `{ "directory": "...", "cwd_id": 1 }` and any other extra-field shape must fail instead of being silently ignored

## Existing Tool Input Contract

Every existing path tool gets an optional `cwd_id` integer input:

- When absent: path inputs are absolute-only.
- When present: path inputs are relative-only and resolved under the cwd registered for that id.

The implementation must use a shared presence-aware input representation for `cwd_id`; a plain `*int` is not sufficient because Go JSON decoding cannot distinguish an absent field from `null` through `*int` alone.

Recommended shape, or a named equivalent:

```go
type CwdIDInput struct {
    Present bool
    Null    bool
    Value   int64
}
```

Decode it through custom JSON logic or `json.RawMessage` before path resolution:

- absent field: `Present=false`, no-cwd absolute mode
- `cwd_id: null`: `Present=true`, `Null=true`, return `invalid_cwd_id`
- malformed, non-integer, fractional, string, non-positive, or out-of-range values: return `invalid_cwd_id`
- positive integer within the public `cwd_id` numeric domain: cwd mode

Every cwd-aware input type, including inputs decoded through generic server registration and `GrepToolInput.UnmarshalJSON`, must preserve this absent/null/value distinction.

## Existing Tool Output Contract

Every existing path tool output gets optional metadata fields:

```json
{
  "cwd_id": 1,
  "cwd": "D:/ai-apps/mcp-file-tools"
}
```

Rules:

- Include `cwd_id` and `cwd` on successful cwd-aware responses.
- Include `cwd_id` on cwd-aware structured errors only when the request supplied a parseable positive integer id. This includes positive ids that are unknown, expired, or unavailable.
- For `cwd_id: null`, malformed JSON values, strings, fractional numbers, zero, and negative numbers, return `invalid_cwd_id` without a `cwd_id` field and without echoing the raw invalid value.
- Include `cwd` on cwd-aware errors only if the id was valid and resolved before the error.
- Do not include `cwd_id` or `cwd` in no-cwd responses.
- Do not add any metadata to `set_cwd` success output beyond `cwd_id`.

## Registry Semantics

The registry is owned by `Handler`, not by MCP session, chat, thread, or subagent.

Active entry:

```go
type CwdEntry struct {
    ID               int64
    CanonicalAbsPath string
    Display          string
    ExpiresAt        time.Time
}
```

Synchronization model:

```go
type CwdRegistry struct {
    mu              sync.RWMutex
    byID            map[int64]*CwdEntry
    byCanonical     map[string]int64
    expiredIDs      map[int64]time.Time
    maxSeenIssuedID int64
}
```

- All access to `byID`, `byCanonical`, `expiredIDs`, and `maxSeenIssuedID` is protected by `mu`.
- Lookup uses `RLock` for live entries. If the entry is expired, it upgrades to `Lock`, removes the canonical index for that id, records `expiredIDs[id] = now`, and returns `cwd_id_expired`.
- `set_cwd` takes `Lock` for allocator-health check, canonical lookup, TTL refresh, SQLite allocation, and map insertion. Holding the registry lock during allocation is acceptable because allocation is local and prevents duplicate ids for the same canonical cwd.
- Allocator unhealthy state has precedence over canonical reuse: once the allocator is marked unhealthy, every future `set_cwd` returns `cwd_state_unavailable`, including requests for an already-live canonical cwd that would otherwise only refresh TTL.
- Before SQLite allocation commits, `set_cwd` verifies the candidate id is greater than `maxSeenIssuedID` and absent from both `byID` and `expiredIDs`. If this guard fails, roll back allocation, mark the allocator unhealthy, and return `cwd_state_unavailable`.
- After SQLite allocation succeeds, `set_cwd` rechecks `byCanonical` while still holding `Lock` before inserting, so concurrent registrations of the same canonical cwd cannot create two active ids.
- After inserting a new id, update `maxSeenIssuedID` to that id. Reusing an existing live canonical entry refreshes TTL but does not lower or reset `maxSeenIssuedID`.
- Expiry cleanup may remove stale `byCanonical` / `byID` entries only while holding `Lock`; it must keep `expiredIDs` tombstones for the rest of the process so in-process lookups can distinguish expired ids from never-active ids.
- `expiredIDs` is memory-only and is cleared on process restart; after restart, old ids are `cwd_id_unknown` because active entries are not persisted.

Behavior:

- IDs are positive integers.
- IDs are immutable: once an id has pointed to one absolute cwd, it is never reassigned to another cwd while that id may be remembered by an agent.
- `CanonicalAbsPath` is the canonical equality key for id reuse. It is resolved from the accepted absolute directory after cleaning and symlink/case normalization available on the host OS, before path-map display projection.
- `Display` is the slash-normalized `cwd` output anchor captured when the id is first created.
- Same live `CanonicalAbsPath` returns the same active id from `set_cwd`, refreshes TTL from that `set_cwd` call, and keeps the original `Display` immutable even if the later call used a different path-map alias, case variant, or symlink spelling, unless the allocator has already been marked unhealthy.
- Different live directories receive different ids.
- Lookup by id is the only lookup used by other tools.
- `maxSeenIssuedID` is an in-process collision guard. A newly allocated id must be strictly greater than every id this process has ever allocated, returned, kept active, or tombstoned.
- Normal path-tool lookup does not refresh TTL; if the id has expired, the tool returns `cwd_id_expired` and the agent must call `set_cwd` again.
- Expired active entries are removed lazily during set and lookup operations, but their ids remain in the in-process `expiredIDs` tombstone map.
- Unknown or expired ids return structured errors; they do not fall back to absolute mode.

## SQLite Allocator

Use SQLite for the high-water mark allocator only. Active cwd path entries stay memory-resident. This does not mutate the registered workspace/target filesystem, but it may create or update server-local allocator state at `MCP_CWD_STATE_PATH`.

Recommended dependencies:

- `modernc.org/sqlite` through `database/sql`, to avoid CGO.
- `github.com/gofrs/flock` for cross-platform advisory locking of the allocator state path.
- Because the repository uses vendoring, adding these dependencies must update `go.mod`, `go.sum`, `vendor/`, and `vendor/modules.txt`.

State table:

```sql
CREATE TABLE IF NOT EXISTS cwd_allocator (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  state_uuid TEXT NOT NULL,
  last_issued INTEGER NOT NULL CHECK (last_issued >= 0 AND last_issued <= 9007199254740991),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
```

`cwd_id` numeric domain:

- Public `cwd_id` values are positive integers in `[1, 9007199254740991]` (`2^53 - 1`) so they remain exact for JSON/tool clients and fit comfortably in SQLite signed integer storage.
- The schema for every `cwd_id` property uses integer type, minimum `1`, and maximum `9007199254740991`.
- JSON input numbers outside this range, huge numeric literals, fractional values, strings, `null`, zero, and negatives return `invalid_cwd_id` without echoing the raw invalid value.
- Allocator logic checks `last_issued < 9007199254740991` before incrementing. If the limit is reached, `set_cwd` returns `cwd_state_unavailable` before any id is returned or committed.

Allocation:

1. Resolve the state file, sibling guard marker, and sibling lock file paths.
2. Acquire an exclusive process lock for the state path with `github.com/gofrs/flock` through a sibling `cwd-state.lock` file. The lock covers first-run initialization, guard validation, SQLite state validation, and every allocation.
3. Use a bounded wait/retry for the lock. If the lock cannot be acquired within the configured or hard-coded short timeout, `set_cwd` returns `cwd_state_unavailable`.
4. Open the state database only while the lock is held.
5. Start a SQLite transaction that serializes database writers.
6. Insert the singleton row with `state_uuid = <new random uuid>` and `last_issued = 0` only during guarded first-run initialization.
7. Verify the singleton `state_uuid` still matches the guard marker.
8. Read current `last_issued`; if it is already `>= 9007199254740991`, roll back and return `cwd_state_unavailable`.
9. Compute candidate id, and verify candidate is greater than the registry `maxSeenIssuedID` and absent from active/expired id maps.
10. If the candidate collides with in-process state or is not greater than `maxSeenIssuedID`, roll back, mark allocator unhealthy for the process, and return `cwd_state_unavailable`.
11. Increment `last_issued`.
12. Commit and return the new id.

State reset guard and lock protocol:

- Create a sibling guard marker for the allocator state, for example `cwd-state.guard`, containing the allocator `state_uuid`.
- Create or use a sibling lock file, for example `cwd-state.lock`, for the exclusive state-path lock.
- The guard marker and lock are server-local allocator metadata. They are not stored in, and never mutate, the registered workspace/target filesystem.
- Use SQLite rollback journal mode for the allocator database unless implementation proves WAL sidecars are intentionally part of the state bundle. The allocator state bundle is the SQLite DB file, guard marker, lock file, and any SQLite sidecar files matching the DB path with `-journal`, `-wal`, or `-shm` suffixes.
- Operator reset, test cleanup, snapshot, and restore docs must treat the whole allocator state bundle atomically; deleting or restoring only the DB and guard marker while leaving sidecars behind is unsupported.
- The lock file itself may remain on disk after a crash; file existence is not treated as a stale lock. Lock ownership is determined only by `flock` acquisition. A crashed process releases the advisory lock; a live holder may temporarily block a bounded retry.
- First-run initialization is allowed only while holding the lock, only when both the SQLite state file and guard marker are absent, and only in an allowed initialization context:
  - direct local runs using the default user-config state path may initialize on first use
  - explicit state-path runtimes may initialize only when their runtime config is explicitly documented/provisioned as persistent, such as Docker Compose with a named state volume
  - packaged/container runtimes that cannot prove a persistent state path through their config surface must return `cwd_state_unavailable` before allocation instead of treating an empty directory as first run
- During first-run initialization, create the SQLite singleton row inside the transaction, commit it, then write the guard marker containing the same `state_uuid` via a temp file and atomic rename while still holding the lock.
- If an interrupted first-run leaves only a temp guard file and no canonical guard marker, ignore or remove the temp file while holding the lock; do not allocate until the SQLite singleton row and canonical guard marker are consistent.
- If the guard marker exists but the SQLite state file is missing, has no singleton row, or has a different `state_uuid`, `set_cwd` returns `cwd_state_unavailable` and no new id is allocated.
- If the SQLite state file exists but the guard marker is missing, `set_cwd` returns `cwd_state_unavailable` instead of adopting or recreating state silently.
- Sharing one state path across multiple server processes is supported only through this lock protocol. Concurrent first-run or allocation attempts must either serialize through the lock or return `cwd_state_unavailable`; they must never create ambiguous DB/guard state.
- The normal tool surface does not provide an automatic reset path. An operator who deliberately deletes all server-local cwd state, including both SQLite and guard marker, is performing an explicit allocator reset and must ensure stale remembered `cwd_id` values are discarded before re-enabling cwd use.

Failure behavior:

- If allocator initialization or transaction fails, `set_cwd` returns structured error code `cwd_state_unavailable`.
- Existing active ids in memory continue to work if the registry was already initialized and allocator later fails.
- No id is returned until the allocator commit has succeeded.
- A process restart with the same SQLite state advances from the stored high-water mark.
- A stale id from before restart is unavailable because active entries are not persisted; it must not map to a new cwd.
- Handler stores the allocator `state_uuid` read during initialization and verifies the same `state_uuid` on every allocation.
- If the state DB disappears, is recreated with a different `state_uuid`, loses the singleton row, or cannot be advanced after active ids already exist, mark the allocator unhealthy for the rest of the process.
- If the state DB is rolled back or restored with the same `state_uuid` but a lower `last_issued` than ids already seen in this live process, the in-process collision guard catches the stale candidate, marks the allocator unhealthy, and prevents id reuse.
- A cold-start rollback or snapshot restore of both SQLite state and guard marker to an older matching `state_uuid` is not automatically detectable by this design. Treat it as an operator restore/reset scenario, not normal recovery. Operators and packaged-runtime docs must require agents to discard remembered `cwd_id` values before re-enabling cwd use after such a restore.
- The server does not try to invent volatile ids or trust remembered ids after an operator restore. If the runtime cannot preserve monotonic allocator state across restore/recreation, it must document `set_cwd` as unavailable or require explicit reset/discard of old agent context before use.
- If the state DB is missing or recreated across a cold process start while the guard marker remains, initialization fails with `cwd_state_unavailable`; it must not recreate `last_issued = 0` and risk id reuse.
- While the allocator is unhealthy, future `set_cwd` calls return `cwd_state_unavailable`; no volatile fallback ids are allocated.
- Active id lookups that are already in memory may continue until their TTL expires, because lookup does not allocate and cannot remap an id to a different cwd.
- The implementation must include tests that register one cwd, simulate allocator reset/recreation or failed advance in a live process, prove a new `set_cwd` returns `cwd_state_unavailable`, and prove the old active id still resolves only to its original cwd.
- The implementation must include a live-process stale high-water test: register one or more ids, roll back SQLite `last_issued` to an already seen value while keeping the same `state_uuid`, then prove the next `set_cwd` returns `cwd_state_unavailable` and no active or expired id is remapped.
- The implementation or docs tests must cover cold-start rollback/snapshot restore as an operator restore/reset scenario: stale remembered ids are declared invalid context, not silently trusted.
- The implementation must also test cold restart with missing/recreated SQLite state while the guard marker remains, proving `set_cwd` returns `cwd_state_unavailable` and does not issue an id that could collide with remembered context.
- The implementation must test interrupted and concurrent initialization: DB-without-guard fails unavailable, temp-guard-only cleanup does not allocate until canonical state is consistent, and a second registry/process using the same state path either waits/serializes through the lock or returns `cwd_state_unavailable` without id collision.

State path:

- Add `MCP_CWD_STATE_PATH`.
- Add `MCP_CWD_REQUIRE_EXPLICIT_STATE_PATH`.
- Default to `os.UserConfigDir()/mcp-file-tools/cwd-state.sqlite`.
- If `MCP_CWD_STATE_PATH` is set, it must be an absolute, stable, writable server-local path in the filesystem where the MCP server process runs. Empty or relative values make `set_cwd` return `cwd_state_unavailable`.
- Parse `MCP_CWD_REQUIRE_EXPLICIT_STATE_PATH` as a strict boolean: unset/empty is false for direct local runs; accepted true values are `true`, `1`, `yes`, and `on`; accepted false values are `false`, `0`, `no`, and `off`, case-insensitive.
- Malformed `MCP_CWD_REQUIRE_EXPLICIT_STATE_PATH` values are fail-closed: `set_cwd` returns `cwd_state_unavailable` before resolving or creating allocator state.
- `MCP_CWD_REQUIRE_EXPLICIT_STATE_PATH=true` disables the default user-config path. When it is true and `MCP_CWD_STATE_PATH` is unset, empty, or relative, `set_cwd` returns `cwd_state_unavailable` before first allocation.
- If no default path can be resolved and `MCP_CWD_STATE_PATH` is unset, `set_cwd` returns `cwd_state_unavailable`.
- Create parent directories for the state file during registry initialization.
- Place the guard marker next to the state file, or derive its location from `MCP_CWD_STATE_PATH`, so tests and operators can isolate or remove both together.
- Direct local process runs may use the default user-config path.
- Container/packaged runtimes must not silently use the default user-config path. They must set `MCP_CWD_REQUIRE_EXPLICIT_STATE_PATH=true` and require `MCP_CWD_STATE_PATH` to be set to a persistent writable mount before `set_cwd` can allocate ids.
- If a container/packaged runtime cannot prove a persistent state path through its config surface, `set_cwd` returns `cwd_state_unavailable` before first allocation instead of creating ephemeral state.
- Packaged-runtime recreation tests must be stateful: allocate at least one id, recreate the runtime with the configured persistent state surface, then prove the SQLite state and guard marker remain available and the next allocation advances from the stored high-water mark.
- A packaged runtime without a persistent state surface must fail before allocation with `cwd_state_unavailable`.
- If both SQLite state and guard marker are absent after a supposed packaged-runtime recreation, that scenario is not treated as normal recreation; it is either an explicit operator reset or an unsupported ephemeral configuration, and tests/docs must require stale remembered `cwd_id` values to be discarded before cwd use resumes.

TTL:

- Add `MCP_CWD_TTL_SECONDS`.
- Default: `604800` seconds, exactly 7 days.
- TTL is counted from the most recent successful `set_cwd` registration for that active id, not from ordinary tool usage.
- Invalid, unparsable, or non-positive `MCP_CWD_TTL_SECONDS` falls back to the default `604800`, matching the existing config style for numeric env settings.
- Invalid TTL config does not make `set_cwd` fail; tests must prove fallback behavior.

## Error Codes

Minimum cwd-specific errors:

- `invalid_directory`: missing, relative, non-directory, or unresolved `set_cwd.directory`
- `cwd_state_unavailable`: SQLite allocator cannot be used
- `invalid_cwd_id`: non-positive or malformed id
- `cwd_id_unknown`: id was never active in this process
- `cwd_id_expired`: id existed but expired
- `absolute_path_not_allowed_with_cwd`: absolute input supplied with `cwd_id`
- `relative_path_requires_cwd`: relative input supplied without `cwd_id`
- `path_outside_cwd`: relative input escapes cwd or output path cannot be represented under cwd

## Acceptance

- `set_cwd` has exactly one input and returns exactly one success field.
- Registry is shared by all clients using the same `Handler`.
- Sequential ids survive handler/server restart as high-water allocation state, but old active mappings do not.
- Expired and stale ids produce structured errors and never remap.
- Tests prove `set_cwd` does not call or emulate `os.Chdir`.

## Stop And Ask If

- The repository cannot accept `modernc.org/sqlite` or `github.com/gofrs/flock`.
- The SQLite or lock dependency cannot be downloaded, vendored, or kept consistent with `vendor/modules.txt`.
- Default config directory behavior conflicts with the intended deployment model.
- Product wants active cwd entries persisted across process restart, which is broader than the accepted concept.
