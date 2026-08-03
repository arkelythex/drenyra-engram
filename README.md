# Drenyra Engram

> **Institutional Accounting Memory** — scope-first memory for companies, fiscal periods, policies, and institutional knowledge.

> **Status: pre-alpha.** The memory engine is being extracted from `arkelythex/Drenyra` (`packages/memory`, `packages/agent-memory`) through vertical slices. Nothing here is production-ready yet.

Drenyra Engram is the direct counterpart of Engram, adapted to accounting: persistent observations, mission summaries, learned policies, professional judgments, relations, vigencia, and provenance — searched **scope-first** (company/RUC/period), local and cloud, over MCP, HTTP, CLI, and TUI.

## Critical rule

**Drenyra Engram does not authorize operations.**

```text
Memoria orienta.     Memory guides.
Política restringe.  Policy restricts.
Evidencia demuestra. Evidence demonstrates.
Receipt certifica.   Receipt certifies.
Profesional autoriza. A professional authorizes.
```

Memory informs decisions. It never approves them.

## What it provides

- **Observations** — structured memories with type, scope, topic key, and content.
- **Institutional knowledge** — policies, conventions, and precedents that outlive sessions.
- **Mission summaries and learnings** — persisted at mission end for cross-session recovery.
- **Relations and conflicts** — `related`, `supersedes`, `conflicts_with`, and more between memories.
- **Vigencia** — effective/expiry semantics so stale knowledge is visible, not silently trusted.
- **Provenance** — who/what/when/why for every observation, auditable.
- **Scope-first search** — company/RUC/period filters are first-class, not post-filters.
- **Local and cloud memory** — with clear sync semantics.
- **MCP, HTTP, CLI, TUI** — same engine, multiple surfaces.

## Quick start

```bash
drenyra-engram save
drenyra-engram search
drenyra-engram context
drenyra-engram compare
drenyra-engram review
drenyra-engram promote
drenyra-engram supersede
drenyra-engram doctor
drenyra-engram mcp     # MCP server over stdio (agents)
drenyra-engram serve   # HTTP REST /v1 + MCP /mcp (127.0.0.1)
```

## Layout (Go engine)

```text
cmd/drenyra-engram  CLI entrypoint (save/search/context/doctor/compare/review/promote/supersede/mcp/serve)
internal/core       Core memory model, lifecycle machine, validators
internal/store      SQLite store (pure Go, immutable history)
internal/search     Scope-first search
internal/server     Shared domain services + MCP (JSON-RPC) + HTTP REST adapters
contracts/          Frozen-for-0.1 contract set (memory, scope, lifecycle, provenance)
core/ store/        TypeScript reference implementation (pre-Go, retired by parity)
```

## Surfaces

- **CLI** — `drenyra-engram <command>`; JSON output, exit codes 0/1/2.
- **MCP** — `drenyra-engram mcp` serves the Model Context Protocol over stdio
  (12 `engram_*` tools: save/get/get_by_topic/search/context/compare/doctor/
  review/promote/supersede/relations/transitions); also available as
  `POST /mcp` on the HTTP port. The tool catalog has no authorize/approve/allow
  tool — memory never authorizes.
- **HTTP** — `drenyra-engram serve --addr 127.0.0.1:8787 [--token <secret>]`
  exposes REST `/v1/*` (observations, topic, search, context, compare,
  lifecycle, relations, transitions, doctor) bound to localhost by default;
  when a token is configured every request must present
  `Authorization: Bearer <token>`. Error envelope:
  `{"error": {"code", "message"}}` with statuses 400/404/409/500.

### Scope across surfaces

The CLI and HTTP identify a company by RUC and **derive** `companyId` from it
(`companyId = ruc`) — `search`/`context` on those surfaces only see
observations saved with that derived scope. MCP accepts the **full scope** in
arguments (`organizationId`, `companyId`, `ruc`, `period`), so an MCP client
saving with a custom `companyId` creates memory that CLI/HTTP derived-scope
queries will not surface (exact-scope semantics, fail-closed direction). Save
company memory through the surface you intend to query it from, or keep
`companyId = ruc` for cross-surface visibility.

## Ecosystem

| Project                                                        | Role                                    |
| -------------------------------------------------------------- | --------------------------------------- |
| [Drenyra](https://github.com/arkelythex/Drenyra)               | Accounting Command Center (uses memory) |
| [Drenyra AI](https://github.com/arkelythex/drenyra-ai)         | Agent ecosystem (may integrate)         |
| [Drenyra Pi](https://github.com/arkelythex/drenyra-pi)         | Pi-native harness (reads context)       |

**Direction rule:** Drenyra Engram is independent. It has no dependencies on Drenyra, Drenyra AI, or Drenyra Pi, and it never authorizes operations in any of them.

## License

Proprietary. © 2026 Arkelythex. All rights reserved. See [LICENSE](LICENSE).
