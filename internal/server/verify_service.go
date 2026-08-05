// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the OFFLINE verification
// orchestration service (v0.4.0 Step 4 — docs/architecture/
// offline-verification-step4.md §5). All PURE layer semantics and the report
// contract live in internal/core/verify.go; this file ONLY loads subjects,
// receipts, keys, provenance and current links through a narrow read interface
// and runs the pure layers in the stable order.
//
// Verification is READ-ONLY: no transaction is ever started, no row is ever
// written, and a failed layer is evidence, never a store error. Errors here
// mean the report cannot be built (subject/target not found, malformed target,
// database/query/decode failures, corruption) — a verifiable-but-failed
// subject ALWAYS yields a report ending with
// `Accounting correctness: NOT ASSERTED`.
package server

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

// VerificationStore is the narrow read surface the offline verification engine
// needs (design §4/§5). SQLiteStore satisfies it; every method is read-only
// and never starts a transaction.
type VerificationStore interface {
	ReceiptsForSubject(ctx context.Context, subjectType core.SubjectType, subjectID string) ([]core.SignedReceipt, error)
	ReceiptByHash(ctx context.Context, receiptHash string) (core.SignedReceipt, error)
	ReceiptByID(ctx context.Context, id int64) (core.SignedReceipt, error)
	ReceiptPayloadByHash(ctx context.Context, receiptHash string) (payloadJSON string, storedHash string, rowID int64, err error)
	ReceiptActProvenance(ctx context.Context, subjectType core.SubjectType, subjectID string, action core.ReceiptAction, issuedAt string) (core.ActProvenance, bool, error)
	EvidenceLinkRefs(ctx context.Context, memoryID string) ([]string, error)
	RuleLinkRefs(ctx context.Context, memoryID string) ([]string, error)
	SigningKeyForVerify(ctx context.Context, keyID string) (store.SigningKeyRecord, error)
	FindByID(id string) (core.AccountingMemory, bool)
	GetJudgment(ctx context.Context, id string) (core.AccountingJudgment, bool)
	SuccessorOf(memoryID string) (core.AccountingMemory, bool)
	JudgmentSuccessorOf(ctx context.Context, judgmentID string) (core.AccountingJudgment, bool)
}

// Sentinels distinguish reportable verification failures (exit 1 evidence)
// from report-building failures (exit 2 in the CLI): a layer failure is
// EVIDENCE, never one of these errors.
var (
	// ErrSubjectNotFound means the subject row does not exist.
	ErrSubjectNotFound = errors.New("verification: subject not found")
	// ErrNoReceipts means the subject exists but has no signed receipt chain
	// (e.g. an imported memory that never minted local receipts).
	ErrNoReceipts = errors.New("verification: subject has no signed receipts")
	// ErrInvalidReceiptTarget means the standalone target did not identify
	// exactly one receipt (neither/both of hash and id, or a malformed hash).
	ErrInvalidReceiptTarget = errors.New("verification: invalid receipt target")
)

// VerifyMemory verifies the FULL signed chain of one memory subject (design
// §5): the six receipt layers over every receipt ordered by issued_at, then
// principal provenance, supersession chain, evidence availability and rule
// availability.
func VerifyMemory(ctx context.Context, st VerificationStore, memoryID string) (core.VerificationReport, error) {
	memory, ok := st.FindByID(memoryID)
	if !ok {
		return core.VerificationReport{}, fmt.Errorf("%w: memory %s", ErrSubjectNotFound, memoryID)
	}
	scope := core.SubjectScope{
		TenantID:       memory.Scope.OrganizationID,
		CompanyID:      memory.Scope.CompanyID,
		FiscalPeriodID: memory.Scope.Period,
	}
	report, payloads, err := verifyReceiptLayers(ctx, st, core.SubjectTypeMemory, memoryID, scope)
	if err != nil {
		return core.VerificationReport{}, err
	}

	// 7. principal provenance — every covered act matches its immutable event.
	provInstances := make([]core.VerificationLayer, len(payloads))
	for i, p := range payloads {
		provInstances[i] = provenanceLayer(ctx, st, core.SubjectTypeMemory, memoryID, p)
	}
	report.Layers = append(report.Layers, core.AggregateLayers(core.LayerPrincipalProvenance, provInstances))

	// 8. supersession chain — walk SuccessorOf until current.
	report.Layers = append(report.Layers, memorySupersessionLayer(st, memory))

	// 9. evidence availability — every declared ref row-backed + head envelope.
	evidenceLinks, err := st.EvidenceLinkRefs(ctx, memoryID)
	if err != nil {
		return core.VerificationReport{}, fmt.Errorf("verify memory %s: read evidence links: %w", memoryID, err)
	}
	report.Layers = append(report.Layers, core.VerifyEvidenceAvailability(
		declaredEvidenceRefs(payloads), evidenceLinks,
		core.ComputeEnvelopeHash(memory), latestCommittedEnvelope(payloads),
	))

	// 10. rule availability — dynamic refs row-backed + head envelope.
	ruleLinks, err := st.RuleLinkRefs(ctx, memoryID)
	if err != nil {
		return core.VerificationReport{}, fmt.Errorf("verify memory %s: read rule links: %w", memoryID, err)
	}
	report.Layers = append(report.Layers, core.VerifyRuleAvailability(
		setDifference(memory.RuleRefs, ruleLinks), ruleLinks, memory.RuleRefs,
		core.ComputeEnvelopeHash(memory), latestCommittedEnvelope(payloads),
	))

	core.Finalize(&report)
	return report, nil
}

// VerifyJudgment verifies the FULL signed chain of one judgment subject (design
// §5): the six receipt layers, then principal provenance, judgment hash and
// supersession chain.
func VerifyJudgment(ctx context.Context, st VerificationStore, judgmentID string) (core.VerificationReport, error) {
	judgment, ok := st.GetJudgment(ctx, judgmentID)
	if !ok {
		return core.VerificationReport{}, fmt.Errorf("%w: judgment %s", ErrSubjectNotFound, judgmentID)
	}
	scope := core.SubjectScope{
		TenantID:       judgment.TenantID,
		CompanyID:      judgment.CompanyID,
		FiscalPeriodID: judgment.FiscalPeriodID,
	}
	report, payloads, err := verifyReceiptLayers(ctx, st, core.SubjectTypeJudgment, judgmentID, scope)
	if err != nil {
		return core.VerificationReport{}, err
	}

	// 7. principal provenance — every decision receipt matches its immutable
	// event.
	provInstances := make([]core.VerificationLayer, len(payloads))
	for i, p := range payloads {
		provInstances[i] = provenanceLayer(ctx, st, core.SubjectTypeJudgment, judgmentID, p)
	}
	report.Layers = append(report.Layers, core.AggregateLayers(core.LayerPrincipalProvenance, provInstances))

	// 8. judgment hash — recompute the current row vs the latest decision
	// receipt and the immutable decision event.
	report.Layers = append(report.Layers, judgmentHashLayer(ctx, st, judgment, payloads))

	// 9. supersession chain — walk JudgmentSuccessorOf until current.
	report.Layers = append(report.Layers, judgmentSupersessionLayer(ctx, st, judgment))

	core.Finalize(&report)
	return report, nil
}

// VerifyReceipt verifies ONE selected receipt and its predecessor link (design
// §5): the six receipt layers over the single receipt. The FK-backed subject
// is loaded so tenant/company scope is never mere self-consistency; a
// non-genesis predecessor must resolve by hash and share the subject type/id.
func VerifyReceipt(ctx context.Context, st VerificationStore, target core.ReceiptTarget) (core.VerificationReport, error) {
	if (target.Hash == "") == (target.ID == 0) {
		return core.VerificationReport{}, fmt.Errorf("%w: exactly one of hash or id must identify the receipt", ErrInvalidReceiptTarget)
	}
	var (
		receipt core.SignedReceipt
		err     error
	)
	if target.Hash != "" {
		if !isReceiptHash(target.Hash) {
			return core.VerificationReport{}, fmt.Errorf("%w: hash %q is not 64 hex digits", ErrInvalidReceiptTarget, target.Hash)
		}
		receipt, err = st.ReceiptByHash(ctx, target.Hash)
	} else {
		receipt, err = st.ReceiptByID(ctx, target.ID)
	}
	if err != nil {
		return core.VerificationReport{}, fmt.Errorf("verify receipt: %w", err)
	}

	scope, err := subjectScopeOf(ctx, st, receipt)
	if err != nil {
		return core.VerificationReport{}, err
	}

	report := core.NewReport(receipt.SubjectType, receipt.SubjectID)

	payloadJSON, storedHash, _, err := st.ReceiptPayloadByHash(ctx, core.ReceiptHash(receipt))
	if err != nil {
		if errors.Is(err, store.ErrReceiptNotFound) {
			return core.VerificationReport{}, fmt.Errorf("verify receipt: stored receipt_hash differs from the recomputed digest (corruption): %w", err)
		}
		return core.VerificationReport{}, fmt.Errorf("verify receipt: read payload: %w", err)
	}
	payload, err := core.DecodeStoredPayload(payloadJSON)
	if err != nil {
		return core.VerificationReport{}, fmt.Errorf("verify receipt: %w", err)
	}
	keyRec, err := st.SigningKeyForVerify(ctx, receipt.KeyID)
	if err != nil {
		return core.VerificationReport{}, fmt.Errorf("verify receipt: resolve signing key %s: %w", receipt.KeyID, err)
	}
	key := core.SigningKey{
		Found:     keyRec.Found,
		Algorithm: keyRec.Algorithm,
		PublicKey: keyRec.PublicKey,
		CreatedAt: keyRec.CreatedAt,
		RevokedAt: keyRec.RevokedAt,
	}

	layers := []core.VerificationLayer{
		core.VerifyPayloadCanonicalization(payloadJSON, payload, receipt),
		core.VerifyEnvelopeIntegrity(receipt, payload, storedHash),
		core.VerifySignature(receipt, core.DecodeSigningPublicKey(key)),
		core.VerifySigningKeyValidity(key, receipt),
		core.VerifyTenantCompanyScope(receipt, payload, scope),
	}

	// Chain link: the standalone predecessor must resolve by hash and share the
	// subject type/id.
	prevComputed, predecessorResolved := "", false
	chainAppended := false
	if receipt.PreviousReceiptHash != "" {
		pred, perr := st.ReceiptByHash(ctx, receipt.PreviousReceiptHash)
		switch {
		case errors.Is(perr, store.ErrReceiptNotFound):
			// Unresolved — the pure layer reports the missing predecessor.
		case perr != nil:
			return core.VerificationReport{}, fmt.Errorf("verify receipt: resolve predecessor: %w", perr)
		default:
			if pred.SubjectType != receipt.SubjectType || pred.SubjectID != receipt.SubjectID {
				layers = append(layers, core.VerificationLayer{
					Name:   core.LayerChainLink,
					Status: core.VerificationFailed,
					Detail: fmt.Sprintf("predecessor %s resolves to a different subject (%s %s)", receipt.PreviousReceiptHash, pred.SubjectType, pred.SubjectID),
				})
				chainAppended = true
			} else {
				prevComputed = core.ReceiptHash(pred)
				predecessorResolved = true
			}
		}
	}
	if !chainAppended {
		layers = append(layers, core.VerifyChainLink(receipt, prevComputed, predecessorResolved))
	}

	for _, l := range layers {
		report.Layers = append(report.Layers, core.AggregateLayers(l.Name, []core.VerificationLayer{l}))
	}
	report.Receipts = append(report.Receipts, core.ReceiptVerification{
		ReceiptHash: storedHash,
		Action:      receipt.Action,
		Layers:      layers,
	})
	core.Finalize(report)
	return *report, nil
}

// verifyReceiptLayers runs the six pure receipt layers over the subject's full
// ordered chain and returns the report (per-receipt diagnostics + the six
// aggregate top-level layers) plus the decoded payloads the object layers
// need.
func verifyReceiptLayers(ctx context.Context, st VerificationStore, subjectType core.SubjectType, subjectID string, scope core.SubjectScope) (core.VerificationReport, []core.ReceiptPayload, error) {
	receipts, err := st.ReceiptsForSubject(ctx, subjectType, subjectID)
	if err != nil {
		return core.VerificationReport{}, nil, fmt.Errorf("verify %s %s: read receipts: %w", subjectType, subjectID, err)
	}
	if len(receipts) == 0 {
		return core.VerificationReport{}, nil, fmt.Errorf("%w: %s %s has no signed receipt chain", ErrNoReceipts, subjectType, subjectID)
	}

	report := core.NewReport(subjectType, subjectID)
	perReceipt := make([][]core.VerificationLayer, len(receipts))
	payloads := make([]core.ReceiptPayload, len(receipts))
	storedHashes := make([]string, len(receipts))

	for i, r := range receipts {
		hash := core.ReceiptHash(r)
		payloadJSON, storedHash, _, err := st.ReceiptPayloadByHash(ctx, hash)
		if err != nil {
			if errors.Is(err, store.ErrReceiptNotFound) {
				return core.VerificationReport{}, nil, fmt.Errorf("verify %s %s: receipt row at recomputed hash %s is missing — stored receipt_hash differs from the recomputed digest (corruption): %w", subjectType, subjectID, hash, err)
			}
			return core.VerificationReport{}, nil, fmt.Errorf("verify %s %s: read payload: %w", subjectType, subjectID, err)
		}
		payload, err := core.DecodeStoredPayload(payloadJSON)
		if err != nil {
			return core.VerificationReport{}, nil, fmt.Errorf("verify %s %s: %w", subjectType, subjectID, err)
		}
		keyRec, err := st.SigningKeyForVerify(ctx, r.KeyID)
		if err != nil {
			return core.VerificationReport{}, nil, fmt.Errorf("verify %s %s: resolve signing key %s: %w", subjectType, subjectID, r.KeyID, err)
		}
		key := core.SigningKey{
			Found:     keyRec.Found,
			Algorithm: keyRec.Algorithm,
			PublicKey: keyRec.PublicKey,
			CreatedAt: keyRec.CreatedAt,
			RevokedAt: keyRec.RevokedAt,
		}

		payloads[i] = payload
		storedHashes[i] = storedHash
		perReceipt[i] = []core.VerificationLayer{
			core.VerifyPayloadCanonicalization(payloadJSON, payload, r),
			core.VerifyEnvelopeIntegrity(r, payload, storedHash),
			core.VerifySignature(r, core.DecodeSigningPublicKey(key)),
			core.VerifySigningKeyValidity(key, r),
			core.VerifyTenantCompanyScope(r, payload, scope),
		}
	}

	// Chain link needs the immediately preceding COMPUTED hash (genesis empty).
	prevComputed := ""
	for i, r := range receipts {
		perReceipt[i] = append(perReceipt[i], core.VerifyChainLink(r, prevComputed, true))
		prevComputed = core.ReceiptHash(r)
	}

	for col, name := range core.ReceiptLayerNames() {
		instances := make([]core.VerificationLayer, len(receipts))
		for i := range receipts {
			instances[i] = perReceipt[i][col]
		}
		report.Layers = append(report.Layers, core.AggregateLayers(name, instances))
	}
	for i := range receipts {
		report.Receipts = append(report.Receipts, core.ReceiptVerification{
			ReceiptHash: storedHashes[i],
			Action:      receipts[i].Action,
			Layers:      perReceipt[i],
		})
	}
	return *report, payloads, nil
}

// provenanceLayer resolves the immutable event for one receipt and runs the
// pure principal-provenance layer; a missing event fails the layer (provenance
// continuity cannot be established) and an ambiguous resolution also fails it
// (corruption is evidence, not a skip).
func provenanceLayer(ctx context.Context, st VerificationStore, subjectType core.SubjectType, subjectID string, payload core.ReceiptPayload) core.VerificationLayer {
	act, found, err := st.ReceiptActProvenance(ctx, subjectType, subjectID, payload.Action, payload.IssuedAt)
	if err != nil {
		return core.VerificationLayer{
			Name:   core.LayerPrincipalProvenance,
			Status: core.VerificationFailed,
			Detail: "ambiguous provenance: " + err.Error(),
		}
	}
	if !found {
		return core.VerificationLayer{
			Name:   core.LayerPrincipalProvenance,
			Status: core.VerificationFailed,
			Detail: fmt.Sprintf("no immutable provenance record matches %s %s %s at %s", subjectType, subjectID, payload.Action, payload.IssuedAt),
		}
	}
	return core.VerifyPrincipalProvenance(payload, act)
}

// judgmentHashLayer recomputes the CURRENT judgment hash and compares it with
// the latest decision receipt's committed result and the immutable decision
// event's recorded hash (design §3). No decision receipt is a
// target-not-verifiable failure, never a successful skip.
func judgmentHashLayer(ctx context.Context, st VerificationStore, judgment core.AccountingJudgment, payloads []core.ReceiptPayload) core.VerificationLayer {
	var decision *core.ReceiptPayload
	for i := range payloads {
		p := &payloads[i]
		if p.Action == core.ReceiptActionRelationConfirmed || p.Action == core.ReceiptActionRelationRejected {
			decision = p
		}
	}
	if decision == nil {
		return core.VerificationLayer{
			Name:   core.LayerJudgmentHash,
			Status: core.VerificationFailed,
			Detail: "no decision receipt for the subject — target not verifiable",
		}
	}
	act, found, err := st.ReceiptActProvenance(ctx, core.SubjectTypeJudgment, judgment.ID, decision.Action, decision.IssuedAt)
	if err != nil {
		return core.VerificationLayer{
			Name:   core.LayerJudgmentHash,
			Status: core.VerificationFailed,
			Detail: "ambiguous provenance: " + err.Error(),
		}
	}
	if !found {
		return core.VerificationLayer{
			Name:   core.LayerJudgmentHash,
			Status: core.VerificationFailed,
			Detail: "no immutable decision event for the latest decision receipt",
		}
	}
	return core.VerifyJudgmentHash(core.ComputeJudgmentHash(judgment), decision.ResultingJudgmentHash, act.RecordedJudgmentHash)
}

// memorySupersessionLayer walks SuccessorOf from the subject memory until the
// terminal current row (the walk terminates on a missing successor or a
// repeated id; the pure layer classifies cycles, scope crossings and
// status/relation disagreements).
func memorySupersessionLayer(st VerificationStore, subject core.AccountingMemory) core.VerificationLayer {
	chainScope := memoryScope(subject)
	seen := make(map[string]bool)
	links := make([]core.SupersessionLink, 0, 2)
	cur := subject
	for {
		successor, ok := st.SuccessorOf(cur.Identity.ID)
		successorID := ""
		if ok {
			successorID = successor.Identity.ID
		}
		links = append(links, core.SupersessionLink{
			SubjectID:   cur.Identity.ID,
			SuccessorID: successorID,
			Superseded:  cur.Status == core.StatusSuperseded,
			Scope:       memoryScope(cur),
		})
		if !ok || seen[cur.Identity.ID] {
			break
		}
		seen[cur.Identity.ID] = true
		cur = successor
	}
	return core.VerifySupersessionChain(links, chainScope)
}

// judgmentSupersessionLayer walks JudgmentSuccessorOf from the subject judgment
// until the terminal current row.
func judgmentSupersessionLayer(ctx context.Context, st VerificationStore, subject core.AccountingJudgment) core.VerificationLayer {
	chainScope := judgmentScope(subject)
	seen := make(map[string]bool)
	links := make([]core.SupersessionLink, 0, 2)
	cur := subject
	for {
		successor, ok := st.JudgmentSuccessorOf(ctx, cur.ID)
		successorID := ""
		if ok {
			successorID = successor.ID
		}
		links = append(links, core.SupersessionLink{
			SubjectID:   cur.ID,
			SuccessorID: successorID,
			Superseded:  cur.Status == core.JudgmentSuperseded,
			Scope:       judgmentScope(cur),
		})
		if !ok || seen[cur.ID] {
			break
		}
		seen[cur.ID] = true
		cur = successor
	}
	return core.VerifySupersessionChain(links, chainScope)
}

func memoryScope(m core.AccountingMemory) core.SubjectScope {
	return core.SubjectScope{
		TenantID:       m.Scope.OrganizationID,
		CompanyID:      m.Scope.CompanyID,
		FiscalPeriodID: m.Scope.Period,
	}
}

func judgmentScope(j core.AccountingJudgment) core.SubjectScope {
	return core.SubjectScope{
		TenantID:       j.TenantID,
		CompanyID:      j.CompanyID,
		FiscalPeriodID: j.FiscalPeriodID,
	}
}

// subjectScopeOf loads the FK-backed subject of a standalone receipt so the
// tenant/company scope layer is anchored to the STORED subject, never mere
// self-consistency (design §3).
func subjectScopeOf(ctx context.Context, st VerificationStore, receipt core.SignedReceipt) (core.SubjectScope, error) {
	switch receipt.SubjectType {
	case core.SubjectTypeMemory:
		m, ok := st.FindByID(receipt.SubjectID)
		if !ok {
			return core.SubjectScope{}, fmt.Errorf("%w: memory %s", ErrSubjectNotFound, receipt.SubjectID)
		}
		return memoryScope(m), nil
	case core.SubjectTypeJudgment:
		j, ok := st.GetJudgment(ctx, receipt.SubjectID)
		if !ok {
			return core.SubjectScope{}, fmt.Errorf("%w: judgment %s", ErrSubjectNotFound, receipt.SubjectID)
		}
		return judgmentScope(j), nil
	default:
		return core.SubjectScope{}, fmt.Errorf("%w: unknown subjectType %q", ErrInvalidReceiptTarget, receipt.SubjectType)
	}
}

// declaredEvidenceRefs collects every non-empty evidenceRef the subject's
// receipts declared (deduplicated, order-preserving) — the immutable
// declaration set the evidence-availability layer requires to be row-backed.
func declaredEvidenceRefs(payloads []core.ReceiptPayload) []string {
	var out []string
	for _, p := range payloads {
		if p.EvidenceRef == "" || containsString(out, p.EvidenceRef) {
			continue
		}
		out = append(out, p.EvidenceRef)
	}
	return out
}

// latestCommittedEnvelope returns the envelope hash the subject's most recent
// receipt committed (resultingEnvelopeHash, or toEnvelopeHash for acts that
// commit it), scanning in issued_at ASC order so the LAST commit wins. Only
// the chain head's committed result is compared with current state — comparing
// historical results would falsely fail after legitimate later
// links/transitions (design §3).
func latestCommittedEnvelope(payloads []core.ReceiptPayload) string {
	var latest string
	for _, p := range payloads {
		switch {
		case p.ResultingEnvelopeHash != "":
			latest = p.ResultingEnvelopeHash
		case p.ToEnvelopeHash != "":
			latest = p.ToEnvelopeHash
		}
	}
	return latest
}

// setDifference returns the elements of merged that are NOT in linked — the
// immutable stored refs (the merged read view minus the current link rows).
func setDifference(merged, linked []string) []string {
	var out []string
	for _, ref := range merged {
		if !containsString(linked, ref) {
			out = append(out, ref)
		}
	}
	return out
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// isReceiptHash reports whether s is exactly 64 hex digits — the portable
// receipt identity (design §5).
func isReceiptHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	if strings.TrimSpace(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
