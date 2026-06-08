# MCP File Tools

Cross-platform MCP filesystem tools for Codex. The public surface is intentionally small and agent-oriented:

- [`set_cwd`](TOOLS.md#set_cwd) - register one absolute directory and receive a small `cwd_id`
- [`read_file`](TOOLS.md#read_file) - read one file or line range with line numbers
- [`read_files`](TOOLS.md#read_files) - batch-read known files/ranges with continuation and coverage proof
- [`outline_file`](TOOLS.md#outline_file) - inspect file structure and get a write-safe fingerprint
- [`resolve_symbol_range`](TOOLS.md#resolve_symbol_range) - resolve outline selectors to concrete ranges and dry-run write hints
- [`copy_ranges`](TOOLS.md#copy_ranges) - copy exact line ranges into one explicit target
- [`move_ranges`](TOOLS.md#move_ranges) - move exact line ranges into one explicit target
- [`copy_ranges_batch`](TOOLS.md#copy_ranges_batch) - copy ranges into multiple explicit targets
- [`move_ranges_batch`](TOOLS.md#move_ranges_batch) - move ranges into multiple explicit targets
- [`list_dir`](TOOLS.md#list_dir) - list direct directory children
- [`glob_file_search`](TOOLS.md#glob_file_search) - recursively find files by glob pattern
- [`grep`](TOOLS.md#grep) - agent-friendly search with stats, grouped ranges, and next-call hints
- [`inspect_path`](TOOLS.md#inspect_path) - inspect useful metadata for one path
- [`workspace_inventory`](TOOLS.md#workspace_inventory) - build a directories-only project map

The tools are adapted from the `mr-agent` file discovery/search tools, with local filesystem access and complete single-response MCP output. The same server works on Windows, Linux, and macOS; paths are resolved by the OS where the MCP server runs. By default, path inputs must be absolute paths for that server OS and path outputs are slash-normalized absolute/display paths such as `D:/ai-apps/mcp-file-tools/README.md`. Agents can call `set_cwd` once with an absolute directory, receive a small integer `cwd_id`, and then pass cwd-relative paths with that `cwd_id`; in cwd mode, successful outputs include absolute `cwd` metadata and all other filesystem path fields are relative to that cwd. `read_file` and `read_files` provide line-numbered content with chunk continuation and coverage metadata. `outline_file` gives agents compact Markdown/Go/JS/TS/TSX/Python/Java/Rust/C/C++/C#/Ruby/Kotlin/Swift/Bash/JSON/YAML/Svelte structure, generic text chunks for ordinary text files, selector metadata, and stable fingerprints before write operations; JSON/YAML config nodes are exact for navigation but conservative for writes, and Svelte nested script symbols are explicitly partial/future-gated. `resolve_symbol_range` turns selectors or enclosing lines into concrete ranges and can produce dry-run-only `copy_ranges`/`move_ranges` recommendations when write safety is proven. `copy_ranges`/`move_ranges` and their batch variants use fingerprints, explicit targets, `dry_run`, bounded unified diff previews, joiner/boundary diagnostics, post-write read-back validation, optional sidecar backups, backup rediscovery hints, and structured partial-state recovery for fast mechanical refactors. `glob_file_search` supports sort/continuation and hidden/VCS discovery flags; `grep` supports literal or regex pattern mode, ripgrep-like output modes, JSON-native context fields, type/glob filters, multiline search, `search_stats`, `file_groups` with `read_ranges`, safe hidden traversal, redaction modes, and `next_recommended_call`, and defaults to `limit=50`; `inspect_path` and `workspace_inventory` provide cheap metadata, discovery visibility context, flattened directory pages with `continuation_after`, and summaries without listing every file name. All tools return friendly validation/no-result messages.

Every successful tool returns the complete result as tool-specific structured JSON. Metadata is exposed as JSON fields instead of being embedded in formatted text: read tools return paths, line ranges, coverage and continuation; `outline_file` returns `fingerprint`, `imports`, `symbols`, `sections`, `enclosing_items`, warnings, selectors, and recommended next calls; `resolve_symbol_range` returns matches, resolved ranges, read hints, write refusal reasons, and dry-run write hints when safe; range tools return operation status, fingerprints for the next safe write, previews, validation, backups, warnings, and partial state when needed; discovery tools return counters, skip stats, groups/pages/summaries, and optional next calls. There is no opaque `cursor` input and no `nextCursor` field.

Tool errors are structured for agents: the MCP result is marked `isError=true`, plain text content is empty, and the actionable message is returned in the structured `error` field instead of duplicated as plain text content.

`read_file` accepts `start_line`/`end_line` as source line-range selectors, so agents can jump directly to a known part of a large file. When both range bounds are set, `read_file` avoids a full pre-scan; if EOF is not reached, `total_lines_known` is false and `total_lines` is omitted. A start line beyond EOF returns a structured error with known `total_lines` instead of a silent empty success. For mechanical refactors, use `outline_file` to get line ranges and `source_fingerprint`, run range tools with `dry_run=true`, then apply with the returned next-write fingerprints.

## Agent Workflow

The high-value path is: call `set_cwd`, search with `grep` or `glob_file_search`, follow their bounded `read_file`/`read_files` or `outline_file` recommendations, use `outline_file` with default `output_profile="agent"`, pass selectors to `resolve_symbol_range` with an explicit `target_intent`, run the returned dry-run write call, then apply only after preview/read-back looks right. For escape-sensitive code such as regexes or string literals, treat diff/boundary previews as display previews and verify with post-write read-back or an explicit `read_file`.

`read_file` is literal-only. `read_files`, `grep`, and write previews default to `redaction_mode="off"`; `strict` is explicit opt-in, and `auto` is kept only as a compatibility alias for `strict`. TSX/JS/TS outlines default to a compact agent profile that hides duplicate declaration/local-variable noise while keeping top-level declarations and components. JSON/YAML outlines default to the compact `agent` profile, which keeps key/property paths but omits noisy value and synthetic wrapper items, reporting `omitted_leaf_items`; use `output_profile="full"` only when local symbols or leaf-level config values are needed. Unicode diff and boundary previews are truncated as valid display text, not as an exact escaped-code oracle. `workspace_inventory` remains directory-level and recommends bounded `glob_file_search` rather than guessed file reads; `continuation.page_complete` describes the returned page, while `summary.summary_coverage_complete`, `summary.tree_scan_complete`, `summary.summary_incomplete_reason`, and `summary.scan_scope` describe summary/tree coverage.

## Build

Windows:

```powershell
go build -o .\mcp-file-tools.exe .\cmd\mcp-file-tools
```

Linux/macOS:

```sh
go build -o ./mcp-file-tools ./cmd/mcp-file-tools
```

All release targets:

```sh
make build-all
```

## Run

Windows:

```powershell
.\mcp-file-tools.exe
```

Linux/macOS:

```sh
./mcp-file-tools
```

Streamable HTTP:

```sh
./mcp-file-tools --http 127.0.0.1:8787
```

Append HTTP/tool-call logs to a file:

```sh
./mcp-file-tools --http 127.0.0.1:8787 --log-file ./logs/mcp-file-tools.log
```

Native watchdog with file logs:

```sh
# Linux/macOS after `make build` or `go build -o ./mcp-file-tools ./cmd/mcp-file-tools`
sh ./scripts/start-mcp-file-tools-watchdog.sh
```

```powershell
# Windows after `go build -o .\mcp-file-tools.exe .\cmd\mcp-file-tools`
.\scripts\start-mcp-file-tools-watchdog.ps1
```

The watchdog scripts restart the HTTP server if it exits and write server logs to `logs/mcp-file-tools.log` plus watchdog lifecycle logs to `logs/watchdog.log`. For always-on operation after reboot, run the watchdog with the native process manager for the OS: systemd/launchd on Linux/macOS or Task Scheduler on Windows.

Docker Compose:

```sh
cp .env.example .env
docker compose up -d --build
docker compose logs -f mcp-file-tools
```

The compose service binds the server to `0.0.0.0` inside the container, but publishes only `127.0.0.1:8787` on the host by default. Configure the read/write workspace mount in `.env`; `MCP_HOST_HOME` stays read-only for path lookup convenience. Without `cwd_id`, tool inputs still need absolute paths as seen inside the container; with `cwd_id`, use paths relative to the directory registered by `set_cwd`.

Codex streamable HTTP config:

```toml
[mcp_servers.file-tools]
url = "http://127.0.0.1:8787/mcp"
```

With Docker Compose, keep the container running and let Codex connect to this URL instead of launching the executable through stdio. The compose service uses `restart: unless-stopped`; Docker Desktop or the Docker daemon still needs to start after an OS reboot.

Docker can only access host paths that are mounted into the container. For broad local filesystem access across normal OS paths, run the native binary on that OS and configure your process manager of choice to keep it alive.

## Current Working Directory IDs

Call `set_cwd` with an absolute directory path:

```json
{"directory":"D:/ai-apps/mcp-file-tools"}
```

The success response is intentionally small:

```json
{"cwd_id":1}
```

Pass that `cwd_id` to other path tools to use relative paths:

```json
{"cwd_id":1,"target_file":"README.md"}
```

`cwd_id` is server-wide state owned by the running file-tools server, not by a chat, MCP session, or subagent. It does not call `os.Chdir`, does not create a hidden default cwd, and is not a security token. If a `cwd_id` expires or is lost after restart, tools return a structured error recommending `set_cwd`; the agent should call `set_cwd` again with the remembered absolute `cwd`.

When `cwd_id` is present, successful outputs include `cwd_id` and absolute slash-normalized `cwd`; all other filesystem path fields are cwd-relative and use `/`, without a leading `./`. Without `cwd_id`, inputs remain absolute-only and outputs are slash-normalized absolute/display paths.

## Configuration

The server keeps the public tool API small; runtime tuning is done with environment variables:

- `MCP_MEMORY_THRESHOLD` - file-size threshold for in-memory vs streaming reads, default `67108864` (64 MiB).
- `MCP_MAX_TOOL_CALLS` - concurrent tool-call limit, default `min(8, max(4, runtime.NumCPU()))`.
- `MCP_MAX_SCAN_CALLS` - concurrent recursive `grep`/`glob_file_search` scan limit, default `2`.
- `MCP_MAX_LARGE_READ_CALLS` - concurrent large `read_file` limit for files above `MCP_MEMORY_THRESHOLD`, default `2`.
- `MCP_WRITE_THRESHOLD` - maximum file size accepted by range write tools and Go outline parsing, default equals `MCP_MEMORY_THRESHOLD`.
- `MCP_BATCH_MAX_TARGETS` - maximum targets in one batch range call, default `100`.
- `MCP_BATCH_MAX_RANGES_PER_TARGET` - maximum ranges for one batch target, default `100`.
- `MCP_BATCH_MAX_RANGES_PER_CALL` - maximum total ranges in one batch call, default `500`.
- `MCP_BATCH_MAX_PLANNED_BYTES` - maximum planned batch write bytes, default equals `MCP_WRITE_THRESHOLD`.
- `MCP_DIFF_PREVIEW_MAX_BYTES` - maximum bytes per bounded unified diff preview, default `32768`.
- `MCP_READ_BACK_MAX_LINES` - maximum post-write validation read-back window, default `80`.
- `MCP_BOUNDARY_PREVIEW_MAX_CHARS` - maximum boundary preview characters around write placement, default `1000`.
- `MCP_READ_FILES_MAX_ITEMS` - maximum items in one `read_files` call, default `24`.
- `MCP_READ_FILES_MAX_TOTAL_BYTES` - maximum total returned text bytes across one `read_files` call, default `262144`.
- `MCP_READ_FILES_MAX_ITEM_BYTES` - maximum returned text bytes for one `read_files` item, default `65536`.
- `MCP_CWD_STATE_PATH` - absolute server-local SQLite allocator state path for `cwd_id` high-water allocation. Local runs default to `os.UserConfigDir()/mcp-file-tools/cwd-state.sqlite`.
- `MCP_CWD_REQUIRE_EXPLICIT_STATE_PATH` - strict boolean. When true, `MCP_CWD_STATE_PATH` must be set or `set_cwd` fails closed with `cwd_state_unavailable`. Accepted true values: `true`, `1`, `yes`, `on`; false values: `false`, `0`, `no`, `off`.
- `MCP_CWD_TTL_SECONDS` - active in-memory cwd id TTL in seconds, default `604800` (7 days). Ordinary path-tool lookups do not refresh TTL; successful `set_cwd` for the same active directory does.
- `MCP_HTTP_ADDR` - optional streamable HTTP bind address, equivalent to `--http`; omitted means stdio mode.
- `MCP_LOG_FILE` - optional HTTP/tool-call log file path, equivalent to `--log-file`; parent directories are created automatically.
- `MCP_PATH_MAPS` - optional semicolon-separated `source=target` rewrites for same-OS absolute path aliases. Cross-OS host/container path rewrites are ignored; no-cwd inputs still use paths as seen by the OS where the MCP server runs.

The cwd allocator state bundle is the SQLite DB file, sibling `.guard`, sibling `.lock`, and any SQLite sidecars with `-journal`, `-wal`, or `-shm` suffixes. Treat that bundle atomically for reset, backup, snapshot, and restore. Deleting or restoring only part of it is unsupported; after an intentional allocator reset or snapshot rollback, discard remembered `cwd_id` values before using cwd mode again.

## Tools

See [TOOLS.md](TOOLS.md) for the exact tool inputs and output shape.

## Smoke Test

Windows PowerShell:

```powershell
go test ./...
go run .\test_server.go
```

Linux/macOS shell:

```sh
go test ./...
go run ./test_server.go
```
