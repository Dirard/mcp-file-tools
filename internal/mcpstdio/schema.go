package mcpstdio

import (
	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/jsonwire"
)

const (
	methodInitialize            = "initialize"
	methodPing                  = "ping"
	methodToolsList             = "tools/list"
	methodToolsCall             = "tools/call"
	methodNotificationReady     = "notifications/initialized"
	methodNotificationCancelled = "notifications/cancelled"
)

type inboundSchemaKind uint8

const (
	inboundSchemaAccepted inboundSchemaKind = iota + 1
	inboundSchemaUnknown
	inboundSchemaInvalid
)

type inboundSchemaResult struct {
	kind               inboundSchemaKind
	method             string
	requestID          RequestID
	protocolVersion    string
	call               api.Call
	cancellationID     SemanticIDKey
	cancellationReason string
	output             string
}

func validateInboundSchema(message inboundMessage) inboundSchemaResult {
	methodValue, ok := message.protocol.Root().Value("method")
	if !ok {
		return inboundSchemaResult{kind: inboundSchemaUnknown}
	}
	method, ok := decodedStringValue(methodValue)
	if !ok {
		return inboundSchemaResult{kind: inboundSchemaUnknown}
	}

	result := inboundSchemaResult{
		kind:      inboundSchemaUnknown,
		method:    method,
		requestID: message.requestID,
	}
	if !knownInboundSchema(message.kind, method) {
		return result
	}
	if message.validationError != nil {
		return invalidInboundSchema(message, result)
	}

	switch method {
	case methodInitialize:
		version, valid := validateInitializeParams(message)
		if !valid {
			return invalidInboundSchema(message, result)
		}
		result.protocolVersion = version
	case methodPing, methodNotificationReady:
		if !validateOpenBaseParams(message, false) {
			return invalidInboundSchema(message, result)
		}
	case methodToolsList:
		if !validateListToolsParams(message) {
			return invalidInboundSchema(message, result)
		}
	case methodToolsCall:
		call, valid := validateCallToolParams(message)
		if !valid {
			return invalidInboundSchema(message, result)
		}
		result.call = call
	case methodNotificationCancelled:
		cancelID, reason, valid := validateCancelledParams(message)
		if !valid {
			return invalidInboundSchema(message, result)
		}
		result.cancellationID = cancelID
		result.cancellationReason = reason
	}
	result.kind = inboundSchemaAccepted
	return result
}

func knownInboundSchema(kind inboundKind, method string) bool {
	if kind == inboundRequest {
		switch method {
		case methodInitialize, methodPing, methodToolsList, methodToolsCall:
			return true
		}
		return false
	}
	if kind == inboundNotification {
		return method == methodNotificationReady || method == methodNotificationCancelled
	}
	return false
}

func invalidInboundSchema(message inboundMessage, result inboundSchemaResult) inboundSchemaResult {
	result.kind = inboundSchemaInvalid
	if message.kind == inboundRequest {
		result.output = invalidParamsForID(message.requestID)
	}
	return result
}

func invalidParamsForID(id RequestID) string {
	return string(encodeProtocolError(id, protocolInvalidParams))
}

func validateInitializeParams(message inboundMessage) (string, bool) {
	params, ok := inboundParamsObject(message, true)
	if !ok || !validateMetaField(params) {
		return "", false
	}
	versionValue, ok := params.Value("protocolVersion")
	if !ok {
		return "", false
	}
	version, ok := decodedStringValue(versionValue)
	if !ok {
		return "", false
	}
	capabilities, ok := params.Value("capabilities")
	if !ok || !validateClientCapabilities(capabilities) {
		return "", false
	}
	clientInfo, ok := params.Value("clientInfo")
	if !ok || !validateImplementation(clientInfo) {
		return "", false
	}
	return version, true
}

func validateOpenBaseParams(message inboundMessage, required bool) bool {
	params, ok := inboundParamsObject(message, required)
	return ok && validateMetaField(params)
}

func validateListToolsParams(message inboundMessage) bool {
	params, ok := inboundParamsObject(message, false)
	if !ok || !validateMetaField(params) {
		return false
	}
	_, hasCursor := params.Member("cursor")
	return !hasCursor
}

func validateCallToolParams(message inboundMessage) (api.Call, bool) {
	params, ok := inboundParamsObject(message, true)
	if !ok || !validateMetaField(params) {
		return api.Call{}, false
	}

	nameValue, ok := params.Value("name")
	if !ok {
		return api.Call{}, false
	}
	nameText, ok := decodedStringValue(nameValue)
	name := api.ToolName(nameText)
	if !ok || !name.Valid() {
		return api.Call{}, false
	}

	arguments := []byte(`{}`)
	if _, present := params.Member("arguments"); present {
		argumentsValue, available := message.protocol.Arguments()
		if !available || argumentsValue.Kind() != jsonwire.Object {
			return api.Call{}, false
		}
		arguments = argumentsValue.Bytes()
	}

	if task, present := params.Value("task"); present && !validateTaskMetadata(task) {
		return api.Call{}, false
	}
	return api.NewCall(name, arguments), true
}

func validateCancelledParams(message inboundMessage) (SemanticIDKey, string, bool) {
	params, ok := inboundParamsObject(message, true)
	if !ok || !validateMetaField(params) {
		return SemanticIDKey{}, "", false
	}
	requestIDValue, present := params.Value("requestId")
	if !present {
		return SemanticIDKey{}, "", false
	}
	requestID, err := parseRequestIDValue(requestIDValue)
	if err != nil {
		return SemanticIDKey{}, "", false
	}

	var reason string
	if reasonValue, present := params.Value("reason"); present {
		var valid bool
		reason, valid = decodedStringValue(reasonValue)
		if !valid {
			return SemanticIDKey{}, "", false
		}
	}
	return requestID.SemanticKey(), reason, true
}

func inboundParamsObject(message inboundMessage, required bool) (jsonwire.ObjectView, bool) {
	value, present := message.protocol.ParamsValue()
	if !present {
		return jsonwire.ObjectView{}, !required
	}
	if value.Kind() != jsonwire.Object {
		return jsonwire.ObjectView{}, false
	}
	params, available := message.protocol.Params()
	return params, available
}

func validateMetaField(params jsonwire.ObjectView) bool {
	meta, present := params.Value("_meta")
	return !present || jsonwire.ValidateMeta(meta.Bytes()) == nil
}

func validateTaskMetadata(value jsonwire.ValueView) bool {
	task, ok := scanObjectValue(value)
	if !ok {
		return false
	}
	ttl, present := task.Value("ttl")
	return !present || ttl.Kind() == jsonwire.Number
}

func validateClientCapabilities(value jsonwire.ValueView) bool {
	capabilities, ok := scanObjectValue(value)
	if !ok {
		return false
	}

	experimental, present, valid := optionalObjectField(capabilities, "experimental")
	if !valid {
		return false
	}
	if present {
		for _, member := range experimental.Members() {
			if member.Kind != jsonwire.Object {
				return false
			}
		}
	}

	roots, present, valid := optionalObjectField(capabilities, "roots")
	if !valid || present && !optionalBooleanFields(roots, "listChanged") {
		return false
	}

	sampling, present, valid := optionalObjectField(capabilities, "sampling")
	if !valid || present && !optionalObjectFields(sampling, "context", "tools") {
		return false
	}

	elicitation, present, valid := optionalObjectField(capabilities, "elicitation")
	if !valid || present && !optionalObjectFields(elicitation, "form", "url") {
		return false
	}

	tasks, present, valid := optionalObjectField(capabilities, "tasks")
	if !valid {
		return false
	}
	if !present {
		return true
	}
	if !optionalObjectFields(tasks, "list", "cancel") {
		return false
	}
	requests, requestsPresent, valid := optionalObjectField(tasks, "requests")
	if !valid || !requestsPresent {
		return valid
	}

	samplingRequests, present, valid := optionalObjectField(requests, "sampling")
	if !valid || present && !optionalObjectFields(samplingRequests, "createMessage") {
		return false
	}
	elicitationRequests, present, valid := optionalObjectField(requests, "elicitation")
	return valid && (!present || optionalObjectFields(elicitationRequests, "create"))
}

func validateImplementation(value jsonwire.ValueView) bool {
	implementation, ok := scanObjectValue(value)
	if !ok || !requiredStringFields(implementation, "name", "version") {
		return false
	}
	if !optionalStringFields(implementation, "title", "description", "websiteUrl") {
		return false
	}

	iconsValue, present := implementation.Value("icons")
	if !present {
		return true
	}
	if iconsValue.Kind() != jsonwire.Array {
		return false
	}
	icons, err := jsonwire.ScanArray(iconsValue.Bytes(), protocolJSONLimits(), jsonwire.ValidateAll)
	if err != nil {
		return false
	}
	for _, iconValue := range icons.Values() {
		if !validateIcon(iconValue) {
			return false
		}
	}
	return true
}

func validateIcon(value jsonwire.ValueView) bool {
	icon, ok := scanObjectValue(value)
	if !ok || !requiredStringFields(icon, "src") || !optionalStringFields(icon, "mimeType") {
		return false
	}

	sizesValue, present := icon.Value("sizes")
	if present {
		if sizesValue.Kind() != jsonwire.Array {
			return false
		}
		sizes, err := jsonwire.ScanArray(sizesValue.Bytes(), protocolJSONLimits(), jsonwire.ValidateAll)
		if err != nil {
			return false
		}
		for _, size := range sizes.Values() {
			if size.Kind() != jsonwire.String {
				return false
			}
		}
	}

	themeValue, present := icon.Value("theme")
	if !present {
		return true
	}
	theme, ok := decodedStringValue(themeValue)
	return ok && (theme == "light" || theme == "dark")
}

func scanObjectValue(value jsonwire.ValueView) (jsonwire.ObjectView, bool) {
	if value.Kind() != jsonwire.Object {
		return jsonwire.ObjectView{}, false
	}
	view, err := jsonwire.ScanObject(value.Bytes(), protocolJSONLimits(), jsonwire.ValidateAll)
	return view, err == nil
}

func optionalObjectField(view jsonwire.ObjectView, name string) (jsonwire.ObjectView, bool, bool) {
	value, present := view.Value(name)
	if !present {
		return jsonwire.ObjectView{}, false, true
	}
	object, valid := scanObjectValue(value)
	return object, true, valid
}

func optionalObjectFields(view jsonwire.ObjectView, names ...string) bool {
	for _, name := range names {
		value, present := view.Value(name)
		if present && value.Kind() != jsonwire.Object {
			return false
		}
	}
	return true
}

func optionalBooleanFields(view jsonwire.ObjectView, names ...string) bool {
	for _, name := range names {
		value, present := view.Value(name)
		if present && value.Kind() != jsonwire.True && value.Kind() != jsonwire.False {
			return false
		}
	}
	return true
}

func requiredStringFields(view jsonwire.ObjectView, names ...string) bool {
	for _, name := range names {
		value, present := view.Value(name)
		if !present || value.Kind() != jsonwire.String {
			return false
		}
	}
	return true
}

func optionalStringFields(view jsonwire.ObjectView, names ...string) bool {
	for _, name := range names {
		value, present := view.Value(name)
		if present && value.Kind() != jsonwire.String {
			return false
		}
	}
	return true
}

func decodedStringValue(value jsonwire.ValueView) (string, bool) {
	if value.Kind() != jsonwire.String {
		return "", false
	}
	key, err := jsonwire.RequestIDSemanticKey(value.Bytes())
	if err != nil || len(key) == 0 || key[0] != 's' {
		return "", false
	}
	return string(key[1:]), true
}
