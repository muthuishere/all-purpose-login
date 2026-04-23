package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/muthuishere/all-purpose-login/internal/config"
	"github.com/muthuishere/all-purpose-login/internal/provider"
)

func TestScopesCmd_Google(t *testing.T) {
	reg, _ := provider.DefaultRegistry(&config.Config{})
	var out, errB bytes.Buffer
	cmd := ScopesCmd(reg, &out, &errB)
	cmd.SetArgs([]string{"google"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("exec: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "gmail.readonly") {
		t.Errorf("missing gmail.readonly: %q", s)
	}
	if !strings.Contains(s, "https://www.googleapis.com/auth/gmail.readonly") {
		t.Errorf("missing URI: %q", s)
	}
}

func TestScopesCmd_UnknownProvider(t *testing.T) {
	reg, _ := provider.DefaultRegistry(&config.Config{})
	var out, errB bytes.Buffer
	cmd := ScopesCmd(reg, &out, &errB)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"bogus"})
	err := cmd.ExecuteContext(context.Background())
	var ce *CLIError
	if !errors.As(err, &ce) || ce.Code != ExitUser {
		t.Fatalf("expected ExitUser, got %v", err)
	}
}
