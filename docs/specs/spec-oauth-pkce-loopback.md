# Spec: OAuth 2.0 Authorization Code + PKCE with Loopback Redirect

**Status:** Proposed
**Priority:** High
**Depends on:** `spec-setup-bootstrapper.md` (provides per-provider `client_id` in `~/.config/apl/config.yaml`), `spec-token-storage.md` (provides `TokenRecord` and the `Store` interface)

---

## Problem

`apl login <handle>` and `apl token <handle> --scope <scope>` are the two commands that every other command in the tool exists to feed. Between them they implement the full OAuth 2.0 Authorization Code + PKCE flow (RFC 7636) with loopback redirect (RFC 8252), plus refresh-token exchange, scope validation, and re-consent. The PRD sketches this in §6.3 and §6.4 but leaves the protocol-level details — exact form fields, port binding, CSRF handling, Google/Microsoft-specific quirks, headless fallback — unspecified.

This spec pins those details. It is the contract the `internal/oauth` and `internal/provider` packages must satisfy for v1.

Assumption: the v1 flow treats the ID token as trusted because it was received over a TLS-validated connection directly from the provider's token endpoint. We parse `sub`/`email`/`upn` claims but do **not** verify JWT signatures in v1.

---

## Goals

- Deterministic, RFC-compliant Auth Code + PKCE flow for Google and Microsoft
- Loopback HTTP server on `127.0.0.1` with a random port, single-shot, 5-minute deadline
- `apl token` returns only the raw access token (plus trailing newline) on stdout — scriptable
- Auto-refresh when the cached access token is within 30 seconds of expiry
- Hard fail (no auto-login) when a scope is requested that the stored record does not cover
- `--force` re-consent path that bypasses the cached token and runs the full browser flow
- Headless SSH fallback: print the auth URL + redirect URL when the browser cannot be opened
- No secrets in the binary — client IDs come from config, produced by `apl setup`

---

## Non-Goals

- Command surface / flag parsing (see `spec-command-surface.md`)
- `apl setup` bootstrapping of client IDs (see `spec-setup-bootstrapper.md`)
- On-disk encryption / keychain behavior of `TokenRecord` (see `spec-token-storage.md`)
- Device-code flow (deferred to v2)
- JWT signature verification of the ID token (v1 trusts the TLS channel)
- Providers beyond Google and Microsoft
- Concurrent multi-account login in parallel (one flow at a time per provider)

---

## Functional Requirements

### OAUTH-1 — PKCE verifier and challenge (RFC 7636)

- `code_verifier`: 64 bytes from `crypto/rand`, base64url-encoded without padding (yields 86 chars, inside the 43–128 range)
- `code_challenge`: `base64url(sha256(verifier))`, no padding
- `code_challenge_method`: always `S256`. The `plain` method is never used
- Verifier is held in memory only — never written to disk, never logged

### OAUTH-2 — CSRF state

- `state`: 32 bytes from `crypto/rand`, hex-encoded (64 chars)
- On callback, the received state is compared to the generated state using `crypto/subtle.ConstantTimeCompare`
- A mismatch aborts the flow with `state mismatch (possible CSRF)` and does not exchange the code

### OAUTH-3 — Loopback HTTP server

- Bind `127.0.0.1:0` literally — never `localhost` (avoids `/etc/hosts` and IPv6 resolution ambiguity)
- Never bind `0.0.0.0` — the listener must be unreachable from other hosts on the network
- Exactly one route: `GET /callback`
- The listener is the concurrency lock: a second `apl login` for the same provider does not strictly need an external lock file, because the active flow holds a live socket and the user can only service one browser prompt at a time. If a second `login` is launched, it gets its own random port and runs independently — provider-level serialization is only needed for the token-endpoint write at the end
- Single-shot: after one successful callback the server calls `Shutdown(ctx)` and the goroutine returns
- Hard deadline: 5 minutes from `Start()`. On timeout the server shuts down and `Login` returns `timed out waiting for OAuth callback (5m)`
- On success the browser is shown a minimal HTML page (`<h1>Signed in. You can close this tab.</h1>`). The authorization code is **not** echoed into the HTML and is not rendered into any user-visible URL

### OAUTH-4 — Authorization URL construction

Common query parameters (URL-encoded):

```
response_type=code
client_id=<from config>
redirect_uri=http://127.0.0.1:<port>/callback
scope=<space-joined expanded scopes>
state=<hex>
code_challenge=<S256>
code_challenge_method=S256
```

Provider-specific:

- **Google** — authorize endpoint `https://accounts.google.com/o/oauth2/v2/auth`; always append `access_type=offline` and `prompt=consent` (the only reliable way to get a refresh token back every time, including on re-login)
- **Microsoft** — authorize endpoint `https://login.microsoftonline.com/{tenant}/oauth2/v2.0/authorize`; `{tenant}` defaults to `common` and comes from `LoginOpts.Tenant`; if the scope list does not contain `offline_access` it is auto-prepended (Graph will not issue a refresh token otherwise)

### OAUTH-5 — Browser open and headless fallback

- Attempt `browser.OpenURL(authURL)` (via `github.com/pkg/browser`)
- Regardless of success, print to stderr:

  ```
  → If your browser didn't open, visit:
    <auth URL>
  → Waiting for callback on http://127.0.0.1:<port>/callback
  ```

- If `OpenURL` returns an error we do not abort — the user can still paste the URL
- No device-code fallback in v1

### OAUTH-6 — Code → token exchange

POST `application/x-www-form-urlencoded` to the provider's token endpoint with exactly these fields:

```
grant_type=authorization_code
code=<from callback>
code_verifier=<PKCE verifier>
client_id=<config>
redirect_uri=http://127.0.0.1:<port>/callback
```

Token endpoints:

- Google: `https://oauth2.googleapis.com/token`
- Microsoft: `https://login.microsoftonline.com/{tenant}/oauth2/v2.0/token`

The response is parsed into:

```go
type tokenResp struct {
    AccessToken  string `json:"access_token"`
    RefreshToken string `json:"refresh_token"`
    ExpiresIn    int    `json:"expires_in"`
    Scope        string `json:"scope"`   // space-separated; may be absent on MS
    IDToken      string `json:"id_token"`
    TokenType    string `json:"token_type"`
}
```

`ExpiresAt = now + ExpiresIn - 30s` safety skew. Scopes stored on the record are the union of `resp.Scope` (split on whitespace) and the originally requested scopes — Microsoft sometimes omits the echo.

### OAUTH-7 — ID-token parsing

- Split the JWT on `.`, base64url-decode the middle segment
- Extract `sub`, `email`, `upn` as strings (any may be absent)
- `TokenRecord.Subject` precedence: `email` → `upn` → `sub` (first non-empty)
- **No signature verification in v1.** The ID token arrived over a TLS-validated HTTPS response from the provider's own token endpoint; we treat that channel as the proof. Document this in the code comment at the parse site

### OAUTH-8 — Scope validation on `apl token`

- Load `TokenRecord` for `(provider, label)`. If missing: exit 1 with `no account "<label>" for provider "<provider>". Run: apl login <handle>`
- Expand the requested scope alias (single scope per invocation by design)
- If the expanded scope is not in `record.Scopes`: exit 1 with

  ```
  scope "<alias>" not granted for this account.
  Run: apl login <handle> --scope <alias> --force
  ```

  No auto-login. No interactive prompt. The command is meant to be pipeable.

### OAUTH-9 — Auto-refresh

- If `record.ExpiresAt.Sub(now) > 30s`, print `record.AccessToken\n` and exit 0
- Otherwise exchange the refresh token (OAUTH-10), update the record, persist via the `Store` interface, then print
- If persistence fails but the access token is valid we still print the token and exit 0, with a stderr warning — the user's script should not break because of a disk hiccup

### OAUTH-10 — Refresh exchange

POST to the token endpoint with:

```
grant_type=refresh_token
refresh_token=<record.RefreshToken>
client_id=<config>
scope=<space-joined record.Scopes>    # optional but we send it; Google ignores, MS honors
```

Rotation: if the response contains a non-empty `refresh_token`, overwrite `record.RefreshToken`. If it is absent or empty, preserve the existing refresh token. `AccessToken`, `ExpiresAt`, and `IssuedAt` are always updated.

### OAUTH-11 — Error mapping

| Provider response | User-facing message |
|---|---|
| HTTP 400 `invalid_grant` on refresh | `refresh token no longer valid. Run: apl login <handle>` |
| HTTP 400 `invalid_scope` | `one or more scopes were rejected: <scopes>. Run apl scopes <provider> to list valid aliases` |
| HTTP 4xx with `error` field | `<provider> rejected the request: <error>: <error_description>` |
| Network / DNS / TLS failure | `could not reach <provider>: <err>. Check your connection and retry` |
| Token endpoint returned non-JSON | `unexpected response from <provider> token endpoint (HTTP <code>)` |

Exit code: 1 for all. Error messages go to stderr. `apl token` never prints anything to stdout when it fails.

### OAUTH-12 — Security invariants

- PKCE is mandatory on every auth request. There is no code path that omits `code_challenge`
- `state` comparison uses `crypto/subtle.ConstantTimeCompare`
- The loopback listener binds `127.0.0.1` only, never `0.0.0.0`, never `::`
- All outbound HTTPS uses Go's default TLS config (no `InsecureSkipVerify` anywhere; not even behind a flag)
- The authorization code, PKCE verifier, refresh token, and access token are never written to application logs. `stderr` status messages mention only the provider name, label, and port
- The success HTML page contains no token material and no reflected query parameters

### OAUTH-13 — `--force` semantics

- `apl login <handle> --force` runs OAUTH-1 through OAUTH-7 unconditionally, regardless of any existing record
- On success the new record overwrites the old one for that `(provider, label)`. The old refresh token is discarded in memory; revocation of the old refresh token at the provider is **not** performed by `--force` (that is `apl logout`'s job)
- Without `--force`, `apl login` on a `(provider, label)` that already has a record whose `Scopes ⊇ requestedScopes` exits 0 with `already signed in as <subject>` and does nothing else

### OAUTH-14 — Concurrency

- Within one process, only one `Login` may be in flight per `(provider, label)` — enforced by taking a `flock(2)` on `~/.config/apl/locks/<provider>-<label>.lock`
- Cross-process concurrency on the same handle is blocked by the same flock. The second process exits with `another apl login is already running for <handle>`
- Different handles can log in concurrently — each gets its own random loopback port

---

## Design

### Provider interface (refined from PRD §6.2)

```go
// internal/provider/provider.go

package provider

import (
    "context"
    "net/url"

    "github.com/deemwar/apl/internal/store"
)

type Provider interface {
    Name() string // "google" | "microsoft"

    // AuthURL builds the full authorize URL for this provider.
    // `extras` carries PKCE + state + redirect_uri; the provider appends its
    // own quirks (access_type/prompt for Google, tenant path segment for MS).
    AuthURL(opts LoginOpts, extras AuthExtras) (string, error)

    // TokenEndpoint is the absolute URL for authorization_code and
    // refresh_token grants. Microsoft varies it per tenant.
    TokenEndpoint(tenant string) string

    // NormalizeScopes applies provider-specific scope rules. For Microsoft
    // it prepends `offline_access` if missing.
    NormalizeScopes(scopes []string) []string

    // ExpandAliases turns user-facing aliases (gmail.readonly) into full
    // scope URIs. Configured via go:embed YAML per provider.
    ExpandAliases(aliases []string) ([]string, error)
}

type LoginOpts struct {
    Label  string
    Tenant string   // microsoft only; default "common"
    Scopes []string // already expanded, not aliases
    Force  bool
}

type AuthExtras struct {
    ClientID      string
    RedirectURI   string
    State         string
    CodeChallenge string // S256
}
```

### OAuth helpers

```go
// internal/oauth/pkce.go
func NewPKCE() (verifier, challenge string, err error)

// internal/oauth/state.go
func NewState() (string, error)                  // 32 bytes hex
func StateEquals(a, b string) bool               // constant-time

// internal/oauth/loopback.go
type Loopback struct {
    Addr       string      // http://127.0.0.1:<port>
    RedirectURI string     // Addr + /callback
    Result     <-chan Callback
    shutdown   func(context.Context) error
}

type Callback struct {
    Code  string
    State string
    Err   error
}

// Start binds 127.0.0.1:0 and returns immediately. The caller must call
// Stop() exactly once. A successful callback or 5-minute timeout both
// cause Result to yield exactly one value and the server to shut itself.
func Start(expectedState string) (*Loopback, error)

// internal/oauth/exchange.go
func ExchangeCode(ctx context.Context, tokenURL, clientID, code,
    verifier, redirectURI string) (*TokenResponse, error)

func Refresh(ctx context.Context, tokenURL, clientID, refreshToken string,
    scopes []string) (*TokenResponse, error)

// internal/oauth/idtoken.go
func ParseIDToken(idToken string) (sub, email, upn string, err error)
// v1: base64-decode the payload segment only; no signature verification.
```

### Login flow (sequence)

```
  apl CLI        oauth helpers      browser      provider
     │                │                │             │
     │── NewPKCE ────▶│                │             │
     │── NewState ───▶│                │             │
     │── Start() ────▶│ bind 127.0.0.1:0            │
     │◀─── Loopback ──│                             │
     │── AuthURL ────▶│                             │
     │─ browser.Open ─────────────────▶│             │
     │                │                │── GET auth ▶│
     │                │                │◀── 302  ───│
     │                │◀── GET /callback?code&state │
     │                │ validate state              │
     │◀── Callback ───│                             │
     │── ExchangeCode ──────────────────────────────▶│
     │◀── tokens + id_token ────────────────────────│
     │── ParseIDToken ▶│                            │
     │── Store.Put ──▶│                             │
     ▼
   "Signed in as <subject> (label: <label>)"
```

### `apl token` flow

```
  record := store.Get(provider, label)
  if record == nil                     → exit 1 "no account…"
  if !record.HasScope(requestedScope)  → exit 1 "scope not granted… --force"
  if record.ExpiresAt - now > 30s      → print record.AccessToken
  else
      resp := oauth.Refresh(...)
      record.Update(resp)              # preserve refresh_token if unset
      store.Put(record)                # warn-only on failure
      print record.AccessToken
```

---

## Acceptance Criteria

- [ ] `oauth.NewPKCE()` returns a verifier of length 86 and a challenge whose SHA-256 pre-image is the verifier
- [ ] `oauth.NewState()` returns 64 hex chars; `StateEquals` is constant-time (verified by `testing.AllocsPerRun` and table test)
- [ ] `Loopback.Start` binds `127.0.0.1:0` — a unit test asserts the `net.Listener.Addr()` is an `*net.TCPAddr` with `IP.Equal(127.0.0.1)` and `Port != 0`
- [ ] `Loopback` rejects callbacks whose `state` does not match, returning an error on `Result`
- [ ] `Loopback` times out after 5 minutes and yields a timeout error
- [ ] Google `AuthURL` includes `access_type=offline` and `prompt=consent`
- [ ] Microsoft `AuthURL` path contains `/{tenant}/oauth2/v2.0/authorize` and scope list contains `offline_access`
- [ ] `ExchangeCode` sends `grant_type=authorization_code` and includes `code_verifier`
- [ ] `Refresh` preserves the old refresh token when the response omits one; overwrites when present
- [ ] `apl token <handle> --scope X` prints only `<access_token>\n` to stdout on success
- [ ] `apl token <handle> --scope X` with a scope not in the record exits 1, prints only stderr, stdout is empty
- [ ] `apl login <handle>` with an existing valid record exits 0 without opening a browser; `--force` opens a browser
- [ ] All outbound HTTP calls use the default `http.Transport` — a grep for `InsecureSkipVerify` across the package returns no matches
- [ ] The success HTML page is constant — it does not echo `code`, `state`, or any query parameter

---

## Implementation Notes

- Files likely to change / be created:
  - `internal/oauth/pkce.go`, `state.go`, `loopback.go`, `exchange.go`, `idtoken.go`
  - `internal/provider/google.go`, `microsoft.go` — implement the refined `Provider` interface
  - `internal/cli/login.go`, `internal/cli/token.go` — orchestration only
- Use `net/http.Server.Shutdown` (not `Close`) to give the browser response time to flush before the socket dies
- Keep the success HTML at `internal/oauth/success.html` and embed with `//go:embed` so it can be styled without touching Go code
- The flock path (`~/.config/apl/locks/<provider>-<label>.lock`) is the only new path this spec adds to the filesystem — ensure the directory exists at process start and that `apl logout` removes the lock file if present
- Log lines on stderr use `→` for progress and `✓` for success, matching the PRD §5.2 examples
- The HTTP client used for token exchange sets an explicit `Timeout: 30 * time.Second` — the default `http.Client` has no timeout and a stalled provider would hang the CLI
- Scope comparison is set-based on the expanded URI; alias-to-URI expansion happens once at the CLI boundary

---

## Verification

### Unit tests

```bash
go test ./internal/oauth/...
go test ./internal/provider/...
```

Expected coverage: PKCE generation determinism (given a fixed `rand.Reader`), state constant-time equality, loopback bind address + port, Google auth URL shape, Microsoft tenant path + `offline_access` injection, refresh-token rotation preservation, ID-token claim precedence.

### End-to-end with a real provider (Google)

```bash
# 1. Fresh login — opens browser, completes flow, stores record
apl login google:test --scope gmail.readonly
# expected stderr:
#   → Opening browser for Google sign-in…
#   → Waiting for callback on http://127.0.0.1:54221/callback
#   ✓ Signed in as you@example.com (label: test)

# 2. Token retrieval — raw access token to stdout, nothing else
apl token google:test --scope gmail.readonly
# expected stdout: ya29.a0AfH6SMB…<newline>
# expected stderr: (empty)

# 3. Pipeable: curl Gmail API directly
curl -sSf -H "Authorization: Bearer $(apl token google:test --scope gmail.readonly)" \
  https://gmail.googleapis.com/gmail/v1/users/me/profile | jq .emailAddress
```

### Scope-miss scenario

```bash
apl login google:test --scope profile
apl token google:test --scope gmail.readonly
# expected exit code: 1
# expected stdout: (empty)
# expected stderr:
#   scope "gmail.readonly" not granted for this account.
#   Run: apl login google:test --scope gmail.readonly --force
```

### Expired-token refresh scenario

```bash
# Simulate expiry by setting ExpiresAt to the past in the stored record
# (test helper, not a user-facing command)
apltest expire google:test
apl token google:test --scope gmail.readonly
# expected: silent refresh, fresh access token on stdout, updated ExpiresAt in store
```

### Forced re-consent

```bash
apl login google:test --scope gmail.readonly --force
# expected: browser opens again even though a valid record exists
#           new record overwrites old one
```

### Microsoft tenant path

```bash
apl login ms:volentis --tenant contoso.onmicrosoft.com --scope Mail.Read
# inspect captured auth URL — must begin with
#   https://login.microsoftonline.com/contoso.onmicrosoft.com/oauth2/v2.0/authorize?
# and scope must contain offline_access
```

### Headless fallback

On a host where `browser.OpenURL` is stubbed to return an error, `apl login google:test` must still print the auth URL + loopback URL to stderr and wait 5 minutes for a callback — verified via a unit test that injects a fake browser opener.
