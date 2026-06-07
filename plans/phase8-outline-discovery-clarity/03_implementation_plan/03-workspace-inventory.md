# Stage 3: Workspace Inventory Completeness

Goal:
Make `workspace_inventory` completeness semantics self-explanatory so agents cannot confuse a complete returned page with incomplete summary/tree coverage.

Depends on:
- Accepted concept canonical fields.

Touched areas:
- `filetoolsserver/handler/tool_types.go`
- `filetoolsserver/handler/workspace_inventory.go`
- `filetoolsserver/handler/workspace_inventory_schema.go`
- workspace inventory tests
- `filetoolsserver/server.go`
- `TOOLS.md`
- `README.md`

Public contract:
- `page_complete` answers whether the current `directories_page` response page is complete for this query.
- `page_incomplete_reason` explains why the page needs continuation.
- `summary_coverage_complete` answers whether summary counters/hints cover the requested scan scope.
- `summary_incomplete_reason` explains depth, continuation, page limit, scan limit, or context interruption limitations.
- `tree_scan_complete` answers whether the traversed directory tree coverage is complete under the requested depth and limits.
- `scan_scope` is one of: `requested_depth`, `max_depth_limited`, `page_limited`, `scan_limited`, `continuation_page`, `context_limited`.
- `continuation.page_complete` mirrors page completeness in the continuation object.
- Legacy `summary.complete` and `continuation.complete` stay for compatibility, but docs/descriptions prefer canonical fields.

Steps:
1. Extend `WorkspaceInventoryOutput` with additive canonical fields:
   - `PageComplete bool`
   - `PageIncompleteReason string`
   - `SummaryCoverageComplete bool`
   - `SummaryIncompleteReason string`
   - `TreeScanComplete bool`
   - `ScanScope string`
2. Extend the shared `ContinuationHint` type additively with optional JSON field `page_complete,omitempty`.
   - Set `page_complete` only for `workspace_inventory` outputs.
   - Leave it omitted for `read_file`, `read_files`, `glob_file_search` and other continuation users.
   - Update schemas/tests to prove other tools do not start emitting workspace-only page semantics.
3. Update `WorkspaceSummary` with additive canonical coverage fields if summary-local placement is clearer:
   - keep root-level canonical fields as the first place agents should read;
   - keep `summary.complete` as alias.
4. In `workspace_inventory.go`, compute page completeness separately from summary/tree coverage:
   - page incomplete when `builder.truncated` because page limit or scan cap stops page production;
   - page complete when no continuation is needed for the current query page.
5. Compute summary coverage:
   - false when continuation_after is present and summary is page-local;
   - false when max_depth was reached and summary does not cover deeper tree;
   - false when scan/page truncation limits summary;
   - true only when summary covers the requested scope without truncation.
6. Compute tree scan completeness:
   - false when max_depth reached before full tree;
   - false when scan cap/context/truncation stops traversal;
   - true when traversal covered requested depth and no truncation happened.
7. Generate reasons with short stable codes or strings:
   - `page_limit_reached`
   - `scan_limit_reached`
   - `max_depth_reached`
   - `continuation_page`
   - `context_cancelled`
   - `complete`
8. Set `scan_scope` deterministically:
   - `requested_depth` when the requested depth was fully scanned without page/scan/context truncation;
   - `max_depth_limited` when `max_depth_reached` limits tree/summary coverage;
   - `page_limited` when page limit stops the returned page;
   - `scan_limited` when scan cap stops traversal;
   - `continuation_page` when `continuation_after` scopes the response to a later page;
   - `context_limited` when context cancellation/interruption limits traversal.
9. Update `workspaceInventoryContinuation` so `continuation.complete` remains its legacy behavior and `continuation.page_complete` is explicit for workspace inventory only.
10. Update `workspaceInventoryNextRecommendedCalls` reasons:
   - page incomplete -> continue workspace inventory page;
   - page complete but summary/tree incomplete -> rerun with deeper/narrower scope or use glob/grep as appropriate;
   - directory-level file discovery -> bounded `glob_file_search`.
11. Update `workspace_inventory_schema.go` with all new fields because schema uses strict additional properties.
12. Update any shared continuation schema expectations so `page_complete` is optional and documented as workspace-inventory-only.
13. Update docs and tool descriptions to steer agents to canonical fields first.

Test cases:
1. Page complete and summary complete on a small tree.
2. Page complete but summary/tree incomplete because `max_depth` was reached.
3. Page incomplete because `limit` was reached; continuation has `page_complete=false`.
4. Continuation page where summary is page-local; summary coverage reports incomplete or scoped.
5. Scan cap/context truncation if existing test hooks make it practical.
6. Backward compatibility: `summary.complete` and `continuation.complete` still exist and retain documented compatibility meaning.
7. Shared continuation compatibility: `page_complete` appears on `workspace_inventory.continuation` and is omitted from read/glob continuations.
8. `scan_scope` only emits the six allowed values.

Checks:
- Focused workspace inventory tests.
- Schema tests if present.
- Docs/description consistency check.

Handoff / next stage:
Stage 1 metadata must mention canonical fields after this stage is implemented.

Stop and ask if:
- Implementation reveals that keeping legacy `complete` semantics would actively mislead agents even with canonical fields. That would be a public compatibility decision.
