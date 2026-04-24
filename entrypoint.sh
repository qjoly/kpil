#!/bin/bash
set -e

if [ -z "$GH_TOKEN" ]; then
  echo "Error: GH_TOKEN is not set." >&2
  echo "       Re-run with: docker run -e GH_TOKEN=<token> ..." >&2
  exit 1
fi

# Install the gh copilot extension if it is not already present.
#
# We check with `gh extension list` (non-interactive) instead of
# `gh copilot --version` because the latter prompts "Would you like to
# install it?" on a TTY when the extension is missing — which hangs the
# startup and confuses the user.
#
# If the extension is already available as a gh built-in (gh >= 2.39) the
# list will still show it and the install step is skipped.
if ! gh extension list 2>/dev/null | grep -qi "copilot"; then
  echo "Installing gh copilot extension…" >&2
  gh extension install github/gh-copilot
fi

exec gh copilot suggest
