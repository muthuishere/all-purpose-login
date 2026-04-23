package store

import (
	"context"
	"time"
)

// TokenRecord is the unit of per-account state. Serialised as JSON into
// the OS keychain, one record per {provider, label}.
type TokenRecord struct {
	Provider     string    `json:"provider"`
	Label        string    `json:"label"`
	Handle       string    `json:"handle"`
	Subject      string    `json:"sub"`
	Tenant       string    `json:"tenant,omitempty"`
	RefreshToken string    `json:"refresh_token"`
	AccessToken  string    `json:"access_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	Scopes       []string  `json:"scopes"`
	IssuedAt     time.Time `json:"issued_at"`
}

// HandleString returns the canonical "{provider}:{label}" handle for this
// record. It does not mutate r.Handle.
func (r *TokenRecord) HandleString() string {
	return r.Provider + ":" + r.Label
}

// Store is the only API other packages use for token persistence.
// Every method takes context.Context for future backends; the keychain
// backend ignores cancellation.
type Store interface {
	Put(ctx context.Context, rec *TokenRecord) error
	Get(ctx context.Context, handle string) (*TokenRecord, error)
	List(ctx context.Context) ([]*TokenRecord, error)
	Delete(ctx context.Context, handle string) error
}

// New returns the default (keychain) Store. v0 only supports keychain.
func New() (Store, error) {
	return newKeychainStore()
}
