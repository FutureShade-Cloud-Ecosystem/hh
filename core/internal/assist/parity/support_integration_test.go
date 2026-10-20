//go:build integration

package parity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
)

func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// PlantSecondBranch makes sure orders exist at more than one branch: the seed keeps them all at one, so
// it adds two committed orders (the second with two lines) at another branch and removes them when the
// test ends. A parity test that would otherwise skip on a one branch world must not.
func plantSecondBranch(t *testing.T, e *testsupport.Env) {
	t.Helper()
	ctx := testsupport.Ctx(t)
	var n int
	if err := e.Admin.QueryRow(ctx, `SELECT count(DISTINCT branch_id) FROM orders`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n >= 2 {
		return
	}
	var other string
	if err := e.Admin.QueryRow(ctx, `SELECT id::text FROM locations WHERE id NOT IN (SELECT branch_id FROM orders) ORDER BY id LIMIT 1`).Scan(&other); err != nil {
		t.Fatalf("no second branch to plant orders at: %v", err)
	}
	tag := randomHex(t, 3)
	var ids []string
	for i, no := range []string{"SO-S5-" + tag + "-A", "SO-S5-" + tag + "-B"} {
		var id string
		if err := e.Admin.QueryRow(ctx, `INSERT INTO orders (order_no, customer_id, branch_id, status, total_cents)
			SELECT $1, (SELECT customer_id FROM orders LIMIT 1), $2::uuid, 'CONFIRMED', 12345 RETURNING id::text`, no, other).Scan(&id); err != nil {
			t.Fatalf("plant order: %v", err)
		}
		ids = append(ids, id)
		for ln := 1; ln <= i+1; ln++ {
			if _, err := e.Admin.Exec(ctx, `INSERT INTO order_lines (order_id, line_no, description, qty_entered, uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents)
				SELECT $1::uuid, $2, 'planted line', 2, uom_entered, 2, 100, price_uom, 200 FROM order_lines LIMIT 1`, id, ln); err != nil {
				t.Fatalf("plant line: %v", err)
			}
		}
	}
	dsn := e.AdminURL
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		for _, id := range ids {
			_, _ = c.Exec(context.Background(), `DELETE FROM order_lines WHERE order_id = $1::uuid`, id)
			_, _ = c.Exec(context.Background(), `DELETE FROM orders WHERE id = $1::uuid`, id)
		}
	})
}

// Branches returns the distinct branches that hold orders, in id order.
func branchesWithOrders(t *testing.T, e *testsupport.Env) []string {
	t.Helper()
	rows, err := e.Admin.Query(testsupport.Ctx(t), `SELECT DISTINCT branch_id::text FROM orders ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
	return out
}

// plantMany adds n orders at the branches that hold orders (alternating), each with its own created_at
// (one second apart), and removes them when the test ends. CI's integration job leaves a few hundred
// orders behind from earlier packages; a parity test that only works on a world under data.query's
// page size of 100 is a test that depends on test order.
func plantMany(t *testing.T, env *testsupport.Env, n int) {
	t.Helper()
	ctx := testsupport.Ctx(t)
	tag := randomHex(t, 3)
	branches := branchesWithOrders(t, env)
	if len(branches) < 2 {
		t.Fatalf("need two branches to plant across, have %d", len(branches))
	}
	if _, err := env.Admin.Exec(ctx, `INSERT INTO orders (order_no, customer_id, branch_id, status, total_cents, created_at)
		SELECT 'SO-S5P-' || $1 || '-' || g, (SELECT customer_id FROM orders LIMIT 1), ($2::uuid[])[1 + g % 2], 'CONFIRMED', 100 * g, now() - (g || ' seconds')::interval
		FROM generate_series(1, $3) g`, tag, branches[:2], n); err != nil {
		t.Fatalf("plant orders: %v", err)
	}
	dsn := env.AdminURL
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), `DELETE FROM orders WHERE order_no LIKE $1`, "SO-S5P-"+tag+"-%")
	})
}

// plantStrayStock leaves one stock row at a third location whose id sorts before every real branch's, the way
// another package's fixtures do in CI's shared database. A test that picks its two branches from "every location
// that holds stock, in id order" picks the stray one first. The row and the location are removed after.
func plantStrayStock(t *testing.T, env *testsupport.Env) {
	t.Helper()
	ctx := testsupport.Ctx(t)
	const stray = "00000000-0000-4000-8000-0000000000aa"
	if _, err := env.Admin.Exec(ctx, `INSERT INTO locations (id, branch_id, loc_type, code, name) VALUES ($1::uuid, $1::uuid, 'BRANCH', 'ZZSTRAY', 'Stray fixture branch')`, stray); err != nil {
		t.Fatalf("plant the stray location: %v", err)
	}
	if _, err := env.Admin.Exec(ctx, `INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base)
		SELECT product_id, $1::uuid, $1::uuid, 3 FROM stock_balances LIMIT 1`, stray); err != nil {
		t.Fatalf("plant the stray stock row: %v", err)
	}
	dsn := env.AdminURL
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), `DELETE FROM stock_balances WHERE location_id = $1::uuid`, stray)
		_, _ = c.Exec(context.Background(), `DELETE FROM locations WHERE id = $1::uuid`, stray)
	})
}

// plantBranch adds one BRANCH location of the test's own, removed after. Tests plant their own branches and rows
// and never assume a table holds only the seeded world (the lesson of the stock tests).
func plantBranch(t *testing.T, env *testsupport.Env, tag, name string) string {
	t.Helper()
	ctx := testsupport.Ctx(t)
	var id string
	// A branch is its own branch_id; one statement so the foreign key sees both.
	if err := env.Admin.QueryRow(ctx, `WITH n AS (SELECT gen_random_uuid() AS id)
		INSERT INTO locations (id, branch_id, loc_type, code, name) SELECT id, id, 'BRANCH', $1, $2 FROM n RETURNING id::text`, "S6B"+tag, name).Scan(&id); err != nil {
		t.Fatalf("plant branch %s: %v", name, err)
	}
	dsn := env.AdminURL
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), `DELETE FROM locations WHERE id = $1::uuid`, id)
	})
	return id
}

// everyBranch is the dealer's branch directory as a global caller's cost resolution sees it: every BRANCH
// location in the database, the seeded ones, the test's own and any stray one another package left.
func everyBranch(t *testing.T, env *testsupport.Env) allBranches {
	t.Helper()
	rows, err := env.Admin.Query(testsupport.Ctx(t), `SELECT id::text, name FROM locations WHERE loc_type = 'BRANCH' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out allBranches
	for rows.Next() {
		var b assist.NamedBranch
		if err := rows.Scan(&b.BranchID, &b.Name); err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
	return out
}
