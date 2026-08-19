// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This module is the operator tenant CLI
// surface (sdd-060-tenant-cli, FR-TEN-1/FR-TEN-2): `tenant list` enumerates
// ids/counts (never per-tenant content) and `tenant consolidate` detects — and
// with --apply merges — topic-key drift within ONE exact tenant scope via the
// existing supersede path. consolidate authorizes nothing (non-authorization
// boundary) and never crosses RUC. No monetary field exists anywhere in this
// file.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/server"
)

// cmdTenant dispatches the tenant subcommands (list | consolidate).
func cmdTenant(args []string) int {
	if len(args) == 0 {
		tenantUsage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "list":
		return cmdTenantList(args[1:])
	case "consolidate":
		return cmdTenantConsolidate(args[1:])
	default:
		tenantUsage(os.Stderr)
		return 2
	}
}

func tenantUsage(w *os.File) {
	fmt.Fprintln(w, `drenyra-engram tenant — operator tenant surface (ids/counts only, never per-tenant content)

Usage:
  drenyra-engram tenant list [--db <path>]                          enumerate tenants (organizations, companies, periods, counts)
  drenyra-engram tenant consolidate --ruc <11 digits> [--period <YYYYMM>] [--dry-run | --apply] [--db <path>]
                                                                     detect topic-key drift within one RUC; --apply merges via supersede (audited)`)
}

// cmdTenantList prints the deterministic operator tenant enumeration (FR-TEN-1).
// Read-only; emits ids/counts only — never topic keys or narrative.
func cmdTenantList(args []string) int {
	fs := flag.NewFlagSet("tenant list", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	fs.Usage = func() { fmt.Fprintln(fs.Output(), "usage: drenyra-engram tenant list [--db <path>]") }
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
	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()
	result, err := st.TenantList(context.Background())
	if err != nil {
		return fail("%v", err)
	}
	return emit(result)
}

// cmdTenantConsolidate detects — and with --apply merges — topic-key drift
// within ONE exact tenant scope (FR-TEN-2/4/5). Default is dry-run (ZERO
// writes); --apply supersedes each drifted chain head into the canonical chain
// head via the existing audited supersede path. --dry-run and --apply are
// mutually exclusive (usage error 2).
func cmdTenantConsolidate(args []string) int {
	fs := flag.NewFlagSet("tenant consolidate", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	ruc := fs.String("ruc", "", "company RUC (exactly 11 digits) — REQUIRED")
	period := fs.String("period", "", "fiscal period YYYYMM (optional; empty = whole tenant)")
	dryRun := fs.Bool("dry-run", false, "report drift candidates only (default; ZERO writes)")
	apply := fs.Bool("apply", false, "merge drifted chains into the canonical chain (audited supersede)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram tenant consolidate --ruc <11 digits> [--period <YYYYMM>] [--dry-run | --apply] [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--ruc": true, "--period": true})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 || *ruc == "" {
		fs.Usage()
		return 2
	}
	if *dryRun && *apply {
		fmt.Fprintln(os.Stderr, "drenyra-engram: --dry-run and --apply are mutually exclusive")
		return 2
	}
	scope, err := cliCompanyScope(*ruc, *period)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drenyra-engram: %v\n", err)
		return 2
	}
	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	groups, err := st.DriftCandidates(context.Background(), scope)
	if err != nil {
		return fail("%v", err)
	}
	report := core.ConsolidateReport{
		RUC:              *ruc,
		Period:           *period,
		DryRun:           !*apply,
		DriftGroups:      groups,
		TotalDriftGroups: len(groups),
	}
	for _, g := range groups {
		report.TotalDriftedChains += len(g.Drifted)
	}

	if !*apply {
		return emit(report)
	}

	// --apply: merge each drifted chain into the canonical chain via the
	// existing audited supersede path (FR-TEN-5/6). Each merge is one atomic
	// supersede act; failures are reported per-merge and the command exits
	// non-zero (successful merges are NOT rolled back — documented semantics).
	api := server.New(st, "cli")
	source := core.Source{System: "cli", ActorID: "tenant-consolidate", ActorKind: core.ActorKindAgent}
	merges := []map[string]any{}
	failed := 0
	for _, group := range groups {
		// Every merge stays inside ONE exact period scope (FR-TEN-7): the group
		// carries its period; the canonical/ drifted heads resolve under that
		// exact (tenant, period) tuple, never across periods.
		groupScope := scope
		groupScope.Period = group.Period
		canonicalHead, err := chainHead(st, groupScope, group.Canonical)
		if err != nil {
			merges = append(merges, map[string]any{
				"from": group.Canonical, "to": "", "error": err.Error(),
			})
			failed++
			continue
		}
		for _, drifted := range group.Drifted {
			driftedHead, err := chainHead(st, groupScope, drifted.TopicKey)
			if err != nil {
				merges = append(merges, map[string]any{
					"from": drifted.TopicKey, "to": group.Canonical, "error": err.Error(),
				})
				failed++
				continue
			}
			output, err := api.Supersede(driftedHead.Identity.ID, canonicalHead.Identity.ID, source)
			if err != nil {
				merges = append(merges, map[string]any{
					"from": drifted.TopicKey, "to": group.Canonical, "error": err.Error(),
				})
				failed++
				continue
			}
			merges = append(merges, map[string]any{
				"from":       drifted.TopicKey,
				"to":         group.Canonical,
				"superseded": output.ID,
				"target":     output.TargetID,
			})
		}
	}
	report.DriftGroups = nil // apply mode: the merge ledger replaces the report
	out := map[string]any{
		"ruc":    *ruc,
		"period": *period,
		"dryRun": false,
		"merges": merges,
		"failed": failed,
	}
	if failed > 0 {
		_ = emit(out)
		return 1
	}
	return emit(out)
}

// chainHead resolves the CURRENT head (latest revision, non-terminal status)
// of the chain whose topic key equals key under the exact scope. Terminal
// chains (rejected/superseded/voided heads) yield an error — a consolidate
// merge never routes readers through a dead chain.
func chainHead(st interface {
	FindByScope(core.Scope) ([]core.AccountingMemory, error)
}, scope core.Scope, topicKey string) (core.AccountingMemory, error) {
	memories, err := st.FindByScope(scope)
	if err != nil {
		return core.AccountingMemory{}, err
	}
	var head core.AccountingMemory
	found := false
	for _, m := range memories {
		if m.Identity.TopicKey != topicKey {
			continue
		}
		switch m.Status {
		case core.StatusActive, core.StatusPendingReview:
			if !found || m.Revision > head.Revision {
				head = m
				found = true
			}
		}
	}
	if !found {
		return core.AccountingMemory{}, fmt.Errorf("chain %q has no active head under the exact scope", topicKey)
	}
	return head, nil
}
