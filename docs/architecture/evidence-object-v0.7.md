# Evidence Objects (v0.7.0) — Local-First WORM Object Store

> **Status:** DELIVERED (local-first slice, v0.7.0) · **Date:** 2026-08-07
> **Basis:** implemented behavior with Go + TypeScript tests (see
> [docs/due-diligence/2026-08-product-architecture-audit.md](../due-diligence/2026-08-product-architecture-audit.md),
> [docs/security/evidence-lifecycle-and-threat-model.md](../security/evidence-lifecycle-and-threat-model.md)).

An **EvidenceObject** is ONE immutable artifact (XML/PDF/CDR/extracto bytes)
plus its metadata, stored WRITE-ONCE-READ-MANY (WORM) under a
content-addressed layout. This slice delivers the local-first object layer:
store bytes, read bytes back scope-first, and prove at read time that the
bytes still hash to their content address. The production-facing stages —
retention, legal hold, export, purge, cloud — are **explicitly deferred**.

## Quick path

```bash
drenyra-engram object store ./factura-2026-07.xml --ruc 20123456789 --period 202607
drenyra-engram object get <sha256> --ruc 20123456789 --period 202607   # scope-first
drenyra-engram verify object <sha256>                                   # rehash + full chain
```

Identical bytes are a content-addressed duplicate: the second `store` is a
NO-OP (`created=false`) and mints no receipt.

## Delivered scope

| Capability | What it means | Anchor |
|---|---|---|
| Local content-addressed WORM bytes | Layout `objects/<sha[0:2]>/<sha[2:4]>/<sha256>`; the object id IS the SHA-256 hex of the bytes (`ComputeObjectID`); no overwrite/delete API; path escape fails closed as corruption; no silent repair | [internal/store/object_store.go](../../internal/store/object_store.go), [internal/core/evidence_object.go](../../internal/core/evidence_object.go) |
| Schema v8 immutable metadata | `evidence_objects` table with no-update/no-delete triggers + scope index; one-transaction v7→v8 migration (receipts table copied+swapped to extend subject/action CHECKs and add the typed FK) | [internal/store/store.go](../../internal/store/store.go) |
| `object_stored` receipt | Emitted atomically inside the store transaction, ONLY for genuinely new objects; subjectType `evidence_object`; `objectId = sha256` | [contracts/receipts.md](../../contracts/receipts.md), [internal/receipts/signer.go](../../internal/receipts/signer.go) |
| Scoped store/get | Exact tenant/company/RUC/period scope (institutional objects rejected); reads are scope-first — an exact scope match is required | [internal/core/evidence_object.go](../../internal/core/evidence_object.go), [contracts/scope.md](../../contracts/scope.md) |
| Object-level rehash availability verification | `verify object` runs the six receipt layers + principal provenance + WORM byte integrity (stored bytes re-hash to the content address; mismatch fails closed); `verify memory` reports object availability for refs that resolve to stored objects (legacy refs reported, never failed) | [internal/server/verify_service.go](../../internal/server/verify_service.go), [contracts/verification.md](../../contracts/verification.md) |
| CLI / HTTP / MCP surfaces | CLI `object store|get` + `verify object` · HTTP `POST /accounting/objects`,`GET /accounting/objects/{objectId}` · MCP `accounting_object_store`,`accounting_object_get` | [cmd/drenyra-engram/main.go](../../cmd/drenyra-engram/main.go), [internal/server/object_http.go](../../internal/server/object_http.go), [internal/server/mcp.go](../../internal/server/mcp.go) |
| Go/TS coverage | Go suite green (core/store/server/cmd, re-run `-count=1`); TypeScript suite green (277 tests / 20 files) incl. the mirror [core/evidence-object.ts](../../core/evidence-object.ts) and the v0.7 mirror in [store/memory-store.ts](../../store/memory-store.ts) | — |

## Deferred scope (NOT implemented — no claims)

- **Retention expiry** — no retention clock; objects have no expiry semantics.
- **Legal hold** — no hold records; nothing overrides a retention decision.
- **Export** — no tenant export surface for objects + links + receipts.
- **Purge / deletion** — no delete API exists by design; purge flows are absent.
- **Cloud / remote object storage** — local-first only (ROADMAP non-goal).
- **OCR / content search over objects** — objects are opaque bytes; the engine
  never parses or executes their content (data, never instructions).
- **SUNAT/ERP object ingestion** — no integration surface.
- **Production object-store operations** — backup/restore drills,
  encryption-at-rest/TDE, recovery objectives are not demonstrated.

Object storage never authorizes anything: storing an object is a
provenance-recorded act, not an approval (non-authorization boundary).

## Next step

Plan the deferred stages (retention → legal hold → export → purge) as the next
EvidenceObject work, gated by the [v1 gate](../product/initial-market-and-v1-gate.md)
criterion G-5, and review the [threat model](../security/evidence-lifecycle-and-threat-model.md)
(T-7) against production hardening.
