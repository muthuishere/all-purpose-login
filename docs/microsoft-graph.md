# Microsoft Graph URLs — Reference

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

### Gotcha — recording mp4 returns 403

Even with `OnlineMeetingRecording.Read.All` **and** `Files.Read.All` **and** tenant admin consent, `GET /recordings/{id}/content` returns 403 unless the caller is:

1. The meeting **organizer**, OR
2. A user the recording was **explicitly shared with** (via the meeting chat's "Recording" message, or via a direct OneDrive share).

The recording file lives in the organizer's OneDrive; Microsoft gates binary access at the item ACL layer above Graph scopes. No additional delegated scope unlocks this.

**Workarounds:**
- Meeting organizer: save the recording somewhere shared, or explicitly share with the caller from Teams.
- Programmatic: use application-permission (`OnlineMeetingRecording.Read.All` as a Role, not a Scope) — requires admin consent to an app-only token, broader risk.
- Alternative: the meeting chat contains a system message with a share link to the recording. Parse it, follow the share link with `Files.Read.All`.

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

## References

- Microsoft Graph API reference — https://learn.microsoft.com/graph/api/overview
- Graph Explorer — https://developer.microsoft.com/graph/graph-explorer
- Delegated permissions reference — https://learn.microsoft.com/graph/permissions-reference
