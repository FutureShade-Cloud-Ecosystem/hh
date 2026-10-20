// Package testsupport is the database harness the assistant's integration and parity suites share: an
// admin connection to the migrated database, a signing key and login passwords provisioned for the test
// and removed after, the staff read pool opened over them, and the seeded world topped up to two branches.
// It is imported by tests only; nothing in a binary imports it.
package testsupport

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/redact"
)

// Env is one test's view of the migrated database.
type Env struct {
	Admin    *pgx.Conn
	AdminURL string
	Roles    readtx.Roles
	Readers  *readtx.Readers
	Key      string
}

// Ctx is a context that ends with the test.
func Ctx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func random(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// Open provisions and opens the staff read path on the database DATABASE_URL names. The test skips
// when it is not set (the integration job sets it).
func Open(t *testing.T) *Env {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := Ctx(t)
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	var db string
	if err := admin.QueryRow(ctx, `SELECT current_database()`).Scan(&db); err != nil {
		t.Fatal(err)
	}
	roles := readtx.RolesFor(db)

	key := "ts-key-" + random(t, 24)
	kid := int(readtx.KeyID(redact.NewSecret(key)))
	if _, err := admin.Exec(ctx, `INSERT INTO ai.claims_key (key_id, key) VALUES ($1, $2)`, kid, key); err != nil {
		t.Fatalf("insert key: %v", err)
	}
	pw := "ts-pw-" + random(t, 16)
	if _, err := admin.Exec(ctx, `ALTER ROLE `+pgx.Identifier{roles.Login}.Sanitize()+` PASSWORD '`+pw+`'`); err != nil {
		t.Fatalf("set password: %v", err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), `DELETE FROM ai.claims_key WHERE key_id = $1`, kid)
		_, _ = c.Exec(context.Background(), `ALTER ROLE `+pgx.Identifier{roles.Login}.Sanitize()+` PASSWORD NULL`)
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(roles.Login, pw)
	rs, err := readtx.Open(ctx, readtx.Config{ReaderURL: redact.NewSecret(u.String()), ClaimsKey: redact.NewSecret(key)})
	if err != nil {
		t.Fatalf("open the read path: %v", err)
	}
	t.Cleanup(rs.Close)
	return &Env{Admin: admin, AdminURL: dsn, Roles: roles, Readers: rs, Key: key}
}

// PlantSecondBranch makes sure orders exist at more than one branch: the seed keeps them all at one, so
// it adds two committed orders (the second with two lines) at another branch and removes them when the
// test ends. A parity test that would otherwise skip on a one branch world must not.
func (e *Env) PlantSecondBranch(t *testing.T) {
	t.Helper()
	ctx := Ctx(t)
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
	tag := random(t, 3)
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
func (e *Env) Branches(t *testing.T) []string {
	t.Helper()
	rows, err := e.Admin.Query(Ctx(t), `SELECT DISTINCT branch_id::text FROM orders ORDER BY 1`)
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
