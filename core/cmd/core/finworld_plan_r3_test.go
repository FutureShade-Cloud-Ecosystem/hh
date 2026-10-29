//go:build finworld

package main

import (
	"fmt"
	"sort"
	"testing"
)

// finworldSlabCategoryCode is the catalogue category that carries the slab products.
const finworldSlabCategoryCode = "SLABSTEP"

// finworldReturnFacts is what the return rule has to satisfy, read back off a finished plan.
type finworldReturnFacts struct {
	returnLines           int
	returnSubtotalCents   int64
	returnTotalCents      int64
	salesLines            int
	salesSubtotalCents    int64
	salesTotalCents       int64
	returnsByCategory     map[string]int
	returnValueByCategory map[string]int64
	salesValueByCategory  map[string]int64
	slabReturnsByQuarter  map[string]int
	priceMismatches       int
	mismatchDetail        string
}

// finworldQuarterOf names the calendar quarter a month falls in.
func finworldQuarterOf(ym YearMonth) string {
	return fmt.Sprintf("%d-Q%d", ym.Year, (ym.Month-1)/3+1)
}

// finworldPlanReturnFacts walks a finished plan and collects the return side of the world: the
// returns by category, the money on the sales and return sides, the slab returns per calendar
// quarter, and every unit price that does not match the sale line it references.
func finworldPlanReturnFacts(t *testing.T, plan *FinWorldPlanT) finworldReturnFacts {
	t.Helper()

	categoryBySKU := make(map[string]string, len(plan.Products))
	for _, p := range plan.Products {
		categoryBySKU[p.SKU] = p.CategoryCode
	}

	f := finworldReturnFacts{
		returnsByCategory:     map[string]int{},
		returnValueByCategory: map[string]int64{},
		salesValueByCategory:  map[string]int64{},
		slabReturnsByQuarter:  map[string]int{},
	}

	// The unit price of every sale line, so a return line can be checked against the line it
	// references.
	unitPriceByLineKey := make(map[string]int64)

	for _, m := range plan.Months {
		for _, td := range m.TradingDays {
			for _, ev := range td.Events {
				if ev.Sale == nil {
					continue
				}
				for _, ln := range ev.Sale.Lines {
					unitPriceByLineKey[ln.LineKey] = ln.UnitPriceCents
					f.salesLines++
					f.salesSubtotalCents += ln.LineSubtotalCents
					f.salesTotalCents += ln.LineTotalCents
					f.salesValueByCategory[categoryBySKU[ln.SKU]] += ln.LineSubtotalCents
				}
			}
		}
	}

	for _, m := range plan.Months {
		quarter := finworldQuarterOf(m.YearMonth)
		for _, td := range m.TradingDays {
			for _, ev := range td.Events {
				if ev.Return == nil {
					continue
				}
				for _, ln := range ev.Return.Lines {
					f.returnLines++
					f.returnSubtotalCents += ln.LineSubtotalCents
					f.returnTotalCents += ln.LineTotalCents
					category := categoryBySKU[ln.SKU]
					f.returnsByCategory[category]++
					f.returnValueByCategory[category] += ln.LineSubtotalCents
					if category == finworldSlabCategoryCode {
						f.slabReturnsByQuarter[quarter]++
					}
					salePrice, ok := unitPriceByLineKey[ln.OriginalLineKey]
					if !ok {
						t.Fatalf("the return on sale line %q references a sale line the plan does not carry",
							ln.OriginalLineKey)
					}
					if salePrice != ln.UnitPriceCents {
						f.priceMismatches++
						if f.mismatchDetail == "" {
							f.mismatchDetail = fmt.Sprintf("the return on sale line %s carries %d cents, the sale line carries %d cents",
								ln.OriginalLineKey, ln.UnitPriceCents, salePrice)
						}
					}
				}
			}
		}
	}

	return f
}

// finworldPlanQuarters lists the calendar quarters the plan's months fall in, in order.
func finworldPlanQuarters(plan *FinWorldPlanT) []string {
	seen := map[string]bool{}
	var quarters []string
	for _, m := range plan.Months {
		q := finworldQuarterOf(m.YearMonth)
		if !seen[q] {
			seen[q] = true
			quarters = append(quarters, q)
		}
	}
	sort.Strings(quarters)
	return quarters
}

// TestFinWorldPlan_ReturnsCarrySlabsAndAValueShare asserts the return rule on seed 1: every
// calendar quarter of the world carries at least three slab returns, the returns carry between 2
// and 3 percent of the sales value, and every return line carries the unit price of the sale line
// it references.
func TestFinWorldPlan_ReturnsCarrySlabsAndAValueShare(t *testing.T) {
	anchor := YearMonth{Year: 2026, Month: 9}
	plan, err := FinWorldPlan(1, anchor, finworldTestDealerName)
	if err != nil {
		t.Fatalf("FinWorldPlan(1, %s, %q) failed: %v", anchor, finworldTestDealerName, err)
	}
	f := finworldPlanReturnFacts(t, plan)

	// Rule 3: every quarter carries at least a few slab returns.
	for _, q := range finworldPlanQuarters(plan) {
		if f.slabReturnsByQuarter[q] < 3 {
			t.Fatalf("quarter %s carries %d slab returns, the rule asks for at least 3",
				q, f.slabReturnsByQuarter[q])
		}
	}

	// Rule 2: the returns carry between 2 and 3 percent of the sales value. The comparison is in
	// integer cents, never a float, so no money figure is ever a float.
	if f.salesSubtotalCents <= 0 {
		t.Fatalf("the plan carries no sales value, so the return share cannot be read")
	}
	shareBps := f.returnSubtotalCents * 10000 / f.salesSubtotalCents
	if shareBps < 200 || shareBps > 300 {
		t.Fatalf("the returns carry %d basis points of the sales value (%d of %d cents), the rule asks for between 2 and 3 percent",
			shareBps, f.returnSubtotalCents, f.salesSubtotalCents)
	}

	// Rule 4: every return carries its sale line's unit price.
	if f.priceMismatches != 0 {
		t.Fatalf("%d return lines carry a unit price other than the sale line's: %s",
			f.priceMismatches, f.mismatchDetail)
	}
}

// TestFinWorldPlan_ReturnsFollowEachCategorysSalesValue asserts the pick itself: the returns are
// spread across the catalogue categories in proportion to each category's share of the sales
// value. A single pool picked LIFO concentrates the returns on whatever happened to be sold last,
// which is how the world ended up with no slab return at all.
func TestFinWorldPlan_ReturnsFollowEachCategorysSalesValue(t *testing.T) {
	anchor := YearMonth{Year: 2026, Month: 9}
	plan, err := FinWorldPlan(1, anchor, finworldTestDealerName)
	if err != nil {
		t.Fatalf("FinWorldPlan(1, %s, %q) failed: %v", anchor, finworldTestDealerName, err)
	}
	f := finworldPlanReturnFacts(t, plan)

	if f.returnLines == 0 {
		t.Fatal("the plan carries no return lines")
	}
	if f.salesSubtotalCents <= 0 {
		t.Fatal("the plan carries no sales value")
	}

	// The share of lines stays about 3 percent, as before.
	lineShareBps := int64(f.returnLines) * 10000 / int64(f.salesLines)
	if lineShareBps < 250 || lineShareBps > 350 {
		t.Fatalf("the returns are %d lines against %d sales lines, %d basis points, the rule asks for about 3 percent",
			f.returnLines, f.salesLines, lineShareBps)
	}

	categories := make([]string, 0, len(f.salesValueByCategory))
	for category := range f.salesValueByCategory {
		categories = append(categories, category)
	}
	sort.Strings(categories)

	for _, category := range categories {
		salesValue := f.salesValueByCategory[category]
		if salesValue <= 0 {
			continue
		}
		wantShareBps := salesValue * 10000 / f.salesSubtotalCents
		gotShareBps := int64(f.returnsByCategory[category]) * 10000 / int64(f.returnLines)
		// The pick is proportional to sales value, so the two shares have to sit close together.
		const toleranceBps = 200
		diff := gotShareBps - wantShareBps
		if diff < 0 {
			diff = -diff
		}
		if diff > toleranceBps {
			t.Fatalf("category %s carries %d basis points of the sales value but %d basis points of the returns, the rule asks for them to follow the sales value within %d basis points",
				category, wantShareBps, gotShareBps, toleranceBps)
		}
	}
}
