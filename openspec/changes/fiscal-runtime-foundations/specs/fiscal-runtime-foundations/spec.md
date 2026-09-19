# Fiscal Runtime Foundations Specification

## Purpose

Define the versioned runtime contracts that protect the fiscal onboarding
consumer without changing frozen receipt payloads or weakening fiscal
non-authorization, tenant-isolation, provenance, integer-money, audit, or
human-approval guarantees.

This is a new domain specification. No canonical `openspec/specs/` domain spec
or proposal `Capabilities` section exists; the domain is inferred from the
proposal's three proposed capabilities and its affected-area table.

## Requirements

### Requirement: Canonical SUNAT RUC validation

The system MUST expose one versioned canonical validator for a company RUC.
The validator MUST accept exactly eleven ASCII digits and MUST apply the SUNAT
modulo-11 check digit algorithm to the first ten digits using weights
`5,4,3,2,7,6,5,4,3,2`; the expected digit MUST be `11 - (sum mod 11)`, with
results `10` and `11` reduced by subtracting `10`. The final digit MUST equal
the expected digit.

The validator MUST be the only RUC checksum authority used by Go and
TypeScript protected boundaries and MUST NOT query SUNAT or assert taxpayer
existence, ownership, membership, or authorization. Go and TypeScript MUST
produce the same decision and stable diagnostic classification for the same
bytes.

#### Scenario: Valid RUC is accepted

- GIVEN an eleven-digit ASCII RUC whose final digit satisfies the SUNAT
  modulo-11 algorithm
- WHEN a protected boundary validates the company scope
- THEN validation succeeds and the original RUC bytes are retained

#### Scenario: Shape or checksum failure is rejected

- GIVEN a RUC that is non-ASCII, not exactly eleven digits, or has an invalid
  check digit
- WHEN any protected boundary validates it
- THEN the operation fails with the stable typed code `INVALID_RUC`
- AND the response does not disclose whether a corresponding company exists

#### Scenario: Go and TypeScript parity is verified

- GIVEN the shared positive, negative, and boundary RUC vectors
- WHEN Go and TypeScript execute the vectors
- THEN both runtimes return byte-identical normalized input, decision, and
  classification results

### Requirement: RUC validation precedes protected work

Every protected company-scoped CLI, HTTP, MCP decoder, direct service, store
command, authentication/local-development seed, evidence export, review,
verification, close, reopen, and reconstruction entry point MUST validate the
RUC before scoped reads, ranking, authentication membership lookup, mutation,
idempotency reservation, object write, receipt creation, or audit append.

Adapters MUST NOT maintain private checksum implementations. The store MUST
repeat validation at its authoritative command boundary so a direct caller or
legacy adapter cannot bypass it.

#### Scenario: Invalid RUC produces no side effect

- GIVEN a protected request containing a checksum-invalid RUC
- WHEN the request is submitted through an applicable public or direct entry
  point
- THEN it returns `INVALID_RUC`
- AND it performs no data read that could disclose scoped data
- AND it creates no reservation, object, receipt, audit event, or partial state

#### Scenario: Cross-RUC request is not repaired from session data

- GIVEN an authenticated principal for RUC A and a request naming valid RUC B
- WHEN the request reaches a protected boundary
- THEN the request fails closed under the existing membership/scope policy
- AND the system neither substitutes RUC A nor discloses RUC B's data

### Requirement: Versioned ten-element fiscal operation scope

The system MUST provide an additive versioned fiscal operation-scope binding
contract, initially `v1`, with exactly these ordered elements:

`tenant, organization, company, fiscalPeriod, ledgerBook, operationType,
sourceSnapshot, policyVersion, actor, authorityLevel`.

All ten elements MUST be present and non-empty. `company` MUST be a valid
SUNAT RUC. `fiscalPeriod` MUST match `YYYYMM` and contain a month from `01`
through `12`. `sourceSnapshot` MUST be lowercase hexadecimal SHA-256 of the
frozen source manifest. `authorityLevel` MUST be exactly one of `ASK`,
`ANALYZE`, `PREPARE`, or `EXECUTE`. Unknown binding versions, extra elements,
missing elements, malformed values, duplicate keys, or partial bindings MUST
be rejected.

The binding MUST remain separate from caller authority. `actor` and
`authorityLevel` are scope metadata and MUST NOT grant roles, assurance,
membership, approval, ledger, filing, payment, or business authority. The
public error envelope MUST distinguish `SCOPE_BINDING_REQUIRED`,
`SCOPE_BINDING_INVALID`, `SCOPE_MISMATCH`, and
`UNSUPPORTED_SCOPE_VERSION` without revealing foreign scope data.

#### Scenario: Complete v1 binding is accepted

- GIVEN a v1 binding containing all ten elements with valid domain values
- WHEN the binding is validated against the authenticated operation context
- THEN validation succeeds only if tenant, organization, company, period, and
  trusted actor policy also match

#### Scenario: Missing or unknown binding data fails closed

- GIVEN a binding with an omitted element, an extra element, an invalid period,
  an invalid snapshot, an unknown authority level, or an unknown version
- WHEN a v1-protected operation is attempted
- THEN it fails with a stable binding-validation error
- AND no default, session, payload, migration, or adapter value fills the gap

#### Scenario: Scope metadata does not authorize

- GIVEN a caller supplies `actor` or `authorityLevel` naming an eligible actor
  or a stronger level
- WHEN the caller is not independently authenticated and authorized
- THEN the operation remains unauthorized and fails under the applicable
  authentication or policy error

### Requirement: Deterministic cross-runtime binding bytes and hash

The v1 binding MUST define one canonical byte representation and lowercase
SHA-256 hash. Canonical bytes MUST be UTF-8 and MUST use this exact framing:

`drenyra:fiscal-scope:v1\0`, followed for each of the ten elements in the
specified order by the ASCII key, `=`, the decimal UTF-8 byte length, `:`, the
value's UTF-8 bytes, and `\0`. Keys MUST be the contract spellings shown above;
values MUST be valid UTF-8, MUST contain no NUL byte, and MUST be preserved
without trimming, case folding, or Unicode normalization. The hash MUST be
SHA-256 of those canonical bytes, rendered as lowercase hexadecimal.

Go and TypeScript MUST publish and test shared golden vectors containing the
complete bytes and hash. Reordering elements, changing any value, changing its
byte length, changing version framing, or changing case MUST produce a
 different hash or a validation failure.

#### Scenario: Equivalent Go and TypeScript bindings hash identically

- GIVEN one shared valid v1 vector
- WHEN Go and TypeScript serialize and hash it
- THEN the canonical bytes and lowercase hash are identical

#### Scenario: Every element is hash-bound

- GIVEN a valid binding
- WHEN exactly one of its ten elements changes
- THEN the canonical bytes and hash change
- AND a protected operation using the old binding fails with a scope mismatch

#### Scenario: Ambiguous encoding is rejected

- GIVEN duplicate keys, reordered keys, NUL-containing values, invalid UTF-8,
  or a length inconsistent with UTF-8 bytes
- WHEN the binding is decoded
- THEN it fails closed before authorization or persistence

### Requirement: Persisted binding and store-authoritative enforcement

Every v1-bound protected object and protected act MUST persist the binding
version, all ten canonical elements, canonical hash, and immutable linkage to
the applicable reviewed envelope and audit/provenance record. Reloaded work
MUST recompute the hash from persisted elements and compare it with the stored
hash before proceeding.

The store MUST be the final authority for v1 mutation, approval, close,
reopen, evidence linking, and post-close checks. A command MUST provide the
complete current binding; an old company/RUC/period tuple or partial adapter
MUST NOT mutate a v1-bound object. Any mismatch, tampering, scope change, or
closed-period write MUST fail atomically with no partial state.

The first-slice protected operations are fiscal-memory save/supersede, scoped
review reads, evidence storage/retrieval/linking, professional memory approval,
monthly close creation and professional close approval, post-close mutation
checks, reconstruction, and offline verification.

#### Scenario: Direct store bypass is denied

- GIVEN a v1-bound object and a direct store command carrying an old tuple or a
  different complete binding
- WHEN mutation, approval, close, reopen, or evidence linking is attempted
- THEN the store rejects it with the applicable scope error
- AND no row, object, receipt, audit, or idempotency state changes

#### Scenario: Reload detects binding tampering

- GIVEN persisted v1 elements whose recomputed hash differs from the stored
  hash
- WHEN the object is reloaded for protected work or verification
- THEN the operation fails closed and reports an unverifiable binding
- AND it does not infer or repair missing values

#### Scenario: Scope change invalidates review

- GIVEN a reviewed envelope and binding for scope hash A
- WHEN any binding element or reviewed envelope changes
- THEN the prior approval/review state cannot be reused
- AND a fresh detail, binding, and decision attempt is required

### Requirement: Public versioned binding input and compatibility

The canonical CLI representation for v1 binding input MUST be a strict JSON
binding document containing `version` and exactly the ten named elements. The
CLI MUST reject unknown fields, duplicate fields, missing fields, invalid RUCs,
invalid periods, malformed snapshots, and unknown versions with stable
machine-readable errors; it MUST print tokens and credentials only in redacted
form.

Authenticated HTTP and any MCP operation that creates or mutates v1-bound
state MUST require the equivalent complete binding and reject unsupported or
partial input. The stdio MCP server MUST remain unauthenticated for
professional approval: adding identity, role, assurance, or actor fields to a
tool argument MUST NOT create an approval path, and approval MUST continue to
fail with `AUTHENTICATION_REQUIRED`.

#### Scenario: CLI opts into v1 explicitly

- GIVEN a protected CLI command and a valid strict v1 binding document
- WHEN the command is executed
- THEN the command validates and carries that exact binding through the store
- AND its machine-readable result identifies the binding version and hash

#### Scenario: Legacy tuple cannot opt in implicitly

- GIVEN a command that supplies only the legacy company/RUC/period tuple
- WHEN it targets v1-bound mutation or approval
- THEN it fails closed without synthesizing the remaining elements

#### Scenario: Unauthenticated stdio MCP cannot approve

- GIVEN an MCP approval tool call, even if it includes claimed identity or
  review fields
- WHEN it is handled by the stdio server
- THEN it returns `AUTHENTICATION_REQUIRED`
- AND no approval, receipt, or audit mutation occurs

### Requirement: Authenticated professional approval acknowledgements

The canonical CLI approval command and the authenticated HTTP approval route
MUST accept the exact current reviewed-envelope hash, a non-empty reason, an
idempotency key under the existing contract, and a strict `reviewChecks` object
with exactly `evidenceInspected` and `applicableRulesInspected` boolean
members. Omission MUST remain distinguishable from `false`; omitted or false
required checks MUST fail with `REVIEW_CHECKS_REQUIRED` for material or
critical memory. Unknown review-check fields MUST be rejected.

The local-development professional path MUST require explicit
`DRENYRA_ENV=local_dev`, isolated local paths, short-lived fictional session
data, and no committed token or credential. A local seed MAY create fictional
membership/session data but MUST NOT mint production authority.

The principal, subject, roles, membership, assurance, trusted actor, and
separation-of-duties inputs MUST be derived from the authenticated session and
policy context. Payloads, flags, binding elements, memory content, and MCP
arguments MUST NOT declare or elevate authority. The store MUST enforce exact
reviewed-envelope freshness, tenant/company scope, active membership, role and
assurance policy, proposer/reviewer separation, material/critical checks,
idempotency, and one atomic state transition.

#### Scenario: Eligible professional approves exact material envelope

- GIVEN an authenticated fictional local-development professional with active
  membership, sufficient role and assurance, no proposer conflict, the exact
  current binding and envelope hash, a reason, an idempotency key, and both
  explicit positive review acknowledgements
- WHEN approval is submitted through CLI or authenticated HTTP
- THEN the memory transitions exactly once in one transaction
- AND immutable approval audit evidence and the existing compatible signed
  receipt are recorded

#### Scenario: Missing or false acknowledgement is rejected

- GIVEN a material or critical memory and omitted or false evidence/rule review
  acknowledgement
- WHEN approval is submitted
- THEN it returns `REVIEW_CHECKS_REQUIRED`
- AND state, receipts, audit, objects, and idempotency reservation remain
  unchanged

#### Scenario: Existing policy failures remain typed and atomic

- GIVEN a stale envelope, wrong binding, inactive membership, insufficient
  role/assurance, self-approval, idempotency conflict, or missing authentication
- WHEN approval is submitted
- THEN the applicable existing typed outcome is returned
- AND no partial approval or receipt is produced

### Requirement: Receipts, audit, and offline verification remain compatible

The change MUST preserve existing signed receipt payload versions, Ed25519 chain
semantics, and historical receipt verification. New binding evidence MUST be
linked through the reviewed/canonical envelope and immutable audit linkage
without changing a frozen receipt format. If complete v1 coverage cannot be
represented without changing a receipt payload, implementation MUST stop and
require a separate receipt-contract proposal.

Every new mutation or approval audit record MUST include trusted tenant/RUC,
period, timestamp, actor/provenance, reason where required, binding version and
hash, and the exact reviewed-envelope reference. Audit and binding records MUST
be append-only and immutable.

Offline verification MUST report v1 binding completeness and hash consistency
when applicable, explicitly classify legacy/unbound records, and MUST retain
the exact conclusion `Accounting correctness: NOT ASSERTED`.

#### Scenario: Historical receipt remains verifiable

- GIVEN a receipt created under an existing frozen payload version
- WHEN offline verification runs after the migration
- THEN the receipt verifies under its original contract and is not relabeled
  as v1-bound without binding evidence

#### Scenario: Offline v1 verification is complete

- GIVEN a v1-bound act with intact binding, envelope, audit, and receipt links
- WHEN offline verification runs without network access
- THEN it reports complete, hash-consistent binding evidence and ends with
  `Accounting correctness: NOT ASSERTED`

#### Scenario: Missing evidence is not a pass

- GIVEN a v1-referenced act with missing, inconsistent, or tampered binding
  evidence
- WHEN offline verification runs
- THEN it reports an unverifiable or incomplete binding and never reports a v1
  pass or accounting correctness

### Requirement: Legacy classification, additive migration, and rollout gates

Migration MUST be additive and MUST classify each persisted company-scoped
record as `legacy` when it lacks a complete verified v1 binding or `v1` when it
contains all ten elements, the version, and a verified hash. Migration MUST NOT
invent values, silently rewrite RUCs, delete records, or alter historical
receipts.

Checksum-invalid legacy records MUST be reported using counts and safe opaque
identifiers without cross-tenant disclosure. They MUST remain explicitly
legacy/unbound; remediation requires a separately audited operator decision.
The default rollout MUST fail closed for protected mutation, approval, close,
reopen, and linking of legacy or partial records unless a separately documented
compatibility mode is explicitly selected and does not weaken v1 guarantees.

V1 writes MUST be gated by inventory, contract/vector freeze, shadow/read-only
validation, boundary/store/parity/rollback evidence, and professional-path
positive and negative controls. Once v1 records exist, an older binary MUST
refuse downgrade mutation and may only run in a compatible read-only or
dual-read mode. The dependent `fiscal-onboarding-journey` MUST remain blocked
until this specification's implementation is verified.

#### Scenario: Legacy rows remain readable but are not upgraded by inference

- GIVEN an existing valid legacy row without a complete binding
- WHEN it is read or verified
- THEN it remains readable under its original contract and is labelled legacy/
  unbound
- AND verification does not claim v1 enforcement

#### Scenario: Invalid legacy RUC is surfaced safely

- GIVEN a persisted legacy row with a checksum-invalid RUC
- WHEN migration diagnostics run
- THEN diagnostics report a count and safe opaque identifier classification
- AND no RUC normalization, cross-tenant disclosure, or data mutation occurs

#### Scenario: Write gate blocks premature v1 creation

- GIVEN one or more required rollout gates or rollback checks have not passed
- WHEN a v1-bound write is attempted
- THEN creation fails closed and existing legacy data remains unchanged

### Requirement: Fiscal guardrails remain invariants

The implementation MUST preserve integer-cent money only, structural tenant and
RUC isolation before ranking or mutation, immutable history and provenance,
human-controlled close, `PERIOD_CLOSED` post-close denial, no external SUNAT
lookup, fictional local-development data only, and the non-authorization
boundary. No scope binding, receipt, observation, review acknowledgement,
`authorityLevel`, or `actor` field MAY be interpreted as authorization for a
ledger entry, declaration, filing, payment, or business act.

#### Scenario: Cross-scope probes disclose nothing

- GIVEN a request crossing tenant, organization, RUC, period, or source snapshot
- WHEN it reaches a protected read or mutation
- THEN it fails closed without foreign identifiers/content or partial state

#### Scenario: Closed period remains human-controlled

- GIVEN a period approved and closed by an authenticated eligible professional
- WHEN an agent or unauthenticated caller attempts a protected write
- THEN it returns `PERIOD_CLOSED` and creates no mutation, receipt, or audit act

#### Scenario: Fiscal correctness is not asserted

- GIVEN any successful binding, approval, receipt, or offline verification
- WHEN a public result or verification report is produced
- THEN it does not claim accounting correctness, taxpayer existence, or business
  authorization
