#!/bin/bash
set -e

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
