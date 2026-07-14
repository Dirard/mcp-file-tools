package jsonwire

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestDecodeStringArray(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  []byte
		want []string
	}{
		{name: "empty", raw: []byte(`[]`), want: []string{}},
		{name: "JSON whitespace", raw: []byte(" \n[ \"a\" , \"b\" ]\t"), want: []string{"a", "b"}},
		{
			name: "escapes and surrogate pair",
			raw:  []byte(`["quote\"","solidus\/","backslash\\","controls\b\f\n\r\t","\u0061","\uD83D\uDE00","�"]`),
			want: []string{"quote\"", "solidus/", "backslash\\", "controls\b\f\n\r\t", "a", "😀", "�"},
		},
		{name: "raw Unicode without normalization", raw: []byte(`["日本語","é","é"]`), want: []string{"日本語", "é", "é"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := DecodeStringArray(test.raw)
			if err != nil {
				t.Fatalf("DecodeStringArray() error = %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("DecodeStringArray() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestDecodeStringArrayRejectsMalformedInputWithoutPartialResult(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  []byte
	}{
		{name: "empty input", raw: nil},
		{name: "null", raw: []byte(`null`)},
		{name: "object", raw: []byte(`{}`)},
		{name: "non-string element", raw: []byte(`[1]`)},
		{name: "null after valid element", raw: []byte(`["ok",null]`)},
		{name: "leading comma", raw: []byte(`[,"a"]`)},
		{name: "trailing comma", raw: []byte(`["a",]`)},
		{name: "missing comma", raw: []byte(`["a" "b"]`)},
		{name: "missing close bracket", raw: []byte(`["a"`)},
		{name: "trailing bytes", raw: []byte(`["a"] false`)},
		{name: "unknown escape", raw: []byte(`["\x20"]`)},
		{name: "short Unicode escape", raw: []byte(`["\u12"]`)},
		{name: "isolated high surrogate", raw: []byte(`["\uD800"]`)},
		{name: "isolated low surrogate", raw: []byte(`["\uDC00"]`)},
		{name: "high surrogate followed by scalar", raw: []byte(`["\uD800\u0041"]`)},
		{name: "non-adjacent surrogate pair", raw: []byte(`["\uD800x\uDC00"]`)},
		{name: "literal control", raw: []byte("[\"line\nbreak\"]")},
		{name: "invalid UTF-8 in string", raw: []byte{'[', '"', 0xff, '"', ']'}},
		{name: "invalid UTF-8 outside string", raw: []byte{0xff, '[', ']'}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := DecodeStringArray(test.raw)
			if err == nil {
				t.Fatal("DecodeStringArray() error = nil")
			}
			if got != nil {
				t.Fatalf("DecodeStringArray() returned partial result %#v", got)
			}
		})
	}
}

func TestDecodeStringArrayOwnsDecodedStrings(t *testing.T) {
	raw := []byte(`["name"]`)
	got, err := DecodeStringArray(raw)
	if err != nil {
		t.Fatalf("DecodeStringArray() error = %v", err)
	}
	for i := range raw {
		raw[i] = 'x'
	}
	if !reflect.DeepEqual(got, []string{"name"}) {
		t.Fatalf("decoded strings changed after input reuse: %#v", got)
	}
}

func TestDecodeStringArrayDecodesEscapedBasenamesExactly(t *testing.T) {
	raw := []byte(`["node\u005fmodules","\u002e\u0067it","space\u0020name","quote\"name"]`)
	want := []string{"node_modules", ".git", "space name", `quote"name`}
	got, err := DecodeStringArray(raw)
	if err != nil {
		t.Fatalf("DecodeStringArray() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DecodeStringArray() = %#v, want %#v", got, want)
	}
}

func TestDecodeStringArrayHandlesLargeArraysWithoutPartialFailure(t *testing.T) {
	const count = 4_096
	var builder strings.Builder
	builder.WriteByte('[')
	want := make([]string, 0, count)
	for index := 0; index < count; index++ {
		if index != 0 {
			builder.WriteByte(',')
		}
		value := "dir-" + strconv.Itoa(index)
		want = append(want, value)
		builder.WriteString(strconv.Quote(value))
	}
	builder.WriteByte(']')
	got, err := DecodeStringArray([]byte(builder.String()))
	if err != nil {
		t.Fatalf("DecodeStringArray() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("large DecodeStringArray() returned %d values, want %d", len(got), len(want))
	}

	lateInvalid := []byte("[" + strings.Repeat(`"ok",`, count) + `"\uD800"]`)
	got, err = DecodeStringArray(lateInvalid)
	if err == nil || got != nil {
		t.Fatalf("late malformed value returned (%d values, %v), want nil,error", len(got), err)
	}
}
