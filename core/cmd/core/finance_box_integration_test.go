//go:build integration

// finance_box_integration_test.go, the finance box boot guard against a real
// Postgres.
//
//	Run: DATABASE_URL='postgres://postgres:postgres@127.0.0.1:57724/postgres?sslmode=disable' \
//	     HH_CORE_TEST_DATABASE_URL='...' go test -tags integration ./cmd/core/ -count=1 -run FinanceBox
//
// What only a database can prove: the guard reads box_kind through the
// settings seam and refuses a non empty database the box did not seed, and it
// accepts an empty database (the entrypoint migrates and seeds before serve
// on first boot) and a finance seeded one. Each database leg builds its own
// database from zero, per this package's convention.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/config"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

func financeStagingConfig(t *testing.T) config.Config {
	t.Helper()
	t.Setenv("APP_ENV", "staging")
	return config.Load()
}

func TestIntegration_FinanceBoxRefusesNonStagingEnv(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	cfg := config.Load()
	err := financeBoxBootGuard(financeBoxGetenv(map[string]string{"HH_BOX_KIND": "finance"}), cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "APP_ENV") || !strings.Contains(err.Error(), "staging") {
		t.Fatalf("the guard must refuse a finance box outside staging, naming APP_ENV, got %v", err)
	}
}

func TestIntegration_FinanceBoxRefusesLiveCardRail(t *testing.T) {
	cfg := financeStagingConfig(t)
	err := financeBoxBootGuard(financeBoxGetenv(map[string]string{
		"HH_BOX_KIND":        "finance",
		"HOS_CARD_PROCESSOR": "run",
		"HOS_CARD_BASE_URL":  "https://run-pay.example.test",
	}), cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "HOS_CARD_PROCESSOR") {
		t.Fatalf("the guard must refuse a finance box on a live card rail, naming HOS_CARD_PROCESSOR, got %v", err)
	}
}

func TestIntegration_FinanceBoxRefusesLivePortalRail(t *testing.T) {
	cfg := financeStagingConfig(t)
	err := financeBoxBootGuard(financeBoxGetenv(map[string]string{
		"HH_BOX_KIND":                "finance",
		"HOS_PAYMENT_GATEWAY":        "run",
		"HOS_PAYMENT_API_KEY":        "placeholder-key",
		"HOS_PAYMENT_MERCHANT_ID":    "placeholder-mid",
		"HOS_PAYMENT_BASE_URL":       "https://run-pay.example.test",
		"HOS_PAYMENT_WEBHOOK_SECRET": "placeholder-secret",
	}), cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "HOS_PAYMENT_GATEWAY") {
		t.Fatalf("the guard must refuse a finance box on a live portal rail, naming HOS_PAYMENT_GATEWAY, got %v", err)
	}
}

func TestIntegration_FinanceBoxRefusesNonEmptyDatabaseWithoutBoxKind(t *testing.T) {
	pool, _ := freshMigratedBoxDB(t, "finboxnokind")
	cfg := financeStagingConfig(t)

	err := financeBoxBootGuard(financeBoxGetenv(map[string]string{"HH_BOX_KIND": "finance"}), cfg, pool)
	if err == nil || !strings.Contains(err.Error(), settings.KeyBoxKind) {
		t.Fatalf("a migrated database the box did not seed must stop the boot naming box_kind, got %v", err)
	}
}

func TestIntegration_FinanceBoxRefusesAnotherBoxKind(t *testing.T) {
	ctx := context.Background()
	pool, _ := freshMigratedBoxDB(t, "finboxdemo")
	writeBoxKindForTest(t, ctx, pool, "demo")
	cfg := financeStagingConfig(t)

	err := financeBoxBootGuard(financeBoxGetenv(map[string]string{"HH_BOX_KIND": "finance"}), cfg, pool)
	if err == nil || !strings.Contains(err.Error(), settings.KeyBoxKind) {
		t.Fatalf("a database holding another box_kind must stop the boot naming box_kind, got %v", err)
	}
}

func TestIntegration_FinanceBoxPassesOnEmptyDatabase(t *testing.T) {
	pool := freshEmptyDB(t, "finboxempty")
	cfg := financeStagingConfig(t)

	if err := financeBoxBootGuard(financeBoxGetenv(map[string]string{"HH_BOX_KIND": "finance"}), cfg, pool); err != nil {
		t.Fatalf("an empty database is first boot territory and must pass the guard, got %v", err)
	}
}

func TestIntegration_FinanceBoxPassesOnFinanceSeededDatabase(t *testing.T) {
	ctx := context.Background()
	pool, _ := freshMigratedBoxDB(t, "finboxok")
	writeBoxKindForTest(t, ctx, pool, "finance")
	cfg := financeStagingConfig(t)

	if err := financeBoxBootGuard(financeBoxGetenv(map[string]string{"HH_BOX_KIND": "finance"}), cfg, pool); err != nil {
		t.Fatalf("a finance seeded database must pass the guard, got %v", err)
	}
}

func TestIntegration_FinanceBoxRefusesThroughTheWiring(t *testing.T) {
	env := newCompEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("PAYMENT_VAULT_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("PORTAL_BASE_URL", "https://finance-box.example.test")
	t.Setenv("ERP_BASE_URL", "https://finance-box.example.test")
	env.set("HH_BOX_KIND", "finance")

	_, err := registerRoutes(discardRegistrar{}, env.getenv)
	if err == nil || !strings.Contains(err.Error(), "APP_ENV") {
		t.Fatalf("the production wiring must stop a finance box outside staging, naming APP_ENV, got %v", err)
	}
}

func TestIntegration_FinanceBoxBootsThroughTheWiring(t *testing.T) {
	ctx := context.Background()
	env := newCompEnv(t)
	t.Setenv("APP_ENV", "staging")
	t.Setenv("PAYMENT_VAULT_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("PORTAL_BASE_URL", "https://finance-box.example.test")
	t.Setenv("ERP_BASE_URL", "https://finance-box.example.test")
	env.set("HH_BOX_KIND", "finance")

	fixture := env.fixturePool()
	defer fixture.Close()
	restore := boxKindSnapshotForTest(t, ctx, fixture)
	writeBoxKindForTest(t, ctx, fixture, "finance")
	defer restore()

	graph, err := registerRoutes(discardRegistrar{}, env.getenv)
	if err != nil {
		t.Fatalf("the production wiring must boot a finance box on its own seeded database, got %v", err)
	}
	graph.pool.Close()
}

func TestIntegration_DemoseedRefusesTheFinanceBox(t *testing.T) {
	t.Setenv("APP_ENV", "staging")
	f, err := os.CreateTemp(t.TempDir(), "demoseed-out")
	if err != nil {
		t.Fatalf("temp output: %v", err)
	}
	defer f.Close()

	// The environment leg: the box statement alone refuses the role, before
	// it resolves a database or anything else. A finance box is never
	// demoseed territory, whatever database it could name.
	unsetForTest(t, "DATABASE_URL")
	unsetForTest(t, "CORE_DATABASE_URL")
	err = demoseedRole([]string{"-plan"}, f, financeBoxGetenv(map[string]string{"HH_BOX_KIND": "finance"}))
	if err == nil || !strings.Contains(err.Error(), envBoxKind) {
		t.Fatalf("demoseed must refuse a finance box on the box statement alone, naming HH_BOX_KIND, got %v", err)
	}

	// The database leg: a database whose box_kind is finance refuses the
	// role through the settings seam, before the plan reads a branch or
	// writes a row.
	unsetForTest(t, "HH_BOX_KIND")
	pool, dsn := freshMigratedBoxDB(t, "demoseedfin")
	writeBoxKindForTest(t, context.Background(), pool, "finance")
	err = demoseedRole([]string{"-plan"}, f, financeBoxGetenv(map[string]string{"CORE_DATABASE_URL": dsn}))
	if err == nil || !strings.Contains(err.Error(), settings.KeyBoxKind) {
		t.Fatalf("demoseed must refuse a finance marked database, naming box_kind, got %v", err)
	}
}

// TestIntegration_FinanceBoxEmptyBranchNeedsCREATEROLE: migration 0418
// creates the assist roles, so the box's database role must be able to
// create roles. A role without that right left a fresh database half
// migrated: every migration before 0418 committed and 0418 rolled back
// whole, so the box wrote a mark, a bookkeeping table and hundreds of rows
// into a database it could never finish. The empty branch refuses before it
// writes anything, so the database is left exactly as it was found. The
// check lives in the empty branch alone: a database the box has already
// migrated holds the assist roles, so the box never creates them again, and
// a finance marked database still passes the box check with a role without
// CREATEROLE.
func TestIntegration_FinanceBoxEmptyBranchNeedsCREATEROLE(t *testing.T) {
	ctx := context.Background()

	t.Run("an empty database refuses the boot and is left empty", func(t *testing.T) {
		pool := freshEmptyDB(t, "finboxnocreaterole")
		role, password := createLoginRoleWithoutCREATEROLE(t, ctx, pool)
		asRole := connectAsRole(t, ctx, pool, role, password)
		cfg := financeStagingConfig(t)

		err := financeBoxBootGuard(financeBoxGetenv(map[string]string{"HH_BOX_KIND": "finance"}), cfg, asRole)
		if err == nil || !strings.Contains(err.Error(), "the box's database role needs CREATEROLE: migration 0418 creates the assist roles") {
			t.Fatalf("an empty database must refuse the boot naming CREATEROLE, got %v", err)
		}
		empty, err := financeBoxDatabaseEmpty(ctx, asRole)
		if err != nil {
			t.Fatalf("reading the emptiness: %v", err)
		}
		if !empty {
			t.Fatal("a refused database must still read empty")
		}
		own, err := FinanceBootstrapMarkPresent(ctx, asRole)
		if err != nil {
			t.Fatalf("reading the bootstrap mark: %v", err)
		}
		if own {
			t.Fatal("a refused boot must not write the bootstrap ownership mark")
		}
		if _, present, err := MigrationsAppliedCount(ctx, asRole); err != nil || present {
			t.Fatalf("a refused boot must not write a migration row (present %t, err %v)", present, err)
		}
	})

	t.Run("a finance marked database passes the box check", func(t *testing.T) {
		pool, _ := freshMigratedBoxDB(t, "finboxnocreaterolefin")
		writeBoxKindForTest(t, ctx, pool, "finance")
		role, password := createLoginRoleWithoutCREATEROLE(t, ctx, pool)
		// The box's role reads the settings table and the bookkeeping, and it
		// holds no grant of its own on either. The settings table carries a
		// row policy (migration 0427 closes it to everybody but the zone
		// reader), so a role that does not own it sees no rows at all: the
		// box's own role owns the tables it migrated, and the test role is
		// made the owner of the two tables the box check reads. The settings
		// key is a citext, so the read needs the compare function too, which
		// migration 0418 hands back to the migrating role through the
		// function users role.
		if _, err := pool.Exec(ctx, fmt.Sprintf(
			`GRANT CREATE ON SCHEMA public TO %[1]s;
			 ALTER TABLE schema_migrations OWNER TO %[1]s;
			 ALTER TABLE system_settings OWNER TO %[1]s;
			 GRANT SELECT ON schema_migrations, system_settings TO %[1]s;
			 GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO %[1]s`,
			role)); err != nil {
			t.Fatalf("granting the role its reads: %v", err)
		}
		asRole := connectAsRole(t, ctx, pool, role, password)
		cfg := financeStagingConfig(t)

		if err := financeBoxBootGuard(financeBoxGetenv(map[string]string{"HH_BOX_KIND": "finance"}), cfg, asRole); err != nil {
			t.Fatalf("a finance marked database must pass the box check with a role without CREATEROLE, got %v", err)
		}
	})
}

// -- helpers ---------------------------------------------------------------------------------------

// createLoginRoleWithoutCREATEROLE creates the role the box's database role
// is when it may not create roles: a login role with no CREATEROLE and no
// superuser. The password is generated here, never printed, never written to
// a file and never committed: it is a throwaway for a role that lives only
// for the test that made it.
func createLoginRoleWithoutCREATEROLE(t *testing.T, ctx context.Context, admin *pgxpool.Pool) (string, string) {
	t.Helper()
	name := "finbox_nocreaterole_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	password := throwawayPassword(t)
	if _, err := admin.Exec(ctx, fmt.Sprintf(
		`CREATE ROLE %[1]s LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD '%[2]s'`,
		name, password)); err != nil {
		t.Fatalf("creating the role without CREATEROLE: %v", err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(
		`GRANT CONNECT ON DATABASE %[1]s TO %[2]s`,
		admin.Config().ConnConfig.Database, name)); err != nil {
		t.Fatalf("granting the role the connection: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), fmt.Sprintf(`DROP OWNED BY %[1]s; DROP ROLE IF EXISTS %[1]s`, name))
	})
	return name, password
}

// connectAsRole opens a pool as another role on the same database.
func connectAsRole(t *testing.T, ctx context.Context, pool *pgxpool.Pool, user, password string) *pgxpool.Pool {
	t.Helper()
	cfg := pool.Config().Copy()
	cfg.ConnConfig.User = user
	cfg.ConnConfig.Password = password
	as, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connecting as %s: %v", user, err)
	}
	t.Cleanup(as.Close)
	return as
}

// throwawayPassword is a random hex string: a password for a role that
// exists only inside one test, generated so it is never written anywhere.
func throwawayPassword(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("generating the throwaway password: %v", err)
	}
	return hex.EncodeToString(buf)
}

// freshMigratedBoxDB builds a database the way the migrate role does: every
// migration file applied in order, each recorded in schema_migrations (the
// demoseed suite's helper applies the files without the bookkeeping, which
// would read as an empty database to the guard).
func freshMigratedBoxDB(t *testing.T, suffix string) (*pgxpool.Pool, string) {
	t.Helper()
	base, ok := os.LookupEnv("DATABASE_URL")
	if !ok {
		t.Skip("DATABASE_URL is unset, the finance box guard tests need a Postgres to build a database on")
	}
	name := "finbox_it_" + suffix
	adminDSN, testDSN := swapDatabase(base, "postgres"), swapDatabase(base, name)

	ctx := context.Background()
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("connecting to the admin database: %v", err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
		t.Fatalf("dropping %s: %v", name, err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}

	pool, err := pgxpool.New(ctx, testDSN)
	if err != nil {
		t.Fatalf("connecting to %s: %v", name, err)
	}
	t.Cleanup(pool.Close)

	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		t.Fatalf("bookkeeping table: %v", err)
	}
	files, err := filepath.Glob(filepath.Join("..", "..", "db", "migrations", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations found (err=%v)", err)
	}
	sort.Strings(files)
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin %s: %v", f, err)
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("applying %s: %v", f, err)
		}
		version := strings.TrimSuffix(filepath.Base(f), ".sql")
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, version); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("recording %s: %v", f, err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit %s: %v", f, err)
		}
	}
	return pool, testDSN
}

// freshEmptyDB creates a database with no migrations applied at all: the
// guard's empty leg territory.
func freshEmptyDB(t *testing.T, suffix string) *pgxpool.Pool {
	t.Helper()
	base, ok := lookupEnv("DATABASE_URL")
	if !ok {
		t.Skip("DATABASE_URL is unset, the finance box guard tests need a Postgres to build a database on")
	}
	name := "finbox_it_" + suffix
	adminDSN, testDSN := swapDatabase(base, "postgres"), swapDatabase(base, name)

	ctx := context.Background()
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("connecting to the admin database: %v", err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
		t.Fatalf("dropping %s: %v", name, err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}

	pool, err := pgxpool.New(ctx, testDSN)
	if err != nil {
		t.Fatalf("connecting to %s: %v", name, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func writeBoxKindForTest(t *testing.T, ctx context.Context, pool *pgxpool.Pool, kind string) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := settings.WriteBoxKindTx(ctx, tx, kind); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("WriteBoxKindTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// boxKindSnapshotForTest restores whatever box_kind row existed (or its
// absence) when the test ends.
func boxKindSnapshotForTest(t *testing.T, ctx context.Context, pool *pgxpool.Pool) func() {
	t.Helper()
	var before []byte
	hadRow := pool.QueryRow(ctx, `SELECT value FROM system_settings WHERE key = $1`, settings.KeyBoxKind).Scan(&before) == nil
	return func() {
		if hadRow {
			_, _ = pool.Exec(ctx, `UPDATE system_settings SET value = $2::jsonb WHERE key = $1`, settings.KeyBoxKind, string(before))
		} else {
			_, _ = pool.Exec(ctx, `DELETE FROM system_settings WHERE key = $1`, settings.KeyBoxKind)
		}
	}
}

func lookupEnv(name string) (string, bool) {
	return os.LookupEnv(name)
}
