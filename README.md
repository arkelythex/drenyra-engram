<div align="center">

<img width="1200" alt="Drenyra Engram flow — scoped observation → institutional memory/evidence chain → reconstructible context" src="assets/branding/drenyra-engram-flow-banner.svg" />

<p><code>scoped observation → institutional memory/evidence chain → reconstructible context</code></p>

</div>

<div align="center">

**Institutional accounting memory for AI agents**<br>
<em>One brain for fiscal knowledge. Scope-first, audit-grade, agent-agnostic.</em>

> [!IMPORTANT]
> **Drenyra Engram is the open component of the Drenyra ecosystem** —
> licensed under Apache-2.0 and publicly visible, mirroring
> `Gentleman-Programming/engram`. The commercial core (drenyra-ai,
> drenyra-command-center, drenyra-pi, drenyra-skills,
> drenyra-guardian-angel) is **private** and proprietary. Engram informs,
> never authorizes — memory is context, not authority.

</div>

<p align="center">
  <a href="docs/INSTALLATION.md">Installation</a> &bull;
  <a href="docs/CONSUMING.md">Agent Setup</a> &bull;
  <a href="docs/CODEBASE-GUIDE.md">Codebase Guide</a> &bull;
  <a href="docs/ARCHITECTURE.md">Architecture</a> &bull;
  <a href="docs/benchmark/search-baseline-v0.1.md">Benchmark</a> &bull;
  <a href="contracts/README.md">Contracts</a> &bull;
  <a href="ROADMAP.md">Roadmap</a> &bull;
  <a href="DOCS.md">Full Docs</a>
</p>

---

> **engram** `/ˈen.ɡræm/` — _neuroscience_: the physical trace of a memory in the brain.

When an accounting professional leaves — or the agent that drafted a closing
entry is gone — **so is the WHY**. The balance is on the ledger, but the
reasoning, the evidence, the rule that was applied, the professional who
approved it: unrecoverable. Auditors call this a reconstruction problem. Drenyra
Engram turns it into a query.

A **single Go binary** with a scope-first SQLite store, Ed25519 receipts and
offline verification — exposed via CLI, HTTP API, and MCP. Works with **any
agent** that supports MCP. Agents observe and propose. Professionals approve.
The memory becomes **institutional** — it outlives every session, every agent,
every departure.

```
Agent (any MCP client: Claude, pi, OpenCode, Gemini CLI, Codex, ...)
    ↓ MCP stdio
Drenyra Engram (single Go binary)
    ↓
SQLite — scope-first (RUC / period) + immutable history + Ed25519 receipts
    ↓
WORM evidence objects (XML / CDR / PDF) — content-addressed, unalterable
```

## The critical rule

```text
Memoria orienta.      Memory guides.
Política restringe.   Policy restricts.
Evidencia demuestra.  Evidence demonstrates.
Receipt certifica.    Receipt certifies.
Profesional autoriza. A professional authorizes.
```

**Drenyra Engram never authorizes operations.** It records what happened, why,
with which evidence, and who approved it — and it makes that chain
reconstructible offline. It is **not** a ledger, it does **not** post journal
entries, and it does **not** file anything with SUNAT. A signature proves nobody
altered the act — never that the professional decision was correct.

## Quick start

### Install

```bash
go install github.com/arkelythex/drenyra-engram/cmd/drenyra-engram@latest
```

Linux, macOS, Windows binaries and Docker → [docs/INSTALLATION.md](docs/INSTALLATION.md)

### Save, review, reconstruct

```bash
# An agent records an observation (scope-first: RUC + period).
drenyra-engram save --company 20100039201 --period 202601 fixture.json

# A professional sees the review queue — prioritized by materiality.
drenyra-engram review queue 20100039201 --period 202601

# ... approves against the exact envelope they reviewed (SoD enforced).
drenyra-engram review approve <memory-id> --expected-envelope <hash> --reason "..."
```

Monetary values are **whole int64 cents** — never floats (the domain contract).

### Point an agent at it

```bash
drenyra-engram mcp     # MCP server over stdio (Claude, pi, OpenCode, Codex, ...)
drenyra-engram serve   # HTTP REST /v1 + MCP /mcp (127.0.0.1:8787)
```

Full per-agent setup, the HTTP API and the CLI → [docs/CONSUMING.md](docs/CONSUMING.md)

## How it works

```
1. An agent observes — a fact, an evidence link, a proposed decision.
2. Fiscally material observations land in pending_review (the human gate).
3. A professional reviews the queue — diff, evidence state, applicable rule.
4. Approve / reject / return with reason — every act is signed and receipted.
5. The memory becomes institutional — it survives sessions and agents.
6. Later: reconstruct any material balance from evidence + rule + approval.
```

The killer demo: **explain account 4011** — why it ended with this balance,
ordered by accounting-effective date, with the facts, approved adjustments,
rules applied, evidence objects and late exceptions — **without the original
agent**. Reproduced end-to-end in `TestReconstructibleCloseFixture`
([docs/demo/reconstructible-close.md](docs/demo/reconstructible-close.md)).

## What it provides

| Capability | Why it matters |
|---|---|
| **Scope-first memory** | RUC / company / period are structural filters, never post-filters — cross-tenant isolation is impossible to bypass, not a convention |
| **Immutable history** | Revisions never mutate; supersession is explicit and atomic |
| **Human approval gate** | Only an authenticated professional can approve a fiscally material memory — never an agent, never a caller-declared `"human"` |
| **Ed25519 receipts** | Every act is signed and offline-verifiable; integrity is provable without the issuing agent |
| **Offline verification** | 12 layers end with **"Accounting correctness: NOT ASSERTED"** — provenance, never a rubber stamp |
| **Evidence objects (WORM)** | XML / CDR / PDF bytes stored content-addressed; the object id IS the SHA-256 of the bytes; retention, holds and purge are policy-backed |
| **Review workspace** | A scope-first professional queue with diff, evidence state, open judgments, SoD and anti-rubber-stamp gates |
| **Fiscal policy memory** | Versioned rules with vigencia + jurisdiction; regulatory-change impact; rule-version verification in every report |
| **Explainable period summary** | The 4011 killer demo — reconstruction without the original agent |
| **Institutional knowledge** | Policies, conventions and precedents that outlive sessions and teams |

## Surfaces

### MCP (57 tools)

| Family | Tools |
|---|---|
| **engram_** (13) | `save`, `get`, `get_by_topic`, `chain`, `search`, `context`, `compare`, `doctor`, `review`, `promote`, `supersede`, `relations`, `transitions` |
| **accounting_** (44) | record/search/timeline, approve, close, judgments, reconciliations, review queue/detail/reject/return, rule show/history/impact, object store/get/ingest, retention/holds/purge/export, period comparison |

The catalog has **no authorize/approve/allow tool** — memory never authorizes.

### CLI

`save · search · context · doctor · compare · approve · reject · review queue|detail|reject|return · rule show|history|impact · judge · reconcile · object store|get|ingest · verify memory|judgment|receipt|object · close · period-summary · keys · auth · sync · mcp · serve`

### HTTP

`/v1/*` (observations, topic, chain, search, context, compare) · `/accounting/*`
(approve, review, judgments, reconciliations, rules, objects, retention, holds,
purge, export, closings, period comparison). Error envelope:
`{"error": {"code", "message"}}`.

## FAQ

**Is Drenyra Engram a ledger?** No. It records and explains institutional
accounting memory; the deployment-selected ledger remains the source of truth.

**Can an agent approve anything?** No. The approval gate is a human-only,
authenticated, envelope-hash-guarded act — and the proposer can never approve
their own proposal (SoD).

**Does a valid receipt prove the accounting is right?** No. Every verification
report ends with **"Accounting correctness: NOT ASSERTED"**.

**What about SUNAT?** The comprobante ingestion **adapter contract** is
defined (parse + WORM store, `object ingest`), but no production SUNAT/ERP
integration exists — no credentials, no retries, no filings.

**Is it multi-tenant?** Structurally. Every read and mutation is scoped by
organization / company / RUC / period; a company-A query can never see
company-B memory — this is a tested invariant, not a filter.

## Documentation

- [DOCS.md](DOCS.md) — technical reference: environment variables, CLI, MCP tools, HTTP endpoints, scope
- [docs/INSTALLATION.md](docs/INSTALLATION.md) — build, install, Docker
- [docs/CONSUMING.md](docs/CONSUMING.md) — connect any MCP agent, HTTP API, CLI
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — design, trust model, boundaries
- [docs/CODEBASE-GUIDE.md](docs/CODEBASE-GUIDE.md) — repository layout and mental model
- [contracts/](contracts/README.md) — the frozen contract set (memory, scope, lifecycle, provenance, receipts, verification, approval, closing)
- [ROADMAP.md](ROADMAP.md) — delivered milestones and the v1.0 gate
- [docs/due-diligence/2026-08-product-architecture-audit.md](docs/due-diligence/2026-08-product-architecture-audit.md) — evidence-based product audit

## Ecosystem

| Project | Role |
|---|---|
| [Drenyra Command Center](https://github.com/arkelythex/drenyra-command-center) | Command Center — web application (consumes memory) |
| [Drenyra AI](https://github.com/arkelythex/drenyra-ai) | Agent ecosystem (may integrate) |
| [Drenyra Pi](https://github.com/arkelythex/drenyra-pi) | Pi-native harness (reads context) |

**Direction rule:** Drenyra Engram is independent. It has no dependencies on
Drenyra, Drenyra AI, or Drenyra Pi, and it never authorizes operations in any
of them.

## License

Apache License 2.0. © 2026 Arkelythex. See [LICENSE](LICENSE).
