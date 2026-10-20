//go:build integration

package sync

// seqcatalogue_integration_test.go: the database half of the seq trigger guard (migration 0422,
// ADR 0025): the catalogue checks (b) and (e) against pg_trigger and the column catalogue, and the
// trigger's own behaviour on a probe table (insert draws, update draws, an app draw is respected,
// a no op update is skipped, an upsert draws twice, the one time re-draw does not draw again, a
// tombstone draws, and two transactions on one stream commit in order).

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	stdsync "sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func seqPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...)
		}
		t.Skipf(format, a...)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("integration DB not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// - (b) the trigger catalogue -

type trigRow struct {
	table, fn, stream string
	tgtype            int16
	enabled           string
}

func readTriggers(t *testing.T, pool *pgxpool.Pool) []trigRow {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT c.relname, p.proname, split_part(encode(t.tgargs, 'escape'), '\000', 1), t.tgtype, t.tgenabled::text
		  FROM pg_trigger t
		  JOIN pg_class c ON c.oid = t.tgrelid
		  JOIN pg_namespace n ON n.oid = c.relnamespace AND n.nspname = 'public'
		  JOIN pg_proc p ON p.oid = t.tgfoid
		 WHERE NOT t.tgisinternal AND p.proname = 'sync_seq_draw'`)
	if err != nil {
		t.Fatalf("read triggers: %v", err)
	}
	defer rows.Close()
	var out []trigRow
	for rows.Next() {
		var r trigRow
		if err := rows.Scan(&r.table, &r.fn, &r.stream, &r.tgtype, &r.enabled); err != nil {
			t.Fatalf("scan trigger: %v", err)
		}
		out = append(out, r)
	}
	return out
}

// beforeInsertUpdateRow is pg_trigger.tgtype for BEFORE INSERT OR UPDATE FOR EACH ROW:
// ROW (1) + BEFORE (2) + INSERT (4) + UPDATE (16).
const beforeInsertUpdateRow = int16(1 + 2 + 4 + 16)

// triggerFindings is check (b): every mapped table carries the trigger with its stream, in the
// right shape, enabled, and no excluded table or unmapped table carries one.
func triggerFindings(rows []trigRow, streams map[string]string, excluded map[string]string) []string {
	have := map[string]trigRow{}
	var bad []string
	for _, r := range rows {
		if _, dup := have[r.table]; dup {
			bad = append(bad, fmt.Sprintf("table %q carries sync_seq_draw twice", r.table))
		}
		have[r.table] = r
	}
	for tb, stream := range streams {
		r, ok := have[tb]
		switch {
		case !ok:
			bad = append(bad, fmt.Sprintf("table %q has no sync_seq_draw trigger (stream %q)", tb, stream))
		case r.stream != stream:
			bad = append(bad, fmt.Sprintf("table %q draws stream %q, the catalogue says %q", tb, r.stream, stream))
		case r.tgtype != beforeInsertUpdateRow:
			bad = append(bad, fmt.Sprintf("table %q trigger has type %d, want BEFORE INSERT OR UPDATE FOR EACH ROW (%d)", tb, r.tgtype, beforeInsertUpdateRow))
		case r.enabled != "O":
			bad = append(bad, fmt.Sprintf("table %q trigger is not enabled (%s)", tb, r.enabled))
		}
	}
	for tb := range have {
		if _, ok := streams[tb]; ok {
			continue
		}
		if why, ex := excluded[tb]; ex {
			bad = append(bad, fmt.Sprintf("table %q is a declared exclusion (%s) but carries the trigger: update the catalogue", tb, why))
			continue
		}
		bad = append(bad, fmt.Sprintf("table %q carries sync_seq_draw but is not in the catalogue", tb))
	}
	sort.Strings(bad)
	return bad
}

func TestIntegration_SeqCatalogue_EveryMappedTableCarriesItsTrigger(t *testing.T) {
	pool := seqPool(t)
	if bad := triggerFindings(readTriggers(t, pool), seqStreams, seqExcluded); len(bad) > 0 {
		t.Fatalf("trigger catalogue:\n  %s", strings.Join(bad, "\n  "))
	}
}

func TestIntegration_SeqCatalogue_BiteTriggerFindings(t *testing.T) {
	full := []trigRow{}
	for tb, s := range seqStreams {
		full = append(full, trigRow{tb, "sync_seq_draw", s, beforeInsertUpdateRow, "O"})
	}
	if bad := triggerFindings(full, seqStreams, seqExcluded); len(bad) != 0 {
		t.Fatalf("a complete set was reported: %v", bad)
	}
	// Remove one trigger.
	cut := append([]trigRow(nil), full[1:]...)
	if bad := triggerFindings(cut, seqStreams, seqExcluded); len(bad) != 1 || !strings.Contains(bad[0], "no sync_seq_draw trigger") {
		t.Fatalf("a missing trigger was not reported: %v", bad)
	}
	// A wrong stream.
	wrong := append([]trigRow(nil), full...)
	wrong[0].stream = "elsewhere"
	if bad := triggerFindings(wrong, seqStreams, seqExcluded); len(bad) != 1 || !strings.Contains(bad[0], "elsewhere") {
		t.Fatalf("a wrong stream was not reported: %v", bad)
	}
	// A wrong shape, a disabled trigger.
	shape := append([]trigRow(nil), full...)
	shape[0].tgtype = 7
	if bad := triggerFindings(shape, seqStreams, seqExcluded); len(bad) != 1 {
		t.Fatalf("a wrong shape was not reported: %v", bad)
	}
	off := append([]trigRow(nil), full...)
	off[0].enabled = "D"
	if bad := triggerFindings(off, seqStreams, seqExcluded); len(bad) != 1 {
		t.Fatalf("a disabled trigger was not reported: %v", bad)
	}
	// An excluded table that grew the trigger, and an unknown table with one.
	extra := append(append([]trigRow(nil), full...), trigRow{"stock_balances", "sync_seq_draw", "inventory_lookup", beforeInsertUpdateRow, "O"})
	if bad := triggerFindings(extra, seqStreams, seqExcluded); len(bad) != 1 || !strings.Contains(bad[0], "declared exclusion") {
		t.Fatalf("an excluded table with the trigger was not reported: %v", bad)
	}
	extra = append(append([]trigRow(nil), full...), trigRow{"mystery", "sync_seq_draw", "x", beforeInsertUpdateRow, "O"})
	if bad := triggerFindings(extra, seqStreams, seqExcluded); len(bad) != 1 || !strings.Contains(bad[0], "not in the catalogue") {
		t.Fatalf("an unmapped table with the trigger was not reported: %v", bad)
	}
}

// - (e) no column the row comparison cannot compare -

// uncomparableTypes have no equality operator, so NEW IS DISTINCT FROM OLD fails on them.
var uncomparableTypes = []string{"json", "xml", "point", "line", "lseg", "box", "path", "polygon", "circle"}

func uncomparableColumns(t *testing.T, pool *pgxpool.Pool, tables []string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT table_name || '.' || column_name || ' (' || udt_name || ')'
		  FROM information_schema.columns
		 WHERE table_schema = 'public' AND table_name = ANY($1) AND udt_name = ANY($2)
		 ORDER BY 1`, tables, uncomparableTypes)
	if err != nil {
		t.Fatalf("read columns: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		out = append(out, s)
	}
	return out
}

func mappedTableNames() []string {
	var out []string
	for tb := range seqStreams {
		out = append(out, tb)
	}
	sort.Strings(out)
	return out
}

func TestIntegration_SeqCatalogue_NoUncomparableColumnOnATriggerTable(t *testing.T) {
	pool := seqPool(t)
	if bad := uncomparableColumns(t, pool, mappedTableNames()); len(bad) > 0 {
		t.Fatalf("a trigger table carries a column the row comparison cannot compare (the table's UPDATEs would fail): %v", bad)
	}
}

func TestIntegration_SeqCatalogue_BiteUncomparableColumn(t *testing.T) {
	pool := seqPool(t)
	ctx := context.Background()
	const tbl = "sf1_json_probe"
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS `+tbl); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE `+tbl+` (id int PRIMARY KEY, seq bigint, body json)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP TABLE IF EXISTS `+tbl) })
	if bad := uncomparableColumns(t, pool, []string{tbl}); len(bad) != 1 || !strings.Contains(bad[0], "body (json)") {
		t.Fatalf("a json column was not reported: %v", bad)
	}
	// And the reason the check exists: under the trigger, the first UPDATE of that table fails.
	if _, err := pool.Exec(ctx, `CREATE TRIGGER trg_sync_seq_draw BEFORE INSERT OR UPDATE ON `+tbl+` FOR EACH ROW EXECUTE FUNCTION sync_seq_draw('sf1_json_probe')`); err != nil {
		t.Fatalf("trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM scope_sequences WHERE scope = 'sf1_json_probe'`)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO `+tbl+` (id, body) VALUES (1, '{}')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE `+tbl+` SET body = '{"a":1}' WHERE id = 1`); err == nil {
		t.Fatalf("an UPDATE of a json column under the trigger succeeded; the guard's premise is gone")
	}
}

// - the trigger's own behaviour, on a probe table -

const probeStream = "sf1_probe"

type probe struct {
	pool *pgxpool.Pool
}

func newProbe(t *testing.T) *probe {
	t.Helper()
	pool := seqPool(t)
	ctx := context.Background()
	for _, q := range []string{
		`DROP TABLE IF EXISTS sf1_seq_probe`,
		`CREATE TABLE sf1_seq_probe (id int PRIMARY KEY, seq bigint, v int NOT NULL DEFAULT 0, deleted_at timestamptz)`,
		`CREATE TRIGGER trg_sync_seq_draw BEFORE INSERT OR UPDATE ON sf1_seq_probe FOR EACH ROW EXECUTE FUNCTION sync_seq_draw('` + probeStream + `')`,
		`DELETE FROM scope_sequences WHERE scope = '` + probeStream + `'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("probe setup %.40s: %v", q, err)
		}
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DROP TABLE IF EXISTS sf1_seq_probe`)
		_, _ = pool.Exec(c, `DELETE FROM scope_sequences WHERE scope = '`+probeStream+`'`)
	})
	return &probe{pool: pool}
}

func (p *probe) counter(t *testing.T) int64 {
	t.Helper()
	var n int64
	err := p.pool.QueryRow(context.Background(), `SELECT next_value FROM scope_sequences WHERE scope = $1`, probeStream).Scan(&n)
	if err == pgx.ErrNoRows {
		return 0
	}
	if err != nil {
		t.Fatalf("counter: %v", err)
	}
	return n
}

func (p *probe) seq(t *testing.T, id int) int64 {
	t.Helper()
	var s *int64
	if err := p.pool.QueryRow(context.Background(), `SELECT seq FROM sf1_seq_probe WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatalf("seq of %d: %v", id, err)
	}
	if s == nil {
		return -1
	}
	return *s
}

func (p *probe) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := p.pool.Exec(context.Background(), q, args...); err != nil {
		t.Fatalf("%.60s: %v", q, err)
	}
}

func TestIntegration_SeqTrigger_InsertDraws(t *testing.T) {
	p := newProbe(t)
	p.exec(t, `INSERT INTO sf1_seq_probe (id) VALUES (1)`)
	p.exec(t, `INSERT INTO sf1_seq_probe (id) VALUES (2)`)
	if p.seq(t, 1) != 1 || p.seq(t, 2) != 2 || p.counter(t) != 2 {
		t.Fatalf("inserts drew seq %d and %d with the counter at %d, want 1, 2, 2", p.seq(t, 1), p.seq(t, 2), p.counter(t))
	}
}

func TestIntegration_SeqTrigger_UpdateDraws(t *testing.T) {
	p := newProbe(t)
	p.exec(t, `INSERT INTO sf1_seq_probe (id) VALUES (1)`)
	before := p.seq(t, 1)
	p.exec(t, `UPDATE sf1_seq_probe SET v = 7 WHERE id = 1`)
	if after := p.seq(t, 1); after <= before || after != p.counter(t) {
		t.Fatalf("an update left seq at %d (was %d, counter %d)", after, before, p.counter(t))
	}
}

func TestIntegration_SeqTrigger_TombstoneDraws(t *testing.T) {
	p := newProbe(t)
	p.exec(t, `INSERT INTO sf1_seq_probe (id) VALUES (1)`)
	before := p.seq(t, 1)
	p.exec(t, `UPDATE sf1_seq_probe SET deleted_at = now() WHERE id = 1`)
	if after := p.seq(t, 1); after <= before {
		t.Fatalf("a tombstone update kept seq %d (was %d): devices would never learn the delete", after, before)
	}
}

func TestIntegration_SeqTrigger_AnAppDrawIsRespected(t *testing.T) {
	p := newProbe(t)
	// An insert that carries its own seq keeps it and draws nothing.
	p.exec(t, `INSERT INTO sf1_seq_probe (id, seq) VALUES (1, 500)`)
	if p.seq(t, 1) != 500 || p.counter(t) != 0 {
		t.Fatalf("an explicit insert seq became %d with the counter at %d, want 500 and 0", p.seq(t, 1), p.counter(t))
	}
	// An update that sets seq itself (the app's own draw) keeps its value and draws nothing.
	p.exec(t, `UPDATE sf1_seq_probe SET v = 3, seq = 900 WHERE id = 1`)
	if p.seq(t, 1) != 900 || p.counter(t) != 0 {
		t.Fatalf("an explicit update seq became %d with the counter at %d, want 900 and 0", p.seq(t, 1), p.counter(t))
	}
	// The app's own draw statement, the shape every adapter runs: the trigger adds no second draw.
	p.exec(t, `WITH s AS (INSERT INTO scope_sequences (scope) VALUES ('`+probeStream+`')
		ON CONFLICT (scope) DO UPDATE SET next_value = scope_sequences.next_value + 1 RETURNING next_value)
		UPDATE sf1_seq_probe SET v = 4, seq = (SELECT next_value FROM s) WHERE id = 1`)
	if p.seq(t, 1) != 1 || p.counter(t) != 1 {
		t.Fatalf("an app draw left seq %d and the counter %d, want 1 and 1 (one draw, not two)", p.seq(t, 1), p.counter(t))
	}
}

func TestIntegration_SeqTrigger_ANoOpUpdateIsSkipped(t *testing.T) {
	p := newProbe(t)
	p.exec(t, `INSERT INTO sf1_seq_probe (id, v) VALUES (1, 5)`)
	seq, counter := p.seq(t, 1), p.counter(t)
	p.exec(t, `UPDATE sf1_seq_probe SET v = v WHERE id = 1`)
	p.exec(t, `UPDATE sf1_seq_probe SET v = 5 WHERE id = 1`)
	if p.seq(t, 1) != seq || p.counter(t) != counter {
		t.Fatalf("a no op update moved seq %d to %d or the counter %d to %d", seq, p.seq(t, 1), counter, p.counter(t))
	}
}

// An upsert draws twice: the BEFORE INSERT trigger for the proposed row, then the BEFORE UPDATE
// trigger on the conflict arm. A gap, documented in ADR 0025; the row still ends on the latest.
func TestIntegration_SeqTrigger_AnUpsertDrawsTwiceAndLeavesAGap(t *testing.T) {
	p := newProbe(t)
	p.exec(t, `INSERT INTO sf1_seq_probe (id) VALUES (1)`)
	if p.counter(t) != 1 {
		t.Fatalf("counter %d after the first insert, want 1", p.counter(t))
	}
	p.exec(t, `INSERT INTO sf1_seq_probe (id, v) VALUES (1, 9) ON CONFLICT (id) DO UPDATE SET v = EXCLUDED.v`)
	if p.counter(t) != 3 {
		t.Fatalf("counter %d after an upsert on conflict, want 3 (two draws)", p.counter(t))
	}
	if p.seq(t, 1) != 3 {
		t.Fatalf("the upserted row's seq %d, want the latest draw, 3", p.seq(t, 1))
	}
	// A fresh upsert (no conflict) draws once.
	p.exec(t, `INSERT INTO sf1_seq_probe (id, v) VALUES (2, 1) ON CONFLICT (id) DO UPDATE SET v = EXCLUDED.v`)
	if p.counter(t) != 4 || p.seq(t, 2) != 4 {
		t.Fatalf("a non conflicting upsert: counter %d seq %d, want 4 and 4", p.counter(t), p.seq(t, 2))
	}
}

// The migration's re-draw sets seq explicitly (a touch would be skipped by the no op condition),
// so the trigger must not draw again on those updates.
func TestIntegration_SeqTrigger_TheReDrawDoesNotDoubleDraw(t *testing.T) {
	p := newProbe(t)
	for id := 1; id <= 5; id++ {
		p.exec(t, `INSERT INTO sf1_seq_probe (id, seq) VALUES ($1, NULL)`, id)
	}
	p.exec(t, `UPDATE sf1_seq_probe SET deleted_at = now() WHERE id = 5`) // a tombstone, re-drawn too
	// The same statement forms the migration runs, against the probe.
	var n, base int64
	if err := p.pool.QueryRow(context.Background(), `SELECT count(*) FROM sf1_seq_probe`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if err := p.pool.QueryRow(context.Background(),
		`UPDATE scope_sequences SET next_value = next_value + $1 WHERE scope = $2 RETURNING next_value - $1`, n, probeStream).Scan(&base); err != nil {
		t.Fatalf("advance: %v", err)
	}
	counter := p.counter(t)
	p.exec(t, fmt.Sprintf(`UPDATE sf1_seq_probe x SET seq = %d + r.rn
		FROM (SELECT ctid AS c, row_number() OVER (ORDER BY seq NULLS FIRST, ctid) AS rn FROM sf1_seq_probe) r
		WHERE x.ctid = r.c`, base))
	if p.counter(t) != counter {
		t.Fatalf("the re-draw moved the counter %d to %d: the trigger drew again", counter, p.counter(t))
	}
	seen := map[int64]bool{}
	for id := 1; id <= 5; id++ {
		s := p.seq(t, id)
		if s <= base || s > base+n || seen[s] {
			t.Fatalf("row %d re-drawn to %d, want a distinct value in (%d, %d]", id, s, base, base+n)
		}
		seen[s] = true
	}
}

// Seq order is commit order per stream: the counter row the first transaction drew is held to its
// commit, so a second transaction's draw waits for it and lands above it.
func TestIntegration_SeqTrigger_TwoTransactionsCommitInOrder(t *testing.T) {
	p := newProbe(t)
	ctx := context.Background()
	tx1, err := p.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx1.Rollback(ctx)
	if _, err := tx1.Exec(ctx, `INSERT INTO sf1_seq_probe (id) VALUES (1)`); err != nil {
		t.Fatalf("tx1 insert: %v", err)
	}

	var wg stdsync.WaitGroup
	done := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := p.pool.Exec(ctx, `INSERT INTO sf1_seq_probe (id) VALUES (2)`)
		done <- err
	}()

	// The second insert must be waiting on the counter row tx1 holds: poll the lock wait, bounded.
	deadline := time.Now().Add(15 * time.Second)
	for {
		var waiting int
		if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			 WHERE wait_event_type = 'Lock' AND query LIKE 'INSERT INTO sf1_seq_probe%'`).Scan(&waiting); err != nil {
			t.Fatalf("activity: %v", err)
		}
		if waiting > 0 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("the second transaction's insert returned (%v) while the first held the counter: draws are not serialised to commit", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the second insert never showed as waiting on the first's counter row")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := tx1.Commit(ctx); err != nil {
		t.Fatalf("tx1 commit: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("tx2 insert: %v", err)
	}
	wg.Wait()
	if p.seq(t, 1) >= p.seq(t, 2) {
		t.Fatalf("seq order is not commit order: first transaction %d, second %d", p.seq(t, 1), p.seq(t, 2))
	}
}

// - (f) no UPDATE of a trigger table names a key column -
//
// A BEFORE UPDATE row trigger makes Postgres lock the row FOR UPDATE, not FOR NO KEY UPDATE,
// whenever the UPDATE's SET list names a key column (a column of a unique index), even when the
// value is unchanged. FOR UPDATE conflicts with the FOR KEY SHARE a referencing insert takes, so a
// batch that reloads a row would block every child insert until it commits (the products reload
// leg pins exactly that). The statements below name a key column and are accepted, each with its
// reason; a new one is a finding.
var keyColumnUpdateAccepted = map[string]string{
	"attachment.idempotency_key":                     "the statement stamps a new key on every write, so the key changes and the row lock is FOR UPDATE with or without the trigger",
	"customer_quote.idempotency_key":                 "same: a new key on every write",
	"plan.idempotency_key":                           "same: a new key on every write",
	"plan_line.idempotency_key":                      "same: a new key on every write",
	"customers.account_no ../customer/loadwriter.go": "the loader's own statement, run only when the account number changes (then FOR UPDATE is the right strength)",
	"products.sku ../product/productload.go":         "the loader's own statement, run only when the sku changes (then FOR UPDATE is the right strength)",
	"dispatch_trucks.code":                           "the desk's truck edit is one statement on the pool (SF-6 owns the table); the lock is held for the statement only",
}

var updateSetRe = regexp.MustCompile(`(?is)\bUPDATE\s+([a-z_][a-z0-9_]*)\s+(?:[a-z_][a-z0-9_]*\s+)?SET\s+(.*?)(?:\bWHERE\b|\bFROM\b|\bRETURNING\b|$)`)
var setColRe = regexp.MustCompile(`(?im)(?:^|,)\s*([a-z_][a-z0-9_]*)\s*=`)

// keyColumnUpdates returns "table.column" for every UPDATE in the files that lists a key column of
// a trigger table in its SET list, with the position.
func keyColumnUpdates(files []goFile, keys map[string]map[string]bool) []string {
	var out []string
	for _, gf := range files {
		if strings.Contains(filepath.ToSlash(gf.path), "/testkit/") {
			continue
		}
		ast.Inspect(gf.file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			for _, m := range updateSetRe.FindAllStringSubmatch(s, -1) {
				tb := strings.ToLower(m[1])
				if keys[tb] == nil {
					continue
				}
				for _, c := range setColRe.FindAllStringSubmatch(m[2], -1) {
					col := strings.ToLower(c[1])
					if keys[tb][col] {
						out = append(out, fmt.Sprintf("%s.%s at %s", tb, col, gf.fset.Position(lit.Pos())))
					}
				}
			}
			return true
		})
	}
	sort.Strings(out)
	return out
}

func readKeyColumns(t *testing.T, pool *pgxpool.Pool, tables []string) map[string]map[string]bool {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT c.relname, a.attname
		  FROM pg_index i
		  JOIN pg_class c ON c.oid = i.indrelid
		  JOIN pg_namespace n ON n.oid = c.relnamespace AND n.nspname = 'public'
		  JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = ANY (i.indkey)
		 WHERE i.indisunique AND i.indpred IS NULL AND i.indexprs IS NULL AND a.attnum > 0
		   AND c.relname = ANY ($1)`, tables)
	if err != nil {
		t.Fatalf("read key columns: %v", err)
	}
	defer rows.Close()
	out := map[string]map[string]bool{}
	for rows.Next() {
		var tb, col string
		if err := rows.Scan(&tb, &col); err != nil {
			t.Fatalf("scan key column: %v", err)
		}
		if out[tb] == nil {
			out[tb] = map[string]bool{}
		}
		out[tb][col] = true
	}
	return out
}

func TestIntegration_SeqCatalogue_NoUpdateOfATriggerTableNamesAKeyColumn(t *testing.T) {
	pool := seqPool(t)
	keys := readKeyColumns(t, pool, mappedTableNames())
	files := parseTree(t, coreRoots)
	var bad []string
	for _, h := range keyColumnUpdates(files, keys) {
		tc := h[:strings.Index(h, " at ")]
		file := h[strings.Index(h, " at ")+4:]
		file = file[:strings.Index(file, ":")]
		if _, ok := keyColumnUpdateAccepted[tc]; ok {
			continue
		}
		if _, ok := keyColumnUpdateAccepted[tc+" "+file]; ok {
			continue
		}
		bad = append(bad, h)
	}
	if len(bad) > 0 {
		t.Fatalf("an UPDATE of a trigger table names a key column in its SET list (the row lock becomes FOR UPDATE and blocks referencing inserts):\n  %s", strings.Join(bad, "\n  "))
	}
}

func TestIntegration_SeqCatalogue_BiteKeyColumnUpdate(t *testing.T) {
	files := plantTree(t, "package p\nfunc bad() { _ = `UPDATE quotes SET quote_no = $2, status = 'X' WHERE id = $1`; _ = `UPDATE quotes SET status = 'X' WHERE id = $1` }\n")
	keys := map[string]map[string]bool{"quotes": {"quote_no": true}}
	got := keyColumnUpdates(files, keys)
	if len(got) != 1 || !strings.HasPrefix(got[0], "quotes.quote_no at ") {
		t.Fatalf("a planted key column update was not found, or a clean one was: %v", got)
	}
}
