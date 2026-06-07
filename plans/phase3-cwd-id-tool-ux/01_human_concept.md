# Phase 3 CWD ID And Tool UX Concept

concept_version_label: phase3-cwd-id-tool-ux-v1
status: accepted concept source for SRS planning
acceptance_record: concept review passed; current active gate is clean SRS plan review plus explicit user OK before implementation

## Goal

Сделать `mcp-file-tools` удобнее для агента на длинных задачах, где он много раз обращается к одному и тому же workspace, но не сломать текущую safety-модель явных путей.

Главная идея: агент один раз вызывает `set_cwd` с абсолютной директорией, получает короткий `cwd_id`, а дальше может передавать короткие относительные пути вместе с этим id.

Пример желаемого сценария:

```json
{"directory":"D:/ai-apps/mcp-file-tools"}
```

сервер возвращает короткий id:

```json
{"cwd_id":1}
```

после этого агент может вызывать инструменты короче:

```json
{"cwd_id":1,"target_file":"filetoolsserver/handler/read_file.go"}
```

При этом без `cwd_id` старый режим остается прежним: все path inputs должны быть абсолютными.

## User / Maintainer

Основной пользователь - Codex/агент, который работает с большим локальным repo и должен:

- не тратить токены на длинные абсолютные пути в каждом tool call;
- безопасно продолжать работу после reconnect или истечения `cwd_id`;
- не путать текущий чат, параллельного сабагента и другой Codex thread;
- получать полезный `outline_file` не только для Go и Markdown;
- видеть одинаковый `line_count` во всех tools;
- не путаться в batch write byte metrics.

Поддерживающий пользователь - человек, который развивает `mcp-file-tools` и хочет сохранить понятный, bounded, agent-friendly API без скрытого process cwd.

## Scope

### C-001: Add `set_cwd`

Новый tool `set_cwd` регистрирует рабочую директорию для коротких дальнейших вызовов.

Input:

```json
{"directory":"D:/ai-apps/mcp-file-tools"}
```

Требования:

- `directory` обязателен;
- `directory` должен быть абсолютным путем в файловой системе, доступной file-tools server-у;
- в текущем Windows-сценарии preferred display/input form: `D:/ai-apps/mcp-file-tools`, без JSON-escaped backslashes;
- путь должен существовать;
- путь должен быть директорией;
- `set_cwd` не делает `os.Chdir`;
- `set_cwd` не создает скрытый default cwd.

Output:

```json
{
  "cwd_id": 1
}
```

`cwd_id` должен быть коротким integer, потому что смысл этой функции - экономить токены.
`set_cwd` сам возвращает только id. Абсолютный `cwd` возвращают последующие tools, когда агент использует этот `cwd_id`.

### C-002: Add Optional `cwd_id` To Path Tools

Все tools с path inputs получают optional `cwd_id`.

Если `cwd_id` не задан:

- текущий absolute-only контракт остается;
- относительные пути по-прежнему запрещены;
- старые клиенты с absolute path inputs продолжают работать;
- absolute path outputs тоже переходят на slash-normalized display form, например `D:/ai-apps/mcp-file-tools/README.md`.

Если `cwd_id` задан:

- относительные path inputs разрешены;
- относительные path inputs резолвятся от `cwd`;
- absolute path inputs с `cwd_id` rejected; для absolute paths агент не передает `cwd_id`;
- успешные outputs включают `cwd` как абсолютный slash-normalized anchor;
- остальные output path-поля возвращаются relative to `cwd`;
- relative output paths используют `/` как separator и не имеют prefix `./`;
- если нужно сослаться ровно на сам `cwd`, используется `"."`.

Пример успешного output с `cwd_id`:

```json
{
  "cwd_id": 1,
  "cwd": "D:/ai-apps/mcp-file-tools",
  "file": "internal/test.go",
  "total_lines": 152
}
```

### C-003: Unknown Or Expired `cwd_id` Is A Normal Recovery Case

`cwd_id` может протухнуть или стать недоступным, если registry потерян или сервер перезапущен.

Reconnect или пересоздание чата сами по себе не должны сбрасывать live `cwd_id`. Пока id жив и доступен в registry, он должен означать ту же директорию.

Если агент вызывает tool с неизвестным или протухшим `cwd_id`, сервер не должен угадывать путь и не должен silently fallback в absolute-only режим.

Он должен вернуть structured error с понятным next step:

```json
{
  "error_code": "cwd_id_expired",
  "error": "cwd_id 1 is expired or unavailable; call set_cwd again with the intended directory",
  "action_hint": {
    "safe_to_retry": false,
    "recommended_next_tool": "set_cwd"
  }
}
```

Агент берет абсолютный `cwd` из своего контекста или из последнего успешного output, снова вызывает `set_cwd`, получает новый короткий id и продолжает работу.

### C-004: `cwd_id` Must Not Conflict Across Parallel Work

`cwd_id` не должен быть session-scoped state и не должен быть hidden mutable current directory.

Требования:

- `cwd_id` хранится в общем registry, а не внутри MCP session/chat;
- `set_cwd` всегда создает новый immutable handle или возвращает безопасно переиспользуемый handle на ту же директорию, но никогда не перезаписывает чужой id;
- разные чаты и параллельные сабагенты не получают один и тот же active id для разных директорий;
- один чат не может "поменять cwd" другому чату, потому что cwd не меняется глобально, а каждый id immutable;
- несколько `cwd_id` могут жить одновременно;
- каждый `cwd_id` immutable: созданный id указывает на одну директорию до expiry;
- старый `cwd_id` после reconnect либо указывает на ту же директорию, либо возвращает structured expired/unavailable error;
- нет hidden current cwd, который меняется последним вызовом.

`cwd_id` не является security token или authorization boundary. Это короткая ссылка на путь, а не механизм доступа.

### C-005: `outline_file` Must Be Useful For More Text Files

Сейчас `outline_file` дает структуру только для Go и Markdown, а для остальных текстовых файлов в основном возвращает fingerprint.

Это нужно исправить так, чтобы агент получал полезные ranges даже для обычных unsupported text files, но сервер не должен притворяться, что у него есть точный AST для всех языков.

Ожидаемое поведение:

- Go и Markdown остаются exact parsers;
- для обычных non-binary text files появляется generic text outline fallback;
- generic fallback возвращает честно промаркированные synthetic/text chunks или найденные простые структурные anchors;
- такие items не называются exact AST symbols;
- `confidence` и `parser_status` показывают, насколько результат точен;
- fingerprint остается доступен всегда для text files.

Цель - убрать бесполезный тупик "unsupported => только fingerprint", но не создавать fake semantic outline.

### C-006: Line Count Must Be Consistent

`inspect_path`, `read_file`, `outline_file` и fingerprint должны считать строки одинаково.

Текущая UX-нестыковка: `inspect_path` может показать на одну строку меньше, чем `read_file`/`outline_file`, если файл заканчивается финальным newline.

Единая модель должна быть display-line моделью:

```text
empty file -> 0 lines
"a"        -> 1 line
"a\n"      -> 2 lines, because the final empty display line is addressable
```

Это важно, потому что агент использует `line_count` для ranges, outline continuation и write preconditions.

### C-007: Batch Byte Metrics Must Be Clear

В `move_ranges_batch` текущий общий `would_write_bytes` выглядит неоднозначно: per-target bytes понятны, а общий показатель может смешивать разные смыслы.

Нужно сделать byte metrics понятными:

- per-target bytes остаются в `target_results`;
- aggregate fields должны явно различать target write bytes, source rewrite bytes и общий planned write budget;
- старое поле можно сохранить ради совместимости, но documentation/output должны объяснять, что оно значит и чем пользоваться агенту.

## Out Of Scope

Эта фаза не включает:

- настоящий process cwd;
- `os.Chdir`;
- hidden default cwd;
- global cwd на сервер;
- durable active cwd entries через restart процесса: после restart старый id не обязан продолжать работать;
- сохранение самих cwd path entries как workspace state;
- auto-detect workspace root;
- sandbox или authorization redesign через `cwd_id`;
- ослабление path-map semantics;
- ослабление fingerprints, dry-run, path locks, symlink checks, backups или partial recovery;
- full AST parser для всех языков сразу;
- LSP, project index, watcher/cache;
- автоматический выбор файлов, ranges или target names за агента.

## Must Not Break

- Без `cwd_id` все path inputs остаются absolute-only.
- Без `cwd_id` absolute path outputs используют `/`, а не обратные слеши.
- С `cwd_id` absolute path inputs rejected, чтобы у режима была одна система координат.
- Относительный path без `cwd_id` все еще должен быть rejected.
- Empty path не превращается в `"."`.
- `cwd_id` не меняет поведение других чатов или параллельных agents.
- Unknown/expired `cwd_id` не игнорируется.
- Write tools сохраняют все safety preconditions.
- Output остается structured JSON.
- Existing clients that pass absolute paths without `cwd_id` keep working.
- Windows/Linux/macOS/Docker path semantics остаются first-class.
- Path maps продолжают работать для absolute paths.
- Restart allocator не переиспользует старый id так, что он начинает указывать на другой cwd.

## Success

Фаза успешна, если агент может:

- один раз вызвать `set_cwd` для repo;
- дальше читать, искать, инспектировать, outline-ить и выполнять range operations короткими relative paths plus `cwd_id`;
- видеть `cwd` в successful outputs и восстановиться после expiry/unavailable registry;
- получить clear error при expired/unknown `cwd_id`;
- использовать `outline_file` для non-Go/non-Markdown text files хотя бы через honest generic text outline;
- видеть одинаковый `line_count` во всех tools;
- понять batch write byte metrics без догадок;
- продолжать использовать старый absolute-only input режим, но видеть slash-normalized absolute outputs.

## Unacceptable Result

Результат неприемлем, если:

- relative path работает без `cwd_id`;
- `set_cwd` вызывает `os.Chdir`;
- active `cwd_id` переиспользуется или перезаписывается для другой директории;
- старый `cwd_id` после reconnect/restart начинает указывать на другой cwd;
- последний вызов `set_cwd` скрыто меняет поведение tools без явного `cwd_id`;
- unknown/expired `cwd_id` silently falls back to absolute behavior;
- output paths при `cwd_id` остаются абсолютными;
- relative output paths не anchored by `cwd`, содержат backslashes или лишний `./` prefix;
- absolute output paths без `cwd_id` содержат backslashes вместо `/`;
- `outline_file` выдает fake exact AST для unsupported languages;
- `inspect_path.line_count` снова расходится с `read_file`/fingerprint;
- batch output оставляет только мутный aggregate `would_write_bytes`;
- write safety ослаблена ради удобства.

## Key Decisions

- `cwd_id` - короткий integer, выдаваемый сервером.
- Агент не выбирает `cwd_id` сам.
- `set_cwd` принимает только `directory` как path input.
- `cwd_id` передается явно в каждом следующем tool call.
- `cwd_id` хранится в общем server-side registry, не в session/chat scope.
- Active ids не переиспользуются для разных директорий.
- Session reconnect сам по себе не должен сбрасывать live `cwd_id`; если id уже недоступен, tool возвращает structured error и агент заново вызывает `set_cwd`.
- Успешные outputs при использовании `cwd_id` включают `cwd` absolute anchor и relative path-поля.
- `cwd_id` - convenience handle с TTL; это не workspace state и не security token.
- Для restart-safety можно хранить минимальную allocator metadata, например high-water mark, но не durable cwd path entries. SQLite подходит как локальное хранилище для такой metadata.
- `outline_file` получает generic text fallback for non-binary text files, но exact parsers остаются отдельной категорией.
- `line_count` uses the same display-line model everywhere.
- Batch byte metrics are split into clear named fields.

## Open Questions

None blocking for concept.

Planning can decide exact TTL configuration name/default, generic text chunk sizing, and exact byte metric field names, as long as the decisions preserve this concept.

## Acceptance Path

This concept has passed concept review and is now the accepted source for SRS planning.

```text
current_process:
  1. create human and technical concept files - done
  2. run concept review cycle - done
  3. after clean review and user approval, create detailed SRS-style plan bundle - done
  4. run plan review cycle - active
  5. after clean plan review and user approval, implement
```
