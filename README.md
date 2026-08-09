<div align="center">

<h1>Drenyra Engram</h1>

<p><strong>Institutional Accounting Memory</strong> — scope-first memory for companies, fiscal periods, policies, and institutional knowledge.</p>

<p>
<a href="https://github.com/arkelythex/drenyra-engram/releases"><img src="https://img.shields.io/github/v/release/arkelythex/drenyra-engram" alt="Release"></a>
<a href="LICENSE"><img src="https://img.shields.io/badge/License-Proprietary-red.svg" alt="License: Proprietary"></a>
<img src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white" alt="Go 1.26">
<img src="https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey" alt="Platform">
<img src="https://img.shields.io/badge/tests-277%20TS%20%2B%20Go%20suite-green" alt="Tests">
</p>

</div>

---

> [!IMPORTANT]
> **Private commercial product.** This repository is **private**; releases
> and container images stay private (GHCR, authenticated pulls). Distribution
> is contractual, never public. See the Drenyra
> [Private Product Policy](https://github.com/arkelythex/Drenyra/blob/main/docs/products/private-product-policy.md).
>
> **v0.7.0 — Evidence Objects (local-first)** (implemented in this repository):
> v0.3 Accounting Memory Kernel, v0.4 authenticated approvals, judgments,
> Ed25519 receipts, offline verification, v0.5 close intelligence, and the
> v0.7.0 local-first evidence-object slice (content-addressed WORM object
> bytes, schema-v8 immutable metadata, `object_stored` receipts, scoped
> store/get, object-level rehash verification, CLI/HTTP/MCP surfaces) are
> implemented and tested. Fiscal Policy Memory (v0.6.0) is in progress. See the
> [ROADMAP](ROADMAP.md) and the [due-diligence audit](docs/due-diligence/2026-08-product-architecture-audit.md).

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
  and `verify object` re-hashing stored bytes. Deferred: retention, legal
  hold, export, purge, cloud storage.
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
- **MCP, HTTP, CLI** — same engine, multiple surfaces (50 MCP tools: 37
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
  | `engram_reject` | reject a pending_review memory (terminal, human only) |
  | `engram_void` | void an active/pending_review/approved memory (terminal, human or system, never agent) |
  | `engram_supersede` | promoted → superseded with a required replacement target (never auto-promotes) |
  | `engram_relations` | every recorded relation |
  | `engram_transitions` | the lifecycle audit trail |

      The fiscal lifecycle adds the `accounting_*` catalog (37 tools):

      - **Memory** — `accounting_record`, `accounting_get`, `accounting_search`,
        `accounting_context`, `accounting_timeline`, `accounting_compare`,
        `accounting_link_evidence`
      - **Human gate** — `accounting_approve` (professional review, principal-only)
      - **Judgment** — `accounting_judgment_propose` / `_confirm` / `_reject` /
        `_withdraw` (confirm/reject principal-only)
      - **Reconciliation** — `accounting_reconciliation_propose` / `_confirm` /
        `_reject` / `_withdraw`
      - **Close & periods** — `accounting_close_create`, `accounting_period_reopen`,
        `accounting_period_summary`, `accounting_compare_periods`
      - **Evidence objects** — `accounting_object_store`, `accounting_object_get`
      - **Retention & holds** — `accounting_retention_policy_put` / `_evaluate` /
        `_resolve`, `accounting_hold_place`, `accounting_hold_lift`,
        `accounting_holds_list`
      - **Approved purge** — `accounting_purge_request` / `_approve` / `_reject` /
        `_cancel` / `_withdraw` / `_finalize`
      - **Context & export** — `accounting_current_context`,
        `accounting_lifecycle_export`, `accounting_doctor`

      The tool catalog has no authorize/approve/allow tool — memory never
      authorizes. Domain failures return in-band tool results (isError=true)
  with the engine's stable error codes; shape errors are JSON-RPC -32602.
- **HTTP** — `drenyra-engram serve --addr 127.0.0.1:8787 [--token <secret>]`
  exposes REST `/v1/*` (observations, topic, search, context, compare,
  lifecycle, relations, transitions, doctor) bound to localhost by default;
  when a token is configured every request must present
  `Authorization: Bearer <token>`. Error envelope:
  `{"error": {"code", "message"}}` with statuses 400/404/409/500.

### Production identity (OIDC)

`drenyra-engram serve` can validate OpenID Connect access tokens as a first
production identity slice: stateless RS256 JWT validation with exact
issuer/audience, a DB membership/scope cross-check, and standard assurance
only (no MFA elevation, no revocation beyond DB membership). Enable it with
`DRENYRA_OIDC_ISSUER` and `DRENYRA_OIDC_AUDIENCE`; a partial
`DRENYRA_OIDC_*` set fails startup. See
[docs/architecture/oidc-access-token-identity.md](docs/architecture/oidc-access-token-identity.md).

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
