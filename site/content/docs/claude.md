---
title: Claude Code
weight: 4
---

Run [Anthropic Claude Code](https://www.anthropic.com/claude-code) inside the kpil sandbox with `--agent claude`. Authentication uses your Claude Pro/Max subscription via the standard OAuth flow.

```sh
kpil --agent claude
```

## First-run setup

On the first run, no credentials file exists yet, so kpil prints a hint:

```
No Claude Code session found in /root/.claude.
On first run, type  /login  inside Claude Code to sign in with your
Claude Pro/Max subscription. The session is then persisted on the host
and reused on subsequent kpil --agent claude invocations.
```

Inside the agent prompt, type `/login`. Claude Code prints a URL — open it in your browser, complete the OAuth dance, and paste the resulting code back into the agent. The credentials are written under `/root/.claude/.credentials.json`, which is bind-mounted from the host so the login survives container teardown.

## Where the session is stored

By default, kpil persists Claude Code's state under:

```
$HOME/.kpil/claude/
```

The directory is created automatically with mode `0700` and mounted at `/root/.claude` inside the container. Override the location with `--claude-config`:

```sh
kpil --agent claude --claude-config /path/to/store
```

The runtime config file (`/root/.claude.json`, which pairs the OAuth token to a user identity) is shuttled in and out of the persisted dir on each run by the entrypoint, so a single `/login` is enough.

## API key alternative

If you have an Anthropic API key, export it before launching and Claude Code will use it instead of the subscription flow:

```sh
export ANTHROPIC_API_KEY=sk-ant-xxxxxxxxxxxx
kpil --agent claude
```

## Running as root inside the container

The container runs as `root`, and Claude Code's `--dangerously-skip-permissions` refuses to start under root by default. The container is itself the sandbox (read-only RBAC, no host filesystem outside the bind mounts, isolated network namespace), so kpil sets `IS_SANDBOX=1` in the entrypoint — the documented escape hatch for this exact use case.

## Reset

To force a fresh login, remove the persisted state on the host:

```sh
rm -rf ~/.kpil/claude
```
