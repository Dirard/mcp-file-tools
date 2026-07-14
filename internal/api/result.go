package api

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const maxCWDID uint64 = 1<<53 - 1

// ResultKind identifies the single payload channel carried by a Result.
type ResultKind uint8

const (
	ResultText ResultKind = iota + 1
	ResultCWD
)

// Result is a transport-neutral, single-channel tool outcome.
type Result struct {
	kind    ResultKind
	text    string
	cwdID   uint64
	isError bool
}

// Navigation constructs a text result for navigation output or an error line.
func Navigation(text string, isError bool) Result {
	return Result{
		kind:    ResultText,
		text:    text,
		isError: isError,
	}
}

// SetCWD constructs a successful cwd registration result.
func SetCWD(cwdID uint64) Result {
	return Result{
		kind:  ResultCWD,
		cwdID: cwdID,
	}
}

// Kind returns the active result channel.
func (r Result) Kind() ResultKind {
	return r.kind
}

// Text returns the text payload only for navigation results.
func (r Result) Text() (string, bool) {
	if r.kind != ResultText {
		return "", false
	}
	return r.text, true
}

// CWDID returns the registered cwd identifier only for cwd results.
func (r Result) CWDID() (uint64, bool) {
	if r.kind != ResultCWD {
		return 0, false
	}
	return r.cwdID, true
}

// IsError reports whether a navigation result represents a tool failure.
func (r Result) IsError() bool {
	return r.isError
}

// Validate enforces the single-channel result contract.
func (r Result) Validate() error {
	switch r.kind {
	case ResultText:
		if r.text == "" {
			return errors.New("text result is empty")
		}
		if !utf8.ValidString(r.text) {
			return errors.New("text result is not valid UTF-8")
		}
		if !strings.HasSuffix(r.text, "\n") {
			return errors.New("text result lacks final LF")
		}
		if strings.HasSuffix(r.text, "\n\n") {
			return errors.New("text result has a trailing blank line")
		}
		if r.cwdID != 0 {
			return errors.New("text result also contains cwd id")
		}
		return nil
	case ResultCWD:
		if r.text != "" {
			return errors.New("cwd result also contains text")
		}
		if r.cwdID == 0 || r.cwdID > maxCWDID {
			return errors.New("cwd id is outside the supported range")
		}
		if r.isError {
			return errors.New("cwd result cannot be an error")
		}
		return nil
	default:
		return errors.New("unknown result kind")
	}
}
