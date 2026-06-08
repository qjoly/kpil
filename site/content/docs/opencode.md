---
title: OpenCode
weight: 5
---

Run [sst/opencode](https://github.com/sst/opencode) inside the kpil sandbox with `--agent opencode`. OpenCode is provider-agnostic — pair it with Anthropic, OpenAI, or any other supported provider.

```sh
kpil --agent opencode
```

## First-run setup

On the first run, no auth file exists, so kpil prints a hint:

```
No OpenCode session found in /root/.local/share/opencode.
On first run, run  opencode auth login  inside the container to sign in
with your provider (Anthropic, OpenAI, …). The session is then persisted
on the host and reused on subsequent kpil --agent opencode invocations.
```

Inside the agent prompt, run:

```
opencode auth login
```

Pick your provider, follow the prompts, and the credentials are written to `/root/.local/share/opencode/auth.json` — bind-mounted from the host so the login survives container teardown.

## Where the session is stored

By default, kpil persists OpenCode's state under:

```
$HOME/.kpil/opencode/
├── data/      → /root/.local/share/opencode   (auth, history, model state)
└── config/    → /root/.config/opencode        (user configuration)
```

Both subdirs are created automatically with mode `0700`. Override the parent directory with `--opencode-config`:

```sh
kpil --agent opencode --opencode-config /path/to/store
```

## API key alternative

If you already have provider credentials in env vars, OpenCode picks them up directly. kpil forwards both:

```sh
export ANTHROPIC_API_KEY=sk-ant-xxxxxxxxxxxx
# or
export OPENAI_API_KEY=sk-xxxxxxxxxxxx
kpil --agent opencode
```

## Reset

To force a fresh login, remove the persisted state on the host:

```sh
rm -rf ~/.kpil/opencode
```
