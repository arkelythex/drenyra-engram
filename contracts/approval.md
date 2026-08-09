# Contract — Approval (v0.4.0 Step 1, frozen)

> Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
> no float is ever used for money; version/sequence numbers are JSON integers,
> never floats.
>
> Status: frozen for v0.4.0 Step 1 (ApprovalPrincipal autenticado). Applies to
> the Go engine and the TypeScript semantic mirror. Related: ADR-003,
> contracts/lifecycle.md (the human approval gate), contracts/provenance.md.
>
> Updated 2026-08-08 (unreleased slice): `oidc` is now resolvable — stateless
> RS256 access-token validation with exact issuer/audience and a DB
> membership/scope cross-check; assurance stays `standard`. Limits and
> configuration: [docs/architecture/oidc-access-token-identity.md](../docs/architecture/oidc-access-token-identity.md).

## Invariant

An approval is authorized by an AUTHENTICATED, PRE-VERIFIED principal derived
from a session, membership and roles — never from caller-declared fields. The
transport payload (HTTP body, MCP arguments, CLI flags) carries only the
command; `actorKind`, `subjectId` and `roles` in a payload are rejected.

## Command

```ts
interface ApproveMemoryCommand {
  memoryId: string;
  expectedEnvelopeHash: string; // the envelope hash the reviewer actually saw
  reason: string;               // required, non-whitespace (REASON_REQUIRED)
  requestId: string;            // idempotency key scoped to (tenant, requestId)
}
```

The principal is passed as a separate, verified argument:

```ts
approveMemory(command, authenticatedPrincipal)
```

## Principal

```ts
interface VerifiedApprovalPrincipal {
  subjectId: string;
  tenantId: string;
  membershipId: string;
  companyScopes: string[];
  roles: AccountingRole[]; // accountant | senior_accountant | controller | tax_reviewer | authorized_tax_professional
  authenticationMethod: "oidc" | "session" | "service_assertion" | "local_dev";
  assuranceLevel: "low" | "standard" | "strong";
  authenticatedAt: string; // RFC3339
  sessionId?: string;
}
```

Constructed only inside the authentication module (`Resolver.Authenticate`).
No public arbitrary-input constructor. `oidc` resolves through the stateless
access-token slice: RS256 JWT validation against the exact configured
issuer/audience, then a DB membership/scope cross-check; assurance is always
`standard` (no ACR/MFA elevation). Limits: no token revocation beyond DB
membership, no ID tokens, no browser flows — see
[docs/architecture/oidc-access-token-identity.md](../docs/architecture/oidc-access-token-identity.md).
`service_assertion` is an
opaque stored bearer credential (SHA-256 hash in `sessions`); no self-declared
JWT claims.

## Result

```ts
interface ApprovalResult {
  memoryId: string;
  approvalEventId: string;
  previousStatus: "pending_review";
  currentStatus: "approved";
  reviewedEnvelopeHash: string;   // H1 — what was examined
  resultingEnvelopeHash: string;  // H2 — after status → approved (always ≠ H1)
  principalSubjectId: string;
  membershipId: string;
  policyVersion: string;          // approval-policy/v0.4.0
  approvedAt: string;
  idempotentReplay: boolean;      // true when replayed from a completed reservation
}
```

## Atomic transition

One SQLite `BEGIN IMMEDIATE` transaction (write intent before race-sensitive
reads):

1. resolve idempotency by (tenant, requestId) — replay or conflict
2. locked re-read of the memory + current evidence/rule refs
3. scope checks (tenant, company; institutional memories cannot be approved)
4. status must be `pending_review`
5. recompute H1 FRESH and compare with `expectedEnvelopeHash`
6. run the versioned policy in-transaction
7. guarded status flip to `approved` + recompute H2
8. insert immutable `approval_events` row + legacy transition row
9. complete the idempotency reservation
10. COMMIT

Concurrency: exactly one transition. Loser → `ALREADY_DECIDED`. Same
requestId + same payload → replay (`idempotentReplay: true`, no new event).
Same requestId + different payload/principal → `IDEMPOTENCY_CONFLICT`.

## Event (immutable)

```ts
interface ApprovalEvent {
  id: string;
  requestId: string;
  memoryId: string;
  tenantId: string;
  companyId: string;
  fiscalPeriodId?: string;
  action: "approved";
  fromStatus: "pending_review";
  toStatus: "approved";
  reviewedEnvelopeHash: string;
  resultingEnvelopeHash: string;
  reason: string;
  principalSnapshot: {
    subjectId: string;
    membershipId: string;
    roles: AccountingRole[]; // canonical set: sorted + deduplicated
    authenticationMethod: string;
    assuranceLevel: string;
    authenticatedAt: string;
  };
  policyVersion: string;
  authorizationReasonCode: "AUTHORIZED";
  createdAt: string;
}
```

Never stored: access tokens, refresh tokens, cookies, session secrets,
irrelevant claims. The snapshot lets audit reconstruct the authority used at
decision time even after memberships/roles change.

## Policy (approval-policy/v0.4.0)

Pure, deterministic, versioned. Base role by fiscal effect:

| Effect               | Minimum role                |
| -------------------- | --------------------------- |
| journal_entry        | accountant                  |
| adjustment           | accountant                  |
| reclassification     | accountant                  |
| approval             | senior_accountant           |
| closing              | controller                  |
| declaration          | tax_reviewer                |
| sunat_filing         | authorized_tax_professional |
| (none / other)       | denied                      |

Materiality (declared `materialityLevel`, set by the writing agent) raises the
accounting ladder: `material` → `senior_accountant`, `critical` → `controller`.
Role dominance applies ONLY within the accounting ladder
(accountant < senior_accountant < controller); tax roles are explicit and never
implied. Minimum assurance `standard`; `sunat_filing` additionally `strong`.

Check order (first frozen code wins): tenant → company scope → membership
active → role → assurance → materiality. Reason codes:
`AUTHORIZED | TENANT_SCOPE_MISMATCH | COMPANY_SCOPE_DENIED |
MEMBERSHIP_INACTIVE | ROLE_NOT_AUTHORIZED | ASSURANCE_TOO_LOW |
MATERIALITY_LIMIT_EXCEEDED`.

## Error codes (transport-independent)

```text
AUTHENTICATION_REQUIRED  PRINCIPAL_INVALID         MEMBERSHIP_INACTIVE
TENANT_SCOPE_MISMATCH    COMPANY_SCOPE_DENIED      ROLE_NOT_AUTHORIZED
ASSURANCE_TOO_LOW        MATERIALITY_LIMIT_EXCEEDED REASON_REQUIRED
MEMORY_NOT_FOUND         INVALID_TRANSITION        ENVELOPE_MISMATCH
ALREADY_DECIDED          IDEMPOTENCY_CONFLICT
```

`ENVELOPE_MISMATCH` carries ONLY `expectedEnvelopeHash` + `actualEnvelopeHash`;
memory content never appears in approval errors.

## Surfaces

- HTTP: `POST /accounting/memories/{memoryId}/approve` — `Authorization:
  Bearer`, `Idempotency-Key: approval-...`, strict body `{expectedEnvelopeHash,
  reason}` (`DisallowUnknownFields`). Legacy `/v1/observations/{id}/approve`
  compiled but disabled by default; removed in v0.5.0.
- MCP: `accounting_approve(memory_id, expected_envelope_hash, reason,
  request_id)` — requires an authenticated session binding; stdio fails closed
  `AUTHENTICATION_REQUIRED`.
- CLI: `auth login` → `approve <id> --expected-envelope <hash> --reason "..."`;
  `--actor` removed. `auth seed-local-dev` requires `DRENYRA_ENV=local_dev`.
