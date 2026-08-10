// Rule CLI surfaces — Phase 6 v0.6.0, design §6. Read-only: rule show (current
// revision), rule history (full chain), rule impact (regulatory-change
// reconstruction). Rule creation/evolution continues through `save kind=rule`.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/arkelythex/drenyra-engram/internal/server"
)

// cmdRule dispatches the read-only rule surfaces.
func cmdRule(args []string) int {
	if len(args) == 0 {
		printRuleUsage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "show":
		return cmdRuleShow(args[1:])
	case "history":
		return cmdRuleHistory(args[1:])
	case "impact":
		return cmdRuleImpact(args[1:])
	default:
		printRuleUsage(os.Stderr)
		return 2
	}
}

// cmdRuleShow prints the CURRENT rule revision (chain head) of a
// (topicKey, exact Scope) — the rule the reconstruction would resolve to.
func cmdRuleShow(args []string) int {
	fs := flag.NewFlagSet("rule show", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	rruc := fs.String("ruc", "", "company RUC (exactly 11 digits; required)")
	period := fs.String("period", "", "fiscal period YYYYMM (optional)")
	fs.Usage = func() { printRuleUsage(fs.Output()) }
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--ruc": true, "--period": true})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fs.Usage()
		return 2
	}
	scope, err := cliCompanyScope(*rruc, *period)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drenyra-engram: %v\n", err)
		return 2
	}
	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()
	mem, err := server.New(st, "cli").RuleShow(rest[0], scope)
	if err != nil {
		return fail("%v", err)
	}
	return emit(mem)
}

// cmdRuleHistory prints the FULL rule chain (topicKey, exact Scope), ordered by
// revision ascending — superseded revisions stay visible and historically valid.
func cmdRuleHistory(args []string) int {
	fs := flag.NewFlagSet("rule history", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	rruc := fs.String("ruc", "", "company RUC (exactly 11 digits; required)")
	period := fs.String("period", "", "fiscal period YYYYMM (optional)")
	fs.Usage = func() { printRuleUsage(fs.Output()) }
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--ruc": true, "--period": true})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fs.Usage()
		return 2
	}
	scope, err := cliCompanyScope(*rruc, *period)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drenyra-engram: %v\n", err)
		return 2
	}
	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()
	chain, err := server.New(st, "cli").RuleHistory(rest[0], scope)
	if err != nil {
		return fail("%v", err)
	}
	return emit(chain)
}

// cmdRuleImpact prints the regulatory-change impact read: every consuming
// memory of the rule chain, classified against the SELECTED changed revision's
// vigencia window. The CLI is session-less, so the exact scope (--ruc) is
// REQUIRED — the tenant comes from the scope, never from a credential.
func cmdRuleImpact(args []string) int {
	fs := flag.NewFlagSet("rule impact", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	rruc := fs.String("ruc", "", "company RUC (exactly 11 digits; required — session-less CLI derives the tenant from the scope)")
	period := fs.String("period", "", "fiscal period YYYYMM (optional)")
	revision := fs.Int("revision", 0, "1-based revision to compute impact against (default: chain head)")
	fs.Usage = func() { printRuleUsage(fs.Output()) }
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--ruc": true, "--period": true, "--revision": true})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fs.Usage()
		return 2
	}
	scope, err := cliCompanyScope(*rruc, *period)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drenyra-engram: %v\n", err)
		return 2
	}
	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()
	result, err := server.New(st, "cli").RuleImpact(context.Background(), scope.OrganizationID, rest[0], &scope, *revision)
	if err != nil {
		return fail("%v", err)
	}
	return emit(result)
}

func printRuleUsage(w io.Writer) {
	fmt.Fprintln(w, `usage: drenyra-engram rule <command> [flags]
  rule show <topic> --ruc <11 digits> [--period <YYYYMM>] [--db <path>]    (v0.6.0 read-only)
  rule history <topic> --ruc <11 digits> [--period <YYYYMM>] [--db <path>] (v0.6.0 read-only)
  rule impact <topic> --ruc <11 digits> [--period <YYYYMM>] [--revision N] [--db <path>]  (v0.6.0 read-only)
Rule creation/evolution continues through 'save' with kind=rule.`)
}
