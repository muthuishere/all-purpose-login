package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func newTokenServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(handler))
}

func TestExchangeCode_PostsCorrectFormAndParses(t *testing.T) {
	var gotForm url.Values
	srv := newTokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
			t.Errorf("Content-Type = %q", ct)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		gotForm = r.PostForm
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "AT",
			"refresh_token": "RT",
			"expires_in":    3600,
			"id_token":      "ID",
			"scope":         "a b",
			"token_type":    "Bearer",
		})
	})
	defer srv.Close()

	cfg := EndpointConfig{
		TokenURL:    srv.URL,
		ClientID:    "client-x",
		RedirectURI: "http://127.0.0.1:1234/callback",
	}
	resp, err := ExchangeCode(context.Background(), cfg, "CODE", "VERIFIER")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if resp.AccessToken != "AT" || resp.RefreshToken != "RT" || resp.ExpiresIn != 3600 ||
		resp.IDToken != "ID" || resp.Scope != "a b" {
		t.Fatalf("parsed response wrong: %+v", resp)
	}

	want := map[string]string{
		"grant_type":    "authorization_code",
		"code":          "CODE",
		"code_verifier": "VERIFIER",
		"client_id":     "client-x",
		"redirect_uri":  "http://127.0.0.1:1234/callback",
	}
	for k, v := range want {
		if got := gotForm.Get(k); got != v {
			t.Errorf("form[%s] = %q, want %q", k, got, v)
		}
	}
}

func TestExchangeCode_InvalidGrantError(t *testing.T) {
	srv := newTokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":             "invalid_grant",
			"error_description": "bad code",
		})
	})
	defer srv.Close()

	cfg := EndpointConfig{TokenURL: srv.URL, ClientID: "c", RedirectURI: "http://127.0.0.1:1/callback"}
	_, err := ExchangeCode(context.Background(), cfg, "CODE", "V")
	if !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("err = %v, want ErrInvalidGrant", err)
	}
}

func TestExchangeCode_InvalidScopeError(t *testing.T) {
	srv := newTokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "invalid_scope",
		})
	})
	defer srv.Close()

	cfg := EndpointConfig{TokenURL: srv.URL, ClientID: "c", RedirectURI: "http://127.0.0.1:1/callback"}
	_, err := ExchangeCode(context.Background(), cfg, "CODE", "V")
	if !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("err = %v, want ErrInvalidScope", err)
	}
}

func TestExchangeCode_NetworkFailureWrapped(t *testing.T) {
	// URL that cannot be reached — port 1 on 127.0.0.1 refused.
	cfg := EndpointConfig{TokenURL: "http://127.0.0.1:1/token", ClientID: "c", RedirectURI: "http://127.0.0.1:1/callback"}
	_, err := ExchangeCode(context.Background(), cfg, "CODE", "V")
	if err == nil {
		t.Fatal("expected error for unreachable endpoint")
	}
}

func TestRefresh_PostsCorrectFormAndParses(t *testing.T) {
	var gotForm url.Values
	srv := newTokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		gotForm = r.PostForm
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "AT2",
			"refresh_token": "RT2",
			"expires_in":    1800,
			"token_type":    "Bearer",
		})
	})
	defer srv.Close()

	cfg := EndpointConfig{TokenURL: srv.URL, ClientID: "client-x", RedirectURI: "http://127.0.0.1:1/callback"}
	resp, err := Refresh(context.Background(), cfg, "OLD_RT", []string{"scope1", "scope2"})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if resp.AccessToken != "AT2" || resp.RefreshToken != "RT2" {
		t.Fatalf("parsed wrong: %+v", resp)
	}

	want := map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": "OLD_RT",
		"client_id":     "client-x",
		"scope":         "scope1 scope2",
	}
	for k, v := range want {
		if got := gotForm.Get(k); got != v {
			t.Errorf("form[%s] = %q, want %q", k, got, v)
		}
	}
}

func TestRefresh_PreservesOldRefreshWhenAbsent(t *testing.T) {
	srv := newTokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "AT3",
			"expires_in":   900,
			// no refresh_token in response
		})
	})
	defer srv.Close()

	cfg := EndpointConfig{TokenURL: srv.URL, ClientID: "c", RedirectURI: "http://127.0.0.1:1/callback"}
	resp, err := Refresh(context.Background(), cfg, "OLD_RT", nil)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if resp.RefreshToken != "OLD_RT" {
		t.Fatalf("RefreshToken = %q, want preserved OLD_RT", resp.RefreshToken)
	}
	if resp.AccessToken != "AT3" {
		t.Fatalf("AccessToken = %q", resp.AccessToken)
	}
}
