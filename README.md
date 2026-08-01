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
```

## Layout

```text
cmd/drenyra-engram  CLI entrypoint
core/               Core memory model (observations, topics, relations)
store/              Persistence (local, cloud)
search/             Scope-first search
relations/          Relation graph
lifecycle/          Lifecycle states (draft → reviewed → promoted → superseded)
authority/          Provenance and audit metadata
sync/               Local/cloud sync semantics
cloud/              Cloud backend
mcp/                MCP server
http/               HTTP API
tui/                Terminal UI
clients/            Language clients
```

## Ecosystem

| Project                                                        | Role                                    |
| -------------------------------------------------------------- | --------------------------------------- |
| [Drenyra](https://github.com/arkelythex/Drenyra)               | Accounting Command Center (uses memory) |
| [Drenyra AI](https://github.com/arkelythex/drenyra-ai)         | Agent ecosystem (may integrate)         |
| [Drenyra Pi](https://github.com/arkelythex/drenyra-pi)         | Pi-native harness (reads context)       |

**Direction rule:** Drenyra Engram is independent. It has no dependencies on Drenyra, Drenyra AI, or Drenyra Pi, and it never authorizes operations in any of them.

## License

Proprietary. © 2026 Arkelythex. All rights reserved. See [LICENSE](LICENSE).
