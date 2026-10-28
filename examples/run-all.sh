#!/usr/bin/env bash
# Run every self-contained example in sequence and stop on the first failure.
# Docker is excluded: it builds an image and is documented in docker/README.md.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
for script in cli/quickstart.sh verification/verify-memory.sh mcp/quickstart.sh http/quickstart.sh; do
  printf '\n######## %s\n' "$script"
  bash "$here/$script"
done
printf '\nAll examples passed.\n'
