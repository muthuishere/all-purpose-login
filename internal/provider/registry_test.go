package provider

import (
	"errors"
	"reflect"
	"testing"

	"github.com/muthuishere/all-purpose-login/internal/config"
)

func TestRegistry_GetAndNames(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(NewGoogle(config.ProviderConfig{ClientID: "gid"}))
	_ = reg.Register(NewMicrosoft(config.ProviderConfig{ClientID: "mid"}))

	got, err := reg.Get("google")
	if err != nil {
		t.Fatalf("Get google: %v", err)
	}
	if got.Name() != "google" {
		t.Errorf("got name %q", got.Name())
	}

	if _, err := reg.Get("bogus"); !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("expected ErrUnknownProvider, got %v", err)
	}

	names := reg.Names()
	want := []string{"google", "ms"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("Names = %v; want %v", names, want)
	}
}

func TestDefaultRegistry_PartialConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.SetProvider("google", "muthuishere", config.ProviderConfig{ClientID: "gid"})
	// Microsoft missing on purpose
	reg, err := DefaultRegistry(cfg)
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}
	if _, err := reg.Get("google"); err != nil {
		t.Errorf("google missing: %v", err)
	}
	if _, err := reg.Get("ms"); err != nil {
		t.Errorf("ms missing: %v", err)
	}
}

func TestRegistry_Resolve_HitMissUnknown(t *testing.T) {
	cfg := &config.Config{}
	cfg.SetProvider("google", "muthuishere", config.ProviderConfig{ClientID: "gid"})
	reg, _ := DefaultRegistry(cfg)

	p, err := reg.Resolve("google", "muthuishere")
	if err != nil {
		t.Fatalf("Resolve google:muthuishere: %v", err)
	}
	if p.Name() != "google" {
		t.Errorf("name = %q; want google", p.Name())
	}

	if _, err := reg.Resolve("google", "deemwar"); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured for missing label, got %v", err)
	}

	if _, err := reg.Resolve("nosuch", "x"); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured for unknown provider, got %v", err)
	}
}
