package api

// ErrorCode identifies one stable machine-readable navigation error.
type ErrorCode string

const (
	ErrorInvalidInput        ErrorCode = "invalid_input"
	ErrorCWDUnknown          ErrorCode = "cwd_unknown"
	ErrorPathOutsideCWD      ErrorCode = "path_outside_cwd"
	ErrorNotFound            ErrorCode = "not_found"
	ErrorBinary              ErrorCode = "binary"
	ErrorUnsupportedEncoding ErrorCode = "unsupported_encoding"
	ErrorUnsupportedLanguage ErrorCode = "unsupported_language"
	ErrorRecordExceedsBudget ErrorCode = "record_exceeds_budget"
	ErrorCursorExpired       ErrorCode = "cursor_expired"
	ErrorCursorWrongTool     ErrorCode = "cursor_wrong_tool"
	ErrorCursorWrongCWD      ErrorCode = "cursor_wrong_cwd"
	ErrorBudgetExceeded      ErrorCode = "budget_exceeded"
	ErrorPermissionDenied    ErrorCode = "permission_denied"
	ErrorIOError             ErrorCode = "io_error"
	ErrorParserFailed        ErrorCode = "parser_failed"
)

// OrderedErrorCodes returns the canonical error-code order.
func OrderedErrorCodes() [15]ErrorCode {
	return [15]ErrorCode{
		ErrorInvalidInput,
		ErrorCWDUnknown,
		ErrorPathOutsideCWD,
		ErrorNotFound,
		ErrorBinary,
		ErrorUnsupportedEncoding,
		ErrorUnsupportedLanguage,
		ErrorRecordExceedsBudget,
		ErrorCursorExpired,
		ErrorCursorWrongTool,
		ErrorCursorWrongCWD,
		ErrorBudgetExceeded,
		ErrorPermissionDenied,
		ErrorIOError,
		ErrorParserFailed,
	}
}

// Valid reports whether code belongs to the closed error vocabulary.
func (code ErrorCode) Valid() bool {
	switch code {
	case ErrorInvalidInput,
		ErrorCWDUnknown,
		ErrorPathOutsideCWD,
		ErrorNotFound,
		ErrorBinary,
		ErrorUnsupportedEncoding,
		ErrorUnsupportedLanguage,
		ErrorRecordExceedsBudget,
		ErrorCursorExpired,
		ErrorCursorWrongTool,
		ErrorCursorWrongCWD,
		ErrorBudgetExceeded,
		ErrorPermissionDenied,
		ErrorIOError,
		ErrorParserFailed:
		return true
	default:
		return false
	}
}

// WarningCode identifies one stable machine-readable navigation warning.
type WarningCode string

const (
	WarningBinarySkipped              WarningCode = "binary_skipped"
	WarningParserPartial              WarningCode = "parser_partial"
	WarningParserSkipped              WarningCode = "parser_skipped"
	WarningPathEncodingUnsupported    WarningCode = "path_encoding_unsupported"
	WarningSpecialFileSkipped         WarningCode = "special_file_skipped"
	WarningUnreadableSkipped          WarningCode = "unreadable_skipped"
	WarningUnsupportedEncodingSkipped WarningCode = "unsupported_encoding_skipped"
	WarningSourceChangedSkipped       WarningCode = "source_changed_skipped"
	WarningSymlinkSkipped             WarningCode = "symlink_skipped"
	WarningMountSkipped               WarningCode = "mount_skipped"
	WarningUnaddressablePathSkipped   WarningCode = "unaddressable_path_skipped"
)

// OrderedWarningCodes returns the canonical warning-code order.
func OrderedWarningCodes() [11]WarningCode {
	return [11]WarningCode{
		WarningBinarySkipped,
		WarningParserPartial,
		WarningParserSkipped,
		WarningPathEncodingUnsupported,
		WarningSpecialFileSkipped,
		WarningUnreadableSkipped,
		WarningUnsupportedEncodingSkipped,
		WarningSourceChangedSkipped,
		WarningSymlinkSkipped,
		WarningMountSkipped,
		WarningUnaddressablePathSkipped,
	}
}

// Valid reports whether code belongs to the closed warning vocabulary.
func (code WarningCode) Valid() bool {
	switch code {
	case WarningBinarySkipped,
		WarningParserPartial,
		WarningParserSkipped,
		WarningPathEncodingUnsupported,
		WarningSpecialFileSkipped,
		WarningUnreadableSkipped,
		WarningUnsupportedEncodingSkipped,
		WarningSourceChangedSkipped,
		WarningSymlinkSkipped,
		WarningMountSkipped,
		WarningUnaddressablePathSkipped:
		return true
	default:
		return false
	}
}

// Language identifies one parser-supported public language name.
type Language string

const (
	LanguageMarkdown   Language = "markdown"
	LanguageGo         Language = "go"
	LanguageJavaScript Language = "javascript"
	LanguageJSX        Language = "jsx"
	LanguageTypeScript Language = "typescript"
	LanguageTSX        Language = "tsx"
	LanguagePython     Language = "python"
	LanguageJava       Language = "java"
	LanguageRust       Language = "rust"
	LanguageC          Language = "c"
	LanguageCPP        Language = "cpp"
	LanguageCSharp     Language = "csharp"
	LanguageRuby       Language = "ruby"
	LanguageKotlin     Language = "kotlin"
	LanguageSwift      Language = "swift"
	LanguageBash       Language = "bash"
	LanguageJSON       Language = "json"
	LanguageYAML       Language = "yaml"
	LanguageSvelte     Language = "svelte"
)

// OrderedLanguages returns the canonical language order.
func OrderedLanguages() [19]Language {
	return [19]Language{
		LanguageMarkdown,
		LanguageGo,
		LanguageJavaScript,
		LanguageJSX,
		LanguageTypeScript,
		LanguageTSX,
		LanguagePython,
		LanguageJava,
		LanguageRust,
		LanguageC,
		LanguageCPP,
		LanguageCSharp,
		LanguageRuby,
		LanguageKotlin,
		LanguageSwift,
		LanguageBash,
		LanguageJSON,
		LanguageYAML,
		LanguageSvelte,
	}
}

// Valid reports whether language belongs to the closed language vocabulary.
func (language Language) Valid() bool {
	switch language {
	case LanguageMarkdown,
		LanguageGo,
		LanguageJavaScript,
		LanguageJSX,
		LanguageTypeScript,
		LanguageTSX,
		LanguagePython,
		LanguageJava,
		LanguageRust,
		LanguageC,
		LanguageCPP,
		LanguageCSharp,
		LanguageRuby,
		LanguageKotlin,
		LanguageSwift,
		LanguageBash,
		LanguageJSON,
		LanguageYAML,
		LanguageSvelte:
		return true
	default:
		return false
	}
}

// Kind identifies one public outline/search symbol category.
type Kind string

const (
	KindPackage     Kind = "package"
	KindModule      Kind = "module"
	KindNamespace   Kind = "namespace"
	KindClass       Kind = "class"
	KindInterface   Kind = "interface"
	KindStruct      Kind = "struct"
	KindEnum        Kind = "enum"
	KindTrait       Kind = "trait"
	KindType        Kind = "type"
	KindConstant    Kind = "constant"
	KindVariable    Kind = "variable"
	KindField       Kind = "field"
	KindProperty    Kind = "property"
	KindFunction    Kind = "function"
	KindMethod      Kind = "method"
	KindConstructor Kind = "constructor"
	KindObject      Kind = "object"
	KindComponent   Kind = "component"
	KindSection     Kind = "section"
	KindOther       Kind = "other"
)

// OrderedKinds returns the canonical public kind order.
func OrderedKinds() [20]Kind {
	return [20]Kind{
		KindPackage,
		KindModule,
		KindNamespace,
		KindClass,
		KindInterface,
		KindStruct,
		KindEnum,
		KindTrait,
		KindType,
		KindConstant,
		KindVariable,
		KindField,
		KindProperty,
		KindFunction,
		KindMethod,
		KindConstructor,
		KindObject,
		KindComponent,
		KindSection,
		KindOther,
	}
}

// Valid reports whether kind belongs to the closed public kind vocabulary.
func (kind Kind) Valid() bool {
	switch kind {
	case KindPackage,
		KindModule,
		KindNamespace,
		KindClass,
		KindInterface,
		KindStruct,
		KindEnum,
		KindTrait,
		KindType,
		KindConstant,
		KindVariable,
		KindField,
		KindProperty,
		KindFunction,
		KindMethod,
		KindConstructor,
		KindObject,
		KindComponent,
		KindSection,
		KindOther:
		return true
	default:
		return false
	}
}
