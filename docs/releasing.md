# Releasing apl

apl ships as:
- **GitHub release** — signed tarballs for 5 platforms (darwin-arm64, darwin-x64, linux-arm64, linux-x64, windows-x64) plus SHA256 checksums.
- **npm scoped packages** — 1 main (`@muthuishere/apl`) + 5 platform-gated sub-packages.

**All releases are cut manually from a local machine.** No GitHub Actions are involved in publishing — only in running tests on push/PR. Decision: fewer moving parts, fewer token secrets, every release is an explicit human action.

## How to release

```bash
bash scripts/release-local.sh 0.2.0
```

The script handles everything in order:

1. Preflight: `npm login`, `gh auth`, goreleaser, clean working tree, tag uniqueness.
2. Bump all 6 package.json files to the new version via `scripts/bump-npm-version.js`.
3. Build 5 cross-compiled binaries via `goreleaser --snapshot` → staged into `npm/platforms/<target>/bin/`.
4. Publish all 6 npm packages in order (sub-packages first, main last). Prompts for OTP once.
5. Commit the version bump, tag `v<version>`, push main + tag.
6. Create the GitHub release with tarballs, zip, and checksums attached.

That's it. One command per release.

## Prerequisites (one-time)

1. `npm login` — your npm credentials cached locally.
2. `gh auth login` — GitHub CLI authed with repo-write + release scope.
3. `brew install goreleaser` — v2.x.
4. `brew install node` — for the bump script.

No `NPM_TOKEN` secret needed. No GitHub Actions publish workflow. Nothing to rotate on an expiry schedule.

## Why not GitHub Actions publish?

Trusted Publishing via OIDC (https://docs.npmjs.com/trusted-publishers) is a legitimate and more secure option for automated publishes. We chose manual-from-local instead because:

- One person, low-volume cadence — the automation tax isn't worth it yet.
- Human-in-the-loop on every publish means an unreviewed commit can't ship to npm.
- No per-package Trusted Publisher config to maintain across 6 packages.
- No secrets on the repo; no CI steps to debug when npm changes policy.

If apl ever moves to multiple maintainers or a faster cadence, revisit: add `.github/workflows/release.yml` triggered on tag, configure Trusted Publisher on each of the 6 packages, delete the manual script.

## First-ever publish (done, 2026-04-24)

Version `0.1.0` was cut from local using an earlier version of this flow. See `git log --grep='apl call'` for the commit that introduced the release tooling.

## Verifying a release

```bash
npm view @muthuishere/apl
npm install -g @muthuishere/apl
apl version
```

The release page: https://github.com/muthuishere/all-purpose-login/releases
