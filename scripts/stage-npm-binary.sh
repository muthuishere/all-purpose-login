#!/usr/bin/env bash
# Copies a single goreleaser-built binary into the matching npm platform sub-package.
# Invoked by .goreleaser.yml's per-build post hook.
#
# Args:
#   $1 — goreleaser target (e.g. darwin_arm64, darwin_amd64_v1, linux_amd64_v1, windows_amd64_v1)
#   $2 — path to the built binary (goreleaser's {{ .Path }})
set -euo pipefail

target="${1:?target required}"
src="${2:?source binary path required}"

repo_root="$(cd "$(dirname "$0")/.." && pwd)"

# Normalize goreleaser target -> npm platform dir.
# goreleaser targets look like: darwin_arm64, darwin_amd64_v1, linux_arm64, linux_amd64_v1, windows_amd64_v1
os="${target%%_*}"
rest="${target#*_}"
arch="${rest%%_*}"

case "$arch" in
  amd64) npm_arch="x64" ;;
  arm64) npm_arch="arm64" ;;
  *) echo "stage-npm-binary: unexpected arch '$arch' in target '$target'" >&2; exit 1 ;;
esac

case "$os" in
  darwin|linux) npm_os="$os" ;;
  windows)      npm_os="windows" ;;
  *) echo "stage-npm-binary: unexpected os '$os' in target '$target'" >&2; exit 1 ;;
esac

dest_dir="$repo_root/npm/platforms/${npm_os}-${npm_arch}/bin"
mkdir -p "$dest_dir"

bin_name="apl"
[ "$os" = "windows" ] && bin_name="apl.exe"

cp "$src" "$dest_dir/$bin_name"
chmod +x "$dest_dir/$bin_name" || true

echo "stage-npm-binary: $src -> $dest_dir/$bin_name"
