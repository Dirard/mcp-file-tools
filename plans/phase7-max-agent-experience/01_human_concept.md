# Phase 7 Max Agent Experience Concept

concept_version_label: phase7-max-agent-experience-v1
status: clean_product_owner_reviewed_ready_for_srs

## Goal

Довести `mcp file tools` до максимально практичного agent experience: меньше шума, меньше ручной сборки JSON, больше фактической пользы в каждом ответе.

Phase 7 исправляет главный drift предыдущих фаз: часть поведения была оптимизирована под защитное скрытие данных, а пользовательская цель была другой - агент должен видеть точные, полезные, неиспорченные данные и быстрее переходить от "нашел" к "прочитал", "понял" и "безопасно применил".

Цель не в том, чтобы добавить еще больше функций ради функций. Цель в том, чтобы существующие 14 tools стали удобнее именно для агента:

- outputs не должны портить пути, identifiers, module names, benign config keys и diff evidence;
- previews не должны ломать Unicode;
- JSON/YAML outline должен давать сигнал, а не поток однотипных leaf items;
- write workflows должны требовать меньше ручного заполнения fingerprints, ranges и target preconditions;
- next recommended calls должны быть ближе к готовому следующему действию.

## User / Scenario

Основной пользователь - coding agent, который работает с локальным repo через MCP tools.

Типичный сценарий:

1. Агент ищет место через `grep`, `glob_file_search` или `workspace_inventory`.
2. Агент читает один или несколько файлов через `read_file` / `read_files`.
3. Агент получает structure через `outline_file` и/или `resolve_symbol_range`.
4. Агент готовит `copy_ranges`, `move_ranges` или batch write.
5. Агент смотрит preview, применяет change, проверяет read-back.

В Phase 7 этот цикл должен ощущаться легче:

- меньше false-positive redaction;
- меньше повторяющихся metadata;
- меньше ручного переноса line ranges;
- меньше "надо перечитать руками, потому что output может быть испорчен";
- больше готовых маленьких JSON-заготовок для следующего tool call.

## What 10/10 AX Means

Phase 7 считается удачной, когда агенту удобнее работать с tools, чем с raw shell, не только из-за безопасности, но из-за практической скорости и точности.

10/10 AX означает:

1. Default output fidelity: если агент просит файл, diff, preview или outline, tool показывает фактические данные, а не маскирует нормальные пути/keys/identifiers.
2. Redaction не является default agent workflow. Если redaction остается, она должна быть явным opt-in режимом или отдельной server policy, а не скрытым источником шума.
3. Никакой preview/truncation не создает `�` или битый UTF-8.
4. JSON/YAML outline по умолчанию показывает полезную структуру и key paths, а leaf noise отдает только по запросу или narrow-фильтрам.
5. Keys с `:`, пробелами, точками, bracket-like chars и Unicode сохраняют точную path identity.
6. `resolve_symbol_range` и write-related hints дают агенту готовые dry-run inputs настолько полно, насколько можно без mutation.
7. `read_files` не ведет себя неожиданно более "редактирующе", чем `read_file`; batch read должен быть удобным batch read, не отдельной safety-first поверхностью.
8. Output verbosity управляемый: compact by default для agent navigation, full/proof-heavy режимы доступны явно.
9. Errors and refusals говорят, что делать дальше, а не только почему tool отказал.
10. Existing cwd/slash path behavior остается стабильным.

## Scope

### C-001: Redaction Default Repair

Phase 7 должна убрать automatic redaction из обычного agent path.

Required direction:

- omitted/default redaction mode is literal `off` for normal tool outputs;
- explicit `redaction_mode="strict"` performs value-only redaction for strong secret-like content;
- legacy `redaction_mode="auto"` remains accepted but becomes a deprecated alias for repaired `strict`, not the documented agent default;
- paths, diff headers, filenames, module paths, package import paths, identifiers, config key names and numeric option names are never masked as secrets;
- if a deployment-level policy forces redaction, output includes `redaction_policy.forced=true` and still preserves navigation-critical fields where possible;
- docs and schemas must stop teaching agents to expect redaction as default.

This is a product correction, not a cosmetic change. A preview that masks `target.md` as `[REDACTED].md` is bad AX.

### C-002: Unicode-Safe Preview And Truncation

Every content-bearing truncation must be Unicode-safe.

Affected surfaces include:

- `diff_previews[].text`;
- `boundary_preview`;
- read-back snippets;
- grep context snippets if truncation applies;
- error/warning snippets that quote content;
- any future preview field.

Unacceptable: `�` appears because a UTF-8 sequence was cut by byte budget.

### C-003: JSON/YAML Outline Signal-To-Noise

JSON/YAML outline should be useful for navigation, not noisy by default.

Required direction:

- default output emphasizes document, object/mapping, array/sequence and meaningful key paths;
- leaf `value` items are opt-in or shown only when filters make them clearly relevant;
- use one public profile vocabulary: `output_profile="agent"` by default, `output_profile="full"` for leaf-heavy output, `output_profile="fingerprint_only"` for metadata-only output, with legacy `outline` accepted as `agent`;
- exact leaf override rules: `full` always includes leaves; `agent` omits leaves unless `kinds` explicitly includes leaf/value kinds or `line_window` / `name_contains` directly matches them;
- path names preserve exact key identity, including keys with `:`, dots, spaces and Unicode;
- canonical path grammar is `document.services[0]["api:key"]`: dot notation only for simple identifier keys, bracket notation with JSON string escaping for ambiguous keys, `[index]` for sequences, and `document[0]` prefix for multi-document YAML;
- output explains why config nodes are read-safe but usually not move/delete-safe;
- refusal should recommend useful next actions, such as read exact range or future replace-value flow.

### C-004: Write Workflow Ergonomics

Write tools are powerful but still too heavy to prepare manually.

Phase 7 should reduce boilerplate:

- `resolve_symbol_range` should provide fuller ready-to-dry-run inputs for `copy_ranges` / `move_ranges` when possible;
- write preparation only happens when source selector/range and target intent are explicit;
- `resolve_symbol_range` must not infer target paths;
- source fingerprint and concrete ranges should be carried automatically in recommendations;
- target preconditions should be prepared or clearly recommended through one next call;
- target missing/existing cases should produce direct dry-run-ready variants where safe;
- manual range workflows use `resolve_symbol_range` with an explicit range selector plus `target_intent`; no new public helper tool is required for concept.

Mutation remains explicit. Phase 7 improves preparation; it does not make hidden automatic writes.

### C-005: Compact Output Profiles

Agents need proof, but repeated proof can become noise.

Required direction:

- keep fingerprints and range proofs available;
- reduce repeated fingerprints in nested outline items where a shared file-level proof and selector metadata are enough;
- add or improve compact/full output profiles where useful;
- default should favor agent navigation clarity without removing write-critical data.

### C-006: Read Consistency

`read_file` and `read_files` should feel like single and batch forms of the same idea.

Required direction:

- exact content by default;
- same redaction semantics;
- same coverage vocabulary;
- same Unicode display behavior;
- batch output remains compact but does not surprise the agent by silently treating config-like files differently.

Contract clarification: `read_file` remains literal-only and does not need a `redaction_mode` input. `read_files` keeps redaction controls for compatibility, but default/omitted behavior is literal `off`, so both tools return the same requested lines by default.

### C-007: Better Next Calls

The tools should not only report facts; they should help the agent continue.

Next calls should be:

- small;
- cwd-aware;
- dry-run by default for writes;
- filled with known fingerprints/ranges/preconditions when available;
- honest when one more read/outline call is required.

Search and discovery handoff is in scope:

- `grep` should provide ready `read_file`, `read_files` and/or `outline_file` next calls for matched files/ranges where possible;
- `glob_file_search` should provide ready `read_files` or `outline_file` next calls for selected results when the result set is bounded enough;
- `workspace_inventory` should provide directory-level narrowing calls such as `glob_file_search` / continued `workspace_inventory`, not direct file-specific `read_files` / `outline_file` calls unless an exact file path already exists in its own output contract;
- all recommended inputs must be schema-valid, cwd-aware and range-aware when range information exists.
- no recommended read/outline input may contain inferred or guessed file paths.

### C-008: Actionable Failure UX

10/10 AX includes refusal paths.

Common failures should return the smallest useful next action:

- stale fingerprint -> refresh `outline_file` / `inspect_path` input;
- out-of-range -> bounded `read_file` / `outline_file` lookup;
- ambiguous selector -> narrowed selector examples;
- target precondition mismatch -> target refresh input;
- invalid profile/options -> valid enum values and corrected minimal input;
- JSON/YAML write-unsafe node -> exact read recommendation and explanation of delimiter/indent reason.

## Out Of Scope

- No full LSP.
- No project-wide semantic index.
- No cross-file rename.
- No formatter integration.
- No automatic mutation from a read-only helper.
- No automatic cleanup/delete.
- No broad structural edit engine.
- No new safety-first redaction policy as a product goal.

## Must Not Break

- Existing 14 tool names remain available.
- `cwd_id` path projection remains stable.
- Without `cwd_id`, paths remain slash-normalized absolute outputs.
- With `cwd_id`, path outputs remain cwd-relative except `cwd`.
- `copy_ranges` / `move_ranges` mutation still requires explicit call and fingerprints.
- Dry-run stays non-mutating.
- Phase 4 `grep` navigation value remains.
- Go/Markdown outline remains useful.
- JSON/YAML parser honesty remains; do not pretend unsafe config moves are safe.
- Tests must not contain or print real secrets.

## Success

Phase 7 succeeds when a practical agent review gives a higher AX score because:

1. benign module paths, config keys and target paths are no longer masked;
2. previews are readable in Cyrillic/Unicode;
3. JSON/YAML outline is compact and navigable;
4. key paths are exact;
5. write recommendations require less manual JSON assembly;
6. batch read behaves like expected batch read;
7. outputs are shorter without losing important next-action proof.

## Canonical AX Workflows

The SRS must make these workflows measurable:

1. Search -> read: a `grep` match can become a schema-valid `read_file` or `read_files` call without manual path/range reconstruction.
2. Search -> structure: a bounded `grep` or `glob_file_search` result can become a schema-valid `outline_file` call without manual path reconstruction.
3. Read parity: `read_file` and `read_files` return the same literal lines by default for a config-like file.
4. Symbol/section -> write dry-run: `outline_file` or `resolve_symbol_range` can produce a dry-run-ready `copy_ranges` / `move_ranges` input with source fingerprint and concrete ranges.
5. JSON/YAML navigation: default `output_profile="agent"` is materially smaller/noise-lower than `full`, while preserving exact key paths.
6. Preview fidelity: Cyrillic/Unicode diff and boundary previews never contain `�` and never mask target paths by default.

Suggested proof targets for SRS:

- no false redaction for benign module paths, filenames, identifiers or config keys;
- zero `�` from truncation tests;
- recommended next inputs validate against tool schemas;
- JSON/YAML default output omits low-value leaf noise and reports omitted counts;
- batch read default equals single read for the same requested ranges.

## Unacceptable Result

The result is unacceptable if:

- redaction still corrupts normal agent evidence by default;
- `�` still appears in truncated previews;
- JSON/YAML outline remains a flood of low-value `value` items by default;
- keys with `:` or Unicode lose exact identity;
- write tools still force the agent to manually copy basic fingerprints/ranges when the tool already knows them;
- compact output hides information required for the next safe dry-run;
- Phase 7 adds another large feature but leaves the core workflow awkward.

## Open Questions

None for concept. Defaults chosen here:

- Default agent workflow prioritizes literal, exact output.
- Redaction is explicit opt-in or deployment policy, not default AX behavior.
- Mutation remains explicit and fingerprint-gated.
