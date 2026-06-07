# Stage 1: Redaction And Read Parity

## Goal

Make literal output the default agent workflow and repair redaction false positives without removing explicit strict redaction.

## Depends On

- Clean Phase 7 concept.
- Existing Phase 5 redaction helpers in `filetoolsserver/handler/phase5_helpers.go`.
- Existing content-bearing tools: `grep`, `read_files`, write preview/read-back outputs.

## Touched Areas

- `filetoolsserver/handler/phase5_helpers.go`
- `filetoolsserver/handler/tool_types.go`
- `filetoolsserver/handler/grep_tool.go`
- `filetoolsserver/handler/read_files.go`
- `filetoolsserver/handler/range_transfer.go`
- `filetoolsserver/handler/write_preview.go`
- `filetoolsserver/handler/schema_constraints.go`
- tests in `filetoolsserver/handler/agent_tools_test.go`, `write_tools_test.go`, `batch_tools_test.go`

## Contract

Public values:

- `redaction_mode="off"`: literal content, no redaction.
- `redaction_mode="strict"`: redact strong secret-like values only.
- `redaction_mode="auto"`: accepted deprecated alias for repaired `strict`.

Defaults:

- omitted redaction mode = `off`.
- `read_file` remains literal-only and has no `redaction_mode` input.
- `read_files` default/omitted mode = `off`.
- write tools default/omitted mode = `off`.
- `grep` default/omitted mode = `off`.

Hard no-redact surfaces:

- filesystem paths;
- diff header labels;
- filenames;
- module/package/import paths;
- symbol names and identifiers;
- JSON/YAML key names;
- option names and numeric config-like fields, including `max_output_tokens`.

Policy-forced metadata:

- if config/server policy ever forces redaction, output must expose `redaction_policy.forced=true` and effective mode.
- If no such policy currently exists, add only schema-compatible struct fields where needed or explicitly defer policy support in docs without hidden behavior.

## Steps

1. Add `redactionOff` constant and update `normalizeRedactionMode`.
2. Change omitted mode normalization to return `off`.
3. Map `auto` to strict behavior internally or normalize to `strict` while preserving input echo if compatibility needs it.
4. Update redaction helpers so strict redaction is value-only.
5. Ensure strict redaction never receives path labels as redactable content.
6. Update `grep` redaction flow:
   - default literal;
   - strict/auto redacts all grep content-bearing text values, including match and context snippets;
   - path/range metadata remains literal;
   - path fields remain literal.
7. Update `read_files`:
   - default literal;
   - strict/auto opt-in only;
   - per-item `redacted` false by default.
8. Update write preview/read-back:
   - default literal;
   - strict/auto opt-in;
   - diff labels/path headers never redacted.
9. Update schema enum constraints to include `off`.
10. Update tests that currently expect auto redaction by default.
11. Add false-positive tests for:
   - Go module path;
   - import path;
   - filename ending in `.md`;
   - `max_output_tokens`;
   - JSON/YAML key names;
   - cwd-relative paths in recommended inputs.
   - grep strict context redaction where match/context snippets contain secret-like values but path/key/name metadata remains literal.

## Checks

- Targeted redaction/read tests pass.
- Existing redaction tests are rewritten to explicit `strict`/`auto`.
- No docs example shows `redaction_mode="auto"` as normal default workflow.

## Handoff / Next Stage

After Stage 1, content outputs should be literal by default. Stage 2 can then repair truncation without redaction false positives hiding preview evidence.

## Stop And Ask If

- Existing public schema cannot accept `off` without breaking registration.
- A server policy already exists that forces redaction and needs a product decision.
