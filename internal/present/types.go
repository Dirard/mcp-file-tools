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
	return api.Navigation("ERROR\t"+string(code)+"\n", true)
}

func InputError(field, reason string) api.Result {
	if field == "" || reason == "" {
		return Error(api.ErrorInvalidInput)
	}
	return api.Navigation("ERROR\tinvalid_input\tfield="+field+"\treason="+reason+"\n", true)
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
