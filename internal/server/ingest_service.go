// Comprobante ingestion adapter — design brief §7.3.
//
// The ADAPTER CONTRACT for comprobante ingestion: parse an electronic invoice
// XML into the minimal reconstruction metadata, store its bytes as a WORM
// evidence object, and return the parsed metadata + the content address. The
// explicit NON-INTEGRATION boundary: this is MANUAL/offline ingestion — NO
// SUNAT credentials, NO retries, NO outage behavior, NO response retention, NO
// source authority. Production SUNAT/ERP integration is a Gate 0 decision and
// is NOT implemented here (§7.3).
package server

import (
	"context"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// IngestStore is the narrow surface the ingestion adapter delegates to.
type IngestStore interface {
	StoreObject(ctx context.Context, input core.ObjectStoreInput) (core.ObjectStoreResult, error)
}

// IngestComprobante parses ONE electronic invoice XML (UBL 2.1) and stores its
// bytes as a content-addressed WORM evidence object in the exact scope. The
// parsed metadata (kind, serie-numero, emitter RUC, issue date, total in whole
// cents, currency) rides the result; the object id IS the SHA-256 of the bytes
// (identical bytes → duplicate NO-OP). The XML/CDR are evidence, never
// accounting truth; the adapter never claims SUNAT integration.
func IngestComprobante(ctx context.Context, st IngestStore, scope core.Scope, source core.Source, bytes []byte) (core.Comprobante, core.ObjectStoreResult, error) {
	comp, err := core.ParseComprobanteXML(bytes)
	if err != nil {
		return core.Comprobante{}, core.ObjectStoreResult{}, err
	}
	result, err := st.StoreObject(ctx, core.ObjectStoreInput{
		Bytes:       bytes,
		ContentType: "application/xml",
		Scope:       scope,
		Source:      source,
	})
	if err != nil {
		return core.Comprobante{}, core.ObjectStoreResult{}, err
	}
	return comp, result, nil
}
