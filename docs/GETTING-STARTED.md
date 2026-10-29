# Get a verified local result

This tutorial creates one scope-bound memory in an isolated local SQLite database,
retrieves it, checks the store, and removes the files. It uses only test-style
content; do not replace the fixture with taxpayer or customer data.

## Before you start

You need a checkout of this repository and Go 1.26 or later. Run the commands
from the repository root.

```bash
go version
go run ./cmd/drenyra-engram help
```

The second command should print the CLI command list. For an installed binary
or another installation path, see [Installation](INSTALLATION.md).

## Create an isolated workspace

The CLI defaults to a fail-closed runtime mode for company-scoped protected
writes. This tutorial explicitly selects `legacy_compat` only for its local,
legacy-class fixture. Do not carry this setting into a deployment.

```bash
export DRENYRA_ENGRAM_TUTORIAL_DIR="$(mktemp -d)"
export DRENYRA_FISCAL_RUNTIME_MODE=legacy_compat
```

All tutorial data stays under `$DRENYRA_ENGRAM_TUTORIAL_DIR`. The fixture uses
the repository's CLI-shaped company scope and descriptive synthetic content.

```bash
cat > "$DRENYRA_ENGRAM_TUTORIAL_DIR/memory.json" <<'JSON'
{
  "topicKey": "tutorial/local-policy",
  "title": "Synthetic local policy",
  "kind": "fact",
  "scope": {
    "kind": "company",
    "organizationId": "cli",
    "companyId": "20100039201",
    "ruc": "20100039201",
    "period": "202401"
  },
  "content": {
    "what": "A local tutorial record",
    "why": "Verify the CLI workflow without operational data",
    "where": "docs/GETTING-STARTED.md",
    "learned": "The record is scoped to one company and period"
  },
  "fiscalEffect": "none",
  "effectiveAt": "2024-01-15T00:00:00Z",
  "source": {
    "system": "tutorial",
    "actorId": "local-user",
    "actorKind": "agent"
  }
}
JSON
```

## Record and retrieve the memory

Save the fixture to the isolated database, then search within its exact company
and period scope.

```bash
go run ./cmd/drenyra-engram save \
  "$DRENYRA_ENGRAM_TUTORIAL_DIR/memory.json" \
  --db "$DRENYRA_ENGRAM_TUTORIAL_DIR/engram.db"

go run ./cmd/drenyra-engram search "Synthetic local policy" \
  --company 20100039201 \
  --period 202401 \
  --db "$DRENYRA_ENGRAM_TUTORIAL_DIR/engram.db"
```

The search result is the first verified outcome: its JSON includes the title
`Synthetic local policy` and the same company scope. It demonstrates scoped
storage and retrieval, not accounting correctness or business authorization.

## Check the local store

```bash
go run ./cmd/drenyra-engram doctor \
  --db "$DRENYRA_ENGRAM_TUTORIAL_DIR/engram.db"
```

`doctor` prints a JSON health report for this database. Its
`fiscalRuntimeMode` should be `legacy_compat`, confirming the intentionally
local setting used for the fixture.

## Clean up

Leave the repository root unchanged and remove only the directory created by
this tutorial:

```bash
test -n "${DRENYRA_ENGRAM_TUTORIAL_DIR:-}" && \
  rm -rf -- "$DRENYRA_ENGRAM_TUTORIAL_DIR"
unset DRENYRA_ENGRAM_TUTORIAL_DIR DRENYRA_FISCAL_RUNTIME_MODE
```

## Next steps

- Read the [Product guide](PRODUCT.md) for the problem Engram addresses and
  its boundaries.
- Read [Consuming Engram](CONSUMING.md) before connecting an MCP client, HTTP
  program, or another CLI workflow.
- Read the [technical reference](../DOCS.md) for the complete CLI,
  configuration, MCP, HTTP, scope, and verification surfaces.
