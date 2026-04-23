package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/user"
	"regexp"
	"sort"
	"strings"

	"github.com/muthuishere/all-purpose-login/internal/config"
	"github.com/muthuishere/all-purpose-login/internal/oauth"
)

// googleClientIDRe validates Desktop-app Google Client IDs.
var googleClientIDRe = regexp.MustCompile(`^[0-9]+-[a-z0-9]{32}\.apps\.googleusercontent\.com$`)

type gcloudAuthEntry struct {
	Account string `json:"account"`
	Status  string `json:"status"`
}

type gcloudProject struct {
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
}

func runGoogle(
	ctx context.Context,
	current config.ProviderConfig,
	shell Shell,
	prompter Prompter,
	validator Validator,
	stdout, stderr io.Writer,
) (config.ProviderConfig, error) {
	fmt.Fprintln(stdout, "→ Google setup")

	if !shell.Available("gcloud") {
		return config.ProviderConfig{}, fmt.Errorf(
			"%w: Google setup needs the gcloud CLI\n    Install: https://cloud.google.com/sdk/docs/install\n    Or:      brew install --cask google-cloud-sdk",
			ErrMissingCLI)
	}

	// List gcloud credentialed accounts and let the user pick — or sign in a
	// new one. An empty list is allowed; the picker collapses to sign-in.
	out, _, err := shell.Run(ctx, "gcloud", "auth", "list", "--format=json")
	if err != nil {
		return config.ProviderConfig{}, fmt.Errorf(
			"%w: gcloud auth list failed\n    Run: gcloud auth login",
			ErrNotLoggedIn)
	}
	var accounts []gcloudAuthEntry
	_ = json.Unmarshal([]byte(out), &accounts)
	activeAccount := ""
	for _, a := range accounts {
		if strings.EqualFold(a.Status, "ACTIVE") {
			activeAccount = a.Account
			break
		}
	}

	// Build account picker: existing accounts (active first), then "Sign in".
	sort.SliceStable(accounts, func(i, j int) bool {
		ai := strings.EqualFold(accounts[i].Status, "ACTIVE")
		aj := strings.EqualFold(accounts[j].Status, "ACTIVE")
		if ai != aj {
			return ai
		}
		return accounts[i].Account < accounts[j].Account
	})
	acctOptions := make([]string, 0, len(accounts)+1)
	for _, a := range accounts {
		label := a.Account
		if strings.EqualFold(a.Status, "ACTIVE") {
			label += "  [active]"
		}
		acctOptions = append(acctOptions, label)
	}
	acctOptions = append(acctOptions, "Sign in another account")

	fmt.Fprintln(stdout, "\nDetected gcloud accounts:")
	choice := prompter.Pick("Pick account", acctOptions)
	if choice < 0 || choice >= len(acctOptions) {
		return config.ProviderConfig{}, fmt.Errorf("%w: invalid account choice", ErrProviderFailure)
	}

	var chosen string
	if choice == len(acctOptions)-1 {
		// Sign in a new account interactively.
		email := strings.TrimSpace(prompter.Input("Email to sign in: "))
		if email == "" {
			return config.ProviderConfig{}, fmt.Errorf("%w: empty email for sign-in", ErrProviderFailure)
		}
		fmt.Fprintf(stdout, "→ running `gcloud auth login %s` (browser opens)\n", email)
		if err := shell.RunInteractive(ctx, "gcloud", "auth", "login", email); err != nil {
			return config.ProviderConfig{}, fmt.Errorf("%w: gcloud auth login: %v", ErrNotLoggedIn, err)
		}
		if _, serr, err := shell.Run(ctx, "gcloud", "config", "set", "account", email); err != nil {
			return config.ProviderConfig{}, fmt.Errorf("%w: gcloud config set account: %v\n%s", ErrProviderFailure, err, serr)
		}
		fmt.Fprintf(stdout, "✓ signed in as %s\n→ set as active gcloud account\n", email)
		chosen = email
	} else {
		chosen = accounts[choice].Account
		if !strings.EqualFold(accounts[choice].Status, "ACTIVE") {
			if _, serr, err := shell.Run(ctx, "gcloud", "config", "set", "account", chosen); err != nil {
				return config.ProviderConfig{}, fmt.Errorf("%w: gcloud config set account: %v\n%s", ErrProviderFailure, err, serr)
			}
		}
	}
	activeAccount = chosen

	// Fetch the project currently set in gcloud config for context.
	curProjectOut, _, _ := shell.Run(ctx, "gcloud", "config", "get-value", "project")
	curProject := strings.TrimSpace(curProjectOut)
	if curProject == "" || curProject == "(unset)" {
		curProject = "(none set)"
	}
	fmt.Fprintf(stdout, "\n  Google signed-in account: %s\n  Active project:           %s\n\n",
		activeAccount, curProject)

	// List projects.
	out, _, err = shell.Run(ctx, "gcloud", "projects", "list", "--format=json")
	if err != nil {
		return config.ProviderConfig{}, fmt.Errorf("%w: gcloud projects list: %v", ErrProviderFailure, err)
	}
	var projects []gcloudProject
	_ = json.Unmarshal([]byte(out), &projects)

	// Sort: active first, then apl-*, then alphabetic.
	sort.SliceStable(projects, func(i, j int) bool {
		ai := projects[i].ProjectID == curProject
		aj := projects[j].ProjectID == curProject
		if ai != aj {
			return ai
		}
		bi := strings.HasPrefix(projects[i].ProjectID, "apl-")
		bj := strings.HasPrefix(projects[j].ProjectID, "apl-")
		if bi != bj {
			return bi
		}
		return projects[i].ProjectID < projects[j].ProjectID
	})

	// Build a picker: all reachable projects, plus a "Create new apl-* project"
	// entry at the end. Active project gets a [current] suffix. The user may
	// reuse any existing project — setup will enable the required APIs on it
	// and wire the consent-screen walkthrough to it.
	options := make([]string, 0, len(projects)+1)
	choices := make([]string, 0, len(projects)+1)
	for _, p := range projects {
		label := p.ProjectID
		if p.Name != "" && p.Name != p.ProjectID {
			label = fmt.Sprintf("%s (%s)", p.ProjectID, p.Name)
		}
		if p.ProjectID == curProject {
			label += "  [current]"
		}
		if strings.HasPrefix(p.ProjectID, "apl-") {
			label += "  [apl]"
		}
		options = append(options, label)
		choices = append(choices, p.ProjectID)
	}
	options = append(options, "Create a new apl-* project")
	choices = append(choices, "")

	var projectID string
	if len(options) == 1 {
		// No existing projects at all — only the "Create new" option.
		if !prompter.Confirm("No GCP projects visible. Create a new apl-* project?") {
			return config.ProviderConfig{}, fmt.Errorf("%w: user declined project creation", ErrProviderFailure)
		}
	} else {
		choice := prompter.Pick("Which GCP project?", options)
		if choice < 0 || choice >= len(choices) {
			return config.ProviderConfig{}, fmt.Errorf("%w: invalid project choice", ErrProviderFailure)
		}
		projectID = choices[choice]
		if projectID != "" {
			fmt.Fprintf(stdout, "→ using %s\n", projectID)
		}
	}

	if projectID == "" {
		projectID = generateProjectID()
		fmt.Fprintf(stdout, "→ creating project %s\n", projectID)
		if _, serr, err := shell.Run(ctx, "gcloud", "projects", "create", projectID,
			"--name", "All Purpose Login"); err != nil {
			return config.ProviderConfig{}, fmt.Errorf("%w: gcloud projects create: %v\n%s", ErrProviderFailure, err, serr)
		}
	}

	// Enable APIs.
	fmt.Fprintln(stdout, "→ enabling Gmail, Calendar, People, Drive APIs")
	if _, serr, err := shell.Run(ctx, "gcloud", "services", "enable",
		"gmail.googleapis.com",
		"calendar-json.googleapis.com",
		"people.googleapis.com",
		"drive.googleapis.com",
		"--project="+projectID); err != nil {
		return config.ProviderConfig{}, fmt.Errorf("%w: gcloud services enable: %v\n%s", ErrProviderFailure, err, serr)
	}

	// Try to headlessly create the project's OAuth consent brand via
	// `gcloud alpha iap oauth-brands create`. When this works, the
	// consent-screen UI step can be skipped entirely. It fails silently on:
	// brand already exists, caller lacks IAP admin, alpha component missing.
	brandAuto := false
	if _, _, err := shell.Run(ctx, "gcloud", "alpha", "iap", "oauth-brands", "create",
		"--application_title", "apl (local)",
		"--support_email", activeAccount,
		"--project="+projectID); err == nil {
		brandAuto = true
		fmt.Fprintln(stdout, "✓ OAuth consent brand created via gcloud alpha iap oauth-brands")
	}

	// Google's "Auth Platform" UI URLs (new, replaces the old
	// /apis/credentials/consent + /apis/credentials paths).
	brandURL := fmt.Sprintf("https://console.cloud.google.com/auth/overview?project=%s", projectID)
	dataAccessURL := fmt.Sprintf("https://console.cloud.google.com/auth/scopes?project=%s", projectID)
	audienceURL := fmt.Sprintf("https://console.cloud.google.com/auth/audience?project=%s", projectID)
	clientsURL := fmt.Sprintf("https://console.cloud.google.com/auth/clients/create?project=%s", projectID)

	// Step 1: configure the OAuth brand (App Info / Audience / Contact /
	// Finish wizard). Only if we couldn't auto-create via gcloud alpha.
	if !brandAuto {
		fmt.Fprintf(stdout, `
─────────────────────────────────────────────────────────────────
Step 1 of 3 — Configure OAuth (Auth Platform wizard)
─────────────────────────────────────────────────────────────────
Google will walk you through 4 sub-screens. Fill in:

  App Information
    App name:            apl (local)
    User support email:  %s

  Audience
    Select: External
    (keep as Testing — adds you as a test user; supports up to 100
     personal Gmail users before verification is required)

  Contact Information
    Email:               %s

  Finish → click CREATE

`, activeAccount, activeAccount)

		if prompter.Confirm("Open the Auth Platform wizard in your browser now?") {
			if err := oauth.Open(brandURL); err != nil {
				fmt.Fprintf(stderr, "could not open browser: %v\n  Open this URL manually: %s\n", err, brandURL)
			}
		} else {
			fmt.Fprintf(stdout, "Open manually: %s\n", brandURL)
		}
		if err := prompter.Wait("Press ENTER when the wizard finishes..."); err != nil {
			return config.ProviderConfig{}, err
		}
	}

	// Step 2: add scopes (Data Access) + add the user as a Test User.
	step2Num := 2
	if brandAuto {
		step2Num = 1
	}
	fmt.Fprintf(stdout, `
─────────────────────────────────────────────────────────────────
Step %d — Add scopes and your test user
─────────────────────────────────────────────────────────────────
Open "Data Access" (left sidebar) → ADD OR REMOVE SCOPES.

Scroll to "Manually add scopes" and paste this block verbatim
(one scope per line), then click "Add to table":

  openid
  https://www.googleapis.com/auth/userinfo.email
  https://www.googleapis.com/auth/userinfo.profile
  https://www.googleapis.com/auth/gmail.modify
  https://www.googleapis.com/auth/calendar
  https://www.googleapis.com/auth/contacts.readonly
  https://www.googleapis.com/auth/drive.readonly

Click UPDATE → SAVE.

Then open "Audience" (left sidebar) → Test users → + ADD USERS:
  %s
Click SAVE.

`, step2Num, activeAccount)

	if prompter.Confirm("Open Data Access page in your browser now?") {
		if err := oauth.Open(dataAccessURL); err != nil {
			fmt.Fprintf(stderr, "could not open browser: %v\n  Open this URL manually: %s\n", err, dataAccessURL)
		}
		fmt.Fprintf(stdout, "After saving scopes, open: %s\n", audienceURL)
	} else {
		fmt.Fprintf(stdout, "Open manually:\n  Data Access: %s\n  Audience:    %s\n", dataAccessURL, audienceURL)
	}
	if err := prompter.Wait("Press ENTER when scopes and test user are saved..."); err != nil {
		return config.ProviderConfig{}, err
	}

	// Step 3: create the OAuth 2.0 Desktop Client ID.
	step3Num := step2Num + 1
	fmt.Fprintf(stdout, `
─────────────────────────────────────────────────────────────────
Step %d — Create OAuth 2.0 Client ID
─────────────────────────────────────────────────────────────────
On the Clients page:
  + CREATE CLIENT
  Application type:  Desktop app
  Name:              apl-desktop
  Click CREATE

A dialog shows your Client ID and Client secret. Copy the Client ID.

`, step3Num)

	if prompter.Confirm("Open the Clients page in your browser now?") {
		if err := oauth.Open(clientsURL); err != nil {
			fmt.Fprintf(stderr, "could not open browser: %v\n  Open this URL manually: %s\n", err, clientsURL)
		}
	} else {
		fmt.Fprintf(stdout, "Open manually: %s\n", clientsURL)
	}
	if err := prompter.Wait("Press ENTER when you've clicked CREATE and have the Client ID..."); err != nil {
		return config.ProviderConfig{}, err
	}

	// Client-ID loop. Accepts: raw client_id, pasted JSON blob, or path to a
	// downloaded client-secret JSON file (drag-drop from Finder works).
	var clientID string
	for attempts := 0; attempts < 3; attempts++ {
		raw := prompter.Input("Paste Client ID, JSON, or path to downloaded JSON file: ")
		raw = strings.TrimSpace(raw)
		// Strip shell-style surrounding quotes if someone drag-dropped a path
		// with spaces (macOS Finder drops paths as 'Volumes/..' or "Vol..").
		raw = strings.Trim(raw, "'\"")
		// Expand leading ~.
		if strings.HasPrefix(raw, "~/") {
			if u, uerr := user.Current(); uerr == nil && u.HomeDir != "" {
				raw = u.HomeDir + raw[1:]
			}
		}
		// If the input points at an existing file, read its contents.
		if !strings.HasPrefix(raw, "{") && !strings.Contains(raw, "apps.googleusercontent.com") {
			if body, rerr := os.ReadFile(raw); rerr == nil {
				raw = strings.TrimSpace(string(body))
			}
		}
		// If we now have JSON, extract client_id.
		if strings.HasPrefix(raw, "{") {
			var m map[string]any
			if err := json.Unmarshal([]byte(raw), &m); err == nil {
				if installed, ok := m["installed"].(map[string]any); ok {
					if cid, ok := installed["client_id"].(string); ok {
						raw = cid
					}
				} else if cid, ok := m["client_id"].(string); ok {
					raw = cid
				}
			}
		}
		if !googleClientIDRe.MatchString(raw) {
			fmt.Fprintf(stderr, "✗ client ID format invalid (expected <digits>-<hex>.apps.googleusercontent.com)\n")
			continue
		}
		// Validate via OAuth round-trip.
		if err := validator.Validate(ctx, raw); err != nil {
			fmt.Fprintf(stderr, "✗ OAuth round-trip failed: %v\n", err)
			if !prompter.Confirm("Try again?") {
				return config.ProviderConfig{}, fmt.Errorf("%w: client ID validation failed", ErrProviderFailure)
			}
			continue
		}
		clientID = raw
		break
	}
	if clientID == "" {
		return config.ProviderConfig{}, fmt.Errorf("%w: failed to obtain valid client ID after 3 attempts", ErrProviderFailure)
	}

	fmt.Fprintf(stdout, "✓ Google configured\n    project: %s\n    client: %s\n", projectID, clientID)
	return config.ProviderConfig{
		ClientID:  clientID,
		ProjectID: projectID,
	}, nil
}

func generateProjectID() string {
	u, _ := user.Current()
	whoami := "user"
	if u != nil && u.Username != "" {
		whoami = strings.ToLower(sanitizeLabel(u.Username))
	}
	var b [3]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("apl-%s-%s", whoami, hex.EncodeToString(b[:])[:5])
}
