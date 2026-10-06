package main

// card_charge_rotation_test.go pins the rotation ledger against a REAL
// declined-then-committed pair of sales, re-pointed at the CAPTURE seam the
// card charge fix moved the charge onto (round 1's P2 and P3 1, the same
// claims on the new seam): a credential rotation the live client performs
// during a capture must SURVIVE the declined ring that carried it, because
// the processor has already exchanged the old key. The strict host answers
// 401 to the planted key, serves ONE refresh of it (a second refresh of the
// same key is refused, the exchange is single-use), declines the charge
// retried under the fresh key (so sale 1 answers the decline with nothing
// committed), and approves every later charge under the fresh key. On the
// ledger-shaped core the flusher lands the fresh key through the pool, and
// sale 2 rings COMMITTED under it. On a core whose write-back rode the sale
// transaction (round 1's shape) the rollback discarded the rotation, the
// ciphertext never moved, and sale 2 died at the refused refresh.
//
// The leg rides the same one-connection rig as the stall leg: the capture
// holds no connection while it dispatches, so the flush may land during or
// after the ring, either way before sale 2 needs the fresh key.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// rotation host facts: the planted (expired) key and the fresh one the
// refresh mints. Not secrets: stand-in values on a throwaway rig.
const (
	rotOldKey   = "rot-expired-standin-key"
	rotFreshKey = "rot-fresh-standin-key"
)

// rotFlushWaitBudget bounds this leg's wait for the flusher's landing: the
// payment ledger's own flush window (rotationFlushWindow in that package,
// unexported) plus a scheduling margin, so the leg reports a lost rotation
// rather than the suite's timeout doing it.
const rotFlushWaitBudget = 35 * time.Second

// rotatingRunHost stands in for Run Payments with a single-use refresh:
// charges under the old key are refused (401), the FIRST refresh of the
// old key mints the fresh key, any later refresh of the old key is refused
// (the exchange already happened), the first charge under the fresh key
// declines (so the sale carrying the rotation rolls back), and every later
// charge under the fresh key approves. The leg reads the recorded charge
// keys and refresh counts back, so the no-second-refresh claim is asserted,
// not implied.
type rotatingRunHost struct {
	mu             sync.Mutex
	refreshed      bool
	refreshCalls   int
	refreshRefused int
	freshCharges   int
	chargeKeys     []string
}

func newRotatingRunHost(t *testing.T) (*httptest.Server, *rotatingRunHost) {
	t.Helper()
	h := &rotatingRunHost{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/charge", func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		h.mu.Lock()
		h.chargeKeys = append(h.chargeKeys, key)
		fresh := key == rotFreshKey
		n := h.freshCharges
		if fresh {
			h.freshCharges++
		}
		h.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case !fresh:
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "api_key expired"})
		case n == 0:
			_ = json.NewEncoder(w).Encode(map[string]string{
				"result": "C", "resp_code": "05", "resp_text": "DECLINED", "trans_id": "rot_dec_1",
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]string{
				"result": "A", "resp_code": "00", "resp_text": "APPROVED", "trans_id": "rot_app_1", "card_type": "CREDIT",
			})
		}
	})
	mux.HandleFunc("/api/v1/api_keys/refresh", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		h.mu.Lock()
		h.refreshCalls++
		done := h.refreshed
		h.refreshed = true
		refuse := body.Token != rotOldKey || done
		if refuse {
			h.refreshRefused++
		}
		h.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if refuse {
			w.WriteHeader(http.StatusGone)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "token already exchanged"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"api_key": rotFreshKey, "refresh_token": "rot-refresh-standin"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, h
}

// rotationHostFacts reads the host's recorded facts under its lock.
func rotationHostFacts(h *rotatingRunHost) (keys []string, served, refused int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.chargeKeys...), h.refreshCalls - h.refreshRefused, h.refreshRefused
}

// rotCiphertext reads the active planted row's sealed credentials, the
// fact the flusher's write moves.
func rotCiphertext(t *testing.T, pool *pgxpool.Pool) []byte {
	t.Helper()
	var ct []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT credentials_ciphertext FROM payment_config
		  WHERE processor='run_payments' AND merchant_id='MID-WIRING' AND status='active'`).Scan(&ct); err != nil {
		t.Fatalf("read the planted config row: %v", err)
	}
	return ct
}

// TestCardRailWiring_RotationSurvivesARolledBackSale: sale 1 declines
// after a refresh (402, nothing committed), the flusher lands the fresh
// key through the pool after the rollback, and sale 2 rings COMMITTED
// under the fresh key without any second refresh.
func TestCardRailWiring_RotationSurvivesARolledBackSale(t *testing.T) {
	hostSrv, host := newRotatingRunHost(t)
	t.Setenv("APP_ENV", "dev")
	t.Setenv("HOS_CARD_PROCESSOR", "run")
	t.Setenv("HOS_CARD_BASE_URL", hostSrv.URL)

	pool := wiringOneConnPool(t)
	key := wiringRailDB(t, pool, map[string]string{"api_key": rotOldKey})
	w := plantWiringCounter(t, pool)
	tops, mux, sg := wireFold(t, pool, key, w.branchID)
	if tops.cardRail.Simulated() {
		t.Fatal("the wiring built the simulator under HOS_CARD_PROCESSOR=run")
	}
	_ = mux

	saleBody := func(idem string) map[string]any {
		return map[string]any{
			"idempotency_key": idem,
			"branch_id":       w.branchID,
			"till_session_id": w.sessionID,
			"sale_type":       "COLLECT_LATER",
			"tender":          "CARD",
			"payment_token":   "tok_rot_rig",
			"card_type":       "CREDIT",
			"offline":         false,
			"lines":           []map[string]any{{"product_id": w.productID, "qty_entered": 1.0, "uom_entered": w.uom, "cost_cents": 0}},
		}
	}

	// The planted ciphertext, read BEFORE sale 1 fires: the flusher can land
	// the fresh key the moment the rolled-back sale releases the one
	// connection, so any read taken after the 402 may already see the landed
	// rotation and would then wait for a second move that never comes. The
	// pre-sale read is also the stronger pin: the flush must move the row OFF
	// the planted value.
	before := rotCiphertext(t, pool)

	// Sale 1: 401 under the old key, one refresh, the retried charge
	// declines at the CAPTURE (before any transaction opens), the ring
	// answers the decline with nothing committed.
	rec1 := postJSON(t, mux, sg, "/api/v1/pos/sales", saleBody(uuidKey(t)))
	if rec1.Code != http.StatusPaymentRequired || !strings.Contains(rec1.Body.String(), "card_charge_card_declined") {
		t.Fatalf("sale 1 = %d (%s), want the capture seam's declined 402", rec1.Code, rec1.Body.String())
	}

	// The flusher: the fresh key lands through the pool once the rolled
	// back sale releases the one connection. Bounded wait on the
	// ciphertext moving off the planted value; on a write-back riding the
	// transaction this never happens and the leg fails here.
	deadline := time.Now().Add(rotFlushWaitBudget)
	for {
		now := rotCiphertext(t, pool)
		if string(now) != string(before) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the rotation never landed through the pool after the rollback: the rolled-back sale discarded the fresh key (round 1's P2)")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Sale 2: rings COMMITTED under the fresh key. The host refuses a
	// second refresh of the old key, so a lost rotation cannot recover:
	// this sale only succeeds because the carried rotation landed.
	rec2 := postJSON(t, mux, sg, "/api/v1/pos/sales", saleBody(uuidKey(t)))
	if rec2.Code != http.StatusCreated && rec2.Code != http.StatusOK {
		t.Fatalf("sale 2 = %d (%s), want the committed 201/200 under the fresh key", rec2.Code, rec2.Body.String())
	}
	var sale struct {
		GatewayRef string `json:"gateway_ref"`
		Status     string `json:"status"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &sale); err != nil {
		t.Fatalf("decode sale 2: %v", err)
	}
	if sale.Status != "COMMITTED" || sale.GatewayRef != "rot_app_1" {
		t.Fatalf("sale 2 = %+v, want COMMITTED with the approval ref", sale)
	}

	// The host's own record (round 2's P3 4): sale 1 presented the old key, was refreshed once, and
	// its retried charge presented the fresh key; sale 2 presented the fresh key with NO second
	// refresh of the already exchanged old one. The no-second-refresh claim is asserted, not implied.
	keys, served, refused := rotationHostFacts(host)
	want := []string{rotOldKey, rotFreshKey, rotFreshKey}
	if len(keys) != len(want) {
		t.Fatalf("the host saw %d charges (%v), want %d (%v): the shape of the two-sale rotation changed", len(keys), keys, len(want), want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("charge %d presented %q, want %q (keys %v)", i+1, keys[i], want[i], keys)
		}
	}
	if served != 1 || refused != 0 {
		t.Fatalf("refreshes served = %d, refused = %d, want exactly one served and none refused: a second refresh means the rotation was lost and retried", served, refused)
	}
}

// TestCardRailWiring_OrdinaryPoolNextSaleRingsWithNoWait is the standing composition pin for the
// overlay (the round 2 probe's shape, the round 3 review's P3 4): on an ORDINARY multi-connection
// pool, the sale fired straight after a rotating declined sale, with no wait for the flush, must
// commit. Without the overlay this shape failed more than half the runs (the sale read the stale
// row while the flush was still in flight and the host refused the second refresh of the exchanged
// key); with it, sale 2 presents the pending set or the landed row, either way the fresh key, and
// the host must see exactly one refresh.
func TestCardRailWiring_OrdinaryPoolNextSaleRingsWithNoWait(t *testing.T) {
	hostSrv, host := newRotatingRunHost(t)
	t.Setenv("APP_ENV", "dev")
	t.Setenv("HOS_CARD_PROCESSOR", "run")
	t.Setenv("HOS_CARD_BASE_URL", hostSrv.URL)

	pool := wiringPool(t)
	key := wiringRailDB(t, pool, map[string]string{"api_key": rotOldKey})
	w := plantWiringCounter(t, pool)
	_, mux, sg := wireFold(t, pool, key, w.branchID)

	saleBody := func(idem string) map[string]any {
		return map[string]any{
			"idempotency_key": idem,
			"branch_id":       w.branchID,
			"till_session_id": w.sessionID,
			"sale_type":       "COLLECT_LATER",
			"tender":          "CARD",
			"payment_token":   "tok_rot_rig2",
			"card_type":       "CREDIT",
			"offline":         false,
			"lines":           []map[string]any{{"product_id": w.productID, "qty_entered": 1.0, "uom_entered": w.uom, "cost_cents": 0}},
		}
	}

	// Sale 1: 401 under the old key, one single-use refresh, the retried charge declines, nothing
	// committed.
	rec1 := postJSON(t, mux, sg, "/api/v1/pos/sales", saleBody(uuidKey(t)))
	if rec1.Code != http.StatusPaymentRequired {
		t.Fatalf("sale 1 = %d (%s), want the declined 402", rec1.Code, rec1.Body.String())
	}

	// Sale 2, immediately: no wait for the flush. The overlay must answer the pending rotation (or
	// the landed row), never the exchanged key.
	rec2 := postJSON(t, mux, sg, "/api/v1/pos/sales", saleBody(uuidKey(t)))
	if rec2.Code != http.StatusCreated && rec2.Code != http.StatusOK {
		t.Fatalf("sale 2 with no wait = %d (%s), want the committed 201/200 under the fresh key", rec2.Code, rec2.Body.String())
	}
	var sale struct {
		GatewayRef string `json:"gateway_ref"`
		Status     string `json:"status"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &sale); err != nil {
		t.Fatalf("decode sale 2: %v", err)
	}
	if sale.Status != "COMMITTED" || sale.GatewayRef != "rot_app_1" {
		t.Fatalf("sale 2 = %+v, want COMMITTED with the approval ref", sale)
	}
	keys, served, refused := rotationHostFacts(host)
	want := []string{rotOldKey, rotFreshKey, rotFreshKey}
	if len(keys) != len(want) {
		t.Fatalf("the host saw %d charges (%v), want %d (%v)", len(keys), keys, len(want), want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("charge %d presented %q, want %q (keys %v): the stale key crossed the seam", i+1, keys[i], want[i], keys)
		}
	}
	if served != 1 || refused != 0 {
		t.Fatalf("refreshes served = %d, refused = %d, want exactly one served and none refused", served, refused)
	}
}
