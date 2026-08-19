# Archive Report — sdd-060-at-rest-encryption

> Phase: archive · Artifact: archive-report · Status: closed
> Inputs: proposal + spec + design + tasks + apply-progress + verify-report.

## Outcome

sdd-060-at-rest-encryption is COMPLETE and archived. SDD-060 §5 security slices
delivered: per-tenant at-rest content encryption (opt-in via
`DRENYRA_ENCRYPTION_MASTER_KEY`, HKDF-derived per-tenant keys, AES-256-GCM,
schema v15 additive, fail-closed reads) and the sync encryption-mismatch guard
(an encrypted source never copies into a plaintext sink).

## Final state facts (supersede intermediate snapshots)

- All tasks.md checkboxes marked after the final gate (fresh `-count=1` AFTER
  the last code edit).
- Verify: PASS — AC-ENC-1…7 all green with mapped evidence (verify-report.md).
- Gates at the final boundary: `go test ./... -count=1` 10/10 ok · `npm test`
  386/386 · typecheck clean · vet clean · gofmt clean · `TestGoldenVectorsGo` ok.
- Delivery: two atomic conventional commits (feat encryption core + schema v15 →
  feat sync guard + CLI env + docs), review clone-off (user decision) —
  ordinary-policy commits.

## Handoff notes

- **Master key custody:** `DRENYRA_ENCRYPTION_MASTER_KEY` is operator-held env
  config (32 bytes, hex/base64). Derived per-tenant KEKs never persist;
  discarding a tenant's salt makes its content unrecoverable (right-to-delete
  posture). ADR-005 covers SIGNING-key custody (HSM/KMS); encryption master-key
  custody is documented as env config in this change.
- **Legacy rows** (content_algo = '') stay plaintext forever; a re-encryption
  tool is a future change (documented non-goal).
- **Sync transparency:** Source/Sink already carry decrypted memories; the
  target re-encrypts via ImportObservation (never lands plaintext).
- The pi-lens runner staleness (replayed RED-state snapshot with old line
  numbers) is documented here and in the tenant-cli verify-report; the
  authoritative Go toolchain verification is green.
