package present

import "testing"

func TestQuoteScalar(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "plain", value: "path/file.go", want: `"path/file.go"`},
		{name: "quote backslash slash", value: "\"\\/", want: `"\"\\/"`},
		{name: "controls", value: "\x00\x01\b\t\n\f\r\x1f", want: `"\u0000\u0001\b\t\n\f\r\u001f"`},
		{name: "no html escaping", value: "<tag>&", want: `"<tag>&"`},
		{name: "line separators", value: "\u2028\u2029", want: "\"\u2028\u2029\""},
		{name: "unicode", value: "café/😀", want: `"café/😀"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := QuoteScalar([]byte("prefix:"), test.value)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "prefix:"+test.want {
				t.Fatalf("got %q, want %q", got, "prefix:"+test.want)
			}
		})
	}

	prefix := []byte("unchanged")
	got, err := QuoteScalar(prefix, string([]byte{0xff}))
	if err == nil {
		t.Fatal("invalid UTF-8 was accepted")
	}
	if string(got) != "unchanged" {
		t.Fatalf("destination changed on error: %q", got)
	}
}
