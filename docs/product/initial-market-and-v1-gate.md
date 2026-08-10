# Initial Market and v1 Gate — Frozen, Falsifiable Hypotheses

> **Status:** product decision freeze (draft for review) · **Date:** 2026-08-07
> **Basis:** repository evidence at audit time, per
> [docs/due-diligence/2026-08-product-architecture-audit.md](../due-diligence/2026-08-product-architecture-audit.md).
> **Effect:** this document adds no code and modifies no existing document.

This document freezes the six product decisions the due-diligence audit named as
blocking v1.0 (audit *Decision* section): initial ICP, initial paid workflow,
buyer/operator, the ledger/source-of-truth boundary, the commercial and North
Star hypotheses, and the v1.0 acceptance gate. Every hypothesis below is
**falsifiable and labelled**; a falsified hypothesis triggers a documented
re-review, never silent re-scoping.

## Quick path

1. Read the status legend, then the six decisions D-1…D-6.
2. Each decision states the frozen claim, the repository evidence, the
   falsification test, and what closes it.
3. Use the **v1 gate (D-6)** as the release checklist; nothing ships as v1.0
   with an open gate item.

## Status legend

| Label | Meaning |
|---|---|
| **HYPOTHESIS** | A claim about the market. Falsifiable. **Not validated** by customer evidence (audit blocks B, AM, AO are UNKNOWN/RISK until then). |
| **FROZEN DIRECTION** | A product/engineering invariant. Violating it is a defect, not a finding. |
| **DEFERRED** | Explicitly postponed; deciding it is itself a v1 gate item. |

---

## D-1 — Initial ICP: Peruvian accounting firms serving multiple SMEs

**Status: HYPOTHESIS.**

> The first paying segment is **Peruvian accounting firms (estudios
> contables) that serve multiple SMEs as clients**, each client identified by
> its RUC, with the firm as the organization operating across those RUCs.

**Repository evidence (supports the shape, not the validation):**

- The domain vocabulary is Peru-specific: RUC checksummed scope, SUNAT filing,
  SIRE, PDT 621, IGV, comprobantes, monthly close (cierre) —
  [contracts/memory.md](../../contracts/memory.md) (kinds, fiscal effects),
  [contracts/closing.md](../../contracts/closing.md).
- Scope is structural and multi-level: `organizationId` (the firm) +
  `companyId`/`ruc` (the client) + `period` — [contracts/scope.md](../../contracts/scope.md).
- The v1.0 roadmap names exactly this shape: Phase 7 "Institutional Accounting
  Brain — Accounting firms: shared memory with controlled sync; Authorized
  anonymized cross-company learning" — [ROADMAP.md](../../ROADMAP.md).
- The role ladder implies a firm hierarchy: `accountant < senior_accountant <
  controller` plus tax roles — [contracts/approval.md](../../contracts/approval.md).

**Falsification (any one falsifies):** validated interviews show the first
paying segment is (a) single-company finance departments, (b) outsourced BPOs
rather than the firm itself, or (c) non-Peruvian companies; or firms state the
multi-client organization is not how they operate.

**Closure evidence (audit Block B):** ICP interview notes, company size, client
count, monthly document volume, accounting-team operating model, and
willingness-to-pay evidence recorded in this repository.

---

## D-2 — Initial paid workflow: monthly close + explainability and audit reconstruction across client RUCs

**Status: HYPOTHESIS.** The engine implements the workflow's primitives; no
validated customer workflow is recorded (audit Q10 RISK, Block U PASS/RISK).

> The first paid workflow is the **monthly close (cierre mensual) of a client
> RUC**: produce the close, keep it explainable (why does an account end with
> this balance), and reconstruct any material decision later from evidence +
> rule + approval — per client, with the firm operating across all of its
> client RUCs.

**Repository evidence:**

- Close Intelligence v0.5 is implemented and frozen: `closing/CIERRE-YYYYMM`
  summary memory, controller-only approval, the period write gate
  (`PERIOD_CLOSED` blocks all period-scoped mutations until an explicit
  controller reopen), pending items, `memory_closed`/`memory_reopened`
  receipts — [contracts/closing.md](../../contracts/closing.md),
  [docs/architecture/close-intelligence-v0.5.md](../architecture/close-intelligence-v0.5.md).
- Explainability: the "killer demo" — explainable period summary of account
  4011 — [README.md](../../README.md), [ROADMAP.md](../../ROADMAP.md) (Phase 3).
- Reconstruction primitives: triple timestamps (effective/recorded/observed),
  immutable revisions + explicit supersession, evidence links, Ed25519
  receipts, and offline verification layers (evidence availability, chain
  continuity) — [contracts/memory.md](../../contracts/memory.md),
  [contracts/lifecycle.md](../../contracts/lifecycle.md),
  [contracts/receipts.md](../../contracts/receipts.md),
  [contracts/verification.md](../../contracts/verification.md).
- The audit's candidate one-sentence problem statement (Q1) and aha moment
  (Q20) match this workflow: "reconstruct material accounting decisions from
  evidence, rules, and approvals" and "explain account 4011 from balance to
  source evidence, applicable rule, and approval without the original agent".

**Falsification (any one falsifies):** validated evidence that firms would pay
first for a different workflow (declarations/filing, reconciliation-only,
policy/research); or that month-end close is not a blocking pain; or that the
ERP plus its audit log already provides the reconstruction firms need.

**Closure evidence:** one real or legally approved anonymized close reproduced
end-to-end (gate G-2) and willingness-to-pay evidence for this specific
workflow.

---

## D-3 — Buyer and operator

**Status: HYPOTHESIS** (audit Q5/Q6 UNKNOWN).

> **Operator:** the accountant / senior accountant at the firm who executes
> closes per client RUC and reviews pending memories (the daily user).
> **Approver inside the workflow:** the `controller` role, which the approval
> policy requires for `closing` (and `tax_reviewer` /
> `authorized_tax_professional` for declarations/filings).
> **Buyer (economic approver):** the firm's partner/owner, who pays one
> subscription covering the firm's clients.

**Repository evidence:**

- Role ladder and per-fiscal-effect policy matrix —
  [contracts/approval.md](../../contracts/approval.md) (`closing` → `controller`).
- Human-only approval gate with authenticated principal and company-scoped
  authorization — [contracts/lifecycle.md](../../contracts/lifecycle.md),
  [docs/decisions/ADR-003-approval-principal.md](../decisions/ADR-003-approval-principal.md).
- The operator works per exact scope; session context is exact-scope, never
  inferred (`DRENYRA_DEFAULT_SCOPE`) — [contracts/period-comparison.md](../../contracts/period-comparison.md).

**Falsification (any one falsifies):** purchase decisions happen at the client
company rather than the firm; the daily operator is a non-accountant admin or
an outsourced BPO; or the paying unit is per-client rather than per-firm.

**Closure evidence:** buyer/operator interview notes and a decision record on
per-firm vs per-client commercial packaging.

---

## D-4 — Source-of-truth boundary

**Status: FROZEN DIRECTION** (not a hypothesis to validate; the audit lists it
under "Product answers that are already safe to state").

> Drenyra Engram is **not the general ledger**. The actual ledger source must
> be selected per deployment (ERP, ledger service, or a future Drenyra
> ledger); this repository does not freeze one. Engram **records and
> explains** institutional accounting memory; it never authorizes business
> operations, and receipts certify **Engram's own acts**, never accounting
> correctness.

**Repository evidence:**

- Non-authorization boundary is the defining invariant —
  [docs/architecture.md](../architecture.md),
  [docs/architecture/trust-model.md](../architecture/trust-model.md),
  [docs/architecture/ecosystem-boundaries.md](../architecture/ecosystem-boundaries.md),
  [contracts/provenance.md](../../contracts/provenance.md).
- Every verification report ends with exactly "Accounting correctness:
  NOT ASSERTED" — [contracts/verification.md](../../contracts/verification.md).
- The approval gate is the professional review of a memory, never
  authorization of a declaration, payment, or filing —
  [contracts/lifecycle.md](../../contracts/lifecycle.md) rule 5.
- ROADMAP non-goals: no authorization engine, no cloud offering in this repo —
  [ROADMAP.md](../../ROADMAP.md).

**Falsification (a violation, not a finding):** any implementation in which
Engram becomes the ledger, or any verifier/UI presents cryptographic integrity
as accounting correctness (gate G-9), or any surface offers an
authorize/allow/execute/file/pay/declare operation.

---

## D-5 — Commercial and North Star hypotheses

**Status: HYPOTHESIS / metric TBD** (audit Blocks AM and AO UNKNOWN/RISK).

**Commercial hypothesis (HYPOTHESIS):**

> Firms will pay a recurring fee per firm (covering all client RUCs) when the
> workflow measurably cuts month-end close effort and makes audit
> reconstruction faster and more defensible. **The pricing metric and price
> point are NOT set** (DEFERRED; audit Block AM closure evidence required).

**North Star hypothesis (candidate, to be confirmed):**

> **Percentage of material decisions reconstructible to evidence + rule +
> approval without the original agent**, with the aha moment being the
> end-to-end explanation of a balance (e.g., account 4011) from source
> evidence, applicable rule, and approval. Baseline and customer-observed
> target are NOT yet defined (gate G-10).

**Repository evidence:** the audit's recommended candidates for Q18 and Q20;
the measurement surface already exists in the engine (envelope hashes,
evidence-availability verification, supersession chains, close snapshots) —
[contracts/verification.md](../../contracts/verification.md),
[contracts/closing.md](../../contracts/closing.md).

**Falsification (any one falsifies):** firms state they would not pay for this
workflow; the metric cannot be measured from repository state plus customer
workflow; or reconstruction is not the defensibility problem firms name.

---

## D-6 — v1 gate

**Status: FROZEN DIRECTION as a release rule.** The gate is the audit's
"v1.0 gate proposal" converted to a checklist. **No release is labelled v1.0
with an open item.** Current status of every item: **NOT MET**, unless noted.

| # | Gate criterion | Closes | Status now | Evidence / owner |
|---|---|---|---|---|
| G-1 | One paying customer profile and one workflow validated | D-1, D-2, D-3 | NOT MET | ICP + workflow closure evidence (audit Block B) |
| G-2 | One real or legally approved anonymized close is reproducible end-to-end | D-2 | PARTIAL (the deterministic FICTIONAL fixture is delivered and green: `TestReconstructibleCloseFixture` — versioned rules + WORM evidence + late event + adjudicated contradiction + approvals + close + offline verification; G-2 still requires ONE real or legally approved anonymized close reproduced the same way) | [docs/demo/reconstructible-close.md](../demo/reconstructible-close.md); audit Block C |
| G-3 | Ledger boundary and first ERP/SUNAT integration are explicit | D-4 | PARTIAL (ledger boundary FROZEN — D-4; first integration DECIDED at Gate 0 — G0-5: CDR/XML comprobante ingestion first, SIRE second, ERP deferred; the ADAPTER CONTRACT is defined; production integration is NOT implemented — no credentials/retries/outage/response-retention, audit Block W) | [gate-0-decisions.md](gate-0-decisions.md); design brief §7.3 |
| G-4 | Human identity, MFA/assurance, membership, SoD, dual approval production-backed | D-3 | NOT MET | Production identity provider is DEFERRED (audit decision #4); `oidc` is recognized but not resolvable — [contracts/approval.md](../../contracts/approval.md) |
| G-5 | Evidence objects have hash, retention, availability, legal-hold, export semantics | — | PARTIAL (hash + availability DELIVERED v0.7.0; retention policies + legal holds + approved purge pipeline + lifecycle export DELIVERED v0.8.0 — schema v12; **still DEFERRED: cloud/remote object storage, scheduler executor, OCR/content search, production backup/restore drills**) | [docs/architecture/evidence-lifecycle-v0.8.md](../architecture/evidence-lifecycle-v0.8.md); [docs/security/evidence-lifecycle-and-threat-model.md](../security/evidence-lifecycle-and-threat-model.md) |
| G-6 | Cross-tenant, race, fuzz, restore, and corruption drills pass | — | PARTIAL (cross-tenant structural + race coverage exist; fuzz/restore/corruption drills not demonstrated) | [contracts/scope.md](../../contracts/scope.md); audit Blocks G, K, Q, Z |
| G-7 | Signing-key rotation/revocation and compromise response are tested | — | PARTIAL (rotation + revocation are implemented and tested; compromise-response playbook is not) | [contracts/receipts.md](../../contracts/receipts.md); audit Block O |
| G-8 | Threat model and privacy/data-provider policy are reviewed | — | NOT MET (first written threat model: [docs/security/evidence-lifecycle-and-threat-model.md](../security/evidence-lifecycle-and-threat-model.md); external review pending) | audit Block Y |
| G-9 | Verification UI says cryptographic integrity is **not** accounting correctness | D-4 | PARTIAL (CLI reports end with "Accounting correctness: NOT ASSERTED"; product UI does not exist in this repo) | [contracts/verification.md](../../contracts/verification.md) |
| G-10 | North Star metric has a baseline and a customer-observed target | D-5 | NOT MET | Metric definition + instrumentation plan |
| G-11 | Product/license/non-goals documentation is internally consistent | — | PARTIAL (license decision flipped to Apache-2.0 — ADR-004; doc repair across README/RELEASING/SECURITY/threat-model in progress) | [docs/due-diligence/2026-08-product-architecture-audit.md](../due-diligence/2026-08-product-architecture-audit.md); [ADR-004](../../docs/decisions/ADR-004-open-source-license.md) |

**Non-goals re-affirmed for v1 scope:** authorization engine, cloud offering,
PostgreSQL inside this repository, product UI — [ROADMAP.md](../../ROADMAP.md)
(non-goals), [docs/architecture/ecosystem-boundaries.md](../architecture/ecosystem-boundaries.md).

**Owners and review dates (draft — pending owner confirmation at Gate 0,
2026-08-17):** every gate item is owned by Arkelythex (product owner); the
review cadence is per-evidence, with the hard checkpoint at the Gate 0
deadline (2026-08-17) for G-3/G-4 (integration + identity decisions) and at
each customer-evidence milestone for G-1/G-2/G-10. A gate marked PARTIAL may
advance only when its missing piece is explicitly tracked (never silently).

---

## What this document does NOT claim

- **No validated customers, prices, or pilots.** D-1, D-2, D-3, D-5 remain
  hypotheses until their closure evidence lands (audit Blocks B, AM, AO).
- **No implemented SUNAT/ERP integration** (D-6 G-3; audit Block W UNKNOWN).
- **No production identity provider or MFA** (G-4; `oidc` is not resolvable).
- **No cloud infrastructure or managed offering** (ROADMAP non-goal).
- **No compliance certification** of any kind (audit: legal/tax/compliance
  validation is out of scope for repository evidence).
- **No production evidence-object retention/export/purge** (G-5): local WORM
  storage, hash identity and availability verification are delivered (v0.7.0);
  retention, legal hold, export, purge and cloud object storage are deferred —
  see the [security doc](../security/evidence-lifecycle-and-threat-model.md).

## Next step

Review and sign these decisions (product + architecture). Once signed, the
companion [evidence lifecycle and threat model](../security/evidence-lifecycle-and-threat-model.md)
provides the evidence/retention and threat-model layers this gate depends on,
and the audit's "Next actions" items 1 and 3 can close.
