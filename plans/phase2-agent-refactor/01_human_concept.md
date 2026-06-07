# Phase 2 Agent Refactor Tools Concept

concept_version_label: phase2-agent-refactor-v1
status: accepted for implementation planning
acceptance_record: user accepted this concept in the current planning thread

## Goal

Дать агенту быстрый и безопасный способ разбирать большие файлы на структуру и механически переносить точные текстовые блоки между файлами без регенерации кода или Markdown из памяти.

Главный сценарий:

```text
outline_file -> выбрать точные ranges -> copy_ranges / move_ranges -> проверить результат обычными project checks
```

10/10 agent ergonomics сценарий:

```text
outline_file -> выбрать stable outline items/ranges -> dry_run при риске -> copy/move или batch copy/move -> использовать returned next fingerprints/recovery hints
```

Это следующая фаза после agent-navigation. Фаза 1 намеренно оставалась read-only и запрещала `outline_file` / mutating tools. Фаза 2 осознанно добавляет ограниченные write-tools, но не превращает сервер в IDE, LSP, formatter или semantic refactor engine.

## User / Maintainer

Основной пользователь - Codex/агент, который работает с большим локальным workspace и должен быстро:

- вынести функции, классы, методы или type blocks из слишком большого файла;
- разложить большой Markdown concept на несколько меньших файлов;
- избежать токенового full-file чтения там, где достаточно структуры и line ranges;
- не переписывать большой блок по памяти и не вносить случайные отличия.

Поддерживающий пользователь - человек, который развивает `mcp-file-tools` и хочет сохранить понятный, bounded, stateless API с явными safety-границами.

## Scope

### C-001: Add A Refactor Layer Without Becoming A Refactor Engine

Фаза 2 добавляет refactor-friendly primitives, а не semantic refactor.

Инструменты могут:

- показать структуру одного файла;
- скопировать точные line ranges в другой файл;
- переместить точные line ranges в другой файл;
- применить explicit one-source/multi-target range plan, когда агент уже сам выбрал ranges, target paths и placement.

Инструменты не должны:

- искать зависимости;
- чинить imports;
- переименовывать символы;
- обновлять references;
- форматировать код;
- менять архитектуру;
- выбирать за агента, какие блоки нужно переносить.

Агент принимает продуктовые и инженерные решения сам, а MCP tool выполняет только точную механическую операцию.

### C-002: Add `outline_file`

`outline_file` - read-only инструмент для дешевого понимания структуры одного файла без вывода содержимого блоков.

Он должен отвечать на вопросы агента:

- какие top-level code blocks есть в файле;
- где находятся imports/package/declarations;
- где начинаются и заканчиваются функции, методы, классы, type/interface/enum blocks;
- какие Markdown sections есть в документе;
- какие line ranges можно безопасно передать в `copy_ranges` или `move_ranges`;
- какой file fingerprint нужно использовать как stale-write guard.

`outline_file` возвращает compact structured JSON, а не formatted text.

### C-003: Add `copy_ranges`

`copy_ranges` копирует один или несколько явных line ranges из одного source-файла в один target-файл.

Он должен уметь:

- создать новый target-файл из нескольких ranges;
- добавить ranges к существующему target-файлу;
- вставить ranges в существующий target перед конкретной строкой;
- заменить явный target range, если target fingerprint совпал.

`copy_ranges` не изменяет source-файл.

### C-004: Add `move_ranges`

`move_ranges` использует тот же range/target mental model, но после успешной записи target удаляет source ranges из source-файла.

Публично это отдельный tool, а не `copy_ranges` с `mode: "move"`, потому что удаление source-текста является более рискованной операцией. Отдельный tool снижает шанс, что агент случайно передаст неверный mode.

Для `move_ranges` особенно важно:

- проверять source fingerprint;
- проверять target precondition;
- запрещать overlapping source ranges;
- удалять source ranges по original source snapshot;
- применять удаление снизу вверх внутри реализации;
- не обещать невозможную cross-file transaction atomicity.

### C-005: Markdown Decomposition Uses The Same Primitives

Большой Markdown concept раскладывается так же:

```text
outline_file(file.md) -> выбрать heading section ranges -> copy_ranges_batch / move_ranges_batch into smaller .md files
```

Отдельный `split_markdown_sections` не входит в MVP, потому что он выбирал бы naming/overwrite/frontmatter/index policy за агента.

Вместо этого MVP должен дать агенту удобный explicit batch path:

- non-destructive decomposition: один `copy_ranges_batch` из одного source outline в несколько target files;
- destructive decomposition: один `move_ranges_batch`, который сначала пишет все targets, а потом один раз удаляет все source ranges из original source snapshot;
- если batch не нужен, single-target `copy_ranges` / `move_ranges` остаются простыми primitives.

Причина: автоматический split требует скрытых product policy decisions:

- как называть файлы;
- что делать со slug collisions;
- создавать ли директории;
- перезаписывать ли существующие файлы;
- переносить ли frontmatter;
- включать ли parent headings;
- делать ли index file.

Эти решения лучше оставить агенту/плану. Batch tools не принимают скрытых решений: они только исполняют явный план агента.

### C-006: Multi-Language Outline Is Staged

`outline_file` должен иметь единый output model для разных языков, но не должен обещать одинаковую точность для всех языков сразу.

Рекомендуемый staged scope:

- MVP exact: Markdown ATX heading parser (`#` through `######`). Setext headings and other non-ATX heading forms must be explicitly warned about or left unsupported; the tool must not silently claim full Markdown outline exactness for them.
- MVP exact: Go через `go/parser` / `go/ast`.
- Next language pack: TypeScript, JavaScript, Python, Java через per-file Tree-sitter-style parser backend, если dependency/build cost принят в technical plan.
- Unsupported languages: вернуть полезную file metadata/fingerprint и warning, но не притворяться, что regex-outline является точным AST.

Каждый outline item должен явно показывать `confidence`, например `exact` или `best_effort`.

### C-007: Fingerprints Are Product-Safety, Not Metadata Decoration

Write tools должны защищать агента от stale line ranges.

`outline_file` всегда возвращает full-file fingerprint:

```json
{
  "sha256": "...",
  "size_bytes": 12345,
  "line_count": 900,
  "modified_unix_nano": 1780600000000000000
}
```

`copy_ranges` и `move_ranges` требуют source fingerprint. Для существующего target они требуют target fingerprint. Для нового target они требуют precondition `must_not_exist`.

Если fingerprint не совпадает, tool должен отказать до записи.

### C-008: Single Source, Explicit Target Plan

MVP сохраняет один source-файл за вызов, чтобы fingerprint, line ranges и deletion semantics оставались понятными.

Single-target tools:

- создать один новый файл из нескольких source ranges;
- добавить несколько source ranges к одному существующему target;
- переместить несколько source ranges в один target.

Batch tools:

- `copy_ranges_batch` создает или обновляет несколько target files из одного source snapshot;
- `move_ranges_batch` пишет несколько target files и затем удаляет все moved source ranges из source один раз;
- каждый target в batch имеет собственные `target_precondition`, `placement`, `joiner` и `backup`;
- batch не выбирает target names, directories, heading policy, imports или formatting.

Один результат `outline_file` можно безопасно использовать для нескольких `copy_ranges`, пока source fingerprint не изменился.

Для `move_ranges` reuse того же outline после первого move запрещен: source fingerprint и line numbers меняются. Безопасные варианты:

- одним `move_ranges` перенести все нужные ranges из source в один target;
- одним `move_ranges_batch` перенести ranges из source в несколько targets;
- после каждого move заново вызвать `outline_file`;
- в будущей версии явно поддержать descending-source-order multi-call protocol через `source_fingerprint_after`, если это будет нужно.

### C-009: Large-File Acceptance Must Stay Honest

Фаза 2 нужна именно для больших файлов, поэтому write threshold не должен быть скрытой лазейкой, из-за которой MVP формально проходит, но проваливает основной сценарий.

План implementation должен явно выбрать configurable write threshold и проверить representative large files. Инструмент может отклонять экстремально большие файлы ради памяти/безопасности, но acceptance должен доказать, что типичный "слишком большой для агента" файл поддерживается без full-token dump.

### C-010: Hard Non-Goals

Фаза 2 не включает:

- LSP server;
- project-wide index;
- watcher/cache как обязательную часть correctness;
- semantic symbol move;
- dependency graph;
- auto-import management;
- code formatting;
- rename/update references;
- AST rewrite;
- fuzzy range selection;
- natural-language split of Markdown by meaning;
- automatic directory/file naming policy for Markdown split;
- hidden backups by default;
- writing through symlinks by default;
- writing through any symlink path component for mutated files in MVP;
- binary file editing;
- same-file copy in MVP;
- same-file reorder/move in MVP.

### C-011: Agent Ergonomics Is Acceptance-Critical

Инструменты должны быть удобны именно агенту, а не только человеку, который читает документацию.

10/10 agent ergonomics значит:

- every write refusal tells the agent the next safe tool call or decision;
- stale fingerprint errors include expected/current fingerprints and whether re-outline is required;
- range errors include current line count and the failing range;
- existing target fingerprint workflow is documented and does not require full-file read;
- `outline_file` truncation includes recovery metadata instead of a dead-end `truncated=true`;
- outline items have stable ids/paths within a fingerprint snapshot so the agent can reason about selected blocks;
- write tools support `dry_run` for risky operations, especially before source mutation;
- successful write output returns fingerprints for the next safe write;
- boundary newline risks are returned as structured warnings before or after write, not left as silent text damage;
- multi-target Markdown decomposition does not require re-reading/re-outlining the source after each section.

## Must Not Break

- Все path inputs остаются полными абсолютными путями ОС, где запущен MCP server.
- Пустые и относительные paths запрещены.
- Existing read-only tools не меняют default behavior.
- Structured output остается JSON-native, с отдельной output schema на каждый tool.
- No hidden cursor pagination is introduced for this phase.
- Tool errors остаются structured and actionable.
- Tool errors include agent-actionable retry/recovery fields, not just codes.
- Windows/Linux/macOS/Docker path semantics остаются first-class.

## Success

Фаза 2 успешна, если агент может:

- посмотреть структуру большого `.go`, `.md`, а позже `.ts`, `.js`, `.py`, `.java` файла без чтения всего файла в контекст;
- выбрать exact range по output `outline_file`;
- создать новый файл из нескольких ranges одним `copy_ranges`;
- добавить ranges к существующему файлу с fingerprint-защитой;
- переместить ranges через `move_ranges` без ручного копирования текста;
- разложить Markdown concept в несколько target files одним explicit batch call без hidden naming policy;
- сделать `dry_run` перед risky write и получить line/fingerprint/boundary diagnostics без mutation;
- получить отказ, если source изменился до финальной source-проверки перед target write или target изменился до финальной target-проверки перед write;
- восстановиться после common write errors по structured `recommended_next_tool` без догадок;
- не потерять исходный текст при partial cross-file move; при crash между target/source writes возможен duplicated state, который агент или человек вручную reconciles;
- явно увидеть, когда language outline unsupported или best-effort.

## Unacceptable Result

Результат неприемлем, если:

- tool сам переписывает или "улучшает" код;
- source text теряется при crash/partial failure;
- stale line ranges могут молча удалить или скопировать не тот блок;
- common error forces the agent to guess the next recovery tool;
- `outline_file` truncation gives no useful way to narrow or retry;
- unsupported language получает уверенные, но неверные ranges;
- Markdown non-ATX headings silently ignored while output claims full exact Markdown outline;
- Markdown split начинает принимать скрытые решения о naming/overwrite/frontmatter;
- agent снова вынужден читать весь большой файл ради точных ranges;
- write tool работает с относительными путями;
- write tool молча следует symlink target;
- write tool writes through a symlink directory component;
- write tool overwrites a target whose fingerprint differs at the final target precondition recheck;
- same-file copy/move mutates the source in MVP;
- destructive multi-target Markdown split requires one re-outline per target;
- existing read-only tools регрессируют.

## Key Decisions

- Public write tools: `copy_ranges`, `move_ranges`, `copy_ranges_batch`, and `move_ranges_batch`, not one `transfer_ranges` with mode.
- `outline_file` is required before normal agent refactor flow.
- Fingerprints are required preconditions for writes.
- `split_markdown_sections` is deferred.
- MVP language support is staged; exact support first, best-effort clearly labelled later.
- One outline can feed several copy calls, but move calls require one-call-all-ranges or re-outline after each move.
- Batch move is the ergonomic path for destructive one-source/multi-target decomposition.
- Large-file thresholds are acceptance-critical and must be chosen in the implementation plan.
- Optional joiners and sidecar backups are explicit caller choices; defaults preserve extracted bytes and create no backup files.
- External editor races after the final precondition recheck are a residual OS-level risk unless a platform gives true conditional replace; the MVP must be honest about that instead of claiming cross-process transaction atomicity.

## Open Questions

None blocking for concept.

Decision that may be revisited during planning: whether the first implementation should include Tree-sitter language pack immediately, or ship Markdown + Go exact outline first with parser interface prepared for TS/JS/Python/Java.

## Acceptance Record

This concept is accepted for implementation planning.

```text
accepted_concept_record:
  concept_version_label: phase2-agent-refactor-v1
  status: accepted
  accepted_by: user
  accepted_scope:
    - outline_file
    - copy_ranges
    - move_ranges
    - copy_ranges_batch
    - move_ranges_batch
    - markdown decomposition through explicit ranges
    - staged language outline support
    - agent-actionable errors and dry-run diagnostics
```
