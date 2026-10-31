//go:build integration

package purchasing

import (
	"testing"
	"time"
)

// TestIntegration_W29AR2B_LandedPayableCountsFromPosting proves S2's P3 1 red: a
// landed cost payable counts from its posting, not its creation. The payable's
// inclusion instant is its apply journal's effective instant; a void counts from the
// void journal's effective instant; a retro adjustment from its own journal's. The
// old rule read the row's created_at and updated_at, and every status change stamps
// updated_at with now, so an as-of instant between a landed cost's creation (a
// DRAFT, nothing posted) and its apply counted a payable the GL never carried.
func TestIntegration_W29AR2B_LandedPayableCountsFromPosting(t *testing.T) {
	rig := newAPAgingRig(t)
	reach := BranchReach{BranchIDs: []string{branchTRT}}
	// Every instant hangs off the bill's own house midnight, never off now. The bill's journal
	// counts from that midnight; the apply and void journals are posted for real in this house
	// month, so their stamps must stay in it too or the GL files them late (counting from the
	// end of the filed period). Hours after the midnight satisfy both in every window, including
	// 00:30 on the first of a month, which now-minus-hours stamps cross into the month before.
	nowLocal := time.Now().In(rig.zone)
	midnight := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, rig.zone)
	todayStr := midnight.Format("2006-01-02")

	po, _ := rig.createReceivedPOAndPostBill(t, "r2b-lc", 40000, todayStr, todayStr)
	lcID := rig.createOpenLandedCost(t, po, 7500, 0)

	// C: the landed cost is created (a DRAFT, nothing posted). A: the apply journal
	// posts. V: the void journal posts.
	createdAt := midnight.Add(1 * time.Hour).UTC()
	appliedAt := midnight.Add(2 * time.Hour).UTC()
	voidedAt := midnight.Add(3 * time.Hour).UTC()
	if _, err := rig.pool.Exec(rig.ctx, `UPDATE landed_costs SET created_at = $2, updated_at = $2 WHERE id = $1::uuid`, lcID, createdAt); err != nil {
		t.Fatalf("stamp created_at: %v", err)
	}
	if _, err := rig.pool.Exec(rig.ctx, `UPDATE gl_journals SET posted_at = $2 WHERE idempotency_key = 'landedcost.apply:' || $1`, lcID, appliedAt); err != nil {
		t.Fatalf("stamp apply journal posted_at: %v", err)
	}
	if err := rig.landedSvc.Void(rig.ctx, lcID); err != nil {
		t.Fatalf("void landed cost: %v", err)
	}
	if _, err := rig.pool.Exec(rig.ctx, `UPDATE gl_journals SET posted_at = $2 WHERE idempotency_key = 'landedcost.void:' || $1`, lcID, voidedAt); err != nil {
		t.Fatalf("stamp void journal posted_at: %v", err)
	}
	if _, err := rig.pool.Exec(rig.ctx, `UPDATE landed_costs SET updated_at = $2 WHERE id = $1::uuid`, lcID, voidedAt); err != nil {
		t.Fatalf("stamp updated_at: %v", err)
	}

	landedOpenAt := func(asOf time.Time) int64 {
		t.Helper()
		page, err := rig.svc.APAging(rig.ctx, asOf, reach)
		if err != nil {
			t.Fatalf("APAging: %v", err)
		}
		for _, row := range page.Items {
			if row.IsLandedCost && row.LandedCostID == lcID {
				return row.TotalCents
			}
		}
		return 0
	}

	// Between the creation and the apply the payable counts nothing: a DRAFT owes
	// nobody, and the GL carries no 2100 credit for it yet.
	if got := landedOpenAt(createdAt.Add(30 * time.Minute)); got != 0 {
		t.Fatalf("W2-9a r2b red: the landed cost counts %d cents as of an instant between its creation and its apply, want 0 (nothing was posted yet)", got)
	}
	// Between the apply and the void it counts, and it ties: the apply journal is on
	// 2100 from A, the void is not on until V.
	if got := landedOpenAt(appliedAt.Add(30 * time.Minute)); got != 7500 {
		t.Fatalf("W2-9a r2b red: the landed cost counts %d cents as of an instant between its apply and its void, want 7500", got)
	}
	pageOpen, err := rig.svc.APAging(rig.ctx, appliedAt.Add(30*time.Minute), reach)
	if err != nil {
		t.Fatalf("APAging(open window): %v", err)
	}
	glBal, _, err := rig.glRepo.AccountBalanceAsOf(rig.ctx, "2100", appliedAt.Add(30*time.Minute), branchTRT)
	if err != nil {
		t.Fatalf("AccountBalanceAsOf(2100): %v", err)
	}
	if pageOpen.TotalCents != 40000+7500 || pageOpen.TotalCents != -glBal {
		t.Fatalf("W2-9a r2b red: aging total = %d, gl 2100 credit = %d, want both %d (bill plus open landed cost)",
			pageOpen.TotalCents, -glBal, 40000+7500)
	}
	// From the void journal's instant the payable is gone.
	if got := landedOpenAt(voidedAt.Add(30 * time.Minute)); got != 0 {
		t.Fatalf("W2-9a r2b red: the landed cost counts %d cents as of an instant after its void, want 0", got)
	}
}
