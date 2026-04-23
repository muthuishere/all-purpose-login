# Microsoft Graph URLs — Reference

> **Setup flow:** `apl setup ms` now shows a two-step picker. First pick the az account/tenant (dedup'd by `user.name`, active account labelled `[active]`; choose "Sign in another tenant/account" to run `az login --tenant <domain> --allow-no-subscriptions` in-line). Then pick an existing `apl-*` app registration or create a new one. Re-running setup lets you change either pick.

Every URL `apl` has been verified against. Use these with the token from `apl login ms:<handle>`.

```bash
TOKEN=$(apl login ms:reqsume)
curl -H "Authorization: Bearer $TOKEN" <URL>
```

All paths assume `https://graph.microsoft.com/v1.0` base unless noted.

---

## Identity

| URL | Scope | Notes |
|---|---|---|
| `GET /me` | `User.Read` | Current user profile (displayName, mail, UPN, id). |
| `GET /me?$select=displayName,mail,userPrincipalName` | `User.Read` | Subset. |

## Mail

| URL | Scope | Notes |
|---|---|---|
| `GET /me/messages?$top=10&$orderby=receivedDateTime desc` | `Mail.Read` | Most recent messages. |
| `GET /me/messages?$select=subject,from,receivedDateTime,bodyPreview` | `Mail.Read` | Lean projection. |
| `GET /me/messages/{id}` | `Mail.Read` | Full message body. |
| `POST /me/sendMail` | `Mail.Send` | Body: `{"message":{"subject":...,"body":{"contentType":"Text","content":...},"toRecipients":[{"emailAddress":{"address":"..."}}]},"saveToSentItems":true}`. Returns 202. |

## Calendar

| URL | Scope | Notes |
|---|---|---|
| `GET /me/events?$top=10&$orderby=start/dateTime desc` | `Calendars.Read` | Recent events. Use `Calendars.ReadWrite` to modify. |
| `GET /me/events?$select=subject,start,end,isOnlineMeeting,onlineMeeting,organizer` | `Calendars.Read` | Include onlineMeeting object to extract `joinUrl`. |
| `POST /me/events` | `Calendars.ReadWrite` | Create event. |

## Teams meetings

| URL | Scope | Notes |
|---|---|---|
| `GET /me/onlineMeetings?$filter=JoinWebUrl eq '<urlencoded-joinUrl>'` | `OnlineMeetings.Read` | **Only** `JoinWebUrl` and `VideoTeleconferenceId` filters are supported. You cannot list all meetings — always look up by join URL (usually from a calendar event's `onlineMeeting.joinUrl`). |
| `GET /me/onlineMeetings/{meetingId}` | `OnlineMeetings.Read` | Full meeting metadata. |
| `GET /me/onlineMeetings/{meetingId}/recordings` | `OnlineMeetingRecording.Read.All` | Returns recording metadata: id, timestamps, meetingOrganizer, `recordingContentUrl`. Admin consent required. |
| `GET /me/onlineMeetings/{meetingId}/recordings/{recordingId}/content` | `OnlineMeetingRecording.Read.All` + file access | Binary mp4 stream. **See gotcha below.** |
| `GET /me/onlineMeetings/{meetingId}/transcripts` | `OnlineMeetingTranscript.Read.All` | Returns transcript list. Admin consent required. |
| `GET /me/onlineMeetings/{meetingId}/transcripts/{transcriptId}/content?$format=text/vtt` | `OnlineMeetingTranscript.Read.All` | Speaker-tagged VTT. Verified ~93KB for a 90-minute meeting. |

### Recording mp4 — two-step workflow via OneDrive

The `/recordings/{id}/content` endpoint returns **403 accessDenied** for non-organizers even with `OnlineMeetingRecording.Read.All` + `Files.Read.All` + tenant admin consent. Graph gates that stream on item ACL, not just scope. The workaround is to bypass `/content` and fetch the file from the organizer's OneDrive directly.

**Step 1 — identify the meeting and its organizer:**

```bash
TOKEN=$(apl login ms:reqsume)

# 1a) From a calendar event with isOnlineMeeting=true, grab onlineMeeting.joinUrl:
curl -s -H "Authorization: Bearer $TOKEN" \
  "$GRAPH/me/events?\$top=20&\$filter=isOnlineMeeting%20eq%20true&\$select=subject,organizer,onlineMeeting"

# 1b) Resolve the meetingId via joinWebUrl:
curl -s -H "Authorization: Bearer $TOKEN" \
  "$GRAPH/me/onlineMeetings?\$filter=JoinWebUrl%20eq%20'<urlencoded-joinUrl>'"
```

**Step 2 — list recordings metadata (still needs `OnlineMeetingRecording.Read.All`):**

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "$GRAPH/me/onlineMeetings/$MEETING_ID/recordings"
```

The metadata includes `meetingOrganizer.user.id` (organizer's AAD object id) and `createdDateTime` — but **not** the OneDrive path. Teams creates the file at a predictable path under the organizer's OneDrive:

```
/personal/<organizer-upn-with-dots-to-underscores>/Documents/Recordings/<Meeting Subject>-<YYYYMMDD>_<HHMMSS>-Meeting Recording.mp4
```

The OneDrive path (from Graph's perspective) is just `/Recordings/<filename>.mp4` relative to the organizer's drive root.

**Step 3 — fetch the drive item metadata on the organizer's drive (`Files.Read.All` scope):**

```bash
# The filename is deterministic; you can either list the Recordings folder or
# construct the filename yourself. Listing is safer:

ORGANIZER_UPN="shaama@reqsume.com"

curl -s -H "Authorization: Bearer $TOKEN" \
  "$GRAPH/users/$ORGANIZER_UPN/drive/root:/Recordings:/children?\$select=name,id,size,webUrl,@microsoft.graph.downloadUrl"
```

Or if you already know the exact filename:

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "$GRAPH/users/$ORGANIZER_UPN/drive/root:/Recordings/$(python3 -c \"import urllib.parse;print(urllib.parse.quote('Reqsume Team Meeting-20260328_150203-Meeting Recording.mp4'))\")"
```

The response includes a pre-authenticated SharePoint URL: `@microsoft.graph.downloadUrl`. This URL is time-limited (≈1h) and **does not require the Bearer token** — it has a `tempauth` query param already baked in.

**Step 4 — download the binary via the pre-authenticated URL:**

```bash
DOWNLOAD_URL=$(curl -s -H "Authorization: Bearer $TOKEN" \
  "$GRAPH/users/$ORGANIZER_UPN/drive/root:/Recordings/<filename>" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["@microsoft.graph.downloadUrl"])')

curl -L -o recording.mp4 "$DOWNLOAD_URL"
```

Do **not** pass the Bearer token on the download URL — SharePoint rejects it because the URL already carries a `tempauth` token with its own audience. Curl without `-H Authorization` is correct here.

**Required scopes for this full workflow:**

- `Calendars.Read` (step 1a)
- `OnlineMeetings.Read` (step 1b)
- `OnlineMeetingRecording.Read.All` (step 2, admin consent)
- `Files.Read.All` (step 3, admin consent) — to read the organizer's OneDrive
- No scope needed for step 4 (downloadUrl is pre-authenticated)

**Why this works when `/recordings/{id}/content` doesn't:**

`/recordings/{id}/content` uses Graph's `OnlineMeetingRecording` authorization path, which gates on meeting-participant ACL. `/users/{id}/drive/root:/path` uses Graph's `Files` authorization path, which gates on the OneDrive item's sharing ACL — and Teams auto-shares meeting recordings with all participants for Teams' own playback. `Files.Read.All` is enough to read them that way.

**Equivalent for transcripts:**

Transcripts live inside the meeting object, not in OneDrive, so `/me/onlineMeetings/{id}/transcripts/{id}/content?$format=text/vtt` works with `OnlineMeetingTranscript.Read.All` — no OneDrive detour needed.

---

### Alternative: discover the recording URL by iterating meeting chat messages

If you don't want to hardcode the OneDrive path (filename, organizer UPN, etc.), Teams posts a **system message** in the meeting chat when a recording completes. That message carries the share URL directly. This is a good discovery approach when you only know the meeting id.

**Step 1 — find the meeting chat id.** From the onlineMeeting object, `chatInfo.threadId` holds it:

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "$GRAPH/me/onlineMeetings/$MEETING_ID?\$select=chatInfo" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["chatInfo"]["threadId"])'
# → 19:meeting_...@thread.v2
```

Or filter `/me/chats?$filter=chatType eq 'meeting'`.

**Step 2 — list chat messages, projecting `eventDetail`:**

The default projection returns `body.content = "<systemEventMessage/>"` with no detail. You must explicitly `$select` (or `$expand`) the `eventDetail` complex type:

```bash
CHAT_ID='19:meeting_...@thread.v2'

curl -s -H "Authorization: Bearer $TOKEN" \
  "$GRAPH/chats/$CHAT_ID/messages?\$top=50"
```

The `eventDetail` field is returned by default on `/chats/{id}/messages` but is *not* populated on the nested `/me/chats/...` projection — prefer the top-level `/chats/{chatId}/messages` path. Required scope: `Chat.Read` (or `ChatMessage.Read`).

**Step 3 — scan for the recording event.** The message you want has:

```json
{
  "messageType": "systemEventMessage",
  "eventDetail": {
    "@odata.type": "#microsoft.graph.callRecordingEventMessageDetail",
    "callId": "<guid>",
    "callRecordingDisplayName": "Reqsume Team Meeting-20260328_150203-Meeting Recording.mp4",
    "callRecordingDuration": "PT1H32M28S",
    "callRecordingStatus": "success",
    "callRecordingUrl": "https://<tenant>-my.sharepoint.com/personal/<upn>/Documents/Recordings/<filename>.mp4",
    "meetingOrganizer": { "user": { "id": "...", "userIdentityType": "aadUser" } },
    "initiator": { "user": { "id": "...", "displayName": "Shaama Manoharan" } }
  }
}
```

Python snippet to extract:

```python
import json, urllib.request

req = urllib.request.Request(
    f"{GRAPH}/chats/{chat_id}/messages?$top=50",
    headers={"Authorization": f"Bearer {token}"},
)
data = json.load(urllib.request.urlopen(req))
for msg in data.get("value", []):
    ed = msg.get("eventDetail") or {}
    if ed.get("@odata.type") == "#microsoft.graph.callRecordingEventMessageDetail":
        print(ed.get("callRecordingStatus"), ed.get("callRecordingUrl"))
```

**Step 4 — use the URL.** The `callRecordingUrl` points to a SharePoint stream.aspx page (user-facing playback). To download the mp4, parse the URL's `id=` query param (URL-decoded, it's a path like `/personal/<upn>/Documents/Recordings/<file>.mp4`), then call Graph's Files API:

```bash
# Given: https://reqsume-my.sharepoint.com/personal/shaama_reqsume_com/_layouts/15/stream.aspx?id=%2Fpersonal%2Fshaama%5Freqsume%5Fcom%2FDocuments%2FRecordings%2F<file>.mp4&...

# Extract path: /personal/shaama_reqsume_com/Documents/Recordings/<file>.mp4
# The drive is identified by the "personal/<upn-with-underscores>" segment.
# Map back to Graph: /users/<upn-with-dots>/drive/root:/Recordings/<file>.mp4

# Upn reconstruction: shaama_reqsume_com → shaama@reqsume.com
ORGANIZER_UPN="shaama@reqsume.com"
FILE="Reqsume Team Meeting-20260328_150203-Meeting Recording.mp4"

curl -s -H "Authorization: Bearer $TOKEN" \
  "$GRAPH/users/$ORGANIZER_UPN/drive/root:/Recordings/$(python3 -c \"import urllib.parse;print(urllib.parse.quote('$FILE'))\")" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["@microsoft.graph.downloadUrl"])' \
  | xargs -I {} curl -L -o recording.mp4 {}
```

**Scopes required for this path:**

- `Chat.Read` — list meeting chat messages
- `Files.Read.All` — resolve drive item in organizer's OneDrive
- No `OnlineMeetingRecording.Read.All` needed! The chat message path bypasses the meeting-recording authorization gate entirely.

**Which path to pick:**

| You know | Use |
|---|---|
| the meeting id and organizer UPN | OneDrive path (step 3 in the earlier workflow) |
| only the meeting id | This chat-iteration path — discovers URL + organizer from the event message |
| only the SharePoint URL | Just parse the `id=` param and skip straight to `/users/{upn}/drive/root:/path` |

The chat-iteration path is what automation should prefer: it's resilient to Microsoft renaming/relocating the Recordings folder in the future, since Teams controls the URL it posts.

## Teams chat

| URL | Scope | Notes |
|---|---|---|
| `GET /me/chats?$expand=members&$top=50` | `Chat.Read` | All chats. `chatType` is `oneOnOne`, `group`, or `meeting`. |
| `GET /me/chats/{chatId}` | `Chat.Read` | Single chat. |
| `GET /me/chats/{chatId}/messages?$top=50` | `Chat.Read` | Messages in a chat. System events appear as `messageType=systemEventMessage`. |
| `POST /chats/{chatId}/messages` | `ChatMessage.Send` | Body: `{"body":{"content":"hello"}}`. Returns 201 + message object. |

## Files / OneDrive / SharePoint

| URL | Scope | Notes |
|---|---|---|
| `GET /me/drive/sharedWithMe` | `Files.Read.All` | Items shared with current user. |
| `GET /drives/{driveId}/items/{itemId}` | `Files.Read.All` | Direct item access. |
| `GET /drives/{driveId}/items/{itemId}/content` | `Files.Read.All` | Binary file stream. |
| `GET /sites/{siteId}` | `Sites.Read.All` | SharePoint site metadata. Admin consent required. |

## Scope summary for the default `apl setup ms` registration

User-consentable (no admin consent needed):

- `openid email profile offline_access`
- `User.Read`
- `Mail.ReadWrite Mail.Send`
- `Calendars.ReadWrite`
- `Chat.ReadWrite ChatMessage.Send`
- `OnlineMeetings.Read`

Opt-in during setup (admin consent required):

- `OnlineMeetingRecording.Read.All`
- `OnlineMeetingTranscript.Read.All`
- `Files.Read.All` (needed alongside the above to fetch recording mp4 when possible)

## Auth endpoints (handled by `apl`; documented for reference)

| Purpose | URL |
|---|---|
| Authorize (PKCE) | `https://login.microsoftonline.com/{tenant}/oauth2/v2.0/authorize` |
| Token | `https://login.microsoftonline.com/{tenant}/oauth2/v2.0/token` |
| `{tenant}` value | `common` (default; works for work+personal), or a tenant GUID / `<domain>.onmicrosoft.com` |

## Useful query shapes

- Select fields: `?$select=subject,from`
- Filter: `?$filter=isOnlineMeeting eq true` (only on endpoints that support OData filter — online meetings does NOT support the usual operators)
- Sort: `?$orderby=start/dateTime desc`
- Top / skip: `?$top=10&$skip=20`
- Count: `?$count=true` with header `ConsistencyLevel: eventual` for some endpoints

---

## Common flows (FAQ)

All snippets assume `TOKEN=$(apl login ms:<handle>)` and `GRAPH=https://graph.microsoft.com/v1.0`. Header `-H "Authorization: Bearer $TOKEN"` is elided for brevity as `$AUTH`.

```bash
TOKEN=$(apl login ms:reqsume)
AUTH=(-H "Authorization: Bearer $TOKEN")
GRAPH=https://graph.microsoft.com/v1.0
```

### Mail

| Q | Call |
|---|---|
| **Unread emails in inbox** | `GET /me/mailFolders/inbox/messages?$filter=isRead eq false&$top=20&$select=subject,from,receivedDateTime` |
| **Today's inbox** | `GET /me/messages?$filter=receivedDateTime ge $(date -u +%Y-%m-%dT00:00:00Z)&$orderby=receivedDateTime desc` |
| **Unread count only** | `GET /me/mailFolders/inbox?$select=unreadItemCount` |
| **Search inbox by keyword** | `GET /me/messages?$search="project kickoff"` + header `ConsistencyLevel: eventual` |
| **Mail from a specific sender** | `GET /me/messages?$filter=from/emailAddress/address eq 'shaama@reqsume.com'&$top=20` |
| **Emails with attachments** | `GET /me/messages?$filter=hasAttachments eq true&$top=20` |
| **List attachments** | `GET /me/messages/{id}/attachments` |
| **Download attachment** | `GET /me/messages/{id}/attachments/{aid}/$value` (binary stream) |
| **Mark as read** | `PATCH /me/messages/{id}` body `{"isRead": true}` |
| **Reply** | `POST /me/messages/{id}/reply` body `{"comment":"thanks"}` |
| **Reply all** | `POST /me/messages/{id}/replyAll` |
| **Forward** | `POST /me/messages/{id}/forward` body `{"toRecipients":[{"emailAddress":{"address":"x@y.com"}}],"comment":"fyi"}` |
| **Move to folder** | `POST /me/messages/{id}/move` body `{"destinationId":"archive"}` (well-known: `inbox`, `sentItems`, `drafts`, `deleteditems`, `archive`, `junkemail`) |
| **List folders** | `GET /me/mailFolders?$top=50` |
| **Sent mail** | `GET /me/mailFolders/sentItems/messages?$top=20` |
| **Drafts** | `GET /me/mailFolders/drafts/messages` |
| **Create a draft** | `POST /me/messages` body `{"subject":"...","body":{"contentType":"Text","content":"..."},"toRecipients":[...]}` |
| **Delta (new since last sync)** | `GET /me/mailFolders/inbox/messages/delta` — follow `@odata.nextLink` until you get `@odata.deltaLink`; persist that and re-use next time |

### Calendar

| Q | Call |
|---|---|
| **Today's invites/meetings** | `GET /me/calendarView?startDateTime=$(date -u +%Y-%m-%dT00:00:00Z)&endDateTime=$(date -u -v+1d +%Y-%m-%dT00:00:00Z)&$select=subject,start,end,organizer,isOnlineMeeting` |
| **This week's meetings** | `GET /me/calendarView?startDateTime=<monday-iso>&endDateTime=<sunday-iso>` |
| **Next 5 upcoming** | `GET /me/events?$filter=start/dateTime ge '$(date -u +%Y-%m-%dT%H:%M:%SZ)'&$orderby=start/dateTime&$top=5` |
| **Create event** | `POST /me/events` body `{"subject":"Sync","start":{"dateTime":"2026-05-01T09:00:00","timeZone":"UTC"},"end":{"dateTime":"2026-05-01T09:30:00","timeZone":"UTC"},"attendees":[{"emailAddress":{"address":"x@y.com"}}],"isOnlineMeeting":true}` |
| **Accept invite** | `POST /me/events/{id}/accept` body `{"sendResponse":true,"comment":"joining"}` |
| **Decline** | `POST /me/events/{id}/decline` body `{"sendResponse":true}` |
| **Tentatively accept** | `POST /me/events/{id}/tentativelyAccept` |
| **Find free slots** | `POST /me/findMeetingTimes` body `{"attendees":[{"emailAddress":{"address":"x@y.com"}}],"timeConstraint":{"timeSlots":[{"start":{"dateTime":"..."},"end":{"dateTime":"..."}}]},"meetingDuration":"PT30M"}` |
| **Free/busy lookup** | `POST /me/calendar/getSchedule` body `{"schedules":["shaama@reqsume.com"],"startTime":{"dateTime":"...","timeZone":"UTC"},"endTime":{"dateTime":"...","timeZone":"UTC"}}` |
| **Cancel event** | `POST /me/events/{id}/cancel` body `{"comment":"conflict"}` |

### Teams meetings

| Q | Call |
|---|---|
| **All online meetings in calendar** | `GET /me/events?$filter=isOnlineMeeting eq true&$orderby=start/dateTime desc&$top=50&$select=subject,start,organizer,onlineMeeting` |
| **Find meeting by subject substring** | Filter client-side after `/me/events?$top=100`. OData's `contains()` does **not** work on events reliably. |
| **Resolve meeting by joinWebUrl** | `GET /me/onlineMeetings?$filter=JoinWebUrl eq '<urlencoded-url>'` (only supported filter; cannot list all meetings) |
| **Get a meeting by videoTeleconferenceId** | `GET /me/onlineMeetings?$filter=VideoTeleconferenceId eq 'X'` |
| **Create an online meeting (ad hoc)** | `POST /me/onlineMeetings` body `{"subject":"Quick sync","startDateTime":"...","endDateTime":"..."}` — returns `joinWebUrl` |
| **Meeting recordings (metadata)** | `GET /me/onlineMeetings/{id}/recordings` (needs `OnlineMeetingRecording.Read.All`) |
| **Meeting transcripts (metadata)** | `GET /me/onlineMeetings/{id}/transcripts` (needs `OnlineMeetingTranscript.Read.All`) |
| **Transcript content (VTT)** | `GET /me/onlineMeetings/{id}/transcripts/{tid}/content?$format=text/vtt` |
| **Recording binary path** | See "Recording mp4" section above — two routes: OneDrive path or meeting-chat iteration |

### Searching transcripts

There is **no** server-side full-text search of meeting transcripts. Two options:

1. **Download-and-grep:** pull the VTT for each meeting in the date range, then `grep -i "keyword"` locally.
2. **Index yourself:** batch-download transcripts nightly into a local index (SQLite FTS, ripgrep, whatever) and query there.

Pseudocode for option 1:
```bash
for event_id in $(curl -s "${AUTH[@]}" "$GRAPH/me/events?\$filter=isOnlineMeeting eq true&\$top=50" \
                  | jq -r '.value[].onlineMeeting.joinUrl' \
                  | grep -v null); do
  # resolve to meeting id then transcript id then $format=text/vtt → grep
done
```

### Teams chats

| Q | Call |
|---|---|
| **All my chats** | `GET /me/chats?$expand=members&$top=50` |
| **Find 1:1 chat with X** | List chats with `chatType eq 'oneOnOne'`, filter client-side by `members[].email == 'x@y.com'` |
| **Find group chat by name** | `GET /me/chats?$filter=chatType eq 'group'` then match `topic` client-side |
| **Find meeting chats** | `GET /me/chats?$filter=chatType eq 'meeting'` |
| **Messages in a chat** | `GET /chats/{chatId}/messages?$top=50` — pagination via `@odata.nextLink` |
| **Only messages after ts** | `GET /chats/{chatId}/messages/delta` — persist `@odata.deltaLink`, use next time for "new since" |
| **All new messages across chats** | `GET /me/chats/getAllMessages` (preview; requires `Chat.Read` or `ChatMessage.Read`; returns a feed across all user's chats) |
| **Search a chat by keyword** | No server-side text search on chat messages — paginate + client-side grep. Global search is only on the mail endpoint. |
| **Send message to existing chat** | `POST /chats/{chatId}/messages` body `{"body":{"content":"hi"}}` |
| **Send HTML message** | `POST /chats/{chatId}/messages` body `{"body":{"contentType":"html","content":"<b>hi</b>"}}` |
| **Start a new 1:1 chat** | `POST /chats` body `{"chatType":"oneOnOne","members":[{"@odata.type":"#microsoft.graph.aadUserConversationMember","roles":["owner"],"user@odata.bind":"https://graph.microsoft.com/v1.0/users('<my-id>')"},{"@odata.type":"#microsoft.graph.aadUserConversationMember","roles":["owner"],"user@odata.bind":"https://graph.microsoft.com/v1.0/users('<their-id>')"}]}` → returns chatId, then POST messages |
| **React to a message** | `POST /chats/{cid}/messages/{mid}/setReaction` body `{"reactionType":"like"}` (or `heart`, `laugh`, `surprised`, `sad`, `angry`) |
| **Remove reaction** | `POST /chats/{cid}/messages/{mid}/unsetReaction` |
| **Reply to a message** | `POST /chats/{cid}/messages/{mid}/replies` body `{"body":{"content":"..."}}` (chat replies are thread-scoped) |
| **Edit a message** | `PATCH /chats/{cid}/messages/{mid}` body `{"body":{"content":"edited"}}` |
| **Delete a message** | `DELETE /chats/{cid}/messages/{mid}/softDelete` |
| **Channel message (Teams channel)** | `POST /teams/{teamId}/channels/{channelId}/messages` body `{"body":{"content":"..."}}` (needs `ChannelMessage.Send`) |

### Users & presence

| Q | Call |
|---|---|
| **My profile** | `GET /me` |
| **Who am I (lean)** | `GET /me?$select=displayName,mail,userPrincipalName,id` |
| **My photo** | `GET /me/photo/$value` (binary, e.g. `-o me.jpg`) |
| **Look up user by email** | `GET /users/x@y.com?$select=id,displayName,mail,jobTitle` |
| **Search directory** | `GET /users?$search="displayName:shaama"` + header `ConsistencyLevel: eventual` |
| **My direct reports** | `GET /me/directReports` |
| **My manager** | `GET /me/manager` |
| **My presence** | `GET /me/presence` |
| **Get others' presence** | `POST /communications/getPresencesByUserId` body `{"ids":["<user-id-1>","<user-id-2>"]}` |
| **Set my presence** | `POST /me/presence/setPresence` body `{"sessionId":"<app-id>","availability":"Busy","activity":"InACall","expirationDuration":"PT1H"}` |

### Drive / files

| Q | Call |
|---|---|
| **My recent files** | `GET /me/drive/recent` |
| **Files shared with me** | `GET /me/drive/sharedWithMe` |
| **Search my drive** | `GET /me/drive/root/search(q='kickoff')` |
| **List a folder** | `GET /me/drive/root:/Documents:/children` |
| **Download file** | `GET /me/drive/items/{itemId}/content` (redirects; use `curl -L`) |
| **Get organizer's OneDrive item** (e.g. meeting recording) | `GET /users/{upn}/drive/root:/Recordings/<filename>` — returns `@microsoft.graph.downloadUrl` |
| **Upload small file** | `PUT /me/drive/root:/path/to/file.txt:/content` with raw bytes |
| **Create folder** | `POST /me/drive/root/children` body `{"name":"New","folder":{},"@microsoft.graph.conflictBehavior":"rename"}` |
| **Share a file** | `POST /me/drive/items/{id}/createLink` body `{"type":"view","scope":"organization"}` |

### Contacts

| Q | Call |
|---|---|
| **My contacts** | `GET /me/contacts?$top=50` |
| **Create contact** | `POST /me/contacts` body `{"givenName":"Shaama","surname":"Manoharan","emailAddresses":[{"address":"shaama@reqsume.com","name":"Shaama"}]}` |
| **Contact folders** | `GET /me/contactFolders` |

### Delta (for "what changed since last sync")

Delta queries let you poll cheaply for changes. Pattern:

1. **First call** — `GET /me/mailFolders/inbox/messages/delta` (or `/me/events/delta`, `/me/chats/getAllMessages`, `/me/drive/root/delta`). Follow `@odata.nextLink` across pages.
2. **Last page** returns `@odata.deltaLink` — persist it (per user+resource).
3. **Next poll** — GET the deltaLink. It returns only items changed/deleted since the prior call.

Supported on: mail folders, events, chat messages (`getAllMessages`), drive items, user directory (`/users/delta`), groups.

### Batching (up to 20 requests in one POST)

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  https://graph.microsoft.com/v1.0/\$batch \
  -d '{
    "requests": [
      {"id":"1","method":"GET","url":"/me"},
      {"id":"2","method":"GET","url":"/me/messages?$top=5&$select=subject"},
      {"id":"3","method":"GET","url":"/me/calendarView?startDateTime=2026-04-23T00:00:00Z&endDateTime=2026-04-24T00:00:00Z"}
    ]
  }'
```

Responses come back keyed by the ids you provided. Great for dashboards where you need mail+calendar+chat state in one round trip.

### Throttling

Graph returns `429 Too Many Requests` with a `Retry-After: <seconds>` header. Honor it. Typical per-app limits are generous (thousands of req/s across a tenant), but individual user buckets are lower. For bulk work, use `$batch`, delta queries, and `$select` to keep payload small.

### URL encoding quirks

- `$filter`, `$select`, `$search` need the literal `$` in the query. Shells usually require single-quotes or a literal escape: `"$GRAPH/me/messages?\$top=5"` (bash) or `'$GRAPH/me/messages?$top=5'` (awkward inside double-quote strings).
- Values inside `$filter` must single-quote strings: `$filter=from/emailAddress/address eq 'x@y.com'`.
- `$search` values must be double-quoted and require `ConsistencyLevel: eventual` header.
- Paths with spaces in OneDrive: URL-encode per path segment, e.g. `/me/drive/root:/Meet%20Recordings:/children`.

## References

- Microsoft Graph API reference — https://learn.microsoft.com/graph/api/overview
- Graph Explorer — https://developer.microsoft.com/graph/graph-explorer
- Delegated permissions reference — https://learn.microsoft.com/graph/permissions-reference
