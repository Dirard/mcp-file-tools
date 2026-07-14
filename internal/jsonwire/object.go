package jsonwire

// Member describes one object member and its exact spans in the owning view.
type Member struct {
	Key     string
	KeySpan Span
	Value   Span
	Kind    ValueKind
}

// ObjectView owns one validated JSON object and exposes immutable member
// metadata. Spans index the private owned copy, not caller-owned memory.
type ObjectView struct {
	raw     []byte
	members []Member
}

// ArrayView owns one validated JSON array and exposes immutable element views.
// Spans index the private owned copy, not caller-owned memory.
type ArrayView struct {
	raw      []byte
	elements []scannedElement
}

// ValueView identifies one validated value inside an owning immutable view.
// Its span indexes the complete owned document, while Bytes returns a copy of
// only this value.
type ValueView struct {
	raw  []byte
	span Span
	kind ValueKind
}

// ProtocolView owns one protocol object. A successful scan exposes validated
// params members and deferred tools/call arguments; a scoped semantic or
// resource failure may expose only the recovered root header and params value.
type ProtocolView struct {
	root           ObjectView
	params         ObjectView
	paramsValue    ValueView
	arguments      ValueView
	hasParams      bool
	hasParamsValue bool
	hasArguments   bool
}

// ScanObject copies and validates one JSON object.
func ScanObject(raw []byte, limits Limits, mode Mode) (ObjectView, error) {
	owned := append([]byte(nil), raw...)
	result, err := scanDocumentDetailed(owned, limits, mode)
	if err != nil {
		return ObjectView{}, err
	}
	if result.Kind != Object {
		return ObjectView{}, newValidationError(KindMismatch, result.Span.Start)
	}
	return ObjectView{
		raw:     owned,
		members: result.Members,
	}, nil
}

// ScanArray copies and validates one JSON array.
func ScanArray(raw []byte, limits Limits, mode Mode) (ArrayView, error) {
	owned := append([]byte(nil), raw...)
	result, err := scanArrayDocumentDetailed(owned, limits, mode)
	if err != nil {
		return ArrayView{}, err
	}
	if result.Kind != Array {
		return ArrayView{}, newValidationError(KindMismatch, result.Span.Start)
	}
	return ArrayView{
		raw:      owned,
		elements: result.Elements,
	}, nil
}

// ScanProtocolObject copies and validates one protocol object. For a
// tools/call request, duplicate-key and surrogate validation inside arguments
// remains deferred until the returned arguments value is validated in
// ToolArguments mode. A semantic or resource error returns a structurally
// recovered view with its scoped ValidationError; a document error returns an
// empty view.
func ScanProtocolObject(raw []byte, limits Limits) (ProtocolView, error) {
	owned := append([]byte(nil), raw...)
	result, err := scanProtocolDocumentDetailed(owned, limits)
	if result.Kind != Object {
		return ProtocolView{}, err
	}
	view := protocolViewFromResult(owned, result)
	if err != nil {
		return view, err
	}
	if params, ok := view.root.Member("params"); ok && params.Kind == Object {
		view.params = ObjectView{raw: owned, members: result.ParamsMembers}
		view.hasParams = true
	}
	if result.HasRawArguments {
		view.arguments = ValueView{raw: owned, span: result.RawArguments, kind: Object}
		view.hasArguments = true
	}
	return view, nil
}

func protocolViewFromResult(owned []byte, result documentScanResult) ProtocolView {
	view := ProtocolView{
		root: ObjectView{
			raw:     owned,
			members: result.Members,
		},
	}
	if params, ok := view.root.Member("params"); ok {
		view.paramsValue = ValueView{raw: owned, span: params.Value, kind: params.Kind}
		view.hasParamsValue = true
	}
	return view
}

// Member returns the named member without exposing mutable internal storage.
func (view ObjectView) Member(name string) (Member, bool) {
	for _, member := range view.members {
		if member.Key == name {
			return member, true
		}
	}
	return Member{}, false
}

// Value returns the named member as an immutable value view.
func (view ObjectView) Value(name string) (ValueView, bool) {
	member, ok := view.Member(name)
	if !ok {
		return ValueView{}, false
	}
	return ValueView{raw: view.raw, span: member.Value, kind: member.Kind}, true
}

// Members returns the members in source insertion order as a defensive copy.
func (view ObjectView) Members() []Member {
	return append([]Member(nil), view.members...)
}

// Values returns the array elements in source order without exposing mutable
// internal storage.
func (view ArrayView) Values() []ValueView {
	values := make([]ValueView, len(view.elements))
	for index, element := range view.elements {
		values[index] = ValueView{raw: view.raw, span: element.Span, kind: element.Kind}
	}
	return values
}

// Root returns the complete root-member view, including a recovered header on
// a params-scoped semantic or resource error.
func (view ProtocolView) Root() ObjectView {
	return view.root
}

// Params returns validated direct members of an object-valued params field.
func (view ProtocolView) Params() (ObjectView, bool) {
	return view.params, view.hasParams
}

// ParamsValue returns the exact params value even when semantic or resource
// validation inside it failed during the bounded protocol scan.
func (view ProtocolView) ParamsValue() (ValueView, bool) {
	return view.paramsValue, view.hasParamsValue
}

// Arguments returns the exact tools/call arguments value when present.
func (view ProtocolView) Arguments() (ValueView, bool) {
	return view.arguments, view.hasArguments
}

// Kind reports the JSON kind recorded during the owning scan.
func (view ValueView) Kind() ValueKind {
	return view.kind
}

// Span returns the value's exact byte span in the complete owned document.
func (view ValueView) Span() Span {
	return view.span
}

// Bytes returns a defensive copy of the exact encoded JSON value.
func (view ValueView) Bytes() []byte {
	return append([]byte(nil), view.raw[view.span.Start:view.span.End]...)
}

// Validate revalidates the exact value under the requested semantic mode.
// Validation error positions are relative to the value returned by Bytes.
func (view ValueView) Validate(limits Limits, mode Mode) error {
	_, _, err := scanDocument(view.raw[view.span.Start:view.span.End], limits, mode)
	return err
}
