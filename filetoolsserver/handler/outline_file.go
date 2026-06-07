package handler

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	outlineLanguageAuto       = "auto"
	outlineLanguageMarkdown   = "markdown"
	outlineLanguageGo         = "go"
	outlineLanguageJavaScript = "javascript"
	outlineLanguageTypeScript = "typescript"
	outlineLanguageTSX        = "tsx"
	outlineLanguagePython     = "python"
	outlineLanguageJava       = "java"
	outlineLanguageJSON       = "json"
	outlineLanguageYAML       = "yaml"
	outlineLanguageSvelte     = "svelte"
	outlineLanguageUnknown    = "unknown"

	outlineProfileAgent           = "agent"
	outlineProfileFull            = "full"
	outlineProfileOutline         = "outline"
	outlineProfileFingerprintOnly = "fingerprint_only"

	defaultOutlineMaxItems = 500
	defaultOutlineMaxDepth = 8
)

func (h *Handler) HandleOutlineFile(ctx context.Context, req *mcp.CallToolRequest, input OutlineFileInput) (*mcp.CallToolResult, OutlineFileOutput, error) {
	pathCtx, cwdErr := h.BuildPathContext(input.CwdID)
	if cwdErr != nil {
		return toolCwdError[OutlineFileOutput](cwdErr)
	}
	resolvedFile, displayFile, err := h.resolveToolPath(pathCtx, input.TargetFile, "target_file")
	if err != nil {
		return toolError[OutlineFileOutput](fmt.Sprintf("Cannot outline target_file: %v", err))
	}
	releaseTool, err := h.acquireToolCall(ctx)
	if err != nil {
		return toolError[OutlineFileOutput](limiterWaitError("tool call", err))
	}
	defer releaseTool()

	info, err := h.inspectTextFileForRefactor(ctx, resolvedFile)
	if err != nil {
		return toolError[OutlineFileOutput](fmt.Sprintf("Cannot outline %q: %v", displayFile, pathCtx.sanitizeErrorText(err.Error())))
	}
	info.displayPath = displayFile

	language := outlineLanguage(input.Language, info.resolvedPath)
	base := outlineBaseOutput(info, "ok", language)
	if language == outlineLanguageUnknown && explicitOutlineLanguageRequested(input.Language) {
		base.ParserStatus = "unsupported_language"
		base.Error = fmt.Sprintf("unsupported_language: %q is not a supported outline language", input.Language)
		base.ErrorCode = "unsupported_language"
		return errorResult(base.Error), base, nil
	}
	outputProfile, err := outlineOutputProfile(input.OutputProfile)
	if err != nil {
		base.ErrorCode = "invalid_output_profile"
		base.NextRecommendedCall = outlineOutputProfileRetryHint(pathCtx, displayFile, input)
		return outlineToolError(base, err.Error())
	}
	if outputProfile == outlineProfileFingerprintOnly {
		base.ParserStatus = outlineProfileFingerprintOnly
		return structuredResultOnly(), base, nil
	}

	maxItems, err := effectiveOptionalLimit(input.MaxItems, defaultOutlineMaxItems)
	if err != nil {
		return outlineToolError(base, err.Error())
	}
	maxDepth, err := effectiveOptionalNonNegative(input.MaxDepth, defaultOutlineMaxDepth, "max_depth")
	if err != nil {
		return outlineToolError(base, err.Error())
	}
	if input.LineWindow != nil {
		if input.LineWindow.StartLine < 1 || input.LineWindow.EndLine < input.LineWindow.StartLine {
			return outlineToolError(base, "line_window must use 1-based start_line/end_line and end_line >= start_line")
		}
	}
	if input.EnclosingLine != nil && *input.EnclosingLine < 1 {
		return outlineToolError(base, "enclosing_line must be a 1-based positive line number")
	}
	options := outlineOptions{
		includeImports:  input.IncludeImports,
		includeSymbols:  input.IncludeSymbols,
		includeSections: input.IncludeSections,
		lineWindow:      input.LineWindow,
		enclosingLine:   input.EnclosingLine,
		nameContains:    input.NameContains,
		kinds:           input.Kinds,
		outputProfile:   outputProfile,
		maxItems:        maxItems,
		maxDepth:        maxDepth,
	}
	options.applyIncludeDefaults()

	var output OutlineFileOutput
	switch language {
	case outlineLanguageMarkdown:
		output, err = h.outlineMarkdown(ctx, info, options)
	case outlineLanguageGo:
		if info.stat.Size() > h.config.WriteThreshold {
			base.ParserStatus = "outline_parse_threshold_exceeded"
			base.Warnings = append(base.Warnings, ToolWarning{
				Code:    "outline_parse_threshold_exceeded",
				Message: "Go outline requires whole-file parsing in this phase and the file is above the configured parser threshold.",
				File:    info.displayPath,
			})
			nextInput := map[string]any{
				"target_file":    info.displayPath,
				"output_profile": "fingerprint_only",
			}
			addCwdIDToRecommendedInput(pathCtx, "outline_file", nextInput)
			base.NextRecommendedCall = &ActionHint{
				SafeToRetry:                false,
				RecommendedNextTool:        "outline_file",
				RecommendedNextInputPolicy: "use_fingerprint_only_or_smaller_file",
				RecommendedNextInput:       nextInput,
				Reason:                     "Stage 1 Go outline is whole-file AST parsing; line_window cannot reduce parse cost.",
			}
			return outlineToolError(base, "outline_parse_threshold_exceeded")
		}
		output, err = h.outlineGo(ctx, info, options)
	case outlineLanguageJavaScript, outlineLanguageTypeScript, outlineLanguageTSX, outlineLanguagePython, outlineLanguageJava, outlineLanguageJSON, outlineLanguageYAML, outlineLanguageSvelte:
		if info.stat.Size() > h.config.WriteThreshold {
			base.ParserStatus = "outline_parse_threshold_exceeded"
			base.Warnings = append(base.Warnings, ToolWarning{
				Code:    "outline_parse_threshold_exceeded",
				Message: "Tree-sitter outline requires whole-file parsing and the file is above the configured parser threshold.",
				File:    info.displayPath,
			})
			nextInput := map[string]any{
				"target_file":    info.displayPath,
				"output_profile": "fingerprint_only",
			}
			addCwdIDToRecommendedInput(pathCtx, "outline_file", nextInput)
			base.NextRecommendedCall = &ActionHint{
				SafeToRetry:                false,
				RecommendedNextTool:        "outline_file",
				RecommendedNextInputPolicy: "use_fingerprint_only_or_smaller_file",
				RecommendedNextInput:       nextInput,
				Reason:                     "Tree-sitter outline is whole-file parsing; line_window cannot reduce parse cost.",
			}
			return outlineToolError(base, "outline_parse_threshold_exceeded")
		}
		output, err = h.outlineTreeSitter(ctx, info, language, options)
	default:
		output, err = h.outlineGenericText(ctx, info, options)
	}
	if err != nil {
		if output.Fingerprint == nil {
			output = base
		}
		return outlineToolError(output, err.Error())
	}
	applyOutlineTruncationHint(pathCtx, &output, input)
	applyOutlineCompactProfileHint(pathCtx, &output, input)
	return structuredResultOnly(), output, nil
}

type outlineOptions struct {
	includeImports  bool
	includeSymbols  bool
	includeSections bool
	lineWindow      *SourceLineRange
	enclosingLine   *int
	nameContains    string
	kinds           []string
	outputProfile   string
	maxItems        int
	maxDepth        int
}

func (o *outlineOptions) applyIncludeDefaults() {
	if !o.includeImports && !o.includeSymbols && !o.includeSections {
		o.includeImports = true
		o.includeSymbols = true
		o.includeSections = true
	}
}

func outlineBaseOutput(info fileTextInfo, parserStatus, language string) OutlineFileOutput {
	return OutlineFileOutput{
		File:         info.displayPath,
		Language:     language,
		ParserStatus: parserStatus,
		Fingerprint:  &info.fingerprint,
		Imports:      []OutlineItem{},
		Symbols:      []OutlineItem{},
		Sections:     []OutlineItem{},
		Warnings:     []ToolWarning{},
	}
}

func outlineToolError(output OutlineFileOutput, message string) (*mcp.CallToolResult, OutlineFileOutput, error) {
	output.Error = message
	if output.ErrorCode == "" {
		output.ErrorCode = errorCodeFromMessage(message)
	}
	return errorResult(message), output, nil
}

func outlineLanguage(requested, path string) string {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested == "" {
		requested = outlineLanguageAuto
	}
	if requested != outlineLanguageAuto {
		switch requested {
		case outlineLanguageMarkdown, "md":
			return outlineLanguageMarkdown
		case outlineLanguageGo:
			return outlineLanguageGo
		case outlineLanguageJavaScript, "js", "jsx", "mjs", "cjs":
			return outlineLanguageJavaScript
		case outlineLanguageText, "plain_text", "plaintext", "generic_text":
			return outlineLanguageText
		case outlineLanguageTypeScript, "ts":
			return outlineLanguageTypeScript
		case outlineLanguageTSX, "react":
			return outlineLanguageTSX
		case outlineLanguagePython, "py":
			return outlineLanguagePython
		case outlineLanguageJava:
			return outlineLanguageJava
		case outlineLanguageJSON:
			return outlineLanguageJSON
		case outlineLanguageYAML, "yml":
			return outlineLanguageYAML
		case outlineLanguageSvelte:
			return outlineLanguageSvelte
		default:
			return outlineLanguageUnknown
		}
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return outlineLanguageMarkdown
	case ".go":
		return outlineLanguageGo
	case ".js", ".jsx", ".mjs", ".cjs":
		return outlineLanguageJavaScript
	case ".ts":
		return outlineLanguageTypeScript
	case ".tsx":
		return outlineLanguageTSX
	case ".py":
		return outlineLanguagePython
	case ".java":
		return outlineLanguageJava
	case ".json":
		return outlineLanguageJSON
	case ".yaml", ".yml":
		return outlineLanguageYAML
	case ".svelte":
		return outlineLanguageSvelte
	default:
		return outlineLanguageUnknown
	}
}

func explicitOutlineLanguageRequested(requested string) bool {
	requested = strings.ToLower(strings.TrimSpace(requested))
	return requested != "" && requested != outlineLanguageAuto
}

func outlineOutputProfile(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", outlineProfileAgent, outlineProfileOutline:
		return outlineProfileAgent, nil
	case outlineProfileFull:
		return outlineProfileFull, nil
	case outlineProfileFingerprintOnly:
		return outlineProfileFingerprintOnly, nil
	default:
		return "", fmt.Errorf("invalid_output_profile: got %q; use %q, %q, %q, or %q", value, outlineProfileAgent, outlineProfileFull, outlineProfileFingerprintOnly, outlineProfileOutline)
	}
}

func outlineOutputProfileRetryHint(pathCtx PathContext, targetFile string, input OutlineFileInput) *ActionHint {
	nextInput := map[string]any{
		"target_file":    targetFile,
		"output_profile": outlineProfileAgent,
	}
	if strings.TrimSpace(input.Language) != "" {
		nextInput["language"] = input.Language
	}
	addCwdIDToRecommendedInput(pathCtx, "outline_file", nextInput)
	return &ActionHint{
		SafeToRetry:                true,
		RecommendedNextTool:        "outline_file",
		RecommendedNextInputPolicy: "retry_valid_output_profile",
		RecommendedNextInput:       nextInput,
		Reason:                     "Retry with a valid output_profile enum value.",
	}
}

func applyOutlineTruncationHint(pathCtx PathContext, output *OutlineFileOutput, input OutlineFileInput) {
	if output == nil || !output.Truncated || output.Fingerprint == nil || output.OutlineStats.TruncationReason == "" {
		return
	}
	nextStart := output.OutlineStats.NextOmittedLine
	if nextStart < 1 || nextStart > output.Fingerprint.LineCount {
		return
	}
	nextInput := map[string]any{
		"target_file": output.File,
		"language":    output.Language,
		"line_window": map[string]any{
			"start_line": nextStart,
			"end_line":   output.Fingerprint.LineCount,
		},
	}
	if profile, err := outlineOutputProfile(input.OutputProfile); err == nil && profile != "" {
		nextInput["output_profile"] = profile
	}
	if input.IncludeImports {
		nextInput["include_imports"] = true
	}
	if input.IncludeSymbols {
		nextInput["include_symbols"] = true
	}
	if input.IncludeSections {
		nextInput["include_sections"] = true
	}
	if input.MaxItems != nil {
		nextInput["max_items"] = *input.MaxItems + defaultOutlineMaxDepth
	}
	if input.MaxDepth != nil {
		nextInput["max_depth"] = *input.MaxDepth
	}
	if strings.TrimSpace(input.NameContains) != "" {
		nextInput["name_contains"] = input.NameContains
	}
	if len(input.Kinds) > 0 {
		nextInput["kinds"] = input.Kinds
	}
	addCwdIDToRecommendedInput(pathCtx, "outline_file", nextInput)
	output.NextRecommendedCall = &ActionHint{
		SafeToRetry:                true,
		RecommendedNextTool:        "outline_file",
		RecommendedNextInputPolicy: "continue_from_last_included_line_window",
		RecommendedNextInput:       nextInput,
		Reason:                     "The outline was truncated by max_items; continue with the bounded line_window instead of guessing the next range.",
	}
}

func applyOutlineCompactProfileHint(pathCtx PathContext, output *OutlineFileOutput, input OutlineFileInput) {
	if output == nil || output.NextRecommendedCall != nil || output.OutlineStats.OmittedLeafItems == 0 {
		return
	}
	nextInput := map[string]any{
		"target_file":    output.File,
		"output_profile": outlineProfileFull,
	}
	if strings.TrimSpace(input.Language) != "" {
		nextInput["language"] = input.Language
	}
	if input.LineWindow != nil {
		nextInput["line_window"] = map[string]any{
			"start_line": input.LineWindow.StartLine,
			"end_line":   input.LineWindow.EndLine,
		}
	}
	if input.EnclosingLine != nil {
		nextInput["enclosing_line"] = *input.EnclosingLine
	}
	if strings.TrimSpace(input.NameContains) != "" {
		nextInput["name_contains"] = input.NameContains
	}
	if len(input.Kinds) > 0 {
		nextInput["kinds"] = input.Kinds
	}
	addCwdIDToRecommendedInput(pathCtx, "outline_file", nextInput)
	output.NextRecommendedCall = &ActionHint{
		SafeToRetry:                true,
		RecommendedNextTool:        "outline_file",
		RecommendedNextInputPolicy: "expand_config_leaf_items",
		RecommendedNextInput:       nextInput,
		Reason:                     "The agent profile omitted JSON/YAML leaf value items; retry full profile only if leaf-level detail is needed.",
	}
}
