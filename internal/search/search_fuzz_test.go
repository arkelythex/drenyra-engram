// Fuzz target for the scope-first tokenizer (spec FR-1 / design D-10, WU-4).
// Drives tokenize (internal/search/search.go) with arbitrary UTF-8 — including
// invalid bytes, emoji and NUL — and enforces the frozen invariants: no panic,
// deterministic tokens, only non-empty tokens, and no separator characters in
// emitted tokens.
//
// Input cap: any input strictly larger than 1 MiB is ignored immediately
// (spec FR-1 v, design D-10).
package search

import (
	"regexp"
	"slices"
	"testing"
)

// searchFuzzMaxInputBytes is the per-input size cap for the search fuzz target
// (spec FR-1 v, design D-10): inputs strictly larger than 1 MiB return before
// tokenizing.
const searchFuzzMaxInputBytes = 1 << 20 // 1 MiB

// tokenShapePattern matches exactly the characters a valid emitted token may
// contain — the complement of the reference token separator class
// /[^a-z0-9áéíóúñ]+/u after strings.ToLower.
var tokenShapePattern = regexp.MustCompile(`^[a-z0-9áéíóúñ]+$`)

// checkTokenizeInvariants runs the frozen FuzzSearchTokenize invariant set
// over data (spec FR-1, AC-1). It is shared by the fuzz target and the named
// regression/corpus tests so the corpus replay and the unit tests exercise the
// exact same checks.
func checkTokenizeInvariants(t testing.TB, data []byte) {
	t.Helper()

	// Determinism: identical input yields identical tokens.
	first := tokenize(string(data))
	if !slices.Equal(first, tokenize(string(data))) {
		t.Fatalf("non-deterministic tokens for input of %d bytes", len(data))
	}

	// No-invalid-success: emitted tokens are non-empty and contain no
	// separator characters (spec FR-1 iv).
	for _, token := range first {
		if token == "" {
			t.Fatalf("empty token emitted for input of %d bytes", len(data))
		}
		if !tokenShapePattern.MatchString(token) {
			t.Fatalf("token %q contains separator characters for input of %d bytes", token, len(data))
		}
	}
}

// FuzzSearchTokenize fuzzes the scope-first tokenizer (tokenize,
// internal/search/search.go). Input cap: 1 MiB (searchFuzzMaxInputBytes) —
// larger inputs are ignored before tokenizing.
func FuzzSearchTokenize(f *testing.F) {
	// Starting seeds: the committed corpus under
	// testdata/fuzz/FuzzSearchTokenize/ mirrors these (empty, 1 byte,
	// production-shaped Spanish text, emoji + punctuation, NUL/control,
	// invalid UTF-8, at-cap and just-below-cap boundary).
	f.Add([]byte{})
	f.Add([]byte("a"))
	f.Add([]byte("Compañía García S.A.C. emitió la factura F001-948 el 31/01/2026 por el servicio de consultoría"))
	f.Add([]byte("💸 transferencia — #2026-01 · confirmada ✅"))
	f.Add([]byte("a\x00b\x01c"))
	f.Add([]byte{0xff, 0xfe, 0x80, 0xc3, 0x28})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > searchFuzzMaxInputBytes {
			return
		}
		checkTokenizeInvariants(t, data)
	})
}
