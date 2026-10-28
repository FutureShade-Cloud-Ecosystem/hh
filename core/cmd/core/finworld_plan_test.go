//go:build finworld

package main

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestFinWorldPlan_Determinism(t *testing.T) {
	anchor := YearMonth{Year: 2026, Month: 9}

	plan1a, err := FinWorldPlan(1, anchor, finworldTestDealerName)
	if err != nil {
		t.Fatalf("FinWorldPlan(1, %s) first call failed: %v", anchor, err)
	}
	plan1b, err := FinWorldPlan(1, anchor, finworldTestDealerName)
	if err != nil {
		t.Fatalf("FinWorldPlan(1, %s) second call failed: %v", anchor, err)
	}

	json1a, err := json.Marshal(plan1a)
	if err != nil {
		t.Fatalf("marshal plan1a: %v", err)
	}
	json1b, err := json.Marshal(plan1b)
	if err != nil {
		t.Fatalf("marshal plan1b: %v", err)
	}
	if !bytes.Equal(json1a, json1b) {
		t.Fatalf("same (seed, anchor) produced non-identical JSON (%d bytes vs %d bytes)", len(json1a), len(json1b))
	}

	plan2, err := FinWorldPlan(2, anchor, finworldTestDealerName)
	if err != nil {
		t.Fatalf("FinWorldPlan(2, %s) failed: %v", anchor, err)
	}
	json2, err := json.Marshal(plan2)
	if err != nil {
		t.Fatalf("marshal plan2: %v", err)
	}
	if bytes.Equal(json1a, json2) {
		t.Fatalf("different seeds (1 vs 2) produced identical JSON")
	}
}

func TestFinWorldPlan_SeasonalityAndBranchMix(t *testing.T) {
	anchor := YearMonth{Year: 2026, Month: 9}
	plan, err := FinWorldPlan(1, anchor, finworldTestDealerName)
	if err != nil {
		t.Fatalf("FinWorldPlan(1, %s) failed: %v", anchor, err)
	}

	if len(plan.Months) != 12 {
		t.Fatalf("expected 12 months in plan, got %d", len(plan.Months))
	}
	if plan.Months[11].YearMonth != anchor {
		t.Fatalf("expected last month to be anchor %s, got %s", anchor, plan.Months[11].YearMonth)
	}
	if len(plan.Products) != 30 {
		t.Fatalf("expected 30 seeded DBS- products, got %d", len(plan.Products))
	}

	yearEndCount := 0
	for i, m := range plan.Months {
		if len(m.TradingDays) == 0 {
			t.Fatalf("month %s has no trading days", m.YearMonth)
		}
		for _, td := range m.TradingDays {
			parsed, err := time.Parse("2006-01-02", td.Date)
			if err != nil {
				t.Fatalf("invalid trading day date %q: %v", td.Date, err)
			}
			if parsed.Weekday() == time.Sunday {
				t.Fatalf("trading day %s in month %s falls on a Sunday", td.Date, m.YearMonth)
			}
		}

		if m.YearMonth.Month == 12 {
			yearEndCount++
			if !m.IsFiscalYearEnd {
				t.Fatalf("month %s (December) must have IsFiscalYearEnd=true", m.YearMonth)
			}
			if m.Summary.YearCloses != 1 {
				t.Fatalf("fiscal year-end month %s expected 1 YearClose, got %d", m.YearMonth, m.Summary.YearCloses)
			}
		} else {
			if m.IsFiscalYearEnd {
				t.Fatalf("non-December month %s has IsFiscalYearEnd=true", m.YearMonth)
			}
			if m.Summary.YearCloses != 0 {
				t.Fatalf("non-December month %s expected 0 YearCloses, got %d", m.YearMonth, m.Summary.YearCloses)
			}
		}

		if i < 11 {
			if !m.IsClosed || m.Summary.PeriodCloses != 1 {
				t.Fatalf("month %d (%s) before anchor must be closed with 1 PeriodClose (IsClosed=%v, PeriodCloses=%d)",
					i, m.YearMonth, m.IsClosed, m.Summary.PeriodCloses)
			}
		} else {
			if m.IsClosed || m.Summary.PeriodCloses != 0 {
				t.Fatalf("anchor month %s must stay open with 0 PeriodCloses (IsClosed=%v, PeriodCloses=%d)",
					m.YearMonth, m.IsClosed, m.Summary.PeriodCloses)
			}
		}

		// Check seasonality factor and monthly volume scaling within +/- 10% tolerance.
		trt := m.Summary.ByBranch[FinWorldBranchCodeTRT]
		kgn := m.Summary.ByBranch[FinWorldBranchCodeKGN]

		expectFactor := float64(m.SeasonFactorBps) / 10000.0
		assertWithinTol := func(label string, got int, expected float64, tol float64) {
			t.Helper()
			diff := math.Abs(float64(got) - expected)
			maxDiff := math.Max(1.0, expected*tol)
			if diff > maxDiff {
				t.Fatalf("month %s (%s) %s: got %d, expected %.1f (tol %.0f%%)",
					m.YearMonth, m.SeasonBand, label, got, expected, tol*100)
			}
		}

		// Trenton seasonal targets (peak: 600 counter, 260 account, 110 delivered, 40 collect_later/COD, 8 credit memos, 40 bills, 40 receipts, 6 transfers).
		assertWithinTol("TRT counter sales", trt.CounterSales, 600.0*expectFactor, 0.08)
		assertWithinTol("TRT account sales", trt.AccountSales, 260.0*expectFactor, 0.08)
		assertWithinTol("TRT delivered sales", trt.DeliveredSales, 110.0*expectFactor, 0.08)
		assertWithinTol("TRT collect-later/COD sales", trt.CollectLaterOrCODSales, 40.0*expectFactor, 0.10)
		assertWithinTol("TRT credit memos", trt.CreditMemos, 8.0*expectFactor, 0.25)
		assertWithinTol("TRT purchase receipts", trt.PurchaseReceipts, 40.0*expectFactor, 0.10)
		assertWithinTol("TRT vendor bills", trt.VendorBills, 40.0*expectFactor, 0.10)

		// Kingston is 0.5 of Trenton within tolerance.
		assertWithinTol("KGN counter sales vs 0.5 TRT", kgn.CounterSales, float64(trt.CounterSales)*0.5, 0.08)
		assertWithinTol("KGN account sales vs 0.5 TRT", kgn.AccountSales, float64(trt.AccountSales)*0.5, 0.08)
		assertWithinTol("KGN delivered sales vs 0.5 TRT", kgn.DeliveredSales, float64(trt.DeliveredSales)*0.5, 0.10)
		assertWithinTol("KGN collect-later/COD sales vs 0.5 TRT", kgn.CollectLaterOrCODSales, float64(trt.CollectLaterOrCODSales)*0.5, 0.15)
		assertWithinTol("KGN purchase receipts vs 0.5 TRT", kgn.PurchaseReceipts, float64(trt.PurchaseReceipts)*0.5, 0.15)
		assertWithinTol("KGN vendor bills vs 0.5 TRT", kgn.VendorBills, float64(trt.VendorBills)*0.5, 0.15)

		// Daily tills (2 per branch per trading day), daily lodgement (1 per branch per trading day), daily card settlement (1 per branch per trading day).
		expectedTillSessionsPerBranch := len(m.TradingDays) * 2
		if trt.TillOpens != expectedTillSessionsPerBranch || trt.TillCounts != expectedTillSessionsPerBranch || trt.TillCompletes != expectedTillSessionsPerBranch {
			t.Fatalf("month %s TRT till counts mismatch: opens=%d counts=%d completes=%d want=%d",
				m.YearMonth, trt.TillOpens, trt.TillCounts, trt.TillCompletes, expectedTillSessionsPerBranch)
		}
		if kgn.TillOpens != expectedTillSessionsPerBranch || kgn.TillCounts != expectedTillSessionsPerBranch || kgn.TillCompletes != expectedTillSessionsPerBranch {
			t.Fatalf("month %s KGN till counts mismatch: opens=%d counts=%d completes=%d want=%d",
				m.YearMonth, kgn.TillOpens, kgn.TillCounts, kgn.TillCompletes, expectedTillSessionsPerBranch)
		}
		if trt.Lodgements != len(m.TradingDays) || kgn.Lodgements != len(m.TradingDays) {
			t.Fatalf("month %s daily lodgements mismatch: TRT=%d KGN=%d tradingDays=%d",
				m.YearMonth, trt.Lodgements, kgn.Lodgements, len(m.TradingDays))
		}
		if trt.CardSettlements != len(m.TradingDays) || kgn.CardSettlements != len(m.TradingDays) {
			t.Fatalf("month %s daily card settlements mismatch: TRT=%d KGN=%d tradingDays=%d",
				m.YearMonth, trt.CardSettlements, kgn.CardSettlements, len(m.TradingDays))
		}
		if m.Summary.BankStatements != 1 {
			t.Fatalf("month %s expected 1 bank statement import, got %d", m.YearMonth, m.Summary.BankStatements)
		}
	}

	if yearEndCount != 1 {
		t.Fatalf("expected fiscal year-end month (December) in window exactly once, got %d", yearEndCount)
	}

	// Returns: ~3% of sales lines.
	returnLineRatio := float64(plan.Totals.ReturnLines) / float64(plan.Totals.TotalSalesLines)
	if returnLineRatio < 0.025 || returnLineRatio > 0.035 {
		t.Fatalf("return lines ratio = %.4f (%d / %d), expected ~0.03 (between 0.025 and 0.035)",
			returnLineRatio, plan.Totals.ReturnLines, plan.Totals.TotalSalesLines)
	}

	// Pareto split: top 15 contractors ~60% of account sales.
	paretoDocRatio := float64(plan.Totals.Top15ContractorAccountSales) / float64(plan.Totals.TotalAccountInvoiceDocs)
	if paretoDocRatio < 0.55 || paretoDocRatio > 0.65 {
		t.Fatalf("Pareto top-15 contractor account sale doc ratio = %.4f (%d / %d), expected ~0.60 (0.55..0.65)",
			paretoDocRatio, plan.Totals.Top15ContractorAccountSales, plan.Totals.TotalAccountInvoiceDocs)
	}
	paretoCentsRatio := float64(plan.Totals.Top15ContractorAccountCents) / float64(plan.Totals.AccountSalesTotalCents)
	if paretoCentsRatio < 0.52 || paretoCentsRatio > 0.68 {
		t.Fatalf("Pareto top-15 contractor account cents ratio = %.4f (%d / %d), expected ~0.60 (0.52..0.68)",
			paretoCentsRatio, plan.Totals.Top15ContractorAccountCents, plan.Totals.AccountSalesTotalCents)
	}

	// Account receipts cover ~92% of account sales, leaving real 31-90d and >90d open balances.
	receiptCoverage := float64(plan.Totals.ReceiptsTotalCents) / float64(plan.Totals.AccountSalesTotalCents)
	if receiptCoverage < 0.88 || receiptCoverage > 0.96 {
		t.Fatalf("account receipt coverage = %.4f (%d / %d), expected ~0.92 (0.88..0.96)",
			receiptCoverage, plan.Totals.ReceiptsTotalCents, plan.Totals.AccountSalesTotalCents)
	}
	if plan.Totals.OpenAR31To90Count <= 0 || plan.Totals.OpenAR31To90Cents <= 0 {
		t.Fatalf("expected non-zero 31-90 day AR aging balance, got count=%d cents=%d",
			plan.Totals.OpenAR31To90Count, plan.Totals.OpenAR31To90Cents)
	}
	if plan.Totals.OpenAROver90Count <= 0 || plan.Totals.OpenAROver90Cents <= 0 {
		t.Fatalf("expected non-zero >90 day AR aging balance, got count=%d cents=%d",
			plan.Totals.OpenAROver90Count, plan.Totals.OpenAROver90Cents)
	}

	// Restocking below reorder points and positive on-hand stock across all 313 days.
	if plan.Totals.ReorderTriggeredReceipts <= 0 {
		t.Fatalf("expected reorder-triggered purchase receipts > 0, got %d", plan.Totals.ReorderTriggeredReceipts)
	}
	simStock := map[string]map[string]int64{
		FinWorldBranchCodeTRT: make(map[string]int64, len(plan.Products)),
		FinWorldBranchCodeKGN: make(map[string]int64, len(plan.Products)),
	}
	for _, p := range plan.Products {
		simStock[FinWorldBranchCodeTRT][p.SKU] = p.OpeningStockTRT
		simStock[FinWorldBranchCodeKGN][p.SKU] = p.OpeningStockKGN
	}
	for _, m := range plan.Months {
		for _, td := range m.TradingDays {
			for _, ev := range td.Events {
				if ev.Sale != nil {
					for _, ln := range ev.Sale.Lines {
						simStock[ev.Sale.BranchCode][ln.SKU] -= ln.Quantity
						if simStock[ev.Sale.BranchCode][ln.SKU] <= 0 {
							t.Fatalf("day %s sale %s drove stock non-positive at %s for %s: %d",
								td.Date, ev.Sale.SaleKey, ev.Sale.BranchCode, ln.SKU, simStock[ev.Sale.BranchCode][ln.SKU])
						}
					}
				}
				if ev.PurchaseReceipt != nil {
					for _, ln := range ev.PurchaseReceipt.Lines {
						if ev.PurchaseReceipt.TriggeredByReorder && (!ln.TriggeredReorder || ln.StockBeforeBase > ln.ReorderPointBase) {
							t.Fatalf("purchase receipt %s marked TriggeredByReorder but line %s stock_before=%d > reorder_point=%d",
								ev.PurchaseReceipt.PurchaseReceiptKey, ln.SKU, ln.StockBeforeBase, ln.ReorderPointBase)
						}
						simStock[ev.PurchaseReceipt.BranchCode][ln.SKU] += ln.Quantity
					}
				}
				if ev.Return != nil {
					for _, ln := range ev.Return.Lines {
						simStock[ev.Return.BranchCode][ln.SKU] += ln.Quantity
					}
				}
				if ev.StockTransfer != nil {
					for _, ln := range ev.StockTransfer.Lines {
						avail := simStock[ev.StockTransfer.FromBranchCode][ln.SKU]
						if avail < ln.Quantity {
							t.Fatalf("day %s transfer %s overdraws %s %s: avail=%d requested=%d",
								td.Date, ev.StockTransfer.TransferKey, ev.StockTransfer.FromBranchCode, ln.SKU, avail, ln.Quantity)
						}
						simStock[ev.StockTransfer.FromBranchCode][ln.SKU] -= ln.Quantity
						simStock[ev.StockTransfer.ToBranchCode][ln.SKU] += ln.Quantity
					}
				}
			}
		}
	}
	for _, brCode := range []string{FinWorldBranchCodeTRT, FinWorldBranchCodeKGN} {
		for sku, q := range simStock[brCode] {
			if q <= 0 {
				t.Fatalf("ending stock at %s for %s is non-positive: %d", brCode, sku, q)
			}
		}
	}

	// Till over/short variance roughly 1 in 10 sessions.
	overShortRatio := float64(plan.Totals.TillOverShortCount) / float64(plan.Totals.TillCounts)
	if overShortRatio < 0.06 || overShortRatio > 0.14 {
		t.Fatalf("till over/short ratio = %.4f (%d / %d), expected ~0.10 (0.06..0.14)",
			overShortRatio, plan.Totals.TillOverShortCount, plan.Totals.TillCounts)
	}

	t.Logf("Seed=1 Anchor=%s Dealer=%q TotalTradingDays=%d TotalEvents=%d SalesSubtotalCents=%d SalesTaxCents=%d SalesTotalCents=%d",
		anchor, plan.Dealer.Name, plan.Totals.TotalTradingDays, plan.Totals.TotalEvents,
		plan.Totals.SalesSubtotalCents, plan.Totals.SalesTaxCents, plan.Totals.SalesTotalCents)
	for _, m := range plan.Months {
		s := m.Summary
		t.Logf("Month=%s Band=%s Days=%d Counter=%d Account=%d Deliv=%d CollectCOD=%d Returns=%d(lines=%d) CM=%d Rcpts=%d NSF=%d TillOpen=%d PaidInOut=%d Lodg=%d CardSettle=%d PR=%d VBill=%d Xfr=%d MJ=%d BankStmt=%d PClose=%d YClose=%d SalesTotalCents=%d",
			s.YearMonth, s.SeasonBand, s.TradingDayCount, s.CounterSales, s.AccountSales, s.DeliveredSales,
			s.CollectLaterOrCODSales, s.ReturnDocs, s.ReturnLines, s.CreditMemos, s.AccountReceipts, s.NSFBounces,
			s.TillOpens, s.TillPaidInOuts, s.Lodgements, s.CardSettlements, s.PurchaseReceipts, s.VendorBills,
			s.StockTransfers, s.ManualJournals, s.BankStatements, s.PeriodCloses, s.YearCloses, s.SalesTotalCents)
	}
	t.Logf("GrandTotals: Counter=%d Account=%d Deliv=%d CollectCOD=%d SalesDocs=%d SalesLines=%d TaxRelief=%d Returns=%d(lines=%d) CM=%d Rcpts=%d NSF=%d TillOpen=%d PaidInOut=%d OverShort=%d Lodg=%d CardSettle=%d PR=%d(reorder=%d) VBill=%d Xfr=%d MJ=%d BankStmt=%d PClose=%d YClose=%d",
		plan.Totals.CounterSales, plan.Totals.AccountSales, plan.Totals.DeliveredSales, plan.Totals.CollectLaterOrCODSales,
		plan.Totals.TotalSalesDocs, plan.Totals.TotalSalesLines, plan.Totals.TaxReliefSales,
		plan.Totals.ReturnDocs, plan.Totals.ReturnLines, plan.Totals.CreditMemos, plan.Totals.AccountReceipts,
		plan.Totals.NSFBounces, plan.Totals.TillOpens, plan.Totals.TillPaidInOuts, plan.Totals.TillOverShortCount,
		plan.Totals.Lodgements, plan.Totals.CardSettlements, plan.Totals.PurchaseReceipts, plan.Totals.ReorderTriggeredReceipts,
		plan.Totals.VendorBills, plan.Totals.StockTransfers, plan.Totals.ManualJournals, plan.Totals.BankStatements,
		plan.Totals.PeriodCloses, plan.Totals.YearCloses)
	t.Logf("AgingAndPareto: AccountSalesTotalCents=%d Top15Docs=%d/%d Top15Cents=%d ReceiptsTotalCents=%d Open0To30Cents=%d Open31To90=%d(%d cents) OpenOver90=%d(%d cents)",
		plan.Totals.AccountSalesTotalCents, plan.Totals.Top15ContractorAccountSales, plan.Totals.TotalAccountInvoiceDocs,
		plan.Totals.Top15ContractorAccountCents, plan.Totals.ReceiptsTotalCents,
		plan.Totals.OpenARCurrent0To30Cents, plan.Totals.OpenAR31To90Count, plan.Totals.OpenAR31To90Cents,
		plan.Totals.OpenAROver90Count, plan.Totals.OpenAROver90Cents)
}

func TestFinWorldPlan_NoFloatAmounts(t *testing.T) {
	anchor := YearMonth{Year: 2026, Month: 9}
	plan, err := FinWorldPlan(1, anchor, finworldTestDealerName)
	if err != nil {
		t.Fatalf("FinWorldPlan(1, %s) failed: %v", anchor, err)
	}

	var checkNoFloats func(v reflect.Value, path string)
	checkNoFloats = func(v reflect.Value, path string) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Float32, reflect.Float64:
			t.Fatalf("found forbidden float field at %s (kind %s)", path, v.Kind())
		case reflect.Pointer, reflect.Interface:
			if !v.IsNil() {
				checkNoFloats(v.Elem(), path)
			}
		case reflect.Struct:
			rt := v.Type()
			for i := 0; i < v.NumField(); i++ {
				checkNoFloats(v.Field(i), path+"."+rt.Field(i).Name)
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				checkNoFloats(v.Index(i), path)
			}
		case reflect.Map:
			iter := v.MapRange()
			for iter.Next() {
				checkNoFloats(iter.Key(), path+".<key>")
				checkNoFloats(iter.Value(), path+"["+iter.Key().String()+"]")
			}
		}
	}
	checkNoFloats(reflect.ValueOf(plan), "FinWorldPlanT")

	// Also verify per-line tax calculation matches tax.hstOn and relief sales exist.
	stdSales := 0
	reliefSales := 0
	for _, m := range plan.Months {
		for _, td := range m.TradingDays {
			for _, ev := range td.Events {
				if ev.Sale != nil {
					var subSum, taxSum, totalSum int64
					switch ev.Sale.TaxReasonCode {
					case FinWorldTaxReasonStandardON:
						stdSales++
						if ev.Sale.TaxRateBps != FinWorldTaxRateStandardBps {
							t.Fatalf("sale %s standard tax rate = %d, want %d", ev.Sale.SaleKey, ev.Sale.TaxRateBps, FinWorldTaxRateStandardBps)
						}
					case FinWorldTaxReasonReliefFN8:
						reliefSales++
						if ev.Sale.TaxRateBps != FinWorldTaxRateReliefBps {
							t.Fatalf("sale %s relief tax rate = %d, want %d", ev.Sale.SaleKey, ev.Sale.TaxRateBps, FinWorldTaxRateReliefBps)
						}
					default:
						t.Fatalf("sale %s unexpected tax reason %q", ev.Sale.SaleKey, ev.Sale.TaxReasonCode)
					}
					for _, ln := range ev.Sale.Lines {
						if ln.LineSubtotalCents != ln.Quantity*ln.UnitPriceCents {
							t.Fatalf("sale %s line %d subtotal %d != %d * %d",
								ev.Sale.SaleKey, ln.LineNo, ln.LineSubtotalCents, ln.Quantity, ln.UnitPriceCents)
						}
						wantTax := finworldHSTOn(ln.LineSubtotalCents, ln.TaxRateBps)
						if ln.TaxCents != wantTax {
							t.Fatalf("sale %s line %d tax %d != hstOn(%d, %d)=%d",
								ev.Sale.SaleKey, ln.LineNo, ln.TaxCents, ln.LineSubtotalCents, ln.TaxRateBps, wantTax)
						}
						if ln.LineTotalCents != ln.LineSubtotalCents+ln.TaxCents {
							t.Fatalf("sale %s line %d total %d != subtotal %d + tax %d",
								ev.Sale.SaleKey, ln.LineNo, ln.LineTotalCents, ln.LineSubtotalCents, ln.TaxCents)
						}
						subSum += ln.LineSubtotalCents
						taxSum += ln.TaxCents
						totalSum += ln.LineTotalCents
					}
					if ev.Sale.SubtotalCents != subSum || ev.Sale.TaxCents != taxSum || ev.Sale.TotalCents != totalSum {
						t.Fatalf("sale %s header totals (%d, %d, %d) != line sums (%d, %d, %d)",
							ev.Sale.SaleKey, ev.Sale.SubtotalCents, ev.Sale.TaxCents, ev.Sale.TotalCents, subSum, taxSum, totalSum)
					}
				}
			}
		}
	}
	if stdSales == 0 || reliefSales == 0 {
		t.Fatalf("expected both standard 13%% and relief 8%% sales, got std=%d relief=%d", stdSales, reliefSales)
	}
}

func TestFinWorldPlan_InventedNamesAndAddresses(t *testing.T) {
	anchor := YearMonth{Year: 2026, Month: 9}
	plan, err := FinWorldPlan(1, anchor, finworldTestDealerName)
	if err != nil {
		t.Fatalf("FinWorldPlan(1, %s) failed: %v", anchor, err)
	}

	if len(plan.Contractors) != 70 {
		t.Fatalf("expected 70 contractors, got %d", len(plan.Contractors))
	}
	if len(plan.Homeowners) != 180 {
		t.Fatalf("expected 180 homeowners, got %d", len(plan.Homeowners))
	}

	allowedTowns := make(map[string]bool, len(finworldAllowedTowns))
	for _, town := range finworldAllowedTowns {
		allowedTowns[town] = true
	}
	allowedStreets := make(map[string]bool, len(finworldStreetNames))
	for _, st := range finworldStreetNames {
		allowedStreets[st] = true
	}
	allowedFirstWords := make(map[string]bool, len(finworldCustomerFirstWords))
	for _, w := range finworldCustomerFirstWords {
		allowedFirstWords[w] = true
	}
	allowedSecondWords := make(map[string]bool, len(finworldCustomerSecondWords))
	for _, w := range finworldCustomerSecondWords {
		allowedSecondWords[w] = true
	}
	allowedHouseholds := make(map[string]bool, len(finworldHouseholdWords))
	for _, w := range finworldHouseholdWords {
		allowedHouseholds[w] = true
	}

	// Real local businesses and chains that must never appear anywhere in the plan.
	forbiddenBusinesses := []string{
		"dibbits",
		"home depot",
		"homedepot",
		"lowe's",
		"lowes",
		"rona",
		"home hardware",
		"timbr-mart",
		"timbrmart",
		"cabal",
		"cruickshank",
		"lafarge",
		"totten",
		"rushnell",
		"kimmett",
		"dewe",
	}

	checkAddressAndTown := func(who, streetAddr, town string) {
		t.Helper()
		if !allowedTowns[town] {
			t.Fatalf("%s has town %q outside allowed Quinte town list", who, town)
		}
		matchedStreet := false
		for st := range allowedStreets {
			if strings.HasSuffix(streetAddr, " "+st) {
				matchedStreet = true
				break
			}
		}
		if !matchedStreet {
			t.Fatalf("%s has street address %q not built from invented street list", who, streetAddr)
		}
	}

	// The dealer's display name is an input the caller supplies, so it is the caller's name verbatim and
	// never a word list product.
	if plan.Dealer.Name != finworldTestDealerName {
		t.Fatalf("dealer name %q, want the caller's name %q", plan.Dealer.Name, finworldTestDealerName)
	}
	if !strings.HasPrefix(plan.Dealer.LegalName, finworldTestDealerName) {
		t.Fatalf("dealer legal name %q is not derived from the caller's dealer name %q",
			plan.Dealer.LegalName, finworldTestDealerName)
	}
	checkAddressAndTown("dealer", plan.Dealer.StreetAddress, plan.Dealer.Town)
	for _, b := range plan.Branches {
		checkAddressAndTown("branch "+b.Code, b.StreetAddress, b.Town)
	}

	topParetoCount := 0
	for _, c := range plan.Contractors {
		if c.CustomerType != "CONTRACTOR" {
			t.Fatalf("contractor %s has customer_type %q", c.Key, c.CustomerType)
		}
		if c.CreditLimitCents <= 0 || c.PaymentTermsDays <= 0 {
			t.Fatalf("contractor %s missing credit limit (%d) or terms (%d)", c.Key, c.CreditLimitCents, c.PaymentTermsDays)
		}
		cParts := strings.Fields(c.Name)
		if len(cParts) < 4 || !allowedFirstWords[cParts[0]] || !allowedSecondWords[cParts[1]] {
			t.Fatalf("contractor %s name %q not built from invented word lists", c.Key, c.Name)
		}
		if c.IsTopPareto {
			topParetoCount++
		}
		checkAddressAndTown("contractor "+c.Key, c.StreetAddress, c.Town)
	}
	if topParetoCount != 15 {
		t.Fatalf("expected 15 top-Pareto contractors, got %d", topParetoCount)
	}

	for _, h := range plan.Homeowners {
		if h.CustomerType != "RETAIL" {
			t.Fatalf("homeowner %s has customer_type %q, want RETAIL", h.Key, h.CustomerType)
		}
		if !strings.HasPrefix(h.Name, "The ") || !strings.HasSuffix(h.Name, " Household") {
			t.Fatalf("homeowner %s name %q does not follow 'The <Word> Household' shape", h.Key, h.Name)
		}
		word := strings.TrimSuffix(strings.TrimPrefix(h.Name, "The "), " Household")
		if !allowedHouseholds[word] {
			t.Fatalf("homeowner %s word %q not in invented household list", h.Key, word)
		}
		checkAddressAndTown("homeowner "+h.Key, h.StreetAddress, h.Town)
	}

	// Walk every string in the entire plan to verify:
	// 1. No forbidden business names (including "dibbits").
	// 2. Every string containing '@' ends with '@example.invalid'.
	// 3. No em dash or en dash anywhere.
	var walkStrings func(v reflect.Value, path string)
	walkStrings = func(v reflect.Value, path string) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.String:
			s := v.String()
			lower := strings.ToLower(s)
			for _, bad := range forbiddenBusinesses {
				if strings.Contains(lower, bad) {
					t.Fatalf("forbidden word %q found at %s: %q", bad, path, s)
				}
			}
			if strings.Contains(s, "@") && !strings.HasSuffix(lower, "@example.invalid") {
				t.Fatalf("string with '@' at %s does not end in @example.invalid: %q", path, s)
			}
			if strings.ContainsRune(s, '\u2014') || strings.ContainsRune(s, '\u2013') {
				t.Fatalf("string at %s contains em or en dash: %q", path, s)
			}
		case reflect.Pointer, reflect.Interface:
			if !v.IsNil() {
				walkStrings(v.Elem(), path)
			}
		case reflect.Struct:
			rt := v.Type()
			for i := 0; i < v.NumField(); i++ {
				walkStrings(v.Field(i), path+"."+rt.Field(i).Name)
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walkStrings(v.Index(i), path)
			}
		case reflect.Map:
			iter := v.MapRange()
			for iter.Next() {
				walkStrings(iter.Key(), path+".<key>")
				walkStrings(iter.Value(), path+"["+iter.Key().String()+"]")
			}
		}
	}
	walkStrings(reflect.ValueOf(plan), "FinWorldPlanT")
}

func TestFinWorldPlan_ReferentialAndTemporalOrder(t *testing.T) {
	anchor := YearMonth{Year: 2026, Month: 9}
	plan, err := FinWorldPlan(1, anchor, finworldTestDealerName)
	if err != nil {
		t.Fatalf("FinWorldPlan(1, %s) failed: %v", anchor, err)
	}

	seenSales := make(map[string]*FinWorldSaleEvent)
	seenInvoices := make(map[string]*FinWorldSaleEvent)
	seenReceipts := make(map[string]*FinWorldAccountReceiptEvent)
	seenPurchaseReceipts := make(map[string]*FinWorldPurchaseReceiptEvent)
	lastGlobalSeq := 0

	for _, m := range plan.Months {
		lastTradingDate := m.TradingDays[len(m.TradingDays)-1].Date
		seenPeriodCloseInMonth := false
		seenYearCloseInMonth := false

		for tdIdx, td := range m.TradingDays {
			isLastDay := tdIdx == len(m.TradingDays)-1
			if td.IsLastOfMonth != isLastDay {
				t.Fatalf("month %s day %s IsLastOfMonth=%v, want %v", m.YearMonth, td.Date, td.IsLastOfMonth, isLastDay)
			}

			for evIdx, ev := range td.Events {
				if ev.GlobalSeq <= lastGlobalSeq {
					t.Fatalf("event %s global_seq %d not strictly increasing after %d", ev.EventKey, ev.GlobalSeq, lastGlobalSeq)
				}
				lastGlobalSeq = ev.GlobalSeq

				if seenPeriodCloseInMonth && ev.Kind != FinWorldEventYearClose {
					t.Fatalf("month %s event %s (%s) occurs after PERIOD_CLOSE", m.YearMonth, ev.EventKey, ev.Kind)
				}
				if seenYearCloseInMonth {
					t.Fatalf("month %s event %s (%s) occurs after YEAR_CLOSE", m.YearMonth, ev.EventKey, ev.Kind)
				}

				switch ev.Kind {
				case FinWorldEventCounterSale, FinWorldEventAccountSale, FinWorldEventDeliveredSale, FinWorldEventCollectLaterOrCODSale:
					if ev.Sale == nil {
						t.Fatalf("event %s (%s) has nil Sale payload", ev.EventKey, ev.Kind)
					}
					seenSales[ev.Sale.SaleKey] = ev.Sale
					if ev.Sale.InvoiceKey != "" {
						seenInvoices[ev.Sale.InvoiceKey] = ev.Sale
					}

				case FinWorldEventReturn:
					if ev.Return == nil {
						t.Fatalf("event %s has nil Return payload", ev.EventKey)
					}
					orig, ok := seenSales[ev.Return.OriginalSaleKey]
					if !ok {
						t.Fatalf("return %s on %s references unknown or future sale %q",
							ev.Return.ReturnKey, ev.Date, ev.Return.OriginalSaleKey)
					}
					if orig.IssueDate > ev.Date {
						t.Fatalf("return %s on %s precedes original sale date %s",
							ev.Return.ReturnKey, ev.Date, orig.IssueDate)
					}

				case FinWorldEventCreditMemo:
					if ev.CreditMemo == nil {
						t.Fatalf("event %s has nil CreditMemo payload", ev.EventKey)
					}
					if _, ok := seenInvoices[ev.CreditMemo.OriginalInvoiceKey]; !ok {
						t.Fatalf("credit memo %s on %s references unknown or future invoice %q",
							ev.CreditMemo.CreditMemoKey, ev.Date, ev.CreditMemo.OriginalInvoiceKey)
					}

				case FinWorldEventAccountReceipt:
					if ev.AccountReceipt == nil {
						t.Fatalf("event %s has nil AccountReceipt payload", ev.EventKey)
					}
					if len(ev.AccountReceipt.Allocations) == 0 {
						t.Fatalf("account receipt %s has no invoice allocations", ev.AccountReceipt.ReceiptKey)
					}
					for _, alloc := range ev.AccountReceipt.Allocations {
						inv, ok := seenInvoices[alloc.InvoiceKey]
						if !ok {
							t.Fatalf("receipt %s on %s references unknown or future invoice %q",
								ev.AccountReceipt.ReceiptKey, ev.Date, alloc.InvoiceKey)
						}
						if inv.IssueDate > ev.Date {
							t.Fatalf("receipt %s on %s precedes invoice %s issue date %s",
								ev.AccountReceipt.ReceiptKey, ev.Date, alloc.InvoiceKey, inv.IssueDate)
						}
					}
					seenReceipts[ev.AccountReceipt.ReceiptKey] = ev.AccountReceipt

				case FinWorldEventNSFBounce:
					if ev.NSFBounce == nil {
						t.Fatalf("event %s has nil NSFBounce payload", ev.EventKey)
					}
					rcpt, ok := seenReceipts[ev.NSFBounce.OriginalReceiptKey]
					if !ok {
						t.Fatalf("NSF bounce %s on %s references unknown or future receipt %q",
							ev.NSFBounce.BounceKey, ev.Date, ev.NSFBounce.OriginalReceiptKey)
					}
					if rcpt.PaymentMethod != "CHEQUE" {
						t.Fatalf("NSF bounce %s references non-cheque receipt %s (%s)",
							ev.NSFBounce.BounceKey, rcpt.ReceiptKey, rcpt.PaymentMethod)
					}

				case FinWorldEventPurchaseReceipt:
					if ev.PurchaseReceipt == nil {
						t.Fatalf("event %s has nil PurchaseReceipt payload", ev.EventKey)
					}
					seenPurchaseReceipts[ev.PurchaseReceipt.PurchaseReceiptKey] = ev.PurchaseReceipt

				case FinWorldEventVendorBill:
					if ev.VendorBill == nil {
						t.Fatalf("event %s has nil VendorBill payload", ev.EventKey)
					}
					if _, ok := seenPurchaseReceipts[ev.VendorBill.PurchaseReceiptKey]; !ok {
						t.Fatalf("vendor bill %s on %s references unknown or future purchase receipt %q",
							ev.VendorBill.VendorBillKey, ev.Date, ev.VendorBill.PurchaseReceiptKey)
					}

				case FinWorldEventPeriodClose:
					if ev.PeriodClose == nil {
						t.Fatalf("event %s has nil PeriodClose payload", ev.EventKey)
					}
					if !isLastDay || td.Date != lastTradingDate {
						t.Fatalf("period close %s on %s is not on month %s last trading day %s",
							ev.PeriodClose.CloseKey, td.Date, m.YearMonth, lastTradingDate)
					}
					if m.IsFiscalYearEnd {
						if evIdx != len(td.Events)-2 {
							t.Fatalf("period close in fiscal year-end month %s must be second-to-last event on last trading day", m.YearMonth)
						}
					} else if evIdx != len(td.Events)-1 {
						t.Fatalf("period close in month %s must be the final event on last trading day", m.YearMonth)
					}
					seenPeriodCloseInMonth = true

				case FinWorldEventYearClose:
					if ev.YearClose == nil {
						t.Fatalf("event %s has nil YearClose payload", ev.EventKey)
					}
					if !m.IsFiscalYearEnd {
						t.Fatalf("year close %s emitted in non-year-end month %s", ev.YearClose.YearCloseKey, m.YearMonth)
					}
					if !isLastDay || td.Date != lastTradingDate || !seenPeriodCloseInMonth || evIdx != len(td.Events)-1 {
						t.Fatalf("year close %s must immediately follow period close at end of last trading day in %s",
							ev.YearClose.YearCloseKey, m.YearMonth)
					}
					seenYearCloseInMonth = true
				}
			}
		}
	}
}
