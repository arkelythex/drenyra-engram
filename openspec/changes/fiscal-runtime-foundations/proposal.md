# Proposal — Fiscal Runtime Foundations

> Phase: propose · Artifact: proposal · Status: draft
> Strict TDD: not active in this phase (planning only).
> Dependent consumer: `fiscal-onboarding-journey` remains preserved and blocked until this foundation is implemented and verified.

## Intent

Add the minimum production/runtime foundations needed for Drenyra Engram to demonstrate a truthful public fiscal onboarding journey without weakening its guarantees. The change establishes three capabilities:

1. checksum-valid Peruvian RUC enforcement at protected company-scope boundaries;
2. complete, structural, fail-closed binding of the ten-element fiscal operation scope used by protected fiscal work; and
3. at least one public, authenticated professional approval path that can prove exact-envelope review, role policy, separation of duties, and required material-review acknowledgements using fictional local-development data.

The outcome is not a broader product redesign. It is a bounded runtime prerequisite that closes concrete gaps found while validating `fiscal-onboarding-journey`. That onboarding change remains the documentation-and-proof consumer of these capabilities; it is neither replaced nor allowed to emulate, preflight around, or soften them.

## Problem statement

Design validation for `fiscal-onboarding-journey` found that its strongest public-surface claims cannot currently be demonstrated honestly:

- `internal/core.IsValidRUC` validates only eleven digits. General CLI scope construction, HTTP query-scope parsing, evidence-export scope checks, reconstructibility adapters, and `AssertValidScope` therefore accept checksum-invalid RUCs. SUNAT modulo-11 validation exists only inside the comprobante adapter and is not the canonical protected-scope boundary.
- The runtime `core.Scope` structurally binds only the existing company tuple (`kind`, `organizationId`, `companyId`, `ruc`, and optional `period`). The onboarding design can declare ten scope elements in a fixture, but several are narrative mappings rather than one validated, persisted, hash-bound runtime scope.
- The core approval command already carries `ReviewChecks`, and the store correctly enforces exact-envelope review, authorization policy, separation of duties, and material/critical review checks inside one transaction. Public adapters do not expose the full command: the CLI approval command and authenticated HTTP approval body omit review acknowledgements, while stdio MCP deliberately has no authenticated professional binding and fails closed.

The predecessor design proposed test-only preflight and an internal close fixture as substitutes for these gaps. Those are useful evidence, but they cannot support the intended product claim that a fictional professional completes the declared-material review through a public production surface. Continuing that approach would make the onboarding executable while leaving its central public guarantee untrue.

## Proposed change

### 1. Canonical checksum-valid RUC boundary

Promote SUNAT modulo-11 validation into the canonical reusable RUC validator and apply it consistently before protected company-scoped work.

Required behavior:

- A company RUC is exactly eleven ASCII digits and passes the SUNAT modulo-11 check digit algorithm.
- Shape and checksum validation use one canonical implementation, with Go↔TypeScript parity vectors. The comprobante adapter consumes that implementation instead of retaining a private checksum oracle.
- Company-scope construction and validation fail before reads that could disclose scoped data and before any mutation, idempotency reservation, object write, receipt, or audit event.
- CLI, HTTP, MCP scope decoding, direct service inputs, store-facing protected commands, authentication/local-dev membership seeding, evidence export, close/reopen, review, and verification entry points cannot bypass the canonical validator when they accept or reconstruct a company RUC.
- Invalid-RUC errors remain typed, non-enumerating, and stable. Specification will freeze whether the existing `INVALID_RUC` code is retained with stronger semantics or a versioned code is required; implementation must not silently change frozen error envelopes.
- Existing records are never silently rewritten to a different RUC. Checksum-invalid legacy rows are reported by migration/diagnostic evidence and handled through the compatibility policy below.

This is jurisdictional identifier validation only. It does not query SUNAT, prove taxpayer existence, or establish that fictional fixture values are legally allocated.

### 2. Complete structural fiscal operation-scope binding

Introduce an additive, versioned fiscal operation-scope binding with exactly these ten canonical elements:

```text
tenant, organization, company, fiscalPeriod, ledgerBook, operationType,
sourceSnapshot, policyVersion, actor, authorityLevel
```

The binding is separate from caller authority and must satisfy these rules:

- `company` is the checksum-valid eleven-digit Peruvian RUC.
- `fiscalPeriod` is canonical `YYYYMM` with a real month.
- `sourceSnapshot` is lowercase hexadecimal SHA-256 of the frozen source manifest.
- `authorityLevel` is one of `ASK`, `ANALYZE`, `PREPARE`, or `EXECUTE`.
- Every other element is present, non-empty, canonically encoded, and validated against its closed or versioned domain where one exists.
- Canonical serialization and a lowercase SHA-256 scope hash are deterministic and byte-identical in Go and TypeScript.
- Missing, malformed, mismatched, changed, or partially reconstructed bindings fail closed. No adapter, service, store, default scope, session, or migration may guess a missing element.
- Reloaded protected work recomputes the hash from the persisted ten elements and compares it with the stored binding before proceeding.
- Each protected command carries or resolves the exact binding it needs. The store remains the authoritative final guard; adapter validation is defense in depth, not the only enforcement point.
- The authenticated principal must still match the tenant/company membership policy where identity is required. The binding's `actor` must match the operation's trusted provenance source or verified professional principal according to the operation type; a caller-supplied actor never creates authority.
- `authorityLevel` is a scope classification and must never grant a role, bypass professional approval, authorize a business operation, post a ledger entry, file with SUNAT, or elevate assurance.
- Scope changes invalidate prior operation authorization and reviewed-envelope state. A caller must obtain/review a fresh binding and current envelope rather than reusing a stale hash.

The existing frozen company/RUC/period scope semantics remain valid for legacy data and ordinary compatibility reads. The new binding is a versioned production capability for protected fiscal operations and v1-bound records; it must not mutate the meaning of an existing frozen scope or receipt contract in place.

For the first slice, “protected fiscal operations” means only the operations needed by the preserved onboarding journey and the invariants they transitively depend on:

- save/supersede of company-scoped fiscal memory;
- exact-scope context, review queue/detail, and timeline reads used before a protected decision;
- evidence object storage/retrieval and evidence/rule linking;
- professional approval of a pending fiscal memory;
- monthly close creation, professional close approval, and post-close mutation denial; and
- read-only reconstruction and offline verification of those acts.

Unrelated institutional knowledge, retention/purge expansion, sync redesign, cloud operation, and new fiscal workflows are not part of this slice. If a shared canonical boundary must be changed to prevent bypass, that boundary change is in scope even when more than one listed operation calls it.

### 3. Public authenticated professional approval with material-review acknowledgements

Expose the already-enforced review checks through a bounded public professional path, with the CLI as the required canonical local onboarding surface.

The path must:

- derive the professional principal only from the authenticated session; payloads and flags cannot supply subject, roles, membership, assurance, or actor authority;
- accept the exact current reviewed-envelope hash, a non-empty reason, an idempotency key generated or supplied under the existing contract, and explicit acknowledgements that evidence and applicable rules were inspected;
- pass those acknowledgements into `core.ApproveMemoryCommand.ReviewChecks` without adapter defaults that turn omitted values into successful declarations;
- continue to enforce role policy, assurance, tenant/company membership, proposer/reviewer separation of duties, required material/critical checks, exact-envelope freshness, and one atomic state transition inside the store transaction;
- fail closed with the existing typed outcomes for missing authentication, stale envelope, SoD violation, insufficient role/assurance, and missing review checks;
- record immutable approval/audit evidence and existing signed receipts without implying accounting correctness or business authorization; and
- operate in the onboarding proof only with explicit `DRENYRA_ENV=local_dev`, isolated paths, a short-lived fictional professional, and no committed token or credential.

The authenticated HTTP approval route must accept an equivalent strict `reviewChecks` object if it remains a supported public approval adapter for the same command. Unknown fields and caller-declared authority remain rejected. The stdio MCP server remains non-authenticated and must continue to fail approval with `AUTHENTICATION_REQUIRED`; adding identity fields to MCP tool arguments is explicitly forbidden. Full authenticated MCP transport parity is not required for this foundation.

## Public contract, API, and CLI boundaries

These changes must be specified and reviewed separately even if delivered in one dependency chain.

### Versioned public contract changes

- Add a new versioned fiscal operation-scope binding contract and canonical hash definition. Do not edit frozen scope semantics as though they had always covered ten elements.
- Add or correct the versioned approval command documentation so `reviewChecks` is an explicit reviewer acknowledgement field for material/critical approvals while principal authority remains a separate verified argument.
- Freeze validation rules, canonical encoding, mismatch behavior, typed errors, compatibility markers, and Go↔TypeScript golden vectors.
- Preserve existing receipt payload versions. The design must bind the new scope through the reviewed/canonical envelope and immutable scope-binding evidence without changing signed receipt formats. If this cannot be proven, design must stop and request a separate receipt-contract proposal rather than expanding this change silently.

### Shared runtime/API changes

- Add the canonical checksum validator and remove duplicated/private checksum semantics.
- Add the versioned fiscal scope-binding value, validator, canonical serializer, and hash comparison at the shared core/service/store boundaries.
- Add additive persistence for the complete binding and its version/hash, plus immutable audit linkage needed to reload and verify it.
- Require v1-bound protected operations to provide and match the full binding; a legacy adapter cannot mutate a v1-bound object by presenting only the old tuple.
- Carry public review acknowledgements into the existing atomic approval command and receipt/audit path without adding authority fields.
- Extend read-only verification to report whether the applicable scope binding is complete and hash-consistent while retaining the exact conclusion `Accounting correctness: NOT ASSERTED`.

### CLI changes required for onboarding

- Provide one explicit way to supply the complete versioned scope binding for the protected onboarding commands. Design will choose a single canonical representation, preferring a strict JSON binding file over ten repeated flags if that reduces drift and secret-free copy/paste errors.
- Extend authenticated `approve` with explicit material-review acknowledgements. Omission must remain distinguishable from affirmative acknowledgement and must fail for material/critical memory.
- Keep `auth seed-local-dev` explicitly local-development-only and checksum-valid; it may create fictional membership/session data but cannot mint arbitrary production authority.
- Keep CLI output machine-readable, redact tokens, and expose stable codes suitable for the dependent onboarding proof.

### HTTP and MCP compatibility

- Update company-scope parsing to use canonical checksum validation.
- Update the authenticated HTTP approval body with strict review acknowledgements and no authority fields.
- Require full binding for any HTTP/MCP protected operation that can create or mutate v1-bound fiscal state, or fail that operation closed when the adapter cannot provide it.
- Preserve stdio MCP's non-authenticated professional boundary. Agent tools may prepare and record within their allowed scope; they do not approve or close.

## Scope

### In scope

- Canonical SUNAT modulo-11 RUC validation at all protected company-scope boundaries reached by the target journey.
- A versioned, persisted, canonically hashed ten-element fiscal operation-scope binding.
- Store-level fail-closed binding checks plus adapter/service defense in depth.
- Additive schema migration and explicit legacy/v1 compatibility states.
- Go↔TypeScript type, canonicalization, validation, and golden parity needed by the new domain behavior.
- Public CLI and authenticated HTTP approval acknowledgement support over the existing principal/policy/store transaction.
- Audit, receipt-continuity, offline-verification, and no-partial-state evidence for the new binding.
- Fictional local-development principals, scopes, evidence, and fixtures needed to verify the foundation safely.
- Documentation and migration guidance for the versioned contracts and public surfaces.
- A dependency handoff that lets `fiscal-onboarding-journey` replace its test-only workarounds with these verified production capabilities.

### Non-goals

- Replacing, archiving, or re-scoping `fiscal-onboarding-journey`.
- Implementing the onboarding guide, demo runner, or complete monthly-close narrative in this change.
- Any float-based money representation; monetary values remain whole `int64` cents in Go and BigInt/integer cents in TypeScript.
- A receipt-format rewrite, key rotation redesign, mutable audit history, or weakening of offline verification.
- External identity providers, MFA, browser login, membership provisioning, authenticated stdio MCP, or caller-declared authority.
- Business authorization, journal posting, ledger mutation, ERP/payment authority, declaration submission, SUNAT filing, credential handling, or accounting-correctness claims.
- Cloud, remote object storage, hosted synchronization, multi-node coordination, UI, TUI, dashboard, or review workspace redesign.
- Supporting arbitrary jurisdictions or taxpayer identifier algorithms beyond the Peruvian RUC boundary required here.
- Automatically repairing checksum-invalid legacy data or inventing missing scope elements.
- Broad parity work for unrelated protected workflows unless a shared boundary would otherwise permit bypass.

## Business rules and preserved invariants

- **Money remains integer cents:** schema, APIs, CLI, fixtures, receipts, and parity vectors use integers only; float-form money is rejected atomically.
- **Tenant isolation remains structural:** exact scope filtering occurs before ranking or mutation. Cross-tenant and cross-RUC probes disclose no foreign identifiers or content.
- **RUC validity is necessary but not sufficient:** a valid checksum does not establish taxpayer existence, ownership, membership, or authorization.
- **Memory never authorizes:** observations, scope bindings, receipts, and verification report what was recorded and reviewed. They never authorize a ledger, declaration, filing, payment, or business act.
- **Professional authority is derived:** authenticated principal, active membership, company scope, role, assurance, materiality, and SoD remain policy inputs resolved independently of caller payloads.
- **Review is exact:** acknowledgements apply only to the exact reviewed envelope and binding. Any envelope or binding change requires fresh detail and a new decision attempt.
- **Close remains human-controlled:** an agent may prepare a close memory, but only an authenticated controller can approve it; post-close protected writes fail with `PERIOD_CLOSED` and no partial state.
- **History and audit are immutable:** scope bindings and approval acts are appended/versioned, never edited to retrofit a claim. Every mutation records tenant/RUC, period, timestamp, trusted actor/provenance, and reason where required.
- **Receipts remain verifiable:** existing Ed25519 chains and frozen payload versions remain valid. New binding evidence cannot invalidate historical receipts or fabricate coverage they never had.
- **Frozen contracts change only by version:** additive fields, error semantics, and canonical bytes require explicit versioning, migration notes, and parity evidence.
- **Fictional data only:** local-development fixtures contain no real taxpayer, customer, invoice, token, credential, or production fiscal data and are never queried against SUNAT.

## Compatibility and migration

### Data classification

After migration, persisted company-scoped fiscal records must be distinguishable as:

- **legacy scope:** created under the existing company/RUC/period contract and lacking a complete ten-element binding; or
- **v1 fiscal binding:** carrying all ten canonical elements, the binding version, and a verified hash.

The migration is additive and must not fabricate values for legacy rows.

### Legacy behavior

- Existing valid historical rows and receipts remain readable and verifiable under their original contract, with an explicit legacy/unbound scope result rather than a false v1 pass.
- Checksum-invalid legacy rows are never silently normalized. Diagnostics enumerate counts and safe opaque identifiers without cross-tenant disclosure; remediation requires an explicit, separately audited operator decision.
- Legacy records cannot be presented as evidence that the ten-element v1 binding was enforced.
- A legacy adapter or partial scope cannot mutate, approve, close, reopen, or link a v1-bound protected object.
- Specification/design must define whether protected mutation of legacy fiscal rows is blocked pending explicit rebinding or remains available only through a clearly labelled compatibility mode. The default rollout must fail closed and must not weaken new v1 guarantees.

### Contract and client compatibility

- Frozen scope and approval versions stay documented and testable.
- New clients opt into the versioned binding explicitly; servers reject unknown versions and incomplete bindings.
- Public error envelopes and receipt versions remain stable unless a separately approved version bump is required.
- Go and TypeScript mirrors advance together for canonical domain behavior; there is no period where the two runtimes disagree on checksum or scope hash bytes.

## Rollout and required evidence

Rollout is gated, not inferred from compilation success.

1. **Inventory:** report existing checksum-invalid RUC rows, legacy-scope rows, affected public commands/routes/tools, and frozen contracts. Do not mutate data during inventory.
2. **Contract freeze:** approve versioned scope-binding and approval-acknowledgement specs, canonical vectors, error behavior, and migration states before production code changes.
3. **Shadow/read-only validation:** compute checksum and v1 binding diagnostics without changing existing records or authorization outcomes.
4. **Write gate:** enable creation of v1-bound fiscal data only after cross-surface boundary tests, store bypass tests, parity tests, and rollback readiness pass.
5. **Professional path gate:** publish CLI/HTTP acknowledgement support only after material/critical positive and negative controls prove exact envelope, authentication, role, SoD, acknowledgements, idempotency, receipts, and no partial mutation.
6. **Consumer handoff:** resume `fiscal-onboarding-journey` only after this change's verification report is passing. The onboarding implementation must consume the production validator, persisted binding, and public professional path; it must delete or narrow claims based on test-only substitutes.

Required verification evidence includes:

- SUNAT modulo-11 positive/negative/boundary vectors with Go↔TypeScript parity.
- A boundary matrix proving invalid checksums fail before data access or mutation through CLI, HTTP, MCP decoding, direct services, and store entry points applicable to the slice.
- Canonical scope vectors proving every one of the ten elements participates in serialization/hash and that changing, omitting, reordering, or malformed-encoding any element fails closed.
- Restart/reload tests proving persisted elements recompute to the same hash and tampering/mismatch blocks protected work.
- Cross-tenant, cross-organization, cross-RUC, cross-period, and source-snapshot mismatch controls with no foreign disclosure and no partial state.
- Public material/critical approval tests for successful explicit acknowledgements and failures for omission/false values, stale envelope, wrong role, low assurance, inactive membership, self-approval, wrong scope, and idempotency conflict.
- Human-controlled close and post-close denial tests using integer cents.
- Receipt/audit continuity and offline-verification tests ending with `Accounting correctness: NOT ASSERTED`.
- Migration tests proving legacy rows are not guessed, silently rewritten, or falsely reported as v1-bound.
- A changed-path and frozen-contract audit showing every public change is versioned and documented.

## Affected areas

| Area | Expected impact |
| --- | --- |
| `contracts/scope.md` and approval contract documentation | Add versioned fiscal-binding and review-acknowledgement semantics without rewriting frozen history. |
| `internal/core` and TypeScript core mirror | Canonical checksum validator, fiscal binding value/validation/hash, envelope linkage, and parity types. |
| `testdata/golden` and parity suites | Shared RUC, canonical binding, hash, and approval-check vectors. |
| `internal/store` and migrations | Additive binding persistence/version/hash, authoritative protected-operation guards, audit linkage, and legacy classification. |
| `internal/auth` / `internal/authz` | Preserve principal construction and policy; bind trusted actor/membership to the applicable operation without payload authority. |
| `internal/server` shared services | Require and validate complete binding for applicable protected operations; carry review acknowledgements to the existing atomic store command. |
| `cmd/drenyra-engram` | Canonical binding input for the target protected commands, checksum-valid local-dev seed/scope parsing, and explicit approval-review acknowledgement flags/input. |
| HTTP server | Checksum-valid scope parsing, strict versioned binding input where needed, and strict authenticated approval `reviewChecks`. |
| MCP server | Checksum-valid scope decoding and v1-bound protection; stdio approval remains unauthenticated and fail-closed. |
| Receipts and offline verification | No payload-format change planned; add verification of applicable binding completeness/hash through existing envelope/audit linkage. |
| Docs and migration guidance | Explain versioning, legacy limitations, fictional local-dev use, non-authorization, rollout, rollback, and dependent onboarding handoff. |
| `fiscal-onboarding-journey` | No implementation in this change. Its artifacts remain preserved as the dependent consumer and must be updated later to use this foundation. |

## Risks and tradeoffs

- **Scope-model expansion:** ten structural elements affect identity, hashing, persistence, adapters, and verification. Mitigation: additive versioned binding, one canonical serializer, store authority, and strict changed-path/traceability planning.
- **Frozen-contract breakage:** changing existing scope, approval, envelope, or receipt bytes in place could invalidate compatibility. Mitigation: versioned addenda, golden vectors, legacy classification, and a hard stop if receipt coverage requires a separate proposal.
- **Legacy invalid RUCs:** stronger validation may strand historical fixture or user data. Mitigation: read-only inventory, no auto-correction, explicit legacy reporting, and separately audited remediation.
- **False authority from `actor` or `authorityLevel`:** callers may interpret scope metadata as permission. Mitigation: principal-derived policy remains authoritative; payload actor cannot authorize; contracts repeat the non-authorization boundary.
- **Adapter bypass:** validating only CLI would leave HTTP, MCP, or direct Go paths inconsistent. Mitigation: store-level enforcement plus a boundary matrix; unsupported adapters fail closed for v1-protected mutation.
- **Rubber-stamp acknowledgements:** boolean flags prove declaration, not actual human diligence. Mitigation: label them acknowledgements, bind them to the exact envelope and immutable act, require authenticated professional identity, and make no accounting-correctness claim.
- **Onboarding coupling:** implementing a broad platform abstraction could overbuild beyond the consumer. Mitigation: protected-operation allowlist and explicit non-goals; additional workflows require separate proposals.
- **Review workload:** the runtime, schema, contracts, parity, adapters, and migration evidence will exceed the 400-line review budget. Under `delivery_strategy=ask-on-risk`, task planning must propose independently reviewable dependency slices and pause before apply for the delivery decision; this proposal grants no size exception.
- **Rollback after v1 writes:** an older binary cannot safely understand v1-bound data. Mitigation: capability/version gate, additive schema, pre-write rollback checkpoint, and downgrade refusal/read-only mode once v1 records exist.

## Rollback

- Before the v1 write gate is enabled, roll back code and additive migration artifacts while leaving diagnostic output and legacy data unchanged.
- After v1-bound data exists, rollback means disabling new v1 mutations and returning to a compatible read-only or dual-read release that understands the additive schema. Do not run an older binary that interprets v1 rows as fully legacy or drops binding evidence.
- Public CLI/HTTP acknowledgement exposure can be disabled only by failing material/critical approval closed; rollback must never default omitted checks to true or bypass the store policy.
- Binding and audit rows already created remain immutable. They are not deleted or rewritten during rollback.
- Existing receipts, keys, integer-cent data, closures, and legacy records remain intact. If a receipt-contract change becomes necessary, stop this change and open a separate approved proposal rather than making rollback depend on receipt rewriting.
- `fiscal-onboarding-journey` remains blocked/preserved until a verified compatible foundation is available; rollback must not revive its weaker test-only claims as production guarantees.

## Success criteria

This proposal is successful when later specification, design, implementation, and verification can prove all of the following:

1. Every applicable protected company-scope boundary rejects malformed or checksum-invalid RUCs before scoped data access or mutation, with no receipt, audit event, idempotency reservation, object write, or partial state.
2. The ten canonical fiscal scope elements are explicit, validated, persisted, canonically serialized, and hash-bound; changing or omitting any one element invalidates protected work.
3. Go and TypeScript produce byte-identical RUC decisions, canonical binding bytes, and scope hashes from shared golden vectors.
4. Store-level guards prevent partial/legacy adapters and direct callers from mutating a v1-bound protected object without the exact complete binding.
5. Cross-tenant, cross-organization, cross-RUC, cross-period, and changed-snapshot attempts fail closed and disclose no foreign identifiers or content.
6. A fictional local-development professional can use the public CLI to inspect and approve a declared-material pending memory only with the exact current envelope, an authenticated eligible principal, satisfied role/assurance policy, proposer/reviewer SoD, and both explicit review acknowledgements.
7. The authenticated HTTP approval route, if retained for the command, carries equivalent strict acknowledgements; stdio MCP still cannot supply identity or approve.
8. Omitted or false material-review acknowledgements return `REVIEW_CHECKS_REQUIRED`; stale envelopes, self-approval, scope mismatch, and insufficient authority retain their typed fail-closed outcomes and leave state unchanged.
9. Integer-cent money, tenant isolation, immutable history, audit provenance, Ed25519 receipt chains, human-controlled close, post-close mutation denial, and the non-authorization boundary remain passing invariants.
10. Offline verification reports binding completeness/hash consistency for applicable v1 acts without changing frozen receipt payloads and still ends with `Accounting correctness: NOT ASSERTED`.
11. Migration evidence distinguishes legacy from v1-bound records, never invents missing elements, never silently rewrites invalid RUCs, and defines a safe rollback/downgrade boundary.
12. `fiscal-onboarding-journey` remains a preserved dependent consumer and can subsequently replace its checksum preflight, narrative scope mapping, and internal-only material-review proof with these verified production/public capabilities rather than weakening its claims.
