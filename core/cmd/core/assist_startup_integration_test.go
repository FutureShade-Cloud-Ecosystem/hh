//go:build integration

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
)

// Against the real migrated database: the superuser app passes; a login that owns
// nothing and has no BYPASSRLS would read filtered tables and is refused by name; a
// BYPASSRLS login passes the table check but is refused until it is a member of the
// function users role; once a member it passes.
func TestAssistStartupCheckAgainstTheMigratedDatabase(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	if err := assistRequireServingRole(ctx, admin); err != nil {
		t.Fatalf("the app role (superuser or owner) must pass: %v", err)
	}
	var db string
	if err := admin.QueryRow(ctx, `SELECT current_database()`).Scan(&db); err != nil {
		t.Fatal(err)
	}
	fn := readtx.RolesFor(db).FunctionUsers

	var b [4]byte
	rand.Read(b[:])
	sfx := hex.EncodeToString(b[:])
	plain, bypass := "s5_serve_plain_"+sfx, "s5_serve_bypass_"+sfx
	pw := "pw-" + sfx + "-0123456789"
	for role, attrs := range map[string]string{plain: "NOSUPERUSER", bypass: "NOSUPERUSER BYPASSRLS"} {
		if _, err := admin.Exec(ctx, `CREATE ROLE `+pgx.Identifier{role}.Sanitize()+` LOGIN `+attrs+` PASSWORD '`+pw+`'`); err != nil {
			t.Fatal(err)
		}
		// Read access to the tables so that only the checks can refuse.
		if _, err := admin.Exec(ctx, `GRANT SELECT ON orders, order_lines TO `+pgx.Identifier{role}.Sanitize()); err != nil {
			t.Fatal(err)
		}
		role := role
		t.Cleanup(func() {
			c, err := pgx.Connect(ctx, base)
			if err != nil {
				return
			}
			defer c.Close(ctx)
			_, _ = c.Exec(ctx, `REVOKE SELECT ON orders, order_lines FROM `+pgx.Identifier{role}.Sanitize())
			_, _ = c.Exec(ctx, `DROP ROLE IF EXISTS `+pgx.Identifier{role}.Sanitize())
		})
	}
	as := func(role string) error {
		u, _ := url.Parse(base)
		u.User = url.UserPassword(role, pw)
		c, err := pgx.Connect(ctx, u.String())
		if err != nil {
			t.Fatalf("connect as %s: %v", role, err)
		}
		defer c.Close(ctx)
		return assistRequireServingRole(ctx, c)
	}
	if err := as(plain); err == nil || !strings.Contains(err.Error(), "orders") {
		t.Errorf("a plain role must be refused, naming a table: %v", err)
	}
	if err := as(bypass); err == nil || !strings.Contains(err.Error(), fn) {
		t.Errorf("a BYPASSRLS role outside the function users role must be refused, naming it: %v", err)
	}
	if _, err := admin.Exec(ctx, `GRANT `+pgx.Identifier{fn}.Sanitize()+` TO `+pgx.Identifier{bypass}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if err := as(bypass); err != nil {
		t.Errorf("a member that bypasses row security must pass: %v", err)
	}
}
