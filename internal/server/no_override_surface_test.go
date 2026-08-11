// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test is the STRUCTURAL half of negative
// override conformance (FR-J.2 / AC-J-2, design §J): it proves, over the
// canonical request/command types of the HTTP, MCP and shared API mutation
// boundaries, that NO field or input spelling exists for override, break-glass,
// force, bypass, or any equivalent — and that no adapter input maps to such a
// field.
//
// The deliberate absence is already recorded in the code; this test freezes it:
//
//   - internal/authz/evidence_lifecycle_policy.go:38 — "no override field exists"
//     (blockers run before authorization);
//   - internal/store/purge_store.go:32 — "no override field exists" (blockers
//     before authz); :856 — "no override exists" (authorizePurgeAct gate);
//     :1010 — "no override exists" (blocker set); :1237 — "approval can never
//     override a blocker";
//   - internal/store/store.go:383/:390 — "no override exists" (blockers precede
//     authz; approve can never override a blocker);
//   - internal/auth/errors.go:104 — "no override exists (design §7)" (HOLD_ACTIVE
//     blocker);
//   - internal/store/purge_execution_store.go:573 — "no override field exists"
//     (execute re-runs the full non-overridable blocker set).
//
// Mechanics (D-3/D-4): reflection over an explicit []reflect.Type registry of
// canonical exported core.*Command / core.*Input values actually accepted by the
// API, HTTP and MCP mutation boundaries. For every recursively reachable
// EXPORTED field (visited-type set; structs, pointers, slices, arrays and map
// element types) the Go name and the JSON tag are normalized and rejected when
// they spell override / break-glass / force / bypass or an equivalent. Comments
// and output types are NOT scanned (denial messages legitimately contain the
// forbidden words). An adjacent AST guard parses internal/server/api.go and
// requires every exported *core.XCommand / *core.XInput parameter of an
// exported API method to be registered, so a new canonical request type cannot
// drift past the reflection sweep. Reflection never invokes unknown methods or
// infers business outcomes — it only inspects the frozen request shapes.
package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unicode"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// canonicalRequestTypes is the explicit registry of canonical exported
// core.*Command / core.*Input values accepted by the API, HTTP and MCP mutation
// boundaries (design D-3: request types are the security boundary, so the
// registry is explicit rather than "every struct in core").
var canonicalRequestTypes = []reflect.Type{
	// Shared API surface (internal/server/api.go mutation boundaries).
	reflect.TypeOf(core.SaveInput{}),
	reflect.TypeOf(core.ObjectStoreInput{}),
	reflect.TypeOf(core.PutRetentionPolicyCommand{}),
	reflect.TypeOf(core.EvaluatePurgeEligibilityInput{}),
	reflect.TypeOf(core.PlaceHoldCommand{}),
	reflect.TypeOf(core.LiftHoldCommand{}),
	reflect.TypeOf(core.RequestPurgeCommand{}),
	reflect.TypeOf(core.ApprovePurgeCommand{}),
	reflect.TypeOf(core.RejectPurgeCommand{}),
	reflect.TypeOf(core.CancelPurgeCommand{}),
	reflect.TypeOf(core.WithdrawPurgeCommand{}),
	reflect.TypeOf(core.ExecutePurgeCommand{}),
	reflect.TypeOf(core.RejectMemoryCommand{}),
	reflect.TypeOf(core.ReturnMemoryCommand{}),
	// HTTP/MCP adapter-only mutation boundaries (built by the adapters from
	// strict decoder arguments; never params of the canonical API methods).
	reflect.TypeOf(core.ApproveMemoryCommand{}),
	reflect.TypeOf(core.ProposeJudgmentCommand{}),
	reflect.TypeOf(core.ConfirmJudgmentCommand{}),
	reflect.TypeOf(core.RejectJudgmentCommand{}),
	reflect.TypeOf(core.WithdrawJudgmentCommand{}),
	reflect.TypeOf(core.ProposeReconciliationCommand{}),
	reflect.TypeOf(core.ConfirmReconciliationCommand{}),
	reflect.TypeOf(core.RejectReconciliationCommand{}),
	reflect.TypeOf(core.WithdrawReconciliationCommand{}),
	reflect.TypeOf(core.ReopenPeriodCommand{}),
	reflect.TypeOf(core.CreateCloseInput{}),
}

// adapterOnlyRequestTypes is the subset of the registry consumed by the HTTP/MCP
// adapters only (never a parameter of an exported core.*-typed API method). It
// keeps the AST guard's classification honest: a type found in api.go must NOT
// be adapter-only.
var adapterOnlyRequestTypes = map[reflect.Type]bool{
	reflect.TypeOf(core.ApproveMemoryCommand{}):          true,
	reflect.TypeOf(core.ProposeJudgmentCommand{}):        true,
	reflect.TypeOf(core.ConfirmJudgmentCommand{}):        true,
	reflect.TypeOf(core.RejectJudgmentCommand{}):         true,
	reflect.TypeOf(core.WithdrawJudgmentCommand{}):       true,
	reflect.TypeOf(core.ProposeReconciliationCommand{}):  true,
	reflect.TypeOf(core.ConfirmReconciliationCommand{}):  true,
	reflect.TypeOf(core.RejectReconciliationCommand{}):   true,
	reflect.TypeOf(core.WithdrawReconciliationCommand{}): true,
	reflect.TypeOf(core.ReopenPeriodCommand{}):           true,
	reflect.TypeOf(core.CreateCloseInput{}):              true,
}

// forbiddenSpellingWords are the normalized override-equivalent tokens. A field
// whose Go name or JSON tag yields ANY of these words (as a camel/snake/kebab
// segment) is a forbidden override spelling.
var forbiddenSpellingWords = map[string]bool{
	"override":   true,
	"break":      true,
	"glass":      true,
	"breakglass": true,
	"force":      true,
	"bypass":     true,
}

// normalizedFieldWords splits a Go field name (CamelCase) or a JSON tag into
// lowercase words: case boundaries and non-alphanumeric separators split words,
// so "OverrideEnabled" → [override, enabled], "break_glass" → [break, glass],
// "breakglass" → [breakglass].
func normalizedFieldWords(name string) []string {
	var words []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			words = append(words, current.String())
			current.Reset()
		}
	}
	for _, r := range name {
		switch {
		case unicode.IsUpper(r):
			flush()
			current.WriteRune(unicode.ToLower(r))
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			current.WriteRune(unicode.ToLower(r))
		default:
			flush()
		}
	}
	flush()
	return words
}

// fieldNames returns the normalized words of both the Go field name and its
// JSON tag (when present). The empty JSON tag yields no additional words.
func fieldNames(f reflect.StructField) []string {
	words := normalizedFieldWords(f.Name)
	if tag := strings.Split(f.Tag.Get("json"), ",")[0]; tag != "" && tag != "-" {
		words = append(words, normalizedFieldWords(tag)...)
	}
	return words
}

// walkRequestType traverses every exported field reachable from the request
// type (structs, pointers, slices, arrays and map element types; visited set
// against cycles) and invokes visit for each field.
func walkRequestType(t reflect.Type, seen map[reflect.Type]bool, visit func(f reflect.StructField)) {
	if t == nil {
		return
	}
	for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	if seen[t] {
		return
	}
	seen[t] = true
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" { // unexported
			continue
		}
		visit(f)
		walkRequestType(f.Type, seen, visit)
	}
}

// TestNoOverrideFieldOnRequestSurfaces (AC-J-2 / FR-J.2): recursively inspect
// every canonical request type and reject any field whose Go name or JSON tag
// spells an override equivalent.
func TestNoOverrideFieldOnRequestSurfaces(t *testing.T) {
	bad := []string{}
	report := func(t reflect.Type, f reflect.StructField, words []string) {
		bad = append(bad, t.String()+"."+f.Name+" (json "+f.Tag.Get("json")+") → "+strings.Join(words, " "))
	}

	for _, typ := range canonicalRequestTypes {
		walkRequestType(typ, map[reflect.Type]bool{}, func(f reflect.StructField) {
			words := fieldNames(f)
			for _, w := range words {
				if forbiddenSpellingWords[w] {
					report(typ, f, words)
					return
				}
			}
		})
	}

	if len(bad) > 0 {
		t.Fatalf("override-equivalent field spellings found on canonical request surfaces:\n%s",
			strings.Join(bad, "\n"))
	}
}

// TestCanonicalRequestTypesMatchAPISurface is the AST drift guard: every
// exported *core.XCommand / *core.XInput parameter on an exported API method in
// internal/server/api.go must be registered (or explicitly classified
// adapter-only). A new canonical request type cannot bypass the reflection
// sweep above.
func TestCanonicalRequestTypesMatchAPISurface(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "api.go", nil, 0)
	if err != nil {
		t.Fatalf("parse api.go: %v", err)
	}

	registered := map[string]bool{}
	for _, typ := range canonicalRequestTypes {
		name := typ.Name()
		if name == "" {
			t.Fatalf("registry type %v has no name", typ)
		}
		registered[name] = true
	}

	discovered := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || !fn.Name.IsExported() {
			continue
		}
		for _, param := range fn.Type.Params.List {
			name := coreSelectorCommandOrInput(param.Type)
			if name == "" {
				continue
			}
			discovered[name] = true
		}
	}

	var missing []string
	for name := range discovered {
		if !registered[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("exported core.*Command/*Input params on API methods not registered in canonicalRequestTypes:\n%s",
			strings.Join(missing, "\n"))
	}

	// A type discovered in api.go must not be classified adapter-only: the
	// classification must stay honest.
	for typ := range adapterOnlyRequestTypes {
		if discovered[typ.Name()] {
			t.Fatalf("type %s appears in api.go but is classified adapter-only", typ.Name())
		}
	}

	// The adapter-only classification must itself be registered.
	for typ := range adapterOnlyRequestTypes {
		if !registered[typ.Name()] {
			t.Fatalf("adapter-only type %s missing from canonicalRequestTypes", typ.Name())
		}
	}
}

// coreSelectorCommandOrInput returns the type name of a *core.XCommand /
// core.XInput (or *core.XInput) parameter expression, or "" when the expression
// is not a core selector with a Command/Input suffix. Adapter inputs (search,
// store) and value types without the Command/Input suffix are not canonical
// request types and are deliberately skipped.
func coreSelectorCommandOrInput(expr ast.Expr) string {
	e := expr
	if star, ok := e.(*ast.StarExpr); ok {
		e = star.X
	}
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "core" {
		return ""
	}
	name := sel.Sel.Name
	if strings.HasSuffix(name, "Command") || strings.HasSuffix(name, "Input") {
		return name
	}
	return ""
}
