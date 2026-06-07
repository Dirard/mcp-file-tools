# Phase 4 Grep Agent Navigation Concept

concept_version_label: phase4-grep-agent-navigation-v1
status: clean_product_owner_approved

## Goal

Сделать `grep` инструментом уровня 10/10 для coding agents: не просто найти строки как `rg`, а сразу дать агенту надежную навигацию к следующему действию.

Фаза 4 должна сделать `grep` полезнее, чем raw `rg`, именно для агента:

- агент не парсит terminal text руками;
- агент видит, насколько поиск полный;
- агент получает сгруппированные файлы и ranges для `read_file`;
- агент получает один понятный следующий вызов, когда он очевиден;
- агент меньше ошибается с regex escaping;
- агент не тонет в повторяющихся матчах одного файла.

`grep` не должен обещать быть быстрее `rg`, поддерживать все флаги `rg` или становиться language server. Победа над `rg` здесь измеряется агентским outcome: меньше лишних вызовов, меньше токенов, больше source anchors и меньше ложной уверенности.

## User / Scenario

Основной пользователь - coding agent, который делает repo review, debugging, implementation или refactor и ищет:

- определение символа;
- usages функции, типа, поля, config key или error string;
- подозрительные TODO/errors;
- файлы с похожими строками;
- места, которые надо затем открыть через `read_file` или структурно понять через `outline_file`.

Человек не должен помогать выбирать мелкие UX-решения. Tool должен сам давать агенту достаточно структуры, чтобы агент мог продолжать работу уверенно.

## What 10/10 Means

`grep` 10/10 означает:

1. Агент за один вызов получает не только raw matches, но и понятную карту результата.
2. Если результата слишком много, tool честно говорит, что именно неполно и как сузить поиск.
3. Если результат полезный, tool дает готовые ranges для `read_file`.
4. Если regex не нужен, агент может использовать literal mode и не думать об escaping.
5. Повторяющиеся совпадения группируются по файлам, чтобы первый шумный файл не скрывал всю картину.
6. Cwd-aware режим Phase 3 сохраняется: все пути и recommended inputs уважают `cwd_id`.

## Scope

### C-001: Preserve Current Grep Behavior

Текущие режимы остаются:

- `content`;
- `files_with_matches`;
- `count`.

Существующие поля остаются совместимыми:

- `matches`;
- `files`;
- `counts`;
- `pattern`;
- `path`;
- `match_count`;
- `row_count`;
- `truncated`;
- `dot_entries_skipped`;
- `cwd_id` / `cwd` поведение.

Фаза 4 добавляет новые поля и options, но не ломает старые agent workflows.

### C-002: Literal Pattern Mode

Добавить явный режим паттерна:

```json
{
  "pattern_mode": "literal"
}
```

Поддерживаемые значения:

- `regex` - текущий Go regexp режим, default;
- `literal` - искать строку буквально, без необходимости экранировать `(`, `{`, `.`, `*`, `\` и похожие символы.

Это один из главных UX wins против `rg`/terminal: агенту не надо вспоминать escaping для точного поиска `interface{}`, `foo.bar`, `path\to\file`, `functionCall(`.

### C-003: Search Stats And Completeness

Output должен явно показывать, насколько результат полон.

Добавить `search_stats`, например:

```json
{
  "search_stats": {
    "files_seen": 120,
    "files_searched": 74,
    "files_with_matches": 6,
    "skipped_hidden": 3,
    "skipped_ignored": 12,
    "skipped_binary": 4,
    "skipped_type_or_glob": 29,
    "completed": false,
    "stop_reason": "limit",
    "counts_are_complete": false
  }
}
```

Exact field names can be settled in SRS, but concept requirement is stable:

- no-match must be distinguishable from "not fully searched";
- truncated result must explain why it stopped;
- count fields must not imply full-repo truth when the scan stopped early.

### C-004: File Groups For Navigation

Add `file_groups[]` as a compact navigation layer.

Example:

```json
{
  "file_groups": [
    {
      "path": "filetoolsserver/handler/grep_tool.go",
      "match_count": 4,
      "row_count": 7,
      "first_line": 21,
      "last_line": 88,
      "read_ranges": [
        { "start_line": 18, "end_line": 31 },
        { "start_line": 62, "end_line": 93 }
      ]
    }
  ]
}
```

Purpose:

- agent sees result by file, not only by rows;
- agent can choose the next file/range without parsing line text;
- broad noisy results become navigable;
- `read_ranges` are ready to feed `read_file`.

`file_groups` should not duplicate full match text. `matches[]` remains the evidence layer; `file_groups[]` is the navigation layer.

### C-005: Recommended Next Call

Add one compact `next_recommended_call` when there is a clear next step.

Examples:

- useful non-truncated content result -> recommend `read_file` for the strongest returned range;
- many matches in one file -> recommend `outline_file` with a line window or `read_file` around the dense block;
- truncated result -> recommend a narrower `grep` call;
- no-match result -> recommend literal mode, case-insensitive mode, different glob/type, or exact dot-prefix file path only when appropriate.

The recommendation must be structured and safe:

```json
{
  "next_recommended_call": {
    "safe_to_retry": true,
    "recommended_next_tool": "read_file",
    "recommended_next_input": {
      "cwd_id": 1,
      "target_file": "filetoolsserver/handler/grep_tool.go",
      "start_line": 18,
      "end_line": 31
    },
    "reason": "Open the first returned match group with enough local context."
  }
}
```

There should be only one top-level recommended call. The goal is clarity, not a menu of possibilities.

### C-006: Better Control Of Noisy Results

Add bounded noise controls that do not hide data by default:

- `max_matches_per_file` for `content` output, so one noisy file cannot consume the whole global limit;
- `line_window` for single-file search, so an agent can rerun grep inside a known range.

Default behavior must remain conservative and compatible:

- no hidden default cwd;
- no new default ignore of `node_modules`, `vendor`, `dist`, `build`;
- no automatic `.gitignore` semantics in this phase;
- no dotfile broad search by default.

If the agent wants to exclude noise, it can use existing `ignore_globs`.

### C-007: Keep Safety Defaults

Phase 4 does not weaken the current dotfile behavior.

Current behavior stays:

- directory traversal skips dot-prefix files and dot-prefix directories;
- exact dot-prefix file path remains searchable;
- `.git`, `.hg`, `.svn`, `.jj` metadata stays pruned during traversal.

Broad dotfile search, OS-specific hidden-attribute semantics, and secret redaction are not part of this phase. They can be a future safety/product phase if needed.

### C-008: Keep Grep Honest, Not Semantic

`grep` must not pretend it understands code semantics.

Outcomes it may provide:

- literal or regex text search;
- line-level evidence;
- file-level grouping;
- read ranges;
- search stats;
- next-step hints to `read_file`, `outline_file`, or another `grep`.

Outcomes it must not claim:

- exact symbol definition vs usage classification;
- AST semantics;
- language server behavior;
- semantic search;
- AI relevance ranking.

If structure is needed, `grep` should point to `outline_file`; it should not secretly parse AST itself.

## Out Of Scope

This phase does not include:

- full `rg` flag parity;
- PCRE2 support;
- project indexing or cached search index;
- semantic search or embedding search;
- LSP integration;
- AST parsing inside `grep`;
- replace/write behavior;
- dotfile broad search default changes;
- `.gitignore` compatibility;
- secret redaction;
- stateful pagination, cursor, or `nextCursor`;
- UI for humans.

## Must Not Break

- Existing `grep` calls keep working.
- Existing output modes keep their current meaning.
- `regex` remains default `pattern_mode`.
- `cwd_id` path rules from Phase 3 remain exact.
- Output stays one structured JSON response.
- `matches[]` keeps line-level source evidence.
- `files_with_matches` remains lightweight.
- `count` remains count-oriented and does not start returning content.
- Large-file streaming behavior remains safe.
- Multiline guardrails remain safe.
- Hidden/dotfile default behavior remains conservative.
- `truncated` and counts must not create false confidence.

## Success

Phase 4 succeeds when an agent can:

- search literal text without regex escaping mistakes;
- search broad repo content and immediately see which files matter;
- tell whether the search completed or stopped early;
- open the most useful range with `read_file` using a ready suggested input;
- rerun a narrower `grep` using a ready suggested input when truncated;
- understand no-match reasons better than raw `rg` output;
- use `cwd_id` paths everywhere without absolute path leaks in recommended inputs.

## Unacceptable Result

The result is unacceptable if:

- it only adds more `rg`-like flags without better agent navigation;
- it increases output size without better structure;
- it changes defaults in a way that hides real matches;
- it claims semantic understanding it does not have;
- it silently ranks or drops results without explaining;
- it loses exact file/line anchors;
- it leaks absolute paths in cwd-aware recommendations;
- it makes current simple grep scenarios worse.

## Key Decisions

- "Better than `rg`" means better for agents, not faster or feature-equivalent as a CLI.
- Primary win: fewer follow-up calls.
- Secondary win: lower token noise while preserving source anchors.
- Third win: honest completeness and explicit next steps.
- `pattern_mode=literal` is in scope.
- `file_groups`, `search_stats`, and one `next_recommended_call` are in scope.
- `max_matches_per_file` and single-file `line_window` are in scope.
- Hidden defaults, `.gitignore`, semantic search, indexing, and secret redaction are out of scope.

## Open Questions

None blocking.

Root decisions for this concept:

- Do not ask the human to choose between token reduction, fewer follow-up calls, and ranking. Optimize all three in this order: fewer follow-up calls, lower token noise, honest explicit ordering.
- Do not change broad ignore defaults in Phase 4.
- Do not add semantic/ranking claims.

## Consultation Summary

Consulted roles before drafting:

- `architect`: recommended agent navigation grep with `pattern_mode`, `search_stats`, `file_groups`, `next_recommended_call`, no stateful pagination, no AST parsing inside grep.
- `product_owner`: emphasized agent outcomes over `rg` feature parity, stable structured output, clear truncation, bounded context, and no scope creep into semantic search.
- `independent_opinion_agent`: warned against CLI parity, hidden ranking, `.gitignore` complexity, broad hidden/secret exposure, and misleading incomplete counts.
