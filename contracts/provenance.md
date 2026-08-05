# Contract: provenance

> Version: 0.2 · Status: frozen-for-0.2 · Transport-agnostic.

Defines **audit metadata** and the **non-authorization boundary** — who
recorded what, when, why, and what memory is allowed (and not allowed) to do.

## Source (v2 structured provenance)

Every memory records its provenance at creation as a structured `Source`:

| Field       | Description                                                       |
| ----------- | ----------------------------------------------------------------- |
| `system`    | REQUIRED — which system produced the event (`drenyra-core`, `sire`, `manual`, …) |
| `reference` | Optional external reference (`F001-948`, `AJ-2026-07-019`)         |
| `actorId`   | Who (user/agent/system id); REQUIRED for human actors             |
| `actorKind` | `human` \| `agent` \| `system` — REQUIRED                          |
| `model`     | Agent model, when the actor is an agent                           |
| `session`   | Session identifier (agent continuity)                             |

`recordedAt` (the engine clock at write) is the immutable creation time.
`effectiveAt` (accounting date) and `observedAt` (detection date) are domain
timestamps, not provenance — they describe the EVENT, not the record.

## Rules

1. **Written at creation.** Source and `recordedAt` are captured when the
   memory is written; they are not editable afterward.
2. **Corrections are new memories.** A mistaken memory is superseded, never
   rewritten in place.
3. **Auditable end to end.** Every state in [lifecycle](lifecycle.md) traces
   to an actor, actor kind and timestamp (audit trail).
4. **Provenance is not identity.** Provenance records the writer; content
   identity is derived from the canonical content hash + scope.
5. **Human acts are marked human.** The approval gate requires
   `actorKind == human`; a machine cannot forge a human approval.

## Non-authorization boundary

**Memory informs decisions. It never authorizes them.**

```text
La IA asiste.  La política restringe.  La evidencia demuestra.
El receipt certifica.  El profesional autoriza.  La memoria orienta.
```

- The engine has NO authorize/allow/execute/file/pay/declare operation.
- `approve`/`reject` in v2 are the PROFESSIONAL REVIEW of a memory
  (the human gate on a `pending_review` memory) — approval of a MEMORY, never
  authorization of a business action. The professional authorizes OUTSIDE the
  engine; the engine records the decision with full provenance.
- `receiptId` references the Ed25519 receipt issued by the Drenyra ecosystem;
  this engine never signs.

## Frozen semantics (v0.2) — migration policy

> The bullets in this section are **frozen-for-0.2**: normative, enforced by
> the conformance suite, and carried into every storage adapter. They bind
> every adapter, including the PostgreSQL authority (ADR-002).

1. **Additive, reversible migrations.** Store layout is versioned
   (schema_version = 2); migrations ALTER additively and never rewrite or
   delete history. A store with an unsupported version fails closed.
2. **Corruption fails closed.** Checksums and schema guards detect corruption;
   silent repair is forbidden.
3. **The v1→v2 migration is lossless.** Legacy `type` maps to `kind`
   (decision/judgment→decision, policy/pattern/config/preference→rule,
   discovery/bugfix→fact, architecture→summary, default→fact), legacy
   `authority_status` maps to `status` (promoted→approved,
   superseded→superseded, otherwise→active — migrated history is informative
   and never blocked by the gate), and the flat provenance is re-encoded into
   `source_json`. Original v1 columns are preserved for historical reads.

## Conformance

Vectors cover: canonical provenance serialization, source validation (human
actors carry `actorId`), the non-authorization boundary (no authorize/allow/
execute operation on any surface), the human-approval gate
(`GATE_REQUIRES_HUMAN` on machine approval), migration mapping, and the
fail-closed corruption paths.
