package oauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
)

// GenerateState returns 32 random bytes hex-encoded (64 chars).
func GenerateState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// ConstantTimeEqual compares two strings in constant time.
// Different-length inputs return false without panic.
func ConstantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
