//go:build integration

// finance_box_guard_direction_integration_test.go, the finance box guard in
// both directions, against a real Postgres.
//
// What only a database can prove:
//
//   - the guard cannot be reversed: a finance marked database refuses the
//     boot unless the environment states the finance box (item 1);
//   - empty means no bookkeeping AND no tables: a database that holds
//     tables this tree never migrated is not empty, so the guard refuses it
//     (item 3);
//
// Each database leg builds its own database from zero, per this package's
// convention.
package main

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// TestIntegration_FinanceBoxGuardCannotBeReversed: the database is the
// finance box's own world and the environment does not claim it, so the
// boot is refused. The statement can be lost (a platform entrypoint
// override, a hand run of the binary); it can never be reversed.
func TestIntegration_FinanceBoxGuardCannotBeReversed(t *testing.T) {
	ctx := context.Background()
	pool, _ := freshMigratedBoxDB(t, "finboxreverse")
	writeBoxKindForTest(t, ctx, pool, "finance")
	cfg := financeStagingConfig(t)

	err := financeBoxBootGuard(financeBoxGetenv(map[string]string{}), cfg, pool)
	if err == nil || !strings.Contains(err.Error(), envBoxKind) {
		t.Fatalf("a finance marked database must stop the boot when the environment does not state %s, got %v", envBoxKind, err)
	}
}

// TestIntegration_FinanceBoxGuardCannotBeReversedThroughTheWiring: the same
// refusal through the production wiring (registerRoutes), which is how serve
// and worker build their graph.
func TestIntegration_FinanceBoxGuardCannotBeReversedThroughTheWiring(t *testing.T) {
	ctx := context.Background()
	env := newCompEnv(t)
	t.Setenv("APP_ENV", "staging")
	t.Setenv("PAYMENT_VAULT_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("PORTAL_BASE_URL", "https://finance-box.example.test")
	t.Setenv("ERP_BASE_URL", "https://finance-box.example.test")

	fixture := env.fixturePool()
	defer fixture.Close()
	restore := boxKindSnapshotForTest(t, ctx, fixture)
	writeBoxKindForTest(t, ctx, fixture, "finance")
	defer restore()

	graph, err := registerRoutes(discardRegistrar{}, env.getenv)
	if err == nil || !strings.Contains(err.Error(), envBoxKind) {
		t.Fatalf("the production wiring must stop a finance world that does not claim itself, naming %s, got %v", envBoxKind, err)
	}
	if graph != nil {
		graph.pool.Close()
	}
}

// TestIntegration_FinanceBoxEmptyMeansNoBookkeepingAndNoTables: a database
// that holds tables but no migration bookkeeping is not empty. The guard
// used to read it as empty (the bookkeeping table alone decided), and an
// empty database passes the guard, so the box would have booted against a
// foreign schema.
func TestIntegration_FinanceBoxEmptyMeansNoBookkeepingAndNoTables(t *testing.T) {
	pool, _ := freshUnbookkeptDB(t, "finboxunbookkept")
	cfg := financeStagingConfig(t)

	err := financeBoxBootGuard(financeBoxGetenv(map[string]string{"HH_BOX_KIND": "finance"}), cfg, pool)
	if err == nil || !strings.Contains(err.Error(), settings.KeyBoxKind) {
		t.Fatalf("a database with tables and no bookkeeping must stop the boot naming %s, got %v", settings.KeyBoxKind, err)
	}
}

// freshBookkeepingOnlyDB builds the database an interrupted first migrate
// leaves behind: the migrate runner creates the bookkeeping table before it
// applies anything, so a crash there leaves the bookkeeping table and no
// other table at all, and no version recorded.
func freshBookkeepingOnlyDB(t *testing.T, suffix string) (*pgxpool.Pool, string) {
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
	return pool, testDSN
}

// freshUnbookkeptDB builds a database that holds this tree's tables but no
// migration bookkeeping: the bookkeeping table is never created and no
// version is recorded. It is the shape of a database the box did not
// migrate, and it is not empty.
func freshUnbookkeptDB(t *testing.T, suffix string) (*pgxpool.Pool, string) {
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
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("applying %s: %v", f, err)
		}
	}
	return pool, testDSN
}
