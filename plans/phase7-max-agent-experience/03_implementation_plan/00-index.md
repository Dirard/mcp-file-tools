# Phase 7 Implementation Plan: Max Agent Experience

plan_version_label: phase7-max-agent-experience-srs-v1
status: clean_reviewed_ready_for_implementation
concept_source:
- plans/phase7-max-agent-experience/01_human_concept.md
- plans/phase7-max-agent-experience/02_technical_concept.md

## Goal

Repair and polish the existing 14 MCP file tools so the default agent workflow is literal, compact, accurate and easier to continue.

Phase 7 is accepted only if it improves practical AX:

1. normal outputs stop masking benign paths, identifiers, module paths and config keys;
2. content previews never corrupt Unicode through truncation;
3. JSON/YAML outlines become compact and navigable by default;
4. recommended next calls reduce manual path/range/fingerprint reconstruction;
5. common refusal paths return the smallest useful next action;
6. `read_file` and `read_files` behave like single/batch forms of literal reading by default.

## Scope

Affected tools:

- `grep`
- `read_files`
- `outline_file`
- `resolve_symbol_range`
- `copy_ranges`
- `move_ranges`
- `copy_ranges_batch`
- `move_ranges_batch`
- `glob_file_search`
- `workspace_inventory`
- error/action hint surfaces shared by path/write tools

Affected technical areas:

- redaction mode parsing and schema enums;
- Unicode-safe preview truncation helpers;
- JSON/YAML tree-sitter outline item filtering and path naming;
- next recommended call generation;
- write preparation recommendations;
- schema/docs/tests/server metadata.

## Out Of Scope

- No full LSP.
- No cross-file rename.
- No formatter integration.
- No broad structural edit engine.
- No automatic mutation from read-only helpers.
- No automatic cleanup/delete.
- No new public helper tool in Phase 7. If `resolve_symbol_range` cannot carry the explicit manual range path, stop and return to concept/plan for a product decision.
- No safety-first automatic redaction as a product goal.

## Must Preserve

- All 14 tools remain registered.
- `cwd_id` behavior remains stable: cwd-relative inputs/outputs with `cwd_id`; slash-normalized absolute outputs without `cwd_id`.
- Mutation remains explicit and fingerprint-gated.
- `dry_run=true` remains non-mutating.
- Go/Markdown outline regressions remain green.
- JSON/YAML parser honesty remains: navigation can be exact while move/delete stays conservative.
- Existing `redaction_mode="auto"` inputs continue to parse, but are deprecated alias behavior.
- Existing `output_profile="outline"` continues to parse as `agent`.

## Concept Transferred Into SRS

User-visible result:

- Agents get literal useful evidence by default.
- Agents spend fewer steps reconstructing next calls.
- Agents see shorter JSON/YAML outline output without losing key navigation.
- Agents can trust preview snippets with Cyrillic/Unicode.

Behavior / contracts:

- `redaction_mode` public values become `off`, `strict`, `auto`.
- Default/omitted redaction is `off`.
- `auto` is accepted as deprecated alias for repaired `strict`.
- Strong redaction never masks paths, filenames, module/import paths, identifiers or key names.
- `output_profile` public values become `agent`, `full`, `fingerprint_only`, with `outline` accepted as alias for `agent`.
- JSON/YAML canonical path grammar is `document.services[0]["api:key"]`; YAML multi-doc roots use `document[0]`.
- `workspace_inventory` remains directory-level and never guesses direct file-specific read/outline calls.

Acceptance:

- Workflow tests prove search->read, search->outline, read parity, symbol/range->dry-run, JSON/YAML profile, and Unicode preview fidelity.
- Changed schema examples validate.
- Full test suite and targeted race checks pass.
- Registered public tool list remains exactly the existing 14 tools; no new public tool/schema name is added.

## Plan File Map

- `00-index.md`: global goal, scope, decisions, acceptance and checks.
- `01-redaction-and-read-parity.md`: redaction mode contract, false-positive repairs, read_files/read_file parity.
- `02-unicode-preview-truncation.md`: shared valid-display truncation helper and preview/read-back/grep adoption.
- `03-json-yaml-outline-profiles.md`: `output_profile=agent|full`, leaf filtering, canonical path grammar and stats.
- `04-next-calls-and-write-prep.md`: search-to-read handoff, schema-valid recommendations, `resolve_symbol_range` write-prep ergonomics.
- `05-actionable-errors-and-compact-output.md`: repair hints for common failures and noise reduction without proof loss.
- `06-docs-tests-runtime-review.md`: docs/schema/server metadata, tests, review handoff, rebuild and restart.

## Global Decisions

1. Default literal output is a deliberate AX correction.
2. `read_file` remains literal-only and does not gain `redaction_mode`.
3. `read_files` accepts `redaction_mode=off|strict|auto`; omitted/default is `off`.
4. `auto` maps to repaired `strict` and is documented as deprecated/not recommended for normal AX.
5. Redaction policy-forced mode is represented by output metadata, not hidden behavior.
6. Existing byte budgets stay byte budgets; truncation is valid UTF-8 and grapheme-aware where feasible.
7. `output_profile="agent"` is the new default for `outline_file`; legacy `outline` aliases to `agent`.
8. `output_profile="full"` returns leaf-heavy JSON/YAML output.
9. `output_profile="fingerprint_only"` keeps existing metadata-only behavior.
10. `workspace_inventory` does not infer file paths.
11. `resolve_symbol_range` can prepare writes only when source selector/range and target intent are explicit.
12. No new mutation tool is added in Phase 7.

## Global Acceptance

Implementation is accepted only when:

- Default `grep`, `read_files`, diff preview and read-back do not redact benign literal content.
- `strict`/`auto` redaction does not mask paths, filenames, Go module paths, import paths, identifiers, JSON/YAML key names or option names such as `max_output_tokens`.
- Diff headers and target/source labels are never replaced by `[REDACTED]`.
- Unicode truncation tests produce valid UTF-8 and no U+FFFD for Cyrillic, combining marks and emoji sequences.
- JSON/YAML `output_profile="agent"` omits low-value leaf noise by default and reports omitted leaf counts.
- JSON/YAML `output_profile="full"` returns leaf/value items.
- JSON/YAML paths are exact and distinct for keys with `:`, dots, spaces and Unicode.
- `grep` recommended read/outline inputs validate against schemas and require no manual path/range reconstruction for canonical cases.
- `glob_file_search` bounded result recommendations validate against schemas and do not over-batch noisy results.
- `glob_file_search` returns a schema-valid `outline_file` recommendation for exactly one source/config-like result by the Phase 7 extension/type classification, cwd-aware and without manual path reconstruction; broad/truncated results recommend narrowing instead.
- `workspace_inventory` recommendations remain directory-level and do not guess file paths.
- `resolve_symbol_range(target_intent)` returns dry-run-ready write inputs with source fingerprint and concrete ranges when target intent is explicit and safe.
- Existing/missing target precondition preparation is tested.
- Stale fingerprint, out-of-range, ambiguous selector, target mismatch and invalid profile errors return actionable hints.
- `read_file` and `read_files` default outputs match for identical ranges.
- Cwd projection remains correct for every changed recommended input and output path field.
- Registered public tool list remains exactly the existing 14 tools; no new public tool/schema name is added.

## Global Checks

Expected verification:

- PowerShell: `$env:GOFLAGS='-mod=vendor'; $env:GOPROXY='off'; $env:CGO_ENABLED='0'; go test -count=1 ./filetoolsserver/handler -run "Redact|ReadFiles|ReadFile|Unicode|Preview|Outline|JSON|YAML|ResolveSymbol|Recommended|Cwd|Schema|Error"`
- PowerShell: `$env:GOFLAGS='-mod=vendor'; $env:GOPROXY='off'; $env:CGO_ENABLED='0'; go test -count=1 ./...`
- PowerShell: `$env:GOFLAGS='-mod=vendor'; $env:GOPROXY='off'; $env:CGO_ENABLED='1'; go test -race -count=1 ./filetoolsserver/handler -run "Read|Preview|Outline|Resolve|Recommended|Cwd|Error"`
- PowerShell: `$env:GOFLAGS='-mod=vendor'; $env:GOPROXY='off'; $env:CGO_ENABLED='0'; go build -trimpath -buildvcs=false -o .\mcp-file-tools.exe .\cmd\mcp-file-tools`
- Manual schema/docs check: `README.md`, `TOOLS.md`, `server.json`, `filetoolsserver/server.go`.
- Runtime smoke after rebuild/restart for literal `read_files`, strict redaction, Unicode preview, JSON/YAML outline profile and one recommended next call.

## Stop And Ask If

- A proposed implementation requires automatic redaction to remain default.
- `workspace_inventory` needs guessed file-specific recommendations to satisfy a test.
- JSON/YAML compact output would remove exact key navigation.
- Write preparation would infer target paths or mutate files.
- Compatibility constraints prevent accepting `off` as an explicit redaction value.
