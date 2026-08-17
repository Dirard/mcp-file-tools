package mcpstdio

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

type protocolError struct {
	code    int
	message string
}

var (
	protocolParseError          = protocolError{code: -32700, message: "parse error"}
	protocolInvalidRequest      = protocolError{code: -32600, message: "invalid request"}
	protocolMethodNotFound      = protocolError{code: -32601, message: "method not found"}
	protocolInvalidParams       = protocolError{code: -32602, message: "invalid params"}
	protocolServerBusy          = protocolError{code: -32000, message: "server busy"}
	protocolSessionRequestLimit = protocolError{code: -32000, message: "session request limit exceeded"}
)

func encodeProtocolErrorNull(protocolErr protocolError) []byte {
	return appendProtocolError(nil, "null", protocolErr)
}

func encodeProtocolError(id RequestID, protocolErr protocolError) []byte {
	return appendProtocolError(nil, id.RawJSON(), protocolErr)
}

func appendProtocolError(dst []byte, rawID string, protocolErr protocolError) []byte {
	dst = append(dst, `{"jsonrpc":"2.0","id":`...)
	dst = append(dst, rawID...)
	dst = append(dst, `,"error":{"code":`...)
	dst = strconv.AppendInt(dst, int64(protocolErr.code), 10)
	dst = append(dst, `,"message":`...)
	dst = appendJSONString(dst, protocolErr.message)
	dst = append(dst, '}', '}', '\n')
	return dst
}

func encodeLifecycleDecision(decision lifecycleDecision) ([]byte, error) {
	switch decision.action {
	case lifecycleInitialize:
		if decision.requestID.RawJSON() == "" ||
			decision.initialize.protocolVersion == "" ||
			decision.initialize.serverName == "" ||
			decision.initialize.serverVersion == "" ||
			!decision.initialize.tools {
			return nil, errors.New("mcpstdio: invalid initialize response")
		}
		dst := appendResultPrefix(nil, decision.requestID)
		dst = append(dst, `{"protocolVersion":`...)
		dst = appendJSONString(dst, decision.initialize.protocolVersion)
		dst = append(dst, `,"capabilities":{"tools":{}},"serverInfo":{"name":`...)
		dst = appendJSONString(dst, decision.initialize.serverName)
		dst = append(dst, `,"version":`...)
		dst = appendJSONString(dst, decision.initialize.serverVersion)
		dst = append(dst, `},"instructions":`...)
		dst = appendJSONString(dst, decision.initialize.instructions)
		dst = append(dst, '}', '}', '\n')
		return dst, nil
	case lifecyclePing:
		if decision.requestID.RawJSON() == "" {
			return nil, errors.New("mcpstdio: ping response lacks request id")
		}
		dst := appendResultPrefix(nil, decision.requestID)
		dst = append(dst, '{', '}', '}', '\n')
		return dst, nil
	case lifecycleToolsList:
		if decision.requestID.RawJSON() == "" {
			return nil, errors.New("mcpstdio: tools/list response lacks request id")
		}
		result, err := toolsListResultJSON()
		if err != nil {
			return nil, fmt.Errorf("mcpstdio: encode tools/list catalog: %w", err)
		}
		dst := appendResultPrefix(nil, decision.requestID)
		dst = append(dst, result...)
		dst = append(dst, '}', '\n')
		return dst, nil
	case lifecycleMethodNotFound:
		if decision.requestID.RawJSON() == "" {
			return nil, errors.New("mcpstdio: method-not-found response lacks request id")
		}
		return encodeProtocolError(decision.requestID, protocolMethodNotFound), nil
	case lifecycleRejected:
		if decision.output == "" {
			return nil, errors.New("mcpstdio: rejected response lacks output")
		}
		return append([]byte(nil), decision.output...), nil
	default:
		return nil, errors.New("mcpstdio: lifecycle action has no immediate response")
	}
}

func encodeToolResult(id RequestID, result api.Result) ([]byte, error) {
	if id.RawJSON() == "" {
		return nil, errors.New("mcpstdio: tool response lacks request id")
	}
	payload, err := encodeToolResultPayload(result)
	if err != nil {
		return nil, err
	}
	dst := appendResultPrefix(nil, id)
	dst = append(dst, payload...)
	dst = append(dst, '}', '\n')
	return dst, nil
}

func encodeToolResultPayload(result api.Result) ([]byte, error) {
	if err := result.Validate(); err != nil {
		return nil, fmt.Errorf("mcpstdio: invalid tool result: %w", err)
	}

	var dst []byte
	switch result.Kind() {
	case api.ResultText:
		text, ok := result.Text()
		if !ok {
			return nil, errors.New("mcpstdio: validated text result is inaccessible")
		}
		dst = append(dst, `{"content":[{"type":"text","text":`...)
		dst = appendJSONString(dst, text)
		dst = append(dst, '}', ']')
		if result.IsError() {
			dst = append(dst, `,"isError":true`...)
		}
		dst = append(dst, '}')
		return dst, nil
	case api.ResultCWD:
		cwdID, ok := result.CWDID()
		if !ok {
			return nil, errors.New("mcpstdio: validated cwd result is inaccessible")
		}
		dst = append(dst, `{"content":[{"type":"text","text":"cwd_id=`...)
		dst = strconv.AppendUint(dst, cwdID, 10)
		dst = append(dst, `\n"}],"structuredContent":{"cwd_id":`...)
		dst = strconv.AppendUint(dst, cwdID, 10)
		dst = append(dst, '}', '}')
		return dst, nil
	default:
		return nil, errors.New("mcpstdio: validated result has unknown kind")
	}
}

func appendResultPrefix(dst []byte, id RequestID) []byte {
	dst = append(dst, `{"jsonrpc":"2.0","id":`...)
	dst = append(dst, id.RawJSON()...)
	dst = append(dst, `,"result":`...)
	return dst
}

func appendJSONString(dst []byte, value string) []byte {
	const hex = "0123456789abcdef"
	dst = append(dst, '"')
	start := 0
	for index := 0; index < len(value); index++ {
		byteValue := value[index]
		if byteValue >= 0x20 && byteValue != '"' && byteValue != '\\' {
			continue
		}
		dst = append(dst, value[start:index]...)
		switch byteValue {
		case '"', '\\':
			dst = append(dst, '\\', byteValue)
		case '\b':
			dst = append(dst, `\b`...)
		case '\f':
			dst = append(dst, `\f`...)
		case '\n':
			dst = append(dst, `\n`...)
		case '\r':
			dst = append(dst, `\r`...)
		case '\t':
			dst = append(dst, `\t`...)
		default:
			dst = append(dst, `\u00`...)
			dst = append(dst, hex[byteValue>>4], hex[byteValue&0x0f])
		}
		start = index + 1
	}
	dst = append(dst, value[start:]...)
	dst = append(dst, '"')
	return dst
}
