package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestRunFlow_HappyPath simulates the entire loopback + browser + token exchange.
// The fake browser opener performs the GET to the loopback callback URL the way
// a real browser would after the provider redirects.
func TestRunFlow_HappyPath(t *testing.T) {
	// Fake provider token endpoint.
	var gotForm url.Values
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.PostForm
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "AT",
			"refresh_token": "RT",
			"expires_in":    3600,
			"id_token":      "ID",
			"scope":         "openid email",
			"token_type":    "Bearer",
		})
	}))
	defer tokenSrv.Close()

	// Swap the browser opener to act as the user: it hits the loopback callback.
	origOpener := Opener
	defer func() { Opener = origOpener }()
	Opener = func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		// AuthURL built by FlowConfig.AuthURLBuilder embeds redirect_uri + state.
		redirect := u.Query().Get("redirect_uri")
		state := u.Query().Get("state")
		go func() {
			// Tiny delay so Wait() is running; not strictly needed but harmless.
			time.Sleep(10 * time.Millisecond)
			resp, err := http.Get(redirect + "?code=AUTHCODE&state=" + state)
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := FlowConfig{
		Endpoint: EndpointConfig{
			TokenURL: tokenSrv.URL,
			ClientID: "client-x",
		},
		Scopes: []string{"openid", "email"},
		AuthURLBuilder: func(redirectURI, state, challenge string) (string, error) {
			v := url.Values{}
			v.Set("response_type", "code")
			v.Set("client_id", "client-x")
			v.Set("redirect_uri", redirectURI)
			v.Set("state", state)
			v.Set("code_challenge", challenge)
			v.Set("code_challenge_method", "S256")
			v.Set("scope", strings.Join([]string{"openid", "email"}, " "))
			return "http://fake-auth.example/authorize?" + v.Encode(), nil
		},
	}

	resp, err := RunFlow(ctx, cfg)
	if err != nil {
		t.Fatalf("RunFlow: %v", err)
	}
	if resp.AccessToken != "AT" || resp.RefreshToken != "RT" {
		t.Fatalf("got resp = %+v", resp)
	}
	// Token endpoint must have received grant_type=authorization_code and the PKCE verifier.
	if gotForm.Get("grant_type") != "authorization_code" {
		t.Errorf("grant_type = %q", gotForm.Get("grant_type"))
	}
	if gotForm.Get("code") != "AUTHCODE" {
		t.Errorf("code = %q", gotForm.Get("code"))
	}
	if gotForm.Get("code_verifier") == "" {
		t.Error("code_verifier missing from exchange")
	}
	if gotForm.Get("client_id") != "client-x" {
		t.Errorf("client_id = %q", gotForm.Get("client_id"))
	}
	if !strings.HasPrefix(gotForm.Get("redirect_uri"), "http://localhost:") {
		t.Errorf("redirect_uri = %q", gotForm.Get("redirect_uri"))
	}
	if !strings.HasSuffix(gotForm.Get("redirect_uri"), "/") {
		t.Errorf("redirect_uri should end with root path /, got %q", gotForm.Get("redirect_uri"))
	}
}
