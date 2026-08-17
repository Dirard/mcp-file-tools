package present

import "github.com/Dirard/mcp-file-tools/internal/api"

type Status uint8

const (
	Complete Status = iota + 1
	Partial
)

type Cursor string

type Page struct {
	Result   api.Result
	Rows     uint64
	Matches  uint64
	Items    uint64
	Complete bool
}

func Error(code api.ErrorCode) api.Result {
	if !code.Valid() {
		code = api.ErrorInvalidInput
	}
	if code == api.ErrorInvalidInput {
		return InputError("arguments", "does_not_match_tool_contract")
	}
	message, hint := errorGuidance(code)
	return api.Navigation("ERROR\t"+string(code)+"\tmessage="+message+"\thint="+hint+"\n", true)
}

func InputError(field, reason string) api.Result {
	if field == "" || reason == "" {
		return Error(api.ErrorInvalidInput)
	}
	return api.Navigation("ERROR\tinvalid_input\tfield="+field+"\treason="+reason+"\tmessage=tool_arguments_are_invalid\thint=fix_the_named_field_and_retry\n", true)
}

func errorGuidance(code api.ErrorCode) (string, string) {
	switch code {
	case api.ErrorCWDUnknown:
		return "cwd_id_is_unknown", "call_set_cwd_and_retry_with_returned_cwd_id"
	case api.ErrorPathOutsideCWD:
		return "path_escapes_registered_root", "use_a_relative_path_inside_registered_cwd"
	case api.ErrorNotFound:
		return "path_was_not_found", "check_the_relative_path_and_registered_cwd"
	case api.ErrorBinary:
		return "file_is_binary", "choose_a_text_file_or_use_another_tool"
	case api.ErrorUnsupportedEncoding:
		return "file_encoding_is_not_supported", "convert_file_to_utf_8"
	case api.ErrorUnsupportedLanguage:
		return "outline_language_is_not_supported", "use_source_view_instead"
	case api.ErrorLineTooLong:
		return "line_exceeds_supported_size", "choose_another_file_or_reduce_generated_line_size"
	case api.ErrorRecordExceedsBudget:
		return "record_exceeds_page_budget", "increase_max_bytes_or_narrow_request"
	case api.ErrorCursorExpired:
		return "cursor_expired", "repeat_the_initial_request"
	case api.ErrorCursorWrongTool:
		return "cursor_belongs_to_another_tool", "continue_with_the_tool_that_created_cursor"
	case api.ErrorCursorWrongCWD:
		return "cursor_belongs_to_another_cwd", "continue_with_the_original_cwd_id"
	case api.ErrorBudgetExceeded:
		return "operation_exceeded_server_budget", "narrow_request_or_reduce_limit"
	case api.ErrorPermissionDenied:
		return "filesystem_permission_denied", "choose_a_readable_path_or_adjust_permissions"
	case api.ErrorIOError:
		return "filesystem_operation_failed", "retry_once_then_check_file_state"
	case api.ErrorParserFailed:
		return "source_parser_failed", "use_source_view_or_narrow_file"
	default:
		return "tool_arguments_are_invalid", "fix_the_named_field_and_retry"
	}
}

func validStatusCursor(status Status, cursor Cursor) bool {
	switch status {
	case Complete:
		return cursor == ""
	case Partial:
		return validCursor(cursor)
	default:
		return false
	}
}

func validCursor(cursor Cursor) bool {
	if len(cursor) != 22 {
		return false
	}
	for index := range cursor {
		character := cursor[index]
		if (character >= 'A' && character <= 'Z') ||
			(character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}
