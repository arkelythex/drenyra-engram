# Design — audit-register-closure

> Phase: design · Artifact: design · Status: draft
> Inputs: frozen spec + proposal + exploration inventory. Strict TDD applies during apply and verify.
> Store: OpenSpec file-based; mirrored to Engram topic `sdd/audit-register-closure/design`.
> Binding constraints: FR-L.*, FR-J.*, FR-Q.*, FR-Z.*, FR-G.*, AC-*, NFR-*, EC-*, and FD-1…FD-4 are not reopened here.

## Context and design posture

This change closes evidence gaps around behavior that is expected to exist already. The implementation consists of Go test-only catalogs, fixtures, conformance matrices, and bounded documentation updates. It introduces no production API, schema, override, migration ledger, or numerical recovery objective.

The central rule for apply is fail-before-fix: a matrix row that exposes a real behavioral defect remains red, the affected block remains RISK, and apply stops that slice. A production correction is an exception requiring a separately approved proposal/spec amendment with compatibility and rollback analysis. It must not be hidden in test helpers or made green by weakening an expected outcome.

Inspection of `internal/server/api.go` establishes an important Q risk up front: several canonical methods accept only an object/memory ID or no scope at all (`Get`, `Relations`, `Transitions`, `Compare`, `Approve`, `Reject`, `Void`, `Supersede`, `LinkEvidence`, `LinkRules`, `LinkRuleVersion`). The Q catalog must include them. If tenant-B identity cannot be expressed at the canonical boundary, or tenant-A state is returned, the row is a real conformance failure; no passing synthetic outcome is designed here.

## Decisions

- `D-1` — All reusable machinery stays in `_test.go` files in the package that owns the exercised boundary. Go cannot import helpers declared in another package's `_test.go` files; therefore server and CLI use small local adapters rather than creating a production `testsupport` package.
- `D-2` — Consolidated matrices are executable catalogs, not duplicated implementations. Umbrella rows either execute in-package HTTP/MCP/store scenarios or name a unique dedicated CLI/sync test. A named external proof is validated by a structural guard so stale links fail.
- `D-3` — Reflection is permitted only where it is the least brittle representation of an exported Go method/struct shape: Q compares the exported method set of `*server.API` with the test catalog, and J inspects an explicit set of canonical request/command types. Reflection never invokes unknown methods or infers business outcomes.
- `D-4` — CLI structural absence is checked from the command's constructed `flag.FlagSet`, not by repository-wide source-string scanning. HTTP/MCP unknown-input behavior is checked by sending forbidden keys through the real strict decoders.
- `D-5` — Migration failure proof uses a targeted v13 schema collision in the last migration. A deliberately wrong pre-existing `idx_rule_links_ref` lets `migrateV13ToV14` execute its earlier transactional ALTER work and then fail at index creation. Reopening the raw database proves those earlier changes rolled back; removing the blocker and reopening through `Open` proves re-run convergence. This is deterministic transaction-interruption proof, not a claim that a test killed an OS process.
- `D-6` — Operability gets a new focused document, `docs/architecture/operability-evidence.md`. The older evidence-object document is still edited because its deferred-production-operations sentence is stale, but it does not become the home for doctor and recovery evidence.
- `D-7` — `contracts/provenance.md` receives two frozen subsections under the existing non-authorization/migration contract: “Override absence (frozen)” and “Migration provenance for schema v14 (frozen)”. No contract version or production schema version changes.

## L — Idempotency matrix and lost-response proof

### File placement

- `internal/server/idempotency_replay_matrix_test.go` — `TestIdempotencyReplayMatrix`, the consolidated catalog, server-local fixtures, request builders, replay/conflict assertions, and proof-link validation.
- `internal/server/mcp_purge_test.go` — add `TestMCPPurgeRequestIdempotentReplay`.
- `internal/server/mcp_judgment_test.go` — add `TestMCPJudgmentProposeIdempotentReplay`.
- `internal/server/mcp_reconciliation_test.go` — add `TestMCPReconciliationProposeIdempotentReplay`.
- `cmd/drenyra-engram/judge_test.go` — add `TestCLIJudgeReplay`.
- `cmd/drenyra-engram/reconcile_test.go` — add `TestCLIReconcileReplay`.
- `cmd/drenyra-engram/review_test.go` — add `TestCLIReviewRejectReplay`.
- `cmd/drenyra-engram/purge_test.go` — add `TestCLIPurgeRequestReplay`; add rows for every other idempotent purge subcommand exposed by the current parser.
- Other existing command test files receive replay tests when their command exposes `--request-id` (retention, hold, reopen). They are linked from the umbrella catalog by their unique test name.
- `internal/store/idempotency_interrupted_reservation_test.go` — consolidated lost-response cases for approval, judgment, reconciliation, review, hold, and reopen, while citing the existing purge tests.

### Consolidated catalog

`TestIdempotencyReplayMatrix` owns this test-only shape:

```go
type replaySurface string
const (
    surfaceHTTP replaySurface = "HTTP"
    surfaceMCP  replaySurface = "MCP"
    surfaceCLI  replaySurface = "CLI"
    surfaceSync replaySurface = "sync"
)

type replayCase struct {
    operation       string
    surface         replaySurface
    fixture         func(t *testing.T) replayFixture
    firstRequest    func(replayFixture) replayInvocation
    sameRequest     func(replayFixture) replayInvocation
    changedPayload  func(replayFixture) replayInvocation
    changedPrincipal func(replayFixture) replayInvocation
    assertOutcome   func(t *testing.T, first, replay replayResponse)
    assertConflict  func(t *testing.T, response replayResponse)
    effectSnapshot  func(t *testing.T, fixture replayFixture) effectSnapshot
    externalProof   string
}
```

The catalog has one named row per `(operation/surface)`, with names formatted `operation/surface/expected`, satisfying EC-2. It enumerates:

- approval: approve memory;
- judgment: propose, confirm, withdraw;
- reconciliation: propose, decide, confirm, withdraw;
- review: reject;
- evidence lifecycle: place hold, lift hold, retention-policy put;
- period lifecycle: reopen period;
- purge: request, approve, reject, execute/finalize, plus any currently exposed idempotent cancel/withdraw command;
- sync: second import.

For HTTP and MCP rows the closures execute the real registered handler/tool. CLI and sync rows set `externalProof` to the unique test named above or the existing `TestSyncIdempotent` / `TestCLISyncRoundTrip`. A `TestIdempotencyReplayMatrix/proof-links-resolve` subtest parses package test declarations with `go/parser` and fails if a named external proof does not exist. It does not claim that a link executed in the server package; the whole-suite gate executes it.

`replayFixture` contains a fresh in-memory store, deterministic tenant/scope, two verified principals, a fixed clock-compatible fixture, and operation-specific seeded IDs. Each operation fixture is a dedicated function (`seedApprovalReplay`, `seedJudgmentReplay`, and so on) so setup does not branch inside assertions.

`replayInvocation` carries serialized request bytes/arguments and an invocation closure. Request builders must derive all four variants from one canonical command: identical replay changes nothing; changed payload changes exactly one bound business field; changed principal changes only the verified principal. Request IDs and timestamps are constants per subtest.

`effectSnapshot` is logical, not raw SQLite bytes: it captures operation-specific entity/projection fields plus event, receipt, and idempotency-completion counts. `assertReplay` requires the same stored entity/event/result identity, `idempotentReplay=true` where exposed, and an equal before/after snapshot. `assertConflict` requires `auth.Code(err) == IDEMPOTENCY_CONFLICT` or the existing operation-specific frozen typed conflict, and the same snapshot. Sync uses `{imports, skipped, conflicts}` and content counts rather than a request ID; its second run must report zero imports and conflicts.

The MCP tests mirror the setup and transport invocation of the fresh-only tests at `mcp_purge_test.go`, `mcp_judgment_test.go`, and `mcp_reconciliation_test.go`, but issue the exact same tool call twice and decode both results. The second result must carry the first stored IDs and replay marker. The CLI tests mirror the command harnesses already present in `judge_test.go`, `reconcile_test.go`, `review_test.go`, and `purge_test.go`; they run the command twice against the same temporary database with the same `--request-id`, decode stdout JSON, then open/read the store to compare event/receipt counts.

### Interrupted reservation / lost-response tests

`internal/store/idempotency_interrupted_reservation_test.go` uses a test-only descriptor:

```go
type interruptedReservationCase struct {
    operation string
    seed      func(t *testing.T, s *SQLiteStore) interruptedInvocation
    insert    func(t *testing.T, s *SQLiteStore, in interruptedInvocation)
    invoke    func(context.Context, *SQLiteStore, interruptedInvocation) (any, error)
    code      string
    snapshot  func(t *testing.T, s *SQLiteStore, in interruptedInvocation) effectSnapshot
}
```

For approval, judgment, reconciliation, review, and hold, `insert` computes the command hash with the same package-private hash/shape helper used by production and inserts a row into the operation's real idempotency table with the entity/event/result/completed columns `NULL`, exactly mirroring `TestRequestPurgeInterruptedReservationIsConflictNotScanError` at `purge_store_test.go:58`. Every replay asserts the typed conflict (never a scan/JSON decoding/persistence error) and equal logical state/event/receipt counts.

Use the actual ledgers:

- approval: `idempotency_keys`, NULL `approval_event_id`, `result_json`, `completed_at`;
- judgment: `judgment_idempotency_keys`, NULL `judgment_id`, `judgment_event_id`, `result_json`, `completed_at` as applicable to the command;
- reconciliation: `reconciliation_idempotency_keys`, NULL entity/event/result/completion columns;
- review: `review_idempotency_keys`, NULL `decision_event_id`, `result_json`, `completed_at`;
- hold: `evidence_hold_idempotency_keys`, NULL `hold_id`, `result_json`, `completed_at`.

Reopen does not have a nullable reservation ledger: `period_closure_events` is the completed immutable outcome, and the projection flip plus event insert occur in one `BEGIN IMMEDIATE` transaction. Its lost-response proof is therefore two-part and must state this distinction:

1. install a temporary test trigger that aborts insertion of the target `reopened` event after the projection update has been attempted; call `ReopenPeriod`, then assert rollback left the period closed, no event/receipt/request row exists, and a clean retry succeeds exactly once;
2. simulate response loss after commit by discarding the first successful result and invoking the same command again; assert the existing event is replayed with `IdempotentReplay=true` and no duplicate event/receipt.

This is stronger and more honest than fabricating an impossible half-event row. If the schema prevents the trigger mechanism, apply records the limitation and leaves AC-L-4 red; adding a production reservation seam is an exception requiring re-approval.

## J — Policy matrix and override-negative conformance

### Consolidated policy matrix

Add `internal/authz/consolidated_policy_matrix_test.go` with `TestConsolidatedVersionedPolicyMatrix`.

```go
type policyCase struct {
    policy      string
    version     string
    role        auth.AccountingRole
    action      string
    effect      core.FiscalEffect
    assurance   auth.AssuranceLevel
    relationship principalRelationship
    configure   func(*policyFixture)
    authorize   func(policyFixture) policyDecision
    allowed     bool
    reasonCode  string
}
```

The first subtest asserts exact constants:

- `approval-policy/v0.4.0`;
- `judgment-policy/v0.4.0`;
- `reconciliation-policy/v0.5.0`;
- `evidence-lifecycle-policy/v0.8.0`.

Representative rows then reuse the real public authorization functions and frozen fixture constructors from each policy's existing tests. At minimum each policy has one allow, one role denial, one scope denial, one assurance denial, and one SoD denial. Lifecycle adds first approval, second approval by controller/tax-responsible, operational-accountant/admin denial, dual-category disabled denial, requester-equals-approver denial, and same-principal second approval denial. A separate `check-order` subtest copies the ten named probes from `TestLifecyclePolicyCheckOrderIsFrozen` and asserts the exact existing order rather than inventing a second order.

The consolidated matrix supplements rather than replaces the four policy-specific suites. Version changes intentionally fail this test and require separate contract approval.

### Structural absence

Add `internal/server/no_override_surface_test.go` with `TestNoOverrideFieldOnRequestSurfaces`. It uses reflection over an explicit `[]reflect.Type` registry of canonical exported `core.*Command` / `core.*Input` values actually accepted by API, HTTP, and MCP mutation boundaries. For each recursively reachable exported field it normalizes the Go name and JSON tag, then rejects `override`, `breakglass`, `force`, `bypass`, and equivalent normalized spellings. Recursive traversal has a visited-type set and inspects structs, pointers, slices, arrays, and maps' element types; it does not scan comments or output types.

The explicit registry is justified because request types are the security boundary, not every struct in `core`. To prevent drift, an adjacent AST guard parses `internal/server/api.go`, identifies exported `*core.XCommand`/`*core.XInput` parameters on exported API methods, and requires each discovered type to be in the reflection registry or explicitly classified as read-only. Adapter-local HTTP/MCP argument structs are registered alongside canonical types. The test doc comment cites the deliberate-absence comments listed by FR-J.2.

Add `cmd/drenyra-engram/no_override_surface_test.go` with `TestCLINoOverrideFlagOnCommands`. It constructs each current command's real `flag.FlagSet`, calls `VisitAll`, and rejects the normalized forbidden spellings. A command-catalog guard compares tested command names with the root command dispatch/help catalog so a new command cannot bypass inspection. This is more robust than scanning all source strings, where comments and denial messages legitimately contain the forbidden words.

### Behavioral absence at boundaries

- `internal/server/override_negative_test.go` — `TestOverrideInputDeniedFailClosed`: send JSON/MCP arguments containing each forbidden spelling through real strict decoders for representative approval, judgment, reconciliation, review, and evidence-lifecycle mutations. Expected result is the adapter's existing unknown-field/invalid-input typed denial, with no state/event/receipt/idempotency change.
- `cmd/drenyra-engram/no_override_surface_test.go` — `TestCLIOverrideInputDeniedFailClosed`: invoke representative privileged commands with `--override`, `--break-glass`, `--force`, and `--bypass`; expect existing unknown-flag usage failure and no database change.
- `internal/store/authorization_no_bypass_test.go` — `TestDeniedOperationNotBypassableViaAdapterOrStore`: table rows invoke the same denied scenario at policy, server/service, and store boundaries. Include an administrative principal and a same-principal SoD case. Each layer must return the same frozen auth reason code; store snapshots remain unchanged.

No test API, fake override field, or permissive parser is added. A surface that currently accepts unknown fields/flags is a real defect and triggers the production-exception rule.

### Contract placement

In `contracts/provenance.md`, insert `## Frozen override-absence decision` immediately after `## Non-authorization boundary` and before migration semantics. It states:

- no override, break-glass, force, bypass, or privileged emergency path exists;
- override audit is negative conformance, not an unimplemented feature;
- privileged and urgent callers remain subject to the same versioned policy, exact scope, assurance, role, and SoD/dual-approval checks;
- professional memory approval remains outside business/payment authorization.

The audit J row cites the consolidated matrix, server/CLI structural tests, behavioral boundary test, and this section. Block I wording remains unchanged.

## Q — Generated cross-tenant matrix

### Test-only catalog and exhaustiveness guard

Add `internal/server/cross_tenant_matrix_test.go` in package `server` so it can build the real API and access established server fixtures without production exports.

```go
type operationKind string
const (
    opRead operationKind = "read"
    opMutation operationKind = "mutation"
    opObservation operationKind = "observation"
)

type safeOutcome string
const (
    safeNotFound safeOutcome = "not_found"
    safeEmpty    safeOutcome = "empty"
    safeZero     safeOutcome = "zero"
    safeDenied   safeOutcome = "scope_denied"
)

type apiOperationCase struct {
    method       string
    kind         operationKind
    seedTenantA  func(t *testing.T, f *crossTenantFixture) operationState
    invokeAsB    func(t *testing.T, f *crossTenantFixture, s operationState) (any, error)
    safeOutcome  safeOutcome
    assertSafe   func(t *testing.T, result any, err error, s operationState)
    snapshotA    func(t *testing.T, f *crossTenantFixture, s operationState) tenantStateSnapshot
}
```

`TestCrossTenantMatrixExhaustiveness` computes the exported method set with `reflect.TypeOf((*API)(nil))`, removes only constructor/non-method helpers (none are methods), and compares it with unique `method` values in the catalog. The catalog must include every method currently listed by `internal/server/api.go`, including `FindPeriodClosure`; adding or removing an API method makes the test red. Reflection is appropriate here because the exported method set is exactly the canonical surface named by FD-4. No JSON or production registry is created.

The catalog explicitly covers: Save, Get, GetByTopic, Chain, Search, Context, Relations, Transitions, Doctor, StoreObject, GetObject, PutRetentionPolicy, ResolveRetentionPolicy, EvaluatePurgeEligibility, PlaceHold, LiftHold, ActiveBlockingHolds, HoldsForObject, RequestPurge, ApprovePurge, RejectPurge, CancelPurge, WithdrawPurge, FinalizePurge, ExportEvidenceLifecycle, ReviewQueue, Reconstructibility, RuleShow, RuleHistory, RuleImpact, ReviewDetail, RejectMemory, ReturnMemory, Compare, Approve, Reject, Void, Supersede, LinkEvidence, LinkRules, LinkRuleVersion, Judge, PeriodSummary, and FindPeriodClosure.

Private `gatedTransition` is not in the reflected exported method set and is therefore not a catalog row.

### Scenario construction

`crossTenantFixture` owns one fresh in-memory store and two exact company scopes with different organization, company, RUC, and period identifiers. Each row receives a fresh fixture to prevent order dependence.

Each `seedTenantA` creates the minimum valid state through existing store/server fixture helpers, never by weakening constraints:

- memory reads/lifecycle/link/rule/compare rows seed one or two tenant-A memories and required approval state;
- object/retention/hold/purge/export rows seed tenant-A object bytes, policy, holds, and pipeline stage as needed;
- judgment/reconciliation/period/review rows seed the required tenant-A workflow state;
- search/context/chain/summary rows seed colliding topic/content under A and harmless control data under B;
- doctor is classified as store-global operational observation and must prove its report does not contain tenant/company/RUC/period identifiers; if the requirement is interpreted as tenant-scoped invocation, the row remains red because the API has no caller scope.

`invokeAsB` always supplies tenant B's exact scope and, for authenticated operations, a verified tenant-B principal. It intentionally passes the tenant-A resource ID/topic/request where the method permits it. `assertSafe` pins the operation's existing contract: not found, empty collection, zero result, or exact frozen scope-denied code. It serializes both result and error and rejects any tenant-A organization/company/RUC/period/resource identifier.

For ID-only or global methods that cannot carry B scope/principal, the row is still present but `invokeAsB` fails with a test diagnostic such as `canonical API.Get cannot express caller scope`. This is not an allowed safe outcome. It exposes the real gap and blocks AC-Q-1…Q-5 until a separately approved correction defines the canonical boundary. The apply agent must not skip, mark N/A, call the store directly, or pretend adapter scoping proves the canonical API method.

`TestCrossTenantMatrix` executes all rows. `TestCrossTenantMutationNoSideEffect` filters `kind == opMutation`, captures `snapshotA` before and after, and requires equality. The snapshot is a deterministic logical digest over tenant-A rows relevant to the operation plus event, receipt, idempotency-key/completion, relation, transition, and object-byte hashes. It does not hash a live WAL file. Read rows explicitly pin:

- object → `OBJECT_NOT_FOUND`;
- retention resolve → zero policy / `ok=false`;
- reconstructibility → zero denominator;
- chain/search/context/review queues/history → empty;
- policy-frozen scope denial where that is already the contract.

Any row exposing foreign existence or lacking a way to express B at the canonical boundary fails Q. Production remediation is outside this design and requires re-approval.

## Z — Operability documentation closure

### New evidence document

Create `docs/architecture/operability-evidence.md` with exactly these sections:

1. **Purpose and evidence boundary** — repository-verifiable mechanics only; no production-readiness, external-drill, SLA, SLO, RTO, or RPO claim.
2. **Delivered capabilities and executable evidence** — a table with capability, implementation, positive tests, and fail-closed tests:
   - routine/full doctor: `internal/store/doctor.go`, the cited doctor/object-hardening/server/CLI tests;
   - WAL-safe snapshot: `internal/store/drill.go::CreateDrillSnapshot`;
   - restore and verify-after-restore: `TestRunRestoreDrillSuccess`, negative matrix, scope isolation, schema-version fail-closed;
   - corruption drills on marked copies only: all cited `TestRunCorruptionDrill*` tests;
   - exact scope and backup-identity verification;
   - tenant lifecycle export: the cited store and HTTP tests.
3. **Qualitative recovery objectives** — state that snapshot, restore, post-restore verification, and corruption detection are proven mechanisms. State verbatim that numerical RTO and RPO targets are deployment/business-owned and UNKNOWN until an accountable owner records them. Include no number, duration, percentile, or target.
4. **Operational boundaries and non-claims** — tests do not prove a deployment's backup schedule, retained recovery points, external drill completion, guaranteed recovery time/data loss, encryption-at-rest/TDE, cloud storage, or production service readiness.
5. **Evidence maintenance** — PASS claims must retain unique executable citations; removal/failure of cited proof restores conservative RISK wording.

### Reconciliation edits

- `docs/architecture/evidence-object-v0.7.md`, current lines 58–59: replace the single “Production object-store operations” deferred bullet with a split statement. Mark repository-local snapshot/restore and marked-copy corruption drills as delivered with `drill.go`/test citations. Keep cloud/remote operations, encryption-at-rest/TDE, deployment-owned numerical recovery objectives, and externally run production drills explicitly unproven.
- `docs/product/initial-market-and-v1-gate.md`, G-5 row: remove only “production backup/restore drills” from the stale deferred list and cite `operability-evidence.md`; retain cloud/remote storage, scheduler executor, OCR/content search, and all unrelated unmet items.
- Same file, G-6 row: record restore/corruption drills as repository-delivered with test citations. Do not mark the whole gate MET unless every other G-6 element is proven by its own current evidence; retain a bounded PARTIAL classification where race/fuzz/cross-tenant prerequisites are not all closed.
- `docs/due-diligence/2026-08-product-architecture-audit.md`: change only rows G, J, L, Q, and Z after their complete block criteria land. Z becomes `PASS for repository-demonstrated operability / RISK for deployment objectives` or equivalently bounded wording, with citations to the new document and executable tests. Adjacent rows and production-readiness caveats remain unchanged.

Add `internal/store/operability_documentation_test.go` as a structural readback test. It locates the repository root from the test file path, reads the four documents, checks required headings/citations, checks stale drill phrases are removed from the named blocks, and rejects numeric RTO/RPO target patterns. This is a documentation contract test, not production behavior.

## G — Direct upgrades, interruption recovery, and provenance

### Genuine legacy fixture matrix

Add `internal/store/migration_direct_upgrade_test.go` with `TestDirectUpgradeMatrixV1ToV14`.

```go
type legacyFixture struct {
    version int
    build   func(t *testing.T, path string) *sql.DB
    seed    func(t *testing.T, db *sql.DB) legacySnapshot
    assertPreserved func(t *testing.T, s *SQLiteStore, before legacySnapshot)
}
```

Builders reuse the frozen historical helpers rather than cloning DDL:

- v1 uses `v1SchemaDDL` and its legacy-row seed from `store_test.go`;
- v2, v3, v4, v5, v7, v8, v9, v10, v11, and v13 use `openV2Schema`, `openV3Schema`, `openV4Schema`, `openV5Schema`, `openV7Schema`, `openV8Schema`, `openV9Schema`, `openV10Schema`, `openV11Schema`, and `openV13Schema`;
- v6 is built from `openV5Schema` plus the real `migrateV5ToV6`;
- v12 is built from `openV11Schema` plus the real `migrateV11ToV12`.

Where an existing builder reaches its version by applying prior production migrations, that is the frozen migration-era layout already used by the per-step tests and is accepted as genuine. Each builder asserts `readSchemaVersion == source version` before seeding and closes the raw handle before normal `Open`.

Every source fixture seeds a baseline observation/provenance row valid at that version. At the first version where a feature exists, also seed a representative row that exercises its additive migration preservation: approval/membership, judgment, reconciliation, materiality/close, structured policy data, receipt/key, evidence object, retention policy, hold, purge lifecycle, review decision, and legacy unversioned rule link. The row snapshot stores exact scalar/JSON/blob bytes and row counts. Later versions carry forward all applicable representatives.

For each v1…v13 row:

1. build and seed the genuine source store;
2. close the raw handle;
3. call normal `Open(path)` exactly once;
4. assert schema version 14;
5. compare every seeded value and relevant row count with the pre-open snapshot;
6. assert the final invariant manifest: all v14 tables/triggers/indexes from the existing per-version helper lists, `rule_links.version`, `rule_links.effective_at`, and `idx_rule_links_ref`; no staging tables such as `receipts_v*` remain;
7. run representative immutable/no-delete/fail-closed checks already exposed by the migrated structures.

The test must not reduce validation to schema version. Existing per-step migration tests remain unchanged and green.

### Deterministic interruption and retry

In the same file add:

- `TestMigrationInterruptedRollsBackToPriorVersion`;
- `TestMigrationCrashReopenConvergesToV14`.

Use a genuine seeded v13 database. Before closing the raw handle, create an index named `idx_rule_links_ref` with a deliberately wrong definition. Normal `Open` enters `migrateV13ToV14`: the migration adds the two columns and then fails when creating the expected index. Assertions after the failed open, using a new raw read handle, are:

- `schema_version` is still 13;
- `rule_links.version` and `rule_links.effective_at` are absent, proving earlier statements in the transaction rolled back;
- the seeded observation and legacy rule-link bytes are unchanged;
- no staging/partial artifacts or duplicate rows exist.

Then drop only the deliberately wrong index, close the raw handle, and call `Open` again. Assert convergence to v14, exact data preservation, the correct index definition, and one copy of each seeded row. The second test may share a setup helper but must execute the full reopen path independently so both required test names carry evidence.

The tests document precisely that they prove SQLite transaction rollback plus deterministic re-run convergence under the repository's one-transaction-per-step model. They do not claim to emulate an OS kill at every instruction. No production hook is added. If SQLite/driver behavior makes this point non-injectable, AC-G-2 remains red and the residual gap is documented; a production migration hook or ledger is not authorized.

### Provenance contract

In `contracts/provenance.md`, replace the stale literal `schema_version = 2` wording in the migration section with version-neutral policy text plus a new `### Migration provenance for schema v14 (frozen)` subsection. It states:

- ordered migration functions and their reviewed code order;
- one transaction per migration step, with schema version advanced last;
- final `schema_version=14` as the authoritative release provenance record;
- no migration-history table, per-deployment event ledger, or schema v15 in this change;
- proves current schema generation and deterministic code path;
- does not prove wall-clock execution history, operator identity, or per-deployment migration events.

The audit G row cites the direct-upgrade matrix, both interruption/reopen tests, and this subsection.

## Data flow and invariants

### Replay flow

1. Fixture creates valid state and captures logical effect counts.
2. First transport request reaches the existing adapter/service/store and commits once.
3. The same transport request repeats with identical tenant, request ID, payload, and principal.
4. Existing idempotency storage returns the original outcome; the adapter exposes replay semantics where supported.
5. Test compares stored identity and logical counts; changed payload/principal requests are then rejected without effects.

### Cross-tenant flow

1. Fresh fixture seeds tenant A through valid existing APIs.
2. Test captures tenant-A logical state.
3. Tenant B invokes the canonical API method with A's identifier and B's exact scope/principal where the signature permits it.
4. Result must be operation-specific not-found/empty/zero/typed denial and must not serialize A identifiers.
5. Mutation rows compare tenant-A state and global event/receipt/idempotency counts before and after.

### Migration flow

1. Frozen helper builds a source-version database and seeds representative legacy bytes.
2. Normal `Open` reads `schema_version` and executes each remaining production migration in order.
3. Every step commits independently and advances its version last.
4. Final test verifies v14 structure and unchanged legacy data.
5. Interruption case fails inside v13→v14, proves rollback to coherent v13, removes the test corruption, and proves reopen convergence.

Standing invariants remain: exact structural tenant scope, no new authority, integer-only money, no shared Go↔TS semantic change, no production schema/API change, deterministic fixtures, and no secrets/customer data.

## TDD delivery order

Strict TDD applies to every slice. Each slice starts by adding the named failing tests, captures the expected red reason, then adds only test fixtures/helpers/docs needed to make existing production behavior provable. At each green boundary run `gofmt` on touched Go files, focused tests, `go test ./...`, and `npm test`. Final verification also runs `npm run typecheck`, `go vet ./...`, `gofmt -l .`, and `go test ./internal/core -run TestGoldenVectorsGo`.

1. **J slice** — red consolidated policy/structural/behavioral tests; add test catalogs and provenance override section; green without production changes. If unknown input is accepted or a store bypass exists, stop for re-approval.
2. **L slice** — red replay matrix, MCP/CLI replay, and interrupted-reservation tests; add only test helpers. Reopen's trigger rollback test and post-commit replay are mandatory. Any inconsistent runtime behavior stops the slice.
3. **Q slice** — first land the exhaustiveness guard and complete catalog red; then per-operation seeds/outcomes. Known unscoped canonical methods are expected risk points. Any such failure blocks Q and requires a separate approved API/security correction; no production change is allowed in this slice.
4. **G slice** — red v1…v13 rows and interruption/reopen tests; add fixture composition and assertions only; update migration provenance after executable proof is green.
5. **Z slice** — red documentation readback test; add new evidence document and three reconciliation edits; update the audit row only after all Z checks pass.
6. **Whole-change evidence pass** — only when each block's ACs are green, update that audit row to bounded PASS. Partial slices retain RISK and cite landed evidence. Run all whole-change gates.

## File change plan

| File | Planned change | Production behavior? |
|---|---|---|
| `internal/authz/consolidated_policy_matrix_test.go` | Four-policy version/role/action/effect/SoD/check-order matrix | No |
| `internal/server/no_override_surface_test.go` | Canonical request-type structural guard | No |
| `internal/server/override_negative_test.go` | HTTP/MCP unknown-input denial and no-effect proof | No |
| `cmd/drenyra-engram/no_override_surface_test.go` | CLI flag catalog and denial tests | No |
| `internal/store/authorization_no_bypass_test.go` | Policy/service/store denial parity | No |
| `internal/server/idempotency_replay_matrix_test.go` | Consolidated operation × surface catalog | No |
| Existing MCP test files | Purge/judgment/reconciliation replay cases | No |
| Existing CLI command test files | Idempotent command replay cases | No |
| `internal/store/idempotency_interrupted_reservation_test.go` | Approval/judgment/reconciliation/review/hold/reopen recovery | No |
| `internal/server/cross_tenant_matrix_test.go` | API method catalog, exhaustiveness, scenarios, side-effect proof | No |
| `internal/store/migration_direct_upgrade_test.go` | v1…v13 fixtures, v14 assertions, interruption/reopen | No |
| `internal/store/operability_documentation_test.go` | Documentation structural readback | No |
| `contracts/provenance.md` | Override absence and schema-v14 provenance decisions | Documentation only |
| `docs/architecture/operability-evidence.md` | Delivered capability evidence and qualitative objectives | Documentation only |
| `docs/architecture/evidence-object-v0.7.md` | Reconcile stale drill deferral | Documentation only |
| `docs/product/initial-market-and-v1-gate.md` | Reconcile G-5/G-6 conservatively | Documentation only |
| `docs/due-diligence/2026-08-product-architecture-audit.md` | Bounded G/J/L/Q/Z status/citations after full evidence | Documentation only |

Any edit to a non-test Go/TypeScript file, schema DDL, API signature, adapter decoder, policy semantics, idempotency implementation, or migration implementation is an exception. Apply must stop, report the failing criterion and evidence, and obtain re-approval before such a change.

## Acceptance traceability

| Criteria | Primary design proof |
|---|---|
| AC-L-1, AC-L-5, AC-L-6 | `TestIdempotencyReplayMatrix` named rows, effect snapshots, conflict variants |
| AC-L-2 | Three dedicated MCP replay tests |
| AC-L-3 | CLI replay tests in owning command files |
| AC-L-4 | Consolidated interrupted-reservation tests plus atomic reopen rollback/post-commit replay |
| AC-L-7 | External proof links to `TestSyncIdempotent` and `TestCLISyncRoundTrip` |
| AC-L-8 | Audit L row updated only after L suite passes |
| AC-J-1 | `TestConsolidatedVersionedPolicyMatrix` |
| AC-J-2 | Server reflection/AST guard plus CLI real-FlagSet guard |
| AC-J-3 | Adapter unknown-input and policy/service/store denial tests |
| AC-J-4 | Frozen override-absence subsection in `contracts/provenance.md` |
| AC-J-5 | Bounded audit J wording; block I unchanged |
| AC-Q-1 | Reflected exported API method set equals test catalog |
| AC-Q-2, AC-Q-4 | Per-method seed/invoke/assert rows and leakage scan |
| AC-Q-3 | Mutation-filtered logical state/event/receipt/idempotency snapshots |
| AC-Q-5 | Audit Q row only after every catalog row is safe |
| AC-Z-1, AC-Z-2 | New operability evidence document plus readback test |
| AC-Z-3 | Exact audit/v1-gate/evidence-object reconciliation edits |
| AC-Z-4 | Bounded PASS and explicit deployment-owned UNKNOWN objectives |
| AC-G-1 | `TestDirectUpgradeMatrixV1ToV14` with genuine builders, preservation, invariants |
| AC-G-2 | Deterministic v13 mid-step failure, rollback, blocker removal, normal reopen |
| AC-G-3, AC-G-4 | Frozen schema-v14 provenance and limitations subsection |
| AC-G-5 | Audit G row updated only after G suite passes |
| AC-XC-1…AC-XC-6 | Slice gates, full Go/TS/type/vet/format/golden checks, and production-change review |

## Rollout and rollback

Delivery follows independently reviewable J → L → Q → G → Z slices. Audit classifications change last in each block. Test-only additions are removable without data migration. Documentation rollback restores the prior conservative RISK wording and must not leave an unsupported PASS. Because no production schema or API change is authorized, there is no persisted-data rollout. A separately approved defect correction, if needed, must carry its own rollout/rollback design and cannot be folded into this artifact.

## Risks

- Q may immediately expose that ID-only/global API methods cannot express tenant B. That is valuable evidence, not a reason to exclude methods; it blocks Q pending separate approval.
- Some idempotency families may treat incomplete reservations differently. The test pins the existing typed deterministic contract; scan/decoding errors or duplicate effects are failures.
- Reopen has no nullable reservation row. The design tests both sides of its atomic commit boundary instead of fabricating impossible state.
- The migration collision proves transaction rollback and retry, not an arbitrary OS kill. The claim and documentation stay bounded to that property.
- Reflection/AST guards can fail on harmless refactors; their scope is deliberately limited to exported API methods and canonical request types/flags.
- Documentation must not erase still-open cloud storage, encryption, external drill, deployment objective, identity, race, or other adjacent risks.

## skill_resolution

`none` — no project/user skill path was injected for this delegated phase. The required OpenSpec artifacts and targeted source evidence were read directly. CodeGraph was not available in the provided tool surface, so structural inspection used targeted file reads and symbol grep rather than broad filesystem discovery.
