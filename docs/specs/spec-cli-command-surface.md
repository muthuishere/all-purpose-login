# Spec: CLI Command Surface

**Status:** Proposed
**Priority:** High
**Depends on:** `spec-setup-bootstrapper.md`, `spec-oauth-pkce-loopback.md`, `spec-token-storage.md`

---

## Problem

`apl` is a token-broker CLI for Google and Microsoft. Users will script against it (`TOKEN=$(apl token google:work --scope gmail.readonly)`), pipe its output into `curl`, and parse its listings from shell and other languages. Before any internal flow is built, the user-facing surface — handles, flags, stdout/stderr contracts, exit codes, error strings — has to be pinned down so that:

- Scripts written against v1 keep working in v2.
- Every subcommand has a single, predictable output shape.
- Errors tell the user the exact next command to run.

The original PRD §5.1 used `<provider> --as <label>`. That has since been replaced (huddle 2026-04-23) by rclone-style `provider:label` handles. This spec is the canonical definition of the resulting command surface.

---

## Goals

- Define every user-facing `apl` command, its arguments, and its flags.
- Pin the handle grammar (`provider:label`) and its validation rule.
- Fix exit codes so scripts can branch on them reliably.
- Fix the stdout contract for `apl token` (pipeable to `curl`) and for `apl accounts` (table vs JSON).
- Specify error message style and concrete example strings for the common failure modes.

---

## Non-Goals

- OAuth/PKCE flow internals — see `spec-oauth-pkce-loopback.md`.
- Setup bootstrapper behavior (`apl setup`) beyond its CLI signature — see `spec-setup-bootstrapper.md`.
- Token persistence (keychain, file fallback, encryption) — see `spec-token-storage.md`.
- Scope alias expansion semantics beyond what `apl scopes` prints — that belongs in a provider-registry spec.
- Shell completion, man pages, long-form help text content.

---

## Functional Requirements

### CLI-1 — Binary and top-level invocation

- The shipped binary is named `apl`. No long-name symlink is installed.
- `apl` with no arguments prints short usage to stdout and exits 0.
- `apl --help` / `apl -h` prints help to stdout, exits 0.
- Unknown subcommand: print `unknown command "X". Run: apl --help` to stderr, exit 1.

### CLI-2 — Handle grammar

- A handle is `provider:label`.
- Regex: `^[a-z]+:[a-zA-Z0-9._-]+$`.
  - Provider segment: lowercase letters only.
  - Label segment: letters, digits, `.`, `_`, `-`.
- Parsing is pure-lexical and must NOT consult storage.
- Invalid handle → stderr `invalid handle "X". Expected form: provider:label (e.g. google:work)`, exit 1.
- A bare provider with no colon (`apl login google`) is rejected with:
  `missing label. Use provider:label form, e.g. apl login google:work`, exit 1.
- Unknown provider (well-formed handle, provider not registered) → stderr
  `unknown provider "X". Known providers: google, ms`, exit 1.

### CLI-3 — Exit codes

Fixed contract, used uniformly across commands:

| Code | Meaning |
|---|---|
| `0` | Success. |
| `1` | User error: invalid args, unknown subcommand, malformed handle, unknown provider, missing required flag. |
| `2` | Auth/token error: no stored account, scope not granted, refresh token expired, provider returned `invalid_grant`. |
| `3` | Network / provider error: DNS failure, timeout, 5xx from provider, loopback bind failure. |

Panics and unexpected runtime errors exit `1` with a stderr line prefixed `internal error:`.

### CLI-4 — `apl setup [provider]`

- Arguments: optional positional provider (`google` or `ms`). None → interactive bootstrapper covers both.
- Flags: `--reconfigure` forces re-prompt even if already configured.
- Output: human-readable progress on stdout. Exit 0 on completion, 1 on user-cancelled, 3 on network/API error.
- Internals deferred to `spec-setup-bootstrapper.md`.

### CLI-5 — `apl login <handle>`

- Positional: exactly one handle (see CLI-2).
- Flags:
  - `--tenant <id>` — Microsoft only. Passing `--tenant` with a `google:` handle is a user error: stderr `--tenant is only valid for the ms provider`, exit 1.
  - `--scope <scope>` — repeatable. Optional. If omitted, uses provider default minimal scopes:
    - `google`: `openid email profile`
    - `ms`: `openid profile offline_access User.Read`
  - `--force` — re-consent even if a token for this handle already exists.
- Behavior summary: runs the OAuth flow (see dependent spec), persists the record under the handle, prints `Signed in as <email> (handle: <provider>:<label>)` on success.
- Exit: 0 success, 1 user error, 2 auth denied / flow aborted, 3 network error.

### CLI-6 — `apl token <handle> --scope <scope>`

- Positional: exactly one handle.
- Required flag: `--scope <scope>` (single value; repeats are a user error — token output is for one API call).
- stdout contract on success: the raw access token followed by a single `\n`. Nothing else is ever written to stdout. This makes `TOKEN=$(apl token google:work --scope gmail.readonly)` safe under `set -e` and under shells that strip trailing newlines from command substitution.
- stderr on success: empty.
- No `--json` flag. Structured introspection is a separate future command.
- Auto-refresh: if stored access token is within 30s of expiry or already expired, the command refreshes silently using the stored refresh token before printing.
- If the requested scope is not in the stored record's granted-scopes set, fail hard — do NOT auto-trigger a login:
  - stderr: `scope "<scope>" not granted for <handle>. Run: apl login <handle> --scope <scope> --force`
  - exit 2.
- If no account is stored for this handle:
  - stderr: `no account for <handle>. Run: apl login <handle>`
  - exit 2.
- If refresh fails with `invalid_grant` / refresh token expired:
  - stderr: `token refresh failed: refresh token expired. Run: apl login <handle>`
  - exit 2.

### CLI-7 — `apl logout <handle>`

- Positional: exactly one handle.
- Flags: none in v1.
- Behavior: best-effort revoke refresh token at provider, then delete local record.
- Output on success: `Removed <handle>` on stdout. Exit 0.
- If no account exists: stderr `no account for <handle>`, exit 2. (We do not silently succeed — the user asked to remove something that isn't there.)
- If revoke call fails but local delete succeeds, print warning to stderr and still exit 0:
  `warning: provider revoke failed (<reason>); local record removed`.

### CLI-8 — `apl accounts [--json]`

- No positional args.
- Flag: `--json` — emit machine-readable JSON array to stdout instead of a table.

**Table mode (default).** Fixed column order, space-padded, single header row:

```
PROVIDER   LABEL      EMAIL                        TENANT                       STORED
google     work       muthu@deemwar.com            -                            keychain
google     personal   muthuishere@gmail.com        -                            keychain
ms         volentis   muthu@volentis.ai            volentis.onmicrosoft.com     keychain
```

- `TENANT` column is always present; value is `-` for Google and for Microsoft accounts that used the `common` tenant.
- `STORED` is one of `keychain` or `file`.
- Empty list: print a single informational line to stdout `No accounts. Run: apl login <provider>:<label>` and exit 0. (Not an error.)

**JSON mode.** Exactly this shape:

```json
[
  {
    "provider": "google",
    "label": "work",
    "handle": "google:work",
    "email": "muthu@deemwar.com",
    "tenant": null,
    "stored": "keychain"
  },
  {
    "provider": "ms",
    "label": "volentis",
    "handle": "ms:volentis",
    "email": "muthu@volentis.ai",
    "tenant": "volentis.onmicrosoft.com",
    "stored": "keychain"
  }
]
```

- Always a JSON array, even when empty (`[]`).
- Trailing newline after the closing `]`.
- Fields are stable; new fields may be added in later versions but existing ones must not change name or type.

### CLI-9 — `apl scopes <provider>`

- Positional: exactly one provider (`google` or `ms`). Not a handle.
- Output to stdout, two-column aligned, alias first then full scope URI:

```
gmail.readonly      https://www.googleapis.com/auth/gmail.readonly
gmail.send          https://www.googleapis.com/auth/gmail.send
calendar            https://www.googleapis.com/auth/calendar
profile             openid email profile
```

- One alias per line. Aliases expanded to multiple scopes are rendered space-separated on the right column.
- Unknown provider: same error as CLI-2.
- Exit 0 on success.

### CLI-10 — `apl version`

- No args, no flags.
- stdout: `apl <semver> (<git-sha>, <build-date>)` followed by `\n`, e.g. `apl 0.3.0 (abc1234, 2026-04-23)`.
- Exit 0.

### CLI-11 — Error message style

- All error messages go to stderr, never stdout.
- Every actionable error ends with a `Run: <exact command>` suggestion when a recovery command exists.
- No stack traces or Go-style error wrappers reach the user; they are logged under `APL_DEBUG=1` only.
- Messages are lowercase first letter, no trailing period, imperative where relevant — matching the style of `git` and `gh`.

### CLI-12 — Global flags

- `--help` / `-h` on any subcommand prints that subcommand's help to stdout, exit 0.
- `APL_DEBUG=1` env var enables verbose logs to stderr. Never required for normal use.
- No `--verbose`, `--quiet`, or `--config` flags in v1.

---

## Acceptance Criteria

- [ ] CLI-2: `apl login google` exits 1 with stderr suggesting `google:<label>` form.
- [ ] CLI-2: `apl login GOOGLE:Work` exits 1 (uppercase provider rejected).
- [ ] CLI-2: `apl login google:work!` exits 1 (invalid label char).
- [ ] CLI-2: `apl login slack:x` exits 1 with `unknown provider "slack"`.
- [ ] CLI-5: `apl login ms:x --tenant foo` is accepted; `apl login google:x --tenant foo` exits 1.
- [ ] CLI-6: `TOKEN=$(apl token google:work --scope gmail.readonly); echo "[$TOKEN]"` yields `[ya29....]` with no embedded whitespace.
- [ ] CLI-6: `apl token google:nonexistent --scope x` exits 2 with `no account for google:nonexistent. Run: apl login google:nonexistent`.
- [ ] CLI-6: `apl token google:work --scope scope.not.granted` exits 2 and does NOT open a browser.
- [ ] CLI-6: expired access token with valid refresh token → token output works, no user prompt.
- [ ] CLI-7: `apl logout google:nonexistent` exits 2.
- [ ] CLI-8: `apl accounts --json | jq '.[0].handle'` returns a string matching the handle regex.
- [ ] CLI-8: `apl accounts` with zero accounts exits 0 and prints a hint (not an error).
- [ ] CLI-9: `apl scopes google` lists at least `gmail.readonly`, `gmail.send`, `calendar`, `profile` aliases.
- [ ] CLI-10: `apl version` stdout matches `^apl \d+\.\d+\.\d+ \([a-f0-9]+, \d{4}-\d{2}-\d{2}\)$`.
- [ ] CLI-3: every failure path in the suite above exits with the documented code (1, 2, or 3).

---

## Implementation Notes

- Use `cobra` for command wiring. One file per command under `internal/cli/` mirroring PRD §6.1 layout.
- Handle parsing lives in `internal/handle/handle.go` as a pure function with a compiled regex — do not let cobra split on `:`.
- Exit codes are centralised in `internal/cli/exit.go`:

```go
const (
    ExitOK       = 0
    ExitUser     = 1
    ExitAuth     = 2
    ExitNetwork  = 3
)
```

- `apl token` output must use `fmt.Println(token)` (single trailing `\n`); never `fmt.Print` with manual newline handling, never structured log libraries that flush extra whitespace.
- `apl accounts --json` uses `encoding/json` with `SetIndent("", "  ")` for readability; script consumers should `jq` over it rather than parse raw — indentation is not part of the contract, field names and types are.
- Error strings are defined as constants in `internal/cli/errors.go` so tests can assert on them.
- Help text for each command is owned by the command file; keep it under 15 lines.

---

## Design (optional)

Minimal shared interfaces the command layer uses. Full implementations covered by dependent specs.

```go
// internal/handle/handle.go
type Handle struct {
    Provider string // "google" | "ms"
    Label    string
}

func Parse(s string) (Handle, error) // enforces CLI-2 regex
func (h Handle) String() string       // "provider:label"
```

```go
// internal/cli/runner.go
type Runner interface {
    Login(ctx context.Context, h handle.Handle, opts LoginOpts) error
    Token(ctx context.Context, h handle.Handle, scope string) (string, error)
    Logout(ctx context.Context, h handle.Handle) error
    Accounts(ctx context.Context) ([]AccountRow, error)
    Scopes(provider string) ([]ScopeAlias, error)
}
```

The CLI layer is a thin adapter: parse flags → build `Runner` call → map errors to exit codes and stderr strings per CLI-11.

---

## Verification

Run these from a shell against a built `apl` binary. Assumes `google:work` has been set up via `apl setup google` and `apl login google:work`.

```bash
# CLI-2: handle validation
apl login google;          echo "exit=$?"   # expect: error, exit=1
apl login google:work!;    echo "exit=$?"   # expect: error, exit=1
apl login slack:x;         echo "exit=$?"   # expect: unknown provider, exit=1

# CLI-5: tenant flag gate
apl login google:x --tenant foo; echo "exit=$?"  # expect: error, exit=1

# CLI-6: token pipeability
TOKEN=$(apl token google:work --scope gmail.readonly)
test -n "$TOKEN" && echo "got token, len=${#TOKEN}"
curl -s -H "Authorization: Bearer $TOKEN" \
  https://gmail.googleapis.com/gmail/v1/users/me/profile | jq .emailAddress

# CLI-6: missing account
apl token google:does-not-exist --scope gmail.readonly; echo "exit=$?"  # expect exit=2

# CLI-6: ungranted scope does NOT open browser
apl token google:work --scope gmail.send 2>&1 | grep -q 'not granted'
echo "exit=$?"  # expect 0 (grep matched)

# CLI-7: logout missing
apl logout google:nobody; echo "exit=$?"  # expect exit=2

# CLI-8: accounts JSON
apl accounts --json | jq -e 'type == "array"'
apl accounts --json | jq -r '.[].handle' | grep -E '^[a-z]+:[a-zA-Z0-9._-]+$'

# CLI-9: scopes
apl scopes google | grep -E '^gmail\.readonly\s+https://'

# CLI-10: version
apl version | grep -E '^apl [0-9]+\.[0-9]+\.[0-9]+ \([a-f0-9]+, [0-9]{4}-[0-9]{2}-[0-9]{2}\)$'
```

Automated coverage: an integration test binary under `test/cli/` drives a built `apl` and asserts exit codes + stderr substrings for every bullet in Acceptance Criteria.
