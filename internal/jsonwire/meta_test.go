package jsonwire

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestValidateMetaAcceptsExactTopLevelKeyGrammar(t *testing.T) {
	for _, key := range []string{
		"", "a", "A", "0", "name", "a-b", "a_b", "a.b",
		"com.example/name", "com.example/", "a-b.c9/x_y.z-1",
		"io.modelcontextprotocol/name", "io.mcp/name",
	} {
		raw := []byte(`{"` + key + `":0}`)
		if err := ValidateMeta(raw); err != nil {
			t.Fatalf("ValidateMeta key %q: %v", key, err)
		}
	}
	if err := ValidateMeta([]byte(`{"\u0061":0}`)); err != nil {
		t.Fatalf("escaped ASCII key rejected: %v", err)
	}
}

func TestValidateMetaRejectsInvalidTopLevelKeyGrammar(t *testing.T) {
	for _, key := range []string{
		"/name", ".a/name", "a./name", "a..b/name", "1a/name", "a-/name", "a_b/name",
		"a/b/c", "-name", "_name", ".name", "name-", "name_", "name.",
	} {
		raw := []byte(`{"` + key + `":0}`)
		requireMetaErrorKind(t, raw, KindMismatch)
	}
	nonASCII := []byte(`{"` + string(rune(0x00e9)) + `":0}`)
	requireMetaErrorKind(t, nonASCII, KindMismatch)
}

func TestValidateMetaProgressTokenKinds(t *testing.T) {
	for _, raw := range []string{
		`{"progressToken":"token"}`,
		`{"progressToken":0}`,
		`{"progressToken":-12.5e3}`,
		`{"progressToken":` + strings.Repeat("9", 256) + `}`,
	} {
		if err := ValidateMeta([]byte(raw)); err != nil {
			t.Fatalf("ValidateMeta(%s): %v", raw, err)
		}
	}
	for _, raw := range []string{
		`{"progressToken":true}`,
		`{"progressToken":null}`,
		`{"progressToken":{}}`,
		`{"progressToken":[]}`,
	} {
		requireMetaErrorKind(t, []byte(raw), KindMismatch)
	}
}

func TestValidateMetaTreatsNestedExtensionValuesAsOpaqueButStrict(t *testing.T) {
	valid := []byte(`{"vendor":{"not/metadata/grammar":[true,null,{"unicode-` + string(rune(0x00e9)) + `":"value"}]}}`)
	if err := ValidateMeta(valid); err != nil {
		t.Fatalf("opaque nested value rejected: %v", err)
	}
	requireMetaErrorKind(t, []byte(`{"vendor":{"x":1,"x":2}}`), KindDuplicate)
	requireMetaErrorKind(t, []byte(`{"vendor":"\uD800"}`), KindUnicode)
}

func TestValidateMetaRawByteBoundary(t *testing.T) {
	base := []byte(`{"k":0}`)
	atMax := append(append([]byte(nil), base...), bytes.Repeat([]byte{' '}, 16_384-len(base))...)
	if err := ValidateMeta(atMax); err != nil {
		t.Fatalf("16,384-byte metadata rejected: %v", err)
	}
	requireMetaErrorKind(t, append(atMax, ' '), KindResource)
}

func TestValidateMetaDepthBoundary(t *testing.T) {
	if err := ValidateMeta(metaWithNestedArrays(15)); err != nil {
		t.Fatalf("depth 16 metadata rejected: %v", err)
	}
	requireMetaErrorKind(t, metaWithNestedArrays(16), KindResource)
}

func TestValidateMetaContainerItemBoundary(t *testing.T) {
	if err := ValidateMeta(metaWithArrayItems(1_023)); err != nil {
		t.Fatalf("1,024-item metadata rejected: %v", err)
	}
	requireMetaErrorKind(t, metaWithArrayItems(1_024), KindResource)
}

func TestValidateMetaScalarByteBoundaries(t *testing.T) {
	keyAtMax := strings.Repeat("k", 256)
	if err := ValidateMeta([]byte(`{"` + keyAtMax + `":0}`)); err != nil {
		t.Fatalf("256-byte key rejected: %v", err)
	}
	requireMetaErrorKind(t, []byte(`{"`+keyAtMax+`k":0}`), KindResource)

	stringAtMax := strings.Repeat("x", 4_096)
	if err := ValidateMeta([]byte(`{"k":"` + stringAtMax + `"}`)); err != nil {
		t.Fatalf("4,096-byte string rejected: %v", err)
	}
	requireMetaErrorKind(t, []byte(`{"k":"`+stringAtMax+`x"}`), KindResource)

	numberAtMax := strings.Repeat("9", 256)
	if err := ValidateMeta([]byte(`{"k":` + numberAtMax + `}`)); err != nil {
		t.Fatalf("256-byte number rejected: %v", err)
	}
	requireMetaErrorKind(t, []byte(`{"k":`+numberAtMax+`9}`), KindResource)
}

func TestValidateMetaRequiresObject(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `"value"`, `1`} {
		requireMetaErrorKind(t, []byte(raw), KindMismatch)
	}
}

func requireMetaErrorKind(t *testing.T, raw []byte, want ErrorKind) {
	t.Helper()
	err := ValidateMeta(raw)
	if err == nil {
		t.Fatalf("ValidateMeta(%d bytes) error = nil", len(raw))
	}
	var validationError *ValidationError
	if !errors.As(err, &validationError) || validationError.Kind() != want {
		t.Fatalf("ValidateMeta(%d bytes) error = %v, want %s", len(raw), err, want)
	}
}

func metaWithNestedArrays(depth int) []byte {
	return []byte(`{"k":` + strings.Repeat("[", depth) + `0` + strings.Repeat("]", depth) + `}`)
}

func metaWithArrayItems(count int) []byte {
	var builder strings.Builder
	builder.WriteString(`{"k":[`)
	for index := 0; index < count; index++ {
		if index != 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(strconv.Itoa(index))
	}
	builder.WriteString(`]}`)
	return []byte(builder.String())
}
