#!/usr/bin/env bash
# Drive the MCP stdio server with a scripted JSON-RPC session.
#
# Expected: tools/list reports 58 tools; engram_search returns the fixture;
# accounting_approve fails closed with AUTHENTICATION_REQUIRED because stdio
# has no authenticated session. Agents cannot turn claimed identity into
# approval authority.
source "$(dirname "$0")/../lib.sh"

step "seed the store through the CLI"
engram save "$FIXTURE" >/dev/null

step "replay examples/mcp/session.jsonl against the stdio server"
engram mcp <"$EXAMPLES_DIR/mcp/session.jsonl"
