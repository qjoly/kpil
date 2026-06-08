---
title: Container Image
weight: 6
---

Images are published to [ghcr.io/qjoly/kpil](https://github.com/qjoly/kpil/pkgs/container/kpil).

## Tags

| Tag | Updated on |
|---|---|
| `latest` | Release tag (`v*`) |
| `v1.2.3` | Release tag — immutable |
| `edge` | Every commit to `main` |
| `sha-<7chars>` | Every commit to `main` — immutable |

## Platforms

The image is built for `linux/amd64` and `linux/arm64`.

## Contents

- `kubectl` (latest stable at build time)
- `gh` CLI + `gh copilot` extension (latest stable at build time)
- `copilot` binary (from [github/copilot-cli](https://github.com/github/copilot-cli))
- `claude` binary — [Anthropic Claude Code](https://www.anthropic.com/claude-code), installed via `@anthropic-ai/claude-code`
- `opencode` binary — [sst/opencode](https://github.com/sst/opencode), installed via `opencode-ai`

The entrypoint dispatches to the right agent at runtime based on the `AGENT` environment variable set by kpil (mirroring `--agent`).

## Signature verification

Every image is signed with [cosign keyless signing](https://docs.sigstore.dev/cosign/signing/overview/) via GitHub Actions OIDC. The CLI verifies the signature automatically before starting the container.

```sh
# Verification is automatic when cosign is in PATH
kpil

# Skip verification for unsigned or locally-built images
kpil --insecure-image
```

Verify manually:

```sh
cosign verify \
  --certificate-identity-regexp "https://github.com/qjoly/kpil/" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  ghcr.io/qjoly/kpil:latest
```

## Build locally

```sh
docker build -t kpil:local .
GH_TOKEN=$GH_TOKEN kpil --build --insecure-image
```
