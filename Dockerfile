# syntax=docker/dockerfile:1

# ---------------------------------------------------------------------------
# kpil — interactive shell image
#
# Bakes in:
#   - gh CLI (GitHub CLI)
#   - kubectl
#
# The gh copilot extension is installed at container startup via entrypoint.sh
# using the GH_TOKEN environment variable — no token is needed at build time.
#
# Usage:
#   docker build -t kpil:latest .
#   docker run -it \
#     -v /path/to/ro-kubeconfig:/root/.kube/config:ro \
#     -e GH_TOKEN=$GH_TOKEN \
#     kpil:latest
# ---------------------------------------------------------------------------

FROM node:20-slim

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
# Entrypoint — installs gh copilot extension at startup using runtime GH_TOKEN
# ---------------------------------------------------------------------------
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
