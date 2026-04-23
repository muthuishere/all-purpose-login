# Spec: Token Storage Layer

**Status:** Proposed
**Priority:** High
**Depends on:** none
**Referenced by:** `spec-oauth-pkce-loopback.md`, `spec-cli-command-surface.md`, `spec-setup-bootstrapper.md`

---

## Problem

`apl` is a local-first OAuth token broker. Every `apl login`, `apl token`, `apl accounts`, and `apl logout` invocation has to read or write two distinct pieces of state on the user's machine:

1. **Static configuration** — the per-provider OAuth client IDs (and, for Microsoft, the tenant / project metadata) the user pastes in during `apl setup`. This is non-secret but user-specific.
2. **Per-account secrets** — one `TokenRecord` per `provider:label` handle, holding a refresh token, a possibly-stale access token, the granted scopes, and the subject (email/UPN).

Without a single, documented storage contract we risk:

- Scattering file-layout and keychain-key conventions across every command.
- Leaking tokens to logs, stdout, or crash traces.
- Losing accounts when two concurrent `apl token` calls race on a refresh.
- Painting ourselves into a corner when the file-encrypted fallback lands (later spec).

This spec locks the v0 storage layer: config on disk, secrets in the OS keychain only. No encryption layer of our own. No file fallback yet.

---

## Goals

- Define a single `store.Store` Go interface that every command uses — no direct `go-keyring` calls outside `internal/store/`.
- Lock the on-disk layout: `~/.config/apl/config.yaml` (XDG-aware), dir `0700`, file `0600`.
- Lock the keychain identity: service `all-purpose-login`, account `{provider}:{label}`, secret = JSON-encoded `TokenRecord`.
- Make `Put` / `Get` / `List` / `Delete` atomic at the OS level; no hand-rolled locking.
- Define `TokenRecord` once, with stable JSON tags, so later versions can evolve it additively.
- Forbid tokens in logs, error messages, and stdout (except the single `apl token` line that prints the access token itself).

---

## Non-Goals

- **Age-encrypted file fallback + Argon2id passphrase mode.** Deferred to `spec-token-storage-file-fallback.md`. v0 is keychain-only; on systems without a usable keychain we fail hard with a clear error.
- **Cross-machine sync / export / import.** Users re-run `apl login` on a new laptop. This is a principle (PRD §2), not a missing feature.
- **OAuth flow mechanics** (PKCE, loopback, browser opening) — see `spec-oauth-pkce-loopback.md`.
- **CLI surface** (`login`, `token`, `accounts`, `logout`) — see `spec-cli-command-surface.md`.
- **The `apl setup` bootstrapper** that writes the initial config.yaml — see `spec-setup-bootstrapper.md`. This spec only defines the *shape* of the file it produces.
- **Encryption-at-rest for config.yaml.** The file contains client IDs only; they are not secret. File mode `0600` is enough.
- **Windows-specific tuning.** `go-keyring` supports Credential Manager natively; we document it as best-effort for v0 and test on macOS + Linux first.

---

## Functional Requirements

### STORE-1 — Config file location and XDG compliance

The config file lives at `$XDG_CONFIG_HOME/apl/config.yaml`. If `$XDG_CONFIG_HOME` is unset or empty, fall back to `~/.config/apl/config.yaml`. On macOS we deliberately do *not* use `~/Library/Application Support/` — `~/.config/` is what CLI users expect and what our docs will show.

The directory is created on first write with mode `0700`. The file is created with mode `0600`.

### STORE-2 — Config file permissions are a read-time invariant

On every read, `store` stats the config file and refuses to proceed if either:

- the directory mode has group or world bits set, or
- the file mode has group or world bits set, or
- the file is not owned by the current uid.

The error is actionable and names the exact `chmod` / `chown` the user should run. We do not silently fix permissions — a loose-permissions config is a signal that something else went wrong (a backup restore, a shared home dir) and we want the user to look at it.

### STORE-3 — Config file schema

YAML, two top-level keys. Both are optional (a fresh install has neither until `apl setup` runs).

```yaml
# ~/.config/apl/config.yaml
google:
  client_id: "123456789012-abcdefghijklmnopqrstuvwxyz012345.apps.googleusercontent.com"
  project_id: "apl-muthu-dev"

microsoft:
  client_id: "11111111-2222-3333-4444-555555555555"
  tenant: "common"
```

Unknown keys are preserved on write (round-trip fidelity) so that a newer `apl` version's config doesn't get stripped by an older binary.

### STORE-4 — Config file atomic writes

Writes go to a sibling temp file (`config.yaml.<pid>.tmp`), `fsync`, then `os.Rename` onto `config.yaml`. On failure the temp file is removed. Readers never see a half-written file.

### STORE-5 — `TokenRecord` schema

The struct matches PRD §6.2 with one addition — a `Handle` convenience field so callers don't have to re-concat the key. All time fields are RFC 3339 UTC. `omitempty` is used where a zero value is meaningful-as-absent (tenant, issued_at).

### STORE-6 — `Store` interface

Four methods: `Put`, `Get`, `List`, `Delete`. All take a `context.Context` even though the keychain backend is synchronous — this keeps future backends (network-backed, rate-limited) drop-in.

### STORE-7 — Keychain identity

- Service: the literal string `all-purpose-login`.
- Account: `{provider}:{label}`, matching the CLI handle exactly. Example: `google:work`, `microsoft:volentis`.
- Secret: the full `TokenRecord` JSON-marshalled with `json.Marshal` (compact, no indent).

No additional encryption wrapper. The keychain *is* the security boundary (PRD §7).

### STORE-8 — `List()` implementation

`go-keyring` does not ship a portable "list all accounts for a service" primitive across every backend. We handle this by maintaining an **index entry** alongside the records:

- Account `__index` under the same service holds a JSON array of handle strings.
- `Put` adds the handle to the index (idempotent).
- `Delete` removes it.
- `List` reads the index, then `Get`s each handle.

If the index exists but a referenced entry is missing (e.g. the user pruned the keychain manually), `List` silently drops the orphan and rewrites the index. It does not fail the whole call.

### STORE-9 — Missing or locked keychain

On a system with no usable keychain backend (e.g. headless Linux without `libsecret` / a running Secret Service), every `Store` operation fails with:

```
apl: keychain unavailable on this system (libsecret not found / Secret Service not running).
     An encrypted-file fallback is planned for a later release.
     See: https://github.com/muthuishere/all-purpose-login/issues/<tbd>
```

On macOS, the first access in a session may trigger a Keychain prompt ("allow `apl` to access key …"). This is expected and documented in the user-facing README; the store layer makes no attempt to suppress or pre-authorise it.

### STORE-10 — Delete revokes first, then wipes

`Delete(handle)` must:

1. Call the provider's token revocation endpoint with the stored refresh token (so the server-side grant is dropped).
2. Only if revocation succeeds — or returns a definitive "already revoked / unknown token" response — remove the local record and update the index.

If revocation fails with a network error, `Delete` returns the error and leaves the local record intact, so the user can retry. A `--force` flag on `apl logout` (defined in the CLI spec) bypasses this and wipes local state regardless. The revocation call itself lives in the `oauth` package — `Delete` calls into it via an injected function.

### STORE-11 — Concurrency

Two `apl token` processes may try to refresh the same account at once. The design accepts this:

- Each process writes its own freshly-refreshed record.
- Keychain writes are atomic at the OS level; last write wins.
- Both records are individually valid. The cost is one wasted refresh round-trip, not corruption.

No advisory locks, no pid files, no retries. If we ever need strict single-writer semantics we will revisit.

### STORE-12 — Logging and error discipline

- Token values (access, refresh, id) are never logged, never included in error messages, never printed except the dedicated stdout line in `apl token`.
- All log lines at `INFO` include `handle`, action (`put|get|list|delete`), and result (`ok|error`), and nothing else from the record.
- Errors bubbling up from `go-keyring` are wrapped with `fmt.Errorf("store: %w", err)` so callers can `errors.Is` against sentinel errors: `ErrNotFound`, `ErrKeychainUnavailable`, `ErrCorruptRecord`.
- `ErrCorruptRecord` fires when the keychain returns a value that fails `json.Unmarshal`. The error message says which handle, not the raw bytes.

---

## Design

### Package layout

```
internal/store/
├── store.go      // interface, sentinel errors, factory
├── record.go     // TokenRecord + JSON helpers
├── config.go     // Config struct, load/save, permission checks
└── keyring.go    // keychainStore implementing Store
```

### Types

```go
package store

import (
    "context"
    "errors"
    "time"
)

// Sentinel errors. Callers use errors.Is.
var (
    ErrNotFound            = errors.New("store: record not found")
    ErrKeychainUnavailable = errors.New("store: keychain unavailable")
    ErrCorruptRecord       = errors.New("store: corrupt record")
    ErrBadPermissions      = errors.New("store: config file permissions too open")
)

// TokenRecord is the unit of per-account state. Serialised as JSON into
// the OS keychain, one record per {provider, label}.
type TokenRecord struct {
    Handle       string    `json:"handle"`            // "{provider}:{label}" — convenience duplicate
    Provider     string    `json:"provider"`          // "google" | "microsoft"
    Label        string    `json:"label"`             // user-chosen, e.g. "work"
    Subject      string    `json:"sub"`               // email for google, UPN for microsoft
    Tenant       string    `json:"tenant,omitempty"`  // microsoft only
    RefreshToken string    `json:"refresh_token"`
    AccessToken  string    `json:"access_token,omitempty"`
    ExpiresAt    time.Time `json:"expires_at"`
    Scopes       []string  `json:"scopes"`            // full URIs, not aliases
    IssuedAt     time.Time `json:"issued_at,omitempty"`
}

// Store is the only API other packages use for token persistence.
type Store interface {
    Put(ctx context.Context, rec *TokenRecord) error
    Get(ctx context.Context, handle string) (*TokenRecord, error)
    List(ctx context.Context) ([]*TokenRecord, error)
    Delete(ctx context.Context, handle string) error
}

// New returns the default (keychain) Store.
// In v0 this is the only implementation.
func New() (Store, error)
```

### Config types

```go
package store

type Config struct {
    Google    ProviderConfig `yaml:"google,omitempty"`
    Microsoft ProviderConfig `yaml:"microsoft,omitempty"`
}

type ProviderConfig struct {
    ClientID  string `yaml:"client_id,omitempty"`
    ProjectID string `yaml:"project_id,omitempty"` // google only
    Tenant    string `yaml:"tenant,omitempty"`     // microsoft only
}

// LoadConfig reads ~/.config/apl/config.yaml, validates permissions,
// and returns the parsed struct. Missing file is not an error — it
// returns a zero Config and os.ErrNotExist wrapped.
func LoadConfig() (*Config, error)

// SaveConfig writes atomically (temp + rename) with mode 0600,
// creating the parent dir with 0700 if needed.
func SaveConfig(cfg *Config) error
```

### Keychain backend sketch

```go
package store

const (
    keychainService = "all-purpose-login"
    indexAccount    = "__index"
)

type keychainStore struct {
    // injected so tests can stub go-keyring and the revoker.
    get    func(service, account string) (string, error)
    set    func(service, account, secret string) error
    delete func(service, account string) error
    revoke func(ctx context.Context, rec *TokenRecord) error
}

func (s *keychainStore) Put(ctx context.Context, rec *TokenRecord) error {
    rec.Handle = rec.Provider + ":" + rec.Label
    blob, err := json.Marshal(rec)
    if err != nil { return err }
    if err := s.set(keychainService, rec.Handle, string(blob)); err != nil {
        return mapKeychainErr(err)
    }
    return s.indexAdd(rec.Handle)
}

func (s *keychainStore) Delete(ctx context.Context, handle string) error {
    rec, err := s.Get(ctx, handle)
    if err != nil { return err }
    if err := s.revoke(ctx, rec); err != nil {
        return fmt.Errorf("store: revoke before delete failed: %w", err)
    }
    if err := s.delete(keychainService, handle); err != nil {
        return mapKeychainErr(err)
    }
    return s.indexRemove(handle)
}
```

`mapKeychainErr` translates `go-keyring`'s errors into our sentinels — `keyring.ErrNotFound` → `ErrNotFound`; "Secret Service not available" (Linux) → `ErrKeychainUnavailable`.

---

## Acceptance Criteria

- [ ] Every command in `internal/cli/` that touches token state goes through `store.Store`; no direct `go-keyring` import outside `internal/store/`.
- [ ] `store.New()` succeeds on a stock macOS machine and round-trips a `TokenRecord` via Put/Get.
- [ ] `apl accounts` lists all records that were `Put` during a test session, in any order, via `Store.List()`.
- [ ] `apl logout google:work` calls the Google revocation endpoint before removing the local record; if the revocation call is stubbed to fail with a network error, the record is still present afterwards.
- [ ] `LoadConfig()` returns `ErrBadPermissions` when `config.yaml` is `chmod 0644`.
- [ ] `SaveConfig` never produces a partial file: killing the process mid-write (simulated via a temp-file-write failure injection) leaves the previous `config.yaml` intact.
- [ ] The access token and refresh token values never appear in any log line emitted by the store package (verified by a test that captures structured logs).
- [ ] On Linux without Secret Service running, `store.New()` or the first `Put` returns `ErrKeychainUnavailable` with the documented message.
- [ ] An entry added via `security add-generic-password -s all-purpose-login -a google:manual` with a valid JSON record is visible to `apl accounts` once its handle is added to the index (documents the index contract).

---

## Implementation Notes

- Library: `github.com/zalando/go-keyring`. Pin the version in `go.mod` once the M1 milestone lands — let v1 dogfooding choose the pin.
- Config YAML parser: `gopkg.in/yaml.v3`. Use `yaml.Node` round-tripping if we find users losing unknown keys; otherwise `yaml.Unmarshal` / `yaml.Marshal` is fine.
- The index entry (`__index`) is a pragmatic workaround for the lack of a cross-backend `List`. If a future `go-keyring` release exposes a portable list, we drop the index and migrate once.
- `context.Context` on every method is forward-looking; the keychain backend ignores cancellation. Document this on the interface.
- Keep `internal/store/` free of `cobra`, `fmt.Println`, and any CLI concerns. It is a library package.
- Tests: use an in-memory fake that implements `Store` for everyone else's unit tests; reserve real-keychain tests for a `//go:build darwin` integration file gated on `APL_KEYCHAIN_TEST=1`.

---

## Verification

Manual, on macOS:

```bash
# 1. Fresh install: config file is created with correct perms.
rm -rf ~/.config/apl
apl setup google   # (covered by spec-setup-bootstrapper.md)
stat -f "%Sp %Su" ~/.config/apl/config.yaml
# expect: -rw-------  muthu

# 2. Permission-tampering detection.
chmod 0644 ~/.config/apl/config.yaml
apl accounts
# expect: error naming chmod 0600 as the fix; non-zero exit.

# 3. Round-trip a token via the real flow.
chmod 0600 ~/.config/apl/config.yaml
apl login google:work --scope profile
apl accounts
# expect: one row, provider=google, label=work, storage=keychain.

# 4. Prove the record is actually in the macOS Keychain.
security find-generic-password -s all-purpose-login -a google:work -w
# expect: a JSON blob starting with {"handle":"google:work",...}. Close the terminal buffer after.

# 5. Logout revokes + wipes.
apl logout google:work
apl accounts
# expect: empty. And the refresh token is invalid at Google's token endpoint.

# 6. Concurrency smoke test.
apl login google:work --scope gmail.readonly
( apl token google:work --scope gmail.readonly >/dev/null & \
  apl token google:work --scope gmail.readonly >/dev/null & \
  wait )
apl accounts --json | jq '.[0].expires_at'
# expect: a valid future timestamp; no "corrupt record" error from either child.
```

Automated (CI, Linux + macOS):

- `go test ./internal/store/...` with the fake keychain.
- A `darwin`-gated integration test that `Put`s, `List`s, `Get`s, `Delete`s against the real Keychain, guarded by `APL_KEYCHAIN_TEST=1` so it only runs on Muthu's machine / an unlocked CI runner.
- A lint rule (or `grep` check in `Taskfile.yml`) that fails the build if any file outside `internal/store/` imports `github.com/zalando/go-keyring`.
