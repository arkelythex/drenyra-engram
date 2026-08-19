// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module freezes the v0.9.0 REVIEW
// WORKSPACE MCP surface (docs/architecture/review-workspace-v0.9.md §7):
// accounting_review_queue / accounting_review_detail are SCOPE-FIRST READS whose
// exact scope tuple is part of the arguments (a caller whose scope differs sees
// NOTHING — never the memory); accounting_review_reject / accounting_review_return
// are the AUTHENTICATED decision tools (strict shape, NO identity field) and FAIL
// CLOSED with AUTHENTICATION_REQUIRED on this session-less stdio server — the
// same ADR-003 contract as accounting_approve.
package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// reviewSaveInput builds a pending_review save (fiscalEffect != none → the
// approval gate) recorded by an AGENT — the proposer a reviewer must differ from.
func reviewSaveInput(topicKey, what string, scope core.Scope) core.SaveInput {
	return core.SaveInput{
		TopicKey:     topicKey,
		Title:        "Pendiente de revision",
		Kind:         core.KindDecision,
		Scope:        scope,
		Content:      core.Content{What: what, Why: "fixture", Where: "internal/server", Learned: "n/a"},
		FiscalEffect: core.FiscalEffectAdjustment,
		EffectiveAt:  "2026-07-31T12:00:00Z",
		Source:       testAgentSource,
		Confidence:   0.8,
	}
}

func TestMCPReviewToolsRegistered(t *testing.T) {
	m, _ := newTestMCP(t)
	response := call(t, m, 1, "tools/list", map[string]any{})
	var listing struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(response.Result, &listing); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range listing.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{
		"accounting_review_queue", "accounting_review_detail",
		"accounting_review_reject", "accounting_review_return",
	} {
		if !names[want] {
			t.Fatalf("tool %q missing from the catalog", want)
		}
	}
}

// TestMCPReviewQueueScopeFirst: the queue returns the pending_review items of the
// EXACT company scope in the arguments; a caller whose scope differs sees an
// EMPTY page — never another company's pending reviews.
func TestMCPReviewQueueScopeFirst(t *testing.T) {
	m, api := newTestMCP(t)
	res := saveOne(t, api, reviewSaveInput("review.queue.a", "pendiente en cmp_01", demoScope()))
	if res.Status != core.StatusPendingReview {
		t.Fatalf("fixture status = %q, want pending_review", res.Status)
	}
	scopeJSON, err := json.Marshal(demoScope())
	if err != nil {
		t.Fatalf("marshal scope: %v", err)
	}

	response := call(t, m, 1, "tools/call", map[string]any{
		"name": "accounting_review_queue",
		"arguments": map[string]any{
			"scope": string(scopeJSON),
		},
	})
	if response.Error != nil {
		t.Fatalf("queue error: %+v", response.Error)
	}
	var page core.ReviewQueuePage
	if err := json.Unmarshal([]byte(toolResultText(t, response)), &page); err != nil {
		t.Fatalf("decode queue page: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].MemoryID != res.Identity.ID {
		t.Fatalf("queue items = %+v, want exactly the pending memory %s", page.Items, res.Identity.ID)
	}
	if page.Items[0].EnvelopeHash == "" || page.Items[0].RecordedBy != "test-agent" {
		t.Fatalf("queue item must carry the CURRENT envelope and the proposer: %+v", page.Items[0])
	}

	// Cross-company invisibility: the same call with a DIFFERENT company sees an
	// empty page — never the other company's pending review.
	other := demoScope()
	other.CompanyID = "cmp_99"
	other.RUC = "20600995804"
	otherJSON, err := json.Marshal(other)
	if err != nil {
		t.Fatalf("marshal other scope: %v", err)
	}
	response = call(t, m, 2, "tools/call", map[string]any{
		"name":      "accounting_review_queue",
		"arguments": map[string]any{"scope": string(otherJSON)},
	})
	var otherPage core.ReviewQueuePage
	if err := json.Unmarshal([]byte(toolResultText(t, response)), &otherPage); err != nil {
		t.Fatalf("decode other page: %v", err)
	}
	if len(otherPage.Items) != 0 {
		t.Fatalf("cross-company queue must be empty, got %d items", len(otherPage.Items))
	}
}

// TestMCPReviewDetailScopeFirst: the detail of ONE pending revision is
// scope-guarded — the same memoryId with a different scope is MEMORY_NOT_FOUND.
func TestMCPReviewDetailScopeFirst(t *testing.T) {
	m, api := newTestMCP(t)
	res := saveOne(t, api, reviewSaveInput("review.detail.a", "detalle pendiente", demoScope()))
	scopeJSON, _ := json.Marshal(demoScope())

	response := call(t, m, 1, "tools/call", map[string]any{
		"name": "accounting_review_detail",
		"arguments": map[string]any{
			"memory_id": res.Identity.ID,
			"scope":     string(scopeJSON),
		},
	})
	if response.Error != nil {
		t.Fatalf("detail error: %+v", response.Error)
	}
	var detail core.ReviewDetail
	if err := json.Unmarshal([]byte(toolResultText(t, response)), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.Memory.Identity.ID != res.Identity.ID {
		t.Fatalf("detail memory = %q, want %q", detail.Memory.Identity.ID, res.Identity.ID)
	}
	if detail.ReviewMetadata.EnvelopeHashToSign == "" || detail.ReviewMetadata.RecordedBy != "test-agent" {
		t.Fatalf("detail metadata must carry H1 and the proposer: %+v", detail.ReviewMetadata)
	}
	if detail.BoundaryNotice != core.ReviewBoundaryNotice {
		t.Fatalf("boundary notice = %q, want %q", detail.BoundaryNotice, core.ReviewBoundaryNotice)
	}

	other := demoScope()
	other.CompanyID = "cmp_99"
	other.RUC = "20600995804"
	otherJSON, _ := json.Marshal(other)
	response = call(t, m, 2, "tools/call", map[string]any{
		"name": "accounting_review_detail",
		"arguments": map[string]any{
			"memory_id": res.Identity.ID,
			"scope":     string(otherJSON),
		},
	})
	if response.Error != nil {
		t.Fatalf("cross-scope detail must be an in-band result: %+v", response.Error)
	}
	if text := toolResultText(t, response); !strings.Contains(text, "MEMORY_NOT_FOUND") {
		t.Fatalf("cross-scope detail = %q, want MEMORY_NOT_FOUND (invisible, never a leak)", text)
	}
}

// TestMCPReviewRejectFailsClosedWithoutSession: accounting_review_reject accepts
// exactly its four command arguments but the stdio MCP server has NO authenticated
// session binding, so the tool FAILS CLOSED with AUTHENTICATION_REQUIRED as an
// in-band tool result — never a JSON-RPC error, never a silent identity.
func TestMCPReviewRejectFailsClosedWithoutSession(t *testing.T) {
	m, _ := newTestMCP(t)
	response := call(t, m, 1, "tools/call", map[string]any{
		"name": "accounting_review_reject",
		"arguments": map[string]any{
			"memory_id":              "m-1",
			"expected_envelope_hash": "abc",
			"reason":                 "fuera de alcance",
			"request_id":             "req-1",
		},
	})
	if response.Error != nil {
		t.Fatalf("domain failure must be a tool result, not a JSON-RPC error: %+v", response.Error)
	}
	var output toolCallOutput
	if err := json.Unmarshal(response.Result, &output); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
	if !output.IsError {
		t.Fatal("isError = false, want true (fail closed without a session binding)")
	}
	if len(output.Content) == 0 || !strings.Contains(output.Content[0]["text"], "AUTHENTICATION_REQUIRED") {
		t.Fatalf("error text must carry AUTHENTICATION_REQUIRED: %v", output.Content)
	}
}

// TestMCPReviewReturnFailsClosedWithoutSession: the return tool fails closed with
// the same AUTHENTICATION_REQUIRED contract as reject/approve.
func TestMCPReviewReturnFailsClosedWithoutSession(t *testing.T) {
	m, _ := newTestMCP(t)
	response := call(t, m, 1, "tools/call", map[string]any{
		"name": "accounting_review_return",
		"arguments": map[string]any{
			"memory_id":              "m-1",
			"expected_envelope_hash": "abc",
			"reason":                 "falta evidencia",
			"request_id":             "req-1",
		},
	})
	if response.Error != nil {
		t.Fatalf("domain failure must be a tool result: %+v", response.Error)
	}
	var output toolCallOutput
	if err := json.Unmarshal(response.Result, &output); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
	if !output.IsError || len(output.Content) == 0 || !strings.Contains(output.Content[0]["text"], "AUTHENTICATION_REQUIRED") {
		t.Fatalf("return must fail closed with AUTHENTICATION_REQUIRED: %+v", output)
	}
}

// TestMCPReviewRejectRejectsExtraArgs: the decision tools parse their args
// STRICTLY — ANY unknown field, including any caller-supplied authority
// (actorId/actorKind/subjectId/roles), is a malformed argument shape
// (JSON-RPC -32602), never silently ignored.
func TestMCPReviewRejectRejectsExtraArgs(t *testing.T) {
	m, _ := newTestMCP(t)
	base := map[string]any{
		"memory_id": "m-1", "expected_envelope_hash": "abc", "reason": "r", "request_id": "q-1",
	}
	cases := []struct {
		name   string
		extras map[string]any
	}{
		{"actorId", map[string]any{"actorId": "maria.torres"}},
		{"actorKind", map[string]any{"actorKind": "human"}},
		{"roles", map[string]any{"roles": []string{"controller"}}},
		{"subjectId", map[string]any{"subjectId": "maria.torres"}},
		{"unrelated extra", map[string]any{"bogus": 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := make(map[string]any, len(base)+len(tc.extras))
			for k, v := range base {
				args[k] = v
			}
			for k, v := range tc.extras {
				args[k] = v
			}
			response := call(t, m, 1, "tools/call", map[string]any{
				"name": "accounting_review_reject", "arguments": args,
			})
			if response.Error == nil || response.Error.Code != codeInvalidParams {
				t.Fatalf("extra args %v: want JSON-RPC -32602, got %+v", tc.extras, response.Error)
			}
		})
	}
}
