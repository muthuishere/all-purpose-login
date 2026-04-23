package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/muthuishere/all-purpose-login/internal/config"
	"github.com/muthuishere/all-purpose-login/internal/oauth"
	"github.com/muthuishere/all-purpose-login/internal/store"
)

const (
	defaultGoogleAuthURL   = "https://accounts.google.com/o/oauth2/v2/auth"
	defaultGoogleTokenURL  = "https://oauth2.googleapis.com/token"
	defaultGoogleRevokeURL = "https://oauth2.googleapis.com/revoke"
)

// Google implements the Provider interface.
type Google struct {
	cfg       config.ProviderConfig
	authURL   string
	tokenURL  string
	revokeURL string
	client    *http.Client
	// runFlow is injectable for tests. Defaults to oauth.RunFlow.
	runFlow func(ctx context.Context, cfg oauth.FlowConfig) (*oauth.TokenResponse, error)
	// now is injectable for tests.
	now func() time.Time
}

type Option func(interface{})

func WithHTTPClient(c *http.Client) Option {
	return func(p interface{}) {
		switch v := p.(type) {
		case *Google:
			v.client = c
		case *Microsoft:
			v.client = c
		}
	}
}

func WithAuthEndpoint(u string) Option {
	return func(p interface{}) {
		switch v := p.(type) {
		case *Google:
			v.authURL = u
		case *Microsoft:
			v.authURL = u
		}
	}
}

func WithTokenEndpoint(u string) Option {
	return func(p interface{}) {
		switch v := p.(type) {
		case *Google:
			v.tokenURL = u
		case *Microsoft:
			v.tokenURL = u
		}
	}
}

func WithRevokeEndpoint(u string) Option {
	return func(p interface{}) {
		switch v := p.(type) {
		case *Google:
			v.revokeURL = u
		}
	}
}

func withRunFlow(f func(ctx context.Context, cfg oauth.FlowConfig) (*oauth.TokenResponse, error)) Option {
	return func(p interface{}) {
		switch v := p.(type) {
		case *Google:
			v.runFlow = f
		case *Microsoft:
			v.runFlow = f
		}
	}
}

func withNow(n func() time.Time) Option {
	return func(p interface{}) {
		switch v := p.(type) {
		case *Google:
			v.now = n
		case *Microsoft:
			v.now = n
		}
	}
}

// NewGoogle builds a Google provider from config.
func NewGoogle(cfg config.ProviderConfig, opts ...Option) *Google {
	g := &Google{
		cfg:       cfg,
		authURL:   defaultGoogleAuthURL,
		tokenURL:  defaultGoogleTokenURL,
		revokeURL: defaultGoogleRevokeURL,
		client:    http.DefaultClient,
		runFlow:   oauth.RunFlow,
		now:       time.Now,
	}
	for _, o := range opts {
		o(g)
	}
	return g
}

func (g *Google) Name() string { return "google" }

// ExpandScopes resolves aliases. URIs (containing "://") pass through.
func (g *Google) ExpandScopes(aliases []string) ([]string, error) {
	out := make([]string, 0, len(aliases))
	for _, a := range aliases {
		if strings.Contains(a, "://") {
			out = append(out, a)
			continue
		}
		v, ok := googleAliases[a]
		if !ok {
			return nil, fmt.Errorf("google: unknown scope alias %q", a)
		}
		// compound aliases (e.g. "profile" -> "openid email profile") are
		// space-joined values; split into individual scopes.
		for _, s := range strings.Fields(v) {
			out = append(out, s)
		}
	}
	return out, nil
}

// buildAuthURL constructs the full Google authorize URL.
func (g *Google) buildAuthURL(redirectURI, state, challenge string, scopes []string) (string, error) {
	if g.cfg.ClientID == "" {
		return "", ErrNoClientID
	}
	u, err := url.Parse(g.authURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", g.cfg.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", strings.Join(scopes, " "))
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("access_type", "offline")
	q.Set("prompt", "consent")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (g *Google) Login(ctx context.Context, label string, opts LoginOpts) (*store.TokenRecord, error) {
	if g.cfg.ClientID == "" {
		return nil, ErrNoClientID
	}
	scopes := opts.Scopes
	if len(scopes) == 0 {
		scopes = []string{"openid", "email", "profile"}
	}
	flowCfg := oauth.FlowConfig{
		Endpoint: oauth.EndpointConfig{
			TokenURL: g.tokenURL,
			ClientID: g.cfg.ClientID,
		},
		Scopes: scopes,
		AuthURLBuilder: func(redirectURI, state, challenge string) (string, error) {
			return g.buildAuthURL(redirectURI, state, challenge, scopes)
		},
	}
	tr, err := g.runFlow(ctx, flowCfg)
	if err != nil {
		return nil, err
	}
	claims, _ := oauth.ParseClaims(tr.IDToken)
	subject := ""
	if claims != nil {
		switch {
		case claims.Email != "":
			subject = claims.Email
		case claims.UPN != "":
			subject = claims.UPN
		default:
			subject = claims.Sub
		}
	}
	now := g.now()
	expires := now.Add(time.Duration(tr.ExpiresIn)*time.Second - 30*time.Second)
	granted := unionScopes(scopes, strings.Fields(tr.Scope))
	rec := &store.TokenRecord{
		Provider:     "google",
		Label:        label,
		Handle:       "google:" + label,
		Subject:      subject,
		RefreshToken: tr.RefreshToken,
		AccessToken:  tr.AccessToken,
		ExpiresAt:    expires,
		Scopes:       granted,
		IssuedAt:     now,
	}
	return rec, nil
}

func (g *Google) Token(ctx context.Context, rec *store.TokenRecord, scope string) (string, *store.TokenRecord, error) {
	if rec == nil {
		return "", nil, fmt.Errorf("google: nil record")
	}
	// Scope check: the requested scope (may be an alias) must be in record.Scopes
	// after expansion.
	exp, err := g.ExpandScopes([]string{scope})
	if err != nil {
		return "", nil, err
	}
	for _, s := range exp {
		if !hasScope(rec.Scopes, s) {
			return "", nil, fmt.Errorf("%w: %s", ErrScopeNotGranted, scope)
		}
	}
	now := g.now()
	if rec.ExpiresAt.Sub(now) > 30*time.Second {
		return rec.AccessToken, rec, nil
	}
	// Refresh.
	ep := oauth.EndpointConfig{TokenURL: g.tokenURL, ClientID: g.cfg.ClientID}
	tr, err := oauth.Refresh(ctx, ep, rec.RefreshToken, rec.Scopes)
	if err != nil {
		return "", nil, err
	}
	updated := *rec
	updated.AccessToken = tr.AccessToken
	if tr.RefreshToken != "" {
		updated.RefreshToken = tr.RefreshToken
	}
	updated.IssuedAt = now
	updated.ExpiresAt = now.Add(time.Duration(tr.ExpiresIn)*time.Second - 30*time.Second)
	return updated.AccessToken, &updated, nil
}

func (g *Google) Logout(ctx context.Context, rec *store.TokenRecord) error {
	if rec == nil || rec.RefreshToken == "" {
		return nil
	}
	form := url.Values{}
	form.Set("token", rec.RefreshToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.revokeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("google: revoke returned %d", resp.StatusCode)
	}
	return nil
}

func hasScope(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func unionScopes(a, b []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range a {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, s := range b {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
