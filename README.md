# apl — all-purpose login

`apl` is an OAuth token broker and HTTP relay for Google and Microsoft productivity APIs (Graph, Gmail, Drive, Calendar). It handles the PKCE + loopback dance once, stores tokens securely in the OS keyring, then lets any CLI, script, or agent call upstream APIs with a single `apl call <account> <path>`.

## Install

### Via npm (recommended)

```bash
npm install -g @muthuishere/apl
```

This works on macOS (arm64 / x64), Linux (arm64 / x64), and Windows (x64). It uses npm's per-platform optional dependencies to install the right prebuilt binary — no postinstall scripts, so it works fine with `--ignore-scripts` and locked-down CI.

### Via GitHub Release tarball

Prebuilt binaries for each release are attached to the [GitHub Releases page](https://github.com/muthuishere/all-purpose-login/releases). Download the tarball for your platform and extract `apl` onto your `PATH`:

```bash
# Example: Linux x64
curl -sL https://github.com/muthuishere/all-purpose-login/releases/latest/download/apl_<version>_Linux_x86_64.tar.gz \
  | tar -xz -C /usr/local/bin apl
```

## Quick start

```bash
apl setup ms          # one-time interactive bootstrap for Microsoft Graph
apl login ms:work     # open browser, complete OAuth, tokens land in the keyring
apl call ms:work /me  # HTTP call with the bearer token attached
```

Swap `ms` for `google` for Google APIs. Multiple accounts per provider are supported via the `provider:label` form.

## Development

Contributors build locally with Go and [Task](https://taskfile.dev):

```bash
task build          # builds ./bin/apl
task test           # go test ./...
task run -- version # apl version
```

The npm layer is purely release-time packaging — contributors who never cut a release never see it.

## Specs

Detailed specs live in [`docs/specs/`](docs/specs). Start points:

- [`spec-cli-command-surface.md`](docs/specs/spec-cli-command-surface.md) — the user-facing command set.
- [`spec-oauth-pkce-loopback.md`](docs/specs/spec-oauth-pkce-loopback.md) — the OAuth flow.
- [`spec-token-storage.md`](docs/specs/spec-token-storage.md) — keyring layout.
- [`spec-npm-distribution.md`](docs/specs/spec-npm-distribution.md) — release + npm packaging.

## Release process

Cutting a release is a single git tag push:

```bash
git tag v0.1.0 && git push origin v0.1.0
```

The `release` workflow then:

1. Runs `goreleaser release --clean` — builds 5 targets and attaches tarballs + `checksums.txt` to the GitHub Release.
2. Stages each binary into `npm/platforms/<os>-<arch>/bin/`.
3. Rewrites the version in all 6 `package.json` files to match the tag.
4. Publishes the 5 platform sub-packages then the main `@muthuishere/apl` package.

Required secrets: `NPM_TOKEN` (npm automation token with publish rights on the `@muthuishere` scope). `GITHUB_TOKEN` is provided automatically by Actions.

Local smoke test of the release plumbing (no publish):

```bash
task release:snapshot
```

## License

MIT — see [LICENSE](LICENSE).
