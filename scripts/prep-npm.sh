#!/usr/bin/env bash
# Fallback / idempotent helper: scans goreleaser's dist/ output and copies each
# built binary into the matching npm/platforms/<os>-<arch>/bin/ directory.
#
# The goreleaser post-build hook (stage-npm-binary.sh) handles this during
# `goreleaser release`, but this script can be run manually or from CI if the
# hook path needs bypassing.
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
dist_dir="${1:-$repo_root/dist}"

if [ ! -d "$dist_dir" ]; then
  echo "prep-npm: dist directory not found: $dist_dir" >&2
  exit 1
fi

declare -A MAP=(
  ["darwin_amd64_v1"]="darwin-x64:apl"
  ["darwin_arm64"]="darwin-arm64:apl"
  ["linux_amd64_v1"]="linux-x64:apl"
  ["linux_arm64"]="linux-arm64:apl"
  ["windows_amd64_v1"]="windows-x64:apl.exe"
)

for key in "${!MAP[@]}"; do
  entry="${MAP[$key]}"
  npm_platform="${entry%%:*}"
  bin_name="${entry##*:}"

  # goreleaser usually names the build dir "apl_<target>"; also accept bare "<target>".
  candidate_dirs=(
    "$dist_dir/apl_${key}"
    "$dist_dir/${key}"
  )
  src=""
  for d in "${candidate_dirs[@]}"; do
    if [ -f "$d/$bin_name" ]; then
      src="$d/$bin_name"
      break
    fi
  done

  if [ -z "$src" ]; then
    echo "prep-npm: WARNING no binary found for $key (looked in ${candidate_dirs[*]})" >&2
    continue
  fi

  dest_dir="$repo_root/npm/platforms/${npm_platform}/bin"
  mkdir -p "$dest_dir"
  cp "$src" "$dest_dir/$bin_name"
  chmod +x "$dest_dir/$bin_name" || true
  echo "prep-npm: $src -> $dest_dir/$bin_name"
done
