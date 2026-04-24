package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muthuishere/all-purpose-login/internal/config"
)

func setXDG(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return dir
}

func goodMSFakeShell() *fakeShell {
	fs := newFakeShell()
	fs.setAvailable("az", true)
	fs.respond("az account", fakeResp{Stdout: `{"user":{"name":"u"}}`})
	stubGraphSP(fs)
	fs.respond("az ad app list", fakeResp{Stdout: `[]`})
	fs.respond("az ad app",
		fakeResp{Stdout: `{"appId":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"}`})
	fs.respond("az ad app permission", fakeResp{Stdout: ""})
	return fs
}

func goodGoogleFakeShell() *fakeShell {
	fs := newFakeShell()
	fs.setAvailable("gcloud", true)
	fs.respond("gcloud auth", fakeResp{Stdout: `[{"account":"u@x.com","status":"ACTIVE"}]`})
	fs.respond("gcloud config", fakeResp{Stdout: "my-current-project\n"})
	fs.respond("gcloud projects", fakeResp{Stdout: `[{"projectId":"apl-muthu-abc","name":"x"}]`})
	fs.respond("gcloud services", fakeResp{Stdout: ""})
	return fs
}

// mergedShell runs the given shells in order — for tests that need both az and gcloud in one run.
type mergedShell struct {
	ms  *fakeShell
	goo *fakeShell
}

func (m *mergedShell) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	if name == "az" {
		return m.ms.Run(ctx, name, args...)
	}
	return m.goo.Run(ctx, name, args...)
}
func (m *mergedShell) Available(name string) bool {
	if name == "az" {
		return m.ms.Available(name)
	}
	return m.goo.Available(name)
}

func TestSetup_Idempotent_AlreadyConfigured(t *testing.T) {
	setXDG(t)
	// Pre-seed config with valid-looking IDs.
	cfg := &config.Config{
		Google: config.ProviderConfig{
			ClientID:  "409786642553-abcdefghijklmnopqrstuvwxyz012345.apps.googleusercontent.com",
			ProjectID: "apl-muthu-abc",
		},
		Microsoft: config.ProviderConfig{
			ClientID: "11111111-2222-3333-4444-555555555555",
			Tenant:   "common",
		},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("seed: %v", err)
	}

	fs := newFakeShell() // no responses set; any call would be recorded
	p := &fakePrompter{}
	v := &fakeValidator{}

	var stdout, stderr bytes.Buffer
	opts := SetupOptions{
		Providers: []string{"google", "microsoft"},
		Shell:     fs, Prompter: p, Validator: v,
		Stdout: &stdout, Stderr: &stderr,
	}
	if err := RunSetup(context.Background(), opts); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	if len(fs.calls) != 0 {
		t.Errorf("expected no shell calls on idempotent run, got %d: %+v", len(fs.calls), fs.calls)
	}
	if len(p.inputCalls)+len(p.pickCalls)+len(p.confirmCalls) != 0 {
		t.Errorf("expected no prompts on idempotent run")
	}
	if !strings.Contains(stdout.String(), "already configured") {
		t.Errorf("expected 'already configured' message, got:\n%s", stdout.String())
	}
}

func TestSetup_Reconfigure_ForcesReprompting(t *testing.T) {
	setXDG(t)
	cfg := &config.Config{
		Google: config.ProviderConfig{
			ClientID:  "409786642553-abcdefghijklmnopqrstuvwxyz012345.apps.googleusercontent.com",
			ProjectID: "apl-muthu-abc",
		},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("seed: %v", err)
	}

	fs := goodGoogleFakeShell()
	p := &fakePrompter{
		picks:  []int{0, 0}, // account=0, project=0
		inputs: []string{writeGoogleClientJSON(t, "409786642553-zyxwvutsrqponmlkjihgfedcba012345.apps.googleusercontent.com")},
	}
	v := &fakeValidator{}

	opts := SetupOptions{
		Providers:   []string{"google"},
		Reconfigure: true,
		Shell:       fs, Prompter: p, Validator: v,
		Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{},
	}
	if err := RunSetup(context.Background(), opts); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	if len(fs.calls) == 0 {
		t.Fatal("expected shell calls when --reconfigure is set")
	}

	// New client ID should be written.
	got, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !strings.HasPrefix(got.Google.ClientID, "409786642553-zyxwvu") {
		t.Errorf("expected new clientID to be written, got %q", got.Google.ClientID)
	}
}

func TestSetup_Reset_WipesAndReruns(t *testing.T) {
	dir := setXDG(t)
	cfg := &config.Config{
		Google:    config.ProviderConfig{ClientID: "409786642553-abcdefghijklmnopqrstuvwxyz012345.apps.googleusercontent.com", ProjectID: "apl-muthu-abc"},
		Microsoft: config.ProviderConfig{ClientID: "11111111-2222-3333-4444-555555555555", Tenant: "common"},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cfgPath := filepath.Join(dir, "apl", "config.yaml")
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("precondition: %v", err)
	}

	// No shells needed — reset just wipes.
	fs := newFakeShell()
	p := &fakePrompter{confirms: []bool{true}} // confirm reset

	opts := SetupOptions{
		Providers: []string{}, // reset + no providers = exit after reset
		Reset:     true,
		Shell:     fs, Prompter: p, Validator: &fakeValidator{},
		Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{},
	}
	if err := RunSetup(context.Background(), opts); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	// After reset+no providers, config should be wiped (empty) or file absent.
	got, err := config.Load()
	if err == nil {
		if got.Google.ClientID != "" || got.Microsoft.ClientID != "" {
			t.Errorf("reset failed; config still has values: %+v", got)
		}
	}
}

func TestSetup_ProviderFilter_GoogleOnly(t *testing.T) {
	setXDG(t)

	fs := goodGoogleFakeShell()
	p := &fakePrompter{
		picks:  []int{0, 0},
		inputs: []string{writeGoogleClientJSON(t, "409786642553-abcdefghijklmnopqrstuvwxyz012345.apps.googleusercontent.com")},
	}
	v := &fakeValidator{}
	opts := SetupOptions{
		Providers: []string{"google"},
		Shell:     fs, Prompter: p, Validator: v,
		Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{},
	}
	if err := RunSetup(context.Background(), opts); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	for _, c := range fs.calls {
		if c.Name == "az" {
			t.Fatalf("google-only run should not call az, got %+v", c)
		}
	}
	got, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Google.ClientID == "" {
		t.Error("expected google client id to be written")
	}
	if got.Microsoft.ClientID != "" {
		t.Error("expected microsoft to be untouched")
	}
}

func TestSetup_AbortMidFlow_NoPartialConfig(t *testing.T) {
	setXDG(t)

	// Google shell is fine, but validator fails and user says no.
	fs := goodGoogleFakeShell()
	p := &fakePrompter{
		picks:    []int{0, 0},                                                                            // account=0, project=0
		inputs:   []string{writeGoogleClientJSON(t, "409786642553-abcdefghijklmnopqrstuvwxyz012345.apps.googleusercontent.com")},
		confirms: []bool{false}, // decline retry
	}
	v := &fakeValidator{errors: []error{someErr("invalid_client")}}

	opts := SetupOptions{
		Providers: []string{"google"},
		Shell:     fs, Prompter: p, Validator: v,
		Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{},
	}
	err := RunSetup(context.Background(), opts)
	if err == nil {
		t.Fatal("want error on abort")
	}
	// config should NOT exist / should be empty.
	got, loadErr := config.Load()
	if loadErr == nil {
		if got.Google.ClientID != "" {
			t.Errorf("partial config written: %+v", got)
		}
	}
}

type simpleErr string

func (e simpleErr) Error() string { return string(e) }
func someErr(s string) error      { return simpleErr(s) }
