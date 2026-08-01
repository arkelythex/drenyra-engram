# Contract: provenance

> Version: 0.1-draft · Status: draft · Transport-agnostic.

Defines **audit metadata** and the **non-authorization boundary** — who recorded what, when, why, and what memory is allowed (and not allowed) to do.

## Provenance

Every observation records at creation:

| Field       | Description                                           |
| ----------- | ----------------------------------------------------- |
| `actor`     | Agent, user, or system that wrote it                  |
| `timestamp` | UTC creation time                                     |
| `source`    | Origin surface/session (CLI, MCP, HTTP, mission, …)   |
| `session`   | Session identifier (if any)                           |
| `reason`    | Why this observation exists                           |

## Rules

1. **Written at creation.** Provenance is captured when the observation is created; it is not editable afterward.
2. **Corrections are new observations.** A mistaken observation is superseded, never rewritten in place.
3. **Auditable end to end.** Every state in [lifecycle](lifecycle.md) can be traced to an actor and timestamp.
4. **Provenance is not identity.** Provenance records the writer; content identity is derived from content + scope.

## The non-authorization boundary

**Memory never authorizes operations.**

```text
Memoria orienta.      Memory guides.
Política restringe.   Policy restricts.
Evidencia demuestra.  Evidence demonstrates.
Receipt certifica.    Receipt certifies.
Profesional autoriza. A professional authorizes.
```

Consequences, enforced by contract:

1. No API in this engine returns an "approval" or "authorization" verdict.
2. No observation may be presented as permission to act.
3. Consumers (Drenyra, Drenyra AI, Drenyra Pi) route authorization through gates and human approval — never through memory.
4. A consumer that treats memory as authority violates this contract and the fiscal safety rules.

## Conformance

Vectors cover: provenance immutability, correction-via-supersede, audit tracing, and absence of any authorization surface in the public API.
