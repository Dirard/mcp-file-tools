package jsonwire

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestParseDecimalRejectsInvalidJSONNumberGrammar(t *testing.T) {
	for _, raw := range []string{
		"", "+1", "-", "01", "-01", ".1", "1.", "1e", "1e+", "1e-", "1 2", "NaN", "Inf",
	} {
		_, err := ParseDecimal([]byte(raw))
		var validationError *ValidationError
		if !errors.As(err, &validationError) || validationError.Kind() != KindSyntax {
			t.Fatalf("ParseDecimal(%q) error = %v, want syntax", raw, err)
		}
	}
}

func TestDecimalNormalizesEquivalentSpellings(t *testing.T) {
	assertSameDecimalKey(t, "1e3", "1000.0", "1E+03")
	assertSameDecimalKey(t, "1.5", "15e-1", "150e-2")
	assertSameDecimalKey(t, "-0", "0", "0.0", "0e999999999999999999999999")

	decimal := mustParseDecimal(t, "1000.0")
	if !decimal.IsInteger() || decimal.CompareUint64(1_000) != 0 {
		t.Fatalf("1000.0 normalization = %#v", decimal)
	}
	value, ok := decimal.Uint64()
	if !ok || value != 1_000 {
		t.Fatalf("1000.0 Uint64() = (%d, %t)", value, ok)
	}
}

func TestDecimalIntegerPredicateUsesNormalizedScale(t *testing.T) {
	for _, raw := range []string{"0", "-0", "1", "1.0", "1e0", "10e-1", "1e3", "1000.000"} {
		if decimal := mustParseDecimal(t, raw); !decimal.IsInteger() {
			t.Fatalf("%s reported non-integer: %#v", raw, decimal)
		}
	}
	for _, raw := range []string{"1.5", "15e-1", "1e-1", "0.0001", "1000.0001"} {
		if decimal := mustParseDecimal(t, raw); decimal.IsInteger() {
			t.Fatalf("%s reported integer: %#v", raw, decimal)
		}
	}
}

func TestDecimalComparesAndConvertsOnlyAfterRangeProof(t *testing.T) {
	for _, test := range []struct {
		raw        string
		against    uint64
		comparison int
	}{
		{raw: "-1", against: 0, comparison: -1},
		{raw: "0", against: 0, comparison: 0},
		{raw: "0.1", against: 0, comparison: 1},
		{raw: "0.1", against: 1, comparison: -1},
		{raw: "1.5", against: 1, comparison: 1},
		{raw: "1.5", against: 2, comparison: -1},
		{raw: "9007199254740991", against: 9_007_199_254_740_991, comparison: 0},
		{raw: "18446744073709551615.0", against: math.MaxUint64, comparison: 0},
		{raw: "18446744073709551616", against: math.MaxUint64, comparison: 1},
	} {
		decimal := mustParseDecimal(t, test.raw)
		if got := decimal.CompareUint64(test.against); got != test.comparison {
			t.Fatalf("%s CompareUint64(%d) = %d, want %d", test.raw, test.against, got, test.comparison)
		}
	}

	max := mustParseDecimal(t, "18446744073709551615")
	if value, ok := max.Uint64(); !ok || value != math.MaxUint64 {
		t.Fatalf("max Uint64() = (%d, %t)", value, ok)
	}
	for _, raw := range []string{"-1", "1.5", "18446744073709551616", "1e999999999999999999999999"} {
		if value, ok := mustParseDecimal(t, raw).Uint64(); ok {
			t.Fatalf("%s Uint64() = (%d, true), want range/integer failure", raw, value)
		}
	}
}

func TestDecimalHandlesHugeExponentWithoutMachineConversion(t *testing.T) {
	hugeSignificand := mustParseDecimal(t, strings.Repeat("9", 256))
	hugeSignificandKey, err := hugeSignificand.SemanticKey(nil)
	if err != nil || len(hugeSignificandKey) > 272 || hugeSignificand.CompareUint64(math.MaxUint64) != 1 {
		t.Fatalf("huge significand key length = %d, comparison = %d, error = %v", len(hugeSignificandKey), hugeSignificand.CompareUint64(math.MaxUint64), err)
	}

	positiveRaw := "1e" + strings.Repeat("9", 253)
	positive := mustParseDecimal(t, positiveRaw)
	if !positive.IsInteger() || positive.CompareUint64(math.MaxUint64) != 1 {
		t.Fatalf("huge positive exponent = %#v", positive)
	}
	key, err := positive.SemanticKey(nil)
	if err != nil {
		t.Fatalf("huge positive SemanticKey() error = %v", err)
	}
	if len(key) > 272 {
		t.Fatalf("semantic key length = %d, want <= 272", len(key))
	}

	negative := mustParseDecimal(t, "1e-"+strings.Repeat("9", 200))
	if negative.IsInteger() || negative.CompareUint64(0) != 1 || negative.CompareUint64(1) != -1 {
		t.Fatalf("huge negative exponent = %#v", negative)
	}
}

func TestDecimalSemanticKeyIsCanonicalBoundedAndAppended(t *testing.T) {
	zeroKey, err := mustParseDecimal(t, "-0.0e999").SemanticKey([]byte("prefix"))
	if err != nil {
		t.Fatalf("zero SemanticKey() error = %v", err)
	}
	if !bytes.Equal(zeroKey, append([]byte("prefix"), 'n', 0)) {
		t.Fatalf("zero key = %v", zeroKey)
	}

	positive, _ := mustParseDecimal(t, "12.30").SemanticKey(nil)
	negative, _ := mustParseDecimal(t, "-12.30").SemanticKey(nil)
	if bytes.Equal(positive, negative) || len(positive) > 272 || len(negative) > 272 {
		t.Fatalf("signed semantic keys = %v / %v", positive, negative)
	}
	assertSameDecimalKey(t, "12.30", "123e-1")
}

func TestParseDecimalOwnsNormalizedState(t *testing.T) {
	raw := []byte("123.4500")
	decimal, err := ParseDecimal(raw)
	if err != nil {
		t.Fatalf("ParseDecimal() error = %v", err)
	}
	for index := range raw {
		raw[index] = '0'
	}
	if decimal.CompareUint64(123) != 1 || decimal.CompareUint64(124) != -1 {
		t.Fatalf("decimal changed after input mutation: %#v", decimal)
	}
}

func assertSameDecimalKey(t *testing.T, spellings ...string) {
	t.Helper()
	var want []byte
	for index, spelling := range spellings {
		key, err := mustParseDecimal(t, spelling).SemanticKey(nil)
		if err != nil {
			t.Fatalf("SemanticKey(%s) error = %v", spelling, err)
		}
		if index == 0 {
			want = key
			continue
		}
		if !bytes.Equal(key, want) {
			t.Fatalf("SemanticKey(%s) = %v, want %v", spelling, key, want)
		}
	}
}

func mustParseDecimal(t *testing.T, raw string) Decimal {
	t.Helper()
	decimal, err := ParseDecimal([]byte(raw))
	if err != nil {
		t.Fatalf("ParseDecimal(%q) error = %v", raw, err)
	}
	return decimal
}
