# apl — all-purpose login

`apl` is an OAuth token broker and HTTP relay for **Google Workspace** and **Microsoft 365** productivity APIs — mail, calendar, Teams, Meet, Drive, OneDrive, SharePoint, contacts. It handles the PKCE + loopback dance once, stores tokens encrypted under the OS keychain, then lets any CLI, script, or AI agent call upstream APIs with a single `apl call <handle> <method> <url>`.

```
apl setup ms          # one-time interactive bootstrap
apl login ms:work     # browser OAuth, tokens land encrypted on disk
apl call ms:work GET https://graph.microsoft.com/v1.0/me
                      # bearer injected; auto-refresh on 401
```

---

## Requirements

### Always required

| Tool | For | Install |
|---|---|---|
| **`apl`** itself | The CLI | `npm install -g @muthuishere/apl` — or download from [Releases](https://github.com/muthuishere/all-purpose-login/releases) |
| Node **20+** | Only if installing via npm | `brew install node` / [nodejs.org](https://nodejs.org/) |

Not needed if you download the prebuilt tarball from GitHub Releases.

### Only if you want Microsoft APIs

| Tool | For | Install |
|---|---|---|
| **`az`** CLI | `apl setup ms` — lets apl create the Azure AD app registration + grant Graph permissions without you clicking through the Azure portal | `brew install azure-cli` |

You need to be **logged in**: `az login` (one time). Setup shows you the active account and asks you to confirm before creating the app registration.

### Only if you want Google APIs

| Tool | For | Install |
|---|---|---|
| **`gcloud`** CLI | `apl setup google` — lets apl create/pick a GCP project and enable Gmail, Calendar, People, Drive APIs | `brew install --cask google-cloud-sdk` |

You need to be **logged in**: `gcloud auth login` (one time). Setup shows you the active account and picks a project from your list (or offers to create one).

**Google requires one unavoidable manual step:** creating the OAuth 2.0 Desktop Client ID. Google deliberately blocks `gcloud` from doing it. apl guides you through the GCP console walkthrough (3 steps, ~2 minutes), then accepts the downloaded JSON file.

### Never required

- **A cloud account on the side you don't use.** If you only want Google APIs, no `az` or Azure tenant needed. If you only want Microsoft, no `gcloud` or GCP project needed.
- **Homebrew, Docker, Python** beyond what your OS ships.
- **Network access except to the provider's OAuth endpoints and the APIs you call.** apl is local-first — no telemetry, no phone-home.

---

## Install

### npm (recommended)

```bash
npm install -g @muthuishere/apl
```

Works on macOS (arm64 / x64), Linux (arm64 / x64), Windows (x64). Uses npm's per-platform optional dependencies — the right prebuilt binary lands, no postinstall scripts required (plays nicely with `npm ci --ignore-scripts`).

### GitHub Release tarball

Prebuilt tarballs per release on the [Releases page](https://github.com/muthuishere/all-purpose-login/releases):

```bash
# Linux x64 example
curl -sL https://github.com/muthuishere/all-purpose-login/releases/latest/download/apl_<version>_Linux_x86_64.tar.gz \
  | tar -xz -C /usr/local/bin apl
```

### From source

```bash
git clone https://github.com/muthuishere/all-purpose-login
cd all-purpose-login
task build   # produces ./bin/apl
```

Requires Go 1.24 + [Task](https://taskfile.dev). Contributors only — end users go via npm.

---

## Quick start

### Microsoft side

```bash
az login                              # one time
apl setup ms --label work             # creates Azure app registration, grants Graph scopes
apl login ms:work                     # browser OAuth, tokens encrypted on disk
apl call ms:work GET https://graph.microsoft.com/v1.0/me/messages?\$top=5
```

### Google side

```bash
gcloud auth login                     # one time
apl setup google --label personal     # pick GCP project, enable APIs, paste client JSON
apl login google:personal             # browser OAuth
apl call google:personal GET https://gmail.googleapis.com/gmail/v1/users/me/messages?q=is:unread
```

### Multiple accounts per provider

Each label gets its own OAuth client config (`config.yaml` keyed under
`google:<label>` / `microsoft:<label>`). The fastest path for a second
account is **reuse**: share the existing OAuth client and just add the
new email as a test user.

```bash
# First account — full setup creates a new GCP project + OAuth client.
apl setup google --label muthuishere
apl login google:muthuishere

# Second account on the same project — reuse the client, add as test user only.
apl setup google --label deemwar --reuse=muthuishere --account=admin@deemwar.com
apl login google:deemwar
```

### LLM-driveable setup (`--json`)

Setup is a resumable state machine. Each browser hand-off emits an
`awaiting_human` or `awaiting_input` NDJSON event with the **exact**
console URL and required fields, then exits. Re-invoke with `--resume`
(plus any newly-supplied flags) to continue.

```bash
apl setup google --label deemwar --reuse=muthuishere --json --reuse-confirm=yes
# {"event":"step_started","step":"reuse_confirm"}
# {"event":"step_done","step":"reuse_confirm",...}
# {"event":"awaiting_human","step":"add_test_user",
#  "url":"https://console.cloud.google.com/auth/audience?project=apl-...",
#  "instructions":"... add: admin@deemwar.com. SAVE.",
#  "resume_command":"apl setup google --label deemwar --resume --json"}

# After the human adds the test user in the browser, resume:
apl setup google --label deemwar --resume --json --account=admin@deemwar.com
# {"event":"completed","label":"deemwar","summary":{"client_id":...}}
```

State lives at `~/.config/apl/setup-state/<provider>-<label>.yaml`.
`--status` shows progress; `--reset-state` wipes it.

---

## Companion: the agent skill

For Claude Code / Codex agents, install the companion skill:

```bash
git clone https://github.com/muthuishere-agent-skills/all-purpose-data-skill
cd all-purpose-data-skill
sh install.sh
```

The skill turns "send an email to x@y.com", "what meetings do I have today", "find the transcript of last week's team meeting" into the right `apl call` invocations automatically. See [muthuishere-agent-skills/all-purpose-data-skill](https://github.com/muthuishere-agent-skills/all-purpose-data-skill).

---

## Development

```bash
task build           # builds ./bin/apl
task test            # go test ./...
task e2e:ms          # manual smoke against a real MS tenant
task e2e:google      # same for Google
task run -- version  # ./bin/apl version
```

Specs live in [`docs/specs/`](docs/specs). Start with [`spec-cli-command-surface.md`](docs/specs/spec-cli-command-surface.md), [`spec-oauth-pkce-loopback.md`](docs/specs/spec-oauth-pkce-loopback.md), and [`spec-recipes.md`](docs/specs/spec-recipes.md) (the catalogue the agent skill is built from).

## Releasing

All releases are cut manually from a local machine. See [`docs/releasing.md`](docs/releasing.md).

```bash
bash scripts/release-local.sh 0.2.0
```

---

## Troubleshooting

**`apl: command not found` after npm install** — Make sure `$(npm prefix -g)/bin` is on your PATH. For macOS with Homebrew's node: `/opt/homebrew/bin` (arm64) or `/usr/local/bin` (x64).

**`apl setup ms` says "az not installed"** — Install Azure CLI: `brew install azure-cli` or follow https://learn.microsoft.com/cli/azure/install-azure-cli. Then `az login`.

**`apl setup google` says "gcloud not installed"** — `brew install --cask google-cloud-sdk` or https://cloud.google.com/sdk/docs/install. Then `gcloud auth login`.

**OAuth browser flow completes but I get "invalid_grant"** — Your stored refresh token expired or was revoked. Run `apl login <handle> --force` to re-consent.

**`403 accessDenied` downloading a Teams meeting recording** — See [`docs/microsoft-graph.md`](docs/microsoft-graph.md#recording-mp4-two-step-workflow-via-onedrive) — SharePoint ACLs gate meeting recordings outside participants-with-explicit-shares. Not an apl bug; Microsoft design boundary.

---

## License

MIT — see [LICENSE](LICENSE).
