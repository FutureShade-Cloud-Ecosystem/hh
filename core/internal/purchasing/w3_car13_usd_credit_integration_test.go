//go:build integration

package purchasing

import (
	"strings"
	"testing"
	"time"
)

// Car 13 (PR 717 merged onto 704): a vendor credit and its application are CAD cents. On a USD bill
// the credit comes off the document open at the bill's own rate, so the bill pays its remaining
// document amount to zero, refuses one cent more, and 2100 keeps only the credit's CAD.

func TestIntegration_W3Car13_USDBillWithCreditPaysRemainingDocAmount(t *testing.T) {
	for _, tc := range []struct {
		name       string
		payRate    float64
		wantCash   int64
		wantFXLoss int64
	}{
		{"at the bill's own rate", 1.35, 108_000, 0},
		{"at a different rate", 1.40, 112_000, 4_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newVCRig(t)
			today := time.Now().In(rig.zone).Format("2006-01-02")
			// USD 1,000.00 at 1.35 is CAD 1,350.00; a CAD 270.00 credit is USD 200.00 at the same rate.
			bill := rig.postUSDBillAt(t, "car13", today, today, 100_000, 1.35, time.Time{})
			credit := rig.createPostedCredit(t, "car13", branchTRT, 27_000)
			if _, err := rig.svc.ApplyVendorCredit(rig.ctx, credit.ID, ApplyVendorCreditRequest{
				BillID: bill.ID, AmountCents: 27_000, IdempotencyKey: "car13-apply-" + tc.name,
			}); err != nil {
				t.Fatalf("apply: %v", err)
			}

			detail, err := rig.svc.GetBill(rig.ctx, bill.ID)
			if err != nil {
				t.Fatalf("GetBill: %v", err)
			}
			if detail.DocOpenCents != 80_000 {
				t.Fatalf("W3-Car13 red: doc_open_cents %d after the credit, want 80000", detail.DocOpenCents)
			}
			if got := rig.docOpenInAging(t, bill.ID); got != 80_000 {
				t.Fatalf("W3-Car13 red: aging doc_open_cents %d, want 80000", got)
			}

			_, err = rig.svc.PayBill(rig.ctx, bill.ID, PayBillRequest{AmountCents: 80_001, FXRate: tc.payRate, IdempotencyKey: "car13-over-" + tc.name})
			if err == nil || !strings.Contains(err.Error(), "exceeds open balance 80000") {
				t.Fatalf("W3-Car13 red: 80001 USD cents paid on an 80000 open: %v", err)
			}
			paid, err := rig.svc.PayBill(rig.ctx, bill.ID, PayBillRequest{AmountCents: 80_000, FXRate: tc.payRate, IdempotencyKey: "car13-pay-" + tc.name})
			if err != nil {
				t.Fatalf("W3-Car13 red: paying the remaining 80000 USD cents: %v", err)
			}
			if paid.DocOpenCents != 0 {
				t.Fatalf("W3-Car13 red: doc_open_cents %d after paying the remainder, want 0", paid.DocOpenCents)
			}
			if _, err := rig.svc.PayBill(rig.ctx, bill.ID, PayBillRequest{AmountCents: 1, FXRate: tc.payRate, IdempotencyKey: "car13-one-" + tc.name}); err == nil {
				t.Fatalf("W3-Car13 red: one more cent was accepted on a settled bill")
			}

			legs := rig.paymentJournalLegs(t, "car13-pay-"+tc.name)
			if legs["2100"] != ([2]int64{108_000, 0}) {
				t.Fatalf("W3-Car13 red: 2100 = %v, want DR 108000 (the bill's CAD less the credit)", legs["2100"])
			}
			if legs["1000"] != ([2]int64{0, tc.wantCash}) {
				t.Fatalf("W3-Car13 red: 1000 = %v, want CR %d", legs["1000"], tc.wantCash)
			}
			if tc.wantFXLoss == 0 {
				if _, has := legs["6310"]; has {
					t.Fatalf("W3-Car13 red: FX posted at the bill's own rate: %v", legs["6310"])
				}
			} else if legs["6310"] != ([2]int64{tc.wantFXLoss, 0}) {
				t.Fatalf("W3-Car13 red: 6310 = %v, want DR %d", legs["6310"], tc.wantFXLoss)
			}
			// The bill's own 2100 is its CAD less what it cleared; the credit's CAD is what remains,
			// netted by the credit's own journal.
			if got := rig.billNetAP2100Cents(t, bill.ID); got != 27_000 {
				t.Fatalf("W3-Car13 red: bill 2100 net %d, want 27000 (the credit's CAD)", got)
			}
		})
	}
}

// TestIntegration_W3Car13_RunOpenAgreesWithPayBill: the run proposes the CAD open PayBill's own
// guard sees (total, less cleared, less credited) for a USD bill with a credit applied.
func TestIntegration_W3Car13_RunOpenAgreesWithPayBill(t *testing.T) {
	rig := newVCRig(t)
	today := time.Now().In(rig.zone).Format("2006-01-02")
	bill := rig.postUSDBillAt(t, "car13run", today, today, 100_000, 1.35, time.Time{})
	credit := rig.createPostedCredit(t, "car13run", branchTRT, 27_000)
	if _, err := rig.svc.ApplyVendorCredit(rig.ctx, credit.ID, ApplyVendorCreditRequest{
		BillID: bill.ID, AmountCents: 27_000, IdempotencyKey: "car13run-apply",
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	run, err := rig.svc.ProposePaymentRun(rig.ctx, ProposePaymentRunRequest{BranchID: branchTRT, DueOn: today, VendorID: rig.vendor})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	cleanupRun(t, rig.pool, run.ID)
	var line *PaymentRunLine
	for i := range run.Lines {
		if run.Lines[i].BillID == bill.ID {
			line = &run.Lines[i]
		}
	}
	if line == nil {
		t.Fatalf("W3-Car13 red: the run holds no line for the USD bill: %+v", run.Lines)
	}
	_, open, err := rig.svc.repo.BillPayState(rig.ctx, bill.ID)
	if err != nil {
		t.Fatalf("BillPayState: %v", err)
	}
	if line.AmountCents != open || open != 108_000 {
		t.Fatalf("W3-Car13 red: run line %d cents, BillPayState open %d, want both 108000 (PayBill's CAD open)", line.AmountCents, open)
	}
	// The run carries no payment rate, and PayBill takes a USD bill's amount in USD at a rate: the
	// pay is refused in words and moves no money. Paying USD bills from a run needs a rule for the
	// rate and the amount that this car does not invent (see the report).
	_, err = rig.svc.PayPaymentRun(rig.ctx, run.ID)
	if err == nil || !strings.Contains(err.Error(), "fx_rate is required") {
		t.Fatalf("W3-Car13: the run's pay of a USD bill: %v, want the refusal naming fx_rate", err)
	}
	if got := rig.billNetAP2100Cents(t, bill.ID); got != 135_000 {
		t.Fatalf("W3-Car13: bill 2100 net %d after the refused run pay, want 135000", got)
	}
}

// docOpenInAging returns the aging item's doc_open_cents for billID.
func (rig *apAgingTestRig) docOpenInAging(t *testing.T, billID string) int64 {
	t.Helper()
	page, err := rig.svc.APAging(rig.ctx, time.Now().UTC().Add(time.Second), BranchReach{BranchIDs: []string{branchTRT}})
	if err != nil {
		t.Fatalf("APAging: %v", err)
	}
	for _, row := range page.Items {
		for _, item := range row.OpenItems {
			if item.ID == billID {
				return item.DocOpenCents
			}
		}
	}
	t.Fatalf("bill %s not in the aging", billID)
	return 0
}
