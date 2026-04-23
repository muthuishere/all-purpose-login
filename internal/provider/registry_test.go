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
	cfg := &config.Config{
		Google: config.ProviderConfig{ClientID: "gid"},
		// Microsoft missing on purpose
	}
	reg, err := DefaultRegistry(cfg)
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}
	// Both providers must be present in the registry.
	if _, err := reg.Get("google"); err != nil {
		t.Errorf("google missing: %v", err)
	}
	if _, err := reg.Get("ms"); err != nil {
		t.Errorf("ms missing: %v", err)
	}
}
