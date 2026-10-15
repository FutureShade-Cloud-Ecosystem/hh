//go:build demo

package main

// demoseed_quotes_world_test.go, QD-6: proof of the quote world, on a throwaway local Postgres.
//
// The test builds the world the order a demo box does: the frozen migration set, the first
// `demoseed -apply` (the demo role logins are not there yet, so the world holds no quote), the REAL
// demo populate in process (the two role accounts, their roles, branch grants and platform links),
// then `demoseed -apply` again, which now adds the quotes. Everything after runs through the real
// registerRoutes as the demo Sales login and the demo Sales manager login, plus an HR login planted
// beside them for the one negative leg.
//
// The database gate is the demo world proof's own variable (the till's world test reads the same one),
// and the harness refuses an address that is not local.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customerquote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// The two role accounts the demo populate plants (core/internal/demo/populate.go, pinned there). The
// literals are repeated here on purpose: if the populate ever moves them, the quote world stops
// resolving its owners and this test goes red, which is the point.
const (
	qwSalesUserID   = "de110000-0000-4000-9000-000000000001"
	qwManagerUserID = "de110000-0000-4000-9000-000000000002"
	qwHRSubject     = "hh-demo-quotes-hr"
)

// qwClock pins every apply in this test: one instant, so the day-keyed rows are the same on every
// run and a second apply is the same plan as the first.
var qwClock = time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

// qwQuoteTables are the tables the quote world writes, the census of "what the second apply added".
var qwQuoteTables = []string{
	"quotes", "quote_lines", "quote_share_links", "quote_acceptances", "quote_share_link_events",
}

// qwOtherWorldTables are the tables the rest of the demo world writes (the till's, the order book's,
// dispatch's, picking's, the freight approvals'). They are fingerprinted before and after the apply
// that adds the quotes: nothing in them may move.
var qwOtherWorldTables = []string{
	"credit_holds", "customers", "delivery_freight_raises", "delivery_runs", "delivery_stops",
	"dispatch_branch_depots", "dispatch_drivers", "dispatch_reschedules", "dispatch_trucks",
	"invoice_lines", "invoices", "load_scan_checkpoints", "order_lines", "orders",
	"payment_allocations", "payments", "pos_parked_tickets", "pos_sale", "pos_signature",
	"pricing_list_prices", "pricing_rules", "product_branch_costs", "product_images",
	"product_reorder_points", "product_uoms", "products", "quantity_break_headers",
	"quantity_break_items", "stock_balances", "stock_moves", "task_events", "task_shortages",
	"tasks", "will_call_pickups",
	"users", "user_roles", "user_branch_grants", "staff_role_grant", "payment_config", "org",
	"system_settings", "demo_box",
}

// qwCreateDB creates the test's own database from zero. The name is the caller's, so each world
// proof holds a database of its own and two proofs cannot collide on one name. Nothing is applied
// here: the first boot proof runs the entrypoint's steps between the empty database and migrate,
// so the migration set has its own helper below.
func qwCreateDB(t *testing.T, ctx context.Context, dsn, name string) (*pgxpool.Pool, string) {
	t.Helper()
	admin, err := pgxpool.New(ctx, swapDSNDatabase(dsn, "postgres"))
	if err != nil {
		t.Fatalf("connecting to the admin database: %v", err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
		t.Fatalf("dropping %s: %v", name, err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}
	testDSN := swapDSNDatabase(dsn, name)
	pool, err := pgxpool.New(ctx, testDSN)
	if err != nil {
		t.Fatalf("connecting to %s: %v", name, err)
	}
	t.Cleanup(pool.Close)
	return pool, testDSN
}

// qwMigrateFrozenSet runs what the migrate role runs (roles_oneshot.go migrateRole): the numbered
// files of the frozen set, in sort order, each in one transaction, recorded in schema_migrations,
// and an already recorded version is skipped, so a second call is the role's own no-op.
func qwMigrateFrozenSet(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		t.Fatalf("migrate: bookkeeping table: %v", err)
	}
	files, err := filepath.Glob(filepath.Join("..", "..", "db", "migrations", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations found (err=%v)", err)
	}
	sort.Strings(files)
	for _, f := range files {
		version := strings.TrimSuffix(filepath.Base(f), ".sql")
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&exists); err != nil {
			t.Fatalf("migrate: check %s: %v", version, err)
		}
		if exists {
			continue
		}
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", filepath.Base(f), err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("migrate: begin %s: %v", version, err)
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("migrate: applying %s: %v", filepath.Base(f), err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, version); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("migrate: recording %s: %v", version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("migrate: committing %s: %v", version, err)
		}
	}
}

// qwFreshDB creates the test's own database from zero and applies the migration set, the same
// files and order the migrate role applies.
func qwFreshDB(t *testing.T, ctx context.Context, dsn, name string) (*pgxpool.Pool, string) {
	t.Helper()
	pool, testDSN := qwCreateDB(t, ctx, dsn, name)
	qwMigrateFrozenSet(t, ctx, pool)
	return pool, testDSN
}

// qwApply runs `demoseed -apply` the way the entrypoint does, at the pinned clock, and returns how
// many rows it proposed and created.
func qwApply(t *testing.T) (proposed, created int) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "demoseed-out")
	if err != nil {
		t.Fatalf("temp output: %v", err)
	}
	defer f.Close()
	if err := demoseedRole([]string{"-apply", "-now", qwClock.Format(time.RFC3339)}, f, os.Getenv); err != nil {
		t.Fatalf("demoseed -apply: %v", err)
	}
	raw, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("reading demoseed output: %v", err)
	}
	var skipped int
	for _, line := range strings.Split(string(raw), "\n") {
		if n, err := fmt.Sscanf(line, "applied: %d proposed · %d created · %d already present",
			&proposed, &created, &skipped); err == nil && n == 3 {
			return proposed, created
		}
	}
	t.Fatalf("demoseed -apply printed no result line:\n%s", raw)
	return 0, 0
}

// qwFingerprint fingerprints every row of the tables, contents and row versions, so an assertion can
// hold that a run changed nothing at all rather than merely created nothing.
//
// The contractor account's documents are left out of the tables that hold them (qwWithoutContractor):
// the apply that adds the quote world adds the contractor world in the same transaction (the account
// exists by then), and demoseed_contractor_plan_test.go and internal/demo/world_contractor_test.go
// hold that world; these fingerprints are about the OTHER worlds not moving.
func qwFingerprint(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tables []string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, table := range tables {
		var sum string
		if err := pool.QueryRow(ctx, `
			SELECT md5(coalesce(string_agg(line, E'\n' ORDER BY line), 'empty'))
			  FROM (SELECT to_jsonb(r)::text AS line
			          FROM (SELECT *, xmin::text AS row_version FROM `+table+qwWithoutContractor(table)+`) r) s`).Scan(&sum); err != nil {
			t.Fatalf("fingerprint %s: %v", table, err)
		}
		out[table] = sum
	}
	return out
}

// qwWithoutContractor is the WHERE clause that leaves the contractor account's own rows out of a table
// (the account is contractorCustomerID; a line table is reached through its document).
func qwWithoutContractor(table string) string {
	c := "'" + contractorCustomerID + "'::uuid"
	switch table {
	case "orders", "invoices", "payments", "quotes":
		return " WHERE customer_id <> " + c
	case "quote_lines":
		return " WHERE quote_id NOT IN (SELECT id FROM quotes WHERE customer_id = " + c + ")"
	case "order_lines":
		return " WHERE order_id NOT IN (SELECT id FROM orders WHERE customer_id = " + c + ")"
	case "invoice_lines":
		return " WHERE invoice_id NOT IN (SELECT id FROM invoices WHERE customer_id = " + c + ")"
	case "payment_allocations":
		return " WHERE payment_id NOT IN (SELECT id FROM payments WHERE customer_id = " + c + ")"
	}
	return ""
}

// qwDriverDayIDs are the Driver's day-keyed run and stop ids on the pinned clock's day: the rows
// the seed's org stamp addresses. The stamp cannot land on the first apply (the org row does not
// exist until the populate provisions it), so it lands on the first apply AFTER the populate, which
// in this world is the one that adds the quotes. The fingerprints around that apply keep these rows
// in, minus the three columns the stamp itself moves (see qwDriverStampColumns), and the assertion
// below checks the stamp landed on exactly them, so the exception is named and held, not a hole.
func qwDriverDayIDs() []string {
	stamp := qwClock.In(seedLocation()).Format("20060102")
	ids := []string{demoID(dayKey(stamp, "driver-run"))}
	ids = append(ids, driverDayStopIDs(stamp)...)
	return ids
}

// qwDriverStampColumns are the only columns the org stamp moves on a Driver day row: the org it
// sets, the updated_at it bumps, and the row version (xmin) any update changes.
var qwDriverStampColumns = []string{"org_id", "updated_at", "row_version"}

// qwTaskRepairColumns are the only columns the task sync repair (repairDemoTaskSync) moves on a
// demo task row: the org it stamps, the seq it draws from the type's counter, and the row version
// (xmin) any update changes. The repair needs the org row, which the populate provisions after the
// first apply, so like the Driver's stamp it can only land on the first apply after the populate,
// which is the apply that adds the quotes.
var qwTaskRepairColumns = []string{"org_id", "seq", "row_version"}

// qwDemoTaskIDs are the plan's own task ids, the rows the task sync repair addresses.
func qwDemoTaskIDs() []string {
	ids := make([]string, 0, len(demoTasks))
	for _, tk := range demoTasks {
		ids = append(ids, demoID("task:"+tk.Key))
	}
	return ids
}

// qwFingerprintStamped fingerprints one delivery table with the day-keyed Driver rows named by
// except kept in, but with qwDriverStampColumns removed from those rows alone. Every other column of
// those rows, and every column of every other row, still counts, so a status, a date or a driver id
// moving on them is seen.
func qwFingerprintStamped(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string, except, cols []string) string {
	t.Helper()
	var sum string
	if err := pool.QueryRow(ctx, `
		SELECT md5(coalesce(string_agg(line, E'\n' ORDER BY line), 'empty'))
		  FROM (SELECT CASE WHEN id::text = ANY($1::text[]) THEN to_jsonb(r) - $2::text[] ELSE to_jsonb(r) END::text AS line
		          FROM (SELECT *, xmin::text AS row_version FROM `+table+`) r) s`,
		except, cols).Scan(&sum); err != nil {
		t.Fatalf("fingerprint %s: %v", table, err)
	}
	return sum
}

func qwCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, q string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// qwPublic calls a public (tokenless) route.
func qwPublic(mux *http.ServeMux, path string) demoResponse {
	req := httptest.NewRequest("GET", path, bytes.NewReader(nil))
	req.Header.Set("X-Org", demoOrgSlug)
	rsp := httptest.NewRecorder()
	mux.ServeHTTP(rsp, req)
	return demoResponse{status: rsp.Code, body: rsp.Body.Bytes()}
}

func qwPublicPost(mux *http.ServeMux, path, body string) demoResponse {
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("X-Org", demoOrgSlug)
	req.Header.Set("Content-Type", "application/json")
	rsp := httptest.NewRecorder()
	mux.ServeHTTP(rsp, req)
	return demoResponse{status: rsp.Code, body: rsp.Body.Bytes()}
}

type qwLine struct {
	LineNo              int    `json:"line_no"`
	ProductID           string `json:"product_id"`
	UnitPriceCents      int64  `json:"unit_price_cents"`
	LineTotalCents      int64  `json:"line_total_cents"`
	RulePriceBelowFloor *bool  `json:"rule_price_below_floor"`
	FloorClearedBy      string `json:"floor_cleared_by"`
}

type qwQuote struct {
	ID            string   `json:"id"`
	QuoteNo       string   `json:"quote_no"`
	Status        string   `json:"status"`
	BranchID      string   `json:"branch_id"`
	OwnerID       string   `json:"owner_id"`
	CanConvert    bool     `json:"can_convert"`
	Lines         []qwLine `json:"lines"`
	SubtotalCents int64    `json:"subtotal_cents"`
	TaxCents      int64    `json:"tax_cents"`
	TotalCents    int64    `json:"total_cents"`
}

func TestDemoWorld_Quotes(t *testing.T) {
	dsn := os.Getenv(tillDemoDBEnvVar)
	if dsn == "" {
		t.Skip(tillDemoDBEnvVar + " is not set: the demo world proof needs its own throwaway local Postgres")
	}
	requireLocalDSN(t, dsn)
	ctx := context.Background()

	rawKey := make([]byte, 32)
	if _, err := rand.Read(rawKey); err != nil {
		t.Fatalf("generate vault key: %v", err)
	}
	vaultKeyHex := hex.EncodeToString(rawKey)

	// 1. migrate, then the first apply exactly as a box runs it: the role logins are not there yet.
	pool, testDSN := qwFreshDB(t, ctx, dsn, "demoworld_quotes")
	t.Setenv("DATABASE_URL", testDSN)
	t.Setenv("CORE_DATABASE_URL", testDSN)
	t.Setenv("PAYMENT_VAULT_KEY", vaultKeyHex)
	t.Setenv("APP_ENV", "dev")
	_, created := qwApply(t)
	if created == 0 {
		t.Fatal("the first apply created nothing on a fresh database")
	}
	// The migrations plant their own portal fixtures (a quote among them), so the baseline is what a
	// migrated and once-seeded database holds, and the world's own rows are told apart by its marker.
	baseline := map[string]int{}
	for _, table := range append(append([]string{}, qwQuoteTables...), "pricing_audit_log", "permission_approval_requests") {
		baseline[table] = qwCount(t, ctx, pool, `SELECT count(*) FROM `+table)
	}
	if n := qwCount(t, ctx, pool, `SELECT count(*) FROM quotes WHERE metadata->>'demo_seed' = $1`, demoMarker); n != 0 {
		t.Fatalf("the first apply (no demo role logins yet) left %d demo quotes, the world must hold no quote until the logins exist", n)
	}
	if n := qwCount(t, ctx, pool, `SELECT count(*) FROM permission_approval_requests WHERE policy_type = 'MARGIN_BELOW_MINIMUM'`); n != 0 {
		t.Fatalf("the first apply filed %d floor requests with no requester", n)
	}

	// 2. the REAL demo populate, in process: the two role accounts, their grants and platform links.
	now := qwClock
	if _, err := demo.RunPopulate(ctx, demo.Options{Getenv: os.Getenv, Now: &now}); err != nil {
		t.Fatalf("demo populate: %v", err)
	}
	for id, role := range map[string]string{qwSalesUserID: "INSIDE_SALES", qwManagerUserID: "SALES_MANAGER"} {
		var got string
		if err := pool.QueryRow(ctx, `
			SELECT sr.origin_code FROM users u
			  JOIN staff_role_grant g ON g.platform_user_id = u.platform_user_id AND g.deleted_at IS NULL
			  JOIN staff_role sr ON sr.id = g.role_id WHERE u.id = $1`, id).Scan(&got); err != nil || got != role {
			t.Fatalf("demo account %s reads as %q (%v), want %s", id, got, err, role)
		}
	}

	// 3. the second apply: now the logins resolve, so the quote world is added, and nothing else moves.
	// The Driver's day-keyed rows stay in the two delivery fingerprints with only the org stamp's
	// columns taken out (see qwDriverStampColumns): the stamp they take on this apply is the one
	// intended movement beside the quote world, checked by name below instead of hiding inside
	// "nothing else moved".
	driverDayIDs := qwDriverDayIDs()
	taskIDs := qwDemoTaskIDs()
	// The task sync repair is the other named mover on this apply: every demo task enters it
	// unstamped (no seq, so on no sync feed) and leaves it stamped, with only qwTaskRepairColumns
	// moved (the fingerprint below keeps every other column of these rows in).
	if n := qwCount(t, ctx, pool, `SELECT count(*) FROM tasks WHERE id = ANY($1::uuid[]) AND seq IS NULL`, taskIDs); n != len(taskIDs) {
		t.Fatalf("%d of %d demo tasks enter the second apply unstamped, want all of them", n, len(taskIDs))
	}
	fingerprint := func() map[string]string {
		out := map[string]string{}
		for _, table := range qwOtherWorldTables {
			if table == "delivery_runs" || table == "delivery_stops" {
				out[table] = qwFingerprintStamped(t, ctx, pool, table, driverDayIDs, qwDriverStampColumns)
				continue
			}
			if table == "tasks" {
				out[table] = qwFingerprintStamped(t, ctx, pool, table, taskIDs, qwTaskRepairColumns)
				continue
			}
			out[table] = qwFingerprint(t, ctx, pool, []string{table})[table]
		}
		return out
	}
	before := fingerprint()
	proposed, created := qwApply(t)
	const wantQuoteRows = 5 + 10 + 3 + 2 + 2 + 2 + 4 + 2 + controllerWorldRows + contractorWorldRows // quotes, lines, links, acceptances, audit rows, events, log rows, requests, and the controller and contractor worlds the same apply adds
	if created != wantQuoteRows {
		t.Fatalf("the second apply created %d rows, want exactly the quote world's %d (proposed %d)", created, wantQuoteRows, proposed)
	}
	after := fingerprint()
	for table, sum := range before {
		if after[table] != sum {
			t.Errorf("%s changed across the apply that adds the quotes, nothing else in the world may move", table)
		}
	}
	// The named exception, held to its bounds: every day-keyed Driver row now carries the demo org
	// (the stamp the first apply could not run), and the two fingerprints above prove nothing else
	// moved to get it: no other row of those tables, and no other column of these.
	var runsStamped, stopsStamped int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM delivery_runs WHERE id = ANY($1::uuid[]) AND org_id = (SELECT id FROM org WHERE slug = $2)`,
		driverDayIDs, demoOrgSlug).Scan(&runsStamped); err != nil {
		t.Fatalf("reading the stamped run: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM delivery_stops WHERE id = ANY($1::uuid[]) AND org_id = (SELECT id FROM org WHERE slug = $2)`,
		driverDayIDs, demoOrgSlug).Scan(&stopsStamped); err != nil {
		t.Fatalf("reading the stamped stops: %v", err)
	}
	if runsStamped != 1 || stopsStamped != len(driverDayIDs)-1 {
		t.Errorf("the org stamp left %d of 1 run and %d of %d stops stamped, want the day's whole Driver set",
			runsStamped, stopsStamped, len(driverDayIDs)-1)
	}
	if n := qwCount(t, ctx, pool, `SELECT count(*) FROM tasks WHERE id = ANY($1::uuid[]) AND seq IS NOT NULL
		AND org_id = (SELECT id FROM org WHERE slug = $2)`, taskIDs, demoOrgSlug); n != len(taskIDs) {
		t.Errorf("the task sync repair left %d of %d demo tasks on their feed, want all of them", n, len(taskIDs))
	}
	census := map[string]int{"quotes": 5, "quote_lines": 10, "quote_share_links": 3, "quote_acceptances": 2,
		"quote_share_link_events": 2, "pricing_audit_log": 4, "permission_approval_requests": 2 + 2}
	for table, add := range census {
		if n := qwCount(t, ctx, pool, `SELECT count(*) FROM `+table+qwWithoutContractor(table)); n != baseline[table]+add {
			t.Errorf("%s holds %d rows, want the baseline %d plus the world's %d", table, n, baseline[table], add)
		}
	}
	if n := qwCount(t, ctx, pool, `SELECT count(*) FROM quotes WHERE metadata->>'demo_seed' = $1 AND customer_id <> $2::uuid`, demoMarker, contractorCustomerID); n != 5 {
		t.Errorf("demo quotes found by the world's marker: %d, want 4", n)
	}
	if n := qwCount(t, ctx, pool, `SELECT count(*) FROM permission_approval_requests WHERE policy_type = 'MARGIN_BELOW_MINIMUM'`); n != 2 {
		t.Errorf("floor requests: %d, want 1", n)
	}

	// 4. idempotent: applied again, the same rows, nothing created, nothing touched.
	allTables := append(append(append([]string{}, qwOtherWorldTables...), qwQuoteTables...), "pricing_audit_log", "permission_approval_requests")
	snap := qwFingerprint(t, ctx, pool, allTables)
	if _, created := qwApply(t); created != 0 {
		t.Errorf("a third apply created %d rows, the seed must be idempotent", created)
	}
	for table, sum := range qwFingerprint(t, ctx, pool, allTables) {
		if snap[table] != sum {
			t.Errorf("%s changed across a repeat apply", table)
		}
	}

	// 5. the category floor the plan holds is the floor the database holds.
	for _, code := range []string{"PAVER", "BULK"} {
		var floor int
		if err := pool.QueryRow(ctx, `
			SELECT p.margin_floor_bps FROM pricing_category_policy p
			  JOIN product_categories c ON c.id = p.category_id WHERE c.code = $1`, code).Scan(&floor); err != nil || floor != demoFloorBps {
			t.Fatalf("category %s floor is %d (%v), the quote world is struck over %d", code, floor, err, demoFloorBps)
		}
	}

	// 6. the full served surface, then the HR login beside the two demo ones.
	var orgID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM org WHERE slug = $1`, demoOrgSlug).Scan(&orgID); err != nil {
		t.Fatalf("the demo org: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		WITH u AS (
		  INSERT INTO users (email, display_name, status, org_id, platform_user_id)
		  VALUES ('hr.quotes@hh-demo.invalid', 'HR (quote proof)', 'ACTIVE', $1, $2) RETURNING id)
		INSERT INTO user_branch_grants (user_id, branch_id, org_id) SELECT id, $3, $1 FROM u`,
		orgID, qwHRSubject, trentonBranchID); err != nil {
		t.Fatalf("plant the HR login: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO staff_role_grant (platform_user_id, role_id, org_id, branch_id) VALUES ($1, 'hr', $2, $3)`,
		qwHRSubject, orgID, trentonBranchID); err != nil {
		t.Fatalf("plant the HR grant: %v", err)
	}
	appwrite := newAppwriteFake(t)
	for _, sub := range []string{qwSalesUserID, qwManagerUserID, qwHRSubject} {
		appwrite.setTeam(sub, "org-"+demoOrgSlug)
	}
	keys := testkit.NewKeys(t)
	acceptKey := make([]byte, 32)
	if _, err := rand.Read(acceptKey); err != nil {
		t.Fatalf("generate acceptance key: %v", err)
	}
	env := &compEnv{
		keys: keys, minter: &testkit.Minter{Keys: keys, Clock: testkit.NewClock()},
		keySrv: testkit.NewKeyServer(t, keys), appwrite: appwrite, dsn: testDSN,
	}
	values := map[string]string{
		"CORE_TOKEN_ISSUER": testkit.KitIssuer, "CORE_STAFF_AUDIENCE": testkit.KitStaffAud,
		"CORE_TENANT_AUDIENCE": testkit.KitTenantAud, "CORE_JWKS_URL": env.keySrv.URL(),
		"CORE_DATABASE_URL": testDSN, "CORE_APPWRITE_ENDPOINT": appwrite.server.URL,
		"CORE_APPWRITE_PROJECT": "fx-appwrite-project", "CORE_APPWRITE_API_KEY": "fx-appwrite-key-placeholder",
		customerquote.EnvKeyID: "fx-accept-sign-key", customerquote.EnvKey: hex.EncodeToString(acceptKey),
	}
	consoleSessionValues(t, env.minter, values)
	env.getenv = func(k string) string { return values[k] }
	rec, _ := registeredSurface(t, env)
	sales := &tillClient{t: t, mux: rec.inner, token: env.minter.StaffToken(t, qwSalesUserID), who: "Sales"}
	manager := &tillClient{t: t, mux: rec.inner, token: env.minter.StaffToken(t, qwManagerUserID), who: "Sales manager"}
	hr := &tillClient{t: t, mux: rec.inner, token: env.minter.StaffToken(t, qwHRSubject), who: "HR"}

	quoteID := func(key string) string { return demoID("quote:" + key) }
	read := func(c *tillClient, key string) qwQuote {
		t.Helper()
		rsp := c.call("GET", "/api/v1/quotes/"+quoteID(key), nil)
		rsp.ok(t, http.StatusOK)
		var q qwQuote
		rsp.decode(t, &q)
		return q
	}

	// 7. counts, owners and states, read back through the app as the app shows them.
	type want struct {
		no, status, owner string
		lines             int
	}
	wants := map[string]want{
		"Q1": {"DEMO-PLM-Q1", "DRAFT", qwSalesUserID, 2},
		"Q2": {"DEMO-PLM-Q2", "SENT", qwManagerUserID, 2},
		"Q3": {"DEMO-PLM-Q3", "ACCEPTED", qwSalesUserID, 3},
		"Q4": {"DEMO-PLM-Q4", "ACCEPTED", qwSalesUserID, 2},
	}
	seeded := map[string]qwQuote{}
	for key, w := range wants {
		q := read(manager, key)
		seeded[key] = q
		if q.QuoteNo != w.no || q.Status != w.status || q.OwnerID != w.owner || len(q.Lines) != w.lines || q.BranchID != trentonBranchID {
			t.Errorf("%s reads %s %s owner %s branch %s with %d lines, want %+v at Trenton", key, q.QuoteNo, q.Status, q.OwnerID, q.BranchID, len(q.Lines), w)
		}
		if q.TotalCents != q.SubtotalCents+q.TaxCents || q.TaxCents != taxOn(q.SubtotalCents) {
			t.Errorf("%s: subtotal %d tax %d total %d are not the builder's flat HST arithmetic", key, q.SubtotalCents, q.TaxCents, q.TotalCents)
		}
	}
	if seeded["Q1"].OwnerID == seeded["Q2"].OwnerID {
		t.Error("the quotes are not owned by two different salespeople")
	}
	// The app lists the customers' quotes to the manager (Manage reaches every quote of its branch).
	listRsp := manager.call("GET", "/api/v1/quotes", nil)
	listRsp.ok(t, http.StatusOK)
	for _, key := range []string{"Q1", "Q2", "Q3", "Q4"} {
		if !strings.Contains(string(listRsp.body), wants[key].no) {
			t.Errorf("the quote list as the Sales manager lacks %s", wants[key].no)
		}
	}

	// 8. the line figures are the engine's: the rule unit price on every line equals what the engine's
	// own price picture answers for that customer, and the floor mark the app reads matches the story.
	custID := func(key string) string {
		var id string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM customers WHERE account_no = $1`, "DEMO-"+key).Scan(&id); err != nil {
			t.Fatalf("customer %s: %v", key, err)
		}
		return id
	}
	for key, cust := range map[string]string{"Q1": "C02", "Q2": "C01", "Q3": "C02", "Q4": "C10"} {
		items := []map[string]any{}
		for _, ln := range seeded[key].Lines {
			var qty float64
			var uom string
			if err := pool.QueryRow(ctx, `SELECT qty_entered::float8, uom_entered::text FROM quote_lines WHERE quote_id = $1 AND line_no = $2`,
				quoteID(key), ln.LineNo).Scan(&qty, &uom); err != nil {
				t.Fatal(err)
			}
			items = append(items, map[string]any{"product_id": ln.ProductID, "qty_entered": qty, "uom_entered": uom})
		}
		rsp := manager.call("POST", "/api/v1/pos/counter-prices", map[string]any{
			"branch_id": trentonBranchID, "customer_id": custID(cust), "items": items})
		rsp.ok(t, http.StatusOK)
		var prices counterPrices
		rsp.decode(t, &prices)
		engine := map[string]int64{}
		for _, p := range prices.Prices {
			engine[p.ProductID] = p.CustomerCents
		}
		for _, ln := range seeded[key].Lines {
			if engine[ln.ProductID] != ln.UnitPriceCents {
				t.Errorf("%s line %d: the quote holds a rule price of %d, the engine prices that customer and product at %d",
					key, ln.LineNo, ln.UnitPriceCents, engine[ln.ProductID])
			}
		}
	}
	// CP0-3 A: the Holland line's rule price sits below the floor and the app says so on the cost list.
	if l := seeded["Q2"].Lines[0]; l.RulePriceBelowFloor == nil || !*l.RulePriceBelowFloor {
		t.Errorf("Q2 line 1 (rule price under the floor) reads rule_price_below_floor %v", l.RulePriceBelowFloor)
	}
	if l := seeded["Q2"].Lines[1]; l.RulePriceBelowFloor != nil && *l.RulePriceBelowFloor {
		t.Error("Q2 line 2 (list price above the floor) is marked below the floor")
	}
	// The cleared line names its clearer.
	if l := seeded["Q3"].Lines[0]; l.FloorClearedBy == "" {
		t.Error("Q3 line 1 (cleared by the Sales manager) shows no clearer")
	}

	// The Cash On Demand account prices its pavers by its own rule (the seed's R06, ten percent off
	// list), not by the shared CONTRACTOR tier the account read before the rule existed (round 14's
	// finding (b): the tier's fifteen percent put the Holland Paver at 506, under the floor). The
	// engine's answer is R06's net at the resolver's own rounding, 536 (535.5 half up; the plan's
	// pricer rounds the discount instead and carries 535, the one cent on which the two roundings
	// part, and both sit above the floor), and that answer clears the floor over the seeded Trenton
	// cost.
	codRsp := manager.call("POST", "/api/v1/pos/counter-prices", map[string]any{
		"branch_id": trentonBranchID, "customer_id": custID("C10"),
		"items": []map[string]any{{"product_id": demoID("product:P01"), "qty_entered": 1.0, "uom_entered": "PC"}}})
	codRsp.ok(t, http.StatusOK)
	var codPrices counterPrices
	codRsp.decode(t, &codPrices)
	if len(codPrices.Prices) != 1 {
		t.Fatalf("the C10 x P01 price answer carries %d rows, want 1", len(codPrices.Prices))
	}
	if codPrices.Prices[0].CustomerCents != 536 {
		t.Errorf("the engine prices C10 x P01 at %d, want the account rule's 536 (the shared tier would answer 506)",
			codPrices.Prices[0].CustomerCents)
	}
	var codCost int64
	if err := pool.QueryRow(ctx,
		`SELECT avg_unit_cost_cents FROM product_branch_costs WHERE product_id = $1 AND branch_id = $2`,
		demoID("product:P01"), trentonBranchID).Scan(&codCost); err != nil {
		t.Fatalf("the seeded Trenton cost of P01: %v", err)
	}
	if margin := qMarginBps(codPrices.Prices[0].CustomerCents, codCost); margin < demoFloorBps {
		t.Errorf("C10 x P01 prices %d over a Trenton cost of %d, margin %d bps under the %d bps floor",
			codPrices.Prices[0].CustomerCents, codCost, margin, demoFloorBps)
	}

	// 9. the totals equal what the builder computes: the terms write, unchanged terms, restamps the
	// header from the lines and the flat rate, and the figures must not move.
	for _, key := range []string{"Q1", "Q2"} {
		rsp := manager.call("GET", "/api/v1/quotes/"+quoteID(key)+"/terms", nil)
		rsp.ok(t, http.StatusOK)
		var terms map[string]any
		rsp.decode(t, &terms)
		put := manager.call("PUT", "/api/v1/quotes/"+quoteID(key)+"/terms", terms)
		put.ok(t, http.StatusOK)
		now := read(manager, key)
		if now.SubtotalCents != seeded[key].SubtotalCents || now.TaxCents != seeded[key].TaxCents || now.TotalCents != seeded[key].TotalCents {
			t.Errorf("%s: the app's terms write recomputes %d/%d/%d where the seed holds %d/%d/%d", key,
				now.SubtotalCents, now.TaxCents, now.TotalCents,
				seeded[key].SubtotalCents, seeded[key].TaxCents, seeded[key].TotalCents)
		}
	}

	// 10. the share link opens the public view; both derived tokens are live, a wrong one is not.
	for _, key := range []string{"Q2", "Q3"} {
		rsp := qwPublic(rec.inner, "/api/public/v1/quotes/"+quoteToken(key))
		rsp.ok(t, http.StatusOK)
		var view struct {
			QuoteNo    string `json:"quote_no"`
			Status     string `json:"status"`
			Total      int64  `json:"total_cents"`
			Acceptable bool   `json:"acceptable"`
			Expired    bool   `json:"expired"`
		}
		rsp.decode(t, &view)
		if view.QuoteNo != wants[key].no || view.Status != wants[key].status || view.Total != seeded[key].TotalCents || view.Expired {
			t.Errorf("%s's public view reads %+v, want %s %s total %d live", key, view, wants[key].no, wants[key].status, seeded[key].TotalCents)
		}
		if key == "Q2" && !view.Acceptable {
			t.Error("the SENT quote's public view is not acceptable")
		}
	}
	if rsp := qwPublic(rec.inner, "/api/public/v1/quotes/"+quoteToken("Q2")+"x"); rsp.status != http.StatusNotFound {
		t.Errorf("a wrong token answered %d, want 404", rsp.status)
	}

	// 11. the floor request: listed to the branch Sales manager, never to HR; the requester sees its
	// own; four eyes hold (the manager's list names it, and it is filed by the Sales login).
	requestID := demoID("floorrequest:FR1")
	listed := func(c *tillClient) bool {
		t.Helper()
		rsp := c.call("GET", "/api/v1/staff/approvals?status=PENDING&limit=200", nil)
		if rsp.status == http.StatusForbidden {
			return false
		}
		rsp.ok(t, http.StatusOK)
		return strings.Contains(string(rsp.body), requestID)
	}
	if !listed(manager) {
		t.Error("the pending floor request is not listed to the branch's Sales manager")
	}
	if listed(hr) {
		t.Error("the pending floor request is listed to HR, which must not read margin")
	}
	if !listed(sales) {
		t.Error("the requester cannot see its own floor request")
	}
	var requesterRole string
	if err := pool.QueryRow(ctx, `
		SELECT sr.origin_code FROM permission_approval_requests r
		  JOIN users u ON u.id = r.requested_by
		  JOIN staff_role_grant g ON g.platform_user_id = u.platform_user_id AND g.deleted_at IS NULL
		  JOIN staff_role sr ON sr.id = g.role_id
		 WHERE r.id = $1`, requestID).Scan(&requesterRole); err != nil || requesterRole != "INSIDE_SALES" {
		t.Errorf("the request's requester reads as %q (%v), want INSIDE_SALES", requesterRole, err)
	}

	// 11b. every seeded pricing log row is the row the real write path produces: the same lines are
	// written through the real routes (as the roles the story names) on scratch quotes, and the real
	// rows' entity type, entity id, action, field and bypass reason are compared with the seeded rows.
	// The rule price row names the PRODUCT (no category rides the rule price answer), the discount
	// rows name the CATEGORY.
	type logShape struct{ EntityType, EntityID, Action, Field, Reason string }
	shapesOf := func(refQuote string) []logShape {
		t.Helper()
		rows, err := pool.Query(ctx, `
			SELECT entity_type, entity_id::text, action, field_name, COALESCE(bypass_reason, '')
			  FROM pricing_audit_log WHERE ref_entity_type = 'quote' AND ref_entity_id = $1::uuid
			 ORDER BY field_name, action`, refQuote)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []logShape
		for rows.Next() {
			var l logShape
			if err := rows.Scan(&l.EntityType, &l.EntityID, &l.Action, &l.Field, &l.Reason); err != nil {
				t.Fatal(err)
			}
			out = append(out, l)
		}
		return out
	}
	for _, c := range []struct {
		name, seededQuote, cust, prod, qty, uom string
		disc                                    int
		by                                      *tillClient
		seededLine                              string
	}{
		{"rule price under the floor (Q2 line 1)", "Q2", "C01", "P01", "240", "PC", 0, manager, "line_1"},
		{"discount above the floor (Q1 line 2)", "Q1", "C02", "P10", "30", "EA", 4, sales, "line_2"},
		{"discount under the floor cleared by the manager (Q3 line 1)", "Q3", "C02", "P02", "150", "PC", 12, manager, "line_1"},
	} {
		mk := c.by.call("POST", "/api/v1/quotes", map[string]any{"customer_id": custID(c.cust), "branch_id": trentonBranchID})
		mk.ok(t, http.StatusCreated)
		var made struct {
			ID string `json:"id"`
		}
		mk.decode(t, &made)
		add := c.by.call("POST", "/api/v1/quotes/"+made.ID+"/lines", map[string]any{
			"product_id": demoID("product:" + c.prod), "qty_entered": c.qty, "uom_entered": c.uom})
		add.ok(t, http.StatusOK)
		if c.disc > 0 {
			ed := c.by.call("PATCH", "/api/v1/quotes/"+made.ID+"/lines/1", map[string]any{"disc_pct": c.disc})
			ed.ok(t, http.StatusOK)
		}
		real := shapesOf(made.ID)
		var seededRows []logShape
		for _, l := range shapesOf(quoteID(c.seededQuote)) {
			if l.Field == c.seededLine {
				l.Field = "line_1"
				seededRows = append(seededRows, l)
			}
		}
		if len(real) != 1 || len(seededRows) != 1 {
			t.Errorf("%s: the real write left %d log rows (%+v) and the seed holds %d (%+v), want one each", c.name, len(real), real, len(seededRows), seededRows)
			continue
		}
		if real[0] != seededRows[0] {
			t.Errorf("%s: the seeded log row is %+v, the real write path writes %+v", c.name, seededRows[0], real[0])
		}
	}

	// 11c. the accepted quote's acceptance is anchored to its QUOTE_ACCEPTED audit row, as a real accept
	// anchors it: the same Q2 accepted through its seeded token writes a row of the same shape, and
	// the seeded Q3 row reads the same as that real one apart from the amount and the time.
	type auditShape struct {
		Action, EntityType, Changes string
		ActorNull, BranchNull       bool
	}
	auditOf := func(quote string) (auditShape, int64, int64) {
		t.Helper()
		var a auditShape
		var seq, anchor int64
		if err := pool.QueryRow(ctx, `
			SELECT al.action, al.entity_type, (al.changes - 'amount_cents')::text, al.actor_id IS NULL, al.branch_id IS NULL,
			       al.seq, COALESCE(qa.audit_seq, 0)
			  FROM quote_acceptances qa JOIN audit_log al ON al.seq = qa.audit_seq
			 WHERE qa.quote_id = $1::uuid`, quote).Scan(&a.Action, &a.EntityType, &a.Changes, &a.ActorNull, &a.BranchNull, &seq, &anchor); err != nil {
			t.Fatalf("the acceptance of %s is not anchored to an audit row: %v", quote, err)
		}
		return a, seq, anchor
	}
	acc := qwPublicPost(rec.inner, "/api/public/v1/quotes/"+quoteToken("Q2")+"/accept", `{"typed_name":"Customer representative (demo)","accepted":true,"quantities_confirmed":true}`)
	acc.ok(t, http.StatusOK)
	realAudit, realSeq, realAnchor := auditOf(quoteID("Q2"))
	seedAudit, seedSeq, seedAnchor := auditOf(quoteID("Q3"))
	if realSeq != realAnchor || seedSeq != seedAnchor || seedSeq == 0 {
		t.Errorf("anchors: real %d/%d seeded %d/%d", realSeq, realAnchor, seedSeq, seedAnchor)
	}
	if realAudit != seedAudit {
		t.Errorf("the seeded QUOTE_ACCEPTED row is %+v, the real accept writes %+v", seedAudit, realAudit)
	}
	var acceptedAt, updatedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT qa.accepted_at, q.updated_at FROM quote_acceptances qa JOIN quotes q ON q.id = qa.quote_id WHERE q.id = $1::uuid`,
		quoteID("Q3")).Scan(&acceptedAt, &updatedAt); err != nil || updatedAt.Before(acceptedAt) {
		t.Errorf("Q3 updated_at %v is before its accepted_at %v (%v)", updatedAt, acceptedAt, err)
	}

	// 12. the accepted quote converts through the real service, at its own price.
	if !seeded["Q3"].CanConvert {
		t.Error("the ACCEPTED quote reads can_convert false for the Sales manager")
	}
	conv := sales.call("POST", "/api/v1/quotes/"+quoteID("Q3")+"/convert", nil)
	conv.ok(t, http.StatusCreated)
	var converted struct {
		OrderID string `json:"order_id"`
		OrderNo string `json:"order_no"`
	}
	conv.decode(t, &converted)
	var orderSub, orderTax, orderTotal int64
	var orderStatus, quoteStatus string
	if err := pool.QueryRow(ctx, `
		SELECT o.status, o.subtotal_cents, o.tax_cents, o.total_cents, q.status
		  FROM orders o JOIN quotes q ON q.id = o.quote_id WHERE o.id = $1`, converted.OrderID).
		Scan(&orderStatus, &orderSub, &orderTax, &orderTotal, &quoteStatus); err != nil {
		t.Fatalf("the converted order: %v", err)
	}
	if quoteStatus != "CONVERTED" {
		t.Errorf("the quote is %s after conversion, want CONVERTED", quoteStatus)
	}
	if orderSub != seeded["Q3"].SubtotalCents || orderTax != seeded["Q3"].TaxCents || orderTotal != seeded["Q3"].TotalCents {
		t.Errorf("the order carries %d/%d/%d, the accepted quote agreed %d/%d/%d", orderSub, orderTax, orderTotal,
			seeded["Q3"].SubtotalCents, seeded["Q3"].TaxCents, seeded["Q3"].TotalCents)
	}
	if n := qwCount(t, ctx, pool, `SELECT count(*) FROM order_lines WHERE order_id = $1`, converted.OrderID); n != 3 {
		t.Errorf("the converted order has %d lines, want 3", n)
	}
	t.Logf("converted %s into order %s (%s), quote %s", wants["Q3"].no, converted.OrderNo, orderStatus, quoteStatus)
}
