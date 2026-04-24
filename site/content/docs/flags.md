---
title: Flags
weight: 2
---

```
kpil [flags]
```

| Flag | Default | Description |
|---|---|---|
| `--image` | `ghcr.io/qjoly/kpil:latest` | Container image to run |
| `--kubeconfig` | `$KUBECONFIG` or `~/.kube/config` | Admin kubeconfig path |
| `--namespace` | `default` | Namespace for the ServiceAccount |
| `--sa-name` | `copilot-readonly` | Name of the SA / ClusterRole / CRB |
| `--out` | `./ro-kubeconfig` | Path for the generated read-only kubeconfig |
| `--token-ttl` | `24h` | ServiceAccount token lifetime |
| `--runtime` | auto | Container runtime: `docker` or `podman` |
| `--platform` | daemon default | OCI platform, e.g. `linux/amd64`, `linux/arm64` |
| `--workdir` | — | Mount a host directory into the container at `/workspace` |
| `--workdir-readonly` | `false` | Mount `--workdir` as read-only |
| `--build` | `false` | Build the image from the local Dockerfile instead of pulling |
| `--pull` | `false` | Always pull the latest image before running |
| `--insecure-image` | `false` | Skip cosign signature verification |
| `--no-cleanup` | `false` | Skip deleting RBAC resources and kubeconfig on exit |
| `-i`, `--interactive` | `false` | Prompt for runtime parameters before launching |
| `--skill` | — | Bake an agent skill into the image at build time (requires `--build`) |

## Examples

```sh
# Use a specific kubeconfig and namespace
kpil --kubeconfig ~/.kube/staging --namespace platform

# Use podman explicitly
kpil --runtime podman

# Force arm64 image on Apple Silicon
kpil --platform linux/arm64

# Mount current directory as workspace (read-write)
kpil --workdir $PWD

# Keep RBAC resources after exit (for debugging)
kpil --no-cleanup

# Build the image locally
GH_TOKEN=$GH_TOKEN kpil --build

# Interactive setup
kpil -i
```
