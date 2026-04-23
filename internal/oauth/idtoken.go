package oauth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Claims carries the subset of ID-token claims v1 needs.
type Claims struct {
	Sub               string `json:"sub"`
	Email             string `json:"email"`
	UPN               string `json:"upn"`                // Microsoft v1 endpoint
	PreferredUsername string `json:"preferred_username"` // Microsoft v2 endpoint, OIDC standard
}

// ParseClaims extracts claims from a JWT payload without verifying the
// signature. v1 trust model: the ID token was received over a TLS-validated
// response from the provider's token endpoint, so the TLS channel is the
// proof. Do not call this on tokens from an untrusted source.
func ParseClaims(idToken string) (*Claims, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed id token: expected 3 segments")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode id token payload: %w", err)
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, fmt.Errorf("parse id token claims: %w", err)
	}
	return &c, nil
}
