// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module freezes the v0.9.0 REVIEW
// WORKSPACE HTTP surface (docs/architecture/review-workspace-v0.9.md §7):
// the scope-first queue/detail reads (GET /accounting/review/*) and the
// AUTHENTICATED reject/return decisions (POST
// /accounting/memories/{memoryId}/reject|return).
//
// Fixtures are end-to-end: identities and sessions are seeded directly on the
// test SQLite store (design section 8 — no environment state), principals are
// minted through the REAL resolver, and every scope guard is exercised over the
// wire (a caller whose exact scope differs sees NOTHING — never the memory).
package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// reviewSave saves a pending_review adjustment recorded by an AGENT (the
// proposer the reviewer must differ from) in the killer-demo scope — the same
// scope shape the review GET routes derive from ?ruc=&companyId=&period=.
func reviewSave(t *testing.T, api *API, topicKey, what string) core.AccountingMemory {
	t.Helper()
	mem := saveOne(t, api, reviewSaveInput(topicKey, what, demoScope()))
	if mem.Status != core.StatusPendingReview {
		t.Fatalf("fixture status = %q, want pending_review (fiscal effect gate)", mem.Status)
	}
	return mem
}

// reviewServer builds a test server plus a demo-tenant controller identity
// (token returned). The memory fixtures are saved by the caller.
func reviewServer(t *testing.T) (*httptest.Server, *API, string) {
	t.Helper()
	ts, api := newTestHTTPServer(t, "")
	token := seedApprovalIdentity(t, api, "cmp_org", "cmp_01", "20601234567",
		[]auth.AccountingRole{auth.RoleController})
	return ts, api, token
}

// reviewScopeQuery is the query string that derives demoScope() on the wire
// (companyId passed explicitly so the HTTP-derived scope equals the stored one).
const reviewScopeQuery = "?ruc=20601234567&companyId=cmp_01&organizationId=cmp_org&period=202607"

func TestHTTPReviewQueueScopeFirst(t *testing.T) {
	ts, api, _ := reviewServer(t)
	mem := reviewSave(t, api, "review.queue.http", "pendiente en el alcance")

	status, raw := httpJSON(t, http.MethodGet, ts.URL+"/accounting/review/queue"+reviewScopeQuery, "", nil)
	if status != http.StatusOK {
		t.Fatalf("queue status = %d, want 200; body %s", status, raw)
	}
	var page core.ReviewQueuePage
	if err := json.Unmarshal([]byte(raw), &page); err != nil {
		t.Fatalf("decode queue: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].MemoryID != mem.Identity.ID {
		t.Fatalf("queue items = %+v, want exactly %s", page.Items, mem.Identity.ID)
	}
	if page.Items[0].EnvelopeHash == "" || page.Items[0].RecordedBy != "test-agent" {
		t.Fatalf("queue item must carry the CURRENT envelope and the proposer: %+v", page.Items[0])
	}

	// Cross-company invisibility over the wire: the same queue with a different
	// company is EMPTY — never another company's pending review.
	status, raw = httpJSON(t, http.MethodGet,
		ts.URL+"/accounting/review/queue?ruc=20600995804&companyId=cmp_99&organizationId=cmp_org&period=202607", "", nil)
	if status != http.StatusOK {
		t.Fatalf("cross-company queue status = %d, want 200; body %s", status, raw)
	}
	var other core.ReviewQueuePage
	if err := json.Unmarshal([]byte(raw), &other); err != nil {
		t.Fatalf("decode cross-company queue: %v", err)
	}
	if len(other.Items) != 0 {
		t.Fatalf("cross-company queue must be empty, got %d items", len(other.Items))
	}
}

func TestHTTPReviewQueueInvalidScopeAndPagination(t *testing.T) {
	ts, _, _ := reviewServer(t)
	// Invalid RUC → 400 INVALID (the frozen scope derivation fails closed).
	status, raw := httpJSON(t, http.MethodGet, ts.URL+"/accounting/review/queue?ruc=123", "", nil)
	if status != http.StatusBadRequest || !strings.Contains(raw, "INVALID_RUC") {
		t.Fatalf("invalid ruc: status = %d body %s, want 400 INVALID_RUC", status, raw)
	}
	// Non-integer limit → 400 INVALID.
	status, raw = httpJSON(t, http.MethodGet, ts.URL+"/accounting/review/queue"+reviewScopeQuery+"&limit=abc", "", nil)
	if status != http.StatusBadRequest || !strings.Contains(raw, "INVALID") {
		t.Fatalf("non-integer limit: status = %d body %s, want 400 INVALID", status, raw)
	}
}

func TestHTTPReviewDetailScopeFirst(t *testing.T) {
	ts, api, _ := reviewServer(t)
	mem := reviewSave(t, api, "review.detail.http", "detalle pendiente")

	status, raw := httpJSON(t, http.MethodGet, ts.URL+"/accounting/review/"+mem.Identity.ID+reviewScopeQuery, "", nil)
	if status != http.StatusOK {
		t.Fatalf("detail status = %d, want 200; body %s", status, raw)
	}
	var detail core.ReviewDetail
	if err := json.Unmarshal([]byte(raw), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.Memory.Identity.ID != mem.Identity.ID {
		t.Fatalf("detail memory = %q, want %q", detail.Memory.Identity.ID, mem.Identity.ID)
	}
	if detail.ReviewMetadata.EnvelopeHashToSign == "" || detail.ReviewMetadata.RecordedBy != "test-agent" {
		t.Fatalf("detail metadata must carry H1 and the proposer: %+v", detail.ReviewMetadata)
	}
	if detail.BoundaryNotice != core.ReviewBoundaryNotice {
		t.Fatalf("boundary notice = %q, want %q", detail.BoundaryNotice, core.ReviewBoundaryNotice)
	}

	// Cross-company detail is MEMORY_NOT_FOUND (404) — invisible, never a leak.
	status, raw = httpJSON(t, http.MethodGet,
		ts.URL+"/accounting/review/"+mem.Identity.ID+"?ruc=20600995804&companyId=cmp_99&organizationId=cmp_org&period=202607", "", nil)
	if status != http.StatusNotFound || !strings.Contains(raw, "MEMORY_NOT_FOUND") {
		t.Fatalf("cross-scope detail: status = %d body %s, want 404 MEMORY_NOT_FOUND", status, raw)
	}
}

// TestHTTPReviewRejectHappyPath: a seeded identity rejects a pending_review
// adjustment against the CURRENT envelope → 200 with the core.RejectMemoryResult
// JSON, pending_review → rejected (terminal), reason persisted, H1 reviewed.
func TestHTTPReviewRejectHappyPath(t *testing.T) {
	ts, api, token := reviewServer(t)
	mem := reviewSave(t, api, "review.reject.http", "rechazable")
	h1 := core.ComputeEnvelopeHash(mem)

	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/memories/"+mem.Identity.ID+"/reject",
		token, "review-reject-1", map[string]string{
			"expectedEnvelopeHash": h1,
			"reason":               "evidencia insuficiente",
		})
	if status != http.StatusOK {
		t.Fatalf("reject status = %d, want 200; body %s", status, raw)
	}
	var result core.RejectMemoryResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.MemoryID != mem.Identity.ID {
		t.Errorf("memoryId = %q, want %q", result.MemoryID, mem.Identity.ID)
	}
	if result.PreviousStatus != string(core.StatusPendingReview) || result.CurrentStatus != string(core.StatusRejected) {
		t.Errorf("statuses = %s -> %s, want pending_review -> rejected", result.PreviousStatus, result.CurrentStatus)
	}
	if result.ReviewedEnvelopeHash != h1 {
		t.Errorf("reviewedEnvelopeHash = %s, want %s (H1)", result.ReviewedEnvelopeHash, h1)
	}
	if result.Reason != "evidencia insuficiente" {
		t.Errorf("reason = %q, want %q", result.Reason, "evidencia insuficiente")
	}

	// Persisted as rejected, decision event + receipt emitted.
	stored, err := api.Get(mem.Identity.ID)
	if err != nil {
		t.Fatalf("stored memory not found after reject: %v", err)
	}
	if stored.Status != core.StatusRejected {
		t.Fatalf("stored status = %q, want rejected", stored.Status)
	}
}

// TestHTTPReviewRejectFailClosedWithoutSession: no Authorization → 401
// AUTHENTICATION_REQUIRED; an unknown credential → 401 PRINCIPAL_INVALID. The
// shared token guard never authorizes a decision.
func TestHTTPReviewRejectFailClosedWithoutSession(t *testing.T) {
	ts, api, _ := reviewServer(t)
	mem := reviewSave(t, api, "review.reject.noauth", "sin sesion")

	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/memories/"+mem.Identity.ID+"/reject",
		"", "review-reject-noauth-1", map[string]string{
			"expectedEnvelopeHash": core.ComputeEnvelopeHash(mem), "reason": "x",
		})
	if status != http.StatusUnauthorized || !strings.Contains(raw, "AUTHENTICATION_REQUIRED") {
		t.Fatalf("no session: status = %d body %s, want 401 AUTHENTICATION_REQUIRED", status, raw)
	}

	status, raw = approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/memories/"+mem.Identity.ID+"/reject",
		"definitely-not-a-real-token", "review-reject-bad-1", map[string]string{
			"expectedEnvelopeHash": core.ComputeEnvelopeHash(mem), "reason": "x",
		})
	if status != http.StatusUnauthorized || !strings.Contains(raw, "PRINCIPAL_INVALID") {
		t.Fatalf("unknown token: status = %d body %s, want 401 PRINCIPAL_INVALID", status, raw)
	}
}

// TestHTTPReviewRejectSODViolation: the reviewer CANNOT decide their own
// proposal — a pending memory recorded by the same authenticated subject fails
// closed with 403 SOD_VIOLATION inside the transaction.
func TestHTTPReviewRejectSODViolation(t *testing.T) {
	ts, api, token := reviewServer(t)
	// A pending memory recorded by the reviewer's OWN subject id (human proposer).
	res, err := api.Save(core.SaveInput{
		TopicKey:     "review.sod.http",
		Title:        "Propuesta propia",
		Kind:         core.KindDecision,
		Scope:        demoScope(),
		Content:      core.Content{What: "propuesta del propio revisor", Why: "fixture", Where: "internal/server", Learned: "n/a"},
		FiscalEffect: core.FiscalEffectAdjustment,
		EffectiveAt:  "2026-07-31T12:00:00Z",
		Source:       core.Source{System: "go-test", ActorID: "maria.torres", ActorKind: core.ActorKindHuman},
	})
	if err != nil {
		t.Fatalf("save SoD fixture: %v", err)
	}
	mem := res.Memory
	if mem.Status != core.StatusPendingReview {
		t.Fatalf("SoD fixture status = %q, want pending_review", mem.Status)
	}

	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/memories/"+mem.Identity.ID+"/reject",
		token, "review-sod-1", map[string]string{
			"expectedEnvelopeHash": core.ComputeEnvelopeHash(mem), "reason": "auto-rechazo",
		})
	if status != http.StatusForbidden || !strings.Contains(raw, "SOD_VIOLATION") {
		t.Fatalf("SoD reject: status = %d body %s, want 403 SOD_VIOLATION", status, raw)
	}
	// The memory is untouched (fail-closed inside the transaction).
	stored, err := api.Get(mem.Identity.ID)
	if err != nil {
		t.Fatalf("stored memory not found: %v", err)
	}
	if stored.Status != core.StatusPendingReview {
		t.Fatalf("SoD failure mutated status to %q, want pending_review", stored.Status)
	}
}

// TestHTTPReviewRejectEnvelopeMismatch: a stale reviewed hash (the proposal
// changed after review) fails closed with 409 ENVELOPE_MISMATCH carrying ONLY
// the two hashes — never memory content.
func TestHTTPReviewRejectEnvelopeMismatch(t *testing.T) {
	ts, api, token := reviewServer(t)
	mem := reviewSave(t, api, "review.envelope.http", "cambia despues de la revision")

	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/memories/"+mem.Identity.ID+"/reject",
		token, "review-envelope-1", map[string]string{
			"expectedEnvelopeHash": strings.Repeat("0", 64), "reason": "stale",
		})
	if status != http.StatusConflict || !strings.Contains(raw, "ENVELOPE_MISMATCH") {
		t.Fatalf("stale envelope: status = %d body %s, want 409 ENVELOPE_MISMATCH", status, raw)
	}
	stored, err := api.Get(mem.Identity.ID)
	if err != nil {
		t.Fatalf("stored memory not found: %v", err)
	}
	if stored.Status != core.StatusPendingReview {
		t.Fatalf("envelope mismatch mutated status to %q", stored.Status)
	}
}

// TestHTTPReviewRejectIdempotentReplay: replaying the SAME (tenant, requestId)
// returns the stored result with idempotentReplay=true and no new decision.
func TestHTTPReviewRejectIdempotentReplay(t *testing.T) {
	ts, api, token := reviewServer(t)
	mem := reviewSave(t, api, "review.idem.http", "rechazo idempotente")
	h1 := core.ComputeEnvelopeHash(mem)
	body := map[string]string{"expectedEnvelopeHash": h1, "reason": "rechazo idempotente"}

	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/memories/"+mem.Identity.ID+"/reject",
		token, "review-idem-1", body)
	if status != http.StatusOK {
		t.Fatalf("first reject: status = %d body %s", status, raw)
	}
	var first core.RejectMemoryResult
	_ = json.Unmarshal([]byte(raw), &first)
	if first.IdempotentReplay {
		t.Fatalf("first decision must not be a replay")
	}

	status, raw = approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/memories/"+mem.Identity.ID+"/reject",
		token, "review-idem-1", body)
	if status != http.StatusOK {
		t.Fatalf("replay: status = %d body %s", status, raw)
	}
	var replay core.RejectMemoryResult
	if err := json.Unmarshal([]byte(raw), &replay); err != nil {
		t.Fatalf("decode replay: %v", err)
	}
	if !replay.IdempotentReplay || replay.MemoryID != mem.Identity.ID {
		t.Fatalf("replay result = %+v, want idempotentReplay=true for %s", replay, mem.Identity.ID)
	}
	if replay.DecisionEventID != first.DecisionEventID {
		t.Fatalf("replay must return the STORED event, got %s want %s", replay.DecisionEventID, first.DecisionEventID)
	}
}

// TestHTTPReviewReturnHappyPathAndSaveReentersPendingReview: the authenticated
// return moves pending_review → returned (NON-terminal) with the reason
// persisted; an agent Save on the returned memory creates a NEW revision that
// re-enters pending_review (design §2/§5).
func TestHTTPReviewReturnHappyPathAndSaveReentersPendingReview(t *testing.T) {
	ts, api, token := reviewServer(t)
	mem := reviewSave(t, api, "review.return.http", "devolver por correccion")
	h1 := core.ComputeEnvelopeHash(mem)

	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/memories/"+mem.Identity.ID+"/return",
		token, "review-return-1", map[string]string{
			"expectedEnvelopeHash": h1,
			"reason":               "falta adjuntar el CDR del comprobante",
		})
	if status != http.StatusOK {
		t.Fatalf("return status = %d, want 200; body %s", status, raw)
	}
	var result core.ReturnMemoryResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode return result: %v", err)
	}
	if result.PreviousStatus != string(core.StatusPendingReview) || result.CurrentStatus != string(core.StatusReturned) {
		t.Fatalf("statuses = %s -> %s, want pending_review -> returned", result.PreviousStatus, result.CurrentStatus)
	}
	if result.Reason != "falta adjuntar el CDR del comprobante" {
		t.Fatalf("reason = %q", result.Reason)
	}
	if result.ReviewedEnvelopeHash != h1 {
		t.Fatalf("reviewedEnvelopeHash = %s, want %s", result.ReviewedEnvelopeHash, h1)
	}

	stored, err := api.Get(mem.Identity.ID)
	if err != nil {
		t.Fatalf("stored memory not found: %v", err)
	}
	if stored.Status != core.StatusReturned {
		t.Fatalf("stored status = %q, want returned (non-terminal)", stored.Status)
	}

	// An agent Save on the returned memory (same topicKey + exact scope) creates
	// a NEW revision that re-enters pending_review.
	saved, err := api.Save(core.SaveInput{
		TopicKey:     "review.return.http",
		Title:        "Devolver por correccion",
		Kind:         core.KindDecision,
		Scope:        demoScope(),
		Content:      core.Content{What: "corregido: CDR adjuntado", Why: "correccion solicitada", Where: "internal/server", Learned: "n/a"},
		FiscalEffect: core.FiscalEffectAdjustment,
		EffectiveAt:  "2026-07-31T12:00:00Z",
		Source:       testAgentSource,
	})
	if err != nil {
		t.Fatalf("corrective save: %v", err)
	}
	if saved.Memory.Status != core.StatusPendingReview {
		t.Fatalf("corrective revision status = %q, want pending_review", saved.Memory.Status)
	}
	if saved.Memory.Revision <= stored.Revision {
		t.Fatalf("corrective revision %d must be NEWER than the returned revision %d", saved.Memory.Revision, stored.Revision)
	}
}

// TestHTTPReviewReturnRequiresReason: the return reason is REQUIRED (a
// correction request) — an empty reason fails closed with 400 REASON_REQUIRED.
func TestHTTPReviewReturnRequiresReason(t *testing.T) {
	ts, api, token := reviewServer(t)
	mem := reviewSave(t, api, "review.return.noreason", "devolver sin razon")

	status, raw := approvalHTTP(t, http.MethodPost,
		ts.URL+"/accounting/memories/"+mem.Identity.ID+"/return",
		token, "review-return-noreason-1", map[string]string{
			"expectedEnvelopeHash": core.ComputeEnvelopeHash(mem), "reason": "",
		})
	if status != http.StatusBadRequest || !strings.Contains(raw, "REASON_REQUIRED") {
		t.Fatalf("empty reason: status = %d body %s, want 400 REASON_REQUIRED", status, raw)
	}
}
