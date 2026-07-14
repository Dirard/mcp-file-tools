package jsonwire

// Span is a validated half-open byte range in one JSON input.
type Span struct {
	Start int
	End   int
}

// Limits bounds structural work and decoded scalar sizes for one scan.
type Limits struct {
	MaxDepth          uint64
	MaxObjectMembers  uint64
	MaxContainerItems uint64
	MaxKeyBytes       uint64
	MaxStringBytes    uint64
	MaxNumberRawBytes uint64
}

// ValueKind identifies one JSON value family.
type ValueKind uint8

const (
	Object ValueKind = iota + 1
	Array
	String
	Number
	True
	False
	Null
)

// Mode selects semantic validation without changing structural accounting.
type Mode uint8

const (
	ValidateAll Mode = iota + 1
	ProtocolWithRawArguments
	ToolArguments
)

func (mode Mode) valid() bool {
	switch mode {
	case ValidateAll, ProtocolWithRawArguments, ToolArguments:
		return true
	default:
		return false
	}
}

// ErrorKind is a stable validation failure category.
type ErrorKind string

const (
	KindSyntax    ErrorKind = "syntax"
	KindResource  ErrorKind = "resource"
	KindUnicode   ErrorKind = "unicode"
	KindDuplicate ErrorKind = "duplicate"
	KindMismatch  ErrorKind = "kind"
)

// ValidationScope identifies which protocol layer owns a validation failure.
type ValidationScope string

const (
	// ScopeValue is used by generic value and object validation.
	ScopeValue ValidationScope = "value"
	// ScopeDocument identifies malformed JSON syntax or raw UTF-8.
	ScopeDocument ValidationScope = "document"
	// ScopeProtocolEnvelope identifies a failure outside the params value.
	ScopeProtocolEnvelope ValidationScope = "protocol_envelope"
	// ScopeProtocolParams identifies a failure inside the params value.
	ScopeProtocolParams ValidationScope = "protocol_params"
)

// ValidationError identifies a failure category and byte position without
// retaining or echoing rejected input.
type ValidationError struct {
	kind     ErrorKind
	position int
	scope    ValidationScope
}

func newValidationError(kind ErrorKind, position int) *ValidationError {
	return &ValidationError{kind: kind, position: position, scope: ScopeValue}
}

func validationErrorWithScope(err error, scope ValidationScope) error {
	validationError, ok := err.(*ValidationError)
	if !ok {
		return err
	}
	result := *validationError
	result.scope = scope
	return &result
}

func (err *ValidationError) Error() string {
	return "jsonwire: " + string(err.kind)
}

// Kind returns the stable validation category.
func (err *ValidationError) Kind() ErrorKind {
	return err.kind
}

// Position returns the zero-based byte position of the failure.
func (err *ValidationError) Position() int {
	return err.position
}

// Scope returns the layer that owns the validation failure.
func (err *ValidationError) Scope() ValidationScope {
	return err.scope
}
