# Tasks — Fiscal Runtime Foundations

## Review Workload Forecast

| Field | Value |
| ------- | ------- |
| Estimated changed lines | Slice 1: 260–360; Slice 2: 300–420; Slice 3: 360–520; Slice 4: 300–440; Slice 5: 280–400; Slice 6: 260–380; Slice 7: 180–300; aggregate: 1,940–2,820 |
| 400-line budget risk | High |
| Chained PRs recommended | Yes |
| Suggested split | PR 1 → PR 2 → PR 3 → PR 4 → PR 5 → PR 6 → PR 7, one design slice per review unit |
| Delivery strategy | auto-chain (session preflight, 2026-09-14) |
| Chain strategy | stacked-to-main (selected 2026-09-14) |

Decision needed before apply: Resolved
Chained PRs recommended: Yes
Chain strategy: stacked-to-main — PR 1 (`feat/fiscal-runtime-foundations-slice-1`, targets main) → PR 2 (`feat/fiscal-runtime-foundations-slice-2`, targets slice-1 branch) → ...
400-line budget risk: High

**Planning gate:** resolved — chain strategy selected 2026-09-14. No task below authorizes implementation by itself; each runtime work unit requires native attempt acquisition before launch.

## Execution Rules

- Strict TDD is active: every implementation slice runs RED → GREEN → TRIANGULATE → REFACTOR, with tests kept in the same work unit as the behavior.
- Every slice records focused Go and TypeScript commands, runtime-harness evidence or an explicit `N/A` rationale, changed-path boundaries, and rollback scope.
- Run unchanged receipt goldens for every slice that touches core, store, services, adapters, or verification. Any required change to `internal/core/receipt.go`, `core/receipt.ts`, `contracts/receipts.md`, receipt canonicalization, or existing receipt goldens is a hard stop for a separate receipt-contract proposal.
- Apply fiscal guardrails in every slice: canonical RUC validation, RUC/tenant isolation, immutable audit provenance, integer cents, no external SUNAT lookup, and no interpretation of scope metadata as authority.

## Delivery Slice 1 — Contract and pure canonical foundation

**Depends on:** validated spec/design only. **Start:** frozen legacy scope/receipt files unchanged and no v1 fiscal binding implementation. **Finish:** Go and TypeScript pure validators, canonical binding/hash, tri-state review types, and shared vectors are independently consumable without persistence or public writes. **Changed-path boundary:** `contracts/fiscal-scope-v1.md`, additive sections in `contracts/scope.md`, `contracts/approval.md`, `internal/core/ruc.go`, `internal/core/fiscal_scope.go`, `internal/core/types.go`, `internal/core/approval.go`, `internal/core/comprobante.go`, `core/ruc.ts`, `core/fiscal-scope.ts`, `core/types.ts`, `core/index.ts`, `testdata/golden/fiscal-ruc-v1.json`, `testdata/golden/fiscal-scope-v1.json`, and focused parity tests; do not modify receipt implementation/goldens or store/server/CLI behavior.

- [x] RED: add table-driven Go and Vitest cases for ASCII shape, SUNAT modulo-11 checksum, Unicode/non-ASCII digits, canonical ten-element validation, strict vocabulary, byte framing, every-element drift, malformed JSON/value encoding, and tri-state review acknowledgements; assert stable classifications/errors and byte-identical golden expectations. <!-- sdd-owner: implementation -->
- [x] GREEN: implement the canonical Go validator/binding decoder/serializer/hash and TypeScript mirror using const vocabularies, flat interfaces, `unknown` decode boundaries, and no private comprobante checksum oracle; preserve frozen public error and receipt contracts. <!-- sdd-owner: implementation -->
- [x] TRIANGULATE: run `go test ./internal/core -run 'RUC|Fiscal|Golden'`, the focused Vitest suite, `npm run typecheck`, and `go test ./internal/core -run TestGoldenVectorsGo`; compare Go/TypeScript vectors and confirm no receipt bytes or frozen receipt paths changed. <!-- sdd-owner: implementation -->
- [x] REFACTOR: remove duplication, document canonical ordering/non-authorization semantics, export only additive APIs, and keep the focused diff within the slice boundary. <!-- sdd-owner: implementation -->
- [x] Record work-unit commit evidence, runtime command/result, and rollback as removal of the additive validators/contracts/vectors without touching legacy scope or receipt artifacts. <!-- sdd-owner: implementation -->

## Delivery Slice 2 — Additive persistence, migration, inventory, and runtime modes

**Depends on:** Slice 1 canonical types, bytes, hashes, and error classifications. **Start:** canonical pure foundation is green; database remains schema v17. **Finish:** schema v18 additive migration, immutable binding/link tables, classification/inventory, mode gates, and downgrade refusal are tested with no public v1 writes. **Changed-path boundary:** `internal/store/fiscal_scope_store.go`, `internal/store/migration_v18_test.go`, relevant additive schema chain in `internal/store/store.go`, `internal/auth/errors.go`, and migration/inventory fixtures/tests; do not wire public adapters or mutate receipt code.

- [x] RED: add temporary-directory tests for genuine v17→v18 upgrade, fresh v18 creation, no backfill, invalid-RUC inventory opacity, legacy/v1/unverifiable classification, immutable triggers, schema refusal by pre-v18 binaries, `shadow`/`enforce`/`legacy_compat`/`read_only` behavior, and rollback boundaries. <!-- sdd-owner: implementation -->
- [x] GREEN: implement the additive migration and repositories for `fiscal_scope_bindings` and `fiscal_binding_links`, immutable constraints/triggers, exact lookup indexes, safe opaque inventory, runtime-mode parsing, and fail-closed schema/downgrade gates. <!-- sdd-owner: implementation -->
- [x] TRIANGULATE: run focused migration/store tests with `t.TempDir()`, `go test ./internal/store ./internal/core`, `go vet ./...`, and receipt golden verification; prove rows, envelopes, receipts, and invalid legacy RUCs are unchanged. <!-- sdd-owner: implementation -->
- [x] REFACTOR: isolate migration and mode policy from command wiring, make diagnostics non-enumerating, and document explicit compatibility/read-only rollback boundaries. <!-- sdd-owner: implementation -->
- [x] Record the migration checkpoint, exact test results, and rollback scope limited to additive schema/runtime-mode code while retaining any existing legacy data. <!-- sdd-owner: implementation -->

## Delivery Slice 3 — Envelope linkage and store enforcement for protected writes

**Depends on:** Slices 1–2. **Start:** v18 storage exists but no protected operation creates v1 links. **Finish:** save/supersede, evidence/object/rule linking, close creation, and post-close guards persist immutable act evidence atomically; direct store bypasses fail closed. **Changed-path boundary:** `internal/core/fiscal_scope.go`, envelope/link-related core files, `internal/store/store.go`, `internal/store/object_store.go`, `internal/store/fiscal_scope_store.go`, and focused store/core tests; do not add public CLI/HTTP/MCP wiring or approval adapter changes.

- [ ] RED: add store-level tests for complete-binding enforcement, operation/classification mismatch, trusted-axis mismatch, legacy-to-v1 refusal, invalid input before reservation/object access, atomic evidence/rule batches, H1/H2 linkage, sequence gaps, tampering, idempotent replay, and `PERIOD_CLOSED`. <!-- sdd-owner: implementation -->
- [ ] GREEN: implement immutable act-evidence canonicalization, v1-only envelope contribution, exact binding reload/hash checks, store-authoritative operation map, bound save/supersede/evidence/object/rule/close commands, atomic batch links, and close write guards. <!-- sdd-owner: implementation -->
- [ ] TRIANGULATE: run focused store/core tests, direct bypass and cross-tenant/RUC/period/source-snapshot controls, `go test ./...`, and unchanged receipt goldens; verify zero partial rows/objects/links/receipts/audit/idempotency state on rejected commands. <!-- sdd-owner: implementation -->
- [ ] REFACTOR: consolidate transaction guard ordering, preserve legacy receipt bytes, and make immutable-link/audit references explicit without introducing receipt payload changes. <!-- sdd-owner: implementation -->
- [ ] Record the slice boundary and rollback as disabling v1 protected write commands while retaining additive immutable evidence and legacy reads. <!-- sdd-owner: implementation -->

## Delivery Slice 4 — Services, protected reads, verification, and parity enforcement

**Depends on:** Slices 1–3. **Start:** store can enforce v1 writes and immutable evidence. **Finish:** services and read-only paths enforce complete scope, exact predicates, audit resolution, reconstruction/export/verification classification, and Go/TypeScript semantic parity. **Changed-path boundary:** `internal/server/fiscal_scope_service.go`, service files for context/review/reconstructibility/verify/close as required by the design, `internal/core/verify.go`, `core/verify.ts`, `store/memory-store.ts`, `core/__tests__`, and focused server/store tests; do not expose new CLI/HTTP/MCP approval input yet.

- [ ] RED: add service/read/verification tests for pre-auth RUC validation, exact scope predicates, cross-tenant/organization/RUC/period/snapshot non-disclosure, legacy/unbound versus v1 versus unverifiable reports, audit-anchor resolution, reload mismatch, offline operation, and the exact `Accounting correctness: NOT ASSERTED` conclusion. <!-- sdd-owner: implementation -->
- [ ] GREEN: implement service defense-in-depth validation, trusted-context comparisons, bound read/reconstruction/export methods, verification classification, audit linkage resolution, and the TypeScript semantic store/verification repetition. <!-- sdd-owner: implementation -->
- [ ] TRIANGULATE: run focused Go server/store tests, TypeScript tests/typecheck, `go test ./...`, `npm test`, and `npm run typecheck`; confirm no network/write behavior in offline verification and unchanged receipt goldens. <!-- sdd-owner: implementation -->
- [ ] REFACTOR: align error mapping and parity fixtures, isolate read-only verification from authorization, and remove any inferred/default scope path. <!-- sdd-owner: implementation -->
- [ ] Record evidence and rollback as disabling bound service/read paths while preserving legacy labelled reads and stored immutable evidence. <!-- sdd-owner: implementation -->

## Delivery Slice 5 — Authenticated approval transaction and acknowledgement evidence

**Depends on:** Slices 1–4. **Start:** bound store/service/read/verification path is green. **Finish:** authenticated CLI/HTTP-facing service command supports presence-aware checks, exact envelope/binding, policy/SoD/idempotency, immutable evidence, compatible receipts, and atomic failure controls; adapter transport remains outside this slice. **Changed-path boundary:** `internal/core/approval.go`, approval service/authz files, relevant `internal/server/approval_service.go` and `internal/server/close_service.go`, idempotency/store transaction code, and focused approval tests; do not change CLI flag parsing or HTTP DTOs yet.

- [ ] RED: add table-driven tests for omitted/false/true acknowledgement states, material/critical policy, stale H1, wrong actor/binding, inactive membership, role/assurance, self-approval, idempotency conflict/replay, concurrency, signing failure, close approval, and post-close denial with zero partial state. <!-- sdd-owner: implementation -->
- [ ] GREEN: carry tri-state `ReviewChecks` through the authenticated approval transaction, hash v1 idempotency intent, enforce policy/SoD and exact envelope freshness, compute/store H2 and immutable act evidence, and retain existing approval/close receipt payloads unchanged. <!-- sdd-owner: implementation -->
- [ ] TRIANGULATE: run focused approval/close tests, store bypass tests, unchanged receipt golden tests, `go test ./...`, and audit/verification assertions for trusted actor, RUC, period, reason, receipt continuity, and `Accounting correctness: NOT ASSERTED`. <!-- sdd-owner: implementation -->
- [ ] REFACTOR: separate principal-derived authority from binding metadata, centralize acknowledgement mapping, and make transaction ordering visibly fail closed before reservation and mutation. <!-- sdd-owner: implementation -->
- [ ] Record rollback as closing the new professional approval capability (never defaulting checks to true or bypassing policy) while retaining historical receipts and immutable evidence. <!-- sdd-owner: implementation -->

## Delivery Slice 6 — Public CLI/HTTP/MCP adapters and local-dev seed hardening

**Depends on:** Slices 1–5. **Start:** authenticated approval command/service is proven independently of transport. **Finish:** strict CLI binding-file input, presence-aware approval flags, authenticated HTTP `fiscalScope`/`reviewChecks`, MCP strict binding for protected operations, unauthenticated MCP approval failure, and isolated fictional local seed are wired and machine-readable. **Changed-path boundary:** `cmd/drenyra-engram/fiscal_scope.go`, `cmd/drenyra-engram/main.go`, relevant CLI reconstructibility/verify files, `internal/server/fiscal_scope_http.go`, `internal/server/http.go`, `internal/server/reconstructibility_http.go`, `internal/server/mcp.go`, auth/local-dev seed files, and adapter tests; do not modify receipt formats or onboarding artifacts.

- [ ] RED: add boundary-matrix tests proving invalid RUC/binding rejection before DB/token/store work, strict duplicate/unknown/trailing JSON rejection, omitted versus false flags/fields, redaction, HTTP authority-field rejection, MCP `AUTHENTICATION_REQUIRED` before decode/store, and `DRENYRA_ENV=local_dev` isolation. <!-- sdd-owner: implementation -->
- [ ] GREEN: wire `--fiscal-scope <path>`, presence-aware CLI acknowledgement flags, strict HTTP DTO/pre-auth validation, MCP binding input/fail-closed approval, canonical RUC scope parsing, and fictional local seed validation through the shared services. <!-- sdd-owner: implementation -->
- [ ] TRIANGULATE: run focused CLI/HTTP/MCP tests, `go test ./...`, `go vet ./...`, `gofmt -l .`, `npm test`, and typecheck where TypeScript adapters are touched; verify no token/credential leakage, foreign disclosure, or partial mutation. <!-- sdd-owner: implementation -->
- [ ] REFACTOR: keep transport decoding strict and thin, preserve machine-readable stable codes, document local-only limitations, and confirm changed paths stay within the adapter/seed boundary. <!-- sdd-owner: implementation -->
- [ ] Record rollback as disabling v1 public adapter exposure and failing closed for material approvals, while leaving authenticated policy and immutable store evidence intact. <!-- sdd-owner: implementation -->

## Delivery Slice 7 — Rollout evidence, frozen-contract audit, documentation, and handoff

**Depends on:** Slices 1–6. **Start:** runtime and public paths pass focused verification. **Finish:** rollout gates, migration/rollback evidence, changed-path/frozen-contract audit, docs, and dependent-consumer handoff are complete; `fiscal-onboarding-journey` remains preserved and blocked until verify passes. **Changed-path boundary:** `contracts/`, `docs/`, migration/rollout guidance, verification fixtures/reports, and explicit handoff documentation; do not implement onboarding or alter frozen receipt files/goldens.

- [ ] RED: add rollout/evidence checks for inventory, contract/vector freeze, shadow gate, enforce gate, legacy compatibility selection, downgrade refusal, receipt changed-path denylist, complete verification evidence, and consumer handoff prerequisites. <!-- sdd-owner: implementation -->
- [ ] GREEN: document versioned scope/approval contracts, legacy/v1 classifications, fictional local-dev operation, rollback/read-only modes, non-authorization boundary, required commands, and the exact onboarding handoff without claiming onboarding implementation. <!-- sdd-owner: implementation -->
- [ ] TRIANGULATE: run the complete verification order `npm run typecheck`, `go vet ./...`, `gofmt -l .`, `go test ./...`, `npm test`; run compliance gates where applicable (`bun run compliance:sire-gate`, `bun run compliance:sire-repro`), inspect changed paths, and confirm all receipt goldens remain byte-identical. <!-- sdd-owner: implementation -->
- [ ] REFACTOR: remove stale workaround language, ensure docs match implemented version/error semantics, preserve explicit `Accounting correctness: NOT ASSERTED`, and produce a final trace from each specification requirement to evidence. <!-- sdd-owner: implementation -->
- [ ] Record final work-unit commit/rollback boundaries and mark the dependent onboarding consumer blocked pending a passing verify report; do not modify its implementation in this change. <!-- sdd-owner: implementation -->

## Parent-owned lifecycle actions

- [x] Select the chain strategy or explicitly approve a documented size exception before launching apply; retain the ask-on-risk decision in the delivery record. <!-- sdd-owner: parent -->
- [ ] Start or reuse one bounded review per approved delivery slice after implementation and before delivery gating. <!-- sdd-owner: parent -->
- [ ] Confirm all seven slices have native attempt acquisition, focused evidence, clean changed-path boundaries, and no unresolved receipt hard stop before verification/archive. <!-- sdd-owner: parent -->
