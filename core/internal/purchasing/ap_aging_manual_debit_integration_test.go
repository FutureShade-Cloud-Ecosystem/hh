//go:build integration

package purchasing

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
)

// TestIntegration_W29AR2_ManualDebitIsNotASupplierPayment proves the round 2 rule:
// the AP aging is purchasing's own books, posted bills less PayBill's PAID instalments
// plus landed cost payables. A 2100 debit posted by a MANUAL journal is a hand posted
// GL correction, not a supplier payment against a bill, so the aging leaves the bill
// open and the aging total differs from GL 2100 by exactly that debit.
func TestIntegration_W29AR2_ManualDebitIsNotASupplierPayment(t *testing.T) {
	rig := newAPAgingRig(t)
	reach := BranchReach{BranchIDs: []string{branchTRT}}
	todayStr := time.Now().In(rig.zone).Format("2006-01-02")

	const manualDebitCents = int64(12500)

	_, bill := rig.createReceivedPOAndPostBill(t, "r2-manual", 40000, todayStr, todayStr)

	manualKey := "w29ar2:manual:" + bill.ID
	if _, err := rig.gl.PostExternal(rig.ctx, gl.ExternalJournal{
		Source:         "MANUAL",
		IdempotencyKey: manualKey,
		Memo:           "hand posted AP correction",
		BranchID:       branchTRT,
		DocumentDate:   time.Now().UTC(),
		Lines: []gl.ExternalLine{
			{AccountCode: "2100", DebitCents: manualDebitCents, Memo: "AP correction"},
			{AccountCode: "1000", CreditCents: manualDebitCents, Memo: "AP correction"},
		},
	}); err != nil {
		t.Fatalf("post manual 2100 debit: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE idempotency_key = $1)`, manualKey)
		_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journals WHERE idempotency_key = $1`, manualKey)
	})

	asOf := time.Now().UTC().Add(time.Second)
	page, err := rig.svc.APAging(rig.ctx, asOf, reach)
	if err != nil {
		t.Fatalf("APAging: %v", err)
	}

	// The bill stays open: the manual debit named no bill and must not be applied to one.
	var billItem *APAgingItem
	for i := range page.Items {
		if page.Items[i].IsLandedCost {
			continue
		}
		for j := range page.Items[i].OpenItems {
			if page.Items[i].OpenItems[j].ID == bill.ID {
				billItem = &page.Items[i].OpenItems[j]
			}
		}
	}
	if billItem == nil {
		t.Fatalf("W2-9a r2 red: bill %s missing from the aging; the manual debit must leave the bill open", bill.ID)
	}
	if billItem.OpenCents != 40000 || billItem.PaidCents != 0 {
		t.Fatalf("W2-9a r2 red: bill %s shows paid_cents=%d open_cents=%d; a MANUAL 2100 debit is not a supplier payment, want paid_cents=0 open_cents=40000",
			bill.ID, billItem.PaidCents, billItem.OpenCents)
	}
	if page.TotalCents != 40000 {
		t.Fatalf("W2-9a r2 red: aging total=%d, want 40000 (the bill stays open)", page.TotalCents)
	}

	// The tie difference is exactly the manual debit: aging total exceeds GL 2100's
	// net credit by the amount the GL carries and the aging does not. The tie itself
	// is restored by paying the bill through PayBill, never by re-reading the GL for
	// payments (item 4's proof).
	glBal, _, err := rig.glRepo.AccountBalanceAsOf(rig.ctx, "2100", asOf, branchTRT)
	if err != nil {
		t.Fatalf("AccountBalanceAsOf(2100): %v", err)
	}
	glLiabilityCents := -glBal
	if diff := page.TotalCents - glLiabilityCents; diff != manualDebitCents {
		t.Fatalf("W2-9a r2 red: aging total=%d gl_2100_credit_cents=%d difference=%d, want the manual debit %d",
			page.TotalCents, glLiabilityCents, diff, manualDebitCents)
	}
}
