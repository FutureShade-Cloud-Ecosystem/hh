//go:build integration

package purchasing

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// TestIntegration_W29AR2_PayBillKeyAndDate proves the round 2 payment contract:
//  1. the idempotency key is required (a 400 without it) and a re-press under the
//     same key returns the first payment and writes nothing (no instalment, no journal);
//  2. the GL journal carries the key as purchasing.bill.payment:<key> and its
//     DocumentDate is the payment's own instant, the instalment's paid_at the same instant;
//  3. a bill paid at T reads open as of any instant before T and paid as of any
//     instant after T (a paid bill is never shown open past its payment).
func TestIntegration_W29AR2_PayBillKeyAndDate(t *testing.T) {
	rig := newAPAgingRig(t)
	reach := BranchReach{BranchIDs: []string{branchTRT}}
	todayStr := time.Now().In(rig.zone).Format("2006-01-02")

	_, bill := rig.createReceivedPOAndPostBill(t, "r2-keydate", 40000, todayStr, todayStr)
	paymentKey := "w29ar2-pay-" + bill.ID
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE idempotency_key = $1)`, "purchasing.bill.payment:"+paymentKey)
		_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journals WHERE idempotency_key = $1`, "purchasing.bill.payment:"+paymentKey)
	})

	// 1. The key is required: a 400 without it, and nothing written.
	if _, err := rig.svc.PayBill(rig.ctx, bill.ID, PayBillRequest{
		AmountCents: 12500,
		PaidAt:      time.Now().UTC(),
		Note:        "no key",
	}); err == nil {
		t.Fatalf("W2-9a r2 red: PayBill without an idempotency key must be refused")
	} else {
		var ce clientError
		if !errors.As(err, &ce) || ce.status != http.StatusBadRequest {
			t.Fatalf("W2-9a r2 red: PayBill without an idempotency key = %v, want a 400 bad request", err)
		}
	}
	var n int
	if err := rig.pool.QueryRow(rig.ctx,
		`SELECT count(*) FROM purchasing_bill_instalments WHERE bill_id = $1`, bill.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("W2-9a r2 red: a keyless refusal wrote %d instalment rows, want 0", n)
	}

	// 2. First press: 12500 cents paid at T1.
	t1 := time.Now().UTC().Add(2 * time.Second)
	first, err := rig.svc.PayBill(rig.ctx, bill.ID, PayBillRequest{
		AmountCents:    12500,
		PaidAt:         t1,
		Note:           "first press",
		IdempotencyKey: paymentKey,
	})
	if err != nil {
		t.Fatalf("first PayBill: %v", err)
	}
	if len(first.Instalments) != 1 || first.Instalments[0].AmountCents != 12500 {
		t.Fatalf("W2-9a r2 red: first press instalments = %+v, want one 12500 cent instalment", first.Instalments)
	}

	// The instalment's paid_at is the payment's own instant, and the journal carries
	// the caller's key with DocumentDate the same instant.
	var paidAt time.Time
	if err := rig.pool.QueryRow(rig.ctx,
		`SELECT paid_at FROM purchasing_bill_instalments WHERE bill_id = $1 AND status = 'PAID'`, bill.ID,
	).Scan(&paidAt); err != nil {
		t.Fatal(err)
	}
	if !paidAt.Truncate(time.Microsecond).Equal(t1.Truncate(time.Microsecond)) {
		t.Fatalf("W2-9a r2 red: instalment paid_at = %v, want the payment's own instant %v", paidAt, t1)
	}
	var journalDate time.Time
	var journalRef string
	if err := rig.pool.QueryRow(rig.ctx,
		// The GL files an externally posted journal by its DocumentDate: for a POSTED
		// journal that instant is the journal's posted_at.
		`SELECT posted_at, COALESCE(source_ref_id::text, '') FROM gl_journals WHERE idempotency_key = $1`,
		"purchasing.bill.payment:"+paymentKey,
	).Scan(&journalDate, &journalRef); err != nil {
		t.Fatalf("W2-9a r2 red: no GL journal under purchasing.bill.payment:%s: %v", paymentKey, err)
	}
	if !journalDate.Truncate(time.Microsecond).Equal(t1.Truncate(time.Microsecond)) {
		t.Fatalf("W2-9a r2 red: journal document_date = %v, want the payment's own instant %v", journalDate, t1)
	}
	if journalRef != bill.ID {
		t.Fatalf("W2-9a r2 red: journal source_ref_id = %q, want the bill %s", journalRef, bill.ID)
	}

	// 3. The re-press returns the first payment and writes nothing.
	again, err := rig.svc.PayBill(rig.ctx, bill.ID, PayBillRequest{
		AmountCents:    12500,
		PaidAt:         time.Now().UTC(),
		Note:           "second press",
		IdempotencyKey: paymentKey,
	})
	if err != nil {
		t.Fatalf("re-press PayBill: %v", err)
	}
	if len(again.Instalments) != 1 || again.Instalments[0].AmountCents != 12500 {
		t.Fatalf("W2-9a r2 red: re-press instalments = %+v, want the first press's single 12500 cent instalment", again.Instalments)
	}
	if err := rig.pool.QueryRow(rig.ctx,
		`SELECT count(*) FROM purchasing_bill_instalments WHERE bill_id = $1`, bill.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("W2-9a r2 red: the re-press wrote a second instalment (%d rows), want 1", n)
	}
	var jn int
	if err := rig.pool.QueryRow(rig.ctx,
		`SELECT count(*) FROM gl_journals WHERE idempotency_key = $1`, "purchasing.bill.payment:"+paymentKey).Scan(&jn); err != nil {
		t.Fatal(err)
	}
	if jn != 1 {
		t.Fatalf("W2-9a r2 red: the re-press wrote a second journal (%d), want 1", jn)
	}

	// 4. As of any instant before the payment the bill reads open at 40000; as of any
	// instant after it, only 27500 of the bill remains open. A paid bill is never
	// shown open past its payment.
	pageBefore, err := rig.svc.APAging(rig.ctx, t1.Add(-time.Minute), reach)
	if err != nil {
		t.Fatalf("APAging before payment: %v", err)
	}
	openBefore := openCentsForBill(pageBefore, bill.ID)
	if openBefore != 40000 {
		t.Fatalf("W2-9a r2 red: bill open before its payment = %d, want 40000", openBefore)
	}
	pageAfter, err := rig.svc.APAging(rig.ctx, t1.Add(time.Minute), reach)
	if err != nil {
		t.Fatalf("APAging after payment: %v", err)
	}
	openAfter := openCentsForBill(pageAfter, bill.ID)
	if openAfter != 27500 {
		t.Fatalf("W2-9a r2 red: bill open as of an instant after its payment = %d, want 27500 (a paid bill is not shown open)", openAfter)
	}

	// The tie holds across the payment at the after instant: aging equals GL 2100's net credit.
	glBal, _, err := rig.glRepo.AccountBalanceAsOf(rig.ctx, "2100", t1.Add(time.Minute), branchTRT)
	if err != nil {
		t.Fatalf("AccountBalanceAsOf(2100): %v", err)
	}
	if pageAfter.TotalCents != -glBal {
		t.Fatalf("W2-9a r2 red: aging total=%d gl_2100_credit_cents=%d after the payment, want a tie", pageAfter.TotalCents, -glBal)
	}
}

// openCentsForBill returns the open cents the aging page shows for billID, 0 when absent.
func openCentsForBill(page APAgingPage, billID string) int64 {
	for _, row := range page.Items {
		if row.IsLandedCost {
			continue
		}
		for _, item := range row.OpenItems {
			if item.ID == billID {
				return item.OpenCents
			}
		}
	}
	return 0
}
