//go:build integration

package purchasing

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// S6 probe (reader only, not for commit): two applications of ONE credit onto two DIFFERENT bills,
// under two different keys, at the same moment. Each press locks the credit row, but reads the
// credit's applied sum in the same statement as the lock, on a snapshot taken before the wait.
func TestIntegration_W3VC_ConcurrentApplyNeverOverAppliesCredit(t *testing.T) {
	rig := newVCRig(t)
	today := time.Now().In(rig.zone).Format("2006-01-02")
	var overApplied, rounds int
	for i := 0; i < 15; i++ {
		_, billA := rig.createReceivedPOAndPostBill(t, fmt.Sprintf("s6a%d", i), 10000, today, today)
		_, billB := rig.createReceivedPOAndPostBill(t, fmt.Sprintf("s6b%d", i), 10000, today, today)
		credit := rig.createPostedCredit(t, fmt.Sprintf("s6c%d", i), branchTRT, 10000)
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for j, bill := range []string{billA.ID, billB.ID} {
			wg.Add(1)
			go func(j int, bill string) {
				defer wg.Done()
				<-start
				_, errs[j] = rig.svc.ApplyVendorCredit(rig.ctx, credit.ID, ApplyVendorCreditRequest{
					BillID: bill, AmountCents: 10000, IdempotencyKey: fmt.Sprintf("s6-%d-%d-%d", time.Now().UnixNano(), i, j),
				})
			}(j, bill)
		}
		close(start)
		wg.Wait()
		var applied int64
		if err := rig.pool.QueryRow(rig.ctx, `SELECT COALESCE(SUM(amount_cents),0) FROM purchasing_vendor_credit_applications WHERE credit_id = $1`, credit.ID).Scan(&applied); err != nil {
			t.Fatal(err)
		}
		rounds++
		if applied > 10000 {
			overApplied++
			t.Logf("round %d: a 10000 cent credit is applied %d cents (errs %v / %v)", i, applied, errs[0], errs[1])
		}
	}
	t.Logf("%d of %d rounds over-applied the credit", overApplied, rounds)
	if overApplied > 0 {
		t.Errorf("a credit was applied above its total in %d of %d concurrent rounds", overApplied, rounds)
	}
}
