package main

// card_rail_wiring_test.go is the card rail's wiring-level evidence
// (HH-178, HH-187, plan row PX): the rail the fold wires is the rail the
// selector chose, the handoff is pinned at the seams themselves, and the
// status route's simulated answer describes that rail. The tests drive the
// REAL wiring: wireMoney, wireOperations and wireTops over the scratch
// database, the composition root's own construction, and then
//   - the seams themselves, through the REGISTERED ROUTES: a card sale
//     rings through POST /api/v1/pos/sales (the till's charger seam takes
//     the tender) and a card return commits through POST /api/v1/pos/returns
//     (the pos refunder seam reverses it), against the local test
//     processor, counting both. What the routes exercise is what
//     till.Service and pos.Service actually hold, so swapping either seam's
//     argument for a separate service fails here: the charge never arrives,
//     or the return rides the stub and its reversal never does.
//   - identity: the tops spine's witnesses hold the one service instance
//     the selector chose, naming the wiring fact beside the behavioral pin.
//   - the status route wireTops registered answers simulated: false under
//     the run setting and simulated: true without it (HH-187).
//
// The world the routes ring against is the migration-planted one (the
// WALK-IN account, the priced products, a branch from 0003), plus what the
// counter needs planted: a till session OPEN at that branch and a users
// row linking the kit's staff subject, exactly the W0 harness shape. The
// sale and return rows the legs commit stay, tagged, as the pos
// integration suite's do; the fixture's deletes touch only the
// payment_config rows it plants, so a shared scratch database never loses
// another suite's rows. The tests gate on HH_CORE_TEST_DATABASE_URL like
// the other composition suites: absent the variable they skip politely,
// and the acceptance run sets it and asserts zero skips.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/config"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// wiringSeen counts what the stand-in Run Payments host received.
type wiringSeen struct {
	mu         sync.Mutex
	chargeHits int
	refundHits int
}

func (s *wiringSeen) hits() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chargeHits, s.refundHits
}

// newWiringRunHost stands in for Run Payments over httptest: an approved
// charge and an approved refund, each counted, each carrying a fixed
// trans_id the receipt must bring back.
func newWiringRunHost(t *testing.T) (*httptest.Server, *wiringSeen) {
	t.Helper()
	seen := &wiringSeen{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/charge", func(w http.ResponseWriter, r *http.Request) {
		seen.mu.Lock()
		seen.chargeHits++
		seen.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"result": "A", "resp_code": "00", "resp_text": "APPROVED",
			"trans_id": "run_wiring_charge_1", "card_type": "CREDIT",
		})
	})
	mux.HandleFunc("/api/v1/void-or-refund", func(w http.ResponseWriter, r *http.Request) {
		seen.mu.Lock()
		seen.refundHits++
		seen.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"result": "A", "resp_code": "00", "resp_text": "APPROVED",
			"trans_id": "run_wiring_refund_1",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, seen
}

// unsetForTest removes a variable for the test's duration and restores the
// prior value after, so an "unset" leg names the truth: the variable is
// absent, not present and empty (which a reader cannot tell apart, but a
// refusal that reads the environment picture can).
func unsetForTest(t *testing.T, name string) {
	t.Helper()
	prior, had := os.LookupEnv(name)
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(name, prior)
		}
	})
	if err := os.Unsetenv(name); err != nil {
		t.Fatalf("unset %s: %v", name, err)
	}
}

// wiringPool opens the scratch database the composition suites share and
// refuses a database that is not on the frozen migration set.
func wiringPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(coreDBEnvVar)
	if dsn == "" {
		t.Skip(coreDBEnvVar + " is not set: the card rail wiring test needs the scratch database with the frozen migrations applied")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("wiring pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := testkit.VerifyBaseline(context.Background(), testkit.PgxPool(pool)); err != nil {
		t.Fatalf("wiring baseline: %v", err)
	}
	// The kit's rows: the org, the branches, the staff principals and their grants the
	// composition legs ride. The composition fixture builds them (newCompEnv), so a leg that
	// opens the scratch database on its own must build them too: a leg must never depend on
	// another leg's leftovers to find the branch it files at. Without them the kit's own
	// branch is absent, and a leg that takes "the first location row" finds the demo branch
	// the migrations plant instead, which carries no org and cannot carry a grant.
	if err := testkit.Build(context.Background(), testkit.PgxPool(pool)); err != nil {
		t.Fatalf("wiring kit rows: %v", err)
	}
	return pool
}

// wiringRailDB plants one active payment_config row and returns the vault
// key that seals it, generated per run and never printed: the wiring's own
// vault must hold the same key to open the row, exactly as a box's
// PAYMENT_VAULT_KEY opens its dealers' rows. The cleanup deletes only the
// rows this fixture plants (the merchant id it names), so a scratch
// database shared with another suite never loses a row of theirs. The
// sealed creds carry no host unless the caller puts one inside: the live
// client prefers the creds' host over the one the environment names, which
// is how the stub leg proves nothing dials out.
func wiringRailDB(t *testing.T, pool *pgxpool.Pool, creds map[string]string) string {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generate vault key: %v", err)
	}
	key := hex.EncodeToString(raw)
	vault, err := payment.NewVault(key)
	if err != nil {
		t.Fatalf("wiring vault: %v", err)
	}
	clean := func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM payment_config WHERE processor = 'run_payments' AND merchant_id = 'MID-WIRING'`); err != nil {
			t.Fatalf("clean payment_config: %v", err)
		}
	}
	clean()
	t.Cleanup(clean)
	store := payment.NewStore(pool, vault)
	if err := store.Configure(context.Background(), "run_payments", "MID-WIRING", "CAD", "", 0, false, creds, ""); err != nil {
		t.Fatalf("configure rail: %v", err)
	}
	return key
}

// wireFold is the composition root's own construction over the test
// doubles: wireMoney, wireOperations and wireTops on a plain mux behind
// the kit's staff guards, the way registerRoutes builds them, with the
// rail variables the caller set. The staff caller reaches the branch the
// counter world was planted at. The returned spine is the real one, so
// the test reads the handoff the wiring actually made.
func wireFold(t *testing.T, pool *pgxpool.Pool, vaultKey, branchID string) (topsSpine, *http.ServeMux, *testkit.StaffGuards) {
	t.Helper()
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(identity.StaffAuthority{RoleID: "inside_sales", Tier: 3, Scope: "branch", Branches: []string{branchID}})
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mux := http.NewServeMux()
	belt := buildSmsBelt(pool, log)
	money := wireMoney(mux, pool, sg.Guards, log, "", mustRoleConfigForWire())
	ops := wireOperations(mux, pool, sg.Guards, log, money, "fx-ticket-key", belt.outbox)
	tops := wireTops(mux, pool, sg.Guards, log, money, ops, config.Config{PaymentVaultKey: vaultKey}, mustRoleConfigForWire(), belt)
	return *tops, mux, sg
}

// wiringStatus drives GET /api/v1/payments/config the way a staff caller's
// screen does and decodes the two facts the exit test turns on.
func wiringStatus(t *testing.T, mux *http.ServeMux, sg *testkit.StaffGuards) (configured, simulated bool) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/payments/config", nil)
	r.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
	r.Header.Set("X-Org", testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status route = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var body struct {
		Configured bool `json:"configured"`
		Simulated  bool `json:"simulated"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status body: %v", err)
	}
	return body.Configured, body.Simulated
}

// pinCardRailHandoff asserts the spine's witnesses hold the one service
// the selector chose. This names the wiring fact; the route legs beside it
// prove the seams behave on it.
func pinCardRailHandoff(t *testing.T, tops topsSpine) {
	t.Helper()
	if tops.cardRail == nil {
		t.Fatal("the wiring built no card rail service")
	}
	if tops.cardCharger.svc != tops.cardRail {
		t.Fatal("the till's charger seam does not hold the service the selector chose: the wiring handed the seam a different rail")
	}
	if tops.cardRefunder.svc != tops.cardRail {
		t.Fatal("the pos refunder seam does not hold the service the selector chose: a refund would ride a rail the setting did not pick (the HH-178 shape, on refunds)")
	}
}

// wiringCounter is the counter world the registered routes ring against:
// a users row linking the kit's staff subject (the caller's id lands on
// payments.received_by and the audit trail, which are FK-bound), a
// migration-planted branch, a priced product whose entered uom is its own
// base (the conversion factor is one with no planted rows), and a till
// session OPEN at that branch. Planted rows are idempotent or cleaned up
// (the session is closed, releasing the one-open-drawer constraint) so a
// shared scratch database survives the run.
type wiringCounter struct {
	branchID  string
	sessionID string
	productID string
	uom       string
	userID    string
}

func plantWiringCounter(t *testing.T, pool *pgxpool.Pool) wiringCounter {
	t.Helper()
	ctx := context.Background()
	var w wiringCounter
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (email, display_name, platform_user_id, status) VALUES ($1, $2, $3, 'ACTIVE')
		 ON CONFLICT DO NOTHING`, "fxstaff-any@wiring.invalid", "Wiring Caller", testkit.StaffSubAny); err != nil {
		t.Fatalf("plant linked user: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM users WHERE platform_user_id = $1`, testkit.StaffSubAny).Scan(&w.userID); err != nil {
		t.Fatalf("linked user: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT p.id::text, lp.price_uom FROM products p
		   JOIN pricing_list_prices lp ON lp.product_id = p.id AND lp.price_uom = p.base_uom
		  WHERE p.is_active ORDER BY p.id LIMIT 1`).Scan(&w.productID, &w.uom); err != nil {
		t.Fatalf("no migration-planted priced product: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations ORDER BY id LIMIT 1`).Scan(&w.branchID); err != nil {
		t.Fatalf("no migration-planted branch: %v", err)
	}
	station := fmt.Sprintf("wiring-%d", time.Now().UnixNano())
	if err := pool.QueryRow(ctx,
		`INSERT INTO till_sessions (branch_id, station, status, opened_by, float_cents)
		 VALUES ($1, $2, 'OPEN', $3, 20000) RETURNING id::text`, w.branchID, station, w.userID).Scan(&w.sessionID); err != nil {
		t.Fatalf("open till session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`UPDATE till_sessions SET status='CLOSED' WHERE id=$1 AND status='OPEN'`, w.sessionID)
	})
	return w
}

// postJSON drives one authenticated request through the wired mux.
func postJSON(t *testing.T, mux *http.ServeMux, sg *testkit.StaffGuards, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw)))
	r.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
	r.Header.Set("X-Org", testkit.OrgNorthSlug)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	return rec
}

// uuidKey mints a fresh UUIDv4: the idempotency key shape the counter's
// tables carry (pos_sale.idempotency_key is a UUID column).
func uuidKey(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("mint idempotency key: %v", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ringCardSale rings one CARD sale through the registered sale route: the
// tender is captured through the till's charger seam, the one the wiring
// closed.
func ringCardSale(t *testing.T, mux *http.ServeMux, sg *testkit.StaffGuards, w wiringCounter) (orderID, gatewayRef string) {
	t.Helper()
	rec := postJSON(t, mux, sg, "/api/v1/pos/sales", map[string]any{
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
	if rec.Code != http.StatusCreated {
		t.Fatalf("card sale = %d (%s), want 201", rec.Code, rec.Body.String())
	}
	var sale struct {
		OrderID    string `json:"order_id"`
		GatewayRef string `json:"gateway_ref"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sale); err != nil {
		t.Fatalf("decode sale: %v", err)
	}
	return sale.OrderID, sale.GatewayRef
}

// returnCardSale returns the sale whole through the registered return
// route: a CARD refund reverses through the pos refunder seam, the one the
// wiring closed. The reversal's gateway_ref is read back off the refund
// payment row (the till is the sole payments writer).
func returnCardSale(t *testing.T, pool *pgxpool.Pool, mux *http.ServeMux, sg *testkit.StaffGuards, orderID string) (refundRef string) {
	t.Helper()
	var lineID string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM order_lines WHERE order_id = $1 ORDER BY line_no LIMIT 1`, orderID).Scan(&lineID); err != nil {
		t.Fatalf("read the sale's line: %v", err)
	}
	rec := postJSON(t, mux, sg, "/api/v1/pos/returns", map[string]any{
		"idempotency_key": uuidKey(t),
		"order_id":        orderID,
		"refund_method":   "CARD",
		"restock":         false,
		"lines":           []map[string]any{{"order_line_id": lineID, "qty_base": 2.0, "reason_code": "WIRING"}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("card return = %d (%s), want 201", rec.Code, rec.Body.String())
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT p.gateway_ref FROM pos_returns r JOIN payments p ON p.id = r.refund_payment_id
		  WHERE r.order_id = $1 ORDER BY r.id DESC LIMIT 1`, orderID).Scan(&refundRef); err != nil {
		t.Fatalf("read the refund payment: %v", err)
	}
	return refundRef
}

// TestCardRailWiring_RunSettingChargesAndRefundsReachTheProcessor, HH-178:
// with HOS_CARD_PROCESSOR=run and HOS_CARD_BASE_URL naming the test host,
// a card sale and a card return through the REGISTERED ROUTES reach that
// host, once each: the sale's tender through the till's charger seam, the
// return's reversal through the pos refunder seam, the seams as the
// services actually hold them. The status route answers simulated: false
// (HH-187).
func TestCardRailWiring_RunSettingChargesAndRefundsReachTheProcessor(t *testing.T) {
	host, seen := newWiringRunHost(t)
	t.Setenv("APP_ENV", "dev")
	t.Setenv("HOS_CARD_PROCESSOR", "run")
	t.Setenv("HOS_CARD_BASE_URL", host.URL)

	pool := wiringPool(t)
	// The sealed creds carry NO host: the environment's host is the one the
	// live client must dial, the exact shape the exit test names.
	key := wiringRailDB(t, pool, map[string]string{"api_key": "wiring-test-not-a-secret"})
	w := plantWiringCounter(t, pool)
	tops, mux, sg := wireFold(t, pool, key, w.branchID)

	if tops.cardRail.Simulated() {
		t.Fatal("the wiring built the simulator under HOS_CARD_PROCESSOR=run: the selector's choice was discarded again")
	}
	pinCardRailHandoff(t, tops)

	orderID, gatewayRef := ringCardSale(t, mux, sg, w)
	if gatewayRef != "run_wiring_charge_1" {
		t.Fatalf("sale gateway ref = %q, want the processor's trans_id run_wiring_charge_1", gatewayRef)
	}
	chargeHits, _ := seen.hits()
	if chargeHits != 1 {
		t.Fatalf("the charger seam made %d charge request(s), want exactly one: the sale's tender did not ride the wired rail", chargeHits)
	}

	refundRef := returnCardSale(t, pool, mux, sg, orderID)
	if refundRef != "run_wiring_refund_1" {
		t.Fatalf("refund gateway ref = %q, want the processor's trans_id run_wiring_refund_1", refundRef)
	}
	_, refundHits := seen.hits()
	if refundHits != 1 {
		t.Fatalf("the refunder seam made %d refund request(s), want exactly one: the return rode a rail that never reached the processor", refundHits)
	}

	// The status route describes the wired rail, honestly.
	configured, simulated := wiringStatus(t, mux, sg)
	if !configured {
		t.Fatal("status route answered configured: false over a configured rail")
	}
	if simulated {
		t.Fatal("status route answered simulated: true with a live processor wired: a screen would badge real charges as simulated")
	}
}

// TestCardRailWiring_UnsetSettingRidesTheSimulator, the default leg: with
// HOS_CARD_PROCESSOR absent the stub is what the wiring builds, the sale
// and the return complete in process with the stub's reference shapes,
// nothing reaches any server, and the status route answers simulated:
// true.
func TestCardRailWiring_UnsetSettingRidesTheSimulator(t *testing.T) {
	host, seen := newWiringRunHost(t)
	t.Setenv("APP_ENV", "dev")
	unsetForTest(t, "HOS_CARD_PROCESSOR")
	unsetForTest(t, "HOS_CARD_BASE_URL")

	pool := wiringPool(t)
	// A host only a wrongly wired live client would dial: the sealed creds
	// carry it (the live client prefers the creds' host over the
	// environment's), and the stub must never look at it.
	key := wiringRailDB(t, pool, map[string]string{"api_key": "wiring-test-not-a-secret", "base_url": host.URL})
	w := plantWiringCounter(t, pool)
	tops, mux, sg := wireFold(t, pool, key, w.branchID)

	if !tops.cardRail.Simulated() {
		t.Fatal("the wiring did not build the simulator with HOS_CARD_PROCESSOR absent: the default must be the stub")
	}
	pinCardRailHandoff(t, tops)

	orderID, gatewayRef := ringCardSale(t, mux, sg, w)
	if !strings.HasPrefix(gatewayRef, "rp_sim_") {
		t.Fatalf("simulated sale gateway ref = %q, want the stub's rp_sim_ shape", gatewayRef)
	}
	refundRef := returnCardSale(t, pool, mux, sg, orderID)
	if !strings.HasPrefix(refundRef, "rp_ref_") {
		t.Fatalf("simulated refund ref = %q, want the stub's rp_ref_ shape", refundRef)
	}

	chargeHits, refundHits := seen.hits()
	if chargeHits != 0 || refundHits != 0 {
		t.Fatalf("the simulator dialed out: %d charge hit(s) and %d refund hit(s) on the host, want none", chargeHits, refundHits)
	}

	configured, simulated := wiringStatus(t, mux, sg)
	if !configured {
		t.Fatal("status route answered configured: false over a configured rail")
	}
	if !simulated {
		t.Fatal("status route answered simulated: false with the simulator wired: a screen would present simulated cards as real")
	}
}
