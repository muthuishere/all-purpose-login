package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"regexp"
	"testing"
)

// RFC 7636 unreserved chars for code_verifier: A-Z / a-z / 0-9 / - . _ ~
var verifierCharset = regexp.MustCompile(`^[A-Za-z0-9._~\-]+$`)

func TestGenerateVerifier_LengthAndCharset(t *testing.T) {
	v, err := GenerateVerifier()
	if err != nil {
		t.Fatalf("GenerateVerifier: %v", err)
	}
	if len(v) < 43 || len(v) > 128 {
		t.Fatalf("verifier length %d out of [43,128]", len(v))
	}
	if !verifierCharset.MatchString(v) {
		t.Fatalf("verifier %q contains chars outside RFC 7636 unreserved set", v)
	}
}

func TestChallenge_IsBase64URLSHA256OfVerifier(t *testing.T) {
	v, err := GenerateVerifier()
	if err != nil {
		t.Fatalf("GenerateVerifier: %v", err)
	}
	c := Challenge(v)
	sum := sha256.Sum256([]byte(v))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if c != want {
		t.Fatalf("challenge mismatch:\n got %q\nwant %q", c, want)
	}
}

func TestGenerateVerifier_EntropySmoke(t *testing.T) {
	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		v, err := GenerateVerifier()
		if err != nil {
			t.Fatalf("GenerateVerifier: %v", err)
		}
		if _, dup := seen[v]; dup {
			t.Fatalf("duplicate verifier at iter %d: %s", i, v)
		}
		seen[v] = struct{}{}
	}
}
