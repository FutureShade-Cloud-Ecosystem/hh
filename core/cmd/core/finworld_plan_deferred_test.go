//go:build finworld

package main

// The richer AP (W2-1b part 2 round 3 addendum): a few vendor bills stay open across a month end on
// net 30 terms, drawn on their own random stream so nothing else in the world moves, and the plan
// settles each with a vendor payment on the next month's first trading day.

import (
	"testing"
)

func TestFinWorldPlanLeavesAFewBillsOpenAcrossMonthEnds(t *testing.T) {
	plan, err := FinWorldPlan(1, YearMonth{Year: 2026, Month: 9}, "Quarryfen Hardscape Supply")
	if err != nil {
		t.Fatalf("FinWorldPlan: %v", err)
	}

	bills, deferred := 0, 0
	payments := 0
	deferredByKey := map[string]*FinWorldVendorBillEvent{}
	for _, d := range finworldFlattenDays(plan) {
		for _, ev := range d.Day.Events {
			switch {
			case ev.VendorBill != nil:
				bills++
				if ev.VendorBill.PaymentDeferred {
					deferred++
					deferredByKey[ev.VendorBill.VendorBillKey] = ev.VendorBill
				}
			case ev.VendorPayment != nil:
				payments++
				vp := ev.VendorPayment
				bill, ok := deferredByKey[vp.VendorBillKey]
				if !ok {
					t.Fatalf("vendor payment %s names bill %s, which the plan did not leave open", vp.VendorPaymentKey, vp.VendorBillKey)
				}
				if bill.BranchCode != vp.BranchCode || bill.TotalCents != vp.AmountCents || bill.VendorInvoiceNo != vp.VendorInvoiceNo {
					t.Fatalf("vendor payment %s does not repeat its bill (%s/%d/%s vs %s/%d/%s)",
						vp.VendorPaymentKey, bill.BranchCode, bill.TotalCents, bill.VendorInvoiceNo,
						vp.BranchCode, vp.AmountCents, vp.VendorInvoiceNo)
				}
				if ev.Date[:7] == bill.IssueDate[:7] {
					t.Fatalf("vendor payment %s settles %s in its own month; it must stay open across the month end", vp.VendorPaymentKey, vp.VendorBillKey)
				}
			}
		}
	}

	if bills == 0 {
		t.Fatalf("the plan carries no vendor bills")
	}
	if deferred == 0 {
		t.Fatalf("the plan carries no deferred vendor bills: the AP would be zero at every month end")
	}
	if deferred >= bills {
		t.Fatalf("the plan defers all %d vendor bills; a few stay open, not the whole ledger", bills)
	}
	if payments == 0 {
		t.Fatalf("the plan carries no vendor payment events")
	}
	if payments > deferred {
		t.Fatalf("the plan carries %d vendor payments for %d deferred bills", payments, deferred)
	}
	t.Logf("bills=%d deferred=%d payments=%d", bills, deferred, payments)
}
