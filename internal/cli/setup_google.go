package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os/user"
	"regexp"
	"sort"
	"strings"

	"github.com/muthuishere/all-purpose-login/internal/config"
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

	// Preflight: logged in? Use auth list --format=json.
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
	if activeAccount == "" {
		return config.ProviderConfig{}, fmt.Errorf(
			"%w: no active gcloud account\n    Run: gcloud auth login",
			ErrNotLoggedIn)
	}
	// Fetch the project currently set in gcloud config for context.
	curProjectOut, _, _ := shell.Run(ctx, "gcloud", "config", "get-value", "project")
	curProject := strings.TrimSpace(curProjectOut)
	if curProject == "" || curProject == "(unset)" {
		curProject = "(none set)"
	}
	fmt.Fprintf(stdout, "\n  Google signed-in account: %s\n  Active project:           %s\n\n",
		activeAccount, curProject)
	if !prompter.Confirm("Continue as this account?") {
		return config.ProviderConfig{}, fmt.Errorf("%w: cancelled by user (run `gcloud auth login` or `gcloud config set account <email>` to switch)", ErrNotLoggedIn)
	}

	// List projects.
	out, _, err = shell.Run(ctx, "gcloud", "projects", "list", "--format=json")
	if err != nil {
		return config.ProviderConfig{}, fmt.Errorf("%w: gcloud projects list: %v", ErrProviderFailure, err)
	}
	var projects []gcloudProject
	_ = json.Unmarshal([]byte(out), &projects)

	// Sort: apl-* first, then alphabetic.
	sort.SliceStable(projects, func(i, j int) bool {
		ai := strings.HasPrefix(projects[i].ProjectID, "apl-")
		aj := strings.HasPrefix(projects[j].ProjectID, "apl-")
		if ai != aj {
			return ai
		}
		return projects[i].ProjectID < projects[j].ProjectID
	})

	var aplProjects []gcloudProject
	for _, p := range projects {
		if strings.HasPrefix(p.ProjectID, "apl-") {
			aplProjects = append(aplProjects, p)
		}
	}

	var projectID string
	if len(aplProjects) > 0 {
		// Always reuse an existing apl-* project — avoids drifting project
		// sprawl on re-runs. To create a fresh one, delete the old project
		// from GCP first.
		projectID = aplProjects[0].ProjectID
		fmt.Fprintf(stdout, "→ reusing %s\n", projectID)
	} else {
		if !prompter.Confirm("No apl-* projects found. Create a new one?") {
			return config.ProviderConfig{}, fmt.Errorf("%w: user declined project creation", ErrProviderFailure)
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
	fmt.Fprintln(stdout, "→ enabling Gmail, Calendar, People APIs")
	if _, serr, err := shell.Run(ctx, "gcloud", "services", "enable",
		"gmail.googleapis.com",
		"calendar-json.googleapis.com",
		"people.googleapis.com",
		"--project="+projectID); err != nil {
		return config.ProviderConfig{}, fmt.Errorf("%w: gcloud services enable: %v\n%s", ErrProviderFailure, err, serr)
	}

	// Console walkthrough.
	printGoogleWalkthrough(stdout, projectID)
	if err := prompter.Wait("Press ENTER when the consent screen is saved..."); err != nil {
		return config.ProviderConfig{}, err
	}

	// Client-ID loop.
	var clientID string
	for attempts := 0; attempts < 3; attempts++ {
		raw := prompter.Input("Paste your OAuth Client ID: ")
		raw = strings.TrimSpace(raw)
		// If user pasted JSON, extract client_id.
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

func printGoogleWalkthrough(w io.Writer, projectID string) {
	fmt.Fprintf(w, `─────────────────────────────────────────────────────────────────
Step 1 of 3 — Configure the OAuth consent screen
─────────────────────────────────────────────────────────────────
Open this URL in your browser:

  https://console.cloud.google.com/apis/credentials/consent?project=%s

Set the following:
  User Type:         External
  App name:          apl (local)
  User support email: <your email>
  Developer contact:  <your email>
  Test users:        <your email>

On the Scopes page, click "ADD OR REMOVE SCOPES" and add:
  openid
  https://www.googleapis.com/auth/userinfo.email
  https://www.googleapis.com/auth/userinfo.profile
  https://www.googleapis.com/auth/gmail.modify
  https://www.googleapis.com/auth/calendar
  https://www.googleapis.com/auth/contacts.readonly

Click SAVE AND CONTINUE through each page, then PUBLISH or leave in Testing.

─────────────────────────────────────────────────────────────────
Step 2 of 3 — Create OAuth 2.0 Client ID
─────────────────────────────────────────────────────────────────
Open:

  https://console.cloud.google.com/apis/credentials?project=%s

Click: + CREATE CREDENTIALS → OAuth client ID
  Application type:  Desktop app
  Name:              apl-desktop

Click CREATE. A dialog shows your Client ID and Client secret.

─────────────────────────────────────────────────────────────────
Step 3 of 3 — Paste the Client ID below
─────────────────────────────────────────────────────────────────
`, projectID, projectID)
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
