# v0.4.0 Step 1 — Authenticated ApprovalPrincipal integration design

Status: implementation-ready (open points resolved by user decisions, 2026-08-05).  
Scope: Go engine plus the TypeScript semantic mirror. v0.3.0 hash bytes and existing consumer adapters remain compatible.

## 1. Decisions and current integration points

- Approval authority moves from caller-declared `Source` to `auth.VerifiedApprovalPrincipal`. `Source` remains provenance only, as required by ADR-003.
- The new service contract is `ApproveMemory(command, principal) (ApprovalResult, error)`, where `command = {MemoryID, Reason, ExpectedEnvelopeHash, RequestID}`. No transport payload may contain principal fields.
- `Scope.OrganizationID` is the tenant id, `Scope.CompanyID` is the company id, and `Scope.Period` is the optional fiscal-period id (`internal/core/types.go:33-55`, `internal/core/types.go:359-416`). Do not derive company id from RUC; the current HTTP query helper does that only as an adapter shortcut (`internal/server/http.go:378-408`).
- H1 is recomputed from the locked current row plus current evidence/rule link rows. H2 is recomputed after status becomes `approved`. The persisted `observations.envelope_hash` becomes a derived current-envelope cache and is updated with status/link changes.
- The old `core.Approve(*AccountingMemory, Source)` remains as a deprecated low-level lifecycle guard for v0.3 consumers (`internal/core/lifecycle.go:130-142`). It is not reachable from the new approval transports. Existing reject/void behavior is unchanged.
- `transition_log` remains the generic v0.3 audit mirror. A successful approval writes both one `approval_events` row and one transition row; the event is authoritative for authenticated approval.

## 2. Contracts and package boundaries

### Go

Add `internal/auth`:

- `AuthenticationMethod`: `oidc | session | service_assertion | local_dev` (`oidc` recognized but resolver returns `AUTHENTICATION_REQUIRED` in Step 1; the stateless access-token slice now resolves it on the HTTP surface — [oidc-access-token-identity.md](oidc-access-token-identity.md)).
- `AssuranceLevel`: `low | standard | strong`.
- `AccountingRole`: `accountant | senior_accountant | controller | tax_reviewer | authorized_tax_professional`.
- `VerifiedApprovalPrincipal` has unexported fields and read-only getters. There is no struct literal or public arbitrary-input constructor outside this package.
- `Resolver.Authenticate(ctx, AuthenticationAssertion) (VerifiedApprovalPrincipal, error)` is the only factory path. The assertion type is produced by transport-specific parsers and contains credential material, not identity claims.
- `PrincipalSnapshot()` deliberately omits `sessionId`, token material, cookies, and unrelated claims.

Add `internal/authz`:

- `ApprovalAuthorizationPolicy.Authorize(principal, memory) Decision` is pure: no database, clock, token, or identity-provider access.
- `Decision = {Allowed bool, PolicyVersion string, ReasonCode string}`; version is exactly `approval-policy/v0.4.0`.
- Base role matrix: journal_entry/adjustment/reclassification → accountant; approval → senior_accountant; closing → controller; declaration → tax_reviewer; sunat_filing → authorized_tax_professional.
- Role dominance applies only to the accounting ladder `accountant < senior_accountant < controller`; tax roles are explicit and not implied by controller.
- Materiality raises the accounting ladder to accountant/senior/controller. Classification comes from a NEW declared `materialityLevel` field on the memory (`normal|material|critical`, NULL = normal), set by the writing agent; the existing `Materiality *int64` threshold field is NOT reinterpreted by policy (user decision).
- Minimum assurance is `standard`; `sunat_filing` requires `strong`. A local-dev seed may issue `standard`, but only outside production.
- Check order is tenant, company scope, membership active, role, assurance, materiality. Return the first frozen reason code for reproducibility.

Add approval contracts in `internal/core/approval.go` (command/result/event/value types only) and orchestration in `internal/server/approval_service.go`:

```go
ApproveMemory(ctx context.Context, cmd core.ApproveMemoryCommand,
    principal auth.VerifiedApprovalPrincipal) (core.ApprovalResult, error)
```

The service validates syntax, maps principal to provenance `Source{System: method, ActorID: subjectId, ActorKind: human, Session: sessionId}`, and delegates the complete state change to one store operation. ActorKind records the verified claim; it does not authorize.

`ApprovalResult` and `ApprovalEvent` exactly follow the binding spec. Roles in snapshots are sorted/deduplicated before JSON encoding so Go and TS produce canonical bytes.

## 3. Authentication derivation

- **session**: hash the bearer token with SHA-256, look up `sessions.token_hash`, require not revoked and `expires_at > now`, then load active membership, company, and roles. Raw bearer values are never stored.
- **service_assertion**: in Step 1 it is also an opaque, high-entropy bearer credential resolved by token hash; its session row has `authentication_method='service_assertion'`. This avoids accepting self-declared JWT claims before a trust/key contract exists.
- **local_dev**: available only when runtime mode is explicitly `local_dev` and the listener is loopback. Starting with local-dev auth in production mode fails; presenting a local-dev session in another mode returns `PRINCIPAL_INVALID`. No silent fallback when Authorization is absent.
- **HTTP context**: middleware resolves Authorization once and places only the verified principal in request context. The current optional shared token guard (`internal/server/http.go:72-125`) is not identity and cannot authorize approval.
- **CLI**: `auth login --token <token>` validates the token against the selected DB/server and stores only the token in a user-only (0600) config file. `approve` loads it and resolves the principal; `--actor` is rejected on the new command.
- **MCP**: `accounting_approve` accepts only command arguments. The current stdio server has no authenticated session binding (`cmd/drenyra-engram/main.go:432-462`), so it returns `AUTHENTICATION_REQUIRED`. HTTP MCP may approve only when the HTTP middleware supplies a bound principal to `MCPServer`; tool arguments never supply it.

## 4. Schema v3 and migration

`schemaVersion` becomes 3 (`internal/store/store.go:68-73`). Keep fresh-store bootstrap at v2, then run the same v2→v3 migration used for existing stores, so all six tables and the version bump occur in one transaction. Set `schema_meta=3` only last; any error rolls back and startup fails closed.

The same v2→v3 migration also adds `materiality_level TEXT` to `observations` (NULL, `normal`, `material`, `critical`; NULL is treated as `normal` by policy).

```sql
CREATE TABLE companies (
  id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, ruc TEXT NOT NULL,
  name TEXT NOT NULL DEFAULT '', active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0,1)),
  created_at TEXT NOT NULL,
  UNIQUE(tenant_id,id), UNIQUE(tenant_id,ruc)
);
CREATE TABLE memberships (
  id TEXT PRIMARY KEY, subject_id TEXT NOT NULL, tenant_id TEXT NOT NULL,
  company_id TEXT NOT NULL, status TEXT NOT NULL CHECK(status IN ('active','inactive')),
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  UNIQUE(tenant_id,id), UNIQUE(subject_id,tenant_id,company_id),
  FOREIGN KEY(tenant_id,company_id) REFERENCES companies(tenant_id,id)
);
CREATE TABLE membership_roles (
  membership_id TEXT NOT NULL REFERENCES memberships(id),
  role TEXT NOT NULL CHECK(role IN ('accountant','senior_accountant','controller','tax_reviewer','authorized_tax_professional')),
  created_at TEXT NOT NULL, PRIMARY KEY(membership_id,role)
);
CREATE TABLE sessions (
  id TEXT PRIMARY KEY, token_hash TEXT NOT NULL UNIQUE,
  membership_id TEXT NOT NULL REFERENCES memberships(id),
  authentication_method TEXT NOT NULL CHECK(authentication_method IN ('session','service_assertion','local_dev')),
  assurance_level TEXT NOT NULL CHECK(assurance_level IN ('low','standard','strong')),
  authenticated_at TEXT NOT NULL, expires_at TEXT NOT NULL,
  revoked_at TEXT, created_at TEXT NOT NULL
);
CREATE TABLE approval_events (
  id TEXT PRIMARY KEY, request_id TEXT NOT NULL, memory_id TEXT NOT NULL REFERENCES observations(id),
  tenant_id TEXT NOT NULL, company_id TEXT NOT NULL, fiscal_period_id TEXT,
  action TEXT NOT NULL CHECK(action='approved'),
  from_status TEXT NOT NULL CHECK(from_status='pending_review'),
  to_status TEXT NOT NULL CHECK(to_status='approved'),
  reviewed_envelope_hash TEXT NOT NULL, resulting_envelope_hash TEXT NOT NULL,
  reason TEXT NOT NULL, principal_subject_id TEXT NOT NULL,
  membership_id TEXT NOT NULL REFERENCES memberships(id), principal_roles_json TEXT NOT NULL,
  authentication_method TEXT NOT NULL, assurance_level TEXT NOT NULL,
  principal_authenticated_at TEXT NOT NULL, policy_version TEXT NOT NULL,
  authorization_reason_code TEXT NOT NULL CHECK(authorization_reason_code='AUTHORIZED'),
  created_at TEXT NOT NULL,
  UNIQUE(tenant_id,request_id), UNIQUE(memory_id)
);
CREATE TABLE idempotency_keys (
  tenant_id TEXT NOT NULL, request_id TEXT NOT NULL,
  command_hash TEXT NOT NULL, principal_subject_id TEXT NOT NULL,
  membership_id TEXT NOT NULL, approval_event_id TEXT REFERENCES approval_events(id),
  result_json TEXT, created_at TEXT NOT NULL, completed_at TEXT,
  PRIMARY KEY(tenant_id,request_id),
  CHECK((approval_event_id IS NULL) = (result_json IS NULL))
);
CREATE INDEX idx_memberships_subject ON memberships(subject_id,tenant_id,status);
CREATE INDEX idx_sessions_membership ON sessions(membership_id,expires_at);
CREATE INDEX idx_approval_events_memory ON approval_events(memory_id,created_at);
CREATE TRIGGER approval_events_no_update BEFORE UPDATE ON approval_events BEGIN
  SELECT RAISE(ABORT,'IMMUTABLE_APPROVAL_EVENT'); END;
CREATE TRIGGER approval_events_no_delete BEFORE DELETE ON approval_events BEGIN
  SELECT RAISE(ABORT,'IMMUTABLE_APPROVAL_EVENT'); END;
```

`companyScopes` is `[memberships.company_id]` in Step 1. The shape remains an array for future multi-company membership. `fiscalPeriodId` is `NULLIF(observations.period,'')`.

Do not add a uniqueness constraint to legacy `transition_log` in v3: existing databases may contain duplicates and sync uses value-based idempotency (`internal/store/store.go:1499-1516`). Approval duplication is prevented by `approval_events.UNIQUE(memory_id)` and the idempotency key.

## 5. Atomic approval algorithm

Add `SQLiteStore.ApproveMemory(ctx, command, principalSnapshot, policy)`. Do not compose `FindByID` plus `ApplyStatusTransition`; that is currently a TOCTOU split (`internal/server/api.go:311-340`) and the low-level update does not compare the old status (`internal/store/store.go:1317-1367`).

Use a dedicated `*sql.Conn` and execute literal `BEGIN IMMEDIATE`; `database/sql.BeginTx(nil)` starts deferred and `MaxOpenConns(1)` only serializes this process, not another process (`internal/store/store.go:153-154,1327`). `BEGIN IMMEDIATE` itself is the write intent and obtains SQLite's reserved writer lock before any race-sensitive read.

Inside the immediate transaction:

1. Look up `(principal.tenantId, requestId)`. If present, compare `command_hash` and stored principal binding. Different command/principal → `IDEMPOTENCY_CONFLICT`; completed identical row → decode result with `idempotentReplay=true` and commit/return.
2. Insert the idempotency reservation. `command_hash = SHA256(memoryId NUL lowercase(expectedHash) NUL exact reason)`; principal subject/membership are separate compared columns.
3. Read the observation and all evidence/rule refs through the same connection. Missing → `MEMORY_NOT_FOUND`.
4. Derive tenant/company/period from scope. Check tenant then company; institutional memories fail `COMPANY_SCOPE_DENIED`. Do not include memory content in errors.
5. Require `pending_review`. Already approved/rejected → `ALREADY_DECIDED`; other states → `INVALID_TRANSITION`.
6. Recompute H1 from current row plus canonical linked refs; never trust the stored envelope cache. Compare with expected; mismatch returns only expected/actual hashes.
7. Run the pure policy. Any denial rolls back the reservation and returns its frozen code.
8. `UPDATE observations SET status='approved', authority_status=?, envelope_hash=? WHERE id=? AND status='pending_review'`; require exactly one row. H2 is computed from the same snapshot with status approved and must differ from H1.
9. Insert immutable approval event and the legacy transition row, using one captured UTC timestamp.
10. Store the serialized non-replay `ApprovalResult` on the idempotency row, commit, then return.

A loser waits at `BEGIN IMMEDIATE`, reads the committed approved status, and returns `ALREADY_DECIDED`. A timeout retry with the same request id replays the committed result. Transaction rollback removes partial reservations/events.

Evidence/rule link writers must also use an immediate transaction and update the derived envelope cache after insertion. This makes a post-review link produce a new actual H1 while retaining v0.3 canonical ref ordering (`internal/core/types.go:441-459`).

## 6. Transport and error contracts

New HTTP route: `POST /accounting/memories/{memoryId}/approve`. Require `Authorization: Bearer ...`, `Idempotency-Key: approval-...`, and strict body `{expectedEnvelopeHash,reason}`. Use `json.Decoder.DisallowUnknownFields`; therefore `actor`, `actorKind`, `subjectId`, `roles`, and any other extra field are rejected, not ignored. The legacy `/v1/observations/{id}/approve` remains temporarily available only behind the deprecated Go adapter and is disabled by default in the daemon.

HTTP mapping:

- 401: `AUTHENTICATION_REQUIRED`, `PRINCIPAL_INVALID`
- 403: `MEMBERSHIP_INACTIVE`, `TENANT_SCOPE_MISMATCH`, `COMPANY_SCOPE_DENIED`, `ROLE_NOT_AUTHORIZED`, `ASSURANCE_TOO_LOW`, `MATERIALITY_LIMIT_EXCEEDED`
- 400: `REASON_REQUIRED`
- 404: `MEMORY_NOT_FOUND`
- 409: `INVALID_TRANSITION`, `ENVELOPE_MISMATCH`, `ALREADY_DECIDED`, `IDEMPOTENCY_CONFLICT`

All use `{"error":{"code","message",...details}}`. Only `ENVELOPE_MISMATCH` adds `expectedEnvelopeHash` and `actualEnvelopeHash`. MCP domain failures remain a successful JSON-RPC `tools/call` response with `isError:true`, but text contains the same JSON error object; malformed argument shape remains JSON-RPC `-32602` (`internal/server/mcp.go:503-517,1040-1049`).

## 7. TypeScript mirror and golden protocol

- `core/types.ts`: add role/auth/principal snapshot, command, decision, event, result, and frozen error-code types; keep existing hash algorithm unchanged.
- New `auth/principal.ts`: closure/private-symbol factory mirroring verified construction; tests receive assertions through a test authenticator, not a public principal constructor.
- New `authz/approval-policy.ts`: exact pure policy and canonical role ordering.
- `lifecycle/transitions.ts`: retain deprecated `approve(memory, meta)` and add async `approveMemory(command, principal, store, policy)`.
- `store/memory-store.ts`: extend the in-memory mirror with atomic/synchronous critical-section semantics, approval events, idempotency map, principal snapshots, and H1/H2 recomputation. It is a semantic mirror, not a SQLite concurrency proof.
- Extend both shared harnesses (`internal/core/golden_test.go`, `core/__tests__/golden.test.ts`) to dispatch legacy hash vectors or v0.4 approval-policy vectors by `contract` field.
- Add six files under `testdata/golden/`: `authorized-controller-closing.json`, `unauthorized-accountant-closing.json`, `cross-tenant-principal.json`, `stale-envelope.json`, `approved-result-envelope.json`, `canonical-principal-role-order.json`.

## 8. Seed and tests

Add `drenyra-engram auth seed-local-dev --db ... --tenant ... --company ... --subject ... --roles ...`. It requires `DRENYRA_ENV=local_dev`, inserts company/membership/roles plus one expiring local-dev session, prints the raw token once, and stores only its hash. Production mode rejects the command.

Test helpers call `store.SeedIdentity` directly on temporary databases; they never depend on environment state. Implement the 20 specified tests across `internal/auth`, `internal/authz`, `internal/store/approval_test.go`, `internal/server/http_approval_test.go`, `internal/server/mcp_test.go`, CLI tests, and TS mirror tests. Concurrency tests use two independently opened stores against one WAL database so `MaxOpenConns(1)` cannot create a false pass.

## 9. Executable batches preserving commit order

### Batch A — identity and policy (commits 1-3)

1. `feat(auth): add verified approval principal contracts` — `internal/auth/types.go`, `internal/auth/errors.go`, `core/types.ts`, tests.
2. `feat(auth): derive principal from request context` — `internal/auth/resolver.go`, `internal/auth/session_store.go`, `internal/server/auth_context.go`, auth tests.
3. `feat(authz): add versioned approval policy` — `internal/authz/approval_policy.go`, `authz/approval-policy.ts`, policy tests. Gate: auth and policy unit tests plus no public arbitrary-principal constructor.

### Batch B — persistence and atomic domain flow (commits 4-5)

1. `feat(store): add approval events and idempotency storage` — `internal/store/store.go`, `internal/store/migration_v3_test.go`, `internal/store/approval_test.go`, `store/memory-store.ts`.
2. `feat(core): approve atomically against expected envelope` — `internal/core/approval.go`, `internal/server/approval_service.go`, `internal/core/lifecycle.go` deprecation, `lifecycle/transitions.ts`, tests. Gate: migration rollback, two-store concurrency, H1/H2, timeout/replay, and all integrity tests.

### Batch C — authenticated surfaces (commits 6-8)

1. `feat(api): remove caller-supplied actor authority` — `internal/server/http.go`, `internal/server/api.go`, `internal/server/http_approval_test.go`.
2. `feat(cli): add authenticated sessions` — `cmd/drenyra-engram/main.go`, `internal/auth/local_dev.go`, CLI tests.
3. `feat(mcp): require authenticated session binding` — `internal/server/mcp.go`, MCP tests; update `engineVersion` from stale `0.2.0` (`internal/server/mcp.go:51-54`). Gate: external principal fields rejected and unauthenticated HTTP/MCP/CLI fail closed.

### Batch D — parity and freeze (commits 9-10)

1. `test(protocol): add approval principal golden cases` — six golden files and both shared harnesses.
2. `docs: freeze authenticated approval contracts` — ADR-003 update, contracts, this document, release notes. Gate: Go tests, TS tests/typecheck, race/concurrency suite, golden parity.

## 10. Risks and explicit non-goals

- `MaxOpenConns(1)` is not a cross-process lock; only `BEGIN IMMEDIATE` closes that race.
- Current linked evidence/rules are merged at read (`internal/store/store.go:1074-1082`) while the stored envelope hash is not recomputed. Approval must use a fresh transaction snapshot and link writes must refresh the cache.
- `transition_log` has no unique constraint. Do not retrofit one in v3; approval uniqueness belongs to the new event table.
- Cross-tenant checks intentionally return `TENANT_SCOPE_MISMATCH` but never title/content/RUC. Logging must also avoid command bodies and credentials.
- `Validity`/vigencia is not in `ComputeEnvelopeHash`; the current hash covers timestamps but not `Validity` (`internal/core/types.go:441-459`). Step 1 freezes v0.3 hash bytes, so vigencia changes cannot trigger `ENVELOPE_MISMATCH`. Fixing that requires a separately versioned envelope-hash contract and regenerated legacy goldens.
- Segregation of duties, blocked periods, dual approval, MFA/ACR elevation, signed service assertions, and Ed25519 receipts remain later steps; ADR-003 lists them but the binding Step 1 does not define their data/policy contracts. OIDC itself is no longer deferred: the stateless access-token slice (RS256, exact issuer/audience, DB membership/scope cross-check, standard assurance only) is implemented — [oidc-access-token-identity.md](oidc-access-token-identity.md).

## Resolved decisions (user-confirmed, 2026-08-05)

1. **Materiality**: declared `materialityLevel` field (`normal|material|critical`) set by the writing agent; policy classifies by level, never by reinterpreting the `Materiality *int64` threshold. `MATERIALITY_LIMIT_EXCEEDED` fires when the memory's level requires a role above the principal's.
2. **Legacy HTTP route**: `POST /v1/observations/{id}/approve` stays compiled but disabled by default in the daemon; removed in v0.5.0. Consumers get a migration window.
3. **Service assertions**: opaque high-entropy stored bearer credentials in `sessions` with `authentication_method='service_assertion'`; no self-declared JWT claims until a signed-assertion trust contract exists.
