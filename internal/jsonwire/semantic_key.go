package jsonwire

// RequestIDSemanticKey encodes one already-bounded raw JSON string or number
// scalar. The returned key owns its bytes and never retains the raw spelling.
func RequestIDSemanticKey(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return nil, newValidationError(KindSyntax, 0)
	}
	bound := uint64(len(raw)) + 1
	kind, span, err := scanDocument(raw, Limits{
		MaxDepth:          bound,
		MaxObjectMembers:  bound,
		MaxContainerItems: bound,
		MaxKeyBytes:       bound,
		MaxStringBytes:    bound,
		MaxNumberRawBytes: bound,
	}, ValidateAll)
	if err != nil {
		return nil, err
	}
	switch kind {
	case String:
		decoded, span, err := decodeJSONString(raw, 0)
		if err != nil {
			return nil, err
		}
		if span.End != len(raw) {
			return nil, newValidationError(KindSyntax, span.End)
		}
		key := make([]byte, 1, len(decoded)+1)
		key[0] = 's'
		return append(key, decoded...), nil
	case Number:
		decimal, err := ParseDecimal(raw)
		if err != nil {
			return nil, err
		}
		return decimal.SemanticKey(nil)
	default:
		return nil, newValidationError(KindMismatch, span.Start)
	}
}
