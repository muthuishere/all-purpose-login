package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/muthuishere/all-purpose-login/internal/provider"
)

func TestParseHandle_Valid(t *testing.T) {
	cases := []struct {
		in       string
		provider string
		label    string
	}{
		{"google:work", "google", "work"},
		{"ms:volentis", "ms", "volentis"},
		{"google:work.prod_1-foo", "google", "work.prod_1-foo"},
	}
	for _, c := range cases {
		h, err := ParseHandle(c.in)
		if err != nil {
			t.Fatalf("ParseHandle(%q) unexpected err: %v", c.in, err)
		}
		if h.Provider != c.provider || h.Label != c.label {
			t.Errorf("ParseHandle(%q) = (%q,%q); want (%q,%q)", c.in, h.Provider, h.Label, c.provider, c.label)
		}
	}
}

func TestParseHandle_Invalid(t *testing.T) {
	cases := []struct {
		in   string
		want error
	}{
		{"google", ErrMissingLabel},
		{"google:", ErrInvalidHandle},
		{"GOOGLE:work", ErrInvalidHandle},
		{"google:work!", ErrInvalidHandle},
		{":work", ErrInvalidHandle},
		{"", ErrInvalidHandle},
	}
	for _, c := range cases {
		_, err := ParseHandle(c.in)
		if err == nil {
			t.Errorf("ParseHandle(%q) expected error", c.in)
			continue
		}
		if !errors.Is(err, c.want) {
			t.Errorf("ParseHandle(%q) = %v; want wrap of %v", c.in, err, c.want)
		}
	}
}

func TestValidateProvider_Unknown(t *testing.T) {
	reg := provider.NewRegistry()
	_ = reg.Register(&fakeProvider{name: "google"})
	h, _ := ParseHandle("slack:x")
	err := ValidateProvider(h, reg)
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if !strings.Contains(err.Error(), `unknown provider "slack"`) {
		t.Errorf("error missing expected text: %v", err)
	}
}

func TestValidateProvider_Known(t *testing.T) {
	reg := provider.NewRegistry()
	_ = reg.Register(&fakeProvider{name: "google"})
	h, _ := ParseHandle("google:work")
	if err := ValidateProvider(h, reg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
