// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. These tests drive the HTTP period-comparison
// surface (GET /accounting/periods/compare, v0.5.0 design §4/§6): the two exact
// company scopes derive from one ?ruc= (+ optional ?organizationId=) and the
// from/to periods; strict validation maps to 400 and the deterministic
// core.PeriodComparison JSON is a pure read. No monetary fields cross the wire.

package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/arkelythex/drenyra-engram/internal/core"
)

// httpCompareScope is the exact company scope the HTTP surface derives for the
// comparison route (companyId := ruc).
func httpCompareScope(period string) core.Scope {
	return core.Scope{
		Kind:           core.ScopeKindCompany,
		OrganizationID: "cmp_org",
		CompanyID:      "20601234567",
		RUC:            "20601234567",
		Period:         period,
	}
}

// seedHTTPCompareScenario seeds July (two chains) and August (one changed, one
// new) through the shared API so the HTTP read sees deterministic deltas.
func seedHTTPCompareScenario(t *testing.T, api *API) {
	t.Helper()
	saveInScope(t, api, httpCompareScope("202607"), "fact/igv-tasa", "fact", "Tasa IGV", "tasa vigente 18%", "2026-07-05T00:00:00Z")
	saveInScope(t, api, httpCompareScope("202607"), "obligation/igv-621", "obligation", "Obligacion PDT 621", "declarar IGV julio", "2026-07-20T00:00:00Z")
	saveInScope(t, api, httpCompareScope("202608"), "fact/igv-tasa", "fact", "Tasa IGV", "tasa vigente 18.5%", "2026-08-05T00:00:00Z")
	saveInScope(t, api, httpCompareScope("202608"), "account/4011/ventas-agosto", "fact", "Ventas agosto", "ventas de agosto", "2026-08-10T00:00:00Z")
}

func TestHTTPPeriodCompare(t *testing.T) {
	ts, api := newTestHTTPServer(t, "")
	seedHTTPCompareScenario(t, api)

	status, raw := httpJSON(t, http.MethodGet,
		ts.URL+"/accounting/periods/compare?ruc=20601234567&organizationId=cmp_org&from=202607&to=202608",
		"", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", status, raw)
	}
	var got core.PeriodComparison
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("decode comparison: %v", err)
	}
	if got.From != "202607" || got.To != "202608" {
		t.Fatalf("periods = %q/%q, want 202607/202608", got.From, got.To)
	}
	if got.Counts.FromTotal != 2 || got.Counts.ToTotal != 2 || got.Counts.Delta != 0 {
		t.Fatalf("counts = %+v, want fromTotal 2, toTotal 2, delta 0", got.Counts)
	}
	if len(got.Chains.New) != 1 || got.Chains.New[0].TopicKey != "account/4011/ventas-agosto" {
		t.Fatalf("new = %+v, want exactly account/4011/ventas-agosto", got.Chains.New)
	}
	if len(got.Chains.Changed) != 1 || got.Chains.Changed[0].TopicKey != "fact/igv-tasa" {
		t.Fatalf("changed = %+v, want exactly fact/igv-tasa", got.Chains.Changed)
	}
	if len(got.Chains.Removed) != 1 || got.Chains.Removed[0].TopicKey != "obligation/igv-621" {
		t.Fatalf("removed = %+v, want exactly obligation/igv-621", got.Chains.Removed)
	}
	if got.Chains.UnchangedCount != 0 {
		t.Fatalf("unchangedCount = %d, want 0", got.Chains.UnchangedCount)
	}
	if !strings.Contains(got.Narrative, "Comparacion 202607 \u2192 202608") {
		t.Fatalf("narrative = %q, want the delta summary", got.Narrative)
	}
}

func TestHTTPPeriodCompareInvalidPeriod(t *testing.T) {
	ts, _ := newTestHTTPServer(t, "")
	for _, query := range []string{
		"ruc=20601234567&organizationId=cmp_org&from=20261&to=202608",
		"ruc=20601234567&organizationId=cmp_org&from=202607&to=202613",
		"ruc=20601234567&organizationId=cmp_org&from=202607&to=202607",
		"ruc=20601234567&organizationId=cmp_org&to=202608",
		"ruc=20601234567&organizationId=cmp_org&from=202607",
	} {
		status, raw := httpJSON(t, http.MethodGet, ts.URL+"/accounting/periods/compare?"+query, "", nil)
		if status != http.StatusBadRequest {
			t.Fatalf("query %q: status = %d, want 400; body %s", query, status, raw)
		}
		if !strings.Contains(raw, "INVALID_PERIOD") {
			t.Fatalf("query %q: body %q must carry INVALID_PERIOD", query, raw)
		}
	}
}

func TestHTTPPeriodCompareInvalidRUC(t *testing.T) {
	ts, _ := newTestHTTPServer(t, "")
	status, raw := httpJSON(t, http.MethodGet,
		ts.URL+"/accounting/periods/compare?ruc=123&organizationId=cmp_org&from=202607&to=202608", "", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", status, raw)
	}
	if !strings.Contains(raw, "INVALID_RUC") {
		t.Fatalf("body %q must carry INVALID_RUC", raw)
	}
}
