// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the legacy re-encryption
// operator command (sdd-060-legacy-reencrypt, FR-RE-1/3): `encrypt` re-encrypts
// the legacy plaintext content of company-scope observations under the
// configured master key (per-tenant derived keys, same envelope semantics as
// new writes). Dry-run is the default (ZERO writes); `--apply` executes in ONE
// transaction. Fail-closed: requires DRENYRA_ENCRYPTION_MASTER_KEY
// (ENCRYPTION_REQUIRED otherwise). Hashes, receipts, relations and the
// transition log are untouched (the decrypted memory is byte-identical). No
// monetary field exists anywhere in this file.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

// cmdEncrypt re-encrypts legacy plaintext rows (dry-run default, --apply to
// execute). FR-RE-1/FR-RE-3.
func cmdEncrypt(args []string) int {
	fs := flag.NewFlagSet("encrypt", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	dryRun := fs.Bool("dry-run", false, "report legacy rows only (default; ZERO writes)")
	apply := fs.Bool("apply", false, "re-encrypt every legacy plaintext company row (one transaction)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram encrypt [--dry-run | --apply] [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}
	if *dryRun && *apply {
		fmt.Fprintln(os.Stderr, "drenyra-engram: --dry-run and --apply are mutually exclusive")
		return 2
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	if !*apply {
		report, err := st.LegacyEncryptionCounts(context.Background())
		if err != nil {
			return fail("%v", err)
		}
		return emit(report)
	}
	report, err := st.ReencryptLegacyContent(context.Background())
	if err != nil {
		return fail("%v", err)
	}
	return emit(report)
}
