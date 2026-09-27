# Drenyra Engram

**Institutional accounting memory for systems that need to reconstruct why a record exists.**

Drenyra Engram is a local Go engine for recording, retrieving, and reviewing
scope-bound accounting memories. It keeps immutable revision chains, links
context to evidence and rules, and exposes the same engine through a CLI, MCP,
and HTTP. It is designed to preserve the context around an accounting decision
when the original agent, session, or contributor is gone.

> **Memory guides. Evidence demonstrates. Policy restricts. A professional
> approves a memory review.**

## What it is—and is not

Use Engram to give agents, accounting professionals, and integrators a shared,
traceable memory layer around a deployment-selected ledger.

| Engram is | Engram is not |
| --- | --- |
| A scope-first store for accounting memories, revisions, evidence links, rule links, and review state | A general ledger or the source of truth for accounting balances |
| A local engine with CLI, MCP, and HTTP adapters | A journal-posting, payment, declaration, or SUNAT filing system |
| A record of the engine's immutable acts, with Ed25519 receipts and offline verification | A claim that a professional decision, accounting treatment, or tax outcome is correct |
| A human-gated review workflow for fiscally material memories | A replacement for professional judgment, compliance review, certification, or business authorization |

Remote/cloud evidence storage, a professional web UI, and production-readiness
claims are outside the demonstrated scope of this repository. See the
[roadmap](ROADMAP.md) for delivered slices and explicit deferrals.

## The trust boundary

An agent can record a proposal or context; it cannot supply authority. A
fiscally material memory enters `pending_review`. A verified professional may
approve the specific review envelope, subject to membership, scope, role,
assurance, separation-of-duties, and policy checks. The engine records that
review; it does not authorize a business operation.

`accounting_approve` is registered in the current MCP catalog. It is a
**memory-review approval** tool, not a business-authorization tool: its
arguments never carry identity, and the principal must come from an
authenticated session binding. The stdio MCP server has no such binding, so
this tool fails closed with `AUTHENTICATION_REQUIRED` there. Agents therefore
cannot turn a claimed human identity into approval authority.

The current MCP catalog contains **58 tools**: 13 `engram_*` tools and 45
`accounting_*` tools. The engine also provides authenticated approval routes
on HTTP and authenticated approval commands in the CLI; all three adapters use
the shared engine rather than separate authorization models.

## Start locally

The module declares Go 1.26. Install the binary, then confirm the local store
is healthy before connecting an MCP client:

```bash
go install github.com/arkelythex/drenyra-engram/cmd/drenyra-engram@latest
drenyra-engram doctor
drenyra-engram mcp
```

`doctor` reports local store health and schema information. `mcp` starts the
newline-delimited JSON-RPC server on standard input/output; it uses
`./engram.db` unless `DRENYRA_ENGRAM_DB` is set. For CLI, HTTP, configuration,
and MCP connection details, continue with the [technical reference](DOCS.md).

## One engine, three adapters

```text
CLI ──────┐
MCP ──────┼──> shared Go API ──> scope-first SQLite store
HTTP ─────┘                         ├── immutable memory revisions
                                    ├── Ed25519-signed receipts
                                    └── content-addressed evidence objects
```

Company, organization, RUC, and period make up the exact accounting scope used
by the engine. Reads and mutations use that scope before search ranking or
state changes. Schema migrations are additive and fail closed when the store
layout is unsupported.

Verification is read-only. A valid receipt establishes integrity and provenance
for an Engram act; it does **not** establish accounting correctness. Verification
reports state: **"Accounting correctness: NOT ASSERTED"**.

## Maturity

**Status: alpha.** The repository demonstrates a local accounting-memory
kernel with scoped storage, immutable lifecycle records, authenticated
professional review paths, receipts, offline verification, and local evidence
objects. It does not demonstrate production readiness, a validated customer
workflow, regulatory compliance, a certification, or an ERP/SUNAT integration.

The [product and architecture audit](docs/due-diligence/2026-08-product-architecture-audit.md)
and [roadmap](ROADMAP.md) distinguish repository evidence from open operational
and product decisions.

## Choose your path

| You are | Start here |
| --- | --- |
| An integrator connecting a client or service | [Installation](docs/INSTALLATION.md) · [Consuming Engram](docs/CONSUMING.md) · [Technical reference](DOCS.md) |
| An accounting professional or reviewer | [Approval contract](contracts/approval.md) · [Verification contract](contracts/verification.md) · [Trust model](docs/architecture/trust-model.md) |
| A contributor evaluating the engine | [Architecture](docs/ARCHITECTURE.md) · [Codebase guide](docs/CODEBASE-GUIDE.md) · [Contracts](contracts/README.md) |
| An operator assessing current boundaries | [Security and lifecycle notes](docs/security/evidence-lifecycle-and-threat-model.md) · [Roadmap](ROADMAP.md) |

## Key documentation

- [Technical reference](DOCS.md) — commands, configuration, MCP, HTTP, scope, and verification.
- [Installation](docs/INSTALLATION.md) — source, release-binary, and container paths.
- [Architecture](docs/ARCHITECTURE.md) — engine structure and trust model.
- [Contracts](contracts/README.md) — transport-independent memory, scope, lifecycle, receipt, verification, and approval contracts.
- [Roadmap](ROADMAP.md) — delivered work, deferrals, and readiness gates.

## License

[Apache License 2.0](LICENSE).
