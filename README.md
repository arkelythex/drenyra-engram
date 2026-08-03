<div align="center">

<h1>Drenyra Engram</h1>

<p><strong>Institutional Accounting Memory</strong> — scope-first memory for companies, fiscal periods, policies, and institutional knowledge.</p>

<p>
<a href="https://github.com/arkelythex/drenyra-engram/releases"><img src="https://img.shields.io/github/v/release/arkelythex/drenyra-engram" alt="Release"></a>
<a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="License: MIT"></a>
<img src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white" alt="Go 1.26">
<img src="https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey" alt="Platform">
<img src="https://img.shields.io/badge/tests-106%20Go%20%2B%2031%20TS-green" alt="Tests">
</p>

</div>

---

> [!IMPORTANT]
> **v0.2.0 released** (2026-08-03) — Go engine Phase 2 complete: CLI (11 commands) + MCP (13 tools) + HTTP REST + local sync + chain history. Static binaries for macOS/Linux/Windows; the Go module is go-gettable at `github.com/arkelythex/drenyra-engram@v0.2.0`. See the [release notes](https://github.com/arkelythex/drenyra-engram/releases/tag/v0.2.0) and the [ROADMAP](ROADMAP.md).

Drenyra Engram is the standalone, scope-first institutional accounting memory engine (Go): persistent observations, mission summaries, learned policies, professional judgments, relations, vigencia, and provenance — searched **scope-first** (company/RUC/period), over MCP, HTTP, CLI, and local sync. First consumer: the Drenyra adapter (observability read, mission write, fiscal memory), live-tested against the released binary.

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
  (13 `engram_*` tools), also available as `POST /mcp` on the HTTP port:

  | Tool | Operation |
  |---|---|
  | `engram_save` | upsert an observation (new immutable revision) |
  | `engram_get` | one observation by id |
  | `engram_get_by_topic` | latest revision of a (topicKey, scope) chain |
  | `engram_chain` | FULL revision history of a chain (ascending) |
  | `engram_search` | scope-first token-overlap search |
  | `engram_context` | current memory for a scope (latest per chain) |
  | `engram_compare` | identity/scope/content deltas + relation verdict |
  | `engram_doctor` | store health (schema guards, counts) |
  | `engram_review` / `engram_promote` / `engram_supersede` | lifecycle transitions (adjacent-forward) |
  | `engram_relations` | every recorded relation |
  | `engram_transitions` | the lifecycle audit trail |

  The tool catalog has no authorize/approve/allow tool — memory never
  authorizes. Domain failures return in-band tool results (isError=true)
  with the engine's stable error codes; shape errors are JSON-RPC -32602.
- **HTTP** — `drenyra-engram serve --addr 127.0.0.1:8787 [--token <secret>]`
  exposes REST `/v1/*` (observations, topic, search, context, compare,
  lifecycle, relations, transitions, doctor) bound to localhost by default;
  when a token is configured every request must present
  `Authorization: Bearer <token>`. Error envelope:
  `{"error": {"code", "message"}}` with statuses 400/404/409/500.

- **Sync** — `drenyra-engram sync --from <src-db> --to <dst-db>` reconciles two
  local stores additively: full revision history, relations and the lifecycle
  audit trail cross with original ids/provenance; status propagates via
  transition replay. Divergence is **surfaced, never silently resolved**:
  divergent chain heads are preserved in both stores and linked with a
  `conflicts_with` relation plus a report entry. Re-running the same pair is a
  no-op. Cloud sync is deferred (ROADMAP non-goals).

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
