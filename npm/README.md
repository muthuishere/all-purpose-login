# @muthuishere/apl

`apl` is an OAuth token broker and HTTP relay for Google and Microsoft productivity APIs (Graph, Gmail, Drive, Calendar). It handles the PKCE + loopback dance once, then stores tokens securely so CLIs, scripts, and agents can call upstream APIs without re-implementing auth.

## Install

```bash
npm install -g @muthuishere/apl
```

This pulls down the correct prebuilt binary for your platform (macOS arm64/x64, Linux arm64/x64, Windows x64) via npm's optional-dependencies mechanism — no postinstall scripts required.

## Quick start

```bash
apl setup ms          # one-time interactive bootstrap for Microsoft Graph
apl login ms:work     # open browser, complete OAuth, store tokens
apl call ms:work /me  # proxied HTTP call with bearer token attached
```

## Links

- Source: https://github.com/muthuishere/all-purpose-login
- Releases: https://github.com/muthuishere/all-purpose-login/releases
- Specs: https://github.com/muthuishere/all-purpose-login/tree/main/docs/specs

## License

MIT — see [LICENSE](https://github.com/muthuishere/all-purpose-login/blob/main/LICENSE).
