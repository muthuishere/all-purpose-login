package provider

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/muthuishere/all-purpose-login/internal/config"
)

// ErrUnknownProvider is returned by Registry.Get when the name is not registered.
var ErrUnknownProvider = errors.New("unknown provider")

// ErrNotConfigured is returned by Resolve when no per-label config exists for
// the requested handle.
var ErrNotConfigured = errors.New("provider not configured for label")

// ErrNoClientID is returned by a provider's Login/Token when config lacks a client_id.
var ErrNoClientID = errors.New("provider not configured: missing client_id")

// ErrScopeNotGranted is returned by Token when the requested scope is not in the record.
var ErrScopeNotGranted = errors.New("scope not granted")

// Registry holds named providers (type-level) plus an optional config so that
// it can build per-label configured Provider instances via Resolve.
type Registry struct {
	mu  sync.RWMutex
	m   map[string]Provider
	cfg *config.Config
}

func NewRegistry() *Registry {
	return &Registry{m: make(map[string]Provider)}
}

func (r *Registry) Register(p Provider) error {
	if p == nil || p.Name() == "" {
		return errors.New("provider: nil or unnamed")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[p.Name()] = p
	return nil
}

// Get returns a type-level Provider instance for `name`. The returned instance
// is configured with empty ProviderConfig and is suitable for ExpandScopes /
// DefaultScopes / Name only. For OAuth flows use Resolve.
func (r *Registry) Get(name string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.m[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownProvider, name)
	}
	return p, nil
}

// Resolve returns a Provider configured with the per-label OAuth client for
// `provider:label`. Returns ErrUnknownProvider if `provider` is unknown, or
// ErrNotConfigured if no per-label config exists.
//
// If the registry has no cfg attached (e.g. tests using NewRegistry directly),
// Resolve falls back to the registered Provider for `provider`. This lets
// fake providers be registered and used directly without per-label config.
func (r *Registry) Resolve(provider, label string) (Provider, error) {
	r.mu.RLock()
	cfg := r.cfg
	r.mu.RUnlock()
	if cfg == nil {
		return r.Get(provider)
	}
	pc, ok := cfg.GetProvider(provider, label)
	if !ok {
		return nil, fmt.Errorf("%w: %s:%s", ErrNotConfigured, provider, label)
	}
	switch provider {
	case "google":
		return NewGoogle(pc), nil
	case "ms":
		return NewMicrosoft(pc), nil
	}
	return nil, fmt.Errorf("%w: %s", ErrUnknownProvider, provider)
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.m))
	for n := range r.m {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// DefaultRegistry builds the stock Google + Microsoft type-level providers and
// stores cfg for label resolution via Resolve.
func DefaultRegistry(cfg *config.Config) (*Registry, error) {
	reg := NewRegistry()
	if cfg == nil {
		cfg = &config.Config{}
	}
	reg.cfg = cfg
	_ = reg.Register(NewGoogle(config.ProviderConfig{}))
	_ = reg.Register(NewMicrosoft(config.ProviderConfig{}))
	return reg, nil
}
