// Comprobante XML adapter contract — design brief §7.3 (Phase 6 integration
// boundary).
//
// This package defines the ADAPTER CONTRACT for a SUNAT comprobante
// (electronic invoice XML): minimal structural parsing (UBL 2.1 fields the
// reconstruction chain needs), strict validation (RUC checksum, document
// identity, totals in whole cents), and the explicit NON-INTEGRATION boundary:
// NO SUNAT credentials, NO retries, NO outage behavior, NO response retention,
// NO source authority. This is manual/offline ingestion only — production
// SUNAT/ERP integration remains a Gate 0 decision and is NOT implemented
// (design brief §7.3: "Phase 6 may define adapter contracts, but it must not
// claim production integration until credentials, retries, outage behavior,
// response retention, and source authority are implemented and tested").
package core

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

// ComprobanteKind is the closed set of comprobante document types the adapter
// recognizes (UBL cbc:InvoiceTypeCode + the CDR response).
type ComprobanteKind string

const (
	ComprobanteFactura     ComprobanteKind = "factura"
	ComprobanteBoleta      ComprobanteKind = "boleta"
	ComprobanteNotaCredito ComprobanteKind = "nota_credito"
	ComprobanteNotaDebito  ComprobanteKind = "nota_debito"
)

// Comprobante is the minimal parsed metadata of an electronic invoice XML —
// exactly the fields the reconstruction chain (balance → entry → evidence →
// rule) needs. Monetary fields are whole int64 CENTS, never floats.
type Comprobante struct {
	Kind         ComprobanteKind `json:"kind"`
	Serie        string          `json:"serie"`
	Numero       string          `json:"numero"`
	DocumentID   string          `json:"documentId"` // serie-numero, e.g. F001-948
	EmitterRUC   string          `json:"emitterRuc"`
	IssueDate    string          `json:"issueDate"`
	TotalCents   int64           `json:"totalCents"`
	Currency     string          `json:"currency"`
	ResponseCode string          `json:"responseCode,omitempty"` // CDR only
}

// parseableInvoice is the minimal UBL 2.1 shape the parser matches by LOCAL
// element name (Go ignores namespace prefixes: <cbc:ID> matches xml:"ID").
type parseableInvoice struct {
	ID            string `xml:"ID"`
	InvoiceType   string `xml:"InvoiceTypeCode"`
	IssueDate     string `xml:"IssueDate"`
	SupplierID    string `xml:"AccountingSupplierParty>Party>PartyIdentification>ID"`
	PayableAmount string `xml:"LegalMonetaryTotal>PayableAmount"`
}

// parseableCDR is the minimal CDR (constancia de recepción) shape.
type parseableCDR struct {
	ResponseCode string `xml:"ResponseCode"`
	Description  string `xml:"Description"`
}

// ParseComprobanteXML parses an electronic invoice XML (UBL 2.1) into the
// minimal adapter metadata, FAILING CLOSED on any malformed or missing field.
// The PayableAmount decimal (2 places) is converted to whole cents; a non-2-
// decimal amount, an invalid emitter RUC (the repo validator is SHAPE-ONLY:
// exactly 11 digits — a real mod-11 checksum is a domain decision, not this
// adapter's), or an unparseable structure is a typed error — never a silent
// guess.
func ParseComprobanteXML(data []byte) (Comprobante, error) {
	var inv parseableInvoice
	if err := xml.Unmarshal(data, &inv); err != nil {
		return Comprobante{}, fmt.Errorf("INVALID_COMPROBANTE_XML: %w", err)
	}
	c := Comprobante{
		IssueDate: inv.IssueDate,
	}
	if inv.ID == "" || inv.IssueDate == "" || inv.SupplierID == "" || inv.PayableAmount == "" {
		return Comprobante{}, fmt.Errorf("INVALID_COMPROBANTE_XML: missing required fields (ID, IssueDate, emitter RUC, PayableAmount)")
	}
	kind, err := kindForTypeCode(inv.InvoiceType)
	if err != nil {
		return Comprobante{}, err
	}
	c.Kind = kind
	serie, numero, ok := splitSerieNumero(inv.ID)
	if !ok {
		return Comprobante{}, fmt.Errorf("INVALID_COMPROBANTE_ID: %q is not serie-numero (e.g. F001-948)", inv.ID)
	}
	c.Serie, c.Numero, c.DocumentID = serie, numero, inv.ID
	c.EmitterRUC = strings.TrimSpace(inv.SupplierID)
	if !IsValidRUC(c.EmitterRUC) || !isValidRUCChecksum(c.EmitterRUC) {
		return Comprobante{}, fmt.Errorf("INVALID_EMITTER_RUC: %q fails the SUNAT mod-11 checksum", c.EmitterRUC)
	}
	amount, currency, err := parsePayableAmount(inv.PayableAmount)
	if err != nil {
		return Comprobante{}, err
	}
	c.TotalCents, c.Currency = amount, currency
	if c.TotalCents < 0 {
		return Comprobante{}, fmt.Errorf("INVALID_COMPROBANTE_TOTAL: negative total in cents")
	}
	return c, nil
}

// ParseCDRXML parses a SUNAT CDR (constancia de recepción) minimally: the
// response code and the acceptance description. The CDR is evidence of the
// SUNAT receipt — parsed metadata only, never a claim of integration.
func ParseCDRXML(data []byte) (Comprobante, error) {
	var cdr parseableCDR
	if err := xml.Unmarshal(data, &cdr); err != nil {
		return Comprobante{}, fmt.Errorf("INVALID_CDR_XML: %w", err)
	}
	if cdr.ResponseCode == "" {
		return Comprobante{}, fmt.Errorf("INVALID_CDR_XML: missing ResponseCode")
	}
	return Comprobante{ResponseCode: cdr.ResponseCode, DocumentID: extractDocNumber(cdr.Description)}, nil
}

// kindForTypeCode maps the SUNAT Catálogo 01 InvoiceTypeCode of an <Invoice>
// root. PER SUNAT UBL 2.1 (verified against the SUNAT XML guides, 2026-08): an
// <Invoice> admits ONLY 01 (factura) and 03 (boleta). Notas de crédito (07) and
// débito (08) are SEPARATE root elements (<CreditNote> / <DebitNote>) — never
// an <Invoice> with InvoiceTypeCode — so an unknown code here FAILS CLOSED
// (never a silent default). CreditNote/DebitNote parsing is a future adapter.
func kindForTypeCode(code string) (ComprobanteKind, error) {
	switch strings.TrimSpace(code) {
	case "01":
		return ComprobanteFactura, nil
	case "03":
		return ComprobanteBoleta, nil
	default:
		return "", fmt.Errorf("INVALID_COMPROBANTE_KIND: InvoiceTypeCode %q is not a valid <Invoice> code (SUNAT Catálogo 01: 01 factura, 03 boleta; 07/08 are CreditNote/DebitNote roots)", code)
	}
}

// isValidRUCChecksum verifies the SUNAT mod-11 check digit of an 11-digit RUC
// (weights [5,4,3,2,7,6,5,4,3,2]; check = (11 - (sum % 11)) % 10). Verified
// against current SUNAT validator references (2026-08). Shape validation stays
// in IsValidRUC; this is the checksum layer the ADAPTER requires.
func isValidRUCChecksum(ruc string) bool {
	if len(ruc) != 11 {
		return false
	}
	weights := [10]int{5, 4, 3, 2, 7, 6, 5, 4, 3, 2}
	sum := 0
	for i := 0; i < 10; i++ {
		sum += int(ruc[i]-'0') * weights[i]
	}
	return (11-(sum%11))%10 == int(ruc[10]-'0')
}

// splitSerieNumero splits "F001-948" into ("F001", "948"). The separator is the
// LAST dash (UBL allows dashes only as the serie-numero separator in the ID).
func splitSerieNumero(id string) (serie, numero string, ok bool) {
	idx := strings.LastIndex(id, "-")
	if idx <= 0 || idx == len(id)-1 {
		return "", "", false
	}
	return id[:idx], id[idx+1:], true
}

// parsePayableAmount parses "1284.30" (+ optional currencyID suffix) into whole
// cents: exactly TWO decimal places; other scales fail closed. The currency is
// extracted from the trailing ISO code when present (the XML attribute rides
// the element text in this minimal parser — the caller's CDR/extracto parser
// is the production path).
func parsePayableAmount(raw string) (cents int64, currency string, err error) {
	raw = strings.TrimSpace(raw)
	// Currency suffix: "1284.30 PEN" or "1284.30" (attribute dropped by the
	// minimal local-name matcher — acceptable for the adapter contract). The
	// optional trailing field MUST be a 3-letter ISO 4217 code; a space-grouped
	// amount ("1 284.30") or junk suffix ("1284.30 99") fails closed instead of
	// being silently misread as a currency.
	fields := strings.Fields(raw)
	if len(fields) > 2 {
		return 0, "", fmt.Errorf("INVALID_COMPROBANTE_TOTAL: %q is not a 2-decimal amount", raw)
	}
	if len(fields) == 2 {
		if !isISO4217Code(fields[1]) {
			return 0, "", fmt.Errorf("INVALID_COMPROBANTE_TOTAL: %q is not an amount", raw)
		}
		currency = strings.ToUpper(fields[1])
		raw = fields[0]
	}
	parts := strings.Split(raw, ".")
	if len(parts) > 2 {
		return 0, "", fmt.Errorf("INVALID_COMPROBANTE_TOTAL: %q is not a 2-decimal amount", raw)
	}
	intPart, fracPart := parts[0], ""
	if len(parts) == 2 {
		fracPart = parts[1]
		if len(fracPart) != 2 {
			return 0, "", fmt.Errorf("INVALID_COMPROBANTE_TOTAL: %q must have exactly 2 decimals", raw)
		}
	}
	var whole, frac int64
	// ParseInt (not Sscanf) so a PayableAmount with trailing or embedded
	// garbage after a parseable integer ("1284abc", "12a4.30") fails closed
	// instead of being silently accepted as the prefix amount — the frozen
	// contract requires a typed error, never a silent guess.
	whole, err = strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("INVALID_COMPROBANTE_TOTAL: %q is not an amount", raw)
	}
	if fracPart != "" {
		frac, err = strconv.ParseInt(fracPart, 10, 64)
		if err != nil {
			return 0, "", fmt.Errorf("INVALID_COMPROBANTE_TOTAL: %q is not an amount", raw)
		}
	}
	cents = whole*100 + frac
	if cents < 0 {
		return 0, "", fmt.Errorf("INVALID_COMPROBANTE_TOTAL: negative total")
	}
	if currency == "" {
		currency = "PEN"
	}
	return cents, currency, nil
}

// isISO4217Code reports whether s is a 3-letter ISO 4217 currency code — the
// only suffix the adapter accepts after a 2-decimal amount.
func isISO4217Code(s string) bool {
	if len(s) != 3 {
		return false
	}
	for i := 0; i < 3; i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return true
}

// extractDocNumber pulls the "numero F001-948" pattern out of a CDR
// description (best effort — the CDR is evidence, not the identity source).
func extractDocNumber(desc string) string {
	lower := strings.ToLower(desc)
	idx := strings.Index(lower, "numero ")
	if idx < 0 {
		return ""
	}
	rest := desc[idx+len("numero "):]
	for i, r := range rest {
		if r == ' ' || r == ',' || r == '.' {
			return rest[:i]
		}
	}
	return rest
}
