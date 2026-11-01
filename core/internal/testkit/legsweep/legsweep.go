package legsweep

// Package legsweep, plan rows 0.39 and 0.43: every integration leg of a package that calls
// Register leaves the shared database as it found it. pos, printing and the counter till fixture
// share it; each package's tests stay in its own package and call Register(t, pool).
//
// Why: CI runs every package on one database in order. The pos legs plant customers, orders,
// invoices, payments, journals and more, and their own cleanups either deleted the customer
// while orders and invoices still pointed at it (refused, and the error ignored) or never
// deleted the rest at all. The rows left behind (18 zero day CONTRACTOR customers among them)
// changed what later packages' legs picked first (printing, quoteassist).
//
// How: intPool(t) calls Register, which records, for every table in a user schema that
// has a primary key, the key of each row and a hash of the whole row (md5 of row_to_json). At
// cleanup it deletes each row whose key was not there, children before parents (the foreign key
// graph, read from the catalog), in one transaction. The sweep is registered before a leg's own
// cleanups, so it runs after them (cleanups run last in, first out): a leg's own deletes still
// run first, and the sweep catches whatever they did not.
//
// What the sweep guarantees, exactly:
//   - Every row added since the snapshot, in a keyed table, is deleted. A delete the database
//     refuses fails the leg and names the table.
//   - After the sweep, a second full read is compared with the snapshot. A row that existed at
//     the snapshot and has since been CHANGED (any column) or DELETED fails the leg, naming
//     the table and the key. The sweep does not restore such a row: the leg must put it back
//     itself, in its own cleanup, which runs first.
//   - A row still present that was not there at the snapshot fails the leg too.
//   - A short list of tables is RESTORED by the sweep rather than failed (sweepRestorable). Each
//     is a table the code under test changes on purpose in nearly every leg, or that a freshly
//     seeded database has changed under it once, so asking every leg to put it back would repeat
//     the same cleanup in a hundred places. The four org stamped tables restore org_id ONLY; any
//     other changed column fails the leg, naming the table and key (sweepRestorable):
//     scope_sequences, order_no_sequences, gl_entry_no_sequences: the numbering counters;
//     pos_policy: the settings row, whose updated_at moves on every save, even one that puts the
//     values back; events_outbox: the dispatcher publishes every pending event, seeded ones
//     included, whenever a leg finalizes an invoice; users, user_roles, user_branch_grants,
//     locations: the Trenton world's StampOrgScope stamps the demo org onto the seeded rows the
//     first time it runs on a freshly seeded database.
//     The sweep writes back the row image it took at the snapshot, after it has deleted the
//     added rows. Restoring a counter is safe because every row that took a number from it was
//     added by the leg and is gone. Nothing else is restored.
//   - A table with no primary key, and a partitioned parent, cannot be swept by a safe key.
//     Those are covered by row count only: a leg that leaves such a table with a different
//     count fails, naming it. They are never deleted from.
//   - Only the sweep's own connection pool's database is covered, and only when legs run one at
//     a time (go test -p 1, no t.Parallel): the sweep deletes EVERY row added since its snapshot,
//     including a concurrent leg's live rows. Register fails a leg that starts while
//     another leg's sweep is live.
//   - Data is compared as text and hashed, so a collision is possible in principle and the fast
//     path (below) compares a count and a sum of hashes per table; neither is a cryptographic
//     proof. It is a tripwire for ordinary test debris, not an audit.
//
// The final check re-reads only the tables that moved during the leg (the ones the sweep
// touched). A table that did not move and is changed by the sweep's own deletes, through an
// ON DELETE cascade or a trigger, is not re-read.
//
// Cost: the first leg reads every keyed table in full (keys and row hashes) and keeps the result.
// Every leg then takes a per table fingerprint (count and sum of row hashes, 330 small rows
// back, no keys shipped) at the start and the end. A leg whose end fingerprint equals its start
// fingerprint did nothing the sweep must undo, and skips the full reads. A later leg whose
// start fingerprint equals the kept full snapshot's reuses it instead of reading again.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type sweepTable struct {
	name    string   // schema qualified, quoted for SQL
	keyCols []string // quoted primary key columns
	keyExpr string   // the row's primary key as one text value: ROW(k1, k2)::text
	setCols []string // quoted non key columns, for the restore
}

// sweepRestorable names the tables the sweep restores from the snapshot's row image instead of
// failing the leg (schema qualified, as the catalog names them), and the columns it writes back.
// A nil list means every non key column. A table with a list restores only those columns: any
// other changed column leaves the row different from the snapshot, and the final check fails the
// leg naming the table and key (row 0.44).
//
// The org stamped tables restore org_id alone, except locations, which restores org_id and
// updated_at: identity.StampOrgScope moves both in one UPDATE (users, user_roles and
// user_branch_grants take org_id only). The assumption: org_id on users, user_roles,
// user_branch_grants and locations has NO foreign key to org (0213 added the column bare), so
// writing the snapshot's org_id back, including NULL, can never be refused or cascade. If a
// later migration adds that foreign key, the restore must be revisited (order against org rows,
// and the org rows the sweep deletes).
var sweepRestorable = map[string][]string{
	"public.scope_sequences":       nil,
	"public.order_no_sequences":    nil,
	"public.gl_entry_no_sequences": nil,
	"public.pos_policy":            nil,
	"public.events_outbox":         nil,
	"public.users":                 {"org_id"},
	"public.user_roles":            {"org_id"},
	"public.user_branch_grants":    {"org_id"},
	"public.locations":             {"org_id", "updated_at"},
}

// keyExprOf is the key expression with every column qualified by alias.
func (tb sweepTable) keyExprOf(alias string) string {
	cols := make([]string, len(tb.keyCols))
	for i, c := range tb.keyCols {
		cols[i] = alias + "." + c
	}
	return "ROW(" + strings.Join(cols, ", ") + ")::text"
}

type sweepPlan struct {
	tables    []sweepTable // keyed tables, children before parents
	countOnly []string     // no primary key, or a partitioned parent: covered by row count
}

// sweepFP is a table's fingerprint: its row count and the sum of its row hashes (empty for the
// count only tables).
type sweepFP struct {
	n    int64
	hash string
}

type sweepSnap struct {
	rows   map[string]map[string]string // table -> key -> row hash
	images map[string]map[string]string // restorable table -> key -> row_to_json
	fp     map[string]sweepFP
}

var (
	sweepMu   sync.Mutex
	sweepLive = map[string]int{}      // test name -> live sweeps, the tripwire for overlapping legs
	sweepDBs  = map[string]*sweepDB{} // connection string -> that database's plan and kept snapshot
)

// sweepDB is what the sweep keeps for one database: its catalog plan (read once) and the last
// full snapshot read (reused while the fingerprint matches). A package that dials two databases
// (the seeded module gate and the core scratch database) gets a plan and a snapshot of each.
type sweepDB struct {
	once sync.Once
	plan *sweepPlan
	err  error
	kept *sweepSnap
}

func sweepDBOf(pool *pgxpool.Pool) *sweepDB {
	key := pool.Config().ConnString()
	sweepMu.Lock()
	defer sweepMu.Unlock()
	if sweepDBs[key] == nil {
		sweepDBs[key] = &sweepDB{}
	}
	return sweepDBs[key]
}

// sweepOrder orders the keyed tables children first. parentsOf maps a table to the tables its
// foreign keys point at. A foreign key cycle emits only the tables that are on a cycle, so a
// refused delete names a cycle member and never a table downstream of it.
func sweepOrder(names []string, parentsOf map[string]map[string]bool) []string {
	sort.Strings(names)
	known := map[string]bool{}
	for _, n := range names {
		known[n] = true
	}
	pointedAtBy := map[string]int{}
	for _, child := range names {
		for parent := range parentsOf[child] {
			if known[parent] {
				pointedAtBy[parent]++
			}
		}
	}
	done := map[string]bool{}
	var ordered []string
	emit := func(n string) {
		done[n] = true
		ordered = append(ordered, n)
		for parent := range parentsOf[n] {
			if known[parent] {
				pointedAtBy[parent]--
			}
		}
	}
	for len(ordered) < len(names) {
		progressed := false
		for _, n := range names {
			if done[n] || pointedAtBy[n] > 0 {
				continue
			}
			emit(n)
			progressed = true
		}
		if progressed {
			continue
		}
		// Stuck: every remaining table is pointed at by another remaining table. Emit the ones
		// that lie on a cycle (they can reach themselves going child to parent through the
		// remaining tables); the tables above the cycle then become ready.
		var cycle []string
		for _, n := range names {
			if done[n] {
				continue
			}
			seen := map[string]bool{}
			stack := []string{n}
			onCycle := false
			for len(stack) > 0 && !onCycle {
				cur := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				for p := range parentsOf[cur] {
					if p == n {
						onCycle = true
						break
					}
					if known[p] && !done[p] && !seen[p] {
						seen[p] = true
						stack = append(stack, p)
					}
				}
			}
			if onCycle {
				cycle = append(cycle, n)
			}
		}
		if len(cycle) == 0 { // not reachable; never loop forever
			for _, n := range names {
				if !done[n] {
					cycle = append(cycle, n)
					break
				}
			}
		}
		for _, n := range cycle {
			emit(n)
		}
	}
	return ordered
}

// sweepCatalog reads the tables in every user schema and splits them into the keyed tables
// (ordered children first) and the count only tables.
func sweepCatalog(ctx context.Context, pool *pgxpool.Pool, db *sweepDB) (*sweepPlan, error) {
	db.once.Do(func() {
		const rel = `format('%I.%I', n.nspname, c.relname)`
		const userSchema = `n.nspname !~ '^pg_' AND n.nspname <> 'information_schema'`
		keys := map[string][]string{}
		rows, err := pool.Query(ctx, `
			SELECT `+rel+`, a.attname
			  FROM pg_class c
			  JOIN pg_namespace n ON n.oid = c.relnamespace AND `+userSchema+`
			  JOIN pg_index i ON i.indrelid = c.oid AND i.indisprimary
			  JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord) ON true
			  JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = k.attnum
			 WHERE c.relkind = 'r' AND NOT (n.nspname = 'public' AND c.relname = 'schema_migrations')
			 ORDER BY 1, k.ord`)
		if err != nil {
			db.err = fmt.Errorf("read primary keys: %w", err)
			return
		}
		for rows.Next() {
			var tbl, col string
			if err := rows.Scan(&tbl, &col); err != nil {
				rows.Close()
				db.err = fmt.Errorf("scan primary keys: %w", err)
				return
			}
			keys[tbl] = append(keys[tbl], col)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			db.err = fmt.Errorf("primary keys: %w", err)
			return
		}

		// Tables the sweep cannot delete from by a safe key: no primary key, or partitioned.
		plan := &sweepPlan{}
		crows, err := pool.Query(ctx, `
			SELECT `+rel+`
			  FROM pg_class c
			  JOIN pg_namespace n ON n.oid = c.relnamespace AND `+userSchema+`
			 WHERE c.relkind IN ('r', 'p')
			   AND NOT (n.nspname = 'public' AND c.relname = 'schema_migrations')
			   AND NOT (c.relkind = 'r' AND EXISTS
			        (SELECT 1 FROM pg_index i WHERE i.indrelid = c.oid AND i.indisprimary))
			 ORDER BY 1`)
		if err != nil {
			db.err = fmt.Errorf("read tables without a key: %w", err)
			return
		}
		for crows.Next() {
			var tbl string
			if err := crows.Scan(&tbl); err != nil {
				crows.Close()
				db.err = fmt.Errorf("scan tables without a key: %w", err)
				return
			}
			plan.countOnly = append(plan.countOnly, tbl)
		}
		crows.Close()
		if err := crows.Err(); err != nil {
			db.err = fmt.Errorf("tables without a key: %w", err)
			return
		}

		// child -> parents among the keyed tables (self references do not order anything).
		parentsOf := map[string]map[string]bool{}
		frows, err := pool.Query(ctx, `
			SELECT format('%I.%I', cn.nspname, cc.relname), format('%I.%I', pn.nspname, pc.relname)
			  FROM pg_constraint f
			  JOIN pg_class cc ON cc.oid = f.conrelid
			  JOIN pg_namespace cn ON cn.oid = cc.relnamespace
			  JOIN pg_class pc ON pc.oid = f.confrelid
			  JOIN pg_namespace pn ON pn.oid = pc.relnamespace
			 WHERE f.contype = 'f'`)
		if err != nil {
			db.err = fmt.Errorf("read foreign keys: %w", err)
			return
		}
		for frows.Next() {
			var child, parent string
			if err := frows.Scan(&child, &parent); err != nil {
				frows.Close()
				db.err = fmt.Errorf("scan foreign keys: %w", err)
				return
			}
			if child == parent {
				continue
			}
			if parentsOf[child] == nil {
				parentsOf[child] = map[string]bool{}
			}
			parentsOf[child][parent] = true
		}
		frows.Close()
		if err := frows.Err(); err != nil {
			db.err = fmt.Errorf("foreign keys: %w", err)
			return
		}

		// The non key columns of the restorable tables, for the restore's UPDATE.
		setCols := map[string][]string{}
		for n, only := range sweepRestorable {
			onlyCols := map[string]bool{}
			for _, c := range only {
				onlyCols[c] = true
			}
			cr, err := pool.Query(ctx, `
				SELECT a.attname FROM pg_attribute a
				 WHERE a.attrelid = $1::regclass AND a.attnum > 0 AND NOT a.attisdropped AND a.attgenerated = ''
				 ORDER BY a.attnum`, n)
			if err != nil {
				db.err = fmt.Errorf("read columns of %s: %w", n, err)
				return
			}
			isKey := map[string]bool{}
			for _, k := range keys[n] {
				isKey[k] = true
			}
			for cr.Next() {
				var c string
				if err := cr.Scan(&c); err != nil {
					cr.Close()
					db.err = fmt.Errorf("scan columns of %s: %w", n, err)
					return
				}
				if !isKey[c] && (len(only) == 0 || onlyCols[c]) {
					setCols[n] = append(setCols[n], pgx.Identifier{c}.Sanitize())
				}
			}
			cr.Close()
			if err := cr.Err(); err != nil {
				db.err = fmt.Errorf("columns of %s: %w", n, err)
				return
			}
			if _, ok := keys[n]; !ok {
				db.err = fmt.Errorf("restorable table %s has no primary key or does not exist", n)
				return
			}
		}

		names := make([]string, 0, len(keys))
		for n := range keys {
			names = append(names, n)
		}
		for _, n := range sweepOrder(names, parentsOf) {
			cols := make([]string, len(keys[n]))
			for i, c := range keys[n] {
				cols[i] = pgx.Identifier{c}.Sanitize()
			}
			// ROW(...)::text quotes and escapes each value, so no two keys can collide.
			plan.tables = append(plan.tables, sweepTable{
				name: n, keyCols: cols, keyExpr: "ROW(" + strings.Join(cols, ", ") + ")::text", setCols: setCols[n]})
		}
		db.plan = plan
	})
	return db.plan, db.err
}

// sweepFingerprint reads every swept table's count and hash sum, and every count only table's
// count, in one round trip. Nothing but one small row per table comes back.
func sweepFingerprint(ctx context.Context, pool *pgxpool.Pool, plan *sweepPlan, only map[string]bool) (map[string]sweepFP, error) {
	var parts []string
	var names []string
	for _, tb := range plan.tables {
		if only != nil && !only[tb.name] {
			continue
		}
		parts = append(parts, fmt.Sprintf(
			`SELECT %d AS i, count(*) AS n, coalesce(sum(hashtextextended(t::text, 0)::numeric), 0)::text AS h FROM %s t`, len(names), tb.name))
		names = append(names, tb.name)
	}
	for _, n := range plan.countOnly {
		if only != nil && !only[n] {
			continue
		}
		parts = append(parts, fmt.Sprintf(`SELECT %d AS i, count(*) AS n, '' AS h FROM %s`, len(names), n))
		names = append(names, n)
	}
	fp := make(map[string]sweepFP, len(names))
	err := sweepRead(ctx, pool, strings.Join(parts, " UNION ALL "), func(rows pgx.Rows) error {
		for rows.Next() {
			var i int
			var f sweepFP
			if err := rows.Scan(&i, &f.n, &f.hash); err != nil {
				return err
			}
			fp[names[i]] = f
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return fp, nil
}

// sweepRead runs one read only query in its own transaction with parallel query off, and hands
// the rows to scan. The fingerprint and the snapshot are one UNION ALL over every swept table,
// which Postgres plans as a Parallel Append; its workers need dynamic shared memory, and a
// server with a small /dev/shm (a CI service container) refuses with SQLSTATE 53100 "could not
// resize shared memory segment". SET LOCAL max_parallel_workers_per_gather = 0 keeps the same
// single round trip and the same result, and needs no segment. It was chosen over reading table
// by table because that would turn one round trip into hundreds. The setting dies with the
// transaction, so no pooled connection carries it away.
func sweepRead(ctx context.Context, pool *pgxpool.Pool, sql string, scan func(rows pgx.Rows) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL max_parallel_workers_per_gather = 0`); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, sql)
	if err != nil {
		return err
	}
	defer rows.Close()
	return scan(rows)
}

// sweepSnapshot reads the row keys and row hashes (and, for the restorable tables, the row
// image) of the named tables in one round trip. A nil only means every swept table.
func sweepSnapshot(ctx context.Context, pool *pgxpool.Pool, plan *sweepPlan, only map[string]bool) (*sweepSnap, error) {
	snap := &sweepSnap{rows: map[string]map[string]string{}, images: map[string]map[string]string{}}
	var parts []string
	var names []string
	for _, tb := range plan.tables {
		if only != nil && !only[tb.name] {
			continue
		}
		img := `''::text`
		if _, ok := sweepRestorable[tb.name]; ok {
			img = `row_to_json(t)::text`
		}
		parts = append(parts, fmt.Sprintf(`SELECT %d AS t, %s AS k, md5(row_to_json(t)::text) AS h, %s AS img FROM %s t`,
			len(names), tb.keyExpr, img, tb.name))
		names = append(names, tb.name)
	}
	if len(parts) == 0 {
		return snap, nil
	}
	err := sweepRead(ctx, pool, strings.Join(parts, " UNION ALL "), func(rows pgx.Rows) error {
		for rows.Next() {
			var ti int
			var k, h, img string
			if err := rows.Scan(&ti, &k, &h, &img); err != nil {
				return err
			}
			name := names[ti]
			if snap.rows[name] == nil {
				snap.rows[name] = map[string]string{}
			}
			snap.rows[name][k] = h
			if img != "" {
				if snap.images[name] == nil {
					snap.images[name] = map[string]string{}
				}
				snap.images[name][k] = img
			}
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return snap, nil
}

// sweepFPDiff lists the tables whose fingerprints differ.
func sweepFPDiff(a, b map[string]sweepFP) []string {
	var out []string
	for n, fa := range a {
		if b[n] != fa {
			out = append(out, n)
		}
	}
	for n := range b {
		if _, ok := a[n]; !ok {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// sweepClaim marks a leg's sweep live, or fails the leg when another leg's sweep is live. A leg
// of a live leg (a subtest, named parent/child) may nest, and so may a second pool in one test.
func sweepClaim(t *testing.T) {
	t.Helper()
	sweepMu.Lock()
	defer sweepMu.Unlock()
	for live := range sweepLive {
		if live == t.Name() || strings.HasPrefix(t.Name(), live+"/") {
			continue
		}
		t.Fatalf("leg sweep: %s started while the sweep of %s is still live. Pos legs must run one at a time "+
			"(go test -p 1, no t.Parallel): a leg's sweep deletes every row added since its snapshot, which "+
			"would include the other leg's live rows", t.Name(), live)
	}
	sweepLive[t.Name()]++
}

func sweepRelease(t *testing.T) {
	sweepMu.Lock()
	defer sweepMu.Unlock()
	if sweepLive[t.Name()]--; sweepLive[t.Name()] <= 0 {
		delete(sweepLive, t.Name())
	}
}

// Register records the database's rows now and deletes every row a leg added when the
// leg ends. It fails the leg, naming the table, when a delete is refused, and names the table
// and key of any row that existed at the start and was changed or deleted. See the file header
// for the exact guarantee.
func Register(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	sweepClaim(t)
	released := false
	release := func() {
		if !released {
			released = true
			sweepRelease(t)
		}
	}
	db := sweepDBOf(pool)
	plan, err := sweepCatalog(ctx, pool, db)
	if err != nil {
		release()
		t.Fatalf("leg sweep: read the catalog: %v", err)
	}
	startFP, err := sweepFingerprint(ctx, pool, plan, nil)
	if err != nil {
		release()
		t.Fatalf("leg sweep: fingerprint: %v", err)
	}
	sweepMu.Lock()
	before := db.kept
	sweepMu.Unlock()
	if before == nil || len(sweepFPDiff(before.fp, startFP)) > 0 {
		before, err = sweepSnapshot(ctx, pool, plan, nil)
		if err != nil {
			release()
			t.Fatalf("leg sweep: snapshot: %v", err)
		}
		before.fp = startFP
		sweepMu.Lock()
		db.kept = before
		sweepMu.Unlock()
	}
	t.Cleanup(func() {
		defer release()
		endFP, err := sweepFingerprint(ctx, pool, plan, nil)
		if err != nil {
			t.Errorf("leg sweep: fingerprint at the end: %v", err)
			return
		}
		if len(sweepFPDiff(before.fp, endFP)) == 0 {
			return // the leg left every table as it found it
		}
		// Only the tables whose fingerprint moved can hold an added row, so only those are read.
		moved := map[string]bool{}
		for _, n := range sweepFPDiff(before.fp, endFP) {
			moved[n] = true
		}
		after, err := sweepSnapshot(ctx, pool, plan, moved)
		if err != nil {
			t.Errorf("leg sweep: read the rows left: %v", err)
			return
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Errorf("leg sweep: begin: %v", err)
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()
		for _, tb := range plan.tables {
			var added []string
			for k := range after.rows[tb.name] {
				if _, had := before.rows[tb.name][k]; !had {
					added = append(added, k)
				}
			}
			if len(added) == 0 {
				continue
			}
			if _, err := tx.Exec(ctx,
				fmt.Sprintf(`DELETE FROM %s WHERE %s = ANY($1::text[])`, tb.name, tb.keyExpr), added); err != nil {
				t.Errorf("leg sweep: delete %d leftover row(s) from %s: %v", len(added), tb.name, err)
				return
			}
		}
		// Put the restorable tables' changed rows back to the image taken at the snapshot.
		for _, tb := range plan.tables {
			for k, img := range before.images[tb.name] {
				if h, ok := after.rows[tb.name][k]; !ok || h == before.rows[tb.name][k] {
					continue
				}
				sets := make([]string, len(tb.setCols))
				for i, c := range tb.setCols {
					sets[i] = c + " = r." + c
				}
				if _, err := tx.Exec(ctx, fmt.Sprintf(
					`UPDATE %s t SET %s FROM json_populate_record(NULL::%s, $2::json) r WHERE %s = $1`,
					tb.name, strings.Join(sets, ", "), tb.name, tb.keyExprOf("t")), k, img); err != nil {
					t.Errorf("leg sweep: restore row %s of %s: %v", k, tb.name, err)
					return
				}
			}
		}
		if err := tx.Commit(ctx); err != nil {
			t.Errorf("leg sweep: commit: %v", err)
			return
		}

		// What the sweep must now find: the snapshot's database, table for table. The tables that
		// moved are fingerprinted again; a match is the whole check, and a table that still
		// differs is read to name its keys.
		finalFP, err := sweepFingerprint(ctx, pool, plan, moved)
		if err != nil {
			t.Errorf("leg sweep: verify after the sweep: %v", err)
			return
		}
		stillOff := map[string]bool{}
		for n, f := range finalFP { // only the moved tables were read
			if before.fp[n] != f {
				stillOff[n] = true
			}
		}
		if len(stillOff) == 0 {
			return
		}
		final, err := sweepSnapshot(ctx, pool, plan, stillOff)
		if err != nil {
			t.Errorf("leg sweep: verify after the sweep: %v", err)
			return
		}
		for _, tb := range plan.tables {
			if !stillOff[tb.name] {
				continue
			}
			var changed, deleted, extra []string
			for k, h := range before.rows[tb.name] {
				fh, ok := final.rows[tb.name][k]
				switch {
				case !ok:
					deleted = append(deleted, k)
				case fh != h:
					changed = append(changed, k)
				}
			}
			for k := range final.rows[tb.name] {
				if _, had := before.rows[tb.name][k]; !had {
					extra = append(extra, k)
				}
			}
			for _, g := range []struct {
				what string
				keys []string
			}{{"CHANGED (the leg must restore it)", changed}, {"DELETED (the leg must put it back)", deleted},
				{"still present although added after the snapshot", extra}} {
				if len(g.keys) == 0 {
					continue
				}
				sort.Strings(g.keys)
				shown := g.keys
				if len(shown) > 5 {
					shown = shown[:5]
				}
				t.Errorf("leg sweep: %d pre existing row(s) of %s %s; first keys: %s",
					len(g.keys), tb.name, g.what, strings.Join(shown, " "))
			}
		}
		for _, n := range plan.countOnly {
			if stillOff[n] {
				t.Errorf("leg sweep: %s has no primary key to sweep by (or is partitioned) and its row count went from %d to %d",
					n, before.fp[n].n, finalFP[n].n)
			}
		}
	})
}
