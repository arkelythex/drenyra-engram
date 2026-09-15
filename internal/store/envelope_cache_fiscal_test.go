// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test proves the fail-closed defect
// fix for refreshEnvelopeCache's fiscal-links read (see its doc comment): a
// transient read failure on fiscal_binding_links must abort the envelope
// cache refresh, never silently persist a wrong hash computed as if the
// subject had no fiscal links.
package store

import (
	"context"
	"testing"
)

// TestRefreshEnvelopeCacheAbortsOnFiscalLinkReadFailure: with
// fiscal_binding_links PRESENT (so fiscalLinksTableExists reports true —
// this is a v18+ schema, not the pre-v18 tolerated case) but its read
// genuinely failing, refreshEnvelopeCache must return an error and leave the
// persisted envelope_hash column exactly as it was, rather than computing and
// persisting a hash as if the subject carried no fiscal links. A real
// transient failure (SQLITE_BUSY, I/O) is not reliably reproducible
// in-process; replacing the table with one whose columns don't match the
// query produces the SAME observable symptom — an existing table whose read
// fails — without needing a mock Queryer.
func TestRefreshEnvelopeCacheAbortsOnFiscalLinkReadFailure(t *testing.T) {
	s := newTestStore(t)
	result, err := s.Save(validInput("tax.igv.rate", "first version"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := result.Memory.Identity.ID

	var before string
	if err := s.db.QueryRow(`SELECT envelope_hash FROM observations WHERE id = ?`, id).Scan(&before); err != nil {
		t.Fatalf("read before hash: %v", err)
	}
	if before == "" {
		t.Fatal("fixture: envelope_hash must already be populated by Save")
	}

	if _, err := s.db.Exec(`DROP TABLE fiscal_binding_links`); err != nil {
		t.Fatalf("drop fiscal_binding_links: %v", err)
	}
	// Re-create the table WITHOUT the columns fiscalBindingLinkContributionsTx
	// selects: fiscalLinksTableExists's sqlite_master lookup still reports it
	// present (this is deliberately NOT the pre-v18 "table missing" case), but
	// the actual SELECT fails — a stand-in for corruption/schema drift on an
	// otherwise-existing table.
	if _, err := s.db.Exec(`CREATE TABLE fiscal_binding_links(id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("recreate fiscal_binding_links: %v", err)
	}

	if err := s.refreshEnvelopeCache(context.Background(), s.db, id); err == nil {
		t.Fatal("refreshEnvelopeCache must fail when an existing fiscal_binding_links read fails, not silently succeed")
	}

	var after string
	if err := s.db.QueryRow(`SELECT envelope_hash FROM observations WHERE id = ?`, id).Scan(&after); err != nil {
		t.Fatalf("read after hash: %v", err)
	}
	if after != before {
		t.Fatalf("envelope_hash changed despite the aborted refresh: before=%q after=%q — a transient read failure must never mask itself as a successful, wrongly-computed cache write", before, after)
	}
}

// TestRefreshEnvelopeCacheSucceedsForSubjectWithNoFiscalLinks: the fix must
// not regress the ordinary case — a legacy subject with genuinely zero
// fiscal_binding_links rows still refreshes successfully (the query returns
// an empty result, not an error).
func TestRefreshEnvelopeCacheSucceedsForSubjectWithNoFiscalLinks(t *testing.T) {
	s := newTestStore(t)
	result, err := s.Save(validInput("tax.igv.rate", "first version"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := result.Memory.Identity.ID

	if err := s.refreshEnvelopeCache(context.Background(), s.db, id); err != nil {
		t.Fatalf("refreshEnvelopeCache must succeed for a legacy subject with no fiscal links: %v", err)
	}
}

// TestRefreshEnvelopeCacheToleratesMissingFiscalLinksTable: a pre-v18 schema
// (the table does not exist at all, exactly TestMigrationV18IsAdditiveAndFailsClosed's
// scenario) is NOT a transient failure and must keep degrading to "no links",
// byte-identical to the pre-fix behavior — only a genuine failure against an
// EXISTING table now aborts the refresh.
func TestRefreshEnvelopeCacheToleratesMissingFiscalLinksTable(t *testing.T) {
	s := newTestStore(t)
	result, err := s.Save(validInput("tax.igv.rate", "first version"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	id := result.Memory.Identity.ID

	if _, err := s.db.Exec(`DROP TABLE fiscal_binding_links`); err != nil {
		t.Fatalf("drop fiscal_binding_links: %v", err)
	}

	if err := s.refreshEnvelopeCache(context.Background(), s.db, id); err != nil {
		t.Fatalf("refreshEnvelopeCache must tolerate a missing (pre-v18) fiscal_binding_links table: %v", err)
	}
}
