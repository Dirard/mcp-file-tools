# Phase 1 Agent Navigation Concept

concept_version_label: phase1-agent-navigation-v1
status: draft, pending user acceptance

## Goal

Сделать MCP file tools удобнее для агентного флоу без превращения сервера в индексатор, language server или набор специализированных парсеров.

Главный сценарий фазы 1:

```text
найти место -> получить точный якорь -> прочитать нужные строки -> сослаться на evidence
```

Фаза 1 не про количество инструментов. Ограничения "ровно 4 инструмента" нет. Ограничение другое: каждый новый инструмент или параметр должен ускорять агентную навигацию, оставаться read-only, быть дешевым по исполнению и укладываться в текущий 10 KiB output-budget с cursor-пагинацией.

## User / Maintainer

Основной пользователь - Codex/агент, который исследует большой локальный workspace через MCP и должен быстро получать точный контекст без лишнего чтения файлов, широких обходов и ручной догадки по путям/cursor.

Поддерживающий пользователь - человек, который развивает `mcp-file-tools` и хочет сохранить маленький понятный API с качеством не хуже текущих `read_file`, `list_dir`, `glob_file_search`, `grep`.

## Scope

### C-001: Preserve Small Read-Only Surface

Сервер остается read-only. Mutating tools не возвращаются.

Текущие инструменты сохраняют свои default-поведения:

- `read_file`
- `list_dir`
- `glob_file_search`
- `grep`

Фаза 1 может добавить новый маленький инструмент, если он решает реальную агентную боль и не раздувает сервер.

### C-002: Add `inspect_path`

Добавить `inspect_path` как дешевую проверку одного пути перед чтением или поиском.

Он должен отвечать на вопросы агента:

- путь существует или нет;
- это файл, директория, symlink или другое;
- какой размер и время изменения;
- можно ли ожидать, что путь читаем;
- какой path/display path дальше передавать в другие tools.

`inspect_path` не читает содержимое файла, не строит дерево, не считает детей директории, не делает hash, не парсит язык/JSON и не пытается быть inventory.

### C-003: Keep `read_file(start_line, end_line)` As The Range Tool

`read_file` уже умеет читать один файл и диапазон строк через `start_line` / `end_line`. Этого достаточно для фазы 1.

В фазу 1 не входят:

- `read_ranges`;
- `read_around`;
- `read_multiple`;
- batch-read по нескольким файлам;
- fuzzy-чтение "вокруг символа" или "вокруг ошибки".

Агент после `grep` сам выбирает точный диапазон, например `line - 20` / `line + 20`, и вызывает существующий `read_file`.

### C-004: Add Structured `grep` Mode

Текущий текстовый `grep` остается default. Он не должен ломаться, переименовываться или становиться менее компактным.

Фаза 1 добавляет opt-in режим `output_mode: "structured"`, чтобы агент мог получить машинно-читаемые результаты поиска с line/source anchors.

Structured output в фазе 1 - это валидный компактный JSON, сериализованный в обычном поле `text`, плюс существующий top-level `nextCursor` для продолжения страниц. Это сохраняет совместимость с текущим MCP wrapper и не заставляет исполнителя придумывать новый envelope.

Structured-режим нужен не для человека, а для надежного перехода:

```text
grep structured match -> source_ref/path/line -> read_file(start_line,end_line)
```

### C-005: Keep Cursor, Budget, Error Semantics Consistent

Все существующие и новые ответы должны сохранять текущую идею:

- результат укладывается в serialized MCP budget около 10 KiB;
- если вывода больше, возвращается `nextCursor`;
- cursor копируется агентом как opaque string, не генерируется вручную;
- длинная строка в текущих text tools сохраняет существующее char-level continuation;
- structured `grep` для длинной строки использует JSON-фрагменты с UTF-8 byte offset и разрезом только на валидной UTF-8 границе;
- no-result не является error;
- no-result в structured `grep` возвращается как валидный JSON `{"matches":[],"truncated":false}`, а не как friendly plain text;
- validation/cursor/regex/path ошибки возвращаются структурно в `error`, при пустом `text`.

### C-006: Use Stable Source Anchors

Structured-результаты, где есть строковая привязка, должны возвращать простой якорь, пригодный для следующего вызова `read_file`.

Минимально полезный anchor:

```text
path + line
```

Если column/span можно получить дешево и надежно, они допустимы, но не должны становиться обязательной сложностью для обычного флоу.

### C-007: Defer `glob_file_search` Feature Additions

Отдельный `recent_files` не нужен.

Фаза 1 не добавляет новые параметры `glob_file_search`. Текущий инструмент уже имеет `ignore_globs`, newest-first output, modified timestamps и cursor pagination; этого достаточно для agent-navigation foundation.

Кандидаты ниже остаются за пределами фазы 1:

- `sort` как enum;
- `modified_since` в одном явном формате времени;
- `max_entries` как общий cap результата;
- возможно `include_metadata`, только opt-in и только короткие поля.

Их можно рассмотреть в следующей фазе, когда будет понятно, что именно не хватает после `inspect_path` и structured `grep`.

### C-008: Cross-Platform Behavior Must Stay First-Class

Фаза 1 должна одинаково хорошо работать на Windows, Linux/macOS и в Docker.

Агент не должен гадать, какой путь использовать дальше. Если сервер применяет path mapping, output должен оставаться пригодным для последующих tool calls.

### C-009: Speed And Concurrency Matter

Инструменты должны оставаться stateless и bounded. Несколько агентов могут одновременно пользоваться одним MCP.

Фаза 1 не должна добавлять общий индекс, watcher, долгоживущий scan-cache или состояние, из-за которого cursor одного агента влияет на другого.

### C-010: Hard Non-Goals For Phase 1

В фазу 1 не входят:

- `list_tree`;
- `workspace_inventory`;
- `outline_file`;
- language-aware parsing;
- JSON/JSONL navigation tools;
- project indexer;
- filesystem watcher;
- mutating tools;
- `read_multiple`;
- `read_ranges`;
- `read_around`.

Эти идеи можно обсуждать позже, но не протаскивать в фазу 1 под видом metadata или structured output.

## Success

Фаза 1 успешна, если агент может:

- проверить сомнительный путь через `inspect_path`;
- найти строку через `grep`;
- получить structured anchor без парсинга human text;
- прочитать точный диапазон через существующий `read_file(start_line,end_line)`;
- продолжить любой большой вывод через compact `nextCursor`;
- понять no-result/error/partial-page без повторного чтения или догадок.

## Unacceptable Result

Результат неприемлем, если:

- default-поведение текущих четырех tools ломается;
- structured `grep` возвращает невалидный JSON/структуру при длинной строке;
- structured `grep` заставляет агента угадывать, где искать machine-readable fields;
- новый инструмент делает скрытый recursive scan;
- `inspect_path` превращается в inventory/outline/parser;
- `read_multiple`, `read_ranges` или mutating tools возвращаются;
- cursor становится длинным, stateful или непредсказуемым для нескольких агентов;
- Windows/Linux/Docker path output заставляет агента гадать, какой путь передать дальше;
- `glob_file_search` получает новые параметры в фазе 1.

## Open Questions

none

## Acceptance Record Draft

This concept is not accepted yet.

To accept it, update this section or record in chat:

```text
accepted_concept_record:
  concept_version_label: phase1-agent-navigation-v1
  status: accepted
  accepted_by: user
  accepted_scope: C-001..C-010
```
