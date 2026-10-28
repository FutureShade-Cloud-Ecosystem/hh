package main

import (
	"path/filepath"
	"sort"
	"testing"
)

// TestW25_StockValueCensus_InventoryWritersAreStampedOrAllowed is the stock value census. It fails
// on any new inventory-value writer that is neither value-stamped nor in the allowance table, and on
// any allowance entry whose site count has become stale.
func TestW25_StockValueCensus_InventoryWritersAreStampedOrAllowed(t *testing.T) {
	coreRoot := filepath.Clean(filepath.Join("..", ".."))
	result, err := w25RunStockValueCensus(coreRoot)
	if err != nil {
		t.Fatalf("scanning %s: %v", coreRoot, err)
	}

	for key, group := range result.found {
		if key == w25TestHelperKey {
			allowance := w25StockValueAllowance[key]
			if len(group) != allowance.sites {
				t.Errorf("test helper site count changed: found %d, allowance says %d: update w25TestHelperSiteCount if a test fixture legitimately writes inventory value", len(group), allowance.sites)
			} else {
				t.Logf("allowed %d test helper writer site(s): %s", len(group), allowance.reason)
			}
			continue
		}
		if w25StockValueStamps[key] {
			for _, s := range group {
				t.Logf("stamped %s", w25SiteError(s))
			}
			continue
		}
		allowance, allowed := w25StockValueAllowance[key]
		if !allowed {
			for _, s := range group {
				t.Errorf("new inventory-value writer not stamped or allowed: %s", w25SiteError(s))
			}
			continue
		}
		if len(group) != allowance.sites {
			t.Errorf("inventory-value writer count changed at %s: found %d, allowance says %d (%s)", key, len(group), allowance.sites, allowance.reason)
			continue
		}
		for _, s := range group {
			t.Logf("allowed %s: %s", w25SiteError(s), allowance.reason)
		}
	}

	// Any allowance entry with no live site is stale and would hide a new writer in that bucket.
	sort.Strings(result.stale)
	for _, key := range result.stale {
		t.Errorf("stale allowance entry %s: no such writer any more (the reason was: %s)", key, w25StockValueAllowance[key].reason)
	}
}
