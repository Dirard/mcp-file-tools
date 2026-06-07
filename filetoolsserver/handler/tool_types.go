package handler

import "encoding/json"

type LineRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type ReadFileOutput struct {
	CwdOutputMeta
	Text            string            `json:"text"`
	Error           string            `json:"error,omitempty"`
	ErrorCode       string            `json:"error_code,omitempty"`
	File            string            `json:"file,omitempty"`
	TotalLines      *int              `json:"total_lines,omitempty"`
	TotalLinesKnown bool              `json:"total_lines_known"`
	RequestedRange  *LineRange        `json:"requested_range,omitempty"`
	Range           *LineRange        `json:"range,omitempty"`
	Coverage        *ReadCoverage     `json:"coverage,omitempty"`
	Continuation    *ContinuationHint `json:"continuation,omitempty"`
	Fingerprint     *FileFingerprint  `json:"fingerprint,omitempty"`
}

type ListDirEntry struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type ListDirOutput struct {
	CwdOutputMeta
	Text                  string         `json:"-"`
	Error                 string         `json:"error,omitempty"`
	ErrorCode             string         `json:"error_code,omitempty"`
	Directory             string         `json:"directory,omitempty"`
	Count                 int            `json:"count"`
	IncludeHidden         bool           `json:"include_hidden"`
	IncludeVCSMetadata    bool           `json:"include_vcs_metadata"`
	DotEntriesSkipped     bool           `json:"dot_entries_skipped"`
	HiddenEntriesIncluded int            `json:"hidden_entries_included,omitempty"`
	VCSEntriesSkipped     int            `json:"vcs_entries_skipped,omitempty"`
	VCSEntriesIncluded    int            `json:"vcs_entries_included,omitempty"`
	Entries               []ListDirEntry `json:"entries"`
	Message               string         `json:"message,omitempty"`
}

type GlobFileMatch struct {
	Path             string `json:"path"`
	ModifiedAt       string `json:"modified_at,omitempty"`
	ModifiedUnixNano int64  `json:"modified_unix_nano,omitempty"`
	SizeBytes        *int64 `json:"size_bytes,omitempty"`
}

type GlobDirectoryGroup struct {
	Directory string          `json:"directory"`
	Count     int             `json:"count"`
	Files     []GlobFileMatch `json:"files"`
}

type GlobFileSearchOutput struct {
	CwdOutputMeta
	Text                  string               `json:"-"`
	Error                 string               `json:"error,omitempty"`
	ErrorCode             string               `json:"error_code,omitempty"`
	Pattern               string               `json:"pattern,omitempty"`
	TargetDirectory       string               `json:"target_directory,omitempty"`
	Sort                  string               `json:"sort,omitempty"`
	IncludeHidden         bool                 `json:"include_hidden"`
	IncludeVCSMetadata    bool                 `json:"include_vcs_metadata"`
	Limit                 int                  `json:"limit"`
	Count                 int                  `json:"count"`
	TotalMatchCount       int                  `json:"total_match_count"`
	Truncated             bool                 `json:"truncated"`
	DotEntriesSkipped     bool                 `json:"dot_entries_skipped"`
	HiddenEntriesIncluded int                  `json:"hidden_entries_included,omitempty"`
	VCSEntriesSkipped     int                  `json:"vcs_entries_skipped,omitempty"`
	VCSEntriesIncluded    int                  `json:"vcs_entries_included,omitempty"`
	Files                 []GlobFileMatch      `json:"files"`
	Groups                []GlobDirectoryGroup `json:"groups,omitempty"`
	SearchStats           *GrepSearchStats     `json:"search_stats,omitempty"`
	Continuation          *ContinuationHint    `json:"continuation,omitempty"`
	NextRecommendedCall   *ActionHint          `json:"next_recommended_call,omitempty"`
	NextRecommendedCalls  []ActionHint         `json:"next_recommended_calls,omitempty"`
	Message               string               `json:"message,omitempty"`
}

type GrepMatch struct {
	Path          string `json:"path"`
	Line          int    `json:"line"`
	Kind          string `json:"kind"`
	Text          string `json:"text"`
	Redacted      bool   `json:"redacted"`
	RedactionMode string `json:"redaction_mode,omitempty"`
}

type GrepCount struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

type GrepSearchStats struct {
	FilesSeen         int    `json:"files_seen"`
	FilesSearched     int    `json:"files_searched"`
	FilesWithMatches  int    `json:"files_with_matches"`
	SkippedHidden     int    `json:"skipped_hidden"`
	SkippedIgnored    int    `json:"skipped_ignored"`
	SkippedVCS        int    `json:"skipped_vcs"`
	SkippedBinary     int    `json:"skipped_binary"`
	SkippedUnreadable int    `json:"skipped_unreadable"`
	SkippedTypeOrGlob int    `json:"skipped_type_or_glob"`
	FilesCapped       int    `json:"files_capped"`
	Completed         bool   `json:"completed"`
	StopReason        string `json:"stop_reason,omitempty"`
	CountsAreComplete bool   `json:"counts_are_complete"`
}

type GrepFileGroup struct {
	Path       string            `json:"path"`
	MatchCount int               `json:"match_count"`
	RowCount   int               `json:"row_count"`
	FirstLine  int               `json:"first_line,omitempty"`
	LastLine   int               `json:"last_line,omitempty"`
	ReadRanges []SourceLineRange `json:"read_ranges"`
	Capped     bool              `json:"capped,omitempty"`
}

type GrepOutput struct {
	CwdOutputMeta
	Text                 string           `json:"-"`
	Error                string           `json:"error,omitempty"`
	ErrorCode            string           `json:"error_code,omitempty"`
	Pattern              string           `json:"pattern,omitempty"`
	PatternMode          string           `json:"pattern_mode,omitempty"`
	Path                 string           `json:"path,omitempty"`
	OutputMode           string           `json:"output_mode,omitempty"`
	IncludeHidden        bool             `json:"include_hidden"`
	RedactionMode        string           `json:"redaction_mode,omitempty"`
	ContextBefore        int              `json:"context_before,omitempty"`
	ContextAfter         int              `json:"context_after,omitempty"`
	CaseInsensitive      bool             `json:"case_insensitive,omitempty"`
	Multiline            bool             `json:"multiline,omitempty"`
	LineWindow           *SourceLineRange `json:"line_window,omitempty"`
	Limit                int              `json:"limit"`
	MatchCount           int              `json:"match_count"`
	RowCount             int              `json:"row_count"`
	Truncated            bool             `json:"truncated"`
	DotEntriesSkipped    bool             `json:"dot_entries_skipped"`
	Matches              []GrepMatch      `json:"matches"`
	Files                []string         `json:"files"`
	Counts               []GrepCount      `json:"counts"`
	SearchStats          *GrepSearchStats `json:"search_stats,omitempty"`
	FileGroups           []GrepFileGroup  `json:"file_groups"`
	NextRecommendedCall  *ActionHint      `json:"next_recommended_call,omitempty"`
	NextRecommendedCalls []ActionHint     `json:"next_recommended_calls,omitempty"`
	Message              string           `json:"message,omitempty"`
}

type InspectPathOutput struct {
	CwdOutputMeta
	Text                    string          `json:"-"`
	Error                   string          `json:"error,omitempty"`
	ErrorCode               string          `json:"error_code,omitempty"`
	Path                    string          `json:"path,omitempty"`
	ResolvedPath            string          `json:"resolved_path,omitempty"`
	Name                    string          `json:"name,omitempty"`
	Extension               string          `json:"extension,omitempty"`
	Exists                  bool            `json:"exists"`
	Kind                    string          `json:"kind,omitempty"`
	SizeBytes               *int64          `json:"size_bytes,omitempty"`
	LineCount               *int            `json:"line_count,omitempty"`
	ModifiedAt              string          `json:"modified_at,omitempty"`
	ModifiedUnixNano        int64           `json:"modified_unix_nano,omitempty"`
	Mode                    string          `json:"mode,omitempty"`
	Permissions             string          `json:"permissions,omitempty"`
	IsHidden                bool            `json:"is_hidden"`
	IsReadable              bool            `json:"is_readable"`
	IsBinary                *bool           `json:"is_binary,omitempty"`
	Encoding                string          `json:"encoding,omitempty"`
	DetectedEncoding        string          `json:"detected_encoding,omitempty"`
	EncodingConfidence      int             `json:"encoding_confidence,omitempty"`
	SymlinkTarget           string          `json:"symlink_target,omitempty"`
	SymlinkTargetKind       string          `json:"symlink_target_kind,omitempty"`
	SymlinkTargetOutsideCwd bool            `json:"symlink_target_outside_cwd,omitempty"`
	BrokenSymlink           bool            `json:"broken_symlink,omitempty"`
	DirectFileCount         *int            `json:"direct_file_count,omitempty"`
	DirectDirCount          *int            `json:"direct_dir_count,omitempty"`
	MimeHint                string          `json:"mime_hint,omitempty"`
	BinaryPreviewAvailable  bool            `json:"binary_preview_available"`
	Visibility              *PathVisibility `json:"visibility,omitempty"`
}

type InspectPathDiscoveryContext struct {
	TargetDirectory    string   `json:"target_directory,omitempty"`
	GlobPattern        string   `json:"glob_pattern,omitempty"`
	GrepGlob           string   `json:"grep_glob,omitempty"`
	Type               string   `json:"type,omitempty"`
	IgnoreGlobs        []string `json:"ignore_globs,omitempty"`
	IncludeHidden      bool     `json:"include_hidden,omitempty"`
	IncludeVCSMetadata bool     `json:"include_vcs_metadata,omitempty"`
}

type PathVisibility struct {
	TargetPath        string             `json:"target_path"`
	Exists            bool               `json:"exists"`
	WouldListDirShow  bool               `json:"would_list_dir_show"`
	WouldGlobMatch    bool               `json:"would_glob_match"`
	WouldGrepTraverse bool               `json:"would_grep_traverse"`
	Reasons           []VisibilityReason `json:"reasons"`
}

type VisibilityReason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type WorkspaceDirectoryNode struct {
	Name            string                   `json:"name"`
	Path            string                   `json:"path"`
	Depth           int                      `json:"depth"`
	DirectFileCount int                      `json:"direct_file_count"`
	DirectDirCount  int                      `json:"direct_dir_count"`
	Truncated       bool                     `json:"truncated"`
	ReadError       string                   `json:"read_error,omitempty"`
	Directories     []WorkspaceDirectoryNode `json:"directories"`
}

type WorkspaceInventoryOutput struct {
	CwdOutputMeta
	Text                  string                        `json:"-"`
	Error                 string                        `json:"error,omitempty"`
	ErrorCode             string                        `json:"error_code,omitempty"`
	Root                  *WorkspaceDirectoryNode       `json:"root,omitempty"`
	DirectoriesPage       []WorkspaceDirectoryPageEntry `json:"directories_page"`
	Summary               *WorkspaceSummary             `json:"summary,omitempty"`
	Continuation          *ContinuationHint             `json:"continuation,omitempty"`
	MaxDepth              int                           `json:"max_depth"`
	Limit                 int                           `json:"limit"`
	DirectoryCount        int                           `json:"directory_count"`
	IgnoredDirectoryCount int                           `json:"ignored_directory_count"`
	IncludeHidden         bool                          `json:"include_hidden"`
	IncludeVCSMetadata    bool                          `json:"include_vcs_metadata"`
	DotEntriesSkipped     bool                          `json:"dot_entries_skipped"`
	HiddenEntriesIncluded int                           `json:"hidden_entries_included,omitempty"`
	VCSEntriesSkipped     int                           `json:"vcs_entries_skipped,omitempty"`
	VCSEntriesIncluded    int                           `json:"vcs_entries_included,omitempty"`
	Truncated             bool                          `json:"truncated"`
	TruncationReason      string                        `json:"truncation_reason,omitempty"`
	MaxDepthReached       bool                          `json:"max_depth_reached"`
	NextRecommendedCall   *ActionHint                   `json:"next_recommended_call,omitempty"`
	NextRecommendedCalls  []ActionHint                  `json:"next_recommended_calls,omitempty"`
}

type WorkspaceSummary struct {
	Complete                   bool                          `json:"complete"`
	SummaryCoverageComplete    bool                          `json:"summary_coverage_complete"`
	TreeScanComplete           bool                          `json:"tree_scan_complete"`
	SummaryIncompleteReason    string                        `json:"summary_incomplete_reason,omitempty"`
	ScanScope                  string                        `json:"scan_scope"`
	Profile                    string                        `json:"profile"`
	FileTypeCounts             map[string]int                `json:"file_type_counts,omitempty"`
	PackageHints               []string                      `json:"package_hints,omitempty"`
	SourceDirHints             []string                      `json:"source_dir_hints,omitempty"`
	TestDirHints               []string                      `json:"test_dir_hints,omitempty"`
	LargestDirectories         []WorkspaceDirectoryPageEntry `json:"largest_directories,omitempty"`
	BackupCandidateDirectories []BackupCandidateDirectory    `json:"backup_candidate_directories,omitempty"`
	BackupDiscoveryHints       []ActionHint                  `json:"backup_discovery_hints,omitempty"`
	HiddenEntriesSkipped       int                           `json:"hidden_entries_skipped,omitempty"`
	IgnoredEntriesSkipped      int                           `json:"ignored_entries_skipped,omitempty"`
}

type BackupCandidateDirectory struct {
	Path                  string `json:"path"`
	CandidateFileCount    int    `json:"candidate_file_count"`
	HiddenEvidenceSkipped bool   `json:"hidden_evidence_skipped,omitempty"`
}

type ReadFileInput struct {
	CwdAwareInput
	TargetFile      string             `json:"target_file"`
	StartLine       *int               `json:"start_line,omitempty"`
	EndLine         *int               `json:"end_line,omitempty"`
	CountTotalLines bool               `json:"count_total_lines,omitempty"`
	ChunkLines      *int               `json:"chunk_lines,omitempty"`
	ExpectedVersion *ReadCoverageProof `json:"expected_version,omitempty"`
}

type ReadFileInputItem struct {
	TargetFile      string             `json:"target_file"`
	StartLine       *int               `json:"start_line,omitempty"`
	EndLine         *int               `json:"end_line,omitempty"`
	ChunkLines      *int               `json:"chunk_lines,omitempty"`
	ExpectedVersion *ReadCoverageProof `json:"expected_version,omitempty"`
}

type ReadFilesInput struct {
	CwdAwareInput
	Items           []ReadFileInputItem `json:"items"`
	MaxTotalLines   *int                `json:"max_total_lines,omitempty"`
	MaxTotalBytes   *int                `json:"max_total_bytes,omitempty"`
	CountTotalLines bool                `json:"count_total_lines,omitempty"`
	RedactionMode   string              `json:"redaction_mode,omitempty"`
}

type ReadFilesItemOutput struct {
	Status          string            `json:"status"`
	File            string            `json:"file,omitempty"`
	Text            string            `json:"text"`
	Range           *LineRange        `json:"range,omitempty"`
	RequestedRange  *LineRange        `json:"requested_range,omitempty"`
	TotalLines      *int              `json:"total_lines,omitempty"`
	TotalLinesKnown bool              `json:"total_lines_known"`
	Truncated       bool              `json:"truncated"`
	Coverage        *ReadCoverage     `json:"coverage,omitempty"`
	Continuation    *ContinuationHint `json:"continuation,omitempty"`
	Fingerprint     *FileFingerprint  `json:"fingerprint,omitempty"`
	Error           string            `json:"error,omitempty"`
	ErrorCode       string            `json:"error_code,omitempty"`
	Redacted        bool              `json:"redacted"`
	RedactionMode   string            `json:"redaction_mode,omitempty"`
}

type ReadFilesOutput struct {
	CwdOutputMeta
	Error         string                `json:"error,omitempty"`
	ErrorCode     string                `json:"error_code,omitempty"`
	Items         []ReadFilesItemOutput `json:"items"`
	MaxTotalLines int                   `json:"max_total_lines"`
	MaxTotalBytes int                   `json:"max_total_bytes"`
	Count         int                   `json:"count"`
	Truncated     bool                  `json:"truncated"`
	Continuation  *ContinuationHint     `json:"continuation,omitempty"`
	RedactionMode string                `json:"redaction_mode,omitempty"`
}

type ListDirInput struct {
	CwdAwareInput
	TargetDirectory    string   `json:"target_directory"`
	IgnoreGlobs        []string `json:"ignore_globs,omitempty"`
	IncludeHidden      bool     `json:"include_hidden,omitempty"`
	IncludeVCSMetadata bool     `json:"include_vcs_metadata,omitempty"`
}

type GlobFileSearchInput struct {
	CwdAwareInput
	GlobPattern        string                      `json:"glob_pattern"`
	TargetDirectory    string                      `json:"target_directory"`
	IgnoreGlobs        []string                    `json:"ignore_globs,omitempty"`
	IncludeHidden      bool                        `json:"include_hidden,omitempty"`
	IncludeVCSMetadata bool                        `json:"include_vcs_metadata,omitempty"`
	Sort               string                      `json:"sort,omitempty"`
	ContinuationAfter  *DiscoveryContinuationAfter `json:"continuation_after,omitempty"`
	Limit              *int                        `json:"limit,omitempty"`
}

type GrepToolInput struct {
	CwdAwareInput
	Pattern           string           `json:"pattern"`
	PatternMode       string           `json:"pattern_mode,omitempty"`
	Path              string           `json:"path"`
	OutputMode        string           `json:"output_mode,omitempty"`
	Before            int              `json:"before,omitempty"`
	After             int              `json:"after,omitempty"`
	Context           int              `json:"context,omitempty"`
	CaseInsensitive   bool             `json:"case_insensitive,omitempty"`
	Type              string           `json:"type,omitempty"`
	Glob              string           `json:"glob,omitempty"`
	IgnoreGlobs       []string         `json:"ignore_globs,omitempty"`
	IncludeHidden     bool             `json:"include_hidden,omitempty"`
	RedactionMode     string           `json:"redaction_mode,omitempty"`
	Multiline         bool             `json:"multiline,omitempty"`
	LineWindow        *SourceLineRange `json:"line_window,omitempty"`
	MaxMatchesPerFile *int             `json:"max_matches_per_file,omitempty"`
	Limit             *int             `json:"limit,omitempty"`
	invalid           string           `json:"-"`
}

type InspectPathInput struct {
	CwdAwareInput
	TargetPath       string                       `json:"target_path"`
	DiscoveryContext *InspectPathDiscoveryContext `json:"discovery_context,omitempty"`
}

type WorkspaceInventoryInput struct {
	CwdAwareInput
	TargetDirectory    string                      `json:"target_directory"`
	MaxDepth           *int                        `json:"max_depth,omitempty"`
	Limit              *int                        `json:"limit,omitempty"`
	IgnoreGlobs        []string                    `json:"ignore_globs,omitempty"`
	IncludeHidden      bool                        `json:"include_hidden,omitempty"`
	IncludeVCSMetadata bool                        `json:"include_vcs_metadata,omitempty"`
	IncludeSummary     *bool                       `json:"include_summary,omitempty"`
	SummaryProfile     string                      `json:"summary_profile,omitempty"`
	ContinuationAfter  *DiscoveryContinuationAfter `json:"continuation_after,omitempty"`
}

func (g *GrepToolInput) UnmarshalJSON(data []byte) error {
	var aux struct {
		Pattern           string           `json:"pattern"`
		PatternMode       string           `json:"pattern_mode,omitempty"`
		Path              string           `json:"path"`
		OutputMode        string           `json:"output_mode,omitempty"`
		Before            *int             `json:"before,omitempty"`
		After             *int             `json:"after,omitempty"`
		Context           *int             `json:"context,omitempty"`
		CaseInsensitive   *bool            `json:"case_insensitive,omitempty"`
		Type              string           `json:"type,omitempty"`
		Glob              string           `json:"glob,omitempty"`
		IgnoreGlobs       []string         `json:"ignore_globs,omitempty"`
		IncludeHidden     bool             `json:"include_hidden,omitempty"`
		RedactionMode     string           `json:"redaction_mode,omitempty"`
		Multiline         bool             `json:"multiline,omitempty"`
		LineWindow        *SourceLineRange `json:"line_window,omitempty"`
		MaxMatchesPerFile *int             `json:"max_matches_per_file,omitempty"`
		Limit             *int             `json:"limit,omitempty"`
		BDash             *int             `json:"-B,omitempty"`
		ADash             *int             `json:"-A,omitempty"`
		CDash             *int             `json:"-C,omitempty"`
		IDash             *bool            `json:"-i,omitempty"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err == nil {
		if _, ok := raw["include_vcs_metadata"]; ok {
			g.invalid = "vcs_content_traversal_unsupported: grep does not support include_vcs_metadata; VCS content traversal is intentionally unsupported"
			return nil
		}
	}
	*g = GrepToolInput{
		Pattern:           aux.Pattern,
		PatternMode:       aux.PatternMode,
		Path:              aux.Path,
		OutputMode:        aux.OutputMode,
		Type:              aux.Type,
		Glob:              aux.Glob,
		IgnoreGlobs:       aux.IgnoreGlobs,
		IncludeHidden:     aux.IncludeHidden,
		RedactionMode:     aux.RedactionMode,
		Multiline:         aux.Multiline,
		LineWindow:        aux.LineWindow,
		MaxMatchesPerFile: aux.MaxMatchesPerFile,
		Limit:             aux.Limit,
	}
	if aux.Before != nil {
		g.Before = *aux.Before
	} else if aux.BDash != nil {
		g.Before = *aux.BDash
	}
	if aux.After != nil {
		g.After = *aux.After
	} else if aux.ADash != nil {
		g.After = *aux.ADash
	}
	if aux.Context != nil {
		g.Context = *aux.Context
	} else if aux.CDash != nil {
		g.Context = *aux.CDash
	}
	if aux.CaseInsensitive != nil {
		g.CaseInsensitive = *aux.CaseInsensitive
	} else if aux.IDash != nil {
		g.CaseInsensitive = *aux.IDash
	}
	return nil
}

type textRow struct {
	Prefix          string
	Body            string
	Path            string
	Line            int
	Kind            string
	Text            string
	Count           int
	MatchCountDelta int
	UseMatchDelta   bool
}
