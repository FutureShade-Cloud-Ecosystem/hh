//go:build integration

package parity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/jackc/pgx/v5"

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
