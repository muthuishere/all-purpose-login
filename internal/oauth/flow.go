package oauth

import (
	"context"
	"fmt"
)

// FlowConfig drives RunFlow. The provider package supplies AuthURLBuilder —
// a closure that receives the dynamic redirect URI, state, and PKCE challenge
// and returns the fully-formed authorization URL (including provider-specific
// params like access_type=offline or the MS tenant path).
type FlowConfig struct {
	Endpoint       EndpointConfig
	Scopes         []string
	AuthURLBuilder func(redirectURI, state, challenge string) (string, error)
}

// RunFlow executes the complete PKCE + loopback browser flow and returns
// the token response from the authorization_code exchange.
func RunFlow(ctx context.Context, cfg FlowConfig) (*TokenResponse, error) {
	verifier, err := GenerateVerifier()
	if err != nil {
		return nil, fmt.Errorf("generate verifier: %w", err)
	}
	challenge := Challenge(verifier)

	state, err := GenerateState()
	if err != nil {
		return nil, fmt.Errorf("generate state: %w", err)
	}

	srv, err := NewLoopbackServer(ctx)
	if err != nil {
		return nil, err
	}
	defer srv.Close()

	redirectURI := srv.RedirectURI()
	authURL, err := cfg.AuthURLBuilder(redirectURI, state, challenge)
	if err != nil {
		return nil, fmt.Errorf("build auth url: %w", err)
	}

	// Best-effort browser open — caller prints URL regardless for headless fallback.
	_ = Open(authURL)

	code, _, err := srv.Wait(ctx, state)
	if err != nil {
		return nil, err
	}

	ep := cfg.Endpoint
	ep.RedirectURI = redirectURI
	return ExchangeCode(ctx, ep, code, verifier)
}
