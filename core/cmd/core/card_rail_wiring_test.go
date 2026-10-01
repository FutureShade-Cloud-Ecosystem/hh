package main

// card_rail_wiring_test.go is the card rail's wiring-level evidence
// (HH-178, HH-187, plan row PX): the rail the fold wires is the rail the
// selector chose, and the status route's simulated answer describes that
// rail. With HOS_CARD_PROCESSOR=run and HOS_CARD_BASE_URL at a local test
// server, the till's card charge and the pos card refund reach that server
// through the exact seams the wiring closes (cardChargerAdapter,
// posCardRefunderAdapter), and GET /api/v1/payments/config answers
// simulated: false; with the setting unset, the simulator is wired, no
// request reaches any server, and the route answers simulated: true.
//
// The charge reads a real payment_config row sealed by the real vault over
// the scratch database, so the config load, decrypt and dispatch are the
// production ones (the internal/payment integration tests' shape, lifted
// to the composition root, where the wiring decision itself lives). The
// tests gate on HH_CORE_TEST_DATABASE_URL like the other composition
// suites: absent the variable they skip politely, and the acceptance run
// sets it and asserts zero skips.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

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
	return pool
}

// wiringRailDB plants one active payment_config row and returns the vault
// key that seals it, generated per run and never printed: the wiring's own
// vault must hold the same key to open the row, exactly as a box's
// PAYMENT_VAULT_KEY opens its dealers' rows. base_url rides inside the
// sealed creds: the live client prefers it over the host the environment
// names, which is how the stub leg proves nothing dials out.
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
		if _, err := pool.Exec(context.Background(), `DELETE FROM payment_config`); err != nil {
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

// wireCardRail is the composition root's own construction over the test
// doubles: buildCardRail on a plain mux behind the kit's staff guards, the
// way wireTops builds it, so the test drives the production wiring and not
// a copy of it.
func wireCardRail(t *testing.T, pool *pgxpool.Pool, vaultKey string) (*payment.Service, *http.ServeMux, *testkit.StaffGuards) {
	t.Helper()
	sg := testkit.NewStaffGuards(t)
	sg.SetAuthority(identity.StaffAuthority{RoleID: "cashier", Tier: 3, Scope: "branch"})
	mux := http.NewServeMux()
	paySvc, _ := buildCardRail(mux, sg.Guards, pool, config.Config{PaymentVaultKey: vaultKey}, linkedUserID(pool), slog.New(slog.NewTextHandler(io.Discard, nil)))
	return paySvc, mux, sg
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

// TestCardRailWiring_RunSettingChargesAndRefundsReachTheProcessor, HH-178:
// with HOS_CARD_PROCESSOR=run and HOS_CARD_BASE_URL naming the test host,
// the selector's processor is the one the service wires, the till's card
// charge and the pos card refund both reach that host, and the status
// route answers simulated: false (HH-187).
func TestCardRailWiring_RunSettingChargesAndRefundsReachTheProcessor(t *testing.T) {
	ctx := context.Background()
	host, seen := newWiringRunHost(t)
	t.Setenv("APP_ENV", "dev")
	t.Setenv("HOS_CARD_PROCESSOR", "run")
	t.Setenv("HOS_CARD_BASE_URL", host.URL)

	pool := wiringPool(t)
	// The sealed creds carry NO host: the environment's host is the one the
	// live client must dial, the exact shape the exit test names.
	key := wiringRailDB(t, pool, map[string]string{"api_key": "wiring-test-not-a-secret"})
	paySvc, mux, sg := wireCardRail(t, pool, key)

	if paySvc.Simulated() {
		t.Fatal("the service wired the simulator under HOS_CARD_PROCESSOR=run: the selector's choice was discarded again")
	}

	// The till's card charge, through the exact seam the wiring closes.
	charge, err := cardChargerAdapter{svc: paySvc}.Charge(ctx, 55000, "tok_wiring_live", "wiring-charge")
	if err != nil {
		t.Fatalf("till card charge through the wired rail: %v", err)
	}
	if charge.ProcessorRef != "run_wiring_charge_1" {
		t.Fatalf("charge ref = %q, want the processor's trans_id run_wiring_charge_1", charge.ProcessorRef)
	}

	// The pos card refund, through the exact seam the wiring closes.
	ref, err := posCardRefunderAdapter{svc: paySvc}.RefundCard(ctx, charge.ProcessorRef, 55000, "wiring-refund", "idem-wiring-1")
	if err != nil {
		t.Fatalf("card refund through the wired rail: %v", err)
	}
	if ref != "run_wiring_refund_1" {
		t.Fatalf("refund ref = %q, want the processor's trans_id run_wiring_refund_1", ref)
	}

	chargeHits, refundHits := seen.hits()
	if chargeHits != 1 || refundHits != 1 {
		t.Fatalf("the processor host saw %d charge(s) and %d refund(s), want exactly one of each", chargeHits, refundHits)
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
// HOS_CARD_PROCESSOR unset the stub is wired, the charge and the refund
// complete in-process with the stub's reference shapes, nothing reaches
// any server, and the status route answers simulated: true.
func TestCardRailWiring_UnsetSettingRidesTheSimulator(t *testing.T) {
	ctx := context.Background()
	// A host only a wrongly wired live client would dial: the sealed creds
	// carry it (the live client prefers the creds' host over the
	// environment's), and the stub must never look at it.
	host, seen := newWiringRunHost(t)
	t.Setenv("APP_ENV", "dev")
	t.Setenv("HOS_CARD_PROCESSOR", "")
	t.Setenv("HOS_CARD_BASE_URL", "")

	pool := wiringPool(t)
	key := wiringRailDB(t, pool, map[string]string{"api_key": "wiring-test-not-a-secret", "base_url": host.URL})
	paySvc, mux, sg := wireCardRail(t, pool, key)

	if !paySvc.Simulated() {
		t.Fatal("the service wired a live processor with HOS_CARD_PROCESSOR unset: the default must be the simulator")
	}

	charge, err := cardChargerAdapter{svc: paySvc}.Charge(ctx, 55000, "tok_wiring_sim", "wiring-charge-sim")
	if err != nil {
		t.Fatalf("simulated card charge: %v", err)
	}
	if !strings.HasPrefix(charge.ProcessorRef, "rp_sim_") {
		t.Fatalf("simulated charge ref = %q, want the stub's rp_sim_ shape", charge.ProcessorRef)
	}
	ref, err := posCardRefunderAdapter{svc: paySvc}.RefundCard(ctx, charge.ProcessorRef, 55000, "wiring-refund-sim", "idem-wiring-2")
	if err != nil {
		t.Fatalf("simulated card refund: %v", err)
	}
	if !strings.HasPrefix(ref, "rp_ref_") {
		t.Fatalf("simulated refund ref = %q, want the stub's rp_ref_ shape", ref)
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
