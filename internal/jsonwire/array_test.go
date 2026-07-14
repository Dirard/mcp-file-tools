package jsonwire

import (
	"errors"
	"testing"
)

func TestScanArrayProvidesExactOrderedValues(t *testing.T) {
	raw := []byte(` ["one", {"two":2}, [true], null] `)
	view, err := ScanArray(raw, protocolTestLimits(), ValidateAll)
	if err != nil {
		t.Fatalf("ScanArray() error = %v", err)
	}
	for index := range raw {
		raw[index] = 'x'
	}

	values := view.Values()
	wantRaw := []string{`"one"`, `{"two":2}`, `[true]`, `null`}
	wantKind := []ValueKind{String, Object, Array, Null}
	if len(values) != len(wantRaw) {
		t.Fatalf("value count = %d, want %d", len(values), len(wantRaw))
	}
	for index, value := range values {
		if got := string(value.Bytes()); got != wantRaw[index] {
			t.Fatalf("value %d raw = %q, want %q", index, got, wantRaw[index])
		}
		if got := value.Kind(); got != wantKind[index] {
			t.Fatalf("value %d kind = %d, want %d", index, got, wantKind[index])
		}
	}

	values[0] = ValueView{}
	if got := string(view.Values()[0].Bytes()); got != `"one"` {
		t.Fatalf("view changed after Values mutation: %q", got)
	}
}

func TestScanArrayRejectsOtherKinds(t *testing.T) {
	_, err := ScanArray([]byte(`{}`), protocolTestLimits(), ValidateAll)
	var validationError *ValidationError
	if !errors.As(err, &validationError) || validationError.Kind() != KindMismatch {
		t.Fatalf("ScanArray({}) error = %v, want kind mismatch", err)
	}
}
