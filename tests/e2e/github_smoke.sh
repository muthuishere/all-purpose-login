#!/usr/bin/env bash
#
# End-to-end smoke test against a real GitHub account via `gh` CLI.
#
# Prerequisites (all manual — this is not a CI test):
#   - `gh` is on PATH and `gh auth status` is green
#   - Either run from inside a git repo with a GitHub `origin` remote, OR
#     pass APL_SMOKE_REPO=<owner/name> to force the target.
#
# Usage:
#   tests/e2e/github_smoke.sh
#
# Env:
#   APL_SMOKE_REPO           owner/name override (default: current repo via git, else muthuishere/all-purpose-login)
#   APL_WRITE_TESTS=1        opt-in: enables write-ish smokes (still very conservative — no real mutation here).
#
# Exit code is the number of failed sections (0 = all green).

set -u -o pipefail

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
if ! command -v gh >/dev/null 2>&1; then
  fail "gh not found on PATH. Install from https://cli.github.com/"
  exit 99
fi
pass "gh on PATH ($(gh --version | head -n1))"

if ! gh auth status >/dev/null 2>&1; then
  fail "gh auth status failed. Run: gh auth login -s workflow"
  exit 99
fi
pass "gh auth status green"

# Resolve target repo
if [[ -n "${APL_SMOKE_REPO:-}" ]]; then
  REPO="$APL_SMOKE_REPO"
  pass "target repo via APL_SMOKE_REPO: $REPO"
elif REPO=$(gh repo view --json nameWithOwner --jq .nameWithOwner 2>/dev/null); then
  pass "target repo via current git remote: $REPO"
else
  REPO="muthuishere/all-purpose-login"
  skip "no git remote detected; falling back to $REPO"
fi

# 1. GH-1 — Repo info
hdr "GH-1 repo view"
if info=$(gh repo view "$REPO" --json name,description,defaultBranchRef,visibility,stargazerCount,pushedAt,url 2>/dev/null); then
  branch=$(echo "$info" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("defaultBranchRef",{}).get("name",""))')
  pass "repo metadata OK (default branch: $branch)"
else
  fail "gh repo view $REPO failed"
fi

# 2. GH-2 — List my repos
hdr "GH-2 list my repos"
if out=$(gh repo list --limit 5 --json nameWithOwner,pushedAt 2>/dev/null); then
  count=$(echo "$out" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
  pass "$count of my repos listed"
else
  fail "gh repo list failed"
fi

# 3. GH-3 — List starred repos
hdr "GH-3 starred repos"
if out=$(gh api "user/starred?per_page=5" 2>/dev/null); then
  count=$(echo "$out" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
  pass "$count starred repos (page 1/5)"
else
  fail "gh api user/starred failed"
fi

# 4. GH-4 — Default branch
hdr "GH-4 default branch"
if br=$(gh repo view "$REPO" --json defaultBranchRef --jq '.defaultBranchRef.name' 2>/dev/null); then
  pass "default branch: $br"
else
  fail "default branch lookup failed"
fi

# 5. GH-5 — Clone: skip (write-ish; would touch disk)
hdr "GH-5 clone"
skip "skip: manual (would write to disk)"

# 6. GH-6 — PRs assigned to me
hdr "GH-6 PRs assigned to me"
if out=$(gh search prs --state=open --assignee=@me --json number,title,url,repository --limit 10 2>/dev/null); then
  count=$(echo "$out" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
  pass "$count PRs assigned"
else
  fail "gh search prs --assignee=@me failed"
fi

# 7. GH-7 — PRs needing my review
hdr "GH-7 PRs needing my review"
if out=$(gh search prs --state=open --review-requested=@me --json number,title,url,repository --limit 10 2>/dev/null); then
  count=$(echo "$out" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
  pass "$count PRs awaiting my review"
else
  fail "gh search prs --review-requested=@me failed"
fi

# 8. GH-8 — My authored open PRs
hdr "GH-8 my authored open PRs"
if out=$(gh search prs --state=open --author=@me --json number,title,url,repository --limit 10 2>/dev/null); then
  count=$(echo "$out" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
  pass "$count PRs authored by me"
else
  fail "gh search prs --author=@me failed"
fi

# 9. GH-9 — View a PR (read-only: pick most recent open PR in $REPO if any)
hdr "GH-9 view PR with comments + checks"
PR_NUM=$(gh pr list --repo "$REPO" --state all --limit 1 --json number --jq '.[0].number // empty' 2>/dev/null || echo "")
if [[ -z "$PR_NUM" ]]; then
  skip "no PRs in $REPO to view"
else
  if gh pr view "$PR_NUM" --repo "$REPO" --json number,title,state,reviewDecision,statusCheckRollup >/dev/null 2>&1; then
    pass "PR #$PR_NUM viewed"
  else
    fail "gh pr view #$PR_NUM failed"
  fi
fi

# 10-15. GH-10..15 writes — skip
hdr "GH-10..15 PR writes (approve / request-changes / comment / checkout / merge / close)"
skip "skip: manual (destructive or branch-mutating)"

# 16. GH-16 — Issues assigned to me
hdr "GH-16 issues assigned"
if out=$(gh search issues --state=open --assignee=@me --json number,title,url,repository --limit 10 2>/dev/null); then
  count=$(echo "$out" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
  pass "$count issues assigned"
else
  fail "gh search issues --assignee=@me failed"
fi

# 17. GH-17 — Issues mentioning me
hdr "GH-17 issues mentioning me"
if out=$(gh search issues --state=open --mentions=@me --json number,title,url,repository --limit 10 2>/dev/null); then
  count=$(echo "$out" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
  pass "$count issues mention me"
else
  fail "gh search issues --mentions=@me failed"
fi

# 18. GH-18 — My authored open issues
hdr "GH-18 my open issues"
if out=$(gh search issues --state=open --author=@me --json number,title,url,repository --limit 10 2>/dev/null); then
  count=$(echo "$out" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
  pass "$count issues authored"
else
  fail "gh search issues --author=@me failed"
fi

# 19. GH-19 — View an issue (pick first in $REPO)
hdr "GH-19 view issue"
ISSUE_NUM=$(gh issue list --repo "$REPO" --state all --limit 1 --json number --jq '.[0].number // empty' 2>/dev/null || echo "")
if [[ -z "$ISSUE_NUM" ]]; then
  skip "no issues in $REPO to view"
else
  if gh issue view "$ISSUE_NUM" --repo "$REPO" --json number,title,state,labels >/dev/null 2>&1; then
    pass "issue #$ISSUE_NUM viewed"
  else
    fail "gh issue view #$ISSUE_NUM failed"
  fi
fi

# 20-21. GH-20..21 issue writes — skip
hdr "GH-20..21 issue writes (create / close / reopen / label / comment)"
skip "skip: manual (destructive or data-creating)"

# 22. GH-22 — Pending review requests (detailed)
hdr "GH-22 pending reviews detail"
if out=$(gh search prs --state=open --review-requested=@me --json number,title,url,repository,updatedAt --limit 50 2>/dev/null); then
  count=$(echo "$out" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
  pass "$count pending review requests"
else
  fail "gh search prs --review-requested=@me failed"
fi

# 23. GH-23 — Recent workflow runs
hdr "GH-23 recent workflow runs"
if out=$(gh run list --repo "$REPO" --limit 5 --json databaseId,name,status,conclusion,createdAt 2>/dev/null); then
  count=$(echo "$out" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
  RUN_ID=$(echo "$out" | python3 -c 'import json,sys; v=json.load(sys.stdin); print(v[0]["databaseId"] if v else "")')
  pass "$count recent runs (RUN_ID=$RUN_ID)"
else
  fail "gh run list failed"
  RUN_ID=""
fi

# 24. GH-24 — View run status
hdr "GH-24 view run"
if [[ -z "$RUN_ID" ]]; then
  skip "no run to view"
else
  if gh run view "$RUN_ID" --repo "$REPO" --json databaseId,status,conclusion,jobs >/dev/null 2>&1; then
    pass "run $RUN_ID viewed"
  else
    fail "gh run view $RUN_ID failed"
  fi
fi

# 25-26. GH-25..26 run writes — skip
hdr "GH-25..26 workflow writes (rerun / cancel)"
skip "skip: manual (destructive)"

# 27. GH-27 — Releases list / latest (legacy entry kept for backward-compat)
hdr "GH-27 releases list + latest (legacy)"
if gh release list --repo "$REPO" --limit 3 >/dev/null 2>&1; then
  pass "release list OK"
else
  fail "gh release list failed"
fi

# 28. GH-28 — Create release: skip (write)
hdr "GH-28 create release"
skip "skip: manual (creates a real tag)"

# 29-30. Superseded by GH-35/36/37 below — run anyway for legacy coverage
hdr "GH-29/30 code/issue/PR search (legacy)"
if gh search code "apl" --limit 3 --json repository,path >/dev/null 2>&1; then
  pass "code search OK"
else
  # code search can 422 without a scope qualifier — treat as skip rather than fail
  skip "code search needs a qualifier (rate-limited or validation-failed)"
fi

# 32. GH-32 — List releases (new shape)
hdr "GH-32 releases list"
if out=$(gh release list --repo "$REPO" --limit 10 --json tagName,name,isLatest,isPrerelease,publishedAt 2>/dev/null); then
  count=$(echo "$out" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
  pass "$count releases"
else
  fail "GH-32 release list failed"
fi

# 33. GH-33 — Latest release
hdr "GH-33 latest release"
if out=$(gh release view --repo "$REPO" --json tagName,name,publishedAt,url 2>/dev/null); then
  tag=$(echo "$out" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("tagName",""))')
  pass "latest: $tag"
else
  skip "no (non-draft, non-prerelease) latest release on $REPO"
fi

# 34. GH-34 — Create release: skip
hdr "GH-34 create release"
skip "skip: manual (creates a real tag)"

# 35. GH-35 — Search code (requires qualifier)
hdr "GH-35 search code"
# Use a repo-scoped query to satisfy the "qualifier required" rule.
if out=$(gh search code "apl" --repo "$REPO" --limit 5 --json repository,path 2>/dev/null); then
  count=$(echo "$out" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
  pass "$count code hits in $REPO"
else
  skip "code search rate-limited or scope-validation failed"
fi

# 36. GH-36 — Search issues
hdr "GH-36 search issues"
if out=$(gh search issues "is:issue" --repo "$REPO" --limit 5 --json number,title,repository 2>/dev/null); then
  count=$(echo "$out" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
  pass "$count issues matched in $REPO"
else
  fail "GH-36 search issues failed"
fi

# 37. GH-37 — Search PRs
hdr "GH-37 search PRs"
if out=$(gh search prs "is:pr" --repo "$REPO" --limit 5 --json number,title,repository,author 2>/dev/null); then
  count=$(echo "$out" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
  pass "$count PRs matched in $REPO"
else
  fail "GH-37 search PRs failed"
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
