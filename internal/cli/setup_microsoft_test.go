package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/muthuishere/all-purpose-login/internal/config"
)

func TestMicrosoft_Preflight_CLIMissing(t *testing.T) {
	fs := newFakeShell()
	fs.setAvailable("az", false)

	_, err := runMicrosoft(context.Background(), config.ProviderConfig{}, fs, &fakePrompter{}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("want error when az missing")
	}
	if !errors.Is(err, ErrMissingCLI) {
		t.Fatalf("want ErrMissingCLI, got %v", err)
	}
	if !strings.Contains(err.Error(), "az") {
		t.Fatalf("error should mention az, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "brew install azure-cli") && !strings.Contains(err.Error(), "install") {
		t.Fatalf("error should include install hint, got %q", err.Error())
	}
}

func TestMicrosoft_Preflight_NotLoggedIn(t *testing.T) {
	fs := newFakeShell()
	fs.setAvailable("az", true)
	fs.respond("az account", fakeResp{Stderr: "Please run 'az login'", Err: errors.New("exit 1")})

	_, err := runMicrosoft(context.Background(), config.ProviderConfig{}, fs, &fakePrompter{}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("want error when not logged in")
	}
	if !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("want ErrNotLoggedIn, got %v", err)
	}
	if !strings.Contains(err.Error(), "az login") {
		t.Fatalf("error should include login hint, got %q", err.Error())
	}
}

func TestMicrosoft_ReuseExistingApp(t *testing.T) {
	fs := newFakeShell()
	fs.setAvailable("az", true)
	fs.respond("az account", fakeResp{Stdout: `{"user":{"name":"u"}}`})
	fs.respond("az ad app list",
		fakeResp{Stdout: `[{"displayName":"apl-muthu","appId":"11111111-2222-3333-4444-555555555555"}]`})
	// permission add calls return empty
	fs.respond("az ad app permission", fakeResp{Stdout: ""})

	p := &fakePrompter{confirms: []bool{true}, picks: []int{0}} // confirm user, pick first existing

	pc, err := runMicrosoft(context.Background(), config.ProviderConfig{}, fs, p, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if pc.ClientID != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("want reused clientID, got %q", pc.ClientID)
	}
	if pc.Tenant != "common" {
		t.Fatalf("want tenant=common, got %q", pc.Tenant)
	}
	// Must NOT have called "az ad app create".
	for _, c := range fs.calls {
		if c.Name == "az" && len(c.Args) >= 3 && c.Args[0] == "ad" && c.Args[1] == "app" && c.Args[2] == "create" {
			t.Fatalf("az ad app create should not be called when reusing")
		}
	}
}

func TestMicrosoft_CreateNewApp(t *testing.T) {
	fs := newFakeShell()
	fs.setAvailable("az", true)
	fs.respond("az account", fakeResp{Stdout: `{"user":{"name":"u"}}`})
	fs.respond("az ad app list", fakeResp{Stdout: `[]`})
	fs.respond("az ad app",
		fakeResp{Stdout: `{"appId":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","displayName":"apl-muthu-host"}`})
	fs.respond("az ad app permission", fakeResp{Stdout: ""})

	p := &fakePrompter{confirms: []bool{true}}

	pc, err := runMicrosoft(context.Background(), config.ProviderConfig{}, fs, p, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if pc.ClientID != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Fatalf("want new clientID, got %q", pc.ClientID)
	}

	create := findCreateCall(fs)
	if create == nil {
		t.Fatal("expected az ad app create to be called")
	}
	if !hasArg(create.Args, "--sign-in-audience") {
		t.Fatal("create missing --sign-in-audience")
	}
	if !hasArg(create.Args, "AzureADandPersonalMicrosoftAccount") {
		t.Fatal("create missing audience value")
	}
	if !hasArg(create.Args, "--is-fallback-public-client") {
		t.Fatal("create missing --is-fallback-public-client")
	}
	if !hasArg(create.Args, "--public-client-redirect-uris") {
		t.Fatal("create missing --public-client-redirect-uris")
	}
	if !hasArg(create.Args, "http://localhost") {
		t.Fatal("create missing http://localhost")
	}
	// Display name should contain "apl-"
	foundName := false
	for i, a := range create.Args {
		if a == "--display-name" && i+1 < len(create.Args) && strings.HasPrefix(create.Args[i+1], "apl-") {
			foundName = true
		}
	}
	if !foundName {
		t.Fatalf("display name should start with apl-, got args=%v", create.Args)
	}
}

func TestMicrosoft_PermissionsAddedForAllScopes(t *testing.T) {
	fs := newFakeShell()
	fs.setAvailable("az", true)
	fs.respond("az account", fakeResp{Stdout: `{"user":{"name":"u"}}`})
	fs.respond("az ad app list", fakeResp{Stdout: `[]`})
	fs.respond("az ad app",
		fakeResp{Stdout: `{"appId":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"}`})
	fs.respond("az ad app permission", fakeResp{Stdout: ""})

	_, err := runMicrosoft(context.Background(), config.ProviderConfig{}, fs, &fakePrompter{confirms: []bool{true}}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	// Collect all permission add GUIDs passed via --api-permissions.
	seen := map[string]bool{}
	for _, c := range fs.calls {
		if c.Name != "az" || len(c.Args) < 3 {
			continue
		}
		if c.Args[0] != "ad" || c.Args[1] != "app" || c.Args[2] != "permission" {
			continue
		}
		// find --api value and --api-permissions value
		gotAPI := ""
		gotPerm := ""
		for i, a := range c.Args {
			if a == "--api" && i+1 < len(c.Args) {
				gotAPI = c.Args[i+1]
			}
			if a == "--api-permissions" && i+1 < len(c.Args) {
				gotPerm = c.Args[i+1]
			}
		}
		if gotAPI != "00000003-0000-0000-c000-000000000000" {
			t.Fatalf("wrong Graph API id: %q (args=%v)", gotAPI, c.Args)
		}
		// gotPerm is "<guid>=Scope"
		idx := strings.IndexByte(gotPerm, '=')
		if idx < 0 {
			continue
		}
		seen[gotPerm[:idx]] = true
	}

	wantGUIDs := []string{
		graphPermUserRead,
		graphPermMailRead,
		graphPermMailSend,
		graphPermCalendarsReadWrite,
		graphPermChatRead,
		graphPermChatMessageSend,
		graphPermOfflineAccess,
	}
	for _, g := range wantGUIDs {
		if !seen[g] {
			t.Errorf("missing permission GUID %s (seen=%v)", g, seen)
		}
	}
}

func TestMicrosoft_UserDeclinesAccountConfirmation(t *testing.T) {
	fs := newFakeShell()
	fs.setAvailable("az", true)
	fs.respond("az account", fakeResp{Stdout: `{"name":"Sub","tenantId":"t","user":{"name":"wrong@user.com"}}`})

	p := &fakePrompter{confirms: []bool{false}}
	out := &bytes.Buffer{}

	_, err := runMicrosoft(context.Background(), config.ProviderConfig{}, fs, p, out, &bytes.Buffer{})
	if err == nil {
		t.Fatal("want error when user declines")
	}
	if !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("want ErrNotLoggedIn on decline, got %v", err)
	}
	if !strings.Contains(out.String(), "wrong@user.com") {
		t.Fatalf("stdout should surface the signed-in user before prompting, got %q", out.String())
	}
	// Must NOT have called app list / create / permission after decline.
	for _, c := range fs.calls {
		if c.Name == "az" && len(c.Args) >= 2 && c.Args[0] == "ad" {
			t.Fatalf("no `az ad ...` should run after decline, got %v", c.Args)
		}
	}
}

func findCreateCall(fs *fakeShell) *fakeCall {
	for i := range fs.calls {
		c := &fs.calls[i]
		if c.Name == "az" && len(c.Args) >= 3 && c.Args[0] == "ad" && c.Args[1] == "app" && c.Args[2] == "create" {
			return c
		}
	}
	return nil
}
