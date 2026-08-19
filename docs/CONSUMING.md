# Connecting Agents and Programs to Drenyra Engram

> How to wire any MCP agent (Claude, pi, OpenCode, Gemini CLI, Codex, …) or a
> program (HTTP / CLI) to the engine. For the model and semantics see
> [architecture.md](architecture.md) and the [contracts](../contracts/).

## 1. Run the engine

```bash
drenyra-engram mcp     # MCP stdio server (agents) — DB ./engram.db or $DRENYRA_ENGRAM_DB
drenyra-engram serve   # HTTP REST /v1 + MCP /mcp on 127.0.0.1:8787
```

Install → [INSTALLATION.md](INSTALLATION.md).

## 2. Connect an MCP agent

`drenyra-engram mcp` speaks the Model Context Protocol over stdio. The server
advertises **57 tools** (13 `engram_*` general memory + 44 `accounting_*`
fiscal) — there is **no authorize/approve/allow tool** (memory never
authorizes).

### Claude Desktop

`claude_desktop_config.json`:

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

### pi (or any stdio MCP client)

Any MCP stdio client uses the same contract: spawn `drenyra-engram mcp` and
exchange JSON-RPC on stdin/stdout. Set the exact scope so agents start with
the right context:

```bash
export DRENYRA_DEFAULT_SCOPE='{"kind":"company","organizationId":"firm-1","companyId":"20100039201","ruc":"20100039201","period":"202601"}'
drenyra-engram mcp
```

The scope is injected into `initialize._meta["drenyra/currentContext"]` and
surfaced by `accounting_current_context` — **exact scope, never inferred**.

### What agents can and cannot do

| Agents can… | Agents can never… |
|---|---|
| `engram_save` / `accounting_record` observations | `accounting_approve` (human session required) |
| propose judgments / reconciliations | confirm them (human adjudication) |
| `accounting_object_store` evidence (WORM) | `accounting_review_reject/return` (human session) |
| propose rule links, ingest comprobantes | execute a journal entry or file with SUNAT |

## 3. HTTP API (programs)

`drenyra-engram serve` binds to `127.0.0.1` by default. When a token is
configured, every request must present `Authorization: Bearer <token>`.

```bash
# Health
curl -s http://127.0.0.1:8787/v1/doctor

# Save an observation (company-scoped; companyId derives from ruc on reads)
curl -s -X POST http://127.0.0.1:8787/v1/observations \
  -H 'Content-Type: application/json' \
  -d '{"topicKey":"policy/igv/rate","title":"IGV base rate","kind":"rule",
       "scope":{"kind":"company","organizationId":"firm-1","companyId":"20100039201","ruc":"20100039201","period":"202601"},
       "content":{"what":"IGV base rate is 18 percent","why":"standard rate for goods","where":"PE","learned":"applies to all invoices"},
       "fiscalEffect":"none","effectiveAt":"2026-01-01T00:00:00Z",
       "validity":{"effectiveAt":"2026-01-01T00:00:00Z","source":"declared"},
       "source":{"system":"curl","actorId":"cli-user","actorKind":"agent"}}'

# Search (scope-first)
curl -s "http://127.0.0.1:8787/v1/search?q=IGV&ruc=20100039201&organizationId=firm-1&period=202601"

# Context (current memory per chain)
curl -s "http://127.0.0.1:8787/v1/context?ruc=20100039201&organizationId=firm-1&period=202601"

# Full revision history of a topic key
curl -s "http://127.0.0.1:8787/v1/chain?topicKey=policy/igv/rate&ruc=20100039201&organizationId=firm-1&period=202601"

# Review queue (scope-first, prioritized)
curl -s "http://127.0.0.1:8787/accounting/review/queue?ruc=20100039201&organizationId=firm-1&period=202601"

# Rule impact (regulatory-change reconstruction) — authenticated
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://127.0.0.1:8787/accounting/rules/policy%2Figv%2Frate/impact?ruc=20100039201&organizationId=firm-1"
```

Error envelope: `{"error": {"code", "message"}}` (400/404/409/500).

## 4. CLI (scripts, humans)

```bash
drenyra-engram save fixture.json --db ./engram.db
drenyra-engram search "IGV rate" --company 20100039201 --period 202601 --db ./engram.db
drenyra-engram context 20100039201 --period 202601 --db ./engram.db
drenyra-engram review queue 20100039201 --period 202601 --db ./engram.db
drenyra-engram rule show policy/igv/rate --ruc 20100039201 --period 202601 --db ./engram.db
drenyra-engram object ingest invoice.xml --ruc 20100039201 --period 202601 --db ./engram.db
drenyra-engram verify memory <memory-id> --db ./engram.db     # 12 layers, NOT ASSERTED
drenyra-engram sync --from a.db --to b.db                      # additive, conflict-visible
```

Authenticated decisions (`approve`, `review reject|return`) require a session:
`drenyra-engram auth login` then the command with `--expected-envelope <hash>`
— the exact envelope the professional reviewed.

## 5. Scope across surfaces

- **CLI / HTTP** derive `companyId = ruc`; reads only see observations saved
  with that derived scope.
- **MCP** accepts the full scope tuple; saving with a custom `companyId`
  creates memory that CLI/HTTP derived-scope queries will not surface
  (exact-scope semantics, fail-closed direction).
- Save company memory through the surface you intend to query it from, or keep
  `companyId = ruc` for cross-surface visibility.

## What to record (institutional memory guidance)

Engram exists so the WHY survives the person and the session. Record
**institutional patterns scoped by RUC**, not transient work:

| Kind | Example | Why it matters |
| --- | --- | --- |
| Provider/tax patterns | "This provider always applies 12% detracción" | Agents propose the right candidate on the first pass |
| Past corrections | "This account was reclassified last month by human error" | Avoid repeating a known mistake |
| Professional criteria | "The accountant prefers criterion X for ambiguous deductible expenses" | Proposals match the professional's judgment |
| Rule applications | "IGV base rate is 18% — applies to all invoices" | Reconstructible reasoning after the author leaves |
| Approved precedents | "The 2025-11 close used method Y for provisions" | Consistency across periods |

Boundary: memory records **what to propose and why**. It never records (and
cannot influence) **how much review a candidate needs** — that is the
deterministic materiality policy in drenyra-ai (BigInt thresholds, frozen).
Memory is context for drafting candidates; gates and materiality are never
decided by memory.

## Notes

- **Money is whole int64 cents** — never floats. Monetary fields on the API
  and CLI are integer cent values; the @drenyra/pi guard enforces the
  convention.
- **Scope-first**: reads filter by exact scope before ranking — a company-A
  observation is never retrievable from company B (a tested invariant).
- **Fail closed**: invalid RUC/period, crafted audit records, and unknown
  states are rejected — never guessed.
- **Best-effort consumers**: the Drenyra adapter treats the engine as advisory
  memory (observability reads fall back; mission writes warn on outage).
