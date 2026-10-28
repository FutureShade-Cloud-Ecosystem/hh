//go:build integration

// demoseed_integration_test.go, the two promises, proved against a real Postgres.
//
// The unit tests in plan_test.go prove the plan cannot EXPRESS a destructive statement. These prove
// the database agrees: seeded twice from zero the world does not grow, and a row somebody else put
// there, including one sitting on an id the seed wants, comes out the far side untouched.
//
// Each test builds its OWN database from zero and migrates it, rather than sharing one: the whole
// claim is about what happens on a second run, so a database another test already seeded would make
// the result meaningless.
//
//	Run: DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:5432/hardscapeos?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -count=1
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dashboard"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tasks"
)

// seedClock pins every integration run. A fixed instant means the demo world's dates, and so every
// count derived from them, are the same on the first run, the second run, and next year's run.
var seedClock = time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

// tablesTheSeedWrites is the census list. It is written out rather than discovered, because a test
// that asks the database which tables to count would happily agree that zero tables changed.
var tablesTheSeedWrites = []string{
	"credit_holds", "customers", "delivery_freight_raises", "delivery_runs", "delivery_stops",
	"dispatch_branch_depots",
	"dispatch_drivers", "dispatch_reschedules", "dispatch_trucks", "invoice_lines", "invoices",
	"load_scan_checkpoints", "order_lines", "orders", "payment_allocations", "payment_nsf", "payments",
	"permission_approval_requests", "pos_parked_tickets",
	"pos_sale", "pos_signature", "pricing_list_prices", "pricing_rules", "product_branch_costs",
	"product_images",
	"product_reorder_points", "product_uoms", "products", "purchasing_po_lines",
	"purchasing_pos", "purchasing_receipts", "quantity_break_headers",
	"quantity_break_items", "quote_lines", "stock_balances", "stock_moves", "task_events",
	"task_shortages",
	"tasks", "vendors", "will_call_pickups",
}

// dayKeyedTables is the subset of the census that §15 adds to on each NEW calendar day. Every other
// table in the list above is written once, ever, which is the claim
// TestIntegration_ASecondDayAddsOnlyTheDayKeyedRows exists to hold, and the reason this list is
// spelled out rather than derived: a table that quietly joins it should have to be typed here.
// delivery_runs joined with the Driver's dispatched day run: the run sheet asks for the dealer's day
// and must find a run out for delivery on it, which a stable-keyed fleet cannot promise past the
// first apply's date.
var dayKeyedTables = map[string]bool{
	"orders": true, "order_lines": true, "invoices": true, "invoice_lines": true,
	"payments": true, "payment_allocations": true, "pos_sale": true, "delivery_stops": true,
	"delivery_runs": true,
}

// -- Promise 1 · running it twice produces the same world ------------------------------------------

func TestIntegration_SeedingTwiceCreatesNothingTheSecondTime(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "idem")

	before := census(t, ctx, pool)
	_, summary := planFor(t, ctx, pool)

	first := seedOnce(t, ctx, pool)
	if first.Created+first.Skipped != first.Proposed {
		t.Fatalf("the first run lost rows: %d proposed, %d created, %d skipped",
			first.Proposed, first.Created, first.Skipped)
	}

	// A FRESH MIGRATED DATABASE IS NOT AN EMPTY ONE. Migration 0093 already plants a depot row for
	// each branch, and this command defers to every row it did not create, so the first run is
	// expected to skip exactly those and nothing else. The number is asserted rather than rounded
	// off, because slack here is where an accidental collision INSIDE the plan (two rows built from
	// the same key) would hide: it would look like one more row the database already had.
	if before["dispatch_branch_depots"] == 0 {
		t.Fatal("this database has no pre-existing depot rows, the assertion below is not testing what it says")
	}
	if want := summary.ByTable["dispatch_branch_depots"]; first.Skipped != want {
		t.Errorf("the first run skipped %d rows; only the %d depot rows the migrations already planted should have been skipped",
			first.Skipped, want)
	}
	afterFirst := census(t, ctx, pool)

	second := seedOnce(t, ctx, pool)
	if second.Created != 0 {
		t.Errorf("the second run created %d rows, the seed is not idempotent", second.Created)
	}
	if second.Skipped != second.Proposed {
		t.Errorf("the second run proposed %d rows and skipped only %d", second.Proposed, second.Skipped)
	}
	afterSecond := census(t, ctx, pool)

	// THE ASSERTION THE WHOLE COMMAND EXISTS TO PASS.
	for _, table := range tablesTheSeedWrites {
		if afterFirst[table] != afterSecond[table] {
			t.Errorf("%s: %d rows after one run, %d after two, the second run was not a no-op",
				table, afterFirst[table], afterSecond[table])
		}
	}

	// And the arithmetic is exactly additive: every table gained precisely what the run REPORTED
	// creating in it, no more (a stray write) and no less (a silent yield the report did not know
	// about), and nothing outside the plan's tables moved at all.
	for _, table := range tablesTheSeedWrites {
		gained := afterFirst[table] - before[table]
		if want := first.ByTable[table]; gained != want {
			t.Errorf("%s: the run reported creating %d rows, the table gained %d", table, want, gained)
		}
	}
	t.Logf("first run: %d proposed, %d created; second run: %d proposed, %d created, %d already present",
		first.Proposed, first.Created, second.Proposed, second.Created, second.Skipped)
}

// -- Promise 2 · a row somebody else made is never touched -----------------------------------------

func TestIntegration_AHandMadeRowSurvivesTheSeedUntouched(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "nondestr")

	branchID := oneBranch(t, ctx, pool)

	// (a) An ordinary hand-made account, of the kind a live tester creates while doing homework.
	// Nothing about it collides with the seed; it must simply still be there, unchanged.
	const handAccount = "HOMEWORK-0001"
	handID := "11111111-2222-4333-8444-555555555555"
	mustExec(t, ctx, pool, `
		INSERT INTO customers (id, account_no, name, customer_type, home_branch_id,
		                       payment_terms_days, credit_limit_cents, metadata)
		VALUES ($1, $2, 'Tester''s own account, DO NOT TOUCH', 'CONTRACTOR', $3, 17, 424242,
		        '{"put_here_by":"a human"}')`, handID, handAccount, branchID)

	// (b) The sharp case: a hand-made row sitting ON one of the seed's own deterministic ids. This is
	// the only way the seed could ever overwrite somebody, and ON CONFLICT DO NOTHING is the reason
	// it cannot. If this row comes back carrying the demo name, the whole staging promise is void.
	squatterID := demoID("customer:C01")
	mustExec(t, ctx, pool, `
		INSERT INTO customers (id, account_no, name, customer_type, home_branch_id,
		                       payment_terms_days, credit_limit_cents, metadata)
		VALUES ($1, 'HOMEWORK-0002', 'A human got to this id first', 'RETAIL', $2, 5, 99,
		        '{"put_here_by":"a human"}')`, squatterID, branchID)

	before := census(t, ctx, pool)

	res := seedOnce(t, ctx, pool)
	if res.Created == 0 {
		t.Fatal("the seed wrote nothing at all, this test would pass vacuously")
	}

	// (a) unchanged, in every column.
	var name, acct, typ, meta string
	var terms int
	var credit int64
	if err := pool.QueryRow(ctx, `
		SELECT name, account_no, customer_type, payment_terms_days, credit_limit_cents, metadata::text
		  FROM customers WHERE id = $1`, handID).
		Scan(&name, &acct, &typ, &terms, &credit, &meta); err != nil {
		t.Fatalf("the hand-made row is gone: %v", err)
	}
	if name != "Tester's own account, DO NOT TOUCH" || acct != handAccount || typ != "CONTRACTOR" ||
		terms != 17 || credit != 424242 || !strings.Contains(meta, "a human") {
		t.Errorf("the hand-made row was modified: name=%q acct=%q type=%q terms=%d credit=%d meta=%s",
			name, acct, typ, terms, credit, meta)
	}

	// (b) the squatter kept its identity: the seed yielded rather than overwrote.
	if err := pool.QueryRow(ctx,
		`SELECT name, account_no, metadata::text FROM customers WHERE id = $1`, squatterID).
		Scan(&name, &acct, &meta); err != nil {
		t.Fatalf("the row on the seed's own id is gone: %v", err)
	}
	if name != "A human got to this id first" {
		t.Errorf("the seed OVERWROTE a hand-made row on one of its own ids: name is now %q", name)
	}
	if !strings.Contains(meta, "a human") {
		t.Errorf("the seed overwrote the hand-made metadata: %s", meta)
	}
	if strings.Contains(meta, demoMarker) {
		t.Errorf("the seed stamped its marker onto a hand-made row: %s", meta)
	}

	// Nothing was deleted anywhere: every table the seed touches ends with at least what it started
	// with. A destructive seed that also inserted could still pass a total-count check; this cannot.
	after := census(t, ctx, pool)
	for _, table := range tablesTheSeedWrites {
		if after[table] < before[table] {
			t.Errorf("%s LOST rows: %d → %d", table, before[table], after[table])
		}
	}
	t.Logf("seeded %d rows around two hand-made rows; both survived unchanged", res.Created)
}

// TestIntegration_TheWorldIsFindableByItsOwnMarker. "Removed by its own marker" is only true if the
// marker actually reaches the rows. This walks the marker back out of the database.
func TestIntegration_TheWorldIsFindableByItsOwnMarker(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "marker")
	seedOnce(t, ctx, pool)

	for _, table := range []string{"customers", "products", "orders", "order_lines", "invoices", "payments", "stock_moves"} {
		var n int
		if err := pool.QueryRow(ctx,
			fmt.Sprintf(`SELECT count(*) FROM %s WHERE metadata->>'demo_seed' = $1`, table), demoMarker).
			Scan(&n); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		if n == 0 {
			t.Errorf("%s carries no row marked %q, the world is not findable there", table, demoMarker)
		}
	}
	// And the DEMO- prefix reaches the human-readable keys, which is how somebody finds the world on
	// a screen rather than in SQL.
	for _, q := range []string{
		`SELECT count(*) FROM orders WHERE order_no LIKE 'DEMO-%'`,
		`SELECT count(*) FROM invoices WHERE invoice_no LIKE 'DEMO-%'`,
		`SELECT count(*) FROM delivery_runs WHERE run_no LIKE '#DEMO-%'`,
		`SELECT count(*) FROM dispatch_trucks WHERE code LIKE 'DEMO-%'`,
		`SELECT count(*) FROM products WHERE sku LIKE 'DEMO-%'`,
	} {
		var n int
		if err := pool.QueryRow(ctx, q).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if n == 0 {
			t.Errorf("no rows matched: %s", q)
		}
	}
}

// -- Promise 3 · a demo row names a demo person, and the database agrees -------------------------

// TestIntegration_TheDemoWorldNamesTheDemoPeople proves the edge END TO END, which the plan tests
// cannot: user_id and claimed_by are UUID columns with a live foreign key behind the first
// (dispatch_drivers.user_id → users.id, 0090), so a plan that resolved the WRONG id, 0200's
// literal rather than the row's own, would build cleanly, pass every unit test, and be refused by
// Postgres here. It also walks the two reads the surfaces actually make, in their own shape:
// DriverForUser's `WHERE user_id = $1 AND is_active`, and "a claim stamp with a name on it".
func TestIntegration_TheDemoWorldNamesTheDemoPeople(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "edge")

	// The gap this exists to close, stated as the database sees it BEFORE the seed: 0200's three
	// personas are here (they arrive by migration, which is the whole reason 0200 is one) and there
	// is no work for any of them to own.
	var personas int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE email LIKE '%.demo@dibbits.example'`).Scan(&personas); err != nil {
		t.Fatal(err)
	}
	if personas != 3 {
		t.Fatalf("a migrations-only database carries %d demo personas, want the 3 that 0200 plants, "+
			"without them this test is asserting nothing", personas)
	}

	seedOnce(t, ctx, pool)

	// 1 · the driver persona reaches a driver record, by the read the run sheet makes.
	var name, branch string
	if err := pool.QueryRow(ctx, `
		SELECT d.name, l.code FROM dispatch_drivers d
		  JOIN users u ON u.id = d.user_id
		  JOIN locations l ON l.id = d.branch_id
		 WHERE u.email = 'driver.demo@dibbits.example' AND d.is_active`).Scan(&name, &branch); err != nil {
		t.Fatalf("the driver persona reaches no driver record, this is the exact state that renders "+
			"\"You are not set up as a driver at this branch\": %v", err)
	}

	// 2 · that record has a day to walk: something behind it, something today, something ahead.
	var past, today, ahead int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE r.scheduled_date < $1::date),
		       count(*) FILTER (WHERE r.scheduled_date = $1::date),
		       count(*) FILTER (WHERE r.scheduled_date > $1::date)
		  FROM delivery_runs r
		  JOIN dispatch_drivers d ON d.id = r.driver_id
		  JOIN users u ON u.id = d.user_id
		 WHERE u.email = 'driver.demo@dibbits.example'`,
		seedClock.Format("2006-01-02")).Scan(&past, &today, &ahead); err != nil {
		t.Fatal(err)
	}
	if past == 0 || today == 0 || ahead == 0 {
		t.Errorf("%s drives %d runs behind, %d today and %d ahead, the run sheet needs one of each "+
			"to be worth opening", name, past, today, ahead)
	}

	// 3 · no demo task carries a claim stamp with nobody on it, and the somebody is the yard persona.
	var orphaned, claimed int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE t.claimed_by IS NULL),
		       count(*) FILTER (WHERE u.email = 'yard.demo@dibbits.example')
		  FROM tasks t
		  LEFT JOIN users u ON u.id = t.claimed_by
		  JOIN orders o ON o.id = t.order_id
		 WHERE o.metadata->>'demo_seed' = $1 AND t.claimed_at IS NOT NULL`, demoMarker).Scan(&orphaned, &claimed); err != nil {
		t.Fatal(err)
	}
	if orphaned != 0 {
		t.Errorf("%d demo tasks carry a claimed_at with no claimed_by, a claim with nobody on it", orphaned)
	}
	if claimed == 0 {
		t.Error("no demo task is claimed by the yard persona, the Needs-you queue has nothing to point at")
	}

	// 4 · and the second run still changes nothing. The edge is written by an INSERT like everything
	// else here, so it has to obey the same promise the rest of the world does.
	if second := seedOnce(t, ctx, pool); second.Created != 0 {
		t.Errorf("the second run created %d rows once the edge was added, it is no longer idempotent",
			second.Created)
	}
}

// -- helpers ---------------------------------------------------------------------------------------

func planFor(t *testing.T, ctx context.Context, pool *pgxpool.Pool) ([]row, planSummary) {
	t.Helper()
	return planAt(t, ctx, pool, seedClock)
}

// planAt is planFor with the clock as an argument. §15 keys a handful of rows on the plan's calendar
// day, so proving that a LATER deploy adds that day's rows and leaves the first day's alone needs two
// plans on two days, which is exactly what a staging box does across a week of deploys, and exactly
// what a fixed seedClock cannot express.
func planAt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, at time.Time) ([]row, planSummary) {
	t.Helper()
	branches, err := resolveBranches(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	cats, err := resolveCategories(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	// The demo LOGINS, resolved the way main.go resolves them. Threading them here rather than
	// leaving the map nil is what makes these tests exercise the REAL plan: user_id and claimed_by
	// are two columns with foreign keys behind them, and a plan that never wrote them would prove
	// idempotency over a statement the command does not actually emit.
	personas, err := resolvePersonas(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	seal, _, err := newSealer("")
	if err != nil {
		t.Fatal(err)
	}
	rows, sum, err := buildPlan(planInput{
		Now: at, Branches: branches, Categories: cats, Personas: personas, Seal: seal})
	if err != nil {
		t.Fatal(err)
	}
	return rows, sum
}

func seedOnce(t *testing.T, ctx context.Context, pool *pgxpool.Pool) applyResult {
	t.Helper()
	return seedAt(t, ctx, pool, seedClock)
}

func seedAt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, at time.Time) applyResult {
	t.Helper()
	rows, _ := planAt(t, ctx, pool, at)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	res, err := apply(ctx, tx, rows)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("apply: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return res
}

func census(t *testing.T, ctx context.Context, pool *pgxpool.Pool) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, table := range tablesTheSeedWrites {
		var n int
		if err := pool.QueryRow(ctx, fmt.Sprintf("SELECT count(*) FROM %s", table)).Scan(&n); err != nil {
			t.Fatalf("counting %s: %v", table, err)
		}
		out[table] = n
	}
	return out
}

func oneBranch(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	bs, err := resolveBranches(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	return bs[0].ID
}

func mustExec(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("%s: %v", strings.TrimSpace(strings.SplitN(sql, "\n", 2)[0]), err)
	}
}

// freshMigratedDB builds a database FROM ZERO for this test and migrates it with the same files
// cmd/migrate applies, in the same order. Nothing is shared with another test or another run: the
// claim under test is about the SECOND seeding of a database, so inheriting one that had already
// been seeded would prove nothing at all.
func freshMigratedDB(t *testing.T, suffix string) *pgxpool.Pool {
	t.Helper()
	return freshMigratedDBWithout(t, suffix, "")
}

// freshMigratedDBWithout is freshMigratedDB with migration files left unapplied (by file name, comma
// separated; "" skips none), for a test that needs the database as it stood just before them.
func freshMigratedDBWithout(t *testing.T, suffix, skip string) *pgxpool.Pool {
	t.Helper()
	base, ok := os.LookupEnv("DATABASE_URL")
	if !ok {
		t.Skip("DATABASE_URL is unset, demoseed's integration tests need a Postgres to build a database on")
	}
	name := "demoseed_it_" + suffix
	adminDSN, testDSN := swapDatabase(base, "postgres"), swapDatabase(base, name)

	ctx := context.Background()
	admin, err := pgxpool.New(ctx, adminDSN)
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

	pool, err := pgxpool.New(ctx, testDSN)
	if err != nil {
		t.Fatalf("connecting to %s: %v", name, err)
	}
	t.Cleanup(pool.Close)

	files, err := filepath.Glob(filepath.Join("..", "..", "db", "migrations", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations found (err=%v)", err)
	}
	skips := map[string]bool{}
	for _, one := range strings.Split(skip, ",") {
		if one = strings.TrimSpace(one); one != "" {
			skips[one] = true
		}
	}
	sort.Strings(files)
	for _, f := range files {
		if skips[filepath.Base(f)] {
			continue
		}
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("applying %s: %v", filepath.Base(f), err)
		}
	}
	return pool
}

// swapDatabase rewrites the database name in a postgres DSN, leaving credentials and options alone.
func swapDatabase(dsn, name string) string {
	slash := strings.LastIndex(dsn, "/")
	if slash < 0 {
		return dsn
	}
	tail := ""
	if q := strings.Index(dsn[slash:], "?"); q >= 0 {
		tail = dsn[slash+q:]
	}
	return dsn[:slash+1] + name + tail
}

// -- Promise 4 · the "today" widgets survive the day after the first apply ------------------------

// TestIntegration_ASecondDayAddsOnlyTheDayKeyedRows is the whole of §15, proved against Postgres
// rather than argued.
//
// WHAT WENT WRONG WITHOUT IT. Every id in this plan was derived from a key with no date in it, and
// every date was computed relative to the clock, so the FIRST apply decided what "today" meant and
// every later apply recognised those rows and wrote nothing. The commander auto-applies on each
// deploy. On the day after the first apply, sales.today was $0, deliveries.today was 0, and both
// dispatch queue rows were empty, on a box people look at weekly.
//
// The test runs the command the way a deploy does: apply, let a day pass, apply again. Three things
// have to hold at once, and the middle one is the one a naive fix would break, a seed that simply
// re-keyed everything by day would pass the first and third assertions while building a second demo
// world beside the first every single deploy.
func TestIntegration_ASecondDayAddsOnlyTheDayKeyedRows(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "daykey")

	tomorrow := seedClock.AddDate(0, 0, 1)
	_, todayPlan := planAt(t, ctx, pool, seedClock)
	if len(todayPlan.DayKeys) == 0 {
		t.Fatal("this plan day-keys nothing, the whole test is vacuous")
	}

	first := seedAt(t, ctx, pool, seedClock)
	if first.Created == 0 {
		t.Fatal("the first run wrote nothing")
	}
	afterDay1 := census(t, ctx, pool)

	// 1 · SAME DAY, AGAIN. A second deploy on the same calendar day still creates nothing: day-keying
	// changes which day a key names, not whether the key is stable within it.
	if again := seedAt(t, ctx, pool, seedClock.Add(6*time.Hour)); again.Created != 0 {
		t.Errorf("a second deploy on the same day created %d rows, day-keying broke idempotency "+
			"within a day, which is the property that lets this command run on every deploy", again.Created)
	}

	// 2 · A DAY LATER. Some rows are created, and ONLY in the tables §15 writes to.
	second := seedAt(t, ctx, pool, tomorrow)
	if second.Created == 0 {
		t.Fatal("a deploy the next day created nothing, the 'today' widgets are still frozen on the " +
			"first apply's date, which is the defect §15 exists to close")
	}
	for table, n := range second.ByTable {
		if n > 0 && !dayKeyedTables[table] {
			t.Errorf("the next day's deploy created %d rows in %s, which is not a day-keyed table, "+
				"the demo world is being rebuilt beside itself rather than refreshed", n, table)
		}
	}
	afterDay2 := census(t, ctx, pool)

	// 3 · THE STABLE WORLD DID NOT MOVE. The fleet, the catalogue, the order book's tasks and stops,
	// the parked tickets and the approvals are all exactly where they were.
	for _, table := range tablesTheSeedWrites {
		if dayKeyedTables[table] {
			continue
		}
		if afterDay1[table] != afterDay2[table] {
			t.Errorf("%s: %d rows after day one, %d after day two, a stable-keyed table grew",
				table, afterDay1[table], afterDay2[table])
		}
	}

	// 4 · AND YESTERDAY IS STILL THERE. The point of day-keying rather than refreshing is that the
	// previous day's rows remain as history, which is what an INSERT-only applier can offer and an
	// UPDATE could not.
	var days int
	if err := pool.QueryRow(ctx, `
		SELECT count(DISTINCT s.created_at::date) FROM pos_sale s
		  JOIN orders o ON o.id = s.order_id
		 WHERE o.order_no LIKE 'DEMO-%T1-%'`).Scan(&days); err != nil {
		t.Fatal(err)
	}
	if days != 2 {
		t.Errorf("the day-keyed counter sale exists on %d distinct days after two deploys, want 2, "+
			"yesterday's history was not kept", days)
	}
	t.Logf("day one created %d rows; the same day again created 0; the next day created %d, all in day-keyed tables",
		first.Created, second.Created)
}

// -- Promise 5 · the two Needs-you sources that had nothing to count, as the readers see them -----

// TestIntegration_TheNeedsQueueHasSomethingToCount walks the two gaps in their OWN shape rather than
// counting rows: pos.parked is `status = 'PARKED'` per branch, and approvals.waiting is PENDING rows
// narrowed to what a caller may decide, four-eyes excluded. A count of rows in the table would pass
// while the widget stayed at zero, which is exactly how these two came to be empty in the first place.
func TestIntegration_TheNeedsQueueHasSomethingToCount(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "needs")
	seedOnce(t, ctx, pool)

	// 1 · pos.parked, ListParked's own predicate, per branch.
	branches, err := resolveBranches(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, br := range branches {
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM pos_parked_tickets WHERE branch_id::text = $1 AND status = 'PARKED'`,
			br.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			t.Errorf("%s has no parked ticket, the counter's Needs-you row is empty at that branch", br.Code)
		}
		seen += n
	}
	if seen == 0 {
		t.Fatal("no parked tickets at all")
	}

	// 2 · approvals.waiting, PENDING, filed by a demo persona, on a policy a rail approver decides.
	// The join to users is the assertion that matters: requested_by is a live foreign key, so a plan
	// that resolved the wrong id would build cleanly, pass every unit test, and be refused here.
	var pending int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM permission_approval_requests r
		  JOIN users u ON u.id = r.requested_by
		 WHERE r.status = 'PENDING'
		   AND u.email = 'dispatch.demo@dibbits.example'
		   AND r.policy_type = 'FREIGHT_ABOVE_QUOTE'`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending < 2 {
		t.Errorf("%d pending approvals are filed by the demo dispatcher, want one per branch", pending)
	}

	// 3 · and each one is bound to a raise that exists, in the state the gate leaves it. An approval
	// whose subject_ref names nothing is a card asking somebody to agree to a figure stored nowhere.
	var bound int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM permission_approval_requests r
		  JOIN delivery_freight_raises f ON f.id::text = r.subject_ref
		 WHERE r.status = 'PENDING' AND f.state = 'PARKED' AND f.approval_id = r.id
		   AND f.proposed_freight_cents > f.quoted_freight_cents`).Scan(&bound); err != nil {
		t.Fatal(err)
	}
	if bound != pending {
		t.Errorf("%d of %d pending approvals reach a parked raise that points back at them", bound, pending)
	}

	// 4 · the cost rows behind the two cost-class measures. inventory.value INNER-joins
	// product_branch_costs, so a demo product with no cost row is invisible to it; margin.gross_bps
	// LEFT-joins it under COALESCE(…, 0), so the same missing row reads as a cost of zero and the
	// branch bar reports a margin near 100%. This is the read margin.gross_bps actually makes.
	var lines, priced int
	if err := pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE c.product_id IS NOT NULL)
		  FROM order_lines ol
		  JOIN orders o ON o.id = ol.order_id
		  LEFT JOIN product_branch_costs c
		    ON c.product_id = ol.product_id AND c.branch_id = o.branch_id
		 WHERE o.status NOT IN ('DRAFT','CANCELLED')
		   AND o.metadata->>'demo_seed' = $1`, demoMarker).Scan(&lines, &priced); err != nil {
		t.Fatal(err)
	}
	if lines == 0 || priced != lines {
		t.Errorf("%d of %d demo order lines resolve a branch cost, the rest are read as free and put "+
			"the gross-margin bars near 100%%", priced, lines)
	}
}

// -- Promise 6 · the yard manager's Needs-you counts match the seed -------------------------------

// TestIntegration_TheYardManagersNeedsCountsMatchTheSeed reads the three seed-counted sources of
// the yard manager's Needs-you (0381) through the adapters the composition root registers, over the
// seeded demo world, and compares each to the plan: the open shortages (SH1 and SH2 open, SH3
// resolved), the stale picks (the PUSHED picks past their accept deadline), and the receiving
// differences (the PARTIAL purchase orders with a short line), counted from demoShortages,
// demoTasks and demoPurchaseOrders rather than typed.
func TestIntegration_TheYardManagersNeedsCountsMatchTheSeed(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "yardneeds")
	seedOnce(t, ctx, pool)

	wantShortages := 0
	for _, s := range demoShortages {
		if s.Resolution == "" {
			wantShortages++
		}
	}
	wantDifferences := 0
	for _, po := range demoPurchaseOrders {
		if po.Status == "PARTIAL" && len(po.Lines) > 0 {
			wantDifferences++
		}
	}
	wantStale := 0
	for _, k := range demoTasks {
		if k.Type == "PICK" && (k.Status == "QUEUED" || k.Status == "PUSHED") && k.Deadline > 0 {
			wantStale++
		}
	}
	if wantShortages != 2 || wantStale != 2 {
		t.Fatalf("the plan changed under this test: %d open shortages, %d stale picks (want 2 and 2)",
			wantShortages, wantStale)
	}

	scope := dashboard.Scope{AllBranches: true, Now: seedClock}
	tasksSvc := tasks.NewService(tasks.NewRepository(pool))
	purchasingSvc := purchasing.NewService(purchasing.NewRepository(pool))
	for _, tc := range []struct {
		name string
		src  dashboard.NeedsFunc
		want int
	}{
		{"open shortages", w6bOpenShortages(tasksSvc), wantShortages},
		{"stale picks", w6bStalePicks(pool), wantStale},
		{"receiving differences", w6bReceivingDifferences(purchasingSvc), wantDifferences},
	} {
		m, err := tc.src(ctx, scope)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if m.Count != tc.want {
			t.Errorf("%s counts %d, the seed plants %d", tc.name, m.Count, tc.want)
		}
	}
	// A caller granted no branch sees none of it: the fence fails closed through the adapter.
	none := dashboard.Scope{Now: seedClock}
	if m, err := w6bOpenShortages(tasksSvc)(ctx, none); err != nil || m.Count != 0 {
		t.Errorf("a caller with no grants counted %d open shortages (%v)", m.Count, err)
	}
}
