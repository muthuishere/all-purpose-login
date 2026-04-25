package setup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"strings"
)

const graphAPIResourceID = "00000003-0000-0000-c000-000000000000"

var msDelegatedScopes = []string{
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

const msOptInScope = "OnlineMeetingRecording.Read.All"

// MicrosoftSteps returns the ordered step list for `apl setup ms`.
func MicrosoftSteps() []Step {
	return []Step{
		&msAccountStep{},
		&msAppPickStep{},
		&msAppCreateStep{},
		&msPermissionsStep{},
		&msAdminConsentNotifyStep{},
	}
}

type azAccountShow struct {
	Name     string `json:"name"`
	TenantID string `json:"tenantId"`
	User     struct {
		Name string `json:"name"`
	} `json:"user"`
}

type azAppListEntry struct {
	DisplayName string `json:"displayName"`
	AppID       string `json:"appId"`
}

type azAppCreateResp struct {
	AppID       string `json:"appId"`
	DisplayName string `json:"displayName"`
}

type graphPermScope struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

func appIDFromState(s *State) string {
	if st, ok := s.Steps["app_pick"]; ok && st != nil {
		if v, ok := st.Output["app_id"]; ok && v != "" {
			return v
		}
	}
	if st, ok := s.Steps["app_create"]; ok && st != nil {
		if v, ok := st.Output["app_id"]; ok {
			return v
		}
	}
	return ""
}

func tenantFromState(s *State) string {
	if st, ok := s.Steps["account"]; ok && st != nil {
		if v, ok := st.Output["tenant"]; ok {
			return v
		}
	}
	return ""
}

// --- account ----------------------------------------------------------------

type msAccountStep struct{}

func (msAccountStep) ID() string { return "account" }
func (msAccountStep) Run(ctx context.Context, s *State, env Env) (Action, error) {
	if !env.Shell.Available("az") {
		return Action{
			Kind:       ActionFailed,
			FailReason: "az CLI not found. Install: https://learn.microsoft.com/cli/azure/install-azure-cli",
		}, nil
	}
	out, _, err := env.Shell.Run(ctx, "az", "account", "show")
	if err != nil {
		return Action{
			Kind:    ActionAwaitHuman,
			URL:     "https://login.microsoftonline.com/",
			Message: "Run interactively: `az login` (or `az login --tenant <tenant>` for a specific tenant). Then resume.",
		}, nil
	}
	var acct azAccountShow
	if err := json.Unmarshal([]byte(out), &acct); err != nil {
		return Action{}, fmt.Errorf("parse az account show: %w", err)
	}
	want := env.Inputs["account"]
	if want != "" && !strings.EqualFold(acct.User.Name, want) {
		return Action{
			Kind:    ActionAwaitHuman,
			URL:     "https://login.microsoftonline.com/",
			Message: fmt.Sprintf("Active az account is %s but --account=%s. Run `az login` as %s and resume.", acct.User.Name, want, want),
		}, nil
	}
	return Action{
		Kind: ActionDone,
		Output: map[string]string{
			"account": acct.User.Name,
			"tenant":  acct.TenantID,
		},
	}, nil
}

// --- pick existing app or skip to create -----------------------------------

type msAppPickStep struct{}

func (msAppPickStep) ID() string { return "app_pick" }
func (msAppPickStep) Run(ctx context.Context, s *State, env Env) (Action, error) {
	want := env.Inputs["app_id"]
	if want != "" {
		return Action{
			Kind:   ActionDone,
			Output: map[string]string{"app_id": want},
		}, nil
	}
	out, _, err := env.Shell.Run(ctx, "az", "ad", "app", "list",
		"--display-name", "apl",
		"--output", "json")
	if err != nil {
		return Action{}, fmt.Errorf("az ad app list: %w", err)
	}
	var apps []azAppListEntry
	if out != "" {
		_ = json.Unmarshal([]byte(out), &apps)
	}
	var existing []string
	for _, a := range apps {
		if strings.HasPrefix(a.DisplayName, "apl") {
			existing = append(existing, fmt.Sprintf("%s=%s", a.DisplayName, a.AppID))
		}
	}
	if len(existing) > 0 {
		// Inform but do not block: caller can pass --app-id=<existing> or omit
		// (app_create will run).
		return Action{
			Kind: ActionDone,
			Output: map[string]string{
				"app_id":         "", // empty signals "create new"
				"existing_hint":  strings.Join(existing, ", "),
			},
		}, nil
	}
	return Action{Kind: ActionDone, Output: map[string]string{"app_id": ""}}, nil
}

// --- create app -------------------------------------------------------------

type msAppCreateStep struct{}

func (msAppCreateStep) ID() string { return "app_create" }
func (msAppCreateStep) Run(ctx context.Context, s *State, env Env) (Action, error) {
	if existing := appIDFromState(s); existing != "" && s.Steps["app_pick"] != nil && s.Steps["app_pick"].Output["app_id"] != "" {
		// Caller supplied --app-id; skip create.
		return Action{Kind: ActionDone, Output: map[string]string{"app_id": existing}}, nil
	}
	displayName := generateAppDisplayName(s.Label)
	out, serr, err := env.Shell.Run(ctx, "az", "ad", "app", "create",
		"--display-name", displayName,
		"--sign-in-audience", "AzureADandPersonalMicrosoftAccount",
		"--is-fallback-public-client", "true",
		"--public-client-redirect-uris", "http://localhost")
	if err != nil {
		return Action{}, fmt.Errorf("az ad app create: %w (%s)", err, serr)
	}
	var created azAppCreateResp
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		return Action{}, fmt.Errorf("parse az ad app create: %w", err)
	}
	if created.AppID == "" {
		return Action{Kind: ActionFailed, FailReason: "az ad app create returned no appId"}, nil
	}
	return Action{
		Kind: ActionDone,
		Output: map[string]string{
			"app_id":       created.AppID,
			"display_name": created.DisplayName,
		},
	}, nil
}

// --- grant Graph permissions ------------------------------------------------

type msPermissionsStep struct{}

func (msPermissionsStep) ID() string { return "permissions" }
func (msPermissionsStep) Run(ctx context.Context, s *State, env Env) (Action, error) {
	appID := appIDFromState(s)
	if appID == "" {
		return Action{Kind: ActionFailed, FailReason: "app_id missing"}, nil
	}
	delegated, err := fetchGraphDelegatedScopes(ctx, env.Shell)
	if err != nil {
		return Action{}, err
	}
	count := 0
	for _, name := range msDelegatedScopes {
		guid, ok := delegated[name]
		if !ok {
			return Action{Kind: ActionFailed, FailReason: fmt.Sprintf("Graph scope %q not found in service principal", name)}, nil
		}
		if _, serr, err := env.Shell.Run(ctx, "az", "ad", "app", "permission", "add",
			"--id", appID,
			"--api", graphAPIResourceID,
			"--api-permissions", guid+"=Scope"); err != nil {
			return Action{}, fmt.Errorf("permission add %s: %w (%s)", name, err, serr)
		}
		count++
	}
	return Action{
		Kind: ActionDone,
		Output: map[string]string{
			"scope_count": fmt.Sprintf("%d", count),
		},
	}, nil
}

// --- admin-consent reminder -------------------------------------------------

type msAdminConsentNotifyStep struct{}

func (msAdminConsentNotifyStep) ID() string { return "admin_consent_notice" }
func (msAdminConsentNotifyStep) Run(ctx context.Context, s *State, env Env) (Action, error) {
	if env.Inputs["include_recording"] != "true" && env.Inputs["include_recording"] != "yes" {
		return Action{
			Kind: ActionDone,
			Output: map[string]string{"include_recording": "false"},
		}, nil
	}
	appID := appIDFromState(s)
	url := fmt.Sprintf("https://entra.microsoft.com/#view/Microsoft_AAD_RegisteredApps/ApplicationMenuBlade/~/CallAnAPI/appId/%s", appID)
	msg := fmt.Sprintf(
		"%s requires tenant admin consent. As tenant admin run:\n  az ad app permission admin-consent --id %s\nOr grant via the URL.",
		msOptInScope, appID)
	return Action{
		Kind:    ActionAwaitHuman,
		URL:     url,
		Message: msg,
	}, nil
}

func fetchGraphDelegatedScopes(ctx context.Context, sh Shell) (map[string]string, error) {
	out, serr, err := sh.Run(ctx, "az", "ad", "sp", "show",
		"--id", graphAPIResourceID,
		"--query", "oauth2PermissionScopes[].{name:value, id:id}",
		"-o", "json")
	if err != nil {
		return nil, fmt.Errorf("az ad sp show: %w (%s)", err, serr)
	}
	res := map[string]string{}
	if out != "" {
		var list []graphPermScope
		if err := json.Unmarshal([]byte(out), &list); err != nil {
			return nil, fmt.Errorf("parse oauth2PermissionScopes: %w", err)
		}
		for _, sc := range list {
			if sc.Name != "" && sc.ID != "" {
				res[sc.Name] = sc.ID
			}
		}
	}
	return res, nil
}

func generateAppDisplayName(label string) string {
	u, _ := user.Current()
	whoami := "user"
	if u != nil && u.Username != "" {
		whoami = sanitizeID(u.Username)
	}
	host, _ := os.Hostname()
	hostPart := "host"
	if host != "" {
		hostPart = sanitizeID(host)
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	suffix := hex.EncodeToString(b[:])
	return fmt.Sprintf("apl-%s-%s-%s-%s", whoami, hostPart, label, suffix)
}
