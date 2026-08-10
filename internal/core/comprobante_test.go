// Comprobante adapter contract tests — design brief §7.3 (pure parser).
package core

import (
	"strings"
	"testing"
)

// validInvoice is a minimal UBL 2.1 invoice (fictional RUC 20100070970).
const validInvoice = `<?xml version="1.0" encoding="UTF-8"?>
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

func TestParseComprobanteXMLValid(t *testing.T) {
	c, err := ParseComprobanteXML([]byte(validInvoice))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.DocumentID != "F001-948" || c.Serie != "F001" || c.Numero != "948" {
		t.Fatalf("document = %+v, want F001-948 / F001 / 948", c)
	}
	if c.EmitterRUC != "20100070970" {
		t.Fatalf("emitter RUC = %q, want 20100070970", c.EmitterRUC)
	}
	if c.TotalCents != 128430 || c.Currency != "PEN" {
		t.Fatalf("total = %d %s, want 128430 PEN (whole cents, never float)", c.TotalCents, c.Currency)
	}
	if c.Kind != ComprobanteFactura || c.IssueDate != "2026-01-31" {
		t.Fatalf("kind/date = %q/%q, want factura/2026-01-31", c.Kind, c.IssueDate)
	}
}

func TestParseComprobanteXMLFailures(t *testing.T) {
	cases := []struct {
		name string
		xml  string
		want string
	}{
		{"not XML", "hello world", "INVALID_COMPROBANTE_XML"},
		{"missing fields", `<Invoice><cbc:ID>F001-948</cbc:ID></Invoice>`, "INVALID_COMPROBANTE_XML"},
		{"bad doc id", strings.Replace(validInvoice, "F001-948", "notanid", 1), "INVALID_COMPROBANTE_ID"},
		{"bad emitter RUC (non-digit)", strings.Replace(validInvoice, "20100070970", "2010003920X", 1), "INVALID_EMITTER_RUC"},
		{"bad emitter RUC (shape-valid, checksum-invalid)", strings.Replace(validInvoice, "20100070970", "20100039201", 1), "INVALID_EMITTER_RUC"},
		{"kind 07 is a CreditNote root, not an Invoice", strings.Replace(validInvoice, "<cbc:InvoiceTypeCode>01</cbc:InvoiceTypeCode>", "<cbc:InvoiceTypeCode>07</cbc:InvoiceTypeCode>", 1), "INVALID_COMPROBANTE_KIND"},
		{"kind 99 unknown", strings.Replace(validInvoice, "<cbc:InvoiceTypeCode>01</cbc:InvoiceTypeCode>", "<cbc:InvoiceTypeCode>99</cbc:InvoiceTypeCode>", 1), "INVALID_COMPROBANTE_KIND"},
		{"bad emitter RUC (short)", strings.Replace(validInvoice, "20100070970", "2010003920", 1), "INVALID_EMITTER_RUC"},
		{"three decimals", strings.Replace(validInvoice, "1284.30", "1284.301", 1), "INVALID_COMPROBANTE_TOTAL"},
		{"negative total", strings.Replace(validInvoice, "1284.30", "-1284.30", 1), "INVALID_COMPROBANTE_TOTAL"},
		{"non-numeric total", strings.Replace(validInvoice, "1284.30", "abc", 1), "INVALID_COMPROBANTE_TOTAL"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseComprobanteXML([]byte(c.xml))
			if err == nil || !strings.HasPrefix(err.Error(), c.want) {
				t.Fatalf("err = %v, want prefix %s", err, c.want)
			}
		})
	}
}

func TestParseCDRXML(t *testing.T) {
	cdr := `<?xml version="1.0"?>
<ApplicationResponse xmlns:cac="urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2" xmlns:cbc="urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2">
  <cbc:ResponseCode>0</cbc:ResponseCode>
  <cbc:Description>La Factura numero F001-948, ha sido aceptada</cbc:Description>
</ApplicationResponse>`
	c, err := ParseCDRXML([]byte(cdr))
	if err != nil {
		t.Fatalf("parse CDR: %v", err)
	}
	if c.ResponseCode != "0" || c.DocumentID != "F001-948" {
		t.Fatalf("CDR = %+v, want response 0 + doc F001-948", c)
	}
	if _, err := ParseCDRXML([]byte("<x/>")); err == nil {
		t.Fatal("missing ResponseCode must fail closed")
	}
}
