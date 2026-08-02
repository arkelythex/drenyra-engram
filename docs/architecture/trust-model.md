# Trust Model — Drenyra Engram (Institutional Accounting Memory)

> **Last updated:** 2026-08-01.

> Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents; no float is ever used for money; version/sequence numbers are JSON integers, never floats.

## Model in five lines

```text
Memory guides.        → Drenyra Engram stores and retrieves knowledge.
Policy restricts.     → Consumers apply their policies; Engram carries them as observations.
Evidence demonstrates. → Provenance and vigencia make knowledge checkable.
Receipt certifies.    → Certification happens in drenyra-ai receipts, never in memory.
Professional authorizes. → Only humans approve; no observation is ever an approval.
```

## The defining invariant

**Memory never authorizes.**

- The engine stores and retrieves knowledge. It has **no authority model**.
- No observation is ever treated as approval, permission, or authorization — by Engram or by any consumer.
- Cross-consumer integration must route approvals through Drenyra AI gates and human professionals, never through memory.

## Trust boundaries

### 1. Knowledge is scoped

- Company/RUC/period scoping is structural, in the schema and in search indexes.
- Queries filter scope before ranking; out-of-scope knowledge is not retrieved as if in-scope.

### 2. Knowledge is provenance-backed

- Provenance (who/what/when/why) is written at creation and cannot be silently rewritten.
- A correction is a new observation linked via relations — history is preserved, not rewritten.

### 3. Knowledge ages visibly

- Vigencia (effective/expiry) semantics make stale knowledge **visible**, never silently trusted.
- Expired or superseded observations are flagged and ranked accordingly.

### 4. Conflicts are surfaced, never resolved silently

- Conflicting memories are surfaced for human/relation review.
- Engram never guesses which memory is right — that is a professional decision.

### 5. The engine decides nothing

- Engram can tell you what the organization knows. It cannot tell you what is authorized.
- Authority lives outside: in `drenyra-ai` gates and in human professionals.

## Fail-closed default

When scope is missing, provenance is incomplete, or vigencia has expired, Engram **fails closed**: it says so, marks the knowledge accordingly, and never dresses stale or unproven knowledge as current truth.
