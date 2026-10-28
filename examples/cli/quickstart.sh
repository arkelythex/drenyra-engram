#!/usr/bin/env bash
# Record, retrieve, and inspect one scoped memory through the CLI.
#
# Expected: save prints "outcome": "created"; search returns one result with
# the fixture title; context lists the current memory; doctor reports
# "fiscalRuntimeMode": "legacy_compat".
source "$(dirname "$0")/../lib.sh"

step "save the synthetic memory"
engram save "$FIXTURE"

step "search inside the exact company and period scope"
engram search "depreciation policy" --company "$RUC" --period "$PERIOD"

step "list the current memory for the scope"
engram context "$RUC" --period "$PERIOD"

step "a different period sees nothing (scope is a filter, not a ranking hint)"
engram search "depreciation policy" --company "$RUC" --period 202402

step "check store health"
engram doctor
