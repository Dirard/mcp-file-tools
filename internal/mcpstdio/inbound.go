package mcpstdio

import (
	"errors"

	"github.com/Dirard/mcp-file-tools/internal/jsonwire"
)

const (
	protocolJSONMaxDepth          uint64 = 64
	protocolJSONObjectMaxMembers  uint64 = 4_096
	protocolJSONTotalItems        uint64 = 65_536
	protocolJSONKeyMaxBytes       uint64 = 4_096
	protocolJSONStringMaxBytes    uint64 = 262_144
	protocolJSONNumberMaxRawBytes uint64 = 256
)

var (
	parseErrorOutput     = string(encodeProtocolErrorNull(protocolParseError))
	invalidRequestOutput = string(encodeProtocolErrorNull(protocolInvalidRequest))
)

type inboundKind uint8

const (
	inboundParseError inboundKind = iota + 1
	inboundInvalidRequest
	inboundResponse
	inboundRequest
	inboundNotification
)

type inboundMessage struct {
	kind            inboundKind
	protocol        jsonwire.ProtocolView
	requestID       RequestID
	validationError *jsonwire.ValidationError
	output          string
}

func classifyInbound(raw []byte) (inboundMessage, error) {
	protocol, scanErr := jsonwire.ScanProtocolObject(raw, protocolJSONLimits())
	var validationError *jsonwire.ValidationError
	if scanErr != nil && !errors.As(scanErr, &validationError) {
		return inboundMessage{}, scanErr
	}
	if validationError != nil && validationError.Scope() == jsonwire.ScopeDocument {
		return inboundMessage{kind: inboundParseError, output: parseErrorOutput}, nil
	}

	root := protocol.Root()
	_, hasMethod := root.Member("method")
	_, hasResult := root.Member("result")
	_, hasError := root.Member("error")
	if !hasMethod && (hasResult || hasError) {
		return inboundMessage{kind: inboundResponse}, nil
	}
	if validationError != nil && validationError.Scope() == jsonwire.ScopeProtocolEnvelope {
		return inboundMessage{kind: inboundInvalidRequest, output: invalidRequestOutput}, nil
	}

	jsonrpc, ok := root.Value("jsonrpc")
	if !ok || !stringValueEquals(jsonrpc, "2.0") {
		return inboundMessage{kind: inboundInvalidRequest, output: invalidRequestOutput}, nil
	}
	method, ok := root.Value("method")
	if !ok || method.Kind() != jsonwire.String {
		return inboundMessage{kind: inboundInvalidRequest, output: invalidRequestOutput}, nil
	}

	message := inboundMessage{
		protocol:        protocol,
		validationError: validationError,
	}
	requestID, hasID, err := parseRequestID(root)
	if err != nil {
		return inboundMessage{kind: inboundInvalidRequest, output: invalidRequestOutput}, nil
	}
	if hasID {
		message.kind = inboundRequest
		message.requestID = requestID
	} else {
		message.kind = inboundNotification
	}
	return message, nil
}

func protocolJSONLimits() jsonwire.Limits {
	return jsonwire.Limits{
		MaxDepth:          protocolJSONMaxDepth,
		MaxObjectMembers:  protocolJSONObjectMaxMembers,
		MaxContainerItems: protocolJSONTotalItems,
		MaxKeyBytes:       protocolJSONKeyMaxBytes,
		MaxStringBytes:    protocolJSONStringMaxBytes,
		MaxNumberRawBytes: protocolJSONNumberMaxRawBytes,
	}
}

func stringValueEquals(value jsonwire.ValueView, want string) bool {
	if value.Kind() != jsonwire.String {
		return false
	}
	key, err := jsonwire.RequestIDSemanticKey(value.Bytes())
	return err == nil && len(key) == len(want)+1 && key[0] == 's' && string(key[1:]) == want
}
