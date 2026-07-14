package jsonwire

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestUTF8IteratorAcceptsEveryScalarWidthAndBoundary(t *testing.T) {
	want := []rune{
		0x0000,
		0x007f,
		0x0080,
		0x07ff,
		0x0800,
		0xd7ff,
		0xe000,
		utf8.RuneError,
		0xffff,
		0x10000,
		utf8.MaxRune,
	}
	raw := []byte(string(want))
	iterator := newUTF8Iterator(raw)
	position := 0
	for i, wantRune := range want {
		gotRune, span, ok, err := iterator.next()
		if err != nil {
			t.Fatalf("next scalar %d: %v", i, err)
		}
		if !ok {
			t.Fatalf("next scalar %d reported EOF", i)
		}
		wantEnd := position + utf8.RuneLen(wantRune)
		if gotRune != wantRune || span != (Span{Start: position, End: wantEnd}) {
			t.Fatalf("next scalar %d = (%U, %#v), want (%U, %#v)", i, gotRune, span, wantRune, Span{Start: position, End: wantEnd})
		}
		position = wantEnd
	}
	if _, _, ok, err := iterator.next(); err != nil || ok {
		t.Fatalf("next after input = ok %t, error %v; want clean EOF", ok, err)
	}
}

func TestUTF8IteratorRejectsMalformedRawBytes(t *testing.T) {
	for _, test := range []struct {
		name     string
		raw      []byte
		position int
	}{
		{name: "overlong two byte", raw: []byte{0xc0, 0x80}},
		{name: "overlong three byte", raw: []byte{0xe0, 0x80, 0x80}},
		{name: "overlong four byte", raw: []byte{0xf0, 0x80, 0x80, 0x80}},
		{name: "encoded high surrogate", raw: []byte{0xed, 0xa0, 0x80}},
		{name: "encoded low surrogate", raw: []byte{0xed, 0xbf, 0xbf}},
		{name: "truncated two byte", raw: []byte{0xc2}},
		{name: "truncated three byte", raw: []byte{0xe2, 0x82}},
		{name: "truncated four byte", raw: []byte{0xf0, 0x9f, 0x98}},
		{name: "leading continuation", raw: []byte{0x80}},
		{name: "invalid second byte", raw: []byte{0xe2, 0x28, 0xa1}},
		{name: "above Unicode maximum", raw: []byte{0xf4, 0x90, 0x80, 0x80}},
		{name: "invalid leading byte", raw: []byte{0xff}},
		{name: "after valid scalar", raw: []byte{'a', 0xc2, 'x'}, position: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := consumeUTF8(test.raw)
			if err == nil {
				t.Fatal("consumeUTF8() error = nil")
			}
			var validationError *ValidationError
			if !errors.As(err, &validationError) {
				t.Fatalf("error type = %T, want *ValidationError", err)
			}
			if validationError.Kind() != KindUnicode || validationError.Position() != test.position {
				t.Fatalf("error = kind %q position %d, want %q at %d", validationError.Kind(), validationError.Position(), KindUnicode, test.position)
			}
		})
	}
}

func TestUTF8IteratorDistinguishesLiteralReplacementRune(t *testing.T) {
	iterator := newUTF8Iterator([]byte("�"))
	got, span, ok, err := iterator.next()
	if err != nil || !ok || got != utf8.RuneError || span != (Span{Start: 0, End: 3}) {
		t.Fatalf("literal U+FFFD = (%U, %#v, %t, %v)", got, span, ok, err)
	}
	if err := consumeUTF8([]byte{0xff}); err == nil {
		t.Fatal("malformed raw byte was accepted as U+FFFD")
	}
}

func TestUTF8IteratorErrorDoesNotEchoInput(t *testing.T) {
	raw := append([]byte{0xff}, []byte("do-not-echo")...)
	err := consumeUTF8(raw)
	if err == nil {
		t.Fatal("consumeUTF8() error = nil")
	}
	if strings.Contains(err.Error(), "do-not-echo") {
		t.Fatalf("error echoed rejected input: %q", err)
	}
}

func TestDecodeJSONStringLosslessly(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  []byte
		want string
	}{
		{name: "empty", raw: []byte(`""`), want: ""},
		{name: "plain Unicode", raw: []byte(`"日本語"`), want: "日本語"},
		{name: "simple escapes", raw: []byte(`"\"\\\/\b\f\n\r\t"`), want: "\"\\/\b\f\n\r\t"},
		{name: "lowercase hex", raw: []byte(`"\u00e9"`), want: "é"},
		{name: "uppercase hex", raw: []byte(`"\u00E9"`), want: "é"},
		{name: "lowercase surrogate pair", raw: []byte(`"\ud83d\ude00"`), want: "😀"},
		{name: "uppercase surrogate pair", raw: []byte(`"\uD83D\uDE00"`), want: "😀"},
		{name: "minimum surrogate pair", raw: []byte(`"\uD800\uDC00"`), want: string(rune(0x10000))},
		{name: "maximum surrogate pair", raw: []byte(`"\uDBFF\uDFFF"`), want: string(utf8.MaxRune)},
		{name: "literal replacement rune", raw: []byte(`"�"`), want: "�"},
		{name: "escaped replacement rune", raw: []byte(`"\uFFFD"`), want: "�"},
		{name: "escaped NUL", raw: []byte(`"\u0000"`), want: string([]byte{0})},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, span, err := decodeJSONString(test.raw, 0)
			if err != nil {
				t.Fatalf("decodeJSONString() error = %v", err)
			}
			if got != test.want || span != (Span{Start: 0, End: len(test.raw)}) {
				t.Fatalf("decodeJSONString() = (%q, %#v), want (%q, %#v)", got, span, test.want, Span{Start: 0, End: len(test.raw)})
			}
		})
	}
}

func TestDecodeJSONStringReturnsExactSubspan(t *testing.T) {
	raw := []byte(`xx"line\n"tail`)
	got, span, err := decodeJSONString(raw, 2)
	if err != nil {
		t.Fatalf("decodeJSONString() error = %v", err)
	}
	if got != "line\n" || span != (Span{Start: 2, End: 10}) {
		t.Fatalf("decodeJSONString() = (%q, %#v), want newline and span 2:10", got, span)
	}
}

func TestDecodeJSONStringRejectsInvalidEscapesAndScalars(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  []byte
		kind ErrorKind
	}{
		{name: "missing opening quote", raw: []byte(`x`), kind: KindSyntax},
		{name: "unterminated", raw: []byte(`"x`), kind: KindSyntax},
		{name: "dangling escape", raw: []byte{'"', 'x', '\\'}, kind: KindSyntax},
		{name: "unknown escape", raw: []byte(`"\v"`), kind: KindSyntax},
		{name: "uppercase U escape", raw: []byte(`"\U0041"`), kind: KindSyntax},
		{name: "short Unicode escape", raw: []byte(`"\u123"`), kind: KindSyntax},
		{name: "non-hex Unicode escape", raw: []byte(`"\u12x4"`), kind: KindSyntax},
		{name: "unescaped newline", raw: []byte("\"line\nbreak\""), kind: KindSyntax},
		{name: "unescaped NUL", raw: []byte{'"', 0, '"'}, kind: KindSyntax},
		{name: "malformed raw UTF-8", raw: []byte{'"', 0xff, '"'}, kind: KindUnicode},
		{name: "isolated high surrogate", raw: []byte(`"\uD800"`), kind: KindUnicode},
		{name: "isolated low surrogate", raw: []byte(`"\uDC00"`), kind: KindUnicode},
		{name: "high followed by scalar", raw: []byte(`"\uD800\u0041"`), kind: KindUnicode},
		{name: "high followed by high", raw: []byte(`"\uD800\uD801"`), kind: KindUnicode},
		{name: "reversed pair", raw: []byte(`"\uDC00\uD800"`), kind: KindUnicode},
		{name: "non-adjacent pair", raw: []byte(`"\uD800x\uDC00"`), kind: KindUnicode},
		{name: "high followed by short escape", raw: []byte(`"\uD800\n"`), kind: KindUnicode},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := decodeJSONString(test.raw, 0)
			if err == nil {
				t.Fatal("decodeJSONString() error = nil")
			}
			var validationError *ValidationError
			if !errors.As(err, &validationError) {
				t.Fatalf("error type = %T, want *ValidationError", err)
			}
			if validationError.Kind() != test.kind {
				t.Fatalf("error kind = %q, want %q", validationError.Kind(), test.kind)
			}
			if validationError.Position() < 0 || validationError.Position() > len(test.raw) {
				t.Fatalf("error position = %d outside input of %d bytes", validationError.Position(), len(test.raw))
			}
		})
	}
}

func TestDecodeJSONStringOwnsDecodedValue(t *testing.T) {
	raw := []byte(`"owned"`)
	got, _, err := decodeJSONString(raw, 0)
	if err != nil {
		t.Fatalf("decodeJSONString() error = %v", err)
	}
	for i := range raw {
		raw[i] = 'x'
	}
	if got != "owned" {
		t.Fatalf("decoded value changed after input reuse: %q", got)
	}
}

func consumeUTF8(raw []byte) error {
	iterator := newUTF8Iterator(raw)
	for {
		_, _, ok, err := iterator.next()
		if err != nil || !ok {
			return err
		}
	}
}
