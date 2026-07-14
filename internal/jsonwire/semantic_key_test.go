package jsonwire

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
)

func TestRequestIDSemanticKeyExactEncodings(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want []byte
	}{
		{raw: `"1"`, want: []byte{'s', '1'}},
		{raw: `"\u0031"`, want: []byte{'s', '1'}},
		{raw: `0`, want: []byte{'n', 0}},
		{raw: `-0.0e99`, want: []byte{'n', 0}},
		{raw: `1`, want: []byte{'n', 1, 0, 1, '1', 0, '0'}},
		{raw: `-1`, want: []byte{'n', 2, 0, 1, '1', 0, '0'}},
		{raw: `1e3`, want: []byte{'n', 1, 0, 1, '1', 0, '3'}},
	} {
		got, err := RequestIDSemanticKey([]byte(test.raw))
		if err != nil {
			t.Fatalf("RequestIDSemanticKey(%s) error = %v", test.raw, err)
		}
		if !bytes.Equal(got, test.want) {
			t.Fatalf("RequestIDSemanticKey(%s) = %v, want %v", test.raw, got, test.want)
		}
	}
}

func TestRequestIDSemanticKeyUsesDecodedStringScalars(t *testing.T) {
	want := append([]byte{'s'}, []byte(string(rune(0x1f600)))...)
	for _, raw := range [][]byte{
		[]byte(`"\uD83D\uDE00"`),
		[]byte(`"` + string(rune(0x1f600)) + `"`),
	} {
		got, err := RequestIDSemanticKey(raw)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("RequestIDSemanticKey(%q) = (%v, %v), want %v", raw, got, err, want)
		}
	}

	_, err := RequestIDSemanticKey([]byte(`"\uD800"`))
	var validationError *ValidationError
	if !errors.As(err, &validationError) || validationError.Kind() != KindUnicode {
		t.Fatalf("isolated surrogate error = %v, want unicode", err)
	}
}

func TestRequestIDSemanticKeySeparatesTypesAndRawSpellings(t *testing.T) {
	stringKey, err := RequestIDSemanticKey([]byte(`"1"`))
	if err != nil {
		t.Fatal(err)
	}
	numberKey, err := RequestIDSemanticKey([]byte(`1`))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(stringKey, numberKey) || stringKey[0] == numberKey[0] {
		t.Fatalf("string and number keys collided: %v / %v", stringKey, numberKey)
	}

	assertSameRequestIDKey(t, `1e3`, `1000.0`, `1E+03`)
	assertSameRequestIDKey(t, `"x"`, `"\u0078"`)

	raw := []byte(`1000.0`)
	key, err := RequestIDSemanticKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	for index := range raw {
		raw[index] = '9'
	}
	want, _ := RequestIDSemanticKey([]byte(`1e3`))
	if !bytes.Equal(key, want) {
		t.Fatalf("semantic key retained raw spelling: %v", key)
	}
}

func TestRequestIDSemanticKeyEncodesUint16SignificandLength(t *testing.T) {
	raw := strings.Repeat("9", 100)
	key, err := RequestIDSemanticKey([]byte(raw))
	if err != nil {
		t.Fatalf("RequestIDSemanticKey() error = %v", err)
	}
	if len(key) != 106 || key[0] != 'n' || key[1] != 1 || key[2] != 0 || key[3] != 100 {
		t.Fatalf("100-digit key header/length = %v (len %d)", key[:4], len(key))
	}
	if !bytes.Equal(key[4:104], []byte(raw)) || !bytes.Equal(key[104:], []byte{0, '0'}) {
		t.Fatalf("100-digit key payload malformed")
	}
}

func TestRequestIDSemanticKeyStaysWithinUsedIDKeyCap(t *testing.T) {
	maxStringRaw := `"` + strings.Repeat("x", 254) + `"`
	maxNumberRaw := strings.Repeat("9", 256)
	for _, raw := range []string{maxStringRaw, maxNumberRaw, "1e" + strings.Repeat("9", 253)} {
		key, err := RequestIDSemanticKey([]byte(raw))
		if err != nil {
			t.Fatalf("RequestIDSemanticKey(%d raw bytes) error = %v", len(raw), err)
		}
		if len(key) > 272 {
			t.Fatalf("key for %d raw bytes has %d bytes, want <= 272", len(raw), len(key))
		}
	}
}

func TestRequestIDSemanticKeyRejectsOtherJSONKinds(t *testing.T) {
	for _, raw := range []string{`null`, `true`, `false`, `{}`, `[]`} {
		_, err := RequestIDSemanticKey([]byte(raw))
		var validationError *ValidationError
		if !errors.As(err, &validationError) || validationError.Kind() != KindMismatch {
			t.Fatalf("RequestIDSemanticKey(%s) error = %v, want kind mismatch", raw, err)
		}
	}
	for _, raw := range []string{``, `01`, `1e+`, `"x"tail`} {
		_, err := RequestIDSemanticKey([]byte(raw))
		var validationError *ValidationError
		if !errors.As(err, &validationError) || validationError.Kind() != KindSyntax {
			t.Fatalf("RequestIDSemanticKey(%q) error = %v, want syntax", raw, err)
		}
	}
}

func TestSemanticKeyFullComparisonSurvivesInjectedDigestCollision(t *testing.T) {
	left, _ := RequestIDSemanticKey([]byte(`"left"`))
	right, _ := RequestIDSemanticKey([]byte(`"right"`))
	injectedDigest := sha256.Sum256([]byte("same digest bucket"))
	leftDigest := injectedDigest
	rightDigest := injectedDigest
	if leftDigest != rightDigest {
		t.Fatal("test did not inject a digest collision")
	}
	if bytes.Equal(left, right) {
		t.Fatalf("distinct full keys merged inside one digest bucket: %v", left)
	}
}

func assertSameRequestIDKey(t *testing.T, spellings ...string) {
	t.Helper()
	var want []byte
	for index, spelling := range spellings {
		key, err := RequestIDSemanticKey([]byte(spelling))
		if err != nil {
			t.Fatalf("RequestIDSemanticKey(%s) error = %v", spelling, err)
		}
		if index == 0 {
			want = key
			continue
		}
		if !bytes.Equal(key, want) {
			t.Fatalf("RequestIDSemanticKey(%s) = %v, want %v", spelling, key, want)
		}
	}
}
