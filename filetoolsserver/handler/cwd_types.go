package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const maxCwdID = int64(9007199254740991)

var integerJSONNumberPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

type CwdIDInput struct {
	Present     bool
	Value       int64
	PathContext *PathContext
}

type CwdAwareInput struct {
	CwdID CwdIDInput `json:"-"`
}

func (i *CwdAwareInput) SetCwdID(cwdID CwdIDInput) {
	i.CwdID = cwdID
}

type CwdIDSetter interface {
	SetCwdID(CwdIDInput)
}

type CwdError struct {
	Code      string
	Message   string
	CwdID     *int64
	Cwd       string
	Hint      *ActionHint
	Retryable bool
}

type CwdOutputMeta struct {
	CwdID      *int64      `json:"cwd_id,omitempty"`
	Cwd        string      `json:"cwd,omitempty"`
	ErrorCode  string      `json:"error_code,omitempty"`
	ActionHint *ActionHint `json:"action_hint,omitempty"`
}

type SetCwdInput struct {
	Directory string `json:"directory"`
	invalid   string `json:"-"`
}

type SetCwdOutput struct {
	CwdID      int64       `json:"cwd_id,omitempty"`
	Error      string      `json:"error,omitempty"`
	ErrorCode  string      `json:"error_code,omitempty"`
	ActionHint *ActionHint `json:"action_hint,omitempty"`
}

func (s *SetCwdInput) UnmarshalJSON(data []byte) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	for name := range object {
		if name != "directory" {
			s.invalid = "set_cwd accepts only the directory input field"
			return nil
		}
	}
	rawDirectory, ok := object["directory"]
	if !ok {
		return nil
	}
	var directory string
	if err := json.Unmarshal(rawDirectory, &directory); err != nil {
		s.invalid = "directory must be a string"
		return nil
	}
	s.Directory = directory
	return nil
}

func DecodeCwdIDFromRaw(raw json.RawMessage) (CwdIDInput, *CwdError) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return CwdIDInput{}, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return CwdIDInput{}, nil
	}
	value, ok := object["cwd_id"]
	if !ok {
		return CwdIDInput{}, nil
	}
	cwdID, err := parseCwdIDJSON(value)
	if err != nil {
		return CwdIDInput{}, invalidCwdIDError(nil)
	}
	return CwdIDInput{Present: true, Value: cwdID}, nil
}

func parseCwdIDJSON(raw json.RawMessage) (int64, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return 0, fmt.Errorf("invalid cwd_id")
	}
	if !integerJSONNumberPattern.MatchString(trimmed) {
		return 0, fmt.Errorf("invalid cwd_id")
	}
	value, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid cwd_id")
	}
	if value < 1 || value > maxCwdID {
		return 0, fmt.Errorf("invalid cwd_id")
	}
	return value, nil
}

func invalidCwdIDError(id *int64) *CwdError {
	return &CwdError{
		Code:    "invalid_cwd_id",
		Message: "cwd_id must be an integer from 1 to 9007199254740991",
		CwdID:   id,
		Hint: &ActionHint{
			SafeToRetry: false,
			Reason:      "Use a cwd_id returned by set_cwd, or omit cwd_id and use absolute paths.",
		},
	}
}

func staleCwdHint(reason string) *ActionHint {
	return &ActionHint{
		SafeToRetry:         false,
		RecommendedNextTool: "set_cwd",
		Reason:              reason,
	}
}
