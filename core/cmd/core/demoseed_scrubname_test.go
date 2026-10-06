//go:build demo

package main

// demoseed_scrubname_test.go, the demo world's name probe: after the demoseed runs, no
// customer a tester can meet is named like a scrub placeholder.
//
// WHY A PROBE AND NOT A LOOKUP OF ONE ID. The scrub pass (core/db/scrub) writes customer
// names as "Scrubbed Party NNNN", and the migrations plant more than one demo island that
// carries such a name into every migrated database: 0055's portal demo island (the record the
// principal's look traced onto the Quote Desk's list, decision log row 925), 0057's three
// warranty-demo accounts, and 0029's house WALK-IN. A probe that read one id would go green
// the moment the traced row was fixed and stay silent about the next scrub-named customer a
// tester meets on the counter's search, a receipt, or a warranty claim. So the probe reads
// EVERY customer's name in the demo world and holds the whole family to the same rule.
//
// THE ORDER IS THE BOX'S ORDER. The database is built from the frozen migration set (so the
// islands are in the world, placeholder names and all), then `demoseed -apply` runs exactly as
// the demo image's entrypoint runs it, and the probe reads the world that leaves behind.
//
// The database gate is the demo world proof's own variable (the till's and the quotes' world
// tests read the same one), and the harness refuses an address that is not local.

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// scrubNameRe is the placeholder family: the brief's alternation, which covers the pattern the
// scrub pass writes into customer names ("Scrubbed Party %04d" in core/db/scrub/build_map.py)
// and the shapes a future pass could plausibly write, without matching any real name.
var scrubNameRe = regexp.MustCompile(`^Scrubbed (Party|Customer|Record|Acct) [0-9]{4}$`)

// The portal demo island's customer (0055_portal_demo_fixtures.sql), the traced record. The id
// and the placeholder are repeated here on purpose: if the migration ever moves either, this
// test goes red rather than quietly probing a row that no longer exists.
const (
	snIslandCustomerID   = "df000000-0000-4000-9000-000000000001"
	snIslandScrubbedName = "Scrubbed Party 0004"
)

// snFreshDB creates this test's own database from zero and applies the migration set, the same
// files and order the migrate role applies (mirroring the quotes world's helper beside it, on
// its own database name so the two proofs never share one).
func snFreshDB(t *testing.T, ctx context.Context, dsn string) (*pgxpool.Pool, string) {
	t.Helper()
	name := "demoworld_scrubname"
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
	files, err := filepath.Glob(filepath.Join("..", "..", "db", "migrations", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations found (err=%v)", err)
	}
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", filepath.Base(f), err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("applying %s: %v", filepath.Base(f), err)
		}
	}
	return pool, testDSN
}

// snCustomerNames reads every customer's name in the world, the column the Quote Desk's list,
// the counter's customer search and the printed receipt all render.
func snCustomerNames(t *testing.T, ctx context.Context, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT name FROM customers ORDER BY name`)
	if err != nil {
		t.Fatalf("reading customer names: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scanning a customer name: %v", err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading customer names: %v", err)
	}
	return out
}

// snOffenders returns the names in the world that read like a scrub placeholder.
func snOffenders(names []string) []string {
	var bad []string
	for _, name := range names {
		if scrubNameRe.MatchString(name) {
			bad = append(bad, name)
		}
	}
	sort.Strings(bad)
	return bad
}

// snTokenRe is the other placeholder family: the word-level token the scrub pass left where it
// swapped a name or a common word ("Scrubname0014 Scrubname0027" for a role title, "Hey
// Scrubname0008" in a seeded message). It is searched for anywhere in a value, not anchored.
var snTokenRe = regexp.MustCompile(`Scrubname[0-9]+`)

// snTokenAllowed is every text column the migrations leave a Scrubname token in that no persona,
// customer, contact or staff record reaches: the scrub swapped common words inside report,
// ledger, dashboard-gallery and tour copy ("Scrubname0027 by Branch", "Scrubname0027 Revenue"),
// which are not names of anybody and are shared with every build, so they are out of this
// pass's reach. users.display_name is listed because the demo populate (a separate step, run
// after this seed, internal/demo/populate.go) renames those rows; this probe runs the seed only.
// Anything NOT listed here and carrying a token fails the probe, so a new migration that plants
// a scrub-named persona, channel or message cannot slip through.
var snTokenAllowed = map[string]bool{
	"gl_accounts.name": true, "v_gl_detail.account_name": true, "v_gl_account_balances.account_name": true,
	"v_trial_balance.account_name": true, "v_income_statement.account_name": true,
	"report_templates.name": true, "report_templates.legacy_name": true,
	"platform_tour_steps.line":        true,
	"dashboard_gallery_entries.title": true,
	"dashboard_gallery_entries.blurb": true,
	"dashboard_profile_widgets.title": true,
	"users.display_name":              true,
}

// snTokenOffenders scans every text column of every base table and returns "table.column: value"
// for each value carrying a Scrubname token in a column outside the allow-list.
func snTokenOffenders(t *testing.T, ctx context.Context, pool *pgxpool.Pool) []string {
	t.Helper()
	cols, err := pool.Query(ctx, `
		SELECT c.table_name, c.column_name
		  FROM information_schema.columns c
		  JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		 WHERE c.table_schema = 'public' AND t.table_type = 'BASE TABLE'
		   AND c.data_type IN ('text', 'character varying')
		 ORDER BY 1, 2`)
	if err != nil {
		t.Fatalf("listing text columns: %v", err)
	}
	type tc struct{ table, col string }
	var all []tc
	for cols.Next() {
		var x tc
		if err := cols.Scan(&x.table, &x.col); err != nil {
			t.Fatalf("scanning a column: %v", err)
		}
		all = append(all, x)
	}
	cols.Close()
	var bad []string
	for _, x := range all {
		if snTokenAllowed[x.table+"."+x.col] {
			continue
		}
		rows, err := pool.Query(ctx, `SELECT `+pgx.Identifier{x.col}.Sanitize()+` FROM `+pgx.Identifier{x.table}.Sanitize()+
			` WHERE `+pgx.Identifier{x.col}.Sanitize()+` ~ 'Scrubname[0-9]+'`)
		if err != nil {
			t.Fatalf("scanning %s.%s: %v", x.table, x.col, err)
		}
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				t.Fatalf("scanning a value of %s.%s: %v", x.table, x.col, err)
			}
			if snTokenRe.MatchString(v) {
				bad = append(bad, x.table+"."+x.col+": "+v)
			}
		}
		rows.Close()
	}
	return bad
}

func TestDemoWorld_NoCustomerNamedLikeAScrubPlaceholder(t *testing.T) {
	dsn := os.Getenv(tillDemoDBEnvVar)
	if dsn == "" {
		t.Skip(tillDemoDBEnvVar + " is not set: the demo world proof needs its own throwaway local Postgres")
	}
	requireLocalDSN(t, dsn)
	ctx := context.Background()

	// 1. the world the way a demo box builds it: every migration first, so the islands the
	// migrations plant are in it with their placeholder names, then the demoseed over them.
	pool, testDSN := snFreshDB(t, ctx, dsn)
	t.Setenv("DATABASE_URL", testDSN)
	t.Setenv("CORE_DATABASE_URL", testDSN)
	t.Setenv("APP_ENV", "dev")

	// 2. the probe bites: a freshly migrated database carries scrub-named customers (the
	// island's among them, by its own id), or everything below would pass vacuously.
	migrated := snOffenders(snCustomerNames(t, ctx, pool))
	if len(migrated) == 0 {
		t.Fatal("a freshly migrated database carries no scrub-named customer, this test is not probing anything")
	}
	var islandBefore string
	if err := pool.QueryRow(ctx, `SELECT name FROM customers WHERE id = $1::uuid`, snIslandCustomerID).
		Scan(&islandBefore); err != nil {
		t.Fatalf("the portal demo island's customer is not on a migrated database: %v", err)
	}
	if islandBefore != snIslandScrubbedName {
		t.Fatalf("the island customer reads %q before the seed, want the placeholder %q, the migration moved", islandBefore, snIslandScrubbedName)
	}

	// 3. the demoseed runs, the way the demo image's entrypoint runs it.
	if _, created := qwApply(t); created == 0 {
		t.Fatal("the demoseed created nothing on a fresh database")
	}

	// 4. THE PROBE. No customer in the demo world is named like a scrub placeholder.
	if bad := snOffenders(snCustomerNames(t, ctx, pool)); len(bad) != 0 {
		t.Errorf("after the demoseed ran, %d customer(s) still carry scrub placeholder names: %s",
			len(bad), strings.Join(bad, ", "))
	}

	// 4b. no persona-facing text (a role title, a conversation's title or a seeded message) still
	// reads a Scrubname token.
	if bad := snTokenOffenders(t, ctx, pool); len(bad) != 0 {
		t.Errorf("after the demoseed ran, %d value(s) still carry a Scrubname token:\n  %s", len(bad), strings.Join(bad, "\n  "))
	}

	// 5. the traced record itself: the island's customer reads a demo name, not the placeholder.
	var islandAfter string
	if err := pool.QueryRow(ctx, `SELECT name FROM customers WHERE id = $1::uuid`, snIslandCustomerID).
		Scan(&islandAfter); err != nil {
		t.Fatalf("the island customer vanished from the world: %v", err)
	}
	if islandAfter == snIslandScrubbedName || !strings.Contains(islandAfter, "(demo)") {
		t.Errorf("the island customer reads %q after the seed, want a believable (demo) name in the style of the seeded customers", islandAfter)
	}

	// 6. idempotent: a second apply creates nothing and moves no name.
	if _, created := qwApply(t); created != 0 {
		t.Errorf("a second apply created %d rows, the seed must be idempotent", created)
	}
	if bad := snOffenders(snCustomerNames(t, ctx, pool)); len(bad) != 0 {
		t.Errorf("after a second apply, %d customer(s) carry scrub placeholder names: %s", len(bad), strings.Join(bad, ", "))
	}
	t.Logf("migrated world carried %d scrub-named customers (%s); after the demoseed the probe reads none", len(migrated), strings.Join(migrated, ", "))
}
