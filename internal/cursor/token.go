package cursor

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

const encodedTokenBytes = 22

var tokenEncoding = base64.RawURLEncoding.Strict()

// Token is one opaque, connection-bound 128-bit cursor identifier.
type Token [16]byte

// ParseToken accepts only the canonical unpadded base64url representation.
func ParseToken(raw string) (Token, api.ErrorCode) {
	if len(raw) != encodedTokenBytes {
		return Token{}, api.ErrorInvalidInput
	}
	var token Token
	n, err := tokenEncoding.Decode(token[:], []byte(raw))
	if err != nil || n != len(token) || token.String() != raw {
		return Token{}, api.ErrorInvalidInput
	}
	return token, ""
}

// String returns the canonical unpadded base64url representation.
func (token Token) String() string {
	return tokenEncoding.EncodeToString(token[:])
}

func deriveChildToken(secret [32]byte, parent Token, digest [32]byte) Token {
	mac := hmac.New(sha256.New, secret[:])
	_, _ = mac.Write(parent[:])
	_, _ = mac.Write(digest[:])
	sum := mac.Sum(nil)
	var child Token
	copy(child[:], sum[:len(child)])
	return child
}
