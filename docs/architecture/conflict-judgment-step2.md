# v0.4.0 Step 2 — Adjudicable conflicts / AccountingJudgment

Status: integration design, implementation-ready (user-confirmed decisions, 2026-08-05: confirmation and approval are SEPARATE acts; minimum adjudicator role senior_accountant+).  
Scope: Go engine and TypeScript semantic mirror. This step does not issue receipts and does not change v0.3 memory hash bytes.

## 1. Binding decisions

- `AccountingJudgment` is a first-class entity, not a `KindDecision` memory. A judgment is an adjudication act over two immutable observations; it is not an observation itself.
- Agents/systems may propose and withdraw their own proposals. Their `Source` is provenance only and never authority. Only an `auth.VerifiedApprovalPrincipal`, resolved from a session as in Step 1, may confirm or reject.
- Legal states are `proposed | confirmed | rejected | withdrawn | superseded`.
  - `proposed -> confirmed | rejected | withdrawn | superseded`
  - `confirmed -> superseded` only, and only while atomically confirming its correction
  - `rejected`, `withdrawn`, and `superseded` are terminal
- Confirmation requires a non-empty human resolution. The proposal reason is not silently promoted into a professional resolution.
- A correction is a new proposed judgment with `predecessorId`. Confirming it atomically confirms the successor and changes the predecessor from `confirmed` to `superseded`, sets predecessor `supersedesId = successor.id`, and records predecessor `supersedes` successor. This intentionally follows `SupersedeExplicit`/`SuccessorOf` routing (`internal/store/store.go:1849-1925`).
- Confirming a judgment inserts the proposed observation relation as a compatibility projection. The judgment row remains authoritative; legacy relation rows alone never prove confirmation.
- Judgment confirmation and memory approval are separate acts. Confirming a judgment concerning a `pending_review` memory does not approve that memory.
- Policy is frozen as `judgment-policy/v0.4.0`: minimum `senior_accountant` (or `controller` by ladder dominance), minimum assurance `standard`, active membership, exact tenant, and access to the common company of both observations.

## 2. Go contracts and package boundaries

Add `internal/core/judgment.go`:

```go
type JudgmentStatus string // proposed|confirmed|rejected|withdrawn|superseded

type AccountingJudgment struct {
    ID, TenantID, CompanyID, FiscalPeriodID string
    FromID, ToID string
    Relation Relation
    Status JudgmentStatus
    Proposer Source                 // ActorKind agent|system; provenance only
    ProposalReason string
    Resolution string               // empty until confirmed/rejected
    Adjudicator *auth.PrincipalSnapshot
    PolicyVersion string            // empty until authenticated decision
    PredecessorID string             // correction target declared by successor
    SupersedesID string              // successor routing stored on old row
    ProposedAt, UpdatedAt, DecidedAt string
}

type ProposeJudgmentCommand struct {
    FromID, ToID string; Relation Relation; Reason, RequestID string
    PredecessorID string
}
type ConfirmJudgmentCommand struct {
    JudgmentID, Resolution, ExpectedJudgmentHash, RequestID string
}
type RejectJudgmentCommand struct {
    JudgmentID, Reason, ExpectedJudgmentHash, RequestID string
}
type WithdrawJudgmentCommand struct { JudgmentID, RequestID string }
```

`ProposeJudgment(ctx, command, callerSource)` accepts only `agent|system`; `callerSource` is separate from the command. `ConfirmJudgment` and `RejectJudgment` receive `auth.VerifiedApprovalPrincipal` separately. No command has subject, membership, role, actor-kind, or assurance fields.

Add `ComputeJudgmentHash` using canonical JSON and SHA-256. The reviewed hash covers `id`, scope, pair, relation, `status=proposed`, canonical proposer, proposal reason, predecessor id, and proposed timestamp; it excludes mutable routing and `updatedAt`. The resulting confirmed hash additionally covers resolution, canonical adjudicator snapshot, policy version, status, and decided timestamp. Confirmation/rejection compare a freshly recomputed proposed hash with `ExpectedJudgmentHash` before policy evaluation.

Add `internal/server/judgment_service.go` for syntax validation and orchestration. Persistence and race-sensitive checks belong to one store operation, never `FindJudgment` followed by a mutation.

## 3. Proposal semantics

Only `supports`, `contradicts`, `explains`, `reconciles`, `reverses`, and `supersedes` are proposable (`internal/core/types.go:312-360`). `conflicts_with` remains a legacy sync/discovery marker: it can motivate a proposal, but is neither accepted as a proposal relation nor removed automatically.

Proposal validation:

1. Require distinct existing `fromId` and `toId` observations.
2. Require both observations in the same tenant and company. Cross-period pairs are allowed; `fiscalPeriodId` is set only when periods match, otherwise NULL.
3. Require proposer `ActorKind` to be `agent|system`, a non-empty reason/request id, and one of the six relations.
4. If an identical confirmed judgment exists, return `JUDGMENT_CONFLICT`, unless this proposal names it as `predecessorId`.
5. Permit different relations for the same pair: apparent disagreement is precisely what adjudication must preserve.
6. Permit only one open proposal for `(tenant, company, from, to, relation)`. A same-request retry replays; another request returns `JUDGMENT_CONFLICT` rather than silently deduplicating authorship.
7. A predecessor must concern the same pair and relation. A confirmed predecessor remains current until the correction is confirmed. A proposed predecessor may be superseded immediately only by the same proposer identity (`system`, `actorId`, `actorKind`, `session`).

Withdrawal uses the same exact proposer identity and only from `proposed`; mismatch is `PROPOSAL_UNAUTHORIZED`. Because `Source` is a claim, this is provenance continuity and availability control, never professional authorization.

## 4. Schema v4 and migration

Raise `schemaVersion` from 3 to 4. Fresh databases still traverse the existing additive migration chain. Run v3->v4 in one transaction and update `schema_meta` last; any DDL/data error rolls back and startup fails closed.

```sql
CREATE TABLE judgments (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL, company_id TEXT NOT NULL, fiscal_period_id TEXT,
  from_id TEXT NOT NULL REFERENCES observations(id),
  to_id TEXT NOT NULL REFERENCES observations(id),
  relation TEXT NOT NULL CHECK(relation IN
    ('supports','contradicts','explains','reconciles','reverses','supersedes')),
  status TEXT NOT NULL CHECK(status IN
    ('proposed','confirmed','rejected','withdrawn','superseded')),
  proposer_system TEXT NOT NULL, proposer_actor_id TEXT NOT NULL DEFAULT '',
  proposer_actor_kind TEXT NOT NULL CHECK(proposer_actor_kind IN ('agent','system')),
  proposer_session TEXT NOT NULL DEFAULT '', proposal_reason TEXT NOT NULL,
  resolution TEXT, policy_version TEXT,
  adjudicator_subject_id TEXT, adjudicator_membership_id TEXT REFERENCES memberships(id),
  adjudicator_roles_json TEXT, authentication_method TEXT, assurance_level TEXT,
  principal_authenticated_at TEXT,
  predecessor_id TEXT REFERENCES judgments(id), supersedes_id TEXT REFERENCES judgments(id),
  proposed_at TEXT NOT NULL, updated_at TEXT NOT NULL, decided_at TEXT,
  CHECK(from_id <> to_id),
  CHECK((status='proposed') = (decided_at IS NULL)),
  CHECK(status NOT IN ('confirmed','rejected') OR adjudicator_subject_id IS NOT NULL),
  CHECK(adjudicator_subject_id IS NULL OR status IN ('confirmed','rejected','superseded')),
  CHECK(status NOT IN ('confirmed','rejected') OR
    (length(trim(resolution))>0 AND length(policy_version)>0))
);
CREATE UNIQUE INDEX uq_judgment_open_tuple
  ON judgments(tenant_id,company_id,from_id,to_id,relation) WHERE status='proposed';
CREATE INDEX idx_judgments_pair ON judgments(tenant_id,company_id,from_id,to_id,status);
CREATE INDEX idx_judgments_predecessor ON judgments(predecessor_id);
CREATE INDEX idx_judgments_successor ON judgments(supersedes_id);
```

Add `judgment_events(id, judgment_id, request_id, action, from_status, to_status, judgment_hash, principal_snapshot_json, policy_version, reason, created_at)` with action/check constraints and immutable update/delete triggers. Every transition writes one event; confirm/reject events carry the principal snapshot. Add `judgment_idempotency_keys(tenant_id,request_id,command_hash,actor_binding,judgment_id,result_json,judgment_event_id,created_at,completed_at)` with primary key `(tenant_id,request_id)`.

Add `judgment_relations(from_judgment_id,to_judgment_id,relation,actor,timestamp)` with `relation='supersedes'`, primary key on the pair, and FKs to judgments. `JudgmentSuccessorOf` reads this table; do not put judgment IDs into the observation `relations` table.

Triggers:

- all `judgment_events` updates/deletes abort `IMMUTABLE_JUDGMENT_EVENT`;
- all judgment deletes abort `IMMUTABLE_JUDGMENT`;
- an update of a `confirmed` row is allowed only for `status confirmed->superseded`, setting a previously empty `supersedes_id`, while every proposal/adjudication field remains byte-equal; every other update aborts `IMMUTABLE_JUDGMENT`;
- updates of `rejected|withdrawn|superseded` rows abort `IMMUTABLE_JUDGMENT`.

The v0.3 `API.Judge(conflictID,resolution,actor)` path (`internal/server/api.go:451-493`) is deprecated and fail-closed with `AUTHENTICATION_REQUIRED`; it no longer writes. Remove `accounting_judge` from the MCP catalog and dispatch. Existing `KindDecision` memories and `explains` relations remain readable and are not migrated or treated as authenticated judgments. Remove the deprecated Go method in v0.5.0.

## 5. Atomic authenticated adjudication

Add `SQLiteStore.ConfirmJudgment` and `RejectJudgment` using a dedicated connection and literal `BEGIN IMMEDIATE`, matching Step 1 rather than the deferred transaction pattern.

Confirmation transaction:

1. Resolve `(principal.tenantId, requestId)` in judgment idempotency. Different command or principal binding returns `IDEMPOTENCY_CONFLICT`; a completed match replays with `idempotentReplay=true`.
2. Reserve the key and read the judgment plus both observations on the same connection.
3. Require `proposed`, same stored pair/scope, and current hash equal to `expectedJudgmentHash`; otherwise return the typed error and roll back.
4. Run pure `JudgmentAuthorizationPolicy`: tenant, company, membership, role, assurance, in that order.
5. Require a non-empty resolution. Set confirmed fields from `PrincipalSnapshot`, policy version, and one captured UTC timestamp.
6. Insert the immutable confirm event and `INSERT ... SELECT ... WHERE NOT EXISTS` the compatibility observation relation. Its actor is the verified subject. Judgment queries still use judgments, not relation presence, as authority.
7. For a correction, require predecessor `confirmed`; atomically mark it superseded, set its `supersedes_id`, write its event, and insert the judgment supersedes relation.
8. Store the result, commit, and return.

Reject follows the same lock/hash/policy/idempotency path, stores the human reason as `resolution`, writes no observation relation, and becomes terminal. Two concurrent confirms serialize: exactly one confirms; the loser returns `INVALID_JUDGMENT_TRANSITION`, unless it carries the winner's identical request id and receives a replay.

## 6. Policy and errors

Add `internal/authz/judgment_policy.go` and `authz/judgment-policy.ts`. Version is exactly `judgment-policy/v0.4.0`. Minimum role is `senior_accountant`; `controller` dominates it, while tax roles do not. Minimum assurance is `standard`. Both observations must be company-scoped to the principal tenant and the same company, and that company must be in `companyScopes`. Check order is tenant -> company -> membership -> role -> assurance; first code wins.

Reuse Step 1 codes: `AUTHENTICATION_REQUIRED`, `PRINCIPAL_INVALID`, `MEMBERSHIP_INACTIVE`, `TENANT_SCOPE_MISMATCH`, `COMPANY_SCOPE_DENIED`, `ROLE_NOT_AUTHORIZED`, `ASSURANCE_TOO_LOW`, `MEMORY_NOT_FOUND`, and `IDEMPOTENCY_CONFLICT`.

Freeze new codes:

- `JUDGMENT_NOT_FOUND` (404)
- `RELATION_NOT_PROPOSABLE`, `RESOLUTION_REQUIRED` (400)
- `PROPOSAL_UNAUTHORIZED` (403)
- `INVALID_JUDGMENT_TRANSITION`, `JUDGMENT_CONFLICT`, `JUDGMENT_HASH_MISMATCH` (409)

Only `JUDGMENT_HASH_MISMATCH` returns details, limited to expected/actual hashes. Auth codes keep Step 1 HTTP mappings. Do not reuse `ENVELOPE_MISMATCH`: judgment hashes are a separately versioned contract.

## 7. Surfaces

HTTP, all strict with `DisallowUnknownFields`:

- `POST /accounting/judgments` — proposal body `{fromId,toId,relation,reason,predecessorId?,source}`; `Idempotency-Key` header.
- `POST /accounting/judgments/{id}/confirm` — authenticated body `{resolution,expectedJudgmentHash}`.
- `POST /accounting/judgments/{id}/reject` — authenticated body `{reason,expectedJudgmentHash}`.
- `POST /accounting/judgments/{id}/withdraw` — proposal source body only; no authority fields.

HTTP middleware supplies the principal to confirm/reject. Payload identity fields are rejected. Proposal `source` is explicitly provenance, not authority.

MCP replaces `accounting_judge` with `accounting_judgment_propose`, `accounting_judgment_confirm`, `accounting_judgment_reject`, and `accounting_judgment_withdraw`. Use strict argument decoding. Stdio may propose/withdraw but confirm/reject fail closed with `AUTHENTICATION_REQUIRED`; authenticated HTTP MCP binds the session principal.

CLI adds `judge propose`, `judge confirm`, `judge reject`, `judge withdraw`, and `judge show`. Confirm/reject load the existing 0600 auth session and reject `--actor`, `--subject`, or role flags. `--request-id` is optional only at the CLI adapter, which generates a UUID when absent.

## 8. TypeScript mirror and protocol parity

- `core/types.ts`: add the exact judgment statuses, entity, commands/results/events, hash input, and frozen errors.
- `store/memory-store.ts`: add judgment maps, events, judgment idempotency, relation projection, and one promise-mutex critical section around propose/confirm/reject/withdraw. It is a semantic mirror, not SQLite concurrency proof.
- `lifecycle/transitions.ts`: add pure judgment transition predicates and orchestration; agent-confirm is impossible because confirm requires `VerifiedApprovalPrincipal`.
- Add `authz/judgment-policy.ts` with the exact Go matrix/check order.
- Extend both golden harnesses to dispatch `contract: "judgment"`. Vectors: `proposed-confirmed`, `agent-cannot-confirm`, `cross-tenant-adjudicator`, `superseded-judgment-corrects-confirmed`, and `immutable-confirmed`. Canonical fields and hashes must match Go/TS byte-for-byte.

## 9. Acceptance and concurrency tests

- agent proposal persists, but no agent-shaped command can call confirm;
- unauthenticated HTTP/MCP/CLI confirm returns `AUTHENTICATION_REQUIRED`;
- cross-tenant and out-of-company principals are denied without leaking observation content;
- confirmed resolution/principal/proposal fields cannot update or delete (`IMMUTABLE_JUDGMENT`);
- confirmed correction supersedes the predecessor and `JudgmentSuccessorOf(old)` returns the correction;
- confirmation inserts one compatibility observation relation; rejection inserts none;
- judgment confirmation does not change either observation status;
- identical request replay returns one event with `idempotentReplay=true`; changed payload/principal returns `IDEMPOTENCY_CONFLICT`;
- two independent stores confirming concurrently produce one confirmed transition and one loser/replay;
- v3->v4 rollback, trigger, partial-index, and old-memory readability tests pass;
- Go and TS golden suites produce identical hashes and transition outcomes.

## 10. Executable batches and commit order

### Batch A — domain and policy

1. `feat(core): add accounting judgment model and lifecycle`
2. `feat(authz): add versioned judgment policy`
3. `test(protocol): add judgment lifecycle golden vectors`

Gate: Go/TS lifecycle, policy, canonical-hash, and golden parity tests.

### Batch B — persistence and atomic adjudication

1. `feat(store): add schema v4 judgment persistence`
2. `feat(store): adjudicate judgments atomically`
3. `test(store): enforce immutable and concurrent judgments`

Gate: migration rollback, triggers, two-store concurrency, supersession routing, relation projection, and idempotency tests.

### Batch C — authenticated surfaces and freeze

1. `feat(api): add authenticated judgment services and routes`
2. `feat(mcp): replace caller-declared accounting judge tool`
3. `feat(cli): add authenticated judgment commands`
4. `docs: freeze adjudicable conflict contracts`

Gate: strict payload tests, unauthenticated/cross-scope failures, legacy path closure, full Go tests/race, TS tests/typecheck, and golden parity.

## 11. Risks and non-goals

- Existing observation `relations` have no authority metadata. They remain compatibility/read projections; only a confirmed judgment establishes adjudicated meaning.
- Agent `Source` can be spoofed by an untrusted transport. It may create/withdraw proposals but can never confirm/reject or supersede a confirmed judgment.
- Receipt signing, offline verification, segregation of duties, dual control, relation removal, and migration of legacy decision memories are later steps.
