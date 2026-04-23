package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/muthuishere/all-purpose-login/internal/provider"
	"github.com/muthuishere/all-purpose-login/internal/store"
)

func providerErrScopeNotGranted() error {
	return fmt.Errorf("%w: x", provider.ErrScopeNotGranted)
}

func TestTokenCmd_Success(t *testing.T) {
	fp := &fakeProvider{
		name: "google",
		tokenFn: func(ctx context.Context, rec *store.TokenRecord, scope string) (string, *store.TokenRecord, error) {
			return "A-TOKEN", rec, nil
		},
	}
	reg := registryWith(fp)
	st := newFakeStore()
	st.records["google:work"] = &store.TokenRecord{
		Provider: "google", Label: "work", Handle: "google:work",
		Scopes: []string{"gmail.readonly"},
	}
	var out, errB bytes.Buffer
	cmd := TokenCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work", "--scope", "gmail.readonly"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if out.String() != "A-TOKEN\n" {
		t.Errorf("stdout = %q; want %q", out.String(), "A-TOKEN\n")
	}
}

func TestTokenCmd_ScopeMissing(t *testing.T) {
	fp := &fakeProvider{name: "google"}
	reg := registryWith(fp)
	st := newFakeStore()
	// no record for handle
	var out, errB bytes.Buffer
	cmd := TokenCmd(reg, st, &out, &errB)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"google:work", "--scope", "gmail.readonly"})
	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	var ce *CLIError
	if !errors.As(err, &ce) || ce.Code != ExitAuth {
		t.Fatalf("expected ExitAuth CLIError, got %v", err)
	}
	if !strings.Contains(ce.Msg, "apl login google:work") {
		t.Errorf("msg missing hint: %q", ce.Msg)
	}
}

func TestTokenCmd_ScopeNotGranted(t *testing.T) {
	fp := &fakeProvider{
		name: "google",
		tokenFn: func(ctx context.Context, rec *store.TokenRecord, scope string) (string, *store.TokenRecord, error) {
			return "", nil, errors.New("scope not granted: " + scope)
		},
	}
	// Wrap as ErrScopeNotGranted
	fp.tokenFn = func(ctx context.Context, rec *store.TokenRecord, scope string) (string, *store.TokenRecord, error) {
		return "", nil, providerErrScopeNotGranted()
	}
	reg := registryWith(fp)
	st := newFakeStore()
	st.records["google:work"] = &store.TokenRecord{Provider: "google", Label: "work", Handle: "google:work"}
	var out, errB bytes.Buffer
	cmd := TokenCmd(reg, st, &out, &errB)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"google:work", "--scope", "gmail.readonly"})
	err := cmd.ExecuteContext(context.Background())
	var ce *CLIError
	if !errors.As(err, &ce) || ce.Code != ExitAuth {
		t.Fatalf("expected ExitAuth, got %v", err)
	}
	if !strings.Contains(ce.Msg, "apl login google:work --scope gmail.readonly --force") {
		t.Errorf("msg wrong: %q", ce.Msg)
	}
}
