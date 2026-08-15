# First Production Identity Slice — Stateless Auth0 Access-Token OIDC

Status: implemented (merged to main 2026-08-15; issue #18 approved slice).
Scope: Go engine `internal/auth` (validator), `internal/store` (DB cross-check),
`internal/server` (HTTP middleware wiring) and the `serve` daemon
configuration. Fiscal role / SoD policy semantics are UNCHANGED — this slice
only changes how a principal proves identity on the HTTP surface.

## 1. Decisions

- **Resource-server access tokens only.** The engine is an API resource server:
  it validates Auth0 access tokens, never ID tokens, and never acts as a
  browser-facing OIDC client (no `authorization_code`/PKCE flows, no sessions).
- **OIDC is stateless in this slice.** The JWT is verified in memory per
  request; the raw token is NEVER persisted, logged or hashed into a session
  row. No `sessions` row is created on the OIDC path — `authentication_method`
  stays session-based for the CLI (`auth login`, judge/reconcile/close/hold/
  purge commands).
- **DB-backed membership cross-check.** The verified `sub`, tenant claim and
  company claim are looked up as the exact `(subject, tenant, company)`
  membership tuple (`memberships` UNIQUE `subject_id,tenant_id,company_id`).
  Ambiguity/mismatch fails closed: a missing tuple is `PRINCIPAL_INVALID`
  (401), an existing-but-inactive membership is `MEMBERSHIP_INACTIVE` (403).
  Claims alone never mint membership.
- **Fail closed at startup.** A partial or invalid `DRENYRA_OIDC_*` set aborts
  `serve`. There is no trust configuration that "half works".
- **Default assurance `standard`.** No unconfigured ACR/MFA elevation; the
  `acr`/`amr` claims are ignored in this slice.
- **No dependency added.** JWT parse, RS256 verification and JWKS handling are
  implemented with the Go standard library only (`crypto/rsa`, `net/http`,
  `encoding/json`).

## 2. Validation contract (`internal/auth/oidc.go`)

Every access token must satisfy ALL of the following or it is rejected:

| Rule | Enforced behavior |
|---|---|
| Algorithm | `alg` MUST be exactly `RS256`. `none`, HS*/PS*/ES*, unknown → reject BEFORE any signature/key work (no algorithm confusion). |
| Key id | Header `kid` is required and must resolve in the JWKS. |
| Issuer | `iss` MUST equal the configured `DRENYRA_OIDC_ISSUER` exactly. |
| Audience | `aud` MUST be exactly the configured `DRENYRA_OIDC_AUDIENCE` — a string must match; an array must contain exactly that one audience. Multi-audience tokens fail closed. |
| Subject | `sub` is required, non-empty. |
| Time | `exp` REQUIRED and `now <= exp + skew`; `nbf` (when present) `now >= nbf − skew`; `iat` (when present) `now >= iat − skew` and `iat <= now + skew` (a future `iat` is rejected). Skew defaults to 30s and is bounded at 5m (`DRENYRA_OIDC_CLOCK_SKEW`). |
| Tenant/company claims | The configured custom claims (default `tenant_id` / `company_id`) are REQUIRED non-empty strings. |
| Signature | RS256 PKCS#1 v1.5 over SHA-256 against the JWKS key for `kid`. |

Ordering is fail-closed: the header pins `alg`/`kid`, then the SIGNATURE is
verified against the JWKS key BEFORE any claim value is parsed or trusted, and
only then are `iss`/`aud`/time/custom-claim rules applied. A forged token only
ever sees the generic signature verdict — there is no error oracle that
reveals which claim rule failed.

### JWKS cache and rotation

- Keys are cached in memory (kid → RSA public key) after the first fetch.
- An **unknown `kid` triggers exactly ONE JWKS refresh** (the rotation path:
  the fresh key set replaces the cache), then a final lookup. A still-unknown
  kid fails closed. A rotated-out key fails closed once the cache is replaced.
- The fetch is bounded: 1 MiB response cap, 10s client timeout, caller context.
- Non-RSA / `enc`-use / other-alg keys are filtered out (they are irrelevant to
  RS256); a key that claims to be an RS256 signing key but is malformed fails
  the whole refresh (never trust a partial set).

### No raw token persistence

The validator holds no token material: raw tokens exist only as local
variables inside `Validate`; the JWKS cache is keyed by `kid`; the returned
`OIDCClaims` carries only `sub`, tenant, company and `iat`. The resolver never
calls the session path for OIDC (no SHA-256 hash lookup, no session write) —
tests prove the store sees zero hash lookups on the OIDC path.

## 3. Resolver integration (`internal/auth/resolver.go`)

`Resolver.Authenticate(ctx, {Method: oidc, Credential: <access token>})`:

1. Fails closed with `AUTHENTICATION_REQUIRED` when the resolver carries no
   configured validator (the previous Step 1 behavior).
2. `OIDCValidator.Validate` → verified claims or `PRINCIPAL_INVALID`.
3. `Sessions.LookupMembershipByScope(sub, tenant, company)` — the DB
   cross-check; missing tuple → `PRINCIPAL_INVALID`.
4. Inactive membership or inactive company → `MEMBERSHIP_INACTIVE`.
5. Builds the principal: `authenticationMethod=oidc`,
   `assuranceLevel=standard`, `authenticatedAt` from `iat`, `sessionId=""`
   (stateless), `companyScopes=[membership.company_id]`, roles from the
   membership. Policy authorization (role matrix, SoD, materiality) is
   unchanged.

`SessionStore` gains `LookupMembershipByScope(ctx, subjectID, tenantID,
companyID)`; the SQLite implementation shares the membership row load with
`LoadMembership` (one query by the unique tuple).

## 4. HTTP wiring (`internal/server/http.go`)

- `NewHTTPServer(...)` and `NewHTTPServerWithDefaultScope(...)` are unchanged
  (OIDC stays disabled by default).
- `h.EnableOIDC(cfg)` validates the config (fail closed) and attaches the
  validator to the resolver.
- The `authenticate` middleware routes credentials deterministically: when OIDC
  is enabled, **JWT-shaped** credentials (three dot-separated segments) go to
  OIDC validation; every other credential resolves through the session store.
  The same credential is never tried on both paths. Session/service
  credentials are high-entropy opaque strings (hex), never JWT-shaped, so the
  discriminator is safe.
- A rejected credential stores the typed error; handlers keep answering 401
  `AUTHENTICATION_REQUIRED` / `PRINCIPAL_INVALID` exactly as before. Error-code
  → HTTP-status mappings are unchanged.

## 5. Configuration (`drenyra-engram serve`)

Enable stateless OIDC by setting the environment variables below. If ANY
`DRENYRA_OIDC_*` variable is set, the whole set must be valid and complete —
`serve` refuses to start otherwise.

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `DRENYRA_OIDC_ISSUER` | yes (to enable) | — | Exact `iss`; must be an https URL (e.g. `https://drenyra.eu.auth0.com/`). |
| `DRENYRA_OIDC_AUDIENCE` | yes (to enable) | — | Exact resource-server `aud` (e.g. `https://engram.drenyra.local/api`). |
| `DRENYRA_OIDC_JWKS_URL` | no | `<issuer>/.well-known/jwks.json` | JWKS endpoint; must be https when set. |
| `DRENYRA_OIDC_CLAIM_TENANT` | no | `tenant_id` | Custom claim carrying the tenant id. |
| `DRENYRA_OIDC_CLAIM_COMPANY` | no | `company_id` | Custom claim carrying the company id. |
| `DRENYRA_OIDC_CLOCK_SKEW` | no | `30s` | Go duration; must be 0..5m. |

Example (Auth0):

```bash
export DRENYRA_OIDC_ISSUER='https://drenyra.eu.auth0.com/'
export DRENYRA_OIDC_AUDIENCE='https://engram.drenyra.local/api'
drenyra-engram serve
```

The Auth0 application issuing the access token must include the custom claims
in the token payload (Action or RBAC mapping), e.g. `tenant_id` and
`company_id`, with values matching the `memberships` rows (`tenant_id`,
`company_id` columns). The audience must be the engine's resource-server
identifier and the token must be signed by the tenant's signing key (RS256;
HS256-issued tokens are rejected — enable "RS256" as the signing algorithm in
the Auth0 tenant settings).

Startup line prints `(oidc access tokens enabled)` when configured; the shared
`--token` guard and the OIDC validation are independent layers (the shared
token is a transport guard, never identity).

## 6. Tests

- `internal/auth/oidc_config_test.go` — fail-closed configuration/default
  derivation plus pure JWT claim parsing checks.
- `internal/auth/oidc_jwks_test.go` — TLS JWKS fixtures, cache behavior,
  one-refresh key rotation, malformed-key rejection, and RS256 verification.
- `internal/auth/oidc_test.go` — end-to-end valid-token and
  signature/issuer/audience/algorithm/time/required-claim failure matrix,
  custom claim names, and no-raw-token-retention proof.
- `internal/auth/resolver_oidc_test.go` — principal factory over the fake
  store keyed by (subject|tenant|company): resolution, claim/membership
  mismatch, no membership, inactive membership, rejected token never touches
  the store, stateless proof (zero hash lookups, no session id).
- `internal/store/session_store_test.go` — `LookupMembershipByScope` resolves
  the unique tuple with roles/company-active; wrong company and unknown subject
  fail closed.
- `internal/server/http_oidc_test.go` — end-to-end HTTP: valid access token
  authorizes the approval route, session credentials still work with OIDC
  enabled, bad signature → 401 `PRINCIPAL_INVALID`, claim/membership mismatch →
  401 `PRINCIPAL_INVALID`.

## 7. Risks and explicit non-goals

- **No token TTL on the JWKS cache yet** — rotation is picked up through the
  unknown-kid refresh. **Compromise-specific implication:** removing a
  compromised key from the issuer's JWKS does NOT by itself invalidate the
  cached key — tokens signed with it stay trusted until a request bearing a
  different unknown `kid` forces a refresh or the process restarts. Bounded
  controls: the cache is in-memory (per-process), the refresh is a single
  bounded fetch per unknown `kid`, and an operator who suspects key compromise
  MUST restart the serve process (or trigger a JWKS refresh) to drop the
  cached key. A TTL-based proactive refresh is a later hardening.
- **No ACR/MFA elevation**: `acr`/`amr` are ignored; assurance is always
  `standard`. Elevation is a separate slice.
- **No ID tokens, no browser flows, no refresh tokens**: the engine remains a
  resource server; consumer applications own the Auth0 front-channel flows.
- **CLI remains session-based**: OIDC access tokens are only accepted on the
  HTTP serve surface (the middleware discriminator). `auth login` is unchanged.
- **No user-provisioning**: a token for a `sub` without an active membership
  row fails closed; membership provisioning is out of scope.
- **JWKS over plain HTTP is rejected** at configuration time (https only).
