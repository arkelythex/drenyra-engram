# Operability Evidence — Repository-Demonstrated Capabilities

> **Status:** evidence document · **Audit closure:** block Z (420-433) of
> [docs/due-diligence/2026-08-product-architecture-audit.md](../due-diligence/2026-08-product-architecture-audit.md)
> **Scope:** repository-verifiable operational mechanics only. No production
> readiness claim, no external-drill claim, no numerical recovery target (FD-3).

## Purpose and evidence boundary

This document records what the Drenyra Engram repository **proves today** about
operability: the executable doctor surface, WAL-safe snapshot and restore
drills, corruption drills on marked drill copies, exact scope and backup-identity
verification, and tenant lifecycle export. Every claim pairs with an executable
citation — a test file plus a unique test name — per the audit evidence contract
(EC-1, NFR-Z.1).

The evidence boundary is deliberately narrow: passing tests demonstrate the
recovery **mechanics**; they do not prove a deployment backup schedule, retained
recovery points, a completed external operational drill, guaranteed recovery
time or data loss, encryption-at-rest/TDE, cloud storage, or production service
readiness.

## Delivered capabilities and executable evidence

| Capability | Implementation | Positive evidence | Fail-closed evidence |
|---|---|---|---|
| Routine/full doctor | `internal/store/doctor.go` | `TestDoctorRoutineRunsQuickCheckThenForeignKeyCheck` (`internal/store/doctor_test.go`), `TestDoctorReportsLifecycleTableCounts` (`internal/store/object_hardening_test.go`), `TestDoctorReportsPurgeLifecycleThroughAPI` (`internal/server/api_test.go`), `TestCLIDoctorDrillCopyModeContract` (`cmd/drenyra-engram/main_test.go`) | `TestDoctorRoutineNeverRunsIntegrityCheckOnCorruptStore`, `TestDoctorFullRequiresMarkedDrillCopy` (`internal/store/doctor_test.go`), `TestDoctorReportsOrphanAndTempFilesWithoutRepair`, `TestDoctorMissingBytesFailsClosed`, `TestDoctorInvalidRowPathFailsClosed` (`internal/store/object_hardening_test.go`), `TestDoctorFailsClosedOnMissingTable` (`internal/server/api_test.go`) |
| WAL-safe snapshot | `internal/store/drill.go` `CreateDrillSnapshot` | `TestRunRestoreDrillSuccess` (`internal/store/drill_test.go`) | distinct canonical paths, overwrite refused |
| Restore + verify-after-restore | `internal/store/drill.go` `RunRestoreDrill` | `TestRunRestoreDrillSuccess` (`internal/store/drill_test.go`) | `TestRunRestoreDrillNegativeMatrix`, `TestRunRestoreDrillScopeIsolation`, `TestSchemaVersionParseFailsClosed` (`internal/store/drill_test.go`) |
| Corruption drills on marked copies only | `internal/store/drill.go` `RunCorruptionDrill` | `TestRunCorruptionDrillFullPath` (`internal/store/drill_test.go`) | `TestCorruptionDrillRequiresMarkedCopy`, `TestCorruptionDrillEvidencePathContract`, `TestCorruptionDrillEvidenceCannotOpenAsLiveStore`, `TestCorruptionDrillNotDetectedFailsClosed` (`internal/store/drill_test.go`) |
| Exact scope + backup-identity verification | `internal/store/drill.go` (manifest `Scope`, `SourceSHA256`) | `TestRunRestoreDrillSuccess` (`internal/store/drill_test.go`) | `TestRunRestoreDrillScopeIsolation` (`internal/store/drill_test.go`) |
| Tenant lifecycle export | `internal/store/evidence_export_test.go`; `internal/server/purge_http_test.go` | `TestExportEvidenceLifecycleDeterministicBundle`, `TestExportEvidenceLifecycleTenantIsolation`, `TestExportCrashIntentNotStoredAndRecoveryConverges` (`internal/store/evidence_export_test.go`) | `TestHTTPLifecycleExportScopeFirst` (`internal/server/purge_http_test.go`) |

## Qualitative recovery objectives

Snapshot, restore, verify-after-restore, and corruption detection are **proven
mechanisms** in this repository: the restore drill verifies integrity, foreign
keys, exact expected scope, and backup identity before publishing the output
(`TestRunRestoreDrillSuccess`), and corruption drills run only on marked drill
copies with writes frozen closed on detection.

Numerical RTO and RPO targets are **deployment/business-owned and UNKNOWN** until
an accountable owner records them (FD-3). This document sets no recovery-time or
data-loss objective for any deployment.

## Operational boundaries and non-claims

Repository tests do **NOT** prove:

- a deployment backup schedule, retained recovery points, or a completed
  external operational drill;
- guaranteed recovery time or data loss for any deployment;
- encryption-at-rest/TDE or cloud/remote object storage;
- production service readiness or a numerical service objective.

## Evidence maintenance

Each PASS claim above keeps a unique executable citation (file + test name). If
a cited test is renamed, removed, or fails, the corresponding claim reverts to
conservative RISK wording — a citation is never dropped while its claim stays.
No claim in this document is prose-only (EC-1, EC-4).
