// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module owns the CANONICAL
// presence-aware decoder for the v1 anti-rubber-stamp review acknowledgements
// (openspec/changes/fiscal-runtime-foundations, design.md "The raw DTO uses
// presence-aware nullable booleans").
//
// It lives in core — beside the ReviewChecksV1 contract it decodes — and not
// in a transport adapter, because "an omitted acknowledgement is NEVER a
// successful declaration" (proposal.md) is a property of the CONTRACT, not of
// any one transport. Stating it once here means a new adapter inherits the
// rule by construction instead of re-implementing it correctly by discipline.
//
// core/approval.go is values-only by design ("no logic lives here"), so the
// decoder is a separate module rather than an addition to that file.
package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// DecodeReviewChecksV1JSON strictly decodes one `reviewChecks` object into the
// presence-aware ReviewChecksV1. An absent (zero-length) document yields the
// zero value — both checks OMITTED — which is exactly the state a caller that
// never supplies the object produces; it is never an implicit "true".
//
// The decode fails closed on anything ambiguous: a non-object document, an
// unknown member, a duplicate member, a non-boolean value, or trailing JSON
// after the object. Duplicates matter because encoding/json alone would
// silently keep the LAST value, letting a caller smuggle a false declaration
// past a reviewer reading the first one.
//
// Member ORDER is deliberately free (unlike DecodeFiscalScopeV1JSON's
// canonical-order binding): these two members are independently optional, so
// no canonical byte sequence exists to enforce.
func DecodeReviewChecksV1JSON(raw []byte) (ReviewChecksV1, error) {
	var checks ReviewChecksV1
	if len(raw) == 0 {
		return checks, nil
	}
	// The accepted member set is DATA, not control flow: adding a future
	// acknowledgement is one entry here, and "unknown member" stays a single
	// fail-closed branch.
	targets := map[string]*ReviewAcknowledgement{
		"evidenceInspected":        &checks.EvidenceInspected,
		"applicableRulesInspected": &checks.RuleInspected,
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if first, err := decoder.Token(); err != nil || first != json.Delim('{') {
		return ReviewChecksV1{}, errors.New("reviewChecks must be one JSON object")
	}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return ReviewChecksV1{}, errors.New("reviewChecks: malformed member name")
		}
		key, ok := keyToken.(string)
		if !ok {
			return ReviewChecksV1{}, errors.New("reviewChecks: member names must be strings")
		}
		target, known := targets[key]
		if !known {
			return ReviewChecksV1{}, errors.New("reviewChecks: unknown member " + key)
		}
		// A repeated member is necessarily already Present — the tri-state
		// itself is the duplicate detector, so no side table is needed.
		if target.Present {
			return ReviewChecksV1{}, errors.New("reviewChecks: duplicate member " + key)
		}
		// Decode the value as RAW bytes and accept only a boolean literal.
		// Decoding straight into a bool would be wrong: encoding/json treats
		// a JSON null as a no-op on the destination and returns NO error, so
		// an explicit `"evidenceInspected": null` would silently become a
		// present-and-false declaration instead of being rejected.
		var rawValue json.RawMessage
		if err := decoder.Decode(&rawValue); err != nil {
			return ReviewChecksV1{}, errors.New("reviewChecks: malformed value for " + key)
		}
		value, ok := boolLiteral(rawValue)
		if !ok {
			return ReviewChecksV1{}, errors.New("reviewChecks: " + key + " must be a boolean")
		}
		*target = ReviewAcknowledgement{Present: true, Value: value}
	}
	if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
		return ReviewChecksV1{}, errors.New("reviewChecks: invalid object")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ReviewChecksV1{}, errors.New("reviewChecks: trailing JSON value")
	}
	return checks, nil
}

// boolLiteral reports the value of raw when it is exactly the JSON literal
// true or false, and ok=false for ANYTHING else — null, a number, a quoted
// "true", an object or an array. An acknowledgement is a reviewer's explicit
// statement, so only an explicit boolean may produce one.
func boolLiteral(raw json.RawMessage) (value bool, ok bool) {
	switch string(bytes.TrimSpace(raw)) {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}
