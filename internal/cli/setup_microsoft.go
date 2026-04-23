package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"

	"github.com/muthuishere/all-purpose-login/internal/config"
)

// Microsoft Graph API well-known resource ID.
const graphAPIResourceID = "00000003-0000-0000-c000-000000000000"

// Delegated scope GUIDs for Microsoft Graph.
// Source: Microsoft Graph delegated permissions (oauth2PermissionScopes on the
// Graph service principal). The canonical values are in the spec at
// docs/specs/spec-setup-bootstrapper.md §SETUP-4. For one-off verification:
//
//	az ad sp show --id 00000003-0000-0000-c000-000000000000 \
//	    --query "oauth2PermissionScopes[?value=='Mail.Read'].id" -o tsv
const (
	graphPermUserRead           = "e1fe6dd8-ba31-4d61-89e7-88639da4683d"
	graphPermMailRead           = "570282fd-fa5c-430d-a7fd-fc8dc98a9dca"
	graphPermMailSend           = "e383f46e-2787-4529-855e-0e479a3ffac0"
	graphPermCalendarsReadWrite = "1ec239c2-d7c9-4623-a91a-a9775856bb36"
	graphPermChatRead           = "f501c180-9344-439a-bca0-6cbf209fd270"
	graphPermChatMessageSend    = "9ff7295e-131b-4d94-90e1-69fde507ac11"
	graphPermOfflineAccess      = "7427e0e9-2fba-42fe-b0c0-848c9e6a8182"
)

// graphDelegatedPermissions is the ordered list of delegated scopes granted to
// a freshly created apl app registration. Values are "<guid>=Scope" — az's
// expected --api-permissions syntax.
var graphDelegatedPermissions = []struct {
	Name string
	GUID string
}{
	{"User.Read", graphPermUserRead},
	{"Mail.Read", graphPermMailRead},
	{"Mail.Send", graphPermMailSend},
	{"Calendars.ReadWrite", graphPermCalendarsReadWrite},
	{"Chat.Read", graphPermChatRead},
	{"ChatMessage.Send", graphPermChatMessageSend},
	{"offline_access", graphPermOfflineAccess},
}

// ErrMissingCLI / ErrNotLoggedIn / ErrProviderFailure are sentinel errors
// surfaced by setup flows so the orchestrator can map them to exit codes.
var (
	ErrMissingCLI      = errors.New("missing CLI dependency")
	ErrNotLoggedIn     = errors.New("CLI not logged in")
	ErrProviderFailure = errors.New("provider failure")
)

type azAppListEntry struct {
	DisplayName string `json:"displayName"`
	AppID       string `json:"appId"`
}

type azAccountShow struct {
	Name     string `json:"name"`
	TenantID string `json:"tenantId"`
	User     struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"user"`
}

type azAppCreateResp struct {
	AppID       string `json:"appId"`
	DisplayName string `json:"displayName"`
}

func runMicrosoft(
	ctx context.Context,
	current config.ProviderConfig,
	shell Shell,
	prompter Prompter,
	stdout, stderr io.Writer,
) (config.ProviderConfig, error) {
	fmt.Fprintln(stdout, "→ Microsoft setup")

	// Preflight: CLI available?
	if !shell.Available("az") {
		return config.ProviderConfig{}, fmt.Errorf(
			"%w: Microsoft setup needs the Azure CLI\n    Install: https://learn.microsoft.com/cli/azure/install-azure-cli\n    Or:      brew install azure-cli",
			ErrMissingCLI)
	}

	// Preflight: logged in? Show who before doing anything.
	acctOut, _, err := shell.Run(ctx, "az", "account", "show")
	if err != nil {
		return config.ProviderConfig{}, fmt.Errorf(
			"%w: Azure CLI is installed but not logged in\n    Run: az login",
			ErrNotLoggedIn)
	}
	var acct azAccountShow
	if err := json.Unmarshal([]byte(acctOut), &acct); err != nil {
		return config.ProviderConfig{}, fmt.Errorf("%w: parse az account show: %v", ErrProviderFailure, err)
	}
	fmt.Fprintf(stdout, "\n  Azure signed-in user: %s\n  Subscription:         %s\n  Tenant:               %s\n\n",
		acct.User.Name, acct.Name, acct.TenantID)
	if !prompter.Confirm("Continue as this user?") {
		return config.ProviderConfig{}, fmt.Errorf("%w: cancelled by user (run `az login` to switch accounts)", ErrNotLoggedIn)
	}

	// List existing apl-* apps.
	out, _, err := shell.Run(ctx, "az", "ad", "app", "list",
		"--display-name", "apl",
		"--output", "json")
	if err != nil {
		return config.ProviderConfig{}, fmt.Errorf("%w: az ad app list: %v", ErrProviderFailure, err)
	}
	var apps []azAppListEntry
	if out != "" {
		// Tolerate empty bodies.
		if err := json.Unmarshal([]byte(out), &apps); err != nil {
			// Treat as no apps.
			apps = nil
		}
	}
	// Filter to those starting with "apl-" or named "apl".
	var existing []azAppListEntry
	for _, a := range apps {
		if len(a.DisplayName) >= 3 && a.DisplayName[:3] == "apl" {
			existing = append(existing, a)
		}
	}

	var appID string
	if len(existing) > 0 {
		opts := make([]string, 0, len(existing)+1)
		for _, a := range existing {
			opts = append(opts, fmt.Sprintf("%s (%s)", a.DisplayName, a.AppID))
		}
		opts = append(opts, "Create a new one")
		choice := prompter.Pick("Existing apl app registrations in this tenant:", opts)
		if choice < len(existing) {
			appID = existing[choice].AppID
			fmt.Fprintf(stdout, "→ reusing %s (%s)\n", existing[choice].DisplayName, appID)
		}
	}

	if appID == "" {
		// Create new app.
		displayName := generateAppDisplayName()
		fmt.Fprintf(stdout, "→ creating app registration %s\n", displayName)
		out, serr, err := shell.Run(ctx, "az", "ad", "app", "create",
			"--display-name", displayName,
			"--sign-in-audience", "AzureADandPersonalMicrosoftAccount",
			"--is-fallback-public-client", "true",
			"--public-client-redirect-uris", "http://localhost")
		if err != nil {
			return config.ProviderConfig{}, fmt.Errorf("%w: az ad app create: %v\n%s", ErrProviderFailure, err, serr)
		}
		var created azAppCreateResp
		if err := json.Unmarshal([]byte(out), &created); err != nil {
			return config.ProviderConfig{}, fmt.Errorf("%w: parse az ad app create output: %v", ErrProviderFailure, err)
		}
		if created.AppID == "" {
			return config.ProviderConfig{}, fmt.Errorf("%w: az ad app create returned no appId", ErrProviderFailure)
		}
		appID = created.AppID
	}

	// Grant delegated Graph permissions.
	for _, p := range graphDelegatedPermissions {
		if _, serr, err := shell.Run(ctx, "az", "ad", "app", "permission", "add",
			"--id", appID,
			"--api", graphAPIResourceID,
			"--api-permissions", p.GUID+"=Scope"); err != nil {
			return config.ProviderConfig{}, fmt.Errorf("%w: permission add %s: %v\n%s", ErrProviderFailure, p.Name, err, serr)
		}
	}

	fmt.Fprintf(stdout, "✓ Microsoft configured\n    appId: %s\n    tenant: common\n    scopes: %d delegated Graph permissions\n",
		appID, len(graphDelegatedPermissions))

	return config.ProviderConfig{
		ClientID: appID,
		Tenant:   "common",
	}, nil
}

func generateAppDisplayName() string {
	u, _ := user.Current()
	whoami := "user"
	if u != nil && u.Username != "" {
		whoami = sanitizeLabel(u.Username)
	}
	host, _ := os.Hostname()
	hostPart := "host"
	if host != "" {
		hostPart = sanitizeLabel(host)
	}
	// Append short random suffix to avoid collisions.
	var b [4]byte
	_, _ = rand.Read(b[:])
	suffix := hex.EncodeToString(b[:])
	return fmt.Sprintf("apl-%s-%s-%s", whoami, hostPart, suffix)
}

func sanitizeLabel(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			out = append(out, r)
		case r == '\\', r == '/':
			out = append(out, '-')
		}
	}
	if len(out) == 0 {
		return "x"
	}
	return string(out)
}
