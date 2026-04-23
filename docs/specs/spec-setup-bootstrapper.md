# Spec: `apl setup` Bootstrapper — OAuth Client Provisioning

**Status:** Proposed
**Priority:** High
**Depends on:** none (foundational)
**Referenced by:** `spec-cli-command-surface.md`, `spec-oauth-pkce-loopback.md` (consumes the stored client IDs)

---

## Problem

`apl` is a BYO-client OAuth token broker. The user must supply their own Google and Microsoft OAuth client IDs before `apl login` can do anything. Without tooling, this means reading PRD §8, hand-walking the Google Cloud Console and the Azure Portal, copy-pasting client IDs into a YAML file, and hoping the scopes line up with what `apl token` will later ask for. That is a cliff — especially for Google, which requires manual steps that no CLI can automate.

The huddle decisions (2026-04-23) settled that:

- We do **not** embed Deemwar client IDs. Every user brings their own.
- Microsoft provisioning **can** be fully automated via `az`.
- Google provisioning is **partially** automated via `gcloud`, with a short guided console walkthrough for the two steps Google refuses to expose programmatically (consent screen config, OAuth 2.0 Client ID creation of type "Desktop app").
- Both flows should reuse existing `apl-*` projects / app registrations when they exist.
- Running `apl setup` twice on an already-configured machine must be a no-op, not a re-prompt loop.

This spec defines the bootstrapper command that makes BYO-client realistic for casual users.

---

## Goals

- First-time user on a clean machine runs `apl setup` and ends with a valid `~/.config/apl/config.yaml` containing Google + Microsoft client IDs, ready for `apl login`.
- Zero manual editing of `config.yaml`. Anything the user must type goes through prompts.
- Re-running `apl setup` on an already-configured machine is a green-check no-op (idempotent).
- Microsoft flow is 100 % non-interactive after the provider/scope confirmation.
- Google flow keeps manual console steps to the minimum Google forces on us (exactly three clicks away from fully-automated), with exact URLs, field values, and a paste-back prompt.
- Partial failure never leaves a half-written config file.

## Non-Goals

- OAuth runtime (PKCE loopback, token exchange, refresh). Covered by `spec-oauth-pkce-loopback.md`.
- Token storage internals (keychain, age-encrypted file fallback). Covered by `spec-token-storage.md`.
- Adding providers beyond Google and Microsoft.
- Shipping a TUI / curses UI. `apl setup` is line-oriented, stdin/stdout only.
- Automating Google OAuth 2.0 Client ID creation. Google blocks this at the provider level — not a bug we can work around.
- Managing the user's billing / org policies. If `gcloud projects create` is refused by org policy, we surface the provider error and exit.

---

## Functional Requirements

### SETUP-1 — Command surface

```
apl setup                    # interactive bootstrapper, both providers (default: google + microsoft)
apl setup google             # google only
apl setup ms                 # microsoft only (alias: microsoft)
apl setup --reconfigure      # force re-prompting even if already configured
apl setup --reset            # wipe config.yaml then start fresh
apl setup --dry-run          # print what would happen; touch nothing
```

- `apl setup` with no sub-argument selects both providers. User may deselect at the first prompt.
- `--reconfigure` and `--reset` are mutually exclusive with each other but may combine with a provider filter (e.g. `apl setup google --reconfigure`).
- `--dry-run` skips all `az`/`gcloud` mutations and all config writes; preflight checks still run.

### SETUP-2 — Preflight per provider

Before any mutation, for each selected provider verify:

1. **CLI present on `PATH`.** `command -v az` / `command -v gcloud`. If missing, print:

   ```
   ✗ Microsoft setup needs the Azure CLI.
       Install: https://learn.microsoft.com/cli/azure/install-azure-cli
       Or:      brew install azure-cli
   ```

   Exit code `2` (missing dependency).

2. **CLI logged in.** `az account show` / `gcloud auth list --filter=status:ACTIVE --format="value(account)"`. If the CLI is present but no active account:

   ```
   ✗ Azure CLI is installed but not logged in.
       Run: az login
   ```

   Exit code `2`.

3. Both checks must pass for **all** selected providers before any provisioning starts. A partial success ("MS ready, Google not") still blocks the MS flow until the user fixes Google or re-runs with `apl setup ms`.

### SETUP-3 — Provider selection prompt

When invoked as `apl setup` (no sub-argument), print:

```
apl setup — configure OAuth clients for token brokerage

Select providers to configure:
  [x] Google      (Gmail, Calendar, People)
  [x] Microsoft   (Mail, Calendar, Teams, Graph)

Press ENTER to accept, or toggle with: g / m
>
```

Empty ENTER accepts both. Typing `g` or `m` toggles the respective box. Any other input re-prompts. If both are deselected, exit `0` with "nothing to do".

### SETUP-4 — Microsoft flow (fully automated)

Sequence, each step announced with a prefix line (`→`) before running:

1. **List existing candidates.**

   ```bash
   az ad app list --filter "startswith(displayName, 'apl-')" \
                  --query "[].{name:displayName, id:appId}" -o json
   ```

   If one or more results:

   ```
   Existing apl app registrations in this tenant:
     1) apl-muthu-macbook     (appId: 11111111-2222-3333-4444-555555555555)
     2) apl-ci-bot            (appId: aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee)
     3) Create a new one
   Choose [1]:
   ```

   Default = `1` (reuse first match). Selecting "create new" continues at step 2.

2. **Create app registration.** Default name `apl-<whoami>-<8-char-hex>`:

   ```bash
   az ad app create \
     --display-name "apl-muthu-3f9a1c2d" \
     --sign-in-audience "AzureADandPersonalMicrosoftAccount" \
     --public-client-redirect-uris "http://localhost" \
     --is-fallback-public-client true
   ```

   Capture `appId` from output.

3. **Grant delegated Graph permissions.** Microsoft Graph resource ID is well-known: `00000003-0000-0000-c000-000000000000`.

   We do **not** hardcode permission GUIDs. Before the permission-add loop, fetch the Graph service principal once and build a name→GUID map at runtime:

   ```bash
   az ad sp show --id 00000003-0000-0000-c000-000000000000 \
       --query "oauth2PermissionScopes[].{name:value, id:id, type:type}" -o json
   ```

   Default delegated scope set (granted unconditionally):

   - `User.Read`
   - `offline_access`
   - `openid`
   - `email`
   - `profile`
   - `Mail.ReadWrite`
   - `Mail.Send`
   - `Calendars.ReadWrite`
   - `Chat.ReadWrite`
   - `ChatMessage.Send`
   - `OnlineMeetings.Read`

   For each scope NAME, look up its GUID in the map and run:

   ```bash
   az ad app permission add \
     --id <appId> \
     --api 00000003-0000-0000-c000-000000000000 \
     --api-permissions <GUID>=Scope
   ```

   No admin consent is requested for these — delegated scopes consent at login time per user.

3a. **Opt-in admin-consent scope.** Prompt the user:

   ```
   Include OnlineMeetingRecording.Read.All? (requires tenant admin consent) [y/N]:
   ```

   If yes, look up `OnlineMeetingRecording.Read.All` first in the `oauth2PermissionScopes` map, then in the service principal's `appRoles` array (it may be delegated or application-only depending on Microsoft's current publishing). Grant using the matching `--api-permissions <GUID>=Scope` or `=Role` accordingly. After granting, print to stderr:

   ```
   note: OnlineMeetingRecording.Read.All requires tenant admin consent. As tenant admin, run:
       az ad app permission admin-consent --id <appId>
   ```

4. **Write to config.** Append or update the `microsoft:` block in the config file (see SETUP-7).

5. **Confirm.**

   ```
   ✓ Microsoft configured
       appId: 11111111-2222-3333-4444-555555555555
       tenant: common
       scopes: 11 Graph permissions
   ```

### SETUP-5 — Google flow (partial automation)

1. **List existing projects.**

   ```bash
   gcloud projects list --filter="projectId:apl-*" \
          --format="value(projectId,name)" --sort-by=projectId
   ```

   If one or more:

   ```
   Existing apl-* GCP projects:
     1) apl-muthu-macbook    (All Purpose Login — muthu's mbp)
     2) Create a new project
   Choose [1]:
   ```

2. **Create project** (if "new" chosen). Default ID `apl-<whoami>-<6-char-hex>`:

   ```bash
   gcloud projects create apl-muthu-3f9a1c --name "All Purpose Login (muthu)"
   gcloud config set project apl-muthu-3f9a1c
   ```

   If the create fails (quota, org policy, reserved ID), print the raw gcloud stderr verbatim inside a framed block and exit `3` (provider failure). Do not retry silently.

3. **Enable APIs.**

   ```bash
   gcloud services enable gmail.googleapis.com \
                          calendar-json.googleapis.com \
                          people.googleapis.com \
                          --project apl-muthu-3f9a1c
   ```

4. **Guided console walkthrough.** This is the only manual segment. Print exactly:

   ```
   ─────────────────────────────────────────────────────────────────
   Step 1 of 3 — Configure the OAuth consent screen
   ─────────────────────────────────────────────────────────────────
   Open this URL in your browser:

     https://console.cloud.google.com/apis/credentials/consent?project=apl-muthu-3f9a1c

   Set the following:
     User Type:         External
     App name:          apl (local)
     User support email: <your email>
     Developer contact:  <your email>
     Scopes:            (leave empty — apl requests at login time)
     Test users:        <your email>   ← add yourself

   Click SAVE AND CONTINUE through each page, then PUBLISH or leave in Testing.

   Press ENTER when done...
   ```

   Wait for ENTER (blocking read on stdin; Ctrl-C aborts cleanly — see SETUP-9).

   ```
   ─────────────────────────────────────────────────────────────────
   Step 2 of 3 — Create OAuth 2.0 Client ID
   ─────────────────────────────────────────────────────────────────
   Open:

     https://console.cloud.google.com/apis/credentials?project=apl-muthu-3f9a1c

   Click: + CREATE CREDENTIALS → OAuth client ID

     Application type:  Desktop app
     Name:              apl-desktop

   Click CREATE. A dialog shows your Client ID and Client secret.
   ─────────────────────────────────────────────────────────────────
   Step 3 of 3 — Paste the Client ID here
   ─────────────────────────────────────────────────────────────────
   Client ID: _
   ```

5. **Validate Client ID format** client-side before accepting:

   - Regex: `^[0-9]+-[a-z0-9]{32}\.apps\.googleusercontent\.com$`
   - On mismatch, re-prompt up to 3 times; on 3rd failure exit `3`.

6. **Live validation — OAuth round-trip.** Do a real PKCE loopback exchange requesting scopes `openid email profile` against the freshly-pasted Client ID. This reuses the loopback implementation from `spec-oauth-pkce-loopback.md`. If the exchange returns a valid ID token whose `email` matches the gcloud active account, accept; otherwise:

   ```
   ✗ That Client ID didn't complete the OAuth handshake.
     Google returned: invalid_client
     Common causes:
       — You copied the client SECRET instead of the ID
       — The consent screen in Step 1 hasn't been saved yet
       — Your email isn't in the Test Users list
     Try again? [Y/n]:
   ```

7. **Write to config** (see SETUP-7).

8. **Confirm.**

   ```
   ✓ Google configured
       project: apl-muthu-3f9a1c
       client: 409786642553-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx.apps.googleusercontent.com
       scopes: gmail.readonly, gmail.send, gmail.modify, calendar, calendar.readonly, profile
   ```

### SETUP-6 — Client-secret confidentiality

Google's "Desktop app" client type returns both a client ID **and** a client secret. Per RFC 8252, the secret is not a secret for public clients, but we do not store it anyway — we only store the ID. If the user pastes a full JSON (downloaded from console), we extract `client_id` and discard the rest.

### SETUP-7 — Config file

**Path:** `${XDG_CONFIG_HOME:-$HOME/.config}/apl/config.yaml`

**Shape:**

```yaml
version: 1
providers:
  google:
    client_id: 409786642553-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx.apps.googleusercontent.com
    project_id: apl-muthu-3f9a1c
    configured_at: 2026-04-23T14:21:09Z
  microsoft:
    client_id: 11111111-2222-3333-4444-555555555555
    tenant: common
    app_display_name: apl-muthu-3f9a1c2d
    configured_at: 2026-04-23T14:22:47Z
```

**Permissions:**

- Directory `~/.config/apl/`: mode `0700`, created with `os.MkdirAll` then `os.Chmod` (Go's MkdirAll respects umask — explicit chmod is required).
- File `config.yaml`: mode `0600`.

**Atomic write:**

1. Serialize full (merged) config to bytes in memory.
2. Write to `config.yaml.tmp.<pid>` in the same directory.
3. `fsync` the temp file.
4. `os.Rename` over the real path.
5. `fsync` the directory.

A provider block is only written once **all** steps in its flow succeed. Partial MS + failed Google leaves `microsoft:` populated and `google:` untouched (or absent). No half-written provider block is ever persisted.

### SETUP-8 — Idempotency

`apl setup` is considered "already configured" for a provider when:

- `providers.<name>.client_id` is present **and**
- the string matches the provider's client-ID format regex

If both selected providers are already configured and `--reconfigure` is not set:

```
✓ Google already configured (apl-muthu-3f9a1c)
✓ Microsoft already configured (apl-muthu-3f9a1c2d)
Nothing to do. Use --reconfigure to re-run, or --reset to start over.
```

Exit `0`. No network calls. No prompts.

If **one** is configured and the other isn't, skip the configured one (green-check) and run the flow for the other.

### SETUP-9 — Flags: `--reconfigure` and `--reset`

- `--reconfigure` forces re-prompting even if detected as configured. Existing values are shown as defaults in prompts (e.g. "Create new project, or reuse `apl-muthu-3f9a1c`? [reuse]"). No Azure/GCP resources are deleted.
- `--reset` deletes `config.yaml` at start (after a `Are you sure? [y/N]` prompt unless stdin is non-TTY and `--yes` is passed), then proceeds as a first-time run. Cloud-side app registrations and projects are **not** deleted — the user can clean those up manually if desired. We print the cleanup commands:

  ```
  Reset complete. Cloud-side resources left in place. To delete them yourself:
    az ad app delete --id 11111111-2222-3333-4444-555555555555
    gcloud projects delete apl-muthu-3f9a1c
  ```

### SETUP-10 — Abort and failure semantics

| Situation                                              | Behaviour                                    | Exit |
|--------------------------------------------------------|----------------------------------------------|------|
| User presses Ctrl-C mid-flow                           | No config write. Clean message "aborted."    | `1`  |
| Missing CLI (`az` / `gcloud`)                          | Print install hint, exit                     | `2`  |
| CLI not logged in                                      | Print login hint, exit                       | `2`  |
| `az ad app create` fails (e.g. `Insufficient privileges`) | Print raw stderr, exit                    | `3`  |
| `gcloud projects create` fails (quota, org policy)     | Print raw stderr, exit                       | `3`  |
| Invalid Client ID format after 3 retries               | Exit                                         | `3`  |
| OAuth round-trip fails after user retry declined       | Exit                                         | `3`  |
| Config write fails (disk full, permission denied)      | Keep temp file for inspection, print path    | `1`  |
| `apl setup` with everything already configured         | Green-check, no prompts                      | `0`  |
| Successful run                                          | `✓` summary                                  | `0`  |

Exit codes match the CLI surface convention: `0` success, `1` user/generic, `2` missing dependency, `3` provider / external-system failure.

---

## Acceptance Criteria

- [ ] `apl setup` on a clean machine with both CLIs logged in writes a valid `~/.config/apl/config.yaml` containing both `google:` and `microsoft:` blocks.
- [ ] `apl setup ms` alone creates an Azure app registration named `apl-<whoami>-<hex>`, grants the 11 delegated Graph permissions listed in SETUP-4 (GUIDs looked up at runtime via `az ad sp show`), and writes the `microsoft:` block.
- [ ] `apl setup ms` prompts for `OnlineMeetingRecording.Read.All`; declining skips it, accepting grants it and prints the admin-consent follow-up command to stderr.
- [ ] No hardcoded Graph permission GUID appears anywhere in the source tree — every GUID is resolved from `az ad sp show` output.
- [ ] `apl setup google` alone creates (or reuses) a GCP project `apl-*`, enables Gmail + Calendar + People APIs, prints the 3-step console walkthrough with real URLs substituted, accepts a pasted Client ID, validates it via an `openid email profile` round-trip, and writes the `google:` block.
- [ ] Re-running `apl setup` after a successful run exits `0` with a green-check message and writes nothing.
- [ ] `apl setup --reconfigure` re-runs prompts even when configured; existing values are offered as defaults.
- [ ] `apl setup --reset` deletes `config.yaml` after confirmation and prints manual cleanup commands for cloud resources.
- [ ] Missing `az` / `gcloud` produces exit `2` and an install hint. Unauthenticated CLI produces exit `2` and a login hint.
- [ ] Ctrl-C mid-Google-walkthrough produces exit `1` and leaves `config.yaml` unchanged (including no half-written `google:` block).
- [ ] A pasted Client ID that fails the regex is rejected without a network call; 3 consecutive bad pastes exit `3`.
- [ ] A pasted but invalid (e.g. revoked) Client ID fails at the OAuth round-trip and triggers the retry prompt.
- [ ] Config file mode is `0600`; directory mode is `0700`; an XDG-non-standard `$XDG_CONFIG_HOME` is honoured.
- [ ] No partial provider block ever appears in `config.yaml`: either the full block is there, or the block is absent.
- [ ] If a user has an existing `apl-*` Azure app registration, the MS flow offers reuse as choice `1` and does not create a duplicate.

---

## Implementation Notes

- Package layout suggestion:
  - `internal/setup/setup.go` — top-level orchestration, provider selection, idempotency check, atomic config write.
  - `internal/setup/google.go` — gcloud shell-outs, console walkthrough strings, paste-back loop.
  - `internal/setup/microsoft.go` — az shell-outs, Graph permission GUID table.
  - `internal/setup/config.go` — load/merge/write YAML with atomic rename + chmod.
- Shell out via `exec.CommandContext` with the parent context bound to a signal handler that catches SIGINT and returns a sentinel `ErrAborted` — this is how Ctrl-C mid-flow exits cleanly at `1`.
- Google OAuth round-trip reuses the PKCE loopback implementation from `spec-oauth-pkce-loopback.md`. Setup does not persist the resulting token — it is immediately discarded after `email` is verified.
- The Graph permission GUIDs are **not** stored in the source tree. At setup time, `az ad sp show --id 00000003-0000-0000-c000-000000000000 --query "oauth2PermissionScopes[].{name:value, id:id, type:type}" -o json` is shelled out once and parsed into a `map[string]string` (name → GUID). The default scope NAME list IS code-resident (a small ordered `[]string`) because changing the default set is a deliberate product decision. For the admin-consent opt-in, both `oauth2PermissionScopes` and `appRoles` are consulted — Microsoft moves scopes between the two.
- All user-facing strings should be in one file (`internal/setup/messages.go`) for future i18n, even though v1 is English-only.
- Pseudocode for the top-level loop:

  ```
  func Run(flags) error {
      cfg := loadConfig()                 // tolerates missing file
      if flags.Reset { confirmAndDelete(cfg); cfg = empty }
      providers := selectProviders(flags) // prompt if setup (no arg)
      for _, p := range providers {
          if cfg.isConfigured(p) && !flags.Reconfigure {
              greenCheck(p); continue
          }
          if err := preflight(p); err != nil { return err }
      }
      // only mutate after all preflights pass
      for _, p := range providers {
          block, err := provision(p, flags)   // MS or Google flow
          if err != nil { return err }        // no write
          cfg.merge(p, block)
          if err := cfg.atomicWrite(); err != nil { return err }
      }
      return nil
  }
  ```

---

## Verification

**Manual — clean macOS machine, both providers:**

1. `rm -rf ~/.config/apl`
2. `az logout; gcloud auth revoke --all`
3. Run `apl setup` → expect exit `2` with "login to az" / "login to gcloud" hints.
4. `az login && gcloud auth login`
5. Run `apl setup` → expect the provider-selection prompt; accept both.
6. Observe Microsoft flow runs non-interactively, prints the 7 Graph permissions, green-checks.
7. Observe Google flow lists projects (or offers create), enables APIs, prints the 3-step walkthrough with a real project-ID-substituted URL.
8. Follow the walkthrough in a browser, paste the Client ID back.
9. Observe OAuth round-trip completes; green-check.
10. `cat ~/.config/apl/config.yaml` → both blocks present, mode `0600`.
11. `stat -f %Mp%Lp ~/.config/apl/config.yaml` → `0600`. `stat -f %Mp%Lp ~/.config/apl` → `0700`.
12. Run `apl setup` again → expect green-checks for both, no prompts, exit `0`.

**Manual — fail-and-retry (Google):**

1. At the "Client ID:" prompt, paste the **client secret** instead of the ID.
2. Expect regex rejection without a network call. Re-prompt.
3. Paste a well-formed but non-existent Client ID (`000000000000-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.apps.googleusercontent.com`).
4. Expect regex pass, OAuth round-trip failure, "Try again? [Y/n]" prompt.
5. Answer `n` → expect exit `3`, no `google:` block written, `microsoft:` block (if it ran first) still present.

**Manual — `--reset`:**

1. `apl setup --reset` on a configured machine → confirmation prompt → after `y`, `config.yaml` is deleted and cleanup commands are printed.
2. `ls ~/.config/apl/` → empty or only the dir.

**Manual — `--reconfigure`:**

1. `apl setup google --reconfigure` on a configured machine → expect the project-selection prompt to default to the existing project, walkthrough re-runs, existing `google:` block is overwritten atomically (no window where it's missing mid-write).

**Automated (unit):**

- Atomic-write test: simulate a failing write partway, assert `config.yaml` is unchanged.
- Idempotency test: pre-seed a valid config, run `Run()` with flags = empty, assert zero shell-outs.
- Permission-table test: stub `az ad sp show` with a canned `oauth2PermissionScopes` array; assert the permission-add loop runs `az ad app permission add` once per default-scope NAME with the GUID taken from the stubbed response (not from any hardcoded constant).
- Regex test: Client ID validator accepts a real Google ID, rejects a secret, rejects UUID, rejects empty.
