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
