// Operability documentation readback contract (AC-Z-1..AC-Z-4, FR-Z.1..FR-Z.3,
// FD-3; design D-6 "Z — Operability documentation closure").
//
// This is a STRUCTURAL READBACK test, not a behavior test: it pins the four
// Z-slice documents so a reviewer can verify the operability closure without
// re-reading every claim against the code:
//
//   - docs/architecture/operability-evidence.md          (new, Z.2)
//   - docs/architecture/evidence-object-v0.7.md          (reconciled, Z.3)
//   - docs/product/initial-market-and-v1-gate.md         (reconciled, Z.4)
//   - docs/due-diligence/2026-08-product-architecture-audit.md (Z row read-only;
//     the bounded PASS flip is the evidence-pass slice E.1, never this test)
//
// The test checks: required headings, executable citations (cited files exist
// and every cited TestXxx is declared in a cited _test.go file), removal of the
// stale drill-deferral phrases from the named blocks (FR-Z.3), and absence of
// numeric RTO/RPO/SLA/SLO recovery targets in the changed artifacts (FD-3).
//
// This surface is observation/denial-only (NFR-XC-4): it reads documents and
// rejects stale claims. It never writes, approves, posts, files, or reopens.
// Money is never involved (IR-1); the only "numbers" allowed are file:line
// citation anchors, which are not recovery targets.
package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// operabilityRepoRoot locates the repository root from this test file's path:
// <root>/internal/store/operability_documentation_test.go -> up two levels.
func operabilityRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root %s has no go.mod: %v", root, err)
	}
	return root
}

func readOperabilityDoc(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// recoveryTargetPatterns rejects numerical recovery objectives: RTO/RPO/SLA/SLO
// bound to a value, explicit duration targets, and percentages/percentiles.
// File:line citation anchors (e.g. "doctor_test.go:67") and non-recovery
// numbers (versions, hashes, question IDs) are allowed; a recovery target is a
// NUMBER EXPRESSING A RECOVERY GOAL. Single-letter unit regexes are deliberately
// avoided: they false-positive on legitimate technical text (e.g. "SHA-256 hex"
// already present in evidence-object-v0.7.md).
var recoveryTargetPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(rto|rpo|sla|slo)\b[^\n]{0,24}\d`),
	regexp.MustCompile(`(?i)\d[^\n]{0,24}\b(rto|rpo|sla|slo)\b`),
	regexp.MustCompile(`(?i)\b\d+(\.\d+)?\s*(hours?|hrs?|minutes?|mins?|seconds?|secs?|days?|weeks?|months?|years?)\b`),
	regexp.MustCompile(`\b\d+(\.\d+)?\s*%`),
}

func assertNoRecoveryTargets(t *testing.T, where, doc string) {
	t.Helper()
	for _, re := range recoveryTargetPatterns {
		if m := re.FindString(doc); m != "" {
			t.Errorf("%s: numeric recovery target pattern %q matched %q", where, re.String(), m)
		}
	}
}

// checkOperabilityCitations asserts every cited Go file exists and every cited
// TestXxx name in the document is declared in at least one cited _test.go file.
func checkOperabilityCitations(t *testing.T, root, doc string) {
	t.Helper()
	pathRE := regexp.MustCompile(`(?:internal|cmd)/[A-Za-z0-9_./-]+\.go`)
	testRE := regexp.MustCompile(`\bTest[A-Za-z0-9_]+\b`)

	paths := map[string]bool{}
	for _, m := range pathRE.FindAllString(doc, -1) {
		paths[m] = true
	}
	if len(paths) == 0 {
		t.Error("operability-evidence.md cites no Go files")
	}
	declared := map[string]bool{}
	for p := range paths {
		full := filepath.Join(root, filepath.FromSlash(p))
		if _, err := os.Stat(full); err != nil {
			t.Errorf("cited file %s does not exist: %v", p, err)
			continue
		}
		if !strings.HasSuffix(p, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, full, nil, 0)
		if err != nil {
			t.Errorf("parse cited %s: %v", p, err)
			continue
		}
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok {
				declared[fn.Name.Name] = true
			}
		}
	}
	for _, m := range testRE.FindAllString(doc, -1) {
		if !declared[m] {
			t.Errorf("cited test %s is not declared in any cited _test.go file", m)
		}
	}
}

func rowContaining(doc, marker string) string {
	for _, line := range strings.Split(doc, "\n") {
		if strings.Contains(line, marker) {
			return line
		}
	}
	return ""
}

// TestOperabilityDocumentationReadback is the Z-slice structural readback
// contract (AC-Z-1..AC-Z-4, FR-Z.1..FR-Z.3, FD-3). RED until the four
// documents satisfy every check below.
func TestOperabilityDocumentationReadback(t *testing.T) {
	root := operabilityRepoRoot(t)

	const (
		evDoc  = "docs/architecture/operability-evidence.md"
		objDoc = "docs/architecture/evidence-object-v0.7.md"
		gate   = "docs/product/initial-market-and-v1-gate.md"
		audit  = "docs/due-diligence/2026-08-product-architecture-audit.md"
	)

	evidence := readOperabilityDoc(t, root, evDoc)

	t.Run("required headings and qualitative objectives", func(t *testing.T) {
		for _, h := range []string{
			"Purpose and evidence boundary",
			"Delivered capabilities and executable evidence",
			"Qualitative recovery objectives",
			"Operational boundaries and non-claims",
			"Evidence maintenance",
		} {
			if !strings.Contains(evidence, "## "+h) {
				t.Errorf("operability-evidence.md missing required section %q", h)
			}
		}
		// FR-Z.2 / AC-Z-2: qualitative statement naming RTO/RPO as UNKNOWN and
		// deployment/business-owned.
		for _, want := range []string{"UNKNOWN", "RTO", "RPO", "deployment/business-owned"} {
			if !strings.Contains(evidence, want) {
				t.Errorf("operability-evidence.md qualitative statement missing %q", want)
			}
		}
	})

	t.Run("executable citations resolve", func(t *testing.T) {
		checkOperabilityCitations(t, root, evidence)
	})

	t.Run("evidence-object stale drill deferral removed", func(t *testing.T) {
		obj := readOperabilityDoc(t, root, objDoc)
		for _, stale := range []string{
			"recovery objectives are not demonstrated",
			"backup/restore drills, encryption-at-rest/TDE",
		} {
			if strings.Contains(obj, stale) {
				t.Errorf("evidence-object-v0.7.md still contains stale phrase %q (FR-Z.3)", stale)
			}
		}
		for _, want := range []string{
			"snapshot/restore",
			"corruption drill",
			"internal/store/drill.go",
			"internal/store/drill_test.go",
			"TestRunRestoreDrillSuccess",
			"TestRunCorruptionDrillFullPath",
			"operability-evidence.md",
			"encryption-at-rest/TDE",
			"cloud",
		} {
			if !strings.Contains(obj, want) {
				t.Errorf("evidence-object-v0.7.md split statement missing %q (NFR-Z.1)", want)
			}
		}
	})

	t.Run("v1 gate G-5 reconciled", func(t *testing.T) {
		gateDoc := readOperabilityDoc(t, root, gate)
		g5 := rowContaining(gateDoc, "| G-5 |")
		if g5 == "" {
			t.Fatal("initial-market-and-v1-gate.md missing G-5 row")
		}
		if strings.Contains(g5, "production backup/restore drills") {
			t.Error("G-5 still defers production backup/restore drills (FR-Z.3)")
		}
		for _, want := range []string{
			"operability-evidence.md",
			"cloud/remote object storage",
			"scheduler executor",
			"OCR/content search",
		} {
			if !strings.Contains(g5, want) {
				t.Errorf("G-5 missing %q", want)
			}
		}
	})

	t.Run("v1 gate G-6 reconciled", func(t *testing.T) {
		gateDoc := readOperabilityDoc(t, root, gate)
		g6 := rowContaining(gateDoc, "| G-6 |")
		if g6 == "" {
			t.Fatal("initial-market-and-v1-gate.md missing G-6 row")
		}
		if strings.Contains(g6, "restore/corruption drills not demonstrated") {
			t.Error("G-6 still claims restore/corruption drills not demonstrated (FR-Z.3)")
		}
		for _, want := range []string{
			"repository-delivered",
			"drill_test.go",
			"PARTIAL",
		} {
			if !strings.Contains(g6, want) {
				t.Errorf("G-6 missing %q", want)
			}
		}
	})

	t.Run("audit register Z row conservative and numeric-free", func(t *testing.T) {
		auditDoc := readOperabilityDoc(t, root, audit)
		zRow := rowContaining(auditDoc, "| Z. Operability |")
		if zRow == "" {
			t.Fatal("audit register missing Z. Operability row")
		}
		assertNoRecoveryTargets(t, "audit register Z row", zRow)
	})

	t.Run("no numeric recovery targets in changed artifacts", func(t *testing.T) {
		for _, rel := range []string{evDoc, objDoc, gate} {
			assertNoRecoveryTargets(t, rel, readOperabilityDoc(t, root, rel))
		}
	})
}
