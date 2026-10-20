//go:build demo

package main

// demoworld_seqguard_test.go: the guard that no seeded row of the demo world is left outside the
// device sync feeds. Every sync pull serves only rows with a seq (`seq IS NOT NULL AND seq > cursor`,
// migration 0213 leaves carried rows unsequenced), so a row the seed writes straight into a table
// reaches REST and never reaches a Field or Pro offline view. The guard boots the world the way a
// demo box does and counts the unsequenced rows of EVERY table that has a seq column, read from the
// schema at run time, so a table added later is counted without anyone remembering to list it.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seqTablesInSchema is every base table of the public schema with a seq column.
func seqTablesInSchema(ctx context.Context, t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT c.table_name FROM information_schema.columns c
		  JOIN information_schema.tables t
		    ON t.table_schema = c.table_schema AND t.table_name = c.table_name AND t.table_type = 'BASE TABLE'
		 WHERE c.table_schema = 'public' AND c.column_name = 'seq'
		 ORDER BY c.table_name`)
	if err != nil {
		t.Fatalf("listing the tables with a seq column: %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scanning a table name: %v", err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("listing the tables with a seq column: %v", err)
	}
	return names
}

// unsequencedByTable counts the rows with seq NULL in every seq table that has any, except the tables
// demoSeqNotScoped names with its reason (the repair never touches those).
func unsequencedByTable(ctx context.Context, t *testing.T, pool *pgxpool.Pool) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, n := range seqTablesInSchema(ctx, t, pool) {
		var c int
		if err := pool.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s WHERE seq IS NULL`, n)).Scan(&c); err != nil {
			t.Fatalf("counting the unsequenced rows of %s: %v", n, err)
		}
		if _, left := demoSeqNotScoped[n]; left {
			continue // a table whose seq is not a scope number, or which cannot be updated: see its reason
		}
		if c > 0 {
			out[n] = c
		}
	}
	return out
}

func listUnsequenced(m map[string]int) string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, n := range names {
		parts = append(parts, fmt.Sprintf("%s=%d", n, m[n]))
	}
	return strings.Join(parts, ", ")
}

// TestDemoWorld_NoSeededRowIsUnsequenced: after a fresh demo boot no table with a seq column holds a
// row with seq NULL.
func TestDemoWorld_NoSeededRowIsUnsequenced(t *testing.T) {
	w := bootDriveWorld(t)
	left := unsequencedByTable(context.Background(), t, w.pool)
	if len(left) > 0 {
		t.Fatalf("%d table(s) hold seeded rows no sync pull can serve (seq IS NULL): %s", len(left), listUnsequenced(left))
	}
}

// TestDemoWorld_SeqRepairListMatchesTheSchema: every table with a seq column is named by the repair's one
// declared list (sequenced, or left alone with its reason), and the list names no table the schema
// lacks. A new table with a seq column fails here until someone decides which it is.
func TestDemoWorld_SeqRepairListMatchesTheSchema(t *testing.T) {
	w := bootDriveWorld(t)
	ctx := context.Background()
	listed := demoSeqTableSet()
	inSchema := map[string]bool{}
	for _, n := range seqTablesInSchema(ctx, t, w.pool) {
		inSchema[n] = true
		if !listed[n] {
			t.Errorf("table %s has a seq column and is in neither demoSeqTables nor demoSeqNotScoped", n)
		}
	}
	for n := range listed {
		if !inSchema[n] {
			t.Errorf("the repair list names %s, which has no seq column in the schema", n)
		}
	}
}

// TestDemoWorld_SeqRepairSecondApplyDrawsNothing: a second apply on the same world sequences no row and
// moves no scope counter.
func TestDemoWorld_SeqRepairSecondApplyDrawsNothing(t *testing.T) {
	w := bootDriveWorld(t)
	ctx := context.Background()
	snap := func() string {
		rows, err := w.pool.Query(ctx, `SELECT scope || '=' || next_value FROM scope_sequences ORDER BY scope`)
		if err != nil {
			t.Fatalf("reading the scope counters: %v", err)
		}
		defer rows.Close()
		var parts []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatalf("scan: %v", err)
			}
			parts = append(parts, s)
		}
		return strings.Join(parts, " ")
	}
	before := snap()
	driveApplyAt(t, qwClock)
	if after := snap(); after != before {
		t.Fatalf("a second apply moved the scope counters:\nbefore %s\nafter  %s", before, after)
	}
	if left := unsequencedByTable(ctx, t, w.pool); len(left) > 0 {
		t.Fatalf("rows left unsequenced after a second apply: %s", listUnsequenced(left))
	}
}
