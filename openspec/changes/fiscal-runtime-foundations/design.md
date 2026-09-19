# Design — Fiscal Runtime Foundations

> Phase: design · Artifact store: OpenSpec · Change: `fiscal-runtime-foundations`
> Dependent consumer: `fiscal-onboarding-journey` remains preserved and blocked until this foundation is implemented and verified.
> Delivery policy: `ask-on-risk`; implementation must be split into dependency-ordered review slices at task planning because the aggregate change exceeds the review budget.

## 1. Objective and hard stop

Add an additive fiscal runtime beside the frozen legacy scope and receipt contracts. It strengthens RUC validation, introduces a persisted ten-element fiscal operation binding, enforces it at adapter/service/store boundaries, exposes review acknowledgements on authenticated CLI and HTTP approval, and extends read-only verification through immutable binding/audit evidence.

Existing receipt payload versions, canonical payload bytes, signed-envelope bytes, signatures, and historical golden receipts must not change. Implementation must stop and open a separate proposal if any criterion requires adding, removing, renaming, reordering, or reinterpreting a receipt payload field; changing receipt canonicalization; rewriting a receipt/golden; or pretending a historical receipt carried v1 binding evidence.

The permitted continuity mechanism is an unchanged receipt carrying H1/H2, where the v1-only H2 transitively commits to immutable fiscal act evidence through a versioned memory-envelope contribution. A legacy memory has no such contribution, preserving its content, identity, envelope, and receipt bytes.

## Architectural decisions

### Separate binding value; frozen legacy scope stays frozen

`core.Scope` and `MemoryScope` remain the legacy company tuple. Add a distinct fiscal operation binding with transport version `v1` and exactly the ten elements required by the specification. Transport version is not an eleventh scope element.

The binding order is tenant, organization, company, fiscal period, ledger book, operation type, source snapshot, policy version, actor, and authority level. The public JSON spellings remain those frozen by the specification.

Trusted comparisons are explicit and never fill a missing field:

| Binding element | Trusted comparison |
| --- | --- |
| tenant | Legacy organization identifier; for professional acts, verified principal tenant |
| organization | Legacy company identifier; for professional acts, a verified principal company scope |
| company | Legacy RUC and the registered company RUC for the exact tenant/organization |
| fiscal period | Legacy period; v1 always requires a real year-month |
| ledger book | Exact trusted ledger/book identifier resolved from the registered fiscal ledger context; non-empty, independently validated, and never defaulted from the command |
| operation type | Exact closed action token expected by the invoked boundary |
| source snapshot | Exact lowercase SHA-256 of the frozen source manifest |
| policy version | Exact fiscal-policy version, separate from approval authorization-policy version |
| actor | Trusted provenance actor, verified professional subject, or server-stamped read adapter actor |
| authority level | Exact operation classification; never an authorization input |

The closed first-slice operation map is:

| Classification | Operation tokens |
| --- | --- |
| `PREPARE` | `memory.save`, `memory.supersede`, `evidence.store`, `evidence.link`, `rule.link`, `close.create` |
| `ASK` | `context.read`, `review.queue`, `review.detail`, `timeline.read`, `evidence.get` |
| `ANALYZE` | `reconstruct.read`, `verify.read` |
| `EXECUTE` | `memory.approve`, `close.approve`, `period.reopen`, `period.write-check` |

`ledgerBook` identifies the trusted accounting ledger or statutory book against which the act is scoped (for example, a registered purchases or sales book identifier). It is a domain identity axis, not a verb or operation classification: it must be resolved from trusted ledger/book registration or persisted subject context and compared independently from `operationType`. `operationType` names the protected action being attempted. Neither value may be copied from the other, and changing either one independently changes the canonical binding hash and invalidates prior review state.

This map validates intent; it grants nothing. Authentication, active membership, role, assurance, separation of duties, close policy, and all existing authorization rules remain independent and authoritative.

### Canonical versioned RUC validation

Add one pure validator in Go and one semantic mirror in TypeScript. Both implement the exact ASCII-shape and SUNAT checksum algorithm frozen by the specification. The API returns stable internal classifications `valid`, `invalid_shape`, and `invalid_checksum` for parity and diagnostics. Public protected boundaries retain `INVALID_RUC` with a non-enumerating message.

No adapter keeps a private checksum oracle. `internal/core/comprobante.go` delegates to the canonical validator while preserving `INVALID_EMITTER_RUC`. Caller-supplied RUCs always use the stronger validator. Stored checksum-invalid legacy rows are never normalized; compatibility reads and historical verification may load them but must classify them legacy/unbound and never v1-valid.

### Strict input and canonical hash

The one canonical CLI representation is a strict JSON document passed with `--fiscal-scope <path>`. It carries `version: "v1"` and exactly the ten named elements. V1 commands do not expose ten parallel flags. Legacy commands keep their old scope shape only under the compatibility policy.

A dedicated raw decoder rejects duplicate, unknown, missing, and extra keys; unknown versions; invalid UTF-8 or NUL; empty or whitespace-only identifiers; invalid RUC, period, snapshot, operation, or classification; and trailing JSON values. It preserves accepted values without trimming, case folding, or Unicode normalization.

Canonical bytes and lowercase SHA-256 follow the specification exactly: the version frame, then each ASCII key in contract order, equals sign, decimal UTF-8 byte length, colon, exact value bytes, and NUL terminator. Shared goldens pin valid bytes/hash, malformed encodings, and one-element drift. TypeScript uses `as const` vocabularies with extracted types, flat interfaces, and `unknown` at decode boundaries.

### Immutable act evidence and envelope linkage

A protected subject accumulates one immutable binding link per successful persisted act. This permits an agent's prepare binding and a professional's later approval binding to differ legitimately in operation, actor, and classification.

Each link has a subject-local sequence, binding hash, reviewed H1 when applicable, closed review-check state, resulting H2, timestamp, and immutable audit reference. Its act-evidence hash canonically covers:

- a versioned act-evidence frame;
- the binding hash;
- the reviewed envelope hash, empty when inapplicable; and
- `reviewChecksState` as an ordered pair in which each check is independently `omitted`, `false`, or `true`.

The core command representation preserves that tri-state instead of collapsing transport defaults into booleans:

```go
type ReviewAcknowledgement struct {
    Present bool
    Value   bool
}

type ReviewChecks struct {
    EvidenceInspected ReviewAcknowledgement
    RuleInspected     ReviewAcknowledgement
}
```

Adapters construct this value explicitly. Policy requires `Present && Value` for both checks on material or critical approval. The canonical evidence state distinguishes all nine ordered combinations; SQLite stores `NULL` for omitted, `0` for provided false, and `1` for provided true. A successful material or critical approval necessarily records `true,true`; non-approval acts record `omitted,omitted`. This presence state participates in command intent and immutable act evidence even when omitted and false lead to the same `REVIEW_CHECKS_REQUIRED` policy result.

For a v1 subject, `ComputeEnvelopeHash` appends one self-describing contribution containing every link's sequence, binding hash, and act-evidence hash in sequence order. No links means no contribution, so every legacy hash remains byte-identical. Mutation flow recomputes H1 from locked state, simulates the pending link, computes H2, then atomically stores the business transition, audit anchor, binding link, unchanged receipt payload, and idempotency completion. Any binding/check/state/sequence change alters H2 or fails verification. Replay adds nothing.

This is the sole receipt-continuity mechanism: the existing receipt signs its unchanged payload containing H2, while H2 transitively commits to the new immutable evidence.

### Additive persistence

Advance SQLite additively from schema v17 to v18 in one fail-closed transaction. Do not backfill bindings, rewrite RUCs, alter observations, or re-hash receipts.

Add `fiscal_scope_bindings` with binding hash primary key, constrained version, all ten non-null elements, exact canonical bytes, creation time, and no-update/no-delete triggers.

Add `fiscal_binding_links` with immutable id, subject type/id, contiguous subject-local sequence, binding foreign key, act-evidence hash, H1/H2, nullable constrained acknowledgement columns, logical audit-reference type/id, creation time, uniqueness for sequence and idempotent act identity, and no-update/no-delete triggers.

Absence of links is the legacy classification. V1 requires every referenced binding, byte/hash recomputation, contiguous sequence, resolvable audit reference, and current envelope recomputation to succeed. Partial/tampered evidence is `unverifiable`, never repaired or relabelled legacy. Index exact tenant/organization/RUC/period lookups and subject reloads. Pre-v18 binaries fail closed on the unsupported schema.

### Runtime and downgrade modes

Use `DRENYRA_FISCAL_RUNTIME_MODE`:

| Mode | Behavior |
| --- | --- |
| `shadow` | Initial upgraded-store default. Strictly read/diagnostic-only for the protected slice: inventory, verification, and supplied-binding diagnostics may run, but every protected v1 or legacy write fails `FISCAL_WRITE_GATE_CLOSED`. |
| `enforce` | First-slice protected mutations require a complete binding. V1 targets require an applicable binding for protected reads/mutations. Legacy protected mutation fails `SCOPE_BINDING_REQUIRED`. |
| `legacy_compat` | Explicit temporary mode and the only mode that permits a protected legacy write. It may mutate only legacy targets and cannot create or touch v1 objects or claim v1 evidence. |
| `read_only` | Safe downgrade/incident mode. Legacy/v1 reads and verification work; protected mutation fails closed. |

Unknown values fail startup. There is no per-command legacy-write exception in `shadow` or `enforce`: every protected legacy write requires the operator to select `legacy_compat` explicitly, and that selection is surfaced in audit/diagnostic output. Once v1 data exists, rollback uses `read_only` or another v18-aware dual-read release. Binding/audit rows are never removed.

## Enforcement and data flow

The common flow is: strict raw decode; pure binding/RUC validation and hash; expected operation/classification check; explicit legacy-scope projection and trusted-axis comparison; principal/provenance comparison where applicable; service repetition; store repetition before data access or reservation; exact-scope locked target load; prior link/H1 recomputation; existing policy/SoD/check/close/idempotency gates; pending act/H2 computation; one atomic commit.

Invalid input fails before scoped reads, membership lookup, idempotency reservation, object bytes, SQL mutation, signing, or audit append. Public target lookup uses exact scope predicates and preserves non-enumerating not-found behavior. Once an authorized subject is loaded, non-identity drift returns `SCOPE_MISMATCH` without foreign values.

All v1 store commands carry the full binding, not only its hash. The store recomputes it and the operation map. Legacy wrappers are mode-gated and refuse v1 targets. Evidence/rule linking gains a bound batch store transaction so one command cannot commit only a subset of refs.

### Authenticated approval transaction

Approval order is:

1. Validate binding and RUC before DB/auth lookup.
2. Begin immediate and exact-scope load before reservation.
3. Compare tenant, organization, and RUC with company registry and verified principal membership.
4. Reload links and recompute H1.
5. Hash the v1 idempotency intent over memory id, H1, reason, binding hash, and the ordered tri-state of both acknowledgements (`omitted`, `false`, or `true`); legacy reservations retain the legacy formula.
6. Enforce status, policy, assurance, SoD, and required review checks using presence plus value, never value alone.
7. Build pending approval evidence and compute H2 with status plus new link.
8. Atomically write guarded status, existing frozen approval event, fiscal link, unchanged approval/close receipts, close projection when applicable, and completed idempotency result.

Do not rebuild `approval_events`; the new immutable link references it.

## Public contracts

### CLI

The v1 professional command is:

```text
drenyra-engram approve <memory-id> --fiscal-scope <binding.json>
  --expected-envelope <hash> --reason <text>
  --evidence-inspected --applicable-rules-inspected
  [--request-id <id>] [--db <path>]
```

Acknowledgement flags are presence-aware declarations. The CLI records whether each flag occurred by inspecting the parsed flag set, so omission becomes `{Present:false, Value:false}`, `--flag=false` becomes `{Present:true, Value:false}`, and an affirmative flag becomes `{Present:true, Value:true}` in `core.ApproveMemoryCommand.ReviewChecks`. Material/critical omission and provided false both return `REVIEW_CHECKS_REQUIRED`, but they remain distinct through adapter validation, command intent, and diagnostics. Principal identity still comes only from the authenticated session. Parse/validate the binding before loading a token or opening the store. V1 machine output adds binding version/hash and a redacted presence/value `reviewChecksRecorded` summary; tokens remain redacted.

Local seed requires the authoritative `DRENYRA_ENV=local_dev`, isolated paths, fictional data, and canonical RUC validation. It cannot mint production authority.

### HTTP

Keep `POST /accounting/memories/{memoryId}/approve`. The strict v1 body contains expected envelope, reason, complete `fiscalScope`, and a `reviewChecks` object that recognizes only `evidenceInspected` and `applicableRulesInspected`. The raw DTO uses presence-aware nullable booleans (or an equivalent custom decoder): an absent object or member maps to `Present:false`, explicit `false` maps to `Present:true, Value:false`, and explicit `true` maps to `Present:true, Value:true`; non-boolean values, duplicate keys, and unknown fields are rejected. A bounded pre-auth middleware validates raw binding/RUC before the resolver performs membership reads, caches the body, and places the validated value in context. The handler maps the public applicable-rules name to the command's `RuleInspected` acknowledgement without losing presence, rejects authority fields, and passes principal as a separate verified argument.

### MCP

Stdio remains unauthenticated and advertises no identity/role/assurance fields. First-slice v1 create/mutate tools take one strict binding JSON string or fail closed. `accounting_approve` short-circuits to `AUTHENTICATION_REQUIRED` before argument decode or state work; claimed identity/review fields cannot create authority. The published schema does not advertise identity fields.

## Boundary matrix

| Boundary | Input | First guard | Authoritative guard | Failure/compatibility |
| --- | --- | --- | --- | --- |
| CLI local seed | RUC flag | Before DB open | `SeedIdentity` repeats validation | `INVALID_RUC`; no identity/session row |
| CLI protected writes | Binding file | Before store open | Bound store transaction | No object/row/receipt/audit/partial batch |
| CLI protected reads | Binding file for v1 | Before query | Service/store exact scope and hash reload | Empty/not-found or binding code; no writes |
| CLI approval | Binding file | Before token/auth | Locked approval transaction | Existing policy or binding code; no reservation |
| HTTP scope query | Query/body RUC | Adapter/pre-auth | Service/store | Non-enumerating invalid/binding error |
| HTTP approval | Raw fiscal scope + checks | Bounded pre-auth binding middleware | Locked approval transaction | Strict existing envelope plus stable codes |
| MCP protected operation | Binding JSON string | Strict decoder | Service/store | Invalid params/in-band code; no mutation |
| MCP approval | No trusted identity | Immediate auth failure | No store call | Always `AUTHENTICATION_REQUIRED` |
| Direct Go service | Complete binding | Before store interface call | Store repeats | Mock observes no call for invalid input |
| Direct SQLite command | Complete binding | Before connection/read/reservation | Exact-scope locked reload | Rollback and typed error |
| Company/auth relation | Binding RUC | Pure RUC before lookup | Tenant+organization+RUC+principal comparison | No substitution or foreign values |
| Evidence object/link | Binding and exact target | Before temp path/target read | Object/link transaction | No bytes, link, row, receipt, or orphan from validation |
| Export/reconstruction | Requested binding + stored links | Before scoped query | Read-only recomputation | Legacy labelled; tampering unverifiable |
| Offline verification | Persisted legacy/v1 evidence | Caller RUC when supplied | Binding/link/envelope/receipt recomputation | No network/write/inference |
| TypeScript mirror | Binding/company input | Pure decoder | In-memory store repetition | Same classifications/hashes; atomic failure |

## Verification and audit

Add a pure verification layer named `fiscal scope binding` in Go and TypeScript:

- no links: skipped with `legacy/unbound: no v1 fiscal binding evidence`;
- complete links: passed with deterministic act count;
- missing binding, sequence gap, invalid canonical bytes/hash/RUC, unresolved audit anchor, act-evidence mismatch, or envelope mismatch: failed.

`VerifyMemory`, `VerifyEvidenceObject`, `VerifyReceipt`, reconstruction, and export use read-only store methods. Existing Ed25519 and payload verification stays unchanged. Every report retains `Accounting correctness: NOT ASSERTED`; no binding, classification, receipt, or acknowledgement authorizes a business act.

| Persisted act | Existing anchor | New immutable linkage |
| --- | --- | --- |
| Save/supersede | Observation/source and transition/receipt | Sequence, binding/act evidence, H1/H2 |
| Evidence store/link | Object/link row and applicable receipt | Binding evidence and exact anchor |
| Memory/close approval | Frozen approval event and existing receipts | Binding, checks state, H1/H2, event id |
| Close create/reopen | Close memory and closure event/projection | Binding evidence for successful act |
| Verification/reconstruction | Existing subject evidence | Read-only classification only |

Inventory is read-only and reports counts plus stable opaque identifiers derived from tenant, table, and primary key. It never prints invalid RUCs, content, foreign tenant identifiers, tokens, or credentials.

## Proposed modules

### New

| Path | Responsibility |
| --- | --- |
| `internal/core/ruc.go` | Canonical SUNAT validator and classification. |
| `internal/core/fiscal_scope.go` | Binding, strict validation, canonical/hash, operation map, act/envelope evidence. |
| `core/ruc.ts`, `core/fiscal-scope.ts` | TypeScript mirrors using const vocabularies and strict types. |
| `internal/store/fiscal_scope_store.go` | v18 migration/DDL, repository, classification, inventory, mode gates. |
| `internal/store/migration_v18_test.go` | Additive, rollback, no-backfill, immutability, downgrade tests. |
| `internal/server/fiscal_scope_service.go` | Shared operation/trusted-context checks. |
| `internal/server/fiscal_scope_http.go` | Raw-body pre-auth validation and strict DTOs. |
| `cmd/drenyra-engram/fiscal_scope.go` | Binding-file loader, acknowledgement flags, inventory/mode wiring. |
| `testdata/golden/fiscal-ruc-v1.json` | Shared RUC vectors. |
| `testdata/golden/fiscal-scope-v1.json` | Shared binding, act, and envelope vectors. |
| `contracts/fiscal-scope-v1.md` | Additive contract; does not rewrite frozen history. |

### Existing

| Path(s) | Planned change |
| --- | --- |
| `internal/core/types.go`, `internal/core/approval.go`, `core/types.ts`, `core/index.ts` | Canonical RUC delegation, presence-aware approval command acknowledgements, optional fiscal link state, v1-only envelope contribution, exports. |
| `internal/core/comprobante.go` | Remove private checksum logic. |
| `internal/core/golden_test.go`, `core/__tests__/golden.test.ts` | Shared parity runners. |
| `internal/core/verify.go`, `core/verify.ts` | Fiscal binding verification layer. |
| `internal/auth/errors.go` | Add binding/write-gate codes only. |
| `internal/store/store.go`, `internal/store/object_store.go` | Schema chain, bound operations, exact-scope query, reload, v1 idempotency, object guard. |
| `internal/server/api.go`, `approval_service.go`, `close_service.go`, `context_service.go`, `review_service.go`, `reconstructibility_service.go`, `verify_service.go` | Bound operations, batch links, trusted context, verification. |
| `internal/server/http.go`, `reconstructibility_http.go`, `mcp.go` | Canonical RUC, strict v1 input, public checks, MCP boundary. |
| `cmd/drenyra-engram/main.go`, `reconstructibility.go`, `verify_test.go` | Public wiring in the actual command host, machine-readable metadata, and CLI verification coverage. |
| `store/memory-store.ts` | Repeat semantic store enforcement atomically. |
| `contracts/scope.md`, `approval.md`, `verification.md`, `README.md` | Additive references and public contract; frozen sections remain intact. |

Frozen changed-path alarm: `internal/core/receipt.go`, `core/receipt.ts`, `contracts/receipts.md`, receipt canonicalization tests, and existing receipt golden vectors. A necessary payload-format edit is not a task here; it triggers the hard stop.

## Test strategy

- Table-driven Go and Vitest RUC shape/checksum/boundary cases, including Unicode digits.
- Shared binding bytes/hash, every-element drift (including independent `ledgerBook` versus `operationType` drift), duplicate/unknown/reordered/NUL/multibyte/version/period/snapshot failures.
- Act-evidence and cumulative envelope vectors, including byte-identical legacy no-contribution.
- Fresh v18 and genuine v17 upgrade in temporary directories; rows/envelopes/receipts unchanged; no inferred bindings/RUC rewrite.
- Immutable trigger, tamper, sequence-gap, audit-anchor, mode, downgrade, and direct-store-bypass tests.
- Boundary-matrix positive/negative controls with zero reservations, bytes, rows, links, receipts, audit, or idempotency state on invalid input.
- Cross-tenant, organization, RUC, period, and source-snapshot non-disclosure controls.
- CLI/HTTP/core-command material and critical approval success with both checks; table-driven coverage distinguishes each acknowledgement's omitted, explicit-false, and true states, while every required omitted/false combination fails `REVIEW_CHECKS_REQUIRED` atomically.
- Stale H1, wrong actor/binding, inactive membership, role/assurance, SoD, conflict, concurrency, and signing-failure controls.
- Close approval and post-close denial retain integer-cent money and human control.
- Existing receipt goldens run unchanged; v1 H2 fails without exact immutable binding/check evidence.
- Legacy records remain readable and explicitly unbound; offline verification remains network-free/read-only and ends with the exact non-claim.

No tests run in this planning phase.

## Delivery slices

Tasks must produce dependency-ordered, independently reviewable slices and keep each candidate within the review budget where practical. Implementation starts only after a later native attempt acquire authorizes its work unit.

1. Contract and pure Go/TypeScript RUC/binding canonical foundation plus shared vectors.
2. Additive persistence, migration, inventory, classification, and runtime modes; no public writes.
3. V1 envelope linkage and store enforcement for save/supersede/evidence/rule/object/close, including atomic batch links.
4. Service/read/verification enforcement, audit resolution, cross-runtime layer parity.
5. Authenticated approval transaction, v1 idempotency intent, acknowledgement evidence, atomicity controls.
6. Public CLI/HTTP/MCP adapters and local-dev seed hardening.
7. Rollout integration evidence, receipt changed-path audit, docs, and consumer handoff.

Each slice runs focused Go tests, TypeScript tests/typecheck where applicable, and unchanged receipt goldens. `ask-on-risk` requires a delivery decision before apply; this design grants no size exception.

## Rollout and rollback

1. Freeze and approve the versioned scope/acknowledgement contracts, error envelopes, canonical RUC/scope/act vectors, and migration-state definitions before any production source or schema change.
2. Ship the additive migration under default `shadow`, then run read-only inventory and checksum/binding diagnostics; mutate no legacy or v1 data.
3. Compare Go/TypeScript vectors, validate migrated-store classification and rollback, and keep every protected write gated. Any protected legacy write still requires an explicit, separately selected `legacy_compat` mode.
4. Enable gated v1 writes through `enforce` only after migration rollback, store bypass, boundary, parity, receipt-continuity, no-partial-state, and rollback-readiness evidence passes.
5. Publish CLI/HTTP professional approval only after presence-aware acknowledgement, policy, SoD, idempotency, and positive/negative atomicity controls pass.
6. Unblock `fiscal-onboarding-journey` only after a passing verify report. It must consume production binding persistence, validator, and public professional path, replacing test-only substitutes.

Before v1 writes, code may roll back while empty additive tables remain. After v1 links exist, use `read_only` or a v18-aware release. Never run a v17 binary, delete immutable evidence, default omitted checks true, or relabel v1 as legacy.

## Risks and controls

- Receipt continuity: denylist, unchanged payloads, legacy byte goldens, hard stop.
- Envelope expansion: self-describing and present only for linked v1 subjects.
- Tenant/RUC leakage: validation before reads and exact-scope SQL.
- False authority: binding metadata never enters authorization as permission.
- Rubber stamping: checks are immutable declarations, not proof of diligence/correctness.
- Legacy invalid data: no repair; readable/diagnosable but protected mutation fails closed.
- Adapter drift: one canonical core plus store repetition and boundary matrix.
- Downgrade: schema refusal plus explicit read-only mode.
- Local-development isolation: the frozen spelling is `DRENYRA_ENV=local_dev`; tests reject every other environment value before seed mutation.
- Workload: seven slices, no implied single-PR exception.

## Acceptance

Task decomposition is authorized only if it preserves the receipt hard stop, uses this operation/boundary matrix, keeps migration additive and legacy bytes stable, advances Go and TypeScript together, and retains `fiscal-onboarding-journey` solely as the blocked dependent consumer.
