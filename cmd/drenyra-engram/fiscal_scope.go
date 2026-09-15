// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the Delivery Slice 6 CLI
// fiscal-scope binding-file loader and presence-aware acknowledgement flag
// helper (openspec/changes/fiscal-runtime-foundations, design.md "Public
// contracts > CLI"):
//
//   - loadFiscalScopeBindingFile reads one --fiscal-scope binding file and
//     hands the bytes to the SAME shared seam the HTTP and MCP adapters use
//     (internal/core.DecodeFiscalWriteIntentJSON) — the CLI keeps no private,
//     weaker parser. A read or decode failure fails closed BEFORE any session
//     token is loaded or store opened (spec.md "RUC validation precedes
//     protected work"; design.md boundary matrix "CLI approval | Binding
//     file | Before token/auth").
//   - reviewAckFromFlag inspects the parsed flag set (not just the bound
//     value) so an omitted acknowledgement flag is distinguishable from an
//     explicit false, matching design.md's presence-aware semantics: omission
//     -> {Present:false, Value:false}; --flag=false -> {Present:true,
//     Value:false}; a bare/true flag -> {Present:true, Value:true}.
package main

import (
	"flag"
	"os"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// loadFiscalScopeBindingFile reads path and strictly decodes it as a v1 fiscal
// scope binding document, wrapping the result in a *core.FiscalWriteIntent —
// the value core.ApproveMemoryCommand.FiscalIntent (and core.SaveInput.
// FiscalIntent) expect. A nil intent is never returned on error: the caller
// distinguishes "no binding requested" (an empty --fiscal-scope flag) from "a
// binding was requested but is unreadable/invalid" (this function's error).
func loadFiscalScopeBindingFile(path string) (*core.FiscalWriteIntent, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return core.DecodeFiscalWriteIntentJSON(raw)
}

// reviewAckFromFlag builds the presence-aware core.ReviewAcknowledgement for
// one bool flag by name: Present is true only when fs.Visit reports the flag
// was actually supplied on the command line (bare, "=true" or "=false" all
// count as supplied); value is the flag's final parsed bool. fs must already
// be Parse()d.
func reviewAckFromFlag(fs *flag.FlagSet, name string, value bool) core.ReviewAcknowledgement {
	present := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			present = true
		}
	})
	return core.ReviewAcknowledgement{Present: present, Value: value}
}
