package main

// card_charge_one_connection_test.go is the charge-side pool stall's named
// leg: the REAL wiring, wireMoney and wireTops over the scratch database,
// rings ONE card sale through the REGISTERED route POST /api/v1/pos/sales
// with the fold built over a ONE-CONNECTION pool. The sale transaction
// (pos RunInTx) holds the pool's only connection while it takes the
// tender; if the till's charger seam reaches the payment config through
// the POOL (the pool-shaped Service.Charge), the config read waits for a
// second connection the pool can never grant: the sale stalls until its
// context dies. On the transaction-riding core (the RefundTx discipline on
// the charge side) the config read and any rotation write-back ride the
// sale's own transaction and the sale commits: the response arrives, the
// gateway ref is the stand-in host's trans_id, and the host saw exactly
// one charge.
//
// The payment config is planted FIRST, sequentially, so its Configure
// transaction releases the one connection before the sale fires. The
// request's context timeout is far wider than a healthy ring and far
// narrower than the leg's own watchdog, so a stalled sale fails HERE with
// the stall named instead of eating the suite.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// chargeStallRequestTimeout bounds ONE card sale's wait for its response.
// A healthy ring over the wired fold answers in well under a second; on
// the stall the sale never answers and this timeout is what ends it.
const chargeStallRequestTimeout = 15 * time.Second

// chargeStallTestBudget is the leg's own watchdog, wider than the request
// timeout it wraps: the leg must be the one that reports the stall, in
// words, before the suite's timeout could.
const chargeStallTestBudget = 45 * time.Second

// wiringOneConnPool is wiringPool over a pool that may hold exactly ONE
// connection (the pos rig's smallPool pattern against the composition
// suite's gate variable): any in-transaction work that reaches for a
// second connection stalls visibly instead of quietly borrowing one.
func wiringOneConnPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(coreDBEnvVar)
	if dsn == "" {
		t.Skip(coreDBEnvVar + " is not set: the card rail wiring test needs the scratch database with the frozen migrations applied")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("wiring pool config: %v", err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("wiring pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := testkit.VerifyBaseline(context.Background(), testkit.PgxPool(pool)); err != nil {
		t.Fatalf("wiring baseline: %v", err)
	}
	return pool
}

// TestCardRailWiring_OneConnectionCardSaleCommits, the charge-side stall
// leg: ONE card sale through the registered sale route over the
// one-connection pool must commit (the success shape, the gateway ref the
// stand-in host minted, exactly one charge on the host). The sale
// transaction holds the pool's only connection while the tender's charge
// runs; a charger that reads the payment config through the pool waits for
// a second connection that never comes, the response never arrives inside
// the request timeout, and this leg fails naming the stall.
func TestCardRailWiring_OneConnectionCardSaleCommits(t *testing.T) {
	host, seen := newWiringRunHost(t)
	t.Setenv("APP_ENV", "dev")
	t.Setenv("HOS_CARD_PROCESSOR", "run")
	t.Setenv("HOS_CARD_BASE_URL", host.URL)

	pool := wiringOneConnPool(t)
	// The payment config first, sequential over the one connection
	// (Configure's transaction commits and releases before anything else
	// runs); the sealed creds carry no host, the environment's host is the
	// one the live client dials.
	key := wiringRailDB(t, pool, map[string]string{"api_key": "wiring-test-not-a-secret"})
	w := plantWiringCounter(t, pool)
	tops, mux, sg := wireFold(t, pool, key, w.branchID)

	if tops.cardRail.Simulated() {
		t.Fatal("the wiring built the simulator under HOS_CARD_PROCESSOR=run: the selector's choice was discarded again")
	}

	// ONE card sale, the response required before the request timeout.
	raw, err := json.Marshal(map[string]any{
		"idempotency_key": uuidKey(t),
		"branch_id":       w.branchID,
		"till_session_id": w.sessionID,
		"sale_type":       "COLLECT_LATER",
		"tender":          "CARD",
		"payment_token":   "tok_wiring_counter",
		"card_type":       "CREDIT",
		"offline":         false,
		"lines":           []map[string]any{{"product_id": w.productID, "qty_entered": 2.0, "uom_entered": w.uom, "cost_cents": 0}},
	})
	if err != nil {
		t.Fatalf("encode sale: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), chargeStallRequestTimeout)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pos/sales", strings.NewReader(string(raw))).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
	req.Header.Set("X-Org", testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()

	done := make(chan bool, 1)
	go func() {
		mux.ServeHTTP(rec, req)
		done <- true
	}()
	select {
	case <-done:
	case <-time.After(chargeStallTestBudget):
		t.Fatalf("the one-connection card sale never answered even past its own watchdog: the sale transaction holds the pool's only connection and something inside it is waiting for a second (the stall)")
	}
	if ctx.Err() != nil {
		t.Fatalf("the one-connection card sale stalled past its request timeout (%v): the sale transaction holds the pool's only connection and the tender's charge read the payment config through the pool (the stall)", ctx.Err())
	}
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("card sale = %d (%s), want the committed 201/200 shape", rec.Code, rec.Body.String())
	}
	var sale struct {
		OrderID    string `json:"order_id"`
		GatewayRef string `json:"gateway_ref"`
		Status     string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sale); err != nil {
		t.Fatalf("decode sale: %v", err)
	}
	if sale.Status != "COMMITTED" {
		t.Fatalf("sale status = %q, want COMMITTED: the sale did not commit", sale.Status)
	}
	if sale.GatewayRef == "" {
		t.Fatal("the committed sale's gateway ref is empty: the charge never reached the processor")
	}
	chargeHits, _ := seen.hits()
	if chargeHits != 1 {
		t.Fatalf("the charger seam made %d charge request(s), want exactly one", chargeHits)
	}
}
