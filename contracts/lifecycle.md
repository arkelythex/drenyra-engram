# Contract: lifecycle

> Version: 0.5 · Status: frozen-for-0.5 · Transport-agnostic.

Defines the **memory lifecycle and vigencia** — how institutional accounting
memory matures, expires, and is approved. The v2 machine replaces the v1
`draft → reviewed → promoted → superseded` chain with an approval-gated model:
the fiscal effect of a memory decides whether it needs a HUMAN approval.
Since v0.5.0 the lifecycle also interacts with the **period closure gate**: an
approved monthly close (contracts/closing.md) blocks new period-scoped
mutations until an explicit controller reopen. The external
captura → clasificacion → conciliacion → cierre → declaracion → auditoria FSD
lifecycle of the Drenyra ecosystem is NOT encoded here; this contract defines
the per-memory state machine only.

## States

```text
save (fiscalEffect == none)   ──► active             (informative, current)
save (fiscalEffect != none)   ──► pending_review     (GATE: human approval)
pending_review ──approve(human)──► approved
pending_review ──reject(human)───► rejected           (terminal)
active | pending_review | approved ──void(human|system)──► voided   (terminal)
active | pending_review | approved ──supersede──► superseded          (terminal)
```

| State            | Meaning                                                                 |
| ---------------- | ----------------------------------------------------------------------- |
| `active`         | Informative memory (no fiscal effect), current and effective            |
| `pending_review` | Memory with fiscal effect, waiting for explicit HUMAN approval          |
| `approved`       | Approved by a human professional (the gate is closed)                   |
| `rejected`       | Rejected by a human professional — terminal, history stays visible      |
| `superseded`     | Replaced by a newer revision of the same (topicKey, scope) chain        |
| `voided`         | Annulled without a successor (correction) — terminal                    |

## The human approval gate

A memory with `fiscalEffect != none` (journal entry, declaration, closing,
adjustment, reclassification, approval, SUNAT filing) is saved as
`pending_review` and can only reach `approved` through a HUMAN actor
(`source.actorKind == human`). Agents and systems RECORD; they never approve.
The gate fails closed with `GATE_REQUIRES_HUMAN`.

```text
La IA asiste.  La memoria orienta.  El profesional revisa.  La evidencia permanece.
```

Voiding admits human or system actors (systemic correction), never agents.
`rejected`, `superseded` and `voided` are terminal — they never reopen.

## Transitions

| Transition | Allowed from              | Guard                                        |
| ---------- | ------------------------- | -------------------------------------------- |
| `approve`  | `pending_review`          | actorKind == human (GATE_REQUIRES_HUMAN)     |
| `reject`   | `pending_review`          | actorKind == human                           |
| `void`     | `active`, `pending_review`, `approved` | human or system, never agent       |
| `supersede`| `active`, `pending_review`, `approved` | successor id recorded (relation + supersedes_id) |

## Supersession

A `save()` on the same `topicKey` + exact scope creates a NEW immutable
revision (`revision + 1`) and supersedes the previous current revision: its
status becomes `superseded` and its `supersedesId` points to the successor,
with a `supersedes` relation recorded in the same transaction. Explicit
supersede (route memory A to an existing memory B) follows the same contract.
History is never edited; terminal predecessors are skipped.

## Vigencia

- Every memory may carry `effective` and `expiry` (Validity).
- Expired memories are returned as **stale** at read time — visible, but never
  presented as current fact.
- A `superseded` memory routes readers to its successor.

## Rules

1. **The gate is mandatory.** A memory with fiscal effect never reaches
   `approved` without a human actor; agents can create `pending_review`, never
   approve.
2. **Re-submission is new-version, not in-place edit.** History is preserved.
3. **Timestamps are triple.** `effectiveAt` (when it happened accounting-wise),
   `recordedAt` (when it entered the system — automatic, immutable),
   `observedAt` (when it was detected — optional). A late event that affects a
   previous closed period is visible precisely because these three dates can
   differ.
4. **Every transition traces to actor + actorKind + timestamp** (audit trail).
5. **Memory never authorizes business actions.** The approve gate is the
   professional review of a memory, not authorization of a declaration, payment
   or filing.
