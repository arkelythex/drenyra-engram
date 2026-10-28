# Drenyra Engram product guide

Drenyra Engram is a local accounting-memory engine for preserving the context
behind a record after the original session, agent, or contributor is gone. It
stores scope-bound memories, immutable revisions, evidence and rule links, and
memory-review state. It is an alpha repository, not a production-readiness
claim.

## The problem it addresses

Accounting work often needs a later answer to a narrower question: why does a
record exist, which context and supporting artifacts informed it, and who
reviewed the related memory? Engram provides a traceable memory layer around a
deployment-selected ledger so that context can be recorded, retrieved, and
verified without making that layer the ledger itself.

## Who should read this guide

| Reader | What Engram currently offers |
| --- | --- |
| Accounting professional or reviewer | A human-gated memory-review workflow with scope, role, policy, and review-envelope checks. |
| Agent or other contributor | A place to record proposals and context, subject to scope and lifecycle rules; it cannot supply approval authority. |
| Integrator or operator | One local Go engine exposed through CLI, MCP, and HTTP adapters. |
| Product evaluator | Repository evidence, explicit boundaries, and labelled hypotheses rather than customer or readiness claims. |

For a runnable local path, start with [Getting started](GETTING-STARTED.md).
For adapter details, continue with [Consuming Engram](CONSUMING.md).

## The delivered workflow

1. A caller records accounting context in an exact company scope, optionally
   with a fiscal period.
2. The engine preserves immutable revisions and can link memories to evidence
   and rules.
3. A fiscal-effect memory enters review state; an authenticated professional
   may review its specific envelope when membership, scope, role, assurance,
   separation-of-duties, and policy checks pass.
4. Readers retrieve context within the same exact scope and verify Engram's
   receipts and stored evidence layers offline.

The engine records its own acts and review state. Its verification output does
not assert that an accounting treatment, tax result, or professional decision
is correct.

## Product boundaries

| Engram does | Engram does not do |
| --- | --- |
| Preserve and retrieve scope-bound accounting memory | Act as a general ledger or balance source of truth |
| Record integrity and provenance for Engram acts | Establish accounting correctness, regulatory compliance, or certification |
| Gate a professional's review of a memory | Authorize journal entries, payments, declarations, filings, or another business operation |
| Store local evidence objects and expose local adapters | Demonstrate remote/cloud evidence storage, a professional web UI, or production readiness |

Model output is an untrusted proposal or explanation until structured source
evidence and human review support it. A receipt proves integrity and provenance
for an Engram act, not the correctness of an accounting outcome. The
[trust model](architecture/trust-model.md) and [verification contract](../contracts/verification.md)
define these boundaries in detail.

## Maturity and evidence

**Current status: alpha.** The repository demonstrates a local kernel with
scope-bound storage, immutable lifecycle records, local evidence objects,
Ed25519 receipts, offline verification, and authenticated professional review
paths. It does not demonstrate a validated customer workflow, a complete
production identity program, regulatory compliance, a certification, cloud
support, or a production ERP/SUNAT integration.

The [product and architecture audit](due-diligence/2026-08-product-architecture-audit.md)
separates implemented evidence from risks and unknowns. The [v1 gate](product/initial-market-and-v1-gate.md)
requires its listed conditions before a v1.0 label.

## Delivered decisions and current hypotheses

Keep an implementation fact separate from a direction that still needs outside
validation.

| Category | Current statement | Status |
| --- | --- | --- |
| Ledger boundary | Engram is not the general ledger; the ledger source is selected per deployment. | Frozen product and engineering boundary |
| Authority boundary | Memory informs and records review; it never authorizes a business operation. | Delivered boundary |
| Local engine | CLI, MCP, and HTTP adapters share one engine. | Delivered repository behavior |
| Initial audience | Peruvian accounting firms serving multiple SMEs is the proposed first paying segment. | Hypothesis; customer validation pending |
| Initial workflow | A monthly close that is explainable and reconstructible across client RUCs is the proposed first paid workflow. | Hypothesis; customer validation pending |
| Buyer, pricing, and metric | A firm partner/owner as buyer, recurring per-firm pricing, and a reconstruction-based North Star are proposed. | Hypotheses; no validated customer, price, baseline, or target |
| First integration direction | CDR/XML comprobante ingestion is the provisional first choice; SIRE is second and ERP integration is deferred. | Product direction, not a production integration |
| Identity direction | An OIDC-compatible provider is the provisional direction for the first workflow. | Product direction; not a complete production identity program |

The [Gate 0 decision record](product/gate-0-decisions.md) owns the provisional
and validated decision states. The [initial-market and v1 gate](product/initial-market-and-v1-gate.md)
records the hypotheses, their falsification conditions, and their required
closure evidence.

## Relationship to the Drenyra ecosystem

Engram is the accounting-memory and evidence-holder component in the documented
Drenyra integration cuts. It provides context, scoped storage, memory-review
surfaces, and integrity receipts. Authority for governed fiscal operations
stays outside Engram in `drenyra-ai`; a professional web UI belongs outside
this repository. Engram therefore supplies evidence about what it stored and
which memory was reviewed, without becoming the authority for a fiscal close,
payment, declaration, or SUNAT action.

See [Drenyra ecosystem integration](ecosystem/dominion-integration.md) for the
repository's documented integration boundaries, and [Architecture](ARCHITECTURE.md)
for the engine's internal structure.
