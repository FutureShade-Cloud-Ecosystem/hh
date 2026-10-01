//go:build demo

package main

// The role gate on the demo build (the post-review fix routed from W9's
// acceptance run): the demo wiring attaches the same gate the default
// build attaches, so a demo bearer is held to the dealer's configuration
// and not to the tier gates alone. The probes run the REAL
// demoRegisterRoutes over a hand-built demo world: a case database of
// one's own, the frozen migrations, the facts the boot guard reads, and
// the two flow accounts' users rows and staff role grants the populate
// plants, the Sales login inside_sales and the Sales manager login
// sales_manager at a migration-seeded branch. As inside_sales a dispatch
// read the matrix refuses answers 403 with the gate's body; as
// sales_manager the same read answers the handler's own answer; and the
// public leg (the tenant config, registered through the same wrapper)
// answers anonymously unchanged, which is the public family's
// zero-effect rule the demo mint pair will ride when its lane lands (the
// pair is not yet registered on this tree; the census side of its
// promise is the registrar's public pass-through unit test).

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/flows"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

// The flow accounts' platform user ids (the closed flow table's two
// logins) and a migration-seeded branch the grants scope to.
const (
	rgDemoSalesSubject   = "de110000-0000-4000-9000-000000000001"
	rgDemoManagerSubject = "de110000-0000-4000-9000-000000000002"
	rgDemoBranchID       = "5d1b3f6e-0000-4000-9000-000000000001"
)

// TestDemoRoleGateHoldsTheDemoLoginsToTheMatrix is the demo build's half
// of the role gate's exit test.
func TestDemoRoleGateHoldsTheDemoLoginsToTheMatrix(t *testing.T) {
	rec, graph := rgDemoGateCase(t)
	if graph.demoMint == nil {
		t.Fatal("the demo graph carries no mint; the probe cannot hold a credential")
	}
	salesToken, err := graph.demoMint(rgDemoSalesSubject)
	if err != nil {
		t.Fatalf("mint the Sales token: %v", err)
	}
	managerToken, err := graph.demoMint(rgDemoManagerSubject)
	if err != nil {
		t.Fatalf("mint the Sales manager token: %v", err)
	}
	sales := map[string]string{"Authorization": "Bearer " + salesToken, "X-Org": flows.OrgSlug}
	manager := map[string]string{"Authorization": "Bearer " + managerToken, "X-Org": flows.OrgSlug}

	code, body := rgDemoProbe(t, rec, http.MethodGet, "/api/v1/dispatch/board", sales)
	if code != http.StatusForbidden || body != string(authz.BodyForbidden) {
		t.Fatalf("inside sales on the dispatch board: status %d body %s, want the role gate's RBAC-403 (the tier gate alone would admit the read)", code, body)
	}
	code, body = rgDemoProbe(t, rec, http.MethodGet, "/api/v1/dispatch/board", manager)
	if body == string(authz.BodyForbidden) {
		t.Fatalf("sales manager on the dispatch board: the answer wears the gate's refusal body (status %d); the matrix admits the read", code)
	}
	if code == http.StatusUnauthorized || (code == http.StatusServiceUnavailable && body == string(authz.BodyUnavailable)) {
		t.Fatalf("sales manager on the dispatch board: status %d body %s, want the handler's own answer", code, body)
	}

	// The public leg rides the same wrapper and stays public: a demo
	// bearer changes nothing about the answer (ADR-0020 leg 2's zero
	// effect), which is the promise the demo session and flows routes
	// will ride when their lane lands.
	anonCode, anonBody := rgDemoProbe(t, rec, http.MethodGet, "/api/v1/tenant/config", nil)
	bearerCode, bearerBody := rgDemoProbe(t, rec, http.MethodGet, "/api/v1/tenant/config", manager)
	if anonCode != bearerCode || anonBody != bearerBody {
		t.Fatalf("the public leg changed under a demo bearer: anonymous %d %q, bearer %d %q", anonCode, anonBody, bearerCode, bearerBody)
	}
}

// rgHexKey returns a fresh 32 byte key, hex encoded, generated per run
// and never printed.
func rgHexKey(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return hex.EncodeToString(raw)
}

// rgDemoGateCase builds the demo world on a case database of its own and
// wires the REAL demo graph over a recording registrar, so the probes
// ride the served handlers.
func rgDemoGateCase(t *testing.T) (*recordingRegistrar, *appGraph) {
	t.Helper()
	caseURL := rgDemoCaseDatabase(t)
	pool, err := pgxpool.New(context.Background(), caseURL)
	if err != nil {
		t.Fatalf("case pool: %v", err)
	}
	defer pool.Close()
	rgPlantDemoWorld(t, pool)

	signKey := rgHexKey(t)
	getenv := func(k string) string {
		return map[string]string{
			"CORE_DATABASE_URL":         caseURL,
			"CORE_STAFF_AUDIENCE":       "hh-staff",
			"CORE_TENANT_AUDIENCE":      "hh-tenant",
			"CORE_STREAM_TICKET_KEY":    "fx-ticket-key",
			"HH_ACCEPTANCE_SIGN_KEY_ID": "fx-accept-sign-key",
			"HH_ACCEPTANCE_SIGN_KEY":    signKey,
		}[k]
	}
	rec := &recordingRegistrar{inner: http.NewServeMux()}
	graph, err := demoRegisterRoutes(rec, getenv)
	if err != nil {
		t.Fatalf("demoRegisterRoutes: %v", err)
	}
	t.Cleanup(graph.pool.Close)
	if len(rec.patterns) == 0 {
		t.Fatal("demoRegisterRoutes registered nothing; the probe surface is empty")
	}
	return rec, graph
}

// rgDemoProbe issues one request against the demo surface with the probe
// path's parameters substituted the x-role suite's way.
func rgDemoProbe(t *testing.T, rec *recordingRegistrar, method, path string, headers map[string]string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, xrProbePath(path), nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rsp := httptest.NewRecorder()
	rec.inner.ServeHTTP(rsp, req)
	return rsp.Code, rsp.Body.String()
}

// rgDemoCaseDatabase creates one database on the gate server for this
// test alone, applies the frozen migration set, and drops it after: the
// boot guard's own suite shape. Absent the gate variable the leg skips.
func rgDemoCaseDatabase(t *testing.T) string {
	t.Helper()
	base := os.Getenv(coreDBEnvVar)
	if base == "" {
		t.Skip(coreDBEnvVar + " is not set: the demo role gate leg needs a scratch Postgres it may create databases in")
	}
	adminURL, err := rgSwapDSNDatabase(base, "postgres")
	if err != nil {
		t.Fatalf("%s is not a database URL: %v", coreDBEnvVar, err)
	}
	admin, err := pgx.Connect(context.Background(), adminURL)
	if err != nil {
		t.Fatalf("connect to the maintenance database: %v", err)
	}
	const name = "rg_demo_gate"
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
	caseURL, err := rgSwapDSNDatabase(base, name)
	if err != nil {
		t.Fatalf("%s is not a database URL: %v", coreDBEnvVar, err)
	}
	pool, err := pgxpool.New(context.Background(), caseURL)
	if err != nil {
		t.Fatalf("case pool: %v", err)
	}
	defer pool.Close()
	rgApplyFrozenMigrations(t, pool)
	return caseURL
}

// rgSwapDSNDatabase replaces a postgres URL's database name, keeping the
// query string intact.
func rgSwapDSNDatabase(baseURL, dbName string) (string, error) {
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

// rgApplyFrozenMigrations applies the migration set exactly as the
// migrate role does: numbered files in order, each in its own
// transaction, recorded in schema_migrations.
func rgApplyFrozenMigrations(t *testing.T, pool *pgxpool.Pool) {
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

// rgPlantDemoWorld plants the facts the boot guard reads (the populate's
// own moves in miniature: the one demo org, the demo_box row, no active
// outsider accounts, the demo account active at the reserved domain, the
// demo's invented payment configuration) plus the two flow logins' users
// rows and staff role grants, so the authority resolution finds the
// matrix roles behind the demo bearers.
func rgPlantDemoWorld(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	var orgID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO org (slug, name) VALUES ($1, $2) RETURNING id::text`,
		flows.OrgSlug, "The demo dealer").Scan(&orgID); err != nil {
		t.Fatalf("plant the demo org: %v", err)
	}
	steps := []struct {
		why  string
		sql  string
		args []any
	}{
		{"the box table", `CREATE TABLE demo_box (marker TEXT)`, nil},
		{"the box row", `INSERT INTO demo_box (marker) VALUES ($1)`, []any{flows.PopulateMarker}},
		{"no active outsider accounts", `UPDATE users SET status = 'SUSPENDED' WHERE status = 'ACTIVE'`, nil},
		{"the demo account", `INSERT INTO users (email, display_name, status) VALUES ($1, $2, 'ACTIVE')`,
			[]any{"sales@" + flows.ReservedDomain, "Sales (demo)"}},
		{"the demo payment configuration", `INSERT INTO payment_config
			(processor, merchant_id, currency, credentials_ciphertext, key_version, base_url, status)
			VALUES ('run_payments', $1, 'CAD', decode('00', 'hex'), 1, '', 'active')`, []any{flows.DemoMerchantID}},
		{"the Sales login", `INSERT INTO users (email, display_name, status, platform_user_id, org_id)
			VALUES ($1, $2, 'ACTIVE', $3, $4)`,
			[]any{"sales.login@" + flows.ReservedDomain, "Sales", rgDemoSalesSubject, orgID}},
		{"the Sales manager login", `INSERT INTO users (email, display_name, status, platform_user_id, org_id)
			VALUES ($1, $2, 'ACTIVE', $3, $4)`,
			[]any{"sales.manager@hh-demo.invalid", "Sales manager (demo)", rgDemoManagerSubject, orgID}},
		{"the Sales grant", `INSERT INTO staff_role_grant (platform_user_id, role_id, org_id, branch_id)
			VALUES ($1, 'inside_sales', $2, $3)`,
			[]any{rgDemoSalesSubject, orgID, rgDemoBranchID}},
		{"the Sales manager grant", `INSERT INTO staff_role_grant (platform_user_id, role_id, org_id, branch_id)
			VALUES ($1, 'sales_manager', $2, $3)`,
			[]any{rgDemoManagerSubject, orgID, rgDemoBranchID}},
	}
	for _, s := range steps {
		if _, err := pool.Exec(ctx, s.sql, s.args...); err != nil {
			t.Fatalf("plant %s: %v", s.why, err)
		}
	}
}
