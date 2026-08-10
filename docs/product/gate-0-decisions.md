# Gate 0 — Bounded Product Decisions

> **Status:** draft for review · **Date:** 2026-08-10
> **Freeze deadline:** 2026-08-17
> **Owner of all records:** Arkelythex (product owner) unless noted.
> **Companion document:** [initial-market-and-v1-gate.md](initial-market-and-v1-gate.md)
> freezes the six product hypotheses D-1…D-6. This record is the design brief's
> Gate 0 (§5): it maps the six required decisions to D-1…D-6 where they exist and
> adds the three decision records that were previously only deferred gate items.

## Decision states (design brief §5.2)

| State | Meaning |
|---|---|
| **VALIDATED** | Supported by customer, operational, legal, or technical evidence in the repository. |
| **PROVISIONAL** | Adopted temporarily with assumptions, risks, owner, missing evidence, and review date. Does not silently block Phase 6; work stops only if the decision creates a contradiction in security, authority, tenant isolation, or evidence preservation. |
| **DEFERRED** | Explicitly outside current scope, with impact documented. |

## The six Gate 0 decisions

| # | Required decision | Existing record | State (Gate 0) |
|---|---|---|---|
| 1 | Initial ICP and initial monthly-close workflow | D-1 + D-2 | PROVISIONAL |
| 2 | Ledger / source-of-truth boundary | D-4 | VALIDATED |
| 3 | Evidence retention, legal hold, export, deletion model | — (new: G0-3) | PROVISIONAL |
| 4 | Production identity provider and approval/SoD policy | — (new: G0-4) | PROVISIONAL |
| 5 | First SUNAT or ERP integration | — (new: G0-5) | PROVISIONAL |
| 6 | v1.0 acceptance gate | D-6 | VALIDATED (as release rule) |

---

## G0-1 — Initial ICP and initial monthly-close workflow

**Decision:** The initial ICP is Peruvian accounting firms serving multiple
SMEs (each client identified by RUC), and the first paid workflow is the
monthly close of a client RUC, explainable and reconstructible from evidence

+ rule + approval, with the firm operating across all its client RUCs.

**State:** PROVISIONAL (market hypothesis; customer validation pending).

+ **Owner:** Arkelythex.
+ **Supporting evidence:** [D-1](initial-market-and-v1-gate.md#d-1--initial-icp-peruvian-accounting-firms-serving-multiple-smes)
  and [D-2](initial-market-and-v1-gate.md#d-2--initial-paid-workflow-monthly-close--explainability-and-audit-reconstruction-across-client-rucs)
  with repository evidence (Peru-specific vocabulary, structural multi-level
  scope, role ladder, Close Intelligence v0.5, killer demo account 4011).
+ **Assumptions:** firms operate multi-client; month-end close is a blocking
  pain; ERP audit logs do not already provide the reconstruction.
+ **Known risks:** ICP could be single-company finance departments, BPOs, or
  non-Peruvian firms; workflow priority could be filings, not close (D-1/D-2
  falsification clauses).
+ **Affected contracts/components:** scope.md, closing.md, approval.md
  (role ladder), period-comparison.md, Review Workspace (block AG).
+ **Review date:** 2026-08-17; every 4 weeks after first customer interviews.
+ **Consequence if invalidated:** Phase 6 reconstruction chain and Review
  Workspace re-target the validated segment/workflow; D-1/D-2 falsification
  triggers documented re-review, never silent re-scoping.

## G0-2 — Ledger / source-of-truth boundary

**Decision:** Drenyra Engram is not the general ledger. The ledger source is
selected per deployment (ERP, ledger service, or future Drenyra ledger); this
repository does not freeze one. Engram records and explains institutional
accounting memory; it never authorizes operations, and receipts certify
Engram's own acts, never accounting correctness.

**State:** VALIDATED (frozen invariant).

+ **Owner:** Arkelythex (violations are defects, not findings).
+ **Supporting evidence:** [D-4](initial-market-and-v1-gate.md#d-4--source-of-truth-boundary);
  trust-model.md, ecosystem-boundaries.md, provenance.md; every verification
  report ends with "Accounting correctness: NOT ASSERTED".
+ **Assumptions:** none (invariant, not a hypothesis).
+ **Known risks:** product UI (Review Workspace) could blur the boundary;
  gate G-9 guards the wording.
+ **Affected contracts/components:** provenance.md, verification.md,
  lifecycle.md rule 5, Review Workspace acceptance criteria (§6.5 of the
  design brief), non-goals.
+ **Review date:** only on a proposed boundary change.
+ **Consequence if invalidated:** a violation is a defect; the design brief's
  non-goals (no journal entries, no SUNAT filing from memory) must be re-read
  as broken.

## G0-3 — Evidence retention, legal hold, export, and deletion model

**Decision (PROVISIONAL, confirmed 2026-08-10):** Peru-first, **Perú-only for v1** jurisdiction scope; legal/audit holds at object level; approved purge pipeline
(request → approval → physical execution); read-only deterministic export.
Concrete retention periods per document class are **pending legal/tax
validation** — no period is frozen until counsel confirms.

**State:** PROVISIONAL.

+ **Owner:** Arkelythex (with legal/tax counsel for retention periods).
+ **Supporting evidence:** engine mechanics DELIVERED in v0.8 — retention
  policy + eligibility, object-level holds, approved purge, deterministic
  lifecycle export (docs/architecture/evidence-lifecycle-v0.8.md); audit
  Block N: "Object-retention policy (per-jurisdiction), legal-hold records,
  tenant export, purge approval flow" as required closure.
+ **Assumptions:** SUNAT/Código Tributario record-keeping rules apply to
  stored comprobantes; **Perú-only for v1** (confirmed);
  retention periods can differ by document class and fiscal effect.
+ **Known risks:** wrong retention period = legal exposure or premature
  evidence loss; purge is irreversible once executed; multi-jurisdiction
  (LATAM, Phase 6) complicates policy tables.
+ **Affected contracts/components:** evidence-object v0.7 / evidence-lifecycle
  v0.8 contracts (retention, hold, purge, export), receipts (purge approvals),
  verification (availability after hold).
+ **Review date:** 2026-08-17 (candidate + legal-validation owner named);
  re-review on any legal/tax opinion.
+ **Consequence if invalidated:** if a validated period contradicts a
  candidate, only the period table changes (mechanics are already delivered);
  if the legal-hold model is rejected, the object store API surface changes
  before v1.

## G0-4 — Production identity provider and approval/SoD policy

**Decision (PROVISIONAL, confirmed 2026-08-10):** authenticate professionals through an
**OIDC-compatible identity provider**; `authenticationMethod: "oidc"` becomes
resolvable (today it is recognized but not resolvable). Provider: **self-hosted
Keycloak**, frozen for the first workflow (fits the local-first posture, no cloud
dependency); a managed IdP (e.g. Entra ID / Google Workspace) remains a
per-deployment option for firms that prefer it.
Approval/SoD: keep the role ladder (`accountant < senior_accountant <
controller` + tax roles); **dual approval remains DEFERRED** (audit Block J).

**State:** PROVISIONAL.

+ **Owner:** Arkelythex.
+ **Supporting evidence:** contracts/approval.md (principal model, role/assurance
  matrix, `oidc` recognized-not-resolvable, minimum assurance `standard`,
  `sunat_filing` → `strong`); ADR-003 (approval principal); audit Block I
  ("Real OIDC/MFA provider, membership source, assurance policy, historical
  principal snapshot").
+ **Assumptions:** the firm can operate an IdP (managed or self-hosted) per
  deployment; MFA is achievable with the chosen provider; membership source
  can be mapped to the engine's tenant/company/role model.
+ **Known risks:** no HSM/KMS for signing keys (audit O); self-hosted IdP
  shifts operational burden to the firm; historical principal snapshot and
  assurance levels must survive provider changes.
+ **Affected contracts/components:** approval.md (authenticationMethod,
  assuranceLevel), auth principal, sessions, signing-key lifecycle,
  Review Workspace (SoD display).
+ **Review date:** 2026-08-17 (provider selection + assurance policy);
  re-review on provider change.
+ **Consequence if invalidated:** a different provider changes only the
  transport integration (contracts stay); dropping MFA lowers assurance
  guarantees and raises audit Block I risk.

## G0-5 — First SUNAT or ERP integration

**Decision (PROVISIONAL, confirmed 2026-08-10):** first integration is **CDR/XML
comprobante ingestion** — matching the v0.7 evidence-object store and the
reconstruction chain (balance → entry → XML/CDR → rule → approval). SIRE
(libros electrónicos) is the second candidate; ERP integration is DEFERRED.
No production integration is claimed until credentials, retries, outage
behavior, response retention, and source authority are implemented and tested
(design brief §7.3).

**State:** PROVISIONAL.

+ **Owner:** Arkelythex.
+ **Supporting evidence:** v0.7 evidence objects store exactly XML/CDR-shaped
  artifacts (content-addressed WORM); audit Block W UNKNOWN (no integration
  decision recorded); gate G-3 DEFERRED.
+ **Assumptions:** comprobante XML/CDR is the most relevant, lowest-dependency
  artifact to ingest manually first (demo boundary); SIRE exports are
  obtainable from the firm; ERP vendors will not be necessary for the first
  paid workflow.
+ **Known risks:** SUNAT API/credential handling, outage/retry, and response
  retention are real production requirements (Block W); claiming ingestion
  without them breaks the demo boundary.
+ **Affected contracts/components:** evidence-object store, object ingestion
  surface (new), fiscal-policy-memory v0.6 (rule applicability), verification
  (source authority).
+ **Review date:** 2026-08-17 (first-integration choice); re-review after the
  reproducible fixture (delivery order item 4).
+ **Consequence if invalidated:** if the first workflow needs ERP data instead,
  the ingestion priority swaps (SIRE/ERP first); the manual-ingestion demo
  fixture still stands as the falsification boundary.

## G0-6 — v1.0 acceptance gate

**Decision:** no release is labelled v1.0 with an open item in the D-6
checklist (G-1…G-11). The gate is a release rule, not a target date.

**State:** VALIDATED (as release rule).

+ **Owner:** Arkelythex.
+ **Supporting evidence:** [D-6](initial-market-and-v1-gate.md#d-6--v1-gate);
  audit "v1.0 gate proposal"; current statuses: G-1 NOT MET, G-2 NOT MET,
  G-3 NOT MET, G-4 NOT MET, G-5 PARTIAL, G-6 PARTIAL, G-7 PARTIAL, G-8 NOT
  MET, G-9 PARTIAL, G-10 NOT MET, G-11 PARTIAL (license resolved via ADR-004).
+ **Assumptions:** gate items are independent; a PARTIAL item may advance when
  its missing piece is explicitly tracked, never silently.
+ **Known risks:** gate scope creep delays release; treating PARTIAL as PASS
  breaks the audit discipline.
+ **Affected contracts/components:** all; the gate is the release contract.
+ **Review date:** 2026-08-17 (owners and dates for every gate item).
+ **Consequence if invalidated:** weakening the gate re-opens audit blocks B,
  C, I, N, W, Y and voids the "frozen, falsifiable" posture.

---

## Open items pending owner confirmation (as of 2026-08-10)

Resolved on 2026-08-10 (owner): **G0-3** jurisdiction scope → Perú-only for
v1; **G0-4** identity provider → self-hosted Keycloak (managed IdP remains a
per-deployment option); **G0-5** first integration → CDR/XML comprobante
ingestion (SIRE second, ERP deferred).

Still open (freeze deadline 2026-08-17):

1. **G0-3:** retention periods per document class (needs legal/tax counsel
   sign-off).
2. **G0-4:** MFA minimum for `closing`/`sunat_filing` with the chosen provider;
   membership-source mapping to the engine's tenant/company/role model.
3. **G0-6:** named owner + review date per gate item G-1…G-11.
4. **D-1/D-2/D-3/D-5 (existing hypotheses):** confirm they keep their
   HYPOTHESIS status under this record's legend (PROVISIONAL) until customer
   evidence lands.

No open item in this section should be represented as implemented.
