# Phase 8: 10/10 Agent AX For Outline, Discovery, Inventory, And Joiners

## Зачем Это Нужно

После Phase 7 инструменты стали сильнее, но агентская оценка осталась около 8.5/10. Главная проблема теперь не в отсутствии возможностей, а в лишней когнитивной нагрузке: агенту приходится помнить нюансы discovery, перепроверять шумные outline, правильно трактовать `workspace_inventory` completeness и осторожно угадывать эффект `joiner`.

Цель Phase 8: приблизить текущие file tools к 10/10 именно для агента. Инструменты должны давать меньше шума, больше готовых решений и меньше мест, где агент может неправильно понять корректный ответ.

## Ответ На Вопрос Про Tool Discovery И Truncate

Да, lazy discovery может быть связан с truncation или размером metadata. Это не единственное возможное объяснение, но длинные tool descriptions и похожие друг на друга формулировки точно ухудшают обнаружение: инструмент может хуже ранжироваться, описание может обрезаться, а агент видит не тот набор callable tools до дополнительного поиска.

Phase 8 должна относиться к этому как к продуктовой проблеме: descriptions должны быть компактными, контрастными и discovery-friendly. Агент должен быстрее находить `outline_file`, `glob_file_search`, `read_files`, `resolve_symbol_range`, `workspace_inventory` и range tools по смысловым запросам.

Diagnostic baseline after increasing discovery context to `max_lines=150`: a generic/default agent sees all 14 MCP file tool names and useful short summaries in `tool_search` metadata, but does not get the callable namespace or full input schemas until lazy discovery runs. Phase 8 should therefore optimize both layers: pre-search metadata must make the right tool obvious, and post-search descriptions/schemas must be useful without becoming noisy mini-docs.

## Пользователь И Сценарий

Основной пользователь - агент, который делает repo review, навигацию, точечные правки и механические переносы кода/документов через MCP file tools.

Человек в этом сценарии не должен вручную объяснять агенту нюансы вроде:

- почему `outline_file` по TSX шумнее Go;
- что `workspace_inventory.summary.complete=false` не значит, что page continuation неполная;
- почему `blank_line` может дать больше визуальной пустоты, если выбранные ranges уже содержат пустые строки;
- какие инструменты надо отдельно искать, если discovery не выдал их сразу.

## Scope

Phase 8 исправляет четыре текущих AX-проблемы.

1. Tool discovery и descriptions.
   - Сократить и сфокусировать descriptions так, чтобы они были полезны при truncation.
   - Убрать повторяющиеся длинные описания, которые размывают смысл.
   - Явно подсветить, когда использовать `outline_file`, `glob_file_search`, `read_files`, `resolve_symbol_range`, `workspace_inventory`, range tools.
   - Сохранить точность контрактов, но не превращать tool description в мини-документацию.

2. Outline quality для основных языков.
   - Довести JS, TS, TSX, Python, Java, JSON и YAML максимально близко к качеству Go/Markdown.
   - Уменьшить шум в default `agent` profile.
   - Убрать или спрятать неакциональный `write_safe=false` шум там, где он не помогает следующему шагу.
   - Исправить неверные классификации, например TSX export, попавший в `import_block`.
   - Добавить Java outline как основной язык.
   - Для JSON/YAML сохранить точную навигацию, но сделать output компактнее и понятнее.

3. `workspace_inventory` completeness semantics.
   - Сделать невозможным неправильное чтение `summary.complete=false` при `continuation.complete=true`.
   - Развести понятия: page/continuation completeness и summary/scan coverage.
   - Поля и сообщения должны прямо объяснять, что именно полно, а что нет.

4. Joiner UX в range tools.
   - Сделать эффект `joiner` понятнее до записи.
   - Объяснять, сколько newline уже есть на границах выбранных ranges и сколько будет добавлено.
   - `blank_line` должен быть безопаснее для агента: dry-run output должен явно показывать, когда визуальная пустота станет больше ожидаемой.

## Out Of Scope

- Не строим полноценный IDE-grade semantic engine.
- Не обещаем rename symbol, type checking, import organizer или AST rewrite.
- Не ослабляем fingerprint/dry-run/read-back модель range tools.
- Не возвращаем safety-first redaction по умолчанию.
- Не делаем UI или внешний сервис.
- Не меняем cwd_id архитектуру, если для этой фазы это не требуется.

## Must Not Break

- Go и Markdown outline должны остаться такими же надежными или лучше.
- Default redaction остается `off`; strict redaction только явный режим.
- Все cwd-aware tools продолжают принимать relative paths с `cwd_id` и возвращать relative paths.
- Range tools не мутируют файлы без явного apply; `resolve_symbol_range` продолжает выдавать только dry-run recommendations.
- Existing tests/build/restart workflow должен остаться рабочим.

## Success Criteria

- Агентская оценка должна подняться выше 8.5 за счет меньшего шума и меньшей необходимости перепроверять очевидные next steps.
- `outline_file` для JS/TS/TSX/Python/Java/JSON/YAML дает компактные, точные и action-oriented symbols/sections.
- TSX exports не классифицируются как import-only блоки.
- Java файлы получают parser-backed outline с классами, интерфейсами, методами, constructors, enums и imports/package.
- JSON/YAML default profile не заваливает агента leaf/value шумом, но full profile сохраняет точную навигацию.
- `workspace_inventory` явно показывает page completeness отдельно от summary coverage.
- Range tool dry-run делает joiner outcome визуально и структурно понятным.
- Tool descriptions остаются точными, но становятся короче и лучше обнаруживаемыми.

## Agent-Facing Acceptance Probes

Phase 8 считается успешной только если улучшение видно на практических agent tasks, а не только в количестве новых полей или зеленых unit tests.

1. Discovery probe.
   - По смысловым запросам вроде "find files by name", "symbols in file", "batch read context", "selector to ranges", "copy range with dry-run preview" и "repo directory map completeness" metadata должна явно вести к правильным tools.
   - `glob_file_search`, `outline_file`, `read_files`, `resolve_symbol_range`, `workspace_inventory` и range tools должны отличаться друг от друга по первым строкам descriptions.
   - Короткий/truncated tool context не должен прятать назначение основных tools за длинной мини-документацией.

2. Outline probe.
   - Для каждого из JS, TS, TSX, Python, Java, JSON и YAML должны быть representative fixtures с ожидаемыми default `agent` outputs.
   - Default `agent` profile должен показывать actionable containers, symbols, selectors, ranges и compact summaries.
   - Leaf/value noise, повторяющийся `write_safe=false` и подробные refusal details не должны заполнять default output; при этом `full` profile обязан сохранять подробную навигацию.
   - Все selectors, которые возвращает `outline_file` в `agent` или `full`, должны round-trip через `resolve_symbol_range`.

3. Inventory probe.
   - Когда returned page полная, но summary/tree coverage неполная, output должен явно сказать это разными canonical fields.
   - Старые compatibility поля могут остаться, но агент должен видеть новые ясные поля первыми в schema/docs/descriptions и понимать next action без догадки.

4. Joiner probe.
   - Dry-run должен показывать requested/normalized joiner, newline style, source boundary blank lines, target insertion boundary, resulting visual blank lines and actionable warnings.
   - Если `blank_line` из-за уже пустых edge lines создаст больше визуальной пустоты, это должно быть видно без apply и без ручного перечитывания больших ranges.

## Unacceptable Result

- Агенту все еще приходится отдельно искать основные инструменты из-за размытых descriptions.
- JS/TS/TSX/Python/Java outline дает много мусора или ложные группы, из-за которых агент обязан читать большие ranges вручную.
- JSON/YAML keys/indices снова теряют точную идентичность.
- `workspace_inventory` продолжает использовать два разных `complete`, которые легко перепутать.
- `blank_line` продолжает быть “угадай и перечитай”, без явного boundary/joiner diagnostics.
- Решение добавляет большие новые зависимости без понятной пользы или ломает Windows build.
- Реализация добавляет поля или parser cases, но agent task по-прежнему требует дополнительных ручных поисков, больших range reads или интерпретации ambiguous status fields.

## Open Questions

Open product questions are not blocking.

- Если Java grammar из vendored `gotreesitter` не подключается текущими build tags, можно добавить/настроить зависимость, потому что пользователь уже разрешил менять зависимости для максимальной эффективности.
- Конкретные имена новых completeness/joiner fields можно выбрать по месту, если смысл будет однозначным и schema/tests/docs это закрепят.
