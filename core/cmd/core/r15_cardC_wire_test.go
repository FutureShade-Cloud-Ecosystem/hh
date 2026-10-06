package main

// r15_cardC_wire_test.go is the crash windows' fifth leg, the wire answers (the card charge
// design section 5.3 leg 6, the same-key ring's shape after the sweep has moved a CHARGING row
// to UNKNOWN). The leg plants a CHARGING attempt for the next ring's sale key, lets the sweep
// move it past grace with no processor call, and then rings the REGISTERED sale route twice: the
// same key answers 409 card_charge_needs_manager (an unknown outcome is a manager item, never a
// second charge), and a fresh key at the same amount on the same till answers 409
// card_charge_unresolved_on_till (the till guard holds the till for a person). The host's charge
// count does not move across all of it.
//
// Red on the packet B tip: no sweep exists to move the row, and with the entry point stubbed the
// row stays CHARGING, so the same-key ring answers 409 card_charge_in_flight instead of the
// manager's word, which is the leg's own red shape. Gated on HH_CORE_TEST_DATABASE_URL like the
// other composition suites; each run names its own database on the command.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
)

// TestR15CardCWire_UnknownOutcomeRingsAnswerTheManagerAndHoldTheTill runs the sweep's move and
// the two rings against the stand-in host.
func TestR15CardCWire_UnknownOutcomeRingsAnswerTheManagerAndHoldTheTill(t *testing.T) {
	host, seen := newWiringRunHost(t)
	t.Setenv("APP_ENV", "dev")
	t.Setenv("HOS_CARD_PROCESSOR", "run")
	t.Setenv("HOS_CARD_BASE_URL", host.URL)

	pool := wiringPool(t)
	key := wiringRailDB(t, pool, map[string]string{"api_key": "r15-cardc-not-a-secret"})
	w := plantWiringCounter(t, pool)
	tops, mux, sg := wireFold(t, pool, key, w.branchID)

	// The probe sale names the exact total the planted row and both rings must carry.
	probe := postJSON(t, mux, sg, "/api/v1/pos/sales", map[string]any{
		"idempotency_key": uuidKey(t),
		"branch_id":       w.branchID,
		"till_session_id": w.sessionID,
		"sale_type":       "COLLECT_LATER",
		"tender":          "CARD",
		"payment_token":   "tok_r15_cardc_probe",
		"card_type":       "CREDIT",
		"offline":         false,
		"lines":           []map[string]any{{"product_id": w.productID, "qty_entered": 2.0, "uom_entered": w.uom, "cost_cents": 0}},
	})
	if probe.Code != http.StatusCreated {
		t.Fatalf("probe sale = %d (%s), want 201", probe.Code, probe.Body.String())
	}
	var probeSale struct {
		TotalCents int64 `json:"total_cents"`
	}
	if err := json.Unmarshal(probe.Body.Bytes(), &probeSale); err != nil {
		t.Fatalf("decode probe sale: %v", err)
	}
	total := probeSale.TotalCents
	if total <= 0 {
		t.Fatalf("probe sale total %d, want a priced ring", total)
	}
	charges, _ := seen.hits()
	if charges != 1 {
		t.Fatalf("the probe made %d charge request(s), want one", charges)
	}

	// The crash window's row: claimed for the next ring's sale key (a single tender's leg key IS
	// the sale key), left CHARGING by a process that died before its answer was stamped.
	saleKey := uuidKey(t)
	vault, err := payment.NewVault(key)
	if err != nil {
		t.Fatalf("leg vault: %v", err)
	}
	store := payment.NewStore(pool, vault)
	if _, created, err := store.Claim(context.Background(), payment.ClaimInput{
		LegKey: saleKey, SaleKey: saleKey, LegSeq: 1, AmountCents: total,
		BranchID: w.branchID, TillSessionID: w.sessionID, CardType: "CREDIT", Processor: "run_payments",
	}); err != nil || !created {
		t.Fatalf("plant the crash window's claim: created=%v err=%v", created, err)
	}

	// The sweep, on a clock past the grace: the row goes UNKNOWN with no processor call.
	sres, err := tops.cardRail.SweepCardAttempts(context.Background(),
		func() time.Time { return time.Now().Add(2 * payment.AttemptGrace) })
	if err != nil {
		t.Fatalf("the sweep: %v", err)
	}
	if sres.Examined != 1 || sres.ToUnknown != 1 {
		t.Fatalf("the sweep's pass = %+v, want the crash window's row moved to UNKNOWN", sres)
	}
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM card_charge_attempts WHERE leg_key = $1`, saleKey).Scan(&status); err != nil {
		t.Fatalf("read the attempt: %v", err)
	}
	if status != "UNKNOWN" {
		t.Fatalf("the crash window's row = %s, want UNKNOWN: the sweep moves a CHARGING row past grace with no processor call", status)
	}
	if charges, _ = seen.hits(); charges != 1 {
		t.Fatalf("after the sweep the host saw %d charge request(s), want still the probe's one: there is no ref to void and no lookup", charges)
	}

	// The same key rings again: the row's answer IS the answer, 409 for a manager, no call.
	body := func(idem string) map[string]any {
		return map[string]any{
			"idempotency_key": idem,
			"branch_id":       w.branchID,
			"till_session_id": w.sessionID,
			"sale_type":       "COLLECT_LATER",
			"tender":          "CARD",
			"payment_token":   "tok_r15_cardc_rerings",
			"card_type":       "CREDIT",
			"offline":         false,
			"lines":           []map[string]any{{"product_id": w.productID, "qty_entered": 2.0, "uom_entered": w.uom, "cost_cents": 0}},
		}
	}
	rec := postJSON(t, mux, sg, "/api/v1/pos/sales", body(saleKey))
	if rec.Code != http.StatusConflict {
		t.Fatalf("the same-key ring = %d (%s), want 409", rec.Code, rec.Body.String())
	}
	var word struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &word); err != nil {
		t.Fatalf("decode the same-key ring's answer: %v", err)
	}
	if word.Error != "card_charge_needs_manager" {
		t.Fatalf("the same-key ring's word = %q, want card_charge_needs_manager", word.Error)
	}
	if charges, _ = seen.hits(); charges != 1 {
		t.Fatalf("after the same-key ring the host saw %d charge request(s), want still one: a row answer never calls", charges)
	}

	// A fresh key at the same amount on the same till: the till guard holds the till.
	rec = postJSON(t, mux, sg, "/api/v1/pos/sales", body(uuidKey(t)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("the fresh-key ring = %d (%s), want 409", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &word); err != nil {
		t.Fatalf("decode the fresh-key ring's answer: %v", err)
	}
	if word.Error != "card_charge_unresolved_on_till" {
		t.Fatalf("the fresh-key ring's word = %q, want card_charge_unresolved_on_till", word.Error)
	}
	if charges, _ = seen.hits(); charges != 1 {
		t.Fatalf("after the fresh-key ring the host saw %d charge request(s), want still one: the charge count does not move", charges)
	}
	var fresh int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM card_charge_attempts WHERE till_session_id = $1::uuid AND leg_key <> $2 AND status <> 'BOUND'`,
		w.sessionID, saleKey).Scan(&fresh); err != nil {
		t.Fatalf("count the fresh rows: %v", err)
	}
	if fresh != 0 {
		t.Fatalf("the fresh-key ring left %d unresolved attempt row(s), want none: the guard refuses before any claim (the probe's own row is BOUND and stays)", fresh)
	}
}
