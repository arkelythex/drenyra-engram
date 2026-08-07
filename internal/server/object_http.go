// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module adds the v0.7.0 EvidenceObject
// HTTP surfaces to the shared HTTPServer (docs/architecture/
// evidence-object-v0.7.md §7): STORE + GET that can NEVER approve anything —
// both carry the shared-token guard, neither derives an authenticated
// principal, and storing/reading an object is a provenance-recorded capture,
// not an authorization.
//
// Wire shape (identical across HTTP and MCP): the artifact bytes travel as
// standard padded base64 in JSON; the exact scope and the capture source are
// explicit body fields on store and explicit query parameters on get
// (?ruc= + ?organizationId= + ?period=). Get is SCOPE-FIRST: a caller whose
// exact scope differs from the stored scope sees OBJECT_NOT_FOUND
// (cross-tenant invisibility, fail closed), and the stored bytes are re-hashed
// on every read (corruption fails closed, no silent repair).
package server

import (
	"encoding/base64"
	"errors"
	"net/http"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// objectStoreRequest is the HTTP/MCP wire shape of an object store: the
// artifact bytes base64-encoded (JSON-safe, identical across transports), an
// optional content type hint and the exact scope + capture source. The wire
// NEVER carries authority fields — storing an object cannot approve anything.
type objectStoreRequest struct {
	BytesB64    string      `json:"bytesB64"`
	ContentType string      `json:"contentType,omitempty"`
	Scope       core.Scope  `json:"scope"`
	Source      core.Source `json:"source"`
}

// objectGetResponse is the wire shape of a scope-first object read: the
// metadata plus the artifact bytes base64-encoded.
type objectGetResponse struct {
	Object   core.EvidenceObject `json:"object"`
	BytesB64 string              `json:"bytesB64"`
}

// handleObjectStore captures one evidence object WORM-style (v0.7.0): closed
// period gate, content-addressed duplicate no-op, atomic object_stored receipt
// — all in the store. Shared-token guard only; the surface can NEVER approve.
func (h *HTTPServer) handleObjectStore(w http.ResponseWriter, r *http.Request) {
	var req objectStoreRequest
	if !h.decodeBody(w, r, &req) {
		return
	}
	bytes, err := base64.StdEncoding.DecodeString(req.BytesB64)
	if err != nil {
		h.writeError(w, errors.New("INVALID_OBJECT: bytesB64 must be standard padded base64"))
		return
	}
	result, err := h.api.StoreObject(r.Context(), core.ObjectStoreInput{
		Bytes:       bytes,
		ContentType: req.ContentType,
		Scope:       req.Scope,
		Source:      req.Source,
	})
	if err != nil {
		h.writeError(w, err)
		return
	}
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, result)
}

// handleObjectGet reads one evidence object SCOPE-FIRST (?ruc= + ?organizationId=
// + ?period=): a caller whose exact scope differs from the stored scope sees
// OBJECT_NOT_FOUND (cross-tenant invisibility); the stored bytes are re-hashed
// on every read (corruption fails closed, no silent repair).
func (h *HTTPServer) handleObjectGet(w http.ResponseWriter, r *http.Request) {
	scope, err := httpQueryScope(r, false)
	if err != nil {
		h.writeError(w, err)
		return
	}
	object, bytes, err := h.api.GetObject(r.Context(), r.PathValue("objectId"), scope)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, objectGetResponse{
		Object:   object,
		BytesB64: base64.StdEncoding.EncodeToString(bytes),
	})
}
