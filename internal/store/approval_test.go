// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module freezes the authenticated
// approval path (v0.4.0 Step 1, ADR-003) at the store boundary: the atomic
// ApproveMemory operation against the expected envelope hash, the frozen error
// codes, idempotency replay/conflict, and the cross-process concurrency proof.
//
// Principals are minted through auth.Resolver.Authenticate with a fake session
// store — the SAME path production middleware uses; there is no public
// arbitrary-input principal constructor.

package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// ──────────────────────────────────────────────
// Principal fixtures (resolver-minted, never caller-declared)
// ──────────────────────────────────────────────

// fixedSessionStore ignores the token hash and returns the configured
// session/membership (or the configured error) — enough to mint fixtures.
type fixedSessionStore struct {
	session    auth.StoredSession
	membership auth.MembershipRecord
	lookupErr  error
}

func (f *fixedSessionStore) LookupByTokenHash(context.Context, string) (auth.StoredSession, error) {
	if f.lookupErr != nil {
		return auth.StoredSession{}, f.lookupErr
	}
	return f.session, nil
}

func (f *fixedSessionStore) LoadMembership(context.Context, string) (auth.MembershipRecord, error) {
	return f.membership, nil
}

func fixtureSessionStore(tenantID, companyID string, roles []auth.AccountingRole, assurance auth.AssuranceLevel) auth.SessionStore {
	return &fixedSessionStore{
		session: auth.StoredSession{
			ID:                   "session-1",
			MembershipID:         "membership-1",
			AuthenticationMethod: auth.AuthMethodSession,
			AssuranceLevel:       assurance,
			AuthenticatedAt:      "2026-08-05T12:00:00Z",
			ExpiresAt:            time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		},
		membership: auth.MembershipRecord{
			ID:            "membership-1",
			SubjectID:     "subject-1",
			TenantID:      tenantID,
			CompanyID:     companyID,
			Status:        "active",
			Roles:         roles,
			CompanyActive: true,
		},
	}
}

func mustPrincipal(t *testing.T, store auth.SessionStore) auth.VerifiedApprovalPrincipal {
	t.Helper()
	resolver := &auth.Resolver{Sessions: store, Mode: auth.RuntimeProduction}
	p, err := resolver.Authenticate(context.Background(), auth.AuthenticationAssertion{
		Method:     auth.AuthMethodSession,
		Credential: "fixture-token",
	})
	if err != nil {
		t.Fatalf("fixture principal: %v", err)
	}
	return p
}

// controllerPrincipal is a controller in tenant-1/acme with standard assurance.
func controllerPrincipal(t *testing.T) auth.VerifiedApprovalPrincipal {
	return mustPrincipal(t, fixtureSessionStore(testOrgID, "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard))
}

// seedAcmeIdentity seeds the FK chain (companies → memberships →
// membership_roles) the approval event requires: the fixture principal's
// membership (membership-1/subject-1) in tenant-1/acme.
func seedAcmeIdentity(t *testing.T, s *SQLiteStore, roles []auth.AccountingRole) {
	t.Helper()
	if err := s.SeedIdentity(IdentitySeed{
		TenantID:     testOrgID,
		CompanyID:    "acme",
		CompanyRUC:   testRucA,
		CompanyName:  "ACME SAC",
		MembershipID: "membership-1",
		SubjectID:    "subject-1",
		Roles:        roles,
	}); err != nil {
		t.Fatalf("seed identity: %v", err)
	}
}

// ──────────────────────────────────────────────
// Save helpers
// ──────────────────────────────────────────────

// gatedInput is a fiscalEffect=closing save: it lands pending_review behind the
// human gate and can only reach approved via an authenticated approval.
func gatedInput(topicKey, what string) core.SaveInput {
	input := validInput(topicKey, what)
	input.FiscalEffect = core.FiscalEffectClosing
	return input
}

// currentEnvelope is the FRESH envelope of a just-saved memory (no links): the
// WriteResult memory carries the canonical content hash, so computing the
// envelope over it yields the H1 the reviewer would see.
func currentEnvelope(saved core.WriteResult) string {
	return core.ComputeEnvelopeHash(saved.Memory)
}

// openTestStorePath opens a store at an explicit path (concurrency tests open
// two INDEPENDENT stores against ONE WAL database file).
func openTestStorePath(t *testing.T, path string) *SQLiteStore {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open store at %s: %v", path, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// approve runs one approval with the given request id and expected hash.
func approve(s *SQLiteStore, id, expected, requestID string, principal auth.VerifiedApprovalPrincipal) (core.ApprovalResult, error) {
	return s.ApproveMemory(context.Background(), core.ApproveMemoryCommand{
		MemoryID:             id,
		ExpectedEnvelopeHash: expected,
		Reason:               "approved by fixture reviewer",
		RequestID:            requestID,
	}, principal, authz.NewApprovalPolicy())
}

func countRows(t *testing.T, s *SQLiteStore, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// ──────────────────────────────────────────────
// Happy path
// ──────────────────────────────────────────────

func TestApproveMemoryHappyPath(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	saved, err := s.Save(gatedInput("tax.igv.approve", "needs approval"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := saved.Memory.Identity.ID
	expected := currentEnvelope(saved)
	principal := controllerPrincipal(t)

	result, err := approve(s, id, expected, "req-happy", principal)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}

	if result.MemoryID != id {
		t.Errorf("MemoryID = %q, want %q", result.MemoryID, id)
	}
	if result.ApprovalEventID == "" {
		t.Error("ApprovalEventID must not be empty")
	}
	if result.PreviousStatus != "pending_review" || result.CurrentStatus != "approved" {
		t.Errorf("statuses = %s → %s, want pending_review → approved", result.PreviousStatus, result.CurrentStatus)
	}
	if result.ReviewedEnvelopeHash != expected {
		t.Errorf("ReviewedEnvelopeHash = %s, want expected %s", result.ReviewedEnvelopeHash, expected)
	}
	if result.ResultingEnvelopeHash == expected {
		t.Error("ResultingEnvelopeHash must differ from ReviewedEnvelopeHash (H2 != H1)")
	}
	if result.PrincipalSubjectID != principal.SubjectID() {
		t.Errorf("PrincipalSubjectID = %s, want %s", result.PrincipalSubjectID, principal.SubjectID())
	}
	if result.MembershipID != principal.MembershipID() {
		t.Errorf("MembershipID = %s, want %s", result.MembershipID, principal.MembershipID())
	}
	if result.PolicyVersion != authz.PolicyVersion {
		t.Errorf("PolicyVersion = %s, want %s", result.PolicyVersion, authz.PolicyVersion)
	}
	if result.ApprovedAt == "" {
		t.Error("ApprovedAt must not be empty")
	}
	if result.IdempotentReplay {
		t.Error("fresh approval must not be a replay")
	}

	// The persisted memory: approved, envelope cache updated to H2.
	mem, ok := s.FindByID(id)
	if !ok {
		t.Fatal("approved memory not found")
	}
	if mem.Status != core.StatusApproved {
		t.Errorf("status = %s, want approved", mem.Status)
	}
	if mem.EnvelopeHash != result.ResultingEnvelopeHash {
		t.Errorf("stored envelope cache = %s, want H2 %s", mem.EnvelopeHash, result.ResultingEnvelopeHash)
	}

	// One immutable approval event with the canonical fields.
	if n := countRows(t, s, `SELECT COUNT(*) FROM approval_events WHERE memory_id = ?`, id); n != 1 {
		t.Fatalf("approval_events rows = %d, want 1", n)
	}
	var (
		action, fromStatus, toStatus, reasonCode, method, assurance string
		actor, membership, fiscalPeriod, tenantID, companyID        string
	)
	if err := s.db.QueryRow(`
		SELECT action, from_status, to_status, authorization_reason_code,
		       authentication_method, assurance_level, principal_subject_id,
		       membership_id, fiscal_period_id, tenant_id, company_id
		FROM approval_events WHERE memory_id = ?`, id).Scan(
		&action, &fromStatus, &toStatus, &reasonCode, &method, &assurance,
		&actor, &membership, &fiscalPeriod, &tenantID, &companyID,
	); err != nil {
		t.Fatalf("read approval event: %v", err)
	}
	if action != "approved" || fromStatus != "pending_review" || toStatus != "approved" || reasonCode != authz.ReasonAuthorized {
		t.Errorf("event = %s %s→%s code %s, want approved pending_review→approved AUTHORIZED", action, fromStatus, toStatus, reasonCode)
	}
	if method != string(auth.AuthMethodSession) || assurance != string(auth.AssuranceStandard) {
		t.Errorf("event auth = %s/%s, want session/standard", method, assurance)
	}
	if actor != "subject-1" || membership != "membership-1" {
		t.Errorf("event principal = %s/%s, want subject-1/membership-1", actor, membership)
	}
	if fiscalPeriod != testPeriod || tenantID != testOrgID || companyID != "acme" {
		t.Errorf("event scope = %s/%s/%s, want %s/%s/%s", tenantID, companyID, fiscalPeriod, testOrgID, "acme", testPeriod)
	}

	// One legacy transition mirror with the same single timestamp.
	if n := countRows(t, s, `SELECT COUNT(*) FROM transition_log WHERE observation_id = ?`, id); n != 1 {
		t.Fatalf("transition_log rows = %d, want 1", n)
	}
	var from, to, tActor, tKind string
	if err := s.db.QueryRow(`SELECT from_status, to_status, actor, actor_kind FROM transition_log WHERE observation_id = ?`, id).Scan(&from, &to, &tActor, &tKind); err != nil {
		t.Fatalf("read transition: %v", err)
	}
	if from != "pending_review" || to != "approved" || tActor != "subject-1" || tKind != string(core.ActorKindHuman) {
		t.Errorf("transition = %s→%s actor %s kind %s, want pending_review→approved subject-1 human", from, to, tActor, tKind)
	}

	// The approval event is immutable (schema triggers).
	if _, err := s.db.Exec(`UPDATE approval_events SET reason = 'mutated' WHERE memory_id = ?`, id); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_APPROVAL_EVENT") {
		t.Fatalf("UPDATE on approval_events must abort with IMMUTABLE_APPROVAL_EVENT, got %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM approval_events WHERE memory_id = ?`, id); err == nil || !strings.Contains(err.Error(), "IMMUTABLE_APPROVAL_EVENT") {
		t.Fatalf("DELETE on approval_events must abort with IMMUTABLE_APPROVAL_EVENT, got %v", err)
	}
}

// ──────────────────────────────────────────────
// Envelope integrity (post-review links change H1)
// ──────────────────────────────────────────────

func TestApproveMemoryEnvelopeMismatchAfterEvidenceLink(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	saved, _ := s.Save(gatedInput("tax.igv.stale-evidence", "needs approval"))
	id := saved.Memory.Identity.ID
	staleExpected := currentEnvelope(saved)

	// A link added AFTER the review changes the canonical refs → new actual H1.
	if err := s.AddEvidenceLink(id, "xml:F001-948", "maria.torres"); err != nil {
		t.Fatalf("add evidence link: %v", err)
	}
	fresh, ok := s.FindByID(id)
	if !ok {
		t.Fatal("memory not found")
	}
	if fresh.EnvelopeHash == staleExpected {
		t.Fatal("link writer must refresh the derived envelope cache (new H1)")
	}

	_, err := approve(s, id, staleExpected, "req-stale-evidence", controllerPrincipal(t))
	if auth.Code(err) != auth.CodeEnvelopeMismatch {
		t.Fatalf("code = %q, want ENVELOPE_MISMATCH", auth.Code(err))
	}
	var ae *auth.Error
	if !errors.As(err, &ae) {
		t.Fatalf("error %v is not an auth.Error", err)
	}
	if ae.ExpectedEnvelopeHash != staleExpected {
		t.Errorf("ExpectedEnvelopeHash = %s, want the stale reviewed hash", ae.ExpectedEnvelopeHash)
	}
	if ae.ActualEnvelopeHash != fresh.EnvelopeHash {
		t.Errorf("ActualEnvelopeHash = %s, want current %s", ae.ActualEnvelopeHash, fresh.EnvelopeHash)
	}

	// The failed approval left NO event and NO reservation behind (rollback).
	if n := countRows(t, s, `SELECT COUNT(*) FROM approval_events WHERE memory_id = ?`, id); n != 0 {
		t.Errorf("approval_events rows = %d, want 0 (rolled back)", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM idempotency_keys`); n != 0 {
		t.Errorf("idempotency_keys rows = %d, want 0 (reservation rolled back)", n)
	}
	mem, _ := s.FindByID(id)
	if mem.Status != core.StatusPendingReview {
		t.Errorf("status = %s, want pending_review (no partial approval)", mem.Status)
	}
}

func TestApproveMemoryEnvelopeMismatchAfterRuleLink(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	saved, _ := s.Save(gatedInput("tax.igv.stale-rule", "needs approval"))
	id := saved.Memory.Identity.ID
	staleExpected := currentEnvelope(saved)

	if err := s.AddRuleLink(id, "policy/igv/late-document-v3", "maria.torres"); err != nil {
		t.Fatalf("add rule link: %v", err)
	}
	fresh, _ := s.FindByID(id)
	if fresh.EnvelopeHash == staleExpected {
		t.Fatal("rule link must refresh the derived envelope cache")
	}

	_, err := approve(s, id, staleExpected, "req-stale-rule", controllerPrincipal(t))
	if auth.Code(err) != auth.CodeEnvelopeMismatch {
		t.Fatalf("code = %q, want ENVELOPE_MISMATCH", auth.Code(err))
	}
}

// ──────────────────────────────────────────────
// Status / existence failures
// ──────────────────────────────────────────────

func TestApproveMemoryMemoryNotFound(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	_, err := approve(s, "missing-memory", "0", "req-missing", controllerPrincipal(t))
	if auth.Code(err) != auth.CodeMemoryNotFound {
		t.Fatalf("code = %q, want MEMORY_NOT_FOUND", auth.Code(err))
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM idempotency_keys`); n != 0 {
		t.Errorf("idempotency_keys rows = %d, want 0 (reservation rolled back)", n)
	}
}

func TestApproveMemoryInvalidTransitionFromActive(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	// fiscalEffect none → saved active (no gate); approval is not legal.
	saved, err := s.Save(validInput("tax.igv.informative", "informative"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	_, err = approve(s, saved.Memory.Identity.ID, currentEnvelope(saved), "req-active", controllerPrincipal(t))
	if auth.Code(err) != auth.CodeInvalidTransition {
		t.Fatalf("code = %q, want INVALID_TRANSITION", auth.Code(err))
	}
}

// ──────────────────────────────────────────────
// Scope / policy denials (frozen codes)
// ──────────────────────────────────────────────

func TestApproveMemoryTenantScopeMismatch(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	saved, _ := s.Save(gatedInput("tax.igv.cross-tenant", "needs approval"))

	foreign := mustPrincipal(t, fixtureSessionStore("tenant-other", "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard))
	_, err := approve(s, saved.Memory.Identity.ID, currentEnvelope(saved), "req-tenant", foreign)
	if auth.Code(err) != auth.CodeTenantScopeMismatch {
		t.Fatalf("code = %q, want TENANT_SCOPE_MISMATCH", auth.Code(err))
	}
}

func TestApproveMemoryCompanyScopeDenied(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	saved, _ := s.Save(gatedInput("tax.igv.cross-company", "needs approval"))

	outsider := mustPrincipal(t, fixtureSessionStore(testOrgID, "other-company", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard))
	_, err := approve(s, saved.Memory.Identity.ID, currentEnvelope(saved), "req-company", outsider)
	if auth.Code(err) != auth.CodeCompanyScopeDenied {
		t.Fatalf("code = %q, want COMPANY_SCOPE_DENIED", auth.Code(err))
	}
}

func TestApproveMemoryRoleNotAuthorized(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleAccountant})
	saved, _ := s.Save(gatedInput("tax.igv.role", "closing needs a controller"))

	accountant := mustPrincipal(t, fixtureSessionStore(testOrgID, "acme", []auth.AccountingRole{auth.RoleAccountant}, auth.AssuranceStandard))
	_, err := approve(s, saved.Memory.Identity.ID, currentEnvelope(saved), "req-role", accountant)
	if auth.Code(err) != auth.CodeRoleNotAuthorized {
		t.Fatalf("code = %q, want ROLE_NOT_AUTHORIZED", auth.Code(err))
	}
}

func TestApproveMemoryAssuranceTooLow(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleAuthorizedTaxProfessional})
	input := validInput("tax.igv.filing", "sunat filing")
	input.FiscalEffect = core.FiscalEffectSunatFiling
	saved, err := s.Save(input)
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	// Authorized tax professional with only STANDARD assurance: role fits,
	// sunat_filing additionally requires strong.
	standard := mustPrincipal(t, fixtureSessionStore(testOrgID, "acme", []auth.AccountingRole{auth.RoleAuthorizedTaxProfessional}, auth.AssuranceStandard))
	_, err = approve(s, saved.Memory.Identity.ID, currentEnvelope(saved), "req-assurance", standard)
	if auth.Code(err) != auth.CodeAssuranceTooLow {
		t.Fatalf("code = %q, want ASSURANCE_TOO_LOW", auth.Code(err))
	}
}

func TestApproveMemoryMaterialityLimitExceeded(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleSeniorAccountant})
	input := validInput("tax.igv.critical", "critical adjustment")
	input.FiscalEffect = core.FiscalEffectAdjustment
	level := core.MaterialityCritical
	input.MaterialityLevel = &level
	saved, err := s.Save(input)
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	// Senior accountant on a CRITICAL adjustment: the materiality ladder raises
	// the requirement to controller.
	senior := mustPrincipal(t, fixtureSessionStore(testOrgID, "acme", []auth.AccountingRole{auth.RoleSeniorAccountant}, auth.AssuranceStandard))
	_, err = approve(s, saved.Memory.Identity.ID, currentEnvelope(saved), "req-materiality", senior)
	if auth.Code(err) != auth.CodeMaterialityLimitExceeded {
		t.Fatalf("code = %q, want MATERIALITY_LIMIT_EXCEEDED", auth.Code(err))
	}
}

func TestApproveMemoryMembershipInactive(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	saved, _ := s.Save(gatedInput("tax.igv.membership", "needs approval"))

	// A membership record without an id is not a real active membership: the
	// resolver derives a principal with an EMPTY membership id.
	store := fixtureSessionStore(testOrgID, "acme", []auth.AccountingRole{auth.RoleController}, auth.AssuranceStandard)
	fixed := store.(*fixedSessionStore)
	fixed.membership.ID = ""
	principal := mustPrincipal(t, store)
	if principal.MembershipID() != "" {
		t.Fatalf("fixture must carry an empty membership id, got %q", principal.MembershipID())
	}

	_, err := approve(s, saved.Memory.Identity.ID, currentEnvelope(saved), "req-membership", principal)
	if auth.Code(err) != auth.CodeMembershipInactive {
		t.Fatalf("code = %q, want MEMBERSHIP_INACTIVE", auth.Code(err))
	}
}

// ──────────────────────────────────────────────
// Command syntax (store-level defense in depth)
// ──────────────────────────────────────────────

func TestApproveMemoryReasonRequired(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	saved, _ := s.Save(gatedInput("tax.igv.reason", "needs approval"))
	id := saved.Memory.Identity.ID
	principal := controllerPrincipal(t)

	for _, reason := range []string{"", "   ", "\t\n"} {
		_, err := s.ApproveMemory(context.Background(), core.ApproveMemoryCommand{
			MemoryID:             id,
			ExpectedEnvelopeHash: currentEnvelope(saved),
			Reason:               reason,
			RequestID:            "req-reason",
		}, principal, authz.NewApprovalPolicy())
		if auth.Code(err) != auth.CodeReasonRequired {
			t.Errorf("reason %q: code = %q, want REASON_REQUIRED", reason, auth.Code(err))
		}
	}
}

// ──────────────────────────────────────────────
// Idempotency: replay, conflict, no duplicate events
// ──────────────────────────────────────────────

func TestApproveMemoryIdempotentReplay(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	saved, _ := s.Save(gatedInput("tax.igv.replay", "needs approval"))
	id := saved.Memory.Identity.ID
	expected := currentEnvelope(saved)
	principal := controllerPrincipal(t)

	first, err := approve(s, id, expected, "req-replay", principal)
	if err != nil {
		t.Fatalf("first approve: %v", err)
	}
	if first.IdempotentReplay {
		t.Fatal("first approval must not be a replay")
	}

	second, err := approve(s, id, expected, "req-replay", principal)
	if err != nil {
		t.Fatalf("replay approve: %v", err)
	}
	if !second.IdempotentReplay {
		t.Fatal("replay must set IdempotentReplay=true")
	}
	if second.ApprovalEventID != first.ApprovalEventID {
		t.Errorf("replay event id = %s, want %s (same committed result)", second.ApprovalEventID, first.ApprovalEventID)
	}
	if second.MemoryID != first.MemoryID || second.ReviewedEnvelopeHash != first.ReviewedEnvelopeHash || second.ResultingEnvelopeHash != first.ResultingEnvelopeHash {
		t.Error("replay must return the stored result unchanged")
	}
	if second.IdempotentReplay != true {
		t.Error("replayed result must keep IdempotentReplay=true")
	}

	// Retry after the committed result never duplicates the immutable event.
	if n := countRows(t, s, `SELECT COUNT(*) FROM approval_events WHERE memory_id = ?`, id); n != 1 {
		t.Errorf("approval_events rows = %d, want exactly 1 (no duplicate on replay)", n)
	}
}

func TestApproveMemoryIdempotencyConflict(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	saved, _ := s.Save(gatedInput("tax.igv.conflict", "needs approval"))
	id := saved.Memory.Identity.ID
	expected := currentEnvelope(saved)
	principal := controllerPrincipal(t)

	if _, err := approve(s, id, expected, "req-conflict", principal); err != nil {
		t.Fatalf("first approve: %v", err)
	}
	// Same request id, DIFFERENT payload (stale hash) → the command hash
	// differs → IDEMPOTENCY_CONFLICT (never a second approval).
	_, err := approve(s, id, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "req-conflict", principal)
	if auth.Code(err) != auth.CodeIdempotencyConflict {
		t.Fatalf("code = %q, want IDEMPOTENCY_CONFLICT", auth.Code(err))
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM approval_events WHERE memory_id = ?`, id); n != 1 {
		t.Errorf("approval_events rows = %d, want exactly 1", n)
	}
}

// ──────────────────────────────────────────────
// Concurrency: two INDEPENDENT stores, one WAL file
// ──────────────────────────────────────────────

// TestApproveMemoryConcurrentSingleEvent is the cross-process race proof: two
// independently opened stores (separate *sql.DB handles, MaxOpenConns(1) cannot
// serialize them) approve the SAME memory concurrently. BEGIN IMMEDIATE closes
// the race: exactly ONE approval wins, the loser reads the committed approved
// status and returns ALREADY_DECIDED, and exactly ONE event + reservation
// survive. Run with -race.
func TestApproveMemoryConcurrentSingleEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engram-concurrent.db")
	s1 := openTestStorePath(t, path)
	s2 := openTestStorePath(t, path)

	seedAcmeIdentity(t, s1, []auth.AccountingRole{auth.RoleController})
	saved, err := s1.Save(gatedInput("tax.igv.concurrent", "concurrent approval"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := saved.Memory.Identity.ID
	expected := currentEnvelope(saved)
	principal := controllerPrincipal(t)
	policy := authz.NewApprovalPolicy()

	start := make(chan struct{})
	results := make([]core.ApprovalResult, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			st := s1
			if i == 1 {
				st = s2
			}
			results[i], errs[i] = st.ApproveMemory(context.Background(), core.ApproveMemoryCommand{
				MemoryID:             id,
				ExpectedEnvelopeHash: expected,
				Reason:               "concurrent approval",
				RequestID:            fmt.Sprintf("req-concurrent-%d", i),
			}, principal, policy)
		}(i)
	}
	close(start)
	wg.Wait()

	approved, decided := 0, 0
	for i := 0; i < 2; i++ {
		switch {
		case errs[i] == nil:
			approved++
			if results[i].IdempotentReplay {
				t.Fatalf("goroutine %d must be a fresh approval, not a replay", i)
			}
		case auth.Code(errs[i]) == auth.CodeAlreadyDecided:
			decided++
		default:
			t.Fatalf("goroutine %d: unexpected error %v (code %q)", i, errs[i], auth.Code(errs[i]))
		}
	}
	if approved != 1 || decided != 1 {
		t.Fatalf("approved=%d decided=%d, want exactly 1 APPROVED and 1 ALREADY_DECIDED", approved, decided)
	}

	if n := countRows(t, s1, `SELECT COUNT(*) FROM approval_events`); n != 1 {
		t.Errorf("approval_events rows = %d, want exactly 1", n)
	}
	if n := countRows(t, s1, `SELECT COUNT(*) FROM idempotency_keys`); n != 1 {
		t.Errorf("idempotency_keys rows = %d, want exactly 1 (loser reservation rolled back)", n)
	}
	mem, ok := s1.FindByID(id)
	if !ok || mem.Status != core.StatusApproved {
		t.Fatalf("memory status = %v, want approved", mem.Status)
	}
}
