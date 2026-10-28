# Shared setup for the example scripts. Source it; do not execute it.
#
# Every example runs against a fresh temporary workspace that is removed on
# exit, so it never touches ./engram.db, your signing keyring, or real data.

set -euo pipefail

EXAMPLES_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FIXTURE="$EXAMPLES_DIR/fixtures/synthetic-memory.json"
RUC="20100039201"
PERIOD="202401"

# Use an installed binary by default; set DRENYRA_ENGRAM_BIN to override,
# for example DRENYRA_ENGRAM_BIN="go run ./cmd/drenyra-engram".
ENGRAM="${DRENYRA_ENGRAM_BIN:-drenyra-engram}"

WORKDIR="$(mktemp -d)"
cleanup() { rm -rf -- "$WORKDIR"; }
trap cleanup EXIT

export DRENYRA_ENGRAM_DB="$WORKDIR/engram.db"
export DRENYRA_ENGRAM_OBJECTS="$WORKDIR/objects"
export DRENYRA_ENGRAM_SIGNING_KEY="$WORKDIR/signing-keys.json"
# The synthetic fixture is a legacy-class (unbound) memory. legacy_compat is
# for local examples only; never carry it into a deployment.
export DRENYRA_FISCAL_RUNTIME_MODE=legacy_compat

engram() { $ENGRAM "$@"; }
step() { printf '\n== %s\n' "$*"; }
