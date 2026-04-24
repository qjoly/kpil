# syntax=docker/dockerfile:1

# ---------------------------------------------------------------------------
# kpil — interactive shell image
#
# Bakes in:
#   - gh CLI (GitHub CLI)
#   - kubectl
#   - gh copilot extension (downloaded directly from the public release)
#
# The gh copilot extension binary is fetched from the public
# github/gh-copilot releases page using plain curl — no token required at
# build time.
#
# Usage:
#   docker build -t kpil:latest .
#   docker run -it \
#     -v /path/to/ro-kubeconfig:/root/.kube/config:ro \
#     -e GH_TOKEN=$GH_TOKEN \
#     kpil:latest
# ---------------------------------------------------------------------------

# ---------------------------------------------------------------------------
# Stage 1: download the GitHub Copilot CLI binary for the TARGET architecture.
#
# Running this stage FROM --platform=$BUILDPLATFORM (the CI host, always
# amd64) while declaring ARG TARGETARCH causes BuildKit to correctly inject
# the target architecture (e.g. arm64) rather than the host's architecture.
# Without this stage, BuildKit's native cross-compilation mode sets TARGETARCH
# to the *execution* platform (amd64), so the arm64 image would get an x64
# binary.
# ---------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM alpine:3.21 AS copilot-downloader
ARG TARGETARCH=amd64
RUN apk add --no-cache curl \
    && case "${TARGETARCH}" in \
         amd64) CLI_ARCH="x64" ;; \
         arm64) CLI_ARCH="arm64" ;; \
         *)     CLI_ARCH="x64" ;; \
       esac \
    && echo "  Downloading GitHub Copilot CLI (linux-${CLI_ARCH})…" \
    && curl -fsSL \
        "https://github.com/github/copilot-cli/releases/download/v1.0.35/copilot-linux-${CLI_ARCH}.tar.gz" \
        | tar -xz -C /usr/local/bin copilot \
    && chmod +x /usr/local/bin/copilot \
    && echo "  Installed to /usr/local/bin/copilot"

# ---------------------------------------------------------------------------
# Stage 2: main image
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
# GitHub Copilot CLI — copied from the downloader stage above.
# The binary was fetched for the correct TARGETARCH in stage 1.
# ---------------------------------------------------------------------------
COPY --from=copilot-downloader /usr/local/bin/copilot /usr/local/bin/copilot

# ---------------------------------------------------------------------------
# Entrypoint — safety-net download of the Copilot CLI if absent, then
# launches gh copilot (interactive chat agent).
# ---------------------------------------------------------------------------
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
