#!/bin/bash
set -e

if [ -z "$GH_TOKEN" ]; then
  echo "Error: GH_TOKEN is not set." >&2
  echo "       Re-run with: docker run -e GH_TOKEN=<token> ..." >&2
  exit 1
fi

exec gh copilot suggest
