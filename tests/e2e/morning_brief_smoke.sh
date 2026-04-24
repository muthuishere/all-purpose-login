#!/usr/bin/env bash
#
# End-to-end smoke test for Family 13 — Morning Brief recipes.
#
# Each section exercises BRIEF-1..8 by running the underlying recipes (from
# Families 2, 4, 6, 10, 12) against real tenants. Sections skip cleanly if the
# corresponding handle isn't provided.
#
# Prerequisites:
#   - `apl` binary built (task build)
#   - `gh` auth'd for the GitHub sections (optional — they skip if missing)
#   - Per-provider: `apl login <handle>` already consented
#
# Usage:
#   tests/e2e/morning_brief_smoke.sh [google:handle] [ms:handle]
#
# Exit code is the number of failed sections (0 = all green, skips don't count).

set -u -o pipefail

GOOGLE_HANDLE="${1:-}"
MS_HANDLE="${2:-}"

APL="${APL_BIN:-./bin/apl}"
OUT_DIR="${OUT_DIR:-./.e2e-out}"
mkdir -p "$OUT_DIR"

GRAPH="https://graph.microsoft.com/v1.0"
GMAIL="https://gmail.googleapis.com/gmail/v1/users/me"
CAL="https://www.googleapis.com/calendar/v3"

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

MS_TOKEN=""
GOOGLE_TOKEN=""

if [[ -n "$MS_HANDLE" ]]; then
  if MS_TOKEN=$("$APL" login "$MS_HANDLE" 2>/dev/null) && [[ -n "$MS_TOKEN" ]]; then
    pass "ms token acquired for $MS_HANDLE"
  else
    fail "apl login $MS_HANDLE failed"
    MS_TOKEN=""
  fi
else
  skip "no ms handle given — pass as 2nd arg to enable ms-side recipes"
fi

if [[ -n "$GOOGLE_HANDLE" ]]; then
  if GOOGLE_TOKEN=$("$APL" login "$GOOGLE_HANDLE" 2>/dev/null) && [[ -n "$GOOGLE_TOKEN" ]]; then
    pass "google token acquired for $GOOGLE_HANDLE"
  else
    fail "apl login $GOOGLE_HANDLE failed"
    GOOGLE_TOKEN=""
  fi
else
  skip "no google handle given — pass as 1st arg to enable google-side recipes"
fi

GH_OK=0
if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
  GH_OK=1
  pass "gh CLI authed — GitHub sections enabled"
else
  skip "gh CLI unavailable or not authed — GitHub sections will skip"
fi

# helpers
ms_get() {
  local path="$1"
  [[ -z "$MS_TOKEN" ]] && { echo ""; return 1; }
  curl -s -w '\n%{http_code}' -H "Authorization: Bearer $MS_TOKEN" "$GRAPH$path"
}
google_get() {
  local url="$1"
  [[ -z "$GOOGLE_TOKEN" ]] && { echo ""; return 1; }
  curl -s -w '\n%{http_code}' -H "Authorization: Bearer $GOOGLE_TOKEN" "$url"
}
check200() {
  local label="$1" resp="$2"
  local code body
  code=$(echo "$resp" | tail -n1)
  body=$(echo "$resp" | sed '$d')
  if [[ "$code" == "200" ]]; then
    echo "$body"
    return 0
  else
    fail "$label -> HTTP $code"
    return 1
  fi
}

# ──────────────────────────────────────────────────────────────────────────
# BRIEF-1 — Morning brief (full)
# ──────────────────────────────────────────────────────────────────────────
hdr "BRIEF-1 morning brief (full)"

MS_UNREAD_COUNT=0
GOOGLE_UNREAD_COUNT=0
MS_EVENTS_COUNT=0
GOOGLE_EVENTS_COUNT=0
GH_PR_ASSIGNED=0
GH_PR_REVIEW=0
GH_ISSUE_ASSIGNED=0
GH_ISSUE_MENTION=0

# step 1: MAIL-R-2 unread MS
if [[ -n "$MS_TOKEN" ]]; then
  resp=$(ms_get "/me/mailFolders/inbox/messages?\$filter=isRead%20eq%20false&\$top=25&\$select=subject,from,receivedDateTime" || true)
  if body=$(check200 "MAIL-R-2 (MS unread)" "$resp"); then
    MS_UNREAD_COUNT=$(echo "$body" | python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("value",[])))')
    pass "MS unread: $MS_UNREAD_COUNT"
  fi
else
  skip "MAIL-R-2 — no ms handle"
fi

# step 2: MAIL-R-17 unread Gmail
if [[ -n "$GOOGLE_TOKEN" ]]; then
  resp=$(google_get "$GMAIL/messages?q=is:unread&maxResults=25" || true)
  if body=$(check200 "MAIL-R-17 (Gmail unread)" "$resp"); then
    GOOGLE_UNREAD_COUNT=$(echo "$body" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("resultSizeEstimate",len(d.get("messages",[]))))')
    pass "Gmail unread estimate: $GOOGLE_UNREAD_COUNT"
  fi
else
  skip "MAIL-R-17 — no google handle"
fi

# step 3: CAL-R-1 today's MS events
if [[ -n "$MS_TOKEN" ]]; then
  start=$(date -u +%Y-%m-%dT00:00:00Z 2>/dev/null || date -u -Idate)T00:00:00Z
  end=$(date -u -v+1d +%Y-%m-%dT00:00:00Z 2>/dev/null || date -u -d 'tomorrow' +%Y-%m-%dT00:00:00Z)
  resp=$(ms_get "/me/calendarView?startDateTime=$start&endDateTime=$end&\$orderby=start/dateTime&\$select=subject,start,onlineMeeting" || true)
  if body=$(check200 "CAL-R-1 (MS today)" "$resp"); then
    MS_EVENTS_COUNT=$(echo "$body" | python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("value",[])))')
    pass "MS events today: $MS_EVENTS_COUNT"
  fi
else
  skip "CAL-R-1 — no ms handle"
fi

# step 4: CAL-R-7 today's Google events
if [[ -n "$GOOGLE_TOKEN" ]]; then
  start=$(date -u +%Y-%m-%dT00:00:00Z 2>/dev/null || date -u -Idate)T00:00:00Z
  end=$(date -u -v+1d +%Y-%m-%dT00:00:00Z 2>/dev/null || date -u -d 'tomorrow' +%Y-%m-%dT00:00:00Z)
  resp=$(google_get "$CAL/calendars/primary/events?timeMin=$start&timeMax=$end&singleEvents=true&orderBy=startTime" || true)
  if body=$(check200 "CAL-R-7 (Google today)" "$resp"); then
    GOOGLE_EVENTS_COUNT=$(echo "$body" | python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("items",[])))')
    pass "Google events today: $GOOGLE_EVENTS_COUNT"
  fi
else
  skip "CAL-R-7 — no google handle"
fi

# step 5: CHAT-7 (skipped here — tenant flag; smoke covered in ms_smoke.sh CHAT-1 fallback)
skip "CHAT-7 (all new Teams messages) — preview scope; ms_smoke.sh covers CHAT-1 fallback"

# steps 6-9: GH-6/7/16/17
if [[ $GH_OK -eq 1 ]]; then
  if out=$(gh search prs --state=open --assignee=@me --json number --limit 50 2>/dev/null); then
    GH_PR_ASSIGNED=$(echo "$out" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
    pass "GH-6 PRs assigned: $GH_PR_ASSIGNED"
  else
    fail "GH-6 search failed"
  fi
  if out=$(gh search prs --state=open --review-requested=@me --json number --limit 50 2>/dev/null); then
    GH_PR_REVIEW=$(echo "$out" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
    pass "GH-7 PRs needing review: $GH_PR_REVIEW"
  else
    fail "GH-7 search failed"
  fi
  if out=$(gh search issues --state=open --assignee=@me --json number --limit 50 2>/dev/null); then
    GH_ISSUE_ASSIGNED=$(echo "$out" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
    pass "GH-16 issues assigned: $GH_ISSUE_ASSIGNED"
  else
    fail "GH-16 search failed"
  fi
  if out=$(gh search issues --state=open --mentions=@me --json number --limit 50 2>/dev/null); then
    GH_ISSUE_MENTION=$(echo "$out" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
    pass "GH-17 issues mentioning: $GH_ISSUE_MENTION"
  else
    fail "GH-17 search failed"
  fi
else
  skip "GH-6/7/16/17 — gh unavailable"
fi

# Synthesised brief (demo)
hdr "BRIEF-1 synthesis (demo output)"
cat <<EOF
  Unread mail: $MS_UNREAD_COUNT ms / $GOOGLE_UNREAD_COUNT gmail
  Today's meetings: $MS_EVENTS_COUNT ms / $GOOGLE_EVENTS_COUNT google
  Review queue: $GH_PR_REVIEW PRs, $GH_PR_ASSIGNED assigned
  Issues: $GH_ISSUE_ASSIGNED assigned, $GH_ISSUE_MENTION mentioning me
EOF

# ──────────────────────────────────────────────────────────────────────────
# BRIEF-2 — Delta since last check
# ──────────────────────────────────────────────────────────────────────────
hdr "BRIEF-2 delta since last check"
skip "BRIEF-2 requires saved deltaLink / syncToken / historyId — out of scope for single-run smoke"

# ──────────────────────────────────────────────────────────────────────────
# BRIEF-3 — Unread mail (cross-provider)
# ──────────────────────────────────────────────────────────────────────────
hdr "BRIEF-3 unread mail (cross-provider)"
if [[ -z "$MS_TOKEN" && -z "$GOOGLE_TOKEN" ]]; then
  skip "no provider handles configured"
else
  # Reuse BRIEF-1 results — same underlying calls.
  pass "unread totals: $MS_UNREAD_COUNT ms + $GOOGLE_UNREAD_COUNT gmail (from BRIEF-1)"
fi

# ──────────────────────────────────────────────────────────────────────────
# BRIEF-4 — Today's calendar (cross-provider)
# ──────────────────────────────────────────────────────────────────────────
hdr "BRIEF-4 today's calendar (cross-provider)"
if [[ -z "$MS_TOKEN" && -z "$GOOGLE_TOKEN" ]]; then
  skip "no provider handles configured"
else
  pass "today's event totals: $MS_EVENTS_COUNT ms + $GOOGLE_EVENTS_COUNT google (from BRIEF-1)"
fi

# ──────────────────────────────────────────────────────────────────────────
# BRIEF-5 — My work queue
# ──────────────────────────────────────────────────────────────────────────
hdr "BRIEF-5 my work queue"
if [[ $GH_OK -eq 1 ]]; then
  pass "gh queue: $GH_PR_REVIEW review / $GH_PR_ASSIGNED assigned / $GH_ISSUE_MENTION mentioned (from BRIEF-1)"
else
  skip "gh unavailable"
fi

# MAIL-R-27 — flagged Outlook
if [[ -n "$MS_TOKEN" ]]; then
  resp=$(ms_get "/me/messages?\$filter=flag/flagStatus%20eq%20%27flagged%27&\$top=25&\$select=subject,receivedDateTime,flag" || true)
  if body=$(check200 "MAIL-R-27 (flagged MS)" "$resp"); then
    c=$(echo "$body" | python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("value",[])))')
    pass "flagged MS messages: $c"
  fi
else
  skip "MAIL-R-27 — no ms handle"
fi

# Gmail starred + unread
if [[ -n "$GOOGLE_TOKEN" ]]; then
  resp=$(google_get "$GMAIL/messages?q=is:starred+is:unread&maxResults=25" || true)
  if body=$(check200 "Gmail starred+unread" "$resp"); then
    c=$(echo "$body" | python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("messages",[]) or []))')
    pass "starred+unread Gmail: $c"
  fi
else
  skip "Gmail starred+unread — no google handle"
fi

# ──────────────────────────────────────────────────────────────────────────
# BRIEF-6 — Any new message (lightweight poll)
# ──────────────────────────────────────────────────────────────────────────
hdr "BRIEF-6 any new message since (last 1h)"
SINCE=$(date -u -v-1H +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '1 hour ago' +%Y-%m-%dT%H:%M:%SZ)
if [[ -n "$MS_TOKEN" ]]; then
  resp=$(ms_get "/me/mailFolders/inbox/messages?\$filter=receivedDateTime%20gt%20$SINCE%20and%20isRead%20eq%20false&\$top=25&\$select=subject,from,receivedDateTime" || true)
  if body=$(check200 "MS poll since=$SINCE" "$resp"); then
    c=$(echo "$body" | python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("value",[])))')
    pass "MS new since $SINCE: $c"
  fi
else
  skip "MS poll — no ms handle"
fi

if [[ -n "$GOOGLE_TOKEN" ]]; then
  epoch=$(date -u -j -f "%Y-%m-%dT%H:%M:%SZ" "$SINCE" +%s 2>/dev/null || date -u -d "$SINCE" +%s)
  resp=$(google_get "$GMAIL/messages?q=is:unread+after:$epoch&maxResults=25" || true)
  if body=$(check200 "Gmail poll after=$epoch" "$resp"); then
    c=$(echo "$body" | python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("messages",[]) or []))')
    pass "Gmail new since $SINCE: $c"
  fi
else
  skip "Gmail poll — no google handle"
fi

# ──────────────────────────────────────────────────────────────────────────
# BRIEF-7 — Mentions aggregator
# ──────────────────────────────────────────────────────────────────────────
hdr "BRIEF-7 mentions aggregator"
# Canonical path: fetch chat batch + filter client-side. We just verify CHAT-7 is reachable.
if [[ -n "$MS_TOKEN" ]]; then
  resp=$(ms_get "/me/chats/getAllMessages?\$top=10" || true)
  code=$(echo "$resp" | tail -n1)
  if [[ "$code" == "200" ]]; then
    pass "CHAT-7 reachable for mention inspection"
  elif [[ "$code" == "403" ]]; then
    skip "CHAT-7 403 — fallback to contains(body/content,<display-name>) (documented)"
  else
    fail "CHAT-7 -> HTTP $code"
  fi
else
  skip "CHAT-7 — no ms handle"
fi

if [[ $GH_OK -eq 1 ]]; then
  pass "GH review requests + mentions already verified in BRIEF-1"
fi

# ──────────────────────────────────────────────────────────────────────────
# BRIEF-8 — Today's focus (terse)
# ──────────────────────────────────────────────────────────────────────────
hdr "BRIEF-8 today's focus"
# next MS meeting
NEXT_MS=""
if [[ -n "$MS_TOKEN" ]]; then
  now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  end=$(date -u -v+1d +%Y-%m-%dT00:00:00Z 2>/dev/null || date -u -d 'tomorrow' +%Y-%m-%dT00:00:00Z)
  resp=$(ms_get "/me/calendarView?startDateTime=$now&endDateTime=$end&\$orderby=start/dateTime&\$top=1&\$select=subject,start,onlineMeeting" || true)
  if body=$(check200 "BRIEF-8 next MS meeting" "$resp"); then
    NEXT_MS=$(echo "$body" | python3 -c 'import json,sys; v=json.load(sys.stdin).get("value",[]); print(v[0]["subject"] if v else "")')
  fi
fi

# MS unread count via $count
MS_COUNT="?"
if [[ -n "$MS_TOKEN" ]]; then
  resp=$(curl -s -w '\n%{http_code}' \
    -H "Authorization: Bearer $MS_TOKEN" \
    -H "ConsistencyLevel: eventual" \
    "$GRAPH/me/mailFolders/inbox/messages/\$count?\$filter=isRead%20eq%20false" || true)
  code=$(echo "$resp" | tail -n1)
  body=$(echo "$resp" | sed '$d')
  if [[ "$code" == "200" ]]; then
    MS_COUNT="$body"
    pass "MS unread count: $MS_COUNT"
  else
    fail "MS unread count -> HTTP $code"
  fi
fi

echo "  Next meeting: ${NEXT_MS:-none today}"
echo "  Unread: ${MS_COUNT} ms / ${GOOGLE_UNREAD_COUNT} gmail"
echo "  Review queue: ${GH_PR_REVIEW} PRs waiting"

# wrap
hdr "result"
if [[ $fails -eq 0 ]]; then
  green "all sections passed (skips allowed)"
  exit 0
else
  red "$fails section(s) failed"
  exit $fails
fi
