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

func TestGoogle_Preflight_NoAccounts_CollapsesToSignIn(t *testing.T) {
	fs := newFakeShell()
	fs.setAvailable("gcloud", true)
	fs.respond("gcloud auth", fakeResp{Stdout: `[]`}) // empty list
	fs.respond("gcloud config", fakeResp{Stdout: "(unset)\n"})
	fs.respond("gcloud projects", fakeResp{Stdout: `[]`})
	fs.respond("gcloud services", fakeResp{Stdout: ""})

	p := &fakePrompter{
		// Only 1 option: sign in. Then email, then project create confirm, then client id.
		picks:    []int{0},
		inputs:   []string{"new@x.com", "409786642553-abcdefghijklmnopqrstuvwxyz012345.apps.googleusercontent.com"},
		confirms: []bool{true}, // confirm project creation
	}
	v := &fakeValidator{}
	_, err := runGoogle(context.Background(), config.ProviderConfig{}, fs, p, v, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// Verify interactive login was invoked with the typed email.
	var loginCall *fakeCall
	for i := range fs.calls {
		c := &fs.calls[i]
		if c.Interactive && c.Name == "gcloud" && len(c.Args) >= 2 && c.Args[0] == "auth" && c.Args[1] == "login" {
			loginCall = c
			break
		}
	}
	if loginCall == nil {
		t.Fatal("expected interactive `gcloud auth login` when signing in new account")
	}
	if len(loginCall.Args) < 3 || loginCall.Args[2] != "new@x.com" {
		t.Fatalf("login call args: %v", loginCall.Args)
	}
}

func TestGoogle_AccountPicker_ExistingSelected(t *testing.T) {
	fs := newFakeShell()
	fs.setAvailable("gcloud", true)
	// Two accounts: active first, inactive second. We'll pick the inactive one.
	fs.respond("gcloud auth", fakeResp{Stdout: `[{"account":"a@x.com","status":"ACTIVE"},{"account":"b@x.com","status":""}]`})
	fs.respond("gcloud config", fakeResp{Stdout: "my-current-project\n"})
	fs.respond("gcloud projects", fakeResp{Stdout: `[{"projectId":"apl-muthu-abc","name":"x"}]`})
	fs.respond("gcloud services", fakeResp{Stdout: ""})

	p := &fakePrompter{
		// pick index 1 (b@x.com), then pick project index 0
		picks:    []int{1, 0},
		inputs:   []string{"409786642553-abcdefghijklmnopqrstuvwxyz012345.apps.googleusercontent.com"},
		confirms: []bool{},
	}
	v := &fakeValidator{}
	_, err := runGoogle(context.Background(), config.ProviderConfig{}, fs, p, v, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// Must have called `gcloud config set account b@x.com`.
	var setCall *fakeCall
	for i := range fs.calls {
		c := &fs.calls[i]
		if c.Name == "gcloud" && len(c.Args) >= 3 && c.Args[0] == "config" && c.Args[1] == "set" && c.Args[2] == "account" {
			setCall = c
			break
		}
	}
	if setCall == nil {
		t.Fatal("expected `gcloud config set account` when switching to non-active account")
	}
	if len(setCall.Args) < 4 || setCall.Args[3] != "b@x.com" {
		t.Fatalf("set-account args: %v", setCall.Args)
	}
	// Must NOT have invoked interactive auth login.
	for _, c := range fs.calls {
		if c.Interactive && c.Name == "gcloud" && len(c.Args) >= 2 && c.Args[0] == "auth" && c.Args[1] == "login" {
			t.Fatalf("should not have invoked `gcloud auth login` when picking existing account")
		}
	}
}

func TestGoogle_AccountPicker_SignInNew(t *testing.T) {
	fs := newFakeShell()
	fs.setAvailable("gcloud", true)
	fs.respond("gcloud auth", fakeResp{Stdout: `[{"account":"a@x.com","status":"ACTIVE"}]`})
	fs.respond("gcloud config", fakeResp{Stdout: "my-current-project\n"})
	fs.respond("gcloud projects", fakeResp{Stdout: `[{"projectId":"apl-muthu-abc","name":"x"}]`})
	fs.respond("gcloud services", fakeResp{Stdout: ""})

	p := &fakePrompter{
		// pick last option (sign in), then project, then client id
		picks:  []int{1, 0},
		inputs: []string{"new@x.com", "409786642553-abcdefghijklmnopqrstuvwxyz012345.apps.googleusercontent.com"},
	}
	v := &fakeValidator{}
	_, err := runGoogle(context.Background(), config.ProviderConfig{}, fs, p, v, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	var loginCall, setCall *fakeCall
	for i := range fs.calls {
		c := &fs.calls[i]
		if c.Interactive && c.Name == "gcloud" && len(c.Args) >= 2 && c.Args[0] == "auth" && c.Args[1] == "login" {
			loginCall = c
		}
		if c.Name == "gcloud" && len(c.Args) >= 3 && c.Args[0] == "config" && c.Args[1] == "set" && c.Args[2] == "account" {
			setCall = c
		}
	}
	if loginCall == nil || len(loginCall.Args) < 3 || loginCall.Args[2] != "new@x.com" {
		t.Fatalf("expected interactive `gcloud auth login new@x.com`, got %+v", loginCall)
	}
	if setCall == nil || len(setCall.Args) < 4 || setCall.Args[3] != "new@x.com" {
		t.Fatalf("expected `gcloud config set account new@x.com`, got %+v", setCall)
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
		// picks: [account=0 (u@x.com active), project=0 (apl-muthu-abc)]
		picks:  []int{0, 0},
		inputs: []string{"409786642553-abcdefghijklmnopqrstuvwxyz012345.apps.googleusercontent.com"},
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

	// picks: [account=0 (u@x.com)], confirms: [create-new=true]
	p := &fakePrompter{
		picks:    []int{0},
		confirms: []bool{true},
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

	// picks: [account=0, project=0]; confirms: [retry-after-validator-fail=false]
	p := &fakePrompter{
		picks:    []int{0, 0},
		inputs:   []string{"409786642553-abcdefghijklmnopqrstuvwxyz012345.apps.googleusercontent.com"},
		confirms: []bool{false},
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
	// Simulate common failure mode: user lacks IAP admin — brand auto-create
	// fails so Step 1 is printed in full with the scope list.
	fs.respond("gcloud alpha", fakeResp{Stderr: "permission denied", Err: errors.New("exit 1")})

	var stdout bytes.Buffer
	p := &fakePrompter{
		picks:  []int{0, 0}, // account=0, project=0
		inputs: []string{"409786642553-abcdefghijklmnopqrstuvwxyz012345.apps.googleusercontent.com"},
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
