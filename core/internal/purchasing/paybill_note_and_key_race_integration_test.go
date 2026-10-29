//go:build integration

package purchasing

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"
)

// TestIntegration_W29AR2c_PayBillWithoutANote pins S2's round 2 P1: the
// contract requires only amount_cents, so a payment with no note pays the bill.
func TestIntegration_W29AR2c_PayBillWithoutANote(t *testing.T) {
	rig := newAPAgingRig(t)
	todayStr := time.Now().In(rig.zone).Format("2006-01-02")
	_, bill := rig.createReceivedPOAndPostBill(t, "r2c-nonote", 40000, todayStr, todayStr)
	key := "w29ar2c-nonote-" + bill.ID
	t.Cleanup(func() { cleanupPaymentJournal(rig, key) })

	if _, err := rig.svc.PayBill(rig.ctx, bill.ID, PayBillRequest{AmountCents: 1000, IdempotencyKey: key}); err != nil {
		t.Fatalf("W2-9a r2c red: a payment with no note must pay the bill: %v", err)
	}
	var n int
	if err := rig.pool.QueryRow(rig.ctx, `SELECT count(*) FROM purchasing_bill_instalments WHERE bill_id = $1 AND status = 'PAID'`, bill.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("W2-9a r2c red: %d paid instalments, want 1", n)
	}
}

// TestIntegration_W29AR2c_OneKeyOnTwoBillsAtOnce pins S2's round 2 P2: one
// key pressed on two bills at the same moment records exactly one payment and
// posts exactly one journal; the other press is refused as a key spent on
// another bill, so the aging and GL 2100 never part.
func TestIntegration_W29AR2c_OneKeyOnTwoBillsAtOnce(t *testing.T) {
	rig := newAPAgingRig(t)
	todayStr := time.Now().In(rig.zone).Format("2006-01-02")
	_, billA := rig.createReceivedPOAndPostBill(t, "r2c-raceA", 40000, todayStr, todayStr)
	_, billB := rig.createReceivedPOAndPostBill(t, "r2c-raceB", 40000, todayStr, todayStr)
	key := "w29ar2c-race-" + billA.ID
	t.Cleanup(func() { cleanupPaymentJournal(rig, key) })

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, id := range []string{billA.ID, billB.ID} {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			_, errs[i] = rig.svc.PayBill(rig.ctx, id, PayBillRequest{AmountCents: 700, IdempotencyKey: key, Note: "race"})
		}(i, id)
	}
	wg.Wait()

	ok, refused := 0, 0
	for _, err := range errs {
		var ce clientError
		switch {
		case err == nil:
			ok++
		case errors.As(err, &ce) && ce.status == http.StatusConflict:
			refused++
		default:
			t.Fatalf("W2-9a r2c red: unexpected error from a raced press: %v", err)
		}
	}
	if ok != 1 || refused != 1 {
		t.Fatalf("W2-9a r2c red: %d presses paid and %d were refused, want 1 and 1", ok, refused)
	}
	var paid, journals int
	if err := rig.pool.QueryRow(rig.ctx, `SELECT count(*) FROM purchasing_bill_instalments WHERE bill_id = ANY($1::uuid[]) AND status = 'PAID'`, []string{billA.ID, billB.ID}).Scan(&paid); err != nil {
		t.Fatal(err)
	}
	if err := rig.pool.QueryRow(rig.ctx, `SELECT count(*) FROM gl_journals WHERE idempotency_key = $1`, glPaymentKeyPrefix+key).Scan(&journals); err != nil {
		t.Fatal(err)
	}
	if paid != 1 || journals != 1 {
		t.Fatalf("W2-9a r2c red: %d paid instalments and %d journals for one key, want 1 and 1", paid, journals)
	}
}

func cleanupPaymentJournal(rig *apAgingTestRig, key string) {
	bg := context.Background()
	_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE idempotency_key = $1)`, glPaymentKeyPrefix+key)
	_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journals WHERE idempotency_key = $1`, glPaymentKeyPrefix+key)
}
