package jsonwire

// DecodeStringArray strictly decodes one JSON array containing only strings.
func DecodeStringArray(raw []byte) ([]string, error) {
	bound := uint64(len(raw)) + 1
	result, err := scanArrayDocumentDetailed(raw, Limits{
		MaxDepth:          bound,
		MaxObjectMembers:  bound,
		MaxContainerItems: bound,
		MaxKeyBytes:       bound,
		MaxStringBytes:    bound,
		MaxNumberRawBytes: bound,
	}, ValidateAll)
	if err != nil || result.Kind != Array {
		return nil, stringArrayError{}
	}

	values := make([]string, 0, len(result.Elements))
	for _, element := range result.Elements {
		if element.Kind != String {
			return nil, stringArrayError{}
		}
		value, span, err := decodeJSONString(raw, element.Span.Start)
		if err != nil || span != element.Span {
			return nil, stringArrayError{}
		}
		values = append(values, value)
	}
	return values, nil
}

type stringArrayError struct{}

func (stringArrayError) Error() string {
	return "invalid JSON string array"
}
