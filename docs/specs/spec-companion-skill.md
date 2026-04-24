# Spec: `all-purpose-data-skill` — Claude Code Companion Skill

**Status:** Proposed
**Priority:** High
**Depends on:** `spec-cli-command-surface.md`, `spec-apl-call.md`, `spec-npm-distribution.md`, `spec-recipes.md`
**Repo (to be created):** `github.com/muthuishere-agent-skills/all-purpose-data-skill`

---

## Problem

`apl` solves authentication: it brokers OAuth tokens for Google and Microsoft and — via `apl call` — relays authenticated HTTP requests to Gmail, Calendar, Graph, Drive, OneDrive, SharePoint, Teams, and People APIs. What it does *not* do is know what to do.

When a user types "send an email to x@y.com saying hi" into Claude Code, the agent needs to know:

1. That this is a mail intent, not a calendar or file intent.
2. Which handle (`google:work`, `google:personal`, `ms:volentis`, …) to operate against.
3. Which endpoint + method + body to hit.
4. How to render the response back to the user.
5. What to do when a provider wall (tenant policy, missing scope, SharePoint ACL on a Teams recording) blocks the call.

That routing + recipe knowledge is the job of `all-purpose-data-skill` — a Claude Code agent skill that sits on top of `apl` and turns natural-language productivity requests into `apl call …` invocations.

The skill is the *agent-facing front door*. `apl` is the auth engine. Users install both; they cooperate but ship independently.

---

## Goals

- A single installable Claude Code skill that handles the full Google Workspace + Microsoft 365 productivity surface (mail, calendar, meetings, chat, files, contacts).
- Natural-language triggers cover the common phrases a user would actually say.
- Every recipe is a copy-pasteable `apl call <handle> <METHOD> <url>` line, 1:1 with `spec-recipes.md`.
- Handle selection happens once per session via `apl accounts --json`, cached in conversation context.
- Setup flows point users at `apl setup google` / `apl setup ms` — the skill never touches credentials.
- Platform walls (e.g. Teams recording mp4 when the user isn't the organizer) are surfaced honestly, with the documented workaround, rather than faked.
- Installs cleanly via a standard `install.sh` that symlinks into `~/.claude/skills/`.

---

## Non-Goals

- The skill does not implement any OAuth. All auth is delegated to `apl`.
- The skill does not bundle `apl`. The user installs the npm package separately.
- The skill has no state of its own. Active handle + any other context lives in the Claude Code conversation only.
- No auto-update mechanism. Users re-run the install curl to upgrade.
- No language-specific client code generation (no Python SDK, no TS wrapper).
- The skill does not wrap `apl setup` — it points at the command and lets `apl` drive the browser.
- Not a general HTTP client. It only routes intents that map to the productivity recipes documented in `spec-recipes.md`.

---

## Design

### Skill identity (`SKILL.md` frontmatter)

```yaml
---
name: all-purpose-data-skill
description: >
  Email, calendar, meetings, chat, and files across Google Workspace + Microsoft 365.
  Read mail, send mail, list/create calendar events, look up online meetings,
  pull transcripts + recordings, send Teams messages, search Drive / OneDrive
  / SharePoint, export Google Docs. Uses apl (`npm install -g @muthuishere/apl`)
  as the token broker. Trigger on: "send email", "check my inbox", "today's
  meetings", "meeting recording", "search my drive", "send teams message",
  "find in my gmail", "list calendar events", etc.
---
```

### Repository layout

```
all-purpose-data-skill/
├── LICENSE
├── README.md              # human-facing: partnership with apl, install, quickstart
├── SKILL.md               # agent manifest: frontmatter + routing rules
├── install.sh             # symlink into ~/.claude/skills (honors CLAUDE_SKILLS_DIR)
├── uninstall.sh           # remove symlink
└── references/
    ├── workflow.md         # top-level routing — how the agent decides what to run
    ├── handle-selection.md # picking google:<label> / ms:<label> from `apl accounts --json`
    ├── setup-google.md     # points at `apl setup google` + describes what the user will see
    ├── setup-microsoft.md  # points at `apl setup ms` + describes what the user will see
    ├── mail.md             # read / send / search / reply / attachments (Gmail + Graph Mail)
    ├── calendar.md         # today / week / create / RSVP / free-busy (Google + Graph)
    ├── teams-chat.md       # 1:1, group, meeting chats; send / reply / react / edit
    ├── online-meetings.md  # meeting lookup + transcripts + recordings (+ SharePoint ACL wall)
    ├── drive.md            # Drive + OneDrive + SharePoint search / list / download / export
    ├── contacts.md         # Google People + Microsoft Graph contacts / users / directory
    └── delta-and-batch.md  # delta syncs + Graph $batch + Gmail history
```

### How the skill thinks (decision tree)

When a user sends a productivity request, the agent follows this mental path:

1. **Preflight.** Is `apl` installed? (`which apl` / `apl version` exits 0.) If not, surface `npm install -g @muthuishere/apl` and stop.
2. **Active handle?** If conversation context has no active handle, load `references/handle-selection.md`, call `apl accounts --json`, and ask the user to choose. If there are zero accounts, route to `references/setup-google.md` or `references/setup-microsoft.md` based on what the user asked for.
3. **Classify intent.** Map the phrase to a recipe family:
   - mail / inbox / gmail → `references/mail.md`
   - meeting / calendar / today's schedule → `references/calendar.md`
   - transcript / recording / online meeting → `references/online-meetings.md`
   - teams message / channel / chat → `references/teams-chat.md`
   - drive / onedrive / sharepoint / file / document / pdf → `references/drive.md`
   - contact / people directory → `references/contacts.md`
   - sync / changes since / batch → `references/delta-and-batch.md`
4. **Provider compatibility check.** If the intent is Google-only (e.g. "export this Google Doc as pdf") and the active handle is `ms:*` (or vice versa for Microsoft-only intents like Teams / OnlineMeetings): scan `apl accounts --json` for compatible handles. If exactly one compatible handle exists → silently auto-switch for this request (tell the user briefly what you did). If zero or multiple → ask.
5. **Execute recipe.** Read the matching subsection in the family MD, run the documented `apl call …` command, capture JSON stdout.
6. **Render.** Format the response for the user per the recipe's "user-visible formatting" block.
7. **On failure**, apply the rules in "Failure handling" below.

### Recipe MD structure (every `references/<family>.md` follows this layout)

```
# <Family> — recipes

> Generated from apl/docs/specs/spec-recipes.md §<Family> at commit <sha>.

## <Intent group, e.g. "Send mail">

### <Intent, e.g. "Send a plain-text email">
Trigger phrases: "send email", "email X saying …", "reply to the last mail from Y"

Command:
    apl call <handle> POST https://.../…  --body-file -     # stdin
    # or --body '<inline json>' for short payloads

Expected response shape:
    { "id": "...", "threadId": "...", "labelIds": [...] }

Common errors:
  - 403 insufficient scope → user needs `gmail.send`; re-run `apl login <handle> --scope gmail.send --force`
  - 400 invalid raw → body must be base64url RFC-2822

User-visible formatting:
    "Sent. Message id: <id>."
```

Every intent block is complete enough to copy-paste.

### Workflow rules (`SKILL.md` body)

- **Ask for the handle once per session.** On first productivity tool call in a session, run `apl accounts --json`, present the options, and cache the user's pick in conversation context.
- **Let the user switch.** Phrases like "use my work account", "switch to personal gmail", "change to volentis" → re-run the picker and swap the active handle for the rest of the session.
- **Never guess when ambiguous.** If two handles match (two Google accounts, two tenants), always ask.
- **Setup if empty.** If `apl accounts --json` returns `[]`, route to the right setup reference and tell the user the exact `apl setup …` command. Do not proceed until they come back.
- **First rule: use `apl call`.** Token handling is entirely inside `apl`. Only drop to `TOKEN=$(apl login <handle>) && curl …` when `apl call` cannot express the response (streaming bodies, redirect capture, very large binaries, SSE). Document that exception in the recipe.
- **Never print the token.** Not in responses, not in logs. Any `apl login` output is piped into the consuming command, never echoed.
- **Never ask for credentials.** The skill never prompts for a client id, secret, app password, or pasted token. Everything credential-bearing routes through `apl setup <provider>` or `apl login <handle>`.
- **Surface walls honestly.** When a 403 is a platform policy (e.g. a Teams meeting mp4 when the caller isn't the organizer, or a SharePoint ACL), say so, cite the recipe's documented workaround, and stop — do not pretend to retry or fake content.

### Session context shape

The active selection is held in conversation context only:

```
active_handle: google:work      # or ms:volentis, etc.
active_provider: google         # google | ms
```

If the user kills the session and starts a new one, the skill asks again.

### Failure handling

| Class | Trigger | Action |
|---|---|---|
| 3xx | Graph / Google follow-up redirect for large media | Fall back to `TOKEN=$(apl login …)` + `curl -L` and note it in the recipe |
| 401 | Access token rejected | `apl call` itself auto-refreshes once via `apl login`; if still 401 surface `apl login <handle> --force` |
| 403 insufficient scope | Provider error mentions scope | Tell user which scope + `apl login <handle> --scope <scope> --force` |
| 403 tenant policy | e.g. Teams recording, DLP | Surface the wall + the documented user-action workaround from the recipe |
| 404 | Target doesn't exist | Return provider message verbatim; suggest the listing recipe to re-discover ids |
| 429 | Rate limit | Honor `Retry-After`; back off once; if second call also 429 surface to user |
| 5xx | Provider outage | Retry once with small jitter; if still failing, report and stop |

---

## Functional Requirements

### SKILL-1 — Trigger coverage

`SKILL.md`'s `description` frontmatter lists **at least 40** natural-language trigger phrases across the six recipe families. Minimum coverage per family:

- mail (≥10): "send email", "send an email to X", "email X saying …", "check my inbox", "what's in my gmail", "find emails from X", "search my mail for …", "reply to X", "draft a mail to …", "show unread emails"
- calendar (≥8): "today's meetings", "what's on my calendar", "list events this week", "create a meeting with X", "schedule a call for …", "free/busy for X", "my next meeting", "decline this event"
- online meetings (≥6): "meeting recording", "transcript of last week's team meeting", "find the recording of …", "get the transcript", "who attended …", "meeting chat history"
- teams chat (≥6): "send teams message", "dm X on teams", "post in the channel", "reply in teams", "react to the last message", "find teams message about …"
- drive / files (≥8): "search my drive", "find in my drive", "list files in …", "download that pdf from drive", "export this google doc", "what's in my onedrive", "search sharepoint", "get that file from drive"
- contacts (≥4): "look up X in my contacts", "find X in directory", "search people", "who is X"

### SKILL-2 — Pre-flight check

On first invocation in a session, the skill verifies `apl version` returns exit 0. If `apl` is not on `PATH`, the first message to the user is:

> `apl` is not installed. Install it with: `npm install -g @muthuishere/apl`, then try again.

No further calls are attempted until `apl` is available.

### SKILL-3 — Handle picker

- On first intent in a session, the skill runs `apl accounts --json`.
- If the array is non-empty, it renders the handles grouped by provider and asks the user which to use.
- The chosen handle is cached in conversation context for the remainder of the session.
- If the user later says "switch account" / "use X instead" / "change to my personal gmail", the picker re-runs and the active handle is replaced.
- If `apl accounts --json` returns `[]`, the skill loads the appropriate `setup-*.md` reference and points the user at `apl setup <provider>`.

### SKILL-4 — Recipe-family references

For each family in {mail, calendar, teams-chat, online-meetings, drive, contacts, delta-and-batch}, there is a corresponding `references/<family>.md` file. Every intent in `spec-recipes.md` under that family appears as its own subsection, containing:

- trigger phrases that route here
- the exact `apl call <handle> <METHOD> <url>` command (with body example when applicable)
- expected response shape (brief JSON)
- common errors + the recovery `apl …` command
- user-visible formatting template

Recipe counts must match `spec-recipes.md` 1:1. A top-line banner in each file names the spec commit it was generated from.

### SKILL-5 — Setup references

`references/setup-google.md` and `references/setup-microsoft.md` each:

- State the single entry-point command (`apl setup google` / `apl setup ms`).
- Describe the interactive picker flow the user will see (step 1 → pick project / tenant, step 2 → consent in browser, step 3 → label prompt).
- List the default scope set `apl` requests (per `spec-cli-command-surface.md` CLI-5).
- Explain how to add a second account with a different label (`apl login google:personal --force`).
- Do NOT walk the user through Google Cloud Console or Azure Portal steps — that belongs in `spec-setup-bootstrapper.md` and is reached through the `apl setup` wizard itself.

### SKILL-6 — `install.sh`

- Determines the skills directory: `${CLAUDE_SKILLS_DIR:-$HOME/.claude/skills}`.
- Creates it if missing.
- Symlinks the skill directory into `<skills_dir>/all-purpose-data-skill` (removes an existing symlink first; refuses to clobber a real directory).
- **Also** symlinks into `$HOME/.agents/skills/all-purpose-data-skill` if `codex` is on PATH (matches `storage-workflow/install.sh` for Codex parity).
- Asserts `apl` is on `PATH` via `command -v apl`; if missing, prints a warning with `npm install -g @muthuishere/apl` and continues (the skill still installs; it just won't work until `apl` is present).
- Exits 0 on success, 1 on a refused clobber.

### SKILL-7 — `uninstall.sh`

- Removes the symlink at `<skills_dir>/all-purpose-data-skill`.
- No-op (and exits 0) if the symlink is absent.
- Never touches a non-symlink path at that location.

### SKILL-8 — README

`README.md` explains, in human-readable prose:

- What the skill is (natural-language front door for productivity APIs).
- What `apl` is (the OAuth token broker + HTTP relay).
- How they cooperate ("skill = how to do it; apl = the auth engine").
- Install steps: `npm install -g @muthuishere/apl` then `curl -fsSL <install-url> | sh`.
- A 6-step typical session example.
- Links to `SKILL.md` and each `references/*.md`.

### SKILL-9 — Honest failure on platform walls

Recipes that hit a documented platform wall (non-organizer trying to pull a Teams meeting mp4, SharePoint ACL on a recording file, tenant-blocked transcript download, Gmail DLP quarantine) MUST:

- Recognise the specific error shape from the provider.
- Surface the wall in plain language ("This recording lives in the organizer's OneDrive; you don't have permission. Ask `<organizer email>` to share it, or use the chat-iteration path below if the meeting is in a Teams channel.")
- Present the documented workaround from `spec-recipes.md` when one exists.
- Never silently retry, spoof a response, or pretend success.

### SKILL-10 — Zero direct credential handling

The skill never:

- Prompts the user for a client id, client secret, tenant id, app password, or refresh token.
- Echoes any `apl login` stdout to the user.
- Reads or writes to `apl`'s token store directly.
- Suggests editing `~/.config/apl/*.json` manually.

All credential-bearing flows go through `apl setup <provider>` (first-time registration) or `apl login <handle>` (token acquisition). The skill's only credential-adjacent command is `apl accounts --json` (read-only, no secrets in output).

---

## Acceptance Criteria

- [ ] SKILL-1: `SKILL.md` description contains ≥40 distinct trigger phrases covering all six families.
- [ ] SKILL-2: Skill invoked on a machine without `apl` replies with the npm install instruction and makes no other calls.
- [ ] SKILL-3: Clean session → first productivity intent → skill runs `apl accounts --json` and presents a picker. Second intent in the same session does NOT re-prompt.
- [ ] SKILL-3: Mid-session "switch to my personal gmail" re-runs the picker and uses the new handle for subsequent calls.
- [ ] SKILL-4: Every intent listed in `spec-recipes.md` is represented by a subsection in the matching `references/<family>.md` with a runnable `apl call` line.
- [ ] SKILL-5: `references/setup-google.md` and `references/setup-microsoft.md` each name `apl setup google` / `apl setup ms` as the single setup entry point.
- [ ] SKILL-6: `sh install.sh` on a clean machine creates `~/.claude/skills/all-purpose-data-skill` as a symlink to the repo; a second run is idempotent.
- [ ] SKILL-6: `CLAUDE_SKILLS_DIR=/tmp/s sh install.sh` installs under `/tmp/s/all-purpose-data-skill`.
- [ ] SKILL-6: When `codex` is on PATH, `sh install.sh` also creates `~/.agents/skills/all-purpose-data-skill` symlink.
- [ ] SKILL-7: `sh uninstall.sh` removes the symlink; running twice is a no-op.
- [ ] SKILL-8: `README.md` renders with install + session example + links to every `references/*.md`.
- [ ] SKILL-9: "Find last week's team meeting recording" when the user isn't the organizer surfaces the SharePoint ACL wall with the documented workaround (ask organizer / chat-iteration path).
- [ ] SKILL-10: No recipe in `references/` asks the user to paste a token, client id, or secret. `grep -rni "paste.*token\|client.?secret\|client.?id" references/` returns zero hits.
- [ ] End-to-end: "send an email to x@y.com saying hi" on a machine with `google:work` configured returns a Gmail message id.
- [ ] End-to-end: "what are my meetings today" returns a list of today's events for the active handle.
- [ ] End-to-end: "switch accounts to my personal gmail" re-runs the picker and uses the new handle going forward.

---

## Implementation Notes

- **Content is ~90% markdown + 10% shell.** No Go, no Node, no Python inside the skill. All logic is prose the agent reads plus copy-pasteable `apl call …` snippets.
- **Generated-from-spec header.** Every `references/<family>.md` starts with a single-line banner: `> Generated from apl/docs/specs/spec-recipes.md §<Family> at commit <sha>.` Keep in sync manually for now; a later spec may automate.
- **Install script details.**
  - Resolve `SKILL_DIR` via `CDPATH= cd -- "$(dirname -- "$0")" && pwd` (POSIX, same pattern as `storage-workflow`).
  - Target: `${CLAUDE_SKILLS_DIR:-$HOME/.claude/skills}/all-purpose-data-skill`.
  - If the target is an existing symlink, `rm` it and re-link. If it's a real directory or file, print an error and exit 1 — never overwrite a real directory.
  - Print "Installing all-purpose-data-skill…" + the final path + a hint to restart the agent session.
  - Warn (but don't fail) if `command -v apl` is empty.
- **Distribution.** The install one-liner is `curl -fsSL https://raw.githubusercontent.com/muthuishere-agent-skills/all-purpose-data-skill/main/install.sh | sh`. The README also documents the manual `git clone && sh install.sh` path.
- **Repo contents at v1.** `LICENSE` (MIT), `README.md`, `SKILL.md`, `install.sh`, `uninstall.sh`, and the `references/` tree listed above. No CI beyond a lint pass on the shell scripts.
- **Versioning.** The skill carries its own semver in `SKILL.md` as a comment at the top (`<!-- version: 0.1.0 -->`) for human reference; there is no runtime version check against `apl`.
- **`apl call` stdout/stderr contract.** Recipes assume `apl call` prints the provider response body to stdout and status/diagnostics to stderr (per `spec-apl-call.md`). Recipes capture stdout for rendering and surface stderr only on non-zero exit.
- **Provider compatibility gates.** Google-only families: `contacts` (Google People half), `drive` (Google Drive half + Docs export). Microsoft-only families: `teams-chat`, `online-meetings`. `mail` and `calendar` and the Microsoft half of `drive`/`contacts` are mirrored per-provider. Each recipe explicitly names which provider it belongs to.

---

## Verification

Manual, run against a fresh Claude Code session with the skill installed and two handles configured (`google:work`, `ms:volentis`):

1. **Install idempotency.**
   ```bash
   sh install.sh
   sh install.sh               # second run: no errors, same symlink
   ls -l ~/.claude/skills/all-purpose-data-skill
   sh uninstall.sh
   sh uninstall.sh             # second run: no-op, exit 0
   ```
2. **Preflight gate.** Temporarily rename the `apl` binary on PATH; start a session; ask "what's in my inbox". Expect the npm install instruction and no `apl call` attempts.
3. **Handle picker.** Fresh session → "check my inbox" → skill lists `google:work` + `ms:volentis`, user picks one, skill runs. Follow-up "any new calendar events today" → no re-prompt.
4. **Handle switch.** "switch to my personal gmail" → picker re-runs.
5. **Canonical prompts (5 in a row):**
   - "Send an email to muthuishere@gmail.com saying hi from the skill." → Gmail message id returned.
   - "What are my meetings today?" → list of today's events.
   - "Find the recording of my last team meeting." → either a playback/download URL or an honest SharePoint-ACL surface with the documented workaround.
   - "Post 'starting shortly' in the volentis-eng Teams channel." → message id returned.
   - "Search my drive for invoices from Q1 2026." → file list rendered.
6. **Credential-pasting scan.** `grep -rni "paste.*token\|client.?secret\|client.?id" references/` → zero hits.
7. **Recipe parity.** For every intent in `spec-recipes.md`, grep the corresponding `references/<family>.md` for the intent's trigger phrase; 100% hit rate.
