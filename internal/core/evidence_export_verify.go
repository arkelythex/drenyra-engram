// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money; version/sequence numbers are JSON integers,
// never floats. This module is the PURE OFFLINE VERIFIER of the deterministic
// evidence-lifecycle export bundle (docs/architecture/evidence-lifecycle-v0.8.md
// §12; WU-2 — the core-only verifier; the HTTP/CLI/MCP transports are deferred
// to the transport work — the report contract below is the machine-readable
// surface those transports consume later):
//
//   - the verifier is READ-ONLY and PURE: it takes a serialized or in-memory
//     EvidenceExportBundle and re-runs the FULL fail-closed validation
//     (AssertValidEvidenceExportBundle — version, criteria, scope coverage,
//     content hash, envelope hash, content-addressed exportId, counts, object
//     metadata and signing-key coverage) and then verifies every receipt
//     offline against the EXPORTED signing keys and the per-subject chain
//     links — no store, no keyring, no network, no clock;
//   - every receipt is checked through the SAME six pure layers as the store
//     verifier (payload canonicalization, envelope integrity, signature,
//     signing-key validity, tenant/company scope, chain link) using the
//     exported public keys — a tampered receipt (envelope field, payload
//     bytes, signature, key row or chain link) fails closed even when the
//     hashes were re-committed by the tamperer;
//   - byte-state consistency is validated per object against the immutable
//     execution ledger INSIDE the bundle: "purged" requires a completed
//     purge execution (and, when a signer exists, the purge_executed
//     completion receipt resolved in the bundle), "intended" requires a
//     durable intent (intent/interrupted execution) WITHOUT any completion,
//     and "stored" must not claim any purge execution at all;
//   - the verifier fails closed on ANY tampered row/key/receipt/scope/order/
//     hash: the report outcome is "passed" ONLY when every applicable layer
//     passes; a skipped layer (no receipts, no objects) never fails the
//     report — an empty-but-valid export and a no-signer-store export are
//     legitimate bundles;
//   - the report is a deterministic, machine-readable JSON document: the
//     frozen version, the content-addressed exportId, the outcome, the eight
//     top-level layers in fixed order (bundle structural validity, byte-state
//     consistency, then the six receipt layers aggregated) and the
//     per-receipt/per-object diagnostics. It ends with the exact conclusion
//     `Accounting correctness: NOT ASSERTED` — verification never asserts
//     accounting correctness.
//
// This module is PURE: no I/O, no store, no clock. It reuses the export
// canonicalizers and the receipt verification layers from evidence_export.go,
// receipt.go and verify.go.
package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// Stable top-level layer names of the export verifier (the receipt layers reuse
// the frozen names of ReceiptLayerNames — payload canonicalization, envelope
// integrity, signature, signing-key validity, tenant/company scope, chain link).
const (
	// LayerExportBundleStructural is the first top-level layer: version,
	// criteria, fail-closed scope coverage, content hash, envelope hash,
	// content-addressed exportId, counts, object metadata, byte-state tokens
	// and signing-key coverage (the full AssertValidEvidenceExportBundle run).
	LayerExportBundleStructural = "bundle structural validity"
	// LayerExportByteStateConsistency is the second top-level layer: the
	// per-object byte-state token vs the immutable execution ledger of the
	// bundle (stored/intended/purged consistency, completion receipt anchors).
	LayerExportByteStateConsistency = "byte-state consistency"
)

// EvidenceExportReceiptVerification is the per-receipt diagnostic block of the
// export verifier: the propagated chain ordinal (the store's emission position
// — the canonical sort tie-break), the stored receipt hash, the covered action
// and the six receipt-layer instances in the frozen ReceiptLayerNames order.
type EvidenceExportReceiptVerification struct {
	ChainOrdinal int                 `json:"chainOrdinal"`
	ReceiptHash  string              `json:"receiptHash"`
	Action       ReceiptAction       `json:"action"`
	Layers       []VerificationLayer `json:"layers"`
}

// EvidenceExportObjectVerification is the per-object diagnostic block of the
// export verifier: the object id, the claimed byte-state token and the
// byte-state consistency layer instance.
type EvidenceExportObjectVerification struct {
	ObjectID   string              `json:"objectId"`
	BytesState EvidenceBytesState  `json:"bytes"`
	Layers     []VerificationLayer `json:"layers"`
}

// EvidenceExportVerificationReport is the machine-readable result of
// VerifyEvidenceExportBundle: the frozen version, the content-addressed
// exportId, the fail-closed outcome and the eight deterministic top-level
// layers (bundle structural validity, byte-state consistency, then the six
// aggregated receipt layers in the frozen order) plus the per-receipt and
// per-object diagnostics. The outcome is "passed" ONLY when every applicable
// layer passes; a skipped layer (inapplicable) never fails the report. The
// report always ends with `Accounting correctness: NOT ASSERTED` (verification
// establishes integrity, signer possession, scope and chain continuity — never
// accounting correctness). The field order IS the canonical JSON property
// order — the stable shape a CLI/MCP transport serializes later.
type EvidenceExportVerificationReport struct {
	Version               string                              `json:"version"`
	ExportID              string                              `json:"exportId"`
	Outcome               VerificationOutcome                 `json:"outcome"`
	Layers                []VerificationLayer                 `json:"layers"`
	Receipts              []EvidenceExportReceiptVerification `json:"receipts"`
	Objects               []EvidenceExportObjectVerification  `json:"objects"`
	AccountingCorrectness string                              `json:"accountingCorrectness"`
}

// VerifyEvidenceExportBundleJSON strict-decodes exactly ONE
// EvidenceExportBundle from serialized JSON (unknown fields and trailing data
// are rejected — a malformed document is a transport error, never a silent
// skip) and runs the full offline verification. The verifier works on the
// decoded MODEL, not on the raw wire bytes: any valid JSON encoding of the
// same bundle (alternate whitespace/indentation) verifies identically — the
// canonical wire bytes are the store's transport form, the hashes commit to
// the canonical model bytes. The returned report carries the verification
// result (a decode-valid bundle that is self-inconsistent yields a FAILED
// report, not an error).
func VerifyEvidenceExportBundleJSON(data []byte) (EvidenceExportVerificationReport, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var b EvidenceExportBundle
	if err := dec.Decode(&b); err != nil {
		return EvidenceExportVerificationReport{}, fmt.Errorf("INVALID_EXPORT_BUNDLE: input is not a valid EvidenceExportBundle JSON document: %w", err)
	}
	if dec.More() {
		return EvidenceExportVerificationReport{}, errors.New("INVALID_EXPORT_BUNDLE: trailing data after the bundle object")
	}
	return VerifyEvidenceExportBundle(b), nil
}

// VerifyEvidenceExportBundle is the PURE offline verifier of an
// evidence-export/v0.8.0 bundle. It never mutates the caller's bundle (a
// defensive canonicalization normalizes a COPY so every check walks the same
// deterministic bytes the hashes commit to — permuted source array order
// canonicalizes away by design, while a changed chain order or any row-content
// change re-hashes and fails closed).
//
// Checks, in report order:
//
//  1. bundle structural validity — the complete AssertValidEvidenceExportBundle
//     re-run: frozen version, criteria, fail-closed scope coverage (no row
//     crosses tenant/company/RUC/period), content hash over the canonical
//     content (including the chain-ordinal commitment), envelope hash,
//     content-addressed exportId, exact counts, valid object metadata with a
//     closed byte-state token and full signing-key coverage;
//  2. byte-state consistency — per object, the byte-state token vs the
//     immutable execution ledger of the bundle: "purged" requires a completed
//     purge execution whose completion receipt (when a signer exists) matches
//     the object's purgeExecutedReceiptHash and resolves to the exported
//     purge_executed receipt; "intended" requires a durable intent
//     (intent/interrupted execution) and NO completion; "stored" must not
//     carry ANY purge execution row;
//  3. the six receipt layers aggregated (payload canonicalization, envelope
//     integrity, signature, signing-key validity, tenant/company scope, chain
//     link) — every receipt verified offline against the EXPORTED public
//     signing key and the per-subject chain links in canonical chain order
//     (genesis must carry an empty previousReceiptHash; every later receipt
//     must chain on the immediately preceding recomputed digest).
//
// The outcome is "passed" ONLY when every applicable layer passes; a skipped
// layer never fails the report (a bundle with no receipts — a no-signer
// store — and an empty bundle are legitimate). The report ends with
// `Accounting correctness: NOT ASSERTED`.
func VerifyEvidenceExportBundle(b EvidenceExportBundle) EvidenceExportVerificationReport {
	// Defensive canonical copy — the verifier never mutates the caller's bundle
	// and normalizes array order so every check walks the identical bytes the
	// hashes commit to (idempotent for an already-canonical store bundle).
	CanonicalizeEvidenceExportBundle(&b)

	report := EvidenceExportVerificationReport{
		Version:  b.Manifest.Version,
		ExportID: b.Manifest.ExportID,
		Outcome:  VerificationOutcomePassed,
		Layers:   make([]VerificationLayer, 0, 8),
		Receipts: make([]EvidenceExportReceiptVerification, 0, len(b.Receipts)),
		Objects:  make([]EvidenceExportObjectVerification, 0, len(b.Objects)),
	}

	// 1. Bundle structural validity — version, criteria, fail-closed scope
	// coverage, content/envelope hashes, exportId, counts, object metadata,
	// byte-state tokens and signing-key coverage (the full re-run of the
	// fail-closed bundle validator).
	if err := AssertValidEvidenceExportBundle(b); err != nil {
		report.Layers = append(report.Layers, layerFailed(LayerExportBundleStructural, err.Error()))
	} else {
		report.Layers = append(report.Layers, layerPassed(LayerExportBundleStructural,
			"bundle structural validity: version, criteria, scope coverage, content/envelope hashes, exportId, counts and signing-key coverage all match"))
	}

	// 2. Byte-state consistency — per object, the byte-state token vs the
	// immutable execution ledger carried INSIDE the bundle: "purged" requires a
	// completed purge execution (and, when a signer exists, the purge_executed
	// completion receipt resolved in the bundle), "intended" requires a durable
	// intent WITHOUT any completion, "stored" must not claim any purge
	// execution at all.
	execsByObject := make(map[string][]EvidencePurgeExecution, len(b.PurgeExecutions))
	for _, e := range b.PurgeExecutions {
		execsByObject[e.ObjectID] = append(execsByObject[e.ObjectID], e)
	}
	receiptsByHash := make(map[string]EvidenceExportReceipt, len(b.Receipts))
	for _, r := range b.Receipts {
		receiptsByHash[r.ReceiptHash] = r
	}
	byteStateLayers := make([]VerificationLayer, 0, len(b.Objects))
	for _, o := range b.Objects {
		l := exportByteStateLayer(o, execsByObject[o.Object.ObjectID], receiptsByHash)
		byteStateLayers = append(byteStateLayers, l)
		report.Objects = append(report.Objects, EvidenceExportObjectVerification{
			ObjectID:   o.Object.ObjectID,
			BytesState: o.BytesState,
			Layers:     []VerificationLayer{l},
		})
	}
	report.Layers = append(report.Layers, aggregateExportByteState(byteStateLayers))

	// 3. Receipt verification — the six pure layers over every exported receipt,
	// using ONLY the exported public signing keys, and the per-subject chain
	// links in canonical chain order. b.Receipts is canonical here (per-subject
	// contiguous), so a single walk resets the computed predecessor whenever the
	// subject changes.
	keysByID := make(map[string]EvidenceExportSigningKey, len(b.SigningKeys))
	for _, k := range b.SigningKeys {
		keysByID[k.KeyID] = k
	}
	objectScopes := make(map[string]SubjectScope, len(b.Objects))
	for _, o := range b.Objects {
		objectScopes[o.Object.ObjectID] = SubjectScope{
			TenantID:       o.Object.TenantID,
			CompanyID:      o.Object.CompanyID,
			FiscalPeriodID: o.Object.Period,
		}
	}
	perReceipt := make([][]VerificationLayer, 0, len(b.Receipts))
	prevSubject := ""
	prevComputed := ""
	chainIndex := 0
	for _, r := range b.Receipts {
		if r.Receipt.SubjectID != prevSubject {
			prevSubject = r.Receipt.SubjectID
			prevComputed = ""
			chainIndex = 0
		}
		subjectScope, subjectFound := objectScopes[r.Receipt.SubjectID]
		key, keyFound := keysByID[r.Receipt.KeyID]
		var keyPtr *EvidenceExportSigningKey
		if keyFound {
			keyPtr = &key
		}
		perReceipt = append(perReceipt, verifyExportReceipt(r, keyPtr, subjectScope, subjectFound, prevComputed, chainIndex > 0))
		prevComputed = ReceiptHash(r.Receipt)
		chainIndex++
	}
	for col, name := range ReceiptLayerNames() {
		instances := make([]VerificationLayer, len(perReceipt))
		for i := range perReceipt {
			instances[i] = perReceipt[i][col]
		}
		report.Layers = append(report.Layers, AggregateLayers(name, instances))
	}
	for i, r := range b.Receipts {
		report.Receipts = append(report.Receipts, EvidenceExportReceiptVerification{
			ChainOrdinal: r.ChainOrdinal,
			ReceiptHash:  r.ReceiptHash,
			Action:       r.Receipt.Action,
			Layers:       perReceipt[i],
		})
	}

	// Fail closed: the outcome is "passed" ONLY when every applicable layer
	// passes; the accounting-correctness conclusion is always stamped LAST.
	for _, l := range report.Layers {
		if l.Status == VerificationFailed {
			report.Outcome = VerificationOutcomeFailed
			break
		}
	}
	report.AccountingCorrectness = AccountingCorrectnessNotAsserted
	return report
}

// verifyExportReceipt runs the six pure receipt layers of ONE exported receipt
// in the frozen ReceiptLayerNames order: payload canonicalization, envelope
// integrity, signature, signing-key validity, tenant/company scope and chain
// link. key is nil when the receipt's key has no exported row (the
// signing-key layer fails and the signature layer is skipped — the codebase's
// dependency-blocked pattern). A payload that fails strict decoding fails the
// payload layer and skips its payload-dependent layers (envelope integrity,
// tenant/company scope) with the prerequisite named — signature, signing-key
// validity and chain link still run. subjectFound is false when the receipt's
// subject is not an evidence object of the bundle (the scope layer fails —
// the scope cannot be anchored).
func verifyExportReceipt(r EvidenceExportReceipt, key *EvidenceExportSigningKey, subject SubjectScope, subjectFound bool, prevComputed string, predecessorResolved bool) []VerificationLayer {
	payload, payloadErr := DecodeStoredPayload(r.PayloadJSON)

	var payloadLayer, envelopeLayer, scopeLayer VerificationLayer
	if payloadErr != nil {
		payloadLayer = layerFailed(LayerPayloadCanonicalization, payloadErr.Error())
		envelopeLayer = layerSkipped(LayerEnvelopeIntegrity, "skipped: prerequisite 'payload canonicalization' failed (the stored payload is not a valid canonical receipt payload)")
		scopeLayer = layerSkipped(LayerTenantCompanyScope, "skipped: prerequisite 'payload canonicalization' failed (the stored payload is not a valid canonical receipt payload)")
	} else {
		payloadLayer = VerifyPayloadCanonicalization(r.PayloadJSON, payload, r.Receipt)
		envelopeLayer = VerifyEnvelopeIntegrity(r.Receipt, payload, r.ReceiptHash)
		if subjectFound {
			scopeLayer = VerifyTenantCompanyScope(r.Receipt, payload, subject)
		} else {
			scopeLayer = layerFailed(LayerTenantCompanyScope, fmt.Sprintf("receipt subject %s is not an evidence object of the bundle (scope cannot be anchored)", r.Receipt.SubjectID))
		}
	}

	keyLayer, rawKey := exportSigningKeyLayer(key, r.Receipt)

	return []VerificationLayer{
		payloadLayer,
		envelopeLayer,
		VerifySignature(r.Receipt, rawKey),
		keyLayer,
		scopeLayer,
		VerifyChainLink(r.Receipt, prevComputed, predecessorResolved),
	}
}

// exportSigningKeyLayer resolves the exported public-key row of a receipt: a
// missing row fails the signing-key layer (an unverifiable chain — the
// structural validator already refuses to export one, defense in depth) and
// returns a nil raw key so the signature layer skips with the prerequisite
// named; a present row runs the pure signing-key validity layer and returns
// the decoded raw Ed25519 key material.
func exportSigningKeyLayer(key *EvidenceExportSigningKey, receipt SignedReceipt) (VerificationLayer, []byte) {
	if key == nil {
		return VerifySigningKeyValidity(SigningKey{Found: false}, receipt), nil
	}
	sk := SigningKey{
		Found:     true,
		Algorithm: key.Algorithm,
		PublicKey: key.PublicKey,
		CreatedAt: key.CreatedAt,
		RevokedAt: key.RevokedAt,
	}
	return VerifySigningKeyValidity(sk, receipt), DecodeSigningPublicKey(sk)
}

// exportByteStateLayer validates ONE object's byte-state token against the
// immutable execution ledger carried INSIDE the bundle (fail closed):
//
//   - "stored": NO purge execution row may exist for the object (any execution
//     row — completed, intent or interrupted — would claim a purge the honest
//     state never claims) and no completion receipt hash may be carried;
//   - "purged": a COMPLETED purge execution must exist (the completion
//     evidence), the object's purgeExecutedReceiptHash must equal the completed
//     execution's completion receipt id (both empty only for a no-signer
//     store), and — when a signer exists — the completion hash must resolve to
//     the exported purge_executed receipt of the object (the design §12 audit
//     anchor is part of the bundle's complete chains);
//   - "intended": a durable purge intent (an intent or interrupted execution
//     row) must exist WITHOUT any completed execution, and no completion
//     receipt hash may be carried (no completion exists — a crash-intent is
//     never presented as stored bytes);
//   - an unknown token fails closed (the structural layer also fails it).
func exportByteStateLayer(entry EvidenceObjectExport, execs []EvidencePurgeExecution, receiptsByHash map[string]EvidenceExportReceipt) VerificationLayer {
	name := LayerExportByteStateConsistency
	switch entry.BytesState {
	case EvidenceBytesStored:
		if len(execs) != 0 {
			return layerFailed(name, fmt.Sprintf("object %s claims bytes %q but %d purge execution row(s) exist in the bundle — a completed or intended purge is claimed by the ledger",
				entry.Object.ObjectID, entry.BytesState, len(execs)))
		}
		if entry.PurgeExecutedReceiptHash != "" {
			return layerFailed(name, fmt.Sprintf("stored object %s must not carry a purgeExecutedReceiptHash (no purge execution exists)", entry.Object.ObjectID))
		}
		return layerPassed(name, fmt.Sprintf("object %s bytes %q — no purge execution row exists (bytes expected present at the content address, never verified)", entry.Object.ObjectID, entry.BytesState))
	case EvidenceBytesPurged:
		var completed *EvidencePurgeExecution
		for i := range execs {
			if execs[i].State == PurgeExecutionCompleted {
				completed = &execs[i]
				break
			}
		}
		if completed == nil {
			return layerFailed(name, fmt.Sprintf("object %s claims bytes %q but no completed purge execution exists in the bundle (the completion evidence is absent)", entry.Object.ObjectID, entry.BytesState))
		}
		if entry.PurgeExecutedReceiptHash != completed.CompletionReceiptID {
			return layerFailed(name, fmt.Sprintf("object %s purgeExecutedReceiptHash %q differs from the completed execution %s completion receipt %q",
				entry.Object.ObjectID, entry.PurgeExecutedReceiptHash, completed.ExecutionID, completed.CompletionReceiptID))
		}
		if entry.PurgeExecutedReceiptHash != "" {
			rec, ok := receiptsByHash[entry.PurgeExecutedReceiptHash]
			if !ok {
				return layerFailed(name, fmt.Sprintf("object %s completion receipt %s is not exported (the purge_executed receipt must be part of the bundle)", entry.Object.ObjectID, entry.PurgeExecutedReceiptHash))
			}
			if rec.Receipt.SubjectID != entry.Object.ObjectID || rec.Receipt.Action != ReceiptActionPurgeExecuted {
				return layerFailed(name, fmt.Sprintf("object %s completion receipt %s does not resolve to its purge_executed receipt (the bundle row is subject %s, action %s)",
					entry.Object.ObjectID, entry.PurgeExecutedReceiptHash, rec.Receipt.SubjectID, rec.Receipt.Action))
			}
		}
		return layerPassed(name, fmt.Sprintf("object %s bytes %q — a completed execution removed the bytes and the completion receipt evidence is consistent", entry.Object.ObjectID, entry.BytesState))
	case EvidenceBytesIntended:
		if entry.PurgeExecutedReceiptHash != "" {
			return layerFailed(name, fmt.Sprintf("intended object %s must not carry a purgeExecutedReceiptHash (no completion exists)", entry.Object.ObjectID))
		}
		hasIntent := false
		for _, e := range execs {
			if e.State == PurgeExecutionCompleted {
				return layerFailed(name, fmt.Sprintf("object %s claims bytes %q but execution %s completed — the bytes were removed (purged, never intended)", entry.Object.ObjectID, entry.BytesState, e.ExecutionID))
			}
			if e.State == PurgeExecutionIntent || e.State == PurgeExecutionInterrupted {
				hasIntent = true
			}
		}
		if !hasIntent {
			return layerFailed(name, fmt.Sprintf("object %s claims bytes %q but no durable purge intent (intent/interrupted execution row) exists in the bundle", entry.Object.ObjectID, entry.BytesState))
		}
		return layerPassed(name, fmt.Sprintf("object %s bytes %q — a receipt-covered purge intent committed but no completion exists (crash-intent; never presented as ordinary stored bytes)", entry.Object.ObjectID, entry.BytesState))
	default:
		return layerFailed(name, fmt.Sprintf("object %s carries an unknown byte-state token %q (fail closed)", entry.Object.ObjectID, entry.BytesState))
	}
}

// aggregateExportByteState aggregates the per-object byte-state instances into
// the top-level byte-state layer: failed if any object fails (the first failed
// detail is kept — deterministic because objects are canonicalized), skipped
// when there are no objects, otherwise passed with the object count.
func aggregateExportByteState(layers []VerificationLayer) VerificationLayer {
	if len(layers) == 0 {
		return layerSkipped(LayerExportByteStateConsistency, "inapplicable")
	}
	if len(layers) == 1 {
		out := layers[0]
		out.Name = LayerExportByteStateConsistency
		return out
	}
	for _, l := range layers {
		if l.Status == VerificationFailed {
			return layerFailed(LayerExportByteStateConsistency, l.Detail)
		}
	}
	return layerPassed(LayerExportByteStateConsistency, fmt.Sprintf("all %d objects carry a consistent byte state", len(layers)))
}
