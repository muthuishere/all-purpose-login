package oauth

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func makeJWT(t *testing.T, payload map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	p := base64.RawURLEncoding.EncodeToString(body)
	return header + "." + p + ".sig"
}

func TestParseClaims_ExtractsSubEmailUPN(t *testing.T) {
	tok := makeJWT(t, map[string]any{
		"sub":   "user-123",
		"email": "a@b.com",
		"upn":   "a@contoso",
	})
	c, err := ParseClaims(tok)
	if err != nil {
		t.Fatalf("ParseClaims: %v", err)
	}
	if c.Sub != "user-123" || c.Email != "a@b.com" || c.UPN != "a@contoso" {
		t.Fatalf("claims wrong: %+v", c)
	}
}

func TestParseClaims_OptionalFieldsAbsent(t *testing.T) {
	tok := makeJWT(t, map[string]any{"sub": "only-sub"})
	c, err := ParseClaims(tok)
	if err != nil {
		t.Fatalf("ParseClaims: %v", err)
	}
	if c.Sub != "only-sub" || c.Email != "" || c.UPN != "" {
		t.Fatalf("claims wrong: %+v", c)
	}
}

func TestParseClaims_MalformedToken(t *testing.T) {
	cases := []string{
		"",
		"notajwt",
		"only.two",
		"bad.!!!notbase64!!!.sig",
	}
	for _, tc := range cases {
		if _, err := ParseClaims(tc); err == nil {
			t.Errorf("ParseClaims(%q) want error, got nil", tc)
		}
	}
}
