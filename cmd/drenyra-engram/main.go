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
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/arkelythex/drenyra-engram/internal/auth"
	"github.com/arkelythex/drenyra-engram/internal/authz"
	"github.com/arkelythex/drenyra-engram/internal/core"
	"github.com/arkelythex/drenyra-engram/internal/receipts"
	"github.com/arkelythex/drenyra-engram/internal/search"
	"github.com/arkelythex/drenyra-engram/internal/server"
	"github.com/arkelythex/drenyra-engram/internal/store"
	"github.com/arkelythex/drenyra-engram/internal/sync"
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
	case "approve":
		return cmdApprove(args[1:])
	case "review":
		return cmdReview(args[1:])
	case "rule":
		return cmdRule(args[1:])
	case "reconstructibility":
		return cmdReconstructibility(args[1:])
	case "auth":
		return cmdAuth(args[1:])
	case "keys":
		return cmdKeys(args[1:])
	case "judge":
		return cmdJudge(args[1:])
	case "reconcile":
		return cmdReconcile(args[1:])
	case "reject":
		return cmdReject(args[1:])
	case "void":
		return cmdVoid(args[1:])
	case "link-evidence":
		return cmdLinkEvidence(args[1:])
	case "object":
		return cmdObject(args[1:])
	case "hold":
		return cmdHold(args[1:])
	case "retention-policy":
		return cmdRetentionPolicy(args[1:])
	case "purge":
		return cmdPurge(args[1:])
	case "export":
		return cmdExport(args[1:])
	case "period-summary":
		return cmdPeriodSummary(args[1:])
	case "compare-periods":
		return cmdComparePeriods(args[1:])
	case "close":
		return cmdClose(args[1:])
	case "timeline":
		return cmdTimeline(args[1:])
	case "supersede":
		return cmdSupersede(args[1:])
	case "mcp":
		return cmdMCP(args[1:])
	case "serve":
		return cmdServe(args[1:])
	case "sync":
		return cmdSync(args[1:])
	case "verify":
		return cmdVerify(args[1:])
	case "tenant":
		return cmdTenant(args[1:])
	case "encrypt":
		return cmdEncrypt(args[1:])
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

// openStore opens the store at path with the receipt signer attached (v0.4.0
// Step 3 atomic emission): every covered mutation on the CLI surface mints an
// immutable Ed25519 receipt inside its own transaction, below the adapter. The
// signer is attached right after Open because the signer itself needs the opened
// store (the store↔signer construction cycle); nil signer → no emission. The
// keyring is created lazily on the FIRST covered mutation. The WORM evidence
// object root (v0.7.0) follows the same explicit convention as the DB path:
// $DRENYRA_ENGRAM_OBJECTS when set, otherwise <dir-of-db>/objects.
func openStore(path string) (*store.SQLiteStore, error) {
	return openStoreWithRoot(path, "")
}

// openStoreWithRoot opens the store at path with an EXPLICIT WORM objects root
// (the CLI's real --objects flag — v0.7.x hardening — honored by object
// store/get, doctor and verify object); an empty root falls back to the
// default convention (defaultObjectsRoot). Signer semantics are identical to
// openStore.
func openStoreWithRoot(dbPath, objectsRoot string) (*store.SQLiteStore, error) {
	if objectsRoot == "" {
		objectsRoot = defaultObjectsRoot(dbPath)
	}
	// At-rest content encryption (sdd-060-at-rest-encryption, FR-ENC-5): the
	// operator master key comes from DRENYRA_ENCRYPTION_MASTER_KEY (hex or
	// base64 of 32 bytes). When set, company-scope content is encrypted at rest
	// with per-tenant derived keys and reads fail closed without it. Absent →
	// encryption disabled (the default — legacy deployments unchanged).
	opts := store.Options{}
	if raw := os.Getenv("DRENYRA_ENCRYPTION_MASTER_KEY"); raw != "" {
		key, err := decodeEncryptionMasterKey(raw)
		if err != nil {
			return nil, err
		}
		opts.EncryptionKey = key
	}
	st, err := store.OpenWithObjectsAndOptions(dbPath, objectsRoot, opts)
	if err != nil {
		return nil, err
	}
	st.SetReceiptSigner(receipts.NewSigner(st, receipts.DefaultKeyringPath()))
	return st, nil
}

// decodeEncryptionMasterKey decodes DRENYRA_ENCRYPTION_MASTER_KEY (hex or
// base64) into the 32-byte master key; malformed or non-32-byte material fails
// closed (FR-ENC-5).
func decodeEncryptionMasterKey(raw string) ([]byte, error) {
	key, err := hex.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		key, err = base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
		if err != nil {
			return nil, errors.New("INVALID_ENCRYPTION_KEY: DRENYRA_ENCRYPTION_MASTER_KEY must be hex or base64 of exactly 32 bytes")
		}
	}
	if len(key) != 32 {
		return nil, errors.New("INVALID_ENCRYPTION_KEY: DRENYRA_ENCRYPTION_MASTER_KEY must decode to exactly 32 bytes")
	}
	return key, nil
}

// defaultObjectsRoot resolves the safe explicit local WORM object root for a
// DB path: $DRENYRA_ENGRAM_OBJECTS when set, otherwise <dir-of-db>/objects
// (consistent with the repo's env-or-default convention — never $HOME, never a
// shared directory).
func defaultObjectsRoot(dbPath string) string {
	if root := os.Getenv("DRENYRA_ENGRAM_OBJECTS"); root != "" {
		return root
	}
	return filepath.Join(filepath.Dir(dbPath), "objects")
}

// oidcConfigFromEnv builds the stateless OIDC access-token configuration of
// the serve surface from the DRENYRA_OIDC_* environment variables. It FAILS
// CLOSED: when ANY oidc variable is set the whole set must be complete and
// valid (issuer + audience required, https URLs, bounded clock skew), and a
// partial or invalid set is an error — the server never starts with a partial
// trust contract. When NONE are set, oidc is disabled (nil, nil).
//
// Optional variables follow the repo env-or-default convention:
//   - DRENYRA_OIDC_ISSUER (required when oidc is enabled) — the exact `iss`.
//   - DRENYRA_OIDC_AUDIENCE (required) — the exact resource-server `aud`.
//   - DRENYRA_OIDC_JWKS_URL (optional) — defaults to <issuer>/.well-known/jwks.json.
//   - DRENYRA_OIDC_CLAIM_TENANT (optional) — defaults to tenant_id.
//   - DRENYRA_OIDC_CLAIM_COMPANY (optional) — defaults to company_id.
//   - DRENYRA_OIDC_CLOCK_SKEW (optional) — Go duration (e.g. 30s); bounded to
//     MaxOIDCClockSkew; defaults to 30s.
func oidcConfigFromEnv() (*auth.OIDCConfig, error) {
	var (
		cfg     auth.OIDCConfig
		enabled bool
	)
	if v := os.Getenv("DRENYRA_OIDC_ISSUER"); v != "" {
		enabled = true
		cfg.Issuer = v
	}
	if v := os.Getenv("DRENYRA_OIDC_AUDIENCE"); v != "" {
		enabled = true
		cfg.Audience = v
	}
	if v := os.Getenv("DRENYRA_OIDC_JWKS_URL"); v != "" {
		enabled = true
		cfg.JWKSURL = v
	}
	if v := os.Getenv("DRENYRA_OIDC_CLAIM_TENANT"); v != "" {
		enabled = true
		cfg.TenantClaim = v
	}
	if v := os.Getenv("DRENYRA_OIDC_CLAIM_COMPANY"); v != "" {
		enabled = true
		cfg.CompanyClaim = v
	}
	if v := os.Getenv("DRENYRA_OIDC_CLOCK_SKEW"); v != "" {
		enabled = true
		skew, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("DRENYRA_OIDC_CLOCK_SKEW: %v", err)
		}
		cfg.ClockSkew = skew
	}
	if !enabled {
		return nil, nil
	}
	// NormalizeOIDCConfig is the single fail-closed gate: missing issuer or
	// audience, non-https URLs or an out-of-bounds skew all error here.
	if _, err := auth.NormalizeOIDCConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid oidc configuration: %w", err)
	}
	return &cfg, nil
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

	st, err := openStore(*dbPath)
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
	// scope-param-rollout FR-SPR-3/D-SPR-4: when a session credential resolves
	// to a verified approval principal, the derived scope MUST match the
	// principal's membership before any store data access — typed denial +
	// non-zero exit otherwise; session-less operation unchanged (FD-SPR-3).
	if code := cliBindScope(scope, *dbPath); code != 0 {
		return code
	}

	st, err := openStore(*dbPath)
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
	// scope-param-rollout FR-SPR-3/D-SPR-4: session-present commands bind the
	// derived scope to the principal's membership before store data access.
	if code := cliBindScope(scope, *dbPath); code != 0 {
		return code
	}

	st, err := openStore(*dbPath)
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
		current = []core.AccountingMemory{}
	}
	return emit(current)
}

func cmdDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	objects := fs.String("objects", "", "WORM evidence object root (default <dir-of-db>/objects or $DRENYRA_ENGRAM_OBJECTS)")
	drillCopy := fs.String("drill-copy", "", "marked drill copy for the FULL diagnostic path (mutually exclusive with --db; requires --snapshot-manifest)")
	manifest := fs.String("snapshot-manifest", "", "drill manifest (the adjacent <copy>.drenyra-drill.json marker) for --drill-copy")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram doctor [--db <path>] [--objects <dir>] | doctor --drill-copy <copy.db> --snapshot-manifest <manifest.json>")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--objects": true, "--drill-copy": true, "--snapshot-manifest": true})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}

	// Full diagnostic path (design D-6): doctor --drill-copy <copy.db>
	// --snapshot-manifest <manifest.json> runs integrity_check + foreign_key_check
	// on a MARKED drill copy only. It is mutually exclusive with --db — the full
	// path never opens a live database — and both flags are required together.
	explicitDB := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "db" {
			explicitDB = true
		}
	})
	if *drillCopy != "" || *manifest != "" {
		if *drillCopy == "" || *manifest == "" {
			fmt.Fprintf(os.Stderr, "drenyra-engram: DRILL_COPY_REQUIRED: --drill-copy and --snapshot-manifest must be used together\n")
			return 2
		}
		if explicitDB {
			fmt.Fprintf(os.Stderr, "drenyra-engram: INVALID_DRILL_PATH: --db cannot be combined with --drill-copy (the full path never opens a live database)\n")
			return 2
		}
		st, err := store.OpenDrillCopy(*drillCopy, *manifest)
		if err != nil {
			return fail("%v", err)
		}
		defer func() { _ = st.Close() }()
		report, err := st.Doctor(context.Background(), store.DoctorOptions{Mode: store.ModeFull})
		if err != nil {
			return fail("%v", err)
		}
		return emit(report)
	}

	st, err := openStoreWithRoot(*dbPath, *objects)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	report, err := st.Doctor(context.Background(), store.DoctorOptions{Mode: store.ModeRoutine})
	if err != nil {
		return fail("%v", err)
	}
	return emit(report)
}

// ──────────────────────────────────────────────
// compare — identity / scope / content deltas + relation verdict
// ──────────────────────────────────────────────

// compareOutput mirrors server.CompareOutput on the CLI surface (same JSON
// shape, same verdict semantics — the CLI delegates to the shared domain
// services so MCP/HTTP/CLI verdicts stay byte-identical).
type compareOutput = server.CompareOutput

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

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	api := server.New(st, "cli")
	output, err := api.Compare(rest[0], rest[1])
	if err != nil {
		return fail("%v", err)
	}
	return emit(output)
}

// ──────────────────────────────────────────────
// review / promote — adjacent-forward status transitions
// ──────────────────────────────────────────────

// transitionOutput mirrors server.TransitionOutput on the CLI surface.
type transitionOutput = server.TransitionOutput

// cliSource builds a HUMAN actor source for the CLI lifecycle commands: the
// CLI is the professional's surface — approvals from the terminal are human
// approvals (the gate demands it).
func cliSource(actor string) core.Source {
	if actor == "" {
		actor = "cli"
	}
	return core.Source{System: "cli", ActorID: actor, ActorKind: core.ActorKindHuman}
}

func cmdApprove(args []string) int {
	// The authenticated approval command (v0.4.0 Step 1, ADR-003): the principal
	// is DERIVED from the stored CLI session (auth login), never declared by the
	// caller — there is deliberately NO --actor flag on this command (caller-
	// supplied authority is gone). Each invocation generates a fresh requestId.
	fs := flag.NewFlagSet("approve", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	expectedEnvelope := fs.String("expected-envelope", "", "REQUIRED envelope hash the reviewer actually saw")
	reason := fs.String("reason", "", "REQUIRED approval justification")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram approve <memory-id> --expected-envelope <hash> --reason <text> [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--expected-envelope": true, "--reason": true})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 || strings.TrimSpace(*expectedEnvelope) == "" || strings.TrimSpace(*reason) == "" {
		fs.Usage()
		return 2
	}

	token, err := loadSessionToken()
	if err != nil {
		return fail("AUTHENTICATION_REQUIRED: no authenticated CLI session — run `drenyra-engram auth login --token <token> --db <path>` first (%v)", err)
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	resolver := &auth.Resolver{Sessions: st, Mode: auth.RuntimeProduction}
	principal, err := resolver.Authenticate(context.Background(), auth.AuthenticationAssertion{
		Method:     auth.AuthMethodSession,
		Credential: token,
	})
	if err != nil {
		return fail("%v", err)
	}

	requestID, err := newRequestID()
	if err != nil {
		return fail("generate request id: %v", err)
	}
	result, err := server.ApproveMemory(context.Background(), st, authz.NewApprovalPolicy(), core.ApproveMemoryCommand{
		MemoryID:             rest[0],
		ExpectedEnvelopeHash: *expectedEnvelope,
		Reason:               *reason,
		RequestID:            requestID,
	}, principal)
	if err != nil {
		return fail("%v", err)
	}
	return emit(result)
}

func cmdReject(args []string) int {
	return cmdGatedTransition("reject", args, func(api *server.API, id string, src core.Source) (transitionOutput, error) {
		m, err := api.Reject(id, src)
		if err != nil {
			return transitionOutput{}, err
		}
		return transitionOutput{ID: m.Identity.ID, From: core.StatusPendingReview, To: m.Status, Revision: m.Revision}, nil
	})
}

func cmdVoid(args []string) int {
	return cmdGatedTransition("void", args, func(api *server.API, id string, src core.Source) (transitionOutput, error) {
		before, err := api.Get(id)
		if err != nil {
			return transitionOutput{}, err
		}
		m, err := api.Void(id, src)
		if err != nil {
			return transitionOutput{}, err
		}
		return transitionOutput{ID: m.Identity.ID, From: before.Status, To: m.Status, Revision: m.Revision}, nil
	})
}

// cmdGatedTransition implements approve/reject/void: the human-gated lifecycle
// commands via the shared domain services. The gate fails closed on machine
// actors and illegal transitions, leaving the memory unchanged.
func cmdGatedTransition(name string, args []string, run func(*server.API, string, core.Source) (transitionOutput, error)) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	actor := fs.String("actor", "cli", "human actor id recorded in the audit trail (default cli)")
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

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	api := server.New(st, "cli")
	output, err := run(api, rest[0], cliSource(*actor))
	if err != nil {
		return fail("%v", err)
	}
	return emit(output)
}

// ──────────────────────────────────────────────
// keys — signing-key lifecycle (v0.4.0 Step 3 keys commit)
// ──────────────────────────────────────────────

// cmdKeys dispatches the signing-key lifecycle subcommands: init ensures an
// active key (generating the user-owned 0600 keyring on first use), show
// prints the active key id, its public key and lifecycle timestamps (NEVER
// the seed), and rotate durably activates a new key while revoking the old
// public key in one DB transaction.
func cmdKeys(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: drenyra-engram keys init")
		fmt.Fprintln(os.Stderr, "       drenyra-engram keys show")
		fmt.Fprintln(os.Stderr, "       drenyra-engram keys rotate --db <path>")
		return 2
	}
	switch args[0] {
	case "init":
		return cmdKeysInit(args[1:])
	case "show":
		return cmdKeysShow(args[1:])
	case "rotate":
		return cmdKeysRotate(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "drenyra-engram: unknown keys subcommand %q\n", args[0])
		return 2
	}
}

// keyringPathForCLI is the effective keyring location: $DRENYRA_ENGRAM_SIGNING_KEY
// when set (tests/CI), otherwise the platform config dir.
func keyringPathForCLI() string {
	return receipts.DefaultKeyringPath()
}

// cmdKeysInit ensures the ACTIVE key exists (first use generates a durable
// 0600 keyring) and prints its key id. Generation is also triggered by the
// first covered mutation in batch 2; init is the explicit, idempotent CLI.
func cmdKeysInit(args []string) int {
	fs := flag.NewFlagSet("keys init", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(fs.Output(), "usage: drenyra-engram keys init") }
	if err := fs.Parse(reorderFlags(args, nil)); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}
	kr, err := receipts.EnsureActiveKey(keyringPathForCLI())
	if err != nil {
		return fail("keys init: %v", err)
	}
	return emit(map[string]string{
		"keyId":     kr.ActiveKeyID,
		"createdAt": kr.CreatedAt(kr.ActiveKeyID),
	})
}

// cmdKeysShow prints the ACTIVE key id, its public key (hex) and lifecycle
// timestamps from the keyring file. The private seed is NEVER printed or
// exposed; the store is never opened (no side effect).
func cmdKeysShow(args []string) int {
	fs := flag.NewFlagSet("keys show", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(fs.Output(), "usage: drenyra-engram keys show") }
	if err := fs.Parse(reorderFlags(args, nil)); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}
	kr, err := receipts.LoadKeyring(keyringPathForCLI())
	if err != nil {
		if os.IsNotExist(err) {
			return fail("keys show: no signing key yet — run 'drenyra-engram keys init'")
		}
		return fail("keys show: %v", err)
	}
	keyID := kr.ActiveKeyID
	pub, err := kr.PublicKeyFor(keyID)
	if err != nil {
		return fail("keys show: %v", err)
	}
	return emit(map[string]string{
		"keyId":     keyID,
		"publicKey": hex.EncodeToString(pub),
		"createdAt": kr.CreatedAt(keyID),
		"revokedAt": kr.RevokedAt(keyID),
	})
}

// cmdKeysRotate durably creates and activates a new key, then registers it
// and revokes the old public key IN ONE DB transaction (docs/architecture/
// ed25519-receipts-step3.md "Signing-key lifecycle"). Rotation is explicit,
// never scheduled; old receipts stay verifiable.
func cmdKeysRotate(args []string) int {
	fs := flag.NewFlagSet("keys rotate", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	fs.Usage = func() { fmt.Fprintln(fs.Output(), "usage: drenyra-engram keys rotate --db <path>") }
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
		return fail("keys rotate: %v", err)
	}
	defer func() { _ = st.Close() }()
	res, err := receipts.Rotate(context.Background(), st, keyringPathForCLI())
	if err != nil {
		return fail("keys rotate: %v", err)
	}
	return emit(map[string]string{
		"keyId":         res.NewKeyID,
		"previousKeyId": res.OldKeyID,
		"createdAt":     res.CreatedAt,
		"revokedAt":     res.RevokedAt,
	})
}

// ──────────────────────────────────────────────
// auth — authenticated CLI sessions (v0.4.0 Step 1, ADR-003)
// ──────────────────────────────────────────────

// cmdAuth dispatches the authenticated-session subcommands: login validates a
// bearer token against the selected DB and stores it in a user-only config file;
// seed-local-dev provisions a local_dev identity + expiring session
// (DRENYRA_ENV=local_dev only, design section 8).
func cmdAuth(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: drenyra-engram auth login --token <token> [--db <path>]")
		fmt.Fprintln(os.Stderr, "       drenyra-engram auth seed-local-dev --db <path> --tenant <id> --company <id> --ruc <11 digits> --subject <id> --roles <list>")
		return 2
	}
	switch args[0] {
	case "login":
		return cmdAuthLogin(args[1:])
	case "seed-local-dev":
		return cmdAuthSeedLocalDev(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "drenyra-engram: unknown auth subcommand %q\n", args[0])
		return 2
	}
}

// sessionConfigPath is the CLI session file location: $DRENYRA_ENGRAM_SESSION
// when set (tests/CI), otherwise the platform config dir (os.UserConfigDir →
// ~/.config on Linux) under drenyra-engram/session.json. The file holds the RAW
// token with 0600 permissions; the store keeps only its SHA-256 hash (design
// section 3 — the CLI owns the raw credential and never writes a second hash).
func sessionConfigPath() string {
	if path := os.Getenv("DRENYRA_ENGRAM_SESSION"); path != "" {
		return path
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "drenyra-engram", "session.json")
	}
	return filepath.Join(".", ".drenyra-engram", "session.json")
}

// loadSessionToken reads the raw bearer token from the user-only session file.
func loadSessionToken() (string, error) {
	data, err := os.ReadFile(sessionConfigPath())
	if err != nil {
		return "", err
	}
	var file struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return "", fmt.Errorf("parse session file: %w", err)
	}
	if strings.TrimSpace(file.Token) == "" {
		return "", errors.New("session file carries no token")
	}
	return file.Token, nil
}

// writeSessionToken stores the RAW token in the user-only session file (0600);
// the directory is created 0700 so the credential never lives in a world-readable
// path.
func writeSessionToken(token string) error {
	path := sessionConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create session config dir: %w", err)
	}
	data, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		return fmt.Errorf("encode session file: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write session file: %w", err)
	}
	return nil
}

// cmdAuthLogin validates a bearer token against the store (SHA-256 →
// LookupByTokenHash → active, not revoked/expired, active membership) and, only
// on success, stores the RAW token in the user-only session file. Invalid tokens
// map to PRINCIPAL_INVALID; store failures surface with a clear message. Never
// stores a hash anywhere the CLI owns — the store already holds it.
func cmdAuthLogin(args []string) int {
	fs := flag.NewFlagSet("auth login", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	token := fs.String("token", "", "REQUIRED bearer token to validate")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram auth login --token <token> [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--token": true})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 || strings.TrimSpace(*token) == "" {
		fs.Usage()
		return 2
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("auth login: %v", err)
	}
	defer func() { _ = st.Close() }()

	resolver := &auth.Resolver{Sessions: st, Mode: auth.RuntimeProduction}
	principal, err := resolver.Authenticate(context.Background(), auth.AuthenticationAssertion{
		Method:     auth.AuthMethodSession,
		Credential: *token,
	})
	if err != nil {
		switch auth.Code(err) {
		case auth.CodePrincipalInvalid:
			return fail("auth login: PRINCIPAL_INVALID: the token does not resolve to an active, unexpired session")
		case auth.CodeMembershipInactive:
			return fail("auth login: MEMBERSHIP_INACTIVE: the session's membership is not active")
		default:
			return fail("auth login: %v", err)
		}
	}
	if err := writeSessionToken(*token); err != nil {
		return fail("auth login: %v", err)
	}
	return emit(map[string]any{
		"authenticated": true,
		"subjectId":     principal.SubjectID(),
		"sessionFile":   sessionConfigPath(),
	})
}

// cmdAuthSeedLocalDev provisions one local_dev identity + session (design
// section 8): it REQUIRES DRENYRA_ENV=local_dev (production mode rejects the
// command), inserts company/membership/roles via SeedIdentity plus one expiring
// local_dev session, prints the raw token ONCE on stdout and stores only its
// SHA-256 hash.
func cmdAuthSeedLocalDev(args []string) int {
	fs := flag.NewFlagSet("auth seed-local-dev", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	tenant := fs.String("tenant", "", "REQUIRED tenant (organization) id")
	company := fs.String("company", "", "REQUIRED company id")
	ruc := fs.String("ruc", "", "REQUIRED company RUC (exactly 11 digits)")
	subject := fs.String("subject", "", "REQUIRED subject (professional) id")
	roles := fs.String("roles", "", "REQUIRED comma-separated accounting roles")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram auth seed-local-dev --db <path> --tenant <id> --company <id> --ruc <11 digits> --subject <id> --roles <comma-separated>")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"--db": true, "--tenant": true, "--company": true, "--ruc": true, "--subject": true, "--roles": true,
	})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 || *tenant == "" || *company == "" || *subject == "" || *roles == "" {
		fs.Usage()
		return 2
	}
	if !core.IsValidRUC(*ruc) {
		fmt.Fprintln(os.Stderr, "drenyra-engram: auth seed-local-dev: invalid RUC: expected exactly 11 digits")
		return 2
	}
	var accountingRoles []auth.AccountingRole
	for _, raw := range splitCSV(*roles) {
		role := auth.AccountingRole(raw)
		if !isAccountingRole(role) {
			fmt.Fprintf(os.Stderr, "drenyra-engram: auth seed-local-dev: unknown role %q — expected accountant,senior_accountant,controller,tax_reviewer,authorized_tax_professional\n", raw)
			return 2
		}
		accountingRoles = append(accountingRoles, role)
	}

	// Explicit, isolated, rejected in production (design section 8).
	if os.Getenv("DRENYRA_ENV") != "local_dev" {
		return fail("auth seed-local-dev is only available with DRENYRA_ENV=local_dev; production mode rejects the command")
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("auth seed-local-dev: %v", err)
	}
	defer func() { _ = st.Close() }()

	membershipID, err := newRequestID()
	if err != nil {
		return fail("auth seed-local-dev: generate membership id: %v", err)
	}
	if err := st.SeedIdentity(store.IdentitySeed{
		TenantID:     *tenant,
		CompanyID:    *company,
		CompanyRUC:   *ruc,
		CompanyName:  "Local Dev",
		MembershipID: membershipID,
		SubjectID:    *subject,
		Roles:        accountingRoles,
	}); err != nil {
		return fail("auth seed-local-dev: %v", err)
	}

	token, err := newSessionToken()
	if err != nil {
		return fail("auth seed-local-dev: generate token: %v", err)
	}
	sessionID, err := newRequestID()
	if err != nil {
		return fail("auth seed-local-dev: generate session id: %v", err)
	}
	now := time.Now().UTC()
	expiresAt := now.Add(time.Hour)
	if err := st.SeedSession(store.SessionSeed{
		ID:                   sessionID,
		TokenHash:            sha256Hex(token),
		MembershipID:         membershipID,
		AuthenticationMethod: auth.AuthMethodLocalDev,
		AssuranceLevel:       auth.AssuranceStandard,
		AuthenticatedAt:      now.Format(time.RFC3339),
		ExpiresAt:            expiresAt.Format(time.RFC3339),
	}); err != nil {
		return fail("auth seed-local-dev: %v", err)
	}
	// The raw token is printed exactly ONCE; the store keeps only its hash.
	return emit(map[string]any{
		"token":     token,
		"sessionId": sessionID,
		"expiresAt": expiresAt.Format(time.RFC3339),
	})
}

// newRequestID returns a random (v4) UUID — the fresh idempotency key the CLI
// generates per approval invocation (mirrors the store's newUUID shape).
func newRequestID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// newSessionToken returns a fresh high-entropy bearer token (32 random bytes,
// hex-encoded) for the seed-local-dev session.
func newSessionToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// sha256Hex is the CLI's token-hash helper: raw credentials become SHA-256 hex
// BEFORE any store lookup; the raw value never leaves the CLI (design section 3).
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// isAccountingRole reports whether the role is one of the five accounting roles
// of the v0.4 policy ladder/track.
func isAccountingRole(r auth.AccountingRole) bool {
	switch r {
	case auth.RoleAccountant, auth.RoleSeniorAccountant, auth.RoleController,
		auth.RoleTaxReviewer, auth.RoleAuthorizedTaxProfessional:
		return true
	}
	return false
}

// ──────────────────────────────────────────────
// judge — authenticated judgment commands (v0.4.0 Step 2, design §7)
// ──────────────────────────────────────────────

// cmdJudge dispatches the adjudication surface: propose/withdraw carry the
// agent provenance source of the CLI caller (never authority); confirm/reject
// derive the principal ONLY from the stored 0600 CLI session — there is
// deliberately NO --actor/--subject/--role flag on them (caller-supplied
// authority is gone, the ADR-003 closure for adjudication); show is read-only.
func cmdJudge(args []string) int {
	if len(args) == 0 {
		printJudgeUsage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "propose":
		return cmdJudgePropose(args[1:])
	case "confirm":
		return cmdJudgeConfirm(args[1:])
	case "reject":
		return cmdJudgeReject(args[1:])
	case "withdraw":
		return cmdJudgeWithdraw(args[1:])
	case "show":
		return cmdJudgeShow(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "drenyra-engram: unknown judge subcommand %q\n", args[0])
		return 2
	}
}

// cliJudgeSource is the provenance-only source of the agent CLI judgment
// caller: {system: "cli", actorId: "cli", actorKind: "agent"}. It is
// provenance, never authority — confirm/reject derive the principal from the
// stored session, and only that verified principal may adjudicate (design
// §3/§7).
func cliJudgeSource() core.Source {
	return core.Source{System: "cli", ActorID: "cli", ActorKind: core.ActorKindAgent}
}

// cliRequestID returns the caller's --request-id or, when absent, a freshly
// generated UUID — the design §7 rule: --request-id is optional ONLY at the
// CLI adapter, which generates a UUID when absent.
func cliRequestID(requestID string) (string, error) {
	if strings.TrimSpace(requestID) != "" {
		return requestID, nil
	}
	return newRequestID()
}

func printJudgeUsage(w *os.File) {
	fmt.Fprintln(w, "usage: drenyra-engram judge propose <from-id> <to-id> --relation <rel> --reason <text> [--predecessor <id>] [--request-id <id>] [--db <path>]")
	fmt.Fprintln(w, "       drenyra-engram judge confirm <judgment-id> --resolution <text> --expected-hash <hash> [--request-id <id>] [--db <path>]")
	fmt.Fprintln(w, "       drenyra-engram judge reject <judgment-id> --reason <text> --expected-hash <hash> [--request-id <id>] [--db <path>]")
	fmt.Fprintln(w, "       drenyra-engram judge withdraw <judgment-id> [--request-id <id>] [--db <path>]")
	fmt.Fprintln(w, "       drenyra-engram judge show <judgment-id> [--db <path>]")
}

// cmdJudgePropose proposes a judgment over two existing observations with the
// agent provenance source of the CLI caller. The relation is validated against
// the six proposable relations; a fresh UUID requestId is generated when the
// caller omits --request-id.
func cmdJudgePropose(args []string) int {
	fs := flag.NewFlagSet("judge propose", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	relation := fs.String("relation", "", "REQUIRED proposable relation: supports|contradicts|explains|reconciles|reverses|supersedes")
	reason := fs.String("reason", "", "REQUIRED proposer justification")
	predecessor := fs.String("predecessor", "", "id of an existing judgment this proposal corrects (optional)")
	requestID := fs.String("request-id", "", "idempotency key (optional; a UUID is generated when absent)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram judge propose <from-id> <to-id> --relation <rel> --reason <text> [--predecessor <id>] [--request-id <id>] [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--relation": true, "--reason": true, "--predecessor": true, "--request-id": true})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 2 || strings.TrimSpace(*relation) == "" || strings.TrimSpace(*reason) == "" {
		fs.Usage()
		return 2
	}
	if !core.IsProposableRelation(core.Relation(*relation)) {
		fmt.Fprintf(os.Stderr, "drenyra-engram: judge propose: invalid relation %q — expected supports|contradicts|explains|reconciles|reverses|supersedes\n", *relation)
		return 2
	}
	key, err := cliRequestID(*requestID)
	if err != nil {
		return fail("judge propose: %v", err)
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	result, err := server.ProposeJudgment(context.Background(), st, core.ProposeJudgmentCommand{
		FromID:        rest[0],
		ToID:          rest[1],
		Relation:      core.Relation(*relation),
		Reason:        *reason,
		RequestID:     key,
		PredecessorID: *predecessor,
	}, cliJudgeSource())
	if err != nil {
		return fail("%v", err)
	}
	return emit(result)
}

// cmdJudgeConfirm confirms a proposed judgment with the professional human
// resolution. The principal is DERIVED from the stored CLI session (auth
// login), never declared by the caller; each invocation generates a fresh
// requestId when --request-id is absent.
func cmdJudgeConfirm(args []string) int {
	fs := flag.NewFlagSet("judge confirm", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	resolution := fs.String("resolution", "", "REQUIRED professional human resolution")
	expectedHash := fs.String("expected-hash", "", "REQUIRED proposed judgment hash the adjudicator actually saw")
	requestID := fs.String("request-id", "", "idempotency key (optional; a UUID is generated when absent)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram judge confirm <judgment-id> --resolution <text> --expected-hash <hash> [--request-id <id>] [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--resolution": true, "--expected-hash": true, "--request-id": true})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 || strings.TrimSpace(*resolution) == "" || strings.TrimSpace(*expectedHash) == "" {
		fs.Usage()
		return 2
	}

	token, err := loadSessionToken()
	if err != nil {
		return fail("AUTHENTICATION_REQUIRED: no authenticated CLI session — run `drenyra-engram auth login --token <token> --db <path>` first (%v)", err)
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	resolver := &auth.Resolver{Sessions: st, Mode: auth.RuntimeProduction}
	principal, err := resolver.Authenticate(context.Background(), auth.AuthenticationAssertion{
		Method:     auth.AuthMethodSession,
		Credential: token,
	})
	if err != nil {
		return fail("%v", err)
	}

	key, err := cliRequestID(*requestID)
	if err != nil {
		return fail("judge confirm: %v", err)
	}
	result, err := server.ConfirmJudgment(context.Background(), st, authz.NewJudgmentPolicy(), core.ConfirmJudgmentCommand{
		JudgmentID:           rest[0],
		Resolution:           *resolution,
		ExpectedJudgmentHash: *expectedHash,
		RequestID:            key,
	}, principal)
	if err != nil {
		return fail("%v", err)
	}
	return emit(result)
}

// cmdJudgeReject rejects a proposed judgment with a human reason — the same
// authenticated pattern as confirm (principal from the stored CLI session
// only; no caller-supplied authority flags).
func cmdJudgeReject(args []string) int {
	fs := flag.NewFlagSet("judge reject", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	reason := fs.String("reason", "", "REQUIRED human rejection reason")
	expectedHash := fs.String("expected-hash", "", "REQUIRED proposed judgment hash the adjudicator actually saw")
	requestID := fs.String("request-id", "", "idempotency key (optional; a UUID is generated when absent)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram judge reject <judgment-id> --reason <text> --expected-hash <hash> [--request-id <id>] [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--reason": true, "--expected-hash": true, "--request-id": true})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 || strings.TrimSpace(*reason) == "" || strings.TrimSpace(*expectedHash) == "" {
		fs.Usage()
		return 2
	}

	token, err := loadSessionToken()
	if err != nil {
		return fail("AUTHENTICATION_REQUIRED: no authenticated CLI session — run `drenyra-engram auth login --token <token> --db <path>` first (%v)", err)
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	resolver := &auth.Resolver{Sessions: st, Mode: auth.RuntimeProduction}
	principal, err := resolver.Authenticate(context.Background(), auth.AuthenticationAssertion{
		Method:     auth.AuthMethodSession,
		Credential: token,
	})
	if err != nil {
		return fail("%v", err)
	}

	key, err := cliRequestID(*requestID)
	if err != nil {
		return fail("judge reject: %v", err)
	}
	result, err := server.RejectJudgment(context.Background(), st, authz.NewJudgmentPolicy(), core.RejectJudgmentCommand{
		JudgmentID:           rest[0],
		Reason:               *reason,
		ExpectedJudgmentHash: *expectedHash,
		RequestID:            key,
	}, principal)
	if err != nil {
		return fail("%v", err)
	}
	return emit(result)
}

// cmdJudgeWithdraw withdraws the caller's OWN proposal with the same agent
// provenance source that proposed it (provenance continuity — never
// professional authorization; the store enforces the identity match).
func cmdJudgeWithdraw(args []string) int {
	fs := flag.NewFlagSet("judge withdraw", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	requestID := fs.String("request-id", "", "idempotency key (optional; a UUID is generated when absent)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram judge withdraw <judgment-id> [--request-id <id>] [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--request-id": true})); err != nil {
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
	key, err := cliRequestID(*requestID)
	if err != nil {
		return fail("judge withdraw: %v", err)
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	result, err := server.WithdrawJudgment(context.Background(), st, core.WithdrawJudgmentCommand{
		JudgmentID: rest[0],
		RequestID:  key,
	}, cliJudgeSource())
	if err != nil {
		return fail("%v", err)
	}
	return emit(result)
}

// cmdJudgeShow is the read-only surface of the adjudication store: it prints
// the judgment JSON (any status) without any transition.
func cmdJudgeShow(args []string) int {
	fs := flag.NewFlagSet("judge show", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram judge show <judgment-id> [--db <path>]")
	}
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

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	judgment, ok := st.GetJudgment(context.Background(), rest[0])
	if !ok {
		return fail("JUDGMENT_NOT_FOUND: no judgment %q", rest[0])
	}
	return emit(judgment)
}

// ──────────────────────────────────────────────
// reconcile — authenticated reconciliation commands (v0.5.0, design §3.2/§6)
// ──────────────────────────────────────────────

// cmdReconcile dispatches the first-class reconciliation surface mirroring the
// judgment surface 1:1: propose/withdraw carry the agent provenance source of
// the CLI caller (never authority); confirm/reject derive the principal ONLY
// from the stored 0600 CLI session — there is deliberately NO
// --actor/--subject/--role flag on them (caller-supplied authority is gone, the
// ADR-003 closure for adjudication); show is read-only. Monetary amounts are
// int64 cents parsed from the CLI flags; floats are never used for money.
func cmdReconcile(args []string) int {
	if len(args) == 0 {
		printReconcileUsage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "propose":
		return cmdReconcilePropose(args[1:])
	case "confirm":
		return cmdReconcileConfirm(args[1:])
	case "reject":
		return cmdReconcileReject(args[1:])
	case "withdraw":
		return cmdReconcileWithdraw(args[1:])
	case "show":
		return cmdReconcileShow(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "drenyra-engram: unknown reconcile subcommand %q\n", args[0])
		return 2
	}
}

// cliReconciliationSource is the provenance-only source of the agent CLI
// reconciliation caller: {system: "cli", actorId: "cli", actorKind: "agent"}.
// It is provenance, never authority — confirm/reject derive the principal from
// the stored session, and only that verified principal may adjudicate (design
// §3.2/§7).
func cliReconciliationSource() core.Source {
	return core.Source{System: "cli", ActorID: "cli", ActorKind: core.ActorKindAgent}
}

func printReconcileUsage(w *os.File) {
	fmt.Fprintln(w, "usage: drenyra-engram reconcile propose <left-id> <right-id> --method <m> --currency <c> --left-amount-cents <int> --right-amount-cents <int> [--tolerance-cents <int>] [--predecessor <id>] --reason <text> [--request-id <id>] [--db <path>]")
	fmt.Fprintln(w, "       drenyra-engram reconcile confirm <reconciliation-id> --resolution <text> --expected-hash <hash> [--request-id <id>] [--db <path>]")
	fmt.Fprintln(w, "       drenyra-engram reconcile reject <reconciliation-id> --reason <text> --expected-hash <hash> [--request-id <id>] [--db <path>]")
	fmt.Fprintln(w, "       drenyra-engram reconcile withdraw <reconciliation-id> [--request-id <id>] [--db <path>]")
	fmt.Fprintln(w, "       drenyra-engram reconcile show <reconciliation-id> [--db <path>]")
}

// cliAmountCents parses a CLI monetary flag as int64 cents — the only legal
// money transport; a non-integer value is a usage error (exit 2), never a
// silent float conversion.
func cliAmountCents(name, value string) (int64, error) {
	amount, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("reconcile propose: %s must be an integer amount in cents (int64, never float)", name)
	}
	return amount, nil
}

// cmdReconcilePropose proposes a first-class reconciliation over two existing
// observations with the agent provenance source of the CLI caller. The method,
// currency, the left/right amounts (int64 cents) and the reason are required;
// the tolerance defaults to zero; a fresh UUID requestId is generated when the
// caller omits --request-id.
func cmdReconcilePropose(args []string) int {
	fs := flag.NewFlagSet("reconcile propose", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	method := fs.String("method", "", "REQUIRED reconciliation method (e.g. extracto_contable)")
	currency := fs.String("currency", "", "REQUIRED ISO 4217 currency code (e.g. PEN)")
	leftAmountCents := fs.String("left-amount-cents", "", "REQUIRED left endpoint amount in int64 cents (never float)")
	rightAmountCents := fs.String("right-amount-cents", "", "REQUIRED right endpoint amount in int64 cents (never float)")
	toleranceCents := fs.String("tolerance-cents", "0", "accepted variance band in int64 cents (optional, default 0)")
	reason := fs.String("reason", "", "REQUIRED proposer justification")
	predecessor := fs.String("predecessor", "", "id of an existing reconciliation this proposal corrects (optional)")
	requestID := fs.String("request-id", "", "idempotency key (optional; a UUID is generated when absent)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram reconcile propose <left-id> <right-id> --method <m> --currency <c> --left-amount-cents <int> --right-amount-cents <int> [--tolerance-cents <int>] [--predecessor <id>] --reason <text> [--request-id <id>] [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--method": true, "--currency": true, "--left-amount-cents": true, "--right-amount-cents": true, "--tolerance-cents": true, "--predecessor": true, "--reason": true, "--request-id": true})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 2 || strings.TrimSpace(*method) == "" || strings.TrimSpace(*currency) == "" ||
		strings.TrimSpace(*leftAmountCents) == "" || strings.TrimSpace(*rightAmountCents) == "" ||
		strings.TrimSpace(*reason) == "" {
		fs.Usage()
		return 2
	}
	leftAmount, err := cliAmountCents("left-amount-cents", *leftAmountCents)
	if err != nil {
		return fail("%v", err)
	}
	rightAmount, err := cliAmountCents("right-amount-cents", *rightAmountCents)
	if err != nil {
		return fail("%v", err)
	}
	tolerance, err := cliAmountCents("tolerance-cents", *toleranceCents)
	if err != nil {
		return fail("%v", err)
	}
	key, err := cliRequestID(*requestID)
	if err != nil {
		return fail("reconcile propose: %v", err)
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	result, err := server.ProposeReconciliation(context.Background(), st, core.ProposeReconciliationCommand{
		LeftMemoryID:     rest[0],
		RightMemoryID:    rest[1],
		Method:           *method,
		Currency:         *currency,
		LeftAmountCents:  leftAmount,
		RightAmountCents: rightAmount,
		ToleranceCents:   tolerance,
		Reason:           *reason,
		RequestID:        key,
		PredecessorID:    *predecessor,
	}, cliReconciliationSource())
	if err != nil {
		return fail("%v", err)
	}
	return emit(result)
}

// cmdReconcileConfirm confirms a proposed reconciliation with the professional
// human resolution. The principal is DERIVED from the stored CLI session (auth
// login), never declared by the caller; each invocation generates a fresh
// requestId when --request-id is absent.
func cmdReconcileConfirm(args []string) int {
	fs := flag.NewFlagSet("reconcile confirm", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	resolution := fs.String("resolution", "", "REQUIRED professional human resolution")
	expectedHash := fs.String("expected-hash", "", "REQUIRED proposed reconciliation hash the adjudicator actually saw")
	requestID := fs.String("request-id", "", "idempotency key (optional; a UUID is generated when absent)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram reconcile confirm <reconciliation-id> --resolution <text> --expected-hash <hash> [--request-id <id>] [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--resolution": true, "--expected-hash": true, "--request-id": true})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 || strings.TrimSpace(*resolution) == "" || strings.TrimSpace(*expectedHash) == "" {
		fs.Usage()
		return 2
	}

	token, err := loadSessionToken()
	if err != nil {
		return fail("AUTHENTICATION_REQUIRED: no authenticated CLI session — run `drenyra-engram auth login --token <token> --db <path>` first (%v)", err)
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	resolver := &auth.Resolver{Sessions: st, Mode: auth.RuntimeProduction}
	principal, err := resolver.Authenticate(context.Background(), auth.AuthenticationAssertion{
		Method:     auth.AuthMethodSession,
		Credential: token,
	})
	if err != nil {
		return fail("%v", err)
	}

	key, err := cliRequestID(*requestID)
	if err != nil {
		return fail("reconcile confirm: %v", err)
	}
	result, err := server.ConfirmReconciliation(context.Background(), st, authz.NewReconciliationPolicy(), core.ConfirmReconciliationCommand{
		ReconciliationID:           rest[0],
		Resolution:                 *resolution,
		ExpectedReconciliationHash: *expectedHash,
		RequestID:                  key,
	}, principal)
	if err != nil {
		return fail("%v", err)
	}
	return emit(result)
}

// cmdReconcileReject rejects a proposed reconciliation with a human reason — the
// same authenticated pattern as confirm (principal from the stored CLI session
// only; no caller-supplied authority flags).
func cmdReconcileReject(args []string) int {
	fs := flag.NewFlagSet("reconcile reject", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	reason := fs.String("reason", "", "REQUIRED human rejection reason")
	expectedHash := fs.String("expected-hash", "", "REQUIRED proposed reconciliation hash the adjudicator actually saw")
	requestID := fs.String("request-id", "", "idempotency key (optional; a UUID is generated when absent)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram reconcile reject <reconciliation-id> --reason <text> --expected-hash <hash> [--request-id <id>] [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--reason": true, "--expected-hash": true, "--request-id": true})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 || strings.TrimSpace(*reason) == "" || strings.TrimSpace(*expectedHash) == "" {
		fs.Usage()
		return 2
	}

	token, err := loadSessionToken()
	if err != nil {
		return fail("AUTHENTICATION_REQUIRED: no authenticated CLI session — run `drenyra-engram auth login --token <token> --db <path>` first (%v)", err)
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	resolver := &auth.Resolver{Sessions: st, Mode: auth.RuntimeProduction}
	principal, err := resolver.Authenticate(context.Background(), auth.AuthenticationAssertion{
		Method:     auth.AuthMethodSession,
		Credential: token,
	})
	if err != nil {
		return fail("%v", err)
	}

	key, err := cliRequestID(*requestID)
	if err != nil {
		return fail("reconcile reject: %v", err)
	}
	result, err := server.RejectReconciliation(context.Background(), st, authz.NewReconciliationPolicy(), core.RejectReconciliationCommand{
		ReconciliationID:           rest[0],
		Reason:                     *reason,
		ExpectedReconciliationHash: *expectedHash,
		RequestID:                  key,
	}, principal)
	if err != nil {
		return fail("%v", err)
	}
	return emit(result)
}

// cmdReconcileWithdraw withdraws the caller's OWN proposal with the same agent
// provenance source that proposed it (provenance continuity — never
// professional authorization; the store enforces the identity match).
func cmdReconcileWithdraw(args []string) int {
	fs := flag.NewFlagSet("reconcile withdraw", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	requestID := fs.String("request-id", "", "idempotency key (optional; a UUID is generated when absent)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram reconcile withdraw <reconciliation-id> [--request-id <id>] [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--request-id": true})); err != nil {
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
	key, err := cliRequestID(*requestID)
	if err != nil {
		return fail("reconcile withdraw: %v", err)
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	result, err := server.WithdrawReconciliation(context.Background(), st, core.WithdrawReconciliationCommand{
		ReconciliationID: rest[0],
		RequestID:        key,
	}, cliReconciliationSource())
	if err != nil {
		return fail("%v", err)
	}
	return emit(result)
}

// cmdReconcileShow is the read-only surface of the reconciliation store: it
// prints the reconciliation JSON (any status) without any transition.
func cmdReconcileShow(args []string) int {
	fs := flag.NewFlagSet("reconcile show", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram reconcile show <reconciliation-id> [--db <path>]")
	}
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

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	reconciliation, ok := st.GetReconciliation(context.Background(), rest[0])
	if !ok {
		return fail("RECONCILIATION_NOT_FOUND: no reconciliation %q", rest[0])
	}
	return emit(reconciliation)
}

// ──────────────────────────────────────────────
// supersede — promoted → superseded with a REQUIRED target
// ──────────────────────────────────────────────

// supersedeOutput mirrors server.SupersedeOutput on the CLI surface.
type supersedeOutput = server.SupersedeOutput

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

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	api := server.New(st, "cli")
	output, err := api.Supersede(rest[0], *target, cliSource(*actor))
	if err != nil {
		return fail("%v", err)
	}
	return emit(output)
}

// ──────────────────────────────────────────────
// mcp — Model Context Protocol server over stdio (agents)
// ──────────────────────────────────────────────

// cmdMCP runs the MCP stdio server (newline-delimited JSON-RPC over stdin/
// stdout) — the agent transport (Claude Desktop, pi, etc.). It serves until
// stdin closes. The non-authorization boundary applies: the tool catalog has
// no authorize/approve/allow tool.
func cmdMCP(args []string) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	fs.Usage = func() { fmt.Fprintln(fs.Output(), "usage: drenyra-engram mcp [--db <path>]") }
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

	api := server.New(st, "mcp")
	// The automatic session context (v0.5.0 design §5): one exact scope from
	// DRENYRA_DEFAULT_SCOPE (JSON-encoded company scope). Unset → null context
	// (initialize points at accounting_current_context); present but invalid or
	// inaccessible → the server fails closed at startup, never serving partial
	// cross-scope data.
	mcp, err := server.NewMCPServerWithDefaultScope(api, os.Getenv("DRENYRA_DEFAULT_SCOPE"))
	if err != nil {
		return fail("%v", err)
	}
	if err := mcp.ServeStdio(os.Stdin, os.Stdout); err != nil {
		return fail("mcp stdio: %v", err)
	}
	return 0
}

// ──────────────────────────────────────────────
// serve — HTTP server (REST /v1 + MCP /mcp)
// ──────────────────────────────────────────────

// cmdServe runs the local HTTP server: the REST /v1 surface and the MCP
// streamable-HTTP JSON endpoint (/mcp). It binds 127.0.0.1 by default (fail
// closed — a memory engine for local agents, not a public service) and, when a
// token is configured, requires Authorization: Bearer <token> on every request.
func cmdServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	addr := fs.String("addr", "127.0.0.1:8787", "listen address (default 127.0.0.1:8787)")
	token := fs.String("token", os.Getenv("DRENYRA_ENGRAM_TOKEN"), "bearer token required on every request (default $DRENYRA_ENGRAM_TOKEN, empty = no auth)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram serve [--addr <host:port>] [--token <secret>] [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--addr": true, "--token": true})); err != nil {
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

	api := server.New(st, "http")
	// The HTTP /mcp surface honors DRENYRA_DEFAULT_SCOPE exactly like the stdio
	// server (v0.5.0 design §5): fail closed at startup when the configured
	// scope is invalid or inaccessible.
	httpServer, err := server.NewHTTPServerWithDefaultScope(api, *token, os.Getenv("DRENYRA_DEFAULT_SCOPE"))
	if err != nil {
		return fail("%v", err)
	}
	// Stateless OIDC access-token validation (first Production Identity slice):
	// configured via DRENYRA_OIDC_*; partial or invalid configuration fails the
	// server at startup, never at request time.
	oidcCfg, err := oidcConfigFromEnv()
	if err != nil {
		return fail("serve: %v", err)
	}
	if oidcCfg != nil {
		if _, err := httpServer.EnableOIDC(*oidcCfg); err != nil {
			return fail("serve: %v", err)
		}
	}
	fmt.Fprintf(os.Stderr, "drenyra-engram: serving on http://%s (db %s)%s%s\n",
		*addr, *dbPath, tokenSuffix(*token), oidcSuffix(oidcCfg))
	if err := http.ListenAndServe(*addr, httpServer.Handler()); err != nil {
		return fail("serve: %v", err)
	}
	return 0
}

func tokenSuffix(token string) string {
	if token == "" {
		return " (no token — localhost only)"
	}
	return " (bearer token required)"
}

func oidcSuffix(cfg *auth.OIDCConfig) string {
	if cfg == nil {
		return ""
	}
	return " (oidc access tokens enabled)"
}

// ──────────────────────────────────────────────
// sync — additive, conflict-visible store reconciliation
// ──────────────────────────────────────────────

// cmdSync reconciles one store into another (docs/architecture.md sync
// semantics: additive, provenance-preserving, conflict-visible). It imports the
// source's full revision history, relations and lifecycle audit trail into the
// sink, replays transitions through the lifecycle machine, and SURFACES
// divergence (conflicts_with relations + report entries) — it never deletes,
// overwrites, or silently resolves anything. Re-running the same pair is a
// no-op. Cloud is out of scope (ROADMAP non-goals); this operates on local
// store files.
func cmdSync(args []string) int {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fromPath := fs.String("from", "", "REQUIRED source SQLite database path")
	toPath := fs.String("to", "", "REQUIRED target SQLite database path")
	actor := fs.String("actor", "cli", "actor recorded on conflict relations (default cli)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram sync --from <src-db> --to <dst-db> [--actor <name>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--from": true, "--to": true, "--actor": true})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 || *fromPath == "" || *toPath == "" {
		fs.Usage()
		return 2
	}

	from, err := openStore(*fromPath)
	if err != nil {
		return fail("open source store: %v", err)
	}
	defer func() { _ = from.Close() }()
	to, err := openStore(*toPath)
	if err != nil {
		return fail("open target store: %v", err)
	}
	defer func() { _ = to.Close() }()

	report, err := sync.Sync(from, to, sync.Options{
		Actor:     *actor,
		Timestamp: nowISO(),
	})
	if err != nil {
		return fail("%v", err)
	}
	return emit(report)
}

// ──────────────────────────────────────────────
// verify — offline verification of signed receipt chains (v0.4.0 Step 4)
// ──────────────────────────────────────────────

// cmdVerify dispatches the offline verification subcommands: memory, judgment
// and receipt. Verification is READ-ONLY over the local store — the design's
// "offline" contract: no network, HTTP or MCP surface.
func cmdVerify(args []string) int {
	if len(args) == 0 {
		printVerifyUsage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "memory":
		return cmdVerifyMemory(args[1:])
	case "judgment":
		return cmdVerifyJudgment(args[1:])
	case "receipt":
		return cmdVerifyReceipt(args[1:])
	case "object":
		return cmdVerifyObject(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "drenyra-engram: unknown verify subcommand %q\n", args[0])
		return 2
	}
}

func printVerifyUsage(w *os.File) {
	fmt.Fprintln(w, "usage: drenyra-engram verify memory <id> [--db <path>]")
	fmt.Fprintln(w, "       drenyra-engram verify judgment <id> [--db <path>]")
	fmt.Fprintln(w, "       drenyra-engram verify receipt <hash|id> [--db <path>]")
	fmt.Fprintln(w, "       drenyra-engram verify object <sha256> [--db <path>] [--objects <dir>]   (v0.7.0 evidence object)")
}

// cmdVerifyMemory verifies the FULL signed chain of one memory subject (design
// §5): the six receipt layers over every receipt, then principal provenance,
// supersession chain, evidence availability and rule availability.
func cmdVerifyMemory(args []string) int {
	fs := flag.NewFlagSet("verify memory", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram verify memory <id> [--db <path>]")
	}
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

	st, err := openStore(*dbPath)
	if err != nil {
		return failVerify("verify memory: %v", err)
	}
	defer func() { _ = st.Close() }()

	report, err := server.VerifyMemory(context.Background(), st, rest[0])
	if err != nil {
		return failVerify("verify memory: %v", err)
	}
	return emitVerifyReport(report)
}

// cmdVerifyJudgment verifies the FULL signed chain of one judgment subject
// (design §5): the six receipt layers, then principal provenance, judgment hash
// and supersession chain.
func cmdVerifyJudgment(args []string) int {
	fs := flag.NewFlagSet("verify judgment", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram verify judgment <id> [--db <path>]")
	}
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

	st, err := openStore(*dbPath)
	if err != nil {
		return failVerify("verify judgment: %v", err)
	}
	defer func() { _ = st.Close() }()

	report, err := server.VerifyJudgment(context.Background(), st, rest[0])
	if err != nil {
		return failVerify("verify judgment: %v", err)
	}
	return emitVerifyReport(report)
}

// cmdVerifyReceipt verifies ONE selected receipt and its predecessor link. The
// single argument is the portable identity when it is exactly 64 lowercase hex
// digits and the local SQLite row id when it parses as a decimal int64;
// anything else is a usage error (exit 2) BEFORE the store is touched.
func cmdVerifyReceipt(args []string) int {
	fs := flag.NewFlagSet("verify receipt", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram verify receipt <hash|id> [--db <path>]")
	}
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
	target, ok := parseReceiptTarget(rest[0])
	if !ok {
		fmt.Fprintf(os.Stderr, "drenyra-engram: verify receipt: %q is neither 64 lowercase hex digits nor a decimal row id\n", rest[0])
		return 2
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return failVerify("verify receipt: %v", err)
	}
	defer func() { _ = st.Close() }()

	report, err := server.VerifyReceipt(context.Background(), st, target)
	if err != nil {
		return failVerify("verify receipt: %v", err)
	}
	return emitVerifyReport(report)
}

// cmdVerifyObject verifies the FULL signed chain of one evidence-object subject
// (v0.7.0): the six receipt layers over the object_stored chain, principal
// provenance (the immutable evidence_objects row) and the WORM byte-integrity
// layer (the stored bytes re-hash to the content address — corruption fails
// closed, no silent repair). The --objects root is honored explicitly so a
// custom-root store verifies its own bytes.
func cmdVerifyObject(args []string) int {
	fs := flag.NewFlagSet("verify object", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	objects := fs.String("objects", "", "WORM evidence object root (default <dir-of-db>/objects or $DRENYRA_ENGRAM_OBJECTS)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram verify object <sha256> [--db <path>] [--objects <dir>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--objects": true})); err != nil {
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

	st, err := openStoreWithRoot(*dbPath, *objects)
	if err != nil {
		return failVerify("verify object: %v", err)
	}
	defer func() { _ = st.Close() }()

	report, err := server.VerifyEvidenceObject(context.Background(), st, rest[0])
	if err != nil {
		return failVerify("verify object: %v", err)
	}
	return emitVerifyReport(report)
}

// ──────────────────────────────────────────────
// object — v0.7.0 WORM evidence object surface
// ──────────────────────────────────────────────

// cmdObject dispatches the v0.7.0 evidence-object subcommands: store and get.
// Both are provenance-recorded captures/reads — NEITHER can approve anything
// (non-authorization boundary).
func cmdObject(args []string) int {
	if len(args) == 0 {
		printObjectUsage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "store":
		return cmdObjectStore(args[1:])
	case "get":
		return cmdObjectGet(args[1:])
	case "ingest":
		return cmdObjectIngest(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "drenyra-engram: unknown object subcommand %q\n", args[0])
		printObjectUsage(os.Stderr)
		return 2
	}
}

func printObjectUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: drenyra-engram object store <file> --ruc <11 digits> [--period <YYYYMM>] [--organization <id>] [--content-type <mime>] [--actor <name>] [--source-system <system>] [--reference <ref>] [--objects <dir>] [--db <path>]")
	fmt.Fprintln(w, "       drenyra-engram object get <sha256> --ruc <11 digits> [--period <YYYYMM>] [--organization <id>] [--objects <dir>] [--db <path>]   (scope-first: exact scope must match)")
	fmt.Fprintln(w, "       drenyra-engram object ingest <invoice.xml> --ruc <11 digits> [--period <YYYYMM>] [--actor <name>] [--db <path>]   (v0.6.0 adapter contract — parse + WORM store; NEVER claims SUNAT integration)")
}

// objectCLIScope builds the CLI's exact company scope for objects: the same
// convention as every other CLI surface (organizationId defaults to "cli",
// companyId is derived from the RUC). The scope is EXACT — an object stored
// with a period is only readable with that period.
func objectCLIScope(organization, ruc, period string) (core.Scope, error) {
	if organization == "" {
		organization = cliOrganizationID
	}
	scope := core.Scope{
		Kind:           core.ScopeKindCompany,
		OrganizationID: organization,
		CompanyID:      ruc,
		RUC:            ruc,
		Period:         period,
	}
	if err := core.AssertValidScope(scope); err != nil {
		return core.Scope{}, err
	}
	return scope, nil
}

// cmdObjectStore captures ONE evidence object WORM-style: the artifact bytes
// are read from <file>, the identity is their SHA-256, a duplicate store is a
// no-op and genuinely new objects mint the object_stored receipt atomically.
func cmdObjectStore(args []string) int {
	fs := flag.NewFlagSet("object store", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	ruc := fs.String("ruc", "", "company RUC (exactly 11 digits)")
	period := fs.String("period", "", "fiscal period YYYYMM (optional; exact scope)")
	organization := fs.String("organization", "", "organization id (default cli)")
	contentType := fs.String("content-type", "", "optional MIME hint, stored verbatim")
	actor := fs.String("actor", "cli", "actor id recorded as the capture provenance (default cli)")
	sourceSystem := fs.String("source-system", "cli", "system that produced the artifact (default cli)")
	reference := fs.String("reference", "", "optional external reference (e.g. F001-948)")
	objects := fs.String("objects", "", "WORM evidence object root (default <dir-of-db>/objects or $DRENYRA_ENGRAM_OBJECTS)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram object store <file> --ruc <11 digits> [--period <YYYYMM>] [--organization <id>] [--content-type <mime>] [--actor <name>] [--source-system <system>] [--reference <ref>] [--objects <dir>] [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--ruc": true, "--period": true, "--organization": true, "--content-type": true, "--actor": true, "--source-system": true, "--reference": true, "--objects": true})); err != nil {
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
	scope, err := objectCLIScope(*organization, *ruc, *period)
	if err != nil {
		return fail("%v", err)
	}
	bytes, err := os.ReadFile(rest[0])
	if err != nil {
		return fail("read %s: %v", rest[0], err)
	}

	st, err := openStoreWithRoot(*dbPath, *objects)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	result, err := st.StoreObject(context.Background(), core.ObjectStoreInput{
		Bytes:       bytes,
		ContentType: *contentType,
		Scope:       scope,
		Source: core.Source{
			System:    *sourceSystem,
			Reference: *reference,
			ActorID:   *actor,
			ActorKind: core.ActorKindAgent,
		},
	})
	if err != nil {
		return fail("%v", err)
	}
	return emit(result)
}

// cmdObjectGet reads one evidence object SCOPE-FIRST: the caller's exact scope
// must equal the stored scope (cross-tenant invisibility — OBJECT_NOT_FOUND
// otherwise); the stored bytes are re-hashed on every read (corruption fails
// closed). The artifact bytes are written to stdout AFTER the metadata line on
// stderr, so pipes stay byte-safe.
func cmdObjectGet(args []string) int {
	fs := flag.NewFlagSet("object get", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	ruc := fs.String("ruc", "", "company RUC (exactly 11 digits)")
	period := fs.String("period", "", "fiscal period YYYYMM (optional; exact scope)")
	organization := fs.String("organization", "", "organization id (default cli)")
	objects := fs.String("objects", "", "WORM evidence object root (default <dir-of-db>/objects or $DRENYRA_ENGRAM_OBJECTS)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram object get <sha256> --ruc <11 digits> [--period <YYYYMM>] [--organization <id>] [--objects <dir>] [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--ruc": true, "--period": true, "--organization": true, "--objects": true})); err != nil {
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
	scope, err := objectCLIScope(*organization, *ruc, *period)
	if err != nil {
		return fail("%v", err)
	}

	st, err := openStoreWithRoot(*dbPath, *objects)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	obj, bytes, err := st.GetObject(context.Background(), rest[0], scope)
	if err != nil {
		return fail("%v", err)
	}
	meta, err := json.Marshal(obj)
	if err != nil {
		return fail("encode metadata: %v", err)
	}
	fmt.Fprintf(os.Stderr, "%s\n", meta)
	if _, err := os.Stdout.Write(bytes); err != nil {
		return fail("write bytes: %v", err)
	}
	return 0
}

// emitVerifyReport prints the verification report as indent-2 JSON for BOTH
// outcomes and maps the result to the exit code: 0 only when the outcome is
// passed, 1 when any applicable layer fails. A layer failure is EVIDENCE — the
// report is still emitted — never a store error (design §6).
func emitVerifyReport(report core.VerificationReport) int {
	code := 1
	if report.Outcome == core.VerificationOutcomePassed {
		code = 0
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return fail("encode output: %v", err)
	}
	return code
}

// failVerify prints a report-building error and returns exit code 2: usage,
// malformed/not-found target, database/query/decode failure or any other
// inability to complete. A verifiable-but-failed subject is NOT an error here —
// it produced a printed report with exit 1.
func failVerify(format string, args ...any) int {
	fmt.Fprintf(os.Stderr, "drenyra-engram: "+format+"\n", args...)
	return 2
}

// parseReceiptTarget classifies the CLI's single receipt argument: exactly 64
// lowercase hex digits select the portable hash; a decimal int64 selects the
// local SQLite row id (local convenience — the hash is portable identity,
// design §5).
func parseReceiptTarget(raw string) (core.ReceiptTarget, bool) {
	if isLowerHex64(raw) {
		return core.ReceiptTarget{Hash: raw}, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return core.ReceiptTarget{}, false
	}
	return core.ReceiptTarget{ID: id}, true
}

// isLowerHex64 reports whether s is exactly 64 lowercase hex digits — the CLI's
// portable receipt identity selector (uppercase hex is deliberately NOT a hash
// target: it is neither lowercase hex nor a decimal row id → usage error).
func isLowerHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
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

// ──────────────────────────────────────────────
// link-evidence — attach evidence references to a memory (immutable memory,
// growing links)
// ──────────────────────────────────────────────

func cmdLinkEvidence(args []string) int {
	fs := flag.NewFlagSet("link-evidence", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	actor := fs.String("actor", "cli", "actor id recorded on the link (default cli)")
	ruc := fs.String("ruc", "", "company RUC (exactly 11 digits)")
	period := fs.String("period", "", "fiscal period YYYYMM (optional; exact scope)")
	organization := fs.String("organization", "", "organization id (default cli)")
	var refs multiFlag
	fs.Var(&refs, "ref", "evidence reference (repeatable)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram link-evidence <id> --ref <ref> [--ref <ref>...] --ruc <11 digits> [--period <YYYYMM>] [--organization <id>] [--actor <name>] [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--actor": true, "--ref": true, "--ruc": true, "--period": true, "--organization": true})); err != nil {
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
	if len(refs) == 0 {
		fs.Usage()
		return 2
	}
	// Scope-first (contracts/scope.md rule 4): the caller must supply the
	// memory's EXACT company scope; a foreign-scope memory reads
	// MEMORY_NOT_FOUND (non-enumerating — same as the HTTP adapter fix).
	scope, err := objectCLIScope(*organization, *ruc, *period)
	if err != nil {
		return fail("%v", err)
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	api := server.New(st, "cli")
	out, err := api.LinkEvidence(rest[0], refs, *actor, scope)
	if err != nil {
		return fail("%v", err)
	}
	return emit(out)
}

// ──────────────────────────────────────────────
// period-summary — the explainable period narrative (killer demo)
// ──────────────────────────────────────────────

func cmdPeriodSummary(args []string) int {
	fs := flag.NewFlagSet("period-summary", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	period := fs.String("period", "", "fiscal period YYYYMM (optional)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram period-summary <ruc> [--period <YYYYMM>] [--db <path>]")
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

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	api := server.New(st, "cli")
	summary, err := api.PeriodSummary(scope)
	if err != nil {
		return fail("%v", err)
	}
	if summary.NarrativeText != "" {
		fmt.Fprintln(os.Stdout, summary.NarrativeText)
	}
	return emit(summary)
}

// ──────────────────────────────────────────────
// compare-periods — period-over-period comparison (v0.5.0, design §4/§6)
// ──────────────────────────────────────────────

func cmdComparePeriods(args []string) int {
	fs := flag.NewFlagSet("compare-periods", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	from := fs.String("from", "", "fiscal period YYYYMM of the from scope (required)")
	to := fs.String("to", "", "fiscal period YYYYMM of the to scope (required)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram compare-periods <ruc> --from <YYYYMM> --to <YYYYMM> [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--from": true, "--to": true})); err != nil {
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
	if strings.TrimSpace(*from) == "" || strings.TrimSpace(*to) == "" {
		fs.Usage()
		return 2
	}
	fromScope, err := cliCompanyScope(rest[0], *from)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drenyra-engram: %v\n", err)
		return 2
	}
	toScope, err := cliCompanyScope(rest[0], *to)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drenyra-engram: %v\n", err)
		return 2
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	api := server.New(st, "cli")
	comparison, err := server.ComparePeriods(context.Background(), api, fromScope, toScope)
	if err != nil {
		return fail("%v", err)
	}
	return emit(comparison)
}

// ──────────────────────────────────────────────
// close — monthly close surfaces (v0.5.0 close foundation, design §6)
// ──────────────────────────────────────────────

// cmdClose dispatches the monthly-close subcommands: create (a NORMAL agent save
// that lands pending_review), show (inspect one close memory with its frozen
// snapshot) and reopen (the EXPLICIT AUTHENTICATED controller act that admits
// corrections — the principal comes from the stored CLI session like approve,
// never from a caller flag).
func cmdClose(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: drenyra-engram close create|show|reopen ...")
		return 2
	}
	switch args[0] {
	case "create":
		return cmdCloseCreate(args[1:])
	case "show":
		return cmdCloseShow(args[1:])
	case "reopen":
		return cmdCloseReopen(args[1:])
	case "help", "-h", "--help":
		fmt.Fprintln(os.Stdout, `usage: drenyra-engram close create <ruc> --period YYYYMM [--total code=currency=amountCents[=memoryId]]... [--reason <text>] [--db <path>]
       drenyra-engram close show <memory-id> [--db <path>]
       drenyra-engram close reopen <ruc> --period YYYYMM --expected-close <id> --reason <text> [--db <path>]`)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "drenyra-engram: unknown close subcommand %q\n", args[0])
		return 2
	}
}

// closeTotalSpec is one parsed --total code=currency=amountCents[=memoryId]
// value: the explicit monetary total and its optional same-scope source memory.
type closeTotalSpec struct {
	code     string
	currency string
	cents    int64
	sourceID string
}

// parseCloseTotal parses one --total value. The amount is a SIGNED int64 in
// cents (never float); the optional 4th segment is the same-scope source memory
// id the total must be backed by (the engine never derives money from prose).
func parseCloseTotal(raw string) (closeTotalSpec, error) {
	parts := strings.Split(raw, "=")
	if len(parts) < 3 || len(parts) > 4 {
		return closeTotalSpec{}, fmt.Errorf("INVALID_TOTAL: --total must be code=currency=amountCents[=memoryId], got %q", raw)
	}
	code := strings.TrimSpace(parts[0])
	currency := strings.TrimSpace(parts[1])
	amount := strings.TrimSpace(parts[2])
	if code == "" || currency == "" || amount == "" {
		return closeTotalSpec{}, fmt.Errorf("INVALID_TOTAL: code, currency and amountCents must be non-empty in %q", raw)
	}
	cents, err := strconv.ParseInt(amount, 10, 64)
	if err != nil {
		return closeTotalSpec{}, fmt.Errorf("INVALID_TOTAL: amountCents %q is not a signed int64: %v", amount, err)
	}
	sourceID := ""
	if len(parts) == 4 {
		sourceID = strings.TrimSpace(parts[3])
		if sourceID == "" {
			return closeTotalSpec{}, fmt.Errorf("INVALID_TOTAL: the source memory id segment of %q is empty", raw)
		}
	}
	return closeTotalSpec{code: code, currency: currency, cents: cents, sourceID: sourceID}, nil
}

// cmdCloseCreate creates a monthly close through the canonical CreateClose
// service: the memory lands pending_review behind the human gate and only the
// authenticated controller approval can close the period.
func cmdCloseCreate(args []string) int {
	fs := flag.NewFlagSet("close create", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	period := fs.String("period", "", "fiscal period YYYYMM (REQUIRED)")
	reason := fs.String("reason", "", "close rationale (optional)")
	var totals multiFlag
	fs.Var(&totals, "total", "monetary total code=currency=amountCents[=memoryId] (repeatable)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram close create <ruc> --period YYYYMM [--total code=currency=amountCents[=memoryId]]... [--reason <text>] [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--period": true, "--reason": true, "--total": true})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 || strings.TrimSpace(*period) == "" {
		fs.Usage()
		return 2
	}
	scope, err := cliCompanyScope(rest[0], *period)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drenyra-engram: %v\n", err)
		return 2
	}
	var closeTotals []core.CloseTotal
	for _, raw := range totals {
		spec, err := parseCloseTotal(raw)
		if err != nil {
			return fail("%v", err)
		}
		sourceIDs := []string(nil)
		if spec.sourceID != "" {
			sourceIDs = []string{spec.sourceID}
		}
		closeTotals = append(closeTotals, core.CloseTotal{
			Code:            spec.code,
			Currency:        spec.currency,
			AmountCents:     spec.cents,
			SourceMemoryIDs: sourceIDs,
		})
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	api := server.New(st, "cli")
	memory, err := server.CreateClose(context.Background(), api, scope, core.CreateCloseInput{
		Period: *period,
		Totals: closeTotals,
		Reason: *reason,
		// The CLI is the professional's surface: the creation claim is human
		// provenance (actorId cli) — it records WHO drafted the close; the
		// approval remains the authenticated controller act.
		Source: core.Source{System: "cli", ActorID: "cli", ActorKind: core.ActorKindHuman},
	})
	if err != nil {
		return fail("%v", err)
	}
	return emit(memory)
}

// cmdCloseShow inspects one close memory by id (its frozen snapshot included).
func cmdCloseShow(args []string) int {
	fs := flag.NewFlagSet("close show", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram close show <memory-id> [--db <path>]")
	}
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
	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()
	api := server.New(st, "cli")
	memory, err := api.Get(rest[0])
	if err != nil {
		return fail("%v", err)
	}
	return emit(memory)
}

// cmdCloseReopen is the EXPLICIT AUTHENTICATED controller reopen: the principal
// is DERIVED from the stored CLI session (auth login), never declared by the
// caller — there is deliberately NO --actor flag on this command. Each
// invocation generates a fresh requestId.
func cmdCloseReopen(args []string) int {
	fs := flag.NewFlagSet("close reopen", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	period := fs.String("period", "", "fiscal period YYYYMM being reopened (REQUIRED)")
	expectedClose := fs.String("expected-close", "", "REQUIRED close memory id that closed the period")
	reason := fs.String("reason", "", "REQUIRED reopen justification")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram close reopen <ruc> --period YYYYMM --expected-close <id> --reason <text> [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--period": true, "--expected-close": true, "--reason": true})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 || strings.TrimSpace(*period) == "" || strings.TrimSpace(*expectedClose) == "" || strings.TrimSpace(*reason) == "" {
		fs.Usage()
		return 2
	}
	scope, err := cliCompanyScope(rest[0], *period)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drenyra-engram: %v\n", err)
		return 2
	}

	token, err := loadSessionToken()
	if err != nil {
		return fail("AUTHENTICATION_REQUIRED: no authenticated CLI session — run `drenyra-engram auth login --token <token> --db <path>` first (%v)", err)
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	resolver := &auth.Resolver{Sessions: st, Mode: auth.RuntimeProduction}
	principal, err := resolver.Authenticate(context.Background(), auth.AuthenticationAssertion{
		Method:     auth.AuthMethodSession,
		Credential: token,
	})
	if err != nil {
		return fail("%v", err)
	}

	requestID, err := newRequestID()
	if err != nil {
		return fail("generate request id: %v", err)
	}
	result, err := server.ReopenPeriod(context.Background(), st, authz.NewApprovalPolicy(), scope,
		*expectedClose, *reason, requestID, principal)
	if err != nil {
		return fail("%v", err)
	}
	return emit(result)
}

// ──────────────────────────────────────────────
// timeline — full revision history of a (topicKey, scope) chain
// ──────────────────────────────────────────────

func cmdTimeline(args []string) int {
	fs := flag.NewFlagSet("timeline", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	topic := fs.String("topic", "", "topic key of the chain (REQUIRED)")
	period := fs.String("period", "", "fiscal period YYYYMM (optional)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram timeline <ruc> --topic <topicKey> [--period <YYYYMM>] [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--topic": true, "--period": true})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 || *topic == "" {
		fs.Usage()
		return 2
	}
	scope, err := cliCompanyScope(rest[0], *period)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drenyra-engram: %v\n", err)
		return 2
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	api := server.New(st, "cli")
	chain, err := api.Chain(*topic, scope)
	if err != nil {
		return fail("%v", err)
	}
	return emit(chain)
}

// ──────────────────────────────────────────────
// hold — v0.8 batch 3 object-level legal-hold surface (place/lift/list)
// ──────────────────────────────────────────────

// cmdHold dispatches the v0.8 batch 3 object-level legal-hold subcommands
// (docs/architecture/evidence-lifecycle-v0.8.md §3.2/§7/§9): place and lift
// are the AUTHENTICATED preservation mutations (the principal is DERIVED from
// the stored CLI session like approve, never from a caller flag — there is
// deliberately NO --actor/--subject/--role flag) that DELIBERATELY BYPASS the
// closed-period gate (holds only preserve evidence — emergency placement/lift
// works inside a closed period); list is a SCOPE-FIRST READ (the caller's
// exact scope tuple is built from the flags and must equal the object's stored
// scope — cross-tenant invisibility). NO purge, NO export, NO deletion, NO
// scheduling.
func cmdHold(args []string) int {
	if len(args) == 0 {
		printHoldUsage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "place":
		return cmdHoldPlace(args[1:])
	case "lift":
		return cmdHoldLift(args[1:])
	case "list":
		return cmdHoldList(args[1:])
	case "help", "-h", "--help":
		printHoldUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "drenyra-engram: unknown hold subcommand %q\n", args[0])
		printHoldUsage(os.Stderr)
		return 2
	}
}

func printHoldUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: drenyra-engram hold place <object-id> --kind <legal|audit|dispute|fiscalization|other> --reason <text> --owner <subject-id> [--request-id <id>] [--db <path>]   (authenticated session; identity never from flags; emergency bypass: no closed-period gate)")
	fmt.Fprintln(w, "       drenyra-engram hold lift <hold-id> --reason <text> [--request-id <id>] [--db <path>]   (authenticated session; one-way closure)")
	fmt.Fprintln(w, "       drenyra-engram hold list <object-id> --ruc <11 digits> [--period <YYYYMM>] [--organization <id>] [--blocking-kinds <list>] [--db <path>]   (scope-first read)")
}

// cmdHoldPlace places ONE object-level legal hold (v0.8 batch 3, design
// §3.2/§7/§9): the principal is DERIVED from the stored CLI session (auth
// login), never declared by the caller — caller-supplied authority is gone.
// --request-id is optional; a fresh UUID is generated when absent. The
// authenticated preservation gate ((tenant, requestId) idempotency, the
// deny-list-first role check through the EXTENDED evidence-lifecycle policy
// with the place_hold action) lives in the store; the hold_placed receipt is
// emitted atomically on the evidence_object chain. EMERGENCY BYPASS: no
// closed-period gate (holds only preserve evidence).
func cmdHoldPlace(args []string) int {
	fs := flag.NewFlagSet("hold place", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	kind := fs.String("kind", "", "REQUIRED closed hold-kind token (legal|audit|dispute|fiscalization|other)")
	reason := fs.String("reason", "", "REQUIRED placement justification")
	owners := fs.String("owner", "", "REQUIRED subject id responsible for the hold")
	requestID := fs.String("request-id", "", "tenant-scoped idempotency key (optional; a UUID is generated when absent)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram hold place <object-id> --kind <legal|audit|dispute|fiscalization|other> --reason <text> --owner <subject-id> [--request-id <id>] [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--kind": true, "--reason": true, "--owner": true, "--request-id": true})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 || strings.TrimSpace(*kind) == "" || strings.TrimSpace(*reason) == "" || strings.TrimSpace(*owners) == "" {
		fs.Usage()
		return 2
	}

	token, err := loadSessionToken()
	if err != nil {
		return fail("AUTHENTICATION_REQUIRED: no authenticated CLI session — run `drenyra-engram auth login --token <token> --db <path>` first (%v)", err)
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	resolver := &auth.Resolver{Sessions: st, Mode: auth.RuntimeProduction}
	principal, err := resolver.Authenticate(context.Background(), auth.AuthenticationAssertion{
		Method:     auth.AuthMethodSession,
		Credential: token,
	})
	if err != nil {
		return fail("%v", err)
	}

	key := *requestID
	if strings.TrimSpace(key) == "" {
		key, err = newRequestID()
		if err != nil {
			return fail("generate request id: %v", err)
		}
	}
	result, err := server.New(st, "cli").PlaceHold(context.Background(), core.PlaceHoldCommand{
		ObjectID:       rest[0],
		Kind:           core.HoldKind(*kind),
		Reason:         *reason,
		OwnerSubjectID: *owners,
		RequestID:      key,
	}, principal)
	if err != nil {
		return fail("%v", err)
	}
	return emit(result)
}

// cmdHoldLift closes ONE placed hold ONE-WAY (v0.8 batch 3, design §3.2/§7):
// the principal is DERIVED from the stored CLI session, never declared by the
// caller. The authenticated lift gate (extended policy, lift_hold action),
// (tenant, requestId) idempotency, the one-way closure guard (ALREADY_DECIDED
// on a fresh lift of an already-lifted hold) and the hold_lifted receipt all
// live in the store. EMERGENCY BYPASS: no closed-period gate.
func cmdHoldLift(args []string) int {
	fs := flag.NewFlagSet("hold lift", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	reason := fs.String("reason", "", "REQUIRED lift justification")
	requestID := fs.String("request-id", "", "tenant-scoped idempotency key (optional; a UUID is generated when absent)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram hold lift <hold-id> --reason <text> [--request-id <id>] [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--reason": true, "--request-id": true})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 || strings.TrimSpace(*reason) == "" {
		fs.Usage()
		return 2
	}

	token, err := loadSessionToken()
	if err != nil {
		return fail("AUTHENTICATION_REQUIRED: no authenticated CLI session — run `drenyra-engram auth login --token <token> --db <path>` first (%v)", err)
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	resolver := &auth.Resolver{Sessions: st, Mode: auth.RuntimeProduction}
	principal, err := resolver.Authenticate(context.Background(), auth.AuthenticationAssertion{
		Method:     auth.AuthMethodSession,
		Credential: token,
	})
	if err != nil {
		return fail("%v", err)
	}

	key := *requestID
	if strings.TrimSpace(key) == "" {
		key, err = newRequestID()
		if err != nil {
			return fail("generate request id: %v", err)
		}
	}
	result, err := server.New(st, "cli").LiftHold(context.Background(), core.LiftHoldCommand{
		HoldID:    rest[0],
		Reason:    *reason,
		RequestID: key,
	}, principal)
	if err != nil {
		return fail("%v", err)
	}
	return emit(result)
}

// cmdHoldList is the SCOPE-FIRST hold read (v0.8 batch 3, design §7): the
// caller's exact scope tuple is built from the flags (organizationId defaults
// to cli, companyId is derived from the RUC) and must equal the object's
// stored scope (OBJECT_NOT_FOUND otherwise — cross-tenant invisibility).
// --blocking-kinds (comma-separated subset of
// legal,audit,dispute,fiscalization,other) selects the deployment's blocking
// set; when absent NOTHING is treated as blocking. The output carries every
// hold record plus the active blocking subset. Pure read — no principal.
func cmdHoldList(args []string) int {
	fs := flag.NewFlagSet("hold list", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	ruc := fs.String("ruc", "", "company RUC (exactly 11 digits)")
	period := fs.String("period", "", "fiscal period YYYYMM (optional; exact scope)")
	organization := fs.String("organization", "", "organization id (default cli)")
	blockingKinds := fs.String("blocking-kinds", "", "comma-separated subset of legal,audit,dispute,fiscalization,other (optional; absent = nothing blocks)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram hold list <object-id> --ruc <11 digits> [--period <YYYYMM>] [--organization <id>] [--blocking-kinds <list>] [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--ruc": true, "--period": true, "--organization": true, "--blocking-kinds": true})); err != nil {
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
	scope, err := objectCLIScope(*organization, *ruc, *period)
	if err != nil {
		return fail("%v", err)
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	holds, err := st.HoldsForObject(context.Background(), rest[0], scope)
	if err != nil {
		return fail("%v", err)
	}
	active, err := st.ActiveBlockingHolds(context.Background(), rest[0], scope, splitCLIList(*blockingKinds))
	if err != nil {
		return fail("%v", err)
	}
	return emit(struct {
		Holds               []core.EvidenceHold `json:"holds"`
		ActiveBlockingHolds []core.EvidenceHold `json:"activeBlockingHolds"`
	}{Holds: holds, ActiveBlockingHolds: active})
}

// splitCLIList splits a comma-separated token list (trimmed, empty tokens
// dropped) — the CLI --blocking-kinds wire form.
func splitCLIList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// ──────────────────────────────────────────────
// retention-policy — v0.8 batch 2 policy surface (put/resolve/evaluate)
// ──────────────────────────────────────────────

// cmdRetentionPolicy dispatches the v0.8 batch 2 retention-policy
// subcommands (docs/architecture/evidence-lifecycle-v0.8.md §3.1/§6/§9): put
// (the AUTHENTICATED administration mutation — the principal is DERIVED from
// the stored CLI session like approve, never from a caller flag; there is
// deliberately NO --actor/--subject/--role flag) plus resolve/evaluate
// (SCOPE-FIRST READS: the caller's exact scope tuple is built from the flags
// and matches the policy's scope exactly — cross-tenant invisibility). NO
// holds, NO purge, NO export, NO deletion, NO scheduling.
func cmdRetentionPolicy(args []string) int {
	if len(args) == 0 {
		printRetentionPolicyUsage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "put":
		return cmdRetentionPolicyPut(args[1:])
	case "resolve":
		return cmdRetentionPolicyResolve(args[1:])
	case "evaluate":
		return cmdRetentionPolicyEvaluate(args[1:])
	case "help", "-h", "--help":
		printRetentionPolicyUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "drenyra-engram: unknown retention-policy subcommand %q\n", args[0])
		printRetentionPolicyUsage(os.Stderr)
		return 2
	}
}

func printRetentionPolicyUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: drenyra-engram retention-policy put [--ruc <11 digits>] [--period <YYYYMM>] [--organization <id>] --jurisdiction <token> --legislation <id> --authority <owner> --source <decision> --category <cat> --min-period <YYYYMM> [--expected-version <n>] [--dual-approval-required] [--dual-approver-roles <list>] [--blocking-hold-kinds <list>] [--enabled] [--request-id <id>] [--db <path>]   (authenticated session; identity never from flags)")
	fmt.Fprintln(w, "       drenyra-engram retention-policy resolve [--ruc <11 digits>] [--period <YYYYMM>] [--organization <id>] --jurisdiction <token> --legislation <id> --category <cat> [--db <path>]   (scope-first read)")
	fmt.Fprintln(w, "       drenyra-engram retention-policy evaluate [--ruc <11 digits>] [--period <YYYYMM>] [--organization <id>] --jurisdiction <token> --legislation <id> --category <cat> --object-period <YYYYMM> [--db <path>]   (fail-closed read)")
}

// retentionPolicyCLIScope builds the exact retention-policy scope: the tenant
// (organizationId, default cli) with an OPTIONAL company tuple (--ruc and
// --period, both or neither — a period without a RUC is a usage error). The
// scope is EXACT: a policy written for a scope is only resolved by the same
// scope. AssertValidRetentionScope runs at the adapter so a malformed scope
// is a usage error (exit 2), never a runtime failure.
func retentionPolicyCLIScope(organization, ruc, period string) (core.Scope, error) {
	if strings.TrimSpace(ruc) == "" {
		if strings.TrimSpace(period) != "" {
			return core.Scope{}, errors.New("invalid scope: --period requires --ruc (a tenant-level policy has no company tuple)")
		}
		if organization == "" {
			organization = cliOrganizationID
		}
		scope := core.Scope{Kind: core.ScopeKindCompany, OrganizationID: organization}
		if err := core.AssertValidRetentionScope(scope); err != nil {
			return core.Scope{}, err
		}
		return scope, nil
	}
	return objectCLIScope(organization, ruc, period)
}

// retentionPolicyResolveOutput mirrors the HTTP resolve response shape
// (internal/server retentionPolicyResolveResponse) so CLI/HTTP/MCP verdicts
// stay byte-identical.
type retentionPolicyResolveOutput struct {
	Policy  core.RetentionPolicy `json:"policy"`
	Matched bool                 `json:"matched"`
}

// cmdRetentionPolicyPut writes ONE immutable retention-policy version (v0.8
// batch 2, design §3.1/§6/§9): the principal is DERIVED from the stored CLI
// session (auth login), never declared by the caller — caller-supplied
// authority is gone. --request-id is optional; a fresh UUID is generated when
// absent. The administration gate ((tenant, requestId) idempotency, the
// expected-version supersession guard, the deny-list-first role check) lives
// in the store; NO receipt is emitted (a policy put is not an object-chain
// act).
func cmdRetentionPolicyPut(args []string) int {
	fs := flag.NewFlagSet("retention-policy put", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	organization := fs.String("organization", "", "organization id (default cli)")
	ruc := fs.String("ruc", "", "company RUC (exactly 11 digits; empty = tenant-level policy)")
	period := fs.String("period", "", "fiscal period YYYYMM (optional; exact scope)")
	jurisdiction := fs.String("jurisdiction", "", "REQUIRED jurisdiction token (^[A-Z][A-Z0-9-]{1,15}$)")
	legislation := fs.String("legislation", "", "REQUIRED regime/family identifier")
	authority := fs.String("authority", "", "REQUIRED policy owner/issuer")
	source := fs.String("source", "", "REQUIRED who decided, when, on what basis")
	category := fs.String("category", "", "REQUIRED retention category")
	minPeriod := fs.String("min-period", "", "REQUIRED deployment-declared YYYYMM retention floor (NO statutory duration claim)")
	expectedVersion := fs.Int64("expected-version", 0, "version of the current chain head the caller reviewed (0 = none)")
	dualApprovalRequired := fs.Bool("dual-approval-required", false, "require a second dual-approval role")
	dualApproverRoles := fs.String("dual-approver-roles", "", "comma-separated subset of controller,tax_responsible (default both)")
	blockingHoldKinds := fs.String("blocking-hold-kinds", "", "comma-separated subset of legal,audit,dispute,fiscalization,other (default the four blocking kinds)")
	enabled := fs.Bool("enabled", false, "enable the policy for resolution")
	requestID := fs.String("request-id", "", "tenant-scoped idempotency key (optional; a UUID is generated when absent)")
	fs.Usage = func() {
		printRetentionPolicyUsage(fs.Output())
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"--db": true, "--organization": true, "--ruc": true, "--period": true,
		"--jurisdiction": true, "--legislation": true, "--authority": true, "--source": true,
		"--category": true, "--min-period": true, "--expected-version": true,
		"--dual-approver-roles": true, "--blocking-hold-kinds": true, "--request-id": true,
	})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 || strings.TrimSpace(*jurisdiction) == "" || strings.TrimSpace(*legislation) == "" ||
		strings.TrimSpace(*authority) == "" || strings.TrimSpace(*source) == "" ||
		strings.TrimSpace(*category) == "" || strings.TrimSpace(*minPeriod) == "" {
		fs.Usage()
		return 2
	}
	scope, err := retentionPolicyCLIScope(*organization, *ruc, *period)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drenyra-engram: %v\n", err)
		return 2
	}

	token, err := loadSessionToken()
	if err != nil {
		return fail("AUTHENTICATION_REQUIRED: no authenticated CLI session — run `drenyra-engram auth login --token <token> --db <path>` first (%v)", err)
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	resolver := &auth.Resolver{Sessions: st, Mode: auth.RuntimeProduction}
	principal, err := resolver.Authenticate(context.Background(), auth.AuthenticationAssertion{
		Method:     auth.AuthMethodSession,
		Credential: token,
	})
	if err != nil {
		return fail("%v", err)
	}

	key, err := cliRequestID(*requestID)
	if err != nil {
		return fail("retention-policy put: %v", err)
	}

	api := server.New(st, "cli")
	result, err := api.PutRetentionPolicy(context.Background(), core.PutRetentionPolicyCommand{
		Scope:                scope,
		Jurisdiction:         *jurisdiction,
		Legislation:          *legislation,
		Authority:            *authority,
		Source:               *source,
		Category:             *category,
		MinPeriod:            *minPeriod,
		ExpectedVersion:      *expectedVersion,
		DualApprovalRequired: *dualApprovalRequired,
		DualApproverRoles:    splitCSV(*dualApproverRoles),
		BlockingHoldKinds:    splitCSV(*blockingHoldKinds),
		Enabled:              *enabled,
		RequestID:            key,
	}, principal)
	if err != nil {
		return fail("%v", err)
	}
	return emit(result)
}

// cmdRetentionPolicyResolve is the SCOPE-FIRST exact resolution read (design
// §6): the exact scope tuple + (jurisdiction, legislation, category) against
// the HIGHEST version of an ENABLED policy. A caller whose exact scope differs
// from the policy's scope sees matched=false (cross-tenant invisibility),
// never the policy. Pure read — no session, no mutation.
func cmdRetentionPolicyResolve(args []string) int {
	fs := flag.NewFlagSet("retention-policy resolve", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	organization := fs.String("organization", "", "organization id (default cli)")
	ruc := fs.String("ruc", "", "company RUC (exactly 11 digits; empty = tenant-level scope)")
	period := fs.String("period", "", "fiscal period YYYYMM (optional; exact scope)")
	jurisdiction := fs.String("jurisdiction", "", "REQUIRED jurisdiction token")
	legislation := fs.String("legislation", "", "REQUIRED regime/family identifier")
	category := fs.String("category", "", "REQUIRED retention category")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram retention-policy resolve [--ruc <11 digits>] [--period <YYYYMM>] [--organization <id>] --jurisdiction <token> --legislation <id> --category <cat> [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"--db": true, "--organization": true, "--ruc": true, "--period": true,
		"--jurisdiction": true, "--legislation": true, "--category": true,
	})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 || strings.TrimSpace(*jurisdiction) == "" || strings.TrimSpace(*legislation) == "" || strings.TrimSpace(*category) == "" {
		fs.Usage()
		return 2
	}
	scope, err := retentionPolicyCLIScope(*organization, *ruc, *period)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drenyra-engram: %v\n", err)
		return 2
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	api := server.New(st, "cli")
	policy, matched, err := api.ResolveRetentionPolicy(context.Background(), scope, *jurisdiction, *legislation, *category)
	if err != nil {
		return fail("%v", err)
	}
	return emit(retentionPolicyResolveOutput{Policy: policy, Matched: matched})
}

// cmdRetentionPolicyEvaluate is the fail-closed eligibility read (design §6):
// UNKNOWN_RETENTION_STATE unless an exact active policy resolves; otherwise the
// pure eligible|not_due dimension of the object's period vs the
// deployment-declared min_period floor. Read-only — never deletes, never
// schedules, no statutory duration claim.
func cmdRetentionPolicyEvaluate(args []string) int {
	fs := flag.NewFlagSet("retention-policy evaluate", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	organization := fs.String("organization", "", "organization id (default cli)")
	ruc := fs.String("ruc", "", "company RUC (exactly 11 digits; empty = tenant-level scope)")
	period := fs.String("period", "", "fiscal period YYYYMM (optional; exact scope)")
	jurisdiction := fs.String("jurisdiction", "", "REQUIRED jurisdiction token")
	legislation := fs.String("legislation", "", "REQUIRED regime/family identifier")
	category := fs.String("category", "", "REQUIRED retention category")
	objectPeriod := fs.String("object-period", "", "REQUIRED the object's fiscal period YYYYMM")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram retention-policy evaluate [--ruc <11 digits>] [--period <YYYYMM>] [--organization <id>] --jurisdiction <token> --legislation <id> --category <cat> --object-period <YYYYMM> [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"--db": true, "--organization": true, "--ruc": true, "--period": true,
		"--jurisdiction": true, "--legislation": true, "--category": true, "--object-period": true,
	})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 || strings.TrimSpace(*jurisdiction) == "" || strings.TrimSpace(*legislation) == "" ||
		strings.TrimSpace(*category) == "" || strings.TrimSpace(*objectPeriod) == "" {
		fs.Usage()
		return 2
	}
	if !core.IsValidPeriod(*objectPeriod) {
		fmt.Fprintf(os.Stderr, "drenyra-engram: invalid --object-period %q: expected YYYYMM with month 01-12\n", *objectPeriod)
		return 2
	}
	scope, err := retentionPolicyCLIScope(*organization, *ruc, *period)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drenyra-engram: %v\n", err)
		return 2
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	api := server.New(st, "cli")
	result, err := api.EvaluatePurgeEligibility(context.Background(), core.EvaluatePurgeEligibilityInput{
		Scope:        scope,
		Jurisdiction: *jurisdiction,
		Legislation:  *legislation,
		Category:     *category,
		ObjectPeriod: *objectPeriod,
	})
	if err != nil {
		return fail("%v", err)
	}
	return emit(result)
}

// ──────────────────────────────────────────────
// purge — v0.8 batch 4 evidence purge pipeline (request/approve/reject/
// cancel/withdraw/execute)
// ──────────────────────────────────────────────

// cmdPurge dispatches the v0.8 batch 4 evidence purge subcommands
// (docs/architecture/evidence-lifecycle-v0.8.md §2/§3/§9/§10/§11): request,
// approve, reject, cancel, withdraw and execute are the AUTHENTICATED
// principal mutations (the principal is DERIVED from the stored CLI session
// like approve, never from a caller flag — there is deliberately NO
// --actor/--subject/--role flag). approve serves BOTH the default approver
// (order 1) and the dual second approver (order 2) — the store derives the
// order from the decision ledger. The read-only lifecycle export lives under
// `export lifecycle` (design §12). NO deletion outside the guarded execute
// protocol.
func cmdPurge(args []string) int {
	if len(args) == 0 {
		printPurgeUsage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "request":
		return cmdPurgeRequest(args[1:])
	case "approve":
		return cmdPurgeApprove(args[1:])
	case "reject":
		return cmdPurgeReject(args[1:])
	case "cancel":
		return cmdPurgeCancel(args[1:])
	case "withdraw":
		return cmdPurgeWithdraw(args[1:])
	case "execute":
		return cmdPurgeExecute(args[1:])
	case "help", "-h", "--help":
		printPurgeUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "drenyra-engram: unknown purge subcommand %q\n", args[0])
		printPurgeUsage(os.Stderr)
		return 2
	}
}

func printPurgeUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: drenyra-engram purge request <object-id> --jurisdiction <token> --legislation <id> --category <cat> --expected-hash <hash> --reason <text> [--request-id <id>] [--db <path>]   (authenticated session; identity never from flags)")
	fmt.Fprintln(w, "       drenyra-engram purge approve <request-id> --expected-hash <hash> --reason <text> [--request-id-key <id>] [--db <path>]   (authenticated session; order 1 or the dual second approval — derived by the store)")
	fmt.Fprintln(w, "       drenyra-engram purge reject <request-id> --reason <text> [--request-id-key <id>] [--db <path>]   (authenticated session; terminal)")
	fmt.Fprintln(w, "       drenyra-engram purge cancel <request-id> [--request-id-key <id>] [--db <path>]   (authenticated session; original requester retraction)")
	fmt.Fprintln(w, "       drenyra-engram purge withdraw <request-id> --reason <text> [--request-id-key <id>] [--db <path>]   (authenticated session; approval retraction)")
	fmt.Fprintln(w, "       drenyra-engram purge execute <request-id> --expected-hash <hash> [--reason <text>] [--execution-id <id>] [--db <path>]   (authenticated session; two-phase receipt-covered protocol)")
}

// cliPurgePrincipal authenticates the stored CLI session and returns the
// pre-verified principal — the ONLY identity source of the purge mutations
// (ADR-003: caller-supplied authority is gone).
func cliPurgePrincipal(st *store.SQLiteStore) (auth.VerifiedApprovalPrincipal, error) {
	token, err := loadSessionToken()
	if err != nil {
		return auth.VerifiedApprovalPrincipal{}, fmt.Errorf("AUTHENTICATION_REQUIRED: no authenticated CLI session — run `drenyra-engram auth login --token <token> --db <path>` first (%v)", err)
	}
	resolver := &auth.Resolver{Sessions: st, Mode: auth.RuntimeProduction}
	return resolver.Authenticate(context.Background(), auth.AuthenticationAssertion{
		Method:     auth.AuthMethodSession,
		Credential: token,
	})
}

// cmdPurgeRequest opens ONE purge pipeline per object (v0.8 batch 4, design
// §2/§3.3/§9/§10): the FULL blocker set (closed-period gate → exact active
// retention resolution → eligibility → active blocking hold scan → expected
// lifecycle hash) BEFORE the authenticated request gate (accounting ladder),
// (tenant, requestId) idempotency, the immutable request row (one per
// object), the retention binding and the purge_requested event + receipt all
// live in the store. --request-id is optional; a fresh UUID is generated when
// absent.
func cmdPurgeRequest(args []string) int {
	fs := flag.NewFlagSet("purge request", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	jurisdiction := fs.String("jurisdiction", "", "REQUIRED jurisdiction token (^[A-Z][A-Z0-9-]{1,15}$)")
	legislation := fs.String("legislation", "", "REQUIRED regime/family identifier (resolution evidence)")
	category := fs.String("category", "", "REQUIRED retention category (resolution evidence)")
	expectedHash := fs.String("expected-hash", "", "REQUIRED the canonical lifecycle snapshot hash (H1) the requester reviewed (64 lowercase hex)")
	reason := fs.String("reason", "", "REQUIRED non-empty justification")
	requestID := fs.String("request-id", "", "tenant-scoped idempotency key (optional; a UUID is generated when absent)")
	fs.Usage = func() {
		printPurgeUsage(fs.Output())
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"--db": true, "--jurisdiction": true, "--legislation": true, "--category": true,
		"--expected-hash": true, "--reason": true, "--request-id": true,
	})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 || strings.TrimSpace(*jurisdiction) == "" || strings.TrimSpace(*legislation) == "" ||
		strings.TrimSpace(*category) == "" || strings.TrimSpace(*expectedHash) == "" || strings.TrimSpace(*reason) == "" {
		fs.Usage()
		return 2
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	principal, err := cliPurgePrincipal(st)
	if err != nil {
		return fail("%v", err)
	}
	key, err := cliRequestID(*requestID)
	if err != nil {
		return fail("purge request: %v", err)
	}
	result, err := server.New(st, "cli").RequestPurge(context.Background(), core.RequestPurgeCommand{
		ObjectID:              rest[0],
		Jurisdiction:          *jurisdiction,
		Legislation:           *legislation,
		Category:              *category,
		ExpectedLifecycleHash: *expectedHash,
		Reason:                *reason,
		RequestID:             key,
	}, principal)
	if err != nil {
		return fail("%v", err)
	}
	return emit(result)
}

// cmdPurgeApprove records ONE human approval (v0.8 batch 4, design §2/§3.4/
// §8/§9): the SAME command serves the default approver (order 1) and the
// DISTINCT dual second approver (order 2) — the store derives the order from
// the decision ledger, re-checks the FULL blocker set BEFORE authz and
// enforces requester ≠ approver plus the distinct-principal rule store-side.
// --request-id-key is optional; a fresh UUID is generated when absent.
func cmdPurgeApprove(args []string) int {
	fs := flag.NewFlagSet("purge approve", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	expectedHash := fs.String("expected-hash", "", "REQUIRED the reviewed canonical lifecycle snapshot hash H1 (64 lowercase hex; for the second approval, the first approval's resulting hash)")
	reason := fs.String("reason", "", "REQUIRED non-empty justification")
	requestIDKey := fs.String("request-id-key", "", "tenant-scoped idempotency key of this approval act (optional; a UUID is generated when absent)")
	fs.Usage = func() {
		printPurgeUsage(fs.Output())
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"--db": true, "--expected-hash": true, "--reason": true, "--request-id-key": true,
	})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 || strings.TrimSpace(*expectedHash) == "" || strings.TrimSpace(*reason) == "" {
		fs.Usage()
		return 2
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	principal, err := cliPurgePrincipal(st)
	if err != nil {
		return fail("%v", err)
	}
	key, err := cliRequestID(*requestIDKey)
	if err != nil {
		return fail("purge approve: %v", err)
	}
	result, err := server.New(st, "cli").ApprovePurge(context.Background(), core.ApprovePurgeCommand{
		RequestID:             rest[0],
		ExpectedLifecycleHash: *expectedHash,
		Reason:                *reason,
		RequestIDKey:          key,
	}, principal)
	if err != nil {
		return fail("%v", err)
	}
	return emit(result)
}

// cmdPurgeReject records the TERMINAL rejection (v0.8 batch 4, design §2):
// an authenticated default approver closes the request with a reason; the
// projection moves to purge_rejected and never re-opens.
func cmdPurgeReject(args []string) int {
	fs := flag.NewFlagSet("purge reject", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	reason := fs.String("reason", "", "REQUIRED non-empty rejection justification")
	requestIDKey := fs.String("request-id-key", "", "tenant-scoped idempotency key of this act (optional; a UUID is generated when absent)")
	fs.Usage = func() {
		printPurgeUsage(fs.Output())
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"--db": true, "--reason": true, "--request-id-key": true,
	})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 || strings.TrimSpace(*reason) == "" {
		fs.Usage()
		return 2
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	principal, err := cliPurgePrincipal(st)
	if err != nil {
		return fail("%v", err)
	}
	key, err := cliRequestID(*requestIDKey)
	if err != nil {
		return fail("purge reject: %v", err)
	}
	result, err := server.New(st, "cli").RejectPurge(context.Background(), core.RejectPurgeCommand{
		RequestID:    rest[0],
		Reason:       *reason,
		RequestIDKey: key,
	}, principal)
	if err != nil {
		return fail("%v", err)
	}
	return emit(result)
}

// cmdPurgeCancel is the ORIGINAL requester's idempotent retraction (v0.8
// batch 4, design §2): the pipeline returns to stored and a fresh request is
// a fresh act on the same one-per-object row. There is no reason flag — a
// cancellation carries no command evidence.
func cmdPurgeCancel(args []string) int {
	fs := flag.NewFlagSet("purge cancel", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	requestIDKey := fs.String("request-id-key", "", "tenant-scoped idempotency key of this act (optional; a UUID is generated when absent)")
	fs.Usage = func() {
		printPurgeUsage(fs.Output())
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"--db": true, "--request-id-key": true,
	})); err != nil {
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

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	principal, err := cliPurgePrincipal(st)
	if err != nil {
		return fail("%v", err)
	}
	key, err := cliRequestID(*requestIDKey)
	if err != nil {
		return fail("purge cancel: %v", err)
	}
	result, err := server.New(st, "cli").CancelPurge(context.Background(), core.CancelPurgeCommand{
		RequestID:    rest[0],
		RequestIDKey: key,
	}, principal)
	if err != nil {
		return fail("%v", err)
	}
	return emit(result)
}

// cmdPurgeWithdraw is the approval retraction — the documented cleanup (v0.8
// batch 4, design §2/§7): a default approver or dual second approver
// withdraws an approved pipeline with a reason; the pipeline returns to
// stored.
func cmdPurgeWithdraw(args []string) int {
	fs := flag.NewFlagSet("purge withdraw", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	reason := fs.String("reason", "", "REQUIRED non-empty withdrawal justification")
	requestIDKey := fs.String("request-id-key", "", "tenant-scoped idempotency key of this act (optional; a UUID is generated when absent)")
	fs.Usage = func() {
		printPurgeUsage(fs.Output())
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"--db": true, "--reason": true, "--request-id-key": true,
	})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 || strings.TrimSpace(*reason) == "" {
		fs.Usage()
		return 2
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	principal, err := cliPurgePrincipal(st)
	if err != nil {
		return fail("%v", err)
	}
	key, err := cliRequestID(*requestIDKey)
	if err != nil {
		return fail("purge withdraw: %v", err)
	}
	result, err := server.New(st, "cli").WithdrawPurge(context.Background(), core.WithdrawPurgeCommand{
		RequestID:    rest[0],
		Reason:       *reason,
		RequestIDKey: key,
	}, principal)
	if err != nil {
		return fail("%v", err)
	}
	return emit(result)
}

// cmdPurgeExecute physically executes an APPROVED purge pipeline (v0.8 batch
// 4, design §2/§3.7/§9/§11): the TWO-PHASE, RECEIPT-COVERED protocol (durable
// intent → byte removal outside SQL with the pre-removal hash check → durable
// completion) all lives in the store. --execution-id is the (tenant,
// executionId) idempotency key of THIS attempt (optional; a fresh UUID is
// generated when absent — a retry after an interrupted attempt MUST use a
// FRESH id, replaying the same id returns the stored outcome). Only object
// bytes are removed; the immutable audit rows never change.
func cmdPurgeExecute(args []string) int {
	fs := flag.NewFlagSet("purge execute", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	expectedHash := fs.String("expected-hash", "", "REQUIRED the reviewed canonical lifecycle snapshot hash (64 lowercase hex; the store fails closed on drift)")
	reason := fs.String("reason", "", "optional execution note")
	executionID := fs.String("execution-id", "", "tenant-scoped idempotency key of this execution attempt (optional; a UUID is generated when absent)")
	fs.Usage = func() {
		printPurgeUsage(fs.Output())
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"--db": true, "--expected-hash": true, "--reason": true, "--execution-id": true,
	})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 || strings.TrimSpace(*expectedHash) == "" {
		fs.Usage()
		return 2
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	principal, err := cliPurgePrincipal(st)
	if err != nil {
		return fail("%v", err)
	}
	key, err := cliRequestID(*executionID)
	if err != nil {
		return fail("purge execute: %v", err)
	}
	result, err := server.New(st, "cli").FinalizePurge(context.Background(), core.ExecutePurgeCommand{
		RequestID:             rest[0],
		ExpectedLifecycleHash: *expectedHash,
		Reason:                *reason,
		ExecutionID:           key,
	}, principal)
	if err != nil {
		return fail("%v", err)
	}
	return emit(result)
}

// ──────────────────────────────────────────────
// export — v0.8 batch 4 deterministic evidence-lifecycle export (design §12)
// ──────────────────────────────────────────────

// cmdExport dispatches the read-only export subcommands. This slice ships the
// deterministic lifecycle bundle (`export lifecycle`) — a READ-ONLY,
// tenant/RUC-scoped audit query that emits NO receipt and never reads object
// bytes.
func cmdExport(args []string) int {
	if len(args) == 0 {
		printExportUsage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "lifecycle":
		return cmdExportLifecycle(args[1:])
	case "help", "-h", "--help":
		printExportUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "drenyra-engram: unknown export subcommand %q\n", args[0])
		printExportUsage(os.Stderr)
		return 2
	}
}

func printExportUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: drenyra-engram export lifecycle [--ruc <11 digits>] [--period <YYYYMM>] [--organization <id>] [--db <path>]   (read-only, scope-first; empty --period selects all periods of the RUC)")
}

// cmdExportLifecycle produces the deterministic evidence-lifecycle audit
// bundle (v0.8 batch 4, design §12 — WU-3/WU-4): a READ-ONLY, tenant/RUC-
// scoped query for the exact company scope built from the flags (organizationId
// defaults to cli; --ruc is REQUIRED — the export requires an exact company
// scope; an empty --period selects ALL periods of the RUC). The store enforces
// the tenant/company/RUC/period boundary structurally and the bundle fails
// closed on any cross-scope row. The export emits NO receipt and never reads
// object bytes (purged objects export immutable metadata + lifecycle +
// receipt evidence only). Pure read — no session, no mutation.
func cmdExportLifecycle(args []string) int {
	fs := flag.NewFlagSet("export lifecycle", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	organization := fs.String("organization", "", "organization id (default cli)")
	ruc := fs.String("ruc", "", "REQUIRED company RUC (exactly 11 digits; the export requires an exact company scope)")
	period := fs.String("period", "", "fiscal period YYYYMM (optional; empty selects ALL periods of the RUC)")
	fs.Usage = func() {
		printExportUsage(fs.Output())
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"--db": true, "--organization": true, "--ruc": true, "--period": true,
	})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 || strings.TrimSpace(*ruc) == "" {
		fs.Usage()
		return 2
	}
	scope, err := objectCLIScope(*organization, *ruc, *period)
	if err != nil {
		return fail("%v", err)
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	bundle, err := server.New(st, "cli").ExportEvidenceLifecycle(context.Background(), core.EvidenceExportCriteria{Scope: scope})
	if err != nil {
		return fail("%v", err)
	}
	return emit(bundle)
}

// multiFlag collects repeated string flags (e.g. --ref a --ref b).
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }

func (m *multiFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}

// splitCSV splits a comma-separated list, trimming spaces and dropping empties
// (the CLI's role-list parser for auth seed-local-dev).
func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// ──────────────────────────────────────────────
// review — v0.9.0 review workspace (docs/architecture/review-workspace-v0.9.md)
// ──────────────────────────────────────────────

// cmdReview dispatches the review workspace subcommands: queue and detail are
// SCOPE-FIRST READS (no authenticated session needed — reads never authorize);
// reject and return are the AUTHENTICATED decision commands whose principal is
// DERIVED from the stored CLI session (auth login), never declared by the caller
// — there is deliberately NO --actor/--subject flag on them (the same ADR-003
// contract as approve and the purge decisions).
func cmdReview(args []string) int {
	if len(args) == 0 {
		printReviewUsage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "queue":
		return cmdReviewQueue(args[1:])
	case "detail":
		return cmdReviewDetail(args[1:])
	case "reject":
		return cmdReviewReject(args[1:])
	case "return":
		return cmdReviewReturn(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "drenyra-engram: unknown review subcommand %q\n", args[0])
		printReviewUsage(os.Stderr)
		return 2
	}
}

// printReviewUsage prints the review subcommand surface.
func printReviewUsage(w io.Writer) {
	fmt.Fprintln(w, `usage:
  drenyra-engram review queue <ruc> [--period <YYYYMM>] [--limit <n>] [--offset <n>] [--db <path>]   (v0.9.0 scope-first read)
  drenyra-engram review detail <memory-id> --ruc <11 digits> [--period <YYYYMM>] [--db <path>]   (v0.9.0 scope-first read)
  drenyra-engram review reject <memory-id> --expected-envelope <hash> --reason <text> [--request-id <id>] [--db <path>]   (v0.9.0 authenticated human gate)
  drenyra-engram review return <memory-id> --expected-envelope <hash> --reason <text> [--request-id <id>] [--db <path>]   (v0.9.0 authenticated human gate)`)
}

// cmdReviewQueue lists the pending_review queue of an EXACT company scope
// (design §3): scope-first, deterministically ordered, bounded pagination
// (--limit default 50 max 200, --offset default 0). Read-only; no session.
func cmdReviewQueue(args []string) int {
	fs := flag.NewFlagSet("review queue", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	period := fs.String("period", "", "fiscal period YYYYMM (optional)")
	limit := fs.Int("limit", 0, "page size (default 50, max 200)")
	offset := fs.Int("offset", 0, "page offset (default 0)")
	fs.Usage = func() {
		printReviewUsage(fs.Output())
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"--db": true, "--period": true, "--limit": true, "--offset": true,
	})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 || *limit < 0 || *offset < 0 {
		fs.Usage()
		return 2
	}
	scope, err := cliCompanyScope(rest[0], *period)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drenyra-engram: %v\n", err)
		return 2
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	page, err := server.New(st, "cli").ReviewQueue(context.Background(), core.ReviewQueueQuery{
		Scope:  scope,
		Limit:  *limit,
		Offset: *offset,
	})
	if err != nil {
		return fail("%v", err)
	}
	return emit(page)
}

// cmdReviewDetail composes the review of ONE pending revision, scope-guarded
// (design §4): the diff, evidence/rules state, open judgments, review metadata
// and the boundary notice. Read-only; no session.
func cmdReviewDetail(args []string) int {
	fs := flag.NewFlagSet("review detail", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	rruc := fs.String("ruc", "", "company RUC (exactly 11 digits; required)")
	period := fs.String("period", "", "fiscal period YYYYMM (optional)")
	fs.Usage = func() {
		printReviewUsage(fs.Output())
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"--db": true, "--ruc": true, "--period": true,
	})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 || strings.TrimSpace(*rruc) == "" {
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

	detail, err := server.New(st, "cli").ReviewDetail(context.Background(), rest[0], scope)
	if err != nil {
		return fail("%v", err)
	}
	return emit(detail)
}

// cmdReviewReject rejects a pending_review memory (design §5): pending_review →
// rejected (terminal), AUTHENTICATED via the stored CLI session, idempotent by
// (tenant, requestId) and hash-guarded. --expected-envelope is REQUIRED (the
// envelope hash the reviewer actually saw); --reason is REQUIRED when the
// memory's risk class demands it (material/critical or
// closing/declaration/sunat_filing) and always persisted; --request-id is
// optional (a UUID is generated when absent).
func cmdReviewReject(args []string) int {
	fs := flag.NewFlagSet("review reject", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	expectedEnvelope := fs.String("expected-envelope", "", "REQUIRED envelope hash the reviewer actually saw")
	reason := fs.String("reason", "", "human rejection reason (REQUIRED for material/critical or closing/declaration/sunat_filing; always persisted)")
	requestID := fs.String("request-id", "", "tenant-scoped idempotency key (optional; a UUID is generated when absent)")
	fs.Usage = func() {
		printReviewUsage(fs.Output())
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"--db": true, "--expected-envelope": true, "--reason": true, "--request-id": true,
	})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 || strings.TrimSpace(*expectedEnvelope) == "" {
		fs.Usage()
		return 2
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	principal, err := cliReviewPrincipal(st)
	if err != nil {
		return fail("%v", err)
	}
	key, err := cliRequestID(*requestID)
	if err != nil {
		return fail("review reject: %v", err)
	}
	result, err := server.New(st, "cli").RejectMemory(context.Background(), core.RejectMemoryCommand{
		MemoryID:             rest[0],
		ExpectedEnvelopeHash: *expectedEnvelope,
		Reason:               *reason,
		RequestID:            key,
	}, principal)
	if err != nil {
		return fail("%v", err)
	}
	return emit(result)
}

// cmdReviewReturn RETURNS a pending_review memory to its proposer for correction
// (design §5): pending_review → returned (NON-terminal — an agent Save on the
// returned memory creates a NEW revision that re-enters pending_review).
// AUTHENTICATED via the stored CLI session; the reason is REQUIRED (a correction
// request — the reason tells the agent what to fix).
func cmdReviewReturn(args []string) int {
	fs := flag.NewFlagSet("review return", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)")
	expectedEnvelope := fs.String("expected-envelope", "", "REQUIRED envelope hash the reviewer actually saw")
	reason := fs.String("reason", "", "REQUIRED correction request reason")
	requestID := fs.String("request-id", "", "tenant-scoped idempotency key (optional; a UUID is generated when absent)")
	fs.Usage = func() {
		printReviewUsage(fs.Output())
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{
		"--db": true, "--expected-envelope": true, "--reason": true, "--request-id": true,
	})); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	rest := fs.Args()
	if len(rest) != 1 || strings.TrimSpace(*expectedEnvelope) == "" || strings.TrimSpace(*reason) == "" {
		fs.Usage()
		return 2
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	principal, err := cliReviewPrincipal(st)
	if err != nil {
		return fail("%v", err)
	}
	key, err := cliRequestID(*requestID)
	if err != nil {
		return fail("review return: %v", err)
	}
	result, err := server.New(st, "cli").ReturnMemory(context.Background(), core.ReturnMemoryCommand{
		MemoryID:             rest[0],
		ExpectedEnvelopeHash: *expectedEnvelope,
		Reason:               *reason,
		RequestID:            key,
	}, principal)
	if err != nil {
		return fail("%v", err)
	}
	return emit(result)
}

// cliReviewPrincipal derives the AUTHENTICATED review principal from the stored
// CLI session (auth login) — never from a caller flag; a missing session fails
// closed with AUTHENTICATION_REQUIRED pointing at auth login.
func cliReviewPrincipal(st *store.SQLiteStore) (auth.VerifiedApprovalPrincipal, error) {
	token, err := loadSessionToken()
	if err != nil {
		return auth.VerifiedApprovalPrincipal{}, fmt.Errorf("AUTHENTICATION_REQUIRED: no authenticated CLI session — run `drenyra-engram auth login --token <token> --db <path>` first (%v)", err)
	}
	resolver := &auth.Resolver{Sessions: st, Mode: auth.RuntimeProduction}
	return resolver.Authenticate(context.Background(), auth.AuthenticationAssertion{
		Method:     auth.AuthMethodSession,
		Credential: token,
	})
}

func printUsage(w *os.File) {
	fmt.Fprintln(w, `drenyra-engram — institutional accounting memory engine (v0.2 Go foundation)

Usage:
  drenyra-engram save <json-file> [--db <path>]
  drenyra-engram record <json-file> [--db <path>]
  drenyra-engram search <query> --company <ruc> [--period <YYYYMM>] [--any] [--db <path>]
  drenyra-engram context <ruc> [--period <YYYYMM>] [--db <path>]
  drenyra-engram doctor [--db <path>] [--objects <dir>]
  drenyra-engram doctor --drill-copy <copy.db> --snapshot-manifest <manifest.json>   (G-6 full diagnostics on a MARKED drill copy only; never a live database)
  drenyra-engram compare <idA> <idB> [--db <path>]
  drenyra-engram approve <id> --expected-envelope <hash> --reason <text> [--db <path>]   (authenticated human gate)
  drenyra-engram review queue <ruc> [--period <YYYYMM>] [--limit <n>] [--offset <n>] [--db <path>]   (v0.9.0 scope-first read)
  drenyra-engram review detail <memory-id> --ruc <11 digits> [--period <YYYYMM>] [--db <path>]   (v0.9.0 scope-first read)
  drenyra-engram review reject <memory-id> --expected-envelope <hash> --reason <text> [--request-id <id>] [--db <path>]   (v0.9.0 authenticated human gate; terminal)
  drenyra-engram review return <memory-id> --expected-envelope <hash> --reason <text> [--request-id <id>] [--db <path>]   (v0.9.0 authenticated human gate; non-terminal)
  drenyra-engram auth login --token <token> [--db <path>]
  drenyra-engram auth seed-local-dev --db <path> --tenant <id> --company <id> --ruc <11 digits> --subject <id> --roles <list>   (DRENYRA_ENV=local_dev only)
  drenyra-engram keys init
  drenyra-engram keys show
  drenyra-engram keys rotate --db <path>
  drenyra-engram judge propose <from-id> <to-id> --relation <rel> --reason <text> [--predecessor <id>] [--request-id <id>] [--db <path>]   (agent provenance)
  drenyra-engram judge confirm <judgment-id> --resolution <text> --expected-hash <hash> [--request-id <id>] [--db <path>]   (authenticated human gate)
  drenyra-engram judge reject <judgment-id> --reason <text> --expected-hash <hash> [--request-id <id>] [--db <path>]   (authenticated human gate)
  drenyra-engram judge withdraw <judgment-id> [--request-id <id>] [--db <path>]
  drenyra-engram judge show <judgment-id> [--db <path>]
  drenyra-engram reconcile propose <left-id> <right-id> --method <m> --currency <c> --left-amount-cents <int> --right-amount-cents <int> [--tolerance-cents <int>] [--predecessor <id>] --reason <text> [--request-id <id>] [--db <path>]   (agent provenance)
  drenyra-engram reconcile confirm <reconciliation-id> --resolution <text> --expected-hash <hash> [--request-id <id>] [--db <path>]   (authenticated human gate)
  drenyra-engram reconcile reject <reconciliation-id> --reason <text> --expected-hash <hash> [--request-id <id>] [--db <path>]   (authenticated human gate)
  drenyra-engram reconcile withdraw <reconciliation-id> [--request-id <id>] [--db <path>]
  drenyra-engram reconcile show <reconciliation-id> [--db <path>]
  drenyra-engram reject <id> [--actor <name>] [--db <path>]    (human gate)
  drenyra-engram void <id> [--actor <name>] [--db <path>]
  drenyra-engram supersede <id> --target <targetId> [--actor <name>] [--db <path>]
  drenyra-engram link-evidence <id> --ref <ref> [--ref <ref>...] --ruc <11 digits> [--period <YYYYMM>] [--organization <id>] [--db <path>]
  drenyra-engram object store <file> --ruc <11 digits> [--period <YYYYMM>] [--content-type <mime>] [--objects <dir>] [--db <path>]   (v0.7.0 WORM evidence object; never an approval)
  drenyra-engram object get <sha256> --ruc <11 digits> [--period <YYYYMM>] [--objects <dir>] [--db <path>]   (scope-first read)
  drenyra-engram hold place <object-id> --kind <legal|audit|dispute|fiscalization|other> --reason <text> --owner <subject-id> [--request-id <id>] [--db <path>]   (v0.8 authenticated preservation act; emergency bypass: no closed-period gate)
  drenyra-engram hold lift <hold-id> --reason <text> [--request-id <id>] [--db <path>]   (v0.8 authenticated one-way closure)
  drenyra-engram hold list <object-id> --ruc <11 digits> [--period <YYYYMM>] [--blocking-kinds <list>] [--db <path>]   (scope-first read)
  drenyra-engram retention-policy put [--ruc <11 digits>] [--period <YYYYMM>] --jurisdiction <token> --legislation <id> --authority <owner> --source <decision> --category <cat> --min-period <YYYYMM> [--db <path>]   (v0.8 authenticated administration mutation)
  drenyra-engram retention-policy resolve [--ruc <11 digits>] [--period <YYYYMM>] --jurisdiction <token> --legislation <id> --category <cat> [--db <path>]   (scope-first read)
  drenyra-engram retention-policy evaluate [--ruc <11 digits>] [--period <YYYYMM>] --jurisdiction <token> --legislation <id> --category <cat> --object-period <YYYYMM> [--db <path>]   (fail-closed read)
  drenyra-engram purge request <object-id> --jurisdiction <token> --legislation <id> --category <cat> --expected-hash <hash> --reason <text> [--request-id <id>] [--db <path>]   (v0.8 authenticated purge pipeline)
  drenyra-engram purge approve <request-id> --expected-hash <hash> --reason <text> [--request-id-key <id>] [--db <path>]   (v0.8 authenticated; order 1 or the dual second approval — derived by the store)
  drenyra-engram purge reject <request-id> --reason <text> [--request-id-key <id>] [--db <path>]   (v0.8 authenticated; terminal)
  drenyra-engram purge cancel <request-id> [--request-id-key <id>] [--db <path>]   (v0.8 authenticated; original requester retraction)
  drenyra-engram purge withdraw <request-id> --reason <text> [--request-id-key <id>] [--db <path>]   (v0.8 authenticated; approval retraction)
  drenyra-engram purge execute <request-id> --expected-hash <hash> [--reason <text>] [--execution-id <id>] [--db <path>]   (v0.8 authenticated; two-phase receipt-covered protocol)
  drenyra-engram export lifecycle [--ruc <11 digits>] [--period <YYYYMM>] [--organization <id>] [--db <path>]   (v0.8 read-only deterministic audit bundle)
  drenyra-engram period-summary <ruc> [--period <YYYYMM>] [--db <path>]
  drenyra-engram compare-periods <ruc> --from <YYYYMM> --to <YYYYMM> [--db <path>]
  drenyra-engram close create <ruc> --period YYYYMM [--total code=currency=amountCents[=memoryId]]... [--reason <text>] [--db <path>]   (agent save, pending_review)
  drenyra-engram close show <memory-id> [--db <path>]
  drenyra-engram close reopen <ruc> --period YYYYMM --expected-close <id> --reason <text> [--db <path>]   (authenticated human gate)
  drenyra-engram timeline <ruc> --topic <topicKey> [--period <YYYYMM>] [--db <path>]
  drenyra-engram reconstructibility <ruc> --period <YYYYMM> [--company-id <id>] [--organization <id>] [--db <path>] [--objects <dir>]   (v1-readiness G-10 read-only metric)
  drenyra-engram mcp [--db <path>]              MCP stdio server (agents)
  drenyra-engram serve [--addr <host:port>] [--token <secret>] [--db <path>]
  drenyra-engram sync --from <src-db> --to <dst-db> [--actor <name>]
  drenyra-engram verify memory <id> [--db <path>]
  drenyra-engram verify judgment <id> [--db <path>]
  drenyra-engram verify receipt <hash|id> [--db <path>]
  drenyra-engram verify object <sha256> [--db <path>] [--objects <dir>]   (v0.7.0 evidence object)
  drenyra-engram tenant list [--db <path>]                             (operator: ids/counts only, never per-tenant content)
  drenyra-engram tenant consolidate --ruc <11 digits> [--period <YYYYMM>] [--dry-run | --apply] [--db <path>]   (topic-key drift within one RUC; --apply merges via audited supersede)
  drenyra-engram encrypt [--dry-run | --apply] [--db <path>]   (re-encrypt legacy plaintext rows; requires DRENYRA_ENCRYPTION_MASTER_KEY)
    
Flags:
  --db <path>      SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)
  --objects <dir>  WORM evidence object root for object store/get, doctor, verify object and reconstructibility (default <dir-of-db>/objects or $DRENYRA_ENGRAM_OBJECTS)
  --drill-copy <copy.db>  marked drill copy for doctor full diagnostics (G-6; mutually exclusive with --db, requires --snapshot-manifest; never a live database)
  --snapshot-manifest <manifest.json>  the adjacent <copy>.drenyra-drill.json drill marker for --drill-copy
  --company <ruc>  company RUC (exactly 11 digits); companyId is derived from it
  --company-id <id>  company id override for reconstructibility (default: the RUC — the established CLI derivation)
  --organization <id>  organization id override for reconstructibility (default: the fixed CLI organization)
  --period <YYYYMM> fiscal period; omitted scopes only match period-less observations
  --any            match ANY query token (default: match ALL)
  --expected-envelope <hash> envelope hash the reviewer actually saw (approve, REQUIRED)
  --reason <text>            approval justification (approve, REQUIRED)
  --limit <n>                review queue page size (review queue, default 50, max 200)
  --offset <n>               review queue page offset (review queue, default 0)
  --relation <rel>           proposable adjudication relation (judge propose, REQUIRED)
  --resolution <text>        professional human resolution (judge confirm, REQUIRED)
  --expected-hash <hash>     proposed judgment hash the adjudicator actually saw (judge confirm/reject, REQUIRED)
  --predecessor <id>         id of an existing judgment the proposal corrects (judge propose, optional)
  --request-id <id>          idempotency key for judge/reconcile commands (optional; a UUID is generated when absent)
  --method <m>               reconciliation method (reconcile propose, REQUIRED)
  --currency <c>             ISO 4217 currency code (reconcile propose, REQUIRED)
  --left-amount-cents <int>  left endpoint amount in int64 cents (reconcile propose, REQUIRED; never float)
  --right-amount-cents <int> right endpoint amount in int64 cents (reconcile propose, REQUIRED; never float)
  --tolerance-cents <int>    accepted variance band in int64 cents (reconcile propose, optional, default 0)
  --total <spec>   close monetary total code=currency=amountCents[=memoryId] (close create, repeatable; signed int64 cents, never float)
  --expected-close <id> close memory id that closed the period (close reopen, REQUIRED)
  --actor <name>   actor recorded in the audit trail (default cli)
  --target <id>    replacing observation for supersede (REQUIRED)
  --jurisdiction <token> retention jurisdiction token (retention-policy, REQUIRED; ^[A-Z][A-Z0-9-]{1,15}$)
  --legislation <id>  retention regime/family identifier (retention-policy, REQUIRED)
  --authority <owner> retention policy owner/issuer (retention-policy put, REQUIRED)
  --source <decision> retention policy source — who decided, when, on what basis (retention-policy put, REQUIRED)
  --category <cat>   retention category (retention-policy, REQUIRED)
  --min-period <YYYYMM> deployment-declared retention floor (retention-policy put, REQUIRED; NO statutory duration claim)
  --object-period <YYYYMM> the object's fiscal period for eligibility (retention-policy evaluate, REQUIRED)
  --expected-version <n>  current chain-head version the caller reviewed (retention-policy put, default 0)
  --dual-approval-required require a second dual-approval role (retention-policy put)
  --dual-approver-roles <list> comma-separated subset of controller,tax_responsible (retention-policy put)
  --blocking-hold-kinds <list> comma-separated subset of legal,audit,dispute,fiscalization,other (retention-policy put)
  --enabled         enable the policy for resolution (retention-policy put)
  --request-id <id>  idempotency key for retention-policy put (optional; a UUID is generated when absent)
  --expected-hash <hash> canonical lifecycle snapshot hash H1 the caller reviewed (purge request/approve/execute, REQUIRED; 64 lowercase hex)
  --request-id-key <id>  tenant-scoped idempotency key of the approval act (purge approve/reject/cancel/withdraw, optional; a UUID is generated when absent)
  --execution-id <id>  tenant-scoped idempotency key of the execution attempt (purge execute, optional; a UUID is generated when absent)
  --kind <kind>    closed hold-kind token legal|audit|dispute|fiscalization|other (hold place, REQUIRED)
  --owner <subject-id> subject responsible for the hold (hold place, REQUIRED)
  --blocking-kinds <list> comma-separated subset of legal,audit,dispute,fiscalization,other (hold list, optional; absent = nothing blocks)
  --addr <host:port> listen address for serve (default 127.0.0.1:8787)
  --token <token>  bearer token for serve (default $DRENYRA_ENGRAM_TOKEN) or auth login (REQUIRED)
  --roles <list>   comma-separated accounting roles for auth seed-local-dev

Surfaces: mcp runs the Model Context Protocol server over stdio (newline-
delimited JSON-RPC) for agent clients; serve runs the local HTTP API (REST
/v1/* plus the MCP endpoint POST /mcp), bound to 127.0.0.1 by default. Both
speak the same domain services as the CLI — compare verdicts and lifecycle
semantics are identical everywhere.

Lifecycle (v2): memories with fiscal effect are saved pending_review and only
approve (AUTHENTICATED human gate) moves them to approved; reject ends the
review, void annuls without successor, supersede routes readers to --target.
compare reports identity/scope/content deltas and a relation verdict.
Review workspace (v0.9.0): review queue/detail are scope-first reads of the
pending_review queue; review reject (terminal) and review return (NON-terminal
— an agent Save on the returned memory creates a NEW revision that re-enters
pending_review) are the AUTHENTICATED decisions derived from the CLI session.

Monthly close (v0.5): "close create" drafts a monthly close through the
canonical CreateClose service — kind=summary, fiscalEffect=closing, topic
closing/CIERRE-<period>, effectiveAt at month end UTC, the frozen
CloseSnapshot (counts, explicit totals with same-scope source memories,
pending-item digest, canonical summary hash). The memory lands pending_review
behind the human gate; only the AUTHENTICATED controller approval closes the
period, after which period-scoped mutations fail with PERIOD_CLOSED until
"close reopen" (an explicit AUTHENTICATED controller act, the same 0600
session as approve) admits corrections. Reopening never edits the approved
close memory; a later close is a new revision of the same close topic.

Authentication (v0.4): approve requires an authenticated CLI session. "auth
login --token <token>" validates the token against the store and stores the
RAW token only in a user-only (0600) config file
(~/.config/drenyra-engram/session.json, or $DRENYRA_ENGRAM_SESSION); the
store keeps only its SHA-256 hash. "auth seed-local-dev" provisions a
local_dev identity + one expiring session and prints the raw token once
(DRENYRA_ENV=local_dev only; rejected in production). judge confirm/reject
use the SAME authenticated session — the verified principal is the only
adjudicator; propose/withdraw carry agent provenance (cli/cli/agent) and
never authorize.

OIDC access tokens (first Production Identity slice): the serve surface can
additionally authenticate STATELESSLY with Auth0 resource-server access
tokens. DRENYRA_OIDC_ISSUER and DRENYRA_OIDC_AUDIENCE are REQUIRED together
(a partial DRENYRA_OIDC_* set fails startup); DRENYRA_OIDC_JWKS_URL,
DRENYRA_OIDC_CLAIM_TENANT, DRENYRA_OIDC_CLAIM_COMPANY and
DRENYRA_OIDC_CLOCK_SKEW are optional (JWKS defaults to
<issuer>/.well-known/jwks.json, claim names to tenant_id/company_id, skew
to 30s bounded at 5m). Tokens are verified in memory — RS256 ONLY, exact
issuer/audience, bounded exp/nbf/iat, JWKS cache with one refresh on an
unknown kid — and the tenant/company claims are cross-checked against the
ACTIVE DB membership for the token sub; any mismatch fails closed. OIDC
NEVER creates sessions and NEVER persists raw tokens; the CLI remains
session-based (docs/architecture/oidc-access-token-identity.md).

Signing keys (v0.4 Step 3): covered acts are signed with the ACTIVE Ed25519
key. The private seeds live ONLY in the user-owned 0600 keyring
(~/.config/drenyra-engram/signing-keys.json, or $DRENYRA_ENGRAM_SIGNING_KEY);
the store keeps public keys and revocation only. "keys init" ensures an
active key (first use generates it), "keys show" prints the active key id,
public key and lifecycle timestamps (NEVER the seed), and "keys rotate"
activates a new key and revokes the old one in a single DB transaction.
Revocation blocks new signatures; receipts issued before it stay verifiable.

Verification (v0.4 Step 4): "verify" runs the OFFLINE read-only engine over
the local store — canonical payload bytes, envelope integrity, Ed25519
signatures, signing-key validity, tenant/company scope, chain links,
principal provenance, supersession chains and referenced-state availability.
Receipts verify by portable 64-hex hash or local SQLite row id. The report
JSON is emitted for BOTH outcomes; exit 0 only when passed, 1 when any
applicable layer fails, and 2 for malformed/unknown targets or a report that
cannot be built. Verification never asserts accounting correctness.

The engine surface is non-authorizing (contracts/provenance.md): there is no
authorize/allow/execute command. Approve/Reject are the PROFESSIONAL review
of a memory (human gate), never authorization of business actions.

Exit codes: 0 ok, 1 runtime error, 2 usage error.`)
}
