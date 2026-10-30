//go:build finworld

package main

import (
	"fmt"
	"sort"
	"testing"
	"time"
)

// finworldLandedCostEventFact is one landed cost event read back off a finished plan.
type finworldLandedCostEventFact struct {
	EventKey        string
	Date            string
	Month           YearMonth
	AppliedMonth    YearMonth
	IsMonthEnd      bool
	BranchCode      string
	ReceiptKey      string
	ReceiptDate     string
	ReceiptMonth    YearMonth
	ChargeCents     int64
	ChargeKind      string
	Status          string
	AppliedDate     string
	VoidOfKey       string
	VoidedDate      string
	Currency        string
	AllocationBasis string
}

// finworldLandedCostFacts is what the landed cost rule has to satisfy, read back off a finished plan.
type finworldLandedCostFacts struct {
	events                 []finworldLandedCostEventFact
	appliedByQuarter       map[string]int
	appliedByQuarterBranch map[string]int
	voidsByQuarter         map[string]int
	crossedMonthByQuarter  map[string]int
	monthEndByQuarter      map[string]int
	chargeKinds            map[string]int
	nonPositiveCharges     int
	unknownReceipts        int
	receiptAfterEvent      int
	receiptTooFarBack      int
	badVoidOrder           int
	badVoidTarget          int
	detail                 string
}

// finworldLandedCostPlanFacts walks a finished plan and collects the landed cost side of the
// world: every landed cost event, the purchase receipt each one names, and every way a landed
// cost can break the rule.
func finworldLandedCostPlanFacts(t *testing.T, plan *FinWorldPlanT) finworldLandedCostFacts {
	t.Helper()

	// Every purchase receipt in the plan, with the month it landed in.
	type receiptFact struct {
		Date   string
		Month  YearMonth
		Branch string
	}
	receipts := map[string]receiptFact{}

	for _, m := range plan.Months {
		for _, td := range m.TradingDays {
			for _, ev := range td.Events {
				if ev.PurchaseReceipt == nil {
					continue
				}
				receipts[ev.PurchaseReceipt.PurchaseReceiptKey] = receiptFact{
					Date:   td.Date,
					Month:  m.YearMonth,
					Branch: ev.PurchaseReceipt.BranchCode,
				}
			}
		}
	}

	f := finworldLandedCostFacts{
		appliedByQuarter:       map[string]int{},
		appliedByQuarterBranch: map[string]int{},
		voidsByQuarter:         map[string]int{},
		crossedMonthByQuarter:  map[string]int{},
		monthEndByQuarter:      map[string]int{},
		chargeKinds:            map[string]int{},
	}

	// The landed cost keys the plan has already applied, so a void can be checked against the
	// landed cost it reverses.
	appliedKeys := map[string]bool{}
	appliedMonthByKey := map[string]YearMonth{}
	appliedDateByKey := map[string]string{}

	for _, m := range plan.Months {
		quarter := finworldQuarterOf(m.YearMonth)
		for _, td := range m.TradingDays {
			for _, ev := range td.Events {
				if ev.LandedCost == nil {
					continue
				}
				lc := ev.LandedCost
				isVoid := lc.Status == "VOIDED"

				// The landed cost's own month is the month it was applied in. A void repeats
				// the document it reverses, so the rule is measured against the applied month
				// and not against the day the void was booked.
				appliedMonth := m.YearMonth
				if appliedYM, ok := finworldYearMonthOf(lc.AppliedDate); ok {
					appliedMonth = appliedYM
				}

				fact := finworldLandedCostEventFact{
					EventKey:        ev.EventKey,
					Date:            ev.Date,
					Month:           m.YearMonth,
					AppliedMonth:    appliedMonth,
					IsMonthEnd:      td.IsLastOfMonth,
					BranchCode:      lc.BranchCode,
					ReceiptKey:      lc.PurchaseReceiptKey,
					ChargeCents:     lc.ChargeCents,
					ChargeKind:      lc.ChargeKind,
					Status:          lc.Status,
					AppliedDate:     lc.AppliedDate,
					VoidOfKey:       lc.VoidOfLandedCostKey,
					VoidedDate:      lc.VoidedDate,
					Currency:        lc.Currency,
					AllocationBasis: lc.AllocationBasis,
				}

				// The receipt the landed cost names must exist earlier in the plan.
				rcpt, ok := receipts[lc.PurchaseReceiptKey]
				switch {
				case !ok:
					f.unknownReceipts++
					if f.detail == "" {
						f.detail = fmt.Sprintf("landed cost %s names purchase receipt %q, which the plan does not carry",
							lc.LandedCostKey, lc.PurchaseReceiptKey)
					}
				case rcpt.Date >= ev.Date:
					f.receiptAfterEvent++
					if f.detail == "" {
						f.detail = fmt.Sprintf("landed cost %s on %s names purchase receipt %s, which the plan carries on %s",
							lc.LandedCostKey, ev.Date, lc.PurchaseReceiptKey, rcpt.Date)
					}
				default:
					fact.ReceiptDate = rcpt.Date
					fact.ReceiptMonth = rcpt.Month
					// The receipt is from the month the landed cost was applied in, or the
					// month before.
					if rcpt.Month.Compare(appliedMonth) != 0 && rcpt.Month.Compare(appliedMonth.AddMonths(-1)) != 0 {
						f.receiptTooFarBack++
						if f.detail == "" {
							f.detail = fmt.Sprintf("landed cost %s applied on %s names purchase receipt %s from %s, the applied month is %s or %s",
								lc.LandedCostKey, lc.AppliedDate, lc.PurchaseReceiptKey, rcpt.Month, appliedMonth, appliedMonth.AddMonths(-1))
						}
					}
				}

				// Every charge is a positive integer number of cents.
				if lc.ChargeCents <= 0 {
					f.nonPositiveCharges++
					if f.detail == "" {
						f.detail = fmt.Sprintf("landed cost %s carries a charge of %d cents, the rule asks for a positive integer",
							lc.LandedCostKey, lc.ChargeCents)
					}
				}

				f.chargeKinds[lc.ChargeKind]++

				if isVoid {
					f.voidsByQuarter[quarter]++
					// A void reverses a landed cost the plan applied earlier.
					if lc.VoidOfLandedCostKey == "" || !appliedKeys[lc.VoidOfLandedCostKey] {
						f.badVoidTarget++
						if f.detail == "" {
							f.detail = fmt.Sprintf("void %s on %s names landed cost %q, which the plan did not apply earlier",
								lc.LandedCostKey, ev.Date, lc.VoidOfLandedCostKey)
						}
					}
					if lc.VoidedDate != ev.Date {
						f.badVoidOrder++
						if f.detail == "" {
							f.detail = fmt.Sprintf("void %s on %s carries voided date %q, the rule asks for the day it was voided",
								lc.LandedCostKey, ev.Date, lc.VoidedDate)
						}
					}
					// The void repeats the document it reverses.
					if origDate, ok := appliedDateByKey[lc.VoidOfLandedCostKey]; ok && origDate != lc.AppliedDate {
						f.badVoidOrder++
						if f.detail == "" {
							f.detail = fmt.Sprintf("void %s on %s carries applied date %q, the landed cost it reverses was applied on %q",
								lc.LandedCostKey, ev.Date, lc.AppliedDate, origDate)
						}
					}
					if origMonth, ok := appliedMonthByKey[lc.VoidOfLandedCostKey]; ok && origMonth.Compare(appliedMonth) != 0 {
						f.badVoidOrder++
						if f.detail == "" {
							f.detail = fmt.Sprintf("void %s on %s carries applied month %s, the landed cost it reverses was applied in %s",
								lc.LandedCostKey, ev.Date, appliedMonth, origMonth)
						}
					}
				} else {
					appliedKeys[lc.LandedCostKey] = true
					appliedMonthByKey[lc.LandedCostKey] = appliedMonth
					appliedDateByKey[lc.LandedCostKey] = lc.AppliedDate
					f.appliedByQuarter[quarter]++
					f.appliedByQuarterBranch[quarter+"|"+lc.BranchCode]++
					if fact.ReceiptMonth.Compare(appliedMonth) != 0 {
						f.crossedMonthByQuarter[quarter]++
					}
					if td.IsLastOfMonth {
						f.monthEndByQuarter[quarter]++
					}
				}

				f.events = append(f.events, fact)
			}
		}
	}

	return f
}

// finworldYearMonthOf reads the year and month off a YYYY-MM-DD date string.
func finworldYearMonthOf(dateStr string) (YearMonth, bool) {
	parsed, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return YearMonth{}, false
	}
	return YearMonth{Year: parsed.Year(), Month: int(parsed.Month())}, true
}

// TestFinWorldPlan_LandedCostsLandOnEarlierReceipts asserts the landed cost rule on seed 1: the
// counts per quarter per branch, at least one applied across a month end per quarter, at least
// one void per quarter, every charge a positive integer number of cents, and every landed cost
// naming a purchase receipt that the plan already carried.
func TestFinWorldPlan_LandedCostsLandOnEarlierReceipts(t *testing.T) {
	anchor := YearMonth{Year: 2026, Month: 9}
	plan, err := FinWorldPlan(1, anchor, finworldTestDealerName)
	if err != nil {
		t.Fatalf("FinWorldPlan(1, %s, %q) failed: %v", anchor, finworldTestDealerName, err)
	}
	f := finworldLandedCostPlanFacts(t, plan)

	if len(f.events) == 0 {
		t.Fatal("the plan carries no landed cost events")
	}

	// Counts per quarter: 3 to 5 a quarter per branch.
	for _, q := range finworldPlanQuarters(plan) {
		for _, br := range []string{FinWorldBranchCodeTRT, FinWorldBranchCodeKGN} {
			got := f.appliedByQuarterBranch[q+"|"+br]
			if got < 3 || got > 5 {
				t.Fatalf("quarter %s branch %s carries %d landed costs, the rule asks for 3 to 5",
					q, br, got)
			}
		}
	}

	// At least one applied across a month end per quarter: the charge is applied in the month
	// after its receipt, so it crosses the month boundary.
	for _, q := range finworldPlanQuarters(plan) {
		if f.crossedMonthByQuarter[q] < 1 {
			t.Fatalf("quarter %s carries no landed cost applied in the month after its receipt", q)
		}
	}

	// At least one applied on a month's last trading day per quarter.
	for _, q := range finworldPlanQuarters(plan) {
		if f.monthEndByQuarter[q] < 1 {
			t.Fatalf("quarter %s carries no landed cost applied on a month's last trading day", q)
		}
	}

	// At least one void per quarter.
	for _, q := range finworldPlanQuarters(plan) {
		if f.voidsByQuarter[q] < 1 {
			t.Fatalf("quarter %s carries no voided landed cost", q)
		}
	}

	// Every charge is a positive integer number of cents.
	if f.nonPositiveCharges != 0 {
		t.Fatalf("%d landed costs carry a charge that is not a positive integer number of cents: %s",
			f.nonPositiveCharges, f.detail)
	}

	// Every landed cost names a purchase receipt that the plan already carried.
	if f.unknownReceipts != 0 {
		t.Fatalf("%d landed costs name a purchase receipt the plan does not carry: %s",
			f.unknownReceipts, f.detail)
	}
	if f.receiptAfterEvent != 0 {
		t.Fatalf("%d landed costs name a purchase receipt the plan carries later: %s",
			f.receiptAfterEvent, f.detail)
	}
	if f.receiptTooFarBack != 0 {
		t.Fatalf("%d landed costs name a receipt from outside the application month or the month before: %s",
			f.receiptTooFarBack, f.detail)
	}

	// Every void reverses a landed cost the plan applied earlier.
	if f.badVoidTarget != 0 {
		t.Fatalf("%d voids name a landed cost the plan did not apply earlier: %s",
			f.badVoidTarget, f.detail)
	}
	if f.badVoidOrder != 0 {
		t.Fatalf("%d voids carry a voided date that is not the day they were voided: %s",
			f.badVoidOrder, f.detail)
	}

	// The charges are freight and duty, and the module's own currency and bases.
	if f.chargeKinds["FREIGHT"] < 1 || f.chargeKinds["DUTY"] < 1 {
		t.Fatalf("the landed costs carry %d freight and %d duty charges, the rule asks for both",
			f.chargeKinds["FREIGHT"], f.chargeKinds["DUTY"])
	}
	for _, ev := range f.events {
		if ev.Currency != "CAD" {
			t.Fatalf("landed cost %s carries currency %q, the module's own currency is CAD", ev.EventKey, ev.Currency)
		}
		switch ev.AllocationBasis {
		case "WEIGHT", "VALUE", "QUANTITY":
		default:
			t.Fatalf("landed cost %s carries allocation basis %q, the module's own bases are WEIGHT, VALUE and QUANTITY",
				ev.EventKey, ev.AllocationBasis)
		}
	}
}

// TestFinWorldPlan_LandedCostsLandOnEarlierReceiptsEverySeed asserts the landed cost rule on
// every seed from 1 to 20, not only on seed 1: a charge that rides a receipt the branch has not
// had yet would silently drop one of the quarter's landed costs, and the seed that caught it
// was not the seed the world is published on.
func TestFinWorldPlan_LandedCostsLandOnEarlierReceiptsEverySeed(t *testing.T) {
	anchor := YearMonth{Year: 2026, Month: 9}
	for seed := uint64(1); seed <= 20; seed++ {
		plan, err := FinWorldPlan(seed, anchor, finworldTestDealerName)
		if err != nil {
			t.Fatalf("FinWorldPlan(%d, %s, %q) failed: %v", seed, anchor, finworldTestDealerName, err)
		}
		f := finworldLandedCostPlanFacts(t, plan)
		if len(f.events) == 0 {
			t.Fatalf("seed %d carries no landed cost events", seed)
		}

		for _, q := range finworldPlanQuarters(plan) {
			for _, br := range []string{FinWorldBranchCodeTRT, FinWorldBranchCodeKGN} {
				got := f.appliedByQuarterBranch[q+"|"+br]
				if got < 3 || got > 5 {
					t.Fatalf("seed %d quarter %s branch %s carries %d landed costs, the rule asks for 3 to 5",
						seed, q, br, got)
				}
			}
			if f.crossedMonthByQuarter[q] < 1 {
				t.Fatalf("seed %d quarter %s carries no landed cost applied in the month after its receipt", seed, q)
			}
			if f.monthEndByQuarter[q] < 1 {
				t.Fatalf("seed %d quarter %s carries no landed cost applied on a month's last trading day", seed, q)
			}
			if f.voidsByQuarter[q] < 1 {
				t.Fatalf("seed %d quarter %s carries no voided landed cost", seed, q)
			}
		}
		if f.nonPositiveCharges != 0 {
			t.Fatalf("seed %d carries %d landed costs whose charge is not a positive integer number of cents: %s",
				seed, f.nonPositiveCharges, f.detail)
		}
		if f.unknownReceipts != 0 || f.receiptAfterEvent != 0 || f.receiptTooFarBack != 0 {
			t.Fatalf("seed %d carries a landed cost that does not ride an earlier receipt: %s", seed, f.detail)
		}
		if f.badVoidTarget != 0 || f.badVoidOrder != 0 {
			t.Fatalf("seed %d carries a void that does not reverse a landed cost applied earlier: %s", seed, f.detail)
		}
	}
}

// TestFinWorldPlan_LandedCostsAreDeterministic asserts that the same seed gives the same landed
// costs, in the same order, with the same charges.
func TestFinWorldPlan_LandedCostsAreDeterministic(t *testing.T) {
	anchor := YearMonth{Year: 2026, Month: 9}

	read := func() []finworldLandedCostEventFact {
		plan, err := FinWorldPlan(1, anchor, finworldTestDealerName)
		if err != nil {
			t.Fatalf("FinWorldPlan(1, %s, %q) failed: %v", anchor, finworldTestDealerName, err)
		}
		return finworldLandedCostPlanFacts(t, plan).events
	}

	first := read()
	second := read()
	if len(first) == 0 {
		t.Fatal("the plan carries no landed cost events")
	}
	if len(first) != len(second) {
		t.Fatalf("the same seed gave %d landed costs on the first call and %d on the second", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("the same seed gave a different landed cost at position %d: %+v then %+v", i, first[i], second[i])
		}
	}
}

// TestFinWorldPlan_LandedCostsLeaveTheRestOfTheWorldAlone asserts that the landed costs do not
// touch the sales side of the world: the sale documents, the sale lines and the sales money are
// the same whether or not the landed costs are there.
func TestFinWorldPlan_LandedCostsLeaveTheRestOfTheWorldAlone(t *testing.T) {
	anchor := YearMonth{Year: 2026, Month: 9}
	plan, err := FinWorldPlan(1, anchor, finworldTestDealerName)
	if err != nil {
		t.Fatalf("FinWorldPlan(1, %s, %q) failed: %v", anchor, finworldTestDealerName, err)
	}

	var salesDocs, salesLines, purchaseReceipts, vendorBills int
	var salesSubtotal, salesTax, salesTotal, returnsTotal int64
	for _, m := range plan.Months {
		for _, td := range m.TradingDays {
			for _, ev := range td.Events {
				switch {
				case ev.Sale != nil:
					salesDocs++
					salesLines += len(ev.Sale.Lines)
					salesSubtotal += ev.Sale.SubtotalCents
					salesTax += ev.Sale.TaxCents
					salesTotal += ev.Sale.TotalCents
				case ev.Return != nil:
					returnsTotal += ev.Return.TotalCents
				case ev.PurchaseReceipt != nil:
					purchaseReceipts++
				case ev.VendorBill != nil:
					vendorBills++
				}
			}
		}
	}

	if salesDocs != 11820 || salesLines != 19017 {
		t.Fatalf("the plan carries %d sale documents and %d sale lines, the world before the landed costs carried 11820 and 19017",
			salesDocs, salesLines)
	}
	// The money pin rides the tree's own base world. Round 4 pinned round 3's
	// 414779960 / 53695286 / 468475246; the driver's round 2 world moved the sales money (the
	// reorder multiples, the payment method draws and the tax consistent line building) while the
	// document counts and the returns held, so the merged tree re-pinned here. W2-1d item 2 left
	// these figures alone: the contractors' unique names draw from the plan's own name stream, not
	// the world's, so the sales side never moved. The invariant under test is unchanged: the
	// landed-cost stream is the plan's own PCG stream and these figures are whatever the
	// non-landed-cost world carries, so any future change to either side trips it.
	if salesSubtotal != 418148682 || salesTax != 54133056 || salesTotal != 472281738 {
		t.Fatalf("the plan carries sales %d subtotal, %d tax, %d total cents, the merged world's base carried 418148682, 54133056 and 472281738",
			salesSubtotal, salesTax, salesTotal)
	}
	if returnsTotal != 13421676 {
		t.Fatalf("the plan carries %d cents of returns, the merged world's base carried 13421676", returnsTotal)
	}
	if purchaseReceipts == 0 || vendorBills == 0 {
		t.Fatalf("the plan carries %d purchase receipts and %d vendor bills, the world carries both",
			purchaseReceipts, vendorBills)
	}
}

// TestFinWorldPlan_LandedCostAppliedDateMatchesItsDay asserts that a landed cost carries the
// day it was applied, the module's own statuses, and that the applied month is the month the
// event sits in.
func TestFinWorldPlan_LandedCostAppliedDateMatchesItsDay(t *testing.T) {
	anchor := YearMonth{Year: 2026, Month: 9}
	plan, err := FinWorldPlan(1, anchor, finworldTestDealerName)
	if err != nil {
		t.Fatalf("FinWorldPlan(1, %s, %q) failed: %v", anchor, finworldTestDealerName, err)
	}
	facts := finworldLandedCostPlanFacts(t, plan)
	if len(facts.events) == 0 {
		t.Fatal("the plan carries no landed cost events")
	}

	months := []string{}
	seen := map[string]bool{}
	for _, ev := range facts.events {
		switch ev.Status {
		case "POSTED":
			// An applied landed cost carries the day it was applied.
			if ev.AppliedDate != ev.Date {
				t.Fatalf("landed cost %s carries applied date %q, the event sits on %s", ev.EventKey, ev.AppliedDate, ev.Date)
			}
		case "VOIDED":
			// A void carries the day the landed cost it reverses was applied, and the day the
			// void itself was booked.
			if ev.AppliedDate >= ev.Date {
				t.Fatalf("void %s on %s carries applied date %q, the rule asks for an earlier day",
					ev.EventKey, ev.Date, ev.AppliedDate)
			}
			if ev.VoidedDate != ev.Date {
				t.Fatalf("void %s on %s carries voided date %q, the rule asks for the day it was voided",
					ev.EventKey, ev.Date, ev.VoidedDate)
			}
		default:
			t.Fatalf("landed cost %s carries status %q, the module's own statuses are POSTED and VOIDED",
				ev.EventKey, ev.Status)
		}
		if _, err := time.Parse("2006-01-02", ev.Date); err != nil {
			t.Fatalf("landed cost %s carries an unparsable date %q", ev.EventKey, ev.Date)
		}
		if !seen[ev.Month.String()] {
			seen[ev.Month.String()] = true
			months = append(months, ev.Month.String())
		}
	}
	sort.Strings(months)
	t.Logf("landed costs land in %d months: %v", len(months), months)
}
