# ADR-003 — Approval must derive from an authenticated principal, not a declared claim

> Status: accepted (direction) · Applied: v0.4.0 Step 1 (ApprovalPrincipal autenticado) · Updated: 2026-08-05

## Context

The v0.3.0 kernel enforces the mandatory human-approval gate through
`source.actorKind == "human"`. This is **necessary but not sufficient**: the
field arrives in the request, so a caller can send `{ "actorKind": "human",
"actorId": "fake-user" }` and pass the gate if the transport trusts the claim.

The invariant must move from:

> Solo un humano aprueba. (Only a human approves.)

to:

> Solo un humano autenticado, autorizado y dentro del alcance correcto puede
> aprobar. (Only an authenticated, authorized human within the correct scope
> can approve.)

## Decision

The engine keeps recording the *claim* (`Source.ActorKind`, `ActorID`) for
provenance — the kernel is memory, not an identity provider. The
*authorization* derives from a `VerifiedApprovalPrincipal` built by the
authentication module from an authenticated session, membership and roles, and
passed to the domain as a SEPARATE argument — never assembled from
caller-declared fields.

The transport payload can never declare authority:

```text
approveMemory({ memoryId, reason, expectedEnvelopeHash, requestId }, authenticatedPrincipal)
```

The external JSON (HTTP body, MCP tool arguments, CLI flags) accepts only the
command fields; `actorKind`, `subjectId` and `roles` in a payload are rejected
(or, on strict surfaces, refused by `DisallowUnknownFields`), never honored.

### Frozen v0.4.0 Step 1 contracts

`VerifiedApprovalPrincipal` (constructed only inside `internal/auth`, via the
`Resolver.Authenticate` factory; no public arbitrary-input constructor):

```typescript
interface VerifiedApprovalPrincipal {
  subjectId: string;
  tenantId: string;
  membershipId: string;
  companyScopes: string[];
  roles: AccountingRole[];
  authenticationMethod: "oidc" | "session" | "service_assertion" | "local_dev";
  assuranceLevel: "low" | "standard" | "strong";
  authenticatedAt: string;
  sessionId?: string;
}
```

Command and result:

```typescript
interface ApproveMemoryCommand {
  memoryId: string;
  expectedEnvelopeHash: string; // the hash the human actually reviewed
  reason: string;               // required, non-whitespace
  requestId: string;            // idempotency key scoped to (tenant, requestId)
}

interface ApprovalResult {
  memoryId: string;
  approvalEventId: string;
  previousStatus: "pending_review";
  currentStatus: "approved";
  reviewedEnvelopeHash: string;   // H1 — what was examined
  resultingEnvelopeHash: string;  // H2 — after status → approved
  principalSubjectId: string;
  membershipId: string;
  policyVersion: string;
  approvedAt: string;
  idempotentReplay: boolean;
}
```

Status participates in the envelope hash, so approving changes it: the
immutable `approval_events` row records BOTH hashes — `reviewedEnvelopeHash`
(H1, what the human examined) and `resultingEnvelopeHash` (H2, the new state).
An `ENVELOPE_MISMATCH` fails the approval when the current envelope differs
from the expected one (evidence/rule linked after review, status changed, …)
and carries ONLY the two hashes, never memory content.

The atomic transition (one SQLite `BEGIN IMMEDIATE` transaction): resolve
idempotency by (tenant, requestId) → locked re-read of the memory + current
refs → scope checks → status `pending_review` → fresh H1 recompute vs expected
→ pure policy → guarded status flip + H2 → immutable approval event + legacy
transition row → completed idempotency reservation → COMMIT. Two concurrent
approvals produce exactly ONE transition (the loser returns
`ALREADY_DECIDED`); a retry with the same requestId + payload replays the
committed result (`idempotentReplay: true`) without a second event.

Authorization is a versioned, deterministic policy (`internal/authz`,
`approval-policy/v0.4.0`) — pure, no DB/clock/token/provider access:

| Fiscal effect      | Minimum role                |
| ------------------ | --------------------------- |
| `journal_entry`    | `accountant`                |
| `adjustment`       | `accountant`                |
| `reclassification` | `accountant`                |
| `approval`         | `senior_accountant`         |
| `closing`          | `controller`                |
| `declaration`      | `tax_reviewer`              |
| `sunat_filing`     | `authorized_tax_professional` |

A declared `materialityLevel` (`normal | material | critical`, set by the
writing agent) raises the accounting ladder: material → `senior_accountant`,
critical → `controller`. Minimum assurance is `standard`; `sunat_filing`
additionally requires `strong`. Frozen reason codes (first check wins, for
reproducibility): tenant → company scope → membership active → role →
assurance → materiality.

Transport-independent error codes (frozen):

```text
AUTHENTICATION_REQUIRED  PRINCIPAL_INVALID         MEMBERSHIP_INACTIVE
TENANT_SCOPE_MISMATCH    COMPANY_SCOPE_DENIED      ROLE_NOT_AUTHORIZED
ASSURANCE_TOO_LOW        MATERIALITY_LIMIT_EXCEEDED REASON_REQUIRED
MEMORY_NOT_FOUND         INVALID_TRANSITION        ENVELOPE_MISMATCH
ALREADY_DECIDED          IDEMPOTENCY_CONFLICT
```

Surfaces:

- **HTTP** `POST /accounting/memories/{memoryId}/approve`: `Authorization:
  Bearer` + `Idempotency-Key` header; strict body `{expectedEnvelopeHash,
  reason}` (`DisallowUnknownFields` rejects any authority field). Principal
  derived from the bearer, never the body.
- **MCP** `accounting_approve` (`memory_id`, `expected_envelope_hash`, `reason`,
  `request_id`): allowed only with an authenticated session binding; the stdio
  server has none, so it fails closed with `AUTHENTICATION_REQUIRED`.
- **CLI** `auth login` (token validated, stored user-only 0600) then `approve
  <id> --expected-envelope <hash> --reason "..."`; caller-supplied `--actor`
  authority is removed. `auth seed-local-dev` is explicit, isolated, rejected
  in production.

## Consequences

- The kernel's gate remains the last line of defense; authentication +
  authorization become the first.
- `Source.ActorKind` continues to record the claim for provenance; it never
  *proves* authorization.
- The `requireExplicitActor` fail-closed default (v0.3.0) is superseded by
  strict payload parsing and the authenticated principal: an omitted or
  invalid identity is rejected, so the gate never opens by omission.
- Approval events are immutable (SQLite triggers) and keep the principal
  SNAPSHOT used for the decision (roles canonicalized as a set), so later role
  changes never alter historical audit.
- Idempotency by (tenant, requestId) makes a retry after a lost response safe:
  no duplicate approval event.
- OIDC, signed service assertions, Ed25519 receipts and offline verification
  remain later v0.4.0 steps (ADR-003 direction applies; their data/policy
  contracts are not frozen here). Adjudicable conflicts ARE frozen in Step 2
  (`AccountingJudgment`, contracts/judgment.md): agents propose, authenticated
  principals confirm/reject, a confirmed judgment is immutable and corrections
  supersede it — the same never-caller-declared-authority invariant applies to
  adjudication.

## Reference

- `docs/architecture/approval-principal-step1.md` — integration design
  (schema v3, atomic transaction, surfaces, batches)
- `internal/auth` (principal factory + resolver), `internal/authz`
  (policy v0.4.0), `internal/store` (approval_events, idempotency_keys),
  `internal/server` (ApproveMemory service + HTTP route)
- `contracts/approval.md` — frozen approval contract
- contracts/lifecycle.md (the human approval gate)
- contracts/provenance.md rule 5 (human acts are marked human)
