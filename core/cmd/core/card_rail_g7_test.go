//go:build demo

package main

// card_rail_g7_test.go is G7's evidence (plan 4A, row PX), in three layers.
//
// The wiring level: a demo wiring given a live processor (HOS_CARD_PROCESSOR
// =run at a local httptest base URL) fails demoRefuseLiveCardRail and the
// refusal names its own check; the simulator wiring passes it; a nil rail
// refuses too. Both legs construct the rail through buildCardRail itself,
// the composition root's one construction, over a nil pool: registration
// touches no table, so the choice of processor is provable without a
// database.
//
// The serve level, through the REAL demoServe. The live leg drives
// demoServe with a live card processor and asserts the exit clause's
// refusal half: the serve role returns, and the listener never opens. In
// process the environment layer refuses first (G4 scans what the box
// declared and stops the boot naming the variable; no guard relies on
// another), which is exactly why the third layer exists. The simulator leg
// hand-builds the minimal demo-passing world on the test's own database
// (a fresh database on the gate server, the frozen migration set applied,
// then the facts G3 reads: the one hh-demo org row, the populate's
// demo_box row, every migration-planted user deactivated with the demo's
// account active at the reserved domain, and the demo's one invented
// payment configuration with no host), starts demoServe, and proves the
// serve half: the listener opens and /healthz answers, the serve role
// stops on its shutdown signal and reports no error.
//
// The pin: a source-order test holds demoServe to ACTING ON the guard at
// its marked spot: the call sits after the graph build and before the
// listener, and its error is checked and returned. Deleting the call,
// replacing it with a blank assignment, dropping the return, or moving the
// acting behind the listener fails this test in every demo-tagged run,
// gated or not, which is the bite: the serve-level live leg cannot tell
// G7's refusal from the environment layer's, so the pin is what holds the
// serving path to the refusal's consequence.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/config"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/flows"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// g7WiredRail builds the card rail the way wireTops builds it, over the
// kit's DB-free guards and a nil pool, picking up whatever rail variables
// the caller set.
func g7WiredRail(t *testing.T) *payment.Service {
	t.Helper()
	sg := testkit.NewStaffGuards(t)
	paySvc, _ := buildCardRail(http.NewServeMux(), sg.Guards, nil, config.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return paySvc
}

// TestDemoG7_LiveCardRailRefusesToServe: a demo wiring on a live processor
// is a refusal, with the sentence that names the check, and nothing
// reaches the processor host (the refusal fires on what was wired, before
// any call exists to make).
func TestDemoG7_LiveCardRailRefusesToServe(t *testing.T) {
	var hitsMu sync.Mutex
	var hits int
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitsMu.Lock()
		hits++
		hitsMu.Unlock()
	}))
	t.Cleanup(host.Close)
	t.Setenv("APP_ENV", "dev")
	t.Setenv("HOS_CARD_PROCESSOR", "run")
	t.Setenv("HOS_CARD_BASE_URL", host.URL)

	paySvc := g7WiredRail(t)
	if paySvc.Simulated() {
		t.Fatal("the wiring built the simulator under HOS_CARD_PROCESSOR=run, there is nothing for G7 to refuse")
	}
	err := demoRefuseLiveCardRail(paySvc)
	if err == nil {
		t.Fatal("a demo wiring on a live processor passed the G7 refusal: the demo build would open a listener on a live rail")
	}
	if !strings.Contains(err.Error(), "G7") || !strings.Contains(err.Error(), "not the simulator") {
		t.Fatalf("the refusal must name its own check, got %q", err)
	}
	hitsMu.Lock()
	defer hitsMu.Unlock()
	if hits != 0 {
		t.Fatalf("the live rail made %d request(s) during wiring, want none", hits)
	}
}

// TestDemoG7_SimulatorCardRailServes: with the setting absent the stub is
// what the wiring builds, and that is the rail the demo build serves.
func TestDemoG7_SimulatorCardRailServes(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	unsetForTest(t, "HOS_CARD_PROCESSOR")
	unsetForTest(t, "HOS_CARD_BASE_URL")

	paySvc := g7WiredRail(t)
	if !paySvc.Simulated() {
		t.Fatal("the wiring did not build the simulator with HOS_CARD_PROCESSOR absent: the demo default must be the stub")
	}
	if err := demoRefuseLiveCardRail(paySvc); err != nil {
		t.Fatalf("the simulator wiring must pass the G7 refusal: %v", err)
	}
}

// TestDemoG7_ANilCardRailRefusesToo: the guard fails closed. A rail that
// cannot say what it is never serves a demo box.
func TestDemoG7_ANilCardRailRefusesToo(t *testing.T) {
	if err := demoRefuseLiveCardRail(nil); err == nil {
		t.Fatal("a nil card rail passed the G7 refusal: the guard must fail closed")
	}
}

// TestDemoServeRefusesALiveCardRailBeforeTheListener, the serve role's
// refusal half through the REAL demoServe: a box that says demo twice and
// carries a live card processor is refused, with the refusal naming the
// card processor variable. That returned refusal is the leg's whole
// evidence: demoServe returns before it builds the server and opens the
// listener, so a refusal precedes serving by construction, and the
// simulator leg beside this one proves the serve half actually serves.
func TestDemoServeRefusesALiveCardRailBeforeTheListener(t *testing.T) {
	t.Setenv("HOS_CARD_PROCESSOR", "run")
	err := demoServe(func(string) string { return "" }, demoSaysDemoTwice(), os.Environ)
	if err == nil {
		t.Fatal("demoServe accepted a live card processor: the demo build would serve on a live rail")
	}
	if !strings.Contains(err.Error(), "HOS_CARD_PROCESSOR") {
		t.Fatalf("the refusal %q does not name the card processor variable", err)
	}
}

// TestDemoServeServesOnTheSimulatorCardRail, the serve half through the
// REAL demoServe over the hand-built demo world: the graph builds, the G7
// spot passes on the simulator rail, the listener opens and /healthz
// answers, and the serve role stops cleanly on its shutdown signal.
func TestDemoServeServesOnTheSimulatorCardRail(t *testing.T) {
	caseURL := demoServeCase(t)
	port := freeServePort(t)
	t.Setenv("APP_ENV", "demo")
	t.Setenv("DEMO_MODE", "true")
	t.Setenv("PAYMENT_VAULT_KEY", hexKey(t))
	unsetForTest(t, "HOS_CARD_PROCESSOR")
	unsetForTest(t, "HOS_CARD_BASE_URL")

	signKey := hexKey(t)
	getenv := func(k string) string {
		return map[string]string{
			"CORE_DATABASE_URL":         caseURL,
			"CORE_STAFF_AUDIENCE":       "hh-staff",
			"CORE_TENANT_AUDIENCE":      "hh-tenant",
			"CORE_STREAM_TICKET_KEY":    "fx-ticket-key",
			"CORE_LISTEN_ADDR":          fmt.Sprintf("127.0.0.1:%d", port),
			"HH_ACCEPTANCE_SIGN_KEY_ID": "fx-accept-sign-key",
			"HH_ACCEPTANCE_SIGN_KEY":    signKey,
		}[k]
	}

	errCh := make(chan error, 1)
	go func() { errCh <- demoServe(getenv, demoSaysDemoTwice(), os.Environ) }()

	if !waitUntilServes(t, port) {
		select {
		case err := <-errCh:
			t.Fatalf("demoServe refused or failed before serving: %v", err)
		default:
			t.Fatalf("the simulator wiring never opened a listener on %d that answers /healthz", port)
		}
	}
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("signal the serve role: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("demoServe reported an error on shutdown: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("demoServe did not stop on its shutdown signal")
	}
}

// TestDemoServeRunsTheCardRailGuardAtTheMarkedSpot is the pin: demoServe
// ACTS ON the guard's answer at the marked spot: the call sits between the
// graph build and the listener, and its error is checked and returned, not
// computed and dropped. Deleting the call, replacing it with a blank
// assignment, moving it behind the listener, or dropping the return all
// fail here in every demo-tagged run. What the guard's answer is worth is
// the wiring-level legs' business above; this pin holds the serving path
// to acting on it.
func TestDemoServeRunsTheCardRailGuardAtTheMarkedSpot(t *testing.T) {
	src, err := os.ReadFile("demo_on.go")
	if err != nil {
		t.Fatalf("read demo_on.go: %v", err)
	}
	serveStart := bytes.Index(src, []byte("func demoServe("))
	routesCall := bytes.Index(src, []byte("graph, err := demoRegisterRoutes("))
	guardBlob := []byte("if err := demoRefuseLiveCardRail(graph.tops.cardRail); err != nil {\n\t\treturn err\n\t}")
	guardPos := bytes.Index(src, guardBlob)
	listenPos := bytes.Index(src, []byte("srv.ListenAndServe()"))

	if serveStart < 0 || routesCall < 0 || listenPos < 0 {
		t.Fatal("demo_on.go no longer shows the serve shape this pin guards: demoServe, the graph build or the listener moved")
	}
	if guardPos < 0 {
		t.Fatal("demoServe no longer acts on the card rail guard (the call is gone, not returned on, or reformatted away): the demo build is free to serve on a live rail")
	}
	if n := bytes.Count(src, guardBlob); n != 1 {
		t.Fatalf("the acted-on card rail guard appears %d times, want the one marked-spot call", n)
	}
	if !(serveStart < routesCall && routesCall < guardPos && guardPos < listenPos) {
		t.Fatal("the card rail guard is not acted on between the graph build and the listener: plan 4A places the refusal after wiring, before serving")
	}
}

// demoSaysDemoTwice is the lookup double of a box that passes G2.
func demoSaysDemoTwice() func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := map[string]string{"DEMO_MODE": "true", "APP_ENV": "demo"}[k]
		return v, ok
	}
}

// freeServePort reserves an ephemeral port and gives it back.
func freeServePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// hexKey returns a fresh 32 byte key, hex encoded, generated per run and
// never printed.
func hexKey(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return hex.EncodeToString(raw)
}

// waitUntilServes polls /healthz until it answers 200 or the deadline
// passes.
func waitUntilServes(t *testing.T, port int) bool {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", port))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// demoServeCase builds the minimal demo-passing world on the test's OWN
// database, created on the gate variable's server: the frozen migration
// set applied, then the facts G3 reads. Nothing on the shared scratch
// database is touched. Absent the gate variable the leg skips politely.
func demoServeCase(t *testing.T) string {
	t.Helper()
	base := os.Getenv(coreDBEnvVar)
	if base == "" {
		t.Skip(coreDBEnvVar + " is not set: the demo serve leg needs a scratch Postgres it may create databases in")
	}
	caseURL := createCaseDatabase(t, base, "px_g7_serve")
	pool, err := pgxpool.New(context.Background(), caseURL)
	if err != nil {
		t.Fatalf("case pool: %v", err)
	}
	t.Cleanup(pool.Close)
	applyFrozenMigrations(t, pool)
	plantDemoWorld(t, pool)
	return caseURL
}

// createCaseDatabase creates one database on the gate server for this test
// alone and drops it after, the boot package's G3 suite shape.
func createCaseDatabase(t *testing.T, baseURL, name string) string {
	t.Helper()
	adminURL, err := swapDemoDSNDatabase(baseURL, "postgres")
	if err != nil {
		t.Fatalf("%s is not a database URL: %v", coreDBEnvVar, err)
	}
	admin, err := pgx.Connect(context.Background(), adminURL)
	if err != nil {
		t.Fatalf("connect to the maintenance database: %v", err)
	}
	if _, err := admin.Exec(context.Background(), fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, name)); err != nil {
		t.Fatalf("drop case database: %v", err)
	}
	if _, err := admin.Exec(context.Background(), fmt.Sprintf(`CREATE DATABASE %s`, name)); err != nil {
		t.Fatalf("create case database: %v", err)
	}
	t.Cleanup(func() {
		if again, cerr := pgx.Connect(context.Background(), adminURL); cerr == nil {
			_, _ = again.Exec(context.Background(), fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, name))
			_ = again.Close(context.Background())
		}
	})
	if err := admin.Close(context.Background()); err != nil {
		t.Fatalf("close maintenance connection: %v", err)
	}
	caseURL, err := swapDemoDSNDatabase(baseURL, name)
	if err != nil {
		t.Fatalf("%s is not a database URL: %v", coreDBEnvVar, err)
	}
	return caseURL
}

// swapDemoDSNDatabase replaces a postgres URL's database name, keeping the
// query string intact.
func swapDemoDSNDatabase(baseURL, dbName string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("not an absolute database URL")
	}
	u.Path = "/" + dbName
	return u.String(), nil
}

// applyFrozenMigrations applies the migration set exactly as the migrate
// role does: numbered files in order, each in its own transaction,
// recorded in schema_migrations.
func applyFrozenMigrations(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	dir := filepath.Join("..", "..", "db", "migrations")
	if _, err := pool.Exec(context.Background(), `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		t.Fatalf("migration bookkeeping: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("read migrations: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		version := strings.TrimSuffix(name, ".sql")
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Fatalf("begin %s: %v", version, err)
		}
		if _, err := tx.Exec(context.Background(), string(body)); err != nil {
			_ = tx.Rollback(context.Background())
			t.Fatalf("apply %s: %v", version, err)
		}
		if _, err := tx.Exec(context.Background(), `INSERT INTO schema_migrations(version) VALUES($1)`, version); err != nil {
			_ = tx.Rollback(context.Background())
			t.Fatalf("record %s: %v", version, err)
		}
		if err := tx.Commit(context.Background()); err != nil {
			t.Fatalf("commit %s: %v", version, err)
		}
	}
}

// plantDemoWorld plants the facts G3 reads, the populate's own moves in
// miniature: the one hh-demo org row, the demo_box row only the populate
// writes, every migration-planted user deactivated (the populate makes the
// real-domain rows inactive) with the demo's account active at the
// reserved domain, and the demo's one invented payment configuration with
// no host.
func plantDemoWorld(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	steps := []struct {
		why  string
		sql  string
		args []any
	}{
		{"the demo org", `INSERT INTO org (slug, name) VALUES ($1, $2)`, []any{flows.OrgSlug, "The demo dealer"}},
		{"the populate's box table", `CREATE TABLE demo_box (singleton TEXT PRIMARY KEY, populated_at TIMESTAMPTZ NOT NULL, marker TEXT NOT NULL)`, nil},
		{"the box row", `INSERT INTO demo_box VALUES ('demo', now(), 'hh-demo-populate-v1')`, nil},
		{"no active outsider accounts", `UPDATE users SET status = 'SUSPENDED' WHERE status = 'ACTIVE'`, nil},
		{"the demo account", `INSERT INTO users (email, display_name, status) VALUES ($1, $2, 'ACTIVE')`,
			[]any{"sales@" + flows.ReservedDomain, "Sales (demo)"}},
		{"the demo payment configuration", `INSERT INTO payment_config
			(processor, merchant_id, currency, credentials_ciphertext, key_version, base_url, status)
			VALUES ('run_payments', $1, 'CAD', decode('00', 'hex'), 1, '', 'active')`, []any{flows.DemoMerchantID}},
	}
	for _, s := range steps {
		if _, err := pool.Exec(context.Background(), s.sql, s.args...); err != nil {
			t.Fatalf("plant %s: %v", s.why, err)
		}
	}
}
