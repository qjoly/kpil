# Image signature verification (cosign)

Every image published by this project is signed using
[Sigstore cosign](https://docs.sigstore.dev/cosign/overview/) **keyless
signing** — no private key is stored anywhere.  Signatures are produced
automatically inside GitHub Actions using the runner's OIDC identity and are
recorded in the public [Rekor](https://docs.sigstore.dev/logging/overview/)
transparency log.

By default the CLI verifies the image's signature **before** the container is
started.  An unsigned or tampered image causes a hard failure.

---

## How images are signed

Each CI and release workflow installs cosign via
`sigstore/cosign-installer@v3` and calls `cosign sign --yes` immediately after
`docker/build-push-action` pushes the manifest.  The `--yes` flag bypasses the
interactive confirmation; the OIDC token provided by the GitHub Actions runner
authenticates the signing request to Fulcio (Sigstore's certificate authority).

```
cosign sign --yes ghcr.io/qjoly/kpil@<digest>
```

The signature is stored as an OCI artefact in the same registry namespace as
the image.  No private key material is used or stored.

### Which workflow signs which tags

| Workflow | Trigger | Tags signed |
|---|---|---|
| `ci.yml` | Push to `main` | `sha-<7chars>`, `edge` |
| `release.yml` | Push `v*` tag | `v1.2.3`, `1.2`, `latest` |

### Certificate identity

The signing certificate issued by Fulcio embeds the workflow's identity:

| Field | Value |
|---|---|
| Subject (regexp) | `https://github.com/qjoly/kpil/` |
| OIDC issuer | `https://token.actions.githubusercontent.com` |

This means only a GitHub Actions workflow running **inside this repository**
can produce a valid signature.  A forked repository or a local build cannot
replicate it.

---

## Automatic verification at runtime

When you run the CLI without `--insecure-image`, it calls `cosign verify`
before `docker run` / `podman run`:

```
Verifying image signature (cosign)…
  Verifying cosign signature for ghcr.io/qjoly/kpil:latest…
  Signature verified.
Starting GitHub Copilot CLI (image: ghcr.io/qjoly/kpil:latest)…
```

The check enforces:

1. The image was signed by a GitHub Actions workflow in this repository.
2. The signing was witnessed by the Rekor transparency log (Sigstore public
   good instance).
3. The certificate chain roots back to Fulcio's public CA.

If verification fails the CLI prints the cosign error output and exits before
the container is started:

```
image verification failed:
  image signature verification failed for ghcr.io/qjoly/kpil:latest: ...
  cosign output: ...
  Use --insecure-image to skip verification (not recommended)
```

### When verification is automatically skipped

| Situation | Behaviour |
|---|---|
| `--build` flag passed | Local builds are never signed — verification is silently skipped |
| `--insecure-image` flag passed | Verification is skipped with a stderr warning |

---

## `--insecure-image` flag

```sh
kpil --insecure-image
```

Skips cosign verification entirely.  Use this when:

- You are running an image built locally with `--build`.
- You are testing a custom image that is not signed.
- cosign is not installed and you accept the risk.

> **Security note:** bypassing signature verification removes the guarantee
> that the container image was produced by this project's CI pipeline.  Only
> use `--insecure-image` in development or testing contexts.

---

## Install cosign

cosign must be present in your `PATH` for automatic verification to work.

| Method | Command |
|---|---|
| Homebrew (macOS / Linux) | `brew install cosign` |
| apt (Debian / Ubuntu) | See [cosign install docs](https://docs.sigstore.dev/cosign/system_config/installation/) |
| Binary download | [github.com/sigstore/cosign/releases](https://github.com/sigstore/cosign/releases) |

Quick install via the official install script:

```sh
curl -O -L https://github.com/sigstore/cosign/releases/latest/download/cosign-linux-amd64
sudo install -m 755 cosign-linux-amd64 /usr/local/bin/cosign
```

Verify the installation:

```sh
cosign version
```

If cosign is not in `PATH` and `--insecure-image` is not set, the CLI exits
with:

```
image verification failed:
  cosign is not installed — cannot verify image signature
  Install cosign: https://docs.sigstore.dev/cosign/system_config/installation/
  Or skip verification with: --insecure-image (not recommended)
```

---

## Manual verification

You can verify any published image independently of the CLI:

```sh
cosign verify \
  --certificate-identity-regexp="https://github.com/qjoly/kpil/" \
  --certificate-oidc-issuer="https://token.actions.githubusercontent.com" \
  ghcr.io/qjoly/kpil:latest
```

Successful output (JSON, one entry per signature):

```json
[
  {
    "critical": {
      "identity": { "docker-reference": "ghcr.io/qjoly/kpil" },
      "image": { "docker-manifest-digest": "sha256:..." },
      "type": "cosign container image signature"
    },
    "optional": {
      "Bundle": { ... },
      "Issuer": "https://token.actions.githubusercontent.com",
      "Subject": "https://github.com/qjoly/kpil/.github/workflows/release.yml@refs/tags/v..."
    }
  }
]
```

The `Subject` field shows exactly which workflow file and git ref produced the
signature.

To verify a specific digest (recommended for reproducibility):

```sh
cosign verify \
  --certificate-identity-regexp="https://github.com/qjoly/kpil/" \
  --certificate-oidc-issuer="https://token.actions.githubusercontent.com" \
  ghcr.io/qjoly/kpil@sha256:<digest>
```

---

## Troubleshooting

### `cosign: command not found`

cosign is not in your `PATH`.  Either install it (see above) or pass
`--insecure-image` to skip verification.

### `no matching signatures`

The image has no cosign signature in the registry.  Possible causes:

- You are using an image tag that predates the introduction of signing (before
  the `feat: add cosign keyless signing` commit).
- The image was built locally with `docker build` and never pushed through CI.
- The signing step in CI failed (check the workflow run in GitHub Actions).

Pass `--insecure-image` to proceed without verification, or pull a newer
signed tag:

```sh
kpil --image ghcr.io/qjoly/kpil:latest
```

### `error connecting to Rekor`

cosign contacts the Rekor transparency log during verification.  If your
machine cannot reach `https://rekor.sigstore.dev` (network restriction,
firewall, offline environment), verification will time out.

Options:

- Allow outbound HTTPS to `rekor.sigstore.dev`.
- Use `--insecure-image` if outbound access is not possible.
