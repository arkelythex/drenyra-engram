// Fiscal convention: monetary values in the Drenyra ecosystem are int64 cents;
// no float is ever used for money. This test proves the fix for
// review-d630ac6a761a3e19's R4-1 (CRITICAL, resilience): FiscalInventory's
// unbounded scan is now capped at fiscalInventoryMaxScan, with a honest
// Truncated signal instead of silent incompleteness.
package store

import (
	"context"
	"fmt"
	"testing"
)

// TestFiscalInventoryReportsUntruncatedBelowCap: a scan that fits under the
// cap reports Truncated=false and covers every row (the ordinary case —
// nothing changes for it).
func TestFiscalInventoryReportsUntruncatedBelowCap(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 3; i++ {
		input := validInput(fmt.Sprintf("topic/fiscal/inventory-%d", i), "inventory fixture")
		input.Scope = testScope(testRucA)
		if _, err := s.Save(input); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	report, err := s.FiscalInventory(context.Background())
	if err != nil {
		t.Fatalf("FiscalInventory: %v", err)
	}
	if report.Truncated {
		t.Fatal("a scan under the cap must not report Truncated")
	}
	if report.LegacyCount != 3 {
		t.Fatalf("LegacyCount = %d, want 3", report.LegacyCount)
	}
}

// TestFiscalInventoryCapsAndReportsTruncation: a scan that exceeds
// fiscalInventoryMaxScan stops at the cap and honestly reports Truncated,
// rather than either buffering unboundedly or silently under-reporting.
// Temporarily lowers the package-level cap so the boundary is exercised with
// a handful of rows instead of the real 10000 — production never does this.
func TestFiscalInventoryCapsAndReportsTruncation(t *testing.T) {
	original := fiscalInventoryMaxScan
	fiscalInventoryMaxScan = 5
	t.Cleanup(func() { fiscalInventoryMaxScan = original })

	s := newTestStore(t)
	for i := 0; i < fiscalInventoryMaxScan+5; i++ {
		input := validInput(fmt.Sprintf("topic/fiscal/inventory-cap-%d", i), "inventory cap fixture")
		input.Scope = testScope(testRucA)
		if _, err := s.Save(input); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	report, err := s.FiscalInventory(context.Background())
	if err != nil {
		t.Fatalf("FiscalInventory: %v", err)
	}
	if !report.Truncated {
		t.Fatal("a scan exceeding the cap must report Truncated")
	}
	if report.LegacyCount != fiscalInventoryMaxScan {
		t.Fatalf("LegacyCount = %d, want exactly the cap (%d)", report.LegacyCount, fiscalInventoryMaxScan)
	}
}
