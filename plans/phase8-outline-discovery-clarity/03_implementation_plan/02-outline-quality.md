# Stage 2: Outline Quality

Goal:
Make `outline_file` for JS, TS, TSX, Python, Java, JSON and YAML compact, accurate, selector-safe, and close to the practical usefulness of Go/Markdown outlines.

Depends on:
- Stage 1 style decisions for descriptions, but implementation can proceed independently after plan approval.

Touched areas:
- `filetoolsserver/handler/outline_file.go`
- `filetoolsserver/handler/outline_treesitter.go`
- `filetoolsserver/handler/resolve_symbol_range.go`
- `filetoolsserver/handler/outline_schema.go`
- `filetoolsserver/handler/refactor_types.go`
- outline tests, likely existing `agent_tools_test.go` plus new focused test file if clearer
- fixtures under the handler test package if existing patterns support them

Public contract:
- `outline_file` emits only real, resolvable selectors.
- Every selector from `agent` and `full` profile round-trips through `resolve_symbol_range`.
- `agent` profile is compact/action-oriented; `full` preserves all parser-visible navigation detail.
- Repeated non-actionable `write_safe=false` noise is summarized in default profile rather than repeated on every item.
- Java is parser-backed, not generic text.

Language routing steps:
1. Add `outlineLanguageJava` constant.
2. Route explicit `language:"java"` and `.java` extension in `outlineLanguage`.
3. Add Java to the tree-sitter language switch using vendored `grammars.JavaLanguage()`.
4. Include Java in the parser-backed branch in `HandleOutlineFile`.
5. Include Java in resolver language normalization and any parser-backed language checks.
6. Update docs/descriptions only after tests prove Java grammar builds.

JS/TS/TSX extraction steps:
1. Split import/export classification:
   - `import_statement` -> `import`;
   - source-bearing re-export, such as `export { X } from "pkg"` or `export * from "pkg"` -> `re_export`;
   - exported declarations -> declaration kind, not import kind.
2. Change `isTreeSitterImportKind` and `isTreeSitterTopLevelImportBlockMember` so `import_block` groups real imports only.
3. Ensure `export default function/class`, `export function`, `export class`, `export const`, interfaces, type aliases and methods produce predictable symbols.
4. Keep re-exports visible without hiding declarations; exported declarations keep their declaration kind and add export status through detail/metadata.
5. Improve arrow/function variable naming only where grammar gives stable names; do not create fake symbols from arbitrary expressions.
6. TSX component classification requires component-like evidence, such as PascalCase plus JSX-like body/initializer.

Python extraction steps:
1. Keep functions, classes, methods, imports and import_from.
2. Ensure decorated definitions use the decorated symbol's range without creating decorator-only symbols.
3. Mark nested symbols but keep default output from being overwhelmed.
4. Preserve `full` profile access to nested details.

Java extraction steps:
1. Add `javaSymbolSpec`.
2. Minimum useful baseline:
   - package declaration;
   - imports;
   - class declarations;
   - interface declarations;
   - enum declarations;
   - record declarations;
   - annotation declarations;
   - methods;
   - constructors;
   - fields.
3. Treat package/imports as import-like/navigation metadata.
4. Keep write safety conservative for Java initially; read/navigation ranges must still be exact and useful.
5. Add Java fixture with nested class/interface/enum/record/annotation and representative methods/constructors/fields.
6. If node names differ from expectation, update tests and extraction empirically from actual grammar output; do not silently drop the Java baseline.

JSON/YAML profile steps:
1. Preserve Phase 7 exact config path identity.
2. Default `agent` profile keeps containers and key/property paths, not noisy value leaves.
3. `outline_stats.omitted_leaf_items` remains the summary for omitted values.
4. Add or refine a summary for omitted non-actionable write-safety reasons if repeated `write_safe=false` is noisy.
5. `full` profile returns leaves and detailed diagnostics.
6. Ensure sequence indexes and literal keys remain exact.

Selector round-trip steps:
1. For every language fixture, collect selectors emitted by `outline_file` in `agent`.
2. Repeat for `full` where it returns additional leaf/nested items.
3. Resolve by `symbol_ref`.
4. Resolve representative selectors by `kind/name/path`.
5. Resolve representative `enclosing_line`.
6. Assert resolved ranges and byte ranges match the outline item.
7. Assert stale fingerprint behavior is unchanged.

Agent-facing outline probes:
1. JS fixture: imports, re-export, exported function/class/const, normal function/class.
2. TS fixture: interface, type alias, exported declarations, methods.
3. TSX fixture: React-like component, non-component PascalCase value, export default.
4. Python fixture: imports, decorated function/class, nested method.
5. Java fixture: package/import/class/interface/enum/record/annotation/method/constructor/field.
6. JSON fixture: nested objects/arrays with noisy scalar leaves.
7. YAML fixture: mapping, sequence, literal keys, multi-document if existing support expects it.

Checks:
- Focused outline tests for all seven languages.
- Resolver round-trip tests for all selectors.
- Existing Go/Markdown outline tests still pass.
- `go test -count=1 ./filetoolsserver/handler`.

Handoff / next stage:
After Java and profile behavior are stable, Stage 1 docs/descriptions must be aligned with the final supported language list and profile wording.

Stop and ask if:
- Java grammar requires non-standard build behavior or a new dependency that cannot be justified as ordinary Windows-compatible.
