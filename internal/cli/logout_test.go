package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/muthuishere/all-purpose-login/internal/store"
)

func TestLogoutCmd_Success(t *testing.T) {
	logoutCalled := false
	fp := &fakeProvider{
		name: "google",
		logoutFn: func(ctx context.Context, rec *store.TokenRecord) error {
			logoutCalled = true
			return nil
		},
	}
	reg := registryWith(fp)
	st := newFakeStore()
	st.records["google:work"] = &store.TokenRecord{Provider: "google", Label: "work", Handle: "google:work"}
	var out, errB bytes.Buffer
	cmd := LogoutCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !logoutCalled {
		t.Errorf("provider.Logout not called")
	}
	if st.delCalls != 1 {
		t.Errorf("delete not called: %d", st.delCalls)
	}
	if !strings.Contains(out.String(), "Removed google:work") {
		t.Errorf("stdout: %q", out.String())
	}
}

func TestLogoutCmd_Missing(t *testing.T) {
	fp := &fakeProvider{name: "google"}
	reg := registryWith(fp)
	st := newFakeStore()
	var out, errB bytes.Buffer
	cmd := LogoutCmd(reg, st, &out, &errB)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"google:nobody"})
	err := cmd.ExecuteContext(context.Background())
	var ce *CLIError
	if !errors.As(err, &ce) || ce.Code != ExitAuth {
		t.Fatalf("expected ExitAuth, got %v", err)
	}
}

func TestLogoutCmd_RevokeFailsWarnsAndContinues(t *testing.T) {
	fp := &fakeProvider{
		name: "google",
		logoutFn: func(ctx context.Context, rec *store.TokenRecord) error {
			return errors.New("network down")
		},
	}
	reg := registryWith(fp)
	st := newFakeStore()
	st.records["google:work"] = &store.TokenRecord{Provider: "google", Label: "work", Handle: "google:work"}
	var out, errB bytes.Buffer
	cmd := LogoutCmd(reg, st, &out, &errB)
	cmd.SetArgs([]string{"google:work"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !strings.Contains(errB.String(), "warning") {
		t.Errorf("stderr missing warning: %q", errB.String())
	}
	if st.delCalls != 1 {
		t.Errorf("delete not called")
	}
}
