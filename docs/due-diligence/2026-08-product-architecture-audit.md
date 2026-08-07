# Drenyra Engram — Product and Architecture Due Diligence

> **Status:** evidence-based working audit
> **Date:** 2026-08-07
> **Scope:** repository state at audit time, not a substitute for customer, legal, security, or tax validation.

## Executive verdict

**Direction: coherent. Production readiness: not demonstrated. v1.0: not ready.**

The repository has a strong deterministic kernel: scoped memories, immutable revisions, lifecycle controls, authenticated approval, optimistic concurrency, Ed25519 receipts, offline verification, close intelligence, and Go↔TypeScript parity tests. The strongest product boundary is also clear: **Engram records and explains institutional accounting knowledge; it does not assert accounting correctness or replace the ledger.**

The principal risks are not missing cryptographic primitives. They are product definition, source-of-truth ownership, evidence storage/retention, real authentication and SUNAT/ERP integrations, threat-model coverage, operational recovery, and unresolved documentation drift between the architecture, roadmap, and README.

### Decision

Do not expand the domain surface until these are decided and documented:

1. the first paying ICP and one initial workflow;
2. the ledger/source-of-truth boundary;
3. the evidence object and retention model;
4. the production identity provider and approval policy;
5. the first SUNAT/ERP integration;
6. the v1.0 acceptance gate.

## Evidence rules

- **PASS:** the repository contains an implemented behavior and a relevant test or frozen contract.
- **RISK:** part of the behavior exists, but an important production or policy condition is missing.
- **FAIL:** repository evidence contradicts the required property.
- **UNKNOWN:** the question cannot be answered honestly from this repository.

Business, pricing, customer, legal, compliance, and deployment claims are **UNKNOWN unless explicitly recorded as a decision**. A passing hash or receipt test never proves fiscal correctness.

## The first 25 questions

| # | Status | Evidence-based answer |
|---:|:---:|---|
| 1 | UNKNOWN | The technical thesis is clear, but the customer-facing one-sentence problem statement is not frozen. Candidate: reconstruct material accounting decisions from evidence, rules, and approvals. |
| 2 | UNKNOWN | No validated user segment is recorded. |
| 3 | UNKNOWN | No initial customer/ICP is frozen. |
| 4 | UNKNOWN | Pricing and payer are not recorded. |
| 5 | UNKNOWN | Daily operator is not recorded. |
| 6 | UNKNOWN | Economic approver/buyer is not recorded. |
| 7 | RISK | The kernel addresses explainability, provenance, approval, and historical reconstruction; the first monetizable pain is not selected. |
| 8 | UNKNOWN | Frequency is not established by customer evidence. |
| 9 | UNKNOWN | Current cost of the pain is not quantified. |
| 10 | RISK | The repo describes replacement activities but contains no validated current-state workflow study. |
| 11 | RISK | ERP, spreadsheets, files, and email are named as ecosystem context, but competitive insufficiency is not demonstrated. |
| 12 | PASS / RISK | The engine adds immutable memory history, scope-first retrieval, judgments, receipts, and offline verification; whether that wins over ERP plus audit logs is commercially UNKNOWN. |
| 13 | PASS | It is not merely RAG: the domain has immutable revisions, lifecycle, relations, approval, receipts, and deterministic verification. |
| 14 | PASS | It is not merely an audit log: it models accounting meaning, rule links, judgments, close state, and reconstruction. |
| 15 | PASS / RISK | It is more than generic Engram through fiscal kinds, effects, periods, rules, reconciliations, close snapshots, and accounting contracts; the product moat remains unvalidated. |
| 16 | RISK | The repository calls it institutional accounting memory / accounting memory engine. A market category and positioning statement are not frozen. |
| 17 | UNKNOWN | Category strategy is not recorded. |
| 18 | UNKNOWN | No measured product promise is recorded. Recommended candidate: percentage of material decisions reconstructible to evidence + rule + approval. |
| 19 | UNKNOWN | Time-to-value is not measured. |
| 20 | UNKNOWN | The aha moment is not frozen. Recommended candidate: explain account 4011 from balance to source evidence, applicable rule, and approval without the original agent. |
| 21 | UNKNOWN | First company type is not recorded. |
| 22 | UNKNOWN | Company size is not recorded. |
| 23 | UNKNOWN | Monthly document volume is not recorded. |
| 24 | UNKNOWN | Accounting-team size and operating model are not recorded. |
| 25 | UNKNOWN | Internal versus outsourced accounting is not recorded. |

### Product answers that are already safe to state

- **Source of truth:** Engram is not the general ledger. The actual ledger source must be selected per deployment (ERP, ledger service, or a future Drenyra ledger); the repository does not freeze one.
- **Agent authority:** agents may propose memories, links, and judgments; they must not approve, confirm, or impersonate a verified human principal.
- **Human approval:** approval is authenticated, tenant/company/role scoped, guarded by the reviewed envelope hash, and idempotent by tenant/request ID in the implemented v0.4 path.
- **Evidence:** the current kernel records evidence links/provenance, verifies
  availability/integrity layers, and stores **local-first WORM evidence-object
  bytes** (v0.7.0: content-addressed object id = SHA-256 of the bytes, schema-v8
  immutable metadata, `object_stored` receipts, scoped store/get, rehash
  availability verification via `verify object` and the `verify memory` object
  layer). A production-grade object-**retention** service — retention clock,
  legal hold, export, purge, cloud/remote storage — is still not demonstrated
  (explicitly deferred).
- **LLM output:** model output is not accounting evidence. It is an untrusted proposal/explanation until supported by structured source evidence and human review.
- **Ledger boundary:** receipts certify Engram acts, not accounting correctness. This sentence must remain visible in every verification surface.
- **v1.0:** cannot be declared until a real customer workflow, production identity, recovery test, evidence retention, cross-tenant testing, and an externally reviewed threat model exist.

## 660-question register

The supplied 660-question questionnaire is retained as the audit backlog. The following register classifies every block without inventing facts. Individual questions inherit the status shown for their block; a question becomes PASS only when its acceptance evidence is added to the repository or an approved operational record.

| Block | Questions | Current classification | Required closure evidence |
|---|---:|:---:|---|
| A. Product thesis | 1–20 | RISK / UNKNOWN | ICP interview notes, payer, pain baseline, aha demo, measurable promise |
| B. ICP and first workflow | 21–40 | UNKNOWN | One ICP, one workflow, volume, ERP, automation boundary, willingness-to-pay evidence |
| C. Killer demo | 41–58 | PASS for kernel primitives / RISK for end-to-end | Reproducible fixture proving balance → entry → document → XML/CDR → rule → validity → approval → later change |
| D. AccountingMemory | 59–76 | PASS for current model / RISK for semantic completeness | Frozen taxonomy ADR, claim-vs-fact rule, summary provenance and reconstruction tests |
| E. Temporal semantics | 77–92 | PASS for core timestamps / RISK for unresolved fiscal cases | Contract for observed/effective/recorded, timezone normalization, special periods, reopening and retroactive-adjustment tests |
| F. Identity and hashes | 93–110 | PASS for implemented v0.3–v0.6 contracts / RISK for future versioning | Canonicalization spec, Unicode/null policy, currency model, versioned protocol policy |
| G. Migrations | 111–122 | PASS for tested migrations present / RISK for operations | Direct-upgrade matrix, journal/recovery proof, backup/restore policy, migration provenance |
| H. Supersession | 123–138 | PASS for immutable lifecycle / RISK for all branch/delete policies | Concurrency and fork policy, explicit hard-delete/legal-retention decision |
| I. ApprovalPrincipal | 139–158 | PASS in domain contract / RISK in production identity | Real OIDC/MFA provider, membership source, assurance policy, historical principal snapshot |
| J. Authorization | 159–175 | PASS for implemented approval policy / RISK for SoD/override | Versioned policy matrix, dual-approval rules, override audit and segregation tests |
| K. Concurrency | 176–185 | PASS for hash guard / RISK for race depth | Database race tests, deterministic loser errors, all mutation surfaces parity |
| L. Idempotency | 186–194 | PASS for implemented paths / RISK for complete surface coverage | Request replay matrix across HTTP/MCP/CLI/sync and lost-response scenarios |
| M. AccountingJudgment | 195–210 | PASS | Proposal/confirmation separation, immutable correction, evidence/reason, model identity tests |
| N. Evidence | 211–234 | PARTIAL / RISK | Local-first v0.7.0 slice DELIVERED (WORM object bytes, schema v8, `object_stored` receipts, scoped store/get, rehash availability verification, CLI/HTTP/MCP, Go+TS tests); **retention clock, legal hold, export, purge, cloud object storage remain DEFERRED** | Object-retention policy (per-jurisdiction), legal-hold records, tenant export, purge approval flow, backup/restore + corruption drills |
| O. Ed25519 receipts | 235–260 | PASS for kernel protocol / RISK for key operations | HSM/KMS decision, rotation/revocation, compromise response, Go↔TS CI gates |
| P. Offline verification | 261–276 | PASS for kernel protocol + v0.7.0 object verifier / RISK for key operations | PASS/FAIL/UNKNOWN/INCOMPLETE contract, key publication, `verify object` (six receipt layers + principal provenance + WORM byte integrity), UI wording audit | HSM/KMS decision, rotation/revocation, compromise response, Go↔TS CI gates |
| Q. Multi-tenant security | 277–296 | PASS for structural scope claims / RISK for exhaustive coverage | Generated cross-tenant matrix for every operation and nonexistence-safe errors |
| R. Agent security | 297–313 | RISK | Capability matrix, prompt-injection tests, untrusted-document policy, identical MCP/HTTP auth |
| S. LLM correctness | 314–326 | RISK | Deterministic calculation boundary, citations, insufficient-evidence behavior, adversarial evals |
| T. Ledger boundary | 327–338 | RISK | Source-of-truth ADR, immutable ledger links, reversal/reconciliation contract |
| U. Close Intelligence | 339–353 | PASS for v0.5 kernel / RISK for production close | Close policy, re-open authority, version comparison, offline reconstruction and human sign-off |
| V. Fiscal Policy Memory | 354–366 | PASS for design / RISK for completed integration | Official-source capture, publication/vigencia semantics, applicability, human rule approval |
| W. Peru/SUNAT | 367–383 | UNKNOWN | First integration decision, credential architecture, outage/retry/response retention tests |
| X. Privacy | 384–400 | RISK | Data classification, provider routing, redaction, deletion/retention/legal-hold policy |
| Y. Threat model | 401–419 | UNKNOWN / RISK | Written threat model, assets/actors, key/database/object-store compromise playbooks |
| Z. Operability | 420–433 | RISK | Doctor coverage, backup/restore, recovery objectives, tenant export and corruption drills |
| AA. Scale | 434–448 | UNKNOWN | Volume assumptions, large-store benchmark, FTS/partitioning decision, latency/cost budgets |
| AB. Local-first/sync | 449–460 | PASS for additive local sync / UNKNOWN for production authority | Cloud authority, conflict/approval semantics, fork/clock policy, sync invariants |
| AC. APIs/MCP/CLI | 461–472 | PASS for shared services / RISK for long-term compatibility | Surface parity matrix, version negotiation, stable error catalog, tool deprecation policy |
| AD. Go↔TS | 473–482 | PASS for current mirror contracts / RISK for maintenance cost | Protocol ownership decision, golden-vector CI, independent canonicalization and crypto matrix |
| AE. Testing | 483–503 | PASS for current suite / RISK for production assurance | Race/fuzz/property/cross-tenant/generated tests, mutation testing for critical invariants |
| AF. Observability | 504–515 | UNKNOWN / RISK | Structured audit/application logs, correlation IDs, reason-code metrics and alerts |
| AG. Professional UX | 516–530 | UNKNOWN | Review UI flow, evidence/rule diff, materiality/risk display, rubber-stamping controls |
| AH. Human factors | 531–539 | UNKNOWN | Correction/reversal/fraud/velocity/SoD policy and usability evidence |
| AI. Integrations | 540–552 | UNKNOWN | Adapter priority, canonical integration model, first ERP/SUNAT pilot |
| AJ. Open source | 553–570 | FAIL as currently described / RISK | License/product policy reconciliation, moat, contribution review, disclosure, signed releases, SBOM |
| AK. Developer experience | 571–584 | RISK | Ten-minute setup, fixtures, architecture/ADR/threat docs, API and versioning guide |
| AL. Cost/performance | 585–595 | UNKNOWN | Per-company cost model, model tiers, budgets, runaway-agent controls |
| AM. Commercialization | 596–614 | UNKNOWN | Price metric, ROI baseline, pilot results, buyer validation |
| AN. Competition | 615–625 | UNKNOWN | Competitive map and moat evidence based on workflow/trust/integrations/data |
| AO. Product metric | 626–633 | RISK | North Star definition, event instrumentation, baseline and target |
| AP. Non-goals | 634–642 | PASS for stated ledger/authorization boundary / RISK for contract completeness | Published non-goal contract copied into API/UI/docs and reviewed by consumers |
| AQ. v1.0 definition | 643–660 | UNKNOWN | Signed release checklist with customer, security, recovery, integrations, evidence, docs and human-approval gates |

## Documentation findings and corrections required

1. `README.md` shows an MIT badge while `LICENSE` and the product policy state proprietary rights. This is a release-facing contradiction; the badge must say proprietary.
2. `README.md` reports stale test/tool counts and calls the next milestone v0.4 even though the repository documents v0.5 complete and v0.6 in progress.
3. `docs/architecture.md` says the repository does not contain authorization, receipts, or gates, but the current repository implements authenticated approvals, receipts, and verification. The architecture boundary must distinguish **business authorization** from the engine's professional approval and act receipts.
4. `ROADMAP.md` contains completed v0.4 work followed by unchecked completion criteria and an old implementation sequence. The completed milestone must be marked consistently.
    5. `docs/architecture/fiscal-policy-memory-v0.6.md` is a design/integration document, not proof that all v0.6 capabilities are shipped. Its status must remain explicit until the rule-link, impact, verification, and surface batches land.

## Reconciliation (2026-08-07) — v0.7.0 evidence objects delivered

Since this audit's initial pass, the **local-first EvidenceObject slice (v0.7.0)**
has been implemented and fully tested (Go suite green; TypeScript suite green,
277 tests / 20 files). Documentation was reconciled to match the delivered
state, applying the same evidence rules (PASS requires implemented behavior +
test or frozen contract; deferrals stay RISK/DEFERRED, never silently claimed):

- **Delivered and now documented as implemented:** content-addressed WORM object
  bytes (`objects/<sha[0:2]>/<sha[2:4]>/<sha256>`), schema-v8 immutable
  `evidence_objects` metadata (one-transaction v7→v8 migration), `object_stored`
  receipts (subjectType `evidence_object`), scoped store/get (exact
  tenant/company/RUC/period), object-level rehash availability verification
  (`verify object`; `verify memory` object-availability layer; legacy refs
  reported, never failed), and CLI/HTTP/MCP surfaces
  (`object store|get`, `verify object` · `POST/GET /accounting/objects…` ·
  `accounting_object_store|get`).
- **Still explicitly DEFERRED (not implemented, no claims):** retention expiry
  (no retention clock), legal hold, export, purge/deletion, cloud/remote object
  storage, OCR/content search over objects, SUNAT/ERP object ingestion, and
  production object-store operations (backup/restore drills,
  encryption-at-rest/TDE, recovery objectives). Block N remains PARTIAL/RISK
  until these land; the v1.0 gate (G-5) remains only partially met.
- **Documents updated:** docs/security/evidence-lifecycle-and-threat-model.md
  (IMPLEMENTED vs DEFERRED split), ROADMAP.md (Phase 6b DELIVERED),
  README.md (version status, MCP tool counts), contracts/receipts.md,
  contracts/verification.md, contracts/memory.md,
  docs/product/initial-market-and-v1-gate.md (G-5 PARTIAL),
  docs/architecture/evidence-object-v0.7.md (new — resolves the
  code-referenced architecture doc).

## v1.0 gate proposal

Do not label v1.0 until all are true:

- one paying customer profile and one workflow are validated;
- one real or legally approved anonymized close is reproducible;
- Engram's ledger boundary and first ERP/SUNAT integration are explicit;
- human identity, MFA/assurance, membership, SoD, and dual approval are production-backed;
- evidence objects have hash, retention, availability, legal-hold, and export
  semantics — **PARTIAL: hash + availability are delivered (v0.7.0 local-first);
  retention, legal hold and export remain deferred and are still required**;
- cross-tenant, race, fuzz, restore, and corruption drills pass;
- signing-key rotation/revocation and compromise response are tested;
- threat model and privacy/data-provider policy are reviewed;
- verification UI says that cryptographic integrity is **not accounting correctness**;
- the North Star metric has a baseline and a customer-observed target;
- the complete product/license/non-goals documentation is internally consistent.

## Next actions

1. Freeze the six product decisions listed at the top.
2. Correct README, architecture, and roadmap drift.
3. Add the evidence/retention and threat-model documents before adding more integrations.
4. Turn the 25 answers into signed product/architecture decisions, then close the 660-question register with tests or operational evidence rather than prose alone.
