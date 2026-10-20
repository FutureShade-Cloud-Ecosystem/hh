//go:build integration

package readtx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// gateURL is the migrated (and, in CI, seeded) database the integration job
// points DATABASE_URL at. The legs skip politely without it.
func gateURL(t *testing.T) string {
	t.Helper()
	u := os.Getenv("DATABASE_URL")
	if u == "" {
		t.Skip("DATABASE_URL not set")
	}
	return u
}

func ctxT(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func connect(t *testing.T, dsn string) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(ctxT(t), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func currentDatabase(t *testing.T, conn *pgx.Conn) string {
	t.Helper()
	var name string
	if err := conn.QueryRow(ctxT(t), `SELECT current_database()`).Scan(&name); err != nil {
		t.Fatalf("current_database: %v", err)
	}
	return name
}

// withUser rewrites a database URL to log in as user with password.
func withUser(t *testing.T, dsn, user, password string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	u.User = url.UserPassword(user, password)
	return u.String()
}

func withDatabase(t *testing.T, dsn, database string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	u.Path = "/" + database
	return u.String()
}

func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(b)
}

// loginPasswords gives both assist logins a random password for the test and
// takes it away again when the test ends, so the shared gate database is left
// with logins that cannot sign in. It returns the two login URLs.
func loginPasswords(t *testing.T, admin *pgx.Conn) (staffURL, proURL string) {
	t.Helper()
	db := currentDatabase(t, admin)
	roles := RolesFor(db)
	urls := map[string]string{}
	for _, login := range []string{roles.Login, roles.ProLogin} {
		pw := randomHex(t, 16)
		if _, err := admin.Exec(ctxT(t), `ALTER ROLE `+pgx.Identifier{login}.Sanitize()+` PASSWORD '`+pw+`'`); err != nil {
			t.Fatalf("set test password: %v", err)
		}
		urls[login] = withUser(t, gateURL(t), login, pw)
		login := login
		t.Cleanup(func() {
			c, err := pgx.Connect(ctxT(t), gateURL(t))
			if err != nil {
				return
			}
			defer c.Close(ctxT(t))
			_, _ = c.Exec(ctxT(t), `ALTER ROLE `+pgx.Identifier{login}.Sanitize()+` PASSWORD NULL`)
		})
	}
	return urls[roles.Login], urls[roles.ProLogin]
}

// freshMigratedDB creates an empty database on the cluster of the gate database,
// installs pgcrypto and applies migration 0418 to it, and drops it (with its
// roles) when the test ends. It returns an admin connection to it and its name.
func freshMigratedDB(t *testing.T, prefix string, exts ...string) (*pgx.Conn, string) {
	t.Helper()
	gate := gateURL(t)
	root := connect(t, gate)
	name := prefix + "_" + randomHex(t, 4)
	if _, err := root.Exec(ctxT(t), `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create database: %v", err)
	}
	roles := RolesFor(name)
	t.Cleanup(func() {
		c, err := pgx.Connect(ctxT(t), gate)
		if err != nil {
			return
		}
		defer c.Close(ctxT(t))
		_, _ = c.Exec(ctxT(t), `DROP DATABASE IF EXISTS `+pgx.Identifier{name}.Sanitize()+` WITH (FORCE)`)
		for _, r := range []string{roles.Login, roles.ProLogin, roles.Reader, roles.ProReader, roles.ViewOwner, roles.ClaimsOwner, roles.FunctionUsers} {
			_, _ = c.Exec(ctxT(t), `DROP ROLE IF EXISTS `+pgx.Identifier{r}.Sanitize())
		}
	})
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "db", "migrations", "0418_assist_roles_and_claims.sql"))
	if err != nil {
		t.Fatal(err)
	}
	conn := connect(t, withDatabase(t, gate, name))
	for _, ext := range append([]string{"pgcrypto"}, exts...) {
		if _, err := conn.Exec(ctxT(t), `CREATE EXTENSION `+ext); err != nil {
			t.Fatalf("extension %s: %v", ext, err)
		}
	}
	if _, err := conn.Exec(ctxT(t), string(body)); err != nil {
		t.Fatalf("0418 on a fresh database: %v", err)
	}
	return conn, name
}

// freshMigratedDBBefore is freshMigratedDB with extra extensions installed before
// 0418 runs, so the migration meets their functions.
func freshMigratedDBBefore(t *testing.T, prefix string, exts ...string) (*pgx.Conn, string) {
	return freshMigratedDB(t, prefix, exts...)
}
