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
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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
	case "auth":
		return cmdAuth(args[1:])
	case "keys":
		return cmdKeys(args[1:])
	case "judge":
		return cmdJudge(args[1:])
	case "reject":
		return cmdReject(args[1:])
	case "void":
		return cmdVoid(args[1:])
	case "link-evidence":
		return cmdLinkEvidence(args[1:])
	case "period-summary":
		return cmdPeriodSummary(args[1:])
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
// keyring is created lazily on the FIRST covered mutation.
func openStore(path string) (*store.SQLiteStore, error) {
	st, err := store.Open(path)
	if err != nil {
		return nil, err
	}
	st.SetReceiptSigner(receipts.NewSigner(st, receipts.DefaultKeyringPath()))
	return st, nil
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

	st, err := openStore(*dbPath)
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
	mcp := server.NewMCPServer(api)
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
	httpServer := server.NewHTTPServer(api, *token)
	fmt.Fprintf(os.Stderr, "drenyra-engram: serving on http://%s (db %s)%s\n", *addr, *dbPath, tokenSuffix(*token))
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
	var refs multiFlag
	fs.Var(&refs, "ref", "evidence reference (repeatable)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: drenyra-engram link-evidence <id> --ref <ref> [--ref <ref>...] [--actor <name>] [--db <path>]")
	}
	if err := fs.Parse(reorderFlags(args, map[string]bool{"--db": true, "--actor": true, "--ref": true})); err != nil {
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

	st, err := openStore(*dbPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = st.Close() }()

	api := server.New(st, "cli")
	out, err := api.LinkEvidence(rest[0], refs, *actor)
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

func printUsage(w *os.File) {
	fmt.Fprintln(w, `drenyra-engram — institutional accounting memory engine (v0.2 Go foundation)

Usage:
  drenyra-engram save <json-file> [--db <path>]
  drenyra-engram record <json-file> [--db <path>]
  drenyra-engram search <query> --company <ruc> [--period <YYYYMM>] [--any] [--db <path>]
  drenyra-engram context <ruc> [--period <YYYYMM>] [--db <path>]
  drenyra-engram doctor [--db <path>]
  drenyra-engram compare <idA> <idB> [--db <path>]
  drenyra-engram approve <id> --expected-envelope <hash> --reason <text> [--db <path>]   (authenticated human gate)
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
  drenyra-engram reject <id> [--actor <name>] [--db <path>]    (human gate)
  drenyra-engram void <id> [--actor <name>] [--db <path>]
  drenyra-engram supersede <id> --target <targetId> [--actor <name>] [--db <path>]
  drenyra-engram link-evidence <id> --ref <ref> [--ref <ref>...] [--db <path>]
  drenyra-engram period-summary <ruc> [--period <YYYYMM>] [--db <path>]
  drenyra-engram timeline <ruc> --topic <topicKey> [--period <YYYYMM>] [--db <path>]
  drenyra-engram mcp [--db <path>]              MCP stdio server (agents)
  drenyra-engram serve [--addr <host:port>] [--token <secret>] [--db <path>]
  drenyra-engram sync --from <src-db> --to <dst-db> [--actor <name>]

Flags:
  --db <path>      SQLite database path (default ./engram.db or $DRENYRA_ENGRAM_DB)
  --company <ruc>  company RUC (exactly 11 digits); companyId is derived from it
  --period <YYYYMM> fiscal period; omitted scopes only match period-less observations
  --any            match ANY query token (default: match ALL)
  --expected-envelope <hash> envelope hash the reviewer actually saw (approve, REQUIRED)
  --reason <text>            approval justification (approve, REQUIRED)
  --relation <rel>           proposable adjudication relation (judge propose, REQUIRED)
  --resolution <text>        professional human resolution (judge confirm, REQUIRED)
  --expected-hash <hash>     proposed judgment hash the adjudicator actually saw (judge confirm/reject, REQUIRED)
  --predecessor <id>         id of an existing judgment the proposal corrects (judge propose, optional)
  --request-id <id>          idempotency key for judge commands (optional; a UUID is generated when absent)
  --actor <name>   actor recorded in the audit trail (default cli)
  --target <id>    replacing observation for supersede (REQUIRED)
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

Signing keys (v0.4 Step 3): covered acts are signed with the ACTIVE Ed25519
key. The private seeds live ONLY in the user-owned 0600 keyring
(~/.config/drenyra-engram/signing-keys.json, or $DRENYRA_ENGRAM_SIGNING_KEY);
the store keeps public keys and revocation only. "keys init" ensures an
active key (first use generates it), "keys show" prints the active key id,
public key and lifecycle timestamps (NEVER the seed), and "keys rotate"
activates a new key and revokes the old one in a single DB transaction.
Revocation blocks new signatures; receipts issued before it stay verifiable.

The engine surface is non-authorizing (contracts/provenance.md): there is no
authorize/allow/execute command. Approve/Reject are the PROFESSIONAL review
of a memory (human gate), never authorization of business actions.

Exit codes: 0 ok, 1 runtime error, 2 usage error.`)
}
