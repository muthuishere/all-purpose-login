package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/muthuishere/all-purpose-login/internal/config"
)

func TestGoogle_Preflight_CLIMissing(t *testing.T) {
	fs := newFakeShell()
	fs.setAvailable("gcloud", false)

	_, err := runGoogle(context.Background(), config.ProviderConfig{}, fs, &fakePrompter{}, &fakeValidator{}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("want error when gcloud missing")
	}
	if !errors.Is(err, ErrMissingCLI) {
		t.Fatalf("want ErrMissingCLI, got %v", err)
	}
	if !strings.Contains(err.Error(), "gcloud") {
		t.Fatalf("error should mention gcloud, got %q", err.Error())
	}
}

func TestGoogle_Preflight_NotLoggedIn(t *testing.T) {
	fs := newFakeShell()
	fs.setAvailable("gcloud", true)
	fs.respond("gcloud auth", fakeResp{Stdout: `[]`}) // empty list → not logged in

	_, err := runGoogle(context.Background(), config.ProviderConfig{}, fs, &fakePrompter{}, &fakeValidator{}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("want error when gcloud not logged in")
	}
	if !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("want ErrNotLoggedIn, got %v", err)
	}
	if !strings.Contains(err.Error(), "gcloud auth login") {
		t.Fatalf("error should include login hint, got %q", err.Error())
	}
}

func TestGoogle_ReuseExistingProject(t *testing.T) {
	fs := newFakeShell()
	fs.setAvailable("gcloud", true)
	fs.respond("gcloud auth", fakeResp{Stdout: `[{"account":"u@x.com","status":"ACTIVE"}]`})
	fs.respond("gcloud config", fakeResp{Stdout: "my-current-project\n"})
	fs.respond("gcloud projects", fakeResp{Stdout: `[{"projectId":"apl-muthu-abc","name":"apl muthu"},{"projectId":"other","name":"other"}]`})
	fs.respond("gcloud services", fakeResp{Stdout: ""})

	p := &fakePrompter{
		confirms: []bool{true}, // confirm active account
		inputs:   []string{"409786642553-abcdefghijklmnopqrstuvwxyz012345.apps.googleusercontent.com"},
	}
	v := &fakeValidator{}

	pc, err := runGoogle(context.Background(), config.ProviderConfig{}, fs, p, v, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if pc.ProjectID != "apl-muthu-abc" {
		t.Fatalf("want reused project, got %q", pc.ProjectID)
	}
	if pc.ClientID == "" {
		t.Fatalf("want clientID set, got empty")
	}
	// Must NOT have called gcloud projects create.
	for _, c := range fs.calls {
		if c.Name == "gcloud" && len(c.Args) >= 2 && c.Args[0] == "projects" && c.Args[1] == "create" {
			t.Fatalf("gcloud projects create should not be called when reusing")
		}
	}
	// Services must have been enabled with the 3 APIs.
	svc := fs.findCall("gcloud", "services")
	if svc == nil {
		t.Fatal("expected gcloud services enable to be called")
	}
	joined := strings.Join(svc.Args, " ")
	if !strings.Contains(joined, "gmail.googleapis.com") ||
		!strings.Contains(joined, "calendar-json.googleapis.com") ||
		!strings.Contains(joined, "people.googleapis.com") ||
		!strings.Contains(joined, "drive.googleapis.com") {
		t.Fatalf("services enable missing expected APIs, args=%v", svc.Args)
	}
}

func TestGoogle_CreateNewProject(t *testing.T) {
	fs := newFakeShell()
	fs.setAvailable("gcloud", true)
	fs.respond("gcloud auth", fakeResp{Stdout: `[{"account":"u@x.com","status":"ACTIVE"}]`})
	fs.respond("gcloud config", fakeResp{Stdout: "(unset)\n"})
	fs.respond("gcloud projects", fakeResp{Stdout: `[]`})
	fs.respond("gcloud services", fakeResp{Stdout: ""})

	// confirms: [account-confirm=true, create-new=true]
	p := &fakePrompter{
		confirms: []bool{true, true},
		inputs:   []string{"409786642553-abcdefghijklmnopqrstuvwxyz012345.apps.googleusercontent.com"},
	}
	v := &fakeValidator{}

	pc, err := runGoogle(context.Background(), config.ProviderConfig{}, fs, p, v, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.HasPrefix(pc.ProjectID, "apl-") {
		t.Fatalf("want project starting with apl-, got %q", pc.ProjectID)
	}
	// Find create call
	var create *fakeCall
	for i := range fs.calls {
		c := &fs.calls[i]
		if c.Name == "gcloud" && len(c.Args) >= 2 && c.Args[0] == "projects" && c.Args[1] == "create" {
			create = c
			break
		}
	}
	if create == nil {
		t.Fatal("expected gcloud projects create")
	}
}

func TestGoogle_ClientIDValidationFails_ThenAbort(t *testing.T) {
	fs := newFakeShell()
	fs.setAvailable("gcloud", true)
	fs.respond("gcloud auth", fakeResp{Stdout: `[{"account":"u@x.com","status":"ACTIVE"}]`})
	fs.respond("gcloud config", fakeResp{Stdout: "my-current-project\n"})
	fs.respond("gcloud projects", fakeResp{Stdout: `[{"projectId":"apl-muthu-abc","name":"x"}]`})
	fs.respond("gcloud services", fakeResp{Stdout: ""})

	// confirms: [account-confirm=true, retry-after-validator-fail=false]
	p := &fakePrompter{
		inputs:   []string{"409786642553-abcdefghijklmnopqrstuvwxyz012345.apps.googleusercontent.com"},
		confirms: []bool{true, false},
	}
	v := &fakeValidator{errors: []error{errors.New("invalid_client")}}

	_, err := runGoogle(context.Background(), config.ProviderConfig{}, fs, p, v, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("want error when validator fails and user declines retry")
	}
	if !errors.Is(err, ErrProviderFailure) {
		t.Fatalf("want ErrProviderFailure, got %v", err)
	}
}

func TestGoogle_WalkthroughIncludesProjectURL(t *testing.T) {
	fs := newFakeShell()
	fs.setAvailable("gcloud", true)
	fs.respond("gcloud auth", fakeResp{Stdout: `[{"account":"u@x.com","status":"ACTIVE"}]`})
	fs.respond("gcloud config", fakeResp{Stdout: "my-current-project\n"})
	fs.respond("gcloud projects", fakeResp{Stdout: `[{"projectId":"apl-muthu-abc","name":"x"}]`})
	fs.respond("gcloud services", fakeResp{Stdout: ""})

	var stdout bytes.Buffer
	p := &fakePrompter{
		confirms: []bool{true},
		inputs:   []string{"409786642553-abcdefghijklmnopqrstuvwxyz012345.apps.googleusercontent.com"},
	}
	v := &fakeValidator{}

	_, err := runGoogle(context.Background(), config.ProviderConfig{}, fs, p, v, &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "project=apl-muthu-abc") {
		t.Errorf("walkthrough output should include project URL, got:\n%s", out)
	}
	if !strings.Contains(out, "console.cloud.google.com") {
		t.Errorf("walkthrough should reference GCP console URL")
	}
	if !strings.Contains(out, "https://www.googleapis.com/auth/drive.readonly") {
		t.Errorf("walkthrough should list drive.readonly scope")
	}
}
