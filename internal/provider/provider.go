package provider

import (
	"context"

	"github.com/muthuishere/all-purpose-login/internal/store"
)

type LoginOpts struct {
	Tenant string
	Scopes []string
	Force  bool
}

type Provider interface {
	Name() string
	Login(ctx context.Context, label string, opts LoginOpts) (*store.TokenRecord, error)
	Token(ctx context.Context, rec *store.TokenRecord, scope string) (access string, refreshed *store.TokenRecord, err error)
	Logout(ctx context.Context, rec *store.TokenRecord) error
	ExpandScopes(aliases []string) ([]string, error)
}
