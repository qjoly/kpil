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
    if ! command -v claude >/dev/null 2>&1; then
      echo "Error: claude binary not found in the image." >&2
      echo "       Rebuild the image with --build to install Claude Code." >&2
      exit 1
    fi

    # Authentication:
    #   - If /root/.claude already contains a credentials file (mounted from
    #     the host's --claude-config directory), claude reuses the existing
    #     Pro/Max subscription session.
    #   - Otherwise claude prompts the user to run `/login` and walks them
    #     through the OAuth flow; the resulting credentials are written under
    #     /root/.claude and persist on the host via the bind mount.
    #   - ANTHROPIC_API_KEY is honoured by the claude binary natively if set.
    if [ ! -f /root/.claude/.credentials.json ] && [ -z "$ANTHROPIC_API_KEY" ]; then
      echo "" >&2
      echo "No Claude Code session found in /root/.claude." >&2
      echo "On first run, type  /login  inside Claude Code to sign in with your" >&2
      echo "Claude Pro/Max subscription. The session is then persisted on the host" >&2
      echo "and reused on subsequent kpil --agent claude invocations." >&2
      echo "" >&2
    fi

    # --dangerously-skip-permissions keeps parity with the Copilot flow:
    # the container is already an isolated, read-only-RBAC sandbox.
    exec claude --dangerously-skip-permissions
    ;;

  *)
    echo "Error: unknown AGENT=\"$AGENT\" (expected \"copilot\" or \"claude\")." >&2
    exit 1
    ;;
esac
