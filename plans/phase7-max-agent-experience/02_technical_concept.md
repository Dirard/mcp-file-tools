# Phase 7 Max Agent Experience Technical Concept

concept_version_label: phase7-max-agent-experience-v1
status: clean_product_owner_reviewed_ready_for_srs

## Technical Direction

Phase 7 is an ergonomics repair and polish phase. It should improve the existing tool surface rather than add a broad new platform.

The key technical rule: preserve exactness and next-action usefulness by default.

Implementation should focus on five core areas plus two cross-cutting AX repairs:

1. redaction default and false-positive repair;
2. Unicode-safe truncation across previews/snippets;
3. JSON/YAML outline profiles and exact path naming;
4. lower-boilerplate write preparation;
5. compact output profiles for agent navigation.
6. search-to-read/discovery handoff without guessed paths;
7. actionable failure UX for common repairable errors.

## Current Baseline

Current strengths:

- `set_cwd` makes paths short and stable for agents.
- `grep` gives grouped matches and `read_ranges`.
- `read_file` / `read_files` return coverage and continuation.
- `outline_file` supports Go, Markdown, JS/TS/TSX/JSX, Python, JSON, YAML, Svelte and generic text.
- `resolve_symbol_range` resolves selectors and can recommend dry-run writes.
- write tools provide fingerprints, diff previews, boundary previews, validation, backups and partial-state recovery.

Current AX defects to address:

- redaction can mask benign paths, module names and config-like keys;
- diff/boundary preview can become less trustworthy when redaction touches path-like evidence;
- byte-budget truncation can cut UTF-8 and show replacement chars;
- JSON/YAML outline can be too noisy by default;
- JSON/YAML key path formatting can lose exact identity for keys containing punctuation;
- write-tool preparation still requires too much manual JSON assembly.

## Redaction Contract

Phase 7 should change the public default:

- omitted redaction means no content redaction in normal agent workflow;
- add `redaction_mode="off"` as the explicit default value;
- keep `redaction_mode="strict"` as explicit opt-in;
- keep existing `redaction_mode="auto"` accepted as a deprecated alias for repaired `strict`.

Final mode contract:

- `off`: literal output;
- `strict`: redact only content values that match strong secret patterns;
- `auto`: accepted legacy alias for `strict`, documented as not recommended for normal AX.

Hard constraints:

- never redact filesystem paths, diff header paths, filenames, module/package/import paths, symbol names, identifiers, JSON/YAML key names, or option names;
- if strict redaction touches content, preserve enough surrounding structure for navigation;
- redaction must run after path projection decisions, not before, so it cannot corrupt cwd-relative paths;
- if server policy forces redaction despite omitted/`off`, output includes `redaction_policy.forced=true`, `redaction_policy.mode=<effective mode>` and per-surface `redacted=true`;
- docs examples should not show `redaction_mode="auto"` as normal usage.

## Unicode-Safe Truncation

Introduce or centralize a helper for display-budget truncation that never splits UTF-8 sequences or display clusters when feasible.

Required behavior:

- all existing per-surface budgets remain byte budgets for compatibility;
- the shared helper truncates on grapheme cluster boundaries where feasible and always returns valid UTF-8;
- the truncation marker counts inside the configured byte budget when there is enough room;
- if a full grapheme cluster cannot fit, the helper falls back to the largest valid UTF-8 prefix only when needed, never a partial rune;
- never emits U+FFFD because of truncation;
- tests cover Cyrillic, combining marks, variation selectors, skin-tone modifiers and ZWJ emoji sequences;
- is used by diff preview, boundary preview, read-back, grep snippets and warning/error snippets.

SRS should identify current truncation helpers and replace byte slicing at content boundaries.

## JSON/YAML Outline Profiles

Use one profile model for outlines.

Public input:

```json
{
  "output_profile": "agent"
}
```

Preferred behavior:

- `output_profile="agent"` is the default and returns structural nodes plus meaningful key paths;
- `output_profile="full"` includes all property/value/leaf nodes;
- `output_profile="fingerprint_only"` keeps the existing metadata-only behavior;
- legacy `output_profile="outline"` remains accepted as an alias for `agent`;
- exact override rules:
  - `full` always includes leaves;
  - `agent` omits leaves by default;
  - `agent` includes leaves when `kinds` explicitly includes leaf/value kinds;
  - `agent` includes leaves when `line_window` intersects a leaf or `name_contains` directly matches a leaf path/name;
- `outline_stats` reports hidden/omitted leaf count;
- next recommended call suggests `output_profile="full"` or narrow filters when leaf detail is needed.

Path naming:

- use `document` as the root prefix for JSON and YAML;
- use `document[0]`, `document[1]` for multi-document YAML;
- use dot notation only for simple identifier keys;
- use bracket notation for ambiguous keys: `document["foo:bar"]`, `document.services[0]["api:key"]`;
- preserve Unicode and punctuation literally in JSON string-escaped form;
- never collapse different keys into the same display path.

Write-safety:

- keep JSON/YAML move/delete conservative;
- distinguish `read_safe`, `replace_value_safe` if support exists or is planned, and `move_delete_safe`;
- current Phase 7 does not need to implement replace-value mutation unless SRS proves it is small and high-value.

## Write Preparation Ergonomics

Improve existing read-only preparation before adding new mutation tools.

Preferred direction:

1. Enhance `resolve_symbol_range(target_intent)` to return fuller write preparation:
   - `source_file`;
   - `source_fingerprint`;
   - concrete `ranges`;
   - target file;
   - target precondition when target exists;
   - `must_not_exist` variant when target is missing and requested placement is create-new;
   - placement and joiner defaults;
   - `dry_run=true`.
2. Use `resolve_symbol_range` for manual range preparation through an explicit range selector plus `target_intent`; do not add a new public helper tool for concept.
3. Do not make helpers mutate files.

Boundaries:

- source selector/range must be explicit;
- target intent must include explicit target file and operation;
- target paths must never be inferred from symbol names;
- if target precondition cannot be prepared, return the exact `inspect_path` or `outline_file` next call needed.

The goal is that an agent can copy a recommended input directly into `copy_ranges` / `move_ranges` dry-run with minimal edits.

## Compact Output Profiles

Reduce noise without removing required proof.

Candidate approach:

- keep existing full output available;
- add compact defaults for nested outline items where repeated `range_fingerprint` and selector fields are verbose;
- expose shared fingerprint context at file level;
- keep per-item selector enough to resolve or read the item;
- if write preparation needs more proof, provide it in `resolve_symbol_range` or recommended write input rather than repeating it everywhere.

Do not remove fields from existing full/default output until SRS checks backward compatibility. If changing defaults is worth it for AX, the plan must say so explicitly and tests must pin the new behavior.

## Read Consistency

`read_files` should be a compact batch version of `read_file`.

Required technical behavior:

- default literal output;
- `read_file` remains literal-only and does not gain a `redaction_mode` input;
- `read_files` accepts `off|strict|auto` for compatibility, with omitted/default `off`;
- same Unicode-safe line display;
- same coverage fields;
- per-item redaction/coverage/fingerprint fields remain available;
- no config/log hidden special case unless the caller explicitly requested strict redaction or server policy forces it.

## Docs And Schema Direction

Docs should teach the simplest high-value agent path:

1. Use `set_cwd`.
2. Use `grep` / `glob_file_search`.
3. Use `read_file` / `read_files` for literal context.
4. Use `outline_file` compact structure.
5. Use `resolve_symbol_range` to prepare concrete ranges.
6. Use recommended dry-run write input.
7. Apply only after preview.

Docs should stop teaching redaction as normal AX.

Schema updates should:

- include `off` if redaction mode becomes explicit;
- keep old `auto` accepted as deprecated strict alias;
- document default clearly;
- ensure examples use minimal useful inputs.

## Search-To-Read Handoff

Discovery tools should prepare practical next calls:

- `grep` content matches include schema-valid `read_file`/`read_files` inputs using grouped ranges and cwd-projected paths;
- dense or language-relevant grep results include schema-valid `outline_file` inputs;
- `glob_file_search` can recommend `read_files` for bounded result sets and `outline_file` for source-like files;
- `workspace_inventory` stays directory-level: it recommends `glob_file_search`, continued `workspace_inventory`, or other directory narrowing calls, not direct `read_files` / `outline_file` calls with guessed file paths;
- direct file-specific read/outline recommendations are allowed only from tools that already output exact file paths, such as `grep` and `glob_file_search`;
- recommended inputs must not include absolute paths under `cwd_id`;
- if a result is too broad, return a narrowing recommendation instead of a noisy batch read.

## Actionable Failure UX

Structured failures should include repair-oriented hints when the next action is deterministic:

- stale source fingerprint -> `outline_file` refresh input;
- target fingerprint mismatch -> target `outline_file` or `inspect_path` refresh input;
- out-of-range -> `read_file`/`outline_file` bounded lookup;
- ambiguous selector -> narrowed selector examples using kind/name/path/range;
- invalid profile/enum -> valid enum list and corrected minimal input shape;
- JSON/YAML write-unsafe node -> exact read recommendation and write-safety reason.

The SRS should enumerate which error codes get hints and keep the payload small.

## Testing Direction

SRS should require tests for:

- no redaction by default in `grep`, `read_files`, write diff preview and read-back;
- strict redaction does not mask paths, filenames, module paths, identifiers or keys;
- benign strings such as Go module paths and `max_output_tokens` are not redacted;
- Unicode truncation never emits replacement chars;
- boundary/diff preview truncates Cyrillic correctly;
- JSON/YAML default outline omits low-value leaf noise and reports omitted count;
- JSON/YAML full profile returns leaf nodes;
- JSON/YAML keys with `:`, dots, spaces and Unicode have exact distinct paths;
- `resolve_symbol_range(target_intent)` returns dry-run-ready write inputs with source fingerprint and ranges;
- target precondition preparation for existing and missing target files;
- `grep` / `glob_file_search` / `workspace_inventory` recommended read/outline inputs validate against schemas;
- `workspace_inventory` never emits guessed file-specific read/outline recommendations;
- stale fingerprint, out-of-range, ambiguous selector, target precondition mismatch and invalid profile errors return actionable hints;
- `read_file` and `read_files` default outputs match for identical ranges;
- cwd projection for every new or changed recommended input field;
- docs/schema examples reflect literal default output.

## Risks

- Changing redaction default can break tests and clients that expected automatic redaction. Phase 7 accepts this as a deliberate AX correction, but SRS must preserve explicit strict mode.
- Compact output can accidentally hide proof needed for writes. Mitigation: keep write preparation proof in `resolve_symbol_range`.
- JSON/YAML path formatting can get complex. Mitigation: define one escaping strategy and test ambiguous keys.
- Unicode-safe truncation can change byte budgets slightly. Mitigation: prefer valid display evidence over exact byte-count clipping.

## Stop And Ask If

- A proposed implementation requires automatic redaction to remain default for normal tool use.
- JSON/YAML compacting would remove navigation paths the agent needs.
- Write preparation would mutate files or infer target paths without explicit user/tool input.
- Compatibility constraints prevent adding an explicit `off` redaction mode.
