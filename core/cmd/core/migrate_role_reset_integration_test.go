//go:build integration

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// A migration that switches role and forgets RESET ROLE must fail loudly and
// leave nothing behind (review r3 P3 note 1). Two shapes: the switched role has
// no privilege on schema_migrations (the runner's bookkeeping insert would fail
// by accident) and it does (the migration would otherwise commit, with whatever
// it made owned as the switched role left it, and the connection still switched).
func TestMigrateRefusesAFileThatLeavesARoleSet(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer admin.Close(ctx)

	for _, tc := range []struct {
		name          string
		grantBookkeep bool
	}{{"role without bookkeeping privilege", false}, {"role with bookkeeping privilege", true}} {
		t.Run(tc.name, func(t *testing.T) {
			var b [4]byte
			rand.Read(b[:])
			suffix := hex.EncodeToString(b[:])
			db, role := "migrate_reset_"+suffix, "migrate_reset_role_"+suffix
			if _, err := admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{db}.Sanitize()); err != nil {
				t.Fatal(err)
			}
			if _, err := admin.Exec(ctx, `CREATE ROLE `+pgx.Identifier{role}.Sanitize()+` NOLOGIN`); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = admin.Exec(ctx, `DROP DATABASE IF EXISTS `+pgx.Identifier{db}.Sanitize()+` WITH (FORCE)`)
				_, _ = admin.Exec(ctx, `DROP ROLE IF EXISTS `+pgx.Identifier{role}.Sanitize())
			})
			u, _ := url.Parse(base)
			u.Path = "/" + db
			dsn := u.String()

			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "db", "migrations"), 0o755); err != nil {
				t.Fatal(err)
			}
			q := pgx.Identifier{role}.Sanitize()
			grant := ""
			if tc.grantBookkeep {
				grant = "GRANT INSERT, SELECT ON schema_migrations TO " + q + ";\n"
			}
			body := "CREATE SCHEMA scratch;\nGRANT ALL ON SCHEMA scratch TO " + q + ";\n" + grant +
				"SET LOCAL ROLE " + q + ";\nCREATE TABLE scratch.made_under_the_role (x int);\n-- no RESET ROLE\n"
			if err := os.WriteFile(filepath.Join(dir, "db", "migrations", "0001_forgot_reset.sql"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Chdir(dir)

			err := migrateRole(func(k string) string {
				if k == "DATABASE_URL" {
					return dsn
				}
				return ""
			})
			if err == nil {
				t.Fatal("migrate accepted a file that left a role set")
			}
			if !strings.Contains(err.Error(), "RESET ROLE") {
				t.Errorf("the failure must say what to do (RESET ROLE), got: %v", err)
			}

			check, err := pgx.Connect(ctx, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer check.Close(ctx)
			var made, recorded bool
			if err := check.QueryRow(ctx, `SELECT to_regclass('scratch.made_under_the_role') IS NOT NULL,
				EXISTS (SELECT 1 FROM schema_migrations WHERE version = '0001_forgot_reset')`).Scan(&made, &recorded); err != nil {
				t.Fatal(err)
			}
			if made || recorded {
				t.Errorf("the failed file left state behind: object=%v recorded=%v", made, recorded)
			}
		})
	}
}
