package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/muthuishere/all-purpose-login/internal/config"
	"github.com/muthuishere/all-purpose-login/internal/store"
)

func TestGoogle_ExpandScopes(t *testing.T) {
	g := NewGoogle(config.ProviderConfig{ClientID: "x"})
	got, err := g.ExpandScopes([]string{"gmail.readonly", "calendar"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"https://www.googleapis.com/auth/gmail.readonly",
		"https://www.googleapis.com/auth/calendar",
	}
	if !strSliceEq(got, want) {
		t.Errorf("got %v; want %v", got, want)
	}

	if _, err := g.ExpandScopes([]string{"nope.nothing"}); err == nil {
		t.Errorf("expected error for unknown alias")
	}

	// URI passthrough
	got, err = g.ExpandScopes([]string{"https://full.uri/scope"})
	if err != nil || len(got) != 1 || got[0] != "https://full.uri/scope" {
		t.Errorf("passthrough failed: %v, %v", got, err)
	}
}

func TestGoogle_BuildAuthURL(t *testing.T) {
	g := NewGoogle(config.ProviderConfig{ClientID: "my-client"})
	u, err := g.buildAuthURL("http://127.0.0.1:5555/callback", "STATE123", "CHAL456",
		[]string{"https://www.googleapis.com/auth/gmail.readonly"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(u)
	q := parsed.Query()
	checks := map[string]string{
		"response_type":         "code",
		"client_id":             "my-client",
		"redirect_uri":          "http://127.0.0.1:5555/callback",
		"state":                 "STATE123",
		"code_challenge":        "CHAL456",
		"code_challenge_method": "S256",
		"access_type":           "offline",
		"prompt":                "consent",
	}
	for k, want := range checks {
		if got := q.Get(k); got != want {
			t.Errorf("%s: got %q want %q", k, got, want)
		}
	}
	if !strings.Contains(q.Get("scope"), "gmail.readonly") {
		t.Errorf("scope missing: %q", q.Get("scope"))
	}
}

func TestGoogle_Token_HappyPath_NotExpired(t *testing.T) {
	g := NewGoogle(config.ProviderConfig{ClientID: "x"}, withNow(func() time.Time {
		return time.Unix(1000, 0)
	}))
	rec := &store.TokenRecord{
		AccessToken: "A-TOKEN",
		ExpiresAt:   time.Unix(2000, 0),
		Scopes:      []string{"https://www.googleapis.com/auth/gmail.readonly"},
	}
	tok, _, err := g.Token(context.Background(), rec, "gmail.readonly")
	if err != nil {
		t.Fatal(err)
	}
	if tok != "A-TOKEN" {
		t.Errorf("got %q", tok)
	}
}

func TestGoogle_Token_Expired_Refreshes(t *testing.T) {
	// Stub token endpoint.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" {
			t.Errorf("grant_type = %q", r.Form.Get("grant_type"))
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "NEW-TOK",
			"expires_in":   3600,
		})
	}))
	defer ts.Close()
	g := NewGoogle(config.ProviderConfig{ClientID: "x"},
		WithTokenEndpoint(ts.URL),
		withNow(func() time.Time { return time.Unix(1000, 0) }),
	)
	rec := &store.TokenRecord{
		AccessToken:  "OLD",
		RefreshToken: "R",
		ExpiresAt:    time.Unix(500, 0), // expired
		Scopes:       []string{"https://www.googleapis.com/auth/gmail.readonly"},
	}
	tok, updated, err := g.Token(context.Background(), rec, "gmail.readonly")
	if err != nil {
		t.Fatal(err)
	}
	if tok != "NEW-TOK" {
		t.Errorf("tok = %q", tok)
	}
	if updated == nil || updated.AccessToken != "NEW-TOK" {
		t.Errorf("updated record wrong: %+v", updated)
	}
}

func TestGoogle_Token_ScopeNotGranted(t *testing.T) {
	g := NewGoogle(config.ProviderConfig{ClientID: "x"},
		withNow(func() time.Time { return time.Unix(1000, 0) }),
	)
	rec := &store.TokenRecord{
		AccessToken: "A",
		ExpiresAt:   time.Unix(9999, 0),
		Scopes:      []string{"https://www.googleapis.com/auth/calendar"},
	}
	_, _, err := g.Token(context.Background(), rec, "gmail.readonly")
	if !errors.Is(err, ErrScopeNotGranted) {
		t.Errorf("expected ErrScopeNotGranted, got %v", err)
	}
}

func TestGoogle_Logout_CallsRevoke(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "token=REFRESH") {
			t.Errorf("body missing token: %s", body)
		}
		w.WriteHeader(200)
	}))
	defer ts.Close()
	g := NewGoogle(config.ProviderConfig{ClientID: "x"}, WithRevokeEndpoint(ts.URL))
	err := g.Logout(context.Background(), &store.TokenRecord{RefreshToken: "REFRESH"})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("revoke endpoint not called")
	}
}

func strSliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
