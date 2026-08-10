// object ingest — the comprobante ingestion adapter (design brief §7.3).
// Parses an electronic invoice XML (UBL 2.1), stores its bytes as a WORM
// evidence object, and prints the parsed metadata + content address. This is
// MANUAL ingestion — it NEVER claims SUNAT integration (no credentials, no
// retries, no outage/response-retention handling).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/server"
)

// cmdObjectIngest reads ONE local invoice XML file and ingests it through the
// adapter contract: parse → validate → WORM object store → print metadata.
func cmdObjectIngest(args []string) int {
	fs := flag.NewFlagSet("object ingest", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	rruc := fs.String("ruc", "", "company RUC (exactly 11 digits; required)")
	period := fs.String("period", "", "fiscal period YYYYMM (optional)")
	actor := fs.String("actor", "cli-user", "provenance actor id")
	fs.Usage = func() { printObjectUsage(fs.Output()) }
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--ruc": true, "--period": true, "--actor": true})); err != nil {
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
	bytes, err := os.ReadFile(rest[0])
	if err != nil {
		return fail("read %s: %v", rest[0], err)
	}
	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()
	source := core.Source{System: "cli", ActorID: *actor, ActorKind: core.ActorKindAgent}
	comp, result, err := server.IngestComprobante(context.Background(), st, scope, source, bytes)
	if err != nil {
		return fail("%v", err)
	}
	type out struct {
		Comprobante core.Comprobante    `json:"comprobante"`
		Object      core.EvidenceObject `json:"object"`
		Created     bool                `json:"created"`
	}
	return emit(out{Comprobante: comp, Object: result.Object, Created: result.Created})
}
