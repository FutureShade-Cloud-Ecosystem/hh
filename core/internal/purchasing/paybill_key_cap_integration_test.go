//go:build integration

package purchasing

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestIntegration_W3_PayBillKeyOver200IsRefused proves the contract's own
// 200 character bound is enforced and not only declared (PR686 round 4, the
// round 4 note): a key longer than 200 characters is refused with a 400 and
// writes nothing. Unenforced, a long key rode the journal until a key of a few
// thousand bytes died on the key index with a 500.
func TestIntegration_W3_PayBillKeyOver200IsRefused(t *testing.T) {
	rig := newAPAgingRig(t)
	todayStr := time.Now().In(rig.zone).Format("2006-01-02")

	_, bill := rig.createReceivedPOAndPostBill(t, "w3-key-cap", 40000, todayStr, todayStr)
	longKey := strings.Repeat("k", 201)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE idempotency_key = $1)`, "purchasing.bill.payment:"+longKey)
		_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journals WHERE idempotency_key = $1`, "purchasing.bill.payment:"+longKey)
	})

	_, err := rig.svc.PayBill(rig.ctx, bill.ID, PayBillRequest{
		AmountCents:    1000,
		PaidAt:         time.Now().UTC(),
		Note:           "a key over the bound",
		IdempotencyKey: longKey,
	})
	if err == nil {
		t.Fatalf("W3 red: a 201 character key was accepted, want a 400")
	}
	var ce clientError
	if !errors.As(err, &ce) || ce.status != http.StatusBadRequest {
		t.Fatalf("W3 red: a 201 character key = %v, want a 400 bad request", err)
	}

	// The refused press wrote nothing.
	var n int
	if err := rig.pool.QueryRow(rig.ctx,
		`SELECT count(*) FROM purchasing_bill_instalments WHERE bill_id = $1`, bill.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("W3 red: the refused press left %d instalment rows, want 0", n)
	}
	var jn int
	if err := rig.pool.QueryRow(rig.ctx,
		`SELECT count(*) FROM gl_journals WHERE idempotency_key = $1`, "purchasing.bill.payment:"+longKey).Scan(&jn); err != nil {
		t.Fatal(err)
	}
	if jn != 0 {
		t.Fatalf("W3 red: the refused press left %d journals under the key, want 0", jn)
	}

	// The bound is 200, inclusive: a key of exactly 200 characters pays.
	atBound := strings.Repeat("k", 200)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE idempotency_key = $1)`, "purchasing.bill.payment:"+atBound)
		_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journals WHERE idempotency_key = $1`, "purchasing.bill.payment:"+atBound)
	})
	if _, err := rig.svc.PayBill(rig.ctx, bill.ID, PayBillRequest{
		AmountCents:    1000,
		PaidAt:         time.Now().UTC(),
		Note:           "a key at the bound",
		IdempotencyKey: atBound,
	}); err != nil {
		t.Fatalf("W3 red: a 200 character key was refused: %v", err)
	}
}
