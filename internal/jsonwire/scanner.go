package jsonwire

import "unicode/utf8"

type containerState uint8

const (
	objectFirstKeyOrEnd containerState = iota + 1
	objectKey
	objectColon
	objectValue
	objectCommaOrEnd
	arrayFirstValueOrEnd
	arrayValue
	arrayCommaOrEnd
)

type containerFrame struct {
	kind              ValueKind
	state             containerState
	role              containerRole
	scope             ValidationScope
	deferred          bool
	compactChildren   bool
	start             int
	members           uint64
	membersExhausted  bool
	pendingKey        string
	pendingKeySpan    Span
	rootMemberIndex   int
	paramsMemberIndex int
	rootElementIndex  int
	// A Go string map hashes for lookup and confirms full-string equality, so
	// hash collisions cannot turn distinct decoded keys into duplicates.
	seenKeys map[string]struct{}
}

type containerRole uint8

const (
	roleGeneric containerRole = iota
	roleRoot
	roleParams
	roleRawArguments
)

type protocolRootHeader uint8

const (
	protocolHeaderJSONRPC protocolRootHeader = 1 << iota
	protocolHeaderID
	protocolHeaderMethod
	protocolHeaderParams
	protocolHeaderResult
	protocolHeaderError
)

type documentScanResult struct {
	Kind            ValueKind
	Span            Span
	Members         []Member
	ParamsMembers   []Member
	Elements        []scannedElement
	RawArguments    Span
	HasRawArguments bool
}

type scannedElement struct {
	Span Span
	Kind ValueKind
}

type structuralScanner struct {
	raw                     []byte
	limits                  Limits
	mode                    Mode
	position                int
	totalItems              uint64
	protocolEnvelopeItems   uint64
	protocolParamsItems     uint64
	protocolEnvelopeFirst   int
	protocolParamsFirst     int
	stack                   []containerFrame
	overflow                []containerState
	method                  string
	methodIsString          bool
	rawArguments            Span
	rawArgumentsKind        ValueKind
	hasRawArguments         bool
	captureRootMembers      bool
	captureParamsMembers    bool
	captureRootElements     bool
	rootMembers             []Member
	paramsMembers           []Member
	rootElements            []scannedElement
	rootHeaders             protocolRootHeader
	collectProtocolErrors   bool
	containerItemsExhausted bool
	envelopeError           *ValidationError
	paramsError             *ValidationError
	deferredError           *ValidationError
}

func scanDocument(raw []byte, limits Limits, mode Mode) (ValueKind, Span, error) {
	result, err := scanDocumentConfigured(raw, limits, mode, false, false, false, false)
	if err != nil {
		return 0, Span{}, err
	}
	return result.Kind, result.Span, nil
}

func scanDocumentDetailed(raw []byte, limits Limits, mode Mode) (documentScanResult, error) {
	return scanDocumentConfigured(raw, limits, mode, true, false, false, false)
}

func scanArrayDocumentDetailed(raw []byte, limits Limits, mode Mode) (documentScanResult, error) {
	return scanDocumentConfigured(raw, limits, mode, false, true, false, false)
}

func scanProtocolDocumentDetailed(raw []byte, limits Limits) (documentScanResult, error) {
	return scanDocumentConfigured(raw, limits, ProtocolWithRawArguments, true, false, true, true)
}

func scanDocumentConfigured(raw []byte, limits Limits, mode Mode, captureMembers, captureElements, captureParamsMembers, collectProtocolErrors bool) (documentScanResult, error) {
	if !mode.valid() {
		return documentScanResult{}, newValidationError(KindMismatch, 0)
	}
	scanner := structuralScanner{
		raw:                   raw,
		limits:                limits,
		mode:                  mode,
		captureRootMembers:    captureMembers,
		captureParamsMembers:  captureParamsMembers,
		captureRootElements:   captureElements,
		collectProtocolErrors: collectProtocolErrors,
		protocolEnvelopeFirst: -1,
		protocolParamsFirst:   -1,
	}
	scanner.skipWhitespace()
	start := scanner.position
	scope := ScopeValue
	if collectProtocolErrors {
		scope = ScopeProtocolEnvelope
	}
	kind, err := scanner.scanValue(roleRoot, scope, false)
	if err != nil {
		return documentScanResult{}, scanner.documentError(err)
	}
	for len(scanner.stack) != 0 {
		if err := scanner.stepContainer(); err != nil {
			return documentScanResult{}, scanner.documentError(err)
		}
	}
	end := scanner.position
	scanner.skipWhitespace()
	if scanner.position != len(raw) {
		return documentScanResult{}, scanner.documentError(scanner.invalidAt(scanner.position))
	}
	scanner.finishContainerItemLimit()
	if err := scanner.finishProtocolSemantics(); err != nil {
		return documentScanResult{}, scanner.documentError(err)
	}
	if collectProtocolErrors && kind != Object {
		scanner.recordRecoverable(newValidationError(KindMismatch, start), ScopeProtocolEnvelope, false)
	}
	result := documentScanResult{
		Kind:            kind,
		Span:            Span{Start: start, End: end},
		Members:         scanner.rootMembers,
		ParamsMembers:   scanner.paramsMembers,
		Elements:        scanner.rootElements,
		RawArguments:    scanner.rawArguments,
		HasRawArguments: scanner.hasRawArguments && scanner.methodIsString && scanner.method == "tools/call",
	}
	if collectProtocolErrors {
		if scanner.envelopeError != nil {
			return result, scanner.envelopeError
		}
		if scanner.paramsError != nil {
			return result, scanner.paramsError
		}
	}
	return result, nil
}

func (scanner *structuralScanner) scanValue(role containerRole, scope ValidationScope, deferred bool) (ValueKind, error) {
	if scanner.position >= len(scanner.raw) {
		return 0, newValidationError(KindSyntax, len(scanner.raw))
	}
	start := scanner.position
	switch scanner.raw[start] {
	case '{':
		if err := scanner.pushContainer(Object, objectFirstKeyOrEnd, role, scope, deferred); err != nil {
			return 0, err
		}
		return Object, nil
	case '[':
		if err := scanner.pushContainer(Array, arrayFirstValueOrEnd, role, scope, deferred); err != nil {
			return 0, err
		}
		return Array, nil
	case '"':
		_, span, err := scanner.scanString(start, scanner.limits.MaxStringBytes, false, scope, deferred)
		if err != nil {
			return 0, err
		}
		scanner.position = span.End
		return String, nil
	case 't':
		if err := scanner.consumeLiteral("true"); err != nil {
			return 0, err
		}
		return True, nil
	case 'f':
		if err := scanner.consumeLiteral("false"); err != nil {
			return 0, err
		}
		return False, nil
	case 'n':
		if err := scanner.consumeLiteral("null"); err != nil {
			return 0, err
		}
		return Null, nil
	case '-':
		if err := scanner.scanNumberRecovering(scope, deferred); err != nil {
			return 0, err
		}
		return Number, nil
	default:
		if scanner.raw[start] >= '0' && scanner.raw[start] <= '9' {
			if err := scanner.scanNumberRecovering(scope, deferred); err != nil {
				return 0, err
			}
			return Number, nil
		}
		return 0, scanner.invalidAt(start)
	}
}

func (scanner *structuralScanner) pushContainer(kind ValueKind, state containerState, role containerRole, scope ValidationScope, deferred bool) error {
	if len(scanner.overflow) != 0 || (len(scanner.stack) != 0 && scanner.stack[len(scanner.stack)-1].compactChildren) {
		scanner.position++
		scanner.overflow = append(scanner.overflow, state)
		return nil
	}
	compactChildren := false
	if uint64(len(scanner.stack)) >= scanner.limits.MaxDepth {
		err := newValidationError(KindResource, scanner.position)
		if !scanner.scopeHasError(scope) && !scanner.recordRecoverable(err, scope, deferred) {
			return err
		}
		compactChildren = true
	}
	start := scanner.position
	scanner.position++
	scanner.stack = append(scanner.stack, containerFrame{
		kind:              kind,
		state:             state,
		role:              role,
		scope:             scope,
		deferred:          deferred,
		compactChildren:   compactChildren,
		start:             start,
		rootMemberIndex:   -1,
		paramsMemberIndex: -1,
		rootElementIndex:  -1,
	})
	return nil
}

func (scanner *structuralScanner) stepContainer() error {
	if len(scanner.overflow) != 0 {
		return scanner.stepOverflow()
	}
	index := len(scanner.stack) - 1
	frame := scanner.stack[index]
	if frame.kind == Object {
		return scanner.stepObject(index, frame.state)
	}
	return scanner.stepArray(index, frame.state)
}

func (scanner *structuralScanner) stepOverflow() error {
	index := len(scanner.overflow) - 1
	scanner.skipWhitespace()
	switch scanner.overflow[index] {
	case objectFirstKeyOrEnd:
		if scanner.take('}') {
			scanner.overflow = scanner.overflow[:index]
			return nil
		}
		return scanner.scanOverflowObjectKey(index)
	case objectKey:
		return scanner.scanOverflowObjectKey(index)
	case objectColon:
		if !scanner.take(':') {
			return scanner.invalidAt(scanner.position)
		}
		scanner.overflow[index] = objectValue
		return nil
	case objectValue:
		scanner.overflow[index] = objectCommaOrEnd
		_, err := scanner.scanOverflowValue()
		return err
	case objectCommaOrEnd:
		if scanner.take(',') {
			scanner.overflow[index] = objectKey
			return nil
		}
		if scanner.take('}') {
			scanner.overflow = scanner.overflow[:index]
			return nil
		}
		return scanner.invalidAt(scanner.position)
	case arrayFirstValueOrEnd:
		if scanner.take(']') {
			scanner.overflow = scanner.overflow[:index]
			return nil
		}
		scanner.overflow[index] = arrayCommaOrEnd
		_, err := scanner.scanOverflowValue()
		return err
	case arrayValue:
		scanner.overflow[index] = arrayCommaOrEnd
		_, err := scanner.scanOverflowValue()
		return err
	case arrayCommaOrEnd:
		if scanner.take(',') {
			scanner.overflow[index] = arrayValue
			return nil
		}
		if scanner.take(']') {
			scanner.overflow = scanner.overflow[:index]
			return nil
		}
		return scanner.invalidAt(scanner.position)
	default:
		return newValidationError(KindSyntax, scanner.position)
	}
}

func (scanner *structuralScanner) scanOverflowObjectKey(index int) error {
	_, span, err := scanJSONStringSemantics(scanner.raw, scanner.position, ^uint64(0), false, false)
	if err != nil {
		return err
	}
	scanner.position = span.End
	scanner.overflow[index] = objectColon
	return nil
}

func (scanner *structuralScanner) scanOverflowValue() (ValueKind, error) {
	if len(scanner.stack) == 0 {
		return 0, newValidationError(KindSyntax, scanner.position)
	}
	frame := scanner.stack[len(scanner.stack)-1]
	return scanner.scanValue(roleGeneric, frame.scope, frame.deferred)
}

func (scanner *structuralScanner) stepObject(index int, state containerState) error {
	scanner.skipWhitespace()
	switch state {
	case objectFirstKeyOrEnd:
		if scanner.take('}') {
			scanner.closeContainer(index)
			return nil
		}
		return scanner.scanObjectKey(index)
	case objectKey:
		return scanner.scanObjectKey(index)
	case objectColon:
		if !scanner.take(':') {
			return scanner.invalidAt(scanner.position)
		}
		scanner.stack[index].state = objectValue
		return nil
	case objectValue:
		return scanner.scanObjectValue(index)
	case objectCommaOrEnd:
		if scanner.take(',') {
			scanner.stack[index].state = objectKey
			return nil
		}
		if scanner.take('}') {
			scanner.closeContainer(index)
			return nil
		}
		return scanner.invalidAt(scanner.position)
	default:
		return newValidationError(KindSyntax, scanner.position)
	}
}

func (scanner *structuralScanner) scanObjectKey(index int) error {
	if scanner.position >= len(scanner.raw) || scanner.raw[scanner.position] != '"' {
		return scanner.invalidAt(scanner.position)
	}
	if err := scanner.chargeObjectMember(index); err != nil {
		return err
	}
	frame := scanner.stack[index]
	semanticValidation := !frame.deferred || scanner.mode == ProtocolWithRawArguments
	capture := semanticValidation && !scanner.scopeHasError(frame.scope) && !(frame.deferred && scanner.deferredError != nil)
	key, span, err := scanner.scanString(scanner.position, scanner.limits.MaxKeyBytes, capture, frame.scope, frame.deferred)
	if err != nil {
		return err
	}
	if key == "" && scanner.collectProtocolErrors {
		key = scanner.knownProtocolKey(frame.role, span)
	}
	semanticRecovery := scanner.scopeHasError(frame.scope) || (frame.deferred && scanner.deferredError != nil)
	if semanticValidation && !semanticRecovery {
		if scanner.stack[index].seenKeys == nil {
			scanner.stack[index].seenKeys = make(map[string]struct{})
		}
		if _, exists := scanner.stack[index].seenKeys[key]; exists {
			err := newValidationError(KindDuplicate, span.Start)
			if !scanner.recordRecoverable(err, frame.scope, frame.deferred) {
				return err
			}
		} else {
			scanner.stack[index].seenKeys[key] = struct{}{}
		}
		scanner.stack[index].pendingKey = key
	} else {
		scanner.stack[index].pendingKey = key
	}
	scanner.position = span.End
	scanner.stack[index].pendingKeySpan = span
	scanner.stack[index].state = objectColon
	return nil
}

func (scanner *structuralScanner) scanObjectValue(index int) error {
	frame := scanner.stack[index]
	role := roleGeneric
	scope := frame.scope
	deferred := frame.deferred
	if !deferred && scanner.mode == ProtocolWithRawArguments {
		switch {
		case frame.role == roleRoot && frame.pendingKey == "params":
			role = roleParams
			scope = ScopeProtocolParams
		case frame.role == roleParams && frame.pendingKey == "arguments":
			role = roleRawArguments
			deferred = true
		}
	}

	start := scanner.position
	scanner.stack[index].state = objectCommaOrEnd
	stackDepth := len(scanner.stack)
	kind, err := scanner.scanValue(role, scope, deferred)
	if err != nil {
		return err
	}
	if frame.role == roleRoot && frame.pendingKey == "method" {
		scanner.methodIsString = kind == String
		if scanner.methodIsString && !scanner.scopeHasError(ScopeProtocolEnvelope) {
			method, _, err := decodeJSONString(scanner.raw, start)
			if err != nil {
				if !scanner.recordRecoverable(err, ScopeProtocolEnvelope, false) {
					return err
				}
			} else {
				scanner.method = method
			}
		}
	}
	captureRoot := frame.role == roleRoot && scanner.captureRootMembers && scanner.shouldCaptureRootMember(frame.pendingKey)
	if (kind == Object || kind == Array) && len(scanner.stack) == stackDepth {
		captureRoot = false
	}
	if captureRoot {
		memberIndex := len(scanner.rootMembers)
		valueSpan := Span{Start: start, End: scanner.position}
		if (kind == Object || kind == Array) && len(scanner.stack) > stackDepth {
			valueSpan.End = 0
			scanner.stack[len(scanner.stack)-1].rootMemberIndex = memberIndex
		}
		scanner.rootMembers = append(scanner.rootMembers, Member{
			Key:     frame.pendingKey,
			KeySpan: frame.pendingKeySpan,
			Value:   valueSpan,
			Kind:    kind,
		})
		scanner.rootHeaders |= protocolRootHeaderFor(frame.pendingKey)
	}
	captureParams := frame.role == roleParams && scanner.captureParamsMembers &&
		!scanner.scopeHasError(ScopeProtocolParams) && !scanner.containerItemsExhausted
	if (kind == Object || kind == Array) && len(scanner.stack) == stackDepth {
		captureParams = false
	}
	if captureParams {
		memberIndex := len(scanner.paramsMembers)
		valueSpan := Span{Start: start, End: scanner.position}
		if (kind == Object || kind == Array) && len(scanner.stack) > stackDepth {
			valueSpan.End = 0
			scanner.stack[len(scanner.stack)-1].paramsMemberIndex = memberIndex
		}
		scanner.paramsMembers = append(scanner.paramsMembers, Member{
			Key:     frame.pendingKey,
			KeySpan: frame.pendingKeySpan,
			Value:   valueSpan,
			Kind:    kind,
		})
	}
	if role == roleRawArguments && kind != Object && kind != Array {
		scanner.recordRawArguments(kind, Span{Start: start, End: scanner.position})
	}
	return nil
}

func (scanner *structuralScanner) stepArray(index int, state containerState) error {
	scanner.skipWhitespace()
	switch state {
	case arrayFirstValueOrEnd:
		if scanner.take(']') {
			scanner.closeContainer(index)
			return nil
		}
		return scanner.scanArrayValue(index)
	case arrayValue:
		return scanner.scanArrayValue(index)
	case arrayCommaOrEnd:
		if scanner.take(',') {
			scanner.stack[index].state = arrayValue
			return nil
		}
		if scanner.take(']') {
			scanner.closeContainer(index)
			return nil
		}
		return scanner.invalidAt(scanner.position)
	default:
		return newValidationError(KindSyntax, scanner.position)
	}
}

func (scanner *structuralScanner) scanArrayValue(index int) error {
	frame := scanner.stack[index]
	if err := scanner.chargeItem(scanner.position, frame.scope, frame.deferred); err != nil {
		return err
	}
	start := scanner.position
	scanner.stack[index].state = arrayCommaOrEnd
	stackDepth := len(scanner.stack)
	kind, err := scanner.scanValue(roleGeneric, frame.scope, frame.deferred)
	if err != nil {
		return err
	}
	if frame.role == roleRoot && scanner.captureRootElements {
		elementIndex := len(scanner.rootElements)
		span := Span{Start: start, End: scanner.position}
		if (kind == Object || kind == Array) && len(scanner.stack) > stackDepth {
			span.End = 0
			scanner.stack[len(scanner.stack)-1].rootElementIndex = elementIndex
		}
		scanner.rootElements = append(scanner.rootElements, scannedElement{Span: span, Kind: kind})
	}
	return nil
}

func (scanner *structuralScanner) closeContainer(index int) {
	frame := scanner.stack[index]
	if frame.rootMemberIndex >= 0 {
		scanner.rootMembers[frame.rootMemberIndex].Value.End = scanner.position
	}
	if frame.paramsMemberIndex >= 0 {
		scanner.paramsMembers[frame.paramsMemberIndex].Value.End = scanner.position
	}
	if frame.rootElementIndex >= 0 {
		scanner.rootElements[frame.rootElementIndex].Span.End = scanner.position
	}
	if frame.role == roleRawArguments {
		scanner.recordRawArguments(frame.kind, Span{Start: frame.start, End: scanner.position})
	}
	scanner.stack = scanner.stack[:index]
}

func (scanner *structuralScanner) recordRawArguments(kind ValueKind, span Span) {
	scanner.rawArguments = span
	scanner.rawArgumentsKind = kind
	scanner.hasRawArguments = true
}

func (scanner *structuralScanner) documentError(err error) error {
	if !scanner.collectProtocolErrors {
		return err
	}
	return validationErrorWithScope(err, ScopeDocument)
}

func (scanner *structuralScanner) recordRecoverable(err error, scope ValidationScope, deferred bool) bool {
	validationError, ok := err.(*ValidationError)
	if !ok {
		return false
	}
	switch validationError.Kind() {
	case KindResource, KindDuplicate, KindMismatch:
	case KindUnicode:
		position := validationError.Position()
		if position < 0 || position >= len(scanner.raw) || scanner.raw[position] != '\\' {
			return false
		}
	default:
		return false
	}

	result := *validationError
	result.scope = scope
	if deferred && scanner.mode == ProtocolWithRawArguments && (result.kind == KindDuplicate || result.kind == KindUnicode) {
		if !scanner.collectProtocolErrors {
			result.scope = ScopeValue
		}
		if scanner.deferredError == nil {
			scanner.deferredError = &result
		}
		return true
	}
	if !scanner.collectProtocolErrors {
		return false
	}
	switch scope {
	case ScopeProtocolEnvelope:
		if scanner.envelopeError == nil {
			scanner.envelopeError = &result
		}
	case ScopeProtocolParams:
		if scanner.paramsError == nil {
			scanner.paramsError = &result
		}
	default:
		return false
	}
	return true
}

func (scanner *structuralScanner) scopeHasError(scope ValidationScope) bool {
	if !scanner.collectProtocolErrors {
		return false
	}
	switch scope {
	case ScopeProtocolEnvelope:
		return scanner.envelopeError != nil
	case ScopeProtocolParams:
		return scanner.envelopeError != nil || scanner.paramsError != nil
	default:
		return false
	}
}

func (scanner *structuralScanner) scanString(start int, maxDecodedBytes uint64, capture bool, scope ValidationScope, deferred bool) (string, Span, error) {
	semanticValidation := !deferred || scanner.mode == ProtocolWithRawArguments
	semanticRecovery := scanner.scopeHasError(scope) || (deferred && scanner.deferredError != nil)
	limit := maxDecodedBytes
	if scanner.scopeHasError(scope) {
		limit = ^uint64(0)
	}
	value, span, err := scanJSONStringSemantics(
		scanner.raw,
		start,
		limit,
		capture && semanticValidation && !semanticRecovery,
		semanticValidation && !semanticRecovery,
	)
	if err == nil {
		return value, span, nil
	}
	if !scanner.recordRecoverable(err, scope, deferred) {
		return "", Span{}, err
	}

	limit = maxDecodedBytes
	if scanner.scopeHasError(scope) {
		limit = ^uint64(0)
	}
	_, span, err = scanJSONStringSemantics(scanner.raw, start, limit, false, false)
	if err == nil {
		return "", span, nil
	}
	if !scanner.recordRecoverable(err, scope, deferred) {
		return "", Span{}, err
	}
	_, span, err = scanJSONStringSemantics(scanner.raw, start, ^uint64(0), false, false)
	return "", span, err
}

func (scanner *structuralScanner) knownProtocolKey(role containerRole, span Span) string {
	var names []string
	switch role {
	case roleRoot:
		names = []string{"jsonrpc", "id", "method", "params", "result", "error"}
	case roleParams:
		names = []string{"arguments"}
	default:
		return ""
	}
	for _, name := range names {
		if jsonStringEquals(scanner.raw, span, name) {
			return name
		}
	}
	return ""
}

func jsonStringEquals(raw []byte, span Span, want string) bool {
	if span.Start < 0 || span.End > len(raw) || span.End-span.Start < 2 || raw[span.Start] != '"' || raw[span.End-1] != '"' {
		return false
	}
	position := span.Start + 1
	wantPosition := 0
	for position < span.End-1 {
		var value rune
		if raw[position] == '\\' {
			var next int
			var err error
			value, next, err = decodeJSONEscape(raw, position, false)
			if err != nil {
				return false
			}
			position = next
		} else {
			if raw[position] >= utf8.RuneSelf {
				return false
			}
			value = rune(raw[position])
			position++
		}
		if value > utf8.RuneSelf-1 || wantPosition >= len(want) || byte(value) != want[wantPosition] {
			return false
		}
		wantPosition++
	}
	return wantPosition == len(want)
}

func protocolRootHeaderFor(key string) protocolRootHeader {
	switch key {
	case "jsonrpc":
		return protocolHeaderJSONRPC
	case "id":
		return protocolHeaderID
	case "method":
		return protocolHeaderMethod
	case "params":
		return protocolHeaderParams
	case "result":
		return protocolHeaderResult
	case "error":
		return protocolHeaderError
	default:
		return 0
	}
}

func (scanner *structuralScanner) shouldCaptureRootMember(key string) bool {
	if !scanner.collectProtocolErrors {
		return true
	}
	if !scanner.scopeHasError(ScopeProtocolEnvelope) && !scanner.containerItemsExhausted {
		return true
	}
	header := protocolRootHeaderFor(key)
	return header != 0 && scanner.rootHeaders&header == 0
}

func (scanner *structuralScanner) finishProtocolSemantics() error {
	if scanner.mode != ProtocolWithRawArguments || !scanner.hasRawArguments {
		return nil
	}
	if scanner.methodIsString && scanner.method == "tools/call" {
		if scanner.rawArgumentsKind != Object {
			err := newValidationError(KindMismatch, scanner.rawArguments.Start)
			if !scanner.recordRecoverable(err, ScopeProtocolParams, false) {
				return err
			}
		}
		return nil
	}
	if scanner.deferredError != nil && scanner.paramsError == nil {
		if !scanner.collectProtocolErrors {
			return scanner.deferredError
		}
		result := *scanner.deferredError
		result.scope = ScopeProtocolParams
		scanner.paramsError = &result
	}
	return nil
}

func (scanner *structuralScanner) chargeObjectMember(index int) error {
	frame := &scanner.stack[index]
	if !frame.membersExhausted && frame.members >= scanner.limits.MaxObjectMembers {
		frame.membersExhausted = true
		err := newValidationError(KindResource, scanner.position)
		if !scanner.scopeHasError(frame.scope) && !scanner.recordRecoverable(err, frame.scope, frame.deferred) {
			return err
		}
	}
	if err := scanner.chargeItem(scanner.position, frame.scope, frame.deferred); err != nil {
		return err
	}
	if !frame.membersExhausted {
		frame.members++
	}
	return nil
}

func (scanner *structuralScanner) chargeItem(position int, scope ValidationScope, deferred bool) error {
	if scanner.collectProtocolErrors {
		scanner.chargeProtocolItem(position, scope)
		return nil
	}
	if scanner.totalItems >= scanner.limits.MaxContainerItems {
		err := newValidationError(KindResource, position)
		if !scanner.recordRecoverable(err, scope, deferred) {
			return err
		}
		return nil
	}
	scanner.totalItems++
	return nil
}

func (scanner *structuralScanner) chargeProtocolItem(position int, scope ValidationScope) {
	incrementSaturating(&scanner.totalItems)
	switch scope {
	case ScopeProtocolEnvelope:
		if scanner.protocolEnvelopeFirst < 0 {
			scanner.protocolEnvelopeFirst = position
		}
		incrementSaturating(&scanner.protocolEnvelopeItems)
	case ScopeProtocolParams:
		if scanner.protocolParamsFirst < 0 {
			scanner.protocolParamsFirst = position
		}
		incrementSaturating(&scanner.protocolParamsItems)
	}
	if scanner.totalItems > scanner.limits.MaxContainerItems {
		scanner.containerItemsExhausted = true
	}
}

func incrementSaturating(value *uint64) {
	if *value != ^uint64(0) {
		*value = *value + 1
	}
}

func (scanner *structuralScanner) finishContainerItemLimit() {
	if !scanner.collectProtocolErrors || !scanner.containerItemsExhausted {
		return
	}

	// Canonically account for all envelope items before params items. This
	// makes the owning error layer independent of source member order: params
	// owns the overflow only when the envelope by itself remains within cap.
	scope := ScopeProtocolParams
	position := scanner.protocolParamsFirst
	if scanner.protocolEnvelopeItems > scanner.limits.MaxContainerItems {
		scope = ScopeProtocolEnvelope
		position = scanner.protocolEnvelopeFirst
	}
	if position < 0 {
		position = 0
	}
	scanner.recordRecoverable(newValidationError(KindResource, position), scope, false)
}

func (scanner *structuralScanner) consumeLiteral(want string) error {
	start := scanner.position
	for offset := range len(want) {
		position := start + offset
		if position >= len(scanner.raw) {
			return newValidationError(KindSyntax, len(scanner.raw))
		}
		if scanner.raw[position] != want[offset] {
			return scanner.invalidAt(position)
		}
	}
	scanner.position += len(want)
	return nil
}

func (scanner *structuralScanner) scanNumberRecovering(scope ValidationScope, deferred bool) error {
	start := scanner.position
	limit := scanner.limits.MaxNumberRawBytes
	if scanner.scopeHasError(scope) {
		limit = ^uint64(0)
	}
	if err := scanner.scanNumber(limit); err != nil {
		if !scanner.recordRecoverable(err, scope, deferred) {
			return err
		}
		scanner.position = start
		return scanner.scanNumber(^uint64(0))
	}
	return nil
}

func (scanner *structuralScanner) scanNumber(maxRawBytes uint64) error {
	start := scanner.position
	if scanner.take('-') {
		if err := scanner.checkNumberLimit(start, maxRawBytes); err != nil {
			return err
		}
	}
	if scanner.position >= len(scanner.raw) {
		return newValidationError(KindSyntax, len(scanner.raw))
	}
	if scanner.take('0') {
		if err := scanner.checkNumberLimit(start, maxRawBytes); err != nil {
			return err
		}
	} else {
		if scanner.raw[scanner.position] < '1' || scanner.raw[scanner.position] > '9' {
			return scanner.invalidAt(scanner.position)
		}
		for scanner.position < len(scanner.raw) && scanner.raw[scanner.position] >= '0' && scanner.raw[scanner.position] <= '9' {
			scanner.position++
			if err := scanner.checkNumberLimit(start, maxRawBytes); err != nil {
				return err
			}
		}
	}
	if scanner.take('.') {
		if err := scanner.checkNumberLimit(start, maxRawBytes); err != nil {
			return err
		}
		if scanner.position >= len(scanner.raw) || scanner.raw[scanner.position] < '0' || scanner.raw[scanner.position] > '9' {
			return scanner.invalidAt(scanner.position)
		}
		for scanner.position < len(scanner.raw) && scanner.raw[scanner.position] >= '0' && scanner.raw[scanner.position] <= '9' {
			scanner.position++
			if err := scanner.checkNumberLimit(start, maxRawBytes); err != nil {
				return err
			}
		}
	}
	if scanner.position < len(scanner.raw) && (scanner.raw[scanner.position] == 'e' || scanner.raw[scanner.position] == 'E') {
		scanner.position++
		if err := scanner.checkNumberLimit(start, maxRawBytes); err != nil {
			return err
		}
		if scanner.take('+') || scanner.take('-') {
			if err := scanner.checkNumberLimit(start, maxRawBytes); err != nil {
				return err
			}
		}
		if scanner.position >= len(scanner.raw) || scanner.raw[scanner.position] < '0' || scanner.raw[scanner.position] > '9' {
			return scanner.invalidAt(scanner.position)
		}
		for scanner.position < len(scanner.raw) && scanner.raw[scanner.position] >= '0' && scanner.raw[scanner.position] <= '9' {
			scanner.position++
			if err := scanner.checkNumberLimit(start, maxRawBytes); err != nil {
				return err
			}
		}
	}
	return nil
}

func (scanner *structuralScanner) checkNumberLimit(start int, maxRawBytes uint64) error {
	if uint64(scanner.position-start) > maxRawBytes {
		return newValidationError(KindResource, scanner.position-1)
	}
	return nil
}

func (scanner *structuralScanner) skipWhitespace() {
	for scanner.position < len(scanner.raw) {
		switch scanner.raw[scanner.position] {
		case ' ', '\t', '\n', '\r':
			scanner.position++
		default:
			return
		}
	}
}

func (scanner *structuralScanner) take(want byte) bool {
	if scanner.position >= len(scanner.raw) || scanner.raw[scanner.position] != want {
		return false
	}
	scanner.position++
	return true
}

func (scanner *structuralScanner) invalidAt(position int) error {
	if position >= len(scanner.raw) {
		return newValidationError(KindSyntax, len(scanner.raw))
	}
	if scanner.raw[position] < utf8.RuneSelf {
		return newValidationError(KindSyntax, position)
	}
	iterator := utf8Iterator{raw: scanner.raw, position: position}
	_, _, _, err := iterator.next()
	if err != nil {
		return err
	}
	return newValidationError(KindSyntax, position)
}
