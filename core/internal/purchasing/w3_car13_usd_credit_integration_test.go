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

// runLineFor finds a run's line for billID.
func runLineFor(run PaymentRun, billID string) *PaymentRunLine {
	for i := range run.Lines {
		if run.Lines[i].BillID == billID {
			return &run.Lines[i]
		}
	}
	return nil
}

// A payment run takes CAD bills only (the lead's ruling on the run's gap): the proposal leaves a
// USD bill out and says so in words, and the run over the CAD bill pays and completes.
func TestIntegration_W3Car13_RunProposesOnlyCADAndNamesTheUSDBill(t *testing.T) {
	rig := newVCRig(t)
	today := time.Now().In(rig.zone).Format("2006-01-02")
	_, cad := rig.createReceivedPOAndPostBill(t, "car13runcad", 50_000, today, today)
	usd := rig.postUSDBillAt(t, "car13runusd", today, today, 100_000, 1.35, time.Time{})

	run, err := rig.svc.ProposePaymentRun(rig.ctx, ProposePaymentRunRequest{BranchID: branchTRT, DueOn: today, VendorID: rig.vendor})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	cleanupRun(t, rig.pool, run.ID)
	if runLineFor(run, cad.ID) == nil {
		t.Fatalf("W3-Car13 red: the run holds no line for the CAD bill: %+v", run.Lines)
	}
	if runLineFor(run, usd.ID) != nil {
		t.Fatalf("W3-Car13 red: the run proposed the USD bill %s", usd.BillNo)
	}
	want := usd.BillNo + " is billed in USD: pay it from its bill with the day's rate"
	found := false
	for _, w := range run.LeftOut {
		if w == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("W3-Car13 red: left_out %q does not name the USD bill as %q", run.LeftOut, want)
	}

	paid, err := rig.svc.PayPaymentRun(rig.ctx, run.ID)
	if err != nil {
		t.Fatalf("W3-Car13 red: the run's pay: %v", err)
	}
	if paid.Status != RunPaid {
		t.Fatalf("W3-Car13 red: run status %s after the pay, want %s", paid.Status, RunPaid)
	}
	if ln := runLineFor(paid, cad.ID); ln == nil || ln.Status != RunLinePaid || ln.PaidCents != 50_000 {
		t.Fatalf("W3-Car13 red: the CAD line after the pay: %+v", ln)
	}
	if got := rig.billNetAP2100Cents(t, usd.ID); got != 135_000 {
		t.Fatalf("W3-Car13 red: the USD bill's 2100 net %d after the run, want 135000 (untouched)", got)
	}
}

// A USD bill's line planted on a run directly (a run proposed before the ruling, or a bill whose
// currency was edited) is skipped with the reason while the CAD line pays and the run completes.
func TestIntegration_W3Car13_RunSkipsAPlantedUSDLineAndPaysTheRest(t *testing.T) {
	rig := newVCRig(t)
	today := time.Now().In(rig.zone).Format("2006-01-02")
	_, cad := rig.createReceivedPOAndPostBill(t, "car13plantcad", 50_000, today, today)
	usd := rig.postUSDBillAt(t, "car13plantusd", today, today, 100_000, 1.35, time.Time{})

	run, err := rig.svc.ProposePaymentRun(rig.ctx, ProposePaymentRunRequest{BranchID: branchTRT, DueOn: today, VendorID: rig.vendor})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	cleanupRun(t, rig.pool, run.ID)
	tx, err := rig.svc.repo.Begin(rig.ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := rig.svc.repo.InsertPaymentRunLine(rig.ctx, tx, run.ID, len(run.Lines)+1, usd.ID, 135_000); err != nil {
		_ = tx.Rollback(rig.ctx)
		t.Fatalf("plant the USD line: %v", err)
	}
	if err := tx.Commit(rig.ctx); err != nil {
		t.Fatalf("commit the planted line: %v", err)
	}

	paid, err := rig.svc.PayPaymentRun(rig.ctx, run.ID)
	if err != nil {
		t.Fatalf("W3-Car13 red: a planted USD line refused the whole run: %v", err)
	}
	if paid.Status != RunPaid {
		t.Fatalf("W3-Car13 red: run status %s, want %s", paid.Status, RunPaid)
	}
	if ln := runLineFor(paid, cad.ID); ln == nil || ln.Status != RunLinePaid || ln.PaidCents != 50_000 {
		t.Fatalf("W3-Car13 red: the CAD line: %+v", ln)
	}
	ln := runLineFor(paid, usd.ID)
	wantReason := usd.BillNo + " is billed in USD: pay it from its bill with the day's rate"
	if ln == nil || ln.Status != RunLineSkipped || ln.SkipReason != wantReason || ln.PaidCents != 0 {
		t.Fatalf("W3-Car13 red: the USD line %+v, want SKIPPED with %q", ln, wantReason)
	}
	if got := rig.billNetAP2100Cents(t, usd.ID); got != 135_000 {
		t.Fatalf("W3-Car13 red: the USD bill's 2100 net %d, want 135000 (untouched)", got)
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
