# Google APIs — Reference

Every URL `apl` has been verified against. Use these with the token from `apl login google:<handle>`.

```bash
TOKEN=$(apl login google:muthu)
curl -H "Authorization: Bearer $TOKEN" <URL>
```

Unlike Microsoft Graph, Google does not have a single base URL — each API family lives on its own host (`gmail.googleapis.com`, `www.googleapis.com/calendar/v3`, `people.googleapis.com`, etc.). Each table below lists full URLs.

---

## Identity

| URL | Scope | Notes |
|---|---|---|
| `GET https://openidconnect.googleapis.com/v1/userinfo` | `openid email profile` | OIDC-standard userinfo. Returns `sub`, `email`, `name`, `picture`. Preferred. |
| `GET https://www.googleapis.com/oauth2/v3/userinfo` | `openid email profile` | Legacy alias of the same endpoint. Still works; new code should use the OIDC host. |

## Gmail

Base: `https://gmail.googleapis.com/gmail/v1/users/me`

| URL | Scope | Notes |
|---|---|---|
| `GET /messages?maxResults=10` | `gmail.readonly` | Lists message ids + threadIds only. Paginate with `&pageToken=<nextPageToken>` from the response. |
| `GET /messages/{id}` | `gmail.readonly` | Full message: headers, MIME parts, body (base64url inside `payload.parts[*].body.data`). |
| `GET /messages/{id}?format=metadata&metadataHeaders=Subject,From,Date` | `gmail.readonly` | Lean projection — headers only, no body. Much smaller payload when you just need a subject list. |
| `GET /messages?q=from:boss@example.com after:2026/01/01&maxResults=50` | `gmail.readonly` | Gmail search syntax (same as the web UI): `from:`, `to:`, `subject:`, `after:`, `before:`, `label:`, `has:attachment`, `is:unread`. URL-encode the `q` value. |
| `GET /messages/{id}/attachments/{attachmentId}` | `gmail.readonly` | Attachment body as base64url in `data`. Attachment ids come from `payload.parts[*].body.attachmentId`. |
| `POST /messages/send` | `gmail.send` (or `gmail.modify`) | Body is JSON `{"raw":"<base64url RFC 2822 message>"}` — **not** a structured JSON payload. See gotcha below. |
| `GET /labels` | `gmail.readonly` | System + user labels with ids. |

## Calendar

Base: `https://www.googleapis.com/calendar/v3`

| URL | Scope | Notes |
|---|---|---|
| `GET /calendars/primary/events?timeMin=2026-01-01T00:00:00Z&maxResults=10&singleEvents=true&orderBy=startTime` | `calendar.readonly` | Recent events on primary calendar. `singleEvents=true` expands recurring events into instances; `orderBy=startTime` only works when `singleEvents=true`. |
| `GET /calendars/primary/events/{eventId}` | `calendar.readonly` | Full event — includes `conferenceData`, `hangoutLink`, `attendees`, `organizer`. |
| `POST /calendars/primary/events` | `calendar` | Create. Body: `{"summary":"...","start":{"dateTime":"2026-05-01T10:00:00+05:30"},"end":{...},"attendees":[{"email":"..."}]}`. Add `conferenceDataVersion=1` as a query param and `conferenceData.createRequest` in the body to auto-provision a Meet link. |
| `GET /users/me/calendarList` | `calendar.readonly` | All calendars this user sees (primary, secondaries, subscribed). |

**Finding Google Meet events:** events with a Meet link have either `hangoutLink` (string) or `conferenceData.conferenceId` / `conferenceData.entryPoints[*].uri` where `entryPointType == "video"`. Check both — older events often only have `hangoutLink`, new API-created events fill `conferenceData`.

## Drive

Base: `https://www.googleapis.com/drive/v3`

| URL | Scope | Notes |
|---|---|---|
| `GET /files?pageSize=20&fields=files(id,name,mimeType,size,webViewLink,createdTime,modifiedTime,owners)` | `drive.readonly` | List files. Without `fields=` you get a minimal projection (id, name, mimeType). Paginate with `pageToken`. |
| `GET /files?q=name contains 'meeting'&pageSize=20` | `drive.readonly` | Search by name. URL-encode the whole `q` value. Other operators: `mimeType='video/mp4'`, `'me' in owners`, `trashed=false`, `modifiedTime > '2026-01-01T00:00:00'`, `parents in '<folderId>'`. Combine with `and` / `or`. |
| `GET /files/{id}?fields=id,name,mimeType,size,webViewLink,owners` | `drive.readonly` | Single file metadata. |
| `GET /files/{id}?alt=media` | `drive.readonly` | Binary download for non-Google-Doc files (PDFs, mp4s, images, zips). Follow redirects — Google 302s to a signed storage URL. |
| `GET /files/{id}/export?mimeType=application/pdf` | `drive.readonly` | Export a Google Doc / Sheet / Slide to a concrete format. Common targets: `application/pdf`, `text/plain`, `text/csv` (Sheets), `application/vnd.openxmlformats-officedocument.wordprocessingml.document` (Docs→.docx). Native Google files **must** use `/export` — `alt=media` on a Google Doc returns 403. |
| `GET /files/{id}/permissions` | `drive.readonly` | Who can see the file. |

**Drive `q=` is the workhorse.** See https://developers.google.com/drive/api/guides/search-files. A few patterns that come up constantly:

- `mimeType='application/vnd.google-apps.folder'` — list folders
- `'<folderId>' in parents and trashed=false` — list contents of a folder
- `name contains 'Recording' and mimeType='video/mp4'` — find Meet recordings
- `sharedWithMe=true` — only shared-in items

## Contacts / People

Base: `https://people.googleapis.com/v1`

| URL | Scope | Notes |
|---|---|---|
| `GET /people/me?personFields=names,emailAddresses` | `contacts.readonly` | Current user's profile from the People API. `personFields` is **required** on every People call — requests without it 400. |
| `GET /people/me/connections?personFields=names,emailAddresses,phoneNumbers&pageSize=100` | `contacts.readonly` | "My Contacts" — people the user has explicitly saved. Paginate with `pageToken`. Does not include the auto-populated "Other contacts" list (that lives at `/otherContacts`). |
| `GET /otherContacts?readMask=names,emailAddresses&pageSize=100` | `contacts.other.readonly` | Auto-populated contacts from Gmail. |

## Google Meet recordings

Meet recordings are plain `.mp4` files stored in the **organizer's Drive**, by default in `My Drive > Meet Recordings`. There is no dedicated recordings API for personal / Workspace accounts — you go through Drive.

Three-step workflow, all with `drive.readonly` + `calendar.readonly`:

1. **Identify the meeting.** From a calendar event, take `conferenceData.conferenceId` (or just the event subject + start time). Meet recording filenames look like:

    ```
    Meeting Recording 2026-03-28 15:02 GMT+05:30 - <Subject> - Recording.mp4
    ```

2. **Search Drive for the recording file:**

    ```bash
    # URL-encode the quotes, spaces, and + signs
    curl -s -H "Authorization: Bearer $TOKEN" \
      "https://www.googleapis.com/drive/v3/files?q=name%20contains%20'<subject>'%20and%20mimeType%3D'video%2Fmp4'&fields=files(id,name,size,createdTime,webViewLink)"
    ```

3. **Download the binary** (follow redirects):

    ```bash
    curl -L -H "Authorization: Bearer $TOKEN" \
      -o recording.mp4 \
      "https://www.googleapis.com/drive/v3/files/$FILE_ID?alt=media"
    ```

**Contrast with Microsoft Teams:** the MS side needed a convoluted OneDrive detour because Graph gates `/recordings/{id}/content` on meeting-participant ACL. Google is much simpler — if the recording file is in your Drive or shared with you, `drive.readonly` + `alt=media` is enough. No admin consent, no separate recording scope, no pre-authenticated download URL dance. The only asymmetry: if you're not the organizer and the organizer hasn't shared the file with you, you cannot reach it at all — there's no organizer-tenant-wide fallback like `Files.Read.All`.

## Gotchas

### Unverified-app / Testing-mode limits

Scopes like `gmail.modify`, `gmail.send`, `drive`, `drive.readonly` (for Gmail attachments), `calendar` are **restricted** by Google. If your OAuth client is in **Production** mode without passing Google's annual security review, users see an "unverified app" warning or the OAuth request is blocked entirely.

Because `apl setup google` registers a BYO OAuth client in **Testing** mode by default, you avoid review but:

- You must add every user as a **Test User** on the OAuth consent screen
- There's a hard cap of **100 test users** per consent screen
- Refresh tokens expire after **7 days** in Testing mode (tokens issued to Production clients don't)

This is fine for single-developer / small-team usage, which is `apl`'s target.

### `POST /messages/send` wants base64url, not JSON

Gmail's send endpoint is an outlier — the body is `{"raw":"<base64url-encoded RFC 2822 message>"}`, not a structured JSON message. The classic mistake is to send a JSON body with `to`, `subject`, `body` fields and get back a 400.

Minimal Python snippet that builds a correct `raw`:

```python
import base64, json
from email.message import EmailMessage

msg = EmailMessage()
msg["To"] = "someone@example.com"
msg["From"] = "me@example.com"
msg["Subject"] = "apl e2e smoke"
msg.set_content("sent by tests/e2e/google_smoke.sh")

raw = base64.urlsafe_b64encode(bytes(msg)).decode().rstrip("=")
print(json.dumps({"raw": raw}))
```

Post that JSON to `https://gmail.googleapis.com/gmail/v1/users/me/messages/send` with `Content-Type: application/json`. Response is `200` with `{"id":"...","threadId":"...","labelIds":["SENT"]}`.

### Calendar `timeMin` defaults to "now"

If you omit `timeMin` from `GET /calendars/primary/events`, Google defaults it to the time of the request — so you'll only see **future** events and think your calendar is empty. Always pass `timeMin` explicitly when doing historical lookups.

Matching gotcha: `orderBy=startTime` only works when `singleEvents=true`. If you forget the second param, the API returns 400, not "ordered differently".

### Drive `files.list` is scoped to the caller

Unlike some MS Graph endpoints, there is no "list everyone in the domain's files" for personal accounts. `files.list` returns files where the caller is an owner, editor, viewer, or has them in `sharedWithMe`. For cross-user access you need a Workspace domain with a service account + domain-wide delegation — out of scope for `apl`.

### Native Google files and `alt=media`

`GET /files/{id}?alt=media` returns `403` for Google Docs/Sheets/Slides/Forms — those have no binary form. Use `/files/{id}/export?mimeType=...` instead. Check `mimeType` before deciding which one to call: anything starting with `application/vnd.google-apps.*` is native, everything else is a regular file.

## Scope summary for the default `apl setup google` registration

All user-consentable (Testing mode, no Google security review):

- `openid`, `email`, `profile`
- `https://www.googleapis.com/auth/gmail.modify`
- `https://www.googleapis.com/auth/calendar`
- `https://www.googleapis.com/auth/contacts.readonly`
- `https://www.googleapis.com/auth/drive.readonly`

Note: `gmail.modify` is a superset of `gmail.readonly` + `gmail.send`, so there's one scope for all three. `calendar` (read/write) is used instead of `calendar.readonly` so the same registration can create events during demos.

## Auth endpoints (handled by `apl`; documented for reference)

| Purpose | URL |
|---|---|
| Authorize (PKCE) | `https://accounts.google.com/o/oauth2/v2/auth` |
| Token | `https://oauth2.googleapis.com/token` |
| UserInfo | `https://openidconnect.googleapis.com/v1/userinfo` |
| Revoke | `https://oauth2.googleapis.com/revoke` |

Authorize params worth knowing:

- `access_type=offline` — required to receive a refresh token
- `prompt=consent` — force the consent screen again, so a refresh token is actually returned (Google will silently skip issuing a new refresh token on re-auth otherwise)
- `include_granted_scopes=true` — incremental authorization: the returned token covers previously-granted scopes too

## Useful query shapes

- Gmail search (`q=`): `from:boss@example.com`, `after:2026/01/01`, `label:INBOX`, `has:attachment`, `is:unread`, `subject:"quarterly review"`
- Drive search (`q=`): `name contains 'X'`, `mimeType='video/mp4'`, `'<folderId>' in parents`, `modifiedTime > '2026-01-01T00:00:00'`, `sharedWithMe=true`, `trashed=false`
- Fields projection: Google uses `fields=` (not OData `$select`). Syntax is partial-response: `fields=files(id,name,size)` or `fields=nextPageToken,files(id,name)`
- Pagination: every list endpoint returns `nextPageToken`; pass it back as `pageToken=<value>`
- Calendar ranges: ISO 8601 with timezone — `timeMin=2026-01-01T00:00:00Z`

## References

- Gmail API — https://developers.google.com/gmail/api/reference/rest
- Calendar API — https://developers.google.com/calendar/api/v3/reference
- Drive API — https://developers.google.com/drive/api/reference/rest/v3
- People API — https://developers.google.com/people/api/rest
- OAuth 2.0 scopes — https://developers.google.com/identity/protocols/oauth2/scopes
- OAuth app verification — https://support.google.com/cloud/answer/9110914

---

## Common flows (FAQ)

All snippets assume `TOKEN=$(apl login google:<handle>)` and `AUTH` header elided for brevity.

```bash
TOKEN=$(apl login google:muthu)
AUTH=(-H "Authorization: Bearer $TOKEN")
```

### Gmail

| Q | Call |
|---|---|
| **Unread emails** | `GET https://gmail.googleapis.com/gmail/v1/users/me/messages?q=is:unread&maxResults=20` then fetch each `id` |
| **Today's inbox** | `GET /users/me/messages?q=in:inbox newer_than:1d&maxResults=50` |
| **From a specific sender** | `GET /users/me/messages?q=from:shaama@reqsume.com` |
| **With attachments** | `GET /users/me/messages?q=has:attachment&maxResults=50` |
| **By label** | `GET /users/me/messages?q=label:important` (labels: `inbox`, `sent`, `drafts`, `important`, `starred`, `spam`, `trash`, or your custom labels) |
| **Full-text search** | `GET /users/me/messages?q="project kickoff"` — supports Gmail's full search syntax (from:, to:, subject:, before:, after:, has:, filename:, is:, larger:, smaller:, etc.) |
| **Get full message** | `GET /users/me/messages/{id}?format=full` (includes payload tree with MIME parts) |
| **Get headers only** | `GET /users/me/messages/{id}?format=metadata&metadataHeaders=Subject,From,Date` |
| **Get raw RFC 2822** | `GET /users/me/messages/{id}?format=raw` — returns base64url-encoded `.eml` |
| **Get attachment** | `GET /users/me/messages/{msgId}/attachments/{attId}` — returns base64url blob in `data` |
| **Mark as read** | `POST /users/me/messages/{id}/modify` body `{"removeLabelIds":["UNREAD"]}` |
| **Mark as unread** | `POST /users/me/messages/{id}/modify` body `{"addLabelIds":["UNREAD"]}` |
| **Trash message** | `POST /users/me/messages/{id}/trash` |
| **Untrash** | `POST /users/me/messages/{id}/untrash` |
| **Delete permanently** | `DELETE /users/me/messages/{id}` |
| **Send email** | `POST /users/me/messages/send` body `{"raw":"<base64url-encoded RFC 2822>"}` |
| **Reply (threading)** | Same POST, but set `"threadId":"<threadId from original>"` on the body and include `In-Reply-To` + `References` headers in the RFC 2822 message |
| **Drafts (create)** | `POST /users/me/drafts` body `{"message":{"raw":"..."}}` |
| **Send draft** | `POST /users/me/drafts/send` body `{"id":"<draftId>"}` |
| **List labels** | `GET /users/me/labels` |
| **Create label** | `POST /users/me/labels` body `{"name":"Project/X","messageListVisibility":"show","labelListVisibility":"labelShow"}` |
| **Apply label** | `POST /users/me/messages/{id}/modify` body `{"addLabelIds":["Label_123"]}` |
| **Watch inbox (push)** | `POST /users/me/watch` body `{"topicName":"projects/.../topics/gmail","labelIds":["INBOX"]}` — Pub/Sub destination for realtime notifications |
| **History (delta)** | `GET /users/me/history?startHistoryId=<previous>` — returns messages added/deleted/labeled since that point |

**Base64url encoding for send** (Python):

```python
import base64, json
from email.mime.text import MIMEText

msg = MIMEText("hello from apl")
msg["To"] = "muthuishere@gmail.com"
msg["From"] = "me@gmail.com"
msg["Subject"] = "test"
raw = base64.urlsafe_b64encode(msg.as_bytes()).decode().rstrip("=")
print(json.dumps({"raw": raw}))
```

### Calendar

| Q | Call |
|---|---|
| **Today's events** | `GET https://www.googleapis.com/calendar/v3/calendars/primary/events?timeMin=$(date -u +%Y-%m-%dT00:00:00Z)&timeMax=$(date -u -v+1d +%Y-%m-%dT00:00:00Z)&singleEvents=true&orderBy=startTime` |
| **Next 10 upcoming** | `GET /calendars/primary/events?timeMin=$(date -u +%Y-%m-%dT%H:%M:%SZ)&maxResults=10&singleEvents=true&orderBy=startTime` |
| **Search by text** | `GET /calendars/primary/events?q=retro&singleEvents=true` |
| **Events with Meet link** | List events; filter client-side for `hangoutLink` or `conferenceData.entryPoints[].uri` present |
| **Get event** | `GET /calendars/primary/events/{eventId}` |
| **Create event with Meet link** | `POST /calendars/primary/events?conferenceDataVersion=1` body `{"summary":"Sync","start":{"dateTime":"...","timeZone":"UTC"},"end":{...},"attendees":[{"email":"x@y.com"}],"conferenceData":{"createRequest":{"requestId":"<random>","conferenceSolutionKey":{"type":"hangoutsMeet"}}}}` |
| **Quick-add (natural language)** | `POST /calendars/primary/events/quickAdd?text=Lunch tomorrow 12:30` |
| **RSVP (accept/decline)** | `PATCH /calendars/primary/events/{eventId}` body `{"attendees":[{"email":"<my email>","responseStatus":"accepted"}]}` |
| **Delete event** | `DELETE /calendars/primary/events/{eventId}` |
| **Free/busy lookup** | `POST /freeBusy` body `{"timeMin":"...","timeMax":"...","items":[{"id":"muthu@example.com"},{"id":"shaama@reqsume.com"}]}` |
| **List calendars** | `GET /users/me/calendarList` |
| **Watch for changes** | `POST /calendars/primary/events/watch` body `{"id":"<chan-id>","type":"web_hook","address":"https://..."}` — webhook notifications |
| **Sync (delta)** | First call: `GET .../events` captures `nextSyncToken`. Subsequent: `GET .../events?syncToken=<token>` returns only changes. |

### Google Meet recordings

Meet recordings auto-save to the host's Drive under `Meet Recordings` folder (named `<Event Title> (YYYY-MM-DD HH:MM GMT+<tz>)`).

| Q | Call |
|---|---|
| **Find today's recording** | `GET https://www.googleapis.com/drive/v3/files?q=mimeType='video/mp4' and name contains 'Reqsume' and modifiedTime > '2026-04-23T00:00:00'&fields=files(id,name,mimeType,size,webViewLink,owners)` |
| **Find by folder** | First get the "Meet Recordings" folder id: `GET /drive/v3/files?q=name='Meet Recordings' and mimeType='application/vnd.google-apps.folder'&fields=files(id)` — then list children: `q='<folderId>' in parents and mimeType='video/mp4'` |
| **Download the mp4** | `GET /drive/v3/files/{id}?alt=media` (use `curl -L -o recording.mp4`). Works as long as the caller owns or has been shared on the file. |
| **Get auto-generated transcript/caption** | `GET /drive/v3/files?q=name contains '<same base name>' and mimeType='text/vtt'` — Google Meet saves captions alongside the mp4 if the host enabled them. |

### Drive

| Q | Call |
|---|---|
| **Most recent 20 files** | `GET /drive/v3/files?pageSize=20&orderBy=modifiedTime desc&fields=files(id,name,mimeType,size,modifiedTime,webViewLink,owners)` |
| **Search by name** | `GET /drive/v3/files?q=name contains 'budget'&pageSize=20` |
| **Shared with me** | `GET /drive/v3/files?q=sharedWithMe&pageSize=50` |
| **Starred** | `GET /drive/v3/files?q=starred=true` |
| **Folders only** | `GET /drive/v3/files?q=mimeType='application/vnd.google-apps.folder'` |
| **In a specific folder** | `GET /drive/v3/files?q='<folderId>' in parents` |
| **Download non-Google file** | `GET /drive/v3/files/{id}?alt=media` (binary) |
| **Export Google Doc to PDF** | `GET /drive/v3/files/{id}/export?mimeType=application/pdf` |
| **Export Google Sheet to xlsx** | `GET /drive/v3/files/{id}/export?mimeType=application/vnd.openxmlformats-officedocument.spreadsheetml.sheet` |
| **Export Google Slides to pptx** | `GET /drive/v3/files/{id}/export?mimeType=application/vnd.openxmlformats-officedocument.presentationml.presentation` |
| **File metadata** | `GET /drive/v3/files/{id}?fields=id,name,mimeType,size,webViewLink,owners,permissions` |
| **List permissions** | `GET /drive/v3/files/{id}/permissions` |
| **Share file** | `POST /drive/v3/files/{id}/permissions` body `{"role":"reader","type":"user","emailAddress":"x@y.com"}` |
| **Changes feed** | `GET /drive/v3/changes?pageToken=<token>` — first get a startPageToken from `/drive/v3/changes/startPageToken` |

### People / Contacts

| Q | Call |
|---|---|
| **My profile** | `GET https://people.googleapis.com/v1/people/me?personFields=names,emailAddresses,photos` |
| **My contacts** | `GET /v1/people/me/connections?personFields=names,emailAddresses,phoneNumbers&pageSize=100` |
| **Search contacts** | `GET /v1/people:searchContacts?query=shaama&readMask=names,emailAddresses` |
| **Other directory contacts** (Workspace dir) | `GET /v1/people:listDirectoryPeople?sources=DIRECTORY_SOURCE_TYPE_DOMAIN_PROFILE&readMask=names,emailAddresses&pageSize=50` — needs `directory.readonly` scope |
| **Create contact** | `POST /v1/people:createContact` body `{"names":[{"givenName":"Shaama"}],"emailAddresses":[{"value":"shaama@reqsume.com"}]}` |

### Useful Gmail `q=` operators

`from:` `to:` `cc:` `subject:` `label:` `has:attachment` `filename:pdf` `is:unread` `is:starred` `is:important` `newer_than:1d` `older_than:7d` `before:2026/04/23` `after:2026/04/01` `larger:5M` `smaller:1M` `category:primary|social|promotions|updates|forums` `in:anywhere` `list:dev@` — combine with spaces (AND), `OR`, parentheses, negation with `-`.

### Useful Drive `q=` operators

`name = 'x'`, `name contains 'x'`, `fullText contains 'x'`, `mimeType = '...'`, `'<folderId>' in parents`, `sharedWithMe`, `starred = true`, `trashed = false`, `modifiedTime > '2026-04-01T00:00:00'`, `owners in 'me'`. Combine with `and`, `or`, parentheses. String literals use single quotes.

### Throttling

Gmail: per-user quota ~250 units/sec (each op has a unit cost). 429 responses come with `Retry-After`.

Calendar: 50 queries/sec per user by default.

Drive: 20,000 queries/100s per user by default.

Honor `Retry-After` headers; back off exponentially on 403 `userRateLimitExceeded`.

### Batching

Each API has its own batch endpoint that accepts a multipart/mixed body:

- Gmail: `POST https://www.googleapis.com/batch/gmail/v1`
- Calendar: `POST https://www.googleapis.com/batch/calendar/v3`
- Drive: `POST https://www.googleapis.com/batch/drive/v3`

Each sub-request is a full HTTP request with its own headers. Good for bulk reads; unnecessary for light usage.
