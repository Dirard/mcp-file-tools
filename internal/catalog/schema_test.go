package catalog

import (
	"bytes"
	"encoding/json"
	"io"
	"math/big"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

const expectedSetCWDInputSchema = `{"type":"object","properties":{"directory":{"type":"string","minLength":1,"maxLength":4096,"x-utf8MaxBytes":4096,"pattern":"^(/|[A-Za-z]:[\\\\/]).*","description":"Absolute local directory; 1..4096 UTF-8 bytes."}},"required":["directory"],"additionalProperties":false}`

const expectedProjectInputSchema = `{"oneOf":[{"type":"object","properties":{"cwd_id":{"type":"integer","minimum":1,"maximum":9007199254740991,"description":"Registered cwd_id."},"path":{"type":"string","minLength":1,"maxLength":4096,"x-utf8MaxBytes":4096,"default":".","description":"Relative subtree root; 1..4096 UTF-8 bytes."},"depth":{"type":"integer","minimum":0,"maximum":8,"default":2,"description":"Maximum traversal depth."},"limit":{"type":"integer","minimum":1,"maximum":1000,"default":200,"description":"Maximum result rows."},"include_ignored":{"type":"boolean","default":false,"description":"Include ordinary ignored directories."}},"required":["cwd_id"],"additionalProperties":false},{"type":"object","properties":{"cwd_id":{"type":"integer","minimum":1,"maximum":9007199254740991,"description":"Registered cwd_id."},"cursor":{"type":"string","minLength":22,"maxLength":22,"pattern":"^[A-Za-z0-9_-]{22}$","description":"Opaque 22-character continuation token."}},"required":["cwd_id","cursor"],"additionalProperties":false}]}`

const expectedSearchInputSchema = `{"oneOf":[{"type":"object","properties":{"cwd_id":{"type":"integer","minimum":1,"maximum":9007199254740991,"description":"Registered cwd_id."},"query":{"type":"string","minLength":1,"maxLength":4096,"x-utf8MaxBytes":4096,"description":"Search expression; 1..4096 UTF-8 bytes."},"mode":{"type":"string","enum":["file","text","symbol"],"default":"text","description":"Search family."},"path":{"type":"string","minLength":1,"maxLength":4096,"x-utf8MaxBytes":4096,"default":".","description":"Relative search root; 1..4096 UTF-8 bytes."},"glob":{"type":"string","minLength":1,"maxLength":4096,"x-utf8MaxBytes":4096,"description":"Optional file glob; 1..4096 UTF-8 bytes."},"regex":{"type":"boolean","default":false,"description":"Interpret text or symbol query as RE2."},"ignore_case":{"type":"boolean","default":false,"description":"Use Unicode case-fold matching."},"context":{"type":"integer","minimum":0,"maximum":20,"default":0,"description":"Context lines for text mode."},"include_ignored":{"type":"boolean","default":false,"description":"Include ordinary ignored directories."},"limit":{"type":"integer","minimum":1,"maximum":1000,"default":50,"description":"Maximum result rows."}},"required":["cwd_id","query"],"additionalProperties":false,"allOf":[{"if":{"properties":{"mode":{"const":"file"}},"required":["mode"]},"then":{"not":{"anyOf":[{"required":["glob"]},{"required":["regex"]},{"required":["context"]}]}}},{"if":{"properties":{"mode":{"const":"symbol"}},"required":["mode"]},"then":{"not":{"required":["context"]}}}]},{"type":"object","properties":{"cwd_id":{"type":"integer","minimum":1,"maximum":9007199254740991,"description":"Registered cwd_id."},"cursor":{"type":"string","minLength":22,"maxLength":22,"pattern":"^[A-Za-z0-9_-]{22}$","description":"Opaque 22-character continuation token."}},"required":["cwd_id","cursor"],"additionalProperties":false}]}`

const expectedReadInputSchema = `{"oneOf":[{"type":"object","properties":{"cwd_id":{"type":"integer","minimum":1,"maximum":9007199254740991,"description":"Registered cwd_id."},"files":{"type":"array","minItems":1,"maxItems":24,"description":"Files to read."},"view":{"type":"string","enum":["source","outline"],"default":"source","description":"Output representation."},"max_bytes":{"type":"integer","minimum":4096,"maximum":32768,"default":32768,"description":"Maximum aggregate TextContent bytes."}},"required":["cwd_id","files"],"additionalProperties":false,"allOf":[{"if":{"properties":{"view":{"const":"outline"}},"required":["view"]},"then":{"properties":{"files":{"items":{"type":"object","properties":{"path":{"type":"string","minLength":1,"maxLength":4096,"x-utf8MaxBytes":4096,"description":"Relative file path; 1..4096 UTF-8 bytes."}},"required":["path"],"additionalProperties":false}}}},"else":{"properties":{"files":{"items":{"type":"object","properties":{"path":{"type":"string","minLength":1,"maxLength":4096,"x-utf8MaxBytes":4096,"description":"Relative file path; 1..4096 UTF-8 bytes."},"start":{"type":"integer","minimum":1,"maximum":2147483647,"default":1,"description":"First source line."},"end":{"type":"integer","minimum":1,"maximum":2147483647,"description":"Last source line."}},"required":["path","end"],"additionalProperties":false}}}}}]},{"type":"object","properties":{"cwd_id":{"type":"integer","minimum":1,"maximum":9007199254740991,"description":"Registered cwd_id."},"cursor":{"type":"string","minLength":22,"maxLength":22,"pattern":"^[A-Za-z0-9_-]{22}$","description":"Opaque 22-character continuation token."}},"required":["cwd_id","cursor"],"additionalProperties":false}]}`

func TestInputSchemaSnapshots(t *testing.T) {
	expected := map[api.ToolName]string{
		api.ToolSetCWD:  expectedSetCWDInputSchema,
		api.ToolProject: expectedProjectInputSchema,
		api.ToolSearch:  expectedSearchInputSchema,
		api.ToolRead:    expectedReadInputSchema,
	}

	for _, name := range api.OrderedToolNames() {
		t.Run(string(name), func(t *testing.T) {
			definition, ok := Lookup(name)
			if !ok {
				t.Fatalf("Lookup(%q) not found", name)
			}
			want := expected[name]
			if got := string(definition.InputSchema); got != want {
				t.Fatalf("InputSchema snapshot mismatch:\n got %s\nwant %s", got, want)
			}
			if !json.Valid(definition.InputSchema) {
				t.Fatal("InputSchema is not valid JSON")
			}
			var compact bytes.Buffer
			if err := json.Compact(&compact, definition.InputSchema); err != nil {
				t.Fatalf("json.Compact() error = %v", err)
			}
			if !bytes.Equal(compact.Bytes(), definition.InputSchema) {
				t.Fatal("InputSchema is not canonical compact JSON")
			}
		})
	}
}

func TestInputSchemasAreClosedAndInputOnly(t *testing.T) {
	if _, exists := reflect.TypeOf(Definition{}).FieldByName("OutputSchema"); exists {
		t.Fatal("Definition exposes an output schema")
	}
	for _, name := range api.OrderedToolNames() {
		definition, _ := Lookup(name)
		schema := mustDecodeJSON(t, string(definition.InputSchema))
		walkSchemaContract(t, string(name), schema)
	}
}

func TestInputSchemaExamples(t *testing.T) {
	const cursor = "A7k3mP9qR2sT5uV8wX1yZw"
	assertSchemaExamples(t, api.ToolSetCWD,
		[]string{
			`{"directory":"/tmp/project"}`,
			`{"directory":"C:\\repo"}`,
		},
		[]string{
			`{}`,
			`{"directory":""}`,
			`{"directory":"relative"}`,
			`{"directory":null}`,
			`{"directory":"/tmp","extra":true}`,
		},
	)

	assertSchemaExamples(t, api.ToolProject,
		[]string{
			`{"cwd_id":1}`,
			`{"cwd_id":1.0,"path":".","depth":1e0,"limit":1000,"include_ignored":true}`,
			`{"cwd_id":9007199254740991,"cursor":"` + cursor + `"}`,
		},
		[]string{
			`{}`,
			`{"cwd_id":0}`,
			`{"cwd_id":9007199254740992}`,
			`{"cwd_id":1.5}`,
			`{"cwd_id":1,"depth":9}`,
			`{"cwd_id":1,"cursor":"` + cursor + `","path":"."}`,
			`{"cwd_id":1,"cursor":"short"}`,
			`{"cwd_id":1,"extra":true}`,
		},
	)

	assertSchemaExamples(t, api.ToolSearch,
		[]string{
			`{"cwd_id":1,"query":"needle"}`,
			`{"cwd_id":1.0,"query":"needle","mode":"text","glob":"*.go","regex":true,"ignore_case":true,"context":20,"include_ignored":true,"limit":1e3}`,
			`{"cwd_id":1,"query":"*.go","mode":"file","ignore_case":true,"path":"."}`,
			`{"cwd_id":1,"query":"Handler","mode":"symbol","glob":"*.go","regex":true}`,
			`{"cwd_id":1,"cursor":"` + cursor + `"}`,
		},
		[]string{
			`{"cwd_id":1}`,
			`{"cwd_id":1,"query":"*.go","mode":"file","glob":"*.go"}`,
			`{"cwd_id":1,"query":"*.go","mode":"file","regex":false}`,
			`{"cwd_id":1,"query":"*.go","mode":"file","context":0}`,
			`{"cwd_id":1,"query":"Handler","mode":"symbol","context":0}`,
			`{"cwd_id":1,"query":"needle","mode":"unknown"}`,
			`{"cwd_id":1,"query":"needle","limit":1.5}`,
			`{"cwd_id":1,"cursor":"` + cursor + `","query":"needle"}`,
		},
	)

	assertSchemaExamples(t, api.ToolRead,
		[]string{
			`{"cwd_id":1,"files":[{"path":"main.go","end":10}]}`,
			`{"cwd_id":1.0,"files":[{"path":"main.go","start":1.0,"end":2e0}],"view":"source","max_bytes":4.096e3}`,
			`{"cwd_id":1,"files":[{"path":"main.go"}],"view":"outline"}`,
			`{"cwd_id":1,"cursor":"` + cursor + `"}`,
			readExample(24, `{"path":"main.go","end":1}`),
		},
		[]string{
			`{"cwd_id":1,"files":[]}`,
			`{"cwd_id":1,"files":[{"path":"main.go"}]}`,
			`{"cwd_id":1,"files":[{"path":"main.go","end":1,"extra":true}],"view":"source"}`,
			`{"cwd_id":1,"files":[{"path":"main.go","start":1}],"view":"outline"}`,
			`{"cwd_id":1,"files":[{"path":"main.go","end":1}],"view":"outline"}`,
			`{"cwd_id":1,"files":[{"path":"main.go"}],"view":"source"}`,
			`{"cwd_id":1,"files":[{"path":"main.go","end":1}],"view":"unknown"}`,
			`{"cwd_id":1,"files":[{"path":"main.go","end":1}],"max_bytes":4096.5}`,
			`{"cwd_id":1,"cursor":"` + cursor + `","view":"source"}`,
			readExample(25, `{"path":"main.go","end":1}`),
		},
	)
}

func TestInputSchemaUTF8ByteLimits(t *testing.T) {
	directoryAtMax := "/" + strings.Repeat("😀", 1_023) + "abc"
	directoryTooLong := directoryAtMax + "a"
	stringAtMax := strings.Repeat("😀", 1_024)
	stringTooLong := stringAtMax + "a"

	for _, test := range []struct {
		name string
		tool api.ToolName
		raw  string
		want bool
	}{
		{name: "set_cwd directory at max", tool: api.ToolSetCWD, raw: `{"directory":` + mustJSONString(t, directoryAtMax) + `}`, want: true},
		{name: "set_cwd directory over max", tool: api.ToolSetCWD, raw: `{"directory":` + mustJSONString(t, directoryTooLong) + `}`, want: false},
		{name: "project path at max", tool: api.ToolProject, raw: `{"cwd_id":1,"path":` + mustJSONString(t, stringAtMax) + `}`, want: true},
		{name: "project path over max", tool: api.ToolProject, raw: `{"cwd_id":1,"path":` + mustJSONString(t, stringTooLong) + `}`, want: false},
		{name: "search query at max", tool: api.ToolSearch, raw: `{"cwd_id":1,"query":` + mustJSONString(t, stringAtMax) + `}`, want: true},
		{name: "search query over max", tool: api.ToolSearch, raw: `{"cwd_id":1,"query":` + mustJSONString(t, stringTooLong) + `}`, want: false},
		{name: "search path at max", tool: api.ToolSearch, raw: `{"cwd_id":1,"query":"q","path":` + mustJSONString(t, stringAtMax) + `}`, want: true},
		{name: "search path over max", tool: api.ToolSearch, raw: `{"cwd_id":1,"query":"q","path":` + mustJSONString(t, stringTooLong) + `}`, want: false},
		{name: "search glob at max", tool: api.ToolSearch, raw: `{"cwd_id":1,"query":"q","glob":` + mustJSONString(t, stringAtMax) + `}`, want: true},
		{name: "search glob over max", tool: api.ToolSearch, raw: `{"cwd_id":1,"query":"q","glob":` + mustJSONString(t, stringTooLong) + `}`, want: false},
		{name: "read source path at max", tool: api.ToolRead, raw: `{"cwd_id":1,"files":[{"path":` + mustJSONString(t, stringAtMax) + `,"end":1}]}`, want: true},
		{name: "read source path over max", tool: api.ToolRead, raw: `{"cwd_id":1,"files":[{"path":` + mustJSONString(t, stringTooLong) + `,"end":1}]}`, want: false},
		{name: "read outline path at max", tool: api.ToolRead, raw: `{"cwd_id":1,"files":[{"path":` + mustJSONString(t, stringAtMax) + `}],"view":"outline"}`, want: true},
		{name: "read outline path over max", tool: api.ToolRead, raw: `{"cwd_id":1,"files":[{"path":` + mustJSONString(t, stringTooLong) + `}],"view":"outline"}`, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			definition, ok := Lookup(test.tool)
			if !ok {
				t.Fatalf("Lookup(%q) not found", test.tool)
			}
			schema := mustDecodeJSON(t, string(definition.InputSchema)).(map[string]any)
			if got := schemaAcceptsJSON(schema, test.raw); got != test.want {
				t.Errorf("schema acceptance = %t, want %t", got, test.want)
			}
		})
	}
}

func assertSchemaExamples(t *testing.T, name api.ToolName, valid, invalid []string) {
	t.Helper()
	definition, ok := Lookup(name)
	if !ok {
		t.Fatalf("Lookup(%q) not found", name)
	}
	schemaValue := mustDecodeJSON(t, string(definition.InputSchema))
	schema, ok := schemaValue.(map[string]any)
	if !ok {
		t.Fatalf("%s schema root is %T, want object", name, schemaValue)
	}
	for _, raw := range valid {
		if !schemaAcceptsJSON(schema, raw) {
			t.Errorf("%s schema rejected valid example: %s", name, raw)
		}
	}
	for _, raw := range invalid {
		if schemaAcceptsJSON(schema, raw) {
			t.Errorf("%s schema accepted invalid example: %s", name, raw)
		}
	}
}

func walkSchemaContract(t *testing.T, path string, value any) {
	t.Helper()
	switch value := value.(type) {
	case map[string]any:
		if _, exists := value["outputSchema"]; exists {
			t.Errorf("%s contains outputSchema", path)
		}
		if _, exists := value["nullable"]; exists {
			t.Errorf("%s contains nullable", path)
		}
		if value["type"] == "null" {
			t.Errorf("%s permits null type", path)
		}
		if value["type"] == "object" && value["additionalProperties"] != false {
			t.Errorf("%s object is not closed", path)
		}
		if maximum, ok := schemaInteger(value, "maxLength"); ok && maximum == api.InputStringMaxBytes {
			byteMaximum, present := schemaInteger(value, "x-utf8MaxBytes")
			if !present || byteMaximum != api.InputStringMaxBytes {
				t.Errorf("%s maxLength=%d requires x-utf8MaxBytes=%d", path, maximum, api.InputStringMaxBytes)
			}
		}
		if enum, ok := value["enum"].([]any); ok {
			for _, item := range enum {
				if item == nil {
					t.Errorf("%s enum permits null", path)
				}
			}
		}
		for key, child := range value {
			walkSchemaContract(t, path+"."+key, child)
		}
	case []any:
		for _, child := range value {
			walkSchemaContract(t, path+"[]", child)
		}
	}
}

func schemaAcceptsJSON(schema map[string]any, raw string) bool {
	value, err := decodeJSON(raw)
	if err != nil {
		return false
	}
	return validAgainstSchema(schema, value)
}

func validAgainstSchema(schema map[string]any, value any) bool {
	if schemaType, ok := schema["type"].(string); ok && !matchesSchemaType(schemaType, value) {
		return false
	}
	if constant, exists := schema["const"]; exists && !reflect.DeepEqual(value, constant) {
		return false
	}
	if enum, ok := schema["enum"].([]any); ok {
		matched := false
		for _, candidate := range enum {
			if reflect.DeepEqual(value, candidate) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	if text, ok := value.(string); ok {
		length := utf8.RuneCountInString(text)
		if minimum, ok := schemaInteger(schema, "minLength"); ok && length < minimum {
			return false
		}
		if maximum, ok := schemaInteger(schema, "maxLength"); ok && length > maximum {
			return false
		}
		if maximum, ok := schemaInteger(schema, "x-utf8MaxBytes"); ok && len(text) > maximum {
			return false
		}
		if pattern, ok := schema["pattern"].(string); ok {
			compiled, err := regexp.Compile(pattern)
			if err != nil || !compiled.MatchString(text) {
				return false
			}
		}
	}

	if number, ok := value.(json.Number); ok {
		valueRat, ok := numberRat(number)
		if !ok {
			return false
		}
		if schema["type"] == "integer" && !valueRat.IsInt() {
			return false
		}
		if minimum, ok := schema["minimum"].(json.Number); ok {
			minimumRat, valid := numberRat(minimum)
			if !valid || valueRat.Cmp(minimumRat) < 0 {
				return false
			}
		}
		if maximum, ok := schema["maximum"].(json.Number); ok {
			maximumRat, valid := numberRat(maximum)
			if !valid || valueRat.Cmp(maximumRat) > 0 {
				return false
			}
		}
	}

	if object, ok := value.(map[string]any); ok {
		properties, _ := schema["properties"].(map[string]any)
		if required, ok := schema["required"].([]any); ok {
			for _, item := range required {
				name, valid := item.(string)
				if !valid {
					return false
				}
				if _, present := object[name]; !present {
					return false
				}
			}
		}
		for name, child := range object {
			propertySchema, known := properties[name]
			if !known {
				if schema["additionalProperties"] == false {
					return false
				}
				continue
			}
			propertyMap, valid := propertySchema.(map[string]any)
			if !valid || !validAgainstSchema(propertyMap, child) {
				return false
			}
		}
	}

	if array, ok := value.([]any); ok {
		if minimum, ok := schemaInteger(schema, "minItems"); ok && len(array) < minimum {
			return false
		}
		if maximum, ok := schemaInteger(schema, "maxItems"); ok && len(array) > maximum {
			return false
		}
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			for _, item := range array {
				if !validAgainstSchema(itemSchema, item) {
					return false
				}
			}
		}
	}

	if alternatives, ok := schema["oneOf"].([]any); ok {
		matches := 0
		for _, alternative := range alternatives {
			alternativeMap, valid := alternative.(map[string]any)
			if valid && validAgainstSchema(alternativeMap, value) {
				matches++
			}
		}
		if matches != 1 {
			return false
		}
	}
	if alternatives, ok := schema["anyOf"].([]any); ok {
		matched := false
		for _, alternative := range alternatives {
			alternativeMap, valid := alternative.(map[string]any)
			if valid && validAgainstSchema(alternativeMap, value) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if requirements, ok := schema["allOf"].([]any); ok {
		for _, requirement := range requirements {
			requirementMap, valid := requirement.(map[string]any)
			if !valid || !validAgainstSchema(requirementMap, value) {
				return false
			}
		}
	}
	if excluded, ok := schema["not"].(map[string]any); ok && validAgainstSchema(excluded, value) {
		return false
	}
	if condition, ok := schema["if"].(map[string]any); ok {
		if validAgainstSchema(condition, value) {
			if thenSchema, ok := schema["then"].(map[string]any); ok && !validAgainstSchema(thenSchema, value) {
				return false
			}
		} else if elseSchema, ok := schema["else"].(map[string]any); ok && !validAgainstSchema(elseSchema, value) {
			return false
		}
	}
	return true
}

func matchesSchemaType(schemaType string, value any) bool {
	switch schemaType {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return false
		}
		rat, valid := numberRat(number)
		return valid && rat.IsInt()
	default:
		return false
	}
}

func schemaInteger(schema map[string]any, key string) (int, bool) {
	number, ok := schema[key].(json.Number)
	if !ok {
		return 0, false
	}
	value, err := number.Int64()
	if err != nil || value < 0 {
		return 0, false
	}
	return int(value), true
}

func numberRat(number json.Number) (*big.Rat, bool) {
	value, ok := new(big.Rat).SetString(number.String())
	return value, ok
}

func mustDecodeJSON(t *testing.T, raw string) any {
	t.Helper()
	value, err := decodeJSON(raw)
	if err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	return value
}

func decodeJSON(raw string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, &trailingJSONError{}
		}
		return nil, err
	}
	return value, nil
}

type trailingJSONError struct{}

func (*trailingJSONError) Error() string {
	return "trailing JSON value"
}

func readExample(count int, item string) string {
	items := make([]string, count)
	for i := range items {
		items[i] = item
	}
	return `{"cwd_id":1,"files":[` + strings.Join(items, ",") + `]}`
}

func mustJSONString(t *testing.T, value string) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON string: %v", err)
	}
	return string(raw)
}
