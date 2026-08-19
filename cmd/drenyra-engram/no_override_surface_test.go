// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test is the CLI half of structural
// negative override conformance (FR-J.2 / AC-J-2, design §J/D-4): it constructs
// each current command's real flag.FlagSet (by dispatching the real command
// with the forbidden flag as its ONLY argument) and proves the flag package
// rejects every normalized override spelling — override, break-glass, force,
// bypass and equivalents — with the frozen unknown-flag failure, before any
// store, file or side effect. A command-catalog guard parses the root dispatch
// switch (run) and every group subcommand switch from source, so a new command
// or subcommand cannot bypass inspection. This is more robust than scanning all
// source strings, where comments and denial messages legitimately contain the
// forbidden words.
//
// The behavioral half (TestCLIOverrideInputDeniedFailClosed, AC-J-3) lives in
// this file too: representative privileged commands invoked with the forbidden
// flags fail with the existing unknown-flag usage failure and leave the
// database byte-identical.
package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// cliPath is one dispatch path to a flag-bearing command (leaf commands have a
// single element; group subcommands carry the full path).
type cliPath struct {
	name string
	path []string
}

// cliDispatchPaths enumerates every current flag-bearing command of the CLI
// surface. The catalog guard below fails when a dispatch case exists without a
// matching registry entry.
var cliDispatchPaths = []cliPath{
	{name: "save", path: []string{"save"}},
	{name: "search", path: []string{"search"}},
	{name: "context", path: []string{"context"}},
	{name: "doctor", path: []string{"doctor"}},
	{name: "compare", path: []string{"compare"}},
	{name: "approve", path: []string{"approve"}},
	{name: "reject", path: []string{"reject"}},
	{name: "void", path: []string{"void"}},
	{name: "supersede", path: []string{"supersede"}},
	{name: "link-evidence", path: []string{"link-evidence"}},
	{name: "period-summary", path: []string{"period-summary"}},
	{name: "compare-periods", path: []string{"compare-periods"}},
	{name: "timeline", path: []string{"timeline"}},
	{name: "mcp", path: []string{"mcp"}},
	{name: "serve", path: []string{"serve"}},
	{name: "sync", path: []string{"sync"}},
	{name: "reconstructibility", path: []string{"reconstructibility"}},
	{name: "auth login", path: []string{"auth", "login"}},
	{name: "auth seed-local-dev", path: []string{"auth", "seed-local-dev"}},
	{name: "keys init", path: []string{"keys", "init"}},
	{name: "keys show", path: []string{"keys", "show"}},
	{name: "keys rotate", path: []string{"keys", "rotate"}},
	{name: "judge propose", path: []string{"judge", "propose"}},
	{name: "judge confirm", path: []string{"judge", "confirm"}},
	{name: "judge reject", path: []string{"judge", "reject"}},
	{name: "judge withdraw", path: []string{"judge", "withdraw"}},
	{name: "judge show", path: []string{"judge", "show"}},
	{name: "reconcile propose", path: []string{"reconcile", "propose"}},
	{name: "reconcile confirm", path: []string{"reconcile", "confirm"}},
	{name: "reconcile reject", path: []string{"reconcile", "reject"}},
	{name: "reconcile withdraw", path: []string{"reconcile", "withdraw"}},
	{name: "reconcile show", path: []string{"reconcile", "show"}},
	{name: "verify memory", path: []string{"verify", "memory"}},
	{name: "verify judgment", path: []string{"verify", "judgment"}},
	{name: "verify receipt", path: []string{"verify", "receipt"}},
	{name: "verify object", path: []string{"verify", "object"}},
	{name: "object store", path: []string{"object", "store"}},
	{name: "object get", path: []string{"object", "get"}},
	{name: "object ingest", path: []string{"object", "ingest"}},
	{name: "close create", path: []string{"close", "create"}},
	{name: "close show", path: []string{"close", "show"}},
	{name: "close reopen", path: []string{"close", "reopen"}},
	{name: "hold place", path: []string{"hold", "place"}},
	{name: "hold lift", path: []string{"hold", "lift"}},
	{name: "hold list", path: []string{"hold", "list"}},
	{name: "retention-policy put", path: []string{"retention-policy", "put"}},
	{name: "retention-policy resolve", path: []string{"retention-policy", "resolve"}},
	{name: "retention-policy evaluate", path: []string{"retention-policy", "evaluate"}},
	{name: "purge request", path: []string{"purge", "request"}},
	{name: "purge approve", path: []string{"purge", "approve"}},
	{name: "purge reject", path: []string{"purge", "reject"}},
	{name: "purge cancel", path: []string{"purge", "cancel"}},
	{name: "purge withdraw", path: []string{"purge", "withdraw"}},
	{name: "purge execute", path: []string{"purge", "execute"}},
	{name: "export lifecycle", path: []string{"export", "lifecycle"}},
	{name: "tenant list", path: []string{"tenant", "list"}},
	{name: "tenant consolidate", path: []string{"tenant", "consolidate"}},
	{name: "review queue", path: []string{"review", "queue"}},
	{name: "review detail", path: []string{"review", "detail"}},
	{name: "review reject", path: []string{"review", "reject"}},
	{name: "review return", path: []string{"review", "return"}},
	{name: "rule show", path: []string{"rule", "show"}},
	{name: "rule history", path: []string{"rule", "history"}},
	{name: "rule impact", path: []string{"rule", "impact"}},
}

// forbiddenCLISpellings are the normalized override-equivalent flag names. The
// flag package rejects them at parse time when the command's real FlagSet does
// not define them.
var forbiddenCLISpellings = []string{"override", "break-glass", "force", "bypass"}

// runWithCapturedStderr invokes run() in-process with os.Stderr piped so the
// flag package's unknown-flag error can be asserted verbatim.
func runWithCapturedStderr(t *testing.T, args []string) (int, string) {
	t.Helper()
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stderr: %v", err)
	}
	os.Stderr = w
	code := run(args)
	_ = w.Close()
	os.Stderr = oldStderr
	out, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	return code, string(out)
}

// TestCLINoOverrideFlagOnCommands (AC-J-2 / FR-J.2): every flag-bearing command
// rejects every forbidden spelling with the flag package's unknown-flag failure
// (exit 2 + "flag provided but not defined: -<flag>") BEFORE any store, file or
// side effect. If a command ever defined an override flag, its Parse would
// succeed and the command would proceed — the assertion below would fail.
func TestCLINoOverrideFlagOnCommands(t *testing.T) {
	for _, entry := range cliDispatchPaths {
		for _, spelling := range forbiddenCLISpellings {
			args := append(append([]string{}, entry.path...), "--"+spelling)
			t.Run(entry.name+"/--"+spelling, func(t *testing.T) {
				code, stderr := runWithCapturedStderr(t, args)
				if code != 2 {
					t.Fatalf("run %v = exit %d, want 2 (unknown flag); stderr: %s", args, code, stderr)
				}
				want := "flag provided but not defined: -" + spelling
				if !strings.Contains(stderr, want) {
					t.Fatalf("run %v stderr = %q, want it to contain %q", args, stderr, want)
				}
			})
		}
	}
}

// TestCLICommandCatalogGuard is the drift guard: every top-level dispatch case
// in run() and every group subcommand case must be covered by cliDispatchPaths,
// so a new command or subcommand cannot bypass TestCLINoOverrideFlagOnCommands.
func TestCLICommandCatalogGuard(t *testing.T) {
	topLevel := cliSwitchCases(t, "main.go", "run")
	topLevel = dropHelp(topLevel)

	registered := map[string][]string{}
	for _, entry := range cliDispatchPaths {
		registered[entry.path[0]] = append(registered[entry.path[0]], entry.path[1:]...)
	}

	var missingTop []string
	for _, cmd := range topLevel {
		if _, ok := registered[cmd]; !ok {
			missingTop = append(missingTop, cmd)
		}
	}
	sort.Strings(missingTop)
	if len(missingTop) > 0 {
		t.Fatalf("top-level commands without flag inspection in cliDispatchPaths:\n%s", strings.Join(missingTop, "\n"))
	}

	// Every top-level command in the registry must exist in the dispatch catalog.
	var phantom []string
	for cmd := range registered {
		found := false
		for _, c := range topLevel {
			if c == cmd {
				found = true
				break
			}
		}
		if !found {
			phantom = append(phantom, cmd)
		}
	}
	sort.Strings(phantom)
	if len(phantom) > 0 {
		t.Fatalf("registry commands not present in the run() dispatch catalog:\n%s", strings.Join(phantom, "\n"))
	}

	// Group subcommands: each top-level command with a sub-switch must have all
	// its subcommand cases covered.
	groupFiles := map[string]string{
		"auth": "main.go", "keys": "main.go", "judge": "main.go", "reconcile": "main.go",
		"verify": "main.go", "object": "main.go", "close": "main.go", "hold": "main.go",
		"retention-policy": "main.go", "purge": "main.go", "export": "main.go",
		"review": "main.go", "rule": "rule.go",
	}
	for cmd, file := range groupFiles {
		subs := dropHelp(cliSwitchCases(t, file, "cmd"+upperFirst(cmd)))
		if len(subs) == 0 {
			continue // leaf command, no sub-switch
		}
		covered := registered[cmd]
		for _, sub := range subs {
			if !containsString(covered, sub) {
				t.Errorf("subcommand %q of %q missing from cliDispatchPaths", sub, cmd)
			}
		}
		for _, sub := range covered {
			if !containsString(subs, sub) {
				t.Errorf("registry path %q %q not present in the %q dispatch switch", cmd, sub, cmd)
			}
		}
	}
}

// cliSwitchCases parses the named file, finds the function and returns the
// string case labels of its FIRST switch statement (the dispatch switch).
func cliSwitchCases(t *testing.T, filename, funcName string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != funcName || fn.Body == nil {
			continue
		}
		var labels []string
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok || sw.Body == nil {
				return true
			}
			for _, stmt := range sw.Body.List {
				cc, ok := stmt.(*ast.CaseClause)
				if !ok {
					continue
				}
				for _, e := range cc.List {
					if bl, ok := e.(*ast.BasicLit); ok && bl.Kind == token.STRING {
						labels = append(labels, strings.Trim(bl.Value, `"`))
					}
				}
			}
			return false // only the first switch
		})
		if labels != nil {
			return labels
		}
	}
	return nil
}

func dropHelp(labels []string) []string {
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		if l != "help" && l != "-h" && l != "--help" {
			out = append(out, l)
		}
	}
	return out
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestCLIOverrideInputDeniedFailClosed (AC-J-3 / FR-J.3): representative
// privileged commands invoked with the forbidden flags fail with the existing
// unknown-flag usage failure and leave the database byte-identical (the denied
// command never opens the store — the strongest form of no state change).
func TestCLIOverrideInputDeniedFailClosed(t *testing.T) {
	db := filepath.Join(t.TempDir(), "engram.db")
	token := seedCLIIdentity(t, db)
	env := sessionFileEnv(t.TempDir())
	if _, stderr, code := runCLIEnv(t, env, "auth", "login", "--token", token, "--db", db); code != 0 {
		t.Fatalf("auth login failed (exit %d): %s", code, stderr)
	}

	before, err := os.ReadFile(db)
	if err != nil {
		t.Fatalf("read db before: %v", err)
	}

	// Representative privileged commands: each one would be able to act with the
	// seeded session, but the forbidden flag is rejected at flag-parse time.
	paths := [][]string{
		{"approve", "mem-1", "--expected-envelope", strings.Repeat("a", 64), "--reason", "r"},
		{"review", "reject", "mem-1"},
		{"purge", "approve", "req-1"},
		{"close", "reopen", "202401"},
		{"judge", "confirm", "judgment-1"},
		{"reconcile", "confirm", "reconciliation-1"},
		{"hold", "place", "obj-1"},
	}
	for _, spelling := range forbiddenCLISpellings {
		for _, path := range paths {
			args := append(append([]string{}, path...), "--"+spelling, "--db", db)
			t.Run(path[0]+"-"+path[len(path)-1]+"/--"+spelling, func(t *testing.T) {
				stdout, stderr, code := runCLIEnv(t, env, args...)
				_ = stdout
				if code != 2 {
					t.Fatalf("run %v = exit %d, want 2 (unknown flag); stderr: %s", args, code, stderr)
				}
				if !strings.Contains(stderr, "flag provided but not defined: -"+spelling) {
					t.Fatalf("stderr = %q, want unknown-flag failure naming -%s", stderr, spelling)
				}
				after, err := os.ReadFile(db)
				if err != nil {
					t.Fatalf("read db after: %v", err)
				}
				if !bytes.Equal(before, after) {
					t.Fatal("database changed after a denied override flag (must be byte-identical)")
				}
			})
		}
	}
}
