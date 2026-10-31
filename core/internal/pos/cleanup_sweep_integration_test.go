//go:build integration

package pos

// cleanup_sweep_integration_test.go, plan row 0.39: every integration leg in this package
// leaves the shared database exactly as it found it.
//
// Why: CI runs every package on one database in order. The pos legs plant customers, orders,
// invoices, payments, journals and more, and their own cleanups either deleted the customer
// while orders and invoices still pointed at it (refused, and the error ignored) or never
// deleted the rest at all. The rows left behind (18 zero day CONTRACTOR customers among them)
// changed what later packages' legs picked first (printing, quoteassist).
//
// How: intPool(t) calls registerLegSweep, which records the primary key of every row in every
// table that has one, and at cleanup deletes each row that was not there, children before
// parents (the foreign key graph, read from the catalog), in one transaction. Any delete that
// is refused fails the test loudly and names the table. The sweep is registered before a leg's
// own cleanups, so it runs after them (cleanups run last in, first out): a leg's own deletes
// still run first, and the sweep catches whatever they did not.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

type sweepTable struct {
	name    string
	keyExpr string // the row's primary key as one text value
}

type sweepPlan struct {
	tables []sweepTable // children before parents
}

var (
	sweepPlanOnce sync.Once
	sweepPlanVal  *sweepPlan
	sweepPlanErr  error
)

// sweepCatalog reads the tables that have a primary key and orders them children first.
func sweepCatalog(ctx context.Context, pool *pgxpool.Pool) (*sweepPlan, error) {
	sweepPlanOnce.Do(func() {
		keys := map[string][]string{}
		rows, err := pool.Query(ctx, `
			SELECT c.relname, a.attname
			  FROM pg_class c
			  JOIN pg_namespace n ON n.oid = c.relnamespace AND n.nspname = 'public'
			  JOIN pg_index i ON i.indrelid = c.oid AND i.indisprimary
			  JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord) ON true
			  JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = k.attnum
			 WHERE c.relkind = 'r' AND c.relname <> 'schema_migrations'
			 ORDER BY c.relname, k.ord`)
		if err != nil {
			sweepPlanErr = fmt.Errorf("read primary keys: %w", err)
			return
		}
		for rows.Next() {
			var tbl, col string
			if err := rows.Scan(&tbl, &col); err != nil {
				rows.Close()
				sweepPlanErr = fmt.Errorf("scan primary keys: %w", err)
				return
			}
			keys[tbl] = append(keys[tbl], col)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			sweepPlanErr = fmt.Errorf("primary keys: %w", err)
			return
		}

		// child -> parents among the keyed tables (self references do not order anything).
		parentsOf := map[string]map[string]bool{}
		frows, err := pool.Query(ctx, `
			SELECT conrelid::regclass::text, confrelid::regclass::text
			  FROM pg_constraint WHERE contype = 'f'`)
		if err != nil {
			sweepPlanErr = fmt.Errorf("read foreign keys: %w", err)
			return
		}
		for frows.Next() {
			var child, parent string
			if err := frows.Scan(&child, &parent); err != nil {
				frows.Close()
				sweepPlanErr = fmt.Errorf("scan foreign keys: %w", err)
				return
			}
			child, parent = strings.TrimPrefix(child, "public."), strings.TrimPrefix(parent, "public.")
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
			sweepPlanErr = fmt.Errorf("foreign keys: %w", err)
			return
		}

		// Children first: a table is ready to delete once every keyed table that points at it is done.
		names := make([]string, 0, len(keys))
		for n := range keys {
			names = append(names, n)
		}
		sort.Strings(names)
		pointedAtBy := map[string]int{}
		for _, child := range names {
			for parent := range parentsOf[child] {
				if _, ok := keys[parent]; ok {
					pointedAtBy[parent]++
				}
			}
		}
		done := map[string]bool{}
		var ordered []string
		for len(ordered) < len(names) {
			progressed := false
			for _, n := range names {
				if done[n] || pointedAtBy[n] > 0 {
					continue
				}
				done[n] = true
				ordered = append(ordered, n)
				progressed = true
				for parent := range parentsOf[n] {
					if _, ok := keys[parent]; ok {
						pointedAtBy[parent]--
					}
				}
			}
			if !progressed { // a cycle: take the rest in name order, a refused delete will say which
				for _, n := range names {
					if !done[n] {
						done[n] = true
						ordered = append(ordered, n)
					}
				}
			}
		}

		plan := &sweepPlan{}
		for _, n := range ordered {
			cols := make([]string, len(keys[n]))
			for i, c := range keys[n] {
				cols[i] = fmt.Sprintf(`%q::text`, c)
			}
			plan.tables = append(plan.tables, sweepTable{
				name:    n,
				keyExpr: "concat_ws('|', " + strings.Join(cols, ", ") + ")",
			})
		}
		sweepPlanVal = plan
	})
	return sweepPlanVal, sweepPlanErr
}

// sweepSnapshot returns every keyed table's row keys in one round trip.
func sweepSnapshot(ctx context.Context, pool *pgxpool.Pool, plan *sweepPlan) (map[string]map[string]struct{}, error) {
	parts := make([]string, len(plan.tables))
	for i, tb := range plan.tables {
		parts[i] = fmt.Sprintf(`SELECT %d AS t, %s AS k FROM %q`, i, tb.keyExpr, tb.name)
	}
	rows, err := pool.Query(ctx, strings.Join(parts, " UNION ALL "))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	snap := make(map[string]map[string]struct{}, len(plan.tables))
	for rows.Next() {
		var ti int
		var k string
		if err := rows.Scan(&ti, &k); err != nil {
			return nil, err
		}
		name := plan.tables[ti].name
		if snap[name] == nil {
			snap[name] = map[string]struct{}{}
		}
		snap[name][k] = struct{}{}
	}
	return snap, rows.Err()
}

// registerLegSweep records the database's rows now and deletes every row a leg added when the
// leg ends. It fails the leg, naming the table, when a delete is refused.
func registerLegSweep(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	plan, err := sweepCatalog(ctx, pool)
	if err != nil {
		t.Fatalf("leg sweep: read the catalog: %v", err)
	}
	before, err := sweepSnapshot(ctx, pool, plan)
	if err != nil {
		t.Fatalf("leg sweep: snapshot: %v", err)
	}
	t.Cleanup(func() {
		after, err := sweepSnapshot(ctx, pool, plan)
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
			for k := range after[tb.name] {
				if _, had := before[tb.name][k]; !had {
					added = append(added, k)
				}
			}
			if len(added) == 0 {
				continue
			}
			if _, err := tx.Exec(ctx,
				fmt.Sprintf(`DELETE FROM %q WHERE %s = ANY($1::text[])`, tb.name, tb.keyExpr), added); err != nil {
				t.Errorf("leg sweep: delete %d leftover row(s) from %s: %v", len(added), tb.name, err)
				return
			}
		}
		if err := tx.Commit(ctx); err != nil {
			t.Errorf("leg sweep: commit: %v", err)
		}
	})
}
