package mcpstdio

import (
	"errors"

	"github.com/Dirard/mcp-file-tools/internal/config"
	"github.com/Dirard/mcp-file-tools/internal/jsonwire"
)

var errInvalidRequestID = errors.New("mcpstdio: invalid request id")

// SemanticIDKey is the immutable decoded-string or normalized-integer identity
// used for whole-session request tracking and cancellation. Its representation
// stays opaque so only validated request IDs can create non-zero keys.
type SemanticIDKey struct {
	encoded string
}

// RequestID preserves both the exact response scalar and its semantic identity.
type RequestID struct {
	rawJSON     string
	semanticKey SemanticIDKey
}

// RawJSON returns the byte-exact JSON scalar supplied by the requestor.
func (id RequestID) RawJSON() string {
	return id.rawJSON
}

// SemanticKey returns the immutable decoded or normalized request identity.
func (id RequestID) SemanticKey() SemanticIDKey {
	return id.semanticKey
}

func parseRequestID(root jsonwire.ObjectView) (RequestID, bool, error) {
	value, present := root.Value("id")
	if !present {
		return RequestID{}, false, nil
	}
	id, err := parseRequestIDValue(value)
	return id, true, err
}

func parseRequestIDValue(value jsonwire.ValueView) (RequestID, error) {
	raw := value.Bytes()
	if len(raw) == 0 || uint64(len(raw)) > config.RequestIDMaxRawBytes {
		return RequestID{}, errInvalidRequestID
	}

	var key []byte
	switch value.Kind() {
	case jsonwire.String:
		var err error
		key, err = jsonwire.RequestIDSemanticKey(raw)
		if err != nil {
			return RequestID{}, errInvalidRequestID
		}
	case jsonwire.Number:
		decimal, err := jsonwire.ParseDecimal(raw)
		if err != nil || !decimal.IsInteger() {
			return RequestID{}, errInvalidRequestID
		}
		key, err = decimal.SemanticKey(nil)
		if err != nil {
			return RequestID{}, errInvalidRequestID
		}
	default:
		return RequestID{}, errInvalidRequestID
	}
	if len(key) == 0 || uint64(len(key)) > config.UsedIDKeyMaxBytes {
		return RequestID{}, errInvalidRequestID
	}
	return RequestID{
		rawJSON:     string(raw),
		semanticKey: SemanticIDKey{encoded: string(key)},
	}, nil
}
