# Spec: `apl call` — HTTP Relay Subcommand

**Status:** Proposed
**Priority:** High
**Depends on:** `spec-cli-command-surface.md`, `spec-token-storage.md`, `spec-oauth-pkce-loopback.md`

---

## Problem

`apl` today is a token broker. `apl login <handle>` prints a valid access token to stdout and auto-refreshes transparently. That's **Mode A**: shell scripts wrap the token into their own `curl`/SDK call:

```bash
TOKEN=$(apl login google:work)
curl -H "Authorization: Bearer $TOKEN" https://gmail.googleapis.com/gmail/v1/users/me/profile
```

This works, but it has two sharp edges when the consumer is an LLM agent (specifically the Claude Code companion skill `all-purpose-data-skill`):

1. **Two shell calls per request** — the agent must first extract the token, then issue curl. That's two tool calls, two transcripts, and one extra place to leak or truncate the token.
2. **Mid-session 401s leak out** — if the stored access token happens to expire between `apl login` and `curl`, the agent sees a 401 and has to invent a retry loop. Refresh logic gets reimplemented in every caller.

**Mode B** fixes both: `apl call <handle> <METHOD> <url>` issues the request itself, injects the bearer, streams the response, and silently retries once on 401 after refreshing. One tool call. No recoverable 401s escape.

`apl call` is deliberately narrow. It is an HTTP relay with token injection — not a SDK, not a client, not a smart proxy. No retry policy, no pagination, no request rewriting, no $select injection.

---

## Goals

- Add `apl call <handle> <METHOD> <url> [flags]` as a sibling of `apl login`.
- Reuse existing `Provider.Refresh` and `Store` — no new OAuth or storage primitives.
- Inject `Authorization: Bearer <token>` using the stored record, refreshing if stale.
- Silently refresh-and-retry exactly once on a 401 response.
- Stream the response body byte-for-byte to stdout (or `-o <path>`).
- Pin a distinct exit code for 429 so agent-side backoff stays simple.
- Match the stdout/stderr discipline of `apl login`: body on stdout, diagnostics on stderr, and only when stderr is a TTY.

---

## Non-Goals

- **No retry policy beyond the single 401 bounce.** No exponential backoff on 429 or 5xx. No idempotency keys. Callers handle that.
- **No cookie jar.** Each call is stateless.
- **No request signing beyond bearer.** No AWS SigV4. No mTLS. No HMAC.
- **No interactive output.** No progress bars, no spinners. Body streams as-is.
- **No response parsing.** No JSON pretty-printing, no `$select` injection, no pagination follow-through (`@odata.nextLink`, `nextPageToken`). Body passes through untouched.
- **No HTTP/2 push.** Standard `net/http` defaults.
- **No SSE handling.** If the provider returns `text/event-stream`, we pass it through and let the caller parse.
- **No implicit base URL.** `<url>` must be absolute. No `apl call google:work GET /gmail/v1/...` sugar — keeps the spec simple and the agent honest about what it's hitting.
- **No multi-request batching.** One `apl call` = one HTTP request.

---

## Functional Requirements

### CALL-1 — Handle parsing and scope pre-check

- Positional `<handle>` is parsed via `internal/handle.Parse` (same regex as CLI-2).
- Invalid handle → stderr `apl call: invalid handle "X" (user error)`, exit 1.
- Unknown provider → stderr `apl call: unknown provider "X" (user error)`, exit 1.
- No stored record for the handle → stderr `apl call: no account for <handle>. Run: apl login <handle> (auth error)`, exit 2.
- `--scope <s>` is repeatable. Before issuing the request, the command asserts that **every** listed scope is present in the stored record's granted scopes. If any are missing, stderr `apl call: missing scope(s): <list>. Run: apl login <handle> --force --scope <s> ... (auth error)`, exit 2.
- Scope check happens before any refresh or network call.

### CALL-2 — Token refresh before issuing the request

- Load the `TokenRecord` from `Store`.
- Call `Provider.Refresh(ctx, rec)` — same path `apl login` uses. The provider returns early if the access token has >30s headroom.
- On refresh failure with `invalid_grant` → stderr `apl call: refresh token expired. Run: apl login <handle> --force (auth error)`, exit 2.
- On refresh failure due to network → exit 3.
- Persist the refreshed record via `Store.Put` before issuing the request.

### CALL-3 — Bearer injection

- The outgoing request always carries `Authorization: Bearer <rec.AccessToken>`.
- If the caller supplies `-H 'Authorization: ...'`, it is rejected: stderr `apl call: cannot override Authorization header (user error)`, exit 1. (`apl call` owns auth by definition — otherwise there's no reason to use it.)

### CALL-4 — Request body from `--body` or `--body-file`

- `--body '<string>'` sends the literal string as the request body.
- `--body-file <path>` streams the file as the request body. `-` means stdin.
- Passing both is a user error: stderr `apl call: --body and --body-file are mutually exclusive (user error)`, exit 1.
- Neither → no body, `Content-Length: 0` on methods that carry one.
- Body flags are valid for any `<METHOD>`; the command does not second-guess (a `GET` with a body is the caller's problem).

### CALL-5 — Auto `Content-Type: application/json`

- If a body is present and no user-supplied `Content-Type` is in `-H`, set `Content-Type: application/json`.
- If `--content-type <mime>` is supplied, it wins over both the auto default and any `-H 'Content-Type: ...'`. (Explicit flag beats header-bag ordering.)
- Supplying both `--content-type` and `-H 'Content-Type: ...'` is a user error: exit 1.

### CALL-6 — Custom headers via repeatable `-H`

- `-H 'Key: Value'` may be passed multiple times.
- Split on the **first** colon; trim one leading space from the value if present. This matches `curl` behaviour.
- Malformed (`-H 'nocolon'`) → stderr `apl call: malformed header "nocolon" (user error)`, exit 1.
- Duplicate keys are allowed and sent as multiple header lines (HTTP permits this).

### CALL-7 — Output destination

- Default: response body streams to stdout, byte-for-byte, no transcoding, no trailing newline injected.
- `-o <path>`: body is written to `<path>` instead. On success the path is **not** echoed to stdout (stdout stays empty so `apl call ... -o out.bin && cat out.bin` composes cleanly).
- `-o -` is equivalent to the default (explicit stdout).
- If `-o <path>` is given and the HTTP status is non-2xx, the body is still written to the file (callers asked for it; they may want to inspect the error body). The exit code still reflects the status.

### CALL-8 — Auto refresh-and-retry on a single 401

- First attempt uses the token from CALL-2.
- If the response status is **exactly 401**:
  1. Close/drain the first response body.
  2. Call `Provider.Refresh(ctx, rec)` with `force=true` (bypass the >30s headroom check) on a fresh context.
  3. If refresh fails → exit per CALL-2 rules.
  4. Replay the request exactly once with the new bearer.
  5. If the replay also returns 401 → stderr `apl call: 401 after refresh (auth error)`, exit 2. Body still streams per CALL-7.
- No retry is attempted for any other status (403, 429, 5xx, network errors).

### CALL-9 — Exit code mapping

| Code | Meaning | Trigger |
|---|---|---|
| 0 | Success | HTTP 2xx |
| 1 | User error | Bad flags, malformed handle, malformed header, `--body` + `--body-file`, bad `<METHOD>`, non-absolute URL, Authorization override attempt. Also HTTP 4xx **other than 401 and 429** (bad request the caller built). |
| 2 | Auth error | No record, missing scope, refresh failure, 401 after single retry |
| 3 | Network error | DNS, TLS, connection refused, context timeout, malformed response |
| 4 | Throttled | HTTP 429 (distinct so agents can implement their own backoff) |
| 2 | Server error | HTTP 5xx |

Clarification: 4xx responses from the server are mapped to exit 1 (caller built a bad request), except 401 (exit 2) and 429 (exit 4). 5xx is mapped to exit 2 — wait, that collides with auth. **Fix:** 5xx maps to a dedicated **exit 5** to keep auth failures (2) distinct from server failures. Updated table:

| Code | Meaning |
|---|---|
| 0 | HTTP 2xx |
| 1 | User error / client-built 4xx (not 401, not 429) |
| 2 | Auth error (no record, missing scope, refresh failed, 401 after retry) |
| 3 | Network / transport error |
| 4 | HTTP 429 |
| 5 | HTTP 5xx |

This extends `spec-cli-command-surface.md`'s exit code table with 4 and 5, scoped to `apl call` only. Other subcommands continue to use 0/1/2/3.

### CALL-10 — Status and selected headers to stderr on TTY

- After the (possibly retried) final response, if **stderr is a TTY**, print to stderr:
  - One status line: `HTTP/<v> <code> <reason>`
  - `Content-Type: <value>` if set
  - `WWW-Authenticate: <value>` if set (helps debug 401s)
- If stderr is **not** a TTY (piped, redirected, or running inside a non-interactive shell), **print nothing to stderr** on the success path. Agents and scripts get clean output.
- Error messages (CALL-14) are printed to stderr regardless of TTY.

### CALL-11 — `--status-only` mode

- `--status-only` discards the response body entirely (Close, don't buffer).
- Status line is always printed to stderr in this mode, TTY or not. (That's the only signal the caller gets; suppressing it would defeat the flag.)
- Stdout is empty.
- Exit code is mapped per CALL-9.

### CALL-12 — `--timeout <dur>` flag

- Go `time.Duration` syntax (`30s`, `2m`, `500ms`).
- Default: `60s`.
- Applied via `context.WithTimeout` covering both refresh and HTTP call.
- On timeout → stderr `apl call: timeout after <dur> (network error)`, exit 3.

### CALL-13 — `--scope` guard

- Covered by CALL-1, restated here: listed scopes must all be present in `rec.Scopes`. Missing → exit 2, no network call.
- Rationale: a token may be valid (refreshable) but not authorised for the resource the agent wants to hit. Failing fast before the request saves a round-trip and gives a specific recovery command.

### CALL-14 — Error message format

- Every `apl call`-originated error line uses the shape:
  `apl call: <short reason> (<exit code name>)`
- Exit code names: `user error`, `auth error`, `network error`, `throttled`, `server error`.
- Provider-side 4xx/5xx error bodies are **not** reformatted or wrapped — they go to stdout (or `-o`) unchanged. The one-liner is only for `apl`-originated failures.
- When a provider returns an error with a `WWW-Authenticate` header, that header is surfaced per CALL-10 so the caller can see the realm/error hint.

### CALL-15 — Method validation

- Accepted methods: `GET`, `POST`, `PUT`, `PATCH`, `DELETE`. Case-insensitive on input; normalised to uppercase before use.
- Any other value → stderr `apl call: unsupported method "X" (user error)`, exit 1.
- `HEAD` and `OPTIONS` are intentionally omitted in v1 — no concrete consumer needs them. Revisit when one does.

### CALL-16 — URL validation

- `<url>` must parse via `net/url.Parse` and have a non-empty scheme (`http` or `https`) and a non-empty host.
- `http://` is allowed (loopback tooling, localhost APIs) but no TLS downgrade warning is printed. Caller's problem.
- Non-absolute URL → stderr `apl call: url must be absolute (user error)`, exit 1.

---

## Design

### Command layout

`apl call` lives in `internal/cli/call.go`, mirroring `internal/cli/login.go`. Tests in `internal/cli/call_test.go` use a `httptest.Server` to drive the full path including the 401-refresh-retry bounce.

### Go skeleton

```go
// internal/cli/call.go

package cli

import (
    "context"
    "fmt"
    "io"
    "net/http"
    "os"
    "strings"
    "time"

    "github.com/spf13/cobra"

    "apl/internal/handle"
    "apl/internal/provider"
    "apl/internal/store"
)

type callOpts struct {
    body        string
    bodyFile    string
    headers     []string
    output      string
    contentType string
    statusOnly  bool
    scopes      []string
    timeout     time.Duration
}

func CallCmd(reg *provider.Registry, st store.Store, stdout, stderr io.Writer) *cobra.Command {
    var o callOpts

    cmd := &cobra.Command{
        Use:   "call <handle> <METHOD> <url>",
        Short: "Issue an authenticated HTTP request via a stored handle",
        Args:  cobra.ExactArgs(3),
        RunE: func(cmd *cobra.Command, args []string) error {
            // 1. Parse handle.
            h, err := handle.Parse(args[0])
            if err != nil { return userErr(stderr, "invalid handle %q", args[0]) }

            // 2. Normalise and validate method.
            method := strings.ToUpper(args[1])
            if !isAllowedMethod(method) { return userErr(stderr, "unsupported method %q", args[1]) }

            // 3. Validate URL.
            if err := validateAbsURL(args[2]); err != nil { return userErr(stderr, "url must be absolute") }

            // 4. Resolve provider.
            p, ok := reg.Get(h.Provider)
            if !ok { return userErr(stderr, "unknown provider %q", h.Provider) }

            // 5. Load record.
            rec, err := st.Get(h)
            if err != nil { return authErr(stderr, "no account for %s. Run: apl login %s", h, h) }

            // 6. Scope guard (CALL-13).
            if missing := missingScopes(rec.Scopes, o.scopes); len(missing) > 0 {
                return authErr(stderr, "missing scope(s): %s", strings.Join(missing, ","))
            }

            // 7. Context + timeout.
            ctx, cancel := context.WithTimeout(cmd.Context(), o.timeout)
            defer cancel()

            // 8. Refresh if needed (CALL-2).
            if err := p.Refresh(ctx, &rec, false); err != nil { return mapRefreshErr(stderr, err, h) }
            _ = st.Put(rec)

            // 9. Build + send.
            resp, err := doWithRetry(ctx, p, st, &rec, method, args[2], &o)
            if err != nil { return netErr(stderr, "%v", err) }
            defer resp.Body.Close()

            // 10. Diagnostics to stderr if TTY (CALL-10 / CALL-11).
            if o.statusOnly || isTTY(stderr) {
                printStatus(stderr, resp)
            }

            // 11. Stream body (CALL-7 / CALL-11).
            if !o.statusOnly {
                if err := streamBody(resp.Body, stdout, o.output); err != nil {
                    return netErr(stderr, "%v", err)
                }
            } else {
                io.Copy(io.Discard, resp.Body)
            }

            // 12. Map status to exit code (CALL-9).
            return statusToExit(resp.StatusCode)
        },
    }

    cmd.Flags().StringVar(&o.body, "body", "", "request body string")
    cmd.Flags().StringVar(&o.bodyFile, "body-file", "", "request body from file ('-' for stdin)")
    cmd.Flags().StringArrayVarP(&o.headers, "header", "H", nil, "extra header 'Key: Value' (repeatable)")
    cmd.Flags().StringVarP(&o.output, "output", "o", "", "write body to file instead of stdout")
    cmd.Flags().StringVar(&o.contentType, "content-type", "", "override Content-Type")
    cmd.Flags().BoolVar(&o.statusOnly, "status-only", false, "discard body; print status to stderr")
    cmd.Flags().StringArrayVar(&o.scopes, "scope", nil, "required scope (repeatable)")
    cmd.Flags().DurationVar(&o.timeout, "timeout", 60*time.Second, "overall request timeout")

    return cmd
}

// doWithRetry issues the request once; on 401 refreshes and retries exactly once (CALL-8).
func doWithRetry(
    ctx context.Context,
    p provider.Provider,
    st store.Store,
    rec *store.TokenRecord,
    method, url string,
    o *callOpts,
) (*http.Response, error) {
    req, err := buildRequest(ctx, method, url, rec.AccessToken, o)
    if err != nil { return nil, err }

    client := &http.Client{Timeout: 0} // context owns the deadline
    resp, err := client.Do(req)
    if err != nil { return nil, err }
    if resp.StatusCode != 401 { return resp, nil }

    resp.Body.Close()
    if err := p.Refresh(ctx, rec, true); err != nil { return nil, err }
    _ = st.Put(*rec)

    req2, err := buildRequest(ctx, method, url, rec.AccessToken, o)
    if err != nil { return nil, err }
    return client.Do(req2)
}
```

The skeleton is illustrative. Real code will tighten the error helpers (`userErr`, `authErr`, `netErr`, `mapRefreshErr`) into a single small package-private set that returns a typed error the cobra top-level catches and translates to an exit code via `os.Exit`.

### No new dependencies

- No new package in go.mod. `net/http`, `encoding/json` (only if we parse errors later — we don't in v1), `context`, `io`, `os`.
- No changes to `Store` interface.
- No changes to `Provider` interface — `Refresh(ctx, rec, force bool)` already exists per `spec-token-storage.md`.

---

## Acceptance Criteria

- [ ] CALL-1: `apl call notahandle GET https://x` exits 1; `apl call google:none GET https://x` exits 2 with a `Run: apl login` hint.
- [ ] CALL-1/CALL-13: `apl call google:work GET https://x --scope gmail.send` exits 2 when the stored record lacks `gmail.send`, with no network call issued.
- [ ] CALL-2: record with `ExpiresAt` in the past triggers exactly one refresh before the request; the refreshed record is persisted.
- [ ] CALL-3: outgoing request observed by a test server carries `Authorization: Bearer <rec.AccessToken>`.
- [ ] CALL-3: `apl call ... -H 'Authorization: Bearer x'` exits 1.
- [ ] CALL-4: `--body '{"a":1}' --body-file foo.json` exits 1.
- [ ] CALL-4: `--body-file -` reads stdin as the body.
- [ ] CALL-5: body present, no override → `Content-Type: application/json` on the wire.
- [ ] CALL-5: `--content-type text/plain` overrides the auto default.
- [ ] CALL-5: `--content-type x -H 'Content-Type: y'` exits 1.
- [ ] CALL-6: `-H 'X-A: 1' -H 'X-B: 2'` yields both headers on the wire.
- [ ] CALL-6: `-H 'bad'` exits 1.
- [ ] CALL-7: default → body on stdout, no trailing newline injection; `-o out.bin` → stdout empty, file has bytes.
- [ ] CALL-8: test server returns 401 then 200 → `apl call` exits 0; refresh was called exactly once with `force=true`; second request carries the new bearer.
- [ ] CALL-8: test server returns 401 twice → exit 2 with `401 after refresh`.
- [ ] CALL-9: 200→0, 400→1, 401→2 (after retry), 404→1, 429→4, 500→5, network drop→3.
- [ ] CALL-10: stderr is a pipe → no stderr output on 200; stderr is a TTY → status line + `Content-Type` appear.
- [ ] CALL-11: `--status-only` on 200 → stdout empty, stderr has status line, exit 0.
- [ ] CALL-12: `--timeout 10ms` against a slow endpoint → exit 3 with timeout message.
- [ ] CALL-14: every `apl`-originated error line matches `^apl call: .+ \((user error|auth error|network error|throttled|server error)\)$`.
- [ ] CALL-15: `apl call google:work FOO https://x` exits 1.
- [ ] CALL-16: `apl call google:work GET /relative` exits 1.

---

## Implementation Notes

- Files: `internal/cli/call.go`, `internal/cli/call_test.go`. Pattern after `internal/cli/login.go`.
- No changes to `store.Store`; `Get` and `Put` suffice.
- No changes to `provider.Provider`; `Refresh(ctx, rec, force)` already exists.
- No new OAuth helpers — this is pure `net/http`.
- Register the command in `internal/cli/root.go` alongside `LoginCmd`, `LogoutCmd`, etc.
- Exit codes: extend `internal/cli/exit.go` with `ExitThrottled = 4` and `ExitServer = 5`. These are `apl call`-only; other commands keep using 0-3.
- TTY detection: `golang.org/x/term.IsTerminal(int(os.Stderr.Fd()))`. If that dependency isn't already in go.mod, use `(os.Stderr.Stat()).Mode() & os.ModeCharDevice != 0` as a no-dep fallback.
- `http.Client.Timeout` is left at zero because the `context.WithTimeout` wrapping the request owns cancellation; mixing both causes confusing error wrapping.
- `buildRequest` must be called twice (for the retry path), so it cannot consume an `io.Reader` body — materialise `--body-file` into a `[]byte` at startup for files; for `--body-file -` (stdin), read once into memory. For typical API calls the body is small; if it ever matters we can switch to `http.Request.GetBody` later.
- Keep help text under 15 lines (per CLI-12 conventions).

---

## Verification

Assume `google:work` has a valid record with scopes `openid email profile gmail.modify`.

```bash
# CALL-1: bad handle
apl call notahandle GET https://example.com ; echo "exit=$?"   # expect 1

# CALL-1: unknown account
apl call google:nobody GET https://example.com ; echo "exit=$?" # expect 2

# CALL-2: force a refresh by seeding an expired record, then call.
#   (assumes a debug helper; alternatively mutate the on-disk JSON directly)
apl-debug expire google:work
apl call google:work GET https://gmail.googleapis.com/gmail/v1/users/me/profile | jq .emailAddress
# expect 200 + email; APL_DEBUG=1 logs show "refresh: force=false -> rotated"

# CALL-3: bearer injection (observe with a local server)
( while true; do { echo -e 'HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n'; } | nc -l 8787; done ) &
apl call google:work GET http://localhost:8787/  # check nc output shows Authorization: Bearer ...

# CALL-4: body flags mutual exclusion
apl call google:work POST https://example.com --body '{}' --body-file x.json ; echo "exit=$?" # expect 1

# CALL-5: auto content-type
apl call google:work POST http://localhost:8787/ --body '{"a":1}'
# expect Content-Type: application/json observed at the server

# CALL-6: custom headers
apl call google:work GET http://localhost:8787/ -H 'X-A: 1' -H 'X-B: 2'
# expect both on the wire

# CALL-7: output to file
apl call google:work GET https://www.gstatic.com/generate_204 -o /tmp/x.bin
test ! -s /tmp/x.bin && echo "empty body captured, as expected for 204"

# CALL-8: 401 bounce (requires a test server; sketch)
#   server returns 401 first hit, 200 after. Confirm exit=0 and exactly one refresh.

# CALL-9: status to exit code
apl call google:work GET https://gmail.googleapis.com/gmail/v1/users/me/bogus ; echo "exit=$?" # 404 -> 1
apl call google:work GET https://httpstat.us/429              ; echo "exit=$?"                 # 429 -> 4
apl call google:work GET https://httpstat.us/500              ; echo "exit=$?"                 # 500 -> 5

# CALL-10: TTY vs piped stderr
apl call google:work GET https://... 2>&1 >/dev/null | wc -c   # piped stderr, should be 0 on 2xx
apl call google:work GET https://...                           # TTY: status line visible

# CALL-11: status-only
apl call google:work HEAD-equivalent --status-only GET https://gmail.googleapis.com/gmail/v1/users/me/profile

# CALL-12: timeout
apl call google:work GET https://httpstat.us/200?sleep=5000 --timeout 200ms ; echo "exit=$?" # 3

# CALL-13: scope guard
apl call google:work GET https://... --scope calendar.events  ; echo "exit=$?" # 2 if not granted
```

Automated coverage: `internal/cli/call_test.go` spins up an `httptest.Server` that can be scripted to return 401→200, 401→401, 429, 5xx, and slow responses; it asserts exit code, refresh invocation count, and captured request headers for each acceptance bullet.
