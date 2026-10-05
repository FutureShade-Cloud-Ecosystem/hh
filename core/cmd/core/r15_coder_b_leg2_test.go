package main

// r15_coder_b_leg2_test.go is packet B's transient leg (design 5.3 leg 2):
// the same test-only trigger, raising SQLSTATE 40P01 only on its FIRST
// firing (a counter table). The ring must answer 201 THROUGH the bounded
// rerun: one charge, no void, the attempt BOUND, the sale and its payments
// row committed. On the tip (no rerun) the ring answers a bare 500 with one
// charge and no record: the captured money is reversed by nobody.

import (
	"context"
	"net/http"
	"testing"
)

// TestR15B_Leg2_TransientSerializationFailureRingsThroughTheBoundedRerun:
// one 40P01 on the first pos_sale insert, then the sale commits on the
// rerun, binding the already-captured leg (no void, no second charge).
func TestR15B_Leg2_TransientSerializationFailureRingsThroughTheBoundedRerun(t *testing.T) {
	host, seen := newR15RunHost(t)
	t.Setenv("APP_ENV", "dev")
	t.Setenv("HOS_CARD_PROCESSOR", "run")
	t.Setenv("HOS_CARD_BASE_URL", host.URL)

	pool := wiringPool(t)
	key := wiringRailDB(t, pool, map[string]string{"api_key": "r15-leg-not-a-secret"})
	w := plantWiringCounter(t, pool)
	r15BangFirstOnly(t, pool)
	_, mux, sg := wireFold(t, pool, key, w.branchID)

	saleKey := uuidKey(t)
	rec := postJSONBounded(t, mux, sg, "/api/v1/pos/sales", r15RingBody(w, saleKey, "tok_r15_leg2"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("the transient ring = %d (%s), want the committed 201 through the bounded rerun", rec.Code, rec.Body.String())
	}
	charges, voids, refunds := seen.counts()
	if charges != 1 || voids != 0 || refunds != 0 {
		t.Fatalf("charges=%d voids=%d refunds=%d, want one charge and no reversal", charges, voids, refunds)
	}
	var status string
	var bound int
	if err := pool.QueryRow(context.Background(), `
		SELECT a.status, (p.id IS NOT NULL)::int FROM card_charge_attempts a
		LEFT JOIN payments p ON p.id = a.payment_id WHERE a.leg_key = $1`, saleKey).Scan(&status, &bound); err != nil {
		t.Fatalf("read the attempt row: %v", err)
	}
	if status != "BOUND" || bound != 1 {
		t.Fatalf("the attempt is %s with payments row %d, want BOUND with its payments row", status, bound)
	}
	var sales int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pos_sale WHERE idempotency_key = $1`, saleKey).Scan(&sales); err != nil {
		t.Fatalf("count pos_sale: %v", err)
	}
	if sales != 1 {
		t.Fatalf("pos_sale rows for the key = %d, want exactly the one committed rerun", sales)
	}
}
