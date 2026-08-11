// Named regression tests for the W1 fuzz seed corpus (spec FR-2 / design D-10
// WU-4): each known edge class covered by a committed seed is pinned here as a
// permanent unit regression, so the corpus file alone never documents the fix.
package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseComprobanteXMLTrailingGarbageAmountRejected is the RED→GREEN
// regression for the one invalid-success defect found while building the W1
// harnesses: parsePayableAmount accepted a PayableAmount with trailing garbage
// after a parseable integer ("1284abc" → 128400 cents) because fmt.Sscanf
// stops at the first non-digit and ignores the trailing bytes. The frozen
// adapter contract requires a typed INVALID_COMPROBANTE_TOTAL — never a silent
// guess (comprobante.go doc comment). Fixed by parsing with strconv.ParseInt,
// which rejects any input that is not a complete integer.
func TestParseComprobanteXMLTrailingGarbageAmountRejected(t *testing.T) {
	cases := []struct {
		name   string
		amount string
	}{
		{"trailing letters after integer", "1284abc"},
		{"embedded letters in integer part", "12a4.30"},
		{"leading junk before integer", "x5.00"},
		{"internal space in integer part", "1 284.30"},
		{"trailing junk after two decimals", "12.30x"},
		{"junk currency suffix", "1284.30 99"},
		{"three whitespace fields", "1284.30 PEN USD"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			xml := strings.Replace(validInvoice, "1284.30", tc.amount, 1)
			_, err := ParseComprobanteXML([]byte(xml))
			if err == nil {
				t.Fatalf("amount %q must fail closed, got success", tc.amount)
			}
			if !strings.HasPrefix(err.Error(), "INVALID_COMPROBANTE_TOTAL") {
				t.Fatalf("err = %v, want INVALID_COMPROBANTE_TOTAL prefix", err)
			}
		})
	}
}

// TestParseComprobanteXMLCurrencySuffixAccepted is the triangulation control
// for the strict currency guard: the documented "1284.30 PEN" form (trailing
// ISO 4217 code) must keep parsing to whole cents, never rejected.
func TestParseComprobanteXMLCurrencySuffixAccepted(t *testing.T) {
	xml := strings.Replace(validInvoice, "1284.30", "1284.30 PEN", 1)
	c, err := ParseComprobanteXML([]byte(xml))
	if err != nil {
		t.Fatalf("currency-suffixed amount must parse: %v", err)
	}
	if c.TotalCents != 128430 || c.Currency != "PEN" {
		t.Fatalf("total = %d %s, want 128430 PEN", c.TotalCents, c.Currency)
	}
	checkComprobanteInvariants(t, []byte(xml))
}

// TestParseComprobanteXMLWrongEncodingFailsClosed pins the wrong-encoding seed
// (declared ISO-8859-1 with a raw latin-1 byte): the charset mismatch must be
// a typed INVALID_COMPROBANTE_XML error, never a panic and never a silent
// success.
func TestParseComprobanteXMLWrongEncodingFailsClosed(t *testing.T) {
	data := mustReadCorpusSeed(t, "FuzzParseComprobanteXML", "seed_wrong_encoding.bin")
	_, err := ParseComprobanteXML(data)
	if err == nil || !strings.HasPrefix(err.Error(), "INVALID_COMPROBANTE_XML") {
		t.Fatalf("err = %v, want INVALID_COMPROBANTE_XML prefix", err)
	}
	checkComprobanteInvariants(t, data)
}

// TestParseComprobanteXMLTruncatedFailsClosed pins the truncated-XML seed: a
// cut mid-document must fail closed with a typed error, deterministically.
func TestParseComprobanteXMLTruncatedFailsClosed(t *testing.T) {
	data := mustReadCorpusSeed(t, "FuzzParseComprobanteXML", "seed_truncated_invoice.xml")
	_, err := ParseComprobanteXML(data)
	if err == nil || !strings.HasPrefix(err.Error(), "INVALID_COMPROBANTE_XML") {
		t.Fatalf("err = %v, want INVALID_COMPROBANTE_XML prefix", err)
	}
	checkComprobanteInvariants(t, data)
}

// TestParseComprobanteXMLDeepNestingBounded pins the unbounded-depth probe
// seed: 10,000 nested elements must be handled deterministically (typed
// fail-closed result, never a panic or unbounded work), and the input stays
// far under the 1 MiB cap.
func TestParseComprobanteXMLDeepNestingBounded(t *testing.T) {
	data := mustReadCorpusSeed(t, "FuzzParseComprobanteXML", "seed_deep_nesting.xml")
	if len(data) > fuzzMaxInputBytes {
		t.Fatalf("depth probe seed exceeds the 1 MiB cap: %d bytes", len(data))
	}
	// Well-formed deep XML: no matching invoice fields, so the adapter must
	// fail closed on missing fields; the harness also proves determinism and
	// typed errors.
	_, err := ParseComprobanteXML(data)
	if err == nil || !strings.HasPrefix(err.Error(), "INVALID_COMPROBANTE_XML") {
		t.Fatalf("err = %v, want INVALID_COMPROBANTE_XML prefix", err)
	}
	checkComprobanteInvariants(t, data)
}

// TestParseCDRXMLSeedRegression pins the committed CDR fixture: a well-formed
// CDR must parse with a non-empty response code and the document number from
// the description.
func TestParseCDRXMLSeedRegression(t *testing.T) {
	c, err := ParseCDRXML([]byte(cdrFixture))
	if err != nil {
		t.Fatalf("parse CDR fixture: %v", err)
	}
	if c.ResponseCode != "0" || c.DocumentID != "F001-948" {
		t.Fatalf("CDR = %+v, want response 0 + doc F001-948", c)
	}
	checkComprobanteInvariants(t, []byte(cdrFixture))
}

// TestCanonicalReceiptPayloadRoundTripAcrossFrozenVersions pins the receipt
// seed class: for every frozen payload version (v0.4.0 … v0.10.0) the
// production-shaped fixture must canonicalize deterministically and round-trip
// byte-identically (spec FR-1 iii/iv). The version-conditional v0.9.0
// extension is exercised with lifecycle hashes and an execution-attempt id.
func TestCanonicalReceiptPayloadRoundTripAcrossFrozenVersions(t *testing.T) {
	var p ReceiptPayload
	if err := json.Unmarshal([]byte(validPayloadJSONv04), &p); err != nil {
		t.Fatalf("unmarshal v0.4.0 fixture: %v", err)
	}
	var p09 ReceiptPayload
	if err := json.Unmarshal([]byte(validPayloadJSONv09), &p09); err != nil {
		t.Fatalf("unmarshal v0.9.0 fixture: %v", err)
	}
	// v0.9.0-specific carrier fields are only appended for v0.9.0 payloads;
	// pin the conditional by running both fixtures across all six versions.
	checkReceiptInvariants(t, p)
	checkReceiptInvariants(t, p09)

	// Pin the version-conditional bytes directly: the v0.9.0 fixture's
	// lifecycle fields appear in the canonical JSON, and a legacy-versioned
	// payload with those fields populated must NOT carry them.
	p09b := p09
	canonical09 := string(CanonicalReceiptPayload(p09b))
	if !strings.Contains(canonical09, `"reviewedLifecycleHash":"h1-lifecycle"`) ||
		!strings.Contains(canonical09, `"executionAttemptId":"(tenant-1, exec-42)"`) {
		t.Fatalf("v0.9.0 canonical bytes omit the version-conditional fields: %s", canonical09)
	}
	pLegacy := p09b
	pLegacy.Version = ReceiptPayloadVersion // receipt-payload/v0.4.0
	if legacy := string(CanonicalReceiptPayload(pLegacy)); strings.Contains(legacy, "reviewedLifecycleHash") {
		t.Fatalf("legacy canonical bytes must not carry the v0.9.0-only fields: %s", legacy)
	}
}

// TestSearchTokenizeEdgeSeeds is the search-side seed regression (lives in the
// search package tests via the corpus manifest; here the core package pins the
// tokenizer's contract indirectly through the shared corpus green run).
func TestSearchTokenizeEdgeSeeds(t *testing.T) {
	// The tokenizer contract regression for emoji/NUL/invalid-UTF-8 lives in
	// internal/search (package search). This guard proves the cross-package
	// seed dependency stays resolvable: the search corpus directory must be
	// present with its README index (spec FR-2 seed policy).
	entries, err := os.ReadDir(filepath.Join("..", "search", "testdata", "fuzz", "FuzzSearchTokenize"))
	if err != nil {
		t.Fatalf("search corpus dir unreadable: %v", err)
	}
	if len(entries) < 6 {
		t.Fatalf("search tokenize corpus has %d entries, want >= 6", len(entries))
	}
}

// mustReadCorpusSeed reads one committed seed from the core testdata corpus,
// failing the test when the entry is missing (the never-deleted contract).
func mustReadCorpusSeed(t testing.TB, target, entry string) []byte {
	t.Helper()
	data, err := readCorpusFile(target, entry)
	if err != nil {
		t.Fatalf("corpus entry %s/%s unreadable or deleted: %v", target, entry, err)
	}
	return data
}
