# syntax=docker/dockerfile:1

# ---------------------------------------------------------------------------
# kpil — interactive shell image
#
# Bakes in:
#   - gh CLI (GitHub CLI)
#   - kubectl
#   - gh copilot extension (when gh_token build secret is provided)
#
# The gh copilot extension is installed at build time via a BuildKit secret
# mount so the token is never written into any image layer. If the secret is
# absent (e.g. a plain `docker build` without --secret) the step is skipped
# and entrypoint.sh installs the extension at first run instead.
#
# Usage:
#   docker build -t kpil:latest .
#   docker run -it \
#     -v /path/to/ro-kubeconfig:/root/.kube/config:ro \
#     -e GH_TOKEN=$GH_TOKEN \
#     kpil:latest
# ---------------------------------------------------------------------------

FROM node:25-slim

# ---------------------------------------------------------------------------
# System dependencies
# ---------------------------------------------------------------------------
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        bash \
        && rm -rf /var/lib/apt/lists/*

# ---------------------------------------------------------------------------
# gh CLI — install from the official GitHub APT repository
# ---------------------------------------------------------------------------
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
        -o /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] \
        https://cli.github.com/packages stable main" \
        > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends gh \
    && rm -rf /var/lib/apt/lists/*

# ---------------------------------------------------------------------------
# kubectl — pinned to the latest stable release at build time
# ---------------------------------------------------------------------------
RUN KUBECTL_VERSION=$(curl -fsSL https://dl.k8s.io/release/stable.txt) \
    && curl -fsSL "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/$(dpkg --print-architecture)/kubectl" \
        -o /usr/local/bin/kubectl \
    && chmod +x /usr/local/bin/kubectl \
    && kubectl version --client --output=yaml

# ---------------------------------------------------------------------------
# Runtime environment
# ---------------------------------------------------------------------------
ENV KUBECONFIG=/root/.kube/config

# Ensure the .kube directory exists so the volume mount works even if the
# host path resolves to a file (Docker creates a directory if the mount target
# is missing).
RUN mkdir -p /root/.kube

# Print a brief banner when the shell is invoked (e.g. via docker exec).
# The entrypoint goes straight to gh copilot suggest, so .bashrc is only
# sourced if a user explicitly opens a bash session inside the container.
RUN echo 'echo ""' >> /root/.bashrc \
    && echo 'echo "  kubectl : $(kubectl version --client --short 2>/dev/null)"' >> /root/.bashrc \
    && echo 'echo "  gh      : $(gh --version | head -1)"' >> /root/.bashrc \
    && echo 'echo ""' >> /root/.bashrc

# ---------------------------------------------------------------------------
# Skills — bake agent skills at build time
#
# Two sources are merged (duplicates are silently deduplicated):
#
#   1. skills.txt  — default skills committed to the repository.
#                    Each non-blank, non-comment line is an
#                    "owner/repo/skill-name" spec.
#
#   2. SKILLS build-arg — space-separated list of additional specs supplied
#                         at build time:
#                           docker build --build-arg SKILLS="owner/repo/skill-name" .
#                           kpil --build --skill owner/repo/skill-name
#
# Every SKILL.md is fetched from:
#   https://raw.githubusercontent.com/<owner>/<repo>/main/.claude/skills/<skill-name>/SKILL.md
# and installed to:
#   /root/.copilot/skills/<skill-name>/SKILL.md
#
# GitHub Copilot discovers personal skills from ~/.copilot/skills/
# (and also ~/.claude/skills/ and ~/.agents/skills/).
# ---------------------------------------------------------------------------
ARG SKILLS=""
RUN --mount=type=bind,source=.,target=/build-ctx \
    set -e; \
    # Read skills.txt from the build context if present (optional file). \
    FILE_SKILLS="$(grep -v '^\s*#' /build-ctx/skills.txt 2>/dev/null | grep -v '^\s*$' || true)"; \
    # Append any build-arg extras (convert spaces to newlines). \
    EXTRA_SKILLS="$(echo "$SKILLS" | tr ' ' '\n' | grep -v '^\s*$' || true)"; \
    # Merge, deduplicate, and drop blank lines. \
    ALL_SKILLS="$(printf '%s\n%s\n' "$FILE_SKILLS" "$EXTRA_SKILLS" \
                  | sort -u | grep -v '^\s*$' || true)"; \
    if [ -z "$ALL_SKILLS" ]; then \
      echo "  No skills configured — skipping skill installation."; \
    else \
      echo "$ALL_SKILLS" | while IFS= read -r SKILL_SPEC; do \
        OWNER=$(echo "$SKILL_SPEC"      | cut -d'/' -f1); \
        REPO=$(echo  "$SKILL_SPEC"      | cut -d'/' -f2); \
        SKILL_NAME=$(echo "$SKILL_SPEC" | cut -d'/' -f3); \
        SKILL_URL="https://raw.githubusercontent.com/${OWNER}/${REPO}/main/.claude/skills/${SKILL_NAME}/SKILL.md"; \
        echo "  Installing skill: ${SKILL_NAME}"; \
        echo "    from: ${SKILL_URL}"; \
        mkdir -p "/root/.copilot/skills/${SKILL_NAME}"; \
        curl -fsSL "${SKILL_URL}" -o "/root/.copilot/skills/${SKILL_NAME}/SKILL.md"; \
        echo "    installed to: /root/.copilot/skills/${SKILL_NAME}/SKILL.md"; \
      done; \
    fi

# ---------------------------------------------------------------------------
# gh copilot extension — installed at build time via a BuildKit secret mount.
# The token is read from /run/secrets/gh_token and is never written into any
# image layer or visible in `docker history`.
#
# If the secret is not supplied (plain `docker build` without --secret) the
# step prints a notice and exits cleanly; entrypoint.sh will install the
# extension at first run using the runtime GH_TOKEN instead.
#
# CI / release workflows pass GITHUB_TOKEN automatically (see .github/).
# Local builds: `docker build --secret id=gh_token,env=GH_TOKEN …`
#                or use `kpil --build` which forwards GH_TOKEN for you.
# ---------------------------------------------------------------------------
RUN --mount=type=secret,id=gh_token \
    if gh copilot --version > /dev/null 2>&1; then \
      echo "gh copilot is already available as a built-in — skipping extension install."; \
    elif GH_TOKEN=$(cat /run/secrets/gh_token 2>/dev/null) && [ -n "$GH_TOKEN" ]; then \
      echo "Installing gh copilot extension (build time)…"; \
      gh extension install github/gh-copilot; \
    else \
      echo "gh_token secret not provided — extension will be installed at first run."; \
    fi

# ---------------------------------------------------------------------------
# Entrypoint — installs the gh copilot extension on first run (using the
# runtime GH_TOKEN) then launches gh copilot suggest.
# ---------------------------------------------------------------------------
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
