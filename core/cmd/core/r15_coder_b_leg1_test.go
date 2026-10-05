package main

// r15_coder_b_leg1_test.go is packet B's 40P01 leg (design 5.3 leg 1, r12's
// proof shape): a test-only BEFORE INSERT ON pos_sale trigger always raises
// SQLSTATE 40P01, so the fault lands after the card was captured. The ring
// must answer 503 sale_failed_card_reversed with the capture reversed at the
// processor (one charge, one void on that charge's trans_id, an attempt row
// VOIDED with both refs, no payments row); a same-key retry after the
// trigger is dropped must still answer 503 from the row with no new charge;
// a fresh key must ring 201 and bind. On the tip (no capture loop, no
// compensation) ring 1 answers a bare 500 with one charge and no record, and
// the same-key retry charges again: exactly r12's output.
//
// The file also carries the stand-in the other packet B legs share: it
// counts charges and reversals BY ACTION, records every body, and can be
// rigged to decline a named token, to read the attempt table while a charge
// is in flight (leg 3), and to hold the first charge until a second arrives
// (leg 5). Throwaway: removed at cleanup with the legs.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// r15RunHost is the packet B stand-in for Run Payments over httptest: every
// charge and every reversal is approved and counted, reversals counted by
// their action, every request body recorded.
type r15RunHost struct {
	mu           sync.Mutex
	chargeBodies []string
	chargeTrans  []string
	voidTrans    []string
	refundTrans  []string

	// declineToken: a charge whose body carries this token is answered C.
	declineToken string
	// probe: when non-nil, the charge handler reads the CHARGING attempt
	// rows through this SECOND pool before answering (leg 3).
	probe         *pgxpool.Pool
	probeCharging int
	// hold: the FIRST charge waits until a second charge arrives or the
	// bound ends (leg 5); a later charge releases it.
	hold       bool
	secondC    chan struct{}
	secondOnce sync.Once
}

// newR15RunHost builds the stand-in. Every knob is optional and off by
// default.
func newR15RunHost(t *testing.T) (*httptest.Server, *r15RunHost) {
	t.Helper()
	h := &r15RunHost{secondC: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/charge", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		h.mu.Lock()
		h.chargeBodies = append(h.chargeBodies, string(body))
		n := len(h.chargeTrans) + 1
		trans := fmt.Sprintf("run_b_charge_%d", n)
		decline := h.declineToken != "" && strings.Contains(string(body), h.declineToken)
		hold := h.hold && n == 1
		probe := h.probe
		h.mu.Unlock()
		if probe != nil {
			var cnt int
			if err := probe.QueryRow(r.Context(),
				`SELECT count(*) FROM card_charge_attempts WHERE status = 'CHARGING'`).Scan(&cnt); err == nil && cnt > 0 {
				h.mu.Lock()
				h.probeCharging += cnt
				h.mu.Unlock()
			}
		}
		if hold {
			// Hold the first charge until a second one arrives (the red
			// shape: two goroutines both charge) or the bound ends (the
			// green shape: the loser never reaches the processor).
			select {
			case <-h.secondC:
			case <-time.After(r15HoldBound):
			}
		} else if h.hold {
			h.secondOnce.Do(func() { close(h.secondC) })
		}
		w.Header().Set("Content-Type", "application/json")
		h.mu.Lock()
		h.chargeTrans = append(h.chargeTrans, trans)
		h.mu.Unlock()
		if decline {
			_ = json.NewEncoder(w).Encode(map[string]string{
				"result": "C", "resp_code": "05", "resp_text": "DECLINED", "trans_id": "run_b_decline_" + trans,
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"result": "A", "resp_code": "00", "resp_text": "APPROVED", "trans_id": trans, "card_type": "CREDIT",
		})
	})
	mux.HandleFunc("/api/v1/void-or-refund", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			TransID string `json:"trans_id"`
			Action  string `json:"action"`
		}
		_ = json.Unmarshal(body, &req)
		h.mu.Lock()
		if req.Action == "void" {
			h.voidTrans = append(h.voidTrans, req.TransID)
		} else {
			h.refundTrans = append(h.refundTrans, req.TransID)
		}
		n := len(h.voidTrans) + len(h.refundTrans)
		h.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"result": "A", "resp_code": "00", "resp_text": "APPROVED",
			"trans_id": fmt.Sprintf("run_b_reversal_%d", n+1),
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, h
}

// r15HoldBound bounds the leg 5 stand-in's hold on the first charge.
const r15HoldBound = 2 * time.Second

// counts reads the stand-in's tallies under its lock.
func (h *r15RunHost) counts() (charges, voids, refunds int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.chargeTrans), len(h.voidTrans), len(h.refundTrans)
}

// r15BangAlways plants the test-only trigger that raises SQLSTATE 40P01 on
// EVERY INSERT INTO pos_sale, and drops it at cleanup. The fault lands after
// the card was captured (the capture loop runs before the sale transaction).
func r15BangAlways(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	r15DropBang(t, pool)
	if _, err := pool.Exec(context.Background(), `
		CREATE OR REPLACE FUNCTION r15_bang() RETURNS trigger AS $fn$
		BEGIN
			RAISE EXCEPTION 'r15 leg: forced serialization failure' USING ERRCODE = '40P01';
		END; $fn$ LANGUAGE plpgsql;
		CREATE TRIGGER r15_bang BEFORE INSERT ON pos_sale
			FOR EACH ROW EXECUTE FUNCTION r15_bang();`); err != nil {
		t.Fatalf("plant the 40P01 trigger: %v", err)
	}
	t.Cleanup(func() { r15DropBang(t, pool) })
}

// r15BangFirstOnly plants the trigger in its first-firing-only shape (leg 2):
// a SEQUENCE makes the FIRST INSERT raise 40P01 and every later one pass, the
// transient fault the bounded rerun must see through. The sequence is the
// counter that counts firings: nextval moves forward even when the raising
// transaction rolls back, where a table's counter would reset with it and
// the fault would fire on every rerun forever.
func r15BangFirstOnly(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	r15DropBang(t, pool)
	if _, err := pool.Exec(context.Background(), `
		CREATE SEQUENCE IF NOT EXISTS r15_bang_seq START 1;
		CREATE OR REPLACE FUNCTION r15_bang() RETURNS trigger AS $fn$
		DECLARE c bigint;
		BEGIN
			c := nextval('r15_bang_seq');
			IF c = 1 THEN
				RAISE EXCEPTION 'r15 leg: first firing only' USING ERRCODE = '40P01';
			END IF;
			RETURN NEW;
		END; $fn$ LANGUAGE plpgsql;
		CREATE TRIGGER r15_bang BEFORE INSERT ON pos_sale
			FOR EACH ROW EXECUTE FUNCTION r15_bang();`); err != nil {
		t.Fatalf("plant the first-only 40P01 trigger: %v", err)
	}
	t.Cleanup(func() { r15DropBang(t, pool) })
}

// r15DropBang removes the trigger, its function and its counter sequence.
func r15DropBang(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		DROP TRIGGER IF EXISTS r15_bang ON pos_sale;
		DROP FUNCTION IF EXISTS r15_bang();
		DROP SEQUENCE IF EXISTS r15_bang_seq;`)
	if err != nil {
		t.Fatalf("drop the 40P01 trigger: %v", err)
	}
}

// r15RingBody is one CARD sale body for the planted counter.
func r15RingBody(w wiringCounter, key, token string) map[string]any {
	return map[string]any{
		"idempotency_key": key,
		"branch_id":       w.branchID,
		"till_session_id": w.sessionID,
		"sale_type":       "COLLECT_LATER",
		"tender":          "CARD",
		"payment_token":   token,
		"card_type":       "CREDIT",
		"offline":         false,
		"lines":           []map[string]any{{"product_id": w.productID, "qty_entered": 1.0, "uom_entered": w.uom, "cost_cents": 0}},
	}
}

// postJSONBounded is postJSON under a watchdog: a ring that never answers
// fails the leg in words before the suite's timeout could.
func postJSONBounded(t *testing.T, mux *http.ServeMux, sg *testkit.StaffGuards, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		rec = postJSON(t, mux, sg, path, body)
	}()
	select {
	case <-done:
		return rec
	case <-time.After(30 * time.Second):
		t.Fatalf("the ring on %s never answered inside its bound", path)
		return rec
	}
}

// TestR15B_Leg1_ChargedRingRollsBackReversedAndNeverChargesTwice, the 40P01
// leg: with the trigger raising on every pos_sale insert, ring 1 must answer
// 503 sale_failed_card_reversed (one charge, one void on that charge, the
// attempt VOIDED with both refs, no payments row); the same key with the
// trigger dropped must still answer 503 from the row with no new charge; a
// fresh key must ring 201 and bind.
func TestR15B_Leg1_ChargedRingRollsBackReversedAndNeverChargesTwice(t *testing.T) {
	host, seen := newR15RunHost(t)
	t.Setenv("APP_ENV", "dev")
	t.Setenv("HOS_CARD_PROCESSOR", "run")
	t.Setenv("HOS_CARD_BASE_URL", host.URL)

	pool := wiringPool(t)
	key := wiringRailDB(t, pool, map[string]string{"api_key": "r15-leg-not-a-secret"})
	w := plantWiringCounter(t, pool)
	r15BangAlways(t, pool)
	_, mux, sg := wireFold(t, pool, key, w.branchID)

	// Ring 1: the fault lands after the capture; the ring must answer 503
	// sale_failed_card_reversed, not a bare 500.
	saleKey := uuidKey(t)
	rec := postJSONBounded(t, mux, sg, "/api/v1/pos/sales", r15RingBody(w, saleKey, "tok_r15_leg1"))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "sale_failed_card_reversed") {
		t.Fatalf("ring 1 = %d (%s), want 503 sale_failed_card_reversed", rec.Code, rec.Body.String())
	}
	charges, voids, refunds := seen.counts()
	if charges != 1 || voids != 1 || refunds != 0 {
		t.Fatalf("after ring 1: charges=%d voids=%d refunds=%d, want one charge and one void of it", charges, voids, refunds)
	}
	if seen.voidTrans[0] != seen.chargeTrans[0] {
		t.Fatalf("the void reversed trans_id %q, want the charge's %q", seen.voidTrans[0], seen.chargeTrans[0])
	}
	var status, procRef, revKind, revRef string
	if err := pool.QueryRow(context.Background(),
		`SELECT status, processor_ref, reversal_kind, reversal_ref FROM card_charge_attempts WHERE leg_key = $1`, saleKey).
		Scan(&status, &procRef, &revKind, &revRef); err != nil {
		t.Fatalf("read the attempt row: %v", err)
	}
	if status != "VOIDED" || procRef == "" || revKind == "" || revRef == "" {
		t.Fatalf("the attempt row is %s ref %q by %q %q, want VOIDED with both refs", status, procRef, revKind, revRef)
	}
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM payments WHERE idempotency_key = $1`, saleKey).Scan(&n); err != nil {
		t.Fatalf("count payments: %v", err)
	}
	if n != 0 {
		t.Fatalf("the rolled-back ring left %d payments rows, want none", n)
	}

	// Same key with the trigger dropped: still 503 from the row, still one
	// charge, no new void.
	r15DropBang(t, pool)
	rec2 := postJSONBounded(t, mux, sg, "/api/v1/pos/sales", r15RingBody(w, saleKey, "tok_r15_leg1"))
	if rec2.Code != http.StatusServiceUnavailable || !strings.Contains(rec2.Body.String(), "sale_failed_card_reversed") {
		t.Fatalf("same-key retry = %d (%s), want the row's 503 sale_failed_card_reversed", rec2.Code, rec2.Body.String())
	}
	charges, voids, _ = seen.counts()
	if charges != 1 || voids != 1 {
		t.Fatalf("the same-key retry charged again: charges=%d voids=%d, want 1 and 1", charges, voids)
	}

	// Fresh key: 201, a second charge, one more payments row, the attempt
	// BOUND to it.
	fresh := uuidKey(t)
	rec3 := postJSONBounded(t, mux, sg, "/api/v1/pos/sales", r15RingBody(w, fresh, "tok_r15_leg1c"))
	if rec3.Code != http.StatusCreated {
		t.Fatalf("fresh key = %d (%s), want 201", rec3.Code, rec3.Body.String())
	}
	charges, voids, _ = seen.counts()
	if charges != 2 || voids != 1 {
		t.Fatalf("after the fresh ring: charges=%d voids=%d, want 2 and 1", charges, voids)
	}
	var hasPayment bool
	if err := pool.QueryRow(context.Background(), `
		SELECT a.status, p.id IS NOT NULL FROM card_charge_attempts a
		LEFT JOIN payments p ON p.id = a.payment_id WHERE a.leg_key = $1`, fresh).Scan(&status, &hasPayment); err != nil {
		t.Fatalf("read the fresh attempt row: %v", err)
	}
	if status != "BOUND" || !hasPayment {
		t.Fatalf("the fresh attempt is %s with payments row %v, want BOUND with its payments row", status, hasPayment)
	}
}
