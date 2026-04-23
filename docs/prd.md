# All Purpose Login — Product Requirements Document

**Status:** Draft v1
**Owner:** Muthukumaran Navaneethakrishnan (Deemwar)
**Last Updated:** 2026-04-23
**Codename / Binary:** `apl` (long name: `all-purpose-login`)

---

## 1. Why We Are Building This

### The problem
Developers who want to script against Google Workspace and Microsoft 365 APIs hit the same wall every time:

1. **Every provider has its own CLI**, and each CLI only solves one service:
   - `mgc` (retiring Aug 2026) → Microsoft Graph only
   - `m365` → Microsoft 365 admin only
   - `gcalcli` → Google Calendar only
   - `himalaya` → IMAP/Gmail only
   - `oauth2l` → Google only, needs BYO client
   - `az` → Azure/Graph, but primarily Azure
2. **None of them is a clean token broker.** You either live inside that CLI's commands, or you fight with SDKs to get a raw access token out. Most devs want to just `curl` the REST API.
3. **Multi-account is painful.** Juggling personal / work / client accounts means juggling config directories, env vars, and browser profiles.
4. **Google's Gmail scope is "restricted"**, so most community CLIs can't legally ship a built-in OAuth client — each user has to create their own Google Cloud project. This is a cliff for casual users.
5. **Microsoft `mgc` is being retired** (deprecation started Sep 2025, full retirement Aug 2026). The recommended replacement (Graph PowerShell SDK) is awkward on macOS/Linux.

### The gap
There is **no unified, multi-provider, multi-account OAuth token CLI**. `rclone` solved this for cloud storage. Nothing equivalent exists for productivity APIs (mail / calendar / chat). We verified this in April 2026 — the closest analogues (OAuth2 Proxy, Keycloak, WorkOS) are server-side products, not CLI brokers.

### What success looks like
A user runs:

```bash
apl login google --as work
apl login ms --as work --tenant contoso.onmicrosoft.com
TOKEN=$(apl token ms --as work --scope Mail.Read)
curl -H "Authorization: Bearer $TOKEN" https://graph.microsoft.com/v1.0/me/messages
```

That's it. No SDK, no config file editing, no per-service CLI to learn. Tokens live locally. Works with any HTTP client in any language.

---

## 2. Principles

1. **Local-first.** Tokens never leave the user's machine. No telemetry. No phone-home. No analytics.
2. **Provider-agnostic.** Providers are plugins. Adding Slack/GitHub/Notion later = one file.
3. **Token broker, not a wrapper.** We do not wrap Google/Microsoft SDKs. We hand out access tokens; the user calls the API.
4. **Multi-account by default.** Every account is labelled. Every command takes `--as <label>`.
5. **Trustable.** Open source. Reproducible builds. Auditable OAuth flows (PKCE, exact scopes only on user request).
6. **Boring and fast.** Single static Go binary. No runtime dependencies. `brew install apl` and go.

---

## 3. Scope

### In scope (v1)

**Providers:**
- Google (Gmail, Calendar, basic profile, userinfo)
- Microsoft (Graph — Mail, Calendar, Teams, basic profile; multi-tenant)

**Commands:**
- `apl login <provider> --as <label> [--tenant <id>] [--scope <scope>...]`
- `apl token <provider> --as <label> --scope <scope>`
- `apl accounts` (list all stored accounts across providers)
- `apl logout <provider> --as <label>`
- `apl scopes <provider>` (list known scope aliases for that provider)
- `apl version`

**Platforms:**
- macOS (primary — Intel + Apple Silicon)
- Linux (amd64 + arm64)
- Windows (stretch; same code, needs credential-manager backend tested)

### Out of scope (v1)

- **File storage** (Drive, S3, Dropbox) — `rclone` already owns this space
- **Admin operations** (user management, tenant ops) — `m365` and `gam7` already do this well
- **Service-specific wrappers** (e.g. `apl gmail send`) — that defeats the broker model
- **MCP server mode** — revisit post-v1 if demand emerges
- **Server/hosted deployment** — we are a local tool, not a SaaS

### Future / v2+

- Additional providers: Slack, GitHub, Notion, Linear, Atlassian
- MCP server that exposes tokens to AI agents
- Optional thin-wrapper commands for the most common 5 operations (`apl gmail list`, `apl ms mail send`) behind a feature flag
- Token introspection (`apl inspect <provider> --as <label>` → show expiry, scopes)

---

## 4. Users & Use Cases

### Primary user
Backend / platform engineers on macOS/Linux who:
- Write bash/python/go scripts that touch Gmail, Outlook, Calendar, or Teams
- Prefer `curl + REST` over SDKs for glue code
- Have 2–4 accounts across work/personal/client contexts
- Care about keeping credentials off servers they don't control

### Key scenarios

| # | Scenario | Command shape |
|---|---|---|
| 1 | "Send me all today's calendar events across work + personal" | `apl token google --as work --scope calendar.readonly` + curl, repeat for personal |
| 2 | "Forward last week's support emails to Notion" | `apl token google --as support --scope gmail.readonly` + curl + piping |
| 3 | "Post a Teams message from CI" | `apl token ms --as bot --scope ChatMessage.Send` (note: CI use implies refresh token rotation strategy — see Risks §11) |
| 4 | "I switched laptops, give me my tokens" | `apl login` again per account (tokens are local-only by design) |

---

## 5. UX Design

### 5.1 Command surface

```
apl login <provider> --as <label>
    [--tenant <id>]              # microsoft only; default: common
    [--scope <scope> ...]        # optional pre-consent; default = minimal openid/profile
    [--force]                    # re-consent even if token exists

apl token <provider> --as <label> --scope <scope>
    # prints ONLY the access token to stdout. Nothing else. Pipeable.
    # Exits 0 on success, non-zero + stderr message on failure.
    # Auto-refreshes if expired. Re-consents if scope not in refresh token.

apl accounts [--json]
    # human-readable table by default
    # --json for scripting

apl logout <provider> --as <label>
    # revokes refresh token at provider + wipes local storage

apl scopes <provider>
    # prints known scope aliases mapped to full scope URIs

apl version
```

### 5.2 Example session

```bash
$ apl login google --as work
→ Opening browser for Google sign-in...
→ Listening on http://localhost:54221/callback
✓ Signed in as muthu@deemwar.com (label: work)

$ apl login google --as personal
→ Opening browser for Google sign-in...
✓ Signed in as muthuishere@gmail.com (label: personal)

$ apl accounts
PROVIDER   LABEL      EMAIL                        STORED
google     work       muthu@deemwar.com            keychain
google     personal   muthuishere@gmail.com        keychain
microsoft  volentis   muthu@volentis.ai            keychain

$ apl token google --as work --scope gmail.readonly
ya29.a0AfH6SMB...
```

### 5.3 Scope aliases

Users shouldn't memorise full scope URIs. Each provider ships an alias map:

```
google:
  gmail.readonly      → https://www.googleapis.com/auth/gmail.readonly
  gmail.send          → https://www.googleapis.com/auth/gmail.send
  gmail.modify        → https://www.googleapis.com/auth/gmail.modify
  calendar            → https://www.googleapis.com/auth/calendar
  calendar.readonly   → https://www.googleapis.com/auth/calendar.readonly
  profile             → profile email openid

microsoft:
  Mail.Read
  Mail.Send
  Calendars.Read
  Calendars.ReadWrite
  Chat.Read
  ChatMessage.Send
  User.Read
  offline_access      (auto-added to get refresh token)
```

Users pass aliases; the CLI expands them before sending to provider.

### 5.4 Errors

Plain English, actionable:
- `no account "work" for provider "google". Run: apl login google --as work`
- `scope "gmail.readonly" not granted for this account. Run: apl login google --as work --scope gmail.readonly --force`
- `token refresh failed: refresh_token_expired. Run: apl login google --as work`

---

## 6. Architecture

### 6.1 Module layout

```
all-purpose-login/
├── cmd/apl/
│   └── main.go                    # thin: wire cobra commands
├── internal/
│   ├── cli/
│   │   ├── login.go
│   │   ├── token.go
│   │   ├── accounts.go
│   │   ├── logout.go
│   │   └── scopes.go
│   ├── provider/
│   │   ├── provider.go            # interface
│   │   ├── registry.go            # map[string]Provider
│   │   ├── google.go              # embedded client_id, scope aliases, PKCE
│   │   └── microsoft.go           # embedded client_id, multi-tenant, PKCE
│   ├── oauth/
│   │   ├── pkce.go                # code_verifier / challenge
│   │   ├── loopback.go            # random-port http server + state check
│   │   ├── browser.go             # open URL cross-platform
│   │   └── refresh.go             # shared refresh-token exchange
│   ├── store/
│   │   ├── store.go               # interface + factory (keychain → file fallback)
│   │   ├── record.go              # TokenRecord struct (json-serialisable)
│   │   ├── keyring.go             # github.com/zalando/go-keyring wrapper
│   │   └── file.go                # age-encrypted file fallback
│   └── version/
│       └── version.go             # build-time ldflags
├── configs/
│   ├── google_scopes.yaml         # alias → full scope URI
│   └── microsoft_scopes.yaml
├── docs/
│   └── prd.md                     # this file
├── Taskfile.yml
├── go.mod
└── README.md
```

### 6.2 Provider interface

```go
type Provider interface {
    Name() string                                              // "google" / "microsoft"
    Login(ctx context.Context, label string, opts LoginOpts) (*TokenRecord, error)
    Token(ctx context.Context, rec *TokenRecord, scope string) (access string, refreshed *TokenRecord, err error)
    Logout(ctx context.Context, rec *TokenRecord) error        // revoke + return
    ExpandScopes(aliases []string) ([]string, error)           // alias → full URI
}

type LoginOpts struct {
    Tenant string    // microsoft only
    Scopes []string  // expanded, not aliases
    Force  bool
}

type TokenRecord struct {
    Provider     string    `json:"provider"`
    Label        string    `json:"label"`
    Subject      string    `json:"sub"`            // email / upn
    Tenant       string    `json:"tenant,omitempty"`
    RefreshToken string    `json:"refresh_token"`
    AccessToken  string    `json:"access_token"`   // cached, may be stale
    ExpiresAt    time.Time `json:"expires_at"`
    Scopes       []string  `json:"scopes"`
    IssuedAt     time.Time `json:"issued_at"`
}
```

### 6.3 OAuth flow (both providers)

1. Generate PKCE `code_verifier` (43-128 chars) and `code_challenge` (S256).
2. Generate CSRF `state` (32 bytes hex).
3. Bind loopback HTTP server on `127.0.0.1:0` (random port). Handler accepts `/callback`, validates `state`, writes code to a channel.
4. Build authorisation URL with `response_type=code`, `client_id`, `redirect_uri=http://127.0.0.1:<port>/callback`, `scope`, `code_challenge`, `code_challenge_method=S256`, `state`, and provider-specific extras (`access_type=offline&prompt=consent` for Google; `tenant` path segment for Microsoft).
5. Open browser to auth URL. Show URL in terminal too (headless fallback).
6. Wait for callback (timeout: 5 min). Server auto-shuts after one successful request.
7. Exchange code for tokens at provider's token endpoint with `grant_type=authorization_code`, `code`, `code_verifier`, `client_id`, `redirect_uri`.
8. Parse response → `TokenRecord`. Extract `sub`/`upn`/`email` from ID token (or /userinfo).
9. Persist via store.

### 6.4 Token retrieval flow (`apl token`)

1. Load `TokenRecord` by `(provider, label)`.
2. If requested scope is not in `record.Scopes`, return error: re-login required with `--force`.
3. If `access_token` still has >30s validity, print it.
4. Else, POST to token endpoint with `grant_type=refresh_token`, `refresh_token`, `client_id`, `scope` (optional).
5. Update `access_token`, `expires_at` (keep refresh_token unless provider rotated it).
6. Persist updated record. Print access token.

### 6.5 Storage

**Primary:** OS keychain via `github.com/zalando/go-keyring`.
- Service = `all-purpose-login`
- Account = `{provider}:{label}`
- Secret = JSON-encoded `TokenRecord`

**Fallback:** Encrypted file at `~/.config/all-purpose-login/data`.
- File format: one JSON record per line (JSONL), age-encrypted
- Encryption key: 32 bytes random, stored itself in keychain under `all-purpose-login:master-key`
- If keychain unavailable (headless Linux with no secret-service), prompt user for a passphrase once; derive key via Argon2id; cache key in-memory for the life of the process

**Decision point (open):** when both backends are available, keychain wins. If a user explicitly wants file-only (e.g., portable across machines), allow `APL_STORE=file` env override.

### 6.6 Client IDs (Model A — embedded)

Both client IDs are baked into the binary at build time via ldflags:

```
go build -ldflags "
  -X internal/provider.googleClientID=<ID>
  -X internal/provider.microsoftClientID=<ID>
"
```

Rationale:
- For native/desktop apps, the client_id is not a secret (Google and Microsoft both explicitly allow publishing it)
- Users consent to **"All Purpose Login by Deemwar"** in the browser — consistent brand
- Quotas roll up to our apps, so we can monitor load and request bumps if needed
- For forks / self-hosted builds, users can override via build-time ldflags

### 6.7 Cross-platform browser opening

Use `github.com/pkg/browser`. Fall back to printing `xdg-open`/`open`/`start` command if auto-open fails. For genuinely headless SSH sessions, print the URL + loopback redirect and let the user curl the URL to their local browser (device-code flow is a v2 stretch).

---

## 7. Security

### Threat model
| Threat | Mitigation |
|---|---|
| Refresh token leaked from disk | Keychain is OS-protected; file fallback is age-encrypted with key in keychain |
| Malicious local process tries to exfiltrate tokens | keychain ACLs on macOS scope access to our binary; file fallback requires passphrase if keychain absent |
| User tricked into running a fake `apl` binary | Signed release builds; publish SHA256SUMS; reproducible build goal |
| CSRF attack during OAuth callback | `state` parameter; loopback is `127.0.0.1` only, random port; single-use server |
| Authorization code interception on loopback | PKCE (S256); localhost-only listener |
| Consent-screen spoofing | We only use official provider `accounts.google.com` / `login.microsoftonline.com` URLs |
| Over-broad scopes | Scope is passed explicitly by user; no "god" default. Minimal scopes on first login. |

### Privacy commitments
- No telemetry, no crash reports, no analytics. Hardcoded: zero outbound connections except to the provider's OAuth + API endpoints.
- Source open on GitHub. PRs welcome for security audits.
- Privacy policy: `https://deemwar.com/products/all-purpose-login/privacy` (published).

---

## 8. OAuth App Registration

### Google
- Project: `all-purpose-login-prod` (GCP)
- OAuth client type: **Desktop app** (uses loopback + PKCE)
- Authorised redirect URIs: not needed for desktop apps (loopback is implicit)
- Scopes to register on consent screen: `openid email profile` + Gmail read/send/modify + Calendar read/write
- Consent screen: **External**, published (post-v1: security verification for Gmail scope)
- Required: privacy policy URL, terms URL, app logo, authorised domains (`deemwar.com`)

### Microsoft (Entra ID)
- Tenant: `deemwar.onmicrosoft.com` (or personal MSA as publisher)
- App registration type: **Multi-tenant + personal Microsoft accounts**
- Platform: **Mobile and desktop applications** (Public client)
- Redirect URIs: `http://localhost` (native client)
- Required: `offline_access` + whatever Graph scopes users select
- No admin-consent-required scopes in v1 (keeps onboarding zero-friction)

---

## 9. Distribution

### v0 / internal
- Built locally, binary in `~/bin`
- Embedded client IDs = Muthu's own test apps
- Used only by Muthu to validate flows

### v1 public
- GitHub repo: `deemwar/all-purpose-login` (public)
- Releases: goreleaser → tarballs for darwin/linux/windows × amd64/arm64
- Homebrew: `brew tap deemwar/apl` + formula pointing at GH release
- `curl ... | sh` installer as a convenience
- Reproducible build instructions in README

### Later
- Submit to `homebrew-core` if adoption is healthy
- `winget` / `scoop` for Windows
- `apt` / `rpm` packages if demand

---

## 10. Milestones

| M | Goal | Definition of done |
|---|---|---|
| M0 | Scaffold | Repo layout, go.mod, cobra wired, `apl version` works |
| M1 | Google happy path | `login google --as x` → `token google --as x --scope profile` works end-to-end, stored in keychain |
| M2 | Google Gmail + Calendar scopes | Scope aliases, re-consent on new scope, refresh works |
| M3 | Microsoft happy path | Multi-tenant login, Graph `User.Read` token works end-to-end |
| M4 | Microsoft Mail/Calendar/Teams scopes | All target scopes tested with real curl calls |
| M5 | Storage hardening | File fallback (age-encrypted) + passphrase mode for headless Linux |
| M6 | Polish | `accounts` / `logout` / `scopes` commands, clean error messages, `--json` outputs |
| M7 | Release | OAuth apps published + verified (Microsoft), goreleaser, Homebrew tap, docs site live |
| M8 | Verify Google | Google security audit for Gmail scope (~4–6 weeks, external dep) |

---

## 11. Risks & Open Questions

### Risks
1. **Google Gmail verification delay.** Security audit can take 4–6 weeks and may require CASA assessment (~$75–$500). Mitigation: v1 ships with Gmail in "testing" mode (100 user cap); verification is a post-launch track.
2. **Microsoft tenant restrictions.** Some orgs block third-party multi-tenant apps by default. Mitigation: document how an admin can consent per tenant; provide `--tenant <id>` as explicit override.
3. **Trust.** Users are handing consent to "All Purpose Login by Deemwar". Mitigation: open source, minimal scope defaults, reproducible builds, publishable audit trail.
4. **CI / headless usage.** PKCE loopback doesn't work in headless CI. Mitigation v1: document that the tool is for interactive use; if a token is already stored, `apl token` works headlessly. Mitigation v2: device-code flow.
5. **Token leakage.** A malicious process on the user's machine with their uid can read the keychain with user consent. This is true of every credential on their machine. Out of scope to solve.
6. **Provider quota.** If a viral spike happens, our embedded client may hit Google's per-project quota. Mitigation: monitor; request bumps; allow `APL_CLIENT_ID` override for power users.

### Open questions (to decide before / during implementation)
- [ ] Binary name `apl` vs `all-purpose-login` — ship `apl` and install `all-purpose-login` as symlink?
- [ ] Default port range for loopback — random 49152–65535 or pin a small set?
- [ ] Should `apl token` auto-trigger `login` if no token exists, or fail hard with instructions? (leaning: fail hard, keep commands single-purpose)
- [ ] Scope alias format — YAML shipped embedded, or Go map literals? (leaning: embedded YAML via `go:embed` for easy editing)
- [ ] Version pin for `go-keyring` vs writing our own thin wrappers — depends on macOS + linux behaviour during M1
- [ ] `--json` output for `apl token`? (leaning: no, keep it stdout-token-only for pipeability; use `apl inspect` in v2 for structured output)

---

## 12. Success Metrics

Post-v1 (6 months after public release):
- 500+ GitHub stars
- 50+ unique Homebrew installs/week (via Cloudflare analytics on release URLs)
- 10+ external contributors
- 0 reported credential-leak incidents
- Gmail verification complete

Strong signal: unsolicited blog posts / Hacker News threads / "I wrote a script using apl" tweets.

---

## 13. Decisions Log

| Date | Decision | Rationale |
|---|---|---|
| 2026-04-23 | Language: Go | Single static binary, cross-platform, good OAuth libs |
| 2026-04-23 | Model A (embedded client_ids) | Easiest end-user UX; native client_id is not a secret |
| 2026-04-23 | Scope v1 = Google + Microsoft productivity APIs only | `rclone` owns storage; `m365`/`gam` own admin |
| 2026-04-23 | Storage: keychain primary, age-encrypted file fallback | OS-protected by default, portable fallback |
| 2026-04-23 | No telemetry | Local-first principle; user trust |
| 2026-04-23 | `mgc` is retiring → we are a replacement for its "give me a token" use case, not its command surface | Scope discipline |

---

## 14. References

- Microsoft Graph CLI retirement — https://devblogs.microsoft.com/microsoft365dev/microsoft-graph-cli-retirement/
- Google OAuth 2.0 for Desktop Apps — https://developers.google.com/identity/protocols/oauth2/native-app
- Microsoft identity platform native app — https://learn.microsoft.com/en-us/entra/identity-platform/scenario-desktop-overview
- Google API Services User Data Policy — https://developers.google.com/terms/api-services-user-data-policy
- RFC 7636 (PKCE) — https://datatracker.ietf.org/doc/html/rfc7636
- RFC 8252 (OAuth for Native Apps) — https://datatracker.ietf.org/doc/html/rfc8252
- `go-keyring` — https://github.com/zalando/go-keyring
- `age` encryption — https://github.com/FiloSottile/age
