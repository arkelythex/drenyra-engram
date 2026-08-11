# Proposal — audit-register-closure

> Phase: propose · Artifact: proposal · Status: draft
> Strict TDD: not active in this phase (planning only); strict TDD applies during apply and verify.

## Problem statement

Drenyra Engram's due-diligence register still classifies five trust-critical areas as partially proven even though much of the underlying behavior is already implemented. The remaining gap is primarily consolidated, code-verifiable evidence rather than new product capability:

- **G. Migrations (111–122):** PASS for tested migrations present / RISK for operations. The repository lacks a direct-upgrade matrix, explicit crash/recovery proof, and a frozen migration-provenance decision.
- **J. Authorization (159–175):** PASS for implemented approval policy / RISK for SoD/override. Versioned policies and segregation rules exist, but there is no consolidated policy matrix, negative override conformance test, or formal decision that overrides are deliberately absent.
- **L. Idempotency (186–194):** PASS for implemented paths / RISK for complete surface coverage. Replay behavior is tested in scattered store and HTTP paths, but MCP/CLI replay and non-purge lost-response scenarios are not comprehensively proven.
- **Q. Multi-tenant security (277–296):** PASS for structural scope claims / RISK for exhaustive coverage. Strong isolation examples exist, but no generated matrix covers every API operation and its nonexistence-safe cross-tenant result.
- **Z. Operability (420–433):** RISK. Doctor, backup/restore, corruption drills, and tenant export are delivered, while the audit and v1-gate documentation still contain stale deferrals and no recovery-objective statement.

This matters because maintainers, reviewers, operators, and prospective adopters cannot distinguish an implemented invariant from an untested assumption by reading the audit register. The repository's own evidence rule requires implemented behavior plus a relevant test or frozen contract before a block becomes PASS. Scattered tests and stale documentation leave avoidable uncertainty in the exact areas where retry safety, authorization, tenant isolation, migration recovery, and operational recovery must be explainable.

## Proposed change

Close the five audit blocks with exhaustive test matrices and narrowly scoped decision documentation. The change should prefer proof of existing behavior over production changes and must not claim guarantees that the repository cannot verify.

### 1. L — Idempotency replay and lost-response evidence

- Add a consolidated, table-driven **operation × surface** replay matrix for supported idempotent operations across HTTP, MCP, CLI, and sync.
- For each applicable operation/surface pair, prove that repeating the same request identifier and payload returns the original stored outcome, reports replay semantics where that surface exposes them, and creates no duplicate domain event or receipt.
- Prove that reusing a request identifier with a different payload or principal fails with the frozen idempotency-conflict behavior.
- Add actual MCP and CLI replay cases; current tests that only assert a fresh request is not a replay are insufficient.
- Add lost-response/interrupted-reservation cases for approval, judgment, reconciliation, review, hold, and reopen behavior, following the existing purge precedent. Each test must prove deterministic recovery or a typed conflict without duplicate mutation.
- Keep sync's no-op second import in the consolidated evidence matrix even where its replay representation differs from request-ID surfaces.

No production behavior change is anticipated. If the matrix exposes a real inconsistency, that defect must be specified and corrected explicitly rather than weakening the matrix.

### 2. J — Authorization policy and override-absence evidence

- Add a consolidated, version-aware policy matrix spanning approval, judgment, reconciliation, and evidence-lifecycle policies. The matrix must freeze policy versions and representative role × action × effect outcomes, including dual approval and segregation-of-duties denials.
- Add negative conformance tests proving that supported request/command surfaces do not accept an override or break-glass field and that policy-denied operations cannot bypass authorization through adapters or store calls.
- Record the decision that **Drenyra Engram has no override feature**. Override audit means negative conformance: privileged or urgent callers remain subject to the same frozen policy, scope, assurance, and SoD rules.
- Preserve the non-authorization boundary: this work proves the engine's professional approval policy; it does not turn Engram into a business or payment authorization system.

The recommended and proposed default is negative-only closure. Adding an override would create a new security capability, audit model, policy version, and product risk and is outside this audit-evidence change.

### 3. Q — Generated cross-tenant and nonexistence-safe matrix

- Add a Go test-only operation catalog/helper that enumerates the complete API surface and drives cross-tenant conformance cases. It must cover reads, searches, mutations, lifecycle operations, rules, judgments, links, comparisons, period operations, exports, and other methods exposed by the canonical API.
- For every operation, create tenant A state and invoke the operation from tenant B under an exact mismatched scope.
- Assert the operation-specific safe outcome: not found, empty/zero result, or a frozen scope-denied code as appropriate. Responses must not disclose whether the foreign resource exists or leak tenant/company/RUC/period identifiers or foreign data.
- Assert that denied cross-tenant mutations produce no state change, event, receipt, idempotency completion, or externally visible side effect.
- Include an exhaustiveness guard so adding an API operation without a matrix case fails the test suite.

The matrix is a Go test-only generated helper, not a checked-in JSON artifact. Executable enumeration reduces drift and keeps the evidence coupled to the actual API surface without creating a second manually maintained registry.

### 4. Z — Operability evidence reconciliation

- Add or update an operability decision/evidence document that states the capabilities the repository can prove today: routine/full doctor behavior, WAL-safe snapshot/restore drills, corruption drills on marked copies, scope and backup-identity verification, and tenant export.
- Document recovery objectives qualitatively without inventing RTO or RPO numbers. The repository proves recovery mechanisms and drill behavior; deployment-specific numerical targets remain business/operator-owned and UNKNOWN until an owner records them.
- Reconcile the audit register, the v1 gate, and related architecture/evidence-lifecycle documents so backup/restore and corruption drills are no longer described as DEFERRED or undelivered.
- Preserve accurate boundaries: repository tests demonstrate executable recovery capabilities, not a production service-level objective, a completed external operational drill, or guaranteed recovery time/data loss for every deployment.

The proposed default is doc-only closure for Z. A consolidated operability matrix test is unnecessary unless specification work finds that existing tests cannot be cited unambiguously.

### 5. G — Direct upgrades, crash recovery, and provenance decision

- Add a generated/table-driven direct-upgrade matrix for every legacy schema version **v1 through v13**, opening each fixture through the normal `Open` path and proving direct convergence to schema v14.
- For every source version, verify representative additive data preservation and final structural invariants rather than checking only the version number.
- Add deterministic migration-failure/crash simulation proving that an interrupted migration transaction leaves the prior schema and data coherent, and that reopening can safely rerun the chain to v14 without partial artifacts or duplicate effects.
- Freeze the provenance decision: the ordered migration code, one-transaction-per-step rule, and final `schema_version` are the authoritative migration provenance record for this release. Do not add a production migration-history table during audit closure.
- Update the frozen migration/provenance documentation to explain what this record proves and does not prove: it proves the current schema generation and deterministic code path, but does not claim wall-clock execution history or operator identity.

The recommended and proposed default is **doc-only provenance closure**. A new schema-history table would itself require schema v15, migration and rollback semantics, timestamp/identity trust decisions, and another audit surface; that risk is disproportionate when the purpose of this change is to prove the existing v1–v14 chain.

## Non-goals

- No override, break-glass, super-admin bypass, or emergency authorization feature.
- No production migration-history table, schema v15, or per-deployment migration event ledger.
- No invented numerical RTO, RPO, SLA, SLO, or production recovery claim.
- No checked-in JSON cross-tenant matrix or second API registry.
- No redesign of idempotency reservations, authorization policies, tenant scoping, migration architecture, backup/restore, or corruption handling unless a new conformance test exposes an actual defect.
- No product UI, new public API operation, ERP/SUNAT integration, identity-provider work, or external security/compliance certification.
- No claim that passing repository tests proves accounting correctness, fiscal correctness, customer validation, or production readiness as a whole.
- No closure of audit blocks outside G, J, L, Q, and Z.

## Business rules and implications

- **Evidence threshold:** an audit block moves to PASS only when each required closure item is backed by executable tests or a frozen decision/contract. Prose may reconcile delivered evidence but must not substitute for missing behavior proof.
- **Scope first:** all tenant-sensitive operations use exact tenant/company/RUC/period boundaries where applicable and fail closed without foreign-resource enumeration.
- **No authorization bypass:** all principals, including administrative roles, remain subject to the versioned policy, assurance, role, scope, and SoD checks. No input spelling or adapter may create an implicit override.
- **Replay identity:** replay evidence must bind the same tenant, request identifier, operation, payload, and relevant principal. Changed inputs under the same key are conflicts, not successful retries.
- **No duplicate effects:** successful replay or interrupted-response recovery must not create duplicate events, receipts, mutations, exports, or sync imports.
- **Migration atomicity:** every migration step remains fail-closed and transactional, with `schema_version` changed only after the step succeeds. Unknown or malformed versions fail closed.
- **Recovery claims remain bounded:** tests may prove snapshot, restore, verification, and corruption-detection mechanics. Numerical recovery targets require an explicit deployment/business decision and observed operational evidence.
- **Frozen contracts:** if conformance work discovers a necessary behavior or public-contract change, it requires explicit versioning, migration/compatibility analysis, and approval rather than an incidental test workaround.
- **Runtime parity:** shared Go↔TypeScript semantics remain unchanged. Any discovered shared-contract correction must preserve golden parity and be treated as separately approved scope.
- **Money remains integer-only:** no test helper, response, or documentation may introduce floating-point monetary semantics; Go uses `int64` cents and TypeScript uses `BigInt` cents.

## Impact

| Area | Expected impact |
|---|---|
| `internal/server` and adapter tests | Consolidated HTTP/MCP replay evidence and the generated API cross-tenant/nonexistence-safe matrix. |
| `cmd/drenyra-engram` tests | CLI replay cases and evidence that no override flag/input path exists. |
| `internal/store` tests | Lost-response scenarios, no-duplicate-effect assertions, migration direct-upgrade fixtures, and migration failure/recovery proof. |
| `internal/authz` tests | Consolidated versioned policy matrix and negative override conformance. |
| `internal/sync` tests | Sync's idempotent second-run behavior represented in the surface matrix. |
| Test-only helpers/fixtures | API operation catalog, v1–v13 schema fixture builders, and deterministic failure injection where existing seams permit it. |
| `contracts/provenance.md` and/or a decision record | Frozen override-absence and migration-provenance semantics. |
| Due-diligence and v1-gate documentation | Evidence citations and corrected classifications for G, J, L, Q, and Z; stale drill deferrals removed. |
| Operability/evidence architecture docs | Qualitative recovery-objective statement and accurate delivered-versus-unproven boundaries. |
| Production Go/TS code | No change expected. Any required correction discovered by tests is exceptional and must be separately justified in design/tasks. |

## Acceptance criteria

### L — Idempotency becomes PASS

- A single inspectable matrix enumerates each supported idempotent operation against HTTP, MCP, CLI, and sync where applicable.
- Replaying identical inputs proves original-result replay/no-op semantics and no duplicate state, event, receipt, or import.
- Reusing a key with changed bound inputs proves the frozen conflict behavior.
- Lost-response/interrupted-reservation tests cover approval, judgment, reconciliation, review, hold, reopen, and existing purge behavior with deterministic, typed outcomes.
- The audit register cites the matrix and lost-response tests and removes the complete-surface RISK qualifier.

### J — Authorization becomes PASS

- One versioned matrix covers all four authorization policies and freezes representative role/action/effect, dual-approval, and SoD outcomes.
- Negative tests prove that no supported command/request/adapter has an override path and that denied operations cannot bypass policy enforcement.
- A decision record states that override absence is intentional and that override audit is negative conformance, not an unimplemented feature.
- The audit register cites these artifacts and removes the SoD/override RISK qualifier without claiming production identity readiness from block I.

### Q — Multi-tenant security becomes PASS

- A Go test-only generated matrix enumerates every canonical API operation, with an exhaustiveness guard for newly added operations.
- Every operation has a cross-tenant case proving the correct nonexistence-safe result and zero foreign-data leakage.
- Every denied mutation proves no side effect, and every read result is not-found/empty/zero or frozen scope-denied according to its contract.
- The audit register cites the generated matrix and removes the exhaustive-coverage RISK qualifier.

### Z — Operability becomes PASS for repository-demonstrated capabilities

- Documentation cites the existing doctor, backup/restore, corruption-drill, and tenant-export implementations and tests as delivered.
- Stale DEFERRED/not-demonstrated statements are reconciled across the audit register, v1 gate, and related architecture documents.
- Recovery objectives are documented without fabricated values: delivered mechanisms are stated; numerical RTO/RPO targets remain explicitly owner/deployment-defined and may be added later.
- The resulting PASS is bounded to repository-verifiable operability capabilities and does not claim measured production objectives.

### G — Migrations becomes PASS

- A direct-upgrade matrix covers every v1…v13 source schema through the normal open path to v14.
- Each row proves final schema, representative additive preservation, and required invariants.
- Failure/crash-recovery evidence proves atomic rollback to a coherent prior version and successful safe reopen/retry to v14.
- A frozen decision defines the migration chain plus `schema_version` as the provenance record for this release, with its limitations explicit.
- The audit register cites the matrix, recovery proof, and provenance decision and removes the operations RISK qualifier.

### Whole-change success

- `go test ./...` and `npm test` remain green under the repository's strict-TDD workflow; TypeScript tests are required even though no shared runtime change is expected.
- No audit status is upgraded on prose alone where executable evidence is required.
- No new production schema, override path, public API, or numerical recovery promise is introduced.
- Every updated audit claim links to stable tests, contracts, or decision records that a reviewer can inspect independently.

## Open questions and recommended defaults

These four decisions are resolved by recommendation for planning, but remain visible for owner correction before specification is frozen:

1. **J — What counts as override audit?** Adopt a negative conformance test plus a decision record that no override exists. Do not build an override feature. This matches deliberate code behavior and avoids creating a privileged bypass during evidence closure.
2. **G — What is migration provenance?** Use doc-only closure: the ordered, transactional migration chain and `schema_version` are the authoritative provenance record for this release. Do not add a production table or schema v15.
3. **Z — What RTO/RPO should be recorded?** State delivered recovery capabilities and explicitly leave numerical targets unset. Owner-defined targets can be added later when deployment and business requirements exist.
4. **Q — What form should the generated matrix take?** Use a Go test-only helper/catalog tied to the canonical API surface. Do not maintain a parallel JSON artifact.

Proposal assumption: these defaults are accepted unless the owner explicitly changes them before specification. A change to any default may materially expand scope and requires proposal/spec revision.

## Alternatives considered

### Implement an authorization override

Rejected. It would require new policy semantics, roles, audit events, adapter inputs, threat analysis, and tests. More importantly, it would weaken the current fail-closed model merely to satisfy an audit phrase that can be answered more strongly through negative conformance.

### Add a migration-history table

Not selected. A durable history could record timestamps or actors, but it would force a production schema change while the audit is evaluating migration safety. Without a trusted operator identity and clock policy, such rows could imply provenance they do not actually prove. The existing ordered code plus transactional `schema_version` is narrower but honest.

### Publish numerical RTO/RPO targets now

Rejected. Repository tests establish mechanics, not business tolerance or observed production performance. Invented targets would convert a documentation gap into a misleading product claim.

### Check in a JSON matrix

Rejected. A static artifact is easy to drift from `internal/server/api.go` and would still need executable tests. A test-only enumerator can fail when operations are unclassified and is therefore stronger evidence.

### Preserve scattered tests and update only the audit prose

Rejected. The current RISK classifications exist precisely because scattered examples do not prove complete coverage. Documentation-only closure is valid for stale Z claims and explicit decisions, not for L, J, Q, or G's missing executable matrices.

## Risks

- **Matrix incompleteness:** a manually maintained catalog may omit operations and create false confidence. Mitigation: add an exhaustiveness guard tied as closely as practical to the canonical API surface and require explicit classification for every operation.
- **Large test surface:** exhaustive HTTP/MCP/CLI, tenant, and migration matrices may be expensive and brittle. Mitigation: centralize setup in test-only helpers, keep cases deterministic, and split delivery into focused review slices.
- **Behavioral defects discovered:** tests may reveal that an existing path leaks existence, duplicates effects, or handles interrupted reservations inconsistently. Mitigation: fail the criterion, document the discovered defect, and require a narrowly scoped production correction rather than weakening assertions.
- **False PASS wording:** updating the register could overstate production readiness, identity assurance, or deployment recovery. Mitigation: keep each PASS bounded to the block's repository-verifiable evidence and retain adjacent UNKNOWN/RISK classifications.
- **Migration fixture fidelity:** synthetic old schemas may not accurately represent historical databases. Mitigation: derive fixtures from frozen migration-era schemas/tests, preserve representative rows, and verify structural invariants rather than version numbers alone.
- **Failure-injection realism:** process-crash simulation can accidentally test only ordinary SQL errors. Mitigation: design a deterministic transaction-interruption seam and state precisely what recovery property it proves.
- **Negative override proof limits:** reflection or input-shape checks alone cannot prove absence of every bypass. Mitigation: combine structural request/command checks with behavioral denial tests at policy, service/adapter, and store boundaries.
- **Documentation drift:** audit, v1 gate, and architecture docs may diverge again. Mitigation: cite exact executable evidence and update all known stale statements in the same documentation slice.

## Rollback

- Test-only matrices and helpers can be reverted without changing persisted customer data or public contracts.
- Documentation classification updates can be reverted independently if evidence later proves incomplete; reverting must restore the prior RISK status rather than leave an unsupported PASS.
- No schema change is planned, so migration-provenance rollback is a documentation/decision reversal only.
- No override path is introduced; rollback cannot weaken authorization behavior.
- If a conformance test drives a production correction, that correction requires its own compatibility and rollback plan before inclusion. Schema or frozen-contract changes are not implicitly authorized by this proposal.
- Delivery slices should remain independently revertible while preserving the audit register at the most conservative status supported by the remaining evidence.

## Delivery shape and review workload forecast

Chained PRs are expected. The combined work spans exhaustive API-operation tenant cases, HTTP/MCP/CLI replay cases, interrupted-reservation fixtures, four authorization policies, thirteen migration source versions, crash/recovery proof, and coordinated documentation updates.

- **Changed-line forecast:** likely **greater than 400 lines**, primarily tests and fixtures rather than production code. Q and G alone can plausibly exceed the preferred review budget once complete operation/version matrices and setup helpers are included.
- **Recommended slices:**
  1. J authorization matrix, override-negative tests, and override-absence decision;
  2. L replay/lost-response matrix;
  3. Q generated cross-tenant/nonexistence-safe matrix;
  4. G direct-upgrade/crash-recovery matrix and provenance decision;
  5. Z recovery-objective wording plus final audit/v1-gate evidence reconciliation.
- Each slice should carry its own focused tests and keep the audit classification conservative until all acceptance evidence for that block is present.
- No production-code slice is planned. If tests uncover a defect, isolate the correction from evidence-only work where practical so reviewers can distinguish changed behavior from newly demonstrated behavior.

## Acceptance outlook

This proposal succeeds when the repository can replace five broad RISK/UNKNOWN-style evidence gaps with inspectable, repeatable proof: retries are safe across surfaces and lost responses; authorization has no bypass and all policies are versioned and matrix-tested; every API operation is cross-tenant safe and non-enumerating; delivered recovery capabilities are accurately documented without fabricated objectives; and every historical schema can recoverably upgrade to v14 with honest provenance semantics.
