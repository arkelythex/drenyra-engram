// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test is the AC-6 / NFR-5 structural
// readback + contract guard for the G-7 key-compromise playbook
// (docs/security/key-compromise-response.md): it pins the eight NIST-aligned
// response steps IN ORDER, the exact compromise-time recording, the FZ-3
// cutoff/retention policy, the fail-closed unparseable-timestamp rule, the
// non-authorization/recovery boundaries, and the completed gap-analysis
// conclusion. The contract guard then proves the playbook's quoted evidence is
// VERBATIM in the frozen contract (contracts/verification.md) AND in the
// implementation (internal/core/verify.go) — a three-way pin that breaks red if
// docs, contract and code ever drift apart.
package server

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// wsCollapse normalizes runs of whitespace (including markdown line wrapping at
// ~80 columns) to single spaces, so verbatim sentence pins survive source-line
// breaks without weakening them to substring fragments.
var wsCollapse = regexp.MustCompile(`\s+`)

func collapseWS(s string) string {
	return wsCollapse.ReplaceAllString(s, " ")
}

// keyCompromisePlaybookReadback returns the playbook bytes; the read fails the
// test when the file is missing (the doc is a first-class artifact of W3).
func keyCompromisePlaybookReadback(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../../docs/security/key-compromise-response.md")
	if err != nil {
		t.Fatalf("read docs/security/key-compromise-response.md: %v", err)
	}
	return string(data)
}

// TestKeyCompromisePlaybookStructuralReadback asserts the playbook's structure
// (AC-6 / WU-5): the exact eight ordered NIST-aligned steps, exact compromise-
// time recording, pre-cutoff retention policy, at/after fail-closed policy,
// RFC3339 fail-closed rule, non-authorization/recovery boundaries, and the
// completed gap-analysis conclusion with quoted evidence.
func TestKeyCompromisePlaybookStructuralReadback(t *testing.T) {
	// Whitespace-normalized: markdown wraps prose at ~80 columns, so verbatim
	// phrase pins must survive line breaks (same rule as the contract guard).
	playbook := collapseWS(keyCompromisePlaybookReadback(t))

	// The eight response steps, in the frozen order of spec FR-7.
	steps := []string{
		"### Step 1 — Treat the exposure as a FULL TRUST FAILURE",
		"### Step 2 — Stop signing and suspend verification with the compromised key",
		"### Step 3 — Preserve evidence and record the exact compromise time",
		"### Step 4 — Generate an INDEPENDENT replacement keypair in a clean environment",
		"### Step 5 — Publish an authenticated revocation",
		"### Step 6 — Fail closed for signatures at/after the cutoff",
		"### Step 7 — Inventory and re-sign affected artifacts where policy permits",
		"### Step 8 — Investigate adjacent systems as potentially compromised",
	}
	last := -1
	for _, heading := range steps {
		idx := strings.Index(playbook, heading)
		if idx < 0 {
			t.Fatalf("playbook must contain heading %q (eight-step sequence broken)", heading)
		}
		if idx < last {
			t.Fatalf("step headings must appear in order; %q appears before a prior step", heading)
		}
		last = idx
	}

	// NIST alignment and the exact compromise-time recording discipline.
	for _, want := range []string{
		"NIST SP 800-57",
		"exact compromise time",
		"RFC3339",
	} {
		if !strings.Contains(playbook, want) {
			t.Fatalf("playbook must state %q", want)
		}
	}

	// FZ-3 cutoff policy: pre-cutoff retention AND at/after fail-closed.
	for _, want := range []string{
		"issued_at < revoked_at",
		"retained",
		"issued_at >= revoked_at",
		"reject",
		"created_at > issued_at",
		"never a guess",
	} {
		if !strings.Contains(playbook, want) {
			t.Fatalf("playbook must pin the FZ-3 cutoff policy %q", want)
		}
	}

	// Non-authorization / recovery boundaries (IR-3, NFR-2).
	for _, want := range []string{
		"never reopens writes",
		"never authorizes recovery",
		"read-only",
	} {
		if !strings.Contains(playbook, want) {
			t.Fatalf("playbook must state the boundary %q", want)
		}
	}

	// The engine commands the playbook claims (must be real commands).
	for _, want := range []string{
		"keys rotate --db",
		"keys show",
		"verify receipt",
	} {
		if !strings.Contains(playbook, want) {
			t.Fatalf("playbook must document the real engine command %q", want)
		}
	}

	// The gap analysis must reach the frozen conclusion.
	if !strings.Contains(playbook, "Implementation == Contract") {
		t.Fatalf("playbook gap analysis must conclude 'Implementation == Contract' (FZ-3)")
	}
}

// TestKeyCompromisePlaybookContractGuard is the NFR-5 contract guard: the
// playbook's quoted evidence sentence must exist VERBATIM in the frozen
// contract AND in the implementation. Any of the three (docs, contract, code)
// drifting apart turns this test red — no semantic change may be shipped to
// reconcile it (a real mismatch is surfaced, never silently fixed).
func TestKeyCompromisePlaybookContractGuard(t *testing.T) {
	playbook := collapseWS(keyCompromisePlaybookReadback(t))

	quoted := "Issued before revocation passes; issued at/after revocation fails."

	contract, err := os.ReadFile("../../contracts/verification.md")
	if err != nil {
		t.Fatalf("read contracts/verification.md: %v", err)
	}
	impl, err := os.ReadFile("../../internal/core/verify.go")
	if err != nil {
		t.Fatalf("read internal/core/verify.go: %v", err)
	}
	contractWS := collapseWS(string(contract))
	implWS := collapseWS(strings.ReplaceAll(string(impl), "//", ""))

	if !strings.Contains(playbook, quoted) {
		t.Fatalf("playbook must quote the contract evidence sentence %q", quoted)
	}
	if !strings.Contains(contractWS, quoted) {
		t.Fatalf("contracts/verification.md must contain the quoted evidence sentence %q (frozen surface)", quoted)
	}
	if !strings.Contains(implWS, quoted) {
		t.Fatalf("internal/core/verify.go must contain the quoted evidence sentence %q (implementation)", quoted)
	}

	// The gap analysis must name the surfaces it compared (FR-7).
	for _, want := range []string{
		"SigningKeyForVerify",
		"LookupSigningKey",
		"VerifySigningKeyValidity",
		"revoke-only trigger",
	} {
		if !strings.Contains(playbook, want) {
			t.Fatalf("gap analysis must name %q", want)
		}
	}
}
