package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/muthuishere/all-purpose-login/internal/oauth"
	"github.com/muthuishere/all-purpose-login/internal/provider"
	"github.com/muthuishere/all-purpose-login/internal/store"
)

// Case (a): no record → browser flow invoked, token printed to stdout.
func TestLoginCmd_NoRecord_RunsBrowserFlow(t *testing.T) {
	loginCalled := 0
	fp := &fakeProvider{
		name:       "google",
		expandPass: true,
		loginFn: func(ctx context.Context, label string, opts provider.LoginOpts) (*store.TokenRecord, error) {
			loginCalled++
			return &store.TokenRecord{
				Provider: "google", Label: label, Handle: "google:" + label, Subject: "u@x",
				AccessToken: "FRESH-TOKEN",
			}, nil
		},
	}
	reg := registryWith(fp)
	st := newFakeStore()
	var out, errB bytes.Buffer
	cmd := LoginCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if loginCalled != 1 {
		t.Errorf("Login called %d times; want 1", loginCalled)
	}
	if out.String() != "FRESH-TOKEN\n" {
		t.Errorf("stdout = %q; want %q", out.String(), "FRESH-TOKEN\n")
	}
	if !strings.Contains(errB.String(), "Signed in as u@x") {
		t.Errorf("stderr missing status: %q", errB.String())
	}
	if st.putCalls != 1 {
		t.Errorf("store.Put calls = %d; want 1", st.putCalls)
	}
}

// Case (b): record + valid token → no browser, cached token printed.
func TestLoginCmd_RecordValid_PrintsCached(t *testing.T) {
	loginCalled := 0
	fp := &fakeProvider{
		name: "google",
		loginFn: func(ctx context.Context, label string, opts provider.LoginOpts) (*store.TokenRecord, error) {
			loginCalled++
			return nil, errors.New("should not be called")
		},
		refreshFn: func(ctx context.Context, rec *store.TokenRecord) (string, *store.TokenRecord, error) {
			// Simulate still-fresh cache: return same record, same token.
			return rec.AccessToken, rec, nil
		},
	}
	reg := registryWith(fp)
	st := newFakeStore()
	st.records["google:work"] = &store.TokenRecord{
		Provider: "google", Label: "work", Handle: "google:work",
		AccessToken: "CACHED-TOKEN",
	}
	var out, errB bytes.Buffer
	cmd := LoginCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if loginCalled != 0 {
		t.Errorf("Login should not be called when cached token is valid")
	}
	if out.String() != "CACHED-TOKEN\n" {
		t.Errorf("stdout = %q; want %q", out.String(), "CACHED-TOKEN\n")
	}
	if errB.String() != "" {
		t.Errorf("stderr should be empty, got %q", errB.String())
	}
}

// Case (c): record + expired → refresh called, new token printed, record persisted.
func TestLoginCmd_RecordExpired_Refreshes(t *testing.T) {
	loginCalled := 0
	refreshCalled := 0
	fp := &fakeProvider{
		name: "google",
		loginFn: func(ctx context.Context, label string, opts provider.LoginOpts) (*store.TokenRecord, error) {
			loginCalled++
			return nil, errors.New("should not be called")
		},
		refreshFn: func(ctx context.Context, rec *store.TokenRecord) (string, *store.TokenRecord, error) {
			refreshCalled++
			updated := *rec
			updated.AccessToken = "NEW-TOKEN"
			return "NEW-TOKEN", &updated, nil
		},
	}
	reg := registryWith(fp)
	st := newFakeStore()
	st.records["google:work"] = &store.TokenRecord{
		Provider: "google", Label: "work", Handle: "google:work",
		AccessToken: "OLD-TOKEN",
	}
	var out, errB bytes.Buffer
	cmd := LoginCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if loginCalled != 0 {
		t.Errorf("Login should not be called on refresh path")
	}
	if refreshCalled != 1 {
		t.Errorf("Refresh called %d times; want 1", refreshCalled)
	}
	if out.String() != "NEW-TOKEN\n" {
		t.Errorf("stdout = %q; want %q", out.String(), "NEW-TOKEN\n")
	}
	if st.putCalls != 1 {
		t.Errorf("store.Put calls = %d; want 1", st.putCalls)
	}
}

// Case (d): --force always does browser flow, even with an existing record.
func TestLoginCmd_Force_AlwaysBrowser(t *testing.T) {
	loginCalled := 0
	fp := &fakeProvider{
		name:       "google",
		expandPass: true,
		loginFn: func(ctx context.Context, label string, opts provider.LoginOpts) (*store.TokenRecord, error) {
			loginCalled++
			if !opts.Force {
				t.Errorf("--force not plumbed to provider")
			}
			return &store.TokenRecord{
				Provider: "google", Label: label, Handle: "google:" + label,
				Subject: "u@x", AccessToken: "FORCED",
			}, nil
		},
		refreshFn: func(ctx context.Context, rec *store.TokenRecord) (string, *store.TokenRecord, error) {
			t.Error("Refresh should not be called on --force")
			return "", nil, nil
		},
	}
	reg := registryWith(fp)
	st := newFakeStore()
	st.records["google:work"] = &store.TokenRecord{
		Provider: "google", Label: "work", Handle: "google:work",
		AccessToken: "CACHED",
	}
	var out, errB bytes.Buffer
	cmd := LoginCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work", "--force"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if loginCalled != 1 {
		t.Errorf("Login called %d times with --force; want 1", loginCalled)
	}
	if out.String() != "FORCED\n" {
		t.Errorf("stdout = %q; want %q", out.String(), "FORCED\n")
	}
}

// Case (e): --scope override forwards scopes to provider and runs browser flow.
func TestLoginCmd_ScopeOverride_ForwardsToProvider(t *testing.T) {
	var captured provider.LoginOpts
	fp := &fakeProvider{
		name:       "google",
		expandPass: true,
		loginFn: func(ctx context.Context, label string, opts provider.LoginOpts) (*store.TokenRecord, error) {
			captured = opts
			return &store.TokenRecord{
				Provider: "google", Label: label, Handle: "google:" + label,
				Subject: "u@x", AccessToken: "T",
			}, nil
		},
	}
	reg := registryWith(fp)
	st := newFakeStore()
	// existing record should be ignored when --scope is passed
	st.records["google:work"] = &store.TokenRecord{
		Provider: "google", Label: "work", Handle: "google:work",
		AccessToken: "CACHED",
	}
	var out, errB bytes.Buffer
	cmd := LoginCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work", "--scope", "gmail.readonly"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if len(captured.Scopes) != 1 || captured.Scopes[0] != "gmail.readonly" {
		t.Errorf("scopes not passed: %v", captured.Scopes)
	}
}

// stdout contract: nothing but the token + newline.
func TestLoginCmd_StdoutOnlyTokenAndNewline(t *testing.T) {
	fp := &fakeProvider{
		name:       "google",
		expandPass: true,
		loginFn: func(ctx context.Context, label string, opts provider.LoginOpts) (*store.TokenRecord, error) {
			return &store.TokenRecord{
				Provider: "google", Label: label, Handle: "google:" + label,
				Subject: "u@x", AccessToken: "ONLY-TOKEN",
			}, nil
		},
	}
	reg := registryWith(fp)
	st := newFakeStore()
	var out, errB bytes.Buffer
	cmd := LoginCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if out.String() != "ONLY-TOKEN\n" {
		t.Errorf("stdout contract broken: %q", out.String())
	}
	// status messages should live on stderr
	if !strings.Contains(errB.String(), "Signed in as") {
		t.Errorf("expected status on stderr, got %q", errB.String())
	}
}

// --tenant is rejected for google handle.
func TestLoginCmd_TenantWithGoogleRejected(t *testing.T) {
	fp := &fakeProvider{name: "google"}
	reg := registryWith(fp)
	st := newFakeStore()
	var out, errB bytes.Buffer
	cmd := LoginCmd(reg, st, &out, &errB)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"google:work", "--tenant", "contoso"})
	err := cmd.ExecuteContext(context.Background())
	var ce *CLIError
	if !errors.As(err, &ce) || ce.Code != ExitUser {
		t.Fatalf("expected ExitUser, got %v", err)
	}
	if !strings.Contains(ce.Msg, "--tenant") {
		t.Errorf("msg: %q", ce.Msg)
	}
}

// invalid_grant on refresh → actionable auth error.
func TestLoginCmd_RefreshInvalidGrant_SuggestsForceLogin(t *testing.T) {
	fp := &fakeProvider{
		name: "google",
		refreshFn: func(ctx context.Context, rec *store.TokenRecord) (string, *store.TokenRecord, error) {
			return "", nil, fmt.Errorf("refresh: %w", oauth.ErrInvalidGrant)
		},
	}
	reg := registryWith(fp)
	st := newFakeStore()
	st.records["google:work"] = &store.TokenRecord{
		Provider: "google", Label: "work", Handle: "google:work",
	}
	var out, errB bytes.Buffer
	cmd := LoginCmd(reg, st, &out, &errB)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"google:work"})
	err := cmd.ExecuteContext(context.Background())
	var ce *CLIError
	if !errors.As(err, &ce) || ce.Code != ExitAuth {
		t.Fatalf("expected ExitAuth CLIError, got %v", err)
	}
	if !strings.Contains(ce.Msg, "--force") {
		t.Errorf("hint missing: %q", ce.Msg)
	}
}
