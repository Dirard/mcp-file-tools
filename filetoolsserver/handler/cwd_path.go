package handler

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

type PathContext struct {
	HasCwd bool
	CwdID  int64
	CwdAbs string
	CwdOut string
}

func (p PathContext) outputMeta() CwdOutputMeta {
	if !p.HasCwd {
		return CwdOutputMeta{}
	}
	return CwdOutputMeta{
		CwdID: int64Ptr(p.CwdID),
		Cwd:   slashPath(p.CwdOut),
	}
}

func (h *Handler) BuildPathContext(cwdID CwdIDInput) (PathContext, *CwdError) {
	if !cwdID.Present {
		return PathContext{}, nil
	}
	if cwdID.Value < 1 || cwdID.Value > maxCwdID {
		return PathContext{}, invalidCwdIDError(nil)
	}
	if cwdID.PathContext != nil && cwdID.PathContext.HasCwd && cwdID.PathContext.CwdID == cwdID.Value {
		return *cwdID.PathContext, nil
	}
	if h.cwdRegistry == nil {
		return PathContext{}, &CwdError{
			Code:    "cwd_state_unavailable",
			Message: "cwd registry is unavailable",
			CwdID:   int64Ptr(cwdID.Value),
			Hint:    staleCwdHint("The cwd registry is unavailable; call set_cwd after server state is healthy."),
		}
	}
	entry, err := h.cwdRegistry.lookup(cwdID.Value)
	if err != nil {
		return PathContext{}, err
	}
	return PathContext{
		HasCwd: true,
		CwdID:  entry.ID,
		CwdAbs: entry.Abs,
		CwdOut: entry.Out,
	}, nil
}

func (h *Handler) resolveToolPath(pathCtx PathContext, inputPath, fieldName string) (string, string, error) {
	if strings.TrimSpace(inputPath) == "" {
		if pathCtx.HasCwd {
			return "", "", fmt.Errorf("%s is required and must be a relative path under cwd_id", fieldName)
		}
		return "", "", fmt.Errorf("%s is required and must be an absolute path", fieldName)
	}
	if !pathCtx.HasCwd {
		if !isAbsoluteToolPath(inputPath) {
			return "", "", fmt.Errorf("%s must be an absolute path for this server OS; relative paths require cwd_id", fieldName)
		}
		v := h.ResolvePath(inputPath)
		if !v.Ok() {
			return "", "", v.Err
		}
		return v.Path, h.displayResolvedPath(inputPath, v.Path), nil
	}
	resolved, err := resolveRelativePathUnderCwd(pathCtx, inputPath, fieldName)
	if err != nil {
		return "", "", err
	}
	if err := ensureExistingComponentWithinCwd(pathCtx.CwdAbs, resolved); err != nil {
		return "", "", err
	}
	return resolved, projectPathUnderCwd(pathCtx, resolved), nil
}

func (h *Handler) resolveInspectPath(pathCtx PathContext, inputPath, fieldName string) (string, string, error) {
	if !pathCtx.HasCwd {
		return h.resolveToolPath(pathCtx, inputPath, fieldName)
	}
	resolved, err := resolveRelativePathUnderCwd(pathCtx, inputPath, fieldName)
	if err != nil {
		return "", "", err
	}
	if err := ensureExistingComponentWithinCwdMode(pathCtx.CwdAbs, resolved, true); err != nil {
		return "", "", err
	}
	return resolved, projectPathUnderCwd(pathCtx, resolved), nil
}

func resolveRelativePathUnderCwd(pathCtx PathContext, inputPath, fieldName string) (string, error) {
	raw := strings.TrimSpace(inputPath)
	if raw == "" {
		return "", fmt.Errorf("%s is required and must be a relative path under cwd_id", fieldName)
	}
	if looksAbsoluteForAnySupportedOS(raw) {
		return "", fmt.Errorf("%s must be relative when cwd_id is provided", fieldName)
	}
	slash := strings.ReplaceAll(raw, "\\", "/")
	cleanSlash := path.Clean(slash)
	if cleanSlash == "/" || strings.HasPrefix(cleanSlash, "../") || cleanSlash == ".." {
		return "", fmt.Errorf("%s escapes cwd_id directory", fieldName)
	}
	if cleanSlash == "." {
		return filepath.Clean(pathCtx.CwdAbs), nil
	}
	resolved := filepath.Clean(filepath.Join(pathCtx.CwdAbs, filepath.FromSlash(cleanSlash)))
	if !pathInsideOrEqual(pathCtx.CwdAbs, resolved) {
		return "", fmt.Errorf("%s escapes cwd_id directory", fieldName)
	}
	return resolved, nil
}

func looksAbsoluteForAnySupportedOS(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	slash := strings.ReplaceAll(value, "\\", "/")
	if strings.HasPrefix(slash, "/") {
		return true
	}
	if len(slash) >= 2 && isASCIIAlpha(slash[0]) && slash[1] == ':' {
		return true
	}
	if strings.HasPrefix(value, `\\`) || strings.HasPrefix(value, `//`) {
		return true
	}
	return false
}

func isASCIIAlpha(b byte) bool {
	return b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z'
}

func ensureExistingComponentWithinCwd(cwdAbs, resolved string) error {
	return ensureExistingComponentWithinCwdMode(cwdAbs, resolved, false)
}

func ensureExistingComponentWithinCwdMode(cwdAbs, resolved string, allowFinalSymlinkOutside bool) error {
	check := filepath.Clean(resolved)
	final := check
	for {
		info, err := os.Lstat(check)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				if allowFinalSymlinkOutside && check == final {
					return nil
				}
				evaluated, evalErr := filepath.EvalSymlinks(check)
				if evalErr != nil {
					return evalErr
				}
				if !pathInsideOrEqual(cwdAbs, evaluated) {
					return fmt.Errorf("path resolves outside cwd_id directory")
				}
			} else if info.IsDir() || info.Mode().IsRegular() || check == filepath.Clean(resolved) {
				evaluated, evalErr := filepath.EvalSymlinks(check)
				if evalErr == nil && !pathInsideOrEqual(cwdAbs, evaluated) {
					return fmt.Errorf("path resolves outside cwd_id directory")
				}
			}
			return nil
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(check)
		if parent == check {
			return nil
		}
		check = parent
	}
}

func pathInsideOrEqual(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	if isWindowsRuntime() {
		root = strings.ToLower(root)
		candidate = strings.ToLower(candidate)
	}
	if root == candidate {
		return true
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func projectPathUnderCwd(pathCtx PathContext, abs string) string {
	rel, err := filepath.Rel(pathCtx.CwdAbs, filepath.Clean(abs))
	if err != nil || rel == "." {
		return "."
	}
	return slashPath(rel)
}

func (h *Handler) projectOutputPath(pathCtx PathContext, abs string) string {
	if pathCtx.HasCwd {
		return projectPathUnderCwd(pathCtx, abs)
	}
	return h.displayResolvedPath(abs, abs)
}

func (h *Handler) projectSearchPath(pathCtx PathContext, abs, requestedRoot, resolvedRoot string) string {
	if pathCtx.HasCwd {
		return projectPathUnderCwd(pathCtx, abs)
	}
	return h.displaySearchPath(abs, requestedRoot, resolvedRoot)
}

func (p PathContext) sanitizeErrorText(message string) string {
	if !p.HasCwd || message == "" {
		return message
	}
	out := strings.ReplaceAll(message, "\\", "/")
	for _, root := range []string{slashPath(p.CwdAbs), slashPath(p.CwdOut)} {
		if root == "" {
			continue
		}
		out = strings.ReplaceAll(out, root+"/", "")
		out = strings.ReplaceAll(out, root, ".")
	}
	return out
}

func (p PathContext) sanitizePathText(value string) string {
	if value == "" {
		return value
	}
	out := slashPath(value)
	if !p.HasCwd {
		return out
	}
	for _, root := range []string{slashPath(p.CwdAbs), slashPath(p.CwdOut)} {
		if root == "" {
			continue
		}
		if out == root {
			return "."
		}
		if strings.HasPrefix(out, root+"/") {
			return strings.TrimPrefix(out, root+"/")
		}
	}
	return out
}

func (p PathContext) sanitizePathSlice(values []string) []string {
	for i := range values {
		values[i] = p.sanitizePathText(values[i])
	}
	return values
}

func (p PathContext) fileCandidateAllowed(candidate string) bool {
	if !p.HasCwd {
		return true
	}
	info, err := os.Lstat(candidate)
	if err != nil {
		return false
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return true
	}
	evaluated, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return false
	}
	return pathInsideOrEqual(p.CwdAbs, evaluated)
}

func slashPath(value string) string {
	return strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
}

func isWindowsRuntime() bool {
	return runtime.GOOS == "windows"
}

func AttachCwdOutputMeta[Out any](output *Out, pathCtx PathContext) {
	if output == nil || !pathCtx.HasCwd {
		return
	}
	meta := pathCtx.outputMeta()
	switch value := any(output).(type) {
	case *ReadFileOutput:
		mergeCwdOutputMeta(&value.CwdOutputMeta, meta)
		completeCwdToolErrorMeta(&value.CwdOutputMeta, value.Error)
		promoteCwdToolErrorMeta(value.CwdOutputMeta, &value.ErrorCode, &value.ActionHint)
		sanitizeReadFileOutput(pathCtx, value)
	case *ReadFilesOutput:
		mergeCwdOutputMeta(&value.CwdOutputMeta, meta)
		completeCwdToolErrorMeta(&value.CwdOutputMeta, value.Error)
		promoteCwdToolErrorMeta(value.CwdOutputMeta, &value.ErrorCode, &value.ActionHint)
		sanitizeReadFilesOutput(pathCtx, value)
	case *ListDirOutput:
		mergeCwdOutputMeta(&value.CwdOutputMeta, meta)
		completeCwdToolErrorMeta(&value.CwdOutputMeta, value.Error)
		promoteCwdToolErrorMeta(value.CwdOutputMeta, &value.ErrorCode, &value.ActionHint)
		sanitizeListDirOutput(pathCtx, value)
	case *GlobFileSearchOutput:
		mergeCwdOutputMeta(&value.CwdOutputMeta, meta)
		completeCwdToolErrorMeta(&value.CwdOutputMeta, value.Error)
		promoteCwdToolErrorMeta(value.CwdOutputMeta, &value.ErrorCode, &value.ActionHint)
		sanitizeGlobFileSearchOutput(pathCtx, value)
	case *GrepOutput:
		mergeCwdOutputMeta(&value.CwdOutputMeta, meta)
		completeCwdToolErrorMeta(&value.CwdOutputMeta, value.Error)
		promoteCwdToolErrorMeta(value.CwdOutputMeta, &value.ErrorCode, &value.ActionHint)
		sanitizeGrepOutput(pathCtx, value)
	case *InspectPathOutput:
		mergeCwdOutputMeta(&value.CwdOutputMeta, meta)
		completeCwdToolErrorMeta(&value.CwdOutputMeta, value.Error)
		promoteCwdToolErrorMeta(value.CwdOutputMeta, &value.ErrorCode, &value.ActionHint)
		sanitizeInspectPathOutput(pathCtx, value)
	case *WorkspaceInventoryOutput:
		mergeCwdOutputMeta(&value.CwdOutputMeta, meta)
		completeCwdToolErrorMeta(&value.CwdOutputMeta, value.Error)
		promoteCwdToolErrorMeta(value.CwdOutputMeta, &value.ErrorCode, &value.ActionHint)
		sanitizeWorkspaceInventoryOutput(pathCtx, value)
	case *OutlineFileOutput:
		mergeCwdOutputMeta(&value.CwdOutputMeta, meta)
		completeCwdToolErrorMeta(&value.CwdOutputMeta, value.Error)
		promoteCwdToolErrorMeta(value.CwdOutputMeta, &value.ErrorCode, &value.ActionHint)
		addCwdIDToReplayHint(pathCtx, value.NextRecommendedCall)
		sanitizeOutlineFileOutput(pathCtx, value)
	case *ResolveSymbolRangeOutput:
		mergeCwdOutputMeta(&value.CwdOutputMeta, meta)
		completeCwdToolErrorMeta(&value.CwdOutputMeta, value.Error)
		promoteCwdToolErrorMeta(value.CwdOutputMeta, &value.ErrorCode, &value.ActionHint)
		addCwdIDToReplayHint(pathCtx, value.NextRecommendedCall)
		addCwdIDToReplayHint(pathCtx, value.RecommendedWriteCall)
		addCwdIDToReplayHint(pathCtx, value.PreviewWriteCall)
		sanitizeResolveSymbolRangeOutput(pathCtx, value)
	case *CopyRangesOutput:
		mergeCwdOutputMeta(&value.CwdOutputMeta, meta)
		completeCwdToolErrorMeta(&value.CwdOutputMeta, (*RangeTransferOutput)(value).Error)
		promoteCwdToolErrorMeta(value.CwdOutputMeta, &(*RangeTransferOutput)(value).ErrorCode, &(*RangeTransferOutput)(value).ActionHint)
		enrichRangeTransferReplayInputs(pathCtx, (*RangeTransferOutput)(value))
		sanitizeRangeTransferOutput(pathCtx, (*RangeTransferOutput)(value))
	case *MoveRangesOutput:
		mergeCwdOutputMeta(&value.CwdOutputMeta, meta)
		completeCwdToolErrorMeta(&value.CwdOutputMeta, (*RangeTransferOutput)(value).Error)
		promoteCwdToolErrorMeta(value.CwdOutputMeta, &(*RangeTransferOutput)(value).ErrorCode, &(*RangeTransferOutput)(value).ActionHint)
		enrichRangeTransferReplayInputs(pathCtx, (*RangeTransferOutput)(value))
		sanitizeRangeTransferOutput(pathCtx, (*RangeTransferOutput)(value))
	case *CopyRangesBatchOutput:
		mergeCwdOutputMeta(&value.CwdOutputMeta, meta)
		completeCwdToolErrorMeta(&value.CwdOutputMeta, (*BatchRangeTransferOutput)(value).Error)
		promoteCwdToolErrorMeta(value.CwdOutputMeta, &(*BatchRangeTransferOutput)(value).ErrorCode, &(*BatchRangeTransferOutput)(value).ActionHint)
		enrichBatchTransferReplayInputs(pathCtx, (*BatchRangeTransferOutput)(value))
		sanitizeBatchRangeTransferOutput(pathCtx, (*BatchRangeTransferOutput)(value))
	case *MoveRangesBatchOutput:
		mergeCwdOutputMeta(&value.CwdOutputMeta, meta)
		completeCwdToolErrorMeta(&value.CwdOutputMeta, (*BatchRangeTransferOutput)(value).Error)
		promoteCwdToolErrorMeta(value.CwdOutputMeta, &(*BatchRangeTransferOutput)(value).ErrorCode, &(*BatchRangeTransferOutput)(value).ActionHint)
		enrichBatchTransferReplayInputs(pathCtx, (*BatchRangeTransferOutput)(value))
		sanitizeBatchRangeTransferOutput(pathCtx, (*BatchRangeTransferOutput)(value))
	}
}

func sanitizeReadFileOutput(pathCtx PathContext, output *ReadFileOutput) {
	output.Error = pathCtx.sanitizeErrorText(output.Error)
	output.File = pathCtx.sanitizePathText(output.File)
	sanitizeContinuationHint(pathCtx, output.Continuation)
}

func sanitizeReadFilesOutput(pathCtx PathContext, output *ReadFilesOutput) {
	output.Error = pathCtx.sanitizeErrorText(output.Error)
	for i := range output.Items {
		output.Items[i].Error = pathCtx.sanitizeErrorText(output.Items[i].Error)
		output.Items[i].File = pathCtx.sanitizePathText(output.Items[i].File)
		sanitizeContinuationHint(pathCtx, output.Items[i].Continuation)
	}
	sanitizeContinuationHint(pathCtx, output.Continuation)
}

func sanitizeListDirOutput(pathCtx PathContext, output *ListDirOutput) {
	output.Error = pathCtx.sanitizeErrorText(output.Error)
	output.Directory = pathCtx.sanitizePathText(output.Directory)
	output.Message = pathCtx.sanitizeErrorText(output.Message)
}

func sanitizeGlobFileSearchOutput(pathCtx PathContext, output *GlobFileSearchOutput) {
	output.Error = pathCtx.sanitizeErrorText(output.Error)
	output.TargetDirectory = pathCtx.sanitizePathText(output.TargetDirectory)
	output.Message = pathCtx.sanitizeErrorText(output.Message)
	for i := range output.Files {
		output.Files[i].Path = pathCtx.sanitizePathText(output.Files[i].Path)
	}
	for i := range output.Groups {
		output.Groups[i].Directory = pathCtx.sanitizePathText(output.Groups[i].Directory)
		for j := range output.Groups[i].Files {
			output.Groups[i].Files[j].Path = pathCtx.sanitizePathText(output.Groups[i].Files[j].Path)
		}
	}
	sanitizeContinuationHint(pathCtx, output.Continuation)
	addCwdIDToReplayHint(pathCtx, output.NextRecommendedCall)
	sanitizeActionHint(pathCtx, output.NextRecommendedCall)
	for i := range output.NextRecommendedCalls {
		addCwdIDToReplayHint(pathCtx, &output.NextRecommendedCalls[i])
		sanitizeActionHint(pathCtx, &output.NextRecommendedCalls[i])
	}
}

func sanitizeGrepOutput(pathCtx PathContext, output *GrepOutput) {
	output.Error = pathCtx.sanitizeErrorText(output.Error)
	output.Path = pathCtx.sanitizePathText(output.Path)
	output.Message = pathCtx.sanitizeErrorText(output.Message)
	for i := range output.Matches {
		output.Matches[i].Path = pathCtx.sanitizePathText(output.Matches[i].Path)
	}
	output.Files = pathCtx.sanitizePathSlice(output.Files)
	for i := range output.Counts {
		output.Counts[i].Path = pathCtx.sanitizePathText(output.Counts[i].Path)
	}
	for i := range output.FileGroups {
		output.FileGroups[i].Path = pathCtx.sanitizePathText(output.FileGroups[i].Path)
	}
	addCwdIDToReplayHint(pathCtx, output.NextRecommendedCall)
	sanitizeActionHint(pathCtx, output.NextRecommendedCall)
	for i := range output.NextRecommendedCalls {
		addCwdIDToReplayHint(pathCtx, &output.NextRecommendedCalls[i])
		sanitizeActionHint(pathCtx, &output.NextRecommendedCalls[i])
	}
}

func sanitizeInspectPathOutput(pathCtx PathContext, output *InspectPathOutput) {
	output.Error = pathCtx.sanitizeErrorText(output.Error)
	output.Path = pathCtx.sanitizePathText(output.Path)
	output.ResolvedPath = pathCtx.sanitizePathText(output.ResolvedPath)
	output.SymlinkTarget = pathCtx.sanitizePathText(output.SymlinkTarget)
	if output.Visibility != nil {
		output.Visibility.TargetPath = pathCtx.sanitizePathText(output.Visibility.TargetPath)
	}
}

func sanitizeWorkspaceInventoryOutput(pathCtx PathContext, output *WorkspaceInventoryOutput) {
	output.Error = pathCtx.sanitizeErrorText(output.Error)
	output.TruncationReason = pathCtx.sanitizeErrorText(output.TruncationReason)
	if output.Root != nil {
		sanitizeWorkspaceDirectoryNode(pathCtx, output.Root)
	}
	for i := range output.DirectoriesPage {
		output.DirectoriesPage[i].Path = pathCtx.sanitizePathText(output.DirectoriesPage[i].Path)
		output.DirectoriesPage[i].ParentPath = pathCtx.sanitizePathText(output.DirectoriesPage[i].ParentPath)
		output.DirectoriesPage[i].ReadError = pathCtx.sanitizeErrorText(output.DirectoriesPage[i].ReadError)
	}
	if output.Summary != nil {
		sanitizeWorkspaceSummary(pathCtx, output.Summary)
	}
	sanitizeContinuationHint(pathCtx, output.Continuation)
	addCwdIDToReplayHint(pathCtx, output.NextRecommendedCall)
	sanitizeActionHint(pathCtx, output.NextRecommendedCall)
	for i := range output.NextRecommendedCalls {
		addCwdIDToReplayHint(pathCtx, &output.NextRecommendedCalls[i])
		sanitizeActionHint(pathCtx, &output.NextRecommendedCalls[i])
	}
}

func sanitizeWorkspaceDirectoryNode(pathCtx PathContext, node *WorkspaceDirectoryNode) {
	if node == nil {
		return
	}
	node.Path = pathCtx.sanitizePathText(node.Path)
	node.ReadError = pathCtx.sanitizeErrorText(node.ReadError)
	for i := range node.Directories {
		sanitizeWorkspaceDirectoryNode(pathCtx, &node.Directories[i])
	}
}

func sanitizeOutlineFileOutput(pathCtx PathContext, output *OutlineFileOutput) {
	output.Error = pathCtx.sanitizeErrorText(output.Error)
	output.File = pathCtx.sanitizePathText(output.File)
	output.Warnings = sanitizeToolWarnings(pathCtx, output.Warnings)
	sanitizeActionHint(pathCtx, output.NextRecommendedCall)
}

func sanitizeResolveSymbolRangeOutput(pathCtx PathContext, output *ResolveSymbolRangeOutput) {
	output.Error = pathCtx.sanitizeErrorText(output.Error)
	output.File = pathCtx.sanitizePathText(output.File)
	output.WriteRefusalReason = pathCtx.sanitizeErrorText(output.WriteRefusalReason)
	output.TargetSyntaxProofReason = pathCtx.sanitizeErrorText(output.TargetSyntaxProofReason)
	sanitizeActionHint(pathCtx, output.ActionHint)
	sanitizeActionHint(pathCtx, output.NextRecommendedCall)
	sanitizeActionHint(pathCtx, output.RecommendedWriteCall)
	sanitizeActionHint(pathCtx, output.PreviewWriteCall)
	for i := range output.NextRecommendedCalls {
		sanitizeActionHint(pathCtx, &output.NextRecommendedCalls[i])
	}
}

func sanitizeRangeTransferOutput(pathCtx PathContext, output *RangeTransferOutput) {
	if output == nil {
		return
	}
	output.Error = pathCtx.sanitizeErrorText(output.Error)
	output.SourceFile = pathCtx.sanitizePathText(output.SourceFile)
	output.TargetFile = pathCtx.sanitizePathText(output.TargetFile)
	output.BoundaryWarnings = sanitizeBoundaryWarnings(pathCtx, output.BoundaryWarnings)
	output.Warnings = sanitizeToolWarnings(pathCtx, output.Warnings)
	output.BackupPaths = pathCtx.sanitizePathSlice(output.BackupPaths)
	output.BackupResults = sanitizeBackupResults(pathCtx, output.BackupResults)
	output.DiffPreviews = sanitizeDiffPreviews(pathCtx, output.DiffPreviews)
	sanitizeBoundaryPreview(pathCtx, &output.BoundaryPreview)
	sanitizeWriteValidation(pathCtx, &output.Validation)
	sanitizeBackupDiscovery(pathCtx, output.BackupDiscovery)
	sanitizePartialState(pathCtx, output.PartialState)
	sanitizeActionHint(pathCtx, output.ActionHint)
}

func sanitizeBatchRangeTransferOutput(pathCtx PathContext, output *BatchRangeTransferOutput) {
	if output == nil {
		return
	}
	output.Error = pathCtx.sanitizeErrorText(output.Error)
	output.SourceFile = pathCtx.sanitizePathText(output.SourceFile)
	output.TargetResults = sanitizeBatchTargetResults(pathCtx, output.TargetResults)
	output.TargetsWritten = pathCtx.sanitizePathSlice(output.TargetsWritten)
	output.BatchWarnings = sanitizeToolWarnings(pathCtx, output.BatchWarnings)
	output.Warnings = sanitizeToolWarnings(pathCtx, output.Warnings)
	output.BackupPaths = pathCtx.sanitizePathSlice(output.BackupPaths)
	output.BackupResults = sanitizeBackupResults(pathCtx, output.BackupResults)
	output.SourceDiffPreviews = sanitizeDiffPreviews(pathCtx, output.SourceDiffPreviews)
	if output.SourceValidation != nil {
		sanitizeWriteValidation(pathCtx, output.SourceValidation)
	}
	sanitizeBackupDiscovery(pathCtx, output.BackupDiscovery)
	sanitizeBatchPartialState(pathCtx, output.PartialState)
	sanitizeActionHint(pathCtx, output.ActionHint)
}

func sanitizePartialState(pathCtx PathContext, partial *PartialState) {
	if partial == nil {
		return
	}
	partial.SourceFile = pathCtx.sanitizePathText(partial.SourceFile)
	partial.TargetFile = pathCtx.sanitizePathText(partial.TargetFile)
	partial.FilesMaybeModified = pathCtx.sanitizePathSlice(partial.FilesMaybeModified)
	partial.BackupPaths = pathCtx.sanitizePathSlice(partial.BackupPaths)
	partial.Error = pathCtx.sanitizeErrorText(partial.Error)
	partial.RecoveryHint = pathCtx.sanitizeErrorText(partial.RecoveryHint)
	sanitizeRecommendedInput(pathCtx, partial.RecommendedNextInput)
}

func sanitizeBatchPartialState(pathCtx PathContext, partial *BatchPartialState) {
	if partial == nil {
		return
	}
	partial.SourceFile = pathCtx.sanitizePathText(partial.SourceFile)
	partial.TargetResults = sanitizeBatchTargetResults(pathCtx, partial.TargetResults)
	partial.BackupPaths = pathCtx.sanitizePathSlice(partial.BackupPaths)
	partial.BackupResults = sanitizeBackupResults(pathCtx, partial.BackupResults)
	partial.Error = pathCtx.sanitizeErrorText(partial.Error)
	partial.RecoveryHint = pathCtx.sanitizeErrorText(partial.RecoveryHint)
	sanitizeRecommendedInput(pathCtx, partial.RecommendedNextInput)
}

func sanitizeBatchTargetResults(pathCtx PathContext, results []BatchTargetResult) []BatchTargetResult {
	for i := range results {
		results[i].TargetFile = pathCtx.sanitizePathText(results[i].TargetFile)
		results[i].Error = pathCtx.sanitizeErrorText(results[i].Error)
		results[i].BackupPaths = pathCtx.sanitizePathSlice(results[i].BackupPaths)
		results[i].BackupError = pathCtx.sanitizeErrorText(results[i].BackupError)
		results[i].BoundaryWarnings = sanitizeBoundaryWarnings(pathCtx, results[i].BoundaryWarnings)
		results[i].Warnings = sanitizeToolWarnings(pathCtx, results[i].Warnings)
		results[i].DiffPreviews = sanitizeDiffPreviews(pathCtx, results[i].DiffPreviews)
		sanitizeBoundaryPreview(pathCtx, &results[i].BoundaryPreview)
		sanitizeWriteValidation(pathCtx, &results[i].Validation)
	}
	return results
}

func sanitizeToolWarnings(pathCtx PathContext, warnings []ToolWarning) []ToolWarning {
	for i := range warnings {
		warnings[i].Message = pathCtx.sanitizeErrorText(warnings[i].Message)
		warnings[i].File = pathCtx.sanitizePathText(warnings[i].File)
	}
	return warnings
}

func sanitizeBoundaryWarnings(pathCtx PathContext, warnings []BoundaryWarning) []BoundaryWarning {
	for i := range warnings {
		warnings[i].Message = pathCtx.sanitizeErrorText(warnings[i].Message)
		warnings[i].TargetFile = pathCtx.sanitizePathText(warnings[i].TargetFile)
		warnings[i].RecommendedAction = pathCtx.sanitizeErrorText(warnings[i].RecommendedAction)
	}
	return warnings
}

func sanitizeBackupResults(pathCtx PathContext, results []BackupResult) []BackupResult {
	for i := range results {
		results[i].File = pathCtx.sanitizePathText(results[i].File)
		results[i].BackupPath = pathCtx.sanitizePathText(results[i].BackupPath)
		results[i].Error = pathCtx.sanitizeErrorText(results[i].Error)
	}
	return results
}

func sanitizeDiffPreviews(pathCtx PathContext, previews []DiffPreview) []DiffPreview {
	for i := range previews {
		previews[i].Text = pathCtx.sanitizeErrorText(previews[i].Text)
	}
	return previews
}

func sanitizeBoundaryPreview(pathCtx PathContext, preview *BoundaryPreview) {
	if preview == nil {
		return
	}
	preview.TargetFile = pathCtx.sanitizePathText(preview.TargetFile)
	preview.Before = pathCtx.sanitizeErrorText(preview.Before)
	preview.Between = pathCtx.sanitizeErrorText(preview.Between)
	preview.After = pathCtx.sanitizeErrorText(preview.After)
}

func sanitizeWriteValidation(pathCtx PathContext, validation *WriteValidation) {
	if validation == nil {
		return
	}
	validation.Error = pathCtx.sanitizeErrorText(validation.Error)
	for i := range validation.TargetReadBack {
		sanitizeReadBackWindow(pathCtx, &validation.TargetReadBack[i])
	}
	for i := range validation.SourceReadBack {
		sanitizeReadBackWindow(pathCtx, &validation.SourceReadBack[i])
	}
	addCwdIDToReplayHint(pathCtx, validation.NextRecommendedCall)
	sanitizeActionHint(pathCtx, validation.NextRecommendedCall)
	for i := range validation.NextRecommendedCalls {
		addCwdIDToReplayHint(pathCtx, &validation.NextRecommendedCalls[i])
		sanitizeActionHint(pathCtx, &validation.NextRecommendedCalls[i])
	}
}

func sanitizeReadBackWindow(pathCtx PathContext, window *ReadBackWindow) {
	if window == nil {
		return
	}
	window.File = pathCtx.sanitizePathText(window.File)
	window.Text = pathCtx.sanitizeErrorText(window.Text)
}

func sanitizeBackupDiscovery(pathCtx PathContext, discovery *BackupDiscoveryHint) {
	if discovery == nil {
		return
	}
	discovery.BackupPaths = pathCtx.sanitizePathSlice(discovery.BackupPaths)
	for i := range discovery.DiscoveryGroups {
		discovery.DiscoveryGroups[i].Directory = pathCtx.sanitizePathText(discovery.DiscoveryGroups[i].Directory)
		discovery.DiscoveryGroups[i].BackupPaths = pathCtx.sanitizePathSlice(discovery.DiscoveryGroups[i].BackupPaths)
		addCwdIDToReplayHint(pathCtx, &discovery.DiscoveryGroups[i].NextRecommendedCall)
		sanitizeActionHint(pathCtx, &discovery.DiscoveryGroups[i].NextRecommendedCall)
	}
	addCwdIDToReplayHint(pathCtx, discovery.NextRecommendedCall)
	sanitizeActionHint(pathCtx, discovery.NextRecommendedCall)
	for i := range discovery.NextRecommendedCalls {
		addCwdIDToReplayHint(pathCtx, &discovery.NextRecommendedCalls[i])
		sanitizeActionHint(pathCtx, &discovery.NextRecommendedCalls[i])
	}
	discovery.Reason = pathCtx.sanitizeErrorText(discovery.Reason)
}

func sanitizeWorkspaceSummary(pathCtx PathContext, summary *WorkspaceSummary) {
	if summary == nil {
		return
	}
	summary.PackageHints = pathCtx.sanitizePathSlice(summary.PackageHints)
	summary.SourceDirHints = pathCtx.sanitizePathSlice(summary.SourceDirHints)
	summary.TestDirHints = pathCtx.sanitizePathSlice(summary.TestDirHints)
	for i := range summary.LargestDirectories {
		summary.LargestDirectories[i].Path = pathCtx.sanitizePathText(summary.LargestDirectories[i].Path)
		summary.LargestDirectories[i].ParentPath = pathCtx.sanitizePathText(summary.LargestDirectories[i].ParentPath)
	}
	for i := range summary.BackupCandidateDirectories {
		summary.BackupCandidateDirectories[i].Path = pathCtx.sanitizePathText(summary.BackupCandidateDirectories[i].Path)
	}
	for i := range summary.BackupDiscoveryHints {
		addCwdIDToReplayHint(pathCtx, &summary.BackupDiscoveryHints[i])
		sanitizeActionHint(pathCtx, &summary.BackupDiscoveryHints[i])
	}
}

func sanitizeContinuationHint(pathCtx PathContext, continuation *ContinuationHint) {
	if continuation == nil {
		return
	}
	if continuation.LastSortKey != nil {
		continuation.LastSortKey.Path = pathCtx.sanitizePathText(continuation.LastSortKey.Path)
	}
	addCwdIDToReplayHint(pathCtx, continuation.NextRecommendedCall)
	sanitizeActionHint(pathCtx, continuation.NextRecommendedCall)
	for i := range continuation.NextRecommendedCalls {
		addCwdIDToReplayHint(pathCtx, &continuation.NextRecommendedCalls[i])
		sanitizeActionHint(pathCtx, &continuation.NextRecommendedCalls[i])
	}
	continuation.Reason = pathCtx.sanitizeErrorText(continuation.Reason)
}

func sanitizeActionHint(pathCtx PathContext, hint *ActionHint) {
	if hint == nil {
		return
	}
	hint.Reason = pathCtx.sanitizeErrorText(hint.Reason)
	sanitizeRecommendedInput(pathCtx, hint.RecommendedNextInput)
}

func sanitizeRecommendedInput(pathCtx PathContext, input map[string]any) {
	if input == nil {
		return
	}
	for _, key := range []string{"path", "source_file", "target_directory", "target_file", "target_path"} {
		value, ok := input[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			input[key] = pathCtx.sanitizePathText(typed)
		case []string:
			input[key] = pathCtx.sanitizePathSlice(typed)
		case []any:
			for i, item := range typed {
				if s, ok := item.(string); ok {
					typed[i] = pathCtx.sanitizePathText(s)
				}
			}
			input[key] = typed
		}
	}
	sanitizeRecommendedTargets(pathCtx, input["targets"])
	sanitizeRecommendedItems(pathCtx, input["items"])
	sanitizeRecommendedContinuationAfter(pathCtx, input["continuation_after"])
}

func sanitizeRecommendedTargets(pathCtx PathContext, value any) {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			sanitizeRecommendedTargets(pathCtx, item)
		}
	case []map[string]any:
		for _, item := range typed {
			sanitizeRecommendedTargets(pathCtx, item)
		}
	case map[string]any:
		if value, ok := typed["target_file"].(string); ok {
			typed["target_file"] = pathCtx.sanitizePathText(value)
		}
	}
}

func sanitizeRecommendedItems(pathCtx PathContext, value any) {
	switch typed := value.(type) {
	case []map[string]any:
		for _, item := range typed {
			if value, ok := item["target_file"].(string); ok {
				item["target_file"] = pathCtx.sanitizePathText(value)
			}
		}
	case []any:
		for _, item := range typed {
			if object, ok := item.(map[string]any); ok {
				if value, ok := object["target_file"].(string); ok {
					object["target_file"] = pathCtx.sanitizePathText(value)
				}
			}
		}
	}
}

func sanitizeRecommendedContinuationAfter(pathCtx PathContext, value any) {
	object, ok := value.(map[string]any)
	if !ok {
		return
	}
	key, ok := object["last_sort_key"].(map[string]any)
	if !ok {
		return
	}
	if value, ok := key["path"].(string); ok {
		key["path"] = pathCtx.sanitizePathText(value)
	}
}

func mergeCwdOutputMeta(target *CwdOutputMeta, source CwdOutputMeta) {
	if target == nil {
		return
	}
	if source.CwdID != nil {
		target.CwdID = source.CwdID
	}
	if source.Cwd != "" {
		target.Cwd = source.Cwd
	}
}

func completeCwdToolErrorMeta(meta *CwdOutputMeta, message string) {
	if meta == nil || strings.TrimSpace(message) == "" {
		return
	}
	if meta.ErrorCode == "" {
		meta.ErrorCode = cwdPathErrorCode(message)
	}
	if meta.ActionHint == nil {
		meta.ActionHint = cwdPathErrorHint(meta.ErrorCode)
	}
}

func promoteCwdToolErrorMeta(meta CwdOutputMeta, errorCode *string, actionHint **ActionHint) {
	if errorCode != nil && *errorCode == "" {
		*errorCode = meta.ErrorCode
	}
	if actionHint != nil && *actionHint == nil {
		*actionHint = meta.ActionHint
	}
}

func cwdPathErrorCode(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "must be relative when cwd_id"):
		return "absolute_path_not_allowed_with_cwd"
	case strings.Contains(lower, "escapes cwd_id") || strings.Contains(lower, "outside cwd_id") || strings.Contains(lower, "resolves outside cwd"):
		return "path_outside_cwd"
	case strings.Contains(lower, "relative paths require cwd_id") || strings.Contains(lower, "must be an absolute path"):
		return "relative_path_requires_cwd"
	case strings.Contains(lower, "is required"):
		return "invalid_path"
	default:
		return "tool_error"
	}
}

func cwdPathErrorHint(code string) *ActionHint {
	reason := "Inspect the structured error and adjust the path input before retrying."
	switch code {
	case "absolute_path_not_allowed_with_cwd":
		reason = "Omit cwd_id for absolute paths, or pass a relative path under the registered cwd."
	case "path_outside_cwd":
		reason = "Use a path inside the registered cwd, or register a different cwd with set_cwd."
	case "relative_path_requires_cwd":
		reason = "Call set_cwd and pass cwd_id for relative paths, or use an absolute path without cwd_id."
	case "invalid_path":
		reason = "Provide the required non-empty path input."
	}
	return &ActionHint{
		SafeToRetry: false,
		Reason:      reason,
	}
}

func enrichRangeTransferReplayInputs(pathCtx PathContext, output *RangeTransferOutput) {
	if output == nil || !pathCtx.HasCwd {
		return
	}
	addCwdIDToReplayHint(pathCtx, output.ActionHint)
	if output.PartialState != nil {
		addCwdIDToRecommendedInput(pathCtx, output.PartialState.RecommendedNextTool, output.PartialState.RecommendedNextInput)
	}
}

func enrichBatchTransferReplayInputs(pathCtx PathContext, output *BatchRangeTransferOutput) {
	if output == nil || !pathCtx.HasCwd {
		return
	}
	addCwdIDToReplayHint(pathCtx, output.ActionHint)
	if output.PartialState != nil {
		addCwdIDToRecommendedInput(pathCtx, output.PartialState.RecommendedNextTool, output.PartialState.RecommendedNextInput)
	}
}

func addCwdIDToReplayHint(pathCtx PathContext, hint *ActionHint) {
	if hint == nil {
		return
	}
	addCwdIDToRecommendedInput(pathCtx, hint.RecommendedNextTool, hint.RecommendedNextInput)
}

func addCwdIDToRecommendedInput(pathCtx PathContext, recommendedTool string, input map[string]any) {
	if !pathCtx.HasCwd || input == nil || !toolAcceptsCwdID(recommendedTool) {
		return
	}
	input["cwd_id"] = pathCtx.CwdID
}

func toolAcceptsCwdID(name string) bool {
	switch name {
	case "read_file", "read_files", "outline_file", "resolve_symbol_range", "copy_ranges", "move_ranges", "copy_ranges_batch", "move_ranges_batch", "list_dir", "glob_file_search", "grep", "inspect_path", "workspace_inventory":
		return true
	default:
		return false
	}
}
