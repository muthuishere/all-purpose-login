package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/muthuishere/all-purpose-login/internal/config"
	"github.com/muthuishere/all-purpose-login/internal/oauth"
	"github.com/muthuishere/all-purpose-login/internal/store"
)

const (
	defaultMSAuthTemplate  = "https://login.microsoftonline.com/%s/oauth2/v2.0/authorize"
	defaultMSTokenTemplate = "https://login.microsoftonline.com/%s/oauth2/v2.0/token"
)

// Microsoft implements the Provider interface.
type Microsoft struct {
	cfg      config.ProviderConfig
	authURL  string // if non-empty overrides the tenant template
	tokenURL string // if non-empty overrides the tenant template
	client   *http.Client
	runFlow  func(ctx context.Context, cfg oauth.FlowConfig) (*oauth.TokenResponse, error)
	now      func() time.Time
}

func NewMicrosoft(cfg config.ProviderConfig, opts ...Option) *Microsoft {
	m := &Microsoft{
		cfg:     cfg,
		client:  http.DefaultClient,
		runFlow: oauth.RunFlow,
		now:     time.Now,
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

func (m *Microsoft) Name() string { return "ms" }

func (m *Microsoft) tenantFor(opts LoginOpts) string {
	if opts.Tenant != "" {
		return opts.Tenant
	}
	if m.cfg.Tenant != "" {
		return m.cfg.Tenant
	}
	return "common"
}

func (m *Microsoft) authEndpoint(tenant string) string {
	if m.authURL != "" {
		return m.authURL
	}
	return fmt.Sprintf(defaultMSAuthTemplate, tenant)
}

func (m *Microsoft) tokenEndpoint(tenant string) string {
	if m.tokenURL != "" {
		return m.tokenURL
	}
	return fmt.Sprintf(defaultMSTokenTemplate, tenant)
}

// ExpandScopes resolves aliases. URIs (containing "://") pass through.
func (m *Microsoft) ExpandScopes(aliases []string) ([]string, error) {
	out := make([]string, 0, len(aliases))
	for _, a := range aliases {
		if strings.Contains(a, "://") {
			out = append(out, a)
			continue
		}
		v, ok := microsoftAliases[a]
		if !ok {
			return nil, fmt.Errorf("ms: unknown scope alias %q", a)
		}
		out = append(out, v)
	}
	return out, nil
}

func (m *Microsoft) normalizeScopes(scopes []string) []string {
	hasOffline := false
	for _, s := range scopes {
		if s == "offline_access" {
			hasOffline = true
			break
		}
	}
	if hasOffline {
		return scopes
	}
	out := make([]string, 0, len(scopes)+1)
	out = append(out, "offline_access")
	out = append(out, scopes...)
	return out
}

func (m *Microsoft) buildAuthURL(tenant, redirectURI, state, challenge string, scopes []string) (string, error) {
	if m.cfg.ClientID == "" {
		return "", ErrNoClientID
	}
	u, err := url.Parse(m.authEndpoint(tenant))
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", m.cfg.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", strings.Join(scopes, " "))
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (m *Microsoft) Login(ctx context.Context, label string, opts LoginOpts) (*store.TokenRecord, error) {
	if m.cfg.ClientID == "" {
		return nil, ErrNoClientID
	}
	scopes := opts.Scopes
	if len(scopes) == 0 {
		scopes = []string{"openid", "profile", "User.Read"}
	}
	scopes = m.normalizeScopes(scopes)
	tenant := m.tenantFor(opts)
	flowCfg := oauth.FlowConfig{
		Endpoint: oauth.EndpointConfig{
			TokenURL: m.tokenEndpoint(tenant),
			ClientID: m.cfg.ClientID,
		},
		Scopes: scopes,
		AuthURLBuilder: func(redirectURI, state, challenge string) (string, error) {
			return m.buildAuthURL(tenant, redirectURI, state, challenge, scopes)
		},
	}
	tr, err := m.runFlow(ctx, flowCfg)
	if err != nil {
		return nil, err
	}
	claims, _ := oauth.ParseClaims(tr.IDToken)
	subject := ""
	if claims != nil {
		switch {
		case claims.PreferredUsername != "":
			subject = claims.PreferredUsername
		case claims.UPN != "":
			subject = claims.UPN
		case claims.Email != "":
			subject = claims.Email
		default:
			subject = claims.Sub
		}
	}
	now := m.now()
	expires := now.Add(time.Duration(tr.ExpiresIn)*time.Second - 30*time.Second)
	granted := unionScopes(scopes, strings.Fields(tr.Scope))
	storeTenant := tenant
	if storeTenant == "common" {
		storeTenant = ""
	}
	rec := &store.TokenRecord{
		Provider:     "ms",
		Label:        label,
		Handle:       "ms:" + label,
		Subject:      subject,
		Tenant:       storeTenant,
		RefreshToken: tr.RefreshToken,
		AccessToken:  tr.AccessToken,
		ExpiresAt:    expires,
		Scopes:       granted,
		IssuedAt:     now,
	}
	return rec, nil
}

func (m *Microsoft) Token(ctx context.Context, rec *store.TokenRecord, scope string) (string, *store.TokenRecord, error) {
	if rec == nil {
		return "", nil, fmt.Errorf("ms: nil record")
	}
	exp, err := m.ExpandScopes([]string{scope})
	if err != nil {
		return "", nil, err
	}
	for _, s := range exp {
		if !hasScope(rec.Scopes, s) {
			return "", nil, fmt.Errorf("%w: %s", ErrScopeNotGranted, scope)
		}
	}
	now := m.now()
	if rec.ExpiresAt.Sub(now) > 30*time.Second {
		return rec.AccessToken, rec, nil
	}
	tenant := rec.Tenant
	if tenant == "" {
		tenant = "common"
	}
	ep := oauth.EndpointConfig{TokenURL: m.tokenEndpoint(tenant), ClientID: m.cfg.ClientID}
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

// Logout on Microsoft: Graph v2 does not expose a user-initiated refresh-token
// revoke endpoint comparable to Google's. This is a deliberate no-op — local
// record deletion by the CLI is sufficient for sign-out.
func (m *Microsoft) Logout(ctx context.Context, rec *store.TokenRecord) error {
	_ = ctx
	_ = rec
	return nil
}
