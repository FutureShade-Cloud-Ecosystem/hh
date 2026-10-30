//go:build integration

package purchasing

import (
	"context"
	"testing"
	"time"
)

// TestIntegration_W3_RigJournalCleanupLeavesNothing proves the rig's own journal
// cleanup really cleans (W2-9a round 2, observation 2): after the wipe that runs
// at the end of a bill test, no GL journal is left standing for the bill, the
// post journal's or any payment's, so a shared database stops leaking journal
// rows across runs.
//
// The assertion is registered BEFORE the rig helper's cleanup, so t.Cleanup's
// LIFO order runs it AFTER the wipe: it reads exactly the state the rig leaves
// behind.
func TestIntegration_W3_RigJournalCleanupLeavesNothing(t *testing.T) {
	rig := newAPAgingRig(t)
	todayStr := time.Now().In(rig.zone).Format("2006-01-02")

	var billID string
	t.Cleanup(func() {
		if billID == "" {
			return
		}
		bg := context.Background()
		var leaked int
		var keys string
		if err := rig.pool.QueryRow(bg,
			`SELECT count(*), COALESCE(string_agg(idempotency_key, ', '), '')
			   FROM gl_journals WHERE source_ref_id = $1::uuid`, billID,
		).Scan(&leaked, &keys); err != nil {
			t.Errorf("the leak read: %v", err)
			return
		}
		if leaked != 0 {
			t.Errorf("W3 red: the rig's journal cleanup left %d journal row(s) for bill %s (%s): the wipe failed on the bill's own gl_journal_id reference and was swallowed",
				leaked, billID, keys)
		}
	})

	_, bill := rig.createReceivedPOAndPostBill(t, "w3-rig-wipe", 30000, todayStr, todayStr)
	billID = bill.ID

	// A payment too: its journal carries the bill as its source ref, so the wipe
	// has to take it with the post journal.
	if _, err := rig.svc.PayBill(rig.ctx, bill.ID, PayBillRequest{
		AmountCents:    5000,
		PaidAt:         time.Now().UTC(),
		Note:           "a payment the wipe must also take",
		IdempotencyKey: "w3-rig-wipe-" + bill.ID,
	}); err != nil {
		t.Fatalf("PayBill: %v", err)
	}
}
