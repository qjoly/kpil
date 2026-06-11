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
| `nightly` | Daily rebuild at 02:30 UTC — picks up upstream base, apt, npm |
| `nightly-YYYY-MM-DD` | Daily rebuild — date-stamped, kept forever |

### Nightly rebuilds

The Dockerfile pulls a few moving pieces at build time that aren't pinned in
git: the `node:26-slim` base, apt packages (including `gh` and security
updates), `kubectl` latest stable, and the npm `latest` releases of
`@anthropic-ai/claude-code` and `opencode-ai`. The `nightly` workflow rebuilds
the image from scratch every night (`--no-cache --pull`) so those stay current
without waiting for a code change. Both `nightly` and `nightly-YYYY-MM-DD` are
signed with cosign and carry a SLSA build provenance attestation, identical to
release tags.

Pull the freshest image:

```sh
kpil --image ghcr.io/qjoly/kpil:nightly
```

Or pin to a specific night:

```sh
kpil --image ghcr.io/qjoly/kpil:nightly-2026-06-09
```

## Platforms

The image is built for `linux/amd64` and `linux/arm64`.

## Contents

- `kubectl` (latest stable at build time)
- `gh` CLI + `gh copilot` extension (latest stable at build time)
- `copilot` binary (from [github/copilot-cli](https://github.com/github/copilot-cli))
- `claude` binary — [Anthropic Claude Code](https://www.anthropic.com/claude-code), installed via `@anthropic-ai/claude-code`
- `opencode` binary — [sst/opencode](https://github.com/sst/opencode), installed via `opencode-ai`

The entrypoint dispatches to the right agent at runtime based on the `AGENT` environment variable set by kpil (mirroring `--agent`).

## Startup update check

When you run `kpil` against an image already present locally (no `--pull`, no
`--build`), kpil queries the registry for the current manifest digest and
compares it against the local image. If they differ, kpil also reads the
image's baked-in agent versions from the registry without pulling any layers
— the SBOM attached by buildx for `claude` / `opencode`, and the OCI label
for `copilot` — and prints both sides side-by-side before prompting:

```
Image ghcr.io/qjoly/kpil:nightly found locally.
A newer version of ghcr.io/qjoly/kpil:nightly is available in the registry.
  local digest:  sha256:b79290…
  remote digest: sha256:443df4…
  baked-in agent versions:
  → claude    2.0.4   →  2.1.173
      opencode  1.16.0  →  1.17.3
      copilot   1.0.35  →  1.0.35
Pull the latest version now? [Y/n]:
```

Answer `Y` (or just Enter) to pull the new image in place; answer `n` to keep
the local version. `Ctrl+C` cancels both the prompt and the run.

A locally-built image (`--build`) carries no registry-bound digest, so the
check skips silently. Same goes for images whose registry returns no digest
header.

### Disabling the check

Set `KPIL_SKIP_UPDATE_CHECK=1` in the environment to skip the check
entirely — useful in CI, offline environments, or any flow where the
prompt would block:

```sh
export KPIL_SKIP_UPDATE_CHECK=1
kpil
```

Equivalent on a single invocation:

```sh
KPIL_SKIP_UPDATE_CHECK=1 kpil
```

The check is unaffected when `--pull` or `--build` is set: those flags
already force a specific image state.

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
