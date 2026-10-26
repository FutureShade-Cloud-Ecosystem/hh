//go:build integration

package payments_test

// void_gl_integration_test.go, FW1-06b: a true void for a mis-keyed receipt.
//
// AR-03: the only undo a desk receipt has today is the reversal, and a reversal always books the
// money as held for the customer (Dr AR / Cr 2310), whatever the screen labels it. A receipt keyed
// against the wrong figures on the day it was taken needs the act that undoes the receipt itself:
// its allocation and its journal, with the money never having left the building, so the undo leg is
// the receipt's own leg credited back (1020 undeposited, or 1010 card clearing) and 2310 never
// moves. The red here is the journal: no GL builder takes payment.voided today, so the outbox row
// dies and the books keep the receipt.

import (
	"context"
	"testing"
)

// The reversal, the same-day undo the desk has today, books the amount into 2310: this leg pins the
// defect the void replaces, and it must stay true after, because a reversal keeps its own meaning
// for money that really was taken back.
func TestIntegration_PaymentVoid_TodaysSameDayUndoHoldsTheMoneyIn2310(t *testing.T) {
	w := newReversalGLWorld(t)
	orig := w.receipt("CHEQUE", 10_000)
	w.drain()
	rev := w.reverse(orig)
	keys := caseKeys(orig, rev)
	w.cleanupJournals(keys, nil)
	w.drain()
	got := w.nets(keys, nil)
	if got["2310"] != -10_000 {
		t.Fatalf("the same day undo books 2310 at %d, want -10000: the reversal holds the amount for the customer", got["2310"])
	}
}

func TestIntegration_PaymentVoid_TheGLBooksTheUndoOnTheReceiptsOwnLeg(t *testing.T) {
	w := newReversalGLWorld(t)
	orig := w.receipt("CHEQUE", 10_000) // Dr 1020 / Cr 1100 posted at the receipt
	w.drain()
	// The void's outbox row, emitted with the payment facts the act reads under its row lock.
	if _, err := w.r.obs.Exec(context.Background(),
		`INSERT INTO events_outbox (topic, payload) VALUES ('payment.voided', jsonb_build_object(
			 'payment_id', $1::uuid, 'customer_id', $2::uuid, 'branch_id', $3::uuid,
			 'method', 'CHEQUE', 'amount_cents', 10000, 'reason', 'keyed against the wrong figures'))`,
		orig, w.r.custID, w.r.branchID); err != nil {
		t.Fatalf("seed the void row: %v", err)
	}
	keys := []string{"payment.voided:" + orig}
	w.cleanupJournals(keys, nil)
	w.drain()
	var rows, dead int
	if err := w.r.obs.QueryRow(context.Background(),
		`SELECT count(*), count(*) FILTER (WHERE status = 'DEAD') FROM events_outbox
		  WHERE topic = 'payment.voided' AND payload->>'payment_id' = $1`, orig).Scan(&rows, &dead); err != nil {
		t.Fatalf("read the void row: %v", err)
	}
	if rows != 1 || dead != 0 {
		t.Fatalf("the payment.voided outbox row is %d row(s), %d dead, want one taken row: "+
			"no GL builder takes the topic, so the void's journal cannot exist", rows, dead)
	}
	got := w.nets(keys, nil)
	for code, want := range map[string]int64{"1020": 0, "1100": 0, "2310": 0} {
		if got[code] != want {
			t.Errorf("account %s nets %d over the receipt and its void, want %d: the void undoes the receipt on the "+
				"receipt's own leg and 2310 never moves", code, got[code], want)
		}
	}
}
