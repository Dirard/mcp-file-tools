package api

// ToolName identifies one tool in the closed v2 surface.
type ToolName string

// InputStringMaxBytes is the shared UTF-8 byte cap for bounded string arguments.
const InputStringMaxBytes = 4_096

const (
	ToolSetCWD  ToolName = "set_cwd"
	ToolProject ToolName = "project"
	ToolSearch  ToolName = "search"
	ToolRead    ToolName = "read"
)

// OrderedToolNames returns the canonical v2 tool order without shared backing.
func OrderedToolNames() [4]ToolName {
	return [4]ToolName{ToolSetCWD, ToolProject, ToolSearch, ToolRead}
}

// Valid reports whether name belongs to the closed v2 tool vocabulary.
func (name ToolName) Valid() bool {
	switch name {
	case ToolSetCWD, ToolProject, ToolSearch, ToolRead:
		return true
	default:
		return false
	}
}

// Call is a transport-neutral tool invocation with owned argument bytes.
type Call struct {
	name      ToolName
	arguments []byte
}

// NewCall copies rawArguments so transport frame reuse cannot mutate the call.
func NewCall(name ToolName, rawArguments []byte) Call {
	return Call{
		name:      name,
		arguments: append([]byte(nil), rawArguments...),
	}
}

// Name returns the requested tool name.
func (c Call) Name() ToolName {
	return c.name
}

// Arguments returns a copy of the exact raw argument bytes.
func (c Call) Arguments() []byte {
	return append([]byte(nil), c.arguments...)
}
