package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// GenerateVerifier returns a PKCE code_verifier per RFC 7636.
// 64 random bytes base64url-encoded (no padding) yields an 86-char string
// using only unreserved characters [A-Za-z0-9_.~-].
func GenerateVerifier() (string, error) {
	buf := make([]byte, 64)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Challenge returns base64url(sha256(verifier)) with no padding — S256 method.
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
