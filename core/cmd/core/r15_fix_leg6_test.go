package main

// r15_fix_leg6_test.go is the card B fix round's red leg 6 (the GLM review's P2 on packet B,
// ruled into this fix round at decision log row 860), in two phases, one per unclassified arm
// of the capture loop:
//
// Phase A, the invalid charge. A split with two CARD legs rings through the REGISTERED sale
// route; the second leg carries an EMPTY payment token, so its capture is refused before the
// claim with the payment module's invalid-charge word (errBadCharge). On the tip that word is
// untyped on the till-facing side (the adapter translated the seven typed sentinels and not
// this one), so the raw error reached the capture loop's default arm and rode straight back to
// Sale with no settlement. On the fix the adapter crosses it as the seam's not-dispatched
// answer whose text names card_charge_invalid_token (nothing was claimed, nothing was
// dispatched), settleFailedCardRing compensates the captured leg, and the cashier's answer is
// the typed 503 carrying the invalid-charge cause, with leg 1 VOIDED and both refs on the row.
//
// Phase B, the mid-ring till refusal. A second split rings while the stand-in host closes the
// till session as it answers the ring's first leg's charge, so leg 2's own CaptureCard re-check
// refuses with the till's 409 client error, a till-typed word pos cannot classify. On the tip
// the raw error answered the bare 500 with leg 1's money held. On the fix the loop's default
// arm ranks the ring manager (the design's 3.5: an unclassified error is an unknown outcome),
// returns the ring, and the compensator reverses leg 1: the cashier's answer is the typed 409
// card_charge_needs_manager.
//
// On the tip before any fix the leg fails in phase A's shape: the bare 500 with one charge and
// no void. The two fix bites each re-derive it in its own shape: reverting the default-arm
// compensation fails phase B (the mid-ring refusal rides raw to the bare 500), and reverting
// the invalid-charge map fails phase A (the charge is compensated but the word is the manager's
// 409 instead of the invalid charge's). Gated on HH_CORE_TEST_DATABASE_URL like the other
// composition suites; each run names its own throwaway database on the command.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// leg6RunHost is the stand-in Run Payments host the phase A and B rings share: every charge and
// every reversal is approved and counted, and on the third charge request of the run (the probe
// is the first, phase A's captured leg the second) it closes the leg's till session before
// answering, the mid-ring session loss phase B rings against.
type leg6RunHost struct {
	mu        sync.Mutex
	charges   int
	voids     int
	pool      *pgxpool.Pool
	sessionID string
}

func (h *leg6RunHost) counts() (int, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.charges, h.voids
}

func newLeg6RunHost(t *testing.T, pool *pgxpool.Pool, sessionID string) (*httptest.Server, *leg6RunHost) {
	t.Helper()
	h := &leg6RunHost{pool: pool, sessionID: sessionID}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/charge", func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.charges++
		n := h.charges
		h.mu.Unlock()
		if n == 3 {
			// The mid-ring session loss: the till's own per-leg re-check refuses leg 2.
			if _, err := pool.Exec(r.Context(),
				`UPDATE till_sessions SET status='CLOSED' WHERE id=$1 AND status='OPEN'`, sessionID); err != nil {
				t.Logf("leg6 host: close the session: %v", err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"result": "A", "resp_code": "00", "resp_text": "APPROVED",
			"trans_id": "run_leg6_charge", "card_type": "CREDIT",
		})
	})
	mux.HandleFunc("/api/v1/void-or-refund", func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.voids++
		h.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"result": "A", "resp_code": "00", "resp_text": "APPROVED",
			"trans_id": "run_leg6_void",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, h
}

func TestR15FixLeg6_MidRingUnclassifiedArmsCompensateTheCapturedLeg(t *testing.T) {
	pool := wiringPool(t)
	t.Setenv("APP_ENV", "dev")
	t.Setenv("HOS_CARD_PROCESSOR", "run")

	key := wiringRailDB(t, pool, map[string]string{"api_key": "r15-fix-leg6-not-a-secret"})
	w := plantWiringCounter(t, pool)
	hostSrv, host := newLeg6RunHost(t, pool, w.sessionID)
	t.Setenv("HOS_CARD_BASE_URL", hostSrv.URL)
	_, mux, sg := wireFold(t, pool, key, w.branchID)

	// The probe sale: one CARD leg with a token, committed, whose response names the exact
	// total the two splits below must settle (the split's sum check is exact). It is also the
	// host's first charge, the count the phase B hook arms on.
	probe := postJSON(t, mux, sg, "/api/v1/pos/sales", map[string]any{
		"idempotency_key": uuidKey(t),
		"branch_id":       w.branchID,
		"till_session_id": w.sessionID,
		"sale_type":       "COLLECT_LATER",
		"tender":          "CARD",
		"payment_token":   "tok_r15_fix_leg6_probe",
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
	if total <= 200 {
		t.Fatalf("probe sale total %d is too small to split two positive card legs", total)
	}

	// -- Phase A: the mid-ring invalid charge. --

	chargeBefore, voidBefore := host.counts()
	saleA := uuidKey(t)
	rec := postJSON(t, mux, sg, "/api/v1/pos/sales", map[string]any{
		"idempotency_key": saleA,
		"branch_id":       w.branchID,
		"till_session_id": w.sessionID,
		"sale_type":       "COLLECT_LATER",
		"offline":         false,
		"lines":           []map[string]any{{"product_id": w.productID, "qty_entered": 2.0, "uom_entered": w.uom, "cost_cents": 0}},
		"tenders": []map[string]any{
			{"method": "CARD", "amount_cents": total - 100, "payment_token": "tok_r15_fix_leg6_first", "card_type": "CREDIT"},
			{"method": "CARD", "amount_cents": int64(100), "payment_token": "", "card_type": "CREDIT"},
		},
	})
	if rec.Code == http.StatusInternalServerError {
		charges, voids := host.counts()
		t.Fatalf("the mid-ring invalid charge answered the bare 500 (%s): the ring charged %d leg(s) and voided %d", rec.Body.String(), charges-chargeBefore, voids-voidBefore)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("phase A's split = %d (%s), want the typed 503", rec.Code, rec.Body.String())
	}
	var bodyA struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &bodyA); err != nil {
		t.Fatalf("decode phase A's answer: %v", err)
	}
	if !strings.Contains(bodyA.Error, "card_charge_invalid_token") {
		t.Fatalf("the cashier's answer does not name the invalid charge: %q", bodyA.Error)
	}
	charges, voids := host.counts()
	if charges-chargeBefore != 1 {
		t.Fatalf("phase A charged %d leg(s), want exactly leg 1's", charges-chargeBefore)
	}
	if voids-voidBefore != 1 {
		t.Fatalf("after phase A's failed ring the host saw %d void(s), want the captured leg reversed", voids-voidBefore)
	}
	pinLeg6Reversal(t, pool, saleA)

	// -- Phase B: the mid-ring till refusal (the host hook closes the session as it answers
	// this ring's first leg's charge, so leg 2's own re-check refuses with the till's
	// unmapped 409). --

	chargeBefore, voidBefore = host.counts()
	saleB := uuidKey(t)
	rec = postJSON(t, mux, sg, "/api/v1/pos/sales", map[string]any{
		"idempotency_key": saleB,
		"branch_id":       w.branchID,
		"till_session_id": w.sessionID,
		"sale_type":       "COLLECT_LATER",
		"offline":         false,
		"lines":           []map[string]any{{"product_id": w.productID, "qty_entered": 2.0, "uom_entered": w.uom, "cost_cents": 0}},
		"tenders": []map[string]any{
			{"method": "CARD", "amount_cents": total - 100, "payment_token": "tok_r15_fix_leg6_second", "card_type": "CREDIT"},
			{"method": "CARD", "amount_cents": int64(100), "payment_token": "tok_r15_fix_leg6_third", "card_type": "CREDIT"},
		},
	})
	if rec.Code == http.StatusInternalServerError {
		charges, voids := host.counts()
		t.Fatalf("the mid-ring till refusal answered the bare 500 (%s): the ring charged %d leg(s) and voided %d", rec.Body.String(), charges-chargeBefore, voids-voidBefore)
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("phase B's split = %d (%s), want the typed 409", rec.Code, rec.Body.String())
	}
	var bodyB struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &bodyB); err != nil {
		t.Fatalf("decode phase B's answer: %v", err)
	}
	if bodyB.Error != "card_charge_needs_manager" {
		t.Fatalf("phase B's word = %q, want card_charge_needs_manager", bodyB.Error)
	}
	charges, voids = host.counts()
	if charges-chargeBefore != 1 {
		t.Fatalf("phase B charged %d leg(s), want exactly leg 1's", charges-chargeBefore)
	}
	if voids-voidBefore != 1 {
		t.Fatalf("after phase B's failed ring the host saw %d void(s), want the captured leg reversed", voids-voidBefore)
	}
	pinLeg6Reversal(t, pool, saleB)
}

// pinLeg6Reversal pins a failed ring's row shape: exactly one attempt row (the leg that
// captured), VOIDED by VOID with its reversal ref; the refused leg claimed nothing.
func pinLeg6Reversal(t *testing.T, pool *pgxpool.Pool, saleKey string) {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT leg_seq, status, COALESCE(reversal_kind, ''), COALESCE(reversal_ref, '')
		  FROM card_charge_attempts WHERE sale_key::text = $1 ORDER BY leg_seq`, saleKey)
	if err != nil {
		t.Fatalf("read the attempt rows: %v", err)
	}
	defer rows.Close()
	type attemptRow struct {
		seq      int
		status   string
		kind     string
		reversal string
	}
	var got []attemptRow
	for rows.Next() {
		var r attemptRow
		if err := rows.Scan(&r.seq, &r.status, &r.kind, &r.reversal); err != nil {
			t.Fatalf("scan the attempt rows: %v", err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the attempt rows: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("the ring left %d attempt row(s), want the capturing leg's only", len(got))
	}
	if got[0].seq != 1 || got[0].status != "VOIDED" || got[0].kind != "VOID" || got[0].reversal == "" {
		t.Fatalf("leg 1's attempt row = seq %d %s kind %q ref %q, want VOIDED by VOID with both refs", got[0].seq, got[0].status, got[0].kind, got[0].reversal)
	}
}
