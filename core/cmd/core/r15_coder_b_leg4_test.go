package main

// r15_coder_b_leg4_test.go is packet B's split decline leg (design 5.3 leg
// 4, section 3.6): two CARD legs, the stand-in answers C for the second
// leg's token. The ring must answer 402 with two charges (one approved),
// one void on leg 1's trans_id, the attempts VOIDED and DECLINED, and no
// order, pos_sale or payments row behind the key. A same-key retry must
// answer 402 again with no new charge. On the tip the ring answers a bare
// 500 with two charges, no void and no record: leg 1's capture is orphaned.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// TestR15B_Leg4_TheSplitDeclineReversesTheApprovedLegAndAnswers402.
func TestR15B_Leg4_TheSplitDeclineReversesTheApprovedLegAndAnswers402(t *testing.T) {
	host, seen := newR15RunHost(t)
	seen.declineToken = "tok_r15_leg4b"
	t.Setenv("APP_ENV", "dev")
	t.Setenv("HOS_CARD_PROCESSOR", "run")
	t.Setenv("HOS_CARD_BASE_URL", host.URL)

	pool := wiringPool(t)
	key := wiringRailDB(t, pool, map[string]string{"api_key": "r15-leg-not-a-secret"})
	w := plantWiringCounter(t, pool)
	_, mux, sg := wireFold(t, pool, key, w.branchID)

	// Learn the ring's exact total first with a throwaway CASH sale (no
	// processor call): the split's legs must sum to it EXACTLY.
	probe := postJSONBounded(t, mux, sg, "/api/v1/pos/sales", map[string]any{
		"idempotency_key": uuidKey(t),
		"branch_id":       w.branchID,
		"till_session_id": w.sessionID,
		"sale_type":       "COLLECT_LATER",
		"tender":          "CASH",
		"offline":         false,
		"lines":           []map[string]any{{"product_id": w.productID, "qty_entered": 1.0, "uom_entered": w.uom, "cost_cents": 0}},
	})
	if probe.Code != http.StatusCreated {
		t.Fatalf("the probe cash sale = %d (%s), want 201", probe.Code, probe.Body.String())
	}
	var probeSale struct {
		TotalCents int64 `json:"total_cents"`
	}
	if err := json.Unmarshal(probe.Body.Bytes(), &probeSale); err != nil {
		t.Fatalf("decode the probe sale: %v", err)
	}
	half := probeSale.TotalCents / 2

	saleKey := uuidKey(t)
	splitBody := map[string]any{
		"idempotency_key": saleKey,
		"branch_id":       w.branchID,
		"till_session_id": w.sessionID,
		"sale_type":       "COLLECT_LATER",
		"offline":         false,
		"lines":           []map[string]any{{"product_id": w.productID, "qty_entered": 1.0, "uom_entered": w.uom, "cost_cents": 0}},
		"tenders": []map[string]any{
			{"method": "CARD", "amount_cents": half, "payment_token": "tok_r15_leg4a", "card_type": "CREDIT"},
			{"method": "CARD", "amount_cents": probeSale.TotalCents - half, "payment_token": "tok_r15_leg4b", "card_type": "CREDIT"},
		},
	}

	// Ring 1: 402, the approved leg reversed.
	rec := postJSONBounded(t, mux, sg, "/api/v1/pos/sales", splitBody)
	if rec.Code != http.StatusPaymentRequired || !jsonErrWord(rec.Body.String(), "card_charge_card_declined") {
		t.Fatalf("the split decline ring = %d (%s), want 402 card_charge_card_declined", rec.Code, rec.Body.String())
	}
	charges, voids, refunds := seen.counts()
	if charges != 2 || voids != 1 || refunds != 0 {
		t.Fatalf("charges=%d voids=%d refunds=%d, want two charges (one approved) and one void", charges, voids, refunds)
	}
	seen.mu.Lock()
	voidTarget := seen.voidTrans[0]
	firstCharge := seen.chargeTrans[0]
	seen.mu.Unlock()
	if voidTarget != firstCharge {
		t.Fatalf("the void reversed trans_id %q, want leg 1's charge %q", voidTarget, firstCharge)
	}
	var leg1Status, leg2Status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM card_charge_attempts WHERE sale_key = $1 AND leg_seq = 1`, saleKey).Scan(&leg1Status); err != nil {
		t.Fatalf("read leg 1's attempt: %v", err)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM card_charge_attempts WHERE sale_key = $1 AND leg_seq = 2`, saleKey).Scan(&leg2Status); err != nil {
		t.Fatalf("read leg 2's attempt: %v", err)
	}
	if leg1Status != "VOIDED" || leg2Status != "DECLINED" {
		t.Fatalf("the attempts are %s and %s, want VOIDED and DECLINED", leg1Status, leg2Status)
	}
	var rows int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pos_sale WHERE idempotency_key = $1`, saleKey).Scan(&rows); err != nil {
		t.Fatalf("count pos_sale: %v", err)
	}
	if rows != 0 {
		t.Fatalf("the declined split left %d pos_sale rows, want none", rows)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM payments p JOIN card_charge_attempts a ON a.payment_id = p.id
		 WHERE a.sale_key = $1`, saleKey).Scan(&rows); err != nil {
		t.Fatalf("count the split's payments rows: %v", err)
	}
	if rows != 0 {
		t.Fatalf("the declined split left %d payments rows, want none", rows)
	}

	// Same-key retry: 402 again, from the rows, with no new charge.
	rec2 := postJSONBounded(t, mux, sg, "/api/v1/pos/sales", splitBody)
	if rec2.Code != http.StatusPaymentRequired || !jsonErrWord(rec2.Body.String(), "card_charge_card_declined") {
		t.Fatalf("the same-key retry = %d (%s), want the row's 402 card_charge_card_declined", rec2.Code, rec2.Body.String())
	}
	charges, voids, _ = seen.counts()
	if charges != 2 || voids != 1 {
		t.Fatalf("the retry moved the processor: charges=%d voids=%d, want 2 and 1", charges, voids)
	}
}

// jsonErrWord reports whether the response body's error field carries the
// given word.
func jsonErrWord(body, word string) bool {
	var env struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		return false
	}
	return env.Error == word || len(env.Error) >= len(word) && env.Error[:len(word)] == word
}
