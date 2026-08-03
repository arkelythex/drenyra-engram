# Consuming Drenyra Engram

> **Last updated:** 2026-08-03
>
> How-to: connect an agent (Claude Desktop, pi, any MCP client) or a program
> (HTTP/CLI) to the drenyra-engram memory engine. For the model and semantics,
> see [architecture.md](architecture.md) and the [contracts](../contracts/).

## 1. Run the engine

Download the [v0.2.0 binary](https://github.com/arkelythex/drenyra-engram/releases/tag/v0.2.0)
for your platform (or `go install github.com/arkelythex/drenyra-engram/cmd/drenyra-engram@v0.2.0`),
then:

```bash
# MCP stdio server (agents) — default DB ./engram.db or $DRENYRA_ENGRAM_DB
drenyra-engram mcp

# HTTP REST + MCP endpoint — localhost only by default
drenyra-engram serve --addr 127.0.0.1:8787
```

## 2. Connect an MCP agent (Claude Desktop)

`drenyra-engram mcp` speaks the Model Context Protocol over stdio
(newline-delimited JSON-RPC). Claude Desktop config (`claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "drenyra-engram": {
      "command": "/usr/local/bin/drenyra-engram",
      "args": ["mcp", "--db", "/path/to/engram.db"]
    }
  }
}
```

The server advertises 13 tools (`engram_save`, `engram_search`, `engram_context`,
`engram_chain`, `engram_compare`, `engram_doctor`, lifecycle, relations,
transitions). Company-scoped tools require the exact scope:
`{kind: "company", organizationId, companyId, ruc, period?}` — a period-less
scope only matches period-less observations (scope is part of identity).

## 3. Connect pi (or any stdio MCP client)

Any MCP stdio client uses the same contract: spawn
`drenyra-engram mcp [--db <path>]` and exchange JSON-RPC messages on
stdin/stdout. The non-authorization boundary applies: there is no
authorize/approve/allow tool — memory guides, it never authorizes.

## 4. HTTP API (programs)

`drenyra-engram serve` exposes REST `/v1/*` bound to 127.0.0.1 by default.
When `DRENYRA_ENGRAM_TOKEN` (or `--token`) is set, every request must present
`Authorization: Bearer <token>`.

```bash
# Health
curl -s http://127.0.0.1:8787/v1/doctor

# Save an observation (company-scoped; companyId derives from ruc on reads)
curl -s -X POST http://127.0.0.1:8787/v1/observations \
  -d '{"topicKey":"policy/igv","title":"IGV rate","type":"policy",
       "scope":{"kind":"company","organizationId":"org-1","companyId":"20123456789","ruc":"20123456789"},
       "content":{"what":"IGV is 18%","why":"standard rate","where":"-","learned":"n/a"},
       "provenance":{"actor":"me","timestamp":"2026-08-03T00:00:00Z","source":"curl"}}'

# Search (scope-first)
curl -s "http://127.0.0.1:8787/v1/search?q=IGV&ruc=20123456789&organizationId=org-1"

# Context (current memory per chain)
curl -s "http://127.0.0.1:8787/v1/context?ruc=20123456789&organizationId=org-1"

# Full revision history of a topic key
curl -s "http://127.0.0.1:8787/v1/chain?topicKey=policy/igv&ruc=20123456789&organizationId=org-1"
```

Error envelope: `{"error": {"code", "message"}}` with 400/404/409/500.

## 5. CLI (scripts, humans)

```bash
drenyra-engram save obs.json --db ./engram.db
drenyra-engram search "IGV rate" --company 20123456789 --db ./engram.db
drenyra-engram context 20123456789 --db ./engram.db
drenyra-engram sync --from a.db --to b.db   # additive, conflict-visible
```

## Notes

- **Scope-first**: reads filter by exact scope before ranking; a company-A
  observation is never retrievable from company B.
- **Fail closed**: invalid RUC/period, crafted audit records, and unknown
  states are rejected — never guessed.
- **Best-effort consumers**: the Drenyra adapter treats the engine as advisory
  memory (observability reads fall back; mission writes warn on outage).
