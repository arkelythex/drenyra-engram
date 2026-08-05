# ADR-003 — Approval must derive from an authenticated principal, not a declared claim

> Status: accepted (direction) · Applies from: v0.4.0 · Updated: 2026-08-05

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

The engine keeps recording the *claim* (`actorKind`, `actorId`) — the kernel
is memory, not an identity provider. The *authorization* derives from an
`ApprovalPrincipal` built by the gateway (Drenyra authz) and passed to the
domain, never assembled from caller-declared fields:

```typescript
interface ApprovalPrincipal {
  subjectId: string;            // authenticated subject
  tenantId: string;             // tenant of the session
  companyMembershipId: string;  // active membership
  roles: AccountingRole[];      // accountant, senior, controller, tax pro…
  authenticationMethod: string; // how the session was proven
  authenticatedAt: string;
}
```

The domain API then receives `approveMemory(memoryId, principal, reason)`
instead of `approveMemory(memoryId, { actorKind: "human" })`.

Minimum checks (enforced by the gateway, surfaced by the engine):

- principal authenticated
- membership active
- tenant matches the memory scope
- access to the memory's companyId
- role allowed for the operation
- segregation of duties
- period not blocked, or explicit exceptional permission
- documented reason

Future policies (Phase 4 / later):

- `adjustment < 5,000` → accountant
- `adjustment >= 5,000` → senior accountant
- `closing` → controller
- `sunat_filing` → authorized tax professional
- high materiality → dual approval

## Consequences

- The kernel's gate remains the last line of defense; the gateway becomes the
  first.
- `Source.ActorKind` continues to record the claim for provenance; it never
  *proves* authorization.
- The `requireExplicitActor` fail-closed default (v0.3.0) stays: an omitted or
  invalid `actorKind` is rejected, so the gate never opens by omission.

## Reference

- contracts/lifecycle.md (the human approval gate)
- contracts/provenance.md rule 5 (human acts are marked human)
