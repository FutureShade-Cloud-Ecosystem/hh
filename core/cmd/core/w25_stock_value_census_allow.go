package main

// w25_stock_value_census_allow.go holds the stamped / allowed inventory-value writers.
//
// The rule: every writer of inventory value either (a) writes a move that carries the exact subledger
// delta the GL booked, or (b) is listed here with an honest reason and an accurate count of sites.
//
// A writer not in either map is a red: the as-of read has not proven it safe. An allowance entry whose
// site count no longer matches the source is also a red: a stale entry would hide a new writer in the
// same bucket.

import (
	"fmt"
	"strings"
)

// w25StockValueAllowanceEntry is one allowance table entry: how many source sites the key holds, and
// why each is allowed to write inventory value without a value-stamped move.
type w25StockValueAllowanceEntry struct {
	sites  int
	reason string
}

// w25StockValueStamps is the set of writers whose moves are value-stamped: the move carries the exact
// subledger delta the GL posted (wac_value_delta_cents, the row's exact change in round(on_hand x
// avg), stamped by every value writer since W2-5 round 5; a VALUE move's own signed value_cents for
// landed cost).
var w25StockValueStamps = map[string]bool{
	"internal/inventory/service.go|(*Service).applyReceipt|call:InsertMove":               true,
	"internal/inventory/service.go|(*Service).applyReceipt|call:UpsertBranchCost":         true,
	"internal/inventory/service.go|(*Service).decrementOneShipLine|call:InsertMove":       true,
	"internal/inventory/service.go|(*Service).decrementOneShipLine|call:UpsertBranchCost": true,
	"internal/inventory/service.go|(*Service).restockForReturn|call:InsertMove":           true,
	"internal/inventory/service.go|(*Service).postCountAdjustment|call:InsertMove":        true,
	"internal/inventory/transfer.go|(*Service).ShipTransferWithETA|call:InsertMove":       true,
	"internal/inventory/transfer.go|(*Service).ReceiveTransfer|call:InsertMove":           true,
	"internal/inventory/transfer.go|(*Service).ReceiveTransfer|call:UpsertBranchCost":     true,
	// Landed cost (W2-5b, 0453): the WAC write is paired with the VALUE move bookValueEvent writes on the
	// same transaction, at the instant the landed cost's own journal carries.
	"internal/inventory/service.go|(*Service).capitalizeLandedSnap|call:UpsertBranchCost": true,
	"internal/inventory/service.go|(*Service).restoreLandedCost|call:UpsertBranchCost":    true,
	"internal/inventory/service.go|(*Service).bookValueEvent|call:InsertMove":             true,
}

// w25StockOnHandStamps is the set of on-hand writers paired with a stamped move: the same function
// writes the value-stamped move and the balance delta in one transaction, so the delta can never
// move a row the movement ledger cannot unwind to the cent (S5's P3 2).
var w25StockOnHandStamps = map[string]bool{
	"internal/inventory/service.go|(*Service).applyReceipt|call:UpsertBalanceDelta":         true,
	"internal/inventory/service.go|(*Service).decrementOneShipLine|call:UpsertBalanceDelta": true,
	"internal/inventory/service.go|(*Service).restockForReturn|call:UpsertBalanceDelta":     true,
	"internal/inventory/service.go|(*Service).postCountAdjustment|call:UpsertBalanceDelta":  true,
	"internal/inventory/transfer.go|(*Service).ShipTransferWithETA|call:UpsertBalanceDelta": true,
	"internal/inventory/transfer.go|(*Service).ReceiveTransfer|call:UpsertBalanceDelta":     true,
}

// w25StockValueAllowance names every writer that is NOT value-stamped and is therefore on the record
// with the reason it is allowed to write inventory value anyway.
var w25StockValueAllowance = map[string]w25StockValueAllowanceEntry{
	"cmd/core/dibbits_seed.sql|(sql)|SQL:product_branch_costs:INSERT": w25StockValueAllowanceEntry{
		sites:  1,
		reason: "seed plants demo per-branch WAC rows before the opening moves are inserted; the opening moves themselves carry the WAC value",
	},
	"cmd/core/dibbits_seed.sql|(sql)|SQL:stock_moves:INSERT": w25StockValueAllowanceEntry{
		sites:  3,
		reason: "seed writes the opening balance RECEIPT moves at the WAC the books open at; value_cents is the books' opening value, not a later movement",
	},
	"db/migrations/0058_landedcost_demo_fixture.sql|(sql)|SQL:product_branch_costs:INSERT": w25StockValueAllowanceEntry{
		sites:  1,
		reason: "demo migration seeds a prior WAC so the landed-cost Apply/Void fixture has inventory value to capitalize over on staging",
	},
	"db/migrations/0065_dpa3_low_margin_demo.sql|(sql)|SQL:product_branch_costs:INSERT": w25StockValueAllowanceEntry{
		sites:  2,
		reason: "demo migration seeds per-branch WAC rows so the low-margin report shows real grade rows on a fresh migrate",
	},
	w25TestHelperKey: w25StockValueAllowanceEntry{
		sites:  w25TestHelperSiteCount,
		reason: "test helpers write inventory rows directly to set up or tear down fixtures; they never run in production and are not part of the as-of read's movement stream",
	},
	w25OnHandTestHelperKey: w25StockValueAllowanceEntry{
		sites:  w25OnHandTestHelperSiteCount,
		reason: "test helpers write stock_balances rows directly to set up or tear down fixtures; they never run in production and are not part of the as-of read's movement stream",
	},
	// On-hand writers with no stamped move, each on the record with its reason (S5's P3 2).
	"cmd/core/dibbits_seed.sql|(sql)|SQL:stock_balances:INSERT:on_hand": w25StockValueAllowanceEntry{
		sites:  1,
		reason: "seed plants the opening on-hand balance; the opening moves carry the WAC value the books open at",
	},
	"db/migrations/0058_landedcost_demo_fixture.sql|(sql)|SQL:stock_balances:INSERT:on_hand": w25StockValueAllowanceEntry{
		sites:  1,
		reason: "demo migration seeds on-hand so the landed-cost Apply/Void fixture has stock to capitalize over on staging",
	},
	"internal/inventory/reservation_repository.go|(*Repository).AdjustReserved|SQL:stock_balances:INSERT:on_hand": w25StockValueAllowanceEntry{
		sites:  1,
		reason: "a positive hold creates the balance row at zero on hand when none exists; it moves no quantity and writes no value, so it has no move of its own",
	},
}

// w25TestHelperKey is the allowance bucket for every inventory-value writer found in *_test.go files.
const w25TestHelperKey = "test_helpers|*|*"

// w25TestHelperSiteCount is the exact number of inventory-value writer sites found in *_test.go files.
// It is a constant so a change in the test tree fails the census until the table is updated.
// The round 7 verb-kinding leg's own probe fixtures (two UPDATE and two DELETE writers) are scanned
// like any other test helper site, so the count is 99: the round 5 tree's 95 plus those 4.
// The round 10 leg's planted const writer probe (S5's P3-5) is one more site: the count is 100.
const w25TestHelperSiteCount = 100

// w25OnHandTestHelperKey is the allowance bucket for every on-hand writer found in *_test.go files.
const w25OnHandTestHelperKey = "test_helpers_on_hand|*|*"

// w25OnHandTestHelperSiteCount is the exact number of on-hand writer sites found in *_test.go files.
const w25OnHandTestHelperSiteCount = 74

// w25CensusResult is the census answer: every site found, grouped by key, and every allowance key that
// no live site matches.
type w25CensusResult struct {
	found map[string][]w25InventoryWriterSite
	stale []string
}

// w25RunStockValueCensus scans the tree and returns what it found, without judgement.
func w25RunStockValueCensus(coreRoot string) (w25CensusResult, error) {
	var result w25CensusResult
	sites, err := w25FindInventoryWriters(coreRoot)
	if err != nil {
		return result, err
	}
	result.found = map[string][]w25InventoryWriterSite{}
	for _, s := range sites {
		key := s.key()
		if w25IsTestHelper(s) {
			key = w25TestHelperKey
			if w25IsOnHandKind(s.kind) {
				key = w25OnHandTestHelperKey
			}
		}
		result.found[key] = append(result.found[key], s)
	}
	for key := range w25StockValueAllowance {
		if _, live := result.found[key]; !live {
			result.stale = append(result.stale, key)
		}
	}
	return result, nil
}

// w25IsTestHelper reports whether a site lives in a Go test file (the aggregated test-helper bucket).
func w25IsTestHelper(s w25InventoryWriterSite) bool {
	return strings.HasSuffix(s.file, "_test.go")
}

// w25IsOnHandKind reports whether a site's kind is an on-hand write (a stock_balances statement or
// an UpsertBalanceDelta call) rather than an inventory-value write.
func w25IsOnHandKind(kind string) bool {
	return strings.HasPrefix(kind, "SQL:stock_balances:") || kind == "call:UpsertBalanceDelta"
}

// w25SiteError formats a census failure line for a site.
func w25SiteError(s w25InventoryWriterSite) string {
	return fmt.Sprintf("%s:%d %s %s", s.file, s.line, s.fn, s.kind)
}
