# Contract: lifecycle

> Version: 0.1-draft · Status: draft · Transport-agnostic.

Defines the **observation lifecycle and vigencia** — how memory matures and expires.

## States

```text
draft ──► reviewed ──► promoted ──► superseded
  ▲          │            │
  └──────────┴────────────┘   (re-submission allowed only via new version)
```

| State      | Meaning                                                   |
| ---------- | --------------------------------------------------------- |
| `draft`    | Written by an agent/session; not yet reviewed             |
| `reviewed` | Reviewed by a human or senior reviewer                    |
| `promoted` | Accepted as institutional knowledge                       |
| `superseded`| Replaced by a newer observation via relation            |

## Transitions

| Transition          | Allowed from        | Guard                                    |
| ------------------- | ------------------- | ---------------------------------------- |
| `review`            | `draft`             | Reviewer recorded in provenance          |
| `promote`           | `draft`, `reviewed` | Explicit acceptance event                |
| `supersede`         | any non-terminal    | Points to the replacing observation      |
| `reopen`            | `superseded`        | Only as a new version, never in place    |

## Vigencia

- Every observation may carry `effective` and `expiry`.
- Expired observations are returned as **stale** at read time — visible, but never presented as current fact.
- A `superseded` observation routes readers to its successor.

## Rules

1. **Promotion is explicit.** Nothing auto-promotes to institutional knowledge.
2. **Re-submission is new-version, not in-place edit.** History is preserved.
3. **Unknown states fail closed.** A lifecycle state the engine does not know is treated as not-promoted.
    4. **Judgments and conflicts are first-class.** A memory that conflicts with another must be related, not silently dropped.

## Frozen semantics (v0.1)

> The bullets in this section are **frozen-for-0.1**: normative, enforced by the
> conformance suite, and carried unchanged into the standalone Go engine
> (ADR-001). The rules above stay `0.1-draft` until the Phase 1 freeze review.

1. **UNKNOWN states fail closed.** An observation never enters an unknown
   authority state: the engine recognizes exactly `draft`, `reviewed`,
   `promoted`, `superseded`. An unknown state observed at read time is treated
   as **not-promoted** — never as authority.
2. **Supersede requires an explicit target id.** `supersede` must name the
   replacing observation (`targetId`); an observation never supersedes itself,
   and a missing or unknown target throws instead of silently marking
   `superseded`.

## Conformance

Vectors cover: legal/illegal transitions, expiry staleness, supersession routing, promotion explicitness, fail-closed unknown states, and the frozen-for-0.1 semantics above (including the explicit-target supersede requirement).
