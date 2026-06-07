# Phase 6 Language-Aware Outline And Symbol Operations Concept

concept_version_label: phase6-language-outline-symbol-ops-v1
status: clean_srs_reviewed_ready_for_implementation

## Goal

Сделать `outline_file` и связанные write workflows structure-aware для языков, с которыми coding agents реально работают: Go/Markdown уже есть, Phase 6 добавляет TypeScript/JavaScript/React, Svelte, Python, JSON и YAML.

Главная идея: агент должен уметь перейти от "строки 120-180" к "функция LoadConfig", "React component SettingsPanel", "YAML path services.api", "Python class Client" или "Svelte script block" без ручного line-number переноса.

Phase 6 не должна превращать file-tools в language server или AST rewrite платформу. Она должна дать честные structure ranges, selector resolution и symbol-aware copy/move, которые все еще опираются на fingerprints, diff preview и validation из Phase 5.

## User / Scenario

Основной пользователь - coding agent, который делает:

- code review;
- поиск и перенос функций/классов/sections;
- разбор frontend/backend mixed repos;
- точечные single-file refactors;
- documentation/config refactors;
- безопасные edits после `grep`.

Типичный сценарий:

1. Агент находит файл через `grep` или `glob_file_search`.
2. Агент вызывает `outline_file`.
3. Tool возвращает language-aware symbols/sections с ranges и confidence.
4. Агент выбирает symbol/range без ручного подсчета строк.
5. Агент делает read/copy/move/replace через symbol selector.
6. Write preview/validation идет через Phase 5 safety layer.

## What 10/10 Means

Phase 6 считается удачной, когда:

1. `outline_file` дает полезный structure map не только для Go/Markdown.
2. Каждый parser честно сообщает `parser_status`, `parser_scope`, confidence и fallback.
3. JSON/YAML config files становятся navigable по object/path ranges.
4. TypeScript/React/Svelte/Python files показывают imports, exports, classes, functions, methods and component/script blocks where possible.
5. Агент может ссылаться на outline item / symbol selector вместо ручного переноса line ranges.
6. Symbol-based copy/move работает только при unambiguous symbol и matching fingerprint.
7. Full structural edits are explicitly not the core deliverable; Phase 6 may only prepare a gated contract for them after parser and symbol-range behavior is proven.
8. Tool never pretends to understand semantics it did not parse.

## Phase Relationship

Phase 6 depends on Phase 5 for safe write UX:

- diff preview;
- joiner/boundary clarity;
- post-write read-back;
- backup discovery;
- specific error codes;
- cwd path projection.

If Phase 5 is not implemented yet, Phase 6 concept still stands, but implementation of symbol writes should wait for Phase 5 safety contracts or include equivalent safeguards.

## Scope

### C-001: Preserve Existing Go/Markdown/Generic Behavior

Existing `outline_file` behavior remains:

- Go parser uses Go AST declarations;
- Markdown parser uses ATX headings and ignores fenced headings;
- generic text outline exists for non-Go/non-Markdown text;
- binary/undecodable files do not get fake structure;
- fingerprints remain present;
- `line_window`, `name_contains`, `kinds`, `max_items`, `max_depth`, `output_profile` remain useful;
- cwd-aware output projection remains unchanged.

Phase 6 adds language support; it does not remove generic fallback.

### C-002: Language Support Tiers

Phase 6 should support these language families, but with honest tiers:

- TypeScript / JavaScript / TSX / JSX / React;
- Svelte;
- Python;
- JSON;
- YAML;
- existing Go;
- existing Markdown.

Not every language must expose the same symbol kinds. A useful exact parser for JSON/YAML paths is better than shallow pretend-symbols. A Svelte parser may start with exact top-level blocks plus script symbols if available.

Each output must make its parser confidence visible.

### C-003: Normalized Outline Items

Outline items should stay normalized across languages:

- `kind`;
- `name`;
- `detail`;
- `range`;
- `path`;
- `confidence`;
- `range_is_estimated`;
- `range_fingerprint`;
- `metadata`.

Phase 6 may add a stable `symbol_ref` / selector payload, but it must remain tied to file fingerprint and parser output. It is not a permanent project-wide symbol ID.

### C-004: Useful Language Structures

Expected useful structures:

For TypeScript / JavaScript / React:

- import/export blocks;
- functions;
- classes;
- methods;
- interfaces/types where applicable;
- top-level constants/variables when likely component or exported symbol;
- React function/class components where parser can detect them honestly.

For Svelte:

- module script block;
- instance script block;
- style block;
- major markup/template sections where parser can represent them safely;
- script symbols if using a nested JS/TS parser.

For Python:

- imports;
- functions;
- classes;
- methods;
- decorators as metadata where useful.

For JSON:

- object keys and array paths;
- path ranges for top-level and nested values;
- parser error location.

For YAML:

- mapping keys and sequence paths;
- document boundaries;
- anchors/aliases as metadata if available;
- parser error location.

### C-005: Symbol Selectors For Read/Copy/Move

Phase 6 should reduce manual range entry by allowing agents to resolve outline items into ranges.

Possible product shape:

- `outline_file` returns `symbol_ref` or selector-ready metadata;
- new helper resolves symbol selectors to exact ranges;
- core workflow gives ready dry-run inputs for single `copy_ranges` / `move_ranges` after fingerprint verification;
- batch symbol workflows are future-gated unless a later SRS makes them bounded and equally safe;
- outputs still show final concrete line ranges.

Important: symbol selectors are convenience and safety. The underlying write must still be explicit, fingerprinted and previewed.

### C-006: Structural Edit Is Gated, Not Core

Phase 6 should not mix a new language parser pack with a broad structural edit engine.

Core Phase 6 deliverables:

- language-aware outline;
- exact symbol/range selectors;
- selector-to-range resolution;
- symbol-aware copy/move through existing range transfer safety.

Structural edit may be documented as a later gated stage, but should not be implemented as part of Phase 6 unless root/user explicitly approves a narrower follow-up after parser acceptance.

Allowed later direction:

- single-file operations;
- exact parser-supported languages only;
- unambiguous selector required;
- fingerprint required;
- diff preview before mutation;
- post-write validation/read-back;
- no cross-file semantic rename;
- no type-aware rewrite.

Candidate operations:

- replace symbol body/range;
- insert import in parser-known import region;
- insert sibling before/after a selected symbol;
- replace JSON/YAML value by path.

For Phase 6 planning, these are non-core unless explicitly promoted by root/user after review.

### C-007: Parser Honesty And Fallback

No fake semantics.

If a parser fails or only partially understands a file:

- `parser_status` must say so;
- warnings must be actionable;
- generic fallback may be used, but confidence must be lower;
- symbol-based operations must refuse estimated or ambiguous ranges unless explicitly allowed by a safe non-structural path.

### C-008: Agent Navigation After Grep

Phase 4 `grep` already recommends `outline_file` when structure is useful. Phase 6 should make that recommendation much more valuable:

- `grep` finds a line in TS/Python/YAML;
- `outline_file` returns enclosing function/class/key path;
- agent can read or move the enclosing symbol, not just the matching line.

Phase 6 should include enclosing-symbol lookup via `line_window` or a similar selector if SRS confirms the best API shape.

## Out Of Scope

- No full LSP.
- No project-wide semantic index.
- No embeddings.
- No cross-file rename.
- No dependency graph or build graph resolution.
- No type checking.
- No guarantee that all language constructs are represented.
- No AST rewrite for every supported language.
- No structural edit implementation as a default Phase 6 deliverable.
- No formatter integration unless SRS proves it is small and safe.
- No automatic large refactor.
- No hidden/default path behavior changes.

## Must Not Break

- Go and Markdown outline remain exact where they are exact today.
- Generic fallback remains available and honest.
- Existing write tools remain line/fingerprint-safe even when symbol selectors are added.
- Phase 5 diff/validation/backup behavior applies to any symbol-based write.
- Cwd-aware path projection applies to all new fields and recommended inputs.
- Parser failures do not crash tools or produce misleading ranges.

## Success

Phase 6 succeeds when an agent can:

1. Open `outline_file` for Go, Markdown, TS/React, Svelte, Python, JSON and YAML files.
2. See useful language-appropriate symbols/sections with ranges and confidence.
3. Identify the enclosing symbol around a known line.
4. Copy/move a function/class/section/config node by selector or safely resolved range.
5. Preview and validate any symbol-based write through Phase 5 write safety.
6. Get clear refusal when a parser cannot provide exact structure.

## Unacceptable Result

The result is unacceptable if:

- TS/Python/Svelte/JSON/YAML outline is just regex chunks labeled as exact;
- parser failures silently degrade into fake symbol ranges;
- symbol selectors can apply to a changed file without fingerprint checks;
- structural edit can mutate files without diff preview and validation;
- Phase 6 breaks existing Go/Markdown output;
- cwd mode leaks absolute paths in new selector/recommended fields;
- implementation tries to become full LSP and stalls the practical agent workflow.

## Open Questions

None for concept. Defaults chosen here:

- Phase 6 prioritizes language-aware outline, selector resolution and symbol-aware copy/move.
- Structural edit is a gated later stage, not the default Phase 6 core.
- Full LSP semantics and cross-file refactors are explicitly out of scope.
