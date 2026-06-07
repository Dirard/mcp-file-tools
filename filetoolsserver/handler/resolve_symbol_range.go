package handler

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	resolveStatusResolved  = "resolved"
	resolveStatusAmbiguous = "ambiguous"
	resolveStatusNotFound  = "not_found"
	resolveStatusEstimated = "estimated_only"
	resolveStatusStale     = "stale"
	resolveStatusFailed    = "failed"

	writeRecommendationNotRequested  = "not_requested"
	writeRecommendationReady         = "ready"
	writeRecommendationRefused       = "refused"
	writeRecommendationNotApplicable = "not_applicable"

	targetSyntaxNotChecked = "not_checked"
	targetSyntaxSafe       = "safe"
	targetSyntaxUnknown    = "unknown"

	targetSyntaxProofNone       = "none"
	targetSyntaxProofCreateNew  = "create_new"
	targetSyntaxProofMarkdownOK = "markdown_parser_ok"
	targetSyntaxProofPlainText  = "plain_text_asserted"
)

func (h *Handler) HandleResolveSymbolRange(ctx context.Context, req *mcp.CallToolRequest, input ResolveSymbolRangeInput) (*mcp.CallToolResult, ResolveSymbolRangeOutput, error) {
	releaseTool, err := h.acquireToolCall(ctx)
	if err != nil {
		return toolError[ResolveSymbolRangeOutput](limiterWaitError("tool call", err))
	}
	defer releaseTool()

	pathCtx, cwdErr := h.BuildPathContext(input.CwdID)
	if cwdErr != nil {
		return toolCwdError[ResolveSymbolRangeOutput](cwdErr)
	}
	if strings.TrimSpace(input.SourceFile) == "" {
		return resolveSymbolToolError("source_file is required", "missing_source_file")
	}
	resolvedFile, displayFile, err := h.resolveToolPath(pathCtx, input.SourceFile, "source_file")
	if err != nil {
		return resolveSymbolToolError(fmt.Sprintf("Cannot resolve source_file: %v", err), "invalid_source_file")
	}
	info, err := h.inspectTextFileForRefactor(ctx, resolvedFile)
	if err != nil {
		return resolveSymbolToolError(fmt.Sprintf("Cannot inspect source_file: %v", pathCtx.sanitizeErrorText(err.Error())), "source_inspect_failed")
	}
	info.displayPath = displayFile
	base := ResolveSymbolRangeOutput{
		File:             displayFile,
		Fingerprint:      &info.fingerprint,
		Matches:          []ResolvedSymbolMatch{},
		ResolvedRanges:   []ResolvedRange{},
		ResolutionStatus: resolveStatusFailed,
	}
	if !fingerprintsEqual(info.fingerprint, input.SourceFingerprint) {
		base.Error = "symbol_fingerprint_mismatch: source file changed; refresh outline_file and retry with the new fingerprint"
		base.ErrorCode = "symbol_fingerprint_mismatch"
		base.ResolutionStatus = resolveStatusStale
		base.ActionHint = outlineRefreshHint(pathCtx, displayFile)
		return errorResult(base.Error), base, nil
	}
	if input.Selector.Range != nil {
		if err := validateSelectorLineRange(*input.Selector.Range, info.fingerprint.LineCount, "selector.range"); err != nil {
			base.Error = "invalid_selector_range: " + err.Error()
			base.ErrorCode = "invalid_selector_range"
			return errorResult(base.Error), base, nil
		}
		if input.Selector.RangeFingerprint == nil {
			base.Error = "selector_range_fingerprint_required: selector.range requires selector.range_fingerprint"
			base.ErrorCode = "selector_range_fingerprint_required"
			return errorResult(base.Error), base, nil
		}
		if !fingerprintsEqual(info.fingerprint, *input.Selector.RangeFingerprint) {
			base.Error = "symbol_fingerprint_mismatch: selector.range_fingerprint is stale; refresh outline_file and retry"
			base.ErrorCode = "symbol_fingerprint_mismatch"
			base.ResolutionStatus = resolveStatusStale
			base.ActionHint = outlineRefreshHint(pathCtx, displayFile)
			return errorResult(base.Error), base, nil
		}
	}
	if input.Selector.EnclosingLine != nil {
		if err := validateSelectorLine(*input.Selector.EnclosingLine, info.fingerprint.LineCount, "selector.enclosing_line"); err != nil {
			base.Error = "invalid_enclosing_line: " + err.Error()
			base.ErrorCode = "invalid_enclosing_line"
			return errorResult(base.Error), base, nil
		}
	}
	language, errCode, errMessage := resolveSelectorLanguage(input.Language, input.Selector.Language, info.resolvedPath)
	if errCode != "" {
		base.Error = errMessage
		base.ErrorCode = errCode
		return errorResult(errMessage), base, nil
	}
	base.Language = language
	if selectorHasOnlyExactRange(input.Selector) {
		match := syntheticRangeSelectorMatch(input.Selector)
		base.ParserStatus = "range_selector"
		base.Matches = []ResolvedSymbolMatch{match}
		base.ResolvedRanges = []ResolvedRange{resolvedRangeFromMatch(match, info.fingerprint)}
		base.ResolutionStatus = resolveStatusResolved
		base.NextRecommendedCall = readResolvedRangeHint(pathCtx, displayFile, *input.Selector.Range)
		if input.TargetIntent != nil {
			if handled := h.populateWriteRecommendation(ctx, pathCtx, input, &base); handled {
				return errorResult(base.Error), base, nil
			}
		} else {
			base.WriteRecommendationStatus = writeRecommendationNotRequested
			base.TargetSyntaxStatus = targetSyntaxNotChecked
			base.TargetSyntaxProof = targetSyntaxProofNone
		}
		return structuredResultOnly(), base, nil
	}
	var lineWindow *SourceLineRange
	if input.Selector.Range != nil {
		lineWindow = input.Selector.Range
	}
	if input.Selector.EnclosingLine != nil {
		lineWindow = &SourceLineRange{StartLine: *input.Selector.EnclosingLine, EndLine: *input.Selector.EnclosingLine}
	}
	outline, err := h.outlineForSymbolResolution(ctx, info, language, lineWindow, input.Selector.EnclosingLine)
	if err != nil {
		base.Error = err.Error()
		base.ErrorCode = "outline_failed"
		return errorResult(base.Error), base, nil
	}
	base.ParserStatus = outline.ParserStatus
	if outline.Error != "" {
		base.Error = outline.Error
		base.ErrorCode = firstNonEmpty(outline.ErrorCode, errorCodeFromMessage(outline.Error))
		return errorResult(outline.Error), base, nil
	}
	candidates := dedupeOutlineItems(flattenOutlineItems(append(append([]OutlineItem{}, outline.Imports...), append(outline.Symbols, outline.Sections...)...)))
	unestimatedQuery := input.Selector
	unestimatedQuery.AllowEstimated = true
	allMatches := filterSymbolCandidates(candidates, unestimatedQuery)
	matches := allMatches
	if !input.Selector.AllowEstimated {
		matches = make([]OutlineItem, 0, len(allMatches))
		for _, item := range allMatches {
			if !item.RangeIsEstimated {
				matches = append(matches, item)
			}
		}
	}
	if input.Selector.EnclosingLine != nil {
		sort.SliceStable(matches, func(i, j int) bool {
			return rangeLineSpan(matches[i].Range) < rangeLineSpan(matches[j].Range)
		})
	}
	for _, item := range matches {
		match := resolvedSymbolMatchFromItem(item)
		base.Matches = append(base.Matches, match)
		base.ResolvedRanges = append(base.ResolvedRanges, resolvedRangeFromMatch(match, info.fingerprint))
	}
	switch len(base.Matches) {
	case 0:
		if input.Selector.Range != nil && selectorHasOnlyExactRange(input.Selector) {
			match := syntheticRangeSelectorMatch(input.Selector)
			base.ParserStatus = "range_selector"
			base.Matches = []ResolvedSymbolMatch{match}
			base.ResolvedRanges = []ResolvedRange{resolvedRangeFromMatch(match, info.fingerprint)}
			base.ResolutionStatus = resolveStatusResolved
			base.NextRecommendedCall = readResolvedRangeHint(pathCtx, displayFile, *input.Selector.Range)
		} else if len(allMatches) > 0 {
			base.ResolutionStatus = resolveStatusEstimated
		} else {
			base.ResolutionStatus = resolveStatusNotFound
		}
		if base.NextRecommendedCall == nil {
			base.NextRecommendedCall = outlineRefreshHint(pathCtx, displayFile)
		}
	case 1:
		base.ResolutionStatus = resolveStatusResolved
		base.NextRecommendedCall = readResolvedRangeHint(pathCtx, displayFile, base.ResolvedRanges[0].Range)
	default:
		base.ResolutionStatus = resolveStatusAmbiguous
		base.Ambiguous = true
		base.NextRecommendedCalls = readHintListForMatches(pathCtx, displayFile, base.ResolvedRanges)
		if len(base.NextRecommendedCalls) > 0 {
			base.NextRecommendedCall = &base.NextRecommendedCalls[0]
		}
	}
	if input.TargetIntent != nil {
		if handled := h.populateWriteRecommendation(ctx, pathCtx, input, &base); handled {
			return errorResult(base.Error), base, nil
		}
	} else {
		base.WriteRecommendationStatus = writeRecommendationNotRequested
		base.TargetSyntaxStatus = targetSyntaxNotChecked
		base.TargetSyntaxProof = targetSyntaxProofNone
	}
	return structuredResultOnly(), base, nil
}

func (h *Handler) outlineForSymbolResolution(ctx context.Context, info fileTextInfo, language string, lineWindow *SourceLineRange, enclosingLine *int) (OutlineFileOutput, error) {
	options := outlineOptions{
		includeImports:  true,
		includeSymbols:  true,
		includeSections: true,
		lineWindow:      lineWindow,
		enclosingLine:   enclosingLine,
		maxItems:        0,
		maxDepth:        0,
	}
	if isJSLikeLanguage(language) || language == outlineLanguageJSON || language == outlineLanguageYAML {
		options.outputProfile = outlineProfileFull
	}
	switch language {
	case outlineLanguageMarkdown:
		return h.outlineMarkdown(ctx, info, options)
	case outlineLanguageGo:
		if info.stat.Size() > h.config.WriteThreshold {
			return outlineThresholdExceededOutput(info, language), nil
		}
		return h.outlineGo(ctx, info, options)
	case outlineLanguageJavaScript, outlineLanguageTypeScript, outlineLanguageTSX, outlineLanguagePython, outlineLanguageJava, outlineLanguageJSON, outlineLanguageYAML, outlineLanguageSvelte:
		if info.stat.Size() > h.config.WriteThreshold {
			return outlineThresholdExceededOutput(info, language), nil
		}
		return h.outlineTreeSitter(ctx, info, language, options)
	default:
		return h.outlineGenericText(ctx, info, options)
	}
}

func outlineThresholdExceededOutput(info fileTextInfo, language string) OutlineFileOutput {
	output := outlineBaseOutput(info, "outline_parse_threshold_exceeded", language)
	output.Error = "outline_parse_threshold_exceeded"
	output.Warnings = append(output.Warnings, ToolWarning{
		Code:    "outline_parse_threshold_exceeded",
		Message: "Parser-backed outline requires whole-file parsing and the file is above the configured parser threshold.",
		File:    info.displayPath,
	})
	return output
}

func syntheticRangeSelectorMatch(query SymbolSelectorQuery) ResolvedSymbolMatch {
	return ResolvedSymbolMatch{
		Kind:             firstNonEmpty(query.Kind, "range"),
		Name:             firstNonEmpty(query.Name, "selected_range"),
		SymbolPath:       append([]string(nil), query.SymbolPath...),
		Range:            *query.Range,
		Confidence:       "exact",
		RangeIsEstimated: false,
		WholeLineRange:   true,
		WriteSafe:        true,
	}
}

func nestedPythonSymbol(match ResolvedSymbolMatch) bool {
	return match.Selector != nil &&
		match.Selector.Language == outlineLanguagePython &&
		match.Metadata != nil &&
		match.Metadata["nested"] == "true"
}

func validateSelectorLineRange(r SourceLineRange, lineCount int, fieldName string) error {
	if lineCount < 1 {
		return fmt.Errorf("%s cannot select lines from an empty source file", fieldName)
	}
	if r.StartLine < 1 {
		return fmt.Errorf("%s.start_line must be >= 1", fieldName)
	}
	if r.EndLine < r.StartLine {
		return fmt.Errorf("%s.end_line must be >= start_line", fieldName)
	}
	if r.EndLine > lineCount {
		return fmt.Errorf("%s.end_line must be <= source line_count (%d)", fieldName, lineCount)
	}
	return nil
}

func validateSelectorLine(line, lineCount int, fieldName string) error {
	if lineCount < 1 {
		return fmt.Errorf("%s cannot select lines from an empty source file", fieldName)
	}
	if line < 1 {
		return fmt.Errorf("%s must be >= 1", fieldName)
	}
	if line > lineCount {
		return fmt.Errorf("%s must be <= source line_count (%d)", fieldName, lineCount)
	}
	return nil
}

func dedupeOutlineItems(items []OutlineItem) []OutlineItem {
	seen := map[string]bool{}
	out := make([]OutlineItem, 0, len(items))
	for _, item := range items {
		key := item.SymbolRef
		if key == "" {
			key = fmt.Sprintf("%s:%s:%d:%d", item.Kind, item.Name, item.Range.StartLine, item.Range.EndLine)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out
}

func selectorHasOnlyExactRange(query SymbolSelectorQuery) bool {
	return query.Range != nil &&
		query.SymbolRef == "" &&
		query.Kind == "" &&
		query.Name == "" &&
		len(query.SymbolPath) == 0 &&
		query.Disambiguator == "" &&
		query.EnclosingLine == nil
}

func resolveSymbolToolError(message, code string) (*mcp.CallToolResult, ResolveSymbolRangeOutput, error) {
	return errorResult(message), ResolveSymbolRangeOutput{
		Error:            message,
		ErrorCode:        code,
		Matches:          []ResolvedSymbolMatch{},
		ResolvedRanges:   []ResolvedRange{},
		ResolutionStatus: resolveStatusFailed,
	}, nil
}

func (h *Handler) populateWriteRecommendation(ctx context.Context, pathCtx PathContext, input ResolveSymbolRangeInput, output *ResolveSymbolRangeOutput) bool {
	intent := input.TargetIntent
	output.WriteRecommendationStatus = writeRecommendationRefused
	output.TargetSyntaxStatus = targetSyntaxNotChecked
	output.TargetSyntaxProof = targetSyntaxProofNone
	if intent == nil {
		output.WriteRecommendationStatus = writeRecommendationNotRequested
		return false
	}
	operation := strings.ToLower(strings.TrimSpace(intent.Operation))
	if operation != operationCopy && operation != operationMove {
		return resolveWriteIntentInputError(output, "invalid_target_operation", "target_intent.operation must be copy or move")
	}
	if strings.TrimSpace(intent.TargetFile) == "" {
		return resolveWriteIntentInputError(output, "invalid_target_file", "target_intent.target_file is required")
	}
	if _, err := normalizeTargetSyntaxMode(intent.TargetSyntaxMode); err != nil {
		return resolveWriteIntentInputError(output, "invalid_target_syntax_mode", err.Error())
	}
	if targetPreconditionProvided(intent.TargetPrecondition) {
		if err := validateTargetPrecondition(intent.TargetPrecondition); err != nil {
			return resolveWriteIntentInputError(output, errorCodeFromMessage(err.Error()), err.Error())
		}
	}
	if err := validatePlacementShape(intent.Placement); err != nil {
		return resolveWriteIntentInputError(output, errorCodeFromMessage(err.Error()), err.Error())
	}
	backup := BackupSpec{}
	if intent.Backup != nil {
		backup = *intent.Backup
	}
	if err := validateBackupSpec(backup, "target_intent.backup.mode"); err != nil {
		return resolveWriteIntentInputError(output, errorCodeFromMessage(err.Error()), err.Error())
	}
	if _, err := normalizeJoinerName(intent.Joiner); err != nil {
		return resolveWriteIntentInputError(output, errorCodeFromMessage(err.Error()), err.Error())
	}
	if _, err := normalizeRedactionMode(intent.RedactionMode); err != nil {
		return resolveWriteIntentInputError(output, errorCodeFromMessage(err.Error()), err.Error())
	}
	if output.ResolutionStatus != resolveStatusResolved || len(output.ResolvedRanges) != 1 {
		output.WriteRecommendationStatus = writeRecommendationNotApplicable
		output.WriteRefusalCode = "symbol_resolution_not_single"
		output.WriteRefusalReason = "A write recommendation requires exactly one resolved symbol range."
		return false
	}
	resolved := output.ResolvedRanges[0]
	if output.ParserStatus != "ok" && output.ParserStatus != "range_selector" {
		output.WriteRefusalCode = "symbol_parser_not_write_safe"
		output.WriteRefusalReason = "Parser status must be ok, or selector.range must be exact with a current range_fingerprint, before resolver can recommend a write preview."
		return false
	}
	if resolved.RangeIsEstimated {
		output.WriteRefusalCode = "symbol_range_estimated"
		output.WriteRefusalReason = "Estimated symbol ranges are read-only."
		return false
	}
	if !resolved.WholeLineRange || !resolved.WriteSafe {
		output.WriteRefusalCode = firstNonEmpty(resolved.RefusalReason, "symbol_range_not_write_safe")
		output.WriteRefusalReason = writeUnsafeResolvedRangeReason(output.Language, resolved)
		return false
	}
	if operation == operationMove && len(output.Matches) == 1 && nestedPythonSymbol(output.Matches[0]) {
		output.WriteRefusalCode = "symbol_source_deletion_not_proven"
		output.WriteRefusalReason = "Nested Python symbols are not source-deletion safe for move recommendations until enclosing-suite validity can be proven."
		return false
	}
	sourceResolved, _, err := h.resolveRefactorPath(pathCtx, input.SourceFile, "source_file")
	if err != nil {
		return resolveWriteIntentInputError(output, "invalid_source_file", fmt.Sprintf("Cannot resolve source_file for write recommendation: %v", err))
	}
	targetResolved, _, err := h.resolveRefactorPath(pathCtx, intent.TargetFile, "target_intent.target_file")
	if err != nil {
		return resolveWriteIntentInputError(output, "invalid_target_file", fmt.Sprintf("Cannot resolve target_intent.target_file: %v", err))
	}
	if err := rejectSymlinkPath(sourceResolved, true); err != nil {
		return resolveWriteIntentInputError(output, "source_symlink_unsupported", err.Error())
	}
	if err := rejectSymlinkPath(targetResolved, true); err != nil {
		return resolveWriteIntentInputError(output, "target_symlink_unsupported", err.Error())
	}
	if !targetPreconditionProvided(intent.TargetPrecondition) {
		precondition, refused := h.prepareTargetPreconditionForRecommendation(ctx, pathCtx, *intent, targetResolved, output)
		if refused {
			return false
		}
		intent.TargetPrecondition = precondition
	}
	same, err := sameFileOrPath(sourceResolved, targetResolved)
	if err != nil {
		return resolveWriteIntentInputError(output, "target_identity_check_failed", err.Error())
	}
	if same {
		output.WriteRefusalCode = "target_same_file_unsupported"
		output.WriteRefusalReason = "Phase 7 does not recommend same-file symbol copy/move; use explicit range tools manually after inspection."
		output.TargetSyntaxStatus = targetSyntaxNotChecked
		return false
	}
	syntax := h.evaluateTargetSyntaxForRecommendation(ctx, pathCtx, *intent, targetResolved)
	output.TargetSyntaxStatus = syntax.status
	output.TargetSyntaxProof = syntax.proof
	output.TargetSyntaxProofReason = syntax.reason
	if syntax.refusalCode != "" {
		output.WriteRefusalCode = syntax.refusalCode
		output.WriteRefusalReason = syntax.refusalReason
		if syntax.refusalCode == "target_fingerprint_mismatch" || syntax.refusalCode == "target_syntax_not_proven" {
			output.ActionHint = inspectTargetPathHint(pathCtx, intent.TargetFile, "refresh_target_precondition")
		}
		return false
	}
	writeHint := writeRecommendationHint(pathCtx, input, *intent, operation, resolved.Range, backup)
	output.WriteRecommendationStatus = writeRecommendationReady
	output.RecommendedWriteCall = writeHint
	output.WriteRefusalCode = ""
	output.WriteRefusalReason = ""
	return false
}

func targetPreconditionProvided(precondition TargetPrecondition) bool {
	return precondition.MustNotExist || precondition.Fingerprint != nil
}

func (h *Handler) prepareTargetPreconditionForRecommendation(ctx context.Context, pathCtx PathContext, intent WriteTargetIntent, targetResolved string, output *ResolveSymbolRangeOutput) (TargetPrecondition, bool) {
	info, err := os.Stat(targetResolved)
	if err == nil {
		if info.IsDir() {
			output.WriteRefusalCode = "target_path_is_directory"
			output.WriteRefusalReason = "target_intent.target_file resolves to a directory; choose an explicit target file."
			output.ActionHint = inspectTargetPathHint(pathCtx, intent.TargetFile, "inspect_directory_target")
			return TargetPrecondition{}, true
		}
		if intent.Placement.Mode == placementCreateNew {
			output.WriteRefusalCode = "target_exists_for_create_new"
			output.WriteRefusalReason = "placement create_new requires a missing target; choose append/prepend/insert/replace or a different target_file."
			output.ActionHint = inspectTargetPathHint(pathCtx, intent.TargetFile, "inspect_existing_create_new_target")
			return TargetPrecondition{}, true
		}
		targetInfo, inspectErr := h.inspectTextFileForRefactor(ctx, targetResolved)
		if inspectErr != nil {
			output.WriteRefusalCode = "target_precondition_unavailable"
			output.WriteRefusalReason = fmt.Sprintf("Cannot prepare target_precondition.fingerprint: %v", pathCtx.sanitizeErrorText(inspectErr.Error()))
			output.ActionHint = inspectTargetPathHint(pathCtx, intent.TargetFile, "inspect_target_for_precondition")
			return TargetPrecondition{}, true
		}
		return TargetPrecondition{Fingerprint: &targetInfo.fingerprint}, false
	}
	if os.IsNotExist(err) {
		if intent.Placement.Mode == placementCreateNew {
			return TargetPrecondition{MustNotExist: true}, false
		}
		output.WriteRefusalCode = "target_missing_for_placement"
		output.WriteRefusalReason = fmt.Sprintf("placement %q requires an existing target; use create_new or create the target first.", intent.Placement.Mode)
		output.ActionHint = inspectTargetPathHint(pathCtx, intent.TargetFile, "inspect_missing_target")
		return TargetPrecondition{}, true
	}
	output.WriteRefusalCode = "target_precondition_unavailable"
	output.WriteRefusalReason = fmt.Sprintf("Cannot inspect target_intent.target_file: %v", pathCtx.sanitizeErrorText(err.Error()))
	output.ActionHint = inspectTargetPathHint(pathCtx, intent.TargetFile, "inspect_target_for_precondition")
	return TargetPrecondition{}, true
}

type targetSyntaxRecommendation struct {
	status        string
	proof         string
	reason        string
	refusalCode   string
	refusalReason string
}

func (h *Handler) evaluateTargetSyntaxForRecommendation(ctx context.Context, pathCtx PathContext, intent WriteTargetIntent, targetResolved string) targetSyntaxRecommendation {
	mode, err := normalizeTargetSyntaxMode(intent.TargetSyntaxMode)
	if err != nil {
		return targetSyntaxRecommendation{
			status:        targetSyntaxUnknown,
			proof:         targetSyntaxProofNone,
			reason:        err.Error(),
			refusalCode:   "invalid_target_syntax_mode",
			refusalReason: err.Error(),
		}
	}
	if intent.Placement.Mode == placementCreateNew {
		if !intent.TargetPrecondition.MustNotExist {
			return targetSyntaxRefusal("target_precondition.must_not_exist is required for create_new target syntax proof")
		}
		if _, err := os.Stat(targetResolved); err == nil {
			return targetSyntaxRefusal("create_new target already exists")
		} else if err != nil && !os.IsNotExist(err) {
			return targetSyntaxRefusal(err.Error())
		}
		return targetSyntaxRecommendation{
			status: targetSyntaxSafe,
			proof:  targetSyntaxProofCreateNew,
			reason: "Target does not exist and target_precondition.must_not_exist makes the dry-run range tool preview safe.",
		}
	}
	if _, err := os.Stat(targetResolved); err != nil {
		if os.IsNotExist(err) {
			return targetSyntaxRefusal(fmt.Sprintf("placement %q requires an existing target", intent.Placement.Mode))
		}
		return targetSyntaxRefusal(err.Error())
	}
	if intent.TargetPrecondition.Fingerprint == nil {
		return targetSyntaxRefusal("target_precondition.fingerprint is required for existing target syntax proof")
	}
	targetInfo, err := h.inspectTextFileForRefactor(ctx, targetResolved)
	if err != nil {
		return targetSyntaxRefusal(err.Error())
	}
	if !fingerprintsEqual(targetInfo.fingerprint, *intent.TargetPrecondition.Fingerprint) {
		return targetSyntaxRecommendation{
			status:        targetSyntaxUnknown,
			proof:         targetSyntaxProofNone,
			reason:        "target_precondition.fingerprint is stale",
			refusalCode:   "target_fingerprint_mismatch",
			refusalReason: "target_precondition.fingerprint is stale; refresh the explicit target path before preparing the write preview.",
		}
	}
	detectedLanguage := outlineLanguage("", targetResolved)
	if isStructuredTargetLanguage(detectedLanguage) && detectedLanguage != outlineLanguageMarkdown {
		return targetSyntaxRefusal(fmt.Sprintf("structured target language %q is not allowlisted for Phase 6 symbol write recommendations", detectedLanguage))
	}
	if mode == "plain_text" {
		return targetSyntaxRecommendation{
			status: targetSyntaxSafe,
			proof:  targetSyntaxProofPlainText,
			reason: "Caller explicitly asserted a non-structured plain text target and target fingerprint is current.",
		}
	}
	if mode == "markdown" || detectedLanguage == outlineLanguageMarkdown {
		targetInfo.displayPath = h.projectOutputPath(pathCtx, targetResolved)
		outline, err := h.outlineMarkdown(ctx, targetInfo, outlineOptions{
			includeSections: true,
			maxItems:        0,
			maxDepth:        0,
		})
		if err != nil {
			return targetSyntaxRefusal(err.Error())
		}
		if outline.ParserStatus != "ok" || outline.Error != "" {
			return targetSyntaxRefusal("Markdown target parser did not return ok")
		}
		return targetSyntaxRecommendation{
			status: targetSyntaxSafe,
			proof:  targetSyntaxProofMarkdownOK,
			reason: "Markdown target parsed cleanly and line-based placement does not need structured delimiter repair.",
		}
	}
	return targetSyntaxRefusal("target syntax is not proven; use target_syntax_mode=plain_text for non-structured text or a Markdown target")
}

func targetSyntaxRefusal(reason string) targetSyntaxRecommendation {
	return targetSyntaxRecommendation{
		status:        targetSyntaxUnknown,
		proof:         targetSyntaxProofNone,
		reason:        reason,
		refusalCode:   "target_syntax_not_proven",
		refusalReason: reason,
	}
}

func writeUnsafeResolvedRangeReason(language string, resolved ResolvedRange) string {
	if language == outlineLanguageJSON || language == outlineLanguageYAML {
		return "Resolved JSON/YAML range is exact for reading but not line-write-safe because delimiter, indentation, or sibling boundaries cannot be repaired by copy/move ranges."
	}
	if resolved.RefusalReason != "" {
		return "Resolved symbol range is exact for reading but not safe for line-based copy/move: " + resolved.RefusalReason + "."
	}
	return "Resolved symbol range is exact for reading but not safe for line-based copy/move."
}

func normalizeTargetSyntaxMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "auto":
		return "auto", nil
	case "markdown":
		return "markdown", nil
	case "plain_text":
		return "plain_text", nil
	default:
		return "", fmt.Errorf("invalid_target_syntax_mode: use auto, markdown, or plain_text")
	}
}

func isStructuredTargetLanguage(language string) bool {
	switch language {
	case outlineLanguageGo, outlineLanguageJavaScript, outlineLanguageTypeScript, outlineLanguageTSX, outlineLanguagePython, outlineLanguageJava, outlineLanguageJSON, outlineLanguageYAML, outlineLanguageSvelte:
		return true
	default:
		return false
	}
}

func writeRecommendationHint(pathCtx PathContext, input ResolveSymbolRangeInput, intent WriteTargetIntent, operation string, r SourceLineRange, backup BackupSpec) *ActionHint {
	toolName := "copy_ranges"
	if operation == operationMove {
		toolName = "move_ranges"
	}
	nextInput := map[string]any{
		"source_file":         input.SourceFile,
		"source_fingerprint":  input.SourceFingerprint,
		"ranges":              []SourceLineRange{r},
		"target_file":         intent.TargetFile,
		"target_precondition": intent.TargetPrecondition,
		"placement":           intent.Placement,
		"dry_run":             true,
	}
	if strings.TrimSpace(intent.Joiner) != "" {
		nextInput["joiner"] = intent.Joiner
	}
	if intent.Backup != nil {
		nextInput["backup"] = backup
	}
	if strings.TrimSpace(intent.RedactionMode) != "" {
		nextInput["redaction_mode"] = intent.RedactionMode
	}
	addCwdIDToRecommendedInput(pathCtx, toolName, nextInput)
	return &ActionHint{
		SafeToRetry:                true,
		RecommendedNextTool:        toolName,
		RecommendedNextInputPolicy: "dry_run_symbol_range_preview",
		RecommendedNextInput:       nextInput,
		Reason:                     "Preview this symbol-derived range transfer with diff/read-back validation before any apply call.",
	}
}

func resolveWriteIntentInputError(output *ResolveSymbolRangeOutput, code, message string) bool {
	if code == "" {
		code = "invalid_target_intent"
	}
	output.Error = fmt.Sprintf("%s: %s", code, message)
	output.ErrorCode = code
	output.WriteRecommendationStatus = writeRecommendationRefused
	output.WriteRefusalCode = code
	output.WriteRefusalReason = message
	output.TargetSyntaxStatus = targetSyntaxUnknown
	output.TargetSyntaxProof = targetSyntaxProofNone
	return true
}

func pathCtxToCwdIDInput(pathCtx PathContext) CwdIDInput {
	if !pathCtx.HasCwd {
		return CwdIDInput{}
	}
	return CwdIDInput{Present: true, Value: pathCtx.CwdID, PathContext: &pathCtx}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func resolveSelectorLanguage(topLevel, selectorLanguage, path string) (string, string, string) {
	top := outlineLanguage(topLevel, path)
	sel := ""
	if strings.TrimSpace(selectorLanguage) != "" {
		sel = outlineLanguage(selectorLanguage, path)
	}
	if explicitOutlineLanguageRequested(topLevel) && top == outlineLanguageUnknown {
		return "", "unsupported_language", fmt.Sprintf("unsupported_language: %q is not a supported outline language", topLevel)
	}
	if explicitOutlineLanguageRequested(selectorLanguage) && sel == outlineLanguageUnknown {
		return "", "unsupported_language", fmt.Sprintf("unsupported_language: %q is not a supported outline language", selectorLanguage)
	}
	if topLevel != "" && selectorLanguage != "" && top != sel {
		return "", "selector_language_conflict", "selector_language_conflict: language and selector.language resolve to different languages"
	}
	if sel != "" {
		return sel, "", ""
	}
	return top, "", ""
}

func filterSymbolCandidates(items []OutlineItem, query SymbolSelectorQuery) []OutlineItem {
	out := []OutlineItem{}
	for _, item := range items {
		if query.EnclosingLine != nil && (*query.EnclosingLine < item.Range.StartLine || *query.EnclosingLine > item.Range.EndLine) {
			continue
		}
		if query.SymbolRef != "" && item.SymbolRef != query.SymbolRef {
			continue
		}
		if query.Kind != "" && !strings.EqualFold(query.Kind, item.Kind) {
			continue
		}
		if query.Name != "" && item.Name != query.Name {
			continue
		}
		if len(query.SymbolPath) > 0 && !stringSlicesEqual(query.SymbolPath, item.Path) {
			continue
		}
		if query.Disambiguator != "" && (item.Selector == nil || item.Selector.Disambiguator != query.Disambiguator) {
			continue
		}
		if query.Range != nil && (item.Range.StartLine != query.Range.StartLine || item.Range.EndLine != query.Range.EndLine) {
			continue
		}
		if !query.AllowEstimated && item.RangeIsEstimated {
			continue
		}
		out = append(out, item)
	}
	return out
}

func resolvedSymbolMatchFromItem(item OutlineItem) ResolvedSymbolMatch {
	disambiguator := ""
	if item.Selector != nil {
		disambiguator = item.Selector.Disambiguator
	}
	return ResolvedSymbolMatch{
		SymbolRef:        item.SymbolRef,
		Kind:             item.Kind,
		Name:             item.Name,
		SymbolPath:       item.Path,
		Range:            item.Range,
		ByteRange:        item.ByteRange,
		Confidence:       item.Confidence,
		RangeIsEstimated: item.RangeIsEstimated,
		WholeLineRange:   boolValue(item.WholeLineRange),
		WriteSafe:        boolValue(item.WriteSafe),
		Disambiguator:    disambiguator,
		RefusalReason:    item.RefusalReason,
		Selector:         item.Selector,
		Metadata:         item.Metadata,
	}
}

func resolvedRangeFromMatch(match ResolvedSymbolMatch, fingerprint FileFingerprint) ResolvedRange {
	return ResolvedRange{
		Range:            match.Range,
		ByteRange:        match.ByteRange,
		Confidence:       match.Confidence,
		RangeIsEstimated: match.RangeIsEstimated,
		WholeLineRange:   match.WholeLineRange,
		WriteSafe:        match.WriteSafe,
		RangeFingerprint: fingerprint,
		Selector:         match.Selector,
		RefusalReason:    match.RefusalReason,
	}
}

func readResolvedRangeHint(pathCtx PathContext, file string, r SourceLineRange) *ActionHint {
	input := map[string]any{"target_file": file, "start_line": r.StartLine, "end_line": r.EndLine}
	addCwdIDToRecommendedInput(pathCtx, "read_file", input)
	return &ActionHint{
		SafeToRetry:                true,
		RecommendedNextTool:        "read_file",
		RecommendedNextInputPolicy: "read_resolved_symbol_range",
		RecommendedNextInput:       input,
		Reason:                     "Read the resolved symbol range before deciding on a write operation.",
	}
}

func readHintListForMatches(pathCtx PathContext, file string, ranges []ResolvedRange) []ActionHint {
	hints := make([]ActionHint, 0, len(ranges))
	for _, resolved := range ranges {
		if hint := readResolvedRangeHint(pathCtx, file, resolved.Range); hint != nil {
			hints = append(hints, *hint)
		}
	}
	return hints
}

func outlineRefreshHint(pathCtx PathContext, file string) *ActionHint {
	input := map[string]any{"target_file": file}
	addCwdIDToRecommendedInput(pathCtx, "outline_file", input)
	return &ActionHint{
		SafeToRetry:                true,
		RecommendedNextTool:        "outline_file",
		RecommendedNextInputPolicy: "refresh_outline_for_symbol_resolution",
		RecommendedNextInput:       input,
		Reason:                     "Refresh outline_file to get current selectors and source_fingerprint.",
	}
}

func inspectTargetPathHint(pathCtx PathContext, targetFile, policy string) *ActionHint {
	input := map[string]any{"target_path": targetFile}
	addCwdIDToRecommendedInput(pathCtx, "inspect_path", input)
	return &ActionHint{
		SafeToRetry:                true,
		RecommendedNextTool:        "inspect_path",
		RecommendedNextInputPolicy: policy,
		RecommendedNextInput:       input,
		Reason:                     "Inspect the explicit target path before asking resolve_symbol_range to prepare a write preview.",
	}
}

func rangeLineSpan(r SourceLineRange) int {
	return r.EndLine - r.StartLine
}

func boolValue(value *bool) bool {
	return value != nil && *value
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func fingerprintsEqual(left, right FileFingerprint) bool {
	return left.SHA256 == right.SHA256 &&
		left.SizeBytes == right.SizeBytes &&
		left.LineCount == right.LineCount &&
		left.ModifiedUnixNano == right.ModifiedUnixNano
}
