package jsonwire

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestScanDocumentAcceptsEveryTopLevelKind(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
		kind ValueKind
	}{
		{name: "object", raw: ` {"a":[1,true,null]} `, kind: Object},
		{name: "array", raw: "\n[{},false,\"x\"]\t", kind: Array},
		{name: "string", raw: `"value"`, kind: String},
		{name: "number", raw: `-12.5e+3`, kind: Number},
		{name: "true", raw: `true`, kind: True},
		{name: "false", raw: `false`, kind: False},
		{name: "null", raw: `null`, kind: Null},
	} {
		t.Run(test.name, func(t *testing.T) {
			kind, span, err := scanDocument([]byte(test.raw), protocolTestLimits(), ValidateAll)
			if err != nil {
				t.Fatalf("scanDocument() error = %v", err)
			}
			if kind != test.kind || span.Start < 0 || span.End > len(test.raw) || span.Start >= span.End {
				t.Fatalf("scanDocument() = (%q, %#v), want kind %q and bounded non-empty span", kind, span, test.kind)
			}
			if strings.TrimSpace(test.raw) != test.raw[span.Start:span.End] {
				t.Fatalf("span bytes = %q, want exact JSON value %q", test.raw[span.Start:span.End], strings.TrimSpace(test.raw))
			}
		})
	}
}

func TestScanDocumentRejectsMalformedStructure(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ``},
		{name: "whitespace only", raw: " \n\t"},
		{name: "unterminated object", raw: `{`},
		{name: "unterminated array", raw: `[`},
		{name: "object key not string", raw: `{1:2}`},
		{name: "missing colon", raw: `{"a" 1}`},
		{name: "missing object value", raw: `{"a":}`},
		{name: "leading object comma", raw: `{,"a":1}`},
		{name: "trailing object comma", raw: `{"a":1,}`},
		{name: "missing object comma", raw: `{"a":1 "b":2}`},
		{name: "leading array comma", raw: `[,1]`},
		{name: "trailing array comma", raw: `[1,]`},
		{name: "missing array comma", raw: `[1 2]`},
		{name: "mismatched object close", raw: `[}`},
		{name: "mismatched array close", raw: `{]`},
		{name: "trailing bytes", raw: `{} false`},
		{name: "extra close", raw: `[]]`},
		{name: "invalid literal", raw: `truth`},
		{name: "leading zero", raw: `01`},
		{name: "minus only", raw: `-`},
		{name: "fraction missing digits", raw: `1.`},
		{name: "exponent missing digits", raw: `1e+`},
	} {
		t.Run(test.name, func(t *testing.T) {
			requireScanErrorKind(t, []byte(test.raw), protocolTestLimits(), KindSyntax)
		})
	}
}

func TestScanDocumentDepthBoundary(t *testing.T) {
	limits := protocolTestLimits()
	requireScanOK(t, nestedArrays(64), limits)
	requireScanErrorKind(t, nestedArrays(65), limits, KindResource)
}

func TestScanDocumentObjectMemberBoundary(t *testing.T) {
	limits := protocolTestLimits()
	requireScanOK(t, objectWithMembers(4_096), limits)
	requireScanErrorKind(t, objectWithMembers(4_097), limits, KindResource)
}

func TestScanDocumentTotalContainerItemBoundary(t *testing.T) {
	limits := protocolTestLimits()
	requireScanOK(t, arrayWithItems(65_536), limits)
	requireScanErrorKind(t, arrayWithItems(65_537), limits, KindResource)
}

func TestScanDocumentKeyByteBoundary(t *testing.T) {
	limits := protocolTestLimits()
	atMax := strings.Repeat("😀", 1_024)
	requireScanOK(t, []byte(`{"`+atMax+`":0}`), limits)
	requireScanErrorKind(t, []byte(`{"`+atMax+`a":0}`), limits, KindResource)
}

func TestScanDocumentStringByteBoundary(t *testing.T) {
	limits := protocolTestLimits()
	atMax := strings.Repeat("x", 262_144)
	requireScanOK(t, []byte(`"`+atMax+`"`), limits)
	requireScanErrorKind(t, []byte(`"`+atMax+`x"`), limits, KindResource)
}

func TestScanDocumentNumberRawByteBoundary(t *testing.T) {
	limits := protocolTestLimits()
	requireScanOK(t, []byte(strings.Repeat("1", 256)), limits)
	requireScanErrorKind(t, []byte(strings.Repeat("1", 257)), limits, KindResource)
}

func TestScanDocumentUsesExplicitStack(t *testing.T) {
	limits := protocolTestLimits()
	limits.MaxDepth = 10_000
	requireScanOK(t, nestedArrays(10_000), limits)
}

func protocolTestLimits() Limits {
	return Limits{
		MaxDepth:          64,
		MaxObjectMembers:  4_096,
		MaxContainerItems: 65_536,
		MaxKeyBytes:       4_096,
		MaxStringBytes:    262_144,
		MaxNumberRawBytes: 256,
	}
}

func requireScanOK(t *testing.T, raw []byte, limits Limits) {
	t.Helper()
	if _, _, err := scanDocument(raw, limits, ValidateAll); err != nil {
		t.Fatalf("scanDocument(%d bytes) error = %v", len(raw), err)
	}
}

func requireScanErrorKind(t *testing.T, raw []byte, limits Limits, want ErrorKind) {
	t.Helper()
	_, _, err := scanDocument(raw, limits, ValidateAll)
	if err == nil {
		t.Fatalf("scanDocument(%d bytes) error = nil", len(raw))
	}
	var validationError *ValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("error type = %T, want *ValidationError", err)
	}
	if validationError.Kind() != want {
		t.Fatalf("error kind = %q, want %q", validationError.Kind(), want)
	}
	if validationError.Position() < 0 || validationError.Position() > len(raw) {
		t.Fatalf("error position = %d outside input of %d bytes", validationError.Position(), len(raw))
	}
}

func nestedArrays(depth int) []byte {
	return []byte(strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth))
}

func objectWithMembers(count int) []byte {
	var builder strings.Builder
	builder.WriteByte('{')
	for i := 0; i < count; i++ {
		if i > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(`"k`)
		builder.WriteString(strconv.Itoa(i))
		builder.WriteString(`":0`)
	}
	builder.WriteByte('}')
	return []byte(builder.String())
}

func arrayWithItems(count int) []byte {
	if count == 0 {
		return []byte(`[]`)
	}
	return []byte("[" + strings.Repeat("0,", count-1) + "0]")
}
