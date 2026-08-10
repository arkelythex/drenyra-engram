// Package bench — deterministic search benchmark corpus and labeled query set
// (design brief §8). The corpus is SYNTHETIC (never real taxpayer data) and
// reproducible: a seeded PRNG, fixed vocabulary, stable ids.
//
// Scale (documented deviation from §8.1): the brief assumes 25,000 memories
// and 50,000 evidence-object metadata records per company/year. This harness
// generates 5,000 memories per company/year × 5 fiscal years = 25,000 memories
// for ONE company, plus a second company for the cross-tenant leakage probe.
// Evidence-object metadata is represented as object-shaped refs on the memories
// (raw bytes are never indexed — §8.1 exclusion). The latency measurements are
// reported at THIS scale; the §8.3 thresholds are proposed design targets
// pending approval, not release gates.
package bench

import (
	"fmt"
	"math/rand"
	"strings"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// Fixed benchmark identity (fictional).
const (
	OrgID   = "bench_org"
	RucA    = "20100039201"
	RucB    = "20600995804"
	Years   = 5
	PerYear = 5000
)

// Spanish accounting vocabulary — the §8.1 mix: SUNAT identifiers, account
// codes, document numbers, rule terms, and adversarial punctuation cases.
var (
	vocabNouns   = []string{"igv", "retencion", "percepcion", "detraccion", "comprobante", "factura", "boleta", "nota-credito", "pdt", "sire", "libro", "diario", "mayor", "asiento", "cierre", "declaracion", "credito", "debito", "cobranza", "pago", "bancarizacion"}
	vocabVerbs   = []string{"registrado", "diferido", "reconocido", "aplicado", "conciliado", "ajustado", "observado", "pendiente", "revisado", "aprobado"}
	accountCodes = []string{"4011", "4017", "6011", "6012", "6211", "6212", "7011", "7012", "1211", "1212", "1611", "1891"}
	periods      []string
	kindWeights  = []core.MemoryKind{core.KindFact, core.KindEvidence, core.KindDecision, core.KindRule, core.KindException, core.KindControl, core.KindObligation, core.KindSummary}
)

func init() {
	for y := 2022; y < 2022+Years; y++ {
		for m := 1; m <= 12; m++ {
			periods = append(periods, fmt.Sprintf("%04d%02d", y, m))
		}
	}
}

// ScopeFor builds the exact company scope for a RUC and period.
func ScopeFor(ruc, period string) core.Scope {
	return core.Scope{Kind: core.ScopeKindCompany, OrganizationID: OrgID, CompanyID: ruc, RUC: ruc, Period: period}
}

// DocNumber renders a deterministic comprobante number for (ruc, year, i).
func DocNumber(ruc string, year, i int) string {
	return fmt.Sprintf("F%03d-%d-%04d", year%1000, i%9000+1000, year)
}

// GenerateMemory builds ONE deterministic memory from the seed. The topic key
// and the distinctive tokens are derived from (ruc, year, i) so the SAME seed
// always yields the SAME memory — the corpus is reproducible. Monetary fields
// (Materiality) are whole int64 CENTS, never floats (the domain convention).
func GenerateMemory(rng *rand.Rand, ruc string, year, i int) core.AccountingMemory {
	period := fmt.Sprintf("%04d%02d", year, 1+(i%12))
	scope := ScopeFor(ruc, period)
	kind := kindWeights[i%len(kindWeights)]
	account := accountCodes[i%len(accountCodes)]
	doc := DocNumber(ruc, year, i)
	noun := vocabNouns[i%len(vocabNouns)]
	verb := vocabVerbs[(i/7)%len(vocabVerbs)]
	effective := fmt.Sprintf("%04d-%02d-15T00:00:00Z", year, 1+(i%12))

	mem := core.AccountingMemory{
		Identity: core.Identity{ID: fmt.Sprintf("bench-%s-%d-%05d", ruc, year, i), TopicKey: fmt.Sprintf("bench/%s/%04d/%05d", noun, year, i)},
		Title:    fmt.Sprintf("%s %s %s %s", strings.ToUpper(noun), verb, account, doc),
		Kind:     kind,
		Scope:    scope,
		Content: core.Content{
			What:    fmt.Sprintf("%s %s %s %s %s", noun, verb, account, doc, period),
			Why:     fmt.Sprintf("benchmark corpus seed %d", i),
			Where:   "PE",
			Learned: fmt.Sprintf("deterministic fixture; %s in force", noun),
		},
		Status:       core.StatusActive,
		FiscalEffect: core.FiscalEffectNone,
		EffectiveAt:  effective,
		RecordedAt:   effective,
		Revision:     1,
		Source:       core.Source{System: "bench", ActorID: "bench-agent", ActorKind: core.ActorKindAgent},
	}
	// Evidence-object metadata: 1-3 object-shaped refs (raw bytes never indexed).
	obj := fmt.Sprintf("%064x", uint64(i)*uint64(year)+uint64(i))
	mem.EvidenceRefs = []string{doc, obj}
	if i%3 == 0 {
		mem.RuleRefs = []string{fmt.Sprintf("policy/%s/%04d", noun, year)}
	}
	// Materiality in whole cents for gated kinds (realistic corpus; never float).
	if kind == core.KindDecision || kind == core.KindSummary {
		cents := int64((i%5000 + 1) * 100)
		mem.Materiality = &cents
	}
	// Rules carry policy metadata + vigencia.
	if kind == core.KindRule {
		mem.PolicyRule = &core.PolicyRule{Jurisdiction: "PE", Legislation: "NATIONAL-TAX", Authority: "SUNAT", Tags: []string{noun, verb}}
		mem.Validity = &core.Validity{EffectiveAt: fmt.Sprintf("%04d-01-01T00:00:00Z", year), Source: "declared"}
	}
	return mem
}

// GenerateCorpus builds the deterministic corpus: RucA memories (25,000) plus
// a smaller RucB set for the leakage probe. The slice is in insertion order
// (stable); search consumes it via MemorySource.List.
func GenerateCorpus() []core.AccountingMemory {
	rng := rand.New(rand.NewSource(20260810))
	out := make([]core.AccountingMemory, 0, Years*PerYear+500)
	for year := 2022; year < 2022+Years; year++ {
		for i := 0; i < PerYear; i++ {
			out = append(out, GenerateMemory(rng, RucA, year, i))
		}
	}
	// Company B — 500 memories, same generator, distinct scope (leakage probe).
	for year := 2022; year < 2022+Years; year++ {
		for i := 0; i < 100; i++ {
			out = append(out, GenerateMemory(rng, RucB, year, i))
		}
	}
	return out
}
