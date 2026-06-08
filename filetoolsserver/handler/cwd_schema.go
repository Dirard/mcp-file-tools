package handler

import "github.com/google/jsonschema-go/jsonschema"

// SetCwdOutputSchema keeps the success contract exact while still documenting
// structured tool errors.
func SetCwdOutputSchema() *jsonschema.Schema {
	falseSchema := &jsonschema.Schema{Not: &jsonschema.Schema{}}
	stringSchema := func() *jsonschema.Schema { return &jsonschema.Schema{Type: "string"} }
	boolSchema := func() *jsonschema.Schema { return &jsonschema.Schema{Type: "boolean"} }
	objectSchema := func() *jsonschema.Schema { return &jsonschema.Schema{Type: "object"} }
	minimum := float64(1)
	maximum := float64(maxCwdID)
	actionHintRef := &jsonschema.Schema{Ref: "#/$defs/action_hint"}

	return &jsonschema.Schema{
		Type: "object",
		OneOf: []*jsonschema.Schema{
			{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"cwd_id": &jsonschema.Schema{
						Type:        "integer",
						Minimum:     &minimum,
						Maximum:     &maximum,
						Description: "Small cwd id returned by set_cwd.",
					},
				},
				Required:             []string{"cwd_id"},
				AdditionalProperties: falseSchema,
			},
			{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"error":       stringSchema(),
					"error_code":  stringSchema(),
					"action_hint": actionHintRef,
				},
				Required:             []string{"error"},
				AdditionalProperties: falseSchema,
			},
		},
		Defs: map[string]*jsonschema.Schema{
			"action_hint": &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"safe_to_retry":                 boolSchema(),
					"recommended_next_tool":         stringSchema(),
					"recommended_next_input":        objectSchema(),
					"recommended_next_input_policy": stringSchema(),
					"reason":                        stringSchema(),
				},
				Required:             []string{"safe_to_retry"},
				AdditionalProperties: falseSchema,
			},
		},
	}
}
