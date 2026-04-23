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
	"sort"
	"strings"

	"github.com/muthuishere/all-purpose-login/internal/config"
)

// Microsoft Graph API well-known resource ID.
const graphAPIResourceID = "00000003-0000-0000-c000-000000000000"

// defaultGraphDelegatedScopes is the ordered list of delegated Graph scope
// NAMES granted to a freshly-created apl app registration. GUIDs are resolved
// at runtime via `az ad sp show` on the Graph service principal — we never
// hardcode them, because Microsoft occasionally adds/splits scopes.
var defaultGraphDelegatedScopes = []string{
	"User.Read",
	"offline_access",
	"openid",
	"email",
	"profile",
	"Mail.ReadWrite",
	"Mail.Send",
	"Calendars.ReadWrite",
	"Chat.ReadWrite",
	"ChatMessage.Send",
	"OnlineMeetings.Read",
}

// optInAdminConsentScope is offered via a prompt. Granting it requires tenant
// admin consent and covers meeting-recording access.
const optInAdminConsentScope = "OnlineMeetingRecording.Read.All"

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

// azAccountListEntry mirrors `az account list --all --output json` output. We
// only read the fields we care about.
type azAccountListEntry struct {
	ID       string `json:"id"` // subscription id
	Name     string `json:"name"`
	TenantID string `json:"tenantId"`
	State    string `json:"state"`
	IsDefault bool  `json:"isDefault"`
	User     struct {
		Name string `json:"name"`
	} `json:"user"`
}

type azAppCreateResp struct {
	AppID       string `json:"appId"`
	DisplayName string `json:"displayName"`
}

// graphPermScope is an entry in the Graph service principal's
// oauth2PermissionScopes (delegated) or appRoles (app-only) arrays, projected
// by az's --query.
type graphPermScope struct {
	Name string `json:"name"`
	ID   string `json:"id"`
	Type string `json:"type"` // "Scope" (delegated) or "Role" (app)
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

	// Account picker. List all az accounts (across tenants), dedup by
	// user.name, filter out Disabled subscriptions, and show a picker plus a
	// final "Sign in another tenant/account" option.
	listOut, _, _ := shell.Run(ctx, "az", "account", "list", "--all", "--output", "json")
	var allSubs []azAccountListEntry
	if listOut != "" {
		_ = json.Unmarshal([]byte(listOut), &allSubs)
	}
	// Filter Disabled.
	enabled := make([]azAccountListEntry, 0, len(allSubs))
	for _, s := range allSubs {
		if !strings.EqualFold(s.State, "Disabled") {
			enabled = append(enabled, s)
		}
	}
	// Dedup by user.name; remember first subscription per user.
	type userEntry struct {
		UserName string
		TenantID string
		SubID    string // first Enabled sub id, may be empty
	}
	var users []userEntry
	seen := map[string]int{}
	for _, s := range enabled {
		key := strings.ToLower(s.User.Name)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = len(users)
		users = append(users, userEntry{
			UserName: s.User.Name,
			TenantID: s.TenantID,
			SubID:    s.ID,
		})
	}
	// Make sure the currently-active user is in the list (could be a tenant-
	// level account with no subscriptions).
	if acct.User.Name != "" {
		if _, ok := seen[strings.ToLower(acct.User.Name)]; !ok {
			users = append([]userEntry{{
				UserName: acct.User.Name,
				TenantID: acct.TenantID,
			}}, users...)
		}
	}

	// Build the picker: active first.
	sort.SliceStable(users, func(i, j int) bool {
		ai := strings.EqualFold(users[i].UserName, acct.User.Name)
		aj := strings.EqualFold(users[j].UserName, acct.User.Name)
		if ai != aj {
			return ai
		}
		return users[i].UserName < users[j].UserName
	})
	acctOptions := make([]string, 0, len(users)+1)
	for _, u := range users {
		label := u.UserName
		if u.TenantID != "" {
			label += " (tenant " + u.TenantID + ")"
		}
		if strings.EqualFold(u.UserName, acct.User.Name) {
			label += "  [active]"
		}
		acctOptions = append(acctOptions, label)
	}
	acctOptions = append(acctOptions, "Sign in another tenant/account")

	fmt.Fprintln(stdout, "\nDetected az accounts:")
	choice := prompter.Pick("Pick account", acctOptions)
	if choice < 0 || choice >= len(acctOptions) {
		return config.ProviderConfig{}, fmt.Errorf("%w: invalid account choice", ErrProviderFailure)
	}

	if choice == len(acctOptions)-1 {
		tenant := strings.TrimSpace(prompter.Input("Tenant domain (e.g. reqsume.onmicrosoft.com, or 'common'): "))
		if tenant == "" {
			return config.ProviderConfig{}, fmt.Errorf("%w: empty tenant for sign-in", ErrProviderFailure)
		}
		fmt.Fprintf(stdout, "→ running `az login --tenant %s --allow-no-subscriptions` (browser opens)\n", tenant)
		if err := shell.RunInteractive(ctx, "az", "login", "--tenant", tenant, "--allow-no-subscriptions"); err != nil {
			return config.ProviderConfig{}, fmt.Errorf("%w: az login: %v", ErrNotLoggedIn, err)
		}
		// Re-read active identity.
		acctOut2, _, err := shell.Run(ctx, "az", "account", "show")
		if err != nil {
			return config.ProviderConfig{}, fmt.Errorf("%w: az account show after login: %v", ErrProviderFailure, err)
		}
		if err := json.Unmarshal([]byte(acctOut2), &acct); err != nil {
			return config.ProviderConfig{}, fmt.Errorf("%w: parse az account show: %v", ErrProviderFailure, err)
		}
	} else {
		picked := users[choice]
		if !strings.EqualFold(picked.UserName, acct.User.Name) && picked.SubID != "" {
			if _, serr, err := shell.Run(ctx, "az", "account", "set", "--subscription", picked.SubID); err != nil {
				return config.ProviderConfig{}, fmt.Errorf("%w: az account set: %v\n%s", ErrProviderFailure, err, serr)
			}
			// Refresh acct to reflect switch.
			acctOut2, _, err := shell.Run(ctx, "az", "account", "show")
			if err == nil {
				_ = json.Unmarshal([]byte(acctOut2), &acct)
			}
		}
	}

	fmt.Fprintf(stdout, "\n  Azure signed-in user: %s\n  Subscription:         %s\n  Tenant:               %s\n\n",
		acct.User.Name, acct.Name, acct.TenantID)
	if !prompter.Confirm("Continue as this user?") {
		return config.ProviderConfig{}, fmt.Errorf("%w: cancelled by user (run `az login` to switch accounts)", ErrNotLoggedIn)
	}

	// Fetch Graph service principal permission table (both delegated + app-role)
	// so scope names resolve to current GUIDs without any hardcoding.
	delegated, appRoles, err := fetchGraphPermissions(ctx, shell)
	if err != nil {
		return config.ProviderConfig{}, err
	}

	// Ask up-front about the admin-consent opt-in scope.
	includeRecording := prompter.Confirm(
		fmt.Sprintf("Include %s? (requires tenant admin consent)", optInAdminConsentScope))

	// List existing apl-* apps.
	out, _, err := shell.Run(ctx, "az", "ad", "app", "list",
		"--display-name", "apl",
		"--output", "json")
	if err != nil {
		return config.ProviderConfig{}, fmt.Errorf("%w: az ad app list: %v", ErrProviderFailure, err)
	}
	var apps []azAppListEntry
	if out != "" {
		if err := json.Unmarshal([]byte(out), &apps); err != nil {
			apps = nil
		}
	}
	var existing []azAppListEntry
	for _, a := range apps {
		if len(a.DisplayName) >= 3 && a.DisplayName[:3] == "apl" {
			existing = append(existing, a)
		}
	}

	// App-registration picker — list existing apl-* apps and a final
	// "Create a new app registration" entry. Re-running setup can thus pick
	// a different registration or make a fresh one.
	appOptions := make([]string, 0, len(existing)+1)
	for _, a := range existing {
		appOptions = append(appOptions, fmt.Sprintf("%s  [apl]", a.DisplayName))
	}
	appOptions = append(appOptions, "Create a new app registration")

	var appID string
	if len(existing) == 0 {
		fmt.Fprintln(stdout, "No existing apl-* app registrations in this tenant.")
	} else {
		fmt.Fprintln(stdout, "\nDetected apl-* app registrations in this tenant:")
		ac := prompter.Pick("Pick app registration", appOptions)
		if ac < 0 || ac >= len(appOptions) {
			return config.ProviderConfig{}, fmt.Errorf("%w: invalid app-registration choice", ErrProviderFailure)
		}
		if ac < len(existing) {
			appID = existing[ac].AppID
			fmt.Fprintf(stdout, "→ using %s (%s)\n", existing[ac].DisplayName, appID)
		}
	}

	if appID == "" {
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

	// Grant delegated Graph permissions using GUIDs looked up from the
	// service principal.
	scopeCount := 0
	for _, name := range defaultGraphDelegatedScopes {
		guid, ok := delegated[name]
		if !ok {
			return config.ProviderConfig{}, fmt.Errorf("%w: Graph delegated scope %q not found in service principal", ErrProviderFailure, name)
		}
		if _, serr, err := shell.Run(ctx, "az", "ad", "app", "permission", "add",
			"--id", appID,
			"--api", graphAPIResourceID,
			"--api-permissions", guid+"=Scope"); err != nil {
			return config.ProviderConfig{}, fmt.Errorf("%w: permission add %s: %v\n%s", ErrProviderFailure, name, err, serr)
		}
		scopeCount++
	}

	// Opt-in admin-consent scope — may be delegated or app-role.
	if includeRecording {
		guid, assignType, ok := lookupOptionalScope(optInAdminConsentScope, delegated, appRoles)
		if !ok {
			fmt.Fprintf(stderr, "warning: %s not exposed by current Graph SP; skipping\n", optInAdminConsentScope)
		} else {
			if _, serr, err := shell.Run(ctx, "az", "ad", "app", "permission", "add",
				"--id", appID,
				"--api", graphAPIResourceID,
				"--api-permissions", guid+"="+assignType); err != nil {
				return config.ProviderConfig{}, fmt.Errorf("%w: permission add %s: %v\n%s", ErrProviderFailure, optInAdminConsentScope, err, serr)
			}
			scopeCount++
			fmt.Fprintf(stderr,
				"note: %s requires tenant admin consent. As tenant admin, run:\n    az ad app permission admin-consent --id %s\n",
				optInAdminConsentScope, appID)
		}
	}

	fmt.Fprintf(stdout, "✓ Microsoft configured\n    appId: %s\n    tenant: common\n    scopes: %d Graph permissions\n",
		appID, scopeCount)

	return config.ProviderConfig{
		ClientID: appID,
		Tenant:   "common",
	}, nil
}

// fetchGraphPermissions runs `az ad sp show` on the Graph service principal
// and returns two name→GUID maps: delegated (oauth2PermissionScopes) and
// app-only roles (appRoles).
func fetchGraphPermissions(ctx context.Context, shell Shell) (delegated, appRoles map[string]string, err error) {
	// Delegated scopes.
	out, serr, err := shell.Run(ctx, "az", "ad", "sp", "show",
		"--id", graphAPIResourceID,
		"--query", "oauth2PermissionScopes[].{name:value, id:id, type:type}",
		"-o", "json")
	if err != nil {
		return nil, nil, fmt.Errorf("%w: az ad sp show oauth2PermissionScopes: %v\n%s", ErrProviderFailure, err, serr)
	}
	delegated = map[string]string{}
	if out != "" {
		var list []graphPermScope
		if jerr := json.Unmarshal([]byte(out), &list); jerr != nil {
			return nil, nil, fmt.Errorf("%w: parse oauth2PermissionScopes: %v", ErrProviderFailure, jerr)
		}
		for _, s := range list {
			if s.Name != "" && s.ID != "" {
				delegated[s.Name] = s.ID
			}
		}
	}

	// App roles (for scopes like OnlineMeetingRecording.Read.All that may be
	// application-only rather than delegated).
	out, serr, err = shell.Run(ctx, "az", "ad", "sp", "show",
		"--id", graphAPIResourceID,
		"--query", "appRoles[].{name:value, id:id, type:\"Role\"}",
		"-o", "json")
	if err != nil {
		// Non-fatal: app roles lookup is only used for optional scopes.
		return delegated, map[string]string{}, nil
	}
	appRoles = map[string]string{}
	if out != "" {
		var list []graphPermScope
		if jerr := json.Unmarshal([]byte(out), &list); jerr == nil {
			for _, s := range list {
				if s.Name != "" && s.ID != "" {
					appRoles[s.Name] = s.ID
				}
			}
		}
	}
	return delegated, appRoles, nil
}

// lookupOptionalScope checks delegated first, then app-role. Returns the
// GUID, the az --api-permissions assignment type ("Scope" or "Role"), and ok.
func lookupOptionalScope(name string, delegated, appRoles map[string]string) (string, string, bool) {
	if g, ok := delegated[name]; ok {
		return g, "Scope", true
	}
	if g, ok := appRoles[name]; ok {
		return g, "Role", true
	}
	return "", "", false
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
