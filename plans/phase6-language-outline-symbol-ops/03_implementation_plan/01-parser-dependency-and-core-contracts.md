# Stage 1: Parser Dependency And Core Contracts

## Goal

Prove the parser dependency strategy, define the parser adapter layer, and extend outline schemas before language-specific parsers are built.

## Depends On

- Clean Phase 6 concept.
- Phase 5 plan for diff/validation safety.

## Touched Areas

- `go.mod`
- `go.sum`
- `filetoolsserver/handler/outline_file.go`
- `filetoolsserver/handler/outline_common.go`
- `filetoolsserver/handler/refactor_types.go`
- `filetoolsserver/handler/cwd_path.go`
- `filetoolsserver/handler/schema_constraints.go`
- `filetoolsserver/server.go`
- tests

## Dependency Proof

Phase 6 must first prove parser dependencies.

Candidate primary parser:

- `github.com/odvcencio/gotreesitter`
- `github.com/odvcencio/gotreesitter/grammars`

Reason:

- pure-Go tree-sitter runtime;
- embedded grammar registry;
- target languages include `javascript`, `typescript`, `tsx`, `python`, `json`, `yaml`, and `svelte`;
- avoids CGo tree-sitter build/runtime issues.

Optional YAML helper:

- `gopkg.in/yaml.v3`
- only if it improves YAML node position or parser-error handling without replacing the primary proof.

Proof steps:

0. Stop before dependency proof and get root/user approval for the exact proof scope:
   - exact module/version candidates, or explicitly bounded "latest candidate proof";
   - network access for module download;
   - expected `go.mod`, `go.sum`, `vendor/`, and `vendor/modules.txt` mutation;
   - proof commands to run.
1. After approval, add dependency in a small isolated work slice; do not create a git branch just for this proof.
2. Add a tiny parser smoke test for required languages.
3. Add extraction proof tests that do more than parse:
   - run a minimal query or node traversal for JavaScript, TypeScript, TSX/JSX, Python, JSON, YAML and Svelte;
   - assert start/end byte offsets, parser points and converted line ranges for at least one symbol/node per language;
   - assert Svelte nested script offsets map back to source-file lines before nested symbols are accepted.
4. Run:
   - this command is allowed only after the stop-before-proof approval above;
   - `go mod download`;
   - PowerShell: `$env:CGO_ENABLED='0'; go test -count=1 ./filetoolsserver/handler -run "OutlineParserDependency"`;
   - PowerShell: `$env:CGO_ENABLED='0'; go build -trimpath -buildvcs=false -o .\mcp-file-tools.exe .\cmd\mcp-file-tools`.
5. Phase 6 commits vendored parser dependencies because this repository already keeps `vendor/` checked in:
   - run the project's vendoring command, normally `go mod vendor`;
   - ensure `vendor/modules.txt` reflects parser modules;
   - do not rely on a developer-local module cache for later offline checks;
   - verify with concrete PowerShell commands for targeted parser tests and build:
     - `$env:GOFLAGS='-mod=vendor'; $env:GOPROXY='off'; $env:CGO_ENABLED='0'; go test -count=1 ./filetoolsserver/handler -run "OutlineParserDependency|Outline|Symbol|Schema|Cwd"`;
     - `$env:GOFLAGS='-mod=vendor'; $env:GOPROXY='off'; $env:CGO_ENABLED='0'; go build -trimpath -buildvcs=false -o .\mcp-file-tools.exe .\cmd\mcp-file-tools`.
6. Record dependency version, license, module maturity, vendored/offline status, build time, binary size before/after, and grammar bundle size/cost in review notes.
7. Default dependency budget:
   - no CGo or platform-native runtime dependency;
   - license must be compatible with current project distribution;
   - Windows build still succeeds with `CGO_ENABLED=0`;
   - binary size delta should stay under 25 MB unless root explicitly accepts more;
   - targeted parser dependency tests should finish in under 10 seconds on the local dev machine unless root explicitly accepts more.
8. Stop for root decision after proof if the chosen parser dependency is pre-v1, even if it passes license/build checks. Also stop if APIs look unstable, license is unclear, binary delta/build time exceeds budget, vendored/offline verification cannot be made reliable, or parser extraction is weaker than required.
9. If dependency fails build, license, stability, extraction, offset mapping, vendored/offline verification or size sanity, do not implement regex exact parsers; return to planning.

## Parser Adapter

Add an internal parser registry:

```go
type outlineParser interface {
    language() string
    aliases() []string
    detect(path string) bool
    parse(ctx context.Context, info fileTextInfo, options outlineParseOptions) (OutlineParseResult, error)
}
```

Rules:

- Go and Markdown parsers can be adapted into this interface without changing output semantics.
- Generic parser remains fallback.
- Registry resolves requested language before extension auto-detect.
- Unsupported requested language returns `error_code=unsupported_language`.
- Parser adapters own language extraction only.
- Central `outline_file` handler owns file IO, fingerprinting, parser selection, filters, output limits/truncation, cwd projection, warnings normalization and `next_recommended_call`.
- Parser adapter returns normalized items plus status/scope; it must not assemble `OutlineFileOutput` directly.

Core internal result:

```go
type OutlineParseResult struct {
    Language string
    ParserStatus string
    ParserScope string
    Imports []OutlineItem
    Symbols []OutlineItem
    Sections []OutlineItem
    Warnings []ToolWarning
}
```

## Language Detection

Support:

- `go`
- `markdown`, `md`
- `typescript`, `ts`
- `tsx`
- `javascript`, `js`
- `jsx`
- `svelte`
- `python`, `py`
- `json`
- `yaml`, `yml`
- `auto`

Auto detection:

- `.go` -> Go;
- `.md`, `.markdown` -> Markdown;
- `.ts` -> TypeScript;
- `.tsx` -> TSX;
- `.js`, `.mjs`, `.cjs` -> JavaScript;
- `.jsx` -> JSX;
- `.svelte` -> Svelte;
- `.py` -> Python;
- `.json`, `.jsonc` only if parser supports comments honestly;
- `.yaml`, `.yml` -> YAML.

## Outline Item Additions

Extend `OutlineItem`:

```go
Selector *SymbolSelector `json:"selector,omitempty"`
EnclosingPath []string `json:"enclosing_path,omitempty"`
```

Existing `OutlineItem.path`, new `enclosing_path`, and selector `symbol_path` are language-local breadcrumb paths, not filesystem paths. They may contain JSON/YAML keys, class/function nesting, object property names or other parser-local segments. Path/cwd projection must not slash-normalize, relativize, reject, or otherwise treat these fields as OS paths.

Phase 6 filesystem path fields are explicitly limited to:

- tool inputs: `target_file`, `source_file`, and Stage 4 `target_intent.target_file`;
- tool outputs: top-level `file`, warnings/diagnostics fields that refer to files, and Stage 4 recommended range-tool inputs such as `source_file` / `target_file`.

Any field named `path` that is a slice/breadcrumb on an outline/symbol item is language-local unless this SRS lists it above as a filesystem path.

Add:

```go
type SymbolSelector struct {
    SymbolRef string `json:"symbol_ref"`
    Language string `json:"language"`
    Kind string `json:"kind"`
    Name string `json:"name"`
    SymbolPath []string `json:"symbol_path"`
    Range SourceLineRange `json:"range"`
    ByteRange *SourceByteRange `json:"byte_range,omitempty"`
    WholeLineRange bool `json:"whole_line_range"`
    WriteSafe bool `json:"write_safe"`
    RangeFingerprint FileFingerprint `json:"range_fingerprint"`
    Disambiguator string `json:"disambiguator,omitempty"`
}

type SourceByteRange struct {
    StartByte int64 `json:"start_byte"`
    EndByteExclusive int64 `json:"end_byte_exclusive"`
}
```

Rules:

- `selector` is present only for parser-backed exact ranges.
- `symbol_ref` is deterministic for the same file fingerprint, parser scope/version, language, kind, symbol_path, name and exact byte/line range.
- `symbol_ref` is file/fingerprint-scoped and not a permanent global ID across edits.
- `selector.symbol_path` is language-local, not a filesystem path. The field is intentionally not named `path` so schema/path projection code does not treat it as an OS path.
- `selector.range_fingerprint` equals the file fingerprint used for range computation.
- `disambiguator`, if emitted, must be deterministic for the current file fingerprint, parser scope/version, language, kind, symbol_path, name and exact range. If a parser cannot make it deterministic, omit it.
- `disambiguator` can be human-readable, but resolver correctness must not depend on a non-deterministic value and tests must prove unchanged-file reparse stability.
- `byte_range` is for proof and diagnostics; current write tools still consume line ranges.
- `whole_line_range=true` means the parser node can be represented as a line range without swallowing sibling tokens.
- `write_safe=true` requires exact parser range, matching fingerprint, `whole_line_range=true`, and no need to modify adjacent delimiter/syntax tokens outside the selected line range.
- Exact read/navigation selectors may have `write_safe=false` when the exact node is a same-line fragment.
- No absolute filesystem path is embedded in selector under `cwd_id`.

## Parser Statuses

Add machine-readable statuses:

- `ok`
- `partial`
- `parse_error`
- `unsupported_language`
- `outline_parse_threshold_exceeded`
- `generic_fallback`
- `estimated_only`

Error codes:

- `unsupported_language`
- `parser_dependency_unavailable`
- `parse_error`
- `outline_parse_threshold_exceeded`
- `ambiguous_symbol`
- `symbol_not_found`
- `symbol_range_estimated`
- `symbol_range_not_write_safe`
- `symbol_parser_not_write_safe`
- `invalid_target_operation`
- `invalid_target_syntax_mode`
- `target_same_file_unsupported`
- `target_syntax_not_proven`
- `selector_range_fingerprint_required`
- `selector_language_conflict`
- `symbol_fingerprint_mismatch`
- `target_intent_requires_dry_run`

## Boundedness

Rules:

- Parser input must respect `MCP_WRITE_THRESHOLD` or a dedicated `MCP_OUTLINE_PARSE_THRESHOLD`.
- Add `MCP_OUTLINE_PARSE_THRESHOLD`, default same as `MCP_WRITE_THRESHOLD`.
- `max_items`, `max_depth`, `line_window`, `name_contains`, and `kinds` apply after parser extraction.
- Parser extraction may build an internal tree, but output remains bounded.

## Checks

- Dependency smoke fixtures parse required languages.
- Vendored/offline proof uses these concrete PowerShell commands when `vendor/` is present:
  - `$env:GOFLAGS='-mod=vendor'; $env:GOPROXY='off'; $env:CGO_ENABLED='0'; go test -count=1 ./filetoolsserver/handler -run "OutlineParserDependency|Outline|Symbol|Schema|Cwd"`;
  - `$env:GOFLAGS='-mod=vendor'; $env:GOPROXY='off'; $env:CGO_ENABLED='0'; go build -trimpath -buildvcs=false -o .\mcp-file-tools.exe .\cmd\mcp-file-tools`.
- Dependency smoke fixtures prove query/range extraction and byte-to-line conversion for required languages.
- Svelte dependency proof covers nested source offset mapping or marks nested symbols unsupported.
- Unsupported requested language structured error.
- Auto-detect tests for each extension.
- Existing Go/Markdown tests unchanged.
- Generic fallback for unknown text file.
- Schema tests for selector fields.
- Test that a `symbol_ref` emitted by `outline_file` resolves after a reparse of the unchanged file.
- Test that emitted `disambiguator` values are deterministic across unchanged-file reparse, or absent for parsers that cannot prove stability.
- Cwd no-leak tests for selector/recommended inputs.

## Handoff / Next Stage

After Stage 1, language parsers can implement extraction without changing public field names.

## Stop And Ask If

- Dependency proof fails.
- Dependency adds CGo or unacceptable binary/runtime cost.
- Selector schema cannot be made cwd-safe.
