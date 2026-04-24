#!/usr/bin/env bash
# publish-npm-local.sh — publish all 6 @muthuishere/apl* packages to npm
# from the local machine using an OTP (for accounts with 2FA enabled).
#
# Usage:
#   bash scripts/publish-npm-local.sh
#
# Prerequisites:
#   1. npm whoami prints 'muthuishere' (run `npm login` first).
#   2. Versions are bumped: `node scripts/bump-npm-version.js 0.1.0`.
#   3. Binaries are staged for all 5 platforms: run goreleaser first.
#      e.g.  goreleaser release --snapshot --clean --skip=publish
#      Each npm/platforms/<platform>/bin/apl must be a real executable
#      (not the placeholder .gitkeep).
#
# The script prompts once for an OTP. If any publish fails mid-batch
# (likely: OTP expired — codes rotate every 30s), it re-prompts and
# retries just that one package.

set -e
cd "$(dirname "$0")/.."

# Fail fast if you're not logged in or binaries aren't staged.
WHOAMI=$(npm whoami 2>/dev/null || true)
if [ -z "$WHOAMI" ]; then
  echo "✗ not logged in to npm. Run: npm login" >&2
  exit 1
fi
echo "→ logged in as: $WHOAMI"

for p in darwin-arm64 darwin-x64 linux-arm64 linux-x64 windows-x64; do
  bin="npm/platforms/$p/bin/apl"
  [ "$p" = "windows-x64" ] && bin="npm/platforms/$p/bin/apl.exe"
  if [ ! -s "$bin" ]; then
    echo "✗ missing binary: $bin" >&2
    echo "  Run: goreleaser release --snapshot --clean --skip=publish" >&2
    exit 1
  fi
done
echo "→ all 5 binaries staged"

# Packages — platform sub-packages FIRST (main's optionalDependencies
# resolve against them), main package LAST.
PACKAGES=(
  "npm/platforms/darwin-arm64"
  "npm/platforms/darwin-x64"
  "npm/platforms/linux-arm64"
  "npm/platforms/linux-x64"
  "npm/platforms/windows-x64"
  "npm"
)

read -s -p "npm 2FA code: " OTP
echo

publish_one() {
  local dir=$1
  (cd "$dir" && npm publish --access public --otp "$OTP")
}

for dir in "${PACKAGES[@]}"; do
  name=$(basename "$dir")
  [ "$dir" = "npm" ] && name="@muthuishere/apl (main)"
  echo ""
  echo "=== publishing $name ==="
  if ! publish_one "$dir"; then
    echo ""
    echo "✗ publish failed for $name — OTP may have expired."
    read -s -p "fresh npm 2FA code: " OTP
    echo
    publish_one "$dir"
  fi
done

echo ""
echo "✓ all 6 packages published. Verify:"
echo "    npm view @muthuishere/apl"
echo "    npm install -g @muthuishere/apl"
