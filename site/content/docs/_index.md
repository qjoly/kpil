---
title: Documentation
weight: 1
---

Welcome to the **kpil** documentation.

kpil provisions a scoped, read-only Kubernetes ServiceAccount (secrets excluded), generates a short-lived kubeconfig, and drops you into your AI coding agent of choice — **GitHub Copilot CLI**, **Anthropic Claude Code**, or **OpenCode** — inside an isolated container, then cleans everything up on exit.

Pick an agent with `--agent` and follow the per-agent setup page:

- [GitHub Token](github-token) — for `--agent copilot` (default)
- [Claude Code](claude) — for `--agent claude`
- [OpenCode](opencode) — for `--agent opencode`
