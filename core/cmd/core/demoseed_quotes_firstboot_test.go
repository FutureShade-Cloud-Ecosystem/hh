//go:build demo

package main

// demoseed_quotes_firstboot_test.go: what ONE first boot of the demo image leaves on an empty
// database. The desk report said the image seeds the quote world only on its second boot, because
// the quotes' owners are the two demo role logins and the populate plants those after the seed
// has run; the staging deploy of the same round was read seeding all four on its first start, so
// the truth had to come from a run.
//
// The steps are the entrypoint's own, in the entrypoint's order (deploy/demo/entrypoint.sh, the
// empty database path), through the same Go entry points the entrypoint's commands call:
//
//	boot.DemoCheck          for core demo-check (the G0 guard, before anything writes)
//	the entrypoint's two    for the image's build marker (outside the migration chain)
//	  marker statements
//	qwMigrateFrozenSet      for core migrate (the same files and order the migrate role applies)
//	demoseedRole -apply     for core demoseed -apply
//	demo.PopulateMain       for core demo-populate
//	demoseedRole -apply     for the apply the entrypoint runs again after the populate (the quote
//	                        world's owners exist only once the populate has run, and the seed is
//	                        idempotent, so the boot applies the seed once more)
//
// Then it asks the database for DEMO-TRT-Q1 to Q4. A second boot, the entrypoint's built path
// (demo-check, migrate, apply, populate, apply), must change nothing at all: the seed stays
// idempotent and the populate's writes are guarded.
//
// The gate is the demo world proof's own variable (tillDemoDBEnvVar); the test builds and drops
// its own database beside it, as the world proofs do.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/boot"
)

func TestDemoWorld_FirstBootSeedsQuotes(t *testing.T) {
	dsn := os.Getenv(tillDemoDBEnvVar)
	if dsn == "" {
		t.Skip(tillDemoDBEnvVar + " is not set: the demo world proof needs its own throwaway local Postgres")
	}
	requireLocalDSN(t, dsn)
	ctx := context.Background()

	rawKey := make([]byte, 32)
	if _, err := rand.Read(rawKey); err != nil {
		t.Fatalf("generate vault key: %v", err)
	}
	vaultKeyHex := hex.EncodeToString(rawKey)

	// The platform hands the boot a fresh, empty database.
	pool, testDSN := qwCreateDB(t, ctx, dsn, "demoworld_firstboot")

	// The entrypoint's own exports: the box says demo twice, both database variables are one
	// value, the vault key seals the payment configuration the populate writes.
	t.Setenv("DEMO_MODE", "true")
	t.Setenv("APP_ENV", "demo")
	t.Setenv("DATABASE_URL", testDSN)
	t.Setenv("CORE_DATABASE_URL", testDSN)
	t.Setenv("PAYMENT_VAULT_KEY", vaultKeyHex)

	// --- boot one, the entrypoint's empty database path, in the entrypoint's order -------------
	// demo-check before anything writes.
	if err := boot.DemoCheck(ctx, os.LookupEnv); err != nil {
		t.Fatalf("demo-check refused the empty database: %v", err)
	}
	// The image's build marker, the entrypoint's own two statements.
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS demo_build_marker
		(started timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatalf("the build marker table: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO demo_build_marker SELECT now() WHERE NOT EXISTS (SELECT 1 FROM demo_build_marker)`); err != nil {
		t.Fatalf("the build marker row: %v", err)
	}
	// core migrate.
	qwMigrateFrozenSet(t, ctx, pool)
	// core demoseed -apply.
	if _, created := qwApply(t); created == 0 {
		t.Fatal("the boot's apply created nothing on a fresh database")
	}
	// core demo-populate.
	var populateOut bytes.Buffer
	if err := demo.PopulateMain([]string{"-now", qwClock.Format(time.RFC3339)}, &populateOut, os.Getenv); err != nil {
		t.Fatalf("demo-populate: %v", err)
	}
	// core demoseed -apply, again: the quote world's owners are the populate's two role logins,
	// so the quotes can only be added once they exist, and the seed is idempotent.
	_, created := qwApply(t)
	const wantQuoteRows = 4 + 9 + 3 + 2 + 2 + 2 + 4 + 1 // the quote world's census, the world proof's own
	if created != wantQuoteRows {
		t.Fatalf("the apply after the populate created %d rows, want exactly the quote world's %d", created, wantQuoteRows)
	}

	// --- the boot is over: the four demo quotes must be on the database ------------------------
	for key, no := range map[string]string{
		"Q1": "DEMO-TRT-Q1", "Q2": "DEMO-TRT-Q2", "Q3": "DEMO-TRT-Q3", "Q4": "DEMO-TRT-Q4",
	} {
		if n := qwCount(t, ctx, pool,
			`SELECT count(*) FROM quotes WHERE quote_no = $1 AND metadata->>'demo_seed' = $2`, no, demoMarker); n != 1 {
			t.Errorf("one first boot left %d %s (%s) rows on the database, want the quote", n, key, no)
		}
	}
	if n := qwCount(t, ctx, pool, `
		SELECT count(*) FROM quotes q JOIN users u ON u.id = q.created_by
		 WHERE q.quote_no LIKE 'DEMO-TRT-%'`); n != 4 {
		t.Errorf("%d demo quotes resolve an owner, want 4: the world's quotes are owned by the two demo logins", n)
	}

	// --- boot two, the entrypoint's built path: nothing may move -------------------------------
	// Beside the world proof's two lists: the quote audit rows the world writes, the dealer rows
	// the populate's guarded renames converge, and the migrate role's bookkeeping table.
	allTables := append(append(append([]string{}, qwOtherWorldTables...), qwQuoteTables...),
		"pricing_audit_log", "permission_approval_requests", "demo_build_marker", "audit_log",
		"locations", "schema_migrations")
	snap := qwFingerprint(t, ctx, pool, allTables)
	if err := boot.DemoCheck(ctx, os.LookupEnv); err != nil {
		t.Fatalf("demo-check refused the built database: %v", err)
	}
	qwMigrateFrozenSet(t, ctx, pool)
	if _, created := qwApply(t); created != 0 {
		t.Errorf("the second boot's apply before the populate created %d rows", created)
	}
	var populateOut2 bytes.Buffer
	if err := demo.PopulateMain([]string{"-now", qwClock.Format(time.RFC3339)}, &populateOut2, os.Getenv); err != nil {
		t.Fatalf("demo-populate on the second boot: %v", err)
	}
	if _, created := qwApply(t); created != 0 {
		t.Errorf("the second boot's apply after the populate created %d rows", created)
	}
	for table, sum := range qwFingerprint(t, ctx, pool, allTables) {
		if snap[table] != sum {
			t.Errorf("%s changed across the second boot, a boot must change nothing", table)
		}
	}
}
