package handler

type SourceLineRange struct {
	StartLine int `json:"start_line"`
	EndLine   int `json:"end_line"`
}

type FileFingerprint struct {
	SHA256           string `json:"sha256"`
	SizeBytes        int64  `json:"size_bytes"`
	LineCount        int    `json:"line_count"`
	ModifiedUnixNano int64  `json:"modified_unix_nano"`
}

type ActionHint struct {
	SafeToRetry                bool           `json:"safe_to_retry"`
	RecommendedNextTool        string         `json:"recommended_next_tool,omitempty"`
	RecommendedNextInput       map[string]any `json:"recommended_next_input,omitempty"`
	RecommendedNextInputPolicy string         `json:"recommended_next_input_policy,omitempty"`
	Reason                     string         `json:"reason,omitempty"`
}

type DiffPreviewStats struct {
	FilesChanged  int `json:"files_changed"`
	LinesAdded    int `json:"lines_added"`
	LinesRemoved  int `json:"lines_removed"`
	HunksReturned int `json:"hunks_returned"`
	HunksOmitted  int `json:"hunks_omitted,omitempty"`
}

type DiffPreview struct {
	Role          string           `json:"role"`
	Format        string           `json:"format"`
	Text          string           `json:"text,omitempty"`
	Truncated     bool             `json:"truncated"`
	Stats         DiffPreviewStats `json:"stats"`
	Redacted      bool             `json:"redacted"`
	RedactionMode string           `json:"redaction_mode,omitempty"`
	PathMode      string           `json:"path_mode,omitempty"`
}

type JoinerEffect struct {
	Requested                     string                 `json:"requested"`
	Normalized                    string                 `json:"normalized"`
	NewlineBytes                  string                 `json:"newline_bytes,omitempty"`
	InsertedNewlinesBetweenBlocks int                    `json:"inserted_newlines_between_blocks"`
	SourceRangeJoinCount          int                    `json:"source_range_join_count"`
	InsertedNewlinesBetweenRanges int                    `json:"inserted_newlines_between_ranges"`
	LeftEndedWithNewline          bool                   `json:"left_ended_with_newline"`
	RightStartedWithNewline       bool                   `json:"right_started_with_newline"`
	SourceBoundaries              []JoinerBoundaryEffect `json:"source_boundaries,omitempty"`
	TargetBoundary                *JoinerBoundaryEffect  `json:"target_boundary,omitempty"`
	TargetBoundaries              []JoinerBoundaryEffect `json:"target_boundaries,omitempty"`
}

type JoinerBoundaryEffect struct {
	Boundary                string   `json:"boundary"`
	ExistingLeftNewlines    int      `json:"existing_left_newlines"`
	ExistingRightNewlines   int      `json:"existing_right_newlines"`
	InsertedNewlines        int      `json:"inserted_newlines"`
	VisualBlankLinesBetween int      `json:"visual_blank_lines_between"`
	LeftEndedWithNewline    bool     `json:"left_ended_with_newline"`
	RightStartedWithNewline bool     `json:"right_started_with_newline"`
	WarningCodes            []string `json:"warning_codes,omitempty"`
}

type BoundaryPreview struct {
	TargetFile    string `json:"target_file,omitempty"`
	Placement     string `json:"placement"`
	Before        string `json:"before,omitempty"`
	Between       string `json:"between,omitempty"`
	After         string `json:"after,omitempty"`
	Redacted      bool   `json:"redacted"`
	RedactionMode string `json:"redaction_mode,omitempty"`
	Truncated     bool   `json:"truncated"`
}

type ReadBackWindow struct {
	File          string    `json:"file"`
	Range         LineRange `json:"range"`
	Text          string    `json:"text,omitempty"`
	Truncated     bool      `json:"truncated"`
	Redacted      bool      `json:"redacted"`
	RedactionMode string    `json:"redaction_mode,omitempty"`
}

type WriteValidation struct {
	Status               string           `json:"status"`
	TargetReadBack       []ReadBackWindow `json:"target_read_back"`
	SourceReadBack       []ReadBackWindow `json:"source_read_back,omitempty"`
	RedactionMode        string           `json:"redaction_mode,omitempty"`
	NextRecommendedCall  *ActionHint      `json:"next_recommended_call,omitempty"`
	NextRecommendedCalls []ActionHint     `json:"next_recommended_calls,omitempty"`
	ErrorCode            string           `json:"error_code,omitempty"`
	Error                string           `json:"error,omitempty"`
}

type BackupDiscoveryHint struct {
	BackupPaths          []string               `json:"backup_paths"`
	DiscoveryGroups      []BackupDiscoveryGroup `json:"discovery_groups"`
	NextRecommendedCall  *ActionHint            `json:"next_recommended_call,omitempty"`
	NextRecommendedCalls []ActionHint           `json:"next_recommended_calls,omitempty"`
	Reason               string                 `json:"reason,omitempty"`
}

type BackupDiscoveryGroup struct {
	Role                string     `json:"role,omitempty"`
	Directory           string     `json:"directory"`
	GlobPattern         string     `json:"glob_pattern"`
	IncludeHidden       bool       `json:"include_hidden"`
	BackupPaths         []string   `json:"backup_paths"`
	NextRecommendedCall ActionHint `json:"next_recommended_call"`
}

type ReadCoverage struct {
	RequestedRangeComplete bool               `json:"requested_range_complete"`
	CompleteFileRead       bool               `json:"complete_file_read"`
	FileTotalLinesKnown    bool               `json:"file_total_lines_known"`
	NextRange              *SourceLineRange   `json:"next_range,omitempty"`
	Proof                  *ReadCoverageProof `json:"proof,omitempty"`
}

type ReadCoverageProof struct {
	SizeBytes        int64           `json:"size_bytes"`
	ModifiedUnixNano int64           `json:"modified_unix_nano"`
	SHA256           string          `json:"sha256,omitempty"`
	ProofStrength    string          `json:"proof_strength"`
	Range            SourceLineRange `json:"range"`
}

type DiscoverySortKey struct {
	Path             string `json:"path"`
	ModifiedUnixNano *int64 `json:"modified_unix_nano,omitempty"`
	SizeBytes        *int64 `json:"size_bytes,omitempty"`
}

type DiscoveryContinuationAfter struct {
	CanonicalQueryHash string           `json:"canonical_query_hash"`
	LastSortKey        DiscoverySortKey `json:"last_sort_key"`
}

type ContinuationHint struct {
	Complete             bool              `json:"complete"`
	PageComplete         *bool             `json:"page_complete,omitempty"`
	Consistency          string            `json:"consistency,omitempty"`
	CanonicalQueryHash   string            `json:"canonical_query_hash,omitempty"`
	LastSortKey          *DiscoverySortKey `json:"last_sort_key,omitempty"`
	StaleIfFileChanges   bool              `json:"stale_if_file_changes,omitempty"`
	NextRecommendedCall  *ActionHint       `json:"next_recommended_call,omitempty"`
	NextRecommendedCalls []ActionHint      `json:"next_recommended_calls,omitempty"`
	Reason               string            `json:"reason,omitempty"`
}

type WorkspaceDirectoryPageEntry struct {
	Path            string `json:"path"`
	ParentPath      string `json:"parent_path,omitempty"`
	Depth           int    `json:"depth"`
	DirectFileCount int    `json:"direct_file_count"`
	DirectDirCount  int    `json:"direct_dir_count"`
	ReadError       string `json:"read_error,omitempty"`
}

type OutlineContinuationHint struct {
	Complete             bool             `json:"complete"`
	Consistency          string           `json:"consistency,omitempty"`
	CanonicalQueryHash   string           `json:"canonical_query_hash,omitempty"`
	LastIncludedLine     int              `json:"last_included_line,omitempty"`
	NextOmittedLine      int              `json:"next_omitted_line,omitempty"`
	NextOmittedItemKey   string           `json:"next_omitted_item_key,omitempty"`
	SourceFingerprint    *FileFingerprint `json:"source_fingerprint,omitempty"`
	StaleIfFileChanges   bool             `json:"stale_if_file_changes,omitempty"`
	NextRecommendedCall  *ActionHint      `json:"next_recommended_call,omitempty"`
	NextRecommendedCalls []ActionHint     `json:"next_recommended_calls,omitempty"`
	Reason               string           `json:"reason,omitempty"`
}

type ToolWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
}

type WarningSummary struct {
	TotalWarnings int            `json:"total_warnings"`
	ByCode        map[string]int `json:"by_code,omitempty"`
	ByTargetRole  map[string]int `json:"by_target_role,omitempty"`
}

type BoundaryWarning struct {
	Code              string `json:"code"`
	Message           string `json:"message"`
	TargetFile        string `json:"target_file,omitempty"`
	Placement         string `json:"placement,omitempty"`
	Boundary          string `json:"boundary,omitempty"`
	RecommendedAction string `json:"recommended_action,omitempty"`
}

type OutlineStats struct {
	ItemsReturned     int    `json:"items_returned"`
	ItemsOmitted      int    `json:"items_omitted,omitempty"`
	ItemsOmittedKnown bool   `json:"items_omitted_known"`
	OmittedLeafItems  int    `json:"omitted_leaf_items,omitempty"`
	LastIncludedLine  int    `json:"last_included_line,omitempty"`
	NextOmittedLine   int    `json:"next_omitted_line,omitempty"`
	NextOmittedKind   string `json:"next_omitted_kind,omitempty"`
	NextOmittedName   string `json:"next_omitted_name,omitempty"`
	TruncationReason  string `json:"truncation_reason,omitempty"`
}

type OutlineItem struct {
	ID               string            `json:"id"`
	Kind             string            `json:"kind"`
	Name             string            `json:"name"`
	Detail           string            `json:"detail,omitempty"`
	Path             []string          `json:"path,omitempty"`
	EnclosingPath    []string          `json:"enclosing_path,omitempty"`
	Range            SourceLineRange   `json:"range"`
	ByteRange        *SourceByteRange  `json:"byte_range,omitempty"`
	Depth            int               `json:"depth,omitempty"`
	Confidence       string            `json:"confidence"`
	RangeIsEstimated bool              `json:"range_is_estimated"`
	RangeFingerprint *FileFingerprint  `json:"range_fingerprint,omitempty"`
	Selector         *OutlineSelector  `json:"selector,omitempty"`
	SymbolRef        string            `json:"symbol_ref,omitempty"`
	WholeLineRange   *bool             `json:"whole_line_range,omitempty"`
	WriteSafe        *bool             `json:"write_safe,omitempty"`
	RefusalReason    string            `json:"refusal_reason,omitempty"`
	Children         []OutlineItem     `json:"children,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

type SourceByteRange struct {
	StartByte        int `json:"start_byte"`
	EndByteExclusive int `json:"end_byte_exclusive"`
}

type OutlineSelector struct {
	Language         string          `json:"language"`
	Kind             string          `json:"kind"`
	Name             string          `json:"name"`
	SymbolPath       []string        `json:"symbol_path"`
	Range            SourceLineRange `json:"range"`
	ByteRange        SourceByteRange `json:"byte_range"`
	WholeLineRange   bool            `json:"whole_line_range"`
	WriteSafe        bool            `json:"write_safe"`
	RangeFingerprint FileFingerprint `json:"range_fingerprint"`
	SymbolRef        string          `json:"symbol_ref"`
	Disambiguator    string          `json:"disambiguator,omitempty"`
}

type SymbolSelectorQuery struct {
	SymbolRef        string           `json:"symbol_ref,omitempty"`
	Language         string           `json:"language,omitempty"`
	Kind             string           `json:"kind,omitempty"`
	Name             string           `json:"name,omitempty"`
	SymbolPath       []string         `json:"symbol_path,omitempty"`
	Range            *SourceLineRange `json:"range,omitempty"`
	RangeFingerprint *FileFingerprint `json:"range_fingerprint,omitempty"`
	Disambiguator    string           `json:"disambiguator,omitempty"`
	EnclosingLine    *int             `json:"enclosing_line,omitempty"`
	AllowEstimated   bool             `json:"allow_estimated,omitempty"`
}

type ResolveSymbolRangeInput struct {
	CwdAwareInput
	SourceFile        string              `json:"source_file"`
	Language          string              `json:"language,omitempty"`
	Selector          SymbolSelectorQuery `json:"selector"`
	SourceFingerprint FileFingerprint     `json:"source_fingerprint"`
	TargetIntent      *WriteTargetIntent  `json:"target_intent,omitempty"`
}

type WriteTargetIntent struct {
	Operation          string             `json:"operation"`
	TargetFile         string             `json:"target_file"`
	TargetPrecondition TargetPrecondition `json:"target_precondition,omitempty"`
	Placement          TargetPlacement    `json:"placement"`
	TargetSyntaxMode   string             `json:"target_syntax_mode,omitempty"`
	Joiner             string             `json:"joiner,omitempty"`
	Backup             *BackupSpec        `json:"backup,omitempty"`
	RedactionMode      string             `json:"redaction_mode,omitempty"`
	DryRun             bool               `json:"dry_run,omitempty"`
}

type ResolvedSymbolMatch struct {
	SymbolRef        string            `json:"symbol_ref,omitempty"`
	Kind             string            `json:"kind"`
	Name             string            `json:"name"`
	SymbolPath       []string          `json:"symbol_path,omitempty"`
	Range            SourceLineRange   `json:"range"`
	ByteRange        *SourceByteRange  `json:"byte_range,omitempty"`
	Confidence       string            `json:"confidence"`
	RangeIsEstimated bool              `json:"range_is_estimated"`
	WholeLineRange   bool              `json:"whole_line_range"`
	WriteSafe        bool              `json:"write_safe"`
	Disambiguator    string            `json:"disambiguator,omitempty"`
	RefusalReason    string            `json:"refusal_reason,omitempty"`
	Selector         *OutlineSelector  `json:"selector,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

type ResolvedRange struct {
	Range            SourceLineRange  `json:"range"`
	ByteRange        *SourceByteRange `json:"byte_range,omitempty"`
	Confidence       string           `json:"confidence"`
	RangeIsEstimated bool             `json:"range_is_estimated"`
	WholeLineRange   bool             `json:"whole_line_range"`
	WriteSafe        bool             `json:"write_safe"`
	RangeFingerprint FileFingerprint  `json:"range_fingerprint"`
	Selector         *OutlineSelector `json:"selector,omitempty"`
	RefusalReason    string           `json:"refusal_reason,omitempty"`
}

type ResolveSymbolRangeOutput struct {
	CwdOutputMeta
	Error                     string                `json:"error,omitempty"`
	ErrorCode                 string                `json:"error_code,omitempty"`
	File                      string                `json:"file,omitempty"`
	Language                  string                `json:"language,omitempty"`
	ParserStatus              string                `json:"parser_status,omitempty"`
	Fingerprint               *FileFingerprint      `json:"fingerprint,omitempty"`
	Matches                   []ResolvedSymbolMatch `json:"matches"`
	ResolvedRanges            []ResolvedRange       `json:"resolved_ranges"`
	Ambiguous                 bool                  `json:"ambiguous"`
	ResolutionStatus          string                `json:"resolution_status"`
	NextRecommendedCall       *ActionHint           `json:"next_recommended_call,omitempty"`
	NextRecommendedCalls      []ActionHint          `json:"next_recommended_calls,omitempty"`
	WriteRecommendationStatus string                `json:"write_recommendation_status,omitempty"`
	WriteRefusalCode          string                `json:"write_refusal_code,omitempty"`
	WriteRefusalReason        string                `json:"write_refusal_reason,omitempty"`
	TargetSyntaxStatus        string                `json:"target_syntax_status,omitempty"`
	TargetSyntaxProof         string                `json:"target_syntax_proof,omitempty"`
	TargetSyntaxProofReason   string                `json:"target_syntax_proof_reason,omitempty"`
	RecommendedWriteCall      *ActionHint           `json:"recommended_write_call,omitempty"`
	PreviewWriteCall          *ActionHint           `json:"preview_write_call,omitempty"`
	ActionHint                *ActionHint           `json:"action_hint,omitempty"`
}

type OutlineFileInput struct {
	CwdAwareInput
	TargetFile      string           `json:"target_file"`
	Language        string           `json:"language,omitempty"`
	OutputProfile   string           `json:"output_profile,omitempty"`
	IncludeImports  bool             `json:"include_imports,omitempty"`
	IncludeSymbols  bool             `json:"include_symbols,omitempty"`
	IncludeSections bool             `json:"include_sections,omitempty"`
	LineWindow      *SourceLineRange `json:"line_window,omitempty"`
	EnclosingLine   *int             `json:"enclosing_line,omitempty"`
	NameContains    string           `json:"name_contains,omitempty"`
	Kinds           []string         `json:"kinds,omitempty"`
	MaxItems        *int             `json:"max_items,omitempty"`
	MaxDepth        *int             `json:"max_depth,omitempty"`
}

type OutlineFileOutput struct {
	CwdOutputMeta
	Text                string           `json:"-"`
	Error               string           `json:"error,omitempty"`
	File                string           `json:"file,omitempty"`
	Language            string           `json:"language,omitempty"`
	ParserStatus        string           `json:"parser_status,omitempty"`
	ParserScope         string           `json:"parser_scope,omitempty"`
	Fingerprint         *FileFingerprint `json:"fingerprint,omitempty"`
	Imports             []OutlineItem    `json:"imports"`
	Symbols             []OutlineItem    `json:"symbols"`
	Sections            []OutlineItem    `json:"sections"`
	EnclosingItems      []OutlineItem    `json:"enclosing_items,omitempty"`
	OutlineStats        OutlineStats     `json:"outline_stats"`
	Truncated           bool             `json:"truncated"`
	Warnings            []ToolWarning    `json:"warnings"`
	NextRecommendedCall *ActionHint      `json:"next_recommended_call,omitempty"`
	ErrorCode           string           `json:"error_code,omitempty"`
}

type TargetPrecondition struct {
	Fingerprint  *FileFingerprint `json:"fingerprint,omitempty"`
	MustNotExist bool             `json:"must_not_exist,omitempty"`
}

type TargetPlacement struct {
	Mode  string           `json:"mode"`
	Line  int              `json:"line,omitempty"`
	Range *SourceLineRange `json:"range,omitempty"`
}

type BackupSpec struct {
	Mode string `json:"mode,omitempty"`
}

type TransferRangeResult struct {
	Range     SourceLineRange `json:"range"`
	LineCount int             `json:"line_count"`
	ByteCount int64           `json:"byte_count"`
}

type PartialState struct {
	Operation                  string                `json:"operation,omitempty"`
	Phase                      string                `json:"phase,omitempty"`
	SourceFile                 string                `json:"source_file,omitempty"`
	TargetFile                 string                `json:"target_file,omitempty"`
	SourceModifiedByTool       bool                  `json:"source_modified_by_tool"`
	TargetWritten              bool                  `json:"target_written"`
	FilesMaybeModified         []string              `json:"files_maybe_modified"`
	BackupPaths                []string              `json:"backup_paths"`
	SourceFingerprintBefore    *FileFingerprint      `json:"source_fingerprint_before,omitempty"`
	SourceFingerprintAfter     *FileFingerprint      `json:"source_fingerprint_after,omitempty"`
	TargetFingerprintBefore    *FileFingerprint      `json:"target_fingerprint_before,omitempty"`
	TargetFingerprintAfter     *FileFingerprint      `json:"target_fingerprint_after,omitempty"`
	CurrentSourceFingerprint   *FileFingerprint      `json:"current_source_fingerprint,omitempty"`
	CurrentTargetFingerprint   *FileFingerprint      `json:"current_target_fingerprint,omitempty"`
	ErrorCode                  string                `json:"error_code,omitempty"`
	Error                      string                `json:"error,omitempty"`
	RecommendedNextTool        string                `json:"recommended_next_tool,omitempty"`
	RecommendedNextInput       map[string]any        `json:"recommended_next_input,omitempty"`
	RecommendedNextInputPolicy string                `json:"recommended_next_input_policy,omitempty"`
	RecoveryHint               string                `json:"recovery_hint,omitempty"`
	Ranges                     []TransferRangeResult `json:"ranges,omitempty"`
}

type BackupResult struct {
	File       string `json:"file,omitempty"`
	Role       string `json:"role,omitempty"`
	Requested  bool   `json:"requested"`
	Created    bool   `json:"created"`
	BackupPath string `json:"backup_path,omitempty"`
	ErrorCode  string `json:"error_code,omitempty"`
	Error      string `json:"error,omitempty"`
}

type RangeTransferOutput struct {
	CwdOutputMeta
	Text                            string                `json:"-"`
	Error                           string                `json:"error,omitempty"`
	ErrorCode                       string                `json:"error_code,omitempty"`
	Operation                       string                `json:"operation,omitempty"`
	DryRun                          bool                  `json:"dry_run"`
	Applied                         bool                  `json:"applied"`
	SourceFile                      string                `json:"source_file,omitempty"`
	TargetFile                      string                `json:"target_file,omitempty"`
	RequestedRanges                 []SourceLineRange     `json:"requested_ranges,omitempty"`
	Ranges                          []TransferRangeResult `json:"ranges"`
	TargetPlacement                 TargetPlacement       `json:"target_placement"`
	WouldWriteBytes                 int64                 `json:"would_write_bytes,omitempty"`
	BytesWritten                    int64                 `json:"bytes_written,omitempty"`
	WouldRemoveSourceLines          int                   `json:"would_remove_source_lines,omitempty"`
	WouldRemoveSourceRanges         []SourceLineRange     `json:"would_remove_source_ranges,omitempty"`
	RemovedSourceLines              int                   `json:"removed_source_lines,omitempty"`
	RemovedSourceRanges             []SourceLineRange     `json:"removed_source_ranges,omitempty"`
	SourceFingerprintBefore         *FileFingerprint      `json:"source_fingerprint_before,omitempty"`
	SourceFingerprintCheckedAtWrite *FileFingerprint      `json:"source_fingerprint_checked_at_write,omitempty"`
	SourceFingerprintAfter          *FileFingerprint      `json:"source_fingerprint_after,omitempty"`
	TargetFingerprintBefore         *FileFingerprint      `json:"target_fingerprint_before,omitempty"`
	TargetFingerprintAfter          *FileFingerprint      `json:"target_fingerprint_after,omitempty"`
	SourceFingerprintForNextWrite   *FileFingerprint      `json:"source_fingerprint_for_next_write,omitempty"`
	TargetFingerprintForNextWrite   *FileFingerprint      `json:"target_fingerprint_for_next_write,omitempty"`
	ExpectedSourceFingerprint       *FileFingerprint      `json:"expected_source_fingerprint,omitempty"`
	CurrentSourceFingerprint        *FileFingerprint      `json:"current_source_fingerprint,omitempty"`
	ExpectedTargetFingerprint       *FileFingerprint      `json:"expected_target_fingerprint,omitempty"`
	CurrentTargetFingerprint        *FileFingerprint      `json:"current_target_fingerprint,omitempty"`
	RangeErrorFileRole              string                `json:"-"`
	BoundaryWarnings                []BoundaryWarning     `json:"boundary_warnings"`
	Warnings                        []ToolWarning         `json:"warnings"`
	BackupPaths                     []string              `json:"backup_paths"`
	BackupResults                   []BackupResult        `json:"backup_results"`
	DiffPreviews                    []DiffPreview         `json:"diff_previews"`
	JoinerEffect                    JoinerEffect          `json:"joiner_effect"`
	BoundaryPreview                 BoundaryPreview       `json:"boundary_preview"`
	Validation                      WriteValidation       `json:"validation"`
	BackupDiscovery                 *BackupDiscoveryHint  `json:"backup_discovery,omitempty"`
	PartialState                    *PartialState         `json:"partial_state,omitempty"`
	ActionHint                      *ActionHint           `json:"action_hint,omitempty"`
}

type CopyRangesInput struct {
	CwdAwareInput
	SourceFile         string             `json:"source_file"`
	SourceFingerprint  FileFingerprint    `json:"source_fingerprint"`
	Ranges             []SourceLineRange  `json:"ranges"`
	TargetFile         string             `json:"target_file"`
	TargetPrecondition TargetPrecondition `json:"target_precondition"`
	Placement          TargetPlacement    `json:"placement"`
	Joiner             string             `json:"joiner,omitempty"`
	Backup             BackupSpec         `json:"backup,omitempty"`
	RedactionMode      string             `json:"redaction_mode,omitempty"`
	DryRun             bool               `json:"dry_run,omitempty"`
}

type MoveRangesInput CopyRangesInput

func (i *MoveRangesInput) SetCwdID(cwdID CwdIDInput) {
	(*CopyRangesInput)(i).SetCwdID(cwdID)
}

type CopyRangesOutput RangeTransferOutput

type MoveRangesOutput RangeTransferOutput

type BatchRangeTarget struct {
	TargetFile         string             `json:"target_file"`
	TargetPrecondition TargetPrecondition `json:"target_precondition"`
	Placement          TargetPlacement    `json:"placement"`
	Ranges             []SourceLineRange  `json:"ranges"`
	Joiner             string             `json:"joiner,omitempty"`
	Backup             BackupSpec         `json:"backup,omitempty"`
	RedactionMode      string             `json:"redaction_mode,omitempty"`
}

type CopyRangesBatchInput struct {
	CwdAwareInput
	SourceFile        string             `json:"source_file"`
	SourceFingerprint FileFingerprint    `json:"source_fingerprint"`
	Targets           []BatchRangeTarget `json:"targets"`
	RedactionMode     string             `json:"redaction_mode,omitempty"`
	DryRun            bool               `json:"dry_run,omitempty"`
}

type MoveRangesBatchInput struct {
	CwdAwareInput
	SourceFile        string             `json:"source_file"`
	SourceFingerprint FileFingerprint    `json:"source_fingerprint"`
	Targets           []BatchRangeTarget `json:"targets"`
	SourceBackup      BackupSpec         `json:"source_backup,omitempty"`
	RedactionMode     string             `json:"redaction_mode,omitempty"`
	DryRun            bool               `json:"dry_run,omitempty"`
}

type BatchTargetResult struct {
	TargetFile                    string                `json:"target_file,omitempty"`
	Status                        string                `json:"status,omitempty"`
	Written                       bool                  `json:"written"`
	Skipped                       bool                  `json:"skipped"`
	Failed                        bool                  `json:"failed"`
	FailedAt                      string                `json:"failed_at,omitempty"`
	Error                         string                `json:"error,omitempty"`
	ErrorCode                     string                `json:"error_code,omitempty"`
	RequestedRanges               []SourceLineRange     `json:"requested_ranges,omitempty"`
	Ranges                        []TransferRangeResult `json:"ranges"`
	WouldWriteBytes               int64                 `json:"would_write_bytes,omitempty"`
	BytesWritten                  int64                 `json:"bytes_written,omitempty"`
	TargetFingerprintBefore       *FileFingerprint      `json:"target_fingerprint_before,omitempty"`
	TargetFingerprintAfter        *FileFingerprint      `json:"target_fingerprint_after,omitempty"`
	TargetFingerprintForNextWrite *FileFingerprint      `json:"target_fingerprint_for_next_write,omitempty"`
	ExpectedTargetFingerprint     *FileFingerprint      `json:"expected_target_fingerprint,omitempty"`
	CurrentTargetFingerprint      *FileFingerprint      `json:"current_target_fingerprint,omitempty"`
	BackupRequested               bool                  `json:"backup_requested"`
	BackupPaths                   []string              `json:"backup_paths"`
	BackupError                   string                `json:"backup_error,omitempty"`
	BoundaryWarnings              []BoundaryWarning     `json:"boundary_warnings"`
	Warnings                      []ToolWarning         `json:"warnings"`
	DiffPreviews                  []DiffPreview         `json:"diff_previews"`
	JoinerEffect                  JoinerEffect          `json:"joiner_effect"`
	BoundaryPreview               BoundaryPreview       `json:"boundary_preview"`
	Validation                    WriteValidation       `json:"validation"`
}

type BatchPartialState struct {
	Operation                  string              `json:"operation,omitempty"`
	Phase                      string              `json:"phase,omitempty"`
	SourceFile                 string              `json:"source_file,omitempty"`
	SourceModifiedByTool       bool                `json:"source_modified_by_tool"`
	SourceFingerprintBefore    *FileFingerprint    `json:"source_fingerprint_before,omitempty"`
	SourceFingerprintAfter     *FileFingerprint    `json:"source_fingerprint_after,omitempty"`
	CurrentSourceFingerprint   *FileFingerprint    `json:"current_source_fingerprint,omitempty"`
	TargetResults              []BatchTargetResult `json:"target_results"`
	BackupPaths                []string            `json:"backup_paths"`
	BackupResults              []BackupResult      `json:"backup_results"`
	RecommendedNextTool        string              `json:"recommended_next_tool,omitempty"`
	RecommendedNextInputPolicy string              `json:"recommended_next_input_policy,omitempty"`
	RecommendedNextInput       map[string]any      `json:"recommended_next_input,omitempty"`
	RecoveryHint               string              `json:"recovery_hint,omitempty"`
	ErrorCode                  string              `json:"error_code,omitempty"`
	Error                      string              `json:"error,omitempty"`
}

type BatchRangeTransferOutput struct {
	CwdOutputMeta
	Text                            string               `json:"-"`
	Error                           string               `json:"error,omitempty"`
	ErrorCode                       string               `json:"error_code,omitempty"`
	Operation                       string               `json:"operation,omitempty"`
	DryRun                          bool                 `json:"dry_run"`
	Applied                         bool                 `json:"applied"`
	SourceFile                      string               `json:"source_file,omitempty"`
	TargetResults                   []BatchTargetResult  `json:"target_results"`
	TargetsWritten                  []string             `json:"targets_written"`
	SourceFingerprintBefore         *FileFingerprint     `json:"source_fingerprint_before,omitempty"`
	SourceFingerprintCheckedAtWrite *FileFingerprint     `json:"source_fingerprint_checked_at_write,omitempty"`
	SourceFingerprintAfter          *FileFingerprint     `json:"source_fingerprint_after,omitempty"`
	CurrentSourceFingerprint        *FileFingerprint     `json:"current_source_fingerprint,omitempty"`
	SourceFingerprintForNextWrite   *FileFingerprint     `json:"source_fingerprint_for_next_write,omitempty"`
	WouldWriteBytes                 int64                `json:"would_write_bytes,omitempty"`
	WouldWriteTargetBytes           int64                `json:"would_write_target_bytes,omitempty"`
	WouldRewriteSourceBytes         int64                `json:"would_rewrite_source_bytes,omitempty"`
	WouldWriteTotalBytes            int64                `json:"would_write_total_bytes,omitempty"`
	BytesWritten                    int64                `json:"bytes_written,omitempty"`
	BytesWrittenTargetBytes         int64                `json:"bytes_written_target_bytes,omitempty"`
	BytesRewrittenSourceBytes       int64                `json:"bytes_rewritten_source_bytes,omitempty"`
	BytesWrittenTotalBytes          int64                `json:"bytes_written_total_bytes,omitempty"`
	WouldRemoveSourceLines          int                  `json:"would_remove_source_lines,omitempty"`
	WouldRemoveSourceRanges         []SourceLineRange    `json:"would_remove_source_ranges,omitempty"`
	RemovedSourceLines              int                  `json:"removed_source_lines,omitempty"`
	RemovedSourceRanges             []SourceLineRange    `json:"removed_source_ranges,omitempty"`
	BatchWarnings                   []ToolWarning        `json:"batch_warnings"`
	Warnings                        []ToolWarning        `json:"warnings"`
	WarningsTruncated               bool                 `json:"warnings_truncated"`
	WarningSummary                  *WarningSummary      `json:"warning_summary,omitempty"`
	OmittedWarningCounts            map[string]int       `json:"-"`
	BackupPaths                     []string             `json:"backup_paths"`
	BackupResults                   []BackupResult       `json:"backup_results"`
	SourceDiffPreviews              []DiffPreview        `json:"source_diff_previews,omitempty"`
	SourceValidation                *WriteValidation     `json:"source_validation,omitempty"`
	BackupDiscovery                 *BackupDiscoveryHint `json:"backup_discovery,omitempty"`
	PartialState                    *BatchPartialState   `json:"partial_state,omitempty"`
	ActionHint                      *ActionHint          `json:"action_hint,omitempty"`
}

type CopyRangesBatchOutput BatchRangeTransferOutput

type MoveRangesBatchOutput BatchRangeTransferOutput
