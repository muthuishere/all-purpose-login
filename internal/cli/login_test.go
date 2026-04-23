package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/muthuishere/all-purpose-login/internal/provider"
	"github.com/muthuishere/all-purpose-login/internal/store"
)

func TestLoginCmd_HappyPath(t *testing.T) {
	var capturedOpts provider.LoginOpts
	fp := &fakeProvider{
		name:       "google",
		expandPass: true,
		loginFn: func(ctx context.Context, label string, opts provider.LoginOpts) (*store.TokenRecord, error) {
			capturedOpts = opts
			return &store.TokenRecord{
				Provider: "google", Label: label, Handle: "google:" + label, Subject: "u@x",
			}, nil
		},
	}
	reg := registryWith(fp)
	st := newFakeStore()
	var out, errB bytes.Buffer
	cmd := LoginCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work", "--scope", "gmail.readonly", "--force"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if st.putCalls != 1 {
		t.Errorf("store.Put calls = %d; want 1", st.putCalls)
	}
	if !capturedOpts.Force {
		t.Errorf("--force not passed through")
	}
	if len(capturedOpts.Scopes) != 1 || capturedOpts.Scopes[0] != "gmail.readonly" {
		t.Errorf("scopes not passed: %v", capturedOpts.Scopes)
	}
	if !strings.Contains(out.String(), "Signed in as u@x") {
		t.Errorf("stdout missing: %q", out.String())
	}
}

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
