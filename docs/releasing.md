# Releasing apl

apl ships as:
- **GitHub release** — signed tarballs for 5 platforms (darwin-arm64, darwin-x64, linux-arm64, linux-x64, windows-x64).
- **npm scoped packages** — 1 main (`@muthuishere/apl`) + 5 platform-gated sub-packages.

Both are cut from the same git tag.

## Normal release (automated)

```bash
git tag v0.2.0
git push origin v0.2.0
```

That triggers `.github/workflows/release.yml`:

1. goreleaser builds 5 cross-compiled binaries with ldflags stamping version/commit/date.
2. `scripts/stage-npm-binary.sh` copies each into `npm/platforms/<target>/bin/apl`.
3. `scripts/bump-npm-version.js <tag-without-v>` rewrites version + optionalDependencies pins across all 6 package.json files.
4. `npm publish --access public --provenance` each platform sub-package, then the main package last.
5. goreleaser creates the GitHub release with tarballs + SHA256SUMS.

## Prerequisites

### NPM credentials

**Preferred: Trusted Publishing (OIDC, no tokens)**

Per https://docs.npmjs.com/trusted-publishers. One-time setup — visit each of these 6 package settings pages and add a GitHub Actions trusted publisher:

- https://www.npmjs.com/package/@muthuishere/apl/access → **Trusted Publisher**
- https://www.npmjs.com/package/@muthuishere/apl-darwin-arm64/access
- https://www.npmjs.com/package/@muthuishere/apl-darwin-x64/access
- https://www.npmjs.com/package/@muthuishere/apl-linux-arm64/access
- https://www.npmjs.com/package/@muthuishere/apl-linux-x64/access
- https://www.npmjs.com/package/@muthuishere/apl-windows-x64/access

For each: Add publisher → **GitHub Actions**, configure:

| Field | Value |
|---|---|
| Repository owner | `muthuishere` |
| Repository name | `all-purpose-login` |
| Workflow filename | `release.yml` |
| Environment | *(leave blank unless you add an environment gate later)* |

Once every package has its trusted publisher configured, the `NPM_TOKEN` secret becomes unnecessary — the workflow's `id-token: write` permission is enough for npm to accept the publish over OIDC. Delete the secret at that point (`gh secret delete NPM_TOKEN --repo muthuishere/all-purpose-login`).

**Fallback: NPM_TOKEN secret**

A granular automation token with "Bypass 2FA" + publish rights on the `@muthuishere/*` scope. Set via:

```bash
gh secret set NPM_TOKEN --repo muthuishere/all-purpose-login --body '<token>'
```

Token rotation reminder: npm automation tokens cap at 1 year; regenerate before expiry.

## First-ever publish (done, 2026-04-24)

The first release (`0.1.0`) was cut manually from local with `scripts/publish-npm-local.sh` because:

- Trusted Publishing requires the package to already exist on npm.
- The GitHub Actions workflow requires `NPM_TOKEN` first time (chicken-and-egg with Trusted Publishing).

Local publish flow (if ever needed again):

```bash
node scripts/bump-npm-version.js 0.X.Y
goreleaser release --snapshot --clean --skip=publish   # builds 5 targets → npm/platforms/*/bin/
bash scripts/publish-npm-local.sh                       # prompts for OTP if 2FA is on
```

Prefer the tag-triggered workflow for all future releases.

## Verifying a release

```bash
npm view @muthuishere/apl
npm install -g @muthuishere/apl
apl version
```

The release notes should appear at https://github.com/muthuishere/all-purpose-login/releases.
