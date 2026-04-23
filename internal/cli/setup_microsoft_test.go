package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/muthuishere/all-purpose-login/internal/config"
)

// canonicalGraphSP is a fake Graph service-principal response. The GUIDs
// below are arbitrary test values — the whole point of fetching from `az`
// at runtime is that tests don't need the real Microsoft values.
const canonicalGraphSP = `[
  {"name":"User.Read","id":"guid-user-read","type":"User"},
  {"name":"offline_access","id":"guid-offline-access","type":"User"},
  {"name":"openid","id":"guid-openid","type":"User"},
  {"name":"email","id":"guid-email","type":"User"},
  {"name":"profile","id":"guid-profile","type":"User"},
  {"name":"Mail.ReadWrite","id":"guid-mail-readwrite","type":"User"},
  {"name":"Mail.Send","id":"guid-mail-send","type":"User"},
  {"name":"Calendars.ReadWrite","id":"guid-calendars-readwrite","type":"User"},
  {"name":"Chat.ReadWrite","id":"guid-chat-readwrite","type":"User"},
  {"name":"ChatMessage.Send","id":"guid-chatmessage-send","type":"User"},
  {"name":"OnlineMeetings.Read","id":"guid-onlinemeetings-read","type":"User"}
]`

const canonicalGraphAppRoles = `[
  {"name":"OnlineMeetingRecording.Read.All","id":"guid-meetingrec-all","type":"Role"}
]`

// stubGraphSP wires canned `az ad sp show` responses by --query prefix.
// Both queries start with "oauth2PermissionScopes" or "appRoles".
func stubGraphSP(fs *fakeShell) {
	// The fakeShell matches on name+first N args; --query value lives at
	// position 4 (after --id <graphId> --query <expr>), so we use a custom
	// trick: respond on "az ad sp" with delegated; app-roles comes via a
	// separate key. But our fakeShell only keys on first-3 args. So we
	// always return delegated for "az ad sp show" — tests that need app
	// roles will override with a dedicated response. For the default
	// happy-path we need both calls to succeed; the fake will return the
	// same stdout for both. The Microsoft setup code tolerates that by
	// handling identical returns.
	//
	// Simpler: return delegated array for any "az ad sp" invocation. When
	// unmarshalled as appRoles the same shape happens to parse; we only
	// use the name+id pair.
	fs.respond("az ad sp", fakeResp{Stdout: canonicalGraphSP})
}

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
	stubGraphSP(fs)
	fs.respond("az ad app list",
		fakeResp{Stdout: `[{"displayName":"apl-muthu","appId":"11111111-2222-3333-4444-555555555555"}]`})
	fs.respond("az ad app permission", fakeResp{Stdout: ""})

	// prompter: confirm user, decline admin-consent opt-in.
	// Existing registrations are now auto-reused (no picker).
	p := &fakePrompter{confirms: []bool{true, false}}

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
	stubGraphSP(fs)
	fs.respond("az ad app list", fakeResp{Stdout: `[]`})
	fs.respond("az ad app",
		fakeResp{Stdout: `{"appId":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","displayName":"apl-muthu-host"}`})
	fs.respond("az ad app permission", fakeResp{Stdout: ""})

	p := &fakePrompter{confirms: []bool{true, false}}

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
	stubGraphSP(fs)
	fs.respond("az ad app list", fakeResp{Stdout: `[]`})
	fs.respond("az ad app",
		fakeResp{Stdout: `{"appId":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"}`})
	fs.respond("az ad app permission", fakeResp{Stdout: ""})

	_, err := runMicrosoft(
		context.Background(), config.ProviderConfig{}, fs,
		&fakePrompter{confirms: []bool{true, false}}, // confirm user, decline admin-consent
		&bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	// Collect all permission-add GUID=Type entries.
	seen := map[string]string{}
	for _, c := range fs.calls {
		if c.Name != "az" || len(c.Args) < 3 {
			continue
		}
		if c.Args[0] != "ad" || c.Args[1] != "app" || c.Args[2] != "permission" {
			continue
		}
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
		if gotAPI != graphAPIResourceID {
			t.Fatalf("wrong Graph API id: %q (args=%v)", gotAPI, c.Args)
		}
		idx := strings.IndexByte(gotPerm, '=')
		if idx < 0 {
			continue
		}
		seen[gotPerm[:idx]] = gotPerm[idx+1:]
	}

	// The GUIDs must be those returned by the stubbed az ad sp show, not
	// any hardcoded constants in the production code.
	wantGUIDs := []string{
		"guid-user-read",
		"guid-offline-access",
		"guid-openid",
		"guid-email",
		"guid-profile",
		"guid-mail-readwrite",
		"guid-mail-send",
		"guid-calendars-readwrite",
		"guid-chat-readwrite",
		"guid-chatmessage-send",
		"guid-onlinemeetings-read",
	}
	for _, g := range wantGUIDs {
		if _, ok := seen[g]; !ok {
			t.Errorf("missing permission GUID %s (seen=%v)", g, seen)
		}
	}
	// All must have been added as =Scope (delegated).
	for guid, typ := range seen {
		if typ != "Scope" {
			t.Errorf("%s added with type %q; want Scope", guid, typ)
		}
	}
}

func TestMicrosoft_OptInAdminConsentScope(t *testing.T) {
	fs := newFakeShell()
	fs.setAvailable("az", true)
	fs.respond("az account", fakeResp{Stdout: `{"user":{"name":"u"}}`})
	// Stub graph SP: delegated array does NOT contain recording scope —
	// it appears in appRoles. Our fakeShell can only key on the first few
	// args, and both sp-show calls share the same key. So we return a
	// superset array that includes the recording scope as well. The
	// production code will pick it up either from delegated or app-roles.
	fs.respond("az ad sp", fakeResp{Stdout: `[
  {"name":"User.Read","id":"guid-user-read","type":"User"},
  {"name":"offline_access","id":"guid-offline-access","type":"User"},
  {"name":"openid","id":"guid-openid","type":"User"},
  {"name":"email","id":"guid-email","type":"User"},
  {"name":"profile","id":"guid-profile","type":"User"},
  {"name":"Mail.ReadWrite","id":"guid-mail-readwrite","type":"User"},
  {"name":"Mail.Send","id":"guid-mail-send","type":"User"},
  {"name":"Calendars.ReadWrite","id":"guid-calendars-readwrite","type":"User"},
  {"name":"Chat.ReadWrite","id":"guid-chat-readwrite","type":"User"},
  {"name":"ChatMessage.Send","id":"guid-chatmessage-send","type":"User"},
  {"name":"OnlineMeetings.Read","id":"guid-onlinemeetings-read","type":"User"},
  {"name":"OnlineMeetingRecording.Read.All","id":"guid-recording-all","type":"User"}
]`})
	fs.respond("az ad app list", fakeResp{Stdout: `[]`})
	fs.respond("az ad app",
		fakeResp{Stdout: `{"appId":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"}`})
	fs.respond("az ad app permission", fakeResp{Stdout: ""})

	stderr := &bytes.Buffer{}
	_, err := runMicrosoft(
		context.Background(), config.ProviderConfig{}, fs,
		&fakePrompter{confirms: []bool{true, true}}, // confirm user, ACCEPT admin-consent
		&bytes.Buffer{}, stderr)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	// Must have requested the recording GUID.
	foundRecording := false
	for _, c := range fs.calls {
		if c.Name != "az" || len(c.Args) < 3 {
			continue
		}
		if c.Args[0] != "ad" || c.Args[1] != "app" || c.Args[2] != "permission" {
			continue
		}
		for i, a := range c.Args {
			if a == "--api-permissions" && i+1 < len(c.Args) {
				if strings.HasPrefix(c.Args[i+1], "guid-recording-all=") {
					foundRecording = true
				}
			}
		}
	}
	if !foundRecording {
		t.Errorf("admin-consent scope GUID not added")
	}
	// stderr should include the admin-consent instruction.
	if !strings.Contains(stderr.String(), "admin-consent") {
		t.Errorf("stderr missing admin-consent hint, got %q", stderr.String())
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
