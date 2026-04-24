#!/bin/bash
set -e

if [ -z "$GH_TOKEN" ]; then
  echo "Error: GH_TOKEN is not set." >&2
  echo "       Re-run with: docker run -e GH_TOKEN=<token> ..." >&2
  exit 1
fi

# Install the gh copilot extension if it is not already available.
# Recent versions of gh ship copilot as a built-in, so check with
# `gh copilot --version` rather than scanning the extension list.
# GH_TOKEN is already exported so gh uses it for authentication.
if ! gh copilot --version > /dev/null 2>&1; then
  echo "Installing gh copilot extension…" >&2
  gh extension install github/gh-copilot
fi

exec gh copilot suggest
