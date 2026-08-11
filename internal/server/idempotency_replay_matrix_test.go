// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This file freezes the CONSOLIDATED
// IDEMPOTENCY REPLAY MATRIX (FR-L.1 / FR-L.2 / FR-L.6 / AC-L-1 / AC-L-5 /
// AC-L-6 / AC-L-7): one inspectable operation × surface catalog (EC-2) proving,
// per named row, that replaying the same tenant + request identifier + payload +
// relevant principal returns the ORIGINAL stored outcome (idempotentReplay=true
// where the surface exposes it) with NO duplicate domain event, receipt or
// mutation, and that reusing the key with a changed payload OR a changed
// principal fails with the frozen typed conflict (IDEMPOTENCY_CONFLICT or the
// per-operation typed conflict) — never a silent success, never a duplicate
// effect.
//
// Execution strategy per surface (design §L, D-2):
//
//   - HTTP rows execute the REAL registered handlers over httptest with the
//     real strict decoders and the real Idempotency-Key header.
//   - MCP rows execute the REAL tools over the session-less stdio MCPServer.
//     The four provenance-carried tools (judgment propose/withdraw,
//     reconciliation propose/withdraw) reach the domain service and prove the
//     full replay contract. The AUTHENTICATED tools (approve, judgment
//     confirm/reject, reconciliation confirm/reject, review reject, hold
//     place/lift, retention-policy put, period reopen, purge request/approve/
//     reject/cancel/withdraw/execute) FAIL CLOSED with AUTHENTICATION_REQUIRED
//     on every call (the frozen stdio contract — tool arguments never supply
//     identity, design §3): the row proves the deterministic fail-closed replay
//     semantics (identical outcome, zero effect) and links the same-operation
//     HTTP row, which executes the exact domain service the tool delegates to
//     with a bound principal.
//   - CLI and sync rows are NOT runnable in this package: they name the
//     dedicated proof test via externalProof, and the `proof-links-resolve`
//     subtest parses the package test declarations with go/parser so a stale
//     link fails the matrix (FR-L.7 — TestSyncIdempotent and
//     TestCLISyncRoundTrip; AC-L-2/AC-L-3 dedicated tests).
//
// Existing replay/conflict anchors stay green (approval_test.go:505/:544,
// judgment_test.go:677/:719/:763, reconciliation_test.go:685/:736/:763,
// review_store_test.go:589, hold_test.go:172/:189, retention_policy_test.go:178,
// reopen_test.go:237, receipt_emission_test.go:429, http_review_test.go:268,
// purge_http_test.go:279–297/:533–547). The matrix supplements, never replaces.
package server

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
)

type replaySurface string

const (
	surfaceHTTP replaySurface = "HTTP"
	surfaceMCP  replaySurface = "MCP"
	surfaceCLI  replaySurface = "CLI"
	surfaceSync replaySurface = "sync"
)

// replayInvocation is one transport call (HTTP request or MCP tool call) with
// its exact tenant-scoped idempotency key.
type replayInvocation struct {
	// HTTP
	method string
	url    string
	token  string
	key    string
	body   any
	// MCP
	mcpTool string
	mcpArgs map[string]any
}

// replayResponse is the decoded transport result.
type replayResponse struct {
	status int
	raw    string
	// mcpIsError is the in-band tool failure marker (MCP rows).
	mcpIsError bool
}

// effectSnapshot is a deterministic LOGICAL digest (doctor counts per
// operation + idempotency-ledger counts) — never raw SQLite bytes.
type effectSnapshot map[string]int64

// replayFixture carries the server-local state an operation row needs.
type replayFixture struct {
	ts  *httptest.Server
	api *API
	m   *MCPServer

	controllerToken string
	accountantToken string
	recordsToken    string
	secondToken     string

	memoryID string
	envelope string

	fromID string
	toID   string

	leftID  string
	rightID string

	judgmentID         string
	judgmentHash       string
	reconciliationID   string
	reconciliationHash string

	objectID   string
	policy     core.RetentionPolicy
	scope      core.Scope
	h          string
	hRequested string
	hApproved  string
	requestID  string
	approvalID string
	holdID     string
	closeID    string
	key        string
}

// replayCase is the consolidated catalog row (design §L).
type replayCase struct {
	operation        string
	surface          replaySurface
	fixture          func(t *testing.T) replayFixture
	firstRequest     func(replayFixture) replayInvocation
	sameRequest      func(replayFixture) replayInvocation
	changedPayload   func(replayFixture) replayInvocation
	changedPrincipal func(replayFixture) replayInvocation
	assertOutcome    func(t *testing.T, f replayFixture, first, replay replayResponse)
	assertConflict   func(t *testing.T, response replayResponse)
	effectSnapshot   func(t *testing.T, f replayFixture) effectSnapshot
	externalProof    string
	// failClosed marks the session-bound MCP rows (see the file doc comment).
	failClosed bool
}

// httpErrorCode extracts the frozen typed error code from an HTTP error body.
func httpErrorCode(t *testing.T, raw string) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode http error body %q: %v", raw, err)
	}
	return body.Error.Code
}

// doctorDigest is the logical zero-effect digest of the whole store.
func doctorDigest(t *testing.T, api *API) effectSnapshot {
	t.Helper()
	report, err := api.Doctor()
	if err != nil {
		t.Fatalf("doctor digest: %v", err)
	}
	return effectSnapshot{
		"observations":     int64(report.Observations),
		"pendingApprovals": int64(report.PendingApprovals),
		"transitions":      int64(report.Transitions),
		"purgeRequests":    int64(report.PurgeRequests),
		"purgeApprovals":   int64(report.PurgeApprovals),
		"lifecycleEvents":  int64(report.LifecycleEvents),
		"purgeIdemKeys":    int64(report.PurgeIdempotencyKeys),
		"holds":            int64(report.Holds),
		"holdIdemKeys":     int64(report.HoldIdempotencyKeys),
	}
}

// invoke executes one transport call.
func invoke(t *testing.T, f replayFixture, in replayInvocation) replayResponse {
	t.Helper()
	if in.mcpTool != "" {
		response := call(t, f.m, 1, "tools/call", map[string]any{"name": in.mcpTool, "arguments": in.mcpArgs})
		if response.Error != nil {
			t.Fatalf("MCP %s must not be a JSON-RPC error: %+v", in.mcpTool, response.Error)
		}
		var output toolCallOutput
		if err := json.Unmarshal(response.Result, &output); err != nil {
			t.Fatalf("decode MCP tool result: %v", err)
		}
		raw := ""
		if len(output.Content) > 0 {
			raw = output.Content[0]["text"]
		}
		return replayResponse{status: 200, raw: raw, mcpIsError: output.IsError}
	}
	status, raw := approvalHTTP(t, in.method, in.url, in.token, in.key, in.body)
	return replayResponse{status: status, raw: raw}
}

// assertSnapshotEqual asserts replay/recovery left the logical digest unchanged.
func assertSnapshotEqual(t *testing.T, before, after effectSnapshot) {
	t.Helper()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("effect snapshot changed: before %v after %v", before, after)
	}
}

// TestIdempotencyReplayMatrix is the consolidated catalog (AC-L-1, FR-L.1,
// FR-L.2, FR-L.6, AC-L-5, AC-L-6, AC-L-7). One named row per operation/surface
// (EC-2).
func TestIdempotencyReplayMatrix(t *testing.T) {
	cases := []replayCase{
		// ── approval ──────────────────────────────────────────────
		{
			operation: "approve-memory", surface: surfaceHTTP,
			fixture:          matrixApproveFixture,
			firstRequest:     matrixApproveInvoke,
			sameRequest:      matrixApproveInvoke,
			changedPayload:   matrixApproveChangedPayload,
			changedPrincipal: matrixApproveChangedPrincipal,
			assertOutcome:    matrixApproveAssert,
			assertConflict: func(t *testing.T, r replayResponse) {
				if r.status != http.StatusConflict || httpErrorCode(t, r.raw) != auth.CodeIdempotencyConflict {
					t.Fatalf("approve conflict = %d %s, want 409 IDEMPOTENCY_CONFLICT", r.status, r.raw)
				}
			},
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},
		{
			operation: "approve-memory", surface: surfaceMCP, failClosed: true,
			fixture:        matrixApproveFixture,
			firstRequest:   matrixMCPApproveInvoke,
			sameRequest:    matrixMCPApproveInvoke,
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},

		// ── judgment ──────────────────────────────────────────────
		{
			operation: "judgment-propose", surface: surfaceHTTP,
			fixture:          matrixJudgmentProposeFixture,
			firstRequest:     matrixJudgmentProposeInvoke,
			sameRequest:      matrixJudgmentProposeInvoke,
			changedPayload:   matrixJudgmentProposeChangedPayload,
			changedPrincipal: matrixJudgmentProposeChangedPrincipal,
			assertOutcome:    matrixJudgmentProposeAssert,
			assertConflict: func(t *testing.T, r replayResponse) {
				if r.status != http.StatusConflict || httpErrorCode(t, r.raw) != auth.CodeIdempotencyConflict {
					t.Fatalf("judgment propose conflict = %d %s, want 409 IDEMPOTENCY_CONFLICT", r.status, r.raw)
				}
			},
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},
		{
			operation: "judgment-propose", surface: surfaceMCP,
			fixture:          matrixJudgmentProposeFixture,
			firstRequest:     matrixMCPJudgmentProposeInvoke,
			sameRequest:      matrixMCPJudgmentProposeInvoke,
			changedPayload:   matrixMCPJudgmentProposeChangedPayload,
			changedPrincipal: matrixJudgmentProposeChangedPrincipal, // MCP has no principal; HTTP row proves it
			assertOutcome:    matrixJudgmentProposeAssert,
			assertConflict: func(t *testing.T, r replayResponse) {
				if !r.mcpIsError || !strings.Contains(r.raw, "IDEMPOTENCY_CONFLICT") {
					t.Fatalf("MCP judgment propose conflict = %q, want IDEMPOTENCY_CONFLICT", r.raw)
				}
			},
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},
		{
			operation: "judgment-confirm", surface: surfaceHTTP,
			fixture:          matrixJudgmentConfirmFixture,
			firstRequest:     matrixJudgmentConfirmInvoke,
			sameRequest:      matrixJudgmentConfirmInvoke,
			changedPayload:   matrixJudgmentConfirmChangedPayload,
			changedPrincipal: matrixJudgmentConfirmChangedPrincipal,
			assertOutcome:    matrixJudgmentConfirmAssert,
			assertConflict: func(t *testing.T, r replayResponse) {
				if r.status != http.StatusConflict || httpErrorCode(t, r.raw) != auth.CodeIdempotencyConflict {
					t.Fatalf("judgment confirm conflict = %d %s, want 409 IDEMPOTENCY_CONFLICT", r.status, r.raw)
				}
			},
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},
		{
			operation: "judgment-confirm", surface: surfaceMCP, failClosed: true,
			fixture:        matrixJudgmentConfirmFixture,
			firstRequest:   matrixMCPJudgmentConfirmInvoke,
			sameRequest:    matrixMCPJudgmentConfirmInvoke,
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},
		{
			operation: "judgment-reject", surface: surfaceHTTP,
			fixture:          matrixJudgmentRejectFixture,
			firstRequest:     matrixJudgmentRejectInvoke,
			sameRequest:      matrixJudgmentRejectInvoke,
			changedPayload:   matrixJudgmentRejectChangedPayload,
			changedPrincipal: matrixJudgmentRejectChangedPrincipal,
			assertOutcome:    matrixJudgmentRejectAssert,
			assertConflict: func(t *testing.T, r replayResponse) {
				if r.status != http.StatusConflict || httpErrorCode(t, r.raw) != auth.CodeIdempotencyConflict {
					t.Fatalf("judgment reject conflict = %d %s, want 409 IDEMPOTENCY_CONFLICT", r.status, r.raw)
				}
			},
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},
		{
			operation: "judgment-withdraw", surface: surfaceHTTP,
			fixture:          matrixJudgmentWithdrawFixture,
			firstRequest:     matrixJudgmentWithdrawInvoke,
			sameRequest:      matrixJudgmentWithdrawInvoke,
			changedPayload:   matrixJudgmentWithdrawChangedPayload,
			changedPrincipal: matrixJudgmentWithdrawChangedPrincipal,
			assertOutcome:    matrixJudgmentWithdrawAssert,
			assertConflict: func(t *testing.T, r replayResponse) {
				if r.status != http.StatusConflict || httpErrorCode(t, r.raw) != auth.CodeIdempotencyConflict {
					t.Fatalf("judgment withdraw conflict = %d %s, want 409 IDEMPOTENCY_CONFLICT", r.status, r.raw)
				}
			},
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},
		{
			operation: "judgment-withdraw", surface: surfaceMCP,
			fixture:          matrixJudgmentWithdrawFixture,
			firstRequest:     matrixMCPJudgmentWithdrawInvoke,
			sameRequest:      matrixMCPJudgmentWithdrawInvoke,
			changedPayload:   matrixMCPJudgmentWithdrawChangedPayload,
			changedPrincipal: matrixJudgmentWithdrawChangedPrincipal,
			assertOutcome:    matrixJudgmentWithdrawAssert,
			assertConflict: func(t *testing.T, r replayResponse) {
				if !r.mcpIsError || !strings.Contains(r.raw, "IDEMPOTENCY_CONFLICT") {
					t.Fatalf("MCP judgment withdraw conflict = %q, want IDEMPOTENCY_CONFLICT", r.raw)
				}
			},
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},

		// ── reconciliation ────────────────────────────────────────
		{
			operation: "reconciliation-propose", surface: surfaceHTTP,
			fixture:          matrixReconciliationProposeFixture,
			firstRequest:     matrixReconciliationProposeInvoke,
			sameRequest:      matrixReconciliationProposeInvoke,
			changedPayload:   matrixReconciliationProposeChangedPayload,
			changedPrincipal: matrixReconciliationProposeChangedPrincipal,
			assertOutcome:    matrixReconciliationProposeAssert,
			assertConflict: func(t *testing.T, r replayResponse) {
				if r.status != http.StatusConflict || httpErrorCode(t, r.raw) != auth.CodeIdempotencyConflict {
					t.Fatalf("reconciliation propose conflict = %d %s, want 409 IDEMPOTENCY_CONFLICT", r.status, r.raw)
				}
			},
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},
		{
			operation: "reconciliation-propose", surface: surfaceMCP,
			fixture:          matrixReconciliationProposeFixture,
			firstRequest:     matrixMCPReconciliationProposeInvoke,
			sameRequest:      matrixMCPReconciliationProposeInvoke,
			changedPayload:   matrixMCPReconciliationProposeChangedPayload,
			changedPrincipal: matrixReconciliationProposeChangedPrincipal,
			assertOutcome:    matrixReconciliationProposeAssert,
			assertConflict: func(t *testing.T, r replayResponse) {
				if !r.mcpIsError || !strings.Contains(r.raw, "IDEMPOTENCY_CONFLICT") {
					t.Fatalf("MCP reconciliation propose conflict = %q, want IDEMPOTENCY_CONFLICT", r.raw)
				}
			},
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},
		{
			operation: "reconciliation-confirm", surface: surfaceHTTP,
			fixture:          matrixReconciliationConfirmFixture,
			firstRequest:     matrixReconciliationConfirmInvoke,
			sameRequest:      matrixReconciliationConfirmInvoke,
			changedPayload:   matrixReconciliationConfirmChangedPayload,
			changedPrincipal: matrixReconciliationConfirmChangedPrincipal,
			assertOutcome:    matrixReconciliationConfirmAssert,
			assertConflict: func(t *testing.T, r replayResponse) {
				if r.status != http.StatusConflict || httpErrorCode(t, r.raw) != auth.CodeIdempotencyConflict {
					t.Fatalf("reconciliation confirm conflict = %d %s, want 409 IDEMPOTENCY_CONFLICT", r.status, r.raw)
				}
			},
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},
		{
			operation: "reconciliation-confirm", surface: surfaceMCP, failClosed: true,
			fixture:        matrixReconciliationConfirmFixture,
			firstRequest:   matrixMCPReconciliationConfirmInvoke,
			sameRequest:    matrixMCPReconciliationConfirmInvoke,
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},
		{
			operation: "reconciliation-reject", surface: surfaceHTTP,
			fixture:          matrixReconciliationRejectFixture,
			firstRequest:     matrixReconciliationRejectInvoke,
			sameRequest:      matrixReconciliationRejectInvoke,
			changedPayload:   matrixReconciliationRejectChangedPayload,
			changedPrincipal: matrixReconciliationRejectChangedPrincipal,
			assertOutcome:    matrixReconciliationRejectAssert,
			assertConflict: func(t *testing.T, r replayResponse) {
				if r.status != http.StatusConflict || httpErrorCode(t, r.raw) != auth.CodeIdempotencyConflict {
					t.Fatalf("reconciliation reject conflict = %d %s, want 409 IDEMPOTENCY_CONFLICT", r.status, r.raw)
				}
			},
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},
		{
			operation: "reconciliation-withdraw", surface: surfaceHTTP,
			fixture:          matrixReconciliationWithdrawFixture,
			firstRequest:     matrixReconciliationWithdrawInvoke,
			sameRequest:      matrixReconciliationWithdrawInvoke,
			changedPayload:   matrixReconciliationWithdrawChangedPayload,
			changedPrincipal: matrixReconciliationWithdrawChangedPrincipal,
			assertOutcome:    matrixReconciliationWithdrawAssert,
			assertConflict: func(t *testing.T, r replayResponse) {
				if r.status != http.StatusConflict || httpErrorCode(t, r.raw) != auth.CodeIdempotencyConflict {
					t.Fatalf("reconciliation withdraw conflict = %d %s, want 409 IDEMPOTENCY_CONFLICT", r.status, r.raw)
				}
			},
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},
		{
			operation: "reconciliation-withdraw", surface: surfaceMCP,
			fixture:          matrixReconciliationWithdrawFixture,
			firstRequest:     matrixMCPReconciliationWithdrawInvoke,
			sameRequest:      matrixMCPReconciliationWithdrawInvoke,
			changedPayload:   matrixMCPReconciliationWithdrawChangedPayload,
			changedPrincipal: matrixReconciliationWithdrawChangedPrincipal,
			assertOutcome:    matrixReconciliationWithdrawAssert,
			assertConflict: func(t *testing.T, r replayResponse) {
				if !r.mcpIsError || !strings.Contains(r.raw, "IDEMPOTENCY_CONFLICT") {
					t.Fatalf("MCP reconciliation withdraw conflict = %q, want IDEMPOTENCY_CONFLICT", r.raw)
				}
			},
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},

		// ── review reject ─────────────────────────────────────────
		{
			operation: "review-reject", surface: surfaceHTTP,
			fixture:          matrixReviewRejectFixture,
			firstRequest:     matrixReviewRejectInvoke,
			sameRequest:      matrixReviewRejectInvoke,
			changedPayload:   matrixReviewRejectChangedPayload,
			changedPrincipal: matrixReviewRejectChangedPrincipal,
			assertOutcome:    matrixReviewRejectAssert,
			assertConflict: func(t *testing.T, r replayResponse) {
				if r.status != http.StatusConflict || httpErrorCode(t, r.raw) != auth.CodeIdempotencyConflict {
					t.Fatalf("review reject conflict = %d %s, want 409 IDEMPOTENCY_CONFLICT", r.status, r.raw)
				}
			},
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},
		{
			operation: "review-reject", surface: surfaceMCP, failClosed: true,
			fixture:        matrixReviewRejectFixture,
			firstRequest:   matrixMCPReviewRejectInvoke,
			sameRequest:    matrixMCPReviewRejectInvoke,
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},

		// ── evidence lifecycle: hold place/lift, retention-policy put ──
		{
			operation: "hold-place", surface: surfaceHTTP,
			fixture:          matrixHoldPlaceFixture,
			firstRequest:     matrixHoldPlaceInvoke,
			sameRequest:      matrixHoldPlaceInvoke,
			changedPayload:   matrixHoldPlaceChangedPayload,
			changedPrincipal: matrixHoldPlaceChangedPrincipal,
			assertOutcome:    matrixHoldPlaceAssert,
			assertConflict: func(t *testing.T, r replayResponse) {
				if r.status != http.StatusConflict || httpErrorCode(t, r.raw) != auth.CodeIdempotencyConflict {
					t.Fatalf("hold place conflict = %d %s, want 409 IDEMPOTENCY_CONFLICT", r.status, r.raw)
				}
			},
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},
		{
			operation: "hold-place", surface: surfaceMCP, failClosed: true,
			fixture:        matrixHoldPlaceFixture,
			firstRequest:   matrixMCPHoldPlaceInvoke,
			sameRequest:    matrixMCPHoldPlaceInvoke,
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},
		{
			operation: "hold-lift", surface: surfaceHTTP,
			fixture:          matrixHoldLiftFixture,
			firstRequest:     matrixHoldLiftInvoke,
			sameRequest:      matrixHoldLiftInvoke,
			changedPayload:   matrixHoldLiftChangedPayload,
			changedPrincipal: matrixHoldLiftChangedPrincipal,
			assertOutcome:    matrixHoldLiftAssert,
			assertConflict: func(t *testing.T, r replayResponse) {
				if r.status != http.StatusConflict || httpErrorCode(t, r.raw) != auth.CodeIdempotencyConflict {
					t.Fatalf("hold lift conflict = %d %s, want 409 IDEMPOTENCY_CONFLICT", r.status, r.raw)
				}
			},
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},
		{
			operation: "hold-lift", surface: surfaceMCP, failClosed: true,
			fixture:        matrixHoldLiftFixture,
			firstRequest:   matrixMCPHoldLiftInvoke,
			sameRequest:    matrixMCPHoldLiftInvoke,
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},
		{
			operation: "retention-policy-put", surface: surfaceHTTP,
			fixture:          matrixRetentionPutFixture,
			firstRequest:     matrixRetentionPutInvoke,
			sameRequest:      matrixRetentionPutInvoke,
			changedPayload:   matrixRetentionPutChangedPayload,
			changedPrincipal: matrixRetentionPutChangedPrincipal,
			assertOutcome:    matrixRetentionPutAssert,
			assertConflict: func(t *testing.T, r replayResponse) {
				if r.status != http.StatusConflict || httpErrorCode(t, r.raw) != auth.CodeIdempotencyConflict {
					t.Fatalf("retention put conflict = %d %s, want 409 IDEMPOTENCY_CONFLICT", r.status, r.raw)
				}
			},
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},
		{
			operation: "retention-policy-put", surface: surfaceMCP, failClosed: true,
			fixture:        matrixRetentionPutFixture,
			firstRequest:   matrixMCPRetentionPutInvoke,
			sameRequest:    matrixMCPRetentionPutInvoke,
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},

		// ── period reopen ─────────────────────────────────────────
		{
			operation: "period-reopen", surface: surfaceHTTP,
			fixture:          matrixReopenFixture,
			firstRequest:     matrixReopenInvoke,
			sameRequest:      matrixReopenInvoke,
			changedPayload:   matrixReopenChangedPayload,
			changedPrincipal: matrixReopenChangedPrincipal,
			assertOutcome:    matrixReopenAssert,
			assertConflict: func(t *testing.T, r replayResponse) {
				if r.status != http.StatusConflict || httpErrorCode(t, r.raw) != auth.CodeIdempotencyConflict {
					t.Fatalf("reopen conflict = %d %s, want 409 IDEMPOTENCY_CONFLICT", r.status, r.raw)
				}
			},
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},
		{
			operation: "period-reopen", surface: surfaceMCP, failClosed: true,
			fixture:        matrixReopenFixture,
			firstRequest:   matrixMCPReopenInvoke,
			sameRequest:    matrixMCPReopenInvoke,
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},

		// ── purge lifecycle ───────────────────────────────────────
		{
			operation: "purge-request", surface: surfaceHTTP,
			fixture:          matrixPurgeRequestFixture,
			firstRequest:     matrixPurgeRequestInvoke,
			sameRequest:      matrixPurgeRequestInvoke,
			changedPayload:   matrixPurgeRequestChangedPayload,
			changedPrincipal: matrixPurgeRequestChangedPrincipal,
			assertOutcome:    matrixPurgeRequestAssert,
			assertConflict: func(t *testing.T, r replayResponse) {
				if r.status != http.StatusConflict || httpErrorCode(t, r.raw) != auth.CodeIdempotencyConflict {
					t.Fatalf("purge request conflict = %d %s, want 409 IDEMPOTENCY_CONFLICT", r.status, r.raw)
				}
			},
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},
		{
			operation: "purge-request", surface: surfaceMCP, failClosed: true,
			fixture:        matrixPurgeRequestFixture,
			firstRequest:   matrixMCPPurgeRequestInvoke,
			sameRequest:    matrixMCPPurgeRequestInvoke,
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},
		{
			operation: "purge-approve", surface: surfaceHTTP,
			fixture:          matrixPurgeApproveFixture,
			firstRequest:     matrixPurgeApproveInvoke,
			sameRequest:      matrixPurgeApproveInvoke,
			changedPayload:   matrixPurgeApproveChangedPayload,
			changedPrincipal: matrixPurgeApproveChangedPrincipal,
			assertOutcome:    matrixPurgeApproveAssert,
			assertConflict: func(t *testing.T, r replayResponse) {
				if r.status != http.StatusConflict || httpErrorCode(t, r.raw) != auth.CodeIdempotencyConflict {
					t.Fatalf("purge approve conflict = %d %s, want 409 IDEMPOTENCY_CONFLICT", r.status, r.raw)
				}
			},
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},
		{
			operation: "purge-reject", surface: surfaceHTTP,
			fixture:          matrixPurgeRejectFixture,
			firstRequest:     matrixPurgeRejectInvoke,
			sameRequest:      matrixPurgeRejectInvoke,
			changedPayload:   matrixPurgeRejectChangedPayload,
			changedPrincipal: matrixPurgeRejectChangedPrincipal,
			assertOutcome:    matrixPurgeRejectAssert,
			assertConflict: func(t *testing.T, r replayResponse) {
				if r.status != http.StatusConflict || httpErrorCode(t, r.raw) != auth.CodeIdempotencyConflict {
					t.Fatalf("purge reject conflict = %d %s, want 409 IDEMPOTENCY_CONFLICT", r.status, r.raw)
				}
			},
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},
		{
			operation: "purge-cancel", surface: surfaceHTTP,
			fixture:          matrixPurgeCancelFixture,
			firstRequest:     matrixPurgeCancelInvoke,
			sameRequest:      matrixPurgeCancelInvoke,
			changedPayload:   matrixPurgeCancelChangedPayload,
			changedPrincipal: matrixPurgeCancelChangedPrincipal,
			assertOutcome:    matrixPurgeCancelAssert,
			assertConflict: func(t *testing.T, r replayResponse) {
				if r.status != http.StatusConflict || httpErrorCode(t, r.raw) != auth.CodeIdempotencyConflict {
					t.Fatalf("purge cancel conflict = %d %s, want 409 IDEMPOTENCY_CONFLICT", r.status, r.raw)
				}
			},
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},
		{
			operation: "purge-withdraw", surface: surfaceHTTP,
			fixture:          matrixPurgeWithdrawFixture,
			firstRequest:     matrixPurgeWithdrawInvoke,
			sameRequest:      matrixPurgeWithdrawInvoke,
			changedPayload:   matrixPurgeWithdrawChangedPayload,
			changedPrincipal: matrixPurgeWithdrawChangedPrincipal,
			assertOutcome:    matrixPurgeWithdrawAssert,
			assertConflict: func(t *testing.T, r replayResponse) {
				if r.status != http.StatusConflict || httpErrorCode(t, r.raw) != auth.CodeIdempotencyConflict {
					t.Fatalf("purge withdraw conflict = %d %s, want 409 IDEMPOTENCY_CONFLICT", r.status, r.raw)
				}
			},
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},
		{
			operation: "purge-execute", surface: surfaceHTTP,
			fixture:          matrixPurgeExecuteFixture,
			firstRequest:     matrixPurgeExecuteInvoke,
			sameRequest:      matrixPurgeExecuteInvoke,
			changedPayload:   matrixPurgeExecuteChangedPayload,
			changedPrincipal: matrixPurgeExecuteChangedPrincipal,
			assertOutcome:    matrixPurgeExecuteAssert,
			assertConflict: func(t *testing.T, r replayResponse) {
				if r.status != http.StatusConflict || httpErrorCode(t, r.raw) != auth.CodeIdempotencyConflict {
					t.Fatalf("purge execute conflict = %d %s, want 409 IDEMPOTENCY_CONFLICT", r.status, r.raw)
				}
			},
			effectSnapshot: func(t *testing.T, f replayFixture) effectSnapshot { return doctorDigest(t, f.api) },
		},

		// ── CLI / sync rows: dedicated external proofs (FR-L.7) ───
		{
			operation: "judge", surface: surfaceCLI,
			fixture:       func(t *testing.T) replayFixture { return replayFixture{key: "req-cli-judge-replay-1"} },
			firstRequest:  func(replayFixture) replayInvocation { return replayInvocation{} },
			sameRequest:   func(replayFixture) replayInvocation { return replayInvocation{} },
			externalProof: "TestCLIJudgeReplay",
		},
		{
			operation: "reconcile", surface: surfaceCLI,
			fixture:       func(t *testing.T) replayFixture { return replayFixture{key: "req-cli-reconcile-replay-1"} },
			firstRequest:  func(replayFixture) replayInvocation { return replayInvocation{} },
			sameRequest:   func(replayFixture) replayInvocation { return replayInvocation{} },
			externalProof: "TestCLIReconcileReplay",
		},
		{
			operation: "review-reject", surface: surfaceCLI,
			fixture:       func(t *testing.T) replayFixture { return replayFixture{key: "req-cli-review-reject-replay-1"} },
			firstRequest:  func(replayFixture) replayInvocation { return replayInvocation{} },
			sameRequest:   func(replayFixture) replayInvocation { return replayInvocation{} },
			externalProof: "TestCLIReviewRejectReplay",
		},
		{
			operation: "purge", surface: surfaceCLI,
			fixture:       func(t *testing.T) replayFixture { return replayFixture{key: "req-purge-cli-replay-request"} },
			firstRequest:  func(replayFixture) replayInvocation { return replayInvocation{} },
			sameRequest:   func(replayFixture) replayInvocation { return replayInvocation{} },
			externalProof: "TestCLIPurgeRequestReplay",
		},
		{
			operation: "sync-second-import", surface: surfaceSync,
			fixture:       func(t *testing.T) replayFixture { return replayFixture{} },
			firstRequest:  func(replayFixture) replayInvocation { return replayInvocation{} },
			sameRequest:   func(replayFixture) replayInvocation { return replayInvocation{} },
			externalProof: "TestSyncIdempotent",
		},
		{
			operation: "sync-cli-round-trip", surface: surfaceSync,
			fixture:       func(t *testing.T) replayFixture { return replayFixture{} },
			firstRequest:  func(replayFixture) replayInvocation { return replayInvocation{} },
			sameRequest:   func(replayFixture) replayInvocation { return replayInvocation{} },
			externalProof: "TestCLISyncRoundTrip",
		},
	}

	for _, tc := range cases {
		t.Run(tc.operation+"/"+string(tc.surface)+"/replay", func(t *testing.T) {
			f := tc.fixture(t)
			f.key = "matrix-key-" + tc.operation + "-" + strings.ReplaceAll(string(tc.surface), "/", "-")

			if tc.failClosed {
				// Session-bound MCP surface: the tool cannot reach the domain
				// service on the session-less stdio server — both calls fail
				// closed with the SAME deterministic AUTHENTICATION_REQUIRED
				// result and zero effect (the surface's frozen replay semantics).
				before := tc.effectSnapshot(t, f)
				first := invoke(t, f, tc.firstRequest(f))
				if !first.mcpIsError || !strings.Contains(first.raw, "AUTHENTICATION_REQUIRED") {
					t.Fatalf("session-bound MCP tool = %q (isError=%v), want the deterministic AUTHENTICATION_REQUIRED fail-closed result", first.raw, first.mcpIsError)
				}
				replay := invoke(t, f, tc.sameRequest(f))
				if !replay.mcpIsError || replay.raw != first.raw {
					t.Fatalf("replayed fail-closed result = %q, want the identical %q", replay.raw, first.raw)
				}
				assertSnapshotEqual(t, before, tc.effectSnapshot(t, f))
				return
			}

			if tc.externalProof != "" {
				// CLI/sync row: the dedicated test (validated by
				// proof-links-resolve below) is the executable proof.
				return
			}

			first := invoke(t, f, tc.firstRequest(f))
			if first.status != http.StatusOK && first.status != http.StatusCreated {
				t.Fatalf("first request status = %d, want 2xx; body %s", first.status, first.raw)
			}
			afterFirst := tc.effectSnapshot(t, f)

			replay := invoke(t, f, tc.sameRequest(f))
			tc.assertOutcome(t, f, first, replay)
			assertSnapshotEqual(t, afterFirst, tc.effectSnapshot(t, f))
		})

		if tc.externalProof != "" || tc.failClosed {
			continue
		}

		t.Run(tc.operation+"/"+string(tc.surface)+"/conflict-payload", func(t *testing.T) {
			f := tc.fixture(t)
			f.key = "matrix-key-" + tc.operation + "-" + strings.ReplaceAll(string(tc.surface), "/", "-")
			first := invoke(t, f, tc.firstRequest(f))
			if first.status != http.StatusOK && first.status != http.StatusCreated {
				t.Fatalf("first request status = %d, want 2xx; body %s", first.status, first.raw)
			}
			before := tc.effectSnapshot(t, f)
			conflict := invoke(t, f, tc.changedPayload(f))
			tc.assertConflict(t, conflict)
			assertSnapshotEqual(t, before, tc.effectSnapshot(t, f))
		})

		t.Run(tc.operation+"/"+string(tc.surface)+"/conflict-principal", func(t *testing.T) {
			f := tc.fixture(t)
			f.key = "matrix-key-" + tc.operation + "-" + strings.ReplaceAll(string(tc.surface), "/", "-")
			first := invoke(t, f, tc.firstRequest(f))
			if first.status != http.StatusOK && first.status != http.StatusCreated {
				t.Fatalf("first request status = %d, want 2xx; body %s", first.status, first.raw)
			}
			before := tc.effectSnapshot(t, f)
			conflict := invoke(t, f, tc.changedPrincipal(f))
			tc.assertConflict(t, conflict)
			assertSnapshotEqual(t, before, tc.effectSnapshot(t, f))
		})
	}
}

// TestIdempotencyReplayMatrixProofLinksResolve (FR-L.7): every named external
// proof (CLI/sync dedicated tests) must exist as a top-level test declaration in
// its package — a stale link fails the matrix.
func TestIdempotencyReplayMatrixProofLinksResolve(t *testing.T) {
	// The test runs with cwd = the package dir (internal/server); the proof
	// files live at the repo root, so resolve ../../ from the package dir.
	repoRoot := filepath.Join("..", "..")
	proofFiles := map[string]string{
		"TestCLIJudgeReplay":        filepath.Join(repoRoot, "cmd/drenyra-engram/judge_test.go"),
		"TestCLIReconcileReplay":    filepath.Join(repoRoot, "cmd/drenyra-engram/reconcile_test.go"),
		"TestCLIReviewRejectReplay": filepath.Join(repoRoot, "cmd/drenyra-engram/review_test.go"),
		"TestCLIPurgeRequestReplay": filepath.Join(repoRoot, "cmd/drenyra-engram/purge_test.go"),
		"TestSyncIdempotent":        filepath.Join(repoRoot, "internal/sync/sync_test.go"),
		"TestCLISyncRoundTrip":      filepath.Join(repoRoot, "cmd/drenyra-engram/main_test.go"),
	}
	declared := map[string]bool{}
	for _, file := range []string{
		filepath.Join(repoRoot, "cmd/drenyra-engram/judge_test.go"),
		filepath.Join(repoRoot, "cmd/drenyra-engram/reconcile_test.go"),
		filepath.Join(repoRoot, "cmd/drenyra-engram/review_test.go"),
		filepath.Join(repoRoot, "cmd/drenyra-engram/purge_test.go"),
		filepath.Join(repoRoot, "cmd/drenyra-engram/main_test.go"),
		filepath.Join(repoRoot, "internal/sync/sync_test.go"),
	} {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name != nil && strings.HasPrefix(fn.Name.Name, "Test") {
				declared[fn.Name.Name] = true
			}
		}
	}
	for proof := range proofFiles {
		if !declared[proof] {
			t.Fatalf("external proof %q (matrix sync/CLI row) does not resolve to any test declaration", proof)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Consolidated matrix per-operation helpers (PR L).
//
// One builder per (operation, surface) referenced by the table above. Fixtures
// are SHARED between the HTTP row and the MCP row of the same operation (the
// table wires both surfaces to one fixture constructor), so every fixture
// wires BOTH the httptest server and the session-less MCPServer over the SAME
// store: HTTP rows execute the real registered handlers, MCP rows execute the
// real tools, and both observe the same store through api.Doctor().
//
// Request builders derive all four variants from one canonical invocation:
//
//   - same:            the IDENTICAL request → the original stored outcome with
//     idempotentReplay=true (never a second domain event, receipt or mutation);
//   - changedPayload:  exactly ONE bound business field changes (reason, hash,
//     amount, category, kind, or the targeted entity id where the engine binds
//     only the entity id — e.g. withdraw commands hash {entityID} alone) →
//     the frozen IDEMPOTENCY_CONFLICT, never a silent success;
//   - changedPrincipal: only the acting principal changes: a DISTINCT subject
//     id seeded in the SAME tenant/company. Every engine reservation binds
//     principal.SubjectID() to the (tenant, requestId) key, so a different
//     subject is IDEMPOTENCY_CONFLICT — never a replay and never a
//     scope/role denial.
//
// The four provenance-carried MCP rows (judgment/reconciliation propose/withdraw)
// share the HTTP changedPrincipal builder with their HTTP sibling: the table
// wires `changedPrincipal: matrix*ChangedPrincipal` for BOTH surfaces. The MCP
// surface has no principal dimension (provenance source only), so the builder
// dispatches on the surface encoded in the matrix key (the test sets
// f.key = "matrix-key-<operation>-<surface>"). The MCP row proves the same
// frozen IDEMPOTENCY_CONFLICT in-band on the tool; the HTTP row proves it over
// HTTP. Both assert the identical typed conflict — nothing is weakened.
//
// The purge-execute row is the one row where the Idempotency-Key header IS the
// bound execution id: the engine requires a UUID execution id
// (INVALID_PURGE_EXECUTION_ID otherwise), so the execute builders use a FIXED
// UUID execution key instead of the generic f.key label (Request IDs are
// constants per subtest, per the design contract).

// matrixHTTPProposerSource is the canonical provenance source of the matrix
// fixtures: agent-1 over the "http" system — exactly agentProposalSource()'s
// value — so withdrawal provenance continuity holds on both surfaces (the MCP
// withdrawal passes the SAME source value in snake_case).
func matrixHTTPProposerSource() core.Source {
	return core.Source{System: "http", ActorID: "agent-1", ActorKind: core.ActorKindAgent, Session: "sess-1"}
}

// matrixMCPProposerSource is the snake_case MCP wire shape of
// matrixHTTPProposerSource (identical core.Source after the strict decode).
func matrixMCPProposerSource() map[string]any {
	return map[string]any{"system": "http", "actor_id": "agent-1", "actor_kind": "agent", "session": "sess-1"}
}

// matrixHTTPProposer2Source is the CHANGED provenance source (agent-2): the
// "principal" of the provenance-only propose/withdraw surfaces.
func matrixHTTPProposer2Source() map[string]any {
	return map[string]any{"system": "http", "actorId": "agent-2", "actorKind": "agent", "session": "sess-2"}
}

// matrixMCPProposer2Source is the snake_case MCP wire shape of the CHANGED
// provenance source.
func matrixMCPProposer2Source() map[string]any {
	return map[string]any{"system": "http", "actor_id": "agent-2", "actor_kind": "agent", "session": "sess-2"}
}

// matrixIsMCPSurface reports whether the matrix key belongs to an MCP row: the
// test sets f.key = "matrix-key-<operation>-<surface>", so the surface suffix
// disambiguates the rows that share a changedPrincipal builder.
func matrixIsMCPSurface(f replayFixture) bool {
	return strings.HasSuffix(f.key, "-MCP")
}

// matrixDecode decodes one transport response raw body into the operation's
// result type.
func matrixDecode[T any](t *testing.T, r replayResponse) T {
	t.Helper()
	var out T
	if err := json.Unmarshal([]byte(r.raw), &out); err != nil {
		t.Fatalf("decode %T from %q: %v", out, r.raw, err)
	}
	return out
}

// ── approval ────────────────────────────────────────────────────────────────

func matrixApproveFixture(t *testing.T) replayFixture {
	t.Helper()
	ts, api := newTestHTTPServer(t, "")
	m := NewMCPServer(api)
	controllerToken := seedPurgeIdentity(t, api, "cmp_org", "cmp_01", "20601234567", "maria.torres",
		[]auth.AccountingRole{auth.RoleController})
	secondToken := seedPurgeIdentity(t, api, "cmp_org", "cmp_01", "20601234567", "carlos.ruiz",
		[]auth.AccountingRole{auth.RoleController})
	mem := savePendingApproval(t, api, "approval/matrix")
	return replayFixture{
		ts: ts, api: api, m: m,
		controllerToken: controllerToken,
		secondToken:     secondToken,
		memoryID:        mem.Identity.ID,
		envelope:        core.ComputeEnvelopeHash(mem),
	}
}

func matrixApproveInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		method: http.MethodPost,
		url:    f.ts.URL + "/accounting/memories/" + f.memoryID + "/approve",
		token:  f.controllerToken,
		key:    f.key,
		body:   map[string]any{"expectedEnvelopeHash": f.envelope, "reason": "revisado y conforme (matrix)"},
	}
}

func matrixApproveChangedPayload(f replayFixture) replayInvocation {
	in := matrixApproveInvoke(f)
	in.body = map[string]any{"expectedEnvelopeHash": f.envelope, "reason": "CHANGED reason under the same request id"}
	return in
}

func matrixApproveChangedPrincipal(f replayFixture) replayInvocation {
	in := matrixApproveInvoke(f)
	in.token = f.secondToken
	return in
}

func matrixApproveAssert(t *testing.T, f replayFixture, first, replay replayResponse) {
	t.Helper()
	firstResult := matrixDecode[core.ApprovalResult](t, first)
	replayResult := matrixDecode[core.ApprovalResult](t, replay)
	if !replayResult.IdempotentReplay {
		t.Fatalf("approve replay must be an idempotent replay, got %+v", replayResult)
	}
	if replayResult.MemoryID != firstResult.MemoryID ||
		replayResult.ApprovalEventID != firstResult.ApprovalEventID ||
		replayResult.ResultingEnvelopeHash != firstResult.ResultingEnvelopeHash {
		t.Fatalf("approve replay outcome = %+v, want the stored outcome %+v", replayResult, firstResult)
	}
}

func matrixMCPApproveInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		mcpTool: "accounting_approve",
		mcpArgs: map[string]any{
			"memory_id": f.memoryID, "expected_envelope_hash": f.envelope,
			"reason": "revisado y conforme (matrix)", "request_id": f.key,
		},
	}
}

// ── judgment ────────────────────────────────────────────────────────────────

func matrixJudgmentProposeFixture(t *testing.T) replayFixture {
	t.Helper()
	ts, api := newTestHTTPServer(t, "")
	m := NewMCPServer(api)
	fromID, toID := seedJudgmentObservations(t, api)
	return replayFixture{ts: ts, api: api, m: m, fromID: fromID, toID: toID}
}

func matrixJudgmentProposeInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		method: http.MethodPost,
		url:    f.ts.URL + "/accounting/judgments",
		key:    f.key,
		body: map[string]any{
			"fromId": f.fromID, "toId": f.toID, "relation": "contradicts",
			"reason": "diferencia de saldo entre mayor y SIRE", "source": agentProposalSource(),
		},
	}
}

func matrixJudgmentProposeChangedPayload(f replayFixture) replayInvocation {
	in := matrixJudgmentProposeInvoke(f)
	in.body = map[string]any{
		"fromId": f.fromID, "toId": f.toID, "relation": "contradicts",
		"reason": "CHANGED reason under the same request id", "source": agentProposalSource(),
	}
	return in
}

func matrixJudgmentProposeChangedPrincipal(f replayFixture) replayInvocation {
	if matrixIsMCPSurface(f) {
		return replayInvocation{
			mcpTool: "accounting_judgment_propose",
			mcpArgs: map[string]any{
				"from_id": f.fromID, "to_id": f.toID, "relation": "contradicts",
				"reason": "diferencia de saldo entre mayor y SIRE", "request_id": f.key,
				"source": agentMCPSource("agent-2"),
			},
		}
	}
	in := matrixJudgmentProposeInvoke(f)
	in.body = map[string]any{
		"fromId": f.fromID, "toId": f.toID, "relation": "contradicts",
		"reason": "diferencia de saldo entre mayor y SIRE", "source": matrixHTTPProposer2Source(),
	}
	return in
}

func matrixJudgmentProposeAssert(t *testing.T, f replayFixture, first, replay replayResponse) {
	t.Helper()
	firstResult := matrixDecode[core.ProposeJudgmentResult](t, first)
	replayResult := matrixDecode[core.ProposeJudgmentResult](t, replay)
	if !replayResult.IdempotentReplay {
		t.Fatalf("judgment propose replay must be an idempotent replay, got %+v", replayResult)
	}
	if replayResult.JudgmentID != firstResult.JudgmentID || replayResult.Judgment.Status != core.JudgmentProposed {
		t.Fatalf("judgment propose replay outcome = %+v, want the stored outcome %+v", replayResult, firstResult)
	}
}

func matrixMCPJudgmentProposeInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		mcpTool: "accounting_judgment_propose",
		mcpArgs: map[string]any{
			"from_id": f.fromID, "to_id": f.toID, "relation": "contradicts",
			"reason": "diferencia de saldo entre mayor y SIRE", "request_id": f.key,
			"source": agentMCPSource("agent-1"),
		},
	}
}

func matrixMCPJudgmentProposeChangedPayload(f replayFixture) replayInvocation {
	in := matrixMCPJudgmentProposeInvoke(f)
	in.mcpArgs = map[string]any{
		"from_id": f.fromID, "to_id": f.toID, "relation": "contradicts",
		"reason": "CHANGED reason under the same request id", "request_id": f.key,
		"source": agentMCPSource("agent-1"),
	}
	return in
}

func matrixJudgmentConfirmFixture(t *testing.T) replayFixture {
	t.Helper()
	ts, api := newTestHTTPServer(t, "")
	m := NewMCPServer(api)
	controllerToken := seedPurgeIdentity(t, api, "cmp_org", "cmp_01", "20601234567", "maria.torres",
		[]auth.AccountingRole{auth.RoleController})
	secondToken := seedPurgeIdentity(t, api, "cmp_org", "cmp_01", "20601234567", "carlos.ruiz",
		[]auth.AccountingRole{auth.RoleController})
	fromID, toID := seedJudgmentObservations(t, api)
	proposed, err := ProposeJudgment(context.Background(), api.Store.(JudgmentStore), core.ProposeJudgmentCommand{
		FromID: fromID, ToID: toID, Relation: core.RelationContradicts,
		Reason: "diferencia de saldo entre mayor y SIRE", RequestID: "req-matrix-judgment-confirm-propose",
	}, matrixHTTPProposerSource())
	if err != nil {
		t.Fatalf("propose judgment confirm fixture: %v", err)
	}
	return replayFixture{
		ts: ts, api: api, m: m,
		controllerToken: controllerToken,
		secondToken:     secondToken,
		judgmentID:      proposed.JudgmentID,
		judgmentHash:    core.ComputeJudgmentHash(proposed.Judgment),
	}
}

func matrixJudgmentConfirmInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		method: http.MethodPost,
		url:    f.ts.URL + "/accounting/judgments/" + f.judgmentID + "/confirm",
		token:  f.controllerToken,
		key:    f.key,
		body:   map[string]any{"resolution": "El mayor prevalece tras la revision.", "expectedJudgmentHash": f.judgmentHash},
	}
}

func matrixJudgmentConfirmChangedPayload(f replayFixture) replayInvocation {
	in := matrixJudgmentConfirmInvoke(f)
	in.body = map[string]any{"resolution": "CHANGED resolution under the same request id", "expectedJudgmentHash": f.judgmentHash}
	return in
}

func matrixJudgmentConfirmChangedPrincipal(f replayFixture) replayInvocation {
	in := matrixJudgmentConfirmInvoke(f)
	in.token = f.secondToken
	return in
}

func matrixJudgmentConfirmAssert(t *testing.T, f replayFixture, first, replay replayResponse) {
	t.Helper()
	firstResult := matrixDecode[core.ConfirmJudgmentResult](t, first)
	replayResult := matrixDecode[core.ConfirmJudgmentResult](t, replay)
	if !replayResult.IdempotentReplay {
		t.Fatalf("judgment confirm replay must be an idempotent replay, got %+v", replayResult)
	}
	if replayResult.JudgmentID != firstResult.JudgmentID || replayResult.JudgmentEventID != firstResult.JudgmentEventID {
		t.Fatalf("judgment confirm replay outcome = %+v, want the stored outcome %+v", replayResult, firstResult)
	}
}

func matrixMCPJudgmentConfirmInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		mcpTool: "accounting_judgment_confirm",
		mcpArgs: map[string]any{
			"judgment_id": f.judgmentID, "resolution": "El mayor prevalece tras la revision.",
			"expected_judgment_hash": f.judgmentHash, "request_id": f.key,
		},
	}
}

func matrixJudgmentRejectFixture(t *testing.T) replayFixture {
	t.Helper()
	ts, api := newTestHTTPServer(t, "")
	m := NewMCPServer(api)
	controllerToken := seedPurgeIdentity(t, api, "cmp_org", "cmp_01", "20601234567", "maria.torres",
		[]auth.AccountingRole{auth.RoleController})
	secondToken := seedPurgeIdentity(t, api, "cmp_org", "cmp_01", "20601234567", "carlos.ruiz",
		[]auth.AccountingRole{auth.RoleController})
	fromID, toID := seedJudgmentObservations(t, api)
	proposed, err := ProposeJudgment(context.Background(), api.Store.(JudgmentStore), core.ProposeJudgmentCommand{
		FromID: fromID, ToID: toID, Relation: core.RelationContradicts,
		Reason: "diferencia de saldo entre mayor y SIRE", RequestID: "req-matrix-judgment-reject-propose",
	}, matrixHTTPProposerSource())
	if err != nil {
		t.Fatalf("propose judgment reject fixture: %v", err)
	}
	return replayFixture{
		ts: ts, api: api, m: m,
		controllerToken: controllerToken,
		secondToken:     secondToken,
		judgmentID:      proposed.JudgmentID,
		judgmentHash:    core.ComputeJudgmentHash(proposed.Judgment),
	}
}

func matrixJudgmentRejectInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		method: http.MethodPost,
		url:    f.ts.URL + "/accounting/judgments/" + f.judgmentID + "/reject",
		token:  f.controllerToken,
		key:    f.key,
		body:   map[string]any{"reason": "El XML no corresponde al CDR.", "expectedJudgmentHash": f.judgmentHash},
	}
}

func matrixJudgmentRejectChangedPayload(f replayFixture) replayInvocation {
	in := matrixJudgmentRejectInvoke(f)
	in.body = map[string]any{"reason": "CHANGED reason under the same request id", "expectedJudgmentHash": f.judgmentHash}
	return in
}

func matrixJudgmentRejectChangedPrincipal(f replayFixture) replayInvocation {
	in := matrixJudgmentRejectInvoke(f)
	in.token = f.secondToken
	return in
}

func matrixJudgmentRejectAssert(t *testing.T, f replayFixture, first, replay replayResponse) {
	t.Helper()
	firstResult := matrixDecode[core.RejectJudgmentResult](t, first)
	replayResult := matrixDecode[core.RejectJudgmentResult](t, replay)
	if !replayResult.IdempotentReplay {
		t.Fatalf("judgment reject replay must be an idempotent replay, got %+v", replayResult)
	}
	if replayResult.JudgmentID != firstResult.JudgmentID || replayResult.JudgmentEventID != firstResult.JudgmentEventID {
		t.Fatalf("judgment reject replay outcome = %+v, want the stored outcome %+v", replayResult, firstResult)
	}
}

func matrixJudgmentWithdrawFixture(t *testing.T) replayFixture {
	t.Helper()
	ts, api := newTestHTTPServer(t, "")
	m := NewMCPServer(api)
	fromID, toID := seedJudgmentObservations(t, api)
	proposedA, err := ProposeJudgment(context.Background(), api.Store.(JudgmentStore), core.ProposeJudgmentCommand{
		FromID: fromID, ToID: toID, Relation: core.RelationContradicts,
		Reason: "diferencia de saldo entre mayor y SIRE", RequestID: "req-matrix-judgment-withdraw-a",
	}, matrixHTTPProposerSource())
	if err != nil {
		t.Fatalf("propose judgment withdraw fixture A: %v", err)
	}
	// The engine allows ONE open proposal per observation pair, so the second
	// (changed-payload target) judgment needs its OWN pair of observations.
	from2 := saveDemoFact(t, api, "judgment/matrix/from-002", "Saldo de mayor 4011 (B)",
		"Saldo de mayor S/ 10,000.00", "2026-07-10T00:00:00Z")
	to2 := saveDemoFact(t, api, "judgment/matrix/to-002", "Saldo de SIRE 4011 (B)",
		"Saldo de SIRE S/ 11,284.30", "2026-07-10T00:00:00Z")
	proposedB, err := ProposeJudgment(context.Background(), api.Store.(JudgmentStore), core.ProposeJudgmentCommand{
		FromID: from2.Identity.ID, ToID: to2.Identity.ID, Relation: core.RelationContradicts,
		Reason: "segunda propuesta (target del conflicto de payload)", RequestID: "req-matrix-judgment-withdraw-b",
	}, matrixHTTPProposerSource())
	if err != nil {
		t.Fatalf("propose judgment withdraw fixture B: %v", err)
	}
	return replayFixture{
		ts: ts, api: api, m: m,
		// requestID holds the SECOND EXISTING judgment id: the withdraw command
		// hash binds ONLY the judgment id, so a changed payload must target a
		// different EXISTING judgment to be IDEMPOTENCY_CONFLICT (a missing
		// judgment would be JUDGMENT_NOT_FOUND before the reservation check).
		judgmentID: proposedA.JudgmentID,
		requestID:  proposedB.JudgmentID,
	}
}

func matrixJudgmentWithdrawInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		method: http.MethodPost,
		url:    f.ts.URL + "/accounting/judgments/" + f.judgmentID + "/withdraw",
		key:    f.key,
		body:   map[string]any{"source": agentProposalSource()},
	}
}

func matrixJudgmentWithdrawChangedPayload(f replayFixture) replayInvocation {
	in := matrixJudgmentWithdrawInvoke(f)
	in.url = f.ts.URL + "/accounting/judgments/" + f.requestID + "/withdraw"
	return in
}

func matrixJudgmentWithdrawChangedPrincipal(f replayFixture) replayInvocation {
	if matrixIsMCPSurface(f) {
		return replayInvocation{
			mcpTool: "accounting_judgment_withdraw",
			mcpArgs: map[string]any{
				"judgment_id": f.judgmentID, "request_id": f.key,
				"source": matrixMCPProposer2Source(),
			},
		}
	}
	in := matrixJudgmentWithdrawInvoke(f)
	in.body = map[string]any{"source": matrixHTTPProposer2Source()}
	return in
}

func matrixJudgmentWithdrawAssert(t *testing.T, f replayFixture, first, replay replayResponse) {
	t.Helper()
	firstResult := matrixDecode[core.WithdrawJudgmentResult](t, first)
	replayResult := matrixDecode[core.WithdrawJudgmentResult](t, replay)
	if !replayResult.IdempotentReplay {
		t.Fatalf("judgment withdraw replay must be an idempotent replay, got %+v", replayResult)
	}
	if replayResult.JudgmentID != firstResult.JudgmentID || replayResult.JudgmentEventID != firstResult.JudgmentEventID {
		t.Fatalf("judgment withdraw replay outcome = %+v, want the stored outcome %+v", replayResult, firstResult)
	}
}

func matrixMCPJudgmentWithdrawInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		mcpTool: "accounting_judgment_withdraw",
		mcpArgs: map[string]any{
			"judgment_id": f.judgmentID, "request_id": f.key,
			"source": matrixMCPProposerSource(),
		},
	}
}

func matrixMCPJudgmentWithdrawChangedPayload(f replayFixture) replayInvocation {
	in := matrixMCPJudgmentWithdrawInvoke(f)
	in.mcpArgs = map[string]any{
		"judgment_id": f.requestID, "request_id": f.key,
		"source": matrixMCPProposerSource(),
	}
	return in
}

// ── reconciliation ──────────────────────────────────────────────────────────

func matrixReconciliationProposeFixture(t *testing.T) replayFixture {
	t.Helper()
	ts, api := newTestHTTPServer(t, "")
	m := NewMCPServer(api)
	leftID, rightID := seedReconciliationObservations(t, api)
	return replayFixture{ts: ts, api: api, m: m, leftID: leftID, rightID: rightID}
}

func matrixReconciliationProposeInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		method: http.MethodPost,
		url:    f.ts.URL + "/accounting/reconciliations",
		key:    f.key,
		body: map[string]any{
			"leftMemoryId": f.leftID, "rightMemoryId": f.rightID,
			"method": "extracto_contable", "currency": "PEN",
			"leftAmountCents": 1000000, "rightAmountCents": 984000, "toleranceCents": 16000,
			"reason": "diferencia de saldo entre mayor y SIRE", "source": agentProposalSource(),
		},
	}
}

func matrixReconciliationProposeChangedPayload(f replayFixture) replayInvocation {
	in := matrixReconciliationProposeInvoke(f)
	in.body = map[string]any{
		"leftMemoryId": f.leftID, "rightMemoryId": f.rightID,
		"method": "extracto_contable", "currency": "PEN",
		"leftAmountCents": 1000000, "rightAmountCents": 984000, "toleranceCents": 16000,
		"reason": "CHANGED reason under the same request id", "source": agentProposalSource(),
	}
	return in
}

func matrixReconciliationProposeChangedPrincipal(f replayFixture) replayInvocation {
	if matrixIsMCPSurface(f) {
		return replayInvocation{
			mcpTool: "accounting_reconciliation_propose",
			mcpArgs: map[string]any{
				"left_memory_id": f.leftID, "right_memory_id": f.rightID,
				"method": "extracto_contable", "currency": "PEN",
				"left_amount_cents": 1000000, "right_amount_cents": 984000, "tolerance_cents": 16000,
				"reason": "diferencia de saldo entre mayor y SIRE", "request_id": f.key,
				"source": agentMCPSource("agent-2"),
			},
		}
	}
	in := matrixReconciliationProposeInvoke(f)
	in.body = map[string]any{
		"leftMemoryId": f.leftID, "rightMemoryId": f.rightID,
		"method": "extracto_contable", "currency": "PEN",
		"leftAmountCents": 1000000, "rightAmountCents": 984000, "toleranceCents": 16000,
		"reason": "diferencia de saldo entre mayor y SIRE", "source": matrixHTTPProposer2Source(),
	}
	return in
}

func matrixReconciliationProposeAssert(t *testing.T, f replayFixture, first, replay replayResponse) {
	t.Helper()
	firstResult := matrixDecode[core.ProposeReconciliationResult](t, first)
	replayResult := matrixDecode[core.ProposeReconciliationResult](t, replay)
	if !replayResult.IdempotentReplay {
		t.Fatalf("reconciliation propose replay must be an idempotent replay, got %+v", replayResult)
	}
	if replayResult.ReconciliationID != firstResult.ReconciliationID ||
		replayResult.Reconciliation.Status != core.ReconciliationProposed {
		t.Fatalf("reconciliation propose replay outcome = %+v, want the stored outcome %+v", replayResult, firstResult)
	}
}

func matrixMCPReconciliationProposeInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		mcpTool: "accounting_reconciliation_propose",
		mcpArgs: map[string]any{
			"left_memory_id": f.leftID, "right_memory_id": f.rightID,
			"method": "extracto_contable", "currency": "PEN",
			"left_amount_cents": 1000000, "right_amount_cents": 984000, "tolerance_cents": 16000,
			"reason": "diferencia de saldo entre mayor y SIRE", "request_id": f.key,
			"source": agentMCPSource("agent-1"),
		},
	}
}

func matrixMCPReconciliationProposeChangedPayload(f replayFixture) replayInvocation {
	in := matrixMCPReconciliationProposeInvoke(f)
	in.mcpArgs = map[string]any{
		"left_memory_id": f.leftID, "right_memory_id": f.rightID,
		"method": "extracto_contable", "currency": "PEN",
		"left_amount_cents": 1000000, "right_amount_cents": 984000, "tolerance_cents": 16000,
		"reason": "CHANGED reason under the same request id", "request_id": f.key,
		"source": agentMCPSource("agent-1"),
	}
	return in
}

func matrixReconciliationConfirmFixture(t *testing.T) replayFixture {
	t.Helper()
	ts, api := newTestHTTPServer(t, "")
	m := NewMCPServer(api)
	controllerToken := seedPurgeIdentity(t, api, "cmp_org", "cmp_01", "20601234567", "maria.torres",
		[]auth.AccountingRole{auth.RoleController})
	secondToken := seedPurgeIdentity(t, api, "cmp_org", "cmp_01", "20601234567", "carlos.ruiz",
		[]auth.AccountingRole{auth.RoleController})
	leftID, rightID := seedReconciliationObservations(t, api)
	proposed, err := ProposeReconciliation(context.Background(), api.Store.(ReconciliationStore), core.ProposeReconciliationCommand{
		LeftMemoryID: leftID, RightMemoryID: rightID, Method: "extracto_contable", Currency: "PEN",
		LeftAmountCents: 1000000, RightAmountCents: 984000, ToleranceCents: 16000,
		Reason: "diferencia de saldo entre mayor y SIRE", RequestID: "req-matrix-rec-confirm-propose",
	}, matrixHTTPProposerSource())
	if err != nil {
		t.Fatalf("propose reconciliation confirm fixture: %v", err)
	}
	return replayFixture{
		ts: ts, api: api, m: m,
		controllerToken:    controllerToken,
		secondToken:        secondToken,
		reconciliationID:   proposed.ReconciliationID,
		reconciliationHash: core.ComputeReconciliationHash(proposed.Reconciliation),
	}
}

func matrixReconciliationConfirmInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		method: http.MethodPost,
		url:    f.ts.URL + "/accounting/reconciliations/" + f.reconciliationID + "/confirm",
		token:  f.controllerToken,
		key:    f.key,
		body:   map[string]any{"resolution": "El mayor y el SIRE concilian tras el ajuste.", "expectedReconciliationHash": f.reconciliationHash},
	}
}

func matrixReconciliationConfirmChangedPayload(f replayFixture) replayInvocation {
	in := matrixReconciliationConfirmInvoke(f)
	in.body = map[string]any{"resolution": "CHANGED resolution under the same request id", "expectedReconciliationHash": f.reconciliationHash}
	return in
}

func matrixReconciliationConfirmChangedPrincipal(f replayFixture) replayInvocation {
	in := matrixReconciliationConfirmInvoke(f)
	in.token = f.secondToken
	return in
}

func matrixReconciliationConfirmAssert(t *testing.T, f replayFixture, first, replay replayResponse) {
	t.Helper()
	firstResult := matrixDecode[core.ConfirmReconciliationResult](t, first)
	replayResult := matrixDecode[core.ConfirmReconciliationResult](t, replay)
	if !replayResult.IdempotentReplay {
		t.Fatalf("reconciliation confirm replay must be an idempotent replay, got %+v", replayResult)
	}
	if replayResult.ReconciliationID != firstResult.ReconciliationID ||
		replayResult.ReconciliationEventID != firstResult.ReconciliationEventID {
		t.Fatalf("reconciliation confirm replay outcome = %+v, want the stored outcome %+v", replayResult, firstResult)
	}
}

func matrixMCPReconciliationConfirmInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		mcpTool: "accounting_reconciliation_confirm",
		mcpArgs: map[string]any{
			"reconciliation_id": f.reconciliationID, "resolution": "El mayor y el SIRE concilian tras el ajuste.",
			"expected_reconciliation_hash": f.reconciliationHash, "request_id": f.key,
		},
	}
}

func matrixReconciliationRejectFixture(t *testing.T) replayFixture {
	t.Helper()
	ts, api := newTestHTTPServer(t, "")
	m := NewMCPServer(api)
	controllerToken := seedPurgeIdentity(t, api, "cmp_org", "cmp_01", "20601234567", "maria.torres",
		[]auth.AccountingRole{auth.RoleController})
	secondToken := seedPurgeIdentity(t, api, "cmp_org", "cmp_01", "20601234567", "carlos.ruiz",
		[]auth.AccountingRole{auth.RoleController})
	leftID, rightID := seedReconciliationObservations(t, api)
	proposed, err := ProposeReconciliation(context.Background(), api.Store.(ReconciliationStore), core.ProposeReconciliationCommand{
		LeftMemoryID: leftID, RightMemoryID: rightID, Method: "extracto_contable", Currency: "PEN",
		LeftAmountCents: 1000000, RightAmountCents: 984000, ToleranceCents: 16000,
		Reason: "diferencia de saldo entre mayor y SIRE", RequestID: "req-matrix-rec-reject-propose",
	}, matrixHTTPProposerSource())
	if err != nil {
		t.Fatalf("propose reconciliation reject fixture: %v", err)
	}
	return replayFixture{
		ts: ts, api: api, m: m,
		controllerToken:    controllerToken,
		secondToken:        secondToken,
		reconciliationID:   proposed.ReconciliationID,
		reconciliationHash: core.ComputeReconciliationHash(proposed.Reconciliation),
	}
}

func matrixReconciliationRejectInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		method: http.MethodPost,
		url:    f.ts.URL + "/accounting/reconciliations/" + f.reconciliationID + "/reject",
		token:  f.controllerToken,
		key:    f.key,
		body:   map[string]any{"reason": "El extracto no respalda el saldo.", "expectedReconciliationHash": f.reconciliationHash},
	}
}

func matrixReconciliationRejectChangedPayload(f replayFixture) replayInvocation {
	in := matrixReconciliationRejectInvoke(f)
	in.body = map[string]any{"reason": "CHANGED reason under the same request id", "expectedReconciliationHash": f.reconciliationHash}
	return in
}

func matrixReconciliationRejectChangedPrincipal(f replayFixture) replayInvocation {
	in := matrixReconciliationRejectInvoke(f)
	in.token = f.secondToken
	return in
}

func matrixReconciliationRejectAssert(t *testing.T, f replayFixture, first, replay replayResponse) {
	t.Helper()
	firstResult := matrixDecode[core.RejectReconciliationResult](t, first)
	replayResult := matrixDecode[core.RejectReconciliationResult](t, replay)
	if !replayResult.IdempotentReplay {
		t.Fatalf("reconciliation reject replay must be an idempotent replay, got %+v", replayResult)
	}
	if replayResult.ReconciliationID != firstResult.ReconciliationID ||
		replayResult.ReconciliationEventID != firstResult.ReconciliationEventID {
		t.Fatalf("reconciliation reject replay outcome = %+v, want the stored outcome %+v", replayResult, firstResult)
	}
}

func matrixReconciliationWithdrawFixture(t *testing.T) replayFixture {
	t.Helper()
	ts, api := newTestHTTPServer(t, "")
	m := NewMCPServer(api)
	leftID, rightID := seedReconciliationObservations(t, api)
	proposedA, err := ProposeReconciliation(context.Background(), api.Store.(ReconciliationStore), core.ProposeReconciliationCommand{
		LeftMemoryID: leftID, RightMemoryID: rightID, Method: "extracto_contable", Currency: "PEN",
		LeftAmountCents: 1000000, RightAmountCents: 984000, ToleranceCents: 16000,
		Reason: "diferencia de saldo entre mayor y SIRE", RequestID: "req-matrix-rec-withdraw-a",
	}, matrixHTTPProposerSource())
	if err != nil {
		t.Fatalf("propose reconciliation withdraw fixture A: %v", err)
	}
	// The engine allows ONE open reconciliation per observation pair + method,
	// so the second (changed-payload target) needs its OWN pair of observations.
	left2 := saveDemoFact(t, api, "reconciliation/matrix/left-002", "Saldo de mayor 4011 (B)",
		"Saldo de mayor S/ 10,000.00", "2026-07-10T00:00:00Z")
	right2 := saveDemoFact(t, api, "reconciliation/matrix/right-002", "Saldo de SIRE 4011 (B)",
		"Saldo de SIRE S/ 10,000.00", "2026-07-10T00:00:00Z")
	proposedB, err := ProposeReconciliation(context.Background(), api.Store.(ReconciliationStore), core.ProposeReconciliationCommand{
		LeftMemoryID: left2.Identity.ID, RightMemoryID: right2.Identity.ID, Method: "extracto_contable", Currency: "PEN",
		LeftAmountCents: 1000000, RightAmountCents: 984000, ToleranceCents: 16000,
		Reason: "segunda propuesta (target del conflicto de payload)", RequestID: "req-matrix-rec-withdraw-b",
	}, matrixHTTPProposerSource())
	if err != nil {
		t.Fatalf("propose reconciliation withdraw fixture B: %v", err)
	}
	return replayFixture{
		ts: ts, api: api, m: m,
		// requestID holds the SECOND EXISTING reconciliation id: the withdraw
		// command hash binds ONLY the reconciliation id, so a changed payload
		// must target a different EXISTING reconciliation to be
		// IDEMPOTENCY_CONFLICT (a missing one would be
		// RECONCILIATION_NOT_FOUND before the reservation check).
		reconciliationID: proposedA.ReconciliationID,
		requestID:        proposedB.ReconciliationID,
	}
}

func matrixReconciliationWithdrawInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		method: http.MethodPost,
		url:    f.ts.URL + "/accounting/reconciliations/" + f.reconciliationID + "/withdraw",
		key:    f.key,
		body:   map[string]any{"source": agentProposalSource()},
	}
}

func matrixReconciliationWithdrawChangedPayload(f replayFixture) replayInvocation {
	in := matrixReconciliationWithdrawInvoke(f)
	in.url = f.ts.URL + "/accounting/reconciliations/" + f.requestID + "/withdraw"
	return in
}

func matrixReconciliationWithdrawChangedPrincipal(f replayFixture) replayInvocation {
	if matrixIsMCPSurface(f) {
		return replayInvocation{
			mcpTool: "accounting_reconciliation_withdraw",
			mcpArgs: map[string]any{
				"reconciliation_id": f.reconciliationID, "request_id": f.key,
				"source": matrixMCPProposer2Source(),
			},
		}
	}
	in := matrixReconciliationWithdrawInvoke(f)
	in.body = map[string]any{"source": matrixHTTPProposer2Source()}
	return in
}

func matrixReconciliationWithdrawAssert(t *testing.T, f replayFixture, first, replay replayResponse) {
	t.Helper()
	firstResult := matrixDecode[core.WithdrawReconciliationResult](t, first)
	replayResult := matrixDecode[core.WithdrawReconciliationResult](t, replay)
	if !replayResult.IdempotentReplay {
		t.Fatalf("reconciliation withdraw replay must be an idempotent replay, got %+v", replayResult)
	}
	if replayResult.ReconciliationID != firstResult.ReconciliationID ||
		replayResult.ReconciliationEventID != firstResult.ReconciliationEventID {
		t.Fatalf("reconciliation withdraw replay outcome = %+v, want the stored outcome %+v", replayResult, firstResult)
	}
}

func matrixMCPReconciliationWithdrawInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		mcpTool: "accounting_reconciliation_withdraw",
		mcpArgs: map[string]any{
			"reconciliation_id": f.reconciliationID, "request_id": f.key,
			"source": matrixMCPProposerSource(),
		},
	}
}

func matrixMCPReconciliationWithdrawChangedPayload(f replayFixture) replayInvocation {
	in := matrixMCPReconciliationWithdrawInvoke(f)
	in.mcpArgs = map[string]any{
		"reconciliation_id": f.requestID, "request_id": f.key,
		"source": matrixMCPProposerSource(),
	}
	return in
}

// ── review reject ───────────────────────────────────────────────────────────

func matrixReviewRejectFixture(t *testing.T) replayFixture {
	t.Helper()
	ts, api := newTestHTTPServer(t, "")
	m := NewMCPServer(api)
	controllerToken := seedPurgeIdentity(t, api, "cmp_org", "cmp_01", "20601234567", "maria.torres",
		[]auth.AccountingRole{auth.RoleController})
	secondToken := seedPurgeIdentity(t, api, "cmp_org", "cmp_01", "20601234567", "carlos.ruiz",
		[]auth.AccountingRole{auth.RoleController})
	mem := saveOne(t, api, reviewSaveInput("review.matrix.reject", "rechazable", demoScope()))
	if mem.Status != core.StatusPendingReview {
		t.Fatalf("review reject fixture status = %q, want pending_review", mem.Status)
	}
	return replayFixture{
		ts: ts, api: api, m: m,
		controllerToken: controllerToken,
		secondToken:     secondToken,
		memoryID:        mem.Identity.ID,
		envelope:        core.ComputeEnvelopeHash(mem),
	}
}

func matrixReviewRejectInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		method: http.MethodPost,
		url:    f.ts.URL + "/accounting/memories/" + f.memoryID + "/reject",
		token:  f.controllerToken,
		key:    f.key,
		body:   map[string]any{"expectedEnvelopeHash": f.envelope, "reason": "evidencia insuficiente (matrix)"},
	}
}

func matrixReviewRejectChangedPayload(f replayFixture) replayInvocation {
	in := matrixReviewRejectInvoke(f)
	in.body = map[string]any{"expectedEnvelopeHash": f.envelope, "reason": "CHANGED reason under the same request id"}
	return in
}

func matrixReviewRejectChangedPrincipal(f replayFixture) replayInvocation {
	in := matrixReviewRejectInvoke(f)
	in.token = f.secondToken
	return in
}

func matrixReviewRejectAssert(t *testing.T, f replayFixture, first, replay replayResponse) {
	t.Helper()
	firstResult := matrixDecode[core.RejectMemoryResult](t, first)
	replayResult := matrixDecode[core.RejectMemoryResult](t, replay)
	if !replayResult.IdempotentReplay {
		t.Fatalf("review reject replay must be an idempotent replay, got %+v", replayResult)
	}
	if replayResult.MemoryID != firstResult.MemoryID ||
		replayResult.DecisionEventID != firstResult.DecisionEventID ||
		replayResult.CurrentStatus != string(core.StatusRejected) {
		t.Fatalf("review reject replay outcome = %+v, want the stored outcome %+v", replayResult, firstResult)
	}
}

func matrixMCPReviewRejectInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		mcpTool: "accounting_review_reject",
		mcpArgs: map[string]any{
			"memory_id": f.memoryID, "expected_envelope_hash": f.envelope,
			"reason": "evidencia insuficiente (matrix)", "request_id": f.key,
		},
	}
}

// ── hold place / lift ───────────────────────────────────────────────────────

func matrixHoldPlaceFixture(t *testing.T) replayFixture {
	t.Helper()
	ts, api := newTestHTTPServer(t, "")
	m := NewMCPServer(api)
	recordsToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "lucia.ramirez",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	secondToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "marcos.torres",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	objectID, _, scope, _ := purgeFixture(t, api, recordsToken)
	return replayFixture{
		ts: ts, api: api, m: m,
		recordsToken: recordsToken,
		secondToken:  secondToken,
		objectID:     objectID,
		scope:        scope,
	}
}

func matrixHoldPlaceInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		method: http.MethodPost,
		url:    f.ts.URL + "/accounting/objects/" + f.objectID + "/holds",
		token:  f.recordsToken,
		key:    f.key,
		body:   map[string]any{"kind": "legal", "reason": "preservacion por litigio (matrix)", "ownerSubjectId": "lucia.ramirez"},
	}
}

func matrixHoldPlaceChangedPayload(f replayFixture) replayInvocation {
	in := matrixHoldPlaceInvoke(f)
	in.body = map[string]any{"kind": "legal", "reason": "CHANGED reason under the same request id", "ownerSubjectId": "lucia.ramirez"}
	return in
}

func matrixHoldPlaceChangedPrincipal(f replayFixture) replayInvocation {
	in := matrixHoldPlaceInvoke(f)
	in.token = f.secondToken
	return in
}

func matrixHoldPlaceAssert(t *testing.T, f replayFixture, first, replay replayResponse) {
	t.Helper()
	firstResult := matrixDecode[core.PlaceHoldResult](t, first)
	replayResult := matrixDecode[core.PlaceHoldResult](t, replay)
	if !replayResult.IdempotentReplay {
		t.Fatalf("hold place replay must be an idempotent replay, got %+v", replayResult)
	}
	if replayResult.Hold.HoldID != firstResult.Hold.HoldID {
		t.Fatalf("hold place replay outcome = %+v, want the stored outcome %+v", replayResult, firstResult)
	}
}

func matrixMCPHoldPlaceInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		mcpTool: "accounting_hold_place",
		mcpArgs: map[string]any{
			"object_id": f.objectID, "kind": "legal", "reason": "preservacion por litigio (matrix)",
			"owner_subject_id": "lucia.ramirez", "request_id": f.key,
		},
	}
}

func matrixHoldLiftFixture(t *testing.T) replayFixture {
	t.Helper()
	ts, api := newTestHTTPServer(t, "")
	m := NewMCPServer(api)
	recordsToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "lucia.ramirez",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	secondToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "marcos.torres",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	objectID, _, _, _ := purgeFixture(t, api, recordsToken)
	placed, err := api.PlaceHold(context.Background(), core.PlaceHoldCommand{
		ObjectID: objectID, Kind: core.HoldKindLegal, Reason: "preservacion por litigio (matrix)",
		OwnerSubjectID: "lucia.ramirez", RequestID: "req-matrix-hold-place",
	}, purgePrincipal(t, api, recordsToken))
	if err != nil {
		t.Fatalf("place hold lift fixture: %v", err)
	}
	return replayFixture{
		ts: ts, api: api, m: m,
		recordsToken: recordsToken,
		secondToken:  secondToken,
		holdID:       placed.Hold.HoldID,
	}
}

func matrixHoldLiftInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		method: http.MethodPost,
		url:    f.ts.URL + "/accounting/holds/" + f.holdID + "/lift",
		token:  f.recordsToken,
		key:    f.key,
		body:   map[string]any{"reason": "litigio resuelto (matrix)"},
	}
}

func matrixHoldLiftChangedPayload(f replayFixture) replayInvocation {
	in := matrixHoldLiftInvoke(f)
	in.body = map[string]any{"reason": "CHANGED reason under the same request id"}
	return in
}

func matrixHoldLiftChangedPrincipal(f replayFixture) replayInvocation {
	in := matrixHoldLiftInvoke(f)
	in.token = f.secondToken
	return in
}

func matrixHoldLiftAssert(t *testing.T, f replayFixture, first, replay replayResponse) {
	t.Helper()
	firstResult := matrixDecode[core.LiftHoldResult](t, first)
	replayResult := matrixDecode[core.LiftHoldResult](t, replay)
	if !replayResult.IdempotentReplay {
		t.Fatalf("hold lift replay must be an idempotent replay, got %+v", replayResult)
	}
	if replayResult.Hold.HoldID != firstResult.Hold.HoldID || replayResult.Hold.LiftedAt != firstResult.Hold.LiftedAt {
		t.Fatalf("hold lift replay outcome = %+v, want the stored outcome %+v", replayResult, firstResult)
	}
}

func matrixMCPHoldLiftInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		mcpTool: "accounting_hold_lift",
		mcpArgs: map[string]any{
			"hold_id": f.holdID, "reason": "litigio resuelto (matrix)", "request_id": f.key,
		},
	}
}

// ── retention-policy put ────────────────────────────────────────────────────

func matrixRetentionPutFixture(t *testing.T) replayFixture {
	t.Helper()
	ts, api := newTestHTTPServer(t, "")
	m := NewMCPServer(api)
	recordsToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "lucia.ramirez",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	secondToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "marcos.torres",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	return replayFixture{
		ts: ts, api: api, m: m,
		recordsToken: recordsToken,
		secondToken:  secondToken,
		scope:        testScope(testRucA),
	}
}

func matrixRetentionPutInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		method: http.MethodPost,
		url:    f.ts.URL + "/accounting/retention-policies",
		token:  f.recordsToken,
		key:    f.key,
		body: map[string]any{
			"scope": f.scope, "jurisdiction": "PE", "legislation": "NATIONAL-TAX",
			"authority": "tenant-records", "source": "deployment decision 2026-08-07",
			"category": "invoice", "minPeriod": "202401", "expectedVersion": int64(0), "enabled": true,
		},
	}
}

func matrixRetentionPutChangedPayload(f replayFixture) replayInvocation {
	in := matrixRetentionPutInvoke(f)
	in.body = map[string]any{
		"scope": f.scope, "jurisdiction": "PE", "legislation": "NATIONAL-TAX",
		"authority": "tenant-records", "source": "deployment decision 2026-08-07",
		"category": "receipt", "minPeriod": "202401", "expectedVersion": int64(0), "enabled": true,
	}
	return in
}

func matrixRetentionPutChangedPrincipal(f replayFixture) replayInvocation {
	in := matrixRetentionPutInvoke(f)
	in.token = f.secondToken
	return in
}

func matrixRetentionPutAssert(t *testing.T, f replayFixture, first, replay replayResponse) {
	t.Helper()
	firstResult := matrixDecode[core.PutRetentionPolicyResult](t, first)
	replayResult := matrixDecode[core.PutRetentionPolicyResult](t, replay)
	if !replayResult.IdempotentReplay {
		t.Fatalf("retention put replay must be an idempotent replay, got %+v", replayResult)
	}
	if replayResult.Policy.PolicyID != firstResult.Policy.PolicyID {
		t.Fatalf("retention put replay outcome = %+v, want the stored outcome %+v", replayResult, firstResult)
	}
}

func matrixMCPRetentionPutInvoke(f replayFixture) replayInvocation {
	scopeJSON, _ := json.Marshal(f.scope)
	return replayInvocation{
		mcpTool: "accounting_retention_policy_put",
		mcpArgs: map[string]any{
			"scope": string(scopeJSON), "jurisdiction": "PE", "legislation": "NATIONAL-TAX",
			"authority": "tenant-records", "source": "deployment decision 2026-08-07",
			"category": "invoice", "min_period": "202401", "expected_version": int64(0),
			"dual_approval_required": false, "dual_approver_roles": []string{},
			"blocking_hold_kinds": []string{}, "enabled": true, "request_id": f.key,
		},
	}
}

// ── period reopen ───────────────────────────────────────────────────────────

// matrixReopenScope is the exact company scope of the reopen fixture: the HTTP
// reopen route derives CompanyID := RUC from the query parameters, so the
// closed period is seeded under CompanyID == RUC (the closure projection lookup
// compares tenant + company + period).
func matrixReopenScope() core.Scope {
	return core.Scope{
		Kind:           core.ScopeKindCompany,
		OrganizationID: "cmp_org",
		CompanyID:      "20601234567",
		RUC:            "20601234567",
		Period:         "202607",
	}
}

// matrixReopenClose creates ONE pending_review close memory at the EXACT matrix
// scope (createJulyClose is hardcoded to the demo scope and is not reusable).
func matrixReopenClose(t *testing.T, api *API, scope core.Scope, sourceFact core.AccountingMemory) core.AccountingMemory {
	t.Helper()
	memory, err := CreateClose(context.Background(), api, scope, core.CreateCloseInput{
		Period: scope.Period,
		Totals: []core.CloseTotal{{
			Code: "ventas", Currency: "PEN", AmountCents: 450000000,
			SourceMemoryIDs: []string{sourceFact.Identity.ID},
		}},
		Reason: "cierre de julio (matrix)",
		Source: testAgentSource,
	})
	if err != nil {
		t.Fatalf("create matrix close: %v", err)
	}
	return memory
}

func matrixReopenFixture(t *testing.T) replayFixture {
	t.Helper()
	ts, api := newTestHTTPServer(t, "")
	m := NewMCPServer(api)
	controllerToken := seedApprovalIdentity(t, api, "cmp_org", "20601234567", "20601234567",
		[]auth.AccountingRole{auth.RoleController})
	secondToken := seedPurgeIdentity(t, api, "cmp_org", "20601234567", "20601234567", "carlos.ruiz",
		[]auth.AccountingRole{auth.RoleController})
	scope := matrixReopenScope()
	sourceFact := saveOne(t, api, validInput("matrix/reopen/ventas", "Ventas de julio", "ventas del periodo", scope))
	closeMemory := matrixReopenClose(t, api, scope, sourceFact)
	principal := resolvePrincipal(t, api, controllerToken)
	h1 := core.ComputeEnvelopeHash(closeMemory)
	if _, err := ApproveMemory(context.Background(), api.Store.(ApprovalStore), authz.NewApprovalPolicy(), core.ApproveMemoryCommand{
		MemoryID:             closeMemory.Identity.ID,
		ExpectedEnvelopeHash: h1,
		Reason:               "cierre revisado y conforme (matrix)",
		RequestID:            "req-matrix-reopen-close",
	}, principal); err != nil {
		t.Fatalf("controller approval of the matrix close: %v", err)
	}
	closure, ok := api.FindPeriodClosure(scope)
	if !ok || closure.Status != "closed" {
		t.Fatalf("matrix period closure = %+v (ok=%v), want closed", closure, ok)
	}
	return replayFixture{
		ts: ts, api: api, m: m,
		controllerToken: controllerToken,
		secondToken:     secondToken,
		scope:           scope,
		closeID:         closeMemory.Identity.ID,
	}
}

func matrixReopenInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		method: http.MethodPost,
		url:    f.ts.URL + "/accounting/periods/" + f.scope.Period + "/reopen?ruc=" + f.scope.RUC + "&organizationId=" + f.scope.OrganizationID,
		token:  f.controllerToken,
		key:    f.key,
		body:   map[string]any{"expectedCloseMemoryId": f.closeID, "reason": "correccion de julio (matrix)"},
	}
}

func matrixReopenChangedPayload(f replayFixture) replayInvocation {
	in := matrixReopenInvoke(f)
	in.body = map[string]any{"expectedCloseMemoryId": f.closeID, "reason": "CHANGED reason under the same request id"}
	return in
}

func matrixReopenChangedPrincipal(f replayFixture) replayInvocation {
	in := matrixReopenInvoke(f)
	in.token = f.secondToken
	return in
}

func matrixReopenAssert(t *testing.T, f replayFixture, first, replay replayResponse) {
	t.Helper()
	firstResult := matrixDecode[core.ReopenPeriodResult](t, first)
	replayResult := matrixDecode[core.ReopenPeriodResult](t, replay)
	if !replayResult.IdempotentReplay {
		t.Fatalf("reopen replay must be an idempotent replay, got %+v", replayResult)
	}
	if replayResult.EventID != firstResult.EventID || replayResult.CloseMemoryID != firstResult.CloseMemoryID {
		t.Fatalf("reopen replay outcome = %+v, want the stored outcome %+v", replayResult, firstResult)
	}
}

func matrixMCPReopenInvoke(f replayFixture) replayInvocation {
	scopeJSON, _ := json.Marshal(f.scope)
	return replayInvocation{
		mcpTool: "accounting_period_reopen",
		mcpArgs: map[string]any{
			"period": f.scope.Period, "scope": string(scopeJSON),
			"expected_close_memory_id": f.closeID, "reason": "correccion de julio (matrix)",
			"request_id": f.key,
		},
	}
}

// ── purge lifecycle ─────────────────────────────────────────────────────────

func matrixPurgeRequestFixture(t *testing.T) replayFixture {
	t.Helper()
	ts, api := newTestHTTPServer(t, "")
	m := NewMCPServer(api)
	recordsToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "lucia.ramirez",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	accountantToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "ana.garcia",
		[]auth.AccountingRole{auth.RoleAccountant})
	secondToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "jose.mendez",
		[]auth.AccountingRole{auth.RoleAccountant})
	objectID, _, _, h := purgeFixture(t, api, recordsToken)
	return replayFixture{
		ts: ts, api: api, m: m,
		recordsToken:    recordsToken,
		accountantToken: accountantToken,
		secondToken:     secondToken,
		objectID:        objectID,
		h:               h,
	}
}

func matrixPurgeRequestInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		method: http.MethodPost,
		url:    f.ts.URL + "/accounting/objects/" + f.objectID + "/purge",
		token:  f.accountantToken,
		key:    f.key,
		body: map[string]any{
			"jurisdiction": "PE", "legislation": "NATIONAL-TAX", "category": "invoice",
			"expectedLifecycleHash": f.h, "reason": "retention period elapsed (matrix)",
		},
	}
}

func matrixPurgeRequestChangedPayload(f replayFixture) replayInvocation {
	in := matrixPurgeRequestInvoke(f)
	in.body = map[string]any{
		"jurisdiction": "PE", "legislation": "NATIONAL-TAX", "category": "invoice",
		"expectedLifecycleHash": f.h, "reason": "CHANGED reason under the same request id",
	}
	return in
}

func matrixPurgeRequestChangedPrincipal(f replayFixture) replayInvocation {
	in := matrixPurgeRequestInvoke(f)
	in.token = f.secondToken
	return in
}

func matrixPurgeRequestAssert(t *testing.T, f replayFixture, first, replay replayResponse) {
	t.Helper()
	firstResult := matrixDecode[core.RequestPurgeResult](t, first)
	replayResult := matrixDecode[core.RequestPurgeResult](t, replay)
	if !replayResult.IdempotentReplay {
		t.Fatalf("purge request replay must be an idempotent replay, got %+v", replayResult)
	}
	if replayResult.Request.RequestID != firstResult.Request.RequestID {
		t.Fatalf("purge request replay outcome = %+v, want the stored outcome %+v", replayResult, firstResult)
	}
}

func matrixMCPPurgeRequestInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		mcpTool: "accounting_purge_request",
		mcpArgs: map[string]any{
			"object_id": f.objectID, "jurisdiction": "PE", "legislation": "NATIONAL-TAX",
			"category": "invoice", "expected_lifecycle_hash": f.h,
			"reason": "retention period elapsed (matrix)", "request_id": f.key,
		},
	}
}

func matrixPurgeApproveFixture(t *testing.T) replayFixture {
	t.Helper()
	ts, api := newTestHTTPServer(t, "")
	m := NewMCPServer(api)
	recordsToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "lucia.ramirez",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	accountantToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "ana.garcia",
		[]auth.AccountingRole{auth.RoleAccountant})
	secondToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "marcos.torres",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	objectID, policy, scope, h := purgeFixture(t, api, recordsToken)
	request := requestPurgeDirect(t, api, objectID, h, "req-purge-matrix-approve-request", accountantToken)
	hRequested := purgeSnapshotHash(t, scope, objectID, core.PurgeLifecycleRequested,
		core.RetentionEligibilityEligible, policy.PolicyID, policy.Category, policy.Version,
		request.Request.RequestID, nil)
	return replayFixture{
		ts: ts, api: api, m: m,
		recordsToken:    recordsToken,
		accountantToken: accountantToken,
		secondToken:     secondToken,
		requestID:       request.Request.RequestID,
		hRequested:      hRequested,
	}
}

func matrixPurgeApproveInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		method: http.MethodPost,
		url:    f.ts.URL + "/accounting/purge-requests/" + f.requestID + "/approve",
		token:  f.recordsToken,
		key:    f.key,
		body:   map[string]any{"expectedLifecycleHash": f.hRequested, "reason": "verified against the reviewed snapshot"},
	}
}

func matrixPurgeApproveChangedPayload(f replayFixture) replayInvocation {
	in := matrixPurgeApproveInvoke(f)
	in.body = map[string]any{"expectedLifecycleHash": f.hRequested, "reason": "CHANGED reason under the same request id"}
	return in
}

func matrixPurgeApproveChangedPrincipal(f replayFixture) replayInvocation {
	in := matrixPurgeApproveInvoke(f)
	in.token = f.secondToken
	return in
}

func matrixPurgeApproveAssert(t *testing.T, f replayFixture, first, replay replayResponse) {
	t.Helper()
	firstResult := matrixDecode[core.ApprovePurgeResult](t, first)
	replayResult := matrixDecode[core.ApprovePurgeResult](t, replay)
	if !replayResult.IdempotentReplay {
		t.Fatalf("purge approve replay must be an idempotent replay, got %+v", replayResult)
	}
	if replayResult.Approval.ApprovalID != firstResult.Approval.ApprovalID {
		t.Fatalf("purge approve replay outcome = %+v, want the stored outcome %+v", replayResult, firstResult)
	}
}

func matrixMCPPurgeApproveInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		mcpTool: "accounting_purge_approve",
		mcpArgs: map[string]any{
			"request_id": f.requestID, "expected_lifecycle_hash": f.hRequested,
			"reason": "verified against the reviewed snapshot", "request_id_key": f.key,
		},
	}
}

func matrixPurgeRejectFixture(t *testing.T) replayFixture {
	t.Helper()
	ts, api := newTestHTTPServer(t, "")
	m := NewMCPServer(api)
	recordsToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "lucia.ramirez",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	accountantToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "ana.garcia",
		[]auth.AccountingRole{auth.RoleAccountant})
	secondToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "marcos.torres",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	objectID, _, _, h := purgeFixture(t, api, recordsToken)
	request := requestPurgeDirect(t, api, objectID, h, "req-purge-matrix-reject-request", accountantToken)
	return replayFixture{
		ts: ts, api: api, m: m,
		recordsToken:    recordsToken,
		accountantToken: accountantToken,
		secondToken:     secondToken,
		requestID:       request.Request.RequestID,
	}
}

func matrixPurgeRejectInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		method: http.MethodPost,
		url:    f.ts.URL + "/accounting/purge-requests/" + f.requestID + "/reject",
		token:  f.recordsToken,
		key:    f.key,
		body:   map[string]any{"reason": "evidence still required"},
	}
}

func matrixPurgeRejectChangedPayload(f replayFixture) replayInvocation {
	in := matrixPurgeRejectInvoke(f)
	in.body = map[string]any{"reason": "CHANGED reason under the same request id"}
	return in
}

func matrixPurgeRejectChangedPrincipal(f replayFixture) replayInvocation {
	in := matrixPurgeRejectInvoke(f)
	in.token = f.secondToken
	return in
}

func matrixPurgeRejectAssert(t *testing.T, f replayFixture, first, replay replayResponse) {
	t.Helper()
	firstResult := matrixDecode[core.RejectPurgeResult](t, first)
	replayResult := matrixDecode[core.RejectPurgeResult](t, replay)
	if !replayResult.IdempotentReplay {
		t.Fatalf("purge reject replay must be an idempotent replay, got %+v", replayResult)
	}
	if replayResult.Approval.ApprovalID != firstResult.Approval.ApprovalID {
		t.Fatalf("purge reject replay outcome = %+v, want the stored outcome %+v", replayResult, firstResult)
	}
}

func matrixPurgeCancelFixture(t *testing.T) replayFixture {
	t.Helper()
	ts, api := newTestHTTPServer(t, "")
	m := NewMCPServer(api)
	recordsToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "lucia.ramirez",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	accountantToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "ana.garcia",
		[]auth.AccountingRole{auth.RoleAccountant})
	secondToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "jose.mendez",
		[]auth.AccountingRole{auth.RoleAccountant})
	objectA, _, scope, hA := purgeFixture(t, api, recordsToken)
	// The purge pipeline is ONE row per object, so the changed-payload target
	// needs a SECOND object with its own requested pipeline (a different request
	// id on the SAME object is impossible while the first is open).
	objB, err := api.StoreObject(context.Background(), core.ObjectStoreInput{
		Bytes: []byte("purge-matrix-cancel-target-b"), ContentType: "application/xml",
		Scope: scope, Source: core.Source{System: "go-test", ActorID: "test-agent", ActorKind: core.ActorKindAgent},
	})
	if err != nil {
		t.Fatalf("store purge cancel target B: %v", err)
	}
	hB := purgeSnapshotHash(t, scope, objB.Object.ObjectID, core.PurgeLifecycleStored,
		core.RetentionEligibility("unmanaged"), "", "", 0, "", nil)
	reqA := requestPurgeDirect(t, api, objectA, hA, "req-purge-matrix-cancel-a", accountantToken)
	reqB := requestPurgeDirect(t, api, objB.Object.ObjectID, hB, "req-purge-matrix-cancel-b", accountantToken)
	return replayFixture{
		ts: ts, api: api, m: m,
		recordsToken:    recordsToken,
		accountantToken: accountantToken,
		secondToken:     secondToken,
		// requestID = the primary pipeline; approvalID = the SECOND EXISTING
		// pipeline the changed-payload cancel targets (the cancel command hash
		// binds ONLY the request id, so the conflict target must exist).
		requestID:  reqA.Request.RequestID,
		approvalID: reqB.Request.RequestID,
	}
}

func matrixPurgeCancelInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		method: http.MethodPost,
		url:    f.ts.URL + "/accounting/purge-requests/" + f.requestID + "/cancel",
		token:  f.accountantToken,
		key:    f.key,
	}
}

func matrixPurgeCancelChangedPayload(f replayFixture) replayInvocation {
	in := matrixPurgeCancelInvoke(f)
	in.url = f.ts.URL + "/accounting/purge-requests/" + f.approvalID + "/cancel"
	return in
}

func matrixPurgeCancelChangedPrincipal(f replayFixture) replayInvocation {
	in := matrixPurgeCancelInvoke(f)
	in.token = f.secondToken
	return in
}

func matrixPurgeCancelAssert(t *testing.T, f replayFixture, first, replay replayResponse) {
	t.Helper()
	firstResult := matrixDecode[core.CancelPurgeResult](t, first)
	replayResult := matrixDecode[core.CancelPurgeResult](t, replay)
	if !replayResult.IdempotentReplay {
		t.Fatalf("purge cancel replay must be an idempotent replay, got %+v", replayResult)
	}
	if replayResult.Request.RequestID != firstResult.Request.RequestID {
		t.Fatalf("purge cancel replay outcome = %+v, want the stored outcome %+v", replayResult, firstResult)
	}
}

func matrixPurgeWithdrawFixture(t *testing.T) replayFixture {
	t.Helper()
	ts, api := newTestHTTPServer(t, "")
	m := NewMCPServer(api)
	recordsToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "lucia.ramirez",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	accountantToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "ana.garcia",
		[]auth.AccountingRole{auth.RoleAccountant})
	secondToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "marcos.torres",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	objectID, policy, scope, h := purgeFixture(t, api, recordsToken)
	request := requestPurgeDirect(t, api, objectID, h, "req-purge-matrix-withdraw-request", accountantToken)
	hRequested := purgeSnapshotHash(t, scope, objectID, core.PurgeLifecycleRequested,
		core.RetentionEligibilityEligible, policy.PolicyID, policy.Category, policy.Version,
		request.Request.RequestID, nil)
	approved, err := api.ApprovePurge(context.Background(), core.ApprovePurgeCommand{
		RequestID:             request.Request.RequestID,
		ExpectedLifecycleHash: hRequested,
		Reason:                "verified against the reviewed snapshot",
		RequestIDKey:          "req-purge-matrix-withdraw-approve",
	}, purgePrincipal(t, api, recordsToken))
	if err != nil {
		t.Fatalf("approve purge withdraw fixture: %v", err)
	}
	return replayFixture{
		ts: ts, api: api, m: m,
		recordsToken:    recordsToken,
		accountantToken: accountantToken,
		secondToken:     secondToken,
		requestID:       request.Request.RequestID,
		approvalID:      approved.Approval.ApprovalID,
	}
}

func matrixPurgeWithdrawInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		method: http.MethodPost,
		url:    f.ts.URL + "/accounting/purge-requests/" + f.requestID + "/withdraw",
		token:  f.recordsToken,
		key:    f.key,
		body:   map[string]any{"reason": "cleanup before execution"},
	}
}

func matrixPurgeWithdrawChangedPayload(f replayFixture) replayInvocation {
	in := matrixPurgeWithdrawInvoke(f)
	in.body = map[string]any{"reason": "CHANGED reason under the same request id"}
	return in
}

func matrixPurgeWithdrawChangedPrincipal(f replayFixture) replayInvocation {
	in := matrixPurgeWithdrawInvoke(f)
	in.token = f.secondToken
	return in
}

func matrixPurgeWithdrawAssert(t *testing.T, f replayFixture, first, replay replayResponse) {
	t.Helper()
	firstResult := matrixDecode[core.WithdrawPurgeResult](t, first)
	replayResult := matrixDecode[core.WithdrawPurgeResult](t, replay)
	if !replayResult.IdempotentReplay {
		t.Fatalf("purge withdraw replay must be an idempotent replay, got %+v", replayResult)
	}
	if replayResult.Approval.ApprovalID != firstResult.Approval.ApprovalID ||
		replayResult.Request.RequestID != firstResult.Request.RequestID {
		t.Fatalf("purge withdraw replay outcome = %+v, want the stored outcome %+v", replayResult, firstResult)
	}
}

// matrixPurgeExecutionKey is the FIXED UUID execution id of the purge-execute
// row: the Idempotency-Key header IS the bound execution id and the engine
// requires a UUID (INVALID_PURGE_EXECUTION_ID otherwise), so the execute
// builders use this constant instead of the generic f.key label.
const matrixPurgeExecutionKey = "00000000-0000-4000-8000-00000000e001"

func matrixPurgeExecuteFixture(t *testing.T) replayFixture {
	t.Helper()
	ts, api := newTestHTTPServer(t, "")
	m := NewMCPServer(api)
	recordsToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "lucia.ramirez",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	accountantToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "ana.garcia",
		[]auth.AccountingRole{auth.RoleAccountant})
	secondToken := seedPurgeIdentity(t, api, testOrgID, "acme", testRucA, "marcos.torres",
		[]auth.AccountingRole{auth.RoleRecordsComplianceOfficer})
	objectID, policy, scope, h := purgeFixture(t, api, recordsToken)
	request := requestPurgeDirect(t, api, objectID, h, "req-purge-matrix-execute-request", accountantToken)
	hRequested := purgeSnapshotHash(t, scope, objectID, core.PurgeLifecycleRequested,
		core.RetentionEligibilityEligible, policy.PolicyID, policy.Category, policy.Version,
		request.Request.RequestID, nil)
	approved, err := api.ApprovePurge(context.Background(), core.ApprovePurgeCommand{
		RequestID:             request.Request.RequestID,
		ExpectedLifecycleHash: hRequested,
		Reason:                "verified against the reviewed snapshot",
		RequestIDKey:          "req-purge-matrix-execute-approve",
	}, purgePrincipal(t, api, recordsToken))
	if err != nil {
		t.Fatalf("approve purge execute fixture: %v", err)
	}
	hApproved := purgeSnapshotHash(t, scope, objectID, core.PurgeLifecycleApproved,
		core.RetentionEligibilityEligible, policy.PolicyID, policy.Category, policy.Version,
		request.Request.RequestID, []string{approved.Approval.ApprovalID})
	return replayFixture{
		ts: ts, api: api, m: m,
		recordsToken:    recordsToken,
		accountantToken: accountantToken,
		secondToken:     secondToken,
		requestID:       request.Request.RequestID,
		approvalID:      approved.Approval.ApprovalID,
		hApproved:       hApproved,
	}
}

func matrixPurgeExecuteInvoke(f replayFixture) replayInvocation {
	return replayInvocation{
		method: http.MethodPost,
		url:    f.ts.URL + "/accounting/purge-requests/" + f.requestID + "/execute",
		token:  f.recordsToken,
		key:    matrixPurgeExecutionKey,
		body:   map[string]any{"expectedLifecycleHash": f.hApproved, "reason": "execution batch approved (matrix)"},
	}
}

func matrixPurgeExecuteChangedPayload(f replayFixture) replayInvocation {
	in := matrixPurgeExecuteInvoke(f)
	in.body = map[string]any{"expectedLifecycleHash": f.hApproved, "reason": "CHANGED reason under the same execution id"}
	return in
}

func matrixPurgeExecuteChangedPrincipal(f replayFixture) replayInvocation {
	in := matrixPurgeExecuteInvoke(f)
	in.token = f.secondToken
	return in
}

func matrixPurgeExecuteAssert(t *testing.T, f replayFixture, first, replay replayResponse) {
	t.Helper()
	firstResult := matrixDecode[core.ExecutePurgeResult](t, first)
	replayResult := matrixDecode[core.ExecutePurgeResult](t, replay)
	if !replayResult.IdempotentReplay {
		t.Fatalf("purge execute replay must be an idempotent replay, got %+v", replayResult)
	}
	if replayResult.Execution.ExecutionID != firstResult.Execution.ExecutionID ||
		replayResult.Request.RequestID != firstResult.Request.RequestID {
		t.Fatalf("purge execute replay outcome = %+v, want the stored outcome %+v", replayResult, firstResult)
	}
}
