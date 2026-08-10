// Reconstructibility CLI surface — v1-readiness G-10 (design D-1). A
// deterministic READ-ONLY metric for ONE exact company scope + period:
//
//	drenyra-engram reconstructibility <ruc> --period <YYYYMM>
//	    [--company-id <id>] [--organization <id>] [--db <path>] [--objects <dir>]
//
// The session-less CLI derives the exact scope the same way every other CLI read
// does (organizationId = the fixed CLI organization, companyId := ruc) unless
// --company-id / --organization override it. --period is REQUIRED. JSON goes to
// stdout; exit 0 means the report was built (INCLUDING a zero denominator); exit
// 2 means an invalid/ambiguous scope/period or an unavailable/corrupt read, with
// the stable code on stderr. The metric has NO failed-metric exit 1:
// non-reconstructible decisions are report data, never command failure.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/server"
)

// cmdReconstructibility prints the frozen ReconstructibilityResult JSON of ONE
// exact company scope + period, delegating to the canonical API method (the
// route/tool/CLI never touch the store reads directly — design D-1).
func cmdReconstructibility(args []string) int {
	fs := flag.NewFlagSet("reconstructibility", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	objects := fs.String("objects", "", "WORM evidence object root (default <dir-of-db>/objects or $DRENYRA_ENGRAM_OBJECTS)")
	companyID := fs.String("company-id", "", "company id (default: the RUC — the established CLI derivation)")
	organization := fs.String("organization", "", "organization id (default: the fixed CLI organization)")
	period := fs.String("period", "", "fiscal period YYYYMM (required)")
	fs.Usage = func() { printReconstructibilityUsage(fs.Output()) }
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"--db": true, "--objects": true, "--company-id": true, "--organization": true, "--period": true,
	})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintf(os.Stderr, "drenyra-engram: INVALID_RECONSTRUCTIBILITY_SCOPE: reconstructibility requires exactly one company RUC (exactly 11 digits) and --period <YYYYMM>\n")
		return 2
	}
	ruc := rest[0]
	if !core.IsValidRUC(ruc) {
		fmt.Fprintf(os.Stderr, "drenyra-engram: INVALID_RECONSTRUCTIBILITY_SCOPE: invalid RUC %q: expected exactly 11 digits\n", ruc)
		return 2
	}
	if *period == "" || !core.IsValidPeriod(*period) {
		fmt.Fprintf(os.Stderr, "drenyra-engram: INVALID_PERIOD: period is required and must be YYYYMM (month 01-12)\n")
		return 2
	}
	scope := core.Scope{
		Kind:           core.ScopeKindCompany,
		OrganizationID: *organization,
		CompanyID:      *companyID,
		RUC:            ruc,
		Period:         *period,
	}
	if scope.OrganizationID == "" {
		scope.OrganizationID = cliOrganizationID
	}
	if scope.CompanyID == "" {
		scope.CompanyID = ruc // the established CLI derivation (design D-1)
	}

	st, err := openStoreWithRoot(*dbPath, *objects)
	if err != nil {
		// An unavailable/corrupt read is exit 2 with the stable code — never a
		// mislabeled metric (design D-1: no failed-metric exit 1).
		fmt.Fprintf(os.Stderr, "drenyra-engram: RECONSTRUCTIBILITY_UNAVAILABLE: %v\n", err)
		return 2
	}
	defer func() { _ = st.Close() }()

	result, err := server.New(st, "cli").Reconstructibility(context.Background(), scope)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drenyra-engram: %v\n", err)
		return 2
	}
	return emit(result)
}

func printReconstructibilityUsage(w io.Writer) {
	fmt.Fprintln(w, `usage: drenyra-engram reconstructibility <ruc> --period <YYYYMM> [--company-id <id>] [--organization <id>] [--db <path>] [--objects <dir>]
  (v1-readiness G-10 read-only metric: deterministic FZ-1 denominator / FZ-2 numerator / six closed reason groups for ONE exact company scope + period.
   JSON to stdout; exit 0 even when the denominator is zero; exit 2 with a stable code on stderr for an invalid scope/period or an unavailable/corrupt read.)`)
}
