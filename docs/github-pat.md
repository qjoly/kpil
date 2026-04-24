# GitHub Personal Access Token — Copilot-only scope

## Short answer

**Yes.** GitHub's fine-grained PATs include a dedicated `copilot_requests` account
permission that limits the token exclusively to making Copilot API calls.
A classic PAT has no equivalent scope and would either grant no Copilot access
(no scopes) or far too much access (any broad scope).  Use a fine-grained PAT.

---

## Required permission

| Layer | Permission name | Display name | Level |
|---|---|---|---|
| Account | `copilot_requests` | Copilot requests | `write` |

> **Note on billing:** every request made with this token counts against the
> token owner's [GitHub Copilot premium request
> allowance](https://docs.github.com/en/copilot/concepts/billing/copilot-requests).
> Overage is billed if the allowance is exceeded.

No repository permissions are needed. No organisation permissions are needed.
The resource owner must be your personal account (not an organisation).

### Optional additional Copilot permissions

If you also want `gh copilot` to be able to read Copilot Chat history or
editor context, add:

| Permission | Level | Purpose |
|---|---|---|
| `copilot_messages` | `read` | Read Copilot Chat message history |
| `copilot_editor_context` | `read` | Read Copilot Editor Context |

These are not required for `gh copilot suggest` or `gh copilot explain`.

---

## Step-by-step: create the token

### Option A — pre-filled URL (fastest)

Open this URL in your browser. It pre-fills the token name, description, and
the `copilot_requests: write` permission for you:

```
https://github.com/settings/personal-access-tokens/new?name=kpil&description=Copilot+CLI+token+for+kpil&user_copilot_requests=read
```

Then:
1. Set an **Expiration** (recommended: 7 or 30 days — no infinite tokens).
2. Confirm **Resource owner** is your personal account.
3. Click **Generate token** and copy the value immediately.

### Option B — manual steps

1. Go to **GitHub → Settings → Developer settings → Personal access tokens →
   Fine-grained tokens**.
2. Click **Generate new token**.
3. Fill in:
   - **Token name**: `kpil` (or any name)
   - **Expiration**: choose a short lifetime (7–30 days recommended)
   - **Resource owner**: your personal account
   - **Repository access**: **No repositories** (Copilot requests need none)
4. Under **Account permissions**, find **Copilot requests** and set it to
   **Read and write** (`write` is the only valid level; the UI may label it
   differently).
5. Click **Generate token** and copy the value immediately.

---

## What this token cannot do

A token with only `copilot_requests: write` **cannot**:

- Read or write any repository content
- Access organisation resources
- Read issues, pull requests, packages, secrets, or any other resource
- Authenticate `git` operations
- Call any GitHub REST API endpoint other than the Copilot API

This makes it safe to pass into a container environment — if it leaks, the
blast radius is limited to Copilot API calls on your behalf.

---

## Using the token with this project

Export the token before running the CLI:

```sh
export GH_TOKEN=github_pat_xxxx
kpil --kubeconfig ~/.kube/config
```

The CLI forwards `GH_TOKEN` into the container at runtime:

```sh
docker run -it \
  -v /path/to/ro-kubeconfig:/root/.kube/config:ro \
  -e GH_TOKEN="$GH_TOKEN" \
  ghcr.io/qjoly/kpil:latest
```

Inside the container, the entrypoint script installs the `gh copilot` extension
automatically using `GH_TOKEN`, then drops you into an interactive shell.
No token is needed to build the image.

---

## Token lifetime recommendations

| Use case | Recommended expiry |
|---|---|
| Personal / local use | 30 days |
| Shared team environment | 7 days |
| CI / automation | Use a GitHub App instead (PATs are user-tied) |

GitHub does not allow fine-grained PATs to be created with no expiry in most
organisation policy contexts. Even for personal accounts, setting a short
expiry is strongly recommended.

---

## Classic PAT — why it should not be used

Classic PATs (`ghp_*`) do not have a `copilot` scope. The two realistic options
are both worse than a fine-grained PAT:

| Classic PAT option | Problem |
|---|---|
| No scopes | Only accesses public data; Copilot API calls will be rejected (401) |
| `read:user` + `repo` | Grants read access to all your private repositories — far too broad |

There is no way to grant Copilot-only access with a classic PAT.
