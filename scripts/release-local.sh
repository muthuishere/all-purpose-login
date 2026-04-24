#!/usr/bin/env bash
# release-local.sh — manual, all-local release flow.
#
# One-shot:
#   bash scripts/release-local.sh 0.2.0
#
# What it does, in order:
#   1. Preflight (npm login, goreleaser, gh CLI, clean working tree).
#   2. Bump versions across all 6 package.json files.
#   3. goreleaser --snapshot: cross-compile 5 targets into dist/ and
#      stage binaries into npm/platforms/<os-arch>/bin/.
#   4. Publish all 6 npm packages (prompts for OTP once).
#   5. Commit the version bump, tag v<version>, push main + tag.
#   6. Create GitHub release with tarballs + checksums.
#
# No GitHub Actions are involved. No NPM_TOKEN secret needed.

set -e
cd "$(dirname "$0")/.."

VERSION="${1:-}"
if [ -z "$VERSION" ]; then
  echo "usage: bash scripts/release-local.sh <version>" >&2
  echo "example: bash scripts/release-local.sh 0.2.0" >&2
  exit 1
fi

# ── preflight ────────────────────────────────────────────────────────
command -v goreleaser >/dev/null || { echo "✗ goreleaser not on PATH (brew install goreleaser)" >&2; exit 1; }
command -v gh         >/dev/null || { echo "✗ gh CLI not on PATH" >&2; exit 1; }
command -v node       >/dev/null || { echo "✗ node not on PATH" >&2; exit 1; }

who=$(npm whoami 2>/dev/null || true)
[ -z "$who" ] && { echo "✗ npm not logged in — run: npm login" >&2; exit 1; }
echo "→ npm: $who"

gh auth status >/dev/null || { echo "✗ gh not authed — run: gh auth login" >&2; exit 1; }
echo "→ gh:  $(gh api user -q .login)"

if ! git diff --quiet || ! git diff --staged --quiet; then
  echo "✗ working tree is dirty. Commit or stash first." >&2
  exit 1
fi

git fetch --tags >/dev/null 2>&1 || true
if git rev-parse "v$VERSION" >/dev/null 2>&1; then
  echo "✗ tag v$VERSION already exists" >&2
  exit 1
fi

# ── bump ─────────────────────────────────────────────────────────────
echo ""
echo "── bump npm versions to $VERSION ──"
node scripts/bump-npm-version.js "$VERSION"

# ── build ────────────────────────────────────────────────────────────
echo ""
echo "── goreleaser snapshot build (5 targets) ──"
goreleaser release --snapshot --clean --skip=publish

# ── publish npm ──────────────────────────────────────────────────────
echo ""
echo "── publish to npm ──"
bash scripts/publish-npm-local.sh

# ── tag + push ───────────────────────────────────────────────────────
echo ""
echo "── commit + tag + push ──"
git add npm/package.json npm/platforms/*/package.json
git commit -m "release: v$VERSION"
git tag "v$VERSION"
git push origin main
git push origin "v$VERSION"

# ── GH release ───────────────────────────────────────────────────────
echo ""
echo "── create GitHub release v$VERSION ──"
gh release create "v$VERSION" \
  dist/*.tar.gz \
  dist/*.zip \
  dist/checksums.txt \
  --title "v$VERSION" \
  --generate-notes

echo ""
echo "✓ released v$VERSION"
echo "    npm: https://www.npmjs.com/package/@muthuishere/apl/v/$VERSION"
echo "    gh:  https://github.com/muthuishere/all-purpose-login/releases/tag/v$VERSION"
