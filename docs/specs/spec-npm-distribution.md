# Spec: NPM Distribution + Release Automation

**Status:** Proposed
**Priority:** High
**Depends on:** `spec-cli-command-surface.md` (for `apl version` contract)

---

## Problem

`apl` is a Go CLI at `github.com/muthuishere/all-purpose-login`. Prebuilt binaries are attached to GitHub Releases, which is fine for a "curl the tarball" install but awkward for the primary onboarding path: the companion agent skill `all-purpose-data-skill` (hosted in `github.com/muthuishere-agent-skills`) will tell users to install `apl` with a single command.

Every dev machine we care about already has `node` and `npm`. A scoped npm package (`@muthuishere/apl`) is the shortest path from "read the skill's README" to "have `apl` on PATH". GitHub Releases remain the canonical source of prebuilt binaries; npm is a thin redistribution layer on top.

We also need CI/CD: `go test` on push, and a tag-triggered release that (a) builds 5 platform targets with goreleaser, (b) attaches tarballs to the GitHub Release, and (c) publishes 6 synchronized npm packages.

---

## Goals

- `npm install -g @muthuishere/apl` puts the correct prebuilt `apl` binary on PATH for macOS (arm64, x64), Linux (arm64, x64), and Windows (x64).
- Install works in sandboxed environments that disable npm lifecycle scripts (CI runners with `--ignore-scripts`, Snyk/Dependabot environments, npm's `ignore-scripts=true` config).
- One git tag (`vX.Y.Z`) drives the entire release: GitHub Release tarballs + 6 npm packages, all at the same version.
- `apl version` continues to print `version / commit / date` from existing Go `internal/version` ldflags, unchanged.
- Local Go workflow untouched: `task build` and `go build ./cmd/apl` still work with no npm involvement.

---

## Non-Goals

- Homebrew tap (tracked in PRD §9 for a later pass).
- `curl | sh` one-liner installer.
- `apl self-update` — npm handles upgrade cleanly (`npm update -g @muthuishere/apl`).
- Windows MSI / macOS `.pkg` installers.
- ARM32, 386, FreeBSD, or other long-tail targets.
- Private npm scope. Package is public.

---

## Design

### Pattern: per-platform optional dependencies

Two mainstream patterns exist for shipping native binaries via npm:

- **Pattern A — postinstall download.** A `postinstall` script detects OS+arch, downloads the matching binary from GitHub Releases, places it in the package's `bin/` directory.
- **Pattern B — per-arch optional deps.** Ship N platform-specific sub-packages gated by npm's `os`/`cpu` fields. The main package lists them in `optionalDependencies`; npm's resolver installs only the matching one. No postinstall scripting.

**This spec adopts Pattern B.** Rationale:

1. Works when lifecycle scripts are disabled (`npm ci --ignore-scripts`, Snyk, locked-down CI).
2. No runtime download at install time → works offline after first cache, and has no "GitHub is down = install fails" coupling.
3. Simpler to reason about: install is declarative, not imperative.
4. Canonical pattern — used by `esbuild`, `@swc/core`, `@biomejs/biome`, `turbo`, `rollup`. Reviewers recognize it on sight.

The tradeoff is that npm still downloads the other platforms' metadata (a few KB each) during resolution. Acceptable.

### Package layout

Everything lives in this repo alongside the Go source — monorepo-style, one tag releases everything.

```
all-purpose-login/
├── cmd/apl/                         # existing Go entry
├── internal/                        # existing Go code
├── npm/                             # NEW — npm package root
│   ├── package.json                 # @muthuishere/apl (main, aggregator)
│   ├── README.md                    # links back to repo + docs
│   ├── bin/
│   │   └── apl                      # Node JS shim (see NPM-3)
│   └── platforms/
│       ├── darwin-arm64/
│       │   ├── package.json         # @muthuishere/apl-darwin-arm64
│       │   └── bin/apl              # goreleaser drops binary here pre-publish
│       ├── darwin-x64/
│       │   ├── package.json
│       │   └── bin/apl
│       ├── linux-arm64/
│       │   ├── package.json
│       │   └── bin/apl
│       ├── linux-x64/
│       │   ├── package.json
│       │   └── bin/apl
│       └── windows-x64/
│           ├── package.json
│           └── bin/apl.exe
├── .goreleaser.yml                  # NEW
└── .github/
    └── workflows/
        ├── test.yml                 # NEW — push/PR
        └── release.yml              # NEW — tag push
```

### Main package — `npm/package.json` (sketch)

```json
{
  "name": "@muthuishere/apl",
  "version": "0.0.0",
  "description": "apl — OAuth token broker + HTTP relay for Google + Microsoft productivity APIs",
  "homepage": "https://github.com/muthuishere/all-purpose-login",
  "repository": { "type": "git", "url": "https://github.com/muthuishere/all-purpose-login.git" },
  "license": "MIT",
  "bin": { "apl": "./bin/apl" },
  "files": ["bin/"],
  "optionalDependencies": {
    "@muthuishere/apl-darwin-arm64": "0.0.0",
    "@muthuishere/apl-darwin-x64":   "0.0.0",
    "@muthuishere/apl-linux-arm64":  "0.0.0",
    "@muthuishere/apl-linux-x64":    "0.0.0",
    "@muthuishere/apl-windows-x64":  "0.0.0"
  },
  "os":  ["darwin", "linux", "win32"],
  "cpu": ["arm64", "x64"],
  "engines": { "node": ">=18" }
}
```

Version `0.0.0` is a placeholder; the release workflow rewrites it to match the git tag before publishing (see CI-4).

### Platform sub-package — e.g. `npm/platforms/darwin-arm64/package.json`

```json
{
  "name": "@muthuishere/apl-darwin-arm64",
  "version": "0.0.0",
  "description": "apl prebuilt binary for darwin-arm64",
  "repository": { "type": "git", "url": "https://github.com/muthuishere/all-purpose-login.git" },
  "license": "MIT",
  "os":  ["darwin"],
  "cpu": ["arm64"],
  "files": ["bin/"]
}
```

Five such sub-packages, one per target. The Windows one uses `"os": ["win32"]` and ships `bin/apl.exe`.

### The launcher shim — `npm/bin/apl`

A ~30-line Node script. It's the `bin` entry of the main package, so npm symlinks it onto PATH as `apl`. Its job:

1. Determine the expected sub-package name from `process.platform` + `process.arch` (e.g. `@muthuishere/apl-darwin-arm64`).
2. `require.resolve` that sub-package's `package.json` to find its install path.
3. Locate the binary inside it (`bin/apl`, or `bin/apl.exe` on Windows).
4. `child_process.spawnSync` the binary with the current args and `stdio: 'inherit'`, then `process.exit` with its exit code.

If the sub-package isn't resolvable (unsupported platform, or `--ignore-scripts` was used with `--no-optional`), print a single clear error to stderr listing supported platforms and the exact npm command to fix it, then exit 1.

The `esbuild` implementation (`npm/esbuild/bin/esbuild`) is the canonical reference — copy its shape.

### goreleaser config — `.goreleaser.yml` (sketch)

Responsibilities:

- Build 5 targets: `darwin/amd64`, `darwin/arm64`, `linux/amd64`, `linux/arm64`, `windows/amd64`.
- Use flags `-trimpath -buildvcs=false` and the existing ldflags that inject version/commit/date into `internal/version`.
- Produce GitHub Release artifacts (`.tar.gz` for unix, `.zip` for windows) with a checksums file.
- As a **post-build hook per target**, copy the freshly built binary into the corresponding `npm/platforms/<target>/bin/` path so the npm publish step in the workflow finds it without re-running a build.

Shape:

```yaml
version: 2
project_name: apl

builds:
  - id: apl
    main: ./cmd/apl
    binary: apl
    env: [CGO_ENABLED=0]
    flags: [-trimpath, -buildvcs=false]
    ldflags:
      - -s -w
      - -X github.com/muthuishere/all-purpose-login/internal/version.Version={{.Version}}
      - -X github.com/muthuishere/all-purpose-login/internal/version.Commit={{.Commit}}
      - -X github.com/muthuishere/all-purpose-login/internal/version.Date={{.Date}}
    goos:   [darwin, linux, windows]
    goarch: [amd64, arm64]
    ignore:
      - { goos: windows, goarch: arm64 }
    hooks:
      post:
        - cmd: scripts/stage-npm-binary.sh {{ .Target }} {{ .Path }}

archives:
  - id: default
    format_overrides:
      - { goos: windows, format: zip }

checksum: { name_template: "checksums.txt" }
release: { github: { owner: muthuishere, name: all-purpose-login } }
snapshot: { name_template: "{{ .Version }}-snapshot" }
```

`scripts/stage-npm-binary.sh` is a short helper that maps goreleaser's `{{ .Target }}` (e.g. `darwin_arm64_v8.0`) to the npm sub-package directory (`npm/platforms/darwin-arm64/bin/`) and copies the binary.

### Test workflow — `.github/workflows/test.yml` (sketch)

Triggered on `push` to `main` and on `pull_request`.

```yaml
name: test
on:
  push:    { branches: [main] }
  pull_request:
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.24.1' }
      - run: go vet ./...
      - run: gofmt -l . | tee /tmp/fmt && [ ! -s /tmp/fmt ]
      - run: go test ./...
      - run: go build -o /tmp/apl ./cmd/apl
  cross-compile:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        include:
          - { goos: darwin,  goarch: amd64 }
          - { goos: darwin,  goarch: arm64 }
          - { goos: linux,   goarch: amd64 }
          - { goos: linux,   goarch: arm64 }
          - { goos: windows, goarch: amd64 }
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.24.1' }
      - env: { GOOS: '${{ matrix.goos }}', GOARCH: '${{ matrix.goarch }}', CGO_ENABLED: '0' }
        run: go build -trimpath -o /tmp/apl ./cmd/apl
```

### Release workflow — `.github/workflows/release.yml` (sketch)

Triggered on tag push `v*`.

```yaml
name: release
on:
  push:
    tags: ['v*']
permissions:
  contents: write   # create GitHub release
  id-token: write   # npm provenance
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - uses: actions/setup-go@v5
        with: { go-version: '1.24.1' }
      - uses: actions/setup-node@v4
        with:
          node-version: '20'
          registry-url: 'https://registry.npmjs.org'

      # Sanity
      - run: go test ./...
      - run: go build ./cmd/apl

      # Build all targets + attach GitHub Release + stage npm binaries
      - uses: goreleaser/goreleaser-action@v6
        with: { version: '~> v2', args: 'release --clean' }
        env: { GITHUB_TOKEN: '${{ secrets.GITHUB_TOKEN }}' }

      # Sync versions across main + 5 sub-packages to match tag (strip leading v)
      - name: Sync npm versions
        run: node scripts/sync-npm-version.js "${GITHUB_REF_NAME#v}"

      # Publish sub-packages FIRST so main's optionalDependencies resolve
      - name: Publish platform sub-packages
        run: |
          for p in npm/platforms/*/; do
            npm publish "$p" --access public --provenance
          done
        env: { NODE_AUTH_TOKEN: '${{ secrets.NPM_TOKEN }}' }

      # Then publish main
      - name: Publish main package
        run: npm publish npm/ --access public --provenance
        env: { NODE_AUTH_TOKEN: '${{ secrets.NPM_TOKEN }}' }
```

---

## Functional Requirements

### NPM-1 — Scoped name

The main package publishes as `@muthuishere/apl` on the public npm registry. The five platform sub-packages publish as `@muthuishere/apl-<os>-<arch>`.

### NPM-2 — Platform sub-packages with os/cpu gating

Five sub-packages are published per release: `apl-darwin-arm64`, `apl-darwin-x64`, `apl-linux-arm64`, `apl-linux-x64`, `apl-windows-x64`. Each declares matching `"os"` and `"cpu"` fields so npm's resolver silently skips the non-matching ones on install.

### NPM-3 — Main package delegates via Node shim

The main package's `bin/apl` is a Node script (not the Go binary). It resolves the installed platform sub-package via `require.resolve` and `spawnSync`s its binary with `stdio: 'inherit'`, propagating the exit code. No native binary ships in the main package.

### NPM-4 — Version synchronization from git tag

At release time, the workflow rewrites `version` in all 6 `package.json` files (main + 5 sub-packages) to `${GITHUB_REF_NAME#v}`. The main package's `optionalDependencies` block is rewritten in lock-step so each sub-package pin matches the tag exactly. All 6 packages publish at the same version.

### NPM-5 — README and metadata

The main package ships a `README.md` that links to the GitHub repo, the PRD, and the command surface spec. `repository`, `homepage`, and `license` fields are populated.

### NPM-6 — No postinstall scripts required

`npm install -g @muthuishere/apl` works with `--ignore-scripts`. The main package has no `postinstall`, no `preinstall`, no `install` script. This is the primary reason Pattern B was chosen over Pattern A.

### NPM-7 — Clean uninstall

`npm uninstall -g @muthuishere/apl` removes the shim, the sub-packages (npm garbage-collects them since nothing else depends on them), and the PATH symlink. No state outside `$(npm prefix -g)` is written by install/uninstall.

### NPM-8 — `apl version` parity

The binary shipped via npm is byte-identical to the one on the corresponding GitHub Release tarball. `apl version` prints `version / commit / date` populated via the same ldflags path that `task build` uses today. No npm-specific version string.

### NPM-9 — Windows support

Documented and included in the matrix (`windows-x64`). The sub-package ships `bin/apl.exe`; the shim detects `process.platform === 'win32'` and resolves the `.exe`. ARM64 Windows is explicitly out of scope for v1.

### NPM-10 — Audit-clean dependency graph

The main package declares only the 5 platform sub-packages as `optionalDependencies`. Zero runtime `dependencies`, zero `devDependencies` in the published artifacts. `npm audit` returns clean.

### CI-1 — Test workflow on push/PR

`.github/workflows/test.yml` runs `gofmt -l` check, `go vet ./...`, `go test ./...`, and `go build ./cmd/apl` on every push to `main` and every pull request. A cross-compile matrix builds all 5 release targets to catch platform-specific breakage before tagging.

### CI-2 — Release workflow on tag push

`.github/workflows/release.yml` runs on `push: tags: ['v*']`. No other trigger. Manual `workflow_dispatch` is not wired — the only way to cut a release is to push a tag.

### CI-3 — Required secrets

- `NPM_TOKEN` — npm automation token with publish rights on the `@muthuishere` scope. Stored as a GitHub Actions secret.
- `GITHUB_TOKEN` — default, provided by Actions; used by goreleaser to create the Release and upload tarballs.

No other secrets are required. Both are documented in the repo README under a "Release process" section.

### CI-4 — Reproducible-ish builds

All Go builds use `-trimpath -buildvcs=false` and a fixed set of ldflags. Two invocations of goreleaser on the same tag commit produce identical binaries modulo the build-date stamp, which is derived from the git commit timestamp (not wall-clock). This is the strongest reproducibility guarantee Go offers without vendor lockdown.

### CI-5 — Local verification of release config

Running `goreleaser release --snapshot --clean` locally:

- Populates `dist/` with all 5 target tarballs.
- Populates `npm/platforms/*/bin/apl` (and `apl.exe`) with the staged binaries.
- Does **not** publish, tag, or push.

This lets a human verify the release plumbing end-to-end before pushing a real tag.

---

## Acceptance Criteria

1. On a fresh macOS arm64 or Linux x64 machine with Node 20+, `npm install -g @muthuishere/apl@<version>` completes with zero errors and `apl version` on PATH prints `<version>`, the expected commit, and a date.
2. `npm install -g @muthuishere/apl@<version> --ignore-scripts` works identically (Pattern B requirement).
3. `npm uninstall -g @muthuishere/apl` leaves nothing behind in `$(npm prefix -g)/bin`, `$(npm prefix -g)/lib/node_modules`, or the user's home directory.
4. Pushing tag `vX.Y.Z` results in, within ~5 minutes:
   - A GitHub Release at `vX.Y.Z` with 5 platform tarballs + `checksums.txt`.
   - 6 npm packages published at version `X.Y.Z` (main + 5 sub-packages).
   - `npm view @muthuishere/apl version` returns `X.Y.Z`.
5. The `apl` binary extracted from any GitHub Release tarball is byte-identical to the one inside the matching npm sub-package at the same version.
6. The test workflow passes on a PR that only touches Go code (no npm-related changes required for Go-only work).

---

## Implementation Notes

- **Pattern B is not optional.** Pattern A was rejected because sandboxed CI environments increasingly run `npm ci --ignore-scripts` by default (Snyk, Dependabot, GitHub Actions hardening presets). A postinstall that silently no-ops in those environments would produce a broken `apl` on PATH — worse than a loud failure.
- **Shim: copy esbuild's shape.** Don't reinvent. The `esbuild` npm package's `bin/esbuild` launcher is ~50 lines, battle-tested, handles `--ignore-scripts` failure modes with clear error messages. Reference it and copy its structure.
- **goreleaser ≥ 2.x** required for the `version: 2` config schema shown above.
- **Local Go workflow untouched.** `task build` still builds to `bin/apl`. `go build ./cmd/apl` still works. The npm layer is purely release-time packaging — contributors who never touch a release never see it.
- **Don't commit binaries.** `npm/platforms/*/bin/` is `.gitignore`d. Binaries only appear there transiently during a goreleaser run, between the post-build hook and the `npm publish` step in CI.
- **`0.0.0` placeholder version** in checked-in `package.json` files prevents accidental publishing of a real version from a developer's machine (`npm publish` refuses a version that already exists, and `0.0.0` is reserved-feeling).
- **Public scope.** v1 stays on the public npm registry. No private registry, no GitHub Packages. Keeps the install command identical for every user.
- **Provenance.** The workflow uses `--provenance` on npm publish + `id-token: write` permission, so published packages get npm's build-provenance attestation. Free, adds trust, no downside.

---

## Verification

### Pre-tag (local)

```bash
# From repo root, with goreleaser installed
goreleaser release --snapshot --clean

# Expect:
ls dist/apl_*                       # 5 tarballs + checksums
ls npm/platforms/darwin-arm64/bin/  # apl
ls npm/platforms/windows-x64/bin/   # apl.exe
```

Then dry-run the publish:

```bash
cd npm && npm pack --dry-run        # inspect file list
for p in npm/platforms/*/; do (cd "$p" && npm pack --dry-run); done
```

### Post-tag (CI + registry)

```bash
# After tag vX.Y.Z is pushed and the workflow is green:
npm view @muthuishere/apl version                    # → X.Y.Z
npm view @muthuishere/apl-darwin-arm64 version       # → X.Y.Z
# ... same for the other 4

# Fresh docker run to prove clean install:
docker run --rm -it node:20 bash -lc '
  npm install -g @muthuishere/apl@X.Y.Z &&
  apl version &&
  apl --help
'

# Same, with scripts disabled:
docker run --rm -it node:20 bash -lc '
  npm install -g --ignore-scripts @muthuishere/apl@X.Y.Z &&
  apl version
'
```

### Binary equivalence check

```bash
# Linux x64 example:
curl -sL https://github.com/muthuishere/all-purpose-login/releases/download/vX.Y.Z/apl_Linux_x86_64.tar.gz \
  | tar xz -O apl > /tmp/apl-from-release

npm pack @muthuishere/apl-linux-x64@X.Y.Z
tar xzf muthuishere-apl-linux-x64-X.Y.Z.tgz -C /tmp/
diff -q /tmp/apl-from-release /tmp/package/bin/apl   # expect: no output
```
