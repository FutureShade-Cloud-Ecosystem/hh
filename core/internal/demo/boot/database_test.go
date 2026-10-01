//go:build demo

package boot

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/flows"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The database leg of the guards runs against a scratch Postgres the
// tree's gate variable names, following the gate-variable pattern: absent
// the variable the suite skips politely, present it must answer. The test
// creates one database per case, builds the frozen tables the guard
// touches with their migration shapes, and drops everything it made.

const coreDBEnvVar = "HH_CORE_TEST_DATABASE_URL"

// fixtureDDL builds the tables the demo database guard reads, at the
// shapes the frozen migrations give them (org 0213, users 0004 plus
// 0213's tombstone column and 0215's platform id, payment_config 0027,
// schema_migrations the migrate role's bookkeeping). demo_box is the
// populate's own table, outside the migration chain; the guard reads only
// its existence and its row count.
const fixtureDDL = `
CREATE EXTENSION IF NOT EXISTS citext;
CREATE TABLE schema_migrations (
	version     TEXT PRIMARY KEY,
	applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO schema_migrations (version) VALUES ('0001_extensions');
CREATE TABLE org (
	id    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	slug  CITEXT UNIQUE NOT NULL
);
INSERT INTO org (slug) VALUES ('hh-demo');
CREATE TABLE users (
	id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	email        CITEXT NOT NULL,
	display_name TEXT NOT NULL DEFAULT '',
	status       TEXT NOT NULL DEFAULT 'INVITED',
	platform_user_id TEXT NULL,
	deleted_at   TIMESTAMPTZ NULL
);
CREATE TABLE payment_config (
	id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	status      TEXT NOT NULL,
	merchant_id TEXT NOT NULL
);
CREATE TABLE demo_box (
	moment TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO demo_box DEFAULT VALUES;
`

func insertUser(t *testing.T, pool *pgxpool.Pool, email, status string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO users (email, display_name, status) VALUES ($1, $2, $3)`, email, email, status); err != nil {
		t.Fatalf("insert user %s: %v", email, err)
	}
}

func insertPayment(t *testing.T, pool *pgxpool.Pool, status, merchant string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO payment_config (status, merchant_id) VALUES ($1, $2)`, status, merchant); err != nil {
		t.Fatalf("insert payment config: %v", err)
	}
}

// caseDatabase creates one scratch database on the gate variable's
// server, applies nothing itself, and returns that database's URL with a
// drop for cleanup. The server's maintenance database (postgres) is used
// for the create and the drop.
func caseDatabase(t *testing.T, name string) string {
	t.Helper()
	base := os.Getenv(coreDBEnvVar)
	if base == "" {
		t.Skip(coreDBEnvVar + " is not set: the demo database guard's suite needs a scratch Postgres it may create databases in")
	}
	adminURL, err := swapDatabase(base, "postgres")
	if err != nil {
		t.Fatalf("%s is not a database URL: %v", coreDBEnvVar, err)
	}
	admin, err := pgx.Connect(context.Background(), adminURL)
	if err != nil {
		t.Fatalf("connect to the scratch server's maintenance database: %v", err)
	}
	dbName := "w1_g3_" + name
	if _, err := admin.Exec(context.Background(),
		fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, dbName)); err != nil {
		t.Fatalf("drop scratch database: %v", err)
	}
	if _, err := admin.Exec(context.Background(),
		fmt.Sprintf(`CREATE DATABASE %s`, dbName)); err != nil {
		t.Fatalf("create scratch database: %v", err)
	}
	t.Cleanup(func() {
		if admin2, err := pgx.Connect(context.Background(), adminURL); err == nil {
			_, _ = admin2.Exec(context.Background(),
				fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, dbName))
			_ = admin2.Close(context.Background())
		}
	})
	if err := admin.Close(context.Background()); err != nil {
		t.Fatalf("close maintenance connection: %v", err)
	}
	caseURL, err := swapDatabase(base, dbName)
	if err != nil {
		t.Fatalf("%s is not a database URL: %v", coreDBEnvVar, err)
	}
	return caseURL
}

// swapDatabase replaces the path of a postgres URL with another database
// name, keeping the query string (the sslmode and friends) intact.
func swapDatabase(baseURL, dbName string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("not an absolute database URL")
	}
	u.Path = "/" + dbName
	return u.String(), nil
}

func openPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(context.Background(), fixtureDDL); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return pool
}

func lookupWith(extra map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		if v, ok := extra[k]; ok {
			return v, true
		}
		return map[string]string{"DEMO_MODE": "true", "APP_ENV": "demo"}[k], true
	}
}

// G0: demo-check passes a fresh database: the box says demo twice, the
// database variables agree, and there is no schema_migrations table yet.
func TestDemoCheckPassesAFreshDatabase(t *testing.T) {
	dsn := caseDatabase(t, "fresh")
	if err := DemoCheck(context.Background(), lookupWith(map[string]string{
		"DATABASE_URL": dsn, "CORE_DATABASE_URL": dsn,
	})); err != nil {
		t.Fatalf("a fresh database refused: %v", err)
	}
}

// G0: demo-check refuses divergent database variables: the mismatch is
// two databases wearing one name.
func TestDemoCheckRefusesDivergentDatabaseVariables(t *testing.T) {
	dsn := caseDatabase(t, "divergent")
	other := dsn + "_other"
	err := DemoCheck(context.Background(), lookupWith(map[string]string{
		"DATABASE_URL": dsn, "CORE_DATABASE_URL": other,
	}))
	if err == nil {
		t.Fatal("divergent database variables accepted")
	}
	for _, name := range []string{"DATABASE_URL", "CORE_DATABASE_URL"} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("refusal %q does not name %s", err, name)
		}
	}
}

// G3: the guard accepts the demo world as the populate will build it: one
// demo org, the populate's demo_box row, accounts at the reserved domain
// and the reserved example domains, suspended outsiders, and the demo's
// own invented payment configuration.
func TestDemoDatabaseGuardAcceptsTheDemoWorld(t *testing.T) {
	pool := openPool(t, caseDatabase(t, "demo"))
	insertUser(t, pool, "sales@"+flows.ReservedDomain, "ACTIVE")
	insertUser(t, pool, "manager@"+flows.ReservedDomain, "ACTIVE")
	insertUser(t, pool, "someone.example", "ACTIVE")
	insertUser(t, pool, "someone.invalid", "ACTIVE")
	insertUser(t, pool, "real.person@dibbits.ca", "SUSPENDED")
	insertPayment(t, pool, "active", flows.DemoMerchantID)
	if err := CheckDemoDatabase(context.Background(), pool); err != nil {
		t.Fatalf("the demo world refused: %v", err)
	}
}

// G3: every refusal, each by the case that names it. The refusals pin
// their reason, so a refusal that passes for the wrong reason (a query
// erroring where a check should refuse) fails here too.
func TestDemoDatabaseGuardRefusals(t *testing.T) {
	cases := []struct {
		name  string
		want  string
		plant func(t *testing.T, pool *pgxpool.Pool)
	}{
		{"a second org", "org rows", func(t *testing.T, pool *pgxpool.Pool) {
			if _, err := pool.Exec(context.Background(), `INSERT INTO org (slug) VALUES ('other-dealer')`); err != nil {
				t.Fatal(err)
			}
		}},
		{"the one org is not the demo org", "hh-demo", func(t *testing.T, pool *pgxpool.Pool) {
			if _, err := pool.Exec(context.Background(), `UPDATE org SET slug = 'other-dealer'`); err != nil {
				t.Fatal(err)
			}
		}},
		{"no demo_box table", "only the demo populate writes it", func(t *testing.T, pool *pgxpool.Pool) {
			if _, err := pool.Exec(context.Background(), `DROP TABLE demo_box`); err != nil {
				t.Fatal(err)
			}
		}},
		{"demo_box emptied", "want the populate's one row", func(t *testing.T, pool *pgxpool.Pool) {
			if _, err := pool.Exec(context.Background(), `DELETE FROM demo_box`); err != nil {
				t.Fatal(err)
			}
		}},
		{"an ACTIVE user outside the reserved domains", "reserved demo domains", func(t *testing.T, pool *pgxpool.Pool) {
			insertUser(t, pool, "clerk@dibbits.ca", "ACTIVE")
		}},
		{"a lookalike host outside the reserved domains", "reserved demo domains", func(t *testing.T, pool *pgxpool.Pool) {
			insertUser(t, pool, "clerk@hh-demo.invalid.evil.com", "ACTIVE")
		}},
		{"an active payment configuration that is not the demo's", "payment configuration", func(t *testing.T, pool *pgxpool.Pool) {
			insertPayment(t, pool, "active", "REAL-MERCHANT")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pool := openPool(t, caseDatabase(t, "refuse"))
			c.plant(t, pool)
			err := CheckDemoDatabase(context.Background(), pool)
			if err == nil {
				t.Fatalf("%s: the guard accepted", c.name)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("%s: refusal %q does not pin its reason (%s), so it may be refusing for the wrong reason", c.name, err, c.want)
			}
		})
	}
}

// G0, database half: demo-check runs the guard on a non-empty database,
// so the entrypoint stops before migrate writes anything.
func TestDemoCheckRefusesANonDemoDatabase(t *testing.T) {
	dsn := caseDatabase(t, "nondemo")
	pool := openPool(t, dsn)
	if _, err := pool.Exec(context.Background(), `INSERT INTO org (slug) VALUES ('real-dealer')`); err != nil {
		t.Fatal(err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := DemoCheck(context.Background(), lookupWith(map[string]string{
		"DATABASE_URL": dsn, "CORE_DATABASE_URL": dsn,
	}))
	if err == nil {
		t.Fatal("a non-demo database passed demo-check")
	}
}
