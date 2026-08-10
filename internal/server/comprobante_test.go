// Comprobante ingestion adapter end-to-end — design brief §7.3.
package server

import (
	"context"
	"testing"
)

// validInvoiceFixture is the minimal UBL 2.1 invoice (fictional RUC
// 20100070970) — the SAME fixture the core parser tests use, restated here
// because test fixtures are not shared across packages.
const validInvoiceFixture = `<?xml version="1.0" encoding="UTF-8"?>
<Invoice xmlns="urn:oasis:names:specification:ubl:schema:xsd:Invoice-2"
 xmlns:cac="urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2"
 xmlns:cbc="urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2">
  <cbc:ID>F001-948</cbc:ID>
  <cbc:IssueDate>2026-01-31</cbc:IssueDate>
  <cbc:InvoiceTypeCode>01</cbc:InvoiceTypeCode>
  <cac:AccountingSupplierParty>
    <cac:Party><cac:PartyIdentification><cbc:ID schemeID="6">20100070970</cbc:ID></cac:PartyIdentification></cac:Party>
  </cac:AccountingSupplierParty>
  <cac:LegalMonetaryTotal><cbc:PayableAmount currencyID="PEN">1284.30</cbc:PayableAmount></cac:LegalMonetaryTotal>
</Invoice>`

// TestIngestComprobanteEndToEnd — the adapter contract: parse + WORM store in
// the exact scope; identical bytes → content-addressed duplicate NO-OP; the
// stored bytes are the exact invoice bytes (WORM read-back).
func TestIngestComprobanteEndToEnd(t *testing.T) {
	ctx := context.Background()
	api := closeAcceptanceStore(t)
	scope := ruleFixtureScope()

	comp, result, err := IngestComprobante(ctx, api.Store, scope, testAgentSource, []byte(validInvoiceFixture))
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if !result.Created {
		t.Fatalf("first store must be a fresh object")
	}
	if comp.TotalCents != 128430 || comp.EmitterRUC != "20100070970" {
		t.Fatalf("parsed metadata = %+v", comp)
	}
	// Identical bytes → content-addressed duplicate NO-OP.
	_, dup, err := IngestComprobante(ctx, api.Store, scope, testAgentSource, []byte(validInvoiceFixture))
	if err != nil {
		t.Fatalf("re-ingest: %v", err)
	}
	if dup.Created {
		t.Fatalf("duplicate store must be a NO-OP (content addressing)")
	}
	// WORM read-back returns the exact bytes.
	_, bytes, err := api.GetObject(ctx, result.Object.ObjectID, scope)
	if err != nil {
		t.Fatalf("read object: %v", err)
	}
	if string(bytes) != validInvoiceFixture {
		t.Fatalf("WORM bytes mismatch")
	}
}
