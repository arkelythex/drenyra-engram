// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test runs the SHARED golden vectors
// (testdata/golden/*.json) against the Go implementation. The SAME files run
// from TypeScript (core/__tests__/golden.test.ts), so Go and TS must agree on
// the canonical hashes, the approval gate and the initial status. The expected
// hashes are FIXED values — a divergence between runtimes fails one of the two
// runners, never silently.

package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// goldenCase is the shared vector shape (testdata/golden/*.json).
type goldenCase struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Input       goldenInput    `json:"input"`
	Expected    goldenExpected `json:"expected"`
}

type goldenInput struct {
	ID           string       `json:"id"`
	TopicKey     string       `json:"topicKey"`
	Title        string       `json:"title"`
	Kind         MemoryKind   `json:"kind"`
	Scope        Scope        `json:"scope"`
	Content      Content      `json:"content"`
	FiscalEffect FiscalEffect `json:"fiscalEffect"`
	EffectiveAt  string       `json:"effectiveAt"`
	RecordedAt   string       `json:"recordedAt"`
	ObservedAt   string       `json:"observedAt"`
	Source       Source       `json:"source"`
	EvidenceRefs []string     `json:"evidenceRefs"`
	RuleRefs     []string     `json:"ruleRefs"`
	Confidence   *float64     `json:"confidence"`
	Materiality  *int64       `json:"materiality"`
	ReceiptID    string       `json:"receiptId"`
	SupersedesID string       `json:"supersedesId"`
	Status       MemoryStatus `json:"status"`
}

type goldenExpected struct {
	ContentHash     string `json:"contentHash"`
	IdentityHash    string `json:"identityHash"`
	EnvelopeHash    string `json:"envelopeHash"`
	InitialStatus   string `json:"initialStatus"`
	CanApproveAgent bool   `json:"canApproveAgent"`
	CanApproveHuman bool   `json:"canApproveHuman"`
}

// TestGoldenVectorsGo runs every shared vector against the Go implementation.
func TestGoldenVectorsGo(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "testdata", "golden", "*.json"))
	if err != nil {
		t.Fatalf("glob golden: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no golden vectors found")
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var tc goldenCase
		if err := json.Unmarshal(raw, &tc); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		t.Run(tc.Name, func(t *testing.T) {
			memory := AccountingMemory{
				Identity:     Identity{ID: tc.Input.ID, TopicKey: tc.Input.TopicKey},
				Title:        tc.Input.Title,
				Kind:         tc.Input.Kind,
				Scope:        tc.Input.Scope,
				Content:      tc.Input.Content,
				FiscalEffect: tc.Input.FiscalEffect,
				EffectiveAt:  tc.Input.EffectiveAt,
				RecordedAt:   tc.Input.RecordedAt,
				ObservedAt:   tc.Input.ObservedAt,
				Source:       tc.Input.Source,
				EvidenceRefs: tc.Input.EvidenceRefs,
				RuleRefs:     tc.Input.RuleRefs,
				Confidence:   tc.Input.Confidence,
				Materiality:  tc.Input.Materiality,
				ReceiptID:    tc.Input.ReceiptID,
				SupersedesID: tc.Input.SupersedesID,
				Status:       MemoryStatus(tc.Input.Status),
			}
			if memory.Status == "" {
				memory.Status = InitialStatus(memory.FiscalEffect)
			}

			contentHash := ComputeContentHash(memory)
			identityHash := ComputeIdentityHash(memory)
			memory.ContentHash = contentHash
			envelopeHash := ComputeEnvelopeHash(memory)

			// initial status from the gate
			if got := string(InitialStatus(memory.FiscalEffect)); got != tc.Expected.InitialStatus {
				t.Errorf("%s: initialStatus = %s, want %s", tc.Name, got, tc.Expected.InitialStatus)
			}

			// approval gate: agents never approve; humans approve pending_review
			agentApproves := Approve(&memory, Source{System: "golden", ActorID: "a", ActorKind: ActorKindAgent}) == nil
			if agentApproves != tc.Expected.CanApproveAgent {
				t.Errorf("%s: canApproveAgent = %v, want %v", tc.Name, agentApproves, tc.Expected.CanApproveAgent)
			}
			humanApproves := Approve(&memory, Source{System: "golden", ActorID: "h", ActorKind: ActorKindHuman}) == nil
			if humanApproves != tc.Expected.CanApproveHuman {
				t.Errorf("%s: canApproveHuman = %v, want %v", tc.Name, humanApproves, tc.Expected.CanApproveHuman)
			}

			// fixed hashes: same value in Go and TS (the shared contract)
			if tc.Expected.ContentHash != "" && contentHash != tc.Expected.ContentHash {
				t.Errorf("%s: contentHash = %s, want %s", tc.Name, contentHash, tc.Expected.ContentHash)
			}
			if tc.Expected.IdentityHash != "" && identityHash != tc.Expected.IdentityHash {
				t.Errorf("%s: identityHash = %s, want %s", tc.Name, identityHash, tc.Expected.IdentityHash)
			}
			if tc.Expected.EnvelopeHash != "" && envelopeHash != tc.Expected.EnvelopeHash {
				t.Errorf("%s: envelopeHash = %s, want %s", tc.Name, envelopeHash, tc.Expected.EnvelopeHash)
			}

			// print the computed hashes so the golden files can be pinned
			t.Logf("HASHES %s content=%s identity=%s envelope=%s", tc.Name, contentHash, identityHash, envelopeHash)
		})
	}
}
