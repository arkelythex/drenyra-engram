// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This CLI never reads, writes or computes a
// monetary value — observation content is structured text
// (What/Why/Where/Learned), and there are no monetary fields in the model.
//
// drenyra-engram — the standalone Go engine CLI (ADR-001, v0.2 foundation).
//
// Non-authorization boundary (contracts/provenance.md): this public surface
// has deliberately NO authorize/approve/allow command or API. Memory informs
// decisions; it never authorizes them. Any authorization-sounding command is
// rejected as an unknown command (usage error, exit 2).
//
// Scope model for the CLI surface: the CLI identifies a company by RUC. In
// this slice it derives the exact company scope as
// {kind: company, organizationId: "cli", companyId: <ruc>, ruc: <ruc>,
// period: <--period if given>}. Scope stays exact (scope.md: scope is part of
// identity); a search or context query without --period only matches
// observations saved without a period.
//
// Exit codes: 0 ok, 1 runtime error, 2 usage error.
//
// Lifecycle surface (contracts/lifecycle.md): review/promote/supersede move
// an observation along the only legal chain draft → reviewed → promoted →
// superseded; supersede REQUIRES a --target and records a `supersedes`
// relation. compare reports identity/scope/content deltas plus a relation
// verdict between two stored observations.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/search"
	"github.com/arkelythex/drenyra-engram/internal/store"
)

// cliOrganizationID is the fixed organization identity of the CLI surface in
// this slice; companyId is derived from the RUC (see the package comment).
const cliOrganizationID = "cli"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		printUsage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "save":
		return cmdSave(args[1:])
	case "search":
		return cmdSearch(args[1:])
	case "context":
		return cmdContext(args[1:])
	case "doctor":
		return cmdDoctor(args[1:])
	case "compare":
		return cmdCompare(args[1:])
	case "review":
		return cmdReview(args[1:])
	case "promote":
		return cmdPromote(args[1:])
	case "supersede":
		return cmdSupersede(args[1:])
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return 0
	default:
		// This branch also covers authorize/approve/allow: they are not
		// commands, ever. Memory never authorizes (contracts/provenance.md).
		fmt.Fprintf(os.Stderr, "drenyra-engram: unknown command %q\n", args[0])
		printUsage(os.Stderr)
		return 2
	}
}

func cmdSave(args []string) int {
	fs := flag.NewFlagSet("save", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	fs.Usage = func() { fmt.Fprintln(fs.Output(), "usage: drenyra-engram save <json-file> [--db <path>]") }
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true})); err != nil {
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

	var input core.SaveInput
	data, err := os.ReadFile(rest[0])
	if err != nil {
		return fail("read %s: %v", rest[0], err)
	}
	if err := json.Unmarshal(data, &input); err != nil {
		return fail("parse %s: %v", rest[0], err)
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	result, err := st.Save(input)
	if err != nil {
		return fail("%v", err)
	}
	return emit(result)
}

func cmdSearch(args []string) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	company := fs.String("company", "", "company RUC (exactly 11 digits)")
	period := fs.String("period", "", "fiscal period YYYYMM (optional)")
	any := fs.Bool("any", false, "match ANY query token (default: match ALL)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram search <query> --company <ruc> [--period <YYYYMM>] [--any] [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--company": true, "--period": true})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 || *company == "" {
		fs.Usage()
		return 2
	}
	scope, err := cliCompanyScope(*company, *period)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drenyra-engram: %v\n", err)
		return 2
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	matchMode := search.MatchAll
	if *any {
		matchMode = search.MatchAny
	}
	results, err := search.ScopeFirst(st, search.Input{
		Query:     rest[0],
		Scope:     scope,
		MatchMode: matchMode,
	})
	if err != nil {
		return fail("%v", err)
	}
	if results == nil {
		results = []search.Result{}
	}
	return emit(results)
}

func cmdContext(args []string) int {
	fs := flag.NewFlagSet("context", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	period := fs.String("period", "", "fiscal period YYYYMM (optional)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram context <ruc> [--period <YYYYMM>] [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--period": true})); err != nil {
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
	scope, err := cliCompanyScope(rest[0], *period)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drenyra-engram: %v\n", err)
		return 2
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	observations, err := st.FindByScope(scope)
	if err != nil {
		return fail("%v", err)
	}
	// Context surfaces the CURRENT memory: latest revision per
	// (topicKey, exact scope) chain, never the full revision history.
	current := search.LatestPerChain(observations)
	if current == nil {
		current = []core.Observation{}
	}
	return emit(current)
}

func cmdDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	fs.Usage = func() { fmt.Fprintln(fs.Output(), "usage: drenyra-engram doctor [--db <path>]") }
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

	st, err := store.Open(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	report, err := st.Doctor()
	if err != nil {
		return fail("%v", err)
	}
	return emit(report)
}

// ──────────────────────────────────────────────
// compare — identity / scope / content deltas + relation verdict
// ──────────────────────────────────────────────

// compareOutput is the JSON shape of the compare command.
type compareOutput struct {
	IDA             string               `json:"idA"`
	IDB             string               `json:"idB"`
	IdentityMatch   bool                 `json:"identityMatch"`
	ScopeMatch      string               `json:"scopeMatch"`
	StatusA         core.AuthorityStatus `json:"statusA"`
	StatusB         core.AuthorityStatus `json:"statusB"`
	ContentDeltas   contentDeltas        `json:"contentDeltas"`
	RelationVerdict string               `json:"relationVerdict"`
}

// contentDeltas flags which of the four structured fields differ between the
// two observations (contracts/memory.md rule 1: What/Why/Where/Learned).
type contentDeltas struct {
	What    bool `json:"what"`
	Why     bool `json:"why"`
	Where   bool `json:"where"`
	Learned bool `json:"learned"`
}

func cmdCompare(args []string) int {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	fs.Usage = func() { fmt.Fprintln(fs.Output(), "usage: drenyra-engram compare <idA> <idB> [--db <path>]") }
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 2 {
		fs.Usage()
		return 2
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	a, ok := st.FindByID(rest[0])
	if !ok {
		return fail("OBSERVATION_NOT_FOUND: %s", rest[0])
	}
	b, ok := st.FindByID(rest[1])
	if !ok {
		return fail("OBSERVATION_NOT_FOUND: %s", rest[1])
	}

	identityMatch := a.Identity.ID == b.Identity.ID || a.Identity.TopicKey == b.Identity.TopicKey

	return emit(compareOutput{
		IDA:             a.Identity.ID,
		IDB:             b.Identity.ID,
		IdentityMatch:   identityMatch,
		ScopeMatch:      compareScopeMatch(a.Scope, b.Scope),
		StatusA:         a.AuthorityStatus,
		StatusB:         b.AuthorityStatus,
		ContentDeltas:   compareContentDeltas(a.Content, b.Content),
		RelationVerdict: compareRelationVerdict(st, a, b, identityMatch),
	})
}

// compareScopeMatch reports how two scopes relate: "exact" (equal scope per
// core.ScopeEquals — period participates in equality), "partial" (same
// company/RUC with a different organization or period) or "none" otherwise.
func compareScopeMatch(a, b core.Scope) string {
	if core.ScopeEquals(a, b) {
		return "exact"
	}
	if a.Kind == core.ScopeKindCompany && b.Kind == core.ScopeKindCompany &&
		a.CompanyID == b.CompanyID && a.RUC == b.RUC &&
		(a.OrganizationID != b.OrganizationID || a.Period != b.Period) {
		return "partial"
	}
	return "none"
}

func compareContentDeltas(a, b core.Content) contentDeltas {
	return contentDeltas{
		What:    a.What != b.What,
		Why:     a.Why != b.Why,
		Where:   a.Where != b.Where,
		Learned: a.Learned != b.Learned,
	}
}

// compareRelationVerdict decides how the two observations relate:
//   - "supersedes" — the relations table records A→B as `supersedes` AND A (the
//     superseded source) is stored as superseded — a completed supersede pair.
//     The successor B is typically draft/promoted, never superseded itself.
//   - "related" — the observations share a topicKey;
//   - "not_conflict" — otherwise.
//
// The supersedes check runs first so a completed supersede pair wins over the
// weaker shared-topicKey signal.
func compareRelationVerdict(st store.Store, a, b core.Observation, identityMatch bool) string {
	if rel, ok := st.RelationBetween(a.Identity.ID, b.Identity.ID); ok && rel == string(core.RelationSupersedes) && a.AuthorityStatus == core.StatusSuperseded {
		return "supersedes"
	}
	if identityMatch {
		return "related"
	}
	return "not_conflict"
}

// ──────────────────────────────────────────────
// review / promote — adjacent-forward status transitions
// ──────────────────────────────────────────────

// transitionOutput is the JSON shape of review and promote.
type transitionOutput struct {
	ID       string               `json:"id"`
	From     core.AuthorityStatus `json:"from"`
	To       core.AuthorityStatus `json:"to"`
	Revision int                  `json:"revision"`
}

func cmdReview(args []string) int {
	return cmdStatusTransition("review", core.StatusReviewed, args)
}

func cmdPromote(args []string) int {
	return cmdStatusTransition("promote", core.StatusPromoted, args)
}

// cmdStatusTransition implements review/promote: a single adjacent-forward
// status transition. The observation is read first so the output can report the
// pre-transition status; the transition itself is atomic and fails closed on
// illegal moves (internal/core/lifecycle.go), leaving the observation unchanged.
func cmdStatusTransition(name string, to core.AuthorityStatus, args []string) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	actor := fs.String("actor", "cli", "actor name recorded in the audit trail (default cli)")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "usage: drenyra-engram %s <id> [--actor <name>] [--db <path>]\n", name)
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--actor": true})); err != nil {
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

	st, err := store.Open(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	before, ok := st.FindByID(rest[0])
	if !ok {
		return fail("OBSERVATION_NOT_FOUND: %s", rest[0])
	}
	updated, err := core.ApplyTransition(st, rest[0], to, core.TransitionMeta{
		Actor:     *actor,
		Timestamp: nowISO(),
	})
	if err != nil {
		return fail("%v", err)
	}
	return emit(transitionOutput{
		ID:       updated.Identity.ID,
		From:     before.AuthorityStatus,
		To:       updated.AuthorityStatus,
		Revision: updated.Revision,
	})
}

// ──────────────────────────────────────────────
// supersede — promoted → superseded with a REQUIRED target
// ──────────────────────────────────────────────

// supersedeOutput is the JSON shape of the supersede command.
type supersedeOutput struct {
	ID       string               `json:"id"`
	From     core.AuthorityStatus `json:"from"`
	To       core.AuthorityStatus `json:"to"`
	TargetID string               `json:"targetId"`
}

func cmdSupersede(args []string) int {
	fs := flag.NewFlagSet("supersede", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	actor := fs.String("actor", "cli", "actor name recorded in the audit trail (default cli)")
	target := fs.String("target", "", "REQUIRED replacing observation id this one routes readers to")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram supersede <id> --target <targetId> [--actor <name>] [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--actor": true, "--target": true})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 || *target == "" {
		fs.Usage()
		return 2
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	before, ok := st.FindByID(rest[0])
	if !ok {
		return fail("OBSERVATION_NOT_FOUND: %s", rest[0])
	}
	updated, err := core.Supersede(core.SupersedeInput{
		Store:         st,
		ObservationID: rest[0],
		TargetID:      *target,
		Actor:         *actor,
		Timestamp:     nowISO(),
	})
	if err != nil {
		return fail("%v", err)
	}
	return emit(supersedeOutput{
		ID:       updated.Identity.ID,
		From:     before.AuthorityStatus,
		To:       updated.AuthorityStatus,
		TargetID: *target,
	})
}

// nowISO is the CLI's event timestamp: current UTC time in RFC3339, which the
// core timestamp grammar accepts (contracts/provenance.md rule 3: every state
// traces to actor+time).
func nowISO() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// reorderFlags moves flag tokens to the front of args so flag.FlagSet can parse
// them, preserving positional arguments in order. Go's flag package stops at
// the first positional, but the CLI signatures put positionals before flags
// (e.g. `save <json-file> --db <path>`); valueFlags names the flags that
// consume the following token as their value.
func reorderFlags(args []string, valueFlags map[string]bool) []string {
	flags := make([]string, 0, len(args))
	positional := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") && arg != "-" {
			flags = append(flags, arg)
			name := arg
			if eq := strings.IndexByte(arg, '='); eq >= 0 {
				name = arg[:eq]
			}
			if valueFlags[name] && !strings.Contains(arg, "=") && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		positional = append(positional, arg)
	}
	return append(flags, positional...)
}

// cliCompanyScope derives the exact company scope for the CLI surface
// (organizationId fixed, companyId derived from the RUC). Malformed RUC or
// period is a usage error.
func cliCompanyScope(ruc, period string) (core.Scope, error) {
	if !core.IsValidRUC(ruc) {
		return core.Scope{}, fmt.Errorf("invalid RUC %q: expected exactly 11 digits", ruc)
	}
	if period != "" && !core.IsValidPeriod(period) {
		return core.Scope{}, fmt.Errorf("invalid period %q: expected YYYYMM with month 01-12", period)
	}
	return core.Scope{
		Kind:           core.ScopeKindCompany,
		OrganizationID: cliOrganizationID,
		CompanyID:      ruc,
		RUC:            ruc,
		Period:         period,
	}, nil
}

func defaultDBPath() string {
	if path := os.Getenv("DRENYRA_ENGRAM_DB"); path != "" {
		return path
	}
	return "./engram.db"
}

func emit(value any) int {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fail("encode output: %v", err)
	}
	return 0
}

func fail(format string, args ...any) int {
	fmt.Fprintf(os.Stderr, "drenyra-engram: "+format+"\n", args...)
	return 1
}

func printUsage(w *os.File) {
	fmt.Fprintln(w, `drenyra-engram — institutional accounting memory engine (v0.2 Go foundation)

Usage:
  drenyra-engram save <json-file> [--db <path>]
  drenyra-engram search <query> --company <ruc> [--period <YYYYMM>] [--any] [--db <path>]
  drenyra-engram context <ruc> [--period <YYYYMM>] [--db <path>]
  drenyra-engram doctor [--db <path>]
  drenyra-engram compare <idA> <idB> [--db <path>]
  drenyra-engram review <id> [--actor <name>] [--db <path>]
  drenyra-engram promote <id> [--actor <name>] [--db <path>]
  drenyra-engram supersede <id> --target <targetId> [--actor <name>] [--db <path>]

Flags:
  --db <path>      SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)
  --company <ruc>  company RUC (exactly 11 digits); companyId is derived from it
  --period <YYYYMM> fiscal period; omitted scopes only match period-less observations
  --any            match ANY query token (default: match ALL)
  --actor <name>   actor recorded in the audit trail (default cli)
  --target <id>    replacing observation for supersede (REQUIRED)

Lifecycle: review/promote/supersede move an observation along the only legal
chain draft → reviewed → promoted → superseded; supersede records a
supersedes relation to --target. compare reports identity/scope/content
deltas and a relation verdict for two stored observations.

The engine surface is non-authorizing (contracts/provenance.md): there is no
authorize/approve/allow command. Memory guides; it never authorizes.

Exit codes: 0 ok, 1 runtime error, 2 usage error.`)
}
