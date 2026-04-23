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

// ErrNoClientID is returned by a provider's Login/Token when config lacks a client_id.
var ErrNoClientID = errors.New("provider not configured: missing client_id")

// ErrScopeNotGranted is returned by Token when the requested scope is not in the record.
var ErrScopeNotGranted = errors.New("scope not granted")

// Registry holds named providers.
type Registry struct {
	mu sync.RWMutex
	m  map[string]Provider
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

func (r *Registry) Get(name string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.m[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownProvider, name)
	}
	return p, nil
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

// DefaultRegistry builds the stock Google + Microsoft providers from cfg. The
// registry is returned populated even if one or both providers lack a
// client_id — the error surfaces when Login/Token is called on that provider.
func DefaultRegistry(cfg *config.Config) (*Registry, error) {
	reg := NewRegistry()
	if cfg == nil {
		cfg = &config.Config{}
	}
	_ = reg.Register(NewGoogle(cfg.Google))
	_ = reg.Register(NewMicrosoft(cfg.Microsoft))
	return reg, nil
}
