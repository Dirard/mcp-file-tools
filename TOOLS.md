# MCP File Tools v2

This is the complete public surface. It is intentionally compact so an agent
does not need fourteen schemas or verbose JSON result objects in context.

## Response extraction

- For project, search, read, and all tool errors, emit content[0].text.
- For successful set_cwd, emit structuredContent.cwd_id.
- Never stringify the whole CallToolResult.

All input objects are closed: unknown or misplaced fields are invalid.

## Continuation

A partial project, search, or read page includes cursor in its text header.
Continue the same operation with exactly:

~~~json
{"cwd_id":17,"cursor":"opaque-22-char-token"}
~~~

Do not repeat the initial query fields. A cursor is connection-local, expires,
and advances once. Project and search cursors preserve traversal state and
resume without restarting from the root; they are not filesystem snapshots,
so changes to paths not yet visited can affect later pages. Read cursors page
over content captured before the first response.

## set_cwd

Registers one absolute local directory.

Initial input:

~~~json
{"directory":"/absolute/project"}
~~~

directory must be an absolute host path containing at most 4096 UTF-8 bytes.
Repeated registration of the same live root returns the same cwd_id. The id is
process-local; register the root again after restart.

Successful output:

~~~json
{"cwd_id":17}
~~~

## project

Lists a sorted, bounded tree under cwd_id.

Initial fields:

| Field | Required | Default | Constraint |
| --- | --- | --- | --- |
| cwd_id | yes | - | positive registered id |
| path | no | . | relative path beneath the root |
| depth | no | 2 | 0..8 |
| limit | no | 200 | 1..1000 rows |
| include_ignored | no | false | include ordinary ignored directories |

Example:

~~~json
{"cwd_id":17,"path":"internal","depth":2,"limit":200}
~~~

Text shape:

~~~text
@@project	"internal"	complete	rows=4
D	"internal"
D	"internal/app"
F	"internal/app/run.go"
F	"internal/app/run_test.go"
~~~

D is a directory and F is a file. A partial header also has
cursor=TOKEN.

## search

Searches in one of three modes.

Common initial fields:

| Field | Required | Default | Constraint |
| --- | --- | --- | --- |
| cwd_id | yes | - | positive registered id |
| query | yes | - | 1..4096 UTF-8 bytes |
| mode | no | text | file, text, or symbol |
| path | no | . | relative search root |
| ignore_case | no | false | Unicode case-fold matching |
| include_ignored | no | false | include ordinary ignored directories |
| limit | no | 50 | 1..1000 result rows |

Text and symbol modes also accept:

| Field | Default | Notes |
| --- | --- | --- |
| glob | unset | optional file filter |
| regex | false | interpret query as RE2 |
| context | 0 | text only, 0..20 lines |

In file mode, query itself is the file glob and glob, regex, and context are
not accepted. Symbol mode does not accept context.

Examples:

~~~json
{"cwd_id":17,"mode":"file","query":"**/*.go","path":"internal"}
{"cwd_id":17,"mode":"text","query":"LoadRuntime","path":"internal","glob":"*.go","context":2}
{"cwd_id":17,"mode":"symbol","query":"NewServer","path":"internal","ignore_case":false}
~~~

File output:

~~~text
@@search	file	complete	rows=2
F	"internal/app/run.go"
F	"internal/mcpstdio/server.go"
~~~

Text output groups rows by path:

~~~text
@@search	text	complete	rows=2	matches=1
@	"internal/config/runtime.go"
M	102	// LoadRuntime reads only the closed v2 startup surface.
C	103	func LoadRuntime(lookup LookupEnv) (Runtime, error) {
~~~

M is a match and C is context. Symbol output uses:

~~~text
@	"internal/mcpstdio/server.go"
S	31:34	function	"NewServer"
~~~

## read

Reads 1..24 files from one immutable snapshot.

Top-level fields:

| Field | Required | Default | Constraint |
| --- | --- | --- | --- |
| cwd_id | yes | - | positive registered id |
| files | yes | - | 1..24 file objects |
| view | no | source | source or outline |
| max_bytes | no | 32768 | 4096..32768 output bytes |

Source file objects contain path, optional start defaulting to 1, and required
end. start and end are inclusive positive line numbers.

~~~json
{"cwd_id":17,"view":"source","files":[
  {"path":"internal/app/run.go","start":1,"end":80},
  {"path":"internal/mcpstdio/server.go","start":31,"end":75}
]}
~~~

Outline file objects contain only path:

~~~json
{"cwd_id":17,"view":"outline","files":[
  {"path":"internal/app/run.go"},
  {"path":"README.md"}
]}
~~~

Source output:

~~~text
@@read	source	complete	items=1
@	"internal/app/run.go"	item=0	1:3	complete
1|package app
2|
3|import (
~~~

Outline output uses compact records:

~~~text
@@read	outline	complete	items=1
@	"internal/app/run.go"	item=0	go	complete
I	4:4	"context"
S	0	28:28	variable	"Version"
S	0	88:145	function	"Run"
~~~

I is an import, H is a Markdown heading, and S is a symbol. Symbol records
include depth, range, kind, and quoted name where the parser supplies them.

A failure is item-local:

~~~text
@	"<path-hidden>"	item=1	error	not_found
~~~

If every item fails, the MCP result is an error. Mixed success remains a normal
result and preserves each item status.

## Common text grammar

Each page begins with exactly one @@ header. Tab separates metadata fields.
Paths and names are JSON-quoted UTF-8 scalars. Source rows use line|text so the
file body does not need JSON escaping.

Warnings start with !. Broad terminal warnings contain code, count, omitted,
and sometimes one example path. Read-item warnings contain code and item.

Terminal errors are one line:

~~~text
ERROR	invalid_input
~~~

The stable error codes cover invalid input, unknown/expired/consumed cursors,
root/path/file failures, unsupported language, limits, cancellation/timeouts,
and I/O failures. Error text never includes a raw host path.

## Operational limits

The closed startup environment supports these settings:

- MCP_CWD_TTL_SECONDS, MCP_CWD_MAX_ENTRIES
- MCP_CURSOR_TTL_SECONDS, MCP_CURSOR_MAX_ENTRIES
- MCP_CURSOR_MAX_ENTRY_BYTES, MCP_CURSOR_MAX_TOTAL_BYTES,
  MCP_CURSOR_MAX_PAGES
- MCP_CALL_MAX_CONCURRENT, MCP_CALL_QUEUE_MAX,
  MCP_CALL_QUEUE_TIMEOUT_MS
- MCP_SCAN_MAX_FILES, MCP_SCAN_MAX_DIRS, MCP_SCAN_MAX_BYTES,
  MCP_SCAN_TIMEOUT_MS, MCP_SCAN_MAX_CALLS, MCP_SCAN_FRONTIER_MAX_BYTES
- MCP_PARSE_MAX_BYTES, MCP_PARSE_MAX_CALLS
- MCP_PARSER_CACHE_MAX_ENTRIES, MCP_PARSER_CACHE_MAX_BYTES
- MCP_IGNORE_DIRS_ADD, a JSON array of additional directory basenames

Cross-field-invalid settings fail startup rather than silently changing the
contract. Unknown environment variables are ignored.

## File Tools and CodeGraph

Run File Tools and CodeGraph as separate MCP servers:

- use CodeGraph for semantic discovery, references, call paths, and impact;
- use File Tools for project shape, exhaustive decoded-text/file search,
  non-code content, and exact source or outline reads;
- do not repeat a File Tools read when CodeGraph already returned sufficient,
  current source;
- fall back to File Tools when a graph index is absent or stale.

There is no runtime, configuration, cache, or index coupling between them.
