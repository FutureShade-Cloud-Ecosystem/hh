//go:build integration

// finance_seed_integration_test.go, the finance seed against a real
// Postgres: every guard leg, and the seeded world itself (the seed data,
// box_kind recorded through the settings module, and the seed users holding
// no usable password).
//
//	Run: DATABASE_URL='postgres://postgres:postgres@127.0.0.1:57724/postgres?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -count=1 -run FinanceSeed
package main

import (
	"context"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// financeSeedEnv is the box statement finance-seed reads: the finance box,
// the console mode, and the one database URL name the one-shot resolver
// decides on. Every other name is absent on purpose.
func financeSeedEnv(dsn string) map[string]string {
	return map[string]string{
		"HH_BOX_KIND":       "finance",
		"CORE_SIGNIN_MODE":  "console",
		"CORE_DATABASE_URL": dsn,
	}
}

func TestIntegration_FinanceSeedRefusesWithoutTheFinanceBoxKind(t *testing.T) {
	t.Setenv("APP_ENV", "staging")
	unsetForTest(t, "DATABASE_URL")

	env := financeSeedEnv("")
	delete(env, "HH_BOX_KIND")
	err := financeSeedRole(mapGetenv(env))
	if err == nil || !strings.Contains(err.Error(), "HH_BOX_KIND") {
		t.Fatalf("the seed must refuse without the finance box statement, naming HH_BOX_KIND, got %v", err)
	}
}

func TestIntegration_FinanceSeedRefusesNonStagingEnv(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	unsetForTest(t, "DATABASE_URL")

	err := financeSeedRole(mapGetenv(financeSeedEnv("")))
	if err == nil || !strings.Contains(err.Error(), "APP_ENV") {
		t.Fatalf("the seed must refuse outside staging, naming APP_ENV, got %v", err)
	}
}

func TestIntegration_FinanceSeedRefusesNonConsoleSignin(t *testing.T) {
	t.Setenv("APP_ENV", "staging")
	unsetForTest(t, "DATABASE_URL")

	env := financeSeedEnv("")
	delete(env, "CORE_SIGNIN_MODE")
	err := financeSeedRole(mapGetenv(env))
	if err == nil || !strings.Contains(err.Error(), "CORE_SIGNIN_MODE") {
		t.Fatalf("the seed must refuse without the console sign in mode, naming CORE_SIGNIN_MODE, got %v", err)
	}
}

func TestIntegration_FinanceSeedRefusesWhenBoxKindIsAlreadySet(t *testing.T) {
	ctx := context.Background()
	t.Setenv("APP_ENV", "staging")
	pool, dsn := freshMigratedBoxDB(t, "finseedkind")
	writeBoxKindForTest(t, ctx, pool, "finance")

	err := financeSeedRole(mapGetenv(financeSeedEnv(dsn)))
	if err == nil || !strings.Contains(err.Error(), settings.KeyBoxKind) {
		t.Fatalf("the seed must refuse a database that already holds a box kind, naming box_kind, got %v", err)
	}
}

func TestIntegration_FinanceSeedAppliesTheWorldAndRefusesASecondRun(t *testing.T) {
	ctx := context.Background()
	t.Setenv("APP_ENV", "staging")
	pool, dsn := freshMigratedBoxDB(t, "finseedworld")

	if err := financeSeedRole(mapGetenv(financeSeedEnv(dsn))); err != nil {
		t.Fatalf("finance-seed on a fresh migrated database: %v", err)
	}

	// The seeded world is there.
	var products, customers int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM products WHERE sku LIKE 'DBS-%'`).Scan(&products); err != nil {
		t.Fatalf("products: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM customers WHERE account_no LIKE 'CUST-%'`).Scan(&customers); err != nil {
		t.Fatalf("customers: %v", err)
	}
	if products == 0 || customers == 0 {
		t.Fatalf("the seeded world must be present, got %d products and %d customers", products, customers)
	}

	// box_kind is recorded through the settings module.
	kind, err := settings.NewDealerConfigReader(pool).BoxKind(ctx)
	if err != nil {
		t.Fatalf("BoxKind: %v", err)
	}
	if kind != "finance" {
		t.Fatalf("the seed must record box_kind=finance through settings, got %q", kind)
	}

	// The seed's users hold no usable password: every seed staff id reads
	// NULL (the INVITED spelling) and the seed's portal account carries the
	// seeded-no-password marker (the NOT NULL table's spelling), and the
	// demo bcrypt appears nowhere. The id list is the dibbits seed's fixed
	// staff ids (dibbits_seed.sql), the rows DisableSeedCredentials must
	// clear.
	var holding int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM users
		 WHERE id = ANY($1::uuid[]) AND password_hash IS NOT NULL`, financeSeedStaffIDs).Scan(&holding); err != nil {
		t.Fatalf("staff password probe: %v", err)
	}
	if holding != 0 {
		t.Fatalf("%d seed staff users still hold a usable password", holding)
	}
	var portalHash string
	if err := pool.QueryRow(ctx, `
		SELECT password_hash FROM portal_users WHERE id = 'e7000000-0000-4000-9000-000000000001'::uuid`).Scan(&portalHash); err != nil {
		t.Fatalf("portal password probe: %v", err)
	}
	if strings.HasPrefix(portalHash, "$2") {
		t.Fatal("the seed's portal account still holds a usable bcrypt password")
	}
	// The seed's own demo bcrypt must survive nowhere (staff or portal).
	// Other planted demo logins the migrations carry are a separate finding
	// for the lead, outside this item's scope.
	var hashes int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM users WHERE password_hash = $1)
		     + (SELECT count(*) FROM portal_users WHERE password_hash = $1)`,
		"$2a$10$ja7jD99TS1QsOtU.7dVDIu.HwgOK2pBERc60C4IyjMEi49IvR/dGO").Scan(&hashes); err != nil {
		t.Fatalf("seed hash sweep: %v", err)
	}
	if hashes != 0 {
		t.Fatalf("the seed's demo bcrypt must survive the seed nowhere, found %d rows still holding it", hashes)
	}

	// The second run refuses: the seed is applied.
	err = financeSeedRole(mapGetenv(financeSeedEnv(dsn)))
	if err == nil || !strings.Contains(err.Error(), "seed") {
		t.Fatalf("the second run must refuse, naming the applied seed, got %v", err)
	}
}

func TestIntegration_FinanceBoxCheckRefusesADatabaseTheBoxDidNotSeed(t *testing.T) {
	t.Setenv("APP_ENV", "staging")
	_, dsn := freshMigratedBoxDB(t, "finboxcheck")

	err := financeBoxCheckRole(mapGetenv(financeSeedEnv(dsn)))
	if err == nil || !strings.Contains(err.Error(), settings.KeyBoxKind) {
		t.Fatalf("the pre-migrate check must refuse a database without the finance mark, naming box_kind, got %v", err)
	}
}

func TestIntegration_FinanceBoxCheckPassesOnTheSeededWorld(t *testing.T) {
	t.Setenv("APP_ENV", "staging")
	_, dsn := freshMigratedBoxDB(t, "finboxcheckok")
	if err := financeSeedRole(mapGetenv(financeSeedEnv(dsn))); err != nil {
		t.Fatalf("finance-seed: %v", err)
	}

	if err := financeBoxCheckRole(mapGetenv(financeSeedEnv(dsn))); err != nil {
		t.Fatalf("the pre-migrate check must pass the box's own seeded world, got %v", err)
	}
}

// mapGetenv is the environment stub over a plain map.
func mapGetenv(values map[string]string) func(string) string {
	return func(k string) string { return values[k] }
}

// financeSeedStaffIDs is the dibbits seed's fixed staff id list
// (cmd/core/dibbits_seed.sql), the rows the identity seam must leave with no
// usable password. It is written out here, rather than imported, so the test
// watches the seam from outside.
var financeSeedStaffIDs = []string{
	"d7000000-0000-4000-9000-000000000001",
	"d7000000-0000-4000-9000-000000000002",
	"d7000000-0000-4000-9000-000000000003",
	"d7000000-0000-4000-9000-000000000005",
	"d7000000-0000-4000-9000-000000000006",
	"d7000000-0000-4000-9000-000000000007",
	"d7000000-0000-4000-9000-000000000010",
}
