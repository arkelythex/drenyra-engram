<div align="center">

<h1>Drenyra Engram</h1>

<p><strong>Institutional Accounting Memory</strong> — scope-first memory for companies, fiscal periods, policies, and institutional knowledge.</p>

<p>
<a href="https://github.com/arkelythex/drenyra-engram/releases"><img src="https://img.shields.io/github/v/release/arkelythex/drenyra-engram" alt="Release"></a>
<a href="LICENSE"><img src="https://img.shields.io/badge/License-Apache_2.0-blue.svg" alt="License: Apache 2.0"></a>
<img src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white" alt="Go 1.26">
<img src="https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey" alt="Platform">
<img src="https://img.shields.io/badge/tests-360%20TS%20%2B%20Go%20suite-green" alt="Tests">
</p>

</div>

---

> [!IMPORTANT]
> **Open source (Apache-2.0).** This repository is **public** under the
> [Apache License 2.0](LICENSE): source, releases and container images are
> freely available for commercial and non-commercial use, modification and
> redistribution, subject to the license terms. Drenyra Engram is released
> under Apache-2.0 independently of the Drenyra private-product policy.
>
> **v0.7.0 — Evidence Objects (local-first)** (implemented in this repository):
> v0.3 Accounting Memory Kernel, v0.4 authenticated approvals, judgments,
> Ed25519 receipts, offline verification, v0.5 close intelligence, the
> v0.7.0 local-first evidence-object slice (content-addressed WORM object
> bytes, schema-v8 immutable metadata, `object_stored` receipts, scoped
> store/get, object-level rehash verification, CLI/HTTP/MCP surfaces), and the
> v0.9.0 engine-side Review Workspace slice (scope-first review queue over
> `pending_review`, review-detail assembly with evidence/rule state and open
> judgments, authenticated reject with reason, the non-terminal `return`
> decision, SoD + review-checks anti-rubber-stamp gates, velocity alerts, MCP/
> HTTP/CLI surfaces) are implemented and tested. Phase 6 Fiscal Policy
> Memory is DELIVERED (versioned rules with vigencia + jurisdiction,
> structured rule links, regulatory-change impact, rule-version
> verification). See the [ROADMAP](ROADMAP.md) and the
> [due-diligence audit](docs/due-diligence/2026-08-product-architecture-audit.md).

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

Memory informs decisions. It never authorizes them. `approve`/`reject` are
the PROFESSIONAL review of a memory (the human gate), never authorization of
a business action.

## What it provides

- **AccountingMemory v2** — structured institutional accounting memory with
  eight kinds (`fact`, `evidence`, `decision`, `rule`, `exception`,
  `control`, `obligation`, `summary`).
- **Human approval gate** — memories with a fiscal effect
  (`journal_entry`, `declaration`, `closing`, `adjustment`,
  `reclassification`, `approval`, `sunat_filing`) are saved `pending_review`
  and only a HUMAN actor can approve them (`GATE_REQUIRES_HUMAN` otherwise).
- **Triple timestamps** — `effectiveAt` (when it happened accounting-wise),
  `recordedAt` (when it entered the system), `observedAt` (when detected):
  a late event affecting a previous closed period is visible as such.
- **Evidence-backed relations** — 17 relations (`supports`, `contradicts`,
  `explains`, `reconciles`, `reverses`, `approved_by`, …) turn memory into
  an accounting knowledge graph.
- **Local-first evidence objects (v0.7.0)** — immutable artifact bytes
  (XML/PDF/CDR/extracto) stored WRITE-ONCE-READ-MANY under a
  content-addressed layout (`objects/<sha[0:2]>/<sha[2:4]>/<sha256>`); the
  object id IS the SHA-256 of the bytes (identical bytes → duplicate NO-OP),
  schema-v8 immutable metadata, `object_stored` receipts, scoped store/get,
  and `verify object` re-hashing stored bytes. The v0.8.0 evidence lifecycle
  adds versioned retention policies, legal holds, the approved purge
  pipeline, and the lifecycle export. Deferred: cloud/remote object storage,
  the scheduler executor, OCR/content search, production backup/restore.
- **Canonical content hashes** — `contentHash` identifies the immutable
  content; history never mutates, supersession is explicit and atomic.
- **Institutional knowledge** — policies, conventions, and precedents that
  outlive sessions.
- **Mission summaries and learnings** — persisted for cross-session recovery.
- **Vigencia** — effective/expiry semantics so stale knowledge is visible,
  not silently trusted.
- **Scope-first search** — company/RUC/period filters are first-class, not
  post-filters; cross-tenant isolation is structural.
- **Explainable period summary** — the killer demo: why did account 4011 end
  with this balance (facts, approved adjustments, rules applied, evidence,
  late exceptions — ordered by accounting-effective date).
- **MCP, HTTP, CLI** — same engine, multiple surfaces (57 MCP tools: 44
  `accounting_*` + 13 `engram_*`).

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
drenyra-engram object store <file> --ruc <11 digits>   # v0.7.0 evidence object (WORM)
drenyra-engram object get <sha256> --ruc <11 digits>   # scope-first read
drenyra-engram verify object <sha256>                  # rehash + full signed chain
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

## Consuming

See [docs/consuming.md](docs/consuming.md) — how to connect an MCP agent
(Claude Desktop, pi, any stdio client), the HTTP API with curl examples, and
the CLI. The Drenyra adapter is the reference consumer (observability read +
mission write + fiscal memory), live-tested against the released binary.

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

Apache License 2.0. © 2026 Arkelythex. See [LICENSE](LICENSE).
