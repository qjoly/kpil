---
title: GitHub Token
weight: 3
---

kpil requires a GitHub personal access token (PAT) to authenticate the Copilot CLI inside the container.

## Required permission

| Permission | Scope | Level |
|---|---|---|
| `copilot_requests` | Account | `write` |

No repository or organisation permissions are needed.

## Create a fine-grained PAT

1. Go to **GitHub → Settings → Developer settings → Personal access tokens → Fine-grained tokens**
2. Click **Generate new token**
3. Set an expiration
4. Under **Account permissions**, set `copilot_requests` → **Read and write**
5. Click **Generate token** and copy the value

## Use the token

```sh
export GH_TOKEN=github_pat_xxxxxxxxxxxx
kpil
```

The token is forwarded to the container via the `GH_TOKEN` environment variable and is never written to disk.
