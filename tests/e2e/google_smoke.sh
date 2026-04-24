#!/usr/bin/env bash
#
# End-to-end smoke test against a real Google account.
#
# Prerequisites (all manual — this is not a CI test):
#   - `gcloud auth login` (or console access) is done so you can run `apl setup google`
#   - `apl setup google` has been run for the current machine
#   - `apl login google:<handle>` has been run at least once (browser consent completed,
#     including drive.readonly scope)
#
# Usage:
#   tests/e2e/google_smoke.sh [handle] [mail-recipient]
#
# Defaults:
#   handle         = google:muthu
#   mail-recipient = (skipped)
#
# Exit code is the number of failed sections (0 = all green).

set -u -o pipefail

HANDLE="${1:-google:muthu}"
MAIL_TO="${2:-}"

GMAIL="https://gmail.googleapis.com/gmail/v1/users/me"
CAL="https://www.googleapis.com/calendar/v3"
DRIVE="https://www.googleapis.com/drive/v3"
PEOPLE="https://people.googleapis.com/v1"
OIDC="https://openidconnect.googleapis.com/v1"

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
hdr "1. identity /v3/userinfo"
resp=$(curl -s -w '\n%{http_code}' "${auth[@]}" "$OIDC/userinfo")
code=$(echo "$resp" | tail -n1)
body=$(echo "$resp" | sed '$d')
if [[ "$code" == "200" ]]; then
  email=$(echo "$body" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("email",""))')
  pass "signed in as $email"
else
  fail "/userinfo -> HTTP $code ($body)"
fi

# 2. gmail list
hdr "2. gmail inbox (gmail.readonly)"
resp=$(curl -s -w '\n%{http_code}' "${auth[@]}" "$GMAIL/messages?maxResults=1")
code=$(echo "$resp" | tail -n1)
body=$(echo "$resp" | sed '$d')
if [[ "$code" == "200" ]]; then
  MSG_ID=$(echo "$body" | python3 -c 'import json,sys; v=json.load(sys.stdin).get("messages",[]); print(v[0]["id"] if v else "")')
  if [[ -n "$MSG_ID" ]]; then
    meta=$(curl -s "${auth[@]}" \
      "$GMAIL/messages/$MSG_ID?format=metadata&metadataHeaders=Subject&metadataHeaders=From")
    subj=$(echo "$meta" | python3 -c '
import json, sys
d = json.load(sys.stdin)
h = {x["name"]: x["value"] for x in d.get("payload",{}).get("headers",[])}
print(h.get("Subject","(no subject)"))')
    pass "most recent: $subj"
  else
    skip "inbox empty"
  fi
else
  fail "/messages -> HTTP $code"
fi

# 3. send mail (optional)
hdr "3. send mail (gmail.send)"
if [[ -z "$MAIL_TO" ]]; then
  skip "no recipient given; pass a second arg to enable"
else
  raw=$(python3 -c "
import base64
from email.message import EmailMessage
msg = EmailMessage()
msg['To'] = '$MAIL_TO'
msg['Subject'] = 'apl e2e smoke'
msg.set_content('sent by tests/e2e/google_smoke.sh')
print(base64.urlsafe_b64encode(bytes(msg)).decode().rstrip('='))")
  payload=$(python3 -c "import json,sys; print(json.dumps({'raw':'$raw'}))")
  resp=$(curl -s -w '\n%{http_code}' "${auth[@]}" \
    -H "Content-Type: application/json" -X POST "$GMAIL/messages/send" -d "$payload")
  code=$(echo "$resp" | tail -n1)
  body=$(echo "$resp" | sed '$d')
  if [[ "$code" == "200" ]]; then
    mid=$(echo "$body" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("id",""))')
    pass "sent to $MAIL_TO (id=$mid)"
  else
    fail "messages/send -> HTTP $code ($body)"
  fi
fi

# 4. calendar
hdr "4. calendar events (calendar.readonly)"
resp=$(curl -s -w '\n%{http_code}' "${auth[@]}" \
  "$CAL/calendars/primary/events?maxResults=10&timeMin=2026-01-01T00:00:00Z&singleEvents=true&orderBy=startTime")
code=$(echo "$resp" | tail -n1)
body=$(echo "$resp" | sed '$d')
if [[ "$code" == "200" ]]; then
  echo "$body" > "$OUT_DIR/events.json"
  count=$(echo "$body" | python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("items",[])))')
  pass "$count events since 2026-01-01; saved to $OUT_DIR/events.json"
else
  fail "events list -> HTTP $code"
fi

# 5. Meet event extraction
hdr "5. Meet-enabled events"
MEET_INFO=$(python3 -c "
import json
try:
    d = json.load(open('$OUT_DIR/events.json'))
except Exception:
    print(''); raise SystemExit
for e in d.get('items', []):
    cd = e.get('conferenceData') or {}
    if cd.get('conferenceId') or e.get('hangoutLink'):
        subj = e.get('summary','(no subject)')
        start = (e.get('start') or {}).get('dateTime') or (e.get('start') or {}).get('date','')
        print(f'{subj}\t{start}')
        break
" 2>/dev/null || echo "")

if [[ -z "$MEET_INFO" ]]; then
  skip "no Meet-enabled events found in calendar since 2026-01-01"
else
  subj=$(echo "$MEET_INFO" | cut -f1)
  start=$(echo "$MEET_INFO" | cut -f2)
  pass "Meet event: $subj @ $start"
fi

# 6. Drive list
hdr "6. drive recent files (drive.readonly)"
resp=$(curl -s -w '\n%{http_code}' "${auth[@]}" \
  "$DRIVE/files?pageSize=10&fields=files(id,name,mimeType,size,createdTime)&orderBy=modifiedTime%20desc")
code=$(echo "$resp" | tail -n1)
body=$(echo "$resp" | sed '$d')
if [[ "$code" == "200" ]]; then
  count=$(echo "$body" | python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("files",[])))')
  pass "$count files visible"
else
  fail "files list -> HTTP $code ($body)"
fi

# 7. Drive search for Meet recordings
hdr "7. drive search for Meet recordings"
q=$(python3 -c "import urllib.parse; print(urllib.parse.quote(\"name contains 'Recording' and mimeType='video/mp4'\"))")
resp=$(curl -s -w '\n%{http_code}' "${auth[@]}" \
  "$DRIVE/files?q=$q&pageSize=5&fields=files(id,name,mimeType,size,createdTime,webViewLink,owners)")
code=$(echo "$resp" | tail -n1)
body=$(echo "$resp" | sed '$d')
if [[ "$code" == "200" ]]; then
  first=$(echo "$body" | python3 -c 'import json,sys; v=json.load(sys.stdin).get("files",[]); print(v[0]["name"] if v else "")')
  if [[ -n "$first" ]]; then
    echo "$body" | python3 -c 'import json,sys; v=json.load(sys.stdin).get("files",[]); print(json.dumps(v[0],indent=2))' \
      > "$OUT_DIR/meet_recording_meta.json"
    pass "found: $first (metadata at $OUT_DIR/meet_recording_meta.json; binary not downloaded)"
  else
    skip "no Meet recordings visible"
  fi
else
  fail "drive search -> HTTP $code ($body)"
fi

# 8. Contacts
hdr "8. contacts (contacts.readonly)"
resp=$(curl -s -w '\n%{http_code}' "${auth[@]}" \
  "$PEOPLE/people/me/connections?personFields=names,emailAddresses&pageSize=100")
code=$(echo "$resp" | tail -n1)
body=$(echo "$resp" | sed '$d')
if [[ "$code" == "200" ]]; then
  count=$(echo "$body" | python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("connections",[])))')
  pass "$count contacts visible"
else
  fail "people connections -> HTTP $code ($body)"
fi

# 9. MAIL-R-25 — list labels (Gmail)
hdr "9. MAIL-R-25 list labels (gmail.readonly)"
resp=$(curl -s -w '\n%{http_code}' "${auth[@]}" "$GMAIL/labels")
code=$(echo "$resp" | tail -n1)
body=$(echo "$resp" | sed '$d')
if [[ "$code" == "200" ]]; then
  count=$(echo "$body" | python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("labels",[])))')
  pass "$count labels"
else
  fail "/labels -> HTTP $code"
fi

# 10. MAIL-W-6 — create draft, then delete it (gmail.compose)
hdr "10. MAIL-W-6 create draft + cleanup"
raw=$(python3 -c "
import base64
from email.message import EmailMessage
msg = EmailMessage()
msg['To'] = 'noreply@example.com'
msg['Subject'] = 'apl e2e draft smoke (auto-deleted)'
msg.set_content('draft created by tests/e2e/google_smoke.sh; auto-deleted')
print(base64.urlsafe_b64encode(bytes(msg)).decode().rstrip('='))")
payload=$(python3 -c "import json; print(json.dumps({'message':{'raw':'$raw'}}))")
resp=$(curl -s -w '\n%{http_code}' "${auth[@]}" \
  -H "Content-Type: application/json" -X POST "$GMAIL/drafts" -d "$payload")
code=$(echo "$resp" | tail -n1)
body=$(echo "$resp" | sed '$d')
if [[ "$code" == "200" ]]; then
  DRAFT_ID=$(echo "$body" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("id",""))')
  pass "draft created (id=$DRAFT_ID); deleting..."
  del_code=$(curl -s -o /dev/null -w '%{http_code}' "${auth[@]}" \
    -X DELETE "$GMAIL/drafts/$DRAFT_ID")
  if [[ "$del_code" == "204" ]]; then
    pass "draft deleted (204)"
  else
    fail "draft delete -> HTTP $del_code"
  fi
else
  fail "drafts create -> HTTP $code ($body)"
fi

# 11. CAL-W-8 — quickAdd event, then delete
hdr "11. CAL-W-13 quickAdd event + cleanup"
text="apl e2e smoke test at tomorrow 10am (auto-deleted)"
q=$(python3 -c "import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1]))" "$text")
resp=$(curl -s -w '\n%{http_code}' "${auth[@]}" \
  -X POST "$CAL/calendars/primary/events/quickAdd?text=$q")
code=$(echo "$resp" | tail -n1)
body=$(echo "$resp" | sed '$d')
if [[ "$code" == "200" ]]; then
  EVT_ID=$(echo "$body" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("id",""))')
  pass "event created (id=$EVT_ID); deleting..."
  del_code=$(curl -s -o /dev/null -w '%{http_code}' "${auth[@]}" \
    -X DELETE "$CAL/calendars/primary/events/$EVT_ID")
  if [[ "$del_code" == "204" ]]; then
    pass "event deleted (204)"
  else
    fail "event delete -> HTTP $del_code"
  fi
else
  fail "quickAdd -> HTTP $code ($body)"
fi

# 12. DRIVE — search by MIME type (folders)
hdr "12. DRIVE-15 search by MIME type (folders)"
q=$(python3 -c "import urllib.parse; print(urllib.parse.quote(\"mimeType='application/vnd.google-apps.folder'\"))")
resp=$(curl -s -w '\n%{http_code}' "${auth[@]}" \
  "$DRIVE/files?q=$q&pageSize=5&fields=files(id,name,mimeType)")
code=$(echo "$resp" | tail -n1)
body=$(echo "$resp" | sed '$d')
if [[ "$code" == "200" ]]; then
  count=$(echo "$body" | python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("files",[])))')
  pass "$count folders"
else
  fail "folder search -> HTTP $code"
fi

# 13. DRIVE — shared with me
hdr "13. DRIVE-16 shared-with-me"
q=$(python3 -c "import urllib.parse; print(urllib.parse.quote('sharedWithMe=true'))")
resp=$(curl -s -w '\n%{http_code}' "${auth[@]}" \
  "$DRIVE/files?q=$q&pageSize=5&fields=files(id,name,mimeType,owners)")
code=$(echo "$resp" | tail -n1)
body=$(echo "$resp" | sed '$d')
if [[ "$code" == "200" ]]; then
  count=$(echo "$body" | python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("files",[])))')
  pass "$count shared-with-me files"
else
  fail "shared-with-me -> HTTP $code"
fi

# 14. DRIVE-20 — export Google Doc to PDF (opt-in via APL_SMOKE_DOC_ID)
hdr "14. DRIVE-20 export Google Doc to PDF"
if [[ -z "${APL_SMOKE_DOC_ID:-}" ]]; then
  skip "APL_SMOKE_DOC_ID not set (fileId of a Google Doc you own)"
else
  code=$(curl -s -o "$OUT_DIR/doc_export.pdf" -w '%{http_code}' "${auth[@]}" \
    "$DRIVE/files/$APL_SMOKE_DOC_ID/export?mimeType=application/pdf")
  if [[ "$code" == "200" ]]; then
    bytes=$(wc -c < "$OUT_DIR/doc_export.pdf")
    pass "exported $bytes bytes to $OUT_DIR/doc_export.pdf"
  else
    fail "export -> HTTP $code"
  fi
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
