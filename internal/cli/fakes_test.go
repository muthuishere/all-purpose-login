package cli

import (
	"context"
	"errors"

	"github.com/muthuishere/all-purpose-login/internal/provider"
	"github.com/muthuishere/all-purpose-login/internal/store"
)

// fakeProvider is a test double for provider.Provider.
type fakeProvider struct {
	name       string
	loginFn    func(ctx context.Context, label string, opts provider.LoginOpts) (*store.TokenRecord, error)
	tokenFn    func(ctx context.Context, rec *store.TokenRecord, scope string) (string, *store.TokenRecord, error)
	logoutFn   func(ctx context.Context, rec *store.TokenRecord) error
	expandFn   func(aliases []string) ([]string, error)
	expandPass bool // if true, aliases returned unchanged
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) Login(ctx context.Context, label string, opts provider.LoginOpts) (*store.TokenRecord, error) {
	if f.loginFn != nil {
		return f.loginFn(ctx, label, opts)
	}
	return &store.TokenRecord{Provider: f.name, Label: label, Handle: f.name + ":" + label, Subject: "user@example"}, nil
}
func (f *fakeProvider) Token(ctx context.Context, rec *store.TokenRecord, scope string) (string, *store.TokenRecord, error) {
	if f.tokenFn != nil {
		return f.tokenFn(ctx, rec, scope)
	}
	return rec.AccessToken, rec, nil
}
func (f *fakeProvider) Logout(ctx context.Context, rec *store.TokenRecord) error {
	if f.logoutFn != nil {
		return f.logoutFn(ctx, rec)
	}
	return nil
}
func (f *fakeProvider) ExpandScopes(aliases []string) ([]string, error) {
	if f.expandFn != nil {
		return f.expandFn(aliases)
	}
	if f.expandPass {
		return aliases, nil
	}
	return aliases, nil
}

// fakeStore is an in-memory store.Store.
type fakeStore struct {
	records  map[string]*store.TokenRecord
	putErr   error
	getErr   error
	listErr  error
	putCalls int
	delCalls int
}

func newFakeStore() *fakeStore {
	return &fakeStore{records: make(map[string]*store.TokenRecord)}
}

func (s *fakeStore) Put(ctx context.Context, rec *store.TokenRecord) error {
	s.putCalls++
	if s.putErr != nil {
		return s.putErr
	}
	s.records[rec.HandleString()] = rec
	return nil
}
func (s *fakeStore) Get(ctx context.Context, handle string) (*store.TokenRecord, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	r, ok := s.records[handle]
	if !ok {
		return nil, store.ErrNotFound
	}
	return r, nil
}
func (s *fakeStore) List(ctx context.Context) ([]*store.TokenRecord, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := make([]*store.TokenRecord, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, r)
	}
	return out, nil
}
func (s *fakeStore) Delete(ctx context.Context, handle string) error {
	s.delCalls++
	if _, ok := s.records[handle]; !ok {
		return store.ErrNotFound
	}
	delete(s.records, handle)
	return nil
}

// registryWith builds a Registry populated with the given fakes.
func registryWith(ps ...provider.Provider) *provider.Registry {
	reg := provider.NewRegistry()
	for _, p := range ps {
		_ = reg.Register(p)
	}
	return reg
}

var _ = errors.New
