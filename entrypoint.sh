#!/bin/bash
set -e

# Default to GitHub Copilot for backward compatibility.
AGENT="${AGENT:-copilot}"

case "$AGENT" in
  copilot)
    if [ -z "$GH_TOKEN" ]; then
      echo "Error: GH_TOKEN is not set." >&2
      echo "       Re-run with: docker run -e GH_TOKEN=<token> ..." >&2
      exit 1
    fi

    # Ensure the GitHub Copilot CLI binary is present.
    # If the image was built correctly it will already be at /usr/local/bin/copilot.
    # This block is only a safety net for ad-hoc runs against an unbuilt image.
    if [ ! -x /usr/local/bin/copilot ]; then
      echo "GitHub Copilot CLI not found — downloading…" >&2
      # Map uname -m to the arch suffix used by github/copilot-cli releases:
      #   x86_64  → x64
      #   aarch64 → arm64
      case "$(uname -m)" in
        x86_64)  CLI_ARCH="x64" ;;
        aarch64) CLI_ARCH="arm64" ;;
        *)       CLI_ARCH="x64" ;;
      esac
      curl -fsSL \
        "https://github.com/github/copilot-cli/releases/download/v1.0.35/copilot-linux-${CLI_ARCH}.tar.gz" \
        | tar -xz -C /usr/local/bin copilot \
      && chmod +x /usr/local/bin/copilot \
      || {
        echo "Warning: could not download GitHub Copilot CLI — proceeding anyway." >&2
      }
    fi

    exec gh copilot
    ;;

  claude)
    if [ -z "$ANTHROPIC_API_KEY" ]; then
      echo "Error: ANTHROPIC_API_KEY is not set." >&2
      echo "       Re-run with: kpil --agent claude (with ANTHROPIC_API_KEY exported)" >&2
      exit 1
    fi

    if ! command -v claude >/dev/null 2>&1; then
      echo "Error: claude binary not found in the image." >&2
      echo "       Rebuild the image with --build to install Claude Code." >&2
      exit 1
    fi

    # `claude` with no args starts an interactive session.
    # --dangerously-skip-permissions keeps parity with the Copilot flow:
    # the container is already an isolated, read-only-RBAC sandbox.
    exec claude --dangerously-skip-permissions
    ;;

  *)
    echo "Error: unknown AGENT=\"$AGENT\" (expected \"copilot\" or \"claude\")." >&2
    exit 1
    ;;
esac
