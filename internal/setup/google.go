package setup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"regexp"
	"strings"
)

// googleClientIDRe validates Desktop-app Google Client IDs.
var googleClientIDRe = regexp.MustCompile(`^[0-9]+-[a-z0-9]{32}\.apps\.googleusercontent\.com$`)

// GoogleSteps returns the ordered step list for `apl setup google`.
//
// The flow branches on whether an existing apl project (with OAuth client) is
// already configured for some other label: if so, the user is offered the
// reuse path (just add the new email as a test user; no project creation).
func GoogleSteps(reuseProjectID, reuseClientID, reuseClientSecret string) []Step {
	if reuseProjectID != "" && reuseClientID != "" && reuseClientSecret != "" {
		return []Step{
			&googleReuseConfirmStep{
				ProjectID:    reuseProjectID,
				ClientID:     reuseClientID,
				ClientSecret: reuseClientSecret,
			},
			&googleAddTestUserStep{ProjectID: reuseProjectID},
			&googleSaveReuseStep{
				ProjectID:    reuseProjectID,
				ClientID:     reuseClientID,
				ClientSecret: reuseClientSecret,
			},
		}
	}
	return []Step{
		&googleAccountStep{},
		&googleProjectStep{},
		&googleEnableAPIsStep{},
		&googleConsentScreenStep{},
		&googleScopesAndTestUserStep{},
		&googleClientCreateStep{},
		&googleClientFileStep{},
	}
}

// --- shared helpers ---------------------------------------------------------

type gcloudAuthEntry struct {
	Account string `json:"account"`
	Status  string `json:"status"`
}

type gcloudProject struct {
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
}

func projectIDFromState(s *State) string {
	if st, ok := s.Steps["project"]; ok && st != nil {
		if v, ok := st.Output["project_id"]; ok {
			return v
		}
	}
	return ""
}

func accountFromState(s *State) string {
	if st, ok := s.Steps["account"]; ok && st != nil {
		if v, ok := st.Output["account"]; ok {
			return v
		}
	}
	return ""
}

// --- account step -----------------------------------------------------------

type googleAccountStep struct{}

func (googleAccountStep) ID() string { return "account" }
func (googleAccountStep) Run(ctx context.Context, s *State, env Env) (Action, error) {
	if !env.Shell.Available("gcloud") {
		return Action{
			Kind:       ActionFailed,
			FailReason: "gcloud CLI not found. Install: https://cloud.google.com/sdk/docs/install",
		}, nil
	}
	// Prefer flag input.
	want := env.Inputs["account"]

	out, _, err := env.Shell.Run(ctx, "gcloud", "auth", "list", "--format=json")
	if err != nil {
		return Action{}, fmt.Errorf("gcloud auth list: %w", err)
	}
	var accounts []gcloudAuthEntry
	_ = json.Unmarshal([]byte(out), &accounts)

	if want == "" {
		// LLM-driven mode requires --account.
		acctList := make([]string, 0, len(accounts))
		for _, a := range accounts {
			acctList = append(acctList, a.Account)
		}
		return Action{
			Kind:    ActionAwaitInput,
			Message: fmt.Sprintf("Choose a gcloud account or sign in a new one. Detected: %s. Resume with --account=<email>.", strings.Join(acctList, ", ")),
			Fields:  []string{"account"},
		}, nil
	}

	// Is this account already in gcloud?
	found := false
	active := false
	for _, a := range accounts {
		if strings.EqualFold(a.Account, want) {
			found = true
			active = strings.EqualFold(a.Status, "ACTIVE")
			break
		}
	}
	if !found {
		// Need a browser sign-in. Suspend with the gcloud auth login URL.
		return Action{
			Kind: ActionAwaitHuman,
			URL:  "https://accounts.google.com/o/oauth2/auth (will be opened by gcloud auth login)",
			Message: fmt.Sprintf("Run interactively: `gcloud auth login %s` (signs in via browser). Resume after sign-in.", want),
		}, nil
	}
	if !active {
		if _, serr, err := env.Shell.Run(ctx, "gcloud", "config", "set", "account", want); err != nil {
			return Action{}, fmt.Errorf("gcloud config set account: %w (%s)", err, serr)
		}
	}
	return Action{
		Kind:   ActionDone,
		Output: map[string]string{"account": want},
	}, nil
}

// --- project step -----------------------------------------------------------

type googleProjectStep struct{}

func (googleProjectStep) ID() string { return "project" }
func (googleProjectStep) Run(ctx context.Context, s *State, env Env) (Action, error) {
	want := env.Inputs["project_id"]

	if want == "" {
		// List existing projects so caller can pick or supply --project-id=<new>.
		out, _, err := env.Shell.Run(ctx, "gcloud", "projects", "list", "--format=json")
		if err != nil {
			return Action{}, fmt.Errorf("gcloud projects list: %w", err)
		}
		var projects []gcloudProject
		_ = json.Unmarshal([]byte(out), &projects)
		ids := make([]string, 0, len(projects))
		for _, p := range projects {
			ids = append(ids, p.ProjectID)
		}
		suggested := generateProjectID()
		return Action{
			Kind: ActionAwaitInput,
			Message: fmt.Sprintf(
				"Pick a project_id (existing: %s) or pass a new id (suggested: %s). New ids will be created.",
				strings.Join(ids, ", "), suggested),
			Fields: []string{"project_id"},
		}, nil
	}

	// Does the project exist?
	out, _, _ := env.Shell.Run(ctx, "gcloud", "projects", "list",
		"--filter=projectId="+want, "--format=value(projectId)")
	exists := strings.TrimSpace(out) != ""

	if !exists {
		if _, serr, err := env.Shell.Run(ctx, "gcloud", "projects", "create", want,
			"--name", "All Purpose Login"); err != nil {
			return Action{}, fmt.Errorf("gcloud projects create: %w (%s)", err, serr)
		}
	}
	return Action{
		Kind: ActionDone,
		Output: map[string]string{
			"project_id": want,
			"created":    fmt.Sprintf("%v", !exists),
		},
	}, nil
}

// --- enable APIs ------------------------------------------------------------

type googleEnableAPIsStep struct{}

func (googleEnableAPIsStep) ID() string { return "enable_apis" }
func (googleEnableAPIsStep) Run(ctx context.Context, s *State, env Env) (Action, error) {
	pid := projectIDFromState(s)
	if pid == "" {
		return Action{Kind: ActionFailed, FailReason: "project_id missing from prior step"}, nil
	}
	if _, serr, err := env.Shell.Run(ctx, "gcloud", "services", "enable",
		"gmail.googleapis.com",
		"calendar-json.googleapis.com",
		"people.googleapis.com",
		"drive.googleapis.com",
		"--project="+pid,
	); err != nil {
		return Action{}, fmt.Errorf("gcloud services enable: %w (%s)", err, serr)
	}
	return Action{Kind: ActionDone}, nil
}

// --- consent screen ---------------------------------------------------------

type googleConsentScreenStep struct{}

func (googleConsentScreenStep) ID() string { return "consent_screen" }
func (googleConsentScreenStep) Run(ctx context.Context, s *State, env Env) (Action, error) {
	pid := projectIDFromState(s)
	account := accountFromState(s)
	if pid == "" {
		return Action{Kind: ActionFailed, FailReason: "project_id missing"}, nil
	}

	// Try the headless `gcloud alpha iap oauth-brands create` path.
	if _, _, err := env.Shell.Run(ctx, "gcloud", "alpha", "iap", "oauth-brands", "create",
		"--application_title", "apl ("+s.Label+")",
		"--support_email", account,
		"--project="+pid); err == nil {
		return Action{
			Kind:   ActionDone,
			Output: map[string]string{"brand_auto_created": "true"},
		}, nil
	}

	// Fall through to human walkthrough.
	url := fmt.Sprintf("https://console.cloud.google.com/auth/overview?project=%s", pid)
	msg := fmt.Sprintf(
		"Open the OAuth consent wizard. Set: User Type=External, App name=apl (%s), Support email=%s, Audience=keep Testing. Click CREATE.",
		s.Label, account)
	return Action{
		Kind:    ActionAwaitHuman,
		URL:     url,
		Message: msg,
	}, nil
}

// --- scopes + test user -----------------------------------------------------

type googleScopesAndTestUserStep struct{}

func (googleScopesAndTestUserStep) ID() string { return "scopes_and_test_user" }
func (googleScopesAndTestUserStep) Run(ctx context.Context, s *State, env Env) (Action, error) {
	pid := projectIDFromState(s)
	account := accountFromState(s)
	scopesURL := fmt.Sprintf("https://console.cloud.google.com/auth/scopes?project=%s", pid)
	audienceURL := fmt.Sprintf("https://console.cloud.google.com/auth/audience?project=%s", pid)
	msg := fmt.Sprintf(`At %s click ADD OR REMOVE SCOPES, then in "Manually add scopes" paste:
  openid
  https://www.googleapis.com/auth/userinfo.email
  https://www.googleapis.com/auth/userinfo.profile
  https://www.googleapis.com/auth/gmail.modify
  https://www.googleapis.com/auth/calendar
  https://www.googleapis.com/auth/contacts.readonly
  https://www.googleapis.com/auth/drive.readonly
Click Add to table → UPDATE → SAVE.

Then at %s under Test users click + ADD USERS and add: %s. SAVE.`, scopesURL, audienceURL, account)
	return Action{
		Kind:    ActionAwaitHuman,
		URL:     scopesURL,
		Message: msg,
	}, nil
}

// --- create OAuth client (browser) ------------------------------------------

type googleClientCreateStep struct{}

func (googleClientCreateStep) ID() string { return "client_create" }
func (googleClientCreateStep) Run(ctx context.Context, s *State, env Env) (Action, error) {
	pid := projectIDFromState(s)
	url := fmt.Sprintf("https://console.cloud.google.com/auth/clients/create?project=%s", pid)
	msg := `Click + CREATE CLIENT → Application type: Desktop app → Name: apl-desktop → CREATE. In the dialog click DOWNLOAD JSON. Resume with --client-secret-file=<path-to-downloaded-json>.`
	return Action{
		Kind:    ActionAwaitInput,
		URL:     url,
		Message: msg,
		Fields:  []string{"client_secret_file"},
	}, nil
}

// --- read client secret JSON ------------------------------------------------

type googleClientFileStep struct{}

func (googleClientFileStep) ID() string { return "client_file" }
func (googleClientFileStep) Run(ctx context.Context, s *State, env Env) (Action, error) {
	path := env.Inputs["client_secret_file"]
	if path == "" {
		return Action{
			Kind:    ActionAwaitInput,
			Message: "Pass --client-secret-file=<path> with the JSON downloaded from the OAuth client create dialog.",
			Fields:  []string{"client_secret_file"},
		}, nil
	}
	path = strings.TrimSpace(path)
	path = strings.Trim(path, "'\"")
	if strings.HasPrefix(path, "~/") {
		if u, _ := user.Current(); u != nil && u.HomeDir != "" {
			path = u.HomeDir + path[1:]
		}
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return Action{
			Kind:        ActionFailed,
			FailReason:  fmt.Sprintf("read %s: %v", path, err),
			Recoverable: true,
		}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return Action{
			Kind:        ActionFailed,
			FailReason:  fmt.Sprintf("not valid JSON: %v", err),
			Recoverable: true,
		}, nil
	}
	installed, ok := m["installed"].(map[string]any)
	if !ok {
		return Action{
			Kind:        ActionFailed,
			FailReason:  "JSON missing 'installed' object — must be a Desktop-app client",
			Recoverable: true,
		}, nil
	}
	cid, _ := installed["client_id"].(string)
	csec, _ := installed["client_secret"].(string)
	if !googleClientIDRe.MatchString(cid) {
		return Action{
			Kind:        ActionFailed,
			FailReason:  fmt.Sprintf("client_id format invalid: %q", cid),
			Recoverable: true,
		}, nil
	}
	if csec == "" {
		return Action{
			Kind:        ActionFailed,
			FailReason:  "client_secret missing from JSON",
			Recoverable: true,
		}, nil
	}
	if env.Validator != nil {
		if err := env.Validator.Validate(ctx, cid); err != nil {
			return Action{
				Kind:        ActionFailed,
				FailReason:  fmt.Sprintf("OAuth round-trip failed: %v", err),
				Recoverable: true,
			}, nil
		}
	}
	return Action{
		Kind: ActionDone,
		Output: map[string]string{
			"client_id":     cid,
			"client_secret": csec,
		},
	}, nil
}

// --- reuse-existing-project path -------------------------------------------

type googleReuseConfirmStep struct {
	ProjectID    string
	ClientID     string
	ClientSecret string
}

func (s googleReuseConfirmStep) ID() string { return "reuse_confirm" }
func (s googleReuseConfirmStep) Run(ctx context.Context, st *State, env Env) (Action, error) {
	confirmed := env.Inputs["reuse_confirm"]
	if confirmed != "yes" && confirmed != "true" && confirmed != "y" {
		return Action{
			Kind:    ActionAwaitInput,
			Message: fmt.Sprintf("Reuse existing project %s and OAuth client %s? Pass --reuse=yes (or run without --reuse for fresh project).", s.ProjectID, s.ClientID),
			Fields:  []string{"reuse_confirm"},
		}, nil
	}
	return Action{
		Kind: ActionDone,
		Output: map[string]string{
			"project_id": s.ProjectID,
		},
	}, nil
}

type googleAddTestUserStep struct {
	ProjectID string
}

func (s googleAddTestUserStep) ID() string { return "add_test_user" }
func (s googleAddTestUserStep) Run(ctx context.Context, st *State, env Env) (Action, error) {
	url := fmt.Sprintf("https://console.cloud.google.com/auth/audience?project=%s", s.ProjectID)
	target := env.Inputs["account"]
	if target == "" {
		target = "<email-for-this-label>"
	}
	msg := fmt.Sprintf(
		"Open the URL, scroll to Test users, click + ADD USERS, add: %s. SAVE. (Re-run with --resume after saving.)", target)
	return Action{
		Kind:    ActionAwaitHuman,
		URL:     url,
		Message: msg,
	}, nil
}

type googleSaveReuseStep struct {
	ProjectID    string
	ClientID     string
	ClientSecret string
}

func (s googleSaveReuseStep) ID() string { return "save_reuse" }
func (s googleSaveReuseStep) Run(ctx context.Context, st *State, env Env) (Action, error) {
	return Action{
		Kind: ActionDone,
		Output: map[string]string{
			"project_id":    s.ProjectID,
			"client_id":     s.ClientID,
			"client_secret": s.ClientSecret,
		},
	}, nil
}

// --- helpers ----------------------------------------------------------------

func generateProjectID() string {
	u, _ := user.Current()
	whoami := "user"
	if u != nil && u.Username != "" {
		whoami = strings.ToLower(sanitizeID(u.Username))
	}
	var b [3]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("apl-%s-%s", whoami, hex.EncodeToString(b[:])[:5])
}

func sanitizeID(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' {
			out = append(out, c)
		}
	}
	return string(out)
}
