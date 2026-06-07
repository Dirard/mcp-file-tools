# Phase 5 Agent Safe Write, Read, And Discovery Technical Concept

concept_version_label: phase5-agent-safe-write-read-discovery-v1
status: clean_srs_reviewed_ready_for_implementation

## Technical Direction

Phase 5 upgrades the current file-tools surface from "structured file operations" to an agent-safe operational loop.

The implementation should be additive:

- preserve existing tool names and required inputs;
- add fields/options where they improve agent proof and recovery;
- add small helper tools only when repeated multi-call workflows are clearly wasteful or unsafe;
- keep all outputs structured JSON;
- preserve cwd-aware projection for every new path-bearing field.

The main design rule: if a feature changes what an agent may trust, the output must include the proof or incompleteness reason.

## Current Baseline

Current tools relevant to Phase 5:

- `read_file`;
- `copy_ranges`;
- `move_ranges`;
- `copy_ranges_batch`;
- `move_ranges_batch`;
- `list_dir`;
- `glob_file_search`;
- `grep`;
- `inspect_path`;
- `workspace_inventory`;
- existing `outline_file` continuation hints.

Current support already includes:

- `cwd_id` path mode;
- slash-normalized absolute paths without `cwd_id`;
- cwd-relative paths with `cwd_id`;
- source/target fingerprint preconditions for writes;
- `dry_run`;
- optional sidecar backups;
- boundary warnings;
- batch partial-state recovery;
- Phase 4 grep stats, file groups and next-call hints.

Phase 5 should not discard these mechanisms. It should make them more visible, auditable and composable.

## Contract Additions

### Diff Preview

Add bounded unified diff preview to write outputs.

Conceptual fields:

```json
{
  "diff_previews": [
    {
      "role": "target",
      "format": "unified",
      "truncated": false,
      "text": "...",
      "stats": {
        "files_changed": 1,
        "lines_added": 10,
        "lines_removed": 2
      }
    }
  ]
}
```

For batch tools, diff preview belongs both:

- per target in `target_results[]`;
- top-level for source rewrite/removal when `move_ranges_batch` mutates source.

SRS should decide exact field names and truncation limits. Concept requirements:

- `dry_run=true` produces diff preview without mutation;
- applied outputs may include the same preview or a validation diff summary;
- diff paths are output-projected;
- diff content redaction applies when secret-safe mode requires it;
- truncation is explicit and never silently drops hunks.

### Joiner Boundary Model

The current `joiner` enum can remain, but its output must become explicit.

Conceptual additions:

```json
{
  "joiner_effect": {
    "requested": "blank_line",
    "normalized": "blank_line",
    "inserted_newlines_between_blocks": 2,
    "target_before_ended_with_newline": true,
    "payload_started_with_newline": false
  },
  "boundary_preview": {
    "before": "Keep line.",
    "between": "\n\n",
    "after": "## Beta"
  }
}
```

The important contract is not exact field naming. The important contract:

- agent can predict visual spacing before write;
- `blank_line` means one visually blank line between adjacent non-empty text blocks;
- boundary warnings are deterministic;
- invalid joiner uses a specific `error_code`.

### Post-Write Validation

Write outputs should distinguish:

- planned;
- applied and inspected;
- applied but post-write inspect/read-back failed;
- partially applied with recovery state.

Conceptual additions:

```json
{
  "validation": {
    "status": "applied_and_verified",
    "target_read_back": {
      "file": "docs/part.md",
      "range": { "start": 10, "end": 30 },
      "text": "10|..."
    }
  }
}
```

Validation should be bounded. It should not dump huge files. If read-back is omitted because it would be too large, output should provide a `next_recommended_call` to `read_file`.

### Backup Discovery

Backups should remain sidecar files unless SRS chooses a more discoverable format.

Required technical behavior:

- `BackupResult` continues to return `backup_path`;
- backup paths pass through cwd projection;
- backup naming is stable enough for glob discovery;
- `list_dir` and `glob_file_search` hidden-aware mode can show backup sidecars;
- write outputs provide a recovery hint that includes how to rediscover backup files.

If backups are dot-files, hidden-aware discovery is mandatory. If backups are non-dot files, they must not pollute normal workflows too heavily. SRS should choose one naming convention and document it.

### Include Hidden And Discovery Modes

Add an explicit hidden policy to traversal/listing tools.

Candidate input:

```json
{
  "include_hidden": true
}
```

or, if SRS needs more precision:

```json
{
  "hidden_policy": "exclude|include_dotfiles|include_dotfiles_exclude_vcs"
}
```

Required semantics:

- default remains hidden excluded;
- exact hidden path lookup remains allowed;
- broad VCS metadata traversal remains excluded unless a separate strict option is accepted;
- output echoes hidden policy;
- counters distinguish hidden skipped vs hidden included where practical.

Affected tools:

- `list_dir`;
- `glob_file_search`;
- `workspace_inventory`;
- `grep` only with secret-safe content handling;
- possibly `inspect_path` only to explain how hidden status affects discovery.

### Explain Missing / Explain Ignored

Add a cheap diagnostic path for "why did my file not appear?"

Possible designs:

- add `explain_path` input to discovery tools;
- add a separate `explain_path_visibility` helper;
- extend `inspect_path` with discovery diagnostics.

Concept acceptance:

- agent can ask about one concrete path;
- response does not require broad raw search;
- reasons are structured;
- cwd/outside-cwd and symlink cases remain safe;
- hidden/ignored/glob/type/binary/unreadable reasons are distinguishable.

### Read Completeness And Continuation

`read_file` should expose coverage semantics.

Conceptual fields:

```json
{
  "coverage": {
    "requested_range_complete": true,
    "file_total_lines_known": false,
    "complete_file_read": false,
    "next_range": { "start_line": 201, "end_line": 400 }
  }
}
```

Possible inputs:

- `count_total_lines`: opt-in full line count for bounded ranges;
- `max_lines` / `chunk_lines`: bounded chunk size;
- `continuation` or `start_line`/`end_line` next-call hints.

Required properties:

- no hidden server session state;
- continuation can be reconstructed from file path, line numbers and fingerprint;
- if file fingerprint changed, continuation warns or errors clearly;
- total-line counting remains optional for performance.

### Batch Read

Add a compact batch read path if SRS confirms it reduces calls without confusing outputs.

Candidate tool:

- `read_files`;
- or `read_ranges_batch`.

Required semantics:

- each item has its own `target_file`, optional range, output/error, and coverage;
- global limits prevent dumping too much text;
- cwd projection applies per item;
- per-item errors do not hide other successful reads;
- output order follows input order unless explicitly sorted.

### Cursor / Stateless Continuation

For truncated discovery tools, continuation must be explicit.

Candidate pattern:

```json
{
  "continuation": {
    "complete": false,
    "next_recommended_call": {
        "recommended_next_tool": "glob_file_search",
        "recommended_next_input": {
          "target_directory": "internal",
        "glob_pattern": "*.go",
        "continuation_after": {
          "canonical_query_hash": "sha256:...",
          "last_sort_key": { "path": "internal/a.go", "modified_unix_nano": 123 }
        }
      }
    }
  }
}
```

SRS should choose exact mechanism. Required constraints:

- not bound to MCP session, chat, or subagent;
- invalidated honestly when sort key/fingerprint/filesystem state changed;
- carries enough query parameters for replay;
- never leaks absolute paths under `cwd_id`.

### Workspace Summary

Extend `workspace_inventory` with summary fields.

Candidate output:

```json
{
  "summary": {
    "file_type_counts": { ".go": 42, ".md": 8 },
    "source_dir_hints": ["cmd", "filetoolsserver", "internal"],
    "test_dir_hints": ["filetoolsserver/handler"],
    "package_hints": ["go.mod"],
    "largest_directories": [
      { "path": "filetoolsserver/handler", "direct_file_count": 50 }
    ]
  }
}
```

The summary should be cheap and bounded. It must not require indexing every file in huge workspaces unless SRS explicitly scopes that cost.

### Glob Sort And Grouping

Add sort controls to `glob_file_search`.

Candidate values:

- `modified_desc` default;
- `modified_asc`;
- `path_asc`;
- `path_desc`;
- `size_desc`;
- `size_asc`;
- `directory_path_asc`.

Output should echo `sort` and include stable tie-break semantics.

### Secret Redaction

Add redaction policy to content-bearing risky outputs.

Candidate modes:

- `redaction: "auto"` default for hidden/config/log broad content flows;
- `redaction: "off"` only if SRS and safety review allow it for non-hidden normal files;
- `redaction: "strict"` for high-risk audits.

Affected outputs:

- `grep.matches[].text`;
- `diff_previews[].text`;
- `read_back.text`;
- error snippets, warnings and recovery hints if they quote file content.

The concept requires no raw secret-like literal leaks in broad hidden/config/log workflows. Exact detectors belong in SRS and tests.

### Error Codes

Stabilize specific error codes for common recovery paths.

Existing generic errors should be split where useful:

- invalid input shape;
- invalid enum;
- fingerprint mismatch;
- range out of bounds;
- hidden excluded;
- ignored by glob;
- stale continuation;
- backup creation failure;
- post-write validation failure.

`error_code` should align across single and batch write outputs.

### Scoped Cleanup

Cleanup must be a separate bounded contract, not a hidden behavior inside discovery tools.

SRS resolution: Phase 5 has no cleanup/delete helper. Backup/test-fixture deletion is deferred to a later phase because current sidecar filename shape is not enough provenance.

Candidate helper, if a later SRS can prove provenance:

- `cleanup_artifacts`.

Required safeguards:

- explicit paths or explicit tool-owned backup/test-fixture pattern;
- `dry_run` required by default;
- cwd/workspace boundary proof;
- no symlink-follow deletion surprise;
- no VCS destructive equivalent;
- per-file result with reason.

The Phase 5 SRS may defer this helper if tool-created provenance cannot be proven strongly enough. In that case Phase 5 should still improve backup discovery and must not expose deletion/cleanup.

## Data / Schema Compatibility

Phase 5 should prefer additive optional fields:

- old clients can ignore new fields;
- arrays remain arrays, not `null`;
- existing field meanings do not drift;
- legacy byte metrics remain but clearer fields can be added.

If a new helper tool is added, `README.md`, `TOOLS.md`, server descriptions and `server.json` must be updated together.

## Path And Cwd Projection

All new path-bearing fields must be enumerated in SRS and tests.

Likely surfaces:

- diff file headers;
- backup discovery hints;
- validation/read-back file fields;
- continuation recommended input;
- workspace summary paths;
- grouped glob paths;
- explain-missing diagnostic paths.

With `cwd_id`, these fields must be cwd-relative, while output includes `cwd_id` and absolute slash-normalized `cwd`.

## Safety Direction

Phase 5 touches write behavior, hidden files, redaction and backup rediscovery. Required safety posture:

- hidden traversal remains opt-in;
- VCS metadata remains protected by default;
- secret-like values are redacted in risky broad outputs;
- cleanup/delete is not exposed unless a later provenance-backed SRS accepts it;
- write tools remain fingerprint-gated;
- diff preview never mutates files;
- recovery metadata must not expose raw secrets.

## Testing Direction

SRS should include tests for:

- diff preview for create, append, prepend, insert, replace, move source removal, batch targets and batch source rewrite;
- diff truncation;
- `blank_line` and newline boundary examples;
- post-write validation success and failure;
- backup discovery through hidden-aware listing/search;
- hidden default unchanged;
- hidden opt-in;
- explain missing/ignored reasons;
- `read_file` coverage and total-line opt-in;
- batch read per-item success/error;
- continuation stale/valid cases;
- workspace summary boundedness;
- glob sort modes;
- redaction for grep/diff/read-back;
- cwd projection for every new path surface;
- Windows slash paths and CRLF preservation.

## Stop And Ask If

- Supporting hidden content search safely requires raw secret output by default.
- Cleanup cannot be made narrow, dry-run-first and workspace-bound.
- Diff preview would require reading or returning content beyond configured write/read thresholds.
- Continuation would require session/chat-local server state.
- Any change would break current default hidden skip behavior.
- Any new path-bearing field cannot be projected safely under `cwd_id`.
