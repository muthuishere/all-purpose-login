# Spec: apl Recipe Catalogue

**Status:** Proposed
**Priority:** High (drives companion skill; validates `apl call` surface)
**Depends on:** `spec-apl-call.md` (sibling — being written in parallel), `spec-cli-command-surface.md`
**Authoritative sources:** `docs/microsoft-graph.md`, `docs/google-apis.md`, `tests/e2e/ms_smoke.sh`, `tests/e2e/google_smoke.sh`. Where this spec and those docs drift, **the docs win** — update this file, not the other way round.

---

## Problem

`apl` is a token broker. By itself it just hands out access tokens; the actual value to a human or an AI agent is the ability to phrase a natural intent ("show me today's unread email", "download yesterday's Teams recording", "RSVP yes to that standup") as an HTTP call against Google or Microsoft Graph.

Today that knowledge lives scattered:

- `docs/microsoft-graph.md` and `docs/google-apis.md` (authoritative, but prose-shaped)
- `tests/e2e/*_smoke.sh` (verified, but hand-rolled curl invocations)
- Muthu's head (every gotcha, every 403 wall, every URL-encoding landmine)

The companion agent skill `all-purpose-data-skill` — the front door for LLM agents calling Google + Microsoft APIs via `apl call` — needs a machine-legible catalogue where each recipe is a self-contained, copy-pasteable unit with exact URL, body, scopes, expected response, and failure modes. Without it, the skill's `references/` files become another prose translation of the same material and drift the moment any endpoint changes.

This spec defines that catalogue. Every entry is verified against a real tenant (reqsume / volentis / muthuishere@gmail.com) and is the canonical form from which the companion skill's `references/` files are generated.

---

## Goals

- One authoritative list of every recipe `apl call` (or `apl login` + curl) supports end-to-end.
- Uniform per-recipe shape so an LLM agent can select, parameterise, and execute without prose parsing.
- Coverage parity with `docs/microsoft-graph.md` and `docs/google-apis.md` — every row in those tables is a recipe here.
- Fallback patterns documented for the rare cases `apl call` cannot handle (binary streams, pre-authenticated SharePoint redirect URLs, multipart batch).
- Gotchas surfaced at the recipe level (per-endpoint), not buried in a single "common pitfalls" section.

## Non-Goals

- App-only (client credentials) auth flows. v1 is delegated user auth only; tenant-wide app-only recipes (e.g. `CallRecord.Read.All` read across a tenant, `Files.Read.All` as a Role) are out of scope.
- Provider onboarding (Graph Explorer tours, GCP project creation) — covered by `spec-setup-bootstrapper.md`.
- Device-code flow recipes — out of v1 scope per PRD §11.
- Webhook receivers (hosting the HTTP endpoint a Graph subscription POSTs to) — we document the *subscribe* call, not the *receiver*.
- Service-specific wrappers (e.g. `apl gmail send`). Recipes describe HTTP intent; `apl call` is the generic mechanism.

---

## Functional Requirements

### Recipe template

Every recipe in this catalogue uses this exact shape:

```
### <FAMILY>-N — <human name>

**Purpose:** one line.
**Handle:** ms:<label> | google:<label> | either.
**Scopes:** comma-separated list of full scope names (see provider scope aliases).
**Call:**
    apl call <handle> <METHOD> "<url>" [--body '<json>'] [-o <file>]
**Fallback (when apl call is insufficient):** curl+token pattern.
**Expected:** HTTP 2xx code + one-line shape of the key response fields.
**Gotchas:** list — or `N/A`.
```

Abbreviations used inside recipes:

- `$GRAPH` = `https://graph.microsoft.com/v1.0`
- `$GMAIL` = `https://gmail.googleapis.com/gmail/v1/users/me`
- `$CAL` = `https://www.googleapis.com/calendar/v3`
- `$DRIVE` = `https://www.googleapis.com/drive/v3`
- `$PEOPLE` = `https://people.googleapis.com/v1`
- `$OIDC` = `https://openidconnect.googleapis.com/v1`
- `$TOKEN` = output of `apl login <handle>` (used only in Fallback blocks)
- In bash, literal `$` in URL query params (`$top`, `$filter`, `$select`, `$format`) must be escaped as `\$` OR single-quoted; examples below use `\$` inside double-quoted URLs. `apl call` strips nothing — pass the URL as the provider expects to receive it.

### Handle grammar

Per `spec-cli-command-surface.md` CLI-2: `^[a-z]+:[a-zA-Z0-9._-]+$`. Recipe examples use `ms:reqsume` and `google:muthu` as canonical placeholders; substitute any valid handle.

### Recipe families

Below, recipes are grouped into 14 families. Every recipe is numbered `<FAMILY>-<N>` for stable citation from the companion skill.

---

### State model

Recipes often need user-specific context that isn't worth re-asking every call: the active `ms:` / `google:` handle, the current GitHub repo (`owner/name`), the "since last brief" timestamp for delta recipes, the user's canonical email for `to:me` filters, etc. That context lives in three layers:

```
Layer      Where                                    Scope
─────────  ──────────────────────────────────────   ──────────────────────
session    agent conversation memory only           ephemeral override
project    ~/config/muthuishere-agent-skills/       per-repo defaults
           <reponame>/state.yaml                    (active handles, gh repo,
                                                     'since' timestamp, etc.)
global     ~/.claude/skills/all-purpose-data/       user-global defaults
           state.yaml                                (rare; fallback only)
```

**Resolution order:** session > project > global > ask.

**First-touch interview model is lazy.** Do NOT ask everything upfront. When a recipe needs a value that is not in any layer, ask for *just that value*, execute, then cache it to `project` state for next time. The companion skill owns reads/writes to these files; recipes here assume the values are already resolved at call time.

**Typical keys:**

- `ms_handle` — e.g. `ms:reqsume`. Required for any Family 1-11 MS recipe.
- `google_handle` — e.g. `google:muthu`. Required for any Family 1-11 Google recipe.
- `gh_repo` — `owner/name`. Required for any Family 12 recipe outside a git repo; auto-detected from CWD otherwise.
- `user_email_ms` / `user_email_google` — used by "to:me" filters and mention searches. Resolvable from IDENT-1 / IDENT-4 on first touch.
- `gh_login` — GitHub username. Resolvable from `gh api user --jq .login` on first touch. Used for `user:@me` searches and review-request filters.
- `last_brief_ts` — ISO-8601 UTC. Written by BRIEF-1 and BRIEF-2 at successful completion. Read by BRIEF-2.

Handles and tokens themselves are NOT stored here — those live in the OS keyring via `apl` and in `gh`'s own token store. `state.yaml` stores only the *selection* of which handle is active.

---

## Family 1 — Identity (4 recipes)

### IDENT-1 — Current user (Microsoft)
**Purpose:** Signed-in user profile.
**Handle:** `ms:<label>`
**Scopes:** `User.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me"
```
**Expected:** 200; `{ id, displayName, mail, userPrincipalName, jobTitle, ... }`.
**Gotchas:** `mail` is null for personal MSA accounts; fall back to `userPrincipalName`.

### IDENT-2 — Current user lean projection (Microsoft)
**Purpose:** Minimal identity payload.
**Handle:** `ms:<label>`
**Scopes:** `User.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me?\$select=displayName,mail,userPrincipalName,id"
```
**Expected:** 200; four fields only.
**Gotchas:** Literal `$` needs `\$` in bash.

### IDENT-3 — Profile photo download (Microsoft)
**Purpose:** Fetch the user's avatar as binary.
**Handle:** `ms:<label>`
**Scopes:** `User.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/photo/\$value" -o me.jpg
```
**Expected:** 200 image/jpeg bytes to `me.jpg`.
**Gotchas:** 404 is common when the user has no photo; treat as "no photo" rather than failure. Response is binary — always pass `-o`.

### IDENT-4 — Current user (Google, OIDC)
**Purpose:** Signed-in user profile.
**Handle:** `google:<label>`
**Scopes:** `openid email profile`
**Call:**
```bash
apl call google:muthu GET "$OIDC/userinfo"
```
**Expected:** 200; `{ sub, email, name, picture, given_name, family_name }`.
**Gotchas:** Prefer the OIDC host over the legacy `www.googleapis.com/oauth2/v3/userinfo` alias.

### IDENT-5 — My People profile (Google)
**Purpose:** Extended profile from People API.
**Handle:** `google:<label>`
**Scopes:** `contacts.readonly`
**Call:**
```bash
apl call google:muthu GET "$PEOPLE/people/me?personFields=names,emailAddresses,photos,phoneNumbers"
```
**Expected:** 200; `{ resourceName, names[], emailAddresses[], ... }`.
**Gotchas:** `personFields` is **required** — requests without it 400.

### IDENT-6 — Directory search (Microsoft)
**Purpose:** Find a user in the tenant directory by name.
**Handle:** `ms:<label>`
**Scopes:** `User.Read` (tenant directory read — in-tenant only)
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/users?\$search=\"displayName:shaama\"" -H 'ConsistencyLevel: eventual'
```
**Expected:** 200; `{ value: [{id, displayName, mail, jobTitle}, ...] }`.
**Gotchas:** `$search` **requires** `ConsistencyLevel: eventual` header — omit and you get 400. Quote values with `"..."`.

### IDENT-7 — Look up user by email (Microsoft)
**Purpose:** Resolve AAD object id + metadata from an email.
**Handle:** `ms:<label>`
**Scopes:** `User.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/users/shaama@reqsume.com?\$select=id,displayName,mail,jobTitle"
```
**Expected:** 200; single user object.
**Gotchas:** 404 for external / personal addresses not in the tenant directory.

### IDENT-8 — My presence (Microsoft)
**Purpose:** Current presence state.
**Handle:** `ms:<label>`
**Scopes:** `Presence.Read` (falls under default `User.Read` for /me/presence via `Presence.Read` scope)
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/presence"
```
**Expected:** 200; `{ availability: "Available"|"Busy"|..., activity: "...", id }`.

### IDENT-9 — Others' presence (Microsoft, batch)
**Purpose:** Presence for a list of user ids.
**Handle:** `ms:<label>`
**Scopes:** `Presence.Read.All`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/communications/getPresencesByUserId" \
  --body '{"ids":["<user-id-1>","<user-id-2>"]}'
```
**Expected:** 200; `{ value: [{id, availability, activity}, ...] }`.
**Gotchas:** Takes AAD object ids, not UPNs.

### IDENT-10 — Set my presence (Microsoft)
**Purpose:** Explicitly set presence (e.g. "InACall").
**Handle:** `ms:<label>`
**Scopes:** `Presence.ReadWrite`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/me/presence/setPresence" \
  --body '{"sessionId":"<app-guid>","availability":"Busy","activity":"InACall","expirationDuration":"PT1H"}'
```
**Expected:** 200 empty body.
**Gotchas:** `sessionId` must be a GUID; Graph uses it to scope your app's view of presence.

---

## Family 2 — Mail read (13 recipes)

### MAIL-R-1 — Recent messages (Microsoft)
**Purpose:** 10 newest messages across all folders.
**Handle:** `ms:<label>`
**Scopes:** `Mail.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/messages?\$top=10&\$orderby=receivedDateTime%20desc&\$select=subject,from,receivedDateTime,bodyPreview"
```
**Expected:** 200; `{ value: [{id, subject, from, receivedDateTime, bodyPreview}, ...] }`.
**Gotchas:** URL-encode spaces inside `$orderby` as `%20`.

### MAIL-R-2 — Unread in inbox (Microsoft)
**Purpose:** Inbox messages where `isRead=false`.
**Handle:** `ms:<label>`
**Scopes:** `Mail.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/mailFolders/inbox/messages?\$filter=isRead%20eq%20false&\$top=20&\$select=subject,from,receivedDateTime"
```
**Expected:** 200; filtered `value[]`.
**Gotchas:** N/A.

### MAIL-R-3 — Unread count only (Microsoft)
**Purpose:** Just the badge number.
**Handle:** `ms:<label>`
**Scopes:** `Mail.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/mailFolders/inbox?\$select=unreadItemCount,totalItemCount"
```
**Expected:** 200; `{ unreadItemCount: N, totalItemCount: M }`.

### MAIL-R-4 — Today's inbox (Microsoft)
**Purpose:** Messages received since UTC midnight.
**Handle:** `ms:<label>`
**Scopes:** `Mail.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/messages?\$filter=receivedDateTime%20ge%20$(date -u +%Y-%m-%dT00:00:00Z)&\$orderby=receivedDateTime%20desc"
```
**Expected:** 200; today's value[].
**Gotchas:** OData filter time is ISO-8601 UTC; `date -u -v+1d` is the BSD form (macOS). On Linux use `date -u -d tomorrow`.

### MAIL-R-5 — Search inbox by keyword (Microsoft)
**Purpose:** Full-text search across mail.
**Handle:** `ms:<label>`
**Scopes:** `Mail.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/messages?\$search=\"project%20kickoff\"" -H 'ConsistencyLevel: eventual'
```
**Expected:** 200.
**Gotchas:** `$search` requires `ConsistencyLevel: eventual`. Values must be double-quoted inside the URL. Cannot combine with `$filter` or `$orderby`.

### MAIL-R-6 — Mail from a specific sender (Microsoft)
**Purpose:** Filter by sender address.
**Handle:** `ms:<label>`
**Scopes:** `Mail.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/messages?\$filter=from/emailAddress/address%20eq%20'shaama@reqsume.com'&\$top=20"
```
**Expected:** 200.
**Gotchas:** Single-quote string values inside `$filter`.

### MAIL-R-7 — Emails with attachments (Microsoft)
**Purpose:** `hasAttachments=true` filter.
**Handle:** `ms:<label>`
**Scopes:** `Mail.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/messages?\$filter=hasAttachments%20eq%20true&\$top=20&\$select=subject,from,hasAttachments"
```
**Expected:** 200.

### MAIL-R-8 — Get single message (Microsoft)
**Purpose:** Full message body + headers.
**Handle:** `ms:<label>`
**Scopes:** `Mail.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/messages/{id}"
```
**Expected:** 200; `{ id, subject, body{contentType, content}, from, toRecipients, ccRecipients, ... }`.

### MAIL-R-9 — Raw MIME (EML) (Microsoft)
**Purpose:** Download RFC-2822 source of a message.
**Handle:** `ms:<label>`
**Scopes:** `Mail.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/messages/{id}/\$value" -o message.eml
```
**Expected:** 200 text/plain EML bytes.
**Gotchas:** `$value` must be escaped; response is `Content-Type: text/plain`.

### MAIL-R-10 — List attachments (Microsoft)
**Purpose:** Attachment metadata for a message.
**Handle:** `ms:<label>`
**Scopes:** `Mail.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/messages/{id}/attachments"
```
**Expected:** 200; `{ value: [{id, name, contentType, size}, ...] }`.

### MAIL-R-11 — Download attachment (Microsoft)
**Purpose:** Binary attachment bytes.
**Handle:** `ms:<label>`
**Scopes:** `Mail.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/messages/{id}/attachments/{aid}/\$value" -o attachment.bin
```
**Expected:** 200 binary bytes.

### MAIL-R-12 — List folders (Microsoft)
**Purpose:** Mail folders for the account.
**Handle:** `ms:<label>`
**Scopes:** `Mail.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/mailFolders?\$top=50"
```
**Expected:** 200; `{ value: [{id, displayName, unreadItemCount}, ...] }`.

### MAIL-R-13 — Sent mail (Microsoft)
**Purpose:** Messages in Sent Items.
**Handle:** `ms:<label>`
**Scopes:** `Mail.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/mailFolders/sentItems/messages?\$top=20&\$orderby=sentDateTime%20desc"
```
**Expected:** 200.

### MAIL-R-14 — Drafts (Microsoft)
**Purpose:** Unsent drafts.
**Handle:** `ms:<label>`
**Scopes:** `Mail.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/mailFolders/drafts/messages"
```
**Expected:** 200.

### MAIL-R-15 — Delta sync (Microsoft)
**Purpose:** New/changed/deleted messages since last sync.
**Handle:** `ms:<label>`
**Scopes:** `Mail.Read`
**Call:**
```bash
# First call:
apl call ms:reqsume GET "$GRAPH/me/mailFolders/inbox/messages/delta"
# Subsequent: use the @odata.deltaLink from the last page of the previous run.
apl call ms:reqsume GET "<saved-deltaLink>"
```
**Expected:** 200; paginated `{ value, @odata.nextLink | @odata.deltaLink }`.
**Gotchas:** Must follow every `@odata.nextLink` to completion — only the final page has `@odata.deltaLink`. Persist it per `(handle, folder)`.

### MAIL-R-16 — List messages (Gmail)
**Purpose:** 10 most recent message ids.
**Handle:** `google:<label>`
**Scopes:** `gmail.readonly` (or `gmail.modify`)
**Call:**
```bash
apl call google:muthu GET "$GMAIL/messages?maxResults=10"
```
**Expected:** 200; `{ messages: [{id, threadId}, ...], resultSizeEstimate, nextPageToken? }`.
**Gotchas:** List endpoint returns ids only — fetch each to get headers/bodies.

### MAIL-R-17 — Unread (Gmail)
**Purpose:** Messages where `is:unread`.
**Handle:** `google:<label>`
**Scopes:** `gmail.readonly`
**Call:**
```bash
apl call google:muthu GET "$GMAIL/messages?q=is:unread&maxResults=20"
```
**Expected:** 200; ids only.

### MAIL-R-18 — Today's mail (Gmail)
**Purpose:** `newer_than:1d` in inbox.
**Handle:** `google:<label>`
**Scopes:** `gmail.readonly`
**Call:**
```bash
apl call google:muthu GET "$GMAIL/messages?q=in:inbox%20newer_than:1d&maxResults=50"
```
**Expected:** 200.
**Gotchas:** URL-encode spaces (`%20`) in the `q=` value. Gmail's `q=` supports: `from:`, `to:`, `cc:`, `subject:`, `label:`, `has:attachment`, `filename:pdf`, `is:unread`, `is:starred`, `newer_than:1d`, `older_than:7d`, `before:2026/04/23`, `after:2026/04/01`, `larger:5M`, `category:primary|social|promotions|updates`, `OR`, `-`.

### MAIL-R-19 — Sender-specific search (Gmail)
**Purpose:** From a specific address.
**Handle:** `google:<label>`
**Scopes:** `gmail.readonly`
**Call:**
```bash
apl call google:muthu GET "$GMAIL/messages?q=from:shaama@reqsume.com&maxResults=50"
```
**Expected:** 200.

### MAIL-R-20 — With attachments (Gmail)
**Purpose:** `has:attachment`.
**Handle:** `google:<label>`
**Scopes:** `gmail.readonly`
**Call:**
```bash
apl call google:muthu GET "$GMAIL/messages?q=has:attachment&maxResults=50"
```
**Expected:** 200.

### MAIL-R-21 — Full message (Gmail)
**Purpose:** Headers + MIME tree + bodies.
**Handle:** `google:<label>`
**Scopes:** `gmail.readonly`
**Call:**
```bash
apl call google:muthu GET "$GMAIL/messages/{id}?format=full"
```
**Expected:** 200; `{ id, threadId, labelIds[], payload{headers[], parts[]}, snippet }`. Body text lives in `payload.parts[*].body.data` as base64url.

### MAIL-R-22 — Headers only (Gmail)
**Purpose:** Lean Subject/From/Date projection.
**Handle:** `google:<label>`
**Scopes:** `gmail.readonly`
**Call:**
```bash
apl call google:muthu GET "$GMAIL/messages/{id}?format=metadata&metadataHeaders=Subject&metadataHeaders=From&metadataHeaders=Date"
```
**Expected:** 200; same shape as full but `payload.parts` omitted.
**Gotchas:** Repeat `metadataHeaders=` per header; comma-separated does not work.

### MAIL-R-23 — Raw RFC-2822 (Gmail)
**Purpose:** `.eml` source as base64url.
**Handle:** `google:<label>`
**Scopes:** `gmail.readonly`
**Call:**
```bash
apl call google:muthu GET "$GMAIL/messages/{id}?format=raw"
```
**Expected:** 200; `raw` field is base64url — `python3 -c "import base64,sys,json;print(base64.urlsafe_b64decode(json.load(sys.stdin)['raw']+'==').decode())"`.

### MAIL-R-24 — Get attachment (Gmail)
**Purpose:** Download an attachment body.
**Handle:** `google:<label>`
**Scopes:** `gmail.readonly`
**Call:**
```bash
apl call google:muthu GET "$GMAIL/messages/{msgId}/attachments/{attId}"
```
**Expected:** 200; `{ size, data }` where `data` is base64url bytes.
**Gotchas:** Attachment id is not globally unique — it's scoped to the message. Pull it from `payload.parts[*].body.attachmentId` in the full message fetch.

### MAIL-R-25 — List labels (Gmail)
**Purpose:** All system + user labels.
**Handle:** `google:<label>`
**Scopes:** `gmail.readonly`
**Call:**
```bash
apl call google:muthu GET "$GMAIL/labels"
```
**Expected:** 200; `{ labels: [{id, name, type}, ...] }`.

### MAIL-R-26 — History since ID (Gmail — delta)
**Purpose:** All changes since a known historyId.
**Handle:** `google:<label>`
**Scopes:** `gmail.readonly`
**Call:**
```bash
apl call google:muthu GET "$GMAIL/history?startHistoryId=<prev>"
```
**Expected:** 200; `{ history: [{id, messages, messagesAdded, messagesDeleted, labelsAdded, labelsRemoved}, ...], historyId }`.
**Gotchas:** A message's `historyId` (on any fetch) is your bookmark. Expired historyIds (>7 days old typically) return 404 — fall back to a full sync.

---

## Family 3 — Mail write (11 recipes)

### MAIL-W-1 — Send plain text (Microsoft)
**Purpose:** Send a simple text email.
**Handle:** `ms:<label>`
**Scopes:** `Mail.Send`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/me/sendMail" --body '{
  "message":{
    "subject":"apl smoke",
    "body":{"contentType":"Text","content":"hello"},
    "toRecipients":[{"emailAddress":{"address":"x@y.com"}}]
  },
  "saveToSentItems":true
}'
```
**Expected:** 202 Accepted, empty body.
**Gotchas:** The wrapper key is `message` (singular) and the whole payload includes `saveToSentItems`. Returns 202 not 200.

### MAIL-W-2 — Send with attachment (Microsoft)
**Purpose:** Send mail with an inline-encoded attachment.
**Handle:** `ms:<label>`
**Scopes:** `Mail.Send`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/me/sendMail" --body '{
  "message":{
    "subject":"report",
    "body":{"contentType":"Text","content":"see attached"},
    "toRecipients":[{"emailAddress":{"address":"x@y.com"}}],
    "attachments":[{
      "@odata.type":"#microsoft.graph.fileAttachment",
      "name":"report.pdf",
      "contentType":"application/pdf",
      "contentBytes":"<base64-encoded-bytes>"
    }]
  }
}'
```
**Expected:** 202.
**Gotchas:** `contentBytes` is plain base64 (not base64url). For files >3MB use the create-draft + upload-session flow instead (not covered in v1).

### MAIL-W-3 — Reply (Microsoft)
**Purpose:** Reply to a message.
**Handle:** `ms:<label>`
**Scopes:** `Mail.Send`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/me/messages/{id}/reply" --body '{"comment":"thanks"}'
```
**Expected:** 202.

### MAIL-W-4 — Reply all (Microsoft)
**Purpose:** Reply to all recipients.
**Handle:** `ms:<label>`
**Scopes:** `Mail.Send`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/me/messages/{id}/replyAll" --body '{"comment":"thanks all"}'
```
**Expected:** 202.

### MAIL-W-5 — Forward (Microsoft)
**Purpose:** Forward a message.
**Handle:** `ms:<label>`
**Scopes:** `Mail.Send`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/me/messages/{id}/forward" --body '{
  "toRecipients":[{"emailAddress":{"address":"x@y.com"}}],
  "comment":"fyi"
}'
```
**Expected:** 202.

### MAIL-W-6 — Create draft (Microsoft)
**Purpose:** Save a draft without sending.
**Handle:** `ms:<label>`
**Scopes:** `Mail.ReadWrite`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/me/messages" --body '{
  "subject":"draft",
  "body":{"contentType":"Text","content":"wip"},
  "toRecipients":[{"emailAddress":{"address":"x@y.com"}}]
}'
```
**Expected:** 201; draft message object including `id`.

### MAIL-W-7 — Send a draft (Microsoft)
**Purpose:** Send a previously-created draft.
**Handle:** `ms:<label>`
**Scopes:** `Mail.Send`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/me/messages/{draftId}/send"
```
**Expected:** 202.

### MAIL-W-8 — Mark read/unread (Microsoft)
**Purpose:** Toggle read state.
**Handle:** `ms:<label>`
**Scopes:** `Mail.ReadWrite`
**Call:**
```bash
apl call ms:reqsume PATCH "$GRAPH/me/messages/{id}" --body '{"isRead":true}'
```
**Expected:** 200; updated message.

### MAIL-W-9 — Move to folder (Microsoft)
**Purpose:** Move a message to a folder.
**Handle:** `ms:<label>`
**Scopes:** `Mail.ReadWrite`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/me/messages/{id}/move" --body '{"destinationId":"archive"}'
```
**Expected:** 201; the message as it now exists in the destination folder.
**Gotchas:** `destinationId` accepts well-known folder names: `inbox`, `sentItems`, `drafts`, `deleteditems`, `archive`, `junkemail`. Or use a specific folder id from MAIL-R-12.

### MAIL-W-10 — Delete (trash) (Microsoft)
**Purpose:** Move to Deleted Items.
**Handle:** `ms:<label>`
**Scopes:** `Mail.ReadWrite`
**Call:**
```bash
apl call ms:reqsume DELETE "$GRAPH/me/messages/{id}"
```
**Expected:** 204.
**Gotchas:** This hard-deletes from Deleted Items if the message was already there. Prefer MAIL-W-9 with `destinationId=deleteditems` for soft-delete semantics.

### MAIL-W-11 — Send (Gmail)
**Purpose:** Send via Gmail.
**Handle:** `google:<label>`
**Scopes:** `gmail.send` or `gmail.modify`
**Call:**
```bash
RAW=$(python3 -c "
import base64
from email.message import EmailMessage
m = EmailMessage()
m['To'] = 'x@y.com'
m['Subject'] = 'apl smoke'
m.set_content('hello')
print(base64.urlsafe_b64encode(bytes(m)).decode().rstrip('='))")
apl call google:muthu POST "$GMAIL/messages/send" --body "{\"raw\":\"$RAW\"}"
```
**Expected:** 200; `{ id, threadId, labelIds:["SENT"] }`.
**Gotchas:** Gmail's send takes `{"raw": <base64url RFC-2822>}`, NOT a structured JSON message. This is the single most-missed detail in the Google API.

### MAIL-W-12 — Reply (Gmail, threaded)
**Purpose:** Reply keeping the thread.
**Handle:** `google:<label>`
**Scopes:** `gmail.send`
**Call:**
```bash
# Build RAW with In-Reply-To and References headers set to the parent Message-ID:
apl call google:muthu POST "$GMAIL/messages/send" --body "{\"raw\":\"$RAW\",\"threadId\":\"<threadId>\"}"
```
**Expected:** 200; message with matching `threadId`.
**Gotchas:** `threadId` alone isn't sufficient — you must also set `In-Reply-To` and `References` in the RFC-2822 headers or the Gmail client won't render the reply as threaded.

### MAIL-W-13 — Create draft (Gmail)
**Purpose:** Save a draft.
**Handle:** `google:<label>`
**Scopes:** `gmail.modify`
**Call:**
```bash
apl call google:muthu POST "$GMAIL/drafts" --body "{\"message\":{\"raw\":\"$RAW\"}}"
```
**Expected:** 200; `{ id, message: {...} }`.

### MAIL-W-14 — Send draft (Gmail)
**Purpose:** Send a saved draft.
**Handle:** `google:<label>`
**Scopes:** `gmail.send`
**Call:**
```bash
apl call google:muthu POST "$GMAIL/drafts/send" --body '{"id":"<draftId>"}'
```
**Expected:** 200; the sent message.

### MAIL-W-15 — Mark read (Gmail)
**Purpose:** Remove UNREAD label.
**Handle:** `google:<label>`
**Scopes:** `gmail.modify`
**Call:**
```bash
apl call google:muthu POST "$GMAIL/messages/{id}/modify" --body '{"removeLabelIds":["UNREAD"]}'
```
**Expected:** 200.

### MAIL-W-16 — Mark unread (Gmail)
**Purpose:** Add UNREAD label.
**Handle:** `google:<label>`
**Scopes:** `gmail.modify`
**Call:**
```bash
apl call google:muthu POST "$GMAIL/messages/{id}/modify" --body '{"addLabelIds":["UNREAD"]}'
```
**Expected:** 200.

### MAIL-W-17 — Trash (Gmail)
**Purpose:** Move to Trash.
**Handle:** `google:<label>`
**Scopes:** `gmail.modify`
**Call:**
```bash
apl call google:muthu POST "$GMAIL/messages/{id}/trash"
```
**Expected:** 200; message with `TRASH` label added.

### MAIL-W-18 — Untrash (Gmail)
**Purpose:** Restore from Trash.
**Handle:** `google:<label>`
**Scopes:** `gmail.modify`
**Call:**
```bash
apl call google:muthu POST "$GMAIL/messages/{id}/untrash"
```
**Expected:** 200.

### MAIL-W-19 — Permanently delete (Gmail)
**Purpose:** Hard-delete, bypasses trash.
**Handle:** `google:<label>`
**Scopes:** `gmail.modify`
**Call:**
```bash
apl call google:muthu DELETE "$GMAIL/messages/{id}"
```
**Expected:** 204.
**Gotchas:** Irreversible.

### MAIL-W-20 — Create label (Gmail)
**Purpose:** Add a new user label.
**Handle:** `google:<label>`
**Scopes:** `gmail.modify`
**Call:**
```bash
apl call google:muthu POST "$GMAIL/labels" --body '{
  "name":"Project/X",
  "messageListVisibility":"show",
  "labelListVisibility":"labelShow"
}'
```
**Expected:** 200; `{ id: "Label_123", name, ... }`.
**Gotchas:** Nested names with `/` render as nested labels in the web UI.

### MAIL-W-21 — Apply label (Gmail)
**Purpose:** Attach a label to a message.
**Handle:** `google:<label>`
**Scopes:** `gmail.modify`
**Call:**
```bash
apl call google:muthu POST "$GMAIL/messages/{id}/modify" --body '{"addLabelIds":["Label_123"]}'
```
**Expected:** 200.

### MAIL-W-22 — Remove label (Gmail)
**Purpose:** Detach a label.
**Handle:** `google:<label>`
**Scopes:** `gmail.modify`
**Call:**
```bash
apl call google:muthu POST "$GMAIL/messages/{id}/modify" --body '{"removeLabelIds":["Label_123"]}'
```
**Expected:** 200.

---

## Family 4 — Calendar read (8 recipes)

### CAL-R-1 — Today's events (Microsoft, calendarView)
**Purpose:** All events occurring today.
**Handle:** `ms:<label>`
**Scopes:** `Calendars.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/calendarView?startDateTime=$(date -u +%Y-%m-%dT00:00:00Z)&endDateTime=$(date -u -v+1d +%Y-%m-%dT00:00:00Z)&\$select=subject,start,end,organizer,isOnlineMeeting,onlineMeeting"
```
**Expected:** 200; `{ value: [...events expanded from recurrences] }`.
**Gotchas:** `calendarView` expands recurring events into instances; `/events` does NOT. Use calendarView for "what's on my day".

### CAL-R-2 — This week's events (Microsoft)
**Purpose:** Mon–Sun window.
**Handle:** `ms:<label>`
**Scopes:** `Calendars.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/calendarView?startDateTime=<mon-iso>&endDateTime=<sun-iso>"
```
**Expected:** 200.

### CAL-R-3 — Next 5 upcoming (Microsoft)
**Purpose:** Forward-looking events.
**Handle:** `ms:<label>`
**Scopes:** `Calendars.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/events?\$filter=start/dateTime%20ge%20'$(date -u +%Y-%m-%dT%H:%M:%SZ)'&\$orderby=start/dateTime&\$top=5"
```
**Expected:** 200.
**Gotchas:** Single-quote the datetime value inside `$filter`.

### CAL-R-4 — Events with Teams link (Microsoft)
**Purpose:** Only online meetings.
**Handle:** `ms:<label>`
**Scopes:** `Calendars.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/events?\$filter=isOnlineMeeting%20eq%20true&\$orderby=start/dateTime%20desc&\$top=50&\$select=subject,start,organizer,onlineMeeting"
```
**Expected:** 200; `onlineMeeting.joinUrl` populated.

### CAL-R-5 — Single event (Microsoft)
**Purpose:** Full event record.
**Handle:** `ms:<label>`
**Scopes:** `Calendars.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/events/{id}"
```
**Expected:** 200; full event including `attendees`, `body`, `recurrence`.

### CAL-R-6 — List my calendars (Microsoft)
**Purpose:** Calendars visible to the user.
**Handle:** `ms:<label>`
**Scopes:** `Calendars.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/calendars"
```
**Expected:** 200; `{ value: [{id, name, owner, canEdit}, ...] }`.

### CAL-R-7 — Today's events (Google)
**Purpose:** Primary calendar today.
**Handle:** `google:<label>`
**Scopes:** `calendar.readonly`
**Call:**
```bash
apl call google:muthu GET "$CAL/calendars/primary/events?timeMin=$(date -u +%Y-%m-%dT00:00:00Z)&timeMax=$(date -u -v+1d +%Y-%m-%dT00:00:00Z)&singleEvents=true&orderBy=startTime"
```
**Expected:** 200; `{ items: [...], nextSyncToken? }`.
**Gotchas:** `orderBy=startTime` **only** works when `singleEvents=true`. Omit `timeMin` and Google defaults it to "now" — you'll silently miss past events.

### CAL-R-8 — Next 10 upcoming (Google)
**Purpose:** Forward-looking.
**Handle:** `google:<label>`
**Scopes:** `calendar.readonly`
**Call:**
```bash
apl call google:muthu GET "$CAL/calendars/primary/events?timeMin=$(date -u +%Y-%m-%dT%H:%M:%SZ)&maxResults=10&singleEvents=true&orderBy=startTime"
```
**Expected:** 200.

### CAL-R-9 — Search by text (Google)
**Purpose:** Event text search.
**Handle:** `google:<label>`
**Scopes:** `calendar.readonly`
**Call:**
```bash
apl call google:muthu GET "$CAL/calendars/primary/events?q=retro&singleEvents=true&timeMin=2026-01-01T00:00:00Z"
```
**Expected:** 200.

### CAL-R-10 — Single event (Google)
**Purpose:** Full event record.
**Handle:** `google:<label>`
**Scopes:** `calendar.readonly`
**Call:**
```bash
apl call google:muthu GET "$CAL/calendars/primary/events/{eventId}"
```
**Expected:** 200; includes `conferenceData`, `hangoutLink`, `attendees`, `organizer`.

### CAL-R-11 — List my calendars (Google)
**Purpose:** All visible calendars.
**Handle:** `google:<label>`
**Scopes:** `calendar.readonly`
**Call:**
```bash
apl call google:muthu GET "$CAL/users/me/calendarList"
```
**Expected:** 200; `{ items: [{id, summary, primary, accessRole}, ...] }`.

### CAL-R-12 — Meet-enabled events (Google)
**Purpose:** Events with a Meet link.
**Handle:** `google:<label>`
**Scopes:** `calendar.readonly`
**Call:**
```bash
apl call google:muthu GET "$CAL/calendars/primary/events?timeMin=2026-01-01T00:00:00Z&maxResults=50&singleEvents=true&orderBy=startTime"
# Then client-side filter for items where hangoutLink OR conferenceData.entryPoints[].uri exists
```
**Expected:** 200.
**Gotchas:** Older events only have `hangoutLink`; new API-created ones fill `conferenceData.entryPoints[*].uri` with `entryPointType=="video"`. Check both.

---

## Family 5 — Calendar write (9 recipes)

### CAL-W-1 — Create event (Microsoft)
**Purpose:** New calendar event.
**Handle:** `ms:<label>`
**Scopes:** `Calendars.ReadWrite`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/me/events" --body '{
  "subject":"Sync",
  "start":{"dateTime":"2026-05-01T09:00:00","timeZone":"UTC"},
  "end":{"dateTime":"2026-05-01T09:30:00","timeZone":"UTC"},
  "attendees":[{"emailAddress":{"address":"x@y.com"},"type":"required"}]
}'
```
**Expected:** 201; event object with `id`.
**Gotchas:** `timeZone` is required alongside `dateTime`. Invitations are sent automatically unless you set `sendInvitations=false` (not a Graph thing — use `createOrGet` variants or the `responseRequested` attendee flag).

### CAL-W-2 — Create event with Teams link (Microsoft)
**Purpose:** Auto-provision a Teams meeting.
**Handle:** `ms:<label>`
**Scopes:** `Calendars.ReadWrite`, `OnlineMeetings.ReadWrite` (for some tenants)
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/me/events" --body '{
  "subject":"Teams sync",
  "start":{"dateTime":"2026-05-01T09:00:00","timeZone":"UTC"},
  "end":{"dateTime":"2026-05-01T09:30:00","timeZone":"UTC"},
  "isOnlineMeeting":true,
  "onlineMeetingProvider":"teamsForBusiness"
}'
```
**Expected:** 201; event with `onlineMeeting.joinUrl`.

### CAL-W-3 — Update event (Microsoft)
**Purpose:** Patch an existing event.
**Handle:** `ms:<label>`
**Scopes:** `Calendars.ReadWrite`
**Call:**
```bash
apl call ms:reqsume PATCH "$GRAPH/me/events/{id}" --body '{"subject":"Renamed"}'
```
**Expected:** 200; updated event.

### CAL-W-4 — Cancel event (Microsoft)
**Purpose:** Cancel with notification.
**Handle:** `ms:<label>`
**Scopes:** `Calendars.ReadWrite`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/me/events/{id}/cancel" --body '{"comment":"conflict"}'
```
**Expected:** 202.
**Gotchas:** Organizer only — attendees should DELETE their local copy instead.

### CAL-W-5 — Delete event (Microsoft)
**Purpose:** Remove from calendar.
**Handle:** `ms:<label>`
**Scopes:** `Calendars.ReadWrite`
**Call:**
```bash
apl call ms:reqsume DELETE "$GRAPH/me/events/{id}"
```
**Expected:** 204.

### CAL-W-6 — Accept / decline / tentatively accept (Microsoft)
**Purpose:** RSVP to an invite.
**Handle:** `ms:<label>`
**Scopes:** `Calendars.ReadWrite`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/me/events/{id}/accept" --body '{"sendResponse":true,"comment":"joining"}'
apl call ms:reqsume POST "$GRAPH/me/events/{id}/decline" --body '{"sendResponse":true}'
apl call ms:reqsume POST "$GRAPH/me/events/{id}/tentativelyAccept" --body '{"sendResponse":false}'
```
**Expected:** 202 for each.

### CAL-W-7 — Find meeting times (Microsoft)
**Purpose:** Ask Graph to suggest slots.
**Handle:** `ms:<label>`
**Scopes:** `Calendars.ReadWrite`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/me/findMeetingTimes" --body '{
  "attendees":[{"emailAddress":{"address":"x@y.com"}}],
  "timeConstraint":{"timeSlots":[{"start":{"dateTime":"2026-05-01T09:00:00","timeZone":"UTC"},"end":{"dateTime":"2026-05-01T17:00:00","timeZone":"UTC"}}]},
  "meetingDuration":"PT30M"
}'
```
**Expected:** 200; `{ meetingTimeSuggestions: [...], emptySuggestionsReason? }`.

### CAL-W-8 — Free/busy lookup (Microsoft)
**Purpose:** Raw availability for a set of users.
**Handle:** `ms:<label>`
**Scopes:** `Calendars.Read.Shared` or `Calendars.Read`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/me/calendar/getSchedule" --body '{
  "schedules":["shaama@reqsume.com"],
  "startTime":{"dateTime":"2026-05-01T09:00:00","timeZone":"UTC"},
  "endTime":{"dateTime":"2026-05-01T17:00:00","timeZone":"UTC"},
  "availabilityViewInterval":30
}'
```
**Expected:** 200; `{ value: [{scheduleId, availabilityView, scheduleItems[]}, ...] }`.

### CAL-W-9 — Create event (Google)
**Purpose:** New calendar event.
**Handle:** `google:<label>`
**Scopes:** `calendar`
**Call:**
```bash
apl call google:muthu POST "$CAL/calendars/primary/events" --body '{
  "summary":"Sync",
  "start":{"dateTime":"2026-05-01T10:00:00+05:30"},
  "end":{"dateTime":"2026-05-01T10:30:00+05:30"},
  "attendees":[{"email":"x@y.com"}]
}'
```
**Expected:** 200; event object with `id`, `htmlLink`.

### CAL-W-10 — Create event with Meet link (Google)
**Purpose:** Auto-provision Meet.
**Handle:** `google:<label>`
**Scopes:** `calendar`
**Call:**
```bash
apl call google:muthu POST "$CAL/calendars/primary/events?conferenceDataVersion=1" --body '{
  "summary":"Meet sync",
  "start":{"dateTime":"2026-05-01T10:00:00+05:30"},
  "end":{"dateTime":"2026-05-01T10:30:00+05:30"},
  "conferenceData":{"createRequest":{"requestId":"apl-'$(date +%s)'","conferenceSolutionKey":{"type":"hangoutsMeet"}}}
}'
```
**Expected:** 200; event includes `hangoutLink` + `conferenceData.entryPoints[]`.
**Gotchas:** `conferenceDataVersion=1` is **required** as a query param. `requestId` must be unique per request (use timestamp or UUID).

### CAL-W-11 — Update event / RSVP (Google)
**Purpose:** Patch or respond to invite.
**Handle:** `google:<label>`
**Scopes:** `calendar`
**Call:**
```bash
# RSVP (responseStatus values: "accepted", "declined", "tentative", "needsAction"):
apl call google:muthu PATCH "$CAL/calendars/primary/events/{eventId}" --body '{
  "attendees":[{"email":"me@example.com","responseStatus":"accepted"}]
}'
```
**Expected:** 200.
**Gotchas:** PATCH replaces the attendees array fully — include every attendee (with current responseStatus) to avoid removing others.

### CAL-W-12 — Delete event (Google)
**Purpose:** Remove from calendar.
**Handle:** `google:<label>`
**Scopes:** `calendar`
**Call:**
```bash
apl call google:muthu DELETE "$CAL/calendars/primary/events/{eventId}"
```
**Expected:** 204.

### CAL-W-13 — quickAdd natural language (Google)
**Purpose:** Parse free-form text into an event.
**Handle:** `google:<label>`
**Scopes:** `calendar`
**Call:**
```bash
apl call google:muthu POST "$CAL/calendars/primary/events/quickAdd?text=Lunch%20tomorrow%2012:30"
```
**Expected:** 200; newly-created event.
**Gotchas:** URL-encode the text. Timezone is inferred from the calendar's default.

### CAL-W-14 — Free/busy lookup (Google)
**Purpose:** Availability for multiple calendars.
**Handle:** `google:<label>`
**Scopes:** `calendar` or `calendar.readonly`
**Call:**
```bash
apl call google:muthu POST "$CAL/freeBusy" --body '{
  "timeMin":"2026-05-01T09:00:00Z",
  "timeMax":"2026-05-01T17:00:00Z",
  "items":[{"id":"muthu@example.com"},{"id":"shaama@reqsume.com"}]
}'
```
**Expected:** 200; `{ calendars: {email: {busy: [{start,end}, ...]}} }`.

---

## Family 6 — Teams chat + online meetings chat (13 recipes)

### CHAT-1 — List all my chats (Microsoft)
**Purpose:** Every chat the user is a member of.
**Handle:** `ms:<label>`
**Scopes:** `Chat.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/chats?\$expand=members&\$top=50"
```
**Expected:** 200; `{ value: [{id, chatType, topic?, members}, ...] }`. `chatType` is `oneOnOne`, `group`, or `meeting`.

### CHAT-2 — Find a 1:1 chat with X (Microsoft)
**Purpose:** Locate the 1:1 chat with a specific user.
**Handle:** `ms:<label>`
**Scopes:** `Chat.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/chats?\$filter=chatType%20eq%20'oneOnOne'&\$expand=members"
# Then client-side match: members[].email == 'x@y.com'
```
**Expected:** 200.
**Gotchas:** No server-side filter by member email — you must list and grep.

### CHAT-3 — Find a group chat by topic (Microsoft)
**Purpose:** Named group chats.
**Handle:** `ms:<label>`
**Scopes:** `Chat.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/chats?\$filter=chatType%20eq%20'group'"
# Client-side: topic contains 'X'
```
**Expected:** 200.

### CHAT-4 — Find meeting chats (Microsoft)
**Purpose:** Chat threads attached to Teams meetings.
**Handle:** `ms:<label>`
**Scopes:** `Chat.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/chats?\$filter=chatType%20eq%20'meeting'"
```
**Expected:** 200; chats where `id` is like `19:meeting_...@thread.v2`.

### CHAT-5 — Messages in a chat (Microsoft)
**Purpose:** Paginated message list.
**Handle:** `ms:<label>`
**Scopes:** `Chat.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/chats/{chatId}/messages?\$top=50"
```
**Expected:** 200; `{ value: [...], @odata.nextLink? }`. System events appear as `messageType=systemEventMessage` with populated `eventDetail`.
**Gotchas:** Use the top-level `/chats/{id}/messages` path — `eventDetail` is populated there but often empty via `/me/chats/{id}/messages`.

### CHAT-6 — New messages since last poll (Microsoft, delta)
**Purpose:** Delta for a single chat.
**Handle:** `ms:<label>`
**Scopes:** `Chat.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/chats/{chatId}/messages/delta"
# Persist @odata.deltaLink from the final page; reuse next time.
```
**Expected:** 200 paginated.

### CHAT-7 — All new messages across every chat (Microsoft, preview)
**Purpose:** Firehose of chat activity for the user.
**Handle:** `ms:<label>`
**Scopes:** `Chat.Read` or `ChatMessage.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/chats/getAllMessages"
```
**Expected:** 200; paginated feed.
**Gotchas:** Preview endpoint — watch for schema changes. Use filters `$filter=lastModifiedDateTime ge 2026-01-01T00:00:00Z` to bound the window.

### CHAT-8 — Send text to a chat (Microsoft)
**Purpose:** Plain-text chat message.
**Handle:** `ms:<label>`
**Scopes:** `ChatMessage.Send`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/chats/{chatId}/messages" --body '{"body":{"content":"hello"}}'
```
**Expected:** 201; the posted message object.

### CHAT-9 — Send HTML to a chat (Microsoft)
**Purpose:** Rich-text (bold, links).
**Handle:** `ms:<label>`
**Scopes:** `ChatMessage.Send`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/chats/{chatId}/messages" --body '{
  "body":{"contentType":"html","content":"<b>deploy</b> finished"}
}'
```
**Expected:** 201.

### CHAT-10 — React to a message (Microsoft)
**Purpose:** Add emoji reaction.
**Handle:** `ms:<label>`
**Scopes:** `ChatMessage.Send`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/chats/{cid}/messages/{mid}/setReaction" --body '{"reactionType":"like"}'
```
**Expected:** 204.
**Gotchas:** Valid values: `like`, `heart`, `laugh`, `surprised`, `sad`, `angry`.

### CHAT-11 — Remove reaction (Microsoft)
**Purpose:** Undo reaction.
**Handle:** `ms:<label>`
**Scopes:** `ChatMessage.Send`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/chats/{cid}/messages/{mid}/unsetReaction" --body '{"reactionType":"like"}'
```
**Expected:** 204.

### CHAT-12 — Reply to a message (Microsoft)
**Purpose:** Thread-scoped reply.
**Handle:** `ms:<label>`
**Scopes:** `ChatMessage.Send`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/chats/{cid}/messages/{mid}/replies" --body '{"body":{"content":"acknowledged"}}'
```
**Expected:** 201.

### CHAT-13 — Edit a message (Microsoft)
**Purpose:** Update content of own message.
**Handle:** `ms:<label>`
**Scopes:** `ChatMessage.Send`
**Call:**
```bash
apl call ms:reqsume PATCH "$GRAPH/chats/{cid}/messages/{mid}" --body '{"body":{"content":"edited"}}'
```
**Expected:** 204.

### CHAT-14 — Delete a message (Microsoft)
**Purpose:** Soft-delete own message.
**Handle:** `ms:<label>`
**Scopes:** `ChatMessage.Send`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/chats/{cid}/messages/{mid}/softDelete"
```
**Expected:** 204.
**Gotchas:** Hard-delete (`DELETE`) only works for admins; most users should use `softDelete`.

### CHAT-15 — Start a new 1:1 chat (Microsoft)
**Purpose:** Create a fresh 1:1 chat before posting.
**Handle:** `ms:<label>`
**Scopes:** `Chat.Create`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/chats" --body '{
  "chatType":"oneOnOne",
  "members":[
    {"@odata.type":"#microsoft.graph.aadUserConversationMember","roles":["owner"],"user@odata.bind":"https://graph.microsoft.com/v1.0/users('<my-id>')"},
    {"@odata.type":"#microsoft.graph.aadUserConversationMember","roles":["owner"],"user@odata.bind":"https://graph.microsoft.com/v1.0/users('<their-id>')"}
  ]
}'
```
**Expected:** 201; `{ id, chatType: "oneOnOne", ... }`. Then POST messages via CHAT-8.
**Gotchas:** Both members must be `owner` role for a 1:1. If a 1:1 already exists Graph returns the existing chat rather than duplicating — idempotent.

### CHAT-16 — Send to a Teams channel (Microsoft)
**Purpose:** Post to a Team channel (not a chat).
**Handle:** `ms:<label>`
**Scopes:** `ChannelMessage.Send`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/teams/{teamId}/channels/{channelId}/messages" --body '{
  "body":{"content":"hi channel"}
}'
```
**Expected:** 201.
**Gotchas:** `ChannelMessage.Send` is separate from `ChatMessage.Send`. Channels live under Teams, chats do not.

---

## Family 7 — Online meetings, recordings, transcripts (10 recipes)

### MEET-1 — List online meetings in calendar (Microsoft)
**Purpose:** Filter calendar for online meetings.
**Handle:** `ms:<label>`
**Scopes:** `Calendars.Read`
**Call:** See CAL-R-4.

### MEET-2 — Resolve meeting by joinWebUrl (Microsoft)
**Purpose:** Get `onlineMeeting` object from a join URL.
**Handle:** `ms:<label>`
**Scopes:** `OnlineMeetings.Read`
**Call:**
```bash
# Take joinUrl from a calendar event; URL-encode it as $filter literal:
ENC=$(python3 -c "import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1],safe=''))" "<joinUrl>")
apl call ms:reqsume GET "$GRAPH/me/onlineMeetings?\$filter=JoinWebUrl%20eq%20'$ENC'"
```
**Expected:** 200; `{ value: [{id, joinWebUrl, chatInfo, participants, ...}] }`.
**Gotchas:** **Only** `JoinWebUrl` and `VideoTeleconferenceId` are supported `$filter` fields — other OData operators (`contains`, `startswith`) fail silently or 400. You cannot list all meetings — always look up by join URL.

### MEET-3 — Get meeting metadata (Microsoft)
**Purpose:** Full meeting record by id.
**Handle:** `ms:<label>`
**Scopes:** `OnlineMeetings.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/onlineMeetings/{meetingId}"
```
**Expected:** 200.

### MEET-4 — Meeting chat id (chatInfo.threadId) (Microsoft)
**Purpose:** Find the chat attached to a meeting.
**Handle:** `ms:<label>`
**Scopes:** `OnlineMeetings.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/onlineMeetings/{meetingId}?\$select=chatInfo"
```
**Expected:** 200; `{ chatInfo: { threadId: "19:meeting_...@thread.v2", ... } }`. Use `threadId` as the chatId for CHAT-5.

### MEET-5 — List recordings metadata (Microsoft)
**Purpose:** Recording objects for a meeting.
**Handle:** `ms:<label>`
**Scopes:** `OnlineMeetingRecording.Read.All` (admin consent)
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/onlineMeetings/{meetingId}/recordings"
```
**Expected:** 200; `{ value: [{id, createdDateTime, meetingOrganizer, recordingContentUrl}, ...] }`.
**Gotchas:** Requires admin consent. `recordingContentUrl` is **not** a direct download link.

### MEET-6 — List transcripts metadata (Microsoft)
**Purpose:** Transcript objects.
**Handle:** `ms:<label>`
**Scopes:** `OnlineMeetingTranscript.Read.All` (admin consent)
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/onlineMeetings/{meetingId}/transcripts"
```
**Expected:** 200.

### MEET-7 — Download transcript VTT (Microsoft)
**Purpose:** Speaker-tagged VTT text.
**Handle:** `ms:<label>`
**Scopes:** `OnlineMeetingTranscript.Read.All`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/onlineMeetings/{meetingId}/transcripts/{transcriptId}/content?\$format=text/vtt" -o transcript.vtt
```
**Expected:** 200 text/vtt body; verified ~93 KB for a 90-minute meeting.
**Gotchas:** Default format is IMDN — use `$format=text/vtt` for speaker tags.

### MEET-8 — Download recording via `/content` (Microsoft, fragile)
**Purpose:** Binary mp4 from the meeting-recording endpoint.
**Handle:** `ms:<label>`
**Scopes:** `OnlineMeetingRecording.Read.All` + `Files.Read.All`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/onlineMeetings/{meetingId}/recordings/{recordingId}/content" -o recording.mp4
```
**Expected:** 200 mp4 bytes — **for the organizer only**.
**Gotchas:** Returns **403 accessDenied** for non-organizers even with full admin-consented scopes. Graph gates this endpoint on meeting-participant ACL, not scope. Document this and fall back to MEET-9.

### MEET-9 — Download recording via organizer's OneDrive (Microsoft, preferred)
**Purpose:** Fetch recording bypassing the `/recordings/{id}/content` ACL gate.
**Handle:** `ms:<label>`
**Scopes:** `Files.Read.All` (admin consent) + prior metadata scopes
**Call:**
```bash
# Step 1 — list the Recordings folder in the organizer's drive:
apl call ms:reqsume GET "$GRAPH/users/{organizerUpn}/drive/root:/Recordings:/children?\$select=name,id,size,webUrl,@microsoft.graph.downloadUrl"

# Step 2 — either list-and-pick, or if you already know the exact filename:
apl call ms:reqsume GET "$GRAPH/users/{organizerUpn}/drive/root:/Recordings/<url-encoded-filename>"
# Extract @microsoft.graph.downloadUrl from the response.
```
**Fallback (required for Step 3 — the downloadUrl MUST NOT carry the Bearer):**
```bash
TOKEN=$(apl login ms:reqsume)
DOWNLOAD_URL=$(curl -s -H "Authorization: Bearer $TOKEN" \
  "$GRAPH/users/{organizerUpn}/drive/root:/Recordings/<file>" \
  | python3 -c 'import json,sys;print(json.load(sys.stdin)["@microsoft.graph.downloadUrl"])')
curl -L -o recording.mp4 "$DOWNLOAD_URL"   # NO -H Authorization
```
**Expected:** 200 mp4 bytes.
**Gotchas:**
- `@microsoft.graph.downloadUrl` is time-limited (~1h) and carries `tempauth=` — SharePoint rejects it if you pass the Bearer token alongside.
- Even with `Files.Read.All` + `Sites.Read.All` + admin consent, non-organizers can 403 at download if the recording hasn't been shared explicitly with them (Teams doesn't reliably auto-share with participants).
- Use `apl call` for Step 1 & 2; Step 3 MUST use curl without auth.

### MEET-10 — Download recording via chat iteration (Microsoft, discovery)
**Purpose:** Find recording URL when only the meeting id is known.
**Handle:** `ms:<label>`
**Scopes:** `Chat.Read` + `Files.Read.All`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/chats/{chatId}/messages?\$top=50"
# Client-side: scan for messageType=="systemEventMessage" AND eventDetail.@odata.type=="#microsoft.graph.callRecordingEventMessageDetail". Extract eventDetail.callRecordingUrl.
# Parse the id= query param of callRecordingUrl to reconstruct /users/{upn}/drive/root:/Recordings/<file>.mp4, then MEET-9 Step 2–3.
```
**Expected:** 200 followed by the resolved drive-item fetch.
**Gotchas:** `OnlineMeetingRecording.Read.All` is NOT required on this path — the chat-message path bypasses the meeting-recording authorization gate.

### MEET-11 — Create ad-hoc online meeting (Microsoft)
**Purpose:** Provision a Teams meeting without a calendar event.
**Handle:** `ms:<label>`
**Scopes:** `OnlineMeetings.ReadWrite`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/me/onlineMeetings" --body '{
  "subject":"Quick sync",
  "startDateTime":"2026-05-01T09:00:00Z",
  "endDateTime":"2026-05-01T09:30:00Z"
}'
```
**Expected:** 201; `{ id, joinWebUrl, ... }`.

### MEET-12 — Known 403 wall documentation (reference, not an API call)
**Purpose:** Capture the expected failure so agents recognise it.
**Call:** N/A — a `403 accessDenied` on MEET-8 or MEET-9 Step 3 means the recording has not been shared with the caller. Remediation: ask the organizer to share via Teams Recording chat → Share, or run with app-only `Files.Read.All` role (out of v1 scope).

---

## Family 8 — Drive / OneDrive / SharePoint (14 recipes)

### DRIVE-1 — Recent files (Microsoft, OneDrive)
**Purpose:** Most-recently-accessed items.
**Handle:** `ms:<label>`
**Scopes:** `Files.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/drive/recent"
```
**Expected:** 200.

### DRIVE-2 — Shared with me (Microsoft, OneDrive)
**Purpose:** Items others have shared.
**Handle:** `ms:<label>`
**Scopes:** `Files.Read.All`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/drive/sharedWithMe"
```
**Expected:** 200.

### DRIVE-3 — Search drive (Microsoft, OneDrive)
**Purpose:** Search by name/content.
**Handle:** `ms:<label>`
**Scopes:** `Files.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/drive/root/search(q='kickoff')"
```
**Expected:** 200.

### DRIVE-4 — List folder children (Microsoft, OneDrive)
**Purpose:** Contents of a named folder.
**Handle:** `ms:<label>`
**Scopes:** `Files.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/drive/root:/Documents:/children"
```
**Expected:** 200.
**Gotchas:** URL-encode each segment individually when folders contain spaces: `/Meet%20Recordings:/children`.

### DRIVE-5 — Download non-native file (Microsoft, OneDrive)
**Purpose:** Binary file bytes.
**Handle:** `ms:<label>`
**Scopes:** `Files.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/drive/items/{itemId}/content" -o file.bin
```
**Expected:** 200 with redirects — follow them (apl call follows redirects by default, or use curl -L).
**Gotchas:** 302s to a pre-authenticated storage URL — the response header `Location` carries the final URL. Prefer getting `@microsoft.graph.downloadUrl` from the item metadata and curl'ing that without auth (see MEET-9).

### DRIVE-6 — Single item metadata (Microsoft, OneDrive)
**Purpose:** Item properties + download URL.
**Handle:** `ms:<label>`
**Scopes:** `Files.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/drives/{driveId}/items/{itemId}"
```
**Expected:** 200; `{ id, name, size, webUrl, @microsoft.graph.downloadUrl? }`.

### DRIVE-7 — Create folder (Microsoft, OneDrive)
**Purpose:** New folder under drive root.
**Handle:** `ms:<label>`
**Scopes:** `Files.ReadWrite`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/me/drive/root/children" --body '{
  "name":"New",
  "folder":{},
  "@microsoft.graph.conflictBehavior":"rename"
}'
```
**Expected:** 201.

### DRIVE-8 — Upload small file (Microsoft, OneDrive, <4MB)
**Purpose:** Direct upload.
**Handle:** `ms:<label>`
**Scopes:** `Files.ReadWrite`
**Fallback (apl call does not stream raw bytes as body):**
```bash
TOKEN=$(apl login ms:reqsume)
curl -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: text/plain" \
  --data-binary @./hello.txt \
  -X PUT "$GRAPH/me/drive/root:/hello.txt:/content"
```
**Expected:** 201 or 200; driveItem.
**Gotchas:** For files >4MB use an upload session (`POST .../createUploadSession`) — out of v1 scope.

### DRIVE-9 — Share a file (Microsoft, OneDrive)
**Purpose:** Create a sharing link.
**Handle:** `ms:<label>`
**Scopes:** `Files.ReadWrite`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/me/drive/items/{id}/createLink" --body '{
  "type":"view",
  "scope":"organization"
}'
```
**Expected:** 200; `{ link: { webUrl, ... } }`.

### DRIVE-10 — List permissions (Microsoft, OneDrive)
**Purpose:** Who has access to a file.
**Handle:** `ms:<label>`
**Scopes:** `Files.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/drive/items/{id}/permissions"
```
**Expected:** 200.

### DRIVE-11 — OneDrive delta (Microsoft)
**Purpose:** Changes since last sync.
**Handle:** `ms:<label>`
**Scopes:** `Files.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/drive/root/delta"
# Later: GET <saved @odata.deltaLink>
```
**Expected:** 200 paginated.

### DRIVE-12 — SharePoint site root (Microsoft)
**Purpose:** SharePoint tenant root site.
**Handle:** `ms:<label>`
**Scopes:** `Sites.Read.All`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/sites/root"
# Or by hostname: $GRAPH/sites/{tenant}.sharepoint.com:/sites/{site}
```
**Expected:** 200; site metadata.
**Gotchas:** Admin consent required for `Sites.Read.All`.

### DRIVE-13 — Recent files (Google Drive)
**Purpose:** Most-recently-modified files.
**Handle:** `google:<label>`
**Scopes:** `drive.readonly`
**Call:**
```bash
apl call google:muthu GET "$DRIVE/files?pageSize=20&orderBy=modifiedTime%20desc&fields=files(id,name,mimeType,size,modifiedTime,webViewLink,owners)"
```
**Expected:** 200; `{ files: [...], nextPageToken? }`.
**Gotchas:** Without `fields=`, only `id,name,mimeType` are returned.

### DRIVE-14 — Search by name (Google Drive)
**Purpose:** `name contains 'X'`.
**Handle:** `google:<label>`
**Scopes:** `drive.readonly`
**Call:**
```bash
Q=$(python3 -c "import urllib.parse;print(urllib.parse.quote(\"name contains 'budget'\"))")
apl call google:muthu GET "$DRIVE/files?q=$Q&pageSize=20&fields=files(id,name,mimeType,size,webViewLink)"
```
**Expected:** 200.

### DRIVE-15 — Search by MIME type (Google Drive)
**Purpose:** Videos, PDFs, etc.
**Handle:** `google:<label>`
**Scopes:** `drive.readonly`
**Call:**
```bash
Q=$(python3 -c "import urllib.parse;print(urllib.parse.quote(\"mimeType='video/mp4'\"))")
apl call google:muthu GET "$DRIVE/files?q=$Q&pageSize=20&fields=files(id,name,size)"
```
**Expected:** 200.
**Gotchas:** Use single quotes in `q=` for string values. Valid `q=` operators: `name = 'x'`, `name contains 'x'`, `fullText contains 'x'`, `mimeType = '...'`, `'<folderId>' in parents`, `sharedWithMe`, `starred = true`, `trashed = false`, `modifiedTime > '2026-04-01T00:00:00'`, `owners in 'me'`, combine with `and`/`or`.

### DRIVE-16 — Shared with me (Google Drive)
**Purpose:** Files shared by others.
**Handle:** `google:<label>`
**Scopes:** `drive.readonly`
**Call:**
```bash
apl call google:muthu GET "$DRIVE/files?q=sharedWithMe&pageSize=50&fields=files(id,name,mimeType,owners,sharingUser)"
```
**Expected:** 200.

### DRIVE-17 — List folders only (Google Drive)
**Purpose:** Just folders.
**Handle:** `google:<label>`
**Scopes:** `drive.readonly`
**Call:**
```bash
Q=$(python3 -c "import urllib.parse;print(urllib.parse.quote(\"mimeType='application/vnd.google-apps.folder'\"))")
apl call google:muthu GET "$DRIVE/files?q=$Q"
```
**Expected:** 200.

### DRIVE-18 — Children of a folder (Google Drive)
**Purpose:** List contents.
**Handle:** `google:<label>`
**Scopes:** `drive.readonly`
**Call:**
```bash
Q=$(python3 -c "import urllib.parse;print(urllib.parse.quote(\"'<folderId>' in parents and trashed=false\"))")
apl call google:muthu GET "$DRIVE/files?q=$Q"
```
**Expected:** 200.

### DRIVE-19 — Download non-native file (Google Drive)
**Purpose:** Binary bytes (PDFs, mp4s, zips, images).
**Handle:** `google:<label>`
**Scopes:** `drive.readonly`
**Call:**
```bash
apl call google:muthu GET "$DRIVE/files/{id}?alt=media" -o file.bin
```
**Expected:** 200 via one or more 302 redirects to a signed storage URL.
**Gotchas:** `alt=media` returns **403** for native Google files (Docs, Sheets, Slides, Forms) — use the export endpoint instead (see DRIVE-20 onwards).

### DRIVE-20 — Export Google Doc to PDF (Google Drive)
**Purpose:** Convert a Google Doc to PDF bytes.
**Handle:** `google:<label>`
**Scopes:** `drive.readonly`
**Call:**
```bash
apl call google:muthu GET "$DRIVE/files/{id}/export?mimeType=application/pdf" -o doc.pdf
```
**Expected:** 200 application/pdf.

### DRIVE-21 — Export Google Sheet to xlsx (Google Drive)
**Purpose:** Convert a Sheet to Excel.
**Handle:** `google:<label>`
**Scopes:** `drive.readonly`
**Call:**
```bash
apl call google:muthu GET "$DRIVE/files/{id}/export?mimeType=application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" -o sheet.xlsx
```
**Expected:** 200.

### DRIVE-22 — Export Google Slides to pptx (Google Drive)
**Purpose:** Convert Slides to PowerPoint.
**Handle:** `google:<label>`
**Scopes:** `drive.readonly`
**Call:**
```bash
apl call google:muthu GET "$DRIVE/files/{id}/export?mimeType=application/vnd.openxmlformats-officedocument.presentationml.presentation" -o slides.pptx
```
**Expected:** 200.

### DRIVE-23 — Single file metadata (Google Drive)
**Purpose:** Item metadata.
**Handle:** `google:<label>`
**Scopes:** `drive.readonly`
**Call:**
```bash
apl call google:muthu GET "$DRIVE/files/{id}?fields=id,name,mimeType,size,webViewLink,owners,permissions"
```
**Expected:** 200.

### DRIVE-24 — List permissions (Google Drive)
**Purpose:** Who has access.
**Handle:** `google:<label>`
**Scopes:** `drive.readonly`
**Call:**
```bash
apl call google:muthu GET "$DRIVE/files/{id}/permissions"
```
**Expected:** 200; `{ permissions: [{id, type, role, emailAddress?, domain?}, ...] }`.

### DRIVE-25 — Share a file (Google Drive)
**Purpose:** Grant access to a user.
**Handle:** `google:<label>`
**Scopes:** `drive` (read/write)
**Call:**
```bash
apl call google:muthu POST "$DRIVE/files/{id}/permissions" --body '{
  "role":"reader",
  "type":"user",
  "emailAddress":"x@y.com"
}'
```
**Expected:** 200; created permission.
**Gotchas:** `drive.readonly` default scope in `apl setup google` does NOT allow permission writes — re-login with `--force --scope drive` if needed.

### DRIVE-26 — Create folder (Google Drive)
**Purpose:** New folder.
**Handle:** `google:<label>`
**Scopes:** `drive`
**Call:**
```bash
apl call google:muthu POST "$DRIVE/files" --body '{
  "name":"apl-inbox",
  "mimeType":"application/vnd.google-apps.folder"
}'
```
**Expected:** 200; folder file object.

### DRIVE-27 — Drive changes feed (Google Drive, delta)
**Purpose:** Incremental sync.
**Handle:** `google:<label>`
**Scopes:** `drive.readonly`
**Call:**
```bash
# Bootstrap:
apl call google:muthu GET "$DRIVE/changes/startPageToken"
# Persist .startPageToken, then:
apl call google:muthu GET "$DRIVE/changes?pageToken=<token>"
```
**Expected:** 200; `{ changes: [...], newStartPageToken?, nextPageToken? }`.

---

## Family 9 — Contacts / People (5 recipes)

### CONT-1 — My contacts (Microsoft)
**Purpose:** Contact folder entries.
**Handle:** `ms:<label>`
**Scopes:** `Contacts.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/contacts?\$top=50"
```
**Expected:** 200; `{ value: [{id, displayName, emailAddresses, ...}, ...] }`.

### CONT-2 — Contact folders (Microsoft)
**Purpose:** List folders.
**Handle:** `ms:<label>`
**Scopes:** `Contacts.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/contactFolders"
```
**Expected:** 200.

### CONT-3 — Create contact (Microsoft)
**Purpose:** Save a new contact.
**Handle:** `ms:<label>`
**Scopes:** `Contacts.ReadWrite`
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/me/contacts" --body '{
  "givenName":"Shaama",
  "surname":"Manoharan",
  "emailAddresses":[{"address":"shaama@reqsume.com","name":"Shaama"}]
}'
```
**Expected:** 201.

### CONT-4 — My connections (Google)
**Purpose:** Saved contacts.
**Handle:** `google:<label>`
**Scopes:** `contacts.readonly`
**Call:**
```bash
apl call google:muthu GET "$PEOPLE/people/me/connections?personFields=names,emailAddresses,phoneNumbers&pageSize=100"
```
**Expected:** 200; `{ connections: [...], nextPageToken?, totalPeople }`.
**Gotchas:** `personFields` required.

### CONT-5 — Search contacts (Google)
**Purpose:** Fuzzy search.
**Handle:** `google:<label>`
**Scopes:** `contacts.readonly`
**Call:**
```bash
apl call google:muthu GET "$PEOPLE/people:searchContacts?query=shaama&readMask=names,emailAddresses"
```
**Expected:** 200; `{ results: [{person: {...}}, ...] }`.

### CONT-6 — Other contacts (Google, auto-populated)
**Purpose:** Gmail-auto-added contacts.
**Handle:** `google:<label>`
**Scopes:** `contacts.other.readonly`
**Call:**
```bash
apl call google:muthu GET "$PEOPLE/otherContacts?readMask=names,emailAddresses&pageSize=100"
```
**Expected:** 200.
**Gotchas:** Note `readMask` (not `personFields`) on this endpoint. Separate scope from `contacts.readonly`.

### CONT-7 — Directory people (Google Workspace)
**Purpose:** Domain directory.
**Handle:** `google:<label>`
**Scopes:** `directory.readonly`
**Call:**
```bash
apl call google:muthu GET "$PEOPLE/people:listDirectoryPeople?sources=DIRECTORY_SOURCE_TYPE_DOMAIN_PROFILE&readMask=names,emailAddresses&pageSize=50"
```
**Expected:** 200.
**Gotchas:** Workspace-only; personal Google accounts 403.

### CONT-8 — Create contact (Google)
**Purpose:** Save a new contact.
**Handle:** `google:<label>`
**Scopes:** `contacts` (read/write)
**Call:**
```bash
apl call google:muthu POST "$PEOPLE/people:createContact" --body '{
  "names":[{"givenName":"Shaama"}],
  "emailAddresses":[{"value":"shaama@reqsume.com"}]
}'
```
**Expected:** 200; created person resource.

---

## Family 10 — Sync / delta (5 recipes)

(Most delta recipes are cross-referenced from their parent families; listed here for discovery.)

### SYNC-1 — Gmail history since ID
See MAIL-R-26.

### SYNC-2 — Google Calendar syncToken
**Purpose:** Cheap "what changed".
**Handle:** `google:<label>`
**Scopes:** `calendar.readonly`
**Call:**
```bash
# First call captures nextSyncToken:
apl call google:muthu GET "$CAL/calendars/primary/events?maxResults=250&singleEvents=true&showDeleted=true"
# Subsequent:
apl call google:muthu GET "$CAL/calendars/primary/events?syncToken=<saved>"
```
**Expected:** 200.
**Gotchas:** A `410 GONE` means the syncToken expired — bootstrap a fresh full sync. `showDeleted=true` is required on subsequent calls to see cancellations.

### SYNC-3 — MS Mail delta
See MAIL-R-15.

### SYNC-4 — MS Events delta
**Purpose:** Calendar changes.
**Handle:** `ms:<label>`
**Scopes:** `Calendars.Read`
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/me/calendarView/delta?startDateTime=2026-01-01T00:00:00Z&endDateTime=2026-12-31T00:00:00Z"
# Subsequent: GET <saved @odata.deltaLink>
```
**Expected:** 200 paginated.

### SYNC-5 — Drive v3 changes feed
See DRIVE-27.

### SYNC-6 — OneDrive delta
See DRIVE-11.

### SYNC-7 — Directory users delta (Microsoft)
**Purpose:** Changes to tenant user directory.
**Handle:** `ms:<label>`
**Scopes:** `User.Read.All` (admin consent)
**Call:**
```bash
apl call ms:reqsume GET "$GRAPH/users/delta?\$select=displayName,mail,jobTitle"
```
**Expected:** 200.

---

## Family 11 — Advanced (5 recipes)

### ADV-1 — $batch (Microsoft)
**Purpose:** Up to 20 sub-requests in one POST.
**Handle:** `ms:<label>`
**Scopes:** union of sub-request scopes.
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/\$batch" --body '{
  "requests":[
    {"id":"1","method":"GET","url":"/me"},
    {"id":"2","method":"GET","url":"/me/messages?$top=5&$select=subject"},
    {"id":"3","method":"GET","url":"/me/calendarView?startDateTime=2026-04-23T00:00:00Z&endDateTime=2026-04-24T00:00:00Z"}
  ]
}'
```
**Expected:** 200; `{ responses: [{id, status, body}, ...] }` keyed by your sub-request ids.
**Gotchas:** Sub-request URLs are relative to `/v1.0` — do NOT include the host or `/v1.0` prefix. Max 20 per batch.

### ADV-2 — Gmail batch (Google, multipart)
**Purpose:** Multiple Gmail calls in one HTTP round-trip.
**Handle:** `google:<label>`
**Scopes:** union.
**Fallback (apl call does not construct multipart/mixed bodies):**
```bash
TOKEN=$(apl login google:muthu)
curl -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: multipart/mixed; boundary=batch_apl" \
  --data-binary @batch.body \
  -X POST "https://www.googleapis.com/batch/gmail/v1"
# batch.body is an RFC-1341 multipart document where each part is a full HTTP request with its own headers.
```
**Expected:** 200 multipart response, one part per sub-request.
**Gotchas:** Google also exposes `batch/calendar/v3` and `batch/drive/v3` — same format. Only worthwhile for bulk reads.

### ADV-3 — Graph webhook subscription (Microsoft)
**Purpose:** Push notifications for a resource.
**Handle:** `ms:<label>`
**Scopes:** resource-dependent (e.g. `Mail.Read` for `/me/mailFolders/inbox/messages`).
**Call:**
```bash
apl call ms:reqsume POST "$GRAPH/subscriptions" --body '{
  "changeType":"created,updated",
  "notificationUrl":"https://your-endpoint.example.com/graph",
  "resource":"me/mailFolders/inbox/messages",
  "expirationDateTime":"2026-05-01T00:00:00Z",
  "clientState":"secret-nonce"
}'
```
**Expected:** 201; `{ id, expirationDateTime, resource, ... }`.
**Gotchas:** Graph does a validation handshake: your `notificationUrl` must be publicly reachable and echo the `validationToken` query param within 10s. Max expiration varies by resource (1h for chat messages, 3d for mail). Renew with PATCH on `/subscriptions/{id}` before expiry.

### ADV-4 — Gmail watch (push via Pub/Sub)
**Purpose:** Gmail realtime notifications to a GCP Pub/Sub topic.
**Handle:** `google:<label>`
**Scopes:** `gmail.readonly` (or `gmail.modify`)
**Call:**
```bash
apl call google:muthu POST "$GMAIL/watch" --body '{
  "topicName":"projects/<gcp-project>/topics/gmail",
  "labelIds":["INBOX"]
}'
```
**Expected:** 200; `{ historyId, expiration }`.
**Gotchas:** The Pub/Sub topic must grant `gmail-api-push@system.gserviceaccount.com` the Pub/Sub Publisher role. Watch expires after 7 days — renew on cadence.

### ADV-5 — Google Calendar watch (webhook)
**Purpose:** Webhook notifications for calendar changes.
**Handle:** `google:<label>`
**Scopes:** `calendar.readonly`
**Call:**
```bash
apl call google:muthu POST "$CAL/calendars/primary/events/watch" --body '{
  "id":"apl-channel-'$(uuidgen)'",
  "type":"web_hook",
  "address":"https://your-endpoint.example.com/calendar"
}'
```
**Expected:** 200; channel object with `resourceId` used to stop the channel.
**Gotchas:** Your address must be HTTPS with a publicly-verifiable certificate. Stop channels with `POST /channels/stop`.

---

## Family 12 — GitHub via `gh` CLI (22 recipes)

GitHub's surface is covered by the `gh` CLI, not by `apl`. `gh` owns its own token store (`~/.config/gh/hosts.yml`) and auth flow (`gh auth login`); `apl` is not involved. These recipes follow the same per-recipe shape as earlier families, but the `Handle:` line becomes `Auth:` (since there is no apl handle) and the primary `Call:` uses `gh` directly. A `Fallback:` block is provided where the `gh` surface is thin or where the caller wants to chain the raw REST API through `apl`-style scripting — the fallback form is `gh api <endpoint>` (preferred, since `gh` injects the bearer automatically) or a raw `curl` with `--header "Authorization: Bearer $(gh auth token)"`.

**Auth model.** Every recipe assumes `gh auth status` shows a logged-in account with scopes `repo, read:org, workflow, gist` (the defaults from `gh auth login -s workflow`). If a recipe needs a scope beyond that, it says so.

**Repo detection.** `gh` auto-detects `owner/name` from the current directory's git remote. Outside a git repo, pass `--repo <owner/name>` explicitly; every recipe below notes this where relevant. The active repo can be cached in `project` state as `gh_repo` (see "State model").

**JSON output.** Every `gh <noun> list|view` recipe uses `--json <fields>` where available so the agent gets machine-parseable output. Use `--jq <expr>` to post-filter without piping into a separate `jq`.

### GH-1 — Repo info
**Purpose:** View metadata for a repo (description, default branch, visibility, stars).
**Auth:** `gh auth status` must show a logged-in account. No apl tokens involved.
**Call:**
```bash
gh repo view <owner/name> --json name,description,defaultBranchRef,visibility,stargazerCount,pushedAt,url
```
**Expected response (short):** `{ name, description, defaultBranchRef:{name}, visibility, stargazerCount, pushedAt, url }`.
**Gotchas:** Omit `<owner/name>` to use the current repo's remote. Private-repo visibility requires `repo` scope.
**Fallback:** `gh api repos/<owner/name>` or `curl --header "Authorization: Bearer $(gh auth token)" https://api.github.com/repos/<owner/name>`.

### GH-2 — List my repos
**Purpose:** Every repo the signed-in user owns or collaborates on, most-recently-pushed first.
**Auth:** `gh auth status` must show a logged-in account. No apl tokens involved.
**Call:**
```bash
gh repo list --limit 100 --json name,nameWithOwner,isPrivate,isFork,pushedAt,description --jq 'sort_by(.pushedAt) | reverse'
```
**Expected response (short):** `[{nameWithOwner, isPrivate, isFork, pushedAt, description}, ...]`.
**Gotchas:** Default `--limit` is 30 — always pass `--limit` explicitly when you want the full set. `gh repo list <org>` scopes to an org.
**Fallback:** `gh api user/repos?per_page=100&sort=pushed --paginate`.

### GH-3 — List starred repos
**Purpose:** User's starred repos for quick reference lookups.
**Auth:** `gh auth status`.
**Call:**
```bash
gh api user/starred?per_page=100 --paginate --jq '.[] | {nameWithOwner: .full_name, description, url: .html_url, pushedAt: .pushed_at}'
```
**Expected response (short):** stream of `{nameWithOwner, description, url, pushedAt}` objects.
**Gotchas:** `gh` has no first-class `repo list --starred` — hence the `gh api` form. `--paginate` walks `Link: rel="next"` transparently.
**Fallback:** N/A — this IS the fallback form.

### GH-4 — Default branch lookup
**Purpose:** Resolve a repo's default branch (needed for PR base, release targets).
**Auth:** `gh auth status`.
**Call:**
```bash
gh repo view <owner/name> --json defaultBranchRef --jq '.defaultBranchRef.name'
```
**Expected response (short):** single string, e.g. `main` or `dev`.
**Gotchas:** Deliberately not hard-coding `main` — this repo uses `dev` for reqsume/volentis. Never assume.
**Fallback:** `gh api repos/<owner/name> --jq .default_branch`.

### GH-5 — Clone by slug
**Purpose:** Clone a repo into CWD by `owner/name`.
**Auth:** `gh auth status` (uses gh's token as the git credential helper; avoids SSH-key friction).
**Call:**
```bash
gh repo clone <owner/name> [local-dir]
```
**Expected response (short):** exit 0 + new directory on disk. Stdout is git's clone progress.
**Gotchas:** `gh` configures the clone's `origin` to use HTTPS + gh's credential helper. Switch to SSH with `git remote set-url origin git@github.com:<owner/name>.git` if preferred.
**Fallback:** `git clone https://github.com/<owner/name>.git` (loses gh credential injection).

### GH-6 — PRs assigned to me
**Purpose:** Open PRs with me as assignee (ownership queue), across all repos.
**Auth:** `gh auth status`.
**Call:**
```bash
gh search prs --state=open --assignee=@me --json number,title,url,repository,author,updatedAt --limit 50
```
**Expected response (short):** `[{number, title, url, repository:{nameWithOwner}, author:{login}, updatedAt}, ...]`.
**Gotchas:** Cross-repo queries MUST use `gh search prs`, not `gh pr list` (which is single-repo). Assignment is different from review-request — see GH-7.
**Fallback:** `gh api "search/issues?q=is:pr+is:open+assignee:@me" --jq '.items[]'`.

### GH-7 — PRs needing my review
**Purpose:** PRs where I'm a requested reviewer and haven't yet reviewed.
**Auth:** `gh auth status`.
**Call:**
```bash
gh search prs --state=open --review-requested=@me --json number,title,url,repository,author,updatedAt --limit 50
```
**Expected response (short):** `[{number, title, url, repository:{nameWithOwner}, author:{login}, updatedAt}, ...]`.
**Gotchas:** Once you approve or request-changes, the PR falls off this list automatically. Team-review-requests also count.
**Fallback:** `gh api "search/issues?q=is:pr+is:open+review-requested:@me"`.

### GH-8 — My authored open PRs
**Purpose:** PRs I authored that are still open (my outbound queue).
**Auth:** `gh auth status`.
**Call:**
```bash
gh search prs --state=open --author=@me --json number,title,url,repository,isDraft,reviewDecision,updatedAt --limit 50
```
**Expected response (short):** `[{number, title, url, repository:{nameWithOwner}, isDraft, reviewDecision, updatedAt}, ...]`.
**Gotchas:** `reviewDecision` is one of `APPROVED | CHANGES_REQUESTED | REVIEW_REQUIRED | null`. Drafts have `isDraft: true` — filter client-side if you want non-drafts only.
**Fallback:** `gh api "search/issues?q=is:pr+is:open+author:@me"`.

### GH-9 — View PR with comments and checks
**Purpose:** Full PR context — body, comments, review comments, CI status — in one call tree.
**Auth:** `gh auth status`.
**Call:**
```bash
gh pr view <number-or-url> --comments --json number,title,body,state,isDraft,mergeable,reviewDecision,reviews,comments,statusCheckRollup,headRefName,baseRefName,url
```
**Expected response (short):** PR object with `reviews[]`, `comments[]`, and `statusCheckRollup[]` (one entry per check run).
**Gotchas:** `--comments` prints issue comments to stdout separately from `--json`; if you need them in JSON use the `comments` field. Review-thread comments (inline on diff) are under `reviews[].comments` — the top-level `comments` is conversation-level.
**Fallback:** `gh api "repos/<owner/name>/pulls/<n>"` + `.../pulls/<n>/reviews` + `.../issues/<n>/comments` + `.../commits/<sha>/check-runs`.

### GH-10 — Approve a PR
**Purpose:** Cast an APPROVE review.
**Auth:** `gh auth status`.
**Call:**
```bash
gh pr review <number-or-url> --approve --body "LGTM"
```
**Expected response (short):** `Approved pull request #<n>` on stdout.
**Gotchas:** Self-approval is blocked by GitHub; approving your own PR errors out. `--body` is optional but recommended.
**Fallback:** `gh api --method POST "repos/<owner/name>/pulls/<n>/reviews" -f event=APPROVE -f body="LGTM"`.

### GH-11 — Request changes on a PR
**Purpose:** Block merge with a CHANGES_REQUESTED review.
**Auth:** `gh auth status`.
**Call:**
```bash
gh pr review <number-or-url> --request-changes --body "<required explanation>"
```
**Expected response (short):** `Requested changes on pull request #<n>`.
**Gotchas:** `--body` is REQUIRED for `--request-changes` (GitHub rejects an empty body). Use `--comment` for a non-blocking review comment instead.
**Fallback:** `gh api --method POST "repos/<owner/name>/pulls/<n>/reviews" -f event=REQUEST_CHANGES -f body="..."`.

### GH-12 — Comment on a PR (non-review)
**Purpose:** Drop a conversational comment without submitting a formal review.
**Auth:** `gh auth status`.
**Call:**
```bash
gh pr comment <number-or-url> --body "your message"
```
**Expected response (short):** URL of the new comment on stdout.
**Gotchas:** For an inline review comment on a specific diff line, use `gh api` with the `pulls/<n>/comments` endpoint — `gh pr comment` only creates top-level conversation comments.
**Fallback:** `gh api --method POST "repos/<owner/name>/issues/<n>/comments" -f body="..."` (PR comments live under `/issues/`, not `/pulls/`, for non-review text).

### GH-13 — Check out a PR branch locally
**Purpose:** Fetch a PR's head branch and switch to it.
**Auth:** `gh auth status`.
**Call:**
```bash
gh pr checkout <number-or-url>
```
**Expected response (short):** Git switches to the PR branch; stdout shows `Switched to branch '<branch>'`.
**Gotchas:** Works even for forks — `gh` adds the fork as a remote transparently. Dirty working tree aborts the checkout; stash or commit first.
**Fallback:** `git fetch origin pull/<n>/head:pr-<n> && git switch pr-<n>`.

### GH-14 — Merge a PR
**Purpose:** Merge an approved PR using the configured strategy.
**Auth:** `gh auth status`.
**Call:**
```bash
gh pr merge <number-or-url> --squash --delete-branch
```
**Expected response (short):** `Merged pull request #<n>` + branch deletion confirmation.
**Gotchas:** Pass exactly one of `--merge | --squash | --rebase`. `--delete-branch` only deletes the remote branch; local stays. `--auto` queues a merge once checks pass — useful for "merge when green".
**Fallback:** `gh api --method PUT "repos/<owner/name>/pulls/<n>/merge" -f merge_method=squash`.

### GH-15 — Close PR without merging
**Purpose:** Close a PR (abandon) without merging.
**Auth:** `gh auth status`.
**Call:**
```bash
gh pr close <number-or-url> --comment "abandoning — superseded by #<other>"
```
**Expected response (short):** `Closed pull request #<n>`.
**Gotchas:** `--delete-branch` deletes the remote head branch too. Closing is reversible via `gh pr reopen <n>`.
**Fallback:** `gh api --method PATCH "repos/<owner/name>/pulls/<n>" -f state=closed`.

### GH-16 — Issues assigned to me
**Purpose:** Open issues across all repos with me as assignee.
**Auth:** `gh auth status`.
**Call:**
```bash
gh search issues --state=open --assignee=@me --json number,title,url,repository,labels,updatedAt --limit 50
```
**Expected response (short):** `[{number, title, url, repository:{nameWithOwner}, labels:[{name}], updatedAt}, ...]`.
**Gotchas:** `gh search issues` excludes PRs by default; `gh search prs` is the sibling. `gh issue list` is single-repo only.
**Fallback:** `gh api "search/issues?q=is:issue+is:open+assignee:@me"`.

### GH-17 — Issues mentioning me
**Purpose:** Open issues where I'm @-mentioned (but not necessarily assigned).
**Auth:** `gh auth status`.
**Call:**
```bash
gh search issues --state=open --mentions=@me --json number,title,url,repository,updatedAt --limit 50
```
**Expected response (short):** `[{number, title, url, repository:{nameWithOwner}, updatedAt}, ...]`.
**Gotchas:** "Mentions" is literal `@login` in the issue body or a comment. Does not include team mentions.
**Fallback:** `gh api "search/issues?q=is:issue+is:open+mentions:@me"`.

### GH-18 — My authored open issues
**Purpose:** Open issues I opened (my outbound backlog).
**Auth:** `gh auth status`.
**Call:**
```bash
gh search issues --state=open --author=@me --json number,title,url,repository,updatedAt --limit 50
```
**Expected response (short):** `[{number, title, url, repository:{nameWithOwner}, updatedAt}, ...]`.
**Gotchas:** N/A.
**Fallback:** `gh api "search/issues?q=is:issue+is:open+author:@me"`.

### GH-19 — View issue with comments
**Purpose:** Full issue context including the comment thread.
**Auth:** `gh auth status`.
**Call:**
```bash
gh issue view <number-or-url> --comments --json number,title,body,state,labels,assignees,comments,url
```
**Expected response (short):** issue object with `comments[]` array.
**Gotchas:** Works with a full URL so the agent doesn't need to separately resolve owner/repo when the user pastes a link.
**Fallback:** `gh api "repos/<owner/name>/issues/<n>"` + `.../issues/<n>/comments`.

### GH-20 — Create issue
**Purpose:** Open a new issue in a repo.
**Auth:** `gh auth status`.
**Call:**
```bash
gh issue create --title "<title>" --body "<markdown body>" [--label bug,priority-high] [--assignee @me] [--repo <owner/name>]
```
**Expected response (short):** URL of the new issue on stdout.
**Gotchas:** `--label` values must already exist in the repo; otherwise 422. `--body-file -` lets you stream body from stdin (heredoc-friendly).
**Fallback:** `gh api --method POST "repos/<owner/name>/issues" -f title="..." -f body="..."`.

### GH-21 — Close / reopen / label / comment on an issue
**Purpose:** Common mutations on an existing issue.
**Auth:** `gh auth status`.
**Call:**
```bash
gh issue close <n> --comment "done in #<pr>"
gh issue reopen <n>
gh issue edit <n> --add-label "bug" --remove-label "needs-triage"
gh issue comment <n> --body "an update"
```
**Expected response (short):** one-line confirmation per command (`Closed issue #<n>`, etc.).
**Gotchas:** `gh issue edit --add-assignee @me` is the assignment sibling. Label additions validate server-side.
**Fallback:** `gh api --method PATCH "repos/<owner/name>/issues/<n>" -f state=closed` (and sibling PATCHes for labels/assignees).

### GH-22 — Pending review requests (detail view)
**Purpose:** Same set as GH-7 but including team-review-requests and grouped by repo — useful as a "review queue" summary.
**Auth:** `gh auth status`.
**Call:**
```bash
gh search prs --state=open --review-requested=@me --json number,title,url,repository,author,reviewDecision,updatedAt --limit 100 --jq 'group_by(.repository.nameWithOwner) | map({repo: .[0].repository.nameWithOwner, prs: map({number, title, author: .author.login, updatedAt, url})})'
```
**Expected response (short):** `[{repo, prs: [{number, title, author, updatedAt, url}, ...]}, ...]`.
**Gotchas:** `--jq` expression requires jq syntax — test locally before chaining. Team requests also show up when a team you're in is the requested reviewer.
**Fallback:** GH-7 without the `--jq` grouping.

### GH-23 — Recent workflow runs
**Purpose:** Last 20 Actions runs for the current repo.
**Auth:** `gh auth status` with `workflow` scope (default on `gh auth login -s workflow`).
**Call:**
```bash
gh run list --limit 20 --json databaseId,name,displayTitle,status,conclusion,headBranch,event,createdAt,url
```
**Expected response (short):** `[{databaseId, name, displayTitle, status, conclusion, headBranch, event, createdAt, url}, ...]`.
**Gotchas:** `status` is `queued | in_progress | completed`; `conclusion` is `success | failure | cancelled | ...` and is `null` while `status != completed`. Outside a git repo pass `--repo <owner/name>`.
**Fallback:** `gh api "repos/<owner/name>/actions/runs?per_page=20"`.

### GH-24 — View run status + failing-step logs
**Purpose:** Deep-dive into a specific run, including logs for failed jobs.
**Auth:** `gh auth status`.
**Call:**
```bash
gh run view <run-id> --log-failed
```
**Expected response (short):** Run summary header on stdout, then failed-step log lines. Add `--json jobs,conclusion,headSha,url` for machine-parseable summary without logs.
**Gotchas:** `--log` prints ALL logs (can be huge); `--log-failed` is the right default for triage. Log retention is 90 days by default.
**Fallback:** `gh api "repos/<owner/name>/actions/runs/<run-id>/logs"` (returns a zip of logs — requires binary handling).

### GH-25 — Re-run a failed workflow
**Purpose:** Re-trigger a workflow run (all failed jobs, or all jobs).
**Auth:** `gh auth status` with `workflow` scope.
**Call:**
```bash
gh run rerun <run-id> --failed        # re-run only the failed jobs
gh run rerun <run-id>                 # re-run all jobs
```
**Expected response (short):** `Requested rerun of run <run-id>` on stdout.
**Gotchas:** `--failed` requires the run to be in `completed` state. For a workflow-dispatch event you may want `gh workflow run <workflow.yml>` instead (fresh run with fresh inputs), not `rerun`.
**Fallback:** `gh api --method POST "repos/<owner/name>/actions/runs/<run-id>/rerun-failed-jobs"`.

### GH-26 — Cancel a running workflow
**Purpose:** Cancel an in-progress run.
**Auth:** `gh auth status` with `workflow` scope.
**Call:**
```bash
gh run cancel <run-id>
```
**Expected response (short):** `Request to cancel workflow submitted.`
**Gotchas:** Cancellation is async — the run moves to `cancelled` after the runner acks, typically within 30s. Not all jobs may have started yet; those are skipped.
**Fallback:** `gh api --method POST "repos/<owner/name>/actions/runs/<run-id>/cancel"`.

### GH-27 — List releases / view latest
**Purpose:** Release history for a repo; get the latest release quickly.
**Auth:** `gh auth status`.
**Call:**
```bash
gh release list --limit 10 --json tagName,name,isLatest,isPrerelease,publishedAt,url
gh release view --json tagName,name,body,assets,publishedAt,url   # latest
gh release view <tag> --json tagName,name,body,assets,publishedAt,url   # specific tag
```
**Expected response (short):** release object(s) with `assets[]` each having `{name, size, url}`.
**Gotchas:** `gh release view` with no tag picks the latest non-draft release. Drafts are visible only to users with push access.
**Fallback:** `gh api "repos/<owner/name>/releases/latest"`.

### GH-28 — Create release with notes + artifacts
**Purpose:** Cut a release matching how `apl` itself ships today (tag + notes + binaries from `dist/`).
**Auth:** `gh auth status`.
**Call:**
```bash
gh release create <tag> \
  --title "<release title>" \
  --notes-file CHANGELOG.md \
  --target <branch-or-sha> \
  dist/apl-darwin-arm64 dist/apl-darwin-amd64 dist/apl-linux-amd64 dist/apl-linux-arm64 dist/apl-windows-amd64.exe
```
**Expected response (short):** Release URL on stdout; assets uploaded in the same call.
**Gotchas:** Tag is created server-side if it doesn't already exist on `--target`. For a draft release pass `--draft`; for a pre-release pass `--prerelease`. `--generate-notes` auto-generates notes from PR titles since the previous tag if you'd rather not maintain a CHANGELOG file.
**Fallback:** Two-step: `gh api --method POST "repos/<owner/name>/releases" -f tag_name=... -f name=... -f body=..."` then `gh release upload <tag> <file>...` — the single-shot form above is strictly better.

### GH-29 — Search code across repos
**Purpose:** Full-text code search across GitHub (all accessible repos).
**Auth:** `gh auth status`.
**Call:**
```bash
gh search code "<query>" --language=go --owner=<org-or-user> --limit 30 --json repository,path,url
```
**Expected response (short):** `[{repository:{nameWithOwner}, path, url}, ...]`.
**Gotchas:** `gh search code` does NOT return matching line numbers / snippets in `--json` output — the CLI prints them to stdout in human mode but they're not in the structured form. For snippets fall back to `gh api`. Rate-limited more aggressively than issue/PR search (30 req/min authenticated).
**Fallback:** `gh api "search/code?q=<query>+language:go+org:<org>" --jq '.items[] | {repo: .repository.full_name, path, text_matches}'` with header `Accept: application/vnd.github.text-match+json` to get `text_matches`.

### GH-30 — Search issues / PRs by query
**Purpose:** Free-form search across issues and PRs using GitHub's search qualifiers.
**Auth:** `gh auth status`.
**Call:**
```bash
gh search issues "<query> is:open author:@me label:bug" --json number,title,url,repository,state,updatedAt --limit 50
gh search prs    "<query> is:merged author:@me" --json number,title,url,repository,mergedAt --limit 50
```
**Expected response (short):** per-match objects with the requested fields.
**Gotchas:** Qualifiers like `is:`, `author:`, `label:`, `repo:`, `org:`, `in:title`, `created:>=2026-01-01` all work. Quote multi-word phrases inside the query. `gh search` auto-URL-encodes — do NOT pre-encode.
**Fallback:** `gh api "search/issues?q=<url-encoded-query>"`.

---

## Family 13 — Morning Brief / Daily Aggregate (8 recipes)

These are **agent-directed aggregate** recipes. Each one specifies a sequence of calls from the earlier families (2 Mail, 4 Calendar read, 6 Teams chat, 10 Delta, 12 GitHub) that the agent issues — in parallel where safe — then synthesizes into a single user-facing answer. They are not new HTTP surfaces; they are *orchestration recipes* that cite the underlying recipe IDs so the synthesis logic has a stable reference.

**Shape differs from Families 1-12.** Instead of a single `Call:` block, each recipe has a `Call sequence:` with the recipes it invokes, and a `Synthesis hint:` paragraph telling the agent how to format the combined result. The usual `apl call` / `gh` invocations are fully specified in the cited recipes — this family does not repeat them.

**Handle resolution.** "Both providers" means the agent uses whichever of `ms_handle` and `google_handle` are present in project state. If only one is configured, run only that side — do NOT prompt the user mid-brief. Missing provider is a synthesis note ("No Google account configured — Gmail skipped."), not a blocker.

**Timestamp persistence.** BRIEF-1 and BRIEF-2 write `last_brief_ts = <now, ISO-8601 UTC>` to project state on successful completion. BRIEF-2 reads it on next invocation. BRIEF-6 is stateless and takes `since` as an argument.

### BRIEF-1 — Morning brief (full)

**User intent:** "morning brief", "what's on my plate", "catch me up", "start my day", "daily standup prep".

**Call sequence (agent issues in parallel where safe):**
1. `apl call ms:$ms_handle GET ...` → unread inbox (MAIL-R-2).
2. `apl call google:$google_handle GET ...` → unread Gmail (MAIL-R-17).
3. `apl call ms:$ms_handle GET ...` → today's Microsoft events (CAL-R-1).
4. `apl call google:$google_handle GET ...` → today's Google events (CAL-R-7).
5. `apl call ms:$ms_handle GET ...` → all new Teams messages across chats since `last_brief_ts` (CHAT-7; or per-chat CHAT-6 loop if tenant lacks `/me/chats/getAllMessages`).
6. `gh search prs --state=open --assignee=@me ...` → PRs assigned (GH-6).
7. `gh search prs --state=open --review-requested=@me ...` → PRs needing review (GH-7).
8. `gh search issues --state=open --assignee=@me ...` → issues assigned (GH-16).
9. `gh search issues --state=open --mentions=@me ...` → issues mentioning me (GH-17).

On success: write `last_brief_ts = <now, UTC>` to project state.

**Synthesis hint:** Group by urgency, top-down: (a) next meeting within 1 hour (include time-to-start and Join URL if present); (b) rest of today's meetings in chronological order, deduplicated across providers by subject+start-time; (c) unread mail older than 24h flagged as "aging", newer as "fresh"; (d) PRs needing review sorted by `updatedAt` ascending (oldest wait first); (e) assignments (PRs + issues) sorted by `updatedAt` descending; (f) Teams mentions / direct messages. End with the count of items in each bucket. If a bucket is empty, say "no <bucket>" in one line rather than omitting it silently.

**Gotchas:** If `ms_handle` or `google_handle` is absent from state, skip that side and note it. CHAT-7 requires a preview scope and tenant flag; on 403 fall back to CHAT-1 → loop CHAT-6 per chat (slower, but always works). Deduplicate meetings: a Google Calendar event that's a Teams meeting will ALSO appear in Graph; match on `start.dateTime` + normalized subject.

### BRIEF-2 — What's new since last check (delta only)

**User intent:** "what's new", "what changed since I last checked", "delta", "since my last brief".

**Call sequence:**
1. Read `last_brief_ts` from project state. If absent, tell the user "no prior brief on record — run a full brief first" and stop.
2. `apl call ms:$ms_handle GET "$GRAPH/me/mailFolders/inbox/messages/delta"` → new mail (SYNC-3 / MAIL-R-15, using saved `@odata.deltaLink`).
3. `apl call google:$google_handle GET "$GMAIL/history?startHistoryId=..."` → new Gmail (SYNC-1 / MAIL-R-26).
4. `apl call ms:$ms_handle GET "$GRAPH/me/calendarView/delta?..."` → MS calendar changes (SYNC-4).
5. `apl call google:$google_handle GET "$CAL/calendars/primary/events?syncToken=..."` → Google calendar changes (SYNC-2).
6. `apl call ms:$ms_handle GET "$GRAPH/me/chats/getAllMessages?$filter=lastModifiedDateTime gt $last_brief_ts"` → new Teams messages (CHAT-7).
7. `gh search prs --state=open --review-requested=@me updated:>=$last_brief_ts` → PRs newly-requested or updated (GH-7 + qualifier).
8. `gh search issues state=open mentions:@me updated:>=$last_brief_ts` → issues newly mentioning me (GH-17 + qualifier).

On success: write `last_brief_ts = <now, UTC>`.

**Synthesis hint:** Pure delta — do NOT include items that existed before `last_brief_ts`. Format as a changelog: "2 new mails, 1 meeting moved, 1 PR review requested, 0 new mentions." Expand each bucket with specifics. Empty-overall state is a single line: "Nothing new since <last_brief_ts human-relative>."

**Gotchas:** Delta tokens for Graph mail expire after ~30 days of idleness (410). Google Calendar `syncToken` expires after ~7 days (410). Both require a bootstrap full sync on 410 — synthesis message for the user: "Delta tokens expired; re-bootstrapping." Gmail `historyId` is shorter-lived (1-7 days) and `history` returns `404` when too old.

### BRIEF-3 — Unread mail (cross-provider)

**User intent:** "unread mail", "what's in my inbox", "check my email", "any unread".

**Call sequence:**
1. `apl call ms:$ms_handle GET "$GRAPH/me/mailFolders/inbox/messages?\$filter=isRead eq false&\$top=25&\$select=subject,from,receivedDateTime"` (MAIL-R-2).
2. `apl call google:$google_handle GET "$GMAIL/messages?q=is:unread&maxResults=25"` then per-message MAIL-R-22 for headers (MAIL-R-17 + MAIL-R-22).

**Synthesis hint:** Interleave both providers sorted by received-time descending. Prefix each line with the provider tag (`[ms]` / `[gmail]`) so the user can tell which inbox it came from. Show sender + subject + human-relative time ("2h ago"). Cap the combined list at 25 items; tell the user the total unread counts across both ("25 of 137 unread").

**Gotchas:** Gmail's `q=is:unread` is label-based, not truly "unread" in the IMAP sense — archived-but-unread messages still show up. MS `isRead eq false` is strict inbox-only when scoped to `/mailFolders/inbox`. For just-the-count, use MAIL-R-3 (MS) and a `q=is:unread` count via `resultSizeEstimate` (Gmail) — cheaper.

### BRIEF-4 — Today's calendar (cross-provider)

**User intent:** "today's calendar", "what's my day look like", "meetings today", "agenda".

**Call sequence:**
1. `apl call ms:$ms_handle GET "$GRAPH/me/calendarView?startDateTime=<today-00:00Z>&endDateTime=<tomorrow-00:00Z>&\$orderby=start/dateTime&\$select=subject,start,end,location,onlineMeeting,attendees,organizer"` (CAL-R-1).
2. `apl call google:$google_handle GET "$CAL/calendars/primary/events?timeMin=<today-00:00Z>&timeMax=<tomorrow-00:00Z>&singleEvents=true&orderBy=startTime"` (CAL-R-7).

**Synthesis hint:** Merge both provider lists, sort by start time ascending. Dedupe: if subject+start match within 5 minutes across providers it's the same event — prefer the Graph version (more attendee detail). Mark the **next** event (first one with `start > now`) distinctly. Include join URL (prefer `onlineMeeting.joinUrl` for MS; `hangoutLink` / `conferenceData.entryPoints[0].uri` for Google). Empty day → "No meetings today."

**Gotchas:** `singleEvents=true` on Google expands recurring events — without it a weekly standup shows once as a recurrence master, not as today's instance. MS `calendarView` does this automatically. Time bounds MUST be UTC ISO-8601; the timezone conversion to the user's local tz is a synthesis concern, not a query concern.

### BRIEF-5 — My work queue

**User intent:** "my queue", "what am I on the hook for", "my work", "action items", "what needs me".

**Call sequence:**
1. `gh search prs --state=open --assignee=@me ...` (GH-6).
2. `gh search prs --state=open --review-requested=@me ...` (GH-7).
3. `gh search issues --state=open --assignee=@me ...` (GH-16).
4. `gh search issues --state=open --mentions=@me ...` (GH-17).
5. `apl call ms:$ms_handle GET "$GRAPH/me/messages?\$filter=flag/flagStatus eq 'flagged' and isRead eq false"` → flagged Outlook mail needing follow-up (uses Family 2 pattern; not a numbered recipe — see Gotchas).
6. `apl call google:$google_handle GET "$GMAIL/messages?q=is:starred is:unread"` → starred + unread Gmail.

**Synthesis hint:** Four sections — **Review**, **Assigned**, **Mentioned**, **Follow-up mail** — in that order (review is most time-sensitive because it's blocking someone else). Within each, sort by age-of-last-update ascending (oldest first → most stale → most likely to be forgotten). For each item: one line, `<repo-or-provider> #<n>: <title> — <age>`.

**Gotchas:** Recipe 5 uses a `flagStatus eq 'flagged'` filter that's not currently a numbered recipe in Family 2 — flagged as invented-for-brief. Either add a new MAIL-R recipe, or scope down to unread-only. The Gmail `is:starred is:unread` query is cheap and doesn't need a separate recipe.

### BRIEF-6 — Any new message (lightweight poll)

**User intent:** "any new messages since <time>", "anything new in the last hour", "quick check".

**Call sequence (stateless — `$since` is a user-provided ISO-8601 UTC timestamp):**
1. `apl call ms:$ms_handle GET "$GRAPH/me/mailFolders/inbox/messages?\$filter=receivedDateTime gt $since and isRead eq false&\$select=subject,from,receivedDateTime&\$top=25"` (MAIL-R pattern).
2. `apl call google:$google_handle GET "$GMAIL/messages?q=is:unread after:<epoch-seconds-of-$since>&maxResults=25"` (MAIL-R-17 variant).
3. `apl call ms:$ms_handle GET "$GRAPH/me/chats/getAllMessages?\$filter=lastModifiedDateTime gt $since"` (CHAT-7).

**Synthesis hint:** One-screen output. Three buckets: "Mail (ms)", "Mail (gmail)", "Teams". Count + first 3 items per bucket. If all zero → "Nothing new since <since>.".

**Gotchas:** Different from BRIEF-2: BRIEF-2 uses delta tokens (incremental server-side state); BRIEF-6 uses timestamp filtering (stateless, may miss deleted/edited items, but doesn't require prior state). `$since` for Gmail needs epoch seconds (`date -u -j -f "%Y-%m-%dT%H:%M:%SZ" "$since" +%s` on BSD / macOS; `date -u -d "$since" +%s` on GNU).

### BRIEF-7 — Who's asked me something (mentions aggregator)

**User intent:** "who's pinged me", "who wants my attention", "mentions", "am I needed anywhere".

**Call sequence:**
1. `apl call ms:$ms_handle GET "$GRAPH/me/chats/getAllMessages?\$filter=contains(body/content,'<my-display-name>') or mentionsMe eq true"` → Teams @-mentions (CHAT-7 variant; `mentionsMe` is a convenience filter — see Gotchas).
2. `apl call google:$google_handle GET "$GMAIL/messages?q=to:me is:unread"` + per-message MAIL-R-22 (MAIL-R-17 + MAIL-R-22).
3. `apl call ms:$ms_handle GET "$GRAPH/me/messages?\$filter=isRead eq false and toRecipients/any(r:r/emailAddress/address eq '$user_email_ms')"` → direct-to-me MS mail.
4. `gh search prs --state=open --review-requested=@me ...` (GH-7).
5. `gh search issues --state=open --mentions=@me ...` (GH-17).

**Synthesis hint:** Group by sender/requester, not by channel. One line per asker: "<name>: <n> items across <channels>". Then expand top 3 askers with item specifics. The goal is to surface "the 2 people I need to respond to today" — not a raw feed.

**Gotchas:** `mentionsMe eq true` is NOT a standard Graph property on chat messages — flagged as invented-for-brief. The real mechanism is to fetch messages and inspect `mentions[]` client-side; `contains(body/content, ...)` with the user's display name is a cruder but actually-supported workaround. Muthu to decide which form ships.

### BRIEF-8 — Today's focus (terse one-glance)

**User intent:** "give me the short version", "one line", "quick status", "focus mode", "just the headline".

**Call sequence:**
1. `apl call ms:$ms_handle GET "$GRAPH/me/calendarView?startDateTime=<now>&endDateTime=<end-of-day>&\$orderby=start/dateTime&\$top=1&\$select=subject,start,onlineMeeting"` → next MS meeting (CAL-R-3 variant).
2. `apl call google:$google_handle GET "$CAL/calendars/primary/events?timeMin=<now>&singleEvents=true&orderBy=startTime&maxResults=1"` → next Google meeting (CAL-R-8 variant).
3. `apl call ms:$ms_handle GET "$GRAPH/me/mailFolders/inbox/messages/\$count?\$filter=isRead eq false"` → unread MS count (MAIL-R-3).
4. `apl call google:$google_handle GET "$GMAIL/messages?q=is:unread&maxResults=1"` → unread Gmail count via `resultSizeEstimate`.
5. `gh search prs --state=open --review-requested=@me --json url --limit 100 | jq length` → PRs waiting count (GH-7).

**Synthesis hint:** Exactly 3 lines of output, no preamble:
```
Next meeting: <subject> in <X> min (<join-url-or-location>)
Unread: <N> mail (<ms_n> ms / <google_n> gmail)
Review queue: <K> PRs waiting
```
If no next meeting today: `Next meeting: none today.` Same terseness for empty buckets.

**Gotchas:** Use the cheap count endpoints — do NOT fetch message bodies. `$count` (Graph) and `resultSizeEstimate` (Gmail) are the right tools here. Also consider a short cache (60s) on these calls so a user running `focus` five times in a minute doesn't burn rate limit.

---

## Acceptance Criteria

- [ ] Every recipe has: purpose line, handle, scopes, `apl call` invocation (or a fallback marked clearly), expected response shape, gotchas (or `N/A`).
- [ ] Every recipe can be verified end-to-end against a real tenant. Verification lives in `tests/e2e/*_smoke.sh` (extended) and is expanded family-by-family.
- [ ] Every MS recipe matches the row in `docs/microsoft-graph.md`. Every Google recipe matches `docs/google-apis.md`. Discrepancies are flagged at the top of this file, not silently resolved.
- [ ] Recipes that need curl (binary uploads with streamed bodies, token-free downloads of pre-authenticated URLs, multipart/mixed batch) are explicitly marked `Fallback:` with a minimum-working curl snippet using `TOKEN=$(apl login <handle>)`.
- [ ] Every gotcha that caused a real failure in the huddle (MEET-8 403 wall, CHAT-5 `eventDetail` empty on `/me/chats/...`, Gmail send `raw` payload, calendar `singleEvents=true` requirement) is called out at the recipe level.
- [ ] Scope citations use the exact Graph / Google scope name so the skill can generate the minimal-scope hint in `apl login <handle> --force --scope <...>` suggestions.
- [ ] The catalogue is machine-parseable enough that the companion skill's generator can split on `### <FAMILY>-N` headings and emit one reference markdown file per family.

---

## Implementation Notes

- **Ownership of truth.** When an endpoint changes, update `docs/microsoft-graph.md` / `docs/google-apis.md` first (those are the hand-curated docs), then regenerate the corresponding recipe blocks here. Do **not** diverge this file from the docs in isolation.
- **Skill `references/` derivation.** Treat this file as the seed. The companion skill's `references/mail-ms.md`, `references/mail-google.md`, `references/calendar.md`, etc. are generated by splitting on family headings. Generator script lives in the companion skill repo (`muthuishere-agent-skills/all-purpose-data-skill`).
- **`apl call` contract assumption.** This spec assumes `spec-apl-call.md` ships with:
  - Bearer injection from `apl login <handle>` (auto-refresh on 401 with one retry).
  - `--body '<json>'` passes through as request body with `Content-Type: application/json` when it looks like JSON.
  - `-H 'Header: value'` forwards headers (for `ConsistencyLevel: eventual`).
  - `-o <file>` writes the response body to a file (handles binary).
  - Transparently follows 3xx redirects by default.
  - Preserves the response status code as the process exit code's low byte is NOT used; exit codes follow `spec-cli-command-surface.md` (0=ok, 1=user, 2=auth, 3=network). The HTTP code is available via `--status-only`.
- **Fallback-curl usage pattern.** Every `Fallback:` block uses the exact shape:
  ```bash
  TOKEN=$(apl login <handle>)
  curl -H "Authorization: Bearer $TOKEN" ...
  ```
  No direct keychain access; no manual refresh logic. The user-facing story stays "`apl` is the broker".
- **URL encoding.** The dollar sign in OData params (`$top`, `$filter`) must be backslash-escaped inside double-quoted bash strings: `"$GRAPH/me/messages?\$top=5"`. Inside single quotes the literal `$` is fine. Examples in this spec use the double-quote + backslash form consistently.
- **Date handling.** Smoke tests use BSD `date -u -v+1d` (macOS); document `date -u -d tomorrow` (GNU Linux) where relevant. Recipes themselves embed plain ISO-8601 literals.
- **Recipe stability.** Recipe numbers (`MAIL-R-5`) are stable IDs. Never renumber; only append. If a recipe is deprecated, mark it **Deprecated** at the top of the block but keep the number.

---

## Verification

For each family, automated verification is an extension of `tests/e2e/ms_smoke.sh` and `tests/e2e/google_smoke.sh`:

1. **Extend smoke tests per family.** Every recipe gets a matching `hdr "<FAMILY>-N"` block in the relevant smoke script that runs the call and asserts the expected HTTP status. Read-only recipes are always safe; write recipes are guarded behind an env-var flag (`MAIL_TO`, `APL_WRITE_TESTS=1`) so CI won't send mail on every run.
2. **Manual ground truth.** Each family has been manually verified against one or more tenants:
   - MS: reqsume (`ms:reqsume`), volentis (`ms:volentis`).
   - Google: muthuishere@gmail.com (`google:muthu`), Volentis (`google:volentis`).
3. **Scope minimality audit.** Periodically revoke the OAuth app's consent and re-run with the minimal scope set listed per recipe; any recipe that requires a scope NOT documented in its `**Scopes:**` line is a bug to fix in this spec.
4. **403 wall verification.** MEET-8 must reliably 403 for a non-organizer caller — the smoke test asserts this to catch the day Microsoft changes the gate.

Quick local verification template:

```bash
task build
./bin/apl login ms:reqsume >/dev/null        # warm token
./bin/apl login google:muthu >/dev/null
tests/e2e/ms_smoke.sh      ms:reqsume
tests/e2e/google_smoke.sh  google:muthu
# Family-specific extensions (recipe-level) run the same way.
```

Exit code from each smoke script is the number of failed sections; 0 = everything verified (skips allowed for write ops without recipients).
