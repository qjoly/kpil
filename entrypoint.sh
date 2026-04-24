#!/bin/bash
set -e

if [ -z "$GH_TOKEN" ]; then
  echo "Error: GH_TOKEN is not set." >&2
  echo "       Re-run with: docker run -e GH_TOKEN=<token> ..." >&2
  exit 1
fi

# Check whether gh copilot is available (built-in OR extension) without
# triggering an interactive prompt.  Redirecting stdin from /dev/null
# prevents gh from asking "Would you like to install it?" on a TTY.
if ! gh copilot --version </dev/null >/dev/null 2>&1; then
  echo "Installing gh copilot extension…" >&2
  gh extension install github/gh-copilot || {
    echo "Warning: could not install gh copilot extension — proceeding anyway." >&2
  }
fi

exec gh copilot suggest
