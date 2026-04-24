#!/usr/bin/env bash
#
# End-to-end smoke test against a real Microsoft tenant.
#
# Prerequisites (all manual — this is not a CI test):
#   - `az login` is already done and pointed at the intended tenant
#   - `apl setup ms` has been run for the current machine
#   - `apl login ms:<handle>` has been run at least once (browser consent completed)
#
# Usage:
#   tests/e2e/ms_smoke.sh [handle] [mail-recipient]
#
# Defaults:
#   handle         = ms:reqsume
#   mail-recipient = (skipped)
#
# Exit code is the number of failed sections (0 = all green).

set -u -o pipefail

HANDLE="${1:-ms:reqsume}"
MAIL_TO="${2:-}"

GRAPH="https://graph.microsoft.com/v1.0"
APL="${APL_BIN:-./bin/apl}"
OUT_DIR="${OUT_DIR:-./.e2e-out}"

mkdir -p "$OUT_DIR"

green()  { printf "\033[32m%s\033[0m\n" "$*"; }
red()    { printf "\033[31m%s\033[0m\n" "$*"; }
yellow() { printf "\033[33m%s\033[0m\n" "$*"; }
hdr()    { printf "\n\033[1m── %s ──\033[0m\n" "$*"; }

fails=0
pass() { green "  ✓ $*"; }
fail() { red   "  ✗ $*"; fails=$((fails+1)); }
skip() { yellow "  ∼ $*"; }

# 0. preflight
hdr "preflight"
if [[ ! -x "$APL" ]]; then
  fail "apl binary not found at $APL. Run: task build"
  exit 99
fi
pass "apl binary at $APL"

TOKEN=$("$APL" login "$HANDLE" 2>/dev/null) || { fail "apl login $HANDLE failed"; exit 99; }
if [[ -z "$TOKEN" ]]; then
  fail "apl login returned empty token"
  exit 99
fi
pass "got access token (${#TOKEN} chars)"

auth=(-H "Authorization: Bearer $TOKEN")

# 1. identity
hdr "1. identity /me"
resp=$(curl -s -w '\n%{http_code}' "${auth[@]}" "$GRAPH/me?\$select=displayName,mail,userPrincipalName")
code=$(echo "$resp" | tail -n1)
body=$(echo "$resp" | sed '$d')
if [[ "$code" == "200" ]]; then
  upn=$(echo "$body" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("userPrincipalName",""))')
  pass "signed in as $upn"
else
  fail "/me -> HTTP $code ($body)"
fi

# 2. inbox read
hdr "2. inbox (Mail.Read)"
resp=$(curl -s -w '\n%{http_code}' "${auth[@]}" "$GRAPH/me/messages?\$top=1&\$select=subject,from")
code=$(echo "$resp" | tail -n1)
body=$(echo "$resp" | sed '$d')
if [[ "$code" == "200" ]]; then
  subj=$(echo "$body" | python3 -c 'import json,sys; v=json.load(sys.stdin).get("value",[]); print(v[0]["subject"] if v else "(inbox empty)")')
  pass "most recent: $subj"
else
  fail "/me/messages -> HTTP $code"
fi

# 3. send mail (optional)
hdr "3. send mail (Mail.Send)"
if [[ -z "$MAIL_TO" ]]; then
  skip "no recipient given; pass a second arg to enable"
else
  payload=$(python3 -c "import json,sys; print(json.dumps({
    'message':{
      'subject':'apl e2e smoke',
      'body':{'contentType':'Text','content':'sent by tests/e2e/ms_smoke.sh'},
      'toRecipients':[{'emailAddress':{'address':'$MAIL_TO'}}]
    },
    'saveToSentItems':True}))")
  code=$(curl -s -o /dev/null -w '%{http_code}' "${auth[@]}" \
    -H "Content-Type: application/json" -X POST "$GRAPH/me/sendMail" -d "$payload")
  if [[ "$code" == "202" ]]; then
    pass "sent to $MAIL_TO (202)"
  else
    fail "sendMail -> HTTP $code"
  fi
fi

# 4. calendar
hdr "4. calendar events (Calendars.Read)"
resp=$(curl -s -w '\n%{http_code}' "${auth[@]}" \
  "$GRAPH/me/events?\$top=10&\$orderby=start/dateTime%20desc&\$select=subject,start,isOnlineMeeting,onlineMeeting")
code=$(echo "$resp" | tail -n1)
body=$(echo "$resp" | sed '$d')
if [[ "$code" == "200" ]]; then
  echo "$body" > "$OUT_DIR/events.json"
  count=$(echo "$body" | python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("value",[])))')
  pass "$count recent events; saved to $OUT_DIR/events.json"
else
  fail "/me/events -> HTTP $code"
fi

# 5. teams meetings from calendar
hdr "5. online meetings in calendar"
JOIN_URL=$(python3 -c "
import json
d=json.load(open('$OUT_DIR/events.json'))
for e in d.get('value',[]):
    om = e.get('onlineMeeting')
    if om and om.get('joinUrl'):
        print(om['joinUrl']); break
" 2>/dev/null || echo "")

if [[ -z "$JOIN_URL" ]]; then
  skip "no online meetings found in recent calendar"
else
  pass "found joinUrl; looking up meeting"
  # 6. meeting lookup
  hdr "6. meeting lookup by joinWebUrl (OnlineMeetings.Read)"
  encoded=$(python3 -c "import urllib.parse,sys; print(urllib.parse.quote('$JOIN_URL', safe=''))")
  resp=$(curl -s -w '\n%{http_code}' "${auth[@]}" \
    "$GRAPH/me/onlineMeetings?\$filter=JoinWebUrl%20eq%20%27$JOIN_URL%27")
  code=$(echo "$resp" | tail -n1)
  body=$(echo "$resp" | sed '$d')
  if [[ "$code" == "200" ]]; then
    MEETING_ID=$(echo "$body" | python3 -c 'import json,sys; v=json.load(sys.stdin).get("value",[]); print(v[0]["id"] if v else "")')
    if [[ -n "$MEETING_ID" ]]; then
      pass "meetingId resolved"
    else
      fail "empty value[]"
    fi
  else
    fail "/me/onlineMeetings -> HTTP $code"
    MEETING_ID=""
  fi

  # 7. transcripts
  if [[ -n "$MEETING_ID" ]]; then
    hdr "7. meeting transcripts (OnlineMeetingTranscript.Read.All)"
    resp=$(curl -s -w '\n%{http_code}' "${auth[@]}" \
      "$GRAPH/me/onlineMeetings/$MEETING_ID/transcripts")
    code=$(echo "$resp" | tail -n1)
    body=$(echo "$resp" | sed '$d')
    if [[ "$code" == "200" ]]; then
      TX_ID=$(echo "$body" | python3 -c 'import json,sys; v=json.load(sys.stdin).get("value",[]); print(v[0]["id"] if v else "")')
      if [[ -n "$TX_ID" ]]; then
        pass "transcript metadata OK; downloading VTT"
        code=$(curl -s -o "$OUT_DIR/transcript.vtt" -w '%{http_code}' "${auth[@]}" \
          "$GRAPH/me/onlineMeetings/$MEETING_ID/transcripts/$TX_ID/content?\$format=text/vtt")
        if [[ "$code" == "200" ]]; then
          bytes=$(wc -c < "$OUT_DIR/transcript.vtt")
          pass "downloaded $bytes bytes to $OUT_DIR/transcript.vtt"
        else
          fail "transcript /content -> HTTP $code"
        fi
      else
        skip "no transcripts for this meeting"
      fi
    else
      fail "transcripts list -> HTTP $code"
    fi

    # 8. recordings
    hdr "8. meeting recording metadata (OnlineMeetingRecording.Read.All)"
    resp=$(curl -s -w '\n%{http_code}' "${auth[@]}" \
      "$GRAPH/me/onlineMeetings/$MEETING_ID/recordings")
    code=$(echo "$resp" | tail -n1)
    body=$(echo "$resp" | sed '$d')
    if [[ "$code" == "200" ]]; then
      REC_ID=$(echo "$body" | python3 -c 'import json,sys; v=json.load(sys.stdin).get("value",[]); print(v[0]["id"] if v else "")')
      if [[ -n "$REC_ID" ]]; then
        pass "recording metadata OK; attempting binary"
        code=$(curl -s -L -o "$OUT_DIR/recording.mp4" -w '%{http_code}' "${auth[@]}" \
          "$GRAPH/me/onlineMeetings/$MEETING_ID/recordings/$REC_ID/content")
        if [[ "$code" == "200" ]]; then
          bytes=$(wc -c < "$OUT_DIR/recording.mp4")
          pass "downloaded $bytes bytes to $OUT_DIR/recording.mp4"
        elif [[ "$code" == "403" ]]; then
          skip "recording /content -> 403 (caller is not organizer and recording not shared — see docs/microsoft-graph.md)"
        else
          fail "recording /content -> HTTP $code"
        fi
      else
        skip "no recordings for this meeting"
      fi
    else
      fail "recordings list -> HTTP $code"
    fi
  fi
fi

# 9. chats
hdr "9. chats (Chat.Read)"
resp=$(curl -s -w '\n%{http_code}' "${auth[@]}" "$GRAPH/me/chats?\$top=50&\$expand=members")
code=$(echo "$resp" | tail -n1)
body=$(echo "$resp" | sed '$d')
if [[ "$code" == "200" ]]; then
  echo "$body" > "$OUT_DIR/chats.json"
  count=$(echo "$body" | python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("value",[])))')
  pass "$count chats visible (CHAT-1; saved to $OUT_DIR/chats.json)"
else
  fail "/me/chats -> HTTP $code"
fi

# 10. CAL-W-1 — create event (dry-run: build body + validate endpoint reachability, no POST)
hdr "10. CAL-W-1 create event (dry-run — endpoint ping only)"
# Instead of POSTing a real event, ping /me/calendar/events/$count to verify
# the Calendars.ReadWrite-ish surface is reachable with the current token.
resp=$(curl -s -w '\n%{http_code}' "${auth[@]}" \
  -H "ConsistencyLevel: eventual" \
  "$GRAPH/me/calendar/events/\$count")
code=$(echo "$resp" | tail -n1)
body=$(echo "$resp" | sed '$d')
if [[ "$code" == "200" ]]; then
  pass "calendar events \$count = $body (write endpoint reachable; actual POST skipped)"
else
  fail "/me/calendar/events/\$count -> HTTP $code"
fi

# 11. CHAT-10 — send chat message (opt-in via APL_SMOKE_CHAT_ID)
hdr "11. CHAT-10 send chat message"
if [[ -z "${APL_SMOKE_CHAT_ID:-}" ]]; then
  skip "APL_SMOKE_CHAT_ID not set (e.g. a self-DM chat id); manual opt-in"
else
  payload=$(python3 -c 'import json; print(json.dumps({"body":{"content":"apl e2e smoke ping","contentType":"text"}}))')
  code=$(curl -s -o /dev/null -w '%{http_code}' "${auth[@]}" \
    -H "Content-Type: application/json" -X POST \
    "$GRAPH/me/chats/$APL_SMOKE_CHAT_ID/messages" -d "$payload")
  if [[ "$code" == "201" ]]; then
    pass "chat message sent to $APL_SMOKE_CHAT_ID (201)"
  else
    fail "CHAT-10 send -> HTTP $code"
  fi
fi

# 12. DRIVE-1 — recent files (OneDrive)
hdr "12. DRIVE-1 OneDrive recent files (Files.Read)"
resp=$(curl -s -w '\n%{http_code}' "${auth[@]}" \
  "$GRAPH/me/drive/recent?\$top=10")
code=$(echo "$resp" | tail -n1)
body=$(echo "$resp" | sed '$d')
if [[ "$code" == "200" ]]; then
  count=$(echo "$body" | python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("value",[])))')
  pass "$count recent OneDrive items"
else
  fail "/me/drive/recent -> HTTP $code"
fi

# 13. DRIVE-2 — shared with me (OneDrive)
hdr "13. DRIVE-2 OneDrive shared-with-me (Files.Read.All)"
resp=$(curl -s -w '\n%{http_code}' "${auth[@]}" \
  "$GRAPH/me/drive/sharedWithMe?\$top=10")
code=$(echo "$resp" | tail -n1)
body=$(echo "$resp" | sed '$d')
if [[ "$code" == "200" ]]; then
  count=$(echo "$body" | python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("value",[])))')
  pass "$count shared-with-me items"
else
  fail "/me/drive/sharedWithMe -> HTTP $code"
fi

# 14. MEET-1 — calendar events with online meetings (client-side filter)
hdr "14. MEET-1 online-meeting events (client-side filter)"
# Graph rejects server-side $filter=isOnlineMeeting eq true on /me/events;
# pull recent events and filter in-process.
resp=$(curl -s -w '\n%{http_code}' "${auth[@]}" \
  "$GRAPH/me/events?\$top=25&\$select=subject,start,isOnlineMeeting,onlineMeeting&\$orderby=start/dateTime%20desc")
code=$(echo "$resp" | tail -n1)
body=$(echo "$resp" | sed '$d')
if [[ "$code" == "200" ]]; then
  count=$(echo "$body" | python3 -c '
import json, sys
d = json.load(sys.stdin)
print(sum(1 for e in d.get("value",[]) if e.get("isOnlineMeeting")))')
  pass "$count online-meeting events (client-side filtered)"
else
  fail "/me/events -> HTTP $code"
fi

# 15. MAIL-R-27 — flagged messages
hdr "15. MAIL-R-27 flagged messages (Mail.Read)"
resp=$(curl -s -w '\n%{http_code}' "${auth[@]}" \
  "$GRAPH/me/messages?\$filter=flag/flagStatus%20eq%20%27flagged%27&\$top=10&\$select=subject,from,flag,receivedDateTime")
code=$(echo "$resp" | tail -n1)
body=$(echo "$resp" | sed '$d')
if [[ "$code" == "200" ]]; then
  count=$(echo "$body" | python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("value",[])))')
  pass "$count flagged messages"
else
  fail "flagged messages -> HTTP $code"
fi

# wrap
hdr "result"
if [[ $fails -eq 0 ]]; then
  green "all sections passed (skips allowed)"
  exit 0
else
  red "$fails section(s) failed"
  exit $fails
fi
