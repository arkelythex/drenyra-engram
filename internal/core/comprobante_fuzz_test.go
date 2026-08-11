// Fuzz target for the SUNAT comprobante adapter parsers (spec FR-1 / design
// D-10, WU-4). Drives ParseComprobanteXML and ParseCDRXML
// (internal/core/comprobante.go) with arbitrary bytes and enforces the frozen
// invariants: no panic, deterministic parse/error classification, typed
// INVALID_* errors (never a generic panic), and internally consistent metadata
// on success (serie+numero == documentId, shape- and checksum-valid emitter
// RUC, whole-cents non-negative total, closed <Invoice> kind).
//
// Input cap: any input strictly larger than 1 MiB is ignored immediately
// (spec FR-1 v, design D-10) so expensive non-crashing inputs stay bounded
// without weakening the parsers.
package core

import (
	"strings"
	"testing"
)

// fuzzMaxInputBytes is the per-input size cap shared by the core fuzz targets
// (spec FR-1 v, design D-10): inputs strictly larger than 1 MiB return before
// parsing.
const fuzzMaxInputBytes = 1 << 20 // 1 MiB

// cdrFixture is a minimal SUNAT CDR (constancia de recepción) — fictional
// document number, used as a production-shaped seed for the CDR parser.
const cdrFixture = `<?xml version="1.0"?>
<ApplicationResponse xmlns:cac="urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2" xmlns:cbc="urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2">
  <cbc:ResponseCode>0</cbc:ResponseCode>
  <cbc:Description>La Factura numero F001-948, ha sido aceptada</cbc:Description>
</ApplicationResponse>`

// checkComprobanteInvariants runs the frozen FuzzParseComprobanteXML invariant
// set over data (spec FR-1, AC-1). It is shared by the fuzz target and the
// named regression/corpus tests so the corpus replay and the unit tests
// exercise the exact same checks.
func checkComprobanteInvariants(t testing.TB, data []byte) {
	t.Helper()

	c1, err1 := ParseComprobanteXML(data)
	c2, err2 := ParseComprobanteXML(data)

	// Determinism: identical input yields the identical result, and the two
	// runs agree on success/error classification and on the exact error text.
	if (err1 == nil) != (err2 == nil) {
		t.Fatalf("non-deterministic error classification for input of %d bytes", len(data))
	}
	if err1 != nil {
		if err1.Error() != err2.Error() {
			t.Fatalf("non-deterministic error text: %q vs %q", err1.Error(), err2.Error())
		}
		if !strings.HasPrefix(err1.Error(), "INVALID_") {
			t.Fatalf("parser failure is not a typed INVALID_* error: %v", err1)
		}
		return
	}
	if c1 != c2 {
		t.Fatalf("non-deterministic parse result: %+v vs %+v", c1, c2)
	}

	// No-invalid-success: a nil error must yield internally consistent
	// metadata (spec FR-1 iv).
	if c1.DocumentID == "" || c1.DocumentID != c1.Serie+"-"+c1.Numero {
		t.Fatalf("documentId %q is not serie-numero of serie %q numero %q", c1.DocumentID, c1.Serie, c1.Numero)
	}
	if !IsValidRUC(c1.EmitterRUC) || !isValidRUCChecksum(c1.EmitterRUC) {
		t.Fatalf("emitter RUC %q is not shape- and checksum-valid", c1.EmitterRUC)
	}
	if c1.Kind != ComprobanteFactura && c1.Kind != ComprobanteBoleta {
		t.Fatalf("kind %q is outside the frozen <Invoice> set", c1.Kind)
	}
	if c1.TotalCents < 0 || c1.Currency == "" || c1.IssueDate == "" {
		t.Fatalf("invalid success metadata: %+v", c1)
	}

	// The CDR parser rides the same input: same determinism contract, typed
	// INVALID_* failures, and a non-empty response code on success.
	cdr1, cdrErr1 := ParseCDRXML(data)
	cdr2, cdrErr2 := ParseCDRXML(data)
	if (cdrErr1 == nil) != (cdrErr2 == nil) {
		t.Fatalf("non-deterministic CDR error classification for input of %d bytes", len(data))
	}
	if cdrErr1 != nil {
		if cdrErr1.Error() != cdrErr2.Error() {
			t.Fatalf("non-deterministic CDR error text: %q vs %q", cdrErr1.Error(), cdrErr2.Error())
		}
		if !strings.HasPrefix(cdrErr1.Error(), "INVALID_") {
			t.Fatalf("CDR failure is not a typed INVALID_* error: %v", cdrErr1)
		}
		return
	}
	if cdr1 != cdr2 {
		t.Fatalf("non-deterministic CDR result: %+v vs %+v", cdr1, cdr2)
	}
	if cdr1.ResponseCode == "" {
		t.Fatalf("CDR success with empty ResponseCode: %+v", cdr1)
	}
}

// FuzzParseComprobanteXML fuzzes the SUNAT comprobante XML adapter parsers
// (ParseComprobanteXML + ParseCDRXML, internal/core/comprobante.go). Input cap:
// 1 MiB (fuzzMaxInputBytes) — larger inputs are ignored before parsing.
func FuzzParseComprobanteXML(f *testing.F) {
	// Starting seeds: the committed corpus under
	// testdata/fuzz/FuzzParseComprobanteXML/ mirrors these (valid
	// production-shaped fictional data, empty, 1 byte, truncated, wrong
	// encoding, unbounded-depth probe, previously-failing amount).
	f.Add([]byte(validInvoice))
	f.Add([]byte(cdrFixture))
	f.Add([]byte{})
	f.Add([]byte("<"))
	f.Add([]byte(`<Invoice/>`))
	f.Add([]byte(`hello world`))
	f.Add([]byte(`<Invoice><cbc:ID>F001-948</cbc:ID></Invoice>`))
	f.Add([]byte(`<?xml version="1.0" encoding="ISO-8859-1"?><Invoice><cbc:ID>F001-001</cbc:ID><cbc:IssueDate>2026-01-31</cbc:IssueDate><cbc:InvoiceTypeCode>01</cbc:InvoiceTypeCode><cac:AccountingSupplierParty><cac:Party><cac:PartyIdentification><cbc:ID>20100070970</cbc:ID></cac:PartyIdentification></cac:Party></cac:AccountingSupplierParty><cac:LegalMonetaryTotal><cbc:PayableAmount>10.00</cbc:PayableAmount></cac:LegalMonetaryTotal></Invoice>`))
	f.Add([]byte(strings.Replace(validInvoice, "1284.30", "1284abc", 1)))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > fuzzMaxInputBytes {
			return
		}
		checkComprobanteInvariants(t, data)
	})
}
