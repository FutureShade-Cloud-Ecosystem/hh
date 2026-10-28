package main

import (
	"os"
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
		if key == w25TestHelperKey || key == w25OnHandTestHelperKey {
			allowance := w25StockValueAllowance[key]
			if len(group) != allowance.sites {
				t.Errorf("test helper site count changed: found %d, allowance says %d: update the site count if a test fixture legitimately writes inventory value", len(group), allowance.sites)
			} else {
				t.Logf("allowed %d test helper writer site(s): %s", len(group), allowance.reason)
			}
			continue
		}
		if w25StockValueStamps[key] || w25StockOnHandStamps[key] {
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

// TestW25_StockValueCensus_AnUnpairedOnHandWriteFails is the red leg for the
// on-hand half of the census (S5's P3 2): an on-hand writer with no stamped
// move and no allowance entry is a writer the as-of read has not proven
// safe, and the census must catch it. The probe plants such a writer in a
// scratch tree and insists the scan finds it unpaired; on the code before
// the census watched on-hand writes the scan finds nothing, which is the red.
func TestW25_StockValueCensus_AnUnpairedOnHandWriteFails(t *testing.T) {
	coreRoot := t.TempDir()
	probeDir := filepath.Join(coreRoot, "internal", "probe")
	if err := os.MkdirAll(probeDir, 0o755); err != nil {
		t.Fatalf("mkdir probe: %v", err)
	}
	src := `package probe

func (s *Svc) sneakOnHandWrite() {
	_, _ = s.tx.Exec(context.Background(), ` + "`" + `
		UPDATE stock_balances SET on_hand_base = on_hand_base + 1 WHERE product_id = $1` + "`" + `, s.pid)
}
`
	if err := os.WriteFile(filepath.Join(probeDir, "probe.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	result, err := w25RunStockValueCensus(coreRoot)
	if err != nil {
		t.Fatalf("scanning the probe tree: %v", err)
	}
	key := "internal/probe/probe.go|(*Svc).sneakOnHandWrite|SQL:stock_balances:UPDATE:on_hand"
	group, found := result.found[key]
	if !found {
		t.Fatalf("RED: the census did not see the planted unpaired on-hand write at %s", key)
	}
	if w25StockOnHandStamps[key] {
		t.Fatalf("the planted unpaired on-hand write must not be stamped: %s", key)
	}
	if _, allowed := w25StockValueAllowance[key]; allowed {
		t.Fatalf("the planted unpaired on-hand write must not be allowed: %s", key)
	}
	t.Logf("the census caught the planted unpaired on-hand write: %s", w25SiteError(group[0]))
}
