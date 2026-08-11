// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module freezes the v0.9.0 REVIEW
// WORKSPACE CLI surface (docs/architecture/review-workspace-v0.9.md §7): the
// scope-first queue/detail reads and the AUTHENTICATED reject/return decisions
// (principal from the stored CLI session — auth login — never from a caller
// flag, the same ADR-003 contract as approve).
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// writeReviewFixture writes a pending_review save payload (fiscalEffect != none
// → the approval gate) recorded by an AGENT (the proposer the reviewer must
// differ from) under a distinct topicKey and returns the file path.
func writeReviewFixture(t *testing.T, topicKey string) string {
	t.Helper()
	payload := `{"topicKey":"` + topicKey + `","title":"Pendiente de revision","kind":"decision","scope":{"kind":"company","organizationId":"cli","companyId":"20100039201","ruc":"20100039201","period":"202401"},"content":{"what":"pendiente en el alcance","why":"fixture","where":"cli","learned":"n/a"},"fiscalEffect":"adjustment","effectiveAt":"2024-01-31T00:00:00.000Z","source":{"system":"cli","actorId":"cli-user","actorKind":"agent"}}`
	path := filepath.Join(t.TempDir(), topicKey+".json")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("write review fixture: %v", err)
	}
	return path
}

// savePendingCLIReview saves a pending_review memory through the built CLI under
// the CLI company scope and returns its id.
func savePendingCLIReview(t *testing.T, db, topicKey string) string {
	t.Helper()
	return saveViaCLI(t, db, writeReviewFixture(t, topicKey))
}

// TestCLIReviewQueueScopeFirst: review queue lists the pending_review items of
// the EXACT company scope passed on the command line — another company's queue
// is EMPTY, never the other company's pending reviews.
func TestCLIReviewQueueScopeFirst(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	idA := savePendingCLIReview(t, db, "review.queue.a")

	stdout, stderr, code := runCLI(t, "review", "queue", cliRucA, "--period", "202401", "--db", db)
	if code != 0 {
		t.Fatalf("review queue failed (exit %d): %s", code, stderr)
	}
	var page core.ReviewQueuePage
	if err := json.Unmarshal([]byte(stdout), &page); err != nil {
		t.Fatalf("queue output not JSON: %v\n%s", err, stdout)
	}
	if len(page.Items) != 1 || page.Items[0].MemoryID != idA {
		t.Fatalf("queue items = %+v, want exactly %s", page.Items, idA)
	}
	if page.Items[0].EnvelopeHash == "" || page.Items[0].RecordedBy != "cli-user" {
		t.Fatalf("queue item must carry the CURRENT envelope and the proposer: %+v", page.Items[0])
	}

	// Cross-company isolation: company B's queue is EMPTY.
	stdout, stderr, code = runCLI(t, "review", "queue", cliRucB, "--period", "202401", "--db", db)
	if code != 0 {
		t.Fatalf("review queue B failed (exit %d): %s", code, stderr)
	}
	var pageB core.ReviewQueuePage
	if err := json.Unmarshal([]byte(stdout), &pageB); err != nil {
		t.Fatalf("queue B output not JSON: %v\n%s", err, stdout)
	}
	if len(pageB.Items) != 0 {
		t.Fatalf("cross-company queue must be empty, got %d items", len(pageB.Items))
	}
}

// TestCLIReviewDetail: review detail composes the pending revision with the H1
// to sign, the proposer and the boundary notice; a cross-company scope fails
// closed with MEMORY_NOT_FOUND (invisible, never a leak).
func TestCLIReviewDetail(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	id := savePendingCLIReview(t, db, "review.detail.a")

	stdout, stderr, code := runCLI(t, "review", "detail", id, "--ruc", cliRucA, "--period", "202401", "--db", db)
	if code != 0 {
		t.Fatalf("review detail failed (exit %d): %s", code, stderr)
	}
	var detail core.ReviewDetail
	if err := json.Unmarshal([]byte(stdout), &detail); err != nil {
		t.Fatalf("detail output not JSON: %v\n%s", err, stdout)
	}
	if detail.Memory.Identity.ID != id {
		t.Fatalf("detail memory = %q, want %q", detail.Memory.Identity.ID, id)
	}
	if detail.ReviewMetadata.EnvelopeHashToSign == "" || detail.ReviewMetadata.RecordedBy != "cli-user" {
		t.Fatalf("detail metadata must carry H1 and the proposer: %+v", detail.ReviewMetadata)
	}
	if detail.BoundaryNotice != core.ReviewBoundaryNotice {
		t.Fatalf("boundary notice = %q, want %q", detail.BoundaryNotice, core.ReviewBoundaryNotice)
	}

	// Cross-company detail is MEMORY_NOT_FOUND.
	_, stderr, code = runCLI(t, "review", "detail", id, "--ruc", cliRucB, "--period", "202401", "--db", db)
	if code != 1 || !strings.Contains(stderr, "MEMORY_NOT_FOUND") {
		t.Fatalf("cross-company detail: exit %d stderr %q, want 1 MEMORY_NOT_FOUND", code, stderr)
	}
}

// TestCLIReviewRejectWithoutSession: review reject with no authenticated CLI
// session fails closed with AUTHENTICATION_REQUIRED and points at auth login.
func TestCLIReviewRejectWithoutSession(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	id := savePendingCLIReview(t, db, "review.reject.noauth")
	h1 := memoryEnvelope(t, db, id)

	stdout, stderr, code := runCLIEnv(t, sessionFileEnv(t.TempDir()),
		"review", "reject", id, "--expected-envelope", h1, "--reason", "fuera de alcance", "--db", db)
	if code != 1 {
		t.Fatalf("review reject without session exit = %d, want 1; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("review reject without session must not write stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "AUTHENTICATION_REQUIRED") || !strings.Contains(stderr, "auth login") {
		t.Fatalf("stderr must carry AUTHENTICATION_REQUIRED and point at auth login: %q", stderr)
	}
}

// TestCLIReviewRejectWithSeededSession is the full authenticated round trip
// through the real binary: auth login writes the session file, then review
// reject resolves the principal from it and prints the core.RejectMemoryResult
// JSON (pending_review → rejected, terminal).
func TestCLIReviewRejectWithSeededSession(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	token := seedCLIIdentity(t, db)
	env := sessionFileEnv(t.TempDir())

	if _, stderr, code := runCLIEnv(t, env, "auth", "login", "--token", token, "--db", db); code != 0 {
		t.Fatalf("auth login failed (exit %d): %s", code, stderr)
	}
	id := savePendingCLIReview(t, db, "review.reject.ok")
	h1 := memoryEnvelope(t, db, id)

	stdout, stderr, code := runCLIEnv(t, env,
		"review", "reject", id, "--expected-envelope", h1, "--reason", "evidencia insuficiente", "--db", db)
	if code != 0 {
		t.Fatalf("review reject failed (exit %d): %s", code, stderr)
	}
	var result core.RejectMemoryResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("reject output not JSON: %v\n%s", err, stdout)
	}
	if result.MemoryID != id || result.CurrentStatus != "rejected" || result.PreviousStatus != "pending_review" {
		t.Fatalf("reject result = %+v, want %s pending_review → rejected", result, id)
	}
	if result.ReviewedEnvelopeHash != h1 {
		t.Fatalf("reviewedEnvelopeHash = %q, want %q", result.ReviewedEnvelopeHash, h1)
	}
	if result.Reason != "evidencia insuficiente" {
		t.Fatalf("reason = %q, want %q", result.Reason, "evidencia insuficiente")
	}
	if result.IdempotentReplay {
		t.Fatalf("fresh reject must not be an idempotent replay")
	}
}

// TestCLIReviewReturnHappyPath: the authenticated return moves pending_review →
// returned (NON-terminal); the returned revision is visible with status
// "returned" and a corrective save re-enters pending_review as a NEW revision.
func TestCLIReviewReturnHappyPath(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	token := seedCLIIdentity(t, db)
	env := sessionFileEnv(t.TempDir())

	if _, stderr, code := runCLIEnv(t, env, "auth", "login", "--token", token, "--db", db); code != 0 {
		t.Fatalf("auth login failed (exit %d): %s", code, stderr)
	}
	id := savePendingCLIReview(t, db, "review.return.ok")
	h1 := memoryEnvelope(t, db, id)

	stdout, stderr, code := runCLIEnv(t, env,
		"review", "return", id, "--expected-envelope", h1, "--reason", "falta adjuntar el CDR", "--db", db)
	if code != 0 {
		t.Fatalf("review return failed (exit %d): %s", code, stderr)
	}
	var result core.ReturnMemoryResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("return output not JSON: %v\n%s", err, stdout)
	}
	if result.CurrentStatus != "returned" || result.PreviousStatus != "pending_review" {
		t.Fatalf("return result = %+v, want pending_review → returned", result)
	}
	if result.Reason != "falta adjuntar el CDR" {
		t.Fatalf("reason = %q", result.Reason)
	}

	// The returned revision stays visible with status "returned" (non-terminal).
	stdout, stderr, code = runCLI(t, "review", "detail", id, "--ruc", cliRucA, "--period", "202401", "--db", db)
	if code != 1 || !strings.Contains(stderr, "pending_review") {
		t.Fatalf("detail of a returned memory: exit %d stderr %q, want 1 (review detail requires pending_review)", code, stderr)
	}

	// A corrective save on the same topicKey creates a NEW revision that
	// re-enters pending_review — visible again in the queue.
	corrected := writeReviewFixture(t, "review.return.ok")
	// Same topicKey → the save SUPERSEDES the returned revision and re-enters
	// pending_review (the design's correction loop).
	_ = saveViaCLI(t, db, corrected)
	stdout, stderr, code = runCLI(t, "review", "queue", cliRucA, "--period", "202401", "--db", db)
	if code != 0 {
		t.Fatalf("review queue after correction failed (exit %d): %s", code, stderr)
	}
	var page core.ReviewQueuePage
	if err := json.Unmarshal([]byte(stdout), &page); err != nil {
		t.Fatalf("queue output not JSON: %v\n%s", err, stdout)
	}
	found := false
	for _, item := range page.Items {
		if item.MemoryID != id && item.Status == "pending_review" {
			found = true
		}
	}
	if len(page.Items) == 0 || !found {
		t.Fatalf("corrective save must re-enter pending_review: items = %+v", page.Items)
	}
}

// TestCLIReviewRejectReplay (AC-L-3, FR-L.4): review reject with the SAME
// --request-id submitted twice against the same temp DB — the second run prints
// the FIRST stored decision event id with idempotentReplay=true, the memory
// stays rejected, and the doctor digest (pending approvals unchanged at 0)
// proves no second decision event/receipt. The fresh-only assertion above is
// NOT sufficient alone.
func TestCLIReviewRejectReplay(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	token := seedCLIIdentity(t, db)
	env := sessionFileEnv(t.TempDir())
	if _, stderr, code := runCLIEnv(t, env, "auth", "login", "--token", token, "--db", db); code != 0 {
		t.Fatalf("auth login failed (exit %d): %s", code, stderr)
	}
	id := savePendingCLIReview(t, db, "review.reject.replay")
	h1 := memoryEnvelope(t, db, id)
	const key = "req-cli-review-reject-replay-1"

	runReject := func() core.RejectMemoryResult {
		t.Helper()
		stdout, stderr, code := runCLIEnv(t, env,
			"review", "reject", id, "--expected-envelope", h1, "--reason", "evidencia insuficiente",
			"--request-id", key, "--db", db)
		if code != 0 {
			t.Fatalf("review reject failed (exit %d): %s", code, stderr)
		}
		var result core.RejectMemoryResult
		if err := json.Unmarshal([]byte(stdout), &result); err != nil {
			t.Fatalf("review reject output not JSON: %v\n%s", err, stdout)
		}
		return result
	}

	first := runReject()
	if first.IdempotentReplay || first.DecisionEventID == "" {
		t.Fatalf("first reject = %+v, want a fresh stored decision event", first)
	}
	if first.CurrentStatus != "rejected" {
		t.Fatalf("first reject status = %q, want rejected", first.CurrentStatus)
	}
	afterFirst := cliDoctorDigest(t, db)

	second := runReject()
	if !second.IdempotentReplay || second.DecisionEventID != first.DecisionEventID {
		t.Fatalf("replay reject = %+v, want the stored decision event %s with idempotentReplay", second, first.DecisionEventID)
	}
	if second.CurrentStatus != "rejected" || second.PreviousStatus != "pending_review" {
		t.Fatalf("replay reject = %+v, want the stored pending_review → rejected outcome", second)
	}
	afterSecond := cliDoctorDigest(t, db)
	if afterFirst != afterSecond {
		t.Fatalf("review reject replay duplicated effects: before %s after %s", afterFirst, afterSecond)
	}
}

// TestCLIReviewRejectSODViolation: the reviewer cannot decide their own
// proposal — a pending memory recorded by the SAME authenticated subject fails
// closed with SOD_VIOLATION and the memory stays pending_review.
func TestCLIReviewRejectSODViolation(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	token := seedCLIIdentity(t, db)
	env := sessionFileEnv(t.TempDir())
	if _, stderr, code := runCLIEnv(t, env, "auth", "login", "--token", token, "--db", db); code != 0 {
		t.Fatalf("auth login failed (exit %d): %s", code, stderr)
	}

	// A pending memory recorded by the reviewer's OWN subject id (human proposer
	// "maria.torres" — the seeded identity's subject).
	payload := `{"topicKey":"review.sod.cli","title":"Propuesta propia","kind":"decision","scope":{"kind":"company","organizationId":"cli","companyId":"20100039201","ruc":"20100039201","period":"202401"},"content":{"what":"propuesta del propio revisor","why":"fixture","where":"cli","learned":"n/a"},"fiscalEffect":"adjustment","effectiveAt":"2024-01-31T00:00:00.000Z","source":{"system":"cli","actorId":"maria.torres","actorKind":"human"}}`
	path := filepath.Join(t.TempDir(), "review-sod.json")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("write SoD fixture: %v", err)
	}
	id := saveViaCLI(t, db, path)
	h1 := memoryEnvelope(t, db, id)

	_, stderr, code := runCLIEnv(t, env,
		"review", "reject", id, "--expected-envelope", h1, "--reason", "auto-rechazo", "--db", db)
	if code != 1 || !strings.Contains(stderr, "SOD_VIOLATION") {
		t.Fatalf("SoD reject: exit %d stderr %q, want 1 SOD_VIOLATION", code, stderr)
	}

	// The memory is untouched (fail-closed inside the transaction): still pending.
	stdout, _, code := runCLI(t, "review", "queue", cliRucA, "--period", "202401", "--db", db)
	if code != 0 {
		t.Fatalf("review queue failed (exit %d)", code)
	}
	var page core.ReviewQueuePage
	if err := json.Unmarshal([]byte(stdout), &page); err != nil {
		t.Fatalf("queue output not JSON: %v", err)
	}
	for _, item := range page.Items {
		if item.MemoryID == id {
			if item.Status != "pending_review" {
				t.Fatalf("SoD failure mutated status to %q, want pending_review", item.Status)
			}
			return
		}
	}
	t.Fatalf("the SoD fixture must still be pending in the queue: %+v", page.Items)
}
