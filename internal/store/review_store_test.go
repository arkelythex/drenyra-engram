// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module freezes the v0.9.0 REVIEW
// WORKSPACE store surface (docs/architecture/review-workspace-v0.9.md): the
// pending_review queue (deterministic order, scope isolation, bounded
// pagination), the detail assembly (diff, WORM availability, best-effort rules,
// open judgments, boundary notice), and the AUTHENTICATED reject/return
// decisions (SoD, reason policy, hash guard, idempotency, immutable events and
// signed receipts) plus the anti-rubber-stamp velocity alerts.
package store

import (
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

// reviewSaveInput builds a pending_review save (fiscalEffect != none) recorded
// by the given actor id (agent by default — the proposer the reviewer must
// differ from).
func reviewSaveInput(topicKey, what string, recordedBy string) core.SaveInput {
	in := validInput(topicKey, what)
	in.FiscalEffect = core.FiscalEffectAdjustment
	in.Source = core.Source{System: "go-test", ActorID: recordedBy, ActorKind: core.ActorKindAgent}
	return in
}

// humanRecordedInput is a pending_review save recorded by a HUMAN whose id
// matches the fixture reviewer — the SoD negative fixture.
func humanRecordedInput(topicKey, what string) core.SaveInput {
	in := validInput(topicKey, what)
	in.FiscalEffect = core.FiscalEffectAdjustment
	in.Source = core.Source{System: "go-test", ActorID: "subject-1", ActorKind: core.ActorKindHuman}
	return in
}

func mustSave(t *testing.T, s *SQLiteStore, in core.SaveInput) core.WriteResult {
	t.Helper()
	res, err := s.Save(in)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	return res
}

// ──────────────────────────────────────────────
// Schema v13 — review workspace tables
// ──────────────────────────────────────────────

func TestFreshStoreBootstrapsV13ReviewWorkspace(t *testing.T) {
	s := newTestStore(t)

	version, err := readSchemaVersion(s.db)
	if err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != 17 {
		t.Fatalf("schema_version = %d, want 17 (the chain continues v2→…→v13→v14)", version)
	}

	// The three review-workspace tables plus their guards exist on a fresh store.
	for _, table := range []string{"memory_decision_events", "review_idempotency_keys", "review_velocity_events"} {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("fresh store missing table %q: %v", table, err)
		}
	}
	for _, trg := range []string{
		"memory_decision_events_no_update", "memory_decision_events_no_delete",
		"review_velocity_events_no_update", "review_velocity_events_no_delete",
	} {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name = ?`, trg).Scan(&name); err != nil {
			t.Fatalf("fresh store missing trigger %q: %v", trg, err)
		}
	}

	// The receipts action CHECK is the v13 layout: memory_returned inserts and an
	// unknown action is still rejected by the closed CHECK.
	if err := s.RegisterPublicKey(ctxForTest(), s.db, "ed25519:key-v13", core.ReceiptAlgorithm, "base64-public-v13", testT); err != nil {
		t.Fatalf("register key: %v", err)
	}
	res := mustSave(t, s, reviewSaveInput("review.v13.schema", "pending", "test-agent"))
	returnRow := ReceiptRow{
		SubjectType:         core.SubjectTypeMemory,
		SubjectID:           res.Memory.Identity.ID,
		Action:              core.ReceiptActionMemoryReturned,
		TenantID:            testOrgID,
		CompanyID:           "acme",
		FiscalPeriodID:      testPeriod,
		PayloadHash:         "payload-hash-returned-v13",
		PreviousReceiptHash: "",
		PrincipalID:         "subject-1",
		MembershipID:        "membership-1",
		PolicyVersion:       "kernel/v0.4.0",
		Algorithm:           core.ReceiptAlgorithm,
		KeyID:               "ed25519:key-v13",
		Signature:           []byte{0x0d, 0x0d, 0x0d, 0x0d},
		IssuedAt:            testT,
		PayloadJSON:         `{"version":"receipt-payload/v0.10.0"}`,
		ReceiptHash:         "receipt-hash-returned-v13",
		MemoryID:            res.Memory.Identity.ID,
	}
	if err := s.InsertReceipt(ctxForTest(), s.db, returnRow); err != nil {
		t.Fatalf("fresh store must accept a memory_returned receipt: %v", err)
	}
	bad := returnRow
	bad.Action = core.ReceiptAction("memory_filed")
	bad.ReceiptHash = "receipt-hash-unknown-v13"
	if err := s.InsertReceipt(ctxForTest(), s.db, bad); err == nil {
		t.Fatal("unknown receipt action must be rejected by the closed CHECK")
	}
}

// ──────────────────────────────────────────────
// Queue (design §3)
// ──────────────────────────────────────────────

func TestListReviewQueueDeterministicOrderAndCounts(t *testing.T) {
	s := newTestStore(t)
	ctx := ctxForTest()

	// Three pending items with different risk classes and recorded timestamps.
	matLvl := core.MaterialityMaterial
	critLvl := core.MaterialityCritical
	low := mustSave(t, s, reviewSaveInput("review.queue.low", "normal", "agent-a"))
	material := mustSave(t, s, func() core.SaveInput {
		in := reviewSaveInput("review.queue.material", "material", "agent-b")
		in.MaterialityLevel = &matLvl
		return in
	}())
	critical := mustSave(t, s, func() core.SaveInput {
		in := reviewSaveInput("review.queue.critical", "critical", "agent-c")
		in.MaterialityLevel = &critLvl
		return in
	}())

	// A proposed judgment touching the material memory raises its open count.
	if _, err := s.db.Exec(`
		INSERT INTO judgments (id, tenant_id, company_id, fiscal_period_id, from_id, to_id, relation, status,
			proposer_system, proposer_actor_id, proposer_actor_kind, proposer_session, proposal_reason,
			resolution, policy_version, predecessor_id, supersedes_id, proposed_at, updated_at, decided_at)
		VALUES ('judgment-open-1', ?, 'acme', ?, ?, ?, 'supports', 'proposed',
			'go-test', 'agent-a', 'agent', '', 'open question', NULL, NULL, NULL, NULL, ?, ?, NULL)`,
		testOrgID, testPeriod, material.Memory.Identity.ID, low.Memory.Identity.ID, testT, testT,
	); err != nil {
		t.Fatalf("seed proposed judgment: %v", err)
	}

	page, err := s.ListReviewQueue(ctx, core.ReviewQueueQuery{Scope: testScope(testRucA)})
	if err != nil {
		t.Fatalf("list queue: %v", err)
	}
	if page.Limit != core.DefaultReviewQueueLimit || page.Offset != 0 {
		t.Errorf("page = limit %d offset %d, want 50/0", page.Limit, page.Offset)
	}
	if len(page.Items) != 3 {
		t.Fatalf("queue items = %d, want 3", len(page.Items))
	}
	// Order: critical first, then material, then normal (rank DESC → recordedAt ASC).
	if page.Items[0].MemoryID != critical.Memory.Identity.ID || page.Items[1].MemoryID != material.Memory.Identity.ID || page.Items[2].MemoryID != low.Memory.Identity.ID {
		t.Fatalf("queue order = %s, %s, %s — want critical, material, normal",
			page.Items[0].MemoryID, page.Items[1].MemoryID, page.Items[2].MemoryID)
	}
	// Risk class + proposer + counts.
	if page.Items[0].MaterialityLevel == nil || *page.Items[0].MaterialityLevel != core.MaterialityCritical {
		t.Errorf("critical item level = %v, want critical", page.Items[0].MaterialityLevel)
	}
	if page.Items[0].RecordedBy != "agent-c" {
		t.Errorf("critical item recordedBy = %q, want agent-c", page.Items[0].RecordedBy)
	}
	if page.Items[1].OpenJudgmentCount != 1 {
		t.Errorf("material item openJudgmentCount = %d, want 1", page.Items[1].OpenJudgmentCount)
	}
	if page.Items[0].Status != core.StatusPendingReview {
		t.Errorf("critical item status = %s, want pending_review", page.Items[0].Status)
	}
	if want := currentEnvelope(critical); page.Items[0].EnvelopeHash != want {
		t.Errorf("critical item envelopeHash = %q, want the fresh pending envelope %q", page.Items[0].EnvelopeHash, want)
	}

	// Pagination: limit/offset slices deterministically.
	one, err := s.ListReviewQueue(ctx, core.ReviewQueueQuery{Scope: testScope(testRucA), Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("list queue page: %v", err)
	}
	if len(one.Items) != 1 || one.Items[0].MemoryID != material.Memory.Identity.ID {
		t.Fatalf("page(1,1) = %d items, first %q — want 1 item, the material memory", len(one.Items), one.Items[0].MemoryID)
	}

	// Pagination bounds fail closed.
	if _, err := s.ListReviewQueue(ctx, core.ReviewQueueQuery{Scope: testScope(testRucA), Limit: 201}); err == nil {
		t.Fatal("limit 201 must fail closed")
	}
	if _, err := s.ListReviewQueue(ctx, core.ReviewQueueQuery{Scope: testScope(testRucA), Offset: -1}); err == nil {
		t.Fatal("negative offset must fail closed")
	}
}

func TestReviewQueueScopeIsolation(t *testing.T) {
	s := newTestStore(t)
	mustSave(t, s, reviewSaveInput("review.isolation.one", "pending", "agent-a"))

	// A different RUC sees nothing (structural isolation, never a post-filter).
	other := testScope(testRucA)
	other.RUC = "20481234567"
	page, err := s.ListReviewQueue(ctxForTest(), core.ReviewQueueQuery{Scope: other})
	if err != nil {
		t.Fatalf("list other scope: %v", err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("queue items for another RUC = %d, want 0", len(page.Items))
	}

	// An institutional scope fails closed (institutional memories have no company
	// to review).
	if _, err := s.ListReviewQueue(ctxForTest(), core.ReviewQueueQuery{Scope: core.Scope{Kind: core.ScopeKindInstitutional}}); auth.Code(err) != auth.CodeInvalidTransition {
		t.Fatalf("institutional queue = %v, want INVALID_TRANSITION", err)
	}

	// An approved memory leaves the queue (status is closed to pending_review).
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	res := mustSave(t, s, reviewSaveInput("review.isolation.decided", "pending", "agent-a"))
	if _, err := approve(s, res.Memory.Identity.ID, currentEnvelope(res), "req-iso-1", controllerPrincipal(t)); err != nil {
		t.Fatalf("approve: %v", err)
	}
	page, err = s.ListReviewQueue(ctxForTest(), core.ReviewQueueQuery{Scope: testScope(testRucA)})
	if err != nil {
		t.Fatalf("list queue after approval: %v", err)
	}
	for _, item := range page.Items {
		if item.MemoryID == res.Memory.Identity.ID {
			t.Fatalf("decided memory still in the queue")
		}
	}
}

// ──────────────────────────────────────────────
// Detail (design §4)
// ──────────────────────────────────────────────

func TestReviewDetailComposes(t *testing.T) {
	s := newTestStore(t)
	ctx := ctxForTest()

	// Chain: revision 1 (approved earlier) → revision 2 (pending head).
	v1 := mustSave(t, s, func() core.SaveInput {
		in := reviewSaveInput("review.detail.chain", "original what", "agent-a")
		return in
	}())
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	if _, err := approve(s, v1.Memory.Identity.ID, currentEnvelope(v1), "req-detail-approve-1", controllerPrincipal(t)); err != nil {
		t.Fatalf("approve v1: %v", err)
	}
	v2 := mustSave(t, s, reviewSaveInput("review.detail.chain", "corrected what", "agent-b"))

	// One object-backed evidence ref (stored through the REAL WORM path) + one
	// legacy ref.
	storedObj, err := s.StoreObject(ctx, core.ObjectStoreInput{
		Bytes:       []byte("factura xml bytes"),
		ContentType: "application/xml",
		Scope:       testScope(testRucA),
		Source:      core.Source{System: "go-test", ActorID: "agent-b", ActorKind: core.ActorKindAgent},
	})
	if err != nil {
		t.Fatalf("store evidence object: %v", err)
	}
	objectID := storedObj.Object.ObjectID
	if err := s.AddEvidenceLink(v2.Memory.Identity.ID, objectID, "test"); err != nil {
		t.Fatalf("link object ref: %v", err)
	}
	if err := s.AddEvidenceLink(v2.Memory.Identity.ID, "F001-948", "test"); err != nil {
		t.Fatalf("link legacy ref: %v", err)
	}
	// One rule ref that resolves (a rule memory in the same scope) + one that does not.
	rule := mustSave(t, s, func() core.SaveInput {
		in := reviewSaveInput("policy/igv/late-document-v3", "rule content", "agent-a")
		in.Kind = core.KindRule
		in.FiscalEffect = core.FiscalEffectNone
		in.Validity = &core.Validity{EffectiveAt: "2026-01-01", ExpiresAt: "2026-12-31", Source: "declared"}
		return in
	}())
	_ = rule
	// A proposed judgment touching the pending head.
	if _, err := s.db.Exec(`
		INSERT INTO judgments (id, tenant_id, company_id, fiscal_period_id, from_id, to_id, relation, status,
			proposer_system, proposer_actor_id, proposer_actor_kind, proposer_session, proposal_reason,
			resolution, policy_version, predecessor_id, supersedes_id, proposed_at, updated_at, decided_at)
		VALUES ('judgment-open-2', ?, 'acme', ?, ?, ?, 'contradicts', 'proposed',
			'go-test', 'agent-a', 'agent', '', 'open', NULL, NULL, NULL, NULL, ?, ?, NULL)`,
		testOrgID, testPeriod, v1.Memory.Identity.ID, v2.Memory.Identity.ID, testT, testT,
	); err != nil {
		t.Fatalf("seed proposed judgment: %v", err)
	}

	detail, err := s.ReviewDetail(ctx, v2.Memory.Identity.ID, testScope(testRucA))
	if err != nil {
		t.Fatalf("review detail: %v", err)
	}
	if detail.BoundaryNotice != core.ReviewBoundaryNotice {
		t.Errorf("boundaryNotice = %q, want %q", detail.BoundaryNotice, core.ReviewBoundaryNotice)
	}
	// Diff vs revision 1: the what changed (content) — status/provenance never appear.
	foundWhat := false
	for _, c := range detail.Diff.Changes {
		if c.Field == "content.what" {
			foundWhat = true
			if c.Before != "original what" || c.After != "corrected what" {
				t.Errorf("content.what diff = %q → %q", c.Before, c.After)
			}
		}
		if c.Field == "status" || c.Field == "recordedAt" || c.Field == "recordedBy" {
			t.Errorf("provenance field %q leaked into the diff", c.Field)
		}
	}
	if !foundWhat {
		t.Errorf("diff missing content.what change: %+v", detail.Diff.Changes)
	}
	// Evidence availability: object-backed → present, legacy → not-a-ref,
	// object-shaped without a row → absent.
	avail := map[string]core.EvidenceAvailability{}
	for _, e := range detail.Evidence {
		avail[e.Ref] = e.Availability
	}
	if avail[objectID] != core.EvidencePresent {
		t.Errorf("object-backed ref availability = %q, want present", avail[objectID])
	}
	if avail["F001-948"] != core.EvidenceNotARef {
		t.Errorf("legacy ref availability = %q, want not-a-ref", avail["F001-948"])
	}
	missing := strings.Repeat("e", 64)
	if err := s.AddEvidenceLink(v2.Memory.Identity.ID, missing, "test"); err != nil {
		t.Fatalf("link missing ref: %v", err)
	}
	detail2, err := s.ReviewDetail(ctx, v2.Memory.Identity.ID, testScope(testRucA))
	if err != nil {
		t.Fatalf("review detail with missing ref: %v", err)
	}
	for _, e := range detail2.Evidence {
		if e.Ref == missing && e.Availability != core.EvidenceAbsent {
			t.Errorf("object-shaped missing ref availability = %q, want absent", e.Availability)
		}
	}
	// Rules: the resolvable rule carries its vigencia; the unresolvable one stays
	// resolved=false.
	for _, r := range detail2.Rules {
		switch r.Ref {
		case "policy/igv/late-document-v3":
			if !r.Resolved || r.EffectiveAt != "2026-01-01" || r.ExpiresAt != "2026-12-31" {
				t.Errorf("rule %s = %+v, want resolved with the declared vigencia", r.Ref, r)
			}
		default:
			if r.Resolved {
				t.Errorf("unresolvable rule %q claimed resolved", r.Ref)
			}
		}
	}
	// Open judgments: the proposed judgment touching the head is surfaced.
	if len(detail2.OpenJudgments) != 1 || detail2.OpenJudgments[0].JudgmentID != "judgment-open-2" {
		t.Fatalf("openJudgments = %+v, want the touching proposed judgment", detail2.OpenJudgments)
	}
	// Review metadata: H1 is the fresh pending envelope WITH the current links
	// (the exact bytes the reviewer must sign against); recordedBy is the proposer.
	head, ok := s.FindByID(v2.Memory.Identity.ID)
	if !ok {
		t.Fatal("pending head lost")
	}
	if detail2.ReviewMetadata.EnvelopeHashToSign != core.ComputeEnvelopeHash(head) {
		t.Errorf("envelopeHashToSign = %q, want the fresh pending envelope %q", detail2.ReviewMetadata.EnvelopeHashToSign, core.ComputeEnvelopeHash(head))
	}
	if detail2.ReviewMetadata.RecordedBy != "agent-b" {
		t.Errorf("recordedBy = %q, want agent-b", detail2.ReviewMetadata.RecordedBy)
	}
	if detail2.ReviewMetadata.PriorApprovedRevision != "" {
		t.Errorf("priorApprovedRevision = %q, want empty (the engine's Save always supersedes the previous current revision, so a normal chain has no prior approved revision)", detail2.ReviewMetadata.PriorApprovedRevision)
	}

	// A chain WITH a prior approved revision arises from imports/backfills where
	// the approved row was never superseded; the query must surface it. Seed one
	// directly (revision 1 approved + revision 2 pending, same topic/scope).
	imported := mustSave(t, s, reviewSaveInput("review.detail.prior-approved", "imported", "agent-a"))
	if _, err := s.db.Exec(`UPDATE observations SET status = 'approved', authority_status = 'promoted' WHERE id = ?`, imported.Memory.Identity.ID); err != nil {
		t.Fatalf("flip imported revision to approved: %v", err)
	}
	head3 := mustSave(t, s, reviewSaveInput("review.detail.prior-approved", "imported pending", "agent-a"))
	if prior, ok := s.FindByID(imported.Memory.Identity.ID); !ok || prior.Status != core.StatusSuperseded {
		t.Fatal("the save must supersede the previous current revision")
	}
	detail3, err := s.ReviewDetail(ctx, head3.Memory.Identity.ID, testScope(testRucA))
	if err != nil {
		t.Fatalf("review detail of imported chain: %v", err)
	}
	if detail3.ReviewMetadata.PriorApprovedRevision != "" {
		t.Errorf("imported priorApprovedRevision = %q, want empty (the pending save superseded the approved revision)", detail3.ReviewMetadata.PriorApprovedRevision)
	}
}

func TestReviewDetailGuards(t *testing.T) {
	s := newTestStore(t)
	res := mustSave(t, s, reviewSaveInput("review.detail.guards", "pending", "agent-a"))
	ctx := ctxForTest()

	// Wrong scope → invisible (MEMORY_NOT_FOUND, never a scope error).
	other := testScope(testRucA)
	other.RUC = "20481234567"
	if _, err := s.ReviewDetail(ctx, res.Memory.Identity.ID, other); auth.Code(err) != auth.CodeMemoryNotFound {
		t.Fatalf("detail with wrong scope = %v, want MEMORY_NOT_FOUND", err)
	}
	// A decided memory is not reviewable (detail is for pending items).
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	if _, err := approve(s, res.Memory.Identity.ID, currentEnvelope(res), "req-detail-guards-1", controllerPrincipal(t)); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := s.ReviewDetail(ctx, res.Memory.Identity.ID, testScope(testRucA)); auth.Code(err) != auth.CodeInvalidTransition {
		t.Fatalf("detail of an approved memory = %v, want INVALID_TRANSITION", err)
	}
}

// ──────────────────────────────────────────────
// Reject — authenticated decision (design §5)
// ──────────────────────────────────────────────

func TestRejectMemoryHappyPath(t *testing.T) {
	s, signer := receiptStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	res := mustSave(t, s, reviewSaveInput("review.reject.happy", "pending", "agent-a"))
	id := res.Memory.Identity.ID
	principal := controllerPrincipal(t)

	result, err := s.RejectMemory(ctxForTest(), core.RejectMemoryCommand{
		MemoryID:             id,
		ExpectedEnvelopeHash: currentEnvelope(res),
		Reason:               "amount differs from the comprobante",
		RequestID:            "req-reject-happy",
	}, principal)
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if result.MemoryID != id || result.PreviousStatus != "pending_review" || result.CurrentStatus != "rejected" {
		t.Fatalf("result statuses = %s → %s, want pending_review → rejected", result.PreviousStatus, result.CurrentStatus)
	}
	if result.PrincipalSubjectID != "subject-1" || result.ReviewedEnvelopeHash != currentEnvelope(res) {
		t.Errorf("result principal/hash = %q/%q", result.PrincipalSubjectID, result.ReviewedEnvelopeHash)
	}
	if result.Reason != "amount differs from the comprobante" || result.IdempotentReplay {
		t.Errorf("result reason/replay = %q/%v", result.Reason, result.IdempotentReplay)
	}

	// The memory is rejected and its envelope changed (status participates).
	mem, ok := s.FindByID(id)
	if !ok || mem.Status != core.StatusRejected {
		t.Fatalf("memory status = %v, want rejected", mem.Status)
	}
	// The immutable decision event + transition row exist.
	var action, toStatus, reason, code string
	if err := s.db.QueryRow(`SELECT action, to_status, reason, authorization_reason_code FROM memory_decision_events WHERE memory_id = ?`, id).
		Scan(&action, &toStatus, &reason, &code); err != nil {
		t.Fatalf("read decision event: %v", err)
	}
	if action != "rejected" || toStatus != "rejected" || code != "REJECTED" || reason != "amount differs from the comprobante" {
		t.Fatalf("decision event = %s→%s code %s reason %q", action, toStatus, code, reason)
	}
	var from, to, actor string
	if err := s.db.QueryRow(`SELECT from_status, to_status, actor FROM transition_log WHERE observation_id = ?`, id).
		Scan(&from, &to, &actor); err != nil {
		t.Fatalf("read transition: %v", err)
	}
	if from != "pending_review" || to != "rejected" || actor != "subject-1" {
		t.Fatalf("transition = %s→%s by %s", from, to, actor)
	}

	// The memory_rejected receipt carries the EXTENDED payload (v0.10.0: reason +
	// reviewed H1 + resulting H2 + principal snapshot).
	r := byAction(t, readReceipts(t, s), string(core.ReceiptActionMemoryRejected))
	verifyStored(t, signer, r)
	p := r.payload(t)
	if p.Version != core.ReceiptPayloadVersionV10 {
		t.Errorf("reject payload version = %q, want %q", p.Version, core.ReceiptPayloadVersionV10)
	}
	if p.Reason != "amount differs from the comprobante" || p.ReviewedEnvelopeHash != currentEnvelope(res) {
		t.Errorf("reject payload reason/H1 = %q/%q", p.Reason, p.ReviewedEnvelopeHash)
	}
	if p.ResultingEnvelopeHash == p.ReviewedEnvelopeHash {
		t.Error("reject payload H2 must differ from H1 (status participates)")
	}
	if p.PrincipalID != "subject-1" || p.MembershipID != "membership-1" || p.PolicyVersion != "kernel/v0.4.0" {
		t.Errorf("reject payload principal/policy = %q/%q/%q", p.PrincipalID, p.MembershipID, p.PolicyVersion)
	}
	// The receipt's claimed-act provenance resolves through the transition log.
	if _, ok, err := s.ReceiptActProvenance(ctxForTest(), core.SubjectTypeMemory, id, core.ReceiptActionMemoryRejected, r.IssuedAt, ""); err != nil || !ok {
		t.Fatalf("reject receipt provenance = ok %v err %v, want ok", ok, err)
	}

	// A second decision on the same memory is ALREADY_DECIDED.
	if _, err := s.RejectMemory(ctxForTest(), core.RejectMemoryCommand{
		MemoryID: id, ExpectedEnvelopeHash: currentEnvelope(res), Reason: "again", RequestID: "req-reject-again",
	}, principal); auth.Code(err) != auth.CodeAlreadyDecided {
		t.Fatalf("second reject = %v, want ALREADY_DECIDED", err)
	}
}

func TestRejectMemoryReasonPolicy(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	principal := controllerPrincipal(t)
	ctx := ctxForTest()

	// fiscalEffect=closing demands a reason.
	closing := func() core.SaveInput {
		in := reviewSaveInput("review.reject.closing", "close", "agent-a")
		in.FiscalEffect = core.FiscalEffectClosing
		return in
	}()
	res := mustSave(t, s, closing)
	_, err := s.RejectMemory(ctx, core.RejectMemoryCommand{
		MemoryID: res.Memory.Identity.ID, ExpectedEnvelopeHash: currentEnvelope(res), Reason: "", RequestID: "req-reject-closing",
	}, principal)
	if auth.Code(err) != auth.CodeReasonRequired {
		t.Fatalf("closing reject without reason = %v, want REASON_REQUIRED", err)
	}
	// The failed attempt left the memory untouched.
	if mem, ok := s.FindByID(res.Memory.Identity.ID); !ok || mem.Status != core.StatusPendingReview {
		t.Fatalf("failed reject mutated the memory: %v", mem.Status)
	}

	// materiality=material demands a reason.
	lvl := core.MaterialityMaterial
	mat := mustSave(t, s, func() core.SaveInput {
		in := reviewSaveInput("review.reject.material", "mat", "agent-a")
		in.MaterialityLevel = &lvl
		return in
	}())
	_, err = s.RejectMemory(ctx, core.RejectMemoryCommand{
		MemoryID: mat.Memory.Identity.ID, ExpectedEnvelopeHash: currentEnvelope(mat), Reason: "", RequestID: "req-reject-material",
	}, principal)
	if auth.Code(err) != auth.CodeReasonRequired {
		t.Fatalf("material reject without reason = %v, want REASON_REQUIRED", err)
	}

	// A normal/adjustment memory accepts an OPTIONAL reason — empty succeeds and
	// the event still persists an empty reason.
	normal := mustSave(t, s, reviewSaveInput("review.reject.normal", "normal", "agent-a"))
	res2, err := s.RejectMemory(ctx, core.RejectMemoryCommand{
		MemoryID: normal.Memory.Identity.ID, ExpectedEnvelopeHash: currentEnvelope(normal), Reason: "", RequestID: "req-reject-normal",
	}, principal)
	if err != nil {
		t.Fatalf("normal reject with empty reason must succeed: %v", err)
	}
	if res2.CurrentStatus != "rejected" {
		t.Errorf("normal reject status = %s", res2.CurrentStatus)
	}
}

func TestRejectMemorySODViolation(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	// The pending revision is recorded by subject-1 — the SAME human reviewer.
	res := mustSave(t, s, humanRecordedInput("review.reject.sod", "self proposal"))
	id := res.Memory.Identity.ID

	_, err := s.RejectMemory(ctxForTest(), core.RejectMemoryCommand{
		MemoryID: id, ExpectedEnvelopeHash: currentEnvelope(res), Reason: "self-reject", RequestID: "req-reject-sod",
	}, controllerPrincipal(t))
	if auth.Code(err) != auth.CodeSODViolation {
		t.Fatalf("self-reject = %v, want SOD_VIOLATION", err)
	}
	if mem, ok := s.FindByID(id); !ok || mem.Status != core.StatusPendingReview {
		t.Fatal("SOD failure must leave the memory pending_review (fail-closed, no partial decision)")
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM memory_decision_events`); n != 0 {
		t.Fatalf("SOD failure wrote %d decision events, want 0", n)
	}
}

func TestRejectMemoryEnvelopeMismatch(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	res := mustSave(t, s, reviewSaveInput("review.reject.stale", "pending", "agent-a"))

	_, err := s.RejectMemory(ctxForTest(), core.RejectMemoryCommand{
		MemoryID: res.Memory.Identity.ID, ExpectedEnvelopeHash: strings.Repeat("0", 64), Reason: "stale", RequestID: "req-reject-stale",
	}, controllerPrincipal(t))
	if auth.Code(err) != auth.CodeEnvelopeMismatch {
		t.Fatalf("stale reject = %v, want ENVELOPE_MISMATCH", err)
	}
}

func TestRejectMemoryIdempotentReplay(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	res := mustSave(t, s, reviewSaveInput("review.reject.replay", "pending", "agent-a"))
	id := res.Memory.Identity.ID
	principal := controllerPrincipal(t)
	ctx := ctxForTest()
	cmd := core.RejectMemoryCommand{
		MemoryID: id, ExpectedEnvelopeHash: currentEnvelope(res), Reason: "replay me", RequestID: "req-reject-replay",
	}

	first, err := s.RejectMemory(ctx, cmd, principal)
	if err != nil {
		t.Fatalf("first reject: %v", err)
	}
	if first.IdempotentReplay {
		t.Fatal("first decision must not be a replay")
	}
	second, err := s.RejectMemory(ctx, cmd, principal)
	if err != nil {
		t.Fatalf("replay reject: %v", err)
	}
	if !second.IdempotentReplay || second.DecisionEventID != first.DecisionEventID {
		t.Fatalf("replay = %+v, want the stored result with idempotentReplay", second)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM memory_decision_events WHERE memory_id = ?`, id); n != 1 {
		t.Fatalf("replay created %d events, want 1", n)
	}
	// The same request id with a different command is IDEMPOTENCY_CONFLICT.
	changed := cmd
	changed.Reason = "a different command"
	if _, err := s.RejectMemory(ctx, changed, principal); auth.Code(err) != auth.CodeIdempotencyConflict {
		t.Fatalf("request id reuse = %v, want IDEMPOTENCY_CONFLICT", err)
	}
}

// ──────────────────────────────────────────────
// Return — authenticated decision (design §5)
// ──────────────────────────────────────────────

func TestReturnMemoryHappyPathAndSaveReentersPendingReview(t *testing.T) {
	s, signer := receiptStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	res := mustSave(t, s, reviewSaveInput("review.return.happy", "pending", "agent-a"))
	id := res.Memory.Identity.ID
	principal := controllerPrincipal(t)
	ctx := ctxForTest()

	result, err := s.ReturnMemory(ctx, core.ReturnMemoryCommand{
		MemoryID: id, ExpectedEnvelopeHash: currentEnvelope(res), Reason: "missing supplier substantiation", RequestID: "req-return-happy",
	}, principal)
	if err != nil {
		t.Fatalf("return: %v", err)
	}
	if result.CurrentStatus != "returned" || result.PreviousStatus != "pending_review" {
		t.Fatalf("return statuses = %s → %s", result.PreviousStatus, result.CurrentStatus)
	}
	if mem, ok := s.FindByID(id); !ok || mem.Status != core.StatusReturned {
		t.Fatalf("memory status = %v, want returned", mem.Status)
	}

	// The memory_returned receipt (new action, v0.10.0 payload) is on the chain.
	r := byAction(t, readReceipts(t, s), string(core.ReceiptActionMemoryReturned))
	verifyStored(t, signer, r)
	p := r.payload(t)
	if p.Version != core.ReceiptPayloadVersionV10 || p.Reason != "missing supplier substantiation" {
		t.Errorf("return payload version/reason = %q/%q", p.Version, p.Reason)
	}
	if p.ReviewedEnvelopeHash != currentEnvelope(res) || p.ResultingEnvelopeHash == p.ReviewedEnvelopeHash {
		t.Errorf("return payload H1/H2 = %q/%q, want H1 = reviewed and H2 != H1", p.ReviewedEnvelopeHash, p.ResultingEnvelopeHash)
	}

	// A returned revision never reopens: a second return on the SAME revision is
	// ALREADY_DECIDED.
	if _, err := s.ReturnMemory(ctx, core.ReturnMemoryCommand{
		MemoryID: id, ExpectedEnvelopeHash: currentEnvelope(res), Reason: "again", RequestID: "req-return-again",
	}, principal); auth.Code(err) != auth.CodeAlreadyDecided {
		t.Fatalf("return of a returned revision = %v, want ALREADY_DECIDED", err)
	}

	// An agent Save on the returned chain creates a NEW revision that re-enters
	// pending_review; the returned revision is superseded (never reopened).
	corrected := mustSave(t, s, reviewSaveInput("review.return.happy", "corrected", "agent-a"))
	if corrected.Memory.Revision != 2 || corrected.Memory.Status != core.StatusPendingReview {
		t.Fatalf("correction save = revision %d status %s, want revision 2 pending_review", corrected.Memory.Revision, corrected.Memory.Status)
	}
	if prev, ok := s.FindByID(id); !ok || prev.Status != core.StatusSuperseded || prev.SupersedesID != corrected.Memory.Identity.ID {
		t.Fatalf("returned revision after correction = %s supersedes %q, want superseded → %q", prev.Status, prev.SupersedesID, corrected.Memory.Identity.ID)
	}

	// The superseded returned revision is terminal: a decision is an invalid
	// transition, never a reopen. Only the NEW pending revision is decidable.
	if _, err := s.ReturnMemory(ctx, core.ReturnMemoryCommand{
		MemoryID: id, ExpectedEnvelopeHash: currentEnvelope(res), Reason: "after supersede", RequestID: "req-return-superseded",
	}, principal); auth.Code(err) != auth.CodeInvalidTransition {
		t.Fatalf("decision on a superseded returned revision = %v, want INVALID_TRANSITION", err)
	}
	if _, err := s.ReturnMemory(ctx, core.ReturnMemoryCommand{
		MemoryID: corrected.Memory.Identity.ID, ExpectedEnvelopeHash: currentEnvelope(corrected), Reason: "still missing", RequestID: "req-return-new",
	}, principal); err != nil {
		t.Fatalf("return of the corrected revision: %v", err)
	}
}

func TestReturnMemoryRequiresReason(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	res := mustSave(t, s, reviewSaveInput("review.return.reason", "pending", "agent-a"))

	_, err := s.ReturnMemory(ctxForTest(), core.ReturnMemoryCommand{
		MemoryID: res.Memory.Identity.ID, ExpectedEnvelopeHash: currentEnvelope(res), Reason: "", RequestID: "req-return-reason",
	}, controllerPrincipal(t))
	if auth.Code(err) != auth.CodeReasonRequired {
		t.Fatalf("return without reason = %v, want REASON_REQUIRED", err)
	}
}

// ──────────────────────────────────────────────
// Anti-rubber-stamp velocity alerts (design §6)
// ──────────────────────────────────────────────

func TestConsecutiveRejectionsEmitVelocityAlert(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	principal := controllerPrincipal(t)
	ctx := ctxForTest()

	for i := 0; i < core.ConsecutiveDecisionThreshold; i++ {
		res := mustSave(t, s, reviewSaveInput("review.velocity.reject", "pending", "agent-a"))
		if _, err := s.RejectMemory(ctx, core.RejectMemoryCommand{
			MemoryID: res.Memory.Identity.ID, ExpectedEnvelopeHash: currentEnvelope(res),
			Reason: "reject", RequestID: "req-velocity-" + string(rune('0'+i)),
		}, principal); err != nil {
			t.Fatalf("reject %d: %v", i, err)
		}
	}
	// The 3rd consecutive reject crosses the threshold → EXACTLY ONE alert.
	if n := countRows(t, s, `SELECT COUNT(*) FROM review_velocity_events`); n != 1 {
		t.Fatalf("velocity alerts = %d, want 1 (fresh crossing)", n)
	}
	var alertType, subject string
	var consecutive int
	if err := s.db.QueryRow(`SELECT alert_type, principal_subject_id, consecutive_count FROM review_velocity_events`).
		Scan(&alertType, &subject, &consecutive); err != nil {
		t.Fatalf("read velocity alert: %v", err)
	}
	if alertType != "consecutive_decisions" || subject != "subject-1" || consecutive != 3 {
		t.Fatalf("alert = %s/%s/%d, want consecutive_decisions/subject-1/3", alertType, subject, consecutive)
	}

	// An intervening approval RESETS the streak: a 4th reject does not re-alert.
	res := mustSave(t, s, reviewSaveInput("review.velocity.reset", "pending", "agent-a"))
	if _, err := approve(s, res.Memory.Identity.ID, currentEnvelope(res), "req-velocity-approve", principal); err != nil {
		t.Fatalf("approve reset: %v", err)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM review_velocity_events`); n != 1 {
		t.Fatalf("velocity alerts after approval = %d, want 1 (no re-alert)", n)
	}
}

func TestApprovalVelocityAlert(t *testing.T) {
	s := newTestStore(t)
	seedAcmeIdentity(t, s, []auth.AccountingRole{auth.RoleController})
	principal := controllerPrincipal(t)

	// Cross the >30 approvals/15min threshold: the 31st approval emits the alert.
	for i := 0; i < core.ApprovalVelocityThreshold+1; i++ {
		res := mustSave(t, s, reviewSaveInput("review.velocity.approve", "pending", "agent-a"))
		if _, err := approve(s, res.Memory.Identity.ID, currentEnvelope(res), "req-velocity-approve-"+string(rune('0'+i)), principal); err != nil {
			t.Fatalf("approve %d: %v", i, err)
		}
	}
	var alertType string
	var observed int
	if err := s.db.QueryRow(`SELECT alert_type, observed_count FROM review_velocity_events`).
		Scan(&alertType, &observed); err != nil {
		t.Fatalf("read approval velocity alert: %v", err)
	}
	if alertType != "approval_velocity" || observed != core.ApprovalVelocityThreshold+1 {
		t.Fatalf("alert = %s/%d, want approval_velocity/%d", alertType, observed, core.ApprovalVelocityThreshold+1)
	}
}
