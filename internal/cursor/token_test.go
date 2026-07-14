package cursor

import (
	"bytes"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

func TestParseTokenAcceptsOnlyCanonicalBase64URL(t *testing.T) {
	const valid = "A7k3mP9qR2sT5uV8wX1yZw"
	token, code := ParseToken(valid)
	if code != "" {
		t.Fatalf("ParseToken(valid) code = %q", code)
	}
	if got := token.String(); got != valid {
		t.Fatalf("Token.String() = %q, want %q", got, valid)
	}

	invalid := []string{
		valid[:21],
		valid + "A",
		valid + "==",
		valid[:21] + "x", // non-zero trailing bits
		valid[:10] + "+" + valid[11:],
		valid[:10] + "/" + valid[11:],
		valid[:10] + "*" + valid[11:],
		" " + valid,
	}
	for _, raw := range invalid {
		t.Run(raw, func(t *testing.T) {
			got, code := ParseToken(raw)
			if code != api.ErrorInvalidInput {
				t.Fatalf("ParseToken(%q) code = %q, want %q", raw, code, api.ErrorInvalidInput)
			}
			if got != (Token{}) {
				t.Fatalf("ParseToken(%q) returned non-zero token", raw)
			}
		})
	}
}

func TestDeriveChildTokenUsesHMACAndNoEntropy(t *testing.T) {
	parent, code := ParseToken("A7k3mP9qR2sT5uV8wX1yZw")
	if code != "" {
		t.Fatalf("ParseToken(parent) code = %q", code)
	}
	var secret [32]byte
	var digest [32]byte
	for i := range secret {
		secret[i] = byte(i)
		digest[i] = byte(i + 32)
	}

	got := deriveChildToken(secret, parent, digest)
	if want := "2wWzzMrBfIkvRYQzza9Rrw"; got.String() != want {
		t.Fatalf("deriveChildToken = %q, want %q", got.String(), want)
	}
	if got != deriveChildToken(secret, parent, digest) {
		t.Fatal("same inputs produced different child tokens")
	}

	changed := digest
	changed[0]++
	if got == deriveChildToken(secret, parent, changed) {
		t.Fatal("changed digest produced the same child token")
	}
	if bytes.Equal(got[:], parent[:]) {
		t.Fatal("child token unexpectedly equals parent token")
	}
}
