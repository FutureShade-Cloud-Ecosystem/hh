//go:build integration

package purchasing

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
)

// TestIntegration_W29AR2B_NonBillCreditIsATieDifference pins S2's P2 2 as designed
// (FROM-PRINCIPAL 122, 124, 125): the aging IS the subledger, so a 2100 credit that
// is neither a posted vendor bill nor an open landed cost payable (an ADJUSTMENT or
// MANUAL accrual, another module's posting) shows as a tie difference against
// GL 2100, never as an aging row. This leg pins the measured behavior; it does not
// change it.
func TestIntegration_W29AR2B_NonBillCreditIsATieDifference(t *testing.T) {
	rig := newAPAgingRig(t)
	reach := BranchReach{BranchIDs: []string{branchTRT}}
	todayStr := time.Now().In(rig.zone).Format("2006-01-02")

	_, bill := rig.createReceivedPOAndPostBill(t, "r2b-adj", 40000, todayStr, todayStr)

	adjKey := "w29ar2b:adj:" + bill.ID
	if _, err := rig.gl.PostExternal(rig.ctx, gl.ExternalJournal{
		Source:         "ADJUSTMENT",
		IdempotencyKey: adjKey,
		Memo:           "an accrual, nobody's bill",
		BranchID:       branchTRT,
		DocumentDate:   time.Now().UTC(),
		Lines: []gl.ExternalLine{
			{AccountCode: "5000", DebitCents: 3000, Memo: "accrued expense"},
			{AccountCode: "2100", CreditCents: 3000, Memo: "accrued AP"},
		},
	}); err != nil {
		t.Fatalf("post adjustment credit: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE idempotency_key = $1)`, adjKey)
		_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journals WHERE idempotency_key = $1`, adjKey)
	})

	asOf := time.Now().UTC().Add(time.Second)
	page, err := rig.svc.APAging(rig.ctx, asOf, reach)
	if err != nil {
		t.Fatalf("APAging: %v", err)
	}
	// The aging is unchanged: the accrual names no bill and no landed cost.
	if page.TotalCents != 40000 {
		t.Fatalf("W2-9a r2b red: aging total = %d after a 3000 cent non-bill credit, want 40000 (the aging is the subledger, it grows only by bills and landed costs)", page.TotalCents)
	}
	for _, row := range page.Items {
		if row.IsLandedCost {
			continue
		}
		for _, item := range row.OpenItems {
			if item.ID != bill.ID {
				t.Fatalf("W2-9a r2b red: unexpected aging item %s (%d cents) after the accrual", item.ID, item.OpenCents)
			}
		}
	}
	// And so the tie difference is exactly the credit.
	glBal, _, err := rig.glRepo.AccountBalanceAsOf(rig.ctx, "2100", asOf, branchTRT)
	if err != nil {
		t.Fatalf("AccountBalanceAsOf(2100): %v", err)
	}
	// GL 2100 carries the credit, the aging does not: the difference reads from the
	// GL side down to the aging, exactly S2's probe measured it.
	if diff := -glBal - page.TotalCents; diff != 3000 {
		t.Fatalf("W2-9a r2b red: aging total = %d, gl 2100 credit = %d, difference = %d, want the 3000 cent credit as a tie difference", page.TotalCents, -glBal, diff)
	}
}
