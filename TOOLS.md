# MCP File Tools

Сервер предоставляет 14 кроссплатформенных MCP tools, адаптированных из `mr-agent` references под локальную файловую систему и быстрые agent refactor workflows.

Пути интерпретируются ОС, на которой запущен MCP-сервер: POSIX paths вроде `/path/to/repo` на Linux/macOS, Windows paths вроде `D:/path/to/repo` на Windows. Без `cwd_id` path inputs остаются absolute-only: подставляй реальный полный путь ОС сервера. С `cwd_id` path inputs должны быть относительными к cwd, зарегистрированному через `set_cwd`; absolute path вместе с `cwd_id` отклоняется. Path-поля в успешных ответах без `cwd_id` возвращаются slash-normalized absolute/display paths; с `cwd_id` ответ содержит absolute `cwd`, а остальные filesystem path-поля возвращаются cwd-relative, без leading `./`. Каждый успешный вызов возвращает полный результат в одном structured JSON ответе со своей output schema для каждого инструмента; `cursor` и `nextCursor` не используются.

Ошибки возвращаются для агента структурно: MCP result помечается `isError=true`, plain-text MCP content пустой, а actionable сообщение находится в поле `error` конкретной output schema.

Mutating refactor tools exposed только с явными path inputs: absolute без `cwd_id` или cwd-relative с `cwd_id`, source fingerprint precondition, `dry_run`, лимитами, optional sidecar backups и structured recovery metadata.

## set_cwd

Регистрирует одну абсолютную директорию и возвращает короткий integer `cwd_id` для последующих cwd-relative вызовов.

Использование:

- `directory` обязателен и должен быть абсолютным путем директории в filesystem namespace MCP-сервера.
- `set_cwd` принимает только поле `directory`; лишние поля, включая `cwd_id`, отклоняются.
- Путь должен существовать и быть директорией.
- `set_cwd` не вызывает `os.Chdir`, не создает hidden default cwd и не мутирует зарегистрированную workspace директорию.
- Tool может создать или обновить server-local allocator state в `MCP_CWD_STATE_PATH`.
- Успешный output содержит только `cwd_id`.
- Если `cwd_id` протух или потерян, вызови `set_cwd` снова с absolute `cwd`, который агент помнит из предыдущих outputs.

Input:

```json
{
  "directory": "D:/ai-apps/mcp-file-tools"
}
```

Output:

```json
{
  "cwd_id": 1
}
```

## read_file

Читает один файл из локальной файловой системы. Используй, когда уже известен точный путь и нужен line-addressable вывод.

Использование:

- Без `cwd_id` `target_file` обязателен и должен быть полным абсолютным путем для ОС, где запущен MCP-сервер.
- С `cwd_id` `target_file` обязателен и должен быть относительным путем от зарегистрированного cwd.
- `start_line` и `end_line` выбирают 1-based inclusive диапазон строк.
- Если указан только `start_line`, чтение идет от этой строки до конца файла; если указан только `end_line`, чтение идет с 1-й строки до `end_line`.
- Если `end_line` больше количества строк в файле, диапазон мягко обрезается до конца файла.
- Если явный `start_line` находится за EOF, возвращается structured error с известным `total_lines`, а не пустой успешный ответ.
- Когда заданы оба `start_line` и `end_line`, инструмент использует быстрый bounded range path без предварительного полного подсчета строк; если EOF не достигнут, `total_lines_known=false`, а `total_lines` отсутствует.
- Выбранный файл или диапазон возвращается целиком в одном ответе.
- Содержимое файла возвращается компактной строкой `text` в формате `1|строка`.
- Метадата пути, общего числа строк и выбранного диапазона вынесена в `file`, `total_lines`, `total_lines_known`, `requested_range` и `range`.
- Пустые строки сохраняются со своими номерами.
- CRLF отображается без лишнего `\r`.
- Если файл существует, но пустой, `text=""`, `total_lines=0`, `total_lines_known=true`.
- Можно вызывать несколько `read_file` параллельно для чтения нескольких известных файлов.
- Для больших документов доступны `chunk_lines`, `count_total_lines` и `expected_version`; ответ возвращает `coverage`, `continuation` и fingerprint, когда он был рассчитан.
- `read_file` не применяет redaction; это literal exact read.

Input:

```json
{
  "target_file": "/path/to/file.txt",
  "start_line": 1,
  "end_line": 200
}
```

Copyable examples:

```json
{"target_file":"/path/to/file.go"}
{"target_file":"/path/to/file.go","start_line":8981,"end_line":9000}
```

Output:

```json
{
  "text": "1|first\n2|second\n",
  "file": "/path/to/file.txt",
  "total_lines": 2,
  "total_lines_known": true,
  "requested_range": {"start": 1, "end": 200},
  "range": {"start": 1, "end": 2}
}
```

## read_files

Читает несколько известных файлов или ranges за один вызов. Используй, когда агент уже нашел набор файлов и хочет компактный batch read без нескольких tool calls.

Использование:

- Без `cwd_id` все `items[].target_file` должны быть absolute paths; с `cwd_id` они должны быть cwd-relative.
- `items` задает список файлов/ranges; каждый item поддерживает `start_line`, `end_line`, `chunk_lines` и `expected_version`.
- `max_total_lines` и `max_total_bytes` ограничивают общий ответ; при обрезке возвращается `continuation` с готовым следующим вызовом.
- `count_total_lines=true` просит считать полный размер файлов там, где это нужно для proof-of-complete.
- `redaction_mode` по умолчанию `off`: `read_files` возвращает literal text. `strict` включает явное value-level masking для secret-like значений; `auto` оставлен как deprecated compatibility alias для `strict`.
- Каждый item возвращает `status`, `file`, `text`, `range`, `coverage`, `continuation`, `fingerprint`, `redacted` и error fields при сбое.

Input:

```json
{
  "items": [
    {"target_file": "/path/to/repo/README.md", "chunk_lines": 80},
    {"target_file": "/path/to/repo/go.mod"}
  ],
  "max_total_lines": 160,
  "count_total_lines": true,
  "redaction_mode": "off"
}
```

Output:

```json
{
  "items": [
    {
      "status": "ok",
      "file": "/path/to/repo/README.md",
      "text": "1|# title\n2|...",
      "range": {"start": 1, "end": 80},
      "total_lines_known": true,
      "coverage": {"complete_file_read": false},
      "redacted": false
    }
  ],
  "count": 1,
  "truncated": true,
  "continuation": {"complete": false}
}
```

## outline_file

Строит компактный outline и fingerprint для одного файла. Используй перед `copy_ranges`/`move_ranges`, чтобы не читать большой файл целиком и получить точные line ranges.

Использование:

- Без `cwd_id` `target_file` должен быть полным абсолютным путем для ОС, где запущен MCP-сервер.
- С `cwd_id` `target_file` должен быть относительным путем от зарегистрированного cwd.
- `language="auto"` определяет Markdown, Go, JavaScript/JSX, TypeScript/TSX, Python, Java, Rust, C/C++, C#, Ruby, Kotlin, Swift, Bash, JSON, YAML и Svelte.
- Markdown outline возвращает ATX headings с range до следующего heading того же или меньшего уровня; headings внутри fenced code blocks игнорируются.
- Go outline возвращает `import_block`, `const_block`, `var_block`, `type_block` items с полным block range и children для отдельных specs, плюс functions и methods через `go/parser`.
- Tree-sitter outline для JS/TS/TSX/Python/Java/Rust/C/C++/C#/Ruby/Kotlin/Swift/Bash/JSON/YAML/Svelte возвращает selector metadata: `symbol_ref`, `symbol_path`, `byte_range`, `whole_line_range`, `write_safe` и `range_fingerprint`.
- Source-bearing JS/TS exports like `export { x } from "pkg"` are reported as `kind="re_export"` outside `import_block`; exported declarations remain normal symbols.
- JS/TS/TSX default `output_profile="agent"` keeps high-signal top-level declarations/components and hides duplicate declaration/local-variable noise; `output_profile="full"`, `kinds`, `name_contains`, `line_window`, and `enclosing_line` expose details when needed.
- JSON/YAML config nodes are exact for navigation, but `write_safe=false` by default because moving/deleting delimiter-separated structured nodes can require comma/indent/token repair outside the selected line range.
- JSON/YAML config paths use canonical display names like `document.services[0]["api:key"]`; multi-document YAML uses `document[0]`, `document[1]`, ...
- JSON/YAML default `output_profile="agent"` keeps key/property paths but omits noisy leaf `value` items and synthetic wrapper containers such as `.object` or `["[]"]`, reporting `outline_stats.omitted_leaf_items` and a `next_recommended_call` for `output_profile="full"` when value detail is needed.
- JSON/YAML `output_profile="full"` returns the omitted leaf value items; `output_profile="fingerprint_only"` returns only fingerprint/cheap metadata. Legacy `output_profile="outline"` is accepted as `agent`.
- JSON/YAML config kinds are normalized for filtering: `document`, `object`, `array`, `property`, `value`, `stream`, `mapping`, `sequence`, `key`.
- Svelte currently returns exact block/markup ranges and `parser_status="partial"` with a nested-symbol warning; nested script symbol extraction is future-gated.
- Unsupported text получает generic text outline с honest synthetic chunks; binary/undecodable files не получают fake structure.
- Каждый exact outline item содержит `confidence="exact"`, `range_is_estimated=false` и `range_fingerprint` как fingerprint snapshot файла, для которого этот range был рассчитан; same-line/delimiter-sensitive items могут быть exact для чтения, но `write_safe=false`.
- `output_profile="fingerprint_only"` возвращает только fingerprint и cheap metadata.
- `line_window`, `enclosing_line`, `name_contains`, `kinds`, `max_items` и `max_depth` ограничивают output без cursor state; `enclosing_line` возвращает innermost item и parent chain в `enclosing_items`.
- Если output обрезан по `max_items`, `next_recommended_call` содержит bounded `line_window`; если исходный `max_items` был слишком мал для enclosing context, recommended call может поднять его на небольшой depth budget.
- Для файлов больше `MCP_WRITE_THRESHOLD` Go parser не запускается; ответ содержит actionable error и fingerprint.

Input:

```json
{
  "target_file": "/path/to/repo/concept.md",
  "language": "auto",
  "include_sections": true,
  "max_items": 80,
  "max_depth": 3
}
```

Copyable examples:

```json
{"target_file":"/path/to/repo/concept.md","language":"markdown","max_items":80}
{"target_file":"/path/to/repo/file.go","language":"go","include_imports":true,"include_symbols":true}
{"target_file":"/path/to/repo/src/App.tsx","language":"auto","enclosing_line":42}
{"target_file":"/path/to/repo/huge.md","output_profile":"fingerprint_only"}
```

Output:

```json
{
  "file": "/path/to/repo/concept.md",
  "language": "markdown",
  "parser_status": "ok",
  "parser_scope": "markdown_atx_headings",
  "fingerprint": {
    "sha256": "abc123...",
    "size_bytes": 12000,
    "line_count": 500,
    "modified_unix_nano": 1780403400000000000
  },
  "imports": [],
  "symbols": [],
  "sections": [
    {
      "id": "section:1",
      "kind": "section",
      "name": "Заголовок 1",
      "range": {"start_line": 1, "end_line": 500},
      "confidence": "exact",
      "range_is_estimated": false,
      "range_fingerprint": {
        "sha256": "abc123...",
        "size_bytes": 12000,
        "line_count": 500,
        "modified_unix_nano": 1780403400000000000
      },
      "depth": 1,
      "children": [
        {
          "id": "section:2",
          "kind": "section",
          "name": "Заголовок 2",
          "range": {"start_line": 3, "end_line": 300},
          "confidence": "exact",
          "range_is_estimated": false,
          "depth": 2
        }
      ]
    }
  ],
  "outline_stats": {"items_returned": 2, "items_omitted_known": true},
  "truncated": false,
  "warnings": []
}
```

## resolve_symbol_range

Разрешает selector из `outline_file` или `enclosing_line` в concrete line ranges без ручного переноса line numbers.

Использование:

- `source_fingerprint` обязателен и обычно берется из `outline_file`; stale fingerprint возвращает `symbol_fingerprint_mismatch`.
- `selector` поддерживает `symbol_ref`, `kind`/`name`, `symbol_path`, `range`+`range_fingerprint`, `disambiguator` и `enclosing_line`.
- Без `target_intent` tool read-only: возвращает `matches`, `resolved_ranges` и read/refresh hints.
- С `target_intent` tool все равно не мутирует файлы; он может вернуть `recommended_write_call` только как `dry_run=true` input для `copy_ranges` или `move_ranges`.
- Write recommendation появляется только для одного exact `write_safe` whole-line range: parser-backed `parser_status="ok"` или ручного exact `selector.range` с текущим `range_fingerprint` (`parser_status="range_selector"`), не same-file target и доказанного target syntax: `create_new`, clean Markdown target или explicit non-structured `plain_text`.
- `target_intent.target_precondition` можно опустить для подготовки: resolver сам вложит `must_not_exist=true` для missing `create_new` target или свежий fingerprint для существующего readable text target.
- `target_intent.dry_run` можно опустить; recommended write input всегда принудительно содержит `dry_run=true`. Apply выполняется отдельным явным вызовом range-tool после preview/read-back.

Copyable examples:

```json
{"source_file":"/path/to/repo/src/App.tsx","source_fingerprint":{"sha256":"...","size_bytes":1000,"line_count":80,"modified_unix_nano":1780403400000000000},"selector":{"symbol_ref":"tsx:function:abc..."}}
{"source_file":"/path/to/repo/src/app.py","source_fingerprint":{"sha256":"...","size_bytes":1000,"line_count":80,"modified_unix_nano":1780403400000000000},"selector":{"name":"load_config","kind":"function"},"target_intent":{"operation":"copy","target_file":"/path/to/repo/snippets.py","placement":{"mode":"create_new"}}}
```

Output shape highlights:

```json
{
  "file": "/path/to/repo/src/app.py",
  "resolution_status": "resolved",
  "resolved_ranges": [{"range": {"start_line": 10, "end_line": 14}, "write_safe": true}],
  "next_recommended_call": {"recommended_next_tool": "read_file"},
  "write_recommendation_status": "ready",
  "target_syntax_status": "safe",
  "target_syntax_proof": "create_new",
  "recommended_write_call": {
    "recommended_next_tool": "copy_ranges",
    "recommended_next_input": {
      "source_file": "/path/to/repo/src/app.py",
      "ranges": [{"start_line": 10, "end_line": 14}],
      "target_file": "/path/to/repo/snippets.py",
      "dry_run": true
    }
  }
}
```

## copy_ranges

Копирует точные line ranges из одного `source_file` в один явный `target_file`. Используй для механического переноса кода/Markdown без регенерации содержимого по памяти.

Использование:

- Без `cwd_id` `source_file` и `target_file` обязательны и должны быть полными абсолютными путями.
- С `cwd_id` `source_file` и `target_file` обязательны и должны быть относительными путями от зарегистрированного cwd.
- `source_fingerprint` обязателен; обычно берется из `outline_file`.
- `ranges` задаются как 1-based inclusive `start_line`/`end_line`, не должны пересекаться; target payload собирается в порядке ranges из запроса.
- `dry_run=true` проверяет preconditions и planned deltas без mutation и backups.
- `placement.mode="create_new"` требует `target_precondition.must_not_exist=true`.
- `append`, `prepend`, `insert_before_line` и `replace_range` требуют `target_precondition.fingerprint`.
- `joiner` допускает только `none`, `single_newline` или `blank_line`; неизвестные значения отклоняются.
- `backup.mode="sidecar"` создает backup существующего target перед mutation.
- `backup.mode` допускает только `none` или `sidecar`; неизвестные значения отклоняются.
- `boundary_warnings` возвращаются уже в `dry_run`, если inserted text может склеиться с target text без newline.
- `diff_previews` возвращает bounded unified diff того, что изменится в target; для `move_ranges` также показывает source removal.
- `joiner_effect` показывает нормализованный joiner, newline bytes, `source_boundaries`, compatibility primary `target_boundary`, all `target_boundaries`, existing boundary newlines, inserted newlines, visual blank lines, and warning codes. `blank_line` aims for one visual blank line; warnings report when existing blank lines make the result visually larger.
- `boundary_preview` показывает bounded before/after preview вокруг placement.
- Для escape-sensitive кода (`\r\n`, regex/string literals и похожее) `diff_previews` и `boundary_preview` являются display preview, а не единственным источником истины; после apply проверяй `validation.target_read_back` или делай точный `read_file`.
- После apply `validation.target_read_back` и, для move, `validation.source_read_back` перечитывают затронутое окно.
- Если sidecar backups созданы, `backup_discovery` дает готовый `glob_file_search` с `include_hidden=true`, чтобы rediscover dot-backups.
- Structured errors возвращают `error_code`, `action_hint` и fingerprints, когда их можно безопасно получить.

Input:

```json
{
  "source_file": "/path/to/repo/big.md",
  "source_fingerprint": {
    "sha256": "abc123...",
    "size_bytes": 12000,
    "line_count": 500,
    "modified_unix_nano": 1780403400000000000
  },
  "ranges": [{"start_line": 301, "end_line": 500}],
  "target_file": "/path/to/repo/part-2.md",
  "target_precondition": {"must_not_exist": true},
  "placement": {"mode": "create_new"},
  "joiner": "single_newline",
  "backup": {"mode": "none"},
  "dry_run": true
}
```

Copyable examples:

```json
{"source_file":"/path/to/repo/big.md","source_fingerprint":{"sha256":"abc123...","size_bytes":12000,"line_count":500,"modified_unix_nano":1780403400000000000},"ranges":[{"start_line":301,"end_line":500}],"target_file":"/path/to/repo/part-2.md","target_precondition":{"must_not_exist":true},"placement":{"mode":"create_new"},"dry_run":true}
{"source_file":"/path/to/repo/file.go","source_fingerprint":{"sha256":"abc123...","size_bytes":8000,"line_count":240,"modified_unix_nano":1780403400000000000},"ranges":[{"start_line":40,"end_line":120}],"target_file":"/path/to/repo/new_file.go","target_precondition":{"must_not_exist":true},"placement":{"mode":"create_new"}}
```

Output:

```json
{
  "operation": "copy",
  "dry_run": true,
  "applied": false,
  "source_file": "/path/to/repo/big.md",
  "target_file": "/path/to/repo/part-2.md",
  "ranges": [{"range": {"start_line": 301, "end_line": 500}, "line_count": 200, "byte_count": 6200}],
  "target_placement": {"mode": "create_new"},
  "would_write_bytes": 6200,
  "diff_previews": [{"role": "target", "format": "unified", "truncated": false}],
  "joiner_effect": {
    "requested": "single_newline",
    "normalized": "single_newline",
    "newline_bytes": "\\n",
    "source_range_join_count": 0,
    "inserted_newlines_between_ranges": 0
  },
  "boundary_preview": {"placement": "create_new", "truncated": false},
  "validation": {"status": "planned_only", "target_read_back": []},
  "source_fingerprint_before": {
    "sha256": "abc123...",
    "size_bytes": 12000,
    "line_count": 500,
    "modified_unix_nano": 1780403400000000000
  },
  "boundary_warnings": [],
  "warnings": [],
  "backup_paths": [],
  "backup_results": []
}
```

## move_ranges

Перемещает точные line ranges из одного `source_file` в один `target_file`, а затем удаляет эти ranges из source.

Использование:

- Input shape совпадает с `copy_ranges`.
- Target пишется первым; source изменяется только после успешного target write и повторной проверки source fingerprint.
- `dry_run=true` не мутирует файлы и не создает backups.
- Ответ содержит Phase 5 safety evidence: target/source `diff_previews`, `joiner_effect`, `boundary_preview`, post-write `validation`, `backup_results`/`backup_discovery` и fingerprints for next write.
- При сбое после target write ответ содержит `partial_state`, `files_maybe_modified`, `backup_paths` и `recovery_hint`.
- Используй для безопасного “вынести блок в новый файл” из монолитного кода или концепта.

Input:

```json
{
  "source_file": "/path/to/repo/big.md",
  "source_fingerprint": {
    "sha256": "abc123...",
    "size_bytes": 12000,
    "line_count": 500,
    "modified_unix_nano": 1780403400000000000
  },
  "ranges": [{"start_line": 301, "end_line": 500}],
  "target_file": "/path/to/repo/part-2.md",
  "target_precondition": {"must_not_exist": true},
  "placement": {"mode": "create_new"},
  "dry_run": false
}
```

Output:

```json
{
  "operation": "move",
  "dry_run": false,
  "applied": true,
  "source_file": "/path/to/repo/big.md",
  "target_file": "/path/to/repo/part-2.md",
  "ranges": [{"range": {"start_line": 301, "end_line": 500}, "line_count": 200, "byte_count": 6200}],
  "target_placement": {"mode": "create_new"},
  "bytes_written": 6200,
  "diff_previews": [
    {"role": "target", "format": "unified", "truncated": false},
    {"role": "source_removal", "format": "unified", "truncated": false}
  ],
  "joiner_effect": {"requested": "single_newline", "normalized": "single_newline", "newline_bytes": "\\n"},
  "boundary_preview": {"placement": "create_new", "truncated": false},
  "validation": {
    "status": "applied_and_verified",
    "target_read_back": [],
    "source_read_back": []
  },
  "removed_source_lines": 200,
  "removed_source_ranges": [{"start_line": 301, "end_line": 500}],
  "source_fingerprint_for_next_write": {
    "sha256": "def456...",
    "size_bytes": 5800,
    "line_count": 300,
    "modified_unix_nano": 1780403401000000000
  },
  "target_fingerprint_for_next_write": {
    "sha256": "789abc...",
    "size_bytes": 6200,
    "line_count": 200,
    "modified_unix_nano": 1780403401000000000
  },
  "boundary_warnings": [],
  "warnings": [],
  "backup_paths": [],
  "backup_results": [],
  "backup_discovery": null
}
```

## copy_ranges_batch

Копирует ranges из одного source snapshot в несколько явных targets за один вызов. Используй для декомпозиции большого файла на несколько новых файлов без нескольких чтений source.

Использование:

- Один `source_file` и массив `targets`; target paths не выбираются автоматически.
- Без `cwd_id` `source_file` и `targets[].target_file` должны быть absolute paths; с `cwd_id` они должны быть cwd-relative.
- `source_fingerprint` обязателен и проверяется перед target writes.
- Каждый target задает `target_file`, `target_precondition`, `placement`, `ranges`, optional `joiner` и `backup`.
- `dry_run=true` возвращает per-target planned deltas без mutation и backups.
- Каждый `target_results[]` содержит те же agent-safety поля, что single write: `diff_previews`, `joiner_effect`, `boundary_preview` и `validation`.
- Для copy batch пересекающиеся/дублирующиеся source ranges разрешены, но возвращаются в `batch_warnings`.
- Batch ограничен `MCP_BATCH_MAX_TARGETS`, `MCP_BATCH_MAX_RANGES_PER_TARGET`, `MCP_BATCH_MAX_RANGES_PER_CALL` и `MCP_BATCH_MAX_PLANNED_BYTES`.
- При частичном сбое возвращает `partial_state`, `target_results` и `recovery_hint`.
- Top-level byte metrics используют clear fields: `would_write_target_bytes`, `would_rewrite_source_bytes`, `would_write_total_bytes`; legacy `would_write_bytes` является alias для total.

Input:

```json
{
  "source_file": "/path/to/repo/concept.md",
  "source_fingerprint": {
    "sha256": "abc123...",
    "size_bytes": 12000,
    "line_count": 500,
    "modified_unix_nano": 1780403400000000000
  },
  "targets": [
    {
      "target_file": "/path/to/repo/part-a.md",
      "target_precondition": {"must_not_exist": true},
      "placement": {"mode": "create_new"},
      "ranges": [{"start_line": 1, "end_line": 120}]
    },
    {
      "target_file": "/path/to/repo/part-b.md",
      "target_precondition": {"must_not_exist": true},
      "placement": {"mode": "create_new"},
      "ranges": [{"start_line": 121, "end_line": 300}]
    }
  ],
  "dry_run": true
}
```

Output:

```json
{
  "operation": "copy_batch",
  "dry_run": true,
  "applied": false,
  "source_file": "/path/to/repo/concept.md",
  "target_results": [
    {
      "target_file": "/path/to/repo/part-a.md",
      "status": "planned",
      "written": false,
      "skipped": false,
      "failed": false,
      "ranges": [{"range": {"start_line": 1, "end_line": 120}, "line_count": 120, "byte_count": 3000}],
      "would_write_bytes": 3000,
      "diff_previews": [{"role": "target", "format": "unified"}],
      "validation": {"status": "planned_only", "target_read_back": []}
    }
  ],
  "targets_written": [],
  "would_write_target_bytes": 3000,
  "would_rewrite_source_bytes": 0,
  "would_write_total_bytes": 3000,
  "would_write_bytes": 3000,
  "batch_warnings": [],
  "warnings": [],
  "backup_paths": [],
  "backup_results": []
}
```

## move_ranges_batch

Перемещает ranges из одного source snapshot в несколько явных targets, затем удаляет union moved ranges из source один раз.

Использование:

- Input shape похож на `copy_ranges_batch`, плюс optional `source_backup`.
- Без `cwd_id` `source_file` и `targets[].target_file` должны быть absolute paths; с `cwd_id` они должны быть cwd-relative.
- Все target writes выполняются до изменения source.
- Для move batch пересекающиеся/дублирующиеся moved ranges across targets запрещены.
- Если target write или source recheck fails, source остается неизмененным, а `partial_state` показывает written/failed/skipped targets.
- Это основной ergonomic path для декомпозиции большого Markdown concept на несколько файлов.
- Output включает `source_diff_previews` и `source_validation` для source removal/read-back.
- Top-level byte metrics разделяют target writes и source rewrite: `would_write_target_bytes`, `would_rewrite_source_bytes`, `would_write_total_bytes`, а applied output использует `bytes_written_target_bytes`, `bytes_rewritten_source_bytes`, `bytes_written_total_bytes`; legacy `would_write_bytes`/`bytes_written` являются aliases для total.

Input:

```json
{
  "source_file": "/path/to/repo/concept.md",
  "source_fingerprint": {
    "sha256": "abc123...",
    "size_bytes": 12000,
    "line_count": 500,
    "modified_unix_nano": 1780403400000000000
  },
  "targets": [
    {
      "target_file": "/path/to/repo/part-a.md",
      "target_precondition": {"must_not_exist": true},
      "placement": {"mode": "create_new"},
      "ranges": [{"start_line": 1, "end_line": 120}]
    },
    {
      "target_file": "/path/to/repo/part-b.md",
      "target_precondition": {"must_not_exist": true},
      "placement": {"mode": "create_new"},
      "ranges": [{"start_line": 121, "end_line": 300}]
    }
  ],
  "source_backup": {"mode": "sidecar"},
  "dry_run": false
}
```

Output:

```json
{
  "operation": "move_batch",
  "dry_run": false,
  "applied": true,
  "source_file": "/path/to/repo/concept.md",
  "target_results": [
    {
      "target_file": "/path/to/repo/part-a.md",
      "status": "written",
      "written": true,
      "skipped": false,
      "failed": false,
      "ranges": [{"range": {"start_line": 1, "end_line": 120}, "line_count": 120, "byte_count": 3000}],
      "bytes_written": 3000,
      "target_fingerprint_for_next_write": {"sha256": "abc123..."}
    }
  ],
  "targets_written": ["/path/to/repo/part-a.md", "/path/to/repo/part-b.md"],
  "bytes_written_target_bytes": 6000,
  "bytes_rewritten_source_bytes": 6000,
  "bytes_written_total_bytes": 12000,
  "bytes_written": 12000,
  "removed_source_lines": 300,
  "removed_source_ranges": [{"start_line": 1, "end_line": 300}],
  "source_fingerprint_for_next_write": {
    "sha256": "def456...",
    "size_bytes": 6000,
    "line_count": 200,
    "modified_unix_nano": 1780403401000000000
  },
  "batch_warnings": [],
  "warnings": [],
  "backup_paths": ["/path/to/repo/.concept.md.20260605T120000Z.ab12cd34.1.bak"],
  "backup_results": [
    {
      "role": "source",
      "created": true,
      "backup_path": "/path/to/repo/.concept.md.20260605T120000Z.ab12cd34.1.bak"
    }
  ],
  "backup_discovery": {
    "backup_paths": ["/path/to/repo/.concept.md.20260605T120000Z.ab12cd34.1.bak"],
    "reason": "Sidecar backups are hidden dot-files; use include_hidden=true to rediscover them.",
    "next_recommended_call": {
      "recommended_next_tool": "glob_file_search",
      "recommended_next_input": {
        "target_directory": "/path/to/repo",
        "glob_pattern": ".*.bak",
        "include_hidden": true
      }
    }
  }
}
```

## list_dir

Выводит прямое содержимое одной директории в компактном формате. Используй перед чтением файлов, чтобы быстро понять структуру директории.

Использование:

- Без `cwd_id` `target_directory` должен быть полным абсолютным путем для ОС, где запущен MCP-сервер.
- С `cwd_id` `target_directory` должен быть относительным путем от зарегистрированного cwd.
- Результат возвращает `directory`, `count` и массив `entries`; каждый entry имеет `name` и `kind` (`file` или `directory`).
- Dot-files и dot-directories скрываются; `dot_entries_skipped=true` показывает, что такие элементы реально встретились и были скрыты.
- `include_hidden=true` показывает dot-files/dot-directories, кроме VCS metadata; `include_vcs_metadata=true` явно включает VCS metadata where supported.
- `ignore_globs` пропускает имена и `**` patterns, например `node_modules/**` или `*.tmp`.
- Инструмент не рекурсивен; для рекурсивного поиска используй `glob_file_search`.
- Пустая или полностью отфильтрованная директория возвращает понятное сообщение и подсказку.
- Список возвращается целиком в одном ответе.

Input:

```json
{
  "target_directory": "/path/to/dir",
  "ignore_globs": ["node_modules/**", "*.tmp"],
  "include_hidden": false
}
```

Copyable examples:

```json
{"target_directory":"/path/to/repo","ignore_globs":["node_modules/**","*.tmp"]}
```

Output:

```json
{
  "directory": "/path/to/dir",
  "count": 3,
  "dot_entries_skipped": false,
  "entries": [
    {"name": "cmd", "kind": "directory"},
    {"name": "go.mod", "kind": "file"},
    {"name": "README.md", "kind": "file"}
  ]
}
```

## glob_file_search

Ищет файлы по glob pattern. Используй, когда нужно найти файлы по имени, расширению или форме пути.

Использование:

- Поиск выполняется по локальной файловой системе.
- Без `cwd_id` `target_directory` должен быть полным абсолютным путем для ОС, где запущен MCP-сервер.
- С `cwd_id` `target_directory` должен быть относительным путем от зарегистрированного cwd.
- Простые паттерны вроде `*.go` ищут рекурсивно по имени файла.
- Поддерживаются `**` recursive patterns, включая несколько `**` в одном паттерне, например `**/fixtures/**/*.go`.
- Паттерны вида `**/main.go` также совпадают с `main.go` на верхнем уровне.
- Поддерживается простое brace expansion, например `*.{ts,tsx}`.
- Dot-files и dot-directories пропускаются при обходе; `dot_entries_skipped=true` показывает, что такие элементы реально встретились и были скрыты.
- `include_hidden=true` включает hidden files/dirs; `include_vcs_metadata=true` включает VCS metadata, но high-volume internals still stay bounded.
- `ignore_globs` пропускает совпавшие файлы и директории; директории вроде `node_modules/**` и `vendor/**` не обходятся.
- `sort` поддерживает `modified_desc`, `modified_asc`, `path_asc`, `path_desc`, `size_desc`, `size_asc`, `directory_path_asc`.
- `limit` задает максимум файлов в `files`; если не указан, используется `50`.
- Если `truncated=true`, используй `continuation.next_recommended_call.recommended_next_input` или передай `continuation_after={"canonical_query_hash": continuation.canonical_query_hash, "last_sort_key": continuation.last_sort_key}`; `continuation.consistency` остается `unknown`, потому что стабильность дерева между вызовами не доказана.
- `total_match_count` показывает общее число найденных файлов до ограничения, `truncated=true` означает, что `files` обрезан по `limit`.
- Каждый файл в `files` содержит `path`, `modified_at` и `modified_unix_nano`, когда timestamp доступен.
- `search_stats` объясняет hidden/VCS/ignored/binary/unreadable skips; `groups` группирует файлы по директориям для `directory_path_asc`.
- Если полный результат содержит 1-6 text-like файлов, `next_recommended_calls` дает bounded `read_files`; если найден ровно один source/config-like файл, первым hint будет `outline_file`.
- Для broad/truncated результатов tool не предлагает шумный batch read; используй continuation или сужай `target_directory`/`glob_pattern`.
- Слишком широкий поиск по большой директории может быть медленным и шумным; сужай `target_directory` или `glob_pattern`, особенно если рядом есть `vendor` или `node_modules`.
- Результат возвращается целиком в одном ответе.

Input:

```json
{
  "glob_pattern": "**/*.go",
  "target_directory": "/path/to/repo",
  "ignore_globs": ["node_modules/**", "vendor/**"],
  "include_hidden": false,
  "sort": "modified_desc",
  "limit": 50
}
```

Copyable examples:

```json
{"glob_pattern":"*.go","target_directory":"/path/to/repo"}
{"glob_pattern":"**/fixtures/**/*.go","target_directory":"/path/to/repo"}
{"glob_pattern":"*.go","target_directory":"/path/to/repo","ignore_globs":["node_modules/**","vendor/**"],"limit":25}
```

Output:

```json
{
  "pattern": "**/*.go",
  "target_directory": "/path/to/repo",
  "limit": 50,
  "count": 2,
  "total_match_count": 2,
  "truncated": false,
  "dot_entries_skipped": false,
  "files": [
    {"path": "/path/to/repo/main.go", "modified_at": "2026-06-02T12:30:00Z", "modified_unix_nano": 1780403400000000000},
    {"path": "/path/to/repo/internal/server.go", "modified_at": "2026-06-01T18:10:44Z", "modified_unix_nano": 1780337444000000000}
  ]
}
```

## grep

Мощный поиск по содержимому, похожий на ripgrep. Используй для точного поиска символов и строк, когда нужен MCP-friendly вывод.

Использование:

- Поиск выполняется по локальной файловой системе.
- Без `cwd_id` `path` должен быть полным абсолютным путем к файлу или директории для ОС, где запущен MCP-сервер.
- С `cwd_id` `path` должен быть относительным путем от зарегистрированного cwd.
- Использует Go regexp syntax, например `log.*Error` или `function\\s+\\w+`.
- `pattern_mode="literal"` ищет точный текст без ручного regexp escaping; для exact text это preferred path.
- В `regex` mode экранируй спецсимволы, например `functionCall\\(` или `interface\\{\\}`.
- Избегай слишком широкого `path` или `glob` на больших деревьях: локальный обход может быть медленным и может дать много шума из `vendor` или `node_modules`.
- Используй `type` или `glob` только когда уверен в нужном типе файла; пути импортов могут не совпадать с типами исходных файлов.
- `ignore_globs` пропускает совпавшие файлы и директории; директории вроде `node_modules/**` и `vendor/**` не обходятся.
- При обходе директорий dot-files и dot-directories пропускаются; `dot_entries_skipped=true` показывает, что такие элементы реально встретились и были скрыты. Чтобы искать скрытый файл, передай его точный `path`.
- `include_hidden=true` разрешает traversal hidden working-tree files; `include_vcs_metadata` intentionally unsupported for grep and returns `vcs_content_traversal_unsupported`.
- `redaction_mode` по умолчанию `off`: grep возвращает literal matches. `strict` включает explicit secret-like value masking; `auto` оставлен как deprecated compatibility alias для `strict`.
- Режимы вывода: `content` показывает совпадающие строки, `files_with_matches` показывает только пути файлов, `count` показывает количество совпадений по файлам.
- `content` возвращает массив `matches`, где `kind=match` для совпадений и `kind=context` для контекстных строк.
- Контекстные строки в `content` объединяются по строкам файла: соседние контекстные диапазоны не дублируют одну и ту же строку.
- `files_with_matches` возвращает массив `files`.
- `count` возвращает массив `counts` с `path` и `count`.
- `before` задает строки до совпадения, `after` строки после, `context` строки до и после; `context` имеет приоритет над `before`/`after`.
- `line_window` ограничивает поиск известным диапазоном строк одного файла и сохраняет исходные номера строк.
- `max_matches_per_file` ограничивает число match-строк или логических multiline matches на файл в `content` mode; exact cap без скрытых совпадений не считается truncation.
- `case_insensitive` включает поиск без учета регистра.
- `limit` задает максимум строк/файлов/count rows в результате; если не указан, используется `50`.
- `truncated=true` означает, что выдача неполная из-за `limit`, `max_matches_per_file` или unreadable выбранных файлов; `match_count` и `row_count` относятся к возвращенной части результата.
- `search_stats` объясняет полноту обхода и причины остановки; binary skips сами по себе не делают результат incomplete.
- `file_groups` группирует `content` matches по файлам и дает `read_ranges` для следующего `read_file`/`read_files`.
- Для полного узкого `content` результата `next_recommended_call` первым дает готовый `read_file` или `read_files` input: максимум 6 файлов, 12 ranges всего и 3 ranges на файл. При ровно одном source/config-like файле `next_recommended_calls` также дает `outline_file` с bounded `line_window`.
- Для broad/truncated результатов tool рекомендует narrowing/per-file cap/files mapping вместо шумного batch read.
- `type` фильтрует распространенные типы файлов: `go`, `ts`, `py`, `json`, `yaml`, `md`, `tf` и другие.
- `glob` фильтрует имена/пути файлов, включая `**` и brace patterns вроде `*.{ts,tsx}`.
- По умолчанию паттерны совпадают только в пределах одной строки.
- Для кросс-строчных паттернов вроде `struct \\{[\\s\\S]*?field` используй `multiline: true`.
- В `multiline=true` поле `match_count` считает логические regex-совпадения, а не количество строк, которыми показано одно многострочное совпадение.
- Binary files пропускаются.
- Результат поиска возвращается целиком в одном ответе.
- Некорректный regex, неверный `output_mode`, плохой `path` и отсутствие совпадений возвращают дружелюбные сообщения с подсказками.

Input:

```json
{
  "pattern": "needle",
  "pattern_mode": "regex",
  "path": "/path/to/repo",
  "output_mode": "content",
  "after": 1,
  "before": 1,
  "context": 0,
  "case_insensitive": false,
  "type": "go",
  "glob": "**/*.go",
  "ignore_globs": ["node_modules/**", "vendor/**"],
  "include_hidden": false,
  "redaction_mode": "off",
  "multiline": false,
  "line_window": {"start_line": 1, "end_line": 200},
  "max_matches_per_file": 5,
  "limit": 50
}
```

Copyable examples:

```json
{"pattern":"Handle","path":"/path/to/repo","type":"go","limit":50}
{"pattern":"TODO","path":"/path/to/repo","glob":"*.{go,ts}","ignore_globs":["node_modules/**","vendor/**"],"case_insensitive":true}
{"pattern":"functionCall(","pattern_mode":"literal","path":"/path/to/repo","type":"go"}
{"pattern":"struct \\\\{[\\\\s\\\\S]*?field","path":"/path/to/repo","multiline":true}
```

Output examples:

```json
{
  "pattern": "needle",
  "pattern_mode": "regex",
  "path": "/repo",
  "output_mode": "content",
  "context_before": 0,
  "context_after": 1,
  "limit": 50,
  "matches": [
    {"path": "/repo/main.go", "line": 10, "kind": "match", "text": "needle := value"},
    {"path": "/repo/main.go", "line": 11, "kind": "context", "text": "next line as context"}
  ],
  "files": [],
  "counts": [],
  "match_count": 1,
  "row_count": 2,
  "truncated": false,
  "dot_entries_skipped": false,
  "search_stats": {
    "files_seen": 12,
    "files_searched": 10,
    "files_with_matches": 1,
    "skipped_hidden": 1,
    "skipped_ignored": 1,
    "skipped_vcs": 0,
    "skipped_binary": 0,
    "skipped_unreadable": 0,
    "skipped_type_or_glob": 0,
    "files_capped": 0,
    "completed": true,
    "counts_are_complete": true
  },
  "file_groups": [
    {"path": "/repo/main.go", "match_count": 1, "row_count": 2, "first_line": 10, "last_line": 11, "read_ranges": [{"start_line": 10, "end_line": 11}]}
  ]
}
```

```json
{
  "pattern": "needle",
  "pattern_mode": "regex",
  "path": "/repo",
  "output_mode": "count",
  "limit": 50,
  "matches": [],
  "files": [],
  "counts": [
    {"path": "/repo/main.go", "count": 3}
  ],
  "match_count": 3,
  "row_count": 1,
  "truncated": false,
  "dot_entries_skipped": false,
  "search_stats": {
    "files_seen": 12,
    "files_searched": 10,
    "files_with_matches": 1,
    "skipped_hidden": 1,
    "skipped_ignored": 1,
    "skipped_vcs": 0,
    "skipped_binary": 0,
    "skipped_unreadable": 0,
    "skipped_type_or_glob": 0,
    "files_capped": 0,
    "completed": true,
    "counts_are_complete": true
  },
  "file_groups": []
}
```

## inspect_path

Возвращает полезную метаинформацию по одному пути без рекурсивного обхода.

Использование:

- Без `cwd_id` `target_path` должен быть полным абсолютным путем для ОС, где запущен MCP-сервер.
- С `cwd_id` `target_path` должен быть относительным путем от зарегистрированного cwd.
- Для отсутствующего абсолютного пути возвращается успешный JSON с `exists=false` и `kind=missing`.
- Для текстового файла возвращаются размер, `line_count`, timestamps, `is_binary`, readable-флаг и encoding hints; для бинарных файлов `line_count` не возвращается.
- Для директории возвращаются только прямые счетчики `direct_file_count` и `direct_dir_count`; dot-files и dot-directories не считаются.
- `discovery_context` объясняет, почему конкретный path был бы показан или скрыт `list_dir`/`glob_file_search`/`grep` при заданных flags.
- Ответ включает cheap `mime_hint`, `binary_preview_available=false` для binary/doc previews outside current scope, и `visibility` при discovery context.
- Для symlink возвращаются `symlink_target`, `symlink_target_kind` и `broken_symlink`, когда применимо; без `cwd_id` `symlink_target` нормализуется в абсолютный/display path.
- В cwd mode symlink target внутри cwd возвращается cwd-relative; target вне cwd не выводится как path, вместо этого ставится `symlink_target_outside_cwd=true`.
- `line_count` считается display-line моделью, общей с `read_file`, `outline_file` и fingerprints: empty file -> `0`, `a` -> `1`, `a\n` -> `2`.
- Инструмент не форматирует содержимое файла и не обходит директорию рекурсивно.

Input:

```json
{
  "target_path": "/path/to/repo/README.md",
  "discovery_context": {
    "target_directory": "/path/to/repo",
    "include_hidden": false,
    "glob_pattern": "**/*.md"
  }
}
```

Copyable examples:

```json
{"target_path":"/path/to/repo/README.md"}
{"target_path":"/path/to/repo/internal"}
```

Output:

```json
{
  "path": "/path/to/repo/README.md",
  "resolved_path": "/path/to/repo/README.md",
  "name": "README.md",
  "extension": ".md",
  "exists": true,
  "kind": "file",
  "size_bytes": 1200,
  "line_count": 42,
  "modified_at": "2026-06-02T12:30:00Z",
  "modified_unix_nano": 1780403400000000000,
  "mode": "-rw-r--r--",
  "permissions": "-rw-r--r--",
  "is_hidden": false,
  "is_readable": true,
  "is_binary": false,
  "encoding": "utf-8",
  "detected_encoding": "utf-8",
  "encoding_confidence": 100
}
```

## workspace_inventory

Строит карту workspace только из директорий. Файлы не перечисляются: каждая директория показывает только `direct_file_count`.

Использование:

- Без `cwd_id` `target_directory` должен быть полным абсолютным путем для ОС, где запущен MCP-сервер.
- С `cwd_id` `target_directory` должен быть относительным путем от зарегистрированного cwd.
- `max_depth` ограничивает вложенность дерева директорий; если не указан, используется `4`.
- `limit` ограничивает количество directory nodes в ответе, включая root; если не указан, используется `200`.
- Dot-directories и dot-files пропускаются; `dot_entries_skipped=true` показывает, что такие элементы реально встретились и были скрыты.
- `include_hidden=true` включает hidden working-tree entries; `include_vcs_metadata=true` явно включает VCS metadata while keeping high-volume internals bounded.
- `ignore_globs` пропускает совпавшие файлы и директории; директории вроде `node_modules/**` и `vendor/**` не обходятся.
- `direct_file_count` считает только прямые файлы внутри конкретной директории, без рекурсии.
- `directories` содержит вложенные directory nodes с той же формой.
- `directories_page` возвращает flattened directory page в deterministic path order; `summary` дает page-local file type counts, package/source/test hints, largest dirs and backup rediscovery hints.
- `summary_profile` поддерживает `compact` (по умолчанию), `none` (не возвращать `summary`) и `extended` (тот же summary payload, зарезервирован для будущего расширения).
- `continuation.page_complete` описывает полноту текущей returned page. Старое `continuation.complete` оставлено как совместимый page-complete alias.
- `summary.complete` оставлено как совместимый alias для `summary.summary_coverage_complete`.
- `summary.summary_coverage_complete`, `summary.tree_scan_complete`, `summary.summary_incomplete_reason` и `summary.scan_scope` явно отделяют coverage summary/tree от page pagination. `scan_scope` использует закрытые значения вроде `requested_depth`, `max_depth_limited`, `page_limited`, `scan_limited` и `continuation_page`.
- Если `truncated=true`, используй `continuation.next_recommended_call.recommended_next_input` или передай `continuation_after={"canonical_query_hash": continuation.canonical_query_hash, "last_sort_key": continuation.last_sort_key}`, чтобы получить следующую directory page без дублей в неизменившемся дереве; `continuation.consistency` остается `unknown`, если стабильность дерева не доказана.
- Если карта полная и есть директория с файлами, `next_recommended_call` может предложить bounded `glob_file_search`; `workspace_inventory` не угадывает file-specific `read_files` или `outline_file`.
- Directory node содержит `path` в текущем output mode: slash-normalized absolute/display без `cwd_id` или cwd-relative с `cwd_id`; отдельного relative-поля нет.
- Инструмент делает один shallow `ReadDir` на посещенную директорию и не собирает имена файлов в память.

Input:

```json
{
  "target_directory": "/path/to/repo",
  "max_depth": 2,
  "limit": 200,
  "ignore_globs": ["node_modules/**", "vendor/**"],
  "include_summary": true
}
```

Copyable examples:

```json
{"target_directory":"/path/to/repo","max_depth":2}
{"target_directory":"/path/to/repo","max_depth":3,"limit":100,"ignore_globs":["node_modules/**","vendor/**"]}
```

Output:

```json
{
  "max_depth": 2,
  "limit": 200,
  "directory_count": 3,
  "ignored_directory_count": 0,
  "dot_entries_skipped": false,
  "truncated": false,
  "max_depth_reached": false,
  "summary": {
    "complete": true,
    "summary_coverage_complete": true,
    "tree_scan_complete": true,
    "scan_scope": "requested_depth",
    "profile": "compact"
  },
  "continuation": {
    "complete": true,
    "page_complete": true,
    "consistency": "unknown"
  },
  "root": {
    "name": "repo",
    "path": "/path/to/repo",
    "depth": 0,
    "direct_file_count": 4,
    "direct_dir_count": 2,
    "truncated": false,
    "directories": [
      {
        "name": "cmd",
        "path": "/path/to/repo/cmd",
        "depth": 1,
        "direct_file_count": 1,
        "direct_dir_count": 0,
        "truncated": false,
        "directories": []
      },
      {
        "name": "internal",
        "path": "/path/to/repo/internal",
        "depth": 1,
        "direct_file_count": 0,
        "direct_dir_count": 1,
        "truncated": false,
        "directories": [
          {
            "name": "server",
            "path": "/path/to/repo/internal/server",
            "depth": 2,
            "direct_file_count": 3,
            "direct_dir_count": 0,
            "truncated": false,
            "directories": []
          }
        ]
      }
    ]
  }
}
```
