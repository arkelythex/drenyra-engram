// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money.
//
// v0.4.0 Step 4 — offline verification service (VerifyMemory / VerifyJudgment /
// VerifyReceipt) end-to-end against a real store with the real keyring signer.
// AC7: a removed evidence link is detected by the evidence-availability layer.
// AC12: every report — all-pass AND failed — ends with
// "Accounting correctness: NOT ASSERTED".

package server

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/receipts"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

// verifyStore opens a fresh store with the REAL keyring signer attached, so
// every covered act emits an Ed25519 receipt.
func verifyStore(t *testing.T) (*store.SQLiteStore, string) {
	t.Helper()
	keyringPath := t.TempDir() + "/signing-keys.json"
	if _, err := receipts.EnsureActiveKey(keyringPath); err != nil {
		t.Fatalf("ensure keyring: %v", err)
	}
	dbPath := t.TempDir() + "/verify.db"
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	signer := receipts.NewSigner(st, keyringPath)
	st.SetReceiptSigner(signer)
	return st, dbPath
}

// verifyIdentity seeds an identity + session on the store (mirror of
// seedApprovalIdentity without the API) and returns the session token.
func verifyIdentity(t *testing.T, st *store.SQLiteStore, tenantID, companyID string, roles []auth.AccountingRole) string {
	t.Helper()
	membershipID := "membership-" + tenantID
	if err := st.SeedIdentity(store.IdentitySeed{
		TenantID:     tenantID,
		CompanyID:    companyID,
		CompanyRUC:   "20100039201",
		CompanyName:  "Demo SAC",
		MembershipID: membershipID,
		SubjectID:    "maria.torres",
		Roles:        roles,
	}); err != nil {
		t.Fatalf("seed identity: %v", err)
	}
	token := "verify-token-" + tenantID
	if err := st.SeedSession(store.SessionSeed{
		ID:                   "session-" + tenantID,
		TokenHash:            sha256HexString(token),
		MembershipID:         membershipID,
		AuthenticationMethod: auth.AuthMethodSession,
		AssuranceLevel:       auth.AssuranceStandard,
		AuthenticatedAt:      "2026-08-05T12:00:00Z",
		ExpiresAt:            time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return token
}

// verifyPrincipal resolves the seeded session token into a verified principal.
func verifyPrincipal(t *testing.T, st *store.SQLiteStore, token string) auth.VerifiedApprovalPrincipal {
	t.Helper()
	resolver := &auth.Resolver{Sessions: st, Mode: auth.RuntimeLocalDev}
	principal, err := resolver.Authenticate(context.Background(), auth.AuthenticationAssertion{
		Method:     auth.AuthMethodSession,
		Credential: token,
	})
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	return principal
}

// verifySave stores a gated memory (adjustment → pending_review) under the
// given tenant/company and returns its id.
func verifySave(t *testing.T, st *store.SQLiteStore, tenantID, companyID, topicKey string) string {
	t.Helper()
	wr, err := st.Save(core.SaveInput{
		TopicKey: topicKey,
		Title:    "IGV adjustment",
		Kind:     core.KindException,
		Scope: core.Scope{
			Kind:           core.ScopeKindCompany,
			OrganizationID: tenantID,
			CompanyID:      companyID,
			RUC:            "20100039201",
			Period:         "202401",
		},
		Content: core.Content{
			What:    "XML y factura difieren en la base",
			Why:     "proveedor corrigió la base",
			Where:   "Peru",
			Learned: "documentar la corrección",
		},
		FiscalEffect: core.FiscalEffectAdjustment,
		EffectiveAt:  "2024-01-15T00:00:00.000Z",
		Source:       core.Source{System: "verify-test", ActorID: "agent-1", ActorKind: core.ActorKindAgent},
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	return wr.Memory.Identity.ID
}

func TestVerifyServiceMemoryValid(t *testing.T) {
	st, _ := verifyStore(t)
	id := verifySave(t, st, "t9", "c9", "verify/valid")

	report, err := VerifyMemory(context.Background(), st, id)
	if err != nil {
		t.Fatalf("VerifyMemory: %v", err)
	}
	if report.Outcome != core.VerificationOutcomePassed {
		t.Fatalf("outcome = %s, want passed (%+v)", report.Outcome, report)
	}
	if report.AccountingCorrectness != core.AccountingCorrectnessNotAsserted {
		t.Fatalf("conclusion = %q, want %q", report.AccountingCorrectness, core.AccountingCorrectnessNotAsserted)
	}
	if len(report.Receipts) != 1 {
		t.Fatalf("receipts = %d, want 1 (memory_recorded)", len(report.Receipts))
	}
}

func TestVerifyServiceReceiptByHashAndID(t *testing.T) {
	st, _ := verifyStore(t)
	id := verifySave(t, st, "t9", "c9", "verify/receipt")

	ctx := context.Background()
	chain, err := st.ReceiptsForSubject(ctx, core.SubjectTypeMemory, id)
	if err != nil || len(chain) != 1 {
		t.Fatalf("chain: %v (len %d)", err, len(chain))
	}
	hash := core.ReceiptHash(chain[0])

	byHash, err := VerifyReceipt(ctx, st, core.ReceiptTarget{Hash: hash})
	if err != nil {
		t.Fatalf("VerifyReceipt by hash: %v", err)
	}
	if byHash.Outcome != core.VerificationOutcomePassed {
		t.Fatalf("by-hash outcome = %s, want passed", byHash.Outcome)
	}
}

func TestVerifyServiceMemoryRemovedEvidence(t *testing.T) {
	// AC7: a memory whose envelope commits an evidence ref whose link row was
	// removed (direct SQL — the API has no delete path) fails the
	// evidence-availability layer and the current-envelope recompute diverges.
	st, dbPath := verifyStore(t)
	id := verifySave(t, st, "t9", "c9", "verify/ac7")

	ctx := context.Background()
	if err := st.AddEvidenceLink(id, "xml:FA01-0001", "auditor"); err != nil {
		t.Fatalf("add evidence link: %v", err)
	}

	ok, err := VerifyMemory(ctx, st, id)
	if err != nil {
		t.Fatalf("VerifyMemory before removal: %v", err)
	}
	if ok.Outcome != core.VerificationOutcomePassed {
		t.Fatalf("before removal outcome = %s, want passed", ok.Outcome)
	}

	// Bypass the append-only API: remove the link row through a direct SQLite
	// connection to the same database file.
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw connection: %v", err)
	}
	defer func() { _ = raw.Close() }()
	if _, err := raw.ExecContext(ctx, `DELETE FROM evidence_links WHERE memory_id = ?`, id); err != nil {
		t.Fatalf("remove link row: %v", err)
	}

	failed, err := VerifyMemory(ctx, st, id)
	if err != nil {
		t.Fatalf("VerifyMemory after removal: %v", err)
	}
	if failed.Outcome != core.VerificationOutcomeFailed {
		t.Fatalf("after removal outcome = %s, want failed", failed.Outcome)
	}
	var evidenceFailed bool
	for _, layer := range failed.Layers {
		if layer.Name == "evidence availability" {
			evidenceFailed = layer.Status == core.VerificationFailed
		}
	}
	if !evidenceFailed {
		t.Fatalf("evidence availability layer must fail after link removal: %+v", failed.Layers)
	}
	// AC12: even a failed report ends with the NOT ASSERTED conclusion.
	if failed.AccountingCorrectness != core.AccountingCorrectnessNotAsserted {
		t.Fatalf("failed conclusion = %q, want %q", failed.AccountingCorrectness, core.AccountingCorrectnessNotAsserted)
	}
}

func TestVerifyServiceJudgmentValid(t *testing.T) {
	st, _ := verifyStore(t)
	token := verifyIdentity(t, st, "t9", "c9", []auth.AccountingRole{auth.RoleController})
	principal := verifyPrincipal(t, st, token)

	ctx := context.Background()
	fromID := verifySave(t, st, "t9", "c9", "verify/j1")
	toID := verifySave(t, st, "t9", "c9", "verify/j2")

	proposed, err := st.ProposeJudgment(ctx, core.ProposeJudgmentCommand{
		FromID: fromID, ToID: toID, Relation: core.RelationContradicts,
		Reason: "XML y CDR difieren", RequestID: "verify-propose",
	}, core.Source{System: "verify-test", ActorID: "agent-1", ActorKind: core.ActorKindAgent})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}

	if _, err := st.ConfirmJudgment(ctx, core.ConfirmJudgmentCommand{
		JudgmentID:           proposed.JudgmentID,
		Resolution:           "el CDR es la fuente autoritativa",
		ExpectedJudgmentHash: core.ComputeJudgmentHash(proposed.Judgment),
		RequestID:            "verify-confirm",
	}, principal, authz.NewJudgmentPolicy()); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	report, err := VerifyJudgment(ctx, st, proposed.JudgmentID)
	if err != nil {
		t.Fatalf("VerifyJudgment: %v", err)
	}
	if report.Outcome != core.VerificationOutcomePassed {
		t.Fatalf("outcome = %s, want passed (%+v)", report.Outcome, report)
	}
	if report.AccountingCorrectness != core.AccountingCorrectnessNotAsserted {
		t.Fatalf("conclusion = %q, want %q", report.AccountingCorrectness, core.AccountingCorrectnessNotAsserted)
	}
}

// TestVerifyServiceSigningKeyCutoffMatrix repeats the FZ-3 before/equal/after
// comparisons through the FULL verification service path (real store, real
// keyring signer, real signed receipt chain): a memory signed before the
// authenticated cutoff keeps passing; a memory signed at/after the cutoff
// fails the signing-key validity layer ONLY (every other layer unchanged);
// report construction stays read-only/no-transaction (the report is still
// built and the receipt chain gains no rows). The revocation is applied
// through the engine's ONE legal signing_keys update (RevokePublicKey → the
// revoke-only trigger) on a second connection — never raw SQL. A revoked key
// then refuses to sign any new receipt (store/signing seam, fail closed).
func TestVerifyServiceSigningKeyCutoffMatrix(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name       string
		revokedAt  func(issuedAt string) string
		want       core.VerificationOutcome
		wantDetail string
	}{
		{
			name: "pre-cutoff: issued_at < revoked_at keeps the report passed",
			revokedAt: func(issuedAt string) string {
				ts, err := time.Parse(time.RFC3339, issuedAt)
				if err != nil {
					t.Fatalf("parse issuedAt %q: %v", issuedAt, err)
				}
				return ts.Add(time.Hour).Format(time.RFC3339)
			},
			want: core.VerificationOutcomePassed,
		},
		{
			name:       "exact cutoff: issued_at == revoked_at fails signing-key validity only",
			revokedAt:  func(issuedAt string) string { return issuedAt },
			want:       core.VerificationOutcomeFailed,
			wantDetail: "at/after revocation",
		},
		{
			name: "post-cutoff: issued_at > revoked_at fails signing-key validity only",
			revokedAt: func(issuedAt string) string {
				ts, err := time.Parse(time.RFC3339, issuedAt)
				if err != nil {
					t.Fatalf("parse issuedAt %q: %v", issuedAt, err)
				}
				return ts.Add(-time.Hour).Format(time.RFC3339)
			},
			want:       core.VerificationOutcomeFailed,
			wantDetail: "at/after revocation",
		},
		{
			name:       "unparseable revoked_at fails closed (never a guess)",
			revokedAt:  func(string) string { return "not-a-timestamp" },
			want:       core.VerificationOutcomeFailed,
			wantDetail: "not a valid RFC3339 timestamp",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, dbPath := verifyStore(t)
			id := verifySave(t, st, "t9", "c9", "verify/cutoff")

			chain, err := st.ReceiptsForSubject(ctx, core.SubjectTypeMemory, id)
			if err != nil || len(chain) != 1 {
				t.Fatalf("chain before revocation: %v (len %d)", err, len(chain))
			}
			issuedAt, keyID := chain[0].IssuedAt, chain[0].KeyID

			baseline, err := VerifyMemory(ctx, st, id)
			if err != nil || baseline.Outcome != core.VerificationOutcomePassed {
				t.Fatalf("baseline must pass before revocation: %v (outcome %s)", err, baseline.Outcome)
			}

			// Apply the ONE legal signing_keys update through the engine's own
			// revoke path (the revoke-only trigger) via a second connection.
			raw, err := sql.Open("sqlite", dbPath)
			if err != nil {
				t.Fatalf("open second connection: %v", err)
			}
			defer func() { _ = raw.Close() }()
			revokedAt := tc.revokedAt(issuedAt)
			if err := st.RevokePublicKey(ctx, raw, keyID, revokedAt); err != nil {
				t.Fatalf("revoke %s at %q: %v", keyID, revokedAt, err)
			}

			// The verification service must STILL build the full report
			// (read-only construction), with the FZ-3 outcome.
			report, err := VerifyMemory(ctx, st, id)
			if err != nil {
				t.Fatalf("VerifyMemory after revocation must still build a report (read-only, no transaction): %v", err)
			}
			if report.Outcome != tc.want {
				t.Fatalf("outcome = %s, want %s (layers %+v)", report.Outcome, tc.want, report.Layers)
			}

			// Only the signing-key validity layer may change vs the baseline.
			if len(report.Layers) != len(baseline.Layers) {
				t.Fatalf("layer count changed: %d → %d", len(baseline.Layers), len(report.Layers))
			}
			for i := range baseline.Layers {
				before, after := baseline.Layers[i], report.Layers[i]
				if before.Name != core.LayerSigningKeyValidity {
					if before.Status != after.Status || before.Detail != after.Detail {
						t.Fatalf("layer %q changed: %s %q → %s %q", before.Name, before.Status, before.Detail, after.Status, after.Detail)
					}
					continue
				}
				if tc.want == core.VerificationOutcomePassed {
					if after.Status != core.VerificationPassed {
						t.Fatalf("pre-cutoff signing-key validity must stay passed: %s %q", after.Status, after.Detail)
					}
				} else {
					if after.Status != core.VerificationFailed {
						t.Fatalf("at/after-cutoff signing-key validity must fail: %s %q", after.Status, after.Detail)
					}
					if tc.wantDetail != "" && !strings.Contains(after.Detail, tc.wantDetail) {
						t.Fatalf("detail %q must contain %q", after.Detail, tc.wantDetail)
					}
				}
			}

			// Read-only proof: verification created no receipt rows.
			after, err := st.ReceiptsForSubject(ctx, core.SubjectTypeMemory, id)
			if err != nil || len(after) != 1 || core.ReceiptHash(after[0]) != core.ReceiptHash(chain[0]) {
				t.Fatalf("verification must not mutate the receipt chain: err=%v len=%d", err, len(after))
			}

			// Store/signing seam: a revoked key NEVER signs new receipts — a
			// further covered save fails closed and inserts nothing.
			if _, err := st.Save(core.SaveInput{
				TopicKey:     "verify/cutoff/after-revocation",
				Title:        "post-revocation attempt",
				Kind:         core.KindException,
				Scope:        core.Scope{Kind: core.ScopeKindCompany, OrganizationID: "t9", CompanyID: "c9", RUC: "20100039201", Period: "202401"},
				Content:      core.Content{What: "post-revocation write attempt", Why: "must fail closed", Where: "Peru", Learned: "revoked keys never sign"},
				FiscalEffect: core.FiscalEffectAdjustment,
				EffectiveAt:  "2024-01-16T00:00:00.000Z",
				Source:       core.Source{System: "verify-test", ActorID: "agent-1", ActorKind: core.ActorKindAgent},
			}); err == nil || !strings.Contains(err.Error(), "revoked") {
				t.Fatalf("revoked key must refuse to sign new receipts, got %v", err)
			}
			after, err = st.ReceiptsForSubject(ctx, core.SubjectTypeMemory, id)
			if err != nil || len(after) != 1 {
				t.Fatalf("refused save must not mutate the chain: err=%v len=%d", err, len(after))
			}
		})
	}
}
