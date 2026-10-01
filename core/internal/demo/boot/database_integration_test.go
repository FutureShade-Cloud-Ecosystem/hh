//go:build demo && integration

package boot

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/flows"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The migrated-schema leg of the demo database guard. The hand-built
// fixture tables in database_test.go pin the guard's behaviour, but not
// the schema it runs against: a later migration that renames a column the
// guard reads would leave that suite green while the demo image refuses
// to boot. This leg applies the frozen migration chain to a fresh scratch
// database, exactly as the migrate role does (whole files, in glob
// order), builds the demo world by hand on the real schema, and requires
// the guard to accept it; then it plants one violation and requires the
// refusal to name it.
//
// It runs under -tags "demo,integration" with the suite's gate variable
// naming a Postgres server it may create databases on (the same variable
// database_test.go uses), and skips politely without the variable. The
// migrations are read relative to this package: core/db/migrations.

// TestDemoDatabaseGuardOnTheRealSchema is the renamed-column proof.
func TestDemoDatabaseGuardOnTheRealSchema(t *testing.T) {
	base := os.Getenv(coreDBEnvVar)
	if base == "" {
		t.Skip(coreDBEnvVar + " is not set: the migrated-schema leg needs a Postgres server it may create databases in")
	}
	pool := openPlainPool(t, caseDatabase(t, "migrated"))
	applyMigrationChain(t, pool)

	// The populate's org first, then its demo_box table with the marker:
	// the guard checks in table order (org, demo_box, users, payment), and
	// the point of the next step is the migration-planted users.
	mustExecBoot(t, pool, `INSERT INTO org (name, slug) VALUES ($1, $2)`, "Hardscape House Demo", flows.OrgSlug)
	mustExecBoot(t, pool,
		`CREATE TABLE demo_box (moment TIMESTAMPTZ NOT NULL DEFAULT now(), marker TEXT NOT NULL)`)
	mustExecBoot(t, pool,
		`INSERT INTO demo_box (marker) VALUES ($1)`, flows.PopulateMarker)

	// The migration set plants its own ACTIVE users on real domains (the
	// platform owner and the demo teammate), the rows the populate makes
	// inactive. The guard must refuse this world and say so.
	refused := CheckDemoDatabase(context.Background(), pool)
	if refused == nil || !strings.Contains(refused.Error(), "ACTIVE user(s) sit outside the reserved demo domains") {
		t.Fatalf("the migration-planted real-domain users were not refused: %v", refused)
	}

	// The populate's deactivation, by hand: the real-domain rows are made
	// inactive, and then the rest of the demo world is built on the real
	// schema: the flow account at the reserved domain and the populate's
	// invented payment configuration with no host.
	mustExecBoot(t, pool,
		`UPDATE users SET status = 'DEACTIVATED'
		  WHERE status = 'ACTIVE'
		    AND NOT (lower(email::text) LIKE '%@' || $1
		             OR lower(email::text) LIKE '%.' || $1
		             OR right(lower(email::text), 8) = '.example'
		             OR right(lower(email::text), 8) = '.invalid')`,
		flows.ReservedDomain)
	insertUser(t, pool, "sales@"+flows.ReservedDomain, "ACTIVE")
	mustExecBoot(t, pool,
		`INSERT INTO payment_config (merchant_id, credentials_ciphertext, status, base_url) VALUES ($1, decode('00','hex'), 'active', '')`,
		flows.DemoMerchantID)

	if err := CheckDemoDatabase(context.Background(), pool); err != nil {
		t.Fatalf("the real migrated schema refused the demo world: %v", err)
	}

	// One planted violation, refused by name: the one org becomes a real
	// dealer's (the guard checks the count before the slug, so the plant
	// replaces rather than adds).
	mustExecBoot(t, pool, `UPDATE org SET slug = $1`, "real-dealer")
	err := CheckDemoDatabase(context.Background(), pool)
	if err == nil {
		t.Fatal("the guard accepted a planted violation on the real schema")
	}
	if !strings.Contains(err.Error(), "real-dealer") {
		t.Fatalf("refusal %q does not pin its reason (the planted non-demo org)", err)
	}
}

// applyMigrationChain applies the frozen set the way the migrate role
// does: glob order, one whole-file statement string per file, applied
// exactly once. It writes the same bookkeeping rows the migrate role
// writes, so a fresh scratch database ends in the state serve would find.
func applyMigrationChain(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	dir := filepath.Join("..", "..", "..", "db", "migrations")
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no migration files under %s (err=%v)", dir, err)
	}
	sort.Strings(files)
	mustExecBoot(t, pool, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`)
	for _, f := range files {
		version := strings.TrimSuffix(filepath.Base(f), ".sql")
		var exists bool
		if err := pool.QueryRow(context.Background(),
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&exists); err != nil {
			t.Fatalf("migration bookkeeping: %v", err)
		}
		if exists {
			continue
		}
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if _, err := pool.Exec(context.Background(), string(body)); err != nil {
			t.Fatalf("apply %s: %v", filepath.Base(f), err)
		}
		mustExecBoot(t, pool, `INSERT INTO schema_migrations(version) VALUES ($1)`, version)
	}
}

func mustExecBoot(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec: %v", err)
	}
}

// openPlainPool opens a pool without applying the hand-built fixture:
// this leg builds its world from the real migration chain instead.
func openPlainPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
