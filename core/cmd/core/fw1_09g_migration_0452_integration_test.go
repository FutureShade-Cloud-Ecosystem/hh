//go:build integration

package main

// fw1_09g_migration_0452_integration_test.go, FW1-09g's migration leg: migration 0452 plants
// the year end close's OWN marker, gl_year_end_closes, and backfills the years an upgraded
// box had already closed.
//
// THE MARKER. One row per closed fiscal year. fiscal_year is the PRIMARY KEY, so a year is
// closed once and only once whatever the shape of its books.
//
// THE BACKFILL RULE, in full: a year gets a row when BOTH hold.
//
//	1. every one of the year's fiscal periods is CLOSED (and the year has at least one
//	   period: a year with no periods was never closed), AND
//	2. the year carries EITHER a CLOSING journal OR a GL_YEAR_END_CLOSE audit row.
//
// The row is built from that evidence and from nothing else: closed_at is the evidence's own
// instant (the audit row's created_at, else the closing journal's), closed_by is the
// evidence's own actor (the audit row's actor_id, else the closing journal's created_by),
// and reason is the GL_YEAR_END_CLOSE audit row's own reason (NULL when there is no such a
// row: a reason is never invented). NO ACTOR IS INVENTED: closed_by is NULL only where the
// evidence carries none, and only for a backfilled row.
//
// A year whose periods are all CLOSED but which carries NO evidence is NOT backfilled: twelve
// months sealed one by one is not a year end close, and the marker is what tells the two
// apart from here on.
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 -run FW1_09g ./cmd/core/
import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// fw1_09gMigrationBody reads migration 0452's own file: the file the deploy applies, not a
// copy of it.
func fw1_09gMigrationBody(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "0452_gl_year_end_closes.sql"))
	if err != nil {
		t.Fatalf("read migration 0452: %v", err)
	}
	return string(body)
}

// fw1_09gPlantPeriods plants the year's twelve periods. Every one of them is sealed but the
// last when the year is to be left half closed, which is the shape that must NOT be
// backfilled: a year with one OPEN period is a year that was never closed.
func fw1_09gPlantPeriods(t *testing.T, pool *pgxpool.Pool, year int, actor string, allSealed bool) {
	t.Helper()
	ctx := context.Background()
	for month := 1; month <= 12; month++ {
		status := "CLOSED"
		if !allSealed && month == 12 {
			status = "OPEN"
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO fiscal_periods (year, month, status, closed_at, closed_by)
			 VALUES ($1,$2,$3, CASE WHEN $3 = 'CLOSED' THEN '2024-01-05 12:00:00+00'::timestamptz ELSE NULL END,
			                     CASE WHEN $3 = 'CLOSED' THEN $4::uuid ELSE NULL END)
			 ON CONFLICT (year, month) DO UPDATE SET status = EXCLUDED.status`, year, month, status, actor); err != nil {
			t.Fatalf("plant the period %d-%02d: %v", year, month, err)
		}
	}
}

// fw1_09gPlantClosing plants the CLOSING journal a year end close posts into the closed
// year's own December. Its created_at is its own instant, and the marker takes its
// closed_at from here when no audit row speaks first.
//
// The journal and its two lines land in ONE transaction: the box's own rule (migration
// 0021) holds a POSTED journal to its lines at COMMIT, and a journal with no lines is a
// refusal. The lines carry a branch (migration 0446) and balance (migration 0021), the way
// a closing entry's lines do.
func fw1_09gPlantClosing(t *testing.T, pool *pgxpool.Pool, year int, actor, branch string) {
	t.Helper()
	ctx := context.Background()
	var december string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM fiscal_periods WHERE year=$1 AND month=12`, year).Scan(&december); err != nil {
		t.Fatalf("read the year's December period: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the planting transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var journalID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO gl_journals (fiscal_year, entry_no, period_id, source, memo, status, posted_at, branch_id, created_by, created_at)
		 VALUES ($1, 999, $2::uuid, 'CLOSING', $3, 'POSTED', '2023-12-31 12:00:00+00'::timestamptz, $4::uuid, $5::uuid,
		         '2024-01-05 13:00:00+00'::timestamptz) RETURNING id::text`,
		year, december, "Year-end close FY"+itoa(year), branch, actor).Scan(&journalID); err != nil {
		t.Fatalf("plant the closing journal for %d: %v", year, err)
	}
	for i, line := range []struct {
		account string
		debit   int64
		credit  int64
	}{
		{account: "1000", debit: 100},
		{account: "3900", credit: 100},
	} {
		if _, err := tx.Exec(ctx,
			`INSERT INTO gl_journal_lines (journal_id, line_no, account_id, debit_cents, credit_cents, branch_id)
			 SELECT $1::uuid, $2, a.id, $3, $4, $5::uuid FROM gl_accounts a WHERE a.code = $6`,
			journalID, i+1, nullInt(line.debit), nullInt(line.credit), branch, line.account); err != nil {
			t.Fatalf("plant the closing journal's line for %d: %v", year, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the planted closing journal for %d: %v", year, err)
	}
}

// nullInt reads as NULL when the figure is zero, so the line's own CHECK (one side only)
// is satisfied by the side that carries the money.
func nullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

// fw1_09gPlantYearEndAudit plants the GL_YEAR_END_CLOSE audit row a year end close writes,
// keyed to the year's own January period (audit_log.entity_id is a uuid column). Its
// created_at is its own instant, and its changes carry the year and the reason.
func fw1_09gPlantYearEndAudit(t *testing.T, pool *pgxpool.Pool, year int, actor, reason string) {
	t.Helper()
	var january string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM fiscal_periods WHERE year=$1 AND month=1`, year).Scan(&january); err != nil {
		t.Fatalf("read the year's January period: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO audit_log (action, entity_type, entity_id, actor_id, changes, created_at)
		 VALUES ('GL_YEAR_END_CLOSE', 'gl.period', $1::uuid, $2::uuid, $3::jsonb, '2024-01-05 12:00:00+00'::timestamptz)`,
		january, actor, `{"fiscal_year":`+itoa(year)+`,"reason":"`+reason+`"}`); err != nil {
		t.Fatalf("plant the year end audit row for %d: %v", year, err)
	}
}

// itoa renders a number for the SQL above.
func itoa(n int) string {
	return strconv.Itoa(n)
}

// TestFW1_09g_Migration0452PlantsTheMarkerTableOnAFreshBox is the fresh half of the leg:
// the migration plants the marker table, with the year's own primary key (a year is closed
// once), and the table is empty on a box that has closed nothing.
func TestFW1_09g_Migration0452PlantsTheMarkerTableOnAFreshBox(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "fw1099fresh")

	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name='gl_year_end_closes')`).Scan(&exists); err != nil {
		t.Fatalf("read the table list: %v", err)
	}
	if !exists {
		t.Fatalf("migration 0452 planted no gl_year_end_closes table: the year end close's marker does not exist")
	}
	// The shape: the year is the key, the instant is there, the actor and the reason may be.
	var cols string
	if err := pool.QueryRow(ctx,
		`SELECT string_agg(column_name || ':' || data_type || ':' || is_nullable, ',' ORDER BY ordinal_position)
		   FROM information_schema.columns WHERE table_name='gl_year_end_closes'`).Scan(&cols); err != nil {
		t.Fatalf("read the marker's columns: %v", err)
	}
	want := "fiscal_year:integer:NO,closed_at:timestamp with time zone:NO,closed_by:uuid:YES,reason:text:YES"
	if cols != want {
		t.Fatalf("the marker's columns are %q, want %q", cols, want)
	}
	// The key: the year itself, so a year cannot be closed twice.
	var pk string
	if err := pool.QueryRow(ctx,
		`SELECT a.attname FROM pg_index i JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
		  WHERE i.indrelid = 'gl_year_end_closes'::regclass AND i.indisprimary`).Scan(&pk); err != nil {
		t.Fatalf("read the marker's primary key: %v", err)
	}
	if pk != "fiscal_year" {
		t.Fatalf("the marker's primary key is %q, want fiscal_year (a year is closed once and only once)", pk)
	}
	// A box that has closed nothing carries no marker.
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_year_end_closes`).Scan(&rows); err != nil {
		t.Fatalf("count the marker rows: %v", err)
	}
	if rows != 0 {
		t.Fatalf("a box that has closed nothing carries %d marker rows, want 0", rows)
	}
}

// TestFW1_09g_Migration0452BackfillsTheClosedYearsOnAnUpgradedBox is the upgraded half of
// the leg: a box that was migrated and seeded BEFORE 0452, carrying the years it had
// already closed. The migration plants the table and backfills those years from the
// evidence they left, and leaves the years that were never closed at the year end alone.
func TestFW1_09g_Migration0452BackfillsTheClosedYearsOnAnUpgradedBox(t *testing.T) {
	ctx := context.Background()
	const suffix = "fw1099up"
	pool := freshMigratedDBWithout(t, suffix, "0452_gl_year_end_closes.sql")

	// The upgraded box is a SEEDED box: the demo world is planted on it, the way a real
	// box carries it.
	base := os.Getenv("DATABASE_URL")
	dsn := swapDatabase(base, "demoseed_it_"+suffix)
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("CORE_DATABASE_URL", dsn)
	t.Setenv("ALLOW_SEED", "true")
	if err := seedRole(os.Getenv); err != nil {
		t.Fatalf("seed the upgraded box: %v", err)
	}

	// The actor and the branch the planted evidence carries.
	var actor, branch string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users ORDER BY id LIMIT 1`).Scan(&actor); err != nil {
		t.Fatalf("read a seeded user: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type='BRANCH' ORDER BY id LIMIT 1`).Scan(&branch); err != nil {
		t.Fatalf("read a seeded branch: %v", err)
	}

	// FIVE YEARS, five shapes of the rule.
	//
	//	2071  every period sealed, a CLOSING journal AND an audit row: the row is planted,
	//	      and it is the AUDIT row that names the instant and the reason.
	//	2072  every period sealed, a CLOSING journal only: the row is planted, and the
	//	      JOURNAL names the instant. The reason is NULL: none was ever written.
	//	2073  every period sealed, an audit row only: the row is planted.
	//	2074  every period sealed, NO evidence: NOT planted. Twelve months sealed one by one
	//	      is not a year end close.
	//	2075  a CLOSING journal AND an audit row, but one period still OPEN: NOT planted.
	//	      A year with an OPEN period was never closed.
	fw1_09gPlantPeriods(t, pool, 2071, actor, true)
	fw1_09gPlantClosing(t, pool, 2071, actor, branch)
	fw1_09gPlantYearEndAudit(t, pool, 2071, actor, "the 2071 year end close")

	fw1_09gPlantPeriods(t, pool, 2072, actor, true)
	fw1_09gPlantClosing(t, pool, 2072, actor, branch)

	fw1_09gPlantPeriods(t, pool, 2073, actor, true)
	fw1_09gPlantYearEndAudit(t, pool, 2073, actor, "the 2073 year end close")

	fw1_09gPlantPeriods(t, pool, 2074, actor, true)

	fw1_09gPlantPeriods(t, pool, 2075, actor, false)
	fw1_09gPlantClosing(t, pool, 2075, actor, branch)
	fw1_09gPlantYearEndAudit(t, pool, 2075, actor, "the 2075 year end close")

	// THE MIGRATION, the file the deploy applies.
	if _, err := pool.Exec(ctx, fw1_09gMigrationBody(t)); err != nil {
		t.Fatalf("apply migration 0452: %v", err)
	}

	type marker struct {
		closedBy string
		closedAt time.Time
		reason   string
	}
	readOne := func(year int) (marker, bool) {
		t.Helper()
		var m marker
		err := pool.QueryRow(ctx,
			`SELECT COALESCE(closed_by::text,''), closed_at, COALESCE(reason,'') FROM gl_year_end_closes WHERE fiscal_year=$1`, year).
			Scan(&m.closedBy, &m.closedAt, &m.reason)
		if err != nil {
			return marker{}, false
		}
		return m, true
	}

	// 2071: the audit row names the instant and the reason, and the actor is the
	// evidence's own.
	m, ok := readOne(2071)
	if !ok {
		t.Fatalf("the migration planted no marker for 2071: a year closed at the year end must be marked")
	}
	if m.closedBy != actor {
		t.Fatalf("the marker for 2071 names closed_by %q, want the evidence's own actor %q", m.closedBy, actor)
	}
	if m.closedAt.UTC().Format(time.RFC3339) != "2024-01-05T12:00:00Z" {
		t.Fatalf("the marker for 2071 carries closed_at %s, want the audit row's own instant 2024-01-05T12:00:00Z", m.closedAt.UTC().Format(time.RFC3339))
	}
	if m.reason != "the 2071 year end close" {
		t.Fatalf("the marker for 2071 carries reason %q, want the audit row's own reason", m.reason)
	}

	// 2072: the closing journal names the instant, and the reason is NULL: none was ever
	// written, and none is invented.
	m, ok = readOne(2072)
	if !ok {
		t.Fatalf("the migration planted no marker for 2072: a year closed at the year end must be marked")
	}
	if m.closedBy != actor {
		t.Fatalf("the marker for 2072 names closed_by %q, want the closing journal's own actor %q", m.closedBy, actor)
	}
	if m.closedAt.UTC().Format(time.RFC3339) != "2024-01-05T13:00:00Z" {
		t.Fatalf("the marker for 2072 carries closed_at %s, want the closing journal's own instant 2024-01-05T13:00:00Z", m.closedAt.UTC().Format(time.RFC3339))
	}
	if m.reason != "" {
		t.Fatalf("the marker for 2072 carries reason %q, want none: a reason is never invented", m.reason)
	}

	// 2073: the audit row alone is enough.
	m, ok = readOne(2073)
	if !ok {
		t.Fatalf("the migration planted no marker for 2073: a year closed at the year end must be marked")
	}
	if m.closedBy != actor || m.reason != "the 2073 year end close" {
		t.Fatalf("the marker for 2073 carries closed_by %q reason %q, want the audit row's own actor and reason", m.closedBy, m.reason)
	}

	// 2074: every period sealed and no evidence at all is NOT a closed year.
	if _, ok := readOne(2074); ok {
		t.Fatalf("the migration planted a marker for 2074: twelve months sealed one by one is not a year end close")
	}
	// 2075: one OPEN period is not a closed year either.
	if _, ok := readOne(2075); ok {
		t.Fatalf("the migration planted a marker for 2075: a year with an OPEN period was never closed")
	}

	// Nothing else was planted: the years the box holds that were never closed stay absent.
	var total int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_year_end_closes`).Scan(&total); err != nil {
		t.Fatalf("count the marker rows: %v", err)
	}
	if total != 3 {
		t.Fatalf("the migration planted %d marker rows, want exactly 3 (the three years the box had closed)", total)
	}
}
