#!/usr/bin/env bash
# Run the HTTP adapter on localhost with a bearer token and call it with curl.
#
# Expected: the unauthenticated call returns 401 UNAUTHORIZED; the save
# returns 201; the search returns the fixture; doctor reports the schema.
# Requires curl.
source "$(dirname "$0")/../lib.sh"

ADDR="127.0.0.1:${PORT:-18787}"
TOKEN="example-token-not-a-secret"
BASE="http://$ADDR"

engram serve --addr "$ADDR" --token "$TOKEN" &
server_pid=$!
# Wait for the server to exit before removing the workspace, so nothing it
# writes while shutting down outlives the cleanup.
trap 'kill "$server_pid" 2>/dev/null || true; wait "$server_pid" 2>/dev/null || true; cleanup' EXIT

for _ in $(seq 50); do
  curl -s -o /dev/null "$BASE/v1/doctor" && break
  sleep 0.1
done

step "without a token: rejected"
curl -s -w ' [%{http_code}]\n' "$BASE/v1/doctor"

step "save the synthetic memory"
curl -s -w ' [%{http_code}]\n' \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  --data @"$FIXTURE" \
  "$BASE/v1/observations"

step "search; HTTP reads take the exact scope, including organizationId"
curl -s -w ' [%{http_code}]\n' \
  -H "Authorization: Bearer $TOKEN" \
  "$BASE/v1/search?q=depreciation&organizationId=cli&ruc=$RUC&period=$PERIOD"

step "store health"
curl -s -w ' [%{http_code}]\n' -H "Authorization: Bearer $TOKEN" "$BASE/v1/doctor"
