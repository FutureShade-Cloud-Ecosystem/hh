//go:build integration

// scrub_placeholder_words_integration_test.go: the scrub pass swapped ordinary words for
// "ScrubnameNNNN" tokens ("Scrubname0027 Revenue" for "Sales Revenue", "Scrubname0014 Scrubname0027"
// for "Inside Sales"), and the carried migrations are frozen, so every build shows them. Migration
// 0363 rewrites the values a person reads; this file holds it to that.
//
// Two rules. First, after `migrate`, no text a person can read carries a token that stands for an
// ordinary word. A token that stands for a PERSONAL name (the scrub took those out on purpose, D32)
// is allowed to remain, and the set of those tokens is named below so it can only be changed here, in
// the open. Second, the migration is guarded: it matches exact current values, so a role a dealer
// renamed or a widget a user retitled is never overwritten, and a second run changes nothing.
//
// The word map, with its evidence, is the lane's SCRUB-PLACEHOLDER-MAP.md.
//
//	Run: DATABASE_URL='postgres://.../postgres?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run 'ScrubPlaceholder' -count=1
package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const migration0363File = "0363_scrub_placeholders_become_their_words.sql"

var scrubTokenRe = regexp.MustCompile(`Scrubname[0-9]+`)

// personalNameTokens are the tokens that stand for a person's first name or surname. They are not
// words to restore and stay where they are until the principal and Colton decide what a person reads
// in their place. Every other token stands for an ordinary word and must not survive in readable text.
var personalNameTokens = map[string]bool{
	"Scrubname0005": true, "Scrubname0008": true, "Scrubname0016": true,
	"Scrubname0024": true, "Scrubname0025": true, "Scrubname0030": true,
}

// readableTokenOffenders scans every text-like column of every base table and returns
// "table.column: value" for each value carrying a token that stands for an ordinary word.
func readableTokenOffenders(t *testing.T, ctx context.Context, pool *pgxpool.Pool) []string {
	t.Helper()
	cols, err := pool.Query(ctx, `
		SELECT c.table_name, c.column_name
		  FROM information_schema.columns c
		  JOIN information_schema.tables x ON x.table_schema = c.table_schema AND x.table_name = c.table_name
		 WHERE c.table_schema = 'public' AND x.table_type = 'BASE TABLE'
		   AND c.data_type IN ('text', 'character varying', 'json', 'jsonb', 'ARRAY', 'USER-DEFINED')
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
	if len(all) < 100 {
		t.Fatalf("only %d text-like columns found, the scan is not looking at the schema", len(all))
	}
	var bad []string
	for _, x := range all {
		col, tbl := pgx.Identifier{x.col}.Sanitize(), pgx.Identifier{x.table}.Sanitize()
		rows, err := pool.Query(ctx, `SELECT `+col+`::text FROM `+tbl+` WHERE `+col+`::text ~ 'Scrubname[0-9]+'`)
		if err != nil {
			t.Fatalf("scanning %s.%s: %v", x.table, x.col, err)
		}
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				t.Fatalf("scanning a value of %s.%s: %v", x.table, x.col, err)
			}
			for _, tok := range scrubTokenRe.FindAllString(v, -1) {
				if !personalNameTokens[tok] {
					bad = append(bad, x.table+"."+x.col+": "+v)
					break
				}
			}
		}
		rows.Close()
	}
	return bad
}

func TestIntegration_ScrubPlaceholderWordsNeverReadInAMigratedDatabase(t *testing.T) {
	pool := freshMigratedDB(t, "scrubwords")
	if bad := readableTokenOffenders(t, context.Background(), pool); len(bad) != 0 {
		t.Errorf("after migrate, %d value(s) read a scrub placeholder where an ordinary word belongs:\n  %s",
			len(bad), strings.Join(bad, "\n  "))
	}
}

// scrubRow is one value migration 0363 rewrites: the row (a WHERE on its own key), the column, the
// token form the frozen migrations plant and the words they stood for. The dash in the longer values
// is written as an escape so this file carries none.
type scrubRow struct {
	table, where, column, planted, restored string
}

const scrubDash = "\u2014"

var scrubRows = []scrubRow{
	{"roles", `id = 'e7000000-0000-4000-9000-000000000001'`, "name", "Scrubname0012 Scrubname0019", "General Manager"},
	{"roles", `id = 'e7000000-0000-4000-9000-000000000002'`, "name", "Scrubname0010 Scrubname0007", "Financial Controller"},
	{"roles", `id = 'e7000000-0000-4000-9000-000000000004'`, "name", "Branch Scrubname0019", "Branch Manager"},
	{"roles", `id = 'e7000000-0000-4000-9000-000000000005'`, "name", "Procurement Scrubname0019", "Procurement Manager"},
	{"roles", `id = 'e7000000-0000-4000-9000-000000000006'`, "name", "Scrubname0027 Scrubname0019", "Sales Manager"},
	{"roles", `id = 'e7000000-0000-4000-9000-000000000007'`, "name", "Yard Scrubname0019", "Yard Manager"},
	{"roles", `id = 'e7000000-0000-4000-9000-000000000008'`, "name", "Logistics Scrubname0019", "Logistics Manager"},
	{"roles", `id = 'e7000000-0000-4000-9000-000000000010'`, "name", "Scrubname0014 Scrubname0027", "Inside Sales"},
	{"roles", `id = 'e7000000-0000-4000-9000-000000000011'`, "name", "Outside Scrubname0027", "Outside Sales"},
	{"gl_accounts", `code = '4000'`, "name", "Scrubname0027 Revenue", "Sales Revenue"},
	{"gl_accounts", `code = '1165'`, "name", "FN Point-of-Scrubname0026 Relief Recoverable (ON 8%)", "FN Point-of-Sale Relief Recoverable (ON 8%)"},
	{"report_templates", `code = 'sales_by_day'`, "name", "Scrubname0027 by Scrubname0009", "Sales by Day"},
	{"report_templates", `code = 'sales_by_branch'`, "name", "Scrubname0027 by Branch", "Sales by Branch"},
	{"report_templates", `code = 'sales_by_rep'`, "name", "Scrubname0027 by Rep", "Sales by Rep"},
	{"report_templates", `code = 'sales_by_category'`, "name", "Scrubname0027 by Category", "Sales by Category"},
	{"report_templates", `code = 'payments_by_day'`, "name", "Payments by Scrubname0009", "Payments by Day"},
	{"report_templates", `code = 'sales_by_day'`, "legacy_name", "Scrubname0027 Analysis " + scrubDash + " Daily", "Sales Analysis " + scrubDash + " Daily"},
	{"report_templates", `code = 'sales_by_branch'`, "legacy_name", "Scrubname0027 Analysis " + scrubDash + " Branch", "Sales Analysis " + scrubDash + " Branch"},
	{"report_templates", `code = 'sales_by_rep'`, "legacy_name", "Scrubname0027 Analysis " + scrubDash + " Salesperson", "Sales Analysis " + scrubDash + " Salesperson"},
	{"report_templates", `code = 'sales_by_category'`, "legacy_name", "Scrubname0027 Analysis " + scrubDash + " Product Group", "Sales Analysis " + scrubDash + " Product Group"},
	{"dashboard_profile_widgets", `profile = 'admin' AND widget_key = 'hero.sales'`, "title", "Scrubname0027 today", "Sales today"},
	{"dashboard_profile_widgets", `profile = 'counter' AND widget_key = 'hero.sales'`, "title", "Scrubname0027 today", "Sales today"},
	{"dashboard_profile_widgets", `profile = 'manager' AND widget_key = 'sales.trend'`, "title", "Scrubname0027 trend", "Sales trend"},
	{"dashboard_gallery_entries", `profile = 'counter' AND entry_key = 'sales.spark'`, "title", "Scrubname0027 today", "Sales today"},
	{"dashboard_gallery_entries", `profile = 'manager' AND entry_key = 'sales.trend'`, "title", "Scrubname0027 trend", "Sales trend"},
	{"dashboard_gallery_entries", `profile = 'admin' AND entry_key = 'sales.branch'`, "title", "Scrubname0027 by branch", "Sales by branch"},
	{"dashboard_gallery_entries", `profile = 'counter' AND entry_key = 'orders.status'`, "blurb",
		"Scrubname0032 the open book is sitting " + scrubDash + " one bar per lifecycle state.", "Where the open book is sitting " + scrubDash + " one bar per lifecycle state."},
	{"dashboard_gallery_entries", `profile = 'manager' AND entry_key = 'orders.status'`, "blurb",
		"Scrubname0032 the open book is sitting " + scrubDash + " one bar per state.", "Where the open book is sitting " + scrubDash + " one bar per state."},
	{"platform_tour_steps", `chapter_key = 'pick-to-proof' AND step_key = 'where'`, "line",
		"Scrubname0032 are you putting it? The counter reads this back.", "Where are you putting it? The counter reads this back."},
	{"users", `email = 'scrubbed-0009@example.invalid'`, "display_name", "UAT Tier-3 (Scrubname0014 Scrubname0027, Trenton)", "UAT Tier-3 (Inside Sales, Trenton)"},
	// The three homework users keep the personal-name token the scrub took out on purpose; only the
	// ordinary words beside it come back.
	{"users", `id = '40000061-0000-4000-9000-000000000002'`, "display_name",
		"Scrubname0030 (homework " + scrubDash + " Till Scrubname0009)", "Scrubname0030 (homework " + scrubDash + " Till Day)"},
	{"users", `id = '40000061-0000-4000-9000-000000000003'`, "display_name",
		"Scrubname0025 Scrubname0024 (homework " + scrubDash + " Scrubname0026 to Scrubname0029)", "Scrubname0025 Scrubname0024 (homework " + scrubDash + " Sale to Statements)"},
	{"users", `id = '40000061-0000-4000-9000-000000000004'`, "display_name",
		"Scrubname0016 (homework " + scrubDash + " Scrubname0014 Scrubname0027)", "Scrubname0016 (homework " + scrubDash + " Inside Sales)"},
}

func scrubValue(t *testing.T, pool *pgxpool.Pool, r scrubRow) string {
	t.Helper()
	var v string
	if err := pool.QueryRow(context.Background(),
		`SELECT `+r.column+` FROM `+r.table+` WHERE `+r.where).Scan(&v); err != nil {
		t.Fatalf("reading %s.%s where %s: %v", r.table, r.column, r.where, err)
	}
	return v
}

func plantScrubbedWorld(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, r := range scrubRows {
		mustExec(t, ctx, pool, `UPDATE `+r.table+` SET `+r.column+` = $1 WHERE `+r.where, r.planted)
	}
}

func migration0363Body(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", migration0363File))
	if err != nil {
		t.Fatalf("reading migration %s: %v", migration0363File, err)
	}
	return string(body)
}

// A database holding the frozen migrations' own values (the planted world) comes out reading words.
func TestIntegration_ScrubPlaceholderMigrationRestoresTheWords(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "scrubwordsfix")
	plantScrubbedWorld(t, pool)
	for _, r := range scrubRows {
		if got := scrubValue(t, pool, r); got != r.planted {
			t.Fatalf("planting %s.%s (%s) left %q, the leg is not testing the old world", r.table, r.column, r.where, got)
		}
	}
	if bad := readableTokenOffenders(t, ctx, pool); len(bad) == 0 {
		t.Fatal("the planted world carries no readable placeholder, the leg is not testing anything")
	}
	mustExec(t, ctx, pool, migration0363Body(t))
	for _, r := range scrubRows {
		if got := scrubValue(t, pool, r); got != r.restored {
			t.Errorf("%s.%s where %s: got %q, want %q", r.table, r.column, r.where, got, r.restored)
		}
	}
	if bad := readableTokenOffenders(t, ctx, pool); len(bad) != 0 {
		t.Errorf("after 0363, %d value(s) still read a placeholder:\n  %s", len(bad), strings.Join(bad, "\n  "))
	}
}

// A role a dealer renamed, an account a controller retitled and a widget a person retitled are
// somebody's words, not ours: 0363 matches exact current values, so they survive while the rest comes
// back. A copy of a gallery widget a user placed (its title copied from the gallery) is corrected.
func TestIntegration_ScrubPlaceholderMigrationLeavesAnEditedValueAlone(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "scrubwordsedit")
	plantScrubbedWorld(t, pool)

	mustExec(t, ctx, pool, `UPDATE roles SET name = 'Head of Sales' WHERE code = 'SALES_MANAGER'`)
	mustExec(t, ctx, pool, `UPDATE gl_accounts SET name = 'Product Sales' WHERE code = '4000'`)
	mustExec(t, ctx, pool, `UPDATE dashboard_profile_widgets SET title = 'Sales, mine' WHERE profile = 'manager' AND widget_key = 'sales.trend'`)
	mustExec(t, ctx, pool, `UPDATE users SET display_name = 'Tier-3 tester' WHERE email = 'scrubbed-0009@example.invalid'`)
	var placed, edited string
	if err := pool.QueryRow(ctx, `
		WITH u AS (SELECT id FROM users ORDER BY id LIMIT 2),
		     ins AS (
		       INSERT INTO dashboard_user_widgets (user_id, widget_key, shape, measure, title)
		       SELECT id, 'sales.spark', 'stat', 'sales.today',
		              CASE WHEN id = (SELECT id FROM u ORDER BY id LIMIT 1) THEN 'Scrubname0027 today' ELSE 'My own title' END
		         FROM u
		       RETURNING user_id, title)
		SELECT (SELECT user_id::text FROM ins WHERE title = 'Scrubname0027 today'),
		       (SELECT user_id::text FROM ins WHERE title = 'My own title')`).Scan(&placed, &edited); err != nil {
		t.Fatalf("placing user widgets: %v", err)
	}

	mustExec(t, ctx, pool, migration0363Body(t))

	for _, c := range []struct{ what, sql, want string }{
		{"a renamed role", `SELECT name FROM roles WHERE code = 'SALES_MANAGER'`, "Head of Sales"},
		{"a retitled account", `SELECT name FROM gl_accounts WHERE code = '4000'`, "Product Sales"},
		{"a retitled profile widget", `SELECT title FROM dashboard_profile_widgets WHERE profile = 'manager' AND widget_key = 'sales.trend'`, "Sales, mine"},
		{"a renamed user", `SELECT display_name FROM users WHERE email = 'scrubbed-0009@example.invalid'`, "Tier-3 tester"},
		{"a placed widget carrying the token", `SELECT title FROM dashboard_user_widgets WHERE user_id = '` + placed + `' AND widget_key = 'sales.spark'`, "Sales today"},
		{"a placed widget a user retitled", `SELECT title FROM dashboard_user_widgets WHERE user_id = '` + edited + `' AND widget_key = 'sales.spark'`, "My own title"},
		{"a role nobody touched", `SELECT name FROM roles WHERE code = 'INSIDE_SALES'`, "Inside Sales"},
		{"an untouched account", `SELECT name FROM gl_accounts WHERE code = '1165'`, "FN Point-of-Sale Relief Recoverable (ON 8%)"},
	} {
		var got string
		if err := pool.QueryRow(ctx, c.sql).Scan(&got); err != nil {
			t.Fatalf("%s: %v", c.what, err)
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.what, got, c.want)
		}
	}
}

// Nothing but the placeholder rows is touched, and a second run changes nothing, not even an
// updated_at.
func TestIntegration_ScrubPlaceholderMigrationTwiceChangesNothing(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "scrubwordstwice")
	plantScrubbedWorld(t, pool)
	mustExec(t, ctx, pool, migration0363Body(t))

	const fingerprint = `SELECT md5(string_agg(x, '|' ORDER BY x)) FROM (
		SELECT 'r:' || id || ':' || name || ':' || updated_at AS x FROM roles
		UNION ALL SELECT 'g:' || code || ':' || name || ':' || updated_at FROM gl_accounts
		UNION ALL SELECT 't:' || code || ':' || name || ':' || coalesce(legacy_name, '') || ':' || updated_at FROM report_templates
		UNION ALL SELECT 'w:' || profile || widget_key || ':' || title || ':' || updated_at FROM dashboard_profile_widgets
		UNION ALL SELECT 'e:' || profile || entry_key || ':' || title || ':' || blurb || ':' || updated_at FROM dashboard_gallery_entries
		UNION ALL SELECT 's:' || chapter_key || step_key || ':' || line || ':' || updated_at FROM platform_tour_steps
		UNION ALL SELECT 'u:' || id || ':' || display_name || ':' || updated_at FROM users) q`
	var first, second string
	if err := pool.QueryRow(ctx, fingerprint).Scan(&first); err != nil {
		t.Fatal(err)
	}
	mustExec(t, ctx, pool, migration0363Body(t))
	if err := pool.QueryRow(ctx, fingerprint).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Error("a second run of 0363 changed rows, it must match nothing once the words are back")
	}
}

// The dispatch persona's surname. The scrub pass took the first name out of the tour's dispatch login
// (0200) and left the surname, which is a person's name and must not read on any screen. The surname is
// read from the frozen 0200 file at run time, so this file never carries it.
var dispatchPersonaRe = regexp.MustCompile(`'Scrubname0008 ([^ ']+) \(demo `)

const (
	dispatchPersonaID    = "d0200000-0000-4000-9000-000000000001"
	dispatchPersonaLabel = "Dispatch (demo)"
	migration0364File    = "0364_tour_dispatch_persona_surname_goes.sql"
)

func dispatchPersonaSurname(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "0200_tour_demo_personas.sql"))
	if err != nil {
		t.Fatalf("reading migration 0200: %v", err)
	}
	m := dispatchPersonaRe.FindSubmatch(body)
	if m == nil {
		t.Fatal("0200 no longer plants the dispatch persona's scrub-named login, the leg is not testing anything")
	}
	return string(m[1])
}

// surnameOffenders scans every text-like column of every base table for the surname as a whole word.
func surnameOffenders(t *testing.T, ctx context.Context, pool *pgxpool.Pool, surname string) []string {
	return columnsMatching(t, ctx, pool, `~*`, `\m`+surname+`\M`)
}

// columnsMatching returns "table.column" for every text-like column holding a value that matches the
// Postgres regex under the given operator.
func columnsMatching(t *testing.T, ctx context.Context, pool *pgxpool.Pool, op, re string) []string {
	t.Helper()
	cols, err := pool.Query(ctx, `
		SELECT c.table_name, c.column_name
		  FROM information_schema.columns c
		  JOIN information_schema.tables x ON x.table_schema = c.table_schema AND x.table_name = c.table_name
		 WHERE c.table_schema = 'public' AND x.table_type = 'BASE TABLE'
		   AND c.data_type IN ('text', 'character varying', 'json', 'jsonb', 'ARRAY', 'USER-DEFINED')
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
	if len(all) < 100 {
		t.Fatalf("only %d text-like columns found, the scan is not looking at the schema", len(all))
	}
	var bad []string
	for _, x := range all {
		col, tbl := pgx.Identifier{x.col}.Sanitize(), pgx.Identifier{x.table}.Sanitize()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+tbl+` WHERE `+col+`::text `+op+` $1`, re).Scan(&n); err != nil {
			t.Fatalf("scanning %s.%s: %v", x.table, x.col, err)
		}
		if n > 0 {
			bad = append(bad, x.table+"."+x.col)
		}
	}
	return bad
}

func TestIntegration_DispatchPersonaSurnameNeverReadsInAMigratedDatabase(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "dispatchsurname")
	if bad := surnameOffenders(t, ctx, pool, dispatchPersonaSurname(t)); len(bad) != 0 {
		t.Errorf("after migrate, the dispatch persona's surname still reads in: %s", strings.Join(bad, ", "))
	}
	var got string
	if err := pool.QueryRow(ctx, `SELECT display_name FROM users WHERE id = $1::uuid`, dispatchPersonaID).Scan(&got); err != nil {
		t.Fatalf("reading the dispatch persona: %v", err)
	}
	if got != dispatchPersonaLabel {
		t.Errorf("dispatch persona display name = %q, want %q", got, dispatchPersonaLabel)
	}
}

// A login somebody renamed is theirs: the migration matches the exact planted value only, and a second
// run changes nothing.
func TestIntegration_DispatchPersonaMigrationLeavesARenamedLoginAlone(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "dispatchedit")
	body, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", migration0364File))
	if err != nil {
		t.Fatalf("reading migration %s: %v", migration0364File, err)
	}
	mustExec(t, ctx, pool, `UPDATE users SET display_name = 'Night dispatcher' WHERE id = $1::uuid`, dispatchPersonaID)
	mustExec(t, ctx, pool, string(body))
	var got string
	if err := pool.QueryRow(ctx, `SELECT display_name FROM users WHERE id = $1::uuid`, dispatchPersonaID).Scan(&got); err != nil {
		t.Fatalf("reading the dispatch persona: %v", err)
	}
	if got != "Night dispatcher" {
		t.Errorf("a renamed login was overwritten: %q", got)
	}
}

// firstNameWords are the common words PR 235 restored after a placeholder; a placeholder followed by one of
// them is not a first name beside a surname.
const firstNameWords = `Revenue|Analysis|Manager|Controller|Sales|Statements`

// A placeholder that stands for a first name, followed by a capitalised word that is not one of those, is
// a first name beside an unscrubbed surname (a second placeholder is a scrubbed surname, fine). No value in a migrated database may read like that, in any
// text column (a user's display name, a conversation title, a message, a JSON document). The offenders
// are reported by table and column only, so a failure never prints the surname.
func TestIntegration_NoFirstNamePlaceholderReadsBesideASurnameInAMigratedDatabase(t *testing.T) {
	pool := freshMigratedDB(t, "firstnamepair")
	bad := columnsMatching(t, context.Background(), pool, "~", `Scrubname[0-9]+ (?!Scrubname|(`+firstNameWords+`)\M)[A-Z][a-z]+`)
	if len(bad) != 0 {
		t.Errorf("after migrate, a scrub placeholder for a first name is followed by a surname in: %s", strings.Join(bad, ", "))
	}
}
