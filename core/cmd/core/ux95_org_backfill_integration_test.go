//go:build integration

package main

// ux95_org_backfill_integration_test.go, UX-95: migration 0433 stamps orders.org_id and tasks.org_id from the
// row's branch where NULL, leaves stamped rows and branches with no org alone, re-draws the sync seq of a
// newly stamped PICK or NEED_WEIGHT task past every old one, is idempotent and deletes nothing.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func migration0433(t *testing.T) string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "db", "migrations", "0433_*.sql"))
	if err != nil || len(files) != 1 {
		t.Fatalf("want exactly one 0433 migration, got %v (%v)", files, err)
	}
	body, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestUX95_Migration0433_BackfillsOrdersAndTasksFromTheBranchOrg(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "ux95m0433")
	body := migration0433(t)
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%v\n%s", err, sql)
		}
	}
	str := func(sql string, args ...any) string {
		t.Helper()
		var s string
		if err := pool.QueryRow(ctx, sql, args...).Scan(&s); err != nil {
			t.Fatalf("%v\n%s", err, sql)
		}
		return s
	}
	num := func(sql string, args ...any) int64 {
		t.Helper()
		var n int64
		if err := pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatalf("%v\n%s", err, sql)
		}
		return n
	}

	// A box with one org on Trenton only: Kingston has no org (the case NOT NULL cannot cover).
	must(`UPDATE locations SET org_id = NULL`)
	must(`DELETE FROM org`)
	must(`DELETE FROM tasks`)
	ordersBefore := num(`SELECT count(*) FROM orders`) // the migrations' demo orders stay; they take the branch org too
	orgA := str(`INSERT INTO org (slug, name) VALUES ('ux95-a', 'A') RETURNING id::text`)
	orgB := str(`INSERT INTO org (slug, name) VALUES ('ux95-b', 'B') RETURNING id::text`)
	const trenton = "5d1b3f6e-0000-4000-9000-000000000001"
	const kingston = "5d1b3f6e-0000-4000-9000-000000000002"
	must(`UPDATE locations SET org_id = $1::uuid WHERE id = $2::uuid`, orgA, trenton)
	cust := str(`INSERT INTO customers (account_no, name) VALUES ('UX95M', 'ux95') RETURNING id::text`)
	order := func(no, branch string, org any) string {
		return str(`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents, org_id)
			VALUES ($1, $2::uuid, $3::uuid, 'CONFIRMED', 'PICKUP', 100, 100, $4::uuid) RETURNING id::text`, no, cust, branch, org)
	}
	oNull := order("#UX95-1", trenton, nil)
	oKing := order("#UX95-2", kingston, nil)
	oOwn := order("#UX95-3", trenton, orgB) // already stamped, a different org than the branch: kept
	task := func(kind, branch string, org any, seq int64) string {
		return str(`INSERT INTO tasks (task_type, order_id, branch_id, priority, status, source, seq, org_id)
			VALUES ($1, $2::uuid, $3::uuid, 'NORMAL', 'QUEUED', 'MANUAL', $4, $5::uuid) RETURNING id::text`, kind, oNull, branch, seq, org)
	}
	tPick := task("PICK", trenton, nil, 5)
	tWeight := task("NEED_WEIGHT", trenton, nil, 6)
	tOther := task("OTHER", trenton, nil, 7)
	tKing := task("PICK", kingston, nil, 8)
	tOwn := task("PICK", trenton, orgB, 9)
	must(`INSERT INTO scope_sequences (scope, next_value) VALUES ('pick_tasks', 100), ('weights', 200)
	      ON CONFLICT (scope) DO UPDATE SET next_value = EXCLUDED.next_value`)

	run := func() {
		t.Helper()
		if _, err := pool.Exec(ctx, body); err != nil {
			t.Fatalf("0433 failed: %v", err)
		}
	}
	orgOf := func(tbl, id string) string {
		return str(`SELECT COALESCE(org_id::text, 'NULL') FROM `+tbl+` WHERE id = $1::uuid`, id)
	}
	run()

	for _, c := range []struct{ tbl, id, want, what string }{
		{"orders", oNull, orgA, "an order at a branch with an org takes it"},
		{"orders", oKing, "NULL", "an order at a branch with no org stays NULL"},
		{"orders", oOwn, orgB, "a stamped order keeps its org"},
		{"tasks", tPick, orgA, "a PICK task takes the branch org"},
		{"tasks", tWeight, orgA, "a NEED_WEIGHT task takes the branch org"},
		{"tasks", tOther, orgA, "a task of another type takes the branch org"},
		{"tasks", tKing, "NULL", "a task at a branch with no org stays NULL"},
		{"tasks", tOwn, orgB, "a stamped task keeps its org"},
	} {
		if got := orgOf(c.tbl, c.id); got != c.want {
			t.Errorf("%s: got %s, want %s", c.what, got, c.want)
		}
	}
	// The stamped PICK and NEED_WEIGHT tasks drew a fresh seq past the stream's old counter; the rest kept theirs.
	if seq := num(`SELECT seq FROM tasks WHERE id = $1::uuid`, tPick); seq != 101 {
		t.Errorf("PICK task seq = %d, want 101 (past the pick_tasks counter 100)", seq)
	}
	if seq := num(`SELECT seq FROM tasks WHERE id = $1::uuid`, tWeight); seq != 201 {
		t.Errorf("NEED_WEIGHT task seq = %d, want 201 (past the weights counter 200)", seq)
	}
	for id, want := range map[string]int64{tOther: 7, tKing: 8, tOwn: 9} {
		if seq := num(`SELECT seq FROM tasks WHERE id = $1::uuid`, id); seq != want {
			t.Errorf("task %s seq = %d, want %d untouched", id, seq, want)
		}
	}
	if n := num(`SELECT next_value FROM scope_sequences WHERE scope = 'pick_tasks'`); n != 101 {
		t.Errorf("pick_tasks counter = %d, want 101", n)
	}
	// Idempotent, deletes nothing: a second run draws nothing and changes no row.
	snap := `SELECT (SELECT COALESCE(string_agg(id::text || COALESCE(org_id::text,'-'), ',' ORDER BY id), '') FROM orders)
	         || (SELECT COALESCE(string_agg(id::text || COALESCE(org_id::text,'-') || seq, ',' ORDER BY id), '') FROM tasks)
	         || (SELECT string_agg(scope || next_value, ',' ORDER BY scope) FROM scope_sequences WHERE scope IN ('pick_tasks','weights'))`
	before := str(snap)
	run()
	if after := str(snap); after != before {
		t.Fatalf("a second run of 0433 changed rows:\n%s\n%s", before, after)
	}
	if n := num(`SELECT count(*) FROM orders`); n != ordersBefore+3 {
		t.Errorf("orders = %d, want %d (nothing deleted)", n, ordersBefore+3)
	}
	if n := num(`SELECT count(*) FROM tasks`); n != 5 {
		t.Errorf("tasks = %d, want 5 (nothing deleted)", n)
	}
}
