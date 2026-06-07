# Phase 5 Agent Safe Write, Read, And Discovery Concept

concept_version_label: phase5-agent-safe-write-read-discovery-v1
status: clean_product_owner_reviewed_pending_user_ok

## Goal

Сделать текущие file-tools удобными для agent workflow уровня 10/10 вокруг чтения, поиска файлов, безопасной записи и восстановления.

Phase 5 должна закрыть главный практический разрыв после Phase 4: агент уже умеет хорошо найти место через `grep`, но при реальной работе с файлами ему все еще приходится вручную перепроверять слишком много мелочей:

- что именно изменит write-tool;
- не склеятся ли строки из-за `joiner`;
- где лежит backup, если он был создан;
- почему файл не появился в `list_dir`, `glob_file_search` или `workspace_inventory`;
- прочитан ли нужный документ полностью или только кусок;
- как продолжить большой результат без угадывания;
- не показал ли tool raw secret-like value там, где нужен только безопасный audit.

Идеальный результат Phase 5: агент может пройти полный цикл "найти -> прочитать -> доказать покрытие -> preview diff -> применить -> проверить read-back -> восстановиться при сбое" без raw shell и без гадания по скрытым файлам.

## User / Scenario

Основной пользователь - coding agent, который делает repo review, refactor, документационную декомпозицию, тестовые fixture changes или точечные правки в проекте.

Типичный сценарий:

1. Агент находит файл или группу файлов.
2. Агент читает нужный контекст и честно знает, полное это чтение или частичное.
3. Агент готовит перенос/копирование ranges через `copy_ranges*` или `move_ranges*`.
4. В `dry_run` агент видит unified diff и boundary model, а не только byte counts.
5. После write агент получает read-back окна и fingerprints, чтобы убедиться, что change выглядит sane.
6. Если была ошибка или backup, агент может снова найти backup и понять recovery path.

Человек не должен помогать агенту выбирать hidden flags, искать dot-backups руками или перечитывать результат после каждого write из-за неочевидного newline behavior.

## What 10/10 Means

Phase 5 считается удачной, когда:

1. Каждый write-tool умеет дать bounded unified diff preview до записи.
2. `joiner` и newline boundaries становятся явными: агент видит, какие строки/пустые строки появятся между source payload и target.
3. После write tool возвращает достаточно read-back/validation metadata, чтобы агент мог подтвердить фактическое изменение без обязательного второго ручного lookup.
4. Backups discoverable: если tool создал backup, агент может найти его позже через обычные file-tools, даже если это dot-file.
5. Hidden/dotfile discovery становится явным opt-in, но безопасный default "не обходить hidden broadly" сохраняется.
6. Agent может объяснить отсутствие файла: hidden, ignored, glob mismatch, binary, unreadable, outside cwd или реально missing.
7. Большие чтения и большие inventories получают continuation contract с proof-of-complete, а не только `truncated=true`.
8. `glob_file_search` и `workspace_inventory` становятся полезнее для первого обзора проекта, не только для списка path rows.
9. Secret-like values не попадают в broad search/preview/error outputs случайно.
10. Все новые path-bearing поля сохраняют cwd-aware контракт Phase 3: с `cwd_id` пути относительные, без `cwd_id` slash-normalized absolute paths.

## Scope

### C-001: Preserve Existing Tool Contracts

Phase 5 улучшает текущие инструменты additive-полями, опциями и, если SRS подтвердит пользу, небольшими helper tools.

Существующее поведение должно сохраниться:

- `set_cwd` остается единственным способом получить `cwd_id`;
- без `cwd_id` path inputs остаются absolute-only;
- с `cwd_id` path inputs остаются cwd-relative;
- path outputs с `cwd_id` остаются cwd-relative, кроме absolute `cwd`;
- structured JSON output и пустой plain-text MCP content сохраняются;
- write-tools требуют explicit target path, fingerprint preconditions, limits и `dry_run`;
- hidden/dot traversal не включается по умолчанию.

### C-002: Unified Diff Preview For Write Tools

`copy_ranges`, `move_ranges`, `copy_ranges_batch` и `move_ranges_batch` должны показывать agent-friendly unified diff в `dry_run`.

Diff preview должен отвечать на вопрос агента: "Что именно станет другим?"

Для single-target tools:

- target diff показывает planned insertion/replacement/create;
- для `move_ranges` source diff показывает planned removal;
- diff имеет bounded output и честный `diff_truncated`, если полный diff слишком большой.

Для batch tools:

- per-target diff preview должен быть в `target_results[]`;
- top-level summary должен показывать, какие target/source diffs есть и какие были truncated;
- source removal/rewrite preview для `move_ranges_batch` должен быть отдельным от target writes.

Byte counts остаются полезной metadata, но не являются главным proof для агента.

### C-003: Explicit Joiner And Boundary Model

`joiner` должен перестать быть "угадай, сколько newline появится".

Phase 5 должна зафиксировать:

- что означает `none`;
- что означает `single_newline`;
- что означает `blank_line`;
- как tool ведет себя, если source payload или target уже заканчиваются newline;
- какие before/after boundary snippets показываются в `dry_run`;
- когда warning является предупреждением, а когда операция должна отказать.

Если текущий `blank_line` может визуально дать не ту пустую строку, которую ожидает агент, это считается bug/UX defect для Phase 5.

### C-004: Post-Write Validation And Read-Back

После successful write output должен давать не только fingerprints, но и bounded evidence:

- affected target windows;
- affected source windows for move operations;
- before/after line ranges, если их можно вычислить честно;
- `next_recommended_call` на `read_file` только когда встроенного read-back недостаточно;
- validation status, который отличает "write applied and inspected" от "write applied but read-back failed".

Это не заменяет тесты проекта. Это локальная проверка file-tool результата.

### C-005: Backup Discoverability

Sidecar backups не должны быть "потеряны", если агент потерял `backup_paths` из одного ответа.

Phase 5 должна сделать backups discoverable через обычный безопасный workflow:

- `backup_results[]` остается в write outputs;
- backup paths должны быть cwd-projected как и остальные paths;
- `list_dir` / `glob_file_search` / `workspace_inventory` должны иметь явный hidden-aware mode, достаточный для поиска sidecar backups;
- backup discovery не должен требовать raw shell;
- tool должен давать recovery-oriented hint, если backup создан, но обычный listing его скрывает.

Phase 5 не обязана добавлять автоматический restore tool. Восстановление может остаться явной операцией, но backup нельзя делать труднообнаружимым.

### C-006: Hidden, Ignored, And "Explain Missing" Discovery

Добавить явный, безопасный hidden/discovery model для file listing/search tools.

Required behavior:

- default остается "skip dot-prefix files/directories during broad traversal";
- opt-in hidden mode может показать dotfiles/dotdirs для `list_dir`, `glob_file_search` и `workspace_inventory`;
- exact hidden path lookup остается разрешенным, как сейчас;
- VCS metadata directories вроде `.git`, `.hg`, `.svn`, `.jj` не должны случайно попадать в broad content flows без отдельного строгого решения;
- tools должны объяснять, почему path/result отсутствует, когда это можно проверить дешево и безопасно.

Examples of explain reasons:

- missing;
- hidden and hidden mode is off;
- ignored by `ignore_globs`;
- excluded by glob/type filter;
- binary;
- unreadable;
- outside cwd;
- symlink target outside cwd;
- skipped VCS metadata.

### C-007: Read Completeness, Chunk Continuation, And Batch Read

`read_file` должен помочь агенту доказать, что он прочитал нужный документ полностью или честно остановился на частичном чтении.

Required direction:

- bounded range output должен явно говорить: "это полный requested range" отдельно от "известен ли total_lines файла";
- должен быть opt-in способ узнать `total_lines` для bounded range, когда агенту это важнее скорости;
- для больших документов нужен continuation contract: next range, proof of coverage, и понятный terminal condition;
- `read_file` должен уметь рекомендовать следующий chunk без server session state;
- для нескольких известных файлов должен быть batch read path, чтобы агент не тратил лишние tool calls и tokens на однотипные `read_file` вызовы.

Proof-of-complete важнее, чем слово `cursor`. Если будет cursor, он должен быть reconstructible/stateless enough, not chat/session-bound.

### C-008: Cursor Or Continuation For Truncated Results

`max_items`, `limit` и `truncated` должны стать actionable.

Phase 5 должна дать continuation для:

- `glob_file_search`;
- `workspace_inventory`;
- `outline_file` generic/large output where applicable;
- possibly `grep` only if Phase 4 next-call hints are not enough for broad hidden/discovery use.

Continuation must:

- preserve query parameters;
- be stable enough for agent retry;
- include cwd-aware recommended input;
- avoid hidden server state that disappears with chat/session;
- be honest when filesystem changes make exact continuation impossible.

### C-009: Richer Workspace Inventory

`workspace_inventory` сейчас полезен как directory-only map, но для первого обзора агенту нужна summary layer.

Phase 5 should add project overview metadata such as:

- file type counts;
- largest directories by direct/recursive size when cheap enough;
- source/test split hints;
- package/config hints (`go.mod`, `package.json`, `pyproject.toml`, `Cargo.toml`, etc.);
- hidden/skipped/ignored summary;
- top-level recommended next calls.

It must not become a full indexing engine.

### C-010: Glob Sort Modes And Grouping

`glob_file_search` default newest-first остается, но agent needs other sort modes:

- path order for deterministic review;
- size order for cleanup/large-file inspection;
- grouped-by-directory for project navigation;
- modified order for recent-change workflows.

The result should echo the applied sort mode and preserve stable tie-breaking.

### C-011: Secret-Safe Search And Preview Surfaces

Secret-redaction is a safety contract, not a cosmetic feature.

Phase 5 should add a redaction mode for risky broad content surfaces:

- content search in hidden/config/log-like files;
- write diff previews that might include secret-like values;
- explain/preview outputs that quote file content.

The safe direction is:

- redact secret-like values by default where the feature intentionally broadens hidden/config/log discovery;
- expose names/keys and match locations without dumping values;
- never place raw secret-like literals in error messages, logs, docs examples, or recovery hints;
- if raw output is ever considered, it requires a separate explicit safety decision outside normal concept acceptance.

### C-012: More Specific Error Codes

Generic write errors make recovery harder.

Phase 5 should split common error classes into stable `error_code` values, for example:

- `invalid_joiner`;
- `invalid_placement`;
- `invalid_backup_mode`;
- `source_fingerprint_mismatch`;
- `target_fingerprint_mismatch`;
- `range_out_of_bounds`;
- `hidden_excluded`;
- `glob_mismatch`;
- `cursor_stale`;
- `post_write_validation_failed`.

Exact names belong in SRS, but the product requirement is stable: error codes should guide the next action, not only label everything `tool_error` or `refactor_error`.

### C-013: Windows And Codex App Ergonomics

Phase 5 must preserve and polish Windows behavior:

- slash-normalized output remains `D:/...`, not escaped backslashes;
- paths with spaces and Cyrillic continue to work;
- CRLF preservation remains explicit;
- final app-facing paths remain easy for Codex to convert to absolute links when needed;
- drive roots, symlinks/junctions and outside-cwd targets remain safe and clearly reported.

### C-014: Scoped Safe Cleanup

Cleanup is useful, but destructive.

SRS resolution: Phase 5 does not add any cleanup/delete helper. It only makes backups and hidden files easier to rediscover.

Cleanup remains future-gated until a later concept/SRS can prove:

- dry-run first;
- workspace/cwd boundary proof;
- only explicit tool-created artifacts or explicit test fixture paths;
- no broad `git clean` equivalent;
- no deletion of user files without precise input;
- output must include would-delete diff/list, backup/recovery stance, and refusal conditions.

If this cannot be made narrow and safe, Phase 5 should defer cleanup while still improving backup discovery.

### C-015: Binary Preview Is Not Core

Binary metadata from `inspect_path` is useful. Full PDF/image/doc extraction is not Phase 5 core.

Allowed Phase 5 direction:

- better MIME/extension metadata if cheap;
- thumbnails/text extraction only as a future phase unless SRS finds a low-risk local implementation.

## Out Of Scope

- No language-aware outline expansion; that is Phase 6.
- No symbol-based copy/move or structural AST edit; that is Phase 6.
- No LSP, semantic index, embeddings, or project-wide type resolution.
- No cross-file rename.
- No automatic restore from backup.
- No broad destructive cleanup without dry-run and explicit scope.
- No raw secret display in broad hidden/config/log workflows.
- No hidden default cwd or session-bound cursor state.
- No change to default dotfile/VCS skip behavior unless explicitly requested in SRS and reviewed as a safety change.

## Must Not Break

- Existing tools and old inputs continue working.
- Existing `grep` Phase 4 features continue working.
- Existing `cwd_id` path projection stays consistent in every new field.
- Write tools remain explicit-target and fingerprint-gated.
- `dry_run=true` remains non-mutating and creates no backups.
- Large-file limits and concurrency limits remain respected.
- Structured tool errors remain machine-readable and plain-text MCP content remains empty.

## Success

Phase 5 succeeds when an agent can safely perform a realistic file refactor using only file-tools:

1. Find hidden or non-hidden target files intentionally.
2. Read enough context with proof of coverage.
3. Prepare a write operation and inspect a unified diff preview.
4. Understand newline/joiner behavior before writing.
5. Apply the operation.
6. See read-back/validation evidence.
7. Locate backups later if needed.
8. Explain absent files/results without raw shell.

## Unacceptable Result

The result is unacceptable if:

- write previews still require the agent to manually reconstruct diffs from bytes/ranges;
- `blank_line` or other joiner behavior remains visually surprising;
- backups are still hidden from normal recovery workflows;
- hidden discovery leaks secrets by default;
- continuation requires chat/session state that disappears after reconnect;
- new sort/summary/hidden features leak absolute paths under `cwd_id`;
- errors remain too generic for recovery;
- cleanup can delete broad user files.

## Open Questions

None for concept. Defaults chosen here:

- Phase 5 is additive and preserves existing defaults.
- Hidden traversal is opt-in.
- Secret-safe behavior is required for risky broad content surfaces.
- Cleanup is allowed only if the later SRS can make it narrow, dry-run-first and workspace-bound; otherwise it is deferred.
