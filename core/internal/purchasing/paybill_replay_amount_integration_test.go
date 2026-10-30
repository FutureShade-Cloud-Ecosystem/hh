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

// TestIntegration_W3_PayBillReplayAmountMismatchIsAConflict proves the replay
// judgement answers a different amount with a conflict, never a silent first
// payment (PR686 round 2, P3 1): a key spent on this bill answers a re-press of
// the same amount with the first payment, and a press naming another amount is
// refused with nothing written, so a client bug cannot read as a successful
// payment it never made.
func TestIntegration_W3_PayBillReplayAmountMismatchIsAConflict(t *testing.T) {
	rig := newAPAgingRig(t)
	todayStr := time.Now().In(rig.zone).Format("2006-01-02")

	_, bill := rig.createReceivedPOAndPostBill(t, "w3-replay-amt", 40000, todayStr, todayStr)
	paymentKey := "w3-replay-amt-" + bill.ID
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE idempotency_key = $1)`, "purchasing.bill.payment:"+paymentKey)
		_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journals WHERE idempotency_key = $1`, "purchasing.bill.payment:"+paymentKey)
	})

	first, err := rig.svc.PayBill(rig.ctx, bill.ID, PayBillRequest{
		AmountCents:    1000,
		PaidAt:         time.Now().UTC(),
		Note:           "first press",
		IdempotencyKey: paymentKey,
	})
	if err != nil {
		t.Fatalf("first PayBill: %v", err)
	}
	if len(first.Instalments) != 1 || first.Instalments[0].AmountCents != 1000 {
		t.Fatalf("W3 red: first press instalments = %+v, want one 1000 cent instalment", first.Instalments)
	}

	// The same key pressed again for 2500 cents is a client bug: the key's payment
	// was 1000 cents, so the press is refused with a 409 that says both amounts,
	// and nothing is written.
	_, err = rig.svc.PayBill(rig.ctx, bill.ID, PayBillRequest{
		AmountCents:    2500,
		PaidAt:         time.Now().UTC(),
		Note:           "re-press with a different amount",
		IdempotencyKey: paymentKey,
	})
	if err == nil {
		t.Fatalf("W3 red: a replayed key with a different amount answered the first payment with no error")
	}
	var ce clientError
	if !errors.As(err, &ce) || ce.status != http.StatusConflict {
		t.Fatalf("W3 red: a replayed key with a different amount = %v, want a 409 conflict", err)
	}
	if !strings.Contains(ce.Error(), "1000") || !strings.Contains(ce.Error(), "2500") {
		t.Fatalf("W3 red: the conflict words do not name both amounts: %v", ce)
	}

	// The refused press wrote nothing: one instalment and one journal, the first
	// press's own.
	var n int
	if err := rig.pool.QueryRow(rig.ctx,
		`SELECT count(*) FROM purchasing_bill_instalments WHERE bill_id = $1`, bill.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("W3 red: the refused press left %d instalment rows, want 1", n)
	}
	var jn int
	if err := rig.pool.QueryRow(rig.ctx,
		`SELECT count(*) FROM gl_journals WHERE idempotency_key = $1`, "purchasing.bill.payment:"+paymentKey).Scan(&jn); err != nil {
		t.Fatal(err)
	}
	if jn != 1 {
		t.Fatalf("W3 red: the refused press left %d journals under the key, want 1", jn)
	}

	// An honest retry, the same amount, still replays: the first payment, and
	// nothing new.
	again, err := rig.svc.PayBill(rig.ctx, bill.ID, PayBillRequest{
		AmountCents:    1000,
		PaidAt:         time.Now().UTC(),
		Note:           "honest retry",
		IdempotencyKey: paymentKey,
	})
	if err != nil {
		t.Fatalf("honest retry PayBill: %v", err)
	}
	if len(again.Instalments) != 1 || again.Instalments[0].AmountCents != 1000 {
		t.Fatalf("W3 red: the honest retry instalments = %+v, want the first press's single 1000 cent instalment", again.Instalments)
	}
	if err := rig.pool.QueryRow(rig.ctx,
		`SELECT count(*) FROM purchasing_bill_instalments WHERE bill_id = $1`, bill.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("W3 red: the honest retry wrote a second instalment (%d rows), want 1", n)
	}
}
