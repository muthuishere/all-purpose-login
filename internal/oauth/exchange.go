package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// EndpointConfig is the minimum token-endpoint config the exchange needs.
// Provider-specific auth URL construction lives in internal/provider.
type EndpointConfig struct {
	TokenURL    string
	ClientID    string
	RedirectURI string
}

// TokenResponse mirrors the provider JSON token response.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
}

// Typed errors for OAUTH-11 mapping.
var (
	ErrInvalidGrant = errors.New("invalid_grant")
	ErrInvalidScope = errors.New("invalid_scope")
)

type oauthErrorBody struct {
	Error       string `json:"error"`
	Description string `json:"error_description"`
}

// httpClient has an explicit timeout so a stalled provider can never hang the CLI.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// ExchangeCode performs the authorization_code grant.
func ExchangeCode(ctx context.Context, cfg EndpointConfig, code, verifier string) (*TokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("code_verifier", verifier)
	form.Set("client_id", cfg.ClientID)
	form.Set("redirect_uri", cfg.RedirectURI)
	return postToken(ctx, cfg.TokenURL, form, "")
}

// Refresh performs the refresh_token grant. If the provider does not rotate
// the refresh token in its response, the caller-supplied refreshToken is
// preserved on the returned TokenResponse.
func Refresh(ctx context.Context, cfg EndpointConfig, refreshToken string, scopes []string) (*TokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", cfg.ClientID)
	if len(scopes) > 0 {
		form.Set("scope", strings.Join(scopes, " "))
	}
	return postToken(ctx, cfg.TokenURL, form, refreshToken)
}

func postToken(ctx context.Context, tokenURL string, form url.Values, preserveRefresh string) (*TokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token endpoint request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var e oauthErrorBody
		if jerr := json.Unmarshal(body, &e); jerr == nil && e.Error != "" {
			switch e.Error {
			case "invalid_grant":
				return nil, fmt.Errorf("%w: %s", ErrInvalidGrant, e.Description)
			case "invalid_scope":
				return nil, fmt.Errorf("%w: %s", ErrInvalidScope, e.Description)
			}
			return nil, fmt.Errorf("token endpoint error: %s: %s", e.Error, e.Description)
		}
		return nil, fmt.Errorf("token endpoint http %d", resp.StatusCode)
	}

	var tr TokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	if tr.RefreshToken == "" && preserveRefresh != "" {
		tr.RefreshToken = preserveRefresh
	}
	return &tr, nil
}
