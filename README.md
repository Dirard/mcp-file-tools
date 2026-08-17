# MCP File Tools

Token-efficient, read-only project navigation for MCP clients.

The server exposes four stdio tools:

| Tool | Purpose |
| --- | --- |
| set_cwd | Register one absolute workspace root and return a small cwd_id. |
| project | List a bounded project tree. |
| search | Search file paths, decoded text, or parser symbols. |
| read | Read exact source ranges or compact parser outlines for up to 24 files. |

Navigation results are compact line-oriented text, not deeply nested JSON. The
protocol envelope stays standard MCP, while the useful payload is always
content[0].text. Successful set_cwd also mirrors cwd_id in structuredContent.

## Why four tools

Most agent filesystem cost comes from discovering a project, searching it, and
reading the same source through verbose wrappers. This surface keeps those
operations explicit while sharing:

- one registered root instead of repeated absolute paths;
- bounded scans and parser concurrency;
- immutable cursor state for continuation;
- grouped text records with one header per page;
- batch source and outline reads;
- item-local errors, so one missing file does not discard the rest of a read.

There are no write, copy, move, HTTP, path-remapping, or compatibility tools in
v2.

## Install and run

Use a release binary, or install the command with Go:

~~~sh
go install github.com/Dirard/mcp-file-tools/cmd/mcp-file-tools@latest
~~~

Configure the executable as a stdio MCP server. For Codex:

~~~toml
[mcp_servers.file_tools]
command = "/absolute/path/to/mcp-file-tools"
~~~

The command accepts no server mode flags. The only supported arguments are
-version and -v.

For a local source checkout:

~~~sh
go test ./...
go build -trimpath -buildvcs=false -o mcp-file-tools ./cmd/mcp-file-tools
~~~

## Agent workflow

1. Call set_cwd once with the absolute project root.
2. Use project for structure or search for a focused candidate set.
3. Use read only on the exact files or ranges needed.
4. When a response is partial, call the same tool with only cwd_id and cursor.

Example:

~~~text
set_cwd  {"directory":"/work/project"}
project  {"cwd_id":17,"path":".","depth":2}
search   {"cwd_id":17,"mode":"text","query":"LoadRuntime","path":"internal"}
read     {"cwd_id":17,"view":"source","files":[{"path":"internal/config/runtime.go","start":102,"end":125}]}
~~~

Relative paths are resolved beneath the registered root. Repeat set_cwd after
the MCP process restarts. Cursors are opaque, connection-local, and resume
without restarting the operation. Project and search are one-pass live
traversals: changes to paths not yet visited can affect later pages. Read
captures the requested files before publishing its first cursor.

See [TOOLS.md](TOOLS.md) for the complete compact contract.

## Using File Tools beside CodeGraph

[CodeGraph](https://github.com/colbymchenry/codegraph) is a useful adjacent MCP,
not a dependency or embedded component.

Configure both servers in the client:

- CodeGraph handles semantic code discovery: symbols, references, call paths,
  architecture questions, and blast radius.
- File Tools handles the project map, exhaustive decoded-text or file search,
  non-code files, and exact source ranges or outlines.

A practical route is:

1. Ask codegraph_explore first when the question is about code semantics.
2. If its current source context is already sufficient, do not read the same
   source again.
3. Use File Tools when the graph is absent or stale, the target is non-code, an
   exhaustive text search is required, or exact ranges must be fetched.

Install and index CodeGraph separately according to its repository
instructions, then register it as another MCP server. The two processes do not
share configuration, caches, indexes, or runtime state.

## Output contract

Project, search, read, warnings, and errors are text records. Common prefixes:

- @@project, @@search, @@read: page header;
- D and F: directory and file;
- M and C: text match and context;
- S: symbol;
- @: path or read-item group;
- !: warning;
- ERROR: terminal tool error.

Paths and names in metadata records are JSON-quoted scalars. Source lines use
line|text and preserve literal UTF-8 text.

The MCP server instruction is deliberately short:

~~~text
Code mode: max_output_tokens=10000; emit content[0].text for navigation/errors, cwd_id for successful set_cwd; never stringify CallToolResult.
~~~

## Runtime limits

Defaults bound active calls, queued calls, scan files/directories/bytes/time,
parser work, parser cache size, cursor lifetime/pages/bytes, and cwd registry
size. They can be tuned with the MCP_CALL_*, MCP_SCAN_*, MCP_PARSE_*,
MCP_PARSER_CACHE_*, MCP_CURSOR_*, MCP_CWD_*, and MCP_IGNORE_DIRS_ADD
environment settings documented in [TOOLS.md](TOOLS.md).

Invalid startup configuration fails closed with one detail-free stderr line.
Ordinary tool errors stay in the MCP response and do not write to stderr.

## Container

The Docker image is also stdio-only:

~~~sh
docker build -t mcp-file-tools .
docker run --rm -i -v /absolute/project:/workspace:ro mcp-file-tools
~~~

Inside the container, register /workspace with set_cwd.

## Breaking v2 migration

v2 intentionally has no compatibility aliases. The former fourteen-tool
surface, write/refactor operations, HTTP transport, watchdog scripts, SQLite
cwd state, path maps, encoding detection, and redaction options were removed.

## License

GPL-3.0. See [LICENSE](LICENSE).
