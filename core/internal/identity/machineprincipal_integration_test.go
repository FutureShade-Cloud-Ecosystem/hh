//go:build integration

package identity

// The machine principal record and its resolver (HH-LOAD-1 LD-2):
// migration 0321's table read through the resolver a verified machine
// token drives. The rows are invented fixtures inserted and removed by
// this test alone; no dealer value appears. The suite gates on
// DATABASE_URL on the command line and skips politely without it, the
// same contract every integration file in this repository carries.

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func machineTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, integration tests skipped")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// insertMachineFixture inserts one org anchor row and two machine
// principals (one enabled, one disabled) and removes all three on every
// exit path. It returns the two principal ids.
func insertMachineFixture(t *testing.T, pool *pgxpool.Pool) (string, string) {
	t.Helper()
	ctx := context.Background()
	const orgSlug = "fixture-machine-org"
	var orgID string
	err := pool.QueryRow(ctx,
		`INSERT INTO org (slug, name) VALUES ($1, $2) RETURNING id::text`,
		orgSlug, "Fixture Machine Org").Scan(&orgID)
	if err != nil {
		t.Fatalf("insert org: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM machine_principal WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM org WHERE id = $1`, orgID)
	})
	var enabledID, disabledID string
	err = pool.QueryRow(ctx,
		`INSERT INTO machine_principal (org_id, name, enabled, doors)
		 VALUES ($1, $2, TRUE, $3) RETURNING id::text`,
		orgID, "fixture mover one", []string{"loadUpsertUnits", "loadGetBatch"}).Scan(&enabledID)
	if err != nil {
		t.Fatalf("insert enabled principal: %v", err)
	}
	err = pool.QueryRow(ctx,
		`INSERT INTO machine_principal (org_id, name, enabled, doors)
		 VALUES ($1, $2, FALSE, $3) RETURNING id::text`,
		orgID, "fixture mover two", []string{"loadUpsertUnits"}).Scan(&disabledID)
	if err != nil {
		t.Fatalf("insert disabled principal: %v", err)
	}
	return enabledID, disabledID
}

// TestMachinePrincipalResolverLoadsTheRecord proves the resolver's read:
// every column the gate decides on (org, enabled, the door list) comes
// back from the table exactly as written, for the enabled and the
// disabled row alike.
func TestMachinePrincipalResolverLoadsTheRecord(t *testing.T) {
	pool := machineTestPool(t)
	resolver, err := NewMachinePrincipalResolver(pool)
	if err != nil {
		t.Fatalf("NewMachinePrincipalResolver: %v", err)
	}
	enabledID, disabledID := insertMachineFixture(t, pool)
	ctx := context.Background()

	var orgID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM org WHERE slug = 'fixture-machine-org'`).Scan(&orgID); err != nil {
		t.Fatalf("read org back: %v", err)
	}

	got, ok, err := resolver.MachinePrincipal(ctx, enabledID)
	if err != nil || !ok {
		t.Fatalf("enabled principal: ok=%v err=%v", ok, err)
	}
	if got.ID != enabledID || got.OrgID != orgID || got.Name != "fixture mover one" || !got.Enabled {
		t.Fatalf("enabled principal read back as %+v", got)
	}
	if len(got.Doors) != 2 || got.Doors[0] != "loadUpsertUnits" || got.Doors[1] != "loadGetBatch" {
		t.Fatalf("enabled principal doors read back as %v", got.Doors)
	}

	disabled, ok, err := resolver.MachinePrincipal(ctx, disabledID)
	if err != nil || !ok {
		t.Fatalf("disabled principal: ok=%v err=%v", ok, err)
	}
	if disabled.Enabled {
		t.Fatal("the disabled principal read back as enabled")
	}
	if disabled.OrgID != orgID {
		t.Fatalf("disabled principal org read back as %s", disabled.OrgID)
	}
}

// TestMachinePrincipalResolverMisses: a subject naming no row is a miss,
// not an error, and a subject that is not even a uuid is a miss too, so
// the gate's 403 body answers both without an error path.
func TestMachinePrincipalResolverMisses(t *testing.T) {
	pool := machineTestPool(t)
	resolver, err := NewMachinePrincipalResolver(pool)
	if err != nil {
		t.Fatalf("NewMachinePrincipalResolver: %v", err)
	}
	ctx := context.Background()
	stranger := uuid.NewString()
	if _, ok, err := resolver.MachinePrincipal(ctx, stranger); err != nil || ok {
		t.Fatalf("unknown principal id: ok=%v err=%v", ok, err)
	}
	if _, ok, err := resolver.MachinePrincipal(ctx, "not-a-uuid"); err != nil || ok {
		t.Fatalf("non uuid subject: ok=%v err=%v", ok, err)
	}
}

// TestMachinePrincipalResolverConstruction: the resolver refuses a nil
// pool at construction, never at first read.
func TestMachinePrincipalResolverConstruction(t *testing.T) {
	if _, err := NewMachinePrincipalResolver(nil); err == nil {
		t.Fatal("a nil pool constructed a machine principal resolver")
	}
}
