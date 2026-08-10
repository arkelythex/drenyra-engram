# Reconstructible Monthly Close — Demo Fixture

> **Status:** delivered (2026-08-10) · **Fictional, deterministic, local-first**
> **Contract:** design brief §7 · Test: `TestReconstructibleCloseFixture`
> (`internal/server/close_fixture_test.go`)

## The product promise (§7.1)

An authorized professional can explain a material balance WITHOUT the original
agent:

```text
balance
  → external ledger reference
  → entry or adjustment
  → evidence object (XML/CDR/document)
  → applicable rule and temporal validity
  → judgment or reconciliation
  → professional approval
  → offline-verifiable provenance
```

This fixture proves that chain end to end, deterministically, on a fictional
tenant — the aha-moment demo the due-diligence audit recommended (account 4011
explained from source evidence, rule, and approval; audit Q20).

## Run it

```bash
go test ./internal/server/ -run TestReconstructibleCloseFixture -v
```

## The fixture (fictional — never real taxpayer data)

| Element | Value |
|---|---|
| Tenant / RUC | `cmp_org` / `20100039201` (checksummed TEST RUC, not a real taxpayer) |
| Period | `202601` (January 2026) |
| Professional | MARÍA TORRES, controller (every proposer is an AGENT — SoD holds) |
| Ledger ref (frozen) | `LEDGER//2026-01/4011` — the external source of truth (D-4: Engram records and explains; it is not the ledger) |

## The chain, step by step

1. **Versioned fiscal rule** — `policy/tax/retention-rate`: v1 (vigencia
   2026-01 → 2026-03-31) superseded by v2 (vigencia from 2026-04). The chain
   keeps BOTH revisions; resolution picks v2, supersession is visible.
2. **Manual evidence ingestion** — invoice XML + CDR bytes stored through the
   v0.7 WORM object store (content-addressed: the object id IS the SHA-256 of
   the bytes; a read returns the exact bytes). NO SUNAT/ERP ingestion exists —
   this is manual, per the demo boundary (§7.3).
3. **Opening balance** — fact with the frozen ledger reference.
4. **Entries** — IGV compras (journal_entry) and the LATE comprobante F001-948
   (adjustment, MATERIAL): effectiveAt 2026-01-31, observedAt 2026-02-03 — the
   triple-timestamp late event affecting a period after its close.
5. **Contradiction adjudicated** — the late exception ("credit deferred to
   February") CONTRADICTS the adjustment ("credit in January"); an agent
   proposes the contradiction, the controller CONFIRMS it (resolution: the
   credit belongs to January).
6. **Professional approvals** — every gated entry approved through the
   authenticated path against the exact reviewed envelope; the MATERIAL
   adjustment required both review checks (anti-rubber-stamp).
7. **Monthly close** — `CreateClose` freezes the totals (signed cents, source
   memory ids), the controller approves → `period_closures` = closed, and a
   late save in the period fails `PERIOD_CLOSED` (write gate).
8. **Offline verification** — `VerifyMemory` (12 layers: payload
   canonicalization, envelope integrity, signature, key validity, scope, chain,
   principal provenance, supersession, evidence/object/rule availability) ends
   with **"Accounting correctness: NOT ASSERTED"**; `VerifyJudgment` passes for
   the confirmed contradiction.

## Evidence-object provenance fix (v0.9.0)

The fixture exposed a verifier gap: `evidence_linked` provenance resolved by
`(memory, timestamp)` only, and `nowISO()` has SECOND resolution — two
evidence links in the same second (the realistic XML+CDR pair) were
AMBIGUOUS and failed the provenance layer. Fixed in
`internal/store/store.go`: the receipt payload already carries the exact
`evidenceRef`, so provenance now resolves by `(memory, timestamp, ref)`; the
same-second pair is unambiguous, and a genuinely missing/corrupt record still
fails closed.

## Demo boundary (§7.2/§7.3) — what this fixture does NOT claim

- **No SUNAT/ERP integration** — the ADAPTER CONTRACT is defined (parse an
  electronic invoice XML into reconstruction metadata + WORM store: CLI
  `object ingest <file>`, core.ParseComprobanteXML, server.IngestComprobante),
  but production integration is NOT implemented — no credentials, no
  retries, no outage behavior, no response retention, no source authority
  (design brief §7.3).
- **Fictional data** — no real taxpayer, invoice, or period.
- **No accounting correctness** — the signed chain proves provenance and
  integrity, never that the professional decision is correct.
- **Engram is not the ledger** — ledger references are frozen strings; the
  deployment-selected ledger remains the source of truth (D-4).

## Gate status

Advances the v1.0 gate item **G-2** from NOT MET to PARTIAL: the
deterministic fictional fixture is reproducible end-to-end; G-2 still requires
ONE real or legally approved anonymized close reproduced the same way (see
docs/product/initial-market-and-v1-gate.md).
