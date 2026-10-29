#!/usr/bin/env bash
# Sign a memory with a throwaway Ed25519 key and verify it offline.
#
# Expected: verify prints "outcome": "passed", per-layer results, and
# "accountingCorrectness": "Accounting correctness: NOT ASSERTED".
# A valid receipt proves integrity and provenance, never correctness.
source "$(dirname "$0")/../lib.sh"

step "create a signing key in the temporary keyring"
engram keys init

step "save the memory; the store records a signed receipt with the act"
id="$(engram save "$FIXTURE" | sed -n 's/.*"id": "\([^"]*\)".*/\1/p' | head -n 1)"
echo "memory id: $id"

step "verify the receipt chain offline (read-only)"
engram verify memory "$id"
