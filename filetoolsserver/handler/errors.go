package handler

import "errors"

// Sentinel errors for handler operations.
// Use errors.Is() to check for specific error types.

// Input validation errors
var (
	// ErrPathRequired is returned when a required path parameter is empty.
	ErrPathRequired = errors.New("path is required and must be a non-empty string")

	// ErrPatternRequired is returned when a required pattern parameter is empty.
	ErrPatternRequired = errors.New("pattern is required and must be a non-empty string")

	// ErrPathMustBeDirectory is returned when a directory is expected but a file was provided.
	ErrPathMustBeDirectory = errors.New("path must be a directory")
)

// Encoding errors
var (
	// ErrEncodingUnsupported is returned when an unsupported encoding is specified.
	// Wrap this error to include the encoding name: fmt.Errorf("%w: %s", ErrEncodingUnsupported, name)
	ErrEncodingUnsupported = errors.New("unsupported encoding")
)
