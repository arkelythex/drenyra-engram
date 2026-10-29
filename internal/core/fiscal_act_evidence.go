// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module implements the Slice 3
// "Immutable act evidence and envelope linkage" mechanism (design.md): a
// protected subject accumulates one immutable binding link per successful
// persisted act, and a v1-bound subject's envelope hash transitively commits
// to every link's binding hash and act-evidence hash. Legacy subjects (no
// links) contribute nothing, so every pre-existing envelope hash stays
// byte-identical (frozen receipt continuity — the sole mechanism permitted by
// design.md's hard stop).
package core

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

// FiscalBindingLinkContribution is the read-only, in-memory projection of one
// persisted fiscal_binding_links row that participates in ComputeEnvelopeHash.
// It carries no authority — it only lets the envelope hash commit to the
// exact, ordered sequence of immutable fiscal acts recorded against a
// subject.
type FiscalBindingLinkContribution struct {
	Sequence        int
	BindingHash     string
	ActEvidenceHash string
}

// FiscalWriteIntent carries the exact complete v1 binding a caller wants
// bound to one protected act. It participates in command INTENT only:
// authentication, membership, role, assurance, separation of duties and the
// closed-period gate remain independently and authoritatively enforced by
// the store. Supplying an intent never grants authority (design.md, proposal
// "Scope metadata does not authorize").
type FiscalWriteIntent struct {
	Binding FiscalScopeBinding
}

// DecodeFiscalWriteIntentJSON is the SINGLE seam every adapter uses to turn
// caller-supplied binding JSON into the pure intent value: the CLI's
// --fiscal-scope file, the HTTP body's fiscalScope member and the MCP tool's
// fiscalScope argument all land here, so none of them can grow a private,
// weaker parser or forget to run ValidateFiscalScopeBinding (which
// DecodeFiscalScopeV1JSON applies before returning).
//
// It deliberately does NOT treat an empty document as "no intent": whether a
// binding was SUPPLIED is a transport question (an unset flag, an absent JSON
// member, a blank tool argument) that each adapter answers in its own
// vocabulary before calling. This function only answers whether a supplied
// binding is VALID, and a nil result therefore always means "invalid", never
// "absent".
func DecodeFiscalWriteIntentJSON(raw []byte) (*FiscalWriteIntent, error) {
	binding, err := DecodeFiscalScopeV1JSON(raw)
	if err != nil {
		return nil, err
	}
	return &FiscalWriteIntent{Binding: binding}, nil
}

const fiscalActEvidenceFrame = "drenyra:fiscal-act-evidence:v1\x00"

// ComputeActEvidenceHash canonically covers a versioned act-evidence frame,
// the binding hash, the reviewed envelope hash (empty when inapplicable —
// every non-approval act in this slice) and the ordered tri-state of both
// review acknowledgements (design.md "Immutable act evidence and envelope
// linkage"). It is the immutable fingerprint of ONE persisted fiscal act; it
// grants nothing.
func ComputeActEvidenceHash(bindingHash, reviewedEnvelopeHash string, evidenceInspected, ruleInspected ReviewAcknowledgement) string {
	parts := []string{bindingHash, reviewedEnvelopeHash, string(evidenceInspected.State()), string(ruleInspected.State())}
	buf := make([]byte, 0, 128)
	buf = append(buf, fiscalActEvidenceFrame...)
	for _, part := range parts {
		buf = append(buf, strconv.Itoa(len([]byte(part)))...)
		buf = append(buf, ':')
		buf = append(buf, part...)
		buf = append(buf, 0)
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

const fiscalEnvelopeLinksFrame = "drenyra:fiscal-envelope-links:v1\x00"

// fiscalLinksEnvelopeContribution returns the self-describing envelope
// contribution of a subject's ordered v1 fiscal binding links, or "" when
// there are none — so a legacy subject's envelope hash (design.md: "No links
// means no contribution") stays byte-identical to every pre-existing golden
// receipt.
func fiscalLinksEnvelopeContribution(links []FiscalBindingLinkContribution) string {
	if len(links) == 0 {
		return ""
	}
	buf := make([]byte, 0, 64*len(links))
	buf = append(buf, fiscalEnvelopeLinksFrame...)
	for _, link := range links {
		buf = append(buf, strconv.Itoa(link.Sequence)...)
		buf = append(buf, ':')
		buf = append(buf, link.BindingHash...)
		buf = append(buf, ':')
		buf = append(buf, link.ActEvidenceHash...)
		buf = append(buf, 0)
	}
	return string(buf)
}
