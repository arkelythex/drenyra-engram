# Fiscal Operation Scope Binding v1

> Status: additive versioned contract. This contract does not alter legacy scope or receipt payload versions.

## Binding

Transport documents contain `version: "v1"` followed by exactly these ten ordered string elements:

1. `tenant`
2. `organization`
3. `company`
4. `fiscalPeriod`
5. `ledgerBook`
6. `operationType`
7. `sourceSnapshot`
8. `policyVersion`
9. `actor`
10. `authorityLevel`

All elements are required and non-empty. Values are preserved as supplied: no trimming, case folding, Unicode normalization, or inferred defaults. Values containing NUL, invalid UTF-8, unknown/duplicate/reordered keys, trailing JSON, or extra fields are invalid.

`company` is exactly eleven ASCII digits and passes the SUNAT modulo-11 checksum. `fiscalPeriod` is `YYYYMM` with month `01`–`12`. `sourceSnapshot` is lowercase hexadecimal SHA-256. `authorityLevel` is `ASK`, `ANALYZE`, `PREPARE`, or `EXECUTE` and must match the closed operation classification.

## Canonical bytes and hash

Canonical UTF-8 bytes begin with `drenyra:fiscal-scope:v1\0`. Each element then contributes, in the order above:

```text
<ASCII key>=<decimal UTF-8 byte length>:<exact value bytes>\0
```

The binding hash is lowercase hexadecimal SHA-256 of those bytes. Every element participates independently. Go and TypeScript consume the shared `fiscal-ruc-v1.json` and `fiscal-scope-v1.json` vectors.

## Operation classification

- `PREPARE`: `memory.save`, `memory.supersede`, `evidence.store`, `evidence.link`, `rule.link`, `close.create`
- `ASK`: `context.read`, `review.queue`, `review.detail`, `timeline.read`, `evidence.get`
- `ANALYZE`: `reconstruct.read`, `verify.read`
- `EXECUTE`: `memory.approve`, `close.approve`, `period.reopen`, `period.write-check`

Classification validates scope intent only. `actor`, `authorityLevel`, an observation, a receipt, or a binding NEVER grants identity, membership, role, approval, ledger, filing, payment, or business authority. Those controls remain independently authenticated and policy-enforced.

## Fail-closed errors

- `SCOPE_BINDING_REQUIRED`: no complete binding was supplied.
- `SCOPE_BINDING_INVALID`: structure, value, encoding, operation, or classification is invalid.
- `SCOPE_MISMATCH`: a complete binding differs from trusted or persisted scope.
- `UNSUPPORTED_SCOPE_VERSION`: `version` is not supported.

Errors are non-enumerating and do not reveal foreign scope data. V1 protected work compares the complete binding before reads or mutation; no adapter or migration may fill missing elements.

## Compatibility

Legacy company/RUC/period records remain legacy/unbound and keep their original bytes and historical verification semantics. They are never relabeled v1 by inference. Existing receipt payloads, canonicalization, signatures, and goldens remain unchanged. V1 evidence is additive and immutable; any future receipt payload change requires a separate approved contract.
